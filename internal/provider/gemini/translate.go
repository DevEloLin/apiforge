package gemini

import (
	"encoding/json"
	"io"
	"strings"

	"apiforge/internal/util/idgen"
	"apiforge/internal/util/sse"
)

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

var finishMap = map[string]string{
	"STOP":       "stop",
	"MAX_TOKENS": "length",
	"SAFETY":     "content_filter",
	"RECITATION": "content_filter",
}

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
		if p.Type == "text" {
			b.WriteString(p.Text)
		}
	}
	return b.String()
}

func imagePart(url string) map[string]any {
	if strings.HasPrefix(url, "data:") {
		meta, data, _ := strings.Cut(url, ",")
		mime := "image/png"
		if len(meta) > 5 {
			mime = strings.SplitN(meta[5:], ";", 2)[0]
		}
		return map[string]any{"inlineData": map[string]any{"mimeType": mime, "data": data}}
	}
	ext := url
	if i := strings.IndexByte(ext, '?'); i >= 0 {
		ext = ext[:i]
	}
	if i := strings.LastIndexByte(ext, '.'); i >= 0 {
		ext = strings.ToLower(ext[i+1:])
	}
	mime := "image/png"
	switch ext {
	case "jpg", "jpeg":
		mime = "image/jpeg"
	case "webp":
		mime = "image/webp"
	}
	return map[string]any{"fileData": map[string]any{"fileUri": url, "mimeType": mime}}
}

func contentParts(raw json.RawMessage) []any {
	if len(raw) == 0 {
		return []any{map[string]any{"text": ""}}
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return []any{map[string]any{"text": s}}
	}
	var parts []contentPart
	if json.Unmarshal(raw, &parts) != nil {
		return []any{map[string]any{"text": ""}}
	}
	out := make([]any, 0, len(parts))
	for _, p := range parts {
		if p.Type == "text" {
			out = append(out, map[string]any{"text": p.Text})
		} else {
			out = append(out, imagePart(p.ImageURL.URL))
		}
	}
	return out
}

func safeJSON(s string) any {
	var v any
	if json.Unmarshal([]byte(s), &v) == nil {
		return v
	}
	return map[string]any{}
}

type content struct {
	role  string
	parts []any
}

// openaiToGeminiRequest builds the inner Code Assist `request` object.
func openaiToGeminiRequest(req chatRequest) map[string]any {
	var contents []content
	var systemTexts []string
	toolNames := map[string]string{}

	push := func(role string, parts []any) {
		if n := len(contents); n > 0 && contents[n-1].role == role {
			contents[n-1].parts = append(contents[n-1].parts, parts...)
			return
		}
		contents = append(contents, content{role: role, parts: parts})
	}

	for _, m := range req.Messages {
		switch m.Role {
		case "system", "developer":
			if t := extractText(m.Content); t != "" {
				systemTexts = append(systemTexts, t)
			}
		case "tool":
			name := "tool"
			if n, ok := toolNames[m.ToolCallID]; ok {
				name = n
			}
			push("user", []any{map[string]any{"functionResponse": map[string]any{
				"name": name, "response": map[string]any{"content": extractText(m.Content)},
			}}})
		case "assistant":
			var p []any
			if t := extractText(m.Content); t != "" {
				p = append(p, map[string]any{"text": t})
			}
			for _, call := range m.ToolCalls {
				toolNames[call.ID] = call.Function.Name
				p = append(p, map[string]any{"functionCall": map[string]any{
					"name": call.Function.Name, "args": safeJSON(call.Function.Arguments),
				}})
			}
			if len(p) == 0 {
				p = []any{map[string]any{"text": ""}}
			}
			push("model", p)
		default:
			push("user", contentParts(m.Content))
		}
	}

	genConfig := map[string]any{}
	if req.Temperature != nil {
		genConfig["temperature"] = *req.Temperature
	}
	if req.TopP != nil {
		genConfig["topP"] = *req.TopP
	}
	if mt := maxTokens(req); mt > 0 {
		genConfig["maxOutputTokens"] = mt
	}
	if seqs := stopSequences(req.Stop); len(seqs) > 0 {
		genConfig["stopSequences"] = seqs // omit when "stop" is null/empty (avoids upstream 400)
	}

	request := map[string]any{"contents": contentsToAny(contents), "generationConfig": genConfig}
	if len(systemTexts) > 0 {
		request["systemInstruction"] = map[string]any{"parts": []any{map[string]any{"text": strings.Join(systemTexts, "\n\n")}}}
	}
	if len(req.Tools) > 0 {
		decls := make([]any, 0, len(req.Tools))
		for _, t := range req.Tools {
			schema := json.RawMessage(t.Function.Parameters)
			if len(schema) == 0 {
				schema = json.RawMessage(`{"type":"object","properties":{}}`)
			}
			decls = append(decls, map[string]any{
				"name": t.Function.Name, "description": t.Function.Description, "parameters": schema,
			})
		}
		request["tools"] = []any{map[string]any{"functionDeclarations": decls}}
	}
	return request
}

func contentsToAny(contents []content) []any {
	out := make([]any, len(contents))
	for i, c := range contents {
		out[i] = map[string]any{"role": c.role, "parts": c.parts}
	}
	return out
}

func maxTokens(req chatRequest) int {
	if req.MaxCompletionTokens != nil {
		return *req.MaxCompletionTokens
	}
	if req.MaxTokens != nil {
		return *req.MaxTokens
	}
	return 0
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

// ---- Gemini response -> OpenAI ---------------------------------------------

type geminiPart struct {
	Text         string `json:"text"`
	FunctionCall *struct {
		Name string          `json:"name"`
		Args json.RawMessage `json:"args"`
	} `json:"functionCall"`
}

type geminiResponse struct {
	Candidates []struct {
		Content struct {
			Parts []geminiPart `json:"parts"`
		} `json:"content"`
		FinishReason string `json:"finishReason"`
	} `json:"candidates"`
	UsageMetadata struct {
		PromptTokenCount     int `json:"promptTokenCount"`
		CandidatesTokenCount int `json:"candidatesTokenCount"`
		TotalTokenCount      int `json:"totalTokenCount"`
	} `json:"usageMetadata"`
}

// unwrapCodeAssist unwraps the Code Assist `.response` envelope, returning the
// inner Gemini response bytes (or the input unchanged if not wrapped).
func unwrapCodeAssist(raw []byte) []byte {
	var probe struct {
		Response json.RawMessage `json:"response"`
	}
	if json.Unmarshal(raw, &probe) == nil && len(probe.Response) > 0 {
		return probe.Response
	}
	return raw
}

func geminiToOpenAI(raw []byte, model string) map[string]any {
	var resp geminiResponse
	_ = json.Unmarshal(unwrapCodeAssist(raw), &resp)

	var text strings.Builder
	var toolCalls []any
	finishReason := "STOP"
	if len(resp.Candidates) > 0 {
		cand := resp.Candidates[0]
		if cand.FinishReason != "" {
			finishReason = cand.FinishReason
		}
		for _, part := range cand.Content.Parts {
			if part.Text != "" {
				text.WriteString(part.Text)
			} else if part.FunctionCall != nil {
				args := "{}"
				if len(part.FunctionCall.Args) > 0 {
					args = string(part.FunctionCall.Args)
				}
				toolCalls = append(toolCalls, map[string]any{
					"id": idgen.OpenAI("call"), "type": "function",
					"function": map[string]any{"name": part.FunctionCall.Name, "arguments": args},
				})
			}
		}
	}

	finish := "stop"
	if len(toolCalls) > 0 {
		finish = "tool_calls"
	} else if f, ok := finishMap[finishReason]; ok {
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
			"prompt_tokens":     resp.UsageMetadata.PromptTokenCount,
			"completion_tokens": resp.UsageMetadata.CandidatesTokenCount,
			"total_tokens":      resp.UsageMetadata.TotalTokenCount,
		},
	}
}

// StreamToOpenAI translates a Gemini streamGenerateContent SSE stream into
// OpenAI chat.completion.chunk frames.
func StreamToOpenAI(w io.Writer, upstream io.Reader, model string) {
	id := idgen.OpenAI("chatcmpl")
	created := idgen.NowSeconds()
	toolIndex := 0
	finish := "stop"

	chunk := func(delta map[string]any, fin any) {
		frame := map[string]any{
			"id": id, "object": "chat.completion.chunk", "created": created, "model": model,
			"choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": fin}},
		}
		b, _ := json.Marshal(frame)
		_, _ = w.Write(sse.DataFrame(string(b)))
	}

	chunk(map[string]any{"role": "assistant", "content": ""}, nil)

	for ev := range sse.Frames(upstream) {
		if ev.Data == "" || ev.Data == "[DONE]" {
			continue
		}
		var resp geminiResponse
		if json.Unmarshal(unwrapCodeAssist([]byte(ev.Data)), &resp) != nil || len(resp.Candidates) == 0 {
			continue
		}
		cand := resp.Candidates[0]
		for _, part := range cand.Content.Parts {
			if part.Text != "" {
				chunk(map[string]any{"content": part.Text}, nil)
			} else if part.FunctionCall != nil {
				args := "{}"
				if len(part.FunctionCall.Args) > 0 {
					args = string(part.FunctionCall.Args)
				}
				chunk(map[string]any{"tool_calls": []any{map[string]any{
					"index": toolIndex, "id": idgen.OpenAI("call"), "type": "function",
					"function": map[string]any{"name": part.FunctionCall.Name, "arguments": args},
				}}}, nil)
				toolIndex++
				finish = "tool_calls"
			}
		}
		if cand.FinishReason != "" {
			if f, ok := finishMap[cand.FinishReason]; ok {
				finish = f
			}
		}
	}
	chunk(map[string]any{}, finish)
	_, _ = w.Write(sse.Done)
}
