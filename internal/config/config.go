// Package config loads runtime configuration from environment variables so the
// same image can be reconfigured at `docker run` time without a rebuild.
package config

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Config is the resolved runtime configuration.
type Config struct {
	Host              string
	Port              int
	LogLevel          string
	APIKeys           []string // client-facing sk list; empty => auth disabled (dev only)
	AdminToken        string   // guards /admin/*; empty => admin disabled
	UpstreamTimeoutMs int
	MaxBodyBytes      int64 // 0 = unlimited
	RateLimitRPM      int   // per client key; 0 = disabled

	// sub2api-inspired pool tuning (applied per provider).
	MaxAccountConcurrency int // per account; 0 = unlimited
	StickyTTLSeconds      int // 0 disables sticky sessions

	// Per-provider credential locations (files/dirs). Multiple => account pool.
	Providers map[string]ProviderConfig
}

// ProviderConfig holds one provider's enablement + credential locations.
type ProviderConfig struct {
	Enabled         bool
	CredentialPaths []string
}

func home() string {
	if h, err := os.UserHomeDir(); err == nil {
		return h
	}
	return os.Getenv("HOME")
}

func configHome() string {
	if v := os.Getenv("XDG_CONFIG_HOME"); v != "" {
		return v
	}
	return filepath.Join(home(), ".config")
}

func envStr(name, def string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return def
}

func envInt(name string, def int) int {
	if v := os.Getenv(name); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

func envInt64(name string, def int64) int64 {
	if v := os.Getenv(name); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			return n
		}
	}
	return def
}

func envBool(name string, def bool) bool {
	v := strings.ToLower(os.Getenv(name))
	if v == "" {
		return def
	}
	switch v {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func envList(name string) []string {
	v := os.Getenv(name)
	if v == "" {
		return nil
	}
	var out []string
	for _, s := range strings.Split(v, ",") {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return uniq(out)
}

func uniq(in []string) []string {
	seen := map[string]bool{}
	out := in[:0]
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

// selectExisting keeps candidates that exist (order-preserving, de-duped); if
// none exist, returns the first candidate so the provider still initialises and
// reports a clean "not logged in" error instead of vanishing.
func selectExisting(candidates []string) []string {
	u := uniq(append([]string(nil), candidates...))
	var found []string
	for _, p := range u {
		if _, err := os.Stat(p); err == nil {
			found = append(found, p)
		}
	}
	if len(found) > 0 {
		return found
	}
	if len(u) > 0 {
		return u[:1]
	}
	return nil
}

// resolveCreds resolves credential locations: <NAME>S list env → <NAME> single
// env → auto-detect existing candidates.
func resolveCreds(listEnv, singleEnv string, candidates []string) []string {
	if list := envList(listEnv); len(list) > 0 {
		return list
	}
	if single := os.Getenv(singleEnv); single != "" {
		return []string{single}
	}
	return selectExisting(candidates)
}

// Load resolves the full configuration from the environment.
func Load() Config {
	h, ch := home(), configHome()

	candidates := map[string][]string{
		"codex":   {filepath.Join(h, ".codex", "auth.json"), filepath.Join(ch, "codex", "auth.json")},
		"claude":  {filepath.Join(h, ".claude", ".credentials.json"), filepath.Join(ch, "claude", ".credentials.json")},
		"copilot": {filepath.Join(ch, "github-copilot"), filepath.Join(h, ".config", "github-copilot")},
		"cursor":  {filepath.Join(h, "Library", "Application Support", "Cursor", "User", "globalStorage", "state.vscdb"), filepath.Join(ch, "Cursor", "User", "globalStorage", "state.vscdb")},
		"qwen":    {filepath.Join(h, ".qwen", "oauth_creds.json"), filepath.Join(ch, "qwen", "oauth_creds.json")},
		"gemini":  {filepath.Join(h, ".gemini", "oauth_creds.json"), filepath.Join(ch, "gemini", "oauth_creds.json")},
	}

	providers := map[string]ProviderConfig{}
	for _, name := range []string{"codex", "claude", "copilot", "cursor", "qwen", "gemini"} {
		up := strings.ToUpper(name)
		paths := resolveCreds(up+"_AUTHS", up+"_AUTH", candidates[name])
		providers[name] = ProviderConfig{
			// enabled unless explicitly disabled; auto-detect drives real readiness at init.
			Enabled:         envBool(up+"_ENABLED", true),
			CredentialPaths: paths,
		}
	}

	return Config{
		Host:              envStr("HOST", "127.0.0.1"),
		Port:              envInt("PORT", 8899),
		LogLevel:          envStr("LOG_LEVEL", "info"),
		APIKeys:           envList("API_KEYS"),
		AdminToken:        os.Getenv("ADMIN_TOKEN"),
		UpstreamTimeoutMs: envInt("UPSTREAM_TIMEOUT_MS", 600000),
		MaxBodyBytes:      envInt64("MAX_BODY_BYTES", 10<<20),
		RateLimitRPM:      envInt("RATE_LIMIT_RPM", 0),
		// Default 3: protects subscription accounts from bursts (queueing absorbs
		// the overflow via QUEUE_WAIT_MS). Set 0 to disable the cap entirely.
		MaxAccountConcurrency: envInt("MAX_ACCOUNT_CONCURRENCY", 3),
		StickyTTLSeconds:      envInt("STICKY_TTL_SECONDS", 0),
		Providers:             providers,
	}
}

// IsLoopback reports whether host is a loopback bind.
func IsLoopback(host string) bool {
	switch host {
	case "127.0.0.1", "::1", "localhost", "0.0.0.0":
		return host != "0.0.0.0"
	default:
		return false
	}
}
