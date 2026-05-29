// SPDX-License-Identifier: MIT

package admin_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	pkgauth "github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/gateway/admin"
	"github.com/lennylabs/lenny/pkg/gateway/interactionstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/tenantstore"
	"github.com/lennylabs/lenny/pkg/gateway/userstore"
)

// spec: §11.4 three-tier user invalidation.

func seedUser(t *testing.T, store userstore.Store, tenant, subject string) {
	t.Helper()
	if err := store.Create(context.Background(), userstore.User{
		Subject:  subject,
		TenantID: tenant,
		Roles:    []pkgauth.Role{pkgauth.RoleUser},
	}); err != nil {
		t.Fatalf("seed user %q: %v", subject, err)
	}
}

func invalidateUser(t *testing.T, h http.Handler, subject string, body admin.InvalidateUserRequest, as func(*http.Request) *http.Request) *httptest.ResponseRecorder {
	t.Helper()
	b, _ := json.Marshal(body)
	req := as(httptest.NewRequest(http.MethodPost,
		"/v1/admin/users/"+subject+"/invalidate", bytes.NewReader(b)))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

func TestInvalidateUserSoftDisable(t *testing.T) {
	router, store, audit := newUserAdmin(t)
	seedUser(t, store, "acme", "alice@acme.com")

	rr := invalidateUser(t, router.Handler(), "alice@acme.com",
		admin.InvalidateUserRequest{TenantID: "acme", Mode: admin.InvalidateSoftDisable, Reason: "ticket-42"},
		withAdminPrincipal)
	if rr.Code != http.StatusOK {
		t.Fatalf("soft_disable: status %d, body %s", rr.Code, rr.Body.String())
	}

	got, err := store.Get(context.Background(), "acme", "alice@acme.com")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !got.Disabled {
		t.Error("soft_disable must set the disabled flag")
	}
	if !got.DeletedAt.IsZero() {
		t.Error("soft_disable must not raise the deleted_at tombstone")
	}
	if snap := audit.snapshot(); len(snap) != 1 || snap[0].Type != "admin.user.invalidated" {
		t.Errorf("audit: %+v", snap)
	}
}

func TestInvalidateUserHardDisable(t *testing.T) {
	router, store, _ := newUserAdmin(t)
	seedUser(t, store, "acme", "bob@acme.com")

	rr := invalidateUser(t, router.Handler(), "bob@acme.com",
		admin.InvalidateUserRequest{TenantID: "acme", Mode: admin.InvalidateHardDisable},
		withAdminPrincipal)
	if rr.Code != http.StatusOK {
		t.Fatalf("hard_disable: status %d, body %s", rr.Code, rr.Body.String())
	}

	got, err := store.Get(context.Background(), "acme", "bob@acme.com")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !got.Disabled || got.DeletedAt.IsZero() {
		t.Errorf("hard_disable must set disabled and the tombstone: %+v", got)
	}
}

func TestInvalidateUserFullRevoke(t *testing.T) {
	router, store, _ := newUserAdmin(t)
	seedUser(t, store, "acme", "carol@acme.com")

	rr := invalidateUser(t, router.Handler(), "carol@acme.com",
		admin.InvalidateUserRequest{TenantID: "acme", Mode: admin.InvalidateFullRevoke},
		withAdminPrincipal)
	if rr.Code != http.StatusOK {
		t.Fatalf("full_revoke: status %d, body %s", rr.Code, rr.Body.String())
	}

	got, err := store.Get(context.Background(), "acme", "carol@acme.com")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !got.Disabled || got.DeletedAt.IsZero() {
		t.Errorf("full_revoke must set disabled and the tombstone: %+v", got)
	}
}

func TestInvalidateUserRejectsUnknownMode(t *testing.T) {
	router, store, _ := newUserAdmin(t)
	seedUser(t, store, "acme", "dave@acme.com")

	rr := invalidateUser(t, router.Handler(), "dave@acme.com",
		admin.InvalidateUserRequest{TenantID: "acme", Mode: "banhammer"},
		withAdminPrincipal)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("unknown mode: status %d, want 400", rr.Code)
	}
	got, _ := store.Get(context.Background(), "acme", "dave@acme.com")
	if got.Disabled {
		t.Error("an unknown mode must not mutate the user")
	}
}

func TestInvalidateUserNotFound(t *testing.T) {
	router, _, _ := newUserAdmin(t)
	rr := invalidateUser(t, router.Handler(), "ghost@acme.com",
		admin.InvalidateUserRequest{TenantID: "acme", Mode: admin.InvalidateSoftDisable},
		withAdminPrincipal)
	if rr.Code != http.StatusNotFound {
		t.Errorf("unknown user: status %d, want 404", rr.Code)
	}
}

// spec: §11.4 full_revoke — terminate the user's active sessions.

func newUserAdminWithSessions(t *testing.T) (*admin.Router, userstore.Store, sessionstore.Store, interactionstore.Store, *recordingAudit) {
	t.Helper()
	users := userstore.NewMemory()
	sessions := memstore.New()
	interactions := interactionstore.NewMemory()
	audit := &recordingAudit{}
	router := admin.NewRouter(tenantstore.NewMemory(), admin.Options{
		Clock: func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
		Audit: audit,
	}).WithUsers(users).WithSessions(sessions).WithInteractions(interactions)
	return router, users, sessions, interactions, audit
}

func TestFullRevokeTerminatesActiveSessions(t *testing.T) {
	router, users, sessions, _, _ := newUserAdminWithSessions(t)
	seedUser(t, users, "acme", "alice@acme.com")
	seedSession(t, sessions, sessionstore.Session{
		ID: "run_1", TenantID: "acme", UserID: "alice@acme.com", State: session.StateRunning,
	})
	seedSession(t, sessions, sessionstore.Session{
		ID: "run_2", TenantID: "acme", UserID: "alice@acme.com", State: session.StateAwaitingClientAction,
	})
	seedSession(t, sessions, sessionstore.Session{
		ID: "done", TenantID: "acme", UserID: "alice@acme.com", State: session.StateCompleted,
	})

	rr := invalidateUser(t, router.Handler(), "alice@acme.com",
		admin.InvalidateUserRequest{TenantID: "acme", Mode: admin.InvalidateFullRevoke}, withAdminPrincipal)
	if rr.Code != http.StatusOK {
		t.Fatalf("full_revoke: status %d, body %s", rr.Code, rr.Body.String())
	}
	var resp map[string]any
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	if resp["sessionsTerminated"] != float64(2) {
		t.Errorf("sessionsTerminated = %v, want 2", resp["sessionsTerminated"])
	}
	for _, id := range []string{"run_1", "run_2"} {
		got, _ := sessions.Get(context.Background(), "acme", id)
		if got.State != session.StateCancelled {
			t.Errorf("session %s state = %q, want cancelled", id, got.State)
		}
	}
	done, _ := sessions.Get(context.Background(), "acme", "done")
	if done.State != session.StateCompleted {
		t.Errorf("an already-terminal session was mutated: state = %q, want completed", done.State)
	}
}

// listFilterCapturingStore wraps a sessionstore.Store and records the
// ListFilter values it is called with. It lets the user-invalidation
// test prove the §11.4 step-1 lookup is narrowed by `UserID` at the
// store boundary rather than scanned in-process.
type listFilterCapturingStore struct {
	sessionstore.Store
	listFilters []sessionstore.ListFilter
}

func (s *listFilterCapturingStore) List(ctx context.Context, tenantID string, f sessionstore.ListFilter) ([]sessionstore.Session, error) {
	s.listFilters = append(s.listFilters, f)
	return s.Store.List(ctx, tenantID, f)
}

// TestFullRevokeNarrowsListByUserID asserts the §11.4 step-1
// SessionStore lookup pushes the user filter into the store call so
// the Postgres-backed store reads `idx_sessions_tenant_user` instead
// of scanning tenant-wide. spec: §11.4 line 256.
func TestFullRevokeNarrowsListByUserID_spec_11_4_256(t *testing.T) {
	users := userstore.NewMemory()
	base := memstore.New()
	cap := &listFilterCapturingStore{Store: base}
	interactions := interactionstore.NewMemory()
	audit := &recordingAudit{}
	router := admin.NewRouter(tenantstore.NewMemory(), admin.Options{
		Clock: func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
		Audit: audit,
	}).WithUsers(users).WithSessions(cap).WithInteractions(interactions)

	seedUser(t, users, "acme", "alice@acme.com")
	seedSession(t, cap, sessionstore.Session{ID: "a1", TenantID: "acme", UserID: "alice@acme.com", State: session.StateRunning})
	seedSession(t, cap, sessionstore.Session{ID: "b1", TenantID: "acme", UserID: "bob@acme.com", State: session.StateRunning})

	rr := invalidateUser(t, router.Handler(), "alice@acme.com",
		admin.InvalidateUserRequest{TenantID: "acme", Mode: admin.InvalidateFullRevoke}, withAdminPrincipal)
	if rr.Code != http.StatusOK {
		t.Fatalf("full_revoke: status %d, body %s", rr.Code, rr.Body.String())
	}
	if len(cap.listFilters) == 0 {
		t.Fatal("session store List was never called; §11.4 step-1 lookup did not run")
	}
	for i, f := range cap.listFilters {
		if f.UserID != "alice@acme.com" {
			t.Errorf("List call %d: UserID = %q, want alice@acme.com (tenant-wide scan would be O(N))", i, f.UserID)
		}
	}
}

func TestFullRevokeLeavesOtherUsersSessions(t *testing.T) {
	router, users, sessions, _, _ := newUserAdminWithSessions(t)
	seedUser(t, users, "acme", "alice@acme.com")
	seedSession(t, sessions, sessionstore.Session{
		ID: "alice_s", TenantID: "acme", UserID: "alice@acme.com", State: session.StateRunning,
	})
	seedSession(t, sessions, sessionstore.Session{
		ID: "bob_s", TenantID: "acme", UserID: "bob@acme.com", State: session.StateRunning,
	})

	rr := invalidateUser(t, router.Handler(), "alice@acme.com",
		admin.InvalidateUserRequest{TenantID: "acme", Mode: admin.InvalidateFullRevoke}, withAdminPrincipal)
	if rr.Code != http.StatusOK {
		t.Fatalf("full_revoke: status %d", rr.Code)
	}
	bob, _ := sessions.Get(context.Background(), "acme", "bob_s")
	if bob.State != session.StateRunning {
		t.Errorf("another user's session was terminated: state = %q, want running", bob.State)
	}
}

func TestSoftDisableLeavesSessionsRunning(t *testing.T) {
	router, users, sessions, _, _ := newUserAdminWithSessions(t)
	seedUser(t, users, "acme", "carol@acme.com")
	seedSession(t, sessions, sessionstore.Session{
		ID: "carol_s", TenantID: "acme", UserID: "carol@acme.com", State: session.StateRunning,
	})

	rr := invalidateUser(t, router.Handler(), "carol@acme.com",
		admin.InvalidateUserRequest{TenantID: "acme", Mode: admin.InvalidateSoftDisable}, withAdminPrincipal)
	if rr.Code != http.StatusOK {
		t.Fatalf("soft_disable: status %d", rr.Code)
	}
	got, _ := sessions.Get(context.Background(), "acme", "carol_s")
	if got.State != session.StateRunning {
		t.Errorf("soft_disable terminated a session (state %q) — only full_revoke terminates sessions", got.State)
	}
}

func TestFullRevokeWithNoActiveSessions(t *testing.T) {
	router, users, _, _, _ := newUserAdminWithSessions(t)
	seedUser(t, users, "acme", "dave@acme.com")

	rr := invalidateUser(t, router.Handler(), "dave@acme.com",
		admin.InvalidateUserRequest{TenantID: "acme", Mode: admin.InvalidateFullRevoke}, withAdminPrincipal)
	if rr.Code != http.StatusOK {
		t.Fatalf("full_revoke with no sessions: status %d", rr.Code)
	}
	var resp map[string]any
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	if resp["sessionsTerminated"] != float64(0) {
		t.Errorf("sessionsTerminated = %v, want 0", resp["sessionsTerminated"])
	}
}

func TestFullRevokeDismissesPendingElicitations(t *testing.T) {
	router, users, _, interactions, _ := newUserAdminWithSessions(t)
	seedUser(t, users, "acme", "frank@acme.com")
	if err := interactions.Put(context.Background(), interactionstore.Interaction{
		ID: "el_1", Kind: interactionstore.KindElicitation,
		SessionID: "sess_e", TenantID: "acme", UserID: "frank@acme.com",
	}); err != nil {
		t.Fatalf("seed elicitation: %v", err)
	}

	rr := invalidateUser(t, router.Handler(), "frank@acme.com",
		admin.InvalidateUserRequest{TenantID: "acme", Mode: admin.InvalidateFullRevoke}, withAdminPrincipal)
	if rr.Code != http.StatusOK {
		t.Fatalf("full_revoke: status %d, body %s", rr.Code, rr.Body.String())
	}
	var resp map[string]any
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	if resp["elicitationsDismissed"] != float64(1) {
		t.Errorf("elicitationsDismissed = %v, want 1", resp["elicitationsDismissed"])
	}
	got, err := interactions.Get(context.Background(), "acme", "sess_e", "frank@acme.com", "el_1")
	if err != nil {
		t.Fatalf("Get elicitation: %v", err)
	}
	if got.Phase != interactionstore.PhaseDismissed {
		t.Errorf("elicitation phase = %q, want dismissed", got.Phase)
	}
}

func TestInvalidateUserTenantAdminScoped(t *testing.T) {
	router, store, _ := newUserAdmin(t)
	seedUser(t, store, "acme", "erin@acme.com")

	// A tenant-admin omits tenantId; the handler derives it from the
	// principal's tenant.
	rr := invalidateUser(t, router.Handler(), "erin@acme.com",
		admin.InvalidateUserRequest{Mode: admin.InvalidateSoftDisable},
		func(req *http.Request) *http.Request { return withTenantAdminFor(req, "acme") })
	if rr.Code != http.StatusOK {
		t.Fatalf("tenant-admin invalidate: status %d, body %s", rr.Code, rr.Body.String())
	}
	got, _ := store.Get(context.Background(), "acme", "erin@acme.com")
	if !got.Disabled {
		t.Error("a tenant-admin must be able to invalidate a user in its own tenant")
	}
}
