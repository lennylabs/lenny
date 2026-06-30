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
	"github.com/lennylabs/lenny/pkg/events"
	"github.com/lennylabs/lenny/pkg/gateway/eventbuffer"
)

// spec §4.0, §16.6: a rule that fires emits alert_fired through the
// shared EventEmitter; a firing rule that clears emits alert_resolved.
func TestEmitCallbacksFireAndResolve(t *testing.T) {
	buf := eventbuffer.NewEventBuffer(0)
	em := eventbuffer.NewEmitter(buf, "replica-1")
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
		RuleName   string `json:"ruleName"`
		AlertName  string `json:"alertName"`
		Runbook    string `json:"runbook"`
		RunbookURL string `json:"runbookUrl"`
	}
	if err := json.Unmarshal(got.Data, &data); err != nil {
		t.Fatalf("alert_fired payload: %v", err)
	}
	if data.RuleName != "PoolExhausted" {
		t.Errorf("ruleName = %q, want PoolExhausted", data.RuleName)
	}
	if data.AlertName != "PoolExhausted" {
		t.Errorf("alertName = %q, want PoolExhausted", data.AlertName)
	}
	// spec: §25.7 line 3236 / §25.17 line 5172 — `runbook` is the short
	// slug derived from the rule (here from the URL's last segment, since
	// the rule set only RunbookURL); the full URL is carried separately.
	if data.Runbook != "pool-exhausted" {
		t.Errorf("runbook = %q, want the short slug pool-exhausted", data.Runbook)
	}
	if data.RunbookURL != "https://docs.lenny.dev/runbooks/pool-exhausted" {
		t.Errorf("runbookUrl = %q, want the rule's RunbookURL", data.RunbookURL)
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

// spec: §25.17 line 5172 — the WarmPoolExhausted alert_fired payload
// carries the short runbook slug ("warm-pool-exhaustion", not the URL)
// and a suggestedAction the watchdog routes on. This exercises the
// actual §16.5 catalog rule end-to-end through the evaluator.
func TestWarmPoolExhaustedPayloadCarriesRunbookAndSuggestedAction_spec_25_17(t *testing.T) {
	var rule rules.Rule
	for _, r := range rules.Catalog() {
		if r.Name == "WarmPoolExhausted" {
			rule = r
			break
		}
	}
	if rule.Name == "" {
		t.Fatal("WarmPoolExhausted not in catalog")
	}
	buf := eventbuffer.NewEventBuffer(0)
	em := eventbuffer.NewEmitter(buf, "replica-1")
	fake := &fakeExpr{active: map[string]bool{rule.Expr: true}}
	onFired, onResolved := evaluator.EmitCallbacks(evaluator.EventEmitOptions{Emitter: em})
	// For:60s — fire after the sustain window elapses across two ticks.
	ev := evaluator.New([]rules.Rule{rule}, fake, evaluator.Options{OnFired: onFired, OnResolved: onResolved})
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	ev.Tick(context.Background(), t0)
	ev.Tick(context.Background(), t0.Add(2*time.Minute))
	page := buf.Query(0, events.EventFilter{EventType: "alert_fired"}, 100)
	if len(page.Events) != 1 {
		t.Fatalf("alert_fired emitted %d events, want 1", len(page.Events))
	}
	var data struct {
		AlertName       string `json:"alertName"`
		Runbook         string `json:"runbook"`
		SuggestedAction struct {
			Action   string `json:"action"`
			Endpoint string `json:"endpoint"`
			Runbook  string `json:"runbook"`
		} `json:"suggestedAction"`
	}
	if err := json.Unmarshal(page.Events[0].Event.Data, &data); err != nil {
		t.Fatalf("alert_fired payload: %v", err)
	}
	if data.AlertName != "WarmPoolExhausted" {
		t.Errorf("alertName = %q, want WarmPoolExhausted", data.AlertName)
	}
	if data.Runbook != "warm-pool-exhaustion" {
		t.Errorf("runbook = %q, want the short slug warm-pool-exhaustion", data.Runbook)
	}
	if data.SuggestedAction.Action != "SCALE_WARM_POOL" {
		t.Errorf("suggestedAction.action = %q, want SCALE_WARM_POOL", data.SuggestedAction.Action)
	}
	if data.SuggestedAction.Runbook != "warm-pool-exhaustion" {
		t.Errorf("suggestedAction.runbook = %q, want warm-pool-exhaustion", data.SuggestedAction.Runbook)
	}
}

// spec §16.6: a rule that never fires emits no operational events.
func TestEmitCallbacksNoEventsWhileInactive(t *testing.T) {
	buf := eventbuffer.NewEventBuffer(0)
	em := eventbuffer.NewEmitter(buf, "replica-1")
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
	buf := eventbuffer.NewEventBuffer(0)
	em := eventbuffer.NewEmitter(buf, "replica-1")
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
	buf := eventbuffer.NewEventBuffer(0)
	em := eventbuffer.NewEmitter(buf, "replica-1")
	ev := evaluator.NewWithEmitter(nil, evaluator.NoopExprEvaluator{},
		evaluator.EventEmitOptions{Emitter: em})
	for _, r := range rules.Catalog() {
		if _, ok := ev.State(r.Name); !ok {
			t.Fatalf("catalog rule %q was not registered by NewWithEmitter", r.Name)
		}
	}
}
