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
		// spec: §4.9 line 1218, §15.1 line 990 — the §4.9 pre-claim
		// exhaustion / assignment race is POLICY and retryable (503).
		{"CREDENTIAL_POOL_EXHAUSTED", CategoryPolicy, true},
		// spec: §4.9 line 1364, §15.1 line 993 — a user-only policy with
		// no registered credential is PERMANENT and not retryable (404).
		{"USER_CREDENTIAL_NOT_FOUND", CategoryPermanent, false},
		// spec: §11.5 line 277 — INVALID_IDEMPOTENCY_KEY is PERMANENT
		// (key is malformed; the same key will be rejected identically).
		// F-15.1.30.
		{"INVALID_IDEMPOTENCY_KEY", CategoryPermanent, false},
		// spec: §15.1 line 1052 — DERIVE_ON_LIVE_SESSION is PERMANENT
		// (state controls the failure). F-15.1.29.
		{"DERIVE_ON_LIVE_SESSION", CategoryPermanent, false},
		// spec: §15.1 line 1054 — DERIVE_SNAPSHOT_UNAVAILABLE is TRANSIENT
		// because the missing object can be remediated out of band.
		{"DERIVE_SNAPSHOT_UNAVAILABLE", CategoryTransient, true},
		// spec: §15.1 line 1055 — ISOLATION_MONOTONICITY_VIOLATED is POLICY
		// (the SEC-001 lattice forbids the move); 422, not retryable.
		{"ISOLATION_MONOTONICITY_VIOLATED", CategoryPolicy, false},
		// spec: §15.1 line 1053 — DERIVE_LOCK_CONTENTION is POLICY,
		// retryable=true (back off and try again).
		{"DERIVE_LOCK_CONTENTION", CategoryPolicy, true},
		// spec: §15.1 line 1084 — ELICITATION_NOT_FOUND is PERMANENT
		// and not retryable; the triple mismatch will fail identically
		// until the caller supplies a matching elicitation_id. F-9.2.17.
		{"ELICITATION_NOT_FOUND", CategoryPermanent, false},
		// spec: §15.1 line 1085 — ELICITATION_CONTENT_TAMPERED is
		// PERMANENT and not retryable; the tampering pod's forward fails
		// identically. F-9.2.18.
		{"ELICITATION_CONTENT_TAMPERED", CategoryPermanent, false},
		// spec: §9.2 line 103 — ELICITATION_TIMEOUT is TRANSIENT but
		// not retryable as-is (the original elicitation_id is dismissed);
		// a fresh elicitation may succeed. F-9.2.18.
		{"ELICITATION_TIMEOUT", CategoryTransient, false},
		// spec: §9.2 line 87 — DOMAIN_NOT_ALLOWLISTED is POLICY and not
		// retryable; the operator must add the host to the pool
		// urlModeElicitation.domainAllowlist.
		{"DOMAIN_NOT_ALLOWLISTED", CategoryPolicy, false},
		// spec: §15.1 — INTERACTION_ALREADY_RESOLVED is POLICY (a race
		// the second caller lost) and not retryable; the resolved phase
		// is the authoritative outcome. F-9.2.17.
		{"INTERACTION_ALREADY_RESOLVED", CategoryPolicy, false},
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

// TestClassifyCatalogMatrixCodes asserts that every error class the
// §15.2.1 rule 5(b)/(c) contract test matrix (spec 15:1408) and the
// §15.4 session-creation rejection family name now resolves to its
// authoritative §15 catalog (category, retryable) pair instead of the
// (TRANSIENT, true) unknown-code fallback. Because REST writeError and
// MCP handleToolCall both classify through this one table, a correct
// entry is what keeps rule 5(d) parity. F-15.2.6.
func TestClassifyCatalogMatrixCodes(t *testing.T) {
	cases := []struct {
		code     string
		wantCat  Category
		wantRetr bool
	}{
		// §15.2.1 matrix (spec 15:1408).
		{"INVALID_STATE_TRANSITION", CategoryPermanent, false}, // spec: 15:980
		{"PERMISSION_DENIED", CategoryPolicy, false},           // spec: 15:1028
		{"CREDENTIAL_REVOKED", CategoryPolicy, false},          // spec: 15:1030
		{"CIRCUIT_BREAKER_OPEN", CategoryPolicy, false},        // spec: 15:1032
		// §15.4 session-creation rejection family (spec 15:1408).
		{"VARIANT_ISOLATION_UNAVAILABLE", CategoryPolicy, false},     // spec: 15:1056
		{"REGION_CONSTRAINT_UNRESOLVABLE", CategoryPermanent, false}, // spec: 15:1058
		{"GIT_CLONE_AUTH_UNSUPPORTED_HOST", CategoryPolicy, false},   // spec: 15:1063
		{"GIT_CLONE_AUTH_HOST_AMBIGUOUS", CategoryPolicy, false},     // spec: 15:1064
		{"GIT_CLONE_REF_UNRESOLVABLE", CategoryPermanent, false},     // spec: 15:1065
		{"GIT_CLONE_REF_RESOLVE_TRANSIENT", CategoryTransient, true}, // spec: 15:1066
		{"ENV_VAR_BLOCKLISTED", CategoryPermanent, false},            // spec: 15:1062
		{"SDK_DEMOTION_NOT_SUPPORTED", CategoryPermanent, false},     // spec: 15:1083
		{"POOL_DRAINING", CategoryTransient, true},                   // spec: 15:1034
		{"ERASURE_IN_PROGRESS", CategoryPolicy, false},               // spec: 15:1040
		{"TENANT_SUSPENDED", CategoryPolicy, false},                  // spec: 15:1096
		// A representative spread of other newly-added catalog codes.
		{"TARGET_NOT_READY", CategoryTransient, true},      // spec: 15:1094
		{"TARGET_TERMINAL", CategoryPermanent, false},      // spec: 15:998
		{"STORAGE_QUOTA_EXCEEDED", CategoryPolicy, false},  // spec: 15:1024
		{"BUDGET_EXHAUSTED", CategoryPolicy, false},        // spec: 15:1079
		{"POD_CRASH", CategoryTransient, true},             // spec: 15:995
		{"OUTPUTPART_TOO_LARGE", CategoryPermanent, false}, // spec: 15:1038
		{"ETAG_MISMATCH", CategoryPermanent, false},        // spec: 15:984
		// spec: §8.8 line 869 — a one_shot second input round is 400.
		{"ONE_SHOT_INPUT_EXHAUSTED", CategoryPermanent, false},
		// Code-internal delegate_task / request_input codes classified
		// by sibling analogy (no §15.4 catalog row).
		{"INVALID_LEASE_FIELD", CategoryPermanent, false},
		{"DELEGATION_DENIED", CategoryPolicy, false},
		{"REQUEST_INPUT_CANCELLED", CategoryTransient, false},
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
			// A matrix code must never resolve to the unknown fallback.
			if cat == CategoryTransient && retr && c.wantCat != CategoryTransient {
				t.Errorf("%s fell through to the unknown-code fallback", c.code)
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

// spec: §15.1 lines 974-1099 — the error catalog has no INVALID_REQUEST
// code; the canonical 400 validation code is VALIDATION_ERROR. The
// non-spec INVALID_REQUEST entry was removed from the classifier so it
// no longer registers as a known PERMANENT code (F-15.1.4).
func TestClassifyDropsNonSpecInvalidRequest_spec_15_1(t *testing.T) {
	if _, ok := table["INVALID_REQUEST"]; ok {
		t.Fatal("INVALID_REQUEST must not be a registered error code (non-spec per §15.1)")
	}
	cat, retr := Classify("INVALID_REQUEST")
	if cat != CategoryTransient || !retr {
		t.Errorf("INVALID_REQUEST classify = (%q, %v), want transient fallback (%q, true)", cat, retr, CategoryTransient)
	}
}

// spec: §11.7 item 3 line 368 — an audit advisory-lock acquisition
// timeout is TRANSIENT and retryable; the gateway retries on the same
// replica before surfacing 503 audit_unavailable.
func TestClassifyAuditConcurrencyTimeout_spec_11_7(t *testing.T) {
	cat, retr := Classify("AUDIT_CONCURRENCY_TIMEOUT")
	if cat != CategoryTransient || !retr {
		t.Errorf("AUDIT_CONCURRENCY_TIMEOUT classify = (%q, %v), want (%q, true)", cat, retr, CategoryTransient)
	}
}
