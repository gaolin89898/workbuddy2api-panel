// OAuth 设备授权（RFC 8628 + PKCE），对应 MiniMax Code 桌面端登录流程：
// POST /oauth2/device/code 签发 user_code，浏览器在 unified-login /
// oauth-authorize 页完成登录与授权，本端轮询 POST /oauth2/token 换取令牌。
// 这些端点不要求 yy / x-signature 签名，仅表单请求。
package minimax

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// 设备授权轮询结果；面板以单步轮询暴露给前端，前端按 interval 循环。
var (
	ErrAuthorizationPending = errors.New("等待浏览器授权完成")
	ErrSlowDown             = errors.New("轮询过快，稍后再试")
)

type DeviceAuthorization struct {
	DeviceCode              string
	CodeVerifier            string
	UserCode                string
	VerificationURI         string
	VerificationURIComplete string
	ExpiresIn               time.Duration
	Interval                time.Duration
	Created                 time.Time
}

// Expired reports whether the device code has passed its issued validity window.
func (a *DeviceAuthorization) Expired(now time.Time) bool {
	return a.ExpiresIn > 0 && now.Sub(a.Created) > a.ExpiresIn
}

type TokenGrant struct {
	AccessToken  string
	RefreshToken string
	TokenType    string
	ExpiresIn    time.Duration
	Scopes       []string
	Subject      string
	AccountID    string
}

func (g *TokenGrant) Expiry(now time.Time) time.Time {
	if g.ExpiresIn <= 0 {
		return time.Time{}
	}
	return now.Add(g.ExpiresIn)
}

// UID 优先取 JWT 的 account_id，其次 sub，作为账号稳定身份。
func (g *TokenGrant) UID() string {
	if g.AccountID != "" {
		return g.AccountID
	}
	return g.Subject
}

// StartDeviceAuthorization 向 account.minimax.cn 申请设备码与 user_code。
func (c *Client) StartDeviceAuthorization(ctx context.Context) (*DeviceAuthorization, error) {
	verifierRaw := make([]byte, 32)
	if _, err := rand.Read(verifierRaw); err != nil {
		return nil, fmt.Errorf("无法创建 MiniMax 授权会话")
	}
	verifier := base64.RawURLEncoding.EncodeToString(verifierRaw)
	challengeSum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(challengeSum[:])
	form := url.Values{
		"client_id":             {oauthClientID},
		"scope":                 {oauthScope},
		"audience":              {oauthAudience},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
	}
	root, err := c.postForm(ctx, accountBase+"/oauth2/device/code", form)
	if err != nil {
		return nil, err
	}
	userCode := formText(root, "user_code")
	deviceCode := formText(root, "device_code")
	verification := formText(root, "verification_uri")
	if verification == "" {
		verification = formText(root, "verification_url")
	}
	if userCode == "" || deviceCode == "" || verification == "" {
		return nil, fmt.Errorf("MiniMax 设备授权响应缺少必要字段")
	}
	a := &DeviceAuthorization{
		DeviceCode:              deviceCode,
		CodeVerifier:            verifier,
		UserCode:                userCode,
		VerificationURI:         verification,
		VerificationURIComplete: formText(root, "verification_uri_complete"),
		Created:                 time.Now(),
		ExpiresIn:               300 * time.Second,
		Interval:                3 * time.Second,
	}
	if n := formNumber(root, "expires_in"); n > 0 {
		a.ExpiresIn = time.Duration(n * float64(time.Second))
	}
	if n := formNumber(root, "interval"); n > 0 {
		a.Interval = time.Duration(n * float64(time.Second))
	}
	return a, nil
}

// PollDeviceToken 执行一次令牌轮询；pending / slow_down 以可识别错误返回，
// 由调用方按节奏重试，绝不自动重放。
func (c *Client) PollDeviceToken(ctx context.Context, a *DeviceAuthorization) (*TokenGrant, error) {
	if a == nil {
		return nil, fmt.Errorf("MiniMax 授权会话无效")
	}
	if a.Expired(time.Now()) {
		return nil, fmt.Errorf("MiniMax 授权已过期，请重新获取授权")
	}
	form := url.Values{
		"grant_type":    {deviceGrantType},
		"device_code":   {a.DeviceCode},
		"client_id":     {oauthClientID},
		"code_verifier": {a.CodeVerifier},
	}
	root, status, err := c.postFormAllowError(ctx, accountBase+"/oauth2/token", form)
	if err != nil {
		return nil, err
	}
	if status == http.StatusOK {
		if formText(root, "status") == "pending" {
			return nil, ErrAuthorizationPending
		}
		return parseTokenGrant(root)
	}
	switch formText(root, "error") {
	case "authorization_pending":
		return nil, ErrAuthorizationPending
	case "slow_down":
		return nil, ErrSlowDown
	case "expired_token", "expired":
		return nil, fmt.Errorf("MiniMax 授权已过期，请重新获取授权")
	case "access_denied", "denied":
		return nil, fmt.Errorf("浏览器端已拒绝本次 MiniMax 授权")
	}
	return nil, fmt.Errorf("MiniMax 授权轮询未成功（HTTP %d）", status)
}

// RefreshToken 用刷新令牌换取新的访问令牌。
func (c *Client) RefreshToken(ctx context.Context, refreshToken string) (*TokenGrant, error) {
	if !cleanCredential(refreshToken) {
		return nil, fmt.Errorf("MiniMax 刷新凭证无效，请重新授权")
	}
	form := url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {refreshToken},
		"client_id":     {oauthClientID},
		"scope":         {oauthScope},
		"audience":      {oauthAudience},
	}
	root, status, err := c.postFormAllowError(ctx, accountBase+"/oauth2/token", form)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("MiniMax 凭证已过期或刷新失败（HTTP %d），请重新授权", status)
	}
	return parseTokenGrant(root)
}

func parseTokenGrant(root map[string]json.RawMessage) (*TokenGrant, error) {
	access := formText(root, "access_token")
	refresh := formText(root, "refresh_token")
	tokenType := formText(root, "token_type")
	if access == "" || refresh == "" || !strings.EqualFold(tokenType, "Bearer") {
		return nil, fmt.Errorf("MiniMax 授权响应缺少令牌")
	}
	grant := &TokenGrant{AccessToken: access, RefreshToken: refresh, TokenType: "Bearer"}
	if n := formNumber(root, "expires_in"); n > 0 {
		grant.ExpiresIn = time.Duration(n * float64(time.Second))
	}
	if scope := formText(root, "scope"); scope != "" {
		grant.Scopes = strings.Fields(scope)
	}
	if claims := jwtPayload(access); claims != nil {
		grant.Subject = formText(claims, "sub")
		grant.AccountID = formText(claims, "account_id")
	}
	return grant, nil
}

func (c *Client) postForm(ctx context.Context, endpoint string, form url.Values) (map[string]json.RawMessage, error) {
	root, status, err := c.postFormAllowError(ctx, endpoint, form)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("MiniMax 授权请求未成功（HTTP %d）", status)
	}
	return root, nil
}

// postFormAllowError 保留 4xx 响应体（OAuth 错误在 400 中携带 error 字段）。
func (c *Client) postFormAllowError(ctx context.Context, endpoint string, form url.Values) (map[string]json.RawMessage, int, error) {
	req, err := http.NewRequestWithContext(ctx, "POST", endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, 0, fmt.Errorf("MiniMax 授权请求无效")
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("MiniMax 授权请求失败，请检查网络")
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, (1<<20)+1))
	if err != nil || len(raw) > 1<<20 {
		return nil, resp.StatusCode, fmt.Errorf("MiniMax 授权响应读取失败")
	}
	var root map[string]json.RawMessage
	if json.Unmarshal(raw, &root) != nil {
		root = nil
	}
	return root, resp.StatusCode, nil
}

func formText(m map[string]json.RawMessage, keys ...string) string {
	return text(m, keys...)
}

func formNumber(m map[string]json.RawMessage, keys ...string) float64 {
	n, _ := firstNumber(m, keys...)
	return n
}

func rawText(m map[string]json.RawMessage, keys ...string) string {
	return text(m, keys...)
}
