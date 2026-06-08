// SPDX-License-Identifier: MIT

//go:build integration

// Tier-4 integration test: the §5.2 stateless-concurrent execution mode
// (`executionMode: concurrent`, `concurrencyStyle: stateless`) exercised
// against the real cmd/lenny-gateway binary in dev mode.
//
// The test drives the §15.1 admin pool API to confirm the Phase 12c
// §5.2 admission contract for the stateless sub-variant: a
// stateless-concurrent pool is admitted without the
// acknowledgeProcessLevelIsolation flag (stateless mode materializes no
// per-slot workspace, so the concurrent-workspace acknowledgment does
// not apply), is still rejected when it sets allowCrossTenantReuse
// (concurrent mode has no cross-tenant boundary regardless of style),
// requires a maxConcurrent slot bound, and that the concurrent-only
// fields are rejected on a non-concurrent pool.
//
// The slot routing path itself (tenant-affinity routing through a
// Kubernetes Service, pod readiness reflecting slot availability) needs
// a real cluster; the Tier 5 concurrent_modes_test exercises that. This
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
// §5.2 stateless-concurrent (`executionMode: concurrent`,
// `concurrencyStyle: stateless`) admission rules. A stateless-concurrent
// pool must be admitted without the acknowledgeProcessLevelIsolation
// flag, rejected when it sets allowCrossTenantReuse, require a
// maxConcurrent bound, and the concurrent-only fields must be rejected
// on a non-concurrent pool.
func TestConcurrentStatelessMode(t *testing.T) {
	gw := gateway.StartWith(t, "--dev-mode")
	base := gw.BaseURL()
	client := http.DefaultClient

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

	// ---- register the runtime the concurrent pool warms ----
	code, _ := do(http.MethodPost, "/v1/admin/runtimes", map[string]any{
		"name":   "stateless-runtime",
		"image":  "lenny/stateless@sha256:abc",
		"labels": map[string]string{"tier": "test"}, // §5.1 line 51: labels required
	})
	if code != http.StatusCreated {
		t.Fatalf("register runtime: status %d", code)
	}

	// ---- §5.2: a stateless-concurrent pool is admitted WITHOUT the
	// acknowledgeProcessLevelIsolation flag — stateless mode has no
	// per-slot workspace, so that concurrent-workspace acknowledgment
	// does not apply ----
	code, created := do(http.MethodPost, "/v1/admin/pools", map[string]any{
		"name":             "cs-pool",
		"runtimeRef":       "stateless-runtime",
		"executionMode":    "concurrent",
		"concurrencyStyle": "stateless",
		"maxConcurrent":    8,
	})
	if code != http.StatusCreated {
		t.Fatalf("stateless-concurrent pool: status %d (body %v)", code, created)
	}
	if created["executionMode"] != "concurrent" || created["concurrencyStyle"] != "stateless" {
		t.Errorf("created pool mode/style = %v/%v, want concurrent/stateless",
			created["executionMode"], created["concurrencyStyle"])
	}

	// The pool reads back with its concurrent configuration intact.
	code, got := do(http.MethodGet, "/v1/admin/pools/cs-pool", nil)
	if code != http.StatusOK {
		t.Fatalf("get cs-pool: status %d", code)
	}
	if got["concurrencyStyle"] != "stateless" {
		t.Errorf("cs-pool concurrencyStyle did not round-trip: %v", got["concurrencyStyle"])
	}
	if mc, _ := got["maxConcurrent"].(float64); int(mc) != 8 {
		t.Errorf("cs-pool maxConcurrent did not round-trip: %v", got["maxConcurrent"])
	}

	// ---- §5.2: a stateless-concurrent pool that sets
	// allowCrossTenantReuse is rejected — concurrent mode has no
	// cross-tenant isolation boundary, regardless of style ----
	code, body := do(http.MethodPost, "/v1/admin/pools", map[string]any{
		"name":                  "cs-cross-tenant",
		"runtimeRef":            "stateless-runtime",
		"executionMode":         "concurrent",
		"concurrencyStyle":      "stateless",
		"maxConcurrent":         4,
		"allowCrossTenantReuse": true,
	})
	if code != http.StatusBadRequest {
		t.Fatalf("stateless-concurrent pool with allowCrossTenantReuse: status %d, want 400 (body %v)",
			code, body)
	}

	// ---- §5.2: a stateless-concurrent pool that omits maxConcurrent is
	// rejected — the per-pod slot bound is required ----
	code, body = do(http.MethodPost, "/v1/admin/pools", map[string]any{
		"name":             "cs-no-bound",
		"runtimeRef":       "stateless-runtime",
		"executionMode":    "concurrent",
		"concurrencyStyle": "stateless",
	})
	if code != http.StatusBadRequest {
		t.Fatalf("stateless-concurrent pool without maxConcurrent: status %d, want 400 (body %v)",
			code, body)
	}

	// ---- §5.2: a concurrent pool with an unrecognised concurrencyStyle
	// is rejected ----
	code, body = do(http.MethodPost, "/v1/admin/pools", map[string]any{
		"name":             "cs-bad-style",
		"runtimeRef":       "stateless-runtime",
		"executionMode":    "concurrent",
		"concurrencyStyle": "bogus",
		"maxConcurrent":    4,
	})
	if code != http.StatusBadRequest {
		t.Fatalf("concurrent pool with an unrecognised concurrencyStyle: status %d, want 400 (body %v)",
			code, body)
	}

	// ---- §5.2: a session-mode pool that carries concurrent-only fields
	// is rejected rather than silently ignoring them ----
	code, body = do(http.MethodPost, "/v1/admin/pools", map[string]any{
		"name":             "session-with-style",
		"runtimeRef":       "stateless-runtime",
		"executionMode":    "session",
		"concurrencyStyle": "stateless",
	})
	if code != http.StatusBadRequest {
		t.Fatalf("session-mode pool carrying concurrencyStyle: status %d, want 400 (body %v)", code, body)
	}

	// ---- a stateless-concurrent pool is listable alongside other pools ----
	code, list := do(http.MethodGet, "/v1/admin/pools?runtimeRef=stateless-runtime", nil)
	if code != http.StatusOK {
		t.Fatalf("list pools: status %d", code)
	}
	pools, _ := list["items"].([]any)
	found := false
	for _, p := range pools {
		pm, _ := p.(map[string]any)
		if pm["name"] == "cs-pool" && pm["concurrencyStyle"] == "stateless" {
			found = true
		}
	}
	if !found {
		t.Error("the stateless-concurrent pool was not listed")
	}
}
