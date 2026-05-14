// SPDX-License-Identifier: MIT

//go:build component

// Tier-2 component scaffolds for §12.2.5 Translators. The LLM Proxy
// native translator is exercised against canonical request/response
// pairs from the OpenAI and Anthropic SDKs. The mock LLM provider
// records every translated request and the suite asserts the
// round-trip is byte-equivalent for the canonical inputs.

package translators_test

import "testing"

// TestLLMProxyTranslatorAnthropic — round-trips canonical Anthropic
// SDK requests and responses through the native translator. Byte
// equivalence is the contract.
func TestLLMProxyTranslatorAnthropic(t *testing.T) {
	t.Skip("not implemented: §12.2.5 anthropic_direct translator — requires pkg/llmproxy + the canonical request/response corpus under tests/testdata/anthropic/ + the mock provider recorder")
}

// TestLLMProxyTranslatorOpenAIChatCompletions — round-trips canonical
// OpenAI Chat Completions requests and responses.
func TestLLMProxyTranslatorOpenAIChatCompletions(t *testing.T) {
	t.Skip("not implemented: §12.2.5 openai_chat_completions translator — requires pkg/llmproxy + the canonical corpus under tests/testdata/openai_chat/")
}

// TestLLMProxyTranslatorOpenAIResponses — round-trips canonical
// OpenAI Responses API requests and responses, including the id
// field's extended behavior.
func TestLLMProxyTranslatorOpenAIResponses(t *testing.T) {
	t.Skip("not implemented: §12.2.5 openai_responses translator — requires pkg/llmproxy + the canonical corpus under tests/testdata/openai_responses/")
}
