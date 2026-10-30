// SPDX-License-Identifier: MIT

package evaluator_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/alerting/evaluator"
	"github.com/lennylabs/lenny/pkg/alerting/rules"
)

// fakeExpr resolves alert expressions from a fixed map. When err is
// set every evaluation fails.
type fakeExpr struct {
	active map[string]bool
	err    error
}

func (f *fakeExpr) Active(_ context.Context, expr string) (bool, error) {
	if f.err != nil {
		return false, f.err
	}
	return f.active[expr], nil
}

// testRule builds a valid warning-severity Rule. Warning severity
// needs no RunbookURL, so the rule passes rules.Rule.Validate.
func testRule(name, expr string, forDur time.Duration) rules.Rule {
	return rules.Rule{
		Name:     name,
		Expr:     expr,
		For:      forDur,
		Severity: rules.SeverityWarning,
		Summary:  "test rule " + name,
	}
}

var base = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

func TestTickProgressesInactivePendingFiring(t *testing.T) {
	expr := "metric_a > 0"
	var fired []evaluator.Alert
	fake := &fakeExpr{active: map[string]bool{expr: true}}
	ev := evaluator.New(
		[]rules.Rule{testRule("RuleA", expr, time.Hour)},
		fake,
		evaluator.Options{OnFired: func(a evaluator.Alert) { fired = append(fired, a) }},
	)

	ev.Tick(context.Background(), base)
	if s, _ := ev.State("RuleA"); s != evaluator.StatePending {
		t.Fatalf("after tick 1: state %q, want pending", s)
	}

	ev.Tick(context.Background(), base.Add(30*time.Minute))
	if s, _ := ev.State("RuleA"); s != evaluator.StatePending {
		t.Fatalf("after tick 2: state %q, want pending — For not yet elapsed", s)
	}
	if len(fired) != 0 {
		t.Fatalf("OnFired called %d times before For elapsed", len(fired))
	}

	ev.Tick(context.Background(), base.Add(time.Hour))
	if s, _ := ev.State("RuleA"); s != evaluator.StateFiring {
		t.Fatalf("after tick 3: state %q, want firing", s)
	}
	if len(fired) != 1 || fired[0].Rule.Name != "RuleA" || !fired[0].Since.Equal(base) {
		t.Fatalf("OnFired = %+v, want one RuleA alert with Since=%v", fired, base)
	}
}

func TestTickForZeroFiresOnFirstActiveTick(t *testing.T) {
	expr := "metric > 0"
	fired := 0
	fake := &fakeExpr{active: map[string]bool{expr: true}}
	ev := evaluator.New([]rules.Rule{testRule("Immediate", expr, 0)}, fake,
		evaluator.Options{OnFired: func(evaluator.Alert) { fired++ }})

	ev.Tick(context.Background(), base)
	if s, _ := ev.State("Immediate"); s != evaluator.StateFiring {
		t.Errorf("state %q, want firing on the first tick (For=0)", s)
	}
	if fired != 1 {
		t.Errorf("OnFired called %d times, want 1", fired)
	}
}

func TestTickResolvesFiringAlert(t *testing.T) {
	expr := "metric > 0"
	var resolved []evaluator.Alert
	fake := &fakeExpr{active: map[string]bool{expr: true}}
	ev := evaluator.New([]rules.Rule{testRule("RuleR", expr, 0)}, fake,
		evaluator.Options{OnResolved: func(a evaluator.Alert) { resolved = append(resolved, a) }})

	ev.Tick(context.Background(), base) // fires
	fake.active[expr] = false
	ev.Tick(context.Background(), base.Add(time.Minute)) // resolves

	if s, _ := ev.State("RuleR"); s != evaluator.StateInactive {
		t.Errorf("state %q, want inactive after the expression cleared", s)
	}
	if len(resolved) != 1 || resolved[0].Rule.Name != "RuleR" {
		t.Errorf("OnResolved = %+v, want one RuleR alert", resolved)
	}
}

func TestTickPendingClearsWithoutFiring(t *testing.T) {
	expr := "metric > 0"
	signals := 0
	fake := &fakeExpr{active: map[string]bool{expr: true}}
	ev := evaluator.New([]rules.Rule{testRule("RuleP", expr, time.Hour)}, fake,
		evaluator.Options{
			OnFired:    func(evaluator.Alert) { signals++ },
			OnResolved: func(evaluator.Alert) { signals++ },
		})

	ev.Tick(context.Background(), base) // → pending
	fake.active[expr] = false
	ev.Tick(context.Background(), base.Add(time.Minute)) // pending → inactive

	if s, _ := ev.State("RuleP"); s != evaluator.StateInactive {
		t.Errorf("state %q, want inactive", s)
	}
	if signals != 0 {
		t.Errorf("a pending alert that cleared signalled %d times, want 0", signals)
	}
}

func TestTickEvalErrorPreservesState(t *testing.T) {
	expr := "metric > 0"
	fake := &fakeExpr{active: map[string]bool{expr: true}}
	ev := evaluator.New([]rules.Rule{testRule("RuleE", expr, 0)}, fake, evaluator.Options{})

	if firing := ev.Tick(context.Background(), base); firing != 1 {
		t.Fatalf("firing = %d, want 1 after the first tick", firing)
	}
	fake.err = errors.New("prometheus unreachable")
	ev.Tick(context.Background(), base.Add(time.Minute))
	if s, _ := ev.State("RuleE"); s != evaluator.StateFiring {
		t.Errorf("state %q, want firing preserved across an eval error", s)
	}
}

func TestNewSkipsInvalidRules(t *testing.T) {
	fake := &fakeExpr{active: map[string]bool{}}
	ev := evaluator.New([]rules.Rule{
		{Name: "Bad", Expr: ")(", Severity: rules.SeverityWarning, Summary: "unparseable"},
		testRule("Good", "metric > 0", 0),
	}, fake, evaluator.Options{})

	if _, ok := ev.State("Bad"); ok {
		t.Error("a rule with an unparseable expression must be skipped by New")
	}
	if _, ok := ev.State("Good"); !ok {
		t.Error("a valid rule must be registered by New")
	}
}

func TestNewRegistersTheCatalog(t *testing.T) {
	fake := &fakeExpr{active: map[string]bool{}}
	ev := evaluator.New(rules.Catalog(), fake, evaluator.Options{})
	for _, r := range rules.Catalog() {
		if _, ok := ev.State(r.Name); !ok {
			t.Errorf("catalogue rule %q is not registered", r.Name)
		}
	}
}

func TestFiringListsFiringRules(t *testing.T) {
	a, b := "metric_a > 0", "metric_b > 0"
	fake := &fakeExpr{active: map[string]bool{a: true, b: false}}
	ev := evaluator.New([]rules.Rule{
		testRule("RuleA", a, 0), testRule("RuleB", b, 0),
	}, fake, evaluator.Options{})

	ev.Tick(context.Background(), base)
	firing := ev.Firing()
	if len(firing) != 1 || firing[0] != "RuleA" {
		t.Errorf("Firing() = %v, want [RuleA]", firing)
	}
}

func TestStateUnknownRule(t *testing.T) {
	ev := evaluator.New(nil, &fakeExpr{}, evaluator.Options{})
	if _, ok := ev.State("NoSuchRule"); ok {
		t.Error("State reported ok for a rule not in the catalogue")
	}
}
