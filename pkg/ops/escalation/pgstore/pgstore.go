// SPDX-License-Identifier: MIT

// Package pgstore is the Postgres-backed §25.4 escalation Store (Tier 1).
// It persists escalation records to the ops_escalations table (migration
// 0122) so the durable tier survives a lenny-ops restart and coordinates
// across replicas. A Postgres outage surfaces as
// escalation.ErrStoreUnavailable so the tiered Service falls back to the
// Redis or in-memory tier rather than failing the create.
//
// The ops_escalations table is platform-scoped (the §25 control plane is
// not multi-tenanted at this boundary), so the store does not run inside
// a tenant-scoped transaction; the table has no RLS policy and no tenant
// column.
//
// spec: §25.4 lines 2376-2455.
package pgstore

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lennylabs/lenny/pkg/ops/escalation"
)

// Store is the Postgres-backed §25.4 escalation Tier 1 store. Construct
// with New.
type Store struct {
	pool *pgxpool.Pool
}

// New returns a Store backed by pool. The pool must point at a database
// with the migrations/ schema applied.
func New(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// columns is the ops_escalations projection shared by every read.
const columns = `id, severity, source, operation_id, alert_name, runbook_name, summary,
	diagnostic_data, failed_actions, status, persistence, emitted,
	created_at, updated_at, acknowledged_at, resolved_at`

// Tier reports the durable-postgres persistence label.
func (s *Store) Tier() string { return escalation.PersistenceDurablePostgres }

// classifyErr maps a pgx connection/transport failure to
// escalation.ErrStoreUnavailable (the §25.4 Postgres-outage case), so the
// tiered Service falls through to the Redis or in-memory tier instead of
// returning an internal error. A genuine server-side query error (a PgError
// the database answered with, such as a constraint violation) is left
// intact for the caller to surface.
//
// A connection refused before pgx wraps a PgError is the common outage
// signal and is handled by the default branch. A Postgres administrator
// shutdown or failover window is different: the server answers an in-flight
// request with a FATAL PgError before the connection drops. Those codes are
// still "Postgres is down" for the purpose of the §25.4 degraded-mode
// fall-through, so classifyErr treats the operator_intervention codes
// (57P01 admin_shutdown, 57P02 crash_shutdown, 57P03 cannot_connect_now)
// and the connection-exception class (08) as store-unavailable rather than
// surfacing them.
//
// spec: §25.4 lines 2422-2434 (Storage Tiers, Query Path degraded
// fall-through when Postgres is down).
func classifyErr(err error) error {
	if err == nil {
		return nil
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch {
		case strings.HasPrefix(pgErr.Code, "08"):
			// Class 08 — connection exception (08000, 08003, 08006, etc.).
			return escalation.ErrStoreUnavailable
		case pgErr.Code == "57P01", pgErr.Code == "57P02", pgErr.Code == "57P03":
			// admin_shutdown, crash_shutdown, cannot_connect_now — the backend
			// is terminating or not yet accepting connections, both of which a
			// shutdown or failover produces.
			return escalation.ErrStoreUnavailable
		default:
			// The database answered with a query-level error; surface it.
			return err
		}
	}
	return escalation.ErrStoreUnavailable
}

// Put upserts esc by id. The conditional update overwrites every column,
// so a reconciliation flush of a record the destination already holds is
// an idempotent no-op (§25.4 line 2413).
func (s *Store) Put(ctx context.Context, esc escalation.Escalation) error {
	diag, failed := marshalPayloads(esc)
	_, err := s.pool.Exec(ctx, `
		INSERT INTO ops_escalations (`+columns+`)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16)
		ON CONFLICT (id) DO UPDATE SET
			severity=EXCLUDED.severity, source=EXCLUDED.source,
			operation_id=EXCLUDED.operation_id, alert_name=EXCLUDED.alert_name,
			runbook_name=EXCLUDED.runbook_name, summary=EXCLUDED.summary,
			diagnostic_data=EXCLUDED.diagnostic_data, failed_actions=EXCLUDED.failed_actions,
			status=EXCLUDED.status, persistence=EXCLUDED.persistence, emitted=EXCLUDED.emitted,
			created_at=EXCLUDED.created_at, updated_at=EXCLUDED.updated_at,
			acknowledged_at=EXCLUDED.acknowledged_at, resolved_at=EXCLUDED.resolved_at`,
		esc.ID, esc.Severity, esc.Source, nullStr(esc.OperationID), nullStr(esc.AlertName),
		nullStr(esc.RunbookName), esc.Summary, diag, failed, esc.Status, esc.Persistence,
		esc.Emitted, esc.CreatedAt, esc.UpdatedAt, esc.AcknowledgedAt, esc.ResolvedAt)
	return classifyErr(err)
}

// Get returns the escalation by id, or (nil, nil) when absent.
func (s *Store) Get(ctx context.Context, id string) (*escalation.Escalation, error) {
	esc, err := scanEscalation(s.pool.QueryRow(ctx,
		`SELECT `+columns+` FROM ops_escalations WHERE id=$1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, classifyErr(err)
	}
	return esc, nil
}

// List returns the escalations matching f, newest-first, as one page. The
// durable Postgres tier is the §25.4 CursorKindPK query path: it keyset-
// paginates over the (created_at, id) ordering. A non-empty cursor
// continues after the row it encodes; the page reports HasMore and a
// NextCursor when more matching rows follow, so a caller can advance page
// by page (§25.4 line 2427, "full query with pagination").
//
// spec: §25.4 lines 2425-2429.
func (s *Store) List(ctx context.Context, f escalation.Filter, cursor string, limit int) (escalation.ListPage, error) {
	query := `SELECT ` + columns + ` FROM ops_escalations`
	var conds []string
	var args []any
	if statuses := csvList(f.Status); len(statuses) > 0 {
		args = append(args, statuses)
		conds = append(conds, "status = ANY($"+itoa(len(args))+")")
	}
	if severities := csvList(f.Severity); len(severities) > 0 {
		args = append(args, severities)
		conds = append(conds, "severity = ANY($"+itoa(len(args))+")")
	}
	if !f.Since.IsZero() {
		args = append(args, f.Since)
		conds = append(conds, "created_at >= $"+itoa(len(args)))
	}
	if cursor != "" {
		curTime, curID, err := decodeCursor(cursor)
		if err != nil {
			return escalation.ListPage{}, &escalation.Error{
				Code: escalation.ErrCodeInvalid, Message: "cursor is not a valid continuation token",
			}
		}
		// Keyset continuation. ORDER BY created_at DESC, id DESC gives a
		// strictly-descending (created_at, id) tuple (id is the primary
		// key, so the tuple is unique); the next page is the rows whose
		// tuple sorts strictly after the cursor's, expressed as a row-value
		// comparison so the composite index is usable.
		args = append(args, curTime)
		tsIdx := len(args)
		args = append(args, curID)
		idIdx := len(args)
		conds = append(conds, "(created_at, id) < ($"+itoa(tsIdx)+", $"+itoa(idIdx)+")")
	}
	if len(conds) > 0 {
		query += " WHERE " + strings.Join(conds, " AND ")
	}
	query += " ORDER BY created_at DESC, id DESC"
	// Over-fetch one row so the presence of a next page is known without a
	// second count query; the extra row is trimmed before returning.
	if limit > 0 {
		args = append(args, limit+1)
		query += " LIMIT $" + itoa(len(args))
	}
	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return escalation.ListPage{}, classifyErr(err)
	}
	defer rows.Close()
	var out []escalation.Escalation
	for rows.Next() {
		esc, err := scanEscalation(rows)
		if err != nil {
			return escalation.ListPage{}, classifyErr(err)
		}
		out = append(out, *esc)
	}
	if err := rows.Err(); err != nil {
		return escalation.ListPage{}, classifyErr(err)
	}
	page := escalation.ListPage{Items: out, CursorKind: escalation.CursorKindPK}
	if limit > 0 && len(out) > limit {
		page.HasMore = true
		page.Items = out[:limit]
		last := page.Items[len(page.Items)-1]
		page.NextCursor = encodeCursor(last.CreatedAt, last.ID)
	}
	return page, nil
}

// encodeCursor renders an opaque keyset continuation token for the row
// ending a page: the created_at instant (nanosecond-precise UTC) and the
// primary-key id, which together are unique. Agents MUST NOT parse it.
func encodeCursor(t time.Time, id string) string {
	raw := t.UTC().Format(time.RFC3339Nano) + "\x00" + id
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

// decodeCursor reverses encodeCursor, returning the row-key the token
// encodes. A token this store did not produce is a malformed-cursor error.
func decodeCursor(cursor string) (time.Time, string, error) {
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return time.Time{}, "", err
	}
	sep := strings.IndexByte(string(raw), 0)
	if sep < 0 {
		return time.Time{}, "", errors.New("cursor missing key separator")
	}
	t, err := time.Parse(time.RFC3339Nano, string(raw[:sep]))
	if err != nil {
		return time.Time{}, "", err
	}
	return t, string(raw[sep+1:]), nil
}

// SetStatus moves the escalation to status, stamping the lifecycle
// timestamps from now. The acknowledged/resolved timestamp is set only
// on the first transition into that state. Returns (nil, nil) when absent.
func (s *Store) SetStatus(ctx context.Context, id, status string, now time.Time) (*escalation.Escalation, error) {
	esc, err := scanEscalation(s.pool.QueryRow(ctx, `
		UPDATE ops_escalations SET
			status=$2, updated_at=$3,
			acknowledged_at = CASE WHEN $2='acknowledged' AND acknowledged_at IS NULL THEN $3 ELSE acknowledged_at END,
			resolved_at = CASE WHEN $2='resolved' AND resolved_at IS NULL THEN $3 ELSE resolved_at END
		WHERE id=$1
		RETURNING `+columns, id, status, now))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, classifyErr(err)
	}
	return esc, nil
}

// SetEmitted flips the escalation's emitted flag true.
func (s *Store) SetEmitted(ctx context.Context, id string) error {
	_, err := s.pool.Exec(ctx, `UPDATE ops_escalations SET emitted=true WHERE id=$1`, id)
	return classifyErr(err)
}

// PendingEmission returns escalations whose emitted flag is false.
func (s *Store) PendingEmission(ctx context.Context) ([]escalation.Escalation, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT `+columns+` FROM ops_escalations WHERE emitted=false ORDER BY created_at`)
	if err != nil {
		return nil, classifyErr(err)
	}
	defer rows.Close()
	var out []escalation.Escalation
	for rows.Next() {
		esc, err := scanEscalation(rows)
		if err != nil {
			return nil, classifyErr(err)
		}
		out = append(out, *esc)
	}
	return out, classifyErr(rows.Err())
}

// marshalPayloads renders the JSONB columns: diagnostic_data is NULL when
// empty; failed_actions is always a JSON array (the column is NOT NULL
// DEFAULT '[]').
func marshalPayloads(esc escalation.Escalation) (diag any, failed []byte) {
	if len(esc.DiagnosticData) > 0 {
		diag = []byte(esc.DiagnosticData)
	}
	if len(esc.FailedActions) == 0 {
		return diag, []byte("[]")
	}
	b, err := json.Marshal(esc.FailedActions)
	if err != nil {
		return diag, []byte("[]")
	}
	return diag, b
}

// scanEscalation decodes one ops_escalations row.
func scanEscalation(row pgx.Row) (*escalation.Escalation, error) {
	var (
		esc                                 escalation.Escalation
		operationID, alertName, runbookName *string
		diag, failed                        []byte
	)
	if err := row.Scan(&esc.ID, &esc.Severity, &esc.Source, &operationID, &alertName,
		&runbookName, &esc.Summary, &diag, &failed, &esc.Status, &esc.Persistence,
		&esc.Emitted, &esc.CreatedAt, &esc.UpdatedAt, &esc.AcknowledgedAt, &esc.ResolvedAt); err != nil {
		return nil, err
	}
	esc.OperationID = derefStr(operationID)
	esc.AlertName = derefStr(alertName)
	esc.RunbookName = derefStr(runbookName)
	if len(diag) > 0 {
		esc.DiagnosticData = json.RawMessage(diag)
	}
	if len(failed) > 0 {
		_ = json.Unmarshal(failed, &esc.FailedActions)
	}
	return &esc, nil
}

// nullStr returns nil for an empty string so the column stores NULL.
func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// derefStr returns the pointed-at string, or "" when nil.
func derefStr(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// csvList splits a comma-separated filter value into a trimmed slice.
func csvList(csv string) []string {
	if csv == "" {
		return nil
	}
	var out []string
	for _, part := range strings.Split(csv, ",") {
		if v := strings.TrimSpace(part); v != "" {
			out = append(out, v)
		}
	}
	return out
}

// itoa renders a small positive int without importing strconv at every
// call site (placeholder indices are always single- or double-digit).
func itoa(n int) string {
	if n < 10 {
		return string(rune('0' + n))
	}
	return string(rune('0'+n/10)) + string(rune('0'+n%10))
}

// Compile-time guard that *Store satisfies the escalation.Store contract.
var _ escalation.Store = (*Store)(nil)
