package gateway

import "sort"

// Completion collects the same chunks used for SSE so tools, reasoning and usage
// have identical semantics for stream=false callers.
type Completion struct {
	ID      string
	Created any
	Model   string
	Usage   any
	choices map[int]*choice
}
type choice struct {
	content, reasoning, finish string
	tools                      map[int]map[string]any
}

func (c *Completion) Add(chunk map[string]any) error {
	if c.choices == nil {
		c.choices = map[int]*choice{}
	}
	if s := str(chunk["id"]); s != "" {
		c.ID = s
	}
	if v := chunk["created"]; v != nil {
		c.Created = v
	}
	c.Model = str(chunk["model"])
	if v := chunk["usage"]; v != nil {
		c.Usage = v
	}
	for _, v := range arr(chunk["choices"]) {
		ch := obj(v)
		index := int(num(ch["index"]))
		target := c.choices[index]
		if target == nil {
			target = &choice{tools: map[int]map[string]any{}}
			c.choices[index] = target
		}
		delta := obj(ch["delta"])
		target.content += str(delta["content"])
		target.reasoning += str(delta["reasoning_content"])
		if s := str(ch["finish_reason"]); s != "" {
			target.finish = s
		}
		for _, v := range arr(delta["tool_calls"]) {
			part := obj(v)
			idx := int(num(part["index"]))
			tool := target.tools[idx]
			if tool == nil {
				tool = map[string]any{"type": "function", "function": map[string]any{"name": "", "arguments": ""}}
				target.tools[idx] = tool
			}
			if s := str(part["id"]); s != "" {
				tool["id"] = s
			}
			if extra := obj(part["extra_content"]); extra != nil {
				tool["extra_content"] = extra
			}
			fn := obj(tool["function"])
			pfn := obj(part["function"])
			fn["name"] = str(fn["name"]) + str(pfn["name"])
			fn["arguments"] = str(fn["arguments"]) + str(pfn["arguments"])
		}
	}
	return nil
}
func (c *Completion) Response() map[string]any {
	result := map[string]any{"id": c.ID, "object": "chat.completion", "created": c.Created, "model": c.Model}
	choices := []any{}
	keys := []int{}
	for k := range c.choices {
		keys = append(keys, k)
	}
	sort.Ints(keys)
	for _, k := range keys {
		ch := c.choices[k]
		message := map[string]any{"role": "assistant", "content": ch.content}
		if ch.reasoning != "" {
			message["reasoning_content"] = ch.reasoning
		}
		if len(ch.tools) > 0 {
			tools := []any{}
			indices := []int{}
			for i := range ch.tools {
				indices = append(indices, i)
			}
			sort.Ints(indices)
			for _, i := range indices {
				tools = append(tools, ch.tools[i])
			}
			message["tool_calls"] = tools
			if ch.content == "" {
				message["content"] = nil
			}
		}
		choices = append(choices, map[string]any{"index": k, "message": message, "finish_reason": ch.finish})
	}
	result["choices"] = choices
	if c.Usage != nil {
		result["usage"] = c.Usage
	}
	return result
}
