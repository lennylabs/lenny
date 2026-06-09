// SPDX-License-Identifier: MIT

package credential

import "testing"

// TestUserProxyDialect_spec_4_9_1473 covers the §4.9 user-source proxy
// dialect mapping: the vendor-direct LLM providers map to a single
// canonical dialect; multi-dialect and non-LLM providers do not and are
// not deliverable in proxy mode.
func TestUserProxyDialect_spec_4_9_1473(t *testing.T) {
	cases := []struct {
		provider Provider
		want     ProxyDialect
		ok       bool
	}{
		{ProviderAnthropicDirect, ProxyDialectAnthropic, true},
		{ProviderAzureOpenAI, ProxyDialectOpenAI, true},
		{ProviderVertexAI, ProxyDialectGoogle, true},
		{ProviderCursorDirect, ProxyDialectCursor, true},
		// aws_bedrock has no native dialect in the enum (reached only via
		// translation), so it is not a user-source proxy provider in v1.
		{ProviderAWSBedrock, "", false},
		// Non-LLM providers carry no LLM-proxy dialect.
		{ProviderGitHub, "", false},
		{ProviderVaultTransit, "", false},
		{Provider("custom_thing"), "", false},
	}
	for _, c := range cases {
		got, ok := UserProxyDialect(c.provider)
		if ok != c.ok || got != c.want {
			t.Errorf("UserProxyDialect(%q) = %q,%v; want %q,%v", c.provider, got, ok, c.want, c.ok)
		}
		if ok && !got.IsValid() {
			t.Errorf("UserProxyDialect(%q) returned non-canonical dialect %q", c.provider, got)
		}
	}
}
