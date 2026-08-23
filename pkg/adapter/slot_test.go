// SPDX-License-Identifier: MIT

package adapter_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/lennylabs/lenny/pkg/adapter"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// concurrentServer builds an adapter Server wired to fresh temp roots and
// a single pod-global fakeRuntime. Per spec/05:509 and spec/15:1459 one
// runtime process per pod serves every slot, so the slots share this one
// runtime. Every session is bound to a slot on every pod and a
// session-mode slot's identifier is its session's identifier, so a slot is
// addressed by the session it holds.
func concurrentServer(t *testing.T) (*adapter.Server, *fakeRuntime) {
	t.Helper()
	base := t.TempDir()
	s := adapter.New("test")
	s.WorkspaceBase = filepath.Join(base, "workspace")
	s.SessionsRoot = filepath.Join(base, "sessions")
	s.ArtifactsRoot = filepath.Join(base, "artifacts")
	s.CredentialsDir = filepath.Join(base, "run", "lenny")
	rt := &fakeRuntime{}
	s.Runtime = rt
	return s, rt
}

func slotStartReq(sessionID string) *adapterv1.StartSessionRequest {
	return &adapterv1.StartSessionRequest{
		SessionId: &adapterv1.SessionId{Value: sessionID},
		Runtime:   "echo",
	}
}

// spec: §6.4; spec/05:509 — a slot-qualified StartSession
// creates the per-slot directory tree and starts the slot's session on
// the single pod-global runtime.
func TestStartSessionSlotCreatesTreeAndStartsRuntime_spec_6_4(t *testing.T) {
	s, rt := concurrentServer(t)
	if _, err := s.StartSession(context.Background(), slotStartReq("sess-a")); err != nil {
		t.Fatalf("StartSession(slot): %v", err)
	}
	if len(rt.started) != 1 || rt.started[0] != "sess-a" {
		t.Fatalf("slot session not started for sess-a on the pod runtime: %+v", rt.started)
	}
	// The §6.4 per-slot tree must exist on disk.
	for _, sub := range []string{
		filepath.Join(s.WorkspaceBase, "slots", "sess-a", "current"),
		filepath.Join(s.WorkspaceBase, "slots", "sess-a", "staging"),
		filepath.Join(s.SessionsRoot, "sess-a"),
		filepath.Join(s.ArtifactsRoot, "sess-a"),
		filepath.Join(s.CredentialsDir, "slots", "sess-a"),
	} {
		if info, err := os.Stat(sub); err != nil || !info.IsDir() {
			t.Errorf("expected slot dir %q to exist: err=%v", sub, err)
		}
	}
}

// spec: §5.2 concurrent mode; spec/05:509, spec/05:511 — two distinct slots
// are served concurrently over the single pod-global runtime connection,
// each with its own isolated per-slot workspace tree. With the mode flag and
// the per-slot RuntimeFactory removed, the per-slot path activates on sessionId
// presence alone, one runtime process per pod multiplexes both slots on
// sessionId, and each slot's /workspace/slots/{sessionId}/current/ is materialized
// independently so one slot's files never appear in the other's workspace.
func TestStartSessionSlotAllowsConcurrentSlots_spec_5_2(t *testing.T) {
	s, rt := concurrentServer(t)
	ctx := context.Background()
	if _, err := s.StartSession(ctx, slotStartReq("sess-a")); err != nil {
		t.Fatalf("StartSession(slot-a): %v", err)
	}
	if _, err := s.StartSession(ctx, slotStartReq("sess-b")); err != nil {
		t.Fatalf("StartSession(slot-b): %v", err)
	}
	// One runtime process per pod serves both slots: it sees both sessions
	// started, multiplexed on sessionId over the single connection.
	if got := rt.started; len(got) != 2 || got[0] != "sess-a" || got[1] != "sess-b" {
		t.Fatalf("pod runtime sessions = %v, want [sess-a sess-b]", got)
	}

	// Each slot materializes distinct content into its own per-slot
	// workspace, proving the two concurrent slots have isolated workspaces
	// rather than a shared /workspace/current.
	finalize := func(session, slot, content string) {
		t.Helper()
		req := &adapterv1.FinalizeWorkspaceRequest{
			SessionId: &adapterv1.SessionId{Value: session},
			WorkspacePlan: &adapterv1.WorkspacePlan{
				SchemaVersion: 1,
				Sources: []*adapterv1.WorkspaceSource{
					{Type: "inlineFile", Path: "marker.txt", Content: content, Mode: "0644"},
				},
			},
		}
		if _, err := s.FinalizeWorkspace(ctx, req); err != nil {
			t.Fatalf("FinalizeWorkspace(%s): %v", slot, err)
		}
	}
	finalize("sess-a", "slot-a", "from-slot-a")
	finalize("sess-b", "slot-b", "from-slot-b")

	// Each slot's marker holds only that slot's content; neither slot's file
	// leaked into the sibling slot's workspace.
	for slot, want := range map[string]string{"sess-a": "from-slot-a", "sess-b": "from-slot-b"} {
		path := filepath.Join(s.WorkspaceBase, "slots", slot, "current", "marker.txt")
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s workspace marker: %v", slot, err)
		}
		if string(got) != want {
			t.Errorf("%s workspace marker = %q, want %q; per-slot workspaces are not isolated", slot, got, want)
		}
	}
	// The global whole-pod /workspace/current was never used by either slot.
	if _, err := os.Stat(filepath.Join(s.WorkspaceBase, "current", "marker.txt")); !os.IsNotExist(err) {
		t.Errorf("global workspace/current was written; concurrent slots are not isolated")
	}
}

// spec: §4.7 — a second start for a session that has already started is
// rejected. A second session is not: it arrives on its own slot.
func TestStartSessionRejectsARepeatedStart_spec_4_7(t *testing.T) {
	s, _ := concurrentServer(t)
	if _, err := s.StartSession(context.Background(), slotStartReq("sess-a")); err != nil {
		t.Fatalf("first StartSession: %v", err)
	}
	_, err := s.StartSession(context.Background(), slotStartReq("sess-a"))
	if status.Code(err) != codes.Unavailable {
		t.Errorf("repeated start code = %v, want Unavailable", status.Code(err))
	}
}

// spec: §6.4 — a slot-qualified finalize materializes into the
// slot's /workspace/slots/{sessionId}/current cwd.
func TestFinalizeWorkspaceSlotMaterializesPerSlot_spec_6_4(t *testing.T) {
	s, _ := concurrentServer(t)
	req := &adapterv1.FinalizeWorkspaceRequest{
		SessionId: &adapterv1.SessionId{Value: "sess-a"},
		WorkspacePlan: &adapterv1.WorkspacePlan{
			SchemaVersion: 1,
			Sources: []*adapterv1.WorkspaceSource{
				{Type: "inlineFile", Path: "hello.txt", Content: "hi", Mode: "0644"},
			},
		},
	}
	if _, err := s.FinalizeWorkspace(context.Background(), req); err != nil {
		t.Fatalf("FinalizeWorkspace(slot): %v", err)
	}
	want := filepath.Join(s.WorkspaceBase, "slots", "sess-a", "current", "hello.txt")
	got, err := os.ReadFile(want)
	if err != nil {
		t.Fatalf("expected materialized file at %q: %v", want, err)
	}
	if string(got) != "hi" {
		t.Errorf("file content = %q, want %q", got, "hi")
	}
	// The global /workspace/current must not have been used.
	if _, err := os.Stat(filepath.Join(s.WorkspaceBase, "current", "hello.txt")); !os.IsNotExist(err) {
		t.Errorf("global workspace/current was written; per-slot isolation broken")
	}
}

// spec: §6.1 — a slot-qualified assignment writes the slot's own
// /run/lenny/slots/{sessionId}/credentials.json, not the global file.
func TestAssignCredentialsSlotWritesPerSlotFile_spec_6_1(t *testing.T) {
	s, _ := concurrentServer(t)
	leases := map[string]*adapterv1.CredentialLease{
		"anthropic": {LeaseId: "lease-1", Provider: "anthropic"},
	}
	_, err := s.AssignCredentials(context.Background(), &adapterv1.AssignCredentialsRequest{
		SessionId: &adapterv1.SessionId{Value: "sess-a"},
		Leases:    leases,
	})
	if err != nil {
		t.Fatalf("AssignCredentials(slot): %v", err)
	}
	perSlot := filepath.Join(s.CredentialsDir, "slots", "sess-a", "credentials.json")
	if _, err := os.Stat(perSlot); err != nil {
		t.Errorf("expected per-slot credential file at %q: %v", perSlot, err)
	}
	if _, err := os.Stat(filepath.Join(s.CredentialsDir, "credentials.json")); !os.IsNotExist(err) {
		t.Errorf("global credential file written; per-slot isolation broken")
	}
}

// spec: §6.1 — a rotation on one slot leaves a sibling slot's
// credential file untouched.
func TestRotateCredentialsSlotIsIndependent_spec_6_1(t *testing.T) {
	s, _ := concurrentServer(t)
	ctx := context.Background()
	for _, slot := range []string{"sess-a", "sess-b"} {
		if _, err := s.AssignCredentials(ctx, &adapterv1.AssignCredentialsRequest{
			SessionId: &adapterv1.SessionId{Value: slot},
			Leases: map[string]*adapterv1.CredentialLease{
				"anthropic": {LeaseId: "lease-" + slot, Provider: "anthropic"},
			},
		}); err != nil {
			t.Fatalf("AssignCredentials(%s): %v", slot, err)
		}
	}
	bFile := filepath.Join(s.CredentialsDir, "slots", "sess-b", "credentials.json")
	before, err := os.ReadFile(bFile)
	if err != nil {
		t.Fatalf("read slot-b creds: %v", err)
	}
	// Rotate slot-a only.
	if _, err := s.RotateCredentials(ctx, &adapterv1.RotateCredentialsRequest{
		SessionId: &adapterv1.SessionId{Value: "sess-a"},
		Leases: map[string]*adapterv1.CredentialLease{
			"anthropic": {LeaseId: "lease-a-rotated", Provider: "anthropic"},
		},
	}); err != nil {
		t.Fatalf("RotateCredentials(slot-a): %v", err)
	}
	after, err := os.ReadFile(bFile)
	if err != nil {
		t.Fatalf("re-read slot-b creds: %v", err)
	}
	if string(before) != string(after) {
		t.Errorf("slot-b credential file changed by a slot-a rotation; isolation broken")
	}
}

// spec: §6.4; spec/05:509 — a slot-qualified SendMessage
// validates the slot's bound session and delivers the envelope to the
// single pod-global runtime, which multiplexes sessions on sessionId over the
// one connection. A message naming a session that is not bound to the
// addressed slot is rejected rather than delivered.
func TestSendMessageSlotRoutesToSlotRuntime_spec_6_4(t *testing.T) {
	s, rt := concurrentServer(t)
	ctx := context.Background()
	for _, sess := range []string{"sess-a", "sess-b"} {
		if _, err := s.StartSession(ctx, slotStartReq(sess)); err != nil {
			t.Fatalf("StartSession(%s): %v", sess, err)
		}
	}
	if _, err := s.SendMessage(ctx, &adapterv1.SendMessageRequest{
		SessionId:    &adapterv1.SessionId{Value: "sess-a"},
		EnvelopeJson: []byte(`{"type":"message"}`),
	}); err != nil {
		t.Fatalf("SendMessage(slot-a): %v", err)
	}
	// The single pod-global runtime receives the slot-a envelope.
	if got := len(rt.envelopesSnapshot()); got != 1 {
		t.Errorf("pod runtime envelopes = %d, want 1", got)
	}
	// A session the registry holds no bound entry for is rejected: the
	// adapter validates the binding before delivery so a message for a
	// session this pod does not hold is never written to the shared
	// runtime.
	_, err := s.SendMessage(ctx, &adapterv1.SendMessageRequest{
		SessionId:    &adapterv1.SessionId{Value: "sess-unbound"},
		EnvelopeJson: []byte(`{"type":"message"}`),
	})
	if status.Code(err) != codes.FailedPrecondition {
		t.Errorf("unbound session code = %v, want FailedPrecondition", status.Code(err))
	}
	if got := len(rt.envelopesSnapshot()); got != 1 {
		t.Errorf("pod runtime envelopes after rejected send = %d, want 1", got)
	}
}

// spec: §6.4; spec/05:509 — Shutdown closes the slot's
// session on the single pod-global runtime and removes its per-slot tree;
// a sibling slot, and the shared runtime, are unaffected.
func TestShutdownSlotRemovesTree_spec_6_4(t *testing.T) {
	s, rt := concurrentServer(t)
	ctx := context.Background()
	for _, sess := range []string{"sess-a", "sess-b"} {
		if _, err := s.StartSession(ctx, slotStartReq(sess)); err != nil {
			t.Fatalf("StartSession(%s): %v", sess, err)
		}
	}
	resp, err := s.Shutdown(ctx, &adapterv1.ShutdownRequest{
		SessionId: &adapterv1.SessionId{Value: "sess-a"},
	})
	if err != nil {
		t.Fatalf("Shutdown(slot-a): %v", err)
	}
	if !resp.GetExitedCleanly() {
		t.Errorf("ExitedCleanly = false, want true")
	}
	// The slot teardown closes the slot's session on the shared runtime,
	// scoped by session id, rather than tearing the pod runtime down.
	if got := rt.closed; len(got) != 1 || got[0] != "sess-a" {
		t.Errorf("runtime Close calls = %v, want [sess-slot-a]", got)
	}
	if _, err := os.Stat(filepath.Join(s.WorkspaceBase, "slots", "sess-a")); !os.IsNotExist(err) {
		t.Errorf("slot-a tree not removed on shutdown")
	}
	// slot-b's tree survives.
	if _, err := os.Stat(filepath.Join(s.WorkspaceBase, "slots", "sess-b", "current")); err != nil {
		t.Errorf("sibling slot-b tree was removed: %v", err)
	}
}

// spec: §6.4 — a malformed slot id (path traversal) is rejected before any
// filesystem path is derived.
func TestStartSessionSlotRejectsBadSlotID_spec_6_4(t *testing.T) {
	s, _ := concurrentServer(t)
	_, err := s.StartSession(context.Background(), slotStartReq("../escape"))
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("bad slot id code = %v, want InvalidArgument", status.Code(err))
	}
}

// spec: §6.4; spec/05:509 — a slot-qualified StartSession needs the single
// pod-global runtime configured; with no runtime it fails closed.
func TestStartSessionSlotRequiresRuntime_spec_6_4(t *testing.T) {
	s, _ := concurrentServer(t)
	s.Runtime = nil
	_, err := s.StartSession(context.Background(), slotStartReq("sess-a"))
	if status.Code(err) != codes.FailedPrecondition {
		t.Errorf("no-runtime code = %v, want FailedPrecondition", status.Code(err))
	}
}

// spec: §5.2; §6.4 — a single-session pod takes the same per-slot path:
// the start binds the session's slot and starts the session on the pod's
// one runtime, with no branch on the pool's concurrency.
func TestSingleSessionPodTakesThePerSlotPath_spec_5_2(t *testing.T) {
	s, rt, _ := sessionServer(t)
	if _, err := s.StartSession(context.Background(), startReq("sess-a")); err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	if len(rt.started) != 1 || rt.started[0] != "sess-a" {
		t.Errorf("base runtime started = %v, want [sess-a] on an exclusive pool", rt.started)
	}
}
