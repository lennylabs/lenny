// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/lennylabs/lenny/pkg/audit"
	"github.com/lennylabs/lenny/pkg/audit/ocsf"
	"github.com/lennylabs/lenny/pkg/clockinject"
	"github.com/lennylabs/lenny/pkg/controller/tenantdeletion"
	"github.com/lennylabs/lenny/pkg/events"
	"github.com/lennylabs/lenny/pkg/gateway/audit/auditretention"
	"github.com/lennylabs/lenny/pkg/gateway/audit/auditscope"
	"github.com/lennylabs/lenny/pkg/gateway/audit/auditstore"
	"github.com/lennylabs/lenny/pkg/gateway/audit/jwtaudit"
	"github.com/lennylabs/lenny/pkg/gateway/billing/correctionstore"
	correctionpg "github.com/lennylabs/lenny/pkg/gateway/billing/correctionstore/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/connectors/connectorauthz"
	"github.com/lennylabs/lenny/pkg/gateway/connectors/connectorcredstore"
	"github.com/lennylabs/lenny/pkg/gateway/connectors/connectorinvoke"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credentialpoolstore"
	credrenewalprop "github.com/lennylabs/lenny/pkg/gateway/credentials/credrenewal/propagator"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/impersonation"
	revocationprop "github.com/lennylabs/lenny/pkg/gateway/credentials/revocation/propagator"
	"github.com/lennylabs/lenny/pkg/gateway/environment/customrolestore"
	"github.com/lennylabs/lenny/pkg/gateway/environment/deploymentconfigstore"
	"github.com/lennylabs/lenny/pkg/gateway/environment/environmentstore"
	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantaccessstore"
	"github.com/lennylabs/lenny/pkg/gateway/eventbuffer"
	"github.com/lennylabs/lenny/pkg/gateway/experiment/experimentsticky"
	"github.com/lennylabs/lenny/pkg/gateway/externalapi/admin"
	"github.com/lennylabs/lenny/pkg/gateway/externalapi/admintoken"
	"github.com/lennylabs/lenny/pkg/gateway/externalapi/admintoken/k8ssecret"
	"github.com/lennylabs/lenny/pkg/gateway/llmproxy/semanticcache"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegation"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegationpolicystore"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegationtree/leasecontrol"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/elicitationfloor"
	"github.com/lennylabs/lenny/pkg/gateway/metrics/gatewaymetrics"
	"github.com/lennylabs/lenny/pkg/gateway/operability/recommendations"
	"github.com/lennylabs/lenny/pkg/gateway/pki/carotation"
	"github.com/lennylabs/lenny/pkg/gateway/pki/carotationstore"
	carotationpg "github.com/lennylabs/lenny/pkg/gateway/pki/carotationstore/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
	podterminateprop "github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podterminate/propagator"
	"github.com/lennylabs/lenny/pkg/gateway/policy/interceptor"
	"github.com/lennylabs/lenny/pkg/gateway/policy/interceptor/interceptorstore"
	"github.com/lennylabs/lenny/pkg/gateway/policy/policy"
	"github.com/lennylabs/lenny/pkg/gateway/policy/ratelimit"
	"github.com/lennylabs/lenny/pkg/gateway/quota/quotacheckpoint"
	"github.com/lennylabs/lenny/pkg/gateway/quota/quotafailopen"
	"github.com/lennylabs/lenny/pkg/gateway/quota/quotastore"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/externaladapterstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/memorystore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	"github.com/lennylabs/lenny/pkg/gateway/storage/deadletterredaction"
	"github.com/lennylabs/lenny/pkg/gateway/storage/erasure"
	"github.com/lennylabs/lenny/pkg/gateway/storage/erasurejob"
	"github.com/lennylabs/lenny/pkg/gateway/storage/erasurejob/saltlockpg"
	"github.com/lennylabs/lenny/pkg/gateway/storage/issuedtokenstore"
	"github.com/lennylabs/lenny/pkg/gateway/upgrade/runtimeupgrade"
	"github.com/lennylabs/lenny/pkg/gateway/upgrade/runtimeupgradestore"
	runtimeupgradepg "github.com/lennylabs/lenny/pkg/gateway/upgrade/runtimeupgradestore/pgstore"
	"github.com/lennylabs/lenny/pkg/kms/rekey"
	"github.com/lennylabs/lenny/pkg/legalholdescrow"
	legalholdescrowpg "github.com/lennylabs/lenny/pkg/legalholdescrow/pgstore"
	"github.com/lennylabs/lenny/pkg/preflight"
	preflightinfra "github.com/lennylabs/lenny/pkg/preflight/infra"
	"github.com/lennylabs/lenny/pkg/schemamigrate"
	"github.com/lennylabs/lenny/pkg/tenantkms"
)

// sequenceProvisioningAdminOptions threads the CREATE-privileged DDL pools into
// the admin Router options so the tenant-provisioning helper (S4) can create the
// per-tenant billing_seq_/audit_seq_ sequences on the instance where
// billing_events and audit_log live, plus the primary the §13.3 issued-token
// write-before-issue path seals its audit row on. It takes the caller-built base
// options (Clock, Audit, Metrics, DevMode) and fills the sequence-provisioning
// fields from the wiring struct, so the field wiring is a single
// tier-1-testable statement rather than an assignment buried in the
// startup-only NewRouter chain. Both DDL pools are nil in the in-memory /
// SQLite topology (no DDL DSN), where no Postgres sequence is used; the
// provisioning helper treats a nil billing/audit DDL pool as "no provisioning".
//
// spec: §12.3, §15.1. F-11.2.10.
func (w *gatewayWiring) sequenceProvisioningAdminOptions(base admin.Options) admin.Options {
	base.BillingAuditDDLPool = w.billingAuditDDLPool
	base.PrimaryDDLPool = w.primaryDDLPool
	return base
}

func (w *gatewayWiring) buildAdminRouter(
	gwMetrics *gatewaymetrics.Metrics,
	delegationSvc *delegation.Service,
	environments environmentstore.Store,
	connectorCreds connectorcredstore.Store,
	connectorOAuth *admin.ConnectorOAuth,
	credentialRekeyJob *rekey.Job,
	policyChain *interceptor.Chain,
	auditSink admin.AuditSink,
	auditAppender policy.AuditAppender,
	wireAudit func(*admin.Router) *admin.Router,
	adminStickyFlusher admin.StickyFlusher,
	erasureSticky *experimentsticky.RedisCache,
	deploymentConfig deploymentconfigstore.Store,
	credentialPools credentialpoolstore.Store,
	customRoles customrolestore.Store,
	delegationPolicies delegationpolicystore.Store,
	interceptors interceptorstore.Store,
	leaseBudgets *leasecontrol.MemoryBudgetSource,
	opsEmitter events.EventEmitter,
	opsEventBuffer *eventbuffer.EventBuffer,
	sessionSrv *sessionserver.Server,
	tenantAccess tenantaccessstore.Store,
	auditOpsStore *auditstore.Store,
	auditPruner *auditretention.Pruner,
	auditValidator *auditscope.Validator,
	credRenewalProp *credrenewalprop.Propagator,
	elicitationFloorProvider *elicitationfloor.Provider,
	quotaCheckpointSvc *quotacheckpoint.Service,
	quotaCounter *quotastore.Counter,
	quotaFailOpenAccum *quotafailopen.Accumulator,
	revProp *revocationprop.Propagator,
) (
	connectorAuthorizer *connectorauthz.Authorizer,
	connectorInvoker *connectorinvoke.Invoker,
	ruStore runtimeupgradestore.Store,
	erasureSemanticCache *semanticcache.InMemory,
) {
	f := w.f
	// Re-bind the flags the admin-router wiring reads to their original
	// local names so the moved construct-and-wire block reads unchanged.
	devMode := f.devMode
	tenancyMode := f.tenancyMode
	interceptorWeakeningCooldownSeconds := f.interceptorWeakeningCooldownSeconds
	addr := f.addr
	auditSIEMEndpoint := f.auditSIEMEndpoint
	maxFinalizingTimeoutSeconds := f.maxFinalizingTimeoutSeconds
	multiTenant := f.multiTenant
	bearerExpectedIssuer := f.bearerExpectedIssuer
	runtimeBin := f.runtimeBin
	postgresDSN := f.postgresDSN
	redisURL := f.redisURL
	recommendationsDisabledRules := f.recommendationsDisabledRules
	recommendationsWindowOverrides := f.recommendationsWindowOverrides
	recommendationsDisableOnOutage := f.recommendationsDisableOnOutage
	externalAdapterHarnessPath := f.externalAdapterHarnessPath
	auditPgauditEnabled := f.auditPgauditEnabled
	auditPgauditSinkEndpoint := f.auditPgauditSinkEndpoint
	llmSemanticCache := f.llmSemanticCache
	minioAccessKey := f.minioAccessKey
	minioEndpoint := f.minioEndpoint
	minioSecretKey := f.minioSecretKey
	mtlsCurrentCAID := f.mtlsCurrentCAID
	adminTokenDisabled := f.adminTokenDisabled
	adminTokenNamespace := f.adminTokenNamespace
	adminTokenSecretName := f.adminTokenSecretName
	adminTokenTenant := f.adminTokenTenant
	billingApproverWebhookURL := f.billingApproverWebhookURL
	billingDualControlThreshold := f.billingDualControlThreshold
	legalHoldEscrowBucket := f.legalHoldEscrowBucket
	minioBucket := f.minioBucket
	minioUseSSL := f.minioUseSSL
	opsServiceURL := f.opsServiceURL
	billingApproverWebhookSecret := f.billingApproverWebhookSecret
	legalHoldEscrowEndpoint := f.legalHoldEscrowEndpoint
	// ----- Admin API -----
	// delegationPolicies was constructed above so the delegation
	// admission gate (§8.2 LayerPolicy) and the admin CRUD share one
	// store handle.
	// spec: §12.5 line 301 / line 307 — the §12.5 T4 KMS availability
	// probe Lifecycle, backed by the resolved §4 kms.Provider so the
	// zero-byte encrypt/decrypt round-trip uses the same provider
	// credentials a T4 artifact write would. One Lifecycle instance is
	// shared by the admin-time probe (WithKMSProbe below, F-12.5.3) and
	// the leader-elected continuous probe (the Prober wired with the GC
	// loops, F-12.5.4) so the `t4KmsLastProbeSuccessAt` admin field and
	// the `lenny_t4_kms_probe_last_success_timestamp` gauge read the
	// same recorded last-success time.
	kmsProbeLifecycle := tenantkms.NewProviderProbeLifecycle(w.kmsProvider, clockinject.Now)

	// §9.3 outbound MCP transport shared by the connector live test and
	// the §9.3 line 136 capability refresh. The §9.3 line 164
	// connector-access authorizer resolves the calling session's effective
	// delegation policy (runtime-level + §10.6 environment default) so a
	// session cannot invoke a connector its policy does not permit. The
	// client carries a bounded egress timeout because it dials untrusted
	// external endpoints.
	connectorMCPClient := connectorinvoke.New(&http.Client{Timeout: 15 * time.Second})
	connectorAuthorizer = connectorauthz.New(delegationSvc, w.sessions, environments)
	connectorInvoker = connectorinvoke.NewInvoker(w.connectors, connectorCreds, connectorMCPClient, nil, connectorAuthorizer).
		WithClock(clockinject.Now).
		// spec: §10.6 line 607 — enforce the calling session's environment
		// connectorSelector capability filter on each connector tools/call.
		// F-10.6.2.
		WithEnvironments(environments).
		// spec: §4.8 lines 1057-1058, 1077 — run the PreConnectorRequest and
		// PostConnectorResponse interceptor phases on the gateway-proxied
		// connector path. All connector traffic flows through the gateway, so
		// the phases always apply. F-4.8.14.
		WithInterceptors(policyChain)

	// §25.3 capacity recommendations: the per-replica sliding-window
	// metric store backing the rules engine, plus its §25.3 metrics
	// (lenny_recommendations_generated_total and the
	// lenny_recommendations_ring_buffer_bytes gauge registered over the
	// store's current memory use). spec: §25.3 lines 588-598, 618.
	recommendationStore := recommendations.NewWindowStore(7 * 24 * time.Hour)
	recommendationsMetrics, err := recommendations.NewMetrics(gwMetrics.Registerer())
	if err != nil {
		log.Fatalf("lenny-gateway: §25.3 recommendation metrics: %v", err)
	}
	if err := recommendations.RegisterRingBufferBytes(gwMetrics.Registerer(), recommendationStore); err != nil {
		log.Fatalf("lenny-gateway: §25.3 recommendation ring-buffer gauge: %v", err)
	}

	// §10.3 lines 344-350 — operator-driven cluster-internal CA rotation.
	// The stage machine is durable (carotationstore: Postgres when a pool
	// is wired, else in-memory) so an interrupted rotation resumes after a
	// restart; each committed transition emits the platform.ca_rotated
	// audit row through jwtaudit.CAObserver. Wired only when a current CA
	// id is configured (the chart sets it when mtls.enabled), leaving the
	// /v1/admin/ca-rotation routes absent on a mesh-mTLS deployment.
	// F-10.3.21.
	var caRotationMgr admin.CARotationManager
	if *mtlsCurrentCAID != "" {
		var caRotStore carotationstore.Store = carotationstore.NewMemory()
		if w.pgPool != nil {
			caRotStore = carotationpg.New(w.pgPool)
		}
		caRotOpts := []carotation.Option{}
		if auditAppender != nil {
			caRotOpts = append(caRotOpts, carotation.WithObserver(jwtaudit.NewCAObserver(auditAppender)))
		}
		mgr := carotation.NewManager(caRotStore, caRotOpts...)
		if err := mgr.EnsureInitialized(context.Background(), *mtlsCurrentCAID); err != nil {
			log.Printf("lenny-gateway: §10.3 CA-rotation init: %v", err)
		}
		caRotationMgr = mgr
	}

	// §10.5 lines 466-540 — operator-driven RuntimeUpgrade state machine.
	// The phase is durable (runtimeupgradestore: Postgres when a pool is
	// wired, else in-memory) so a runtime image rollout survives a gateway
	// restart and resumes from the recorded phase; the previous pool spec
	// is captured from the live pool catalog for rollback (line 507). Each
	// committed transition emits the lenny_runtime_upgrade_state gauge
	// family through gwMetrics, and EmitAll primes those gauges at startup
	// so the §16.5 RuntimeUpgradeStuck alert evaluates the durable phase
	// after a restart. F-10.5.1.
	var runtimeUpgradeMgr admin.RuntimeUpgradeManager
	// ruStore is hoisted out of the construction block so the §10.5
	// GET /internal/runtime-upgrade/active endpoint (line 508 deletion
	// guard, line 502 Phase 3 gate) can read the same durable record the
	// manager drives.
	ruStore = runtimeupgradestore.NewMemory()
	{
		if w.pgPool != nil {
			ruStore = runtimeupgradepg.New(w.pgPool)
		}
		mgr := runtimeupgrade.NewManager(
			ruStore,
			runtimeupgrade.WithPoolReader(poolSpecReader{store: w.pools}),
			runtimeupgrade.WithMetrics(gwMetrics),
		)
		if err := mgr.EmitAll(context.Background()); err != nil {
			log.Printf("lenny-gateway: §10.5 runtime-upgrade metric prime: %v", err)
		}
		runtimeUpgradeMgr = mgr
	}

	// spec: §12.3, §15.1 — sequenceProvisioningAdminOptions threads the
	// CREATE-privileged DDL pools and the §12.3 R-03 billing/audit-shard
	// resolvers into the admin Router options so the tenant-provisioning helper
	// creates the per-tenant billing_seq_/audit_seq_ sequences on the right
	// instance. F-11.2.10.
	adminRouter := admin.NewRouter(w.tenants, w.sequenceProvisioningAdminOptions(admin.Options{
		Clock:   clockinject.Now,
		Audit:   auditSink,
		Metrics: gwMetrics,
		DevMode: *devMode,
		// spec: §4.9 — the platform tenancy.mode the warm-pool
		// pool-registration layer-1 check gates the cross-tenant
		// credential-delivery rejections on.
		TenancyMode: *tenancyMode,
	})).
		WithKMSProbe(kmsProbeLifecycle).
		WithRuntimes(w.runtimes).
		WithRuntimeCapabilityOverrides(w.capOverrides).
		WithUsers(w.users).
		WithPools(w.pools).
		// §15.1 line 797: the pool-drain endpoint updates the
		// lenny_pool_draining_sessions_total gauge through gwMetrics.
		WithPoolDrainMetrics(gwMetrics).
		WithBreakers(w.breakers).
		WithConnectors(w.connectors).
		// §15.1 / §24.8 external-protocol adapter registry. The registry is
		// platform-global config (no tenant_id, like runtimes); the
		// in-memory store is the v1 backing, with Postgres as the documented
		// seam. The validate gate drives the lenny-compliance harness.
		WithExternalAdapters(externaladapterstore.NewMemory(), admin.ComplianceValidator{HarnessPath: *externalAdapterHarnessPath}).
		WithConnectorOAuth(connectorOAuth).
		// §15.1 connector live-connectivity test. The outbound MCP
		// client dials untrusted external endpoints, so it carries a
		// bounded egress timeout; the per-connector limiter enforces the
		// §15.1 line 1180 10/min cap.
		WithConnectorTest(
			connectorinvoke.NewTester(connectorMCPClient),
			connectorCreds,
			ratelimit.NewMemory(),
		).
		// §9.3 line 136 connector capability inference on the sanctioned
		// outbound path. Carries the same per-connector 10/min cap as the
		// live test since it also dials the external endpoint.
		WithConnectorRefresh(connectorInvoker, ratelimit.NewMemory()).
		WithDelegationPolicies(delegationPolicies).
		// §4.8 / §15.1 external-interceptor registry CRUD; the cooldown
		// seconds it records on a weakening transition match the delegation
		// service's window. F-4.8.17.
		WithInterceptors(interceptors, *interceptorWeakeningCooldownSeconds).
		WithCredentialPools(credentialPools).
		WithCustomRoles(customRoles).
		WithTenantAccess(tenantAccess).
		WithSessions(w.sessions).
		WithSessionAdmin(sessionAdminAdapter{store: w.sessions, onTerminal: sessionSrv.OnSessionTerminal}).
		WithInteractions(w.interactions).
		WithExperiments(w.experiments).
		WithStickyFlusher(adminStickyFlusher).
		WithEnvironments(environments).
		WithEvalResults(w.evals).
		WithEvalAggregateView(w.evalMatviewEnabled).
		WithRecommendations(recommendations.NewCapacityServiceWithConfig(
			recommendationStore,
			recommendations.Config{
				DisabledRules:             splitCSV(*recommendationsDisabledRules),
				WindowOverrides:           parseWindowOverrides(*recommendationsWindowOverrides),
				DisableOnPrometheusOutage: *recommendationsDisableOnOutage,
			},
		).WithMetrics(recommendationsMetrics))
	adminRouter = adminRouter.
		WithEventBuffer(opsEventBuffer).
		WithEventEmitter(opsEmitter)
	if caRotationMgr != nil {
		adminRouter = adminRouter.WithCARotation(caRotationMgr)
	}
	if runtimeUpgradeMgr != nil {
		adminRouter = adminRouter.WithRuntimeUpgrade(runtimeUpgradeMgr)
	}
	if w.artifactCatalog != nil {
		// §12.8 line 735 / 794(b): the durable artifact_store catalog backs
		// artifact-scoped legal holds (POST /v1/admin/legal-hold with
		// artifactId) and the artifact half of the GDPR-erasure legal-hold
		// preflight.
		adminRouter = adminRouter.WithArtifactLegalHold(w.artifactCatalog)
	}
	if leaseBudgets != nil {
		// §15.1 line 868: expose DELETE …/extension-denial backed by the
		// same leasecontrol budget source the GatewayControl handler reads.
		adminRouter = adminRouter.WithLeaseDenials(leaseBudgets)
		// §10.2 line 261: the same budget source resolves a tree's owning
		// tenant (TenantOf), so a non-platform-admin caller is confined to
		// its own tenant before the durable clear runs (ADM-4).
		adminRouter = adminRouter.WithTenantResolver(leaseBudgets)
	}
	// §5.2 line 629: surface live poolCondition / idlePodCount on the
	// admin pool GET when the gateway has a Kubernetes client (an
	// agent-namespace deployment). The minimal Postgres-only posture
	// leaves the reader unwired and the fields are omitted.
	if w.podBinder != nil && w.podBinder.Client != nil {
		lookup := podsession.PoolStatusLookup{
			Reader:    w.podBinder.Client,
			Namespace: w.podBinder.Namespace,
		}
		adminRouter = adminRouter.WithPoolStatusReader(lookup)
		// spec: §17.8.2 step 3 — surface the cold-start bootstrapStatus
		// hoursOfData / estimatedConvergenceAt from the SandboxWarmPool
		// CRD status the PoolScalingController writes.
		adminRouter = adminRouter.WithPoolBootstrapStatusReader(lookup)
		// spec: §4.6.2 lines 558-560 — populate crdGeneration /
		// lastReconciledAt / lagSeconds / inSync on the sync-status
		// endpoint from the SandboxTemplate annotations the
		// PoolScalingController stamps. Only available with a cluster
		// client; the Postgres-only posture leaves the reader unwired and
		// the sync-status handler reports the Postgres-only generation.
		adminRouter = adminRouter.WithCRDGenerationReader(lookup)
	}
	// spec: §17.2 line 86 — wire the dynamic floor provider so the admin
	// effective-mode resolution and below-floor guard observe a ConfigMap
	// floor change without re-wiring. Always wired (even when the startup
	// flag is empty) because the reconcile may raise the floor at runtime.
	adminRouter = adminRouter.WithElicitationFloorProvider(elicitationFloorProvider.Floor)
	// §16.7 deployment-transition audit emitter: the post-upgrade hook
	// reconciles the rendered Helm deployment config against the persisted
	// baseline and emits the gateway.*/platform.*/deployment.* transition
	// events under the operator's identity. F-8.2.5, F-9.2.10, F-17.2.8.
	adminRouter = adminRouter.WithDeploymentConfig(deploymentConfig)
	// §13.3 / §16.7 platform-admin impersonation: the distinct cross-tenant
	// admin code path (not routed through /v1/oauth/token) that mints a
	// target-user bearer and emits admin.impersonation_started/_ended. The
	// started audit row is written under the platform tenant carrying
	// target_tenant_id, so the auditstore CMP-058 residency gate routes it
	// to the target's regional platform-Postgres before the bearer is
	// minted. F-16.7.1.
	impersonationSvc := impersonation.New(
		impersonation.NewMemStore(), auditValidator, w.jwtSigner,
		impersonation.Config{
			PlatformTenantID: "platform",
			MaxDuration:      time.Hour,
			Issuer:           *bearerExpectedIssuer,
			Audience:         w.expectedAuds,
			Clock:            clockinject.Now,
			NewID:            func() string { var b [16]byte; _, _ = rand.Read(b[:]); return fmt.Sprintf("imp-%x", b[:]) },
		},
	)
	adminRouter = adminRouter.WithImpersonation(impersonationSvc)
	// §6.2 line 260 / §11.3 line 219: pin the admin runtime validator
	// to the same outer bound the finalizing-state watchdog enforces, so
	// the §15.1 POST/PUT /v1/admin/runtimes and POST /v1/admin/bootstrap
	// handlers reject a runtime whose setupPolicy.timeoutSeconds exceeds
	// gateway.maxFinalizingTimeoutSeconds.
	adminRouter = adminRouter.WithMaxFinalizingTimeoutSeconds(*maxFinalizingTimeoutSeconds)
	// spec: §11.7 lines 445-451 — record whether audit.siem.endpoint is
	// configured so the admin compliance gate can reject regulated-profile
	// tenant create/update and environment creation with
	// COMPLIANCE_SIEM_REQUIRED when SIEM is absent. F-11.7.2.
	adminRouter = adminRouter.WithSIEMConfigured(*auditSIEMEndpoint != "")
	// spec: §11.7 lines 374-379 — record whether pgaudit is fully
	// configured (audit.pgaudit.enabled true and a sinkEndpoint set) so
	// the admin compliance gate rejects regulated-profile tenant
	// create/update and environment creation with COMPLIANCE_PGAUDIT_REQUIRED
	// when it is not. F-11.7.10.
	adminRouter = adminRouter.WithPgauditConfigured(*auditPgauditEnabled && *auditPgauditSinkEndpoint != "")
	// §15.1 lines 891-892 / §24.13 lines 150-151: wire the
	// schema-migration management endpoints when a Postgres DSN is set
	// (the migrations the runner applies live in the same database).
	if *postgresDSN != "" {
		if mig, err := schemamigrate.New(*postgresDSN); err != nil {
			log.Printf("lenny-gateway: schema-migration manager disabled: %v", err)
		} else {
			adminRouter = adminRouter.WithMigrationManager(mig)
		}
	}
	// §15.1 line 890 / §24.2: API-backed `lenny-ctl preflight`. The
	// endpoint probes the gateway's own configured backends, so it is
	// registered only when at least one backend DSN is set. The probes
	// re-dial (the endpoint is POST because the dials are
	// side-effecting). F-24.2.2.
	preflightConfig := preflightinfra.Config{
		PostgresDSN:    *postgresDSN,
		RedisDSN:       *redisURL,
		MinIOEndpoint:  *minioEndpoint,
		MinIOAccessKey: *minioAccessKey,
		MinIOSecretKey: *minioSecretKey,
		MinIOBucket:    *minioBucket,
		MinIOUseSSL:    *minioUseSSL,
	}
	if preflightConfig.Configured() {
		probers := preflightinfra.RealProbers()
		adminRouter = adminRouter.WithPreflight(admin.InfraPreflightFunc(func(ctx context.Context) []preflight.CheckResult {
			return preflightinfra.Run(ctx, preflightConfig, probers)
		}))
	}
	adminRouter = wireAudit(adminRouter)
	// §25.9 audit-query observability series (query latency, broken /
	// post-outage-rechain counters, scatter-gather fan-out). F-25.9.13.
	adminRouter = adminRouter.WithAuditMetrics(gwMetrics)
	if auditPruner != nil {
		// §16.4 line 378 force-drop override surface. F-11.7.17.
		adminRouter = adminRouter.WithAuditPruner(auditPruner)
	}
	// §12.5 line 317: the erasure-completion → tenant-scoped GC sweep
	// trigger. The erasure runner is built here but the retention-GC
	// collector is constructed later, so the runner closes over this
	// indirection and the GC block below assigns the real sweep once the
	// collector exists. Nil during the startup window before the GC block
	// runs (no erasure job can complete that early).
	var immediateGCSweep func(ctx context.Context, tenantID string)
	// spec: §12.8 step 2 line 794 — the §4.9 semantic cache holds
	// per-user cached LLM query/response pairs, so a user erasure must
	// purge it. The cache is opt-in (--llm-semantic-cache); when enabled
	// the in-memory store is built here and the same instance is reused on
	// the §4.9 proxy hot path below, so the erasure orchestrator purges
	// the exact cache the proxy populates. Nil when the cache is off
	// (nothing to erase). F-12.2.16.
	if *llmSemanticCache {
		erasureSemanticCache = semanticcache.NewInMemory(nil, 0, 0, clockinject.Now)
	}
	// §12.8 GDPR erasure: build the DeleteByUser orchestrator over the
	// wired stores and expose it behind the admin erasure endpoints.
	// Session-scoped stores (transcripts, artifacts) are erased per
	// session before the session-keyed user-scoped stores.
	{
		sessionScoped := []erasure.SessionEraser{}
		if te, ok := w.transcripts.(sessionArtifactDeleter); ok {
			sessionScoped = append(sessionScoped,
				erasure.SessionEraser{Name: "transcripts", DeleteBySession: te.DeleteBySession})
		}
		if be, ok := w.blobs.(sessionArtifactDeleter); ok {
			sessionScoped = append(sessionScoped,
				erasure.SessionEraser{Name: "artifacts", DeleteBySession: be.DeleteBySession})
		}
		sessionScoped = append(sessionScoped,
			erasure.SessionEraser{Name: "eval_results", DeleteBySession: w.evals.DeleteBySession})
		// §12.8 step 11: the session_tree_archive holds settled child
		// TaskResults keyed by root_session_id with a Postgres FK into
		// sessions(id) (migration 0100, no ON DELETE action), so it is
		// erased per owned session before SessionStore. session_dlq_archive
		// (step 10) is omitted deliberately: its FK is ON DELETE CASCADE
		// (migration 0056), so the step-17 SessionStore delete cleans it up.
		sessionScoped = append(sessionScoped,
			erasure.SessionEraser{Name: "session_tree_archive", DeleteBySession: w.treeArchive.DeleteBySession})
		// §12.8 step 16: purge the tree-wide Redis delegation-budget keys
		// ({root_session_id}:dlg:*) for each of the user's root sessions
		// before SessionStore deletion makes the root ids irrecoverable.
		// The keys carry no tenant prefix (the {root} hash tag scopes
		// them); PurgeRoot no-ops on a non-root session id.
		if w.treeBudgetConcrete != nil {
			tb := w.treeBudgetConcrete
			sessionScoped = append(sessionScoped, erasure.SessionEraser{
				Name: "delegation_budget",
				DeleteBySession: func(ctx context.Context, _ string, sessionID string) (int, error) {
					return tb.PurgeRoot(ctx, sessionID)
				},
			})
		}

		// §12.8 user-scoped stores, appended in dependency-rank order so
		// the orchestrator runs them in the sequence ValidateOrder pins.
		// Each store is wired only when its backend is present, so a
		// no-Redis / no-Postgres posture erases the subset it can satisfy
		// rather than dereferencing a nil store.
		userScoped := []erasure.Eraser{}
		if w.erasureLeaseStore != nil {
			// step 1: release the user's active session-coordination leases.
			// Typed via erasure.FromCounting so the §12.1 erasure contract is
			// compile-checked at this wiring site (F-12.1.5).
			userScoped = append(userScoped,
				erasure.FromCounting("leases", w.erasureLeaseStore))
		}
		if erasureSemanticCache != nil {
			// step 2: purge the user's cached LLM query/response pairs. The
			// §4.9 SemanticCache is a §12.1 pluggable role; erasure.FromStore
			// adapts its error-only DeleteByUser to the orchestrator adapter
			// and compile-checks it against StoreEraser (F-12.1.5).
			userScoped = append(userScoped,
				erasure.FromStore("semantic_cache", erasureSemanticCache))
		}
		if erasureSticky != nil {
			// step 4: delete the user's experiment sticky assignments.
			userScoped = append(userScoped,
				erasure.Eraser{Name: "experiment_sticky", DeleteByUser: erasureSticky.DeleteByUser})
		}
		// step 5: purge staged billing events from the write-ahead buffer
		// (Redis stream + in-memory) before the Runner's pseudonymizing
		// phase, so a post-erasure flush cannot re-insert the raw user_id.
		userScoped = append(userScoped,
			erasure.Eraser{Name: "billing_buffer", DeleteByUser: w.billingPipeline.PurgeStagedByUser})
		// step 6: delete the user's rate-limit and budget counters across the
		// whole §12.2 QuotaStore role — Redis counter, Postgres
		// token_usage_checkpoint, and the in-memory fail-open accumulator —
		// so a post-recovery reconcile cannot re-seed the erased user's usage
		// via the §11.2 line-48 MAX rule. spec: §12.8 step 6 (Redis +
		// Postgres); §12.2.
		if quotaEraser := buildQuotaEraser(quotaCounter, w.pgPool, quotaFailOpenAccum); quotaEraser != nil {
			userScoped = append(userScoped,
				erasure.Eraser{Name: "quota", DeleteByUser: quotaEraser.DeleteByUser})
		}
		userScoped = append(
			userScoped,
			// step 8: §9.4 MemoryStore is a §12.1 pluggable role; FromStore
			// adapts its error-only DeleteByUser and compile-checks it against
			// StoreEraser. Memory and the session-keyed interaction rows
			// precede SessionStore (step 17).
			erasure.FromStore("memory", w.memories),
			erasure.FromCounting("interactions", w.interactions),
			// step 17: SessionStore, the FK parent, after every child store.
			erasure.FromCounting("sessions", w.sessions),
		)
		if w.pgPool != nil {
			// step 18: TokenStore — delete the user's issued OAuth/refresh
			// tokens (the §13.3 issued-token index keyed by sub).
			userScoped = append(userScoped,
				erasure.Eraser{Name: "tokens", DeleteByUser: issuedtokenstore.New(w.pgPool).DeleteByUser})
		}
		// step 19: CredentialPoolStore — drop credential lease assignments
		// referencing the user.
		userScoped = append(userScoped,
			erasure.Eraser{Name: "credential_pool", DeleteByUser: credentialPools.DeleteByUser})

		erasureCfg := erasure.Config{
			Sessions: func(ctx context.Context, tenantID, userID string) ([]string, error) {
				rows, err := w.sessions.List(ctx, tenantID, sessionstore.ListFilter{})
				if err != nil {
					return nil, err
				}
				ids := make([]string, 0, len(rows))
				for _, s := range rows {
					if s.UserID == userID {
						ids = append(ids, s.ID)
					}
				}
				return ids, nil
			},
			SessionScoped: sessionScoped,
			UserScoped:    userScoped,
		}
		// spec: §12.8 lines 792-836 — fail closed if the wired erasure
		// stores violate the dependency order (a foreign-key child erased
		// after its parent would leave orphan rows or violate a constraint).
		if err := erasure.ValidateOrder(erasureCfg); err != nil {
			log.Fatalf("FATAL: §12.8 erasure store ordering invalid: %v", err)
		}
		erasureOrch := erasure.New(erasureCfg)
		erasureJobs := erasurejob.NewMemory()
		erasureRunner := erasurejob.NewRunner(erasureJobs, erasureOrch, nil).
			WithFailureObserver(gwMetrics.IncErasureJobFailed).
			// spec: §12.8 line 768 — emit the in-progress gauge and
			// per-job duration histogram for erasure-throughput / SLA
			// monitoring.
			WithLifecycleMetrics(gwMetrics).
			// spec: §12.8 line 768 / §12.9 — record the tier-specific SLA
			// deadline on each job (T4 Restricted 1h, otherwise 72h).
			WithDeadlineResolver(func(ctx context.Context, tenantID string) time.Duration {
				if t, err := w.tenants.Get(ctx, tenantID); err == nil && t.WorkspaceTier == tenantkms.WorkspaceTierT4 {
					return time.Hour
				}
				return 72 * time.Hour
			}).
			// §12.5 line 317: on completion, trigger an immediate
			// tenant-scoped GC sweep for a `gcPriority: high` tenant.
			WithCompletionHook(func(ctx context.Context, tenantID, _ string) {
				if immediateGCSweep != nil {
					immediateGCSweep(ctx, tenantID)
				}
			})
		// spec: §12.8 lines 743-758 (layer 3) — re-run the MemoryStore
		// erasure preflight at the start of every job so a backend that
		// regressed after the startup check (e.g. a rolling upgrade of an
		// external vector DB) aborts the job as memory_store_preflight_failed
		// before any deletion, leaving processing_restricted set and
		// incrementing lenny_erasure_job_failed_total{failure_phase=
		// memory_store_preflight}. F-9.4.3 / F-12.2.10.
		if w.memories != nil {
			memstore := w.memories
			erasureRunner = erasureRunner.WithMemoryPreflight(func(ctx context.Context) error {
				return memorystore.ValidateMemoryStoreErasure(ctx, memstore)
			})
		}
		// §12.8: billing events are append-only, so the erasure job
		// pseudonymizes them rather than deleting them. Both the in-memory
		// and the Postgres billing stores implement the pseudonymize/count
		// pair (the pg path rewrites under SET LOCAL lenny.erasure_mode), so
		// the BillingEraser is attached whenever the ledger satisfies the
		// erasure interface. The pseudonymize path operates on the durable
		// ledger directly, so it asserts against billingLedger, not the
		// failover pipeline.
		if be, ok := w.billingLedger.(erasurejob.BillingErasureStore); ok {
			billingEraser := erasurejob.NewBillingEraser(be, w.tenants)
			// §12.8 line 856: on Postgres, serialize the per-user pseudonymize
			// and the salt-rotation migration through the cross-replica
			// `erasure_salt_migration:{tenant_id}` advisory lock.
			if w.pgPool != nil {
				billingEraser = billingEraser.WithRotationLock(saltlockpg.New(w.pgPool))
			}
			erasureRunner = erasureRunner.WithBilling(billingEraser)
			// §12.8 line 857: platform-admin compromise-response salt rotation.
			adminRouter = adminRouter.WithErasureSaltRotation(billingEraser)
		}
		// spec: §12.8 lines 810-829 — step-14 OCSF dead-letter PII redaction.
		// When the audit chain is Postgres-backed, scrub raw canonical PII out
		// of the user's dead-lettered audit_log rows in place under a
		// KMS-signed RedactionReceipt, then emit gdpr.erasure_deadletter_redacted
		// (the in-system erasure record) and gdpr.erasure_deadletter_downstream_notified
		// (the GDPR Art. 17(2) signal to OCSF sinks) per row. The payload
		// rewrite runs under SET LOCAL lenny.erasure_mode; migration 0165 grants
		// lenny_erasure UPDATE on the two payload columns, and the erasure-role
		// connection wiring is shared with the Phase-4 audit DeleteByTenant
		// under F-12.2.16. The in-memory audit chain has no dead_lettered rows,
		// so the redaction is wired only on the durable store.
		if auditOpsStore != nil && auditValidator != nil {
			receiptSigner, rerr := deadletterredaction.NewKMSReceiptSigner(
				context.Background(), w.kmsProvider, "platform:audit-redaction-signing", "boot",
			)
			if rerr != nil {
				log.Fatalf("lenny-gateway: §12.8 redaction receipt signer: %v", rerr)
			}
			redactionSvc := deadletterredaction.New(deadletterredaction.Config{
				Store:  auditOpsStore,
				Emit:   auditValidator,
				Signer: receiptSigner,
				// Re-derive the OCSF translation error class the row was
				// dead-lettered with, so the redacted payload preserves it for
				// pipeline-failure forensics (§12.8 line 810).
				Classify: func(row audit.Row) string {
					_, err := ocsf.Translate(ocsf.Input{
						ID:                 row.ID,
						Sequence:           row.Seq,
						TenantID:           row.TenantID,
						EventType:          row.EventType,
						EventSchemaVersion: row.EventSchemaVersion,
						CreatedAtUnixMs:    row.Timestamp.UTC().UnixMilli(),
						Payload:            row.Payload,
						PrevHash:           row.PrevHash,
						ChainIntegrity:     audit.ChainUnchecked,
					})
					var te *ocsf.TranslateError
					if errors.As(err, &te) {
						return string(te.Class)
					}
					return string(ocsf.ErrClassMappingMissing)
				},
			})
			erasureRunner = erasureRunner.WithDeadLetterRedaction(redactionSvc.RedactForUser)
		}
		adminRouter = adminRouter.WithErasure(erasureRunner, erasureJobs)
		// spec: §12.8 line 768 — publish the erasure-SLA gauges
		// (lenny_erasure_job_age_seconds, lenny_erasure_job_deadline_seconds)
		// on a periodic tick so the §16.5 ErasureJobOverdue alert can detect
		// a stalled job before its deadline breaches. The Runner cannot
		// advance a job's age while blocked inside a slow DeleteByUser, so a
		// separate sampler reads the registry. Default deadline is the §12.9
		// T3 72h bound the alert's scalar() compares against.
		erasureSampler := erasurejob.NewSampler(erasureJobs, gwMetrics, 72*time.Hour, clockinject.Now)
		go erasureSampler.Run(context.Background(), 30*time.Second)
	}

	// §12.8 tenant-deletion lifecycle. The gateway hosts the background
	// reconciler that walks a tenant marked for deletion (state
	// `disabling`/`deleting`, set by DELETE /v1/admin/tenants/{id})
	// through the §12.8 phases: soft-disable, terminate sessions, revoke
	// credentials, Phase 3.5 legal-hold segregation (standard blocking
	// path), DeleteByTenant across the erasure-scope stores, Phase 4a KMS
	// destroy (operator-driven on a probe-only host), CRD cleanup, and the
	// erasure receipt. The persisted TenantState column is the durable
	// anchor; an in-memory Job is reconstructed from it each pass. A
	// Postgres advisory lock keeps the destructive loop on one replica.
	// spec: §12.8 line 865, lines 872-889. F-12.8.1, F-24.10.3.
	{
		var tenantErasers []namedTenantEraser
		if w.erasureLeaseStore != nil {
			tenantErasers = append(tenantErasers, namedTenantEraser{"leases", w.erasureLeaseStore.DeleteByTenant})
		}
		// §12.8 Phase 4 quota erasure spans the whole §12.2 QuotaStore role:
		// Redis counter, Postgres token_usage_checkpoint, and the in-memory
		// fail-open accumulator.
		if quotaEraser := buildQuotaEraser(quotaCounter, w.pgPool, quotaFailOpenAccum); quotaEraser != nil {
			tenantErasers = append(tenantErasers, namedTenantEraser{"quota", quotaEraser.DeleteByTenant})
		}
		if w.memories != nil {
			mem := w.memories
			tenantErasers = append(tenantErasers, namedTenantEraser{"memory", func(ctx context.Context, tenantID string) (int, error) {
				return 0, mem.DeleteByTenant(ctx, tenantID)
			}})
		}
		tenantErasers = append(
			tenantErasers,
			namedTenantEraser{"eval_results", w.evals.DeleteByTenant},
			namedTenantEraser{"interactions", w.interactions.DeleteByTenant},
			// SessionStore is the FK parent of the session-keyed stores
			// above, so it is erased after them (§12.8 Phase 4 order).
			namedTenantEraser{"sessions", w.sessions.DeleteByTenant},
		)
		if w.pgPool != nil {
			tenantErasers = append(tenantErasers,
				namedTenantEraser{"tokens", issuedtokenstore.New(w.pgPool).DeleteByTenant})
		}
		tenantErasers = append(tenantErasers,
			namedTenantEraser{"credential_pool", credentialPools.DeleteByTenant})

		deletionReconciler := &tenantdeletion.Reconciler{
			Jobs:     tenantdeletion.NewMemory(),
			Eraser:   &tenantEraser{erasers: tenantErasers},
			Disabler: tenantStateDisabler{tenants: w.tenants, clock: clockinject.Now},
			Clock:    clockinject.Now,
		}
		if auditAppender != nil {
			deletionReconciler.Receipts = auditReceiptSink{appender: auditAppender, clock: clockinject.Now}
			deletionReconciler.Blocked = tenantDeletionBlockedSink{appender: auditAppender, clock: clockinject.Now}
		}
		// §12.8 Phase 3.5 standard path: enumerate active tenant-scoped
		// legal holds (session + artifact) so the deletion pauses rather
		// than destroying held evidence.
		holdEnum := tenantHoldEnumerator{sessions: w.sessions}
		if w.artifactCatalog != nil {
			holdEnum.artifacts = w.artifactCatalog
		}
		deletionReconciler.LegalHolds = holdEnum

		// §12.8 Phase 3.5 override path: force-delete with
		// acknowledgeHoldOverride segregates held evidence into the
		// region-scoped escrow before deletion proceeds. An unconfigured
		// escrow (no --legal-hold-escrow-bucket) leaves the migrator
		// resolving every region as unresolvable, so the override fails
		// closed rather than destroying evidence. spec: §12.8 lines 880-889.
		if auditAppender != nil {
			escrowCfg := escrowConfigFromFlags(*legalHoldEscrowBucket, *legalHoldEscrowEndpoint)
			// §12.8 sub-step 4 escrow record store: durable when Postgres is
			// wired (the records must survive the tenant tombstone so a hold
			// cleared after Phase 4 still resolves the escrow objects), else
			// in-memory. The migrator's escrowLedger writes records here and
			// the admin clear path's Releaser queries them.
			var escrowRecords legalholdescrow.RecordStore
			if w.pgPool != nil {
				escrowRecords = legalholdescrowpg.New(w.pgPool)
			} else {
				escrowRecords = legalholdescrow.NewMemRecordStore()
			}
			escrowLed := escrowLedger{appender: auditAppender, records: escrowRecords, clock: clockinject.Now}
			deletionReconciler.Escrow = tenantEscrowMigrator{
				cfg:       escrowCfg,
				tenants:   w.tenants,
				artifacts: w.artifactCatalog,
				blobs:     w.blobs,
				cipher:    escrowCipherFactory(w.kmsProvider),
				ledger:    escrowLed,
				metrics:   gwMetrics,
				appender:  auditAppender,
				clock:     clockinject.Now,
			}
			deletionReconciler.Override = escrowOverrideSink{
				appender: auditAppender,
				metrics:  gwMetrics,
				clock:    clockinject.Now,
			}
			// §12.8 line 884 escrow-GC release: clearing a hold (hold: false)
			// deletes the escrow objects it protected and emits
			// legal_hold.escrow_released.
			adminRouter = adminRouter.WithEscrowReleaser(&legalholdescrow.Releaser{
				Records: escrowRecords,
				Deleter: blobEscrowDeleter{blobs: w.blobs},
				Ledger:  escrowLed,
				Clock:   clockinject.Now,
			})
		}

		// The receipt sink is required by the reconciler; without an audit
		// appender (a minimal no-audit posture) the lifecycle cannot write
		// its §12.8 Phase 6 proof, so the runner is only started when one
		// is wired.
		if deletionReconciler.Receipts != nil {
			runner := &tenantDeletionRunner{
				reconciler: deletionReconciler,
				tenants:    w.tenants,
				clock:      clockinject.Now,
				interval:   30 * time.Second,
				pool:       w.pgPool,
			}
			go runner.Start(context.Background())
			log.Printf("lenny-gateway: §12.8 tenant-deletion controller started (erasure stores: %d)", len(tenantErasers))
		}
	}
	if w.pgPool != nil {
		// §13.3 operator-initiated token revocation, durable in the
		// issued-token index and reflected in the revocation cache. The
		// admin endpoint routes through the propagator so a revocation
		// fans out to every replica's cache over Redis pub/sub, not just
		// the replica that served the request.
		adminRouter = adminRouter.WithIssuedTokens(issuedtokenstore.New(w.pgPool), revProp)
	}
	// spec: §17.6 lines 455-474 — the initial admin credential. Wired
	// when the gateway has both an in-cluster client (to write the
	// Secret) and a durable token store (so the minted token is
	// revocable on rotation). A deployment lacking either, or with
	// --admin-token-disabled, skips provisioning; the bootstrap response
	// then carries no adminToken section and the CLI prints no prompt.
	// F-17.6.3 / F-24.1.7.
	if !*adminTokenDisabled && w.clusterClient != nil && w.pgPool != nil && *adminTokenNamespace != "" {
		adminTokenProv, atErr := admintoken.New(admintoken.Config{
			Namespace:   *adminTokenNamespace,
			SecretName:  *adminTokenSecretName,
			AdminTenant: *adminTokenTenant,
			Issuer:      *bearerExpectedIssuer,
			Audience:    w.expectedAuds,
		}, w.jwtSigner, w.users,
			k8ssecret.New(w.clusterClient),
			adminIssuedTokens{store: issuedtokenstore.New(w.pgPool), cache: revProp, metrics: gwMetrics, clock: clockinject.Now},
			clockinject.Now)
		if atErr != nil {
			log.Fatalf("lenny-gateway: initial admin credential: %v", atErr)
		}
		adminRouter = adminRouter.WithAdminTokenProvisioner(adminTokenProv)
		log.Printf("lenny-gateway: initial admin credential active (Secret %s/%s, user %s)",
			*adminTokenNamespace, *adminTokenSecretName, adminTokenProv.Username())
	}
	// §11.4 full_revoke fan-out. Each dependency is independently
	// optional: the pod terminator is wired only with warm-pod placement
	// (--agent-namespace), the lease revoker only when the lease store
	// exposes per-session lookup, and the token revoker only with a
	// Postgres-backed issued-token index. A minimal gateway wires none
	// of them and still soft/hard disables a user.
	//
	// userPodTerminateProp carries the §11.4 step-2 Terminate request to
	// peer replicas over Redis pub/sub so a revoked user's pods bound on
	// other replicas are terminated too. Its Run subscriber is launched
	// alongside the other revocation propagators below. F-11.4.3.
	var userPodTerminateProp *podterminateprop.Propagator
	{
		var (
			userPods   admin.UserPodTerminator
			userLeases admin.UserLeaseRevoker
			userTokens admin.UserTokenRevoker
		)
		if w.podRegistry != nil {
			fanOut := &podTerminateFanOut{registry: w.podRegistry}
			fanOut.prop = podterminateprop.New(fanOut, w.securityBus, w.replica,
				podterminateprop.WithErrorHandler(func(err error) {
					log.Printf("lenny-gateway: §11.4 full_revoke: publish pod-termination request: %v", err)
				}))
			userPodTerminateProp = fanOut.prop
			userPods = fanOut
		}
		// llmLeases is the §4.9 credential-lease store; both the in-memory
		// and Postgres-backed implementations expose LeasesBySession, so
		// the assertion succeeds for either backend. The credential-lease
		// revocation propagator carries a revoked lease's credential
		// across replicas — onto every replica's deny list and renewal
		// worker — so the §11.4 full_revoke fan-out stops the lease
		// reaching the provider fleet-wide and no replica renews it.
		if ls, ok := w.llmLeases.(userLeaseStore); ok {
			userLeases = &userLeaseRevoker{leases: ls, denyList: credRenewalProp}
		}
		if w.pgPool != nil {
			userTokens = &userTokenRevoker{store: issuedtokenstore.New(w.pgPool)}
		}
		adminRouter = adminRouter.WithUserRevocation(userPods, userLeases, userTokens)
	}
	// §4.9 emergency credential revocation lease terminator. Wired when
	// the lease store exposes per-credential lookup (both backends do);
	// it reuses the credential-lease revocation propagator so a revoked
	// pool credential is denied on every replica, mirroring the §11.4
	// full_revoke fan-out's deny-list path.
	if ls, ok := w.llmLeases.(poolLeaseStore); ok {
		adminRouter = adminRouter.WithPoolCredentialRevocation(
			&poolCredentialRevoker{leases: ls, denyList: credRenewalProp},
		)
		// §24.5 row 2: surface per-credential lease counts on the admin GET
		// from the same lease store the revoker drains.
		adminRouter = adminRouter.WithPoolCredentialHealth(
			&poolCredentialHealthReader{leases: ls},
		)
	}
	// §4.9.1 KMS-key-rotation re-encryption admin surface. Registered
	// only when at least one envelope-backed store is wired (Postgres).
	if credentialRekeyJob != nil {
		adminRouter = adminRouter.WithCredentialRekey(credentialRekeyJob)
	}
	// §4.9 admin-time RBAC live-probe on credential-pool writes. Wired
	// only when the Token Service link is present.
	if w.secretProber != nil {
		adminRouter = adminRouter.WithSecretAccessProber(w.secretProber)
	}
	adminRouter = adminRouter.WithPlatformInfo(
		admin.PlatformInfo{
			Version:       buildVersion,
			GitCommit:     buildCommit,
			BuildDate:     buildDate,
			OpsServiceURL: *opsServiceURL,
		},
		map[string]string{
			"gateway.addr":          *addr,
			"gateway.multiTenant":   boolStr(*multiTenant),
			"gateway.devMode":       boolStr(*devMode),
			"gateway.runtimeBin":    *runtimeBin,
			"gateway.postgres":      boolStr(*postgresDSN != ""),
			"gateway.redis":         boolStr(*redisURL != ""),
			"gateway.replicaId":     w.replica,
			"gateway.opsServiceURL": *opsServiceURL,
		},
	)
	// §11.2.1 operator-initiated billing-correction workflow. The
	// correction endpoints write through the failover billing pipeline.
	// Pending dual-control requests are held in the durable Postgres
	// registry when Postgres is wired, so a gateway restart does not lose
	// a pending request or its four-eyes audit trail (the spec rules out
	// restart-fragility for financial controls); the in-memory registry
	// backs the Postgres-less minimal gateway. F-11.2.11.
	var corrections correctionstore.Store = correctionstore.NewMemory()
	if w.pgPool != nil {
		corrections = correctionpg.New(w.pgPool)
	}
	adminRouter = adminRouter.WithBillingCorrections(
		w.billing, corrections, *billingDualControlThreshold,
	)
	// spec: §11.2.1 line 175 — wire billing.approverNotificationWebhook so
	// a dual-control correction entering the pending state notifies
	// eligible approvers. Nil leaves the channel unconfigured. F-11.2.14.
	if approverNotifier, err := buildApproverNotifier(*billingApproverWebhookURL, billingApproverWebhookSecret); err != nil {
		log.Fatalf("lenny-gateway: billing approver notification webhook: %v", err)
	} else if approverNotifier != nil {
		adminRouter = adminRouter.WithApproverNotifier(approverNotifier)
		log.Printf("lenny-gateway: §11.2.1 billing approver-notification webhook configured")
	}
	// spec: §24.6 line 99 / §15.1 line 879 — back `POST /v1/admin/quota/reconcile`
	// with the §11.2 MAX-rule reconcile over the Postgres token-usage
	// checkpoint. Until the checkpoint store is wired (no Redis/Postgres)
	// the route keeps answering 503 QUOTA_RECONCILE_UNAVAILABLE.
	// F-24.6.2 / F-24.6.3.
	if quotaCheckpointSvc != nil {
		adminRouter = adminRouter.WithQuotaReconciler(quotacheckpoint.AdminReconciler{Service: quotaCheckpointSvc})
	}
	// spec: §4.1 — record the constructed admin router and the admin-block
	// subsystems the §4.1 background-worker step reads onto the accumulator.
	// The §27 playground-revocation and §12.2 artifact-replication WithX
	// wiring that the mux and the background-worker step add later mutate
	// w.adminRouter in place.
	w.adminRouter = adminRouter
	w.impersonationSvc = impersonationSvc
	w.kmsProbeLifecycle = kmsProbeLifecycle
	w.recommendationStore = recommendationStore
	w.userPodTerminateProp = userPodTerminateProp
	w.immediateGCSweep = immediateGCSweep
	return connectorAuthorizer, connectorInvoker, ruStore, erasureSemanticCache
}

// buildHTTPSurface is an extracted §4.1 composition-root build step (R1).
// spec: §4.1 gateway subsystem seams.
// buildHTTPSurface composes the §15.1 REST mux: it mounts the session,
// admin, credential, playground, JWKS, metrics, health, readiness, and
// drain-readiness handlers, wraps them in the §27.3 / §11 middleware stack
// (correlation, auth, rate-limit, idempotency, fail-open), and records the
// resulting *http.Server on w.httpSrv. The §13.4 stateless-routing wiring
// mutates w.mux in place later.
//
// spec: §4.1 gateway subsystem seams; §15.1 REST API; §27.3 middleware.
