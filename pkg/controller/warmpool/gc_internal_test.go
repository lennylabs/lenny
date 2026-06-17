// SPDX-License-Identifier: MIT

package warmpool

import (
	"context"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
	claimstate "github.com/lennylabs/lenny/pkg/sandboxclaim/state"
)

// TestClassifyReclaimDisposition exercises the §4.6.1 binding-state-aware
// orphan-GC predicate as a pure function of the claim's binding state, its
// orphan key (binding-state-transition time, holdExpiresAt, or
// creationTimestamp), and the clock. It covers the live-state drain, the
// reserved precondition-DELETE, the empty-status creation-timestamp fallback,
// the terminal-disposition skip, and the not-yet-aged skip for each, so the
// reclaim routing is verified without a cluster.
//
// spec: 4.6.1 (orphaned SandboxClaim detection, three binding-state predicates
// plus the creation-timestamp fallback), 3.3 (drain for live states), 6.10
// (recycling with no holdExpiresAt reclaimed by draining).
func TestClassifyReclaimDisposition(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC)
	const orphanTimeout = 5 * time.Minute
	const grace = 60 * time.Second
	old := metav1.Time{Time: now.Add(-orphanTimeout - time.Minute)}
	fresh := metav1.Time{Time: now.Add(-time.Minute)}
	creation := func(at time.Time) metav1.Time { return metav1.Time{Time: at} }

	tests := []struct {
		name string
		// phase is the claim binding state (status.phase).
		phase string
		// transition is status.bindingStateTransitionTime (nil = unset).
		transition *metav1.Time
		// hold is status.holdExpiresAt (nil = unset).
		hold *metav1.Time
		// created is metadata.creationTimestamp.
		created metav1.Time
		want    reclaimDisposition
	}{
		{
			name:       "bound aged on transition time drains",
			phase:      string(claimstate.Bound),
			transition: &old,
			created:    creation(now.Add(-time.Hour)),
			want:       reclaimDrain,
		},
		{
			name:       "bound not yet aged on transition time skips",
			phase:      string(claimstate.Bound),
			transition: &fresh,
			created:    creation(now.Add(-time.Hour)), // old creation does not matter for a bound claim
			want:       reclaimSkip,
		},
		{
			name:       "recycling aged on transition time drains (coordinating-gateway crash, no holdExpiresAt)",
			phase:      string(claimstate.Recycling),
			transition: &old,
			created:    creation(now.Add(-time.Hour)),
			want:       reclaimDrain,
		},
		{
			name:    "recycling without a transition stamp falls back to creationTimestamp and drains",
			phase:   string(claimstate.Recycling),
			created: creation(now.Add(-orphanTimeout - time.Minute)),
			want:    reclaimDrain,
		},
		{
			name:    "reserved aged on holdExpiresAt plus grace reclaims by precondition DELETE",
			phase:   string(claimstate.Reserved),
			hold:    &metav1.Time{Time: now.Add(-grace - time.Second)},
			created: creation(now.Add(-time.Hour)),
			want:    reclaimReserved,
		},
		{
			name:    "reserved within holdExpiresAt plus grace skips",
			phase:   string(claimstate.Reserved),
			hold:    &metav1.Time{Time: now.Add(-grace + 10*time.Second)},
			created: creation(now.Add(-time.Hour)),
			want:    reclaimSkip,
		},
		{
			name:       "reserved missing holdExpiresAt falls back to transition time plus orphan timeout",
			phase:      string(claimstate.Reserved),
			transition: &old,
			created:    creation(now.Add(-time.Hour)),
			want:       reclaimReserved,
		},
		{
			name:    "empty status older than orphan timeout drains (CREATE-before-status fallback)",
			phase:   "",
			created: creation(now.Add(-orphanTimeout - time.Minute)),
			want:    reclaimDrain,
		},
		{
			name:    "empty status younger than orphan timeout skips",
			phase:   "",
			created: creation(now.Add(-time.Minute)),
			want:    reclaimSkip,
		},
		{
			name:    "released terminal disposition is never an orphan",
			phase:   string(claimstate.Released),
			created: creation(now.Add(-time.Hour)),
			want:    reclaimSkip,
		},
		{
			name:    "failed terminal disposition is never an orphan",
			phase:   string(claimstate.Failed),
			created: creation(now.Add(-time.Hour)),
			want:    reclaimSkip,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			g := &ClaimGarbageCollector{OrphanTimeout: orphanTimeout, ReservedHoldGrace: grace}
			cl := &lennyv1.SandboxClaim{
				ObjectMeta: metav1.ObjectMeta{CreationTimestamp: tt.created},
				Status: lennyv1.SandboxClaimStatus{
					Phase:                      tt.phase,
					BindingStateTransitionTime: tt.transition,
					HoldExpiresAt:              tt.hold,
				},
			}
			if got := g.classify(cl, now); got != tt.want {
				t.Errorf("classify(%s) = %v, want %v", tt.name, got, tt.want)
			}
		})
	}
}

// TestClassifyZeroKeyNeverReclaims asserts that a claim whose orphan key
// resolves to a zero time is never reclaimed: a missing timestamp must defer
// to a later sweep rather than delete on an unstamped key.
//
// spec: 4.6.1 (orphan key requires a real timestamp).
func TestClassifyZeroKeyNeverReclaims(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC)
	g := &ClaimGarbageCollector{}

	// Empty status with a zero creationTimestamp: nothing to age against.
	cl := &lennyv1.SandboxClaim{}
	if got := g.classify(cl, now); got != reclaimSkip {
		t.Errorf("empty-status zero-creation claim = %v, want reclaimSkip", got)
	}
	// Bound with neither a transition stamp nor a creation time.
	cl = &lennyv1.SandboxClaim{Status: lennyv1.SandboxClaimStatus{Phase: string(claimstate.Bound)}}
	if got := g.classify(cl, now); got != reclaimSkip {
		t.Errorf("bound zero-key claim = %v, want reclaimSkip", got)
	}
}

// TestGCTimeoutDefaults asserts the §4.6.1 / §11.5 default windows are
// selected when the operator-tunable fields are unset, and the operator value
// otherwise.
//
// spec: 4.6.1 (claimOrphanTimeout default 5m, reserved-hold grace
// operator-tunable).
func TestGCTimeoutDefaults(t *testing.T) {
	t.Parallel()
	zero := &ClaimGarbageCollector{}
	if got := zero.orphanTimeout(); got != defaultClaimOrphanTimeout {
		t.Errorf("orphanTimeout default = %v, want %v", got, defaultClaimOrphanTimeout)
	}
	if got := zero.reservedHoldGrace(); got != defaultReservedHoldGrace {
		t.Errorf("reservedHoldGrace default = %v, want %v", got, defaultReservedHoldGrace)
	}
	set := &ClaimGarbageCollector{OrphanTimeout: 7 * time.Minute, ReservedHoldGrace: 90 * time.Second}
	if got := set.orphanTimeout(); got != 7*time.Minute {
		t.Errorf("orphanTimeout = %v, want 7m", got)
	}
	if got := set.reservedHoldGrace(); got != 90*time.Second {
		t.Errorf("reservedHoldGrace = %v, want 90s", got)
	}
}

// TestGCStartDisabledWithoutSessionsOrNamespaces asserts the §4.6.1 loop is
// disabled (returns immediately without sweeping) when no session source or
// no agent namespace is configured, so a controller without the
// Postgres-backed active-session lookup never deletes a claim it cannot
// classify.
//
// spec: 4.6.1 (the GC must never delete without a session source of truth).
func TestGCStartDisabledWithoutSessionsOrNamespaces(t *testing.T) {
	t.Parallel()
	// No Sessions oracle: disabled.
	g := &ClaimGarbageCollector{Namespaces: []string{"lenny-agents"}}
	if err := g.Start(context.Background()); err != nil {
		t.Errorf("Start with no Sessions = %v, want nil (disabled, no-op)", err)
	}
	// No namespaces: disabled.
	g = &ClaimGarbageCollector{Sessions: stubLookup{}}
	if err := g.Start(context.Background()); err != nil {
		t.Errorf("Start with no namespaces = %v, want nil (disabled, no-op)", err)
	}
}

// stubLookup is a no-op SessionLookup for the disabled-Start test; the loop
// returns before it is ever consulted.
type stubLookup struct{}

func (stubLookup) PodHasActiveSession(context.Context, string) (bool, error) { return false, nil }
