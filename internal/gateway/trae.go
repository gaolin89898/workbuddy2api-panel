package gateway

import (
	"encoding/json"
	"strings"
)

// Trae raw-v2 has native text_delta events and legacy response snapshots.
// Keep incremental events incremental: repeated identical tokens are valid.
type traeState struct {
	id              string
	created         int64
	text, reasoning string
	usage           map[string]any
	tools           map[string]int
	toolSnapshots   map[string]string
	finish          string
}

func (t *traeState) chunk(delta map[string]any, finish any) map[string]any {
	return map[string]any{"id": t.id, "object": "chat.completion.chunk", "created": t.created, "choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": finish}}}
}
func payloadText(m map[string]any) string {
	for _, k := range []string{"delta", "text_delta", "text", "content", "response"} {
		if s, ok := m[k].(string); ok {
			return s
		}
		if nested := obj(m[k]); nested != nil {
			if s := payloadText(nested); s != "" {
				return s
			}
		}
	}
	if message := obj(m["message"]); message != nil {
		return payloadText(message)
	}
	return ""
}
func snapshotDelta(previous *string, next string) (string, error) {
	if next == "" {
		return "", nil
	}
	if !strings.HasPrefix(next, *previous) {
		return "", failure(502, "trae: upstream changed an emitted snapshot")
	}
	delta := strings.TrimPrefix(next, *previous)
	*previous = next
	return delta, nil
}
func traeUsage(m map[string]any) map[string]any {
	if m == nil {
		return nil
	}
	for i := 0; i < 4; i++ {
		found := false
		for _, k := range []string{"usage", "token_usage", "tokenUsage", "data"} {
			if nested := obj(m[k]); nested != nil {
				m = nested
				found = true
				break
			}
		}
		if !found {
			break
		}
	}
	out := map[string]any{}
	for key, aliases := range map[string][]string{"prompt_tokens": {"prompt_tokens", "input_tokens", "inputTokens"}, "completion_tokens": {"completion_tokens", "output_tokens", "outputTokens"}, "total_tokens": {"total_tokens", "totalTokens"}} {
		for _, a := range aliases {
			if v, ok := m[a]; ok {
				out[key] = v
				break
			}
		}
	}
	if len(out) == 0 {
		return nil
	}
	if out["total_tokens"] == nil {
		out["total_tokens"] = num(out["prompt_tokens"]) + num(out["completion_tokens"])
	}
	if v := m["prompt_tokens_details"]; v != nil {
		out["prompt_tokens_details"] = v
	}
	return out
}
func (t *traeState) event(event string, m map[string]any, emit Emit) error {
	if event == "token_usage" {
		t.usage = traeUsage(m)
		return nil
	}
	if usage := traeUsage(obj(m["usage"])); usage != nil {
		t.usage = usage
	}
	delta := map[string]any{}
	var err error
	text := ""
	if response, ok := m["response"].(string); ok {
		text, err = snapshotDelta(&t.text, response)
	} else {
		switch event {
		case "text_delta", "translated_text", "assistant_message":
			text = payloadText(m)
			t.text += text
		case "accumulated":
			text, err = snapshotDelta(&t.text, payloadText(m))
		}
	}
	if err != nil {
		return err
	}
	if text != "" {
		delta["content"] = text
	}
	reasoning := ""
	for _, k := range []string{"reasoning_content", "reasoningContent", "thought", "thinking"} {
		if s := str(m[k]); s != "" {
			reasoning = s
			break
		}
	}
	if event == "reasoning_delta" {
		if reasoning == "" {
			reasoning = payloadText(m)
		}
		t.reasoning += reasoning
	} else if reasoning != "" {
		reasoning, err = snapshotDelta(&t.reasoning, reasoning)
		if err != nil {
			return err
		}
	}
	if reasoning != "" {
		delta["reasoning_content"] = reasoning
	}
	calls := arr(m["tool_calls"])
	if message := obj(m["message"]); message != nil {
		calls = append(calls, arr(message["tool_calls"])...)
	}
	if tool := obj(m["tool_call_info"]); tool != nil {
		calls = append(calls, tool)
	}
	toolDeltas := []any{}
	for _, v := range calls {
		call := obj(v)
		fn := obj(call["function"])
		if fn == nil {
			fn = call
		}
		name := str(fn["name"])
		if name == "" {
			name = str(call["tool_call_name"])
		}
		toolID := str(call["id"])
		if toolID == "" {
			toolID = str(call["tool_call_id"])
		}
		if toolID == "" {
			return failure(502, "trae: tool call missing identity")
		}
		args := fn["arguments"]
		if args == nil {
			args = fn["parameters"]
		}
		argText := str(args)
		if args != nil && argText == "" {
			raw, err := json.Marshal(args)
			if err != nil {
				return failure(502, "trae: malformed tool arguments")
			}
			argText = string(raw)
		}
		index, exists := t.tools[toolID]
		if !exists {
			index = len(t.tools)
			t.tools[toolID] = index
		}
		if t.toolSnapshots == nil {
			t.toolSnapshots = map[string]string{}
		}
		previous := t.toolSnapshots[toolID]
		argDelta := argText
		if exists {
			if !strings.HasPrefix(argText, previous) {
				return failure(502, "trae: upstream changed tool arguments")
			}
			argDelta = strings.TrimPrefix(argText, previous)
		}
		t.toolSnapshots[toolID] = argText
		if exists && argDelta == "" {
			continue
		}
		part := map[string]any{"index": index, "function": map[string]any{"arguments": argDelta}}
		if !exists {
			if name == "" {
				return failure(502, "trae: tool call missing function name")
			}
			part["id"] = toolID
			part["type"] = "function"
			obj(part["function"])["name"] = name
		}
		toolDeltas = append(toolDeltas, part)
	}
	if len(toolDeltas) > 0 {
		delta["tool_calls"] = toolDeltas
	}
	if reason := str(m["finish_reason"]); reason != "" {
		t.finish = reason
	}
	if reason := str(m["stop_reason"]); reason != "" {
		t.finish = reason
	}
	if len(delta) > 0 {
		if err := emit(t.chunk(delta, nil)); err != nil {
			return err
		}
	}
	if event == "done" {
		reason := t.finish
		if reason == "" {
			reason = "stop"
		}
		switch reason {
		case "end_turn":
			reason = "stop"
		case "max_tokens":
			reason = "length"
		case "tool_use":
			reason = "tool_calls"
		}
		if len(t.tools) > 0 && reason == "stop" {
			reason = "tool_calls"
		}
		chunk := t.chunk(map[string]any{}, reason)
		if t.usage != nil {
			chunk["usage"] = t.usage
		}
		return emit(chunk)
	}
	return nil
}
