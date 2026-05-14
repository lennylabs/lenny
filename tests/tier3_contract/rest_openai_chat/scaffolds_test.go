// SPDX-License-Identifier: MIT

//go:build contract

// Tier-3 contract scaffolds for §12.3.2 REST ↔ OpenAI Chat
// Completions translation. The harness sends a Lenny session over
// REST and the equivalent request over /v1/chat/completions, then
// asserts the documented fidelity loss is exact.

package rest_openai_chat_test

import "testing"

// TestRESTOpenAIChatFidelityMatrix — the documented fidelity matrix
// holds for every supported pair: schemaVersion is dropped,
// reasoning_trace is lossy, ref is dropped, etc.
func TestRESTOpenAIChatFidelityMatrix(t *testing.T) {
	t.Skip("not implemented: §12.3.2 REST↔OpenAI Chat — requires the /v1/chat/completions translator and a canonical fidelity-matrix corpus under tests/testdata/openai_chat/")
}

// TestRESTOpenAIChatDroppedFieldsContract — any field documented as
// dropped is in fact dropped on the OpenAI side.
func TestRESTOpenAIChatDroppedFieldsContract(t *testing.T) {
	t.Skip("not implemented: §12.3.2 dropped-fields contract — requires the translator and the corpus that names every dropped field per the spec")
}

// TestRESTOpenAIChatPreservedFieldsContract — any field documented as
// preserved survives the round trip.
func TestRESTOpenAIChatPreservedFieldsContract(t *testing.T) {
	t.Skip("not implemented: §12.3.2 preserved-fields contract — requires the translator and a round-trip diff over the canonical corpus")
}
