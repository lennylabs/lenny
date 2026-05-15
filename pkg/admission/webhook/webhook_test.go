// SPDX-License-Identifier: MIT

package webhook_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	admissionv1 "k8s.io/api/admission/v1"

	"github.com/lennylabs/lenny/pkg/admission/webhook"
)

func postReview(t *testing.T, h http.Handler, review admissionv1.AdmissionReview) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(review)
	if err != nil {
		t.Fatalf("marshal review: %v", err)
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body)))
	return rr
}

func decodeReview(t *testing.T, rr *httptest.ResponseRecorder) admissionv1.AdmissionReview {
	t.Helper()
	var out admissionv1.AdmissionReview
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode response review: %v\nbody: %s", err, rr.Body.String())
	}
	return out
}

func TestHandlerAdmitsAndEchoesUID(t *testing.T) {
	h := webhook.Handler(func(context.Context, *admissionv1.AdmissionRequest) *admissionv1.AdmissionResponse {
		return webhook.Allow()
	})
	rr := postReview(t, h, admissionv1.AdmissionReview{
		Request: &admissionv1.AdmissionRequest{UID: "uid-123"},
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	out := decodeReview(t, rr)
	if out.Response == nil || !out.Response.Allowed {
		t.Fatalf("response = %+v, want allowed", out.Response)
	}
	if out.Response.UID != "uid-123" {
		t.Errorf("response UID = %q, want the request UID uid-123", out.Response.UID)
	}
}

func TestHandlerRelaysDenial(t *testing.T) {
	h := webhook.Handler(func(context.Context, *admissionv1.AdmissionRequest) *admissionv1.AdmissionResponse {
		return webhook.Deny(http.StatusForbidden, "tenant_label_immutable")
	})
	rr := postReview(t, h, admissionv1.AdmissionReview{
		Request: &admissionv1.AdmissionRequest{UID: "uid-deny"},
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (denial is carried in the body)", rr.Code)
	}
	out := decodeReview(t, rr)
	if out.Response == nil || out.Response.Allowed {
		t.Fatalf("response = %+v, want a denial", out.Response)
	}
	if out.Response.Result == nil || out.Response.Result.Code != http.StatusForbidden {
		t.Errorf("denial result = %+v, want code 403", out.Response.Result)
	}
	if out.Response.Result.Message != "tenant_label_immutable" {
		t.Errorf("denial message = %q, want tenant_label_immutable", out.Response.Result.Message)
	}
	if out.Response.UID != "uid-deny" {
		t.Errorf("response UID = %q, want uid-deny", out.Response.UID)
	}
}

func TestHandlerRejectsNonPost(t *testing.T) {
	h := webhook.Handler(func(context.Context, *admissionv1.AdmissionRequest) *admissionv1.AdmissionResponse {
		return webhook.Allow()
	})
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/", nil))
	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405 for a GET", rr.Code)
	}
}

func TestHandlerRejectsMalformedBody(t *testing.T) {
	h := webhook.Handler(func(context.Context, *admissionv1.AdmissionRequest) *admissionv1.AdmissionResponse {
		return webhook.Allow()
	})
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/", bytes.NewReader([]byte("{not json"))))
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 for malformed JSON", rr.Code)
	}
}

func TestHandlerRejectsReviewWithoutRequest(t *testing.T) {
	called := false
	h := webhook.Handler(func(context.Context, *admissionv1.AdmissionRequest) *admissionv1.AdmissionResponse {
		called = true
		return webhook.Allow()
	})
	rr := postReview(t, h, admissionv1.AdmissionReview{})
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 when the review carries no request", rr.Code)
	}
	if called {
		t.Error("the decider ran despite the AdmissionReview having no request")
	}
}
