// SPDX-License-Identifier: MIT

//go:build e2e_kind

// Tier-5 e2e Kind test for the §4.9 multi-tenant credential-delivery
// admission rejections driven through a live AdmissionReview.
//
// Two credential-delivery configurations are blocked by the fail-closed
// lenny-direct-mode-isolation ValidatingAdmissionWebhook when the
// platform runs tenancy.mode: multi with global.devMode: false:
//
//   - deliveryMode: direct with isolationProfile: standard — a runc
//     container escape reaches materialized credential material on the
//     host node, which in a multi-tenant cluster is cross-tenant
//     credential exposure. Rejected with
//     DirectModeStandardIsolationMultiTenantRejected.
//   - deliveryMode: proxy with spiffeBinding: disabled — disabling
//     SPIFFE-binding removes the defense against cross-pod lease-token
//     replay. Rejected with ProxyModeSpiffeBindingDisabledMultiTenantRejected.
//
// The webhook enforces these two rules only in multi-tenant,
// non-development mode; in single-tenant or development mode it admits
// every SandboxTemplate. The chart wires the webhook Deployment's
// enforcement posture from tenancy.mode and global.devMode
// (charts/lenny/templates/admission-policies/_webhook.tpl passes
// --tenancy-mode and --dev-mode). The shared e2e install
// (tests/testinfra/kind/e2e-values.yaml) runs tenancy.mode: single with
// global.devMode: true, so the deployed webhook admits both
// combinations there and this test cannot observe a rejection.
//
// This test therefore reads the deployed webhook's enforcement flags and
// runs the rejection assertions only against an install whose
// lenny-direct-mode-isolation Deployment is configured to enforce
// (tenancy-mode=multi, dev-mode=false). On the shared single-tenant/dev
// install it skips. The webhook's decision logic and its
// HTTP/AdmissionReview transport are additionally covered in-process for
// both combinations by the pkg/admission/direct_mode_isolation and
// pkg/admission/webhook unit suites; this test exercises the deployed,
// chart-wired webhook end to end when an enforcing install is present.

package tier5_e2e_kind_test

import (
	"strings"
	"testing"

	"github.com/lennylabs/lenny/tests/testinfra/kind"
)

// directModeWebhookEnforcementFlags reads the --tenancy-mode and
// --dev-mode flags the chart wires onto the lenny-direct-mode-isolation
// webhook Deployment. The webhook enforces the §4.9 multi-tenant
// credential-delivery rejections only when tenancy-mode is "multi" and
// dev-mode is not "true".
func directModeWebhookEnforcementFlags(t *testing.T, c *kind.Cluster) (tenancyMode, devMode string) {
	t.Helper()
	args := webhookDeploymentArgs(t, c, "lenny-direct-mode-isolation")
	for _, line := range strings.Split(args, "\n") {
		line = strings.TrimSpace(line)
		if v, ok := strings.CutPrefix(line, "--tenancy-mode="); ok {
			tenancyMode = v
		}
		if v, ok := strings.CutPrefix(line, "--dev-mode="); ok {
			devMode = v
		}
	}
	return tenancyMode, devMode
}

// spec: 4.9 (direct/standard and proxy/spiffe-disabled multi-tenant rejections)
// diagnosis: the §4.9 fail-closed lenny-direct-mode-isolation webhook does
// not reject the two cross-tenant-risk credential-delivery combinations on a
// deployed multi-tenant install. The test drives two live AdmissionReviews
// through the API server against the deployed webhook: a SandboxTemplate with
// deliveryMode: direct + isolationProfile: standard, and one with
// deliveryMode: proxy + spiffeBinding: disabled. Both must be denied by
// direct-mode-isolation.lenny.dev with the documented rejection codes. A
// failure means the deployed webhook admitted a configuration that exposes
// materialized credentials to a runc escape or removes the cross-pod
// lease-token replay defense in a multi-tenant deployment. The rejection
// enforces only under tenancy.mode: multi with global.devMode: false, so the
// test skips on the shared single-tenant/dev install rather than reporting a
// spurious pass.
func TestAdmissionDirectModeIsolationMultiTenantRejections(t *testing.T) {
	c := kind.InstallLenny(t)

	const webhook = "lenny-direct-mode-isolation"
	assertFeatureGatedWebhookPresent(t, c, webhook, "the §13.2 baseline (renders unconditionally)")

	tenancyMode, devMode := directModeWebhookEnforcementFlags(t, c)
	if tenancyMode != "multi" || devMode == "true" {
		t.Skipf("the deployed %s webhook is configured tenancy-mode=%q dev-mode=%q; the §4.9 "+
			"direct/standard and proxy/spiffe-disabled rejections enforce only under tenancy.mode: multi "+
			"with global.devMode: false. The shared e2e install runs single-tenant/dev mode, so a "+
			"multi-tenant install overlay is required to drive these rejections end to end.",
			webhook, tenancyMode, devMode)
	}

	cases := []struct {
		name     string
		template string
		wantCode string
	}{
		{
			// deliveryMode: direct + isolationProfile: standard.
			name: "direct-mode-standard-isolation",
			template: `apiVersion: lenny.dev/v1alpha1
kind: SandboxTemplate
metadata:
  name: e2e-mt-direct-standard
  namespace: lenny-agents
spec:
  runtimeRef: e2e-nonexistent-runtime
  deliveryMode: direct
  isolationProfile: standard
`,
			wantCode: "DirectModeStandardIsolationMultiTenantRejected",
		},
		{
			// deliveryMode: proxy + spiffeBinding: disabled.
			name: "proxy-mode-spiffe-binding-disabled",
			template: `apiVersion: lenny.dev/v1alpha1
kind: SandboxTemplate
metadata:
  name: e2e-mt-proxy-spiffe-disabled
  namespace: lenny-agents
spec:
  runtimeRef: e2e-nonexistent-runtime
  deliveryMode: proxy
  isolationProfile: sandboxed
  spiffeBinding: disabled
`,
			wantCode: "ProxyModeSpiffeBindingDisabledMultiTenantRejected",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// A server-side dry-run runs the full admission chain (the
			// direct-mode-isolation webhook fires) without persisting the
			// object, matching the positive-control pattern in
			// TestAdmissionDirectModeIsolation.
			out, err := dryRunApply(t, c, tc.template)
			if err == nil {
				t.Fatalf("API server admitted a §4.9 %s SandboxTemplate in multi-tenant mode; "+
					"the %s webhook did not reject it.\noutput:\n%s", tc.name, webhook, out)
			}
			if !strings.Contains(out, "direct-mode-isolation.lenny.dev") {
				t.Fatalf("rejection did not come from the direct-mode-isolation webhook.\noutput:\n%s", out)
			}
			if !strings.Contains(out, tc.wantCode) {
				t.Errorf("rejection lacks the §4.9 %s code.\noutput:\n%s", tc.wantCode, out)
			}
			t.Logf("%s rejected the %s SandboxTemplate: %s", webhook, tc.name, strings.TrimSpace(out))
		})
	}
}
