// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"crypto/tls"
	"net"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/lennylabs/lenny/pkg/audit"
	"github.com/lennylabs/lenny/pkg/auth/authmw"
	"github.com/lennylabs/lenny/pkg/auth/jwt"
	"github.com/lennylabs/lenny/pkg/blobstore/miniostore"
	"github.com/lennylabs/lenny/pkg/credential/credcache"
	"github.com/lennylabs/lenny/pkg/credential/credentialserver"
	"github.com/lennylabs/lenny/pkg/credential/credentialstore"
	"github.com/lennylabs/lenny/pkg/gateway/api/pipeline"
	"github.com/lennylabs/lenny/pkg/gateway/api/serviceidentity"
	"github.com/lennylabs/lenny/pkg/gateway/connectors/connectorcredstore"
	"github.com/lennylabs/lenny/pkg/gateway/connectors/connectorstore"
	"github.com/lennylabs/lenny/pkg/gateway/core/coordination/coordlease"
	"github.com/lennylabs/lenny/pkg/gateway/core/coordination/leasestore"
	"github.com/lennylabs/lenny/pkg/gateway/core/session/budgetstate"
	"github.com/lennylabs/lenny/pkg/gateway/core/session/executor"
	"github.com/lennylabs/lenny/pkg/gateway/core/session/orphansession"
	"github.com/lennylabs/lenny/pkg/gateway/core/session/recycle"
	"github.com/lennylabs/lenny/pkg/gateway/core/session/sessioninbox"
	"github.com/lennylabs/lenny/pkg/gateway/core/session/slotcounter"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credassign"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credleasestore"
	"github.com/lennylabs/lenny/pkg/gateway/drain/drainreadiness"
	"github.com/lennylabs/lenny/pkg/gateway/experiment/experimentstore"
	"github.com/lennylabs/lenny/pkg/gateway/meteringproxy/llmproxy"
	"github.com/lennylabs/lenny/pkg/gateway/platformtools/delegation"
	"github.com/lennylabs/lenny/pkg/gateway/platformtools/leasecontrol"
	"github.com/lennylabs/lenny/pkg/gateway/platformtools/toolapproval"
	"github.com/lennylabs/lenny/pkg/gateway/uploadhandler"
	"github.com/lennylabs/lenny/pkg/kms"
	"github.com/lennylabs/lenny/pkg/policy/ratelimit"
	"github.com/lennylabs/lenny/pkg/runtime/runtimecapoverride"
	"github.com/lennylabs/lenny/pkg/runtime/runtimestore"
	"github.com/lennylabs/lenny/pkg/session/interactionstore"
	"github.com/lennylabs/lenny/pkg/session/sessionlogstore"
	"github.com/lennylabs/lenny/pkg/storage/deadletterredaction"
	"github.com/lennylabs/lenny/pkg/storerouter"
	"github.com/lennylabs/lenny/pkg/tenancy/userstore"

	"github.com/lennylabs/lenny/pkg/billing/sessionusage"
	"github.com/lennylabs/lenny/pkg/blobstore"
	"github.com/lennylabs/lenny/pkg/blobstore/artifactcatalog"
	"github.com/lennylabs/lenny/pkg/blobstore/cataloging"
	"github.com/lennylabs/lenny/pkg/checkpoint/checkpointretention"
	"github.com/lennylabs/lenny/pkg/checkpoint/partialmanifeststore"
	"github.com/lennylabs/lenny/pkg/credential/credentialpoolstore"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credrenewal"
	"github.com/lennylabs/lenny/pkg/gateway/experiment/evalstore"
	"github.com/lennylabs/lenny/pkg/gateway/platformtools/treebudget"
	"github.com/lennylabs/lenny/pkg/gateway/session/memorystore"
	"github.com/lennylabs/lenny/pkg/quota/quotabudget"
	"github.com/lennylabs/lenny/pkg/quota/quotacheckpoint"
	"github.com/lennylabs/lenny/pkg/quota/quotafailopen"
	"github.com/lennylabs/lenny/pkg/storage/pubsub"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"google.golang.org/grpc"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/lennylabs/lenny/pkg/alerting/evaluator"
	"github.com/lennylabs/lenny/pkg/audit/auditretention"
	"github.com/lennylabs/lenny/pkg/audit/auditscope"
	"github.com/lennylabs/lenny/pkg/audit/auditstore"
	"github.com/lennylabs/lenny/pkg/audit/auditstore/auditbatch"
	"github.com/lennylabs/lenny/pkg/audit/deploymentconfigstore"
	"github.com/lennylabs/lenny/pkg/audit/ocsf"
	"github.com/lennylabs/lenny/pkg/audit/siem"
	"github.com/lennylabs/lenny/pkg/billing/billingfanout"
	"github.com/lennylabs/lenny/pkg/billing/billingstore"
	"github.com/lennylabs/lenny/pkg/billing/billingstore/failover"
	"github.com/lennylabs/lenny/pkg/billing/billingstore/failover/redisstream"
	"github.com/lennylabs/lenny/pkg/billing/usagestore"
	"github.com/lennylabs/lenny/pkg/connectoroauth"
	"github.com/lennylabs/lenny/pkg/credential/denylist"
	"github.com/lennylabs/lenny/pkg/credential/revocation"
	revocationprop "github.com/lennylabs/lenny/pkg/credential/revocation/propagator"
	"github.com/lennylabs/lenny/pkg/credential/usercreds"
	"github.com/lennylabs/lenny/pkg/delegation/delegationpolicystore"
	"github.com/lennylabs/lenny/pkg/driftmonitor"
	"github.com/lennylabs/lenny/pkg/events"
	"github.com/lennylabs/lenny/pkg/events/eventbuffer"
	"github.com/lennylabs/lenny/pkg/gateway/api/admin"
	"github.com/lennylabs/lenny/pkg/gateway/api/circuitbreaker/breakerstore/cachingstore"
	"github.com/lennylabs/lenny/pkg/gateway/api/routetable"
	"github.com/lennylabs/lenny/pkg/gateway/core/checkpoint/checkpointer"
	"github.com/lennylabs/lenny/pkg/gateway/core/coordination/coordination"
	"github.com/lennylabs/lenny/pkg/gateway/core/coordination/dualstore"
	"github.com/lennylabs/lenny/pkg/gateway/core/session/bindrelease"
	"github.com/lennylabs/lenny/pkg/gateway/core/session/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/core/session/sessioncallback"
	"github.com/lennylabs/lenny/pkg/gateway/core/session/sessionidle"
	"github.com/lennylabs/lenny/pkg/gateway/core/session/sessionserver"
	"github.com/lennylabs/lenny/pkg/gateway/core/session/slothealth"
	"github.com/lennylabs/lenny/pkg/gateway/core/session/watchdog"
	credrenewalprop "github.com/lennylabs/lenny/pkg/gateway/credentials/credrenewal/propagator"
	"github.com/lennylabs/lenny/pkg/gateway/environment/translator"
	"github.com/lennylabs/lenny/pkg/gateway/experiment/experimentprovider"
	"github.com/lennylabs/lenny/pkg/gateway/experiment/experimentsticky"
	"github.com/lennylabs/lenny/pkg/gateway/meteringproxy/metering"
	"github.com/lennylabs/lenny/pkg/gateway/meteringproxy/sessionbudget"
	"github.com/lennylabs/lenny/pkg/gateway/opsendpoints/health"
	"github.com/lennylabs/lenny/pkg/gateway/platformtools/deadlock"
	"github.com/lennylabs/lenny/pkg/gateway/platformtools/delegationbudget"
	"github.com/lennylabs/lenny/pkg/gateway/platformtools/elicitationfloor"
	"github.com/lennylabs/lenny/pkg/gateway/platformtools/inputwait"
	"github.com/lennylabs/lenny/pkg/gateway/platformtools/mcp"
	"github.com/lennylabs/lenny/pkg/gateway/platformtools/resultrollup"
	"github.com/lennylabs/lenny/pkg/gateway/platformtools/treearchive"
	"github.com/lennylabs/lenny/pkg/gateway/replica/gatewaymetrics"
	"github.com/lennylabs/lenny/pkg/gateway/seam/subsystem"
	"github.com/lennylabs/lenny/pkg/gateway/streamproxy/sessionevents"
	"github.com/lennylabs/lenny/pkg/kms/rekey"
	mtlsdenylist "github.com/lennylabs/lenny/pkg/mtls/denylist"
	mtlsdenylistprop "github.com/lennylabs/lenny/pkg/mtls/denylist/propagator"
	"github.com/lennylabs/lenny/pkg/policy"
	"github.com/lennylabs/lenny/pkg/policy/interceptor"
	"github.com/lennylabs/lenny/pkg/policy/interceptor/interceptorstore"
	"github.com/lennylabs/lenny/pkg/quota/quotastore"
	"github.com/lennylabs/lenny/pkg/quota/storagequota"
	"github.com/lennylabs/lenny/pkg/recommendations"
	"github.com/lennylabs/lenny/pkg/runtime/poolstore"
	"github.com/lennylabs/lenny/pkg/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/session/transcriptstore"
	"github.com/lennylabs/lenny/pkg/storage/erasurejob"
	"github.com/lennylabs/lenny/pkg/storage/eventbus"
	"github.com/lennylabs/lenny/pkg/storage/failopen"
	"github.com/lennylabs/lenny/pkg/storage/redistopology"
	"github.com/lennylabs/lenny/pkg/storage/sqlitestore"
	"github.com/lennylabs/lenny/pkg/tenancy/customrolestore"
	"github.com/lennylabs/lenny/pkg/tenancy/environmentstore"
	"github.com/lennylabs/lenny/pkg/tenancy/tenantaccessstore"
	"github.com/lennylabs/lenny/pkg/tenancy/tenantstore"
	"github.com/lennylabs/lenny/pkg/tenantkms"
	"github.com/lennylabs/lenny/pkg/uploadtoken"
	"github.com/lennylabs/lenny/pkg/workspaceplan/vcscred"
)

// gatewayWiringFields holds the cross-step composition-root locals the
// build steps thread between each other. A field is assigned by the step
// that constructs the component and read by a later step that wires it.
// The former monolithic composition root held these as inline locals; the
// per-subsystem decomposition (R1) lifts the ones that cross a step
// boundary onto the accumulator so each step stays a focused unit.
//
// spec: §4.1 — subsystem boundaries threaded through the composition root.
type gatewayWiringFields struct {
	// Run-and-shutdown loop inputs (§17). Set by the construct-and-wire
	// steps; read by runServers.
	httpSrv             *http.Server
	llmProxySrv         *http.Server
	gatewayCtrlSrv      *grpc.Server
	gatewayCtrlLis      net.Listener
	pgPool              *pgxpool.Pool
	redisClient         redis.UniversalClient
	concernRedis        *redistopology.Clients
	traceShutdown       func(context.Context) error
	sqliteDB            *sqlitestore.DB
	sqliteFlushCancel   context.CancelFunc
	experimentProviders *experimentprovider.Cache
	gwMetrics           *gatewaymetrics.Metrics
	// meteringMetrics is the metering proxy's own metric set, registered on
	// the gateway's registry, so the metering proxy holds no value of the
	// replica's gateway metrics.
	meteringMetrics *llmproxy.PromMetrics
	opsEmitter      events.EventEmitter
	replica         string
	// coordHolder is the §10.1.1 Replica address holder value every
	// REG-COORDLEASE and REG-COORDMIRROR consumer receives: the session
	// server, the coordination Sweeper, the barrier mirror lister, the
	// coordination seams, and the coordination fencer. Set by
	// buildRedisAndQuota. replica stays the identity of the ops Source,
	// billing, service.instance.id, and the nonce checkpoint.
	coordHolder string
	// holderForwardEnabled is true when a lease store is wired and the
	// gateway replica certificate material is complete, so the gateway
	// builds the CH-HOLDERFORWARD client and listener (§10.3 NET-072).
	holderForwardEnabled bool
	// holderForwardClient is the shared CH-HOLDERFORWARD client; nil without
	// the material. holderForwardTLS is the listener's TLS configuration.
	holderForwardClient *http.Client
	holderForwardTLS    *tls.Config
	// holderForwardSrv is the CH-HOLDERFORWARD listener, started and drained
	// with the REST listener; nil when the forward is not enabled.
	holderForwardSrv *http.Server
	// restAuthOpts is the auth middleware configuration of the REST
	// listener, which the forward listener reuses (§29.3 Forwarding rule 3).
	restAuthOpts authmw.Options
	// §10.6 resolved noEnvironmentPolicy, computed by buildStartupGates from
	// the --no-environment-policy flag and dev mode; read by the session
	// server, the MCP fabric, and the HTTP surface.
	resolvedNoEnvPolicy string
	alertEvalPtr        *atomic.Pointer[evaluator.Evaluator]
	watchdogCtx         context.Context
	// watchdogCancel cancels watchdogCtx. buildControlServer records it; the
	// composition root (runGateway) defers it so the §6.2 watchdog context
	// lives for the process lifetime rather than only for the build step.
	watchdogCancel context.CancelFunc

	// Audit background loops the run loop starts after installing the
	// signal handler (§11.7 item 2, §11.7 Wire Format, §12.3).
	resolvedGrantCheckInterval time.Duration
	grantCheckRegulated        bool
	ocsfTranslator             *ocsf.Translator
	ocsfOutbox                 *siem.Outbox
	auditBatchBuffer           *auditbatch.Buffer

	// §11.7 audit-pipeline outputs. buildAuditPipeline constructs the
	// per-tenant hash chain (durable or in-memory), the §11.7 write-scope
	// validator, the admin and policy audit sinks, the admin-router audit
	// wiring closure, the §16.4 retention pruner, the durable Store the §25.5
	// ops-stream escalation hook attaches to, and the §11.7 SIEM health
	// checker; the session server, the MCP fabric, the admin router, and the
	// HTTP surface read them back.
	auditSink         admin.AuditSink
	wireAudit         func(*admin.Router) *admin.Router
	auditValidator    *auditscope.Validator
	auditOpsStore     *auditstore.Store
	siemHealthChecker health.Checker

	// §12.8 redaction receipt signing outputs. buildReceiptSigning records
	// the receipt keys a chain verifier trusts and the KMS signer the
	// erasure job's dead-letter redaction signs with.
	receiptKeys   *audit.ReceiptKeySet
	receiptSigner *deadletterredaction.KMSReceiptSigner

	// §10.6 / §25.3 / §4.9 / §10.2 / §8.8 auxiliary-store outputs.
	// buildAuxStores selects the in-memory or Postgres backend for the
	// environment, tenant-access, credential-pool, custom-role, usage, and
	// session-usage stores, constructs the §25.3/§25.5 operational-event
	// emitter and its buffer, the §14 VCS credential resolver, and the §8.8
	// shared usage Builder; the policy chain, the session server, the MCP
	// fabric, the admin router, and the HTTP surface read them back.
	environments     environmentstore.Store
	tenantAccess     tenantaccessstore.Store
	opsEventBuffer   *eventbuffer.EventBuffer
	vcsCreds         vcscred.Resolver
	customRoles      customrolestore.Store
	usage            usagestore.Store
	taskUsageBuilder *resultrollup.Builder

	// §4.1 / §4.2 session-server dependency outputs. buildSessionDeps
	// constructs the §4.1 Upload Handler subsystem gate and metrics, the §8.5
	// request_input registry, the §10.7 sticky-cache and OpenFeature provider
	// cache, the §14 completion-webhook validator/seal/dispatcher, the §11.2
	// mid-session budget enforcer, the §8.6 lease-extension budget source and
	// registrars, the §6.2 activity stamper, and the §5.2/§6.2 slot-health
	// tracker; buildSessionServer, the MCP fabric, the admin router, and the
	// background workers read them back.
	uploadMetrics         *uploadhandler.PromUploadMetrics
	sessionStickyCache    sessionserver.StickyCache
	adminStickyFlusher    admin.StickyFlusher
	erasureSticky         *experimentsticky.RedisCache
	callbackValidator     *sessioncallback.Validator
	callbackSeal          func(ctx context.Context, tenantID string, plaintext []byte) ([]byte, error)
	callbackDispatcher    *sessioncallback.Dispatcher
	budgetTerminator      *budgetSessionTerminator
	sessionBudgetEnforcer *sessionbudget.Enforcer
	// meteringBudgets is the metering proxy's per-session budget state:
	// the enforcer, and the usage recorder and anomaly detector
	// buildLLMProxy attaches. The session server's terminal pipeline and
	// the budget state sweep reach it, and the §8.6 leasecontrol episode
	// fan-out raises or ends a detached session's budget through it, all
	// through seam.BudgetTracking and seam.BudgetResolutions.
	meteringBudgets *metering.Budgets
	// proxyUsageRec is the §4.9 usage recorder buildLLMProxy constructs,
	// which the direct-mode usage loop also feeds. Nil when no usagestore is
	// wired.
	proxyUsageRec *metering.Recorder
	// budgetStateSweep reclaims per-session budget state for sessions that
	// ended on another replica or were erased. buildSessionServer constructs
	// it and startBackgroundWorkers runs it. spec: §11.2, §10.1.1.
	budgetStateSweep *budgetstate.Sweep
	leaseBudgets     *leasecontrol.StoreBudgetSource
	// leaseDenials is the §8.6 DenialStore behind leaseBudgets. It is built
	// whether or not the GatewayControl listener is enabled, because the
	// delegate_task token cap reads the tree's cumulative grants from it.
	leaseDenials    leasecontrol.DenialStore
	activityStamper *sessionidle.Stamper
	slotHealth      *slothealth.Tracker

	// §4.9 / §9.3 credential-surface outputs. buildCredentialSurface
	// constructs the OpenAI/Open Responses translators, the §4.9 end-user
	// credential store and server, the §4.9 pre-authorized user-source
	// materializer, the §9.3 connector-credential store, the §4.9.1
	// KMS-rotation re-encryption job, and the §9.3 connector OAuth flow; the
	// MCP fabric, the admin router, the HTTP surface, the LLM proxy, and the
	// background workers read them back.
	openaiHandler        *translator.OpenAIChatHandler
	responsesHandler     *translator.OpenResponsesHandler
	credentials          credentialstore.Store
	userCredMaterializer *usercreds.Materializer
	credServer           *credentialserver.Server
	connectorCreds       connectorcredstore.Store
	credentialRekeyJob   *rekey.Job
	connectorOAuth       *admin.ConnectorOAuth

	// Constructed subsystems and stores the §4.1 background-worker step
	// (startBackgroundWorkers) reads to launch the periodic sweepers,
	// samplers, reconcilers, and propagator subscribers.
	adminRouter     *admin.Router
	auditAppender   audit.Appender
	auditPruner     *auditretention.Pruner
	billing         billingstore.Store
	billingEmitter  *billingfanout.Emitter
	billingPipeline *failover.Pipeline
	breakerCache    *cachingstore.Store
	breakers        breakerRegistry
	checkpointSvc   *checkpointer.Checkpointer
	clusterClient   client.Client
	coordinator     *coordination.Sweeper
	credDeny        *denylist.DenyList
	// credDenyRebuilt is set once the §4.9 credential deny-list startup
	// rebuild commits its authoritative Reset. The /readyz handler gates
	// readiness fail-closed (503) while it is unset so a fresh replica
	// serves no proxy traffic against a retained revoked lease with an
	// incomplete deny list; the rebuild goroutine (startBillingAndSecurityWorkers)
	// flips it after the union across both credential stores lands.
	// spec: §4.9; §10.4 readiness precedence.
	credDenyRebuilt atomic.Bool
	credRenewalProp *credrenewalprop.Propagator
	deadlockTracker *deadlock.AwaitTracker
	// §8.2 / §9.1 MCP fabric outputs. buildMCPSurface constructs the
	// delegation service, the MCP server, and the delegation-policy,
	// external-interceptor, and deployment-config stores; the admin router,
	// the HTTP surface, and the control server read them back.
	delegationSvc      *delegation.Service
	mcpSrv             *mcp.Server
	delegationPolicies delegationpolicystore.Store
	interceptors       interceptorstore.Store
	deploymentConfig   deploymentconfigstore.Store
	// platformToolsMetrics holds the metric sets the platform tools seam's
	// packages register on the gateway's metric registry. buildMCPSurface
	// builds it; the control server and the reconciler workers read the
	// lease-extension, orphan-cleanup, and budget-reconstruction sets back.
	platformToolsMetrics platformToolsMetrics
	// §4.8 policy interceptor chain outputs. buildPolicyChain constructs the
	// chain, the §8.3 maxInputSize resolver holder, the policy audit sink,
	// and the §11.2 quota counter and tenant-limits resolver; the session
	// server, the MCP fabric, the admin router, the HTTP surface, and the
	// LLM proxy read them back. buildInterceptorRegistration registers the
	// §4.8 external interceptors and guardrails classifier onto the chain and
	// records the §10.3 mTLS deny list the control server reads.
	policyChain                 *interceptor.Chain
	mtlsDeny                    *mtlsdenylist.DenyList
	maxInputResolver            *maxInputSizeResolverHolder
	policyAuditSink             *policy.AuditSink
	quotaCounter                *quotastore.Counter
	tenantLimits                *policy.TenantStoreLimits
	delegationBudgetReconciler  *delegationbudget.Reconciler
	driftMonitor                *driftmonitor.Monitor
	effectiveAuditRetentionDays int
	elicitationFloorProvider    *elicitationfloor.Provider
	evalMatviewEnabled          bool
	eventBus                    *sessionevents.Bus
	eventBusRetranscriber       *eventbus.Retranscriber
	failOpenReplicas            *failopen.ReplicaCount
	inputWaits                  *inputwait.Registry
	// apiRoutes is the API front's route table, through which every part
	// registers its HTTP routes and from which the REST and forward
	// listeners serve them.
	apiRoutes *routetable.Table
	// apiPipeline is the API front's request pipeline of the session paths,
	// which buildHTTPSurface mounts on the REST and forward listeners.
	apiPipeline       *pipeline.Pipeline
	kmsProbeLifecycle *tenantkms.Lifecycle
	mtlsDenyProp      *mtlsdenylistprop.Propagator
	podBinder         *podsession.Binder
	// slotCounter is the §5.2 slot set shared by every concurrent-mode
	// reservation and release; its §12.4 recovery probe runs on
	// watchdogCtx, and the leader-run stale-slot reaper reads and removes
	// its members. Nil when no Redis is wired, which also leaves the reaper
	// unregistered.
	slotCounter         *slotcounter.Counter
	recommendationStore *recommendations.WindowStore
	revCache            *revocation.Cache
	revProp             *revocationprop.Propagator
	sessions            sessionstore.Store
	// postTerminalCap is the §7.1 terminal pipeline budget in effect, the
	// cap of the post-terminal window. buildPersistenceStores computes it
	// once and passes it to the session store's slot queries; the retention
	// GC options, the §12.8 erasure release gate, and the tenant-deletion
	// session terminator read the same value. spec: §7.1 (Post-terminal
	// window).
	postTerminalCap time.Duration
	sessionSrv      *sessionserver.Server
	storageCounter  storagequota.Counter
	// storageLiveBytes is the reservation-aware §12.4 LiveBytesSource seam
	// (live artifact_store bytes plus outstanding checkpoint reservations)
	// buildRedisAndQuota composes once and every storage-counter consumer
	// shares: the during-outage Failover enforcement read, the
	// RecoveryReconciler recovery-edge rebuild, and the startup Rehydrate.
	// Nil when no Postgres artifact catalog is wired.
	storageLiveBytes          storagequota.LiveBytesSource
	storageRecoveryReconciler *storagequota.RecoveryReconciler
	subsystemMetrics          *subsystem.Metrics
	tenants                   tenantstore.Store
	tokenServiceSubsystem     *subsystem.Subsystem
	transcripts               transcriptstore.Store
	uploadRotator             *uploadtoken.Rotator
	uploadSubsystem           *subsystem.Subsystem
	uploadTracker             *uploadtoken.MemoryTracker
	wd                        *watchdog.Watchdog
	billingTier               *redisstream.Tier
	// billingStream is the billing pipeline's Tier 1 stream (the Redis
	// stream, or the in-process MemStream without Redis), which the §12.8
	// tenant-deletion billing_buffer entry purges.
	billingStream       failover.StreamTier
	connectorStateStore *connectoroauth.MemoryStateStore
	deadlockManager     *deadlock.Manager
	dsMonitor           *dualstore.Monitor
	pools               poolstore.Store
	treeArchive         treearchive.Store
	bindReleaseProp     *bindrelease.Propagator
	blobsCataloged      *cataloging.Store
	quotaBudgetTracker  *quotabudget.Tracker
	quotaCheckpointSvc  *quotacheckpoint.Service
	quotaFailOpenAccum  *quotafailopen.Accumulator
	sessionUsage        sessionusage.Store
	treeBudgetConcrete  *treebudget.Reserver
	artifactCatalog     artifactcatalog.Store
	blobs               blobstore.Store
	credRenewalWorker   *credrenewal.Worker
	credentialPools     credentialpoolstore.Store
	evals               evalstore.Store
	immediateGCSweep    func(ctx context.Context, tenantID string)
	partialManifests    partialmanifeststore.Store
	// checkpointRetention is the §12.5 checkpoint retention catalog
	// (session_checkpoints). The §12.8 tenant-deletion Phase 4 eraser
	// erases the tenant's rows through it.
	checkpointRetention checkpointretention.Store
	securityBus         *pubsub.Bus
	memories            memorystore.Store
	memoryBackendLabel  string

	// tokenServiceConn is the §4.3 token-service gRPC connection. buildStores
	// dials it; runGateway closes it on exit (the original deferred close is
	// relocated to the composition root so the connection lives for the
	// process lifetime rather than only for the build step).
	tokenServiceConn *grpc.ClientConn
	auditSyncPool    *pgxpool.Pool
	bearerVerifier   jwt.Verifier
	// idpVerifier is the §10.2 upstream IdP verifier buildIdPVerifier
	// returns (nil when no IdP is configured). The §27.3.1 playground
	// exchanger validates the ID token of its authorization-code flow
	// with it.
	idpVerifier      jwt.Verifier
	billingAuditPool *pgxpool.Pool
	// billingAuditDDLPool is the CREATE-privileged DDL pool for the
	// billing/audit instance (LENNY_PG_BILLING_AUDIT_DDL_DSN). It logs in as
	// the migration-0173 DDL role, distinct from the lenny_app billingAuditPool
	// the StoreRouter resolves for Append, so the admin tenant-provisioning
	// helper can issue CREATE SEQUENCE for the per-tenant billing_seq_/audit_seq_
	// sequences, which lenny_app has no CREATE grant to run. Nil when no DDL DSN
	// is configured (in-memory / SQLite mode). spec: §12.3, §15.1. F-11.2.10.
	billingAuditDDLPool *pgxpool.Pool
	// primaryDDLPool is the CREATE-privileged DDL pool for the primary
	// instance (LENNY_PG_PRIMARY_DDL_DSN). In the separate-instance topology it
	// provisions the per-tenant audit sequence the §13.3 issued-token
	// write-before-issue path seals on the primary. In the single-instance
	// topology it aliases billingAuditDDLPool. spec: §12.3, §15.1. F-11.2.10.
	primaryDDLPool    *pgxpool.Pool
	billingLedger     billingstore.Store
	blobProbe         drainreadiness.Prober
	capOverrides      runtimecapoverride.Store
	connectors        connectorstore.Store
	coordFencer       sessionserver.CoordinationFencer
	coordLeaseStore   leasestore.LeaseStore
	coordMirror       coordlease.Store
	credAssign        credassign.Assigner
	credCache         *credcache.Cache
	erasureLeaseStore leasestore.LeaseStore
	exec              executor.Executor
	expectedAuds      []string
	experiments       experimentstore.Store
	holdCoordinator   *recycle.HoldCoordinator
	inProcessAssign   *credassign.Service
	interactions      interactionstore.Store
	jwtKeySet         *jwt.KeySet
	jwtSigner         *jwt.BreakerSigner
	kmsBreakerObs     *kmsBreakerObserver
	kmsProvider       kms.Provider
	kubeHealthzProbe  func(context.Context) error
	llmLeases         credleasestore.LeaseStore
	messagingCoord    *sessioninbox.Coordinator
	minioStore        *miniostore.Store
	objectStore       blobstore.Store
	podRegistry       *podsession.Registry
	rateLimiter       ratelimit.Counter
	readPool          *pgxpool.Pool
	recycleBoundary   *recycle.RecycleBoundaryCoordinator
	ring              *uploadtoken.KeyRing
	runtimes          runtimestore.Store
	saTokenVerifier   leasecontrol.TokenVerifier
	saUserVerifier    serviceidentity.SATokenAuthenticator
	scatterRouter     *storerouter.SingleShardRouter
	secretProber      admin.SecretAccessProber
	// credentialSecretWriter is the §4.9 writer of Lenny-written credential
	// Secrets, nil when no Token Service is configured. The admin Router
	// and the tenant eraser read it.
	credentialSecretWriter admin.CredentialSecretWriter
	// credentialsNamespace is the resolved §4.9 credentials namespace. The
	// tenant eraser parses stored refs against it when no writer is wired.
	credentialsNamespace string
	sessionLogs          sessionlogstore.Store
	sessionSealer        sessionserver.Sealer
	storeRouter          storerouter.StoreRouter
	toolApprovalWaits    *toolapproval.Registry
	treeBudgetReserver   delegation.TreeBudgetReserver
	uploadIssuer         *uploadtoken.Issuer
	uploadVerifier       *uploadtoken.Verifier
	users                userstore.Store
	// erasureJobs is the §12.8 erasure job store buildPersistenceStores
	// builds for the posture: the Postgres store over the primary pool, or
	// erasurejob.Memory writing the flag through the branch's user store.
	// The admin router, the erasure runner, and the sampler read it.
	erasureJobs erasurejob.Store
	// erasureRunner and erasureSampler are the §12.8 erasure executor and
	// SLA sampler the admin router builds. startLeaderElectedSweeps
	// registers both on the gateway-leader gate, so only the leader runs
	// them. Nil when no erasure surface is built.
	erasureRunner  *erasurejob.Runner
	erasureSampler *erasurejob.Sampler
	// tenantDeletion is the §12.8 tenant-deletion runner the admin router
	// builds when a receipt sink is wired. startLeaderElectedSweeps sets its
	// elector to the gateway-leader elector and launches it. Nil when no
	// receipt sink is wired.
	tenantDeletion *tenantDeletionRunner
	// orphanSessionReconciler is the §10.1.4 orphan-session reconciler
	// startReconcilerWorkers builds when the Postgres agent_pod_state
	// mirror is wired. startLeaderElectedSweeps registers it on the
	// gateway-leader gate, so only the leader runs it. Nil without the
	// mirror.
	orphanSessionReconciler *orphansession.Reconciler
	// checkpointStore is objectStore as the §10.1.7 chunked-checkpoint
	// surface (checkpointStoreOf): the chunk resolver, the checkpoint
	// driver, the partial cleaner, and the backstop sweep read it. Nil on a
	// backend that cannot presign, which startup refuses for a pod-based
	// deployment.
	checkpointStore blobstore.CheckpointStore
}
