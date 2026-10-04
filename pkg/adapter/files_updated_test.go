// SPDX-License-Identifier: MIT

package adapter

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/adapter/workspace"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// TestSignalFilesUpdatedEmitsFrame asserts the §7.4 files_updated
// lifecycle signal reaches a connected runtime. F-7.4.6.
func TestSignalFilesUpdatedEmitsFrame_spec_7_4_433(t *testing.T) {
	lc, fr := startRuntimeOps(t)
	fr.handshake()

	if err := lc.SignalFilesUpdated("sess-a"); err != nil {
		t.Fatalf("SignalFilesUpdated: %v", err)
	}
	f := fr.read()
	if f.Type != "files_updated" || f.SessionID != "sess-a" {
		t.Errorf("frame = %+v, want files_updated for sess-a", f)
	}
}

// TestSignalFilesUpdatedNoRuntimeIsBenign asserts that signaling before any
// runtime has connected (the pre-start path) returns the not-connected
// sentinel rather than panicking, so FinalizeWorkspace can ignore it.
// F-7.4.6.
func TestSignalFilesUpdatedNoRuntimeIsBenign_spec_7_4_433(t *testing.T) {
	lc, err := NewRuntimeOps(shortSocketName(t, "lc.sock"), SocketPeerAuth{ExpectedUID: uint32(os.Getuid())})
	if err != nil {
		t.Fatalf("NewRuntimeOps: %v", err)
	}
	t.Cleanup(func() { _ = lc.Close() })
	// No Run, no runtime connection: writeFrame has no encoder.
	if err := lc.SignalFilesUpdated("sess-a"); !errors.Is(err, errLifecycleNotConnected) {
		t.Errorf("SignalFilesUpdated with no runtime = %v, want errLifecycleNotConnected", err)
	}
}

// TestFinalizeWorkspaceMidSessionOverlaysAndSignals is the adapter-side
// end-to-end of a §7.4 mid-session upload: an overlay that preserves the
// running agent's existing files plus a files_updated signal emitted only
// after promotion. With a second session's entry on the same pod, the
// frame names the session whose workspace was promoted. F-7.4.6.
// spec: §7.4, §28.5.3 (CH-RUNTIMEOPS, Messages).
func TestFinalizeWorkspaceMidSessionOverlaysAndSignals_spec_7_4_433(t *testing.T) {
	root := t.TempDir()
	// The running agent's existing workspace content, in its own slot tree.
	current := filepath.Join(root, "slots", "sess-mid", "current")
	staging := filepath.Join(root, "slots", "sess-mid", "staging")
	for _, d := range []string{current, staging} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(current, "work.txt"), []byte("agent work"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A staged upload the gateway streamed via PrepareWorkspace.
	stagedPath, err := workspace.StagingPath(staging, "midupload-0")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stagedPath, []byte("new content"), 0o600); err != nil {
		t.Fatal(err)
	}

	lc, fr := startRuntimeOps(t)
	fr.handshake()
	srv := &Server{WorkspaceBase: root, Lifecycle: lc}
	// §4.7.1 rule 3: a mid-session request resolves an entry the session
	// already holds and never creates one, so the running session's entry is
	// seeded first.
	if _, err := srv.ensureSlotPaths("sess-mid", slotResolve{allowCreate: true}); err != nil {
		t.Fatalf("seed sess-mid entry: %v", err)
	}
	if _, err := srv.ensureSlotPaths("sess-cotenant", slotResolve{allowCreate: true}); err != nil {
		t.Fatalf("seed sess-cotenant entry: %v", err)
	}

	req := &adapterv1.FinalizeWorkspaceRequest{
		SessionId:  &adapterv1.SessionId{Value: "sess-mid"},
		MidSession: true,
		WorkspacePlan: &adapterv1.WorkspacePlan{
			SchemaVersion: 1,
			Sources: []*adapterv1.WorkspaceSource{
				{Type: "uploadFile", Path: "uploads/added.bin", UploadRef: "midupload-0", Mode: "644"},
			},
		},
	}

	// The signal is emitted synchronously inside FinalizeWorkspace via the
	// one-way lifecycle write; read it from the runtime side concurrently so
	// the unbuffered socket does not deadlock the writer.
	got := make(chan lifecycleFrame, 1)
	go func() { got <- fr.read() }()

	if _, err := srv.FinalizeWorkspace(context.Background(), req); err != nil {
		t.Fatalf("FinalizeWorkspace(mid_session): %v", err)
	}

	// Overlay landed and the agent's pre-existing file survived.
	if b, _ := os.ReadFile(filepath.Join(current, "uploads", "added.bin")); string(b) != "new content" {
		t.Errorf("overlaid file = %q, want %q", b, "new content")
	}
	if b, _ := os.ReadFile(filepath.Join(current, "work.txt")); string(b) != "agent work" {
		t.Errorf("mid-session overlay clobbered the agent's existing file: %q", b)
	}
	// files_updated was signaled.
	select {
	case frame := <-got:
		if frame.Type != "files_updated" {
			t.Errorf("lifecycle frame = %q, want files_updated", frame.Type)
		}
		if frame.SessionID != "sess-mid" {
			t.Errorf("files_updated sessionId = %q, want sess-mid (the promoted session, not its co-tenant)", frame.SessionID)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("FinalizeWorkspace(mid_session) did not signal files_updated")
	}
}

// TestFinalizeWorkspacePreStartDoesNotSignal asserts the default (pre-start)
// path neither overlays nor emits files_updated: it replaces the whole tree
// and stays silent on the CH-RUNTIMEOPS. F-7.4.6.
func TestFinalizeWorkspacePreStartDoesNotSignal_spec_7_4_433(t *testing.T) {
	root := t.TempDir()
	// A leftover file in the session's own tree the whole-tree promotion
	// is expected to discard.
	current := filepath.Join(root, "slots", "sess-pre", "current")
	if err := os.MkdirAll(current, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(current, "stale.txt"), []byte("stale"), 0o644); err != nil {
		t.Fatal(err)
	}
	lc, fr := startRuntimeOps(t)
	fr.handshake()
	srv := &Server{WorkspaceBase: root, Lifecycle: lc}

	if _, err := srv.FinalizeWorkspace(context.Background(), finalizeReq("sess-pre",
		wsSource("inlineFile", "fresh.txt", "fresh", "644"))); err != nil {
		t.Fatalf("FinalizeWorkspace(pre-start): %v", err)
	}
	// Whole-tree replacement: the fresh file is present, the stale one gone.
	if _, err := os.Stat(filepath.Join(current, "fresh.txt")); err != nil {
		t.Errorf("pre-start finalize did not materialize the plan: %v", err)
	}
	if _, err := os.Stat(filepath.Join(current, "stale.txt")); !os.IsNotExist(err) {
		t.Errorf("pre-start finalize did not replace the prior tree")
	}
	// No files_updated frame is emitted on the pre-start path.
	_ = fr.conn.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
	if _, err := readLifecycleFrame(fr.r); err == nil {
		t.Error("pre-start finalize emitted a lifecycle frame, want none")
	}
}
