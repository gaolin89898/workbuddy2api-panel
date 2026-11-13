package server

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/linguo2625469/workbuddy2api-panel/internal/gateway"
	"github.com/linguo2625469/workbuddy2api-panel/internal/reqlog"
	"github.com/linguo2625469/workbuddy2api-panel/internal/usage"
)

func TestAntigravityHTTPStreamingUsageAndHiddenModels(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprint(stream), func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/v1internal:fetchAvailableModels" {
					w.Header().Set("Content-Type", "application/json")
					fmt.Fprint(w, `{"models":{"gemini-test":{"displayName":"Gemini Test"}}}`)
					return
				}
				w.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprint(w, "data: {\"response\":{\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"OK\"}]},\"finishReason\":\"STOP\"}],\"usageMetadata\":{\"promptTokenCount\":4,\"candidatesTokenCount\":2,\"thoughtsTokenCount\":1,\"totalTokenCount\":7}}}\n\n")
			}))
			defer upstream.Close()
			router := gateway.New(map[string]gateway.Source{"antigravity": providerSource{}})
			router.Bases["antigravity"] = upstream.URL
			router.HTTP = upstream.Client()
			logs := reqlog.New(reqlog.Config{})
			defer logs.Close()
			u := usage.New("")
			u.Start()
			defer u.Stop()
			p := testPoolWith()
			defer p.Close()
			cfg := Config{Providers: router, Pool: p, Usage: u, RequestLog: logs, APIKey: "test-key"}
			handler := NewHandler(cfg)
			call := func(method, path, body, key string) *httptest.ResponseRecorder {
				r := httptest.NewRequest(method, path, strings.NewReader(body))
				if key != "" {
					r.Header.Set("Authorization", "Bearer "+key)
				}
				w := httptest.NewRecorder()
				handler.ServeHTTP(w, r)
				return w
			}
			if w := call("GET", "/v1/models", "", "test-key"); w.Code != 200 || !strings.Contains(w.Body.String(), "antigravity/gemini-test") {
				t.Fatalf("model list not integrated: %d %s", w.Code, w.Body.String())
			}
			body := fmt.Sprintf(`{"model":"antigravity/gemini-test","stream":%v,"messages":[{"role":"user","content":"hello"}]}`, stream)
			if w := call("POST", "/v1/chat/completions", body, ""); w.Code != 401 {
				t.Fatal("unauthenticated call accepted")
			}
			w := call("POST", "/v1/chat/completions", body, "test-key")
			if w.Code != 200 || !strings.Contains(w.Body.String(), `"model":"antigravity/gemini-test"`) {
				t.Fatalf("route not integrated: %d %s", w.Code, w.Body.String())
			}
			if stream && !strings.HasSuffix(w.Body.String(), "data: [DONE]\n\n") {
				t.Fatal("missing OpenAI terminal marker")
			}
			event := logs.Snapshot().Recent[0]
			if !event.OK || event.TotalTokens != 7 || event.CompletionTokens != 3 || event.Account == "" {
				t.Fatalf("incorrect log stats: %+v", event)
			}
			stats := u.Snapshot(24, nil)
			if len(stats.ByRealm) != 1 || stats.ByRealm[0].Key != "antigravity" || stats.ByRealm[0].TotalTokens != 7 {
				t.Fatal("platform usage missing")
			}
			cfg.ModelsHidden = []string{"antigravity/gemini-test"}
			handler = NewHandler(cfg)
			if w := call("POST", "/v1/chat/completions", body, "test-key"); w.Code != 400 {
				t.Fatal("hidden Antigravity model forwarded")
			}
		})
	}
}
