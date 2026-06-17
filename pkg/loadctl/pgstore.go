// SPDX-License-Identifier: MIT

package loadctl

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// pgStore is the Postgres implementation of Store. Selected when
// DatabaseURL starts with "postgres://".
type pgStore struct {
	pool *pgxpool.Pool
}

func newPGStore(ctx context.Context, dsn string) (*pgStore, error) {
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, fmt.Errorf("loadctl: pgx connect: %w", err)
	}
	s := &pgStore{pool: pool}
	if err := s.migrate(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return s, nil
}

const schemaSQL = `
CREATE TABLE IF NOT EXISTS runs (
    id              TEXT PRIMARY KEY,
    status          TEXT NOT NULL,
    scale           TEXT,
    scenarios       JSONB NOT NULL DEFAULT '[]',
    cluster_release TEXT,
    started_at      TIMESTAMPTZ NOT NULL,
    completed_at    TIMESTAMPTZ,
    report_url      TEXT,
    current_metrics TEXT
);
CREATE INDEX IF NOT EXISTS runs_started_at_idx ON runs (started_at DESC);

CREATE TABLE IF NOT EXISTS baselines (
    name   TEXT PRIMARY KEY,
    run_id TEXT NOT NULL REFERENCES runs(id) ON DELETE CASCADE
);
`

func (s *pgStore) migrate(ctx context.Context) error {
	_, err := s.pool.Exec(ctx, schemaSQL)
	return err
}

func (s *pgStore) CreateRun(ctx context.Context, r *Run) error {
	scenariosJSON, _ := json.Marshal(r.Scenarios)
	_, err := s.pool.Exec(ctx, `
        INSERT INTO runs (id, status, scale, scenarios, cluster_release, started_at, completed_at, report_url, current_metrics)
        VALUES ($1, $2, $3, $4::jsonb, $5, $6, NULLIF($7, $7::timestamptz - $7::timestamptz + 'epoch'::timestamptz), $8, $9)`,
		r.ID, r.Status, r.Scale, string(scenariosJSON), r.ClusterRelease, r.StartedAt, nullTime(r.CompletedAt), nullString(r.ReportURL), nullString(r.CurrentMetrics))
	return err
}

func (s *pgStore) GetRun(ctx context.Context, id string) (*Run, error) {
	var r Run
	var scenariosJSON []byte
	var completedAt *time.Time
	err := s.pool.QueryRow(ctx, `
        SELECT id, status, scale, scenarios, cluster_release, started_at, completed_at, COALESCE(report_url,''), COALESCE(current_metrics,'')
        FROM runs WHERE id=$1`, id).Scan(
		&r.ID, &r.Status, &r.Scale, &scenariosJSON, &r.ClusterRelease, &r.StartedAt, &completedAt, &r.ReportURL, &r.CurrentMetrics,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrRunNotFound
	}
	if err != nil {
		return nil, err
	}
	if completedAt != nil {
		r.CompletedAt = *completedAt
	}
	_ = json.Unmarshal(scenariosJSON, &r.Scenarios)
	return &r, nil
}

func (s *pgStore) UpdateRun(ctx context.Context, r *Run) error {
	scenariosJSON, _ := json.Marshal(r.Scenarios)
	tag, err := s.pool.Exec(ctx, `
        UPDATE runs SET status=$2, scale=$3, scenarios=$4::jsonb, cluster_release=$5,
                        completed_at=$6, report_url=$7, current_metrics=$8
        WHERE id=$1`,
		r.ID, r.Status, r.Scale, string(scenariosJSON), r.ClusterRelease, nullTime(r.CompletedAt), nullString(r.ReportURL), nullString(r.CurrentMetrics))
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrRunNotFound
	}
	return nil
}

func (s *pgStore) ListRuns(ctx context.Context) ([]*Run, error) {
	rows, err := s.pool.Query(ctx, `
        SELECT id, status, scale, scenarios, cluster_release, started_at, completed_at, COALESCE(report_url,''), COALESCE(current_metrics,'')
        FROM runs ORDER BY started_at DESC LIMIT 500`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Run{}
	for rows.Next() {
		var r Run
		var scenariosJSON []byte
		var completedAt *time.Time
		if err := rows.Scan(&r.ID, &r.Status, &r.Scale, &scenariosJSON, &r.ClusterRelease, &r.StartedAt, &completedAt, &r.ReportURL, &r.CurrentMetrics); err != nil {
			return nil, err
		}
		if completedAt != nil {
			r.CompletedAt = *completedAt
		}
		_ = json.Unmarshal(scenariosJSON, &r.Scenarios)
		out = append(out, &r)
	}
	return out, rows.Err()
}

func (s *pgStore) PinBaseline(ctx context.Context, name, runID string) error {
	tag, err := s.pool.Exec(ctx, `
        INSERT INTO baselines (name, run_id) VALUES ($1, $2)
        ON CONFLICT (name) DO UPDATE SET run_id=EXCLUDED.run_id`,
		name, runID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrRunNotFound
	}
	return nil
}

func (s *pgStore) Close() error {
	s.pool.Close()
	return nil
}

func nullString(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func nullTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t
}
