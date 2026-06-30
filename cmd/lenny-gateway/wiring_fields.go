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
	"github.com/lennylabs/lenny/pkg/gateway/connectorstore"
	"github.com/lennylabs/lenny/pkg/gateway/coordlease"
	"github.com/lennylabs/lenny/pkg/gateway/credassign"
	"github.com/lennylabs/lenny/pkg/gateway/credcache"
	"github.com/lennylabs/lenny/pkg/gateway/credleasestore"
	"github.com/lennylabs/lenny/pkg/gateway/delegation"
	"github.com/lennylabs/lenny/pkg/gateway/drainreadiness"
	"github.com/lennylabs/lenny/pkg/gateway/executor"
	"github.com/lennylabs/lenny/pkg/gateway/experimentstore"
	"github.com/lennylabs/lenny/pkg/gateway/interactionstore"
	"github.com/lennylabs/lenny/pkg/gateway/leasecontrol"
	"github.com/lennylabs/lenny/pkg/gateway/leasestore"
	"github.com/lennylabs/lenny/pkg/gateway/ratelimit"
	"github.com/lennylabs/lenny/pkg/gateway/recycle"
	"github.com/lennylabs/lenny/pkg/gateway/runtimecapoverride"
	"github.com/lennylabs/lenny/pkg/gateway/runtimestore"
	"github.com/lennylabs/lenny/pkg/gateway/sessioninbox"
	"github.com/lennylabs/lenny/pkg/gateway/sessionlogstore"
	"github.com/lennylabs/lenny/pkg/gateway/toolapproval"
	"github.com/lennylabs/lenny/pkg/gateway/userstore"
	"github.com/lennylabs/lenny/pkg/kms"
	"github.com/lennylabs/lenny/pkg/storerouter"

	"github.com/lennylabs/lenny/pkg/blobstore"
	"github.com/lennylabs/lenny/pkg/blobstore/artifactcatalog"
	"github.com/lennylabs/lenny/pkg/blobstore/cataloging"
	"github.com/lennylabs/lenny/pkg/gateway/credentialpoolstore"
	"github.com/lennylabs/lenny/pkg/gateway/credrenewal"
	"github.com/lennylabs/lenny/pkg/gateway/evalstore"
	"github.com/lennylabs/lenny/pkg/gateway/memorystore"
	"github.com/lennylabs/lenny/pkg/gateway/partialmanifeststore"
	"github.com/lennylabs/lenny/pkg/gateway/pubsub"
	"github.com/lennylabs/lenny/pkg/gateway/quotabudget"
	"github.com/lennylabs/lenny/pkg/gateway/quotacheckpoint"
	"github.com/lennylabs/lenny/pkg/gateway/quotafailopen"
	"github.com/lennylabs/lenny/pkg/gateway/sessionusage"
	"github.com/lennylabs/lenny/pkg/gateway/treebudget"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"google.golang.org/grpc"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/lennylabs/lenny/pkg/alerting/evaluator"
	"github.com/lennylabs/lenny/pkg/audit/ocsf"
	"github.com/lennylabs/lenny/pkg/audit/siem"
	"github.com/lennylabs/lenny/pkg/connectoroauth"
	"github.com/lennylabs/lenny/pkg/driftmonitor"
	"github.com/lennylabs/lenny/pkg/gateway/admin"
	"github.com/lennylabs/lenny/pkg/gateway/auditretention"
	"github.com/lennylabs/lenny/pkg/gateway/auditstore/auditbatch"
	"github.com/lennylabs/lenny/pkg/gateway/billingfanout"
	"github.com/lennylabs/lenny/pkg/gateway/billingstore"
	"github.com/lennylabs/lenny/pkg/gateway/billingstore/failover"
	"github.com/lennylabs/lenny/pkg/gateway/billingstore/failover/redisstream"
	"github.com/lennylabs/lenny/pkg/gateway/breakerstore/cachingstore"
	"github.com/lennylabs/lenny/pkg/gateway/checkpointer"
	"github.com/lennylabs/lenny/pkg/gateway/coordination"
	credrenewalprop "github.com/lennylabs/lenny/pkg/gateway/credrenewal/propagator"
	"github.com/lennylabs/lenny/pkg/gateway/deadlock"
	"github.com/lennylabs/lenny/pkg/gateway/delegationbudget"
	"github.com/lennylabs/lenny/pkg/gateway/denylist"
	"github.com/lennylabs/lenny/pkg/gateway/dualstore"
	"github.com/lennylabs/lenny/pkg/gateway/elicitationfloor"
	"github.com/lennylabs/lenny/pkg/gateway/eventbus"
	"github.com/lennylabs/lenny/pkg/gateway/events"
	"github.com/lennylabs/lenny/pkg/gateway/experimentprovider"
	"github.com/lennylabs/lenny/pkg/gateway/failopen"
	"github.com/lennylabs/lenny/pkg/gateway/gatewaymetrics"
	"github.com/lennylabs/lenny/pkg/gateway/impersonation"
	"github.com/lennylabs/lenny/pkg/gateway/inputwait"
	"github.com/lennylabs/lenny/pkg/gateway/podsession"
	podterminateprop "github.com/lennylabs/lenny/pkg/gateway/podterminate/propagator"
	"github.com/lennylabs/lenny/pkg/gateway/policy"
	"github.com/lennylabs/lenny/pkg/gateway/poolstore"
	"github.com/lennylabs/lenny/pkg/gateway/recommendations"
	"github.com/lennylabs/lenny/pkg/gateway/redistopology"
	"github.com/lennylabs/lenny/pkg/gateway/revocation"
	revocationprop "github.com/lennylabs/lenny/pkg/gateway/revocation/propagator"
	"github.com/lennylabs/lenny/pkg/gateway/sessionevents"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/sqlitestore"
	"github.com/lennylabs/lenny/pkg/gateway/storagequota"
	"github.com/lennylabs/lenny/pkg/gateway/subsystem"
	"github.com/lennylabs/lenny/pkg/gateway/tenantstore"
	"github.com/lennylabs/lenny/pkg/gateway/transcriptstore"
	"github.com/lennylabs/lenny/pkg/gateway/treearchive"
	"github.com/lennylabs/lenny/pkg/gateway/watchdog"
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
	alertEvalPtr        *atomic.Pointer[evaluator.Evaluator]
	watchdogCtx         context.Context

	// Audit background loops the run loop starts after installing the
	// signal handler (§11.7 item 2, §11.7 Wire Format, §12.3).
	resolvedGrantCheckInterval time.Duration
	grantCheckRegulated        bool
	ocsfTranslator             *ocsf.Translator
	ocsfOutbox                 *siem.Outbox
	auditBatchBuffer           *auditbatch.Buffer

	// Constructed subsystems and stores the §4.1 background-worker step
	// (startBackgroundWorkers) reads to launch the periodic sweepers,
	// samplers, reconcilers, and propagator subscribers.
	adminRouter                 *admin.Router
	auditAppender               policy.AuditAppender
	auditPruner                 *auditretention.Pruner
	billing                     billingstore.Store
	billingEmitter              *billingfanout.Emitter
	billingPipeline             *failover.Pipeline
	breakerCache                *cachingstore.Store
	breakers                    breakerRegistry
	checkpointSvc               *checkpointer.Checkpointer
	clusterClient               client.Client
	coordinator                 *coordination.Sweeper
	credDeny                    *denylist.DenyList
	credRenewalProp             *credrenewalprop.Propagator
	deadlockTracker             *deadlock.AwaitTracker
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
