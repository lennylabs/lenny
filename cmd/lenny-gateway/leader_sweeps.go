// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"log"
	"os"
	"time"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	"github.com/lennylabs/lenny/pkg/clockinject"
	"github.com/lennylabs/lenny/pkg/gateway/checkpoint/checkpointer"
	"github.com/lennylabs/lenny/pkg/gateway/checkpoint/partialmanifeststore"
	"github.com/lennylabs/lenny/pkg/gateway/coordination/gatewayleader"
	"github.com/lennylabs/lenny/pkg/gateway/externalapi/admintoken/k8ssecret"
	admintokenreclaimer "github.com/lennylabs/lenny/pkg/gateway/externalapi/admintoken/reclaimer"
	"github.com/lennylabs/lenny/pkg/gateway/storage/issuedtokenstore"
	"github.com/lennylabs/lenny/pkg/gateway/storage/legalholdreconciler"
	"github.com/lennylabs/lenny/pkg/gateway/storage/partitionmaint"
	"github.com/lennylabs/lenny/pkg/gateway/storage/retentiongc"
	"github.com/lennylabs/lenny/pkg/tenantkms"
)

func (w *gatewayWiring) startLeaderElectedSweeps() {
	f := w.f
	gatewayLeaderElection := f.gatewayLeaderElection
	gatewayNamespace := f.gatewayNamespace
	gcCycleIntervalSeconds := f.gcCycleIntervalSeconds
	gcTombstoneRetentionSeconds := f.gcTombstoneRetentionSeconds
	gdprRetentionDays := f.gdprRetentionDays
	auditRetentionPruneIntervalSeconds := f.auditRetentionPruneIntervalSeconds
	eventBusDuplicateInjectionFactor := f.eventBusDuplicateInjectionFactor
	eventBusMaxRetryAttempts := f.eventBusMaxRetryAttempts
	eventBusRetryIntervalSeconds := f.eventBusRetryIntervalSeconds
	t4KmsProbeIntervalSeconds := f.t4KmsProbeIntervalSeconds
	t4KmsProbeRateLimit := f.t4KmsProbeRateLimit
	adminTokenDisabled := f.adminTokenDisabled
	adminTokenNamespace := f.adminTokenNamespace
	adminTokenSecretName := f.adminTokenSecretName
	adminTokenTenant := f.adminTokenTenant
	adminTokenReclaimIntervalSeconds := f.adminTokenReclaimIntervalSeconds
	maxResumePendingSeconds := f.maxResumePendingSeconds
	// ----- §12.5 gateway-leader election (lenny-gateway-leader Lease) -----
	// spec: §12.5 — the artifact-GC orchestrator and the
	// other gateway-singleton sweeps below (tombstone hard-prune,
	// audit-retention pruner, EventBus retranscribe worker, legal-hold
	// reconciler, T4 KMS probe) run under a single leader-elected
	// lenny-gateway-leader Lease so exactly one gateway replica is the GC
	// writer at a time, with the §4.6.1 25s crash-failover bound. The
	// per-row `WHERE deleted_at IS NULL` guards (rules 2-6) keep the bounded
	// failover window safe; this lease provides the steady-state
	// single-writer guarantee. When the gateway is not in-cluster (single-
	// process dev, tests, or `--gateway-leader-election=false`), the
	// AlwaysLeader fallback runs every sweep exactly as before.
	var leaderElector gatewayleader.Elector = gatewayleader.AlwaysLeader{}
	if *gatewayLeaderElection {
		if cfg, err := rest.InClusterConfig(); err != nil {
			log.Printf("lenny-gateway: §12.5 no in-cluster config (%v); GC sweeps run un-elected (single-replica mode)", err)
		} else if cs, err := kubernetes.NewForConfig(cfg); err != nil {
			log.Printf("lenny-gateway: §12.5 build leader-election clientset: %v; GC sweeps run un-elected", err)
		} else {
			identity := os.Getenv("POD_NAME")
			if identity == "" {
				if h, herr := os.Hostname(); herr == nil {
					identity = h
				}
			}
			le, lerr := gatewayleader.NewLeaseElector(*gatewayNamespace, identity,
				cs.CoreV1(), cs.CoordinationV1(), gatewayleader.LeaseTimings{})
			if lerr != nil {
				log.Printf("lenny-gateway: §12.5 build lenny-gateway-leader elector: %v; GC sweeps run un-elected", lerr)
			} else {
				leaderElector = le
				log.Printf("lenny-gateway: §12.5 gateway-singleton sweeps gated under the %s Lease in namespace %q (identity %q)",
					gatewayleader.LeaseName, *gatewayNamespace, identity)
			}
		}
	}
	// leaderGate collects every gateway-singleton sweep; registered jobs run
	// only while this replica holds the lease (or always, under AlwaysLeader).
	leaderGate := gatewayleader.NewGate(leaderElector)

	// ----- §7.1 artifact-retention GC -----
	// Collects the workspace snapshot, transcript, and blobs of every
	// terminal session past its retention TTL; a §12.8 legal hold
	// exempts the session.
	//
	// When the §12.5 artifact_store catalog is wired (Postgres path),
	// the blobs deleter transitions catalog rows to `soft_deleted` with
	// the §12.5 tombstone retention deadline rather than deleting them
	// outright. A background goroutine runs the §12.5 ll. 341 hard-prune
	// sweep on the same cadence so rows past their deadline (and their
	// matching bucket objects) are physically removed. In-memory dev
	// mode, where no catalog is wired, retains the legacy direct-delete
	// path.
	{
		// §12.5 gc.cycleIntervalSeconds: the GC sweep cadence,
		// clamped up to the spec's 60s floor so a misconfigured chart
		// value cannot make the leader-elected sweep busy-loop.
		rawGCInterval := time.Duration(*gcCycleIntervalSeconds) * time.Second
		gcInterval := retentiongc.ClampSweepInterval(rawGCInterval)
		if gcInterval != rawGCInterval && rawGCInterval > 0 {
			log.Printf("lenny-gateway: §12.5 gc.cycleIntervalSeconds=%d below the %ds floor; clamping to the minimum",
				*gcCycleIntervalSeconds, int(retentiongc.MinSweepInterval/time.Second))
		}
		// §12.5 gc.tombstoneRetentionSeconds: the window a
		// soft-deleted artifact_store row is retained before hard-prune.
		// This is distinct from the §7.1 artifact-retention TTL (when a
		// terminal session's artifacts become soft-delete-eligible).
		gcTombstoneRetention := time.Duration(*gcTombstoneRetentionSeconds) * time.Second
		var arts []retentiongc.Artifact
		if te, ok := w.transcripts.(sessionArtifactDeleter); ok {
			arts = append(arts, retentiongc.Artifact{Name: "transcripts", Delete: te.DeleteBySession})
		}
		if w.blobsCataloged != nil {
			// §12.5 ll. 311-313: soft-delete the catalog rows + bucket
			// objects through the cataloging decorator instead of
			// removing them outright. The hard-prune pass below runs on
			// the same Run loop and bumps lenny_gc_tombstones_pruned_total.
			// The tombstone deadline is now + gc.tombstoneRetentionSeconds.
			arts = append(arts, retentiongc.Artifact{
				Name: "artifacts",
				Delete: func(ctx context.Context, tenantID, sessionID string) (int, error) {
					return w.blobsCataloged.SoftDeleteSession(ctx, tenantID, sessionID, gcTombstoneRetention)
				},
			})
		} else if be, ok := w.blobs.(sessionArtifactDeleter); ok {
			arts = append(arts, retentiongc.Artifact{Name: "artifacts", Delete: be.DeleteBySession})
		}
		retGC := retentiongc.New(w.sessions, tenantsLister{w.tenants}, arts, retentiongc.Options{
			Interval: gcInterval,
			Clock:    clockinject.Now,
			Metrics:  w.gwMetrics,
		})
		log.Printf("lenny-gateway: §12.5 GC sweep cadence %s (gc.cycleIntervalSeconds=%d); tombstone retention %s (gc.tombstoneRetentionSeconds=%d)",
			gcInterval, int(gcInterval/time.Second), gcTombstoneRetention, int(gcTombstoneRetention/time.Second))
		// §12.5: bind the erasure-completion → immediate-sweep
		// trigger now that the collector exists. A `gcPriority: high`
		// tenant's expired artifacts are reclaimed the moment one of its
		// erasure jobs completes, independent of the global cycle. A normal
		// tenant takes no extra sweep. Best-effort: a lookup or sweep error
		// is logged but never propagated back into the completed job.
		w.immediateGCSweep = func(ctx context.Context, tenantID string) {
			t, err := w.tenants.Get(ctx, tenantID)
			if err != nil {
				log.Printf("lenny-gateway: §12.5 gcPriority lookup for tenant %q failed: %v", tenantID, err)
				return
			}
			if !t.TriggersImmediateGC() {
				return
			}
			collected, err := retGC.SweepTenant(ctx, tenantID, clockinject.Now())
			if err != nil {
				log.Printf("lenny-gateway: §12.5 gcPriority=high immediate sweep for tenant %q failed: %v", tenantID, err)
				return
			}
			log.Printf("lenny-gateway: §12.5 gcPriority=high immediate sweep for tenant %q collected %d session(s)", tenantID, collected)
		}
		leaderGate.Add("artifact-gc", func(ctx context.Context) {
			retGC.Run(ctx, func(collected int, err error) {
				if err != nil {
					log.Printf("lenny-gateway: retention-GC sweep error: %v", err)
					return
				}
				if collected > 0 {
					log.Printf("lenny-gateway: retention-GC collected artifacts for %d sessions past their §7.1 retention TTL",
						collected)
				}
			})
		})

		// §16.4 audit-retention pruner: only constructed
		// when a durable Postgres audit chain exists (the in-memory
		// gateway has nothing to prune). Production runs it under the
		// §10.1 leader lease alongside the artifact GC above. F-11.7.17.
		if w.auditPruner != nil {
			log.Printf("lenny-gateway: §16.4 audit-retention sweep cadence %s (retention %d days, gdpr.* %d days)",
				time.Duration(*auditRetentionPruneIntervalSeconds)*time.Second, w.effectiveAuditRetentionDays, *gdprRetentionDays)
			leaderGate.Add("audit-retention", func(ctx context.Context) {
				w.auditPruner.Run(ctx, func(pruned int, err error) {
					if err != nil {
						log.Printf("lenny-gateway: §16.4 audit-retention sweep error: %v", err)
						return
					}
					if pruned > 0 {
						log.Printf("lenny-gateway: §16.4 audit-retention sweep pruned %d audit rows past their retention window", pruned)
					}
				})
			})
		}

		// spec: §16.4 EventStore partition maintainer: the
		// leader-elected sweep that creates the current + ahead daily
		// partitions of session_logs / stream_cursors and drops partitions
		// whose entire range has aged past the §16.4 retention window (30
		// days for session logs, 7 days for stream cursors). Only wired when
		// a durable Postgres pool exists; the in-memory dev gateway has no
		// partitioned EventStore tables. audit_log stays on the DELETE-based
		// pruner above (native partitioning of audit_log conflicts with the
		// §12.8 audit_redaction_receipts FK onto audit_log.id; see
		// BUILD-GAPS F-16.4.6).
		if w.pgPool != nil {
			partMaint := partitionmaint.New(
				partitionmaint.NewPGDriver(w.pgPool),
				[]partitionmaint.Spec{
					{Table: "session_logs", Granularity: partitionmaint.Daily, Retention: partitionmaint.SessionLogRetention},
					{Table: "stream_cursors", Granularity: partitionmaint.Daily, Retention: partitionmaint.StreamCursorRetention},
				},
				partitionmaint.Options{Clock: clockinject.Now},
			)
			log.Printf("lenny-gateway: §16.4 EventStore partition maintainer cadence %s (session_logs 30d, stream_cursors 7d)",
				partMaint.Interval())
			leaderGate.Add("eventstore-partition-maint", func(ctx context.Context) {
				partMaint.Run(ctx, func(res []partitionmaint.Result, err error) {
					if err != nil {
						log.Printf("lenny-gateway: §16.4 EventStore partition maintenance error: %v", err)
						return
					}
					for _, r := range res {
						if len(r.Created) > 0 || len(r.Dropped) > 0 || len(r.Held) > 0 {
							log.Printf("lenny-gateway: §16.4 partition maintenance %s: created %d, dropped %d, held %d",
								r.Table, len(r.Created), len(r.Dropped), len(r.Held))
						}
						// Rows in the never-dropped DEFAULT partition escape the
						// §16.4 retention DROP. A positive count means a write
						// landed before its dated partition existed (maintainer
						// lagging the write path); surface it so the operator can
						// re-provision before the catch-all grows unbounded.
						if r.DefaultRows > 0 {
							log.Printf("lenny-gateway: §16.4 partition maintenance %s: WARNING %d row(s) in the DEFAULT partition escape retention DROP (maintainer may be lagging the write path)",
								r.Table, r.DefaultRows)
						}
					}
				})
			})
		}

		// spec: §12.6 EventBus retranscribe worker. Like the
		// artifact GC and audit-retention pruner above, production gates the
		// sweep under the §10.1 / §12.5 gateway-leader lease (F-12.5.10) so
		// exactly one replica re-publishes at a time; the republish is
		// idempotent (downstream dedups by CloudEvents id), so a transient
		// multi-replica overlap during failover is safe. Only constructed
		// when a durable audit chain and a Redis EventBus both exist.
		if w.eventBusRetranscriber != nil {
			log.Printf("lenny-gateway: §12.6 EventBus retranscribe sweep cadence %s (maxRetryAttempts %d, duplicateInjectionFactor %d)",
				time.Duration(*eventBusRetryIntervalSeconds)*time.Second, *eventBusMaxRetryAttempts, *eventBusDuplicateInjectionFactor)
			leaderGate.Add("eventbus-retranscribe", func(ctx context.Context) {
				w.eventBusRetranscriber.Run(ctx)
			})
		}

		// §12.5 ll. 341 hard-prune sweep: every gc.cycleIntervalSeconds
		// the catalog removes rows whose tombstone deadline has elapsed
		// and emits the count to lenny_gc_tombstones_pruned_total.
		// Production runs this under the §10.1 leader lease; the dev-
		// mode in-memory deployment has no Postgres catalog and skips
		// the sweep entirely.
		if w.blobsCataloged != nil {
			leaderGate.Add("tombstone-hard-prune", func(ctx context.Context) {
				ticker := time.NewTicker(gcInterval)
				defer ticker.Stop()
				for {
					select {
					case <-ctx.Done():
						return
					case <-ticker.C:
						// §12.5 ll. 341: the single hard-prune pass sweeps
						// both GC-managed row classes — artifact_store
						// catalog rows and partial-checkpoint manifest rows
						// — on the same deleted_at retention predicate.
						count, err := w.blobsCataloged.HardPrune(ctx, clockinject.Now())
						if err != nil {
							log.Printf("lenny-gateway: §12.5 hard-prune sweep error: %v", err)
						} else {
							w.gwMetrics.AddGCTombstonesPruned("artifact_store", count)
							if count > 0 {
								log.Printf("lenny-gateway: §12.5 hard-prune removed %d tombstoned artifact_store rows past retention",
									count)
							}
						}
						// §12.5 ll. 316, 341: partial-manifest rows live in
						// the checkpoint metadata table and follow the
						// identical post-soft-delete lifecycle. Prune those
						// whose soft-delete tombstone predates
						// now - gc.tombstoneRetentionSeconds.
						pmCutoff := clockinject.Now().Add(-gcTombstoneRetention)
						pmCount, pmErr := hardPrunePartialManifests(ctx, w.partialManifests, pmCutoff)
						if pmErr != nil {
							log.Printf("lenny-gateway: §12.5 partial-manifest hard-prune sweep error: %v", pmErr)
							continue
						}
						w.gwMetrics.AddGCTombstonesPruned("partial_manifest", pmCount)
						if pmCount > 0 {
							log.Printf("lenny-gateway: §12.5 hard-prune removed %d tombstoned partial_manifest rows past retention",
								pmCount)
						}
					}
				}
			})
		}

		// §12.5 partial-manifest backstop sweep: co-located with the
		// tombstone hard-prune above, it runs on the same GC cycle and
		// reclaims abandoned active partial-manifest rows — the rows a
		// gateway crash between the §11.2 reservation and Finalise leaves
		// behind, and the rows of a session that never resumed within its
		// resume window. Each reclaim runs the same three-step release the
		// abort and supersede arms run (guarded catalog soft-delete, per-key
		// object delete, guarded exactly-once reservation decrement, manifest
		// soft-delete after a final empty prefix list). Without it the §11.2
		// reservation leaks permanently on every such crash and the abandoned
		// rows are never finalised or swept. Active only when the durable
		// catalog, object store, and manifest store are all wired (Postgres
		// mode); the in-memory dev gateway has no catalog and skips it.
		if w.partialManifests != nil && w.blobsCataloged != nil && w.minioStore != nil {
			backstopRelease := checkpointer.BackstopReclaim{
				Manifests:          w.partialManifests,
				Catalog:            w.blobsCataloged,
				Objects:            w.minioStore,
				Quota:              w.storageCounter,
				TombstoneRetention: gcTombstoneRetention,
				Metrics:            w.gwMetrics,
			}
			backstopResumeWindow := time.Duration(*maxResumePendingSeconds) * time.Second
			log.Printf("lenny-gateway: §12.5 partial-manifest backstop sweep cadence %s (maxResumeWindow %s)",
				gcInterval, backstopResumeWindow)
			leaderGate.Add("partial-manifest-backstop", func(ctx context.Context) {
				ticker := time.NewTicker(gcInterval)
				defer ticker.Stop()
				for {
					select {
					case <-ctx.Done():
						return
					case <-ticker.C:
						reclaimed, err := reclaimAbandonedManifests(ctx, backstopRelease, backstopResumeWindow)
						if err != nil {
							log.Printf("lenny-gateway: §12.5 partial-manifest backstop sweep error: %v", err)
							continue
						}
						if reclaimed > 0 {
							log.Printf("lenny-gateway: §12.5 partial-manifest backstop reclaimed %d abandoned active manifest row(s)",
								reclaimed)
						}
					}
				}
			})
		}

		// §12.8 legal-hold reconciler: co-located with the
		// retention-GC sweep. On the same cadence it scans for
		// (tenant, session) pairs under legal_hold=true with one or
		// more checkpoints rotated, emits a
		// legal_hold.checkpoint_gap_detected audit event into the
		// per-tenant §11.7 chain, and bumps
		// lenny_legal_hold_checkpoint_gaps_total. The reconciler is
		// active only when the durable catalog and audit chain are
		// wired (Postgres mode); the in-memory dev gateway has no
		// catalog and skips it.
		if w.artifactCatalog != nil && w.auditAppender != nil {
			recon := legalholdreconciler.New(w.artifactCatalog, w.auditAppender, w.gwMetrics, w.partialManifests, legalholdreconciler.Options{
				Clock: clockinject.Now,
			})
			leaderGate.Add("legal-hold-reconciler", func(ctx context.Context) {
				recon.Run(ctx, func(emitted int, err error) {
					if err != nil {
						log.Printf("lenny-gateway: §12.8 legal-hold reconciler sweep error: %v", err)
						return
					}
					if emitted > 0 {
						log.Printf("lenny-gateway: §12.8 legal-hold reconciler emitted %d checkpoint-gap audit rows", emitted)
					}
				})
			})
		}
	}

	// ----- §12.5 continuous T4 KMS availability probe (STO-021) -----
	// A leader-elected background goroutine, co-located with the GC
	// sweeps under the same gateway lease (the leader-election lease is
	// the §10.1 model the GC loops above already run under). On each
	// cadence it enumerates T4 tenants, re-runs the zero-byte
	// encrypt/decrypt round-trip against every tenant:{tenant_id} key,
	// and updates lenny_t4_kms_probe_last_success_timestamp /
	// lenny_t4_kms_probe_result_total so the T4KmsKeyUnusable alert and
	// the admin t4KmsLastProbeSuccessAt field observe silent
	// post-provisioning key drift. The token-bucket rate ceiling caps
	// KMS API spend; the cadence floor is enforced inside Prober.Start.
	{
		probeMetrics, err := tenantkms.NewProbeMetrics(w.gwMetrics.Registerer())
		if err != nil {
			log.Fatalf("lenny-gateway: §12.5 T4 KMS probe metrics: %v", err)
		}
		prober := &tenantkms.Prober{
			Lifecycle: w.kmsProbeLifecycle,
			Tenants:   t4TenantSource{w.tenants},
			Metrics:   probeMetrics,
			Interval:  time.Duration(*t4KmsProbeIntervalSeconds) * time.Second,
			RateLimit: *t4KmsProbeRateLimit,
			Now:       clockinject.Now,
		}
		log.Printf("lenny-gateway: §12.5 continuous T4 KMS probe interval=%ds rate=%.1f/s",
			*t4KmsProbeIntervalSeconds, *t4KmsProbeRateLimit)
		leaderGate.Add("t4-kms-probe", func(ctx context.Context) {
			if err := prober.Start(ctx); err != nil {
				log.Printf("lenny-gateway: §12.5 T4 KMS probe loop exited: %v", err)
			}
		})
	}

	// ----- §13.3 admin-token reclaimer sweep (C7 crash recovery) -----
	// The §17.6 gateway-mediated bootstrap admin-credential rotation patches
	// the lenny-admin-token Secret before durably revoking the prior token, so
	// a crash after the patch but before the revoke commits leaves the prior
	// token live. The rotation durably names that orphaned predecessor in the
	// Secret's prev_jti slot; this leader-gated sweep durably revokes the
	// single named jti with revocation_reason: rotation_replaced whenever it is
	// still unrevoked (idempotent once the in-request revoke has committed). It
	// closes the crash window the persist-Secret-before-revoke ordering opens
	// without weakening the no-grace-period guarantee, bounding the residual to
	// the sweep interval. A Provision()-time reclaimer would not cover this
	// window: provisioning runs only on an operator bootstrap call, not on a
	// gateway start, and early-returns on an existing Secret (the post-crash
	// state), so the always-running leader-gated sweep is the crash-recovery
	// surface. Wired under the same preconditions as the provisioner
	// (adminrouter.go): an in-cluster client to read the Secret, a durable
	// token store to revoke, a namespace, and admin-token provisioning enabled.
	// spec: §13.3, §16.7, §17.6.
	if !*adminTokenDisabled && w.clusterClient != nil && w.pgPool != nil && *adminTokenNamespace != "" {
		recl, rerr := admintokenreclaimer.New(admintokenreclaimer.Config{
			Namespace:  *adminTokenNamespace,
			SecretName: *adminTokenSecretName,
			Tenant:     *adminTokenTenant,
			Interval:   time.Duration(*adminTokenReclaimIntervalSeconds) * time.Second,
		},
			k8ssecret.New(w.clusterClient),
			adminIssuedTokens{store: issuedtokenstore.New(w.pgPool), cache: w.revProp, metrics: w.gwMetrics, clock: clockinject.Now},
			clockinject.Now)
		if rerr != nil {
			log.Fatalf("lenny-gateway: §13.3 admin-token reclaimer: %v", rerr)
		}
		log.Printf("lenny-gateway: §13.3 admin-token reclaimer sweep cadence %s (Secret %s/%s, tenant %s)",
			recl.Interval(), *adminTokenNamespace, *adminTokenSecretName, *adminTokenTenant)
		leaderGate.Add("admin-token-reclaimer", func(ctx context.Context) {
			recl.Run(ctx, func(reclaimed bool, err error) {
				if err != nil {
					log.Printf("lenny-gateway: §13.3 admin-token reclaimer sweep error: %v", err)
					return
				}
				if reclaimed {
					log.Printf("lenny-gateway: §13.3 admin-token reclaimer durably revoked an orphaned predecessor token (crash between Secret patch and revoke)")
				}
			})
		})
	}

	// spec: §12.5 — drive the lenny-gateway-leader election
	// and run every registered gateway-singleton sweep only while this
	// replica holds the lease (or always, under the AlwaysLeader fallback).
	// Started after every leaderGate.Add above so the full set is gated.
	log.Printf("lenny-gateway: §12.5 gateway-leader gate driving %d singleton sweep(s)", leaderGate.Len())
	go leaderGate.Run(w.watchdogCtx)
}

// hardPrunePartialManifests runs the §12.5 ll. 341 tombstone hard-prune
// pass over the partial-checkpoint manifest table: it physically removes
// every row whose soft-delete tombstone predates cutoff
// (now - gc.tombstoneRetentionSeconds). The pass is the sibling of the
// artifact_store hard-prune and runs on the same GC cycle so partial
// manifests follow the identical post-soft-delete lifecycle. Per-row
// HardDelete failures are logged and skipped — the next cycle retries
// them; a list failure returns the error with no rows pruned. Returns
// the number of rows physically removed.
//
// spec: §12.5 ll. 316, 341 — partial-manifest rows are swept by the same
// hard-prune pass on the same deleted_at retention predicate.
func hardPrunePartialManifests(ctx context.Context, store partialmanifeststore.Store, cutoff time.Time) (int, error) {
	expired, err := store.ListSoftDeletedBefore(ctx, cutoff)
	if err != nil {
		return 0, err
	}
	pruned := 0
	for _, r := range expired {
		if derr := store.HardDelete(ctx, r.TenantID, r.CheckpointID); derr != nil {
			log.Printf("lenny-gateway: §12.5 partial-manifest hard-prune row %s/%s checkpoint=%s: %v",
				r.TenantID, r.SessionID, r.CheckpointID, derr)
			continue
		}
		pruned++
	}
	return pruned, nil
}

// reclaimAbandonedManifests runs the §12.5 partial-manifest backstop sweep: it
// selects every abandoned active row via ListReclaimable (the row whose gateway
// died between Reserve and Finalise, or whose session was never resumed within
// its resume window) and reclaims each one through the same three-step release
// (guarded catalog soft-delete, per-key DeleteObject, guarded exactly-once
// reservation decrement, manifest soft-delete after a final empty prefix list)
// the abort and supersede arms run. The sweep is the sibling of the tombstone
// hard-prune and runs on the same GC cycle. Per-row reclaim failures are logged
// and skipped — ListReclaimable re-selects the row so the next cycle retries it;
// a select failure returns the error with no rows reclaimed. Returns the number
// of rows reclaimed.
//
// spec: §12.5 partial-manifest backstop and GC rule 6; §11.2 reservation
// release is exactly-once and guarded.
func reclaimAbandonedManifests(ctx context.Context, release checkpointer.BackstopReclaim, maxResumeWindow time.Duration) (int, error) {
	rows, err := release.Manifests.ListReclaimable(ctx, maxResumeWindow)
	if err != nil {
		return 0, err
	}
	reclaimed := 0
	for _, r := range rows {
		if rerr := checkpointer.ReclaimAbandonedManifest(ctx, release, r); rerr != nil {
			log.Printf("lenny-gateway: §12.5 partial-manifest backstop reclaim row %s/%s checkpoint=%s: %v",
				r.TenantID, r.SessionID, r.CheckpointID, rerr)
			continue
		}
		reclaimed++
	}
	return reclaimed, nil
}
