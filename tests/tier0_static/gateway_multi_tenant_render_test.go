// SPDX-License-Identifier: MIT

package tier0_static

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/tests/testinfra/helm"
)

// multiTenantFlag is the gateway flag the chart renders from the §10.2
// auth.multiTenant value.
const multiTenantFlag = "--multi-tenant"

// TestGatewayMultiTenantFlagRenderAcrossProfiles renders the chart under
// every supported values profile in both tenancy modes and asserts the
// gateway container carries --multi-tenant equal to auth.multiTenant, and
// that it agrees with --tenancy-mode. Before T-10.2.11 the chart rendered
// --tenancy-mode but never --multi-tenant, so every chart-deployed gateway,
// including a tenancy.mode: multi install, ran with the flag's false
// default and admitted an unregistered tenant claim.
//
// spec: §10.2 (Authentication, `tenant_id` extraction),
// §4.9 (tenancy.mode: multi), §17.4 (Local Development Mode).
func TestGatewayMultiTenantFlagRenderAcrossProfiles(t *testing.T) {
	helm.SkipUnlessAvailable(t)

	multi := []string{"tenancy.mode=multi", "auth.multiTenant=true"}
	cases := []struct {
		name   string
		values string
		set    []string
		want   string
	}{
		{name: "chart defaults", want: "false"},
		{name: "chart defaults, multi tenancy", set: multi, want: "true"},
		{name: "tier1 preset", values: "../../charts/lenny/presets/values-tier1.yaml", want: "false"},
		{name: "tier1 preset, multi tenancy", values: "../../charts/lenny/presets/values-tier1.yaml", set: multi, want: "true"},
		{name: "tier2 preset", values: "../../charts/lenny/presets/values-tier2.yaml", want: "false"},
		{name: "tier2 preset, multi tenancy", values: "../../charts/lenny/presets/values-tier2.yaml", set: multi, want: "true"},
		{name: "tier3 preset", values: "../../charts/lenny/presets/values-tier3.yaml", want: "false"},
		{name: "tier3 preset, multi tenancy", values: "../../charts/lenny/presets/values-tier3.yaml", set: multi, want: "true"},
		{name: "dev preset", values: devProfilePath, want: "false"},
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
			got, present := argValue(args, multiTenantFlag)
			if !present {
				t.Fatalf("%s is not rendered on the gateway container; the gateway would run with its false default and admit an unregistered tenant claim", multiTenantFlag)
			}
			if got != tc.want {
				t.Errorf("%s = %q, want %q", multiTenantFlag, got, tc.want)
			}
			mode, _ := argValue(args, "--tenancy-mode")
			if (mode == "multi") != (got == "true") {
				t.Errorf("%s=%s disagrees with --tenancy-mode=%s; the gateway refuses this pair at startup", multiTenantFlag, got, mode)
			}
		})
	}
}

// TestChartRefusesContradictoryTenancy asserts `helm template` fails when
// auth.multiTenant and tenancy.mode disagree, in both directions, so a
// tenancy.mode: multi install cannot render a gateway that ignores the
// tenant claim, and a single-tenant install cannot render a gateway that
// enforces it.
//
// spec: §10.2 (Authentication, `tenant_id` extraction),
// §4.9 (tenancy.mode: multi).
func TestChartRefusesContradictoryTenancy(t *testing.T) {
	helm.SkipUnlessAvailable(t)

	cases := []struct {
		name    string
		set     []string
		wantErr string
	}{
		{
			name:    "multi tenancy without auth.multiTenant",
			set:     []string{"tenancy.mode=multi"},
			wantErr: "auth.multiTenant=false contradicts tenancy.mode=multi",
		},
		{
			name:    "auth.multiTenant under single tenancy",
			set:     []string{"auth.multiTenant=true"},
			wantErr: "auth.multiTenant=true contradicts tenancy.mode=single",
		},
		{
			name:    "explicit false under multi tenancy",
			set:     []string{"tenancy.mode=multi", "auth.multiTenant=false"},
			wantErr: "auth.multiTenant=false contradicts tenancy.mode=multi",
		},
		{
			name:    "unknown tenancy mode",
			set:     []string{"tenancy.mode=shared"},
			wantErr: "tenancy.mode=shared must be single or multi",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			args := []string{"template", "lenny", "../../charts/lenny", "--namespace", "lenny-system"}
			for _, kv := range renderArgs(tc.set...) {
				args = append(args, "--set", kv)
			}
			out, err := exec.Command("helm", args...).CombinedOutput()
			if err == nil {
				t.Fatalf("helm template with %v rendered; want a refusal naming %q", tc.set, tc.wantErr)
			}
			if !strings.Contains(string(out), tc.wantErr) {
				t.Fatalf("helm template with %v failed without naming %q:\n%s", tc.set, tc.wantErr, out)
			}
		})
	}
}
