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
	call("accounts/"+uid+"/checkin", ``, "test-key", 404)
	call("accounts/"+uid+"/disable", ``, "test-key", 200)
	call("accounts/"+uid+"/revive", ``, "test-key", 200)
	call("balance_all", ``, "test-key", 200)
	call("accounts/"+uid+"/remove", ``, "test-key", 200)
	call("accounts/"+uid+"/disable", ``, "test-key", 404)
}
