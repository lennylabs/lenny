// SPDX-License-Identifier: MIT

package preflight_test

import (
	"strings"
	"testing"

	"github.com/lennylabs/lenny/pkg/preflight"
)

// TestCheckPlaygroundConfigSkipsWhenDisabled exercises the §27.2
// playground-config preflight check's early exit: a chart with
// playground.enabled=false leaves every other playground.* value
// unread, so a malformed authMode passes trivially.
//
// spec: §27.2 lines 41–42.
func TestCheckPlaygroundConfigSkipsWhenDisabled_spec_27_2(t *testing.T) {
	d := preflight.CheckPlaygroundConfig(preflight.PlaygroundConfig{
		Enabled:  false,
		AuthMode: "not-a-real-mode",
	})
	if !d.Passed {
		t.Fatalf("playground-config: disabled chart should pass, got %+v", d)
	}
	if d.Reason != "" {
		t.Fatalf("playground-config: disabled chart should be silent, got reason %q", d.Reason)
	}
}

// TestCheckPlaygroundConfigDevModeForbidden_F_27_2_5 exercises the
// §27.3 install-time gate: playground.authMode=dev requires
// global.devMode=true. The fix for F-27.2.5 surfaces the rejection at
// helm install rather than at pod start.
func TestCheckPlaygroundConfigDevModeForbidden_F_27_2_5(t *testing.T) {
	d := preflight.CheckPlaygroundConfig(preflight.PlaygroundConfig{
		Enabled:       true,
		AuthMode:      "dev",
		DevTenantID:   "alice",
		GlobalDevMode: false,
	})
	if d.Passed {
		t.Fatalf("playground-config: authMode=dev without global.devMode must fail, got pass")
	}
	if !strings.HasPrefix(d.Reason, "LENNY_PLAYGROUND_DEV_MODE_FORBIDDEN") {
		t.Fatalf("playground-config: want LENNY_PLAYGROUND_DEV_MODE_FORBIDDEN prefix, got %q", d.Reason)
	}
}

// TestCheckPlaygroundConfigDevTenantRequired_F_27_2_4 exercises the
// empty devTenantId rejection.
func TestCheckPlaygroundConfigDevTenantRequired_F_27_2_4(t *testing.T) {
	d := preflight.CheckPlaygroundConfig(preflight.PlaygroundConfig{
		Enabled:       true,
		AuthMode:      "dev",
		DevTenantID:   "",
		GlobalDevMode: true,
	})
	if d.Passed {
		t.Fatalf("playground-config: empty devTenantId must fail, got pass")
	}
	if !strings.HasPrefix(d.Reason, "LENNY_PLAYGROUND_DEV_TENANT_REQUIRED") {
		t.Fatalf("playground-config: want LENNY_PLAYGROUND_DEV_TENANT_REQUIRED prefix, got %q", d.Reason)
	}
}

// TestCheckPlaygroundConfigDevTenantInvalid_F_27_2_4 exercises the
// malformed devTenantId rejection (cannot include @ per the canonical
// §10.2 tenant_id format).
func TestCheckPlaygroundConfigDevTenantInvalid_F_27_2_4(t *testing.T) {
	d := preflight.CheckPlaygroundConfig(preflight.PlaygroundConfig{
		Enabled:       true,
		AuthMode:      "dev",
		DevTenantID:   "alice@acme.com",
		GlobalDevMode: true,
	})
	if d.Passed {
		t.Fatalf("playground-config: malformed devTenantId must fail, got pass")
	}
	if !strings.HasPrefix(d.Reason, "LENNY_PLAYGROUND_DEV_TENANT_INVALID") {
		t.Fatalf("playground-config: want LENNY_PLAYGROUND_DEV_TENANT_INVALID prefix, got %q", d.Reason)
	}
}

// TestCheckPlaygroundConfigMultiTenantDefaultRejected_F_27_2_6
// exercises the multi-tenant + default-tenant cross-field rejection.
func TestCheckPlaygroundConfigMultiTenantDefaultRejected_F_27_2_6(t *testing.T) {
	d := preflight.CheckPlaygroundConfig(preflight.PlaygroundConfig{
		Enabled:       true,
		AuthMode:      "dev",
		DevTenantID:   "default",
		MultiTenant:   true,
		GlobalDevMode: true,
	})
	if d.Passed {
		t.Fatalf("playground-config: multi-tenant + default must fail, got pass")
	}
	if !strings.HasPrefix(d.Reason, "LENNY_PLAYGROUND_DEV_TENANT_REQUIRED") {
		t.Fatalf("playground-config: want LENNY_PLAYGROUND_DEV_TENANT_REQUIRED prefix, got %q", d.Reason)
	}
}

// TestCheckPlaygroundAPIKeyModeWarning exercises the §27.9 line 255
// `playground.apiKeyMode` row: apiKey mode outside dev mode without an
// acknowledgement emits a non-blocking WARNING (passes install but
// carries the reason).
func TestCheckPlaygroundAPIKeyModeWarning_F_27_9_2(t *testing.T) {
	d := preflight.CheckPlaygroundAPIKeyMode(preflight.PlaygroundConfig{
		Enabled:               true,
		AuthMode:              "apiKey",
		GlobalDevMode:         false,
		AcknowledgeAPIKeyMode: false,
	})
	if !d.Passed {
		t.Fatalf("playground.apiKeyMode: apiKey warning must pass install, got fail: %s", d.Reason)
	}
	if !strings.HasPrefix(d.Reason, "WARNING") {
		t.Fatalf("playground.apiKeyMode: want WARNING prefix, got %q", d.Reason)
	}
	if !strings.Contains(d.Reason, "acknowledgeApiKeyMode") {
		t.Fatalf("playground.apiKeyMode: WARNING must name acknowledgeApiKeyMode knob, got %q", d.Reason)
	}
}

// TestCheckPlaygroundAPIKeyModeAckSuppresses exercises the
// acknowledgement: playground.acknowledgeApiKeyMode=true silences the row.
func TestCheckPlaygroundAPIKeyModeAckSuppresses_F_27_9_2(t *testing.T) {
	d := preflight.CheckPlaygroundAPIKeyMode(preflight.PlaygroundConfig{
		Enabled:               true,
		AuthMode:              "apiKey",
		GlobalDevMode:         false,
		AcknowledgeAPIKeyMode: true,
	})
	if !d.Passed || d.Reason != "" {
		t.Fatalf("playground.apiKeyMode: ack=true must pass silently, got passed=%v reason=%q", d.Passed, d.Reason)
	}
}

// TestCheckPlaygroundAPIKeyModeDevEscape exercises the global.devMode=true
// escape: a developer install can run apiKey mode without a warning
// because the §27.9 phishing surface only applies outside dev mode.
func TestCheckPlaygroundAPIKeyModeDevEscape_F_27_9_2(t *testing.T) {
	d := preflight.CheckPlaygroundAPIKeyMode(preflight.PlaygroundConfig{
		Enabled:               true,
		AuthMode:              "apiKey",
		GlobalDevMode:         true,
		AcknowledgeAPIKeyMode: false,
	})
	if !d.Passed || d.Reason != "" {
		t.Fatalf("playground.apiKeyMode: dev + apiKey must pass silently, got passed=%v reason=%q", d.Passed, d.Reason)
	}
}

// TestCheckPlaygroundAPIKeyModeDisabled exercises the §27.9 enable gate:
// when the playground is disabled the row never fires regardless of the
// other apiKey values, matching the spec's
// "playground.enabled=true AND ..." conjunction.
func TestCheckPlaygroundAPIKeyModeDisabled_F_27_9_2(t *testing.T) {
	d := preflight.CheckPlaygroundAPIKeyMode(preflight.PlaygroundConfig{
		Enabled:               false,
		AuthMode:              "apiKey",
		GlobalDevMode:         false,
		AcknowledgeAPIKeyMode: false,
	})
	if !d.Passed || d.Reason != "" {
		t.Fatalf("playground.apiKeyMode: disabled playground must pass silently, got passed=%v reason=%q", d.Passed, d.Reason)
	}
}

// TestCheckPlaygroundConfigNoLongerEmitsAPIKeyWarning pins the split: the
// structural playground-config row stays silent on the unacknowledged
// apiKey case (the WARNING moved to the playground.apiKeyMode row) so the
// warning is emitted exactly once. F-27.9.2.
func TestCheckPlaygroundConfigNoLongerEmitsAPIKeyWarning_F_27_9_2(t *testing.T) {
	d := preflight.CheckPlaygroundConfig(preflight.PlaygroundConfig{
		Enabled:               true,
		AuthMode:              "apiKey",
		GlobalDevMode:         false,
		AcknowledgeAPIKeyMode: false,
	})
	if !d.Passed || d.Reason != "" {
		t.Fatalf("playground-config: apiKey case must now pass silently, got passed=%v reason=%q", d.Passed, d.Reason)
	}
}

// TestCheckPlaygroundConfigOIDCHappy exercises the canonical oidc
// happy path: no warnings, no failures.
func TestCheckPlaygroundConfigOIDCHappy_F_27_2(t *testing.T) {
	d := preflight.CheckPlaygroundConfig(preflight.PlaygroundConfig{
		Enabled:  true,
		AuthMode: "oidc",
	})
	if !d.Passed {
		t.Fatalf("playground-config: oidc happy path must pass, got fail: %s", d.Reason)
	}
	if d.Reason != "" {
		t.Fatalf("playground-config: oidc happy path must be silent, got %q", d.Reason)
	}
}

// TestCheckPlaygroundConfigUnknownAuthMode exercises the catch-all
// AuthMode rejection.
func TestCheckPlaygroundConfigUnknownAuthMode_F_27_2(t *testing.T) {
	d := preflight.CheckPlaygroundConfig(preflight.PlaygroundConfig{
		Enabled:  true,
		AuthMode: "saml",
	})
	if d.Passed {
		t.Fatalf("playground-config: unknown authMode must fail, got pass")
	}
	if !strings.HasPrefix(d.Reason, "LENNY_PLAYGROUND_CONFIG_INVALID") {
		t.Fatalf("playground-config: want LENNY_PLAYGROUND_CONFIG_INVALID prefix, got %q", d.Reason)
	}
}
