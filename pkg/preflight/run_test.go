// SPDX-License-Identifier: MIT

package preflight_test

import (
	"context"
	"testing"

	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/lennylabs/lenny/pkg/preflight"
)

const preflightNS = "lenny-system"

func runScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(s); err != nil {
		t.Fatalf("AddToScheme: %v", err)
	}
	return s
}

func validatingWebhook(name string) *admissionregistrationv1.ValidatingWebhookConfiguration {
	fail := admissionregistrationv1.Fail
	return &admissionregistrationv1.ValidatingWebhookConfiguration{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Webhooks: []admissionregistrationv1.ValidatingWebhook{{
			Name:          name + ".lenny.dev",
			FailurePolicy: &fail,
			ClientConfig:  admissionregistrationv1.WebhookClientConfig{CABundle: []byte("ca-cert")},
		}},
	}
}

func phaseStampCM(data map[string]string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: preflight.PhaseStampConfigMapName, Namespace: preflightNS},
		Data:       data,
	}
}

func runClient(t *testing.T, objs ...client.Object) client.Client {
	t.Helper()
	return fake.NewClientBuilder().WithScheme(runScheme(t)).WithObjects(objs...).Build()
}

// allBaselineWebhooks returns the four baseline ValidatingWebhookConfigurations.
func allBaselineWebhooks() []client.Object {
	names := preflight.ExpectedValidatingWebhooks(preflight.WebhookFeatureFlags{})
	out := make([]client.Object, 0, len(names))
	for _, n := range names {
		out = append(out, validatingWebhook(n))
	}
	return out
}

func resultByName(report []preflight.CheckResult, name string) preflight.Decision {
	for _, r := range report {
		if r.Name == name {
			return r.Decision
		}
	}
	return preflight.Decision{}
}

func TestRunPassesWhenWebhooksHealthyAndNoPhaseStamp(t *testing.T) {
	c := runClient(t, allBaselineWebhooks()...)

	report := preflight.Run(context.Background(), c, preflight.Config{Namespace: preflightNS})
	if preflight.Failed(report) {
		for _, r := range report {
			if !r.Decision.Passed {
				t.Errorf("check %q failed: %s", r.Name, r.Decision.Reason)
			}
		}
	}
}

func TestRunFailsOnMissingWebhook(t *testing.T) {
	// Seed only three of the four baseline webhooks.
	objs := allBaselineWebhooks()
	c := runClient(t, objs[:len(objs)-1]...)

	report := preflight.Run(context.Background(), c, preflight.Config{Namespace: preflightNS})
	if !preflight.Failed(report) {
		t.Fatal("Run passed despite a missing baseline webhook")
	}
	if resultByName(report, "admission-webhook-inventory").Passed {
		t.Error("the admission-webhook-inventory check passed despite a missing webhook")
	}
}

func TestRunFailsOnUnacknowledgedPhaseStampDowngrade(t *testing.T) {
	objs := allBaselineWebhooks()
	objs = append(objs, phaseStampCM(map[string]string{
		"llmProxy": `{"enabled":true,"enabledAt":"2026-05-15T00:00:00Z"}`,
	}))
	c := runClient(t, objs...)

	// Features.LLMProxy is false: the recorded flag is being downgraded.
	report := preflight.Run(context.Background(), c, preflight.Config{Namespace: preflightNS})
	if resultByName(report, "phase-stamp-consistency").Passed {
		t.Error("the phase-stamp-consistency check passed an unacknowledged downgrade")
	}
}

func TestRunPassesWhenPhaseStampConsistent(t *testing.T) {
	objs := allBaselineWebhooks()
	objs = append(objs, phaseStampCM(map[string]string{
		"security.elicitationContentIntegrity.floor": "off",
		"compliance": `{"enabled":true,"enabledAt":"2026-05-15T00:00:00Z"}`,
	}))
	c := runClient(t, objs...)

	report := preflight.Run(context.Background(), c, preflight.Config{
		Namespace: preflightNS,
		Features:  preflight.WebhookFeatureFlags{Compliance: true},
	})
	if resultByName(report, "phase-stamp-consistency").Passed != true {
		t.Errorf("phase-stamp-consistency failed though compliance is still enabled: %s",
			resultByName(report, "phase-stamp-consistency").Reason)
	}
}

func TestRunIgnoresNonLennyWebhooks(t *testing.T) {
	objs := allBaselineWebhooks()
	objs = append(objs, validatingWebhook("third-party-webhook"))
	c := runClient(t, objs...)

	report := preflight.Run(context.Background(), c, preflight.Config{Namespace: preflightNS})
	if preflight.Failed(report) {
		t.Error("Run failed though only an unrelated third-party webhook was extra")
	}
}
