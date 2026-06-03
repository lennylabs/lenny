// SPDX-License-Identifier: MIT

// Package pgstore is the Postgres-backed §25.11 backup Store. It persists
// the ops_backups, ops_backup_schedule, ops_retention_policy, and
// ops_restore_state tables (migrations 0123 + 0127) so the §25.11 backup
// and restore pipeline survives a lenny-ops restart and coordinates
// across replicas. Without it lenny-ops runs the in-memory backup.MemStore,
// which loses every recorded backup, schedule edit, and restore on the
// next restart of the replica that served the API call (F-17.3.4 /
// F-25.11.3).
//
// The store also closes the seam the §25.11 reconciler depends on: the
// lenny-backup Job pod writes the row's completion fields directly to
// Postgres (cmd/lenny-backup/reporter.go), and lenny-ops reads them back
// through this store. The in-memory store could never observe those
// out-of-process writes.
//
// All four tables are platform-scoped (the §25 control plane is not
// multi-tenanted at this boundary; §25.4 line 1492 lists them among the
// PlatformPostgres() tables), so the store does not run inside a
// tenant-scoped transaction and the tables carry no RLS policy.
//
// spec: §25.11 lines 3963-4295.
package pgstore

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lennylabs/lenny/pkg/ops/backup"
)

// Store is the Postgres-backed §25.11 backup Store. Construct with New.
type Store struct {
	pool *pgxpool.Pool
}

// New returns a Store backed by pool. The pool must point at a database
// with the migrations/ schema applied.
func New(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// Compile-time guards: the Store satisfies the §25.11 Store contract and
// the optional reconciler/state seams.
var (
	_ backup.Store             = (*Store)(nil)
	_ backup.PendingReconciler = (*Store)(nil)
)

// backupColumns is the ops_backups projection shared by every read. The
// nullable numeric/text/timestamp columns are COALESCE'd to their zero
// value so the scan targets are plain Go values; completed_at and
// expires_at remain nullable because the orchestrator distinguishes
// "not yet completed" from a zero time.
const backupColumns = `id, type, status, started_at, completed_at,
	COALESCE(size_bytes, 0), COALESCE(duration_ms, 0), COALESCE(storage_path, ''),
	COALESCE(checksum, ''), components, started_by, COALESCE(operation_id, ''),
	job_id, COALESCE(error, ''), platform_version, schema_version, expires_at`

// InsertBackup writes a new ops_backups row.
func (s *Store) InsertBackup(ctx context.Context, b backup.Backup) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO ops_backups
			(id, type, status, started_at, completed_at, size_bytes, duration_ms,
			 storage_path, checksum, components, started_by, operation_id, job_id,
			 error, platform_version, schema_version, expires_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17)`,
		backupArgs(b)...)
	return err
}

// UpdateBackup overwrites the ops_backups row identified by b.ID. Every
// column is rewritten from b, so the caller must load the row through
// GetBackup before mutating it — otherwise the Job-written completion
// fields (size, duration, checksum, components) would be clobbered. The
// orchestrator follows that read-modify-write discipline.
func (s *Store) UpdateBackup(ctx context.Context, b backup.Backup) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE ops_backups SET
			type=$2, status=$3, started_at=$4, completed_at=$5, size_bytes=$6,
			duration_ms=$7, storage_path=$8, checksum=$9, components=$10,
			started_by=$11, operation_id=$12, job_id=$13, error=$14,
			platform_version=$15, schema_version=$16, expires_at=$17
		WHERE id=$1`, backupArgs(b)...)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return backup.ErrNotFound
	}
	return nil
}

// GetBackup reads one ops_backups row.
func (s *Store) GetBackup(ctx context.Context, id string) (backup.Backup, error) {
	b, err := scanBackup(s.pool.QueryRow(ctx,
		`SELECT `+backupColumns+` FROM ops_backups WHERE id=$1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return backup.Backup{}, backup.ErrNotFound
	}
	if err != nil {
		return backup.Backup{}, err
	}
	return b, nil
}

// ListBackups reads ops_backups rows matching filter, ordered newest-first
// by started_at with ID descending as the tie-break so the §25.11 cursor
// pagination is stable.
func (s *Store) ListBackups(ctx context.Context, filter backup.BackupFilter) ([]backup.Backup, error) {
	query := `SELECT ` + backupColumns + ` FROM ops_backups`
	var conds []string
	var args []any
	if filter.Type != "" {
		args = append(args, filter.Type)
		conds = append(conds, "type = $"+itoa(len(args)))
	}
	if filter.Status != "" {
		args = append(args, filter.Status)
		conds = append(conds, "status = $"+itoa(len(args)))
	}
	if !filter.Since.IsZero() {
		args = append(args, filter.Since)
		conds = append(conds, "started_at >= $"+itoa(len(args)))
	}
	if !filter.Until.IsZero() {
		args = append(args, filter.Until)
		conds = append(conds, "started_at <= $"+itoa(len(args)))
	}
	if len(conds) > 0 {
		query += " WHERE " + joinAnd(conds)
	}
	query += " ORDER BY started_at DESC, id DESC"
	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []backup.Backup
	for rows.Next() {
		b, err := scanBackup(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// GetSchedule reads the ops_backup_schedule singleton, seeding the
// §25.11 default schedule when the row is absent (a fresh database).
func (s *Store) GetSchedule(ctx context.Context) (backup.BackupSchedule, error) {
	var sc backup.BackupSchedule
	err := s.pool.QueryRow(ctx,
		`SELECT full_cron, pg_cron, enabled FROM ops_backup_schedule WHERE id='singleton'`).
		Scan(&sc.Full, &sc.Postgres, &sc.Enabled)
	if errors.Is(err, pgx.ErrNoRows) {
		return backup.BackupSchedule{Full: "0 2 * * *", Postgres: "0 */6 * * *", Enabled: true}, nil
	}
	if err != nil {
		return backup.BackupSchedule{}, err
	}
	return sc, nil
}

// PutSchedule upserts the ops_backup_schedule singleton.
func (s *Store) PutSchedule(ctx context.Context, sc backup.BackupSchedule) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO ops_backup_schedule (id, full_cron, pg_cron, enabled, updated_at)
		VALUES ('singleton', $1, $2, $3, now())
		ON CONFLICT (id) DO UPDATE SET
			full_cron=EXCLUDED.full_cron, pg_cron=EXCLUDED.pg_cron,
			enabled=EXCLUDED.enabled, updated_at=now()`,
		sc.Full, sc.Postgres, sc.Enabled)
	return err
}

// GetPolicy reads the ops_retention_policy singleton, seeding the Tier-2
// default policy when the row is absent.
func (s *Store) GetPolicy(ctx context.Context) (backup.RetentionPolicy, error) {
	var p backup.RetentionPolicy
	err := s.pool.QueryRow(ctx,
		`SELECT retain_days, retain_count, retain_min_full FROM ops_retention_policy WHERE id='singleton'`).
		Scan(&p.RetainDays, &p.RetainCount, &p.RetainMinFull)
	if errors.Is(err, pgx.ErrNoRows) {
		return backup.RetentionPolicy{RetainDays: 30, RetainCount: 10, RetainMinFull: 3}, nil
	}
	if err != nil {
		return backup.RetentionPolicy{}, err
	}
	return p, nil
}

// PutPolicy upserts the ops_retention_policy singleton.
func (s *Store) PutPolicy(ctx context.Context, p backup.RetentionPolicy) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO ops_retention_policy (id, retain_days, retain_count, retain_min_full, updated_at)
		VALUES ('singleton', $1, $2, $3, now())
		ON CONFLICT (id) DO UPDATE SET
			retain_days=EXCLUDED.retain_days, retain_count=EXCLUDED.retain_count,
			retain_min_full=EXCLUDED.retain_min_full, updated_at=now()`,
		p.RetainDays, p.RetainCount, p.RetainMinFull)
	return err
}

// restoreColumns is the ops_restore_state projection shared by every read.
const restoreColumns = `id, backup_id, started_at, completed_at, status, shard_states,
	started_by, COALESCE(operation_id, ''), pre_restore_backup_id, COALESCE(failed_shard, ''),
	COALESCE(error, ''), job_id, ledger_confirmed_at, ledger_confirmed_by,
	ledger_confirmed_justification`

// InsertRestore writes a new ops_restore_state row.
func (s *Store) InsertRestore(ctx context.Context, r backup.RestoreState) error {
	args, err := restoreArgs(r)
	if err != nil {
		return err
	}
	_, err = s.pool.Exec(ctx, `
		INSERT INTO ops_restore_state
			(id, backup_id, started_at, completed_at, status, shard_states, started_by,
			 operation_id, pre_restore_backup_id, failed_shard, error, job_id,
			 ledger_confirmed_at, ledger_confirmed_by, ledger_confirmed_justification)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)`, args...)
	return err
}

// UpdateRestore overwrites the ops_restore_state row identified by r.ID.
func (s *Store) UpdateRestore(ctx context.Context, r backup.RestoreState) error {
	args, err := restoreArgs(r)
	if err != nil {
		return err
	}
	tag, err := s.pool.Exec(ctx, `
		UPDATE ops_restore_state SET
			backup_id=$2, started_at=$3, completed_at=$4, status=$5, shard_states=$6,
			started_by=$7, operation_id=$8, pre_restore_backup_id=$9, failed_shard=$10,
			error=$11, job_id=$12, ledger_confirmed_at=$13, ledger_confirmed_by=$14,
			ledger_confirmed_justification=$15
		WHERE id=$1`, args...)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return backup.ErrNotFound
	}
	return nil
}

// GetRestore reads one ops_restore_state row.
func (s *Store) GetRestore(ctx context.Context, id string) (backup.RestoreState, error) {
	r, err := scanRestore(s.pool.QueryRow(ctx,
		`SELECT `+restoreColumns+` FROM ops_restore_state WHERE id=$1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return backup.RestoreState{}, backup.ErrNotFound
	}
	if err != nil {
		return backup.RestoreState{}, err
	}
	return r, nil
}

// ListRestores reads ops_restore_state rows matching filter, ordered
// newest-first by started_at.
func (s *Store) ListRestores(ctx context.Context, filter backup.RestoreFilter) ([]backup.RestoreState, error) {
	query := `SELECT ` + restoreColumns + ` FROM ops_restore_state`
	var args []any
	if filter.Status != "" {
		args = append(args, filter.Status)
		query += " WHERE status = $1"
	}
	query += " ORDER BY started_at DESC, id DESC"
	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []backup.RestoreState
	for rows.Next() {
		r, err := scanRestore(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// FailStalePending implements backup.PendingReconciler: it marks every
// ops_backups row still in status:pending whose started_at is older than
// cutoff as failed with error JOB_CREATE_FAILED, in a single server-side
// statement (the §25.11 lines 3976-3977 reconcile, run against Postgres
// rather than the in-memory store). It returns the IDs it failed.
func (s *Store) FailStalePending(ctx context.Context, cutoff time.Time) ([]string, error) {
	rows, err := s.pool.Query(ctx, `
		UPDATE ops_backups
		   SET status=$1, error='JOB_CREATE_FAILED'
		 WHERE status=$2 AND started_at < $3
		RETURNING id`,
		backup.StatusFailed, backup.StatusPending, cutoff)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// backupArgs renders the positional INSERT/UPDATE arguments for an
// ops_backups row.
func backupArgs(b backup.Backup) []any {
	components, err := json.Marshal(b.Components)
	if err != nil || len(b.Components) == 0 {
		components = []byte("[]")
	}
	return []any{
		b.ID, b.Type, b.Status, b.StartedAt, b.CompletedAt, b.SizeBytes,
		durationMillis(b.Duration), nullStr(b.StoragePath), nullStr(b.Checksum),
		components, b.StartedBy, nullStr(b.OperationID), b.JobID, nullStr(b.Error),
		b.PlatformVersion, b.SchemaVersion, b.ExpiresAt,
	}
}

// scanBackup decodes one ops_backups row.
func scanBackup(row pgx.Row) (backup.Backup, error) {
	var (
		b          backup.Backup
		durationMs int64
		components []byte
	)
	if err := row.Scan(&b.ID, &b.Type, &b.Status, &b.StartedAt, &b.CompletedAt,
		&b.SizeBytes, &durationMs, &b.StoragePath, &b.Checksum, &components,
		&b.StartedBy, &b.OperationID, &b.JobID, &b.Error, &b.PlatformVersion,
		&b.SchemaVersion, &b.ExpiresAt); err != nil {
		return backup.Backup{}, err
	}
	b.Duration = formatMillis(durationMs)
	if len(components) > 0 {
		_ = json.Unmarshal(components, &b.Components)
	}
	return b, nil
}

// restoreArgs renders the positional INSERT/UPDATE arguments for an
// ops_restore_state row.
func restoreArgs(r backup.RestoreState) ([]any, error) {
	shards := r.ShardStates
	if shards == nil {
		shards = map[string]backup.ShardState{}
	}
	shardJSON, err := json.Marshal(shards)
	if err != nil {
		return nil, err
	}
	return []any{
		r.ID, r.BackupID, r.StartedAt, r.CompletedAt, r.Status, shardJSON,
		r.StartedBy, nullStr(r.OperationID), r.PreRestoreBackupID, nullStr(r.FailedShard),
		nullStr(r.Error), r.JobID, r.LedgerConfirmedAt, r.LedgerConfirmedBy,
		r.LedgerConfirmedJustification,
	}, nil
}

// scanRestore decodes one ops_restore_state row.
func scanRestore(row pgx.Row) (backup.RestoreState, error) {
	var (
		r      backup.RestoreState
		shards []byte
	)
	if err := row.Scan(&r.ID, &r.BackupID, &r.StartedAt, &r.CompletedAt, &r.Status,
		&shards, &r.StartedBy, &r.OperationID, &r.PreRestoreBackupID, &r.FailedShard,
		&r.Error, &r.JobID, &r.LedgerConfirmedAt, &r.LedgerConfirmedBy,
		&r.LedgerConfirmedJustification); err != nil {
		return backup.RestoreState{}, err
	}
	if len(shards) > 0 {
		_ = json.Unmarshal(shards, &r.ShardStates)
	}
	if r.ShardStates == nil {
		r.ShardStates = map[string]backup.ShardState{}
	}
	return r, nil
}

// durationMillis parses the Backup.Duration display string back to a
// nullable millisecond count for the duration_ms column. An empty or
// unparseable string stores NULL.
func durationMillis(s string) any {
	if s == "" {
		return nil
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return nil
	}
	return d.Milliseconds()
}

// formatMillis renders a duration_ms count back to the Backup.Duration
// display string. Zero (NULL coalesced) renders as the empty string so
// the field's omitempty JSON tag drops it.
func formatMillis(ms int64) string {
	if ms == 0 {
		return ""
	}
	return (time.Duration(ms) * time.Millisecond).String()
}

// nullStr returns nil for an empty string so the column stores NULL.
func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// joinAnd joins SQL conditions with AND.
func joinAnd(conds []string) string {
	out := conds[0]
	for _, c := range conds[1:] {
		out += " AND " + c
	}
	return out
}

// itoa renders a small positive placeholder index without importing
// strconv (the ops_backups filter has at most four conditions).
func itoa(n int) string {
	if n < 10 {
		return string(rune('0' + n))
	}
	return string(rune('0'+n/10)) + string(rune('0'+n%10))
}
