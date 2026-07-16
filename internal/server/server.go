// Package server wires the HTTP surface: OpenAI (/v1/chat/completions,
// /v1/models, /v1/responses), Anthropic (/v1/messages), images, admin, health.
// Provider responses (real or synthesized) are streamed back with io.Copy so
// memory stays flat regardless of payload size.
package server

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"apiforge/internal/config"
	"apiforge/internal/pool"
	"apiforge/internal/registry"
	"apiforge/internal/types"
	"apiforge/internal/util/sanitize"
)

// Server holds the shared dependencies for the HTTP handlers.
type Server struct {
	cfg config.Config
	reg *registry.Registry
	log *slog.Logger
	rl  *rateLimiter
}

// New builds the top-level HTTP handler.
func New(cfg config.Config, reg *registry.Registry, log *slog.Logger) http.Handler {
	s := &Server{cfg: cfg, reg: reg, log: log, rl: newRateLimiter(cfg.RateLimitRPM)}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", s.health)

	v1 := http.NewServeMux()
	v1.HandleFunc("GET /v1/models", s.models)
	v1.HandleFunc("POST /v1/chat/completions", s.chat)
	v1.HandleFunc("POST /v1/responses", s.responses)
	v1.HandleFunc("POST /v1/messages", s.messages)
	v1.HandleFunc("POST /v1/messages/count_tokens", s.countTokens)
	v1.HandleFunc("POST /v1/images/generations", s.imageGenerations)
	v1.HandleFunc("POST /v1/images/edits", s.imageEdits)
	mux.Handle("/v1/", chain(v1, s.authMiddleware, s.rateLimitMiddleware, s.bodyLimitMiddleware))

	admin := http.NewServeMux()
	admin.HandleFunc("GET /admin/providers", s.adminProviders)
	admin.HandleFunc("GET /admin/accounts", s.adminAccounts)
	admin.HandleFunc("POST /admin/accounts/preferred", s.adminSetPreferred)
	admin.HandleFunc("POST /admin/accounts/enabled", s.adminSetEnabled)
	mux.Handle("/admin/", chain(admin, s.adminMiddleware, s.rateLimitMiddleware, s.bodyLimitMiddleware))

	return mux
}

func (s *Server) health(w http.ResponseWriter, _ *http.Request) {
	diag := s.reg.Diagnostics()
	type readyP struct {
		ID     string              `json:"id"`
		Models []types.ModelObject `json:"models"`
	}
	type disabledP struct {
		ID     string `json:"id"`
		Reason string `json:"reason"`
	}
	ready := []readyP{}
	disabled := []disabledP{}
	for _, d := range diag {
		if d.Ready {
			ready = append(ready, readyP{ID: d.ID, Models: d.Models})
		} else {
			disabled = append(disabled, disabledP{ID: d.ID, Reason: sanitize.Secrets(sanitize.Path(d.Reason))})
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "providers": ready, "disabled": disabled})
}

func (s *Server) models(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"object": "list", "data": s.reg.Models()})
}

// dispatch reads the body, resolves the provider by model, and hands off to fn.
func (s *Server) dispatch(w http.ResponseWriter, r *http.Request, need types.Capability, fn func(types.Provider, types.RequestContext, []byte) (*http.Response, error)) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		s.writeError(w, r, http.StatusBadRequest, "invalid_request_error", "Failed to read request body.")
		return
	}
	model := extractModel(body)
	if model == "" {
		s.writeError(w, r, http.StatusBadRequest, "invalid_request_error", "Missing required field: model.")
		return
	}
	p := s.reg.FindByModel(model)
	if p == nil {
		s.writeError(w, r, http.StatusNotFound, "invalid_request_error", "No provider serves model: "+model)
		return
	}
	if need != "" && !types.HasCapability(p, need) {
		s.writeError(w, r, http.StatusBadRequest, "invalid_request_error", "Provider "+p.ID()+" does not support "+string(need)+".")
		return
	}
	rctx := types.RequestContext{
		RequestID:  r.Header.Get("x-request-id"),
		Ctx:        r.Context(),
		AccountPin: r.Header.Get("x-apiforge-account"),
		Session:    r.Header.Get("x-apiforge-session"),
	}
	resp, err := fn(p, rctx, body)
	if err != nil {
		s.log.Error("provider error", "provider", p.ID(), "err", sanitize.Secrets(err.Error()))
		s.writeError(w, r, http.StatusBadGateway, "api_error", "Upstream request failed.")
		return
	}
	writeUpstream(w, resp)
}

func (s *Server) chat(w http.ResponseWriter, r *http.Request) {
	s.dispatch(w, r, "", func(p types.Provider, rc types.RequestContext, b []byte) (*http.Response, error) {
		return p.ChatCompletion(rc, b)
	})
}

func (s *Server) responses(w http.ResponseWriter, r *http.Request) {
	s.dispatch(w, r, types.CapResponses, func(p types.Provider, rc types.RequestContext, b []byte) (*http.Response, error) {
		return p.(types.ResponsesProvider).Responses(rc, b)
	})
}

// defaultImageModel is used when the client omits `model` (OpenAI convention).
const defaultImageModel = "gpt-image-1"

// multipartMaxMemory bounds in-memory buffering of multipart parts; larger parts
// spill to a temp file. The overall size is still capped by bodyLimitMiddleware.
const multipartMaxMemory = 8 << 20

func (s *Server) imageGenerations(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		s.writeError(w, r, http.StatusBadRequest, "invalid_request_error", "Failed to read request body.")
		return
	}
	var req types.ImageRequest
	if err := json.Unmarshal(body, &req); err != nil {
		s.writeError(w, r, http.StatusBadRequest, "invalid_request_error", "Invalid JSON body.")
		return
	}
	req.Images = nil // generations never carries input images
	req.Mask = nil
	if req.Prompt == "" {
		s.writeError(w, r, http.StatusBadRequest, "invalid_request_error", "Missing required parameter: 'prompt'.")
		return
	}
	s.dispatchImages(w, r, req)
}

func (s *Server) imageEdits(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(multipartMaxMemory); err != nil {
		s.writeError(w, r, http.StatusBadRequest, "invalid_request_error", "Expected multipart/form-data within the size limit.")
		return
	}
	prompt := r.FormValue("prompt")
	if prompt == "" {
		s.writeError(w, r, http.StatusBadRequest, "invalid_request_error", "Missing required parameter: 'prompt'.")
		return
	}
	images := collectFormImages(r, "image")
	if len(images) == 0 {
		s.writeError(w, r, http.StatusBadRequest, "invalid_request_error", "Missing required file: 'image'.")
		return
	}
	req := types.ImageRequest{
		Model:   r.FormValue("model"),
		Prompt:  prompt,
		Size:    r.FormValue("size"),
		Quality: r.FormValue("quality"),
		Images:  images,
	}
	if n, err := strconv.Atoi(r.FormValue("n")); err == nil {
		req.N = n
	}
	if mask := collectFormImages(r, "mask"); len(mask) > 0 {
		req.Mask = &mask[0]
	}
	s.dispatchImages(w, r, req)
}

// dispatchImages resolves the image-capable provider by model and hands off the
// normalized request as a JSON body (uniform provider contract).
func (s *Server) dispatchImages(w http.ResponseWriter, r *http.Request, req types.ImageRequest) {
	if req.Model == "" {
		req.Model = defaultImageModel
	}
	p := s.reg.FindByModel(req.Model)
	if p == nil {
		s.writeError(w, r, http.StatusNotFound, "invalid_request_error", "No provider serves model: "+req.Model)
		return
	}
	if !types.HasCapability(p, types.CapImages) {
		s.writeError(w, r, http.StatusBadRequest, "invalid_request_error", "The model `"+req.Model+"` does not support the Images API on this gateway.")
		return
	}
	body, err := json.Marshal(req)
	if err != nil {
		s.writeError(w, r, http.StatusInternalServerError, "api_error", "Failed to encode request.")
		return
	}
	rctx := types.RequestContext{
		RequestID:  r.Header.Get("x-request-id"),
		Ctx:        r.Context(),
		AccountPin: r.Header.Get("x-apiforge-account"),
		Session:    r.Header.Get("x-apiforge-session"),
	}
	resp, err := p.(types.ImagesProvider).Images(rctx, body)
	if err != nil {
		s.log.Error("provider error", "provider", p.ID(), "err", sanitize.Secrets(err.Error()))
		s.writeError(w, r, http.StatusBadGateway, "api_error", "Upstream request failed.")
		return
	}
	writeUpstream(w, resp)
}

// collectFormImages reads uploaded files under field and field[] as base64 inputs.
func collectFormImages(r *http.Request, field string) []types.ImageInput {
	if r.MultipartForm == nil {
		return nil
	}
	var out []types.ImageInput
	for _, key := range []string{field, field + "[]"} {
		for _, fh := range r.MultipartForm.File[key] {
			f, err := fh.Open()
			if err != nil {
				continue
			}
			data, err := io.ReadAll(f)
			f.Close()
			if err != nil {
				continue
			}
			ct := fh.Header.Get("Content-Type")
			if ct == "" {
				ct = "image/png"
			}
			name := fh.Filename
			if name == "" {
				name = field + ".png"
			}
			out = append(out, types.ImageInput{
				B64:         base64.StdEncoding.EncodeToString(data),
				ContentType: ct,
				Filename:    name,
			})
		}
	}
	return out
}

func (s *Server) messages(w http.ResponseWriter, r *http.Request) {
	s.dispatch(w, r, types.CapAnthropic, func(p types.Provider, rc types.RequestContext, b []byte) (*http.Response, error) {
		return p.(types.AnthropicProvider).Messages(rc, b)
	})
}

func (s *Server) countTokens(w http.ResponseWriter, r *http.Request) {
	s.dispatch(w, r, types.CapAnthropic, func(p types.Provider, rc types.RequestContext, b []byte) (*http.Response, error) {
		return p.(types.AnthropicProvider).CountTokens(rc, b)
	})
}

// adminProviders lists every ready provider, its models, and whether it is pooled.
func (s *Server) adminProviders(w http.ResponseWriter, _ *http.Request) {
	type prov struct {
		ID     string   `json:"id"`
		Models []string `json:"models"`
		Pooled bool     `json:"pooled"`
	}
	out := []prov{}
	for _, p := range s.reg.Ready() {
		_, pooled := p.(types.Pooled)
		ids := make([]string, 0)
		for _, m := range p.ListModels() {
			ids = append(ids, m.ID)
		}
		out = append(out, prov{ID: p.ID(), Models: ids, Pooled: pooled})
	}
	writeJSON(w, http.StatusOK, map[string]any{"providers": out})
}

// adminAccounts reports per-provider account health for pooled providers.
func (s *Server) adminAccounts(w http.ResponseWriter, _ *http.Request) {
	out := map[string]any{}
	for _, p := range s.reg.Ready() {
		if pp, ok := p.(types.Pooled); ok {
			out[p.ID()] = pp.AccountPool().Status()
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"accounts": out})
}

// adminSetPreferred pins (or clears, with an empty account) the preferred account.
func (s *Server) adminSetPreferred(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Provider string `json:"provider"`
		Account  string `json:"account"`
	}
	if !s.decodeAdmin(w, r, &body) {
		return
	}
	adm := s.poolFor(w, r, body.Provider)
	if adm == nil {
		return
	}
	if !adm.SetPreferred(body.Account) {
		s.writeError(w, r, http.StatusBadRequest, "invalid_request_error", "Unknown account: "+body.Account)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "provider": body.Provider, "preferred": body.Account})
}

// adminSetEnabled enables/disables one account in a provider's pool.
func (s *Server) adminSetEnabled(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Provider string `json:"provider"`
		Account  string `json:"account"`
		Enabled  *bool  `json:"enabled"`
	}
	if !s.decodeAdmin(w, r, &body) {
		return
	}
	if body.Account == "" {
		s.writeError(w, r, http.StatusBadRequest, "invalid_request_error", "Missing 'account'.")
		return
	}
	enabled := body.Enabled == nil || *body.Enabled
	adm := s.poolFor(w, r, body.Provider)
	if adm == nil {
		return
	}
	if !adm.SetEnabled(body.Account, enabled) {
		s.writeError(w, r, http.StatusBadRequest, "invalid_request_error", "Unknown account: "+body.Account)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "provider": body.Provider, "account": body.Account, "enabled": enabled})
}

// decodeAdmin decodes a JSON admin body and validates the provider field.
func (s *Server) decodeAdmin(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		s.writeError(w, r, http.StatusBadRequest, "invalid_request_error", "Invalid JSON body.")
		return false
	}
	return true
}

// poolFor resolves a provider's admin pool surface, writing an error if missing.
func (s *Server) poolFor(w http.ResponseWriter, r *http.Request, providerID string) pool.Admin {
	if providerID == "" {
		s.writeError(w, r, http.StatusBadRequest, "invalid_request_error", "Missing 'provider'.")
		return nil
	}
	p := s.reg.ByID(providerID)
	if p == nil {
		s.writeError(w, r, http.StatusNotFound, "invalid_request_error", "Unknown provider: "+providerID)
		return nil
	}
	pp, ok := p.(types.Pooled)
	if !ok {
		s.writeError(w, r, http.StatusBadRequest, "invalid_request_error", "Provider "+providerID+" has no account pool.")
		return nil
	}
	return pp.AccountPool()
}

// ---- helpers ---------------------------------------------------------------

func extractModel(body []byte) string {
	var v struct {
		Model string `json:"model"`
	}
	_ = json.Unmarshal(body, &v)
	return v.Model
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// hopByHop headers must not be copied when relaying an upstream response. The
// last group also strips upstream identity/session headers that would leak the
// operator's account/infrastructure to the API caller.
var hopByHop = map[string]bool{
	"Connection": true, "Keep-Alive": true, "Proxy-Authenticate": true,
	"Proxy-Authorization": true, "Te": true, "Trailer": true,
	"Transfer-Encoding": true, "Upgrade": true, "Content-Length": true,
	// upstream account / infra leakage
	"Set-Cookie": true, "Openai-Organization": true, "Cf-Ray": true,
	"Cf-Cache-Status": true, "X-Request-Id": true, "Server": true,
}

// writeUpstream streams an upstream response back to the client (status +
// safe headers + body via io.Copy, flushing for SSE).
func writeUpstream(w http.ResponseWriter, resp *http.Response) {
	defer resp.Body.Close()
	for k, vs := range resp.Header {
		if hopByHop[http.CanonicalHeaderKey(k)] {
			continue
		}
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	flusher, _ := w.(http.Flusher)
	buf := make([]byte, 32*1024)
	for {
		n, err := resp.Body.Read(buf)
		if n > 0 {
			if _, werr := w.Write(buf[:n]); werr != nil {
				return
			}
			if flusher != nil {
				flusher.Flush()
			}
		}
		if err != nil {
			return
		}
	}
}

// writeError emits a surface-aware error (OpenAI vs Anthropic shape).
func (s *Server) writeError(w http.ResponseWriter, r *http.Request, status int, kind, message string) {
	var payload map[string]any
	if strings.Contains(r.URL.Path, "/messages") {
		payload = map[string]any{"type": "error", "error": map[string]any{"type": kind, "message": message}}
	} else {
		payload = map[string]any{"error": map[string]any{"message": message, "type": kind}}
	}
	writeJSON(w, status, payload)
}
