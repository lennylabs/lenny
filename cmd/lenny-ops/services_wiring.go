// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"encoding/json"
	"log"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/kubernetes"

	"github.com/lennylabs/lenny/pkg/events"
	"github.com/lennylabs/lenny/pkg/observability/metrics"
	"github.com/lennylabs/lenny/pkg/ops/backup"
	"github.com/lennylabs/lenny/pkg/ops/coordination"
	"github.com/lennylabs/lenny/pkg/ops/doctor"
	"github.com/lennylabs/lenny/pkg/ops/operations"
	"github.com/lennylabs/lenny/pkg/ops/opsinventory"
	"github.com/lennylabs/lenny/pkg/ops/opsservice"
	"github.com/lennylabs/lenny/pkg/ops/platformassets"
	"github.com/lennylabs/lenny/pkg/ops/upgradeservice"
)

// buildSelfHealthAndLocks constructs the §25.4 self-health checks, the
// in-memory lock-tier policy, the tiered remediation-lock service, and the
// Postgres-Redis clock-skew sampler. spec: §25.4.
func (w *opsWiring) buildSelfHealthAndLocks() {
	// The §25.4 self-health checks every replica runs.
	var disc discovery.DiscoveryInterface
	if w.clientset != nil {
		disc = w.clientset.Discovery()
	}
	w.selfChecks = map[string]opsservice.SelfCheck{
		opsservice.CheckPostgresPool:   opsservice.PostgresPoolCheck(w.pgPool),
		opsservice.CheckRedisLag:       opsservice.RedisLagCheck(w.redisClient, nil),
		opsservice.CheckWebhookBacklog: opsservice.WebhookBacklogCheck(w.webhook.Backlog),
		opsservice.CheckK8sAPI:         opsservice.K8sAPICheck(disc),
		opsservice.CheckMemoryPressure: opsservice.MemoryPressureCheck(*w.f.memoryLimitBytes),
		opsservice.CheckGatewayAuth:    opsservice.GatewayAuthCheck(gatewayAuthProbe(w.gwClient)),
	}
	// §25.8 cert-manager probe: reports the worst Lenny-managed certificate
	// expiry state. A nil source (no dynamic client) reports healthy with a
	// "not configured" note, matching the deployer-provided-Secret model
	// where there are no cert-manager Certificate resources to probe.
	if w.dynClient != nil {
		w.selfChecks[opsservice.CheckCertManager] = opsservice.CertManagerCheck(
			certManagerSource{
				client:    w.dynClient,
				namespace: envOr("POD_NAMESPACE", *w.f.leaderElectNS),
				onExpiry:  setCertExpiry,
			},
		)
	} else {
		w.selfChecks[opsservice.CheckCertManager] = opsservice.CertManagerCheck(nil)
	}

	// §25.4 lines 2206-2212: the in-memory (Tier-3) lock policy. The mode
	// is validated at startup so an operator typo fails fast rather than
	// silently selecting an unintended safety posture. Under the
	// single-replica-only default, a multi-replica deployment rejects
	// uncoordinated in-memory acquisitions; the replica count comes from
	// the lenny-ops Service Endpoints (re-checked every 30s). Without a
	// cluster connection (single-process dev) the counter is nil and the
	// policy treats the deployment as a single replica.
	memTier, ok := coordination.ParseMemoryTier(*w.f.locksMemoryTier)
	if !ok {
		log.Fatalf("lenny-ops: invalid --locks-memory-tier %q: want single-replica-only, always, or never", *w.f.locksMemoryTier)
	}
	if w.clientset != nil {
		ns := envOr("POD_NAMESPACE", *w.f.leaderElectNS)
		ep := opsservice.NewEndpointsReplicaCounter(w.clientset.CoreV1(), ns, *w.f.opsServiceName)
		// §25.4 line 2208: the startup lookup runs synchronously so the
		// policy has a real count before the first acquire; the 30s
		// re-check loop then keeps it current.
		if err := ep.Refresh(w.ctx); err != nil {
			log.Printf("lenny-ops: §25.4 startup replica-count lookup failed (assuming single replica): %v", err)
		}
		go ep.Run(w.ctx, opsservice.ReplicaPollInterval)
		w.replicaCounter = ep
	}
	w.lockCoordination = coordination.NewCoordinationGate(memTier, w.replicaCounter)
	log.Printf("lenny-ops: §25.4 remediation-lock memoryTier=%s", memTier)

	// The §25.4 tiered remediation-lock service: the Postgres Tier 1 store
	// (ops_remediation_locks, migration 0121) and the Redis Tier 2 store
	// (ops:lock:{scope}) over the always-present in-memory Tier 3 store,
	// with outage-epoch reconciliation and deterministic split-brain
	// resolution. The gate enforces ops.locks.memoryTier on the Tier 3
	// fall-through; the §25.4 lock metrics and audit events are emitted from
	// the service. The HTTP layer applies the §25.4 scope-based
	// authorization control before the service. F-25.4.6.
	w.lockSvc, w.lockMetrics = buildLockService(w.pgPool, w.redisClient, w.lockCoordination,
		prometheus.DefaultRegisterer, w.opsEmitter, w.auditRecorder, w.replicaID)

	// §25.4 line 2280: the Postgres-Redis clock-skew sampler. It reads
	// both dependency clocks and publishes the absolute skew on
	// lenny_ops_clock_skew_seconds, the producer the OpsClockSkewExceeded
	// alert needs. nil when either dependency is absent (single-process
	// degraded mode), in which case the reconciler loop is not registered.
	w.clockSkewSampler = buildClockSkewSampler(w.pgPool, w.redisClient, w.lockMetrics)
}

// buildOpsServices constructs the §25.11 backup service, the §25.4
// escalation service, the §25.10 drift service, and the §25.6 diagnostic and
// doctor services. spec: §25.4, §25.6, §25.10, §25.11.
func (w *opsWiring) buildOpsServices() {
	// The §25.11 BackupService. lenny-ops orchestrates backup/restore
	// Kubernetes Jobs through it. The Postgres-backed ops_backups store,
	// the client-go Job launcher, and the §25.4-remediation-lock-backed
	// restore:platform lock are selected when those seams are wired; the
	// in-memory store, fake launcher, and in-memory locker keep the §25.11
	// endpoints serving in a single-process degraded mode. Built after
	// lockSvc so the restore lock can adapt it. F-17.3.4 / F-25.11.3/.4.
	var backupClientset kubernetes.Interface
	if w.clientset != nil {
		backupClientset = w.clientset
	}
	// §12.8 lines 932-936: the per-region backup endpoint map and the
	// shard→region resolver. A parse failure (or a regions map with no
	// shard map) is a fatal config error so lenny-ops fails fast rather
	// than failing every backup at run time.
	backupRegionMap, backupShardResolver, err := parseBackupRegions(*w.f.backupRegions, *w.f.backupShardRegions)
	if err != nil {
		log.Fatalf("lenny-ops: §12.8 per-region backup config: %v", err)
	}
	w.backupSvc, w.backupJobs = buildBackupService(*w.f.production, backupDeps{
		Pool:            w.pgPool,
		Clientset:       backupClientset,
		Locks:           w.lockSvc,
		Recorder:        w.auditRecorder,
		Namespace:       envOr("POD_NAMESPACE", *w.f.leaderElectNS),
		LauncherImage:   *w.f.backupImage,
		MinIOEndpoint:   *w.f.backupMinIOEndpoint,
		MinIOBucket:     *w.f.backupMinIOBucket,
		KMSKeyID:        *w.f.backupKMSKeyID,
		ReportDSNSecret: *w.f.backupReportDSNSecret,
		Regions:         backupRegionMap,
		ShardRegions:    backupShardResolver,

		IncludeSensitiveTables: *w.f.backupIncludeSensitive,
		ExcludeTables:          splitCSV(*w.f.backupExcludeTables),
		RedactColumns:          splitCSV(*w.f.backupRedactColumns),
	})
	// §25.11 Metrics table: the backup/restore outcome series
	// (lenny_backup_total, lenny_backup_size_bytes, lenny_backup_duration_seconds,
	// lenny_restore_total, lenny_restore_duration_seconds) derived from the
	// ops_backups / ops_restore_state rows at scrape time, so they survive a
	// restart and need no in-process completion hook. Registered on the
	// default registry so the §16.9 /metrics exposition scrapes them. F-25.11.7.
	metrics.MustRegister(prometheus.DefaultRegisterer, backup.NewMetricsCollector(w.backupSvc))

	// The §25.4 escalation service, the §25.10 configuration-drift
	// service, and the §25.6 DiagnosticService. Each runs against an
	// in-memory or unconfigured backing store in this single-process
	// degraded mode so the §25 endpoints serve and an agent can exercise
	// them; the durable backing stores are documented seams.
	w.escalationSvc = buildEscalationService(
		newStreamEscalationEmitter(w.opsEmitter, w.replicaID), w.pgPool, w.redisClient,
		w.auditRecorder,
		escalationConfig{
			RequireDurable:                *w.f.escalationRequireDurable,
			ReconciliationWritesPerSecond: *w.f.escalationReconcileWPS,
		},
	)
	w.driftSvc = buildDriftService(driftServiceConfig{
		StaleWarningDays:        *w.f.driftSnapshotStaleWarningDays,
		RunningStateCacheTTLSec: *w.f.driftRunningStateCacheTTLSeconds,
	}, w.pgPool, w.gwClient, w.opsEmitter, w.auditRecorder)
	diagnosticDeps := diagnosticSourceDeps{
		Pool:           w.pgPool,
		Gateway:        w.gwClient,
		Probes:         w.probes,
		ProbeTimeout:   2 * time.Second,
		AgentNamespace: *w.f.agentNamespace,
	}
	// Assign through the nil check so a nil *kubernetes.Clientset does not
	// become a non-nil kubernetes.Interface (the typed-nil interface trap).
	if w.clientset != nil {
		diagnosticDeps.Clientset = w.clientset
	}
	w.diagnosticSvc = buildDiagnosticService(diagnosticDeps)

	// §25.6 doctor auto-remediation orchestrator backing POST
	// /v1/admin/diagnostics/run[?fix=true]. Built only when a Kubernetes
	// client is available; otherwise the endpoint reports 503. F-25.6.2.
	releaseNS := envOr("POD_NAMESPACE", *w.f.leaderElectNS)
	dDeps := doctorDeps{
		ReleaseNS:    releaseNS,
		AllowedFixes: splitCSV(*w.f.doctorAllowedFixes),
		FixTimeout:   time.Duration(*w.f.doctorFixTimeout) * time.Second,
		Helm:         newHelmRenderSource(*w.f.doctorRenderDir, releaseNS),
		// The §25.6.1 pool-diagnosis service supplies warmPoolStuckReplenish
		// its DEMAND_EXCEEDS_SUPPLY classification. buildDiagnosticService
		// returns a non-nil *Service, so this never traps a typed nil.
		PoolDiagnosis: w.diagnosticSvc,
		Audit: func(ev doctor.Event) {
			w.auditRecorder.Record(string(ev.Type), ev.Fields, time.Time{})
		},
	}
	if *w.f.maintenanceMode {
		dDeps.MaintenanceMode = func() bool { return true }
	}
	if w.clientset != nil {
		dDeps.Clientset = w.clientset
	}
	if w.dynClient != nil {
		dDeps.Dynamic = w.dynClient
	}
	w.doctorSvc = buildDoctorService(dDeps)
}

// buildUpgradeSubsystem constructs the §25.8 release-channel publisher, the
// platform-upgrade orchestrator and its checker, the OpsRoll startup hook,
// the runtime registry, the preflighter, the watchdog, the version
// aggregator, and the platform-config service, and assembles the leader-only
// cron jobs. spec: §25.8.
func (w *opsWiring) buildUpgradeSubsystem() {
	// The §25.8 release-channel manifest publisher. Loaded from the
	// operator-supplied key + manifest paths. When no key is configured
	// the publisher is nil and GET /v1/latest is unmapped; lenny-ops
	// will not silently serve unsigned responses on the canonical
	// release-channel path.
	w.releaseChannelPub = buildReleaseChannelPublisher(
		*w.f.releaseChannelKeyPath, *w.f.releaseChannelKeyID,
		*w.f.releaseChannelPrevKeyPath, *w.f.releaseChannelPrevKeyID,
		*w.f.releaseChannelManifestPath,
	)

	// The §25.8 platform-upgrade orchestrator (F-10.5.7) and its
	// upgrade-check client (F-10.5.5). The orchestrator drives the §25.8
	// phase machine and emits the §16.7 platform-upgrade lifecycle audit
	// events; the checker queries the operator-supplied release manifest
	// and emits platform_upgrade_available. The platform-upgrade-check
	// cron (§25.4 line 1338) runs leader-only alongside the backup jobs.
	// §25.2 historical baselines for the canonical Progress Envelope:
	// Postgres-backed (ops_operation_baselines, migration 0128) when a pool
	// is available, in-memory otherwise. The upgrade orchestrator records a
	// completion into it, and the Operations Inventory reads it to derive
	// the historical_p50 ETA. F-25.2.7.
	w.baselineStore = buildBaselineStore(w.pgPool)
	// §25.8 platform_upgrade_state store, shared by the orchestrator and the
	// preflighter so both observe the same in-flight upgrade.
	w.upgradeStore = buildUpgradeStore(w.pgPool)
	w.upgradeSvc = buildUpgradeService(w.upgradeStore, w.driftSvc, w.opsEmitter, w.baselineStore, w.auditRecorder)
	// §25.8 lines 3508,3511 + §25.10 line 3788: the new-pod OpsRoll startup
	// path. When this binary started as the new lenny-ops pod during an
	// upgrade's OpsRoll phase (current_phase==OpsRoll and the persisted
	// target_version matches this binary's compiled-in version), it stamps
	// the ops_healthy heartbeat, writes bootstrap_seed_snapshot_target from
	// the rendered Helm values, then self-advances OpsRoll→CRDUpdate. The
	// hook is version-gated and a no-op outside that condition, so an
	// ordinary (non-upgrade) start runs it harmlessly. A hook failure is
	// logged and does not abort startup: the operator's next proceed and the
	// OpsRoll watchdog still govern the upgrade.
	runOpsRollStartupHook(w.ctx, upgradeStartupHook{
		Upgrades:   w.upgradeSvc,
		Snapshot:   w.driftSvc,
		ConfigMaps: configMapsGetter(w.clientset),
		Namespace:  envOr("POD_NAMESPACE", *w.f.leaderElectNS),
		Version:    buildVersion,
		ValuesCM:   *w.f.driftHelmValuesConfigMap,
		ValuesKey:  *w.f.driftHelmValuesKey,
		WrittenBy:  "lenny-ops",
	})
	w.upgradeChecker = buildUpgradeChecker(*w.f.releaseChannelManifestPath, buildVersion, w.pgPool, w.opsEmitter, w.auditRecorder)
	// §25.8 air-gap item 5 (line 3425): the CRD manifests and migration SQL
	// the upgrade's CRDUpdate/SchemaMigration phases need are compiled into
	// this binary (pkg/ops/platformassets), so an air-gapped install pulls
	// no schema/CRD assets from the release channel. Log the inventory so an
	// operator can confirm the assets travelled with the binary.
	if crdCount, migCount, err := platformassets.Inventory(); err == nil {
		log.Printf("lenny-ops: embedded platform assets: %d CRD manifests, %d migrations (air-gap ready)", crdCount, migCount)
	}
	// §25.8 runtime registry API: the chart platform.registry.* values are
	// the base; a runtime PUT overlays them (Postgres-backed when a pool is
	// present). Also feeds the upgrade preflight's image-plan resolution.
	w.registrySvc = buildRegistryService(w.pgPool, registryBaseConfig(
		*w.f.registryURL, *w.f.registryPullSecret, *w.f.registryRequireDigest, *w.f.registryOverrides,
	), w.auditRecorder)
	// §25.8 upgrade preflight (Phase 1 safety gates) and OpsRoll watchdog.
	w.upgradePreflighter = buildPreflighter(w.upgradeStore, w.pgPool)
	w.upgradeWatchdog = buildWatchdog(w.upgradeSvc, upgradeservice.WatchdogConfig{
		OpsRollTimeout:        time.Duration(*w.f.opsRollTimeout) * time.Second,
		GatewayRollTimeout:    time.Duration(*w.f.gatewayRollTimeout) * time.Second,
		ControllerRollTimeout: time.Duration(*w.f.controllerRollTimeout) * time.Second,
	}, w.opsEmitter)
	// §25.8 live upgrade gauges (phase + duration): a collector that reads
	// the orchestrator's singleton at scrape time so the gauges advance
	// without a background goroutine. Registered on the default registry
	// so the §16.9 /metrics exposition scrapes them.
	metrics.MustRegister(prometheus.DefaultRegisterer, upgradeservice.NewMetricsCollector(w.upgradeSvc))
	// §25.8 GET /v1/admin/platform/version/full aggregator over the
	// component sources lenny-ops can reach; it also raises the
	// lenny_platform_version_drift gauge on each aggregation.
	w.versionAggregator = buildVersionAggregator(
		buildVersion, *w.f.gatewayURL, w.gatewayHTTP, w.pgPool, w.clientset,
		envOr("POD_NAMESPACE", *w.f.leaderElectNS),
	)
	// §25.8 config diff/apply: the operator surface over the gateway's own
	// config API. Wired only when a gateway client exists; otherwise the
	// routes stay unmapped (404).
	w.platformConfigSvc = buildPlatformConfigService(w.gwClient, w.auditRecorder)
	w.cronJobs = append(w.backupJobs, upgradeCheckJob(w.upgradeChecker), versionDriftJob(w.versionAggregator),
		upgradeWatchdogJob(w.upgradeWatchdog), deliveryRetentionJob(w.delivery.Store))
}

// buildInventoryAndIdempotency constructs the §25.4 Operations Inventory and
// its progress observer, the §25.4 idempotency store, and the §25.4 / §25.13
// bundle-rules reconciler. spec: §25.2, §25.4, §25.13.
func (w *opsWiring) buildInventoryAndIdempotency() {
	// §25.4 Operations Inventory: a scatter-gather view over the wired
	// subsystem sources. The lock, escalation, and platform-upgrade
	// adapters project their live records; the §25.10 drift reconcile
	// tracker is already an operations.Source. The backup/restore,
	// idempotency, and webhook-delivery kinds plug in as their subsystems
	// expose enumeration. F-25.4.3.
	// spec: §25.4 (Operations Inventory `resources.audit`) — the
	// gateway-resident §25.9 audit-events query the audit link targets is
	// joined to the gateway base URL so the discovery hop resolves against
	// the gateway rather than 404ing on lenny-ops's origin. F-COV-1.
	gatewayURL := *w.f.gatewayURL
	w.inventory = operations.New(
		opsinventory.NewLockSource(w.lockSvc, gatewayURL),
		opsinventory.NewEscalationSource(w.escalationSvc, gatewayURL),
		opsinventory.NewUpgradeSource(w.upgradeSvc, gatewayURL),
		w.driftSvc.ReconcileSource(),
	)
	// §25.2 lines 357-396: enrich every in-progress operation's Progress
	// with the canonical ETA (historical_p50 from the baseline store) and
	// the cadence-relative stalledForSeconds on each List/Get. F-25.2.7.
	w.inventory.SetProgressBaselines(time.Now, w.baselineStore)
	// §25.2 lines 399-401 operations-observe loop: maintain the
	// lenny_ops_operations_stalled gauge (OperationStalled alert backing)
	// and emit operation_progressed on step transitions and percent-
	// threshold crossings. Runs leader-only via Reconcilers. F-25.2.7 /
	// F-25.2.14.
	w.operationsObserver = opsinventory.NewObserver(w.inventory, setOperationsStalled,
		func(ctx context.Context, ev opsinventory.ProgressUpdate) {
			payload, err := json.Marshal(map[string]any{
				"operationId":       ev.OperationID,
				"kind":              ev.Kind,
				"percent":           ev.Percent,
				"completedSteps":    ev.CompletedSteps,
				"totalSteps":        ev.TotalSteps,
				"currentStep":       ev.CurrentStep,
				"currentStepDetail": ev.CurrentStepDetail,
				"crossedThresholds": ev.CrossedThresholds,
				"stepTransition":    ev.StepTransition,
			})
			if err != nil {
				return
			}
			_ = w.opsEmitter.Emit(ctx, events.OperationalEvent{
				Type:            events.EventOperationProgressed.CloudEventsType(),
				Subject:         "operation/" + ev.OperationID,
				DataContentType: "application/json",
				Data:            payload,
			})
		})

	// §25.4 idempotency: durable when Postgres is available, in-memory in
	// single-process degraded mode. Built before the service body so the
	// leader-only idempotency-cleanup reconciler can drain it. Required-key
	// endpoints (upgrade start, restore execute, full backup) reject a
	// missing key and fail closed on a store outage at Tier 2/3.
	w.idemStore = buildIdempotencyStore(w.pgPool)
}
