// SPDX-License-Identifier: MIT

package lifecycleharness

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
)

// Horizon is how far past its start the checker steps the clock before it
// may stop: the coordination lease TTL, the resuming budget, and the
// resume_pending window, each plus one tick.
const Horizon = LeaseTTL + MaxResuming + MaxResumePending + 3*SweepInterval

// Settle waits until the state the invariants read stops changing: three
// consecutive reads 20ms apart agree, or ten seconds pass.
func (h *Harness) Settle() {
	deadline := time.Now().Add(10 * time.Second)
	last, same := h.fingerprint(), 0
	for same < 3 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
		cur := h.fingerprint()
		if cur == last {
			same++
			continue
		}
		last, same = cur, 0
	}
}

// Pass steps the clock by one tick, runs each surviving replica's Sweeper
// and watchdog once, and settles.
func (h *Harness) Pass() {
	h.Clock.Advance(SweepInterval)
	ctx := context.Background()
	for _, r := range h.Surviving() {
		_, _ = r.Sweeper.Sweep(ctx)
		_, _ = r.Watchdog.Tick(ctx, h.Clock.Now())
	}
	h.Settle()
}

// Check runs passes until the clock has passed the horizon and two
// consecutive passes change nothing the invariants read, then asserts every
// invariant for every tracked session, each as its own subtest.
func (h *Harness) Check(t *testing.T) {
	t.Helper()
	h.RunToQuiescence()
	for _, inv := range h.Invariants() {
		t.Run(inv.Name, func(t *testing.T) {
			for _, v := range inv.Violations {
				t.Error(v)
			}
		})
	}
}

// RunToQuiescence stops the trace and runs checker passes until the clock
// has passed the horizon and two consecutive passes change nothing the
// invariants read.
func (h *Harness) RunToQuiescence() {
	h.Trace.Stop()
	h.Settle()
	stopAt := h.Clock.Now().Add(Horizon)
	unchanged := 0
	for pass := 0; pass < 200; pass++ {
		before := h.fingerprint()
		h.Pass()
		if !h.Clock.Now().After(stopAt) {
			continue
		}
		if h.fingerprint() == before {
			unchanged++
		} else {
			unchanged = 0
		}
		if unchanged >= 2 {
			return
		}
	}
}

// Invariant is one invariant clause and the violations the checker found.
type Invariant struct {
	Name       string
	Violations []string
}

// Violations returns every violation of every invariant, for a caller that
// is not a *testing.T, such as a rapid state machine.
func (h *Harness) Violations() []string {
	var out []string
	for _, inv := range h.Invariants() {
		for _, v := range inv.Violations {
			out = append(out, inv.Name+": "+v)
		}
	}
	return out
}

// fingerprint summarizes the state the invariants read, without lease
// expiry times.
func (h *Harness) fingerprint() string {
	ctx := context.Background()
	var b strings.Builder
	ids := h.Sessions()
	sort.Strings(ids)
	for _, id := range ids {
		row, err := h.Inner.Get(ctx, Tenant, id)
		if err != nil {
			fmt.Fprintf(&b, "%s:err;", id)
			continue
		}
		fmt.Fprintf(&b, "%s:%s:%d:%d:%d:%s;", id, row.State, row.CoordinationGeneration, row.RecoveryGeneration, row.RetryCount, row.PodAssignment)
		fmt.Fprintf(&b, "L%d;", len(h.CredLeases.LeasesBySession([]string{id})))
		if n, err := h.DLQ.Len(ctx, Tenant, id); err == nil {
			fmt.Fprintf(&b, "D%d;", n)
		}
		if n, err := h.Inbox.Len(ctx, Tenant, id); err == nil {
			fmt.Fprintf(&b, "I%d;", n)
		}
		for _, r := range h.Replicas {
			if bind, ok := r.Registry.Get(id); ok {
				fmt.Fprintf(&b, "B%s=%s@%d;", r.ID, bind.SandboxName, bind.CoordinationGeneration)
			}
		}
	}
	claims, _ := h.Cluster.BoundClaims(ctx)
	sort.Strings(claims)
	fmt.Fprintf(&b, "claims=%v", claims)
	return b.String()
}

// noLeaseStates are the states in which a session holds no credential lease
// (I3); a podless suspended row is checked separately.
var noLeaseStates = map[session.State]bool{
	session.StateCreated:              true,
	session.StateResumePending:        true,
	session.StateAwaitingClientAction: true,
}

// Invariants evaluates I1, I3, I4, I5, I6, and I7 for the tracked sessions.
func (h *Harness) Invariants() []Invariant {
	ctx := context.Background()
	ids := h.Sessions()
	sort.Strings(ids)
	held := map[string]bool{}
	rows := map[string]sessionRow{}
	for _, id := range ids {
		row, err := h.Inner.Get(ctx, Tenant, id)
		rows[id] = sessionRow{state: row.State, pod: row.PodAssignment, retries: row.RetryCount, err: err}
		if err == nil && !session.IsTerminal(row.State) && row.PodAssignment != "" {
			held[row.PodAssignment] = true
		}
	}
	var out []Invariant
	i1 := Invariant{Name: "I1 pods and claims"}
	if claims, err := h.Cluster.BoundClaims(ctx); err != nil {
		i1.Violations = append(i1.Violations, "list claims: "+err.Error())
	} else {
		bound := map[string]bool{}
		for _, r := range h.Surviving() {
			for _, b := range r.Registry.Snapshot() {
				bound[b.SandboxName] = true
			}
		}
		for _, pod := range claims {
			// spec: the crash relaxation of I1 — a claim a crash left is
			// accepted when no non-terminal row names its pod and no
			// surviving replica binds it; the claim-orphan GC reclaims it.
			if !held[pod] && h.RelaxOrphanClaims && !bound[pod] {
				continue
			}
			if !held[pod] {
				i1.Violations = append(i1.Violations, fmt.Sprintf("claim on %s is bound, but no non-terminal row names the pod", pod))
			}
		}
	}
	out = append(out, i1)
	for _, id := range ids {
		row := rows[id]
		if row.err != nil {
			out = append(out, Invariant{Name: "read " + id, Violations: []string{row.err.Error()}})
			continue
		}
		i3 := Invariant{Name: "I3 credential leases " + id}
		podless := row.state == session.StateSuspended && row.pod == ""
		if session.IsTerminal(row.state) || noLeaseStates[row.state] || podless {
			if n := len(h.CredLeases.LeasesBySession([]string{id})); n != 0 {
				i3.Violations = append(i3.Violations, fmt.Sprintf("%s session holds %d credential leases, want none", row.state, n))
			}
		}
		i4 := Invariant{Name: "I4 one coordinator " + id}
		i5 := Invariant{Name: "I5 replica records " + id}
		binders := 0
		for _, r := range h.Surviving() {
			if _, ok := r.Registry.Get(id); ok {
				binders++
				if session.IsTerminal(row.state) {
					i5.Violations = append(i5.Violations, fmt.Sprintf("replica %s binds the %s session", r.ID, row.state))
				}
			}
		}
		if binders > 1 {
			i4.Violations = append(i4.Violations, fmt.Sprintf("%d surviving replicas bind the session, want at most one", binders))
		}
		i6 := Invariant{Name: "I6 queues " + id}
		if session.IsTerminal(row.state) {
			if n, _ := h.DLQ.Len(ctx, Tenant, id); n != 0 {
				i6.Violations = append(i6.Violations, fmt.Sprintf("DLQ holds %d entries for the %s session", n, row.state))
			}
			if n, _ := h.Inbox.Len(ctx, Tenant, id); n != 0 {
				i6.Violations = append(i6.Violations, fmt.Sprintf("inbox holds %d entries for the %s session", n, row.state))
			}
		}
		i7 := Invariant{Name: "I7 retries " + id}
		if row.retries > MaxRetries {
			i7.Violations = append(i7.Violations, fmt.Sprintf("RetryCount = %d, want at most %d", row.retries, MaxRetries))
		}
		out = append(out, i3, i4, i5, i6, i7)
	}
	return out
}

type sessionRow struct {
	state   session.State
	pod     string
	retries int64
	err     error
}
