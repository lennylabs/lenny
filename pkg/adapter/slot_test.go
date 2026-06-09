// SPDX-License-Identifier: MIT

package adapter_test

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/lennylabs/lenny/pkg/adapter"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// concurrentServer builds an adapter Server in §6.4 concurrent-workspace
// mode wired to fresh temp roots and a RuntimeFactory that hands out a
// fresh fakeRuntime per slot, recording each so a test can assert
// per-slot routing.
func concurrentServer(t *testing.T) (*adapter.Server, *slotFactory, adapter.SlotRuntimePaths) {
	t.Helper()
	base := t.TempDir()
	s := adapter.New("test")
	s.ConcurrentWorkspace = true
	s.WorkspaceBase = filepath.Join(base, "workspace")
	s.SessionsRoot = filepath.Join(base, "sessions")
	s.ArtifactsRoot = filepath.Join(base, "artifacts")
	s.CredentialsDir = filepath.Join(base, "run", "lenny")
	f := &slotFactory{runtimes: map[string]*fakeRuntime{}}
	s.RuntimeFactory = f.make
	return s, f, adapter.SlotRuntimePaths{}
}

// slotFactory records the per-slot fakeRuntimes RuntimeFactory creates.
type slotFactory struct {
	mu       sync.Mutex
	runtimes map[string]*fakeRuntime
}

func (f *slotFactory) make(slotID string, _ adapter.SlotRuntimePaths) (adapter.RuntimeProcess, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	rt := &fakeRuntime{}
	f.runtimes[slotID] = rt
	return rt, nil
}

func (f *slotFactory) get(slotID string) *fakeRuntime {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.runtimes[slotID]
}

func slotStartReq(sessionID, slotID string) *adapterv1.StartSessionRequest {
	return &adapterv1.StartSessionRequest{
		SessionId: &adapterv1.SessionId{Value: sessionID},
		Runtime:   "echo",
		SlotId:    &adapterv1.SlotId{Value: slotID},
	}
}

// spec: §6.4 lines 385-405 — a slot-qualified StartSession creates the
// per-slot directory tree and starts the slot's runtime.
func TestStartSessionSlotCreatesTreeAndStartsRuntime_spec_6_4(t *testing.T) {
	s, f, _ := concurrentServer(t)
	if _, err := s.StartSession(context.Background(), slotStartReq("sess-a", "slot-a")); err != nil {
		t.Fatalf("StartSession(slot): %v", err)
	}
	rt := f.get("slot-a")
	if rt == nil || len(rt.started) != 1 || rt.started[0] != "sess-a" {
		t.Fatalf("slot runtime not started for sess-a: %+v", rt)
	}
	// The §6.4 per-slot tree must exist on disk.
	for _, sub := range []string{
		filepath.Join(s.WorkspaceBase, "slots", "slot-a", "current"),
		filepath.Join(s.WorkspaceBase, "slots", "slot-a", "staging"),
		filepath.Join(s.SessionsRoot, "slot-a"),
		filepath.Join(s.ArtifactsRoot, "slot-a"),
		filepath.Join(s.CredentialsDir, "slots", "slot-a"),
	} {
		if info, err := os.Stat(sub); err != nil || !info.IsDir() {
			t.Errorf("expected slot dir %q to exist: err=%v", sub, err)
		}
	}
}

// spec: §5.2 concurrent mode — two distinct slots coexist on one pod.
func TestStartSessionSlotAllowsConcurrentSlots_spec_5_2(t *testing.T) {
	s, f, _ := concurrentServer(t)
	if _, err := s.StartSession(context.Background(), slotStartReq("sess-a", "slot-a")); err != nil {
		t.Fatalf("StartSession(slot-a): %v", err)
	}
	if _, err := s.StartSession(context.Background(), slotStartReq("sess-b", "slot-b")); err != nil {
		t.Fatalf("StartSession(slot-b): %v", err)
	}
	if f.get("slot-a") == nil || f.get("slot-b") == nil {
		t.Fatalf("both slots should have runtimes")
	}
}

// spec: §6.4 — re-claiming an already-active slot id is rejected.
func TestStartSessionSlotRejectsDuplicateSlot_spec_6_4(t *testing.T) {
	s, _, _ := concurrentServer(t)
	if _, err := s.StartSession(context.Background(), slotStartReq("sess-a", "slot-a")); err != nil {
		t.Fatalf("first StartSession: %v", err)
	}
	_, err := s.StartSession(context.Background(), slotStartReq("sess-b", "slot-a"))
	if status.Code(err) != codes.Unavailable {
		t.Errorf("duplicate slot code = %v, want Unavailable", status.Code(err))
	}
}

// spec: §6.4 line 404 — a slot-qualified finalize materializes into the
// slot's /workspace/slots/{slotId}/current cwd.
func TestFinalizeWorkspaceSlotMaterializesPerSlot_spec_6_4(t *testing.T) {
	s, _, _ := concurrentServer(t)
	req := &adapterv1.FinalizeWorkspaceRequest{
		SessionId: &adapterv1.SessionId{Value: "sess-a"},
		SlotId:    &adapterv1.SlotId{Value: "slot-a"},
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
	want := filepath.Join(s.WorkspaceBase, "slots", "slot-a", "current", "hello.txt")
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

// spec: §6.1 line 28 — a slot-qualified assignment writes the slot's own
// /run/lenny/slots/{slotId}/credentials.json, not the global file.
func TestAssignCredentialsSlotWritesPerSlotFile_spec_6_1(t *testing.T) {
	s, _, _ := concurrentServer(t)
	leases := map[string]*adapterv1.CredentialLease{
		"anthropic": {LeaseId: "lease-1", Provider: "anthropic"},
	}
	_, err := s.AssignCredentials(context.Background(), &adapterv1.AssignCredentialsRequest{
		SessionId: &adapterv1.SessionId{Value: "sess-a"},
		SlotId:    &adapterv1.SlotId{Value: "slot-a"},
		Leases:    leases,
	})
	if err != nil {
		t.Fatalf("AssignCredentials(slot): %v", err)
	}
	perSlot := filepath.Join(s.CredentialsDir, "slots", "slot-a", "credentials.json")
	if _, err := os.Stat(perSlot); err != nil {
		t.Errorf("expected per-slot credential file at %q: %v", perSlot, err)
	}
	if _, err := os.Stat(filepath.Join(s.CredentialsDir, "credentials.json")); !os.IsNotExist(err) {
		t.Errorf("global credential file written; per-slot isolation broken")
	}
}

// spec: §6.1 line 28 — a rotation on one slot leaves a sibling slot's
// credential file untouched.
func TestRotateCredentialsSlotIsIndependent_spec_6_1(t *testing.T) {
	s, _, _ := concurrentServer(t)
	ctx := context.Background()
	for _, slot := range []string{"slot-a", "slot-b"} {
		if _, err := s.AssignCredentials(ctx, &adapterv1.AssignCredentialsRequest{
			SessionId: &adapterv1.SessionId{Value: "sess-" + slot},
			SlotId:    &adapterv1.SlotId{Value: slot},
			Leases: map[string]*adapterv1.CredentialLease{
				"anthropic": {LeaseId: "lease-" + slot, Provider: "anthropic"},
			},
		}); err != nil {
			t.Fatalf("AssignCredentials(%s): %v", slot, err)
		}
	}
	bFile := filepath.Join(s.CredentialsDir, "slots", "slot-b", "credentials.json")
	before, err := os.ReadFile(bFile)
	if err != nil {
		t.Fatalf("read slot-b creds: %v", err)
	}
	// Rotate slot-a only.
	if _, err := s.RotateCredentials(ctx, &adapterv1.RotateCredentialsRequest{
		SessionId: &adapterv1.SessionId{Value: "sess-slot-a"},
		SlotId:    &adapterv1.SlotId{Value: "slot-a"},
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

// spec: §6.4 lines 401-405 — SendMessage routes to the slot's own runtime.
func TestSendMessageSlotRoutesToSlotRuntime_spec_6_4(t *testing.T) {
	s, f, _ := concurrentServer(t)
	ctx := context.Background()
	for _, slot := range []string{"slot-a", "slot-b"} {
		if _, err := s.StartSession(ctx, slotStartReq("sess-"+slot, slot)); err != nil {
			t.Fatalf("StartSession(%s): %v", slot, err)
		}
	}
	if _, err := s.SendMessage(ctx, &adapterv1.SendMessageRequest{
		SessionId:    &adapterv1.SessionId{Value: "sess-slot-a"},
		SlotId:       &adapterv1.SlotId{Value: "slot-a"},
		EnvelopeJson: []byte(`{"type":"message"}`),
	}); err != nil {
		t.Fatalf("SendMessage(slot-a): %v", err)
	}
	if got := len(f.get("slot-a").envelopesSnapshot()); got != 1 {
		t.Errorf("slot-a runtime envelopes = %d, want 1", got)
	}
	if got := len(f.get("slot-b").envelopesSnapshot()); got != 0 {
		t.Errorf("slot-b runtime envelopes = %d, want 0 (message must not cross slots)", got)
	}
}

// spec: §6.4 lines 401-405 — Shutdown tears down the slot's runtime and
// removes its per-slot tree; a sibling slot is unaffected.
func TestShutdownSlotRemovesTree_spec_6_4(t *testing.T) {
	s, f, _ := concurrentServer(t)
	ctx := context.Background()
	for _, slot := range []string{"slot-a", "slot-b"} {
		if _, err := s.StartSession(ctx, slotStartReq("sess-"+slot, slot)); err != nil {
			t.Fatalf("StartSession(%s): %v", slot, err)
		}
	}
	resp, err := s.Shutdown(ctx, &adapterv1.ShutdownRequest{
		SessionId: &adapterv1.SessionId{Value: "sess-slot-a"},
		SlotId:    &adapterv1.SlotId{Value: "slot-a"},
	})
	if err != nil {
		t.Fatalf("Shutdown(slot-a): %v", err)
	}
	if !resp.GetExitedCleanly() {
		t.Errorf("ExitedCleanly = false, want true")
	}
	if len(f.get("slot-a").closed) != 1 {
		t.Errorf("slot-a runtime not closed")
	}
	if _, err := os.Stat(filepath.Join(s.WorkspaceBase, "slots", "slot-a")); !os.IsNotExist(err) {
		t.Errorf("slot-a tree not removed on shutdown")
	}
	// slot-b's tree survives.
	if _, err := os.Stat(filepath.Join(s.WorkspaceBase, "slots", "slot-b", "current")); err != nil {
		t.Errorf("sibling slot-b tree was removed: %v", err)
	}
}

// spec: §6.4 — a malformed slot id (path traversal) is rejected before any
// filesystem path is derived.
func TestStartSessionSlotRejectsBadSlotID_spec_6_4(t *testing.T) {
	s, _, _ := concurrentServer(t)
	_, err := s.StartSession(context.Background(), slotStartReq("sess-a", "../escape"))
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("bad slot id code = %v, want InvalidArgument", status.Code(err))
	}
}

// spec: §6.4 — concurrent mode without a RuntimeFactory cannot start a slot.
func TestStartSessionSlotRequiresFactory_spec_6_4(t *testing.T) {
	s, _, _ := concurrentServer(t)
	s.RuntimeFactory = nil
	_, err := s.StartSession(context.Background(), slotStartReq("sess-a", "slot-a"))
	if status.Code(err) != codes.FailedPrecondition {
		t.Errorf("no-factory code = %v, want FailedPrecondition", status.Code(err))
	}
}

// spec: §6.4 — a non-concurrent adapter ignores a stray slot id and keeps
// the one-session-only base path (the slot field is inert in session mode).
func TestSlotIgnoredWhenNotConcurrent_spec_6_4(t *testing.T) {
	s, rt, _ := sessionServer(t)
	// ConcurrentWorkspace is false; a slot-qualified StartSession falls
	// through to the base claim and uses the pod-global runtime.
	if _, err := s.StartSession(context.Background(), slotStartReq("sess-a", "slot-a")); err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	if len(rt.started) != 1 {
		t.Errorf("base runtime not started in session mode with a stray slot id")
	}
}
