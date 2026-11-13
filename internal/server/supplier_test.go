package server

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/linguo2625469/workbuddy2api-panel/internal/gateway"
	"github.com/linguo2625469/workbuddy2api-panel/internal/livecfg"
)

func TestSupplierHotSelectionCatalogForwardingAndHealth(t *testing.T) {
	var calls []string
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.URL.Path)
		if r.URL.Path == "/models" {
			fmt.Fprint(w, `{"data":[{"id":"chat","type":"chat","protocol":"openai_chat"}]}`)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, sseOK)
	}))
	defer up.Close()
	router := gateway.New(map[string]gateway.Source{"loomy": providerSource{}})
	router.Bases["loomy"] = up.URL
	router.HTTP = up.Client()
	live := livecfg.New(livecfg.Snapshot{GatewaySupplier: "loomy"})
	h := NewHandler(Config{Pool: testPoolWith(), Providers: router, Live: live})
	request := func(method, path, body string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(method, path, strings.NewReader(body)))
		return w
	}
	list := request("GET", "/v1/models", "")
	if list.Code != 200 || !strings.Contains(list.Body.String(), `"id":"loomy/chat"`) || strings.Contains(list.Body.String(), `cn:`) {
		t.Fatalf("catalog status=%d body=%s", list.Code, list.Body.String())
	}
	for _, model := range []string{"chat", "loomy/chat"} {
		w := request("POST", "/v1/chat/completions", fmt.Sprintf(`{"model":%q,"messages":[{"role":"user","content":"hello"}]}`, model))
		if w.Code != 200 || !strings.Contains(w.Body.String(), `"model":"loomy/chat"`) {
			t.Fatalf("selected application not used: %d %s", w.Code, w.Body.String())
		}
	}
	before := len(calls)
	for _, model := range []string{"cn:chat", "qoder/chat"} {
		w := request("POST", "/v1/chat/completions", fmt.Sprintf(`{"model":%q}`, model))
		if w.Code != 400 || !strings.Contains(w.Body.String(), "supplier_model_mismatch") || len(calls) != before {
			t.Fatal("explicit foreign model escaped selected application")
		}
	}
	live.Store(livecfg.Snapshot{GatewaySupplier: "qoder"})
	if w := request("GET", "/healthz", ""); w.Code != 503 {
		t.Fatal("unavailable selected application reported healthy using a different application")
	}
	if w := request("GET", "/v1/models", ""); w.Code != 200 || strings.Contains(w.Body.String(), "loomy/chat") || len(calls) != before {
		t.Fatal("hot selection leaked previous catalog")
	}
	if w := request("POST", "/v1/search/messages", `{}`); w.Code != 400 || len(calls) != before {
		t.Fatal("selected application escaped through WorkBuddy search")
	}
	live.Store(livecfg.Snapshot{GatewaySupplier: "auto"})
	if w := request("POST", "/v1/chat/completions", `{"model":"loomy/chat","messages":[{"role":"user","content":"hi"}]}`); w.Code != 200 {
		t.Fatalf("automatic routing did not resume: %s", w.Body.String())
	}
}
