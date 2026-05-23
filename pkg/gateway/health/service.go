// SPDX-License-Identifier: MIT

// Package health implements the §25.3 Platform Health API. It
// aggregates the health of the gateway's subsystems into the
// agent-operability surface an AI-DevOps agent reads to decide
// whether the platform needs remediation.
//
// The package is transport-agnostic: a Checker reports a Component
// status, the Aggregator rolls Components into an overall verdict,
// and the §15.1 HTTP handler in handler.go serves the result.
package health

import (
	"context"
	"sort"
	"sync"
)

// Status is the §25.3 health verdict enum.
type Status string

const (
	// StatusHealthy — the component is fully operational.
	StatusHealthy Status = "healthy"

	// StatusDegraded — the component is operational but impaired
	// (e.g., a replica is down, a cache is cold).
	StatusDegraded Status = "degraded"

	// StatusUnhealthy — the component is not operational.
	StatusUnhealthy Status = "unhealthy"
)

// rank orders statuses worst-last so an aggregate can take the worst.
func (s Status) rank() int {
	switch s {
	case StatusHealthy:
		return 0
	case StatusDegraded:
		return 1
	case StatusUnhealthy:
		return 2
	default:
		return 2
	}
}

// Component is the §25.3 per-subsystem health record.
type Component struct {
	// Name identifies the subsystem (e.g., `sessionstore`,
	// `blobstore`, `executor`).
	Name string `json:"name"`

	// Status is the subsystem verdict.
	Status Status `json:"status"`

	// Detail is a human-readable description of the current state.
	Detail string `json:"detail,omitempty"`

	// SuggestedAction is the §25.3 remediation hint an AI-DevOps
	// agent can act on when Status is not healthy. Empty when the
	// component is healthy or no action is known.
	SuggestedAction string `json:"suggestedAction,omitempty"`

	// RunbookRef points at the operational runbook for this
	// component's failure modes. Empty when none is registered.
	RunbookRef string `json:"runbookRef,omitempty"`
}

// Checker reports the health of one subsystem. Implementations must
// be fast and non-blocking — Check runs on the §25.3 health request
// path; expensive probes belong in a background goroutine that
// caches its result.
type Checker interface {
	// Name returns the subsystem name. Must be stable.
	Name() string

	// Check returns the current Component health.
	Check(ctx context.Context) Component
}

// CheckerFunc adapts a function to the Checker interface.
type CheckerFunc struct {
	ComponentName string
	Fn            func(ctx context.Context) Component
}

// Name implements Checker.
func (c CheckerFunc) Name() string { return c.ComponentName }

// Check implements Checker.
func (c CheckerFunc) Check(ctx context.Context) Component { return c.Fn(ctx) }

// Report is the §25.3 aggregate health response.
type Report struct {
	// Status is the worst component status — healthy only when
	// every component is healthy.
	Status Status `json:"status"`

	// Components is the per-subsystem breakdown, name-sorted.
	Components []Component `json:"components"`
}

// Aggregator holds the registered Checkers and rolls them up.
// Goroutine-safe.
type Aggregator struct {
	mu           sync.RWMutex
	checkers     map[string]Checker
	lastStatus   Status
	onTransition func(prev, curr Status)
}

// NewAggregator returns an empty Aggregator.
func NewAggregator() *Aggregator {
	return &Aggregator{checkers: map[string]Checker{}}
}

// OnTransition registers a callback the Aggregator invokes when a
// Report computes an aggregate status that differs from the previous
// Report's. It is the §25.3 health_status_changed hook. The first
// Report establishes the baseline and fires no transition.
func (a *Aggregator) OnTransition(fn func(prev, curr Status)) {
	a.mu.Lock()
	a.onTransition = fn
	a.mu.Unlock()
}

// Register adds a Checker. A later Register with the same Name
// replaces the earlier one.
func (a *Aggregator) Register(c Checker) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.checkers[c.Name()] = c
}

// Report runs every registered Checker and returns the aggregate.
func (a *Aggregator) Report(ctx context.Context) Report {
	a.mu.RLock()
	checkers := make([]Checker, 0, len(a.checkers))
	for _, c := range a.checkers {
		checkers = append(checkers, c)
	}
	a.mu.RUnlock()

	components := make([]Component, 0, len(checkers))
	worst := StatusHealthy
	for _, c := range checkers {
		comp := c.Check(ctx)
		if comp.Name == "" {
			comp.Name = c.Name()
		}
		components = append(components, comp)
		if comp.Status.rank() > worst.rank() {
			worst = comp.Status
		}
	}
	sort.Slice(components, func(i, j int) bool { return components[i].Name < components[j].Name })

	// §25.3: detect an aggregate-status transition against the previous
	// Report and fire the health_status_changed hook outside the lock.
	a.mu.Lock()
	prev := a.lastStatus
	a.lastStatus = worst
	fn := a.onTransition
	a.mu.Unlock()
	if fn != nil && prev != "" && prev != worst {
		fn(prev, worst)
	}
	return Report{Status: worst, Components: components}
}

// Component returns the single named component's health, or
// (Component{}, false) when no Checker with that name is
// registered.
func (a *Aggregator) Component(ctx context.Context, name string) (Component, bool) {
	a.mu.RLock()
	c, ok := a.checkers[name]
	a.mu.RUnlock()
	if !ok {
		return Component{}, false
	}
	comp := c.Check(ctx)
	if comp.Name == "" {
		comp.Name = name
	}
	return comp, true
}
