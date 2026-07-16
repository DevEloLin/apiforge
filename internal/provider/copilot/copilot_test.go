package copilot

import (
	"bytes"
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

func TestRewriteModel_PreservesLargeInts(t *testing.T) {
	// A big integer field (e.g. seed) must not be corrupted into float64 sci-notation.
	body := []byte(`{"model":"copilot/gpt-4o","seed":9007199254740993,"messages":[]}`)
	out, _ := rewriteModel(body)
	if !bytes.Contains(out, []byte(`9007199254740993`)) {
		t.Fatalf("large int corrupted: %s", out)
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
