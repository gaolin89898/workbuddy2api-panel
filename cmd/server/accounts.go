package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/linguo2625469/workbuddy2api-panel/internal/scheduler"
)

type accountSchedule struct {
	Enabled             bool  `json:"checkin_enabled"`
	Hours               []int `json:"checkin_hours"`
	RefreshAfterCheckin bool  `json:"refresh_after_checkin"`
	CatchupOnStart      bool  `json:"catchup_on_start"`
}

func (s accountSchedule) validate() error {
	if len(s.Hours) == 0 || len(s.Hours) > 24 {
		return fmt.Errorf("请设置 1 至 24 个签到时刻")
	}
	seen := make(map[int]bool)
	for _, h := range s.Hours {
		if h < 0 || h > 23 || seen[h] {
			return fmt.Errorf("签到小时须为 0 至 23 的整数，且不能重复")
		}
		seen[h] = true
	}
	return nil
}

type accountSettings struct {
	mu       sync.Mutex
	path     string
	schedule accountSchedule
}

func loadAccountSettings(path string, cfg *Config) (*accountSettings, error) {
	s := &accountSettings{path: path, schedule: accountSchedule{
		Enabled: cfg.Schedule.CheckinEnabled, Hours: cfg.Schedule.CheckinHours,
		RefreshAfterCheckin: true, CatchupOnStart: true,
	}}
	raw, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	if err == nil {
		if err := json.Unmarshal(raw, &s.schedule); err != nil {
			return nil, fmt.Errorf("读取签到设置: %w", err)
		}
	}
	return s, s.schedule.validate()
}

func (s *accountSettings) load() (any, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return map[string]any{
		"checkin_enabled":       s.schedule.Enabled,
		"checkin_hours":         append([]int(nil), s.schedule.Hours...),
		"refresh_after_checkin": s.schedule.RefreshAfterCheckin,
		"catchup_on_start":      s.schedule.CatchupOnStart,
		"timezone":              time.Local.String(),
	}, nil
}

func (s *accountSettings) save(raw []byte, sch *scheduler.Scheduler) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := s.schedule
	next.Hours = append([]int(nil), s.schedule.Hours...)
	if trimmed := bytes.TrimSpace(raw); len(trimmed) == 0 || trimmed[0] != '{' {
		return nil, fmt.Errorf("设置必须是单个 JSON 对象")
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&next); err != nil {
		return nil, err
	}
	if err := dec.Decode(new(any)); err != io.EOF {
		return nil, fmt.Errorf("设置必须是单个 JSON 对象")
	}
	if err := next.validate(); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return nil, err
	}
	out, err := json.MarshalIndent(next, "", "  ")
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(s.path+".tmp", out, 0o600); err != nil {
		return nil, err
	}
	if err := os.Rename(s.path+".tmp", s.path); err != nil {
		return nil, err
	}
	s.schedule = next
	configureAccountSchedule(sch, next)
	return nil, nil
}

// configureAccountSchedule 只应用签到相关排程（时点/开关/签到后刷新）。
// 旅行/活跃/保活等其他任务族归 config.json 网关设置管，签到设置保存不得触碰。
func configureAccountSchedule(sch *scheduler.Scheduler, s accountSchedule) {
	sch.SetCheckinRefreshEnabled(s.RefreshAfterCheckin)
	sch.ReconfigureCheckin(s.Hours, !s.Enabled)
}

// migrateAccountSettings 把账号模式的有效设置物化进 config.json（一次性、幂等、
// 失败不破坏旧数据）。
//
// 迁移优先级（统一后台重构第二阶段约定）：
//   - 签到设置的权威来源始终是 account-settings.json（存在时）；config.json 里的
//     checkin_* 键只是同步副本，供网关侧工具观测。
//   - config.json 显式写过的键绝不覆盖；只补缺失键，未知字段原样保留。
//
// 物化内容：存在 account-settings.json ⇒ 此前以账号模式运行 ⇒ 旅行 / 活跃 /
// 保活 / 夜猫子 / 成长 / 连登兑换在账号模式下从未执行。把它们显式写成 false，
// 避免 -gateway 按缺省值（true）隐式新增这些操作；用户可在网关设置里显式开启。
// 纯网关部署（无 account-settings.json）不做任何迁移，保持既有缺省行为。
//
// 备份：改写前把 config.json 复制为 config.json.migrate-bak-<时间戳>（时间戳命名，
// 永不覆盖历史备份）。config.json 不存在时仅落一份只含 schedule 物化键的最小配置
// （其余字段继续走缺省 + env，不产生行为漂移）。
func migrateAccountSettings(configPath, settingsPath string) error {
	raw, err := os.ReadFile(settingsPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // 纯网关部署：没有账号模式的历史，无需迁移
		}
		return fmt.Errorf("read settings: %w", err)
	}
	var settings accountSchedule
	if err := json.Unmarshal(raw, &settings); err != nil {
		return fmt.Errorf("parse settings: %w", err)
	}
	cfgRaw, err := os.ReadFile(configPath)
	existed := err == nil
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("read config: %w", err)
	}
	var cur map[string]any
	if existed {
		if err := json.Unmarshal(cfgRaw, &cur); err != nil {
			return fmt.Errorf("parse config（已保留原文件，未做任何修改）: %w", err)
		}
	} else {
		cur = map[string]any{}
	}
	schedule, _ := cur["schedule"].(map[string]any)
	if schedule == nil {
		schedule = map[string]any{}
		cur["schedule"] = schedule
	}
	changed := false
	ensure := func(m map[string]any, key string, value any) {
		if _, ok := m[key]; !ok {
			m[key] = value
			changed = true
		}
	}
	// 签到时点/开关同步进 config（account-settings.json 仍是权威，这里只补缺失键）。
	if len(settings.Hours) > 0 {
		ensure(schedule, "checkin_hours", settings.Hours)
	}
	ensure(schedule, "checkin_enabled", settings.Enabled)
	// 账号模式从未执行的任务族显式关停，防止 -gateway 隐式新增。
	ensure(schedule, "travel_enabled", false)
	ensure(schedule, "activity_enabled", false)
	ensure(schedule, "keepalive_enabled", false)
	ensure(schedule, "blackcat_enabled", false)
	ensure(schedule, "growth_enabled", false)
	ensure(schedule, "streak_enabled", false)
	// checkin 明细段（refresh/catchup 等 config 没有的键），供观测与回填。
	checkin, _ := cur["checkin"].(map[string]any)
	if checkin == nil {
		checkin = map[string]any{}
		cur["checkin"] = checkin
	}
	ensure(checkin, "checkin_enabled", settings.Enabled)
	if len(settings.Hours) > 0 {
		ensure(checkin, "checkin_hours", settings.Hours)
	}
	ensure(checkin, "refresh_after_checkin", settings.RefreshAfterCheckin)
	ensure(checkin, "catchup_on_start", settings.CatchupOnStart)
	if !changed {
		return nil // 幂等：键已齐备，第二次运行不再写盘
	}
	if existed && len(cfgRaw) > 0 {
		bak := fmt.Sprintf("%s.migrate-bak-%s", configPath, time.Now().Format("20060102-150405"))
		if err := os.WriteFile(bak, cfgRaw, 0o600); err != nil {
			return fmt.Errorf("backup config: %w", err)
		}
	}
	out, err := json.MarshalIndent(cur, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	tmp := configPath + ".migrate-tmp"
	if err := os.WriteFile(tmp, out, 0o600); err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	if err := os.Rename(tmp, configPath); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("replace config: %w", err)
	}
	log.Printf("config 迁移完成：签到设置已同步，保号任务族保持账号模式状态（false）；原文件备份于 %s", configPath+".migrate-bak-*")
	return nil
}
