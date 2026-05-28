// SPDX-License-Identifier: MIT

package preflight

import (
	"fmt"
	"regexp"
	"strings"
)

// PlaygroundConfig is the subset of chart values the §27.2
// playground-config preflight check evaluates. The lenny-preflight
// Job renders them from chart values; the check is a pure function
// over them so it is unit-testable without a live cluster.
type PlaygroundConfig struct {
	// Enabled mirrors playground.enabled. When false every other
	// playground.* value is unread and the check passes trivially.
	Enabled bool
	// AuthMode mirrors playground.authMode (oidc | apiKey | dev).
	AuthMode string
	// DevTenantID mirrors playground.devTenantId.
	DevTenantID string
	// MultiTenant mirrors auth.multiTenant.
	MultiTenant bool
	// GlobalDevMode mirrors global.devMode.
	GlobalDevMode bool
	// AcknowledgeAPIKeyMode mirrors
	// playground.acknowledgeApiKeyMode (§27.2 line 42).
	AcknowledgeAPIKeyMode bool
}

// playgroundTenantIDPattern is the canonical §10.2 tenant_id format,
// duplicated from pkg/gateway/playground/playground.go so the
// preflight package does not import the gateway.
var playgroundTenantIDPattern = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,128}$`)

// CheckPlaygroundConfig is the §27.2 layer-2 cross-field check the
// lenny-preflight Job runs at install time. It rejects the
// combinations that the gateway's layer-3 startup gate
// (pkg/gateway/playground.Config.Validate) would otherwise catch only
// after the pod has started, plus the apiKey-mode acknowledgement
// warning that has no startup analogue. Failures use the §27.2 fatal
// codes as their prefix so an operator sees the same envelope at
// install time and at startup.
//
// spec: §27.2 lines 41–42 + §27.3 ("rejected at Helm-validate
// otherwise").
func CheckPlaygroundConfig(cfg PlaygroundConfig) Decision {
	if !cfg.Enabled {
		return Decision{Passed: true}
	}
	switch strings.TrimSpace(cfg.AuthMode) {
	case "", "oidc", "apiKey", "dev":
	default:
		return Decision{
			Passed: false,
			Reason: fmt.Sprintf("LENNY_PLAYGROUND_CONFIG_INVALID: playground.authMode %q is not one of oidc, apiKey, dev", cfg.AuthMode),
		}
	}
	if cfg.AuthMode == "dev" {
		// spec: §27.3 "no auth; only permitted when global.devMode=true
		// (rejected at Helm-validate otherwise)". The runtime backstop
		// at cmd/lenny-gateway/main.go is the third layer; this
		// preflight row is the install-time gate. F-27.2.5.
		if !cfg.GlobalDevMode {
			return Decision{
				Passed: false,
				Reason: "LENNY_PLAYGROUND_DEV_MODE_FORBIDDEN: playground.authMode=dev requires global.devMode=true (§27.3)",
			}
		}
		// spec: §27.2 layer-3 startup gate, layered into install time.
		// F-27.2.4.
		if strings.TrimSpace(cfg.DevTenantID) == "" {
			return Decision{
				Passed: false,
				Reason: "LENNY_PLAYGROUND_DEV_TENANT_REQUIRED: playground.authMode=dev requires a non-empty playground.devTenantId",
			}
		}
		if !playgroundTenantIDPattern.MatchString(cfg.DevTenantID) {
			return Decision{
				Passed: false,
				Reason: fmt.Sprintf("LENNY_PLAYGROUND_DEV_TENANT_INVALID: playground.devTenantId %q fails the tenant_id format ^[a-zA-Z0-9_-]{1,128}$", cfg.DevTenantID),
			}
		}
		// spec: §27.2 "auth.multiTenant=true forbids the default
		// devTenantId when multiple tenants are seeded". The chart-side
		// rejection cross-references the same backstop. F-27.2.6.
		if cfg.MultiTenant && cfg.DevTenantID == "default" {
			return Decision{
				Passed: false,
				Reason: "LENNY_PLAYGROUND_DEV_TENANT_REQUIRED: playground.authMode=dev with auth.multiTenant=true requires an explicit (non-default) playground.devTenantId",
			}
		}
	}
	// spec: §27.2 line 42 / §27.9 — apiKey mode outside dev mode
	// requires the operator to set playground.acknowledgeApiKeyMode
	// (same pattern as monitoring.acknowledgeNoPrometheus). The check
	// is non-blocking: a missing acknowledgement surfaces as a passing
	// WARNING so the install does not abort, matching the spec's
	// "non-blocking WARNING" wording. F-27.2.2 / F-27.9.3.
	if cfg.AuthMode == "apiKey" && !cfg.GlobalDevMode && !cfg.AcknowledgeAPIKeyMode {
		return Decision{
			Passed: true,
			Reason: "WARNING: playground.authMode=apiKey is enabled outside global.devMode; set playground.acknowledgeApiKeyMode=true to acknowledge the §27.9 paste-form phishing surface (same pattern as monitoring.acknowledgeNoPrometheus)",
		}
	}
	return Decision{Passed: true}
}
