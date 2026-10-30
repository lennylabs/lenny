//go:build contract

// SPDX-License-Identifier: MIT

// Tier-3 contract test for the §11.4 full_revoke response on the admin REST
// wire (POST /v1/admin/users/{userId}/invalidate). A partial propagation
// answers 200 with the per-effect counts, a failedSessions array of
// {sessionId, error} objects for each session whose cancel write failed, and
// elicitationDismissalError when the dismissal failed. A failure to list the
// user's sessions answers the cataloged INTERNAL_ERROR envelope with the same
// counts in details. The test drives the production admin router over
// httptest and asserts the decoded wire bytes, so a renamed or re-typed field
// surfaces here.
//
// spec: §11.4 (User Invalidation: Full revoke propagation mechanism), §15.1
// (REST API), §25.2
package admin_user_invalidate_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	pkgauth "github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
	"github.com/lennylabs/lenny/pkg/gateway/environment/userstore"
	"github.com/lennylabs/lenny/pkg/gateway/externalapi/admin"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	"github.com/lennylabs/lenny/pkg/gateway/session/interactionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
)

// faultySessions fails the cancel write of failID, or every listing when
// listErr is set.
type faultySessions struct {
	sessionstore.Store
	failID  string
	listErr error
}

func (s *faultySessions) List(ctx context.Context, tenantID string, f sessionstore.ListFilter) ([]sessionstore.Session, error) {
	if s.listErr != nil {
		return nil, s.listErr
	}
	return s.Store.List(ctx, tenantID, f)
}

func (s *faultySessions) Update(ctx context.Context, tenantID, id string, mut func(*sessionstore.Session) error) (sessionstore.Session, error) {
	if id == s.failID {
		return sessionstore.Session{}, errors.New("pgtenant: commit: connection reset")
	}
	return s.Store.Update(ctx, tenantID, id, mut)
}

// failingDismissal fails DismissByUser.
type failingDismissal struct{ interactionstore.Store }

func (failingDismissal) DismissByUser(context.Context, string, string) (int, error) {
	return 0, errors.New("interactions: connection refused")
}

func invalidate(t *testing.T, sessions sessionstore.Store, interactions interactionstore.Store) (int, map[string]json.RawMessage) {
	t.Helper()
	users := userstore.NewMemory()
	if err := users.Create(context.Background(), userstore.User{Subject: "alice@acme.com", TenantID: "acme", Roles: []pkgauth.Role{pkgauth.RoleUser}}); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	h := admin.NewRouter(tenantstore.NewMemory(), admin.Options{
		Clock: func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
	}).WithUsers(users).WithSessions(sessions).WithInteractions(interactions).Handler()
	body, _ := json.Marshal(admin.InvalidateUserRequest{TenantID: "acme", Mode: admin.InvalidateFullRevoke})
	req := httptest.NewRequest(http.MethodPost, "/v1/admin/users/alice@acme.com/invalidate", bytes.NewReader(body))
	req = req.WithContext(authmw.WithPrincipal(req.Context(), authmw.Principal{
		Subject: "admin@acme.com", TenantID: "platform", Roles: []pkgauth.Role{pkgauth.RolePlatformAdmin},
	}))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	var out map[string]json.RawMessage
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode %q: %v", rr.Body.String(), err)
	}
	return rr.Code, out
}

func seeded(t *testing.T, ids ...string) *memstore.Store {
	t.Helper()
	s := memstore.New()
	for _, id := range ids {
		if err := s.Create(context.Background(), sessionstore.Session{ID: id, TenantID: "acme", UserID: "alice@acme.com", State: session.StateRunning}); err != nil {
			t.Fatalf("seed %s: %v", id, err)
		}
	}
	return s
}

// requireCounts asserts every per-effect count field is a JSON number.
func requireCounts(t *testing.T, obj map[string]json.RawMessage) {
	t.Helper()
	for _, k := range []string{
		"sessionsTerminated", "podsTerminated", "leasesRevoked",
		"tokensRevoked", "playgroundSessionsRevoked", "elicitationsDismissed",
	} {
		var n float64
		if err := json.Unmarshal(obj[k], &n); err != nil {
			t.Errorf("%s = %s, want a JSON number", k, obj[k])
		}
	}
}

// spec: §11.4 (User Invalidation: Full revoke propagation mechanism), §15.1 (REST API)
// diagnosis: the full_revoke 200 body no longer reports a failed cancel write as
// failedSessions [{sessionId, error}] or a failed dismissal as
// elicitationDismissalError, or it lost a per-effect count field.
func TestFullRevokePartialFailureResponseFields_spec_11_4(t *testing.T) {
	code, body := invalidate(t, &faultySessions{Store: seeded(t, "s_a", "s_b"), failID: "s_b"}, failingDismissal{interactionstore.NewMemory()})
	if code != http.StatusOK {
		t.Fatalf("status %d, want 200", code)
	}
	requireCounts(t, body)
	var failed []map[string]string
	if err := json.Unmarshal(body["failedSessions"], &failed); err != nil {
		t.Fatalf("failedSessions = %s, want an array of objects: %v", body["failedSessions"], err)
	}
	if len(failed) != 1 || failed[0]["sessionId"] != "s_b" || failed[0]["error"] == "" || len(failed[0]) != 2 {
		t.Errorf("failedSessions = %v, want one {sessionId: s_b, error}", failed)
	}
	var dismissal string
	if err := json.Unmarshal(body["elicitationDismissalError"], &dismissal); err != nil || dismissal != "interactions: connection refused" {
		t.Errorf("elicitationDismissalError = %s, want the dismissal error string", body["elicitationDismissalError"])
	}
	var terminated float64
	_ = json.Unmarshal(body["sessionsTerminated"], &terminated)
	if terminated != 1 {
		t.Errorf("sessionsTerminated = %v, want 1", terminated)
	}
}

// spec: §11.4 (User Invalidation: Full revoke propagation mechanism), §25.2
// diagnosis: a full_revoke whose session listing failed no longer answers the
// cataloged 500 INTERNAL_ERROR envelope with the per-effect counts and
// sessionListingError in details.
func TestFullRevokeListingErrorEnvelope_spec_11_4(t *testing.T) {
	code, body := invalidate(t, &faultySessions{Store: seeded(t), listErr: errors.New("pgtenant: read: connection reset")}, interactionstore.NewMemory())
	if code != http.StatusInternalServerError {
		t.Fatalf("status %d, want 500", code)
	}
	var env struct {
		Code    string                     `json:"code"`
		Details map[string]json.RawMessage `json:"details"`
	}
	if err := json.Unmarshal(body["error"], &env); err != nil {
		t.Fatalf("error envelope %s: %v", body["error"], err)
	}
	if env.Code != "INTERNAL_ERROR" {
		t.Errorf("code = %q, want INTERNAL_ERROR", env.Code)
	}
	requireCounts(t, env.Details)
	var listing string
	if err := json.Unmarshal(env.Details["sessionListingError"], &listing); err != nil || listing == "" {
		t.Errorf("details.sessionListingError = %s, want the listing error string", env.Details["sessionListingError"])
	}
}
