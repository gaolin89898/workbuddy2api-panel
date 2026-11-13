package panel

import (
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/linguo2625469/workbuddy2api-panel/internal/antigravity"
)

func TestAntigravityRoutesRequireAuthenticationAndStrictForms(t *testing.T) {
	s, err := antigravity.Open(filepath.Join(t.TempDir(), "accounts.json"), nil)
	if err != nil {
		t.Fatal(err)
	}
	p := New(Config{AccountsOnly: true, APIKey: "test-key", Antigravity: s})
	defer p.Close()
	for _, test := range []struct {
		path, body, key string
		status          int
	}{
		{"/panel/api/antigravity/import", `{"refresh_token":"private-test"}`, "", 401},
		{"/panel/api/antigravity/accounts", `{} {}`, "test-key", 400},
		{"/panel/api/antigravity/accounts", `{"access_token":"x","base_url":"https://example.com"}`, "test-key", 400},
		{"/panel/api/antigravity/import", `{"type":"other","refresh_token":"x"}`, "test-key", 400},
		{"/panel/api/antigravity/accounts/missing/disable", ``, "test-key", 404},
		{"/panel/api/antigravity/accounts/missing/checkin", ``, "test-key", 404},
	} {
		r := httptest.NewRequest("POST", test.path, strings.NewReader(test.body))
		if test.key != "" {
			r.Header.Set("Authorization", "Bearer "+test.key)
		}
		w := httptest.NewRecorder()
		p.ServeHTTP(w, r)
		if w.Code != test.status {
			t.Fatalf("%s: got %d want %d", test.path, w.Code, test.status)
		}
		if strings.Contains(w.Body.String(), "private-test") {
			t.Fatal("token leaked in panel error")
		}
	}
}
