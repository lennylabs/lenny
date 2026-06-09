// SPDX-License-Identifier: MIT

//go:build integration

// Package integration_test holds the §17.2 named integration suites the
// spec enumerates under tests/integration/ (lines 76, 84). They exercise
// chart-render inventory, controller-pod-spec-vs-admission-policy
// agreement, and the render-time feature-flag downgrade guard.
package integration_test

import (
	"sort"
	"testing"

	"github.com/lennylabs/lenny/pkg/preflight"
	"github.com/lennylabs/lenny/tests/testinfra/helm"
)

// vwcKind is the rendered Kubernetes kind for an admission webhook.
const vwcKind = "ValidatingWebhookConfiguration"

// phaseConfig is one §17.2 feature-flag combination (a deployment phase)
// the webhook-inventory suite parameterises over.
type phaseConfig struct {
	name  string
	flags preflight.WebhookFeatureFlags
	set   []string
}

// phaseConfigs are the four §17.2 cases the spec names for
// admission_webhook_inventory_test.go: Phase 3.5 baseline (all flags
// off), Phase 5.8 (llmProxy), Phase 8 (llmProxy + drainReadiness), and
// Phase 13 (all three). CosignVerify and RegistryDigest are left at
// their chart defaults (off) in every case so the rendered set and the
// preflight expected set are compared on the same footing.
func phaseConfigs() []phaseConfig {
	return []phaseConfig{
		{
			name:  "phase_3.5_baseline_all_flags_off",
			flags: preflight.WebhookFeatureFlags{},
			set:   nil,
		},
		{
			name:  "phase_5.8_llmProxy",
			flags: preflight.WebhookFeatureFlags{LLMProxy: true},
			set:   []string{"features.llmProxy=true"},
		},
		{
			name:  "phase_8_llmProxy_drainReadiness",
			flags: preflight.WebhookFeatureFlags{LLMProxy: true, DrainReadiness: true},
			set:   []string{"features.llmProxy=true", "features.drainReadiness=true"},
		},
		{
			name:  "phase_13_all_flags",
			flags: preflight.WebhookFeatureFlags{LLMProxy: true, DrainReadiness: true, Compliance: true},
			set:   []string{"features.llmProxy=true", "features.drainReadiness=true", "features.compliance=true"},
		},
	}
}

// TestAdmissionWebhookInventoryMatchesPreflightExpected is the §17.2
// admission_webhook_inventory_test.go suite (lines 80-84). It is
// parameterised by the three features.* flags and asserts, in each of
// the four phase configurations, that the set of
// ValidatingWebhookConfiguration resources the chart renders is exactly
// the set pkg/preflight computes as expected for the same flags. This
// catches drift in either direction: a chart template gated on the wrong
// flag (rendered ⊋ expected or rendered ⊊ expected) and a preflight
// expected-set computation that disagrees with the chart.
//
// spec: §17.2 lines 80-84 (feature-gated chart inventory, single source
// of truth; admission_webhook_inventory_test.go). F-17.2.15 / F-17.2.12.
func TestAdmissionWebhookInventoryMatchesPreflightExpected(t *testing.T) {
	helm.SkipUnlessAvailable(t)

	for _, pc := range phaseConfigs() {
		t.Run(pc.name, func(t *testing.T) {
			set := append([]string{"coredns.clusterIP=10.96.0.10"}, pc.set...)
			manifests := helm.Render(t, helm.Options{
				Chart:     "../../charts/lenny",
				Release:   "lenny",
				Namespace: "lenny-system",
				Set:       set,
			})

			rendered := webhookNames(manifests.FindAll(vwcKind))
			expected := append([]string(nil), preflight.ExpectedValidatingWebhooks(pc.flags)...)
			sort.Strings(rendered)
			sort.Strings(expected)

			if !equalStringSets(rendered, expected) {
				t.Errorf("§17.2 webhook inventory drift for %s:\n  rendered: %v\n  expected: %v\n"+
					"the chart-rendered ValidatingWebhookConfiguration set must equal "+
					"preflight.ExpectedValidatingWebhooks(%+v)", pc.name, rendered, expected, pc.flags)
			}
		})
	}
}

// TestAdmissionWebhookInventoryLLMProxyAddsNoWebhook pins the §13.2
// line 440 / F-13.2.12 decision that lenny-direct-mode-isolation renders
// unconditionally: flipping features.llmProxy on must not change the
// rendered webhook set relative to the baseline, because the proxy flag
// no longer gates any webhook.
//
// spec: §17.2; §13.2 line 440 step 2. F-17.2.15 / F-13.2.12.
func TestAdmissionWebhookInventoryLLMProxyAddsNoWebhook(t *testing.T) {
	helm.SkipUnlessAvailable(t)

	base := webhookNames(helm.Render(t, helm.Options{
		Chart: "../../charts/lenny", Release: "lenny", Namespace: "lenny-system",
		Set: []string{"coredns.clusterIP=10.96.0.10"},
	}).FindAll(vwcKind))
	withProxy := webhookNames(helm.Render(t, helm.Options{
		Chart: "../../charts/lenny", Release: "lenny", Namespace: "lenny-system",
		Set: []string{"coredns.clusterIP=10.96.0.10", "features.llmProxy=true"},
	}).FindAll(vwcKind))

	sort.Strings(base)
	sort.Strings(withProxy)
	if !equalStringSets(base, withProxy) {
		t.Errorf("features.llmProxy changed the rendered webhook set; it must gate no webhook (F-13.2.12):\n  baseline: %v\n  llmProxy: %v", base, withProxy)
	}
}

func webhookNames(ms []helm.Manifest) []string {
	names := make([]string, 0, len(ms))
	for _, m := range ms {
		names = append(names, m.Name)
	}
	return names
}

func equalStringSets(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
