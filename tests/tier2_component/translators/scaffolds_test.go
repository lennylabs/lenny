// SPDX-License-Identifier: MIT

//go:build component

// Tier-2 component scaffolds for §12.2.5 Translators. The LLM Proxy
// native translator is exercised against canonical request/response
// pairs from the OpenAI and Anthropic SDKs.
//
// The Anthropic corpus entries are exercised in
// anthropic_translator_test.go against pkg/gateway/llmproxy's
// AnthropicDirectTranslator. The OpenAI corpora remain scaffolds
// because the matching native-translator surfaces are not built.

package translators_test

import "testing"

// TestLLMProxyTranslatorOpenAIChatCompletions — round-trips canonical
// OpenAI Chat Completions requests and responses through a native
// translator targeting an OpenAI-compatible upstream.
//
// spec: 4.9
// diagnosis: the openai_direct translator and a mock OpenAI provider
// recorder are not present in pkg/gateway/llmproxy. Only the Azure
// OpenAI translator serves the openai dialect today, and it rewrites
// the upstream URL with the Azure deployment path rather than passing
// through to api.openai.com.
func TestLLMProxyTranslatorOpenAIChatCompletions(t *testing.T) {
	t.Skip("blocked: §4.9 openai_direct translator — pkg/gateway/llmproxy has AzureOpenAITranslator, AWSBedrockTranslator, VertexAITranslator, and AnthropicDirectTranslator, but no openai_direct translator that round-trips api.openai.com requests; the tests/testdata/openai_chat corpus has no producer until that translator lands")
}

// TestLLMProxyTranslatorOpenAIResponses — round-trips canonical
// OpenAI Responses API requests and responses, including the id
// field's extended behavior.
//
// spec: 4.9
// diagnosis: the openai_responses translator is not present in
// pkg/gateway/llmproxy. No translator implementation today handles the
// /v1/responses dialect with its extended id-field semantics; the
// pkg/gateway/translator/open_responses.go adapter is the gateway-side
// REST handler, not the LLM Proxy translator the scaffold names.
func TestLLMProxyTranslatorOpenAIResponses(t *testing.T) {
	t.Skip("blocked: §4.9 openai_responses translator — pkg/gateway/llmproxy has no translator for the OpenAI Responses API dialect, and no Dialect value or Translator implementation covers /v1/responses; the tests/testdata/openai_responses corpus has no consumer until that translator lands")
}
