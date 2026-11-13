package gateway

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"
)

// doubaoChat maps text chat onto the doubao.com web protocol. Tool calls and
// uploads require separate native APIs and must never be silently discarded.
func (r *Router) doubaoChat(ctx context.Context, c Credential, m Model, request map[string]any, emit Emit) error {
	for _, key := range []string{"tools", "functions", "tool_choice", "function_call", "response_format"} {
		if v := request[key]; v != nil {
			return failure(400, "doubao: tools and structured output are not supported")
		}
	}
	texts := []string{}
	for _, v := range arr(request["messages"]) {
		message := obj(v)
		role := str(message["role"])
		if role != "system" && role != "developer" && role != "user" && role != "assistant" {
			return failure(400, "doubao: unsupported message role")
		}
		if len(arr(message["tool_calls"])) > 0 || message["function_call"] != nil {
			return failure(400, "doubao: tool messages are not supported")
		}
		text, ok := message["content"].(string)
		if !ok {
			parts, valid := message["content"].([]any)
			if !valid {
				return failure(400, "doubao: message content must be text")
			}
			for _, p := range parts {
				part := obj(p)
				value, valid := part["text"].(string)
				if str(part["type"]) != "text" || !valid {
					return failure(400, "doubao: only text content is supported")
				}
				text += value
			}
		}
		texts = append(texts, role+": "+text)
	}
	content, _ := json.Marshal(map[string]any{"text": strings.Join(texts, "\n\n")})
	stamp := strconv.FormatInt(time.Now().UnixNano(), 10)
	deviceHash := sha256.Sum256([]byte("doubao/" + c.ID))
	deviceID := strconv.FormatUint(7000000000000000000+binary.BigEndian.Uint64(deviceHash[:8])%1000000000000000000, 10)
	body := map[string]any{
		"messages":          []any{map[string]any{"content": string(content), "content_type": 2001, "attachments": []any{}, "references": []any{}}},
		"completion_option": map[string]any{"is_regen": false, "with_suggest": false, "need_create_conversation": true, "launch_stage": 1, "is_replace": false, "is_delete": false, "message_from": 0, "use_deep_think": false, "use_auto_cot": false, "event_id": "0"},
		"evaluate_option":   map[string]any{"web_ab_params": ""},
		"section_id":        "26" + stamp[len(stamp)-16:],
		"conversation_id":   "0", "local_conversation_id": "local_16" + stamp[len(stamp)-14:], "local_message_id": id(),
	}
	path := "/samantha/chat/completion?aid=497858&real_aid=497858&device_platform=web&language=zh&region=CN&sys_region=CN&samantha_web=1&use-olympus-account=1&version_code=20800&pc_version=2.44.0&pkg_type=release_version&device_id=" + deviceID + "&web_id=" + deviceID + "&tea_uuid=" + deviceID + "&web_tab_id=" + id()
	resp, err := r.request(ctx, "doubao", "POST", path, c, body, m.ID)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if !strings.Contains(strings.ToLower(resp.Header.Get("Content-Type")), "text/event-stream") {
		return failure(502, "doubao: expected an event stream; check login or verification in doubao.com")
	}
	completionID, created := "chatcmpl-"+id(), time.Now().Unix()
	terminal := errors.New("doubao stream completed")
	started, finished := false, false
	chunk := func(delta map[string]any, finish any) error {
		return emit(map[string]any{"id": completionID, "object": "chat.completion.chunk", "created": created, "model": m.ID, "choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": finish}}})
	}
	err = readSSE(resp.Body, func(event, data string) error {
		var frame map[string]any
		if json.Unmarshal([]byte(data), &frame) != nil {
			return failure(502, "doubao: malformed upstream event")
		}
		if num(frame["code"]) != 0 || event == "error" || frame["error"] != nil {
			return failure(502, "doubao: upstream rejected stream; check login or verification in doubao.com")
		}
		kind := int(num(frame["event_type"]))
		var payload map[string]any
		if kind == 2001 {
			payload = obj(frame["event_data"])
			if encoded, ok := frame["event_data"].(string); ok {
				if json.Unmarshal([]byte(encoded), &payload) != nil {
					return failure(502, "doubao: malformed event payload")
				}
			}
			if payload == nil {
				return failure(502, "doubao: missing event payload")
			}
			message := obj(payload["message"])
			if raw := str(message["content"]); raw != "" {
				var parsed any
				text := raw
				if json.Unmarshal([]byte(raw), &parsed) == nil {
					if s, ok := parsed.(string); ok {
						text = s
					} else {
						p := obj(parsed)
						text = str(p["text"])
						if text == "" {
							text = str(obj(p["delta"])["text"])
						}
						if text == "" {
							text = str(p["content"])
						}
					}
				}
				if text != "" {
					if !started {
						if err := chunk(map[string]any{"role": "assistant"}, nil); err != nil {
							return err
						}
						started = true
					}
					key := "content"
					if int(num(message["content_type"])) == 2008 {
						key = "reasoning_content"
					}
					if err := chunk(map[string]any{key: text}, nil); err != nil {
						return err
					}
				}
			}
		}
		if kind == 2003 || flag(payload["is_finish"]) {
			if !started {
				return failure(502, "doubao: upstream returned no text")
			}
			if err := chunk(map[string]any{}, "stop"); err != nil {
				return err
			}
			finished = true
			return terminal
		}
		return nil
	})
	if errors.Is(err, terminal) {
		return nil
	}
	if err != nil {
		return err
	}
	if !finished {
		return failure(502, "doubao: upstream stream interrupted")
	}
	return nil
}
