package copilot

import (
	"encoding/json"
	"testing"
)

func TestRewriteModel_StripsPrefixAndReportsStream(t *testing.T) {
	body := []byte(`{"model":"copilot/gpt-4o","stream":true,"messages":[]}`)

	out, stream := rewriteModel(body)

	if !stream {
		t.Fatal("stream should be true")
	}
	var m map[string]any
	if json.Unmarshal(out, &m) != nil {
		t.Fatal("output not valid JSON")
	}
	if m["model"] != "gpt-4o" {
		t.Fatalf("model = %v, want gpt-4o (prefix stripped)", m["model"])
	}
}

func TestRewriteModel_NoPrefixNoStream(t *testing.T) {
	body := []byte(`{"model":"gpt-4o","messages":[]}`)
	out, stream := rewriteModel(body)
	if stream {
		t.Fatal("stream should default false")
	}
	var m map[string]any
	_ = json.Unmarshal(out, &m)
	if m["model"] != "gpt-4o" {
		t.Fatalf("model = %v", m["model"])
	}
}
