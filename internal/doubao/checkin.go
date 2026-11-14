package doubao

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// CheckinGroupCode 是"豆包工作"每日签到与任务奖励所在的成长任务组。客户端与
// 网页共用同一套 /alice/ug 协议，用网页会话 Cookie 即可完成签到。
const CheckinGroupCode = "pc_incentive_task_mvp_v3"

// checkinQuery 与网页端签到请求的查询参数保持一致。
const checkinQuery = "aid=497858&real_aid=497858&device_platform=web&doubao_device_platform=web&language=zh&region=CN&sys_region=CN&version_code=20800&pc_version=3.45.6&pkg_type=release_version&samantha_web=1&use-olympus-account=1"

// china 是豆包签到日期归属的时区；服务端 checkin_date 即北京时间当天。
var china = time.FixedZone("Asia/Shanghai", 8*3600)

// CheckinDay 返回北京时间当天日期，与服务端 checkin_date 同口径。
func CheckinDay(now time.Time) string { return now.In(china).Format("2006-01-02") }

// CheckinResult 是一次每日签到领取的回读结果。
type CheckinResult struct {
	CheckedIn      bool
	CheckinDate    string
	ContinuousDays int
	TotalDays      int
	Idempotent     bool
	RewardAmount   float64
	RewardType     string
}

// CheckinStatus 是任务组"每日签到"项与累计奖励的只读状态。
type CheckinStatus struct {
	CheckedInToday  bool
	LastCheckinDate string
	ContinuousDays  int
	TotalDays       int
	Points          float64
	RewardAmount    float64
}

func (c *Client) ugPost(ctx context.Context, a *Account, path, body string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.Base+path+"?"+checkinQuery, strings.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("豆包签到请求参数无效")
	}
	req.Header.Set("Cookie", a.Cookie)
	req.Header.Set("User-Agent", UserAgent)
	req.Header.Set("Origin", BaseURL)
	req.Header.Set("Referer", BaseURL+"/chat/")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Agw-Js-Conv", "str")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("豆包签到请求失败，请检查网络后重试")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
		return nil, fmt.Errorf("豆包签到请求失败（HTTP %d）", resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, (1<<20)+1))
	if err != nil || len(raw) > 1<<20 {
		return nil, fmt.Errorf("豆包签到响应读取失败")
	}
	return raw, nil
}

// Checkin 执行每日签到。服务端按天幂等：当天重复签到返回已签（idempotent），
// 不重复发放奖励。
func (c *Client) Checkin(ctx context.Context, a *Account) (CheckinResult, error) {
	raw, err := c.ugPost(ctx, a, "/alice/ug/campaign/landing/checkin", `{"scene":"`+CheckinGroupCode+`"}`)
	if err != nil {
		return CheckinResult{}, err
	}
	var root struct {
		Code *int   `json:"code"`
		Msg  string `json:"msg"`
		Data *struct {
			CheckedIn      bool            `json:"checked_in"`
			CheckinDate    string          `json:"checkin_date"`
			ContinuousDays int             `json:"continuous_days"`
			TotalDays      int             `json:"total_days"`
			Idempotent     bool            `json:"idempotent"`
			RewardAmount   json.RawMessage `json:"reward_amount"`
			RewardType     string          `json:"reward_type"`
		} `json:"data"`
	}
	if json.Unmarshal(raw, &root) != nil || root.Code == nil {
		return CheckinResult{}, fmt.Errorf("豆包签到响应格式无效")
	}
	if *root.Code != 0 {
		message := strings.TrimSpace(root.Msg)
		if message != "" {
			message = "：" + message
		}
		return CheckinResult{}, fmt.Errorf("豆包签到被平台拒绝（代码 %d）%s", *root.Code, message)
	}
	if root.Data == nil || !root.Data.CheckedIn {
		return CheckinResult{}, fmt.Errorf("豆包签到未完成，请稍后重试")
	}
	result := CheckinResult{
		CheckedIn:      true,
		CheckinDate:    root.Data.CheckinDate,
		ContinuousDays: root.Data.ContinuousDays,
		TotalDays:      root.Data.TotalDays,
		Idempotent:     root.Data.Idempotent,
		RewardType:     root.Data.RewardType,
	}
	if result.CheckinDate == "" {
		result.CheckinDate = CheckinDay(time.Now())
	}
	if amount, err := parseQuotaAmount(root.Data.RewardAmount); err == nil {
		result.RewardAmount = amount
	}
	return result, nil
}

// CheckinDesktopOnlyCard 平台把账号签到路由到桌面端时的落地页卡片代码；此时
// 网页协议领取被拒（代码 710010702），只能在豆包桌面端完成签到。
const CheckinDesktopOnlyCard = "web_pc_download_banner"

// CheckinLandingCard 回读签到落地页路由，返回第一张卡片的代码；无卡片（网页
// 可直接领取或已签）返回空串。
func (c *Client) CheckinLandingCard(ctx context.Context, a *Account) (string, error) {
	raw, err := c.ugPost(ctx, a, "/alice/ug/campaign/landing/get", `{"scene":"+CheckinGroupCode+","badge_counts":{"incentive":1}}`)
	if err != nil {
		return "", err
	}
	var root struct {
		Code *int `json:"code"`
		Data *struct {
			Cards []struct {
				CardCode string `json:"card_code"`
			} `json:"landing_cards"`
		} `json:"data"`
	}
	if json.Unmarshal(raw, &root) != nil || root.Code == nil || *root.Code != 0 || root.Data == nil {
		return "", fmt.Errorf("豆包签到落地页回读失败")
	}
	if len(root.Data.Cards) == 0 {
		return "", nil
	}
	return strings.TrimSpace(root.Data.Cards[0].CardCode), nil
}

// CheckinGroupStatus 只读查询任务组：每日签到状态与累计获得点数，不领取奖励。
func (c *Client) CheckinGroupStatus(ctx context.Context, a *Account) (CheckinStatus, error) {
	raw, err := c.ugPost(ctx, a, "/alice/ug/campaign/v3/detail/", `{"campaign_group_code":"`+CheckinGroupCode+`"}`)
	if err != nil {
		return CheckinStatus{}, err
	}
	var root struct {
		Code *int   `json:"code"`
		Msg  string `json:"msg"`
		Data *struct {
			Progress *struct {
				TotalAmount float64 `json:"total_amount"`
			} `json:"progress"`
			Tasks []struct {
				Kind         string `json:"task_kind"`
				DailyCheckin *struct {
					CheckedInToday  bool   `json:"checked_in_today"`
					LastCheckinDate string `json:"last_checkin_date"`
					ContinuousDays  int    `json:"continuous_days"`
					TotalDays       int    `json:"total_days"`
					Reward          *struct {
						RewardAmount json.RawMessage `json:"reward_amount"`
					} `json:"reward"`
				} `json:"daily_checkin"`
			} `json:"tasks"`
		} `json:"data"`
	}
	if json.Unmarshal(raw, &root) != nil || root.Code == nil {
		return CheckinStatus{}, fmt.Errorf("豆包签到状态响应格式无效")
	}
	if *root.Code != 0 {
		message := strings.TrimSpace(root.Msg)
		if message != "" {
			message = "：" + message
		}
		return CheckinStatus{}, fmt.Errorf("豆包签到状态被平台拒绝（代码 %d）%s", *root.Code, message)
	}
	if root.Data == nil {
		return CheckinStatus{}, fmt.Errorf("豆包签到状态响应缺少任务数据")
	}
	var points float64
	if root.Data.Progress != nil {
		points = root.Data.Progress.TotalAmount
	}
	status := CheckinStatus{Points: points}
	for _, task := range root.Data.Tasks {
		if task.Kind != "daily_signin" || task.DailyCheckin == nil {
			continue
		}
		status.CheckedInToday = task.DailyCheckin.CheckedInToday
		status.LastCheckinDate = task.DailyCheckin.LastCheckinDate
		status.ContinuousDays = task.DailyCheckin.ContinuousDays
		status.TotalDays = task.DailyCheckin.TotalDays
		if task.DailyCheckin.Reward != nil {
			if amount, err := parseQuotaAmount(task.DailyCheckin.Reward.RewardAmount); err == nil {
				status.RewardAmount = amount
			}
		}
		break
	}
	return status, nil
}
