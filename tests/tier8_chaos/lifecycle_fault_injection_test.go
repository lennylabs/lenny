// SPDX-License-Identifier: MIT

//go:build chaos

// Tier-8 fault injection over the lifecycle harness on a Postgres session
// store. A clean run of a scenario records the trace of the shared store's
// calls; the scenario is then re-run once for each distinct call site the
// clean trace reaches and each fault kind (store error, context cancel, crash
// of the calling replica, and peer takeover at that call), and after each
// re-run the session reaches the state the staged spec gives for the
// interruption and the invariant checker holds.
//
// spec: §10.1.2 (Coordinator Handoff Protocol), §7.3 (Resume driver), §7.2
// (Interactive Session Model), §12.3 (Postgres HA Requirements)
package tier8_chaos_test

import (
	"context"
	"fmt"
	"net/http"
	"path/filepath"
	"sort"
	"testing"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
	"github.com/lennylabs/lenny/tests/testinfra/lifecycleharness"
	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// faultScenario starts a session on the replica, files a crash report, and
// lets the resume driver restore it.
func faultScenario(t *testing.T, h *lifecycleharness.Harness, r *lifecycleharness.Replica) string {
	t.Helper()
	id, code, body := h.TryStartSession(r)
	if code != http.StatusCreated {
		// A fault in the create answers a retryable error; the session
		// either was never created or is left for the checker.
		if retryable, _ := body["error"].(map[string]any)["retryable"].(bool); !retryable && code != http.StatusConflict {
			t.Errorf("a create the fault interrupted answered %d %v, want a retryable error", code, body)
		}
		return id
	}
	if _, err := r.Server.ReportSessionFailure(context.Background(), sessionserver.FailureReport{
		TenantID: lifecycleharness.Tenant, SessionID: id, Reason: string(session.FailureRuntimeCrash),
	}); err != nil {
		t.Logf("ReportSessionFailure: %v", err)
	}
	for i := 0; i < 6; i++ {
		h.Pass()
	}
	return id
}

// liveReplica returns the first replica that has not crashed, first adding
// replicas until two survive, so a crash leaves a peer to run the loops.
func liveReplica(h *lifecycleharness.Harness, n *int) *lifecycleharness.Replica {
	for len(h.Surviving()) < 2 {
		*n++
		h.AddReplica(fmt.Sprintf("rep-extra-%d", *n))
	}
	return h.Surviving()[0]
}

// spec: §10.1.2 (Coordinator Handoff Protocol), §7.3 (Resume driver), §7.2
// (Interactive Session Model), §12.3 (Postgres HA Requirements)
// diagnosis: a failure names the call site, the fault kind, and the state the
// session reached, or the invariant a path broke when it was interrupted
// between two effects: a leaked claim, lease, or queue entry, a doubled
// coordinator, or a retry spent twice.
func TestLifecycleFaultInjection_spec_10_1_2(t *testing.T) {
	runFaultInjection(t, faultScenario, []session.State{
		session.StateRunning, session.StateAwaitingClientAction, session.StateResumePending,
		session.StateFailed, session.StateSuspended,
	})
}

// idleSuspensionScenario starts a session on the replica, suspends it for
// idleness, and sends it a message, which resumes it.
func idleSuspensionScenario(t *testing.T, h *lifecycleharness.Harness, r *lifecycleharness.Replica) string {
	t.Helper()
	id, code, _ := h.TryStartSession(r)
	if code != http.StatusCreated {
		return id
	}
	h.SuspendIdle(r, id)
	h.Send(r, id, "after-suspension")
	for i := 0; i < 6; i++ {
		h.Pass()
	}
	return id
}

// spec: §6.2 (maxClientIdleSeconds clock behavior across states), §7.2
// (Message delivery routing — seven paths, path 6), §10.1.2 (Coordinator
// Handoff Protocol)
// diagnosis: an idle suspension or the resume a held message triggers,
// interrupted between two of its effects by a store error, a context
// cancel, a crash, or a takeover, left the session in a state the staged
// spec does not give for the interruption, or leaked a claim, lease, or
// queue entry.
func TestIdleSuspensionFaultInjection_spec_6_2(t *testing.T) {
	runFaultInjection(t, idleSuspensionScenario, []session.State{
		session.StateRunning, session.StateSuspended, session.StateResumePending,
		session.StateResuming, session.StateAwaitingClientAction, session.StateFailed,
	})
}

// runFaultInjection records the distinct call sites a clean run of scenario
// reaches, then re-runs it once per call site and fault kind, and requires
// each re-run to end in one of allowed with no invariant violation.
func runFaultInjection(t *testing.T, scenario func(*testing.T, *lifecycleharness.Harness, *lifecycleharness.Replica) string, allowed []session.State) {
	pg := containers.StartPostgres(t, containers.PostgresOptions{
		MigrationsDir: filepath.Join(schematest.RepoRoot(t), "migrations"),
	})
	seedChaosTenant(t, pg, lifecycleharness.Tenant)
	h := lifecycleharness.New(t, lifecycleharness.Options{
		Replicas: 2, Pods: 96, CredentialPool: true,
		NewStore: lifecycleharness.PostgresStoreOn(pg.Pool),
	})
	h.RelaxOrphanClaims = true
	ok := map[session.State]bool{}
	for _, st := range allowed {
		ok[st] = true
	}

	// Clean run: record the distinct call sites the scenario reaches.
	h.Trace.Start()
	clean := scenario(t, h, h.Replicas[0])
	if st := h.Row(clean).State; !ok[st] {
		t.Fatalf("clean run ended in %q", st)
	}
	seen := map[string]bool{}
	var keys []string
	for _, e := range h.Trace.Entries() {
		if e.SessionID == "" || seen[e.Key()] {
			continue
		}
		seen[e.Key()] = true
		keys = append(keys, e.Key())
	}
	sort.Strings(keys)
	t.Logf("%d distinct call sites", len(keys))

	extra := 0
	for _, key := range keys {
		for _, kind := range []lifecycleharness.FaultKind{
			lifecycleharness.FaultStoreError, lifecycleharness.FaultContextCancel,
			lifecycleharness.FaultCrash, lifecycleharness.FaultPeerTakeover,
		} {
			t.Run(key+"/"+kind.String(), func(t *testing.T) {
				r := liveReplica(h, &extra)
				h.Trace.ArmAtKey(key, kind, func(e lifecycleharness.TraceEntry, k lifecycleharness.FaultKind) {
					switch k {
					case lifecycleharness.FaultCrash:
						h.Crash(r)
					case lifecycleharness.FaultPeerTakeover:
						h.TakeOver(e.SessionID, r)
					}
				})
				id := scenario(t, h, r)
				h.RunToQuiescence()
				if id != "" {
					if st := h.Row(id).State; !ok[st] {
						t.Errorf("session ended in %q after a %s at %s", st, kind, key)
					}
				}
				for _, v := range h.Violations() {
					t.Errorf("%s at %s: %s", kind, key, v)
				}
			})
		}
	}
}
