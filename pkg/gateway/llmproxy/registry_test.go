// SPDX-License-Identifier: MIT

package llmproxy_test

import (
	"net/http"
	"testing"

	"github.com/lennylabs/lenny/pkg/credential"
	"github.com/lennylabs/lenny/pkg/gateway/llmproxy"
)

// spec: §4.9 lines 1525-1526 — the proxy dispatches each lease to the
// translator registered for its resolved provider, and rejects a lease
// whose provider has no registered translator.

func TestNewTranslatorRegistryKeysByProvider(t *testing.T) {
	reg := llmproxy.NewTranslatorRegistry(
		&llmproxy.AnthropicDirectTranslator{},
		&llmproxy.OpenAIDirectTranslator{},
		&llmproxy.AWSBedrockTranslator{Region: "us-east-1"},
		nil, // a nil translator is skipped, not registered under "".
	)
	for _, p := range []string{
		llmproxy.ProviderAnthropicDirect,
		llmproxy.ProviderOpenAIDirect,
		llmproxy.ProviderAWSBedrock,
	} {
		tr, ok := reg.For(p)
		if !ok {
			t.Errorf("registry has no translator for %q", p)
			continue
		}
		if tr.Provider() != p {
			t.Errorf("registry[%q].Provider() = %q, want %q", p, tr.Provider(), p)
		}
	}
	if _, ok := reg.For(""); ok {
		t.Error("a nil translator must not register under the empty provider")
	}
	if _, ok := reg.For(llmproxy.ProviderVertexAI); ok {
		t.Error("an unregistered provider must not resolve")
	}
}

// TestHandlerDispatchesOnLeaseProvider verifies the handler routes a
// lease to the registry entry for its provider rather than a single
// fixed translator.
func TestHandlerDispatchesOnLeaseProvider(t *testing.T) {
	h := newProxyHarness(t)
	// Replace the single Translator with a registry whose
	// anthropic_direct translator targets the fake upstream.
	h.handler.Translator = nil
	h.handler.Translators = llmproxy.NewTranslatorRegistry(
		&llmproxy.AnthropicDirectTranslator{BaseURL: h.upstream.URL, DefaultAnthropicVersion: "2023-06-01"},
	)
	if err := h.leases.Put(handlerLease("lt-disp")); err != nil {
		t.Fatalf("seed lease: %v", err)
	}
	rr := post(h.handler, "lt-disp", messagesBody)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
}

// TestHandlerRejectsUnsupportedProvider verifies a lease whose provider
// is absent from the registry is rejected with
// UPSTREAM_PROVIDER_UNSUPPORTED before any upstream call.
func TestHandlerRejectsUnsupportedProvider(t *testing.T) {
	h := newProxyHarness(t)
	h.handler.Translator = nil
	// Registry serves only openai_direct; the seeded lease is
	// anthropic_direct, so resolution fails.
	h.handler.Translators = llmproxy.NewTranslatorRegistry(
		&llmproxy.OpenAIDirectTranslator{},
	)
	lease := handlerLease("lt-unsup")
	lease.Provider = credential.ProviderAnthropicDirect
	if err := h.leases.Put(lease); err != nil {
		t.Fatalf("seed lease: %v", err)
	}
	rr := post(h.handler, "lt-unsup", messagesBody)
	if rr.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502; body=%s", rr.Code, rr.Body.String())
	}
	if code := errorCode(t, rr); code != "UPSTREAM_PROVIDER_UNSUPPORTED" {
		t.Errorf("error code = %q, want UPSTREAM_PROVIDER_UNSUPPORTED", code)
	}
}
