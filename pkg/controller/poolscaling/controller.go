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
	"strconv"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
	"github.com/lennylabs/lenny/pkg/controller/poolscaling/convergence"
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
	// MinWarm is the warm-pod floor and MaxWarm the warm-pod ceiling,
	// both owned by the PoolScalingController per §4.6.3. MinWarm is the
	// static fallback the §4.6.2 formula returns until observed demand
	// converges.
	MinWarm int32
	MaxWarm int32

	// BootstrapMinWarm is the §17.8.2 cold-start bootstrap override. When
	// non-nil the pool is bootstrap-eligible: the controller pins minWarm
	// to this value (status.scalingMode: bootstrap) until the §17.8.2
	// step-4 convergence criteria are met, then switches to the formula
	// (status.scalingMode: formula). A nil pointer means no override is
	// in force and the pool is formula-driven with no bootstrap-mode
	// signal. The PoolStoreSource populates it from the pool's
	// bootstrap_min_warm column. spec: §17.8.2 "Cold-start bootstrap
	// procedure".
	BootstrapMinWarm *int

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

	// ForceZeroMinWarm pins the pool's minWarm to 0 regardless of
	// observed demand. The §10.7 ExperimentVariantSource sets it for a
	// paused experiment's variant pool: minWarm drops to 0 so no new warm
	// pods are created, while MaxWarm is intentionally left at the pool's
	// current ceiling so existing warm pods drain naturally rather than
	// being pre-terminated and the SandboxWarmPool CRD is retained for
	// re-activation. spec: §10.7 line 1102 (active → paused).
	ForceZeroMinWarm bool

	// DrainAndDelete marks a concluded experiment's variant pool for the
	// §10.7 line 1104 full-drain-then-delete lifecycle: the
	// SandboxWarmPool spec is driven to minWarm=0/maxWarm=0 to trigger a
	// full drain, and once status.readyCount reaches 0 the
	// PoolScalingController deletes the SandboxWarmPool CRD. The
	// SandboxTemplate is retained (it may be referenced by other
	// experiments or kept for audit). spec: §10.7 line 1104
	// (active|paused → concluded).
	DrainAndDelete bool

	// Generation is the §4.6.2 pool_config_generation the admin API
	// bumped on the last write to this pool. The reconciler stamps it
	// onto the SandboxTemplate and SandboxWarmPool CRDs as the
	// `lenny.dev/config-generation` annotation so the gateway-side
	// PoolConfigDrift check can compare Postgres- and CRD-side
	// generations. spec: spec/04_system-components.md lines 557-560.
	Generation int64
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
	// HoursOfData is how many hours of traffic data the source has
	// accumulated for the pool. The §17.8.2 step-4 convergence check
	// reads it as the 48-hour gate; a source that does not track the
	// window leaves it zero, which keeps a bootstrap-override pool in
	// bootstrap mode until the operator clears the override manually.
	HoursOfData float64
}

// DemandSource yields the observed demand signal for a pool. The
// production implementation reads the §16.1 claim-rate metrics from
// Prometheus. A PoolScalingController constructed without a
// DemandSource operates every pool in bootstrap mode.
type DemandSource interface {
	PoolDemand(ctx context.Context, poolName string) (Demand, error)
}

// WarmPoolLowSource reports whether a §16.5 WarmPoolLow alert has fired
// for a pool inside a trailing recency window. The §17.8.2 step-4
// convergence criteria require no WarmPoolLow in the last 6 hours before
// a bootstrapping pool may switch to formula-driven scaling. The
// production implementation reads the alert state from the §16.5 alert
// manager or the firing-alert metric. A PoolScalingController
// constructed without a WarmPoolLowSource treats the criterion as
// satisfied (no recent low), so convergence then rests on the data,
// stability, and provisioning criteria alone.
//
// spec: §17.8.2 step 4 — "No WarmPoolLow alert has fired in the last 6
// hours."
type WarmPoolLowSource interface {
	WarmPoolLowFiredSince(ctx context.Context, poolName string, since time.Time) (bool, error)
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

// ModeFactorSource yields the observed-reuse multiplier the §5.2 line
// 569 mode-adjusted scaling formula consumes as `mode_factor` for
// task-mode pools. The production implementation reads the
// `lenny_task_reuse_count` histogram's median over a rolling 100-task
// convergence window (§5.2 line 549). `ok=false` means the histogram
// has not converged yet — the PoolScalingController falls back to the
// pool's `maxTasksPerPod` (preConnect=false pools) or 1.0
// (preConnect=true pools that should wait for observed reuse).
//
// spec: §5.2 lines 549, 569.
type ModeFactorSource interface {
	PoolTaskReuseMedian(ctx context.Context, poolName string) (median float64, ok bool, err error)
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
	// ModeFactors supplies the §5.2 line 569 observed-reuse signal that
	// drives the mode-adjusted formula's `mode_factor` for task-mode
	// pools. When nil, the controller derives `mode_factor` from the
	// pool's static maxTasksPerPod fallback per the §5.2 task-mode
	// formula. spec: §5.2 lines 549, 569.
	ModeFactors ModeFactorSource
	// Strategy computes the warm-pod floor. When nil, the default
	// §4.6.2 formula is used.
	Strategy strategy.PoolScalingStrategy
	// WarmPoolLow reports recent §16.5 WarmPoolLow alert activity that
	// the §17.8.2 step-4 convergence criteria gate on. When nil, the
	// controller treats the criterion as satisfied. spec: §17.8.2 step
	// 4.
	WarmPoolLow WarmPoolLowSource
	// BootstrapStabilityWindow overrides the §17.8.2 step-4
	// formula-target stability window the convergence tracker enforces.
	// A non-positive value uses convergence.DefaultStabilityWindow (2h).
	// It is a field so tests can drive convergence without two hours of
	// samples.
	BootstrapStabilityWindow time.Duration
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

	// DefaultSafetyFactor is the §17.8.2 per-tier agent-type safety_factor
	// applied to a pool whose PoolConfig leaves SafetyFactor unset. The
	// binary resolves it from poolScaling.safetyFactor when set, otherwise
	// from DefaultSafetyFactorForTier(capacityPlanning.tier) so a Tier 3
	// deployment automatically picks up the 1.2 override. A non-positive
	// value falls back to the agent-type Tier 1/2 default (1.5). spec:
	// spec/17_deployment-topology.md line 1008.
	DefaultSafetyFactor float64

	// retryState is the lazily-constructed admission-retry tracker.
	// It is initialized on the first Sync to honor a Reconciler
	// constructed with the zero value.
	retryState *admissionRetryState

	// lagMeter publishes the §4.6.2 line 557
	// lenny_pool_config_reconciliation_lag_seconds gauge. It is
	// lazily constructed on the first Sync.
	lagMeter *reconciliationLagMeter

	// warmupMeter publishes the §16.5 line 488
	// lenny_pool_warmup_seconds_baseline gauge that the
	// WarmPoolReplenishmentSlow alert reads as its per-pool 2×
	// threshold source. It is lazily constructed on the first Sync.
	warmupMeter *warmupBaselineMeter

	// bootstrapMeter publishes the §17.8.2 cold-start gauges
	// (lenny_pool_bootstrap_mode, lenny_pool_bootstrap_min_warm_override,
	// lenny_pool_bootstrap_target_min_warm) the §16.5 PoolBootstrapMode
	// and PoolBootstrapUnderprovisioned alerts read. Lazily constructed
	// on the first Sync.
	bootstrapMeter *bootstrapModeMeter

	// bootstrapTracker maintains the per-pool formula-target sample
	// history the §17.8.2 step-4 stability criterion reads. Lazily
	// constructed on the first Sync.
	bootstrapTracker *convergence.Tracker
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
	if r.lagMeter == nil {
		r.lagMeter = newReconciliationLagMeter(r.Now)
	}
	if r.warmupMeter == nil {
		r.warmupMeter = newWarmupBaselineMeter()
	}
	if r.bootstrapMeter == nil {
		r.bootstrapMeter = newBootstrapModeMeter()
	}
	if r.bootstrapTracker == nil {
		r.bootstrapTracker = convergence.NewTrackerWithWindow(
			r.BootstrapStabilityWindow, convergence.DefaultMaxCoefficientOfVariation)
	}
	configs, err := r.Source.ListPoolConfigs(ctx)
	if err != nil {
		return fmt.Errorf("list pool configs: %w", err)
	}
	now := r.now()
	// spec: §4.6.2 line 557 — refresh the lag gauge for every tracked
	// pool so it advances even on a quiet tick.
	r.lagMeter.Sample()
	desired := make(map[string]struct{}, len(configs))
	for i := range configs {
		cfg := configs[i]
		desired[cfg.Name] = struct{}{}
		// syncTemplate and syncWarmPool both call into
		// controllerutil.CreateOrUpdate; an admission webhook rejection
		// surfaces as a Forbidden / Invalid / BadRequest status error.
		// Each CRD tuple is gated and counted independently per §4.6.2:
		// a tuple in backoff (or stuck at the ceiling) is skipped this
		// tick, and an admission denial on one tuple never aborts the
		// pass or blocks another pool. A non-admission failure
		// (transport, Postgres, internal) aborts the pass so the next
		// tick retries, matching the SSA conflict retry policy.
		tmplErr := r.syncTuple(ctx, cfg, crdSandboxTmpl, now, r.syncTemplate)
		if tmplErr != nil {
			return tmplErr
		}
		// spec: §10.7 line 1104 — a concluded experiment's variant pool is
		// drained to 0/0 and its SandboxWarmPool deleted once readyCount
		// reaches 0. The SandboxTemplate above is retained. Every other
		// pool reconciles its SandboxWarmPool through the normal upsert.
		warmApply := r.syncWarmPool
		if cfg.DrainAndDelete {
			warmApply = r.drainAndDeleteWarmPool
		}
		warmErr := r.syncTuple(ctx, cfg, crdSandboxPool, now, warmApply)
		if warmErr != nil {
			return warmErr
		}
		// spec: §4.6.2 line 557 — both tuples synced cleanly, reset the
		// lag gauge to zero.
		r.lagMeter.MarkSynced(cfg.Name)
		// spec: §16.5 line 488 — mirror the pool's warm-up baseline so
		// WarmPoolReplenishmentSlow fires at 2× the per-pool value.
		r.warmupMeter.Set(cfg.Name, warmupBaselineForAlert(cfg))
	}
	// Pools removed from the Postgres source no longer participate in
	// the drift check; clear their series so the §16.5 alerts
	// auto-resolve.
	r.lagMeter.forgetNotIn(desired)
	r.warmupMeter.forgetNotIn(desired)
	r.bootstrapMeter.forgetNotIn(desired)
	r.bootstrapTracker.ForgetNotIn(desired)
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
		// spec: §4.6.2 line 558 — stamp pool_config_generation onto the
		// CRD so the gateway-side PoolConfigDrift check can compare it
		// to Postgres.
		setConfigGenerationAnnotation(&tmpl.ObjectMeta, cfg.Generation)
		return nil
	})
	return err
}

// configGenerationAnnotation is the §4.6.2 line 558 annotation key the
// PoolScalingController stamps on every Sandbox CRD it reconciles. The
// gateway-side PoolConfigDrift check reads it and compares to the
// Postgres pool_config_generation; a mismatch over the drift window
// fires the PoolConfigDrift alert.
const configGenerationAnnotation = "lenny.dev/config-generation"

// setConfigGenerationAnnotation writes the §4.6.2 pool_config_generation
// onto the resource. A zero generation leaves the annotation untouched
// (the spec implies a real generation count starts at 1).
func setConfigGenerationAnnotation(meta *metav1.ObjectMeta, generation int64) {
	if generation <= 0 {
		return
	}
	if meta.Annotations == nil {
		meta.Annotations = map[string]string{}
	}
	meta.Annotations[configGenerationAnnotation] = strconv.FormatInt(generation, 10)
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
	wd, err := r.resolveWarm(ctx, cfg)
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
		pool.Spec.MinWarm = wd.MinWarm
		pool.Spec.MaxWarm = cfg.MaxWarm
		pool.Spec.ScalePolicy = cfg.ScalePolicy
		if decision != nil {
			pool.Spec.SDKWarmDisabled = decision.SDKWarmDisabled
		} else {
			pool.Spec.SDKWarmDisabled = cfg.SDKWarmDisabled
		}
		// spec: §4.6.2 line 558 — same generation stamp on the
		// SandboxWarmPool so a partial sync (template-only) still
		// exposes the mismatch.
		setConfigGenerationAnnotation(&pool.ObjectMeta, cfg.Generation)
		return nil
	})
	if err != nil {
		return err
	}

	// The §6.1 circuit-breaker state and the §17.8.2 scaling mode are
	// both status subresource carve-outs (§4.6.3), written together
	// after the spec apply.
	if err := r.syncPoolStatus(ctx, pool, decision, wd.ScalingMode, wd.HoursOfData); err != nil {
		return err
	}
	// spec: §17.8.2 steps 4, 5 — publish the cold-start gauges the
	// PoolBootstrapMode / PoolBootstrapUnderprovisioned alerts read.
	r.bootstrapMeter.Set(cfg.Name, wd.Sample)
	return nil
}

// drainAndDeleteWarmPool implements the §10.7 line 1104 conclude
// lifecycle for a variant pool: it drives the SandboxWarmPool to a full
// drain (minWarm=0, maxWarm=0) and, once status.readyCount has reached
// 0, deletes the SandboxWarmPool CRD. The SandboxTemplate is left in
// place by the caller. A SandboxWarmPool that is already gone is a
// no-op, so a concluded experiment that lingers in the registry does
// not churn after its pool has been reclaimed.
func (r *Reconciler) drainAndDeleteWarmPool(ctx context.Context, cfg PoolConfig) error {
	pool := &lennyv1.SandboxWarmPool{
		ObjectMeta: metav1.ObjectMeta{Name: cfg.Name, Namespace: cfg.Namespace},
	}
	if err := r.Client.Get(ctx, client.ObjectKeyFromObject(pool), pool); err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("get warm pool for drain-delete: %w", err)
	}
	// spec: §10.7 line 1104 — delete only once the pool has fully
	// drained (readyCount == 0). Until then, hold the spec at 0/0 so the
	// WarmPoolController keeps draining and never replenishes.
	if pool.Status.ReadyCount <= 0 {
		if err := r.Client.Delete(ctx, pool); err != nil && !apierrors.IsNotFound(err) {
			return fmt.Errorf("delete drained warm pool: %w", err)
		}
		return nil
	}
	if pool.Spec.MinWarm == 0 && pool.Spec.MaxWarm == 0 {
		// Already draining; nothing to rewrite this pass.
		return nil
	}
	pool.Spec.MinWarm = 0
	pool.Spec.MaxWarm = 0
	setConfigGenerationAnnotation(&pool.ObjectMeta, cfg.Generation)
	if err := r.Client.Update(ctx, pool); err != nil {
		return fmt.Errorf("drain warm pool: %w", err)
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

// syncPoolStatus writes the §6.1 circuit-breaker state and the §17.8.2
// scaling mode to the SandboxWarmPool status subresource in a single
// update. Both are PoolScalingController-owned carve-outs (§4.6.3). A
// nil breaker decision leaves the breaker status untouched (no breaker
// evaluation ran). The update is skipped when neither field changed so a
// steady-state reconcile does not churn the status subresource.
func (r *Reconciler) syncPoolStatus(ctx context.Context, pool *lennyv1.SandboxWarmPool, decision *BreakerDecision, scalingMode string, hoursOfData int32) error {
	changed := false
	if decision != nil {
		want := breakerStatusFromState(decision.State)
		if !breakerStatusEqual(pool.Status.SDKWarmCircuitBreaker, want) {
			pool.Status.SDKWarmCircuitBreaker = want
			changed = true
		}
	}
	if scalingMode != "" && pool.Status.ScalingMode != scalingMode {
		pool.Status.ScalingMode = scalingMode
		changed = true
	}
	// spec: §17.8.2 step 3 — surface accumulated traffic hours while an
	// override is active so the admin GET can report hoursOfData; clear
	// it once the pool converges or has no override.
	if pool.Status.BootstrapHoursOfData != hoursOfData {
		pool.Status.BootstrapHoursOfData = hoursOfData
		changed = true
	}
	if !changed {
		return nil
	}
	if err := r.Client.Status().Update(ctx, pool); err != nil {
		return fmt.Errorf("update pool status: %w", err)
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

// scalingMode names the §17.8.2 cold-start scaling mode the
// PoolScalingController writes to status.scalingMode.
const (
	scalingModeBootstrap = "bootstrap"
	scalingModeFormula   = "formula"
)

// warmPoolLowRecencyWindow is the §17.8.2 step-4 trailing window over
// which a WarmPoolLow alert blocks convergence ("the last 6 hours").
const warmPoolLowRecencyWindow = 6 * time.Hour

// warmDecision is the per-pool outcome of resolveWarm: the warm-pod
// floor written to the SandboxWarmPool spec, the §17.8.2 scaling mode
// written to status, and the cold-start gauge snapshot the bootstrap
// meter publishes.
type warmDecision struct {
	MinWarm     int32
	ScalingMode string
	Sample      bootstrapSample
	// HoursOfData is the whole hours of accumulated traffic data, written
	// to status.bootstrapHoursOfData while the pool operates under an
	// override so the admin GET can surface bootstrapStatus.hoursOfData.
	// Zero when no override is in force.
	HoursOfData int32
}

// resolveWarm derives one pool's warm-pod floor and §17.8.2 cold-start
// scaling mode. A paused or scale-to-zero pool short-circuits to zero
// warm pods in formula mode. A pool without a bootstrapMinWarm override
// is formula-driven (today's behavior) with no bootstrap-mode signal. A
// pool with an override stays pinned to it (status.scalingMode:
// bootstrap) until every §17.8.2 step-4 convergence criterion is met,
// then switches to the formula target (status.scalingMode: formula).
//
// spec: §4.6.2 scaling formula; §17.8.2 "Cold-start bootstrap
// procedure" steps 1-4.
func (r *Reconciler) resolveWarm(ctx context.Context, cfg PoolConfig) (warmDecision, error) {
	now := r.now()
	// spec: §10.7 line 1102 — a paused experiment's variant pool pins
	// minWarm to 0 regardless of any observed demand, short-circuiting
	// the scaling formula so no new warm pods are created while paused.
	if cfg.ForceZeroMinWarm {
		return warmDecision{MinWarm: 0, ScalingMode: scalingModeFormula}, nil
	}
	// A pool inside its §4.6.1 scale-to-zero window targets zero warm
	// pods regardless of observed demand. The window is evaluated before
	// the scaling formula so an off-hours pool short-circuits to zero.
	if cfg.ScalePolicy != nil && cfg.ScalePolicy.ScaleToZero != nil {
		active, err := scaleToZeroActive(cfg.ScalePolicy.ScaleToZero, now)
		if err != nil {
			return warmDecision{}, fmt.Errorf("evaluate scaleToZero for pool %q: %w", cfg.Name, err)
		}
		if active {
			return warmDecision{MinWarm: 0, ScalingMode: scalingModeFormula}, nil
		}
	}

	// spec: §17.8.2 — the bootstrapMinWarm override makes the pool
	// bootstrap-eligible and is the static floor while bootstrap mode is
	// active. A pool without one falls back to its configured WarmCount
	// as the cold-start floor (the prior behavior).
	hasOverride := cfg.BootstrapMinWarm != nil
	bootstrapFloor := int(cfg.MinWarm)
	if hasOverride {
		bootstrapFloor = *cfg.BootstrapMinWarm
	}

	in := strategy.ScalingInputs{
		PoolType:                cfg.PoolType,
		Mode:                    strategy.ExecutionMode(cfg.Template.ExecutionMode),
		SafetyFactor:            cfg.SafetyFactor,
		FailoverSeconds:         failoverSeconds,
		PodWarmupSeconds:        podWarmupSeconds(cfg),
		VariantWeight:           cfg.VariantWeight,
		SumActiveVariantWeights: cfg.SumActiveVariantWeights,
		BootstrapMinWarm:        bootstrapFloor,
	}
	// spec: §5.2 line 549 — resolve `mode_factor` for the pool. Task and
	// concurrent modes have a static fallback from the CRD spec
	// (maxTasksPerPod, maxConcurrent); preConnect task pools that have
	// converged on a task-reuse median override the static fallback.
	mf, bf := resolveModeFactors(ctx, cfg, r.ModeFactors)
	in.ModeFactor = mf
	in.BurstModeFactor = bf
	// An unset PoolType defaults to a standard non-experiment pool; the
	// strategy rejects an empty PoolType, so the default is resolved here.
	if in.PoolType == "" {
		in.PoolType = strategy.PoolStandard
	}
	// spec: §17.8.2 line 1008 — a pool that does not pin SafetyFactor
	// inherits the deployment's tier-resolved default. r.DefaultSafetyFactor
	// carries poolScaling.safetyFactor (or DefaultSafetyFactorForTier of the
	// configured tier); the const fallback keeps a zero-value Reconciler at
	// the agent-type Tier 1/2 default.
	if in.SafetyFactor <= 0 {
		in.SafetyFactor = r.DefaultSafetyFactor
		if in.SafetyFactor <= 0 {
			in.SafetyFactor = defaultSafetyFactor
		}
	}
	var hoursOfData float64
	if r.Demand != nil {
		d, err := r.Demand.PoolDemand(ctx, cfg.Name)
		if err != nil {
			return warmDecision{}, fmt.Errorf("read demand: %w", err)
		}
		in.BaseDemandP95 = d.BaseDemandP95
		in.BurstP99Claims = d.BurstP99Claims
		in.HasObservedDemand = d.Observed
		hoursOfData = d.HoursOfData
	}

	strat := r.Strategy
	if strat == nil {
		strat = strategy.New()
	}
	decision, err := strat.Compute(in)
	if err != nil {
		return warmDecision{}, fmt.Errorf("compute minWarm: %w", err)
	}

	// The formula target only exists once demand is observed; before
	// then Compute returns the bootstrap fallback, not a demand-driven
	// target.
	hasFormula := in.HasObservedDemand
	formulaTarget := 0
	if hasFormula {
		formulaTarget = decision.MinWarm
	}
	// Feed the §17.8.2 step-4 stability tracker so the variance criterion
	// accrues history. A zero target (no demand) resets the window.
	r.bootstrapTracker.Observe(cfg.Name, formulaTarget, now)

	// spec: §17.8.2 — bootstrap mode applies only while spec.bootstrapMinWarm
	// is set. A pool without an override is formula-driven with no
	// bootstrap-mode signal, preserving the prior behavior.
	if !hasOverride {
		return warmDecision{
			MinWarm:     int32(decision.MinWarm),
			ScalingMode: scalingModeFormula,
		}, nil
	}

	cvg := convergence.Inputs{
		HasObservedDemand: in.HasObservedDemand,
		HoursOfData:       hoursOfData,
		TargetStable:      r.bootstrapTracker.Stable(cfg.Name, now),
		WarmPoolLowRecent: r.warmPoolLowRecent(ctx, cfg.Name, now),
		FormulaTarget:     formulaTarget,
		OverrideMinWarm:   *cfg.BootstrapMinWarm,
	}
	sample := bootstrapSample{
		HasOverride:     true,
		OverrideMinWarm: *cfg.BootstrapMinWarm,
		HasFormula:      hasFormula,
		FormulaTarget:   formulaTarget,
	}
	hours := int32(hoursOfData)
	if hours < 0 {
		hours = 0
	}
	if cvg.Converged() {
		// spec: §17.8.2 step 4 — every criterion met, switch to
		// formula-driven scaling and clear the bootstrap-mode signal.
		return warmDecision{
			MinWarm:     int32(formulaTarget),
			ScalingMode: scalingModeFormula,
			Sample:      sample,
		}, nil
	}
	// spec: §17.8.2 step 1 — the static override takes precedence over
	// the formula output while bootstrap mode is active.
	sample.BootstrapActive = true
	return warmDecision{
		MinWarm:     int32(*cfg.BootstrapMinWarm),
		ScalingMode: scalingModeBootstrap,
		Sample:      sample,
		HoursOfData: hours,
	}, nil
}

// warmPoolLowRecent reports the §17.8.2 step-4 WarmPoolLow criterion: a
// WarmPoolLow alert that fired for the pool inside the trailing 6-hour
// window blocks convergence. With no WarmPoolLowSource wired the
// criterion is treated as satisfied; a source error is treated
// conservatively as a recent low so the controller does not converge on
// an unverifiable signal.
func (r *Reconciler) warmPoolLowRecent(ctx context.Context, pool string, now time.Time) bool {
	if r.WarmPoolLow == nil {
		return false
	}
	fired, err := r.WarmPoolLow.WarmPoolLowFiredSince(ctx, pool, now.Add(-warmPoolLowRecencyWindow))
	if err != nil {
		return true
	}
	return fired
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

// resolveModeFactors returns the (mode_factor, burst_mode_factor) the
// §5.2 line 569 mode-adjusted scaling formula consumes for one pool.
//
//   - Session mode: both factors are 1.0 (the base formula).
//   - Task mode: mode_factor defaults to maxTasksPerPod (the static
//     reuse-limit choice). When the runtime declares preConnect and a
//     ModeFactorSource carries a converged task-reuse median, that
//     observed reuse overrides the static fallback per §5.2 line 569.
//     burst_mode_factor stays 1.0 for task mode (no per-pod burst reuse).
//   - Concurrent mode: both factors are maxConcurrent (the per-pod slot
//     bound) — the §5.2 line 569 formula treats concurrent pods as
//     reusing their slots for both the steady-state and burst terms.
//
// spec: §5.2 lines 549, 569.
func resolveModeFactors(ctx context.Context, cfg PoolConfig, src ModeFactorSource) (modeFactor, burstModeFactor float64) {
	switch cfg.Template.ExecutionMode {
	case "task":
		modeFactor = taskStaticReuseFloor(cfg)
		if src != nil {
			if med, ok, err := src.PoolTaskReuseMedian(ctx, cfg.Name); err == nil && ok && med > 0 {
				modeFactor = med
			}
		}
		burstModeFactor = 1
	case "concurrent":
		if cfg.Template.MaxConcurrent > 0 {
			modeFactor = float64(cfg.Template.MaxConcurrent)
			burstModeFactor = modeFactor
		} else {
			modeFactor = 1
			burstModeFactor = 1
		}
	default:
		modeFactor = 1
		burstModeFactor = 1
	}
	return modeFactor, burstModeFactor
}

// taskStaticReuseFloor resolves the §5.2 task-mode static
// `mode_factor` fallback from the pool's TaskPolicy.MaxTasksPerPod.
// Spec line 549 names the field as the bootstrap-mode value; a missing
// or zero TaskPolicy maps to 1.0 (session-equivalent sizing) until the
// pool-config validator catches the misconfiguration. spec: §5.2 line
// 549.
func taskStaticReuseFloor(cfg PoolConfig) float64 {
	if cfg.Template.TaskPolicy != nil && cfg.Template.TaskPolicy.MaxTasksPerPod > 0 {
		return float64(cfg.Template.TaskPolicy.MaxTasksPerPod)
	}
	return 1
}
