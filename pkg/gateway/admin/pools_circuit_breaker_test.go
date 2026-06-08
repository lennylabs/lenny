// SPDX-License-Identifier: MIT

package admin_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/admin"
	"github.com/lennylabs/lenny/pkg/gateway/poolstore"
	"github.com/lennylabs/lenny/pkg/gateway/runtimestore"
)

// seedSDKWarmPool registers a preConnect (SDK-warm) runtime and a pool
// referencing it, returning the pool's current ETag.
func seedSDKWarmPool(t *testing.T, router *admin.Router, runtimes *runtimestore.Memory, pools *poolstore.Memory, name string, preConnect bool) string {
	t.Helper()
	rt := runtimestore.Runtime{Name: "claude-code"}
	if preConnect {
		rt.Capabilities = &runtimestore.RuntimeCapabilities{PreConnect: true}
		rt.SDKWarmBlockingPaths = []string{"CLAUDE.md"}
	}
	if err := runtimes.Create(context.Background(), rt); err != nil {
		t.Fatalf("seed runtime: %v", err)
	}
	if err := pools.Create(context.Background(), poolstore.Pool{
		Name:                 name,
		RuntimeRef:           "claude-code",
		IsolationProfile:     "sandboxed",
		ExecutionMode:        runtimestore.ExecutionModeSession,
		ResourceClass:        "small",
		WarmCount:            2,
		MaxSessionAgeSeconds: 3600,
	}); err != nil {
		t.Fatalf("seed pool: %v", err)
	}
	g := withAdminPrincipal(httptest.NewRequest(http.MethodGet, "/v1/admin/pools/"+name, nil))
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, g)
	return rr.Header().Get("ETag")
}

func putCircuitBreaker(t *testing.T, router *admin.Router, name, etag string, body any) *httptest.ResponseRecorder {
	t.Helper()
	b, _ := json.Marshal(body)
	req := withAdminPrincipal(httptest.NewRequest(http.MethodPut, "/v1/admin/pools/"+name+"/circuit-breaker", bytes.NewReader(b)))
	if etag != "" {
		req.Header.Set("If-Match", etag)
	}
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, req)
	return rr
}

// TestCircuitBreakerOverrideEnabled covers the §15.1 line 801 happy path:
// an `enabled` override on an SDK-warm pool persists and emits the
// pool.sdk_warm_circuit_breaker_override audit event with previous and new
// values.
func TestCircuitBreakerOverrideEnabled_spec_15_1(t *testing.T) {
	router, pools, runtimes, audit := newPoolAdmin(t)
	etag := seedSDKWarmPool(t, router, runtimes, pools, "sdk-pool", true)

	rr := putCircuitBreaker(t, router, "sdk-pool", etag, admin.CircuitBreakerOverrideRequest{
		SDKWarm: &admin.SDKWarmOverridePayload{CircuitBreakerOverride: "enabled"},
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rr.Code, rr.Body.String())
	}
	row, _ := pools.Get(context.Background(), "sdk-pool")
	if row.SDKWarmCircuitBreakerOverride != poolstore.SDKWarmOverrideEnabled {
		t.Errorf("stored override = %q, want enabled", row.SDKWarmCircuitBreakerOverride)
	}
	events := audit.snapshot()
	var found bool
	for _, e := range events {
		if e.Type == "pool.sdk_warm_circuit_breaker_override" {
			found = true
			if e.Detail["newOverride"] != "enabled" {
				t.Errorf("newOverride detail = %v, want enabled", e.Detail["newOverride"])
			}
			if e.Detail["previousOverride"] != "auto" {
				t.Errorf("previousOverride detail = %v, want auto (unset maps to auto)", e.Detail["previousOverride"])
			}
		}
	}
	if !found {
		t.Errorf("expected pool.sdk_warm_circuit_breaker_override audit event, got %+v", events)
	}
}

// TestCircuitBreakerOverrideRejectsNonSDKWarmPool covers the §15.1 line
// 801 rule: the override has no effect on a pool whose runtime is not
// SDK-warm, returning 409 INVALID_STATE_TRANSITION.
func TestCircuitBreakerOverrideRejectsNonSDKWarmPool_spec_15_1(t *testing.T) {
	router, pools, runtimes, _ := newPoolAdmin(t)
	etag := seedSDKWarmPool(t, router, runtimes, pools, "plain-pool", false)

	rr := putCircuitBreaker(t, router, "plain-pool", etag, admin.CircuitBreakerOverrideRequest{
		SDKWarm: &admin.SDKWarmOverridePayload{CircuitBreakerOverride: "enabled"},
	})
	if rr.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body=%s", rr.Code, rr.Body.String())
	}
}

// TestCircuitBreakerOverrideRejectsBadValue covers the §15.1 line 801
// closed vocabulary: a value outside enabled|disabled|auto is rejected 422.
func TestCircuitBreakerOverrideRejectsBadValue_spec_15_1(t *testing.T) {
	router, pools, runtimes, _ := newPoolAdmin(t)
	etag := seedSDKWarmPool(t, router, runtimes, pools, "sdk-pool", true)

	rr := putCircuitBreaker(t, router, "sdk-pool", etag, admin.CircuitBreakerOverrideRequest{
		SDKWarm: &admin.SDKWarmOverridePayload{CircuitBreakerOverride: "sometimes"},
	})
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422; body=%s", rr.Code, rr.Body.String())
	}
}

// TestCircuitBreakerOverrideRequiresField covers the empty-body / missing
// circuitBreakerOverride 400.
func TestCircuitBreakerOverrideRequiresField_spec_15_1(t *testing.T) {
	router, pools, runtimes, _ := newPoolAdmin(t)
	etag := seedSDKWarmPool(t, router, runtimes, pools, "sdk-pool", true)

	rr := putCircuitBreaker(t, router, "sdk-pool", etag, admin.CircuitBreakerOverrideRequest{})
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rr.Code, rr.Body.String())
	}
}

// TestCircuitBreakerOverrideEnforcesIfMatch covers the §15.1 lines
// 1207-1211 optimistic-concurrency precondition.
func TestCircuitBreakerOverrideEnforcesIfMatch_spec_15_1(t *testing.T) {
	router, pools, runtimes, _ := newPoolAdmin(t)
	_ = seedSDKWarmPool(t, router, runtimes, pools, "sdk-pool", true)

	// A stale ETag must be rejected before the mutation applies.
	rr := putCircuitBreaker(t, router, "sdk-pool", "\"999\"", admin.CircuitBreakerOverrideRequest{
		SDKWarm: &admin.SDKWarmOverridePayload{CircuitBreakerOverride: "disabled"},
	})
	if rr.Code != http.StatusPreconditionFailed {
		t.Fatalf("status = %d, want 412; body=%s", rr.Code, rr.Body.String())
	}
}

// TestMainPutSetsAcknowledgeHighDemotionRate covers the §6.1 line 48
// acknowledgeHighDemotionRate flag set through the main pool PUT.
func TestMainPutSetsAcknowledgeHighDemotionRate_spec_6_1(t *testing.T) {
	router, pools, runtimes, _ := newPoolAdmin(t)
	_ = seedSDKWarmPool(t, router, runtimes, pools, "sdk-pool", true)

	rr := poolReq(t, router.Handler(), http.MethodPut, "/v1/admin/pools/sdk-pool", admin.UpdatePoolRequest{
		SDKWarm: &admin.SDKWarmPayload{AcknowledgeHighDemotionRate: true},
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rr.Code, rr.Body.String())
	}
	row, _ := pools.Get(context.Background(), "sdk-pool")
	if !row.AcknowledgeHighDemotionRate {
		t.Error("acknowledgeHighDemotionRate should be set by the main PUT")
	}
}
