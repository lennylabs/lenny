// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/blobstore"
	"github.com/lennylabs/lenny/pkg/clockinject"
	"github.com/lennylabs/lenny/pkg/events"
	"github.com/lennylabs/lenny/pkg/gateway/billing/usagestore"
	"github.com/lennylabs/lenny/pkg/gateway/checkpoint/checkpointer"
	"github.com/lennylabs/lenny/pkg/gateway/checkpoint/resumechunks"
	"github.com/lennylabs/lenny/pkg/gateway/core/subsystem"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credentialpoolstore"
	"github.com/lennylabs/lenny/pkg/gateway/environment/customrolestore"
	"github.com/lennylabs/lenny/pkg/gateway/environment/environmentstore"
	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantaccessstore"
	"github.com/lennylabs/lenny/pkg/gateway/experiment/experimentprovider"
	"github.com/lennylabs/lenny/pkg/gateway/externalapi/admin"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegationtree/resultrollup"
	"github.com/lennylabs/lenny/pkg/gateway/metrics/gatewaymetrics"
	"github.com/lennylabs/lenny/pkg/gateway/policy/interceptor"
	"github.com/lennylabs/lenny/pkg/gateway/policy/policy"
	"github.com/lennylabs/lenny/pkg/gateway/provisioning/gitref"
	"github.com/lennylabs/lenny/pkg/gateway/provisioning/vcscred"
	"github.com/lennylabs/lenny/pkg/gateway/quota/quotacheckpoint"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/slothealth"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/watchdog"
	"github.com/lennylabs/lenny/pkg/gateway/session/inputwait"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionbudget"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessioncallback"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionidle"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionlogstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	"github.com/lennylabs/lenny/pkg/gateway/storage/dualstore"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
	"github.com/lennylabs/lenny/pkg/storerouter"
)

// buildSessionServer is the §4.1 composition-root build step (R1) for the
// §4.2 session server, which realizes the §4.1 Stream Proxy and Upload
// Handler subsystems behind the sessionserver Go interfaces. It assembles
// the §15.1 session-creation service with its full Options set (§11.1
// admission gates, §11.2 budget accounting, §7.1/§7.3 retry and derive
// policy, §7.4 upload caps, §14 completion webhook, §8.6/§8.10 lease and
// tree-recovery wiring, and the §16.1 metric emitters) and returns the
// constructed server for the composition root to thread to the MCP fabric,
// the admin router, the HTTP surface, and the watchdog.
//
// spec: §4.1 gateway subsystem seams; §4.2 session manager; §15.1
// session-creation service.
func (w *gatewayWiring) buildSessionServer(
	gwMetrics *gatewaymetrics.Metrics,
	activityStamper *sessionidle.Stamper,
	sessionBudgetEnforcer *sessionbudget.Enforcer,
	dsMonitor *dualstore.Monitor,
	environments environmentstore.Store,
	tenantAccess tenantaccessstore.Store,
	opsEmitter events.EventEmitter,
	credentialPools credentialpoolstore.Store,
	vcsCreds vcscred.Resolver,
	customRoles customrolestore.Store,
	resolvedNoEnvPolicy string,
	auditSink admin.AuditSink,
	sessionStickyCache sessionserver.StickyCache,
	experimentProviders *experimentprovider.Cache,
	usage usagestore.Store,
	taskUsageBuilder *resultrollup.Builder,
	sessionLeaseRegistrar sessionserver.LeaseTreeRegistrar,
	leaseExtDefaults sessionserver.LeaseExtensionDefaults,
	quotaCheckpointSvc *quotacheckpoint.Service,
	policyChain *interceptor.Chain,
	policyAuditSink *policy.AuditSink,
	auditAppender policy.AuditAppender,
	inputWaits *inputwait.Registry,
	uploadSubsystem *subsystem.Subsystem,
	uploadMetrics *sessionserver.PromUploadMetrics,
	slotHealth *slothealth.Tracker,
	callbackValidator *sessioncallback.Validator,
	callbackSeal func(ctx context.Context, tenantID string, plaintext []byte) ([]byte, error),
	callbackDispatcher *sessioncallback.Dispatcher,
) *sessionserver.Server {
	f := w.f
	delegationMaxOrphanTasksPerTenant := f.delegationMaxOrphanTasksPerTenant
	delegationMaxLevelRecoverySeconds := f.delegationMaxLevelRecoverySeconds
	delegationMaxTreeRecoverySeconds := f.delegationMaxTreeRecoverySeconds
	maxCreatedStateTimeoutSeconds := f.maxCreatedStateTimeoutSeconds
	agentNamespace := f.agentNamespace
	rlPerRuntimePerMin := f.rlPerRuntimePerMin
	rlPerPoolPerMin := f.rlPerPoolPerMin
	maxConcSessGlobal := f.maxConcSessGlobal
	maxConcSessPerUser := f.maxConcSessPerUser
	maxConcSessPerRuntime := f.maxConcSessPerRuntime
	evalRLPerSessionPerMin := f.evalRLPerSessionPerMin
	evalRLPerTenantPerMin := f.evalRLPerTenantPerMin
	defaultIsolationProfile := f.defaultIsolationProfile
	devMode := f.devMode
	multiTenant := f.multiTenant
	tenancyMode := f.tenancyMode
	persistDeriveFailureRows := f.persistDeriveFailureRows
	sessionArtifactRetentionSeconds := f.sessionArtifactRetentionSeconds
	workspaceSealMaxDurationSeconds := f.workspaceSealMaxDurationSeconds
	uploadMaxConcurrentPerSession := f.uploadMaxConcurrentPerSession
	uploadMaxConcurrentGlobal := f.uploadMaxConcurrentGlobal
	uploadMaxBytesPerSession := f.uploadMaxBytesPerSession
	midSessionUploadEnabled := f.midSessionUploadEnabled
	retryMaxRetries := f.retryMaxRetries
	maxResumePendingSeconds := f.maxResumePendingSeconds
	envVarBlocklistCSV := f.envVarBlocklistCSV

	// spec: §10.1 line 155 — the resume path resolves the checkpoint's
	// chunk set and mints one presigned GET capability per chunk. It needs
	// a Presigner-capable and prefix-listing object store; the in-memory
	// dev backend implements neither, so the resolver stays nil there and
	// the resume carries no chunks (the §13.2 startup fail-closed already
	// requires a Presigner-capable backend for pod-based deployments).
	var resumeChunkResolver sessionserver.ResumeChunkResolver
	presigner, presignerOK := w.objectStore.(blobstore.Presigner)
	lister, listerOK := w.objectStore.(resumechunks.ChunkLister)
	if presignerOK && listerOK {
		resumeChunkResolver = &resumechunks.Resolver{
			Manifests:                        w.partialManifests,
			Presigner:                        presigner,
			Lister:                           lister,
			TTL:                              time.Duration(*f.checkpointCapabilityTTLSeconds) * time.Second,
			PartialRecoveryThresholdFraction: *f.partialRecoveryThresholdFraction,
		}
	}

	// §4.4 line 236 / §12.5 GC rule 4 — the resume-path cleaner releases each
	// confirmed chunk through the cataloging decorator (blobsCataloged) and
	// deletes the object (minioStore). Assign only the non-nil concrete stores
	// so a nil *cataloging.Store / *miniostore.Store is not wrapped in a
	// non-nil interface, which the cleaner's nil guard then skips (dev-mode).
	partialCleaner := &checkpointer.PartialCleaner{
		Store:              w.partialManifests,
		Metrics:            gwMetrics,
		TombstoneRetention: time.Duration(*f.gcTombstoneRetentionSeconds) * time.Second,
	}
	if w.blobsCataloged != nil {
		partialCleaner.Catalog = w.blobsCataloged
	}
	if w.minioStore != nil {
		partialCleaner.Objects = w.minioStore
	}

	return sessionserver.New(w.sessions, sessionserver.Options{
		// spec: §6.2 lines 273-300 — stamp agent_output / tool_use events
		// published on the session bus as idle-timer activity. F-11.3.7.
		ActivityStamper: activityStamper,
		// spec: §11.2 — drop a settled session's mid-session budget
		// accounting so the enforcer's per-session map does not grow
		// unbounded. The closure also clears the §8.6 recorder's accumulated
		// grant delta for the session (proposal 0023 S3/S4); the recorder is
		// constructed in a later build step, so w.proxyUsageRec is read at
		// call time rather than captured now.
		BudgetForget: func(sessionID string) {
			sessionBudgetEnforcer.Forget(sessionID)
			w.proxyUsageRec.forget(sessionID)
		},
		// spec: §8.10 line 1103 — operator-tunable per-tenant orphan cap.
		// The default (100) flows through the constructor when the flag
		// is unset; an override surfaces both on the sessionserver
		// detach-cascade fallback and on the §16.5
		// OrphanTasksPerTenantHigh alert (the scalar() denominator below
		// is re-emitted from the same flag). F-8.10.10.
		MaxOrphanTasksPerTenant: *delegationMaxOrphanTasksPerTenant,
		// spec: §8.10 lines 1022-1023 — the bottom-up tree-recovery
		// driver's per-level and whole-tree budgets, plus the §16.1
		// line 144-145 telemetry sink. F-8.10.1.
		TreeRecoveryLevelTimeout: time.Duration(*delegationMaxLevelRecoverySeconds) * time.Second,
		TreeRecoveryTreeTimeout:  time.Duration(*delegationMaxTreeRecoverySeconds) * time.Second,
		TreeRecoveryMetrics:      gwMetrics,
		UploadTokenIssuer:        w.uploadIssuer,
		UploadTokenVerifier:      w.uploadVerifier,
		// F-7.4.7: §7.1 line 58 TTL = maxCreatedStateTimeoutSeconds.
		UploadTokenTTL: time.Duration(*maxCreatedStateTimeoutSeconds) * time.Second,
		Blobs:          w.blobs,
		Executor:       w.exec,
		Transcripts:    w.transcripts,
		// spec: §8.8 lines 888-896 — the §8.10 archive materialization
		// lists a settled child's catalogued artifacts to populate
		// TaskResult.output.artifactRefs. Nil in the in-memory posture.
		// F-8.8.2.
		Artifacts: w.artifactCatalog,
		Events:    w.eventBus,
		// spec: §10.1 item 2 — gate session.create with 503 + Retry-After
		// while both coordination stores are unreachable. Nil monitor
		// (single-store / in-memory posture) leaves the gate open. F-10.1.3.
		DualStore:                  dsMonitor,
		Messaging:                  w.messagingCoord,
		Interactions:               w.interactions,
		ToolApprovalWaits:          w.toolApprovalWaits,
		Evals:                      w.evals,
		Experiments:                w.experiments,
		Pools:                      w.pools,
		Runtimes:                   w.runtimes,
		CapabilityOverrides:        w.capOverrides,
		Environments:               environments,
		TenantAccess:               tenantAccess,
		OpsEmitter:                 opsEmitter,
		RefResolver:                gitref.NewLsRemoteResolver(gitref.Options{}),
		CredentialPools:            credentialPools,
		VCSCredentials:             vcsCreds,
		CustomRoles:                customRoles,
		DefaultNoEnvironmentPolicy: resolvedNoEnvPolicy,
		ExperimentRejections: experimentRejectionReporter{
			audit:   auditSink,
			metrics: gwMetrics,
			emitter: opsEmitter,
		},
		StickyCache:        sessionStickyCache,
		ExternalProviders:  sessionserver.NewExternalProviderResolver(experimentProviders),
		Usage:              usage,
		Users:              w.users,
		Billing:            w.billing,
		Tenants:            w.tenants,
		StorageQuota:       w.storageCounter,
		PodBinder:          w.podBinder,
		PodRegistry:        w.podRegistry,
		CoordinationFencer: w.coordFencer,
		// spec: §4.6.1 (coordinating replica holds the lease), §10.1
		// (per-session coordination lease) — the at-bind Acquire in the bind
		// funnel claims the coordination lease on this replica before the
		// session row becomes an adoptable running-pod session, so the replica
		// that holds the pod binding is the lease holder from bind time. The
		// lease store is the same §12.4 store the Sweeper renews against and is
		// nil in the in-memory/dev posture with no Redis, which makes the
		// at-bind Acquire a no-op. ReplicaID must match the Sweeper's holder
		// identity so a self-bind renews rather than conflicts.
		CoordinationLeaseStore: w.coordLeaseStore,
		ReplicaID:              w.replica,
		AgentNamespace:         *agentNamespace,
		// spec: §11.1 line 7 — per-runtime and per-pool admission rate
		// limits, enforced at session creation where the runtime/pool are
		// known. Shares the same per-minute counter the §11.1 HTTP
		// middleware uses for global/per-user/per-tenant. F-11.1.2.
		AdmissionRateLimitCounter: w.rateLimiter,
		PerRuntimePerMinute:       *rlPerRuntimePerMin,
		PerPoolPerMinute:          *rlPerPoolPerMin,
		RateLimitMetrics:          gwMetrics,
		// spec: §11.1 line 8 — global, per-user, and per-runtime
		// concurrent-session admission caps (live non-terminal session
		// counts). Zero leaves a scope unlimited. F-11.1.3.
		MaxConcurrentSessionsGlobal:     *maxConcSessGlobal,
		MaxConcurrentSessionsPerUser:    *maxConcSessPerUser,
		MaxConcurrentSessionsPerRuntime: *maxConcSessPerRuntime,
		// §10.7 line 938 — the eval-submission rate limit shares the same
		// §11.1 per-minute counter (Redis-backed across replicas) keyed by
		// session_id and tenant_id. F-10.7.4 / F-11.2.19.
		EvalRateLimitCounter:    w.rateLimiter,
		EvalPerSessionPerMinute: *evalRLPerSessionPerMin,
		EvalPerTenantPerMinute:  *evalRLPerTenantPerMin,
		DefaultIsolationProfile: isolation.Profile(*defaultIsolationProfile),
		DevMode:                 *devMode,
		// spec: §10.2 lines 256–264. F-10.2.4. Multi-tenant deployments
		// fail closed on a no-role principal at the session RBAC gate.
		MultiTenant: *multiTenant,
		// spec: §4.9 — the platform tenancy.mode the session-start
		// credential-delivery gate enforces the two cross-tenant
		// credential-delivery rejections against. Wired from the same
		// --tenancy-mode flag the admin Router reads, so the gate keys off
		// the identical signal the layer-1 registration check and the
		// layer-2 admission webhook use.
		TenancyMode: *tenancyMode,
		Sealer:      w.sessionSealer,
		// §4.4 line 236 / §12.5 GC rule 4 — the resume path delegates
		// partial-manifest cleanup to this adapter. It releases each
		// confirmed chunk through the cataloging decorator (soft-delete
		// its artifact_store row, the exactly-once Redis decrement) before
		// the per-key object delete, so a resumed timeout checkpoint's
		// confirmed bytes return to the tenant rather than staying charged
		// forever. Catalog and Objects run only against the durable
		// Postgres+MinIO backends; the in-memory dev gateway leaves them
		// nil and the cleaner only soft-deletes the row.
		PartialManifestCleaner: partialCleaner,
		// spec: §10.1 partial-manifest path — classify a resume as
		// partial_workspace when an active partial manifest exists.
		PartialManifestLookup: w.partialManifests,
		// spec: §10.1 line 155 — resolve the checkpoint's chunk set from the
		// manifest row, verify contiguity, and mint one presigned GET
		// capability per chunk for the resume path; read the row for the
		// workspace-download stream; write the derived manifest on derive.
		ResumeChunkResolver: resumeChunkResolver,
		// spec: §16.1 line 195 — the resume path stamps
		// lenny_checkpoint_partial_total{recovered=true} on the same concrete
		// registry the upload driver's recovered=false abort arms write.
		CheckpointRecoveryMetrics: gwMetrics,
		CheckpointManifestReader:  w.partialManifests,
		CheckpointManifestWriter:  w.partialManifests,
		// §4.4 line 226 session-log close-hook. Wired with the
		// per-deployment Store (Noop for in-memory, MinIOStore when
		// the §4.5 follow-on wiring lands a MinIO uploader). The
		// close-hook fires from the gateway's session-completion path;
		// the SessionLogStore drops or persists best-effort.
		SessionLogHook:     &sessionlogstore.CloseHook{Store: w.sessionLogs},
		TreeArchive:        w.treeArchive,
		TaskUsage:          taskUsageBuilder,
		TreeBudgetReturner: w.treeBudgetReserver,
		// §8.6: register each root tree's lease-extension budget so a
		// later in-process budget-exhaustion extension (the gateway LLM
		// Proxy's ExtendForBudget trigger) resolves it. F-15.3.5.
		LeaseRegistrar:         sessionLeaseRegistrar,
		LeaseExtensionDefaults: leaseExtDefaults,
		QuotaCheckpointer:      quotaCheckpointSvc,
		HighWatermarkReader:    w.hwmReader,
		HighWatermarkObserver:  gwMetrics,
		Interceptors:           policyChain,
		PolicyAuditSink:        policyAuditSink,
		// §7.1 / §16.6 — session lifecycle audit events to the §11.7
		// hash-chained log, written under the session's tenant.
		LifecycleAuditSink: sessionLifecycleAuditor{appender: auditAppender},
		// §7.2 lines 124-127 / §11.7 / §16.7 — interaction-resolution
		// audit events (tool-use approve/deny, elicitation
		// respond/dismiss) to the §11.7 hash-chained log, written under
		// the session's tenant. F-7.2.8.
		InteractionAuditSink: interactionResolutionAuditor{appender: auditAppender},
		// §8.9 line 1003 / §11.7 / §16.1 — tree-walker defensive cycle
		// observer for REST /v1/sessions/{id}/tree. Emits the
		// delegation.tree_cycle_detected audit row + increments the
		// lenny_delegation_tree_cycle_detected_total counter when the
		// walker hits a repeated node. F-8.9.10.
		TreeCycleObserver: sessionserverTreeCycleObserver{emitter: treeCycleEmitter{metrics: gwMetrics}},
		// §7.2 line 317 — shared inputwait registry so REST inReplyTo
		// resolves the same pending `lenny/request_input` MCP registers.
		// F-7.2.14.
		InputWaits: inputWaits,
		// §7.1 line 92 — per-source-session advisory lock that serializes
		// concurrent /derive calls across replicas. Wired with Redis when
		// available (cross-replica serialization); a process-local
		// derivelock.Memory backs the minimal-gateway and single-replica
		// posture. F-7.1.12.
		DeriveLock: defaultDeriveLock(w.concernRedis.For(storerouter.RedisConcernCoordination)),
		// spec: §7.1 derive rule 5 / §11.2.1 — a platform-admin
		// allowIsolationDowngrade override records derive.isolation_downgrade.
		// §16.7 does not list this event type, so it is emitted to the
		// §11.2.1 per-tenant billing stream (an append-only,
		// audit-grade record) rather than the §11.7 audit chain. F-11.2.1.
		DeriveAuditSink: deriveDowngradeBillingAuditor{billing: w.billingEmitter},
		// §7.1 derive rule 2 — opt-in derive_failure audit-row persistence
		// (default off). When on, a copy-stage derive failure persists a
		// CAS-fenced terminal failed row reachable per §15.1. F-15.1.14.
		PersistDeriveFailureRows: *persistDeriveFailureRows,
		IncDeriveFailureAudit:    gwMetrics.IncDeriveFailureAudit,
		// §7.1 line 77 — default artifact retention window.
		DefaultRetention: time.Duration(*sessionArtifactRetentionSeconds) * time.Second,
		// §7.1 line 112 — seal-and-export retry window + outcome histogram.
		WorkspaceSealMaxDuration:     time.Duration(*workspaceSealMaxDurationSeconds) * time.Second,
		ObserveWorkspaceSealDuration: gwMetrics.ObserveWorkspaceSealDuration,
		// §10.7 lines 1120-1132, §16.1 lines 161-164 — variant-labelled
		// rollback-trigger metric family emitted at terminal session
		// transition and at each built-in eval submission.
		RecordSessionTerminal: gwMetrics.RecordSessionTerminal,
		ObserveEvalScore:      gwMetrics.ObserveEvalScore,
		// §10.7 lines 835-844 (SCL-023) — the per-tenant targeting
		// circuit-breaker open/closed gauge.
		SetExperimentTargetingCircuitOpen: gwMetrics.SetExperimentTargetingCircuitOpen,
		Clock:                             clockinject.Now,
		UploadSubsystem:                   uploadSubsystem,
		UploadMetrics:                     uploadMetrics,
		// spec: §11.1 lines 10-11 — concurrent-upload + per-session
		// upload-size admission caps. F-11.1.5, F-11.1.6.
		MaxConcurrentUploadsPerSession: *uploadMaxConcurrentPerSession,
		MaxConcurrentUploadsGlobal:     *uploadMaxConcurrentGlobal,
		MaxUploadBytesPerSession:       *uploadMaxBytesPerSession,
		// spec: §7.4 line 433 — mid-session upload deployer policy. F-7.4.6.
		MidSessionUploadEnabled: *midSessionUploadEnabled,
		// §4.9 line 1220 — the pre-claim availability check race metric.
		PreclaimMismatch: gwMetrics.IncCredentialPreclaimMismatch,
		// §5.2 — the shared fail/leak tracker so the slot-bind-failure path and
		// the §4.7 scrub-report drain ledger accumulate in one rolling window.
		SlotHealth: slotHealth,
		// §5.2 — whole-pod replacement counter, incremented when the
		// concurrent-workspace slot retry policy drains an unhealthy pod
		// (ceil(maxConcurrent/2) slots failed or leaked in the window).
		SlotReplacement: gwMetrics.IncSlotPodReplacement,
		// §6.2 line 179 — per-pod leaked-slot gauge, set when a
		// concurrent-workspace slot's cleanup does not reclaim it.
		SlotLeakGauge: func(pod, pool string, leaked int) {
			gwMetrics.SetAdapterLeakedSlots(pod, pool, float64(leaked))
		},
		// §4.6.1 Pool exhaustion behavior, §16.1 — the per-pool claim FIFO
		// metrics for onPoolExhausted: queue pools. The depth gauge and the
		// timeout counter back the §16.5 PodClaimQueueSaturated alert.
		SetPodClaimQueueDepth:    gwMetrics.SetPodClaimQueueDepth,
		ObservePodClaimQueueWait: gwMetrics.ObservePodClaimQueueWait,
		IncPodClaimTimeout:       gwMetrics.IncPodClaimTimeout,
		// §6.3 lines 348, 372 — startup-latency histograms observed on
		// each successful pod-warm start.
		ObserveStartupDuration: gwMetrics.ObserveSessionStartupDuration,
		ObserveStartupPhase:    gwMetrics.ObserveSessionStartupPhase,
		// §6.3 line 356, §16.1 line 15 — TTFT histogram observed on
		// the first agent-streamed response event per session.
		ObserveTimeToFirstToken: gwMetrics.ObserveSessionTimeToFirstToken,
		// §7.3 lines 377-393 — clamp the client-supplied retry policy
		// against the deployer caps so the per-session value can never
		// exceed the watchdog's platform-wide bounds. F-7.3.1 /
		// F-7.3.24.
		RetryPolicyCaps: session.RetryPolicyCaps{
			MaxRetries:             *retryMaxRetries,
			MaxSessionAgeSeconds:   watchdog.DefaultMaxSessionAgeSeconds,
			MaxResumeWindowSeconds: *maxResumePendingSeconds,
		},
		// §14 line 105 — deployer extension to the platform env-var
		// blocklist; the platform default is always merged in first.
		// F-14.1.12.
		EnvVarBlocklist: splitCSV(*envVarBlocklistCSV),
		// §7.3 / §16.1 — retry + resume metric emitters. The
		// watchdog/coordinator path bumps retryCount on each pod
		// recovery (the v1 retry path); the explicit /resume endpoint
		// counts every attempt with its outcome. F-7.3.10.
		IncSessionResumeAttempt: gwMetrics.IncSessionResumeAttempt,
		IncSessionRetry:         gwMetrics.IncSessionRetry,
		// spec: §16.1 / §16.1.1 — the watchdog's expiry sweeps fire
		// Server.OnSessionExpired, which bumps the
		// lenny_session_expiry_total{pool, reason} counter with the §16.1.1
		// reason the watchdog resolved (max_idle_time | max_session_age).
		// F-11.3.7.
		IncSessionExpiry: gwMetrics.IncSessionExpiry,
		// spec: §16.1 line 124, §7.3 line 387 — F-7.5.9. Increment the
		// lenny_warmpool_warmup_failure_total{error_type=setup_command_failed}
		// counter when a §7.5 setup command fails on the warm-pool side
		// of a bind.
		IncWarmpoolWarmupFailure: gwMetrics.IncWarmpoolWarmupFailure,
		// spec: §5.1 (injection fail-closed), §15.1 (SERVICE_UNAVAILABLE)
		// — F-5.1.20. Increment the
		// lenny_injection_gate_failclosed_total{cause} counter when the
		// §5.1 injection gate fails closed on a transient backing-store
		// read, labeled runtime_store or override_store.
		IncInjectionGateFailClosed: gwMetrics.IncInjectionGateFailClosed,
		// §14 lines 108-150 — the session-completion webhook subsystem.
		// F-14.1.11 / F-15.1.11.
		CallbackValidator:  callbackValidator,
		CallbackSeal:       callbackSeal,
		CallbackDispatcher: callbackDispatcher,
	})
}
