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

// TestSignalFilesUpdatedEmitsFrame asserts the §7.4 line 433 files_updated
// lifecycle signal reaches a connected runtime. F-7.4.6.
func TestSignalFilesUpdatedEmitsFrame_spec_7_4_433(t *testing.T) {
	lc, fr := startLifecycleChannel(t)
	fr.handshake()

	if err := lc.SignalFilesUpdated(); err != nil {
		t.Fatalf("SignalFilesUpdated: %v", err)
	}
	f := fr.read()
	if f.Type != "files_updated" {
		t.Errorf("frame type = %q, want files_updated", f.Type)
	}
}

// TestSignalFilesUpdatedNoRuntimeIsBenign asserts that signaling before any
// runtime has connected (the pre-start path) returns the not-connected
// sentinel rather than panicking, so FinalizeWorkspace can ignore it.
// F-7.4.6.
func TestSignalFilesUpdatedNoRuntimeIsBenign_spec_7_4_433(t *testing.T) {
	dir := t.TempDir()
	lc, err := NewLifecycleChannel(filepath.Join(dir, "lc.sock"))
	if err != nil {
		t.Fatalf("NewLifecycleChannel: %v", err)
	}
	t.Cleanup(func() { _ = lc.Close() })
	// No Run, no runtime connection: writeFrame has no encoder.
	if err := lc.SignalFilesUpdated(); !errors.Is(err, errLifecycleNotConnected) {
		t.Errorf("SignalFilesUpdated with no runtime = %v, want errLifecycleNotConnected", err)
	}
}

// TestFinalizeWorkspaceMidSessionOverlaysAndSignals is the adapter-side
// end-to-end of a §7.4 mid-session upload: an overlay that preserves the
// running agent's existing files plus a files_updated signal emitted only
// after promotion. F-7.4.6.
func TestFinalizeWorkspaceMidSessionOverlaysAndSignals_spec_7_4_433(t *testing.T) {
	root := t.TempDir()
	staging := t.TempDir()
	// The running agent's existing workspace content.
	if err := os.WriteFile(filepath.Join(root, "work.txt"), []byte("agent work"), 0o644); err != nil {
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

	lc, fr := startLifecycleChannel(t)
	fr.handshake()
	srv := &Server{WorkspaceRoot: root, StagingDir: staging, Lifecycle: lc}

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
	got := make(chan string, 1)
	go func() { got <- fr.read().Type }()

	if _, err := srv.FinalizeWorkspace(context.Background(), req); err != nil {
		t.Fatalf("FinalizeWorkspace(mid_session): %v", err)
	}

	// Overlay landed and the agent's pre-existing file survived.
	if b, _ := os.ReadFile(filepath.Join(root, "uploads", "added.bin")); string(b) != "new content" {
		t.Errorf("overlaid file = %q, want %q", b, "new content")
	}
	if b, _ := os.ReadFile(filepath.Join(root, "work.txt")); string(b) != "agent work" {
		t.Errorf("mid-session overlay clobbered the agent's existing file: %q", b)
	}
	// files_updated was signaled.
	select {
	case frameType := <-got:
		if frameType != "files_updated" {
			t.Errorf("lifecycle frame = %q, want files_updated", frameType)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("FinalizeWorkspace(mid_session) did not signal files_updated")
	}
}

// TestFinalizeWorkspacePreStartDoesNotSignal asserts the default (pre-start)
// path neither overlays nor emits files_updated: it replaces the whole tree
// and stays silent on the lifecycle channel. F-7.4.6.
func TestFinalizeWorkspacePreStartDoesNotSignal_spec_7_4_433(t *testing.T) {
	root := t.TempDir()
	// A leftover file the whole-tree promotion is expected to discard.
	if err := os.WriteFile(filepath.Join(root, "stale.txt"), []byte("stale"), 0o644); err != nil {
		t.Fatal(err)
	}
	lc, fr := startLifecycleChannel(t)
	fr.handshake()
	srv := &Server{WorkspaceRoot: root, Lifecycle: lc}

	if _, err := srv.FinalizeWorkspace(context.Background(), finalizeReq("sess-pre",
		wsSource("inlineFile", "fresh.txt", "fresh", "644"))); err != nil {
		t.Fatalf("FinalizeWorkspace(pre-start): %v", err)
	}
	// Whole-tree replacement: the fresh file is present, the stale one gone.
	if _, err := os.Stat(filepath.Join(root, "fresh.txt")); err != nil {
		t.Errorf("pre-start finalize did not materialize the plan: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "stale.txt")); !os.IsNotExist(err) {
		t.Errorf("pre-start finalize did not replace the prior tree")
	}
	// No files_updated frame is emitted on the pre-start path.
	_ = fr.conn.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
	if _, err := readLifecycleFrame(fr.r); err == nil {
		t.Error("pre-start finalize emitted a lifecycle frame, want none")
	}
}
