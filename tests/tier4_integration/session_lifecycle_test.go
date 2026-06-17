// SPDX-License-Identifier: MIT

//go:build integration

// Tier-4 integration test: exercises the full §15.1 REST session
// lifecycle against the real cmd/lenny-gateway binary (subprocess),
// not just the http.Handler. Validates that the binary boots, the
// HTTP listener responds, and the in-memory state machine survives
// real HTTP round-trips.
//
// Production tier-4 tests will swap memstore for a Postgres-backed
// store + the orchestrator + the upload pipeline; this test is the
// substrate that proves the same wire contract holds end-to-end
// through a real process.

package tier4_integration_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"testing"

	"github.com/lennylabs/lenny/tests/testinfra/gateway"
)

// spec: 15.1 (full session lifecycle through the gateway subprocess)
// diagnosis: A transition through the binary returned the wrong
//
//	state or a non-2xx. The integration path differs from the
//	httptest version — middleware order, store wiring, or
//	binary dev-mode flag is wrong in cmd/lenny-gateway.
func TestSessionLifecycleAgainstRealGateway(t *testing.T) {
	gw := gateway.StartWith(t, "--no-environment-policy", "allow-all")
	base := gw.BaseURL()
	client := http.DefaultClient

	post := func(path string, body any) *http.Response {
		t.Helper()
		var reader io.Reader
		if body != nil {
			b, _ := json.Marshal(body)
			reader = bytes.NewReader(b)
		}
		req, _ := http.NewRequest(http.MethodPost, base+path, reader)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Lenny-Tenant-ID", "acme")
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("POST %s: %v", path, err)
		}
		return resp
	}
	decode := func(resp *http.Response) map[string]any {
		t.Helper()
		defer resp.Body.Close()
		raw, _ := io.ReadAll(resp.Body)
		if len(raw) == 0 {
			return nil
		}
		var out map[string]any
		if err := json.Unmarshal(raw, &out); err != nil {
			t.Fatalf("decode response: %v\nbody: %s", err, raw)
		}
		return out
	}

	// Create.
	resp := post("/v1/sessions", map[string]any{
		"runtimeRef": "claude-code",
		"userId":     "alice@acme.com",
	})
	if resp.StatusCode != http.StatusCreated {
		body := decode(resp)
		t.Fatalf("create: want 201, got %d (body %v)", resp.StatusCode, body)
	}
	body := decode(resp)
	id, _ := body["id"].(string)
	if id == "" {
		t.Fatalf("missing session id")
	}

	// Drive through the happy path.
	for _, step := range []struct {
		endpoint  string
		wantState string
	}{
		{"finalize", "ready"},
		{"start", "running"},
		{"interrupt", "suspended"},
		{"terminate", "completed"},
	} {
		resp := post(fmt.Sprintf("/v1/sessions/%s/%s", id, step.endpoint), nil)
		out := decode(resp)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("/%s: want 200, got %d (body %v)", step.endpoint, resp.StatusCode, out)
		}
		if got := out["state"]; got != step.wantState {
			t.Errorf("after /%s: want state %q, got %v", step.endpoint, step.wantState, got)
		}
	}

	// GET the final state from a fresh request.
	getReq, _ := http.NewRequest(http.MethodGet, base+"/v1/sessions/"+id, nil)
	getReq.Header.Set("X-Lenny-Tenant-ID", "acme")
	getResp, err := client.Do(getReq)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	out := decode(getResp)
	if getResp.StatusCode != http.StatusOK {
		t.Fatalf("GET: want 200, got %d", getResp.StatusCode)
	}
	if out["state"] != "completed" {
		t.Errorf("final state: want completed, got %v", out["state"])
	}
}

// spec: 4.2 + 15.1 (tenant isolation through the gateway subprocess)
// diagnosis: A cross-tenant GET succeeded end-to-end. The
//
//	permissiveRegistry must still let the auth middleware
//	resolve distinct tenant ids; the store path then enforces
//	isolation.
func TestCrossTenantLookupRejectedAgainstRealGateway(t *testing.T) {
	gw := gateway.StartWith(t, "--no-environment-policy", "allow-all")
	base := gw.BaseURL()

	// Create as tenant acme.
	req1, _ := http.NewRequest(http.MethodPost, base+"/v1/sessions",
		bytes.NewReader(mustJSON(map[string]any{"runtimeRef": "claude-code"})))
	req1.Header.Set("Content-Type", "application/json")
	req1.Header.Set("X-Lenny-Tenant-ID", "acme")
	resp1, err := http.DefaultClient.Do(req1)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	defer resp1.Body.Close()
	raw, _ := io.ReadAll(resp1.Body)
	var c map[string]any
	_ = json.Unmarshal(raw, &c)
	id, _ := c["id"].(string)

	// Read as tenant globex — must 404.
	req2, _ := http.NewRequest(http.MethodGet, base+"/v1/sessions/"+id, nil)
	req2.Header.Set("X-Lenny-Tenant-ID", "globex")
	resp2, err := http.DefaultClient.Do(req2)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusNotFound {
		t.Errorf("cross-tenant GET: want 404, got %d", resp2.StatusCode)
	}
}

func mustJSON(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}
