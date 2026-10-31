// SPDX-License-Identifier: MIT

package sessionserver_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	pkgauth "github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/gateway/environment/userstore"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
)

// ownerBindingCreatePaths are the REST create routes whose new session's
// owner the session server binds from the principal.
var ownerBindingCreatePaths = []string{
	"/v1/sessions",
	"/v1/sessions/start",
	"/v1/environments/security-team/sessions",
}

// postOwnerBindingCreate posts a create body that names bob as userId to
// path, with principal applied to the request when non-nil, and returns the
// recorder. The handler is mounted without asOwner, so the request carries
// exactly the principal the case gives it.
func postOwnerBindingCreate(t *testing.T, srv *sessionserver.Server, path string, principal *authmw.Principal) *httptest.ResponseRecorder {
	t.Helper()
	body := []byte(`{"runtimeRef":"claude-code","userId":"bob@acme.com"}`)
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	if principal != nil {
		req = req.WithContext(authmw.WithPrincipal(req.Context(), *principal))
	}
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	return rr
}

// assertNoRows fails the test when the store holds any session in acme.
func assertNoRows(t *testing.T, store *memstore.Store) {
	t.Helper()
	rows, err := store.List(context.Background(), "acme", sessionstore.ListFilter{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("a refused create wrote %d session rows", len(rows))
	}
}

// TestCreateBindsOwnerFromPrincipalNotBody_spec_10_2 pins that the owner of
// a session created through REST create, create-and-start, and the
// environment sessions route is the creating principal's subject. A body
// userId naming another user is ignored, so alice cannot create a session
// owned by bob.
//
// spec: 10.2 (Session ownership), 15.1 (REST API)
func TestCreateBindsOwnerFromPrincipalNotBody_spec_10_2(t *testing.T) {
	for _, path := range ownerBindingCreatePaths {
		t.Run(path, func(t *testing.T) {
			store := memstore.New()
			srv := sessionserver.New(store, sessionserver.Options{
				DevTenantHeader: true,
				IDFunc:          func() string { return "sess_owned" },
			})
			rr := postOwnerBindingCreate(t, srv, path, &authmw.Principal{Subject: "alice@acme.com", TenantID: "acme"})
			if rr.Code != http.StatusCreated {
				t.Fatalf("create: status %d, body %s", rr.Code, rr.Body.String())
			}
			row, err := store.Get(context.Background(), "acme", "sess_owned")
			if err != nil {
				t.Fatalf("get: %v", err)
			}
			if row.UserID != "alice@acme.com" {
				t.Errorf("stored owner = %q, want the principal alice@acme.com; the body userId set the owner", row.UserID)
			}
		})
	}
}

// TestCreateWithoutPrincipalSubjectIsUnauthorized_spec_10_2 pins that a
// create whose principal is absent or has an empty Subject is refused with
// 401 UNAUTHORIZED and writes no row, on every REST create route and in a
// dev-mode server that resolves the tenant from the dev header.
//
// spec: 10.2 (Session ownership), 15.1 (UNAUTHORIZED)
func TestCreateWithoutPrincipalSubjectIsUnauthorized_spec_10_2(t *testing.T) {
	cases := []struct {
		name      string
		principal *authmw.Principal
	}{
		{"no principal", nil},
		{"empty subject", &authmw.Principal{TenantID: "acme"}},
	}
	for _, path := range ownerBindingCreatePaths {
		for _, tc := range cases {
			t.Run(path+"/"+tc.name, func(t *testing.T) {
				store := memstore.New()
				srv := sessionserver.New(store, sessionserver.Options{DevTenantHeader: true})
				rr := postOwnerBindingCreate(t, srv, path, tc.principal)
				if rr.Code != http.StatusUnauthorized {
					t.Fatalf("status = %d, want 401; body %s", rr.Code, rr.Body.String())
				}
				if code, _, _ := decodeError(t, rr); code != "UNAUTHORIZED" {
					t.Errorf("code = %s, want UNAUTHORIZED", code)
				}
				assertNoRows(t, store)
			})
		}
	}
}

// TestCreateServicesRequirePrincipalSubject_spec_10_2 pins that the
// in-process create services refuse a call whose context carries a service
// tenant and no principal subject, and bind the owner from the principal
// when one is present. A service tenant resolves the tenant only.
//
// spec: 10.2 (Session ownership), 15.2.1 (rule 1)
func TestCreateServicesRequirePrincipalSubject_spec_10_2(t *testing.T) {
	type createFn func(*sessionserver.Server, context.Context, sessionserver.CreateSessionRequest) (sessionserver.CreateSessionResponse, *sessionserver.ServiceError)
	services := map[string]createFn{
		"CreateSessionService": func(s *sessionserver.Server, ctx context.Context, req sessionserver.CreateSessionRequest) (sessionserver.CreateSessionResponse, *sessionserver.ServiceError) {
			return s.CreateSessionService(ctx, "acme", req)
		},
		"CreateAndStartService": func(s *sessionserver.Server, ctx context.Context, req sessionserver.CreateSessionRequest) (sessionserver.CreateSessionResponse, *sessionserver.ServiceError) {
			return s.CreateAndStartService(ctx, "acme", req)
		},
	}
	for name, create := range services {
		t.Run(name+"/no principal", func(t *testing.T) {
			store := memstore.New()
			srv := sessionserver.New(store, sessionserver.Options{DevTenantHeader: true})
			_, svcErr := create(srv, context.Background(), sessionserver.CreateSessionRequest{RuntimeRef: "claude-code"})
			if svcErr == nil || svcErr.HTTPStatus != http.StatusUnauthorized || svcErr.Code != "UNAUTHORIZED" {
				t.Fatalf("service error = %+v, want 401 UNAUTHORIZED", svcErr)
			}
			assertNoRows(t, store)
		})
		t.Run(name+"/principal", func(t *testing.T) {
			store := memstore.New()
			srv := sessionserver.New(store, sessionserver.Options{DevTenantHeader: true})
			ctx := authmw.WithPrincipal(context.Background(), authmw.Principal{Subject: "carol@acme.com"})
			resp, svcErr := create(srv, ctx, sessionserver.CreateSessionRequest{RuntimeRef: "claude-code"})
			if svcErr != nil {
				t.Fatalf("service error: %+v", svcErr)
			}
			if resp.TenantID != "acme" || resp.UserID != "carol@acme.com" {
				t.Errorf("tenant/owner = %q/%q, want acme/carol@acme.com", resp.TenantID, resp.UserID)
			}
		})
	}
}

// TestDeriveTakesSourceOwnerNotBody_spec_10_2 pins that a derived session
// takes its source session's owner. A derive body naming another user as
// userId is ignored.
//
// spec: 10.2 (Session ownership), 7.1 (derive)
func TestDeriveTakesSourceOwnerNotBody_spec_10_2(t *testing.T) {
	f := deriveFixture(t)
	body, _ := json.Marshal(map[string]string{"userId": "bob@acme.com"})
	req := httptest.NewRequest(http.MethodPost, "/v1/sessions/sess_source/derive", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	asPrincipal("alice@acme.com")(req)
	rr := httptest.NewRecorder()
	f.deriveServer(f.blobs).ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("derive: status %d, body %s", rr.Code, rr.Body.String())
	}
	row, err := f.mem.Get(context.Background(), "acme", "sess_derived")
	if err != nil {
		t.Fatalf("get derived: %v", err)
	}
	if row.UserID != "alice@acme.com" {
		t.Errorf("derived owner = %q, want the source owner alice@acme.com", row.UserID)
	}
}

// TestCreateWithEmptyDevUserHeaderIsUnauthorized_spec_10_2 pins that a
// create through the auth middleware's dev-header transport whose
// X-Lenny-User-ID header is present but empty has no principal subject, so
// it receives 401 UNAUTHORIZED and writes no row rather than a session
// owned by the body's userId or by nobody.
//
// spec: §10.2 (Session ownership) rule 3; §15.1 (UNAUTHORIZED)
func TestCreateWithEmptyDevUserHeaderIsUnauthorized_spec_10_2(t *testing.T) {
	for _, path := range ownerBindingCreatePaths {
		t.Run(path, func(t *testing.T) {
			store := memstore.New()
			h := authmw.Wrap(sessionserver.New(store, sessionserver.Options{DevTenantHeader: true}).Handler(),
				authmw.Options{AllowDevHeaders: true, DevHeaderNamesTenant: true})
			req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader([]byte(`{"runtimeRef":"claude-code","userId":"bob@acme.com"}`)))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-Lenny-Tenant-ID", "acme")
			req.Header.Set("X-Lenny-User-ID", "")
			rr := httptest.NewRecorder()
			h.ServeHTTP(rr, req)
			if rr.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401; body %s", rr.Code, rr.Body.String())
			}
			assertNoRows(t, store)
		})
	}
}

// TestCreateConcurrencyKeyIsTheOwner_spec_10_2 pins that the per-user
// concurrent-session cap counts the principal's sessions. alice holds the
// cap, so her create naming bob as userId is refused with the user scope,
// and bob's create naming alice is admitted and owned by bob. Before the
// owner came from the principal, the body's userId chose the key.
//
// spec: §10.2 (Session ownership) rule 1; §11.1 (per-user concurrency)
func TestCreateConcurrencyKeyIsTheOwner_spec_10_2(t *testing.T) {
	newSrv := func() (*sessionserver.Server, *memstore.Store) {
		store := memstore.New()
		for _, row := range runningRows("acme", "alice@acme.com", "claude-code", 2) {
			if err := store.Create(context.Background(), row); err != nil {
				t.Fatalf("seed: %v", err)
			}
		}
		return sessionserver.New(store, sessionserver.Options{
			DevTenantHeader: true, MaxConcurrentSessionsPerUser: 2,
			IDFunc: func() string { return "sess_new" },
		}), store
	}
	srv, _ := newSrv()
	rr := postOwnerBindingCreate(t, srv, "/v1/sessions", &authmw.Principal{Subject: "alice@acme.com", TenantID: "acme"})
	if rr.Code != http.StatusTooManyRequests || !bytes.Contains(rr.Body.Bytes(), []byte(`"scope":"user"`)) {
		t.Fatalf("saturated alice naming bob = %d %s, want 429 with the user scope", rr.Code, rr.Body.String())
	}
	srv, store := newSrv()
	body := []byte(`{"runtimeRef":"claude-code","userId":"alice@acme.com"}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/sessions", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	req = req.WithContext(authmw.WithPrincipal(req.Context(), authmw.Principal{Subject: "bob@acme.com", TenantID: "acme"}))
	rr = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("bob naming saturated alice = %d %s, want 201", rr.Code, rr.Body.String())
	}
	if row, err := store.Get(context.Background(), "acme", "sess_new"); err != nil || row.UserID != "bob@acme.com" {
		t.Errorf("stored owner = %q (err %v), want bob@acme.com", row.UserID, err)
	}
}

// TestCreateFenceReadsThePrincipal_spec_12_8 pins that the §12.8 creation
// fence reads the principal's markers. An erasure-pending alice whose body
// names an active bob as userId is refused with ERASURE_IN_PROGRESS naming
// alice, and no row is written. Before the owner came from the principal,
// the fence read bob's markers and admitted a session outside alice's
// erasure.
//
// spec: §12.8 (Lifecycle fence and drain); §10.2 (Session ownership) rule 1
func TestCreateFenceReadsThePrincipal_spec_12_8(t *testing.T) {
	for _, path := range ownerBindingCreatePaths[:2] {
		t.Run(path, func(t *testing.T) {
			f := newFenceFixture(t,
				userstore.User{Subject: "alice@acme.com", TenantID: "acme"},
				userstore.User{Subject: "bob@acme.com", TenantID: "acme"})
			f.restrict(t, "alice@acme.com", "job-9")
			srv := sessionserver.New(f.store, sessionserver.Options{DevTenantHeader: true, Tenants: f.tenants, Users: f.users})
			rr := postOwnerBindingCreate(t, srv, path, &authmw.Principal{Subject: "alice@acme.com", TenantID: "acme"})
			if rr.Code != http.StatusForbidden {
				t.Fatalf("status = %d, want 403; body %s", rr.Code, rr.Body.String())
			}
			if code, _, details := decodeError(t, rr); code != "ERASURE_IN_PROGRESS" || details["userId"] != "alice@acme.com" {
				t.Fatalf("code %s details %v, want ERASURE_IN_PROGRESS naming alice", code, details)
			}
			assertNoRows(t, f.mem)
		})
	}
}

// TestCustomRoleCallerCreatesOwnSession_spec_10_2 pins that a principal
// whose custom role grants the tenant read bypass alongside the session
// permissions still creates its own session, owned by its subject.
//
// spec: §10.2 (Session ownership) rules 1 and 4; §10.2 (Custom roles
// (tenant-scoped))
func TestCustomRoleCallerCreatesOwnSession_spec_10_2(t *testing.T) {
	store := memstore.New()
	srv := sessionserver.New(store, sessionserver.Options{
		DevTenantHeader: true, CustomRoles: accessRoles(t), MultiTenant: true,
		IDFunc: func() string { return "sess_bob_new" },
	})
	rr := postOwnerBindingCreate(t, srv, "/v1/sessions", principalOf(accessBob, pkgauth.Role(sessionReader)))
	if rr.Code != http.StatusCreated {
		t.Fatalf("custom-role create = %d %s, want 201", rr.Code, rr.Body.String())
	}
	if row, err := store.Get(context.Background(), "acme", "sess_bob_new"); err != nil || row.UserID != accessBob {
		t.Errorf("stored owner = %q (err %v), want %s", row.UserID, err, accessBob)
	}
}

// TestReplayTakesSourceOwnerNotBody_spec_10_2 pins that a replayed session
// takes its source session's owner. A replay body naming another user as
// userId is ignored.
//
// spec: §10.2 (Session ownership) rule 2; §15.1 (replay)
func TestReplayTakesSourceOwnerNotBody_spec_10_2(t *testing.T) {
	f, _, h := replayFixture(t)
	req := httptest.NewRequest(http.MethodPost, "/v1/sessions/sess_source/replay",
		bytes.NewReader([]byte(`{"targetRuntime":"x","userId":"bob@acme.com"}`)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	asPrincipal("alice@acme.com")(req)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("replay: status %d, body %s", rr.Code, rr.Body.String())
	}
	row, err := f.mem.Get(context.Background(), "acme", "sess_replay")
	if err != nil {
		t.Fatalf("get replayed: %v", err)
	}
	if row.UserID != "alice@acme.com" {
		t.Errorf("replayed owner = %q, want the source owner alice@acme.com", row.UserID)
	}
}

// TestCreateNoSubjectPrecedesBodyValidation_spec_10_2 pins that every REST
// create route refuses a request with no principal subject with 401 whatever
// its body holds, so a malformed body does not turn the refusal into a 400.
//
// spec: 10.2 (Session ownership) rule 3, 15.1 (UNAUTHORIZED)
func TestCreateNoSubjectPrecedesBodyValidation_spec_10_2(t *testing.T) {
	for _, path := range ownerBindingCreatePaths {
		for _, body := range []string{`{not json`, `{}`, ``} {
			t.Run(path+"/"+body, func(t *testing.T) {
				store := memstore.New()
				srv := sessionserver.New(store, sessionserver.Options{DevTenantHeader: true})
				req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader([]byte(body)))
				req.Header.Set("Content-Type", "application/json")
				req.Header.Set("X-Lenny-Tenant-ID", "acme")
				rr := httptest.NewRecorder()
				srv.Handler().ServeHTTP(rr, req)
				if rr.Code != http.StatusUnauthorized {
					t.Fatalf("status = %d, want 401; body %s", rr.Code, rr.Body.String())
				}
				if code, _, _ := decodeError(t, rr); code != "UNAUTHORIZED" {
					t.Errorf("code = %s, want UNAUTHORIZED", code)
				}
				assertNoRows(t, store)
			})
		}
	}
}
