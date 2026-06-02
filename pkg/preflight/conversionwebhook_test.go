// SPDX-License-Identifier: MIT

package preflight_test

import (
	"context"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/lennylabs/lenny/pkg/preflight"
)

// spec: §15.5 line 2438 / §17.2 line 58 — the conversion-webhook
// availability check passes when the lenny-crd-conversion Service and
// Deployment are present and the Deployment reports a ready replica.
// F-15.5.3 / F-17.2.4 / F-10.5.6.
func TestCheckConversionWebhook_spec_15_5_2438(t *testing.T) {
	cases := []struct {
		name       string
		state      preflight.ConversionWebhookState
		wantPassed bool
		wantPhrase string
	}{
		{
			name:       "not yet deployed passes (fresh install ordering)",
			state:      preflight.ConversionWebhookState{},
			wantPassed: true,
			wantPhrase: "not yet deployed",
		},
		{
			name:       "service absent fails fail-closed",
			state:      preflight.ConversionWebhookState{DeploymentPresent: true, DeploymentReady: true},
			wantPassed: false,
			wantPhrase: "Service is absent",
		},
		{
			name:       "deployment not ready fails fail-closed",
			state:      preflight.ConversionWebhookState{ServicePresent: true, DeploymentPresent: true},
			wantPassed: false,
			wantPhrase: "no ready replicas",
		},
		{
			name:       "present and ready passes",
			state:      preflight.ConversionWebhookState{ServicePresent: true, DeploymentPresent: true, DeploymentReady: true},
			wantPassed: true,
			wantPhrase: "present and ready",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := preflight.CheckConversionWebhook(tc.state)
			if d.Passed != tc.wantPassed {
				t.Fatalf("Passed = %v, want %v (reason %q)", d.Passed, tc.wantPassed, d.Reason)
			}
			if !strings.Contains(d.Reason, tc.wantPhrase) {
				t.Errorf("reason %q should contain %q", d.Reason, tc.wantPhrase)
			}
		})
	}
}

func conversionScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(s); err != nil {
		t.Fatalf("AddToScheme: %v", err)
	}
	return s
}

func conversionService() *corev1.Service {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: preflight.ConversionWebhookName, Namespace: "lenny-system"},
	}
}

func conversionDeployment(ready int32) *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: preflight.ConversionWebhookName, Namespace: "lenny-system"},
		Status:     appsv1.DeploymentStatus{ReadyReplicas: ready},
	}
}

// spec: §15.5 line 2438 — gatherConversionWebhook is exercised through
// Run so the namespace-scoped Service/Deployment reads and the readiness
// projection are covered end to end. A ready workload yields a passing
// conversion-webhook-availability entry. F-15.5.3 / F-17.2.4.
func TestRunConversionWebhookReady_spec_15_5_2438(t *testing.T) {
	objs := []client.Object{conversionService(), conversionDeployment(2)}
	c := fake.NewClientBuilder().WithScheme(conversionScheme(t)).WithObjects(objs...).Build()

	report := preflight.Run(context.Background(), c, preflight.Config{Namespace: "lenny-system"})
	d := findCheck(t, report, "conversion-webhook-availability")
	if !d.Passed {
		t.Fatalf("expected pass; got reason %q", d.Reason)
	}
}

// spec: §15.5 line 2438 — a deployed-but-unready conversion webhook
// aborts the upgrade. F-15.5.3 / F-17.2.4.
func TestRunConversionWebhookUnready_spec_15_5_2438(t *testing.T) {
	objs := []client.Object{conversionService(), conversionDeployment(0)}
	c := fake.NewClientBuilder().WithScheme(conversionScheme(t)).WithObjects(objs...).Build()

	report := preflight.Run(context.Background(), c, preflight.Config{Namespace: "lenny-system"})
	d := findCheck(t, report, "conversion-webhook-availability")
	if d.Passed {
		t.Fatal("expected failure on unready conversion webhook")
	}
	if !strings.Contains(d.Reason, "not ready") {
		t.Errorf("reason should describe the unready webhook; got %q", d.Reason)
	}
}
