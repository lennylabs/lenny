// SPDX-License-Identifier: MIT

package recycle

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"

	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
	"github.com/lennylabs/lenny/pkg/controller/warmpool"
	"github.com/lennylabs/lenny/pkg/gateway/podclaim"
	"github.com/lennylabs/lenny/pkg/sandbox/podscrub"
	claimstate "github.com/lennylabs/lenny/pkg/sandboxclaim/state"
)

// MissingReportGracePeriod pads the §3.4 gateway-side missing-report timeout
// beyond the pool's cleanupTimeoutSeconds so a scrub that finishes near the
// deadline still reports before the gateway retires the pod. The timeout
// retires the pod if no ReportPodScrub arrives within
// cleanupTimeoutSeconds plus this grace. It is a round-number control-plane
// pad rather than a spec-fixed constant. spec: §3.4 (cleanupTimeoutSeconds
// plus a grace), §4.7 (missing-report timeout).
const MissingReportGracePeriod = 15 * time.Second

// DefaultCleanupTimeout is the §5.2 sessionPolicy.cleanupTimeoutSeconds
// default the missing-report timeout uses when a recycling pool leaves the
// field unset. The spec runtime.yaml example sets it to 60s. spec: §5.2
// (cleanupTimeoutSeconds default), §3.4.
const DefaultCleanupTimeout = 60 * time.Second

// RewarmReadyPollInterval is how often the §3.4 preConnect re-warm
// completion poll re-reads the agent pod readiness while a recycling claim
// carries rewarmStartedAt. The gateway has no pod informer (it dials the
// cluster with a direct client, not a controller-runtime manager), so the
// re-warm completion is polled rather than watched. spec: §3.4 (gateway-side
// re-warm completion drives recycling → reserved).
const RewarmReadyPollInterval = 1 * time.Second

// reserveFunc patches a recycling claim to reserved, stamping holdExpiresAt,
// and returns the §3.2 hold token. *podclaim.WriteReservedStatus satisfies it.
type reserveFunc func(ctx context.Context, podID string) (podclaim.ReservedHold, error)

// retireFunc writes a terminal disposition on a recycling claim so the
// projection drains the pod. *podclaim.WriteDispositionStatus satisfies it.
type retireFunc func(ctx context.Context, podID string, failed bool) error

// claimBindingReader reports a pod's claim binding state and whether the
// claim carries a rewarmStartedAt stamp. exists is false when the claim is
// gone (a concurrent retirement, the orphan GC, or a hold-expiry DELETE
// reclaimed it).
type claimBindingReader func(ctx context.Context, podID string) (phase claimstate.State, rewarmStarted, exists bool, err error)

// cleanupTimeoutResolver resolves the pod's pool
// sessionPolicy.cleanupTimeoutSeconds, the §3.4 missing-report timeout base.
// A pod whose pool no longer resolves, or a pool with no cleanup timeout set,
// returns DefaultCleanupTimeout so the timeout is always well-defined.
type cleanupTimeoutResolver func(ctx context.Context, podID string) time.Duration

// podReadyReader reports whether the agent pod's Ready condition is True.
// gone is true when the pod object no longer exists.
type podReadyReader func(ctx context.Context, podID string) (ready, gone bool, err error)

// RecycleBoundaryCoordinator owns the two §3.4 gateway-side timers the recycle
// boundary needs beyond the reserved-hold expiry the HoldCoordinator runs:
//
//   - The missing-report timeout: armed at the bound → recycling patch
//     (Binder.Release and SlotClaimer.ReleaseSlot recycle branches), it
//     retires the pod if no ReportPodScrub arrives within
//     cleanupTimeoutSeconds plus a grace. Without it a hung or silent adapter
//     on a still-running gateway leaves the claim in `recycling` (the pod
//     projects `claimed`) until the much longer §4.6.1 orphan-GC window, which
//     the proposal scopes to the coordinator-crash case only.
//   - The preConnect re-warm completion: when ReportPodScrub arrives on a
//     preConnect pool the scrub reporter stamps rewarmStartedAt (the pod
//     projects sdk_connecting) and signals the coordinator, which polls the
//     agent pod readiness and, once Ready, patches the claim recycling →
//     reserved and registers the hold. Nothing else produces this patch: the
//     gateway has no SDK-ready report RPC, so the coordinator drives the
//     success edge. The WarmPoolController's sdkConnectTimeoutSeconds watchdog
//     remains the failure authority (it retires a re-warm that overruns), so
//     the coordinator polls only to drive the reserved edge and stops at the
//     same budget.
//
// The coordinator is replica-local: only the replica that coordinated the
// recycle holds the pod's timers. A holder crash leaves the claim for the
// §4.6.1 orphan GC, which drains a `recycling` claim left with no active
// session after the orphan timeout. The reserved-hold expiry timer the
// re-warm completion arms is owned by the HoldCoordinator (the reserve patch
// hands it the token through the HoldRegistrar).
//
// spec: §3.4 (gateway-side missing-report timeout, preConnect re-warm drives
// recycling → reserved), §4.7 (missing report bounded by cleanupTimeoutSeconds
// plus a grace), §6.2 / §6.14 (recycling → reserved binding edge).
type RecycleBoundaryCoordinator struct {
	reserve        reserveFunc
	retire         retireFunc
	binding        claimBindingReader
	podReady       podReadyReader
	cleanupTimeout cleanupTimeoutResolver
	holds          HoldRegistrar
	now            func() time.Time
	rewarmBudget   time.Duration
	pollEvery      time.Duration
	grace          time.Duration
	log            *slog.Logger

	// afterFunc schedules fn after d, returning a handle whose Stop cancels
	// it. Injectable for deterministic tests; production uses time.AfterFunc.
	afterFunc func(d time.Duration, fn func()) timerHandle

	mu     sync.Mutex
	timers map[string]timerHandle // pod → missing-report timer
	// rewarming guards against arming two re-warm polls for the same pod (a
	// duplicate ReportPodScrub). It is cleared when the poll finishes.
	rewarming map[string]context.CancelFunc
}

// RecycleBoundaryCoordinatorOptions configures a RecycleBoundaryCoordinator.
type RecycleBoundaryCoordinatorOptions struct {
	// Client reads the agent pod readiness and the claim binding state, and
	// backs the reserved/disposition status patches. Required.
	Client client.Client
	// Namespace is the agent namespace the pods and claims live in. Required.
	Namespace string
	// Pools resolves the pod's pool to its sessionPolicy.cleanupTimeoutSeconds,
	// the §3.4 missing-report timeout base. Required.
	Pools poolReader
	// HoldTTL is the §4.6.1 gateway.claimHoldTTLSeconds reserved-hold TTL
	// stamped at the re-warm-completion reserved patch. A non-positive value
	// falls back to DefaultClaimHoldTTL.
	HoldTTL time.Duration
	// Holds receives the §3.2 reserved-hold token after the re-warm-completion
	// reserved patch so the HoldCoordinator arms the hold-TTL expiry timer.
	// Nil leaves expiry to the §4.6.1 orphan GC.
	Holds HoldRegistrar
	// RewarmBudget bounds the preConnect re-warm completion poll: the
	// coordinator stops polling after this long, leaving a re-warm that
	// overran to the WarmPoolController sdkConnectTimeoutSeconds watchdog. A
	// non-positive value falls back to defaultRewarmBudget.
	RewarmBudget time.Duration
	// PollInterval is the re-warm readiness poll period. A non-positive value
	// falls back to RewarmReadyPollInterval.
	PollInterval time.Duration
	// GracePeriod pads the §3.4 missing-report timeout beyond the pool's
	// cleanupTimeoutSeconds. A non-positive value falls back to
	// MissingReportGracePeriod. It is operator-tunable (and shortened in tests)
	// because the grace is a control-plane pad rather than a spec-fixed window.
	GracePeriod time.Duration
	// Now overrides the clock; nil uses wall time.
	Now func() time.Time
	// AfterFunc overrides the missing-report timer scheduler for tests; nil
	// uses time.AfterFunc.
	AfterFunc func(d time.Duration, fn func()) timerHandle
	// Logger records timeout and re-warm outcomes; nil resolves to
	// slog.Default().
	Logger *slog.Logger
}

// defaultRewarmBudget bounds the preConnect re-warm completion poll. It pads
// the §6.1 default sdkConnectTimeoutSeconds (60s) so the coordinator stops
// polling shortly after the WarmPoolController watchdog would have retired an
// overrunning re-warm, leaving the failure path to the watchdog. spec: §6.1
// (sdkConnectTimeoutSeconds), §3.4.
const defaultRewarmBudget = 75 * time.Second

// NewRecycleBoundaryCoordinator builds the §3.4 recycle-boundary coordinator.
// Client and Namespace are required. The reserve, retire, claim-binding, and
// pod-readiness seams are wired to the podclaim writers and the cluster client.
func NewRecycleBoundaryCoordinator(opts RecycleBoundaryCoordinatorOptions) (*RecycleBoundaryCoordinator, error) {
	if opts.Client == nil {
		return nil, errors.New("recycle: RecycleBoundaryCoordinator Client is required")
	}
	if opts.Namespace == "" {
		return nil, errors.New("recycle: RecycleBoundaryCoordinator Namespace is required")
	}
	if opts.Pools == nil {
		return nil, errors.New("recycle: RecycleBoundaryCoordinator Pools is required")
	}
	holdTTL := opts.HoldTTL
	if holdTTL <= 0 {
		holdTTL = DefaultClaimHoldTTL
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	afterFunc := opts.AfterFunc
	if afterFunc == nil {
		afterFunc = func(d time.Duration, fn func()) timerHandle {
			return time.AfterFunc(d, fn)
		}
	}
	rewarmBudget := opts.RewarmBudget
	if rewarmBudget <= 0 {
		rewarmBudget = defaultRewarmBudget
	}
	pollEvery := opts.PollInterval
	if pollEvery <= 0 {
		pollEvery = RewarmReadyPollInterval
	}
	grace := opts.GracePeriod
	if grace <= 0 {
		grace = MissingReportGracePeriod
	}
	log := opts.Logger
	if log == nil {
		log = slog.Default()
	}
	cl := opts.Client
	ns := opts.Namespace
	return &RecycleBoundaryCoordinator{
		reserve: func(ctx context.Context, podID string) (podclaim.ReservedHold, error) {
			return podclaim.WriteReservedStatus(ctx, cl, ns, podclaim.ClaimName(podID), holdTTL, now)
		},
		retire: func(ctx context.Context, podID string, failed bool) error {
			disposition := claimstate.Released
			if failed {
				disposition = claimstate.Failed
			}
			return podclaim.WriteDispositionStatus(ctx, cl, ns, podclaim.ClaimName(podID), disposition, now)
		},
		binding:        bindingReader(cl, ns),
		podReady:       podReadinessReader(cl, ns),
		cleanupTimeout: cleanupTimeoutReader(cl, ns, opts.Pools),
		holds:          opts.Holds,
		now:            now,
		rewarmBudget:   rewarmBudget,
		pollEvery:      pollEvery,
		grace:          grace,
		log:            log,
		afterFunc:      afterFunc,
		timers:         make(map[string]timerHandle),
		rewarming:      make(map[string]context.CancelFunc),
	}, nil
}

// bindingReader reads a pod's claim binding state and rewarm stamp via the
// cluster client. A gone claim reports exists=false.
func bindingReader(cl client.Client, ns string) claimBindingReader {
	return func(ctx context.Context, podID string) (claimstate.State, bool, bool, error) {
		var claim lennyv1.SandboxClaim
		if err := cl.Get(ctx, client.ObjectKey{Namespace: ns, Name: podclaim.ClaimName(podID)}, &claim); err != nil {
			if apierrors.IsNotFound(err) {
				return "", false, false, nil
			}
			return "", false, false, fmt.Errorf("recycle: read claim binding for pod %s: %w", podID, err)
		}
		return claimstate.State(claim.Status.Phase), claim.Status.RewarmStartedAt != nil, true, nil
	}
}

// podReadinessReader reads a pod's Ready condition via the cluster client. A
// gone pod reports gone=true.
func podReadinessReader(cl client.Client, ns string) podReadyReader {
	return func(ctx context.Context, podID string) (bool, bool, error) {
		var pod corev1.Pod
		if err := cl.Get(ctx, client.ObjectKey{Namespace: ns, Name: podID}, &pod); err != nil {
			if apierrors.IsNotFound(err) {
				return false, true, nil
			}
			return false, false, fmt.Errorf("recycle: read pod readiness for pod %s: %w", podID, err)
		}
		for _, c := range pod.Status.Conditions {
			if c.Type == corev1.PodReady {
				return c.Status == corev1.ConditionTrue, false, nil
			}
		}
		return false, false, nil
	}
}

// cleanupTimeoutReader resolves the pod's pool
// sessionPolicy.cleanupTimeoutSeconds via the pod's pool label and the pool
// store. A pod whose pool label is missing, whose pool no longer resolves, or
// whose pool leaves cleanupTimeoutSeconds unset falls back to
// DefaultCleanupTimeout so the §3.4 missing-report timeout is always
// well-defined. spec: §5.2 (cleanupTimeoutSeconds), §3.4.
func cleanupTimeoutReader(cl client.Client, ns string, pools poolReader) cleanupTimeoutResolver {
	return func(ctx context.Context, podID string) time.Duration {
		var pod corev1.Pod
		if err := cl.Get(ctx, client.ObjectKey{Namespace: ns, Name: podID}, &pod); err != nil {
			return DefaultCleanupTimeout
		}
		poolName := pod.Labels[warmpool.LabelPool]
		if poolName == "" {
			return DefaultCleanupTimeout
		}
		pool, err := pools.Get(ctx, poolName)
		if err != nil {
			return DefaultCleanupTimeout
		}
		if pool.SessionPolicy != nil && pool.SessionPolicy.CleanupTimeoutSeconds > 0 {
			return time.Duration(pool.SessionPolicy.CleanupTimeoutSeconds) * time.Second
		}
		return DefaultCleanupTimeout
	}
}

// OnRecycling arms the §3.4 missing-report timeout for podID at the
// bound → recycling patch. The timeout base is the pod's pool
// sessionPolicy.cleanupTimeoutSeconds (defaulting to DefaultCleanupTimeout);
// the timer fires after that plus MissingReportGracePeriod. On expiry, if no
// ReportPodScrub arrived (the claim is still `recycling` with no
// rewarmStartedAt) the pod is retired so it does not linger in `recycling`
// until the much longer §4.6.1 orphan-GC window.
//
// OnRecycling is idempotent on the pod key: a re-arm (a duplicate recycle
// patch on a re-bound pod) cancels the prior timer and re-arms, so a single
// timer fires per recycle episode. The cleanup-timeout resolution issues one
// API read on its own bounded context (the caller is the release hot path).
//
// spec: §3.4 (missing-report timeout armed at session termination), §4.7.
func (c *RecycleBoundaryCoordinator) OnRecycling(podID string) {
	resolveCtx, cancel := context.WithTimeout(context.Background(), recycleBoundaryWriteTimeout)
	cleanupTimeout := c.cleanupTimeout(resolveCtx, podID)
	cancel()
	delay := cleanupTimeout + c.grace
	c.mu.Lock()
	defer c.mu.Unlock()
	if prior, ok := c.timers[podID]; ok {
		prior.Stop()
	}
	c.timers[podID] = c.afterFunc(delay, func() { c.expireMissingReport(podID) })
}

// OnScrubReported cancels the missing-report timeout for podID (a
// ReportPodScrub arrived) and, on a preConnect pool, begins the re-warm
// completion poll that drives the claim recycling → reserved once the SDK
// re-warm makes the pod Ready. On a non-preConnect pool the disposition driver
// already reserved the claim synchronously, so only the timer is cancelled.
//
// spec: §3.4 (ReportPodScrub cancels the missing-report timer; preConnect
// re-warm completion drives recycling → reserved).
func (c *RecycleBoundaryCoordinator) OnScrubReported(podID string, preConnect bool) {
	c.cancelTimer(podID)
	if !preConnect {
		return
	}
	c.startRewarmPoll(podID)
}

// cancelTimer stops and forgets the missing-report timer for podID.
func (c *RecycleBoundaryCoordinator) cancelTimer(podID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if t, ok := c.timers[podID]; ok {
		t.Stop()
		delete(c.timers, podID)
	}
}

// expireMissingReport is the missing-report timer callback. It re-reads the
// claim and retires the pod only when no ReportPodScrub arrived: the claim is
// still `recycling` with no rewarmStartedAt. A claim that already advanced
// (reserved, a terminal disposition, or a rewarmStartedAt stamp meaning the
// preConnect re-warm began) is left alone, and a gone claim is a no-op. The
// retire is fail-closed (`failed`): a scrub that never completed leaves the
// pod's residual state uncleared, so the pod must not be reused.
func (c *RecycleBoundaryCoordinator) expireMissingReport(podID string) {
	c.mu.Lock()
	if _, ok := c.timers[podID]; !ok {
		c.mu.Unlock()
		// Cancelled between the timer firing and this callback acquiring the
		// lock; the report arrived.
		return
	}
	delete(c.timers, podID)
	c.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), recycleBoundaryWriteTimeout)
	defer cancel()
	phase, rewarmStarted, exists, err := c.binding(ctx, podID)
	if err != nil {
		c.log.LogAttrs(ctx, slog.LevelWarn, "recycle: missing-report timeout read claim failed",
			slog.String("pod_id", podID), slog.String("err", err.Error()))
		return
	}
	if !exists {
		// The claim was reclaimed concurrently (orphan GC, a hold-expiry
		// DELETE, or a retire that already drained); nothing to retire.
		return
	}
	if phase != claimstate.Recycling || rewarmStarted {
		// A ReportPodScrub arrived after the timer fired but before this
		// callback (a non-preConnect reserve, a preConnect rewarmStartedAt
		// stamp, or a terminal disposition): the report is not missing.
		return
	}
	if err := c.retire(ctx, podID, true); err != nil {
		if apierrors.IsNotFound(err) {
			return
		}
		c.log.LogAttrs(ctx, slog.LevelWarn, "recycle: missing-report timeout retire failed",
			slog.String("pod_id", podID), slog.String("err", err.Error()))
		return
	}
	c.log.LogAttrs(ctx, slog.LevelWarn, "recycle: pod retired on missing whole-pod scrub report",
		slog.String("pod_id", podID),
		slog.String("reason", string(podscrub.ReasonScrubReportTimeout)))
}

// startRewarmPoll launches the preConnect re-warm completion poll for podID.
// It is idempotent on the pod key: a duplicate ReportPodScrub does not start a
// second poll. The poll runs on its own bounded context (the ReportPodScrub
// RPC returns before the re-warm completes, so a request-scoped deadline would
// abort it).
func (c *RecycleBoundaryCoordinator) startRewarmPoll(podID string) {
	c.mu.Lock()
	if _, ok := c.rewarming[podID]; ok {
		c.mu.Unlock()
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), c.rewarmBudget)
	c.rewarming[podID] = cancel
	c.mu.Unlock()
	go c.pollRewarm(ctx, podID)
}

// pollRewarm reads the agent pod readiness every pollEvery until the pod is
// Ready (then patches the claim recycling → reserved and registers the hold),
// the claim leaves `recycling` (a concurrent reserve, rebind, or retire), the
// claim or pod is gone, or the re-warm budget elapses. On budget exhaustion it
// stops without retiring: the WarmPoolController sdkConnectTimeoutSeconds
// watchdog is the failure authority for an overrunning re-warm. spec: §3.4
// (re-warm completion drives recycling → reserved), §6.1 (watchdog retires an
// overrunning re-warm).
func (c *RecycleBoundaryCoordinator) pollRewarm(ctx context.Context, podID string) {
	defer func() {
		c.mu.Lock()
		if cancel, ok := c.rewarming[podID]; ok {
			cancel()
			delete(c.rewarming, podID)
		}
		c.mu.Unlock()
	}()

	ticker := time.NewTicker(c.pollEvery)
	defer ticker.Stop()
	// Check once immediately so a re-warm that completed before the first
	// tick reserves without waiting a whole interval.
	if c.tryReserveOnReady(ctx, podID) {
		return
	}
	for {
		select {
		case <-ctx.Done():
			// Budget elapsed (or shutdown). The watchdog retires an
			// overrunning re-warm; nothing to do here.
			return
		case <-ticker.C:
			if c.tryReserveOnReady(ctx, podID) {
				return
			}
		}
	}
}

// tryReserveOnReady reads the claim and pod once and, when the claim is still
// `recycling` with rewarmStartedAt and the pod is Ready, reserves the claim
// and registers the hold. It returns done=true when the poll should stop: the
// pod was reserved, the claim left `recycling`, or the claim/pod is gone. A
// transient read error returns done=false so the poll retries on the next
// tick.
func (c *RecycleBoundaryCoordinator) tryReserveOnReady(ctx context.Context, podID string) (done bool) {
	phase, rewarmStarted, exists, err := c.binding(ctx, podID)
	if err != nil {
		return false
	}
	if !exists {
		return true
	}
	if phase != claimstate.Recycling {
		// A concurrent rebind/reserve/retire advanced the claim; stop.
		return true
	}
	if !rewarmStarted {
		// The rewarmStartedAt stamp has not landed yet (a racing read between
		// the OnScrubReported signal and the driver's stamp write); retry.
		return false
	}
	ready, gone, err := c.podReady(ctx, podID)
	if err != nil {
		return false
	}
	if gone {
		return true
	}
	if !ready {
		return false
	}
	hold, err := c.reserve(ctx, podID)
	if err != nil {
		if apierrors.IsNotFound(err) {
			// The claim vanished between the read and the reserve patch.
			return true
		}
		c.log.LogAttrs(ctx, slog.LevelWarn, "recycle: re-warm completion reserve failed",
			slog.String("pod_id", podID), slog.String("err", err.Error()))
		return false
	}
	if c.holds != nil {
		c.holds.Hold(podID, hold)
	}
	c.log.LogAttrs(ctx, slog.LevelDebug, "recycle: preConnect re-warm complete, claim reserved",
		slog.String("pod_id", podID))
	return true
}

// recycleBoundaryWriteTimeout bounds the claim read and the retire/reserve
// patch the timer callbacks issue outside any request scope. It is not
// operator-tunable because it bounds one control-plane call rather than a
// spec-fixed window.
const recycleBoundaryWriteTimeout = 10 * time.Second

// Stop cancels every armed missing-report timer and re-warm poll. It is
// called on gateway shutdown so the in-process timers and goroutines do not
// run against a draining client. The recycling claims it abandons are
// reclaimed by the §4.6.1 orphan GC after the orphan timeout, so a clean
// shutdown never strands a pod.
func (c *RecycleBoundaryCoordinator) Stop() {
	c.mu.Lock()
	defer c.mu.Unlock()
	for podID, t := range c.timers {
		t.Stop()
		delete(c.timers, podID)
	}
	for podID, cancel := range c.rewarming {
		cancel()
		delete(c.rewarming, podID)
	}
}
