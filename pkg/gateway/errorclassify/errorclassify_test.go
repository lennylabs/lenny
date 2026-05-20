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
