// SPDX-License-Identifier: MIT

package evaluator

import (
	"context"
	"encoding/json"
	"log"
	"time"

	"github.com/lennylabs/lenny/pkg/alerting/rules"
	"github.com/lennylabs/lenny/pkg/gateway/events"
)

// EventEmitOptions wires the evaluator's firing-edge callbacks to a
// §25.3 / §25.5 EventEmitter. Each transition publishes one event
// through emitter so an ops agent observes the alert lifecycle on the
// gateway event buffer and the §25.5 Redis stream.
type EventEmitOptions struct {
	// Emitter is the §25.3 / §25.5 EventEmitter every gateway and
	// lenny-ops subsystem shares. Required.
	Emitter events.EventEmitter
	// Source is the §25.5 CloudEvents `source` value (e.g.,
	// "//lenny.dev/gateway/replica-1"). Empty leaves Source unset; the
	// StreamEmitter stamps a default.
	Source string
	// Now overrides time.Now for the ctx-derived emission timestamp.
	// Tests use it for deterministic timestamps.
	Now func() time.Time
	// OnRuleEvalDuration mirrors Options.OnRuleEvalDuration so the
	// §25.13 line 4835 `lenny_alerting_rule_eval_duration_seconds`
	// histogram can be wired through NewWithEmitter alongside the
	// firing-edge event surface. F-25.13.3.
	OnRuleEvalDuration func(rule string, d time.Duration)
}

// EmitCallbacks returns OnFired and OnResolved hooks suitable for
// Options. The hooks publish §16.6 alert_fired and alert_resolved
// CloudEvents records on the firing-edge transitions, carrying the
// rule name, severity, and §17.7 runbook URL in the data payload. A
// nil opts.Emitter returns nil callbacks — the evaluator runs but no
// events are published.
func EmitCallbacks(opts EventEmitOptions) (onFired, onResolved func(Alert)) {
	if opts.Emitter == nil {
		return nil, nil
	}
	now := opts.Now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	emit := func(a Alert, eventType events.EventType, severity string) {
		payload := map[string]any{
			"ruleName":   a.Rule.Name,
			"severity":   string(a.Rule.Severity),
			"summary":    a.Rule.Summary,
			"sinceUnix":  a.Since.Unix(),
		}
		// §16.5 alert_fired payloads carry the optional runbook URL when
		// the rule's annotation set it (§25.7 Path B).
		if a.Rule.RunbookURL != "" {
			payload["runbook"] = a.Rule.RunbookURL
		}
		data, _ := json.Marshal(payload)
		event := events.OperationalEvent{
			Source:          opts.Source,
			Type:            eventType.CloudEventsType(),
			Severity:        severity,
			Time:            now(),
			DataContentType: "application/json",
			Data:            data,
		}
		if err := opts.Emitter.Emit(context.Background(), event); err != nil {
			log.Printf("alerting/evaluator: emit %s for %s: %v",
				eventType, a.Rule.Name, err)
		}
	}
	onFired = func(a Alert) {
		// §16.5: alert_fired severity matches the rule's severity so an
		// operator querying the buffer can filter on severity directly.
		sev := string(a.Rule.Severity)
		if sev == "" {
			sev = "warning"
		}
		emit(a, events.EventAlertFired, sev)
	}
	onResolved = func(a Alert) {
		// §16.6: alert_resolved is informational regardless of the
		// rule's firing severity — the resolution itself is good news.
		emit(a, events.EventAlertResolved, "info")
	}
	return onFired, onResolved
}

// NoopExprEvaluator is the ExprEvaluator the gateway uses when no
// PromQL backend is configured. Every Active call returns (false,
// ErrNoExprBackend) so the evaluator preserves its state across the
// tick — no rule fires, no resolution event triggers, and the §25.13
// per-replica fall-back tracker waits quietly until a real backend
// lands.
type NoopExprEvaluator struct{}

// Active always reports inactive with ErrNoExprBackend. The evaluator's
// per-rule state machine treats a non-nil error as "preserve current
// state", so a no-op backend keeps every rule inactive without
// spurious resolves.
func (NoopExprEvaluator) Active(context.Context, string) (bool, error) {
	return false, ErrNoExprBackend
}

// ErrNoExprBackend is returned by NoopExprEvaluator. Callers tag log
// lines or metrics with this sentinel to distinguish a configured-but-
// unavailable Prometheus from the deliberate no-op.
var ErrNoExprBackend = errNoBackend{}

type errNoBackend struct{}

func (errNoBackend) Error() string { return "alerting/evaluator: no expression backend configured" }

// Compile-time guard.
var _ ExprEvaluator = NoopExprEvaluator{}

// EvaluatorEmitter wires Catalog + Evaluator + EventEmitter into one
// piece the gateway and lenny-ops main binaries instantiate per §4.0.
// Run starts the periodic Tick loop and blocks until ctx is cancelled.
type EvaluatorEmitter struct {
	*Evaluator
}

// NewWithEmitter constructs an Evaluator over catalog wired to emitter.
// Catalog defaults to rules.Catalog() when nil so the §16.5 default set
// flows through without callers re-importing it.
func NewWithEmitter(catalog []rules.Rule, expr ExprEvaluator, opts EventEmitOptions) *Evaluator {
	if catalog == nil {
		catalog = rules.Catalog()
	}
	onFired, onResolved := EmitCallbacks(opts)
	return New(catalog, expr, Options{
		OnFired:            onFired,
		OnResolved:         onResolved,
		OnRuleEvalDuration: opts.OnRuleEvalDuration,
	})
}
