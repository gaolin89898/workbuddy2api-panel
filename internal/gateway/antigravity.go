package gateway

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

func antigravityModels(root map[string]any) []Model {
	out := []Model{}
	keys := []string{}
	for key := range obj(root["models"]) {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		m := obj(obj(root["models"])[key])
		if key == "" || flag(m["disabled"]) || strings.HasSuffix(key, "-disabled") {
			continue
		}
		// This endpoint may also advertise image generation. Only expose models
		// whose output this Chat Completions adapter can represent.
		lower := strings.ToLower(key)
		if strings.Contains(lower, "imagen") || strings.Contains(lower, "image") || strings.Contains(lower, "veo") {
			continue
		}
		f := map[string]any{"name": m["displayName"], "catalog_source": "upstream"}
		for src, dst := range map[string]string{"maxInputTokens": "context_length", "maxOutputTokens": "max_output_tokens", "supportsImages": "supports_images", "supportsThinking": "supports_reasoning", "supportsTools": "supports_tool_call"} {
			if v, ok := m[src]; ok {
				f[dst] = v
			}
		}
		out = append(out, Model{ID: key, Fields: f})
	}
	return out
}

func (r *Router) antigravitySignature(account, model, call, value string) string {
	key := account + "/" + model + "/" + call
	r.mu.Lock()
	defer r.mu.Unlock()
	if value != "" {
		if len(r.signatures) >= 2048 {
			for k := range r.signatures {
				delete(r.signatures, k)
				break
			}
		}
		r.signatures[key] = value
	}
	return r.signatures[key]
}

func antigravityParts(content any) ([]any, error) {
	if content == nil {
		return nil, nil
	}
	if text, ok := content.(string); ok {
		if text == "" {
			return nil, nil
		}
		return []any{map[string]any{"text": text}}, nil
	}
	blocks, ok := content.([]any)
	if !ok {
		return nil, failure(400, "antigravity: invalid message content")
	}
	parts := []any{}
	for _, block := range blocks {
		b := obj(block)
		switch str(b["type"]) {
		case "text":
			parts = append(parts, map[string]any{"text": str(b["text"])})
		case "image_url":
			value := str(obj(b["image_url"])["url"])
			meta, data, ok := strings.Cut(value, ",")
			if !ok || !strings.HasPrefix(meta, "data:image/") || !strings.HasSuffix(meta, ";base64") {
				return nil, failure(400, "antigravity: images require a base64 data URL")
			}
			if len(data) > 16<<20 {
				return nil, failure(400, "antigravity: image exceeds supported size")
			}
			if _, err := base64.StdEncoding.DecodeString(data); err != nil {
				return nil, failure(400, "antigravity: invalid image data")
			}
			parts = append(parts, map[string]any{"inlineData": map[string]any{"mimeType": strings.TrimSuffix(strings.TrimPrefix(meta, "data:"), ";base64"), "data": data}})
		default:
			return nil, failure(400, "antigravity: unsupported message content type")
		}
	}
	return parts, nil
}

// Gemini's function parameter schema is a subset of JSON Schema. Drop only
// descriptive keywords; reject constructs that would change validation meaning.
func antigravitySchema(v any) (any, error) {
	switch value := v.(type) {
	case map[string]any:
		out := map[string]any{}
		for k, item := range value {
			switch k {
			case "$schema", "$id", "title", "examples", "default", "additionalProperties":
				continue
			case "$ref", "$defs", "definitions", "oneOf", "anyOf", "allOf":
				return nil, failure(400, "antigravity: unsupported tool schema keyword "+k)
			}
			if k == "properties" {
				props := map[string]any{}
				for name, schema := range obj(item) {
					next, err := antigravitySchema(schema)
					if err != nil {
						return nil, err
					}
					props[name] = next
				}
				out[k] = props
				continue
			}
			next, err := antigravitySchema(item)
			if err != nil {
				return nil, err
			}
			out[k] = next
		}
		return out, nil
	case []any:
		out := []any{}
		for _, item := range value {
			next, err := antigravitySchema(item)
			if err != nil {
				return nil, err
			}
			out = append(out, next)
		}
		return out, nil
	default:
		return v, nil
	}
}

func (r *Router) antigravityBody(c Credential, m Model, request map[string]any, session string) (map[string]any, error) {
	if c.ProjectID == "" {
		return nil, failure(400, "antigravity: account has no project ID; update its OAuth credentials")
	}
	for _, key := range []string{"logprobs", "top_logprobs", "audio", "modalities", "response_format", "frequency_penalty", "presence_penalty", "logit_bias"} {
		if v, ok := request[key]; ok && v != nil {
			return nil, failure(400, "antigravity: unsupported parameter "+key)
		}
	}
	contents := []any{}
	system := []any{}
	names := map[string]string{}
	appendContent := func(role string, parts []any) {
		if len(parts) == 0 {
			return
		}
		if len(contents) > 0 {
			last := obj(contents[len(contents)-1])
			if str(last["role"]) == role {
				last["parts"] = append(arr(last["parts"]), parts...)
				return
			}
		}
		contents = append(contents, map[string]any{"role": role, "parts": parts})
	}
	for _, value := range arr(request["messages"]) {
		message := obj(value)
		role := str(message["role"])
		if role == "tool" {
			call := str(message["tool_call_id"])
			name := names[call]
			if name == "" {
				return nil, failure(400, "antigravity: tool result has no preceding function call")
			}
			var result any
			content := message["content"]
			text, ok := content.(string)
			if ok && json.Unmarshal([]byte(text), &result) == nil {
			} else {
				result = content
			}
			response := obj(result)
			if response == nil {
				response = map[string]any{"result": result}
			}
			appendContent("user", []any{map[string]any{"functionResponse": map[string]any{"name": name, "response": response}}})
			continue
		}
		parts, err := antigravityParts(message["content"])
		if err != nil {
			return nil, err
		}
		if role == "system" || role == "developer" {
			for _, p := range parts {
				if _, ok := obj(p)["text"]; !ok {
					return nil, failure(400, "antigravity: system instructions must be text")
				}
			}
			system = append(system, parts...)
			continue
		}
		if role != "user" && role != "assistant" {
			return nil, failure(400, "antigravity: unsupported message role")
		}
		if role == "assistant" {
			role = "model"
			for _, v := range arr(message["tool_calls"]) {
				call := obj(v)
				fn := obj(call["function"])
				name := str(fn["name"])
				callID := str(call["id"])
				if str(call["type"]) != "function" || name == "" || callID == "" {
					return nil, failure(400, "antigravity: invalid function call")
				}
				args := map[string]any{}
				if json.Unmarshal([]byte(str(fn["arguments"])), &args) != nil || args == nil {
					return nil, failure(400, "antigravity: function arguments must be a JSON object")
				}
				names[callID] = name
				part := map[string]any{"functionCall": map[string]any{"name": name, "args": args}}
				signature := str(obj(obj(call["extra_content"])["google"])["thought_signature"])
				if signature == "" {
					signature = r.antigravitySignature(c.ID, m.ID, callID, "")
				}
				if signature != "" {
					part["thoughtSignature"] = signature
				}
				parts = append(parts, part)
			}
		}
		appendContent(role, parts)
	}
	if len(contents) == 0 {
		return nil, failure(400, "antigravity: no conversation content")
	}
	inner := map[string]any{"contents": contents}
	if len(system) > 0 {
		inner["systemInstruction"] = map[string]any{"parts": system}
	}
	gen := map[string]any{}
	for src, dst := range map[string]string{"temperature": "temperature", "top_p": "topP", "max_tokens": "maxOutputTokens", "max_completion_tokens": "maxOutputTokens", "seed": "seed"} {
		if value, ok := request[src]; ok {
			gen[dst] = value
		}
	}
	// max_completion_tokens has the same precedence as in the other adapters.
	if value, ok := request["max_completion_tokens"]; ok {
		gen["maxOutputTokens"] = value
	}
	if value, ok := request["stop"]; ok {
		if text, ok := value.(string); ok {
			gen["stopSequences"] = []string{text}
		} else {
			gen["stopSequences"] = value
		}
	}
	if effort := str(request["reasoning_effort"]); effort != "" {
		budgets := map[string]int{"none": 0, "minimal": 128, "low": 1024, "medium": 8192, "high": 16384}
		budget, ok := budgets[effort]
		if !ok {
			return nil, failure(400, "antigravity: unsupported reasoning_effort")
		}
		thinking := map[string]any{"includeThoughts": budget > 0}
		if strings.HasPrefix(m.ID, "gemini-3") || strings.HasPrefix(m.ID, "gemini-4") {
			level := effort
			if level == "none" || level == "minimal" {
				level = "low"
			}
			thinking["thinkingLevel"] = strings.ToUpper(level)
		} else {
			thinking["thinkingBudget"] = budget
		}
		gen["thinkingConfig"] = thinking
	}
	if len(gen) > 0 {
		inner["generationConfig"] = gen
	}
	declarations := []any{}
	for _, value := range arr(request["tools"]) {
		tool := obj(value)
		fn := obj(tool["function"])
		if str(tool["type"]) != "function" || str(fn["name"]) == "" {
			return nil, failure(400, "antigravity: only named function tools are supported")
		}
		decl := map[string]any{"name": fn["name"]}
		if value, ok := fn["description"]; ok {
			decl["description"] = value
		}
		if parameters := fn["parameters"]; parameters != nil {
			schema, err := antigravitySchema(parameters)
			if err != nil {
				return nil, err
			}
			decl["parameters"] = schema
		}
		declarations = append(declarations, decl)
	}
	if len(declarations) > 0 {
		inner["tools"] = []any{map[string]any{"functionDeclarations": declarations}}
	}
	if choice, ok := request["tool_choice"]; ok {
		config := map[string]any{}
		if name, ok := choice.(string); ok {
			mode := map[string]string{"auto": "AUTO", "none": "NONE", "required": "ANY"}[name]
			if mode == "" {
				return nil, failure(400, "antigravity: invalid tool_choice")
			}
			config["mode"] = mode
		} else {
			tool := obj(choice)
			name := str(obj(tool["function"])["name"])
			if str(tool["type"]) != "function" || name == "" {
				return nil, failure(400, "antigravity: invalid tool_choice")
			}
			config["mode"] = "ANY"
			config["allowedFunctionNames"] = []string{name}
		}
		inner["toolConfig"] = map[string]any{"functionCallingConfig": config}
	}
	if session == "" {
		session = id()
	}
	sum := sha256.Sum256([]byte(c.ID + "/" + session))
	inner["sessionId"] = "-" + strconv.FormatUint(binary.BigEndian.Uint64(sum[:8])&0x7fffffffffffffff, 10)
	return map[string]any{"project": c.ProjectID, "model": m.ID, "requestId": "agent/" + id(), "userAgent": "antigravity", "requestType": "agent", "request": inner}, nil
}

func (r *Router) antigravityChat(ctx context.Context, c Credential, m Model, request map[string]any, session string, emit Emit) error {
	body, err := r.antigravityBody(c, m, request, session)
	if err != nil {
		return err
	}
	resp, err := r.request(ctx, "antigravity", "POST", "/v1internal:streamGenerateContent?alt=sse", c, body, m.ID)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if !strings.Contains(strings.ToLower(resp.Header.Get("Content-Type")), "text/event-stream") {
		return failure(502, "antigravity: expected an event stream")
	}
	chunkID := "chatcmpl-" + id()
	created := time.Now().Unix()
	finished := false
	hasChoice := false
	roleSent := false
	toolIndex := 0
	hasTools := false
	chunk := func(delta map[string]any, finish any) map[string]any {
		return map[string]any{"id": chunkID, "created": created, "object": "chat.completion.chunk", "choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": finish}}}
	}
	terminal := errors.New("antigravity stream completed")
	err = readSSE(resp.Body, func(event, data string) error {
		if data == "[DONE]" {
			if !finished {
				return failure(502, "antigravity: stream ended before completion")
			}
			return terminal
		}
		var root map[string]any
		if json.Unmarshal([]byte(data), &root) != nil {
			return failure(502, "antigravity: malformed upstream event")
		}
		if nested := obj(root["response"]); nested != nil {
			root = nested
		}
		if event == "error" || root["error"] != nil {
			status := int(num(obj(root["error"])["code"]))
			if status < 400 || status > 599 {
				status = 502
			}
			return rejection(status, "antigravity: upstream rejected stream")
		}
		if str(obj(root["promptFeedback"])["blockReason"]) != "" {
			return rejection(400, "antigravity: prompt blocked by upstream")
		}
		for _, v := range arr(root["candidates"]) {
			candidate := obj(v)
			if num(candidate["index"]) != 0 {
				continue
			}
			if finished {
				return failure(502, "antigravity: unexpected content after completion")
			}
			hasChoice = true
			for _, v := range arr(obj(candidate["content"])["parts"]) {
				part := obj(v)
				delta := map[string]any{}
				if text, ok := part["text"].(string); ok && text != "" {
					field := "content"
					if flag(part["thought"]) {
						field = "reasoning_content"
					}
					delta[field] = text
				}
				if fn := obj(part["functionCall"]); fn != nil {
					name := str(fn["name"])
					if name == "" {
						return failure(502, "antigravity: invalid function call event")
					}
					callID := str(fn["id"])
					if callID == "" {
						callID = "call_" + strings.ReplaceAll(id(), "-", "")
					}
					args := fn["args"]
					if args == nil {
						args = map[string]any{}
					}
					raw, err := json.Marshal(args)
					if err != nil {
						return failure(502, "antigravity: invalid function arguments")
					}
					tool := map[string]any{"index": toolIndex, "id": callID, "type": "function", "function": map[string]any{"name": name, "arguments": string(raw)}}
					if sig := str(part["thoughtSignature"]); sig != "" {
						r.antigravitySignature(c.ID, m.ID, callID, sig)
						tool["extra_content"] = map[string]any{"google": map[string]any{"thought_signature": sig}}
					}
					delta["tool_calls"] = []any{tool}
					toolIndex++
					hasTools = true
				}
				if part["inlineData"] != nil {
					return failure(502, "antigravity: unsupported image output")
				}
				if len(delta) > 0 {
					if !roleSent {
						delta["role"] = "assistant"
						roleSent = true
					}
					if err := emit(chunk(delta, nil)); err != nil {
						return err
					}
				}
			}
			if reason := str(candidate["finishReason"]); reason != "" {
				finish := "stop"
				switch reason {
				case "STOP":
					if hasTools {
						finish = "tool_calls"
					}
				case "MAX_TOKENS":
					finish = "length"
				case "SAFETY", "RECITATION", "BLOCKLIST", "PROHIBITED_CONTENT", "SPII", "IMAGE_SAFETY":
					finish = "content_filter"
				default:
					return failure(502, fmt.Sprintf("antigravity: generation ended with %s", reason))
				}
				finished = true
				delta := map[string]any{}
				if !roleSent {
					delta["role"] = "assistant"
					roleSent = true
				}
				if err := emit(chunk(delta, finish)); err != nil {
					return err
				}
			}
		}
		if u := obj(root["usageMetadata"]); u != nil {
			prompt := num(u["promptTokenCount"])
			thoughts := num(u["thoughtsTokenCount"])
			completion := num(u["candidatesTokenCount"]) + thoughts
			total := num(u["totalTokenCount"])
			if total == 0 {
				total = prompt + completion
			}
			usage := map[string]any{"prompt_tokens": prompt, "completion_tokens": completion, "total_tokens": total, "prompt_tokens_details": map[string]any{"cached_tokens": num(u["cachedContentTokenCount"])}, "completion_tokens_details": map[string]any{"reasoning_tokens": thoughts}}
			out := map[string]any{"id": chunkID, "created": created, "object": "chat.completion.chunk", "choices": []any{}, "usage": usage}
			if err := emit(out); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil && !errors.Is(err, terminal) {
		return err
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if !finished || !hasChoice {
		return failure(502, "antigravity: upstream stream ended before completion")
	}
	return nil
}
