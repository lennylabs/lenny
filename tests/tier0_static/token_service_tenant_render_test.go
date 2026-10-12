// SPDX-License-Identifier: MIT

package tier0_static

import (
	"strings"
	"testing"

	"github.com/lennylabs/lenny/tests/testinfra/helm"
)

// tokenServiceTenantFlag is the gateway flag that names the fallback
// tenant the §4.3 Token Service client carries on a request whose lease
// names none.
const tokenServiceTenantFlag = "--token-service-tenant"

// argValue returns the value of the first `--flag=value` arg in args and
// whether the flag was present.
func argValue(args []string, flag string) (string, bool) {
	for _, a := range args {
		if v, ok := strings.CutPrefix(a, flag+"="); ok {
			return v, true
		}
		if a == flag {
			return "", true
		}
	}
	return "", false
}

// TestGatewayTokenServiceTenantRenderAcrossProfiles renders the chart
// under every supported values profile and both tenancy modes, and
// asserts the gateway's Token Service tenant wiring for each:
//
//   - With the Token Service enabled in single-tenant mode, the gateway
//     renders --token-service-tenant=default, the §4.2 built-in tenant
//     every single-tenant request is assigned to. Without it every
//     RevokeCredentials call carried an empty tenant and was rejected,
//     so no terminal lease release freed a pool slot.
//   - With the Token Service enabled in multi-tenant mode, the gateway
//     renders no fallback tenant, because each lease carries its own.
//   - With the Token Service disabled (the §17.4 dev profile), the
//     gateway renders neither the Token Service address nor a tenant.
//
// spec: §4.2 (Session Manager, tenant_id claim extraction), §4.3 (Token
// Service), §7.1 (Normal Flow, step 23), §17.4 (Local Development Mode).
func TestGatewayTokenServiceTenantRenderAcrossProfiles(t *testing.T) {
	helm.SkipUnlessAvailable(t)

	cases := []struct {
		name       string
		values     string
		set        []string
		wantTS     bool
		wantTenant string // "" means the flag must be absent
	}{
		{name: "chart defaults", wantTS: true, wantTenant: "default"},
		{name: "chart defaults, explicit single tenancy", set: []string{"tenancy.mode=single"}, wantTS: true, wantTenant: "default"},
		{name: "chart defaults, multi tenancy", set: []string{"tenancy.mode=multi"}, wantTS: true},
		{name: "chart defaults, token service disabled", set: []string{"tokenService.enabled=false"}},
		{name: "chart defaults, token service disabled, multi tenancy", set: []string{"tokenService.enabled=false", "tenancy.mode=multi"}},
		{name: "tier1 preset", values: "../../charts/lenny/presets/values-tier1.yaml", wantTS: true, wantTenant: "default"},
		{name: "tier1 preset, multi tenancy", values: "../../charts/lenny/presets/values-tier1.yaml", set: []string{"tenancy.mode=multi"}, wantTS: true},
		{name: "tier2 preset", values: "../../charts/lenny/presets/values-tier2.yaml", wantTS: true, wantTenant: "default"},
		{name: "tier2 preset, multi tenancy", values: "../../charts/lenny/presets/values-tier2.yaml", set: []string{"tenancy.mode=multi"}, wantTS: true},
		{name: "tier3 preset", values: "../../charts/lenny/presets/values-tier3.yaml", wantTS: true, wantTenant: "default"},
		{name: "tier3 preset, multi tenancy", values: "../../charts/lenny/presets/values-tier3.yaml", set: []string{"tenancy.mode=multi"}, wantTS: true},
		{name: "dev preset", values: devProfilePath},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := helm.Render(t, helm.Options{
				Chart:     "../../charts/lenny",
				Release:   "lenny",
				Namespace: "lenny-system",
				Values:    tc.values,
				Set:       renderArgs(tc.set...),
			})
			args := gatewayArgs(t, m)

			if got := hasArg(args, "--token-service-grpc-addr"); got != tc.wantTS {
				t.Fatalf("--token-service-grpc-addr rendered = %v, want %v", got, tc.wantTS)
			}
			tenant, present := argValue(args, tokenServiceTenantFlag)
			switch {
			case tc.wantTenant == "" && present:
				t.Errorf("%s rendered as %q; want it absent for this profile", tokenServiceTenantFlag, tenant)
			case tc.wantTenant != "" && !present:
				t.Errorf("%s is not rendered; the Token Service is enabled in single-tenant mode, so every lease release would carry an empty tenant", tokenServiceTenantFlag)
			case tc.wantTenant != "" && tenant != tc.wantTenant:
				t.Errorf("%s = %q, want %q", tokenServiceTenantFlag, tenant, tc.wantTenant)
			}
			// The fallback tenant must never render empty: an explicit
			// empty value is the defect this test guards.
			if present && tenant == "" {
				t.Errorf("%s rendered with an empty value", tokenServiceTenantFlag)
			}
		})
	}
}
