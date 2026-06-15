// SPDX-License-Identifier: MIT

// Package recycle wires the concrete gateway-side seams the §4.7
// ScrubReporter (pkg/gateway/leasecontrol) drives. The ScrubReporter
// itself holds the pure orchestration — increment the recycle counters,
// run the taskcleanup disposition, drive the claim binding state — behind
// five narrow interfaces defined at that consumer. This package supplies
// the concrete implementations so leasecontrol stays free of the
// Kubernetes client, agentpodstate, slothealth, podclaim, poolstore,
// runtimestore, and gatewaymetrics. The gateway constructs these here and
// passes them into leasecontrol.NewScrubReporter.
//
// spec: §4.7 (ReportSessionScrub/ReportPodScrub gateway side), §3.4
// (recycle disposition), §5.2 (scrub model, onScrubFailure), §6.39
// (host-node schedulability retire), §16.1 (scrub-failure and retirement
// metrics).
package recycle

import (
	"context"
	"errors"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/lennylabs/lenny/pkg/agentpodstate"
	"github.com/lennylabs/lenny/pkg/controller/warmpool"
	"github.com/lennylabs/lenny/pkg/gateway/leasecontrol"
	"github.com/lennylabs/lenny/pkg/gateway/podclaim"
	"github.com/lennylabs/lenny/pkg/gateway/poolstore"
	"github.com/lennylabs/lenny/pkg/gateway/runtimestore"
	"github.com/lennylabs/lenny/pkg/gateway/slothealth"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
	"github.com/lennylabs/lenny/pkg/sandbox/taskcleanup"
	claimstate "github.com/lennylabs/lenny/pkg/sandboxclaim/state"
)

// DefaultClaimHoldTTL is the §4.6.1 / §6.2 deployment-level reserved-hold
// TTL (`gateway.claimHoldTTLSeconds`) applied when no override is
// configured: the reservation time plus this TTL is stamped as
// `holdExpiresAt` when a recycled non-preConnect pod enters `reserved`.
// The spec deployment default is 10 seconds. spec: §4.6.1, §6.2 (reserved
// hold semantics, claimHoldTTLSeconds default 10s).
const DefaultClaimHoldTTL = 10 * time.Second

// CounterStore is the agent_pod_state subset the §4.7 recycle-counter seam
// needs. *agentpodstate.Store (and its memstore/pgstore implementations)
// satisfy it directly: the three methods are the same recycle-counter
// surface leasecontrol.RecycleCounterStore declares, so NewCounterStore is
// an adapter that maps agentpodstate.RecycleCounters onto the leasecontrol
// return tuple.
type CounterStore interface {
	IncrementSessionsServed(ctx context.Context, podID string) (int, bool, error)
	IncrementScrubFailureCount(ctx context.Context, podID string) (int, bool, error)
	RecycleCounters(ctx context.Context, podID string) (agentpodstate.RecycleCounters, bool, error)
}

// recycleCounterStore adapts a CounterStore (agentpodstate.Store) onto the
// leasecontrol.RecycleCounterStore seam. The increment methods already
// match; only RecycleCounters needs the struct-to-tuple unpacking.
type recycleCounterStore struct {
	store CounterStore
}

// NewRecycleCounterStore wraps an agent_pod_state store as the §4.7
// recycle-counter seam the ScrubReporter writes. spec: §4.7
// (sessionsServed / scrubFailureCount increments), §5.2.
func NewRecycleCounterStore(store CounterStore) leasecontrol.RecycleCounterStore {
	return &recycleCounterStore{store: store}
}

func (s *recycleCounterStore) IncrementSessionsServed(ctx context.Context, podID string) (int, bool, error) {
	return s.store.IncrementSessionsServed(ctx, podID)
}

func (s *recycleCounterStore) IncrementScrubFailureCount(ctx context.Context, podID string) (int, bool, error) {
	return s.store.IncrementScrubFailureCount(ctx, podID)
}

func (s *recycleCounterStore) RecycleCounters(ctx context.Context, podID string) (int, int, bool, error) {
	c, found, err := s.store.RecycleCounters(ctx, podID)
	if err != nil {
		return 0, 0, false, err
	}
	return c.SessionsServed, c.ScrubFailureCount, found, nil
}

// drainLedger is the §5.2 unhealthy-threshold drain ledger: it records a
// leaked session-scrub outcome in the in-memory slothealth tracker and,
// once the pod crosses the §5.2 ceil(maxConcurrentSessions/2) threshold
// within the rolling window, stamps lenny.dev/drain-request on the agent
// Pod so the WarmPoolController drains it. The threshold denominator is the
// pod's pool maxConcurrentSessions, resolved per pod at leak time: a
// single-session recycling pod crosses on the first leak, and a recycling
// concurrent-session pool (the §5.2 "Concurrent" preset, maxConcurrentSessions:
// N with recycle.enabled) drains only at ceil(N/2) failed-or-leaked slots.
//
// The gateway never writes Sandbox.status itself (§4.6.3), so the drain is
// routed through the annotation rather than a phase write; podclaim.
// StampDrainRequest performs the §4.6.3 gateway-stamps-drain-request patch.
type drainLedger struct {
	tracker   *slothealth.Tracker
	cl        client.Client
	namespace string
	pools     poolReader
	now       func() time.Time
}

// DrainLedgerOptions configures a drain ledger.
type DrainLedgerOptions struct {
	// Tracker accumulates per-pod leak/failure events over the §5.2
	// rolling window. Required.
	Tracker *slothealth.Tracker
	// Client patches the agent Pod's lenny.dev/drain-request annotation and
	// reads its pool label to resolve the unhealthy threshold. Required.
	Client client.Client
	// Namespace is the agent namespace the pods live in. Required.
	Namespace string
	// Pools resolves the pod's pool to its §5.2
	// sessionPolicy.maxConcurrentSessions, the denominator of the
	// ceil(maxConcurrentSessions/2) unhealthy threshold. Resolving it per
	// pod keeps a recycling concurrent-session pool (the "Concurrent"
	// preset) on its ceil(N/2) threshold rather than draining on the first
	// leak. Required.
	Pools poolReader
	// Now overrides the clock for the drain-request stamp; nil uses wall
	// time.
	Now func() time.Time
}

// NewDrainLedger builds the §5.2 unhealthy-threshold drain ledger. Tracker,
// Client, Namespace, and Pools are required.
func NewDrainLedger(opts DrainLedgerOptions) (leasecontrol.DrainLedger, error) {
	if opts.Tracker == nil {
		return nil, errors.New("recycle: DrainLedger Tracker is required")
	}
	if opts.Client == nil {
		return nil, errors.New("recycle: DrainLedger Client is required")
	}
	if opts.Namespace == "" {
		return nil, errors.New("recycle: DrainLedger Namespace is required")
	}
	if opts.Pools == nil {
		return nil, errors.New("recycle: DrainLedger Pools is required")
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	return &drainLedger{
		tracker:   opts.Tracker,
		cl:        opts.Client,
		namespace: opts.Namespace,
		pools:     opts.Pools,
		now:       now,
	}, nil
}

// RecordLeak records one leaked session-scrub outcome for podID and stamps
// the drain-request annotation once the pod crosses the unhealthy
// threshold. The threshold denominator is the pod's pool
// maxConcurrentSessions, resolved per pod: a recycling concurrent-session
// pool drains at ceil(maxConcurrentSessions/2) failed-or-leaked slots, while
// a single-session pool drains on the first leak. spec: §4.7 (leaked feeds
// the unhealthy-threshold ledger), §4.6.3 (gateway stamps drain-request),
// §5.2 (ceil(maxConcurrentSessions/2) unhealthy threshold).
func (l *drainLedger) RecordLeak(ctx context.Context, podID string) error {
	l.tracker.RecordLeak(podID)
	maxConcurrent, err := l.maxConcurrentSessions(ctx, podID)
	if err != nil {
		return err
	}
	if !l.tracker.Unhealthy(podID, maxConcurrent) {
		return nil
	}
	if err := podclaim.StampDrainRequest(ctx, l.cl, l.namespace, podID, l.now()); err != nil {
		return fmt.Errorf("recycle: stamp drain-request for leaked pod %s: %w", podID, err)
	}
	return nil
}

// maxConcurrentSessions resolves the pod's pool §5.2
// sessionPolicy.maxConcurrentSessions, the denominator of the
// ceil(maxConcurrentSessions/2) unhealthy threshold. A pod or pool that no
// longer resolves, or a pool with no SessionPolicy, falls back to 1 (the
// §5.2 default one-session-per-pod bound), which slothealth.UnhealthyThreshold
// clamps a sub-1 value to anyway. A missing pool label fails closed: a leak
// on an unresolvable pod must not be silently dropped. spec: §5.2
// (maxConcurrentSessions default 1).
func (l *drainLedger) maxConcurrentSessions(ctx context.Context, podID string) (int32, error) {
	var pod corev1.Pod
	if err := l.cl.Get(ctx, client.ObjectKey{Namespace: l.namespace, Name: podID}, &pod); err != nil {
		if client.IgnoreNotFound(err) == nil {
			// The pod is gone: nothing left to drain, fall back to the default
			// bound so the threshold check is well-defined. spec: §3.4.
			return 1, nil
		}
		return 0, fmt.Errorf("recycle: get pod %s for drain threshold: %w", podID, err)
	}
	poolName := pod.Labels[warmpool.LabelPool]
	if poolName == "" {
		return 0, fmt.Errorf("recycle: leaked pod %s carries no %s label", podID, warmpool.LabelPool)
	}
	pool, err := l.pools.Get(ctx, poolName)
	if err != nil {
		if errors.Is(err, poolstore.ErrNotFound) {
			// The pool was deleted with the pod; the default bound keeps the
			// threshold well-defined. spec: §3.4.
			return 1, nil
		}
		return 0, fmt.Errorf("recycle: resolve pool %s for leaked pod %s: %w", poolName, podID, err)
	}
	return poolMaxConcurrentSessions(pool), nil
}

// poolMaxConcurrentSessions returns the pool's §5.2
// sessionPolicy.maxConcurrentSessions, defaulting to 1 (the one-session-per-pod
// bound) when the pool carries no SessionPolicy or leaves the field unset.
// spec: §5.2 (maxConcurrentSessions default 1).
func poolMaxConcurrentSessions(p poolstore.Pool) int32 {
	if p.SessionPolicy == nil || p.SessionPolicy.MaxConcurrentSessions < 1 {
		return 1
	}
	return int32(p.SessionPolicy.MaxConcurrentSessions)
}

// poolReader resolves a pool's §5.2 recycle policy and runtime reference.
// *poolstore.Memory and the pgstore satisfy it through poolstore.Store.
type poolReader interface {
	Get(ctx context.Context, name string) (poolstore.Pool, error)
}

// runtimeReader resolves a runtime's §5.1 capabilities (preConnect).
// *runtimestore.Memory and the pgstore satisfy it through
// runtimestore.Store.
type runtimeReader interface {
	Get(ctx context.Context, name string) (runtimestore.Runtime, error)
}

// podInspector resolves the §5.2 recycle policy and §6.39 host-node
// schedulability for a pod under a whole-pod scrub report. It reads the
// agent Pod (the lenny.dev/host-schedulable label, the lenny.dev/pool
// label, and the pod's creation time for uptime) via the gateway's Pods
// get access, the pool's recycle policy and runtime reference from
// poolstore, and the runtime's preConnect capability from runtimestore.
type podInspector struct {
	cl        client.Client
	namespace string
	pools     poolReader
	runtimes  runtimeReader
	now       func() time.Time
}

// PodInspectorOptions configures a pod inspector.
type PodInspectorOptions struct {
	// Client reads the agent Pod (host-schedulable label, pool label,
	// creation time). Required.
	Client client.Client
	// Namespace is the agent namespace the pods live in. Required.
	Namespace string
	// Pools resolves the pod's pool to its recycle policy and runtime
	// reference. Required.
	Pools poolReader
	// Runtimes resolves the pool's runtime to its preConnect capability.
	// Required.
	Runtimes runtimeReader
	// Now overrides the clock for the uptime computation; nil uses wall
	// time.
	Now func() time.Time
}

// NewPodInspector builds the §6.39 / §5.2 recycle pod inspector. Client,
// Namespace, Pools, and Runtimes are required.
func NewPodInspector(opts PodInspectorOptions) (leasecontrol.PodInspector, error) {
	if opts.Client == nil {
		return nil, errors.New("recycle: PodInspector Client is required")
	}
	if opts.Namespace == "" {
		return nil, errors.New("recycle: PodInspector Namespace is required")
	}
	if opts.Pools == nil {
		return nil, errors.New("recycle: PodInspector Pools is required")
	}
	if opts.Runtimes == nil {
		return nil, errors.New("recycle: PodInspector Runtimes is required")
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	return &podInspector{
		cl:        opts.Client,
		namespace: opts.Namespace,
		pools:     opts.Pools,
		runtimes:  opts.Runtimes,
		now:       now,
	}, nil
}

// InspectForRecycle resolves the recycle policy and pod facts for podID at
// the recycle boundary. found is false when the pod is gone (a concurrent
// retirement or the orphan GC reclaimed it), in which case the disposition
// is skipped. A missing or "false" lenny.dev/host-schedulable label reads
// as unschedulable, fail-safe per §6.39. spec: §6.39 (host-node
// schedulability read via Pods get), §5.2 (recycle policy resolution).
func (i *podInspector) InspectForRecycle(ctx context.Context, podID string) (leasecontrol.PodRecyclePolicy, bool, error) {
	var pod corev1.Pod
	if err := i.cl.Get(ctx, client.ObjectKey{Namespace: i.namespace, Name: podID}, &pod); err != nil {
		if client.IgnoreNotFound(err) == nil {
			// The pod is gone: nothing to recycle. spec: §3.4.
			return leasecontrol.PodRecyclePolicy{}, false, nil
		}
		return leasecontrol.PodRecyclePolicy{}, false, fmt.Errorf("recycle: get pod %s: %w", podID, err)
	}

	poolName := pod.Labels[warmpool.LabelPool]
	if poolName == "" {
		// A managed agent pod with no pool label cannot be evaluated against
		// a recycle policy; fail closed rather than recycle against a guessed
		// policy. spec: §4.6.1 (pool label), §5.2.
		return leasecontrol.PodRecyclePolicy{}, false, fmt.Errorf("recycle: pod %s carries no %s label", podID, warmpool.LabelPool)
	}
	pool, err := i.pools.Get(ctx, poolName)
	if err != nil {
		if errors.Is(err, poolstore.ErrNotFound) {
			// The pool was deleted: the pod is being retired with it, nothing
			// to recycle into. spec: §3.4.
			return leasecontrol.PodRecyclePolicy{}, false, nil
		}
		return leasecontrol.PodRecyclePolicy{}, false, fmt.Errorf("recycle: resolve pool %s for pod %s: %w", poolName, podID, err)
	}

	recycle := poolRecyclePolicy(pool)
	if recycle == nil {
		// A non-recycling pool reported a whole-pod scrub: the pod is not a
		// recycle candidate (one-session-per-pod), so the disposition is a
		// no-op and the pod retires on session end through the session path.
		// spec: §5.2.
		return leasecontrol.PodRecyclePolicy{}, false, nil
	}

	preConnect, runtimeProfile, err := i.resolveRuntime(ctx, pool.RuntimeRef)
	if err != nil {
		return leasecontrol.PodRecyclePolicy{}, false, err
	}

	return leasecontrol.PodRecyclePolicy{
		PreConnect:          preConnect,
		OnScrubFailure:      onScrubFailure(recycle.OnScrubFailure),
		MaxScrubFailures:    recycle.MaxScrubFailures,
		MaxSessionsPerPod:   recycle.MaxSessionsPerPod,
		MaxPodUptimeSeconds: int64(recycle.MaxPodUptimeSeconds),
		HostSchedulable:     hostSchedulable(pod.Labels),
		PodUptimeSeconds:    podUptimeSeconds(pod.CreationTimestamp.Time, i.now()),
		Pool:                poolName,
		RuntimeClass:        runtimeClass(effectiveProfile(pool, runtimeProfile)),
	}, true, nil
}

// resolveRuntime resolves the pool runtime's §5.1 capabilities.preConnect
// flag and its §5.3 default isolation profile. A runtime that no longer
// resolves (deleted between warm and recycle) is treated as non-preConnect
// with an empty default profile: a non-preConnect recycle never re-warms, so
// a stale resolution under-warms rather than reserving an SDK-cold pod, and
// the empty default falls through to the pool's explicit profile (or an
// empty runtime_class label) rather than guessing. spec: §6.2 (preConnect),
// §5.3 (runtime default isolation profile).
func (i *podInspector) resolveRuntime(ctx context.Context, runtimeRef string) (bool, isolation.Profile, error) {
	if runtimeRef == "" {
		return false, "", nil
	}
	rt, err := i.runtimes.Get(ctx, runtimeRef)
	if err != nil {
		if errors.Is(err, runtimestore.ErrNotFound) {
			return false, "", nil
		}
		return false, "", fmt.Errorf("recycle: resolve runtime %s: %w", runtimeRef, err)
	}
	return rt.Capabilities != nil && rt.Capabilities.PreConnect, rt.IsolationProfile, nil
}

// poolRecyclePolicy returns the pool's §5.2 sessionPolicy.recycle when the
// pool is a recycling session-mode pool, or nil otherwise (no
// SessionPolicy, no Recycle block, or recycle.enabled: false).
func poolRecyclePolicy(p poolstore.Pool) *runtimestore.RecyclePolicy {
	if p.SessionPolicy == nil || p.SessionPolicy.Recycle == nil || !p.SessionPolicy.Recycle.Enabled {
		return nil
	}
	return p.SessionPolicy.Recycle
}

// onScrubFailure maps the runtimestore disposition enum onto the
// taskcleanup policy the disposition reads. An empty or unrecognized value
// is treated as the warn default. spec: §5.2 (onScrubFailure).
func onScrubFailure(d runtimestore.CleanupFailureDisposition) taskcleanup.CleanupFailurePolicy {
	if d == runtimestore.CleanupFailureFail {
		return taskcleanup.OnCleanupFail
	}
	return taskcleanup.OnCleanupWarn
}

// effectiveProfile resolves the pool's §5.3 isolation profile: the pool's
// IsolationProfile override when set, falling back to the pool runtime's
// default profile when the pool carries no override. poolstore.Pool.
// IsolationProfile is documented as an override of the runtime default, so a
// pool that did not set one resolves to the runtime default rather than the
// zero profile. The §16.1 runtime_class label on the recycle metrics derives
// from the result. spec: §5.3 (pool profile overrides the runtime default),
// §16.1 (runtime_class label).
func effectiveProfile(p poolstore.Pool, runtimeDefault isolation.Profile) isolation.Profile {
	if p.IsolationProfile != "" {
		return p.IsolationProfile
	}
	return runtimeDefault
}

// runtimeClass maps the §5.3 isolation profile to the Kubernetes
// RuntimeClass label the §16.1 recycle metrics carry. An unrecognized
// profile yields an empty label rather than a guess. spec: §5.3, §16.1.
func runtimeClass(p isolation.Profile) string {
	name, _ := isolation.RuntimeClassName(p)
	return name
}

// hostSchedulable reads the §6.39 lenny.dev/host-schedulable pod label:
// true only when the label reads exactly "true". An absent or any other
// value is fail-safe-unschedulable. spec: §6.39.
func hostSchedulable(labels map[string]string) bool {
	return labels[warmpool.LabelHostSchedulable] == "true"
}

// podUptimeSeconds returns the pod's wall-clock uptime in whole seconds,
// floored at zero. A zero or future creation time yields zero. spec: §6.2
// (maxPodUptimeSeconds retire).
func podUptimeSeconds(createdAt, now time.Time) int64 {
	if createdAt.IsZero() {
		return 0
	}
	d := now.Sub(createdAt)
	if d < 0 {
		return 0
	}
	return int64(d / time.Second)
}

// claimDispositionDriver applies a resolved §3.4 recycle disposition to the
// pod's SandboxClaim binding state via the podclaim binding-state writers.
// On a recycle it stamps rewarmStartedAt on a preConnect pool (the
// projection drives sdk_connecting and the §6.2 re-warm completes
// asynchronously, after which a later WriteReservedStatus reserves the
// pod) and patches a non-preConnect pool directly to reserved; on a retire
// it writes the terminal disposition (released vs failed).
type claimDispositionDriver struct {
	cl        client.Client
	namespace string
	holdTTL   time.Duration
	now       func() time.Time
}

// ClaimDispositionDriverOptions configures the claim disposition driver.
type ClaimDispositionDriverOptions struct {
	// Client patches the SandboxClaim binding-state status subresource.
	// Required.
	Client client.Client
	// Namespace is the agent namespace the claims live in. Required.
	Namespace string
	// HoldTTL is the §4.6.1 gateway.claimHoldTTLSeconds reserved-hold TTL
	// stamped as holdExpiresAt at the reserved patch. A non-positive value
	// falls back to DefaultClaimHoldTTL.
	HoldTTL time.Duration
	// Now overrides the clock for the binding-state transition stamps; nil
	// uses wall time.
	Now func() time.Time
}

// NewClaimDispositionDriver builds the §3.4 claim disposition driver.
// Client and Namespace are required.
func NewClaimDispositionDriver(opts ClaimDispositionDriverOptions) (leasecontrol.ClaimDispositionDriver, error) {
	if opts.Client == nil {
		return nil, errors.New("recycle: ClaimDispositionDriver Client is required")
	}
	if opts.Namespace == "" {
		return nil, errors.New("recycle: ClaimDispositionDriver Namespace is required")
	}
	holdTTL := opts.HoldTTL
	if holdTTL <= 0 {
		holdTTL = DefaultClaimHoldTTL
	}
	return &claimDispositionDriver{
		cl:        opts.Client,
		namespace: opts.Namespace,
		holdTTL:   holdTTL,
		now:       opts.Now,
	}, nil
}

// Recycle drives the §3.4 reuse disposition. A preConnect pool stamps
// rewarmStartedAt on the recycling claim, anchoring the §6.2 re-warm
// watchdog; the projection then enters sdk_connecting and the re-warm
// completes asynchronously, with the reserved patch following once the SDK
// reports warm. A non-preConnect pool patches the claim directly to
// reserved. spec: §5.2 (recycle lifecycle), §6.2 (preConnect re-warm).
func (d *claimDispositionDriver) Recycle(ctx context.Context, podID string, preConnect, _ bool) error {
	claim := podclaim.ClaimName(podID)
	if preConnect {
		// The scrub_warning annotation persists through the re-warm via the
		// recycling claim's existing status; the rewarm stamp only anchors
		// the watchdog. spec: §6.2 (preConnect re-warm on scrub_warning).
		if err := podclaim.WriteRewarmStartedStatus(ctx, d.cl, d.namespace, claim, d.now); err != nil {
			return fmt.Errorf("recycle: stamp rewarm-started on claim %s: %w", claim, err)
		}
		return nil
	}
	if _, err := podclaim.WriteReservedStatus(ctx, d.cl, d.namespace, claim, d.holdTTL, d.now); err != nil {
		return fmt.Errorf("recycle: reserve claim %s: %w", claim, err)
	}
	return nil
}

// Retire writes the terminal §3.4 disposition on the claim so the
// projection drains the pod. failed selects the claim's `failed` terminal
// (the onScrubFailure: fail termination); every other retire is a drain to
// `released` (the three lifecycle limits and the §6.39 cordon-drain). The
// scrubWarning, reason, and detail are observability inputs the projection
// and audit trail consume; the binding-state writer records the terminal
// phase. spec: §3.4 (retire disposition), §4.6.3 (released vs failed
// terminals), §6.39.
func (d *claimDispositionDriver) Retire(ctx context.Context, podID string, failed, _ bool, _ taskcleanup.RetireReason, _ string) error {
	claim := podclaim.ClaimName(podID)
	disposition := claimstate.Released
	if failed {
		disposition = claimstate.Failed
	}
	if err := podclaim.WriteDispositionStatus(ctx, d.cl, d.namespace, claim, disposition, d.now); err != nil {
		return fmt.Errorf("recycle: write %s disposition on claim %s: %w", disposition, claim, err)
	}
	return nil
}

// RetirementMetricsSink records the §16.1 scrub-failure and retirement
// metrics. *gatewaymetrics.Metrics satisfies it; the interface keeps this
// package free of a direct gatewaymetrics dependency.
type RetirementMetricsSink interface {
	IncScrubFailureTotal(pool, runtimeClass string)
	SetScrubFailureCount(podID, pool, runtimeClass string, count int)
	IncRetirement(reason, pool, runtimeClass string)
}

// retirementMetrics adapts a RetirementMetricsSink onto the
// leasecontrol.RetirementMetrics seam, mapping the typed RetireReason onto
// the string label the metric sink records.
type retirementMetrics struct {
	sink RetirementMetricsSink
}

// NewRetirementMetrics wraps a gatewaymetrics sink as the §16.1 retirement
// metrics seam. A nil sink returns nil so the ScrubReporter falls back to
// its no-op metrics. spec: §16.1 (lenny_pod_scrub_failure_total,
// lenny_pod_scrub_failure_count, lenny_pod_retirement_total).
func NewRetirementMetrics(sink RetirementMetricsSink) leasecontrol.RetirementMetrics {
	if sink == nil {
		return nil
	}
	return &retirementMetrics{sink: sink}
}

func (m *retirementMetrics) IncScrubFailureTotal(pool, runtimeClass string) {
	m.sink.IncScrubFailureTotal(pool, runtimeClass)
}

func (m *retirementMetrics) SetScrubFailureCount(podID, pool, runtimeClass string, count int) {
	m.sink.SetScrubFailureCount(podID, pool, runtimeClass, count)
}

func (m *retirementMetrics) IncRetirement(reason taskcleanup.RetireReason, pool, runtimeClass string) {
	m.sink.IncRetirement(string(reason), pool, runtimeClass)
}

// Compile-time assertions that the concrete seams satisfy the leasecontrol
// consumer interfaces.
var (
	_ leasecontrol.RecycleCounterStore    = (*recycleCounterStore)(nil)
	_ leasecontrol.DrainLedger            = (*drainLedger)(nil)
	_ leasecontrol.PodInspector           = (*podInspector)(nil)
	_ leasecontrol.ClaimDispositionDriver = (*claimDispositionDriver)(nil)
	_ leasecontrol.RetirementMetrics      = (*retirementMetrics)(nil)
)
