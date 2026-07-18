//go:build integration

// SPDX-License-Identifier: MIT

// Tier-4 integration test for the §25.10 gateway-backed running-state
// collector (pkg/ops/driftservice/gatewayreader). The package's own
// tests drive it against a hand-written fake that implements the
// AdminGetter interface directly, bypassing HTTP entirely. This test
// instead wires the production *gateway.Client (pkg/ops/gateway) — the
// same type cmd/lenny-ops's buildDriftService constructs when
// --gateway-url is set — against a real cmd/lenny-gateway subprocess, so
// the reader's HTTP transport, request pagination, and the four admin
// LIST endpoints' actual JSON envelope are all exercised end to end.
package tier4_integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/lennylabs/lenny/pkg/ops/driftservice/gatewayreader"
	opsgateway "github.com/lennylabs/lenny/pkg/ops/gateway"
	"github.com/lennylabs/lenny/tests/testinfra/gateway"
)

// devHeaderTransport stamps every outbound request with the dev-mode
// identity headers (§10.2 AllowDevHeaders / AllowDevRoles path) so the
// production *gateway.Client — which otherwise sends no
// Authorization/identity header at all when configured without a
// service-account TokenSource — authenticates as a platform-admin
// against the dev-mode gateway the same way a minted service-account
// bearer token would in production. This is a test-only RoundTripper
// installed through gateway.Config.HTTPClient, an existing client seam;
// it changes no production code path.
type devHeaderTransport struct {
	base http.RoundTripper
}

func (t devHeaderTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	req.Header.Set("X-Lenny-User-ID", "alice@acme.com")
	req.Header.Set("X-Lenny-Roles", "platform-admin")
	base := t.base
	if base == nil {
		base = http.DefaultTransport
	}
	return base.RoundTrip(req)
}

// TestGatewayReaderRunningStateAgainstLiveGatewayE2E boots a real
// cmd/lenny-gateway, seeds one resource of each of the four §25.10
// running-state resource types through its admin API, then drives
// gatewayreader.Reader — wired over the production *gateway.Client and
// its real HTTP transport, not a fake — and asserts the collected
// running state matches the seeded resources with the server-generated
// and observed fields normalized away.
//
// spec: §25.10 (Drift Detection Logic) — "GET /v1/admin/drift compares:
// 1. Running state — read via GatewayClient calls to GET
// /v1/admin/runtimes, GET /v1/admin/pools, etc."
// diagnosis: a failure means the gatewayreader collector's real HTTP
// transport diverged from its fake-backed unit tests — either a
// regression in gateway.Client's request/response handling, or a JSON
// envelope or field-shape change in one of the four admin LIST endpoints
// that the fake never would have caught.
func TestGatewayReaderRunningStateAgainstLiveGatewayE2E(t *testing.T) {
	gateway.SkipUnlessAvailable(t)

	gw := gateway.StartWith(t, "--dev-mode")
	base := gw.BaseURL()
	ctx := context.Background()
	httpClient := http.DefaultClient

	// do issues one admin request with the platform-admin dev headers
	// and decodes the JSON response.
	do := func(method, path string, body any) (int, map[string]any) {
		t.Helper()
		var reader io.Reader
		if body != nil {
			b, err := json.Marshal(body)
			if err != nil {
				t.Fatalf("marshal request body: %v", err)
			}
			reader = bytes.NewReader(b)
		}
		req, err := http.NewRequestWithContext(ctx, method, base+path, reader)
		if err != nil {
			t.Fatalf("build request: %v", err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Lenny-Tenant-ID", "acme")
		req.Header.Set("X-Lenny-User-ID", "alice@acme.com")
		req.Header.Set("X-Lenny-Roles", "platform-admin")
		resp, err := httpClient.Do(req)
		if err != nil {
			t.Fatalf("%s %s: %v", method, path, err)
		}
		defer func() { _ = resp.Body.Close() }()
		raw, _ := io.ReadAll(resp.Body)
		var out map[string]any
		if len(raw) > 0 {
			if err := json.Unmarshal(raw, &out); err != nil {
				t.Fatalf("decode %s %s response: %v (body %s)", method, path, err, raw)
			}
		}
		return resp.StatusCode, out
	}

	// ---- seed one resource of each §25.10 running-state resource type ----
	code, boot := do(http.MethodPost, "/v1/admin/bootstrap", map[string]any{
		"tenants": []map[string]any{
			{"id": "acme", "displayName": "Acme Corp"},
		},
		"runtimes": []map[string]any{
			{"name": "echo", "image": "lenny/echo@sha256:abc", "labels": map[string]string{"tier": "test"}},
		},
		"pools": []map[string]any{
			{"name": "default-pool", "runtimeRef": "echo", "isolationProfile": "sandboxed", "warmCount": 2},
		},
		"credentialPools": []map[string]any{
			{
				"tenantId": "acme",
				"name":     "anthropic",
				"provider": "anthropic_direct",
				"credentials": []map[string]any{
					{"id": "key-1", "secretRef": "lenny-system/anthropic-key-1"},
				},
			},
		},
	})
	if code != http.StatusOK {
		t.Fatalf("POST /v1/admin/bootstrap: status %d (%v)", code, boot)
	}

	// ---- confirm the raw wire carries the observed/server fields the
	// reader is expected to strip, so the later absence assertions are
	// not vacuously true ----
	code, poolsRaw := do(http.MethodGet, "/v1/admin/pools", nil)
	if code != http.StatusOK {
		t.Fatalf("GET /v1/admin/pools: status %d (%v)", code, poolsRaw)
	}
	poolItems, _ := poolsRaw["items"].([]any)
	if len(poolItems) != 1 {
		t.Fatalf("GET /v1/admin/pools: want 1 seeded pool, got %d (%v)", len(poolItems), poolsRaw)
	}
	rawPool, _ := poolItems[0].(map[string]any)
	if etag, _ := rawPool["etag"].(string); etag == "" {
		t.Fatalf("seeded pool has no etag on the wire; the strip assertion below would be vacuous: %v", rawPool)
	}
	if createdAt, _ := rawPool["createdAt"].(string); createdAt == "" {
		t.Fatalf("seeded pool has no createdAt on the wire; the strip assertion below would be vacuous: %v", rawPool)
	}

	// ---- drive the reader through the production gateway.Client and its
	// real HTTP transport, not the package's fakeAdmin ----
	client, err := opsgateway.NewClient(opsgateway.Config{
		BaseURL:    base,
		HTTPClient: &http.Client{Transport: devHeaderTransport{}},
	})
	if err != nil {
		t.Fatalf("build gateway client: %v", err)
	}
	reader := gatewayreader.New(client)

	state, err := reader.RunningState(ctx, gatewayreader.ScopeAll)
	if err != nil {
		t.Fatalf("RunningState(all) over the real gateway HTTP transport: %v", err)
	}

	runtimes, _ := state["runtimes"].(map[string]any)
	echo, _ := runtimes["echo"].(map[string]any)
	if echo == nil {
		t.Fatalf("running state did not collect the seeded runtime: %#v", state["runtimes"])
	}
	if echo["image"] != "lenny/echo@sha256:abc" {
		t.Errorf("runtime config field lost over the real HTTP transport: %#v", echo)
	}
	for _, dropped := range []string{"etag", "createdAt", "updatedAt", "deletedAt"} {
		if _, present := echo[dropped]; present {
			t.Errorf("observed field %q was not stripped from the runtime collected over real HTTP: %#v", dropped, echo)
		}
	}

	pools, _ := state["pools"].(map[string]any)
	pool, _ := pools["default-pool"].(map[string]any)
	if pool == nil {
		t.Fatalf("running state did not collect the seeded pool: %#v", state["pools"])
	}
	if pool["warmCount"] != float64(2) || pool["isolationProfile"] != "sandboxed" {
		t.Errorf("pool config fields lost over the real HTTP transport: %#v", pool)
	}
	for _, dropped := range []string{
		"etag", "createdAt", "updatedAt", "deletedAt",
		"poolCondition", "idlePodCount", "activeSessions", "syncStatus", "phase", "bootstrapStatus",
	} {
		if _, present := pool[dropped]; present {
			t.Errorf("observed field %q was not stripped from the pool collected over real HTTP: %#v", dropped, pool)
		}
	}

	tenants, _ := state["tenants"].(map[string]any)
	tenant, _ := tenants["acme"].(map[string]any)
	if tenant == nil {
		t.Fatalf("running state did not collect the seeded tenant: %#v", state["tenants"])
	}
	if tenant["displayName"] != "Acme Corp" {
		t.Errorf("tenant config field lost over the real HTTP transport: %#v", tenant)
	}
	for _, dropped := range []string{"etag", "createdAt", "updatedAt", "deletedAt", "t4KmsLastProbeSuccessAt"} {
		if _, present := tenant[dropped]; present {
			t.Errorf("observed field %q was not stripped from the tenant collected over real HTTP: %#v", dropped, tenant)
		}
	}

	creds, _ := state["credential-pools"].(map[string]any)
	cred, _ := creds["acme/anthropic"].(map[string]any)
	if cred == nil {
		t.Fatalf("running state did not collect the seeded credential pool: %#v", state["credential-pools"])
	}
	if cred["provider"] != "anthropic_direct" {
		t.Errorf("credential-pool config field lost over the real HTTP transport: %#v", cred)
	}
	for _, dropped := range []string{"etag", "createdAt", "updatedAt", "deletedAt", "tenantId", "name"} {
		if _, present := cred[dropped]; present {
			t.Errorf("field %q was not stripped from the credential pool collected over real HTTP: %#v", dropped, cred)
		}
	}

	// ---- a narrow scope collects only its one resource type over the
	// real transport too ----
	poolsOnly, err := reader.RunningState(ctx, gatewayreader.ScopePools)
	if err != nil {
		t.Fatalf("RunningState(pools) over the real gateway HTTP transport: %v", err)
	}
	if _, ok := poolsOnly["pools"]; !ok {
		t.Fatal("pools scope did not collect pools over the real HTTP transport")
	}
	if _, ok := poolsOnly["runtimes"]; ok {
		t.Fatal("pools scope collected runtimes over the real HTTP transport — narrow scope leaked")
	}
}
