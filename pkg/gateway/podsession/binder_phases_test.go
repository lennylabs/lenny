// SPDX-License-Identifier: MIT

package podsession_test

import (
	"context"
	"errors"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/lennylabs/lenny/pkg/adapter"
	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
	"github.com/lennylabs/lenny/pkg/gateway/podsession"
	claimstate "github.com/lennylabs/lenny/pkg/sandboxclaim/state"
)

// startFailRuntime is a fakeRuntime whose Start RPC fails, so the adapter's
// StartSession returns an error and the binder's Launch phase aborts after
// the §4.9 credential lease was already assigned at Prepare. It exercises the
// Launch-phase lease-aware reclaim (Gap 2).
type startFailRuntime struct {
	fakeRuntime
}

func (r *startFailRuntime) Start(context.Context, string) error {
	return errors.New("runtime refused to start")
}

// claimAndPrepare runs the §7.1 step-4 claim and the §4.3 prepare barrier
// against srv-backed sandbox sbx-1, returning the binder and the claim so a
// test can drive Launch (or a reclaim) from the persisted binding.
func claimAndPrepare(t *testing.T, binder *podsession.Binder, req podsession.BindRequest) *podsession.ClaimResult {
	t.Helper()
	claim, err := binder.Claim(context.Background(), req)
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	req.SandboxName = claim.SandboxName
	if _, err := binder.Prepare(context.Background(), req); err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	return claim
}

// spec: §4.1 (proposal), §7.1 step 4 — Claim reserves an idle warm pod, runs
// the §15.5 handshake, and returns the binding (sandbox name, pool, pod IP,
// negotiated workspace root) for the gateway to persist at /create, recording
// the §6.3 pod-claim timing. It does not run the setup chain or start the
// runtime, so the pod sits reserved and idle after a successful claim.
// diagnosis: a failure means the at-create claim does not produce a durable
// binding, so the decomposed §7.1 lifecycle cannot reconnect to the pod and
// pool exhaustion is not surfaced at /create.
func TestClaimReservesPodWithoutStarting_spec_7_1(t *testing.T) {
	rt := &fakeRuntime{}
	srv := adapter.New("adapter-test")
	srv.WorkspaceRoot = t.TempDir()
	srv.Runtime = rt

	c := k8sClient(t, idleSandbox("sbx-1", "10.244.1.7"))
	binder := newBinder(c, adapterDialer(t, srv))

	claim, err := binder.Claim(context.Background(), podsession.BindRequest{
		Pool: testPool, SessionID: "sess-1", TenantID: "acme", Runtime: "claude-code",
	})
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if claim.SandboxName != "sbx-1" || claim.PodIP != "10.244.1.7" || claim.Pool != testPool {
		t.Errorf("claim = %+v, want sbx-1 / 10.244.1.7 / %s", claim, testPool)
	}
	if claim.PodClaim <= 0 {
		t.Errorf("claim recorded no pod-claim timing: %v", claim.PodClaim)
	}
	// The runtime must not have started: Claim only reserves the pod.
	if rt.started != "" {
		t.Errorf("Claim started the runtime %q, want no start (claim only reserves)", rt.started)
	}
	// The pod is claimed: the per-pod occupancy claim records `bound`.
	var sc lennyv1.SandboxClaim
	if err := c.Get(context.Background(), client.ObjectKey{Namespace: testNS, Name: "claim-sbx-1"}, &sc); err != nil {
		t.Fatalf("get per-pod claim after Claim: %v", err)
	}
	if sc.Status.Phase != string(claimstate.Bound) {
		t.Errorf("claim binding state = %q, want bound", sc.Status.Phase)
	}
}

// spec: §4.6 (proposal) — Claim, Prepare, and Launch each reconnect to the
// claimed pod from the persisted binding rather than holding one connection
// across the window. Driven independently with the claim threaded through, the
// three phases place a session on the pod exactly as the monolithic Bind does:
// the runtime starts and the claim records `bound`.
// diagnosis: a failure means a phase depends on a connection a prior phase
// held, so the durable-binding / coordinator-handoff guarantee (§4.6) does not
// hold and a handoff mid-window would orphan the pod.
func TestPhasesReconnectFromBinding_spec_4_6(t *testing.T) {
	rt := &fakeRuntime{}
	srv := adapter.New("adapter-test")
	srv.WorkspaceRoot = t.TempDir()
	srv.Runtime = rt

	c := k8sClient(t, idleSandbox("sbx-1", "10.244.1.7"))
	binder := newBinder(c, adapterDialer(t, srv))

	req := podsession.BindRequest{
		Pool: testPool, SessionID: "sess-1", TenantID: "acme", Runtime: "claude-code",
	}
	claim, err := binder.Claim(context.Background(), req)
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	req.SandboxName = claim.SandboxName
	prep, err := binder.Prepare(context.Background(), req)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	req.Demoted = prep.Demoted
	res, err := binder.Launch(context.Background(), req)
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	defer res.Adapter.Close()

	if res.SandboxName != "sbx-1" {
		t.Errorf("launch result sandbox = %q, want sbx-1", res.SandboxName)
	}
	if res.Adapter == nil {
		t.Fatal("Launch returned no live adapter connection")
	}
	if rt.started != "sess-1" {
		t.Errorf("runtime started for %q, want sess-1", rt.started)
	}
}

// spec: §4.3 (proposal), §7.1 step 23 (lease release) — when a Launch step
// fails after the §4.9 credential lease was assigned at Prepare, the reclaim
// drains the pod AND revokes the lease back to its pool (Gap 2), so the
// credential's active-session slot does not leak for the abandoned session.
// diagnosis: a failure means a post-AssignCredentials launch abort leaks the
// credential lease, drifting the §4.9 pool's per-credential active counter up
// until select.go reports exhaustion for idle credentials.
func TestLaunchFailureRevokesAssignedLease_spec_7_1(t *testing.T) {
	srv := adapter.New("adapter-test")
	srv.WorkspaceRoot = t.TempDir()
	srv.CredentialsDir = t.TempDir()
	srv.Runtime = &startFailRuntime{}

	c := k8sClient(t, idleSandbox("sbx-1", "10.244.1.7"))
	binder := newBinder(c, adapterDialer(t, srv))
	assigner := &fakeAssigner{}
	binder.Credentials = assigner

	req := podsession.BindRequest{
		Pool: testPool, SessionID: "sess-leak", TenantID: "acme", Runtime: "claude-code",
		CredentialPools: map[string]string{"anthropic": "claude-prod"},
	}
	claim, err := binder.Claim(context.Background(), req)
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	req.SandboxName = claim.SandboxName
	prep, err := binder.Prepare(context.Background(), req)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	// Prepare assigned the lease; the assigner has not released it yet.
	if len(assigner.calls) != 1 {
		t.Fatalf("assigner served %d assign calls after Prepare, want 1", len(assigner.calls))
	}
	if len(assigner.released) != 0 {
		t.Fatalf("assigner released %v before Launch, want none", assigner.released)
	}

	req.Demoted = prep.Demoted
	if _, err := binder.Launch(context.Background(), req); err == nil {
		t.Fatal("Launch succeeded though the runtime refused to start, want a failure")
	}

	// Gap 2: the launch reclaim revoked the lease assigned at Prepare.
	if len(assigner.released) != 1 || assigner.released[0] != "sess-leak" {
		t.Errorf("ReleaseSession calls after launch failure = %v, want [sess-leak]", assigner.released)
	}
	// The pod is reclaimed by deleting its per-pod claim.
	var sc lennyv1.SandboxClaim
	gerr := c.Get(context.Background(), client.ObjectKey{Namespace: testNS, Name: "claim-sbx-1"}, &sc)
	if !apierrors.IsNotFound(gerr) {
		t.Errorf("per-pod claim get after launch failure = %v, want NotFound (claim deleted)", gerr)
	}
}

// spec: §4.3 (proposal) — a Prepare-phase step that fails BEFORE
// assignCredentials runs reclaims the pod without revoking a lease (the
// lease-assigned flag is still false), so the revoke is a no-op. A
// FinalizeWorkspace failure (no adapter workspace root) exercises this path.
// diagnosis: a failure means the pre-credential reclaim either fails to drain
// the pod or spuriously calls ReleaseSession for a session that never held a
// lease.
func TestPrepareFailureBeforeCredentialsDoesNotRevoke_spec_4_3(t *testing.T) {
	// No WorkspaceRoot: FinalizeWorkspace fails before assignCredentials.
	srv := adapter.New("adapter-test")
	srv.Runtime = &fakeRuntime{}

	c := k8sClient(t, idleSandbox("sbx-1", "10.244.1.7"))
	binder := newBinder(c, adapterDialer(t, srv))
	assigner := &fakeAssigner{}
	binder.Credentials = assigner

	req := podsession.BindRequest{
		Pool: testPool, SessionID: "sess-1", TenantID: "acme", Runtime: "claude-code",
		CredentialPools: map[string]string{"anthropic": "claude-prod"},
	}
	claim, err := binder.Claim(context.Background(), req)
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	req.SandboxName = claim.SandboxName
	if _, err := binder.Prepare(context.Background(), req); err == nil {
		t.Fatal("Prepare succeeded though FinalizeWorkspace could not run, want a failure")
	}

	// The lease was never assigned, so the reclaim must not call ReleaseSession.
	if len(assigner.calls) != 0 {
		t.Errorf("assigner served %d assign calls, want 0 (failure precedes assignment)", len(assigner.calls))
	}
	if len(assigner.released) != 0 {
		t.Errorf("reclaim released %v for a session that holds no lease, want none", assigner.released)
	}
	// The pod is still reclaimed by deleting its per-pod claim.
	var sc lennyv1.SandboxClaim
	gerr := c.Get(context.Background(), client.ObjectKey{Namespace: testNS, Name: "claim-sbx-1"}, &sc)
	if !apierrors.IsNotFound(gerr) {
		t.Errorf("per-pod claim get after prepare failure = %v, want NotFound (claim deleted)", gerr)
	}
}

// spec: §4.5 (proposal), §4.6 (proposal), §7.1 step 23 — ReclaimClaimed
// releases a pod held by a created/finalizing/ready session that has no live
// BindResult (the created-expiry sweeper or a /terminate path) from the
// persisted SandboxName + sessionID alone: it deletes the per-pod claim and
// revokes the session's §4.9 lease.
// diagnosis: a failure means the created-expiry / pre-running terminate path
// leaks the claimed pod or the finalize-assigned credential lease, because the
// claimless reclaim entry point does not return both to the pool.
func TestReclaimClaimedReleasesPodAndLease_spec_4_5(t *testing.T) {
	srv := adapter.New("adapter-test")
	srv.WorkspaceRoot = t.TempDir()
	srv.CredentialsDir = t.TempDir()
	srv.Runtime = &fakeRuntime{}

	c := k8sClient(t, idleSandbox("sbx-1", "10.244.1.7"))
	binder := newBinder(c, adapterDialer(t, srv))
	assigner := &fakeAssigner{}
	binder.Credentials = assigner

	// A finalizing/ready session: claimed at create, lease assigned at prepare,
	// no Launch and so no live BindResult.
	claim := claimAndPrepare(t, binder, podsession.BindRequest{
		Pool: testPool, SessionID: "sess-ttl", TenantID: "acme", Runtime: "claude-code",
		CredentialPools: map[string]string{"anthropic": "claude-prod"},
	})

	if err := binder.ReclaimClaimed(context.Background(), claim.SandboxName, "sess-ttl"); err != nil {
		t.Fatalf("ReclaimClaimed: %v", err)
	}

	// The pod's per-pod claim is deleted (returned to the pool).
	var sc lennyv1.SandboxClaim
	gerr := c.Get(context.Background(), client.ObjectKey{Namespace: testNS, Name: "claim-sbx-1"}, &sc)
	if !apierrors.IsNotFound(gerr) {
		t.Errorf("per-pod claim get after ReclaimClaimed = %v, want NotFound (claim deleted)", gerr)
	}
	// The finalize-assigned lease is revoked: a finalizing/ready session always
	// holds one, so the revoke is mandatory (not best-effort-skipped).
	if len(assigner.released) != 1 || assigner.released[0] != "sess-ttl" {
		t.Errorf("ReleaseSession calls = %v, want [sess-ttl]", assigner.released)
	}
}

// spec: §4.5 (proposal) — ReclaimClaimed for a created session that never
// finalized (so holds only a pod, no lease) deletes the per-pod claim and
// makes the lease revoke a no-op rather than erroring.
// diagnosis: a failure means the created-expiry sweep cannot release a pod
// claimed at create when the session never reached finalize, leaving the warm
// pod stranded.
func TestReclaimClaimedNoLeaseIsNoOp_spec_4_5(t *testing.T) {
	srv := adapter.New("adapter-test")
	srv.WorkspaceRoot = t.TempDir()
	srv.Runtime = &fakeRuntime{}

	c := k8sClient(t, idleSandbox("sbx-1", "10.244.1.7"))
	binder := newBinder(c, adapterDialer(t, srv))
	assigner := &fakeAssigner{}
	binder.Credentials = assigner

	// A created session that only claimed a pod (no Prepare, no lease).
	claim, err := binder.Claim(context.Background(), podsession.BindRequest{
		Pool: testPool, SessionID: "sess-created", TenantID: "acme", Runtime: "claude-code",
	})
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}

	if err := binder.ReclaimClaimed(context.Background(), claim.SandboxName, "sess-created"); err != nil {
		t.Fatalf("ReclaimClaimed: %v", err)
	}

	var sc lennyv1.SandboxClaim
	gerr := c.Get(context.Background(), client.ObjectKey{Namespace: testNS, Name: "claim-sbx-1"}, &sc)
	if !apierrors.IsNotFound(gerr) {
		t.Errorf("per-pod claim get after ReclaimClaimed = %v, want NotFound (claim deleted)", gerr)
	}
	// ReleaseSession is keyed by session and is a no-op for a session with no
	// lease; the fake records the call, which decremented nothing.
	if len(assigner.released) != 1 || assigner.released[0] != "sess-created" {
		t.Errorf("ReleaseSession calls = %v, want [sess-created] (a no-op revoke)", assigner.released)
	}
}

// spec: §4.6 (proposal) — Prepare fails closed when the request carries no
// persisted sandbox binding: it cannot reconnect to the pod claimed at
// /create, so it rejects rather than claiming a fresh pod and orphaning the
// reserved one.
// diagnosis: a failure means Prepare silently re-claims a pod when the binding
// is missing, orphaning the pod the create handler reserved.
func TestPrepareWithoutBindingFailsClosed_spec_4_6(t *testing.T) {
	srv := adapter.New("adapter-test")
	srv.WorkspaceRoot = t.TempDir()
	srv.Runtime = &fakeRuntime{}

	c := k8sClient(t, idleSandbox("sbx-1", "10.244.1.7"))
	binder := newBinder(c, adapterDialer(t, srv))

	_, err := binder.Prepare(context.Background(), podsession.BindRequest{
		Pool: testPool, SessionID: "sess-1", TenantID: "acme", Runtime: "claude-code",
		// no SandboxName: the binding was never persisted
	})
	if err == nil {
		t.Fatal("Prepare succeeded with no claimed-sandbox binding, want a failure")
	}
}

// spec: §4.6 (proposal), §15.5 — when Prepare reconnects to the claimed pod
// and the adapter speaks no protocol version the gateway accepts, the
// reconnection fails closed rather than proceeding against an incompatible
// adapter. A pod claimed at /create whose adapter later reports an
// incompatible version surfaces the handshake error at /finalize.
// diagnosis: a failure means the §15.5 version handshake is skipped on the
// reconnect path, so Prepare could drive RPCs against an adapter that does not
// speak the gateway's protocol.
func TestPrepareRejectsIncompatibleAdapterOnReconnect_spec_4_6(t *testing.T) {
	srv := adapter.New("adapter-test")
	srv.ProtocolVersions = []string{"9.9.9"} // no version the gateway accepts
	srv.WorkspaceRoot = t.TempDir()
	srv.Runtime = &fakeRuntime{}

	c := k8sClient(t, idleSandbox("sbx-1", "10.244.1.7"))
	binder := newBinder(c, adapterDialer(t, srv))

	// The pod exists and carries an IP, so reconnect resolves and dials it;
	// only the handshake rejects the incompatible version.
	_, err := binder.Prepare(context.Background(), podsession.BindRequest{
		Pool: testPool, SessionID: "sess-1", TenantID: "acme", Runtime: "claude-code",
		SandboxName: "sbx-1",
	})
	if err == nil {
		t.Fatal("Prepare succeeded against an incompatible adapter on reconnect, want a failure")
	}
}

// spec: §4.6 (proposal) — when the persisted binding names a pod that no
// longer exists (drained between /create and /finalize), the reconnect fails
// closed rather than proceeding, so Prepare surfaces the error instead of
// driving RPCs against a missing pod.
// diagnosis: a failure means Prepare does not validate that the bound pod
// still exists before reconnecting, so a stale binding silently proceeds.
func TestPrepareFailsWhenBoundPodIsGone_spec_4_6(t *testing.T) {
	srv := adapter.New("adapter-test")
	srv.WorkspaceRoot = t.TempDir()
	srv.Runtime = &fakeRuntime{}

	// No Sandbox is seeded, so resolveSandbox cannot find the bound pod.
	c := k8sClient(t)
	binder := newBinder(c, adapterDialer(t, srv))

	_, err := binder.Prepare(context.Background(), podsession.BindRequest{
		Pool: testPool, SessionID: "sess-1", TenantID: "acme", Runtime: "claude-code",
		SandboxName: "sbx-gone",
	})
	if err == nil {
		t.Fatal("Prepare succeeded though the bound pod is gone, want a failure")
	}
}
