// SPDX-License-Identifier: MIT

package checkpointer_test

import (
	"context"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/checkpoint/checkpointer"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
)

// spec: §7.3 line 397 / §10.1 line 132 — the gateway persists the
// adapter-reported total byte count from the CheckpointSummary frame on
// WorkspaceSnapshot.Bytes. The §10.1 preStop tiered-cap selection reads
// it via the SessionEnumerator, and the §7.2 line 138
// workspaceRecoveryFraction depends on it for partial-workspace resumes.
// F-7.3.21.
func TestCheckpointPersistsWorkspaceBytes_F_7_3_21(t *testing.T) {
	client := dialAdapter(t, fakeCheckpointAdapter{bytes: 4096})
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
	if row.WorkspaceSnapshot.Bytes != 4096 {
		t.Errorf("F-7.3.21: WorkspaceSnapshot.Bytes = %d, want 4096 (the CheckpointSummary total)", row.WorkspaceSnapshot.Bytes)
	}
}
