// SPDX-License-Identifier: MIT

// Package orphansession implements the §10.1 orphan-session reconciler.
//
// A pod whose coordinating gateway replica is lost enters hold state and,
// after coordinatorHoldTimeoutSeconds, terminates. Agent pods carry zero
// RBAC and no network path to the kube-apiserver (§10.3, §13.2), so a
// terminating pod cannot write its own `Sandbox.status.phase = failed`
// CRD update; it can only attempt an AdapterTerminating gRPC message to
// the gateway. When that channel is also down (the coordinating replica
// itself crashed), the session row is left in a non-terminal state with a
// pod binding that no longer has a live pod, holding quota indefinitely.
//
// The reconciler runs every 60 seconds and cross-references the §4.6.1
// agent_pod_state mirror: any non-terminal session with an active pod
// binding whose pod has reached the §6.2 `terminated` phase is forcibly
// transitioned to `failed` with reason `orphan_pod_terminated`. When the
// mirror for a pool is stale (lag > 60s, e.g. during a WarmPoolController
// failover) or carries no row for the bound pod, the reconciler falls
// back to a direct Kubernetes read of the Sandbox phase so stale mirror
// data cannot silently block orphan detection.
//
// The transition is idempotent across gateway replicas: the store Update
// mutator no-ops when a concurrent writer already drove the row terminal,
// so the reconciler tolerates running on every replica, matching the
// pkg/gateway/orphancleanup precedent.
//
// spec: §10.1 lines 47-52 (coordinator-loss detection, orphan-session
// reconciliation, mirror staleness fallback).
package orphansession

import (
	"context"
	"log"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
)

// §10.1 defaults.
const (
	// DefaultInterval is the §10.1 line 51 reconcile cadence ("every 60
	// seconds, same leader-only pattern as orphan claim detection").
	DefaultInterval = 60 * time.Second
	// DefaultStaleMirrorThreshold is the §10.1 line 51 lag past which the
	// reconciler stops trusting the mirror for a pool and falls back to a
	// direct Kubernetes read ("When mirror staleness exceeds 60s …").
	DefaultStaleMirrorThreshold = 60 * time.Second
	// podPhaseTerminated is the §6.2 phase written into the mirror's
	// state column (and Sandbox.status.phase) when a pod has torn down.
	// Kept as a local literal so the reconciler does not pull the
	// controller-runtime dependency the podlifecycle package carries.
	// spec: §6.2 — terminal pod phase.
	podPhaseTerminated = "terminated"
	// orphanReason is the §10.1 line 51 failure reason recorded on the
	// forced transition.
	orphanReason = string(session.FailureOrphanPodTerminated)
)

// orphanEligibleStates is the §10.1 line 51 non-terminal-with-pod set:
// "running, attached, starting, suspended (with pod), finalizing,
// input_required". `attached` is an internal pod phase that the REST
// surface projects as `running`, so `running` covers it. `suspended` is
// admitted only when the row still carries a pod binding (the
// PodAssignment gate below); a podless suspension has no pod to
// cross-reference and is excluded per the same line.
var orphanEligibleStates = map[session.State]bool{
	session.StateStarting:      true,
	session.StateRunning:       true,
	session.StateInputRequired: true,
	session.StateFinalizing:    true,
	session.StateSuspended:     true,
}

// TenantLister enumerates the tenants the reconcile pass covers.
type TenantLister interface {
	ListTenants(ctx context.Context) ([]string, error)
}

// MirrorReader is the §4.6.1 agent_pod_state mirror surface the
// reconciler reads: the per-pod §6.2 phase and the per-pool staleness.
// The cmd-side adapter maps pkg/agentpodstate.Store onto it, which keeps
// this package free of the agentpodstate (and controller-runtime)
// dependency.
type MirrorReader interface {
	// GetByPodID reads the mirror row for podID. found=false means no
	// row exists for the pod.
	GetByPodID(ctx context.Context, podID string) (MirrorPod, bool, error)
	// MirrorLagSeconds returns now() - max(updated_at) for poolID's rows.
	MirrorLagSeconds(ctx context.Context, poolID string) (float64, error)
}

// MirrorPod is the subset of an agent_pod_state row the reconciler
// needs.
type MirrorPod struct {
	// PoolID is the pod's pool, the key for the per-pool lag gauge.
	PoolID string
	// Phase is the mirrored §6.2 phase (agent_pod_state.state).
	Phase string
}

// PodPhaseReader is the §10.1 line 51 direct-Kubernetes fallback,
// consulted when the mirror is stale for the pool or carries no row for
// the bound pod. It wraps the gateway's PodLifecycleManager.GetPodStatus.
// found=false reports that the Sandbox no longer exists, itself a
// terminal signal (the pod is gone). A nil PodPhaseReader disables the
// fallback; the reconciler then conservatively skips a session it cannot
// resolve from the mirror.
type PodPhaseReader interface {
	PodPhase(ctx context.Context, sessionID, podID, poolID string) (phase string, found bool, err error)
}

// TerminalHook runs the gateway's terminal-side-effects pipeline
// (workspace seal, executor release — which reclaims the pod and returns
// §5.2 slots — SSE, audit, billing, archive) once the reconciler has
// written the `failed` row. pkg/gateway/sessionserver.Server satisfies
// it. Shared with pkg/gateway/orphancleanup so a force-terminated
// session emits the same signals exactly once as a REST-terminated one.
type TerminalHook interface {
	// fromState is the orphan's pre-terminal state, captured before the
	// reconciler wrote the `failed` row, so the terminal pod-release path can
	// distinguish a pre-running claimed session from a running one (§4.6).
	OnSessionTerminal(ctx context.Context, fromState session.State, sess sessionstore.Session)
}

// MetricsSink receives the §16.1 orphan-session observations. A nil
// sink disables emission. spec: §10.1 line 51 / §16.1.
type MetricsSink interface {
	// IncOrphanSessionReconciliation bumps
	// lenny_orphan_session_reconciliations_total once per forced
	// transition.
	IncOrphanSessionReconciliation()
	// SetAgentPodStateMirrorLag publishes the per-pool
	// lenny_agent_pod_state_mirror_lag_seconds gauge.
	SetAgentPodStateMirrorLag(poolID string, seconds float64)
}

// Options configures a Reconciler. A zero field selects its §10.1
// default.
type Options struct {
	// Interval overrides DefaultInterval.
	Interval time.Duration
	// StaleMirrorThreshold overrides DefaultStaleMirrorThreshold.
	StaleMirrorThreshold time.Duration
	// Fallback, when set, is the direct-Kubernetes phase reader used for
	// stale-mirror / missing-row sessions.
	Fallback PodPhaseReader
	// Terminal, when set, runs the gateway terminal pipeline on each
	// forced transition.
	Terminal TerminalHook
	// Metrics, when set, receives the §16.1 observability hooks.
	Metrics MetricsSink
	// Clock overrides time.Now (UTC).
	Clock func() time.Time
}

// Reconciler runs the periodic §10.1 orphan-session reconcile.
type Reconciler struct {
	sessions   sessionstore.Store
	tenants    TenantLister
	mirror     MirrorReader
	fallback   PodPhaseReader
	terminal   TerminalHook
	metrics    MetricsSink
	interval   time.Duration
	staleAfter time.Duration
	clock      func() time.Time
}

// New returns a Reconciler. mirror is required; the rest are optional
// per Options.
func New(sessions sessionstore.Store, tenants TenantLister, mirror MirrorReader, opts Options) *Reconciler {
	r := &Reconciler{
		sessions:   sessions,
		tenants:    tenants,
		mirror:     mirror,
		fallback:   opts.Fallback,
		terminal:   opts.Terminal,
		metrics:    opts.Metrics,
		interval:   opts.Interval,
		staleAfter: opts.StaleMirrorThreshold,
		clock:      opts.Clock,
	}
	if r.interval <= 0 {
		r.interval = DefaultInterval
	}
	if r.staleAfter <= 0 {
		r.staleAfter = DefaultStaleMirrorThreshold
	}
	if r.clock == nil {
		r.clock = func() time.Time { return time.Now().UTC() }
	}
	return r
}

// Tick runs one reconcile pass and returns the count of sessions it
// forced to `failed`. It enumerates every tenant's non-terminal sessions
// with an active pod binding, resolves each bound pod's §6.2 phase from
// the mirror (or the Kubernetes fallback when the mirror is stale or
// missing), and transitions the session when the pod has terminated.
// A per-pool mirror-lag gauge is published once per pool per Tick.
func (r *Reconciler) Tick(ctx context.Context) (int, error) {
	tenants, err := r.tenants.ListTenants(ctx)
	if err != nil {
		return 0, err
	}
	// lagByPool caches MirrorLagSeconds per pool for this Tick so the
	// gauge is published once per pool and the staleness decision is
	// consistent across a pool's sessions.
	lagByPool := map[string]float64{}
	failed := 0
	for _, tenantID := range tenants {
		rows, err := r.sessions.List(ctx, tenantID, sessionstore.ListFilter{})
		if err != nil {
			return failed, err
		}
		for _, row := range rows {
			if !eligible(row) {
				continue
			}
			orphaned, err := r.isOrphaned(ctx, row, lagByPool)
			if err != nil {
				// A transient mirror / Kubernetes read error must not
				// abort the whole sweep or, worse, force a false
				// transition. Log and move on; the next Tick reattempts.
				log.Printf("orphansession: resolve pod phase for session %s: %v", row.ID, err)
				continue
			}
			if !orphaned {
				continue
			}
			if r.failOrphan(ctx, tenantID, row) {
				failed++
			}
		}
	}
	return failed, nil
}

// eligible reports whether a session row is a §10.1 line 51 orphan
// candidate: a non-terminal state in the with-pod set, carrying an
// active pod binding.
func eligible(row sessionstore.Session) bool {
	if row.PodAssignment == "" {
		return false
	}
	if session.IsTerminal(row.State) {
		return false
	}
	return orphanEligibleStates[row.State]
}

// poolOf returns the pool a session's pod belongs to, preferring the
// gateway-recorded PoolRef and falling back to the client-requested
// Pool. Empty when neither is set.
func poolOf(row sessionstore.Session) string {
	if row.PoolRef != "" {
		return row.PoolRef
	}
	return row.Pool
}

// isOrphaned resolves whether the session's bound pod has terminated.
// It prefers the mirror; when the pool's mirror lag exceeds the stale
// threshold, or the mirror carries no row for the bound pod, it falls
// back to a direct Kubernetes read. lagByPool memoizes per-pool lag for
// the Tick and drives the per-pool gauge emission.
func (r *Reconciler) isOrphaned(ctx context.Context, row sessionstore.Session, lagByPool map[string]float64) (bool, error) {
	pool := poolOf(row)
	lag, haveLag := r.poolLag(ctx, pool, lagByPool)

	// A pool whose mirror is demonstrably stale is not trusted: jump
	// straight to the Kubernetes fallback for this session.
	if haveLag && lag > r.staleAfter.Seconds() {
		return r.fallbackOrphaned(ctx, row, pool)
	}

	pod, found, err := r.mirror.GetByPodID(ctx, row.PodAssignment)
	if err != nil {
		return false, err
	}
	if !found {
		// No mirror row for a session that still claims a pod: the pod
		// may have been pruned (terminated) or the mirror is merely cold
		// for this pod. Resolve authoritatively via Kubernetes rather
		// than guess. spec: §10.1 line 51 — "ensuring orphan detection
		// is not silently blocked by stale mirror data".
		return r.fallbackOrphaned(ctx, row, pool)
	}
	return pod.Phase == podPhaseTerminated, nil
}

// poolLag returns the pool's mirror lag, computing and caching it on
// first use this Tick and publishing the per-pool gauge. The bool is
// false when pool is empty (no key to measure) or the lag read fails.
func (r *Reconciler) poolLag(ctx context.Context, pool string, lagByPool map[string]float64) (float64, bool) {
	if pool == "" {
		return 0, false
	}
	if lag, ok := lagByPool[pool]; ok {
		return lag, true
	}
	lag, err := r.mirror.MirrorLagSeconds(ctx, pool)
	if err != nil {
		// A lag read failure is treated as "lag unknown": fall through
		// to the mirror row read rather than forcing the fallback path.
		log.Printf("orphansession: mirror lag for pool %s: %v", pool, err)
		return 0, false
	}
	lagByPool[pool] = lag
	if r.metrics != nil {
		r.metrics.SetAgentPodStateMirrorLag(pool, lag)
	}
	return lag, true
}

// fallbackOrphaned consults the direct-Kubernetes phase reader. A
// missing Sandbox (found=false) means the pod is gone, a terminal
// signal. A read error propagates so the caller skips this session
// rather than forcing a false transition. With no fallback wired the
// reconciler conservatively reports not-orphaned.
func (r *Reconciler) fallbackOrphaned(ctx context.Context, row sessionstore.Session, pool string) (bool, error) {
	if r.fallback == nil {
		return false, nil
	}
	phase, found, err := r.fallback.PodPhase(ctx, row.ID, row.PodAssignment, pool)
	if err != nil {
		return false, err
	}
	if !found {
		return true, nil
	}
	return phase == podPhaseTerminated, nil
}

// failOrphan transitions an orphaned session to `failed` with the §10.1
// reason and runs the terminal pipeline. The Update mutator no-ops when
// a concurrent writer already drove the row terminal, so two replicas
// running this pass cannot double-fail a session. Returns true when this
// call performed the transition.
func (r *Reconciler) failOrphan(ctx context.Context, tenantID string, row sessionstore.Session) bool {
	updated, err := r.sessions.Update(ctx, tenantID, row.ID, func(s *sessionstore.Session) error {
		if session.IsTerminal(s.State) {
			return nil // concurrent terminal transition — leave it alone
		}
		s.State = session.StateFailed
		s.FailureClass = session.FailureClassRuntime
		s.FailureReason = orphanReason
		return nil
	})
	if err != nil {
		log.Printf("orphansession: fail orphan session %s: %v", row.ID, err)
		return false
	}
	if updated.State != session.StateFailed || updated.FailureReason != orphanReason {
		// A concurrent writer won the terminal transition; this Tick did
		// not fail it, so do not double-count or re-run side effects.
		return false
	}
	if r.metrics != nil {
		r.metrics.IncOrphanSessionReconciliation()
	}
	if r.terminal != nil {
		// row.State is the pre-terminal state captured before the failed write
		// (§4.6); the §10.1 orphan path only fails post-attached (running)
		// sessions, so this routes their teardown through the executor recycle
		// path rather than the pre-running by-name reclaim.
		r.terminal.OnSessionTerminal(ctx, row.State, updated)
	}
	return true
}

// Run drives Tick on the configured interval until ctx is done. onTick,
// when non-nil, receives each pass's failed-count and error. A Tick
// error is logged and the loop continues — a transient store error must
// not stop orphan reconciliation.
func (r *Reconciler) Run(ctx context.Context, onTick func(int, error)) {
	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			n, err := r.Tick(ctx)
			if err != nil && ctx.Err() == nil {
				log.Printf("orphansession: reconcile pass failed: %v", err)
			}
			if onTick != nil {
				onTick(n, err)
			}
		}
	}
}
