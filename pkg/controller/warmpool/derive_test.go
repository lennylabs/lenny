// SPDX-License-Identifier: MIT

// In-package unit tests for the §4.6.1 agent_pod_state derivation: the
// pure projection of a pool's live Sandbox set onto the mirror row set.
// The Sync write path itself is covered by the Postgres component test
// in tests/tier2_component/stores.
package warmpool

import (
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
)

func deriveTestPool() *lennyv1.SandboxWarmPool {
	return &lennyv1.SandboxWarmPool{
		ObjectMeta: metav1.ObjectMeta{Name: "claude-worker-small", Namespace: "lenny-agents"},
	}
}

func deriveTestTemplate(execMode string) *lennyv1.SandboxTemplate {
	return &lennyv1.SandboxTemplate{
		ObjectMeta: metav1.ObjectMeta{Name: "claude-worker-small", Namespace: "lenny-agents"},
		Spec:       lennyv1.SandboxTemplateSpec{ExecutionMode: execMode},
	}
}

func TestDerivePodStatesProjectsSandboxFields(t *testing.T) {
	pool := deriveTestPool()
	tmpl := deriveTestTemplate("task")
	sandboxes := []lennyv1.Sandbox{
		{
			ObjectMeta: metav1.ObjectMeta{Name: "sb-idle", ResourceVersion: "4096"},
			Spec:       lennyv1.SandboxSpec{IsolationProfile: "sandboxed"},
			Status:     lennyv1.SandboxStatus{Phase: "idle", NodeName: "node-7"},
		},
		{
			// A Sandbox that has not yet reported a phase derives as
			// warming, matching observedPhase.
			ObjectMeta: metav1.ObjectMeta{Name: "sb-fresh", ResourceVersion: "4100"},
			Spec:       lennyv1.SandboxSpec{IsolationProfile: "microvm"},
		},
	}

	got := derivePodStates(pool, tmpl, sandboxes)
	if len(got) != 2 {
		t.Fatalf("derivePodStates returned %d rows, want 2", len(got))
	}

	idle := got[0]
	if idle.PodID != "sb-idle" {
		t.Errorf("PodID = %q, want sb-idle (the Sandbox name)", idle.PodID)
	}
	if idle.PoolID != pool.Name {
		t.Errorf("PoolID = %q, want %q", idle.PoolID, pool.Name)
	}
	if idle.State != "idle" {
		t.Errorf("State = %q, want idle", idle.State)
	}
	if idle.IsolationProfile != "sandboxed" {
		t.Errorf("IsolationProfile = %q, want sandboxed (from the Sandbox spec)", idle.IsolationProfile)
	}
	if idle.ExecutionMode != "task" {
		t.Errorf("ExecutionMode = %q, want task (the pool-level mode from the template)", idle.ExecutionMode)
	}
	if idle.ResourceVersion != 4096 {
		t.Errorf("ResourceVersion = %d, want 4096", idle.ResourceVersion)
	}
	if idle.NodeName != "node-7" {
		t.Errorf("NodeName = %q, want node-7", idle.NodeName)
	}
	// An idle/warm pod carries no session.
	if idle.TenantID != "" || idle.SessionID != "" {
		t.Errorf("idle pod carries tenant/session %q/%q, want empty", idle.TenantID, idle.SessionID)
	}

	fresh := got[1]
	if fresh.State != "warming" {
		t.Errorf("a Sandbox with no reported phase derived State = %q, want warming", fresh.State)
	}
	if fresh.IsolationProfile != "microvm" {
		t.Errorf("IsolationProfile = %q, want microvm", fresh.IsolationProfile)
	}
	if fresh.NodeName != "" {
		t.Errorf("an unscheduled pod derived NodeName = %q, want empty", fresh.NodeName)
	}
}

func TestDerivePodStatesEmptySet(t *testing.T) {
	got := derivePodStates(deriveTestPool(), deriveTestTemplate("session"), nil)
	if len(got) != 0 {
		t.Errorf("derivePodStates(nil sandboxes) returned %d rows, want 0", len(got))
	}
}

func TestDerivePodStatesNilTemplate(t *testing.T) {
	// A nil template must not panic; execution mode falls back to empty.
	got := derivePodStates(deriveTestPool(), nil, []lennyv1.Sandbox{
		{ObjectMeta: metav1.ObjectMeta{Name: "sb-a", ResourceVersion: "1"}},
	})
	if len(got) != 1 {
		t.Fatalf("derivePodStates returned %d rows, want 1", len(got))
	}
	if got[0].ExecutionMode != "" {
		t.Errorf("ExecutionMode with a nil template = %q, want empty", got[0].ExecutionMode)
	}
}

// spec: §25.6 line 2956 (warmPoolStuckReplenish: zero in-flight warm-up
// claims). The PoolDrained condition is True in exactly the zero-in-flight
// state (positive minWarm, no idle, no warming pods) and False otherwise, so
// the doctor's dwell gate keys off a condition whose Status flips on entry.
func TestPoolDrainedConditionDerivation(t *testing.T) {
	cases := []struct {
		name                 string
		minWarm, warm, ready int
		wantStatus           metav1.ConditionStatus
		wantReason           string
	}{
		// No idle and no warming: the zero-in-flight drained state.
		{"drained", 5, 0, 0, metav1.ConditionTrue, "Drained"},
		// Idle pods present: available, not drained.
		{"available", 5, 3, 3, metav1.ConditionFalse, "NotDrained"},
		// Pods still warming: provisioning, not drained.
		{"provisioning", 5, 2, 0, metav1.ConditionFalse, "NotDrained"},
		// minWarm zero: the pool never warms, so it is never drained.
		{"minwarm-zero", 0, 0, 0, metav1.ConditionFalse, "NotDrained"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := poolDrainedCondition(c.minWarm, c.warm, c.ready)
			if got.Type != conditionPoolDrained {
				t.Errorf("Type = %q, want %q", got.Type, conditionPoolDrained)
			}
			if got.Status != c.wantStatus {
				t.Errorf("Status = %q, want %q", got.Status, c.wantStatus)
			}
			if got.Reason != c.wantReason {
				t.Errorf("Reason = %q, want %q", got.Reason, c.wantReason)
			}
		})
	}
}

// TestPoolDrainedTracksEntryTransition drives the PoolWarmingUp and
// PoolDrained conditions through the common Available→Drained transition via
// meta.SetStatusCondition (the exact path updateTemplateCondition uses) and
// asserts that PoolDrained's lastTransitionTime marks entry into Drained,
// while PoolWarmingUp's does not. This is the regression for divergence #2:
// meta.SetStatusCondition refreshes lastTransitionTime only on a Status
// change, and PoolWarmingUp keeps Status False across Available→Drained, so
// its timestamp is stale. Keying the doctor's >5m dwell off PoolWarmingUp
// would fire the instant the pool drains; keying it off PoolDrained (which
// flips False→True on entry) holds the >5m guard the spec mandates.
//
// spec: §25.6 line 2956 (zero in-flight warm-up claims for > 5m).
func TestPoolDrainedTracksEntryTransition(t *testing.T) {
	available := time.Date(2026, 7, 1, 9, 0, 0, 0, time.UTC)
	drainedAt := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC) // 3h later

	var conds []metav1.Condition

	// The pool first becomes Available (idle pods present) at 09:00.
	setAt(&conds, poolWarmingUpCondition(5, 3, 3), available)
	setAt(&conds, poolDrainedCondition(5, 3, 3), available)

	// At 12:00 demand drains the pool to zero idle and zero warming.
	setAt(&conds, poolWarmingUpCondition(5, 0, 0), drainedAt)
	setAt(&conds, poolDrainedCondition(5, 0, 0), drainedAt)

	warming := meta.FindStatusCondition(conds, conditionPoolWarmingUp)
	drained := meta.FindStatusCondition(conds, conditionPoolDrained)
	if warming == nil || drained == nil {
		t.Fatalf("missing conditions: warming=%v drained=%v", warming, drained)
	}

	// PoolWarmingUp stayed False across Available→Drained (both reasons are
	// False), so its lastTransitionTime is stale: it still marks 09:00, three
	// hours before the pool actually drained. A dwell gate reading this would
	// fire the instant the pool drained.
	if !warming.LastTransitionTime.Time.Equal(available) {
		t.Errorf("PoolWarmingUp lastTransitionTime = %v, want the stale %v (Status stayed False)",
			warming.LastTransitionTime.Time, available)
	}

	// PoolDrained flipped False→True on entry, so its lastTransitionTime marks
	// 12:00 — the actual entry into the zero-in-flight state. The doctor's >5m
	// dwell gate reads this and holds until 12:05.
	if !drained.LastTransitionTime.Time.Equal(drainedAt) {
		t.Errorf("PoolDrained lastTransitionTime = %v, want the entry time %v (Status flipped False→True)",
			drained.LastTransitionTime.Time, drainedAt)
	}
	if drained.Status != metav1.ConditionTrue {
		t.Errorf("PoolDrained Status = %q, want True after draining", drained.Status)
	}
}

// setAt applies a condition through meta.SetStatusCondition at a fixed time,
// mirroring the updateTemplateCondition write path so the test exercises the
// same lastTransitionTime semantics the controller relies on.
func setAt(conds *[]metav1.Condition, c metav1.Condition, at time.Time) {
	c.LastTransitionTime = metav1.NewTime(at)
	meta.SetStatusCondition(conds, c)
}

func TestParseResourceVersion(t *testing.T) {
	cases := []struct {
		in   string
		want int64
	}{
		{"0", 0},
		{"4096", 4096},
		{"9007199254740993", 9007199254740993},
		{"", 0},             // an empty resourceVersion mirrors as 0
		{"not-a-number", 0}, // an opaque/unparseable value mirrors as 0
	}
	for _, c := range cases {
		if got := parseResourceVersion(c.in); got != c.want {
			t.Errorf("parseResourceVersion(%q) = %d, want %d", c.in, got, c.want)
		}
	}
}
