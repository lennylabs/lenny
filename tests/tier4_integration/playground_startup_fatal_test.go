// SPDX-License-Identifier: MIT

//go:build integration

// Tier-4 integration test: the §27.2 layer-3 gateway-startup backstop,
// exercised through the real cmd/lenny-gateway process boundary rather
// than a unit-level call to playground.Config.Validate(). The gateway's
// log.Fatalf backstop (cmd/lenny-gateway/httpsurface.go) runs strictly
// before playground.NewMetrics() is registered, so both fatal paths
// covered here exit before ever reaching the unrelated §16.1.1
// authMode-metrics-label crash-loop that blocks every other live-process
// playground test (see the "27" entry in tests/spec-map.json).
package tier4_integration_test

import (
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/tests/testinfra/gateway"
)

// spec: §27.2 ("`playground.devTenantId` validation layering", item 3 —
// "Backstop — gateway startup") — "Two fatal codes —
// `LENNY_PLAYGROUND_DEV_TENANT_INVALID` (devTenantId regex fails at
// startup) and `LENNY_PLAYGROUND_DEV_TENANT_REQUIRED` ... — remain as a
// defense-in-depth check for deployments that skipped preflight
// (`preflight.enabled: false`) or mutated the value after install".
//
// diagnosis: a failure means the real cmd/lenny-gateway binary either
// exited 0 (the fatal check regressed to a warning or was skipped) or
// exited without naming LENNY_PLAYGROUND_DEV_TENANT_INVALID, when
// booted with playground.authMode=dev and a devTenantId that fails the
// ^[a-zA-Z0-9_-]{1,128}$ format. Check the `pgCfg.Validate()` call in
// cmd/lenny-gateway/httpsurface.go and playground.Config.Validate in
// pkg/gateway/mcpfabric/playground/playground.go.
func TestPlaygroundStartupFatalOnMalformedDevTenantID_spec_27_2(t *testing.T) {
	res := gateway.RunToExit(
		t, 20*time.Second,
		"--dev-mode",
		"--no-environment-policy", "deny-all",
		"--playground-enabled",
		"--playground-auth-mode", "dev",
		"--playground-dev-tenant-id", "bad tenant",
	)
	if res.ExitCode == 0 {
		t.Fatalf("gateway exited 0 with a malformed playground.devTenantId; want a non-zero startup-fatal exit\nstderr:\n%s", res.Stderr)
	}
	if !strings.Contains(res.Stderr, "LENNY_PLAYGROUND_DEV_TENANT_INVALID") {
		t.Fatalf("gateway exit %d did not name LENNY_PLAYGROUND_DEV_TENANT_INVALID\nstderr:\n%s", res.ExitCode, res.Stderr)
	}
}

// spec: §27.3.1 ("Bearer token exchange" table, `dev` mode row) —
// "No admission material required (`global.devMode=true` gate is
// enforced at Helm-validate / startup per §27.2)"; see also §27.3:
// "`playground.authMode=dev`: no auth; only permitted when
// `global.devMode=true` (rejected at Helm-validate otherwise)".
//
// diagnosis: a failure means the real cmd/lenny-gateway binary either
// exited 0 (the startup backstop regressed) or exited without naming
// LENNY_PLAYGROUND_DEV_MODE_FORBIDDEN, when booted with
// playground.authMode=dev and global.devMode=false. Check the
// `pgCfg.AuthMode == playground.AuthModeDev && !*devMode` guard in
// cmd/lenny-gateway/httpsurface.go, which is the process-boot backstop
// for a deployment that bypassed Helm-validate.
func TestPlaygroundStartupFatalOnDevAuthModeWithoutDevMode_spec_27_3(t *testing.T) {
	res := gateway.RunToExit(
		t, 20*time.Second,
		"--dev-mode=false",
		"--tls-terminated-upstream=true",
		"--oidc-issuer-url", "https://idp.example.invalid",
		"--oidc-client-id", "test-client",
		"--no-environment-policy", "deny-all",
		"--playground-enabled",
		"--playground-auth-mode", "dev",
		"--playground-dev-tenant-id", "default",
	)
	if res.ExitCode == 0 {
		t.Fatalf("gateway exited 0 with playground.authMode=dev and global.devMode=false; want a non-zero startup-fatal exit\nstderr:\n%s", res.Stderr)
	}
	if !strings.Contains(res.Stderr, "LENNY_PLAYGROUND_DEV_MODE_FORBIDDEN") {
		t.Fatalf("gateway exit %d did not name LENNY_PLAYGROUND_DEV_MODE_FORBIDDEN\nstderr:\n%s", res.ExitCode, res.Stderr)
	}
}
