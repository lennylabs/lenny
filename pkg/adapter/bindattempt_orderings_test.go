// SPDX-License-Identifier: MIT

package adapter

import (
	"context"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// The cases below walk the orderings in which a bind attempt, its retries
// and its compensating Shutdown can reach one pod, one ordering each, and
// assert what the §4.7.1 admission and teardown cascades answer. Every
// Shutdown decides under s.mu, so a case that holds s.mu parks a
// compensation at that barrier with no production seam.

// Distinct bind attempt tokens for the orderings. The adapter treats them
// as opaque, so any distinct non-empty strings serve.
const (
	attempt1 = "attempt-1"
	attempt2 = "attempt-2"
	attempt3 = "attempt-3"
)

// orderingPod builds an adapter with every per-slot root under one temp base
// and a probe runtime that records its closes.
func orderingPod(t *testing.T) (*Server, *probeRuntime) {
	t.Helper()
	s, _ := slotPod(t)
	rt := &probeRuntime{}
	s.Runtime = rt
	return s, rt
}

// assign sends AssignCredentials for sessionID naming token, the first
// entry-creating request of an attempt that binds credentials.
func assign(s *Server, sessionID, token string) error {
	_, err := s.AssignCredentials(context.Background(), &adapterv1.AssignCredentialsRequest{
		SessionId:   &adapterv1.SessionId{Value: sessionID},
		BindAttempt: token,
	})
	return err
}

// start sends StartSession for sessionID. A start carries no token.
func start(s *Server, sessionID string) error {
	_, err := s.StartSession(context.Background(), &adapterv1.StartSessionRequest{
		SessionId: &adapterv1.SessionId{Value: sessionID},
		Runtime:   "echo",
	})
	return err
}

// resume sends a conversation-only Resume for sessionID naming token.
func resume(s *Server, sessionID, token string) error {
	_, err := s.Resume(context.Background(), &adapterv1.ResumeRequest{
		SessionId:    &adapterv1.SessionId{Value: sessionID},
		CheckpointId: "ckpt-1",
		BindAttempt:  token,
	})
	return err
}

// compensate sends the fenced Shutdown naming token and returns its outcome.
func compensate(t *testing.T, s *Server, sessionID, token string) *adapterv1.ShutdownResponse {
	t.Helper()
	resp, err := s.Shutdown(context.Background(), fencedShutdown(sessionID, token))
	if err != nil {
		t.Fatalf("Shutdown naming %s: %v", sessionID, err)
	}
	return resp
}

// stampOf returns the token sessionID's entry carries and whether one stands.
func stampOf(s *Server, sessionID string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.slots[sessionID]
	if !ok {
		return "", false
	}
	return st.bindAttempt, true
}

// startedOf reports whether sessionID's entry records a started session.
func startedOf(s *Server, sessionID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.slots[sessionID]
	return ok && st.started
}

// wantSuperseded fails the case unless err is rule 5's refusal.
func wantSuperseded(t *testing.T, what string, err error) {
	t.Helper()
	if status.Code(err) != codes.Aborted ||
		adapterErrorCode(err) != adapterv1.Error_ERROR_CODE_SLOT_BIND_ATTEMPT_SUPERSEDED {
		t.Errorf("%s = %v, want the superseded refusal on Aborted", what, err)
	}
}

const (
	outcomeReclaimed  = adapterv1.SlotReclaimOutcome_SLOT_RECLAIM_OUTCOME_RECLAIMED
	outcomeAbsent     = adapterv1.SlotReclaimOutcome_SLOT_RECLAIM_OUTCOME_ABSENT
	outcomeSuperseded = adapterv1.SlotReclaimOutcome_SLOT_RECLAIM_OUTCOME_SUPERSEDED
)

// spec: §4.7.1 (role and gateway RPC contract); §7.1 (normal flow)
//
// This ordering is written first because a design that compares the token
// in the handlers rather than at the resolve fails it: the resume's claim
// resolves through ensureSlotStateLocked, which compares before st.started
// is written, so the resume is refused and the earlier attempt's stale
// compensation removes the leaked entry alone, with no runtime to close.
func TestAResumeOntoALeakedEntryIsRefusedAndTheStaleCompensationRemovesIt_spec_4_7_1(t *testing.T) {
	s, rt := orderingPod(t)
	if err := assign(s, "alice", attempt1); err != nil {
		t.Fatalf("attempt 1 assign: %v", err)
	}
	wantSuperseded(t, "a resume naming another attempt onto the leaked entry", resume(s, "alice", attempt2))
	if startedOf(s, "alice") {
		t.Fatal("the refused resume recorded a start on the leaked entry")
	}
	if resp := compensate(t, s, "alice", attempt1); resp.GetSlotReclaim() != outcomeReclaimed {
		t.Errorf("the earlier attempt's compensation = %v, want RECLAIMED", resp.GetSlotReclaim())
	}
	if hasEntry(s, "alice") {
		t.Error("the leaked entry survived its own attempt's compensation")
	}
	if len(rt.closed) != 0 {
		t.Errorf("runtime closed = %v; the refused resume started nothing", rt.closed)
	}
}

// spec: §4.7.1 (role and gateway RPC contract); §7.1 (normal flow)
//
// Written first beside the case above, for the same reason. The refused
// resume created and started nothing, so the compensation its lost response
// provokes names a token no entry carries: it answers superseded, removes
// nothing and closes no runtime, and its clean exit leaves the gateway's
// leaked disposition false.
func TestTheRefusedResumesOwnCompensationRemovesNothing_spec_4_7_1(t *testing.T) {
	s, rt := orderingPod(t)
	if err := assign(s, "alice", attempt1); err != nil {
		t.Fatalf("attempt 1 assign: %v", err)
	}
	wantSuperseded(t, "the resume", resume(s, "alice", attempt2))
	resp := compensate(t, s, "alice", attempt2)
	if resp.GetSlotReclaim() != outcomeSuperseded || !resp.GetExitedCleanly() {
		t.Errorf("the resume's compensation = %+v, want SUPERSEDED with a clean exit", resp)
	}
	if stamp, ok := stampOf(s, "alice"); !ok || stamp != attempt1 {
		t.Errorf("entry after the resume's compensation = (%q, %v), want attempt 1's entry intact", stamp, ok)
	}
	if len(rt.closed) != 0 {
		t.Errorf("runtime closed = %v; no orphan runtime exists", rt.closed)
	}
}

// spec: §4.7.1 (role and gateway RPC contract); §7.1 (normal flow)
//
// Attempt 1 fails before the start and its entry survives. Attempt 2's first
// request is refused on the identity gate before it touches anything,
// attempt 1's compensation matches and removes the entry, and attempt 3
// creates a fresh entry stamped with its own token.
func TestARetryIsRefusedUntilTheFailedAttemptsCompensationLands_spec_4_7_1(t *testing.T) {
	s, _ := orderingPod(t)
	if err := assign(s, "alice", attempt1); err != nil {
		t.Fatalf("attempt 1 assign: %v", err)
	}
	wantSuperseded(t, "attempt 2's first request", assign(s, "alice", attempt2))
	if stamp, _ := stampOf(s, "alice"); stamp != attempt1 {
		t.Errorf("entry stamp after attempt 2's refusal = %q, want attempt 1's", stamp)
	}
	if resp := compensate(t, s, "alice", attempt1); resp.GetSlotReclaim() != outcomeReclaimed {
		t.Errorf("attempt 1's compensation = %v, want RECLAIMED", resp.GetSlotReclaim())
	}
	if err := assign(s, "alice", attempt3); err != nil {
		t.Fatalf("attempt 3 after the compensation: %v", err)
	}
	if stamp, _ := stampOf(s, "alice"); stamp != attempt3 {
		t.Errorf("entry stamp after attempt 3 = %q, want attempt 3's", stamp)
	}
}

// spec: §4.7.1 (role and gateway RPC contract); §7.1 (normal flow)
//
// Attempt 1's StartSession is admitted and its response is lost. The entry
// still carries attempt 1's token, so the compensation matches and the
// orphaned runtime session is torn down. The compensation is parked at the
// registry barrier while the start's state is observed, which is the
// ordering a lost response produces.
func TestALostStartResponsesCompensationTearsTheOrphanDown_spec_4_7_1(t *testing.T) {
	s, rt := orderingPod(t)
	if err := assign(s, "alice", attempt1); err != nil {
		t.Fatalf("attempt 1 assign: %v", err)
	}
	if err := start(s, "alice"); err != nil {
		t.Fatalf("attempt 1 start: %v", err)
	}
	s.mu.Lock()
	done := make(chan *adapterv1.ShutdownResponse, 1)
	go func() {
		resp, err := s.Shutdown(context.Background(), fencedShutdown("alice", attempt1))
		if err != nil {
			t.Errorf("compensation: %v", err)
		}
		done <- resp
	}()
	stamp := s.slots["alice"].bindAttempt
	s.mu.Unlock()
	if stamp != attempt1 {
		t.Fatalf("the started entry carries %q, want attempt 1's token", stamp)
	}
	resp := <-done
	if resp.GetSlotReclaim() != outcomeReclaimed {
		t.Errorf("compensation = %v, want RECLAIMED", resp.GetSlotReclaim())
	}
	if len(rt.closed) != 1 || rt.closed[0] != "alice" {
		t.Errorf("runtime closed = %v, want [alice]; the orphan was not torn down", rt.closed)
	}
	if hasEntry(s, "alice") {
		t.Error("the orphan's entry survived its compensation")
	}
}

// spec: §4.7.1 (role and gateway RPC contract); §7.1 (normal flow)
//
// Attempt 1 creates the entry; attempt 2's first request is refused, so
// attempt 2 never reaches its StartSession and the entry is neither
// restamped nor started.
func TestAConcurrentAttemptIsRefusedBeforeItsStart_spec_4_7_1(t *testing.T) {
	s, _ := orderingPod(t)
	if err := s.PrepareWorkspace(&prepareWorkspaceStreamStub{
		ctx:    context.Background(),
		frames: []*adapterv1.PrepareWorkspaceRequest{prepareFrame("alice", attempt1, false, "x")},
	}); err != nil {
		t.Fatalf("attempt 1 prepare: %v", err)
	}
	err := s.PrepareWorkspace(&prepareWorkspaceStreamStub{
		ctx:    context.Background(),
		frames: []*adapterv1.PrepareWorkspaceRequest{prepareFrame("alice", attempt2, false, "y")},
	})
	wantSuperseded(t, "attempt 2's prepare", err)
	if stamp, _ := stampOf(s, "alice"); stamp != attempt1 || startedOf(s, "alice") {
		t.Errorf("entry after attempt 2's refusal: stamp %q, started %v; want attempt 1's, unstarted",
			stamp, startedOf(s, "alice"))
	}
}

// spec: §4.7.1 (role and gateway RPC contract); §7.1 (normal flow); §7.4 (upload safety)
//
// A mid-session finalize for a session whose entry an unconditional Shutdown
// removed is refused FailedPrecondition and creates no untokened entry,
// because a mid-session request never creates one.
func TestAMidSessionFinalizeAfterTheTeardownCreatesNothing_spec_4_7_1(t *testing.T) {
	s, _ := orderingPod(t)
	if err := assign(s, "alice", attempt1); err != nil {
		t.Fatalf("assign: %v", err)
	}
	if err := start(s, "alice"); err != nil {
		t.Fatalf("start: %v", err)
	}
	unconditionalShutdown(t, s, context.Background(), "alice")
	_, err := s.FinalizeWorkspace(context.Background(), &adapterv1.FinalizeWorkspaceRequest{
		SessionId:     &adapterv1.SessionId{Value: "alice"},
		MidSession:    true,
		WorkspacePlan: &adapterv1.WorkspacePlan{SchemaVersion: 1},
	})
	if status.Code(err) != codes.FailedPrecondition {
		t.Errorf("mid-session finalize after the teardown = %v, want FailedPrecondition", err)
	}
	if hasEntry(s, "alice") || slotDirExists(s, "alice") {
		t.Error("the mid-session finalize created an entry or a tree")
	}
}

// spec: §4.7.1 (role and gateway RPC contract); §7.1 (normal flow)
//
// This pins an accepted residue rather than a refusal. An attempt whose own
// entry an unconditional teardown removed still holds its token, so its
// next FinalizeWorkspace recreates an entry stamped with that token and
// materializes from an empty staging tree. The stamp-once rule cannot tell
// that request from the attempt's first one, and the specification accepts
// the re-creation rather than a per-attempt tombstone.
func TestAnAttemptRecreatesItsOwnEntryAfterAnUnconditionalTeardown_spec_4_7_1(t *testing.T) {
	s, _ := orderingPod(t)
	if err := assign(s, "alice", attempt1); err != nil {
		t.Fatalf("assign: %v", err)
	}
	unconditionalShutdown(t, s, context.Background(), "alice")
	if _, err := s.FinalizeWorkspace(context.Background(), &adapterv1.FinalizeWorkspaceRequest{
		SessionId:     &adapterv1.SessionId{Value: "alice"},
		BindAttempt:   attempt1,
		WorkspacePlan: &adapterv1.WorkspacePlan{SchemaVersion: 1},
	}); err != nil {
		t.Fatalf("the attempt's own finalize after the teardown: %v", err)
	}
	if stamp, ok := stampOf(s, "alice"); !ok || stamp != attempt1 {
		t.Errorf("recreated entry = (%q, %v), want one stamped with the attempt's own token", stamp, ok)
	}
}

// spec: §4.7.1 (role and gateway RPC contract); §7.1 (normal flow)
//
// A second attempt at a live started session is refused on the identity
// gate, which the cascade evaluates ahead of the phase gate, and touches no
// workspace, credential or staging state. Its compensation names its own
// token, answers superseded under rule 13 and removes nothing.
func TestASecondAttemptAgainstALiveSessionDamagesNothing_spec_4_7_1(t *testing.T) {
	s, rt := orderingPod(t)
	bindAndStart(t, s, "alice")
	cur0, cred0 := slotTreeProbe(t, s, "alice")
	if !cur0 || !cred0 {
		t.Fatalf("fixture: cwd %v, credentials.json %v; want both", cur0, cred0)
	}
	wantSuperseded(t, "the second attempt's assign", assign(s, "alice", attempt2))
	_, err := s.RunSetup(context.Background(), &adapterv1.RunSetupRequest{
		SessionId: &adapterv1.SessionId{Value: "alice"}, BindAttempt: attempt2,
	})
	wantSuperseded(t, "the second attempt's setup", err)
	resp := compensate(t, s, "alice", attempt2)
	if resp.GetSlotReclaim() != outcomeSuperseded {
		t.Errorf("the second attempt's compensation = %v, want SUPERSEDED", resp.GetSlotReclaim())
	}
	if cur, cred := slotTreeProbe(t, s, "alice"); !cur || !cred || !startedOf(s, "alice") {
		t.Errorf("the live session after the second attempt: cwd %v, credentials.json %v, started %v; want all",
			cur, cred, startedOf(s, "alice"))
	}
	if len(rt.closed) != 0 {
		t.Errorf("runtime closed = %v; the live session was torn down", rt.closed)
	}
}

// spec: §4.7.1 (role and gateway RPC contract); §7.1 (normal flow)
//
// A resume onto a replacement pod mints its own token, and its claim stamps
// the entry it creates with it. A stale compensation from an earlier
// attempt answers superseded there, and the resume's own compensation
// matches.
func TestAResumeOntoAReplacementPodOwnsTheEntryItCreates_spec_4_7_1(t *testing.T) {
	s, rt := orderingPod(t)
	if err := resume(s, "alice", attempt2); err != nil {
		t.Fatalf("resume: %v", err)
	}
	if stamp, _ := stampOf(s, "alice"); stamp != attempt2 {
		t.Errorf("entry stamp after the resume = %q, want the resume's token", stamp)
	}
	if resp := compensate(t, s, "alice", attempt1); resp.GetSlotReclaim() != outcomeSuperseded {
		t.Errorf("a stale compensation = %v, want SUPERSEDED", resp.GetSlotReclaim())
	}
	if resp := compensate(t, s, "alice", attempt2); resp.GetSlotReclaim() != outcomeReclaimed {
		t.Errorf("the resume's own compensation = %v, want RECLAIMED", resp.GetSlotReclaim())
	}
	if len(rt.closed) != 1 {
		t.Errorf("runtime closed = %v, want the resumed session", rt.closed)
	}
}

// spec: §4.7.1 (role and gateway RPC contract); §7.1 (normal flow); §7.4 (upload safety)
//
// A §7.4 mid-session upload carries no token and mid_session true. It is
// admitted onto a live started session under the admit rule, exempt from
// the phase gate, and barred from creating an entry for a session the pod
// does not hold.
func TestAMidSessionUploadOntoALiveSession_spec_4_7_1(t *testing.T) {
	s, _ := orderingPod(t)
	bindAndStart(t, s, "alice")
	upload := func(id string) error {
		return s.PrepareWorkspace(&prepareWorkspaceStreamStub{
			ctx:    context.Background(),
			frames: []*adapterv1.PrepareWorkspaceRequest{prepareFrame(id, "", true, "x")},
		})
	}
	if err := upload("alice"); err != nil {
		t.Errorf("mid-session upload onto the live session = %v, want admitted", err)
	}
	if stamp, _ := stampOf(s, "alice"); stamp != shutdownAttempt {
		t.Errorf("entry stamp after the upload = %q, want it unchanged", stamp)
	}
	if err := upload("bob"); status.Code(err) != codes.FailedPrecondition {
		t.Errorf("mid-session upload for a session with no entry = %v, want FailedPrecondition", err)
	}
	if hasEntry(s, "bob") {
		t.Error("a mid-session upload created an entry")
	}
}

// spec: §4.7.1 (role and gateway RPC contract); §7.1 (normal flow)
//
// A restarted adapter process holds no entry a stale compensation can
// match, so the compensation answers absent. An entry a later attempt
// creates carries that attempt's token, so the stale compensation then
// answers superseded.
func TestAStaleCompensationAfterAnAdapterRestart_spec_4_7_1(t *testing.T) {
	s, _ := orderingPod(t)
	if resp := compensate(t, s, "alice", attempt1); resp.GetSlotReclaim() != outcomeAbsent {
		t.Errorf("stale compensation on a fresh process = %v, want ABSENT", resp.GetSlotReclaim())
	}
	if err := assign(s, "alice", attempt3); err != nil {
		t.Fatalf("a later attempt: %v", err)
	}
	if resp := compensate(t, s, "alice", attempt1); resp.GetSlotReclaim() != outcomeSuperseded {
		t.Errorf("stale compensation against the later attempt's entry = %v, want SUPERSEDED", resp.GetSlotReclaim())
	}
	if stamp, _ := stampOf(s, "alice"); stamp != attempt3 {
		t.Errorf("entry stamp = %q, want the later attempt's", stamp)
	}
}

// spec: §4.7.1 (role and gateway RPC contract); §7.1 (normal flow)
//
// This pins an accepted residue. When a compensation never leaves a gateway
// that crashed, the entry stands stamped with a dead token and every later
// attempt at that session on that pod is refused rather than admitted onto
// it. Relieving the residue needs a durable compensation record that
// survives the gateway, which is outside this contract; until the pod
// retires, placement elsewhere is what serves the session.
func TestACompensationLostToAGatewayCrashLeavesARefusingEntry_spec_4_7_1(t *testing.T) {
	s, _ := orderingPod(t)
	if err := assign(s, "alice", attempt1); err != nil {
		t.Fatalf("attempt 1 assign: %v", err)
	}
	for _, token := range []string{attempt2, attempt3} {
		wantSuperseded(t, "a later attempt at the crashed attempt's entry", assign(s, "alice", token))
	}
	if stamp, _ := stampOf(s, "alice"); stamp != attempt1 {
		t.Errorf("entry stamp = %q, want the dead attempt's", stamp)
	}
}
