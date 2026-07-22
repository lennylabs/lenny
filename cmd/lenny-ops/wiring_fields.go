// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"net/http"
	"sync"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	apiextensionsclientset "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"

	"github.com/lennylabs/lenny/pkg/audit/pgaudit"
	"github.com/lennylabs/lenny/pkg/events"
	"github.com/lennylabs/lenny/pkg/ops/backup"
	"github.com/lennylabs/lenny/pkg/ops/configservice"
	"github.com/lennylabs/lenny/pkg/ops/coordination"
	"github.com/lennylabs/lenny/pkg/ops/diagnostics"
	"github.com/lennylabs/lenny/pkg/ops/doctor"
	"github.com/lennylabs/lenny/pkg/ops/driftservice"
	"github.com/lennylabs/lenny/pkg/ops/escalation"
	opsstream "github.com/lennylabs/lenny/pkg/ops/events"
	"github.com/lennylabs/lenny/pkg/ops/eventsubscription"
	"github.com/lennylabs/lenny/pkg/ops/gateway"
	"github.com/lennylabs/lenny/pkg/ops/operations"
	"github.com/lennylabs/lenny/pkg/ops/opsaudit"
	"github.com/lennylabs/lenny/pkg/ops/opsidem"
	"github.com/lennylabs/lenny/pkg/ops/opsinventory"
	"github.com/lennylabs/lenny/pkg/ops/opsserver"
	"github.com/lennylabs/lenny/pkg/ops/opsservice"
	"github.com/lennylabs/lenny/pkg/ops/probe"
	"github.com/lennylabs/lenny/pkg/ops/registryservice"
	"github.com/lennylabs/lenny/pkg/ops/upgradeservice"
	"github.com/lennylabs/lenny/pkg/releasechannel"
	"github.com/lennylabs/lenny/pkg/storerouter"
)

// opsWiringFields holds the §25 subsystem components the lenny-ops
// composition root threads between build steps. A field is set by the step
// that constructs the component and read by the steps that wire it,
// mirroring the gatewayWiring accumulator the gateway composition root uses
// (proposal 0020 §4 Part A R1 / R4). The field set is populated from the
// cross-step locals the former monolithic main threaded inline.
//
// spec: §25.4 — the lenny-ops service body is the HTTP surface plus the
// leader-elected background loops, each built from these threaded
// subsystems; §4.1 — the composition root threads its inputs and
// constructed subsystems through the per-subsystem builders in dependency
// order.
type opsWiringFields struct {
	// Process-wide identity and lifecycle.
	replicaID     string
	ctx           context.Context
	stop          context.CancelFunc
	traceShutdown func(context.Context) error

	// §25.4 dependencies.
	pgPool      *pgxpool.Pool
	redisClient redis.UniversalClient
	// opsStreamRedis is the §25.5 event-stream read source's view of the
	// platform Redis: the shared client for the range reads plus a dedicated
	// client per live SSE tail. Nil when no Redis is wired. spec: §25.5.
	opsStreamRedis opsstream.RedisStreamClient
	clientset      *kubernetes.Clientset
	dynClient      dynamic.Interface
	apiextClient   apiextensionsclientset.Interface
	gatewayHTTP    *http.Client
	probes         map[string]probe.Func
	storeRouter    *storerouter.SingleShardRouter
	auditRecorder  *opsaudit.Recorder
	runbookSource  opsserver.RunbookSource
	elector        opsservice.Elector

	// §25.5 event stream + webhook delivery.
	eventStream *opsstream.Service
	srcHealth   *sourceHealthProbe
	// flushRequests carries §25.5 recovery-flush requests from the two edge
	// detectors (the source-health probe and the write path's first successful
	// XADD after a failed one) to the flush worker, so neither detector runs
	// the flush on its own goroutine. spec: §25.5 (best-effort recovery flush).
	flushRequests      chan struct{}
	opsEmitter         events.EventEmitter
	eventSource        opsservice.EventSource
	delivery           webhookDelivery
	webhook            *opsservice.WebhookWorker
	eventSubscriptions *eventsubscription.Service
	subscriptionCache  *opsservice.SubscriptionCache
	subsUnavailMu      sync.Mutex
	subsUnavailEmitted bool

	// §25.4 gateway admin-API client.
	gwClient *gateway.Client

	// §25.4 self-health, lock, and clock-skew.
	selfChecks       map[string]opsservice.SelfCheck
	replicaCounter   coordination.ReplicaCounter
	lockSvc          *coordination.Service
	lockCoordination *coordination.CoordinationGate
	lockMetrics      *lockMetrics
	clockSkewSampler *coordination.ClockSkewSampler

	// §25.11 backup, §25.4 escalation, §25.10 drift, §25.6 diagnostics/doctor.
	backupSvc     *backup.Service
	backupJobs    []opsservice.ScheduledJob
	escalationSvc *escalation.Service
	driftSvc      *driftservice.Service
	diagnosticSvc diagnostics.DiagnosticService
	doctorSvc     doctor.Service

	// §25.8 release channel, upgrade, registry, version.
	releaseChannelPub  *releasechannel.Publisher
	baselineStore      operations.BaselineStore
	upgradeStore       upgradeservice.Store
	upgradeSvc         *upgradeservice.Service
	upgradeChecker     *upgradeservice.Checker
	upgradePreflighter *upgradeservice.Preflighter
	upgradeWatchdog    *upgradeservice.Watchdog
	versionAggregator  *upgradeservice.VersionAggregator
	platformConfigSvc  *configservice.Service
	registrySvc        *registryservice.Service
	cronJobs           []opsservice.ScheduledJob

	// §25.4 operations inventory, idempotency, bundle-rules.
	inventory            *operations.Inventory
	operationsObserver   *opsinventory.Observer
	idemStore            opsidem.Store
	bundleRulesReconcile opsservice.Reconciler

	// §25.4 service body + HTTP surface.
	svc            *opsservice.Service
	opsHandler     *opsserver.Server
	srv            *http.Server
	pgauditShipper *pgaudit.Shipper
}
