// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
)

// spec: §7.3 line 408 step (d) — persistWorkspaceRoot records the
// adapter-reported absolute cwd path on the session row at the first
// non-empty bind so a subsequent Resume can pass it as
// `expected_workspace_root` for the §7.3 "same absolute cwd path"
// assertion. F-7.3.15.
func TestPersistWorkspaceRootRecordsFirstValue_spec_7_3_15(t *testing.T) {
	store := memstore.New()
	now := time.Date(2026, 5, 26, 10, 0, 0, 0, time.UTC)
	if err := store.Create(context.Background(), sessionstore.Session{
		ID: "s1", TenantID: "acme", RuntimeRef: "claude-code",
		State: session.StateRunning, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	srv := New(store, Options{})
	srv.persistWorkspaceRoot(context.Background(), "acme", "s1", "/workspace/current")
	got, err := store.Get(context.Background(), "acme", "s1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.WorkspaceRoot != "/workspace/current" {
		t.Errorf("WorkspaceRoot = %q, want /workspace/current", got.WorkspaceRoot)
	}
}

// spec: an empty payload must never overwrite a recorded value so an
// older replacement-bind adapter cannot clobber the original's value.
// F-7.3.15.
func TestPersistWorkspaceRootIgnoresEmpty_spec_7_3_15(t *testing.T) {
	store := memstore.New()
	now := time.Date(2026, 5, 26, 10, 0, 0, 0, time.UTC)
	if err := store.Create(context.Background(), sessionstore.Session{
		ID: "s2", TenantID: "acme", RuntimeRef: "claude-code",
		State: session.StateRunning, CreatedAt: now, UpdatedAt: now,
		WorkspaceRoot: "/workspace/current",
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	srv := New(store, Options{})
	srv.persistWorkspaceRoot(context.Background(), "acme", "s2", "")
	got, _ := store.Get(context.Background(), "acme", "s2")
	if got.WorkspaceRoot != "/workspace/current" {
		t.Errorf("F-7.3.15: empty payload overwrote recorded value: got %q", got.WorkspaceRoot)
	}
}

// spec: a subsequent non-empty payload must not overwrite a recorded
// value — the §7.3 contract is "original session's cwd"; a Resume's
// replacement-pod handshake reports its own value, which must NOT
// shadow the original. The first-non-empty-write semantics belong on
// the Server helper (the pgstore guard also enforces it via SQL but
// the in-memory path needs the same guarantee). F-7.3.15.
func TestPersistWorkspaceRootDoesNotOverwriteRecorded_spec_7_3_15(t *testing.T) {
	store := memstore.New()
	now := time.Date(2026, 5, 26, 10, 0, 0, 0, time.UTC)
	if err := store.Create(context.Background(), sessionstore.Session{
		ID: "s3", TenantID: "acme", RuntimeRef: "claude-code",
		State: session.StateRunning, CreatedAt: now, UpdatedAt: now,
		WorkspaceRoot: "/workspace/current",
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	srv := New(store, Options{})
	srv.persistWorkspaceRoot(context.Background(), "acme", "s3", "/workspace/replacement")
	got, _ := store.Get(context.Background(), "acme", "s3")
	if got.WorkspaceRoot != "/workspace/current" {
		t.Errorf("F-7.3.15: replacement bind overwrote original: got %q", got.WorkspaceRoot)
	}
}
