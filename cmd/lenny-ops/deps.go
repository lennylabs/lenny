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
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/redis/go-redis/v9"

	"github.com/lennylabs/lenny/pkg/gateway/events"
	"github.com/lennylabs/lenny/pkg/observability/metrics"
	"github.com/lennylabs/lenny/pkg/ops/auditrate"
	"github.com/lennylabs/lenny/pkg/ops/backup"
	"github.com/lennylabs/lenny/pkg/ops/diagnostics"
	"github.com/lennylabs/lenny/pkg/ops/driftservice"
	"github.com/lennylabs/lenny/pkg/ops/escalation"
	opsstream "github.com/lennylabs/lenny/pkg/ops/events"
	"github.com/lennylabs/lenny/pkg/ops/opsidem"
	idempgstore "github.com/lennylabs/lenny/pkg/ops/opsidem/pgstore"
	"github.com/lennylabs/lenny/pkg/ops/opsserver"
	"github.com/lennylabs/lenny/pkg/ops/opsservice"
	"github.com/lennylabs/lenny/pkg/releasechannel"
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
	stream *events.StreamEmitter
	local  *opsstream.Service
}

// newRedisFanOutEmitter constructs an emitter that writes every event
// to client and tees a copy through local.Publish so the local SSE
// subscribers and polling cursor see it without depending on a
// separate Redis consumer loop.
func newRedisFanOutEmitter(client redis.UniversalClient, local *opsstream.Service, replicaID string) *redisFanOutEmitter {
	stream := events.NewStreamEmitter(events.StreamEmitterOptions{
		Client: client,
		// The local buffer the StreamEmitter writes through is the same
		// data structure the opsstream.Service maintains; using a separate
		// buffer would split the read view. The opsstream.Service Publish
		// path covers the local-buffer write, so the stream emitter
		// here only needs a private buffer to satisfy its non-nil
		// requirement — the Publish below is the canonical local
		// delivery.
		Buffer:    events.NewEventBuffer(0),
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

// buildBackupService constructs the §25.11 BackupService and the
// scheduled-backup cron jobs the §25.4 cron evaluator runs.
//
// The §25.11 ops_backups Postgres store and the Kubernetes Job launcher
// are documented seams: the schema migration and the client-go Job
// launcher are wired as those land. Until then lenny-ops runs the
// in-memory store and launcher, so the §25.11 endpoints serve and an
// agent can exercise the API surface in a single-process degraded
// mode. The scheduled-backup loop runs only on the leader replica.
func buildBackupService(production bool) (*backup.Service, []opsservice.ScheduledJob) {
	store := backup.NewMemStore()
	svc, err := backup.NewService(backup.Config{
		Store:    store,
		Launcher: backup.NewFakeLauncher(),
		Locker:   backup.NewMemLocker(),
		// §25.11 line 4343: every backup/restore/retention transition is
		// audited. The orchestrator emits to this sink; the durable
		// audit-append destination is a documented seam (lenny-ops has no
		// audit-store client in this single-process mode), so the sink
		// logs the event until that path lands, matching the escalation
		// logEmitter posture.
		Audit: logAuditSink,
	})
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
			Name:       "backup-postgres",
			Expression: "0 */6 * * *",
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
			Name:       "backup-retention",
			Expression: "30 3 * * *",
			Run: func(ctx context.Context) error {
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
	}
	return svc, jobs
}

// diagnosticsAuditRateLimited is the §25.9 lenny_audit_rate_limited_total
// counter (event_type, service_account). It is registered on the default
// registry so the rate-limit seam increments a real counter; lenny-ops
// gains its own /metrics exposition in a later commit (the same
// documented exposition gap the pgaudit shipper notes).
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
func buildDiagnosticsAudit(ratePerMinute int) *opsserver.DiagnosticsAuditConfig {
	return &opsserver.DiagnosticsAuditConfig{
		RatePerMinute: ratePerMinute,
		Emit: func(ev auditrate.Event) {
			log.Printf("lenny-ops: audit %s resource=%s/%s invocationCount=%d service_account=%s operation_id=%s — "+
				"audit-append destination pending the audit-store wiring",
				ev.EventType, ev.ResourceType, ev.ResourceID, ev.InvocationCount, ev.ServiceAccount, ev.OperationID)
		},
		RateLimited: func(eventType, serviceAccount string) {
			if diagnosticsAuditRateLimited != nil {
				diagnosticsAuditRateLimited.WithLabelValues(eventType, serviceAccount).Inc()
			}
		},
	}
}

// logAuditSink is the §25.11 backup/restore AuditSink used until the
// lenny-ops audit-append path is wired. The orchestrator emits an audit
// event on every state transition it owns (§25.11 line 4343); with no
// audit-store client in this single-process mode the event is logged so
// the emission is observable, matching the escalation logEmitter
// posture. A future commit that wires the audit-append client swaps
// this sink for one that writes the §11.7 append-only row.
func logAuditSink(ev backup.AuditEvent) {
	outcome := ev.Outcome
	if outcome == "" {
		outcome = "success"
	}
	log.Printf("lenny-ops: audit %s outcome=%s backup=%s restore=%s actor=%s — "+
		"audit-append destination pending the audit-store wiring",
		ev.Type, outcome, ev.BackupID, ev.RestoreID, ev.Actor)
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

// buildEscalationService constructs the §25.4 escalation service over
// the in-memory Tier 3 buffer, wired to emitter for the §25.17
// escalation_created event stream. The Postgres ops_escalations table
// and the Redis tier are documented seams: until they land the service
// runs the in-memory buffer so the §25.4 escalation endpoints serve and
// an agent can exercise them in a single-process degraded mode.
func buildEscalationService(emitter escalation.Emitter) *escalation.Service {
	return escalation.NewService(emitter)
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
// The Postgres-backed bootstrap_seed_snapshot store and the
// gateway-client running-state reader are documented seams: until they
// land the service runs the in-memory snapshot store and an empty
// running-state reader, so the §25.10 drift endpoints serve and an
// agent can exercise the validate and snapshot-refresh paths in a
// single-process degraded mode.
//
// The §25.10 line 3822 running-state cache is wired here over the
// in-memory MemRunningStateCache. A non-positive TTL disables caching,
// matching the §25.10 line 3824 "0 disables" posture. F-25.10.7, F-25.10.9.
func buildDriftService(cfg driftServiceConfig) *driftservice.Service {
	svc := driftservice.NewService(driftservice.NewMemSnapshotStore(), emptyRunningState{})
	svc.StaleWarningDays = cfg.StaleWarningDays
	if cfg.RunningStateCacheTTLSec > 0 {
		svc.SetRunningStateCache(driftservice.NewMemRunningStateCache(
			time.Duration(cfg.RunningStateCacheTTLSec) * time.Second))
	}
	return svc
}

// emptyRunningState is the §25.10 RunningStateReader used until the
// gateway-client running-state collector is wired. It reports an empty
// running state, so a drift report against a stored snapshot shows
// every desired field as removed — the correct cold-start behavior
// before a gateway connection exists.
type emptyRunningState struct{}

// RunningState returns an empty running state.
func (emptyRunningState) RunningState(context.Context, string) (map[string]any, error) {
	return map[string]any{}, nil
}

// buildDiagnosticService constructs the §25.6 DiagnosticService. The
// Postgres + Kubernetes API data source is a documented seam: until it
// lands the service runs the unconfigured data source, which reports
// sessions and pools as not-found. The §25.6 connectivity endpoint runs
// from the dependency probes regardless, so connectivity diagnosis
// works even in this degraded mode.
func buildDiagnosticService() diagnostics.DiagnosticService {
	return diagnostics.NewService(unconfiguredDiagnosticSource{})
}

// unconfiguredDiagnosticSource is the §25.6 DataSource used until the
// Postgres + Kubernetes API source is wired. It reports every session,
// pool, and credential pool as not-found and runs no connectivity
// probes — the correct cold-start behavior before a data source exists.
type unconfiguredDiagnosticSource struct{}

func (unconfiguredDiagnosticSource) Session(context.Context, string) (diagnostics.SessionRecord, error) {
	return diagnostics.SessionRecord{Found: false}, nil
}

func (unconfiguredDiagnosticSource) Pool(context.Context, string) (diagnostics.PoolRecord, error) {
	return diagnostics.PoolRecord{Found: false}, nil
}

func (unconfiguredDiagnosticSource) CredentialPool(context.Context, string) (diagnostics.CredentialPoolRecord, error) {
	return diagnostics.CredentialPoolRecord{Found: false}, nil
}

func (unconfiguredDiagnosticSource) Connectivity(context.Context) ([]diagnostics.ConnectivityDependency, error) {
	return nil, nil
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
