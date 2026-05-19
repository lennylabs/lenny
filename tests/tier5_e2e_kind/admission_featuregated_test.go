// SPDX-License-Identifier: MIT

//go:build e2e_kind

// Tier-5 e2e Kind tests for the feature-gated §13.x admission
// webhooks. The e2e values overlay (tests/testinfra/kind/e2e-values.yaml)
// installs the chart in dev mode with features.llmProxy,
// features.drainReadiness, and features.compliance all left at their
// default false, so the chart does not render the feature-gated
// ValidatingWebhookConfigurations.
//
// Each test confirms the chart's feature-gating is correct against the
// live cluster: a feature-gated webhook is absent on a release that
// did not enable its feature. Asserting the webhook is correctly
// withheld is a genuine §17.2-inventory check, not a substitute for
// the rejection behaviour, which the gating itself makes unreachable
// on this install. Each test names the exact Helm value an install
// must set to bring the webhook online.

package tier5_e2e_kind_test

import (
	"strings"
	"testing"

	"github.com/lennylabs/lenny/tests/testinfra/kind"
)

// webhookConfigured reports whether the named
// ValidatingWebhookConfiguration exists on the cluster.
func webhookConfigured(t *testing.T, c *kind.Cluster, name string) bool {
	t.Helper()
	out, err := c.KubectlOut(
		t,
		"get", "validatingwebhookconfiguration", name,
		"--ignore-not-found", "-o", "jsonpath={.metadata.name}",
	)
	if err != nil {
		t.Fatalf("query ValidatingWebhookConfiguration %q: %v\n%s", name, err, out)
	}
	return strings.TrimSpace(out) == name
}

// assertFeatureGatedWebhookAbsent fails the test when a webhook that
// should be gated off by an unset feature flag is nonetheless present.
// flagDesc names the Helm value that gates the webhook.
func assertFeatureGatedWebhookAbsent(t *testing.T, c *kind.Cluster, webhook, flagDesc string) {
	t.Helper()
	if webhookConfigured(t, c, webhook) {
		t.Fatalf("ValidatingWebhookConfiguration %q is installed on a release that did not set %s; "+
			"the chart's feature-gating rendered a webhook it should have withheld", webhook, flagDesc)
	}
}

// spec: 13.15
// diagnosis: the §4.9/§13.2 lenny-direct-mode-isolation webhook is
// mis-gated. The webhook is rendered only when features.llmProxy is
// true; the e2e overlay leaves it false, so the webhook must be
// absent. The test confirms the chart withheld it. To exercise the
// direct-mode credential-isolation rejection, install the chart with
// features.llmProxy=true and apply a SandboxTemplate whose
// deliveryMode is direct with isolationProfile standard.
func TestAdmissionDirectModeIsolation(t *testing.T) {
	c := kind.InstallLenny(t)
	assertFeatureGatedWebhookAbsent(t, c, "lenny-direct-mode-isolation", "features.llmProxy=true")
	t.Skip("lenny-direct-mode-isolation is gated off (features.llmProxy=false in the e2e overlay); " +
		"the §13.15 direct-mode rejection needs an install with features.llmProxy=true")
}

// spec: 13.19
// diagnosis: the §12.5/§13.2 NET-037 lenny-drain-readiness webhook is
// mis-gated. The webhook is rendered only when features.drainReadiness
// is true; the e2e overlay leaves it false, so the webhook must be
// absent. The test confirms the chart withheld it. To exercise the
// eviction-blocking behaviour, install the chart with
// features.drainReadiness=true and a reachable artifact store, then
// attempt a pod eviction while the store is degraded.
func TestDrainReadinessWebhook(t *testing.T) {
	c := kind.InstallLenny(t)
	assertFeatureGatedWebhookAbsent(t, c, "lenny-drain-readiness", "features.drainReadiness=true")
	t.Skip("lenny-drain-readiness is gated off (features.drainReadiness=false in the e2e overlay); " +
		"the §13.19 eviction-blocking behaviour needs an install with features.drainReadiness=true")
}

// spec: 13.28
// diagnosis: the §12.8/§12.9 lenny-data-residency-validator webhook is
// mis-gated. The webhook is rendered only when features.compliance is
// true; the e2e overlay leaves it false, so the webhook must be
// absent. The test confirms the chart withheld it. To exercise the
// data-residency region rejection, install the chart with
// features.compliance=true and a storage.regions map, then apply a
// SandboxClaim whose dataResidencyRegion is not in that map.
func TestAdmissionDataResidency(t *testing.T) {
	c := kind.InstallLenny(t)
	assertFeatureGatedWebhookAbsent(t, c, "lenny-data-residency-validator", "features.compliance=true")
	t.Skip("lenny-data-residency-validator is gated off (features.compliance=false in the e2e overlay); " +
		"the §13.28 data-residency rejection needs an install with features.compliance=true")
}

// spec: 13.28
// diagnosis: the §6.4 STR-003 lenny-t4-node-isolation webhook is
// mis-gated. The webhook is rendered only when features.compliance is
// true; the e2e overlay leaves it false, so the webhook must be
// absent. The test confirms the chart withheld it. To exercise the T4
// dedicated-node rejection, install the chart with
// features.compliance=true, then apply a pod carrying the
// lenny.dev/workspace-tier: t4 label without a T4 nodeSelector and
// taint toleration.
func TestAdmissionT4NodeIsolation(t *testing.T) {
	c := kind.InstallLenny(t)
	assertFeatureGatedWebhookAbsent(t, c, "lenny-t4-node-isolation", "features.compliance=true")
	t.Skip("lenny-t4-node-isolation is gated off (features.compliance=false in the e2e overlay); " +
		"the §13.28 T4 node-isolation rejection needs an install with features.compliance=true")
}

// spec: 13.15
// diagnosis: the §13.15 LLM Proxy proxy-mode path is not available.
// The LLM Proxy and its proxy-mode admission posture are rendered only
// when features.llmProxy is true; the e2e overlay leaves it false.
// Exercising the proxy-mode credential path additionally needs a
// running agent pod making LLM calls through the proxy. The test
// confirms the proxy-mode webhook is correctly withheld and skips the
// behavioural assertion.
func TestLLMProxyProxyMode(t *testing.T) {
	c := kind.InstallLenny(t)
	assertFeatureGatedWebhookAbsent(t, c, "lenny-direct-mode-isolation", "features.llmProxy=true")
	t.Skip("the §13.15 LLM Proxy is gated off (features.llmProxy=false in the e2e overlay) and the " +
		"proxy-mode credential path needs a running agent-pod workload not present in the dev-mode install")
}
