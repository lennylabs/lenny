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
// and watchdog once and the gateway leader's backlog re-check on the first
// surviving replica, which the harness treats as the leader, and settles.
func (h *Harness) Pass() {
	h.Clock.Advance(SweepInterval)
	ctx := context.Background()
	surviving := h.Surviving()
	for _, r := range surviving {
		_, _ = r.Sweeper.Sweep(ctx)
		_, _ = r.Watchdog.Tick(ctx, h.Clock.Now())
	}
	if len(surviving) > 0 {
		h.backlogRechecker(surviving[0]).Pass(ctx)
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
// (§6.1 per-session credential lease lifecycle, §7.3 credential leases on the
// replacement pod); a podless suspended row is checked separately.
var noLeaseStates = map[session.State]bool{
	session.StateCreated:              true,
	session.StateResumePending:        true,
	session.StateAwaitingClientAction: true,
}

// Invariants evaluates the lifecycle invariants for the tracked sessions:
// every bound claim's pod is named by a non-terminal row (pods and claims);
// a session in a lease-free state holds no credential lease (credential
// leases); at most one surviving replica binds a session (one coordinator);
// no surviving replica binds a terminal session (replica records); a
// terminal session's inbox and DLQ are empty (queues); and RetryCount stays
// at or under the retry cap (retries).
//
// spec: §7.2 (Interactive Session Model), §7.3 (Resume driver), §10.1.1
// (one coordinating replica per session), §6.1 (per-session credential lease
// lifecycle)
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
	podsClaims := Invariant{Name: "pods and claims"}
	if claims, err := h.Cluster.BoundClaims(ctx); err != nil {
		podsClaims.Violations = append(podsClaims.Violations, "list claims: "+err.Error())
	} else {
		bound := map[string]bool{}
		for _, r := range h.Surviving() {
			for _, b := range r.Registry.Snapshot() {
				bound[b.SandboxName] = true
			}
		}
		for _, pod := range claims {
			// spec: §4.6.1 (Orphaned SandboxClaim detection), §7.3 — the
			// crash relaxation of the pods-and-claims invariant: a claim a
			// crash left is accepted when no non-terminal row names its pod
			// and no surviving replica binds it; the claim-orphan GC
			// reclaims it.
			if !held[pod] && h.RelaxOrphanClaims && !bound[pod] {
				continue
			}
			if !held[pod] {
				podsClaims.Violations = append(podsClaims.Violations, fmt.Sprintf("claim on %s is bound, but no non-terminal row names the pod", pod))
			}
		}
	}
	out = append(out, podsClaims)
	for _, id := range ids {
		row := rows[id]
		if row.err != nil {
			out = append(out, Invariant{Name: "read " + id, Violations: []string{row.err.Error()}})
			continue
		}
		leases := Invariant{Name: "credential leases " + id}
		podless := row.state == session.StateSuspended && row.pod == ""
		if session.IsTerminal(row.state) || noLeaseStates[row.state] || podless {
			if n := len(h.CredLeases.LeasesBySession([]string{id})); n != 0 {
				leases.Violations = append(leases.Violations, fmt.Sprintf("%s session holds %d credential leases, want none", row.state, n))
			}
		}
		coordinator := Invariant{Name: "one coordinator " + id}
		records := Invariant{Name: "replica records " + id}
		binders := 0
		for _, r := range h.Surviving() {
			if _, ok := r.Registry.Get(id); ok {
				binders++
				if session.IsTerminal(row.state) {
					records.Violations = append(records.Violations, fmt.Sprintf("replica %s binds the %s session", r.ID, row.state))
				}
			}
		}
		if binders > 1 {
			coordinator.Violations = append(coordinator.Violations, fmt.Sprintf("%d surviving replicas bind the session, want at most one", binders))
		}
		queues := Invariant{Name: "queues " + id}
		if session.IsTerminal(row.state) {
			if n, _ := h.DLQ.Len(ctx, Tenant, id); n != 0 {
				queues.Violations = append(queues.Violations, fmt.Sprintf("DLQ holds %d entries for the %s session", n, row.state))
			}
			if n, _ := h.Inbox.Len(ctx, Tenant, id); n != 0 {
				queues.Violations = append(queues.Violations, fmt.Sprintf("inbox holds %d entries for the %s session", n, row.state))
			}
		}
		retries := Invariant{Name: "retries " + id}
		if row.retries > MaxRetries {
			retries.Violations = append(retries.Violations, fmt.Sprintf("RetryCount = %d, want at most %d", row.retries, MaxRetries))
		}
		out = append(out, leases, coordinator, records, queues, retries)
	}
	return out
}

type sessionRow struct {
	state   session.State
	pod     string
	retries int64
	err     error
}
