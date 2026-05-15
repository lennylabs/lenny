// SPDX-License-Identifier: MIT

package webhook_test

import (
	"encoding/json"
	"net/http"
	"testing"

	admissionv1 "k8s.io/api/admission/v1"
	authnv1 "k8s.io/api/authentication/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	labelimm "github.com/lennylabs/lenny/pkg/admission/label_immutability"
	"github.com/lennylabs/lenny/pkg/admission/webhook"
)

func podRaw(t *testing.T, labels map[string]string) runtime.RawExtension {
	t.Helper()
	raw, err := json.Marshal(corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "agent-pod",
			Namespace: "lenny-agents",
			Labels:    labels,
		},
	})
	if err != nil {
		t.Fatalf("marshal pod: %v", err)
	}
	return runtime.RawExtension{Raw: raw}
}

func TestLabelImmutabilityAllowsTenantAssignmentByGateway(t *testing.T) {
	resp := webhook.LabelImmutability()(&admissionv1.AdmissionRequest{
		UID:       "u1",
		Operation: admissionv1.Create,
		Object:    podRaw(t, map[string]string{labelimm.LabelTenantID: "acme"}),
		UserInfo:  authnv1.UserInfo{Username: labelimm.GatewayServiceAccount},
	})
	if !resp.Allowed {
		t.Errorf("gateway SA assigning tenant-id on CREATE should be allowed: %+v", resp.Result)
	}
}

func TestLabelImmutabilityRejectsTenantAssignmentByOtherUser(t *testing.T) {
	resp := webhook.LabelImmutability()(&admissionv1.AdmissionRequest{
		UID:       "u2",
		Operation: admissionv1.Create,
		Object:    podRaw(t, map[string]string{labelimm.LabelTenantID: "acme"}),
		UserInfo:  authnv1.UserInfo{Username: "system:serviceaccount:lenny-system:someone-else"},
	})
	if resp.Allowed {
		t.Fatal("a non-gateway SA assigning tenant-id should be rejected")
	}
	if resp.Result == nil || resp.Result.Code != http.StatusForbidden {
		t.Errorf("rejection result = %+v, want code 403", resp.Result)
	}
}

func TestLabelImmutabilityRejectsImmutableLabelChange(t *testing.T) {
	resp := webhook.LabelImmutability()(&admissionv1.AdmissionRequest{
		UID:       "u3",
		Operation: admissionv1.Update,
		OldObject: podRaw(t, map[string]string{labelimm.LabelManaged: "true"}),
		Object:    podRaw(t, map[string]string{labelimm.LabelManaged: "false"}),
		UserInfo:  authnv1.UserInfo{Username: labelimm.WarmPoolControllerSA},
	})
	if resp.Allowed {
		t.Fatal("mutating the immutable lenny.dev/managed label should be rejected")
	}
	if resp.Result == nil || resp.Result.Code != http.StatusForbidden {
		t.Errorf("rejection result = %+v, want code 403", resp.Result)
	}
}

func TestLabelImmutabilityAllowsUnchangedUpdate(t *testing.T) {
	labels := map[string]string{labelimm.LabelManaged: "true", labelimm.LabelTenantID: "acme"}
	resp := webhook.LabelImmutability()(&admissionv1.AdmissionRequest{
		UID:       "u4",
		Operation: admissionv1.Update,
		OldObject: podRaw(t, labels),
		Object:    podRaw(t, labels),
		UserInfo:  authnv1.UserInfo{Username: labelimm.WarmPoolControllerSA},
	})
	if !resp.Allowed {
		t.Errorf("an update that changes no watched label should be allowed: %+v", resp.Result)
	}
}

func TestLabelImmutabilityRejectsCrossTenantChange(t *testing.T) {
	resp := webhook.LabelImmutability()(&admissionv1.AdmissionRequest{
		UID:       "u5",
		Operation: admissionv1.Update,
		OldObject: podRaw(t, map[string]string{labelimm.LabelTenantID: "acme"}),
		Object:    podRaw(t, map[string]string{labelimm.LabelTenantID: "globex"}),
		UserInfo:  authnv1.UserInfo{Username: labelimm.GatewayServiceAccount},
	})
	if resp.Allowed {
		t.Fatal("re-pointing tenant-id at a different tenant should be rejected")
	}
	if resp.Result == nil || resp.Result.Code != http.StatusForbidden {
		t.Errorf("rejection result = %+v, want code 403", resp.Result)
	}
}

func TestLabelImmutabilityAllowsPoolReturnByController(t *testing.T) {
	resp := webhook.LabelImmutability()(&admissionv1.AdmissionRequest{
		UID:       "u6",
		Operation: admissionv1.Update,
		OldObject: podRaw(t, map[string]string{labelimm.LabelTenantID: "acme"}),
		Object:    podRaw(t, map[string]string{labelimm.LabelTenantID: labelimm.UnassignedTenantID}),
		UserInfo:  authnv1.UserInfo{Username: labelimm.WarmPoolControllerSA},
	})
	if !resp.Allowed {
		t.Errorf("the WarmPoolController returning a pod to the pool should be allowed: %+v", resp.Result)
	}
}

func TestLabelImmutabilityRejectsMalformedObject(t *testing.T) {
	resp := webhook.LabelImmutability()(&admissionv1.AdmissionRequest{
		UID:       "u7",
		Operation: admissionv1.Create,
		Object:    runtime.RawExtension{Raw: []byte("{not a pod")},
		UserInfo:  authnv1.UserInfo{Username: labelimm.GatewayServiceAccount},
	})
	if resp.Allowed {
		t.Fatal("a malformed pod object should be rejected, not admitted")
	}
	if resp.Result == nil || resp.Result.Code != http.StatusBadRequest {
		t.Errorf("rejection result = %+v, want code 400", resp.Result)
	}
}
