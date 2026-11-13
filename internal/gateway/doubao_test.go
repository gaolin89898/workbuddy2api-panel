package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

var doubaoBody = []byte(`{"model":"doubao/doubao","messages":[{"role":"system","content":"Be brief"},{"role":"user","content":[{"type":"text","text":"你好"}]}]}`)

func doubaoText(w http.ResponseWriter, text string, finish bool) {
	content, _ := json.Marshal(map[string]any{"text": text})
	payload, _ := json.Marshal(map[string]any{"message": map[string]any{"content": string(content), "content_type": 2001}, "is_finish": finish})
	sendEvent(w, "", map[string]any{"event_type": 2001, "event_data": string(payload)})
}

func TestDoubaoTextCatalogAndOpenAICompletion(t *testing.T) {
	r := testRouter(t, "doubao", func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path != "/samantha/chat/completion" || req.URL.Query().Get("aid") != "497858" || len(req.URL.Query().Get("device_id")) != 19 || req.Header.Get("Cookie") != "secret-a" || req.Header.Get("Authorization") != "" {
			t.Error("unexpected native request")
		}
		var body map[string]any
		if json.NewDecoder(req.Body).Decode(&body) != nil {
			t.Error("invalid body")
		}
		messages := arr(body["messages"])
		if len(messages) != 1 || !strings.Contains(str(obj(messages[0])["content"]), "Be brief") || !strings.Contains(str(obj(messages[0])["content"]), "你好") || !flag(obj(body["completion_option"])["need_create_conversation"]) {
			t.Error("history not translated")
		}
		doubaoText(w, "你", false)
		doubaoText(w, "好", true)
	}, Account{ID: "a"})
	models := r.Models(context.Background())
	if len(models) != 1 || str(models[0]["id"]) != "doubao/doubao" || flag(models[0]["supports_tool_call"]) || flag(models[0]["supports_images"]) {
		t.Fatalf("bad catalog: %v", models)
	}
	var completion Completion
	if err := r.Chat(context.Background(), doubaoBody, "", completion.Add, nil); err != nil {
		t.Fatal(err)
	}
	response := completion.Response()
	choice := obj(arr(response["choices"])[0])
	if str(response["model"]) != "doubao/doubao" || str(obj(choice["message"])["content"]) != "你好" || str(choice["finish_reason"]) != "stop" {
		t.Fatalf("bad completion: %v", response)
	}
}

func TestDoubaoStreamFailuresNeverReplayGeneration(t *testing.T) {
	for _, mode := range []string{"eof", "business-error", "malformed", "empty", "after-output", "non-sse"} {
		t.Run(mode, func(t *testing.T) {
			calls := 0
			r := testRouter(t, "doubao", func(w http.ResponseWriter, req *http.Request) {
				calls++
				switch mode {
				case "eof":
					doubaoText(w, "partial", false)
				case "business-error":
					sendEvent(w, "", map[string]any{"code": 13, "message": "secret-a"})
				case "malformed":
					w.Header().Set("Content-Type", "text/event-stream")
					fmt.Fprint(w, "data: invalid\n\n")
				case "empty":
					sendEvent(w, "", map[string]any{"event_type": 2003})
				case "after-output":
					doubaoText(w, "partial", false)
					sendEvent(w, "error", map[string]any{"message": "secret-a"})
				case "non-sse":
					sendJSON(w, map[string]any{"error": "secret-a"})
				}
			}, Account{ID: "a"}, Account{ID: "b"})
			finish := false
			err := r.Chat(context.Background(), doubaoBody, "", func(chunk map[string]any) error {
				for _, v := range arr(chunk["choices"]) {
					if str(obj(v)["finish_reason"]) == "stop" {
						finish = true
					}
				}
				return nil
			}, nil)
			if err == nil || Status(err) != 502 || strings.Contains(err.Error(), "secret-a") || finish || calls != 1 {
				t.Fatalf("failure mishandled: %v calls=%d finish=%v", err, calls, finish)
			}
		})
	}
}

func TestDoubaoRejectsUnsupportedInputsWithoutUpstreamRequest(t *testing.T) {
	calls := 0
	r := testRouter(t, "doubao", func(w http.ResponseWriter, req *http.Request) { calls++ }, Account{ID: "a"})
	for _, body := range []string{
		`{"model":"doubao/doubao","messages":[{"role":"user","content":"hi"}],"tools":[]}`,
		`{"model":"doubao/doubao","messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":"https://example.com/image.png"}}]}]}`,
		`{"model":"doubao/doubao","messages":[{"role":"tool","content":"result"}]}`,
	} {
		err := r.Chat(context.Background(), []byte(body), "", func(map[string]any) error { return nil }, nil)
		if err == nil || Status(err) != 400 {
			t.Fatalf("unsupported input not rejected: %v", err)
		}
	}
	if calls != 0 {
		t.Fatal("unsupported input sent upstream")
	}
}

func TestDoubaoTerminalEventAndClientCancellation(t *testing.T) {
	r := testRouter(t, "doubao", func(w http.ResponseWriter, req *http.Request) {
		doubaoText(w, "OK", false)
		sendEvent(w, "", map[string]any{"event_type": 2003})
	}, Account{ID: "a"})
	if err := r.Chat(context.Background(), doubaoBody, "", func(map[string]any) error { return nil }, nil); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := r.Chat(ctx, doubaoBody, "", func(map[string]any) error { return nil }, nil); err != context.Canceled {
		t.Fatalf("cancellation lost: %v", err)
	}
}
