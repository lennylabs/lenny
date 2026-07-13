// SPDX-License-Identifier: MIT

//go:build e2e_kind

// Tier-5 e2e Kind test that replays the §7.1 prompt round-trip journey
// across the gateway's client-facing auth modes on a live cluster.
//
// Every other clustered session test authenticates with the
// X-Lenny-Tenant-ID / X-Lenny-Roles / X-Lenny-User-ID dev headers, so
// the full journey (create -> start -> message -> AttachSession stream
// echo) had only ever been proven under dev-mode auth.
// TestStandardBearerGatesSessionCreation extended the bearer chain to
// session create/read/delete but stopped short of the prompt round-trip
// itself. This test drives the whole journey once per auth mode -- dev
// headers and the standard §10.2 `Authorization: Bearer` chain -- so the
// pod data path is exercised under a non-dev credential as well. §27.3
// confirms the `oidc` and `apiKey` playground auth modes both hand the
// gateway that same standard bearer, so the bearer cell stands in for
// both without an external OIDC identity provider.
package tier5_e2e_kind_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/lennylabs/lenny/tests/testinfra/kind"
	"github.com/lennylabs/lenny/tests/testinfra/matrix"
	"github.com/lennylabs/lenny/tests/testinfra/sessiondriver"
)

// spec: §10.2 (spec/10_gateway-internals.md, Authentication) "Authorization:
// Bearer <jwt> — the canonical RFC 6750 path"; §27.3
// (spec/27_web-playground.md) "`playground.authMode=apiKey`: ... The token is
// an OIDC ID token or service-account bearer token — the same credential
// accepted on the standard Client→Gateway or Automated-clients auth paths
// in §10.2"; §7.1 (spec/07_session-lifecycle.md, Normal Flow) steps 16-18
// (AttachSession / bidirectional stream proxy / full interactive session).
//
// diagnosis: a failure means the §7.1 prompt round-trip data path is not
// auth-mode-agnostic on a live cluster. If the "dev" cell fails, the
// baseline dev-header journey regressed. If the "bearer" cell fails, the
// standard §10.2 Bearer chain -- which §27.3 says backs both the `oidc`
// and `apiKey` playground modes -- either rejects a valid session journey
// or cannot carry a prompt onto a real pod and back, so the only proven
// full-journey auth mode is the dev-header shortcut.
func TestPromptJourneyAcrossAuthModes(t *testing.T) {
	c := kind.InstallLenny(t)

	matrix.Run(t, matrix.Dim("auth", []string{"dev", "bearer"}))(
		func(t *testing.T, cell map[string]string) {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()

			switch cell["auth"] {
			case "dev":
				// Baseline: the X-Lenny-* dev-header identity every other
				// clustered session test uses, driven on a fresh synthetic
				// tenant so the cell owns its own cleanup.
				d := sessiondriver.New(t)
				tenant := uniqueAuthJourneyTenant()
				if err := d.BootstrapTenant(ctx, tenant); err != nil {
					t.Fatalf("bootstrap tenant: %v", err)
				}
				runEchoPromptJourney(ctx, t, d, tenant)

			case "bearer":
				// Standard §10.2 Bearer chain. The bearer is the freshly
				// rotated §17.6 admin credential, whose tenant_id claim is
				// the built-in "default" tenant, so the journey runs in
				// "default": seed its audit/billing sequences and relax its
				// §10.6 no-environment policy through the dev-header admin
				// path (setup driver), then drive the journey itself through
				// a second driver constructed to present the bearer on the
				// session surface.
				setup := sessiondriver.New(t)
				ensureDefaultTenantSeeded(t, setup)
				ensureDefaultTenantAllowsSessionsWithNoEnvironment(t, setup)
				bearer := freshAdminBearer(t, setup, c)

				d := sessiondriver.New(t, sessiondriver.Options{SessionBearer: bearer})
				runEchoPromptJourney(ctx, t, d, "default")

			default:
				t.Fatalf("unknown auth cell %q", cell["auth"])
			}
		})
}

// uniqueAuthJourneyTenant returns a per-run synthetic tenant id that
// satisfies the §10.2 tenant_id format `^[a-zA-Z0-9_-]{1,128}$`. The
// sessiondriver best-effort deletes it on Close; the nanosecond suffix
// sidesteps a stale tenant left in the deleted state by a prior run on
// this persistent e2e cluster, matching promptRoundtripTenant's pattern.
func uniqueAuthJourneyTenant() string {
	return fmt.Sprintf("auth-journey-tenant-%d", time.Now().UnixNano())
}
