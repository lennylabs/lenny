//go:build contract

// SPDX-License-Identifier: MIT

// Tier-3 contract test for the §15.1 tenant suspend response on the admin
// REST wire. The suspend body reports drainedSessions and failedSessions,
// and failedSessions is an array on every 200: empty when the drain brought
// every session to a terminal state, and the unfinished session IDs when it
// did not. A drain that cannot list the tenant's sessions answers the
// cataloged INTERNAL_ERROR envelope with the drained count in details. The
// test drives the production admin router (router, tenant store, drain, and
// JSON encoder) over httptest and asserts the raw bytes, so a payload that
// encodes failedSessions as null or omits it surfaces here.
//
// spec: §15.1 (REST API), §12.8 (Lifecycle fence and drain), §25.2
package admin_tenant_suspend_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	pkgauth "github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
	"github.com/lennylabs/lenny/pkg/gateway/externalapi/admin"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
)

// listing answers the drain's first and verifying listings from fixed
// rows, or fails the first listing when err is set.
type listing struct {
	sessionstore.Store
	first, verify []sessionstore.Session
	err           error
	calls         int
}

func (l *listing) List(context.Context, string, sessionstore.ListFilter) ([]sessionstore.Session, error) {
	l.calls++
	if l.err != nil {
		return nil, l.err
	}
	if l.calls == 1 {
		return l.first, nil
	}
	return l.verify, nil
}

// terminator fails the IDs in failing and transitions every other ID.
type terminator struct{ failing map[string]bool }

func (terminator) GetByID(context.Context, string) (sessionstore.Session, error) {
	return sessionstore.Session{}, sessionstore.ErrNotFound
}

func (t terminator) ForceTerminate(_ context.Context, tenantID, id string) (sessionstore.Session, session.State, bool, error) {
	if t.failing[id] {
		return sessionstore.Session{}, "", false, errors.New("pod release refused")
	}
	return sessionstore.Session{ID: id, TenantID: tenantID, State: session.StateFailed}, session.StateRunning, true, nil
}

func live(id string) sessionstore.Session {
	return sessionstore.Session{ID: id, TenantID: "acme", State: session.StateRunning}
}

func suspend(t *testing.T, l *listing, term terminator) *httptest.ResponseRecorder {
	t.Helper()
	tenants := tenantstore.NewMemory()
	if err := tenants.Create(context.Background(), tenantstore.Tenant{ID: "acme", DisplayName: "acme"}); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	h := admin.NewRouter(tenants, admin.Options{
		Clock: func() time.Time { return time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC) },
	}).WithSessions(l).WithSessionAdmin(term).Handler()
	req := httptest.NewRequest(http.MethodPost, "/v1/admin/tenants/acme/suspend", strings.NewReader(`{"reason":"abuse"}`))
	req = req.WithContext(authmw.WithPrincipal(req.Context(), authmw.Principal{
		TenantID: "platform", Subject: "alice@acme.com", Roles: []pkgauth.Role{pkgauth.RolePlatformAdmin},
	}))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

// spec: §15.1 (REST API), §12.8 (Lifecycle fence and drain)
// diagnosis: a failure means the suspend response no longer matches the
// §15.1 wire contract: failedSessions is null or missing on a complete
// drain, an incomplete drain is not reported as 200 with the unfinished
// IDs, or a listing failure does not answer the INTERNAL_ERROR envelope
// with details.drainedSessions.
func TestTenantSuspendResponseContract_spec_15_1(t *testing.T) {
	t.Run("complete drain encodes failedSessions as an empty array", func(t *testing.T) {
		rr := suspend(t, &listing{first: []sessionstore.Session{live("s1"), live("s2")}}, terminator{})
		if rr.Code != http.StatusOK {
			t.Fatalf("status %d, body %s", rr.Code, rr.Body.String())
		}
		var raw map[string]json.RawMessage
		if err := json.Unmarshal(rr.Body.Bytes(), &raw); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if got := string(raw["failedSessions"]); got != "[]" {
			t.Errorf("failedSessions = %q, want []", got)
		}
		if got := string(raw["drainedSessions"]); got != "2" {
			t.Errorf("drainedSessions = %q, want 2", got)
		}
		if got := string(raw["suspended"]); got != "true" {
			t.Errorf("suspended = %q, want true", got)
		}
	})

	t.Run("incomplete drain answers 200 with the unfinished IDs", func(t *testing.T) {
		rr := suspend(t, &listing{
			first:  []sessionstore.Session{live("s_ok"), live("s_bad")},
			verify: []sessionstore.Session{live("s_bad"), live("s_new")},
		}, terminator{failing: map[string]bool{"s_bad": true}})
		if rr.Code != http.StatusOK {
			t.Fatalf("status %d, body %s", rr.Code, rr.Body.String())
		}
		var out struct {
			DrainedSessions int      `json:"drainedSessions"`
			FailedSessions  []string `json:"failedSessions"`
		}
		if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if out.DrainedSessions != 1 || strings.Join(out.FailedSessions, ",") != "s_bad,s_new" {
			t.Errorf("body = %+v, want drainedSessions 1 and failedSessions [s_bad s_new]", out)
		}
	})

	t.Run("listing failure answers the INTERNAL_ERROR envelope", func(t *testing.T) {
		rr := suspend(t, &listing{err: errors.New("primary unreachable")}, terminator{})
		if rr.Code != http.StatusInternalServerError {
			t.Fatalf("status %d, body %s", rr.Code, rr.Body.String())
		}
		var env struct {
			Error struct {
				Code      string         `json:"code"`
				Category  string         `json:"category"`
				Retryable bool           `json:"retryable"`
				Details   map[string]any `json:"details"`
			} `json:"error"`
		}
		if err := json.Unmarshal(rr.Body.Bytes(), &env); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if env.Error.Code != "INTERNAL_ERROR" {
			t.Errorf("code = %q, want INTERNAL_ERROR", env.Error.Code)
		}
		if env.Error.Category != "TRANSIENT" || !env.Error.Retryable {
			t.Errorf("category/retryable = %q/%v, want TRANSIENT/true", env.Error.Category, env.Error.Retryable)
		}
		if got, ok := env.Error.Details["drainedSessions"]; !ok || got != float64(0) {
			t.Errorf("details.drainedSessions = %v (present %v), want 0", got, ok)
		}
	})
}
