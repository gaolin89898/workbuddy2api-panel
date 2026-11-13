package main

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/linguo2625469/workbuddy2api-panel/internal/panel"
	"github.com/linguo2625469/workbuddy2api-panel/internal/pool"
	"github.com/linguo2625469/workbuddy2api-panel/internal/scheduler"
)

func TestAccountHandlerRestrictsRoutes(t *testing.T) {
	p := pool.New("")
	defer p.Close()
	h := accountHandler(panel.New(panel.Config{AccountsOnly: true, Pool: p, APIKey: "secret"}), func() any {
		return map[string]any{"service": "workbuddy-console", "ok": true, "gateway": false, "instance": "test-instance"}
	})
	for _, tc := range []struct {
		method, path string
		status       int
	}{
		{"GET", "/", 303},
		{"GET", "/panel/", 200},
		{"GET", "/panel/accounts.js", 200},
		{"GET", "/panel/index.css", 200},
		{"GET", "/panel/src/main.js", 404},
		{"GET", "/healthz", 200},
		{"GET", "/panel/api/overview", 401},
		{"GET", "/panel/api/trae/login/available", 401},
		{"POST", "/panel/api/trae/login/start", 401},
		{"GET", "/panel/api/trae/login/poll", 401},
		{"POST", "/panel/api/trae/login/cancel", 401},
		{"POST", "/panel/api/trae/accounts", 401},
		{"POST", "/panel/api/trae/import", 401},
		{"POST", "/panel/api/trae/checkin_all", 401},
		{"POST", "/panel/api/trae/balance_all", 401},
		{"POST", "/panel/api/trae/accounts/t1/remove", 401},
		{"POST", "/panel/api/loomy/accounts", 401},
		{"POST", "/panel/api/loomy/local_import", 401},
		{"POST", "/panel/api/loomy/balance_all", 401},
		{"POST", "/panel/api/loomy/accounts/l1/remove", 401},
		{"POST", "/panel/api/checkin_all", 401},
		{"POST", "/panel/api/login/start", 401},
		{"POST", "/panel/api/config", 401},
		{"POST", "/panel/api/accounts/u1/remove", 401},
		{"GET", "/v1/models", 404},
		{"POST", "/v1/chat/completions", 404},
		{"POST", "/v1/search/messages", 404},
		{"GET", "/status", 404},
		{"GET", "/panel/api/models", 404},
		{"GET", "/panel/api/usage", 404},
		{"POST", "/panel/api/accounts/u1/tasks/auto_all", 404},
		{"POST", "/panel/api/travel_all", 404},
		{"POST", "/panel/api/activity_all", 404},
	} {
		t.Run(tc.method+tc.path, func(t *testing.T) {
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(tc.method, tc.path, nil))
			if rec.Code != tc.status {
				t.Fatalf("status=%d want=%d body=%s", rec.Code, tc.status, rec.Body.String())
			}
		})
	}
	req := httptest.NewRequest("GET", "/panel/api/overview", nil)
	req.Header.Set("Authorization", "Bearer secret")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"accounts"`) {
		t.Fatalf("authenticated overview: %d %s", rec.Code, rec.Body.String())
	}
}

func TestAccountSettingsPersistence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data", "account-settings.json")
	s, err := loadAccountSettings(path, Default())
	if err != nil {
		t.Fatal(err)
	}
	sch := scheduler.New(scheduler.Config{})
	if _, err := s.save([]byte(`{"checkin_enabled":false,"checkin_hours":[8,20]}`), sch); err != nil {
		t.Fatal(err)
	}
	reloaded, err := loadAccountSettings(path, Default())
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.schedule.Enabled || len(reloaded.schedule.Hours) != 2 || reloaded.schedule.Hours[0] != 8 {
		t.Fatalf("saved settings not restored: %+v", reloaded.schedule)
	}
	if !reloaded.schedule.RefreshAfterCheckin || !reloaded.schedule.CatchupOnStart {
		t.Fatal("missing optional settings must preserve enabled defaults")
	}
	before, _ := os.ReadFile(path)
	for _, raw := range []string{
		`{"checkin_enabled":true,"checkin_hours":[]}`,
		`{"checkin_enabled":true,"checkin_hours":[24]}`,
		`{"checkin_enabled":true,"checkin_hours":[-1]}`,
		`{"checkin_enabled":true,"checkin_hours":[9,9]}`,
		`{"checkin_enabled":true,"checkin_hours":[9.5]}`,
		`{"checkin_enabled":true,"checkin_hours":[9],"api_key":""}`,
		`{"checkin_enabled":true,"checkin_hours":[9]} {}`,
		`null`,
	} {
		if _, err := s.save([]byte(raw), sch); err == nil {
			t.Errorf("accepted invalid settings: %s", raw)
		}
	}
	after, _ := os.ReadFile(path)
	if string(after) != string(before) || s.schedule.Enabled || s.schedule.Hours[0] != 8 {
		t.Fatal("invalid update changed settings")
	}
}

func TestAccountOptionsPersistence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "account-settings.json")
	// Old settings files gain the new options with the previous behavior as defaults.
	if err := os.WriteFile(path, []byte(`{"checkin_enabled":true,"checkin_hours":[9,21]}`), 0600); err != nil {
		t.Fatal(err)
	}
	s, err := loadAccountSettings(path, Default())
	if err != nil {
		t.Fatal(err)
	}
	if !s.schedule.RefreshAfterCheckin || !s.schedule.CatchupOnStart {
		t.Fatal("old settings did not receive defaults")
	}
	sch := scheduler.New(scheduler.Config{CheckinOnly: true})
	if _, err := s.save([]byte(`{"refresh_after_checkin":false,"catchup_on_start":false}`), sch); err != nil {
		t.Fatal(err)
	}
	reloaded, err := loadAccountSettings(path, Default())
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.schedule.RefreshAfterCheckin || reloaded.schedule.CatchupOnStart || sch.CheckinRefreshEnabled() {
		t.Fatal("new options were not persisted and applied")
	}
}

// 统一后台第二阶段：账号模式迁移必须幂等、保留未知字段、不覆盖显式配置、
// 失败不破坏旧数据；任务族开关物化为账号模式的有效状态（false）。
func TestMigrateAccountSettings(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root, "config.json")
	settingsPath := filepath.Join(root, "data", "account-settings.json")
	if err := os.MkdirAll(filepath.Dir(settingsPath), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(settingsPath, []byte(`{"checkin_enabled":false,"checkin_hours":[8],"refresh_after_checkin":true,"catchup_on_start":true}`), 0600); err != nil {
		t.Fatal(err)
	}
	original := `{"listen":":7863","unknown_field":{"keep":true},"schedule":{"travel_enabled":true,"custom_key":1}}`
	if err := os.WriteFile(configPath, []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	if err := migrateAccountSettings(configPath, settingsPath); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	// 迁移产物：显式键不覆盖（travel_enabled 保持 true）、缺失键补齐、未知字段保留。
	raw, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Listen   string         `json:"listen"`
		Unknown  any            `json:"unknown_field"`
		Schedule map[string]any `json:"schedule"`
		Checkin  map[string]any `json:"checkin"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Listen != ":7863" || doc.Unknown == nil {
		t.Fatalf("unknown fields or explicit keys lost: %s", raw)
	}
	if doc.Schedule["travel_enabled"] != true || doc.Schedule["custom_key"] != float64(1) {
		t.Fatalf("explicit schedule keys were overwritten: %v", doc.Schedule)
	}
	for _, key := range []string{"activity_enabled", "keepalive_enabled", "blackcat_enabled", "growth_enabled", "streak_enabled"} {
		if doc.Schedule[key] != false {
			t.Fatalf("schedule.%s = %v, want false (materialized)", key, doc.Schedule[key])
		}
	}
	if doc.Schedule["checkin_hours"] == nil || doc.Checkin["catchup_on_start"] != true {
		t.Fatalf("checkin settings not synced: schedule=%v checkin=%v", doc.Schedule, doc.Checkin)
	}
	// 备份存在且内容等于原文。
	matches, _ := filepath.Glob(configPath + ".migrate-bak-*")
	if len(matches) != 1 {
		t.Fatalf("expected exactly one backup, got %v", matches)
	}
	if bak, err := os.ReadFile(matches[0]); err != nil || string(bak) != original {
		t.Fatalf("backup mismatch: %v %s", err, bak)
	}
	// 幂等：第二次运行不再改写（备份不增加、内容不变）。
	if err := migrateAccountSettings(configPath, settingsPath); err != nil {
		t.Fatal(err)
	}
	matches, _ = filepath.Glob(configPath + ".migrate-bak-*")
	if len(matches) != 1 {
		t.Fatalf("migration is not idempotent: backups=%v", matches)
	}
	again, _ := os.ReadFile(configPath)
	var doc2 map[string]any
	if json.Unmarshal(again, &doc2) != nil || doc2["listen"] != ":7863" {
		t.Fatalf("second migration changed data: %s", again)
	}
}

// 纯网关部署（无 account-settings.json）不迁移；损坏的 config 原样保留并报错。
func TestMigrateAccountSettingsBoundaries(t *testing.T) {
	root := t.TempDir()
	if err := migrateAccountSettings(filepath.Join(root, "config.json"), filepath.Join(root, "settings.json")); err != nil {
		t.Fatalf("missing settings must be a no-op: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "config.json")); !os.IsNotExist(err) {
		t.Fatal("config must not be created when no account settings exist")
	}
	settingsPath := filepath.Join(root, "settings.json")
	if err := os.WriteFile(settingsPath, []byte(`{"checkin_enabled":true,"checkin_hours":[9]}`), 0600); err != nil {
		t.Fatal(err)
	}
	broken := filepath.Join(root, "config.json")
	if err := os.WriteFile(broken, []byte(`{broken`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := migrateAccountSettings(broken, settingsPath); err == nil {
		t.Fatal("broken config must fail the migration")
	}
	raw, _ := os.ReadFile(broken)
	if string(raw) != "{broken" {
		t.Fatalf("broken config was modified: %s", raw)
	}
}

// 仅账号模式：/panel/api/config 保持签到设置形态（旧契约），新接口 settings/checkin
// 可读写并热应用；capabilities 如实报告 gateway=false。
func TestAccountModeSettingsEndpoints(t *testing.T) {
	p := pool.New("")
	defer p.Close()
	sch := scheduler.New(scheduler.Config{CheckinOnly: true})
	settingsPath := filepath.Join(t.TempDir(), "account-settings.json")
	settings, err := loadAccountSettings(settingsPath, Default())
	if err != nil {
		t.Fatal(err)
	}
	pn := panel.New(panel.Config{
		AccountsOnly: true, Pool: p, Scheduler: sch, APIKey: "secret",
		ConfigPath:  settingsPath,
		LoadCheckin: settings.load,
		SaveCheckin: func(raw []byte) error {
			_, err := settings.save(raw, sch)
			return err
		},
	})
	call := func(method, path string, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer secret")
		rec := httptest.NewRecorder()
		accountHandler(pn, func() any { return map[string]any{"ok": true} }).ServeHTTP(rec, req)
		return rec
	}
	if rec := call("GET", "/panel/api/capabilities", ""); rec.Code != 200 || strings.Contains(rec.Body.String(), `"gateway":true`) {
		t.Fatalf("capabilities: %d %s", rec.Code, rec.Body.String())
	}
	if rec := call("GET", "/panel/api/config", ""); rec.Code != 200 || !strings.Contains(rec.Body.String(), `"checkin_hours"`) {
		t.Fatalf("legacy config shape: %d %s", rec.Code, rec.Body.String())
	}
	if rec := call("GET", "/panel/api/settings/checkin", ""); rec.Code != 200 || !strings.Contains(rec.Body.String(), `"refresh_after_checkin"`) {
		t.Fatalf("settings/checkin GET: %d %s", rec.Code, rec.Body.String())
	}
	if rec := call("POST", "/panel/api/settings/checkin", `{"checkin_enabled":false,"checkin_hours":[7,19]}`); rec.Code != 200 {
		t.Fatalf("settings/checkin POST: %d %s", rec.Code, rec.Body.String())
	}
	reloaded, err := loadAccountSettings(settingsPath, Default())
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.schedule.Enabled || len(reloaded.schedule.Hours) != 2 || reloaded.schedule.Hours[0] != 7 {
		t.Fatalf("settings/checkin POST did not persist: %+v", reloaded.schedule)
	}
	if rec := call("POST", "/panel/api/settings/checkin", `{"checkin_enabled":true,"checkin_hours":[25]}`); rec.Code == 200 {
		t.Fatal("invalid hours must be rejected")
	}
}
