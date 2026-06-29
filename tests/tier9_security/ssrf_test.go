// SPDX-License-Identifier: MIT

//go:build security

// Tier-9 security test for §12.9.5 SSRF and callback validation. The
// e2e gateway runs against a real lenny-postgres, so the §9.3
// ConnectorDefinition registry — the SSRF allowlist for external MCP
// traffic — is reachable through the POST/PUT /v1/admin/connectors
// admin API. §9.3 requires every connector URL to be HTTPS so a
// registered connector cannot become an http:// SSRF pivot; the
// connectorstore.Connector.Validate cross-field check enforces it at
// the admin Create/Update boundary.
//
// This test drives the live connector-create endpoint with an
// adversarial URL corpus and asserts the HTTPS-scheme requirement is
// enforced on the connector MCP URL and on the OAuth authorization /
// token endpoints: an http:// URL and a non-HTTPS scheme are both
// rejected with a 400 VALIDATION_ERROR. An https:// URL is accepted as
// the positive control.
//
// The DNS-rebinding / IP-pinning / IMDS-hostname blocklist half of
// §12.9.5 is the network-layer SSRF boundary (§13.2 egress except
// blocks covering 169.254.0.0/16 and the IMDS addresses) rather than
// an application-layer URL validator reachable from this endpoint; the
// connector validator checks the URL scheme, not the resolved
// destination IP. That half is asserted by the §13.2 NetworkPolicy
// posture tests, not here. Every connector created is removed in a
// t.Cleanup.

package tier9_security_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/tests/testinfra/kind"
)

// connectorDeployment is the gateway Deployment the connector registry
// admin API is served by.
const connectorDeployment = "lenny-gateway"

// ssrfRejectCase is one adversarial connector-create body the §9.3
// validator must reject, and the reason substring its 400
// VALIDATION_ERROR body must carry.
type ssrfRejectCase struct {
	name       string
	body       string
	wantReason string
}

// spec: 12.9.5
// diagnosis: §12.9.5 SSRF callback validation did not hold — the §9.3
// connector registry admitted a non-HTTPS connector URL. The test
// drives POST /v1/admin/connectors with an http:// MCP URL, a non-HTTPS
// scheme, and an http:// OAuth token endpoint, and asserts each is
// rejected with 400 VALIDATION_ERROR ("must use https"). An https://
// connector is the positive control and must be accepted. An admitted
// http:// connector is a registered SSRF pivot.
func TestSSRFCallbackValidation(t *testing.T) {
	c := kind.InstallLenny(t)
	if !deploymentReadyT9(t, c, connectorDeployment) {
		t.Skipf("precondition not met: %s is not Ready; the §9.3 connector registry is a gateway API",
			connectorDeployment)
	}
	probe := "t9-ssrf-probe"
	gatewayIP := startGatewayProbe(t, c, probe)
	admin := platformAdmin()

	// Connector IDs created (or attempted) by this test, removed in the
	// cleanup. The reject cases never persist a connector (the validator
	// fails before the row is written), but the cleanup deletes them
	// unconditionally — DELETE on an absent connector is a no-op 404.
	//
	// The connectors table soft-deletes: a DELETEd row keeps its
	// primary-key id reserved as a tombstone, so a create that reuses an
	// id fails with 409 RESOURCE_ALREADY_EXISTS. The positive control persists
	// a connector row, so it takes a run-unique id no prior run's
	// tombstone can collide with. The reject ids stay stable: they never
	// persist.
	httpsOKID := fmt.Sprintf("t9ssrf-https-ok-%d", time.Now().UnixNano())
	createdIDs := []string{
		"t9ssrf-http-mcp", "t9ssrf-ftp-scheme", "t9ssrf-http-oauth", httpsOKID,
	}
	t.Cleanup(func() {
		for _, id := range createdIDs {
			_ = gatewayRequest(t, c, probe, gatewayIP, "DELETE", "/v1/admin/connectors/"+id, admin, "")
		}
	})

	rejectCases := []ssrfRejectCase{
		{
			// §9.3: an http:// MCP URL is a plaintext SSRF pivot — rejected.
			name:       "http-mcp-url",
			body:       `{"id":"t9ssrf-http-mcp","mcpServerUrl":"http://internal.example.com/mcp"}`,
			wantReason: "https",
		},
		{
			// §9.3: a non-HTTP(S) scheme (ftp) is not a valid connector
			// transport URL — rejected by the same HTTPS check.
			name:       "non-https-scheme",
			body:       `{"id":"t9ssrf-ftp-scheme","mcpServerUrl":"ftp://internal.example.com/x"}`,
			wantReason: "https",
		},
		{
			// §9.3: the OAuth token endpoint is also gateway-dialled, so it
			// carries the same HTTPS requirement; an http:// token endpoint
			// is rejected even when the MCP URL itself is HTTPS.
			name: "http-oauth-token-endpoint",
			body: `{"id":"t9ssrf-http-oauth","mcpServerUrl":"https://ok.example.com/mcp",` +
				`"auth":{"type":"oauth2","authorizationEndpoint":"https://ok.example.com/authz",` +
				`"tokenEndpoint":"http://internal.example.com/token"}}`,
			wantReason: "https",
		},
	}

	for _, tc := range rejectCases {
		t.Run(tc.name, func(t *testing.T) {
			res := gatewayRequestRetry(t, c, probe, gatewayIP, "POST", "/v1/admin/connectors", admin, tc.body)
			if res.curlExit != 0 {
				t.Fatalf("connector-create request did not complete (curl exit %d, body %q)",
					res.curlExit, res.body)
			}
			if res.statusCode == 201 || res.statusCode == 200 {
				t.Fatalf("§12.9.5 violation: the §9.3 connector registry ADMITTED an adversarial "+
					"connector (%s); a non-HTTPS connector URL is a registered SSRF pivot (body %q)",
					tc.name, res.body)
			}
			if res.statusCode != 400 {
				t.Errorf("§12.9.5: the %s connector was rejected with status %d, expected 400 "+
					"VALIDATION_ERROR (body %q)", tc.name, res.statusCode, res.body)
				return
			}
			if code := res.errorCode(); code != "VALIDATION_ERROR" {
				t.Errorf("§12.9.5: the %s rejection carries error code %q, expected "+
					"\"VALIDATION_ERROR\" (body %q)", tc.name, code, res.body)
			}
			if tc.wantReason != "" && !strings.Contains(res.body, tc.wantReason) {
				t.Errorf("§12.9.5: the %s rejection body lacks the expected reason %q (body %q)",
					tc.name, tc.wantReason, res.body)
			}
			t.Logf("§12.9.5: connector vector %q rejected with 400 VALIDATION_ERROR", tc.name)
		})
	}

	// Positive control: an HTTPS connector with HTTPS OAuth endpoints is
	// admitted. A rejection here would mean the reject results above
	// could be a blanket connector-create failure rather than the §9.3
	// HTTPS check firing.
	t.Run("https-connector-accepted", func(t *testing.T) {
		body := fmt.Sprintf(`{"id":%q,"mcpServerUrl":"https://ok.example.com/mcp",`+
			`"auth":{"type":"oauth2","authorizationEndpoint":"https://ok.example.com/authz",`+
			`"tokenEndpoint":"https://ok.example.com/token","clientSecretRef":"lenny-system/conn-secret"}}`, httpsOKID)
		res := gatewayRequestRetry(t, c, probe, gatewayIP, "POST", "/v1/admin/connectors", admin, body)
		if res.curlExit != 0 {
			t.Fatalf("connector-create request did not complete (curl exit %d, body %q)",
				res.curlExit, res.body)
		}
		if res.statusCode != 201 {
			t.Fatalf("§12.9.5 positive control failed: the §9.3 connector registry rejected a fully "+
				"HTTPS connector (status %d, body %q); the HTTPS-scheme check is over-broad",
				res.statusCode, res.body)
		}
		t.Logf("§12.9.5 positive control: a fully-HTTPS connector was admitted (status 201)")
	})
}
