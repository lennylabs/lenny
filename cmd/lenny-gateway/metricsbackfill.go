// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"log"
	"os"
	"time"

	"github.com/lennylabs/lenny/pkg/clockinject"
	"github.com/lennylabs/lenny/pkg/driftmonitor"
	"github.com/lennylabs/lenny/pkg/gateway/coordfence"
	"github.com/lennylabs/lenny/pkg/gateway/dualstore"
	"github.com/lennylabs/lenny/pkg/gateway/extractionthreshold"
	"github.com/lennylabs/lenny/pkg/gateway/gatewaymetrics"
	"github.com/lennylabs/lenny/pkg/gateway/memorystore"
	memorypg "github.com/lennylabs/lenny/pkg/gateway/memorystore/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/subsystem"
	"github.com/lennylabs/lenny/pkg/gateway/tenantstore"
	"github.com/lennylabs/lenny/pkg/redisconn"
	"github.com/lennylabs/lenny/pkg/storerouter"
	"github.com/lennylabs/lenny/pkg/tenantkms"
)

// buildMetricsBackfill is the §4.1 composition-root build step (R1) for the
// gateway Prometheus metric registry and the wiring that can only be attached
// once the registry exists. It constructs gatewaymetrics, back-fills the
// metric emitters and observers onto the components the stores step built
// before the registry existed (the §10.2 JWT-signer breaker observer, the
// §12.6 scatter-gather collector, the §4.6.1 pod-binder counters, the §9.4
// MemoryStore observer, the §12.5 artifact-store callbacks, and the
// checkpointer histogram), emits the §16.5 / §25.13 startup-set configuration
// scalars the bundled alert expressions read via scalar(...), runs the §12.5
// MinIO catalog/KMS probe and the §12.8 MemoryStore erasure preflight, and
// constructs the §10.1 CoordinatorFence driver, the §13.3 NTP drift monitor,
// the §10.1 dual-store degraded-mode monitor, and the §4.1 per-subsystem
// metric family. It records gwMetrics, driftMonitor, dsMonitor,
// subsystemMetrics, and coordFencer on the accumulator so the later build
// steps read them back.
//
// spec: §4.1 gateway subsystem seams; §16.1 / §16.5 / §25.13 gateway metrics.
func (w *gatewayWiring) buildMetricsBackfill() {
	f := w.f
	dualStoreMaxSeconds := f.dualStoreMaxSeconds
	maxSessionsPerReplica := f.maxSessionsPerReplica
	delegationMaxOrphanTasksPerTenant := f.delegationMaxOrphanTasksPerTenant
	minReplicas := f.minReplicas
	streamCeiling := f.streamCeiling
	sessionEventReplayBufferDepth := f.sessionEventReplayBufferDepth
	auditSIEMEndpoint := f.auditSIEMEndpoint
	auditBatchingEnabled := f.auditBatchingEnabled
	billingCorrectionRateThreshold := f.billingCorrectionRateThreshold
	eventBusDropAlertThreshold := f.eventBusDropAlertThreshold
	postgresWriteCeilingIops := f.postgresWriteCeilingIops
	auditStartupChainCheckEntries := f.auditStartupChainCheckEntries
	gatewayQueueDepthThreshold := f.gatewayQueueDepthThreshold
	gatewayLatencyThresholdSeconds := f.gatewayLatencyThresholdSeconds
	credentialPoolLowThreshold := f.credentialPoolLowThreshold
	sloBurnRateFastMultiplier := f.sloBurnRateFastMultiplier
	sloBurnRateSlowMultiplier := f.sloBurnRateSlowMultiplier

	gwMetrics, err := gatewaymetrics.New()
	if err != nil {
		log.Fatalf("lenny-gateway: metrics: %v", err)
	}
	// spec: §10.1 lines 33-37 / §11.3 line 209 — the gateway-side
	// CoordinatorFence driver. On a resume re-bind the sessionserver
	// announces the session's coordination_generation to the pod; a
	// generation-stale rejection drives the retry/relinquish policy,
	// releasing the coordination lease when the coordinator gives up.
	// Wired only when the lease store exists (it needs Release for the
	// relinquish path); the metrics are now built so the counters move.
	if w.erasureLeaseStore != nil {
		w.coordFencer = coordfence.New(
			sessionGenerationReader{store: w.sessions},
			w.erasureLeaseStore,
			w.replica,
			gwMetrics,
			coordfence.Options{Logf: log.Printf},
		)
	}
	// spec: §10.2 line 225 — back-fill the JWTSigner breaker observer
	// with the freshly-built metrics so signing failures and circuit
	// transitions land on `lenny_gateway_kms_signing_errors_total` and
	// `lenny_gateway_kms_signing_circuit_state`. F-10.2.6.
	w.kmsBreakerObs.SetMetrics(gwMetrics)
	// spec: §12.6 line 560 — register the scatter-gather duration histogram
	// and shard-count gauge and attach them to the store router so the §16
	// ScatterGatherSlowQuery alert has a series. The router is built before
	// the metrics registerer, so the collector is wired here. F-12.6.18.
	if w.scatterRouter != nil {
		scatterMetrics, err := storerouter.NewScatterMetrics(gwMetrics.Registerer())
		if err != nil {
			log.Fatalf("lenny-gateway: scatter-gather metrics: %v", err)
		}
		w.scatterRouter.SetScatterMetrics(scatterMetrics)
	}
	// spec: §13.3 line 595 — NTP drift self-monitor. The source returns
	// the clockinject-injected offset for v1 (zero in production unless
	// an operator wires a real adjtimex/chrony probe). /healthz and any
	// downstream consumer (currently the embedded TokenService path,
	// which lives in lenny-token-service) consult driftMonitor.Degraded.
	// F-13.3.5.
	w.driftMonitor = driftmonitor.New(func() time.Duration {
		off, _ := clockinject.Offset()
		return off
	}, gwMetrics)
	// spec: §10.1 — dual-store degraded-mode monitor. It is active only
	// when both coordination stores are wired; an in-memory / single-store
	// dev posture has no "both down" condition to detect. The monitor
	// probes Postgres and Redis on a short cadence and, on detecting both
	// unreachable, pins lenny_dual_store_unavailable=1, broadcasts a
	// PLATFORM_DEGRADED SSE event to every active client stream, and gates
	// session.create with 503 + Retry-After: 10 (via DualStore on the
	// session-server Options). The per-replica dualStoreUnavailableMaxSeconds
	// countdown is anchored at detection. F-10.1.3.
	if w.pgPool != nil && w.redisClient != nil {
		pgPoolRef := w.pgPool
		redisRef := w.redisClient
		w.dsMonitor = &dualstore.Monitor{
			PostgresProbe: func(ctx context.Context) bool {
				pctx, cancel := context.WithTimeout(ctx, 2*time.Second)
				defer cancel()
				return pgPoolRef.Ping(pctx) == nil
			},
			RedisProbe: func(context.Context) bool {
				return redisconn.PingWithTimeout(redisRef, 2*time.Second) == nil
			},
			Gauge:          gwMetrics,
			Streams:        w.eventBus,
			MaxUnavailable: time.Duration(*dualStoreMaxSeconds) * time.Second,
			Logf:           func(format string, args ...any) { log.Printf(format, args...) },
		}
	}
	// §4.6.1: record fallback-claim skips on the gateway metrics registry.
	// Wired after gatewaymetrics.New() because the binder is constructed
	// earlier in the agent-namespace block.
	if w.podBinder != nil {
		w.podBinder.FallbackSkipped = gwMetrics.IncPodClaimFallbackSkipped
		// §5.2 line 519: record concurrent-mode slot-contention conflicts
		// on lenny_slot_assignment_conflict_total so operators can detect
		// pool under-sizing.
		w.podBinder.SlotConflict = gwMetrics.IncSlotAssignmentConflict
		// §5.2 line 12: record concurrent-workspace slot bind failures on
		// lenny_slot_failure_total (error_type, pool, k8s_pod_name).
		w.podBinder.SlotFailure = gwMetrics.IncSlotFailure
		// §5.2 line 521: record post-recovery slot-counter rehydration
		// events on lenny_slot_rehydration_total (pod, pool).
		w.podBinder.Rehydration = gwMetrics.IncSlotRehydration
		// §6.3 line 352 / §16.1 line 122: emit lenny_warmpool_claims_total
		// on each idle→claimed transition so deployers can read the
		// denominator of the SDK-warm demotion-rate ratio.
		w.podBinder.ClaimAccepted = gwMetrics.IncWarmpoolClaim
		// §6.1 line 34 / §6.3 line 352 / §16.1 line 121: emit
		// lenny_warmpool_sdk_demotions_total (the demotion-rate numerator)
		// and lenny_warmpool_sdk_demotion_duration_seconds (the SDK
		// teardown penalty) on each SDK-warm demotion.
		w.podBinder.SDKDemotion = gwMetrics.RecordSDKDemotion
	}
	// §16.1 lines 51, 53, 55: emit credential-lease assignment, lease
	// duration, and pool-utilization telemetry from the in-process
	// assignment service. The Token Service client path emits its own
	// §16.1 metrics on its registry.
	if w.inProcessAssign != nil {
		w.inProcessAssign.SetMetrics(gwMetrics)
	}
	// spec: §9.4 line 200 / §16.1 lines 151-154 — wire the MemoryStore
	// Observer once gatewaymetrics is ready. The §16.1 `backend` label
	// is the bound implementation tag (`postgres` for the pgvector
	// backend, `memory` for the in-process test backend). F-9.4.1 /
	// F-9.4.6.
	if w.memories != nil {
		obs := memoryStoreObserver{metrics: gwMetrics, backend: w.memoryBackendLabel}
		switch s := w.memories.(type) {
		case *memorystore.InMemory:
			s.SetObserver(obs)
		case *memorypg.Store:
			s.SetObserver(obs)
		}
	}
	// spec: §12.8 lines 743-758 — MemoryStore erasure preflight (stub
	// detection, defense-in-depth layer 2). Before serving traffic, seed a
	// probe memory under the reserved (__preflight__, __preflight_user__)
	// scope, erase it, and assert it does not survive. A backend whose
	// DeleteByUser / DeleteByTenant satisfies the interface but silently
	// no-ops makes the gateway refuse to start, so a GDPR erasure can never
	// report success while memories persist. Skipped when memory.enabled is
	// false (no store wired). F-9.4.3 / F-12.1.4 / F-12.2.10 / F-12.8.9.
	if w.memories != nil {
		preflightCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		preflightErr := memorystore.ValidateMemoryStoreErasure(preflightCtx, w.memories)
		cancel()
		if preflightErr != nil {
			log.Fatalf("FATAL: MemoryStore preflight failed — configured backend (%s) does not honor DeleteByUser; GDPR erasure would silently succeed while leaving memories in place (Section 12.8): %v", w.memoryBackendLabel, preflightErr)
		}
	}
	// §4.1 / §16.1: emit the per-replica capacity ceiling as a startup-set
	// gauge so the §16.5 GatewaySessionBudgetNearExhaustion alert can
	// divide active sessions by it. The spec requires both delivery_mode
	// values be reported per replica; until a separate
	// maxSessionsPerReplicaProxyMode setting exists, both labels carry
	// the same configured value so capacity-planning dashboards have a
	// non-NaN value for either mode.
	if *maxSessionsPerReplica <= 0 {
		log.Fatalf("lenny-gateway: --max-sessions-per-replica must be > 0 (got %d) (§4.1)", *maxSessionsPerReplica)
	}
	gwMetrics.SetMaxSessionsPerReplica("direct", *maxSessionsPerReplica)
	gwMetrics.SetMaxSessionsPerReplica("proxy", *maxSessionsPerReplica)

	// spec: §8.10 line 1103 + §16.5 OrphanTasksPerTenantHigh — publish the
	// configured maxOrphanTasksPerTenant cap so the alert's
	// `scalar(lenny_max_orphan_tasks_per_tenant)` denominator resolves to
	// the live ceiling. The Helm-driven flag is the source of truth; the
	// sessionserver constructor also receives the same value via the
	// Options.MaxOrphanTasksPerTenant field so the detach-cascade fallback
	// path and the alert evaluate against one shared cap. F-8.10.10 /
	// F-8.10.13.
	gwMetrics.SetMaxOrphanTasksPerTenant(*delegationMaxOrphanTasksPerTenant)

	// §4.4 line 254 — late-binding the checkpointer's duration
	// histogram emitter to the freshly-constructed gateway metrics.
	// The checkpointer is constructed before gwMetrics so the Sealer
	// can flow into the session-server, so the Metrics field is wired
	// here once the registry is live.
	if w.checkpointSvc != nil {
		w.checkpointSvc.Metrics = gwMetrics
	}

	// §12.5 ll. 282/303 — wire the artifact store's fail-closed T4
	// KMS-unavailable callback and its retry-exhausted upload-error
	// callback to the gateway metrics emitter, regardless of which
	// §17.9.3 backend (MinIO, S3, GCS, Azure) serves the bucket. Every
	// ErrClassificationControlViolation the store raises bumps
	// `lenny_checkpoint_storage_failure_total{reason="kms_unavailable"}`
	// so the CheckpointStorageUnavailable alert fires under the
	// outage; every retry-exhausted upload bumps
	// `lenny_artifact_upload_error_total` so the §16.5 MinIOUnavailable
	// alert fires from one source of truth. The handler also logs the
	// KMS rejection at INFO so operators see the tenant id without
	// spelunking through the bucket-side access logs. F-17.5.1.
	if sink, ok := w.objectStore.(artifactMetricsSink); ok {
		sink.SetOnKMSUnavailable(func(tenantID string) {
			gwMetrics.IncCheckpointKMSUnavailable()
			log.Printf("lenny-gateway: §12.5 ll. 303 CLASSIFICATION_CONTROL_VIOLATION: tenant=%s KMS key unavailable", tenantID)
		})
		sink.SetOnArtifactUploadError(func(tenantID, errorType string) {
			gwMetrics.IncArtifactUploadError(tenantID, errorType)
		})
	}
	// §12.9 line 1048 — the in-memory / filesystem backends reject a T4
	// tenant's write (they cannot envelope-encrypt at rest). Wire the
	// rejection to the tier_store_mismatch reason of the same
	// checkpoint-storage-failure counter so the misconfiguration is
	// visible to operators.
	if sink, ok := w.objectStore.(tierMismatchSink); ok {
		sink.SetOnTierStoreMismatch(func(tenantID string) {
			gwMetrics.IncCheckpointTierStoreMismatch()
			log.Printf("lenny-gateway: §12.9 line 1048 CLASSIFICATION_CONTROL_VIOLATION: tenant=%s workspaceTier requires envelope encryption but the artifact store is not configured for it (tier_store_mismatch)", tenantID)
		})
	}

	// §12.8 line 735 / §12.5 ll. 297 — the durable artifact_store
	// catalog reader and the startup T4 KMS probe are MinIO-specific
	// (the in-memory and cloud backends do not expose SetCatalog), so
	// they stay gated on the concrete MinIO store.
	if w.minioStore != nil {
		// §12.8 line 735 — wire the durable artifact_store catalog as
		// the legal-hold source of truth on DeleteBySession. The
		// in-memory legalHolds sync.Map remains a v1 fallback for the
		// catalog-less dev gateway; production reads the durable row.
		if w.artifactCatalog != nil {
			w.minioStore.SetCatalog(w.artifactCatalog)
			log.Printf("lenny-gateway: §12.8 line 735 durable legal-hold reader wired into MinIO blob store")
		}

		// §12.5 ll. 297 startup KMS probe: when at least one T4 tenant
		// is configured, probe a sample alias so a chronic
		// misconfiguration surfaces in startup logs. The gateway does
		// NOT fail startup — production may bring the gateway up before
		// every KMS alias is provisioned; the warning is the operator
		// signal.
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			rows, err := w.tenants.List(ctx, tenantstore.ListFilter{})
			if err != nil {
				log.Printf("lenny-gateway: §12.5 startup T4 KMS probe: list tenants: %v", err)
				return
			}
			for _, t := range rows {
				if t.WorkspaceTier != tenantkms.WorkspaceTierT4 {
					continue
				}
				alias := tenantkms.AliasFor(t.ID)
				if _, perr := w.kmsProvider.CurrentKEKVersion(ctx, alias); perr != nil {
					log.Printf("lenny-gateway: §12.5 startup T4 KMS probe WARN: tenant=%s alias=%s unreachable: %v",
						t.ID, alias, perr)
				} else {
					log.Printf("lenny-gateway: §12.5 startup T4 KMS probe OK: tenant=%s alias=%s",
						t.ID, alias)
				}
				return // probe a single sample tenant only
			}
		}()
	}

	// §4.1 / §16.5: emit the configuration scalars the §16.5
	// GatewayNoHealthyReplicas and GatewayActiveStreamsHigh alert
	// expressions read via scalar(...). Each gauge is emitted per
	// replica; for replicaCount the spec recording rule sum() over
	// the fleet yields the fleet-wide ready-replica numerator.
	if *minReplicas <= 0 {
		log.Fatalf("lenny-gateway: --min-replicas must be > 0 (got %d) (§4.1 / §16.5)", *minReplicas)
	}
	if *streamCeiling <= 0 {
		log.Fatalf("lenny-gateway: --stream-ceiling must be > 0 (got %d) (§4.1 / §16.5)", *streamCeiling)
	}
	// spec: §10.4 line 389 — accepted range 64..4096 events.
	if *sessionEventReplayBufferDepth < 64 || *sessionEventReplayBufferDepth > 4096 {
		log.Fatalf("lenny-gateway: --session-event-replay-buffer-depth must be in [64, 4096] (got %d) (§10.4 line 389)", *sessionEventReplayBufferDepth)
	}
	gwMetrics.SetMinReplicas(*minReplicas)
	gwMetrics.SetStreamCeiling(*streamCeiling)
	gwMetrics.SetReplicaCount(1)
	// §16.4 / §16.5: emit the audit alert-support scalars so the
	// AuditSIEMNotConfigured and AuditRetentionLow expressions resolve.
	// siemConfigured is the suppression term (1 when audit.siem.endpoint
	// is set), retentionDays is the resolved §16.4 window, and
	// envProduction gates both alerts to production. F-16.4.9; F-16.4.10.
	gwMetrics.SetAuditSIEMConfigured(*auditSIEMEndpoint != "")
	gwMetrics.SetAuditRetentionDays(w.effectiveAuditRetentionDays)
	gwMetrics.SetEnvProduction(os.Getenv("LENNY_ENV") == "production")
	// spec: §12.3 line 99 — in production with T2 audit batching enabled
	// but no SIEM endpoint, there is no external durable copy to recover
	// buffered T2 events from on a crash. Warn at startup and emit the
	// AuditBatchingNoSIEM counter. F-12.3.15.
	if auditBatchingNoSIEM(os.Getenv("LENNY_ENV"), *auditBatchingEnabled, *auditSIEMEndpoint != "") {
		log.Printf("lenny-gateway: WARNING: Audit batching is enabled for T2 events but no SIEM is configured — buffered T2 audit events will be lost on gateway crash")
		gwMetrics.IncAuditBatchingNoSIEM()
	}
	// spec: §11.2.1 line 187 — emit the configured BillingCorrectionRateHigh
	// threshold as a startup-set scalar gauge so the alert expression in
	// pkg/alerting/rules can read it via scalar(lenny_billing_correction_rate_threshold).
	if *billingCorrectionRateThreshold < 0 || *billingCorrectionRateThreshold > 1 {
		log.Fatalf("lenny-gateway: --billing-correction-rate-threshold must be in [0, 1] (got %v) (§11.2.1 / §16.5)", *billingCorrectionRateThreshold)
	}
	gwMetrics.SetBillingCorrectionRateThreshold(*billingCorrectionRateThreshold)

	// spec: §12.6 line 683 / §16.5 — publish the operator-configured
	// EventBusPublishDropped per-minute threshold so the bundled alert's
	// scalar(lenny_event_bus_drop_alert_threshold) resolves to the
	// eventBus.dropAlertThreshold Helm value rather than a literal. A
	// non-positive value would make the alert fire on any drop, so it is
	// clamped to the spec default. F-12.6.23.
	if *eventBusDropAlertThreshold <= 0 {
		log.Fatalf("lenny-gateway: --eventbus-drop-alert-threshold must be a positive per-minute rate (got %d) (§12.6 line 683)", *eventBusDropAlertThreshold)
	}
	gwMetrics.SetEventBusDropAlertThreshold(float64(*eventBusDropAlertThreshold))

	// §12.3 line 76 — wire the billing_flush_pressure callback now that
	// the metric registry exists (the billing Pipeline was constructed
	// earlier). F-12.3.13.
	w.billingPipeline.SetFlushPressureHook(gwMetrics.IncBillingFlushPressure)
	// §12.3 line 123 — emit the configured Postgres sustained write-IOPS
	// ceiling so the §16.5 PostgresWriteSaturation alert resolves
	// scalar(lenny_postgres_write_ceiling_iops). F-12.3.8.
	if *postgresWriteCeilingIops <= 0 {
		log.Fatalf("lenny-gateway: --postgres-write-ceiling-iops must be > 0 (got %v) (§12.3 line 123)", *postgresWriteCeilingIops)
	}
	gwMetrics.SetPostgresWriteCeilingIops(*postgresWriteCeilingIops)
	// §12.3 line 101 — startup chain-continuity check. After Postgres is
	// reachable the gateway re-verifies the most recent
	// audit.startupChainCheckEntries rows of each tenant's hash chain,
	// emits lenny_audit_chain_integrity_total per tenant, and logs a WARN
	// per detected gap. It never refuses to start — a gap is a compliance
	// signal. The check reads audit_log from the instance it lives on —
	// the separate §12.3 billing/audit pool when configured. F-12.3.9 /
	// F-12.3.5.
	if w.pgPool != nil {
		chainPool := w.pgPool
		if w.billingAuditPool != nil {
			chainPool = w.billingAuditPool
		}
		runStartupChainContinuityCheck(context.Background(), chainPool, *auditStartupChainCheckEntries, gwMetrics)
	}

	// spec: §25.13 line 4737 / §16.5 — emit the configured Tier preset
	// for the §25.13 tier-dependent thresholds (gateway queue depth,
	// gateway p95 latency, credential pool utilisation). The
	// corresponding alert expressions read each value via scalar(...),
	// so a tier preset tightening the threshold flows through to the
	// bundled manifest without re-rendering the rule body. F-25.13.2.
	if *gatewayQueueDepthThreshold < 0 {
		log.Fatalf("lenny-gateway: --gateway-queue-depth-threshold must be >= 0 (got %v) (§25.13 line 4737)", *gatewayQueueDepthThreshold)
	}
	gwMetrics.SetGatewayQueueDepthThreshold(*gatewayQueueDepthThreshold)
	if *gatewayLatencyThresholdSeconds < 0 {
		log.Fatalf("lenny-gateway: --gateway-latency-threshold-seconds must be >= 0 (got %v) (§25.13 line 4737)", *gatewayLatencyThresholdSeconds)
	}
	gwMetrics.SetGatewayLatencyThresholdSeconds(*gatewayLatencyThresholdSeconds)
	if *credentialPoolLowThreshold < 0 || *credentialPoolLowThreshold > 1 {
		log.Fatalf("lenny-gateway: --credential-pool-low-threshold must be in [0, 1] (got %v) (§25.13 line 4737)", *credentialPoolLowThreshold)
	}
	gwMetrics.SetCredentialPoolLowThreshold(*credentialPoolLowThreshold)
	// §16.5 line 640: mirror the operator-configured burn-rate window
	// multipliers onto the lenny_slo_burn_rate_{fast,slow}_multiplier
	// gauges every burn-rate alert reads via scalar(...). Both must be
	// positive — a non-positive multiplier would make every burn-rate
	// alert fire continuously (ratio > 0 always exceeds it). F-16.5.3.
	if *sloBurnRateFastMultiplier <= 0 || *sloBurnRateSlowMultiplier <= 0 {
		log.Fatalf("lenny-gateway: --slo-burn-rate-fast-multiplier and --slo-burn-rate-slow-multiplier must be > 0 (got %v / %v) (§16.5 line 640)", *sloBurnRateFastMultiplier, *sloBurnRateSlowMultiplier)
	}
	gwMetrics.SetSLOBurnRateMultipliers(*sloBurnRateFastMultiplier, *sloBurnRateSlowMultiplier)

	// §4.1 extractionThresholds: read the configured per-subsystem
	// thresholds from LENNY_EXTRACTION_THRESHOLD_* env vars (rendered
	// by charts/lenny/templates/gateway-deployment.yaml from
	// gateway.extractionThresholds Helm values) and emit each one
	// on the lenny_gateway_extraction_threshold gauge so the values
	// used for an extraction decision are auditable against /metrics.
	extractionthreshold.FromEnv().Emit(gwMetrics)

	// §4.1 per-subsystem metric family. Register the
	// lenny_gateway_subsystem_{request_duration_seconds, queue_depth,
	// circuit_state, errors_total} vectors against the gateway's
	// shared private registry so a Subsystem with its DoObserved path
	// wired surfaces samples on /metrics. The §4.1 alerts in §16.5
	// (GatewaySubsystemCircuitOpen, GatewayQueueDepthHigh,
	// GatewayLatencyHigh) read these vectors via the `subsystem` label.
	subsystemMetrics, err := subsystem.NewMetrics(gwMetrics.Registerer())
	if err != nil {
		log.Fatalf("lenny-gateway: subsystem metrics: %v", err)
	}

	w.gwMetrics = gwMetrics
	w.subsystemMetrics = subsystemMetrics
}
