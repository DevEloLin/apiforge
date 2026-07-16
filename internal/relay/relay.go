// Package relay sends OpenAI-format requests to upstreams and drives the
// account-pool retry: switch accounts on 429/401/403/5xx, and hold a
// per-account concurrency slot for the whole life of a streamed success.
package relay

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strconv"
	"sync"
	"time"

	"apiforge/internal/pool"
	"apiforge/internal/util/httpx"
)

// Do sends a POST with the given headers+body to url and returns the raw
// response (caller inspects status). Errors are transport/timeout failures.
func Do(ctx context.Context, url string, headers map[string]string, body []byte) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	return httpx.Client.Do(req)
}

// WithAccountRetry runs fn against the pool's candidate accounts, switching on
// retriable failures. On a 2xx it holds the account's concurrency slot until
// the response body is closed (so streaming counts toward the cap). Returns a
// synthesized 503 when every account is unavailable.
func WithAccountRetry[C any](
	ctx context.Context,
	p *pool.Pool[C],
	pin, session string,
	fn func(acc pool.Account[C]) (*http.Response, error),
) (*http.Response, error) {
	for _, acc := range p.Candidates(pin, session) {
		if !p.Acquire(acc.ID) {
			continue // at concurrency cap — try the next account
		}
		resp, err := fn(acc)
		if err != nil {
			p.Release(acc.ID)
			if ctx.Err() != nil {
				return nil, ctx.Err() // client disconnected — don't blame the account
			}
			p.MarkRateLimited(acc.ID, 10*time.Second) // transport/timeout: cool briefly
			continue
		}
		switch {
		case resp.StatusCode < 300:
			p.MarkOk(acc.ID)
			p.Bind(session, acc.ID)
			resp.Body = &releaseCloser{ReadCloser: resp.Body, release: func() { p.Release(acc.ID) }}
			return resp, nil
		case resp.StatusCode == 429:
			drain(resp)
			p.Release(acc.ID)
			p.MarkRateLimited(acc.ID, retryAfter(resp))
		case resp.StatusCode == 401 || resp.StatusCode == 403:
			drain(resp)
			p.Release(acc.ID)
			p.MarkAuthFailed(acc.ID)
		case resp.StatusCode == 502 || resp.StatusCode == 503 || resp.StatusCode == 504:
			drain(resp)
			p.Release(acc.ID)
			p.MarkRateLimited(acc.ID, 10*time.Second)
		default:
			// Non-retriable (400/404/422/deterministic 5xx): return upstream's
			// error body to the client verbatim.
			p.Release(acc.ID)
			return resp, nil
		}
	}
	return synth503(), nil
}

// releaseCloser frees the concurrency slot exactly once when the body closes.
type releaseCloser struct {
	io.ReadCloser
	once    sync.Once
	release func()
}

func (r *releaseCloser) Close() error {
	r.once.Do(r.release)
	return r.ReadCloser.Close()
}

func drain(resp *http.Response) {
	if resp.Body != nil {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
		resp.Body.Close()
	}
}

func retryAfter(resp *http.Response) time.Duration {
	if v := resp.Header.Get("Retry-After"); v != "" {
		if secs, err := strconv.Atoi(v); err == nil && secs > 0 {
			return time.Duration(secs) * time.Second
		}
	}
	return 0 // 0 => pool default
}

func synth503() *http.Response {
	body := `{"error":{"message":"All upstream accounts are unavailable.","type":"api_error"}}`
	return &http.Response{
		StatusCode: http.StatusServiceUnavailable,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(bytes.NewReader([]byte(body))),
	}
}
