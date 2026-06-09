// SPDX-License-Identifier: MIT

package experimentprovider

import (
	"context"
	"errors"
	"testing"

	"github.com/open-feature/go-sdk/openfeature"
	"github.com/open-feature/go-sdk/openfeature/memprovider"

	"github.com/lennylabs/lenny/pkg/experiment"
)

// memFactory returns a providerFactory whose every call yields a fresh
// OpenFeature in-memory provider exposing the given flags. It stands in
// for the network-bound vendor SDKs so the §10.7 evaluation path is
// exercised without a live LaunchDarkly/Statsig/Unleash service.
func memFactory(flags map[string]memprovider.InMemoryFlag) providerFactory {
	return func(experiment.TargetingConfig) (openfeature.FeatureProvider, error) {
		p := memprovider.NewInMemoryProvider(flags)
		return &p, nil
	}
}

func ldCfg() experiment.TargetingConfig {
	return experiment.TargetingConfig{
		Provider:     experiment.TargetingProviderLaunchDarkly,
		LaunchDarkly: &experiment.LaunchDarklyConfig{SDKKey: "sdk-key"},
	}
}

// spec: §10.7 line 825 — the gateway reads the EvaluationDetails Variant
// then Value from the configured OpenFeature client. A static enabled
// flag resolves to its default variant.
func TestEvaluatorStaticVariant_spec_10_7_825(t *testing.T) {
	cache := newCacheWithFactory(memFactory(map[string]memprovider.InMemoryFlag{
		"exp_a": {
			State:          memprovider.Enabled,
			DefaultVariant: "treatment",
			Variants:       map[string]any{"treatment": "treatment", "control": "control"},
		},
	}))
	ev, err := cache.For(context.Background(), ldCfg())
	if err != nil {
		t.Fatalf("For: %v", err)
	}
	res, err := ev.Evaluate(context.Background(), "exp_a", map[string]any{"targetingKey": "alice"})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if res.Variant != "treatment" {
		t.Errorf("variant = %q, want treatment", res.Variant)
	}
	if res.Value != "treatment" {
		t.Errorf("value = %v, want treatment", res.Value)
	}
}

// spec: §10.7 line 825 — the evaluationContext (targetingKey, user_id,
// tenant_id, runtime) reaches the provider, so a context-driven flag
// resolves per session. This proves toEvaluationContext lifts the flat
// map's targetingKey into the OpenFeature context key.
func TestEvaluatorContextDrivenVariant_spec_10_7_825(t *testing.T) {
	eval := func(_ memprovider.InMemoryFlag, flat openfeature.FlattenedContext) (any, openfeature.ProviderResolutionDetail) {
		if key, _ := flat[openfeature.TargetingKey].(string); key == "alice" {
			return "treatment", openfeature.ProviderResolutionDetail{Variant: "treatment", Reason: openfeature.TargetingMatchReason}
		}
		return "control", openfeature.ProviderResolutionDetail{Variant: "control", Reason: openfeature.DefaultReason}
	}
	cache := newCacheWithFactory(memFactory(map[string]memprovider.InMemoryFlag{
		"exp_a": {State: memprovider.Enabled, DefaultVariant: "control",
			Variants: map[string]any{"treatment": "treatment", "control": "control"}, ContextEvaluator: &eval},
	}))
	ev, err := cache.For(context.Background(), ldCfg())
	if err != nil {
		t.Fatalf("For: %v", err)
	}
	alice, err := ev.Evaluate(context.Background(), "exp_a", map[string]any{"targetingKey": "alice", "tenant_id": "acme"})
	if err != nil || alice.Variant != "treatment" {
		t.Errorf("alice → variant %q err %v, want treatment", alice.Variant, err)
	}
	bob, err := ev.Evaluate(context.Background(), "exp_a", map[string]any{"targetingKey": "bob"})
	if err != nil || bob.Variant != "control" {
		t.Errorf("bob → variant %q err %v, want control", bob.Variant, err)
	}
}

// spec: §10.7 line 833 — a provider-returned error (here FLAG_NOT_FOUND)
// is the targeting_failed condition: Evaluate returns a *EvalError
// carrying the OpenFeature ErrorCode.
func TestEvaluatorFlagNotFoundIsEvalError_spec_10_7_833(t *testing.T) {
	cache := newCacheWithFactory(memFactory(map[string]memprovider.InMemoryFlag{}))
	ev, err := cache.For(context.Background(), ldCfg())
	if err != nil {
		t.Fatalf("For: %v", err)
	}
	_, err = ev.Evaluate(context.Background(), "missing", map[string]any{"targetingKey": "alice"})
	var ee *EvalError
	if !errors.As(err, &ee) {
		t.Fatalf("error = %v, want *EvalError", err)
	}
	if ee.Code != string(openfeature.FlagNotFoundCode) {
		t.Errorf("code = %q, want %q", ee.Code, openfeature.FlagNotFoundCode)
	}
}

// The cache builds one client per distinct config and reuses it across
// sessions, because the vendor SDK clients hold background connections.
func TestCacheReusesEvaluatorPerConfig(t *testing.T) {
	cache := newCacheWithFactory(memFactory(map[string]memprovider.InMemoryFlag{}))
	a, err := cache.For(context.Background(), ldCfg())
	if err != nil {
		t.Fatalf("For #1: %v", err)
	}
	b, err := cache.For(context.Background(), ldCfg())
	if err != nil {
		t.Fatalf("For #2: %v", err)
	}
	if a != b {
		t.Error("a second For with the same config built a new Evaluator; want the cached one")
	}
}

// The Statsig and Unleash Go SDKs initialise process-global state, so a
// second, conflicting config for either is rejected (it would clobber
// the first tenant's client). The rejection is the targeting_failed
// condition for the second tenant.
func TestCacheProcessGlobalConflictRejected(t *testing.T) {
	cache := newCacheWithFactory(memFactory(map[string]memprovider.InMemoryFlag{}))
	cfgA := experiment.TargetingConfig{Provider: experiment.TargetingProviderStatsig, Statsig: &experiment.StatsigConfig{ServerSecret: "secret-A"}}
	cfgB := experiment.TargetingConfig{Provider: experiment.TargetingProviderStatsig, Statsig: &experiment.StatsigConfig{ServerSecret: "secret-B"}}
	if _, err := cache.For(context.Background(), cfgA); err != nil {
		t.Fatalf("For(A): %v", err)
	}
	if _, err := cache.For(context.Background(), cfgB); err == nil {
		t.Error("a second, different statsig config was accepted; want rejection")
	}
	// The original config still resolves from cache.
	if _, err := cache.For(context.Background(), cfgA); err != nil {
		t.Errorf("For(A) again: %v", err)
	}
}

// LaunchDarkly is per-instance (the provider wraps an explicit client),
// so two different LaunchDarkly configs both build clients.
func TestCacheLaunchDarklyAllowsDistinctConfigs(t *testing.T) {
	cache := newCacheWithFactory(memFactory(map[string]memprovider.InMemoryFlag{}))
	a := experiment.TargetingConfig{Provider: experiment.TargetingProviderLaunchDarkly, LaunchDarkly: &experiment.LaunchDarklyConfig{SDKKey: "key-a"}}
	b := experiment.TargetingConfig{Provider: experiment.TargetingProviderLaunchDarkly, LaunchDarkly: &experiment.LaunchDarklyConfig{SDKKey: "key-b"}}
	if _, err := cache.For(context.Background(), a); err != nil {
		t.Fatalf("For(a): %v", err)
	}
	if _, err := cache.For(context.Background(), b); err != nil {
		t.Errorf("For(b): %v — distinct LaunchDarkly configs must both build", err)
	}
}

// For rejects OFREP (handled by pkg/gateway/ofrep, not this package) and
// any provider this build does not ship.
func TestCacheRejectsNonSDKProvider(t *testing.T) {
	cache := newCacheWithFactory(memFactory(map[string]memprovider.InMemoryFlag{}))
	if _, err := cache.For(context.Background(), experiment.TargetingConfig{Provider: experiment.TargetingProviderOFREP, OFREP: &experiment.OFREPConfig{Endpoint: "https://x"}}); err == nil {
		t.Error("ofrep was accepted by the SDK-provider cache; want rejection")
	}
}

// spec: §10.7 lines 805-822 — defaultProviderFactory dispatches on the
// provider name and validates the matching sub-block before constructing
// the vendor provider. Statsig and Unleash construct without network
// (their Init is deferred); LaunchDarkly's network handshake is not
// exercised here.
func TestDefaultProviderFactoryDispatchAndValidation_spec_10_7(t *testing.T) {
	t.Run("launchdarkly missing sdkKey", func(t *testing.T) {
		if _, err := defaultProviderFactory(experiment.TargetingConfig{Provider: experiment.TargetingProviderLaunchDarkly, LaunchDarkly: &experiment.LaunchDarklyConfig{}}); err == nil {
			t.Error("missing launchdarkly.sdkKey accepted; want error")
		}
	})
	t.Run("statsig missing secret", func(t *testing.T) {
		if _, err := defaultProviderFactory(experiment.TargetingConfig{Provider: experiment.TargetingProviderStatsig, Statsig: &experiment.StatsigConfig{}}); err == nil {
			t.Error("missing statsig.serverSecret accepted; want error")
		}
	})
	t.Run("statsig valid constructs", func(t *testing.T) {
		p, err := defaultProviderFactory(experiment.TargetingConfig{Provider: experiment.TargetingProviderStatsig, Statsig: &experiment.StatsigConfig{ServerSecret: "secret"}})
		if err != nil || p == nil {
			t.Errorf("valid statsig config: provider %v err %v, want non-nil provider", p, err)
		}
	})
	t.Run("unleash missing url", func(t *testing.T) {
		if _, err := defaultProviderFactory(experiment.TargetingConfig{Provider: experiment.TargetingProviderUnleash, Unleash: &experiment.UnleashConfig{APIToken: "t"}}); err == nil {
			t.Error("missing unleash.apiUrl accepted; want error")
		}
	})
	t.Run("unleash valid constructs", func(t *testing.T) {
		p, err := defaultProviderFactory(experiment.TargetingConfig{Provider: experiment.TargetingProviderUnleash, Unleash: &experiment.UnleashConfig{APIURL: "https://unleash.internal/api", APIToken: "t"}})
		if err != nil || p == nil {
			t.Errorf("valid unleash config: provider %v err %v, want non-nil provider", p, err)
		}
	})
	t.Run("ofrep is not an SDK provider", func(t *testing.T) {
		if _, err := defaultProviderFactory(experiment.TargetingConfig{Provider: experiment.TargetingProviderOFREP}); err == nil {
			t.Error("ofrep accepted by SDK factory; want error")
		}
	})
}

func TestFingerprintStability(t *testing.T) {
	if fingerprint(ldCfg()) != fingerprint(ldCfg()) {
		t.Error("fingerprint is not stable for an identical config")
	}
	a := experiment.TargetingConfig{Provider: experiment.TargetingProviderStatsig, Statsig: &experiment.StatsigConfig{ServerSecret: "a"}}
	b := experiment.TargetingConfig{Provider: experiment.TargetingProviderStatsig, Statsig: &experiment.StatsigConfig{ServerSecret: "b"}}
	if fingerprint(a) == fingerprint(b) {
		t.Error("distinct statsig secrets share a fingerprint")
	}
}
