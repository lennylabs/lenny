// SPDX-License-Identifier: MIT

package lenny

import (
	"errors"
	"fmt"
	"net/http"
	"testing"
)

// TestDecodeAPIErrorParsesEnvelope confirms a §15.1 error envelope is
// decoded into the typed APIError with every field populated.
func TestDecodeAPIErrorParsesEnvelope(t *testing.T) {
	body := []byte(`{"error":{"code":"VALIDATION_ERROR","category":"PERMANENT",` +
		`"message":"runtimeRef is required","retryable":false,` +
		`"details":{"field":"runtimeRef"}}}`)
	err := decodeAPIError(http.StatusBadRequest, body)
	if err.Code != "VALIDATION_ERROR" {
		t.Errorf("code: want VALIDATION_ERROR, got %q", err.Code)
	}
	if err.Category != CategoryPermanent {
		t.Errorf("category: want PERMANENT, got %q", err.Category)
	}
	if err.Retryable {
		t.Error("retryable: want false")
	}
	if err.HTTPStatus != http.StatusBadRequest {
		t.Errorf("HTTPStatus: want 400, got %d", err.HTTPStatus)
	}
	if err.Details["field"] != "runtimeRef" {
		t.Errorf("details.field: want runtimeRef, got %v", err.Details["field"])
	}
}

// TestDecodeAPIErrorWithoutEnvelope confirms a non-envelope body
// falls back to a status-derived retryable verdict.
func TestDecodeAPIErrorWithoutEnvelope(t *testing.T) {
	err := decodeAPIError(http.StatusServiceUnavailable, []byte("upstream down"))
	if err.Code != "" {
		t.Errorf("code: want empty for non-envelope body, got %q", err.Code)
	}
	if !err.Retryable {
		t.Error("retryable: want true for a 503 status")
	}
}

// TestIsRetryable confirms the predicate reads the APIError flag and
// returns false for a plain error.
func TestIsRetryable(t *testing.T) {
	retryable := &APIError{Code: "RATE_LIMITED", Retryable: true}
	if !IsRetryable(retryable) {
		t.Error("IsRetryable: want true for a retryable APIError")
	}
	permanent := &APIError{Code: "VALIDATION_ERROR", Retryable: false}
	if IsRetryable(permanent) {
		t.Error("IsRetryable: want false for a non-retryable APIError")
	}
	if IsRetryable(errors.New("transport failure")) {
		t.Error("IsRetryable: want false for a non-APIError")
	}
}

// TestAsAPIErrorUnwraps confirms AsAPIError recovers a wrapped
// APIError.
func TestAsAPIErrorUnwraps(t *testing.T) {
	base := &APIError{Code: "RESOURCE_NOT_FOUND", HTTPStatus: http.StatusNotFound}
	wrapped := fmt.Errorf("get session: %w", base)
	got, ok := AsAPIError(wrapped)
	if !ok {
		t.Fatal("AsAPIError: want ok for a wrapped APIError")
	}
	if got.Code != "RESOURCE_NOT_FOUND" {
		t.Errorf("AsAPIError code: want RESOURCE_NOT_FOUND, got %q", got.Code)
	}
}
