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
	"apiforge/internal/provider/openaicompat"
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

	// Phase 3+: codex / claude / gemini / qwen / copilot / cursor registered here.
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
