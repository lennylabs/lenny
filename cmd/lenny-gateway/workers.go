// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"log"
	"log/slog"
	"time"

	"github.com/lennylabs/lenny/pkg/blobstore/replication"
	replicationpgstore "github.com/lennylabs/lenny/pkg/blobstore/replication/pgstore"
	"github.com/lennylabs/lenny/pkg/clockinject"
	"github.com/lennylabs/lenny/pkg/connectoroauth"
	"github.com/lennylabs/lenny/pkg/gateway/coordination/pdbwatcher"
	"github.com/lennylabs/lenny/pkg/gateway/core/subsystem"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/impersonation"
	"github.com/lennylabs/lenny/pkg/gateway/experiment/evalstore"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/elicitationfloor"
	"github.com/lennylabs/lenny/pkg/gateway/metrics/gcpause"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/watchdog"
	"github.com/lennylabs/lenny/pkg/gateway/session/createdsweeper"
	"github.com/lennylabs/lenny/pkg/gateway/storage/failopen"
	"github.com/lennylabs/lenny/pkg/uploadtoken"
)

// startBackgroundWorkers launches the §4.1 gateway's periodic
// sweepers, samplers, reconcilers, leader-election loops, and
// security-cache propagator subscribers under the watchdog context.
// Every component it drives is constructed by an earlier build step and
// threaded through the accumulator; the step launches goroutines and
// returns, so the run loop (runServers) can install the signal handler
// and the listeners. It produces no value the later steps consume.
//
// spec: §4.1 — gateway background subsystems; §10.1 / §11.2 / §12.5 sweeps.
func (w *gatewayWiring) startBackgroundWorkers() {
	f := w.f
	agentNamespace := f.agentNamespace
	artifactReplicationConfig := f.artifactReplicationConfig
	artifactReplicationRoleARN := f.artifactReplicationRoleARN
	elicitationFloorConfigMap := f.elicitationFloorConfigMap
	elicitationFloorReconcileSeconds := f.elicitationFloorReconcileSeconds
	evalAggregationRefreshSeconds := f.evalAggregationRefreshSeconds
	gatewayNamespace := f.gatewayNamespace
	gatewayPDBName := f.gatewayPDBName
	gatewayServiceName := f.gatewayServiceName
	maxCreatedStateTimeoutSeconds := f.maxCreatedStateTimeoutSeconds
	memoryRecordCountInterval := f.memoryRecordCountInterval
	minioAccessKey := f.minioAccessKey
	minioEndpoint := f.minioEndpoint
	minioSecretKey := f.minioSecretKey
	minioUseSSL := f.minioUseSSL

	// spec: §5.2 — wire the concurrent-stateless ingress. When a
	// cluster client and agent namespace are present the gateway routes
	// /v1/stateless/{pool}/... to a tenant-pinned pod IP discovered from
	// the pool's pods, bypassing the Service LB. Each pool's
	// EndpointPoller runs under watchdogCtx. Without a cluster the route
	// is absent (no stateless pods exist to route to). F-5.2.29.
	if w.clusterClient != nil && *agentNamespace != "" {
		statelessMgr := buildStatelessRouting(w.watchdogCtx, w.clusterClient, *agentNamespace, w.pools, w.gwMetrics)
		w.mux.Handle("/v1/stateless/", statelessMgr)
		log.Printf("lenny-gateway: §5.2 concurrent-stateless ingress mounted at /v1/stateless/ (agent namespace %s)", *agentNamespace)
	}

	go w.wd.Run(w.watchdogCtx, func(res watchdog.Result, err error) {
		if err != nil {
			log.Printf("lenny-gateway: watchdog sweep error: %v", err)
			return
		}
		if res.ForcedFailures > 0 {
			log.Printf("lenny-gateway: watchdog forced %d sessions to failed: %v",
				res.ForcedFailures, res.PerReason)
		}
		if res.Expirations > 0 {
			log.Printf("lenny-gateway: watchdog expired %d sessions past their §11.3 deadline",
				res.Expirations)
		}
		if res.ResumePendingTimeouts > 0 {
			log.Printf("lenny-gateway: watchdog transitioned %d resume_pending sessions to awaiting_client_action per §6.2",
				res.ResumePendingTimeouts)
		}
		if res.ResumingTimeouts > 0 {
			log.Printf("lenny-gateway: watchdog fired §6.2 resuming watchdog on %d sessions: %v",
				res.ResumingTimeouts, res.PerResumingOutcome)
		}
	})

	// §7.1 uploadToken signing-key rotator. The default
	// cadence rotates every 24h with a 5-minute overlap window; the
	// rotator both installs the new key and sweeps overlap keys whose
	// deadline has elapsed. spec: §7.1.
	go w.uploadRotator.Run(w.watchdogCtx)

	// F-7.4.9: §7.1 single-use upload-token tracker sweep. The memory
	// tracker holds one entry per consumed token's digest until its
	// expiry timestamp; without a periodic Sweep the map grows
	// monotonically with the gateway's process lifetime. The cadence is
	// the watchdog tick interval — cheap enough to run frequently and
	// short enough that a sweep happens well within one
	// maxCreatedStateTimeoutSeconds window. spec: §7.1.
	go func() {
		tick := time.NewTicker(uploadtoken.DefaultRotationInterval / 96) // 15m at the default 24h
		if tick == nil {
			return
		}
		defer tick.Stop()
		for {
			select {
			case <-w.watchdogCtx.Done():
				return
			case now := <-tick.C:
				if n := w.uploadTracker.Sweep(now.UTC()); n > 0 {
					log.Printf("lenny-gateway: §7.1 upload-token tracker swept %d expired digests", n)
				}
			}
		}
	}()

	// ----- §9.3 connector OAuth state-store sweep -----
	// F-9.3.16: the in-memory MemoryStateStore is bound by the
	// state-TTL (10 min per §9.3) plus a `consumed` flag for
	// single-use enforcement. Without a periodic Sweep the entries
	// accumulate until process restart. The cadence is well inside one
	// TTL window so consumed/expired entries are reclaimed promptly.
	// A Redis-backed store relies on native key expiry instead — this
	// goroutine runs only when the in-memory store is wired (the
	// `--connector-oauth-callback-url` opt-in path).
	// spec: §9.3.
	if w.connectorStateStore != nil {
		go func(store *connectoroauth.MemoryStateStore) {
			tick := time.NewTicker(1 * time.Minute)
			if tick == nil {
				return
			}
			defer tick.Stop()
			for {
				select {
				case <-w.watchdogCtx.Done():
					return
				case now := <-tick.C:
					if n := store.Sweep(now.UTC()); n > 0 {
						log.Printf("lenny-gateway: §9.3 connector OAuth state store swept %d expired/consumed entries", n)
					}
				}
			}
		}(w.connectorStateStore)
	}

	// ----- §13.3 impersonation-session expiry sweep -----
	// Emits admin.impersonation_ended (reason=expired) once a minted
	// impersonation bearer reaches its impersonation_duration_seconds. The
	// bearer self-expires (its exp claim); the sweep records the terminal
	// audit event so the SIEM sees a matching end for every start.
	// spec: §16.7.
	go func(svc *impersonation.Service) {
		tick := time.NewTicker(1 * time.Minute)
		defer tick.Stop()
		for {
			select {
			case <-w.watchdogCtx.Done():
				return
			case now := <-tick.C:
				if n, err := svc.SweepExpired(w.watchdogCtx, now.UTC()); err != nil {
					log.Printf("lenny-gateway: §13.3 impersonation expiry sweep: %v", err)
				} else if n > 0 {
					log.Printf("lenny-gateway: §13.3 impersonation expiry sweep ended %d session(s)", n)
				}
			}
		}
	}(w.impersonationSvc)

	// ----- §7.1 abandoned `created`-state row sweep -----
	// Drops Session rows that stay in `created` past
	// maxCreatedStateTimeoutSeconds (default 300s). The §7.1
	// uploadToken TTL closes the upload window at that instant; without
	// this sweep the row itself lived forever, so abandoned creates
	// accumulated under repeated client retries.
	// spec: §7.1.
	createdGC := createdsweeper.New(w.sessions, tenantsLister{w.tenants}, createdsweeper.Options{
		// F-7.4.7: pinned to the same maxCreatedStateTimeoutSeconds the
		// watchdog and the uploadToken issuer use.
		// spec: §7.1.
		Timeout: time.Duration(*maxCreatedStateTimeoutSeconds) * time.Second,
		Clock:   clockinject.Now,
		// §15.1: an abandoned `created`-state row
		// holds a pod claimed at /create; releasing the row must return that
		// pod to the pool and revoke any lease. Wire the sweep to the same
		// claimless reclaim /terminate runs (Binder.ReclaimClaimed), closing
		// over the kube Client, Namespace, and CredentialAssigner the binder
		// already carries. ReclaimClaimed releases by pod name, so the poolRef
		// the Reclaimer carries is unused here; it is part of the §4.6
		// persisted binding the contract mirrors. Nil when the gateway runs
		// without a pod binder (in-memory mode), where the sweep drops the row
		// without a pod release.
		Reclaim: createdSweeperReclaim(w.podBinder),
	})
	go createdGC.Run(w.watchdogCtx, func(dropped int, err error) {
		if err != nil {
			log.Printf("lenny-gateway: §7.1 created-state sweep error: %v", err)
			return
		}
		if dropped > 0 {
			log.Printf("lenny-gateway: §7.1 created-state sweep dropped %d abandoned rows past the upload-token deadline",
				dropped)
		}
	})

	w.startReconcilerWorkers()
	// ----- §25.11 ArtifactStore cross-region replication -----
	// The §12.5 / §25.11 ArtifactStore replication controller
	// configures continuous MinIO bucket replication to the off-cluster
	// target and runs the runtime residency preflight, suspending
	// replication fail-closed on a jurisdiction mismatch. It is hosted in
	// the gateway because the gateway holds the source ArtifactStore
	// MinIO client, the §16.7 audit pipeline, and the Prometheus metric
	// surface the controller's audit events and residency-violation
	// counters need; it is co-located with the §12.5 leader-elected MinIO
	// maintenance sweeps below. CONFIG_INVALID aborts startup. The
	// subsystem is off until an operator supplies
	// --artifact-replication-config with enabled:true. F-12.5.20 /
	// F-16.7.2 / F-17.3.7 / F-25.11.1.
	if replCfg, err := parseReplicationConfig(*artifactReplicationConfig); err != nil {
		log.Fatalf("lenny-gateway: %v", err)
	} else if replCfg.Enabled {
		if *minioEndpoint == "" {
			log.Fatalf("lenny-gateway: §25.11 artifact replication requires --minio-endpoint (the source ArtifactStore cluster)")
		}
		driver, err := newReplicationDriver(replicationSource{
			endpoint:  *minioEndpoint,
			accessKey: *minioAccessKey,
			secretKey: *minioSecretKey,
			useSSL:    *minioUseSSL,
		}, w.clusterClient, *agentNamespace, *artifactReplicationRoleARN)
		if err != nil {
			log.Fatalf("lenny-gateway: §25.11 artifact replication: %v", err)
		}
		// §25.11 / F-25.11.3: persist the per-region replication state to
		// ops_artifact_replication_state (migration 0126) when Postgres is
		// wired, so a fail-closed residency suspension survives a restart
		// rather than silently re-enabling from an empty in-memory map.
		var replState replication.StateStore
		if w.pgPool != nil {
			replState = replicationpgstore.New(w.pgPool)
		}
		replCtrl, err := replication.NewController(replication.ControllerConfig{
			Config:  replCfg,
			Driver:  driver,
			State:   replState,
			Audit:   replicationAuditSink{appender: w.auditAppender}.emit,
			Metrics: replicationMetricsAdapter{m: w.gwMetrics},
			Lag:     newReplicationLagAdapter(w.gwMetrics),
		})
		if err != nil {
			log.Fatalf("lenny-gateway: §25.11 artifact replication: %v", err)
		}
		log.Printf("lenny-gateway: §25.11 ArtifactStore replication enabled (%d region(s), residency tick %s)",
			len(replCfg.Regions), replCtrl.ResidencyTickInterval())
		go runReplicationController(w.watchdogCtx, replCtrl, log.Printf)
		// Wire the live controller onto the admin Router so the §25.11
		// POST/GET /v1/admin/artifact-replication/{region}/{resume,status}
		// endpoints reach it. The admin Handler is already mounted; the
		// handlers read this field at request time, so the late wiring is
		// honoured (same pattern as the playground-revocation wiring).
		// F-25.11.1.
		w.adminRouter = w.adminRouter.WithArtifactReplication(replCtrl)
	}

	w.startLeaderElectedSweeps()

	w.startBillingAndSecurityWorkers()
	// ----- §16.1 metrics export -----
	// Refreshes the gauge metrics (storage quota, circuit breakers)
	// that the §16.5 alerts read.
	exportGaugeMetrics := func(ctx context.Context) {
		exportStorageQuotaMetrics(ctx, w.tenants, w.storageCounter, w.gwMetrics)
		exportCircuitBreakerMetrics(ctx, w.breakers, w.breakerCache, w.gwMetrics)
		// §16.5 — the standing ElicitationContentIntegrityWeakened
		// alert reads a gauge that must reflect the live tenant posture, so
		// refresh it on the same 30s cadence as the other gauge exporters.
		// F-9.2.5.
		exportElicitationIntegrityWeakened(ctx, w.tenants, w.elicitationFloorProvider.Floor(), w.gwMetrics)
	}
	exportGaugeMetrics(context.Background())
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-w.watchdogCtx.Done():
				return
			case <-ticker.C:
				exportGaugeMetrics(w.watchdogCtx)
			}
		}
	}()

	// spec: §10.7 — when evalAggregationRefreshSeconds is
	// positive, the gateway schedules periodic REFRESH MATERIALIZED VIEW
	// CONCURRENTLY lenny_eval_aggregates at the configured interval so the
	// results API (routed to the matview) reads recent aggregates. The
	// SECURITY DEFINER refresh function (migration 0156) runs the
	// cross-tenant refresh under its BYPASSRLS owner. F-10.7.12.
	if w.evalMatviewEnabled {
		if ar, ok := w.evals.(evalstore.AggregateReader); ok {
			interval := time.Duration(*evalAggregationRefreshSeconds) * time.Second
			refreshEvalAggregates := func(ctx context.Context) {
				if err := ar.RefreshAggregates(ctx); err != nil {
					log.Printf("warning: §10.7 lenny_eval_aggregates refresh failed: %v", err)
				}
			}
			refreshEvalAggregates(context.Background())
			go func() {
				ticker := time.NewTicker(interval)
				defer ticker.Stop()
				for {
					select {
					case <-w.watchdogCtx.Done():
						return
					case <-ticker.C:
						refreshEvalAggregates(w.watchdogCtx)
					}
				}
			}()
		}
	}

	// §4.1 SCL-026 HPA scale-out gauges. Polled on a 5s cadence so the
	// custom-metrics pipeline (Prometheus Adapter / KEDA) observes
	// back-pressure quickly enough to scale before the saturation
	// threshold is reached. The primary trigger
	// (lenny_gateway_request_queue_depth) is the dominant signal here;
	// active streams and active sessions feed the secondary HPA metric
	// and the §16.5 GatewaySessionBudgetNearExhaustion alert.
	hpaTenantLister := tenantsLister{w.tenants}
	exportHPAGauges(context.Background(), w.sessions, hpaTenantLister, w.eventBus, w.gwMetrics)
	exportSessionAvailabilityRatio(context.Background(), w.sessions, w.gwMetrics)
	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-w.watchdogCtx.Done():
				return
			case <-ticker.C:
				exportHPAGauges(w.watchdogCtx, w.sessions, hpaTenantLister, w.eventBus, w.gwMetrics)
				exportSessionAvailabilityRatio(w.watchdogCtx, w.sessions, w.gwMetrics)
			}
		}
	}()

	// §4.1 process-level GC pause sampler. Reads runtime/debug.ReadGCStats
	// every gcpause.DefaultInterval seconds, maintains a sliding window
	// (gcpause.DefaultWindow), computes the p99 in milliseconds, and
	// pushes the value to the lenny_gateway_gc_pause_p99_ms gauge. The
	// §16.5 Tier3GCPressureHigh alert reads the fleet-wide aggregate
	// (`max(...)`) of this gauge to gate Tier 3 promotion.
	gcCollector := &gcpause.Collector{Gauge: w.gwMetrics}
	go gcCollector.Run(w.watchdogCtx)

	// spec: §13.3 — NTP drift sampler. Samples the clockinject
	// offset on the configured cadence, publishes the
	// lenny_time_drift_seconds gauge, and gates /healthz at the 5s
	// degraded threshold. F-13.3.5.
	go w.driftMonitor.Start(w.watchdogCtx, 30*time.Second)

	// spec: §9.4 / §16.1 — periodic per-tenant
	// MemoryStore record-count sampler. Walks the store's tenants and
	// emits `lenny_memory_store_record_count{tenant_id}` on the
	// configured interval (default 60s); 0 disables. The contract is a
	// best-effort approximate gauge sampled periodically. F-9.4.1.
	if w.memories != nil && *memoryRecordCountInterval > 0 {
		if counter, ok := w.memories.(interface {
			TenantRecordCounts(context.Context) (map[string]int, error)
		}); ok {
			interval := *memoryRecordCountInterval
			go func() {
				ticker := time.NewTicker(interval)
				defer ticker.Stop()
				sample := func() {
					ctx, cancel := context.WithTimeout(w.watchdogCtx, 30*time.Second)
					defer cancel()
					counts, err := counter.TenantRecordCounts(ctx)
					if err != nil {
						log.Printf("lenny-gateway: §9.4 record-count sampler: %v", err)
						return
					}
					for tenantID, n := range counts {
						w.gwMetrics.SetMemoryStoreRecordCount(tenantID, n)
					}
				}
				sample()
				for {
					select {
					case <-w.watchdogCtx.Done():
						return
					case <-ticker.C:
						sample()
					}
				}
			}()
			log.Printf("lenny-gateway: §9.4 record-count sampler interval=%s backend=%s",
				interval, w.memoryBackendLabel)
		}
	}

	// spec: §10.4 / §16.5 PDBBlockedEvictions — periodic PDB
	// status poller. Each cycle that observes Status.DisruptionsAllowed
	// == 0 on the gateway PDB increments the
	// lenny_pdb_blocked_evictions_total counter so the §16.5 alert can
	// fire when the PDB sustains blocking. The poller activates only
	// when the cluster client is wired (production install with
	// --agent-namespace) and when --gateway-namespace is set so the
	// poller can address the right PDB object. F-10.4.4.
	if w.clusterClient != nil && *gatewayNamespace != "" {
		watcher := pdbwatcher.New(pdbwatcher.Config{
			Client:    w.clusterClient,
			Namespace: *gatewayNamespace,
			PDBName:   *gatewayPDBName,
			Sink:      w.gwMetrics,
		})
		go watcher.Run(w.watchdogCtx)
		log.Printf("lenny-gateway: §10.4 PDB poller watching %s/%s",
			*gatewayNamespace, *gatewayPDBName)

		// spec: §12.4 — drive the fail-open cached_replica_count
		// from the gateway Service's Endpoints object so the per-replica
		// ceiling divides by the last-known good replica count. The poller
		// retains the cached value across poll failures, so a dual outage
		// (Redis + Endpoints) divides by the last observed count rather than
		// collapsing every replica to 1. F-12.4.9.
		go (&failopen.ReplicaPoller{
			Lister: gatewayEndpointsLister{
				client:    w.clusterClient,
				namespace: *gatewayNamespace,
				service:   *gatewayServiceName,
			},
			Count: w.failOpenReplicas,
			Logf:  func(format string, args ...any) { log.Printf(format, args...) },
		}).Run(w.watchdogCtx)
		log.Printf("lenny-gateway: §12.4 fail-open replica-count poller watching endpoints %s/%s",
			*gatewayNamespace, *gatewayServiceName)

		// spec: §17.2 — keep the §9.2 platform elicitation
		// content-integrity floor live by re-reading the phase-stamp
		// ConfigMap's security.elicitationContentIntegrity.floor key. A
		// `helm upgrade` that raises or lowers the floor takes effect
		// without a gateway restart; a read error or absent key retains
		// the last-known floor. The audit events for the transition
		// (platform.elicitation_content_integrity_floor_changed) carry the
		// operator OIDC sub and are emitted by the chart render path, not
		// here (F-17.2.8 / F-9.2.10). F-17.2.9.
		go (&elicitationfloor.Reconciler{
			Reader: phaseStampFloorReader{
				client:    w.clusterClient,
				namespace: *gatewayNamespace,
				name:      *elicitationFloorConfigMap,
			},
			Provider: w.elicitationFloorProvider,
			Interval: time.Duration(*elicitationFloorReconcileSeconds) * time.Second,
			Logf:     func(format string, args ...any) { log.Printf(format, args...) },
		}).Run(w.watchdogCtx)
		log.Printf("lenny-gateway: §17.2 elicitation-floor reconciler watching configmap %s/%s",
			*gatewayNamespace, *elicitationFloorConfigMap)
	}

	// §4.1 per-subsystem state publisher. Periodically reads the
	// queue depth, in-flight count, and circuit state from every
	// wired Subsystem and pushes the values to the
	// lenny_gateway_subsystem_{queue_depth, circuit_state} gauges
	// so the §16.5 alerts observe back-pressure even when the
	// handler path uses Breaker.Allow / Limiter.TryAcquire directly
	// (the DoObserved per-call path covers histograms / counters).
	subsystems := []*subsystem.Subsystem{w.uploadSubsystem, w.tokenServiceSubsystem}
	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		publish := func() {
			for _, s := range subsystems {
				s.PublishGauges(w.subsystemMetrics)
			}
			// §4.3: mirror the Token Service subsystem's
			// breaker state onto the dedicated
			// lenny_token_service_circuit_state gauge the §16.5
			// TokenServiceUnavailable alert reads. The §4.1
			// per-subsystem gauge already carries it; the dedicated
			// gauge keeps the alert expression cleanly named.
			w.gwMetrics.SetTokenServiceCircuitState(w.tokenServiceSubsystem.State().MetricValue())
		}
		publish()
		for {
			select {
			case <-w.watchdogCtx.Done():
				return
			case <-ticker.C:
				publish()
			}
		}
	}()

	// spec: §11.2 / §4.7 / §8.3 / §6.2 — the single
	// global direct-mode ReportUsage poll loop. It iterates the replica's live
	// pod bindings on the direct-usage poll interval, filters to direct-delivery
	// sessions by their credential lease, pulls each session's incremental §4.7
	// ReportUsage delta from its adapter, and fans it into the direct-mode usage
	// recorder (the accounting sinks plus the §11.2 anomaly detector, gating the
	// §6.2 idle stamp on a non-zero delta and excluding the mid-session enforcer).
	// newDirectUsageLoop returns nil when the registry, the lease store, or the
	// recorder is absent (a minimal gateway that records no usage), and Run is a
	// no-op on a nil loop, so the launch is unconditional. The loop's only exit is
	// watchdogCtx.Done, so it leaks no goroutine at shutdown. F-15.3.7, F-11.2.20.
	//
	// directUsageRecorderOrNil normalizes the recorder before the interface
	// conversion: on a usagestore-less gateway newProxyUsageRecorder returns a
	// nil *proxyUsageRecorder, and passing that concrete typed nil directly would
	// wrap into a non-nil interface, defeating newDirectUsageLoop's recorder==nil
	// short-circuit and running an empty ticker.
	directUsage := newDirectUsageLoop(w.podRegistry, w.llmLeases, directUsageRecorderOrNil(w.proxyUsageRec), *f.directUsagePollIntervalSeconds, slog.Default())
	if directUsage != nil {
		log.Printf("lenny-gateway: §11.2 direct-mode ReportUsage poll loop interval=%s", directUsage.interval)
		go directUsage.Run(w.watchdogCtx)
	}
}
