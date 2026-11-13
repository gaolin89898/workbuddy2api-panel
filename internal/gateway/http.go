package gateway

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

func (r *Router) headers(p string, c Credential) http.Header {
	h := http.Header{}
	h.Set("Content-Type", "application/json")
	h.Set("Accept", "text/event-stream")
	switch p {
	case "doubao":
		h.Set("Cookie", c.Token)
		h.Set("agw-js-conv", "str, str")
		h.Set("Origin", "https://www.doubao.com")
		h.Set("Referer", "https://www.doubao.com/chat/")
		h.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36")
	case "antigravity":
		h.Set("Authorization", "Bearer "+c.Token)
		h.Set("User-Agent", "antigravity/hub/2.29.1 (aidev_client; os_type=windows; arch=amd64; cl=1015941558)")
	case "loomy":
		h.Set("Authorization", "Bearer "+c.Token)
		h.Set("token", c.Token)
		h.Set("loomy-version", "0.9.39")
		h.Set("traceparent", "00-"+strings.ReplaceAll(id(), "-", "")+"-"+strings.ReplaceAll(id(), "-", "")[:16]+"-01")
	case "minimax":
		h.Set("Authorization", "Bearer "+c.Token)
		h.Set("x-api-key", c.Token)
		h.Set("anthropic-version", "2023-06-01")
		h.Set("User-Agent", "MiniMaxAgent")
		h.Set("X-Mavis-Session-Id", id())
		h.Set("X-Mavis-Agent-Id", "main")
		h.Set("X-Mavis-Timezone-Offset", "28800")
	case "trae":
		for k, v := range map[string]string{"Authorization": "Cloud-IDE-JWT " + c.Token, "X-Cloudide-Token": c.Token, "X-Ide-Token": c.Token, "X-Uid": c.UID, "X-App-Id": "6eefa01c-1036-4c7e-9ca5-d891f63bfcd8", "X-Device-Id": c.DeviceID, "X-Machine-Id": c.MachineID, "X-Ide-Version": "3.3.67", "X-Ide-Version-Code": "20260401", "X-App-Version-Code": "20260401", "X-Ide-Version-Type": "stable", "X-Plugin-Channel": "desktop", "X-App-Function": "chat_v3", "X-Ide-Function": "chat_v3", "X-Request-Id": id(), "request-traffic-type": "prod"} {
			if v != "" {
				h.Set(k, v)
			}
		}
	}
	return h
}
func (r *Router) request(ctx context.Context, p, method, path string, c Credential, body any, model string) (*http.Response, error) {
	var raw []byte
	var err error
	if body != nil {
		raw, err = json.Marshal(body)
		if err != nil {
			return nil, failure(400, "invalid request parameters")
		}
	}
	headers := r.headers(p, c)
	if p == "qoder" {
		raw = []byte(qoderEncode(raw))
		headers, err = qoderHeaders(c, string(raw), path, model)
		if err != nil {
			return nil, failure(502, "qoder: unable to sign request")
		}
	}
	req, err := http.NewRequestWithContext(ctx, method, r.Bases[p]+path, bytes.NewReader(raw))
	if err != nil {
		return nil, failure(502, p+": invalid endpoint")
	}
	req.Header = headers
	resp, err := r.HTTP.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, failure(502, p+": upstream connection failed")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		resp.Body.Close()
		status := resp.StatusCode
		if status < 400 {
			status = 502
		}
		return nil, rejection(status, p+": upstream rejected request (HTTP "+strconv.Itoa(resp.StatusCode)+")")
	}
	return resp, nil
}

func (r *Router) fetchModels(ctx context.Context, p string, c Credential) ([]Model, error) {
	if p == "doubao" {
		return []Model{{ID: "doubao", Fields: map[string]any{"name": "豆包", "supports_images": false, "supports_tool_call": false, "supports_reasoning": false, "catalog_source": "web_builtin"}}}, nil
	}
	if p == "minimax" {
		return []Model{
			{ID: "MiniMax-M3", Fields: map[string]any{"name": "MiniMax-M3", "context_length": 512000, "max_output_tokens": 128000, "supports_reasoning": true, "supports_images": true, "supports_tool_call": true, "catalog_source": "native_builtin"}},
			{ID: "MiniMax-M3.1-Flash", Fields: map[string]any{"name": "M3.1-Flash", "context_length": 512000, "max_output_tokens": 128000, "supports_reasoning": true, "supports_images": true, "supports_tool_call": true, "catalog_source": "native_builtin"}},
		}, nil
	}
	method, path := "GET", "/models"
	var body any
	switch p {
	case "antigravity":
		method, path = "POST", "/v1internal:fetchAvailableModels"
		body = map[string]any{"project": c.ProjectID}
	case "qoder":
		path = "/algo/api/v2/model/list?Encode=1"
		body = map[string]any{}
	case "trae":
		method = "POST"
		path = "/api/ide/v1/get_detail_param"
		body = map[string]any{"function": "chat_v3", "config_names": nil, "need_prompt": false, "current_config_info": nil, "poly_prompt": true, "mode_type": nil, "agent_type": nil}
	}
	resp, err := r.request(ctx, p, method, path, c, body, "")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, (8<<20)+1))
	if err != nil || len(raw) > 8<<20 {
		return nil, failure(502, p+": invalid model catalog")
	}
	var root map[string]any
	if json.Unmarshal(raw, &root) != nil {
		return nil, failure(502, p+": invalid model catalog")
	}
	out := []Model{}
	switch p {
	case "antigravity":
		out = antigravityModels(root)
	case "loomy":
		for _, v := range arr(root["data"]) {
			m := obj(v)
			if str(m["id"]) == "" || str(m["type"]) != "chat" || str(m["protocol"]) != "openai_chat" {
				continue
			}
			f := map[string]any{}
			for _, k := range []string{"name", "context_length", "max_output_tokens"} {
				if v, ok := m[k]; ok {
					f[k] = v
				}
			}
			cap := obj(m["capabilities"])
			f["supports_reasoning"] = flag(cap["reasoning"])
			f["supports_images"] = flag(cap["vision"])
			f["supports_tool_call"] = flag(cap["function_calling"])
			f["supported_efforts"] = m["reasoning_efforts"]
			f["default_effort"] = m["default_reasoning_effort"]
			out = append(out, Model{ID: str(m["id"]), Fields: f})
		}
	case "qoder":
		if nested := obj(root["data"]); nested != nil {
			root = nested
		}
		for _, v := range arr(root["chat"]) {
			m := obj(v)
			if !flag(m["enable"]) || str(m["key"]) == "" {
				continue
			}
			out = append(out, Model{ID: str(m["key"]), Fields: map[string]any{"name": m["display_name"], "context_length": m["max_input_tokens"], "supports_images": flag(m["is_vl"]), "supports_reasoning": flag(m["is_reasoning"])}, wire: m})
		}
	case "trae":
		for _, v := range arr(root["config_info_list"]) {
			m := obj(v)
			display := obj(m["display_config"])
			name := str(m["config_name"])
			if name == "" || !flag(m["config_switch"]) || flag(m["is_invisible_to_user"]) || flag(display["is_custom_model"]) || strings.HasPrefix(name, "custom_model") || strings.Contains(name, "__dev") || strings.Contains(name, "advisor") || str(m["usage"]) != "chat_completion" {
				continue
			}
			details := arr(m["model_detail_list"])
			if len(details) == 0 {
				continue
			}
			d := obj(details[0])
			wire := map[string]any{"config_name": name, "model_name": d["model_name"], "config_source": m["config_source"]}
			if params := d["encrypted_model_params"]; params != nil {
				wire["encrypted_model_params"] = params
			}
			if str(wire["model_name"]) == "" {
				continue
			}
			extra := map[string]any{}
			_ = json.Unmarshal([]byte(str(m["extra_config"])), &extra)
			out = append(out, Model{ID: name, Fields: map[string]any{"name": display["display_name"], "context_length": d["prompt_max_tokens"], "max_output_tokens": d["max_tokens"], "supports_images": flag(display["multimodal"]), "supports_tool_call": flag(extra["native_function_call"])}, wire: wire})
		}
	}
	if len(out) == 0 {
		return nil, failure(502, p+": no usable chat models returned")
	}
	return out, nil
}

func (r *Router) chat(ctx context.Context, p string, c Credential, m Model, request map[string]any, session string, emit Emit) error {
	if p == "doubao" {
		return r.doubaoChat(ctx, c, m, request, emit)
	}
	if p == "antigravity" {
		return r.antigravityChat(ctx, c, m, request, session, emit)
	}
	if session == "" {
		session = id()
	}
	req := clone(request)
	req["stream"] = true
	req["stream_options"] = map[string]any{"include_usage": true}
	path := "/chat/completions"
	switch p {
	case "minimax":
		var err error
		req, err = anthropicRequest(request)
		if err != nil {
			return err
		}
		path = "/messages"
	case "qoder":
		req = qoderBody(m, request, session)
		path = "/algo/api/v2/service/pro/sse/agent_chat_generation?FetchKeys=llm_model_result&AgentId=agent_common&Encode=1"
	case "trae":
		req = clone(m.wire)
		sid := session
		req["conversation_id"] = sid
		req["session_id"] = sid
		req["stream"] = true
		req["raw_chat_function"] = "chat_v3"
		messages := []any{}
		for _, v := range arr(request["messages"]) {
			message := clone(obj(v))
			if str(message["role"]) == "developer" {
				message["role"] = "system"
			}
			if text, ok := message["content"].(string); ok {
				message["content"] = []any{map[string]any{"type": "text", "text": text}}
			}
			messages = append(messages, message)
		}
		req["messages"] = messages
		for _, k := range []string{"tools", "tool_choice", "parallel_tool_calls", "max_tokens", "temperature", "top_p", "stop", "reasoning_effort"} {
			if v, ok := request[k]; ok {
				req[k] = v
			}
		}
		if v, ok := request["max_completion_tokens"]; ok {
			req["max_tokens"] = v
		}
		path = "/api/ide/v2/llm_raw_chat"
	}
	resp, err := r.request(ctx, p, "POST", path, c, req, m.ID)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if !strings.Contains(strings.ToLower(resp.Header.Get("Content-Type")), "text/event-stream") {
		return failure(502, p+": expected an event stream")
	}
	state := anthropicState{id: "chatcmpl-" + id(), created: time.Now().Unix(), tools: map[int]int{}, usage: map[string]any{}}
	trae := traeState{id: "chatcmpl-" + id(), created: time.Now().Unix(), tools: map[string]int{}}
	finished := false
	terminal := errors.New("upstream stream completed")
	sawChoice := false
	forward := emit
	emit = func(chunk map[string]any) error {
		if len(arr(chunk["choices"])) > 0 {
			sawChoice = true
		}
		return forward(chunk)
	}
	err = readSSE(resp.Body, func(event, data string) error {
		if data == "[DONE]" {
			if p == "trae" && !finished {
				if err := trae.event("done", map[string]any{}, emit); err != nil {
					return err
				}
			}
			finished = true
			return terminal
		}
		if strings.HasPrefix(data, "[NOTIFICATIONS]#") {
			return nil
		}
		var chunk map[string]any
		if json.Unmarshal([]byte(data), &chunk) != nil {
			return failure(502, p+": malformed upstream event")
		}
		if p == "trae" && event == "" {
			event = str(chunk["event"])
			if event == "" {
				event = str(chunk["type"])
			}
		}
		if p == "qoder" {
			if status := int(num(chunk["statusCodeValue"])); status != 0 && status != 200 {
				return rejection(status, "qoder: upstream rejected stream")
			}
			if body, ok := chunk["body"].(string); ok {
				if body == "[DONE]" {
					finished = true
					return terminal
				}
				var decoded map[string]any
				if json.Unmarshal([]byte(body), &decoded) != nil || decoded == nil {
					return failure(502, "qoder: malformed upstream event")
				}
				chunk = decoded
			} else {
				if event == "finish" {
					return nil
				}
			}
		}
		if event == "error" || chunk["error"] != nil || str(chunk["type"]) == "error" {
			status := 502
			if code := num(chunk["code"]); code == 4011 || code == 3004 || code == 4008 || code == 429 {
				status = 429
			}
			return rejection(status, p+": upstream stream rejected request")
		}
		if p == "minimax" {
			if str(chunk["type"]) == "message_stop" {
				finished = true
				if err := state.event(chunk, emit); err != nil {
					return err
				}
				return terminal
			}
			return state.event(chunk, emit)
		}
		if p == "trae" && len(arr(chunk["choices"])) == 0 {
			if event == "" {
				event = str(chunk["type"])
				if event == "" {
					event = str(chunk["event"])
				}
			}
			if event == "done" {
				finished = true
				if err := trae.event(event, chunk, emit); err != nil {
					return err
				}
				return terminal
			}
			return trae.event(event, chunk, emit)
		}
		if len(arr(chunk["choices"])) == 0 && obj(chunk["usage"]) == nil {
			return nil
		}
		for _, v := range arr(chunk["choices"]) {
			if str(obj(v)["finish_reason"]) != "" {
				finished = true
			}
		}
		chunk["object"] = "chat.completion.chunk"
		return emit(chunk)
	})
	if err != nil && !errors.Is(err, terminal) {
		return err
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if !finished || !sawChoice {
		return failure(502, p+": upstream stream ended before completion")
	}
	return nil
}

// readSSE supports CRLF, data without a space, multiline events and EOF dispatch.
func readSSE(in io.Reader, visit func(event, data string) error) error {
	scanner := bufio.NewScanner(in)
	scanner.Buffer(make([]byte, 4096), 16<<20)
	event := ""
	data := []string{}
	dispatch := func() error {
		if len(data) == 0 {
			event = ""
			return nil
		}
		err := visit(event, strings.Join(data, "\n"))
		event = ""
		data = data[:0]
		return err
	}
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			if err := dispatch(); err != nil {
				return err
			}
			continue
		}
		if strings.HasPrefix(line, ":") {
			continue
		}
		field, value, _ := strings.Cut(line, ":")
		value = strings.TrimPrefix(value, " ")
		switch field {
		case "event":
			event = value
		case "data":
			data = append(data, value)
		}
	}
	if err := scanner.Err(); err != nil {
		return failure(502, "upstream stream read failed")
	}
	return dispatch()
}
