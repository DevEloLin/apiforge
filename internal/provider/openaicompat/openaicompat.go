// Package openaicompat is a provider for any OpenAI-compatible upstream. Each
// API key is an account in the pool (free automatic + manual switching). Used
// for vendor endpoints (DeepSeek/Kimi/GLM/...) and user-defined relays.
package openaicompat

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"apiforge/internal/pool"
	"apiforge/internal/relay"
	"apiforge/internal/types"
	"apiforge/internal/util/httpx"
)

// Options configures a provider instance.
type Options struct {
	ID           string
	BaseURL      string
	OwnedBy      string
	Models       []string
	APIKeys      []string          // one key per account
	ExtraHeaders map[string]string // e.g. editor headers
	AuthHeader   string            // "authorization" (Bearer) or a custom header name
	Concurrency  int               // per-account cap (0 = unlimited)
	StickyTTL    time.Duration
	Log          *slog.Logger
}

// Provider is an OpenAI-compatible upstream backed by an API-key pool.
type Provider struct {
	id           string
	ownedBy      string
	baseURL      string
	authHeader   string
	extraHeaders map[string]string
	models       []string
	pool         *pool.Pool[string]
	ready        bool
	log          *slog.Logger
}

// New builds a provider from opts.
func New(opts Options) *Provider {
	if opts.AuthHeader == "" {
		opts.AuthHeader = "authorization"
	}
	accounts := make([]pool.Account[string], len(opts.APIKeys))
	for i, k := range opts.APIKeys {
		accounts[i] = pool.Account[string]{ID: idFor(opts.ID, i+1), Cred: k}
	}
	pl := pool.New(accounts, pool.Options{
		Strategy:       pool.RoundRobin,
		MaxConcurrency: opts.Concurrency,
		StickyTTL:      opts.StickyTTL,
	}, opts.Log)
	return &Provider{
		id:           opts.ID,
		ownedBy:      opts.OwnedBy,
		baseURL:      strings.TrimRight(opts.BaseURL, "/"),
		authHeader:   opts.AuthHeader,
		extraHeaders: opts.ExtraHeaders,
		models:       opts.Models,
		pool:         pl,
		log:          opts.Log,
	}
}

func idFor(id string, n int) string { return id + "#" + strconv.Itoa(n) }

func (p *Provider) ID() string                    { return p.id }
func (p *Provider) Capabilities() []types.Capability { return nil }
func (p *Provider) IsReady() bool                  { return p.ready }
func (p *Provider) ListModels() []types.ModelObject { return types.ModelObjects(p.models, p.ownedBy) }
func (p *Provider) Pool() *pool.Pool[string]       { return p.pool }

func (p *Provider) OwnsModel(model string) bool {
	for _, m := range p.models {
		if m == model {
			return true
		}
	}
	return false
}

// Init prefers the vendor's live /models list (avoids hardcoded drift), falling
// back to the configured static list. Hard-bounded so a slow vendor can't stall.
func (p *Provider) Init(ctx context.Context) error {
	cctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	if live := p.fetchLiveModels(cctx); len(live) > 0 {
		if p.log != nil {
			p.log.Info("live models", "provider", p.id, "count", len(live))
		}
		p.models = live
	}
	p.ready = true
	return nil
}

func (p *Provider) fetchLiveModels(ctx context.Context) []string {
	keys := p.pool.All()
	if len(keys) == 0 {
		return nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.baseURL+"/models", nil)
	if err != nil {
		return nil
	}
	for k, v := range p.headers(keys[0].Cred) {
		req.Header.Set(k, v)
	}
	res, err := httpx.Client.Do(req)
	if err != nil {
		return nil
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil
	}
	var data struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&data) != nil {
		return nil
	}
	out := make([]string, 0, len(data.Data))
	for _, m := range data.Data {
		if m.ID != "" {
			out = append(out, m.ID)
		}
	}
	return out
}

// ChatCompletion relays to <baseURL>/chat/completions across the account pool.
func (p *Provider) ChatCompletion(rctx types.RequestContext, body []byte) (*http.Response, error) {
	return relay.WithAccountRetry(rctx.Ctx, p.pool, rctx.AccountPin, rctx.Session,
		func(acc pool.Account[string]) (*http.Response, error) {
			return relay.Do(rctx.Ctx, p.baseURL+"/chat/completions", p.headers(acc.Cred), body)
		})
}

func (p *Provider) headers(key string) map[string]string {
	h := map[string]string{}
	for k, v := range p.extraHeaders {
		h[k] = v
	}
	if strings.EqualFold(p.authHeader, "authorization") {
		h["Authorization"] = "Bearer " + key
	} else {
		h[p.authHeader] = key
	}
	return h
}
