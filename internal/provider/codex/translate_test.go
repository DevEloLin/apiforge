package codex

import (
	"encoding/json"
	"strings"
	"testing"

	"apiforge/internal/types"
)

// sse builds a Codex-style SSE stream from a list of event objects.
func sseStream(events ...map[string]any) string {
	var b strings.Builder
	for _, e := range events {
		raw, _ := json.Marshal(e)
		b.WriteString("data: ")
		b.Write(raw)
		b.WriteString("\n\n")
	}
	b.WriteString("data: [DONE]\n\n")
	return b.String()
}

func TestOpenAIToCodex_SystemAndUser(t *testing.T) {
	// Arrange
	req := chatRequest{
		Model: "gpt-5.5",
		Messages: []chatMessage{
			{Role: "system", Content: json.RawMessage(`"be terse"`)},
			{Role: "user", Content: json.RawMessage(`"hello"`)},
		},
	}

	// Act
	body := openaiToCodex(req, "sess-1")

	// Assert
	if body["instructions"] != "be terse" {
		t.Fatalf("instructions = %v, want 'be terse'", body["instructions"])
	}
	if body["stream"] != true || body["store"] != false {
		t.Fatalf("expected stream=true store=false, got %v/%v", body["stream"], body["store"])
	}
	if body["prompt_cache_key"] != "sess-1" {
		t.Fatalf("prompt_cache_key = %v", body["prompt_cache_key"])
	}
	input := body["input"].([]any)
	if len(input) != 1 {
		t.Fatalf("input len = %d, want 1 (system goes to instructions)", len(input))
	}
	msg := input[0].(map[string]any)
	if msg["role"] != "user" {
		t.Fatalf("input[0] role = %v", msg["role"])
	}
}

func TestOpenAIToCodex_ToolsAndReasoning(t *testing.T) {
	req := chatRequest{
		Model:           "gpt-5.5",
		ReasoningEffort: "high",
		Messages:        []chatMessage{{Role: "user", Content: json.RawMessage(`"go"`)}},
		Tools: []chatTool{{Type: "function", Function: struct {
			Name        string          `json:"name"`
			Description string          `json:"description"`
			Parameters  json.RawMessage `json:"parameters"`
		}{Name: "get_weather", Description: "d"}}},
	}

	body := openaiToCodex(req, "s")

	if _, ok := body["tools"]; !ok {
		t.Fatal("tools missing")
	}
	if body["tool_choice"] != "auto" {
		t.Fatalf("tool_choice = %v", body["tool_choice"])
	}
	if body["parallel_tool_calls"] != false {
		t.Fatal("parallel_tool_calls should be false")
	}
	reasoning, ok := body["reasoning"].(map[string]any)
	if !ok || reasoning["effort"] != "high" {
		t.Fatalf("reasoning = %v", body["reasoning"])
	}
	inc := body["include"].([]any)
	if len(inc) != 1 || inc[0] != "reasoning.encrypted_content" {
		t.Fatalf("include = %v", inc)
	}
}

func TestAggregateChatCompletion_TextAndUsage(t *testing.T) {
	stream := sseStream(
		map[string]any{"type": "response.output_text.delta", "delta": "Hel"},
		map[string]any{"type": "response.output_text.delta", "delta": "lo"},
		map[string]any{"type": "response.completed", "response": map[string]any{
			"usage": map[string]any{"input_tokens": 5, "output_tokens": 2},
		}},
	)

	out, ok := AggregateChatCompletion(strings.NewReader(stream), "gpt-5.5")
	if !ok {
		t.Fatal("expected ok=true for a completed stream")
	}

	choices := out["choices"].([]any)
	msg := choices[0].(map[string]any)["message"].(map[string]any)
	if msg["content"] != "Hello" {
		t.Fatalf("content = %v, want Hello", msg["content"])
	}
	if choices[0].(map[string]any)["finish_reason"] != "stop" {
		t.Fatalf("finish = %v", choices[0].(map[string]any)["finish_reason"])
	}
	usage := out["usage"].(map[string]any)
	if usage["prompt_tokens"] != 5 || usage["completion_tokens"] != 2 || usage["total_tokens"] != 7 {
		t.Fatalf("usage = %v", usage)
	}
}

func TestAggregateChatCompletion_ToolCall(t *testing.T) {
	stream := sseStream(
		map[string]any{"type": "response.output_item.added", "item": map[string]any{
			"type": "function_call", "call_id": "c1", "name": "get_weather",
		}},
		map[string]any{"type": "response.function_call_arguments.delta", "call_id": "c1", "delta": `{"city":`},
		map[string]any{"type": "response.function_call_arguments.delta", "call_id": "c1", "delta": `"NYC"}`},
		map[string]any{"type": "response.completed", "response": map[string]any{}},
	)

	out, ok := AggregateChatCompletion(strings.NewReader(stream), "gpt-5.5")
	if !ok {
		t.Fatal("expected ok=true for a completed stream")
	}

	choice := out["choices"].([]any)[0].(map[string]any)
	if choice["finish_reason"] != "tool_calls" {
		t.Fatalf("finish = %v, want tool_calls", choice["finish_reason"])
	}
	calls := choice["message"].(map[string]any)["tool_calls"].([]any)
	fn := calls[0].(map[string]any)["function"].(map[string]any)
	if fn["name"] != "get_weather" || fn["arguments"] != `{"city":"NYC"}` {
		t.Fatalf("tool call = %v", fn)
	}
}

func TestAggregateChatCompletion_UpstreamFailedIsNotOK(t *testing.T) {
	// 200-then-response.failed with no content must NOT become a fake empty 200.
	stream := sseStream(map[string]any{"type": "response.failed",
		"response": map[string]any{"error": map[string]any{"message": "usage cap"}}})
	if _, ok := AggregateChatCompletion(strings.NewReader(stream), "gpt-5.5"); ok {
		t.Fatal("expected ok=false when upstream emits response.failed")
	}
}

func TestAggregateChatCompletion_TruncatedEmptyIsNotOK(t *testing.T) {
	// No completed event and no output → truncated → not ok.
	stream := sseStream(map[string]any{"type": "response.output_text.delta", "delta": ""})
	if _, ok := AggregateChatCompletion(strings.NewReader(stream), "gpt-5.5"); ok {
		t.Fatal("expected ok=false for a truncated empty stream")
	}
}

func TestStreamChatCompletion_EmitsChunksAndDone(t *testing.T) {
	stream := sseStream(
		map[string]any{"type": "response.output_text.delta", "delta": "hi"},
		map[string]any{"type": "response.completed", "response": map[string]any{}},
	)
	var out strings.Builder

	StreamChatCompletion(&out, strings.NewReader(stream), "gpt-5.5")

	s := out.String()
	if !strings.Contains(s, `"role":"assistant"`) {
		t.Fatal("missing initial role frame")
	}
	if !strings.Contains(s, `"content":"hi"`) {
		t.Fatal("missing content delta")
	}
	if !strings.Contains(s, `"finish_reason":"stop"`) {
		t.Fatal("missing finish frame")
	}
	if !strings.HasSuffix(strings.TrimSpace(s), "[DONE]") {
		t.Fatalf("stream must end with [DONE], got tail: %q", s[max(0, len(s)-40):])
	}
}

func TestCollectImage_FinalResult(t *testing.T) {
	stream := sseStream(
		map[string]any{"type": "response.image_generation_call.partial_image", "partial_image_b64": "PARTIAL", "output_format": "webp"},
		map[string]any{"type": "response.output_item.done", "item": map[string]any{
			"type": "image_generation_call", "result": "FINALB64", "output_format": "png", "size": "1024x1024",
		}},
		map[string]any{"type": "response.completed"},
	)

	img := collectImage(strings.NewReader(stream))

	if img == nil || img.b64 != "FINALB64" {
		t.Fatalf("img = %+v, want b64=FINALB64", img)
	}
	if img.outputFormat != "png" || img.size != "1024x1024" {
		t.Fatalf("img meta = %+v", img)
	}
}

func TestCollectImage_PartialOnlyIfCompleted(t *testing.T) {
	// No output_item.done and no completed => truncated => nil.
	stream := sseStream(
		map[string]any{"type": "response.image_generation_call.partial_image", "partial_image_b64": "PARTIAL"},
	)
	if img := collectImage(strings.NewReader(stream)); img != nil {
		t.Fatalf("expected nil for truncated stream, got %+v", img)
	}
}

func TestBuildImageEditBody_WrapsImages(t *testing.T) {
	req := types.ImageRequest{
		Model:  "gpt-image-2",
		Prompt: "make it blue",
		Images: []types.ImageInput{{B64: "AAAA", ContentType: "image/png"}},
	}

	body := buildImageEditBody(req, "s")

	input := body["input"].([]any)
	content := input[0].(map[string]any)["content"].([]any)
	// 3 parts for the image wrap + 1 prompt = 4
	if len(content) != 4 {
		t.Fatalf("content parts = %d, want 4", len(content))
	}
	img := content[1].(map[string]any)
	if img["type"] != "input_image" || img["detail"] != "high" {
		t.Fatalf("image part = %v", img)
	}
}
