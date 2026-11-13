package panel

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/linguo2625469/workbuddy2api-panel/internal/antigravity"
	"github.com/linguo2625469/workbuddy2api-panel/internal/doubao"
)

func webLoginRequest(p *Panel, provider, method, path, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "http://127.0.0.1:7863/panel/api/"+provider+"/login/"+path, strings.NewReader(body))
	r.RemoteAddr = "127.0.0.1:45678"
	r.Header.Set("Origin", "http://127.0.0.1:7863")
	r.Header.Set("Authorization", "Bearer test-key")
	w := httptest.NewRecorder()
	p.ServeHTTP(w, r)
	return w
}

func waitWebLogin(t *testing.T, p *Panel, provider, state string) *httptest.ResponseRecorder {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		w := webLoginRequest(p, provider, "GET", "poll?state="+state, "")
		if strings.Contains(w.Body.String(), `"done":true`) {
			return w
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("web login did not finish")
	return nil
}

func newWebLoginTestPanel(t *testing.T) *Panel {
	t.Helper()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/token":
			r.ParseForm()
			if r.Form.Get("grant_type") != "authorization_code" || r.Form.Get("code_verifier") == "" {
				t.Error("missing OAuth code exchange")
			}
			fmt.Fprint(w, `{"access_token":"private-access","refresh_token":"private-refresh","expires_in":3600}`)
		case "/userinfo":
			if r.Header.Get("Authorization") != "Bearer private-access" {
				t.Error("missing authorization")
			}
			fmt.Fprint(w, `{"email":"web@example.com"}`)
		case "/v1internal:loadCodeAssist":
			fmt.Fprint(w, `{"cloudaicompanionProject":"web-project"}`)
		case "/passport/account/info/v2/":
			cookie, err := r.Cookie("sessionid")
			if err != nil || cookie.Value != "private-session" {
				t.Error("browser session not passed to store")
			}
			fmt.Fprint(w, `{"message":"success","data":{"user_id":"123456","screen_name":"网页登录账号"}}`)
		case "/alice/commerce/sale/subscription/quota/summary/":
			fmt.Fprint(w, `{"code":0,"data":{"incentive_quota_section":{"window_limit_groups":[]}}}`)
		default:
			t.Errorf("unexpected endpoint %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(upstream.Close)
	c := antigravity.NewClient()
	c.HTTP, c.TokenURL, c.UserInfoURL, c.Base = upstream.Client(), upstream.URL+"/token", upstream.URL+"/userinfo", upstream.URL
	a, err := antigravity.Open(filepath.Join(t.TempDir(), "antigravity.json"), c)
	if err != nil {
		t.Fatal(err)
	}
	d, err := doubao.Open(filepath.Join(t.TempDir(), "doubao.json"), &doubao.Client{HTTP: upstream.Client(), Base: upstream.URL})
	if err != nil {
		t.Fatal(err)
	}
	p := New(Config{AccountsOnly: true, APIKey: "test-key", Antigravity: a, Doubao: d})
	p.webLogin.find = func() string { return "fixture-browser" }
	t.Cleanup(p.Close)
	return p
}

func TestAntigravityWebLoginAutomaticallySaves(t *testing.T) {
	p := newWebLoginTestPanel(t)
	w := webLoginRequest(p, "antigravity", "POST", "start", `{"nickname":"网页授权"}`)
	var start struct{ State, URL string }
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &start) != nil || len(start.State) != 48 || start.URL == "" {
		t.Fatalf("start failed: %s", w.Body.String())
	}
	u, _ := url.Parse(start.URL)
	q := u.Query()
	callback := q.Get("redirect_uri") + "?" + url.Values{"state": {q.Get("state")}, "code": {"private-code"}}.Encode()
	resp, err := http.Get(callback)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	w = waitWebLogin(t, p, "antigravity", start.State)
	if !strings.Contains(w.Body.String(), `"status":"success"`) || len(p.cfg.Antigravity.List()) != 1 || p.cfg.Antigravity.List()[0].Nickname != "网页授权" {
		t.Fatalf("account not saved: %s", w.Body.String())
	}
	for _, secret := range []string{"private-access", "private-refresh", "private-code", "code_verifier"} {
		if strings.Contains(w.Body.String(), secret) {
			t.Fatal("login poll leaked credentials")
		}
	}
}

func TestDoubaoWebLoginSaveUpdateAndIdentityProtection(t *testing.T) {
	p := newWebLoginTestPanel(t)
	p.webLogin.runDoubao = func(context.Context, string) (string, error) { return "sessionid=private-session", nil }
	for i := 0; i < 2; i++ {
		body := `{"nickname":"网页账号"}`
		if i == 1 {
			body = fmt.Sprintf(`{"uid":%q,"nickname":"更新备注"}`, p.cfg.Doubao.List()[0].UID)
		}
		w := webLoginRequest(p, "doubao", "POST", "start", body)
		var start struct{ State string }
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &start) != nil {
			t.Fatalf("start failed: %s", w.Body.String())
		}
		w = waitWebLogin(t, p, "doubao", start.State)
		if !strings.Contains(w.Body.String(), `"status":"success"`) || strings.Contains(w.Body.String(), "private-session") || len(p.cfg.Doubao.List()) != 1 {
			t.Fatalf("login failed: %s", w.Body.String())
		}
		if webLoginRequest(p, "antigravity", "GET", "poll?state="+start.State, "").Code != 404 {
			t.Fatal("cross-provider poll accepted")
		}
	}
	if p.cfg.Doubao.List()[0].Nickname != "更新备注" {
		t.Fatal("reauthorization did not update account")
	}
	if webLoginRequest(p, "doubao", "POST", "start", `{"uid":"missing"}`).Code != 404 {
		t.Fatal("missing account accepted")
	}
}

func TestWebLoginAuthenticationStrictFormsAndCancellation(t *testing.T) {
	p := newWebLoginTestPanel(t)
	for _, provider := range []string{"antigravity", "doubao"} {
		for _, body := range []string{`{} {}`, `{"url":"https://example.com"}`, `{"cookie":"private-session"}`} {
			if webLoginRequest(p, provider, "POST", "start", body).Code != 400 {
				t.Fatal("invalid login form accepted")
			}
		}
		for _, mutate := range []func(*http.Request){
			func(r *http.Request) { r.Header.Del("Authorization") },
			func(r *http.Request) { r.RemoteAddr = "192.0.2.10:1234" },
			func(r *http.Request) { r.Header.Set("Origin", "https://example.com") },
			func(r *http.Request) { r.Header.Set("X-Forwarded-For", "127.0.0.1") },
		} {
			r := httptest.NewRequest("POST", "http://127.0.0.1:7863/panel/api/"+provider+"/login/start", nil)
			r.RemoteAddr = "127.0.0.1:1234"
			r.Header.Set("Authorization", "Bearer test-key")
			mutate(r)
			w := httptest.NewRecorder()
			p.ServeHTTP(w, r)
			if w.Code != 401 && w.Code != 403 {
				t.Fatalf("unsafe request accepted: %d", w.Code)
			}
		}
	}
	var cancelled atomic.Bool
	p.webLogin.runDoubao = func(ctx context.Context, _ string) (string, error) {
		<-ctx.Done()
		cancelled.Store(true)
		return "", ctx.Err()
	}
	w := webLoginRequest(p, "doubao", "POST", "start", "{}")
	var start struct{ State string }
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &start) != nil {
		t.Fatal("start failed")
	}
	if webLoginRequest(p, "doubao", "POST", "start", "{}").Code != 409 {
		t.Fatal("duplicate login allowed")
	}
	webLoginRequest(p, "doubao", "POST", "cancel?state="+start.State, "")
	p.Close()
	if !cancelled.Load() || len(p.cfg.Doubao.List()) != 0 {
		t.Fatal("cancelled login saved an account or left browser running")
	}
	if !strings.Contains(waitWebLogin(t, p, "doubao", start.State).Body.String(), `"status":"cancelled"`) {
		t.Fatal("cancelled status lost")
	}
}
