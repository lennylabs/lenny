// SPDX-License-Identifier: MIT

//go:build chaos

package tier8_chaos_test

import (
	"context"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	sessionpg "github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/pgstore"
	claimstate "github.com/lennylabs/lenny/pkg/sandboxclaim/state"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
	"github.com/lennylabs/lenny/tests/testinfra/lifecycleharness"
	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// terminalWriteKey returns the trace key of the DELETE handler's terminal
// write, read off a clean DELETE of a probe session on r.
func terminalWriteKey(t *testing.T, h *lifecycleharness.Harness, r *lifecycleharness.Replica) string {
	t.Helper()
	probe := h.StartSession(r)
	h.Trace.Start()
	if code, body := h.Do(r, http.MethodDelete, "/v1/sessions/"+probe, nil); code >= http.StatusBadRequest {
		t.Fatalf("probe DELETE = %d %v", code, body)
	}
	for _, e := range h.Trace.Entries() {
		if e.Call == "Update" && e.SessionID == probe {
			return e.Key()
		}
	}
	t.Fatal("the probe DELETE made no Update")
	return ""
}

// crashAfterTerminalCommit DELETEs the session on r with a crash of r armed
// right after the terminal write commits, before its post-terminal release.
func crashAfterTerminalCommit(t *testing.T, h *lifecycleharness.Harness, r *lifecycleharness.Replica, id, key string) {
	t.Helper()
	h.Trace.ArmAtKey(key, lifecycleharness.FaultCrash, func(lifecycleharness.TraceEntry, lifecycleharness.FaultKind) {
		h.Crash(r)
	})
	h.Do(r, http.MethodDelete, "/v1/sessions/"+id, nil)
	if !r.Crashed() {
		t.Fatal("the armed crash never fired")
	}
	if st := h.Row(id).State; !session.IsTerminal(st) {
		t.Fatalf("session = %s, want the committed terminal state", st)
	}
}

// spec: §4.6.1 (Orphaned SandboxClaim detection), §5.2 (Slot assignment
// atomicity), §7.2 (Interactive Session Model)
// diagnosis: a replica that exited between a session's terminal commit and
// its release left a residue other than the accepted one. On a concurrent
// pool the session's member must stay counted, which fails closed and is the
// stale-slot reaper's to release, and the claim must stay bound while a
// sibling lives; on an exclusive pool the bound claim must be left for the
// claim-orphan GC. A surviving replica that released the slot or deleted the
// claim through a binding it never held would be a release the spec does not
// define.
func TestCrashBetweenTerminalCommitAndRelease_spec_4_6_1(t *testing.T) {
	pg := containers.StartPostgres(t, containers.PostgresOptions{
		MigrationsDir: filepath.Join(schematest.RepoRoot(t), "migrations"),
	})
	seedChaosTenant(t, pg, lifecycleharness.Tenant)
	newStore := func(now func() time.Time) sessionstore.Store {
		return lifecycleharness.NewSteppedClockStore(sessionpg.New(pg.Pool), now)
	}

	t.Run("concurrent pool", func(t *testing.T) {
		h := lifecycleharness.New(t, lifecycleharness.Options{Replicas: 2, Pods: 3, MaxConcurrentSessions: 4, NewStore: newStore})
		h.RelaxStaleSlotMembers = true
		doomed, survivor := h.Replicas[0], h.Replicas[1]
		key := terminalWriteKey(t, h, doomed)
		sibling := h.StartSession(survivor)
		id := h.StartSession(doomed)
		pod := h.Row(id).PodAssignment
		if h.Row(sibling).PodAssignment != pod {
			t.Fatalf("the sibling is on %s, want the doomed session's pod %s", h.Row(sibling).PodAssignment, pod)
		}
		crashAfterTerminalCommit(t, h, doomed, id, key)
		h.RunToQuiescence()
		members, err := h.SlotMembers(context.Background(), pod)
		if err != nil {
			t.Fatal(err)
		}
		counted := false
		for _, m := range members {
			counted = counted || m == id
		}
		if !counted {
			t.Errorf("slot set of %s = %v, want the crashed release's member still counted", pod, members)
		}
		if phase, _, _ := h.Cluster.ClaimPhase(context.Background(), pod); phase != string(claimstate.Bound) {
			t.Errorf("claim of %s = %q, want bound while the sibling lives", pod, phase)
		}
		h.Check(t)
	})

	t.Run("exclusive pool", func(t *testing.T) {
		h := lifecycleharness.New(t, lifecycleharness.Options{Replicas: 2, Pods: 3, NewStore: newStore})
		h.RelaxOrphanClaims = true
		doomed := h.Replicas[0]
		key := terminalWriteKey(t, h, doomed)
		id := h.StartSession(doomed)
		pod := h.Row(id).PodAssignment
		crashAfterTerminalCommit(t, h, doomed, id, key)
		h.RunToQuiescence()
		if phase, ok, _ := h.Cluster.ClaimPhase(context.Background(), pod); !ok || phase != string(claimstate.Bound) {
			t.Errorf("claim of %s = %q (exists %v), want it left bound for the claim-orphan GC", pod, phase, ok)
		}
		h.Check(t)
	})
}

// spec: 6.2 (finalize timeout), 15.1 (finalize row), 5.2 (Slot assignment
// atomicity), 4.6.1 (Orphaned SandboxClaim detection)
// diagnosis: a finalize whose prepare phase failed on a concurrent pool, and
// whose failure write then reported a store error, released the session's
// slot other than the accepted way. When the write did not commit, the
// finalizing watchdog must end the session and release its member exactly
// once; when it committed, no hook runs and the member must stay counted for
// the stale-slot reaper, with the shared claim left in place for the sibling.
func TestFailFinalizingStoreError_spec_6_2(t *testing.T) {
	for _, tc := range []struct {
		name      string
		fault     lifecycleharness.FaultKind
		committed bool
	}{
		{"the failure write did not commit", lifecycleharness.FaultStoreError, false},
		{"the failure write committed", lifecycleharness.FaultCommitError, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Each case has its own Postgres: harness pod names repeat, so
			// a shared store would rehydrate one case's set from the other's
			// rows.
			pg := containers.StartPostgres(t, containers.PostgresOptions{
				MigrationsDir: filepath.Join(schematest.RepoRoot(t), "migrations"),
			})
			seedChaosTenant(t, pg, lifecycleharness.Tenant)
			newStore := func(now func() time.Time) sessionstore.Store {
				return lifecycleharness.NewSteppedClockStore(sessionpg.New(pg.Pool), now)
			}
			h := lifecycleharness.New(t, lifecycleharness.Options{
				Replicas: 1, Pods: 2, MaxConcurrentSessions: 4, NewStore: newStore, MaxFinalizingSeconds: 10,
				CredentialPool: true,
			})
			h.RelaxStaleSlotMembers = tc.committed
			r := h.Replicas[0]
			sibling := h.StartSession(r)
			target := h.CreateSession(r)
			pod := h.Row(target).PodAssignment
			// A lease recorded under the session stands in for the one a
			// partial credential assignment leaves, which only the
			// prepare-failure exit's revoke returns once a committed failure
			// write runs no hook.
			if _, err := h.Creds.Assign(lifecycleharness.ProxyPool().Name, target, "", lifecycleharness.Tenant, h.Row(target).CoordinationGeneration); err != nil {
				t.Fatalf("assign the finalize lease: %v", err)
			}
			// The pool no longer resolves, so finalize fails before Prepare,
			// the only pre-Prepare exit a concurrent pool reaches.
			if err := h.Cluster.Client.Delete(context.Background(), &lennyv1.SandboxWarmPool{
				ObjectMeta: metav1.ObjectMeta{Name: lifecycleharness.Pool, Namespace: lifecycleharness.Namespace},
			}); err != nil {
				t.Fatalf("delete the warm pool: %v", err)
			}
			// The fault lands on the finalize call's guarded failure write.
			h.Trace.ArmMatching("Update", "writeTerminal", tc.fault, nil)
			code, body := h.Do(r, http.MethodPost, "/v1/sessions/"+target+"/finalize", nil)
			if got := h.Row(target).State == session.StateFailed; got != tc.committed {
				t.Fatalf("target = %s after the faulted failure write, want committed %v", h.Row(target).State, tc.committed)
			}
			requirePrepareFailureExit(t, h, target, code, body)
			// The prepare-failure exit revokes the session's leases on every
			// outcome of its failure write. On the committed branch no hook
			// runs, so this revoke is the only lease return.
			if n := len(h.CredLeases.LeasesBySession([]string{target})); n != 0 {
				t.Errorf("target holds %d credential leases after the prepare-failure exit, want none", n)
			}
			h.RunToQuiescence()
			if st := h.Row(target).State; st != session.StateFailed {
				t.Fatalf("target = %s, want failed", st)
			}
			members, err := h.SlotMembers(context.Background(), pod)
			if err != nil {
				t.Fatal(err)
			}
			counted := false
			for _, m := range members {
				counted = counted || m == target
			}
			if counted != tc.committed {
				t.Errorf("target's member counted = %v (set %v), want %v", counted, members, tc.committed)
			}
			if !tc.committed {
				if n := h.Slots.Removals(pod, target); n != 1 {
					t.Errorf("removals of the target's member = %d, want 1 by the finalizing watchdog's release", n)
				}
			}
			if phase, _, _ := h.Cluster.ClaimPhase(context.Background(), pod); phase != string(claimstate.Bound) {
				t.Errorf("claim of %s = %q, want bound while the sibling %s lives", pod, phase, sibling)
			}
			h.Check(t)
		})
	}
}

// requirePrepareFailureExit fails unless the finalize call left through the
// prepare-failure exit: the trace holds the call's guarded failure write, and
// the answer is the pod-claim error that exit writes rather than the 500
// INTERNAL_ERROR the plan-parse and ready-write exits answer.
func requirePrepareFailureExit(t *testing.T, h *lifecycleharness.Harness, id string, code int, body map[string]any) {
	t.Helper()
	wrote := false
	for _, e := range h.Trace.Entries() {
		wrote = wrote || (e.SessionID == id && e.Call == "Update" && strings.Contains(e.Site, "writeTerminal"))
	}
	if !wrote {
		t.Fatalf("the finalize call made no guarded failure write (answer %d %v)", code, body)
	}
	envelope, _ := body["error"].(map[string]any)
	if got, _ := envelope["code"].(string); code < http.StatusBadRequest || got == "" || got == "INTERNAL_ERROR" {
		t.Fatalf("finalize answered %d %v, want the prepare-failure exit's pod-claim error", code, body)
	}
}
