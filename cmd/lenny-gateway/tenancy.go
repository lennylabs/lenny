// SPDX-License-Identifier: MIT

package main

import (
	"fmt"

	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
)

// Tenancy mode values of the --tenancy-mode flag, mirroring the
// tenancy.mode Helm value.
const (
	tenancyModeSingle = "single"
	tenancyModeMulti  = "multi"
)

// validateTenancyConfig refuses a gateway whose two tenancy inputs
// disagree. --multi-tenant (the auth.multiTenant Helm value) selects the
// §10.2 tenant-claim extraction table, and --tenancy-mode (the
// tenancy.mode Helm value) selects the §4.9 cross-tenant
// credential-delivery rejections. Both describe whether the deployment
// serves more than one tenant, so a gateway that enforced one posture on
// its auth chain and the other on credential delivery would run with
// undefined semantics. The gateway therefore fails closed at startup
// unless --tenancy-mode is "single" or "multi" and --multi-tenant is true
// exactly when --tenancy-mode is "multi".
//
// spec: §10.2 (`tenant_id` extraction); §4.9
// (tenancy.mode: multi credential-delivery rejections); §10.3 (gateway
// startup configuration validation).
func validateTenancyConfig(tenancyMode string, multiTenant bool) error {
	switch tenancyMode {
	case tenancyModeSingle, tenancyModeMulti:
	default:
		return fmt.Errorf("LENNY_CONFIG_INVALID config_key=tenancy.mode: --tenancy-mode must be %q or %q, got %q",
			tenancyModeSingle, tenancyModeMulti, tenancyMode)
	}
	if multiTenant != (tenancyMode == tenancyModeMulti) {
		return fmt.Errorf("LENNY_CONFIG_INVALID config_key=auth.multiTenant: --multi-tenant=%t contradicts --tenancy-mode=%s; "+
			"set auth.multiTenant: true with tenancy.mode: multi, or auth.multiTenant: false with tenancy.mode: single (§10.2, §4.9)",
			multiTenant, tenancyMode)
	}
	return nil
}

// applyTenantAuthOptions sets the §10.2 tenant-resolution fields of the
// auth middleware options from the gateway's tenancy and dev-mode flags.
//
// A multi-tenant gateway runs the full extraction table: the Bearer
// tenant claim must name a tenant the tenantstore holds as active, and an
// unregistered tenant is refused with 403 TENANT_NOT_FOUND. A
// single-tenant gateway ignores the claim and resolves every request to
// the built-in `default` tenant.
//
// The X-Lenny-Tenant-ID dev header follows the same rules, with one
// exception: in dev mode on a single-tenant gateway the header names the
// tenant directly, which the dev and integration suites rely on to
// exercise more than one tenant. Outside dev mode a single-tenant gateway
// resolves the header to `default` as well, and in multi-tenant mode the
// header is checked against the tenantstore like a Bearer claim.
//
// Before this helper existed the single-tenant branch switched the
// middleware to multi-tenant with a registry that admitted every tenant,
// so that the dev header round-tripped. That made a Bearer tenant claim
// select the tenant on every chart-deployed gateway, which §10.2 forbids
// in both modes (T-10.2.11).
//
// spec: §10.2 (`tenant_id` extraction); §17.4 (dev mode).
func applyTenantAuthOptions(opts *authmw.Options, multiTenant, devMode bool, tenants tenantstore.Store) {
	opts.MultiTenant = multiTenant
	opts.DevHeaderNamesTenant = devMode && !multiTenant
	opts.Registry = nil
	if multiTenant {
		opts.Registry = bearerTenantRegistry{store: tenants}
	}
}
