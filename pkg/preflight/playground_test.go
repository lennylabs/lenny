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

// TestCheckPlaygroundConfigDevModeForbidden_spec_27_2 exercises the
// §27.3 install-time gate: playground.authMode=dev requires
// global.devMode=true. The preflight check surfaces the rejection at
// helm install rather than at pod start.
//
// spec: §27.3 ("`playground.authMode=dev`: no auth; only permitted when
// `global.devMode=true`"), §17.6 (the `lenny-preflight` playground rows).
func TestCheckPlaygroundConfigDevModeForbidden_spec_27_2(t *testing.T) {
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

// TestCheckPlaygroundConfigDevTenantRequired_spec_27_2 exercises the
// empty devTenantId rejection.
//
// spec: §17.6 ("when `playground.authMode: dev`, verify the value is
// non-empty"), §27.3 (the `LENNY_PLAYGROUND_DEV_TENANT_REQUIRED` code).
func TestCheckPlaygroundConfigDevTenantRequired_spec_27_2(t *testing.T) {
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

// TestCheckPlaygroundConfigDevTenantInvalid_spec_27_2 exercises the
// malformed devTenantId rejection (cannot include @ per the canonical
// §10.2 tenant_id format).
//
// spec: §17.6 ("verify `playground.devTenantId` (if set) matches
// `^[a-zA-Z0-9_-]{1,128}$`"), §10.2 (canonical tenant_id format), §27.2.
func TestCheckPlaygroundConfigDevTenantInvalid_spec_27_2(t *testing.T) {
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

// TestCheckPlaygroundConfigMultiTenantDefaultRejected_spec_27_2
// exercises the multi-tenant + default-tenant cross-field rejection.
//
// spec: §17.6 ("when `playground.authMode: dev` **and**
// `auth.multiTenant: true`, reject `playground.devTenantId: default`"),
// §27.3 (the same Helm-validate rule).
func TestCheckPlaygroundConfigMultiTenantDefaultRejected_spec_27_2(t *testing.T) {
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
//
// spec: §17.6 (the `playground.apiKeyMode` (warning) row), §27.9 (the
// paste-form phishing surface the acknowledgement covers).
func TestCheckPlaygroundAPIKeyModeWarning_spec_27_9(t *testing.T) {
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
//
// spec: §27.2 (`playground.acknowledgeApiKeyMode` — "emits a non-blocking
// `WARNING` unless this value is `true`"), §17.6.
func TestCheckPlaygroundAPIKeyModeAckSuppresses_spec_27_9(t *testing.T) {
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
//
// spec: §17.6 (the row fires only "when ... `global.devMode: false`"), §27.9.
func TestCheckPlaygroundAPIKeyModeDevEscape_spec_27_9(t *testing.T) {
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
//
// spec: §17.6 (the row fires only "when `playground.enabled: true` **and**
// `playground.authMode: apiKey` ..."), §27.9.
func TestCheckPlaygroundAPIKeyModeDisabled_spec_27_9(t *testing.T) {
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
// warning is emitted exactly once.
//
// spec: §17.6 (the apiKey WARNING belongs to the `playground.apiKeyMode`
// row, distinct from the `playground.devTenantId` format-and-presence row).
func TestCheckPlaygroundConfigNoLongerEmitsAPIKeyWarning_spec_27_2(t *testing.T) {
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
//
// spec: §27.3 (the `oidc` auth mode), §17.6.
func TestCheckPlaygroundConfigOIDCHappy_spec_27_2(t *testing.T) {
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
//
// spec: §27.3 (the closed set of playground auth modes), §17.6.
func TestCheckPlaygroundConfigUnknownAuthMode_spec_27_2(t *testing.T) {
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
