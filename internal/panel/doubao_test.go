package panel

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/linguo2625469/workbuddy2api-panel/internal/doubao"
)

func TestDoubaoRoutesAuthenticationFormsAndLifecycle(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/alice/ug/campaign/landing/checkin" {
			w.Write([]byte(`{"code":0,"msg":"","data":{"checked_in":true,"checkin_date":"2026-11-14","continuous_days":2,"idempotent":true,"reward_amount":200,"reward_type":"quota_grant"}}`))
			return
		}
		if r.URL.Path == "/alice/ug/campaign/v3/detail/" {
			w.Write([]byte(`{"code":0,"data":{"progress":{"total_amount":4100},"tasks":[{"task_kind":"daily_signin","task_status":3,"daily_checkin":{"checked_in_today":true,"last_checkin_date":"2026-11-14","continuous_days":2,"total_days":15,"reward":{"reward_amount":200,"reward_type":"quota_grant"}}}]}}`))
			return
		}
		w.Write([]byte(`{"message":"success","data":{"user_id":"123","screen_name":"豆包测试"}}`))
	}))
	defer upstream.Close()
	s, err := doubao.Open(filepath.Join(t.TempDir(), "accounts.json"), &doubao.Client{HTTP: upstream.Client(), Base: upstream.URL})
	if err != nil {
		t.Fatal(err)
	}
	p := New(Config{AccountsOnly: true, APIKey: "test-key", Doubao: s})
	defer p.Close()
	call := func(path, body, key string, expected int) string {
		t.Helper()
		r := httptest.NewRequest("POST", "/panel/api/doubao/"+path, strings.NewReader(body))
		if key != "" {
			r.Header.Set("Authorization", "Bearer "+key)
		}
		w := httptest.NewRecorder()
		p.ServeHTTP(w, r)
		if w.Code != expected {
			t.Fatalf("%s: got %d want %d: %s", path, w.Code, expected, w.Body.String())
		}
		if strings.Contains(w.Body.String(), "private-session") {
			t.Fatal("credential leaked")
		}
		return w.Body.String()
	}
	call("accounts", `{"cookie":"private-session"}`, "", 401)
	call("accounts", `{} {}`, "test-key", 400)
	call("accounts", `{"cookie":"private-session","base_url":"https://example.com"}`, "test-key", 400)
	call("accounts", `{"cookie":"private-session"}`, "test-key", 200)
	uid := s.List()[0].UID
	call("accounts/"+uid+"/nope", ``, "test-key", 404)
	call("accounts/"+uid+"/checkin", ``, "test-key", 200)
	if s.List()[0].LastCheckinDate != "2026-11-14" {
		t.Fatalf("checkin date not recorded: %+v", s.List()[0])
	}
	call("checkin_all", ``, "test-key", 200)
	call("accounts/"+uid+"/disable", ``, "test-key", 200)
	call("accounts/"+uid+"/revive", ``, "test-key", 200)
	call("balance_all", ``, "test-key", 200)
	call("accounts/"+uid+"/remove", ``, "test-key", 200)
	call("accounts/"+uid+"/disable", ``, "test-key", 404)
}
