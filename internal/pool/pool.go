// Package pool is a rotating, self-healing account pool for one provider.
//
// Beyond apiforge's original round-robin/failover + cooldown, it adds two ideas
// borrowed from sub2api that matter for reusing subscription accounts safely:
//
//   - per-account concurrency cap: never let more than N requests hit one
//     subscription account at once (protects the account, lowers ban risk);
//   - sticky sessions: route a given conversation to the same account when
//     healthy (better upstream cache hits + fairer rate-limit spread).
package pool

import (
	"log/slog"
	"sync"
	"time"
)

const (
	defaultRateLimitCooldown = 60 * time.Second
	authFailCooldown         = 5 * time.Minute
)

// Strategy selects how healthy accounts are ordered.
type Strategy string

const (
	RoundRobin Strategy = "round-robin"
	Failover   Strategy = "failover"
)

// Account is one credential handle in the pool.
type Account[C any] struct {
	ID   string
	Cred C
}

type state struct {
	disabledUntil  time.Time
	failures       int
	manualDisabled bool
	inflight       int // current concurrent requests (for the concurrency cap)
}

// Pool is a generic account pool. Safe for concurrent use.
type Pool[C any] struct {
	accounts  []Account[C]
	byID      map[string]int
	states    []*state
	strategy  Strategy
	conc      int // per-account concurrency cap; 0 = unlimited
	stickyTTL time.Duration

	mu        sync.Mutex
	cursor    int
	preferred string
	sticky    map[string]stickyEntry // sessionKey -> account
	log       *slog.Logger
}

type stickyEntry struct {
	id      string
	expires time.Time
}

// Options configures a Pool.
type Options struct {
	Strategy       Strategy
	MaxConcurrency int           // per account; 0 = unlimited
	StickyTTL      time.Duration // 0 disables sticky sessions
}

// New builds a pool. Panics if accounts is empty (a provider with zero accounts
// should not be registered).
func New[C any](accounts []Account[C], opts Options, log *slog.Logger) *Pool[C] {
	if len(accounts) == 0 {
		panic("pool.New requires at least one account")
	}
	if opts.Strategy == "" {
		opts.Strategy = RoundRobin
	}
	byID := make(map[string]int, len(accounts))
	states := make([]*state, len(accounts))
	for i, a := range accounts {
		byID[a.ID] = i
		states[i] = &state{}
	}
	return &Pool[C]{
		accounts:  accounts,
		byID:      byID,
		states:    states,
		strategy:  opts.Strategy,
		conc:      opts.MaxConcurrency,
		stickyTTL: opts.StickyTTL,
		sticky:    map[string]stickyEntry{},
		log:       log,
	}
}

// Size returns the number of accounts.
func (p *Pool[C]) Size() int { return len(p.accounts) }

// IDs returns all account ids (for diagnostics).
func (p *Pool[C]) IDs() []string {
	ids := make([]string, len(p.accounts))
	for i, a := range p.accounts {
		ids[i] = a.ID
	}
	return ids
}

// All returns a copy of all accounts without perturbing rotation.
func (p *Pool[C]) All() []Account[C] {
	out := make([]Account[C], len(p.accounts))
	copy(out, p.accounts)
	return out
}

func (p *Pool[C]) healthy(i int, now time.Time) bool {
	s := p.states[i]
	return !s.manualDisabled && !s.disabledUntil.After(now)
}

// Candidates returns the accounts to try for one request, in preference order:
// sticky/pinned/preferred first (when healthy), then accounts with a free
// concurrency slot, then the rest per strategy; if everyone is cooling down it
// still returns the account recovering soonest so the request can attempt.
func (p *Pool[C]) Candidates(pin, session string) []Account[C] {
	p.mu.Lock()
	defer p.mu.Unlock()
	now := time.Now()

	// Base set: healthy accounts.
	order := make([]int, 0, len(p.accounts))
	for i := range p.accounts {
		if p.healthy(i, now) {
			order = append(order, i)
		}
	}

	if len(order) == 0 {
		// All cooling down / disabled — pick the one recovering soonest so the
		// request still attempts (prefer non-manually-disabled).
		best, bestT := -1, time.Time{}
		for i, s := range p.states {
			if s.manualDisabled {
				continue
			}
			if best == -1 || s.disabledUntil.Before(bestT) {
				best, bestT = i, s.disabledUntil
			}
		}
		if best == -1 { // everything manually disabled
			best = 0
		}
		return []Account[C]{p.accounts[best]}
	}

	if p.strategy == RoundRobin {
		start := p.cursor % len(order)
		p.cursor = (p.cursor + 1) % len(order)
		order = append(order[start:], order[:start]...)
	}

	// Prefer accounts with a free concurrency slot (soft, keeps ordering stable).
	if p.conc > 0 {
		free, busy := make([]int, 0, len(order)), make([]int, 0, len(order))
		for _, i := range order {
			if p.states[i].inflight < p.conc {
				free = append(free, i)
			} else {
				busy = append(busy, i)
			}
		}
		order = append(free, busy...)
	}

	// Sticky/pin/preferred moves to the very front when in the running set.
	front := pin
	if front == "" {
		front = p.stickyLookup(session, now)
	}
	if front == "" {
		front = p.preferred
	}
	if front != "" {
		if idx, ok := p.byID[front]; ok {
			for pos, i := range order {
				if i == idx {
					order = append([]int{i}, append(order[:pos:pos], order[pos+1:]...)...)
					break
				}
			}
		}
	}

	out := make([]Account[C], len(order))
	for k, i := range order {
		out[k] = p.accounts[i]
	}
	return out
}

func (p *Pool[C]) stickyLookup(session string, now time.Time) string {
	if session == "" || p.stickyTTL == 0 {
		return ""
	}
	e, ok := p.sticky[session]
	if !ok || e.expires.Before(now) {
		return ""
	}
	if idx, ok := p.byID[e.id]; !ok || !p.healthy(idx, now) {
		return ""
	}
	return e.id
}

// Bind records a session -> account affinity (no-op if sticky disabled).
func (p *Pool[C]) Bind(session, id string) {
	if session == "" || p.stickyTTL == 0 {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, ok := p.byID[id]; ok {
		p.sticky[session] = stickyEntry{id: id, expires: time.Now().Add(p.stickyTTL)}
	}
}

// Acquire reserves a concurrency slot on the account, returning false if it is
// at its per-account cap. Always pair a true result with Release.
func (p *Pool[C]) Acquire(id string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	idx, ok := p.byID[id]
	if !ok {
		return false
	}
	if p.conc > 0 && p.states[idx].inflight >= p.conc {
		return false
	}
	p.states[idx].inflight++
	return true
}

// Release frees a concurrency slot previously taken with Acquire.
func (p *Pool[C]) Release(id string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if idx, ok := p.byID[id]; ok && p.states[idx].inflight > 0 {
		p.states[idx].inflight--
	}
}

// MarkOk clears automatic cooldown/failure state (keeps a manual disable).
func (p *Pool[C]) MarkOk(id string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if idx, ok := p.byID[id]; ok {
		p.states[idx].disabledUntil = time.Time{}
		p.states[idx].failures = 0
	}
}

// MarkRateLimited cools an account down after a 429 (retryAfter <= 0 uses default).
func (p *Pool[C]) MarkRateLimited(id string, retryAfter time.Duration) {
	if retryAfter <= 0 {
		retryAfter = defaultRateLimitCooldown
	}
	p.cooldown(id, retryAfter, "rate-limited")
}

// MarkAuthFailed cools an account down after an auth failure (token likely dead).
func (p *Pool[C]) MarkAuthFailed(id string) { p.cooldown(id, authFailCooldown, "auth-failed") }

func (p *Pool[C]) cooldown(id string, d time.Duration, reason string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	idx, ok := p.byID[id]
	if !ok {
		return
	}
	p.states[idx].disabledUntil = time.Now().Add(d)
	p.states[idx].failures++
	if p.log != nil {
		p.log.Warn("account cooldown", "id", id, "reason", reason, "seconds", int(d.Seconds()))
	}
}

// SetPreferred pins a preferred account (empty clears). Returns false on unknown id.
func (p *Pool[C]) SetPreferred(id string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if id != "" {
		if _, ok := p.byID[id]; !ok {
			return false
		}
	}
	p.preferred = id
	return true
}

// SetEnabled toggles a manual disable. Returns false on unknown id.
func (p *Pool[C]) SetEnabled(id string, enabled bool) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	idx, ok := p.byID[id]
	if !ok {
		return false
	}
	p.states[idx].manualDisabled = !enabled
	if enabled {
		p.states[idx].disabledUntil = time.Time{}
	}
	return true
}

// AccountStatus is a health snapshot for the admin API.
type AccountStatus struct {
	ID             string `json:"id"`
	Healthy        bool   `json:"healthy"`
	ManualDisabled bool   `json:"manual_disabled"`
	Failures       int    `json:"failures"`
	CooldownMs     int64  `json:"cooldown_ms"`
	Inflight       int    `json:"inflight"`
	Preferred      bool   `json:"preferred"`
}

// Status returns every account's health for the admin/status API.
func (p *Pool[C]) Status() []AccountStatus {
	p.mu.Lock()
	defer p.mu.Unlock()
	now := time.Now()
	out := make([]AccountStatus, len(p.accounts))
	for i, a := range p.accounts {
		s := p.states[i]
		cd := int64(0)
		if s.disabledUntil.After(now) {
			cd = s.disabledUntil.Sub(now).Milliseconds()
		}
		out[i] = AccountStatus{
			ID:             a.ID,
			Healthy:        p.healthy(i, now),
			ManualDisabled: s.manualDisabled,
			Failures:       s.failures,
			CooldownMs:     cd,
			Inflight:       s.inflight,
			Preferred:      p.preferred == a.ID,
		}
	}
	return out
}
