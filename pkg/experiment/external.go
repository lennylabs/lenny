// SPDX-License-Identifier: MIT

package experiment

import (
	"errors"
	"fmt"
)

// TargetingProvider is the §10.7 experimentTargeting.provider enum:
// the external-targeting integration a tenant uses to resolve
// mode:external experiment assignment.
type TargetingProvider string

const (
	TargetingProviderOFREP        TargetingProvider = "ofrep"
	TargetingProviderLaunchDarkly TargetingProvider = "launchdarkly"
	TargetingProviderStatsig      TargetingProvider = "statsig"
	TargetingProviderUnleash      TargetingProvider = "unleash"
)

// AllTargetingProviders returns the closed §10.7 enum.
func AllTargetingProviders() []TargetingProvider {
	return []TargetingProvider{
		TargetingProviderOFREP, TargetingProviderLaunchDarkly,
		TargetingProviderStatsig, TargetingProviderUnleash,
	}
}

// IsValid reports whether p is one of the §10.7 targeting providers.
func (p TargetingProvider) IsValid() bool {
	for _, v := range AllTargetingProviders() {
		if p == v {
			return true
		}
	}
	return false
}

// DefaultTargetingTimeoutMs is the §10.7 experimentTargeting.timeoutMs
// default — the session-creation hot-path budget for an external
// evaluation.
const DefaultTargetingTimeoutMs = 200

// OFREPConfig is the §10.7 experimentTargeting.ofrep block.
type OFREPConfig struct {
	Endpoint string
	Headers  map[string]string
}

// LaunchDarklyConfig is the §10.7 experimentTargeting.launchdarkly block.
type LaunchDarklyConfig struct {
	SDKKey  string
	BaseURL string
}

// StatsigConfig is the §10.7 experimentTargeting.statsig block.
type StatsigConfig struct {
	ServerSecret string
}

// UnleashConfig is the §10.7 experimentTargeting.unleash block.
type UnleashConfig struct {
	APIURL   string
	APIToken string
}

// TargetingConfig is the §10.7 tenant experimentTargeting block: how a
// tenant resolves mode:external experiment assignment. The zero value
// (Provider == "") means the tenant configures no external targeting —
// its mode:external experiments are skipped and only percentage-mode
// experiments route.
type TargetingConfig struct {
	Provider     TargetingProvider
	TimeoutMs    int
	OFREP        *OFREPConfig
	LaunchDarkly *LaunchDarklyConfig
	Statsig      *StatsigConfig
	Unleash      *UnleashConfig
}

// Configured reports whether the tenant has set up external targeting.
func (c TargetingConfig) Configured() bool { return c.Provider != "" }

// Clone returns a deep copy of the config so a store never shares the
// pointed-to provider sub-blocks or the OFREP header map with a
// caller. The scalar fields are copied by value.
func (c TargetingConfig) Clone() TargetingConfig {
	cp := c
	if c.OFREP != nil {
		o := *c.OFREP
		if c.OFREP.Headers != nil {
			o.Headers = make(map[string]string, len(c.OFREP.Headers))
			for k, v := range c.OFREP.Headers {
				o.Headers[k] = v
			}
		}
		cp.OFREP = &o
	}
	if c.LaunchDarkly != nil {
		ld := *c.LaunchDarkly
		cp.LaunchDarkly = &ld
	}
	if c.Statsig != nil {
		s := *c.Statsig
		cp.Statsig = &s
	}
	if c.Unleash != nil {
		u := *c.Unleash
		cp.Unleash = &u
	}
	return cp
}

// EffectiveTimeoutMs returns the configured hot-path timeout, or
// DefaultTargetingTimeoutMs when none is set.
func (c TargetingConfig) EffectiveTimeoutMs() int {
	if c.TimeoutMs > 0 {
		return c.TimeoutMs
	}
	return DefaultTargetingTimeoutMs
}

// Validate reports the §10.7 invariants of an experimentTargeting
// block. A zero config is valid (no external targeting). When a
// provider is set it must be one of the §10.7 providers, the timeout
// must be non-negative, the provider's own sub-block must be present
// and complete, and no other provider's sub-block may be set.
func (c TargetingConfig) Validate() error {
	if c.Provider == "" {
		if c.OFREP != nil || c.LaunchDarkly != nil || c.Statsig != nil || c.Unleash != nil {
			return errors.New("experiment: experimentTargeting.provider is required when a provider block is set")
		}
		return nil
	}
	if !c.Provider.IsValid() {
		return fmt.Errorf("experiment: experimentTargeting.provider %q is not one of {ofrep, launchdarkly, statsig, unleash}", c.Provider)
	}
	if c.TimeoutMs < 0 {
		return errors.New("experiment: experimentTargeting.timeoutMs must be >= 0")
	}
	switch c.Provider {
	case TargetingProviderOFREP:
		if c.OFREP == nil || c.OFREP.Endpoint == "" {
			return errors.New("experiment: experimentTargeting.ofrep.endpoint is required for provider ofrep")
		}
	case TargetingProviderLaunchDarkly:
		if c.LaunchDarkly == nil || c.LaunchDarkly.SDKKey == "" {
			return errors.New("experiment: experimentTargeting.launchdarkly.sdkKey is required for provider launchdarkly")
		}
	case TargetingProviderStatsig:
		if c.Statsig == nil || c.Statsig.ServerSecret == "" {
			return errors.New("experiment: experimentTargeting.statsig.serverSecret is required for provider statsig")
		}
	case TargetingProviderUnleash:
		if c.Unleash == nil || c.Unleash.APIURL == "" || c.Unleash.APIToken == "" {
			return errors.New("experiment: experimentTargeting.unleash.apiUrl and apiToken are required for provider unleash")
		}
	}
	other := 0
	if c.Provider != TargetingProviderOFREP && c.OFREP != nil {
		other++
	}
	if c.Provider != TargetingProviderLaunchDarkly && c.LaunchDarkly != nil {
		other++
	}
	if c.Provider != TargetingProviderStatsig && c.Statsig != nil {
		other++
	}
	if c.Provider != TargetingProviderUnleash && c.Unleash != nil {
		other++
	}
	if other > 0 {
		return fmt.Errorf("experiment: experimentTargeting carries a provider block that does not match provider %q", c.Provider)
	}
	return nil
}

// EvaluationContextInput is the session data the §10.7 external
// targeting evaluation feeds into an OpenFeature evaluationContext.
type EvaluationContextInput struct {
	UserID    string
	TenantID  string
	SessionID string
	Runtime   string
	Labels    map[string]string
}

// BuildEvaluationContext assembles the §10.7 OpenFeature
// evaluationContext for a mode:external experiment evaluation. The
// targetingKey and user_id are the session's user id, or the
// deterministic anonymous pseudo-ID "anon:<session_id>" when the
// session has no user (§10.7 anonymous session handling). tenant_id
// and runtime are carried as attributes and any session labels are
// merged in. The reserved attributes are written last, so a label
// whose key collides with one of them cannot shadow it.
func BuildEvaluationContext(in EvaluationContextInput) map[string]any {
	userID := in.UserID
	if userID == "" {
		userID = "anon:" + in.SessionID
	}
	ctx := make(map[string]any, len(in.Labels)+4)
	for k, v := range in.Labels {
		ctx[k] = v
	}
	ctx["targetingKey"] = userID
	ctx["user_id"] = userID
	ctx["tenant_id"] = in.TenantID
	ctx["runtime"] = in.Runtime
	return ctx
}

// ResolveExternalVariant implements the §10.7 OpenFeature
// external-targeting variant resolution. A `mode: external` experiment
// is assigned by an OpenFeature provider, which returns an
// EvaluationDetails carrying an optional Variant string and a Value.
// The candidate variant is taken from Variant when it is set,
// otherwise from Value when Value is a string, otherwise from
// Value["variant_id"] when Value is an object that carries that key.
//
// known is true only when the resolved candidate is ControlVariantID
// or one of the experiment's registered variant IDs. Otherwise the
// provider response is unresolvable: variantID is ControlVariantID and
// known is false, and the §10.7 caller emits an
// experiment.unknown_variant_from_provider event and runs the session
// on control.
func ResolveExternalVariant(variant string, value any, registered []string) (variantID string, known bool) {
	candidate := extractProviderVariant(variant, value)
	if candidate == "" {
		return ControlVariantID, false
	}
	if candidate == ControlVariantID {
		return ControlVariantID, true
	}
	for _, r := range registered {
		if candidate == r {
			return candidate, true
		}
	}
	return ControlVariantID, false
}

// extractProviderVariant applies the §10.7 Variant/Value extraction
// precedence to an OpenFeature evaluation result. It returns "" when
// the result carries no usable variant identifier.
func extractProviderVariant(variant string, value any) string {
	if variant != "" {
		return variant
	}
	switch v := value.(type) {
	case string:
		return v
	case map[string]any:
		if id, ok := v["variant_id"].(string); ok {
			return id
		}
	}
	return ""
}
