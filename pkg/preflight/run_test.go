// SPDX-License-Identifier: MIT

package preflight_test

import (
	"context"
	"errors"
	"testing"

	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	"github.com/lennylabs/lenny/pkg/preflight"
)

const preflightNS = "lenny-system"

func runScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(s); err != nil {
		t.Fatalf("AddToScheme: %v", err)
	}
	// spec: §10 line 437 — the crd-schema-version check fetches the
	// installed CRDs by name; tests register apiextensions.k8s.io/v1
	// so the fake reader can deserialize the baseline CRD objects.
	// F-15.5.12.
	if err := apiextensionsv1.AddToScheme(s); err != nil {
		t.Fatalf("apiextensions AddToScheme: %v", err)
	}
	return s
}

// baselineCRDs returns one healthy CRD per LennyCRDNames so the
// crd-schema-version check passes by default when an existing Run test
// only cares about a different §17.9 dimension. F-15.5.12.
func baselineCRDs() []client.Object {
	out := make([]client.Object, 0, len(preflight.LennyCRDNames))
	for _, name := range preflight.LennyCRDNames {
		out = append(out, &apiextensionsv1.CustomResourceDefinition{
			ObjectMeta: metav1.ObjectMeta{
				Name: name,
				Annotations: map[string]string{
					preflight.CRDSchemaVersionAnnotation: preflight.CurrentCRDSchemaVersion,
				},
			},
		})
	}
	return out
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
	all := append(baselineCRDs(), objs...)
	return fake.NewClientBuilder().WithScheme(runScheme(t)).WithObjects(all...).Build()
}

// allBaselineWebhooks returns the baseline ValidatingWebhookConfigurations.
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

func TestRunFailsOnFailOpenWebhook(t *testing.T) {
	// preflight runs before the chart's main phase, so a not-yet-deployed
	// webhook is not a gap. The genuine fail-open gap the inventory check
	// catches is a DEPLOYED webhook whose failurePolicy is not Fail.
	objs := allBaselineWebhooks()
	names := preflight.ExpectedValidatingWebhooks(preflight.WebhookFeatureFlags{})
	ignore := admissionregistrationv1.Ignore
	failOpen := validatingWebhook(names[len(names)-1])
	failOpen.Webhooks[0].FailurePolicy = &ignore
	objs[len(objs)-1] = failOpen
	c := runClient(t, objs...)

	report := preflight.Run(context.Background(), c, preflight.Config{Namespace: preflightNS})
	if !preflight.Failed(report) {
		t.Fatal("Run passed despite a fail-open (failurePolicy: Ignore) webhook")
	}
	if resultByName(report, "admission-webhook-inventory").Passed {
		t.Error("the admission-webhook-inventory check passed despite a fail-open webhook")
	}
}

func TestRunPassesWhenAFeatureGatedWebhookIsNotYetDeployed(t *testing.T) {
	// An upgrade that newly enables a feature-gated webhook: the prior
	// webhooks are deployed fail-closed, the new one lands only in the
	// chart's main phase. preflight must not abort the upgrade for it.
	objs := allBaselineWebhooks()
	c := runClient(t, objs[:len(objs)-1]...)

	report := preflight.Run(context.Background(), c, preflight.Config{Namespace: preflightNS})
	if resultByName(report, "admission-webhook-inventory").Passed != true {
		t.Errorf("admission-webhook-inventory failed though every deployed webhook is fail-closed: %s",
			resultByName(report, "admission-webhook-inventory").Reason)
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

func lennyDeployment(name string, hostPID bool) *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: preflightNS,
			Labels:    map[string]string{"app.kubernetes.io/name": "lenny"},
		},
		Spec: appsv1.DeploymentSpec{
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{HostPID: hostPID},
			},
		},
	}
}

func TestRunPassesWhenWorkloadsHaveNoHostSharing(t *testing.T) {
	objs := allBaselineWebhooks()
	objs = append(objs, lennyDeployment("lenny-gateway", false))
	c := runClient(t, objs...)

	report := preflight.Run(context.Background(), c, preflight.Config{Namespace: preflightNS})
	if !resultByName(report, "host-sharing-flags").Passed {
		t.Errorf("host-sharing-flags failed for a compliant Deployment: %s",
			resultByName(report, "host-sharing-flags").Reason)
	}
}

func TestRunFailsOnHostSharingWorkload(t *testing.T) {
	objs := allBaselineWebhooks()
	objs = append(objs, lennyDeployment("lenny-gateway", true)) // hostPID enabled
	c := runClient(t, objs...)

	report := preflight.Run(context.Background(), c, preflight.Config{Namespace: preflightNS})
	if resultByName(report, "host-sharing-flags").Passed {
		t.Error("host-sharing-flags passed a Deployment with hostPID true")
	}
	if !preflight.Failed(report) {
		t.Error("Run did not fail despite a host-sharing workload")
	}
}

func TestRunReportsAClusterReadFailureFailClosed(t *testing.T) {
	c := fake.NewClientBuilder().
		WithScheme(runScheme(t)).
		WithInterceptorFuncs(interceptor.Funcs{
			List: func(ctx context.Context, cl client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
				if _, ok := list.(*admissionregistrationv1.ValidatingWebhookConfigurationList); ok {
					return errors.New("api server unavailable")
				}
				return cl.List(ctx, list, opts...)
			},
		}).
		Build()

	report := preflight.Run(context.Background(), c, preflight.Config{Namespace: preflightNS})
	if !preflight.Failed(report) {
		t.Error("Run did not fail despite a cluster read error")
	}
	if resultByName(report, "admission-webhook-inventory").Passed {
		t.Error("admission-webhook-inventory passed despite a failed List")
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
