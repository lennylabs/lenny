// SPDX-License-Identifier: MIT

package webhook_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	admissionv1 "k8s.io/api/admission/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

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
		Spec:       lennyv1.SandboxClaimSpec{SandboxRef: sandboxRef},
	})
	if err != nil {
		t.Fatalf("marshal claim: %v", err)
	}
	return runtime.RawExtension{Raw: raw}
}

func seededClaim(name, sandboxRef, phase string) *lennyv1.SandboxClaim {
	return &lennyv1.SandboxClaim{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: guardNS},
		Spec:       lennyv1.SandboxClaimSpec{SandboxRef: sandboxRef},
		Status:     lennyv1.SandboxClaimStatus{Phase: phase},
	}
}

func guardClient(t *testing.T, objs ...client.Object) client.Client {
	t.Helper()
	return fake.NewClientBuilder().WithScheme(guardScheme(t)).WithObjects(objs...).Build()
}

// spec: §4.6.1 — the first claim for a Sandbox is admitted.
func TestSandboxClaimGuardAllowsCreateWithNoExistingClaim(t *testing.T) {
	c := guardClient(t)
	resp := webhook.SandboxClaimGuard(c)(context.Background(), &admissionv1.AdmissionRequest{
		UID:       "c1",
		Operation: admissionv1.Create,
		Namespace: guardNS,
		Object:    claimRaw(t, "claim-pod-1", "sbx-1"),
	})
	if !resp.Allowed {
		t.Errorf("creating the first claim for a Sandbox should be allowed: %+v", resp.Result)
	}
}

// spec: §4.6.1 — a second non-terminal claim for the same Sandbox is
// rejected with 403. The guard reads no Sandbox phase, so the rejection
// holds regardless of any Sandbox.status.phase value.
func TestSandboxClaimGuardRejectsCreateWithExistingBoundClaim(t *testing.T) {
	c := guardClient(t, seededClaim("claim-pod-existing", "sbx-1", "bound"))
	resp := webhook.SandboxClaimGuard(c)(context.Background(), &admissionv1.AdmissionRequest{
		UID:       "c2",
		Operation: admissionv1.Create,
		Namespace: guardNS,
		Object:    claimRaw(t, "claim-pod-2", "sbx-1"),
	})
	if resp.Allowed {
		t.Fatal("a second claim for an already-claimed Sandbox should be rejected")
	}
	if resp.Result == nil || resp.Result.Code != http.StatusForbidden {
		t.Errorf("rejection result = %+v, want code 403", resp.Result)
	}
}

// TestSandboxClaimGuardRejectsConcurrentClaimNoExemption is the
// proposal 0002 regression: per-pod uniqueness has no concurrency
// exemption. A pool that multiplexes multiple concurrent sessions onto
// one per-pod claim (§5.2) still produces exactly one SandboxClaim, so a
// second non-terminal claim for the same Sandbox is a duplicate. spec:
// §4.6.1, §5.2.
func TestSandboxClaimGuardRejectsConcurrentClaimNoExemption(t *testing.T) {
	c := guardClient(t, seededClaim("claim-pod-slot-1", "sbx-1", "bound"))
	resp := webhook.SandboxClaimGuard(c)(context.Background(), &admissionv1.AdmissionRequest{
		UID:       "cslot",
		Operation: admissionv1.Create,
		Namespace: guardNS,
		Object:    claimRaw(t, "claim-pod-slot-2", "sbx-1"),
	})
	if resp.Allowed {
		t.Fatalf("a concurrent-pool Sandbox carries one per-pod claim; a second claim must be rejected: %+v", resp.Result)
	}
	if resp.Result == nil || resp.Result.Code != http.StatusForbidden {
		t.Errorf("rejection result = %+v, want code 403", resp.Result)
	}
}

// spec: §4.6.1 — a terminal sibling claim does not block a fresh claim.
func TestSandboxClaimGuardAllowsCreateWhenExistingClaimTerminal(t *testing.T) {
	c := guardClient(t, seededClaim("claim-pod-old", "sbx-1", "released"))
	resp := webhook.SandboxClaimGuard(c)(context.Background(), &admissionv1.AdmissionRequest{
		UID:       "c3",
		Operation: admissionv1.Create,
		Namespace: guardNS,
		Object:    claimRaw(t, "claim-pod-3", "sbx-1"),
	})
	if !resp.Allowed {
		t.Errorf("a released sibling claim must not block a fresh claim: %+v", resp.Result)
	}
}

// spec: §4.6.1 — a sibling claim targeting a different Sandbox does not
// block a CREATE for this Sandbox.
func TestSandboxClaimGuardIgnoresClaimsForOtherSandbox(t *testing.T) {
	c := guardClient(t, seededClaim("claim-pod-other", "sbx-other", "bound"))
	resp := webhook.SandboxClaimGuard(c)(context.Background(), &admissionv1.AdmissionRequest{
		UID:       "c4",
		Operation: admissionv1.Create,
		Namespace: guardNS,
		Object:    claimRaw(t, "claim-pod-1", "sbx-1"),
	})
	if !resp.Allowed {
		t.Errorf("a claim for a different Sandbox must not block this CREATE: %+v", resp.Result)
	}
}

// TestSandboxClaimGuardRejectsNonCreateOperation guards the CREATE-only
// contract: the webhook is registered on CREATE only, so any other
// operation reaching the handler is rejected. spec: §4.6.1 — PATCH/PUT
// are admitted without inspection and are not registered with the
// webhook.
func TestSandboxClaimGuardRejectsNonCreateOperation(t *testing.T) {
	c := guardClient(t)
	for _, op := range []admissionv1.Operation{admissionv1.Update, admissionv1.Delete, admissionv1.Connect} {
		t.Run(string(op), func(t *testing.T) {
			resp := webhook.SandboxClaimGuard(c)(context.Background(), &admissionv1.AdmissionRequest{
				UID:       "u1",
				Operation: op,
				Namespace: guardNS,
				Object:    claimRaw(t, "claim-pod-1", "sbx-1"),
			})
			if resp.Allowed {
				t.Fatalf("operation %q must not be handled by the CREATE-only guard", op)
			}
			if resp.Result == nil || resp.Result.Code != http.StatusBadRequest {
				t.Errorf("rejection result = %+v, want code 400", resp.Result)
			}
		})
	}
}

// TestSandboxClaimGuardWireContractCreateRoundTrip is the contract-tier
// check: the guard Decider, wrapped by the shared AdmissionReview
// Handler, accepts a v1 AdmissionReview over the wire, echoes the
// request UID, and carries the §4.6.1 duplicate-claim 403 in the
// response body. spec: §4.6.1 (sandboxclaim-guard webhook AdmissionReview
// contract).
func TestSandboxClaimGuardWireContractCreateRoundTrip(t *testing.T) {
	c := guardClient(t, seededClaim("claim-pod-existing", "sbx-1", "bound"))
	h := webhook.Handler(webhook.SandboxClaimGuard(c))

	review := admissionv1.AdmissionReview{
		Request: &admissionv1.AdmissionRequest{
			UID:       "wire-uid",
			Operation: admissionv1.Create,
			Namespace: guardNS,
			Object:    claimRaw(t, "claim-pod-2", "sbx-1"),
		},
	}
	body, err := json.Marshal(review)
	if err != nil {
		t.Fatalf("marshal review: %v", err)
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/sandboxclaim-guard", bytes.NewReader(body)))

	if rr.Code != http.StatusOK {
		t.Fatalf("HTTP status = %d, want 200 (the admission verdict rides in the body)", rr.Code)
	}
	var out admissionv1.AdmissionReview
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode response review: %v\nbody: %s", err, rr.Body.String())
	}
	if out.Response == nil {
		t.Fatal("response review carries no Response")
	}
	if out.Response.UID != "wire-uid" {
		t.Errorf("response UID = %q, want the request UID wire-uid", out.Response.UID)
	}
	if out.Response.Allowed {
		t.Fatal("a duplicate per-pod claim must be denied over the wire")
	}
	if out.Response.Result == nil || out.Response.Result.Code != http.StatusForbidden {
		t.Errorf("denial result = %+v, want code 403", out.Response.Result)
	}
}

// TestSandboxClaimGuardFailsClosedOnAPIServerError covers the
// fail-closed path: when the sibling-claim List against the API server
// fails, the guard rejects the CREATE with 500 rather than admitting it.
// The webhook is deployed failurePolicy: Fail, so a read error during
// admission must deny. spec: §4.6.1 (sandboxclaim-guard webhook,
// fail-closed).
func TestSandboxClaimGuardFailsClosedOnAPIServerError(t *testing.T) {
	c := fake.NewClientBuilder().
		WithScheme(guardScheme(t)).
		WithInterceptorFuncs(interceptor.Funcs{
			List: func(context.Context, client.WithWatch, client.ObjectList, ...client.ListOption) error {
				return errors.New("apiserver unreachable")
			},
		}).
		Build()
	resp := webhook.SandboxClaimGuard(c)(context.Background(), &admissionv1.AdmissionRequest{
		UID:       "err1",
		Operation: admissionv1.Create,
		Namespace: guardNS,
		Object:    claimRaw(t, "claim-pod-1", "sbx-1"),
	})
	if resp.Allowed {
		t.Fatal("a sibling-claim List failure must fail closed, not admit the CREATE")
	}
	if resp.Result == nil || resp.Result.Code != http.StatusInternalServerError {
		t.Errorf("rejection result = %+v, want code 500", resp.Result)
	}
}

// spec: §4.6.1 — a malformed object is rejected with 400 before any
// rule evaluation.
func TestSandboxClaimGuardRejectsMalformedObject(t *testing.T) {
	c := guardClient(t)
	resp := webhook.SandboxClaimGuard(c)(context.Background(), &admissionv1.AdmissionRequest{
		UID:       "m1",
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
