package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

func antiCatalog(w http.ResponseWriter) {
	sendJSON(w, map[string]any{"models": map[string]any{"gemini-test": map[string]any{"displayName": "Gemini Test", "maxInputTokens": 1000, "supportsThinking": true}, "hidden": map[string]any{"disabled": true}, "imagen-test": map[string]any{}}})
}
func antiEvent(w http.ResponseWriter, parts []any, finish string, usage map[string]any) {
	root := map[string]any{"candidates": []any{map[string]any{"index": 0, "content": map[string]any{"role": "model", "parts": parts}, "finishReason": finish}}}
	if usage != nil {
		root["usageMetadata"] = usage
	}
	sendEvent(w, "", map[string]any{"response": root})
}
func antiProject(r *Router) {
	for id, c := range r.Sources["antigravity"].(*staticSource).credentials {
		c.ProjectID = "project-test"
		r.Sources["antigravity"].(*staticSource).credentials[id] = c
	}
}

func TestAntigravityNativeCatalogToolsAndHistory(t *testing.T) {
	requests := 0
	r := testRouter(t, "antigravity", func(w http.ResponseWriter, req *http.Request) {
		if req.Header.Get("Authorization") != "Bearer secret-a" || !strings.Contains(req.Header.Get("User-Agent"), "antigravity/") {
			t.Error("native authentication missing")
		}
		var body map[string]any
		json.NewDecoder(req.Body).Decode(&body)
		if str(body["project"]) != "project-test" {
			t.Error("project was not isolated to account")
		}
		if req.URL.Path == "/v1internal:fetchAvailableModels" {
			antiCatalog(w)
			return
		}
		requests++
		if req.URL.Path != "/v1internal:streamGenerateContent" || req.URL.Query().Get("alt") != "sse" || str(body["model"]) != "gemini-test" || !strings.HasPrefix(str(body["requestId"]), "agent/") {
			t.Error("wrong native envelope")
		}
		inner := obj(body["request"])
		contents := arr(inner["contents"])
		if !strings.HasPrefix(str(inner["sessionId"]), "-") {
			t.Error("missing native session ID")
		}
		if requests == 1 {
			if len(arr(obj(inner["systemInstruction"])["parts"])) != 1 || len(contents) != 1 || len(arr(obj(contents[0])["parts"])) != 2 {
				t.Error("system/image conversion failed")
			}
			decl := obj(arr(obj(arr(inner["tools"])[0])["functionDeclarations"])[0])
			if str(decl["name"]) != "weather" {
				t.Error("function declaration lost")
			}
			antiEvent(w, []any{map[string]any{"text": "thinking", "thought": true}}, "", nil)
			antiEvent(w, []any{map[string]any{"functionCall": map[string]any{"name": "weather", "args": map[string]any{"city": "北京"}}, "thoughtSignature": "signed-history"}}, "STOP", map[string]any{"promptTokenCount": 10, "candidatesTokenCount": 2, "thoughtsTokenCount": 3, "totalTokenCount": 15, "cachedContentTokenCount": 4})
		} else {
			if len(contents) != 3 {
				t.Fatalf("tool history has wrong roles: %v", contents)
			}
			part := obj(arr(obj(contents[1])["parts"])[0])
			if str(part["thoughtSignature"]) != "signed-history" {
				t.Error("cached signature not restored")
			}
			response := obj(obj(arr(obj(contents[2])["parts"])[0])["functionResponse"])
			if str(response["name"]) != "weather" || str(obj(response["response"])["weather"]) != "sunny" {
				t.Error("tool result not matched by call ID")
			}
			antiEvent(w, []any{map[string]any{"text": "晴天"}}, "STOP", nil)
		}
	}, Account{ID: "a"})
	antiProject(r)
	models := r.Models(context.Background())
	if len(models) != 1 || str(models[0]["id"]) != "antigravity/gemini-test" {
		t.Fatalf("bad catalog: %v", models)
	}
	first := []byte(`{"model":"antigravity/gemini-test","messages":[{"role":"system","content":"help"},{"role":"user","content":[{"type":"text","text":"天气？"},{"type":"image_url","image_url":{"url":"data:image/png;base64,aGVsbG8="}}]}],"tools":[{"type":"function","function":{"name":"weather","parameters":{"type":"object","properties":{"city":{"type":"string"}},"required":["city"],"additionalProperties":false}}}],"reasoning_effort":"low"}`)
	var result Completion
	if err := r.Chat(context.Background(), first, "conversation", result.Add, nil); err != nil {
		t.Fatal(err)
	}
	message := obj(obj(arr(result.Response()["choices"])[0])["message"])
	if str(message["reasoning_content"]) != "thinking" || str(obj(arr(result.Response()["choices"])[0])["finish_reason"]) != "tool_calls" {
		t.Fatal("reasoning or finish reason lost")
	}
	tool := obj(arr(message["tool_calls"])[0])
	if str(obj(obj(tool["extra_content"])["google"])["thought_signature"]) != "signed-history" {
		t.Fatal("nonstream signature was lost")
	}
	u := obj(result.Usage)
	if num(u["completion_tokens"]) != 5 || num(u["total_tokens"]) != 15 || num(obj(u["prompt_tokens_details"])["cached_tokens"]) != 4 {
		t.Fatalf("usage incorrect: %v", u)
	}
	delete(tool, "extra_content") // Clients may omit extension fields on roundtrip.
	second, _ := json.Marshal(map[string]any{"model": "antigravity/gemini-test", "messages": []any{map[string]any{"role": "user", "content": "天气？"}, message, map[string]any{"role": "tool", "tool_call_id": tool["id"], "content": `{"weather":"sunny"}`}}})
	var followup Completion
	if err := r.Chat(context.Background(), second, "conversation", followup.Add, nil); err != nil {
		t.Fatal(err)
	}
	if str(obj(obj(arr(followup.Response()["choices"])[0])["message"])["content"]) != "晴天" {
		t.Fatal("follow-up text missing")
	}
}

func TestAntigravityStreamFailuresDoNotReplayOutput(t *testing.T) {
	for _, partial := range []bool{false, true} {
		t.Run(fmt.Sprint(partial), func(t *testing.T) {
			calls := 0
			r := testRouter(t, "antigravity", func(w http.ResponseWriter, req *http.Request) {
				if req.URL.Path == "/v1internal:fetchAvailableModels" {
					antiCatalog(w)
					return
				}
				calls++
				if partial {
					antiEvent(w, []any{map[string]any{"text": "partial"}}, "", nil)
				}
				sendEvent(w, "", map[string]any{"error": map[string]any{"code": 429, "message": "private upstream token"}})
			}, Account{ID: "a"}, Account{ID: "b"})
			antiProject(r)
			err := r.Chat(context.Background(), []byte(`{"model":"antigravity/gemini-test","messages":[{"role":"user","content":"hello"}]}`), "", func(map[string]any) error { return nil }, nil)
			if Status(err) != 429 || strings.Contains(err.Error(), "private") {
				t.Fatalf("failure lost or leaked: %v", err)
			}
			want := 2
			if partial {
				want = 1
			}
			if calls != want {
				t.Fatalf("unsafe retry: %d calls, want %d", calls, want)
			}
		})
	}
}

func TestAntigravityTruncatedStreamIsFailure(t *testing.T) {
	r := testRouter(t, "antigravity", func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path == "/v1internal:fetchAvailableModels" {
			antiCatalog(w)
			return
		}
		antiEvent(w, []any{map[string]any{"text": "incomplete"}}, "", nil)
	}, Account{ID: "a"})
	antiProject(r)
	err := r.Chat(context.Background(), []byte(`{"model":"antigravity/gemini-test","messages":[{"role":"user","content":"hi"}]}`), "", func(map[string]any) error { return nil }, nil)
	if err == nil || Status(err) != 502 {
		t.Fatalf("truncation accepted as success: %v", err)
	}
}
