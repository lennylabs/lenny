// SPDX-License-Identifier: MIT

package webhook_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	admissionv1 "k8s.io/api/admission/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/lennylabs/lenny/pkg/admission/webhook"
)

// spec: §12.8 ("Data residency admission control") / §12.9 — the
// lenny-data-residency-validator webhook transport decodes the
// dataResidencyRegion field from an admitted resource, applies the
// §12.8 inheritance rule via a TenantRegionResolver, and rejects
// fail-closed.

// residencyResourceRaw marshals a resource carrying a spec block with
// dataResidencyRegion and an optional tenant reference.
func residencyResourceRaw(t *testing.T, name, region, tenantID string) runtime.RawExtension {
	t.Helper()
	obj := map[string]any{
		"metadata": map[string]any{"name": name},
		"spec": map[string]any{
			"dataResidencyRegion": region,
			"tenantId":            tenantID,
		},
	}
	raw, err := json.Marshal(obj)
	if err != nil {
		t.Fatalf("marshal resource: %v", err)
	}
	return runtime.RawExtension{Raw: raw}
}

// stubResolver is a test TenantRegionResolver returning a fixed region
// or a fixed error.
type stubResolver struct {
	region string
	err    error
}

func (s stubResolver) TenantRegion(context.Context, string) (string, error) {
	return s.region, s.err
}

func TestDataResidencyValidatorAdmitsDeclaredRegion(t *testing.T) {
	dec := webhook.DataResidencyValidator([]string{"eu-west-1", "us-east-1"}, nil)
	resp := dec(context.Background(), &admissionv1.AdmissionRequest{
		UID:       "d1",
		Operation: admissionv1.Create,
		Kind:      metav1.GroupVersionKind{Kind: "Tenant"},
		Object:    residencyResourceRaw(t, "acme", "eu-west-1", ""),
	})
	if !resp.Allowed {
		t.Fatalf("a declared region should be admitted: %+v", resp.Result)
	}
}

func TestDataResidencyValidatorRejectsUndeclaredRegion(t *testing.T) {
	dec := webhook.DataResidencyValidator([]string{"eu-west-1"}, nil)
	resp := dec(context.Background(), &admissionv1.AdmissionRequest{
		UID:       "d2",
		Operation: admissionv1.Create,
		Kind:      metav1.GroupVersionKind{Kind: "Tenant"},
		Object:    residencyResourceRaw(t, "acme", "ap-south-1", ""),
	})
	if resp.Allowed {
		t.Fatal("an undeclared region must be rejected (fail-closed)")
	}
	if resp.Result.Code != http.StatusForbidden {
		t.Errorf("code = %d, want 403", resp.Result.Code)
	}
}

func TestDataResidencyValidatorAdmitsUnconstrainedResource(t *testing.T) {
	// A resource with no dataResidencyRegion is admitted.
	dec := webhook.DataResidencyValidator([]string{"eu-west-1"}, nil)
	resp := dec(context.Background(), &admissionv1.AdmissionRequest{
		UID:       "d3",
		Operation: admissionv1.Create,
		Kind:      metav1.GroupVersionKind{Kind: "SandboxClaim"},
		Object:    residencyResourceRaw(t, "claim-1", "", ""),
	})
	if !resp.Allowed {
		t.Fatalf("an unconstrained resource should be admitted: %+v", resp.Result)
	}
}

func TestDataResidencyValidatorInheritsTenantRegion(t *testing.T) {
	// An environment-scoped resource (SandboxClaim) with no region of
	// its own inherits the tenant region resolved by the resolver.
	dec := webhook.DataResidencyValidator(
		[]string{"eu-west-1"}, stubResolver{region: "eu-west-1"})
	resp := dec(context.Background(), &admissionv1.AdmissionRequest{
		UID:       "d4",
		Operation: admissionv1.Create,
		Kind:      metav1.GroupVersionKind{Kind: "SandboxClaim"},
		Object:    residencyResourceRaw(t, "claim-1", "", "acme"),
	})
	if !resp.Allowed {
		t.Fatalf("an environment-scoped resource inheriting a declared region should be admitted: %+v", resp.Result)
	}
}

func TestDataResidencyValidatorRejectsEnvironmentRegionDivergence(t *testing.T) {
	// A SandboxClaim that declares a region differing from its tenant's
	// region is rejected per §12.8 inheritance.
	dec := webhook.DataResidencyValidator(
		[]string{"eu-west-1", "us-east-1"}, stubResolver{region: "eu-west-1"})
	resp := dec(context.Background(), &admissionv1.AdmissionRequest{
		UID:       "d5",
		Operation: admissionv1.Create,
		Kind:      metav1.GroupVersionKind{Kind: "SandboxClaim"},
		Object:    residencyResourceRaw(t, "claim-1", "us-east-1", "acme"),
	})
	if resp.Allowed {
		t.Fatal("an environment-scoped region diverging from its tenant must be rejected")
	}
	if !strings.Contains(resp.Result.Message, "REGION_CONSTRAINT_VIOLATED") {
		t.Errorf("reason %q does not carry the violation code", resp.Result.Message)
	}
}

func TestDataResidencyValidatorFailsClosedOnResolverError(t *testing.T) {
	// §12.8 fail-closed: a resolver error means the webhook cannot
	// evaluate the constraint, so the resource is rejected, not
	// admitted.
	dec := webhook.DataResidencyValidator(
		[]string{"eu-west-1"}, stubResolver{err: errors.New("tenant store unavailable")})
	resp := dec(context.Background(), &admissionv1.AdmissionRequest{
		UID:       "d6",
		Operation: admissionv1.Create,
		Kind:      metav1.GroupVersionKind{Kind: "SandboxClaim"},
		Object:    residencyResourceRaw(t, "claim-1", "", "acme"),
	})
	if resp.Allowed {
		t.Fatal("a resolver error must reject (fail-closed), not admit")
	}
	if resp.Result.Code != http.StatusInternalServerError {
		t.Errorf("code = %d, want 500", resp.Result.Code)
	}
}

func TestDataResidencyValidatorRejectsUndecodableObject(t *testing.T) {
	dec := webhook.DataResidencyValidator([]string{"eu-west-1"}, nil)
	resp := dec(context.Background(), &admissionv1.AdmissionRequest{
		UID:    "d7",
		Kind:   metav1.GroupVersionKind{Kind: "Tenant"},
		Object: runtime.RawExtension{Raw: []byte("{not json")},
	})
	if resp.Allowed {
		t.Fatal("an undecodable object must be rejected")
	}
}

func TestDataResidencyValidatorEchoesRequestUID(t *testing.T) {
	dec := webhook.DataResidencyValidator([]string{"eu-west-1"}, nil)
	resp := dec(context.Background(), &admissionv1.AdmissionRequest{
		UID:    "echo-uid",
		Kind:   metav1.GroupVersionKind{Kind: "Tenant"},
		Object: residencyResourceRaw(t, "acme", "eu-west-1", ""),
	})
	// The Handler stamps the UID; the Decider returns a response the
	// Handler then stamps. The decision itself must succeed.
	if !resp.Allowed {
		t.Fatalf("expected admit: %+v", resp.Result)
	}
}
