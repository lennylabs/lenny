// SPDX-License-Identifier: MIT

// Package codex_dialect_selection_test exercises the §26.5 Codex
// Responses-versus-Chat-Completions dialect selection at the LLM proxy
// boundary this repository owns: the gateway's §4.9 LLM reverse proxy,
// which must be able to serve both the OpenAI Chat Completions dialect
// and the OpenAI Responses dialect for the same openai_direct provider
// identity so that a Codex-style runtime adapter's internal selection
// between them is transparent to the client.
package codex_dialect_selection_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/credential"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credleasestore"
	"github.com/lennylabs/lenny/pkg/gateway/llmproxy/llmproxy"
	"github.com/lennylabs/lenny/tests/testinfra/stubs/llmprovider"
)

// fixedKeyResolver is a llmproxy.CredentialResolver returning a fixed
// upstream key for every lease.
type fixedKeyResolver struct{}

func (fixedKeyResolver) UpstreamCredential(credential.Lease) (string, bool) {
	return "sk-openai-real-upstream-key", true
}

// spec: §26.5 (reference-runtime-catalog.md) — "Codex supports the
// OpenAI Responses API and the Chat Completions API; the adapter
// selects the former when the pool's provider identity advertises
// Responses support, falling back to Chat Completions otherwise.
// Responses vs. Chat Completions selection is transparent to the
// client."
//
// The adapter itself lives in the separate github.com/lennylabs/runtime-codex
// repository, so this test exercises the piece of that behavior this
// repository owns: the LLM proxy must be able to serve both dialects
// for the same openai_direct provider identity, and the pod-visible
// output must be equivalent regardless of which upstream API answered
// the call.
//
// diagnosis: once unskipped, a failure means the LLM proxy still
// cannot serve both the OpenAI Chat Completions and Responses dialects
// transparently for the same openai_direct provider identity — check
// whether Handler still hardcodes DialectAnthropic on both translation
// legs (pkg/gateway/llmproxy/llmproxy/handler.go) and whether
// TranslatorRegistry still allows only one translator per provider.
func TestCodexDialectSelectionTransparentToClient(t *testing.T) {
	t.Skip("the LLM proxy Handler hardcodes the Anthropic dialect on both translation legs and " +
		"TranslatorRegistry keys a single translator per provider, so it cannot yet serve the " +
		"OpenAI Chat Completions and Responses dialects for the same openai_direct provider identity; " +
		"tracked as an open test-gaps finding pending a human decision on the wiring redesign")

	upstream := llmprovider.New(t)

	leases := credleasestore.New()
	now := time.Now()

	chatLease := credential.Lease{
		LeaseID:      "cl-chat",
		SessionID:    "s-chat",
		Provider:     credential.Provider(llmproxy.ProviderOpenAIDirect),
		Source:       credential.SourcePool,
		PoolID:       "codex-pool",
		CredentialID: "cred-1",
		DeliveryMode: credential.DeliveryProxy,
		IssuedAt:     now,
		ExpiresAt:    now.Add(time.Hour),
		Proxy: &credential.ProxyConfig{
			ProxyURL:     "https://gateway-internal:8443/llm-proxy",
			ProxyDialect: string(llmproxy.DialectOpenAI),
			LeaseToken:   "lt-codex-chat",
		},
	}
	responsesLease := chatLease
	responsesLease.LeaseID = "cl-responses"
	responsesLease.SessionID = "s-responses"
	responsesLease.Proxy = &credential.ProxyConfig{
		ProxyURL:     chatLease.Proxy.ProxyURL,
		ProxyDialect: string(llmproxy.DialectOpenAIResponses),
		LeaseToken:   "lt-codex-responses",
	}

	if err := leases.Put(chatLease); err != nil {
		t.Fatalf("seed chat-completions lease: %v", err)
	}
	if err := leases.Put(responsesLease); err != nil {
		t.Fatalf("seed responses lease: %v", err)
	}

	// The pool's provider identity is openai_direct in both cases; the
	// registry must be able to serve either dialect for it so that
	// selecting one over the other is an internal, client-transparent
	// decision rather than a pool-identity change.
	registry := llmproxy.NewTranslatorRegistry(
		&llmproxy.OpenAIDirectTranslator{BaseURL: upstream.URL()},
		&llmproxy.OpenAIResponsesTranslator{BaseURL: upstream.URL()},
	)

	h := &llmproxy.Handler{
		Leases:      leases,
		Translators: registry,
		Forwarder:   &llmproxy.Forwarder{Breaker: &llmproxy.CircuitBreaker{}},
		Credentials: fixedKeyResolver{},
	}

	const prompt = "what does the pool's provider identity advertise"

	chatRR := postChatCompletions(h, "lt-codex-chat", prompt)
	if chatRR.Code != http.StatusOK {
		t.Fatalf("Chat-Completions-fallback branch: status=%d body=%s", chatRR.Code, chatRR.Body.String())
	}

	respRR := postResponses(h, "lt-codex-responses", prompt)
	if respRR.Code != http.StatusOK {
		t.Fatalf("Responses-selected branch: status=%d body=%s", respRR.Code, respRR.Body.String())
	}

	var sawChatCompletions, sawResponses bool
	for _, req := range upstream.Requests() {
		switch {
		case strings.Contains(req.Path, "/v1/chat/completions"):
			sawChatCompletions = true
		case strings.Contains(req.Path, "/v1/responses"):
			sawResponses = true
		}
	}
	if !sawChatCompletions {
		t.Error("the Chat-Completions-fallback branch never reached the upstream /v1/chat/completions endpoint")
	}
	if !sawResponses {
		t.Error("the Responses-selected branch never reached the upstream /v1/responses endpoint")
	}

	chatText := extractChatCompletionsContent(t, chatRR.Body.Bytes())
	respText := extractResponsesContent(t, respRR.Body.Bytes())
	if chatText != respText {
		t.Errorf("client-visible output differs between the two dialects (chat=%q responses=%q); "+
			"§26.5 requires the Responses-vs-Chat-Completions selection to be transparent to the client",
			chatText, respText)
	}
}

func postChatCompletions(h http.Handler, token, prompt string) *httptest.ResponseRecorder {
	body := `{"model":"gpt-4o","messages":[{"role":"user","content":"` + prompt + `"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

func postResponses(h http.Handler, token, prompt string) *httptest.ResponseRecorder {
	body := `{"model":"gpt-4o","input":"` + prompt + `"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

func extractChatCompletionsContent(t *testing.T, body []byte) string {
	t.Helper()
	var env struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatalf("decode chat-completions response: %v; body=%s", err, body)
	}
	if len(env.Choices) == 0 {
		t.Fatalf("chat-completions response carries no choices: %s", body)
	}
	return env.Choices[0].Message.Content
}

func extractResponsesContent(t *testing.T, body []byte) string {
	t.Helper()
	var env struct {
		Output []struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"output"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatalf("decode responses response: %v; body=%s", err, body)
	}
	if len(env.Output) == 0 || len(env.Output[0].Content) == 0 {
		t.Fatalf("responses response carries no output content: %s", body)
	}
	return env.Output[0].Content[0].Text
}
