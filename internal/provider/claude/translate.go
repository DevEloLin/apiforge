package claude

import (
	"encoding/json"
	"io"
	"strings"

	"apiforge/internal/util/idgen"
	"apiforge/internal/util/sse"
)

// claudeCodeIdentity is the required first system block for a Claude Code OAuth
// token to be accepted.
const claudeCodeIdentity = "You are Claude Code, Anthropic's official CLI for Claude."

const defaultMaxTokens = 4096

// ---- OpenAI Chat request types (only what we translate) --------------------

type chatRequest struct {
	Model               string          `json:"model"`
	Messages            []chatMessage   `json:"messages"`
	Tools               []chatTool      `json:"tools,omitempty"`
	Stream              bool            `json:"stream,omitempty"`
	Temperature         *float64        `json:"temperature,omitempty"`
	TopP                *float64        `json:"top_p,omitempty"`
	MaxTokens           *int            `json:"max_tokens,omitempty"`
	MaxCompletionTokens *int            `json:"max_completion_tokens,omitempty"`
	Stop                json.RawMessage `json:"stop,omitempty"`
}

type chatMessage struct {
	Role       string          `json:"role"`
	Content    json.RawMessage `json:"content"`
	ToolCallID string          `json:"tool_call_id,omitempty"`
	ToolCalls  []toolCall      `json:"tool_calls,omitempty"`
}

type toolCall struct {
	ID       string `json:"id"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type chatTool struct {
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

// ensureIdentitySystem returns an Anthropic system array whose first block is
// exactly the Claude Code identity string. Idempotent and non-mutating.
func ensureIdentitySystem(system any) []any {
	identity := map[string]any{"type": "text", "text": claudeCodeIdentity}
	switch s := system.(type) {
	case nil:
		return []any{identity}
	case string:
		if strings.TrimSpace(s) == claudeCodeIdentity {
			return []any{identity}
		}
		return []any{identity, map[string]any{"type": "text", "text": s}}
	case []any:
		if len(s) > 0 {
			if first, ok := s[0].(map[string]any); ok {
				if t, _ := first["text"].(string); strings.TrimSpace(t) == claudeCodeIdentity {
					return s
				}
			}
		}
		return append([]any{identity}, s...)
	default:
		return []any{identity}
	}
}

// ---- OpenAI Chat -> Anthropic Messages -------------------------------------

func stringContent(raw json.RawMessage) (string, bool) {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s, true
	}
	return "", false
}

func normalizeContent(raw json.RawMessage) []any {
	// null / absent / empty-string content yields NO blocks. Anthropic rejects
	// empty text blocks with 400, and the common assistant tool-call turn is
	// {"content":null,"tool_calls":[...]} — its tool_use blocks are added
	// separately, so emitting an empty text block here would break it.
	if len(raw) == 0 || strings.TrimSpace(string(raw)) == "null" {
		return []any{}
	}
	if s, ok := stringContent(raw); ok {
		if s == "" {
			return []any{}
		}
		return []any{map[string]any{"type": "text", "text": s}}
	}
	var parts []contentPart
	if json.Unmarshal(raw, &parts) != nil {
		return []any{}
	}
	out := make([]any, 0, len(parts))
	for _, p := range parts {
		if p.Type == "text" {
			out = append(out, map[string]any{"type": "text", "text": p.Text})
			continue
		}
		url := p.ImageURL.URL
		if strings.HasPrefix(url, "data:") {
			meta, b64, _ := strings.Cut(url, ",")
			media := "image/png"
			if len(meta) > 5 {
				media = strings.SplitN(meta[5:], ";", 2)[0]
			}
			out = append(out, map[string]any{"type": "image", "source": map[string]any{
				"type": "base64", "media_type": media, "data": b64,
			}})
		} else {
			out = append(out, map[string]any{"type": "image", "source": map[string]any{
				"type": "url", "url": url,
			}})
		}
	}
	return out
}

type msg struct {
	role    string
	content []any
}

func pushMerged(messages []msg, role string, content []any) []msg {
	if len(content) == 0 && role == "assistant" {
		return messages
	}
	if n := len(messages); n > 0 && messages[n-1].role == role {
		messages[n-1].content = append(messages[n-1].content, content...)
		return messages
	}
	return append(messages, msg{role: role, content: append([]any(nil), content...)})
}

func safeJSON(s string) any {
	var v any
	if json.Unmarshal([]byte(s), &v) == nil {
		return v
	}
	return map[string]any{}
}

func openaiToAnthropic(req chatRequest) map[string]any {
	var systemTexts []string
	var messages []msg

	for _, m := range req.Messages {
		switch m.Role {
		case "system", "developer":
			if s, ok := stringContent(m.Content); ok && s != "" {
				systemTexts = append(systemTexts, s)
			}
		case "tool":
			messages = pushMerged(messages, "user", []any{map[string]any{
				"type": "tool_result", "tool_use_id": m.ToolCallID, "content": toolResultContent(m.Content),
			}})
		case "assistant":
			var blocks []any
			blocks = append(blocks, normalizeContent(m.Content)...)
			for _, call := range m.ToolCalls {
				blocks = append(blocks, map[string]any{
					"type": "tool_use", "id": call.ID, "name": call.Function.Name,
					"input": safeJSON(call.Function.Arguments),
				})
			}
			messages = pushMerged(messages, "assistant", blocks)
		default:
			messages = pushMerged(messages, "user", normalizeContent(m.Content))
		}
	}

	out := map[string]any{
		"model":      req.Model,
		"max_tokens": maxTokens(req),
		"messages":   messagesToAny(messages),
		"stream":     req.Stream,
	}
	if len(systemTexts) > 0 {
		out["system"] = []any{map[string]any{"type": "text", "text": strings.Join(systemTexts, "\n\n")}}
	}
	if req.Temperature != nil {
		out["temperature"] = *req.Temperature
	}
	if req.TopP != nil {
		out["top_p"] = *req.TopP
	}
	if len(req.Stop) > 0 {
		out["stop_sequences"] = stopSequences(req.Stop)
	}
	if len(req.Tools) > 0 {
		tools := make([]any, 0, len(req.Tools))
		for _, t := range req.Tools {
			schema := json.RawMessage(t.Function.Parameters)
			if len(schema) == 0 {
				schema = json.RawMessage(`{"type":"object","properties":{}}`)
			}
			tools = append(tools, map[string]any{
				"name": t.Function.Name, "description": t.Function.Description, "input_schema": schema,
			})
		}
		out["tools"] = tools
	}
	return out
}

// toolResultContent keeps a string result as a string, else normalizes parts.
func toolResultContent(raw json.RawMessage) any {
	if s, ok := stringContent(raw); ok {
		return s
	}
	return normalizeContent(raw)
}

func messagesToAny(messages []msg) []any {
	out := make([]any, len(messages))
	for i, m := range messages {
		out[i] = map[string]any{"role": m.role, "content": m.content}
	}
	return out
}

func maxTokens(req chatRequest) int {
	// Anthropic requires max_tokens >= 1; a client's 0/negative would 400.
	if req.MaxCompletionTokens != nil && *req.MaxCompletionTokens > 0 {
		return *req.MaxCompletionTokens
	}
	if req.MaxTokens != nil && *req.MaxTokens > 0 {
		return *req.MaxTokens
	}
	return defaultMaxTokens
}

func stopSequences(raw json.RawMessage) []string {
	var arr []string
	if json.Unmarshal(raw, &arr) == nil {
		return arr
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return []string{s}
	}
	return nil
}

// ---- Anthropic Messages response -> OpenAI Chat Completion -----------------

var stopMap = map[string]string{
	"end_turn":      "stop",
	"max_tokens":    "length",
	"stop_sequence": "stop",
	"tool_use":      "tool_calls",
}

// anthropicToOpenAI converts a full Anthropic message JSON into an OpenAI
// chat.completion object.
func anthropicToOpenAI(raw []byte, model string) map[string]any {
	var m struct {
		Content []struct {
			Type  string          `json:"type"`
			Text  string          `json:"text"`
			ID    string          `json:"id"`
			Name  string          `json:"name"`
			Input json.RawMessage `json:"input"`
		} `json:"content"`
		StopReason string `json:"stop_reason"`
		Usage      struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
	}
	_ = json.Unmarshal(raw, &m)

	var text strings.Builder
	var toolCalls []any
	for _, b := range m.Content {
		switch b.Type {
		case "text":
			text.WriteString(b.Text)
		case "tool_use":
			args := "{}"
			if len(b.Input) > 0 {
				args = string(b.Input)
			}
			toolCalls = append(toolCalls, map[string]any{
				"id": b.ID, "type": "function",
				"function": map[string]any{"name": b.Name, "arguments": args},
			})
		}
	}

	finish := "stop"
	if f, ok := stopMap[m.StopReason]; ok {
		finish = f
	}
	message := map[string]any{"role": "assistant"}
	if text.Len() > 0 {
		message["content"] = text.String()
	} else {
		message["content"] = nil
	}
	if len(toolCalls) > 0 {
		message["tool_calls"] = toolCalls
	}

	return map[string]any{
		"id": idgen.OpenAI("chatcmpl"), "object": "chat.completion",
		"created": idgen.NowSeconds(), "model": model,
		"choices": []any{map[string]any{"index": 0, "message": message, "finish_reason": finish}},
		"usage": map[string]any{
			"prompt_tokens": m.Usage.InputTokens, "completion_tokens": m.Usage.OutputTokens,
			"total_tokens": m.Usage.InputTokens + m.Usage.OutputTokens,
		},
	}
}

// ---- Anthropic SSE -> OpenAI chat.completion.chunk SSE ---------------------

// StreamMessagesToOpenAI translates an Anthropic Messages SSE stream into OpenAI
// chat.completion.chunk frames, writing to w and finishing with [DONE].
func StreamMessagesToOpenAI(w io.Writer, upstream io.Reader, model string) {
	id := idgen.OpenAI("chatcmpl")
	created := idgen.NowSeconds()
	toolIndexByBlock := map[int]int{}
	nextTool := 0
	sentRole := false

	chunk := func(delta map[string]any, finish any) {
		frame := map[string]any{
			"id": id, "object": "chat.completion.chunk", "created": created, "model": model,
			"choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": finish}},
		}
		b, _ := json.Marshal(frame)
		_, _ = w.Write(sse.DataFrame(string(b)))
	}

	for ev := range sse.Frames(upstream) {
		if ev.Data == "" || ev.Data == "[DONE]" {
			continue
		}
		var evt map[string]any
		if json.Unmarshal([]byte(ev.Data), &evt) != nil {
			continue
		}
		switch str(evt["type"]) {
		case "message_start":
			if !sentRole {
				sentRole = true
				chunk(map[string]any{"role": "assistant", "content": ""}, nil)
			}
		case "content_block_start":
			block, _ := evt["content_block"].(map[string]any)
			index := intOf(evt["index"])
			if block != nil && str(block["type"]) == "tool_use" {
				tIdx := nextTool
				nextTool++
				toolIndexByBlock[index] = tIdx
				chunk(map[string]any{"tool_calls": []any{map[string]any{
					"index": tIdx, "id": str(block["id"]), "type": "function",
					"function": map[string]any{"name": str(block["name"]), "arguments": ""},
				}}}, nil)
			}
		case "content_block_delta":
			index := intOf(evt["index"])
			d, _ := evt["delta"].(map[string]any)
			switch str(d["type"]) {
			case "text_delta":
				chunk(map[string]any{"content": str(d["text"])}, nil)
			case "input_json_delta":
				tIdx, ok := toolIndexByBlock[index]
				if !ok {
					continue
				}
				chunk(map[string]any{"tool_calls": []any{map[string]any{
					"index": tIdx, "function": map[string]any{"arguments": str(d["partial_json"])},
				}}}, nil)
			}
		case "message_delta":
			d, _ := evt["delta"].(map[string]any)
			if stop := str(d["stop_reason"]); stop != "" {
				finish := "stop"
				if f, ok := stopMap[stop]; ok {
					finish = f
				}
				chunk(map[string]any{}, finish)
			}
		case "message_stop":
			_, _ = w.Write(sse.Done)
			return
		}
	}
	_, _ = w.Write(sse.Done)
}

// ---- helpers ---------------------------------------------------------------

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
