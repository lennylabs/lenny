// SPDX-License-Identifier: MIT

package errorclassify

import "testing"

func TestClassifyKnownCodes(t *testing.T) {
	cases := []struct {
		code     string
		wantCat  Category
		wantRetr bool
	}{
		{"INTERNAL_ERROR", CategoryTransient, true},
		{"VALIDATION_ERROR", CategoryPermanent, false},
		{"WORKSPACE_PLAN_INVALID", CategoryPermanent, false},
		{"RESOURCE_NOT_FOUND", CategoryPermanent, false},
		{"CIRCUIT_BREAKER_OPEN", CategoryPolicy, false},
		{"DELEGATION_CYCLE_DETECTED", CategoryPermanent, false},
		{"RATE_LIMITED", CategoryPolicy, true},
		{"UPSTREAM_ERROR", CategoryUpstream, true},
		// spec: §4.3 line 214 — TOKEN_SERVICE_UNAVAILABLE is the
		// session-start retryable failure when the Token Service
		// circuit breaker is open.
		{"TOKEN_SERVICE_UNAVAILABLE", CategoryUpstream, true},
		// spec: §15.1 line 1008 — INTERCEPTOR_TIMEOUT is TRANSIENT and
		// retryable, distinct from the POLICY (non-retryable) codes a
		// deliberate interceptor REJECT produces.
		{"INTERCEPTOR_TIMEOUT", CategoryTransient, true},
		{"INTERCEPTOR_REJECTED", CategoryPolicy, false},
		// spec: §15.1 lines 1012-1013 — a deliberate LLM proxy
		// interceptor REJECT is PERMANENT and non-retryable, distinct
		// from the TRANSIENT INTERCEPTOR_TIMEOUT.
		{"LLM_REQUEST_REJECTED", CategoryPermanent, false},
		{"LLM_RESPONSE_REJECTED", CategoryPermanent, false},
		// spec: §15.1 line 1067, §8.3 line 157 — INPUT_TOO_LARGE is
		// PERMANENT and not retryable; the caller must reduce the input.
		{"INPUT_TOO_LARGE", CategoryPermanent, false},
		// spec: §15.2.1 line 1017 — WARM_POOL_EXHAUSTED (no idle pods or
		// concurrent slots) is TRANSIENT and retryable with backoff.
		{"WARM_POOL_EXHAUSTED", CategoryTransient, true},
	}
	for _, c := range cases {
		t.Run(c.code, func(t *testing.T) {
			cat, retr := Classify(c.code)
			if cat != c.wantCat {
				t.Errorf("category = %q, want %q", cat, c.wantCat)
			}
			if retr != c.wantRetr {
				t.Errorf("retryable = %v, want %v", retr, c.wantRetr)
			}
		})
	}
}

func TestClassifyUnknownCodeIsTransientRetryable(t *testing.T) {
	cat, retr := Classify("UNDEFINED_FUTURE_CODE")
	if cat != CategoryTransient {
		t.Errorf("unknown-code category = %q, want %q", cat, CategoryTransient)
	}
	if !retr {
		t.Errorf("unknown-code retryable = false, want true (transient fallback)")
	}
}
