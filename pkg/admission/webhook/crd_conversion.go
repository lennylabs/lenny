// SPDX-License-Identifier: MIT

package webhook

import (
	"encoding/json"
	"io"
	"net/http"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// CRDConversion returns the HTTP handler for the lenny-crd-conversion
// webhook (§17.2). Every lenny.dev CRD is single-version (v1), so the
// conversion is the identity: each object is already at the only
// served version and is returned unchanged. The webhook is part of the
// §17.2 Phase 3.5 admission-plane baseline and is the endpoint a future
// multi-version CRD would route conversion through.
//
// The conversion contract uses an apiextensions.k8s.io/v1
// ConversionReview rather than an admission.k8s.io/v1 AdmissionReview,
// so this handler does not use the AdmissionReview Decider shim.
func CRDConversion() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "conversion webhook expects POST", http.StatusMethodNotAllowed)
			return
		}
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBodyBytes))
		if err != nil {
			http.Error(w, "read conversion body: "+err.Error(), http.StatusBadRequest)
			return
		}
		var review apiextensionsv1.ConversionReview
		if err := json.Unmarshal(body, &review); err != nil {
			http.Error(w, "decode ConversionReview: "+err.Error(), http.StatusBadRequest)
			return
		}
		if review.Request == nil {
			http.Error(w, "ConversionReview carries no request", http.StatusBadRequest)
			return
		}

		out := apiextensionsv1.ConversionReview{
			TypeMeta: review.TypeMeta,
			Response: &apiextensionsv1.ConversionResponse{
				UID:              review.Request.UID,
				ConvertedObjects: review.Request.Objects,
				Result:           metav1.Status{Status: metav1.StatusSuccess},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(out)
	})
}
