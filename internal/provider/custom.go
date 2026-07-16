package provider

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"apiforge/internal/config"
	"apiforge/internal/provider/openaicompat"
	"apiforge/internal/util/ssrf"
)

// stringList accepts either a JSON array of strings or a comma-separated string.
type stringList []string

func (s *stringList) UnmarshalJSON(b []byte) error {
	var arr []string
	if json.Unmarshal(b, &arr) == nil {
		*s = arr
		return nil
	}
	var str string
	if json.Unmarshal(b, &str) == nil {
		*s = parseList(str)
		return nil
	}
	return fmt.Errorf("apiKeys must be a string or string array")
}

// customSpec is one user-defined relay endpoint (中转站).
type customSpec struct {
	ID         string            `json:"id"`
	BaseURL    string            `json:"baseUrl"`
	Models     []string          `json:"models"`
	OwnedBy    string            `json:"ownedBy"`
	APIKeys    stringList        `json:"apiKeys"`
	KeysEnv    string            `json:"keysEnv"`
	KeyFile    string            `json:"keyFile"`
	AuthHeader string            `json:"authHeader"`
	Headers    map[string]string `json:"headers"`
}

// buildCustomProviders builds providers for every valid, key-bearing custom spec
// from CUSTOM_PROVIDERS (inline JSON array) and/or CUSTOM_PROVIDERS_FILE.
func buildCustomProviders(cfg config.Config, log *slog.Logger) []*openaicompat.Provider {
	var out []*openaicompat.Provider
	sticky := time.Duration(cfg.StickyTTLSeconds) * time.Second
	root := keyfileRoot()

	for _, spec := range loadCustomSpecs(log) {
		if err := validateCustom(spec); err != nil {
			log.Warn("skipping custom provider", "id", spec.ID, "err", err)
			continue
		}
		keys, err := resolveCustomKeys(spec, root)
		if err != nil {
			log.Warn("skipping custom provider (keys)", "id", spec.ID, "err", err)
			continue
		}
		if len(keys) == 0 {
			log.Warn("skipping custom provider (no keys)", "id", spec.ID)
			continue
		}
		ownedBy := spec.OwnedBy
		if ownedBy == "" {
			ownedBy = spec.ID
		}
		out = append(out, openaicompat.New(openaicompat.Options{
			ID: spec.ID, BaseURL: spec.BaseURL, OwnedBy: ownedBy, Models: spec.Models,
			APIKeys: keys, ExtraHeaders: spec.Headers, AuthHeader: spec.AuthHeader,
			Concurrency: cfg.MaxAccountConcurrency, StickyTTL: sticky, Log: log,
		}))
	}
	return out
}

func loadCustomSpecs(log *slog.Logger) []customSpec {
	var specs []customSpec
	sources := map[string]string{"CUSTOM_PROVIDERS": os.Getenv("CUSTOM_PROVIDERS")}
	if f := os.Getenv("CUSTOM_PROVIDERS_FILE"); f != "" {
		if b, err := os.ReadFile(f); err == nil {
			sources["CUSTOM_PROVIDERS_FILE"] = string(b)
		} else {
			log.Warn("cannot read CUSTOM_PROVIDERS_FILE", "path", f, "err", err)
		}
	}
	for src, raw := range sources {
		if raw == "" {
			continue
		}
		var parsed []customSpec
		if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
			log.Warn("invalid JSON", "src", src, "err", err)
			continue
		}
		specs = append(specs, parsed...)
	}
	return specs
}

func validateCustom(spec customSpec) error {
	if spec.ID == "" {
		return fmt.Errorf("missing id")
	}
	if !strings.HasPrefix(spec.BaseURL, "http://") && !strings.HasPrefix(spec.BaseURL, "https://") {
		return fmt.Errorf("invalid baseUrl")
	}
	if len(spec.Models) == 0 {
		return fmt.Errorf("needs models")
	}
	return ssrf.AssertPublicURL(spec.BaseURL, "custom "+spec.ID)
}

func resolveCustomKeys(spec customSpec, root string) ([]string, error) {
	if len(spec.APIKeys) > 0 {
		return spec.APIKeys, nil
	}
	if spec.KeysEnv != "" {
		return parseList(os.Getenv(spec.KeysEnv)), nil
	}
	if spec.KeyFile != "" {
		if err := assertAllowedKeyFile(spec.KeyFile, root); err != nil {
			return nil, err
		}
		b, err := os.ReadFile(spec.KeyFile)
		if err != nil {
			return nil, err
		}
		token := strings.TrimSpace(string(b))
		if token == "" {
			return nil, nil
		}
		return []string{token}, nil
	}
	return nil, nil
}

func allowAnyKeyFile() bool {
	switch strings.ToLower(os.Getenv("ALLOW_ANY_KEYFILE")) {
	case "1", "true", "yes":
		return true
	default:
		return false
	}
}

func keyfileRoot() string {
	base := os.Getenv("CREDS_ROOT")
	if base == "" {
		if h, err := os.UserHomeDir(); err == nil {
			base = h
		}
	}
	if real, err := filepath.EvalSymlinks(base); err == nil {
		return real
	}
	return base
}

// assertAllowedKeyFile keeps a keyFile under the allowed creds root (fail-closed
// against path traversal); ALLOW_ANY_KEYFILE=1 disables the check.
func assertAllowedKeyFile(path, root string) error {
	if allowAnyKeyFile() {
		return nil
	}
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		return fmt.Errorf("keyFile %s cannot be resolved (must exist under %s)", path, root)
	}
	if real != root && !strings.HasPrefix(real, root+string(filepath.Separator)) {
		return fmt.Errorf("keyFile %s is outside allowed root %s (set ALLOW_ANY_KEYFILE=1 or CREDS_ROOT)", path, root)
	}
	return nil
}
