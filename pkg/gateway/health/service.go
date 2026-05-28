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

	"github.com/lennylabs/lenny/pkg/ops/conventions"
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

	// Degradation carries the §25.4 canonical envelope. The gateway's
	// in-process alert tracker always evaluates the compiled-in
	// thresholds, so a single-replica Report stamps
	// `thresholdSource: "compiled-in-defaults"` per §25.13 line 4848.
	// `lenny-ops` overrides the envelope when its aggregated view
	// derives from the operator's Prometheus rules.
	// spec: §25.4 line 215.
	Degradation *conventions.Degradation `json:"degradation,omitempty"`
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

// Report runs every registered Checker and returns the aggregate. The
// checkers fan out across goroutines so the aggregate runtime is
// bounded by the slowest probe rather than the sum of all probe
// latencies; the 2-second per-probe timeout is enforced by the
// individual Checker implementations against the supplied context.
// spec: §25.3 line 441 — "Each probe has a hard timeout of 2 seconds.
// Probes run in parallel."
func (a *Aggregator) Report(ctx context.Context) Report {
	a.mu.RLock()
	checkers := make([]Checker, 0, len(a.checkers))
	for _, c := range a.checkers {
		checkers = append(checkers, c)
	}
	a.mu.RUnlock()

	components := make([]Component, len(checkers))
	var wg sync.WaitGroup
	for i, c := range checkers {
		wg.Add(1)
		go func(i int, c Checker) {
			defer wg.Done()
			comp := c.Check(ctx)
			if comp.Name == "" {
				comp.Name = c.Name()
			}
			components[i] = comp
		}(i, c)
	}
	wg.Wait()

	worst := StatusHealthy
	for _, comp := range components {
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
	// spec: §25.13 line 4848 — the gateway's in-process tracker
	// evaluates the compiled-in thresholds. Surface the source on the
	// envelope so callers (and `lenny-ops` re-aggregation) can decide
	// whether the per-replica view aligns with the operator's
	// Prometheus-side customisation.
	return Report{
		Status:     worst,
		Components: components,
		Degradation: &conventions.Degradation{
			Level:           degradationLevelFor(worst),
			ThresholdSource: conventions.ThresholdSourceCompiledInDefaults,
		},
	}
}

// degradationLevelFor maps the §25.3 worst-status to the §25.4
// envelope level. healthy → healthy, degraded → degraded, unhealthy →
// failed.
func degradationLevelFor(s Status) conventions.DegradationLevel {
	switch s {
	case StatusDegraded:
		return conventions.DegradationDegraded
	case StatusUnhealthy:
		return conventions.DegradationFailed
	default:
		return conventions.DegradationHealthy
	}
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
