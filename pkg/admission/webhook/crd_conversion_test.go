// SPDX-License-Identifier: MIT

package webhook_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/lennylabs/lenny/pkg/admission/webhook"
)

func conversionReview(objects ...string) apiextensionsv1.ConversionReview {
	raw := make([]runtime.RawExtension, 0, len(objects))
	for _, o := range objects {
		raw = append(raw, runtime.RawExtension{Raw: []byte(o)})
	}
	return apiextensionsv1.ConversionReview{
		TypeMeta: metav1.TypeMeta{APIVersion: "apiextensions.k8s.io/v1", Kind: "ConversionReview"},
		Request: &apiextensionsv1.ConversionRequest{
			UID:               "conv-uid-1",
			DesiredAPIVersion: "lenny.dev/v1",
			Objects:           raw,
		},
	}
}

func postConversion(t *testing.T, body []byte, method string) *httptest.ResponseRecorder {
	t.Helper()
	rr := httptest.NewRecorder()
	webhook.CRDConversion().ServeHTTP(rr, httptest.NewRequest(method, "/crd-conversion", bytes.NewReader(body)))
	return rr
}

func TestCRDConversionReturnsObjectsUnchanged(t *testing.T) {
	obj := `{"apiVersion":"lenny.dev/v1","kind":"Sandbox","metadata":{"name":"sbx-1"}}`
	body, err := json.Marshal(conversionReview(obj))
	if err != nil {
		t.Fatalf("marshal review: %v", err)
	}
	rr := postConversion(t, body, http.MethodPost)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}

	var out apiextensionsv1.ConversionReview
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if out.Response == nil {
		t.Fatal("response carries no ConversionResponse")
	}
	if out.Response.UID != "conv-uid-1" {
		t.Errorf("response UID = %q, want conv-uid-1", out.Response.UID)
	}
	if out.Response.Result.Status != metav1.StatusSuccess {
		t.Errorf("result status = %q, want Success", out.Response.Result.Status)
	}
	if len(out.Response.ConvertedObjects) != 1 {
		t.Fatalf("converted objects = %d, want 1", len(out.Response.ConvertedObjects))
	}
	if string(out.Response.ConvertedObjects[0].Raw) != obj {
		t.Errorf("converted object = %s, want it unchanged", out.Response.ConvertedObjects[0].Raw)
	}
}

func TestCRDConversionHandlesMultipleObjects(t *testing.T) {
	body, err := json.Marshal(conversionReview(
		`{"apiVersion":"lenny.dev/v1","kind":"Sandbox","metadata":{"name":"a"}}`,
		`{"apiVersion":"lenny.dev/v1","kind":"Sandbox","metadata":{"name":"b"}}`,
	))
	if err != nil {
		t.Fatalf("marshal review: %v", err)
	}
	rr := postConversion(t, body, http.MethodPost)

	var out apiextensionsv1.ConversionReview
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if out.Response == nil || len(out.Response.ConvertedObjects) != 2 {
		t.Errorf("converted objects = %v, want 2", out.Response)
	}
}

func TestCRDConversionRejectsNonPost(t *testing.T) {
	rr := postConversion(t, nil, http.MethodGet)
	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405 for a GET", rr.Code)
	}
}

func TestCRDConversionRejectsMalformedBody(t *testing.T) {
	rr := postConversion(t, []byte("{not json"), http.MethodPost)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 for a malformed body", rr.Code)
	}
}

func TestCRDConversionRejectsReviewWithoutRequest(t *testing.T) {
	body, err := json.Marshal(apiextensionsv1.ConversionReview{
		TypeMeta: metav1.TypeMeta{APIVersion: "apiextensions.k8s.io/v1", Kind: "ConversionReview"},
	})
	if err != nil {
		t.Fatalf("marshal review: %v", err)
	}
	rr := postConversion(t, body, http.MethodPost)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 for a review with no request", rr.Code)
	}
}
