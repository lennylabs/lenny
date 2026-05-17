// SPDX-License-Identifier: MIT

// Package evaluator implements the §16.5 alert-rule evaluation state
// machine. It drives each PrometheusRule from the §16.5 catalogue
// through the inactive → pending → firing lifecycle and signals the
// §16.6 alert_fired and alert_resolved operational events on the
// firing-edge transitions.
//
// The state machine is pure: rule expressions are resolved through the
// ExprEvaluator interface, so the PromQL backend can be a Prometheus
// query API in production and a fake in tests. The §16.6 emit wiring
// and the production ExprEvaluator are added when the gateway metric
// pipeline lands.
package evaluator

import (
	"context"
	"sync"
	"time"

	"github.com/lennylabs/lenny/pkg/alerting/rules"
)

// AlertState is the §16.5 lifecycle state of one alert rule.
type AlertState string

const (
	// StateInactive: the rule's expression currently yields no series.
	StateInactive AlertState = "inactive"
	// StatePending: the expression is active but has not yet been
	// active for the rule's For sustain duration.
	StatePending AlertState = "pending"
	// StateFiring: the expression has been active for at least the For
	// duration. The alert_fired event was emitted on entry.
	StateFiring AlertState = "firing"
)

// ExprEvaluator resolves a PromQL alert expression to whether it
// currently yields at least one series. A production implementation
// queries Prometheus; tests supply a fake.
type ExprEvaluator interface {
	// Active reports whether expr currently yields a result. When it
	// returns an error the evaluator leaves the rule's state unchanged
	// for that tick rather than flapping the alert.
	Active(ctx context.Context, expr string) (bool, error)
}

// Alert is the evaluator's view of one rule, passed to the OnFired and
// OnResolved callbacks.
type Alert struct {
	Rule  rules.Rule
	State AlertState
	// Since is the instant the rule's expression first went active for
	// the run that produced this transition.
	Since time.Time
}

// Options configures an Evaluator. A zero field selects its default.
type Options struct {
	// Interval overrides DefaultInterval for the Run driver.
	Interval time.Duration
	// Clock overrides time.Now for the Run driver.
	Clock func() time.Time
	// OnFired, when set, is called once when a rule transitions into
	// StateFiring — the §16.6 alert_fired signal.
	OnFired func(Alert)
	// OnResolved, when set, is called once when a firing rule's
	// expression clears — the §16.6 alert_resolved signal.
	OnResolved func(Alert)
}

// DefaultInterval is the evaluation sweep interval for the Run driver.
const DefaultInterval = 30 * time.Second

// Evaluator drives the §16.5 alert catalogue through its lifecycle. It
// is goroutine-safe.
type Evaluator struct {
	exprEval   ExprEvaluator
	interval   time.Duration
	clock      func() time.Time
	onFired    func(Alert)
	onResolved func(Alert)

	mu     sync.Mutex
	states map[string]*ruleState
}

// ruleState tracks one rule between ticks.
type ruleState struct {
	rule        rules.Rule
	state       AlertState
	activeSince time.Time
}

// New returns an Evaluator over the given rule catalogue. Rules that
// fail rules.Rule.Validate are skipped so a single malformed entry
// does not disable the evaluator.
func New(catalog []rules.Rule, exprEval ExprEvaluator, opts Options) *Evaluator {
	e := &Evaluator{
		exprEval:   exprEval,
		interval:   opts.Interval,
		clock:      opts.Clock,
		onFired:    opts.OnFired,
		onResolved: opts.OnResolved,
		states:     map[string]*ruleState{},
	}
	if e.interval <= 0 {
		e.interval = DefaultInterval
	}
	if e.clock == nil {
		e.clock = time.Now
	}
	for _, r := range catalog {
		if r.Validate() != nil {
			continue
		}
		e.states[r.Name] = &ruleState{rule: r, state: StateInactive}
	}
	return e
}

// Tick runs one evaluation sweep at now. Each rule's expression is
// evaluated: a rule active for at least its For duration transitions
// to firing and signals OnFired, and a firing rule whose expression
// clears transitions to inactive and signals OnResolved. A rule whose
// expression errors keeps its state for the tick. Tick returns the
// count of rules currently firing.
func (e *Evaluator) Tick(ctx context.Context, now time.Time) int {
	e.mu.Lock()
	defer e.mu.Unlock()
	firing := 0
	for _, rs := range e.states {
		active, err := e.exprEval.Active(ctx, rs.rule.Expr)
		if err == nil {
			e.advance(rs, active, now)
		}
		if rs.state == StateFiring {
			firing++
		}
	}
	return firing
}

// advance moves one rule's state machine for a single tick. The caller
// holds the lock.
func (e *Evaluator) advance(rs *ruleState, active bool, now time.Time) {
	if !active {
		wasFiring := rs.state == StateFiring
		rs.state = StateInactive
		since := rs.activeSince
		rs.activeSince = time.Time{}
		if wasFiring && e.onResolved != nil {
			e.onResolved(Alert{Rule: rs.rule, State: StateInactive, Since: since})
		}
		return
	}
	if rs.state == StateInactive {
		rs.state = StatePending
		rs.activeSince = now
	}
	// A rule that has been active for at least its For duration fires.
	// For == 0 promotes pending → firing on the same tick.
	if rs.state == StatePending && !now.Before(rs.activeSince.Add(rs.rule.For)) {
		rs.state = StateFiring
		if e.onFired != nil {
			e.onFired(Alert{Rule: rs.rule, State: StateFiring, Since: rs.activeSince})
		}
	}
}

// State reports the current lifecycle state of the named rule. ok is
// false when no rule of that name is in the catalogue.
func (e *Evaluator) State(name string) (state AlertState, ok bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	rs, ok := e.states[name]
	if !ok {
		return "", false
	}
	return rs.state, true
}

// Firing returns the names of every rule currently in StateFiring.
func (e *Evaluator) Firing() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	var out []string
	for name, rs := range e.states {
		if rs.state == StateFiring {
			out = append(out, name)
		}
	}
	return out
}

// Run drives the evaluation sweep on the configured interval until ctx
// is done.
func (e *Evaluator) Run(ctx context.Context) {
	ticker := time.NewTicker(e.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			e.Tick(ctx, e.clock())
		}
	}
}
