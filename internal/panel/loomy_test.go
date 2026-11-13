package panel

import (
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/linguo2625469/workbuddy2api-panel/internal/loomy"
)

// TestLoomyRoutesGuarding 镜像 qoder_test 的面板守卫矩阵：本机导入仅限本机
// （远端 / 跨站 → 403，无密钥 → 401），表单严格 JSON，未接线时 501。
func TestLoomyRoutesGuarding(t *testing.T) {
	s, err := loomy.Open(filepath.Join(t.TempDir(), "accounts.json"), nil)
	if err != nil {
		t.Fatal(err)
	}
	p := New(Config{AccountsOnly: true, APIKey: "test-key", Loomy: s})
	defer p.Close()
	bare := New(Config{AccountsOnly: true, APIKey: "test-key"})
	defer bare.Close()

	call := func(panel *Panel, method, path, body string, withKey bool, remote string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "http://127.0.0.1:7863"+path, strings.NewReader(body))
		r.RemoteAddr = remote
		if withKey {
			r.Header.Set("Authorization", "Bearer test-key")
		}
		w := httptest.NewRecorder()
		panel.ServeHTTP(w, r)
		return w
	}
	loopback := "127.0.0.1:50000"
	// 未接线：所有 loomy 端点 501。
	if w := call(bare, "POST", "/panel/api/loomy/accounts", `{}`, true, loopback); w.Code != 501 {
		t.Fatalf("unwired accounts: %d", w.Code)
	}
	// 粘贴凭证表单必须严格 JSON 且拒绝未知字段（不触网）。
	if w := call(p, "POST", "/panel/api/loomy/accounts", `{} {}`, true, loopback); w.Code != 400 {
		t.Fatalf("accepted trailing JSON: %d", w.Code)
	}
	if w := call(p, "POST", "/panel/api/loomy/accounts", `{"credential":"x","nickname":"n"}`, true, loopback); w.Code != 400 {
		t.Fatalf("missing account_uid must be rejected: %d", w.Code)
	}
	if w := call(p, "POST", "/panel/api/loomy/accounts", `{"credential":"x","account_uid":"u","bogus":1}`, true, loopback); w.Code != 400 {
		t.Fatalf("accepted unknown field: %d", w.Code)
	}
	// 本机导入仅限本机：远端与跨站一律 403。
	if w := call(p, "POST", "/panel/api/loomy/local_import", ``, true, "192.168.1.1:50000"); w.Code != 403 {
		t.Fatalf("remote local_import: %d", w.Code)
	}
	r := httptest.NewRequest("POST", "http://127.0.0.1:7863/panel/api/loomy/local_import", strings.NewReader(``))
	r.RemoteAddr = loopback
	r.Header.Set("Authorization", "Bearer test-key")
	r.Header.Set("Sec-Fetch-Site", "cross-site")
	w := httptest.NewRecorder()
	p.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatalf("cross-site local_import: %d", w.Code)
	}
	// 无密钥 401；未知动作 404。
	if w := call(p, "POST", "/panel/api/loomy/accounts", `{}`, false, loopback); w.Code != 401 {
		t.Fatalf("unauthorized: %d", w.Code)
	}
	if w := call(p, "POST", "/panel/api/loomy/accounts/l1/nope", ``, true, loopback); w.Code != 404 {
		t.Fatalf("unknown action: %d", w.Code)
	}
}
