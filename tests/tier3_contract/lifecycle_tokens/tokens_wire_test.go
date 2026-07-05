//go:build contract

// SPDX-License-Identifier: MIT

// Package lifecycle_tokens_test is the Tier 3 contract suite for the
// §4.7 llm_request_completed direct-mode token fields (proposal 0024,
// F-15.3.7 / F-11.2.20). Direct-mode pods egress to the provider
// directly and the runtime is the sole in-pod observer of provider
// token counts (§11.2); it carries those counts to the adapter on the
// existing llm_request_completed lifecycle frame as the optional
// inputTokens/outputTokens pair. These tests pin the wire contract of
// that enriched frame against schemas/lifecycle-events.schema.json: the
// token pair is accepted at its boundary, and the {"type":"integer",
// "minimum":0} constraint rejects a negative or non-integer count.
package lifecycle_tokens_test

import (
	"testing"

	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// validateFrame compiles the lifecycle-events schema and validates a
// decoded frame against it, returning the validation error (nil when
// the frame conforms).
func validateFrame(t *testing.T, frame any) error {
	t.Helper()
	validator := schematest.Compile(t, "schemas/lifecycle-events.schema.json")
	return validator.Validate(frame)
}

// spec: 4.7 (llm_request_completed inputTokens/outputTokens), 11.2
//
//	(direct-mode usage source)
//
// diagnosis: the §4.7 llm_request_completed frame no longer accepts the
//
//	optional inputTokens/outputTokens direct-mode token pair.
//	Without it the adapter has no spec-grounded direct-mode token
//	source, so ReportUsage stays Unimplemented and the §11.2
//	anomaly metric is dead config. Check that
//	schemas/lifecycle-events.schema.json declares inputTokens and
//	outputTokens as {"type":"integer","minimum":0} properties.
func TestLLMRequestCompletedAcceptsTokenCounts(t *testing.T) {
	t.Parallel()

	frame := map[string]any{
		"type":         "llm_request_completed",
		"requestId":    "req_01HX9F0YWXKK0V7QZ7G6P3R5JN",
		"provider":     "anthropic",
		"status":       "ok",
		"inputTokens":  1200,
		"outputTokens": 340,
	}
	if err := validateFrame(t, frame); err != nil {
		t.Fatalf("direct-mode llm_request_completed with token counts must validate, got: %v", err)
	}
}

// spec: 4.7 (llm_request_completed optional token fields)
// diagnosis: the token fields must be optional so a runtime that cannot
//
//	extract provider counts still emits a valid frame (the §4.7
//	Notes clause: it omits both fields and the session has no
//	direct-mode token source). If this fails, the fields were
//	wrongly added to `required`.
func TestLLMRequestCompletedTokenCountsAreOptional(t *testing.T) {
	t.Parallel()

	frame := map[string]any{
		"type":      "llm_request_completed",
		"requestId": "req_01HX9F0YWXKK0V7QZ7G6P3R5JM",
		"provider":  "anthropic",
		"status":    "ok",
	}
	if err := validateFrame(t, frame); err != nil {
		t.Fatalf("llm_request_completed without token counts must validate, got: %v", err)
	}
}

// spec: 4.7 (inputTokens/outputTokens minimum:0), 11.2 (usage integrity)
// diagnosis: a negative token count is not a well-formed usage delta.
//
//	The {"type":"integer","minimum":0} constraint must reject it
//	so a malformed frame cannot poison the adapter's per-session
//	cumulative accumulator. This case would pass against the
//	pre-0024 schema (which had no such properties, so
//	additionalProperties:false would reject the frame outright for
//	a different reason) and against a schema that omits the
//	minimum bound; it pins the corrected boundary.
func TestLLMRequestCompletedRejectsNegativeTokenCounts(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name  string
		frame map[string]any
	}{
		{
			name: "negative inputTokens",
			frame: map[string]any{
				"type": "llm_request_completed", "requestId": "req_neg_in",
				"provider": "anthropic", "status": "ok",
				"inputTokens": -1, "outputTokens": 10,
			},
		},
		{
			name: "negative outputTokens",
			frame: map[string]any{
				"type": "llm_request_completed", "requestId": "req_neg_out",
				"provider": "anthropic", "status": "ok",
				"inputTokens": 10, "outputTokens": -5,
			},
		},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if err := validateFrame(t, tc.frame); err == nil {
				t.Fatalf("%s: schema must reject a negative token count (minimum:0)", tc.name)
			}
		})
	}
}

// spec: 4.7 (inputTokens/outputTokens type:integer)
// diagnosis: a fractional token count is not a valid integer delta. The
//
//	{"type":"integer"} constraint must reject it. A schema that
//	declared the fields as `number` would wrongly accept 1.5.
func TestLLMRequestCompletedRejectsFractionalTokenCounts(t *testing.T) {
	t.Parallel()

	frame := map[string]any{
		"type": "llm_request_completed", "requestId": "req_frac",
		"provider": "anthropic", "status": "ok",
		"inputTokens": 1.5, "outputTokens": 10,
	}
	if err := validateFrame(t, frame); err == nil {
		t.Fatalf("schema must reject a fractional token count (type:integer)")
	}
}
