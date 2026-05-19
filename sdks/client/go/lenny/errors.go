// SPDX-License-Identifier: MIT

package lenny

import (
	"errors"
	"fmt"
	"net/http"
)

// ErrorCategory is the §15.1 error category. Every gateway error
// envelope carries one of these four values.
type ErrorCategory string

// The §15.1 error categories.
const (
	// CategoryTransient marks an error whose cause is expected to
	// clear without a request change (pod crash, timeout, dependency
	// degradation).
	CategoryTransient ErrorCategory = "TRANSIENT"
	// CategoryPermanent marks an error that will not clear on retry
	// without a request change (validation failure, bad state).
	CategoryPermanent ErrorCategory = "PERMANENT"
	// CategoryPolicy marks an error produced by a policy decision
	// (quota, rate limit, authorization).
	CategoryPolicy ErrorCategory = "POLICY"
	// CategoryUpstream marks an error returned by an external
	// dependency the gateway called.
	CategoryUpstream ErrorCategory = "UPSTREAM"
)

// APIError is the typed form of the §15.1 error envelope
// `{code, category, message, retryable, details}`. The gateway
// returns this body on every non-2xx REST response. APIError
// implements the error interface, so callers can use errors.As to
// recover the structured fields.
type APIError struct {
	// Code is the machine-readable §15.1 error code (for example
	// VALIDATION_ERROR, RESOURCE_NOT_FOUND, RATE_LIMITED).
	Code string `json:"code"`

	// Category is the §15.1 error category.
	Category ErrorCategory `json:"category"`

	// Message is the human-readable description.
	Message string `json:"message"`

	// Retryable reports whether the client should retry the request.
	// The Client retries an error only when this field is true.
	Retryable bool `json:"retryable"`

	// DocsURL points at the documentation for this error code when
	// the gateway supplies one. The §15.1 envelope spells the field
	// `docs_url`.
	DocsURL string `json:"docs_url,omitempty"`

	// Details carries error-specific context. The structure varies by
	// code; INVALID_STATE_TRANSITION, for example, carries
	// currentState and allowedStates.
	Details map[string]any `json:"details,omitempty"`

	// HTTPStatus is the HTTP status code the gateway returned
	// alongside the envelope. It is populated by the Client and is
	// not part of the wire envelope.
	HTTPStatus int `json:"-"`
}

// Error implements the error interface.
func (e *APIError) Error() string {
	if e.Code == "" {
		return fmt.Sprintf("lenny: gateway returned HTTP %d with no error envelope", e.HTTPStatus)
	}
	return fmt.Sprintf("lenny: %s (%s): %s", e.Code, e.Category, e.Message)
}

// IsRetryable reports whether the error is a retryable APIError. A
// non-APIError (a transport failure, a context cancellation) is not
// retryable through this predicate; the Client's retry loop treats
// transport failures separately.
func IsRetryable(err error) bool {
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		return apiErr.Retryable
	}
	return false
}

// AsAPIError narrows err to an *APIError. It returns the error and
// true when err is or wraps an *APIError, and (nil, false)
// otherwise.
func AsAPIError(err error) (*APIError, bool) {
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		return apiErr, true
	}
	return nil, false
}

// retryableStatus reports whether an HTTP status warrants a retry
// when the response carried no parseable error envelope. The §15.1
// retryable categories map onto 429, 500, 502, 503, and 504.
func retryableStatus(status int) bool {
	switch status {
	case http.StatusTooManyRequests,
		http.StatusInternalServerError,
		http.StatusBadGateway,
		http.StatusServiceUnavailable,
		http.StatusGatewayTimeout:
		return true
	default:
		return false
	}
}
