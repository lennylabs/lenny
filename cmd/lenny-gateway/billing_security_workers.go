// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"log"
	"time"

	"github.com/lennylabs/lenny/pkg/clockinject"
	"github.com/lennylabs/lenny/pkg/gateway/billing/billingretention"
	"github.com/lennylabs/lenny/pkg/gateway/checkpoint/checkpointer"
	idempgstore "github.com/lennylabs/lenny/pkg/gateway/middleware/idempotency/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/storage/issuedtokenstore"
	"github.com/lennylabs/lenny/pkg/idempotency"
	"github.com/lennylabs/lenny/pkg/pgwritemetrics"
)

// startBillingAndSecurityWorkers launches the §10.1 coordination-lease
// sweeper, the §11.6 circuit-breaker cache refresh, the §11.2.1 billing
// failover flushers and retention pruner, the §12.3 Postgres write-IOPS
// sampler, the §4.9 / §10.3 / §13.3 security-cache pub/sub subscribers, the
// §4.9 lease-renewal sweep, the §4.4 checkpoint and freshness-reaper loops,
// the §13.3 revocation-cache rehydration, and the §4.9 credential deny-list
// startup rebuild. It is an extracted per-group step of the §4.1
// background-worker stage.
//
// spec: §4.1 gateway background subsystems; §11.2.1 / §11.6 / §13.3 loops.
func (w *gatewayWiring) startBillingAndSecurityWorkers() {
	f := w.f
	billingFlushIntervalMs := f.billingFlushIntervalMs
	billingRetentionDays := f.billingRetentionDays
	checkpointInterval := f.checkpointInterval
	idempotencyGCIntervalSeconds := f.idempotencyGCIntervalSeconds
	credentialLeaseGCIntervalSeconds := f.credentialLeaseGCIntervalSeconds
	postgresWriteIopsSampleSeconds := f.postgresWriteIopsSampleSeconds
	// ----- §10.1 session-coordination lease sweeper -----
	// Active only with Redis: it renews this replica's lease on every
	// non-terminal session so a crashed replica's sessions free up.
	if w.coordinator != nil {
		go w.coordinator.Run(w.watchdogCtx)
	}

	// ----- §11.6 circuit-breaker cache refresh -----
	// Active only with Redis: keeps the local open-breaker snapshot
	// current via pub/sub and a periodic refresh.
	if w.breakerCache != nil {
		go w.breakerCache.Run(w.watchdogCtx)
	}

	// ----- §11.2.1 billing failover Tier 2 flusher -----
	// Drains the in-memory write-ahead buffer into the primary billing
	// ledger once Postgres connectivity is restored, preserving the
	// monotonic ordering guarantee.
	go w.billingPipeline.RunFlusher(w.watchdogCtx)

	// ----- §11 billing failover Tier 1 per-tenant flusher -----
	// When the Tier 1 stream is Redis-backed, a per-tenant flusher
	// goroutine drains each tenant's billing stream back into Postgres
	// after a transient Postgres outage and runs the startup
	// fast-recovery XAUTOCLAIM that claims entries a predecessor replica
	// left. Without it the stream accumulates until billingStreamTTLSeconds
	// and the events are lost. The manager reconciles the per-tenant
	// goroutine set against the tenant store on its own interval.
	// F-11.2.8.
	if w.billingTier != nil {
		flushInterval := time.Duration(*billingFlushIntervalMs) * time.Millisecond
		mgr := w.billingTier.NewFlusherManager(tenantsLister{w.tenants}, flushInterval, 0)
		go mgr.Run(w.watchdogCtx)
		log.Printf("lenny-gateway: §11.2.1 billing failover Tier 1 per-tenant flusher started (flush every %s)", flushInterval)
	}

	// ----- §12.3 Postgres write-IOPS sampler -----
	// Periodically differentiates the pg_stat_database row-write total
	// into a sustained write-IOPS rate and publishes
	// lenny_postgres_write_iops so the §16.5 PostgresWriteSaturation
	// alert has a numerator. Only the Postgres-backed deployment has a
	// pool to sample. F-12.3.7.
	if w.pgPool != nil {
		pool := w.pgPool
		sampler := pgwritemetrics.New(func(ctx context.Context) (uint64, error) {
			// §12.3 write sources are row-level inserts/updates/
			// deletes; pg_stat_database aggregates them per database.
			var n int64
			if err := pool.QueryRow(ctx,
				`SELECT COALESCE(SUM(tup_inserted + tup_updated + tup_deleted), 0)::bigint
				 FROM pg_stat_database WHERE datname = current_database()`).Scan(&n); err != nil {
				return 0, err
			}
			if n < 0 {
				n = 0
			}
			return uint64(n), nil
		}, w.gwMetrics, clockinject.Now)
		go sampler.Start(w.watchdogCtx, time.Duration(*postgresWriteIopsSampleSeconds)*time.Second)
	}

	// ----- §11.2.1 billing retention pruner -----
	// Periodically deletes billing events past the configured
	// billing.retentionDays window across every registered tenant. The
	// DELETE is idempotent, so running it on every replica is safe (a
	// replica that loses the race prunes zero rows). Best-effort: a
	// per-tenant failure is logged and the sweep continues.
	// spec: §11.2.1. F-11.2.15.
	billingPruner := billingretention.New(w.billing, tenantsLister{w.tenants}, billingretention.Options{
		RetentionDays: *billingRetentionDays,
		Clock:         clockinject.Now,
	})
	log.Printf("lenny-gateway: §11.2.1 billing retention pruner active (retention %d days)", billingPruner.RetentionDays())
	go billingPruner.Run(w.watchdogCtx, func(pruned int, err error) {
		if err != nil {
			log.Printf("lenny-gateway: §11.2.1 billing retention sweep error: %v", err)
			return
		}
		if pruned > 0 {
			log.Printf("lenny-gateway: §11.2.1 billing retention pruned %d events past the %d-day window",
				pruned, billingPruner.RetentionDays())
		}
	})

	// ----- §4.9 / §10.3 / §13.3 security-cache pub/sub subscribers -----
	// Active only with Redis: each subscribe loop applies a peer
	// replica's revocations and deny-list mutations onto this replica's
	// local cache, so a §13.3 token revocation, a §10.3 mTLS
	// certificate revocation, and a §4.9 credential revocation each
	// converge fleet-wide. With no Redis the caches stay per-replica.
	//
	// The §4.9 credential-lease revocation runs the credrenewal
	// propagator's subscriber rather than the bare credential-deny-list
	// propagator's: both subscribe to the same channel and apply onto
	// the same deny list, and the credrenewal propagator additionally
	// drops the renewal worker's tracked leases for a revoked
	// credential. Running both would double-deliver onto one deny list
	// for no gain, so only the superset subscriber runs.
	if w.securityBus != nil {
		go w.revProp.Run(w.watchdogCtx)
		go w.mtlsDenyProp.Run(w.watchdogCtx)
		go w.credRenewalProp.Run(w.watchdogCtx)
		// §11.4 step 2: apply a peer replica's full_revoke Terminate
		// request to this replica's pods. Wired only with warm-pod
		// placement (the propagator is nil otherwise). F-11.4.3.
		if w.userPodTerminateProp != nil {
			go w.userPodTerminateProp.Run(w.watchdogCtx)
		}
	}

	// ----- §4.9 Proactive Lease Renewal sweep -----
	// Active only with credential pools wired: the worker sweeps tracked
	// leases on its interval, issues a replacement before each lease's
	// renewBefore deadline, and pushes the rotated credential to the
	// lease's pod via the §4.7 RotateCredentials RPC.
	if w.credRenewalWorker != nil {
		go w.credRenewalWorker.Run(w.watchdogCtx)
	}

	// ----- §4.4 periodic-checkpoint loop -----
	// Active only with --agent-namespace: snapshots every coordinated
	// session's workspace on the checkpoint cadence so the §7.1
	// WorkspaceSnapshot stays fresh against the §16.5 freshness SLO.
	// The same checkpointer backs the §7.1 seal-and-export on the
	// session-completion path.
	if w.checkpointSvc != nil {
		go w.checkpointSvc.Run(w.watchdogCtx)
	}

	// ----- §4.4 freshness-reaper loop -----
	// Scans every active session and populates the per-(pool, level)
	// `lenny_checkpoint_stale_sessions` gauge so the §16.5
	// CheckpointStale alert can fire when any pool reports a non-zero
	// stale count for > 60 s. The reaper runs on every gateway
	// replica (not only the one with --agent-namespace) because the
	// freshness signal is platform-wide and the read path against the
	// session store is cheap.
	// spec: §4.4.
	freshnessReaper := &checkpointer.FreshnessReaper{
		Tenants:  tenantsLister{w.tenants},
		Sessions: w.sessions,
		Gauge:    w.gwMetrics,
		Interval: *checkpointInterval,
		OnError: func(tenantID string, err error) {
			if tenantID == "" {
				log.Printf("lenny-gateway: freshness reaper: list tenants: %v", err)
				return
			}
			log.Printf("lenny-gateway: freshness reaper: tenant %s: %v", tenantID, err)
		},
	}
	go freshnessReaper.Run(w.watchdogCtx)

	// ----- §13.3 revocation-cache rehydration -----
	// Loads revoked-token jtis from the issued-token index so a
	// revocation survives a restart and propagates across replicas.
	if w.pgPool != nil {
		issued := issuedtokenstore.New(w.pgPool)
		lister := tenantsLister{w.tenants}
		if err := w.revCache.Rehydrate(context.Background(), lister, issued); err != nil {
			log.Printf("lenny-gateway: initial revocation rehydration failed: %v", err)
		}
		go func() {
			ticker := time.NewTicker(30 * time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-w.watchdogCtx.Done():
					return
				case <-ticker.C:
					if err := w.revCache.Rehydrate(context.Background(), lister, issued); err != nil && w.watchdogCtx.Err() == nil {
						log.Printf("lenny-gateway: revocation rehydration failed: %v", err)
					}
				}
			}
		}()
	}

	// ----- §4.9 credential deny-list startup rebuild -----
	// Seeds the per-replica credential deny list from the union of the
	// pool store's and the token store's revoked entries, so a replica
	// started immediately after either a pool-credential or a
	// user-credential revocation denies that credential on the upstream
	// path even if it missed the original Redis pub/sub notification. The
	// rebuild is authoritative (Reset) and runs once at startup, seeding a
	// deny entry only for a revoked credential that still has an active
	// lease (the §4.9 lease-existence bound). It runs in a background
	// goroutine so runServers can bring up the /healthz and /readyz
	// listeners rather than blocking on a boot-time store retry; readiness
	// stays gated (credDenyRebuilt unset) until the Reset commits, so the
	// replica serves no proxy traffic against a retained revoked lease with
	// an incomplete deny list while /healthz stays live throughout.
	//
	// spec: §4.9.
	go w.runCredentialDenyListRebuild()

	// ----- §11.5 idempotency-key TTL garbage collection -----
	// Reclaims idempotency_keys rows past the 24-hour retention window
	// so the durable key cache stays bounded. The cadence is operator
	// tunable via --idempotency-gc-interval-seconds (default 3600s).
	if w.pgPool != nil {
		idemGC := idempgstore.New(w.pgPool)
		lister := tenantsLister{w.tenants}
		gcInterval := time.Duration(*idempotencyGCIntervalSeconds) * time.Second
		if gcInterval <= 0 {
			gcInterval = time.Hour
		}
		sweepIdempotencyKeys(context.Background(), idemGC, lister)
		go func() {
			ticker := time.NewTicker(gcInterval)
			defer ticker.Stop()
			for {
				select {
				case <-w.watchdogCtx.Done():
					return
				case <-ticker.C:
					sweepIdempotencyKeys(w.watchdogCtx, idemGC, lister)
				}
			}
		}()
	}

	// ----- §4.9 credential-lease expires_at backfill -----
	// One-time convergence pass for rows written before migration 0175, which
	// carry a NULL expires_at that the sweep below cannot treat as expired and
	// the rebuild filter counts as active. It fills the plain projection from
	// the decrypted lease body, or deletes the row when the lease is already
	// past expiry, so a pre-migration expired lease does not linger past its
	// TTL and keep its deny-list entry forever. Only the Postgres-backed store
	// carries the projection column; the in-memory store keeps ExpiresAt on the
	// struct and needs no backfill. It runs in a background goroutine so a large
	// backfill on an upgraded deployment does not block the /healthz and /readyz
	// listeners, mirroring the deny-list rebuild above.
	//
	// spec: §4.9 — a deny-list entry expires when the credential's
	// natural lease TTL lapses.
	w.startCredentialLeaseExpiresAtBackfill()

	// ----- §4.9 expired-lease sweep and deny-entry expiry -----
	// Each tick deletes credential_leases rows past ExpiresAt (bounding the
	// table) and then reconciles this replica's deny list, dropping a
	// credential's deny entry only once the store definitively reports no
	// remaining active lease against it. The reconcile fails closed: a
	// store error or a positive count keeps the entry, so a transient
	// Postgres or KMS fault never opens a CREDENTIAL_REVOKED bypass. Like
	// the §11.5 idempotency GC above it runs on every replica with no
	// leader gate, and the deny-entry removal is driven by this replica's
	// own Keys() rather than by the deleted rows: under the shared store
	// each expired row is deleted by exactly one replica, so a
	// deletion-derived removal set would bound only the winning replica's
	// list. Iterating each replica's own list bounds every replica's list
	// within its lifetime.
	//
	// spec: §4.9 — a deny-list entry expires when the
	// credential's natural lease TTL lapses.
	if w.llmLeases != nil && w.credDeny != nil {
		gcInterval := time.Duration(*credentialLeaseGCIntervalSeconds) * time.Second
		if gcInterval <= 0 {
			gcInterval = time.Hour
		}
		go runCredentialLeaseSweepLoop(w.watchdogCtx, w.llmLeases, w.credDeny, w.gwMetrics, gcInterval, clockinject.Now)
	}
}

// sweepIdempotencyKeys runs one §11.5 TTL garbage-collection pass,
// deleting idempotency_keys rows older than the 24-hour retention
// window. The sweep is per-tenant because the lenny_tenant_guard
// trigger fires for every DELETE.
func sweepIdempotencyKeys(ctx context.Context, gc *idempgstore.Store, lister tenantsLister) {
	tenants, err := lister.ListTenants(ctx)
	if err != nil {
		log.Printf("lenny-gateway: idempotency GC: listing tenants failed: %v", err)
		return
	}
	cutoff := clockinject.Now().Add(-idempotency.TTL)
	for _, tenant := range tenants {
		if _, err := gc.DeleteExpired(ctx, tenant, cutoff); err != nil && ctx.Err() == nil {
			log.Printf("lenny-gateway: idempotency GC: tenant %q sweep failed: %v", tenant, err)
		}
	}
}
