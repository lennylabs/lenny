// SPDX-License-Identifier: MIT

package adapter

import (
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// The merged start claim discriminates on whether a session has started
// rather than on whether the pod holds one. Every session is bound to a
// slot on every pod, so a second session arriving on a pod-warm pod
// arrives on its own slot and is admitted; the ceiling that bounds how
// many may do so is the gateway's, which allocates the slot and counts
// occupancy at the same site. What the claim refuses is a second start
// for a session that has already started.
//
// spec: §4.7; §5.2.

// spec: 4.7 (StartSession claim), 5.2 (every session is bound to a slot)
func TestStartClaimAdmitsASecondSessionOnItsOwnSlot_spec_5_2(t *testing.T) {
	s := &Server{WorkspaceBase: t.TempDir()}
	if err := s.claimSessionForTest("sess-a"); err != nil {
		t.Fatalf("first claim: %v", err)
	}
	if err := s.claimSessionForTest("sess-b"); err != nil {
		t.Errorf("second session's claim = %v, want admitted on its own slot", err)
	}
}

// spec: 4.7 (StartSession Unavailable for a repeated start), 5.2
func TestStartClaimRefusesASecondStartOfTheSameSession_spec_4_7(t *testing.T) {
	s := &Server{WorkspaceBase: t.TempDir()}
	if err := s.claimSessionForTest("sess-a"); err != nil {
		t.Fatalf("first claim: %v", err)
	}
	err := s.claimSessionForTest("sess-a")
	if err == nil {
		t.Fatal("second start of the same session succeeded; want Unavailable")
	}
	if got := status.Code(err); got != codes.Unavailable {
		t.Errorf("code = %v, want Unavailable", got)
	}
}

// spec: 4.7 (the credentials-first bind sequence), 5.2
//
// The §4.7 bind sequence assigns credentials before StartSession, so the
// session's entry is already bound when its first start arrives. A claim
// keyed on the binding rather than on the started flag would refuse that
// first start on every pool that configures a credential source.
func TestStartClaimAdmitsAStartOnABoundNotStartedEntry_spec_4_7(t *testing.T) {
	s := &Server{WorkspaceBase: t.TempDir()}
	s.mu.Lock()
	st, err := s.ensureSlotStateLocked("sess-a")
	if err != nil {
		s.mu.Unlock()
		t.Fatalf("ensure slot state: %v", err)
	}
	st.sessionID = "sess-a"
	s.mu.Unlock()

	if err := s.claimSessionForTest("sess-a"); err != nil {
		t.Errorf("start on a bound-not-started entry = %v, want admitted", err)
	}
}

// spec: 4.7 (release returns the slot), 5.2
func TestStartClaimReadmitsASessionAfterItsRelease_spec_4_7(t *testing.T) {
	s := &Server{WorkspaceBase: t.TempDir()}
	if err := s.claimSessionForTest("sess-old"); err != nil {
		t.Fatalf("first claim: %v", err)
	}
	s.ReleaseSlotForTest("sess-old")
	if err := s.claimSessionForTest("sess-old"); err != nil {
		t.Errorf("claim after release: %v (the slot must be free again)", err)
	}
}

// spec: 4.7 (the SDK-warm different-session refusal), 6.1 (preConnect is
// admitted only at maxConcurrentSessions: 1)
//
// On an SDK-warm pod the claim refuses a session that arrives while
// another has already started on the pre-connected runtime, because that
// runtime holds one working directory and one authenticated nonce.
func TestSDKWarmClaimRefusesASessionBesideAStartedOne_spec_4_7(t *testing.T) {
	s := &Server{WorkspaceBase: t.TempDir()}
	if _, _, err := s.claimSessionSlot("sess-a", true, false); err != nil {
		t.Fatalf("first claim: %v", err)
	}
	_, _, err := s.claimSessionSlot("sess-b", true, false)
	if err == nil {
		t.Fatal("second session admitted beside a started one; want Unavailable")
	}
	if got := status.Code(err); got != codes.Unavailable {
		t.Errorf("code = %v, want Unavailable", got)
	}
}

// spec: 4.7 (the SDK-warm different-session refusal), 6.1 (preConnect is
// admitted only at maxConcurrentSessions: 1)
//
// The SDK-warm different-session refusal reads the entry's bound state
// rather than the started flag. §6.1 fixes this pod class at
// maxConcurrentSessions: 1, so a second session on it has no runtime of
// its own; admitting one beside a bound-not-started entry would report
// fresh=true while leaving the pod surface unarmed, rewriting the
// pod-global manifest with a nonce no intra-pod MCP server is serving,
// and would leave the pod holding two registry entries for a DemoteSDK
// that releases exactly one.
func TestSDKWarmClaimRefusesASessionBesideABoundNotStartedEntry_spec_6_1(t *testing.T) {
	s := &Server{WorkspaceBase: t.TempDir()}
	s.mu.Lock()
	st, err := s.ensureSlotStateLocked("sess-bound")
	if err != nil {
		s.mu.Unlock()
		t.Fatalf("ensure slot state: %v", err)
	}
	st.sessionID = "sess-bound"
	s.mu.Unlock()

	_, _, err = s.claimSessionSlot("sess-b", true, false)
	if err == nil {
		t.Fatal("second session admitted beside a bound-not-started entry; want Unavailable")
	}
	if got := status.Code(err); got != codes.Unavailable {
		t.Errorf("code = %v, want Unavailable", got)
	}
	s.mu.Lock()
	n := len(s.slots)
	s.mu.Unlock()
	if n != 1 {
		t.Errorf("registry holds %d entries, want 1 on a maxConcurrentSessions: 1 pod", n)
	}
}

// spec: 4.7 (the credentials-first bind sequence), 6.1 (preConnect is
// admitted only at maxConcurrentSessions: 1)
//
// The same session's own first start is admitted on its bound-not-started
// entry, because the §4.7 bind sequence assigns credentials before
// ConfigureWorkspace and binds the entry ahead of the start. That claim
// reports fresh, which gates the §15.4 manifest write and the intra-pod
// MCP start.
func TestSDKWarmClaimAdmitsTheSameSessionOnItsBoundNotStartedEntry_spec_4_7(t *testing.T) {
	s := &Server{WorkspaceBase: t.TempDir()}
	s.mu.Lock()
	st, err := s.ensureSlotStateLocked("sess-a")
	if err != nil {
		s.mu.Unlock()
		t.Fatalf("ensure slot state: %v", err)
	}
	st.sessionID = "sess-a"
	s.mu.Unlock()

	fresh, startMCP, err := s.claimSessionSlot("sess-a", true, false)
	if err != nil {
		t.Fatalf("claim on its own bound-not-started entry = %v, want admitted", err)
	}
	if !fresh {
		t.Error("fresh = false, want true for the first start of sess-a")
	}
	if !startMCP {
		t.Error("startMCP = false, want true for the pod's sole session")
	}
}
