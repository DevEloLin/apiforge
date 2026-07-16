package grokweb

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
)

func TestFoldMessages_SingleVsMulti(t *testing.T) {
	single := foldMessages([]message{{Role: "user", Content: json.RawMessage(`"hi"`)}})
	if single != "hi" {
		t.Fatalf("single = %q, want 'hi'", single)
	}
	multi := foldMessages([]message{
		{Role: "system", Content: json.RawMessage(`"be terse"`)},
		{Role: "user", Content: json.RawMessage(`"hello"`)},
	})
	if multi != "[System]\nbe terse\n\n[User]\nhello" {
		t.Fatalf("multi = %q", multi)
	}
}

func TestResolveModel(t *testing.T) {
	if s := resolveModel("grok-4.2"); s.grokModel != "grok-420" || s.modelMode != "MODEL_MODE_GROK_420" {
		t.Fatalf("grok-4.2 = %+v", s)
	}
	if s := resolveModel("grok-expert"); s.modelMode != "MODEL_MODE_EXPERT" {
		t.Fatalf("grok-expert mode = %q", s.modelMode)
	}
	if s := resolveModel("unknown-x"); s.modelMode != "MODEL_MODE_AUTO" || s.grokModel != "unknown-x" {
		t.Fatalf("unknown passthrough = %+v", s)
	}
}

func TestBuildPayload_ThinkHarder(t *testing.T) {
	p := buildPayload("hi", resolveModel("grok-expert"))
	if p["modelName"] != "grok-420" || p["modelMode"] != "MODEL_MODE_EXPERT" {
		t.Fatalf("payload model = %v/%v", p["modelName"], p["modelMode"])
	}
	rm := p["responseMetadata"].(map[string]any)
	if rm["is_think_harder"] != true {
		t.Fatal("expert mode should set is_think_harder=true")
	}
	// grok-3 (non-thinking) → false
	p2 := buildPayload("hi", resolveModel("grok-3"))
	if p2["responseMetadata"].(map[string]any)["is_think_harder"] != false {
		t.Fatal("grok-3 should set is_think_harder=false")
	}
}

func TestCookieHeader(t *testing.T) {
	if got := cookieHeader("abc123"); got != "sso=abc123" {
		t.Fatalf("bare token = %q", got)
	}
	full := "sso=abc; cf_clearance=xyz"
	if got := cookieHeader(full); got != full {
		t.Fatalf("full cookie = %q", got)
	}
}

func TestGenerateStatsigID_DecodesToFakeError(t *testing.T) {
	id := generateStatsigID()
	raw, err := base64.StdEncoding.DecodeString(id)
	if err != nil {
		t.Fatalf("statsig id not valid base64: %v", err)
	}
	if !strings.HasPrefix(string(raw), "e:TypeError:") {
		t.Fatalf("decoded statsig = %q, want fake-error prefix", string(raw))
	}
}

func TestForEachToken_NDJSON(t *testing.T) {
	// grok.com streams one JSON object per line; text at result.token.
	stream := strings.Join([]string{
		`{"result":{"conversation":{"conversationId":"c1"}}}`,
		`{"result":{"token":"Hello"}}`,
		`{"result":{"token":", world"}}`,
		`{"result":{"response":{"responseId":"r1"}}}`,
		``,
	}, "\n")

	var got strings.Builder
	forEachToken(strings.NewReader(stream), func(s string) { got.WriteString(s) })

	if got.String() != "Hello, world" {
		t.Fatalf("forEachToken = %q, want 'Hello, world'", got.String())
	}
}
