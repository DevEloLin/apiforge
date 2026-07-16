package claude

import (
	"bytes"
	"context"
	"net/http"
	"os"
	"strings"

	"apiforge/internal/relay"
	"apiforge/internal/util/httpx"
)

const anthropicVersion = "2023-06-01"

// OAuth (subscription) mode: the oauth beta flag makes a Bearer login token
// accepted; the UA mimics the genuine Claude Code CLI (no proxy leak).
var oauthBeta = strings.Join([]string{
	"oauth-2025-04-20",
	"claude-code-20250219",
	"interleaved-thinking-2025-05-14",
	"fine-grained-tool-streaming-2025-05-14",
}, ",")

func apiBase() string {
	if v := os.Getenv("ANTHROPIC_BASE_URL"); v != "" {
		return strings.TrimRight(v, "/")
	}
	return "https://api.anthropic.com"
}

func cliUserAgent() string {
	if v := os.Getenv("CLAUDE_USER_AGENT"); v != "" {
		return v
	}
	return "claude-cli/2.1.119 (external, cli)"
}

// auth is either a reused Claude Code OAuth login or a plain Anthropic API key.
type auth struct {
	kind  string // "oauth" | "key"
	creds *creds
	key   string
}

// anthropicFetch calls the Anthropic API. OAuth mode sends Authorization: Bearer
// + the oauth beta flag + CLI fingerprint (no x-api-key); API-key mode sends the
// standard x-api-key. Nothing from the client request is forwarded, so upstream
// sees a clean, genuine-looking call. A token-refresh failure becomes a synthetic
// 401 so the account-retry layer cools the account down.
func anthropicFetch(ctx context.Context, a auth, path string, body []byte) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiBase()+path, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("anthropic-version", anthropicVersion)
	if a.kind == "oauth" {
		tok, err := a.creds.AccessToken(ctx)
		if err != nil {
			return relay.SynthStatus(http.StatusUnauthorized, "claude token refresh failed"), nil
		}
		req.Header.Set("Authorization", "Bearer "+tok)
		req.Header.Set("anthropic-beta", oauthBeta)
		req.Header.Set("User-Agent", cliUserAgent())
		req.Header.Set("x-app", "cli")
	} else {
		req.Header.Set("x-api-key", a.key)
	}
	return httpx.Client.Do(req)
}
