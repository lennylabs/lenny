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

	"github.com/lennylabs/lenny/pkg/adapter"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	"github.com/lennylabs/lenny/pkg/gateway/tenantaccessstore"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
)

// poolSelectErrorEnvelope decodes the §15.1 error envelope a rejected
// create body returns, so the pool-selection matrix can assert the code
// and category without the internal decodeErrorBody helper.
type poolSelectErrorEnvelope struct {
	Error struct {
		Code      string         `json:"code"`
		Category  string         `json:"category"`
		Retryable bool           `json:"retryable"`
		Details   map[string]any `json:"details"`
	} `json:"error"`
}

// poolSelectTenantAccess returns a §4 pool_tenant_access store granting
// tenant acme access to each named pool. spec: §4 (pool_tenant_access).
func poolSelectTenantAccess(t *testing.T, pools ...string) tenantaccessstore.Store {
	t.Helper()
	store := tenantaccessstore.NewMemory()
	for _, p := range pools {
		if _, err := store.Grant(context.Background(), tenantaccessstore.KindPool, p, "acme", "platform-admin@acme.com", time.Time{}); err != nil {
			t.Fatalf("grant pool %s to acme: %v", p, err)
		}
	}
	return store
}

// spec: §7.1 (pool selector), §14.1 (CreateSessionRequest.pool), §4 (pool_tenant_access)
// diagnosis: the create-body pool selector is dropped from scheduling. A
// failure here means a client-pinned, backed, and authorized pool did not
// constrain the claim to that pool, so F-CS2's accept-echo-ignore
// behavior is back: the session scheduled on a different pool (or the
// disambiguation picked the wrong one) despite the explicit pin.
func TestCreateBodyPoolHonorsBackedAuthorizedPin_spec_7_1(t *testing.T) {
	rt := &podBindRuntime{}
	adapterSrv := adapter.New("adapter-test")
	adapterSrv.WorkspaceRoot = t.TempDir()
	adapterSrv.Runtime = rt

	// Two pools both back echo / sandboxed: without a pin the resolution is
	// ambiguous, so a successful schedule on the pinned pool proves the pin
	// (not the disambiguation) selected it. Each pool carries one idle pod.
	cluster := podBindClient(
		t,
		podBindWarmPool("echo-pool-a", "echo-tmpl-a"),
		podBindTemplate("echo-tmpl-a", "echo", string(isolation.ProfileSandboxed)),
		podBindIdleSandbox("sbx-a", "echo-pool-a", "10.244.2.5"),
		podBindWarmPool("echo-pool-b", "echo-tmpl-b"),
		podBindTemplate("echo-tmpl-b", "echo", string(isolation.ProfileSandboxed)),
		podBindIdleSandbox("sbx-b", "echo-pool-b", "10.244.2.6"),
	)
	binder := podBindBinder(cluster, podBindAdapterDialer(t, adapterSrv))
	registry := podsession.NewRegistry()

	srv := sessionserver.New(memstore.New(), sessionserver.Options{
		IDFunc:                  func() string { return "sess-pin-ok" },
		DefaultIsolationProfile: isolation.ProfileSandboxed,
		PodBinder:               binder,
		PodRegistry:             registry,
		AgentNamespace:          podTestNS,
		TenantAccess:            poolSelectTenantAccess(t, "echo-pool-b"),
	})

	body, _ := json.Marshal(sessionserver.CreateAndStartRequest{
		RuntimeRef: "echo", UserID: "alice@acme.com", Pool: "echo-pool-b",
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/sessions/start", bytes.NewReader(body))
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 for a backed+authorized pin; body=%s", rr.Code, rr.Body.String())
	}
	binding, ok := registry.Get("sess-pin-ok")
	if !ok {
		t.Fatal("registry holds no binding for the pinned-pool session")
	}
	// The session must land on the pinned pool's pod, not the sibling pool.
	if binding.SandboxName != "sbx-b" || binding.PodIP != "10.244.2.6" {
		t.Errorf("binding = %+v, want sbx-b / 10.244.2.6 (the pinned echo-pool-b)", binding)
	}
}

// spec: §7.1 (pool selector, line 18 / line 75), §14.1 (CreateSessionRequest.pool)
// diagnosis: a pinned pool whose profile differs from the deployment
// default is rejected when the client omits isolationProfile. A failure
// here means the gate passed the defaulted profile (the deployment
// DefaultIsolationProfile) into ResolvePool's strict-equality isolation
// filter, so a client that pinned a pool and deferred to its profile is
// wrongly rejected 400 pool_not_satisfiable. C6a requires the named pool's
// own profile to govern when the client requested none: the pool here is
// microvm while the default is sandboxed, with isolationProfile omitted, so
// it must schedule.
func TestCreateBodyPoolHonorsPinnedProfileWhenIsolationOmitted_spec_7_1(t *testing.T) {
	rt := &podBindRuntime{}
	adapterSrv := adapter.New("adapter-test")
	adapterSrv.WorkspaceRoot = t.TempDir()
	adapterSrv.Runtime = rt

	// The pinned pool runs the microvm profile, which differs from the
	// deployment DefaultIsolationProfile (sandboxed) wired below. The client
	// omits isolationProfile, deferring to the pool, so the gate must let the
	// pool's own profile govern rather than reject on the defaulted profile.
	cluster := podBindClient(
		t,
		podBindWarmPool("microvm-pool", "microvm-tmpl"),
		podBindTemplate("microvm-tmpl", "echo", string(isolation.ProfileMicrovm)),
		podBindIdleSandbox("sbx-mv", "microvm-pool", "10.244.2.7"),
	)
	binder := podBindBinder(cluster, podBindAdapterDialer(t, adapterSrv))
	registry := podsession.NewRegistry()

	srv := sessionserver.New(memstore.New(), sessionserver.Options{
		IDFunc:                  func() string { return "sess-pin-defer" },
		DefaultIsolationProfile: isolation.ProfileSandboxed,
		PodBinder:               binder,
		PodRegistry:             registry,
		AgentNamespace:          podTestNS,
		TenantAccess:            poolSelectTenantAccess(t, "microvm-pool"),
	})

	// No IsolationProfile in the body: the client defers to the pinned pool.
	body, _ := json.Marshal(sessionserver.CreateAndStartRequest{
		RuntimeRef: "echo", UserID: "alice@acme.com", Pool: "microvm-pool",
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/sessions/start", bytes.NewReader(body))
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 when a pinned pool's profile differs from the default and isolationProfile is omitted; body=%s", rr.Code, rr.Body.String())
	}
	if _, ok := registry.Get("sess-pin-defer"); !ok {
		t.Fatal("registry holds no binding for the deferred-profile pinned-pool session")
	}
}

// spec: §7.1 (pool selector), §14.1 (CreateSessionRequest.pool)
// diagnosis: an unsatisfiable create-body pool pin is not rejected
// fail-closed. A failure here means an absent, not-backed, or
// isolation-inconsistent pin scheduled on some other pool (or returned the
// wrong status), instead of the deterministic 400 VALIDATION_ERROR F-CS2
// requires. The cases share one envtest cluster.
func TestCreateBodyPoolRejectsUnsatisfiablePin_spec_14_1(t *testing.T) {
	rt := &podBindRuntime{}
	adapterSrv := adapter.New("adapter-test")
	adapterSrv.WorkspaceRoot = t.TempDir()
	adapterSrv.Runtime = rt

	cluster := podBindClient(
		t,
		// echo-pool backs echo / sandboxed.
		podBindWarmPool("echo-pool", "echo-tmpl"),
		podBindTemplate("echo-tmpl", "echo", string(isolation.ProfileSandboxed)),
		podBindIdleSandbox("sbx-1", "echo-pool", "10.244.2.5"),
		// other-pool backs a different runtime at a stricter isolation.
		podBindWarmPool("other-pool", "other-tmpl"),
		podBindTemplate("other-tmpl", "other-runtime", string(isolation.ProfileMicrovm)),
		podBindIdleSandbox("sbx-2", "other-pool", "10.244.2.6"),
	)
	binder := podBindBinder(cluster, podBindAdapterDialer(t, adapterSrv))

	srv := sessionserver.New(memstore.New(), sessionserver.Options{
		IDFunc:                  func() string { return "sess-pin-bad" },
		DefaultIsolationProfile: isolation.ProfileSandboxed,
		PodBinder:               binder,
		PodRegistry:             podsession.NewRegistry(),
		AgentNamespace:          podTestNS,
		// Grant every pool so the rejection is on the satisfiability check,
		// not the authorization check.
		TenantAccess: poolSelectTenantAccess(t, "echo-pool", "other-pool", "no-such-pool"),
	})

	cases := []struct {
		name             string
		runtimeRef       string
		isolationProfile isolation.Profile
		pool             string
	}{
		{"absent pool", "echo", isolation.ProfileSandboxed, "no-such-pool"},
		{"pool not backed by the runtime", "echo", isolation.ProfileSandboxed, "other-pool"},
		{"pool isolation-inconsistent", "other-runtime", isolation.ProfileSandboxed, "other-pool"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body, _ := json.Marshal(sessionserver.CreateAndStartRequest{
				RuntimeRef: tc.runtimeRef, UserID: "alice@acme.com",
				IsolationProfile: tc.isolationProfile, Pool: tc.pool,
			})
			req := httptest.NewRequest(http.MethodPost, "/v1/sessions/start", bytes.NewReader(body))
			req.Header.Set("X-Lenny-Tenant-ID", "acme")
			rr := httptest.NewRecorder()
			srv.Handler().ServeHTTP(rr, req)

			if rr.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400 for an unsatisfiable pin; body=%s", rr.Code, rr.Body.String())
			}
			var env poolSelectErrorEnvelope
			if err := json.Unmarshal(rr.Body.Bytes(), &env); err != nil {
				t.Fatalf("decode error envelope: %v (body=%s)", err, rr.Body.String())
			}
			if env.Error.Code != "VALIDATION_ERROR" {
				t.Errorf("error code = %q, want VALIDATION_ERROR", env.Error.Code)
			}
			if env.Error.Details["reason"] != "pool_not_satisfiable" {
				t.Errorf("details.reason = %v, want pool_not_satisfiable", env.Error.Details["reason"])
			}
		})
	}
}

// spec: §7.1 (pool selector), §4 (pool_tenant_access), §15.1 (FORBIDDEN)
// diagnosis: the §4 pool_tenant_access grant is not enforced on a
// create-body pool pin. A failure here means a tenant pinned a real,
// backed pool it was never granted and the session was admitted, so a
// client can schedule onto any platform-global pool by naming it. The pin
// must fail closed with 403 FORBIDDEN. The pool is satisfiable (backed by
// the runtime + isolation), so the rejection is the authorization gate,
// not the satisfiability gate.
func TestCreateBodyPoolRejectsUnauthorizedPin_spec_7_1(t *testing.T) {
	rt := &podBindRuntime{}
	adapterSrv := adapter.New("adapter-test")
	adapterSrv.WorkspaceRoot = t.TempDir()
	adapterSrv.Runtime = rt

	cluster := podBindClient(
		t,
		podBindWarmPool("echo-pool", "echo-tmpl"),
		podBindTemplate("echo-tmpl", "echo", string(isolation.ProfileSandboxed)),
		podBindIdleSandbox("sbx-1", "echo-pool", "10.244.2.5"),
	)
	binder := podBindBinder(cluster, podBindAdapterDialer(t, adapterSrv))

	srv := sessionserver.New(memstore.New(), sessionserver.Options{
		IDFunc:                  func() string { return "sess-pin-403" },
		DefaultIsolationProfile: isolation.ProfileSandboxed,
		PodBinder:               binder,
		PodRegistry:             podsession.NewRegistry(),
		AgentNamespace:          podTestNS,
		// A tenant-access store with a grant to a *different* pool, so acme
		// has no grant for echo-pool: the pin is satisfiable but unauthorized.
		TenantAccess: poolSelectTenantAccess(t, "some-other-pool"),
	})

	body, _ := json.Marshal(sessionserver.CreateAndStartRequest{
		RuntimeRef: "echo", UserID: "alice@acme.com", Pool: "echo-pool",
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/sessions/start", bytes.NewReader(body))
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 for an unauthorized pin; body=%s", rr.Code, rr.Body.String())
	}
	var env poolSelectErrorEnvelope
	if err := json.Unmarshal(rr.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode error envelope: %v (body=%s)", err, rr.Body.String())
	}
	if env.Error.Code != "FORBIDDEN" {
		t.Errorf("error code = %q, want FORBIDDEN", env.Error.Code)
	}
	if env.Error.Details["reason"] != "pool_access_denied" {
		t.Errorf("details.reason = %v, want pool_access_denied", env.Error.Details["reason"])
	}
}
