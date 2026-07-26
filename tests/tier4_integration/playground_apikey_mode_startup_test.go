// SPDX-License-Identifier: MIT

//go:build integration

// Tier-4 integration test: the §27.2 line 42 install-time-only scope of
// playground.acknowledgeApiKeyMode, exercised through the real
// cmd/lenny-gateway process boundary. The acknowledgement knob is read
// only by lenny-preflight (pkg/preflight.CheckPlaygroundAPIKeyMode); the
// gateway binary has no corresponding flag or startup gate at all (see
// cmd/lenny-gateway/flags.go and the playground.Config.Validate call in
// cmd/lenny-gateway/httpsurface.go), so a deployment that ships
// playground.authMode=apiKey outside global.devMode with the
// acknowledgement unset must still boot cleanly.
package tier4_integration_test

import (
	"net/http"
	"testing"

	"github.com/lennylabs/lenny/tests/testinfra/gateway"
)

// spec: §27.2 ("`playground.devTenantId` validation layering" preamble)
// — "The acknowledgement is install-time only — the gateway does not
// gate startup on it."
//
// diagnosis: a failure means the real cmd/lenny-gateway binary either
// failed to reach readiness or exited non-zero when booted with
// playground.authMode=apiKey outside global.devMode and no
// acknowledgement supplied — the gateway has no
// playground.acknowledgeApiKeyMode-equivalent flag, so any such gate
// would have to be a regression that invented one, inverting the §27.2
// documented split between the install-time preflight WARNING and the
// non-enforcing gateway runtime.
func TestPlaygroundAPIKeyModeBootsCleanlyWithoutAcknowledgement_spec_27_2(t *testing.T) {
	// Every live cmd/lenny-gateway process that reaches
	// playground.NewMetrics() with playground.enabled=true currently
	// crash-loops: the lenny_playground_page_views_total counter's
	// "authMode" label fails the §16.1.1 snake_case validator (tracked
	// separately; see BUILD-GAPS.md §16.1 Finding 8). That crash is
	// unrelated to the acknowledgeApiKeyMode gating this test targets —
	// it fires for authMode=oidc and authMode=apiKey alike — but it
	// currently makes any clean-boot assertion with the playground
	// enabled unreachable. Unskip once that finding lands.
	t.Skip("blocked by the pre-existing non-snake_case \"authMode\" playground-metrics label defect (BUILD-GAPS.md §16.1 Finding 8), which crash-loops any live gateway process with playground.enabled=true before this test's assertion can run")

	gw := gateway.StartWith(
		t,
		"--dev-mode=false",
		"--tls-terminated-upstream=true",
		"--oidc-issuer-url", "https://idp.example.invalid",
		"--oidc-client-id", "test-client",
		"--no-environment-policy", "deny-all",
		"--playground-enabled",
		"--playground-auth-mode", "apiKey",
	)
	// gateway.StartWith already fails the test if the process does not
	// reach readiness within its deadline (which a startup log.Fatal on
	// an invented acknowledgement gate would prevent). Confirm the
	// playground routes it mounted for apiKey mode are live rather than
	// 404 (which would indicate playground.enabled did not take, a
	// different failure mode than the one this test targets).
	resp, err := http.Get(gw.BaseURL() + "/playground/")
	if err != nil {
		t.Fatalf("GET /playground/: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		t.Fatalf("GET /playground/ returned 404 with playground.enabled=true; want the mounted SPA route")
	}
}
