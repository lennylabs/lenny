// SPDX-License-Identifier: MIT

package sessionserver_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
	pkgauth "github.com/lennylabs/lenny/pkg/auth"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
)

// targetPoolRoute is derive or one replay mode, called with a targetPool
// and optional request mutators.
type targetPoolRoute struct {
	name     string
	sourceID string
	seed     func(t *testing.T, store sessionstore.Store, mods ...func(*sessionstore.Session))
	call     func(t *testing.T, h http.Handler, pool string, profile isolation.Profile, mods ...func(*http.Request)) *httptest.ResponseRecorder
}

// targetPoolRoutes returns derive and both replay modes. Each source is an
// `echo`-runtime session at the sandboxed profile.
func targetPoolRoutes() []targetPoolRoute {
	replay := func(mode sessionserver.ReplayMode) func(*testing.T, http.Handler, string, isolation.Profile, ...func(*http.Request)) *httptest.ResponseRecorder {
		return func(t *testing.T, h http.Handler, pool string, profile isolation.Profile, mods ...func(*http.Request)) *httptest.ResponseRecorder {
			return replayReqWith(t, h, sessionserver.ReplayRequest{
				ReplayMode: mode, TargetRuntime: "echo", TargetPool: pool,
				TargetIsolationProfile: profile, AllowIsolationDowngrade: profile != "",
			}, mods...)
		}
	}
	seedReplay := func(t *testing.T, store sessionstore.Store, mods ...func(*sessionstore.Session)) {
		seedTerminalSource(t, store, mods...)
	}
	seedDerive := func(t *testing.T, store sessionstore.Store, mods ...func(*sessionstore.Session)) {
		newSourceSession(t, store, mods...)
	}
	return []targetPoolRoute{
		{
			name: "derive", sourceID: "sess_source", seed: seedDerive,
			call: func(t *testing.T, h http.Handler, pool string, profile isolation.Profile, mods ...func(*http.Request)) *httptest.ResponseRecorder {
				return deriveRequest(t, h, sessionserver.DeriveRequest{
					TargetPool: pool, TargetIsolationProfile: profile, AllowIsolationDowngrade: profile != "",
				}, mods...)
			},
		},
		{name: "replay workspace_derive", sourceID: "sess_src", seed: seedReplay, call: replay(sessionserver.ReplayWorkspaceDerive)},
		{name: "replay prompt_history", sourceID: "sess_src", seed: seedReplay, call: replay(sessionserver.ReplayPromptHistory)},
	}
}

// replayReqWith is replayReq with request mutators, for a principal.
func replayReqWith(t *testing.T, h http.Handler, body sessionserver.ReplayRequest, mods ...func(*http.Request)) *httptest.ResponseRecorder {
	t.Helper()
	if len(mods) == 0 {
		return replayReq(t, h, body)
	}
	wrapped := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for _, m := range mods {
			m(r)
		}
		h.ServeHTTP(w, r)
	})
	return replayReq(t, wrapped, body)
}

// asPlatformAdmin attaches a platform-admin principal of tenant acme, so a
// downgrade request passes the role check and reaches the audit sink.
func asPlatformAdmin(req *http.Request) {
	*req = *req.WithContext(authmw.WithPrincipal(req.Context(), authmw.Principal{
		Subject: "carol@acme.com", TenantID: "acme", Roles: []pkgauth.Role{pkgauth.RolePlatformAdmin},
	}))
}

// targetPoolCluster is a fake cluster holding echo-pool (echo, sandboxed),
// micro-pool (echo, microvm), other-pool (other-runtime, sandboxed), and
// standard-pool (echo, standard). Pool resolution reads pools and templates
// with List and Get only, so the fake client serves the selection check.
func targetPoolCluster(t *testing.T) client.Client {
	t.Helper()
	s := runtime.NewScheme()
	if err := lennyv1.AddToScheme(s); err != nil {
		t.Fatalf("AddToScheme: %v", err)
	}
	return fake.NewClientBuilder().WithScheme(s).WithObjects(
		podBindWarmPool("echo-pool", "echo-tmpl"),
		podBindTemplate("echo-tmpl", "echo", string(isolation.ProfileSandboxed)),
		podBindWarmPool("micro-pool", "micro-tmpl"),
		podBindTemplate("micro-tmpl", "echo", string(isolation.ProfileMicrovm)),
		podBindWarmPool("other-pool", "other-tmpl"),
		podBindTemplate("other-tmpl", "other-runtime", string(isolation.ProfileSandboxed)),
		podBindWarmPool("standard-pool", "standard-tmpl"),
		podBindTemplate("standard-tmpl", "echo", string(isolation.ProfileStandard)),
	).Build()
}

// TestDeriveAndReplayTargetPoolSelection_spec_15_1 pins that derive and both
// replay modes validate a named targetPool with the selection check
// POST /v1/sessions runs on a create-body pool. An absent pool, a pool
// backed by another runtime, and a pool whose profile differs from the
// target profile answer 400 VALIDATION_ERROR rather than the retryable 503
// the claim would answer. A real pool the tenant holds no
// pool_tenant_access grant on answers 403 FORBIDDEN. A server with no pool
// resolver refuses any pin, as create does. Every rejection persists no
// row and emits no downgrade audit event.
//
// spec: 15.1 (REST API) Session Replay Semantics (targetPool "Must be a pool
// backed by targetRuntime"), 7.1 (Normal Flow) derive rule 2, 14.1
// (CreateSessionRequest.pool), 4 (pool_tenant_access).
func TestDeriveAndReplayTargetPoolSelection_spec_15_1(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		pool    string
		profile isolation.Profile
		granted []string
		binder  bool
		status  int
		code    string
		reason  string
	}{
		{name: "absent pool", pool: "no-such-pool", granted: []string{"no-such-pool"}, binder: true, status: http.StatusBadRequest, code: "VALIDATION_ERROR", reason: "pool_not_satisfiable"},
		{name: "pool backed by another runtime", pool: "other-pool", granted: []string{"other-pool"}, binder: true, status: http.StatusBadRequest, code: "VALIDATION_ERROR", reason: "pool_not_satisfiable"},
		{name: "pool profile differs from the inherited target profile", pool: "micro-pool", granted: []string{"micro-pool"}, binder: true, status: http.StatusBadRequest, code: "VALIDATION_ERROR", reason: "pool_not_satisfiable"},
		{name: "pool profile differs from a downgrade target profile", pool: "echo-pool", profile: isolation.ProfileStandard, granted: []string{"echo-pool"}, binder: true, status: http.StatusBadRequest, code: "VALIDATION_ERROR", reason: "pool_not_satisfiable"},
		{name: "ungranted pool", pool: "echo-pool", granted: []string{"micro-pool"}, binder: true, status: http.StatusForbidden, code: "FORBIDDEN", reason: "pool_access_denied"},
		{name: "ungranted pool on a downgrade", pool: "standard-pool", profile: isolation.ProfileStandard, binder: true, status: http.StatusForbidden, code: "FORBIDDEN", reason: "pool_access_denied"},
		{name: "no pool resolver", pool: "echo-pool", granted: []string{"echo-pool"}, binder: false, status: http.StatusBadRequest, code: "VALIDATION_ERROR", reason: "pool_selection_unavailable"},
	}
	for _, rt := range targetPoolRoutes() {
		for _, tc := range cases {
			t.Run(rt.name+"/"+tc.name, func(t *testing.T) {
				t.Parallel()
				store := memstore.New()
				rt.seed(t, store, func(s *sessionstore.Session) { s.RuntimeRef = "echo" })
				sink := &recordingSink{}
				opts := sessionserver.Options{
					DevTenantHeader:         true,
					IDFunc:                  func() string { return "sess_new" },
					DefaultIsolationProfile: isolation.ProfileSandboxed,
					DeriveAuditSink:         sink,
					TenantAccess:            poolSelectTenantAccess(t, tc.granted...),
					AgentNamespace:          podTestNS,
				}
				if tc.binder {
					opts.PodBinder = &podsession.Binder{Client: targetPoolCluster(t), Namespace: podTestNS}
					opts.PodRegistry = podsession.NewRegistry()
				}
				srv := sessionserver.New(store, snapshotFor(t, rt.sourceID).wire(opts))
				rr := rt.call(t, asOwner(srv.Handler()), tc.pool, tc.profile, asPlatformAdmin)
				if rr.Code != tc.status {
					t.Fatalf("status = %d, want %d; body=%s", rr.Code, tc.status, rr.Body.String())
				}
				code, _, details := decodeError(t, rr)
				if code != tc.code || details["reason"] != tc.reason {
					t.Errorf("error = %s %v, want %s reason %s", code, details, tc.code, tc.reason)
				}
				if details["field"] != "targetPool" || details["pool"] != tc.pool {
					t.Errorf("details field/pool = %v/%v, want targetPool/%s", details["field"], details["pool"], tc.pool)
				}
				if _, err := store.Get(context.Background(), "acme", "sess_new"); err == nil {
					t.Error("a row was persisted although the targetPool was rejected")
				}
				if len(sink.events) != 0 {
					t.Errorf("downgrade audit events = %d, want 0 for a rejected targetPool", len(sink.events))
				}
			})
		}
	}
}

// TestDeriveAndReplayGrantedTargetPoolClaimsOnIt_spec_15_1 pins the
// accepting side of the targetPool selection check: a granted pool backed by
// the runtime passes, and the new session claims its pod on that pool even
// when a second pool also serves the runtime and profile.
//
// spec: 15.1 (REST API) Session Replay Semantics, 7.1 (Normal Flow) derive
// rule 2, 4 (pool_tenant_access).
func TestDeriveAndReplayGrantedTargetPoolClaimsOnIt_spec_15_1(t *testing.T) {
	t.Parallel()
	for _, rt := range newSessionRoutes() {
		t.Run(rt.name, func(t *testing.T) {
			t.Parallel()
			store := memstore.New()
			rt.seed(t, store)
			rt.call = func(t *testing.T, h http.Handler) *httptest.ResponseRecorder {
				if rt.name == "derive" {
					return deriveRequest(t, h, sessionserver.DeriveRequest{TargetPool: "echo-pool-b"})
				}
				mode := sessionserver.ReplayWorkspaceDerive
				if rt.name == "replay prompt_history" {
					mode = sessionserver.ReplayPromptHistory
				}
				return replayReq(t, h, sessionserver.ReplayRequest{ReplayMode: mode, TargetRuntime: "echo", TargetPool: "echo-pool-b"})
			}
			h, _, _ := claimingServerWith(
				t, store, rt,
				func(o *sessionserver.Options) { o.TenantAccess = poolSelectTenantAccess(t, "echo-pool-b") },
				podBindWarmPool("echo-pool", "echo-tmpl"),
				podBindTemplate("echo-tmpl", "echo", string(isolation.ProfileSandboxed)),
				podBindIdleSandbox("sbx-a", "echo-pool", "10.244.2.5"),
				podBindWarmPool("echo-pool-b", "echo-tmpl-b"),
				podBindTemplate("echo-tmpl-b", "echo", string(isolation.ProfileSandboxed)),
				podBindIdleSandbox("sbx-b", "echo-pool-b", "10.244.2.6"),
			)
			rr := rt.call(t, h)
			if rr.Code != http.StatusCreated {
				t.Fatalf("status = %d, want 201; body=%s", rr.Code, rr.Body.String())
			}
			row, err := store.Get(context.Background(), "acme", rt.newID)
			if err != nil {
				t.Fatalf("get new row: %v", err)
			}
			if row.Pool != "echo-pool-b" || row.PoolRef != "echo-pool-b" || row.PodAssignment != "sbx-b" {
				t.Errorf("row pool/poolRef/pod = %q/%q/%q, want echo-pool-b/echo-pool-b/sbx-b", row.Pool, row.PoolRef, row.PodAssignment)
			}
		})
	}
}
