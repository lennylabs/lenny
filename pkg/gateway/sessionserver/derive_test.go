// SPDX-License-Identifier: MIT

package sessionserver_test

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
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
)

// recordingSink captures the §7.1 derive rule 5 audit emissions so tests
// can verify the gateway emitted exactly one event with the correct
// payload.
type recordingSink struct {
	events []sessionserver.DeriveIsolationDowngradeEvent
}

func (r *recordingSink) EmitDeriveIsolationDowngrade(_ context.Context, ev sessionserver.DeriveIsolationDowngradeEvent) {
	r.events = append(r.events, ev)
}

func newSourceSession(t *testing.T, store sessionstore.Store, mods ...func(*sessionstore.Session)) sessionstore.Session {
	t.Helper()
	row := sessionstore.Session{
		ID:               "sess_source",
		TenantID:         "acme",
		UserID:           "alice@acme.com",
		State:            session.StateCompleted,
		CreatedAt:        time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		UpdatedAt:        time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		RuntimeRef:       "claude-code",
		PoolRef:          "default-pool",
		IsolationProfile: isolation.ProfileSandboxed,
		WorkspaceSnapshot: &sessionstore.WorkspaceSnapshot{
			Ref:       "/acme/workspace/sess_source/snap.tar.zst",
			Source:    sessionstore.WorkspaceSnapshotSealed,
			Timestamp: time.Date(2026, 1, 1, 0, 5, 0, 0, time.UTC),
		},
	}
	for _, m := range mods {
		m(&row)
	}
	if err := store.Create(context.Background(), row); err != nil {
		t.Fatalf("seed source session: %v", err)
	}
	return row
}

func deriveRequest(t *testing.T, h http.Handler, body sessionserver.DeriveRequest, mods ...func(*http.Request)) *httptest.ResponseRecorder {
	t.Helper()
	buf, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/v1/sessions/sess_source/derive", bytes.NewReader(buf))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	for _, m := range mods {
		m(req)
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

func decodeError(t *testing.T, rr *httptest.ResponseRecorder) (code, msg string, details map[string]any) {
	t.Helper()
	var env struct {
		Error struct {
			Code    string         `json:"code"`
			Message string         `json:"message"`
			Details map[string]any `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode error envelope: %v; body=%s", err, rr.Body.String())
	}
	return env.Error.Code, env.Error.Message, env.Error.Details
}

func TestDeriveHappyPathFromTerminal(t *testing.T) {
	store := memstore.New()
	newSourceSession(t, store)
	srv := sessionserver.New(store, sessionserver.Options{
		IDFunc: func() string { return "sess_derived" },
		Clock:  func() time.Time { return time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC) },
	})

	rr := deriveRequest(t, srv.Handler(), sessionserver.DeriveRequest{})
	if rr.Code != http.StatusCreated {
		t.Fatalf("status: got %d, want 201; body=%s", rr.Code, rr.Body.String())
	}
	var resp sessionserver.DeriveResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode derive response: %v; body=%s", err, rr.Body.String())
	}
	if resp.ID != "sess_derived" {
		t.Errorf("derived id: got %q, want sess_derived", resp.ID)
	}
	if resp.State != string(session.StateCreated) {
		t.Errorf("derived state: got %q, want created", resp.State)
	}
	if resp.RuntimeRef != "claude-code" {
		t.Errorf("derived runtimeRef: got %q, want claude-code (inherited)", resp.RuntimeRef)
	}
	if resp.WorkspaceSnapshotSource != "sealed" {
		t.Errorf("workspaceSnapshotSource: got %q, want sealed", resp.WorkspaceSnapshotSource)
	}
	if resp.ParentSessionID != "sess_source" {
		t.Errorf("parentSessionId: got %q, want sess_source", resp.ParentSessionID)
	}

	// Source session is untouched per §7.1 derive copy semantics.
	src, _ := store.Get(context.Background(), "acme", "sess_source")
	if src.State != session.StateCompleted {
		t.Errorf("source state mutated: got %q", src.State)
	}
}

func TestDeriveNonTerminalWithoutAllowStaleRejects(t *testing.T) {
	store := memstore.New()
	newSourceSession(t, store, func(s *sessionstore.Session) {
		s.State = session.StateRunning
	})
	srv := sessionserver.New(store, sessionserver.Options{})

	rr := deriveRequest(t, srv.Handler(), sessionserver.DeriveRequest{})
	if rr.Code != http.StatusConflict {
		t.Fatalf("status: got %d, want 409; body=%s", rr.Code, rr.Body.String())
	}
	code, _, _ := decodeError(t, rr)
	if code != "DERIVE_ON_LIVE_SESSION" {
		t.Errorf("error code: got %q, want DERIVE_ON_LIVE_SESSION", code)
	}
}

func TestDeriveNonTerminalWithAllowStaleSucceeds(t *testing.T) {
	store := memstore.New()
	newSourceSession(t, store, func(s *sessionstore.Session) {
		s.State = session.StateRunning
		s.WorkspaceSnapshot.Source = sessionstore.WorkspaceSnapshotCheckpoint
	})
	srv := sessionserver.New(store, sessionserver.Options{
		IDFunc: func() string { return "sess_d2" },
	})

	rr := deriveRequest(t, srv.Handler(), sessionserver.DeriveRequest{AllowStale: true})
	if rr.Code != http.StatusCreated {
		t.Fatalf("status: got %d, want 201; body=%s", rr.Code, rr.Body.String())
	}
	var resp sessionserver.DeriveResponse
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	if resp.WorkspaceSnapshotSource != "checkpoint" {
		t.Errorf("workspaceSnapshotSource: got %q, want checkpoint", resp.WorkspaceSnapshotSource)
	}
}

func TestDeriveNoSnapshotReturns400(t *testing.T) {
	store := memstore.New()
	newSourceSession(t, store, func(s *sessionstore.Session) {
		s.WorkspaceSnapshot = nil
	})
	srv := sessionserver.New(store, sessionserver.Options{})

	rr := deriveRequest(t, srv.Handler(), sessionserver.DeriveRequest{})
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d, want 400; body=%s", rr.Code, rr.Body.String())
	}
	code, _, details := decodeError(t, rr)
	if code != "VALIDATION_ERROR" {
		t.Errorf("error code: got %q, want VALIDATION_ERROR", code)
	}
	fields, _ := details["fields"].([]any)
	if len(fields) == 0 {
		t.Fatalf("expected details.fields[]; details=%v", details)
	}
	first, _ := fields[0].(map[string]any)
	if first["field"] != "sourceSessionId" {
		t.Errorf("details.fields[0].field: got %v, want sourceSessionId", first["field"])
	}
}

func TestDeriveIsolationDowngradeWithoutFlagReturns422(t *testing.T) {
	store := memstore.New()
	newSourceSession(t, store, func(s *sessionstore.Session) {
		s.IsolationProfile = isolation.ProfileMicrovm
	})
	srv := sessionserver.New(store, sessionserver.Options{})

	rr := deriveRequest(t, srv.Handler(), sessionserver.DeriveRequest{
		TargetIsolationProfile: isolation.ProfileSandboxed,
	})
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status: got %d, want 422; body=%s", rr.Code, rr.Body.String())
	}
	code, _, details := decodeError(t, rr)
	if code != "ISOLATION_MONOTONICITY_VIOLATED" {
		t.Errorf("error code: got %q, want ISOLATION_MONOTONICITY_VIOLATED", code)
	}
	if details["sourceIsolationProfile"] != "microvm" || details["targetIsolationProfile"] != "sandboxed" {
		t.Errorf("details: got %v", details)
	}
}

func TestDeriveIsolationDowngradeWithoutAdminRoleReturns403(t *testing.T) {
	store := memstore.New()
	newSourceSession(t, store, func(s *sessionstore.Session) {
		s.IsolationProfile = isolation.ProfileMicrovm
	})
	srv := sessionserver.New(store, sessionserver.Options{})

	// Attach a non-admin Principal via context.
	rr := deriveRequest(
		t, srv.Handler(),
		sessionserver.DeriveRequest{
			TargetIsolationProfile:  isolation.ProfileSandboxed,
			AllowIsolationDowngrade: true,
		},
		func(req *http.Request) {
			ctx := authmw.WithPrincipal(req.Context(), authmw.Principal{
				Subject:  "alice@acme.com",
				TenantID: "acme",
				Roles:    []pkgauth.Role{pkgauth.RoleTenantAdmin},
			})
			*req = *req.WithContext(ctx)
		},
	)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("status: got %d, want 403; body=%s", rr.Code, rr.Body.String())
	}
	code, _, _ := decodeError(t, rr)
	if code != "FORBIDDEN" {
		t.Errorf("error code: got %q, want FORBIDDEN", code)
	}
}

func TestDeriveIsolationDowngradeWithAdminEmitsAuditAndSucceeds(t *testing.T) {
	store := memstore.New()
	newSourceSession(t, store, func(s *sessionstore.Session) {
		s.IsolationProfile = isolation.ProfileMicrovm
	})
	sink := &recordingSink{}
	srv := sessionserver.New(store, sessionserver.Options{
		IDFunc:          func() string { return "sess_downgrade" },
		DeriveAuditSink: sink,
	})

	rr := deriveRequest(
		t, srv.Handler(),
		sessionserver.DeriveRequest{
			TargetIsolationProfile:  isolation.ProfileSandboxed,
			TargetPool:              "low-iso-pool",
			AllowIsolationDowngrade: true,
			TicketID:                "JIRA-123",
		},
		func(req *http.Request) {
			ctx := authmw.WithPrincipal(req.Context(), authmw.Principal{
				Subject:  "ops@acme.com",
				TenantID: "acme",
				Roles:    []pkgauth.Role{pkgauth.RolePlatformAdmin},
			})
			*req = *req.WithContext(ctx)
		},
	)
	if rr.Code != http.StatusCreated {
		t.Fatalf("status: got %d, want 201; body=%s", rr.Code, rr.Body.String())
	}
	if got, want := len(sink.events), 1; got != want {
		t.Fatalf("audit events: got %d, want %d", got, want)
	}
	ev := sink.events[0]
	if ev.SourceIsolationProfile != isolation.ProfileMicrovm ||
		ev.TargetIsolationProfile != isolation.ProfileSandboxed ||
		ev.AuthorizingUserSubject != "ops@acme.com" ||
		ev.TicketID != "JIRA-123" ||
		ev.TargetPool != "low-iso-pool" ||
		ev.TenantID != "acme" {
		t.Errorf("audit event mismatch: %+v", ev)
	}
}

func TestDeriveInvalidTargetProfileReturns400(t *testing.T) {
	store := memstore.New()
	newSourceSession(t, store)
	srv := sessionserver.New(store, sessionserver.Options{})

	rr := deriveRequest(t, srv.Handler(), sessionserver.DeriveRequest{
		TargetIsolationProfile: isolation.Profile("ferrous"),
	})
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d, want 400; body=%s", rr.Code, rr.Body.String())
	}
	code, _, _ := decodeError(t, rr)
	if code != "VALIDATION_ERROR" {
		t.Errorf("error code: got %q, want VALIDATION_ERROR", code)
	}
}

func TestDeriveSourceNotFoundReturns404(t *testing.T) {
	store := memstore.New()
	srv := sessionserver.New(store, sessionserver.Options{})

	rr := deriveRequest(t, srv.Handler(), sessionserver.DeriveRequest{})
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status: got %d, want 404; body=%s", rr.Code, rr.Body.String())
	}
}

func TestDeriveCrossTenantReturns404(t *testing.T) {
	// §4.2 tenant isolation: deriving a foreign-tenant session yields
	// 404 (existence is never leaked).
	store := memstore.New()
	newSourceSession(t, store, func(s *sessionstore.Session) { s.TenantID = "globex" })
	srv := sessionserver.New(store, sessionserver.Options{})

	rr := deriveRequest(t, srv.Handler(), sessionserver.DeriveRequest{})
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status: got %d, want 404; body=%s", rr.Code, rr.Body.String())
	}
}

func TestDeriveInheritsRuntimeAndUserAndPool(t *testing.T) {
	store := memstore.New()
	newSourceSession(t, store)
	srv := sessionserver.New(store, sessionserver.Options{
		IDFunc: func() string { return "sess_inherit" },
	})

	rr := deriveRequest(t, srv.Handler(), sessionserver.DeriveRequest{})
	if rr.Code != http.StatusCreated {
		t.Fatalf("status: got %d, want 201; body=%s", rr.Code, rr.Body.String())
	}

	derived, err := store.Get(context.Background(), "acme", "sess_inherit")
	if err != nil {
		t.Fatalf("fetch derived: %v", err)
	}
	if derived.RuntimeRef != "claude-code" {
		t.Errorf("RuntimeRef inheritance: got %q, want claude-code", derived.RuntimeRef)
	}
	if derived.UserID != "alice@acme.com" {
		t.Errorf("UserID inheritance: got %q, want alice@acme.com", derived.UserID)
	}
	if derived.PoolRef != "default-pool" {
		t.Errorf("PoolRef inheritance: got %q, want default-pool", derived.PoolRef)
	}
	if derived.ParentSessionID != "sess_source" {
		t.Errorf("ParentSessionID: got %q, want sess_source", derived.ParentSessionID)
	}
	if derived.ParentWorkspaceRef != "/acme/workspace/sess_source/snap.tar.zst" {
		t.Errorf("ParentWorkspaceRef: got %q", derived.ParentWorkspaceRef)
	}
	// The derived snapshot is rewritten to the derived session's own path.
	if derived.WorkspaceSnapshot == nil ||
		derived.WorkspaceSnapshot.Ref == "/acme/workspace/sess_source/snap.tar.zst" {
		t.Errorf("derived snapshot ref must differ from source's; got %+v", derived.WorkspaceSnapshot)
	}
}

func TestDeriveOverrideRuntime(t *testing.T) {
	store := memstore.New()
	newSourceSession(t, store)
	srv := sessionserver.New(store, sessionserver.Options{
		IDFunc: func() string { return "sess_override" },
	})
	rr := deriveRequest(t, srv.Handler(), sessionserver.DeriveRequest{
		RuntimeRef: "gemini-cli",
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("status: got %d, want 201; body=%s", rr.Code, rr.Body.String())
	}
	derived, _ := store.Get(context.Background(), "acme", "sess_override")
	if derived.RuntimeRef != "gemini-cli" {
		t.Errorf("RuntimeRef override: got %q, want gemini-cli", derived.RuntimeRef)
	}
}

func TestDeriveEqualIsolationSucceedsWithoutOverride(t *testing.T) {
	// SEC-001 is satisfied when target == source (no downgrade).
	store := memstore.New()
	newSourceSession(t, store, func(s *sessionstore.Session) {
		s.IsolationProfile = isolation.ProfileSandboxed
	})
	srv := sessionserver.New(store, sessionserver.Options{
		IDFunc: func() string { return "sess_eq" },
	})
	rr := deriveRequest(t, srv.Handler(), sessionserver.DeriveRequest{
		TargetIsolationProfile: isolation.ProfileSandboxed,
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("status: got %d, want 201; body=%s", rr.Code, rr.Body.String())
	}
}

func TestDeriveStrictlyStricterIsolationSucceeds(t *testing.T) {
	store := memstore.New()
	newSourceSession(t, store, func(s *sessionstore.Session) {
		s.IsolationProfile = isolation.ProfileSandboxed
	})
	srv := sessionserver.New(store, sessionserver.Options{
		IDFunc: func() string { return "sess_stricter" },
	})
	rr := deriveRequest(t, srv.Handler(), sessionserver.DeriveRequest{
		TargetIsolationProfile: isolation.ProfileMicrovm,
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("status: got %d, want 201; body=%s", rr.Code, rr.Body.String())
	}
}
