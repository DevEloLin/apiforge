// Package cursor reuses a Cursor session token against Cursor's reverse-engineered
// AiService (Connect-RPC + protobuf). EXPERIMENTAL. The session token is provided
// via CURSOR_ACCESS_TOKEN(S) — on a headless host the editor's state.vscdb is
// absent, so the token is copied from a desktop Cursor install (see README).
package cursor

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"apiforge/internal/pool"
	"apiforge/internal/relay"
	"apiforge/internal/types"
	"apiforge/internal/util/httpx"
	"apiforge/internal/util/idgen"
	"apiforge/internal/util/sse"
)

const (
	upstreamURL   = "https://api2.cursor.sh/aiserver.v1.AiService/StreamUnifiedChatWithTools"
	clientVersion = "0.48.0"
)

var defaultModels = []string{
	"claude-4.5-sonnet", "claude-4-opus", "gpt-5.1", "o3",
	"gemini-3-pro", "grok-4", "deepseek-v3.1", "composer-1", "default",
}

// Config carries pool tuning from the app config.
type Config struct {
	MaxConcurrency int
	StickyTTL      time.Duration
}

// Provider serves OpenAI Chat Completions over Cursor's AiService.
type Provider struct {
	ownedBy string
	models  []string
	pool    *pool.Pool[string]
	ready   atomic.Bool
	log     *slog.Logger
}

// New builds the cursor provider from session tokens. Returns nil when none.
func New(tokens []string, cfg Config, log *slog.Logger) *Provider {
	var accounts []pool.Account[string]
	for i, t := range tokens {
		tok := normalizeToken(t)
		if tok == "" {
			continue
		}
		accounts = append(accounts, pool.Account[string]{ID: "cursor#" + strconv.Itoa(i+1), Cred: tok})
	}
	if len(accounts) == 0 {
		return nil
	}
	models := defaultModels
	prefixed := make([]string, len(models))
	for i, m := range models {
		prefixed[i] = "cursor/" + m
	}
	return &Provider{
		ownedBy: "cursor",
		models:  prefixed,
		pool: pool.New(accounts, pool.Options{
			Strategy: pool.Failover, MaxConcurrency: cfg.MaxConcurrency, StickyTTL: cfg.StickyTTL,
		}, log),
		log: log,
	}
}

func (p *Provider) ID() string                       { return "cursor" }
func (p *Provider) Capabilities() []types.Capability { return nil }
func (p *Provider) IsReady() bool                    { return p.ready.Load() }
func (p *Provider) ListModels() []types.ModelObject  { return types.ModelObjects(p.models, p.ownedBy) }
func (p *Provider) Pool() *pool.Pool[string]         { return p.pool }
func (p *Provider) AccountPool() pool.Admin          { return p.pool }
func (p *Provider) OwnsModel(model string) bool      { return strings.HasPrefix(model, "cursor/") }

func (p *Provider) Init(_ context.Context) error {
	p.ready.Store(true)
	if p.log != nil {
		p.log.Warn("cursor provider is EXPERIMENTAL (reverse-engineered protobuf API)")
	}
	return nil
}

// ---- request types ---------------------------------------------------------

type chatRequest struct {
	Model    string        `json:"model"`
	Stream   bool          `json:"stream"`
	Messages []chatMessage `json:"messages"`
}

type chatMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

func (p *Provider) ChatCompletion(rctx types.RequestContext, body []byte) (*http.Response, error) {
	var req chatRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return relay.SynthStatus(http.StatusBadRequest, "invalid request body"), nil
	}
	model := strings.TrimPrefix(req.Model, "cursor/")
	messages := buildMessages(req.Messages)

	return relay.WithAccountRetry(rctx.Ctx, p.pool, rctx.AccountPin, rctx.Session,
		func(acc pool.Account[string]) (*http.Response, error) {
			payload := frame(encodeChatRequest(messages, model, idgen.UUID()), 0x00)
			hreq, err := http.NewRequestWithContext(rctx.Ctx, http.MethodPost, upstreamURL, strings.NewReader(string(payload)))
			if err != nil {
				return nil, err
			}
			hreq.Header.Set("Authorization", "Bearer "+acc.Cred)
			hreq.Header.Set("Content-Type", "application/connect+proto")
			hreq.Header.Set("connect-protocol-version", "1")
			hreq.Header.Set("x-cursor-checksum", buildChecksum())
			hreq.Header.Set("x-cursor-client-version", clientVersion)
			hreq.Header.Set("x-cursor-timezone", "UTC")
			hreq.Header.Set("User-Agent", "connect-es/1.6.1")
			upstream, err := httpx.Client.Do(hreq)
			if err != nil {
				return nil, err
			}
			if upstream.StatusCode >= 300 {
				return upstream, nil
			}
			if req.Stream {
				return relay.StreamingResponse(func(w io.Writer) {
					defer upstream.Body.Close()
					streamToOpenAI(w, upstream.Body, req.Model)
				}), nil
			}
			defer upstream.Body.Close()
			return relay.JSONResponse(aggregate(upstream.Body, req.Model)), nil
		})
}

// buildMessages folds system/developer text into the first user turn (Cursor has
// no system role on this endpoint).
func buildMessages(in []chatMessage) []CursorMessage {
	var systemParts []string
	for _, m := range in {
		if m.Role == "system" || m.Role == "developer" {
			if t := extractText(m.Content); t != "" {
				systemParts = append(systemParts, t)
			}
		}
	}
	systemText := strings.Join(systemParts, "\n\n")

	var out []CursorMessage
	systemInjected := false
	for _, m := range in {
		if m.Role == "system" || m.Role == "developer" {
			continue
		}
		role := "user"
		if m.Role == "assistant" {
			role = "assistant"
		}
		text := extractText(m.Content)
		if !systemInjected && role == "user" && systemText != "" {
			text = systemText + "\n\n" + text
			systemInjected = true
		}
		out = append(out, CursorMessage{Text: text, Role: role, BubbleID: idgen.UUID()})
	}
	return out
}

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
	streamDeltas(upstream, func(text string) {
		chunk(map[string]any{"content": text}, nil)
	})
	chunk(map[string]any{}, "stop")
	_, _ = w.Write(sse.Done)
}

func aggregate(upstream io.Reader, model string) map[string]any {
	var b strings.Builder
	streamDeltas(upstream, func(text string) { b.WriteString(text) })
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

// extractText concatenates text parts of an OpenAI content field (string or array).
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
