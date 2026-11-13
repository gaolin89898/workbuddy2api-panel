package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/linguo2625469/workbuddy2api-panel/internal/gateway"
	"github.com/linguo2625469/workbuddy2api-panel/internal/reqlog"
	"github.com/linguo2625469/workbuddy2api-panel/internal/usage"
)

type providerSource struct{}

func (providerSource) GatewayAccounts() []gateway.Account {
	return []gateway.Account{{ID: "loomy-test-account", Nickname: "test"}}
}
func (providerSource) GatewayCredential(context.Context, string) (gateway.Credential, error) {
	return gateway.Credential{ID: "loomy-test-account", Token: "private-provider-token", ProjectID: "private-project"}, nil
}

func TestProviderHTTPUsageLogHiddenAndHealth(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprint(stream), func(t *testing.T) {
			calls := 0
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.URL.Path == "/models" {
					w.Header().Set("Content-Type", "application/json")
					fmt.Fprint(w, `{"data":[{"id":"chat","type":"chat","protocol":"openai_chat"}]}`)
					return
				}
				w.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprint(w, sseOK)
			}))
			defer upstream.Close()
			router := gateway.New(map[string]gateway.Source{"loomy": providerSource{}})
			router.Bases["loomy"] = upstream.URL
			router.HTTP = upstream.Client()
			logs := reqlog.New(reqlog.Config{})
			defer logs.Close()
			u := usage.New("")
			u.Start()
			defer u.Stop()
			p := testPoolWith()
			defer p.Close()
			cfg := Config{Providers: router, Pool: p, Usage: u, RequestLog: logs, APIKey: "gateway-key"}
			h := NewHandler(cfg)
			request := func(body, key string) *httptest.ResponseRecorder {
				r := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(body))
				if key != "" {
					r.Header.Set("Authorization", "Bearer "+key)
				}
				w := httptest.NewRecorder()
				h.ServeHTTP(w, r)
				return w
			}
			body := fmt.Sprintf(`{"model":"loomy/chat","stream":%v,"messages":[{"role":"user","content":"private prompt"}]}`, stream)
			if w := request(body, ""); w.Code != 401 || calls != 0 {
				t.Fatal("provider bypassed gateway authentication")
			}
			w := request(body, "gateway-key")
			if w.Code != 200 || !strings.Contains(w.Body.String(), `"model":"loomy/chat"`) {
				t.Fatalf("bad provider completion: %d %s", w.Code, w.Body.String())
			}
			if stream && !strings.HasSuffix(w.Body.String(), "data: [DONE]\n\n") {
				t.Fatal("missing terminal SSE marker")
			}
			snapshot := logs.Snapshot()
			event := snapshot.Recent[0]
			if !event.OK || event.Model != "loomy/chat" || event.Attempts != 1 || event.TotalTokens != 2 || event.Account == "" {
				t.Fatalf("provider log incomplete: %+v", event)
			}
			if snapshot.Succeeded != 1 {
				t.Fatal("success counter incorrect")
			}
			stats := u.Snapshot(24, nil)
			if len(stats.ByRealm) != 1 || stats.ByRealm[0].Key != "loomy" || stats.ByRealm[0].TotalTokens != 2 {
				t.Fatalf("provider usage missing: %+v", stats.ByRealm)
			}
			encoded, _ := json.Marshal(snapshot)
			if strings.Contains(string(encoded), "private") {
				t.Fatal("prompt/token leaked to log")
			}
			before := calls
			cfg.ModelsHidden = []string{"loomy/chat"}
			h = NewHandler(cfg)
			if w := request(body, "gateway-key"); w.Code != 400 || calls != before {
				t.Fatal("hidden provider model still forwarded")
			}
			health := httptest.NewRecorder()
			h.ServeHTTP(health, httptest.NewRequest("GET", "/healthz", nil))
			if health.Code != 200 || !strings.Contains(health.Body.String(), "provider_totals") {
				t.Fatal("provider-only gateway reported unhealthy")
			}
		})
	}
}

func TestProviderStreamFailureIsNotLoggedAsSuccess(t *testing.T) {
	for _, started := range []bool{false, true} {
		t.Run(fmt.Sprint(started), func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/models" {
					fmt.Fprint(w, `{"data":[{"id":"chat","type":"chat","protocol":"openai_chat"}]}`)
					return
				}
				w.Header().Set("Content-Type", "text/event-stream")
				if started {
					fmt.Fprint(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"partial\"}}]}\n\n")
				}
				fmt.Fprint(w, "event: error\ndata: {\"error\":\"private upstream error\"}\n\n")
			}))
			defer upstream.Close()
			router := gateway.New(map[string]gateway.Source{"loomy": providerSource{}})
			router.Bases["loomy"] = upstream.URL
			router.HTTP = upstream.Client()
			logs := reqlog.New(reqlog.Config{})
			defer logs.Close()
			h := NewHandler(Config{Providers: router, RequestLog: logs})
			w := httptest.NewRecorder()
			h.ServeHTTP(w, httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"loomy/chat","stream":true,"messages":[{"role":"user","content":"hi"}]}`)))
			if started {
				if w.Code != 200 || !strings.Contains(w.Body.String(), "event: error") {
					t.Fatal("missing in-stream error")
				}
			} else if w.Code != 502 {
				t.Fatalf("rejection reported as HTTP success: %d", w.Code)
			}
			if strings.Contains(w.Body.String(), "[DONE]") || strings.Contains(w.Body.String(), "private") {
				t.Fatal("error reported as successful or leaked upstream text")
			}
			snapshot := logs.Snapshot()
			if snapshot.Failed != 1 || snapshot.Recent[0].OK {
				t.Fatal("stream failure counted as success")
			}
		})
	}
}
