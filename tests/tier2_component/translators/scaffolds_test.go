// SPDX-License-Identifier: MIT

//go:build component

// Tier-2 component scaffolds for §12.2.5 Translators. The LLM Proxy
// native translator is exercised against canonical request/response
// pairs from the OpenAI and Anthropic SDKs.
//
// The Anthropic corpus entries are exercised in
// anthropic_translator_test.go against pkg/gateway/llmproxy's
// AnthropicDirectTranslator. The OpenAI Chat Completions corpus is
// exercised in openai_chat_translator_test.go and the OpenAI Responses
// corpus in openai_responses_translator_test.go.

package translators_test

// TestLLMProxyTranslatorOpenAIChatCompletions is implemented in
// openai_chat_translator_test.go, which round-trips the canonical
// openai_chat corpus through pkg/gateway/llmproxy.OpenAIDirectTranslator.

// TestLLMProxyTranslatorOpenAIResponses is implemented in
// openai_responses_translator_test.go, which round-trips the canonical
// openai_responses corpus through pkg/gateway/llmproxy.OpenAIResponsesTranslator.
