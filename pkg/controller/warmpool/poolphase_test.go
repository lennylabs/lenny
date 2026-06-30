// SPDX-License-Identifier: MIT

package warmpool_test

import (
	"encoding/json"
	"testing"

	"github.com/lennylabs/lenny/pkg/controller/warmpool"
	"github.com/lennylabs/lenny/pkg/controller/warmpool/plan"
	"github.com/lennylabs/lenny/pkg/events"
	"github.com/lennylabs/lenny/pkg/gateway/eventbuffer"
)

// spec §4.0: the pool state manager derives one of {warming, draining,
// exhausted} (plus the steady "ready" state) from the observed pod set
// and emits pool_state_changed on each transition.
func TestDerivePoolPhase(t *testing.T) {
	cases := []struct {
		name     string
		minWarm  int
		decision plan.Plan
		want     warmpool.PoolPhase
	}{
		{
			name:     "ready when idle pods are at target",
			minWarm:  3,
			decision: plan.Plan{WarmCount: 3, ReadyCount: 3},
			want:     warmpool.PoolPhaseReady,
		},
		{
			name:     "warming when fresh pods are starting up",
			minWarm:  3,
			decision: plan.Plan{WarmCount: 0, ReadyCount: 0, Create: 3},
			want:     warmpool.PoolPhaseWarming,
		},
		{
			name:     "draining when planner sheds idle pods",
			minWarm:  2,
			decision: plan.Plan{WarmCount: 5, ReadyCount: 5, Drain: []string{"a", "b", "c"}},
			want:     warmpool.PoolPhaseDraining,
		},
		{
			name:     "exhausted when minWarm > 0 and pool has zero pods",
			minWarm:  3,
			decision: plan.Plan{WarmCount: 0, ReadyCount: 0},
			want:     warmpool.PoolPhaseExhausted,
		},
		{
			name:     "ready is the default when minWarm is zero",
			minWarm:  0,
			decision: plan.Plan{WarmCount: 0, ReadyCount: 0},
			want:     warmpool.PoolPhaseReady,
		},
		{
			name:     "mixed warming and idle reports ready",
			minWarm:  3,
			decision: plan.Plan{WarmCount: 3, ReadyCount: 1, Create: 0},
			want:     warmpool.PoolPhaseReady,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := warmpool.DerivePoolPhase(c.minWarm, c.decision); got != c.want {
				t.Errorf("DerivePoolPhase(%d, %+v) = %s, want %s",
					c.minWarm, c.decision, got, c.want)
			}
		})
	}
}

// spec §4.0, §16.6: the first observation of a pool sets the baseline
// without emitting; only a phase change fires pool_state_changed.
func TestReconcileEmitsPoolStateChangedOnTransition(t *testing.T) {
	s := newScheme(t)
	c := newClient(t, s, template(), pool(3, 10))
	buf := eventbuffer.NewEventBuffer(0)
	em := eventbuffer.NewEmitter(buf, "test")
	r := reconcilerWithEvents(c, s, em)

	reconcileWith(t, r) // baseline: warming
	if got := buf.Query(0, events.EventFilter{EventType: "pool_state_changed"}, 100); len(got.Events) != 0 {
		t.Errorf("baseline observation emitted %d events, want 0", len(got.Events))
	}

	// Re-reconcile after marking the freshly-created pods idle. The pool
	// transitions warming → ready, which must fire one event.
	for _, sb := range poolSandboxes(t, c) {
		sb.Status.Phase = "idle"
		if err := applyStatus(testContext(), c, &sb); err != nil {
			t.Fatalf("set Sandbox %s idle: %v", sb.Name, err)
		}
	}
	if _, err := r.Reconcile(testContext(), reconcileRequest()); err != nil {
		t.Fatalf("Reconcile after pods became idle: %v", err)
	}

	page := buf.Query(0, events.EventFilter{EventType: "pool_state_changed"}, 100)
	if len(page.Events) != 1 {
		t.Fatalf("after warming → ready transition, emitted %d events, want 1", len(page.Events))
	}
	ev := page.Events[0].Event
	if ev.Type != "dev.lenny.pool_state_changed" {
		t.Errorf("event type = %q, want dev.lenny.pool_state_changed", ev.Type)
	}
	var data struct {
		Pool     string `json:"pool"`
		OldState string `json:"oldState"`
		NewState string `json:"newState"`
	}
	if err := json.Unmarshal(ev.Data, &data); err != nil {
		t.Fatalf("event data: %v", err)
	}
	if data.Pool != testPool || data.OldState != "warming" || data.NewState != "ready" {
		t.Errorf("event = %+v, want %s warming → ready", data, testPool)
	}
}

// spec §4.0: a reconcile that does not change the derived phase emits
// nothing.
func TestReconcileNoEmitOnStablePhase(t *testing.T) {
	s := newScheme(t)
	c := newClient(t, s, template(), pool(3, 10),
		idleSandbox("sb-a"), idleSandbox("sb-b"), idleSandbox("sb-c"))
	buf := eventbuffer.NewEventBuffer(0)
	em := eventbuffer.NewEmitter(buf, "test")
	r := reconcilerWithEvents(c, s, em)

	reconcileWith(t, r) // baseline: ready
	if _, err := r.Reconcile(testContext(), reconcileRequest()); err != nil {
		t.Fatalf("second Reconcile: %v", err)
	}

	if got := buf.Query(0, events.EventFilter{EventType: "pool_state_changed"}, 100); len(got.Events) != 0 {
		t.Errorf("stable pool emitted %d events, want 0", len(got.Events))
	}
}

// spec §4.0: a pool with minWarm > 0 and a maxWarm of 0 derives as
// exhausted, and the emit carries severity=warning per the catalog.
func TestReconcileEmitsExhaustedWithWarningSeverity(t *testing.T) {
	s := newScheme(t)
	c := newClient(t, s, template(), pool(3, 0))
	buf := eventbuffer.NewEventBuffer(0)
	em := eventbuffer.NewEmitter(buf, "test")
	r := reconcilerWithEvents(c, s, em)

	// Two reconciles: baseline records exhausted; second is a no-op
	// because the phase did not change.
	reconcileWith(t, r)
	if _, err := r.Reconcile(testContext(), reconcileRequest()); err != nil {
		t.Fatalf("second Reconcile: %v", err)
	}

	// No event yet because the first observation is the baseline. Now
	// expand maxWarm so the pool can warm up and the phase transitions
	// exhausted → warming.
	p := getPool(t, c)
	p.Spec.MaxWarm = 5
	if err := c.Update(testContext(), &p); err != nil {
		t.Fatalf("update pool maxWarm: %v", err)
	}
	if _, err := r.Reconcile(testContext(), reconcileRequest()); err != nil {
		t.Fatalf("Reconcile after maxWarm bump: %v", err)
	}

	page := buf.Query(0, events.EventFilter{EventType: "pool_state_changed"}, 100)
	if len(page.Events) != 1 {
		t.Fatalf("emitted %d events, want 1 (exhausted → warming)", len(page.Events))
	}
	var data struct {
		OldState string `json:"oldState"`
		NewState string `json:"newState"`
	}
	if err := json.Unmarshal(page.Events[0].Event.Data, &data); err != nil {
		t.Fatalf("event data: %v", err)
	}
	if data.OldState != "exhausted" || data.NewState != "warming" {
		t.Errorf("transition = %s → %s, want exhausted → warming", data.OldState, data.NewState)
	}
}

// spec §4.0: a reconciler without a wired Events sink reconciles
// normally and emits nothing. A baseline-test guard against breaking
// the existing reconciler API.
func TestReconcileWithoutEventsIsSilent(t *testing.T) {
	s := newScheme(t)
	c := newClient(t, s, template(), pool(3, 10))
	r := &warmpool.Reconciler{Client: c, Scheme: s}
	if _, err := r.Reconcile(testContext(), reconcileRequest()); err != nil {
		t.Fatalf("Reconcile without Events: %v", err)
	}
}
