// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podclaim"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// reclaimRecordingAssigner records ReleaseSession calls so a test can assert
// the §7.1 step-23 lease revoke ran (or did not) during a pre-running terminal
// reclaim. AssignProto is unused on the reclaim path; it returns an empty lease.
type reclaimRecordingAssigner struct {
	released []string
}

func (a *reclaimRecordingAssigner) AssignProto(_, _, _, _ string) (*adapterv1.CredentialLease, error) {
	return nil, nil
}

func (a *reclaimRecordingAssigner) ReleaseSession(sessionID string) {
	a.released = append(a.released, sessionID)
}

// reclaimTestServer builds a white-box Server wired with a fake-client binder
// (holding a seeded per-pod SandboxClaim for podName), a live registry, and a
// recording credential assigner, so a test can exercise the terminal reclaim
// dispatch without an envtest apiserver. It returns the server, the fake kube
// client, and the assigner.
func reclaimTestServer(t *testing.T, podName string) (*Server, client.Client, *reclaimRecordingAssigner) {
	t.Helper()
	const ns = "lenny-agents"
	claim := &lennyv1.SandboxClaim{
		ObjectMeta: metav1.ObjectMeta{Name: podclaim.ClaimName(podName), Namespace: ns},
	}
	c := fake.NewClientBuilder().WithScheme(claimAtCreateScheme(t)).WithObjects(claim).Build()
	assigner := &reclaimRecordingAssigner{}
	reg := podsession.NewRegistry()
	s := &Server{
		podBinder:   &podsession.Binder{Client: c, Namespace: ns, Credentials: assigner},
		podRegistry: reg,
	}
	return s, c, assigner
}

func claimExists(t *testing.T, c client.Client, podName string) bool {
	t.Helper()
	const ns = "lenny-agents"
	var sc lennyv1.SandboxClaim
	err := c.Get(context.Background(), client.ObjectKey{Namespace: ns, Name: podclaim.ClaimName(podName)}, &sc)
	if err == nil {
		return true
	}
	if apierrors.IsNotFound(err) {
		return false
	}
	t.Fatalf("get claim: %v", err)
	return false
}

// spec: §15.1 line 620 (/terminate releases the pod), §4.6 (durable binding),
// §6.2 (pre-attached disposition), §7.1 step 23 (lease release) — a session
// terminated in created/finalizing/ready holds a pod claimed at /create but no
// live BindResult, so terminalReclaimPreRunning deletes the per-pod claim and
// revokes the lease from the persisted binding alone, returning true so the
// caller skips the no-bind executor release.
func TestTerminalReclaimPreRunningReleasesPodAndLease(t *testing.T) {
	s, c, assigner := reclaimTestServer(t, "sbx-pre")

	ran := s.terminalReclaimPreRunning(context.Background(), session.StateFinalizing, sessionstore.Session{
		ID: "sess-pre", TenantID: "acme", PodAssignment: "sbx-pre", PoolRef: "pool-a",
	})
	if !ran {
		t.Fatalf("terminalReclaimPreRunning = false, want true (a pre-running terminal session is reclaimed)")
	}
	if claimExists(t, c, "sbx-pre") {
		t.Errorf("per-pod claim still present after reclaim, want deleted (pod returned to pool)")
	}
	// The finalize-assigned lease is revoked; ReleaseSession is a no-op for a
	// created session that never assigned one, so the unconditional revoke is
	// still correct (mandatory revoke for finalizing/ready).
	if len(assigner.released) != 1 || assigner.released[0] != "sess-pre" {
		t.Errorf("ReleaseSession calls = %v, want [sess-pre]", assigner.released)
	}
}

// spec: §4.6 (durable binding) — a created/finalizing/ready session that
// nonetheless holds a live BindResult in the registry (an SDK-warm preConnect
// pod attached at finalize) is released through the binder's Release on the
// executor path. terminalReclaimPreRunning must not reclaim it by name: it
// returns false and leaves the claim untouched so the executor release is not
// double-driven.
func TestTerminalReclaimPreRunningSkipsLocallyBoundSession(t *testing.T) {
	s, c, assigner := reclaimTestServer(t, "sbx-bound")
	// A live BindResult marks the session as attached on this replica.
	s.podRegistry.Put(&podsession.BindResult{SessionID: "sess-bound", TenantID: "acme", SandboxName: "sbx-bound"})

	ran := s.terminalReclaimPreRunning(context.Background(), session.StateReady, sessionstore.Session{
		ID: "sess-bound", TenantID: "acme", PodAssignment: "sbx-bound",
	})
	if ran {
		t.Fatalf("terminalReclaimPreRunning = true for a locally-bound session, want false (executor path owns the release)")
	}
	if !claimExists(t, c, "sbx-bound") {
		t.Errorf("per-pod claim deleted for a locally-bound session, want untouched (the executor release owns it)")
	}
	if len(assigner.released) != 0 {
		t.Errorf("ReleaseSession calls = %v, want none (the executor release revokes the lease)", assigner.released)
	}
}

// spec: §4.6 (durable binding, scoped to created/finalizing/ready); §6.2
// (recycle disposition); §10.1 (running-session handoff) — a running session
// always carries a persisted PodAssignment (set at create-time claim and
// /start), and the per-replica registry misses it after a coordinator handoff.
// terminalReclaimPreRunning must NOT reclaim such a session by name even though
// this replica holds no live BindResult: the by-name claim DELETE would bypass
// the §6.2 recycle disposition the binder's Release applies. Gating on the
// pre-terminal state (running) keeps it on the executor release path. This is
// the regression the design-conformance review identified: a maxSessionAge-
// expired handed-off running session must not be retired by the pre-running
// reclaim.
func TestTerminalReclaimPreRunningSkipsHandedOffRunningSession(t *testing.T) {
	s, c, assigner := reclaimTestServer(t, "sbx-run")
	// No registry entry: the running session's BindResult lives on another
	// replica (coordinator handoff), so this replica's registry misses it.

	ran := s.terminalReclaimPreRunning(context.Background(), session.StateRunning, sessionstore.Session{
		ID: "sess-run", TenantID: "acme", PodAssignment: "sbx-run", PoolRef: "pool-a",
	})
	if ran {
		t.Fatalf("terminalReclaimPreRunning = true for a handed-off running session, want false " +
			"(the executor recycle path owns the release; the by-name reclaim would bypass the §6.2 recycle disposition)")
	}
	if !claimExists(t, c, "sbx-run") {
		t.Errorf("per-pod claim deleted for a running session, want untouched (the §6.2 recycle path owns it)")
	}
	if len(assigner.released) != 0 {
		t.Errorf("ReleaseSession calls = %v, want none (the running-session reclaim does not run the by-name lease revoke)", assigner.released)
	}
}

// spec: §4.6 — a resuming session is likewise excluded from the by-name reclaim
// even when this replica holds no live BindResult (the §10.4 handoff case).
func TestTerminalReclaimPreRunningSkipsResumingSession(t *testing.T) {
	s, c, _ := reclaimTestServer(t, "sbx-resume")

	ran := s.terminalReclaimPreRunning(context.Background(), session.StateResuming, sessionstore.Session{
		ID: "sess-resume", TenantID: "acme", PodAssignment: "sbx-resume", PoolRef: "pool-a",
	})
	if ran {
		t.Fatalf("terminalReclaimPreRunning = true for a resuming session, want false")
	}
	if !claimExists(t, c, "sbx-resume") {
		t.Errorf("per-pod claim deleted for a resuming session, want untouched")
	}
}

// spec: §4.6 — a starting session is mid-launch on the launching replica, which
// holds (or is establishing) a live BindResult, so its teardown follows the
// §6.2 executor recycle path. terminalReclaimPreRunning excludes starting from
// the by-name reclaim scope.
func TestTerminalReclaimPreRunningSkipsStartingSession(t *testing.T) {
	s, c, _ := reclaimTestServer(t, "sbx-start")

	ran := s.terminalReclaimPreRunning(context.Background(), session.StateStarting, sessionstore.Session{
		ID: "sess-start", TenantID: "acme", PodAssignment: "sbx-start", PoolRef: "pool-a",
	})
	if ran {
		t.Fatalf("terminalReclaimPreRunning = true for a starting session, want false")
	}
	if !claimExists(t, c, "sbx-start") {
		t.Errorf("per-pod claim deleted for a starting session, want untouched")
	}
}

// spec: §4.6 (durable binding) — a session row with no persisted pod binding
// (PodAssignment empty: a service-mode or claimless session) has nothing to
// reclaim, so terminalReclaimPreRunning returns false and falls through to the
// executor path.
func TestTerminalReclaimPreRunningNoPodBindingIsSkipped(t *testing.T) {
	s, _, assigner := reclaimTestServer(t, "sbx-none")

	ran := s.terminalReclaimPreRunning(context.Background(), session.StateCreated, sessionstore.Session{
		ID: "sess-none", TenantID: "acme", PodAssignment: "",
	})
	if ran {
		t.Fatalf("terminalReclaimPreRunning = true for a row with no pod binding, want false")
	}
	if len(assigner.released) != 0 {
		t.Errorf("ReleaseSession calls = %v, want none (no binding to reclaim)", assigner.released)
	}
}

// spec: §4.5 (proposal) — the in-memory dev/test gateway runs without a pod
// binder or registry, where there is no claimed pod to reclaim, so
// terminalReclaimPreRunning is a no-op that reports false (the caller falls
// through to the executor path, which is the echo/subprocess teardown).
func TestTerminalReclaimPreRunningNoBinderIsSkipped(t *testing.T) {
	s := &Server{} // podBinder and podRegistry nil
	ran := s.terminalReclaimPreRunning(context.Background(), session.StateCreated, sessionstore.Session{
		ID: "sess-x", TenantID: "acme", PodAssignment: "sbx-x",
	})
	if ran {
		t.Fatalf("terminalReclaimPreRunning = true with no binder, want false")
	}
}

// spec: §4.6 — a created session that holds a pod claim but is force-terminated
// while the gateway has a binder reclaims the pod by name. This pins the
// created state (the most common pre-running abandon case) to the by-name
// reclaim path so the §4.5 created-expiry sweeper and /terminate share it.
func TestTerminalReclaimPreRunningCreatedStateReclaims(t *testing.T) {
	s, c, assigner := reclaimTestServer(t, "sbx-created")

	ran := s.terminalReclaimPreRunning(context.Background(), session.StateCreated, sessionstore.Session{
		ID: "sess-created", TenantID: "acme", PodAssignment: "sbx-created", PoolRef: "pool-a",
	})
	if !ran {
		t.Fatalf("terminalReclaimPreRunning = false for a created session, want true")
	}
	if claimExists(t, c, "sbx-created") {
		t.Errorf("per-pod claim still present after reclaim of a created session, want deleted")
	}
	// A created session never assigned a lease, but the revoke is still issued
	// (a no-op keyed by sessionID), so the path fails closed for the
	// finalizing/ready case that always holds one.
	if len(assigner.released) != 1 || assigner.released[0] != "sess-created" {
		t.Errorf("ReleaseSession calls = %v, want [sess-created]", assigner.released)
	}
}
