// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/auth/authmw"
	"github.com/lennylabs/lenny/pkg/gateway/seam"
	"github.com/lennylabs/lenny/pkg/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/session/sessionstore/memstore"
)

// TestTerminalRowReadsBackTheStoredRow checks that the terminal pipeline a
// seam runs gets the stored row, with the fields the session record does
// not carry, and the record's fields when the row cannot be read.
//
// spec: GWSEAM-OnSessionTerminal
func TestTerminalRowReadsBackTheStoredRow(t *testing.T) {
	store := memstore.New()
	at := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	if err := store.Create(context.Background(), sessionstore.Session{
		ID: "s1", TenantID: "acme", UserID: testOwner, State: session.StateExpired,
		FailureReason: string(session.FailureExpiredDeadline), PodAssignment: "pod-1",
		CreatedAt: at, UpdatedAt: at,
	}); err != nil {
		t.Fatal(err)
	}
	srv := New(store, Options{DevTenantHeader: true})
	defer srv.Close()
	row := srv.terminalRow(context.Background(), seam.Session{ID: "s1", TenantID: "acme"})
	if row.FailureReason != string(session.FailureExpiredDeadline) || row.PodAssignment != "pod-1" {
		t.Errorf("row = %+v, want the stored row", row)
	}
	gone := srv.terminalRow(context.Background(), seam.Session{ID: "gone", TenantID: "acme", PodAssignment: "pod-2", State: "failed"})
	if gone.ID != "gone" || gone.PodAssignment != "pod-2" || gone.State != session.StateFailed {
		t.Errorf("fallback row = %+v, want the record's fields", gone)
	}
}

// TestStoredRowIsUsedOnlyForTheSessionTheRecordNames checks that the core
// runs on a carried row only when the row is the session the record names,
// and otherwise reads the named session back, so a record cannot hand the
// core another session's or another tenant's row.
//
// spec: GWSEAM-OnSessionTerminal, GWSEAM-SessionAccessible
func TestStoredRowIsUsedOnlyForTheSessionTheRecordNames(t *testing.T) {
	store := memstore.New()
	if err := store.Create(context.Background(), sessionstore.Session{
		ID: "s1", TenantID: "acme", UserID: testOwner, State: session.StateRunning, PodAssignment: "pod-1",
	}); err != nil {
		t.Fatal(err)
	}
	srv := New(store, Options{DevTenantHeader: true})
	defer srv.Close()
	carried := sessionstore.Session{ID: "s1", TenantID: "acme", PodAssignment: "pod-carried"}
	if row := srv.terminalRow(context.Background(), seam.Session{ID: "s1", TenantID: "acme", Stored: carried}); row.PodAssignment != "pod-carried" {
		t.Errorf("matching carried row: pod = %q, want pod-carried", row.PodAssignment)
	}
	for name, other := range map[string]sessionstore.Session{
		"another session": {ID: "s2", TenantID: "acme", PodAssignment: "pod-other"},
		"another tenant":  {ID: "s1", TenantID: "globex", PodAssignment: "pod-other"},
	} {
		rec := seam.Session{ID: "s1", TenantID: "acme", Stored: other}
		if row := srv.terminalRow(context.Background(), rec); row.ID != "s1" || row.TenantID != "acme" || row.PodAssignment != "pod-1" {
			t.Errorf("%s: terminal row = %+v, want the stored s1 row", name, row)
		}
		if row := rowOf(rec); row.ID != "s1" || row.TenantID != "acme" {
			t.Errorf("%s: rowOf = %+v, want the record's session", name, row)
		}
	}
}

// TestToolsSeamTreeNodesListFailAndSkipAnUnreadableNode checks that the
// tree node operations list a tree's records, fail a resume_pending node,
// and start no node whose row cannot be read.
//
// spec: GWSEAM-ListTree, GWSEAM-FailNode, GWSEAM-StartNode
func TestToolsSeamTreeNodesListFailAndSkipAnUnreadableNode(t *testing.T) {
	store := memstore.New()
	ctx := context.Background()
	for _, r := range []sessionstore.Session{
		{ID: "root", TenantID: "acme", UserID: testOwner, RootSessionID: "root", State: session.StateRunning, PoolRef: "pool-a"},
		{ID: "n1", TenantID: "acme", UserID: testOwner, RootSessionID: "root", ParentSessionID: "root", DelegationDepth: 1, State: session.StateResumePending},
	} {
		if err := store.Create(ctx, r); err != nil {
			t.Fatal(err)
		}
	}
	srv := New(store, Options{DevTenantHeader: true})
	defer srv.Close()
	nodes := srv.ToolsSeam()
	tree, err := nodes.ListTree(ctx, "acme", "root")
	if err != nil || len(tree) != 2 {
		t.Fatalf("tree = %+v, %v", tree, err)
	}
	for _, n := range tree {
		if n.ID == "n1" && (n.DelegationDepth != 1 || n.State != "resume_pending") {
			t.Errorf("node record = %+v", n)
		}
	}
	nodes.StartNode(ctx, seam.Session{ID: "absent", TenantID: "acme"})
	nodes.FailNode(ctx, seam.Session{ID: "n1", TenantID: "acme"}, "level_timeout")
	_ = srv.WaitTerminalWork(ctx)
	got, _ := store.Get(ctx, "acme", "n1")
	if got.State != session.StateFailed {
		t.Errorf("n1 state = %q, want failed", got.State)
	}
}

// failingResolver fails every read with a transient error.
type failingResolver struct{}

func (failingResolver) GetByID(context.Context, string) (sessionstore.Session, error) {
	return sessionstore.Session{}, errors.New("store down")
}

// TestAttachCallingSessionInstallsTheAgentPrincipal checks that the core
// attaches the principal of a session's agent caller, reports an unknown
// session as ErrSessionNotFound, and returns a transient read failure.
//
// spec: GWSEAM-AttachCallingSession
func TestAttachCallingSessionInstallsTheAgentPrincipal(t *testing.T) {
	store := memstore.New()
	if err := store.Create(context.Background(), sessionstore.Session{ID: "s1", TenantID: "acme", UserID: testOwner, State: session.StateRunning}); err != nil {
		t.Fatal(err)
	}
	cs := NewCallingSessions(store)
	ctx, err := cs.AttachCallingSession(context.Background(), "s1")
	if err != nil {
		t.Fatal(err)
	}
	p, ok := authmw.FromContext(ctx)
	if !ok || p.Subject != testOwner || p.TenantID != "acme" || p.SessionID != "s1" || p.CallerType != AgentCallerType {
		t.Errorf("principal = %+v", p)
	}
	if _, err := cs.AttachCallingSession(context.Background(), "absent"); !errors.Is(err, seam.ErrSessionNotFound) {
		t.Errorf("unknown session = %v, want ErrSessionNotFound", err)
	}
	if _, err := NewCallingSessions(failingResolver{}).AttachCallingSession(context.Background(), "s1"); err == nil || errors.Is(err, seam.ErrSessionNotFound) {
		t.Errorf("transient failure = %v, want a non-not-found error", err)
	}
}
