package grokweb

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"io"
	"math/big"
	"strings"

	"apiforge/internal/util/idgen"
	"apiforge/internal/util/sse"
)

const (
	newConversationURL = "https://grok.com/rest/app-chat/conversations/new"
	origin             = "https://grok.com"
	// A recent desktop-Chrome UA to match the spoofed sec-ch-ua headers.
	userAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/133.0.0.0 Safari/537.36"
)

// modelSpec maps a public model id to Grok's internal (modelName, modelMode).
type modelSpec struct {
	grokModel string
	modelMode string
}

// modelRegistry mirrors grok.com's web model modes (recovered from the client).
var modelRegistry = map[string]modelSpec{
	"grok-3":            {"grok-3", "MODEL_MODE_GROK_3"},
	"grok-3-mini":       {"grok-3", "MODEL_MODE_GROK_3_MINI_THINKING"},
	"grok-4.1-thinking": {"grok-4-1-thinking-1129", "MODEL_MODE_GROK_4_1_THINKING"},
	"grok-4.2-fast":     {"grok-420", "MODEL_MODE_FAST"},
	"grok-4.2":          {"grok-420", "MODEL_MODE_GROK_420"},
	"grok-expert":       {"grok-420", "MODEL_MODE_EXPERT"},
}

// modelIDs is the advertised, deterministic model order.
var modelIDs = []string{"grok-4.2", "grok-4.2-fast", "grok-expert", "grok-4.1-thinking", "grok-3", "grok-3-mini"}

func resolveModel(id string) modelSpec {
	if s, ok := modelRegistry[id]; ok {
		return s
	}
	return modelSpec{grokModel: id, modelMode: "MODEL_MODE_AUTO"} // unknown → passthrough
}

// message is one OpenAI chat turn (only what we fold into Grok's single string).
type message struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

// foldMessages collapses the OpenAI messages into Grok's single `message` string,
// prefixing each turn with a [Role] label (Grok's web app has no role array here).
func foldMessages(msgs []message) string {
	if len(msgs) == 1 {
		return extractText(msgs[0].Content)
	}
	var parts []string
	for _, m := range msgs {
		label := "[User]"
		switch m.Role {
		case "system":
			label = "[System]"
		case "assistant":
			label = "[Assistant]"
		}
		parts = append(parts, label+"\n"+extractText(m.Content))
	}
	return strings.Join(parts, "\n\n")
}

// buildPayload constructs the conversations/new request body.
func buildPayload(msgText string, spec modelSpec) map[string]any {
	isThinkHarder := strings.Contains(spec.modelMode, "THINKING") || strings.Contains(spec.modelMode, "EXPERT")
	return map[string]any{
		"temporary":                 true,
		"modelName":                 spec.grokModel,
		"message":                   msgText,
		"fileAttachments":           []any{},
		"imageAttachments":          []any{},
		"disableSearch":             false,
		"enableImageGeneration":     true,
		"returnImageBytes":          false,
		"returnRawGrokInXaiRequest": false,
		"enableImageStreaming":      true,
		"imageGenerationCount":      2,
		"forceConcise":              false,
		"toolOverrides":             map[string]any{},
		"enableSideBySide":          true,
		"sendFinalMetadata":         true,
		"isReasoning":               false,
		"webpageUrls":               []any{},
		"disableTextFollowUps":      false,
		"responseMetadata": map[string]any{
			"is_think_harder":     isThinkHarder,
			"is_quick_answer":     false,
			"requestModelDetails": map[string]any{"modelId": spec.grokModel},
		},
		"disableMemory":               false,
		"forceSideBySide":             false,
		"modelMode":                   spec.modelMode,
		"isAsyncChat":                 false,
		"disableSelfHarmShortCircuit": false,
	}
}

// headers builds the browser-mimicking request headers, including the spoofed
// x-statsig-id (a base64 fake-error string — the web client's anti-bot token,
// which the server does not cryptographically validate).
func headers(cookie string) map[string]string {
	return map[string]string{
		"Accept":             "*/*",
		"Accept-Language":    "en-US,en;q=0.9",
		"Origin":             origin,
		"Referer":            origin + "/",
		"Priority":           "u=1, i",
		"User-Agent":         userAgent,
		"Sec-Ch-Ua":          `"Not(A:Brand";v="99", "Google Chrome";v="133", "Chromium";v="133"`,
		"Sec-Ch-Ua-Mobile":   "?0",
		"Sec-Ch-Ua-Platform": `"Windows"`,
		"Sec-Fetch-Dest":     "empty",
		"Sec-Fetch-Mode":     "cors",
		"Sec-Fetch-Site":     "same-origin",
		"Baggage":            "sentry-environment=production,sentry-public_key=b311e0f2690c81f25e2c4cf6d4f7ce1c",
		"x-statsig-id":       generateStatsigID(),
		"x-xai-request-id":   idgen.UUID(),
		"Content-Type":       "application/json",
		"Cookie":             cookie,
	}
}

// cookieHeader accepts either a bare sso token or a full `k=v; k=v` cookie string
// (so users can add cf_clearance etc. when Cloudflare challenges).
func cookieHeader(token string) string {
	if strings.Contains(token, "=") {
		return token
	}
	return "sso=" + token
}

func generateStatsigID() string {
	if randBool() {
		msg := "e:TypeError: Cannot read properties of null (reading 'children['" + randString(5) + "']')"
		return base64.StdEncoding.EncodeToString([]byte(msg))
	}
	msg := "e:TypeError: Cannot read properties of undefined (reading '" + randString(10) + "')"
	return base64.StdEncoding.EncodeToString([]byte(msg))
}

const lowerAlnum = "abcdefghijklmnopqrstuvwxyz0123456789"

func randString(n int) string {
	b := make([]byte, n)
	for i := range b {
		idx, _ := rand.Int(rand.Reader, big.NewInt(int64(len(lowerAlnum))))
		b[i] = lowerAlnum[idx.Int64()]
	}
	return string(b)
}

func randBool() bool {
	n, _ := rand.Int(rand.Reader, big.NewInt(2))
	return n.Int64() == 1
}

// streamToOpenAI parses grok.com's NDJSON stream (one JSON object per line;
// text deltas live at result.token) and writes OpenAI chat.completion.chunk
// frames to w, finishing with [DONE].
func streamToOpenAI(w io.Writer, upstream io.Reader, model string) {
	id := idgen.OpenAI("chatcmpl")
	created := idgen.NowSeconds()
	chunk := func(delta map[string]any, finish any) {
		frame := map[string]any{
			"id": id, "object": "chat.completion.chunk", "created": created, "model": model,
			"choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": finish}},
		}
		b, _ := json.Marshal(frame)
		_, _ = w.Write(sse.DataFrame(string(b)))
	}
	chunk(map[string]any{"role": "assistant", "content": ""}, nil)
	forEachToken(upstream, func(text string) {
		chunk(map[string]any{"content": text}, nil)
	})
	chunk(map[string]any{}, "stop")
	_, _ = w.Write(sse.Done)
}

// aggregate collects the full assistant text for a non-streaming response.
func aggregate(upstream io.Reader, model string) map[string]any {
	var b strings.Builder
	forEachToken(upstream, func(text string) { b.WriteString(text) })
	var content any
	if b.Len() > 0 {
		content = b.String()
	}
	return map[string]any{
		"id": idgen.OpenAI("chatcmpl"), "object": "chat.completion",
		"created": idgen.NowSeconds(), "model": model,
		"choices": []any{map[string]any{
			"index": 0, "message": map[string]any{"role": "assistant", "content": content}, "finish_reason": "stop",
		}},
	}
}

// forEachToken invokes fn for each incremental text delta in the NDJSON stream.
func forEachToken(upstream io.Reader, fn func(string)) {
	for line := range sse.Lines(upstream) {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var obj struct {
			Result struct {
				Token string `json:"token"`
			} `json:"result"`
		}
		if json.Unmarshal([]byte(line), &obj) != nil {
			continue
		}
		if obj.Result.Token != "" {
			fn(obj.Result.Token)
		}
	}
}

// extractText concatenates text parts of an OpenAI content field.
func extractText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
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
