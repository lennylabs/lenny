// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"log"
	"time"

	agentpodstatepg "github.com/lennylabs/lenny/pkg/agentpodstate/pgstore"
	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/clockinject"
	"github.com/lennylabs/lenny/pkg/gateway/billing/billingcheckpoint"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegationbudget"
	delegationbudgetpg "github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegationbudget/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegationtree/deadlock"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegationtree/orphancleanup"
	"github.com/lennylabs/lenny/pkg/gateway/operability/recommendations"
	"github.com/lennylabs/lenny/pkg/gateway/quota/quotacheckpoint"
	"github.com/lennylabs/lenny/pkg/gateway/quota/quotafailopen"
	"github.com/lennylabs/lenny/pkg/gateway/session/orphansession"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/podlifecycle"
	"github.com/lennylabs/lenny/pkg/quota"
	"github.com/lennylabs/lenny/pkg/redisconn"
)

// startReconcilerWorkers launches the §8.10 orphan-cleanup job, the §10.1
// orphan-session reconciler, the §8.8 subtree deadlock detector, the §10.1
// dual-store monitor, the §25.3 recommendation sampler, the §12.4 storage-
// quota recovery reconciler, and the §11.2 delegation-tree-budget and
// token-usage checkpoint loops. It is an extracted per-group step of the
// §4.1 background-worker stage.
//
// spec: §4.1 gateway background subsystems; §8.10 / §10.1 / §11.2 sweeps.
func (w *gatewayWiring) startReconcilerWorkers() {
	f := w.f
	agentNamespace := f.agentNamespace
	billingTokenCheckpointIntervalSeconds := f.billingTokenCheckpointIntervalSeconds
	delegationCascadeTimeoutSeconds := f.delegationCascadeTimeoutSeconds
	delegationNodeMemoryFootprintBytes := f.delegationNodeMemoryFootprintBytes
	maxDeadlockWaitSeconds := f.maxDeadlockWaitSeconds
	quotaSyncIntervalSeconds := f.quotaSyncIntervalSeconds
	// ----- §8.10 orphan-cleanup job -----
	orphanSweeper := orphancleanup.New(w.sessions, tenantsLister{w.tenants}, orphancleanup.Options{
		Archive: w.treeArchive,
		Clock:   clockinject.Now,
		// spec: §8.10 — operator-tunable cascade timeout. The
		// per-deploy cap is the Sweeper's wall-clock window an orphan may
		// persist past its root's terminal state. F-8.10.9.
		CascadeTimeout: time.Duration(*delegationCascadeTimeoutSeconds) * time.Second,
		// F-5.2.26: same terminal pipeline as the watchdog so an orphan
		// terminated by background sweep also releases its slot/pod.
		Terminal: w.sessionSrv,
		// spec: §8.10; §16.1 —
		// publish the cleanup-runs counter, the cumulative terminated
		// counter, the fleet-wide active gauge, and the per-tenant active
		// gauge so the §16.5 OrphanTasksPerTenantHigh alert evaluates.
		// F-8.10.7.
		Metrics: w.gwMetrics,
	})
	go orphanSweeper.Run(w.watchdogCtx, func(terminated int, err error) {
		if err != nil {
			log.Printf("lenny-gateway: orphan-cleanup sweep error: %v", err)
			return
		}
		if terminated > 0 {
			log.Printf("lenny-gateway: orphan-cleanup terminated %d sessions past the §8.10 cascade timeout",
				terminated)
		}
	})

	// ----- §10.1 orphan-session reconciler -----
	// Cross-references the §4.6.1 agent_pod_state mirror every 60s: a
	// non-terminal session whose bound pod reached the §6.2 `terminated`
	// phase (without writing a terminal event, because the coordinating
	// replica was lost) is forced to `failed`/orphan_pod_terminated so it
	// stops holding quota. When a pool's mirror is stale (lag > 60s) or
	// carries no row for the bound pod, the reconciler falls back to a
	// direct Sandbox read. Active only when the Postgres mirror is wired;
	// the transition is idempotent across replicas (the Update no-ops on a
	// concurrent terminal write), matching the orphan-cleanup precedent.
	// spec: §10.1. F-10.1.5.
	if w.pgPool != nil {
		orphanSessionOpts := orphansession.Options{
			Terminal: w.sessionSrv,
			Metrics:  w.gwMetrics,
			Clock:    clockinject.Now,
		}
		if w.clusterClient != nil && *agentNamespace != "" {
			orphanSessionOpts.Fallback = sandboxPhaseReader{
				mgr: &podlifecycle.AgentSandboxPodLifecycleManager{
					AgentSandboxPoolReader: podlifecycle.AgentSandboxPoolReader{
						Client:    w.clusterClient,
						Namespace: *agentNamespace,
					},
				},
				ns: *agentNamespace,
			}
		}
		orphanSessionReconciler := orphansession.New(w.sessions, tenantsLister{w.tenants},
			agentPodStateMirror{store: agentpodstatepg.New(w.pgPool)}, orphanSessionOpts)
		go orphanSessionReconciler.Run(w.watchdogCtx, func(failed int, err error) {
			if err != nil {
				log.Printf("lenny-gateway: §10.1 orphan-session reconcile error: %v", err)
				return
			}
			if failed > 0 {
				log.Printf("lenny-gateway: §10.1 reconciler failed %d orphaned sessions (orphan_pod_terminated)",
					failed)
			}
		})
		log.Printf("lenny-gateway: §10.1 orphan-session reconciler enabled (fallback=%t)",
			orphanSessionOpts.Fallback != nil)
	}

	// ----- §8.8 subtree deadlock detector -----
	// Periodically sweeps the live await edges (which session awaits which
	// children) and the request_input registry; when every non-terminal
	// task in a subtree is blocked, the root receives a deadlock_detected
	// event on its lenny/await_children poll, and if the deadlock is not
	// resolved within maxDeadlockWaitSeconds the detector fails the deepest
	// blocked tasks with DEADLOCK_TIMEOUT. Disabled when the timeout is
	// zero. spec: §8.8. F-8.8.6.
	if w.deadlockManager != nil {
		deadlockLookup := func(ctx context.Context, tenantID, sessionID string) (session.State, bool) {
			row, err := w.sessions.Get(ctx, tenantID, sessionID)
			if err != nil {
				// Row gone (reclaimed) — the detector treats it as settled.
				return "", false
			}
			return row.State, true
		}
		deadlockFail := func(ctx context.Context, tenantID, sessionID string) {
			var fromState session.State
			updated, err := w.sessions.Update(ctx, tenantID, sessionID, func(s *sessionstore.Session) error {
				if session.IsTerminal(s.State) {
					return nil // a concurrent terminal transition won the race
				}
				fromState = s.State
				s.State = session.StateFailed
				s.FailureClass = session.FailureClassRuntime
				s.FailureReason = string(session.FailureDeadlockTimeout)
				return nil
			})
			if err != nil {
				log.Printf("lenny-gateway: §8.8 deadlock-timeout fail %s: %v", sessionID, err)
				return
			}
			if updated.State == session.StateFailed &&
				updated.FailureReason == string(session.FailureDeadlockTimeout) {
				// spec: §4.6 — a deadlock-timeout fails a blocked running
				// session, so fromState routes its teardown through the §6.2
				// executor recycle path rather than the pre-running reclaim.
				w.sessionSrv.OnSessionTerminal(ctx, fromState, updated)
			}
		}
		deadlockDetector := deadlock.NewDetector(w.deadlockManager, w.deadlockTracker, w.inputWaits,
			deadlockLookup, deadlockFail)
		go deadlockDetector.Run(w.watchdogCtx, 10*time.Second)
		log.Printf("lenny-gateway: §8.8 subtree deadlock detector enabled (maxDeadlockWaitSeconds=%d)",
			*maxDeadlockWaitSeconds)
	}

	// ----- §10.1 dual-store degraded-mode monitor -----
	// Probes Postgres + Redis on a short cadence; on detecting both
	// unreachable it pins lenny_dual_store_unavailable=1, broadcasts
	// PLATFORM_DEGRADED to active SSE streams, and gates session.create
	// (via DualStore on the session-server). Active only when both stores
	// are wired. F-10.1.3.
	if w.dsMonitor != nil {
		go w.dsMonitor.Run(w.watchdogCtx)
	}

	// ----- §25.3 capacity-recommendation metric sampler -----
	// Reads the recommendation source metrics out of the gateway's
	// in-process Prometheus registry into the WindowStore the rules engine
	// evaluates against, so /v1/admin/recommendations serves real
	// per-replica data instead of a permanently-empty result. Metrics that
	// originate in another process (warm-pool exhaustion from the
	// controller, kubelet OOM kills) are absent from the gateway registry
	// and are served by lenny-ops through its Prometheus reader — the
	// §25.3 per-replica-scope note. F-25.3.20.
	go recommendations.NewSampler(w.gwMetrics.Gatherer(), w.recommendationStore).Run(w.watchdogCtx)

	// ----- §12.4 storage-quota recovery reconciler -----
	// Probes Redis reachability; on a recovery edge it writes each
	// tenant's storage_bytes_used counter back to Redis from the
	// authoritative SUM(artifact_size_bytes) in Postgres so the Lua fast
	// path resumes enforcing against the correct value rather than a
	// stale-zero counter left by a Redis restart. Active only when both
	// Redis and the Postgres artifact catalog are wired. F-12.4.11.
	if w.storageRecoveryReconciler != nil {
		go w.storageRecoveryReconciler.Run(w.watchdogCtx)
	}

	// ----- §11.2 delegation tree budget checkpoint + reconstruction -----
	// On the quotaSyncIntervalSeconds cadence the reconciler persists each
	// active tree's Redis dlg:* counters to the delegation_tree_budget
	// table (§11.2); on a Redis-recovery edge it reconstructs each
	// checkpointed tree's counters to max(postgres_checkpoint, live) per
	// axis before new delegations resume (§11.2 / §12.4),
	// moving a tree whose checkpoint is stale and whose live state cannot
	// be enumerated to awaiting_client_action. Active only when the
	// delegation Redis counters, the Postgres pool, and the SessionStore
	// are all wired. F-11.2.5 / F-12.4.8.
	if w.treeBudgetConcrete != nil && w.pgPool != nil && w.sessions != nil {
		w.delegationBudgetReconciler = &delegationbudget.Reconciler{
			Probe: func(ctx context.Context) bool {
				return redisconn.PingWithTimeout(w.redisClient, 2*time.Second) == nil
			},
			Counters:        delegationbudget.CounterAdapter{Reserver: w.treeBudgetConcrete},
			Trees:           delegationbudget.SessionTreeLister{Sessions: w.sessions, Tenants: (tenantsLister{w.tenants}).ListTenants},
			Store:           delegationbudgetpg.New(w.pgPool),
			Live:            delegationbudget.SessionEnumerator{Sessions: w.sessions},
			Marker:          delegationbudget.SessionUnrecoverableMarker{Sessions: w.sessions},
			Metrics:         w.gwMetrics,
			Interval:        time.Duration(quota.ClampSyncIntervalSeconds(*quotaSyncIntervalSeconds)) * time.Second,
			NodeMemoryBytes: *delegationNodeMemoryFootprintBytes,
			Now:             clockinject.Now,
			Logf:            log.Printf,
		}
		log.Printf("lenny-gateway: §11.2 delegation tree budget checkpoint cadence %s (node footprint %d bytes)",
			time.Duration(quota.ClampSyncIntervalSeconds(*quotaSyncIntervalSeconds))*time.Second, *delegationNodeMemoryFootprintBytes)
		go w.delegationBudgetReconciler.Run(w.watchdogCtx)
	}

	// ----- §11.2 token-usage checkpoint + reconcile loop -----
	// On the quotaSyncIntervalSeconds cadence the reconciler persists each
	// active window total to token_usage_checkpoint (§11.2); on a
	// Redis-recovery edge it restores every still-current counter to
	// MAX(redis_current, postgres_checkpoint) before the next checkpoint
	// (§11.2). The same Service backs the §24.6 operator reconcile
	// and the session-completion final write wired above. F-11.2.4.
	if w.quotaCheckpointSvc != nil {
		quotaCheckpointReconciler := &quotacheckpoint.Reconciler{
			Probe: func(ctx context.Context) bool {
				return redisconn.PingWithTimeout(w.redisClient, 2*time.Second) == nil
			},
			Service:  w.quotaCheckpointSvc,
			Interval: time.Duration(quota.ClampSyncIntervalSeconds(*quotaSyncIntervalSeconds)) * time.Second,
		}
		log.Printf("lenny-gateway: §11.2 token-usage checkpoint cadence %s",
			time.Duration(quota.ClampSyncIntervalSeconds(*quotaSyncIntervalSeconds))*time.Second)
		go quotaCheckpointReconciler.Run(w.watchdogCtx)
	}

	// spec: §11.2.1 token_usage.checkpoint — periodically snapshot each
	// active session's proxy-recorded token delta into the per-tenant
	// billing stream so in-flight cost attribution is visible before
	// session end. Runs only when billing, the session store, and the
	// per-session usage accumulator are all wired. F-11.2.1.
	if billingTokenCP := billingcheckpoint.New(
		w.billingEmitter,
		billingSessionLister{sessions: w.sessions, tenants: (tenantsLister{w.tenants}).ListTenants},
		w.sessionUsage,
	); billingTokenCP != nil && *billingTokenCheckpointIntervalSeconds > 0 {
		interval := time.Duration(*billingTokenCheckpointIntervalSeconds) * time.Second
		log.Printf("lenny-gateway: §11.2.1 token_usage.checkpoint cadence %s", interval)
		go billingTokenCP.Run(w.watchdogCtx, interval)
	}

	// §12.4 source (2): bound the in-memory fail-open accumulator by dropping
	// entries whose window has rolled. Reads already ignore stale windows;
	// this reclaims their memory on a low cadence. F-12.4.20.
	if w.quotaFailOpenAccum != nil {
		go func(acc *quotafailopen.Accumulator) {
			tick := time.NewTicker(1 * time.Minute)
			defer tick.Stop()
			for {
				select {
				case <-w.watchdogCtx.Done():
					return
				case now := <-tick.C:
					acc.Sweep(now.UTC())
				}
			}
		}(w.quotaFailOpenAccum)
	}

	// spec: §12.4 — in the in_memory_reconciled mode, drive the
	// per-replica budget-slice reconcile loop ("reconciles with Postgres
	// periodically (default: every 30s)") and the final flush on shutdown.
	// The Redis checkpoint Reconciler above does not run in this mode
	// (quotaCounter is nil), so the budget tracker owns the tenant rollup
	// checkpoint rows.
	if w.quotaBudgetTracker != nil {
		go w.quotaBudgetTracker.Run(w.watchdogCtx)
	}
}
