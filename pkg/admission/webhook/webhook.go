// SPDX-License-Identifier: MIT

// Package webhook is the thin HTTP shim shared by every Lenny
// ValidatingAdmissionWebhook. It implements the Kubernetes
// AdmissionReview request/response contract once: decode the review,
// dispatch to a per-webhook Decider, echo the request UID onto the
// response, and write the AdmissionReview reply.
//
// The admission decision logic lives in the sibling pure-decision
// packages (label_immutability, sandboxclaim_guard, ownership). Each
// webhook supplies a Decider that adapts an AdmissionRequest into its
// decision package's input and maps the result back to an
// AdmissionResponse via Allow and Deny.
package webhook

import (
	"encoding/json"
	"io"
	"net/http"

	admissionv1 "k8s.io/api/admission/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// maxBodyBytes caps the AdmissionReview request body. An admission
// payload carries one object plus its prior version; 3 MiB is generous
// and bounds memory against a hostile or buggy caller.
const maxBodyBytes = 3 << 20

// Decider renders the admission decision for one webhook. It receives
// the decoded AdmissionRequest and returns the AdmissionResponse; the
// Handler fills in the response UID.
type Decider func(req *admissionv1.AdmissionRequest) *admissionv1.AdmissionResponse

// Handler returns an http.Handler implementing the AdmissionReview
// HTTP contract over decide. It decodes the review, dispatches to
// decide, echoes the request UID onto the response, and writes the
// AdmissionReview reply.
func Handler(decide Decider) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "admission webhook expects POST", http.StatusMethodNotAllowed)
			return
		}
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBodyBytes))
		if err != nil {
			http.Error(w, "read admission body: "+err.Error(), http.StatusBadRequest)
			return
		}
		var review admissionv1.AdmissionReview
		if err := json.Unmarshal(body, &review); err != nil {
			http.Error(w, "decode AdmissionReview: "+err.Error(), http.StatusBadRequest)
			return
		}
		if review.Request == nil {
			http.Error(w, "AdmissionReview carries no request", http.StatusBadRequest)
			return
		}

		resp := decide(review.Request)
		resp.UID = review.Request.UID

		out := admissionv1.AdmissionReview{
			TypeMeta: metav1.TypeMeta{
				APIVersion: "admission.k8s.io/v1",
				Kind:       "AdmissionReview",
			},
			Response: resp,
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(out)
	})
}

// Allow builds an admitting AdmissionResponse.
func Allow() *admissionv1.AdmissionResponse {
	return &admissionv1.AdmissionResponse{Allowed: true}
}

// Deny builds a rejecting AdmissionResponse carrying an HTTP status
// code and a message the API server relays to the offending client.
func Deny(code int32, reason string) *admissionv1.AdmissionResponse {
	return &admissionv1.AdmissionResponse{
		Allowed: false,
		Result: &metav1.Status{
			Status:  metav1.StatusFailure,
			Code:    code,
			Message: reason,
		},
	}
}
