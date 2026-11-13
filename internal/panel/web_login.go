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
	"strings"
	"sync"
	"time"

	"github.com/linguo2625469/workbuddy2api-panel/internal/doubao"
)

type webLoginState struct {
	State    string `json:"state"`
	Status   string `json:"status"`
	URL      string `json:"url,omitempty"`
	Done     bool   `json:"done"`
	Error    string `json:"error,omitempty"`
	Account  any    `json:"account,omitempty"`
	provider string
	created  time.Time
	cancel   context.CancelFunc
}

type webLoginManager struct {
	mu        sync.Mutex
	wg        sync.WaitGroup
	closed    bool
	sessions  map[string]*webLoginState
	find      func() string
	runDoubao func(context.Context, string) (string, error)
}

func newWebLoginManager() *webLoginManager {
	return &webLoginManager{sessions: map[string]*webLoginState{}, find: doubao.BrowserPath, runDoubao: doubao.RunBrowserLogin}
}

func (p *Panel) webLoginRoutes(provider string) {
	prefix := "/panel/api/" + provider + "/login/"
	p.mux.HandleFunc("POST "+prefix+"start", p.withAuth(func(w http.ResponseWriter, r *http.Request) { p.webLoginStart(w, r, provider) }))
	p.mux.HandleFunc("GET "+prefix+"poll", p.withAuth(func(w http.ResponseWriter, r *http.Request) { p.webLoginPoll(w, r, provider) }))
	p.mux.HandleFunc("POST "+prefix+"cancel", p.withAuth(func(w http.ResponseWriter, r *http.Request) { p.webLoginCancel(w, r, provider) }))
}

func (p *Panel) webLoginStart(w http.ResponseWriter, r *http.Request, provider string) {
	if !localDesktopRequest(r) {
		writeErr(w, 403, "网页授权请在运行面板的电脑上，使用 localhost 或 127.0.0.1 打开面板")
		return
	}
	if provider == "antigravity" && !p.antigravityReady(w) || provider == "doubao" && !p.doubaoReady(w) {
		return
	}
	var input struct {
		UID      string `json:"uid"`
		Nickname string `json:"nickname"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	err := dec.Decode(&input)
	if err != nil && err != io.EOF || dec.Decode(new(any)) != io.EOF || len(input.UID) > 256 || len(strings.TrimSpace(input.Nickname)) > 240 {
		writeErr(w, 400, "网页登录参数无效")
		return
	}
	if input.UID != "" {
		found := false
		if provider == "antigravity" {
			for _, a := range p.cfg.Antigravity.List() {
				if a.UID == input.UID {
					found = true
					break
				}
			}
		} else {
			for _, a := range p.cfg.Doubao.List() {
				if a.UID == input.UID {
					found = true
					break
				}
			}
		}
		if !found {
			writeErr(w, 404, "需要重新授权的账号不存在")
			return
		}
	}
	m := p.webLogin
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		writeErr(w, 503, "服务正在关闭，请稍后重试")
		return
	}
	for key, old := range m.sessions {
		if time.Since(old.created) > loginTTL {
			old.cancel()
			delete(m.sessions, key)
		} else if old.provider == provider && !old.Done {
			writeErr(w, 409, "已有授权正在进行，请先完成或取消")
			return
		}
	}
	var id [24]byte
	if _, err := rand.Read(id[:]); err != nil {
		writeErr(w, 500, "创建授权会话失败")
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), loginTTL)
	session := &webLoginState{State: hex.EncodeToString(id[:]), Status: "waiting", provider: provider, created: time.Now(), cancel: cancel}
	var run func(context.Context) (any, error)
	cleanup := func() {}
	if provider == "antigravity" {
		flow, err := p.cfg.Antigravity.BeginOAuth()
		if err != nil {
			cancel()
			writeErr(w, 400, err.Error())
			return
		}
		session.URL, cleanup = flow.URL, flow.Close
		run = func(ctx context.Context) (any, error) {
			a, err := flow.Wait(ctx)
			if err != nil {
				return nil, err
			}
			p.webLoginSaving(session)
			a.Nickname = input.Nickname
			return p.cfg.Antigravity.Add(ctx, a, input.UID)
		}
	} else {
		executable := m.find()
		if executable == "" {
			cancel()
			writeErr(w, 501, "未找到 Edge / Chrome，请先安装桌面浏览器")
			return
		}
		run = func(ctx context.Context) (any, error) {
			cookie, err := m.runDoubao(ctx, executable)
			if err != nil {
				return nil, err
			}
			p.webLoginSaving(session)
			return p.cfg.Doubao.Add(ctx, doubao.Account{Cookie: cookie, Nickname: input.Nickname}, input.UID)
		}
	}
	session.cancel = func() { cancel(); cleanup() }
	if len(m.sessions) >= 16 {
		var oldest string
		for key, old := range m.sessions {
			if old.Done && (oldest == "" || old.created.Before(m.sessions[oldest].created)) {
				oldest = key
			}
		}
		if oldest != "" {
			delete(m.sessions, oldest)
		}
	}
	m.sessions[session.State] = session
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		defer cancel()
		defer cleanup()
		account, err := run(ctx)
		if err == nil {
			err = ctx.Err()
		}
		m.mu.Lock()
		defer m.mu.Unlock()
		if session.Done {
			return
		}
		session.Done, session.URL = true, ""
		if err != nil {
			session.Status = "error"
			switch {
			case errors.Is(ctx.Err(), context.DeadlineExceeded):
				session.Error = "授权已超时，请重新开始"
			case errors.Is(ctx.Err(), context.Canceled):
				session.Error = "授权已取消"
			default:
				session.Error = err.Error() // All flow/store errors exclude upstream bodies and credentials.
			}
			log.Printf("%s: 网页授权失败：%s", provider, session.Error)
		} else {
			session.Status, session.Account = "success", account
			log.Printf("%s: 网页授权成功，账号已保存", provider)
		}
	}()
	writeJSON(w, 200, session)
}

func (p *Panel) webLoginSaving(session *webLoginState) {
	p.webLogin.mu.Lock()
	defer p.webLogin.mu.Unlock()
	if !session.Done {
		session.Status = "saving"
	}
}

func (p *Panel) webLoginPoll(w http.ResponseWriter, r *http.Request, provider string) {
	if !localDesktopRequest(r) {
		writeErr(w, 403, "网页授权状态仅限本机访问")
		return
	}
	m := p.webLogin
	m.mu.Lock()
	defer m.mu.Unlock()
	session := m.sessions[r.URL.Query().Get("state")]
	if session == nil || session.provider != provider || time.Since(session.created) > loginTTL {
		writeErr(w, 404, "授权会话不存在或已过期，请重新开始")
		return
	}
	writeJSON(w, 200, session)
}

func (p *Panel) webLoginCancel(w http.ResponseWriter, r *http.Request, provider string) {
	if !localDesktopRequest(r) {
		writeErr(w, 403, "取消网页授权仅限本机访问")
		return
	}
	m := p.webLogin
	m.mu.Lock()
	defer m.mu.Unlock()
	if session := m.sessions[r.URL.Query().Get("state")]; session != nil && session.provider == provider && !session.Done {
		session.cancel()
		session.Done, session.Status, session.Error, session.URL = true, "cancelled", "授权已取消", ""
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (m *webLoginManager) close() {
	m.mu.Lock()
	m.closed = true
	for _, session := range m.sessions {
		session.cancel()
	}
	m.mu.Unlock()
	m.wg.Wait()
}
