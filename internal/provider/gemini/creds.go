package gemini

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"time"

	"apiforge/internal/token"
	"apiforge/internal/util/filestore"
	"apiforge/internal/util/httpx"
)

// Public OAuth client the Gemini CLI ships with (embedded in its source).
const (
	oauthClientID     = "REDACTED_GEMINI_CLIENT_ID"
	oauthClientSecret = "REDACTED_GEMINI_SECRET_B64="
	oauthTokenURL     = "https://oauth2.googleapis.com/token"
	refreshSkewMs     = 60_000
)

type credState struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	Scope        string `json:"scope,omitempty"`
	TokenType    string `json:"token_type,omitempty"`
	IDToken      string `json:"id_token,omitempty"`
	ExpiryDate   int64  `json:"expiry_date"` // epoch ms
}

// creds reads and refreshes one Gemini CLI Google OAuth login, persisting
// rotations. Google does not return a new refresh_token on refresh.
type creds struct {
	path  string
	log   *slog.Logger
	mgr   *token.Manager
	state atomic.Pointer[credState]
}

func newCreds(path string, log *slog.Logger) *creds {
	c := &creds{path: path, log: log}
	c.mgr = token.New(c)
	return c
}

func (c *creds) AccessToken(ctx context.Context) (string, error) { return c.mgr.AccessToken(ctx) }

func (c *creds) Read(_ context.Context) error {
	var st credState
	if err := filestore.ReadJSON(c.path, &st); err != nil {
		return fmt.Errorf("cannot read Gemini credentials at %s (is Gemini CLI logged in?): %w", c.path, err)
	}
	if st.AccessToken == "" {
		return fmt.Errorf("gemini creds missing access_token")
	}
	c.state.Store(&st)
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
	return time.Now().UnixMilli() < s.ExpiryDate-refreshSkewMs
}

type refreshResponse struct {
	AccessToken string `json:"access_token"`
	ExpiresIn   int64  `json:"expires_in"` // seconds
	IDToken     string `json:"id_token"`
}

func (c *creds) Refresh(ctx context.Context) (string, error) {
	cur := c.state.Load()
	if cur == nil {
		return "", fmt.Errorf("gemini: refresh before load")
	}
	if c.log != nil {
		c.log.Info("refreshing Gemini OAuth token")
	}
	form := url.Values{
		"grant_type":    {"refresh_token"},
		"client_id":     {oauthClientID},
		"client_secret": {oauthClientSecret},
		"refresh_token": {cur.RefreshToken},
	}
	rctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(rctx, http.MethodPost, oauthTokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	res, err := httpx.Client.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		if c.log != nil {
			c.log.Warn("Gemini token refresh failed", "status", res.StatusCode)
		}
		return "", fmt.Errorf("gemini token refresh failed: %d", res.StatusCode)
	}
	var data refreshResponse
	if err := json.NewDecoder(res.Body).Decode(&data); err != nil {
		return "", err
	}
	next := *cur
	next.AccessToken = data.AccessToken
	if data.IDToken != "" {
		next.IDToken = data.IDToken
	}
	next.ExpiryDate = time.Now().UnixMilli() + data.ExpiresIn*1000
	if err := filestore.WriteJSONAtomic(c.path, &next); err != nil && c.log != nil {
		c.log.Warn("could not persist refreshed Gemini token", "err", err)
	}
	c.state.Store(&next)
	return next.AccessToken, nil
}
