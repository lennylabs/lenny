// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/alerting/evaluator"
	"github.com/lennylabs/lenny/pkg/alerting/rules"
)

var overlayBase = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

// staticExpr resolves a fixed set of alert expressions as active.
type staticExpr struct{ active map[string]bool }

func (s staticExpr) Active(_ context.Context, expr string) (bool, error) {
	return s.active[expr], nil
}

// exprFor returns the §16.5 catalogue expression for a named alert so the
// test drives the real rule, not a synthetic one.
func exprFor(t *testing.T, name string) string {
	t.Helper()
	for _, r := range rules.Catalog() {
		if r.Name == name {
			return r.Expr
		}
	}
	t.Fatalf("alert %q not in catalogue", name)
	return ""
}

// spec: §25.3 lines 443-451 — alertHealthSource resolves a component's
// verdict from the real §16.5 evaluator + the rules→component mapping: a
// firing critical alert (SessionStoreUnavailable) reports postgres
// unhealthy; a firing warning (CertExpiryImminent) reports cert-manager
// degraded; an unmapped component resolves nothing.
func TestAlertHealthSourceDerivesComponentVerdict_spec_25_3_443(t *testing.T) {
	expr := staticExpr{active: map[string]bool{
		exprFor(t, "SessionStoreUnavailable"): true, // critical → postgres
		exprFor(t, "CertExpiryImminent"):      true, // warning  → cert-manager
	}}
	ev := evaluator.New(rules.Catalog(), expr, evaluator.Options{})
	// SessionStoreUnavailable has For=15s; advance twice so the second tick
	// crosses the sustain window and the rule reaches StateFiring.
	ev.Tick(context.Background(), overlayBase)
	ev.Tick(context.Background(), overlayBase.Add(time.Minute))

	var ptr atomic.Pointer[evaluator.Evaluator]
	ptr.Store(ev)
	src := alertHealthSource{eval: &ptr}

	st, firing, ok := src.ComponentStatus(rules.HealthComponentPostgres)
	if !ok || st != "unhealthy" {
		t.Fatalf("postgres => (%q, %v), want (unhealthy, true)", st, ok)
	}
	if len(firing) == 0 || firing[0] != "SessionStoreUnavailable" {
		t.Errorf("postgres firing = %v, want [SessionStoreUnavailable]", firing)
	}

	st, _, ok = src.ComponentStatus(rules.HealthComponentCertManager)
	if !ok || st != "degraded" {
		t.Errorf("cert-manager => (%q, %v), want (degraded, true)", st, ok)
	}

	if _, _, ok := src.ComponentStatus(rules.HealthComponentRedis); ok {
		t.Error("redis should resolve nothing (no redis alert firing)")
	}
}

// A nil tracker (in-process tracking disabled) resolves nothing rather than
// panicking, so /v1/admin/health falls back to dependency probes.
func TestAlertHealthSourceNilTrackerResolvesNothing_spec_25_3_443(t *testing.T) {
	var ptr atomic.Pointer[evaluator.Evaluator]
	src := alertHealthSource{eval: &ptr}
	if _, _, ok := src.ComponentStatus(rules.HealthComponentPostgres); ok {
		t.Error("nil tracker must resolve no component verdict")
	}
}
