package doubao

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Quota describes one allowance window from the subscription management page.
// Activity rewards and rolling subscription limits are separate allowances.
type Quota struct {
	Kind      string     `json:"kind"`
	Name      string     `json:"name"`
	Total     float64    `json:"total"`
	Used      float64    `json:"used"`
	Remaining float64    `json:"remaining"`
	StartsAt  *time.Time `json:"starts_at,omitempty"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
}

type Balance struct {
	Credits, Total float64 // Activity rewards only; rolling limits are not added.
	Known          bool
	Plan           string
	Quotas         []Quota
}

type quotaSection struct {
	Groups *[]struct {
		Feature string `json:"feature_group"`
		Name    string `json:"feature_group_name"`
		Windows []struct {
			Total     json.RawMessage `json:"total_amount"`
			Used      json.RawMessage `json:"used_amount"`
			Type      int             `json:"window_type"`
			StartTime int64           `json:"start_time"`
			EndTime   int64           `json:"end_time"`
		} `json:"window_limits"`
	} `json:"window_limit_groups"`
}

var quotaNumber = regexp.MustCompile(`^(?:[0-9]+|[0-9]{1,3}(?:,[0-9]{3})+)(?:\.[0-9]+)?$`)

func parseQuotaAmount(raw json.RawMessage) (float64, error) {
	value := string(raw)
	if strings.HasPrefix(value, `"`) {
		if json.Unmarshal(raw, &value) != nil {
			return 0, fmt.Errorf("豆包额度数值无效")
		}
	}
	if !quotaNumber.MatchString(value) {
		return 0, fmt.Errorf("豆包额度响应缺少有效数值")
	}
	n, err := strconv.ParseFloat(strings.ReplaceAll(value, ",", ""), 64)
	if err != nil || math.IsInf(n, 0) || math.IsNaN(n) || n > 1<<53-1 {
		return 0, fmt.Errorf("豆包额度数值超出有效范围")
	}
	return n, nil
}

func quotaTime(ms int64) (*time.Time, error) {
	if ms < 0 || ms > 253402300799999 {
		return nil, fmt.Errorf("豆包额度时间格式无效")
	}
	if ms == 0 {
		return nil, nil
	}
	t := time.UnixMilli(ms).UTC()
	return &t, nil
}

// Balance reads subscription quotas, not the separate video-generation points
// wallet. The official web_platform and version_code parameters enable exact
// amounts; without them the service can return percentages only.
func (c *Client) Balance(ctx context.Context, a *Account) (Balance, error) {
	query := url.Values{"aid": {"497858"}, "device_platform": {"web"}, "web_platform": {"browser"}, "version_code": {"20800"}, "language": {"zh"}, "region": {"CN"}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.Base+"/alice/commerce/sale/subscription/quota/summary/?"+query.Encode(), strings.NewReader(`{"product_line":"membership"}`))
	if err != nil {
		return Balance{}, fmt.Errorf("豆包额度请求参数无效")
	}
	req.Header.Set("Cookie", a.Cookie)
	req.Header.Set("User-Agent", UserAgent)
	req.Header.Set("Origin", BaseURL)
	req.Header.Set("Referer", BaseURL+"/chat/")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Agw-Js-Conv", "str")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return Balance{}, fmt.Errorf("豆包额度查询失败，请检查网络后重试")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Balance{}, fmt.Errorf("豆包额度查询失败（HTTP %d）", resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, (1<<20)+1))
	if err != nil || len(raw) > 1<<20 {
		return Balance{}, fmt.Errorf("豆包额度响应读取失败")
	}
	var root struct {
		Code *int `json:"code"`
		Data *struct {
			CurrentSubscription struct {
				Display struct {
					ShortName string `json:"short_name"`
				} `json:"display"`
			} `json:"current_subscription"`
			Activity     *quotaSection `json:"incentive_quota_section"`
			Subscription *quotaSection `json:"window_limit_section"`
			Enterprise   *quotaSection `json:"enterprise_window_limit_section"`
		} `json:"data"`
	}
	if json.Unmarshal(raw, &root) != nil || root.Code == nil {
		return Balance{}, fmt.Errorf("豆包额度响应格式无效")
	}
	if *root.Code != 0 {
		return Balance{}, fmt.Errorf("豆包额度查询被平台拒绝，请在官网检查登录状态后重试（代码 %d）", *root.Code)
	}
	if root.Data == nil || root.Data.Activity == nil || root.Data.Activity.Groups == nil {
		return Balance{}, fmt.Errorf("豆包额度响应缺少额度明细")
	}
	balance := Balance{Plan: root.Data.CurrentSubscription.Display.ShortName, Quotas: []Quota{}, Known: len(*root.Data.Activity.Groups) == 0}
	for _, section := range []struct {
		kind string
		data *quotaSection
	}{{"activity", root.Data.Activity}, {"subscription", root.Data.Subscription}, {"enterprise", root.Data.Enterprise}} {
		if section.data == nil || section.data.Groups == nil {
			continue
		}
		for _, group := range *section.data.Groups {
			for _, window := range group.Windows {
				total, err := parseQuotaAmount(window.Total)
				if err != nil {
					return Balance{}, err
				}
				used, err := parseQuotaAmount(window.Used)
				if err != nil {
					return Balance{}, err
				}
				starts, err := quotaTime(window.StartTime)
				if err != nil {
					return Balance{}, err
				}
				expires, err := quotaTime(window.EndTime)
				if err != nil {
					return Balance{}, err
				}
				name := group.Name
				if name == "" {
					switch section.kind {
					case "activity":
						name = "活动赠送额度"
					case "enterprise":
						name = "企业额度"
					default:
						name = "订阅额度"
						if window.Type == 2 {
							name = "近 7 天"
						}
					}
				}
				remaining := math.Max(0, total-used)
				if expires != nil && !expires.After(time.Now()) {
					remaining = 0
				}
				balance.Quotas = append(balance.Quotas, Quota{Kind: section.kind, Name: name, Total: total, Used: used, Remaining: remaining, StartsAt: starts, ExpiresAt: expires})
				if section.kind == "activity" && group.Feature == "doubao_activity_reward_quota" {
					// Multiple windows constrain the same reward pool. Keep the
					// tightest remaining limit instead of double counting it.
					if !balance.Known || remaining < balance.Credits {
						balance.Credits, balance.Total = remaining, total
					}
					balance.Known = true
				}
			}
		}
	}
	return balance, nil
}
