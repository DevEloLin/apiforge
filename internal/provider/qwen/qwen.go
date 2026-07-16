// Package qwen reuses a Qwen Code CLI OAuth login against the OpenAI-compatible
// DashScope endpoint (base URL derived per-account from the login's resource_url).
package qwen

import (
	"context"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"apiforge/internal/pool"
	"apiforge/internal/relay"
	"apiforge/internal/types"
)

// coder-model is OAuth mode's server-resolved alias; other qwen-* ids are left
// to the key-based `qwen` vendor (exact-id routing) to avoid duplicate ads.
var defaultModels = []string{"coder-model"}

// Config carries pool tuning from the app config.
type Config struct {
	MaxConcurrency int
	StickyTTL      time.Duration
}

// Provider serves OpenAI Chat Completions over the Qwen CLI login pool.
type Provider struct {
	ownedBy string
	models  []string
	pool    *pool.Pool[*creds]
	ready   bool
	log     *slog.Logger
}

// New builds the qwen-cli provider from OAuth credential paths. Returns nil when
// no credential path is configured.
func New(credentialPaths []string, cfg Config, log *slog.Logger) *Provider {
	var accounts []pool.Account[*creds]
	for i, p := range credentialPaths {
		accounts = append(accounts, pool.Account[*creds]{
			ID: "qwen#" + strconv.Itoa(i+1), Cred: newCreds(p, log),
		})
	}
	if len(accounts) == 0 {
		return nil
	}
	return &Provider{
		ownedBy: "alibaba",
		models:  defaultModels,
		pool: pool.New(accounts, pool.Options{
			Strategy: pool.RoundRobin, MaxConcurrency: cfg.MaxConcurrency, StickyTTL: cfg.StickyTTL,
		}, log),
		log: log,
	}
}

func (p *Provider) ID() string                       { return "qwen-cli" }
func (p *Provider) Capabilities() []types.Capability { return nil }
func (p *Provider) IsReady() bool                    { return p.ready }
func (p *Provider) ListModels() []types.ModelObject  { return types.ModelObjects(p.models, p.ownedBy) }
func (p *Provider) Pool() *pool.Pool[*creds]         { return p.pool }

func (p *Provider) OwnsModel(model string) bool {
	return model == "coder-model" || strings.HasPrefix(model, "qwen")
}

// Init warms the first account so a dead login surfaces as a clean disable.
func (p *Provider) Init(ctx context.Context) error {
	first := p.pool.All()[0].Cred
	wctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if _, err := first.AccessToken(wctx); err != nil {
		return err
	}
	p.ready = true
	return nil
}

func (p *Provider) ChatCompletion(rctx types.RequestContext, body []byte) (*http.Response, error) {
	return relay.WithAccountRetry(rctx.Ctx, p.pool, rctx.AccountPin, rctx.Session,
		func(acc pool.Account[*creds]) (*http.Response, error) {
			tok, err := acc.Cred.AccessToken(rctx.Ctx)
			if err != nil {
				return relay.SynthStatus(http.StatusUnauthorized, "qwen token refresh failed"), nil
			}
			return relay.Do(rctx.Ctx, acc.Cred.BaseURL()+"/chat/completions",
				map[string]string{"Authorization": "Bearer " + tok}, body)
		})
}
