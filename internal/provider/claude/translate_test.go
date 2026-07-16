package claude

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

func TestEnsureIdentitySystem(t *testing.T) {
	// Arrange / Act / Assert — nil
	got := ensureIdentitySystem(nil)
	if len(got) != 1 || got[0].(map[string]any)["text"] != claudeCodeIdentity {
		t.Fatalf("nil system = %v", got)
	}
	// string gets prepended identity
	got = ensureIdentitySystem("custom rules")
	if len(got) != 2 || got[1].(map[string]any)["text"] != "custom rules" {
		t.Fatalf("string system = %v", got)
	}
	// already-identity string stays single
	got = ensureIdentitySystem(claudeCodeIdentity)
	if len(got) != 1 {
		t.Fatalf("identity string should stay single, got %v", got)
	}
}

func TestOpenAIToAnthropic_SystemUserToolDefault(t *testing.T) {
	req := chatRequest{
		Model: "claude-sonnet-5",
		Messages: []chatMessage{
			{Role: "system", Content: json.RawMessage(`"be nice"`)},
			{Role: "user", Content: json.RawMessage(`"hi"`)},
		},
	}
	out := openaiToAnthropic(req)

	if out["max_tokens"] != defaultMaxTokens {
		t.Fatalf("max_tokens = %v, want default", out["max_tokens"])
	}
	sys := out["system"].([]any)
	if sys[0].(map[string]any)["text"] != "be nice" {
		t.Fatalf("system = %v", sys)
	}
	msgs := out["messages"].([]any)
	if len(msgs) != 1 || msgs[0].(map[string]any)["role"] != "user" {
		t.Fatalf("messages = %v", msgs)
	}
}

func TestOpenAIToAnthropic_AssistantToolCallAndToolResult(t *testing.T) {
	req := chatRequest{
		Model: "claude-sonnet-5",
		Messages: []chatMessage{
			{Role: "user", Content: json.RawMessage(`"weather?"`)},
			{Role: "assistant", ToolCalls: []toolCall{{ID: "t1", Function: struct {
				Name      string `json:"name"`
				Arguments string `json:"arguments"`
			}{Name: "get_weather", Arguments: `{"city":"NYC"}`}}}},
			{Role: "tool", ToolCallID: "t1", Content: json.RawMessage(`"sunny"`)},
		},
	}
	out := openaiToAnthropic(req)
	msgs := out["messages"].([]any)

	// user, assistant(tool_use), user(tool_result)
	if len(msgs) != 3 {
		t.Fatalf("messages len = %d, want 3", len(msgs))
	}
	asst := msgs[1].(map[string]any)["content"].([]any)
	tu := asst[0].(map[string]any)
	if tu["type"] != "tool_use" || tu["name"] != "get_weather" {
		t.Fatalf("tool_use = %v", tu)
	}
	tr := msgs[2].(map[string]any)["content"].([]any)[0].(map[string]any)
	if tr["type"] != "tool_result" || tr["tool_use_id"] != "t1" {
		t.Fatalf("tool_result = %v", tr)
	}
}

func TestOpenAIToAnthropic_AssistantNullContentNoEmptyBlock(t *testing.T) {
	// The standard tool-call turn: content:null + tool_calls. Must NOT produce an
	// empty text block (Anthropic 400s on those) — only the tool_use block.
	req := chatRequest{
		Model: "claude-sonnet-5",
		Messages: []chatMessage{
			{Role: "user", Content: json.RawMessage(`"weather?"`)},
			{Role: "assistant", Content: json.RawMessage(`null`), ToolCalls: []toolCall{{ID: "t1", Function: struct {
				Name      string `json:"name"`
				Arguments string `json:"arguments"`
			}{Name: "get_weather", Arguments: `{}`}}}},
		},
	}
	out := openaiToAnthropic(req)
	asst := out["messages"].([]any)[1].(map[string]any)["content"].([]any)
	if len(asst) != 1 {
		t.Fatalf("assistant content blocks = %d, want 1 (tool_use only, no empty text)", len(asst))
	}
	if asst[0].(map[string]any)["type"] != "tool_use" {
		t.Fatalf("block = %v, want tool_use", asst[0])
	}
}

func TestAnthropicToOpenAI_TextAndTool(t *testing.T) {
	raw := []byte(`{
		"content":[
			{"type":"text","text":"Hello"},
			{"type":"tool_use","id":"tu1","name":"f","input":{"a":1}}
		],
		"stop_reason":"tool_use",
		"usage":{"input_tokens":10,"output_tokens":3}
	}`)
	out := anthropicToOpenAI(raw, "claude-sonnet-5")

	choice := out["choices"].([]any)[0].(map[string]any)
	if choice["finish_reason"] != "tool_calls" {
		t.Fatalf("finish = %v", choice["finish_reason"])
	}
	msg := choice["message"].(map[string]any)
	if msg["content"] != "Hello" {
		t.Fatalf("content = %v", msg["content"])
	}
	calls := msg["tool_calls"].([]any)
	fn := calls[0].(map[string]any)["function"].(map[string]any)
	if fn["name"] != "f" || fn["arguments"] != `{"a":1}` {
		t.Fatalf("tool call = %v", fn)
	}
	usage := out["usage"].(map[string]any)
	if usage["total_tokens"] != 13 {
		t.Fatalf("total_tokens = %v", usage["total_tokens"])
	}
}

func TestStreamMessagesToOpenAI(t *testing.T) {
	stream := sseStream(
		map[string]any{"type": "message_start"},
		map[string]any{"type": "content_block_delta", "index": 0, "delta": map[string]any{"type": "text_delta", "text": "hi"}},
		map[string]any{"type": "message_delta", "delta": map[string]any{"stop_reason": "end_turn"}},
		map[string]any{"type": "message_stop"},
	)
	var out strings.Builder
	StreamMessagesToOpenAI(&out, strings.NewReader(stream), "claude-sonnet-5")
	s := out.String()

	if !strings.Contains(s, `"role":"assistant"`) {
		t.Fatal("missing role frame")
	}
	if !strings.Contains(s, `"content":"hi"`) {
		t.Fatal("missing content delta")
	}
	if !strings.Contains(s, `"finish_reason":"stop"`) {
		t.Fatal("missing finish (end_turn->stop)")
	}
	if !strings.HasSuffix(strings.TrimSpace(s), "[DONE]") {
		t.Fatal("must end with [DONE]")
	}
}

func TestStreamMessagesToOpenAI_ToolUse(t *testing.T) {
	stream := sseStream(
		map[string]any{"type": "message_start"},
		map[string]any{"type": "content_block_start", "index": 0, "content_block": map[string]any{"type": "tool_use", "id": "tu1", "name": "f"}},
		map[string]any{"type": "content_block_delta", "index": 0, "delta": map[string]any{"type": "input_json_delta", "partial_json": `{"a":1}`}},
		map[string]any{"type": "message_stop"},
	)
	var out strings.Builder
	StreamMessagesToOpenAI(&out, strings.NewReader(stream), "claude-sonnet-5")
	s := out.String()

	if !strings.Contains(s, `"name":"f"`) {
		t.Fatal("missing tool name frame")
	}
	if !strings.Contains(s, `"arguments":"{\"a\":1}"`) {
		t.Fatalf("missing tool args frame: %s", s)
	}
}
