// Package doubao manages doubao.com web sessions, independent of Volcengine API keys.
package doubao

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const BaseURL = "https://www.doubao.com"
const UserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"

type Client struct {
	HTTP *http.Client
	Base string // Fixed official endpoint in production; replaceable in tests.
}

func NewClient() *Client {
	return &Client{Base: BaseURL, HTTP: &http.Client{Timeout: 25 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}

// NormalizeCookie accepts a sessionid value or a copied Cookie header. Reject
// ambiguous sessions and control characters before constructing an HTTP header.
func NormalizeCookie(input string) (string, error) {
	input = strings.TrimSpace(input)
	input = strings.TrimSpace(strings.TrimPrefix(input, "Cookie:"))
	if input == "" || len(input) > 32<<10 {
		return "", fmt.Errorf("请填写豆包 sessionid 或 Cookie")
	}
	for _, c := range input {
		if c < 32 || c >= 127 {
			return "", fmt.Errorf("豆包凭证格式无效")
		}
	}
	if !strings.Contains(input, "=") && !strings.Contains(input, ";") {
		input = "sessionid=" + input
	}
	req := &http.Request{Header: http.Header{"Cookie": []string{input}}}
	cookies := req.Cookies()
	seen := map[string]bool{}
	session := ""
	parts := []string{}
	for _, c := range cookies {
		if seen[c.Name] {
			return "", fmt.Errorf("豆包 Cookie 含重复字段")
		}
		seen[c.Name] = true
		if c.Name == "sessionid" {
			session = c.Value
		}
		parts = append(parts, c.Name+"="+c.Value)
	}
	if session == "" || len(session) > 512 {
		return "", fmt.Errorf("豆包 Cookie 缺少有效 sessionid")
	}
	for _, c := range cookies {
		if c.Name == "sessionid_ss" && c.Value != session {
			return "", fmt.Errorf("豆包 Cookie 中的 sessionid 与 sessionid_ss 不一致")
		}
	}
	if !seen["sessionid_ss"] {
		parts = append(parts, "sessionid_ss="+session)
	}
	return strings.Join(parts, "; "), nil
}

// Inspect derives identity exclusively from the authenticated upstream response.
// Upstream bodies and errors are never reflected to callers (they may echo cookies).
func (c *Client) Inspect(ctx context.Context, a *Account) error {
	query := url.Values{"aid": {"497858"}, "account_sdk_source": {"web"}, "device_platform": {"web"}, "language": {"zh"}, "region": {"CN"}}
	req, err := http.NewRequestWithContext(ctx, "GET", c.Base+"/passport/account/info/v2/?"+query.Encode(), nil)
	if err != nil {
		return fmt.Errorf("豆包请求参数无效")
	}
	req.Header.Set("Cookie", a.Cookie)
	req.Header.Set("User-Agent", UserAgent)
	req.Header.Set("Origin", BaseURL)
	req.Header.Set("Referer", BaseURL+"/chat/")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("豆包账号验证失败，请检查网络后重试")
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("豆包账号验证失败（HTTP %d）", resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, (1<<20)+1))
	if err != nil || len(raw) > 1<<20 {
		return fmt.Errorf("豆包账号响应读取失败")
	}
	var root struct {
		Message string `json:"message"`
		Code    int    `json:"code"`
		Data    struct {
			UserID     json.Number `json:"user_id"`
			ScreenName string      `json:"screen_name"`
			Nickname   string      `json:"nickname"`
			ErrorCode  int         `json:"error_code"`
		} `json:"data"`
	}
	if json.Unmarshal(raw, &root) != nil {
		return fmt.Errorf("豆包账号响应格式无效")
	}
	uid := root.Data.UserID.String()
	if root.Code != 0 || root.Message == "error" || root.Data.ErrorCode != 0 || uid == "" || uid == "0" {
		return fmt.Errorf("豆包登录已失效，请在豆包重新登录后更新 Cookie")
	}
	if a.AccountUID != "" && a.AccountUID != uid {
		return fmt.Errorf("新凭证属于另一个豆包账号，请单独添加")
	}
	a.AccountUID = uid
	if a.Nickname == "" {
		a.Nickname = root.Data.ScreenName
		if a.Nickname == "" {
			a.Nickname = root.Data.Nickname
		}
	}
	a.VerifiedAt = time.Now()
	return nil
}
