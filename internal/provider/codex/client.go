package codex

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"

	"apiforge/internal/util/httpx"
)

const (
	codexURL   = "https://chatgpt.com/backend-api/codex/responses"
	originator = "codex_cli_rs"
)

// The backend gates newer models on the client version; keep current or override
// with CODEX_CLIENT_VERSION to match the installed Codex CLI.
func codexVersion() string {
	if v := os.Getenv("CODEX_CLIENT_VERSION"); v != "" {
		return v
	}
	return "0.142.5"
}

// userAgent mimics the genuine Codex CLI UA (no proxy fingerprint).
func userAgent() string {
	if v := os.Getenv("CODEX_USER_AGENT"); v != "" {
		return v
	}
	return "codex_cli_rs/" + codexVersion() + " (Mac OS 15.5.0; arm64)"
}

// codexFetch POSTs a Responses-API body to the ChatGPT Codex backend using the
// account's ChatGPT OAuth token. The backend is stream-first, so the response is
// always SSE. A token-acquisition failure is surfaced as a synthetic 401 so the
// account-retry layer cools the account down (MarkAuthFailed) rather than
// treating it as a transient transport error.
func codexFetch(ctx context.Context, c *creds, body any, sessionID string) (*http.Response, error) {
	tok, err := c.AccessToken(ctx)
	if err != nil {
		// Surface the real error so the retry layer cools this account briefly
		// (or, on client cancellation, short-circuits) — never a 5-min auth blackout.
		return nil, fmt.Errorf("codex token refresh: %w", err)
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, codexURL, bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("originator", originator)
	req.Header.Set("session_id", sessionID)
	req.Header.Set("version", codexVersion())
	req.Header.Set("User-Agent", userAgent())
	if acct := c.AccountID(); acct != "" {
		req.Header.Set("chatgpt-account-id", acct)
	}
	return httpx.Client.Do(req)
}
