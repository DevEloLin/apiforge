package codex

import (
	"encoding/json"
	"io"
	"strings"

	"apiforge/internal/util/idgen"
	"apiforge/internal/util/sse"
)

// ---------------------------------------------------------------------------
// OpenAI Chat request types (only the fields we translate)
// ---------------------------------------------------------------------------

type chatRequest struct {
	Model           string          `json:"model"`
	Messages        []chatMessage   `json:"messages"`
	Tools           []chatTool      `json:"tools,omitempty"`
	ToolChoice      json.RawMessage `json:"tool_choice,omitempty"`
	ReasoningEffort string          `json:"reasoning_effort,omitempty"`
	Stream          bool            `json:"stream,omitempty"`
}

type chatMessage struct {
	Role       string          `json:"role"`
	Content    json.RawMessage `json:"content"`
	ToolCallID string          `json:"tool_call_id,omitempty"`
	ToolCalls  []toolCall      `json:"tool_calls,omitempty"`
}

type toolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type chatTool struct {
	Type     string `json:"type"`
	Function struct {
		Name        string          `json:"name"`
		Description string          `json:"description"`
		Parameters  json.RawMessage `json:"parameters"`
	} `json:"function"`
}

type contentPart struct {
	Type     string `json:"type"`
	Text     string `json:"text"`
	ImageURL struct {
		URL string `json:"url"`
	} `json:"image_url"`
}

// extractText concatenates the text parts of a content field (string or array).
func extractText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var parts []contentPart
	if json.Unmarshal(raw, &parts) != nil {
		return ""
	}
	var b strings.Builder
	for _, p := range parts {
		if p.Type == "text" || p.Type == "output_text" || p.Type == "input_text" {
			b.WriteString(p.Text)
		}
	}
	return b.String()
}

// userContent maps an OpenAI user content field to Codex input parts.
func userContent(raw json.RawMessage) []any {
	if len(raw) == 0 {
		return []any{map[string]any{"type": "input_text", "text": ""}}
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return []any{map[string]any{"type": "input_text", "text": s}}
	}
	var parts []contentPart
	if json.Unmarshal(raw, &parts) != nil {
		return []any{map[string]any{"type": "input_text", "text": ""}}
	}
	out := make([]any, 0, len(parts))
	for _, p := range parts {
		if p.Type == "image_url" {
			out = append(out, map[string]any{"type": "input_image", "image_url": p.ImageURL.URL})
		} else {
			out = append(out, map[string]any{"type": "input_text", "text": p.Text})
		}
	}
	return out
}

// openaiToCodex converts an OpenAI Chat request into a Codex /responses body.
func openaiToCodex(req chatRequest, sessionID string) map[string]any {
	var instructions []string
	input := []any{}

	for _, msg := range req.Messages {
		switch msg.Role {
		case "system", "developer":
			if t := extractText(msg.Content); t != "" {
				instructions = append(instructions, t)
			}
		case "tool":
			input = append(input, map[string]any{
				"type":    "function_call_output",
				"call_id": msg.ToolCallID,
				"output":  extractText(msg.Content),
			})
		case "assistant":
			if t := extractText(msg.Content); t != "" {
				input = append(input, map[string]any{
					"type":    "message",
					"role":    "assistant",
					"content": []any{map[string]any{"type": "output_text", "text": t}},
				})
			}
			for _, call := range msg.ToolCalls {
				input = append(input, map[string]any{
					"type":      "function_call",
					"name":      call.Function.Name,
					"arguments": call.Function.Arguments,
					"call_id":   call.ID,
				})
			}
		default: // user
			input = append(input, map[string]any{
				"type": "message", "role": "user", "content": userContent(msg.Content),
			})
		}
	}

	body := map[string]any{
		"model":            req.Model,
		"instructions":     strings.Join(instructions, "\n\n"),
		"input":            input,
		"store":            false,
		"stream":           true,
		"prompt_cache_key": sessionID,
	}

	if len(req.Tools) > 0 {
		tools := make([]any, 0, len(req.Tools))
		for _, t := range req.Tools {
			params := json.RawMessage(t.Function.Parameters)
			if len(params) == 0 {
				params = json.RawMessage(`{"type":"object","properties":{}}`)
			}
			tools = append(tools, map[string]any{
				"type":        "function",
				"name":        t.Function.Name,
				"description": t.Function.Description,
				"parameters":  params,
				"strict":      false,
			})
		}
		body["tools"] = tools
		if len(req.ToolChoice) > 0 {
			body["tool_choice"] = json.RawMessage(req.ToolChoice)
		} else {
			body["tool_choice"] = "auto"
		}
		body["parallel_tool_calls"] = false
	}

	if req.ReasoningEffort != "" {
		body["reasoning"] = map[string]any{"effort": req.ReasoningEffort, "summary": "auto"}
		body["include"] = []any{"reasoning.encrypted_content"}
	} else {
		body["include"] = []any{}
	}
	return body
}

// ---------------------------------------------------------------------------
// Codex /responses SSE -> OpenAI chunks / aggregated completion
// ---------------------------------------------------------------------------

type toolAcc struct {
	id, name, args string
}

type collector struct {
	text      string
	tools     map[string]*toolAcc
	order     []string
	finish    string
	inTok     int
	outTok    int
	completed bool // saw response.completed
	failed    bool // saw response.failed
}

func newCollector() *collector {
	return &collector{tools: map[string]*toolAcc{}, finish: "stop"}
}

type delta struct {
	textDelta    string
	toolOpen     *toolAcc
	toolArgsKey  string
	toolArgsData string
}

// apply folds one Codex SSE event into the collector, returning stream deltas.
func (c *collector) apply(evt map[string]any) delta {
	typ, _ := evt["type"].(string)

	switch typ {
	case "response.output_text.delta":
		d := str(evt["delta"])
		c.text += d
		return delta{textDelta: d}

	case "response.output_item.added", "response.output_item.done":
		item, _ := evt["item"].(map[string]any)
		if item == nil || str(item["type"]) != "function_call" {
			return delta{}
		}
		key := firstNonEmpty(str(item["call_id"]), str(item["id"]), idgen.OpenAI("call"))
		acc, existing := c.tools[key]
		if !existing {
			acc = &toolAcc{id: key}
		}
		if n := str(item["name"]); n != "" {
			acc.name = n
		}
		var argsDelta string
		if typ == "response.output_item.done" {
			if a := str(item["arguments"]); a != "" && acc.args == "" {
				acc.args = a
				argsDelta = a
			}
		}
		if !existing {
			c.tools[key] = acc
			c.order = append(c.order, key)
			c.finish = "tool_calls"
			if argsDelta != "" {
				return delta{toolOpen: acc, toolArgsKey: key, toolArgsData: argsDelta}
			}
			return delta{toolOpen: acc}
		}
		if argsDelta != "" {
			return delta{toolArgsKey: key, toolArgsData: argsDelta}
		}
		return delta{}

	case "response.function_call_arguments.delta", "response.custom_tool_call_input.delta":
		key := firstNonEmpty(str(evt["call_id"]), str(evt["item_id"]))
		d := str(evt["delta"])
		if acc, ok := c.tools[key]; ok {
			acc.args += d
			return delta{toolArgsKey: key, toolArgsData: d}
		}
		return delta{}

	case "response.completed":
		c.completed = true
		if resp, ok := evt["response"].(map[string]any); ok {
			if u, ok := resp["usage"].(map[string]any); ok {
				c.inTok = intOf(u["input_tokens"])
				c.outTok = intOf(u["output_tokens"])
			}
		}
	case "response.incomplete":
		c.completed = true
		c.finish = "length"
	case "response.failed":
		c.failed = true
	}
	return delta{}
}

// StreamChatCompletion translates a Codex responses SSE stream into OpenAI
// chat.completion.chunk frames, writing them to w and finishing with [DONE].
func StreamChatCompletion(w io.Writer, upstream io.Reader, model string) {
	id := idgen.OpenAI("chatcmpl")
	created := idgen.NowSeconds()
	c := newCollector()
	toolIndex := map[string]int{}

	chunk := func(delta map[string]any, finish any) {
		frame := map[string]any{
			"id": id, "object": "chat.completion.chunk", "created": created, "model": model,
			"choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": finish}},
		}
		b, _ := json.Marshal(frame)
		_, _ = w.Write(sse.DataFrame(string(b)))
	}

	chunk(map[string]any{"role": "assistant", "content": ""}, nil)

	for ev := range sse.Frames(upstream) {
		if ev.Data == "" || ev.Data == "[DONE]" {
			continue
		}
		var evt map[string]any
		if json.Unmarshal([]byte(ev.Data), &evt) != nil {
			continue
		}
		typ, _ := evt["type"].(string)
		out := c.apply(evt)

		if out.textDelta != "" {
			chunk(map[string]any{"content": out.textDelta}, nil)
		}
		if out.toolOpen != nil {
			idx := len(toolIndex)
			toolIndex[out.toolOpen.id] = idx
			chunk(map[string]any{"tool_calls": []any{map[string]any{
				"index": idx, "id": out.toolOpen.id, "type": "function",
				"function": map[string]any{"name": out.toolOpen.name, "arguments": ""},
			}}}, nil)
		}
		if out.toolArgsData != "" {
			idx := toolIndex[out.toolArgsKey]
			chunk(map[string]any{"tool_calls": []any{map[string]any{
				"index": idx, "function": map[string]any{"arguments": out.toolArgsData},
			}}}, nil)
		}

		if typ == "response.completed" || typ == "response.incomplete" {
			chunk(map[string]any{}, c.finish)
			_, _ = w.Write(sse.Done)
			return
		}
		if typ == "response.failed" {
			_, _ = w.Write(sse.Done)
			return
		}
	}
	chunk(map[string]any{}, c.finish)
	_, _ = w.Write(sse.Done)
}

// AggregateChatCompletion consumes a Codex responses SSE stream and returns one
// OpenAI chat.completion object (non-streaming callers). ok is false when the
// upstream failed or truncated with no usable output — the caller must then
// surface an error rather than a fake empty 200.
func AggregateChatCompletion(upstream io.Reader, model string) (result map[string]any, ok bool) {
	c := newCollector()
	for ev := range sse.Frames(upstream) {
		if ev.Data == "" || ev.Data == "[DONE]" {
			continue
		}
		var evt map[string]any
		if json.Unmarshal([]byte(ev.Data), &evt) != nil {
			continue
		}
		c.apply(evt)
	}

	// No usable output AND the stream did not complete normally → treat as an
	// upstream failure (backend error / content policy / truncated connection),
	// never a silent empty success.
	if c.failed || (!c.completed && c.text == "" && len(c.order) == 0) {
		return nil, false
	}

	toolCalls := make([]any, 0, len(c.order))
	for _, key := range c.order {
		t := c.tools[key]
		args := t.args
		if args == "" {
			args = "{}"
		}
		toolCalls = append(toolCalls, map[string]any{
			"id": t.id, "type": "function",
			"function": map[string]any{"name": t.name, "arguments": args},
		})
	}

	message := map[string]any{"role": "assistant"}
	if c.text != "" {
		message["content"] = c.text
	} else {
		message["content"] = nil
	}
	if len(toolCalls) > 0 {
		message["tool_calls"] = toolCalls
	}

	return map[string]any{
		"id": idgen.OpenAI("chatcmpl"), "object": "chat.completion",
		"created": idgen.NowSeconds(), "model": model,
		"choices": []any{map[string]any{"index": 0, "message": message, "finish_reason": c.finish}},
		"usage": map[string]any{
			"prompt_tokens": c.inTok, "completion_tokens": c.outTok,
			"total_tokens": c.inTok + c.outTok,
		},
	}, true
}

// AggregateResponses consumes a Codex responses SSE stream and returns one
// OpenAI Responses object (non-streaming /v1/responses callers).
func AggregateResponses(upstream io.Reader, model string) map[string]any {
	var text, id string
	var usage any
	for ev := range sse.Frames(upstream) {
		if ev.Data == "" || ev.Data == "[DONE]" {
			continue
		}
		var evt map[string]any
		if json.Unmarshal([]byte(ev.Data), &evt) != nil {
			continue
		}
		switch str(evt["type"]) {
		case "response.output_text.delta":
			text += str(evt["delta"])
		case "response.completed":
			if r, ok := evt["response"].(map[string]any); ok {
				if s := str(r["id"]); s != "" {
					id = s
				}
				usage = r["usage"]
			}
		}
	}
	if id == "" {
		id = idgen.OpenAI("resp")
	}
	return map[string]any{
		"id": id, "object": "response", "created_at": idgen.NowSeconds(),
		"model": model, "status": "completed",
		"output": []any{map[string]any{
			"type": "message", "role": "assistant",
			"content": []any{map[string]any{"type": "output_text", "text": text}},
		}},
		"usage": usage,
	}
}

// ---- small helpers ---------------------------------------------------------

func str(v any) string {
	s, _ := v.(string)
	return s
}

func intOf(v any) int {
	if f, ok := v.(float64); ok {
		return int(f)
	}
	return 0
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
