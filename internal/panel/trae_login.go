package panel

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/linguo2625469/workbuddy2api-panel/internal/trae"
	"github.com/linguo2625469/workbuddy2api-panel/internal/traelogin"
)

type traeLoginState struct {
	State      string       `json:"state"`
	Status     string       `json:"status"`
	Done       bool         `json:"done"`
	Error      string       `json:"error,omitempty"`
	Account    *trae.Status `json:"account,omitempty"`
	Warnings   []string     `json:"warnings,omitempty"`
	cancel     context.CancelFunc
	created    time.Time
	expectedID string
}

type traeLoginManager struct {
	mu       sync.Mutex
	wg       sync.WaitGroup
	closed   bool
	sessions map[string]*traeLoginState
	find     func() string
	run      func(context.Context, string) (traelogin.Credentials, error)
}

func newTraeLoginManager() *traeLoginManager {
	return &traeLoginManager{sessions: map[string]*traeLoginState{}, find: traelogin.BrowserPath, run: traelogin.Run}
}

// Desktop login only runs on the machine serving this local panel. Reject
// forwarded requests and cross-site requests even when API authentication is off.
func localDesktopRequest(r *http.Request) bool {
	host := r.Host
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.Trim(host, "[]")
	localHost := strings.EqualFold(host, "localhost") || net.ParseIP(host).IsLoopback()
	remote, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil || !localHost || !net.ParseIP(remote).IsLoopback() {
		return false
	}
	for _, header := range []string{"Forwarded", "X-Forwarded-For", "X-Forwarded-Host", "X-Real-IP"} {
		if r.Header.Get(header) != "" {
			return false
		}
	}
	if origin := r.Header.Get("Origin"); origin != "" {
		u, err := url.Parse(origin)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || !strings.EqualFold(u.Host, r.Host) {
			return false
		}
	}
	return r.Header.Get("Sec-Fetch-Site") != "cross-site"
}

func (p *Panel) traeLoginAvailable(w http.ResponseWriter, r *http.Request) {
	available := false
	reason := "请在运行面板的电脑上，使用 localhost 或 127.0.0.1 打开面板后登录"
	if localDesktopRequest(r) {
		available = p.cfg.Trae != nil && p.traeLogin.find() != ""
		reason = "未找到桌面版 Edge / Chrome，请安装浏览器，或使用登录凭证导入"
	}
	if available {
		reason = "登录后自动添加账号，无需复制 Token"
	}
	writeJSON(w, http.StatusOK, map[string]any{"available": available, "message": reason})
}

func (p *Panel) traeLoginStart(w http.ResponseWriter, r *http.Request) {
	if !p.traeReady(w) {
		return
	}
	if !localDesktopRequest(r) {
		writeErr(w, http.StatusForbidden, "直接登录仅限在运行面板的本机打开")
		return
	}
	var input struct {
		UID string `json:"uid"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&input); err != nil && err != io.EOF {
		writeErr(w, http.StatusBadRequest, "Trae 登录参数无效")
		return
	}
	if err := dec.Decode(new(any)); err != io.EOF {
		writeErr(w, http.StatusBadRequest, "Trae 登录参数无效")
		return
	}
	if input.UID != "" {
		found := false
		for _, a := range p.cfg.Trae.List() {
			if a.UID == input.UID {
				found = true
				break
			}
		}
		if !found {
			writeErr(w, http.StatusNotFound, "Trae 账号不存在")
			return
		}
	}
	m := p.traeLogin
	executable := m.find()
	if executable == "" {
		writeErr(w, http.StatusNotImplemented, "未找到 Edge / Chrome，请安装桌面浏览器或使用凭证导入")
		return
	}
	id := make([]byte, 24)
	if _, err := rand.Read(id); err != nil {
		writeErr(w, http.StatusInternalServerError, "创建登录会话失败")
		return
	}
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		writeErr(w, http.StatusServiceUnavailable, "服务正在关闭，请稍后重试")
		return
	}
	for key, previous := range m.sessions {
		if time.Since(previous.created) > loginTTL {
			previous.cancel()
			delete(m.sessions, key)
		} else if !previous.Done {
			m.mu.Unlock()
			writeErr(w, http.StatusConflict, "已有 Trae 登录窗口，请先完成或取消登录")
			return
		}
	}
	// Bound completed sessions independently of how often clients poll them.
	if len(m.sessions) >= 16 {
		var oldestKey string
		var oldest time.Time
		for key, previous := range m.sessions {
			if oldest.IsZero() || previous.created.Before(oldest) {
				oldestKey, oldest = key, previous.created
			}
		}
		delete(m.sessions, oldestKey)
	}
	ctx, cancel := context.WithTimeout(context.Background(), loginTTL)
	session := &traeLoginState{State: hex.EncodeToString(id), Status: "waiting", cancel: cancel, created: time.Now(), expectedID: input.UID}
	m.sessions[session.State] = session
	m.wg.Add(1)
	m.mu.Unlock()
	go p.finishTraeLogin(ctx, session, executable)
	writeJSON(w, http.StatusOK, map[string]any{"state": session.State, "status": "waiting"})
}

func (p *Panel) finishTraeLogin(ctx context.Context, session *traeLoginState, executable string) {
	defer p.traeLogin.wg.Done()
	defer session.cancel()
	m := p.traeLogin
	credentials, err := m.run(ctx, executable)
	if err == nil {
		err = ctx.Err()
	}
	var account trae.Status
	var warnings []string
	validating := false
	if err == nil {
		m.mu.Lock()
		if !session.Done {
			session.Status = "saving"
		}
		m.mu.Unlock()
		if ctx.Err() == nil {
			validating = true
			verificationCtx, stopVerification := context.WithTimeout(ctx, 90*time.Second)
			account, warnings, err = p.cfg.Trae.Reauthorize(verificationCtx, trae.Account{Token: credentials.Token, Session: credentials.Session, DeviceID: credentials.DeviceID, OAuth: credentials.OAuth}, session.expectedID)
			if errors.Is(verificationCtx.Err(), context.DeadlineExceeded) {
				err = errors.New("验证账号超时，请检查网络后重试")
			}
			stopVerification()
		} else {
			err = ctx.Err()
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if session.Done {
		return
	}
	session.Done = true
	if err != nil {
		session.Status = "error"
		// Never return browser diagnostics or credentials to the HTTP client.
		switch {
		case errors.Is(ctx.Err(), context.DeadlineExceeded):
			session.Error = "登录已超时，请重新开始"
		case errors.Is(ctx.Err(), context.Canceled):
			session.Error = "登录已取消"
		case validating:
			// Store.Add only returns fixed protocol/storage errors, never
			// upstream bodies or credentials. Keep the actionable reason.
			session.Error = "已获取登录信息，但账号验证或保存失败：" + err.Error()
		case errors.Is(err, traelogin.ErrLaunch), errors.Is(err, traelogin.ErrWindowClosed), errors.Is(err, traelogin.ErrCallbackPort), trae.IsOAuthError(err):
			session.Error = err.Error()
		default:
			session.Error = "读取 Trae 登录信息失败，请重新打开登录窗口"
		}
		log.Printf("trae: %s", session.Error)
		return
	}
	session.Status, session.Account, session.Warnings = "success", &account, warnings
	log.Printf("trae: 浏览器登录成功，账号已添加或更新")
}

func (p *Panel) traeLoginPoll(w http.ResponseWriter, r *http.Request) {
	if !localDesktopRequest(r) {
		writeErr(w, http.StatusForbidden, "直接登录仅限本机访问")
		return
	}
	m := p.traeLogin
	m.mu.Lock()
	defer m.mu.Unlock()
	session := m.sessions[r.URL.Query().Get("state")]
	if session == nil {
		writeErr(w, http.StatusNotFound, "登录会话不存在，请重新开始")
		return
	}
	writeJSON(w, http.StatusOK, session)
}

func (p *Panel) traeLoginCancel(w http.ResponseWriter, r *http.Request) {
	if !localDesktopRequest(r) {
		writeErr(w, http.StatusForbidden, "直接登录仅限本机访问")
		return
	}
	m := p.traeLogin
	m.mu.Lock()
	session := m.sessions[r.URL.Query().Get("state")]
	if session != nil && !session.Done {
		session.cancel()
		session.Done, session.Status, session.Error = true, "cancelled", "登录已取消"
	}
	m.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// Close cancels interactive login windows when the server shuts down.
func (p *Panel) Close() {
	if p.webLogin != nil {
		p.webLogin.close()
	}
	if p.desktop != nil {
		p.desktop.Close()
	}
	p.traeLogin.mu.Lock()
	p.traeLogin.closed = true
	for _, session := range p.traeLogin.sessions {
		session.cancel()
	}
	p.traeLogin.mu.Unlock()
	p.traeLogin.wg.Wait()
}
