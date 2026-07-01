// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"sort"
	"time"

	"github.com/lennylabs/lenny/pkg/events"
	"github.com/lennylabs/lenny/pkg/experiment"
	"github.com/lennylabs/lenny/pkg/gateway/experiment/experimentprovider"
	"github.com/lennylabs/lenny/pkg/gateway/experiment/experimentstore"
	"github.com/lennylabs/lenny/pkg/gateway/experiment/ofrep"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
)

// externalEvalResult is one §10.7 external-targeting evaluation outcome,
// shared by the OFREP transport and the built-in OpenFeature SDK
// providers so buildExternalEvaluator's closure is provider-agnostic.
type externalEvalResult struct {
	Variant string
	Value   any
	Key     string
}

// externalEvalClient is the §10.7 evaluation transport for one tenant:
// OFREP (pkg/gateway/ofrep) or a built-in OpenFeature SDK provider
// (pkg/gateway/experimentprovider). Both resolve one experiment flag to
// a variant/value.
type externalEvalClient interface {
	Evaluate(ctx context.Context, flagKey string, evalContext map[string]any) (externalEvalResult, error)
}

// ExternalProviderResolver returns the cached §10.7 OpenFeature SDK
// evaluator for a tenant's launchdarkly/statsig/unleash targeting
// config. The gateway wires *experimentprovider.Cache (adapted to this
// interface); a nil resolver disables the SDK-provider path and only
// OFREP-targeted experiments evaluate.
type ExternalProviderResolver interface {
	For(ctx context.Context, cfg experiment.TargetingConfig) (externalEvalClient, error)
}

// NewExternalProviderResolver adapts an *experimentprovider.Cache to the
// ExternalProviderResolver the session server consumes for §10.7
// mode:external targeting. The gateway calls this to wire the built-in
// OpenFeature SDK providers (launchdarkly, statsig, unleash).
func NewExternalProviderResolver(cache *experimentprovider.Cache) ExternalProviderResolver {
	return cacheResolver{cache: cache}
}

type cacheResolver struct{ cache *experimentprovider.Cache }

func (r cacheResolver) For(ctx context.Context, cfg experiment.TargetingConfig) (externalEvalClient, error) {
	ev, err := r.cache.For(ctx, cfg)
	if err != nil {
		return nil, err
	}
	return evaluatorEvalClient{ev: ev}, nil
}

// evaluatorEvalClient adapts an *experimentprovider.Evaluator to
// externalEvalClient.
type evaluatorEvalClient struct{ ev *experimentprovider.Evaluator }

func (c evaluatorEvalClient) Evaluate(ctx context.Context, flagKey string, evalContext map[string]any) (externalEvalResult, error) {
	r, err := c.ev.Evaluate(ctx, flagKey, evalContext)
	if err != nil {
		return externalEvalResult{}, err
	}
	return externalEvalResult{Variant: r.Variant, Value: r.Value, Key: r.Key}, nil
}

// ofrepEvalClient adapts an *ofrep.Client to externalEvalClient.
type ofrepEvalClient struct{ c *ofrep.Client }

func (a ofrepEvalClient) Evaluate(ctx context.Context, flagKey string, evalContext map[string]any) (externalEvalResult, error) {
	r, err := a.c.Evaluate(ctx, flagKey, evalContext)
	if err != nil {
		return externalEvalResult{}, err
	}
	return externalEvalResult{Variant: r.Variant, Value: r.Value, Key: r.Key}, nil
}

// constErrEvalClient is an externalEvalClient whose every evaluation
// fails with a fixed error. The §10.7 SDK-provider path uses it when
// provider construction itself fails, so the failure surfaces through
// the normal targeting_failed path (event + error counter + breaker)
// rather than as a silent skip.
type constErrEvalClient struct{ err error }

func (c constErrEvalClient) Evaluate(context.Context, string, map[string]any) (externalEvalResult, error) {
	return externalEvalResult{}, c.err
}

// ExperimentRouterPriority is the §4.8 built-in priority the
// ExperimentRouter occupies in the PreRoute phase chain (spec: §4.8
// line 21 priority table, line 115 field-dependency table). The
// ExperimentRouter runs in-process at session creation (routeExperiment)
// rather than as a generic interceptor.Interceptor because it operates
// on the session row and pool registry, not on the serialized chain
// payload. The gateway preserves the §4.8 line-12 ascending-priority
// ordering by running PreRoute external interceptors below this priority
// before routeExperiment and those at or above it after.
const ExperimentRouterPriority int32 = 300

// variantIsolationError is returned by ApplyExperimentRouting when the
// §10.7 ExperimentRouter fails closed: the assigned variant pool's
// isolation profile is weaker than the session's. The handler maps it
// to 422 VARIANT_ISOLATION_UNAVAILABLE.
type variantIsolationError struct {
	ExperimentID         string
	VariantID            string
	SessionMinIsolation  string
	VariantPoolIsolation string
}

func (e *variantIsolationError) Error() string {
	return fmt.Sprintf(
		"variant %q pool isolation %s is weaker than the session's %s",
		e.VariantID, e.VariantPoolIsolation, e.SessionMinIsolation,
	)
}

// ExperimentRejectionReporter receives §10.7 ExperimentRouter
// observability reports. ReportExperimentIsolationRejection records a
// fail-closed rejection (the `experiment.isolation_mismatch` event and
// the `lenny_experiment_isolation_rejections_total` counter). The
// targeting methods record the §16.1 lines 156-157 external-targeting
// metrics on the `mode: external` evaluation path. Defining the
// interface here keeps the session server decoupled from the audit and
// metrics subsystems, matching the DeriveAuditSink pattern. A nil
// reporter disables reporting; routing still proceeds.
type ExperimentRejectionReporter interface {
	ReportExperimentIsolationRejection(ctx context.Context, ev ExperimentIsolationRejection)
	// ObserveTargetingDuration records one external-targeting evaluation
	// latency for `lenny_experiment_targeting_duration_seconds` (§16.1
	// line 156). provider is the OpenFeature provider name, or the OFREP
	// endpoint hostname when provider:ofrep.
	ObserveTargetingDuration(ctx context.Context, provider string, seconds float64)
	// RecordTargetingError increments `lenny_experiment_targeting_error_total`
	// (§16.1 line 157) on a §10.7 targeting_failed condition. errorType
	// classifies the cause (timeout, transport, or the OFREP errorCode).
	RecordTargetingError(ctx context.Context, provider, errorType string)
}

// StickyCache is the §10.7 `sticky: user` variant-assignment cache the
// ExperimentRouter reads through for `mode: external` experiments so the
// OpenFeature provider "is not called again for subsequent sessions if a
// cached assignment exists" (§10.7 line 831). *experimentsticky.RedisCache
// satisfies it. A nil cache disables read-through; routing re-evaluates
// every experiment fresh, which is also the §12.4 Redis-outage fail-open
// path. Get/Put return Redis errors so the router degrades to fresh
// evaluation rather than failing the session.
type StickyCache interface {
	Get(ctx context.Context, tenantID, experimentID, userID string) (variantID string, ok bool, err error)
	Put(ctx context.Context, tenantID, experimentID, userID, variantID string) error
}

// ExperimentIsolationRejection is the §16.6 `experiment.isolation_mismatch`
// event payload. Field names carry through to the operational-event
// record and the §16.1 counter labels.
type ExperimentIsolationRejection struct {
	TenantID             string
	UserID               string
	ExperimentID         string
	VariantID            string
	SessionMinIsolation  string
	VariantPoolIsolation string
}

// ApplyExperimentRouting runs the §10.7 ExperimentRouter over a
// session at creation. The tenant's experiments whose baseRuntime
// matches the session's requested runtime are evaluated in created_at
// order; the first one that buckets the session to a non-control
// variant enrolls it. On enrollment the session's runtime and pool are
// rewritten to the variant's and the §10.7 experimentContext is
// recorded. A session that no experiment enrolls is left unchanged and
// runs the base runtime.
//
// spec: §8.2 line 90 / §10.7 — the delegation path invokes this same
// routing function on a child whose parent's propagation mode is
// `independent` (or whose parent carries no experimentContext), so
// the child is routed afresh through the ExperimentRouter rather than
// silently unenrolled.
//
// §10.7 ExperimentRouter isolation monotonicity: when the assigned
// variant pool's isolation profile is weaker than the session's, the
// router fails closed — it returns a *variantIsolationError and the
// session is not created, rather than silently routing the session
// into a less-isolated pool.
func (s *Server) ApplyExperimentRouting(ctx context.Context, row *sessionstore.Session) error {
	if s.experiments == nil {
		return nil
	}
	all, err := s.experiments.List(ctx, row.TenantID)
	if err != nil || len(all) == 0 {
		return nil
	}
	candidates := make([]experimentstore.Experiment, 0, len(all))
	for _, e := range all {
		if e.BaseRuntime == row.RuntimeRef {
			candidates = append(candidates, e)
		}
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		return candidates[i].CreatedAt.Before(candidates[j].CreatedAt)
	})
	defs := make([]experiment.Definition, len(candidates))
	for i, e := range candidates {
		defs[i] = e.Definition()
	}
	// §10.7: percentage-mode experiments bucket by the built-in hash;
	// mode:external experiments resolve through the tenant's OpenFeature
	// provider. A tenant with no external targeting yields a nil
	// evaluator and RouteMixed skips external-mode experiments.
	evaluator := s.buildExternalEvaluator(ctx, row, candidates)
	// §10.7 lines 831, 1096: a `mode: external` + `sticky: user` experiment
	// reads its assignment from the sticky cache before calling the provider,
	// and writes a fresh evaluation back. A Redis outage falls open to fresh
	// evaluation (§12.4 failure behavior). Percentage-mode `sticky: user`
	// assignment is deterministic in user_id under the built-in HMAC, so it
	// is recomputed rather than cached.
	evaluator = s.stickyWrappedEvaluator(ctx, row, candidates, evaluator)
	assignment := experiment.RouteMixed(defs, row.UserID, row.ID, evaluator)
	if assignment.ExperimentID == "" {
		return nil
	}
	variant, ok := findVariant(candidates, assignment.ExperimentID, assignment.VariantID)
	if !ok {
		return nil
	}
	// §10.7 fail-closed isolation check: the variant pool must be at
	// least as restrictive as the session's isolation profile.
	if err := s.checkVariantIsolation(ctx, row, assignment, variant); err != nil {
		return err
	}
	row.ExperimentContext = &sessionstore.ExperimentContext{
		ExperimentID: assignment.ExperimentID,
		VariantID:    assignment.VariantID,
	}
	if variant.Runtime != "" {
		row.RuntimeRef = variant.Runtime
	}
	if variant.Pool != "" {
		row.PoolRef = variant.Pool
	}
	// §16.6: the first-match rule left every routable experiment created
	// after the enrolled one unevaluated. Emit experiment.multi_eligible_skipped
	// so deployers can audit enrollment overlap.
	s.emitMultiEligibleSkipped(ctx, row, assignment.ExperimentID,
		experiment.SkippedAfter(defs, assignment.ExperimentID, evaluator != nil))
	return nil
}

// emitMultiEligibleSkipped records the §16.6
// experiment.multi_eligible_skipped operational event when the
// first-match rule left one or more later-created experiments
// unevaluated for an enrolled session. It is best-effort: a nil
// emitter or an empty skipped set is a no-op, so experiment routing
// never depends on the event buffer being wired.
func (s *Server) emitMultiEligibleSkipped(ctx context.Context, row *sessionstore.Session, enrolledID string, skipped []string) {
	if s.opsEmitter == nil || len(skipped) == 0 {
		return
	}
	data, _ := json.Marshal(map[string]any{
		"tenant_id":              row.TenantID,
		"user_id":                row.UserID,
		"enrolled_experiment_id": enrolledID,
		"skipped_experiment_ids": skipped,
	})
	_ = s.opsEmitter.Emit(ctx, events.OperationalEvent{
		Source:          "/v1/sessions",
		Type:            events.EventExperimentMultiEligibleSkipped.CloudEventsType(),
		Severity:        "info",
		DataContentType: "application/json",
		Data:            data,
	})
}

// buildExternalEvaluator returns the §10.7 OpenFeature external-targeting
// evaluator for a session, or nil when the tenant configures no
// external targeting or a provider this build does not ship. The
// closure calls the OFREP endpoint once per mode:external experiment;
// per §10.7 a provider failure makes no external assignment for any
// experiment, so the closure latches the failure, emits
// experiment.targeting_failed once, and returns no enrollment for
// every subsequent experiment.
func (s *Server) buildExternalEvaluator(ctx context.Context, row *sessionstore.Session, candidates []experimentstore.Experiment) experiment.ExternalEvaluator {
	if s.tenants == nil {
		return nil
	}
	tenant, err := s.tenants.Get(ctx, row.TenantID)
	if err != nil || !tenant.ExperimentTargeting.Configured() {
		return nil
	}
	cfg := tenant.ExperimentTargeting
	// spec: §10.7 lines 779-782 — OFREP (pkg/gateway/ofrep) or one of the
	// built-in OpenFeature SDK providers (launchdarkly, statsig, unleash,
	// pkg/gateway/experimentprovider). resolveExternalClient returns nil
	// for a provider this build cannot serve, which skips external-mode
	// experiments exactly as a tenant with no targeting does.
	client, providerLabel := s.resolveExternalClient(ctx, cfg)
	if client == nil {
		return nil
	}
	evalCtx := experiment.BuildEvaluationContext(experiment.EvaluationContextInput{
		UserID:    row.UserID,
		TenantID:  row.TenantID,
		SessionID: row.ID,
		Runtime:   row.RuntimeRef,
	})
	knownIDs := knownExperimentIDs(candidates)
	// spec: §10.7 lines 835-844 (SCL-023) — the per-tenant targeting
	// circuit-breaker thresholds resolved from the tenant config.
	breakerParams := targetingBreakerParams{
		threshold: cfg.BreakerFailureThreshold(),
		window:    time.Duration(cfg.BreakerWindowSeconds()) * time.Second,
		openDur:   time.Duration(cfg.BreakerOpenSeconds()) * time.Second,
	}
	failed := false
	return func(experimentID string) (string, bool) {
		if failed {
			return "", false
		}
		// spec: §10.7 lines 835-838 — while the circuit is open the gateway
		// skips the OpenFeature call entirely and returns empty assignment
		// with zero wait, same as a provider error. Latch so no further
		// external experiment is evaluated for this session.
		if !s.targetingBreaker.Allow(row.TenantID, providerLabel) {
			failed = true
			return "", false
		}
		start := time.Now()
		result, err := client.Evaluate(ctx, experimentID, evalCtx)
		// spec: §16.1 line 156 — record latency for every evaluation
		// attempt, success or failure.
		s.observeTargetingDuration(ctx, providerLabel, time.Since(start).Seconds())
		// spec: §10.7 lines 837-839 — feed the outcome back so consecutive
		// failures open the breaker and a half-open probe success closes it.
		s.targetingBreaker.Record(row.TenantID, providerLabel, breakerParams, err == nil)
		if err != nil {
			failed = true
			// spec: §10.7 line 833 / §16.1 line 157 — increment the
			// targeting-error counter alongside the targeting_failed event.
			s.recordTargetingError(ctx, providerLabel, classifyTargetingError(err))
			s.emitExperimentTargetingFailed(ctx, row, string(cfg.Provider), err)
			return "", false
		}
		// spec: §10.7 line 829 / §16.6 line 651 — a provider response that
		// echoes a flag/experiment key Lenny did not register is logged
		// with experiment.unknown_external_id and otherwise ignored (no
		// enrollment is made from an unregistered external experiment).
		if result.Key != "" && result.Key != experimentID && !knownIDs[result.Key] {
			s.emitExperimentUnknownExternalID(ctx, row, string(cfg.Provider), result.Key)
			return "", false
		}
		variantID, known := experiment.ResolveExternalVariant(
			result.Variant, result.Value, variantIDsOf(candidates, experimentID),
		)
		if !known {
			s.emitExperimentUnknownVariant(ctx, row, experimentID, string(cfg.Provider), result.Variant)
			return "", false
		}
		return variantID, true
	}
}

// resolveExternalClient builds the §10.7 evaluation transport for a
// tenant targeting config and the §16.1 line 156 provider metric label.
// OFREP constructs a per-session HTTP client; the launchdarkly, statsig,
// and unleash SDK providers resolve through the cached
// ExternalProviderResolver (the vendor OpenFeature clients hold
// background connections and must not be rebuilt per session). A
// provider this build cannot serve returns a nil client, which skips
// external-mode experiments. A construction failure on the SDK-provider
// path returns a constErrEvalClient so the failure surfaces as
// targeting_failed rather than a silent skip — the gap F-10.7.3 named,
// where the gateway never even tried for launchdarkly/statsig/unleash.
func (s *Server) resolveExternalClient(ctx context.Context, cfg experiment.TargetingConfig) (externalEvalClient, string) {
	switch cfg.Provider {
	case experiment.TargetingProviderOFREP:
		if cfg.OFREP == nil {
			return nil, ""
		}
		client, err := ofrep.New(ofrep.Options{
			BaseURL: cfg.OFREP.Endpoint,
			Headers: cfg.OFREP.Headers,
			Timeout: time.Duration(cfg.EffectiveTimeoutMs()) * time.Millisecond,
		})
		if err != nil {
			return nil, ""
		}
		return ofrepEvalClient{c: client}, targetingProviderLabel(cfg.Provider, cfg.OFREP.Endpoint)
	case experiment.TargetingProviderLaunchDarkly,
		experiment.TargetingProviderStatsig,
		experiment.TargetingProviderUnleash:
		if s.externalProviders == nil {
			return nil, ""
		}
		client, err := s.externalProviders.For(ctx, cfg)
		if err != nil {
			return constErrEvalClient{err: err}, string(cfg.Provider)
		}
		return client, string(cfg.Provider)
	}
	return nil, ""
}

// stickyWrappedEvaluator returns inner wrapped with the §10.7 sticky-cache
// read-through for `mode: external` + `sticky: user` experiments. For such
// an experiment the wrapper consults the cache first (skipping the
// OpenFeature provider on a hit) and writes a fresh provider result back.
// Any other experiment, or a cache miss / Redis error, falls through to
// inner unchanged so routing degrades to fresh evaluation. When no sticky
// cache is wired the wrapper returns inner untouched.
//
// A cached control assignment is kept and returned: the provider is not
// re-called for a user the experiment already bucketed to control, and
// RouteMixed treats the control variant as no-enrollment exactly as it does
// for a fresh control result.
//
// spec: §10.7 line 831 (provider not re-called when a cached assignment
// exists), line 1096 (the cache the pause/conclude flush clears).
func (s *Server) stickyWrappedEvaluator(ctx context.Context, row *sessionstore.Session, candidates []experimentstore.Experiment, inner experiment.ExternalEvaluator) experiment.ExternalEvaluator {
	if s.stickyCache == nil {
		return inner
	}
	stickyExternal := make(map[string]bool, len(candidates))
	for _, e := range candidates {
		d := e.Definition()
		if d.TargetingMode == experiment.TargetingExternal && d.Sticky == experiment.StickyUser {
			stickyExternal[e.ID] = true
		}
	}
	if len(stickyExternal) == 0 {
		return inner
	}
	return func(experimentID string) (string, bool) {
		if !stickyExternal[experimentID] {
			if inner == nil {
				return "", false
			}
			return inner(experimentID)
		}
		if v, ok, err := s.stickyCache.Get(ctx, row.TenantID, experimentID, row.UserID); err == nil && ok {
			return v, true
		}
		if inner == nil {
			return "", false
		}
		v, ok := inner(experimentID)
		if ok {
			// Best-effort write-back: a Redis failure here is the §12.4
			// fail-open path (the next session re-evaluates), not a session
			// error.
			_ = s.stickyCache.Put(ctx, row.TenantID, experimentID, row.UserID, v)
		}
		return v, ok
	}
}

// observeTargetingDuration forwards an external-targeting evaluation
// latency to the wired reporter. Best-effort: a nil reporter is a no-op.
// spec: §16.1 line 156.
func (s *Server) observeTargetingDuration(ctx context.Context, provider string, seconds float64) {
	if s.experimentReporter == nil {
		return
	}
	s.experimentReporter.ObserveTargetingDuration(ctx, provider, seconds)
}

// recordTargetingError forwards a §10.7 targeting_failed classification
// to the wired reporter. Best-effort: a nil reporter is a no-op.
// spec: §16.1 line 157.
func (s *Server) recordTargetingError(ctx context.Context, provider, errorType string) {
	if s.experimentReporter == nil {
		return
	}
	s.experimentReporter.RecordTargetingError(ctx, provider, errorType)
}

// targetingProviderLabel derives the §16.1 line 156 `provider` metric
// label. For provider:ofrep the OFREP endpoint hostname is used; any
// other provider uses its name verbatim. An unparseable endpoint falls
// back to the provider name so the label is never empty.
func targetingProviderLabel(provider experiment.TargetingProvider, endpoint string) string {
	if provider == experiment.TargetingProviderOFREP {
		if u, err := url.Parse(endpoint); err == nil && u.Hostname() != "" {
			return u.Hostname()
		}
	}
	return string(provider)
}

// classifyTargetingError reduces an OFREP evaluation failure to a bounded
// §16.1 line 157 error_type label. A provider-level *ofrep.EvalError
// carries the OFREP errorCode (or "http_error" when the non-2xx response
// had no code); a timeout is "timeout"; any other transport failure is
// "transport". The label set is bounded so the counter does not explode
// cardinality on arbitrary error strings.
func classifyTargetingError(err error) string {
	var ee *ofrep.EvalError
	if errors.As(err, &ee) {
		if ee.Code != "" {
			return ee.Code
		}
		return "http_error"
	}
	// spec: §16.1 line 157 — an OpenFeature SDK-provider failure carries
	// the bounded OpenFeature ErrorCode (FLAG_NOT_FOUND, PROVIDER_NOT_READY,
	// GENERAL, TYPE_MISMATCH, ...); fall back to "provider_error" when the
	// SDK surfaced no code.
	var pe *experimentprovider.EvalError
	if errors.As(err, &pe) {
		if pe.Code != "" {
			return pe.Code
		}
		return "provider_error"
	}
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return "timeout"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "timeout"
	}
	return "transport"
}

// knownExperimentIDs is the set of registered experiment IDs among the
// routable candidates — the keys a provider response is matched against
// for the §10.7 line 829 unknown_external_id check.
func knownExperimentIDs(candidates []experimentstore.Experiment) map[string]bool {
	ids := make(map[string]bool, len(candidates))
	for _, e := range candidates {
		ids[e.ID] = true
	}
	return ids
}

// variantIDsOf returns the registered variant IDs of the named
// experiment among candidates — the set ResolveExternalVariant matches
// a provider response against.
func variantIDsOf(candidates []experimentstore.Experiment, experimentID string) []string {
	for _, e := range candidates {
		if e.ID != experimentID {
			continue
		}
		ids := make([]string, len(e.Variants))
		for i, v := range e.Variants {
			ids[i] = v.ID
		}
		return ids
	}
	return nil
}

// emitExperimentTargetingFailed records the §16.6
// experiment.targeting_failed operational event. Best-effort: a nil
// emitter is a no-op.
func (s *Server) emitExperimentTargetingFailed(ctx context.Context, row *sessionstore.Session, provider string, evalErr error) {
	if s.opsEmitter == nil {
		return
	}
	data, _ := json.Marshal(map[string]any{
		"tenant_id": row.TenantID,
		"user_id":   row.UserID,
		"provider":  provider,
		"error":     evalErr.Error(),
	})
	_ = s.opsEmitter.Emit(ctx, events.OperationalEvent{
		Source:          "/v1/sessions",
		Type:            events.EventExperimentTargetingFailed.CloudEventsType(),
		Severity:        "warning",
		DataContentType: "application/json",
		Data:            data,
	})
}

// emitExperimentUnknownVariant records the §16.6
// experiment.unknown_variant_from_provider operational event when an
// OpenFeature provider returns a variant the experiment does not
// register. Best-effort: a nil emitter is a no-op.
func (s *Server) emitExperimentUnknownVariant(ctx context.Context, row *sessionstore.Session, experimentID, provider, rawVariant string) {
	if s.opsEmitter == nil {
		return
	}
	data, _ := json.Marshal(map[string]any{
		"tenant_id":     row.TenantID,
		"user_id":       row.UserID,
		"experiment_id": experimentID,
		"provider":      provider,
		"raw_variant":   rawVariant,
	})
	_ = s.opsEmitter.Emit(ctx, events.OperationalEvent{
		Source:          "/v1/sessions",
		Type:            events.EventExperimentUnknownVariantFromProvider.CloudEventsType(),
		Severity:        "warning",
		DataContentType: "application/json",
		Data:            data,
	})
}

// emitExperimentUnknownExternalID records the §16.6 line 651
// experiment.unknown_external_id info event when an OpenFeature provider
// returns a flag/experiment key that is not registered in Lenny's
// mode:external catalog. Best-effort: a nil emitter is a no-op.
func (s *Server) emitExperimentUnknownExternalID(ctx context.Context, row *sessionstore.Session, provider, externalID string) {
	if s.opsEmitter == nil {
		return
	}
	data, _ := json.Marshal(map[string]any{
		"tenant_id":              row.TenantID,
		"user_id":                row.UserID,
		"provider":               provider,
		"external_experiment_id": externalID,
	})
	_ = s.opsEmitter.Emit(ctx, events.OperationalEvent{
		Source:          "/v1/sessions",
		Type:            events.EventExperimentUnknownExternalID.CloudEventsType(),
		Severity:        "info",
		DataContentType: "application/json",
		Data:            data,
	})
}

// routeExperiment applies the §10.7 ExperimentRouter to a session at
// creation. When the router fails closed on the isolation-monotonicity
// check it writes the §15.1 `422 VARIANT_ISOLATION_UNAVAILABLE`
// response and returns false; the caller must then abort. It returns
// true when the session may proceed to persistence.
func (s *Server) routeExperiment(w http.ResponseWriter, r *http.Request, row *sessionstore.Session) bool {
	err := s.ApplyExperimentRouting(r.Context(), row)
	if err == nil {
		return true
	}
	var ve *variantIsolationError
	if errors.As(err, &ve) {
		// §10.7 / §16.6: the fail-closed rejection emits the
		// experiment.isolation_mismatch event and increments the
		// rejection counter alongside the 422.
		if s.experimentReporter != nil {
			s.experimentReporter.ReportExperimentIsolationRejection(r.Context(), ExperimentIsolationRejection{
				TenantID:             row.TenantID,
				UserID:               row.UserID,
				ExperimentID:         ve.ExperimentID,
				VariantID:            ve.VariantID,
				SessionMinIsolation:  ve.SessionMinIsolation,
				VariantPoolIsolation: ve.VariantPoolIsolation,
			})
		}
		s.writeError(w, http.StatusUnprocessableEntity, "VARIANT_ISOLATION_UNAVAILABLE",
			"the assigned experiment variant pool's isolation profile is weaker than the session's minimum",
			map[string]any{
				"experimentId":         ve.ExperimentID,
				"variantId":            ve.VariantID,
				"sessionMinIsolation":  ve.SessionMinIsolation,
				"variantPoolIsolation": ve.VariantPoolIsolation,
			})
		return false
	}
	s.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
	return false
}

// findVariant locates the assigned variant within the candidate
// experiments.
func findVariant(candidates []experimentstore.Experiment, experimentID, variantID string) (experimentstore.Variant, bool) {
	for _, e := range candidates {
		if e.ID != experimentID {
			continue
		}
		for _, v := range e.Variants {
			if v.ID == variantID {
				return v, true
			}
		}
	}
	return experimentstore.Variant{}, false
}

// checkVariantIsolation enforces the §10.7 ExperimentRouter
// isolation-monotonicity rule. It is a no-op when the pool store is
// not wired or the variant pool is unresolvable.
func (s *Server) checkVariantIsolation(ctx context.Context, row *sessionstore.Session, a experiment.Assignment, v experimentstore.Variant) error {
	if s.pools == nil || v.Pool == "" || row.IsolationProfile == "" {
		return nil
	}
	pool, err := s.pools.Get(ctx, v.Pool)
	if err != nil || !isolation.IsValid(pool.IsolationProfile) {
		return nil
	}
	if isolation.AtLeastAsRestrictive(pool.IsolationProfile, row.IsolationProfile) {
		return nil
	}
	return &variantIsolationError{
		ExperimentID:         a.ExperimentID,
		VariantID:            a.VariantID,
		SessionMinIsolation:  string(row.IsolationProfile),
		VariantPoolIsolation: string(pool.IsolationProfile),
	}
}
