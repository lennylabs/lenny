// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"net"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/lennylabs/lenny/pkg/auth/jwt"
	"github.com/lennylabs/lenny/pkg/blobstore/miniostore"
	"github.com/lennylabs/lenny/pkg/gateway/connectors/connectorcredstore"
	"github.com/lennylabs/lenny/pkg/gateway/connectors/connectorstore"
	"github.com/lennylabs/lenny/pkg/gateway/coordination/coordlease"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credassign"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credcache"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credentialserver"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credentialstore"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credleasestore"
	"github.com/lennylabs/lenny/pkg/gateway/delegationtree/leasecontrol"
	"github.com/lennylabs/lenny/pkg/gateway/environment/userstore"
	"github.com/lennylabs/lenny/pkg/gateway/experiment/experimentstore"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegation"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/drainreadiness"
	"github.com/lennylabs/lenny/pkg/gateway/policy/ratelimit"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/runtimecapoverride"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/runtimestore"
	"github.com/lennylabs/lenny/pkg/gateway/session/executor"
	"github.com/lennylabs/lenny/pkg/gateway/session/interactionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/recycle"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessioninbox"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionlogstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/toolapproval"
	"github.com/lennylabs/lenny/pkg/gateway/storage/leasestore"
	"github.com/lennylabs/lenny/pkg/kms"
	"github.com/lennylabs/lenny/pkg/storerouter"

	"github.com/lennylabs/lenny/pkg/blobstore"
	"github.com/lennylabs/lenny/pkg/blobstore/artifactcatalog"
	"github.com/lennylabs/lenny/pkg/blobstore/cataloging"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credentialpoolstore"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credrenewal"
	"github.com/lennylabs/lenny/pkg/gateway/delegationtree/treebudget"
	"github.com/lennylabs/lenny/pkg/gateway/experiment/evalstore"
	"github.com/lennylabs/lenny/pkg/gateway/partialmanifeststore"
	"github.com/lennylabs/lenny/pkg/gateway/quota/quotabudget"
	"github.com/lennylabs/lenny/pkg/gateway/quota/quotacheckpoint"
	"github.com/lennylabs/lenny/pkg/gateway/quota/quotafailopen"
	"github.com/lennylabs/lenny/pkg/gateway/session/memorystore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionusage"
	"github.com/lennylabs/lenny/pkg/gateway/storage/pubsub"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"google.golang.org/grpc"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/lennylabs/lenny/pkg/alerting/evaluator"
	"github.com/lennylabs/lenny/pkg/audit/ocsf"
	"github.com/lennylabs/lenny/pkg/audit/siem"
	"github.com/lennylabs/lenny/pkg/connectoroauth"
	"github.com/lennylabs/lenny/pkg/driftmonitor"
	"github.com/lennylabs/lenny/pkg/events"
	"github.com/lennylabs/lenny/pkg/gateway/auditretention"
	"github.com/lennylabs/lenny/pkg/gateway/auditscope"
	"github.com/lennylabs/lenny/pkg/gateway/auditstore"
	"github.com/lennylabs/lenny/pkg/gateway/auditstore/auditbatch"
	"github.com/lennylabs/lenny/pkg/gateway/billing/billingfanout"
	"github.com/lennylabs/lenny/pkg/gateway/billing/billingstore"
	"github.com/lennylabs/lenny/pkg/gateway/billing/billingstore/failover"
	"github.com/lennylabs/lenny/pkg/gateway/billing/billingstore/failover/redisstream"
	"github.com/lennylabs/lenny/pkg/gateway/billing/usagestore"
	"github.com/lennylabs/lenny/pkg/gateway/breakerstore/cachingstore"
	"github.com/lennylabs/lenny/pkg/gateway/checkpointer"
	"github.com/lennylabs/lenny/pkg/gateway/coordination/coordination"
	"github.com/lennylabs/lenny/pkg/gateway/core/subsystem"
	credrenewalprop "github.com/lennylabs/lenny/pkg/gateway/credentials/credrenewal/propagator"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/denylist"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/impersonation"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/revocation"
	revocationprop "github.com/lennylabs/lenny/pkg/gateway/credentials/revocation/propagator"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/usercreds"
	"github.com/lennylabs/lenny/pkg/gateway/delegationtree/deadlock"
	"github.com/lennylabs/lenny/pkg/gateway/delegationtree/resultrollup"
	"github.com/lennylabs/lenny/pkg/gateway/delegationtree/treearchive"
	"github.com/lennylabs/lenny/pkg/gateway/environment/customrolestore"
	"github.com/lennylabs/lenny/pkg/gateway/environment/deploymentconfigstore"
	"github.com/lennylabs/lenny/pkg/gateway/environment/environmentstore"
	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantaccessstore"
	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
	"github.com/lennylabs/lenny/pkg/gateway/environment/transcriptstore"
	"github.com/lennylabs/lenny/pkg/gateway/environment/translator"
	"github.com/lennylabs/lenny/pkg/gateway/eventbuffer"
	"github.com/lennylabs/lenny/pkg/gateway/experiment/experimentprovider"
	"github.com/lennylabs/lenny/pkg/gateway/experiment/experimentsticky"
	"github.com/lennylabs/lenny/pkg/gateway/externalapi/admin"
	"github.com/lennylabs/lenny/pkg/gateway/gatewaymetrics"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegationbudget"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegationpolicystore"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/elicitationfloor"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/mcp"
	"github.com/lennylabs/lenny/pkg/gateway/operability/health"
	"github.com/lennylabs/lenny/pkg/gateway/operability/recommendations"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
	podterminateprop "github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podterminate/propagator"
	"github.com/lennylabs/lenny/pkg/gateway/policy/interceptor"
	"github.com/lennylabs/lenny/pkg/gateway/policy/interceptor/interceptorstore"
	"github.com/lennylabs/lenny/pkg/gateway/policy/policy"
	"github.com/lennylabs/lenny/pkg/gateway/quota/quotastore"
	"github.com/lennylabs/lenny/pkg/gateway/quota/storagequota"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/poolstore"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/slothealth"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/watchdog"
	"github.com/lennylabs/lenny/pkg/gateway/session/inputwait"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionbudget"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessioncallback"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionevents"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionidle"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	"github.com/lennylabs/lenny/pkg/gateway/storage/dualstore"
	"github.com/lennylabs/lenny/pkg/gateway/storage/eventbus"
	"github.com/lennylabs/lenny/pkg/gateway/storage/failopen"
	"github.com/lennylabs/lenny/pkg/gateway/storage/redistopology"
	"github.com/lennylabs/lenny/pkg/gateway/storage/sqlitestore"
	"github.com/lennylabs/lenny/pkg/gateway/vcscred"
	"github.com/lennylabs/lenny/pkg/kms/rekey"
	mtlsdenylist "github.com/lennylabs/lenny/pkg/mtls/denylist"
	mtlsdenylistprop "github.com/lennylabs/lenny/pkg/mtls/denylist/propagator"
	"github.com/lennylabs/lenny/pkg/tenantkms"
	"github.com/lennylabs/lenny/pkg/uploadtoken"
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
	opsEmitter          events.EventEmitter
	replica             string
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
	uploadMetrics         *sessionserver.PromUploadMetrics
	sessionStickyCache    sessionserver.StickyCache
	adminStickyFlusher    admin.StickyFlusher
	erasureSticky         *experimentsticky.RedisCache
	callbackValidator     *sessioncallback.Validator
	callbackSeal          func(ctx context.Context, tenantID string, plaintext []byte) ([]byte, error)
	callbackDispatcher    *sessioncallback.Dispatcher
	budgetTerminator      *budgetSessionTerminator
	sessionBudgetEnforcer *sessionbudget.Enforcer
	leaseBudgets          *leasecontrol.MemoryBudgetSource
	leaseExtDefaults      sessionserver.LeaseExtensionDefaults
	sessionLeaseRegistrar sessionserver.LeaseTreeRegistrar
	childLeaseRegistrar   delegation.LeaseChildRegistrar
	activityStamper       *sessionidle.Stamper
	slotHealth            *slothealth.Tracker

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
	auditAppender   policy.AuditAppender
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
	impersonationSvc            *impersonation.Service
	inputWaits                  *inputwait.Registry
	kmsProbeLifecycle           *tenantkms.Lifecycle
	mtlsDenyProp                *mtlsdenylistprop.Propagator
	mux                         *http.ServeMux
	podBinder                   *podsession.Binder
	recommendationStore         *recommendations.WindowStore
	revCache                    *revocation.Cache
	revProp                     *revocationprop.Propagator
	sessions                    sessionstore.Store
	sessionSrv                  *sessionserver.Server
	storageCounter              storagequota.Counter
	storageRecoveryReconciler   *storagequota.RecoveryReconciler
	subsystemMetrics            *subsystem.Metrics
	tenants                     tenantstore.Store
	tokenServiceSubsystem       *subsystem.Subsystem
	transcripts                 transcriptstore.Store
	uploadRotator               *uploadtoken.Rotator
	uploadSubsystem             *subsystem.Subsystem
	uploadTracker               *uploadtoken.MemoryTracker
	wd                          *watchdog.Watchdog
	billingTier                 *redisstream.Tier
	connectorStateStore         *connectoroauth.MemoryStateStore
	deadlockManager             *deadlock.Manager
	dsMonitor                   *dualstore.Monitor
	pools                       poolstore.Store
	treeArchive                 treearchive.Store
	userPodTerminateProp        *podterminateprop.Propagator
	blobsCataloged              *cataloging.Store
	quotaBudgetTracker          *quotabudget.Tracker
	quotaCheckpointSvc          *quotacheckpoint.Service
	quotaFailOpenAccum          *quotafailopen.Accumulator
	sessionUsage                sessionusage.Store
	treeBudgetConcrete          *treebudget.Reserver
	artifactCatalog             artifactcatalog.Store
	blobs                       blobstore.Store
	credRenewalWorker           *credrenewal.Worker
	credentialPools             credentialpoolstore.Store
	evals                       evalstore.Store
	immediateGCSweep            func(ctx context.Context, tenantID string)
	partialManifests            partialmanifeststore.Store
	securityBus                 *pubsub.Bus
	memories                    memorystore.Store
	memoryBackendLabel          string

	// tokenServiceConn is the §4.3 token-service gRPC connection. buildStores
	// dials it; runGateway closes it on exit (the original deferred close is
	// relocated to the composition root so the connection lives for the
	// process lifetime rather than only for the build step).
	tokenServiceConn   *grpc.ClientConn
	auditSyncPool      *pgxpool.Pool
	bearerVerifier     jwt.Verifier
	billingAuditPool   *pgxpool.Pool
	billingLedger      billingstore.Store
	blobProbe          drainreadiness.Prober
	capOverrides       runtimecapoverride.Store
	connectors         connectorstore.Store
	coordFencer        sessionserver.CoordinationFencer
	coordMirror        coordlease.Store
	credAssign         credassign.Assigner
	credCache          *credcache.Cache
	erasureLeaseStore  leasestore.LeaseStore
	exec               executor.Executor
	expectedAuds       []string
	experiments        experimentstore.Store
	holdCoordinator    *recycle.HoldCoordinator
	hwmReader          sessionserver.DelegationHighWatermarkReader
	inProcessAssign    *credassign.Service
	interactions       interactionstore.Store
	jwtSigner          *jwt.BreakerSigner
	kmsBackedSigner    *jwt.KMSSigner
	kmsBreakerObs      *kmsBreakerObserver
	kmsProvider        kms.Provider
	kubeHealthzProbe   func(context.Context) error
	llmLeases          credleasestore.LeaseStore
	messagingCoord     *sessioninbox.Coordinator
	minioStore         *miniostore.Store
	objectStore        blobstore.Store
	podRegistry        *podsession.Registry
	rateLimiter        ratelimit.Counter
	readPool           *pgxpool.Pool
	recycleBoundary    *recycle.RecycleBoundaryCoordinator
	ring               *uploadtoken.KeyRing
	rotatingVerifier   *jwt.RotatingVerifier
	runtimes           runtimestore.Store
	saTokenVerifier    leasecontrol.TokenVerifier
	scatterRouter      *storerouter.SingleShardRouter
	secretProber       admin.SecretAccessProber
	sessionLogs        sessionlogstore.Store
	sessionSealer      sessionserver.Sealer
	storeRouter        storerouter.StoreRouter
	toolApprovalWaits  *toolapproval.Registry
	treeBudgetReserver delegation.TreeBudgetReserver
	uploadIssuer       *uploadtoken.Issuer
	uploadVerifier     *uploadtoken.Verifier
	users              userstore.Store
}
