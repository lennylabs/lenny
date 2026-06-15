// SPDX-License-Identifier: MIT

package warmpool

import (
	"context"
	"fmt"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	ctrlmetrics "sigs.k8s.io/controller-runtime/pkg/metrics"

	"github.com/lennylabs/lenny/pkg/admission/ownership"
	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
	"github.com/lennylabs/lenny/pkg/observability/metrics"
	sandboxcond "github.com/lennylabs/lenny/pkg/sandbox/condition"
	"github.com/lennylabs/lenny/pkg/sandbox/state"
	claimstate "github.com/lennylabs/lenny/pkg/sandboxclaim/state"
)

// defaultClaimOrphanTimeout is the §4.6.1 default for --claim-orphan-timeout:
// a SandboxClaim whose orphan key is older than this with no active session
// is treated as orphaned. spec: §4.6.1 "Orphaned SandboxClaim detection" —
// default 5 minutes; §11.5 controls table records the same 300s default. The
// key is the binding-state-transition time for a claim that has reached a
// binding state, and metadata.creationTimestamp for an empty-status claim
// (the CREATE-before-status fallback).
const defaultClaimOrphanTimeout = 5 * time.Minute

// defaultReservedHoldGrace is the §4.6.1 grace period the orphan GC adds to a
// reserved claim's holdExpiresAt before reclaiming it. spec: §4.6.1 predicate
// 2 — "reclaimed once holdExpiresAt plus a grace period has passed". The grace
// keeps the orphan GC from racing the gateway's own hold-expiry DELETE on a
// live holder: the gateway deletes the claim at holdExpiresAt, so the GC waits
// an extra window before treating an undeleted reserved claim as a crashed
// holder. The spec fixes no value, so it is operator-tunable
// (ClaimGarbageCollector.ReservedHoldGrace) and defaults to 60 seconds, well
// above the default 10-second hold TTL.
const defaultReservedHoldGrace = 60 * time.Second

// defaultGCInterval is the §4.6.1 orphan-claim sweep cadence: the leader
// lists candidate claims every 60 seconds. spec: §4.6.1 "Orphaned
// SandboxClaim detection" — "every 60 seconds, the leader replica lists
// all SandboxClaim resources".
const defaultGCInterval = 60 * time.Second

// orphanedClaims is the §4.6.1 lenny_orphaned_claims_total counter:
// orphaned SandboxClaims reclaimed by the GarbageCollect loop, labeled by
// pool. spec: §16.1 metrics table; drives the SandboxClaimOrphanRateHigh
// alert. Registered against the controller-runtime registry so it is
// exposed on the controller's existing /metrics endpoint.
var orphanedClaims = func() *prometheus.CounterVec {
	c, err := metrics.NewCounter(prometheus.CounterOpts{
		Name: "lenny_orphaned_claims_total",
		Help: "Orphaned SandboxClaims reclaimed by the GarbageCollect loop.",
	}, []string{"pool"})
	if err != nil {
		panic(fmt.Sprintf("warmpool: build orphaned-claims counter: %v", err))
	}
	metrics.MustRegister(ctrlmetrics.Registry, c)
	return c
}()

// SessionLookup reports whether an active (non-terminal) session backs a
// SandboxClaim. spec: §4.6.1 — "the controller queries Postgres to check
// whether an active session references it. If no active session exists,
// the claim is reclaimed". The per-pod claim (§4.6.3) carries no session
// identifier, so the check keys on the pod through the Postgres
// `pod_assignment` binding (§3.2). The WarmPoolController depends only on
// this narrow contract so the GC loop stays decoupled from the session
// store package.
type SessionLookup interface {
	// PodHasActiveSession reports whether any non-terminal session is
	// bound to the named Sandbox pod. A pod with no live session reports
	// false (the gateway crashed before persisting the session, or every
	// session it served has reached a terminal state), so its orphaned
	// claim is reclaimable.
	PodHasActiveSession(ctx context.Context, sandboxRef string) (bool, error)
}

// ClaimGarbageCollector is the §4.6.1 GarbageCollect loop: a leader-elected
// runnable that reclaims orphaned SandboxClaims. A SandboxClaim carries no
// ownerReference (so the session survives pod deletion and can be
// reassigned), so Kubernetes never garbage-collects it; this loop does.
// Running it only in the elected leader keeps the per-tick API list load
// constant regardless of replica count, per the spec's rationale for
// owning orphan detection in the WarmPoolController rather than the
// gateway.
//
// The loop is binding-state-aware: it evaluates three predicates keyed to
// the claim's status.phase (§4.6.1). A live non-terminal claim (`bound` or
// `recycling`) whose binding-state transition is older than the orphan
// timeout with no active session is reclaimed by draining the pod, because
// the pod may be unscrubbed and returning it to idle would break the
// scrub-before-idle invariant. A `reserved` claim is reclaimed by a
// precondition-guarded DELETE after holdExpiresAt plus a grace period,
// which returns the scrubbed, SDK-warm pod to idle. An empty-status claim
// (the CREATE-before-status crash window) older than the orphan timeout
// from its creation time is reclaimed by draining.
type ClaimGarbageCollector struct {
	// Client is the controller-runtime client.
	Client client.Client
	// Sessions is the §4.6.1 active-session oracle. When nil the loop is
	// disabled: without a session source of truth the controller cannot
	// distinguish an orphan from a live claim and must never delete.
	Sessions SessionLookup
	// Namespaces is the set of agent namespaces whose SandboxClaims the
	// loop sweeps. An empty slice disables the loop.
	Namespaces []string
	// OrphanTimeout is the minimum age a live or empty-status claim's
	// orphan key must reach before it is a GC candidate. A non-positive
	// value selects defaultClaimOrphanTimeout.
	OrphanTimeout time.Duration
	// ReservedHoldGrace is the §4.6.1 grace period added to a reserved
	// claim's holdExpiresAt before reclaiming it. A non-positive value
	// selects defaultReservedHoldGrace.
	ReservedHoldGrace time.Duration
	// Interval is the sweep cadence. A non-positive value selects
	// defaultGCInterval.
	Interval time.Duration
	// Now returns the current time. When nil, time.Now is used. A field so
	// tests can pin the clock.
	Now func() time.Time
}

var (
	_ manager.Runnable               = (*ClaimGarbageCollector)(nil)
	_ manager.LeaderElectionRunnable = (*ClaimGarbageCollector)(nil)
)

// NeedLeaderElection reports that only the elected leader sweeps orphaned
// claims, so non-leader replicas do not multiply the API list load.
func (g *ClaimGarbageCollector) NeedLeaderElection() bool { return true }

// Start runs the orphan-claim sweep until ctx is cancelled. It sweeps once
// immediately on leadership acquisition, then every Interval. A sweep
// failure is logged and the loop continues so the next tick retries.
func (g *ClaimGarbageCollector) Start(ctx context.Context) error {
	logger := logf.FromContext(ctx).WithName("claim-gc")
	if g.Sessions == nil || len(g.Namespaces) == 0 {
		logger.Info("no session source or agent namespaces configured; orphan-claim GC disabled")
		return nil
	}
	interval := g.Interval
	if interval <= 0 {
		interval = defaultGCInterval
	}

	if err := g.sweep(ctx); err != nil {
		logger.Error(err, "initial orphan-claim sweep failed")
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
			if err := g.sweep(ctx); err != nil {
				logger.Error(err, "orphan-claim sweep failed")
			}
		}
	}
}

// reclaimDisposition is how the sweep reclaims one orphaned claim, selected
// by its binding state (§4.6.1 predicates).
type reclaimDisposition int

const (
	// reclaimSkip leaves the claim untouched: it is not yet a candidate
	// (younger than its orphan key) or it is a terminal disposition the
	// occupancy projection retires, not an orphan.
	reclaimSkip reclaimDisposition = iota
	// reclaimDrain drains the pod, then deletes the claim. It applies to
	// the live binding states (`bound`, `recycling`) and the empty-status
	// CREATE-before-status fallback: the pod may be unscrubbed, so returning
	// it to idle would break the scrub-before-idle invariant (§3.1/§5.2).
	reclaimDrain
	// reclaimReserved returns the scrubbed, SDK-warm pod to idle via a
	// precondition-guarded DELETE (podclaim.DeleteOnHoldExpiry semantics),
	// so a concurrent rebind from any gateway replica wins the race (§3.2).
	reclaimReserved
)

// sweep performs one orphan-detection pass across the agent namespaces. For
// each claim it evaluates the §4.6.1 binding-state predicates, checks for an
// active session keyed on the pod through the Postgres pod_assignment binding,
// and reclaims by draining or by the precondition-guarded reserved DELETE.
func (g *ClaimGarbageCollector) sweep(ctx context.Context) error {
	now := time.Now
	if g.Now != nil {
		now = g.Now
	}

	for _, ns := range g.Namespaces {
		var claims lennyv1.SandboxClaimList
		if err := g.Client.List(ctx, &claims, client.InNamespace(ns)); err != nil {
			return fmt.Errorf("list sandboxclaims in %s: %w", ns, err)
		}
		for i := range claims.Items {
			claim := &claims.Items[i]
			if err := g.evaluate(ctx, claim, now()); err != nil {
				logf.FromContext(ctx).Error(err, "evaluate orphaned claim failed",
					"claim", claim.Name)
			}
		}
	}
	return nil
}

// evaluate decides whether and how to reclaim one claim, then performs the
// reclaim. A claim being deleted, not yet aged into its orphan key, or backed
// by an active session is left untouched; the §4.6.1 active-session check
// keys on the pod (not a per-session claim name) and must succeed before any
// reclaim, so a transient session-lookup failure never deletes a live claim.
func (g *ClaimGarbageCollector) evaluate(ctx context.Context, claim *lennyv1.SandboxClaim, now time.Time) error {
	if !claim.DeletionTimestamp.IsZero() {
		return nil
	}
	disposition := g.classify(claim, now)
	if disposition == reclaimSkip {
		return nil
	}
	// Fail closed: only reclaim once Postgres confirms no live session backs
	// the pod. A lookup error skips the candidate so the next tick retries.
	active, err := g.Sessions.PodHasActiveSession(ctx, claim.Spec.SandboxRef)
	if err != nil {
		logf.FromContext(ctx).Error(err, "orphan-claim session lookup failed; skipping",
			"claim", claim.Name, "sandbox", claim.Spec.SandboxRef)
		return nil
	}
	if active {
		return nil
	}
	switch disposition {
	case reclaimDrain:
		return g.reclaimByDraining(ctx, claim)
	case reclaimReserved:
		return g.reclaimReserved(ctx, claim)
	default:
		return nil
	}
}

// classify maps a claim's binding state to the §4.6.1 reclaim predicate and
// orphan key. It returns reclaimSkip when the claim is not yet a candidate.
//
//   - bound or recycling (live non-terminal): orphan key is the
//     binding-state-transition time plus the orphan timeout; reclaim by
//     draining. This covers a coordinating-gateway crash during the
//     recycling scrub wait, where the claim is left in recycling with no
//     holdExpiresAt and no rewarmStartedAt, which neither the reserved
//     predicate nor the sdkConnectTimeoutSeconds watchdog would reach.
//   - reserved: orphan key is holdExpiresAt plus the reserved-hold grace;
//     reclaim by the precondition-guarded DELETE.
//   - empty/unset binding state: orphan key is metadata.creationTimestamp
//     plus the orphan timeout (the CREATE-before-status fallback); reclaim
//     by draining.
//   - released or failed (terminal): never an orphan. The occupancy
//     projection drains then terminates the pod from a terminal disposition;
//     the gateway deletes the terminal claim through the normal pod
//     termination path.
//
// spec: §4.6.1 (orphaned SandboxClaim detection, three binding-state
// predicates plus the creation-timestamp fallback), §3.3 (drain rather than
// return-to-idle for live states), §6.10 (recycling-with-no-holdExpiresAt
// reclaimed by draining).
func (g *ClaimGarbageCollector) classify(claim *lennyv1.SandboxClaim, now time.Time) reclaimDisposition {
	switch claimstate.State(claim.Status.Phase) {
	case claimstate.Bound, claimstate.Recycling:
		// The binding-state-transition time replaces creationTimestamp as the
		// orphan key for any claim that has reached a binding state, because a
		// per-pod claim's creation time marks the start of the whole occupancy
		// episode rather than the start of the orphan window. A claim that
		// reached a live binding state but carries no transition stamp falls
		// back to creationTimestamp so it can never be stranded.
		key := claim.CreationTimestamp.Time
		if t := claim.Status.BindingStateTransitionTime; t != nil {
			key = t.Time
		}
		if g.aged(key, now, g.orphanTimeout()) {
			return reclaimDrain
		}
		return reclaimSkip
	case claimstate.Reserved:
		// A reserved pod is scrubbed and SDK-warm, so returning it to idle is
		// safe. Reclaim only after holdExpiresAt plus the grace period, so the
		// GC does not race the gateway's own hold-expiry DELETE. A reserved
		// claim missing holdExpiresAt is a malformed status; fail closed to
		// the transition-time key so a crashed holder is still reclaimed.
		key := claim.CreationTimestamp.Time
		grace := g.reservedHoldGrace()
		if t := claim.Status.HoldExpiresAt; t != nil {
			key = t.Time
		} else if t := claim.Status.BindingStateTransitionTime; t != nil {
			key = t.Time
			grace = g.orphanTimeout()
		}
		if g.aged(key, now, grace) {
			return reclaimReserved
		}
		return reclaimSkip
	case claimstate.Released, claimstate.Failed:
		// Terminal disposition: the occupancy projection retires the pod; this
		// is not an orphan the GC reclaims.
		return reclaimSkip
	default:
		// Empty/unset binding state: the CREATE-before-status crash window. Key
		// on creationTimestamp because there is no binding-state transition yet.
		if g.aged(claim.CreationTimestamp.Time, now, g.orphanTimeout()) {
			return reclaimDrain
		}
		return reclaimSkip
	}
}

// aged reports whether key plus window is at or before now. A zero key (an
// unstamped time) is never aged, so a claim with no usable orphan key is left
// for a later sweep rather than reclaimed on a missing timestamp.
func (g *ClaimGarbageCollector) aged(key, now time.Time, window time.Duration) bool {
	if key.IsZero() {
		return false
	}
	return !now.Before(key.Add(window))
}

func (g *ClaimGarbageCollector) orphanTimeout() time.Duration {
	if g.OrphanTimeout > 0 {
		return g.OrphanTimeout
	}
	return defaultClaimOrphanTimeout
}

func (g *ClaimGarbageCollector) reservedHoldGrace() time.Duration {
	if g.ReservedHoldGrace > 0 {
		return g.ReservedHoldGrace
	}
	return defaultReservedHoldGrace
}

// reclaimByDraining reclaims a live or empty-status orphaned claim by draining
// the pod, then deleting the claim. Draining (rather than returning to idle)
// is fail-closed by necessity: the whole-pod scrub is adapter-executed and
// gateway-coordinated, the controller has no GatewayControl path to the pod,
// and returning a possibly-unscrubbed pod to idle would break the
// scrub-before-idle invariant. The drain precedes the DELETE so the pod is
// already retiring when the claim disappears; the occupancy projection then
// sees the draining phase and never pulls the pod back into the claimable set.
//
// spec: §4.6.1 (live-state and empty-status reclaim by draining), §3.3
// (drain rather than return-to-idle; fail-closed on a coordinating-gateway
// crash), §5.2 (scrub-before-idle invariant).
func (g *ClaimGarbageCollector) reclaimByDraining(ctx context.Context, claim *lennyv1.SandboxClaim) error {
	if err := g.drainSandbox(ctx, claim.Namespace, claim.Spec.SandboxRef); err != nil {
		return err
	}
	if err := g.Client.Delete(ctx, claim); err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("delete claim %s: %w", claim.Name, err)
	}
	g.recordReclaimed(ctx, claim)
	return nil
}

// reclaimReserved reclaims an orphaned reserved claim with a
// precondition-guarded DELETE (podclaim.DeleteOnHoldExpiry semantics): the
// DELETE carries the claim UID and the resourceVersion observed during the
// sweep, so a cross-replica rebind that landed after the list (changing the
// resourceVersion) makes the DELETE fail its precondition and the reclaim
// aborts, leaving the rebound claim intact. The pod was scrubbed and re-warmed
// before entering reserved, so the claim DELETE returns it to idle through the
// occupancy projection's reserved → idle edge and preserves the
// scrub-before-idle invariant.
//
// spec: §4.6.1 (precondition-guarded hold-expiry DELETE), §3.2
// (rebind-vs-hold-expiry race: a rebind that lands first aborts the reclaim).
func (g *ClaimGarbageCollector) reclaimReserved(ctx context.Context, claim *lennyv1.SandboxClaim) error {
	preconditions := client.Preconditions(metav1.Preconditions{
		UID:             &claim.UID,
		ResourceVersion: &claim.ResourceVersion,
	})
	del := &lennyv1.SandboxClaim{
		ObjectMeta: metav1.ObjectMeta{Name: claim.Name, Namespace: claim.Namespace},
	}
	switch err := g.Client.Delete(ctx, del, preconditions); {
	case err == nil:
		// The reserved → idle edge is the occupancy projection's, driven by
		// the claim DELETE; the GC does not patch the phase.
		g.recordReclaimed(ctx, claim)
		return nil
	case apierrors.IsNotFound(err):
		// Already deleted (the gateway's own hold-expiry DELETE, or a prior
		// sweep): not an error and not a reclaim this sweep performed.
		return nil
	case apierrors.IsConflict(err):
		// A rebind (or any other writer) changed the resourceVersion since the
		// sweep listed the claim: the precondition failed, the reclaim aborts,
		// and the rebound claim is left intact. spec: §3.2.
		logf.FromContext(ctx).Info("reserved-claim reclaim aborted: a rebind won the precondition race",
			"claim", claim.Name)
		return nil
	default:
		return fmt.Errorf("precondition-guarded delete of reserved claim %s: %w", claim.Name, err)
	}
}

// recordReclaimed records a successful reclaim on the orphaned-claims counter
// and in the Sandbox condition history. The condition write is best-effort:
// the reclaim already succeeded, so a condition-write failure is logged but
// does not undo it.
func (g *ClaimGarbageCollector) recordReclaimed(ctx context.Context, claim *lennyv1.SandboxClaim) {
	pool := poolFromSandboxRef(ctx, g.Client, claim.Namespace, claim.Spec.SandboxRef)
	orphanedClaims.WithLabelValues(pool).Inc()
	g.recordReclaimCondition(ctx, claim.Namespace, claim.Spec.SandboxRef)
}

// drainSandbox transitions a Sandbox to draining via an SSA status patch
// under the WarmPoolController field manager. The §4.6.3 retry policy applies
// on HTTP 409. A Sandbox already absent, already draining, or already terminal
// is a no-op. Draining from idle is intentional and fail-closed: a live-state
// or empty-status (CREATE-before-status crash) reclaim cannot know whether the
// crashed gateway scrubbed or even occupied the pod, so the pod is retired
// rather than re-pooled, preserving the scrub-before-idle invariant. Only a
// reserved-claim reclaim returns the pod to idle, through the separate
// precondition-guarded DELETE and the occupancy projection's reserved → idle
// edge, and never reaches this helper.
func (g *ClaimGarbageCollector) drainSandbox(ctx context.Context, namespace, sandboxName string) error {
	if sandboxName == "" {
		return nil
	}
	key := client.ObjectKey{Namespace: namespace, Name: sandboxName}
	return retryOnConflictSSA(ctx, func(int) error {
		var live lennyv1.Sandbox
		if err := g.Client.Get(ctx, key, &live); err != nil {
			return client.IgnoreNotFound(err)
		}
		if live.Status.Phase == string(state.Draining) || state.IsTerminal(state.State(live.Status.Phase)) {
			return nil
		}
		patch := &lennyv1.Sandbox{
			TypeMeta: metav1.TypeMeta{
				APIVersion: lennyv1.GroupVersion.String(),
				Kind:       "Sandbox",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      live.Name,
				Namespace: live.Namespace,
			},
		}
		patch.Status.Phase = string(state.Draining)
		patch.Status.PodName = live.Status.PodName
		patch.Status.NodeName = live.Status.NodeName
		patch.Status.PodIP = live.Status.PodIP
		patch.Status.ObservedGeneration = live.Generation
		return g.Client.Status().Patch(ctx, patch, client.Apply, client.FieldOwner(string(ownership.WarmPoolController)))
	})
}

// recordReclaimCondition records the orphan-claim reclaim in the Sandbox
// condition history so an operator can see the pod was reclaimed from an
// orphaned claim rather than completing a session. Best-effort: a condition
// write failure does not undo the reclaim.
//
// spec: §6.2 / §4.6.1 — OrphanClaimReclaimed condition on the reclaimed pod.
func (g *ClaimGarbageCollector) recordReclaimCondition(ctx context.Context, namespace, sandboxName string) {
	if sandboxName == "" {
		return
	}
	var live lennyv1.Sandbox
	if err := g.Client.Get(ctx, client.ObjectKey{Namespace: namespace, Name: sandboxName}, &live); err != nil {
		return
	}
	cond := metav1.Condition{
		Type:    sandboxcond.OrphanClaimReclaimed,
		Status:  metav1.ConditionTrue,
		Reason:  "OrphanedClaimCollected",
		Message: "orphaned SandboxClaim collected; pod reclaimed",
	}
	if err := sandboxcond.Apply(ctx, g.Client, &live, cond); err != nil {
		logf.FromContext(ctx).Error(err, "record orphan-claim condition", "sandbox", live.Name)
	}
}

// poolFromSandboxRef resolves the pool label of a claim's backing Sandbox
// for the orphaned-claims metric. A missing Sandbox or label yields
// "unknown" so the counter still increments under a stable series.
func poolFromSandboxRef(ctx context.Context, c client.Client, namespace, sandboxName string) string {
	if sandboxName == "" {
		return "unknown"
	}
	var sb lennyv1.Sandbox
	if err := c.Get(ctx, client.ObjectKey{Namespace: namespace, Name: sandboxName}, &sb); err != nil {
		return "unknown"
	}
	if pool := sb.Labels[LabelPool]; pool != "" {
		return pool
	}
	if sb.Spec.PoolRef != "" {
		return sb.Spec.PoolRef
	}
	return "unknown"
}
