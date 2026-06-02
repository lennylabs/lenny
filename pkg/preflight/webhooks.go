// SPDX-License-Identifier: MIT

package preflight

import "fmt"

// baselineValidatingWebhooks are the ValidatingWebhookConfigurations
// always expected from Phase 3.5 onward (§17.2). The Phase 3.5 baseline
// entry lenny-crd-conversion is a CRD conversion endpoint rather than a
// ValidatingWebhookConfiguration; it is verified by the separate
// conversion-webhook-availability check (CheckConversionWebhook) which
// confirms the Service is present and the Deployment is ready per §15.5
// line 2438. F-15.5.3 / F-17.2.4.
//
// lenny-pod-security is a §13.1 pod-security baseline control: it
// renders unconditionally and so belongs in the baseline set.
//
// lenny-direct-mode-isolation renders unconditionally (§13.2 line 440
// step 2): its enforcement target is a pool's credential-delivery and
// egress configuration — the §13.2 NET-006 proxy/provider-direct mutual
// exclusivity (every tenancy mode) and the §4.9 direct/standard and
// proxy/spiffe rules (multi-tenant mode) — which is independent of the
// LLM-proxy feature flag, so it is part of the baseline set rather than
// feature-gated.
var baselineValidatingWebhooks = []string{
	"lenny-label-immutability",
	"lenny-sandboxclaim-guard",
	"lenny-pool-config-validator",
	"lenny-ephemeral-container-cred-guard",
	"lenny-pod-security",
	"lenny-direct-mode-isolation",
}

// WebhookFeatureFlags are the §17.2 chart feature flags that gate the
// non-baseline admission webhooks.
type WebhookFeatureFlags struct {
	// LLMProxy records whether the §4.9 LLM-proxy feature is enabled. It
	// no longer gates an admission webhook (lenny-direct-mode-isolation is
	// a baseline control, see baselineValidatingWebhooks); it is retained
	// for the §17.2 phase-stamp feature-flag downgrade audit.
	LLMProxy bool
	// DrainReadiness gates lenny-drain-readiness.
	DrainReadiness bool
	// Compliance gates lenny-data-residency-validator and
	// lenny-t4-node-isolation.
	Compliance bool
	// CosignVerify gates lenny-cosign-verify, the §5.2 image-signing
	// webhook, behind imageVerification.cosign.enabled.
	CosignVerify bool
	// RegistryDigest gates lenny-registry-digest, the §5.2 / §25.8
	// digest-pinning webhook, behind platform.registry.requireDigest. It
	// is fail-closed (failurePolicy: Fail) and air-gap installs mandate
	// it, so the preflight inventory must track it. spec: §17.2 line 56.
	RegistryDigest bool
}

// ExpectedValidatingWebhooks returns the names of the
// ValidatingWebhookConfigurations that must be deployed for the given
// feature-flag set (§17.2 feature-gated chart inventory). The expected
// set tracks the rendered set exactly, so a pre-Phase-13 chart slice
// installs cleanly while a missing baseline webhook still fails.
func ExpectedValidatingWebhooks(flags WebhookFeatureFlags) []string {
	expected := append([]string(nil), baselineValidatingWebhooks...)
	if flags.DrainReadiness {
		expected = append(expected, "lenny-drain-readiness")
	}
	if flags.Compliance {
		expected = append(expected, "lenny-data-residency-validator", "lenny-t4-node-isolation")
	}
	if flags.CosignVerify {
		expected = append(expected, "lenny-cosign-verify")
	}
	if flags.RegistryDigest {
		expected = append(expected, "lenny-registry-digest")
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

// CheckAdmissionWebhooks verifies the §17.9 admission posture: every
// expected admission webhook that is already deployed must be
// fail-closed (failurePolicy "Fail") with an injected CA bundle. A
// deployed-but-fail-open webhook aborts the install fail-closed.
//
// The Job runs the check as both a pre-install and a pre-upgrade hook,
// so it executes BEFORE the chart's main phase applies any webhook. An
// expected webhook that is not yet deployed is therefore not a
// failure: on a fresh install none are deployed, and on an upgrade
// that newly enables a feature-gated webhook the new webhook is
// applied only in the main phase. The check validates the fail-closed
// posture of what is present; it cannot, by construction, detect a
// webhook the same operation is about to create. A chart-author
// omission is caught instead by the chart's helm-unittest render
// assertions. The check still fails a deployed webhook whose
// failurePolicy is not "Fail" or whose caBundle is empty — a genuine
// fail-open gap that no later phase corrects.
func CheckAdmissionWebhooks(expected []string, deployed []WebhookConfig) Decision {
	byName := make(map[string]WebhookConfig, len(deployed))
	for _, w := range deployed {
		byName[w.Name] = w
	}
	for _, name := range expected {
		w, ok := byName[name]
		if !ok {
			// Not yet deployed — applied in the chart's main phase,
			// after this pre-hook. Not a fail-open gap.
			continue
		}
		if w.FailurePolicy != "Fail" {
			return Decision{Passed: false, Reason: fmt.Sprintf(
				"ValidatingWebhookConfiguration %q has failurePolicy=%q; expected \"Fail\" for fail-closed behavior",
				name, w.FailurePolicy,
			)}
		}
		if !w.HasCABundle {
			return Decision{Passed: false, Reason: fmt.Sprintf(
				"ValidatingWebhookConfiguration %q has an empty caBundle; cert-manager CA injection has not completed", name,
			)}
		}
	}
	return Decision{Passed: true}
}
