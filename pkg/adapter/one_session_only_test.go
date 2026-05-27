// SPDX-License-Identifier: MIT

package adapter

import (
	"context"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// spec: §6.1 lines 5, 16, 24 — "After a session completes or fails in
// `executionMode: session`, the pod is terminated and replaced — never
// recycled for a different session." The adapter is the last line of
// defense for this invariant: a pod that somehow survives the
// gateway-side drain → terminated → replaced loop must NOT accept a
// second session, regardless of whether the new caller is the same
// session id or a different one.
//
// F-6.1.12 verifies both layers. The gateway-side drain is verified by
// the binder + lifecycle tests; this file pins the adapter-side
// defense-in-depth.

// claimSession is cleared on Shutdown so the adapter does not block
// itself on a hypothetical second StartSession after a clean Shutdown.
// But the credSessionID stays sticky on purpose: an AssignCredentials
// for a different session after the original session's Shutdown is the
// canonical "pod was recycled" signal, and the adapter rejects it.
func TestReleaseSessionClearsSessionIdButKeepsCredSessionId_spec_6_1(t *testing.T) {
	s := &Server{}
	s.sessionID = "sess-old"
	s.credSessionID = "sess-old"

	s.releaseSession()

	if s.sessionID != "" {
		t.Errorf("sessionID = %q, want empty after release", s.sessionID)
	}
	// credSessionID stays set as the §6.1 defense-in-depth: a pod that
	// somehow survives termination cannot have credentials reassigned
	// to a different session. The gateway-side drain loop is the
	// primary defense; this is the second layer.
	if s.credSessionID != "sess-old" {
		t.Errorf("credSessionID = %q, want sess-old (sticky on release)", s.credSessionID)
	}
}

// spec: §6.1 — after a Shutdown, an AssignCredentials for a different
// session must be rejected so the §6.1 one-session-only invariant
// cannot be circumvented by a misbehaving controller.
func TestAssignCredentialsRejectsDifferentSessionAfterShutdown_spec_6_1(t *testing.T) {
	clk := &fakeExpiryClock{cur: time.Unix(1_700_000_000, 0).UTC()}
	s := expiryServer(t, clk)

	if _, err := s.AssignCredentials(context.Background(), &adapterv1.AssignCredentialsRequest{
		SessionId: &adapterv1.SessionId{Value: "sess-old"},
		Leases: map[string]*adapterv1.CredentialLease{
			"anthropic_direct": {LeaseId: "l1", Provider: "anthropic_direct"},
		},
	}); err != nil {
		t.Fatalf("initial AssignCredentials: %v", err)
	}

	// Simulate a Shutdown by releasing the session bookkeeping. (The
	// real Shutdown RPC also tears the runtime down; this test isolates
	// the credential bookkeeping behaviour.)
	s.releaseSession()

	// A controller that tried to re-bind this pod to a NEW session
	// would call AssignCredentials with the new session id. The
	// adapter must reject so the pod cannot be recycled.
	_, err := s.AssignCredentials(context.Background(), &adapterv1.AssignCredentialsRequest{
		SessionId: &adapterv1.SessionId{Value: "sess-new"},
		Leases: map[string]*adapterv1.CredentialLease{
			"anthropic_direct": {LeaseId: "l2", Provider: "anthropic_direct"},
		},
	})
	if err == nil {
		t.Fatal("F-6.1.12: AssignCredentials for a different session after Shutdown succeeded; expected rejection")
	}
	if got := status.Code(err); got != codes.FailedPrecondition {
		t.Errorf("F-6.1.12: code = %v, want FailedPrecondition (sticky credSessionID)", got)
	}
}

// spec: §6.1 — Shutdown clears the session-id slot so the adapter is
// not wedged from accepting a fresh StartSession (e.g. on a test
// fixture that reuses the same Server). The §6.1 one-session-only
// invariant is enforced at the credSessionID level above; the
// sessionID slot's "is some session currently running" gate is
// separate.
func TestClaimSessionAfterReleaseSessionAdmitsNewSession_spec_6_1(t *testing.T) {
	s := &Server{}
	if err := s.claimSession("sess-old"); err != nil {
		t.Fatalf("first claim: %v", err)
	}
	s.releaseSession()
	if err := s.claimSession("sess-new"); err != nil {
		t.Errorf("claim after release: %v (sessionID slot must be free)", err)
	}
}

// spec: §6.1 — a second StartSession on a pod that is currently running
// a session is rejected with Unavailable so a misbehaving controller
// cannot collide two sessions on the same pod.
func TestClaimSessionRejectsConcurrentSession_spec_6_1(t *testing.T) {
	s := &Server{}
	if err := s.claimSession("sess-a"); err != nil {
		t.Fatalf("first claim: %v", err)
	}
	err := s.claimSession("sess-b")
	if err == nil {
		t.Fatal("F-6.1.12: second claimSession succeeded; expected Unavailable")
	}
	if got := status.Code(err); got != codes.Unavailable {
		t.Errorf("F-6.1.12: code = %v, want Unavailable", got)
	}
}
