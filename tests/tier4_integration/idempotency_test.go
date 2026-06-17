// SPDX-License-Identifier: MIT

//go:build integration

// Tier-4 integration: §11.5 idempotency end-to-end through the
// gateway subprocess. Verifies that the wire-level Idempotency-Key
// contract holds when the binary's middleware stack is wired in.

package tier4_integration_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/tests/testinfra/gateway"
)

// spec: 11.5 (replay through the gateway subprocess + middleware stack)
// diagnosis: Replay returned a different session id end-to-end. The
//
//	middleware stack does not preserve the store across the
//	first/second request, or the subprocess restarts state.
func TestIdempotencyReplayThroughBinary(t *testing.T) {
	gw := gateway.StartWith(t, "--no-environment-policy", "allow-all")
	body := map[string]any{"runtimeRef": "claude-code"}

	first := postWithKey(t, gw.BaseURL(), "key-A", body, "acme")
	if first["state"] != "created" {
		t.Fatalf("first call must create; got %v", first)
	}

	second := postWithKey(t, gw.BaseURL(), "key-A", body, "acme")
	if first["id"] != second["id"] {
		t.Errorf("replay must return cached id; first=%v second=%v", first["id"], second["id"])
	}
}

// spec: 11.5 (body-hash detection through the running binary)
// diagnosis: Different body under same key was not rejected end-to-
//
//	end. The body-hash branch is not invoked in the binary
//	build, or the envelope mapping was lost.
func TestIdempotencyDifferentBodyReusedThroughBinary(t *testing.T) {
	gw := gateway.StartWith(t, "--no-environment-policy", "allow-all")
	postWithKey(t, gw.BaseURL(), "key-B", map[string]any{"runtimeRef": "claude-code"}, "acme")

	resp := postRaw(t, gw.BaseURL(), "key-B", map[string]any{"runtimeRef": "DIFFERENT"}, "acme")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("want 422 IDEMPOTENCY_KEY_REUSED, got %d", resp.StatusCode)
	}
	raw, _ := io.ReadAll(resp.Body)
	var body map[string]any
	_ = json.Unmarshal(raw, &body)
	envelope, _ := body["error"].(map[string]any)
	if envelope["code"] != "IDEMPOTENCY_KEY_REUSED" {
		t.Errorf("error code: want IDEMPOTENCY_KEY_REUSED, got %v", envelope["code"])
	}
}

// spec: 11.5 (key length cap enforced end-to-end through the binary)
// diagnosis: Oversize key was not rejected end-to-end. The key
//
//	validator is missing from the binary's middleware
//	configuration.
func TestIdempotencyOversizeKeyThroughBinary(t *testing.T) {
	gw := gateway.StartWith(t, "--no-environment-policy", "allow-all")
	resp := postRaw(t, gw.BaseURL(), strings.Repeat("a", 129),
		map[string]any{"runtimeRef": "claude-code"}, "acme")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("oversize key: want 400, got %d", resp.StatusCode)
	}
}

// spec: 11.5 + 4.2 (tenant-scoped idempotency through the binary)
// diagnosis: Tenant B's request replayed tenant A's response end-to-
//
//	end. The store key in the running binary did not include
//	the resolved tenant id from the auth middleware.
func TestIdempotencyTenantScopedThroughBinary(t *testing.T) {
	gw := gateway.StartWith(t, "--no-environment-policy", "allow-all")
	body := map[string]any{"runtimeRef": "claude-code"}

	postWithKey(t, gw.BaseURL(), "key-C", body, "acme")

	// Tenant B with same key+body must create a new session, NOT
	// replay tenant A's response.
	out := postWithKey(t, gw.BaseURL(), "key-C", body, "globex")
	if out["tenantId"] != "globex" {
		t.Errorf("tenant scoping: want globex, got %v", out["tenantId"])
	}
}

func postWithKey(t *testing.T, base, key string, body any, tenant string) map[string]any {
	t.Helper()
	resp := postRaw(t, base, key, body, tenant)
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("postWithKey want 201, got %d (body %s)", resp.StatusCode, raw)
	}
	var out map[string]any
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &out)
	}
	return out
}

func postRaw(t *testing.T, base, key string, body any, tenant string) *http.Response {
	t.Helper()
	b, _ := json.Marshal(body)
	req, _ := http.NewRequest(http.MethodPost, base+"/v1/sessions", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Lenny-Tenant-ID", tenant)
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	return resp
}
