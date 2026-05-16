// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	admissionv1 "k8s.io/api/admission/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1"
)

func TestBuildSchemeRegistersWebhookTypes(t *testing.T) {
	s := buildScheme()
	for _, obj := range []runtime.Object{
		&lennyv1.Sandbox{},
		&lennyv1.SandboxClaim{},
	} {
		if _, _, err := s.ObjectKinds(obj); err != nil {
			t.Errorf("%T is not registered with the webhook scheme: %v", obj, err)
		}
	}
}

func TestMuxHealthEndpoints(t *testing.T) {
	mux := newMux(nil, "multi", false)
	for _, path := range []string{"/healthz", "/readyz"} {
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, path, nil))
		if rr.Code != http.StatusOK {
			t.Errorf("GET %s = %d, want 200", path, rr.Code)
		}
	}
}

// TestMuxServesLabelImmutability posts a real AdmissionReview to the
// label-immutability route and confirms the mux dispatches it to the
// webhook shim, which admits a pod with no watched-label changes.
func TestMuxServesLabelImmutability(t *testing.T) {
	pod := corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "agent", Namespace: "lenny-agents"},
	}
	podRaw, err := json.Marshal(pod)
	if err != nil {
		t.Fatalf("marshal pod: %v", err)
	}
	review := admissionv1.AdmissionReview{
		Request: &admissionv1.AdmissionRequest{
			UID:       "review-1",
			Operation: admissionv1.Create,
			Object:    runtime.RawExtension{Raw: podRaw},
		},
	}
	body, err := json.Marshal(review)
	if err != nil {
		t.Fatalf("marshal review: %v", err)
	}

	rr := httptest.NewRecorder()
	newMux(nil, "multi", false).ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/label-immutability", bytes.NewReader(body)))
	if rr.Code != http.StatusOK {
		t.Fatalf("POST /label-immutability = %d, want 200", rr.Code)
	}

	var out admissionv1.AdmissionReview
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if out.Response == nil || !out.Response.Allowed {
		t.Errorf("response = %+v, want admitted", out.Response)
	}
	if out.Response.UID != "review-1" {
		t.Errorf("response UID = %q, want review-1", out.Response.UID)
	}
}

// TestMuxServesDirectModeIsolation posts a real AdmissionReview to the
// direct-mode-isolation route and confirms the mux dispatches it to the
// webhook shim, which rejects a multi-tenant direct/standard template.
func TestMuxServesDirectModeIsolation(t *testing.T) {
	tmpl := lennyv1.SandboxTemplate{
		ObjectMeta: metav1.ObjectMeta{Name: "claude-pool", Namespace: "lenny-agents"},
		Spec: lennyv1.SandboxTemplateSpec{
			RuntimeRef:       "claude-code",
			DeliveryMode:     "direct",
			IsolationProfile: "standard",
		},
	}
	tmplRaw, err := json.Marshal(tmpl)
	if err != nil {
		t.Fatalf("marshal template: %v", err)
	}
	review := admissionv1.AdmissionReview{
		Request: &admissionv1.AdmissionRequest{
			UID:       "review-2",
			Operation: admissionv1.Create,
			Kind:      metav1.GroupVersionKind{Group: "lenny.dev", Version: "v1", Kind: "SandboxTemplate"},
			Object:    runtime.RawExtension{Raw: tmplRaw},
		},
	}
	body, err := json.Marshal(review)
	if err != nil {
		t.Fatalf("marshal review: %v", err)
	}

	rr := httptest.NewRecorder()
	newMux(nil, "multi", false).ServeHTTP(rr,
		httptest.NewRequest(http.MethodPost, "/direct-mode-isolation", bytes.NewReader(body)))
	if rr.Code != http.StatusOK {
		t.Fatalf("POST /direct-mode-isolation = %d, want 200", rr.Code)
	}

	var out admissionv1.AdmissionReview
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if out.Response == nil || out.Response.Allowed {
		t.Errorf("response = %+v, want a rejection for direct + standard in multi-tenant mode", out.Response)
	}
	if out.Response.UID != "review-2" {
		t.Errorf("response UID = %q, want review-2", out.Response.UID)
	}
}

func TestMuxRejectsUnknownRoute(t *testing.T) {
	rr := httptest.NewRecorder()
	newMux(nil, "multi", false).ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/no-such-webhook", nil))
	if rr.Code != http.StatusNotFound {
		t.Errorf("POST /no-such-webhook = %d, want 404", rr.Code)
	}
}
