// SPDX-License-Identifier: MIT

package sessionserver_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore/memstore"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	"github.com/lennylabs/lenny/pkg/gateway/userstore"
)

// spec: §11.4 — a soft-disabled, hard-disabled, or fully-revoked user
// is denied new sessions.

// gateWithUsers builds a session server whose §11.4 user gate is
// backed by the supplied users.
func gateWithUsers(t *testing.T, seed ...userstore.User) http.Handler {
	t.Helper()
	us := userstore.NewMemory()
	for _, u := range seed {
		if err := us.Create(context.Background(), u); err != nil {
			t.Fatalf("seed user %q: %v", u.Subject, err)
		}
	}
	return sessionserver.New(memstore.New(), sessionserver.Options{Users: us}).Handler()
}

// asUser attaches an authenticated principal to a request.
func asUser(req *http.Request, subject, tenant string) *http.Request {
	req.Header.Set("X-Lenny-Tenant-ID", tenant)
	return req.WithContext(authmw.WithPrincipal(req.Context(),
		authmw.Principal{Subject: subject, TenantID: tenant}))
}

func postSession(t *testing.T, h http.Handler, path, subject, tenant string) *httptest.ResponseRecorder {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"runtimeRef": "claude-code"})
	req := asUser(httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body)), subject, tenant)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

func TestCreateDeniedForSoftDisabledUser(t *testing.T) {
	h := gateWithUsers(t, userstore.User{
		Subject: "alice@acme.com", TenantID: "acme", Disabled: true,
	})
	rr := postSession(t, h, "/v1/sessions", "alice@acme.com", "acme")
	if rr.Code != http.StatusForbidden {
		t.Fatalf("soft-disabled user: status %d, want 403; body %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "USER_INVALIDATED") {
		t.Errorf("denial should carry USER_INVALIDATED: %s", rr.Body.String())
	}
}

func TestCreateDeniedForRevokedUser(t *testing.T) {
	// A hard_disable / full_revoke raises the deleted_at tombstone.
	revoked := userstore.User{Subject: "bob@acme.com", TenantID: "acme"}
	us := userstore.NewMemory()
	if err := us.Create(context.Background(), revoked); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := us.SoftDelete(context.Background(), "acme", "bob@acme.com", time.Now()); err != nil {
		t.Fatalf("soft-delete: %v", err)
	}
	h := sessionserver.New(memstore.New(), sessionserver.Options{Users: us}).Handler()
	rr := postSession(t, h, "/v1/sessions", "bob@acme.com", "acme")
	if rr.Code != http.StatusForbidden {
		t.Fatalf("revoked user: status %d, want 403; body %s", rr.Code, rr.Body.String())
	}
}

func TestCreateAllowedForActiveUser(t *testing.T) {
	h := gateWithUsers(t, userstore.User{Subject: "carol@acme.com", TenantID: "acme"})
	rr := postSession(t, h, "/v1/sessions", "carol@acme.com", "acme")
	if rr.Code != http.StatusCreated {
		t.Fatalf("active user: status %d, want 201; body %s", rr.Code, rr.Body.String())
	}
}

func TestCreateAllowedForUnregisteredPrincipal(t *testing.T) {
	// §11.4 governs invalidation of known users, not registry
	// membership: a principal with no user row still creates sessions.
	h := gateWithUsers(t)
	rr := postSession(t, h, "/v1/sessions", "dave@acme.com", "acme")
	if rr.Code != http.StatusCreated {
		t.Fatalf("unregistered principal: status %d, want 201; body %s", rr.Code, rr.Body.String())
	}
}

func TestCreateAndStartDeniedForDisabledUser(t *testing.T) {
	h := gateWithUsers(t, userstore.User{
		Subject: "erin@acme.com", TenantID: "acme", Disabled: true,
	})
	rr := postSession(t, h, "/v1/sessions/start", "erin@acme.com", "acme")
	if rr.Code != http.StatusForbidden {
		t.Fatalf("/v1/sessions/start with a disabled user: status %d, want 403; body %s",
			rr.Code, rr.Body.String())
	}
}
