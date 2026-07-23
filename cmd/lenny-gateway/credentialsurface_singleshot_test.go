// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/environment/translator"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
)

// TestSingleShotErrorFrom verifies the sessionserver.ServiceError → adapter
// SingleShotError mapping copies status, code, message, retryability, and the
// Retry-After seconds verbatim, so the OpenAI-dialect adapter re-emits the
// shared service layer's error envelope unchanged. The zero-Retry-After case
// (the §4.9 CREDENTIAL_POOL_EXHAUSTED pre-claim miss sets no header) is pinned
// alongside the pod-claim exhaustion case that carries a backoff hint.
// spec: §15.2.1 rule 1; §7.1 create-and-start atomicity; §4.9.
func TestSingleShotErrorFrom(t *testing.T) {
	exhausted := singleShotErrorFrom(&sessionserver.ServiceError{
		HTTPStatus:        http.StatusServiceUnavailable,
		Code:              "SESSION_CREATION_FAILED",
		Message:           "warm pool exhausted",
		Retryable:         true,
		RetryAfterSeconds: 7,
	})
	if exhausted.HTTPStatus != http.StatusServiceUnavailable || exhausted.Code != "SESSION_CREATION_FAILED" ||
		exhausted.Message != "warm pool exhausted" || !exhausted.Retryable || exhausted.RetryAfterSeconds != 7 {
		t.Fatalf("exhaustion mapping = %+v, want status/code/message/retryable/retry-after preserved", exhausted)
	}

	credMiss := singleShotErrorFrom(&sessionserver.ServiceError{
		HTTPStatus: http.StatusServiceUnavailable,
		Code:       "CREDENTIAL_POOL_EXHAUSTED",
		Message:    "no credential available",
		Retryable:  true,
	})
	if credMiss.RetryAfterSeconds != 0 {
		t.Errorf("CREDENTIAL_POOL_EXHAUSTED RetryAfterSeconds = %d, want 0 (no header)", credMiss.RetryAfterSeconds)
	}
}

// TestSessionSingleShotBinderMapsServiceError verifies BindSingleShot projects
// a shared-service rejection into a typed *translator.SingleShotError rather
// than an opaque error, so the adapter fails closed with the service layer's
// code. An empty RuntimeRef trips create-and-start validation before any pod
// claim, exercising the error-mapping branch (the happy path needs a live pod
// and is covered at tier 5). spec: §15.2.1 rule 1; §15 single-shot compute model.
func TestSessionSingleShotBinderMapsServiceError(t *testing.T) {
	srv := sessionserver.New(memstore.New(), sessionserver.Options{})
	binder := sessionSingleShotBinder{srv: srv}

	id, err := binder.BindSingleShot(context.Background(), translator.SingleShotSpec{TenantID: "acme"})
	if err == nil {
		t.Fatal("BindSingleShot with an empty RuntimeRef returned no error; want a validation rejection")
	}
	if id != "" {
		t.Errorf("session id = %q, want empty on rejection", id)
	}
	var ssErr *translator.SingleShotError
	if !errors.As(err, &ssErr) {
		t.Fatalf("error type = %T, want *translator.SingleShotError", err)
	}
	if ssErr.Code == "" || ssErr.HTTPStatus < 400 {
		t.Errorf("mapped error = %+v, want a client error code and status", ssErr)
	}
}
