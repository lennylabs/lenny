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
