package antigravity

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// ParseImport accepts a single OAuth auth file, an array, or an accounts export.
// Only Antigravity entries are accepted from mixed-platform exports. Endpoint
// and OAuth application overrides in imported data are deliberately ignored.
func ParseImport(raw []byte) ([]Account, error) {
	if len(raw) == 0 || len(raw) > 4<<20 {
		return nil, fmt.Errorf("Antigravity 授权 JSON 须在 4 MB 以内")
	}
	var root any
	if json.Unmarshal(bytes.TrimPrefix(raw, []byte{0xef, 0xbb, 0xbf}), &root) != nil {
		return nil, fmt.Errorf("Antigravity 授权 JSON 格式无效")
	}
	entries, ok := root.([]any)
	if !ok {
		m, ok := root.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("Antigravity 授权 JSON 格式无效")
		}
		entries, ok = m["accounts"].([]any)
		if !ok {
			entries = []any{m}
		}
	}
	if len(entries) == 0 || len(entries) > 100 {
		return nil, fmt.Errorf("每次可导入 1 至 100 个 Antigravity 账号")
	}
	out := []Account{}
	for i, value := range entries {
		m, ok := value.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("第 %d 条账号格式无效", i+1)
		}
		text := func(keys ...string) string {
			for _, key := range keys {
				if s, ok := m[key].(string); ok && s != "" {
					return strings.TrimSpace(s)
				}
			}
			return ""
		}
		provider := text("provider", "type")
		if provider != "" && provider != "antigravity" {
			continue
		}
		a := Account{Email: text("email"), Nickname: text("nickname", "name"), ProjectID: text("project_id", "projectId"), AccessToken: text("access_token"), RefreshToken: text("refresh_token")}
		tokens := m
		if nested, ok := m["token"].(map[string]any); ok {
			tokens = nested
			a.AccessToken, _ = tokens["access_token"].(string)
			a.RefreshToken, _ = tokens["refresh_token"].(string)
			if project, ok := tokens["project_id"].(string); ok {
				a.ProjectID = project
			}
		}
		for _, key := range []string{"expires_at", "expired", "expiry_timestamp", "expiry"} {
			v, ok := tokens[key]
			if !ok {
				continue
			}
			if s, ok := v.(string); ok {
				if expiry, err := time.Parse(time.RFC3339, s); err == nil {
					a.ExpiresAt = expiry
					break
				}
				if n, err := strconv.ParseFloat(s, 64); err == nil {
					v = n
				}
			}
			if n, ok := v.(float64); ok && n > 0 {
				if n > 1e12 {
					n /= 1000
				}
				a.ExpiresAt = time.Unix(int64(n), 0)
				break
			}
		}
		if disabled, ok := m["disabled"].(bool); ok {
			a.Disabled = disabled
		}
		if enabled, ok := m["enabled"].(bool); ok && !enabled {
			a.Disabled = true
		}
		if a.AccessToken == "" && a.RefreshToken == "" {
			return nil, fmt.Errorf("第 %d 条账号缺少 OAuth Token", i+1)
		}
		out = append(out, a)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("文件中没有 Antigravity OAuth 账号")
	}
	return out, nil
}
