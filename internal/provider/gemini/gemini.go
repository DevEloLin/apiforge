// Package gemini reuses a Gemini CLI Google OAuth login against the Code Assist
// backend, serving OpenAI Chat Completions (translated to/from Gemini
// generateContent). EXPERIMENTAL: implemented to spec, gated by
// GEMINI_OAUTH_ENABLED, and not yet verified against a live login.
package gemini

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
)

var defaultModels = []string{"gemini-2.5-pro", "gemini-2.5-flash", "gemini-3-pro-preview"}

// Config carries pool tuning from the app config.
type Config struct {
	MaxConcurrency int
	StickyTTL      time.Duration
}

// Provider implements types.Provider (OpenAI chat only) over Code Assist.
type Provider struct {
	ownedBy string
	models  []string
	pool    *pool.Pool[*creds]
	project string
	ready   bool
	log     *slog.Logger
}

// New builds the gemini-cli provider from OAuth credential paths. Returns nil
// when no credential path is configured.
func New(credentialPaths []string, cfg Config, log *slog.Logger) *Provider {
	var accounts []pool.Account[*creds]
	for i, p := range credentialPaths {
		accounts = append(accounts, pool.Account[*creds]{
			ID: "gemini#" + strconv.Itoa(i+1), Cred: newCreds(p, log),
		})
	}
	if len(accounts) == 0 {
		return nil
	}
	models := defaultModels
	if m := parseList(os.Getenv("GEMINI_CLI_MODELS")); len(m) > 0 {
		models = m
	}
	return &Provider{
		ownedBy: "google",
		models:  models,
		pool: pool.New(accounts, pool.Options{
			Strategy: pool.RoundRobin, MaxConcurrency: cfg.MaxConcurrency, StickyTTL: cfg.StickyTTL,
		}, log),
		log: log,
	}
}

func (p *Provider) ID() string                       { return "gemini-cli" }
func (p *Provider) Capabilities() []types.Capability { return nil }
func (p *Provider) IsReady() bool                    { return p.ready }
func (p *Provider) ListModels() []types.ModelObject  { return types.ModelObjects(p.models, p.ownedBy) }
func (p *Provider) Pool() *pool.Pool[*creds]         { return p.pool }
func (p *Provider) AccountPool() pool.Admin          { return p.pool }
func (p *Provider) OwnsModel(model string) bool      { return strings.HasPrefix(model, "gemini") }

// Init warms the first account and discovers the Code Assist project id.
func (p *Provider) Init(ctx context.Context) error {
	first := p.pool.All()[0].Cred
	wctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if _, err := first.AccessToken(wctx); err != nil {
		return err
	}
	p.project = discoverProject(ctx, first, p.log)
	p.ready = true
	if p.log != nil {
		p.log.Warn("gemini-cli provider is EXPERIMENTAL (Code Assist reuse, unverified)")
	}
	return nil
}

func (p *Provider) ChatCompletion(rctx types.RequestContext, body []byte) (*http.Response, error) {
	var req chatRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, err
	}
	request := openaiToGeminiRequest(req)
	return relay.WithAccountRetry(rctx.Ctx, p.pool, rctx.AccountPin, rctx.Session,
		func(acc pool.Account[*creds]) (*http.Response, error) {
			upstream, err := geminiGenerate(rctx.Ctx, acc.Cred, req.Model, p.project, request, req.Stream)
			if err != nil {
				return nil, err
			}
			if upstream.StatusCode >= 300 {
				return upstream, nil
			}
			if req.Stream {
				return relay.StreamingResponse(func(w io.Writer) {
					defer upstream.Body.Close()
					StreamToOpenAI(w, upstream.Body, req.Model)
				}), nil
			}
			defer upstream.Body.Close()
			raw, _ := io.ReadAll(upstream.Body)
			return relay.JSONResponse(geminiToOpenAI(raw, req.Model)), nil
		})
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
