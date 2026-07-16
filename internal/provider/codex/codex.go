// Package codex reuses a Codex CLI ChatGPT login (or plain OpenAI API keys) as
// an upstream. It speaks OpenAI Chat Completions (translated to/from the Codex
// backend Responses SSE), the native OpenAI Responses API, and the Images API.
package codex

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"apiforge/internal/pool"
	"apiforge/internal/relay"
	"apiforge/internal/types"
	"apiforge/internal/util/httpx"
	"apiforge/internal/util/idgen"
	"apiforge/internal/util/ssrf"
)

var defaultModels = []string{"gpt-5.5", "gpt-5.4", "gpt-5.4-mini", "gpt-image-2", "gpt-image-1"}

const (
	maxImages = 4
	openaiUA  = "OpenAI/NodeJS/4.104.0" // genuine-looking UA for direct api.openai.com calls
)

// account is either a reused CLI login (OAuth) or a plain OpenAI API key.
type account struct {
	kind  string // "cli" | "key"
	creds *creds
	key   string
}

// Provider implements types.Provider + ResponsesProvider + ImagesProvider.
type Provider struct {
	ownedBy string
	apiBase string
	models  []string
	pool    *pool.Pool[account]
	ready   atomic.Bool
	log     *slog.Logger
}

// New builds the codex provider from CLI credential paths plus OPENAI_API_KEYS.
// Returns nil when no account is configured (so it is not registered).
func New(credentialPaths, apiKeys []string, cfg Config, log *slog.Logger) *Provider {
	var accounts []pool.Account[account]
	n := 0
	for _, p := range credentialPaths {
		n++
		accounts = append(accounts, pool.Account[account]{
			ID:   "codex#" + strconv.Itoa(n),
			Cred: account{kind: "cli", creds: newCreds(p, log)},
		})
	}
	k := 0
	for _, key := range apiKeys {
		k++
		accounts = append(accounts, pool.Account[account]{
			ID:   "codex-key#" + strconv.Itoa(k),
			Cred: account{kind: "key", key: key},
		})
	}
	if len(accounts) == 0 {
		return nil
	}

	apiBase := strings.TrimRight(envOr("OPENAI_BASE_URL", "https://api.openai.com/v1"), "/")
	if err := ssrf.AssertPublicURL(apiBase, "codex (OPENAI_BASE_URL)"); err != nil {
		if log != nil {
			log.Warn("codex disabled: bad OPENAI_BASE_URL", "err", err)
		}
		return nil
	}

	models := defaultModels
	if m := parseList(os.Getenv("CODEX_MODELS")); len(m) > 0 {
		models = m
	}

	return &Provider{
		ownedBy: "openai",
		apiBase: apiBase,
		models:  models,
		pool: pool.New(accounts, pool.Options{
			Strategy:       pool.RoundRobin,
			MaxConcurrency: cfg.MaxConcurrency,
			StickyTTL:      cfg.StickyTTL,
		}, log),
		log: log,
	}
}

// Config carries pool tuning from the app config.
type Config struct {
	MaxConcurrency int
	StickyTTL      time.Duration
}

func (p *Provider) ID() string { return "codex" }
func (p *Provider) Capabilities() []types.Capability {
	return []types.Capability{types.CapResponses, types.CapImages}
}
func (p *Provider) IsReady() bool                   { return p.ready.Load() }
func (p *Provider) ListModels() []types.ModelObject { return types.ModelObjects(p.models, p.ownedBy) }
func (p *Provider) Pool() *pool.Pool[account]       { return p.pool }
func (p *Provider) AccountPool() pool.Admin         { return p.pool }

func (p *Provider) OwnsModel(model string) bool {
	return strings.HasPrefix(model, "gpt-") ||
		strings.HasPrefix(model, "o3") ||
		strings.HasPrefix(model, "o4") ||
		strings.HasPrefix(model, "codex")
}

// Init is ready if any CLI account warms (token loads/refreshes) or any API key
// is present. Failures are isolated by the registry.
func (p *Provider) Init(ctx context.Context) error {
	var clis []*creds
	hasKey := false
	for _, a := range p.pool.All() {
		switch a.Cred.kind {
		case "cli":
			clis = append(clis, a.Cred.creds)
		case "key":
			hasKey = true
		}
	}
	warmed := len(clis) == 0
	var lastErr error
	for _, c := range clis {
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
	p.ready.Store(true)
	return nil
}

var errNoAccount = &staticErr{"codex: no usable account"}

type staticErr struct{ s string }

func (e *staticErr) Error() string { return e.s }

func (p *Provider) keyHeaders(key string) map[string]string {
	return map[string]string{"Authorization": "Bearer " + key, "User-Agent": openaiUA}
}

// ---- Chat Completions ------------------------------------------------------

func (p *Provider) ChatCompletion(rctx types.RequestContext, body []byte) (*http.Response, error) {
	var req chatRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return relay.SynthStatus(http.StatusBadRequest, "invalid request body"), nil
	}
	return relay.WithAccountRetry(rctx.Ctx, p.pool, rctx.AccountPin, rctx.Session,
		func(acc pool.Account[account]) (*http.Response, error) {
			if acc.Cred.kind == "key" {
				return relay.Do(rctx.Ctx, p.apiBase+"/chat/completions", p.keyHeaders(acc.Cred.key), body)
			}
			sessionID := idgen.UUID()
			codexBody := openaiToCodex(req, sessionID)
			upstream, err := codexFetch(rctx.Ctx, acc.Cred.creds, codexBody, sessionID)
			if err != nil {
				return nil, err
			}
			if upstream.StatusCode >= 300 {
				return upstream, nil // let the retry layer classify the status
			}
			if req.Stream {
				return streamResponse(upstream, req.Model), nil
			}
			defer upstream.Body.Close()
			body, ok := AggregateChatCompletion(upstream.Body, req.Model)
			if !ok {
				// Upstream returned 200 then failed/truncated — surface an error
				// (retriable on another account) instead of a fake empty success.
				return relay.SynthStatus(http.StatusBadGateway, "codex: upstream returned no completion"), nil
			}
			return jsonResponse(body), nil
		})
}

// ---- Native Responses API --------------------------------------------------

func (p *Provider) Responses(rctx types.RequestContext, body []byte) (*http.Response, error) {
	var raw map[string]any
	if err := json.Unmarshal(body, &raw); err != nil {
		return relay.SynthStatus(http.StatusBadRequest, "invalid request body"), nil
	}
	wantStream, _ := raw["stream"].(bool)
	model := str(raw["model"])
	return relay.WithAccountRetry(rctx.Ctx, p.pool, rctx.AccountPin, rctx.Session,
		func(acc pool.Account[account]) (*http.Response, error) {
			if acc.Cred.kind == "key" {
				return relay.Do(rctx.Ctx, p.apiBase+"/responses", p.keyHeaders(acc.Cred.key), body)
			}
			sessionID := idgen.UUID()
			codexBody := stripFingerprint(raw)
			codexBody["store"] = false
			codexBody["stream"] = true
			codexBody["prompt_cache_key"] = sessionID
			upstream, err := codexFetch(rctx.Ctx, acc.Cred.creds, codexBody, sessionID)
			if err != nil {
				return nil, err
			}
			if upstream.StatusCode >= 300 {
				return upstream, nil
			}
			if wantStream {
				// Responses is already the native format — pass the SSE through.
				upstream.Header.Set("Content-Type", "text/event-stream; charset=utf-8")
				upstream.Header.Set("Cache-Control", "no-cache, no-transform")
				upstream.Header.Set("X-Accel-Buffering", "no")
				return upstream, nil
			}
			defer upstream.Body.Close()
			return jsonResponse(AggregateResponses(upstream.Body, model)), nil
		})
}

// ---- Images API ------------------------------------------------------------

func (p *Provider) Images(rctx types.RequestContext, body []byte) (*http.Response, error) {
	var req types.ImageRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return relay.SynthStatus(http.StatusBadRequest, "invalid request body"), nil
	}
	n := clampN(req.N)
	images := make([]*collectedImage, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			images[i] = p.oneImage(rctx, req)
		}(i)
	}
	wg.Wait()

	out := make([]*collectedImage, 0, n)
	for _, img := range images {
		if img != nil {
			out = append(out, img)
		}
	}
	if len(out) == 0 {
		return relay.SynthStatus(http.StatusBadGateway, "The backend did not return an image."), nil
	}
	if len(out) < n && p.log != nil {
		p.log.Warn("fewer images than requested", "want", n, "got", len(out))
	}
	return jsonResponse(toImagesResponse(out)), nil
}

// oneImage runs a single image request through account retry and returns the
// collected image (nil on failure). The envelope round-trip keeps the retry
// layer HTTP-uniform across CLI and API-key accounts.
func (p *Provider) oneImage(rctx types.RequestContext, req types.ImageRequest) *collectedImage {
	res, err := relay.WithAccountRetry(rctx.Ctx, p.pool, rctx.AccountPin, rctx.Session,
		func(acc pool.Account[account]) (*http.Response, error) {
			if acc.Cred.kind == "key" {
				return p.keyImage(rctx.Ctx, acc.Cred.key, req)
			}
			sessionID := idgen.UUID()
			var codexBody map[string]any
			if len(req.Images) > 0 {
				codexBody = buildImageEditBody(req, sessionID)
			} else {
				codexBody = buildImageGenBody(req, sessionID)
			}
			upstream, ferr := codexFetch(rctx.Ctx, acc.Cred.creds, codexBody, sessionID)
			if ferr != nil {
				return nil, ferr
			}
			if upstream.StatusCode >= 300 {
				return upstream, nil
			}
			defer upstream.Body.Close()
			img, completed := collectImage(upstream.Body)
			if img != nil {
				return imageEnvelope(img), nil
			}
			if completed {
				// Stream finished with no image = deterministic (refusal/empty).
				// Return a non-retriable status so we don't re-generate on every
				// other account (422 falls through the retry classifier).
				return relay.SynthStatus(http.StatusUnprocessableEntity, "codex: no image (refused or empty)"), nil
			}
			return relay.SynthStatus(http.StatusBadGateway, "codex: image stream truncated"), nil // retriable
		})
	if err != nil || res == nil || res.StatusCode != http.StatusOK {
		if res != nil {
			res.Body.Close()
		}
		return nil
	}
	defer res.Body.Close()
	return parseEnvelope(res.Body)
}

// keyImage relays an image request to a plain OpenAI endpoint and normalizes the
// first returned image into the internal envelope.
func (p *Provider) keyImage(ctx context.Context, key string, req types.ImageRequest) (*http.Response, error) {
	var res *http.Response
	var err error
	if len(req.Images) > 0 {
		res, err = p.keyImageEdit(ctx, key, req)
	} else {
		// Omit empty enums — OpenAI /images/generations 400s on "" size/quality/format.
		gen := map[string]any{"model": req.Model, "prompt": req.Prompt, "n": 1}
		if req.Size != "" && req.Size != "auto" {
			gen["size"] = req.Size
		}
		if req.Quality != "" && req.Quality != "auto" {
			gen["quality"] = req.Quality
		}
		if req.OutputFormat != "" {
			gen["output_format"] = req.OutputFormat
		}
		genBody, _ := json.Marshal(gen)
		res, err = relay.Do(ctx, p.apiBase+"/images/generations", p.keyHeaders(key), genBody)
	}
	if err != nil {
		return nil, err
	}
	if res.StatusCode >= 300 {
		return res, nil // retriable status -> classified upstream
	}
	defer res.Body.Close()
	img := firstImageFromJSON(res.Body)
	if img == nil {
		return relay.SynthStatus(http.StatusBadGateway, "no image in response"), nil
	}
	return imageEnvelope(img), nil
}

func (p *Provider) keyImageEdit(ctx context.Context, key string, req types.ImageRequest) (*http.Response, error) {
	pr, pw := io.Pipe()
	mw := multipartWriter(pw, req)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.apiBase+"/images/edits", pr)
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", mw.contentType)
	for k, v := range p.keyHeaders(key) {
		httpReq.Header.Set(k, v)
	}
	go mw.write()
	return httpx.Client.Do(httpReq)
}

// ---- response helpers ------------------------------------------------------

func streamResponse(upstream *http.Response, model string) *http.Response {
	return relay.StreamingResponse(func(w io.Writer) {
		defer upstream.Body.Close()
		StreamChatCompletion(w, upstream.Body, model)
	})
}

func jsonResponse(v any) *http.Response { return relay.JSONResponse(v) }

func imageEnvelope(img *collectedImage) *http.Response {
	return jsonResponse(map[string]any{
		"b64": img.b64, "output_format": img.outputFormat,
		"size": img.size, "revised_prompt": img.revisedPrompt,
	})
}

func parseEnvelope(r io.Reader) *collectedImage {
	var e struct {
		B64, OutputFormat, Size, RevisedPrompt string
	}
	var m map[string]string
	if json.NewDecoder(r).Decode(&m) != nil {
		return nil
	}
	e.B64, e.OutputFormat, e.Size, e.RevisedPrompt = m["b64"], m["output_format"], m["size"], m["revised_prompt"]
	if e.B64 == "" {
		return nil
	}
	return &collectedImage{b64: e.B64, outputFormat: e.OutputFormat, size: e.Size, revisedPrompt: e.RevisedPrompt}
}

func firstImageFromJSON(r io.Reader) *collectedImage {
	var d struct {
		Data []struct {
			B64JSON       string `json:"b64_json"`
			RevisedPrompt string `json:"revised_prompt"`
		} `json:"data"`
		OutputFormat string `json:"output_format"`
		Size         string `json:"size"`
	}
	if json.NewDecoder(r).Decode(&d) != nil || len(d.Data) == 0 || d.Data[0].B64JSON == "" {
		return nil
	}
	of := d.OutputFormat
	if of == "" {
		of = "png"
	}
	return &collectedImage{b64: d.Data[0].B64JSON, outputFormat: of, size: d.Size, revisedPrompt: d.Data[0].RevisedPrompt}
}

// ---- misc helpers ----------------------------------------------------------

func clampN(n int) int {
	if n < 1 {
		return 1
	}
	if n > maxImages {
		return maxImages
	}
	return n
}

var fingerprintFields = []string{"metadata", "user", "safety_identifier"}

func stripFingerprint(body map[string]any) map[string]any {
	out := make(map[string]any, len(body))
	for k, v := range body {
		out[k] = v
	}
	for _, f := range fingerprintFields {
		delete(out, f)
	}
	return out
}

func envOr(name, def string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return def
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
