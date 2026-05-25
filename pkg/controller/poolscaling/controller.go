// SPDX-License-Identifier: MIT

// Package poolscaling holds the §4.6.2 PoolScalingController. The
// controller treats the admin API's Postgres pool definitions as the
// source of truth and reconciles them into the SandboxTemplate and
// SandboxWarmPool CRD pair the WarmPoolController consumes.
//
// This file holds the configuration sync: the PoolConfigSource
// abstraction over the pool definitions and the Sync pass that upserts
// each definition's CRD pair. The warm-pod floor is derived per pool
// by feeding the observed demand from a DemandSource and the pool's
// tuning inputs through the §4.6.2 scaling formula in the strategy
// subpackage; a pool with no observed demand stays at its bootstrap
// minWarm. Because the CRDs are derived state, Sync overwrites the
// full SandboxTemplate spec and the PoolScalingController-owned
// SandboxWarmPool spec fields (§4.6.3) on every pass, which also
// corrects any manual drift.
package poolscaling

import (
	"context"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1"
	"github.com/lennylabs/lenny/pkg/controller/poolscaling/strategy"
)

// failoverSeconds is the §4.6.2 worst-case controller failover window
// (leaseDuration 15s + renewDeadline 10s).
const failoverSeconds = 25.0

// defaultSafetyFactor and defaultPodWarmupSeconds are applied when a
// PoolConfig leaves the corresponding input unset. The safety factor
// matches the §4.6.2 agent-type Tier 1/2 default; the warmup baseline
// matches the pod-warm container-pull-plus-startup figure.
const (
	defaultSafetyFactor     = 1.5
	defaultPodWarmupSeconds = 10.0
)

// PoolConfig is the desired configuration of one pool, resolved from
// the admin API's Postgres source of truth (§4.6.2). The
// PoolScalingController reconciles it into a SandboxTemplate and a
// SandboxWarmPool that share the pool name.
type PoolConfig struct {
	// Name is the pool name. The SandboxTemplate and SandboxWarmPool
	// are both created under it.
	Name string
	// Namespace is the agent namespace the pool's CRDs live in.
	Namespace string
	// Template is the desired SandboxTemplate spec. Per §4.6.3 the
	// PoolScalingController owns the whole SandboxTemplate spec.
	Template lennyv1.SandboxTemplateSpec
	// MinWarm is the bootstrap warm-pod floor: the operator-set value a
	// pool uses until the §4.6.2 scaling formula has converged on
	// observed demand. MaxWarm is the warm-pod ceiling. Both are owned
	// by the PoolScalingController per §4.6.3.
	MinWarm int32
	MaxWarm int32
	// SafetyFactor scales the steady-state term of the §4.6.2 formula.
	// A non-positive value is treated as the agent-type Tier 1/2
	// default.
	SafetyFactor float64
	// ScalePolicy carries the scaling-formula tuning inputs and
	// time-of-day overrides (§4.6.3 PoolScalingController-owned).
	ScalePolicy *lennyv1.ScalePolicy
	// SDKWarmDisabled is the SDK-warm circuit-breaker flag (§4.6.3
	// PoolScalingController-owned).
	SDKWarmDisabled bool
	// PoolType selects which §4.6.2 formula sizes the pool: a standard
	// (non-experiment) pool or an A/B experiment variant pool (§10.7).
	// An empty value is treated as strategy.PoolStandard. A
	// PoolConfigSource resolves it from the experiment store via
	// ResolveVariantRoles.
	PoolType strategy.PoolType
	// VariantWeight is the traffic fraction routed to this variant pool,
	// in (0,1). It is meaningful only when PoolType is
	// strategy.PoolVariant and feeds the §4.6.2 variant-pool formula.
	VariantWeight float64
	// SumActiveVariantWeights is Σ variant_weights across every active
	// variant of every active experiment diverting traffic from this
	// standard base pool. It is 0 when no variants are active and feeds
	// the §4.6.2 base-pool adjustment (1 - Σ variant_weights).
	SumActiveVariantWeights float64
}

// Demand is the observed claim-rate signal for one pool — the input
// the §4.6.2 scaling formula consumes alongside the pool's static
// tuning.
type Demand struct {
	// BaseDemandP95 is the p95 claim arrival rate in claims per second.
	BaseDemandP95 float64
	// BurstP99Claims is the p99 claim arrival rate over the short burst
	// window, in claims per second.
	BurstP99Claims float64
	// Observed is true once the metrics window holds a usable sample.
	// While it is false the pool stays at its bootstrap minWarm.
	Observed bool
}

// DemandSource yields the observed demand signal for a pool. The
// production implementation reads the §16.1 claim-rate metrics from
// Prometheus. A PoolScalingController constructed without a
// DemandSource operates every pool in bootstrap mode.
type DemandSource interface {
	PoolDemand(ctx context.Context, poolName string) (Demand, error)
}

// PoolConfigSource yields the current set of pool definitions. The
// production implementation reads the admin API's Postgres tables; the
// controller treats whatever it returns as the desired state.
type PoolConfigSource interface {
	ListPoolConfigs(ctx context.Context) ([]PoolConfig, error)
}

// DemotionSignal is the rolling 5-minute SDK-warm demotion signal for
// one pool — the input the §6.1 circuit breaker consumes.
type DemotionSignal struct {
	// Rate is the rolling 5-minute demotion rate in [0,1]: SDK-warm
	// demotions divided by claims over the window.
	Rate float64
	// HasSample is true once the rolling window holds a usable sample.
	// A freshly-elected leader reports false until its in-memory window
	// has refilled; the breaker holds a tripped pool open in that case
	// rather than auto-closing on a cold-start zero rate.
	HasSample bool
}

// DemotionRateSource yields the rolling SDK-warm demotion signal for a
// pool (§6.1). The production implementation maintains the rolling
// 5-minute window in PoolScalingController memory, fed by the
// lenny_warmpool_sdk_demotions_total and lenny_warmpool_claims_total
// metrics. A PoolScalingController constructed without a
// DemotionRateSource never trips the SDK-warm circuit breaker; it
// still honors a breaker already persisted on the pool status, holding
// it open until its minOpenUntil grace window elapses.
type DemotionRateSource interface {
	PoolDemotionSignal(ctx context.Context, poolName string) (DemotionSignal, error)
}

// Reconciler is the §4.6.2 PoolScalingController. It syncs pool
// definitions from the PoolConfigSource into their CRD pair, deriving
// each pool's warm-pod floor from observed demand.
type Reconciler struct {
	// Client is the controller-runtime client.
	Client client.Client
	// Source supplies the desired pool definitions.
	Source PoolConfigSource
	// Demand supplies the observed per-pool demand that drives the
	// scaling formula. When nil, every pool stays at its bootstrap
	// minWarm.
	Demand DemandSource
	// Demotion supplies the rolling SDK-warm demotion signal that
	// drives the §6.1 circuit breaker. When nil, the controller never
	// trips the breaker but still honors a breaker already persisted on
	// a pool's status until its grace window elapses.
	Demotion DemotionRateSource
	// Strategy computes the warm-pod floor. When nil, the default
	// §4.6.2 formula is used.
	Strategy strategy.PoolScalingStrategy
	// Now returns the current time. It is a field so tests can pin the
	// clock the §6.1 circuit breaker compares against minOpenUntil.
	// When nil, time.Now is used.
	Now func() time.Time

	// AdmissionDeniedRetryCeiling is the §4.6.2 consecutive-rejection
	// count at which a pool is marked stuck. The §16.5
	// PoolScalingAdmissionStuck alert fires when StuckPools reports
	// the pool key for the alert's `for:` window. A non-positive value
	// uses DefaultAdmissionDeniedRetryCeiling.
	AdmissionDeniedRetryCeiling int

	// retryState is the lazily-constructed admission-retry tracker.
	// It is initialized on the first Sync to honor a Reconciler
	// constructed with the zero value.
	retryState *admissionRetryState
}

// StuckPools returns the list of <namespace>/<name> pool keys with at
// least one CRD tuple at or above the configured retry ceiling. A pool
// exits the list on its first clean Sync. The §16.5
// PoolScalingAdmissionStuck alert binds to the same key set.
func (r *Reconciler) StuckPools() []string {
	if r.retryState == nil {
		return nil
	}
	return r.retryState.stuckPools()
}

// ConsecutiveAdmissionDenials returns the highest consecutive-denial
// count across the pool's CRD tuples. Zero when no denial has been
// recorded or the retry tracker has not yet been initialized.
func (r *Reconciler) ConsecutiveAdmissionDenials(namespace, name string) int {
	if r.retryState == nil {
		return 0
	}
	return r.retryState.consecutiveDenials(namespace, name)
}

// ResumeReconciliation clears the in-memory admission-denial state for
// the named pool, implementing §4.6.2 item 3 condition (c): an
// operator POST /v1/admin/pools/{name}/resume-reconciliation resets
// the denial counter so a stuck pool is retried on the next tick
// without requiring a Postgres configuration change. It returns the
// number of CRD tuples cleared (0 when the pool was not stuck or no
// Sync has run yet). The namespace must match the agent namespace the
// pool's CRDs live in.
func (r *Reconciler) ResumeReconciliation(namespace, name string) int {
	if r.retryState == nil {
		return 0
	}
	return r.retryState.resumePool(namespace, name)
}

// AdminResumer adapts a Reconciler to the gateway admin
// ReconciliationResumer interface (§4.6.2 item 3 condition c). The
// admin API addresses pools by name; the PSC keys denial state by the
// agent namespace its CRDs live in, so the adapter binds that
// namespace. It satisfies the admin interface structurally without an
// import edge between the two packages.
type AdminResumer struct {
	// Reconciler is the running PoolScalingController whose in-memory
	// denial tracker is cleared.
	Reconciler *Reconciler
	// Namespace is the agent namespace the pool's CRDs live in.
	Namespace string
}

// ResumePoolReconciliation clears the named pool's admission-denial
// backoff and reports the number of CRD tuples cleared. It never
// errors: clearing in-memory state cannot fail.
func (a AdminResumer) ResumePoolReconciliation(ctx context.Context, poolName string) (int, error) {
	_ = ctx
	return a.Reconciler.ResumeReconciliation(a.Namespace, poolName), nil
}

// now returns the reconcile timestamp, honoring an injected clock.
func (r *Reconciler) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

// Sync performs one full reconciliation pass: every pool definition
// from the source is upserted into its SandboxTemplate and
// SandboxWarmPool CRD pair. A failure on one pool aborts the pass so
// the next tick retries; pools synced before the failure keep their
// applied state.
func (r *Reconciler) Sync(ctx context.Context) error {
	if r.retryState == nil {
		r.retryState = newAdmissionRetryState(r.AdmissionDeniedRetryCeiling)
	}
	configs, err := r.Source.ListPoolConfigs(ctx)
	if err != nil {
		return fmt.Errorf("list pool configs: %w", err)
	}
	now := r.now()
	for i := range configs {
		cfg := configs[i]
		// syncTemplate and syncWarmPool both call into
		// controllerutil.CreateOrUpdate; an admission webhook rejection
		// surfaces as a Forbidden / Invalid / BadRequest status error.
		// Each CRD tuple is gated and counted independently per §4.6.2:
		// a tuple in backoff (or stuck at the ceiling) is skipped this
		// tick, and an admission denial on one tuple never aborts the
		// pass or blocks another pool. A non-admission failure
		// (transport, Postgres, internal) aborts the pass so the next
		// tick retries, matching the SSA conflict retry policy.
		if err := r.syncTuple(ctx, cfg, crdSandboxTmpl, now, r.syncTemplate); err != nil {
			return err
		}
		if err := r.syncTuple(ctx, cfg, crdSandboxPool, now, r.syncWarmPool); err != nil {
			return err
		}
	}
	return nil
}

// syncTuple applies one CRD tuple for a pool through the §4.6.2
// admission-denial backoff gate. A tuple in backoff or marked stuck is
// skipped without an apply. The apply outcome is recorded so an
// admission denial extends the backoff and a clean apply clears it. An
// admission denial is swallowed (the next tick retries after backoff);
// a non-admission error is returned so the pass aborts.
func (r *Reconciler) syncTuple(ctx context.Context, cfg PoolConfig, crd string, now time.Time, apply func(context.Context, PoolConfig) error) error {
	key := denialKey{namespace: cfg.Namespace, pool: cfg.Name, crd: crd}
	if !r.retryState.readyToSync(key, now) {
		return nil
	}
	err := apply(ctx, cfg)
	r.retryState.recordOutcome(key, err, now)
	if err == nil {
		return nil
	}
	if isAdmissionRejection(err) {
		return nil
	}
	return fmt.Errorf("sync %s %s/%s: %w", crd, cfg.Namespace, cfg.Name, err)
}

// syncTemplate upserts the pool's SandboxTemplate. The whole spec is
// PoolScalingController-owned, so it is replaced wholesale. The §5.2
// topology-spread defaults are stamped in when the pool definition
// carries none.
func (r *Reconciler) syncTemplate(ctx context.Context, cfg PoolConfig) error {
	spec := cfg.Template
	// spec: §5.2 lines 631-636 — the PoolScalingController owns
	// SandboxTemplate.spec and writes the soft zone/node spread defaults
	// when the deployer has not overridden them per pool.
	if len(spec.TopologySpreadConstraints) == 0 {
		spec.TopologySpreadConstraints = defaultTopologySpreadConstraints(cfg.Name)
	}
	tmpl := &lennyv1.SandboxTemplate{
		ObjectMeta: metav1.ObjectMeta{Name: cfg.Name, Namespace: cfg.Namespace},
	}
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, tmpl, func() error {
		tmpl.Spec = spec
		return nil
	})
	return err
}

// topology spread keys the §5.2 defaults distribute pods over. The
// label selector matches the pool's managed agent pods (the
// WarmPoolController stamps lenny.dev/pool onto every Sandbox, and the
// podspec builder copies it onto the pod) so the skew is computed
// within the pool rather than across unrelated pods.
const (
	topologyKeyZone = "topology.kubernetes.io/zone"
	topologyKeyNode = "kubernetes.io/hostname"
	poolLabelKey    = "lenny.dev/pool"
)

// defaultTopologySpreadConstraints returns the §5.2 lines 633-634 soft
// spread defaults: maxSkew 1 across zones and across nodes, both with
// whenUnsatisfiable ScheduleAnyway so scheduling never blocks on an
// unsatisfiable spread. The selector scopes the skew to the named
// pool's pods.
func defaultTopologySpreadConstraints(poolName string) []corev1.TopologySpreadConstraint {
	selector := &metav1.LabelSelector{MatchLabels: map[string]string{poolLabelKey: poolName}}
	return []corev1.TopologySpreadConstraint{
		{
			MaxSkew:           1,
			TopologyKey:       topologyKeyZone,
			WhenUnsatisfiable: corev1.ScheduleAnyway,
			LabelSelector:     selector,
		},
		{
			MaxSkew:           1,
			TopologyKey:       topologyKeyNode,
			WhenUnsatisfiable: corev1.ScheduleAnyway,
			LabelSelector:     selector.DeepCopy(),
		},
	}
}

// syncWarmPool upserts the pool's SandboxWarmPool, writing the spec
// fields §4.6.3 assigns to the PoolScalingController and, separately,
// the §4.6.3 status.sdkWarmCircuitBreaker carve-out. minWarm is the
// formula-derived floor; the WarmPoolController-owned status counts
// are not touched.
//
// spec.sdkWarmDisabled is the §6.1 SDK-warm circuit-breaker flag. The
// breaker decision is taken against the rolling demotion signal and
// the breaker state already persisted on the pool. The PoolConfig's
// own SDKWarmDisabled stays authoritative when no DemotionRateSource
// is wired and no breaker is currently persisted, so an operator
// override via the admin API still takes effect.
func (r *Reconciler) syncWarmPool(ctx context.Context, cfg PoolConfig) error {
	minWarm, err := r.targetMinWarm(ctx, cfg)
	if err != nil {
		return err
	}

	pool := &lennyv1.SandboxWarmPool{
		ObjectMeta: metav1.ObjectMeta{Name: cfg.Name, Namespace: cfg.Namespace},
	}

	// The breaker decision is computed against the breaker state read
	// back from the live object inside the mutate closure, so the
	// decision always reflects what is persisted right now. A nil
	// decision pointer means no breaker evaluation ran for this pool.
	var decision *BreakerDecision
	_, err = controllerutil.CreateOrUpdate(ctx, r.Client, pool, func() error {
		decision, err = r.evaluateBreaker(ctx, cfg, pool)
		if err != nil {
			return err
		}
		pool.Spec.TemplateRef = cfg.Name
		pool.Spec.MinWarm = minWarm
		pool.Spec.MaxWarm = cfg.MaxWarm
		pool.Spec.ScalePolicy = cfg.ScalePolicy
		if decision != nil {
			pool.Spec.SDKWarmDisabled = decision.SDKWarmDisabled
		} else {
			pool.Spec.SDKWarmDisabled = cfg.SDKWarmDisabled
		}
		return nil
	})
	if err != nil {
		return err
	}

	// The circuit-breaker state is a status subresource carve-out
	// (§4.6.3), so it is written separately from the spec apply above.
	if decision != nil {
		if err := r.syncBreakerStatus(ctx, pool, decision.State); err != nil {
			return err
		}
	}
	return nil
}

// evaluateBreaker runs the §6.1 SDK-warm circuit-breaker decision for
// one pool. It reads the rolling demotion signal from the
// DemotionRateSource and the persisted breaker state from the live
// pool, then returns the decision the caller writes to
// spec.sdkWarmDisabled and status.sdkWarmCircuitBreaker.
//
// It returns a nil decision when no breaker evaluation applies: when
// no DemotionRateSource is configured and the pool has no breaker
// currently persisted. In that case the pool's PoolConfig value stays
// authoritative, preserving the admin-API operator override path. When
// a breaker IS persisted, the decision runs even without a
// DemotionRateSource so the breaker is held open until its grace
// window elapses (the §6.1 leader-failover guard).
func (r *Reconciler) evaluateBreaker(ctx context.Context, cfg PoolConfig, pool *lennyv1.SandboxWarmPool) (*BreakerDecision, error) {
	cur := breakerStateFromStatus(pool.Status.SDKWarmCircuitBreaker)
	if r.Demotion == nil && !cur.Open {
		return nil, nil
	}

	in := BreakerInputs{
		Current:         cur,
		MinOpenDuration: scalePolicyMinOpenDuration(cfg.ScalePolicy),
		Now:             r.now(),
	}
	if r.Demotion != nil {
		sig, err := r.Demotion.PoolDemotionSignal(ctx, cfg.Name)
		if err != nil {
			return nil, fmt.Errorf("read demotion signal: %w", err)
		}
		in.DemotionRate = sig.Rate
		in.HasWindowSample = sig.HasSample
	}

	decision := EvaluateBreaker(in)
	return &decision, nil
}

// syncBreakerStatus writes the §6.1 circuit-breaker state to the
// SandboxWarmPool status.sdkWarmCircuitBreaker carve-out. The write is
// skipped when the persisted state already matches the decision, so a
// steady-state reconcile does not churn the status subresource.
func (r *Reconciler) syncBreakerStatus(ctx context.Context, pool *lennyv1.SandboxWarmPool, state BreakerState) error {
	want := breakerStatusFromState(state)
	if breakerStatusEqual(pool.Status.SDKWarmCircuitBreaker, want) {
		return nil
	}
	pool.Status.SDKWarmCircuitBreaker = want
	if err := r.Client.Status().Update(ctx, pool); err != nil {
		return fmt.Errorf("update circuit-breaker status: %w", err)
	}
	return nil
}

// breakerStatusEqual reports whether two persisted circuit-breaker
// statuses carry the same open/closed decision and timestamps.
func breakerStatusEqual(a, b *lennyv1.SDKWarmCircuitBreakerStatus) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	if a.OpenedReason != b.OpenedReason {
		return false
	}
	return timePtrEqual(a.OpenedAt, b.OpenedAt) && timePtrEqual(a.MinOpenUntil, b.MinOpenUntil)
}

// timePtrEqual reports whether two optional metav1.Time values are
// equal, treating two nils as equal.
func timePtrEqual(a, b *metav1.Time) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return a.Time.Equal(b.Time)
}

// targetMinWarm derives the warm-pod floor for one pool by running the
// §4.6.2 scaling formula. A pool with no DemandSource, or one whose
// demand has not yet converged, stays at its bootstrap minWarm.
func (r *Reconciler) targetMinWarm(ctx context.Context, cfg PoolConfig) (int32, error) {
	// A pool inside its §4.6.1 scale-to-zero window targets zero warm
	// pods regardless of observed demand. The window is evaluated before
	// the scaling formula so an off-hours pool short-circuits to zero.
	if cfg.ScalePolicy != nil && cfg.ScalePolicy.ScaleToZero != nil {
		active, err := scaleToZeroActive(cfg.ScalePolicy.ScaleToZero, r.now())
		if err != nil {
			return 0, fmt.Errorf("evaluate scaleToZero for pool %q: %w", cfg.Name, err)
		}
		if active {
			return 0, nil
		}
	}

	in := strategy.ScalingInputs{
		PoolType:                cfg.PoolType,
		Mode:                    strategy.ExecutionMode(cfg.Template.ExecutionMode),
		SafetyFactor:            cfg.SafetyFactor,
		FailoverSeconds:         failoverSeconds,
		PodWarmupSeconds:        podWarmupSeconds(cfg),
		VariantWeight:           cfg.VariantWeight,
		SumActiveVariantWeights: cfg.SumActiveVariantWeights,
		BootstrapMinWarm:        int(cfg.MinWarm),
	}
	// An unset PoolType defaults to a standard non-experiment pool; the
	// strategy rejects an empty PoolType, so the default is resolved here.
	if in.PoolType == "" {
		in.PoolType = strategy.PoolStandard
	}
	if in.SafetyFactor <= 0 {
		in.SafetyFactor = defaultSafetyFactor
	}
	if r.Demand != nil {
		d, err := r.Demand.PoolDemand(ctx, cfg.Name)
		if err != nil {
			return 0, fmt.Errorf("read demand: %w", err)
		}
		in.BaseDemandP95 = d.BaseDemandP95
		in.BurstP99Claims = d.BurstP99Claims
		in.HasObservedDemand = d.Observed
	}

	strat := r.Strategy
	if strat == nil {
		strat = strategy.New()
	}
	decision, err := strat.Compute(in)
	if err != nil {
		return 0, fmt.Errorf("compute minWarm: %w", err)
	}
	return int32(decision.MinWarm), nil
}

// podWarmupSeconds resolves the pod creation-to-ready time the scaling
// formula uses, from the pool's scalePolicy, defaulting to the
// pod-warm baseline.
func podWarmupSeconds(cfg PoolConfig) float64 {
	if cfg.ScalePolicy != nil && cfg.ScalePolicy.PodWarmupSecondsBaseline > 0 {
		return float64(cfg.ScalePolicy.PodWarmupSecondsBaseline)
	}
	return defaultPodWarmupSeconds
}
