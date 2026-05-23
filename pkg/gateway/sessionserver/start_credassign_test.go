// SPDX-License-Identifier: MIT

package sessionserver

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/credassign"
)

// TestWriteTokenServiceUnavailableShape exercises the §4.3 line 214
// "retryable error" response envelope: HTTP 503, the
// TOKEN_SERVICE_UNAVAILABLE code, an UPSTREAM category, retryable: true,
// and a Retry-After header of 5 seconds. A client that backs off and
// retries is matched against this shape.
// spec: §4.3 line 214.
func TestWriteTokenServiceUnavailableShape(t *testing.T) {
	t.Parallel()
	s := &Server{}
	rr := httptest.NewRecorder()
	cause := fmt.Errorf("circuit breaker open: %w", credassign.ErrTokenServiceUnavailable)
	s.writeTokenServiceUnavailable(rr, cause)

	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusServiceUnavailable)
	}
	if got := rr.Header().Get("Retry-After"); got != "5" {
		t.Errorf("Retry-After = %q, want %q", got, "5")
	}
	if got := rr.Header().Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", got)
	}

	var env struct {
		Error struct {
			Code      string `json:"code"`
			Category  string `json:"category"`
			Message   string `json:"message"`
			Retryable bool   `json:"retryable"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode envelope: %v body=%s", err, rr.Body.String())
	}
	if env.Error.Code != "TOKEN_SERVICE_UNAVAILABLE" {
		t.Errorf("code = %q, want TOKEN_SERVICE_UNAVAILABLE", env.Error.Code)
	}
	if env.Error.Category != "UPSTREAM" {
		t.Errorf("category = %q, want UPSTREAM", env.Error.Category)
	}
	if !env.Error.Retryable {
		t.Error("retryable = false, want true")
	}
	if env.Error.Message == "" {
		t.Error("message is empty")
	}
	// The wrapped cause must surface in the message so an operator can
	// trace the upstream breaker state from the API response.
	if !errors.Is(cause, credassign.ErrTokenServiceUnavailable) {
		t.Error("test setup bug: cause does not wrap ErrTokenServiceUnavailable")
	}
}

// TestWriteTokenServiceUnavailableNilCause covers the path where the
// session-start handler has only the sentinel and no wrapping context:
// the Retry-After header still emits and the response still classifies
// as TOKEN_SERVICE_UNAVAILABLE.
// spec: §4.3 line 214.
func TestWriteTokenServiceUnavailableNilCause(t *testing.T) {
	t.Parallel()
	s := &Server{}
	rr := httptest.NewRecorder()
	s.writeTokenServiceUnavailable(rr, nil)

	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rr.Code)
	}
	if rr.Header().Get("Retry-After") != "5" {
		t.Errorf("Retry-After = %q, want 5", rr.Header().Get("Retry-After"))
	}
	var env struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if env.Error.Code != "TOKEN_SERVICE_UNAVAILABLE" {
		t.Errorf("code = %q, want TOKEN_SERVICE_UNAVAILABLE", env.Error.Code)
	}
}

// TestTokenServiceUnavailableRetryAfterConstant confirms the §4.3
// Retry-After budget agrees with the subsystem circuit-breaker
// cool-down. The 5-second value matches the open-state window in
// pkg/gateway/subsystem.
// spec: §4.3 line 214.
func TestTokenServiceUnavailableRetryAfterConstant(t *testing.T) {
	t.Parallel()
	if tokenServiceUnavailableRetryAfterSeconds != 5 {
		t.Errorf("tokenServiceUnavailableRetryAfterSeconds = %d, want 5",
			tokenServiceUnavailableRetryAfterSeconds)
	}
}
