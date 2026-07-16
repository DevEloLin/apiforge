package copilot

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"apiforge/internal/util/filestore"
	"apiforge/internal/util/httpx"

	"golang.org/x/sync/singleflight"
)

const (
	tokenExchangeURL    = "https://api.github.com/copilot_internal/v2/token"
	editorVersion       = "vscode/1.96.0"
	editorPluginVersion = "copilot-chat/0.23.0"
	copilotUserAgent    = "GitHubCopilotChat/0.23.0"
	refreshSkewMs       = 60_000
	defaultAPIBase      = "https://api.githubcopilot.com"
)

type tokenState struct {
	token     string
	expiresMs int64
	apiBase   string
}

// creds manages one GitHub Copilot account: it exchanges a long-lived GitHub
// OAuth token for the short-lived Copilot API token and caches it until near
// expiry. The exchange is single-flighted across concurrent callers.
type creds struct {
	githubToken string
	log         *slog.Logger
	sf          singleflight.Group
	state       atomic.Pointer[tokenState]
}

func newCreds(githubToken string, log *slog.Logger) *creds {
	return &creds{githubToken: githubToken, log: log}
}

func (c *creds) apiBaseURL() string {
	if s := c.state.Load(); s != nil && s.apiBase != "" {
		return s.apiBase
	}
	return defaultAPIBase
}

func (c *creds) copilotToken(ctx context.Context) (string, error) {
	if s := c.state.Load(); s != nil && time.Now().UnixMilli() < s.expiresMs-refreshSkewMs {
		return s.token, nil
	}
	v, err, _ := c.sf.Do("exchange", func() (any, error) {
		if s := c.state.Load(); s != nil && time.Now().UnixMilli() < s.expiresMs-refreshSkewMs {
			return s.token, nil
		}
		return c.exchange(ctx)
	})
	if err != nil {
		return "", err
	}
	return v.(string), nil
}

type exchangeResponse struct {
	Token     string `json:"token"`
	ExpiresAt int64  `json:"expires_at"` // epoch seconds
	Endpoints struct {
		API string `json:"api"`
	} `json:"endpoints"`
}

func (c *creds) exchange(ctx context.Context) (string, error) {
	if c.log != nil {
		c.log.Info("exchanging GitHub token for Copilot token")
	}
	rctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(rctx, http.MethodGet, tokenExchangeURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "token "+c.githubToken)
	req.Header.Set("editor-version", editorVersion)
	req.Header.Set("editor-plugin-version", editorPluginVersion)
	req.Header.Set("User-Agent", copilotUserAgent)
	req.Header.Set("Accept", "application/json")
	res, err := httpx.Client.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		if c.log != nil {
			c.log.Warn("Copilot token exchange failed", "status", res.StatusCode)
		}
		return "", fmt.Errorf("copilot token exchange failed: %d", res.StatusCode)
	}
	var data exchangeResponse
	if err := json.NewDecoder(res.Body).Decode(&data); err != nil {
		return "", err
	}
	apiBase := defaultAPIBase
	if data.Endpoints.API != "" {
		apiBase = data.Endpoints.API
	}
	// Defensive: if the response omits expires_at, don't leave expiresMs=0 (which
	// would disable the freshness check and force a token exchange on every
	// request, self-inflicting GitHub rate limits). Fall back to a short TTL.
	expiresMs := data.ExpiresAt * 1000
	if data.ExpiresAt <= 0 {
		expiresMs = time.Now().Add(25 * time.Minute).UnixMilli()
	}
	c.state.Store(&tokenState{token: data.Token, expiresMs: expiresMs, apiBase: apiBase})
	return data.Token, nil
}

// discoverGithubTokens finds GitHub OAuth tokens in the copilot editor config
// dir(s) (apps.json / hosts.json) plus COPILOT_GITHUB_TOKENS. Each is one account.
func discoverGithubTokens(configDirs []string) []string {
	seen := map[string]bool{}
	var out []string
	add := func(t string) {
		if t = strings.TrimSpace(t); t != "" && !seen[t] {
			seen[t] = true
			out = append(out, t)
		}
	}
	for _, dir := range configDirs {
		for _, file := range []string{"apps.json", "hosts.json"} {
			var data map[string]struct {
				OAuthToken string `json:"oauth_token"`
			}
			if filestore.ReadJSON(filepath.Join(dir, file), &data) != nil {
				continue
			}
			for _, entry := range data {
				add(entry.OAuthToken)
			}
		}
	}
	for _, t := range strings.Split(os.Getenv("COPILOT_GITHUB_TOKENS"), ",") {
		add(t)
	}
	return out
}
