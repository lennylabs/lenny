// SPDX-License-Identifier: MIT

//go:build component

// Tier-2 component coverage for the release the failure funnel issues when a
// session fails mid-session on a recycling session-mode pool.
//
// The funnel releases the session's binding with the failed disposition. On
// a pool with maxConcurrentSessions: 1 a failed session retires its pod: the
// release deletes the per-pod claim so the warm pool controller drains the
// pod, rather than patching the claim to recycling and starting the
// whole-pod scrub that would hand the pod to the next session. The outcome is
// the claim in the kube-apiserver, so the case runs against a real one.
//
// spec: §5.2 (Recycle lifecycle; a failed session on a pool with
// maxConcurrentSessions: 1 retires the pod), §6.2 (Pod crash during an active
// session), §7.3 (Retry and Resume).
package slotrelease_test

import (
	"context"
	"testing"

	"sigs.k8s.io/controller-runtime/pkg/client"

	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podclaim"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/session/executor"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
	claimstate "github.com/lennylabs/lenny/pkg/sandboxclaim/state"
)

// bindRecyclingSession places one session on the pod of a recycling
// session-mode pool and registers the binding the executor's release reads.
func (f *releaseFixture) bindRecyclingSession(t *testing.T, sessionID string) {
	t.Helper()
	result, err := f.binder.Bind(context.Background(), podsession.BindRequest{
		Pool:      releasePool,
		SessionID: sessionID,
		TenantID:  "acme",
		Runtime:   "echo",
		Recycle:   true,
		Plan:      &adapterv1.WorkspacePlan{},
	})
	if err != nil {
		t.Fatalf("bind %s: %v", sessionID, err)
	}
	if !result.Recycle {
		t.Fatal("bind result does not carry the pool's recycle flag")
	}
	f.registry.Put(result)
}

// claimPhase returns the per-pod claim's binding state, or "" when the claim
// is gone.
func (f *releaseFixture) claimPhase(t *testing.T) string {
	t.Helper()
	if !f.claimExists(t) {
		return ""
	}
	var claim lennyv1.SandboxClaim
	if err := f.kube.Get(context.Background(), client.ObjectKey{
		Namespace: releaseNS, Name: podclaim.ClaimName(releasePod),
	}, &claim); err != nil {
		t.Fatalf("get the per-pod claim: %v", err)
	}
	return claim.Status.Phase
}

// spec: 5.2 (Recycle lifecycle), 6.2 (Pod crash during an active session), 7.3
// (Retry and Resume)
// diagnosis: the failure funnel's failed-disposition release did not retire a
// recycling session-mode pod. A claim left in `recycling` means the release
// took the recycle path, so the whole-pod scrub can hand the pod whose runtime
// failed to the next session. The completed-disposition case is the control:
// it must recycle, or the fixture is not a recycling pool and the failed case
// proves nothing.
func TestFailedReleaseRetiresRecyclingSessionPod_spec_6_2(t *testing.T) {
	t.Run("failed retires", func(t *testing.T) {
		f := newReleaseFixture(t, &slotRuntime{})
		f.bindRecyclingSession(t, "sess-failed")
		if err := f.exec.Release(context.Background(), "sess-failed", executor.DispositionFailed); err != nil {
			t.Fatalf("release with the failed disposition: %v", err)
		}
		if phase := f.claimPhase(t); phase != "" {
			t.Errorf("per-pod claim phase = %q after a failed release, want the claim deleted so the pod retires", phase)
		}
		if _, ok := f.registry.Get("sess-failed"); ok {
			t.Error("binding still registered after the release")
		}
	})
	t.Run("completed recycles", func(t *testing.T) {
		f := newReleaseFixture(t, &slotRuntime{})
		f.bindRecyclingSession(t, "sess-done")
		if err := f.exec.Release(context.Background(), "sess-done", executor.DispositionCompleted); err != nil {
			t.Fatalf("release with the completed disposition: %v", err)
		}
		if phase := f.claimPhase(t); phase != string(claimstate.Recycling) {
			t.Errorf("per-pod claim phase = %q after a completed release, want %q", phase, claimstate.Recycling)
		}
	})
}
