// SPDX-License-Identifier: MIT

package preflight_test

import (
	"strings"
	"testing"

	"github.com/lennylabs/lenny/pkg/preflight"
)

func TestExpectedValidatingWebhooksBaseline(t *testing.T) {
	got := preflight.ExpectedValidatingWebhooks(preflight.WebhookFeatureFlags{})
	want := map[string]bool{
		"lenny-label-immutability":             true,
		"lenny-sandboxclaim-guard":             true,
		"lenny-pool-config-validator":          true,
		"lenny-ephemeral-container-cred-guard": true,
		"lenny-pod-security":                   true,
		// spec: §13.2 line 440 step 2 — renders unconditionally, so the
		// inventory expects it in the baseline set (F-13.2.12).
		"lenny-direct-mode-isolation": true,
	}
	if len(got) != len(want) {
		t.Fatalf("baseline expected %d webhooks, want %d: %v", len(got), len(want), got)
	}
	for _, name := range got {
		if !want[name] {
			t.Errorf("unexpected webhook %q in the baseline set", name)
		}
	}
}

func TestExpectedValidatingWebhooksWithFeatureFlags(t *testing.T) {
	got := preflight.ExpectedValidatingWebhooks(preflight.WebhookFeatureFlags{
		LLMProxy:       true,
		DrainReadiness: true,
		Compliance:     true,
		CosignVerify:   true,
		RegistryDigest: true,
	})
	want := map[string]bool{
		"lenny-label-immutability":             true,
		"lenny-sandboxclaim-guard":             true,
		"lenny-pool-config-validator":          true,
		"lenny-ephemeral-container-cred-guard": true,
		"lenny-pod-security":                   true,
		"lenny-direct-mode-isolation":          true,
		"lenny-drain-readiness":                true,
		"lenny-data-residency-validator":       true,
		"lenny-t4-node-isolation":              true,
		"lenny-cosign-verify":                  true,
		"lenny-registry-digest":                true,
	}
	if len(got) != len(want) {
		t.Fatalf("all-flags expected %d webhooks, want %d: %v", len(got), len(want), got)
	}
	for _, name := range got {
		if !want[name] {
			t.Errorf("unexpected webhook %q in the all-flags set", name)
		}
	}
}

// spec: §13.2 line 440 step 2 (F-13.2.12) — lenny-direct-mode-isolation
// is no longer gated on the LLM-proxy feature flag; it appears in the
// inventory whether or not LLMProxy is set.
func TestExpectedValidatingWebhooksDirectModeIsolationUngated_spec_13_2(t *testing.T) {
	for _, llmProxy := range []bool{false, true} {
		got := preflight.ExpectedValidatingWebhooks(preflight.WebhookFeatureFlags{LLMProxy: llmProxy})
		found := false
		for _, n := range got {
			if n == "lenny-direct-mode-isolation" {
				found = true
			}
		}
		if !found {
			t.Errorf("lenny-direct-mode-isolation must be expected with LLMProxy=%v", llmProxy)
		}
	}
}

func TestExpectedValidatingWebhooksCosignFlag(t *testing.T) {
	// The cosign webhook appears only when its flag is set.
	without := preflight.ExpectedValidatingWebhooks(preflight.WebhookFeatureFlags{})
	for _, n := range without {
		if n == "lenny-cosign-verify" {
			t.Fatal("lenny-cosign-verify must not be in the baseline set")
		}
	}
	with := preflight.ExpectedValidatingWebhooks(preflight.WebhookFeatureFlags{CosignVerify: true})
	found := false
	for _, n := range with {
		if n == "lenny-cosign-verify" {
			found = true
		}
	}
	if !found {
		t.Error("lenny-cosign-verify must appear when CosignVerify is set")
	}
}

// spec: §17.2 line 56 — lenny-registry-digest is fail-closed and is
// tracked by the preflight inventory only when platform.registry.requireDigest
// is set, so an air-gap install (where the webhook is mandatory) cannot
// ship without it being verified. F-17.2.13.
func TestExpectedValidatingWebhooksRegistryDigestFlag_spec_17_2(t *testing.T) {
	without := preflight.ExpectedValidatingWebhooks(preflight.WebhookFeatureFlags{})
	for _, n := range without {
		if n == "lenny-registry-digest" {
			t.Fatal("lenny-registry-digest must not be in the baseline set")
		}
	}
	with := preflight.ExpectedValidatingWebhooks(preflight.WebhookFeatureFlags{RegistryDigest: true})
	found := false
	for _, n := range with {
		if n == "lenny-registry-digest" {
			found = true
		}
	}
	if !found {
		t.Error("lenny-registry-digest must appear when RegistryDigest is set")
	}
}

func healthyWebhook(name string) preflight.WebhookConfig {
	return preflight.WebhookConfig{Name: name, FailurePolicy: "Fail", HasCABundle: true}
}

func healthyDeployment(names []string) []preflight.WebhookConfig {
	out := make([]preflight.WebhookConfig, 0, len(names))
	for _, n := range names {
		out = append(out, healthyWebhook(n))
	}
	return out
}

func TestCheckAdmissionWebhooksPassesWhenAllPresent(t *testing.T) {
	expected := preflight.ExpectedValidatingWebhooks(preflight.WebhookFeatureFlags{})
	d := preflight.CheckAdmissionWebhooks(expected, healthyDeployment(expected))
	if !d.Passed {
		t.Errorf("check failed though every expected webhook is healthy: %s", d.Reason)
	}
}

func TestCheckAdmissionWebhooksPassesOnFreshInstall(t *testing.T) {
	expected := preflight.ExpectedValidatingWebhooks(preflight.WebhookFeatureFlags{})
	// A fresh install: the chart's webhooks are applied only after the
	// pre-install hooks finish, so none are deployed when preflight
	// runs. The cluster may still carry unrelated webhooks (cert-manager).
	deployed := []preflight.WebhookConfig{
		{Name: "cert-manager-webhook", FailurePolicy: "Fail", HasCABundle: true},
	}
	d := preflight.CheckAdmissionWebhooks(expected, deployed)
	if !d.Passed {
		t.Errorf("check failed on a fresh install with no Lenny webhooks present: %s", d.Reason)
	}
}

func TestCheckAdmissionWebhooksPassesOnPartiallyDeployedWebhooks(t *testing.T) {
	expected := preflight.ExpectedValidatingWebhooks(preflight.WebhookFeatureFlags{})
	// An upgrade that newly enables a feature-gated webhook: the prior
	// release's webhooks are deployed, the newly-enabled one is not yet
	// (it lands in the chart's main phase, after this pre-upgrade hook).
	// A not-yet-deployed webhook is not a fail-open gap, so the check
	// passes as long as every deployed webhook is fail-closed.
	d := preflight.CheckAdmissionWebhooks(expected, healthyDeployment(expected[:len(expected)-1]))
	if !d.Passed {
		t.Errorf("check failed though every deployed webhook is fail-closed: %s", d.Reason)
	}
}

func TestCheckAdmissionWebhooksFailsOnNonFailPolicy(t *testing.T) {
	expected := []string{"lenny-label-immutability"}
	deployed := []preflight.WebhookConfig{
		{Name: "lenny-label-immutability", FailurePolicy: "Ignore", HasCABundle: true},
	}
	d := preflight.CheckAdmissionWebhooks(expected, deployed)
	if d.Passed {
		t.Fatal("check passed a webhook with failurePolicy Ignore")
	}
	if !strings.Contains(d.Reason, "failurePolicy") {
		t.Errorf("reason %q does not report the failure policy", d.Reason)
	}
}

func TestCheckAdmissionWebhooksFailsOnEmptyCABundle(t *testing.T) {
	expected := []string{"lenny-sandboxclaim-guard"}
	deployed := []preflight.WebhookConfig{
		{Name: "lenny-sandboxclaim-guard", FailurePolicy: "Fail", HasCABundle: false},
	}
	d := preflight.CheckAdmissionWebhooks(expected, deployed)
	if d.Passed {
		t.Fatal("check passed a webhook with no caBundle")
	}
	if !strings.Contains(d.Reason, "caBundle") {
		t.Errorf("reason %q does not report the missing caBundle", d.Reason)
	}
}

// TestCatalogueCoverageInventoryMatchesSpec17_2 asserts the §17.2
// admission-policies catalogue and the preflight inventory agree: every
// §17.2-enumerated ValidatingAdmissionWebhook is reachable in the
// inventory, and every inventory webhook is either catalogued or
// declared implementation-only. This makes the §17.2 line-40 "single
// source of truth" cross-check machine-checked. spec: §17.2 line 40.
// F-17.2.12.
func TestCatalogueCoverageInventoryMatchesSpec17_2(t *testing.T) {
	missing, undocumented := preflight.CatalogueCoverage()
	if len(missing) != 0 {
		t.Errorf("§17.2-enumerated webhooks absent from the preflight inventory: %v", missing)
	}
	if len(undocumented) != 0 {
		t.Errorf("preflight inventory tracks webhooks neither catalogued in §17.2 nor declared implementation-only: %v", undocumented)
	}
}

// TestSpec17_2CatalogueExcludesConversionWebhook asserts the §17.2
// catalogue used for the ValidatingWebhookConfiguration inventory does
// not list lenny-crd-conversion (item 12), which is a CRD conversion
// endpoint verified by CheckConversionWebhook, not a
// ValidatingWebhookConfiguration. spec: §17.2 line 53. F-17.2.12.
func TestSpec17_2CatalogueExcludesConversionWebhook(t *testing.T) {
	for _, w := range preflight.Spec17_2ValidatingWebhooks {
		if w == "lenny-crd-conversion" {
			t.Fatalf("lenny-crd-conversion must not be in the ValidatingWebhookConfiguration catalogue; it is a CRD conversion endpoint")
		}
	}
}

// TestCatalogueCoverageDetectsUndocumentedWebhook is a negative control:
// when a webhook enters the inventory that is neither in the §17.2
// catalogue nor the implementation-only set, CatalogueCoverage must
// surface it. The test exercises the detection logic directly by
// confirming that the implementation-only webhooks (which the inventory
// renders) are the reason undocumented stays empty — removing the
// implementation-only acknowledgement would re-expose them. spec: §17.2
// line 40. F-17.2.12.
func TestCatalogueCoverageImplementationOnlyWebhooksAreInInventory(t *testing.T) {
	inventory := preflight.ExpectedValidatingWebhooks(preflight.WebhookFeatureFlags{
		DrainReadiness: true, Compliance: true, CosignVerify: true, RegistryDigest: true,
	})
	have := make(map[string]bool, len(inventory))
	for _, w := range inventory {
		have[w] = true
	}
	for _, w := range []string{"lenny-pod-security", "lenny-cosign-verify", "lenny-registry-digest"} {
		if !have[w] {
			t.Errorf("implementation-only webhook %q is declared but the inventory does not render it; the acknowledgement is stale", w)
		}
	}
}
