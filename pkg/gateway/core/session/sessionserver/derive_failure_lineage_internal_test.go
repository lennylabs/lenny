// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
)

// TestPersistDeriveFailureLineage_spec_4_5 pins the derive-failure audit
// row's lineage: it never carries a delegation parent, and it records the
// source's snapshot as its parent workspace only when the source has one.
//
// spec: §15.1 (Session Replay Semantics), §4.5 (Workspace lineage)
func TestPersistDeriveFailureLineage_spec_4_5(t *testing.T) {
	cases := []struct {
		name     string
		snapshot *sessionstore.WorkspaceSnapshot
		wantRef  string
	}{
		{name: "source with a snapshot", snapshot: &sessionstore.WorkspaceSnapshot{Ref: "/acme/workspace/sess_src/snap"}, wantRef: "/acme/workspace/sess_src/snap"},
		{name: "source without a snapshot"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ctx := context.Background()
			store := memstore.New()
			src := sessionstore.Session{
				ID: "sess_src", TenantID: "acme", UserID: "alice@acme.com",
				State: session.StateCompleted, RuntimeRef: "claude-code",
				CreatedAt: time.Unix(0, 0).UTC(), UpdatedAt: time.Unix(0, 0).UTC(),
				WorkspaceSnapshot: c.snapshot,
			}
			if err := store.Create(ctx, src); err != nil {
				t.Fatalf("seed source: %v", err)
			}
			src, _ = store.Get(ctx, "acme", "sess_src")
			s := New(store, Options{PersistDeriveFailureRows: true})

			s.persistDeriveFailure(ctx, src, "sess_df", isolation.ProfileSandboxed, "workspace_copy_failed")

			row, err := store.Get(ctx, "acme", "sess_df")
			if err != nil {
				t.Fatalf("read audit row: %v", err)
			}
			if row.ParentSessionID != "" || row.RootSessionID != "sess_df" {
				t.Errorf("parent = %q root = %q, want no parent and its own id as root", row.ParentSessionID, row.RootSessionID)
			}
			if row.ParentWorkspaceRef != c.wantRef {
				t.Errorf("ParentWorkspaceRef = %q, want %q", row.ParentWorkspaceRef, c.wantRef)
			}
		})
	}
}
