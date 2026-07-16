package relay

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"apiforge/internal/pool"
)

func okResponse() *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{}`)),
	}
}

func onePool(t *testing.T, conc int) *pool.Pool[string] {
	t.Helper()
	return pool.New([]pool.Account[string]{{ID: "a#1", Cred: "k"}},
		pool.Options{Strategy: pool.RoundRobin, MaxConcurrency: conc}, nil)
}

func TestWithAccountRetry_QueuesUntilSlotFrees(t *testing.T) {
	// Arrange: cap=1, slot held by an in-flight request.
	p := onePool(t, 1)
	if !p.Acquire("a#1") {
		t.Fatal("setup acquire failed")
	}
	t.Setenv("QUEUE_WAIT_MS", "2000")

	done := make(chan *http.Response, 1)
	go func() {
		resp, err := WithAccountRetry(context.Background(), p, "", "",
			func(pool.Account[string]) (*http.Response, error) { return okResponse(), nil })
		if err != nil {
			t.Errorf("unexpected err: %v", err)
		}
		done <- resp
	}()

	// Assert: the queued request must NOT complete while the slot is held.
	select {
	case <-done:
		t.Fatal("request completed while account was at cap — queueing broken")
	case <-time.After(150 * time.Millisecond):
	}

	// Act: free the slot — the queued request should complete promptly.
	p.Release("a#1")
	select {
	case resp := <-done:
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want 200", resp.StatusCode)
		}
		resp.Body.Close()
	case <-time.After(1 * time.Second):
		t.Fatal("queued request did not wake up after Release")
	}
}

func TestWithAccountRetry_QueueTimeoutReturns503(t *testing.T) {
	p := onePool(t, 1)
	if !p.Acquire("a#1") {
		t.Fatal("setup acquire failed")
	}
	t.Setenv("QUEUE_WAIT_MS", "100") // tiny budget; slot never frees

	start := time.Now()
	resp, err := WithAccountRetry(context.Background(), p, "", "",
		func(pool.Account[string]) (*http.Response, error) { return okResponse(), nil })
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 after queue timeout", resp.StatusCode)
	}
	if time.Since(start) < 90*time.Millisecond {
		t.Fatal("returned before the queue budget elapsed")
	}
}

func TestWithAccountRetry_ClientCancelWhileQueued(t *testing.T) {
	p := onePool(t, 1)
	if !p.Acquire("a#1") {
		t.Fatal("setup acquire failed")
	}
	t.Setenv("QUEUE_WAIT_MS", "5000")

	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(50 * time.Millisecond); cancel() }()

	_, err := WithAccountRetry(ctx, p, "", "",
		func(pool.Account[string]) (*http.Response, error) { return okResponse(), nil })
	if err == nil {
		t.Fatal("expected context error when client cancels while queued")
	}
}

func TestWithAccountRetry_NoQueueWhenAccountsFailed(t *testing.T) {
	// All accounts cooling (auth-failed) — must 503 fast, not wait out the queue.
	p := onePool(t, 1)
	t.Setenv("QUEUE_WAIT_MS", "5000")

	start := time.Now()
	resp, err := WithAccountRetry(context.Background(), p, "", "",
		func(pool.Account[string]) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusUnauthorized,
				Header:     http.Header{},
				Body:       io.NopCloser(strings.NewReader(`{}`)),
			}, nil
		})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", resp.StatusCode)
	}
	if time.Since(start) > 1*time.Second {
		t.Fatal("waited in queue for a non-capacity failure")
	}
}

func TestWithAccountRetry_PanicInFnReleasesSlot(t *testing.T) {
	// A panic inside fn must not leak the concurrency slot: after it unwinds,
	// the account must be acquirable again.
	p := onePool(t, 1)
	t.Setenv("QUEUE_WAIT_MS", "100")

	func() {
		defer func() { _ = recover() }() // catch the propagated panic
		_, _ = WithAccountRetry(context.Background(), p, "", "",
			func(pool.Account[string]) (*http.Response, error) { panic("boom") })
	}()

	// Slot must be free again (Acquire succeeds).
	if !p.Acquire("a#1") {
		t.Fatal("slot leaked after panic in fn — account not acquirable")
	}
	p.Release("a#1")
}

func TestWithAccountRetry_ManyWaitersDrainInOrder(t *testing.T) {
	// 1 account, cap=2, 10 concurrent requests, each "upstream call" takes ~30ms.
	// All 10 must complete within the queue budget.
	p := onePool(t, 2)
	t.Setenv("QUEUE_WAIT_MS", "5000")

	results := make(chan int, 10)
	for i := 0; i < 10; i++ {
		go func() {
			resp, err := WithAccountRetry(context.Background(), p, "", "",
				func(pool.Account[string]) (*http.Response, error) {
					time.Sleep(30 * time.Millisecond)
					return okResponse(), nil
				})
			if err != nil {
				results <- 0
				return
			}
			resp.Body.Close() // closing releases the slot
			results <- resp.StatusCode
		}()
	}
	deadline := time.After(4 * time.Second)
	for i := 0; i < 10; i++ {
		select {
		case code := <-results:
			if code != http.StatusOK {
				t.Fatalf("request %d got %d, want 200", i, code)
			}
		case <-deadline:
			t.Fatalf("only %d/10 requests completed before deadline", i)
		}
	}
}
