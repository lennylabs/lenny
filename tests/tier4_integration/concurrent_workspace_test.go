// SPDX-License-Identifier: MIT

//go:build integration

// Tier-4 integration test: the §5.2 workspace-concurrent execution mode
// (`executionMode: concurrent`, `concurrencyStyle: workspace`) exercised
// against the real cmd/lenny-gateway binary in dev mode.
//
// The test drives the §15.1 admin pool API to confirm the Phase 12c
// pod-level isolation enforcement: a workspace-concurrent pool is
// admitted only with the §5.2 acknowledgeProcessLevelIsolation deployer
// flag (the gateway-side half of the two-layer enforcement that keeps a
// concurrent-mode pool from weakening isolation), is rejected without
// it, is rejected when it sets allowCrossTenantReuse (concurrent mode
// has no cross-tenant boundary), and is rejected when its per-slot
// cleanup budget falls below the §5.2 5-second floor. It also confirms
// the slot bound — maxConcurrent >= 1 — is required and round-trips.
//
// The slot claim/assignment path itself (multiple sessions on one pod,
// the §6.4 per-slot workspace tree) needs real warm pods; the Tier 5
// concurrent_modes_test exercises that against a Kind cluster. This
// Tier 4 test covers the gateway-observable §5.2 admission contract.

package tier4_integration_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/lennylabs/lenny/tests/testinfra/gateway"
)

// spec: 5.2
// diagnosis: the gateway's §15.1 admin pool API did not enforce the
// §5.2 workspace-concurrent (`executionMode: concurrent`,
// `concurrencyStyle: workspace`) admission rules. A workspace-concurrent
// pool must be admitted only with the acknowledgeProcessLevelIsolation
// deployer flag, rejected without it, rejected when it sets
// allowCrossTenantReuse, and rejected when cleanupTimeoutSeconds falls
// below maxConcurrent * 5; maxConcurrent must be required and >= 1.
func TestConcurrentWorkspaceMode(t *testing.T) {
	gw := gateway.StartWith(t, "--dev-mode")
	base := gw.BaseURL()
	client := http.DefaultClient

	// do issues an admin request with the dev-mode auth headers and
	// returns the status and decoded body.
	do := func(method, path string, body any) (int, map[string]any) {
		t.Helper()
		var reader io.Reader
		if body != nil {
			b, _ := json.Marshal(body)
			reader = bytes.NewReader(b)
		}
		req, _ := http.NewRequest(method, base+path, reader)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Lenny-Tenant-ID", "platform")
		req.Header.Set("X-Lenny-User-ID", "alice@acme.com")
		req.Header.Set("X-Lenny-Roles", "platform-admin")
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("%s %s: %v", method, path, err)
		}
		defer resp.Body.Close()
		raw, _ := io.ReadAll(resp.Body)
		var out map[string]any
		if len(raw) > 0 {
			_ = json.Unmarshal(raw, &out)
		}
		return resp.StatusCode, out
	}

	// ---- register the runtime the concurrent pools warm ----
	code, _ := do(http.MethodPost, "/v1/admin/runtimes", map[string]any{
		"name":  "concurrent-runtime",
		"image": "lenny/concurrent@sha256:abc",
	})
	if code != http.StatusCreated {
		t.Fatalf("register runtime: status %d", code)
	}

	// ---- §5.2: a workspace-concurrent pool WITHOUT the deployer
	// acknowledgment is rejected ----
	code, body := do(http.MethodPost, "/v1/admin/pools", map[string]any{
		"name":             "cw-no-ack",
		"runtimeRef":       "concurrent-runtime",
		"executionMode":    "concurrent",
		"concurrencyStyle": "workspace",
		"maxConcurrent":    8,
	})
	if code != http.StatusBadRequest {
		t.Fatalf("workspace-concurrent pool without acknowledgeProcessLevelIsolation: "+
			"status %d, want 400 (body %v)", code, body)
	}

	// ---- §5.2: with the acknowledgment the same pool is admitted and
	// round-trips its concurrent fields ----
	code, created := do(http.MethodPost, "/v1/admin/pools", map[string]any{
		"name":                             "cw-pool",
		"runtimeRef":                       "concurrent-runtime",
		"executionMode":                    "concurrent",
		"concurrencyStyle":                 "workspace",
		"maxConcurrent":                    8,
		"acknowledgeProcessLevelIsolation": true,
		"cleanupTimeoutSeconds":            60,
	})
	if code != http.StatusCreated {
		t.Fatalf("workspace-concurrent pool with the acknowledgment: status %d (body %v)", code, created)
	}
	if created["executionMode"] != "concurrent" || created["concurrencyStyle"] != "workspace" {
		t.Errorf("created pool mode/style = %v/%v, want concurrent/workspace",
			created["executionMode"], created["concurrencyStyle"])
	}
	if mc, _ := created["maxConcurrent"].(float64); int(mc) != 8 {
		t.Errorf("created pool maxConcurrent = %v, want 8", created["maxConcurrent"])
	}

	// The pool reads back with its concurrent configuration intact.
	code, got := do(http.MethodGet, "/v1/admin/pools/cw-pool", nil)
	if code != http.StatusOK {
		t.Fatalf("get cw-pool: status %d", code)
	}
	if got["concurrencyStyle"] != "workspace" {
		t.Errorf("cw-pool concurrencyStyle did not round-trip: %v", got["concurrencyStyle"])
	}
	if ack, _ := got["acknowledgeProcessLevelIsolation"].(bool); !ack {
		t.Error("cw-pool acknowledgeProcessLevelIsolation did not round-trip")
	}

	// ---- §5.2: a workspace-concurrent pool that sets
	// allowCrossTenantReuse is rejected — concurrent mode has no
	// cross-tenant isolation boundary ----
	code, body = do(http.MethodPost, "/v1/admin/pools", map[string]any{
		"name":                             "cw-cross-tenant",
		"runtimeRef":                       "concurrent-runtime",
		"executionMode":                    "concurrent",
		"concurrencyStyle":                 "workspace",
		"maxConcurrent":                    4,
		"acknowledgeProcessLevelIsolation": true,
		"allowCrossTenantReuse":            true,
	})
	if code != http.StatusBadRequest {
		t.Fatalf("workspace-concurrent pool with allowCrossTenantReuse: status %d, want 400 (body %v)",
			code, body)
	}

	// ---- §5.2: cleanupTimeoutSeconds below maxConcurrent * 5 is
	// rejected — the per-slot cleanup budget would fall below the 5s
	// floor ----
	code, body = do(http.MethodPost, "/v1/admin/pools", map[string]any{
		"name":                             "cw-short-cleanup",
		"runtimeRef":                       "concurrent-runtime",
		"executionMode":                    "concurrent",
		"concurrencyStyle":                 "workspace",
		"maxConcurrent":                    8,
		"acknowledgeProcessLevelIsolation": true,
		"cleanupTimeoutSeconds":            20, // 20 / 8 = 2.5s < 5s floor
	})
	if code != http.StatusBadRequest {
		t.Fatalf("workspace-concurrent pool with sub-floor cleanup budget: status %d, want 400 (body %v)",
			code, body)
	}

	// ---- §5.2: a concurrent pool that omits maxConcurrent is rejected
	// — the per-pod slot bound is required ----
	code, body = do(http.MethodPost, "/v1/admin/pools", map[string]any{
		"name":                             "cw-no-bound",
		"runtimeRef":                       "concurrent-runtime",
		"executionMode":                    "concurrent",
		"concurrencyStyle":                 "workspace",
		"acknowledgeProcessLevelIsolation": true,
	})
	if code != http.StatusBadRequest {
		t.Fatalf("workspace-concurrent pool without maxConcurrent: status %d, want 400 (body %v)",
			code, body)
	}

	// ---- §5.2: a concurrent pool with no concurrencyStyle is rejected ----
	code, body = do(http.MethodPost, "/v1/admin/pools", map[string]any{
		"name":          "cw-no-style",
		"runtimeRef":    "concurrent-runtime",
		"executionMode": "concurrent",
		"maxConcurrent": 4,
	})
	if code != http.StatusBadRequest {
		t.Fatalf("concurrent pool without concurrencyStyle: status %d, want 400 (body %v)", code, body)
	}
}
