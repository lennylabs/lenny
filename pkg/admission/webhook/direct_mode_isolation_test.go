// SPDX-License-Identifier: MIT

package webhook_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	admissionv1 "k8s.io/api/admission/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	dmi "github.com/lennylabs/lenny/pkg/admission/direct_mode_isolation"
	"github.com/lennylabs/lenny/pkg/admission/webhook"
	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1"
)

// templateObject marshals a SandboxTemplate with the given
// credential-delivery fields into an admission request object.
func templateObject(t *testing.T, deliveryMode, isolationProfile, spiffeBinding string) runtime.RawExtension {
	t.Helper()
	raw, err := json.Marshal(lennyv1.SandboxTemplate{
		Spec: lennyv1.SandboxTemplateSpec{
			RuntimeRef:       "claude-code",
			DeliveryMode:     deliveryMode,
			IsolationProfile: isolationProfile,
			SpiffeBinding:    spiffeBinding,
		},
	})
	if err != nil {
		t.Fatalf("marshal SandboxTemplate: %v", err)
	}
	return runtime.RawExtension{Raw: raw}
}

// directModeDecide runs the DirectModeIsolation decider for the given
// platform tenancy configuration and SandboxTemplate object.
func directModeDecide(tenancyMode string, devMode bool, obj runtime.RawExtension) *admissionv1.AdmissionResponse {
	decide := webhook.DirectModeIsolation(tenancyMode, devMode)
	return decide(context.Background(), &admissionv1.AdmissionRequest{
		Operation: admissionv1.Create,
		Kind:      metav1.GroupVersionKind{Group: "lenny.dev", Version: "v1", Kind: "SandboxTemplate"},
		Object:    obj,
	})
}

func TestDirectModeIsolationRejectsDirectStandardInMultiTenant(t *testing.T) {
	resp := directModeDecide("multi", false, templateObject(t, "direct", "standard", ""))
	if resp.Allowed {
		t.Fatal("direct + standard SandboxTemplate admitted in multi-tenant mode")
	}
	if resp.Result == nil || resp.Result.Code != 403 {
		t.Fatalf("response = %+v, want a 403 rejection", resp.Result)
	}
	if !strings.Contains(resp.Result.Message, dmi.RejectDirectModeStandardIsolation) {
		t.Errorf("message %q does not carry the rejection code", resp.Result.Message)
	}
}

func TestDirectModeIsolationRejectsProxySpiffeDisabledInMultiTenant(t *testing.T) {
	resp := directModeDecide("multi", false, templateObject(t, "proxy", "sandboxed", "disabled"))
	if resp.Allowed {
		t.Fatal("proxy + spiffeBinding disabled SandboxTemplate admitted in multi-tenant mode")
	}
	if resp.Result == nil || !strings.Contains(resp.Result.Message, dmi.RejectProxyModeSpiffeBindingDisabled) {
		t.Errorf("response %+v does not carry the spiffe-binding rejection code", resp.Result)
	}
}

func TestDirectModeIsolationAdmitsSafeTemplate(t *testing.T) {
	resp := directModeDecide("multi", false, templateObject(t, "proxy", "sandboxed", "enabled"))
	if !resp.Allowed {
		t.Errorf("safe SandboxTemplate rejected: %+v", resp.Result)
	}
}

func TestDirectModeIsolationAdmitsEverythingInSingleTenant(t *testing.T) {
	resp := directModeDecide("single", false, templateObject(t, "direct", "standard", "disabled"))
	if !resp.Allowed {
		t.Errorf("single-tenant deployment should admit every template, got %+v", resp.Result)
	}
}

// spec: §13.2 lines 438-442 (NET-006) — the transport threads
// tmpl.Spec.EgressProfile into the decider, and the proxy/provider-direct
// mutual exclusivity is enforced even in single-tenant mode (it is not
// gated on tenancy).
func TestDirectModeIsolationRejectsProxyProviderDirect_spec_13_2_NET006(t *testing.T) {
	raw, err := json.Marshal(lennyv1.SandboxTemplate{
		Spec: lennyv1.SandboxTemplateSpec{
			RuntimeRef:    "claude-code",
			DeliveryMode:  "proxy",
			EgressProfile: "provider-direct",
		},
	})
	if err != nil {
		t.Fatalf("marshal SandboxTemplate: %v", err)
	}
	for _, tenancy := range []string{"multi", "single"} {
		resp := directModeDecide(tenancy, false, runtime.RawExtension{Raw: raw})
		if resp.Allowed {
			t.Fatalf("%s-tenant: proxy + provider-direct SandboxTemplate admitted, want NET-006 rejection", tenancy)
		}
		if resp.Result == nil || resp.Result.Code != 403 {
			t.Fatalf("%s-tenant: response = %+v, want a 403 rejection", tenancy, resp.Result)
		}
		if !strings.Contains(resp.Result.Message, dmi.RejectInvalidPoolEgressDeliveryCombo) {
			t.Errorf("%s-tenant: message %q does not carry the NET-006 rejection code", tenancy, resp.Result.Message)
		}
	}
}

func TestDirectModeIsolationRejectsMalformedObject(t *testing.T) {
	resp := directModeDecide("multi", false, runtime.RawExtension{Raw: []byte(`{not json`)})
	if resp.Allowed {
		t.Fatal("malformed SandboxTemplate admitted")
	}
	if resp.Result == nil || resp.Result.Code != 400 {
		t.Errorf("response = %+v, want a 400 rejection", resp.Result)
	}
}
