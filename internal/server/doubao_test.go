package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/linguo2625469/workbuddy2api-panel/internal/gateway"
	"github.com/linguo2625469/workbuddy2api-panel/internal/reqlog"
)

func TestDoubaoHTTPStreamAndNonStream(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprint(stream), func(t *testing.T) {
			calls := 0
			up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				w.Header().Set("Content-Type", "text/event-stream")
				payload := `{"message":{"content":"{\"text\":\"OK\"}","content_type":2001}}`
				frame, _ := json.Marshal(map[string]any{"event_type": 2001, "event_data": payload})
				fmt.Fprintf(w, "data: %s\n\ndata: {\"event_type\":2003}\n\n", frame)
			}))
			defer up.Close()
			router := gateway.New(map[string]gateway.Source{"doubao": providerSource{}})
			router.Bases["doubao"], router.HTTP = up.URL, up.Client()
			p := testPoolWith()
			defer p.Close()
			logs := reqlog.New(reqlog.Config{})
			defer logs.Close()
			h := NewHandler(Config{Pool: p, Providers: router, APIKey: "test-key", RequestLog: logs})
			body := fmt.Sprintf(`{"model":"doubao/doubao","messages":[{"role":"user","content":"hello"}],"stream":%v}`, stream)
			request := func(key string) *httptest.ResponseRecorder {
				r := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(body))
				if key != "" {
					r.Header.Set("Authorization", "Bearer "+key)
				}
				w := httptest.NewRecorder()
				h.ServeHTTP(w, r)
				return w
			}
			if w := request(""); w.Code != 401 || calls != 0 {
				t.Fatal("unauthenticated request forwarded")
			}
			w := request("test-key")
			if w.Code != 200 || !strings.Contains(w.Body.String(), `"model":"doubao/doubao"`) || !strings.Contains(w.Body.String(), `"content":"OK"`) {
				t.Fatalf("bad completion: %d %s", w.Code, w.Body.String())
			}
			if stream && !strings.HasSuffix(w.Body.String(), "data: [DONE]\n\n") {
				t.Fatal("missing done event")
			}
			event := logs.Snapshot().Recent[0]
			if !event.OK || event.Model != "doubao/doubao" || event.Attempts != 1 || event.TotalTokens != 0 {
				t.Fatalf("wrong log or invented usage: %+v", event)
			}
		})
	}
}
