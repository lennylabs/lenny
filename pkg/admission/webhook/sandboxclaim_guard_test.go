// SPDX-License-Identifier: MIT

package webhook_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	admissionv1 "k8s.io/api/admission/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/lennylabs/lenny/pkg/admission/webhook"
	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
)

const guardNS = "lenny-agents"

func guardScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	if err := lennyv1.AddToScheme(s); err != nil {
		t.Fatalf("AddToScheme: %v", err)
	}
	return s
}

func claimRaw(t *testing.T, name, sandboxRef string) runtime.RawExtension {
	t.Helper()
	raw, err := json.Marshal(lennyv1.SandboxClaim{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: guardNS},
		Spec:       lennyv1.SandboxClaimSpec{SandboxRef: sandboxRef, SessionID: "sess-1"},
	})
	if err != nil {
		t.Fatalf("marshal claim: %v", err)
	}
	return runtime.RawExtension{Raw: raw}
}

func sandbox(name, phase string) *lennyv1.Sandbox {
	return &lennyv1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: guardNS},
		Status:     lennyv1.SandboxStatus{Phase: phase},
	}
}

func seededClaim(name, sandboxRef, phase string) *lennyv1.SandboxClaim {
	return &lennyv1.SandboxClaim{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: guardNS},
		Spec:       lennyv1.SandboxClaimSpec{SandboxRef: sandboxRef, SessionID: "seed"},
		Status:     lennyv1.SandboxClaimStatus{Phase: phase},
	}
}

func guardClient(t *testing.T, objs ...client.Object) client.Client {
	t.Helper()
	return fake.NewClientBuilder().WithScheme(guardScheme(t)).WithObjects(objs...).Build()
}

func TestSandboxClaimGuardAllowsCreateWithNoExistingClaim(t *testing.T) {
	c := guardClient(t, sandbox("sbx-1", "idle"))
	resp := webhook.SandboxClaimGuard(c)(context.Background(), &admissionv1.AdmissionRequest{
		UID:       "c1",
		Operation: admissionv1.Create,
		Namespace: guardNS,
		Object:    claimRaw(t, "claim-1", "sbx-1"),
	})
	if !resp.Allowed {
		t.Errorf("creating the first claim for a Sandbox should be allowed: %+v", resp.Result)
	}
}

func TestSandboxClaimGuardRejectsCreateWithExistingBoundClaim(t *testing.T) {
	c := guardClient(
		t,
		sandbox("sbx-1", "claimed"),
		seededClaim("claim-existing", "sbx-1", "bound"),
	)
	resp := webhook.SandboxClaimGuard(c)(context.Background(), &admissionv1.AdmissionRequest{
		UID:       "c2",
		Operation: admissionv1.Create,
		Namespace: guardNS,
		Object:    claimRaw(t, "claim-2", "sbx-1"),
	})
	if resp.Allowed {
		t.Fatal("a second claim for an already-claimed Sandbox should be rejected")
	}
	if resp.Result == nil || resp.Result.Code != http.StatusForbidden {
		t.Errorf("rejection result = %+v, want code 403", resp.Result)
	}
}

// TestSandboxClaimGuardAllowsCreateOnSlotActiveSandbox is the
// regression for the §5.2 concurrent-mode dispatch path. A Sandbox in
// `slot_active` phase already hosts a non-terminal claim from a prior
// slot reservation; the next dispatched session must be able to add
// its own claim without the §4.6.1 duplicate-claim rule rejecting it.
// The 100% error rate on every tier-7 cstateless / cworkspace
// scenario before this fix came from the webhook applying the
// session-mode rule uniformly.
func TestSandboxClaimGuardAllowsCreateOnSlotActiveSandbox(t *testing.T) {
	c := guardClient(
		t,
		sandbox("sbx-1", "slot_active"),
		seededClaim("claim-slot-1", "sbx-1", "active"),
	)
	resp := webhook.SandboxClaimGuard(c)(context.Background(), &admissionv1.AdmissionRequest{
		UID:       "cslot",
		Operation: admissionv1.Create,
		Namespace: guardNS,
		Object:    claimRaw(t, "claim-slot-2", "sbx-1"),
	})
	if !resp.Allowed {
		t.Errorf("a slot_active Sandbox must accept additional concurrent slot claims; got %+v", resp.Result)
	}
}

func TestSandboxClaimGuardAllowsCreateWhenExistingClaimTerminal(t *testing.T) {
	c := guardClient(
		t,
		sandbox("sbx-1", "idle"),
		seededClaim("claim-old", "sbx-1", "released"),
	)
	resp := webhook.SandboxClaimGuard(c)(context.Background(), &admissionv1.AdmissionRequest{
		UID:       "c3",
		Operation: admissionv1.Create,
		Namespace: guardNS,
		Object:    claimRaw(t, "claim-3", "sbx-1"),
	})
	if !resp.Allowed {
		t.Errorf("a released sibling claim must not block a fresh claim: %+v", resp.Result)
	}
}

func TestSandboxClaimGuardAllowsUpdateWhenSandboxClaimed(t *testing.T) {
	c := guardClient(t, sandbox("sbx-1", "claimed"))
	resp := webhook.SandboxClaimGuard(c)(context.Background(), &admissionv1.AdmissionRequest{
		UID:       "u1",
		Operation: admissionv1.Update,
		Namespace: guardNS,
		Object:    claimRaw(t, "claim-1", "sbx-1"),
	})
	if !resp.Allowed {
		t.Errorf("updating a claim whose Sandbox is claimed should be allowed: %+v", resp.Result)
	}
}

func TestSandboxClaimGuardRejectsUpdateWhenSandboxNotClaimed(t *testing.T) {
	c := guardClient(t, sandbox("sbx-1", "idle"))
	resp := webhook.SandboxClaimGuard(c)(context.Background(), &admissionv1.AdmissionRequest{
		UID:       "u2",
		Operation: admissionv1.Update,
		Namespace: guardNS,
		Object:    claimRaw(t, "claim-1", "sbx-1"),
	})
	if resp.Allowed {
		t.Fatal("a stale claim update against a non-claimed Sandbox should be rejected")
	}
	if resp.Result == nil || resp.Result.Code != http.StatusForbidden {
		t.Errorf("rejection result = %+v, want code 403", resp.Result)
	}
}

func TestSandboxClaimGuardRejectsUpdateWhenSandboxMissing(t *testing.T) {
	c := guardClient(t) // no Sandbox seeded
	resp := webhook.SandboxClaimGuard(c)(context.Background(), &admissionv1.AdmissionRequest{
		UID:       "u3",
		Operation: admissionv1.Update,
		Namespace: guardNS,
		Object:    claimRaw(t, "claim-1", "sbx-missing"),
	})
	if resp.Allowed {
		t.Fatal("updating a claim whose Sandbox no longer exists should be rejected")
	}
	if resp.Result == nil || resp.Result.Code != http.StatusForbidden {
		t.Errorf("rejection result = %+v, want code 403", resp.Result)
	}
}

func TestSandboxClaimGuardRejectsMalformedObject(t *testing.T) {
	c := guardClient(t)
	resp := webhook.SandboxClaimGuard(c)(context.Background(), &admissionv1.AdmissionRequest{
		UID:       "u4",
		Operation: admissionv1.Create,
		Namespace: guardNS,
		Object:    runtime.RawExtension{Raw: []byte("{not a claim")},
	})
	if resp.Allowed {
		t.Fatal("a malformed SandboxClaim object should be rejected")
	}
	if resp.Result == nil || resp.Result.Code != http.StatusBadRequest {
		t.Errorf("rejection result = %+v, want code 400", resp.Result)
	}
}
