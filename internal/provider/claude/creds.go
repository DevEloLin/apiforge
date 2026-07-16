package claude

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
)

// Claude Code OAuth (public client id the CLI itself uses).
const (
	oauthClientID = "9d1c250a-e61b-44d9-88ed-5944d1962f5e"
	oauthTokenURL = "https://console.anthropic.com/v1/oauth/token"
	refreshSkewMs = 60_000
)

type oauthState struct {
	AccessToken      string   `json:"accessToken"`
	RefreshToken     string   `json:"refreshToken"`
	ExpiresAt        int64    `json:"expiresAt"` // epoch ms
	Scopes           []string `json:"scopes,omitempty"`
	SubscriptionType string   `json:"subscriptionType,omitempty"`
	RateLimitTier    string   `json:"rateLimitTier,omitempty"`
}

// creds reads and refreshes one Claude Code OAuth login, persisting rotations so
// the CLI and gateway stay in sync. Auth state is held in an atomic pointer for
// race-free reads against a concurrent refresh.
type creds struct {
	path  string
	log   *slog.Logger
	mgr   *token.Manager
	state atomic.Pointer[oauthState]
	extra atomic.Pointer[map[string]json.RawMessage]
}

func newCreds(path string, log *slog.Logger) *creds {
	c := &creds{path: path, log: log}
	c.mgr = token.New(c)
	return c
}

func (c *creds) AccessToken(ctx context.Context) (string, error) { return c.mgr.AccessToken(ctx) }

// ---- token.Source ----------------------------------------------------------

func (c *creds) Read(_ context.Context) error {
	var raw map[string]json.RawMessage
	if err := filestore.ReadJSON(c.path, &raw); err != nil {
		return fmt.Errorf("cannot read Claude credentials at %s (is Claude Code logged in / mounted?): %w", c.path, err)
	}
	var st oauthState
	if v, ok := raw["claudeAiOauth"]; !ok || json.Unmarshal(v, &st) != nil || st.AccessToken == "" {
		return fmt.Errorf("claude credentials file is missing claudeAiOauth.accessToken")
	}
	extra := map[string]json.RawMessage{}
	for k, v := range raw {
		if k != "claudeAiOauth" {
			extra[k] = v
		}
	}
	c.state.Store(&st)
	c.extra.Store(&extra)
	return nil
}

func (c *creds) Token() string {
	if s := c.state.Load(); s != nil {
		return s.AccessToken
	}
	return ""
}

func (c *creds) Fresh() bool {
	s := c.state.Load()
	if s == nil {
		return false
	}
	return time.Now().UnixMilli() < s.ExpiresAt-refreshSkewMs
}

type refreshResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int64  `json:"expires_in"` // seconds
}

func (c *creds) Refresh(ctx context.Context) (string, error) {
	cur := c.state.Load()
	if cur == nil {
		return "", fmt.Errorf("claude: refresh before load")
	}
	if c.log != nil {
		c.log.Info("refreshing Claude OAuth token")
	}
	reqBody, _ := json.Marshal(map[string]string{
		"grant_type":    "refresh_token",
		"refresh_token": cur.RefreshToken,
		"client_id":     oauthClientID,
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
			c.log.Warn("Claude token refresh failed", "status", res.StatusCode)
		}
		return "", fmt.Errorf("claude token refresh failed: %d", res.StatusCode)
	}
	var data refreshResponse
	if err := json.NewDecoder(res.Body).Decode(&data); err != nil {
		return "", err
	}

	next := *cur
	next.AccessToken = data.AccessToken
	if data.RefreshToken != "" {
		next.RefreshToken = data.RefreshToken
	}
	next.ExpiresAt = time.Now().UnixMilli() + data.ExpiresIn*1000
	if err := c.persist(&next); err != nil && c.log != nil {
		c.log.Warn("could not persist refreshed Claude token", "err", err)
	}
	c.state.Store(&next)
	return next.AccessToken, nil
}

func (c *creds) persist(st *oauthState) error {
	out := map[string]json.RawMessage{}
	if e := c.extra.Load(); e != nil {
		for k, v := range *e {
			out[k] = v
		}
	}
	b, err := json.Marshal(st)
	if err != nil {
		return err
	}
	out["claudeAiOauth"] = b
	return filestore.WriteJSONAtomic(c.path, out)
}
