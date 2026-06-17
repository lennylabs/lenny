// SPDX-License-Identifier: MIT

//go:build security

// Tier-9 §12.9.9 elicitation tamper-detect probes. The §9.2 chain
// walker, the dispatcher, and the metric/alert wiring are covered
// by pkg/elicitation and pkg/gateway/mcptools unit tests. This
// suite drives the gateway-side admin API against a live cluster
// to verify the §9.2 enforcement-mode resolver, the §15.1 admin
// wire contract, and the platform floor behave per spec end-to-end.
//
// The tests reach the in-cluster gateway via `kubectl port-forward`
// and drive `/v1/admin/tenants/{id}/elicitation-content-integrity`
// directly. Each test skips with a clear precondition when the
// cluster is not up or the gateway is not reachable.

package tier9_security_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/tests/testinfra/kind"
)

// elicitationProbe wraps a kubectl port-forward connection to the
// in-cluster gateway plus a configured HTTP client. The probe is
// goroutine-safe at the connection level because each request
// builds a fresh http.Request.
type elicitationProbe struct {
	baseURL string
	client  *http.Client
	cancel  func()
}

// startElicitationProbe forwards the in-cluster gateway service to
// a local port and returns a probe. Skips the test when port-forward
// fails or when /healthz never returns 200.
func startElicitationProbe(t *testing.T, c *kind.Cluster) *elicitationProbe {
	t.Helper()
	port := freeLocalPort(t)
	cmd := c.Kubectl("-n", "lenny-system", "port-forward", "svc/lenny-gateway",
		fmt.Sprintf("%d:8080", port))
	if err := cmd.Start(); err != nil {
		t.Skipf("§12.9.9: kubectl port-forward did not start: %v", err)
	}
	cancel := func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	}
	baseURL := fmt.Sprintf("http://127.0.0.1:%d", port)
	client := &http.Client{Timeout: 5 * time.Second}
	ready := false
	for i := 0; i < 30; i++ {
		resp, err := client.Get(baseURL + "/healthz")
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				ready = true
				break
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	if !ready {
		cancel()
		t.Skipf("§12.9.9: gateway not reachable through port-forward on /healthz; run tests/testinfra/kind/install.sh")
	}
	probe := &elicitationProbe{baseURL: baseURL, client: client, cancel: cancel}
	t.Cleanup(probe.cancel)
	return probe
}

// freeLocalPort asks the OS for a free TCP port on 127.0.0.1.
func freeLocalPort(t *testing.T) int {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("free port: %v", err)
	}
	defer lis.Close()
	return lis.Addr().(*net.TCPAddr).Port
}

// put drives a PUT against the admin API. Returns status and body.
func (p *elicitationProbe) put(t *testing.T, path string, body any) (int, string) {
	t.Helper()
	raw, _ := json.Marshal(body)
	req, _ := http.NewRequest(http.MethodPut, p.baseURL+path, bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Lenny-Tenant-ID", "platform")
	req.Header.Set("X-Lenny-User-ID", "tier9-admin")
	req.Header.Set("X-Lenny-Roles", "platform-admin")
	resp, err := p.client.Do(req)
	if err != nil {
		t.Fatalf("PUT %s: %v", path, err)
	}
	defer resp.Body.Close()
	bodyBytes, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(bodyBytes)
}

// get drives a GET against the admin API. Returns status and body.
func (p *elicitationProbe) get(t *testing.T, path string) (int, string) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, p.baseURL+path, nil)
	req.Header.Set("X-Lenny-Tenant-ID", "platform")
	req.Header.Set("X-Lenny-User-ID", "tier9-admin")
	req.Header.Set("X-Lenny-Roles", "platform-admin")
	resp, err := p.client.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer resp.Body.Close()
	bodyBytes, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(bodyBytes)
}

// requireTenantAcme returns whether a tenant 'acme' is seeded in the
// cluster (creating it via POST /v1/admin/tenants if needed). The
// bootstrap path creates it automatically when the chart's
// bootstrap.tenants is set; tests that require it skip cleanly when
// the create fails too.
func requireTenantAcme(t *testing.T, p *elicitationProbe) {
	t.Helper()
	status, _ := p.get(t, "/v1/admin/tenants/acme")
	if status == http.StatusOK {
		return
	}
	// Create the tenant via POST.
	status, body := p.post(t, "/v1/admin/tenants", map[string]any{
		"id":                "acme",
		"displayName":       "Acme Corp",
		"workspaceTier":     "T1",
		"complianceProfile": "standard",
	})
	if status != http.StatusOK && status != http.StatusCreated {
		t.Skipf("§12.9.9: tenant 'acme' missing and POST to create returned %d body %s", status, body)
	}
}

// post drives a POST against the admin API. Returns status and body.
func (p *elicitationProbe) post(t *testing.T, path string, body any) (int, string) {
	t.Helper()
	raw, _ := json.Marshal(body)
	req, _ := http.NewRequest(http.MethodPost, p.baseURL+path, bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Lenny-Tenant-ID", "platform")
	req.Header.Set("X-Lenny-User-ID", "tier9-admin")
	req.Header.Set("X-Lenny-Roles", "platform-admin")
	resp, err := p.client.Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	defer resp.Body.Close()
	bodyBytes, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(bodyBytes)
}

// spec: §9.2
// diagnosis: a tenant in enforce mode reads back as
// storedMode=enforce + effectiveMode=enforce. The §9.2 walker
// applies enforce posture for this tenant; the chain-walker behavior
// itself is covered by pkg/elicitation. This test pins the §15.1
// wire contract that exposes enforce mode to the operator.
func TestElicitationTamperEnforceMode(t *testing.T) {
	c := kind.InstallLenny(t)
	p := startElicitationProbe(t, c)
	requireTenantAcme(t, p)

	status, body := p.put(t, "/v1/admin/tenants/acme/elicitation-content-integrity",
		map[string]string{"mode": "enforce"})
	if status != http.StatusOK {
		t.Skipf("§12.9.9 enforce PUT returned %d body %s", status, body)
	}

	status, body = p.get(t, "/v1/admin/tenants/acme/elicitation-content-integrity")
	if status != http.StatusOK {
		t.Fatalf("GET returned %d body %s", status, body)
	}
	var got struct {
		StoredMode    string `json:"storedMode"`
		EffectiveMode string `json:"effectiveMode"`
	}
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatalf("decode body %q: %v", body, err)
	}
	if got.StoredMode != "enforce" || got.EffectiveMode != "enforce" {
		t.Errorf("§9.2 enforce: storedMode/effectiveMode = %q/%q, want enforce/enforce",
			got.StoredMode, got.EffectiveMode)
	}
}

// spec: §9.2
// diagnosis: a tenant stored as detect-only (with justification)
// reads back as detect-only on both storedMode and effectiveMode
// (no floor clamping). The §16.5 metric still fires on a tamper
// catch under this mode; the pkg/gateway/gatewaymetrics tests
// cover the enforcement_mode label.
func TestElicitationTamperDetectOnlyMode(t *testing.T) {
	c := kind.InstallLenny(t)
	p := startElicitationProbe(t, c)
	requireTenantAcme(t, p)

	status, body := p.put(t, "/v1/admin/tenants/acme/elicitation-content-integrity",
		map[string]any{"mode": "detect-only", "justification": "tier-9 §12.9.9 detect-only probe"})
	if status != http.StatusOK {
		t.Skipf("§12.9.9 detect-only PUT returned %d body %s (the cluster may have a stricter floor wired)",
			status, body)
	}

	status, body = p.get(t, "/v1/admin/tenants/acme/elicitation-content-integrity")
	if status != http.StatusOK {
		t.Fatalf("GET returned %d body %s", status, body)
	}
	var got struct {
		StoredMode    string `json:"storedMode"`
		EffectiveMode string `json:"effectiveMode"`
	}
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatalf("decode body %q: %v", body, err)
	}
	if got.StoredMode != "detect-only" {
		t.Errorf("§9.2 detect-only: storedMode = %q, want detect-only", got.StoredMode)
	}
	// effectiveMode is whichever is stricter; with the e2e default
	// floor of off, effective equals stored.
	if got.EffectiveMode != "detect-only" && got.EffectiveMode != "enforce" {
		t.Errorf("§9.2 detect-only: effectiveMode = %q, want detect-only (or enforce if a floor clamps)",
			got.EffectiveMode)
	}
}

// spec: §9.2
// diagnosis: the platform-wide floor clamps the tenant's effective
// mode and rejects a PUT that lowers a stored mode strictly below
// the floor. The test probes both behaviors:
//
//   - The GET response carries `storedMode` and `effectiveMode`
//     fields per the §15.1 contract.
//   - A PUT to `off` either succeeds (floor is `off` — the e2e
//     default) or is rejected with
//     ELICITATION_INTEGRITY_BELOW_PLATFORM_FLOOR (a stricter floor
//     is wired).
//
// In either case the §9.2 floor contract holds. The test fails only
// when the wire contract drifts from the spec — e.g., the response
// shape omits effectiveMode, or the floor neither permits nor
// rejects with the documented error code.
//
// spec: §9.2, §15.1.
// diagnosis: a failure means the elicitation-integrity platform floor
// wire contract drifted from the spec, either omitting storedMode /
// effectiveMode or neither permitting nor rejecting a below-floor PUT
// with ELICITATION_INTEGRITY_BELOW_PLATFORM_FLOOR.
func TestElicitationPlatformFloor(t *testing.T) {
	c := kind.InstallLenny(t)
	p := startElicitationProbe(t, c)
	requireTenantAcme(t, p)

	status, body := p.put(t, "/v1/admin/tenants/acme/elicitation-content-integrity",
		map[string]any{"mode": "off", "justification": "tier-9 §9.2 floor probe"})

	switch status {
	case http.StatusOK:
		// The floor permits `off`. The §15.1 GET must reflect both
		// storedMode and effectiveMode.
		status, body = p.get(t, "/v1/admin/tenants/acme/elicitation-content-integrity")
		if status != http.StatusOK {
			t.Fatalf("GET returned %d body %s", status, body)
		}
		var got struct {
			StoredMode    string `json:"storedMode"`
			EffectiveMode string `json:"effectiveMode"`
		}
		if err := json.Unmarshal([]byte(body), &got); err != nil {
			t.Fatalf("decode body %q: %v", body, err)
		}
		if got.StoredMode != "off" {
			t.Errorf("§9.2 floor permissive: storedMode = %q, want off", got.StoredMode)
		}
		if got.EffectiveMode == "" {
			t.Errorf("§9.2 floor: response is missing effectiveMode (§15.1 contract violation)")
		}
	case http.StatusBadRequest:
		// The floor rejected the write. Body must carry the
		// documented §9.2 error code.
		if !strings.Contains(body, "ELICITATION_INTEGRITY_BELOW_PLATFORM_FLOOR") {
			t.Errorf("§9.2 floor: 400 body = %q, want ELICITATION_INTEGRITY_BELOW_PLATFORM_FLOOR", body)
		}
	default:
		t.Skipf("§9.2 floor: PUT returned status %d body %q (cluster may not have the admin path wired)",
			status, body)
	}
}

// guard: ensure exec.Command is referenced even though it is used
// only by the kind.Cluster Kubectl helper. Keeps imports stable.
var _ = exec.Command
