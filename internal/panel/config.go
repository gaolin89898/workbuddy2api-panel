// config.go 面板配置页接口：统一设置新接口 + 旧 /panel/api/config 兼容适配。
//
// 分工：cmd/server 持有 Config 类型与校验逻辑（Load/normalize），此处只做
// HTTP 编排——GET 回显、POST 透传给注入的闭包（由 main 完成
// "校验 → 落盘 → 热应用 → 返回需重启字段列表"）。
//
// 接口分层（统一后台重构第二阶段）：
//   - GET/POST /panel/api/settings/checkin  签到设置（account-settings.json）
//   - GET/POST /panel/api/settings/gateway  网关设置（config.json）
//   - GET      /panel/api/capabilities      服务能力发现（前端据此显示菜单）
//   - GET/POST /panel/api/config            兼容旧页面：网关模式回 config.json 全量；
//     仅账号模式回签到设置形态（旧账号页/旧 DSH 资产的既有契约）。
package panel

import (
	"io"
	"log"
	"net/http"
	"time"
)

// getConfig 旧配置接口（兼容保留）。网关模式：返回 config.json 全量（经典控制台
// 配置页契约）；仅账号模式：返回签到设置形态（旧账号页契约）。新页面一律使用
// settings/checkin 与 settings/gateway。
func (p *Panel) getConfig(w http.ResponseWriter, r *http.Request) {
	if p.cfg.AccountsOnly {
		if p.cfg.LoadCheckin == nil {
			writeErr(w, http.StatusNotImplemented, "config api not available")
			return
		}
		settings, err := p.cfg.LoadCheckin()
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "load settings: "+err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":     true,
			"path":   p.cfg.ConfigPath,
			"config": settings,
		})
		return
	}
	if p.cfg.LoadConfig == nil {
		writeErr(w, http.StatusNotImplemented, "config api not available")
		return
	}
	cfg, err := p.cfg.LoadConfig()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "load config: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":     true,
		"path":   p.cfg.ConfigPath,
		"config": cfg,
	})
}

// saveConfig 旧配置接口（兼容保留）。body 即对应形态的配置 JSON：
// 网关模式 = config.json 全量；仅账号模式 = 签到设置对象。
func (p *Panel) saveConfig(w http.ResponseWriter, r *http.Request) {
	raw, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "read body: "+err.Error())
		return
	}
	if p.cfg.AccountsOnly {
		if p.cfg.SaveCheckin == nil {
			writeErr(w, http.StatusNotImplemented, "config api not available")
			return
		}
		if err := p.cfg.SaveCheckin(raw); err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		log.Printf("panel: 签到设置已保存（兼容接口）")
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "restart_required": []string{}})
		return
	}
	if p.cfg.SaveConfig == nil {
		writeErr(w, http.StatusNotImplemented, "config api not available")
		return
	}
	restartRequired, err := p.cfg.SaveConfig(raw)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if restartRequired == nil {
		restartRequired = []string{}
	}
	log.Printf("panel: 配置已保存（热生效完成；需重启字段 %d 个）", len(restartRequired))
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":               true,
		"restart_required": restartRequired,
	})
}

// getCheckinSettings 签到设置（新接口）：checkin_enabled / checkin_hours /
// refresh_after_checkin / catchup_on_start / timezone。存储在 account-settings.json。
func (p *Panel) getCheckinSettings(w http.ResponseWriter, r *http.Request) {
	if p.cfg.LoadCheckin == nil {
		writeErr(w, http.StatusNotImplemented, "checkin settings not available")
		return
	}
	settings, err := p.cfg.LoadCheckin()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "load checkin settings: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":       true,
		"path":     p.cfg.ConfigPath,
		"settings": settings,
	})
}

// saveCheckinSettings 保存签到设置并热应用（排程时点/开关 + 旁路账号余额刷新）。
func (p *Panel) saveCheckinSettings(w http.ResponseWriter, r *http.Request) {
	if p.cfg.SaveCheckin == nil {
		writeErr(w, http.StatusNotImplemented, "checkin settings not available")
		return
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, 64<<10))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "read body: "+err.Error())
		return
	}
	if err := p.cfg.SaveCheckin(raw); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	log.Printf("panel: 签到设置已保存（热生效）")
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// getGatewaySettings 网关设置（新接口）：config.json 全量（与旧网关配置页同契约，
// 经 LoadConfig 闭包回显；runtime 侧负责用 account-settings.json 覆盖签到字段，
// 保证签到设置只有一个权威来源）。
func (p *Panel) getGatewaySettings(w http.ResponseWriter, r *http.Request) {
	if p.cfg.LoadConfig == nil {
		writeErr(w, http.StatusNotImplemented, "gateway settings not available")
		return
	}
	cfg, err := p.cfg.LoadConfig()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "load config: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":     true,
		"path":   p.cfg.ConfigPath,
		"config": cfg,
	})
}

// saveGatewaySettings 保存网关设置：校验 → 落盘 config.json → 热应用 →
// 返回需重启字段列表。签到相关字段以 account-settings.json 为准（runtime 闭包
// 在保存后重新应用签到排程），普通账号接口不返回、也不经此接口泄露网关密钥以外
// 的账号凭证。
func (p *Panel) saveGatewaySettings(w http.ResponseWriter, r *http.Request) {
	if p.cfg.SaveConfig == nil {
		writeErr(w, http.StatusNotImplemented, "gateway settings not available")
		return
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "read body: "+err.Error())
		return
	}
	restartRequired, err := p.cfg.SaveConfig(raw)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if restartRequired == nil {
		restartRequired = []string{}
	}
	log.Printf("panel: 网关设置已保存（热生效完成；需重启字段 %d 个）", len(restartRequired))
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":               true,
		"restart_required": restartRequired,
	})
}

// schedulesStatus 调度状态：各任务族最近一次执行时刻（Unix 秒；0 = 进程内尚未
// 执行过）。定时任务页「上次运行」列的数据源，只读、无副作用。
func (p *Panel) schedulesStatus(w http.ResponseWriter, r *http.Request) {
	if p.cfg.Scheduler == nil {
		writeErr(w, http.StatusNotImplemented, "scheduler not available")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":        true,
		"last_runs": p.cfg.Scheduler.RunStatus(),
	})
}

// capabilities 服务能力发现：前端据此渲染菜单（账号端恒可用；网关能力仅在
// -gateway 时开放）。只描述能力与观测口径，不返回任何凭证。
func (p *Panel) capabilities(w http.ResponseWriter, r *http.Request) {
	gateway := !p.cfg.AccountsOnly
	features := map[string]any{
		// 两类模式恒可用：五平台账号管理与签到、桌面客户端、任务/奖励。
		"accounts":       true,
		"checkin":        true,
		"desktop":        true,
		"packages":       true,
		"credit_history": p.cfg.CreditHistory != nil,
		"oauth_import":   true,
		// 网关专属：/v1/* 转发、模型目录、用量、请求指标、补偿与保号任务族。
		"forwarding":      gateway,
		"models":          gateway,
		"usage":           gateway && p.cfg.Usage != nil,
		"usage_save":      gateway && p.cfg.Usage != nil,
		"request_metrics": gateway && p.cfg.RequestLog != nil,
		"request_logs":    gateway && p.cfg.RequestLog != nil,
		"model_probes":    gateway,
		"session_sticky":  gateway && p.cfg.StickyCount != nil,
		"compensation":    gateway && p.cfg.Scheduler != nil,
		"balance_refresh": gateway && p.cfg.Scheduler != nil && p.cfg.Scheduler.BalanceInterval() > 0,
		"login_regions":   gateway,
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":         true,
		"service":    "workbuddy-console",
		"version":    p.cfg.Version,
		"gateway":    gateway,
		"redis_mode": p.cfg.RedisMode,
		"timezone":   time.Local.String(),
		"features":   features,
	})
}
