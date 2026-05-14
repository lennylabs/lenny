// SPDX-License-Identifier: MIT

//go:build contract

// Tier-3 contract scaffolds for §12.3.3 REST ↔ OpenAI Responses
// translation. Same shape as §12.3.2 against the Responses API,
// including the id field's extended behavior.

package rest_openai_responses_test

import "testing"

// TestRESTOpenAIResponsesFidelityMatrix — the documented fidelity
// matrix holds for every supported pair on the Responses API.
func TestRESTOpenAIResponsesFidelityMatrix(t *testing.T) {
	t.Skip("not implemented: §12.3.3 REST↔OpenAI Responses — requires the /v1/responses translator and a canonical fidelity-matrix corpus under tests/testdata/openai_responses/")
}

// TestRESTOpenAIResponsesIDFieldExtendedBehavior — the id field has
// extended behavior under the Responses API; the translator preserves
// it exactly.
func TestRESTOpenAIResponsesIDFieldExtendedBehavior(t *testing.T) {
	t.Skip("not implemented: §12.3.3 id-field extended behavior — requires the Responses translator and the id-field round-trip corpus")
}
