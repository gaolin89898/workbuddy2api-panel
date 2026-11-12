// runtime.go 统一运行时装配（统一后台重构第一阶段）：
// 一个服务进程、一份 WorkBuddy 账号池、一套账号状态和签到调度。
//
// 账号管理与网关共用同一份装配：
//   - WorkBuddy Pool 与上游客户端（两种模式指向同一个实例）；
//   - Trae / Qoder / MiniMax / Loomy Store（每个平台只创建一次）；
//   - 积分历史、面板日志环形缓冲、签到设置（account-settings.json）与调度器；
//   - HTTP 服务与关闭流程（先停排程、刷池、再排空存储写入）。
//
// 网关（-gateway）是可选模块：额外创建 Redis 镜像、会话粘性路由、用量统计、
// 请求指标与 /v1/* 转发 Handler。默认（不带 -gateway）不开放 /v1/*，
// 不初始化 Redis、用量记录与会话路由。
package main

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/linguo2625469/workbuddy2api-panel/internal/auth"
	"github.com/linguo2625469/workbuddy2api-panel/internal/credithist"
	"github.com/linguo2625469/workbuddy2api-panel/internal/livecfg"
	"github.com/linguo2625469/workbuddy2api-panel/internal/loomy"
	"github.com/linguo2625469/workbuddy2api-panel/internal/minimax"
	"github.com/linguo2625469/workbuddy2api-panel/internal/panel"
	"github.com/linguo2625469/workbuddy2api-panel/internal/pool"
	"github.com/linguo2625469/workbuddy2api-panel/internal/qoder"
	"github.com/linguo2625469/workbuddy2api-panel/internal/redisstore"
	"github.com/linguo2625469/workbuddy2api-panel/internal/reqlog"
	"github.com/linguo2625469/workbuddy2api-panel/internal/scheduler"
	"github.com/linguo2625469/workbuddy2api-panel/internal/server"
	"github.com/linguo2625469/workbuddy2api-panel/internal/session"
	"github.com/linguo2625469/workbuddy2api-panel/internal/trae"
	"github.com/linguo2625469/workbuddy2api-panel/internal/upstream"
	"github.com/linguo2625469/workbuddy2api-panel/internal/usage"
)

// runtime 统一运行时：两种模式共用一份核心装配；网关模块按需创建。
type appRuntime struct {
	cfg        *Config
	configPath string
	gateway    bool

	p          *pool.Pool
	up         *upstream.Client
	creditHist *credithist.Ledger
	trae       *trae.Store
	qoder      *qoder.Store
	minimax    *minimax.Store
	loomy      *loomy.Store
	settings   *accountSettings
	sch        *scheduler.Scheduler
	pn         *panel.Panel

	// 网关可选模块（gateway=false 时全部为 nil，不初始化、不占资源）。
	store      redisstore.Store
	sess       *session.Router
	usageRec   *usage.Recorder
	requestLog *reqlog.Recorder
	live       *livecfg.Holder

	redisMode  string
	sessCount  func() int
	panelLogMu io.Writer // 装配前的 log 输出（Close 时恢复）
}

// newAppRuntime 构建统一运行时。调用方负责在退出时执行 rt.Close()。
func newAppRuntime(configPath string, cfg *Config, gateway bool) (*appRuntime, error) {
	// 北京时间调度：统一后两类模式都以 Asia/Shanghai 解释排程小时。
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		return nil, err
	}
	time.Local = loc
	rt := &appRuntime{cfg: cfg, configPath: configPath, gateway: gateway, redisMode: "noop"}

	auth.SetGlobalEnabled(cfg.Global.Enabled)
	auths, err := auth.LoadDir(cfg.AuthDir)
	if err != nil {
		return nil, err
	}
	log.Printf("loaded %d account(s) from %s", len(auths), cfg.AuthDir)

	rt.p = pool.New(cfg.StateFile)
	rt.p.SyncToDir(auths) // 与 auths 目录对齐：新账号加入、已删除文件账号剔除（状态保留）
	// 熔断器 + 在途上限 + 连败降权等池参数：统一按 config 注入（缺省值与内置
	// 默认一致，账号模式行为不变；网关模式与原装配逐项相同）。
	rt.p.SetBreaker(cfg.Pool.BreakerThreshold, cfg.BreakerCooldownDur, cfg.BreakerCooldownMaxD)
	rt.p.SetMaxInFlight(cfg.Pool.MaxInFlight)
	rt.p.SetMaxInFlightGlobal(cfg.Pool.MaxInFlightGlobal)
	rt.p.SetDegrade(cfg.Pool.DegradeThreshold, cfg.DegradeCooldownDur, cfg.DegradeCooldownMaxD)
	rt.p.SetSoftRateMax(cfg.SoftRateMaxDur)
	rt.p.SetCostExploreInterval(cfg.CostExploreIntervalDur)
	rt.p.SetCreditFloor(cfg.Pool.CreditFloor)
	rt.p.SetWeights(cfg.Pool.IdleWeightPerHour, cfg.Pool.IdleWeightMax)
	rt.p.SetPreferExpiring(cfg.Pool.PreferExpiring)
	rt.p.SetCrossDomainFallback(cfg.Pool.CrossDomainFallback)
	_ = rt.p.SetScheduling(cfg.Pool.Strategy, cfg.Pool.AccountOrder)

	if gateway {
		// Redis 镜像：未配置/连接失败 → Noop（纯内存模式，一切功能照常）。
		// 停机序：先 pool.Close()（最后一次 Flush → SaveState 已提交到 store），
		// 再 store.Close() 排空在途异步写（见 runtime.Close）。
		rt.store = redisstore.New(cfg.Upstash.URL, cfg.Upstash.Token)
		rt.p.SetStore(rt.store)
		rt.p.RestoreFromSnapshot() // 择新恢复：Redis 快照比本地新才采用，否则本地优先
		if _, ok := rt.store.(redisstore.Noop); !ok {
			rt.redisMode = "upstash"
		}
	}

	rt.up = upstream.New()
	// 短 RPC 总时长上限（refresh/checkin/balance/FetchModels）。
	rt.up.HTTP.Timeout = time.Duration(cfg.Upstream.TimeoutSeconds) * time.Second
	// 聊天 SSE 首字节前（响应头）上限：cfg 已 normalize（缺省回落 timeout_seconds）。
	rt.up.HeaderTimeout = time.Duration(cfg.Upstream.HeaderTimeoutSeconds) * time.Second
	if tr, ok := rt.up.ChatHTTP.Transport.(*http.Transport); ok {
		tr.ResponseHeaderTimeout = rt.up.HeaderTimeout
	}
	// 聊天 SSE 流中空闲上限。
	rt.up.IdleTimeout = time.Duration(cfg.Upstream.IdleTimeoutSeconds) * time.Second
	rt.up.SanitizeFingerprints.Store(cfg.Features.SanitizeBlacklistFingerprints)
	rt.up.UserAgent = cfg.Upstream.UserAgent
	rt.up.ClientVersion = cfg.Upstream.ClientVersion
	rt.up.CliVersion = cfg.Upstream.CliVersion
	rt.up.ClientName = cfg.Upstream.ClientName
	rt.up.DeviceToken = cfg.Upstream.DeviceToken
	rt.up.DeviceTokenFile = cfg.Upstream.DeviceTokenFile
	rt.up.PassthroughIP = cfg.Upstream.PassthroughIP
	// global realm 路由（config global 段）。
	rt.up.GlobalEnabled = cfg.Global.Enabled
	rt.up.ChatBaseGlobal = cfg.Global.ChatBase
	rt.up.BillingBaseGlobal = cfg.Global.BillingBase
	applyCNChatBase(rt.up, cfg)
	if len(cfg.DNSServers) > 0 {
		rt.up.SetDNSDialer(upstream.NewDNSDialer(cfg.DNSServers))
	}
	// model.json 本地缓存接线（与 state 文件同目录，随 state_file 搬移）。
	upstream.SetModelCatalogPath(stateSibling(cfg.StateFile, "model.json"))
	// 积分保底的「收费」兜底判据：接上游模型目录的积分倍率表。
	rt.p.SetModelRateOf(func(realm, model string) string { return rt.up.ModelRate(realm, model) })

	// 积分历史：余额观察者覆盖签到 / 活跃 / 旅行 / 保活 / 面板手动刷新全路径。
	rt.creditHist = credithist.New(stateSibling(cfg.StateFile, "credit-history.json"), 2000)
	rt.up.SetCreditObserver(rt.creditHist.Observe)
	log.Printf("[credithist] 积分历史已启用: %s", stateSibling(cfg.StateFile, "credit-history.json"))

	// 配置迁移（幂等，失败保留旧数据继续运行）+ 签到设置加载。
	settingsPath := stateSibling(cfg.StateFile, "account-settings.json")
	if err := migrateAccountSettings(configPath, settingsPath); err != nil {
		log.Printf("WARN: config 迁移跳过（旧数据未改动）: %v", err)
	}
	rt.settings, err = loadAccountSettings(settingsPath, cfg)
	if err != nil {
		return nil, err
	}

	// 旁路平台 Store：五平台账号管理与签到始终可用，每个 Store 只创建一次。
	if rt.trae, err = trae.Open(stateSibling(cfg.StateFile, "trae-accounts.json"), nil); err != nil {
		return nil, err
	}
	if rt.qoder, err = qoder.Open(stateSibling(cfg.StateFile, "qoder-accounts.json"), nil); err != nil {
		return nil, err
	}
	if rt.minimax, err = minimax.Open(stateSibling(cfg.StateFile, "minimax-accounts.json"), nil); err != nil {
		return nil, err
	}
	if rt.loomy, err = loomy.Open(stateSibling(cfg.StateFile, "loomy-accounts.json"), nil); err != nil {
		return nil, err
	}
	applySideRefresh(rt)

	// 签到入口（自动排程 / 启动补签 / 面板手动触发共用）：
	// WorkBuddy 池签到由 scheduler 负责，旁路平台经 CheckinHook 同批执行。
	runSideCheckin := func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		rt.trae.RunAll(ctx, "checkin")
		rt.minimax.RunAll(ctx, "checkin")
		rt.loomy.RunAll(ctx, "checkin")
	}
	// 调度器：签到永远按 account-settings.json；网关任务族（旅行/活跃/保活/
	// 夜猫子/成长/连登）仅网关模式按 config 生效——账号模式恒关，且迁移已把
	// 该状态物化进 config.json，开启网关不会隐式新增。
	rt.sch = scheduler.New(scheduler.Config{
		Pool:     rt.p,
		Upstream: rt.up,
		CheckinHook:       runSideCheckin,
		CheckinHours:      rt.settings.schedule.Hours,
		CheckinDisabled:   !rt.settings.schedule.Enabled,
		CheckinOnly:       !gateway,
		StreakDisabled:    !gateway || !cfg.Schedule.StreakEnabled,
		ExpiringSoonWindow: cfg.ExpiringSoonDur,
		TravelHours:       cfg.Schedule.TravelHours,
		TravelDisabled:    !gateway || !cfg.Schedule.TravelEnabled,
		ActivityHours:     cfg.Schedule.ActivityHours,
		ActivityDisabled:  !gateway || !cfg.Schedule.ActivityEnabled,
		KeepaliveHours:    cfg.Schedule.KeepaliveHours,
		KeepaliveDisabled: !gateway || !cfg.Schedule.KeepaliveEnabled,
		BlackcatHours:     cfg.Schedule.BlackcatHours,
		BlackcatDisabled:  !gateway || !cfg.Schedule.BlackcatEnabled,
		GrowthHours:       cfg.Schedule.GrowthHours,
		GrowthDisabled:    !gateway || !cfg.Schedule.GrowthEnabled,
		IncludeDisabledInTasks: cfg.Schedule.IncludeDisabledInTasks,
	})
	rt.sch.SetCheckinRefreshEnabled(rt.settings.schedule.RefreshAfterCheckin)
	logSchedulerStatus(rt)

	// 网关可选模块：livecfg / 会话粘性 / 用量 / 请求指标。
	if gateway {
		rt.live = livecfg.New(livecfg.Snapshot{
			APIKey:               cfg.APIKey,
			SoftCooldown:         cfg.SoftRateDur,
			SanitizeFingerprints: cfg.Features.SanitizeBlacklistFingerprints,
			RecordClientInfo:     cfg.Logging.RequestClientInfo,
			HiddenModels:         livecfg.NormalizeHiddenModels(cfg.Models.Hidden),
		})
		usagePath := usagePathFor(cfg.StateFile)
		rt.usageRec = usage.New(usagePath)
		rt.usageRec.Start()
		log.Printf("[usage] 逐请求用量记录已启用: %s (%s)", usagePath, rt.usageRec.Describe())
		rt.requestLog = reqlog.New(reqlog.Config{
			Dir:           stateSibling(cfg.StateFile, "request-logs"),
			Enabled:       cfg.Logging.RequestArchiveEnabled,
			RetentionDays: cfg.Logging.RequestRetentionDays,
			MaxBytes:      int64(cfg.Logging.RequestArchiveMaxMB) << 20,
		})
		if rs := rt.requestLog.Snapshot().Archive; rs.Enabled {
			log.Printf("[reqlog] 请求指标已启用；JSONL 归档 %s（保留 %d 天，上限 %d MiB）",
				rs.Dir, cfg.Logging.RequestRetentionDays, cfg.Logging.RequestArchiveMaxMB)
		} else {
			log.Printf("[reqlog] 请求指标已启用；JSONL 归档已关闭")
		}
		if cfg.SessionSticky.Enabled {
			rt.sess = session.New(session.Config{
				TTL:        cfg.SessionTTL,
				GCInterval: cfg.SessionGCInterval,
				Store:      rt.store,
				Available:  rt.p.AvailableUIDs,
				// realm 感知闭包：带前缀模型名按 realm 过滤可用账号（跨 realm 不泄漏）。
				AvailableForModel: realmAwareAvailableForModel(rt.p),
			})
			rt.sess.LoadFromStore() // 启动时从 Redis 恢复粘性（读操作仅此处）
			rt.sess.StartGC()
		}
	}
	rt.sessCount = func() int {
		if rt.sess != nil {
			return rt.sess.Count()
		}
		return 0
	}

	// 管理面板：统一路由。网关模式额外透出用量/请求指标/粘性会话等观测。
	rt.pn = panel.New(panel.Config{
		AccountsOnly: !gateway,
		Pool:         rt.p,
		Upstream:     rt.up,
		Scheduler:    rt.sch,
		Trae:         rt.trae,
		Qoder:        rt.qoder,
		Minimax:      rt.minimax,
		Loomy:        rt.loomy,
		CreditHistory: rt.creditHist,
		AuthDir:      cfg.AuthDir,
		APIKey:       cfg.APIKey,
		PanelUser:    cfg.PanelUser,
		PanelPass:    cfg.PanelPass,
		RedisMode:    rt.redisMode,
		StickyCount:  rt.sessCount,
		Version:      appVersion,
		Live:         rt.live,
		Usage:        rt.usageRec,
		RequestLog:   rt.requestLog,
		// 模型上限探测数据（与 state 文件同目录，缺省 data/output_probes.json）。
		ProbeFile:  stateSibling(cfg.StateFile, "output_probes.json"),
		ConfigPath: configPath,
		LoadConfig: func() (any, error) {
			c, err := Load(configPath)
			if err != nil {
				return nil, err
			}
			// 签到设置权威在 account-settings.json：回显时覆盖，避免两个来源漂移。
			rt.settings.mu.Lock()
			c.Schedule.CheckinHours = append([]int(nil), rt.settings.schedule.Hours...)
			c.Schedule.CheckinEnabled = rt.settings.schedule.Enabled
			rt.settings.mu.Unlock()
			return c, nil
		},
		SaveConfig: func(raw []byte) ([]string, error) {
			changes, err := saveConfig(raw, configPath, rt.live, rt.p, rt.up, rt.sch)
			if err != nil {
				return nil, err
			}
			// 签到设置不受网关配置保存影响：config.json 里的 checkin_* 是同步副本，
			// 权威在 account-settings.json，保存后立即恢复其排程状态。
			rt.settings.mu.Lock()
			hours := append([]int(nil), rt.settings.schedule.Hours...)
			enabled := rt.settings.schedule.Enabled
			refresh := rt.settings.schedule.RefreshAfterCheckin
			rt.settings.mu.Unlock()
			rt.sch.ReconfigureCheckin(hours, !enabled)
			rt.sch.SetCheckinRefreshEnabled(refresh)
			return changes, nil
		},
		LoadCheckin: rt.settings.load,
		SaveCheckin: func(raw []byte) error {
			if _, err := rt.settings.save(raw, rt.sch); err != nil {
				return err
			}
			applySideRefresh(rt)
			return nil
		},
	})
	// 成长任务队列与任务补领清扫回调（调度器只管时点；账号模式下对应任务
	// 族被禁用，回调不会被触发）。
	rt.sch.SetGrowthHook(rt.pn.RunGrowthQueueOnce)
	rt.sch.SetTaskSweepHook(func() { rt.pn.RunTaskSweepOnce() })
	rt.panelLogMu = log.Writer()
	log.SetOutput(io.MultiWriter(rt.panelLogMu, rt.pn.Logs()))
	if gateway {
		server.SetChatLogOutput(io.MultiWriter(os.Stdout, rt.pn.Logs()))
	}
	return rt, nil
}

// applySideRefresh 签到后是否刷新旁路账号余额（随签到设置热更新）。
func applySideRefresh(rt *appRuntime) {
	rt.settings.mu.Lock()
	refresh := rt.settings.schedule.RefreshAfterCheckin
	rt.settings.mu.Unlock()
	rt.trae.SetRefreshEnabled(refresh)
	rt.qoder.SetRefreshEnabled(refresh)
	rt.minimax.SetRefreshEnabled(refresh)
	rt.loomy.SetRefreshEnabled(refresh)
}

// logSchedulerStatus 启动日志：说明各任务族的实际生效状态。
func logSchedulerStatus(rt *appRuntime) {
	cfg := rt.cfg
	s := rt.settings.schedule
	if !s.Enabled {
		log.Printf("签到已禁用（签到设置 checkin_enabled=false）")
	} else {
		log.Printf("签到已启用：%v 点（WorkBuddy + Trae + MiniMax + Loomy 同批；Qoder 活动轮次独立排程）", s.Hours)
	}
	if !rt.gateway {
		log.Printf("账号管理模式：/v1/* 转发、Redis 镜像、用量记录未启用（-gateway 开启网关）")
		return
	}
	switch {
	case !cfg.Schedule.TravelEnabled:
		log.Printf("猫猫旅行已禁用（schedule.travel_enabled=false）")
	default:
		log.Printf("猫猫旅行已启用：%v 点", cfg.Schedule.TravelHours)
	}
	switch {
	case !cfg.Schedule.ActivityEnabled:
		log.Printf("活跃上报已禁用（schedule.activity_enabled=false）")
	default:
		log.Printf("活跃上报已启用：%v 点", cfg.Schedule.ActivityHours)
	}
	if !cfg.Schedule.KeepaliveEnabled {
		log.Printf("token 保活已禁用（schedule.keepalive_enabled=false）")
	} else {
		log.Printf("token 保活已启用：%v 点", cfg.Schedule.KeepaliveHours)
	}
	switch {
	case !cfg.Schedule.BlackcatEnabled:
		log.Printf("夜猫子已禁用（schedule.blackcat_enabled=false）")
	default:
		log.Printf("夜猫子已启用：%v 点（23:00–08:00 窗口）", cfg.Schedule.BlackcatHours)
	}
	switch {
	case !cfg.Schedule.GrowthEnabled:
		log.Printf("成长任务自动排程已禁用（schedule.growth_enabled=false）")
	default:
		log.Printf("成长任务自动排程已启用：%v 点", cfg.Schedule.GrowthHours)
	}
	if !cfg.Schedule.StreakEnabled {
		log.Printf("连登管家（自动兑换/抽奖）已禁用（schedule.streak_enabled=false）")
	}
	switch {
	case !cfg.Schedule.BalanceRefreshEnabled:
		log.Printf("余额后台刷新已禁用（schedule.balance_refresh_enabled=false）")
	case cfg.BalanceRefreshInterval > 0:
		log.Printf("余额后台刷新：每 %s（签到时点照常额外刷新）", cfg.BalanceRefreshInterval)
	}
	switch {
	case !cfg.Schedule.TaskSweepEnabled:
		log.Printf("任务补领清扫已禁用（schedule.task_sweep_enabled=false）")
	case cfg.TaskSweepInterval > 0:
		log.Printf("任务补领清扫：每 %s", cfg.TaskSweepInterval)
	}
	if cfg.Schedule.IncludeDisabledInTasks {
		log.Printf("保号任务覆盖禁用账号（schedule.include_disabled_in_tasks=true）")
	}
}

// handler 返回根 HTTP Handler：网关模式由 server.Handler 承载 /v1/* 与 /panel/；
// 账号模式仅面板 + 健康检查，不开放 /v1/*。
func (rt *appRuntime) handler() http.Handler {
	if rt.gateway {
		return server.NewHandler(server.Config{
			Pool:         rt.p,
			Upstream:     rt.up,
			APIKey:       rt.cfg.APIKey,
			Session:      rt.sess,
			StickyCount:  rt.sessCount,
			RedisMode:    rt.redisMode,
			Instance:     instanceFingerprint(rt.configPath, rt.cfg),
			SoftCooldown: rt.cfg.SoftRateDur,
			Panel:        rt.pn,
			Live:         rt.live,
			Usage:        rt.usageRec,
			RequestLog:   rt.requestLog,
			PromptMode:   rt.cfg.Prompt.Mode,
			PromptText:   rt.cfg.PromptText,
			RecordClientInfo: rt.cfg.Logging.RequestClientInfo,
			GlobalEnabled: rt.cfg.Global.Enabled,
			ModelsHidden:  rt.cfg.Models.Hidden,
		})
	}
	return accountHandler(rt.pn, rt.healthPayload)
}

// healthPayload 统一服务身份与能力（桌面窗口据此识别同一份账号数据）。
func (rt *appRuntime) healthPayload() any {
	return map[string]any{
		"service":  "workbuddy-console",
		"ok":       true,
		"gateway":  rt.gateway,
		"version":  appVersion,
		"instance": instanceFingerprint(rt.configPath, rt.cfg),
	}
}

// Close 优雅停机：停会话 GC、落盘用量、关请求归档、关面板登录管理，
// 最后 pool.Close()（最后一次 Flush → SaveState 提交到 store）→ store.Close()
// （排空在途异步写，最后一笔 Redis 镜像必须写完才关连接）。
func (rt *appRuntime) Close() {
	if rt.sess != nil {
		rt.sess.StopGC()
	}
	if rt.usageRec != nil {
		rt.usageRec.Stop()
	}
	if rt.requestLog != nil {
		rt.requestLog.Close()
	}
	rt.pn.Close()
	log.SetOutput(rt.panelLogMu)
	rt.p.Close()
	if rt.store != nil {
		_ = rt.store.Close()
	}
}

// accountHandler 账号模式的根路由：面板 + 统一健康检查（声明服务身份与能力，
// 供桌面窗口 / 宿主识别同一份账号数据）。不注册任何 /v1/* 转发。
func accountHandler(pn *panel.Panel, health func() any) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/panel/", pn)
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/panel/", http.StatusSeeOther)
	})
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		raw, _ := json.Marshal(health())
		_, _ = w.Write(raw)
	})
	return mux
}

// runUnified 统一服务入口：账号管理与网关共用（gateway 决定是否装配网关模块）。
func runUnified(configPath string, cfg *Config, gateway bool) error {
	return startUnifiedWithListener(context.Background(), configPath, cfg, nil, gateway)
}

// startUnifiedWithListener 带监听器启动（桌面窗口复用同配置的已有服务时传入）。
func startUnifiedWithListener(parent context.Context, configPath string, cfg *Config, listener net.Listener, gateway bool) error {
	if listener == nil {
		var err error
		listener, err = net.Listen("tcp", cfg.Listen)
		if err != nil {
			return err
		}
	}
	defer listener.Close()
	rt, err := newAppRuntime(configPath, cfg, gateway)
	if err != nil {
		return err
	}
	defer rt.Close()

	ctx, stop := signal.NotifyContext(parent, os.Interrupt, syscall.SIGTERM)
	defer stop()
	go rt.sch.Run(ctx)
	if gateway {
		rt.sch.StartBalanceRefresh(ctx, cfg.BalanceRefreshInterval)
		rt.sch.StartTaskSweep(ctx, cfg.TaskSweepInterval)
		// 启动即预热模型积分倍率表（网关转发需要；异步，失败仅记日志）。
		go warmModelRates(ctx, rt.up, rt.p)
	}
	// Qoder 活动轮次排程（10:00 起按签到时点；两类模式都跑）。
	go runQoderSchedule(ctx, rt.settings, rt.qoder)
	// 启动补签：签到开启且 catchup_on_start 时立即补一轮（含旁路平台）。
	if rt.settings.schedule.Enabled && rt.settings.schedule.CatchupOnStart {
		go rt.sch.RunCheckinNow()
		go rt.sch.RunCheckinHook()
		go func() {
			qctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
			defer cancel()
			rt.qoder.RunAll(qctx, "checkin")
		}()
	}

	srv := &http.Server{
		Addr:    cfg.Listen,
		Handler: rt.handler(),
		ReadHeaderTimeout: 30 * time.Second,
		// ReadTimeout 覆盖整个请求读取（含 body 上传）：防慢速 body 拖死连接。
		// 缺省 300s；server.read_timeout="0" 显式关闭。改动需重启进程。
		ReadTimeout: cfg.ServerReadTimeoutDur,
		IdleTimeout: 120 * time.Second,
	}
	go func() {
		<-ctx.Done()
		rt.p.Flush() // 信号触发：先落盘再做优雅停机
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()
	mode := "账号管理"
	if gateway {
		mode = "账号管理 + API 网关"
	}
	log.Printf("%s已启动：http://127.0.0.1%s/panel/（管理密钥见 %s；时区 Asia/Shanghai）",
		mode, panelListenPath(cfg.Listen), configPath)
	err = srv.Serve(listener)
	if err == http.ErrServerClosed {
		return nil
	}
	return err
}
