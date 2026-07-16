package qwen

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync/atomic"
	"time"

	"apiforge/internal/token"
	"apiforge/internal/util/filestore"
	"apiforge/internal/util/httpx"
	"apiforge/internal/util/ssrf"
)

const (
	oauthClientID = "f0304373b74a44d2b584a3fb70ca9e56"
	oauthTokenURL = "https://chat.qwen.ai/api/v1/oauth2/token"
	defaultBase   = "https://dashscope.aliyuncs.com/compatible-mode/v1"
	refreshSkewMs = 30_000
)

var (
	hasScheme = regexp.MustCompile(`^https?://`)
	hasV1     = regexp.MustCompile(`/v1/?$`)
)

type credState struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	IDToken      string `json:"id_token,omitempty"`
	TokenType    string `json:"token_type,omitempty"`
	ResourceURL  string `json:"resource_url,omitempty"`
	ExpiryDate   int64  `json:"expiry_date"` // epoch ms
}

// creds reads and refreshes one Qwen Code CLI OAuth login. The upstream is
// OpenAI-compatible; the base URL is derived per-account from resource_url.
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

// BaseURL derives the OpenAI-compatible endpoint from the account's resource_url.
func (c *creds) BaseURL() string {
	s := c.state.Load()
	if s == nil {
		return defaultBase
	}
	return deriveEndpoint(s.ResourceURL)
}

// deriveEndpoint normalizes a resource_url into an OpenAI-compatible base URL.
func deriveEndpoint(resourceURL string) string {
	if resourceURL == "" {
		return defaultBase
	}
	u := resourceURL
	if !hasScheme.MatchString(u) {
		u = "https://" + u
	}
	if !hasV1.MatchString(u) {
		u = strings.TrimRight(u, "/") + "/v1"
	}
	return strings.TrimRight(u, "/")
}

func (c *creds) Read(_ context.Context) error {
	var st credState
	if err := filestore.ReadJSON(c.path, &st); err != nil {
		return fmt.Errorf("cannot read Qwen credentials at %s (is Qwen Code logged in?): %w", c.path, err)
	}
	if st.AccessToken == "" {
		return fmt.Errorf("qwen creds missing access_token")
	}
	c.sanitizeResourceURL(&st)
	c.state.Store(&st)
	return nil
}

// sanitizeResourceURL drops a resource_url that resolves to a non-public address
// so the derived per-account base URL can't be steered at an internal host.
func (c *creds) sanitizeResourceURL(st *credState) {
	if st.ResourceURL == "" {
		return
	}
	if err := ssrf.AssertPublicURL(deriveEndpoint(st.ResourceURL), "qwen resource_url"); err != nil {
		if c.log != nil {
			c.log.Warn("ignoring non-public qwen resource_url", "err", err)
		}
		st.ResourceURL = ""
	}
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
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int64  `json:"expires_in"`
	ResourceURL  string `json:"resource_url"`
}

func (c *creds) Refresh(ctx context.Context) (string, error) {
	cur := c.state.Load()
	if cur == nil {
		return "", fmt.Errorf("qwen: refresh before load")
	}
	if c.log != nil {
		c.log.Info("refreshing Qwen OAuth token")
	}
	form := url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {cur.RefreshToken},
		"client_id":     {oauthClientID},
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
			c.log.Warn("Qwen token refresh failed", "status", res.StatusCode)
		}
		return "", fmt.Errorf("qwen token refresh failed: %d", res.StatusCode)
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
	if data.ResourceURL != "" {
		next.ResourceURL = data.ResourceURL
	}
	// Guard against a missing expires_in (0) which would set ExpiryDate=now and
	// force a token refresh on every request.
	expiresIn := data.ExpiresIn
	if expiresIn <= 0 {
		expiresIn = 3600
	}
	next.ExpiryDate = time.Now().UnixMilli() + expiresIn*1000
	c.sanitizeResourceURL(&next)
	if err := filestore.WriteJSONAtomic(c.path, &next); err != nil && c.log != nil {
		c.log.Warn("could not persist refreshed Qwen token", "err", err)
	}
	c.state.Store(&next)
	return next.AccessToken, nil
}
