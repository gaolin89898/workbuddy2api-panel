package panel

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestPanelLoginAndSessionAuth 覆盖：默认拒绝、错误口令拒绝、登录发会话、
// 会话令牌与 api_key 均可通过面板鉴权（GET /config 在未注入 LoadConfig 时
// 返回 501，鉴权通过与否以 401/非 401 区分，不依赖 Pool）。
func TestPanelLoginAndSessionAuth(t *testing.T) {
	p := New(Config{Version: "test", APIKey: "static-key", PanelUser: "admin", PanelPass: "123456"})
	get := func(bearer string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/panel/api/config", nil)
		if bearer != "" {
			req.Header.Set("Authorization", "Bearer "+bearer)
		}
		p.ServeHTTP(rec, req)
		return rec
	}
	if rec := get(""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("no credential: %d", rec.Code)
	}
	login := func(body string) (int, string) {
		rec := httptest.NewRecorder()
		p.ServeHTTP(rec, httptest.NewRequest("POST", "/panel/api/auth/login", strings.NewReader(body)))
		return rec.Code, rec.Body.String()
	}
	if code, _ := login(`{"username":"admin","password":"wrong"}`); code != http.StatusUnauthorized {
		t.Fatalf("wrong password: %d", code)
	}
	code, body := login(`{"username":"admin","password":"123456"}`)
	if code != http.StatusOK {
		t.Fatalf("login: %d %s", code, body)
	}
	var out struct {
		Token string `json:"token"`
	}
	if json.Unmarshal([]byte(body), &out) != nil || out.Token == "" {
		t.Fatalf("missing token: %s", body)
	}
	if rec := get(out.Token); rec.Code == http.StatusUnauthorized {
		t.Fatalf("session token rejected: %d", rec.Code)
	}
	if rec := get("static-key"); rec.Code == http.StatusUnauthorized {
		t.Fatalf("api_key rejected after panel auth enabled: %d", rec.Code)
	}
	// 未配置面板凭据时登录应明确拒绝（回退 api_key 语义）。
	legacy := New(Config{Version: "test", APIKey: "k"})
	legacy.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("POST", "/panel/api/auth/login", strings.NewReader(`{"username":"admin","password":"123456"}`)))
	rec := httptest.NewRecorder()
	legacy.ServeHTTP(rec, httptest.NewRequest("POST", "/panel/api/auth/login", strings.NewReader(`{"username":"admin","password":"123456"}`)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("login must be refused when panel credentials are empty: %d", rec.Code)
	}
}

// TestPasswordOnlyPanelRejectsAnonymous 锁定 P1 修复：仅配置面板密码（api_key
// 为空）时，匿名请求与错误 Bearer 都必须被拒绝——VerifyBearer 对空 key 恒真，
// 修复前会短路放行绕过面板密码。
func TestPasswordOnlyPanelRejectsAnonymous(t *testing.T) {
	p := New(Config{Version: "test", PanelUser: "admin", PanelPass: "secret"})
	get := func(bearer string) int {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/panel/api/config", nil)
		if bearer != "" {
			req.Header.Set("Authorization", "Bearer "+bearer)
		}
		p.ServeHTTP(rec, req)
		return rec.Code
	}
	if code := get(""); code != http.StatusUnauthorized {
		t.Fatalf("anonymous request must be rejected when only panel password is set: %d", code)
	}
	if code := get("anything"); code != http.StatusUnauthorized {
		t.Fatalf("wrong bearer must be rejected when api_key is empty: %d", code)
	}
	login := func(body string) (int, string) {
		rec := httptest.NewRecorder()
		p.ServeHTTP(rec, httptest.NewRequest("POST", "/panel/api/auth/login", strings.NewReader(body)))
		return rec.Code, rec.Body.String()
	}
	code, body := login(`{"username":"admin","password":"secret"}`)
	if code != http.StatusOK {
		t.Fatalf("login: %d %s", code, body)
	}
	var out struct {
		Token string `json:"token"`
	}
	if json.Unmarshal([]byte(body), &out) != nil || out.Token == "" {
		t.Fatalf("missing token: %s", body)
	}
	if rec := httptest.NewRecorder(); func() int {
		req := httptest.NewRequest("GET", "/panel/api/config", nil)
		req.Header.Set("Authorization", "Bearer "+out.Token)
		p.ServeHTTP(rec, req)
		return rec.Code
	}() == http.StatusUnauthorized {
		t.Fatal("session token rejected in password-only mode")
	}
}

// TestAPIKeyOnlyPanelLoginWithKey 锁定升级兼容：旧配置只有 api_key（面板密码
// 为空）时，登录表单里把 api_key 作为密码即可换取会话；错误密码仍拒绝。
func TestAPIKeyOnlyPanelLoginWithKey(t *testing.T) {
	p := New(Config{Version: "test", APIKey: "sk-upgrade-key"})
	login := func(body string) int {
		rec := httptest.NewRecorder()
		p.ServeHTTP(rec, httptest.NewRequest("POST", "/panel/api/auth/login", strings.NewReader(body)))
		return rec.Code
	}
	if code := login(`{"username":"admin","password":"wrong"}`); code != http.StatusBadRequest {
		t.Fatalf("wrong key in key-only mode: %d", code)
	}
	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, httptest.NewRequest("POST", "/panel/api/auth/login", strings.NewReader(`{"username":"whatever","password":"sk-upgrade-key"}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("api_key as password must issue session: %d %s", rec.Code, rec.Body.String())
	}
	var out struct {
		Token string `json:"token"`
	}
	if json.Unmarshal(rec.Body.Bytes(), &out) != nil || out.Token == "" {
		t.Fatalf("missing token: %s", rec.Body.String())
	}
	follow := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/panel/api/config", nil)
	req.Header.Set("Authorization", "Bearer "+out.Token)
	p.ServeHTTP(follow, req)
	if follow.Code == http.StatusUnauthorized {
		t.Fatal("session from key login rejected")
	}
}
