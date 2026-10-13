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
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	sessionpg "github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/pgstore"
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
	pg := containers.StartPostgres(t, containers.PostgresOptions{
		MigrationsDir: filepath.Join(schematest.RepoRoot(t), "migrations"),
	})
	seedChaosTenant(t, pg, lifecycleharness.Tenant)
	var store sessionstore.Store = sessionpg.New(pg.Pool)
	h := lifecycleharness.New(t, lifecycleharness.Options{Replicas: 2, Pods: 64, CredentialPool: true, Store: store})
	h.RelaxOrphanClaims = true

	// Clean run: record the distinct call sites the scenario reaches.
	h.Trace.Start()
	clean := faultScenario(t, h, h.Replicas[0])
	if st := h.Row(clean).State; st != session.StateRunning {
		t.Fatalf("clean run ended in %q, want running", st)
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
				id := faultScenario(t, h, r)
				h.RunToQuiescence()
				if id == "" {
					for _, v := range h.Violations() {
						t.Errorf("%s at %s: %s", kind, key, v)
					}
					return
				}
				switch st := h.Row(id).State; st {
				case session.StateRunning, session.StateAwaitingClientAction, session.StateResumePending,
					session.StateFailed, session.StateSuspended:
				default:
					t.Errorf("session ended in %q after a %s at %s", st, kind, key)
				}
				for _, v := range h.Violations() {
					t.Errorf("%s at %s: %s", kind, key, v)
				}
			})
		}
	}
}
