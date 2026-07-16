package codex

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sync/atomic"
	"time"

	"apiforge/internal/token"
	"apiforge/internal/util/filestore"
	"apiforge/internal/util/httpx"
	"apiforge/internal/util/jwtx"
)

// Codex CLI ChatGPT OAuth (public client id — same values the CLI ships with).
const (
	oauthClientID = "app_EMoamEEZ73f0CkXaXp7hrann"
	oauthTokenURL = "https://auth.openai.com/oauth/token"
	refreshSkewMs = 5 * 60_000 // refresh when <5 min to expiry
)

type codexTokens struct {
	IDToken      string `json:"id_token,omitempty"`
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	AccountID    string `json:"account_id,omitempty"`
}

type authFile struct {
	AuthMode     string      `json:"auth_mode,omitempty"`
	OpenAIAPIKey *string     `json:"OPENAI_API_KEY,omitempty"`
	Tokens       codexTokens `json:"tokens"`
	LastRefresh  string      `json:"last_refresh,omitempty"`
	// preserve unknown fields on write-back
	extra map[string]json.RawMessage
}

// creds reads and refreshes one Codex CLI login, persisting rotated tokens.
// The current auth state is held in an atomic pointer so Token/Fresh/AccountID
// stay race-free against a concurrent Refresh (token.Manager calls them without
// holding a shared lock).
type creds struct {
	path string
	log  *slog.Logger
	mgr  *token.Manager
	auth atomic.Pointer[authFile]
}

func newCreds(path string, log *slog.Logger) *creds {
	c := &creds{path: path, log: log}
	c.mgr = token.New(c)
	return c
}

// AccessToken returns a valid token, refreshing at most once across callers.
func (c *creds) AccessToken(ctx context.Context) (string, error) { return c.mgr.AccessToken(ctx) }

// ---- token.Source ----------------------------------------------------------

func (c *creds) Read(_ context.Context) error {
	b, err := readRaw(c.path)
	if err != nil {
		return fmt.Errorf("cannot read Codex credentials at %s (is Codex logged in / mounted?): %w", c.path, err)
	}
	if b.Tokens.AccessToken == "" {
		return fmt.Errorf("codex auth.json is missing tokens.access_token")
	}
	c.auth.Store(b)
	return nil
}

func (c *creds) Token() string {
	if a := c.auth.Load(); a != nil {
		return a.Tokens.AccessToken
	}
	return ""
}

func (c *creds) Fresh() bool {
	ms, ok := jwtx.ExpiryMs(c.Token())
	if !ok {
		return true // opaque/non-expiring token — treat as fresh, let 401 drive refresh
	}
	return time.Now().UnixMilli() < ms-refreshSkewMs
}

// AccountID resolves the ChatGPT account id from the file or the JWT claims.
func (c *creds) AccountID() string {
	a := c.auth.Load()
	if a == nil {
		return ""
	}
	if a.Tokens.AccountID != "" {
		return a.Tokens.AccountID
	}
	claims := jwtx.DecodePayload(a.Tokens.AccessToken)
	if auth, ok := claims["https://api.openai.com/auth"].(map[string]any); ok {
		if id, ok := auth["chatgpt_account_id"].(string); ok {
			return id
		}
	}
	return ""
}

type refreshResponse struct {
	AccessToken  string `json:"access_token"`
	IDToken      string `json:"id_token"`
	RefreshToken string `json:"refresh_token"`
}

func (c *creds) Refresh(ctx context.Context) (string, error) {
	cur := c.auth.Load()
	if cur == nil {
		return "", fmt.Errorf("codex: refresh before load")
	}
	if c.log != nil {
		c.log.Info("refreshing Codex OAuth token", "path", "<path>")
	}
	reqBody, _ := json.Marshal(map[string]string{
		"client_id":     oauthClientID,
		"grant_type":    "refresh_token",
		"refresh_token": cur.Tokens.RefreshToken,
	})
	rctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(rctx, http.MethodPost, oauthTokenURL, bytes.NewReader(reqBody))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := httpx.Client.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		if c.log != nil {
			c.log.Warn("Codex token refresh failed", "status", res.StatusCode)
		}
		return "", fmt.Errorf("codex token refresh failed: %d", res.StatusCode)
	}
	var data refreshResponse
	if err := json.NewDecoder(res.Body).Decode(&data); err != nil {
		return "", err
	}

	// Immutable update: build the next auth state, then persist + swap.
	next := *cur
	next.Tokens = cur.Tokens
	next.Tokens.AccessToken = data.AccessToken
	if data.IDToken != "" {
		next.Tokens.IDToken = data.IDToken
	}
	if data.RefreshToken != "" {
		next.Tokens.RefreshToken = data.RefreshToken
	}
	next.LastRefresh = time.Now().UTC().Format(time.RFC3339)
	if err := writeRaw(c.path, &next); err != nil && c.log != nil {
		c.log.Warn("could not persist refreshed Codex token", "err", err)
	}
	c.auth.Store(&next)
	return next.Tokens.AccessToken, nil
}

// readRaw parses auth.json while preserving unknown top-level fields so a
// write-back never drops data the CLI cares about.
func readRaw(path string) (*authFile, error) {
	var raw map[string]json.RawMessage
	if err := filestore.ReadJSON(path, &raw); err != nil {
		return nil, err
	}
	af := &authFile{extra: map[string]json.RawMessage{}}
	for k, v := range raw {
		switch k {
		case "auth_mode":
			_ = json.Unmarshal(v, &af.AuthMode)
		case "OPENAI_API_KEY":
			_ = json.Unmarshal(v, &af.OpenAIAPIKey)
		case "tokens":
			_ = json.Unmarshal(v, &af.Tokens)
		case "last_refresh":
			_ = json.Unmarshal(v, &af.LastRefresh)
		default:
			af.extra[k] = v
		}
	}
	return af, nil
}

func writeRaw(path string, af *authFile) error {
	out := map[string]json.RawMessage{}
	for k, v := range af.extra {
		out[k] = v
	}
	set := func(k string, v any) {
		if b, err := json.Marshal(v); err == nil {
			out[k] = b
		}
	}
	if af.AuthMode != "" {
		set("auth_mode", af.AuthMode)
	}
	if af.OpenAIAPIKey != nil {
		set("OPENAI_API_KEY", af.OpenAIAPIKey)
	}
	set("tokens", af.Tokens)
	set("last_refresh", af.LastRefresh)
	return filestore.WriteJSONAtomic(path, out)
}
