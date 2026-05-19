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

func TestCheckAdmissionWebhooksFailsOnMissingWebhook(t *testing.T) {
	expected := preflight.ExpectedValidatingWebhooks(preflight.WebhookFeatureFlags{})
	// Deploy all but the last expected webhook.
	d := preflight.CheckAdmissionWebhooks(expected, healthyDeployment(expected[:len(expected)-1]))
	if d.Passed {
		t.Fatal("check passed with a missing webhook")
	}
	if !strings.Contains(d.Reason, "not found") {
		t.Errorf("reason %q does not report the missing webhook", d.Reason)
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
