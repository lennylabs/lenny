// SPDX-License-Identifier: MIT

package admin_test

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/admin"
	"github.com/lennylabs/lenny/pkg/gateway/runtimestore"
)

// TestCreatePoolRejectsPreConnectConcurrent_spec_6_1 covers the §6.1 lines
// 77-78 admission guard on pool creation: an SDK-warm runtime
// (capabilities.preConnect: true) bound to a concurrent-mode pool is
// rejected with the style-specific message, while the same runtime on a
// session pool and a non-preConnect runtime on a concurrent pool are both
// admitted.
func TestCreatePoolRejectsPreConnectConcurrent_spec_6_1(t *testing.T) {
	router, _, runtimes, _ := newPoolAdmin(t)
	mustCreateRuntime(t, runtimes, "warm-rt", true)
	mustCreateRuntime(t, runtimes, "plain-rt", false)

	// preConnect runtime + concurrent/workspace → rejected.
	rr := poolReq(t, router.Handler(), http.MethodPost, "/v1/admin/pools", admin.PoolPayload{
		Name:             "warm-concurrent-ws",
		RuntimeRef:       "warm-rt",
		ExecutionMode:    "concurrent",
		ConcurrencyStyle: "workspace",
	})
	if rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), "concurrencyStyle: workspace") {
		t.Fatalf("preConnect concurrent/workspace: status %d body=%s", rr.Code, rr.Body.String())
	}

	// preConnect runtime + concurrent/stateless → rejected with its message.
	rr = poolReq(t, router.Handler(), http.MethodPost, "/v1/admin/pools", admin.PoolPayload{
		Name:             "warm-concurrent-sl",
		RuntimeRef:       "warm-rt",
		ExecutionMode:    "concurrent",
		ConcurrencyStyle: "stateless",
	})
	if rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), "concurrencyStyle: stateless") {
		t.Fatalf("preConnect concurrent/stateless: status %d body=%s", rr.Code, rr.Body.String())
	}

	// preConnect runtime on a session pool → admitted.
	rr = poolReq(t, router.Handler(), http.MethodPost, "/v1/admin/pools", admin.PoolPayload{
		Name:          "warm-session",
		RuntimeRef:    "warm-rt",
		ExecutionMode: "session",
		ResourceClass: "small",
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("preConnect session pool must be admitted: status %d body=%s", rr.Code, rr.Body.String())
	}

	// non-preConnect runtime on a concurrent pool → not rejected by this
	// guard (it clears the §6.1 check and proceeds to the concurrent-config
	// validation, which a complete config satisfies).
	rr = poolReq(t, router.Handler(), http.MethodPost, "/v1/admin/pools", admin.PoolPayload{
		Name:                             "plain-concurrent",
		RuntimeRef:                       "plain-rt",
		ExecutionMode:                    "concurrent",
		ConcurrencyStyle:                 "workspace",
		MaxConcurrent:                    4,
		AcknowledgeProcessLevelIsolation: true,
		ResourceClass:                    "small",
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("non-preConnect concurrent pool must be admitted: status %d body=%s", rr.Code, rr.Body.String())
	}
}

// TestUpdatePoolRejectsPreConnectConcurrent_spec_6_1 covers the §6.1 lines
// 77-78 guard on the PUT path: switching a session pool that references a
// preConnect runtime into concurrent mode is rejected on the effective
// post-update mode, while a non-preConnect pool may switch freely.
func TestUpdatePoolRejectsPreConnectConcurrent_spec_6_1(t *testing.T) {
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

	// PUT executionMode: concurrent → rejected because the bound runtime is
	// preConnect.
	concurrent := "concurrent"
	rr = poolReq(t, router.Handler(), http.MethodPut, "/v1/admin/pools/warm-session",
		admin.UpdatePoolRequest{ExecutionMode: &concurrent})
	if rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), "preConnect: true is not supported") {
		t.Fatalf("PUT to concurrent on preConnect pool: status %d body=%s", rr.Code, rr.Body.String())
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
