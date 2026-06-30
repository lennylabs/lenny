// SPDX-License-Identifier: MIT

package checkpointer_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/lennylabs/lenny/pkg/adapter"
	"github.com/lennylabs/lenny/pkg/gateway/checkpoint/checkpointer"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/adapterclient"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
)

// spec: §7.3 line 397 — the gateway persists the adapter-reported
// compressed checkpoint size on WorkspaceSnapshot.Bytes. The §10.1
// preStop tiered-cap selection reads it via the SessionEnumerator,
// and the §7.2 line 138 workspaceRecoveryFraction depends on it for
// partial-workspace resumes. F-7.3.21.
func TestCheckpointPersistsWorkspaceBytes_F_7_3_21(t *testing.T) {
	srv := adapter.New("checkpointer-test")
	srv.WorkspaceRoot = t.TempDir()
	// Write a non-trivial workspace file so the tar archive carries
	// real bytes — the adapter reports the streamed size on success.
	payload := make([]byte, 4096)
	for i := range payload {
		payload[i] = 'A'
	}
	if err := os.WriteFile(filepath.Join(srv.WorkspaceRoot, "data.bin"), payload, 0o644); err != nil {
		t.Fatalf("seed workspace: %v", err)
	}
	srv.Runtime = stubRuntime{}
	srv.Checkpoints = stubSink{id: "ckpt-bytes"}
	client := dialAdapter(t, srv)
	if err := client.StartSession(context.Background(), adapterclient.StartSessionParams{SessionID: "s1", Runtime: "echo"}); err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	registry := podsession.NewRegistry()
	registry.Put(&podsession.BindResult{SessionID: "s1", Adapter: client})

	store := memstore.New()
	runningSession(t, store, "acme", "s1")
	cp := &checkpointer.Checkpointer{Sessions: store, Registry: registry}
	if err := cp.Checkpoint(context.Background(), "acme", "s1"); err != nil {
		t.Fatalf("Checkpoint: %v", err)
	}
	row, _ := store.Get(context.Background(), "acme", "s1")
	if row.WorkspaceSnapshot == nil {
		t.Fatal("no WorkspaceSnapshot recorded")
	}
	if row.WorkspaceSnapshot.Bytes <= 0 {
		t.Errorf("F-7.3.21: WorkspaceSnapshot.Bytes = %d, want > 0 (a non-empty workspace must report a size)", row.WorkspaceSnapshot.Bytes)
	}
}
