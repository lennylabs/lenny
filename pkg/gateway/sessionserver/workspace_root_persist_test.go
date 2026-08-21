// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
)

// spec: §7.3 step (d); §6.4 — persistWorkspaceRoot takes the workspace
// base the adapter reported and records the session's derived slot root
// `<base>/slots/{sessionId}/current` on the session row at the first
// non-empty bind, so a subsequent Resume can pass it as
// `expected_workspace_root` for the §7.3 "same absolute cwd path"
// assertion. The derivation lives in this callee rather than at its call
// sites: the write is first-non-empty-wins, so a site passing an underived
// base would fix the column at the pod's base. F-7.3.15.
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
	srv.persistWorkspaceRoot(context.Background(), "acme", "s1", "/workspace")
	got, err := store.Get(context.Background(), "acme", "s1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.WorkspaceRoot != "/workspace/slots/s1/current" {
		t.Errorf("WorkspaceRoot = %q, want /workspace/slots/s1/current", got.WorkspaceRoot)
	}
}

// spec: §7.3 step (d); §6.4 — a value that already names a session's slot
// root is never handed to the persist. The bind paths carry the reported
// base verbatim, so the column holds one derivation rather than two: a
// second derivation would write
// `<base>/slots/{sid}/current/slots/{sid}/current`, which the adapter's
// §7.3 step (d) guard rejects on every resume. This case pins the input
// contract by driving the persist with the base the bind carries and
// asserting the column holds exactly one slot segment. F-7.3.15.
func TestPersistWorkspaceRootDerivesSlotRootOnce_spec_7_3_15(t *testing.T) {
	store := memstore.New()
	now := time.Date(2026, 5, 26, 10, 0, 0, 0, time.UTC)
	if err := store.Create(context.Background(), sessionstore.Session{
		ID: "s4", TenantID: "acme", RuntimeRef: "claude-code",
		State: session.StateRunning, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	srv := New(store, Options{})
	srv.persistWorkspaceRoot(context.Background(), "acme", "s4", "/workspace")
	got, err := store.Get(context.Background(), "acme", "s4")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if want := "/workspace/slots/s4/current"; got.WorkspaceRoot != want {
		t.Fatalf("WorkspaceRoot = %q, want %q", got.WorkspaceRoot, want)
	}
	if strings.Count(got.WorkspaceRoot, "/slots/") != 1 {
		t.Errorf("WorkspaceRoot %q carries a doubled derivation", got.WorkspaceRoot)
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
		WorkspaceRoot: "/workspace/slots/s2/current",
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	srv := New(store, Options{})
	srv.persistWorkspaceRoot(context.Background(), "acme", "s2", "")
	got, _ := store.Get(context.Background(), "acme", "s2")
	if got.WorkspaceRoot != "/workspace/slots/s2/current" {
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
		WorkspaceRoot: "/workspace/slots/s3/current",
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	srv := New(store, Options{})
	srv.persistWorkspaceRoot(context.Background(), "acme", "s3", "/replacement-workspace")
	got, _ := store.Get(context.Background(), "acme", "s3")
	if got.WorkspaceRoot != "/workspace/slots/s3/current" {
		t.Errorf("F-7.3.15: replacement bind overwrote original: got %q", got.WorkspaceRoot)
	}
}
