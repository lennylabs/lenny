// SPDX-License-Identifier: MIT

package evaluator_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/alerting/evaluator"
	"github.com/lennylabs/lenny/pkg/alerting/rules"
	"github.com/lennylabs/lenny/pkg/gateway/events"
)

// spec §4.0, §16.6: a rule that fires emits alert_fired through the
// shared EventEmitter; a firing rule that clears emits alert_resolved.
func TestEmitCallbacksFireAndResolve(t *testing.T) {
	buf := events.NewEventBuffer(0)
	em := events.NewEmitter(buf, "replica-1")
	expr := "metric > 0"
	fake := &fakeExpr{active: map[string]bool{expr: true}}
	rule := rules.Rule{
		Name:       "PoolExhausted",
		Expr:       expr,
		Severity:   rules.SeverityCritical,
		Summary:    "pool exhausted",
		RunbookURL: "https://docs.lenny.dev/runbooks/pool-exhausted",
	}
	onFired, onResolved := evaluator.EmitCallbacks(evaluator.EventEmitOptions{
		Emitter: em,
		Source:  "//lenny.dev/gateway/test",
	})
	ev := evaluator.New([]rules.Rule{rule}, fake, evaluator.Options{
		OnFired: onFired, OnResolved: onResolved,
	})
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	ev.Tick(context.Background(), t0)
	page := buf.Query(0, events.EventFilter{EventType: "alert_fired"}, 100)
	if len(page.Events) != 1 {
		t.Fatalf("alert_fired emitted %d events, want 1", len(page.Events))
	}
	got := page.Events[0].Event
	if got.Severity != "critical" {
		t.Errorf("alert_fired severity = %q, want critical", got.Severity)
	}
	var data struct {
		RuleName string `json:"ruleName"`
		Runbook  string `json:"runbook"`
	}
	if err := json.Unmarshal(got.Data, &data); err != nil {
		t.Fatalf("alert_fired payload: %v", err)
	}
	if data.RuleName != "PoolExhausted" {
		t.Errorf("ruleName = %q, want PoolExhausted", data.RuleName)
	}
	if data.Runbook != "https://docs.lenny.dev/runbooks/pool-exhausted" {
		t.Errorf("runbook = %q, want the rule's RunbookURL", data.Runbook)
	}

	// Clear the expression and tick again — alert_resolved fires.
	fake.active[expr] = false
	ev.Tick(context.Background(), t0.Add(time.Minute))
	page = buf.Query(0, events.EventFilter{EventType: "alert_resolved"}, 100)
	if len(page.Events) != 1 {
		t.Fatalf("alert_resolved emitted %d events, want 1", len(page.Events))
	}
	if page.Events[0].Event.Severity != "info" {
		t.Errorf("alert_resolved severity = %q, want info (resolution is informational)",
			page.Events[0].Event.Severity)
	}
}

// spec §16.6: a rule that never fires emits no operational events.
func TestEmitCallbacksNoEventsWhileInactive(t *testing.T) {
	buf := events.NewEventBuffer(0)
	em := events.NewEmitter(buf, "replica-1")
	expr := "metric > 0"
	fake := &fakeExpr{active: map[string]bool{expr: false}}
	onFired, onResolved := evaluator.EmitCallbacks(evaluator.EventEmitOptions{Emitter: em})
	ev := evaluator.New(
		[]rules.Rule{{Name: "Quiet", Expr: expr, Severity: rules.SeverityWarning, Summary: "quiet"}},
		fake,
		evaluator.Options{OnFired: onFired, OnResolved: onResolved},
	)
	for i := 0; i < 5; i++ {
		ev.Tick(context.Background(), time.Now().Add(time.Duration(i)*time.Minute))
	}
	if got := buf.Query(0, events.EventFilter{}, 100); len(got.Events) != 0 {
		t.Errorf("an always-inactive rule emitted %d events, want 0", len(got.Events))
	}
}

// spec §4.0: a nil Emitter yields nil callbacks; the evaluator runs
// but no events are published.
func TestEmitCallbacksNilEmitter(t *testing.T) {
	onFired, onResolved := evaluator.EmitCallbacks(evaluator.EventEmitOptions{Emitter: nil})
	if onFired != nil || onResolved != nil {
		t.Error("EmitCallbacks(nil Emitter) returned non-nil callbacks")
	}
}

// spec §25.13: the gateway's per-replica tracker uses NoopExprEvaluator
// when no PromQL backend is wired. Every Active call returns
// ErrNoExprBackend so the evaluator preserves its state per the
// "error preserves state" rule.
func TestNoopExprEvaluator(t *testing.T) {
	noop := evaluator.NoopExprEvaluator{}
	active, err := noop.Active(context.Background(), "metric > 0")
	if active {
		t.Error("NoopExprEvaluator.Active returned true, want false")
	}
	if !errors.Is(err, evaluator.ErrNoExprBackend) {
		t.Errorf("NoopExprEvaluator.Active err = %v, want ErrNoExprBackend", err)
	}
}

// spec §25.13: with NoopExprEvaluator the evaluator runs its sweep
// without firing or resolving anything.
func TestEvaluatorWithNoopExprFiresNothing(t *testing.T) {
	buf := events.NewEventBuffer(0)
	em := events.NewEmitter(buf, "replica-1")
	ev := evaluator.NewWithEmitter(
		[]rules.Rule{{Name: "Quiet", Expr: "metric > 0", Severity: rules.SeverityWarning, Summary: "quiet"}},
		evaluator.NoopExprEvaluator{},
		evaluator.EventEmitOptions{Emitter: em},
	)
	for i := 0; i < 5; i++ {
		ev.Tick(context.Background(), time.Now())
	}
	if got := buf.Query(0, events.EventFilter{}, 100); len(got.Events) != 0 {
		t.Errorf("NoopExprEvaluator emitted %d events, want 0", len(got.Events))
	}
}

// spec §4.0: NewWithEmitter defaults the catalog to rules.Catalog() and
// builds an evaluator that drains the §16.5 default set.
func TestNewWithEmitterDefaultsToCatalog(t *testing.T) {
	buf := events.NewEventBuffer(0)
	em := events.NewEmitter(buf, "replica-1")
	ev := evaluator.NewWithEmitter(nil, evaluator.NoopExprEvaluator{},
		evaluator.EventEmitOptions{Emitter: em})
	for _, r := range rules.Catalog() {
		if _, ok := ev.State(r.Name); !ok {
			t.Fatalf("catalog rule %q was not registered by NewWithEmitter", r.Name)
		}
	}
}
