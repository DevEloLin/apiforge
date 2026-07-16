package gemini

import (
	"encoding/json"
	"strings"
	"testing"
)

func sseStream(events ...map[string]any) string {
	var b strings.Builder
	for _, e := range events {
		raw, _ := json.Marshal(e)
		b.WriteString("data: ")
		b.Write(raw)
		b.WriteString("\n\n")
	}
	return b.String()
}

func TestOpenAIToGemini_RolesAndSystem(t *testing.T) {
	req := chatRequest{
		Model: "gemini-2.5-pro",
		Messages: []chatMessage{
			{Role: "system", Content: json.RawMessage(`"sys"`)},
			{Role: "user", Content: json.RawMessage(`"hi"`)},
			{Role: "assistant", Content: json.RawMessage(`"hello"`)},
		},
	}
	req.MaxTokens = ptr(256)

	out := openaiToGeminiRequest(req)

	si := out["systemInstruction"].(map[string]any)["parts"].([]any)
	if si[0].(map[string]any)["text"] != "sys" {
		t.Fatalf("systemInstruction = %v", si)
	}
	contents := out["contents"].([]any)
	if len(contents) != 2 {
		t.Fatalf("contents len = %d, want 2", len(contents))
	}
	if contents[0].(map[string]any)["role"] != "user" || contents[1].(map[string]any)["role"] != "model" {
		t.Fatalf("roles = %v", contents)
	}
	gc := out["generationConfig"].(map[string]any)
	if gc["maxOutputTokens"] != 256 {
		t.Fatalf("maxOutputTokens = %v", gc["maxOutputTokens"])
	}
}

func TestOpenAIToGemini_ToolCallAndResult(t *testing.T) {
	req := chatRequest{
		Model: "gemini-2.5-pro",
		Messages: []chatMessage{
			{Role: "assistant", ToolCalls: []toolCall{{ID: "c1", Function: struct {
				Name      string `json:"name"`
				Arguments string `json:"arguments"`
			}{Name: "lookup", Arguments: `{"q":"x"}`}}}},
			{Role: "tool", ToolCallID: "c1", Content: json.RawMessage(`"result"`)},
		},
	}
	out := openaiToGeminiRequest(req)
	contents := out["contents"].([]any)

	model := contents[0].(map[string]any)
	fc := model["parts"].([]any)[0].(map[string]any)["functionCall"].(map[string]any)
	if fc["name"] != "lookup" {
		t.Fatalf("functionCall = %v", fc)
	}
	user := contents[1].(map[string]any)
	fr := user["parts"].([]any)[0].(map[string]any)["functionResponse"].(map[string]any)
	if fr["name"] != "lookup" {
		t.Fatalf("functionResponse name = %v (should pair with call)", fr["name"])
	}
}

func TestGeminiToOpenAI_UnwrapAndUsage(t *testing.T) {
	// Code Assist wraps the payload in `.response`.
	raw := []byte(`{"response":{
		"candidates":[{"content":{"parts":[{"text":"Hi"}]},"finishReason":"STOP"}],
		"usageMetadata":{"promptTokenCount":4,"candidatesTokenCount":1,"totalTokenCount":5}
	}}`)
	out := geminiToOpenAI(raw, "gemini-2.5-pro")

	choice := out["choices"].([]any)[0].(map[string]any)
	if choice["message"].(map[string]any)["content"] != "Hi" {
		t.Fatalf("content = %v", choice["message"])
	}
	if choice["finish_reason"] != "stop" {
		t.Fatalf("finish = %v", choice["finish_reason"])
	}
	if out["usage"].(map[string]any)["total_tokens"] != 5 {
		t.Fatalf("usage = %v", out["usage"])
	}
}

func TestStreamToOpenAI(t *testing.T) {
	stream := sseStream(
		map[string]any{"response": map[string]any{"candidates": []any{map[string]any{
			"content": map[string]any{"parts": []any{map[string]any{"text": "one "}}},
		}}}},
		map[string]any{"response": map[string]any{"candidates": []any{map[string]any{
			"content":      map[string]any{"parts": []any{map[string]any{"text": "two"}}},
			"finishReason": "STOP",
		}}}},
	)
	var out strings.Builder
	StreamToOpenAI(&out, strings.NewReader(stream), "gemini-2.5-pro")
	s := out.String()

	if !strings.Contains(s, `"content":"one "`) || !strings.Contains(s, `"content":"two"`) {
		t.Fatalf("missing content deltas: %s", s)
	}
	if !strings.HasSuffix(strings.TrimSpace(s), "[DONE]") {
		t.Fatal("must end with [DONE]")
	}
}

func ptr[T any](v T) *T { return &v }
