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

func TestIdempotencyReplayThroughBinary(t *testing.T) {
	gw := gateway.Start(t)
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

func TestIdempotencyDifferentBodyReusedThroughBinary(t *testing.T) {
	gw := gateway.Start(t)
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

func TestIdempotencyOversizeKeyThroughBinary(t *testing.T) {
	gw := gateway.Start(t)
	resp := postRaw(t, gw.BaseURL(), strings.Repeat("a", 129),
		map[string]any{"runtimeRef": "claude-code"}, "acme")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("oversize key: want 400, got %d", resp.StatusCode)
	}
}

func TestIdempotencyTenantScopedThroughBinary(t *testing.T) {
	gw := gateway.Start(t)
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
