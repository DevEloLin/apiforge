// Package copilot reuses GitHub Copilot logins: it exchanges each discovered
// GitHub OAuth token for a short-lived Copilot API token and relays OpenAI Chat
// Completions to the Copilot backend with the editor fingerprint headers.
package copilot

import (
	"context"
	"encoding/json"
	"fmt"
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

var fallbackModels = []string{"gpt-4o", "gpt-4o-mini", "o3-mini", "claude-3.5-sonnet", "gemini-2.0-flash"}

// Config carries pool tuning from the app config.
type Config struct {
	MaxConcurrency int
	StickyTTL      time.Duration
}

// Provider serves OpenAI Chat Completions over the Copilot backend. Models are
// advertised with a "copilot/" prefix to avoid id collisions with other providers.
type Provider struct {
	ownedBy    string
	configDirs []string
	cfg        Config
	models     []string
	pool       *pool.Pool[*creds]
	ready      bool
	log        *slog.Logger
}

// New returns a copilot provider that discovers its GitHub tokens at Init time.
func New(configDirs []string, cfg Config, log *slog.Logger) *Provider {
	return &Provider{ownedBy: "github-copilot", configDirs: configDirs, cfg: cfg, log: log}
}

func (p *Provider) ID() string                       { return "copilot" }
func (p *Provider) Capabilities() []types.Capability { return nil }
func (p *Provider) IsReady() bool                    { return p.ready }
func (p *Provider) ListModels() []types.ModelObject  { return types.ModelObjects(p.models, p.ownedBy) }
func (p *Provider) Pool() *pool.Pool[*creds]         { return p.pool }
func (p *Provider) AccountPool() pool.Admin          { return p.pool }
func (p *Provider) OwnsModel(model string) bool      { return strings.HasPrefix(model, "copilot/") }

// Init discovers GitHub tokens, builds the pool, and advertises live models.
func (p *Provider) Init(ctx context.Context) error {
	tokens := discoverGithubTokens(p.configDirs)
	if len(tokens) == 0 {
		return &staticErr{"copilot: no GitHub tokens found (login via Copilot, or set COPILOT_GITHUB_TOKENS)"}
	}
	accounts := make([]pool.Account[*creds], len(tokens))
	for i, t := range tokens {
		accounts[i] = pool.Account[*creds]{ID: "copilot#" + strconv.Itoa(i+1), Cred: newCreds(t, p.log)}
	}
	p.pool = pool.New(accounts, pool.Options{
		Strategy: pool.RoundRobin, MaxConcurrency: p.cfg.MaxConcurrency, StickyTTL: p.cfg.StickyTTL,
	}, p.log)

	first := accounts[0].Cred
	wctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	tok, err := first.copilotToken(wctx)
	if err != nil {
		return err
	}
	ids := p.fetchModels(wctx, first, tok)
	p.models = make([]string, len(ids))
	for i, id := range ids {
		p.models[i] = "copilot/" + id
	}
	p.ready = true
	return nil
}

func (p *Provider) ChatCompletion(rctx types.RequestContext, body []byte) (*http.Response, error) {
	upstreamBody, stream := rewriteModel(body)
	return relay.WithAccountRetry(rctx.Ctx, p.pool, rctx.AccountPin, rctx.Session,
		func(acc pool.Account[*creds]) (*http.Response, error) {
			tok, err := acc.Cred.copilotToken(rctx.Ctx)
			if err != nil {
				return nil, fmt.Errorf("copilot token exchange: %w", err)
			}
			return relay.Do(rctx.Ctx, acc.Cred.apiBaseURL()+"/chat/completions", chatHeaders(tok, stream), upstreamBody)
		})
}

// rewriteModel strips the "copilot/" prefix from the model id and reports stream.
func rewriteModel(body []byte) ([]byte, bool) {
	var m map[string]any
	if json.Unmarshal(body, &m) != nil {
		return body, false
	}
	if model, ok := m["model"].(string); ok {
		m["model"] = strings.TrimPrefix(model, "copilot/")
	}
	stream, _ := m["stream"].(bool)
	out, err := json.Marshal(m)
	if err != nil {
		return body, stream
	}
	return out, stream
}

func chatHeaders(token string, stream bool) map[string]string {
	h := map[string]string{
		"Authorization":          "Bearer " + token,
		"copilot-integration-id": "vscode-chat",
		"editor-version":         editorVersion,
		"editor-plugin-version":  editorPluginVersion,
		"User-Agent":             copilotUserAgent,
		"openai-intent":          "conversation-panel",
	}
	if stream {
		h["x-initiator"] = "user"
	}
	return h
}

func (p *Provider) fetchModels(ctx context.Context, c *creds, token string) []string {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.apiBaseURL()+"/models", nil)
	if err != nil {
		return fallbackModels
	}
	for k, v := range chatHeaders(token, false) {
		req.Header.Set(k, v)
	}
	res, err := httpx.Client.Do(req)
	if err != nil {
		return fallbackModels
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fallbackModels
	}
	var data struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&data) != nil {
		return fallbackModels
	}
	var ids []string
	for _, m := range data.Data {
		if m.ID != "" {
			ids = append(ids, m.ID)
		}
	}
	if len(ids) == 0 {
		return fallbackModels
	}
	if p.log != nil {
		p.log.Info("discovered copilot models", "count", len(ids))
	}
	return ids
}

type staticErr struct{ s string }

func (e *staticErr) Error() string { return e.s }
