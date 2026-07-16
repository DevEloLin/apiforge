// Package server wires the HTTP surface: OpenAI (/v1/chat/completions,
// /v1/models, /v1/responses), Anthropic (/v1/messages), images, admin, health.
// Provider responses (real or synthesized) are streamed back with io.Copy so
// memory stays flat regardless of payload size.
package server

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"apiforge/internal/config"
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
	v1.HandleFunc("POST /v1/images/generations", s.images)
	mux.Handle("/v1/", chain(v1, s.authMiddleware, s.rateLimitMiddleware, s.bodyLimitMiddleware))

	admin := http.NewServeMux()
	admin.HandleFunc("GET /admin/accounts", s.adminAccounts)
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
			disabled = append(disabled, disabledP{ID: d.ID, Reason: sanitize.Path(d.Reason)})
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
		s.log.Error("provider error", "provider", p.ID(), "err", err)
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

func (s *Server) images(w http.ResponseWriter, r *http.Request) {
	s.dispatch(w, r, types.CapImages, func(p types.Provider, rc types.RequestContext, b []byte) (*http.Response, error) {
		return p.(types.ImagesProvider).Images(rc, b)
	})
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

func (s *Server) adminAccounts(w http.ResponseWriter, _ *http.Request) {
	// Phase 1 placeholder: per-provider account health is exposed here once
	// providers register their pools (Phase 2+).
	writeJSON(w, http.StatusOK, map[string]any{"accounts": []any{}, "note": "provider pools wired in later phases"})
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

// hopByHop headers must not be copied when relaying an upstream response.
var hopByHop = map[string]bool{
	"Connection": true, "Keep-Alive": true, "Proxy-Authenticate": true,
	"Proxy-Authorization": true, "Te": true, "Trailer": true,
	"Transfer-Encoding": true, "Upgrade": true, "Content-Length": true,
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
