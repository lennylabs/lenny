// SPDX-License-Identifier: MIT

package admin_test

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/externalapi/admin"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/runtimestore"
)

// TestCreatePoolRejectsPreConnectIncompatible_spec_5_2_6_1 covers the §5.2
// line 430 / §6.1 lines 77-78 admission guard on pool creation: an SDK-warm
// runtime (capabilities.preConnect: true) bound to a service-mode pool or a
// session pool with maxConcurrentSessions > 1 is rejected, while the same
// runtime on a one-session-per-pod pool and a non-preConnect runtime on a
// service pool are both admitted.
func TestCreatePoolRejectsPreConnectIncompatible_spec_5_2_6_1(t *testing.T) {
	router, _, runtimes, _ := newPoolAdmin(t)
	mustCreateRuntime(t, runtimes, "warm-rt", true)
	mustCreateRuntime(t, runtimes, "plain-rt", false)

	// preConnect runtime + service mode → rejected.
	rr := poolReq(t, router.Handler(), http.MethodPost, "/v1/admin/pools", admin.PoolPayload{
		Name:          "warm-service",
		RuntimeRef:    "warm-rt",
		ExecutionMode: "service",
		MaxConcurrent: 4,
	})
	if rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), "executionMode: service") {
		t.Fatalf("preConnect service: status %d body=%s", rr.Code, rr.Body.String())
	}

	// preConnect runtime + concurrent sessions → rejected with its message.
	rr = poolReq(t, router.Handler(), http.MethodPost, "/v1/admin/pools", admin.PoolPayload{
		Name:          "warm-concurrent",
		RuntimeRef:    "warm-rt",
		ExecutionMode: "session",
		SessionPolicy: &runtimestore.SessionPolicy{
			MaxConcurrentSessions:            4,
			AcknowledgeProcessLevelIsolation: true,
		},
	})
	if rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), "maxConcurrentSessions") {
		t.Fatalf("preConnect concurrent sessions: status %d body=%s", rr.Code, rr.Body.String())
	}

	// preConnect runtime on a one-session-per-pod pool → admitted.
	rr = poolReq(t, router.Handler(), http.MethodPost, "/v1/admin/pools", admin.PoolPayload{
		Name:          "warm-session",
		RuntimeRef:    "warm-rt",
		ExecutionMode: "session",
		ResourceClass: "small",
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("preConnect session pool must be admitted: status %d body=%s", rr.Code, rr.Body.String())
	}

	// non-preConnect runtime on a service pool → admitted.
	rr = poolReq(t, router.Handler(), http.MethodPost, "/v1/admin/pools", admin.PoolPayload{
		Name:          "plain-service",
		RuntimeRef:    "plain-rt",
		ExecutionMode: "service",
		MaxConcurrent: 4,
		ResourceClass: "small",
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("non-preConnect service pool must be admitted: status %d body=%s", rr.Code, rr.Body.String())
	}
}

// TestUpdatePoolRejectsPreConnectIncompatible_spec_5_2_6_1 covers the §5.2
// line 430 / §6.1 lines 77-78 guard on the PUT path: switching a session
// pool that references a preConnect runtime into service mode is rejected on
// the effective post-update mode.
func TestUpdatePoolRejectsPreConnectIncompatible_spec_5_2_6_1(t *testing.T) {
	router, _, runtimes, _ := newPoolAdmin(t)
	mustCreateRuntime(t, runtimes, "warm-rt", true)

	// Seed a session pool on the preConnect runtime (admitted at create).
	rr := poolReq(t, router.Handler(), http.MethodPost, "/v1/admin/pools", admin.PoolPayload{
		Name:          "warm-session",
		RuntimeRef:    "warm-rt",
		ExecutionMode: "session",
		ResourceClass: "small",
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("seed session pool: status %d body=%s", rr.Code, rr.Body.String())
	}

	// PUT executionMode: service → rejected because the bound runtime is
	// preConnect.
	service := "service"
	maxConcurrent := 4
	rr = poolReq(t, router.Handler(), http.MethodPut, "/v1/admin/pools/warm-session",
		admin.UpdatePoolRequest{ExecutionMode: &service, MaxConcurrent: &maxConcurrent})
	if rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), "preConnect: true is not supported") {
		t.Fatalf("PUT to service on preConnect pool: status %d body=%s", rr.Code, rr.Body.String())
	}
}

func mustCreateRuntime(t *testing.T, runtimes *runtimestore.Memory, name string, preConnect bool) {
	t.Helper()
	rt := runtimestore.Runtime{Name: name}
	if preConnect {
		rt.Capabilities = &runtimestore.RuntimeCapabilities{PreConnect: true}
	}
	if err := runtimes.Create(context.Background(), rt); err != nil {
		t.Fatalf("create runtime %s: %v", name, err)
	}
}
