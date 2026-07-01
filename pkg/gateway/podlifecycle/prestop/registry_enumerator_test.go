// SPDX-License-Identifier: MIT

package prestop_test

import (
	"context"
	"testing"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/prestop"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
)

// spec: §7.3 line 397 / §10.1 — the RegistryEnumerator surfaces
// last_checkpoint_workspace_bytes when a SessionStore is wired, and
// falls back to the postgres_null path otherwise. F-7.3.21.

func seedRunningWithSnapshot(t *testing.T, store sessionstore.Store, id string, bytes int64) {
	t.Helper()
	row := sessionstore.Session{
		ID:       id,
		TenantID: "acme",
		State:    session.StateRunning,
	}
	if bytes > 0 {
		row.WorkspaceSnapshot = &sessionstore.WorkspaceSnapshot{
			Ref:    "ckpt-" + id,
			Source: sessionstore.WorkspaceSnapshotCheckpoint,
			Bytes:  bytes,
		}
	}
	if err := store.Create(context.Background(), row); err != nil {
		t.Fatalf("seed %s: %v", id, err)
	}
}

func TestRegistryEnumeratorSurfacesPersistedBytes_F_7_3_21(t *testing.T) {
	reg := podsession.NewRegistry()
	reg.Put(&podsession.BindResult{SessionID: "sess_a", TenantID: "acme"})
	store := memstore.New()
	seedRunningWithSnapshot(t, store, "sess_a", 12345678)

	enum := &prestop.RegistryEnumerator{
		Registry:    reg,
		Sessions:    store,
		DefaultPool: "default",
	}
	infos, err := enum.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if len(infos) != 1 {
		t.Fatalf("len = %d, want 1", len(infos))
	}
	if infos[0].LastCheckpointWorkspaceBytes != 12345678 {
		t.Errorf("Bytes = %d, want 12345678", infos[0].LastCheckpointWorkspaceBytes)
	}
	if infos[0].IsPostgresNull {
		t.Errorf("IsPostgresNull = true, want false when bytes are persisted")
	}
}

func TestRegistryEnumeratorFallsBackToPostgresNullForUncheckpointed_F_7_3_21(t *testing.T) {
	reg := podsession.NewRegistry()
	reg.Put(&podsession.BindResult{SessionID: "sess_b", TenantID: "acme"})
	store := memstore.New()
	seedRunningWithSnapshot(t, store, "sess_b", 0) // no snapshot

	enum := &prestop.RegistryEnumerator{Registry: reg, Sessions: store, DefaultPool: "default"}
	infos, err := enum.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if len(infos) != 1 {
		t.Fatalf("len = %d, want 1", len(infos))
	}
	if !infos[0].IsPostgresNull {
		t.Errorf("IsPostgresNull = false, want true when no checkpoint bytes")
	}
	if infos[0].LastCheckpointWorkspaceBytes != 0 {
		t.Errorf("Bytes = %d, want 0", infos[0].LastCheckpointWorkspaceBytes)
	}
}

// A nil Sessions store keeps the legacy postgres_null path so the
// minimal gateway / tests without a wired store behave as before.
func TestRegistryEnumeratorNilStoreKeepsPostgresNull_F_7_3_21(t *testing.T) {
	reg := podsession.NewRegistry()
	reg.Put(&podsession.BindResult{SessionID: "sess_c", TenantID: "acme"})

	enum := &prestop.RegistryEnumerator{Registry: reg, DefaultPool: "default"}
	infos, err := enum.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if len(infos) != 1 || !infos[0].IsPostgresNull {
		t.Errorf("nil Sessions: want one postgres_null entry, got %+v", infos)
	}
}
