package gateway

import (
	"context"
	"crypto/md5"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func testRouter(t *testing.T, p string, serve http.HandlerFunc, accounts ...Account) *Router {
	t.Helper()
	s := &staticSource{accounts: accounts, credentials: map[string]Credential{}}
	for _, a := range accounts {
		s.credentials[a.ID] = Credential{ID: a.ID, UID: a.ID, Token: "secret-" + a.ID, MachineID: "device-" + a.ID}
	}
	r := New(map[string]Source{p: s})
	up := httptest.NewServer(serve)
	t.Cleanup(up.Close)
	r.Bases[p] = up.URL
	r.HTTP = up.Client()
	return r
}
func sendJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(value)
}
func sendEvent(w http.ResponseWriter, event string, value any) {
	w.Header().Set("Content-Type", "text/event-stream")
	if event != "" {
		fmt.Fprintf(w, "event: %s\n", event)
	}
	raw, _ := json.Marshal(value)
	fmt.Fprintf(w, "data:%s\n\n", raw)
}
func loomyCatalog(w http.ResponseWriter) {
	sendJSON(w, map[string]any{"data": []any{map[string]any{"id": "chat", "type": "chat", "protocol": "openai_chat"}, map[string]any{"id": "image", "type": "image", "protocol": "openai_image"}}})
}
func completionEvent(w http.ResponseWriter, text, finish string) {
	sendEvent(w, "", map[string]any{"id": "c1", "created": 1, "choices": []any{map[string]any{"index": 0, "delta": map[string]any{"content": text}, "finish_reason": finish}}, "usage": map[string]any{"prompt_tokens": 4, "completion_tokens": 2, "total_tokens": 6}})
}

var chatBody = []byte(`{"model":"loomy/chat","messages":[{"role":"user","content":"hello"}]}`)

func TestLoomyRoutingCatalogAndSticky(t *testing.T) {
	var calls []string
	catalogCalls := 0
	r := testRouter(t, "loomy", func(w http.ResponseWriter, req *http.Request) {
		if req.Header.Get("token") != strings.TrimPrefix(req.Header.Get("Authorization"), "Bearer ") || len(req.Header.Get("traceparent")) != 55 {
			t.Error("missing native authentication/trace")
		}
		if req.URL.Path == "/models" {
			catalogCalls++
			loomyCatalog(w)
			return
		}
		var body map[string]any
		json.NewDecoder(req.Body).Decode(&body)
		if str(body["model"]) != "chat" || !flag(body["stream"]) {
			t.Error("model prefix or stream not rewritten")
		}
		calls = append(calls, req.Header.Get("token"))
		completionEvent(w, "OK", "stop")
	}, Account{ID: "disabled", Disabled: true}, Account{ID: "a"}, Account{ID: "b"})
	ms := r.Models(context.Background())
	if len(ms) != 1 || str(ms[0]["id"]) != "loomy/chat" {
		t.Fatalf("bad catalog: %v", ms)
	}
	raw, _ := json.Marshal(ms)
	if strings.Contains(string(raw), "secret") {
		t.Fatal("credential in catalog")
	}
	for _, session := range []string{"s1", "s1", "s2"} {
		var result Completion
		if err := r.Chat(context.Background(), chatBody, session, result.Add, nil); err != nil {
			t.Fatal(err)
		}
		if str(result.Response()["model"]) != "loomy/chat" {
			t.Fatal("response model prefix lost")
		}
	}
	if len(calls) != 3 || calls[0] != "secret-a" || calls[1] != calls[0] || calls[2] != "secret-b" {
		t.Fatalf("wrong routing: %v", calls)
	}
	if catalogCalls != 2 {
		t.Fatalf("catalog not cached per account: %d", catalogCalls)
	}
}

func TestRejectedRequestRotatesAndCoolsDown(t *testing.T) {
	counts := map[string]int{}
	attempts := 0
	r := testRouter(t, "loomy", func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path == "/models" {
			loomyCatalog(w)
			return
		}
		token := req.Header.Get("token")
		counts[token]++
		if token == "secret-a" {
			w.WriteHeader(429)
			fmt.Fprint(w, "upstream secret-a")
			return
		}
		completionEvent(w, "OK", "stop")
	}, Account{ID: "a"}, Account{ID: "b"})
	for i := 0; i < 2; i++ {
		if err := r.Chat(context.Background(), chatBody, "", func(map[string]any) error { return nil }, func(Attempt) { attempts++ }); err != nil {
			t.Fatal(err)
		}
	}
	if counts["secret-a"] != 1 || counts["secret-b"] != 2 || attempts != 3 {
		t.Fatalf("retry/cooldown mismatch: %v %d", counts, attempts)
	}
}

func TestNeverReplaysAmbiguousOrStartedStreams(t *testing.T) {
	cases := []struct {
		name     string
		response func(http.ResponseWriter, *http.Request)
	}{
		{"disconnect", func(w http.ResponseWriter, r *http.Request) { conn, _, _ := w.(http.Hijacker).Hijack(); conn.Close() }},
		{"truncated", func(w http.ResponseWriter, _ *http.Request) { completionEvent(w, "partial", "") }},
		{"late_error", func(w http.ResponseWriter, _ *http.Request) {
			completionEvent(w, "partial", "")
			sendEvent(w, "error", map[string]any{"error": "secret-a"})
		}},
		{"malformed", func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, "data:broken\n\n")
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			r := testRouter(t, "loomy", func(w http.ResponseWriter, req *http.Request) {
				if req.URL.Path == "/models" {
					loomyCatalog(w)
					return
				}
				calls++
				tc.response(w, req)
			}, Account{ID: "a"}, Account{ID: "b"})
			err := r.Chat(context.Background(), chatBody, "", func(map[string]any) error { return nil }, nil)
			if err == nil || calls != 1 {
				t.Fatalf("request replayed or succeeded: calls=%d err=%v", calls, err)
			}
			if strings.Contains(err.Error(), "secret") {
				t.Fatal("error leaks upstream response")
			}
		})
	}
}

func TestCapacityDisabledAndCancellation(t *testing.T) {
	entered := make(chan struct{})
	cancelled := make(chan struct{})
	var calls atomic.Int32
	r := testRouter(t, "loomy", func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path == "/models" {
			loomyCatalog(w)
			return
		}
		calls.Add(1)
		io.Copy(io.Discard, req.Body)
		close(entered)
		<-req.Context().Done()
		close(cancelled)
	}, Account{ID: "a"}, Account{ID: "disabled", Disabled: true})
	r.MaxInFlight = 1
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- r.Chat(ctx, chatBody, "", func(map[string]any) error { return nil }, nil) }()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("chat did not start")
	}
	if err := r.Chat(context.Background(), chatBody, "", func(map[string]any) error { return nil }, nil); Status(err) != 503 {
		t.Fatalf("capacity not enforced: %v", err)
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("lost cancellation: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("cancellation blocked")
	}
	select {
	case <-cancelled:
	case <-time.After(3 * time.Second):
		t.Fatal("upstream was not cancelled")
	}
	if calls.Load() != 1 {
		t.Fatal("disabled account or capacity used")
	}
}

func TestMiniMaxToolsHistoryReasoningAndUsage(t *testing.T) {
	r := testRouter(t, "minimax", func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path != "/messages" || req.Header.Get("anthropic-version") == "" {
			t.Error("wrong native route")
		}
		var input map[string]any
		json.NewDecoder(req.Body).Decode(&input)
		messages := arr(input["messages"])
		if len(messages) != 3 || str(obj(messages[2])["role"]) != "user" || len(arr(obj(messages[2])["content"])) != 2 {
			t.Errorf("tool results not grouped: %v", messages)
		}
		if len(arr(input["tools"])) != 1 || str(obj(input["tool_choice"])["type"]) != "tool" {
			t.Error("tools not converted")
		}
		events := []map[string]any{
			{"type": "message_start", "message": map[string]any{"id": "a1", "usage": map[string]any{"input_tokens": 0, "output_tokens": 0}}},
			{"type": "content_block_delta", "index": 0, "delta": map[string]any{"type": "thinking_delta", "thinking": "think"}},
			{"type": "content_block_start", "index": 1, "content_block": map[string]any{"type": "tool_use", "id": "call-1", "name": "echo", "input": map[string]any{}}},
			{"type": "content_block_delta", "index": 1, "delta": map[string]any{"type": "input_json_delta", "partial_json": "{\"text\":"}},
			{"type": "content_block_delta", "index": 1, "delta": map[string]any{"type": "input_json_delta", "partial_json": "\"OK\"}"}},
			{"type": "message_delta", "delta": map[string]any{"stop_reason": "tool_use"}, "usage": map[string]any{"input_tokens": 5, "cache_read_input_tokens": 100, "cache_creation_input_tokens": 2, "output_tokens": 8}},
			{"type": "message_stop"},
		}
		for _, e := range events {
			sendEvent(w, str(e["type"]), e)
		}
	}, Account{ID: "a"})
	body := []byte(`{"model":"minimax/MiniMax-M3","messages":[{"role":"system","content":"rules"},{"role":"user","content":"go"},{"role":"assistant","content":null,"tool_calls":[{"id":"old-1","function":{"name":"echo","arguments":"{}"}},{"id":"old-2","function":{"name":"echo","arguments":"{}"}}]},{"role":"tool","tool_call_id":"old-1","content":"ok"},{"role":"tool","tool_call_id":"old-2","content":"ok"}],"tools":[{"type":"function","function":{"name":"echo","parameters":{"type":"object"}}}],"tool_choice":{"type":"function","function":{"name":"echo"}}}`)
	var result Completion
	var attempt Attempt
	err := r.Chat(context.Background(), body, "", result.Add, func(a Attempt) { attempt = a })
	if err != nil {
		t.Fatal(err)
	}
	response := result.Response()
	choice := obj(arr(response["choices"])[0])
	message := obj(choice["message"])
	fn := obj(obj(arr(message["tool_calls"])[0])["function"])
	if str(fn["arguments"]) != `{"text":"OK"}` || str(choice["finish_reason"]) != "tool_calls" || str(message["reasoning_content"]) != "think" {
		t.Fatalf("tool/reasoning aggregation incorrect: %v", message)
	}
	if num(attempt.Usage["prompt_tokens"]) != 107 || num(attempt.Usage["total_tokens"]) != 115 {
		t.Fatalf("cache tokens missing/double counted: %v", attempt.Usage)
	}
}

func TestQoderEncodingSignatureAndEnvelope(t *testing.T) {
	// Independent decode of the three-segment permutation and alphabet.
	for _, plain := range []string{"{}", "hello", "中文", "longer request with trailing padding"} {
		enc := qoderEncode([]byte(plain))
		b := []byte(enc)
		std := "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"
		for i, ch := range b {
			if ch == '$' {
				b[i] = '='
			} else {
				b[i] = std[strings.IndexByte(qoderAlphabet, ch)]
			}
		}
		n := len(b)
		a := n / 3
		reordered := string(b[n-a:]) + string(b[a:n-a]) + string(b[:a])
		out, err := base64.StdEncoding.DecodeString(reordered)
		if err != nil || string(out) != plain {
			t.Fatal("bad COSY encoding")
		}
	}
	c := Credential{UID: "uid", Token: "private-token", RefreshToken: "private-refresh", MachineID: "machine"}
	body := qoderEncode([]byte("{}"))
	h, err := qoderHeaders(c, body, "/algo/api/v2/model/list?Encode=1", "")
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(strings.TrimPrefix(h.Get("Authorization"), "Bearer COSY."), ".")
	if len(parts) != 2 {
		t.Fatal("bad signature")
	}
	sum := md5.Sum([]byte(strings.Join([]string{parts[0], h.Get("Cosy-Key"), h.Get("Cosy-Date"), body, "/api/v2/model/list"}, "\n")))
	if hex.EncodeToString(sum[:]) != parts[1] {
		t.Fatal("signature did not cover encoded body/path")
	}
	payload, _ := base64.StdEncoding.DecodeString(parts[0])
	if strings.Contains(string(payload), "private") {
		t.Fatal("credentials not encrypted")
	}
	r := testRouter(t, "qoder", func(w http.ResponseWriter, req *http.Request) {
		if strings.Contains(req.URL.Path, "model/list") {
			sendJSON(w, map[string]any{"chat": []any{map[string]any{"key": "fast", "enable": true, "format": "openai"}}})
			return
		}
		if req.Header.Get("X-Model-Key") != "fast" {
			t.Error("model not routed")
		}
		inner := `{"id":"q1","choices":[{"index":0,"delta":{"content":"OK"},"finish_reason":"stop"}],"usage":{"total_tokens":9}}`
		sendEvent(w, "", map[string]any{"statusCodeValue": 200, "body": inner})
		sendEvent(w, "", map[string]any{"statusCodeValue": 200, "body": "[DONE]"})
		sendEvent(w, "finish", map[string]any{"totalDuration": 2})
	}, Account{ID: "a"})
	var result Completion
	err = r.Chat(context.Background(), []byte(`{"model":"qoder/fast","messages":[{"role":"user","content":"hi"}]}`), "", func(chunk map[string]any) error {
		for _, key := range []string{"body", "headers", "statusCodeValue"} {
			if chunk[key] != nil {
				t.Errorf("COSY envelope leaked: %s", key)
			}
		}
		return result.Add(chunk)
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if num(obj(result.Usage)["total_tokens"]) != 9 {
		t.Fatal("usage not unwrapped")
	}
}

func TestTraeNativeEventsAndCatalogFiltering(t *testing.T) {
	r := testRouter(t, "trae", func(w http.ResponseWriter, req *http.Request) {
		if strings.Contains(req.URL.Path, "get_detail_param") {
			rows := []any{}
			for _, name := range []string{"chat", "summary", "custom_model_placeholder", "chat__dev"} {
				usage := "chat_completion"
				if name == "summary" {
					usage = "summary"
				}
				rows = append(rows, map[string]any{"config_name": name, "config_switch": true, "usage": usage, "model_detail_list": []any{map[string]any{"model_name": "provider__dev"}}, "display_config": map[string]any{"display_name": name}})
			}
			sendJSON(w, map[string]any{"config_info_list": rows})
			return
		}
		sendEvent(w, "reasoning_delta", map[string]any{"text": "hmm"})
		sendEvent(w, "text_delta", map[string]any{"text": "ha"})
		sendEvent(w, "text_delta", map[string]any{"text": "ha"})
		sendEvent(w, "accumulated", map[string]any{"response": "haha!"})
		sendEvent(w, "token_usage", map[string]any{"usage": map[string]any{"input_tokens": 4, "output_tokens": 3}})
		sendEvent(w, "done", map[string]any{})
	}, Account{ID: "a"})
	ms := r.Models(context.Background())
	if len(ms) != 1 {
		t.Fatalf("auxiliary models leaked: %v", ms)
	}
	var result Completion
	err := r.Chat(context.Background(), []byte(`{"model":"trae/chat","messages":[{"role":"user","content":"hi"}]}`), "", result.Add, nil)
	if err != nil {
		t.Fatal(err)
	}
	message := obj(obj(arr(result.Response()["choices"])[0])["message"])
	if str(message["content"]) != "haha!" || str(message["reasoning_content"]) != "hmm" || num(obj(result.Usage)["total_tokens"]) != 7 {
		t.Fatalf("bad Trae normalization: %v", result.Response())
	}
}

func TestSSEFramesAndEmptyResponses(t *testing.T) {
	got := []string{}
	err := readSSE(strings.NewReader(": heartbeat\r\nevent: test\r\ndata:one\r\ndata: two\r\n\r\ndata:last"), func(event, data string) error { got = append(got, event+":"+data); return nil })
	if err != nil || strings.Join(got, "|") != "test:one\ntwo|:last" {
		t.Fatalf("invalid SSE parsing: %v %v", got, err)
	}
	r := testRouter(t, "loomy", func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path == "/models" {
			loomyCatalog(w)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data:[DONE]\n\n")
	}, Account{ID: "a"})
	if err := r.Chat(context.Background(), chatBody, "", func(map[string]any) error { return nil }, nil); err == nil {
		t.Fatal("empty stream treated as success")
	}
}

func TestTerminalEventClosesUpstreamWithoutWaitingForEOF(t *testing.T) {
	closed := make(chan struct{})
	r := testRouter(t, "loomy", func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path == "/models" {
			loomyCatalog(w)
			return
		}
		io.Copy(io.Discard, req.Body)
		completionEvent(w, "OK", "stop")
		fmt.Fprint(w, "data:[DONE]\n\n")
		w.(http.Flusher).Flush()
		<-req.Context().Done()
		close(closed)
	}, Account{ID: "a"})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := r.Chat(ctx, chatBody, "", func(map[string]any) error { return nil }, nil); err != nil {
		t.Fatal(err)
	}
	select {
	case <-closed:
	case <-time.After(3 * time.Second):
		t.Fatal("terminal stream left connection open")
	}
}
