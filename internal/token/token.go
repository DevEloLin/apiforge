// Package token implements the shared OAuth token lifecycle for file-backed CLI
// credentials: load once, and on expiry perform a single-flight refresh that
// first re-reads from disk (in case the CLI already rotated the token for us).
package token

import (
	"context"
	"sync"

	"golang.org/x/sync/singleflight"
)

// Source is the per-provider credential logic the Manager drives. Implementations
// MUST be safe for Token()/Fresh() to be called concurrently with Read()/Refresh()
// (guard the underlying auth state with a mutex or atomic pointer).
type Source interface {
	// Read loads credentials from disk into the source's state.
	Read(ctx context.Context) error
	// Token returns the current access token.
	Token() string
	// Fresh reports whether the current token is still valid (with skew).
	Fresh() bool
	// Refresh performs the HTTP refresh, persists rotated tokens, returns the new one.
	Refresh(ctx context.Context) (string, error)
}

// Manager wraps a Source with load-once + single-flight refresh semantics.
type Manager struct {
	src    Source
	sf     singleflight.Group
	mu     sync.Mutex
	loaded bool
}

// New returns a Manager for the given credential source.
func New(src Source) *Manager { return &Manager{src: src} }

func (m *Manager) ensureLoaded(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.loaded {
		return nil
	}
	if err := m.src.Read(ctx); err != nil {
		return err
	}
	m.loaded = true
	return nil
}

// AccessToken returns a fresh access token, refreshing at most once across
// concurrent callers.
func (m *Manager) AccessToken(ctx context.Context) (string, error) {
	if err := m.ensureLoaded(ctx); err != nil {
		return "", err
	}
	if m.src.Fresh() {
		return m.src.Token(), nil
	}
	v, err, _ := m.sf.Do("refresh", func() (any, error) {
		// The CLI may have refreshed the file underneath us — re-read first.
		if err := m.src.Read(ctx); err != nil {
			return "", err
		}
		if m.src.Fresh() {
			return m.src.Token(), nil
		}
		return m.src.Refresh(ctx)
	})
	if err != nil {
		return "", err
	}
	return v.(string), nil
}
