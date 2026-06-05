// SPDX-License-Identifier: MIT

package runner

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/lennylabs/lenny/pkg/observability/audit"
	"github.com/lennylabs/lenny/pkg/ops/backup"
	"github.com/lennylabs/lenny/pkg/ops/backup/restoretest"
)

// This file implements the §25.11 read-side backup operations the
// lenny-backup binary performs besides taking a backup: Backup
// Verification (download, checksum, pg_restore --list) and Test Restore
// (the monthly lenny-restore-test CronJob — verification plus an actual
// restore into a scratch Postgres and a sampled-HEAD ArtifactStore
// check). Both run behind interfaces so the orchestration is unit-tested
// without MinIO, pg_restore, or a Postgres server; the lenny-backup
// binary wires the production implementations.
//
// spec: §25.11 lines 4098, 4128-4133, 4254-4256.

// artifactSuccessFloor is the §25.11 line 4098 sampled-HEAD success
// floor: a sampled ArtifactStore success rate below 99% fails the
// restore test (sets lenny_restore_test_success = 0).
const artifactSuccessFloor = 0.99

var (
	// ErrChecksumMismatch is returned by RunVerify when the downloaded
	// archive's SHA-256 does not match the recorded checksum (§25.11
	// Backup Verification step 2). It maps to BACKUP_VERIFICATION_FAILED.
	ErrChecksumMismatch = errors.New("runner: backup archive checksum mismatch")
	// ErrDumpUnreadable is returned by RunVerify when pg_restore --list
	// fails on a shard dump (§25.11 Backup Verification step 3). It maps
	// to BACKUP_VERIFICATION_FAILED.
	ErrDumpUnreadable = errors.New("runner: backup Postgres dump is unreadable")
)

// Target identifies the backup a verify or restore-test run operates on.
type Target struct {
	// BackupID is the ops_backups row id.
	BackupID string
	// BackupType is the backup type ("full", "postgres", "config").
	BackupType string
	// ObjectPath is the MinIO object path of the archive.
	ObjectPath string
	// Checksum is the recorded SHA-256 of the encrypted archive.
	Checksum string
}

// BackupResolver resolves the backup a run targets. Verify names a
// specific backup id; restore-test selects the latest backup matching
// the §25.11 backups.restoreTest.backupSelector.
type BackupResolver interface {
	// Resolve returns the backup with the given id.
	Resolve(ctx context.Context, backupID string) (Target, error)
	// ResolveLatest returns the most recent completed backup matching
	// the selector (a backup type). The boolean is false when no backup
	// matches, so the restore-test run records a failure rather than
	// silently no-opping.
	ResolveLatest(ctx context.Context, selector string) (Target, bool, error)
}

// Downloader fetches a backup archive's bytes from MinIO.
type Downloader interface {
	Download(ctx context.Context, objectPath string) ([]byte, error)
}

// ArchiveOpener decrypts, decompresses, and untars the §25.11 archive
// and returns each shard's Postgres custom-format dump. A config-only
// archive yields no dumps.
type ArchiveOpener interface {
	ExtractPostgresDumps(ctx context.Context, archive []byte) ([][]byte, error)
}

// DumpInspector runs pg_restore --list over one shard dump to prove the
// dump is readable without restoring data (§25.11 Backup Verification
// step 3).
type DumpInspector interface {
	ListDump(ctx context.Context, dump []byte) error
}

// ScratchRestorer restores the shard dumps into a scratch Postgres and
// runs the §25.11 Test Restore smoke check. A nil ScratchRestorer makes
// a restore-test run verify readability only (no scratch DSN wired).
type ScratchRestorer interface {
	RestoreAndSmoke(ctx context.Context, dumps [][]byte) error
}

// ArtifactSampler performs the §25.11 line 4098 sampled-HEAD
// ArtifactStore check against the replication target. It returns how
// many of the sampled object keys were present and how many were
// sampled. A nil ArtifactSampler skips the check.
type ArtifactSampler interface {
	SampleHeads(ctx context.Context, sampleSize int) (present, sampled int, err error)
}

// VerifyReporter records the §25.11 Backup Verification outcome on the
// ops_backups row: status verified or verification_failed.
type VerifyReporter interface {
	MarkVerified(ctx context.Context, backupID string) error
	MarkVerificationFailed(ctx context.Context, backupID, reason string) error
}

// VerifyConfig assembles a §25.11 Backup Verification run.
type VerifyConfig struct {
	// BackupID is the backup to verify. Required.
	BackupID string
	// Resolver, Downloader, Opener, Inspector, and Reporter are the
	// verification seams. All required.
	Resolver   BackupResolver
	Downloader Downloader
	Opener     ArchiveOpener
	Inspector  DumpInspector
	Reporter   VerifyReporter
	// Audit emits the §16.7 backup.verified audit event to the §11.7
	// platform hash chain when the verification succeeds. A nil sink
	// drops the event; the ops_backups status:verified update still
	// lands. A verification failure has no §16.7 catalog event — it is
	// surfaced through the status:verification_failed transition and the
	// §25.11 restore-test gauges — so no audit row is written on failure.
	// spec: §25.11 line 4343.
	Audit backup.AuditSink
}

// RunVerify performs one §25.11 Backup Verification: download the
// archive, validate its SHA-256, run pg_restore --list on each Postgres
// shard dump, and record verified or verification_failed. It returns nil
// when the backup verifies, ErrChecksumMismatch or ErrDumpUnreadable
// when the archive is corrupt, or the underlying error on an
// infrastructure failure. The ops_backups status is moved to
// verification_failed on every failure path so an operator sees the
// outcome.
//
// spec: §25.11 lines 4128-4133.
func RunVerify(ctx context.Context, cfg VerifyConfig) error {
	if cfg.BackupID == "" {
		return errors.New("runner: verify requires a BackupID")
	}
	if cfg.Resolver == nil || cfg.Downloader == nil || cfg.Opener == nil ||
		cfg.Inspector == nil || cfg.Reporter == nil {
		return errors.New("runner: verify requires Resolver, Downloader, Opener, Inspector, and Reporter")
	}
	target, err := cfg.Resolver.Resolve(ctx, cfg.BackupID)
	if err != nil {
		return fmt.Errorf("resolve backup %s: %w", cfg.BackupID, err)
	}
	failed := func(reason string, retErr error) error {
		if mErr := cfg.Reporter.MarkVerificationFailed(ctx, cfg.BackupID, reason); mErr != nil {
			return fmt.Errorf("%w (and record verification_failed: %v)", retErr, mErr)
		}
		return retErr
	}

	data, err := cfg.Downloader.Download(ctx, target.ObjectPath)
	if err != nil {
		return failed("download: "+err.Error(), fmt.Errorf("download archive: %w", err))
	}
	if target.Checksum != "" {
		if got := sha256Hex(data); got != target.Checksum {
			return failed(
				fmt.Sprintf("checksum mismatch: recorded %s, archive hashes to %s", target.Checksum, got),
				ErrChecksumMismatch)
		}
	}
	dumps, err := cfg.Opener.ExtractPostgresDumps(ctx, data)
	if err != nil {
		return failed("open archive: "+err.Error(), fmt.Errorf("open archive: %w", err))
	}
	for i, d := range dumps {
		if err := cfg.Inspector.ListDump(ctx, d); err != nil {
			return failed(
				fmt.Sprintf("pg_restore --list shard %d: %s", i, err.Error()),
				fmt.Errorf("%w: shard %d: %v", ErrDumpUnreadable, i, err))
		}
	}
	if err := cfg.Reporter.MarkVerified(ctx, cfg.BackupID); err != nil {
		return fmt.Errorf("record verified: %w", err)
	}
	// spec: §25.11 line 4343, §16.7 backup.verified — the durable audit
	// row for the successful verification, written from the Job pod
	// alongside the ops_backups status:verified update.
	emitBackupAudit(cfg.Audit, backup.AuditEvent{
		Type:     string(audit.EventBackupVerified),
		BackupID: cfg.BackupID,
		Outcome:  "success",
	})
	return nil
}

// RestoreTestConfig assembles a §25.11 Test Restore run.
type RestoreTestConfig struct {
	// JobID is the result row id (the Kubernetes Job name). Required.
	JobID string
	// Selector is the §25.11 backups.restoreTest.backupSelector: the
	// backup type whose latest member the run restores. Empty selects
	// the latest backup of any type.
	Selector string
	// ArtifactSampleSize is the §25.11 backups.verification.artifactSampleSize
	// number of object keys the sampled-HEAD check samples. Zero skips
	// the ArtifactStore check.
	ArtifactSampleSize int
	// Resolver, Downloader, Opener, Inspector, and Store are required.
	Resolver   BackupResolver
	Downloader Downloader
	Opener     ArchiveOpener
	Inspector  DumpInspector
	// Restorer performs the actual scratch restore and smoke check.
	// Optional: nil verifies readability only.
	Restorer ScratchRestorer
	// Sampler performs the sampled-HEAD ArtifactStore check. Optional:
	// nil skips it.
	Sampler ArtifactSampler
	// Store records the run outcome.
	Store restoretest.Store
	// Now supplies the current time; nil uses time.Now in UTC.
	Now func() time.Time
}

// RunRestoreTest performs one §25.11 Test Restore: select the latest
// backup matching the selector, download and checksum it, run
// pg_restore --list on each shard, restore into a scratch Postgres (when
// a Restorer is wired), sample the ArtifactStore replication target, and
// record the outcome. It records a result on every path — including
// failures — so the lenny-ops sampler always has a current
// lenny_restore_test_success value rather than a stale one. The returned
// error is non-nil only on an infrastructure failure that prevented
// recording (e.g. the Store write itself failed); a failed restore test
// returns a Result with Success=false and a nil error.
//
// spec: §25.11 lines 4098, 4254-4256.
func RunRestoreTest(ctx context.Context, cfg RestoreTestConfig) (restoretest.Result, error) {
	if cfg.JobID == "" {
		return restoretest.Result{}, errors.New("runner: restore-test requires a JobID")
	}
	if cfg.Resolver == nil || cfg.Downloader == nil || cfg.Opener == nil ||
		cfg.Inspector == nil || cfg.Store == nil {
		return restoretest.Result{}, errors.New(
			"runner: restore-test requires Resolver, Downloader, Opener, Inspector, and Store")
	}
	now := cfg.Now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	started := now()

	// record finalizes and persists a result, returning it (and any
	// Store error) to the caller.
	record := func(r restoretest.Result) (restoretest.Result, error) {
		r.ID = cfg.JobID
		r.StartedAt = started
		r.CompletedAt = now()
		if err := cfg.Store.Record(ctx, r); err != nil {
			return r, fmt.Errorf("record restore-test result: %w", err)
		}
		return r, nil
	}
	fail := func(backupID, backupType, reason string) (restoretest.Result, error) {
		return record(restoretest.Result{
			BackupID: backupID, BackupType: backupType, Success: false, Error: reason,
		})
	}

	target, ok, err := cfg.Resolver.ResolveLatest(ctx, cfg.Selector)
	if err != nil {
		return fail("", "", "resolve latest backup: "+err.Error())
	}
	if !ok {
		return fail("", "", "no backup matched the restore-test selector "+selectorLabel(cfg.Selector))
	}
	data, err := cfg.Downloader.Download(ctx, target.ObjectPath)
	if err != nil {
		return fail(target.BackupID, target.BackupType, "download: "+err.Error())
	}
	if target.Checksum != "" {
		if got := sha256Hex(data); got != target.Checksum {
			return fail(target.BackupID, target.BackupType,
				fmt.Sprintf("checksum mismatch: recorded %s, archive hashes to %s", target.Checksum, got))
		}
	}
	dumps, err := cfg.Opener.ExtractPostgresDumps(ctx, data)
	if err != nil {
		return fail(target.BackupID, target.BackupType, "open archive: "+err.Error())
	}
	for i, d := range dumps {
		if err := cfg.Inspector.ListDump(ctx, d); err != nil {
			return fail(target.BackupID, target.BackupType,
				fmt.Sprintf("pg_restore --list shard %d: %s", i, err.Error()))
		}
	}
	if cfg.Restorer != nil {
		if err := cfg.Restorer.RestoreAndSmoke(ctx, dumps); err != nil {
			return fail(target.BackupID, target.BackupType, "scratch restore: "+err.Error())
		}
	}

	result := restoretest.Result{
		BackupID:            target.BackupID,
		BackupType:          target.BackupType,
		Success:             true,
		ArtifactSuccessRate: 1.0,
	}
	if cfg.Sampler != nil && cfg.ArtifactSampleSize > 0 {
		present, sampled, err := cfg.Sampler.SampleHeads(ctx, cfg.ArtifactSampleSize)
		if err != nil {
			return fail(target.BackupID, target.BackupType, "artifact sample: "+err.Error())
		}
		if sampled > 0 {
			result.ArtifactChecked = true
			result.ArtifactSampled = sampled
			result.ArtifactPresent = present
			result.ArtifactMissing = sampled - present
			result.ArtifactSuccessRate = float64(present) / float64(sampled)
			if result.ArtifactSuccessRate < artifactSuccessFloor {
				result.Success = false
				result.Error = fmt.Sprintf(
					"artifact success rate %.4f below the §25.11 %.2f floor (%d of %d sampled objects present)",
					result.ArtifactSuccessRate, artifactSuccessFloor, present, sampled)
			}
		}
	}
	return record(result)
}

// selectorLabel renders the restore-test selector for an error message,
// naming the any-type case explicitly.
func selectorLabel(selector string) string {
	if selector == "" {
		return "(any type)"
	}
	return selector
}
