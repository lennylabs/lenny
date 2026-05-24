// SPDX-License-Identifier: MIT

package warmpool_test

import (
	"context"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1"
	"github.com/lennylabs/lenny/pkg/controller/warmpool"
)

// stubSessions is a fake §4.6.1 active-session oracle keyed on sessionID.
type stubSessions struct {
	active map[string]bool
	err    error
}

func (s stubSessions) SessionActive(_ context.Context, _, sessionID string) (bool, error) {
	if s.err != nil {
		return false, s.err
	}
	return s.active[sessionID], nil
}

func claim(name, sessionID, sandboxRef string) *lennyv1.SandboxClaim {
	return &lennyv1.SandboxClaim{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: testNS},
		Spec: lennyv1.SandboxClaimSpec{
			SandboxRef: sandboxRef,
			SessionID:  sessionID,
			TenantID:   "acme",
		},
	}
}

func claimedSandbox(name string) *lennyv1.Sandbox {
	sb := idleSandbox(name)
	sb.Status.Phase = "claimed"
	return sb
}

func gcSweep(t *testing.T, g *warmpool.ClaimGarbageCollector) {
	t.Helper()
	if err := g.SweepForTest(context.Background()); err != nil {
		t.Fatalf("sweep: %v", err)
	}
}

// spec: §4.6.1 "Orphaned SandboxClaim detection" — an aged claim with no
// active session is deleted and its backing Sandbox returns to idle.
func TestGCReclaimsOrphanedClaim(t *testing.T) {
	s := newScheme(t)
	c := newClient(t, s, claimedSandbox("pod-1"), claim("claim-1", "sess-gone", "pod-1"))

	g := &warmpool.ClaimGarbageCollector{
		Client:     c,
		Sessions:   stubSessions{active: map[string]bool{}},
		Namespaces: []string{testNS},
		// Pin "now" far ahead so the just-created claim is past the orphan
		// timeout; the apiserver stamps creationTimestamp at create time.
		Now: func() time.Time { return time.Now().Add(time.Hour) },
	}
	gcSweep(t, g)

	var got lennyv1.SandboxClaim
	if err := c.Get(context.Background(), client.ObjectKey{Namespace: testNS, Name: "claim-1"}, &got); err == nil {
		t.Fatal("expected orphaned claim to be deleted")
	}
	var sb lennyv1.Sandbox
	if err := c.Get(context.Background(), client.ObjectKey{Namespace: testNS, Name: "pod-1"}, &sb); err != nil {
		t.Fatalf("get sandbox: %v", err)
	}
	if sb.Status.Phase != "idle" {
		t.Errorf("sandbox phase = %q, want idle (returned to pool)", sb.Status.Phase)
	}
}

// spec: §4.6.1 — a claim whose session is still active is never reclaimed.
func TestGCKeepsClaimWithActiveSession(t *testing.T) {
	s := newScheme(t)
	c := newClient(t, s, claimedSandbox("pod-1"), claim("claim-1", "sess-live", "pod-1"))

	g := &warmpool.ClaimGarbageCollector{
		Client:     c,
		Sessions:   stubSessions{active: map[string]bool{"sess-live": true}},
		Namespaces: []string{testNS},
		Now:        func() time.Time { return time.Now().Add(time.Hour) },
	}
	gcSweep(t, g)

	var got lennyv1.SandboxClaim
	if err := c.Get(context.Background(), client.ObjectKey{Namespace: testNS, Name: "claim-1"}, &got); err != nil {
		t.Fatalf("claim with an active session must survive: %v", err)
	}
}

// spec: §4.6.1 — a claim younger than claimOrphanTimeout is not yet a
// candidate, even with no session (the gateway may still be persisting it).
func TestGCSkipsYoungClaim(t *testing.T) {
	s := newScheme(t)
	c := newClient(t, s, claimedSandbox("pod-1"), claim("claim-1", "sess-gone", "pod-1"))

	g := &warmpool.ClaimGarbageCollector{
		Client:        c,
		Sessions:      stubSessions{active: map[string]bool{}},
		Namespaces:    []string{testNS},
		OrphanTimeout: time.Hour,
		Now:           func() time.Time { return time.Now() },
	}
	gcSweep(t, g)

	var got lennyv1.SandboxClaim
	if err := c.Get(context.Background(), client.ObjectKey{Namespace: testNS, Name: "claim-1"}, &got); err != nil {
		t.Fatalf("young claim must survive: %v", err)
	}
}

// spec: §4.6.1 — a session-lookup error must not delete the claim; the
// sweep skips the candidate and retries on the next tick.
func TestGCSkipsOnLookupError(t *testing.T) {
	s := newScheme(t)
	c := newClient(t, s, claimedSandbox("pod-1"), claim("claim-1", "sess-gone", "pod-1"))

	g := &warmpool.ClaimGarbageCollector{
		Client:     c,
		Sessions:   stubSessions{err: context.DeadlineExceeded},
		Namespaces: []string{testNS},
		Now:        func() time.Time { return time.Now().Add(time.Hour) },
	}
	gcSweep(t, g)

	var got lennyv1.SandboxClaim
	if err := c.Get(context.Background(), client.ObjectKey{Namespace: testNS, Name: "claim-1"}, &got); err != nil {
		t.Fatalf("claim must survive a lookup error: %v", err)
	}
}

func TestGCNeedsLeaderElection(t *testing.T) {
	g := &warmpool.ClaimGarbageCollector{}
	if !g.NeedLeaderElection() {
		t.Error("orphan-claim GC must run only on the elected leader")
	}
}
