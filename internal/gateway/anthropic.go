package gateway

import (
	"encoding/json"
	"strings"
)

func anthropicContent(content any) ([]any, error) {
	if content == nil {
		return nil, nil
	}
	if text, ok := content.(string); ok {
		if text == "" {
			return nil, nil
		}
		return []any{map[string]any{"type": "text", "text": text}}, nil
	}
	parts, ok := content.([]any)
	if !ok {
		return nil, failure(400, "minimax: unsupported message content")
	}
	out := []any{}
	for _, v := range parts {
		part := obj(v)
		switch str(part["type"]) {
		case "text":
			out = append(out, map[string]any{"type": "text", "text": part["text"]})
		case "image_url":
			u := str(obj(part["image_url"])["url"])
			if strings.HasPrefix(u, "data:") {
				meta, data, ok := strings.Cut(strings.TrimPrefix(u, "data:"), ",")
				if !ok || !strings.HasSuffix(meta, ";base64") {
					return nil, failure(400, "minimax: image must use a base64 data URL")
				}
				out = append(out, map[string]any{"type": "image", "source": map[string]any{"type": "base64", "media_type": strings.TrimSuffix(meta, ";base64"), "data": data}})
			} else if strings.HasPrefix(u, "https://") || strings.HasPrefix(u, "http://") {
				out = append(out, map[string]any{"type": "image", "source": map[string]any{"type": "url", "url": u}})
			} else {
				return nil, failure(400, "minimax: invalid image URL")
			}
		default:
			return nil, failure(400, "minimax: unsupported content block")
		}
	}
	return out, nil
}

func anthropicRequest(request map[string]any) (map[string]any, error) {
	for _, k := range []string{"response_format", "logprobs", "logit_bias", "functions", "function_call"} {
		if request[k] != nil {
			return nil, failure(400, "minimax: unsupported parameter "+k)
		}
	}
	req := map[string]any{"model": request["model"], "stream": true, "max_tokens": 4096}
	for _, k := range []string{"max_tokens", "temperature", "top_p", "top_k"} {
		if v, ok := request[k]; ok {
			req[k] = v
		}
	}
	if v, ok := request["max_completion_tokens"]; ok {
		req["max_tokens"] = v
	}
	if stop, ok := request["stop"]; ok {
		if s, ok := stop.(string); ok {
			req["stop_sequences"] = []string{s}
		} else {
			req["stop_sequences"] = stop
		}
	}
	if effort := str(request["reasoning_effort"]); effort != "" {
		if effort == "none" {
			req["thinking"] = map[string]any{"type": "disabled"}
		} else {
			req["thinking"] = map[string]any{"type": "adaptive"}
			req["output_config"] = map[string]any{"effort": effort}
		}
	}
	system := []any{}
	messages := []any{}
	appendMessage := func(role string, content []any) {
		if len(content) == 0 {
			return
		}
		if len(messages) > 0 && str(obj(messages[len(messages)-1])["role"]) == role {
			last := obj(messages[len(messages)-1])
			last["content"] = append(arr(last["content"]), content...)
		} else {
			messages = append(messages, map[string]any{"role": role, "content": content})
		}
	}
	for _, v := range arr(request["messages"]) {
		m := obj(v)
		role := str(m["role"])
		content, err := anthropicContent(m["content"])
		if err != nil {
			return nil, err
		}
		switch role {
		case "system", "developer":
			system = append(system, content...)
		case "user":
			appendMessage("user", content)
		case "assistant":
			for _, v := range arr(m["tool_calls"]) {
				tc := obj(v)
				fn := obj(tc["function"])
				var input map[string]any
				if json.Unmarshal([]byte(str(fn["arguments"])), &input) != nil || input == nil || str(tc["id"]) == "" || str(fn["name"]) == "" {
					return nil, failure(400, "minimax: invalid assistant tool call")
				}
				content = append(content, map[string]any{"type": "tool_use", "id": tc["id"], "name": fn["name"], "input": input})
			}
			appendMessage("assistant", content)
		case "tool":
			if str(m["tool_call_id"]) == "" {
				return nil, failure(400, "minimax: tool_call_id is required")
			}
			appendMessage("user", []any{map[string]any{"type": "tool_result", "tool_use_id": m["tool_call_id"], "content": content}})
		default:
			return nil, failure(400, "minimax: unsupported message role")
		}
	}
	if len(messages) == 0 {
		return nil, failure(400, "minimax: messages must include a user turn")
	}
	req["messages"] = messages
	if len(system) > 0 {
		req["system"] = system
	}
	tools := []any{}
	for _, v := range arr(request["tools"]) {
		t := obj(v)
		f := obj(t["function"])
		if str(t["type"]) != "function" || str(f["name"]) == "" {
			return nil, failure(400, "minimax: only function tools are supported")
		}
		schema := f["parameters"]
		if schema == nil {
			schema = map[string]any{"type": "object"}
		}
		tools = append(tools, map[string]any{"name": f["name"], "description": f["description"], "input_schema": schema})
	}
	if len(tools) > 0 {
		req["tools"] = tools
	}
	if choice := request["tool_choice"]; choice != nil {
		var tc map[string]any
		switch str(choice) {
		case "none":
			delete(req, "tools")
		case "auto":
			tc = map[string]any{"type": "auto"}
		case "required":
			tc = map[string]any{"type": "any"}
		default:
			fn := obj(obj(choice)["function"])
			if str(fn["name"]) == "" {
				return nil, failure(400, "minimax: invalid tool_choice")
			}
			tc = map[string]any{"type": "tool", "name": fn["name"]}
		}
		if tc != nil {
			if v, ok := request["parallel_tool_calls"]; ok {
				tc["disable_parallel_tool_use"] = !flag(v)
			}
			req["tool_choice"] = tc
		}
	}
	if _, ok := req["tool_choice"]; !ok && len(tools) > 0 && req["tools"] != nil {
		if v, exists := request["parallel_tool_calls"]; exists {
			req["tool_choice"] = map[string]any{"type": "auto", "disable_parallel_tool_use": !flag(v)}
		}
	}
	return req, nil
}

type anthropicState struct {
	id      string
	created int64
	tools   map[int]int
	usage   map[string]any
	finish  string
}

func (s *anthropicState) chunk(delta map[string]any, finish any) map[string]any {
	return map[string]any{"id": s.id, "object": "chat.completion.chunk", "created": s.created, "choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": finish}}}
}
func (s *anthropicState) updateUsage(u map[string]any) {
	for k, v := range u {
		s.usage[k] = v
	}
}
func (s *anthropicState) openAIUsage() map[string]any {
	u := s.usage
	in := num(u["input_tokens"])
	read := num(u["cache_read_input_tokens"])
	write := num(u["cache_creation_input_tokens"])
	output := num(u["output_tokens"])
	return map[string]any{"prompt_tokens": in + read + write, "completion_tokens": output, "total_tokens": in + read + write + output, "prompt_tokens_details": map[string]any{"cached_tokens": read}, "cache_creation_input_tokens": write}
}
func (s *anthropicState) event(event map[string]any, emit Emit) error {
	switch str(event["type"]) {
	case "message_start":
		message := obj(event["message"])
		if id := str(message["id"]); id != "" {
			s.id = id
		}
		s.updateUsage(obj(message["usage"]))
		return emit(s.chunk(map[string]any{"role": "assistant", "content": ""}, nil))
	case "content_block_start":
		block := obj(event["content_block"])
		switch str(block["type"]) {
		case "tool_use":
			index := int(num(event["index"]))
			toolIndex := len(s.tools)
			s.tools[index] = toolIndex
			args := ""
			if input := obj(block["input"]); len(input) > 0 {
				raw, _ := json.Marshal(input)
				args = string(raw)
			}
			return emit(s.chunk(map[string]any{"tool_calls": []any{map[string]any{"index": toolIndex, "id": block["id"], "type": "function", "function": map[string]any{"name": block["name"], "arguments": args}}}}, nil))
		case "text":
			if text := str(block["text"]); text != "" {
				return emit(s.chunk(map[string]any{"content": text}, nil))
			}
		case "thinking":
			if text := str(block["thinking"]); text != "" {
				return emit(s.chunk(map[string]any{"reasoning_content": text}, nil))
			}
		}
	case "content_block_delta":
		delta := obj(event["delta"])
		switch str(delta["type"]) {
		case "text_delta":
			return emit(s.chunk(map[string]any{"content": delta["text"]}, nil))
		case "thinking_delta":
			return emit(s.chunk(map[string]any{"reasoning_content": delta["thinking"]}, nil))
		case "input_json_delta":
			idx, ok := s.tools[int(num(event["index"]))]
			if !ok {
				return failure(502, "minimax: tool delta without a tool start")
			}
			return emit(s.chunk(map[string]any{"tool_calls": []any{map[string]any{"index": idx, "function": map[string]any{"arguments": delta["partial_json"]}}}}, nil))
		}
	case "message_delta":
		s.updateUsage(obj(event["usage"]))
		reason := str(obj(event["delta"])["stop_reason"])
		if reason != "" {
			switch reason {
			case "tool_use":
				s.finish = "tool_calls"
			case "max_tokens":
				s.finish = "length"
			default:
				s.finish = "stop"
			}
		}
	case "message_stop":
		if s.finish == "" {
			return failure(502, "minimax: missing stop reason")
		}
		chunk := s.chunk(map[string]any{}, s.finish)
		chunk["usage"] = s.openAIUsage()
		return emit(chunk)
	}
	return nil
}
