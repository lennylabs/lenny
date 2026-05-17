// SPDX-License-Identifier: MIT

package experiment

import "testing"

func TestAllTargetingProvidersIsExhaustive(t *testing.T) {
	if got := len(AllTargetingProviders()); got != 4 {
		t.Errorf("AllTargetingProviders() returned %d, want 4 per §10.7", got)
	}
	for _, p := range AllTargetingProviders() {
		if !p.IsValid() {
			t.Errorf("AllTargetingProviders includes %q but IsValid rejects it", p)
		}
	}
	if TargetingProvider("vault").IsValid() {
		t.Error("an unknown provider must not be valid")
	}
}

func TestTargetingConfigZeroValueIsValid(t *testing.T) {
	var c TargetingConfig
	if err := c.Validate(); err != nil {
		t.Errorf("a zero TargetingConfig must be valid: %v", err)
	}
	if c.Configured() {
		t.Error("a zero TargetingConfig must report Configured() false")
	}
}

func TestTargetingConfigEffectiveTimeout(t *testing.T) {
	if got := (TargetingConfig{}).EffectiveTimeoutMs(); got != DefaultTargetingTimeoutMs {
		t.Errorf("EffectiveTimeoutMs default = %d, want %d", got, DefaultTargetingTimeoutMs)
	}
	if got := (TargetingConfig{TimeoutMs: 500}).EffectiveTimeoutMs(); got != 500 {
		t.Errorf("EffectiveTimeoutMs = %d, want 500", got)
	}
}

func TestTargetingConfigValidatesProviders(t *testing.T) {
	valid := []TargetingConfig{
		{Provider: TargetingProviderOFREP, OFREP: &OFREPConfig{Endpoint: "https://flags/ofrep"}},
		{Provider: TargetingProviderLaunchDarkly, LaunchDarkly: &LaunchDarklyConfig{SDKKey: "sdk-1"}},
		{Provider: TargetingProviderStatsig, Statsig: &StatsigConfig{ServerSecret: "secret-1"}},
		{Provider: TargetingProviderUnleash, Unleash: &UnleashConfig{APIURL: "https://u/api", APIToken: "tok"}},
	}
	for _, c := range valid {
		if err := c.Validate(); err != nil {
			t.Errorf("Validate(%s) = %v, want nil", c.Provider, err)
		}
		if !c.Configured() {
			t.Errorf("Configured() = false for a %s config", c.Provider)
		}
	}
}

func TestTargetingConfigRejectsBadConfigs(t *testing.T) {
	cases := []struct {
		name string
		cfg  TargetingConfig
	}{
		{"unknown provider", TargetingConfig{Provider: "vault"}},
		{"negative timeout", TargetingConfig{
			Provider: TargetingProviderOFREP, TimeoutMs: -1,
			OFREP: &OFREPConfig{Endpoint: "https://x/ofrep"},
		}},
		{"ofrep missing endpoint", TargetingConfig{
			Provider: TargetingProviderOFREP, OFREP: &OFREPConfig{},
		}},
		{"ofrep block absent", TargetingConfig{Provider: TargetingProviderOFREP}},
		{"launchdarkly missing sdkKey", TargetingConfig{
			Provider: TargetingProviderLaunchDarkly, LaunchDarkly: &LaunchDarklyConfig{},
		}},
		{"statsig missing secret", TargetingConfig{
			Provider: TargetingProviderStatsig, Statsig: &StatsigConfig{},
		}},
		{"unleash missing token", TargetingConfig{
			Provider: TargetingProviderUnleash, Unleash: &UnleashConfig{APIURL: "https://u/api"},
		}},
		{"mismatched provider block", TargetingConfig{
			Provider: TargetingProviderOFREP,
			OFREP:    &OFREPConfig{Endpoint: "https://x/ofrep"},
			Statsig:  &StatsigConfig{ServerSecret: "s"},
		}},
		{"block set without provider", TargetingConfig{
			OFREP: &OFREPConfig{Endpoint: "https://x/ofrep"},
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.cfg.Validate(); err == nil {
				t.Errorf("Validate(%s) = nil, want an error", tc.name)
			}
		})
	}
}

func TestTargetingConfigCloneIsolatesOFREPHeaders(t *testing.T) {
	orig := TargetingConfig{
		Provider:  TargetingProviderOFREP,
		TimeoutMs: 200,
		OFREP:     &OFREPConfig{Endpoint: "https://flags/ofrep", Headers: map[string]string{"Authorization": "Bearer x"}},
	}
	clone := orig.Clone()

	// Mutating the clone's nested block and header map must not reach
	// the original.
	clone.OFREP.Endpoint = "https://other/ofrep"
	clone.OFREP.Headers["Authorization"] = "Bearer mutated"
	clone.OFREP.Headers["X-Added"] = "1"

	if orig.OFREP.Endpoint != "https://flags/ofrep" {
		t.Errorf("clone mutated the original endpoint: %q", orig.OFREP.Endpoint)
	}
	if orig.OFREP.Headers["Authorization"] != "Bearer x" {
		t.Errorf("clone mutated an original header: %q", orig.OFREP.Headers["Authorization"])
	}
	if _, added := orig.OFREP.Headers["X-Added"]; added {
		t.Error("clone added a header to the original map")
	}
}

func TestTargetingConfigCloneCopiesProviderBlocks(t *testing.T) {
	orig := TargetingConfig{
		Provider:     TargetingProviderLaunchDarkly,
		LaunchDarkly: &LaunchDarklyConfig{SDKKey: "sdk-1"},
	}
	clone := orig.Clone()
	if clone.LaunchDarkly == orig.LaunchDarkly {
		t.Error("clone shares the LaunchDarkly pointer with the original")
	}
	clone.LaunchDarkly.SDKKey = "sdk-2"
	if orig.LaunchDarkly.SDKKey != "sdk-1" {
		t.Errorf("clone mutated the original SDKKey: %q", orig.LaunchDarkly.SDKKey)
	}
}

func TestTargetingConfigCloneZeroValue(t *testing.T) {
	clone := TargetingConfig{}.Clone()
	if clone.Configured() || clone.OFREP != nil || clone.LaunchDarkly != nil {
		t.Errorf("cloning a zero config produced %+v, want a zero config", clone)
	}
}

func TestBuildEvaluationContextWithUser(t *testing.T) {
	ctx := BuildEvaluationContext(EvaluationContextInput{
		UserID: "alice", TenantID: "acme", SessionID: "sess-1", Runtime: "claude-code",
	})
	if ctx["targetingKey"] != "alice" || ctx["user_id"] != "alice" {
		t.Errorf("targetingKey/user_id = %v/%v, want alice", ctx["targetingKey"], ctx["user_id"])
	}
	if ctx["tenant_id"] != "acme" || ctx["runtime"] != "claude-code" {
		t.Errorf("tenant_id/runtime = %v/%v", ctx["tenant_id"], ctx["runtime"])
	}
}

func TestBuildEvaluationContextAnonymousSession(t *testing.T) {
	// §10.7: a session with no user gets the deterministic anon pseudo-ID.
	ctx := BuildEvaluationContext(EvaluationContextInput{
		TenantID: "acme", SessionID: "sess-9", Runtime: "echo",
	})
	if ctx["targetingKey"] != "anon:sess-9" || ctx["user_id"] != "anon:sess-9" {
		t.Errorf("anonymous targetingKey/user_id = %v/%v, want anon:sess-9",
			ctx["targetingKey"], ctx["user_id"])
	}
}

func TestBuildEvaluationContextMergesLabels(t *testing.T) {
	ctx := BuildEvaluationContext(EvaluationContextInput{
		UserID: "bob", TenantID: "acme", SessionID: "s", Runtime: "echo",
		Labels: map[string]string{"team": "platform", "tier": "gold"},
	})
	if ctx["team"] != "platform" || ctx["tier"] != "gold" {
		t.Errorf("labels not merged: %v", ctx)
	}
}

func TestBuildEvaluationContextLabelCannotShadowReserved(t *testing.T) {
	// A label keyed like a reserved attribute must not override it.
	ctx := BuildEvaluationContext(EvaluationContextInput{
		UserID: "carol", TenantID: "acme", SessionID: "s", Runtime: "echo",
		Labels: map[string]string{"tenant_id": "spoofed", "user_id": "spoofed"},
	})
	if ctx["tenant_id"] != "acme" || ctx["user_id"] != "carol" {
		t.Errorf("a label shadowed a reserved attribute: tenant_id=%v user_id=%v",
			ctx["tenant_id"], ctx["user_id"])
	}
}

func TestResolveExternalVariantFromVariantField(t *testing.T) {
	got, known := ResolveExternalVariant("treatment", nil, []string{"treatment", "holdback"})
	if got != "treatment" || !known {
		t.Errorf("ResolveExternalVariant = (%q, %v), want (treatment, true)", got, known)
	}
}

func TestResolveExternalVariantVariantTakesPrecedenceOverValue(t *testing.T) {
	got, known := ResolveExternalVariant("treatment", "holdback", []string{"treatment", "holdback"})
	if got != "treatment" || !known {
		t.Errorf("ResolveExternalVariant = (%q, %v), want (treatment, true) — Variant wins over Value", got, known)
	}
}

func TestResolveExternalVariantFromStringValue(t *testing.T) {
	got, known := ResolveExternalVariant("", "holdback", []string{"treatment", "holdback"})
	if got != "holdback" || !known {
		t.Errorf("ResolveExternalVariant = (%q, %v), want (holdback, true)", got, known)
	}
}

func TestResolveExternalVariantFromObjectValue(t *testing.T) {
	value := map[string]any{"variant_id": "treatment", "other": 1}
	got, known := ResolveExternalVariant("", value, []string{"treatment"})
	if got != "treatment" || !known {
		t.Errorf("ResolveExternalVariant = (%q, %v), want (treatment, true)", got, known)
	}
}

func TestResolveExternalVariantControlIsAlwaysKnown(t *testing.T) {
	// The provider may legitimately assign the reserved control variant.
	got, known := ResolveExternalVariant(ControlVariantID, nil, []string{"treatment"})
	if got != ControlVariantID || !known {
		t.Errorf("ResolveExternalVariant(control) = (%q, %v), want (control, true)", got, known)
	}
}

func TestResolveExternalVariantUnregisteredCandidate(t *testing.T) {
	// A candidate the experiment does not register is unresolvable.
	got, known := ResolveExternalVariant("ghost", nil, []string{"treatment"})
	if got != ControlVariantID || known {
		t.Errorf("ResolveExternalVariant(unregistered) = (%q, %v), want (control, false)", got, known)
	}
}

func TestResolveExternalVariantNoCandidate(t *testing.T) {
	cases := []struct {
		name    string
		variant string
		value   any
	}{
		{"empty variant and nil value", "", nil},
		{"object without variant_id", "", map[string]any{"flag": true}},
		{"object with non-string variant_id", "", map[string]any{"variant_id": 7}},
		{"numeric value", "", 42},
		{"bool value", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, known := ResolveExternalVariant(tc.variant, tc.value, []string{"treatment"})
			if got != ControlVariantID || known {
				t.Errorf("ResolveExternalVariant = (%q, %v), want (control, false)", got, known)
			}
		})
	}
}
