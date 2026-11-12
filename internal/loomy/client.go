// Package loomy implements Loomy（讯飞「AI 工作搭子」桌面端）账号管理与每日
// 宠物打工奖励领取：
//   - 积分 API（loomyad.xunfei.cn，桌面端 .env.prod 的 LOOMY_POINTS_BASE_URL）
//     无需签名，鉴权仅
//     自定义头 token=<session>，业务信封 {code,desc,data}（000000 为成功）；
//   - 宠物打工奖励按当日实际使用量分档发放（tier_1/2/3 阈值 60/100/150），
//     面板只补领本机桌面端已达成、尚未领取的档位（忠实补领，不越权申报）；
//   - 会话复用本机 Loomy 桌面端登录态（auth-session.json），桌面端之外无法
//     自行登录（短信 + HMAC 签名）。
//
// 协议依据：Loomy 桌面端 resources/app.asar.unpacked/electron/points-service.js
// 与 pet/pet-work-service.js（未压缩主进程源码），详见 README「Loomy 签到接入」。
package loomy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// 可在测试中覆盖的目标地址（生产为官方域名）。桌面端通过加密的
// resources/.env.prod（LOOMY_POINTS_BASE_URL）注入生产地址；token 由生产
// 登录颁发，打到测试环境 ossptest 会返回业务码 100002「登录已失效」。
var pointsBase = "https://loomyad.xunfei.cn"

const desktopUA = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Loomy/0.9.39 Chrome/124.0.0.0 Electron/31.7.7 Safari/537.36"

var china = time.FixedZone("Asia/Shanghai", 8*3600)

// RewardPeriod 是宠物打工奖励的归属日（服务端业务日，北京时间当天）。
func RewardPeriod(now time.Time) string {
	return now.In(china).Format("2006-01-02")
}

type Client struct {
	HTTP *http.Client
}

func NewClient() *Client {
	return &Client{HTTP: &http.Client{Timeout: 25 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}

// ErrAuthExpired 对应业务码 100002：session 缺失或已过期（桌面端此时也会清
// 会话强制重登）。
var ErrAuthExpired = errors.New("Loomy 登录已失效，请在 Loomy 桌面端重新登录后重新导入")

// apiError 携带业务码的信封错误，调用方按码翻译提示。
type apiError struct{ code string }

func (e *apiError) Error() string {
	return fmt.Sprintf("Loomy 业务请求未成功（%s）", e.code)
}

// responseError HTTP 状态层错误（积分 API 的业务错误走 HTTP 200 信封）。
type responseError struct{ code int }

func (e *responseError) Error() string {
	if e.code == 401 || e.code == 403 {
		return "Loomy 登录凭证失效或无权访问，请重新导入"
	}
	return fmt.Sprintf("Loomy 请求未成功（HTTP %d），请稍后检查网络", e.code)
}

func isBusinessError(err error) bool {
	var api *apiError
	return errors.As(err, &api)
}

// request 发送积分 API 请求并解析统一信封；data 为对象时返回 data。
func (c *Client) request(ctx context.Context, method, path, token string, body []byte) (map[string]json.RawMessage, error) {
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, pointsBase+path, reader)
	if err != nil {
		return nil, fmt.Errorf("Loomy 请求参数无效")
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", desktopUA)
	req.Header.Set("token", strings.TrimSpace(token))
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("Loomy 请求失败，请检查网络")
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, &responseError{resp.StatusCode}
	}
	return decodeEnvelope(resp.Body)
}

// decodeEnvelope 解析 Loomy 业务信封 {code, desc, data}；code=000000 视为
// 成功并解包 data，100002 为登录失效，其余为业务失败。绝不透传上游 desc，
// 避免把凭证等敏感内容带进面板状态。
func decodeEnvelope(body io.Reader) (map[string]json.RawMessage, error) {
	raw, err := io.ReadAll(io.LimitReader(body, (2<<20)+1))
	if err != nil || len(raw) > 2<<20 {
		return nil, fmt.Errorf("Loomy 响应读取失败或超过限制")
	}
	var root map[string]json.RawMessage
	if json.Unmarshal(raw, &root) != nil || root == nil {
		return nil, fmt.Errorf("Loomy 响应格式无效")
	}
	code := strings.TrimSpace(text(root, "code"))
	switch code {
	case "", "000000":
	case "100002":
		return nil, ErrAuthExpired
	default:
		return nil, &apiError{code}
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

func boolValue(m map[string]json.RawMessage, key string) (bool, bool) {
	var b bool
	if json.Unmarshal(m[key], &b) == nil {
		return b, true
	}
	return false, false
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

// cleanCredential 拒绝换行等注入字符（session 为十六进制串，长度有限）。
func cleanCredential(s string) bool {
	return s != "" && len(s) <= 8192 && !strings.ContainsAny(s, "\r\n\t")
}

// isBusinessDate 校验服务端业务日 YYYY-MM-DD。
func isBusinessDate(value string) bool {
	if len(value) != 10 {
		return false
	}
	t, err := time.ParseInLocation("2006-01-02", value, time.UTC)
	if err != nil {
		return false
	}
	return t.Format("2006-01-02") == value
}

// tier 宠物打工档位：threshold 分数 tier.Score 以上视为达成（升序）。
type tier struct {
	TierID string  `json:"tierId"`
	Score  float64 `json:"score"`
}

// grantInfo 服务端最后一次真实发奖批次。
type grantInfo struct {
	GrantBatchID   string   `json:"grantBatchId"`
	GrantedPoints  float64  `json:"grantedPoints"`
	RewardedAt     float64  `json:"rewardedAt"`
	GrantedTierIDs []string `json:"grantedTierIds"`
}

// rewardStatus 服务端权威到账状态（快照与领奖响应均携带）。
type rewardStatus struct {
	EarnedPointsToday float64    `json:"earnedPointsToday"`
	DailyRewardCapped bool       `json:"dailyRewardCapped"`
	RewardedTierIDs   []string   `json:"rewardedTierIds"`
	LastGrant         *grantInfo `json:"lastGrant"`
}

// Snapshot 是 GET /api/v1/pet-work 返回的当日权威快照（配置 + 到账状态）。
type Snapshot struct {
	Enabled       bool
	BusinessDate  string
	ConfigVersion string
	Tiers         []tier // 按阈值升序
	Status        rewardStatus
}

// hasTier 报告 id 是否为已配置档位。
func (s Snapshot) hasTier(id string) bool {
	for _, t := range s.Tiers {
		if t.TierID == id {
			return true
		}
	}
	return false
}

// normalizeTiers 校验并按阈值升序整理服务端档位列表。
func normalizeTiers(raw json.RawMessage) ([]tier, error) {
	var items []tier
	if json.Unmarshal(raw, &items) != nil {
		return nil, fmt.Errorf("Loomy 宠物打工档位格式无效")
	}
	if len(items) == 0 {
		return nil, fmt.Errorf("Loomy 宠物打工档位为空")
	}
	seen := map[string]bool{}
	for _, t := range items {
		if t.TierID == "" || t.Score <= 0 || seen[t.TierID] {
			return nil, fmt.Errorf("Loomy 宠物打工档位格式无效")
		}
		seen[t.TierID] = true
	}
	sortTiers(items)
	return items, nil
}

func sortTiers(items []tier) {
	for i := 1; i < len(items); i++ {
		for j := i; j > 0 && items[j].Score < items[j-1].Score; j-- {
			items[j], items[j-1] = items[j-1], items[j]
		}
	}
}

// normalizeStatus 校验服务端到账状态（镜像桌面端 normalizePetWorkStatus）。
func normalizeStatus(raw map[string]json.RawMessage, snap Snapshot) (rewardStatus, error) {
	if raw == nil {
		return rewardStatus{}, fmt.Errorf("Loomy 宠物打工状态缺失")
	}
	earned, ok := number(raw["earnedPointsToday"])
	if !ok || earned < 0 {
		return rewardStatus{}, fmt.Errorf("Loomy 宠物打工状态格式无效")
	}
	capped, ok := boolValue(raw, "dailyRewardCapped")
	if !ok {
		return rewardStatus{}, fmt.Errorf("Loomy 宠物打工状态格式无效")
	}
	var rewarded []string
	if json.Unmarshal(raw["rewardedTierIds"], &rewarded) != nil {
		return rewardStatus{}, fmt.Errorf("Loomy 宠物打工状态档位格式无效")
	}
	seen := map[string]bool{}
	for _, id := range rewarded {
		if !snap.hasTier(id) || seen[id] {
			return rewardStatus{}, fmt.Errorf("Loomy 宠物打工状态档位格式无效")
		}
		seen[id] = true
	}
	var last *grantInfo
	if object(raw["lastGrant"]) != nil {
		var g grantInfo
		if json.Unmarshal(raw["lastGrant"], &g) != nil || g.GrantBatchID == "" || g.GrantedPoints < 0 {
			return rewardStatus{}, fmt.Errorf("Loomy 宠物打工最后发奖批次格式无效")
		}
		if len(g.GrantedTierIDs) == 0 {
			return rewardStatus{}, fmt.Errorf("Loomy 宠物打工最后发奖批次格式无效")
		}
		for _, id := range g.GrantedTierIDs {
			if !snap.hasTier(id) {
				return rewardStatus{}, fmt.Errorf("Loomy 宠物打工最后发奖批次格式无效")
			}
		}
		// 此分支内必存在 lastGrant：当日尚未发奖时两者都应为空。
		if len(rewarded) == 0 {
			return rewardStatus{}, fmt.Errorf("Loomy 宠物打工状态与最后发奖批次不一致")
		}
		if g.GrantedPoints > earned {
			return rewardStatus{}, fmt.Errorf("Loomy 宠物打工状态与最后发奖批次不一致")
		}
		last = &g
	} else if len(rewarded) > 0 {
		return rewardStatus{}, fmt.Errorf("Loomy 宠物打工状态与最后发奖批次不一致")
	}
	return rewardStatus{EarnedPointsToday: earned, DailyRewardCapped: capped, RewardedTierIDs: rewarded, LastGrant: last}, nil
}

// snapshot 拉取当日宠物打工权威快照。
func (c *Client) snapshot(ctx context.Context, token string) (Snapshot, error) {
	data, err := c.request(ctx, "GET", "/api/v1/pet-work", token, nil)
	if err != nil {
		return Snapshot{}, err
	}
	return normalizeSnapshot(data)
}

// normalizeSnapshot 归一化服务端统一快照（镜像桌面端 normalizePetWorkSnapshot：
// data 必含 config 与 status 两个对象，enabled/businessDate 在 data 根部）。
func normalizeSnapshot(data map[string]json.RawMessage) (Snapshot, error) {
	configData := object(data["config"])
	if configData == nil || object(data["status"]) == nil {
		return Snapshot{}, fmt.Errorf("Loomy 宠物打工快照格式无效")
	}
	enabled, ok := boolValue(data, "enabled")
	if !ok {
		return Snapshot{}, fmt.Errorf("Loomy 宠物打工快照格式无效")
	}
	businessDate := text(data, "businessDate")
	if !isBusinessDate(businessDate) {
		return Snapshot{}, fmt.Errorf("Loomy 宠物打工快照业务日无效")
	}
	configVersion := text(configData, "configVersion", "version")
	if configVersion == "" {
		return Snapshot{}, fmt.Errorf("Loomy 宠物打工配置版本无效")
	}
	tiers, err := normalizeTiers(configData["tiers"])
	if err != nil {
		return Snapshot{}, err
	}
	snap := Snapshot{Enabled: enabled, BusinessDate: businessDate, ConfigVersion: configVersion, Tiers: tiers}
	status, err := normalizeStatus(object(data["status"]), snap)
	if err != nil {
		return Snapshot{}, err
	}
	snap.Status = status
	return snap, nil
}

// RewardResult 是 POST /api/v1/pet-work/rewards 的归一化结果。
type RewardResult struct {
	BusinessDate   string
	GrantBatchID   string
	Granted        bool
	Duplicate      bool
	GrantedPoints  float64
	GrantedTierIDs []string
	Status         rewardStatus
}

// claim 申请一个已达成档位的当日奖励（请求体只有服务端定义的三个字段）。
func (c *Client) claim(ctx context.Context, token string, snap Snapshot, tierID string) (RewardResult, error) {
	if !isBusinessDate(snap.BusinessDate) || tierID == "" || snap.ConfigVersion == "" {
		return RewardResult{}, fmt.Errorf("Loomy 奖励请求缺少有效业务日、档位或配置版本")
	}
	body, err := json.Marshal(map[string]string{"businessDate": snap.BusinessDate, "configVersion": snap.ConfigVersion, "reachedTierId": tierID})
	if err != nil {
		return RewardResult{}, fmt.Errorf("Loomy 奖励请求编码失败")
	}
	data, err := c.request(ctx, "POST", "/api/v1/pet-work/rewards", token, body)
	if err != nil {
		var api *apiError
		if errors.As(err, &api) {
			switch api.code {
			case "200028":
				return RewardResult{}, fmt.Errorf("该档位尚未达成或已达领取上限")
			case "200029":
				return RewardResult{}, fmt.Errorf("奖励状态与服务端快照不一致，请刷新后重试")
			}
		}
		return RewardResult{}, err
	}
	return normalizeReward(data, snap)
}

// normalizeReward 校验领奖响应（镜像桌面端 normalizeRewardPayload：granted 与
// duplicate 互斥且各自携带一致的批次信息）。
func normalizeReward(data map[string]json.RawMessage, snap Snapshot) (RewardResult, error) {
	if text(data, "businessDate") != snap.BusinessDate {
		return RewardResult{}, fmt.Errorf("Loomy 奖励响应业务日无效")
	}
	status, err := normalizeStatus(object(data["status"]), snap)
	if err != nil {
		return RewardResult{}, err
	}
	granted, okGranted := boolValue(data, "granted")
	duplicate, okDuplicate := boolValue(data, "duplicate")
	if !okGranted || !okDuplicate || granted == duplicate {
		return RewardResult{}, fmt.Errorf("Loomy 奖励响应结果无效")
	}
	points, okPoints := number(data["grantedPoints"])
	if !okPoints || points < 0 {
		return RewardResult{}, fmt.Errorf("Loomy 奖励响应积分无效")
	}
	batch := text(data, "grantBatchId")
	var tiers []string
	if json.Unmarshal(data["grantedTierIds"], &tiers) != nil {
		tiers = nil
	}
	if granted {
		if batch == "" || points <= 0 || len(tiers) == 0 {
			return RewardResult{}, fmt.Errorf("Loomy 奖励响应批次无效")
		}
		if status.LastGrant == nil || status.LastGrant.GrantBatchID != batch ||
			status.LastGrant.GrantedPoints != points || !equalStrings(status.LastGrant.GrantedTierIDs, tiers) {
			return RewardResult{}, fmt.Errorf("Loomy 奖励响应批次无效")
		}
	}
	if duplicate && (batch != "" || points != 0 || len(tiers) != 0) {
		return RewardResult{}, fmt.Errorf("Loomy 奖励重复响应无效")
	}
	for _, id := range tiers {
		if !snap.hasTier(id) {
			return RewardResult{}, fmt.Errorf("Loomy 奖励响应档位无效")
		}
	}
	return RewardResult{BusinessDate: snap.BusinessDate, GrantBatchID: batch, Granted: granted, Duplicate: duplicate, GrantedPoints: points, GrantedTierIDs: tiers, Status: status}, nil
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// balance 查询积分余额。个人口径取 /api/v2/points/records 响应根部的
// balance（永久积分）与 dailyBalance（每日赠送余额），与桌面端「积分余额：
// 永久 X 每日赠送 Y」同源；/api/v1/team-points/balance 仅对已切换团队的
// 账号有效，个人账号返回业务码 100001「当前未切换到团队」。
// 返回值：永久积分、每日赠送余额、错误。
func (c *Client) balance(ctx context.Context, token string) (float64, float64, error) {
	data, err := c.request(ctx, "GET", "/api/v2/points/records?direction=debit&granularity=chat&pageNo=1&pageSize=1", token, nil)
	if err != nil {
		return 0, 0, err
	}
	value, ok := firstNumber(data, "balance")
	if !ok {
		return 0, 0, fmt.Errorf("Loomy 余额响应未包含可识别的余额字段，请核对返回格式")
	}
	daily, _ := firstNumber(data, "dailyBalance")
	return value, daily, nil
}
