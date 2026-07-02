// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"log"
	"os"
	"time"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	agentpodstatepg "github.com/lennylabs/lenny/pkg/agentpodstate/pgstore"
	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/blobstore/replication"
	replicationpgstore "github.com/lennylabs/lenny/pkg/blobstore/replication/pgstore"
	"github.com/lennylabs/lenny/pkg/clockinject"
	"github.com/lennylabs/lenny/pkg/connectoroauth"
	"github.com/lennylabs/lenny/pkg/credential"
	"github.com/lennylabs/lenny/pkg/gateway/billing/billingcheckpoint"
	"github.com/lennylabs/lenny/pkg/gateway/billing/billingretention"
	"github.com/lennylabs/lenny/pkg/gateway/checkpoint/checkpointer"
	"github.com/lennylabs/lenny/pkg/gateway/coordination/gatewayleader"
	"github.com/lennylabs/lenny/pkg/gateway/coordination/pdbwatcher"
	"github.com/lennylabs/lenny/pkg/gateway/core/subsystem"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/impersonation"
	"github.com/lennylabs/lenny/pkg/gateway/experiment/evalstore"
	"github.com/lennylabs/lenny/pkg/gateway/externalapi/admintoken/k8ssecret"
	admintokenreclaimer "github.com/lennylabs/lenny/pkg/gateway/externalapi/admintoken/reclaimer"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegationbudget"
	delegationbudgetpg "github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegationbudget/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegationtree/deadlock"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegationtree/orphancleanup"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/elicitationfloor"
	"github.com/lennylabs/lenny/pkg/gateway/metrics/gcpause"
	idempgstore "github.com/lennylabs/lenny/pkg/gateway/middleware/idempotency/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/operability/recommendations"
	"github.com/lennylabs/lenny/pkg/gateway/quota/quotacheckpoint"
	"github.com/lennylabs/lenny/pkg/gateway/quota/quotafailopen"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/watchdog"
	"github.com/lennylabs/lenny/pkg/gateway/session/createdsweeper"
	"github.com/lennylabs/lenny/pkg/gateway/session/orphansession"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/storage/failopen"
	"github.com/lennylabs/lenny/pkg/gateway/storage/issuedtokenstore"
	"github.com/lennylabs/lenny/pkg/gateway/storage/legalholdreconciler"
	"github.com/lennylabs/lenny/pkg/gateway/storage/partitionmaint"
	"github.com/lennylabs/lenny/pkg/gateway/storage/retentiongc"
	"github.com/lennylabs/lenny/pkg/pgwritemetrics"
	"github.com/lennylabs/lenny/pkg/podlifecycle"
	"github.com/lennylabs/lenny/pkg/quota"
	"github.com/lennylabs/lenny/pkg/redisconn"
	"github.com/lennylabs/lenny/pkg/tenantkms"
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

	// spec: §5.2 line 500 — wire the concurrent-stateless ingress. When a
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
			log.Printf("lenny-gateway: watchdog transitioned %d resume_pending sessions to awaiting_client_action per §6.2 line 292",
				res.ResumePendingTimeouts)
		}
		if res.ResumingTimeouts > 0 {
			log.Printf("lenny-gateway: watchdog fired §6.2 line 249 resuming watchdog on %d sessions: %v",
				res.ResumingTimeouts, res.PerResumingOutcome)
		}
	})

	// §7.1 line 67 uploadToken signing-key rotator. The default
	// cadence rotates every 24h with a 5-minute overlap window; the
	// rotator both installs the new key and sweeps overlap keys whose
	// deadline has elapsed. spec: §7.1 line 67.
	go w.uploadRotator.Run(w.watchdogCtx)

	// F-7.4.9: §7.1 single-use upload-token tracker sweep. The memory
	// tracker holds one entry per consumed token's digest until its
	// expiry timestamp; without a periodic Sweep the map grows
	// monotonically with the gateway's process lifetime. The cadence is
	// the watchdog tick interval — cheap enough to run frequently and
	// short enough that a sweep happens well within one
	// maxCreatedStateTimeoutSeconds window. spec: §7.1 line 60.
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
	// state-TTL (10 min per §9.3 line 157) plus a `consumed` flag for
	// single-use enforcement. Without a periodic Sweep the entries
	// accumulate until process restart. The cadence is well inside one
	// TTL window so consumed/expired entries are reclaimed promptly.
	// A Redis-backed store relies on native key expiry instead — this
	// goroutine runs only when the in-memory store is wired (the
	// `--connector-oauth-callback-url` opt-in path).
	// spec: §9.3 line 157.
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
	// spec: §16.7 line 680.
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
	// maxCreatedStateTimeoutSeconds (default 300s). The §7.1 line 58
	// uploadToken TTL closes the upload window at that instant; without
	// this sweep the row itself lived forever, so abandoned creates
	// accumulated under repeated client retries.
	// spec: §7.1 line 58.
	createdGC := createdsweeper.New(w.sessions, tenantsLister{w.tenants}, createdsweeper.Options{
		// F-7.4.7: pinned to the same maxCreatedStateTimeoutSeconds the
		// watchdog and the uploadToken issuer use.
		// spec: §7.1 line 58.
		Timeout: time.Duration(*maxCreatedStateTimeoutSeconds) * time.Second,
		Clock:   clockinject.Now,
		// §15.1 line 630 (proposal §4.5): an abandoned `created`-state row
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
	// The §12.5 line 278 / §25.11 ArtifactStore replication controller
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
		// §16.5 line 460 — the standing ElicitationContentIntegrityWeakened
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

	// spec: §10.7 line 1088 — when evalAggregationRefreshSeconds is
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

	// spec: §13.3 line 595 — NTP drift sampler. Samples the clockinject
	// offset on the configured cadence, publishes the
	// lenny_time_drift_seconds gauge, and gates /healthz at the 5s
	// degraded threshold. F-13.3.5.
	go w.driftMonitor.Start(w.watchdogCtx, 30*time.Second)

	// spec: §9.4 line 202 / §16.1 line 153 — periodic per-tenant
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

	// spec: §10.4 line 385 / §16.5 PDBBlockedEvictions — periodic PDB
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

		// spec: §12.4 line 224 — drive the fail-open cached_replica_count
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

		// spec: §17.2 line 86 — keep the §9.2 platform elicitation
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
			// §4.3 line 211: mirror the Token Service subsystem's
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
}

// buildLLMProxy builds the §4.9 LLM reverse-proxy HTTP server for
// proxy-mode agent pods: the translator registry, the opt-in semantic
// cache, the §15.1/§11.2 usage recorder, and the §4.9 credential
// fallback controller. It returns nil when --llm-proxy-addr is empty,
// which disables the proxy listener. The dependencies it does not own
// (the credential and policy surfaces, the session-budget gate, and the
// idle-activity stamper) are passed in; the stores and metrics it shares
// with the rest of the gateway are read from the accumulator.
//
// spec: §4.9 — LLM Proxy subsystem (a named §4.1 extraction target).

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
	// ----- §12.5 gateway-leader election (lenny-gateway-leader Lease) -----
	// spec: §12.5 lines 317, 332 — the artifact-GC orchestrator and the
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
		// §12.5 line 317 gc.cycleIntervalSeconds: the GC sweep cadence,
		// clamped up to the spec's 60s floor so a misconfigured chart
		// value cannot make the leader-elected sweep busy-loop.
		rawGCInterval := time.Duration(*gcCycleIntervalSeconds) * time.Second
		gcInterval := retentiongc.ClampSweepInterval(rawGCInterval)
		if gcInterval != rawGCInterval && rawGCInterval > 0 {
			log.Printf("lenny-gateway: §12.5 line 317 gc.cycleIntervalSeconds=%d below the %ds floor; clamping to the minimum",
				*gcCycleIntervalSeconds, int(retentiongc.MinSweepInterval/time.Second))
		}
		// §12.5 line 341 gc.tombstoneRetentionSeconds: the window a
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
		// §12.5 line 317: bind the erasure-completion → immediate-sweep
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

		// §16.4 lines 378-382 audit-retention pruner: only constructed
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

		// spec: §16.4 line 378 EventStore partition maintainer: the
		// leader-elected sweep that creates the current + ahead daily
		// partitions of session_logs / stream_cursors and drops partitions
		// whose entire range has aged past the §16.4 retention window (30
		// days for session logs, 7 days for stream cursors). Only wired when
		// a durable Postgres pool exists; the in-memory dev gateway has no
		// partitioned EventStore tables. audit_log stays on the DELETE-based
		// pruner above (native partitioning of audit_log conflicts with the
		// §12.8 line 815 audit_redaction_receipts FK onto audit_log.id; see
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

		// spec: §12.6 lines 685-689 EventBus retranscribe worker. Like the
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

		// §12.8 line 739 legal-hold reconciler: co-located with the
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
			recon := legalholdreconciler.New(w.artifactCatalog, w.auditAppender, w.gwMetrics, legalholdreconciler.Options{
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

	// ----- §12.5 line 307 continuous T4 KMS availability probe (STO-021) -----
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
	// spec: §13.3 (named predecessor and leader-gated reclaimer, lines
	// 601-607), §16.7 (token.revoked rotation_replaced, line 673), §17.6.
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

	// spec: §12.5 lines 317, 332 — drive the lenny-gateway-leader election
	// and run every registered gateway-singleton sweep only while this
	// replica holds the lease (or always, under the AlwaysLeader fallback).
	// Started after every leaderGate.Add above so the full set is gated.
	log.Printf("lenny-gateway: §12.5 gateway-leader gate driving %d singleton sweep(s)", leaderGate.Len())
	go leaderGate.Run(w.watchdogCtx)
}

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
		// spec: §8.10 line 1078 — operator-tunable cascade timeout. The
		// per-deploy cap is the Sweeper's wall-clock window an orphan may
		// persist past its root's terminal state. F-8.10.9.
		CascadeTimeout: time.Duration(*delegationCascadeTimeoutSeconds) * time.Second,
		// F-5.2.26: same terminal pipeline as the watchdog so an orphan
		// terminated by background sweep also releases its slot/pod.
		Terminal: w.sessionSrv,
		// spec: §8.10 lines 1091, 1093-1101, 1103; §16.1 lines 146-149 —
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
	// spec: §10.1 lines 47-52. F-10.1.5.
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
	// zero. spec: §8.8 lines 981-997. F-8.8.6.
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

	// ----- §12.4 line 210 storage-quota recovery reconciler -----
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
	// table (§11.2 line 44); on a Redis-recovery edge it reconstructs each
	// checkpointed tree's counters to max(postgres_checkpoint, live) per
	// axis before new delegations resume (§11.2 line 48 / §12.4 line 218),
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
	// active window total to token_usage_checkpoint (§11.2 line 44); on a
	// Redis-recovery edge it restores every still-current counter to
	// MAX(redis_current, postgres_checkpoint) before the next checkpoint
	// (§11.2 line 48). The same Service backs the §24.6 operator reconcile
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

	// spec: §12.4 line 268 — in the in_memory_reconciled mode, drive the
	// per-replica budget-slice reconcile loop ("reconciles with Postgres
	// periodically (default: every 30s)") and the final flush on shutdown.
	// The Redis checkpoint Reconciler above does not run in this mode
	// (quotaCounter is nil), so the budget tracker owns the tenant rollup
	// checkpoint rows.
	if w.quotaBudgetTracker != nil {
		go w.quotaBudgetTracker.Run(w.watchdogCtx)
	}
}

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

	// ----- §11 line 144 billing failover Tier 1 per-tenant flusher -----
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

	// ----- §12.3 lines 115-125 Postgres write-IOPS sampler -----
	// Periodically differentiates the pg_stat_database row-write total
	// into a sustained write-IOPS rate and publishes
	// lenny_postgres_write_iops so the §16.5 PostgresWriteSaturation
	// alert has a numerator. Only the Postgres-backed deployment has a
	// pool to sample. F-12.3.7.
	if w.pgPool != nil {
		pool := w.pgPool
		sampler := pgwritemetrics.New(func(ctx context.Context) (uint64, error) {
			// §12.3 line 62 write sources are row-level inserts/updates/
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
	// spec: §11.2.1 line 151. F-11.2.15.
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
	// spec: §4.4 line 256.
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
	// Seeds the per-replica credential deny list from the credential
	// stores' revoked entries, so a replica started immediately after a
	// pool-credential revocation denies that credential on the upstream
	// path even if it missed the original Redis pub/sub notification. The
	// rebuild is authoritative (Reset) and runs once at startup, where
	// the list is empty; it is not periodic because a periodic Reset
	// would drop the entries the live §11.4 revocation path adds for
	// pool credentials not yet reflected in the store query. The §4.9
	// union's user-credential term is vacuous today — no user-backed
	// lease is minted at session creation yet — so only the
	// pool-credential side is seeded.
	//
	// spec: §4.9 lines 1668-1673.
	{
		revoked, err := w.credentialPools.RevokedCredentials(context.Background())
		if err != nil {
			log.Printf("lenny-gateway: §4.9 credential deny-list startup rebuild failed: %v", err)
		} else {
			keys := make([]credential.CredentialKey, 0, len(revoked))
			for _, rc := range revoked {
				keys = append(keys, credential.CredentialKey{
					Source:       credential.SourcePool,
					PoolID:       rc.PoolName,
					CredentialID: rc.CredentialID,
				})
			}
			w.credDeny.Reset(keys)
			if len(keys) > 0 {
				log.Printf("lenny-gateway: §4.9 credential deny list rebuilt with %d revoked credential(s)", len(keys))
			}
		}
	}

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
}

// buildBillingPipeline constructs the §11.2.1 two-tier billing failover
// pipeline (the durable ledger wrapped in the Redis-stream failover Tier so
// a transient Postgres outage never drops a billing event), the billing
// fan-out emitter, and the resolved §11.7 audit-retention and grant-check
// schedule, recording each on the accumulator. It is an extracted
// per-concern step of buildStores.
//
// spec: §4.1 gateway subsystem seams; §11.2.1 billing failover; §11.7 audit
// retention.
