// SPDX-License-Identifier: MIT

package main

import (
	"testing"

	"github.com/lennylabs/lenny/pkg/credential"
)

// TestBuildLLMTranslatorRegistryRegistersCursorProvider pins §26.6's
// commitment that Lenny's LLM proxy gains a translator for the
// cursor_direct upstream provider against buildLLMTranslatorRegistry,
// the production §4.9 wiring point that assembles every built-in
// translator (newLLMProxyServer in llmproxy.go dispatches solely
// through the registry this function returns).
//
// spec: §26.6 "Note on LLM-proxy dialect" (spec/26_reference-runtime-catalog.md:303):
// "Cursor's API surface is proprietary; the `cursor` dialect in Lenny's
// LLM proxy (§4.9) implements the public subset documented by Cursor
// and passes proxying requests through."
func TestBuildLLMTranslatorRegistryRegistersCursorProvider(t *testing.T) {
	t.Skip("spec gap: §26.6 commits the LLM proxy to a cursor dialect \"implementing the public subset documented by Cursor\" but neither §4.9 nor §26.6 specifies that subset (base URL, endpoint path, request/response JSON shape, credential-injection header, streaming envelope, or error-status mapping) needed to build and test a translator faithfully; §4.9's own proxy-dialect enumeration lists only openai and anthropic at launch, so it is unclear whether cursor is a third pod-facing dialect or a passthrough keyed on the cursor_direct provider alone. Needs a spec decision (tracked in TEST-GAPS.md) before a translator or a wire-format-pinning test can be written.")

	registry := buildLLMTranslatorRegistry(llmTranslatorConfig{anthropicVersion: "2023-06-01"})
	if _, ok := registry.For(string(credential.ProviderCursorDirect)); !ok {
		t.Errorf("registry has no translator for provider %q; §26.6 requires the LLM proxy to gain a cursor dialect covering Cursor's inference surface", credential.ProviderCursorDirect)
	}
}
