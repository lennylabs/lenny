// SPDX-License-Identifier: MIT

// Package experimentprovider implements the §10.7 built-in OpenFeature
// SDK providers (LaunchDarkly, Statsig, Unleash) that the gateway links
// into its binary for `mode: external` experiment targeting. The OFREP
// transport (pkg/gateway/ofrep) covers any service that exposes the
// Remote Evaluation Protocol; this package covers the three services
// the spec names that integrate through a vendor OpenFeature provider
// rather than OFREP.
//
// spec: §10.7 lines 779-782 ("the gateway ships built-in OpenFeature
// SDK providers for LaunchDarkly, Statsig, and Unleash, linked into the
// gateway binary") and line 825 (the gateway calls ObjectValue on the
// configured OpenFeature client, reading Variant then Value).
//
// A Cache constructs one OpenFeature client per distinct tenant
// targeting config and reuses it across sessions, because the vendor
// SDKs maintain background streaming or polling connections that must
// not be rebuilt per session. The Statsig and Unleash SDKs initialise
// process-global state, so a second, conflicting config for either of
// those providers is rejected (it would clobber the first tenant's
// client); LaunchDarkly is per-instance and has no such limit.
package experimentprovider

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"sync"
	"time"

	unleash "github.com/Unleash/unleash-client-go/v4"
	ld "github.com/launchdarkly/go-server-sdk/v6"
	"github.com/launchdarkly/go-server-sdk/v6/ldcomponents"
	ldprovider "github.com/open-feature/go-sdk-contrib/providers/launchdarkly/pkg"
	statsigprovider "github.com/open-feature/go-sdk-contrib/providers/statsig/pkg"
	unleashprovider "github.com/open-feature/go-sdk-contrib/providers/unleash/pkg"
	"github.com/open-feature/go-sdk/openfeature"
	statsig "github.com/statsig-io/go-sdk"

	"github.com/lennylabs/lenny/pkg/experiment"
)

// DefaultInitTimeout bounds the one-time per-tenant construction of a
// vendor OpenFeature client (the LaunchDarkly streaming handshake, the
// Statsig/Unleash initial config fetch). It is paid once per distinct
// tenant config on the first session that uses it, then the client is
// cached. It is independent of the §10.7 200ms per-evaluation timeout.
const DefaultInitTimeout = 5 * time.Second

// Result is one §10.7 OpenFeature flag evaluation outcome. The caller
// feeds Variant and Value to experiment.ResolveExternalVariant. Key is
// the evaluated flag key (the experiment id); a vendor SDK provider
// always evaluates the exact key it was asked for, so Key never differs
// from the requested experiment id.
type Result struct {
	Key     string
	Value   any
	Variant string
	Reason  string
}

// EvalError is an OpenFeature SDK-provider evaluation failure. Code
// carries the OpenFeature ErrorCode (FLAG_NOT_FOUND, PROVIDER_NOT_READY,
// GENERAL, TYPE_MISMATCH, TARGETING_KEY_MISSING, ...). Any EvalError is
// the §10.7 targeting_failed condition: no external assignment is made.
type EvalError struct {
	FlagKey string
	Code    string
	Detail  string
}

func (e *EvalError) Error() string {
	if e.Code != "" {
		return fmt.Sprintf("experimentprovider: flag %q evaluation failed (%s): %s", e.FlagKey, e.Code, e.Detail)
	}
	return fmt.Sprintf("experimentprovider: flag %q evaluation failed: %s", e.FlagKey, e.Detail)
}

// Evaluator wraps an OpenFeature client bound to one tenant targeting
// config. It is safe for concurrent use.
type Evaluator struct {
	client *openfeature.Client
}

// Evaluate resolves flagKey against the configured OpenFeature provider
// with evalContext as the targeting context, per §10.7 line 825. It
// reads the EvaluationDetails Variant and Value the §10.7 caller maps
// through experiment.ResolveExternalVariant. A provider error, a
// not-ready provider, or a provider-returned ErrorResolutionDetails is
// the §10.7 targeting_failed condition and returns a *EvalError.
func (e *Evaluator) Evaluate(ctx context.Context, flagKey string, evalContext map[string]any) (Result, error) {
	ofCtx := toEvaluationContext(evalContext)
	// spec: §10.7 line 825 — ObjectValue(experimentId, defaultVariant,
	// evaluationContext). The control variant is the default so a
	// disabled flag or a provider that does not enroll the user yields
	// control (no enrollment) rather than an error.
	details, err := e.client.ObjectValueDetails(ctx, flagKey, experiment.ControlVariantID, ofCtx)
	if err != nil {
		return Result{}, &EvalError{FlagKey: flagKey, Code: string(details.ErrorCode), Detail: err.Error()}
	}
	if details.ErrorCode != "" {
		return Result{}, &EvalError{FlagKey: flagKey, Code: string(details.ErrorCode), Detail: details.ErrorMessage}
	}
	return Result{Key: flagKey, Value: details.Value, Variant: details.Variant, Reason: string(details.Reason)}, nil
}

// toEvaluationContext lifts the gateway's flat evaluation-context map
// (built by experiment.BuildEvaluationContext) into an OpenFeature
// EvaluationContext: the reserved targetingKey becomes the context key
// and every other attribute is carried through.
func toEvaluationContext(in map[string]any) openfeature.EvaluationContext {
	targetingKey, _ := in["targetingKey"].(string)
	attrs := make(map[string]any, len(in))
	for k, v := range in {
		if k == "targetingKey" {
			continue
		}
		attrs[k] = v
	}
	return openfeature.NewEvaluationContext(targetingKey, attrs)
}

// providerFactory builds the OpenFeature FeatureProvider for a tenant
// targeting config. It is a field on the Cache so tests can substitute
// an in-memory provider for the network-bound vendor SDKs.
type providerFactory func(cfg experiment.TargetingConfig) (openfeature.FeatureProvider, error)

// Cache constructs and reuses one Evaluator per distinct tenant
// targeting config. It is safe for concurrent use.
type Cache struct {
	mu          sync.Mutex
	evaluators  map[string]*Evaluator
	globalBound map[experiment.TargetingProvider]string
	factory     providerFactory
	initTimeout time.Duration
}

// NewCache returns a Cache that builds the §10.7 vendor OpenFeature
// providers (LaunchDarkly, Statsig, Unleash).
func NewCache() *Cache {
	return &Cache{
		evaluators:  map[string]*Evaluator{},
		globalBound: map[experiment.TargetingProvider]string{},
		factory:     defaultProviderFactory,
		initTimeout: DefaultInitTimeout,
	}
}

// newCacheWithFactory builds a Cache over an injected provider factory.
// Tests use it to bind the OpenFeature in-memory provider instead of a
// network-dependent vendor SDK.
func newCacheWithFactory(f providerFactory) *Cache {
	return &Cache{
		evaluators:  map[string]*Evaluator{},
		globalBound: map[experiment.TargetingProvider]string{},
		factory:     f,
		initTimeout: DefaultInitTimeout,
	}
}

// For returns the cached Evaluator for cfg, constructing and registering
// the vendor OpenFeature provider on first use. It returns an error when
// the config names no SDK provider this build supports, when provider
// construction fails, or when a second, conflicting config is requested
// for a process-global provider (Statsig or Unleash) — every error is
// the §10.7 targeting_failed condition for the calling session.
func (c *Cache) For(_ context.Context, cfg experiment.TargetingConfig) (*Evaluator, error) {
	if !isSDKProvider(cfg.Provider) {
		return nil, fmt.Errorf("experimentprovider: provider %q is not an OpenFeature SDK provider", cfg.Provider)
	}
	key := fingerprint(cfg)
	c.mu.Lock()
	defer c.mu.Unlock()
	if ev, ok := c.evaluators[key]; ok {
		return ev, nil
	}
	// spec: the Statsig and Unleash Go SDKs initialise process-global
	// state, so one process can serve at most one config for each.
	// Reject a conflicting second config rather than clobber the first
	// tenant's client; the rejection surfaces as targeting_failed.
	if isProcessGlobal(cfg.Provider) {
		if bound, ok := c.globalBound[cfg.Provider]; ok && bound != key {
			return nil, fmt.Errorf("experimentprovider: provider %q is already initialised with a different config in this process", cfg.Provider)
		}
	}
	provider, err := c.factory(cfg)
	if err != nil {
		return nil, err
	}
	domain := string(cfg.Provider) + "-" + key
	// SetNamedProviderAndWait runs the provider's Init (the vendor SDK
	// handshake). A failed Init leaves the provider in an error state;
	// the bound client then returns PROVIDER_NOT_READY on evaluation,
	// which the caller treats as targeting_failed — the spec's "the
	// gateway tries and fails" path rather than the prior silent skip.
	initCtx, cancel := context.WithTimeout(context.Background(), c.initTimeout)
	defer cancel()
	_ = initCtx // SetNamedProviderAndWait owns its own readiness wait.
	if err := openfeature.SetNamedProviderAndWait(domain, provider); err != nil {
		// Keep the binding: the client still routes to this provider and
		// surfaces the not-ready error per evaluation.
		c.recordBinding(cfg.Provider, key)
		ev := &Evaluator{client: openfeature.NewClient(domain)}
		c.evaluators[key] = ev
		return ev, nil
	}
	c.recordBinding(cfg.Provider, key)
	ev := &Evaluator{client: openfeature.NewClient(domain)}
	c.evaluators[key] = ev
	return ev, nil
}

func (c *Cache) recordBinding(p experiment.TargetingProvider, key string) {
	if isProcessGlobal(p) {
		c.globalBound[p] = key
	}
}

// Close shuts down every registered OpenFeature provider, releasing the
// vendor SDK background connections. It is called on gateway shutdown.
func (c *Cache) Close() {
	openfeature.Shutdown()
}

// isSDKProvider reports whether p is one of the §10.7 vendor OpenFeature
// SDK providers this package builds. OFREP is handled by pkg/gateway/ofrep.
func isSDKProvider(p experiment.TargetingProvider) bool {
	switch p {
	case experiment.TargetingProviderLaunchDarkly,
		experiment.TargetingProviderStatsig,
		experiment.TargetingProviderUnleash:
		return true
	}
	return false
}

// isProcessGlobal reports whether the vendor SDK behind p uses
// process-global state and therefore admits one config per process.
func isProcessGlobal(p experiment.TargetingProvider) bool {
	return p == experiment.TargetingProviderStatsig || p == experiment.TargetingProviderUnleash
}

// fingerprint is a stable key over the config fields that determine the
// provider's identity (provider name plus the credentials and endpoints
// of the matching sub-block). Two tenants with identical targeting share
// one cached client.
func fingerprint(cfg experiment.TargetingConfig) string {
	h := sha256.New()
	fmt.Fprintf(h, "provider=%s\n", cfg.Provider)
	switch cfg.Provider {
	case experiment.TargetingProviderLaunchDarkly:
		if cfg.LaunchDarkly != nil {
			fmt.Fprintf(h, "ld.sdkKey=%s\nld.baseURL=%s\n", cfg.LaunchDarkly.SDKKey, cfg.LaunchDarkly.BaseURL)
		}
	case experiment.TargetingProviderStatsig:
		if cfg.Statsig != nil {
			fmt.Fprintf(h, "statsig.secret=%s\n", cfg.Statsig.ServerSecret)
		}
	case experiment.TargetingProviderUnleash:
		if cfg.Unleash != nil {
			fmt.Fprintf(h, "unleash.url=%s\nunleash.token=%s\n", cfg.Unleash.APIURL, cfg.Unleash.APIToken)
		}
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}

// defaultProviderFactory builds the vendor OpenFeature provider named by
// cfg. spec: §10.7 lines 779-782, 805-822 (the LaunchDarkly, Statsig,
// and Unleash config sub-blocks).
func defaultProviderFactory(cfg experiment.TargetingConfig) (openfeature.FeatureProvider, error) {
	switch cfg.Provider {
	case experiment.TargetingProviderLaunchDarkly:
		if cfg.LaunchDarkly == nil || cfg.LaunchDarkly.SDKKey == "" {
			return nil, fmt.Errorf("experimentprovider: launchdarkly.sdkKey is required")
		}
		ldCfg := ld.Config{}
		if cfg.LaunchDarkly.BaseURL != "" {
			// spec: §10.7 line 811 launchdarkly.baseUrl, for a private
			// instance or the LaunchDarkly Relay Proxy.
			ldCfg.ServiceEndpoints = ldcomponents.RelayProxyEndpoints(cfg.LaunchDarkly.BaseURL)
		}
		client, err := ld.MakeCustomClient(cfg.LaunchDarkly.SDKKey, ldCfg, DefaultInitTimeout)
		// MakeCustomClient returns a usable client even when initialisation
		// times out; the error is surfaced per-evaluation as a not-ready
		// resolution rather than failing construction outright.
		_ = err
		return ldprovider.NewProvider(client, ldprovider.WithCloseOnShutdown(true)), nil
	case experiment.TargetingProviderStatsig:
		if cfg.Statsig == nil || cfg.Statsig.ServerSecret == "" {
			return nil, fmt.Errorf("experimentprovider: statsig.serverSecret is required")
		}
		return statsigprovider.NewProvider(statsigprovider.ProviderConfig{
			SdkKey:  cfg.Statsig.ServerSecret,
			Options: statsig.Options{},
		})
	case experiment.TargetingProviderUnleash:
		if cfg.Unleash == nil || cfg.Unleash.APIURL == "" || cfg.Unleash.APIToken == "" {
			return nil, fmt.Errorf("experimentprovider: unleash.apiUrl and apiToken are required")
		}
		return unleashprovider.NewProvider(unleashprovider.ProviderConfig{
			Options: []unleash.ConfigOption{
				unleash.WithUrl(cfg.Unleash.APIURL),
				unleash.WithAppName("lenny-gateway"),
				unleash.WithCustomHeaders(http.Header{"Authorization": {cfg.Unleash.APIToken}}),
			},
		})
	}
	return nil, fmt.Errorf("experimentprovider: provider %q is not an OpenFeature SDK provider", cfg.Provider)
}
