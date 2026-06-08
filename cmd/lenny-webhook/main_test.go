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

	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
	"github.com/lennylabs/lenny/pkg/podsecurity"
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
	mux := newMux(nil, "multi", false, "", "", "", nil, nil, false, podsecurity.RuntimeClassPolicy{}, nil, nil, nil)
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
	newMux(nil, "multi", false, "", "", "", nil, nil, false, podsecurity.RuntimeClassPolicy{}, nil, nil, nil).ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/label-immutability", bytes.NewReader(body)))
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
			Kind:      metav1.GroupVersionKind{Group: "lenny.dev", Version: "v1alpha1", Kind: "SandboxTemplate"},
			Object:    runtime.RawExtension{Raw: tmplRaw},
		},
	}
	body, err := json.Marshal(review)
	if err != nil {
		t.Fatalf("marshal review: %v", err)
	}

	rr := httptest.NewRecorder()
	newMux(nil, "multi", false, "", "", "", nil, nil, false, podsecurity.RuntimeClassPolicy{}, nil, nil, nil).ServeHTTP(rr,
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

// TestMuxServesDrainReadiness posts an eviction AdmissionReview to the
// drain-readiness route and confirms the mux dispatches it to the
// webhook shim. With no gateway endpoint configured the §12.5 webhook
// blocks the drain fail-closed.
func TestMuxServesDrainReadiness(t *testing.T) {
	review := admissionv1.AdmissionReview{
		Request: &admissionv1.AdmissionRequest{
			UID:         "review-3",
			Operation:   admissionv1.Create,
			Namespace:   "lenny-agents",
			Name:        "agent-pod",
			SubResource: "eviction",
		},
	}
	body, err := json.Marshal(review)
	if err != nil {
		t.Fatalf("marshal review: %v", err)
	}

	rr := httptest.NewRecorder()
	newMux(nil, "multi", false, "", "", "", nil, nil, false, podsecurity.RuntimeClassPolicy{}, nil, nil, nil).ServeHTTP(rr,
		httptest.NewRequest(http.MethodPost, "/drain-readiness", bytes.NewReader(body)))
	if rr.Code != http.StatusOK {
		t.Fatalf("POST /drain-readiness = %d, want 200", rr.Code)
	}

	var out admissionv1.AdmissionReview
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if out.Response == nil || out.Response.Allowed {
		t.Errorf("response = %+v, want a fail-closed rejection with no reachable endpoint", out.Response)
	}
	if out.Response.UID != "review-3" {
		t.Errorf("response UID = %q, want review-3", out.Response.UID)
	}
}

func TestMuxRejectsUnknownRoute(t *testing.T) {
	rr := httptest.NewRecorder()
	newMux(nil, "multi", false, "", "", "", nil, nil, false, podsecurity.RuntimeClassPolicy{}, nil, nil, nil).ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/no-such-webhook", nil))
	if rr.Code != http.StatusNotFound {
		t.Errorf("POST /no-such-webhook = %d, want 404", rr.Code)
	}
}

// TestMuxServesDataResidencyValidator posts an AdmissionReview for a
// resource pinning an undeclared region to the §12.8 route and
// confirms the mux dispatches it and the webhook rejects fail-closed.
func TestMuxServesDataResidencyValidator(t *testing.T) {
	obj := map[string]any{
		"metadata": map[string]any{"name": "acme"},
		"spec":     map[string]any{"dataResidencyRegion": "ap-south-1"},
	}
	objRaw, err := json.Marshal(obj)
	if err != nil {
		t.Fatalf("marshal resource: %v", err)
	}
	review := admissionv1.AdmissionReview{
		Request: &admissionv1.AdmissionRequest{
			UID:       "review-residency",
			Operation: admissionv1.Create,
			Kind:      metav1.GroupVersionKind{Kind: "Tenant"},
			Object:    runtime.RawExtension{Raw: objRaw},
		},
	}
	body, err := json.Marshal(review)
	if err != nil {
		t.Fatalf("marshal review: %v", err)
	}

	rr := httptest.NewRecorder()
	// storage.regions declares only eu-west-1, so ap-south-1 is
	// unresolvable and rejected.
	newMux(nil, "multi", false, "", "", "", []string{"eu-west-1"}, nil, false, podsecurity.RuntimeClassPolicy{}, nil, nil, nil).ServeHTTP(rr,
		httptest.NewRequest(http.MethodPost, "/data-residency-validator", bytes.NewReader(body)))
	if rr.Code != http.StatusOK {
		t.Fatalf("POST /data-residency-validator = %d, want 200", rr.Code)
	}
	var out admissionv1.AdmissionReview
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if out.Response == nil || out.Response.Allowed {
		t.Errorf("response = %+v, want a rejection for an undeclared region", out.Response)
	}
	if out.Response.UID != "review-residency" {
		t.Errorf("response UID = %q, want review-residency", out.Response.UID)
	}
}

// TestMuxServesT4NodeIsolation posts an AdmissionReview for a T4 pod
// with no T4 scheduling constraints to the §6.4 route and confirms the
// mux dispatches it and the webhook rejects.
func TestMuxServesT4NodeIsolation(t *testing.T) {
	pod := corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "claude-t4-abc",
			Namespace: "lenny-agents-kata",
			Labels:    map[string]string{"lenny.dev/workspace-tier": "t4"},
		},
	}
	podRaw, err := json.Marshal(pod)
	if err != nil {
		t.Fatalf("marshal pod: %v", err)
	}
	review := admissionv1.AdmissionReview{
		Request: &admissionv1.AdmissionRequest{
			UID:       "review-t4",
			Operation: admissionv1.Create,
			Kind:      metav1.GroupVersionKind{Kind: "Pod"},
			Object:    runtime.RawExtension{Raw: podRaw},
		},
	}
	body, err := json.Marshal(review)
	if err != nil {
		t.Fatalf("marshal review: %v", err)
	}

	rr := httptest.NewRecorder()
	newMux(nil, "multi", false, "", "", "", nil, nil, false, podsecurity.RuntimeClassPolicy{}, nil, nil, nil).ServeHTTP(rr,
		httptest.NewRequest(http.MethodPost, "/t4-node-isolation", bytes.NewReader(body)))
	if rr.Code != http.StatusOK {
		t.Fatalf("POST /t4-node-isolation = %d, want 200", rr.Code)
	}
	var out admissionv1.AdmissionReview
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if out.Response == nil || out.Response.Allowed {
		t.Errorf("response = %+v, want a rejection for a T4 pod with no T4 scheduling", out.Response)
	}
	if out.Response.UID != "review-t4" {
		t.Errorf("response UID = %q, want review-t4", out.Response.UID)
	}
}

// TestMuxServesRegistryDigest posts an AdmissionReview for an agent
// pod with a tag-only image to the §13.1 registry-digest route with
// the requireDigest flag enabled and confirms the mux dispatches it
// and the webhook rejects.
func TestMuxServesRegistryDigest(t *testing.T) {
	pod := corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "agent-tag", Namespace: "lenny-agents"},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{Name: "runtime", Image: "ghcr.io/lennylabs/echo:latest"},
			},
		},
	}
	podRaw, err := json.Marshal(pod)
	if err != nil {
		t.Fatalf("marshal pod: %v", err)
	}
	review := admissionv1.AdmissionReview{
		Request: &admissionv1.AdmissionRequest{
			UID:       "review-rd",
			Operation: admissionv1.Create,
			Kind:      metav1.GroupVersionKind{Kind: "Pod"},
			Object:    runtime.RawExtension{Raw: podRaw},
		},
	}
	body, err := json.Marshal(review)
	if err != nil {
		t.Fatalf("marshal review: %v", err)
	}
	rr := httptest.NewRecorder()
	// requireDigest=true enables the §13.1 check.
	newMux(nil, "multi", false, "", "", "", nil, nil, true, podsecurity.RuntimeClassPolicy{}, nil, nil, nil).ServeHTTP(rr,
		httptest.NewRequest(http.MethodPost, "/registry-digest", bytes.NewReader(body)))
	if rr.Code != http.StatusOK {
		t.Fatalf("POST /registry-digest = %d, want 200", rr.Code)
	}
	var out admissionv1.AdmissionReview
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if out.Response == nil || out.Response.Allowed {
		t.Errorf("response = %+v, want a rejection for a tag-only image", out.Response)
	}
	if out.Response.UID != "review-rd" {
		t.Errorf("response UID = %q, want review-rd", out.Response.UID)
	}
}

// TestMuxRegistryDigestPassThroughWhenDisabled posts the same pod with
// requireDigest=false and confirms the route admits, matching the
// §13.1 default-off posture.
func TestMuxRegistryDigestPassThroughWhenDisabled(t *testing.T) {
	pod := corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "agent-tag", Namespace: "lenny-agents"},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{Name: "runtime", Image: "ghcr.io/lennylabs/echo:latest"},
			},
		},
	}
	podRaw, _ := json.Marshal(pod)
	body, _ := json.Marshal(admissionv1.AdmissionReview{
		Request: &admissionv1.AdmissionRequest{
			UID:       "review-rd-disabled",
			Operation: admissionv1.Create,
			Kind:      metav1.GroupVersionKind{Kind: "Pod"},
			Object:    runtime.RawExtension{Raw: podRaw},
		},
	})
	rr := httptest.NewRecorder()
	newMux(nil, "multi", false, "", "", "", nil, nil, false, podsecurity.RuntimeClassPolicy{}, nil, nil, nil).ServeHTTP(rr,
		httptest.NewRequest(http.MethodPost, "/registry-digest", bytes.NewReader(body)))
	var out admissionv1.AdmissionReview
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if out.Response == nil || !out.Response.Allowed {
		t.Errorf("response = %+v, want admit when requireDigest is false", out.Response)
	}
}

func TestParseRegions(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"", nil},
		{"   ", nil},
		{"eu-west-1", []string{"eu-west-1"}},
		{"eu-west-1,us-east-1", []string{"eu-west-1", "us-east-1"}},
		{" eu-west-1 , us-east-1 ,", []string{"eu-west-1", "us-east-1"}},
	}
	for _, c := range cases {
		got := parseRegions(c.in)
		if len(got) != len(c.want) {
			t.Errorf("parseRegions(%q) = %v, want %v", c.in, got, c.want)
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("parseRegions(%q)[%d] = %q, want %q", c.in, i, got[i], c.want[i])
			}
		}
	}
}
