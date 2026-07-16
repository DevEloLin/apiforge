// Package claude reuses a Claude Code OAuth login (or an Anthropic API key) as
// an upstream. It serves OpenAI Chat Completions (translated to/from Anthropic
// Messages) and the native Anthropic Messages + count_tokens surfaces.
package claude

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"apiforge/internal/pool"
	"apiforge/internal/relay"
	"apiforge/internal/types"
	"apiforge/internal/util/ssrf"
)

var defaultModels = []string{
	"claude-fable-5", "claude-opus-4-8", "claude-sonnet-5",
	"claude-haiku-4-5", "claude-opus-4-6", "claude-sonnet-4-6",
}

// Config carries pool tuning from the app config.
type Config struct {
	MaxConcurrency int
	StickyTTL      time.Duration
}

// Provider implements types.Provider + types.AnthropicProvider.
type Provider struct {
	ownedBy string
	models  []string
	pool    *pool.Pool[auth]
	ready   bool
	log     *slog.Logger
}

// New builds the claude provider from OAuth credential paths plus ANTHROPIC_API_KEYS.
// Returns nil when no account is configured.
func New(credentialPaths, apiKeys []string, cfg Config, log *slog.Logger) *Provider {
	var accounts []pool.Account[auth]
	n := 0
	for _, p := range credentialPaths {
		n++
		accounts = append(accounts, pool.Account[auth]{
			ID: "claude#" + strconv.Itoa(n), Cred: auth{kind: "oauth", creds: newCreds(p, log)},
		})
	}
	k := 0
	for _, key := range apiKeys {
		k++
		accounts = append(accounts, pool.Account[auth]{
			ID: "claude-key#" + strconv.Itoa(k), Cred: auth{kind: "key", key: key},
		})
	}
	if len(accounts) == 0 {
		return nil
	}
	if err := ssrf.AssertPublicURL(apiBase(), "claude (ANTHROPIC_BASE_URL)"); err != nil {
		if log != nil {
			log.Warn("claude disabled: bad ANTHROPIC_BASE_URL", "err", err)
		}
		return nil
	}

	models := defaultModels
	if m := parseList(os.Getenv("CLAUDE_MODELS")); len(m) > 0 {
		models = m
	}
	return &Provider{
		ownedBy: "anthropic",
		models:  models,
		pool: pool.New(accounts, pool.Options{
			Strategy: pool.RoundRobin, MaxConcurrency: cfg.MaxConcurrency, StickyTTL: cfg.StickyTTL,
		}, log),
		log: log,
	}
}

func (p *Provider) ID() string                       { return "claude" }
func (p *Provider) Capabilities() []types.Capability { return []types.Capability{types.CapAnthropic} }
func (p *Provider) IsReady() bool                    { return p.ready }
func (p *Provider) ListModels() []types.ModelObject  { return types.ModelObjects(p.models, p.ownedBy) }
func (p *Provider) Pool() *pool.Pool[auth]           { return p.pool }
func (p *Provider) AccountPool() pool.Admin          { return p.pool }
func (p *Provider) OwnsModel(model string) bool      { return strings.HasPrefix(model, "claude") }

// Init is ready if any OAuth account warms or any API key is present.
func (p *Provider) Init(ctx context.Context) error {
	var oauths []*creds
	hasKey := false
	for _, a := range p.pool.All() {
		if a.Cred.kind == "oauth" {
			oauths = append(oauths, a.Cred.creds)
		} else {
			hasKey = true
		}
	}
	warmed := len(oauths) == 0
	var lastErr error
	for _, c := range oauths {
		wctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		_, err := c.AccessToken(wctx)
		cancel()
		if err == nil {
			warmed = true
			break
		}
		lastErr = err
	}
	if !warmed && !hasKey {
		if lastErr != nil {
			return lastErr
		}
		return errNoAccount
	}
	p.ready = true
	return nil
}

var errNoAccount = &staticErr{"claude: no usable account"}

type staticErr struct{ s string }

func (e *staticErr) Error() string { return e.s }

// oauthPatch, for OAuth (subscription) mode, injects the Claude Code identity
// system and strips client fingerprint fields so the call looks like the genuine
// CLI. API-key mode returns the body unchanged.
func oauthPatch(body map[string]any, a auth) []byte {
	if a.kind == "oauth" {
		body = stripFingerprint(body)
		body["system"] = ensureIdentitySystem(body["system"])
	}
	b, _ := json.Marshal(body)
	return b
}

// ---- OpenAI Chat Completions (translated) ----------------------------------

func (p *Provider) ChatCompletion(rctx types.RequestContext, body []byte) (*http.Response, error) {
	var req chatRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, err
	}
	anthropicBody := openaiToAnthropic(req)
	return relay.WithAccountRetry(rctx.Ctx, p.pool, rctx.AccountPin, rctx.Session,
		func(acc pool.Account[auth]) (*http.Response, error) {
			payload := oauthPatch(cloneMap(anthropicBody), acc.Cred)
			upstream, err := anthropicFetch(rctx.Ctx, acc.Cred, "/v1/messages", payload)
			if err != nil {
				return nil, err
			}
			if upstream.StatusCode >= 300 {
				return upstream, nil
			}
			if req.Stream {
				return relay.StreamingResponse(func(w io.Writer) {
					defer upstream.Body.Close()
					StreamMessagesToOpenAI(w, upstream.Body, req.Model)
				}), nil
			}
			defer upstream.Body.Close()
			raw, rerr := io.ReadAll(upstream.Body)
			if rerr != nil || !json.Valid(raw) {
				// A 200 with a torn/HTML/non-JSON body must not become a fake
				// empty completion — surface it so the retry layer can react.
				return relay.SynthStatus(http.StatusBadGateway, "claude: invalid upstream response body"), nil
			}
			return relay.JSONResponse(anthropicToOpenAI(raw, req.Model)), nil
		})
}

// ---- Native Anthropic Messages (passthrough) -------------------------------

func (p *Provider) Messages(rctx types.RequestContext, body []byte) (*http.Response, error) {
	return p.native(rctx, "/v1/messages", body)
}

func (p *Provider) CountTokens(rctx types.RequestContext, body []byte) (*http.Response, error) {
	return p.native(rctx, "/v1/messages/count_tokens", body)
}

func (p *Provider) native(rctx types.RequestContext, path string, body []byte) (*http.Response, error) {
	var parsed map[string]any
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, err
	}
	return relay.WithAccountRetry(rctx.Ctx, p.pool, rctx.AccountPin, rctx.Session,
		func(acc pool.Account[auth]) (*http.Response, error) {
			payload := body
			if acc.Cred.kind == "oauth" {
				payload = oauthPatch(cloneMap(parsed), acc.Cred)
			}
			return anthropicFetch(rctx.Ctx, acc.Cred, path, payload)
		})
}

// ---- helpers ---------------------------------------------------------------

var fingerprintFields = []string{"metadata", "user", "safety_identifier"}

func stripFingerprint(body map[string]any) map[string]any {
	for _, f := range fingerprintFields {
		delete(body, f)
	}
	return body
}

func cloneMap(in map[string]any) map[string]any {
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func parseList(v string) []string {
	if v == "" {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	for _, s := range strings.Split(v, ",") {
		if s = strings.TrimSpace(s); s != "" && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}
