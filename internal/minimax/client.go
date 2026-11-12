// Package minimax implements MiniMax Code CN (agent.minimax.cn) account
// management and daily sign-in:
//   - OAuth 设备授权（account.minimax.cn/oauth2/*，RFC 8628 + PKCE）；
//   - agent.minimax.cn 业务接口（签到 / 资料）按桌面端的 yy / x-signature 签名；
//   - www.minimaxi.com 积分查询仅需 Bearer。
//
// 抓包与实现依据见 README「MiniMax 签到接入」。
package minimax

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// 可在测试中覆盖的目标地址（生产为官方域名）。
var (
	agentBase   = "https://agent.minimax.cn"
	creditBase  = "https://www.minimaxi.com"
	accountBase = "https://account.minimax.cn"
)

const (
	oauthClientID   = "mcode-public"
	oauthScope      = "agent.default"
	oauthAudience   = "agent-backend"
	deviceGrantType = "urn:ietf:params:oauth:grant-type:device_code"

	signatureSalt = "I*7Cf%WZ#S&%1RlZJ&C2"
	yySuffix      = "ooui"
	bizID         = "3"
	appID         = "3001"
	versionCode   = "22201"
	desktopVer    = "3.1.14"
	desktopUA     = "MiniMaxAgent Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) MiniMax/3.1.14 Chrome/148.0.7778.280 Safari/537.36"
)

var china = time.FixedZone("Asia/Shanghai", 8*3600)

// RewardPeriod 是签到的归属日（北京时间当天，无轮次偏移）。
func RewardPeriod(now time.Time) string {
	return now.In(china).Format("2006-01-02")
}

type Client struct {
	HTTP *http.Client
}

func NewClient() *Client {
	return &Client{HTTP: &http.Client{Timeout: 25 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}

const (
	AuthAuto   = ""
	AuthCookie = "cookie"
	AuthBearer = "bearer"
)

// detectAuthKind 依据凭证形态推断鉴权方式：形如 "k=v; k2=v2" 视为
// Cookie，其余（含显式 Bearer 前缀、JWT、普通令牌）视为 Bearer。
func detectAuthKind(credential string) string {
	if len(credential) > 7 && strings.EqualFold(credential[:7], "bearer ") {
		return AuthBearer
	}
	if strings.Contains(credential, "=") {
		return AuthCookie
	}
	return AuthBearer
}

// applyCredential 按账号凭证类型写入鉴权头（Cookie 或 Bearer）。
func applyCredential(req *http.Request, credential, authKind string) {
	cred := strings.TrimSpace(credential)
	kind := authKind
	if kind == AuthAuto {
		kind = detectAuthKind(cred)
	}
	if kind == AuthCookie {
		req.Header.Set("Cookie", cred)
		return
	}
	if len(cred) > 7 && strings.EqualFold(cred[:7], "bearer ") {
		cred = strings.TrimSpace(cred[7:])
	}
	req.Header.Set("Authorization", "Bearer "+cred)
}

// userIDClaim 从访问令牌的 JWT 载荷提取 account_id / sub 作为 user_id。
func userIDClaim(token string) string {
	claims := jwtPayload(strings.TrimSpace(token))
	if claims == nil {
		return ""
	}
	if id := text(claims, "account_id"); id != "" {
		return id
	}
	return text(claims, "sub")
}

// jwtPayload 解出 JWT 载荷；非 JWT 或解析失败返回 nil（容忍）。
func jwtPayload(token string) map[string]json.RawMessage {
	parts := strings.Split(token, ".")
	if len(parts) != 3 || parts[1] == "" {
		return nil
	}
	raw, err := base64RawURL.DecodeString(parts[1])
	if err != nil {
		return nil
	}
	var claims map[string]json.RawMessage
	if json.Unmarshal(raw, &claims) != nil {
		return nil
	}
	return claims
}

var base64RawURL = base64.RawURLEncoding

// nowFunc 供测试固定签名时间。
var nowFunc = time.Now

type responseError struct{ code int }

func (e *responseError) Error() string {
	if e.code == 401 || e.code == 403 {
		return "MiniMax 登录凭证失效或无权访问，请重新授权"
	}
	return fmt.Sprintf("MiniMax 请求未成功（HTTP %d），请稍后检查网络", e.code)
}

// signedRequest 向 agent.minimax.cn 发送带 yy / x-signature 签名的业务请求，
// 参数与签名算法与桌面端主进程 buildPublicParams / buildHeaders 一致。
func (c *Client) signedRequest(ctx context.Context, method, path string, a Account, body []byte) (map[string]json.RawMessage, error) {
	now := nowFunc()
	token := strings.TrimSpace(a.Credential)
	userID := userIDClaim(token)
	// 桌面端参数为 getTimezoneOffset() * -60（UTC+8 → 28800），即本初子午线
	// 以东为正，Go 的 Zone() 秒偏移与其符号一致，直接使用秒值。
	_, zoneSeconds := now.Zone()
	// 公共参数必须保持桌面端的插入顺序（服务器按原始顺序重算签名），
	// 不能用 url.Values.Encode()——那会按键名排序。
	values := []struct{ K, V string }{
		{"device_platform", "web"},
		{"biz_id", bizID},
		{"app_id", appID},
		{"version_code", versionCode},
		{"unix", strconv.FormatInt(now.UnixMilli(), 10)},
		{"timezone_offset", strconv.Itoa(zoneSeconds)},
		{"sys_language", "zh"},
		{"lang", "zh"},
		{"device_id", "0"},
	}
	if userID != "" {
		values = append(values, struct{ K, V string }{"user_id", userID})
	}
	values = append(values,
		struct{ K, V string }{"client", "desktop"},
		struct{ K, V string }{"region", "cn"},
		struct{ K, V string }{"is_desktop", "1"},
		struct{ K, V string }{"desktop_version", desktopVer},
	)
	var query strings.Builder
	for i, p := range values {
		if i > 0 {
			query.WriteByte('&')
		}
		query.WriteString(p.K)
		query.WriteByte('=')
		query.WriteString(p.V)
	}

	signedPath := path + "?" + query.String()
	second := strconv.FormatInt(now.Unix(), 10)
	ms := strconv.FormatInt(now.UnixMilli(), 10)

	req, err := http.NewRequestWithContext(ctx, method, agentBase+signedPath, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("MiniMax 请求参数无效")
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", desktopUA)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("yy", yyHeader(signedPath, ms, body))
	req.Header.Set("x-timestamp", second)
	req.Header.Set("x-signature", md5Hex(second+signatureSalt+string(body)))
	req.Header.Set("Origin", agentBase)
	req.Header.Set("Referer", agentBase+"/")
	applyCredential(req, token, a.AuthKind)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("MiniMax 请求失败，请检查网络")
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, &responseError{resp.StatusCode}
	}
	return decodeEnvelope(resp.Body)
}

// yyHeader 复刻桌面端：GET 用 "{}"，POST 用发送的 JSON 体；时间用毫秒字符串。
func yyHeader(signedPath, unixMilli string, body []byte) string {
	yyBody := string(body)
	if yyBody == "" {
		yyBody = "{}"
	}
	return md5Hex(url.QueryEscape(signedPath) + "_" + yyBody + md5Hex(unixMilli) + yySuffix)
}

func md5Hex(s string) string {
	sum := md5.Sum([]byte(s))
	return hex.EncodeToString(sum[:])
}

// decodeEnvelope 解析 base_resp 信封；data 为对象时返回 data，否则返回根对象。
func decodeEnvelope(body io.Reader) (map[string]json.RawMessage, error) {
	raw, err := io.ReadAll(io.LimitReader(body, (2<<20)+1))
	if err != nil || len(raw) > 2<<20 {
		return nil, fmt.Errorf("MiniMax 响应读取失败或超过限制")
	}
	var root map[string]json.RawMessage
	if json.Unmarshal(raw, &root) != nil || root == nil {
		return nil, fmt.Errorf("MiniMax 响应格式无效")
	}
	// Never forward server errors: upstream messages may contain credential data.
	if code := object(root["base_resp"]); code != nil {
		if n, ok := number(code["status_code"]); ok && int(n) != 0 {
			return nil, fmt.Errorf("MiniMax 业务请求未成功（%d）", int(n))
		}
	} else if info := object(root["statusInfo"]); info != nil {
		if n, ok := number(info["code"]); ok && int(n) != 0 {
			return nil, fmt.Errorf("MiniMax 业务请求未成功（%d）", int(n))
		}
	} else if text(root, "errorCode", "error_code") != "" || string(root["success"]) == "false" {
		return nil, fmt.Errorf("MiniMax 业务请求未成功，请检查凭证")
	}
	if nested := object(root["data"]); nested != nil {
		return nested, nil
	}
	return root, nil
}

func object(raw json.RawMessage) map[string]json.RawMessage {
	var v map[string]json.RawMessage
	_ = json.Unmarshal(raw, &v)
	return v
}
func text(m map[string]json.RawMessage, keys ...string) string {
	for _, key := range keys {
		var s string
		if json.Unmarshal(m[key], &s) == nil && s != "" {
			return s
		}
	}
	return ""
}
func number(raw json.RawMessage) (float64, bool) {
	if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return 0, false
	}
	var n float64
	if json.Unmarshal(raw, &n) != nil {
		var s string
		if json.Unmarshal(raw, &s) != nil {
			return 0, false
		}
		var err error
		n, err = strconv.ParseFloat(s, 64)
		if err != nil {
			return 0, false
		}
	}
	return n, !math.IsNaN(n) && !math.IsInf(n, 0)
}
func firstNumber(m map[string]json.RawMessage, keys ...string) (float64, bool) {
	for _, key := range keys {
		if n, ok := number(m[key]); ok {
			return n, true
		}
	}
	return 0, false
}

// cleanCredential 允许 Cookie 分号后的空格，但拒绝换行等注入字符。
func cleanCredential(s string) bool {
	return s != "" && len(s) <= 8192 && !strings.ContainsAny(s, "\r\n\t")
}

// signinDay 面板中某一天的签到状态（桌面端枚举）：
// 1=未开始 2=可领取 3=已领取 4=不可用。
const signinDayClaimed = 3

type signinPanel struct {
	Scene int         `json:"scene"`
	Days  []signinDay `json:"days"`
}

type signinDay struct {
	DayNo       int     `json:"day_no"`
	Points      float64 `json:"points"`
	BonusPoints float64 `json:"bonus_points"`
	IsToday     bool    `json:"is_today"`
	Status      int     `json:"status"`
}

func (p signinPanel) today() *signinDay {
	for i := range p.Days {
		if p.Days[i].IsToday {
			return &p.Days[i]
		}
	}
	return nil
}

func (c *Client) signinStatus(ctx context.Context, a *Account) (signinPanel, error) {
	root, err := c.signedRequest(ctx, "GET", "/minimax-cloud/api/v1/signin/status", *a, nil)
	if err != nil {
		return signinPanel{}, err
	}
	raw, err := json.Marshal(root)
	if err != nil {
		return signinPanel{}, fmt.Errorf("MiniMax 签到响应解析失败")
	}
	var panel signinPanel
	if json.Unmarshal(raw, &panel) != nil || len(panel.Days) == 0 {
		return signinPanel{}, fmt.Errorf("MiniMax 签到面板为空或格式变更，请在客户端查看")
	}
	return panel, nil
}

func (c *Client) identify(ctx context.Context, a *Account) error {
	// 身份优先取自令牌 JWT（account_id / sub），与桌面端 fetchIdentity 一致；
	// 再尽力回读 /v1/api/user/info 补昵称。
	token := strings.TrimSpace(a.Credential)
	if a.AccountUID == "" {
		if uid := userIDClaim(token); uid != "" {
			a.AccountUID = uid
		}
	}
	root, err := c.signedRequest(ctx, "GET", "/v1/api/user/info", *a, nil)
	if err != nil {
		var upstream *responseError
		if errors.As(err, &upstream) && (upstream.code == 404 || upstream.code == 405) {
			return nil // 资料端点不可用不算失败，身份已由 JWT 提供
		}
		if a.AccountUID != "" {
			return nil // 已有身份时资料拉取失败仅降级
		}
		return err
	}
	// 响应为 data.userInfo（realUserID 为数字主账号 ID），令牌不是 JWT 时身份只能来自这里。
	profile := object(root["userInfo"])
	if profile == nil {
		profile = root
	}
	uid := text(profile, "realUserID", "real_user_id", "uid", "userId", "user_id", "accountId", "account_id", "id")
	if uid != "" && a.AccountUID != "" && a.AccountUID != uid {
		return fmt.Errorf("MiniMax 凭证账号不一致，已拒绝更新")
	}
	if uid != "" {
		a.AccountUID = uid
	}
	if name := text(profile, "name", "nickname", "nickName", "displayName", "username", "email", "subUserName"); name != "" {
		a.Nickname = name
	}
	return nil
}

// creditDetail 积分桶（credit_type：1=购买 2=签到赠予）；金额为字符串。
type creditDetail struct {
	CreditType      int     `json:"credit_type"`
	GrantedAmount   string  `json:"granted_amount"`
	RemainingAmount string  `json:"remaining_amount"`
	ConsumedAmount  string  `json:"consumed_amount"`
	ExpireAtMs      float64 `json:"expire_at_ms"`
}

// balance 以 /minimax-cloud/api/v1/credit/details 为主源（购买 + 签到积分
// 汇总，与客户端「积分 0 + 2,624」一致），失败时回退 token_plan_credit。
func (c *Client) balance(ctx context.Context, a *Account) error {
	err := c.creditDetailsBalance(ctx, a)
	if err == nil {
		return nil
	}
	var probeErr error
	if !errors.As(err, new(*responseError)) && !isBusinessError(err) {
		probeErr = err // 网络或签名问题直接报错
	}
	if e := c.legacyBalance(ctx, a); e != nil {
		if probeErr != nil {
			return probeErr
		}
		return e
	}
	return nil
}

func isBusinessError(err error) bool {
	return err != nil && strings.Contains(err.Error(), "业务请求未成功")
}

func (c *Client) creditDetailsBalance(ctx context.Context, a *Account) error {
	root, err := c.signedRequest(ctx, "GET", "/minimax-cloud/api/v1/credit/details", *a, nil)
	if err != nil {
		return err
	}
	raw, err := json.Marshal(root["details"])
	if err != nil {
		return fmt.Errorf("MiniMax 积分明细解析失败")
	}
	var items []creditDetail
	if json.Unmarshal(raw, &items) != nil {
		return fmt.Errorf("MiniMax 积分明细格式变更，请核对返回")
	}
	remaining, granted := 0.0, 0.0
	for _, item := range items {
		if n, ok := number(json.RawMessage(item.RemainingAmount)); ok {
			remaining += n
		}
		if n, ok := number(json.RawMessage(item.GrantedAmount)); ok {
			granted += n
		}
	}
	a.Credits, a.Total, a.BalanceKnown, a.BalanceUpdatedAt = remaining, granted, true, time.Now()
	return nil
}

// legacyBalance 使用 token_plan_credit（字段在根级，仅覆盖购买积分）。
func (c *Client) legacyBalance(ctx context.Context, a *Account) error {
	req, err := http.NewRequestWithContext(ctx, "GET", creditBase+"/backend/account/token_plan_credit?business=Agent&device_platform=desktop", nil)
	if err != nil {
		return fmt.Errorf("MiniMax 请求参数无效")
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", desktopUA)
	applyCredential(req, a.Credential, a.AuthKind)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("MiniMax 请求失败，请检查网络")
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &responseError{resp.StatusCode}
	}
	root, err := decodeEnvelope(resp.Body)
	if err != nil {
		return err
	}
	remaining, ok := firstNumber(root, "remaining_credits", "remain_credits", "credits", "points", "balance", "remaining", "remain", "left", "leftQuota", "remainingQuota", "remain_credit", "remainCredit", "credit")
	total, hasTotal := firstNumber(root, "total_credits", "total", "creditsTotal", "totalQuota", "quota", "total_credit", "totalCredit")
	if !ok && hasTotal {
		if used, okUsed := firstNumber(root, "used_credits", "used", "cost", "used_credit", "usedCredit"); okUsed {
			remaining, ok = math.Max(0, total-used), true
		}
	}
	if !ok {
		return fmt.Errorf("MiniMax 余额响应未包含可识别的余额字段，请核对返回格式")
	}
	a.Credits, a.Total, a.BalanceKnown, a.BalanceUpdatedAt = remaining, total, true, time.Now()
	return nil
}

func (c *Client) checkin(ctx context.Context, a *Account, claim bool) error {
	panel, err := c.signinStatus(ctx, a)
	if err != nil {
		return err
	}
	today := panel.today()
	if today == nil {
		return fmt.Errorf("MiniMax 签到面板未包含今日条目，请在客户端查看")
	}
	// 探测（claim=false）绝不领取，只回读状态。
	if !claim {
		if today.Status == signinDayClaimed {
			a.LastPeriod = RewardPeriod(time.Now())
		}
		return nil
	}
	if today.Status == signinDayClaimed {
		a.LastPeriod = RewardPeriod(time.Now())
		return nil
	}
	root, err := c.signedRequest(ctx, "POST", "/minimax-cloud/api/v1/signin/claim", *a, []byte("{}"))
	if err != nil {
		return err
	}
	result, _ := number(root["claim_result"])
	if result != 1 && result != 2 {
		return fmt.Errorf("MiniMax 签到领取结果未确认，请刷新状态，勿连续领取")
	}
	// 回读确认：以面板今日状态为准，绝不凭空推断成功。
	latest, err := c.signinStatus(ctx, a)
	if err != nil {
		return err
	}
	if day := latest.today(); day == nil || day.Status != signinDayClaimed {
		return fmt.Errorf("MiniMax 签到结果尚未确认，请刷新状态，勿连续领取")
	}
	a.LastPeriod = RewardPeriod(time.Now())
	return nil
}
