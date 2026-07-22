// SPDX-License-Identifier: MIT

package main

import (
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
)

// spec: §15 built-in adapter single-shot compute model; §7.1 create-and-start
// atomicity; §4.9 CREDENTIAL_POOL_EXHAUSTED.
//
// singleShotErrorFrom copies the sessionserver.ServiceError classification the
// shared create-and-start service produces into the translator.SingleShotError
// the two OpenAI-dialect adapters re-emit in their native envelope. The
// Retry-After seconds must survive for the retryable warm-pod/slot claim
// exhaustion (503 SESSION_CREATION_FAILED) and stay zero for the §4.9
// credential pre-check miss (503 CREDENTIAL_POOL_EXHAUSTED), which sets no
// header, so the adapter emits Retry-After for the first case and omits it for
// the second.
func TestSingleShotErrorFromPreservesClassificationAndRetryAfter(t *testing.T) {
	cases := []struct {
		name string
		in   *sessionserver.ServiceError
	}{
		{
			name: "warm-pod claim exhaustion carries Retry-After",
			in: &sessionserver.ServiceError{
				HTTPStatus:        503,
				Code:              "SESSION_CREATION_FAILED",
				Message:           "no warm pod available",
				Retryable:         true,
				RetryAfterSeconds: 7,
			},
		},
		{
			name: "credential pool exhaustion carries no Retry-After",
			in: &sessionserver.ServiceError{
				HTTPStatus:        503,
				Code:              "CREDENTIAL_POOL_EXHAUSTED",
				Message:           "no provider has availability",
				Retryable:         false,
				RetryAfterSeconds: 0,
			},
		},
		{
			name: "admission-gate rejection preserves status and code",
			in: &sessionserver.ServiceError{
				HTTPStatus: 429,
				Code:       "CONCURRENCY_LIMIT_EXCEEDED",
				Message:    "over concurrency limit",
				Retryable:  true,
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := singleShotErrorFrom(tc.in)
			if got.HTTPStatus != tc.in.HTTPStatus {
				t.Errorf("HTTPStatus: got %d, want %d", got.HTTPStatus, tc.in.HTTPStatus)
			}
			if got.Code != tc.in.Code {
				t.Errorf("Code: got %q, want %q", got.Code, tc.in.Code)
			}
			if got.Message != tc.in.Message {
				t.Errorf("Message: got %q, want %q", got.Message, tc.in.Message)
			}
			if got.Retryable != tc.in.Retryable {
				t.Errorf("Retryable: got %v, want %v", got.Retryable, tc.in.Retryable)
			}
			if got.RetryAfterSeconds != tc.in.RetryAfterSeconds {
				t.Errorf("RetryAfterSeconds: got %d, want %d", got.RetryAfterSeconds, tc.in.RetryAfterSeconds)
			}
			// The typed error the adapter matches on must format Code and Message.
			if got.Error() != tc.in.Code+": "+tc.in.Message {
				t.Errorf("Error(): got %q, want %q", got.Error(), tc.in.Code+": "+tc.in.Message)
			}
		})
	}
}
