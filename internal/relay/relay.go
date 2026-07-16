// Package relay sends OpenAI-format requests to upstreams and drives the
// account-pool retry: switch accounts on 429/401/403/5xx, and hold a
// per-account concurrency slot for the whole life of a streamed success.
package relay

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
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
// the response body is closed (so streaming counts toward the cap).
//
// When every healthy account is at its concurrency cap, the request QUEUES:
// it waits for a slot to free (Release broadcast) and retries, up to the
// QUEUE_WAIT_MS budget (default 60s). This lets N accounts absorb bursts of
// many concurrent users instead of failing the overflow immediately. Only when
// accounts are truly unavailable (all cooling down / failed) does it return a
// synthesized 503.
func WithAccountRetry[C any](
	ctx context.Context,
	p *pool.Pool[C],
	pin, session string,
	fn func(acc pool.Account[C]) (*http.Response, error),
) (*http.Response, error) {
	deadline := time.Now().Add(queueWait())
	for {
		// Grab the freed-channel BEFORE trying, so a Release that happens
		// mid-attempt is not missed (close-broadcast races are lost otherwise).
		freed := p.SlotFreed()

		resp, err, busySkips := tryAccounts(ctx, p, pin, session, fn)
		if resp != nil || err != nil {
			return resp, err
		}
		if busySkips == 0 {
			// Nothing was skipped for capacity — accounts genuinely failed.
			return synth503("All upstream accounts are unavailable."), nil
		}

		// All healthy accounts busy: queue until a slot frees or budget runs out.
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return synth503("All upstream accounts are busy. Please retry."), nil
		}
		timer := time.NewTimer(remaining)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
			return synth503("All upstream accounts are busy. Please retry."), nil
		case <-freed:
			timer.Stop() // a slot opened — retry immediately
		}
	}
}

// tryAccounts makes one pass over the candidates. Returns (nil, nil, busySkips)
// when no account produced a result: busySkips counts accounts skipped because
// they were at their concurrency cap (queueable), as opposed to marked failures.
func tryAccounts[C any](
	ctx context.Context,
	p *pool.Pool[C],
	pin, session string,
	fn func(acc pool.Account[C]) (*http.Response, error),
) (*http.Response, error, int) {
	busySkips := 0
	for _, acc := range p.Candidates(pin, session) {
		if !p.Acquire(acc.ID) {
			busySkips++ // at concurrency cap — queueable
			continue
		}
		resp, err, retriable := attemptOnce(ctx, p, acc, session, fn)
		if retriable {
			continue // account cooled; try the next candidate
		}
		return resp, err, busySkips // terminal: success, client-cancel, or passthrough error
	}
	return nil, nil, busySkips
}

// attemptOnce runs fn against one already-acquired account. A deferred release
// frees the concurrency slot on EVERY exit — including a panic inside fn — EXCEPT
// the success path, which hands the slot to the response body's releaseCloser.
// Returns retriable=true when the caller should try the next account.
func attemptOnce[C any](
	ctx context.Context,
	p *pool.Pool[C],
	acc pool.Account[C],
	session string,
	fn func(acc pool.Account[C]) (*http.Response, error),
) (resp *http.Response, err error, retriable bool) {
	handedOff := false
	defer func() {
		if !handedOff {
			p.Release(acc.ID) // covers all non-success exits and any panic in fn
		}
	}()

	resp, err = fn(acc)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err(), false // client disconnected — don't blame the account
		}
		p.MarkRateLimited(acc.ID, 10*time.Second) // transport/timeout: cool briefly
		return nil, nil, true
	}
	switch {
	case resp.StatusCode < 300:
		p.MarkOk(acc.ID)
		p.Bind(session, acc.ID)
		resp.Body = &releaseCloser{ReadCloser: resp.Body, release: func() { p.Release(acc.ID) }}
		handedOff = true // slot released when the (possibly streamed) body closes
		return resp, nil, false
	case resp.StatusCode == 429:
		drain(resp)
		p.MarkRateLimited(acc.ID, retryAfter(resp))
		return nil, nil, true
	case resp.StatusCode == 401 || resp.StatusCode == 403:
		drain(resp)
		p.MarkAuthFailed(acc.ID)
		return nil, nil, true
	case resp.StatusCode == 502 || resp.StatusCode == 503 || resp.StatusCode == 504:
		drain(resp)
		p.MarkRateLimited(acc.ID, 10*time.Second)
		return nil, nil, true
	default:
		// Non-retriable (400/404/422/deterministic 5xx): return upstream's error
		// body to the client verbatim (slot freed by the deferred release).
		return resp, nil, false
	}
}

// queueWait is the max time a request may wait for a free concurrency slot.
// Read per-call (cheap) so tests and operators can tune QUEUE_WAIT_MS live.
func queueWait() time.Duration {
	if v := os.Getenv("QUEUE_WAIT_MS"); v != "" {
		if ms, err := strconv.Atoi(v); err == nil && ms >= 0 {
			return time.Duration(ms) * time.Millisecond
		}
	}
	return 60 * time.Second
}

// JSONResponse wraps a value as a 200 application/json *http.Response — used by
// translating providers to hand a synthesized body to the account-retry layer.
func JSONResponse(v any) *http.Response {
	b, _ := json.Marshal(v)
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(bytes.NewReader(b)),
	}
}

// StreamingResponse returns a 200 text/event-stream *http.Response whose body is
// produced by produce (run in a goroutine writing translated SSE frames). produce
// is responsible for closing any upstream body it reads.
func StreamingResponse(produce func(w io.Writer)) *http.Response {
	pr, pw := io.Pipe()
	go func() {
		// A panic in a translation/stream-parse path must fail this one request,
		// never crash the whole gateway. CloseWithError ends the client stream.
		defer func() {
			if r := recover(); r != nil {
				_ = pw.CloseWithError(fmt.Errorf("stream produce panic: %v", r))
			}
		}()
		produce(pw)
		_ = pw.Close()
	}()
	return &http.Response{
		StatusCode: http.StatusOK,
		Header: http.Header{
			"Content-Type":      []string{"text/event-stream; charset=utf-8"},
			"Cache-Control":     []string{"no-cache, no-transform"},
			"X-Accel-Buffering": []string{"no"},
		},
		Body: pr,
	}
}

// SynthStatus builds a small JSON error response with the given status — used to
// steer the account-retry classifier (e.g. a 401 on token-refresh failure).
func SynthStatus(status int, message string) *http.Response {
	msg, _ := json.Marshal(message)
	body := `{"error":{"message":` + string(msg) + `,"type":"api_error"}}`
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(bytes.NewReader([]byte(body))),
	}
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

func synth503(message string) *http.Response {
	return SynthStatus(http.StatusServiceUnavailable, message)
}
