// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"crypto/ed25519"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/redis/go-redis/v9"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"

	"github.com/lennylabs/lenny/pkg/alerting/alertingmetrics"
	"github.com/lennylabs/lenny/pkg/events"
	"github.com/lennylabs/lenny/pkg/gateway/eventbuffer"
	obsaudit "github.com/lennylabs/lenny/pkg/observability/audit"
	"github.com/lennylabs/lenny/pkg/observability/metrics"
	"github.com/lennylabs/lenny/pkg/ops/auditrate"
	"github.com/lennylabs/lenny/pkg/ops/backup"
	backupk8slauncher "github.com/lennylabs/lenny/pkg/ops/backup/k8slauncher"
	backuppgstore "github.com/lennylabs/lenny/pkg/ops/backup/pgstore"
	"github.com/lennylabs/lenny/pkg/ops/backup/restoretest"
	"github.com/lennylabs/lenny/pkg/ops/baselinestore"
	"github.com/lennylabs/lenny/pkg/ops/configservice"
	"github.com/lennylabs/lenny/pkg/ops/coordination"
	"github.com/lennylabs/lenny/pkg/ops/diagnostics"
	"github.com/lennylabs/lenny/pkg/ops/diagnostics/prodsource"
	"github.com/lennylabs/lenny/pkg/ops/doctor"
	"github.com/lennylabs/lenny/pkg/ops/driftservice"
	"github.com/lennylabs/lenny/pkg/ops/driftservice/gatewayreader"
	driftpgstore "github.com/lennylabs/lenny/pkg/ops/driftservice/pgstore"
	"github.com/lennylabs/lenny/pkg/ops/escalation"
	escpgstore "github.com/lennylabs/lenny/pkg/ops/escalation/pgstore"
	escredisstore "github.com/lennylabs/lenny/pkg/ops/escalation/redisstore"
	opsstream "github.com/lennylabs/lenny/pkg/ops/events"
	"github.com/lennylabs/lenny/pkg/ops/eventsubscription"
	eventsubpgstore "github.com/lennylabs/lenny/pkg/ops/eventsubscription/pgstore"
	"github.com/lennylabs/lenny/pkg/ops/gateway"
	"github.com/lennylabs/lenny/pkg/ops/operations"
	"github.com/lennylabs/lenny/pkg/ops/opsaudit"
	"github.com/lennylabs/lenny/pkg/ops/opsidem"
	idempgstore "github.com/lennylabs/lenny/pkg/ops/opsidem/pgstore"
	"github.com/lennylabs/lenny/pkg/ops/opsserver"
	"github.com/lennylabs/lenny/pkg/ops/opsservice"
	"github.com/lennylabs/lenny/pkg/ops/probe"
	"github.com/lennylabs/lenny/pkg/ops/registryservice"
	"github.com/lennylabs/lenny/pkg/ops/upgradeservice"
	upgradepg "github.com/lennylabs/lenny/pkg/ops/upgradeservice/pgstore"
	"github.com/lennylabs/lenny/pkg/releasechannel"
	"github.com/lennylabs/lenny/pkg/webhookdelivery"
)

// redisFanOutEmitter wires lenny-ops's §4.0 EventEmitter dependency to
// both the §25.5 Redis stream and the in-process opsstream.Service buffer.
// Every Emit lands on the platform-scoped ops:events:stream so other
// replicas (gateway, controller, peer lenny-ops) and downstream
// watchdogs see the same event; the local opsstream.Service captures a
// copy so the SSE/polling endpoints continue to serve this replica's
// emissions even when the consumer end of the stream has not been
// wired yet (§25.5 cold-start guarantee).
type redisFanOutEmitter struct {
	stream *eventbuffer.StreamEmitter
	local  *opsstream.Service
}

// newRedisFanOutEmitter constructs an emitter that writes every event
// to client and tees a copy through local.Publish so the local SSE
// subscribers and polling cursor see it without depending on a
// separate Redis consumer loop. streamMaxLen is the §25.5
// ops.events.streamMaxLen cap on the XADD MAXLEN-approximated stream; a
// non-positive value uses the StreamEmitter Tier 1 default.
func newRedisFanOutEmitter(client redis.UniversalClient, local *opsstream.Service, replicaID string, streamMaxLen int64) *redisFanOutEmitter {
	stream := eventbuffer.NewStreamEmitter(eventbuffer.StreamEmitterOptions{
		Client: client,
		// The local buffer the StreamEmitter writes through is the same
		// data structure the opsstream.Service maintains; using a separate
		// buffer would split the read view. The opsstream.Service Publish
		// path covers the local-buffer write, so the stream emitter
		// here only needs a private buffer to satisfy its non-nil
		// requirement — the Publish below is the canonical local
		// delivery.
		Buffer: eventbuffer.NewEventBuffer(0),
		// spec: §25.5 — ops.events.streamMaxLen sizes the platform-scoped
		// ops:events:stream so a Tier 3 install holds the larger catch-up
		// window the spec mandates.
		MaxLen:    streamMaxLen,
		Source:    "//lenny.dev/ops/" + replicaID,
		ReplicaID: replicaID,
	})
	return &redisFanOutEmitter{stream: stream, local: local}
}

// Emit publishes to the local opsstream.Service first so SSE subscribers
// and the polling cursor see the event immediately, then XADDs to the
// §25.5 Redis stream. A Redis write failure is surfaced as the
// returned error; the local publish has already succeeded so the §25.5
// "fall back to gateway buffer" path is preserved.
func (e *redisFanOutEmitter) Emit(ctx context.Context, event events.OperationalEvent) error {
	if _, err := e.local.Publish(ctx, event); err != nil {
		return err
	}
	return e.stream.Emit(ctx, event)
}

// Compile-time guard that *redisFanOutEmitter satisfies the §4.0
// EventEmitter contract.
var _ events.EventEmitter = (*redisFanOutEmitter)(nil)

// noopElector is the §25.4 Elector used when lenny-ops has no
// Kubernetes connection. It never grants leadership, so the
// leader-only background loops stay idle and the replica serves only
// the read-only HTTP surface. This keeps single-process local
// development working without a cluster.
type noopElector struct{}

// Run blocks until ctx is cancelled without ever invoking the
// leader-acquired callback.
func (noopElector) Run(ctx context.Context, _ func(context.Context), _ func()) {
	<-ctx.Done()
}

// IsLeader always reports false: a replica with no cluster connection
// never holds the lenny-ops-leader Lease.
func (noopElector) IsLeader() bool { return false }

// emptyEventSource is the §25.5 EventSource used until the Redis
// ops:events:stream consumer is wired. It yields no events, so the
// webhook delivery worker runs its loop and delivers nothing — the
// correct behavior before an event source exists.
type emptyEventSource struct{}

// Poll returns no events.
func (emptyEventSource) Poll(context.Context) ([]opsservice.WebhookEvent, error) {
	return nil, nil
}

// emptySubscriptionSource is the §25.5 SubscriptionSource used until
// the ops_event_subscriptions cache is wired. It yields no
// subscriptions; §25.5 cold-start behavior is that no webhook delivery
// occurs until the cache is populated.
type emptySubscriptionSource struct{}

// Subscriptions returns no subscriptions.
func (emptySubscriptionSource) Subscriptions() []opsservice.WebhookSubscription {
	return nil
}

// subscriptionAuditFunc adapts a plain function to the
// eventsubscription.AuditSink interface so the §25.5 subscription audit
// events (created/updated/deleted/secret_rotated) reach the lenny-ops
// audit logger. spec: §25.5 lines 2731, 2804-2806.
type subscriptionAuditFunc func(eventsubscription.AuditEvent)

func (f subscriptionAuditFunc) Emit(ev eventsubscription.AuditEvent) { f(ev) }

// webhookDeliveryDeps carries the backing seams buildWebhookDelivery
// selects over its in-memory fallbacks.
type webhookDeliveryDeps struct {
	// Pool, when non-nil, selects the Postgres-backed
	// ops_event_subscriptions / ops_event_deliveries store over the
	// in-memory store so subscriptions and delivery history survive a
	// restart and coordinate across replicas.
	Pool *pgxpool.Pool
	// Clientset, when non-nil, plus a non-empty SharedKey enables the
	// §25.5 subscription_cache_invalidate peer RPC: the broadcaster reads
	// peer pod IPs from the lenny-ops Service Endpoints.
	Clientset kubernetes.Interface
	// Redis is the §25.5 event source consumer (nil disables delivery).
	Source opsservice.EventSource
	// Emitter is where event_delivery_failed is published. Required.
	Emitter events.EventEmitter
	// Audit receives the §25.5 subscription audit events. Optional.
	Audit eventsubscription.AuditSink
	// SharedKey derives the invalidate RPC token; an empty key disables
	// the cross-replica RPC (the cache degrades to periodic refresh).
	SharedKey            []byte
	Namespace            string
	ServiceName          string
	SelfIP               string
	AdminPort            int
	TrackingMode         webhookdelivery.TrackingMode
	Retention            opsservice.RetentionPolicy
	OnAvailabilityChange func(available bool)
	// SSRF is the §25.5 callback-URL validator built from the
	// ops.webhooks.{allowHTTP,blockedCIDRs,domainAllowlist} policy. When
	// non-nil it gates subscription create/update (via the Service) and
	// every delivery attempt (via the transport guard). Nil applies the
	// strict default (HTTPS-only, no allowlist). F-25.4.9.
	SSRF *eventsubscription.SSRFValidator
}

// webhookDelivery bundles the wired §25.5 webhook delivery components the
// lenny-ops binary consumes: the leader-only worker, the CRUD service the
// opsserver routes against, the delivery cache the invalidate RPC
// refreshes, and the shared-secret-derived invalidate token.
type webhookDelivery struct {
	Worker          *opsservice.WebhookWorker
	Subscriptions   *eventsubscription.Service
	Cache           *opsservice.SubscriptionCache
	Store           eventsubscription.Store
	InvalidateToken string
}

// buildWebhookDelivery wires the §25.5 webhook delivery subsystem
// (F-25.5.11, F-25.5.13): a durable subscription store, the in-memory
// reveal-secret cache that lets the worker sign deliveries, the
// delivery-recording + failure-emission worker, the delivery metrics,
// and the cross-replica subscription_cache_invalidate RPC. The leader
// runs the returned Worker; the opsserver registers the returned
// Subscriptions service and Cache invalidator. spec: §25.5 lines
// 2701-2756.
func buildWebhookDelivery(ctx context.Context, deps webhookDeliveryDeps) webhookDelivery {
	var store eventsubscription.Store
	if deps.Pool != nil {
		store = eventsubpgstore.New(deps.Pool)
	} else {
		store = eventsubscription.NewMemoryStore()
	}
	svc := eventsubscription.NewService(store)
	if deps.Audit != nil {
		svc.SetAuditSink(deps.Audit)
	}
	// §25.5 lines 2735-2745: the ops.webhooks SSRF policy validates the
	// callback URL at create/update. A nil validator leaves the Service on
	// its strict default. F-25.4.9.
	if deps.SSRF != nil {
		svc.SSRF = deps.SSRF
	}

	// §25.5 lines 2715-2733: the worker recovers each subscription's
	// plaintext signing secret from this in-memory reveal cache, populated
	// when the secret is generated and pruned when the subscription is
	// deleted.
	secretCache := opsservice.NewSecretCache()
	svc.OnSecret = secretCache.Put
	svc.OnRemove = secretCache.Remove

	cache := opsservice.NewSubscriptionCache(ctx, opsservice.SubscriptionCacheConfig{
		Store:                store,
		Secrets:              secretCache,
		OnAvailabilityChange: deps.OnAvailabilityChange,
	})

	// §25.5 line 2751: the cross-replica subscription_cache_invalidate RPC.
	// The token derives from the shared HMAC key both replicas mount; an
	// empty key (dev) leaves the broadcaster nil and the cache on
	// periodic-refresh-only.
	token := opsservice.InvalidateToken(deps.SharedKey)
	var broadcaster *opsservice.CacheInvalidateBroadcaster
	if token != "" && deps.Clientset != nil {
		broadcaster = opsservice.NewCacheInvalidateBroadcaster(opsservice.CacheInvalidateBroadcasterConfig{
			Peers: opsservice.NewEndpointsPeerLister(opsservice.EndpointsPeerListerConfig{
				Endpoints: deps.Clientset.CoreV1(),
				Namespace: deps.Namespace,
				Service:   deps.ServiceName,
				Port:      deps.AdminPort,
				SelfIP:    deps.SelfIP,
			}),
			Token: token,
		})
	}
	// §25.5 lines 2751, 2756: on every CRUD, refresh the local cache
	// synchronously, then fan the invalidate RPC out to peers off the
	// request path so one slow peer never blocks the response.
	svc.OnChange = func(reqCtx context.Context, _ string) {
		_ = cache.Invalidate(reqCtx)
		if broadcaster != nil {
			go func() {
				bctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				broadcaster.Broadcast(bctx)
			}()
		}
	}

	// §25.5 lines 2701-2713: record each delivery outcome unless the
	// deployment opted into metric-only tracking.
	var recorder opsservice.DeliveryRecorder
	if deps.TrackingMode != webhookdelivery.TrackingMetricOnly {
		recorder = opsservice.NewStoreDeliveryRecorder(store, deps.Retention, nil)
	}

	workerCfg := opsservice.WebhookWorkerConfig{
		Events:        deps.Source,
		Subscriptions: cache,
		Recorder:      recorder,
		TrackingMode:  deps.TrackingMode,
		HTTPTimeout:   10 * time.Second,
		Metrics:       deliveryMetricsObserver{},
		EmitFailure: func(subID, eventID string) {
			// §25.5 line 2713: emit event_delivery_failed but do not deliver
			// it to the subscription, to avoid loops.
			payload, _ := json.Marshal(map[string]any{
				"subscriptionId": subID,
				"eventId":        eventID,
			})
			if err := deps.Emitter.Emit(ctx, events.OperationalEvent{
				Type:            events.EventEventDeliveryFailed.CloudEventsType(),
				Subject:         "event_subscription/" + subID,
				Severity:        "warning",
				DataContentType: "application/json",
				Data:            payload,
			}); err != nil {
				log.Printf("lenny-ops: emit event_delivery_failed: %v", err)
			}
		},
	}
	// §25.5 lines 2735-2745: re-run the SSRF/DNS-rebinding guard on every
	// delivery attempt (not only at subscription create), so a DNS rebind
	// after registration cannot reach a blocked target. Set the transport
	// only when a validator is configured so the nil case still falls back
	// to the worker's default HTTP transport. F-25.4.9.
	if deps.SSRF != nil {
		workerCfg.Transport = webhookdelivery.NewTransport(10 * time.Second).WithSSRFGuard(deps.SSRF.Validate)
	}
	worker := opsservice.NewWebhookWorker(workerCfg)

	return webhookDelivery{Worker: worker, Subscriptions: svc, Cache: cache, Store: store, InvalidateToken: token}
}

// deliveryRetentionJob is the §25.5 lines 2649-2664 leader-only cron that
// purges expired ops_event_deliveries rows daily at 03:45 UTC. expires_at
// is stamped at record time from the retention policy, so the sweep just
// deletes rows whose expires_at has passed, batched at LIMIT 10000 to
// avoid a long lock. spec: §25.5 line 2661.
func deliveryRetentionJob(store eventsubscription.Store) opsservice.ScheduledJob {
	return opsservice.ScheduledJob{
		Name:       "delivery-retention",
		Expression: "45 3 * * *",
		Run: func(ctx context.Context) error {
			const batch = 10000
			total := 0
			for {
				n, err := store.DeleteExpired(ctx, time.Now().UTC(), batch)
				if err != nil {
					return err
				}
				total += n
				if n < batch {
					break
				}
			}
			if total > 0 {
				log.Printf("lenny-ops: §25.5 delivery-retention purged %d expired delivery rows", total)
			}
			return nil
		},
	}
}

// selfHealthEventSeverity maps a §25.4 self-health status text to the
// §25.3 CloudEvents severity extension attribute carried on the
// ops_health_status_changed event. A degraded replica is a warning; an
// unhealthy replica is critical; recovery to healthy is informational.
func selfHealthEventSeverity(statusText string) string {
	switch statusText {
	case "unhealthy":
		return "critical"
	case "degraded":
		return "warning"
	default:
		return "info"
	}
}

// backupDeps carries the production backing seams buildBackupService
// selects over its in-memory fallbacks. Each field is optional: a nil or
// zero field keeps the corresponding fake so the §25.11 endpoints still
// serve in a single-process degraded mode.
type backupDeps struct {
	// Pool, when non-nil, selects the Postgres-backed ops_backups store
	// over backup.MemStore so backups, schedule edits, restores, and the
	// reconciler survive a lenny-ops restart and coordinate across replicas.
	Pool *pgxpool.Pool
	// Clientset, when non-nil, plus a non-empty LauncherImage selects the
	// Kubernetes JobLauncher over backup.FakeLauncher so backup/restore Jobs
	// actually run.
	Clientset kubernetes.Interface
	// Locks, when non-nil, selects the §25.4-remediation-lock-backed
	// RestoreLocker over backup.MemLocker so the restore:platform lock
	// coordinates across replicas.
	Locks coordination.RemediationLockService

	// Recorder is the durable §11.7 platform-audit recorder every
	// backup/restore/retention transition is committed to (§25.11 line
	// 4343). Never nil — main builds the degraded log-only recorder when
	// no Postgres is wired.
	Recorder *opsaudit.Recorder

	Namespace       string
	LauncherImage   string
	MinIOEndpoint   string
	MinIOBucket     string
	KMSKeyID        string
	ReportDSNSecret string

	// IncludeSensitiveTables, ExcludeTables, and RedactColumns are the
	// §25.11 backups.contentPolicy the Job factory forwards to every
	// scheduled backup Job.
	IncludeSensitiveTables bool
	ExcludeTables          []string
	RedactColumns          []string

	// Regions is the §12.8 backups.regions.<region> per-region backup
	// endpoint map. When non-empty, buildBackupService enables per-region
	// dispatch and pairs it with ShardRegions; an empty map keeps the
	// single-region global dump path. spec: §12.8 lines 932-936.
	Regions map[string]backup.RegionBackupConfig
	// ShardRegions resolves each Postgres shard to its data-residency
	// region for per-region dispatch. Required when Regions is non-empty.
	ShardRegions backup.ShardRegionResolver
}

// buildBackupService constructs the §25.11 BackupService and the
// scheduled-backup cron jobs the §25.4 cron evaluator runs.
//
// It selects the Postgres-backed ops_backups store, the Kubernetes Job
// launcher, and the §25.4-remediation-lock-backed RestoreLocker from deps
// when those seams are wired; otherwise it falls back to the in-memory
// store, the fake launcher, and the in-memory locker so the §25.11
// endpoints still serve in a single-process degraded mode. The
// scheduled-backup loop runs only on the leader replica.
func buildBackupService(production bool, deps backupDeps) (*backup.Service, []opsservice.ScheduledJob) {
	cfg := backup.Config{
		// §25.11 line 4343: every backup/restore/retention transition is
		// audited. The orchestrator emits to this sink, which commits the
		// durable §11.7 platform-audit row through deps.Recorder (logged
		// only in the degraded no-Postgres mode).
		Audit: backupAuditSink(deps.Recorder),
		// §12.8 line 936: the lenny_data_residency_violation_total counter
		// the fail-closed BACKUP_REGION_UNRESOLVABLE abort increments.
		Residency: backupResidencyMetrics{},
		// §25.11 line 4320: the lenny_backup_reconcile_blocked_total counter
		// the post-restore erasure-reconcile ledger-stale block increments.
		Reconcile: backupReconcileMetrics{},
	}

	// §12.8 lines 932-936: when per-region backup endpoints are configured,
	// enable per-region pg_dump dispatch with the shard→region resolver.
	if len(deps.Regions) > 0 && deps.ShardRegions != nil {
		cfg.Regions = deps.Regions
		cfg.ShardRegions = deps.ShardRegions
		log.Printf("lenny-ops: §12.8 per-region backup dispatch enabled for %d region(s)", len(deps.Regions))
	}

	// §25.11 / F-25.11.3: the durable ops_backups store when Postgres is
	// wired, else the in-memory store.
	if deps.Pool != nil {
		cfg.Store = backuppgstore.New(deps.Pool)
		log.Printf("lenny-ops: §25.11 backup store: Postgres (ops_backups)")
	} else {
		cfg.Store = backup.NewMemStore()
	}

	// §25.11 / F-25.11.4: the Kubernetes Job launcher when a cluster
	// connection and a lenny-backup image are configured, else the fake
	// launcher.
	realLauncher := false
	if deps.Clientset != nil && deps.LauncherImage != "" {
		launcher, err := backupk8slauncher.New(backupk8slauncher.Config{
			Clientset:       deps.Clientset,
			Namespace:       deps.Namespace,
			Image:           deps.LauncherImage,
			MinIOEndpoint:   deps.MinIOEndpoint,
			MinIOBucket:     deps.MinIOBucket,
			KMSKeyID:        deps.KMSKeyID,
			ReportDSNSecret: deps.ReportDSNSecret,
			ContentPolicy: backupk8slauncher.ContentPolicy{
				IncludeSensitiveTables: deps.IncludeSensitiveTables,
				ExcludeTables:          deps.ExcludeTables,
				RedactColumns:          deps.RedactColumns,
			},
		})
		if err != nil {
			log.Fatalf("lenny-ops: build backup Job launcher: %v", err)
		}
		cfg.Launcher = launcher
		realLauncher = true
		log.Printf("lenny-ops: §25.11 backup launcher: Kubernetes Jobs (image %s)", deps.LauncherImage)
	} else {
		cfg.Launcher = backup.NewFakeLauncher()
	}

	// §25.11 / F-17.3.4: the §25.4-remediation-lock-backed restore:platform
	// lock when the lock service is wired, else the in-memory locker.
	if deps.Locks != nil {
		cfg.Locker = newRestoreLocker(deps.Locks)
	} else {
		cfg.Locker = backup.NewMemLocker()
	}

	svc, err := backup.NewService(cfg)
	if err != nil {
		// NewService fails only on a missing dependency, all supplied
		// here; a failure is a programming error.
		log.Fatalf("lenny-ops: build backup service: %v", err)
	}

	// The §25.11 scheduled-backup cron jobs: a full backup daily at
	// 02:00 UTC, a Postgres backup every 6 hours, and the retention-
	// enforcement sweep daily at 03:30 UTC. The cron evaluator fires
	// these on the leader replica; each creates a Kubernetes Job through
	// the BackupService.
	//
	// spec: §25.11 schedule.enabled — the backup-creating jobs re-read
	// the schedule before each fire and skip when enabled:false so an
	// operator who turns scheduling off via PUT /v1/admin/backups/schedule
	// no longer waits for a lenny-ops restart for the change to take
	// effect. The retention sweep runs unconditionally (it is a
	// retention-policy job, not a backup-creation job).
	jobs := []opsservice.ScheduledJob{
		{
			Name:       "backup-full",
			Expression: "0 2 * * *",
			// §25.11 line 4106: the firing cadence follows the stored
			// schedule's `full` cron, so an operator who edits it via PUT
			// /v1/admin/backups/schedule changes the schedule without a
			// restart. F-25.11.5.
			ExpressionFunc: func() string { return scheduledBackupCron(svc, backup.TypeFull) },
			Run: func(ctx context.Context) error {
				if skip, err := scheduledBackupsDisabled(ctx, svc); err != nil {
					return err
				} else if skip {
					log.Printf("lenny-ops: backup-full skipped: schedule.enabled=false")
					return nil
				}
				_, err := svc.CreateBackup(ctx, backup.BackupRequest{
					Type: string(backup.TypeFull), Confirm: true, Production: production,
				})
				return err
			},
		},
		{
			Name:           "backup-postgres",
			Expression:     "0 */6 * * *",
			ExpressionFunc: func() string { return scheduledBackupCron(svc, backup.TypePostgres) },
			Run: func(ctx context.Context) error {
				if skip, err := scheduledBackupsDisabled(ctx, svc); err != nil {
					return err
				} else if skip {
					log.Printf("lenny-ops: backup-postgres skipped: schedule.enabled=false")
					return nil
				}
				_, err := svc.CreateBackup(ctx, backup.BackupRequest{
					Type: string(backup.TypePostgres),
				})
				return err
			},
		},
		{
			// spec: §25.11 lines 4108-4111 — the daily 03:30 UTC retention
			// sweep deletes expired backups from both MinIO and Postgres. When
			// a real Kubernetes launcher is wired, lenny-ops orchestrates a
			// lenny-backup --mode=retention Job (the Job mounts the MinIO
			// credentials and performs the object deletes lenny-ops itself
			// cannot); without one it runs the in-process planner, which marks
			// rows expired in the store and deletes objects via the
			// ObjectDeleter seam when configured. F-17.3.9.
			Name:       "backup-retention",
			Expression: "30 3 * * *",
			Run: func(ctx context.Context) error {
				if realLauncher {
					jobID, err := svc.LaunchRetentionJob(ctx)
					if err == nil {
						log.Printf("lenny-ops: retention enforcement Job %s launched", jobID)
					}
					return err
				}
				pruned, err := svc.EnforceRetention(ctx)
				if err == nil && len(pruned) > 0 {
					log.Printf("lenny-ops: retention enforcement pruned %d backups", len(pruned))
				}
				return err
			},
		},
		{
			// spec: §25.11 lines 3976-3978 — the reconciler runs every 60s,
			// failing ops_backups rows still pending after 2 minutes
			// (JOB_CREATE_FAILED) and deleting orphaned Kubernetes Jobs. It
			// is leader-gated like the other cron jobs.
			Name:       "backup-reconcile",
			Expression: "* * * * *",
			Run: func(ctx context.Context) error {
				failed, err := svc.ReconcilePending(ctx)
				if err != nil {
					return err
				}
				if len(failed) > 0 {
					log.Printf("lenny-ops: reconciler failed %d stale pending backups", len(failed))
				}
				deleted, err := svc.ReconcileOrphanedJobs(ctx)
				if err != nil {
					return err
				}
				if len(deleted) > 0 {
					log.Printf("lenny-ops: reconciler deleted %d orphaned backup Jobs", len(deleted))
				}
				return nil
			},
		},
		{
			// spec: §25.11 lines 4146-4149 — the restore-completion driver
			// polls every running restore's Job to completion and runs steps
			// 5-8 (per-shard events, the post-restore GDPR erasure reconciler,
			// the gateway rolling restart, and the restore:platform lock
			// release). Leader-gated like the other cron jobs. In this
			// single-process mode the Erasure and GatewayRestart seams are
			// nil, so a restore that completes its shards rolls without an
			// erasure replay and releases its lock immediately; a production
			// lenny-ops supplies the §12.8 reconciler Job adapter and the
			// gateway-Deployment patcher. F-17.3.5 / F-25.11.10.
			Name:       "restore-complete",
			Expression: "* * * * *",
			Run: func(ctx context.Context) error {
				advanced, err := svc.ReconcileRunningRestores(ctx)
				if err != nil {
					return err
				}
				if len(advanced) > 0 {
					log.Printf("lenny-ops: restore-completion reconciler advanced %d restores", len(advanced))
				}
				return nil
			},
		},
		{
			// spec: §25.11 line 4309 — publish the
			// lenny_backup_last_successful_timestamp{type} gauge so the
			// BackupOverdue alert (line 4317) has a source. Leader-gated
			// like the other cron jobs; the §16.9 /metrics exposition that
			// scrapes the gauge is wired (F-16.8.1).
			Name:       "backup-metrics",
			Expression: "* * * * *",
			Run: func(ctx context.Context) error {
				return sampleBackupMetrics(ctx, svc)
			},
		},
		{
			// spec: §25.11 lines 4098, 4254-4256 — publish the restore-test
			// gauges (lenny_restore_test_success / _duration_seconds /
			// _artifact_success_rate) and the cumulative
			// _artifact_missing_total counter from the ops_restore_test_results
			// rows the lenny-restore-test CronJob writes. Leader-gated; the
			// §16.9 /metrics exposition scrapes them. F-17.3.6.
			Name:       "restore-test-metrics",
			Expression: "* * * * *",
			Run: func(ctx context.Context) error {
				if deps.Pool == nil {
					return nil
				}
				return sampleRestoreTestMetrics(ctx, restoretest.NewPGStore(deps.Pool))
			},
		},
	}
	return svc, jobs
}

// backupLastSuccessfulTimestamp is the §25.11 line 4309
// lenny_backup_last_successful_timestamp{type} gauge: the Unix time of
// the last successful backup per type, evaluated by the §25.11
// BackupOverdue alert (line 4317: a full backup older than 48h). It is
// registered on the default registry so the sampler publishes a real
// gauge; the §16.9 lenny-ops /metrics exposition scrapes it (F-16.8.1).
var backupLastSuccessfulTimestamp = func() *prometheus.GaugeVec {
	g, err := metrics.NewGauge(prometheus.GaugeOpts{
		Name: "lenny_backup_last_successful_timestamp",
		Help: "§25.11 Unix timestamp of the last successful backup by type.",
	}, []string{"type"})
	if err != nil {
		return nil
	}
	metrics.MustRegister(prometheus.DefaultRegisterer, g)
	return g
}()

// restoreTestSuccess is the §25.11 / §16.1 lenny_restore_test_success
// gauge: the pass/fail flag of the latest automated test restore. The
// short-lived lenny-restore-test Job records its outcome in
// ops_restore_test_results; the leader lenny-ops replica re-exposes the
// latest run here so the §16.1 restore-test gate has a source. F-17.3.6.
var restoreTestSuccess = func() *prometheus.GaugeVec {
	g, err := metrics.NewGauge(prometheus.GaugeOpts{
		Name: "lenny_restore_test_success",
		Help: "§25.11 pass (1) / fail (0) flag of the latest automated restore test.",
	}, []string{})
	if err != nil {
		return nil
	}
	metrics.MustRegister(prometheus.DefaultRegisterer, g)
	return g
}()

// restoreTestDuration is the §25.11 lenny_restore_test_duration_seconds
// gauge: the wall-clock seconds of the latest automated test restore.
var restoreTestDuration = func() *prometheus.GaugeVec {
	g, err := metrics.NewGauge(prometheus.GaugeOpts{
		Name: "lenny_restore_test_duration_seconds",
		Help: "§25.11 elapsed seconds of the latest automated restore test.",
	}, []string{})
	if err != nil {
		return nil
	}
	metrics.MustRegister(prometheus.DefaultRegisterer, g)
	return g
}()

// restoreTestArtifactRate is the §25.11 line 4098
// lenny_restore_test_artifact_success_rate gauge: the sampled-HEAD
// ArtifactStore success rate of the latest test restore. It is published
// only when the latest run actually ran the sampled-HEAD check.
var restoreTestArtifactRate = func() *prometheus.GaugeVec {
	g, err := metrics.NewGauge(prometheus.GaugeOpts{
		Name: "lenny_restore_test_artifact_success_rate",
		Help: "§25.11 sampled-HEAD ArtifactStore success rate of the latest restore test.",
	}, []string{})
	if err != nil {
		return nil
	}
	metrics.MustRegister(prometheus.DefaultRegisterer, g)
	return g
}()

// restoreTestArtifactMissing is the §25.11 line 4098
// lenny_restore_test_artifact_missing_total counter: the cumulative
// count of sampled ArtifactStore objects absent at the replication
// target across every recorded test restore. The sampler advances it by
// the delta against the durable cumulative total so it stays monotonic
// within the process.
var restoreTestArtifactMissing = func() *prometheus.CounterVec {
	c, err := metrics.NewCounter(prometheus.CounterOpts{
		Name: "lenny_restore_test_artifact_missing_total",
		Help: "§25.11 sampled ArtifactStore objects absent at the replication target across restore tests.",
	}, []string{})
	if err != nil {
		return nil
	}
	metrics.MustRegister(prometheus.DefaultRegisterer, c)
	return c
}()

// lastRestoreTestArtifactMissing tracks the cumulative artifact-missing
// total already published so the sampler advances the monotonic counter
// by the delta. The restore-test sampler is single-threaded
// (leader-gated cron), so no synchronization is required.
var lastRestoreTestArtifactMissing int64

// dataResidencyViolationTotal is the §12.8 line 936
// lenny_data_residency_violation_total{operation} counter incremented on
// every fail-closed region-resolution abort the ops backup pipeline
// raises (operation="backup" on a BACKUP_REGION_UNRESOLVABLE). It is the
// lenny-ops process's series of the same counter the gateway increments
// for runtime StorageRouter and artifact-replication violations; the
// BackupRegionUnresolvable / data-residency alerts read it.
var dataResidencyViolationTotal = func() *prometheus.CounterVec {
	c, err := metrics.NewCounter(prometheus.CounterOpts{
		Name: "lenny_data_residency_violation_total",
		Help: "§12.8 fail-closed data-residency violations by operation.",
	}, []string{"operation"})
	if err != nil {
		return nil
	}
	metrics.MustRegister(prometheus.DefaultRegisterer, c)
	return c
}()

// backupResidencyMetrics adapts the §12.8 backup.ResidencyMetrics seam
// onto dataResidencyViolationTotal.
type backupResidencyMetrics struct{}

func (backupResidencyMetrics) DataResidencyViolation(operation string) {
	if dataResidencyViolationTotal != nil {
		dataResidencyViolationTotal.WithLabelValues(operation).Inc()
	}
}

// backupReconcileBlockedTotal is the §25.11 line 4320
// lenny_backup_reconcile_blocked_total{reason} counter the
// BackupReconcileBlocked alert evaluates. The post-restore erasure
// reconciler increments it (reason="legal_hold_ledger_stale") when the
// legal-hold ledger freshness gate blocks replay. F-25.11.7.
var backupReconcileBlockedTotal = func() *prometheus.CounterVec {
	c, err := metrics.NewCounter(prometheus.CounterOpts{
		Name: "lenny_backup_reconcile_blocked_total",
		Help: "§25.11 post-restore erasure-reconcile blocks by reason.",
	}, []string{"reason"})
	if err != nil {
		return nil
	}
	metrics.MustRegister(prometheus.DefaultRegisterer, c)
	return c
}()

// backupReconcileMetrics adapts the §25.11 backup.ReconcileMetrics seam
// onto backupReconcileBlockedTotal.
type backupReconcileMetrics struct{}

func (backupReconcileMetrics) ReconcileBlocked(reason string) {
	if backupReconcileBlockedTotal != nil {
		backupReconcileBlockedTotal.WithLabelValues(reason).Inc()
	}
}

// staticShardRegions is the §12.8 line 935 shard→region resolver driven
// by configuration (LENNY_OPS_BACKUP_SHARD_REGIONS) rather than a live
// StoreRouter region map. In v1 the residency region of each Postgres
// shard is declared by the operator; a StoreRouter-backed resolver
// replaces this when region-aware routing lands (F-25.4.14).
type staticShardRegions []backup.ShardRegion

func (s staticShardRegions) ShardRegions(context.Context) ([]backup.ShardRegion, error) {
	return []backup.ShardRegion(s), nil
}

// parseBackupRegions decodes the §12.8 backups.regions endpoint map and
// the shard→region resolver from their JSON encodings (the chart renders
// these from backups.regions and backups.shardRegions). An empty regions
// input yields the single-region global-dump path (nil, nil, nil). A
// non-empty regions map with no shardRegions is a misconfiguration that
// would leave every shard unresolvable; it is returned as an error so
// lenny-ops fails fast at startup rather than failing every backup.
// spec: §12.8 lines 932-936.
func parseBackupRegions(regionsJSON, shardRegionsJSON string) (map[string]backup.RegionBackupConfig, backup.ShardRegionResolver, error) {
	regionsJSON = strings.TrimSpace(regionsJSON)
	if regionsJSON == "" || regionsJSON == "{}" {
		return nil, nil, nil
	}
	var raw map[string]struct {
		MinioEndpoint          string `json:"minioEndpoint"`
		KMSKeyID               string `json:"kmsKeyId"`
		AccessCredentialSecret string `json:"accessCredentialSecret"`
		Bucket                 string `json:"bucket"`
	}
	if err := json.Unmarshal([]byte(regionsJSON), &raw); err != nil {
		return nil, nil, fmt.Errorf("parse backups.regions: %w", err)
	}
	regions := make(map[string]backup.RegionBackupConfig, len(raw))
	for region, r := range raw {
		regions[region] = backup.RegionBackupConfig{
			MinioEndpoint:          r.MinioEndpoint,
			KMSKeyID:               r.KMSKeyID,
			AccessCredentialSecret: r.AccessCredentialSecret,
			Bucket:                 r.Bucket,
		}
	}
	var shardRaw []struct {
		ShardID string `json:"shardId"`
		Region  string `json:"region"`
	}
	if s := strings.TrimSpace(shardRegionsJSON); s != "" && s != "[]" {
		if err := json.Unmarshal([]byte(s), &shardRaw); err != nil {
			return nil, nil, fmt.Errorf("parse backups.shardRegions: %w", err)
		}
	}
	if len(shardRaw) == 0 {
		return nil, nil, fmt.Errorf("backups.regions set but backups.shardRegions empty")
	}
	shards := make(staticShardRegions, 0, len(shardRaw))
	for _, sr := range shardRaw {
		shards = append(shards, backup.ShardRegion{ShardID: sr.ShardID, Region: sr.Region})
	}
	return regions, shards, nil
}

// sampleBackupMetrics reads the last successful backup time per type
// from svc and publishes it on lenny_backup_last_successful_timestamp.
// A type with no successful backup leaves its series unset rather than
// reporting a 1970 epoch that would read as a stale backup and trip
// BackupOverdue spuriously before the first backup completes.
//
// spec: §25.11 line 4309.
func sampleBackupMetrics(ctx context.Context, svc *backup.Service) error {
	if backupLastSuccessfulTimestamp == nil {
		return nil
	}
	times, err := svc.LastSuccessfulBackupTimes(ctx)
	if err != nil {
		return err
	}
	for typ, at := range times {
		backupLastSuccessfulTimestamp.WithLabelValues(typ).Set(float64(at.Unix()))
	}
	return nil
}

// sampleRestoreTestMetrics reads the latest §25.11 test-restore result
// from store and publishes the restore-test gauges plus the cumulative
// artifact-missing counter. A store with no recorded run leaves the
// gauges unset rather than reporting a zero that reads as a failed
// restore before the first run. spec: §25.11 lines 4098, 4254-4256.
func sampleRestoreTestMetrics(ctx context.Context, store restoretest.Store) error {
	if store == nil {
		return nil
	}
	latest, ok, err := store.Latest(ctx)
	if err != nil {
		return err
	}
	if ok {
		if restoreTestSuccess != nil {
			v := 0.0
			if latest.Success {
				v = 1.0
			}
			restoreTestSuccess.WithLabelValues().Set(v)
		}
		if restoreTestDuration != nil {
			restoreTestDuration.WithLabelValues().Set(latest.DurationSeconds())
		}
		// The artifact success rate is meaningful only when the latest run
		// actually ran the sampled-HEAD check; otherwise the series stays
		// absent until the first replication-checked run.
		if latest.ArtifactChecked && restoreTestArtifactRate != nil {
			restoreTestArtifactRate.WithLabelValues().Set(latest.ArtifactSuccessRate)
		}
	}
	if restoreTestArtifactMissing != nil {
		total, err := store.TotalArtifactMissing(ctx)
		if err != nil {
			return err
		}
		if delta := total - lastRestoreTestArtifactMissing; delta > 0 {
			restoreTestArtifactMissing.WithLabelValues().Add(float64(delta))
			lastRestoreTestArtifactMissing = total
		}
	}
	return nil
}

// opsSelfHealthStatus is the §16.8 / §25.4 line 2507
// lenny_ops_self_health_status{check} gauge: each self-health check's
// status encoded as 0=healthy, 1=degraded, 2=unhealthy. The
// OpsSelfHealthDegraded alert (§16.5) reads it. Registered on the
// default registry the §16.9 /metrics exposition serves.
var opsSelfHealthStatus = func() *prometheus.GaugeVec {
	g, err := metrics.NewGauge(prometheus.GaugeOpts{
		Name: "lenny_ops_self_health_status",
		Help: "§25.4 lenny-ops self-health status per check (0 healthy, 1 degraded, 2 unhealthy).",
	}, []string{"check"})
	if err != nil {
		return nil
	}
	metrics.MustRegister(prometheus.DefaultRegisterer, g)
	return g
}()

// publishSelfHealthMetric maps a §25.4 self-health report onto the
// lenny_ops_self_health_status{check} gauge, one series per check. It is
// wired to opsservice.Config.OnSelfHealthSample so the gauge is refreshed
// on every self-monitor evaluation. The HealthStatus enum already encodes
// 0=healthy, 1=degraded, 2=unhealthy, matching the §25.4 line 2507 gauge
// semantics.
//
// spec: §16.8 / §25.4 line 2507.
func publishSelfHealthMetric(report opsservice.SelfHealthReport) {
	if opsSelfHealthStatus == nil {
		return
	}
	for _, c := range report.Checks {
		opsSelfHealthStatus.WithLabelValues(c.Name).Set(float64(c.Status))
	}
}

// diagnosticsAuditRateLimited is the §25.9 lenny_audit_rate_limited_total
// counter (event_type, service_account). It is registered on the default
// registry so the rate-limit seam increments a real counter and the
// §16.9 lenny-ops /metrics exposition surfaces it.
var diagnosticsAuditRateLimited = func() *prometheus.CounterVec {
	c, err := metrics.NewCounter(prometheus.CounterOpts{
		Name: "lenny_audit_rate_limited_total",
		Help: "§25.9 diagnostic audit events dropped by rate limiting.",
	}, []string{"event_type", "service_account"})
	if err != nil {
		return nil
	}
	metrics.MustRegister(prometheus.DefaultRegisterer, c)
	return c
}()

// buildDiagnosticsAudit constructs the §25.9 diagnostics-audit config: a
// coalesced diagnostic audit event is logged (the audit-append stub,
// mirroring logAuditSink until the audit-store client lands) and a
// dropped event bumps lenny_audit_rate_limited_total so operators can
// detect rate-limited diagnostics. F-25.9.15.
func buildDiagnosticsAudit(ratePerMinute int, recorder *opsaudit.Recorder) *opsserver.DiagnosticsAuditConfig {
	return &opsserver.DiagnosticsAuditConfig{
		RatePerMinute: ratePerMinute,
		Emit: func(ev auditrate.Event) {
			recorder.Record(ev.EventType, map[string]any{
				"resourceType":    ev.ResourceType,
				"resourceId":      ev.ResourceID,
				"invocationCount": ev.InvocationCount,
				"serviceAccount":  ev.ServiceAccount,
				"operationId":     ev.OperationID,
			}, ev.FirstAt)
		},
		RateLimited: func(eventType, serviceAccount string) {
			if diagnosticsAuditRateLimited != nil {
				diagnosticsAuditRateLimited.WithLabelValues(eventType, serviceAccount).Inc()
			}
		},
	}
}

// bundleRulesReconciler returns the §25.4 line 1339 leader-only
// bundleRules reconciler. §25.13 line 4816 makes the bundled alerting
// rules static manifests rendered at Helm install/upgrade time with no
// runtime mutation, so the reconciler does not re-render rules; it
// re-asserts the §25.13 bundled-rules observability gauges
// (lenny_alerting_rules_bundled{format}, lenny_alerting_rule_overrides)
// from the chart-supplied bundle format set and override count so they
// stay current on the lenny-ops /metrics surface across metric resets.
// F-25.4.17.
func bundleRulesReconciler(mx *alertingmetrics.Metrics, formats []string, overrideCount int) opsservice.Reconciler {
	return func(context.Context) error {
		mx.SetBundledFormats(formats...)
		mx.SetOverrideCount(overrideCount)
		return nil
	}
}

// backupAuditSink is the §25.11 backup/restore AuditSink. The orchestrator
// emits an audit event on every state transition it owns (§25.11 line
// 4343); the sink commits the durable §11.7 platform-audit row through the
// recorder (logged only in the degraded no-Postgres mode). F-25.4.22.
func backupAuditSink(recorder *opsaudit.Recorder) func(backup.AuditEvent) {
	return func(ev backup.AuditEvent) {
		outcome := ev.Outcome
		if outcome == "" {
			outcome = "success"
		}
		recorder.Record(ev.Type, map[string]any{
			"backupId":  ev.BackupID,
			"restoreId": ev.RestoreID,
			"actor":     ev.Actor,
			"outcome":   outcome,
			"detail":    ev.Detail,
		}, time.Now())
	}
}

// scheduledBackupsDisabled reports whether the persisted §25.11
// backup schedule has enabled:false. A non-nil err is a transient
// store failure; the caller treats the fire as a no-op to avoid
// creating a backup the operator may have disabled.
func scheduledBackupsDisabled(ctx context.Context, svc *backup.Service) (bool, error) {
	sched, err := svc.GetSchedule(ctx)
	if err != nil {
		return false, err
	}
	if sched == nil {
		return false, nil
	}
	return !sched.Enabled, nil
}

// scheduledBackupCron returns the persisted §25.11 cron expression for a
// backup type so the cron evaluator fires on the runtime-modifiable
// schedule rather than the compiled-in default. An empty string (a
// store failure or a cleared field) tells the evaluator to fall back to
// the static Expression. spec: §25.11 line 4106; F-25.11.5.
func scheduledBackupCron(svc *backup.Service, typ backup.Type) string {
	sched, err := svc.GetSchedule(context.Background())
	if err != nil || sched == nil {
		return ""
	}
	switch typ {
	case backup.TypeFull:
		return sched.Full
	case backup.TypePostgres:
		return sched.Postgres
	default:
		return ""
	}
}

// streamEscalationEmitter is the §25.4 / §25.17 escalation Emitter. It
// publishes the escalation_created operational event onto the §25.5
// event stream (the platform-scoped ops:events:stream when Redis is
// wired, plus the local opsstream.Service buffer) through the shared
// EventEmitter. The §25.17 failure-path payoff — a webhook subscriber
// routing escalation_created to PagerDuty — depends on the event
// reaching the stream the webhook delivery worker fans out from.
//
// spec: §25.17 lines 5266-5285 ("The escalation_created event is emitted
// to the event stream. A webhook subscriber routes it to PagerDuty.").
type streamEscalationEmitter struct {
	emitter events.EventEmitter
	source  string
}

// newStreamEscalationEmitter wires the escalation service's emit hook to
// the shared lenny-ops EventEmitter. source is the §25.5 CloudEvents
// source value for events that originate in lenny-ops.
func newStreamEscalationEmitter(emitter events.EventEmitter, replicaID string) streamEscalationEmitter {
	return streamEscalationEmitter{emitter: emitter, source: "//lenny.dev/ops/" + replicaID}
}

// EmitEscalationCreated publishes the §25.4 escalation_created event and
// reports whether the publish reached the stream. A true return marks
// the escalation's emitted flag so the §25.4 background retry does not
// re-attempt; a false return (marshal or emit failure) leaves it
// un-emitted for the retry loop.
func (e streamEscalationEmitter) EmitEscalationCreated(esc escalation.Escalation) bool {
	payload, err := json.Marshal(esc)
	if err != nil {
		log.Printf("lenny-ops: marshal escalation_created for %s: %v", esc.ID, err)
		return false
	}
	severity := esc.Severity
	if severity == "" {
		severity = "warning"
	}
	if err := e.emitter.Emit(context.Background(), events.OperationalEvent{
		Type:            events.EventEscalationCreated.CloudEventsType(),
		Source:          e.source,
		Subject:         "escalation/" + esc.ID,
		Severity:        severity,
		DataContentType: "application/json",
		Data:            payload,
	}); err != nil {
		log.Printf("lenny-ops: emit escalation_created for %s: %v", esc.ID, err)
		return false
	}
	return true
}

// escalationConfig carries the §25.4 escalation durability knobs the
// lenny-ops binary exposes via flags / env.
type escalationConfig struct {
	// RequireDurable rejects a create with ESCALATION_NO_DURABLE_STORE when
	// no durable tier accepts it (§25.4 line 2396 ops.escalation.requireDurable).
	RequireDurable bool
	// ReconciliationWritesPerSecond paces the §25.4 line 2414 flush.
	ReconciliationWritesPerSecond int
}

// buildEscalationService constructs the §25.4 tiered escalation service.
// The durable tiers are composed in §25.4 priority order over the
// always-present in-memory Tier 3 buffer: the Postgres ops_escalations
// table (migration 0122) when a pool is available, then the Redis hash
// ops:escalations:{id} when a Redis client is available. Without either,
// the service runs the in-memory buffer alone (single-process degraded
// mode / dev). emitter publishes the §25.17 escalation_created event;
// the audit sink records the remediation.escalation_persisted flush event
// (logged until the lenny-ops audit-store client lands, matching the
// backup and drift audit posture). The leader-only flush reconciler
// (wired in main) promotes buffered records upward as a durable tier
// recovers.
//
// spec: §25.4 lines 2376-2455.
func buildEscalationService(emitter escalation.Emitter, pgPool *pgxpool.Pool, redisClient redis.UniversalClient, recorder *opsaudit.Recorder, cfg escalationConfig) *escalation.Service {
	var durable []escalation.Store
	if pgPool != nil {
		durable = append(durable, escpgstore.New(pgPool))
	}
	if redisClient != nil {
		durable = append(durable, escredisstore.New(redisClient))
	}
	return escalation.NewWithStores(escalation.Options{
		Durable:                       durable,
		Emitter:                       emitter,
		Audit:                         escalationAuditSink{recorder: recorder},
		RequireDurable:                cfg.RequireDurable,
		ReconciliationWritesPerSecond: cfg.ReconciliationWritesPerSecond,
	})
}

// escalationAuditSink is the §25.4 line 2415 escalation-flush audit sink.
// Each successful tier flush commits a durable remediation.escalation_-
// persisted row to the §11.7 platform chain through the recorder (logged
// only in the degraded no-Postgres mode). F-25.4.22.
type escalationAuditSink struct{ recorder *opsaudit.Recorder }

func (s escalationAuditSink) EscalationPersisted(_ context.Context, id, sourceTier, destTier string, durationMS int64) {
	s.recorder.Record(obsaudit.EventRemediationEscalationPersisted.String(), map[string]any{
		"escalationId": id,
		"sourceTier":   sourceTier,
		"destTier":     destTier,
		"durationMs":   durationMS,
	}, time.Now())
}

// buildIdempotencyStore returns the §25.4 idempotency store. When a
// Postgres pool is available it uses the durable ops_idempotency_keys
// table (migration 0116) so required-key endpoints coordinate across
// replicas and survive a restart, and a real outage surfaces as
// IDEMPOTENCY_STORE_UNAVAILABLE. Without Postgres it falls back to the
// in-process MemoryStore (single-process degraded mode / dev), which
// never reports an outage.
//
// spec: §25.4 lines 2011-2130.
func buildIdempotencyStore(pgPool *pgxpool.Pool) opsidem.Store {
	if pgPool != nil {
		return idempgstore.New(pgPool)
	}
	return opsidem.NewMemoryStore()
}

// driftServiceConfig carries the §25.10 lines 3809 / 3824 operator-
// tunable knobs the lenny-ops binary exposes via flags / env. The
// Postgres-backed store and the gateway-client reader are documented
// seams; the knobs apply unchanged once those land. F-25.10.7, F-25.10.9.
type driftServiceConfig struct {
	// StaleWarningDays is ops.drift.snapshotStaleWarningDays (§25.10 line
	// 3809). Default 7; 0 disables the snapshot-staleness warning.
	StaleWarningDays int
	// RunningStateCacheTTLSec is ops.drift.runningStateCacheTTLSeconds
	// (§25.10 line 3824). Default 60; 0 disables the running-state cache.
	RunningStateCacheTTLSec int
}

// buildDriftService constructs the §25.10 configuration-drift service.
// When a Postgres pool is available the desired-state snapshots persist
// to the bootstrap_seed_snapshot table (migration 0117) so the live and
// target rows survive a lenny-ops restart; without it the service falls
// back to the in-memory snapshot store (single-process degraded mode /
// dev). When a gateway admin client is configured the running state is
// read from the gateway admin API (§25.10 line 3770) via the
// gatewayreader collector; without one (no --gateway-url) the service
// falls back to the empty running state so a drift report still
// assembles. The reconcile resource applier remains a documented seam:
// a confirmed reconcile fails closed with DRIFT_RECONCILE_UNAVAILABLE
// until that applier lands (F-25.10.1).
//
// The §25.10 drift metrics, audit events, and operation_progressed
// emission are wired here: the two §25.10 line 3858-3859 counters, the
// audit sink (logged until the lenny-ops audit-store client lands,
// matching the backup/escalation posture), and the operation_progressed
// emitter over the shared §4.0 EventEmitter.
//
// The §25.10 line 3822 running-state cache is wired over the in-memory
// MemRunningStateCache. A non-positive TTL disables caching, matching
// the §25.10 line 3824 "0 disables" posture. F-25.10.2, F-25.10.3,
// F-25.10.4, F-25.10.5, F-25.10.7, F-25.10.9.
func buildDriftService(cfg driftServiceConfig, pgPool *pgxpool.Pool, gwClient *gateway.Client, emitter events.EventEmitter, recorder *opsaudit.Recorder) *driftservice.Service {
	var store driftservice.SnapshotStore = driftservice.NewMemSnapshotStore()
	if pgPool != nil {
		store = driftpgstore.New(pgPool)
	}
	// §25.10 line 3770: the running state is read from the gateway admin
	// API. The gatewayreader collector normalizes the admin LIST responses
	// into the snapshot's resource-keyed structure; without a gateway
	// client the service degrades to the empty running state. F-25.10.4.
	var reader driftservice.RunningStateReader = emptyRunningState{}
	if gwClient != nil {
		reader = gatewayreader.New(gwClient)
	}
	svc := driftservice.NewService(store, reader)
	svc.StaleWarningDays = cfg.StaleWarningDays
	if cfg.RunningStateCacheTTLSec > 0 {
		svc.SetRunningStateCache(driftservice.NewMemRunningStateCache(
			time.Duration(cfg.RunningStateCacheTTLSec) * time.Second,
		))
	}
	svc.SetMetrics(driftPromMetrics{})
	svc.SetAuditSink(driftAuditSink{recorder: recorder})
	if emitter != nil {
		svc.SetProgressEmitter(driftProgressEmitter{emitter: emitter})
	}
	return svc
}

// driftDetectedTotal and driftReconciledTotal are the §25.10 lines
// 3858-3859 drift counters. They are registered on the default registry
// so the service increments real series; the lenny-ops /metrics
// exposition that scrapes them is the same documented gap F-16.8.1
// tracks (mirroring lenny_backup_last_successful_timestamp). F-25.10.3.
var driftDetectedTotal = func() *prometheus.CounterVec {
	c, err := metrics.NewCounter(prometheus.CounterOpts{
		Name: "lenny_drift_detected_total",
		Help: "§25.10 configuration-drift detections by resource type and severity.",
	}, []string{"resource_type", "severity"})
	if err != nil {
		return nil
	}
	metrics.MustRegister(prometheus.DefaultRegisterer, c)
	return c
}()

var driftReconciledTotal = func() *prometheus.CounterVec {
	c, err := metrics.NewCounter(prometheus.CounterOpts{
		Name: "lenny_drift_reconciled_total",
		Help: "§25.10 configuration-drift reconciliation outcomes by resource type.",
	}, []string{"resource_type", "outcome"})
	if err != nil {
		return nil
	}
	metrics.MustRegister(prometheus.DefaultRegisterer, c)
	return c
}()

// driftPromMetrics adapts the §25.10 driftservice.Metrics seam onto the
// two Prometheus counters. F-25.10.3.
type driftPromMetrics struct{}

func (driftPromMetrics) DriftDetected(resourceType, severity string) {
	if driftDetectedTotal != nil {
		driftDetectedTotal.WithLabelValues(resourceType, severity).Inc()
	}
}

func (driftPromMetrics) Reconciled(resourceType, outcome string) {
	if driftReconciledTotal != nil {
		driftReconciledTotal.WithLabelValues(resourceType, outcome).Inc()
	}
}

// driftAuditSink is the §25.10 line 3871 drift audit sink. Each event is
// committed to the §11.7 platform chain through the recorder (logged only
// in the degraded no-Postgres mode). F-25.10.2, F-25.4.22.
type driftAuditSink struct{ recorder *opsaudit.Recorder }

func (s driftAuditSink) Emit(ev driftservice.AuditEvent) {
	fields := map[string]any{"actor": ev.Actor}
	for k, v := range ev.Details {
		fields[k] = v
	}
	s.recorder.Record(ev.Type, fields, time.Now())
}

// driftProgressEmitter emits the §25.10 line 3844 operation_progressed
// event onto the shared §4.0 EventEmitter so the §25.4 watchdog and any
// webhook subscriber observe a reconcile advancing resource-by-resource.
// F-25.10.1.
type driftProgressEmitter struct {
	emitter events.EventEmitter
}

func (e driftProgressEmitter) Progressed(ctx context.Context, info driftservice.ProgressInfo) {
	payload, err := json.Marshal(map[string]any{
		"operationId":    info.OperationID,
		"kind":           info.Kind,
		"totalSteps":     info.TotalSteps,
		"completedSteps": info.CompletedSteps,
		"currentStep":    info.CurrentStep,
		"startedBy":      info.StartedBy,
	})
	if err != nil {
		return
	}
	_ = e.emitter.Emit(ctx, events.OperationalEvent{
		Type:            events.EventOperationProgressed.CloudEventsType(),
		Subject:         "operation/" + info.OperationID,
		DataContentType: "application/json",
		Data:            payload,
	})
}

// emptyRunningState is the §25.10 RunningStateReader fallback used when
// no gateway admin client is configured (no --gateway-url). It reports
// an empty running state, so a drift report against a stored snapshot
// shows every desired field as removed — the degraded behavior when no
// gateway connection exists. When a gateway client is present the
// gatewayreader collector replaces it. F-25.10.4.
type emptyRunningState struct{}

// Compile-time guard that the §25.4 gateway admin client satisfies the
// §25.10 running-state collector's AdminGetter dependency. F-25.10.4.
var _ gatewayreader.AdminGetter = (*gateway.Client)(nil)

// RunningState returns an empty running state.
func (emptyRunningState) RunningState(context.Context, string) (map[string]any, error) {
	return map[string]any{}, nil
}

// diagnosticSourceDeps carries the live backends the §25.6 production
// DataSource reads. Pool is the platform Postgres (sessions,
// agent_pod_state, credential_leases); Clientset is the Kubernetes API
// (pod failure signals); Gateway is the gateway admin API (warm-pool
// config / CRD sync status); Probes are the §25.4 dependency probes
// (connectivity). Each optional backend that is absent degrades the
// affected diagnosis rather than failing it. F-25.6.1.
type diagnosticSourceDeps struct {
	Pool           *pgxpool.Pool
	Clientset      kubernetes.Interface
	Gateway        *gateway.Client
	Probes         map[string]probe.Func
	ProbeTimeout   time.Duration
	AgentNamespace string
}

// buildDiagnosticService constructs the §25.6 DiagnosticService over the
// production data source (prodsource.Source). The source reads sessions,
// pod counts, and credential-pool load from Postgres; enriches pod
// failure signals from the Kubernetes API; reads warm-pool config from
// the gateway admin API; and runs the dependency probes for connectivity.
// A backend that is not configured in a given deployment degrades the
// affected fields (recorded in the §25.6 Degradation envelope) rather
// than failing the diagnosis. With no Postgres the session/pool/credential
// diagnoses report not-found while connectivity still probes. F-25.6.1.
func buildDiagnosticService(deps diagnosticSourceDeps) diagnostics.DiagnosticService {
	src := &prodsource.Source{
		Conn:         prodsource.NewProbeConnectivity(deps.Probes, deps.ProbeTimeout),
		PodNamespace: deps.AgentNamespace,
	}
	if deps.Pool != nil {
		src.PG = prodsource.NewPGReader(deps.Pool)
	}
	if deps.Clientset != nil {
		src.Pods = prodsource.NewK8sReader(deps.Clientset, deps.AgentNamespace)
	}
	if deps.Gateway != nil {
		src.Pools = prodsource.NewGatewayPoolReader(deps.Gateway)
	}
	return diagnostics.NewService(src)
}

// doctorDeps carries the inputs for the §25.6 auto-remediation
// orchestrator backing POST /v1/admin/diagnostics/run[?fix=true].
type doctorDeps struct {
	// Clientset and Dynamic are the cluster clients the remediator acts
	// through. A nil Clientset leaves the surface unconfigured.
	Clientset kubernetes.Interface
	Dynamic   dynamic.Interface
	// ReleaseNS is the namespace holding the cert-manager Certificate CRs
	// the certManagerExpiring fix targets, the lenny-bootstrap ConfigMap
	// the bootstrapConfigDrift fix re-applies, and the monitoring objects
	// the prometheusRuleMissing fix asserts.
	ReleaseNS string
	// Helm yields the §25.6 Helm-rendered references the bootstrapConfigDrift
	// and prometheusRuleMissing fixes compare against and re-apply. A nil
	// source leaves both findings undetected (reported not_detected).
	Helm doctor.HelmRenderSource
	// AllowedFixes is admin.doctor.allowedFixes; empty means the full set.
	AllowedFixes []string
	// FixTimeout is admin.doctor.fixTimeoutSeconds; zero applies the 120s
	// default.
	FixTimeout time.Duration
	// MaintenanceMode reports global.maintenanceMode. A nil hook is "off".
	MaintenanceMode func() bool
	// Audit emits the §25.6 diagnostics.fix_* lifecycle events.
	Audit func(doctor.Event)
}

// buildDoctorService constructs the §25.6 doctor orchestrator over the
// production Kubernetes remediator. A nil Clientset (no cluster
// connection in single-process dev) leaves the orchestrator nil, so the
// run endpoint reports 503 DOCTOR_UNAVAILABLE. spec: §25.6 lines
// 2941-2982. F-25.6.2, F-24.2.3.
func buildDoctorService(deps doctorDeps) doctor.Service {
	if deps.Clientset == nil {
		return nil
	}
	rem := &k8sDoctorRemediator{
		clientset: deps.Clientset,
		dyn:       deps.Dynamic,
		releaseNS: deps.ReleaseNS,
		helm:      deps.Helm,
	}
	o := doctor.New(rem, doctor.Config{
		AllowedFixes:    deps.AllowedFixes,
		FixTimeout:      deps.FixTimeout,
		MaintenanceMode: deps.MaintenanceMode,
		Audit:           deps.Audit,
	})
	if o == nil {
		return nil
	}
	return o
}

// buildReleaseChannelPublisher constructs the §25.8 release-channel
// manifest publisher from the operator-supplied flag set: the active
// signing key (PEM-encoded PKCS8 Ed25519 private key) plus its
// operator-assigned identifier, an optional previous public key
// (PEM-encoded PKIX) for the rotation overlap window, and the manifest
// JSON the publisher serves on the canonical channel.
//
// When the signing-key path is empty the publisher is not built and
// the function returns nil; the lenny-ops opsserver then leaves GET
// /v1/latest unmapped (404). §25.8 keeps the publisher an explicit
// operator activation rather than a default-on path — an air-gap
// mirror that does not yet have an operator-held Ed25519 key should
// not silently serve unsigned responses.
//
// Configuration errors (an unreadable key file, a malformed PEM block,
// a missing key ID) are fatal: a publisher that cannot sign would
// otherwise fail every request with a 503, which is a worse failure
// mode than refusing to start.
func buildReleaseChannelPublisher(
	currentKeyPath, currentKeyID,
	previousKeyPath, previousKeyID,
	manifestPath string,
) *releasechannel.Publisher {
	if currentKeyPath == "" {
		// Not enabled. The opsserver leaves the route unmapped.
		return nil
	}
	if currentKeyID == "" {
		log.Fatalf("lenny-ops: --release-channel-key-file is set but --release-channel-key-id is empty; " +
			"the §25.8 signature envelope requires a stable key identifier")
	}
	if manifestPath == "" {
		log.Fatalf("lenny-ops: --release-channel-key-file is set but --release-channel-manifest-file is empty; " +
			"the §25.8 publisher needs a manifest body to sign and serve")
	}

	current, err := loadEd25519PrivateKey(currentKeyPath, currentKeyID)
	if err != nil {
		log.Fatalf("lenny-ops: load release-channel signing key: %v", err)
	}

	var previous *releasechannel.Key
	if previousKeyPath != "" {
		if previousKeyID == "" {
			log.Fatalf("lenny-ops: --release-channel-previous-key-file is set but --release-channel-previous-key-id is empty")
		}
		prev, err := loadEd25519PublicKey(previousKeyPath, previousKeyID)
		if err != nil {
			log.Fatalf("lenny-ops: load release-channel previous key: %v", err)
		}
		previous = &prev
	}

	signer, err := releasechannel.NewSigner(current, previous)
	if err != nil {
		log.Fatalf("lenny-ops: build release-channel signer: %v", err)
	}

	manifestBytes, err := os.ReadFile(manifestPath)
	if err != nil {
		log.Fatalf("lenny-ops: read release-channel manifest %s: %v", manifestPath, err)
	}
	var stable releasechannel.Manifest
	if err := json.Unmarshal(manifestBytes, &stable); err != nil {
		log.Fatalf("lenny-ops: decode release-channel manifest %s: %v", manifestPath, err)
	}

	source := releasechannel.NewStaticSource(map[releasechannel.Channel]releasechannel.Manifest{
		releasechannel.ChannelStable: stable,
	})
	publisher, err := releasechannel.NewPublisher(releasechannel.PublisherOptions{
		Source: source,
		Signer: signer,
	})
	if err != nil {
		log.Fatalf("lenny-ops: build release-channel publisher: %v", err)
	}
	log.Printf("lenny-ops: §25.8 release-channel publisher active (keyId=%s, previous=%q, manifest=%s)",
		signer.CurrentKeyID(), signer.PreviousKeyID(), manifestPath)
	return publisher
}

// loadEd25519PrivateKey reads a PEM-encoded Ed25519 private key from
// path and returns it as a releasechannel.Key tagged with keyID. The
// PEM block must carry a PKCS8 envelope; PKCS1 and SEC 1 forms are not
// used for Ed25519 in v1.
func loadEd25519PrivateKey(path, keyID string) (releasechannel.Key, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return releasechannel.Key{}, fmt.Errorf("read %s: %w", path, err)
	}
	block, _ := pem.Decode(raw)
	if block == nil {
		return releasechannel.Key{}, fmt.Errorf("no PEM block in %s", path)
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return releasechannel.Key{}, fmt.Errorf("parse PKCS8 in %s: %w", path, err)
	}
	priv, ok := parsed.(ed25519.PrivateKey)
	if !ok {
		return releasechannel.Key{}, errors.New("release-channel signing key is not an Ed25519 private key")
	}
	return releasechannel.Key{
		ID:      keyID,
		Private: priv,
		Public:  priv.Public().(ed25519.PublicKey),
	}, nil
}

// loadEd25519PublicKey reads a PEM-encoded Ed25519 public key from
// path and returns it as a releasechannel.Key tagged with keyID. The
// PEM block must carry a PKIX envelope.
func loadEd25519PublicKey(path, keyID string) (releasechannel.Key, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return releasechannel.Key{}, fmt.Errorf("read %s: %w", path, err)
	}
	block, _ := pem.Decode(raw)
	if block == nil {
		return releasechannel.Key{}, fmt.Errorf("no PEM block in %s", path)
	}
	parsed, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return releasechannel.Key{}, fmt.Errorf("parse PKIX in %s: %w", path, err)
	}
	pub, ok := parsed.(ed25519.PublicKey)
	if !ok {
		return releasechannel.Key{}, errors.New("release-channel previous key is not an Ed25519 public key")
	}
	return releasechannel.Key{ID: keyID, Public: pub}, nil
}

// platformUpgradeAvailable is the §16.5 lenny_platform_upgrade_available
// gauge: 1 when the configured release channel advertises a newer
// version than the running lenny-ops, 0 otherwise. The
// PlatformUpgradeAvailable alert (§16.5 line 1569) reads it. It is
// registered on the process default registry so the §16.9 /metrics
// exposition scrapes it (F-16.8.1).
//
// spec: §16.5 PlatformUpgradeAvailable, §25.8 Upgrade Check.
var platformUpgradeAvailable = func() *prometheus.GaugeVec {
	g, err := metrics.NewGauge(prometheus.GaugeOpts{
		Name: "lenny_platform_upgrade_available",
		Help: "§25.8 1 when a newer platform release than the running version is available.",
	}, []string{})
	if err != nil {
		return nil
	}
	metrics.MustRegister(prometheus.DefaultRegisterer, g)
	return g
}()

// operationsStalled is the §25.2 line 399 lenny_ops_operations_stalled
// gauge: the count of in-flight operations whose progress exceeded their
// expected inter-step cadence (stalledForSeconds > 0). The
// OperationStalled alert (§16.5) reads it. It is registered on the
// process default registry and pre-stamped to 0 so the §16.9 /metrics
// exposition always carries the series, and the §25.2 operations-observe
// loop updates it on each leader tick.
//
// spec: §25.2 lines 399, §16.5 OperationStalled.
var operationsStalled = func() *prometheus.GaugeVec {
	g, err := metrics.NewGauge(prometheus.GaugeOpts{
		Name: "lenny_ops_operations_stalled",
		Help: "§25.2 count of in-flight operations whose progress exceeded their expected cadence (stalledForSeconds > 0).",
	}, []string{})
	if err != nil {
		return nil
	}
	metrics.MustRegister(prometheus.DefaultRegisterer, g)
	g.WithLabelValues().Set(0)
	return g
}()

// setOperationsStalled publishes the §25.2 stalled-operation count onto
// the lenny_ops_operations_stalled gauge.
func setOperationsStalled(n float64) {
	if operationsStalled == nil {
		return
	}
	operationsStalled.WithLabelValues().Set(n)
}

// buildBaselineStore returns the §25.2 operation-baseline store. With a
// Postgres pool it persists the ops_operation_baselines table (migration
// 0128) so the historical_p50 ETA survives a restart and is visible
// across replicas; in single-process degraded mode it falls back to the
// in-memory store, which accumulates baselines within a single process
// lifetime.
//
// spec: §25.2 lines 393-394.
func buildBaselineStore(pool *pgxpool.Pool) operations.BaselineStore {
	if pool != nil {
		return baselinestore.New(pool)
	}
	return operations.NewMemoryBaselineStore()
}

// opsBaselineRecorder adapts a §25.2 operations.BaselineStore onto the
// upgradeservice.BaselineRecorder seam (string kind) so the upgrade
// orchestrator records the platform_upgrade completion duration without
// importing the operations.Kind enum.
type opsBaselineRecorder struct{ store operations.BaselineStore }

func (r opsBaselineRecorder) RecordCompletion(ctx context.Context, kind string, dur time.Duration) error {
	if r.store == nil {
		return nil
	}
	return r.store.RecordCompletion(ctx, operations.Kind(kind), dur)
}

// setPlatformUpgradeAvailable is the upgradeservice.AvailabilityGauge
// the §25.8 upgrade-check raises on each evaluation.
func setPlatformUpgradeAvailable(available bool) {
	if platformUpgradeAvailable == nil {
		return
	}
	v := 0.0
	if available {
		v = 1.0
	}
	platformUpgradeAvailable.WithLabelValues().Set(v)
}

// upgradeAuditSink is the §16.7 platform-upgrade AuditSink. Each lifecycle
// transition is committed to the §11.7 platform chain through the recorder
// (logged only in the degraded no-Postgres mode). F-25.4.22.
func upgradeAuditSink(recorder *opsaudit.Recorder) func(upgradeservice.AuditEvent) {
	return func(ev upgradeservice.AuditEvent) {
		recorder.Record(ev.Type, map[string]any{
			"operationId":   ev.OperationID,
			"actor":         ev.Actor,
			"oldPhase":      ev.OldPhase,
			"newPhase":      ev.NewPhase,
			"targetVersion": ev.TargetVersion,
			"detail":        ev.Detail,
		}, ev.At)
	}
}

// buildUpgradeService constructs the §25.8 platform-upgrade orchestrator.
// The orchestrator drives the §25.8 phase machine (pkg/upgrade) and emits
// the §16.7 platform-upgrade lifecycle audit events plus the §16.6
// upgrade_progressed operational events through the shared lenny-ops
// EventEmitter. When a Postgres pool is available the upgrade state is
// persisted to the platform_upgrade_state singleton (§25.4 line 1492) so
// a paused upgrade survives a leader-election handoff (§25.8 line 3560);
// in single-process degraded mode (no pool) it falls back to the
// in-memory store, which survives within a single leader term. The
// drift cleaner deletes the §25.10 target snapshot when a rollback
// reaches RolledBack (§25.8 line 3551).
//
// spec: §25.8, §10.5 (F-10.5.5 audit emission, F-10.5.7 orchestrator
// consumer).
// buildUpgradeStore selects the §25.8 platform_upgrade_state store: the
// Postgres-backed singleton when a pool is present (so a paused upgrade
// survives a leader handoff, spec line 3560), the in-memory store
// otherwise. It is shared by the orchestrator and the preflighter so both
// observe the same in-flight upgrade.
func buildUpgradeStore(pool *pgxpool.Pool) upgradeservice.Store {
	if pool != nil {
		return upgradepg.New(pool)
	}
	return upgradeservice.NewMemoryStore()
}

func buildUpgradeService(store upgradeservice.Store, drift *driftservice.Service, emitter events.EventEmitter, baselines operations.BaselineStore, recorder *opsaudit.Recorder) *upgradeservice.Service {
	opts := upgradeservice.Options{
		Store:   store,
		Emitter: emitter,
		Audit:   upgradeAuditSink(recorder),
		// §25.2 line 393: fold the upgrade's completion duration into the
		// historical baseline table so a later upgrade gets a historical_p50
		// ETA.
		Baselines: opsBaselineRecorder{store: baselines},
	}
	if drift != nil {
		opts.DriftManager = drift
	}
	return upgradeservice.New(opts)
}

// registryAuditSink adapts the registryservice audit event onto the
// durable §16.7 audit recorder (platform.registry_updated).
func registryAuditSink(recorder *opsaudit.Recorder) registryservice.AuditSink {
	return func(ev registryservice.AuditEvent) {
		recorder.Record(ev.Type, map[string]any{
			"actor":  ev.Actor,
			"detail": ev.Detail,
		}, ev.At)
	}
}

// configAuditSink adapts the configservice audit event onto the durable
// §16.7 audit recorder (platform.config_changed). The changed paths and
// the restart verdict are carried as structured fields so the SIEM can
// pivot from a config-apply row to the affected settings.
func configAuditSink(recorder *opsaudit.Recorder) configservice.AuditSink {
	return func(ev configservice.AuditEvent) {
		recorder.Record(ev.Type, map[string]any{
			"actor":           ev.Actor,
			"changedPaths":    ev.ChangedPaths,
			"restartRequired": ev.RestartRequired,
		}, ev.At)
	}
}

// registryBaseConfig assembles the §25.8 chart-base registry config from
// the platform.registry.* flags. A malformed overrides JSON is logged and
// dropped (the base url still serves), since a registry override is an
// optional refinement of the default resolution.
func registryBaseConfig(url, pullSecret string, requireDigest bool, overridesJSON string) registryservice.EffectiveConfig {
	var overrides map[string]string
	if strings.TrimSpace(overridesJSON) != "" {
		if err := json.Unmarshal([]byte(overridesJSON), &overrides); err != nil {
			log.Printf("lenny-ops: ignoring malformed --registry-overrides: %v", err)
			overrides = nil
		}
	}
	return registryservice.EffectiveConfig{
		URL:            url,
		Overrides:      overrides,
		PullSecretName: pullSecret,
		RequireDigest:  requireDigest,
		Source:         "helm",
	}
}

// buildRegistryService constructs the §25.8 runtime registry API service.
// The chart-rendered platform.registry.* values are the base; a runtime
// PUT persists an override to Postgres (migration 0135) when a pool is
// available so the change survives a restart. In single-process degraded
// mode (no pool) the registry is read at the chart base and PUT reports it
// read-only.
//
// spec: §25.8 Image Registry Configuration / Runtime API.
func buildRegistryService(pool *pgxpool.Pool, base registryservice.EffectiveConfig, recorder *opsaudit.Recorder) *registryservice.Service {
	var store registryservice.Store
	if pool != nil {
		store = registryservice.NewPgStore(pool)
	}
	return registryservice.New(registryservice.Options{
		Base:  base,
		Store: store,
		Audit: registryAuditSink(recorder),
	})
}

// pgConnChecker is the §25.8 preflight Postgres-connection gate (spec line
// 3501): it reports whether the pool has headroom for the migration phase.
// A nil pool reports healthy (the single-process degraded path runs no
// migration phase).
type pgConnChecker struct{ pool *pgxpool.Pool }

func (c pgConnChecker) HasFreeConnections(context.Context) (bool, string, error) {
	if c.pool == nil {
		return true, "no pooled Postgres (single-process mode)", nil
	}
	stat := c.pool.Stat()
	free := stat.MaxConns() - stat.AcquiredConns()
	if free <= 0 {
		return false, "no free Postgres connections for migration", nil
	}
	return true, fmt.Sprintf("%d/%d connections free", free, stat.MaxConns()), nil
}

// buildPreflighter constructs the §25.8 upgrade preflighter over the
// shared upgrade store (so it sees an in-flight upgrade) and the Postgres
// connection gate. The platform-health and image-pullability gates are
// left unconfigured in v1 (the live gateway-health probe and registry HEAD
// check are documented follow-ons); the preflighter degrades those gates
// to skipped and still returns the resolved image plan as a preview.
//
// spec: §25.8 Phase 1 (lines 3496-3503).
func buildPreflighter(store upgradeservice.Store, pool *pgxpool.Pool) *upgradeservice.Preflighter {
	return upgradeservice.NewPreflighter(upgradeservice.PreflighterOptions{
		Store: store,
		Conns: pgConnChecker{pool: pool},
	})
}

// imagePullCheckDuration is the §25.8 line 3619
// lenny_platform_image_pull_check_duration_seconds histogram: the latency
// of one preflight/watchdog image-pullability observation, labelled by
// component. It is registered on the process default registry so the §16.9
// /metrics exposition scrapes it.
//
// spec: §25.8 Metrics (line 3619).
var imagePullCheckDuration = func() *prometheus.HistogramVec {
	h, err := metrics.NewHistogram(prometheus.HistogramOpts{
		Name:    "lenny_platform_image_pull_check_duration_seconds",
		Help:    "§25.8 latency of a platform-upgrade image-pullability check, per component.",
		Buckets: prometheus.DefBuckets,
	}, []string{"component"})
	if err != nil {
		return nil
	}
	metrics.MustRegister(prometheus.DefaultRegisterer, h)
	return h
}()

// recordImagePullCheck observes an image-pull-check latency on the §25.8
// histogram. The watchdog calls it per observation.
func recordImagePullCheck(component string, d time.Duration) {
	if imagePullCheckDuration == nil {
		return
	}
	imagePullCheckDuration.WithLabelValues(component).Observe(d.Seconds())
}

// buildWatchdog constructs the §25.8 OpsRoll watchdog over the upgrade
// orchestrator. The pod-observation seam is left nil in v1 (the Kubernetes
// pod-watch implementation is a documented follow-on), so the watchdog
// drives the timeout auto-rollback while the image-pull-failure event
// remains dormant until a PodObserver is wired. The histogram is recorded
// on every observation pass.
//
// spec: §25.8 lines 3509-3511, 3619.
func buildWatchdog(svc *upgradeservice.Service, cfg upgradeservice.WatchdogConfig, emitter events.EventEmitter) *upgradeservice.Watchdog {
	return upgradeservice.NewWatchdog(upgradeservice.WatchdogOptions{
		Service: svc,
		Config:  cfg,
		Emitter: emitter,
		Record:  recordImagePullCheck,
	})
}

// upgradeWatchdogJob is the leader-only cron that runs the §25.8 OpsRoll
// watchdog every minute while an upgrade is in a roll phase. It is a no-op
// outside a roll phase, so it costs one store read per minute at idle.
//
// spec: §25.8 lines 3509-3511 (OpsRoll watchdog goroutine).
func upgradeWatchdogJob(wd *upgradeservice.Watchdog) opsservice.ScheduledJob {
	return opsservice.ScheduledJob{
		Name:       "platform-upgrade-watchdog",
		Expression: "* * * * *", // every minute
		Run: func(ctx context.Context) error {
			res, err := wd.Evaluate(ctx)
			if err != nil {
				return err
			}
			if res.RolledBack {
				log.Printf("lenny-ops: platform-upgrade watchdog auto-rolled-back %s after timeout", res.Phase)
			}
			return nil
		},
	}
}

// buildUpgradeChecker constructs the §25.8 upgrade-check client. It reads
// the operator-supplied release manifest (the same document the
// release-channel publisher serves at GET /v1/latest) as its Source, so
// an air-gapped or mirror install that hosts its own channel can compare
// the advertised version against the running one. When no manifest is
// configured (platform.upgradeChannel: "") the checker is disabled and
// GET /v1/admin/platform/upgrade-check reports the channel disabled.
//
// spec: §25.8 Upgrade Check, Air-Gapped Support.
func buildUpgradeChecker(manifestPath, currentVersion string, pool *pgxpool.Pool, emitter events.EventEmitter, recorder *opsaudit.Recorder) *upgradeservice.Checker {
	var source releasechannel.Source
	if manifestPath != "" {
		manifestBytes, err := os.ReadFile(manifestPath)
		if err != nil {
			log.Fatalf("lenny-ops: read upgrade-check manifest %s: %v", manifestPath, err)
		}
		var stable releasechannel.Manifest
		if err := json.Unmarshal(manifestBytes, &stable); err != nil {
			log.Fatalf("lenny-ops: decode upgrade-check manifest %s: %v", manifestPath, err)
		}
		source = releasechannel.NewStaticSource(map[releasechannel.Channel]releasechannel.Manifest{
			releasechannel.ChannelStable: stable,
		})
	}
	// §25.8 line 3414 Caching: a successful check writes the Postgres
	// cache so an unreachable channel serves cached data (line 3413); in
	// single-process degraded mode the in-memory cache survives one term.
	var cache upgradeservice.CheckCache = upgradeservice.NewMemCheckCache()
	if pool != nil {
		cache = upgradepg.NewCheckCache(pool)
	}
	return upgradeservice.NewChecker(upgradeservice.CheckerOptions{
		Source:         source,
		CurrentVersion: currentVersion,
		Emitter:        emitter,
		Audit:          upgradeAuditSink(recorder),
		Gauge:          setPlatformUpgradeAvailable,
		Cache:          cache,
	})
}

// certExpirySeconds is the lenny_cert_expiry_seconds gauge the §16.5
// CertExpiryImminent alert reads (min(lenny_cert_expiry_seconds) < 3600).
// The §25.8 cert-manager health source sets it per certificate on each
// self-health probe, so the alert fires on the same signal the certManager
// health component reports (§25.8 line 3461). A negative value (an expired
// certificate) clamps to 0 so the alert reads "expiring now".
//
// spec: §25.8 line 3461, §16.5 CertExpiryImminent.
var certExpirySeconds = func() *prometheus.GaugeVec {
	g, err := metrics.NewGauge(prometheus.GaugeOpts{
		Name: "lenny_cert_expiry_seconds",
		Help: "Seconds until a cert-manager-managed certificate expires, per certificate.",
	}, []string{"certificate"})
	if err != nil {
		return nil
	}
	metrics.MustRegister(prometheus.DefaultRegisterer, g)
	return g
}()

// setCertExpiry records a certificate's remaining lifetime on the
// lenny_cert_expiry_seconds gauge. The §25.8 cert-manager source calls it
// for each certificate it lists.
func setCertExpiry(certificate string, seconds float64) {
	if certExpirySeconds == nil {
		return
	}
	if seconds < 0 {
		seconds = 0
	}
	certExpirySeconds.WithLabelValues(certificate).Set(seconds)
}

// buildPlatformConfigService constructs the §25.8 config diff/apply
// service over the gateway admin-API client. A nil gateway client (no
// cluster / dev mode) yields a nil service so the config routes stay
// unmapped. The v1 service ships without a schema validator: the
// generated pkg/chart/values validator is the documented follow-on
// (§17 line 655); until then any well-formed config is accepted, and the
// gateway remains the authoritative validator on apply.
//
// spec: §25.8 Config Diff and Config Apply (lines 3566-3574).
func buildPlatformConfigService(gw *gateway.Client, recorder *opsaudit.Recorder) *configservice.Service {
	cfgClient := newGatewayConfigClient(gw)
	if cfgClient == nil {
		return nil
	}
	return configservice.New(configservice.Options{
		Gateway: cfgClient,
		Audit:   configAuditSink(recorder),
	})
}

// platformVersionDrift is the §25.8 lenny_platform_version_drift gauge:
// the count of platform components whose version differs from the
// running lenny-ops build, read by the PlatformVersionDrift alert (§16.5
// line 1587). It is registered on the process default registry so the
// §16.9 /metrics exposition scrapes it (F-16.8.1).
//
// spec: §25.8 Metrics (line 3618), §16.5 PlatformVersionDrift.
var platformVersionDrift = func() *prometheus.GaugeVec {
	g, err := metrics.NewGauge(prometheus.GaugeOpts{
		Name: "lenny_platform_version_drift",
		Help: "§25.8 count of platform components whose version drifts from the running lenny-ops build.",
	}, []string{})
	if err != nil {
		return nil
	}
	metrics.MustRegister(prometheus.DefaultRegisterer, g)
	return g
}()

// setPlatformVersionDrift is the upgradeservice.DriftGauge the §25.8
// version aggregator calls after each aggregation.
func setPlatformVersionDrift(count int) {
	if platformVersionDrift == nil {
		return
	}
	platformVersionDrift.WithLabelValues().Set(float64(count))
}

// buildVersionAggregator constructs the §25.8 GET
// /v1/admin/platform/version/full aggregator over the component sources
// lenny-ops can reach: the compiled-in lenny-ops build version (always),
// the gateway binary version (over HTTP, F-25.8.4 GatewayClient.GetVersion
// call site), the controller Deployment image tag (over the K8s API),
// and the Postgres schema migration version (over the connection pool).
// A source the deployment cannot reach degrades its component to
// unavailable in the report rather than failing the whole aggregation
// (the §25.8 partial-data degradation model). The CRD-version and
// Helm-chart-version sources are the documented follow-on additions; the
// aggregator accepts them as further VersionSource implementations.
//
// spec: §25.8 Version Aggregation (line 3364).
func buildVersionAggregator(buildVersion, gatewayURL string, gw *http.Client, pool *pgxpool.Pool, clientset *kubernetes.Clientset, namespace string) *upgradeservice.VersionAggregator {
	sources := []upgradeservice.VersionSource{
		// lenny-ops is the reference: its current version is the required
		// version, so it never drifts and anchors the report.
		upgradeservice.NewFuncVersionSource("ops", buildVersion, func(context.Context) (string, error) {
			return buildVersion, nil
		}),
	}
	if gatewayURL != "" && gw != nil {
		sources = append(sources, upgradeservice.NewFuncVersionSource(
			"gateway", buildVersion, gatewayVersionFunc(gw, gatewayURL),
		))
	}
	if clientset != nil {
		sources = append(sources, upgradeservice.NewFuncVersionSource(
			"controllers", buildVersion, controllerVersionFunc(clientset, namespace),
		))
	}
	if pool != nil {
		// The Postgres schema version is a migration counter, not the
		// platform build version, so it carries no compiled-in required
		// value yet (drift detection for it lands with the embedded
		// required-schema constant). It is reported for introspection.
		sources = append(sources, upgradeservice.NewFuncVersionSource(
			"postgres-schema", "", schemaVersionFunc(pool),
		))
	}
	return upgradeservice.NewVersionAggregator(upgradeservice.VersionAggregatorOptions{
		PlatformVersion: buildVersion,
		Sources:         sources,
		Gauge:           setPlatformVersionDrift,
	})
}

// gatewayVersionFunc resolves the gateway binary version via the §25.3
// GET /v1/admin/platform/version endpoint — the GatewayClient.GetVersion
// call site §25.8 names.
func gatewayVersionFunc(client *http.Client, gatewayURL string) func(context.Context) (string, error) {
	return func(ctx context.Context) (string, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet,
			strings.TrimRight(gatewayURL, "/")+"/v1/admin/platform/version", nil)
		if err != nil {
			return "", err
		}
		resp, err := client.Do(req)
		if err != nil {
			return "", err
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode != http.StatusOK {
			return "", fmt.Errorf("gateway version endpoint returned HTTP %d", resp.StatusCode)
		}
		var body struct {
			GatewayVersion string `json:"gatewayVersion"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
			return "", fmt.Errorf("decode gateway version: %w", err)
		}
		if body.GatewayVersion == "" {
			return "", errors.New("gateway reported an empty version")
		}
		return body.GatewayVersion, nil
	}
}

// schemaVersionFunc resolves the Postgres schema version per §25.8 (the
// value `SELECT version FROM schema_migrations ORDER BY version DESC
// LIMIT 1` reports). The version column is cast to text so an integer
// migration counter and a string version both scan cleanly.
func schemaVersionFunc(pool *pgxpool.Pool) func(context.Context) (string, error) {
	return func(ctx context.Context) (string, error) {
		var v string
		err := pool.QueryRow(ctx,
			"SELECT version::text FROM schema_migrations ORDER BY version DESC LIMIT 1").Scan(&v)
		if err != nil {
			return "", err
		}
		return v, nil
	}
}

// controllerVersionFunc resolves the controller Deployment version from
// the image tag of the `lenny-controller` Deployment's `controller`
// container (the chart names them in charts/lenny/templates/controller-
// deployment.yaml).
func controllerVersionFunc(clientset *kubernetes.Clientset, namespace string) func(context.Context) (string, error) {
	return func(ctx context.Context) (string, error) {
		dep, err := clientset.AppsV1().Deployments(namespace).Get(ctx, "lenny-controller", metav1.GetOptions{})
		if err != nil {
			return "", err
		}
		for _, c := range dep.Spec.Template.Spec.Containers {
			if c.Name != "controller" {
				continue
			}
			if i := strings.LastIndex(c.Image, ":"); i >= 0 && i < len(c.Image)-1 {
				return c.Image[i+1:], nil
			}
			return "", fmt.Errorf("controller image %q has no tag", c.Image)
		}
		return "", errors.New("lenny-controller Deployment has no controller container")
	}
}

// upgradeCheckJob is the §25.8 / §25.4 line 1338 platform_upgrade_check
// cron: the leader-only job that queries the release channel hourly and
// raises the lenny_platform_upgrade_available gauge plus the
// platform_upgrade_available operational event when a newer release is
// advertised. A disabled channel makes the job a no-op.
//
// spec: §25.4 line 1338 (platform_upgrade_check cron), §25.8.
func upgradeCheckJob(chk *upgradeservice.Checker) opsservice.ScheduledJob {
	return opsservice.ScheduledJob{
		Name:       "platform-upgrade-check",
		Expression: "0 * * * *", // hourly, per §25.8 ("the check cron runs hourly")
		Run: func(ctx context.Context) error {
			if !chk.Enabled() {
				return nil
			}
			res, err := chk.Check(ctx)
			if err != nil {
				if errors.Is(err, releasechannel.ErrManifestNotFound) {
					// First check with no advertised release; not an error.
					return nil
				}
				return err
			}
			if res.UpgradeAvailable {
				log.Printf("lenny-ops: platform upgrade available: %s -> %s", res.CurrentVersion, res.AvailableVersion)
			}
			return nil
		},
	}
}

// versionDriftJob is the leader-only cron that runs the §25.8 version
// aggregator hourly so the lenny_platform_version_drift gauge reflects
// the current component-version spread without waiting for an operator
// to call GET /v1/admin/platform/version/full. Aggregate degrades
// gracefully on a source failure, so the job never returns an error.
//
// spec: §25.8 Version Aggregation, Metrics (lenny_platform_version_drift).
func versionDriftJob(agg *upgradeservice.VersionAggregator) opsservice.ScheduledJob {
	return opsservice.ScheduledJob{
		Name:       "platform-version-drift",
		Expression: "0 * * * *", // hourly
		Run: func(ctx context.Context) error {
			report := agg.Aggregate(ctx)
			if report.VersionDrift {
				log.Printf("lenny-ops: platform version drift detected across %d component(s)", report.DriftCount)
			}
			return nil
		},
	}
}
