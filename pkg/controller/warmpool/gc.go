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
)

// defaultClaimOrphanTimeout is the §4.6.1 default for --claim-orphan-timeout:
// a SandboxClaim older than this with no active session is treated as
// orphaned. spec: §4.6.1 "Orphaned SandboxClaim detection" — default 5
// minutes; §11.5 controls table records the same 300s default.
const defaultClaimOrphanTimeout = 5 * time.Minute

// defaultGCInterval is the §4.6.1 orphan-claim sweep cadence: the leader
// lists candidate claims every 60 seconds. spec: §4.6.1 "Orphaned
// SandboxClaim detection" — "every 60 seconds, the leader replica lists
// all SandboxClaim resources".
const defaultGCInterval = 60 * time.Second

// orphanedClaims is the §4.6.1 lenny_orphaned_claims_total counter:
// orphaned SandboxClaims deleted by the GarbageCollect loop, labeled by
// pool. spec: §16.1 metrics table; drives the SandboxClaimOrphanRateHigh
// alert. Registered against the controller-runtime registry so it is
// exposed on the controller's existing /metrics endpoint.
var orphanedClaims = func() *prometheus.CounterVec {
	c, err := metrics.NewCounter(prometheus.CounterOpts{
		Name: "lenny_orphaned_claims_total",
		Help: "Orphaned SandboxClaims deleted by the GarbageCollect loop.",
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
// the claim is deleted". The WarmPoolController depends only on this
// narrow contract so the GC loop stays decoupled from the session store
// package.
type SessionLookup interface {
	// SessionActive reports whether a non-terminal session with the
	// given (tenantID, sessionID) exists. A missing session row reports
	// false (the gateway crashed before persisting the session), and a
	// session in a terminal state also reports false (it no longer needs
	// its claim).
	SessionActive(ctx context.Context, tenantID, sessionID string) (bool, error)
}

// ClaimGarbageCollector is the §4.6.1 GarbageCollect loop: a leader-elected
// runnable that reclaims orphaned SandboxClaims. A SandboxClaim carries no
// ownerReference (so the session survives pod deletion and can be
// reassigned), so Kubernetes never garbage-collects it; this loop does.
// Running it only in the elected leader keeps the per-tick API list load
// constant regardless of replica count, per the spec's rationale for
// owning orphan detection in the WarmPoolController rather than the
// gateway.
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
	// OrphanTimeout is the minimum age a claim must reach before it is a
	// GC candidate. A non-positive value selects defaultClaimOrphanTimeout.
	OrphanTimeout time.Duration
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

// sweep performs one orphan-detection pass across the agent namespaces.
func (g *ClaimGarbageCollector) sweep(ctx context.Context) error {
	now := time.Now
	if g.Now != nil {
		now = g.Now
	}
	timeout := g.OrphanTimeout
	if timeout <= 0 {
		timeout = defaultClaimOrphanTimeout
	}
	cutoff := now().Add(-timeout)

	for _, ns := range g.Namespaces {
		var claims lennyv1.SandboxClaimList
		if err := g.Client.List(ctx, &claims, client.InNamespace(ns)); err != nil {
			return fmt.Errorf("list sandboxclaims in %s: %w", ns, err)
		}
		for i := range claims.Items {
			claim := &claims.Items[i]
			if claim.CreationTimestamp.Time.After(cutoff) {
				// Younger than the orphan timeout: a gateway is still within
				// its window to persist the session, so the claim is not yet
				// a candidate.
				continue
			}
			if !claim.DeletionTimestamp.IsZero() {
				continue
			}
			active, err := g.Sessions.SessionActive(ctx, claim.Spec.TenantID, claim.Spec.SessionID)
			if err != nil {
				logf.FromContext(ctx).Error(err, "orphan-claim session lookup failed; skipping",
					"claim", claim.Name, "session", claim.Spec.SessionID)
				continue
			}
			if active {
				continue
			}
			if err := g.reclaim(ctx, claim); err != nil {
				logf.FromContext(ctx).Error(err, "reclaim orphaned claim failed",
					"claim", claim.Name)
				continue
			}
		}
	}
	return nil
}

// reclaim deletes one orphaned claim and transitions its backing Sandbox
// back to idle, returning the pod to the warm pool. spec: §4.6.1 — "the
// claim is deleted and the underlying Sandbox pod is transitioned back to
// idle". The orphaned-claims counter is incremented per reclaimed claim.
func (g *ClaimGarbageCollector) reclaim(ctx context.Context, claim *lennyv1.SandboxClaim) error {
	if err := g.Client.Delete(ctx, claim); err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("delete claim %s: %w", claim.Name, err)
	}
	if err := g.returnToIdle(ctx, claim.Namespace, claim.Spec.SandboxRef); err != nil {
		return err
	}
	pool := poolFromSandboxRef(ctx, g.Client, claim.Namespace, claim.Spec.SandboxRef)
	orphanedClaims.WithLabelValues(pool).Inc()
	return nil
}

// returnToIdle transitions a claimed Sandbox back to idle via an SSA
// status patch under the WarmPoolController field manager. The §4.6.3
// retry policy applies on HTTP 409. A Sandbox already absent, already
// idle, or being drained is a no-op.
func (g *ClaimGarbageCollector) returnToIdle(ctx context.Context, namespace, sandboxName string) error {
	if sandboxName == "" {
		return nil
	}
	key := client.ObjectKey{Namespace: namespace, Name: sandboxName}
	return retryOnConflictSSA(ctx, func(attempt int) error {
		var live lennyv1.Sandbox
		if err := g.Client.Get(ctx, key, &live); err != nil {
			return client.IgnoreNotFound(err)
		}
		if live.Status.Phase == string(state.Idle) || live.Status.Phase == string(state.Draining) {
			// Already idle, or being drained out of the pool: do not pull a
			// draining pod back into the claimable set.
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
		patch.Status.Phase = string(state.Idle)
		patch.Status.PodName = live.Status.PodName
		patch.Status.NodeName = live.Status.NodeName
		patch.Status.PodIP = live.Status.PodIP
		patch.Status.ObservedGeneration = live.Generation
		if err := g.Client.Status().Patch(ctx, patch, client.Apply, client.FieldOwner(string(ownership.WarmPoolController))); err != nil {
			return err
		}
		// spec: §6.2 line 305 / §4.6.1 — record the orphan-claim collection
		// in the Sandbox condition history so an operator can see the pod was
		// reclaimed from an orphaned claim rather than completing a session.
		// Best-effort: a condition write failure does not undo the reclaim.
		cond := metav1.Condition{
			Type:    sandboxcond.OrphanClaimReclaimed,
			Status:  metav1.ConditionTrue,
			Reason:  "OrphanedClaimCollected",
			Message: "orphaned SandboxClaim collected; pod returned to idle",
		}
		if err := sandboxcond.Apply(ctx, g.Client, &live, cond); err != nil {
			logf.FromContext(ctx).Error(err, "record orphan-claim condition", "sandbox", live.Name)
		}
		return nil
	})
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
