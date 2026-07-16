// Package registry holds the set of providers, initialises them (failures are
// isolated, never fatal), and routes a model id to the provider that owns it.
package registry

import (
	"context"
	"log/slog"
	"sync"

	"apiforge/internal/types"
)

// Diag is one provider's init diagnostic for /health.
type Diag struct {
	ID     string              `json:"id"`
	Ready  bool                `json:"ready"`
	Models []types.ModelObject `json:"models,omitempty"`
	Reason string              `json:"reason,omitempty"`
}

// Registry is the provider set. Safe for concurrent reads after InitAll.
type Registry struct {
	mu        sync.RWMutex
	providers []types.Provider
	reasons   map[string]string
	log       *slog.Logger
}

// New returns an empty registry.
func New(log *slog.Logger) *Registry {
	return &Registry{reasons: map[string]string{}, log: log}
}

// Register adds a provider (before InitAll).
func (r *Registry) Register(p types.Provider) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.providers = append(r.providers, p)
}

// InitAll initialises every provider; a failure disables that provider only.
func (r *Registry) InitAll(ctx context.Context) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, p := range r.providers {
		if err := p.Init(ctx); err != nil {
			r.reasons[p.ID()] = err.Error()
			if r.log != nil {
				r.log.Warn("provider init failed", "id", p.ID(), "err", err)
			}
		}
	}
}

// Ready returns the providers that initialised successfully.
func (r *Registry) Ready() []types.Provider {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var out []types.Provider
	for _, p := range r.providers {
		if p.IsReady() {
			out = append(out, p)
		}
	}
	return out
}

// FindByModel returns the first ready provider that owns the given model id.
func (r *Registry) FindByModel(model string) types.Provider {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, p := range r.providers {
		if p.IsReady() && p.OwnsModel(model) {
			return p
		}
	}
	return nil
}

// ByID returns the ready provider with the given id, or nil.
func (r *Registry) ByID(id string) types.Provider {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, p := range r.providers {
		if p.IsReady() && p.ID() == id {
			return p
		}
	}
	return nil
}

// Models aggregates the advertised models of all ready providers, de-duplicated
// by id (first ready owner wins — matching FindByModel's routing precedence, so
// /v1/models never lists the same id twice when two providers share it).
func (r *Registry) Models() []types.ModelObject {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var out []types.ModelObject
	seen := map[string]bool{}
	for _, p := range r.providers {
		if !p.IsReady() {
			continue
		}
		for _, m := range p.ListModels() {
			if seen[m.ID] {
				continue
			}
			seen[m.ID] = true
			out = append(out, m)
		}
	}
	return out
}

// Diagnostics returns per-provider readiness for /health.
func (r *Registry) Diagnostics() []Diag {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Diag, 0, len(r.providers))
	for _, p := range r.providers {
		d := Diag{ID: p.ID(), Ready: p.IsReady()}
		if d.Ready {
			d.Models = p.ListModels()
		} else {
			d.Reason = r.reasons[p.ID()]
		}
		out = append(out, d)
	}
	return out
}
