// SPDX-License-Identifier: MIT

package preflight

import "fmt"

// baselineValidatingWebhooks are the ValidatingWebhookConfigurations
// always expected from Phase 3.5 onward (§17.2). The fifth Phase 3.5
// baseline entry, lenny-crd-conversion, is a CRD conversion endpoint
// rather than a ValidatingWebhookConfiguration and is verified
// separately.
var baselineValidatingWebhooks = []string{
	"lenny-label-immutability",
	"lenny-sandboxclaim-guard",
	"lenny-pool-config-validator",
	"lenny-ephemeral-container-cred-guard",
}

// WebhookFeatureFlags are the §17.2 chart feature flags that gate the
// non-baseline admission webhooks.
type WebhookFeatureFlags struct {
	// LLMProxy gates lenny-direct-mode-isolation.
	LLMProxy bool
	// DrainReadiness gates lenny-drain-readiness.
	DrainReadiness bool
	// Compliance gates lenny-data-residency-validator and
	// lenny-t4-node-isolation.
	Compliance bool
}

// ExpectedValidatingWebhooks returns the names of the
// ValidatingWebhookConfigurations that must be deployed for the given
// feature-flag set (§17.2 feature-gated chart inventory). The expected
// set tracks the rendered set exactly, so a pre-Phase-13 chart slice
// installs cleanly while a missing baseline webhook still fails.
func ExpectedValidatingWebhooks(flags WebhookFeatureFlags) []string {
	expected := append([]string(nil), baselineValidatingWebhooks...)
	if flags.LLMProxy {
		expected = append(expected, "lenny-direct-mode-isolation")
	}
	if flags.DrainReadiness {
		expected = append(expected, "lenny-drain-readiness")
	}
	if flags.Compliance {
		expected = append(expected, "lenny-data-residency-validator", "lenny-t4-node-isolation")
	}
	return expected
}

// WebhookConfig is the inspected state of one deployed
// ValidatingWebhookConfiguration.
type WebhookConfig struct {
	// Name is the ValidatingWebhookConfiguration's name.
	Name string
	// FailurePolicy is the configuration's failurePolicy; "Fail" is
	// required for the fail-closed admission posture.
	FailurePolicy string
	// HasCABundle reports whether every webhook entry carries a
	// non-empty clientConfig.caBundle.
	HasCABundle bool
}

// CheckAdmissionWebhooks verifies that every expected admission webhook
// is deployed fail-closed with an injected CA bundle (§17.9). A missing
// webhook, a non-Fail failurePolicy, or an absent caBundle aborts the
// install fail-closed, preventing a chart-author omission from
// shipping silently as a fail-open admission gap.
func CheckAdmissionWebhooks(expected []string, deployed []WebhookConfig) Decision {
	byName := make(map[string]WebhookConfig, len(deployed))
	for _, w := range deployed {
		byName[w.Name] = w
	}
	for _, name := range expected {
		w, ok := byName[name]
		if !ok {
			return Decision{Passed: false, Reason: fmt.Sprintf(
				"expected ValidatingWebhookConfiguration %q not found; the chart-rendered webhook is missing", name)}
		}
		if w.FailurePolicy != "Fail" {
			return Decision{Passed: false, Reason: fmt.Sprintf(
				"ValidatingWebhookConfiguration %q has failurePolicy=%q; expected \"Fail\" for fail-closed behavior",
				name, w.FailurePolicy)}
		}
		if !w.HasCABundle {
			return Decision{Passed: false, Reason: fmt.Sprintf(
				"ValidatingWebhookConfiguration %q has an empty caBundle; cert-manager CA injection has not completed", name)}
		}
	}
	return Decision{Passed: true}
}
