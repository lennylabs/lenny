// SPDX-License-Identifier: MIT

package sessionserver_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegationtree/treearchive"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	"github.com/lennylabs/lenny/pkg/gateway/seam"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
)

// streamSeamRow is the session the stream seam tests seed: owned by bob in
// acme, delegated from a parent under a root, with a pool and events.
func streamSeamRow() sessionstore.Session {
	now := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	return sessionstore.Session{
		ID: "sess-stream", TenantID: "acme", UserID: "bob@acme.com", State: session.StateRunning,
		RuntimeRef: "rt", PoolRef: "pool-a", ParentSessionID: "sess-parent", RootSessionID: "sess-root",
		LastSeq: 7, CreatedAt: now, UpdatedAt: now,
	}
}

func seedStreamSeamSession(t *testing.T) sessionstore.Store {
	t.Helper()
	store := memstore.New()
	if err := store.Create(context.Background(), streamSeamRow()); err != nil {
		t.Fatal(err)
	}
	return store
}

// failingGetStore is a session store whose Get fails with a transient
// error, so a test reads how the stream seam reports a store failure.
type failingGetStore struct {
	sessionstore.Store
	err error
}

func (f failingGetStore) Get(context.Context, string, string) (sessionstore.Session, error) {
	return sessionstore.Session{}, f.err
}

// TestStreamSeamLookupCarriesTheSessionRecordAndReportsAMissingSession
// checks that the core returns the session record fields the stream proxy
// reads, a missing session or another tenant's as ErrSessionNotFound, and
// any other store failure as it came.
//
// spec: GWSEAM-LookupSession
func TestStreamSeamLookupCarriesTheSessionRecordAndReportsAMissingSession(t *testing.T) {
	core := sessionserver.New(seedStreamSeamSession(t), sessionserver.Options{}).StreamSeam()
	got, err := core.LookupSession(context.Background(), "acme", "sess-stream")
	if err != nil {
		t.Fatal(err)
	}
	want := seam.Session{
		ID: "sess-stream", TenantID: "acme", UserID: "bob@acme.com", ParentSessionID: "sess-parent",
		RootSessionID: "sess-root", RuntimeRef: "rt", Pool: "pool-a", State: "running", LastSeq: 7,
	}
	if got != want {
		t.Errorf("record = %+v, want %+v", got, want)
	}
	if _, err := core.LookupSession(context.Background(), "acme", "sess-none"); !errors.Is(err, seam.ErrSessionNotFound) {
		t.Errorf("missing session: %v, want ErrSessionNotFound", err)
	}
	if _, err := core.LookupSession(context.Background(), "globex", "sess-stream"); !errors.Is(err, seam.ErrSessionNotFound) {
		t.Errorf("another tenant's session: %v, want ErrSessionNotFound", err)
	}

	outage := errors.New("postgres unavailable")
	broken := sessionserver.New(failingGetStore{Store: memstore.New(), err: outage}, sessionserver.Options{}).StreamSeam()
	if _, err := broken.LookupSession(context.Background(), "acme", "sess-stream"); !errors.Is(err, outage) || errors.Is(err, seam.ErrSessionNotFound) {
		t.Errorf("store failure: %v, want the store's error and not ErrSessionNotFound", err)
	}
}

// TestStreamSeamLookupCarriesNoNegativeSequence checks that a stored
// sequence number below zero, which no write produces, crosses the seam as
// zero, so the stream proxy never reads it as a session that produced
// events.
//
// spec: GWSEAM-LookupSession
func TestStreamSeamLookupCarriesNoNegativeSequence(t *testing.T) {
	store := memstore.New()
	row := streamSeamRow()
	row.LastSeq = -3
	if err := store.Create(context.Background(), row); err != nil {
		t.Fatal(err)
	}
	got, err := sessionserver.New(store, sessionserver.Options{}).StreamSeam().LookupSession(context.Background(), "acme", "sess-stream")
	if err != nil {
		t.Fatal(err)
	}
	if got.LastSeq != 0 {
		t.Errorf("LastSeq = %d, want 0", got.LastSeq)
	}
}

// TestStreamSeamAuthorizeAppliesThePermissionGateAndTheOwnershipRule checks
// the order and the outcomes of the authorization operation: the permission
// gate refuses with ErrForbidden, a caller with no subject is
// ErrUnauthenticated, and a session the caller may not use answers as a
// missing one.
//
// spec: GWSEAM-AuthorizeSession; §10.2 (Session ownership) rules 3, 5, 6,
// and 7.
func TestStreamSeamAuthorizeAppliesThePermissionGateAndTheOwnershipRule(t *testing.T) {
	core := sessionserver.New(seedStreamSeamSession(t), sessionserver.Options{MultiTenant: true}).StreamSeam()
	as := func(subject string, roles ...auth.Role) context.Context {
		return authmw.WithPrincipal(context.Background(), authmw.Principal{Subject: subject, TenantID: "acme", Roles: roles})
	}
	cases := []struct {
		name    string
		ctx     context.Context
		id      string
		action  seam.SessionAction
		wantErr error
	}{
		{"no principal", context.Background(), "sess-stream", seam.SessionRead, seam.ErrUnauthenticated},
		{"principal without a subject", as("", auth.RoleUser), "sess-stream", seam.SessionRead, seam.ErrUnauthenticated},
		{"role without the manage permission", as("bob@acme.com", auth.RoleTenantViewer), "sess-stream", seam.SessionManage, seam.ErrForbidden},
		{"unknown action", as("bob@acme.com", auth.RoleUser), "sess-stream", seam.SessionAction(99), seam.ErrForbidden},
		{"missing session", as("bob@acme.com", auth.RoleUser), "sess-none", seam.SessionRead, seam.ErrSessionNotFound},
		{"another user's session", as("alice@acme.com", auth.RoleUser), "sess-stream", seam.SessionManage, seam.ErrSessionNotFound},
		{"owner manages", as("bob@acme.com", auth.RoleUser), "sess-stream", seam.SessionManage, nil},
		{"tenant reader reads", as("carol@acme.com", auth.RoleTenantViewer), "sess-stream", seam.SessionRead, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := core.AuthorizeSession(tc.ctx, "acme", tc.id, tc.action)
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("err = %v, want %v", err, tc.wantErr)
				}
				return
			}
			if err != nil || got.ID != "sess-stream" {
				t.Fatalf("got (%+v, %v), want the session record", got, err)
			}
		})
	}
}

// TestStreamSeamRequestTenantResolvesTheTenantAsTheSessionRoutesDo checks
// that the request identity operation returns the principal's tenant, under
// the dev header posture the header's tenant, and outside it no tenant.
//
// spec: GWSEAM-RequestTenant
func TestStreamSeamRequestTenantResolvesTheTenantAsTheSessionRoutesDo(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/v1/x", nil)
	req = req.WithContext(authmw.WithPrincipal(req.Context(), authmw.Principal{Subject: "bob@acme.com", TenantID: "acme"}))
	if got := sessionserver.New(memstore.New(), sessionserver.Options{}).StreamSeam().RequestTenant(req); got != "acme" {
		t.Errorf("principal tenant = %q, want acme", got)
	}
	dev := httptest.NewRequest(http.MethodGet, "/v1/x", nil)
	dev.Header.Set("X-Lenny-Tenant-ID", "globex")
	if got := sessionserver.New(memstore.New(), sessionserver.Options{DevTenantHeader: true}).StreamSeam().RequestTenant(dev); got != "globex" {
		t.Errorf("dev header tenant = %q, want globex", got)
	}
	if got := sessionserver.New(memstore.New(), sessionserver.Options{}).StreamSeam().RequestTenant(dev); got != "" {
		t.Errorf("dev header outside dev mode = %q, want empty", got)
	}
}

// TestStreamSeamResumedFrameIsTheCoordinatorHandoffResume checks that the
// resumed frame the core serves names the coordinator handoff resume mode,
// an intact workspace, and full recovery.
//
// spec: GWSEAM-ResumedFrame
func TestStreamSeamResumedFrameIsTheCoordinatorHandoffResume(t *testing.T) {
	frame, ok := sessionserver.New(memstore.New(), sessionserver.Options{}).StreamSeam().ResumedFrame()
	if !ok {
		t.Fatal("ResumedFrame reported no frame")
	}
	var got struct {
		ResumeMode                string   `json:"resumeMode"`
		WorkspaceLost             bool     `json:"workspaceLost"`
		WorkspaceRecoveryFraction *float64 `json:"workspaceRecoveryFraction"`
	}
	if err := json.Unmarshal(frame, &got); err != nil {
		t.Fatalf("decode %s: %v", frame, err)
	}
	if got.ResumeMode != "coordinator_handoff" || got.WorkspaceLost || got.WorkspaceRecoveryFraction == nil || *got.WorkspaceRecoveryFraction != 1 {
		t.Errorf("frame = %s, want coordinator_handoff with an intact, fully recovered workspace", frame)
	}
}

// TestStreamSeamChildrenReattachedFrameListsMissedAndActiveChildren checks
// that the core serves no frame for a session with no children, and for a
// parent a frame that lists a child whose completion the reader missed and
// an active child.
//
// spec: GWSEAM-ChildrenReattachedFrame
func TestStreamSeamChildrenReattachedFrameListsMissedAndActiveChildren(t *testing.T) {
	ctx := context.Background()
	store := memstore.New()
	now := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	for _, row := range []sessionstore.Session{
		{ID: "sess-parent", TenantID: "acme", UserID: "bob@acme.com", State: session.StateRunning, LastSeq: 9, CreatedAt: now, UpdatedAt: now},
		{ID: "sess-active", TenantID: "acme", UserID: "bob@acme.com", ParentSessionID: "sess-parent", RootSessionID: "sess-parent", State: session.StateRunning, CreatedAt: now, UpdatedAt: now},
		{ID: "sess-done", TenantID: "acme", UserID: "bob@acme.com", ParentSessionID: "sess-parent", RootSessionID: "sess-parent", State: session.StateCompleted, CreatedAt: now, UpdatedAt: now},
		{ID: "sess-lonely", TenantID: "acme", UserID: "bob@acme.com", State: session.StateRunning, CreatedAt: now, UpdatedAt: now},
	} {
		if err := store.Create(ctx, row); err != nil {
			t.Fatal(err)
		}
	}
	archive := treearchive.NewMemory()
	if err := archive.Archive(ctx, treearchive.ArchivedNode{
		TenantID: "acme", RootSessionID: "sess-parent", NodeSessionID: "sess-done", ParentSessionID: "sess-parent",
		State: "completed", Result: `{"state":"completed"}`, CompletionSeq: 8, SettledAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	core := sessionserver.New(store, sessionserver.Options{TreeArchive: archive}).StreamSeam()

	if frame, ok := core.ChildrenReattachedFrame(ctx, "acme", "sess-lonely", 0); ok {
		t.Errorf("a session with no children got frame %s", frame)
	}
	frame, ok := core.ChildrenReattachedFrame(ctx, "acme", "sess-parent", 5)
	if !ok {
		t.Fatal("a parent with a missed completion and an active child got no frame")
	}
	var got struct {
		Children []struct {
			SessionID string `json:"session_id"`
		} `json:"children"`
	}
	if err := json.Unmarshal(frame, &got); err != nil {
		t.Fatalf("decode %s: %v", frame, err)
	}
	ids := map[string]bool{}
	for _, c := range got.Children {
		ids[c.SessionID] = true
	}
	if !ids["sess-done"] || !ids["sess-active"] {
		t.Errorf("children = %s, want sess-done and sess-active", frame)
	}
}
