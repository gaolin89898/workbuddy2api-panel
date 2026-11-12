package panel

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/linguo2625469/workbuddy2api-panel/internal/minimax"
)

type minimaxLogin struct {
	auth     *minimax.DeviceAuthorization
	created  time.Time
	expected string
	account  *minimax.Status
	warnings []string
	busy     bool
}
type minimaxLoginManager struct {
	mu       sync.Mutex
	sessions map[string]*minimaxLogin
	client   *minimax.Client
}

func newMinimaxLoginManager() *minimaxLoginManager {
	return &minimaxLoginManager{sessions: map[string]*minimaxLogin{}, client: minimax.NewClient()}
}

func (p *Panel) minimaxRoutes() {
	p.mux.HandleFunc("POST /panel/api/minimax/login/start", p.withAuth(p.minimaxLoginStart))
	p.mux.HandleFunc("GET /panel/api/minimax/login/poll", p.withAuth(p.minimaxLoginPoll))
	p.mux.HandleFunc("POST /panel/api/minimax/login/cancel", p.withAuth(p.minimaxLoginCancel))
	p.mux.HandleFunc("POST /panel/api/minimax/accounts", p.withAuth(p.minimaxAdd))
	p.mux.HandleFunc("POST /panel/api/minimax/accounts/{uid}/{action}", p.withAuth(p.minimaxAction))
	p.mux.HandleFunc("POST /panel/api/minimax/checkin_all", p.withAuth(p.minimaxCheckinAll))
	p.mux.HandleFunc("POST /panel/api/minimax/balance_all", p.withAuth(p.minimaxBalanceAll))
}
func (p *Panel) minimaxAccounts() []minimax.Status {
	if p.cfg.Minimax == nil {
		return []minimax.Status{}
	}
	return p.cfg.Minimax.List()
}
func (p *Panel) minimaxReady(w http.ResponseWriter) bool {
	if p.cfg.Minimax == nil {
		writeErr(w, 501, "MiniMax 账号管理未启用")
		return false
	}
	return true
}
func decodeMinimax(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if dec.Decode(v) != nil || dec.Decode(new(any)) != io.EOF {
		writeErr(w, 400, "MiniMax 表单格式无效")
		return false
	}
	return true
}
func (p *Panel) minimaxAdd(w http.ResponseWriter, r *http.Request) {
	if !p.minimaxReady(w) {
		return
	}
	var input struct {
		Credential string `json:"credential"`
		Nickname   string `json:"nickname"`
		UID        string `json:"uid"`
		AuthKind   string `json:"auth_kind"`
	}
	if !decodeMinimax(w, r, &input) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()
	a, warnings, err := p.cfg.Minimax.Add(ctx, minimax.Account{Credential: input.Credential, Nickname: input.Nickname, AuthKind: input.AuthKind}, input.UID)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	log.Printf("minimax: 账号已添加或更新")
	writeJSON(w, 200, map[string]any{"ok": true, "account": a, "warnings": warnings})
}
func (p *Panel) minimaxAction(w http.ResponseWriter, r *http.Request) {
	if !p.minimaxReady(w) {
		return
	}
	action := r.PathValue("action")
	if action != "checkin" && action != "balance" && action != "remove" && action != "disable" && action != "revive" {
		http.NotFound(w, r)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 100*time.Second)
	defer cancel()
	a, warning, err := p.cfg.Minimax.Action(ctx, r.PathValue("uid"), action)
	if errors.Is(err, os.ErrNotExist) {
		writeErr(w, 404, "MiniMax 账号不存在")
		return
	}
	if err != nil {
		writeErr(w, 502, err.Error())
		return
	}
	log.Printf("minimax: %s 完成", action)
	writeJSON(w, 200, map[string]any{"ok": true, "account": a, "checkin_done": a.CheckinDone, "balance_error": warning})
}
func (p *Panel) minimaxCheckinAll(w http.ResponseWriter, r *http.Request) {
	if !p.minimaxReady(w) {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		p.cfg.Minimax.RunAll(ctx, "checkin")
	}()
	log.Printf("minimax: 手动签到已开始")
	writeJSON(w, 200, map[string]any{"ok": true, "started": true})
}
func (p *Panel) minimaxBalanceAll(w http.ResponseWriter, r *http.Request) {
	if !p.minimaxReady(w) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	errs := p.cfg.Minimax.RunAll(ctx, "balance")
	writeJSON(w, 200, map[string]any{"ok": true, "accounts": p.minimaxAccounts(), "errors": errs})
}
func (p *Panel) minimaxLoginStart(w http.ResponseWriter, r *http.Request) {
	if !p.minimaxReady(w) || !desktopLocal(w, r) {
		return
	}
	var input struct {
		UID string `json:"uid"`
	}
	if !decodeMinimax(w, r, &input) {
		return
	}
	if len(input.UID) > 200 {
		writeErr(w, 400, "MiniMax 账号参数无效")
		return
	}
	auth, err := p.minimaxLogin.client.StartDeviceAuthorization(r.Context())
	if err != nil {
		writeErr(w, 502, err.Error())
		return
	}
	var id [24]byte
	if _, err = rand.Read(id[:]); err != nil {
		writeErr(w, 500, "无法创建授权会话")
		return
	}
	state := hex.EncodeToString(id[:])
	m := p.minimaxLogin
	m.mu.Lock()
	defer m.mu.Unlock()
	for key, s := range m.sessions {
		if time.Since(s.created) > 10*time.Minute {
			delete(m.sessions, key)
		}
	}
	if len(m.sessions) >= 16 {
		writeErr(w, 429, "MiniMax 授权窗口过多，请稍后重试")
		return
	}
	m.sessions[state] = &minimaxLogin{auth: auth, created: time.Now(), expected: input.UID}
	url := auth.VerificationURIComplete
	if url == "" {
		url = auth.VerificationURI
	}
	log.Printf("minimax: 浏览器授权已发起")
	writeJSON(w, 200, map[string]any{"state": state, "url": url, "user_code": auth.UserCode, "interval_sec": int(auth.Interval.Seconds()), "expires_in_sec": int(auth.ExpiresIn.Seconds())})
}
func (p *Panel) minimaxLoginPoll(w http.ResponseWriter, r *http.Request) {
	if !p.minimaxReady(w) || !desktopLocal(w, r) {
		return
	}
	m := p.minimaxLogin
	m.mu.Lock()
	state := r.URL.Query().Get("state")
	s := m.sessions[state]
	if s == nil || time.Since(s.created) > 10*time.Minute {
		delete(m.sessions, state)
		m.mu.Unlock()
		writeErr(w, 410, "MiniMax 授权已过期或已取消，请重新获取授权")
		return
	}
	if s.account != nil {
		m.mu.Unlock()
		writeJSON(w, 200, map[string]any{"done": true, "account": s.account, "warnings": s.warnings})
		return
	}
	if s.busy {
		m.mu.Unlock()
		writeJSON(w, 200, map[string]any{"done": false})
		return
	}
	s.busy = true
	auth := s.auth
	expected := s.expected
	m.mu.Unlock()
	defer func() {
		m.mu.Lock()
		s.busy = false
		m.mu.Unlock()
	}()
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	grant, err := m.client.PollDeviceToken(ctx, auth)
	if err != nil {
		// pending / slow_down 对前端表现为"尚未完成"，其余为失败。
		if errors.Is(err, minimax.ErrAuthorizationPending) || errors.Is(err, minimax.ErrSlowDown) {
			writeJSON(w, 200, map[string]any{"done": false})
			return
		}
		writeErr(w, 502, err.Error())
		return
	}
	a, warnings, err := p.cfg.Minimax.Add(ctx, minimax.Account{Credential: grant.AccessToken, RefreshToken: grant.RefreshToken, ExpiresAt: grant.Expiry(time.Now()), AuthKind: minimax.AuthBearer}, expected)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	m.mu.Lock()
	s.account, s.warnings = &a, warnings
	m.mu.Unlock()
	log.Printf("minimax: 浏览器授权完成，账号已保存")
	writeJSON(w, 200, map[string]any{"done": true, "account": a, "warnings": warnings})
}
func (p *Panel) minimaxLoginCancel(w http.ResponseWriter, r *http.Request) {
	if !desktopLocal(w, r) {
		return
	}
	m := p.minimaxLogin
	m.mu.Lock()
	delete(m.sessions, r.URL.Query().Get("state"))
	m.mu.Unlock()
	writeJSON(w, 200, map[string]bool{"ok": true})
}
