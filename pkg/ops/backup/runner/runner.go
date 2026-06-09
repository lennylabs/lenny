// SPDX-License-Identifier: MIT

// Package runner is the body of the §25.11 lenny-backup binary: the
// in-Job backup run. lenny-ops does not run backups in-process — it
// creates a Kubernetes Job from the lenny-backup image, and that Job
// invokes Run here. A run performs the §25.11 Full backup flow —
// pg_dump each Postgres shard, export platform configuration and CRDs,
// package a tar archive, encrypt it client-side, checksum it, upload it
// to MinIO — then applies the retention policy and exits.
//
// The run steps are behind interfaces (Dumper, Archiver, Uploader,
// Pruner) so Run is unit-tested without a Postgres server, a Kubernetes
// API, or a MinIO cluster. The lenny-backup binary wires the production
// implementations: pg_dump via os/exec, the K8s API via client-go,
// MinIO via pkg/blobstore/miniostore.
package runner

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"sort"
	"time"

	"github.com/lennylabs/lenny/pkg/backup/retention"
	"github.com/lennylabs/lenny/pkg/gateway/events"
	"github.com/lennylabs/lenny/pkg/observability/audit"
	"github.com/lennylabs/lenny/pkg/ops/backup"
)

// Mode selects which §25.11 backup the run performs.
type Mode string

const (
	// ModeFull runs the §25.11 Full backup flow: Postgres dump, config
	// export, CRD export, archive, encrypt, upload.
	ModeFull Mode = "full"
	// ModePostgres runs the Postgres dump and the encrypt/upload steps
	// only.
	ModePostgres Mode = "postgres"
	// ModeConfig runs the config and CRD export and the encrypt/upload
	// steps only.
	ModeConfig Mode = "config"
)

// validMode reports whether m is one of the three §25.11 modes.
func validMode(m Mode) bool {
	switch m {
	case ModeFull, ModePostgres, ModeConfig:
		return true
	default:
		return false
	}
}

// Component is one part of a backup the run produced: the §25.11
// archive segment for Postgres, config, or CRDs.
type Component struct {
	// Name is the §25.11 component name: "postgres", "config", "crds".
	Name string
	// Bytes is the raw segment content staged for the archive.
	Bytes []byte
}

// Dumper produces the §25.11 backup components. The production
// implementation runs pg_dump against each Postgres shard and reads the
// CRD manifests from the Kubernetes API; a test supplies fixed content.
type Dumper interface {
	// DumpPostgres runs the §25.11 step-1 pg_dump against each Postgres
	// shard and returns the dump as one component. Sensitive tables are
	// excluded per the §25.11 content policy by the implementation.
	DumpPostgres(ctx context.Context) (Component, error)
	// ExportConfig runs the §25.11 step-2 platform-configuration export
	// (runtimes, pools, tenants, quotas) as JSON.
	ExportConfig(ctx context.Context) (Component, error)
	// ExportCRDs runs the §25.11 step-3 CRD-manifest export from the
	// Kubernetes API.
	ExportCRDs(ctx context.Context) (Component, error)
}

// Archive is the §25.11 step-4/5/6 result: the encrypted tar archive
// and its checksum.
type Archive struct {
	// Data is the encrypted archive content (the §25.11 step-5 output).
	Data []byte
	// Checksum is the SHA-256 of the encrypted archive (the §25.11
	// step-6 output).
	Checksum string
	// Encrypted reports whether client-side encryption was applied. When
	// false, the upload relies on server-side encryption.
	Encrypted bool
}

// Archiver packages the §25.11 backup components into a tar archive,
// encrypts it client-side, and computes its checksum. The production
// implementation tars the components and applies AES-256-GCM with a
// KMS-wrapped data key; a test supplies a deterministic implementation.
type Archiver interface {
	// Pack runs the §25.11 step-4/5/6: tar the components, encrypt the
	// archive client-side, and checksum it.
	Pack(ctx context.Context, components []Component) (Archive, error)
}

// Uploader writes the §25.11 step-7 encrypted archive to MinIO.
type Uploader interface {
	// Upload writes the archive to the backup bucket at the §25.11 path
	// backups/{type}/{id}/{timestamp}.tar.gz.enc with server-side
	// encryption, and returns the object path written.
	Upload(ctx context.Context, objectPath string, archive Archive) (string, error)
}

// Pruner deletes the §25.11 expired backups from MinIO. The retention
// policy decision is made by pkg/backup/retention.Plan; the Pruner
// performs the MinIO DeleteObject for each pruned backup.
type Pruner interface {
	// DeleteBackupObject removes a pruned backup's object from MinIO.
	DeleteBackupObject(ctx context.Context, objectPath string) error
}

// Reporter records the §25.11 backup outcome. The production
// implementation updates the ops_backups row in Postgres from inside
// the Job pod (the §25.11 step-8 update); a test inspects the calls.
type Reporter interface {
	// BackupCompleted records the §25.11 step-8 completion: size,
	// checksum, components, and status:completed.
	BackupCompleted(ctx context.Context, result Result) error
	// BackupFailed records a failed backup run with status:failed.
	BackupFailed(ctx context.Context, backupID, errMsg string) error
}

// Config assembles a §25.11 backup run.
type Config struct {
	// BackupID is the ops_backups row this run writes to. Required.
	BackupID string
	// Mode selects which backup the run performs. Required.
	Mode Mode
	// Dumper produces the backup components. Required.
	Dumper Dumper
	// Archiver packages and encrypts the archive. Required.
	Archiver Archiver
	// Uploader writes the archive to MinIO. Required.
	Uploader Uploader
	// Pruner deletes expired backups from MinIO during retention
	// enforcement. Required when RetentionStore is set.
	Pruner Pruner
	// Reporter records the run outcome. Required.
	Reporter Reporter
	// Audit emits the §16.7 backup terminal-state audit events
	// (backup.completed, backup.failed) to the §11.7 platform hash chain
	// as the run transitions the ops_backups row. A nil sink drops the
	// events (dev / no-durable-store mode); the status update still
	// lands. spec: §25.11 line 4343.
	Audit backup.AuditSink
	// OpsEmitter publishes the §25.3 / §16.6 backup_completed and
	// backup_failed operational events to the platform ops:events:stream
	// at the run's terminal transition, the §25.3 line 692-694 "backup
	// job finished / failed" producers in the operational-event
	// catalogue. The backup runs in its own Job pod, so the lenny-backup
	// binary wires a §25.5 Redis StreamEmitter here (mirroring the
	// lenny-controller pool_state_changed emitter) when --redis-url is
	// set; an unconfigured Redis leaves this nil and the run emits no
	// operational event (the durable audit row still lands). spec: §25.3
	// lines 670-694; §16.6 backup_completed / backup_failed.
	OpsEmitter events.EventEmitter
	// RetentionStore supplies the existing backups and the retention
	// policy for the §25.11 post-backup retention enforcement. A nil
	// store skips retention enforcement (the daily-cron Job supplies
	// it; an on-demand backup may not).
	RetentionStore RetentionStore
	// PreRestoreRetainDays is the §25.11 pre-restore retention window; a
	// zero value uses the §25.11 default of 7 days.
	PreRestoreRetainDays int
	// Now supplies the current time; nil uses time.Now in UTC.
	Now func() time.Time
}

// RetentionStore is the read side of the backup store the run consults
// for the §25.11 post-backup retention enforcement.
type RetentionStore interface {
	// RetentionInputs returns the existing backups (for the retention
	// math) and the configured retention policy.
	RetentionInputs(ctx context.Context) ([]RetentionBackup, backup.RetentionPolicy, error)
	// MarkExpired records a pruned backup as expired in the store, the
	// §25.11 coordinated Postgres-then-MinIO retention sequence.
	MarkExpired(ctx context.Context, backupID string) error
}

// RetentionBackup is one backup the §25.11 retention enforcement
// evaluates: enough of an ops_backups row to classify and date it.
type RetentionBackup struct {
	// ID is the ops_backups row id.
	ID string
	// Type is the backup type ("full", "postgres", "config",
	// "pre-restore").
	Type string
	// CreatedAt is when the backup completed.
	CreatedAt time.Time
	// ObjectPath is the MinIO object path the Pruner deletes when the
	// backup is pruned.
	ObjectPath string
}

// Result is the outcome of a §25.11 backup run.
type Result struct {
	// BackupID is the ops_backups row id.
	BackupID string
	// Type is the backup type the run performed.
	Type string
	// SizeBytes is the encrypted archive size.
	SizeBytes int64
	// Checksum is the SHA-256 of the encrypted archive.
	Checksum string
	// StoragePath is the MinIO object path the archive was written to.
	StoragePath string
	// Encrypted reports whether the archive was encrypted client-side.
	Encrypted bool
	// Components lists what the backup covered.
	Components []backup.BackupComponent
	// StartedAt and CompletedAt bound the run.
	StartedAt   time.Time
	CompletedAt time.Time
	// Pruned lists the backup IDs the post-backup retention enforcement
	// removed.
	Pruned []string
}

// Run performs one §25.11 backup run: it produces the backup
// components for the configured mode, packages and encrypts the
// archive, uploads it to MinIO, records the completion, and — when a
// RetentionStore is configured — applies the retention policy. A
// failure at any step records the §25.11 failed status and returns the
// error. Run is the body the lenny-backup binary calls.
func Run(ctx context.Context, cfg Config) (Result, error) {
	if err := validateConfig(cfg); err != nil {
		return Result{}, err
	}
	now := cfg.Now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	startedAt := now()

	components, err := dumpComponents(ctx, cfg)
	if err != nil {
		recordBackupFailed(ctx, cfg, err.Error())
		return Result{}, err
	}

	archive, err := cfg.Archiver.Pack(ctx, components)
	if err != nil {
		recordBackupFailed(ctx, cfg, err.Error())
		return Result{}, fmt.Errorf("pack archive: %w", err)
	}
	// §25.11 step-6 invariant: the checksum is the SHA-256 of the
	// encrypted archive. Recompute and cross-check so a buggy Archiver
	// cannot upload an archive whose recorded checksum does not verify.
	if got := sha256Hex(archive.Data); archive.Checksum != "" && got != archive.Checksum {
		err := fmt.Errorf("archive checksum mismatch: archiver reported %s, content hashes to %s",
			archive.Checksum, got)
		recordBackupFailed(ctx, cfg, err.Error())
		return Result{}, err
	}
	if archive.Checksum == "" {
		archive.Checksum = sha256Hex(archive.Data)
	}

	objectPath := StoragePath(string(cfg.Mode), cfg.BackupID, startedAt)
	storedPath, err := cfg.Uploader.Upload(ctx, objectPath, archive)
	if err != nil {
		recordBackupFailed(ctx, cfg, err.Error())
		return Result{}, fmt.Errorf("upload archive: %w", err)
	}

	result := Result{
		BackupID:    cfg.BackupID,
		Type:        string(cfg.Mode),
		SizeBytes:   int64(len(archive.Data)),
		Checksum:    archive.Checksum,
		StoragePath: storedPath,
		Encrypted:   archive.Encrypted,
		Components:  componentSummary(components),
		StartedAt:   startedAt,
		CompletedAt: now(),
	}
	if err := cfg.Reporter.BackupCompleted(ctx, result); err != nil {
		return Result{}, fmt.Errorf("record completion: %w", err)
	}
	// spec: §25.11 line 4343, §16.7 backup.completed — the durable audit
	// row for the terminal completion transition, written from the Job
	// pod alongside the ops_backups status:completed update.
	emitBackupAudit(cfg.Audit, backup.AuditEvent{
		Type:     string(audit.EventBackupCompleted),
		BackupID: cfg.BackupID,
		Outcome:  "success",
		At:       result.CompletedAt,
		Fields: map[string]any{
			"type":        result.Type,
			"sizeBytes":   result.SizeBytes,
			"checksum":    result.Checksum,
			"storagePath": result.StoragePath,
			"durationMs":  result.CompletedAt.Sub(result.StartedAt).Milliseconds(),
			"components":  len(result.Components),
		},
	})
	// spec: §25.3 line 692 / §16.6 backup_completed — the operational
	// event an ops agent subscribes to, emitted at the same terminal
	// transition as the audit row.
	emitBackupCompleted(ctx, cfg.OpsEmitter, result)

	// §25.11: after a successful backup, lenny-ops evaluates the
	// retention policy and deletes expired backups. The daily-cron Job
	// supplies a RetentionStore; an on-demand backup may not.
	if cfg.RetentionStore != nil {
		pruned, err := enforceRetention(ctx, cfg, now())
		if err != nil {
			// A retention failure does not fail the backup — the backup
			// itself completed. Surface it on the result for the caller to
			// log.
			return result, fmt.Errorf("retention enforcement: %w", err)
		}
		result.Pruned = pruned
	}
	return result, nil
}

// validateConfig checks the §25.11 run configuration.
func validateConfig(cfg Config) error {
	if cfg.BackupID == "" {
		return errors.New("runner: BackupID is required")
	}
	if !validMode(cfg.Mode) {
		return fmt.Errorf("runner: invalid mode %q", cfg.Mode)
	}
	if cfg.Dumper == nil || cfg.Archiver == nil || cfg.Uploader == nil || cfg.Reporter == nil {
		return errors.New("runner: Dumper, Archiver, Uploader, and Reporter are required")
	}
	if cfg.RetentionStore != nil && cfg.Pruner == nil {
		return errors.New("runner: a Pruner is required when RetentionStore is set")
	}
	return nil
}

// dumpComponents produces the §25.11 backup components for the
// configured mode: a full backup covers Postgres, config, and CRDs; a
// postgres-only backup covers Postgres; a config-only backup covers
// config and CRDs.
func dumpComponents(ctx context.Context, cfg Config) ([]Component, error) {
	var components []Component
	if cfg.Mode == ModeFull || cfg.Mode == ModePostgres {
		c, err := cfg.Dumper.DumpPostgres(ctx)
		if err != nil {
			return nil, fmt.Errorf("dump postgres: %w", err)
		}
		c.Name = "postgres"
		components = append(components, c)
	}
	if cfg.Mode == ModeFull || cfg.Mode == ModeConfig {
		cfgComp, err := cfg.Dumper.ExportConfig(ctx)
		if err != nil {
			return nil, fmt.Errorf("export config: %w", err)
		}
		cfgComp.Name = "config"
		components = append(components, cfgComp)

		crds, err := cfg.Dumper.ExportCRDs(ctx)
		if err != nil {
			return nil, fmt.Errorf("export crds: %w", err)
		}
		crds.Name = "crds"
		components = append(components, crds)
	}
	return components, nil
}

// enforceRetention applies the §25.11 retention policy after a backup
// run: it computes the prune set with pkg/backup/retention.Plan, marks
// each pruned backup expired in the store, then deletes its MinIO
// object. The store update precedes the MinIO delete so a crash between
// them leaves an expired row whose object the next run cleans up,
// rather than a missing object with a live row.
func enforceRetention(ctx context.Context, cfg Config, now time.Time) ([]string, error) {
	backups, policy, err := cfg.RetentionStore.RetentionInputs(ctx)
	if err != nil {
		return nil, fmt.Errorf("read retention inputs: %w", err)
	}
	byID := make(map[string]RetentionBackup, len(backups))
	records := make([]retention.Backup, 0, len(backups))
	for _, b := range backups {
		byID[b.ID] = b
		records = append(records, retention.Backup{
			ID:        b.ID,
			Kind:      retentionKind(b.Type),
			CreatedAt: b.CreatedAt,
		})
	}
	preDays := cfg.PreRestoreRetainDays
	if preDays <= 0 {
		preDays = 7
	}
	_, prune := retention.Plan(records, retention.Policy{
		RetainDays:           policy.RetainDays,
		RetainCount:          policy.RetainCount,
		RetainMinFull:        policy.RetainMinFull,
		PreRestoreRetainDays: preDays,
	}, now)

	pruned := make([]string, 0, len(prune))
	for _, p := range prune {
		if err := cfg.RetentionStore.MarkExpired(ctx, p.ID); err != nil {
			return pruned, fmt.Errorf("mark backup %s expired: %w", p.ID, err)
		}
		if b, ok := byID[p.ID]; ok && b.ObjectPath != "" {
			if err := cfg.Pruner.DeleteBackupObject(ctx, b.ObjectPath); err != nil {
				return pruned, fmt.Errorf("delete backup object %s: %w", b.ObjectPath, err)
			}
		}
		pruned = append(pruned, p.ID)
	}
	sort.Strings(pruned)
	return pruned, nil
}

// retentionKind maps a backup type to its pkg/backup/retention Kind.
func retentionKind(backupType string) retention.Kind {
	switch backupType {
	case "pre-restore":
		return retention.KindPreRestore
	case "postgres":
		return retention.KindPostgres
	default:
		return retention.KindFull
	}
}

// componentSummary projects the run's components into the §25.11
// BackupComponent summary recorded on the ops_backups row.
func componentSummary(components []Component) []backup.BackupComponent {
	out := make([]backup.BackupComponent, 0, len(components))
	for _, c := range components {
		out = append(out, backup.BackupComponent{
			Name:      c.Name,
			Status:    backup.StatusCompleted,
			SizeBytes: int64(len(c.Bytes)),
		})
	}
	return out
}

// StoragePath returns the §25.11 MinIO object path for a backup:
// backups/{type}/{id}/{timestamp}.tar.gz.enc.
func StoragePath(backupType, backupID string, ts time.Time) string {
	return fmt.Sprintf("backups/%s/%s/%s.tar.gz.enc",
		backupType, backupID, ts.UTC().Format("20060102T150405Z"))
}

// sha256Hex returns the lowercase hex SHA-256 of data.
func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// HashReader returns the lowercase hex SHA-256 of everything readable
// from r. The lenny-backup binary uses it to checksum a streamed
// archive without buffering the whole archive in memory.
func HashReader(r io.Reader) (string, error) {
	h := sha256.New()
	if _, err := io.Copy(h, r); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
