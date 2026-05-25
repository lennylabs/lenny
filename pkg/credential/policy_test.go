// SPDX-License-Identifier: MIT

package credential

import (
	"reflect"
	"testing"
)

// spec: §4.9 lines 1310, 1336 — preferredSource SourceOrder per mode.
func TestPreferredSourceOrder(t *testing.T) {
	cases := []struct {
		src  PreferredSource
		want []LeaseSource
	}{
		{"", []LeaseSource{SourcePool}}, // unset → pool default (spec line 1310 example)
		{PreferredSourcePool, []LeaseSource{SourcePool}},
		{PreferredSourceUser, []LeaseSource{SourceUser}},
		{PreferUserThenPool, []LeaseSource{SourceUser, SourcePool}},
		{PreferPoolThenUser, []LeaseSource{SourcePool, SourceUser}},
	}
	for _, c := range cases {
		if got := c.src.SourceOrder(); !reflect.DeepEqual(got, c.want) {
			t.Errorf("SourceOrder(%q) = %v, want %v", c.src, got, c.want)
		}
	}
}

// spec: §4.9 lines 1364, 1370 — only user-only mode is terminal on a
// user-credential miss; the prefer-* modes fall through to pool.
func TestPreferredSourceUserMissTerminal(t *testing.T) {
	terminal := map[PreferredSource]bool{
		"":                  false,
		PreferredSourcePool: false,
		PreferredSourceUser: true,
		PreferUserThenPool:  false,
		PreferPoolThenUser:  false,
	}
	for src, want := range terminal {
		if got := src.UserMissIsTerminal(); got != want {
			t.Errorf("UserMissIsTerminal(%q) = %v, want %v", src, got, want)
		}
	}
}

func TestPreferredSourceUsesUserCredentials(t *testing.T) {
	for _, src := range []PreferredSource{PreferredSourceUser, PreferUserThenPool, PreferPoolThenUser} {
		if !src.UsesUserCredentials() {
			t.Errorf("UsesUserCredentials(%q) = false, want true", src)
		}
	}
	for _, src := range []PreferredSource{"", PreferredSourcePool} {
		if src.UsesUserCredentials() {
			t.Errorf("UsesUserCredentials(%q) = true, want false", src)
		}
	}
}

func TestPreferredSourceIsValid(t *testing.T) {
	for _, src := range AllPreferredSources() {
		if !src.IsValid() {
			t.Errorf("IsValid(%q) = false, want true", src)
		}
	}
	if PreferredSource("").IsValid() {
		t.Error("empty preferredSource should be invalid for the strict IsValid check")
	}
	if PreferredSource("bogus").IsValid() {
		t.Error("unknown preferredSource should be invalid")
	}
}

// spec: §4.9 lines 1314-1319 — fallback.order wins; defaultPool is the
// single-pool chain when no order is set.
func TestProviderPoolOrder(t *testing.T) {
	if got := (ProviderPool{DefaultPool: "p1"}).PoolOrder(); !reflect.DeepEqual(got, []string{"p1"}) {
		t.Errorf("defaultPool chain = %v, want [p1]", got)
	}
	pp := ProviderPool{DefaultPool: "p1", Fallback: ProviderFallback{Order: []string{"a", "b"}}}
	if got := pp.PoolOrder(); !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Errorf("fallback.order chain = %v, want [a b]", got)
	}
	if got := (ProviderPool{}).PoolOrder(); len(got) != 0 {
		t.Errorf("empty providerPool chain = %v, want empty", got)
	}
}

// spec: §4.9 lines 1321-1322 — fallback defaults (cooldown 60s, max 3).
func TestPolicyFallbackEffectiveDefaults(t *testing.T) {
	var zero PolicyFallback
	if got := zero.EffectiveCooldownSeconds(); got != DefaultCooldownOnRateLimitSeconds {
		t.Errorf("EffectiveCooldownSeconds(zero) = %d, want %d", got, DefaultCooldownOnRateLimitSeconds)
	}
	if got := zero.EffectiveMaxRotations(); got != DefaultMaxRotationsPerSession {
		t.Errorf("EffectiveMaxRotations(zero) = %d, want %d", got, DefaultMaxRotationsPerSession)
	}
	set := PolicyFallback{CooldownOnRateLimitSeconds: 30, MaxRotationsPerSession: 5}
	if set.EffectiveCooldownSeconds() != 30 || set.EffectiveMaxRotations() != 5 {
		t.Errorf("explicit fallback values not preserved: %+v", set)
	}
}

func TestCredentialPolicyConfigured(t *testing.T) {
	if (CredentialPolicy{}).Configured() {
		t.Error("zero policy reports Configured")
	}
	cases := []CredentialPolicy{
		{PreferredSource: PreferredSourcePool},
		{ProviderPools: map[string]ProviderPool{"anthropic_direct": {DefaultPool: "p"}}},
		{Fallback: PolicyFallback{MaxRotationsPerSession: 2}},
		{UserCredentialsEnabled: true},
	}
	for i, c := range cases {
		if !c.Configured() {
			t.Errorf("case %d: Configured = false, want true", i)
		}
	}
}

// Clone must deep-copy the providerPools map and fallback-order slices
// so a mutation through the copy cannot reach the original.
func TestCredentialPolicyCloneDeepCopies(t *testing.T) {
	orig := CredentialPolicy{
		PreferredSource: PreferPoolThenUser,
		ProviderPools: map[string]ProviderPool{
			"anthropic_direct": {DefaultPool: "p1", Fallback: ProviderFallback{Order: []string{"p1", "p2"}}},
		},
	}
	cp := orig.Clone()
	cp.ProviderPools["anthropic_direct"] = ProviderPool{DefaultPool: "mutated"}
	cp.ProviderPools["aws_bedrock"] = ProviderPool{DefaultPool: "new"}
	if orig.ProviderPools["anthropic_direct"].DefaultPool != "p1" {
		t.Error("Clone shares the providerPools map with the original")
	}
	if _, ok := orig.ProviderPools["aws_bedrock"]; ok {
		t.Error("Clone shares the providerPools map keys with the original")
	}
	// Mutating the cloned fallback-order slice must not touch the original.
	cp2 := orig.Clone()
	cp2.ProviderPools["anthropic_direct"].Fallback.Order[0] = "x"
	if orig.ProviderPools["anthropic_direct"].Fallback.Order[0] != "p1" {
		t.Error("Clone shares the fallback.order backing array")
	}
}

func TestCredentialPolicyValidate(t *testing.T) {
	valid := []CredentialPolicy{
		{},
		{PreferredSource: PreferredSourcePool},
		{ProviderPools: map[string]ProviderPool{"anthropic_direct": {DefaultPool: "p"}}},
		{ProviderPools: map[string]ProviderPool{"aws_bedrock": {Fallback: ProviderFallback{Order: []string{"a"}}}}},
		{Fallback: PolicyFallback{CooldownOnRateLimitSeconds: 60, MaxRotationsPerSession: 3}},
	}
	for i, c := range valid {
		if err := c.Validate(); err != nil {
			t.Errorf("valid case %d: Validate = %v", i, err)
		}
	}
	invalid := []CredentialPolicy{
		{PreferredSource: "bogus"},
		{ProviderPools: map[string]ProviderPool{"anthropic_direct": {}}}, // no defaultPool, no order
		{Fallback: PolicyFallback{CooldownOnRateLimitSeconds: -1}},
		{Fallback: PolicyFallback{MaxRotationsPerSession: -1}},
	}
	for i, c := range invalid {
		if err := c.Validate(); err == nil {
			t.Errorf("invalid case %d: Validate = nil, want error", i)
		}
	}
}

func TestCredentialPolicyProvidersSorted(t *testing.T) {
	c := CredentialPolicy{ProviderPools: map[string]ProviderPool{
		"vertex_ai":        {DefaultPool: "v"},
		"anthropic_direct": {DefaultPool: "a"},
		"aws_bedrock":      {DefaultPool: "b"},
	}}
	want := []string{"anthropic_direct", "aws_bedrock", "vertex_ai"}
	if got := c.Providers(); !reflect.DeepEqual(got, want) {
		t.Errorf("Providers = %v, want %v", got, want)
	}
}
