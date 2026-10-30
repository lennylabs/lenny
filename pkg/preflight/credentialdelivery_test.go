// SPDX-License-Identifier: MIT

package preflight_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
	"github.com/lennylabs/lenny/pkg/preflight"
)

// deliveryTemplate builds a SandboxTemplate carrying the four
// credential-delivery fields the §4.9 preflight scan inspects.
func deliveryTemplate(name, delivery, isolation, spiffe, egress string) *lennyv1.SandboxTemplate {
	return &lennyv1.SandboxTemplate{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "lenny-agents"},
		Spec: lennyv1.SandboxTemplateSpec{
			RuntimeRef:       "echo",
			DeliveryMode:     delivery,
			IsolationProfile: isolation,
			SpiffeBinding:    spiffe,
			EgressProfile:    egress,
		},
	}
}

// TestCheckCredentialDelivery_spec_4_9 covers the §4.9 install-time
// credential-delivery scan as a pure decision: the scan enforces only in
// multi-tenant mode, and in multi-tenant mode it rejects each of the
// three combinations the canonical Decide denies, stamping the guard's
// error code and naming the offending SandboxTemplate.
func TestCheckCredentialDelivery_spec_4_9(t *testing.T) {
	cases := []struct {
		name     string
		tenancy  string
		devMode  bool
		pools    []preflight.CredentialDeliveryPool
		wantPass bool
		wantSubs []string
	}{
		{
			name:     "single-tenant permits forbidden combination",
			tenancy:  "single",
			pools:    []preflight.CredentialDeliveryPool{{Name: "p", DeliveryMode: "proxy", SpiffeBinding: "disabled"}},
			wantPass: true,
		},
		{
			name:     "multi-tenant dev mode permits spiffe-disabled",
			tenancy:  "multi",
			devMode:  true,
			pools:    []preflight.CredentialDeliveryPool{{Name: "p", DeliveryMode: "proxy", SpiffeBinding: "disabled"}},
			wantPass: true,
		},
		{
			name:     "no pools passes",
			tenancy:  "multi",
			pools:    nil,
			wantPass: true,
		},
		{
			name:     "multi-tenant safe pool passes",
			tenancy:  "multi",
			pools:    []preflight.CredentialDeliveryPool{{Name: "p", DeliveryMode: "proxy", SpiffeBinding: "enabled", IsolationProfile: "sandboxed"}},
			wantPass: true,
		},
		{
			name:     "multi-tenant proxy spiffe-disabled fails",
			tenancy:  "multi",
			pools:    []preflight.CredentialDeliveryPool{{Name: "leaky", DeliveryMode: "proxy", SpiffeBinding: "disabled"}},
			wantPass: false,
			wantSubs: []string{"ProxyModeSpiffeBindingDisabledMultiTenantRejected", "SandboxTemplate leaky"},
		},
		{
			name:     "multi-tenant direct standard fails",
			tenancy:  "multi",
			pools:    []preflight.CredentialDeliveryPool{{Name: "runc", DeliveryMode: "direct", IsolationProfile: "standard"}},
			wantPass: false,
			wantSubs: []string{"DirectModeStandardIsolationMultiTenantRejected", "SandboxTemplate runc"},
		},
		{
			name:     "multi-tenant proxy provider-direct fails",
			tenancy:  "multi",
			pools:    []preflight.CredentialDeliveryPool{{Name: "bypass", DeliveryMode: "proxy", SpiffeBinding: "enabled", EgressProfile: "provider-direct"}},
			wantPass: false,
			wantSubs: []string{"InvalidPoolEgressDeliveryCombo", "SandboxTemplate bypass"},
		},
		{
			name:    "first forbidden pool reported",
			tenancy: "multi",
			pools: []preflight.CredentialDeliveryPool{
				{Name: "ok", DeliveryMode: "proxy", SpiffeBinding: "enabled"},
				{Name: "bad", DeliveryMode: "proxy", SpiffeBinding: "disabled"},
			},
			wantPass: false,
			wantSubs: []string{"SandboxTemplate bad"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := preflight.CheckCredentialDelivery(tc.pools, tc.tenancy, tc.devMode)
			if d.Passed != tc.wantPass {
				t.Fatalf("Passed = %v, want %v (reason=%q)", d.Passed, tc.wantPass, d.Reason)
			}
			for _, sub := range tc.wantSubs {
				if !strings.Contains(d.Reason, sub) {
					t.Errorf("reason %q missing %q", d.Reason, sub)
				}
			}
		})
	}
}

// TestRun_CredentialDeliveryScan_spec_4_9 verifies Run wires the
// credential-delivery scan: a multi-tenant install with a spiffe-disabled
// proxy pool fails the install, while a single-tenant install with the
// same pool passes (the scan enforces only in multi-tenant mode).
func TestRun_CredentialDeliveryScan_spec_4_9(t *testing.T) {
	baseline := append(allBaselineWebhooks(), phaseStampCM(map[string]string{}))
	leaky := deliveryTemplate("leaky", "proxy", "standard", "disabled", "")

	withLeaky := append([]client.Object{leaky}, baseline...)

	multi := preflight.Run(context.Background(), lennyClient(t, withLeaky...),
		preflight.Config{Namespace: preflightNS, TenancyMode: "multi"})
	d := findCheck(t, multi, "credential-delivery-multitenant")
	if d.Passed {
		t.Fatalf("multi-tenant scan must reject a proxy + spiffeBinding: disabled pool; got Passed=true")
	}
	if !strings.Contains(d.Reason, "ProxyModeSpiffeBindingDisabledMultiTenantRejected") {
		t.Fatalf("reason %q missing guard code", d.Reason)
	}

	single := preflight.Run(context.Background(), lennyClient(t, withLeaky...),
		preflight.Config{Namespace: preflightNS, TenancyMode: "single"})
	d = findCheck(t, single, "credential-delivery-multitenant")
	if !d.Passed {
		t.Fatalf("single-tenant scan must permit the combination; got Passed=false reason=%q", d.Reason)
	}
}

// TestRun_CredentialDeliveryScanListErrorFailsClosed_spec_4_9 pins the
// fail-closed posture that inverts the advisory node-drain-timeout
// sibling: a SandboxTemplateList error other than a missing CRD fails the
// install rather than passing advisory-only. A pre-fix scan that treated
// a list error as a pass would let this test's api-server-unavailable
// install proceed.
func TestRun_CredentialDeliveryScanListErrorFailsClosed_spec_4_9(t *testing.T) {
	baseline := append(baselineCRDs(), allBaselineWebhooks()...)
	baseline = append(baseline, phaseStampCM(map[string]string{}))
	c := fake.NewClientBuilder().
		WithScheme(runScheme(t)).
		WithObjects(baseline...).
		WithInterceptorFuncs(interceptor.Funcs{
			List: func(ctx context.Context, cl client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
				if _, ok := list.(*lennyv1.SandboxTemplateList); ok {
					return errors.New("api server unavailable")
				}
				return cl.List(ctx, list, opts...)
			},
		}).
		Build()

	report := preflight.Run(context.Background(), c,
		preflight.Config{Namespace: preflightNS, TenancyMode: "multi"})
	d := findCheck(t, report, "credential-delivery-multitenant")
	if d.Passed {
		t.Fatalf("a SandboxTemplateList read error must fail the scan closed; got Passed=true")
	}
	if !strings.Contains(d.Reason, "api server unavailable") {
		t.Fatalf("reason %q missing the list error", d.Reason)
	}
}

// TestRun_CredentialDeliveryScanMissingCRDPasses_spec_4_9 verifies a
// fresh install where the SandboxTemplate CRD is not yet applied treats
// the scan as "no pools" and passes cleanly, matching the
// node-drain-timeout gather's missing-CRD tolerance. The NotFound error
// stands in for the API server's response when the CRD is absent.
func TestRun_CredentialDeliveryScanMissingCRDPasses_spec_4_9(t *testing.T) {
	baseline := append(baselineCRDs(), allBaselineWebhooks()...)
	baseline = append(baseline, phaseStampCM(map[string]string{}))
	c := fake.NewClientBuilder().
		WithScheme(runScheme(t)).
		WithObjects(baseline...).
		WithInterceptorFuncs(interceptor.Funcs{
			List: func(ctx context.Context, cl client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
				if _, ok := list.(*lennyv1.SandboxTemplateList); ok {
					return apierrors.NewNotFound(
						schema.GroupResource{Group: "lenny.dev", Resource: "sandboxtemplates"}, "",
					)
				}
				return cl.List(ctx, list, opts...)
			},
		}).
		Build()

	report := preflight.Run(context.Background(), c,
		preflight.Config{Namespace: preflightNS, TenancyMode: "multi"})
	d := findCheck(t, report, "credential-delivery-multitenant")
	if !d.Passed {
		t.Fatalf("a missing SandboxTemplate CRD must be tolerated; got Passed=false reason=%q", d.Reason)
	}
}
