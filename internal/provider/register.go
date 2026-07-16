// Package provider builds and registers every enabled provider: vendor
// OpenAI-compatible endpoints (activated by API keys), user-defined custom
// relays, and — in later phases — the CLI-login providers (codex/claude/...).
package provider

import (
	"log/slog"
	"os"
	"strings"
	"time"

	"apiforge/internal/config"
	"apiforge/internal/provider/claude"
	"apiforge/internal/provider/codex"
	"apiforge/internal/provider/copilot"
	"apiforge/internal/provider/cursor"
	"apiforge/internal/provider/gemini"
	"apiforge/internal/provider/openaicompat"
	"apiforge/internal/provider/qwen"
	"apiforge/internal/registry"
	"apiforge/internal/util/ssrf"
)

// RegisterAll registers all enabled providers into reg.
func RegisterAll(reg *registry.Registry, cfg config.Config, log *slog.Logger) {
	sticky := time.Duration(cfg.StickyTTLSeconds) * time.Second

	vendorCount := 0
	for _, v := range vendors {
		keys := parseList(os.Getenv(v.KeysEnv))
		if len(keys) == 0 {
			continue
		}
		baseURL := v.BaseURL
		if v.BaseURLEnv != "" {
			if o := os.Getenv(v.BaseURLEnv); o != "" {
				baseURL = o
			}
		}
		if err := ssrf.AssertPublicURL(baseURL, v.ID); err != nil {
			log.Warn("skipping vendor (bad base url)", "id", v.ID, "err", err)
			continue
		}
		models := v.DefaultModels
		if m := parseList(os.Getenv(v.ModelsEnv)); len(m) > 0 {
			models = m
		}
		reg.Register(openaicompat.New(openaicompat.Options{
			ID: v.ID, BaseURL: baseURL, OwnedBy: v.OwnedBy, Models: models,
			APIKeys: keys, Concurrency: cfg.MaxAccountConcurrency, StickyTTL: sticky, Log: log,
		}))
		vendorCount++
	}
	if vendorCount > 0 {
		log.Info("registered vendors", "count", vendorCount)
	}

	customs := buildCustomProviders(cfg, log)
	for _, p := range customs {
		reg.Register(p)
	}
	if len(customs) > 0 {
		log.Info("registered custom relays", "count", len(customs))
	}

	registerCodex(reg, cfg, log)
	registerClaude(reg, cfg, log)
	registerGemini(reg, cfg, log)
	registerQwen(reg, cfg, log)
	registerCopilot(reg, cfg, log)
	registerCursor(reg, cfg, log)
}

// registerCursor wires the EXPERIMENTAL cursor provider. The session token comes
// from CURSOR_ACCESS_TOKENS (list) or CURSOR_ACCESS_TOKEN (single); absence =
// disabled (a headless host has no Cursor state.vscdb to read).
func registerCursor(reg *registry.Registry, cfg config.Config, log *slog.Logger) {
	tokens := parseList(os.Getenv("CURSOR_ACCESS_TOKENS"))
	if single := os.Getenv("CURSOR_ACCESS_TOKEN"); single != "" {
		tokens = append(tokens, single)
	}
	if len(tokens) == 0 {
		return
	}
	if p := cursor.New(tokens, cursor.Config{
		MaxConcurrency: cfg.MaxAccountConcurrency,
		StickyTTL:      time.Duration(cfg.StickyTTLSeconds) * time.Second,
	}, log); p != nil {
		reg.Register(p)
		log.Info("registered cursor (experimental)", "accounts", p.Pool().Size())
	}
}

func registerQwen(reg *registry.Registry, cfg config.Config, log *slog.Logger) {
	pc, ok := cfg.Providers["qwen"]
	if !ok || !pc.Enabled {
		return
	}
	if p := qwen.New(pc.CredentialPaths, qwen.Config{
		MaxConcurrency: cfg.MaxAccountConcurrency,
		StickyTTL:      time.Duration(cfg.StickyTTLSeconds) * time.Second,
	}, log); p != nil {
		reg.Register(p)
		log.Info("registered qwen-cli", "accounts", p.Pool().Size())
	}
}

func registerCopilot(reg *registry.Registry, cfg config.Config, log *slog.Logger) {
	pc, ok := cfg.Providers["copilot"]
	if !ok || !pc.Enabled {
		return
	}
	// copilot's credential paths are config dirs; token discovery runs at Init.
	reg.Register(copilot.New(pc.CredentialPaths, copilot.Config{
		MaxConcurrency: cfg.MaxAccountConcurrency,
		StickyTTL:      time.Duration(cfg.StickyTTLSeconds) * time.Second,
	}, log))
}

// registerClaude wires the Claude provider from OAuth credential paths plus any
// ANTHROPIC_API_KEYS.
func registerClaude(reg *registry.Registry, cfg config.Config, log *slog.Logger) {
	pc, ok := cfg.Providers["claude"]
	if !ok || !pc.Enabled {
		return
	}
	p := claude.New(pc.CredentialPaths, parseList(os.Getenv("ANTHROPIC_API_KEYS")), claude.Config{
		MaxConcurrency: cfg.MaxAccountConcurrency,
		StickyTTL:      time.Duration(cfg.StickyTTLSeconds) * time.Second,
	}, log)
	if p != nil {
		reg.Register(p)
		log.Info("registered claude", "accounts", p.Pool().Size())
	}
}

// registerGemini wires the EXPERIMENTAL gemini-cli provider (Code Assist OAuth
// reuse). Opt-in via GEMINI_OAUTH_ENABLED to avoid surprising unverified routing.
func registerGemini(reg *registry.Registry, cfg config.Config, log *slog.Logger) {
	if !boolEnv("GEMINI_OAUTH_ENABLED") {
		return
	}
	pc, ok := cfg.Providers["gemini"]
	if !ok || !pc.Enabled {
		return
	}
	p := gemini.New(pc.CredentialPaths, gemini.Config{
		MaxConcurrency: cfg.MaxAccountConcurrency,
		StickyTTL:      time.Duration(cfg.StickyTTLSeconds) * time.Second,
	}, log)
	if p != nil {
		reg.Register(p)
		log.Info("registered gemini-cli (experimental)", "accounts", p.Pool().Size())
	}
}

func boolEnv(name string) bool {
	switch strings.ToLower(os.Getenv(name)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

// registerCodex wires the Codex provider from its CLI credential paths plus any
// OPENAI_API_KEYS. codex.New returns nil when no account is configured.
func registerCodex(reg *registry.Registry, cfg config.Config, log *slog.Logger) {
	pc, ok := cfg.Providers["codex"]
	if !ok || !pc.Enabled {
		return
	}
	p := codex.New(pc.CredentialPaths, parseList(os.Getenv("OPENAI_API_KEYS")), codex.Config{
		MaxConcurrency: cfg.MaxAccountConcurrency,
		StickyTTL:      time.Duration(cfg.StickyTTLSeconds) * time.Second,
	}, log)
	if p != nil {
		reg.Register(p)
		log.Info("registered codex", "accounts", p.Pool().Size())
	}
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
