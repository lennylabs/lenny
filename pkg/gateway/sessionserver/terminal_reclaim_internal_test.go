// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
	"github.com/lennylabs/lenny/pkg/gateway/podclaim"
	"github.com/lennylabs/lenny/pkg/gateway/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore"
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

	ran := s.terminalReclaimPreRunning(context.Background(), sessionstore.Session{
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

// spec: §4.6 (durable binding) — a bound (running/resuming) session has a live
// BindResult in the registry, so its lease is revoked through the binder's
// Release on the executor path. terminalReclaimPreRunning must not claim it as a
// pre-running session: it returns false and leaves the claim untouched so the
// executor release is not double-driven.
func TestTerminalReclaimPreRunningSkipsBoundSession(t *testing.T) {
	s, c, assigner := reclaimTestServer(t, "sbx-bound")
	// A live BindResult marks the session as launched (running).
	s.podRegistry.Put(&podsession.BindResult{SessionID: "sess-bound", TenantID: "acme", SandboxName: "sbx-bound"})

	ran := s.terminalReclaimPreRunning(context.Background(), sessionstore.Session{
		ID: "sess-bound", TenantID: "acme", PodAssignment: "sbx-bound",
	})
	if ran {
		t.Fatalf("terminalReclaimPreRunning = true for a bound session, want false (executor path owns the release)")
	}
	if !claimExists(t, c, "sbx-bound") {
		t.Errorf("per-pod claim deleted for a bound session, want untouched (the executor release owns it)")
	}
	if len(assigner.released) != 0 {
		t.Errorf("ReleaseSession calls = %v, want none (the executor release revokes the lease)", assigner.released)
	}
}

// spec: §4.6 (durable binding) — a session row with no persisted pod binding
// (PodAssignment empty: a service-mode or claimless session) has nothing to
// reclaim, so terminalReclaimPreRunning returns false and falls through to the
// executor path.
func TestTerminalReclaimPreRunningNoPodBindingIsSkipped(t *testing.T) {
	s, _, assigner := reclaimTestServer(t, "sbx-none")

	ran := s.terminalReclaimPreRunning(context.Background(), sessionstore.Session{
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
	ran := s.terminalReclaimPreRunning(context.Background(), sessionstore.Session{
		ID: "sess-x", TenantID: "acme", PodAssignment: "sbx-x",
	})
	if ran {
		t.Fatalf("terminalReclaimPreRunning = true with no binder, want false")
	}
}
