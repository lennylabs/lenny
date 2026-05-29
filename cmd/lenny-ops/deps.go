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

	"github.com/redis/go-redis/v9"

	"github.com/lennylabs/lenny/pkg/gateway/events"
	"github.com/lennylabs/lenny/pkg/ops/backup"
	"github.com/lennylabs/lenny/pkg/ops/diagnostics"
	"github.com/lennylabs/lenny/pkg/ops/driftservice"
	"github.com/lennylabs/lenny/pkg/ops/escalation"
	opsstream "github.com/lennylabs/lenny/pkg/ops/events"
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
	}
	return svc, jobs
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

// logEmitter is the §25.4 escalation Emitter used until the §25.5 event
// stream is wired into lenny-ops. Creating an escalation must emit an
// escalation_created event to the Redis stream and the gateway buffer;
// until those destinations are wired the emission is logged and
// reported as not-yet-delivered, so the escalation's emitted flag stays
// false and the §25.4 background retry re-attempts the publish once a
// destination exists.
type logEmitter struct{}

// EmitEscalationCreated logs the §25.4 escalation_created event and
// reports false: with no event-stream destination wired the publish has
// not been delivered, so the escalation stays un-emitted for the retry.
func (logEmitter) EmitEscalationCreated(esc escalation.Escalation) bool {
	log.Printf("lenny-ops: escalation %s created (severity=%s) — escalation_created emission "+
		"pending the event-stream wiring", esc.ID, esc.Severity)
	return false
}

// buildEscalationService constructs the §25.4 escalation service over
// the in-memory Tier 3 buffer. The Postgres ops_escalations table and
// the Redis tier are documented seams: until they land the service runs
// the in-memory buffer so the §25.4 escalation endpoints serve and an
// agent can exercise them in a single-process degraded mode.
func buildEscalationService() *escalation.Service {
	return escalation.NewService(logEmitter{})
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
