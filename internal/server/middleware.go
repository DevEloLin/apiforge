package server

import (
	"net/http"
	"strings"
	"sync"
	"time"
)

// bearer extracts the token from an Authorization: Bearer <token> header, or
// the x-api-key header (Anthropic style).
func bearer(r *http.Request) string {
	if h := r.Header.Get("Authorization"); h != "" {
		if after, ok := strings.CutPrefix(h, "Bearer "); ok {
			return strings.TrimSpace(after)
		}
	}
	return strings.TrimSpace(r.Header.Get("x-api-key"))
}

// authMiddleware gates /v1 behind the client API keys. An empty key set means
// auth is disabled (dev only; main() refuses this on a non-loopback bind).
func (s *Server) authMiddleware(next http.Handler) http.Handler {
	allow := map[string]bool{}
	for _, k := range s.cfg.APIKeys {
		allow[k] = true
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if len(allow) == 0 || allow[bearer(r)] {
			next.ServeHTTP(w, r)
			return
		}
		s.writeError(w, r, http.StatusUnauthorized, "invalid_request_error", "Invalid API key.")
	})
}

// adminMiddleware gates /admin behind ADMIN_TOKEN (empty => admin disabled).
func (s *Server) adminMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.cfg.AdminToken == "" {
			s.writeError(w, r, http.StatusForbidden, "invalid_request_error", "Admin API is disabled (ADMIN_TOKEN not set).")
			return
		}
		if bearer(r) != s.cfg.AdminToken {
			s.writeError(w, r, http.StatusUnauthorized, "invalid_request_error", "Invalid admin token.")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// bodyLimitMiddleware caps inbound request bodies (0 = unlimited).
func (s *Server) bodyLimitMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.cfg.MaxBodyBytes > 0 && r.Body != nil {
			r.Body = http.MaxBytesReader(w, r.Body, s.cfg.MaxBodyBytes)
		}
		next.ServeHTTP(w, r)
	})
}

// rateLimiter is a per-key fixed-window (1 min) request counter.
type rateLimiter struct {
	rpm    int
	mu     sync.Mutex
	counts map[string]int
	window time.Time
}

func newRateLimiter(rpm int) *rateLimiter {
	return &rateLimiter{rpm: rpm, counts: map[string]int{}, window: time.Now()}
}

func (rl *rateLimiter) allow(key string) bool {
	if rl.rpm <= 0 {
		return true
	}
	rl.mu.Lock()
	defer rl.mu.Unlock()
	if time.Since(rl.window) >= time.Minute {
		rl.counts = map[string]int{}
		rl.window = time.Now()
	}
	rl.counts[key]++
	return rl.counts[key] <= rl.rpm
}

func (s *Server) rateLimitMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := bearer(r)
		if key == "" {
			key = r.RemoteAddr
		}
		if !s.rl.allow(key) {
			s.writeError(w, r, http.StatusTooManyRequests, "rate_limit_error", "Too many requests.")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// chain applies middlewares in order (first listed = outermost).
func chain(h http.Handler, mws ...func(http.Handler) http.Handler) http.Handler {
	for i := len(mws) - 1; i >= 0; i-- {
		h = mws[i](h)
	}
	return h
}
