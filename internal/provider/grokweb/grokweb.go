// Package grokweb reuses a grok.com subscription session (the `sso` cookie) as
// an upstream, translating OpenAI Chat Completions to grok.com's reverse-
// engineered web chat API. EXPERIMENTAL — like the Grok web client it spoofs the
// x-statsig-id anti-bot token; Cloudflare may still challenge the Go TLS
// fingerprint (see README: pass a full cookie incl. cf_clearance if so).
package grokweb

import (
	"bytes"
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
)

const modelPrefix = "grok-web/"

// Config carries pool tuning from the app config.
type Config struct {
	MaxConcurrency int
	StickyTTL      time.Duration
}

// Provider serves OpenAI Chat Completions over grok.com's web chat API. Models
// are advertised with a "grok-web/" prefix so they never collide with the
// API-key `grok` vendor (api.x.ai).
type Provider struct {
	ownedBy string
	models  []string
	pool    *pool.Pool[string] // each account = one sso cookie / cookie string
	ready   atomic.Bool
	log     *slog.Logger
}

// New builds the grok-web provider from session cookies. Returns nil when none.
func New(cookies []string, cfg Config, log *slog.Logger) *Provider {
	var accounts []pool.Account[string]
	for i, c := range cookies {
		c = strings.TrimSpace(c)
		if c == "" {
			continue
		}
		accounts = append(accounts, pool.Account[string]{ID: "grok-web#" + strconv.Itoa(i+1), Cred: c})
	}
	if len(accounts) == 0 {
		return nil
	}
	models := make([]string, len(modelIDs))
	for i, m := range modelIDs {
		models[i] = modelPrefix + m
	}
	return &Provider{
		ownedBy: "xai",
		models:  models,
		pool: pool.New(accounts, pool.Options{
			Strategy: pool.RoundRobin, MaxConcurrency: cfg.MaxConcurrency, StickyTTL: cfg.StickyTTL,
		}, log),
		log: log,
	}
}

func (p *Provider) ID() string                       { return "grok-web" }
func (p *Provider) Capabilities() []types.Capability { return nil }
func (p *Provider) IsReady() bool                    { return p.ready.Load() }
func (p *Provider) ListModels() []types.ModelObject  { return types.ModelObjects(p.models, p.ownedBy) }
func (p *Provider) Pool() *pool.Pool[string]         { return p.pool }
func (p *Provider) AccountPool() pool.Admin          { return p.pool }
func (p *Provider) OwnsModel(model string) bool      { return strings.HasPrefix(model, modelPrefix) }

func (p *Provider) Init(_ context.Context) error {
	p.ready.Store(true)
	if p.log != nil {
		p.log.Warn("grok-web provider is EXPERIMENTAL (reverse-engineered grok.com web API; Cloudflare may challenge)")
	}
	return nil
}

type chatRequest struct {
	Model    string    `json:"model"`
	Stream   bool      `json:"stream"`
	Messages []message `json:"messages"`
}

func (p *Provider) ChatCompletion(rctx types.RequestContext, body []byte) (*http.Response, error) {
	var req chatRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return relay.SynthStatus(http.StatusBadRequest, "invalid request body"), nil
	}
	spec := resolveModel(strings.TrimPrefix(req.Model, modelPrefix))
	payload, _ := json.Marshal(buildPayload(foldMessages(req.Messages), spec))

	return relay.WithAccountRetry(rctx.Ctx, p.pool, rctx.AccountPin, rctx.Session,
		func(acc pool.Account[string]) (*http.Response, error) {
			hreq, err := http.NewRequestWithContext(rctx.Ctx, http.MethodPost, newConversationURL, bytes.NewReader(payload))
			if err != nil {
				return nil, err
			}
			for k, v := range headers(cookieHeader(acc.Cred)) {
				hreq.Header.Set(k, v)
			}
			upstream, err := httpx.Client.Do(hreq)
			if err != nil {
				return nil, err
			}
			if upstream.StatusCode >= 300 {
				return upstream, nil // 401/403/429 → account-retry classifies
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
