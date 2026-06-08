// SPDX-License-Identifier: MIT

// Package pgstore is the Postgres-backed evalstore.Store, persisting
// the §10.7 built-in eval-result registry to the eval_results table.
// It is a drop-in alternative to evalstore.Memory and satisfies the
// same contract, so the gateway swaps backends without changing
// callers.
//
// eval_results is tenant-scoped, so every operation runs inside a
// transaction that sets app.current_tenant for the §12.3
// lenny_tenant_guard trigger and the row-level security policy. Reads
// additionally carry an explicit tenant_id predicate so the store
// behaves identically whether the connection role is the non-superuser
// lenny_app (RLS-filtered) or a superuser (RLS bypassed).
package pgstore

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lennylabs/lenny/pkg/gateway/evalstore"
	"github.com/lennylabs/lenny/pkg/gateway/pgtenant"
)

// Store is the Postgres-backed eval-result registry. Construct with
// New.
type Store struct {
	pool          *pgxpool.Pool
	maxPerSession int
	clock         func() time.Time
}

// New returns a Store backed by pool. The pool must point at a
// database that has the migrations/ schema applied. The per-session
// storage bound is evalstore.DefaultMaxEvalsPerSession and the
// ingestion clock is time.Now().UTC, mirroring evalstore.NewMemory(0,
// nil).
func New(pool *pgxpool.Pool) *Store {
	return &Store{
		pool:          pool,
		maxPerSession: evalstore.DefaultMaxEvalsPerSession,
		clock:         func() time.Time { return time.Now().UTC() },
	}
}

var _ evalstore.Store = (*Store)(nil)

// selectList is the column projection for reads, in scanResult order.
// session_id is rendered as text so it scans into the EvalResult
// struct's string field.
const selectList = `id, tenant_id, session_id::text, experiment_id, variant_id,
	scorer, score, scores, metadata, delegation_depth, inherited,
	submitted_after_conclusion, idempotency_key, created_at`

// Put assigns an ID and CreatedAt, enforces the §10.7 per-session
// storage bound, and stores the result. The count and the insert run
// in one transaction so the bound holds under concurrent writers.
// Returns evalstore.ErrQuotaExceeded when the session is already at
// its cap.
func (s *Store) Put(ctx context.Context, r evalstore.EvalResult) (evalstore.EvalResult, error) {
	if r.ID == "" {
		r.ID = evalstore.NewID()
	}
	if r.CreatedAt.IsZero() {
		r.CreatedAt = s.clock()
	}
	err := pgtenant.InTx(ctx, s.pool, r.TenantID, func(tx pgx.Tx) error {
		var count int
		if err := tx.QueryRow(ctx,
			`SELECT COUNT(*) FROM eval_results
			 WHERE tenant_id = $1 AND session_id = $2::uuid`,
			r.TenantID, r.SessionID).Scan(&count); err != nil {
			return err
		}
		if count >= s.maxPerSession {
			return evalstore.ErrQuotaExceeded
		}
		_, err := tx.Exec(ctx, `INSERT INTO eval_results (
			tenant_id, id, session_id, experiment_id, variant_id,
			scorer, score, scores, metadata, delegation_depth, inherited,
			submitted_after_conclusion, idempotency_key, created_at
		) VALUES (
			$1, $2, $3::uuid, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14
		)`,
			r.TenantID, r.ID, r.SessionID, r.ExperimentID, r.VariantID,
			r.Scorer, r.Score, scoresArg(r.Scores), metadataArg(r.Metadata),
			int64(r.DelegationDepth), r.Inherited, r.SubmittedAfterConclusion,
			idempotencyArg(r.IdempotencyKey), r.CreatedAt)
		return err
	})
	if err != nil {
		return evalstore.EvalResult{}, err
	}
	return r, nil
}

// FindByIdempotencyKey returns the newest in-window eval result for
// (tenantID, sessionID, key) — the §10.7 dedup lookup. An empty key
// returns ok=false without a query. spec: §10.7 line 940.
func (s *Store) FindByIdempotencyKey(ctx context.Context, tenantID, sessionID, key string, notBefore time.Time) (evalstore.EvalResult, bool, error) {
	if key == "" {
		return evalstore.EvalResult{}, false, nil
	}
	var (
		out   evalstore.EvalResult
		found bool
	)
	err := pgtenant.InTx(ctx, s.pool, tenantID, func(tx pgx.Tx) error {
		row := tx.QueryRow(ctx,
			`SELECT `+selectList+` FROM eval_results
			 WHERE tenant_id = $1 AND session_id = $2::uuid
			   AND idempotency_key = $3 AND created_at >= $4
			 ORDER BY created_at DESC, id DESC
			 LIMIT 1`,
			tenantID, sessionID, key, notBefore)
		r, err := scanResult(row)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		out = r
		found = true
		return nil
	})
	if err != nil {
		return evalstore.EvalResult{}, false, err
	}
	return out, found, nil
}

// CountBySession returns the number of eval results stored for the
// session within the tenant.
func (s *Store) CountBySession(ctx context.Context, tenantID, sessionID string) (int, error) {
	var count int
	err := pgtenant.InTx(ctx, s.pool, tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`SELECT COUNT(*) FROM eval_results
			 WHERE tenant_id = $1 AND session_id = $2::uuid`,
			tenantID, sessionID).Scan(&count)
	})
	if err != nil {
		return 0, err
	}
	return count, nil
}

// ListBySession returns the session's eval results, oldest first.
func (s *Store) ListBySession(ctx context.Context, tenantID, sessionID string) ([]evalstore.EvalResult, error) {
	var out []evalstore.EvalResult
	err := pgtenant.InTx(ctx, s.pool, tenantID, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx,
			`SELECT `+selectList+` FROM eval_results
			 WHERE tenant_id = $1 AND session_id = $2::uuid
			 ORDER BY created_at, id`,
			tenantID, sessionID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			r, err := scanResult(rows)
			if err != nil {
				return err
			}
			out = append(out, r)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// ListByExperiment returns every eval result attributed to the
// experiment within the tenant, oldest first — the input to the §10.7
// results aggregation. Results with no experiment attribution are
// excluded. An empty experimentID yields no rows, mirroring
// evalstore.Memory.
func (s *Store) ListByExperiment(ctx context.Context, tenantID, experimentID string) ([]evalstore.EvalResult, error) {
	if experimentID == "" {
		return nil, nil
	}
	var out []evalstore.EvalResult
	err := pgtenant.InTx(ctx, s.pool, tenantID, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx,
			`SELECT `+selectList+` FROM eval_results
			 WHERE tenant_id = $1 AND experiment_id = $2
			 ORDER BY created_at, id`,
			tenantID, experimentID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			r, err := scanResult(rows)
			if err != nil {
				return err
			}
			out = append(out, r)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

var _ evalstore.AggregateReader = (*Store)(nil)

// AggregatesByExperiment reads the §10.7 results aggregation from the
// lenny_eval_aggregates materialized view (migration 0156) through the
// tenant-scoped lenny_eval_aggregates_tenant view, which filters by
// app.current_tenant. It returns the per-variant aggregates keyed by
// variant id; a variant with no eval rows is absent from the map. The
// matview pre-aggregates across all rows, so this read path serves only
// the unfiltered, no-breakdown request — the handler routes filtered or
// broken-down requests to the base table. spec: §10.7 lines 954, 1088.
func (s *Store) AggregatesByExperiment(ctx context.Context, tenantID, experimentID string) (map[string]evalstore.VariantAggregate, error) {
	out := map[string]evalstore.VariantAggregate{}
	if experimentID == "" {
		return out, nil
	}
	err := pgtenant.InTx(ctx, s.pool, tenantID, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx,
			`SELECT variant_id, agg_kind, scorer, dimension,
			        sample_count, mean_score, p50_score, p95_score
			 FROM lenny_eval_aggregates_tenant
			 WHERE tenant_id = $1 AND experiment_id = $2`,
			tenantID, experimentID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var (
				variantID, aggKind, scorer, dimension string
				sampleCount                           int64
				mean, p50, p95                        *float64
			)
			if err := rows.Scan(&variantID, &aggKind, &scorer, &dimension,
				&sampleCount, &mean, &p50, &p95); err != nil {
				return err
			}
			va := out[variantID]
			va.VariantID = variantID
			if va.Scorers == nil {
				va.Scorers = map[string]evalstore.ScorerAggregate{}
			}
			switch aggKind {
			case "variant":
				va.SampleCount = int(sampleCount)
			case "scorer":
				sa := va.Scorers[scorer]
				sa.Count = int(sampleCount)
				sa.Mean, sa.P50, sa.P95 = deref(mean), deref(p50), deref(p95)
				va.Scorers[scorer] = sa
			case "dimension":
				sa := va.Scorers[scorer]
				if sa.Dimensions == nil {
					sa.Dimensions = map[string]evalstore.ScorerAggregate{}
				}
				sa.Dimensions[dimension] = evalstore.ScorerAggregate{
					Count: int(sampleCount),
					Mean:  deref(mean), P50: deref(p50), P95: deref(p95),
				}
				va.Scorers[scorer] = sa
			}
			out[variantID] = va
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// RefreshAggregates runs one cross-tenant REFRESH MATERIALIZED VIEW
// CONCURRENTLY of lenny_eval_aggregates via the SECURITY DEFINER
// refresh_lenny_eval_aggregates() function (migration 0156). The
// function executes with its BYPASSRLS owner's privileges, so the
// gateway's non-superuser lenny_app role drives the refresh without
// itself bypassing row-level security. spec: §10.7 line 1088.
func (s *Store) RefreshAggregates(ctx context.Context) error {
	_, err := s.pool.Exec(ctx, `SELECT refresh_lenny_eval_aggregates()`)
	return err
}

// deref returns the pointed-to float, or 0 for a NULL aggregate column
// (a scorer with no top-level score reports zero count/mean/percentiles,
// matching the base-table aggregation).
func deref(f *float64) float64 {
	if f == nil {
		return 0
	}
	return *f
}

// DeleteBySession removes every eval result for the session and
// returns the count deleted — the §12.8 GDPR-erasure adapter. A
// session with no eval results yields (0, nil).
func (s *Store) DeleteBySession(ctx context.Context, tenantID, sessionID string) (int, error) {
	deleted := 0
	err := pgtenant.InTx(ctx, s.pool, tenantID, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx,
			`DELETE FROM eval_results
			 WHERE tenant_id = $1 AND session_id = $2::uuid`,
			tenantID, sessionID)
		if err != nil {
			return err
		}
		deleted = int(tag.RowsAffected())
		return nil
	})
	if err != nil {
		return 0, err
	}
	return deleted, nil
}

// DeleteByUser implements the §12.1 mandatory-erasure primitive.
// Eval results carry no user_id directly; the §12.8 orchestrator
// walks the user's sessions and calls DeleteBySession per session.
// DeleteByUser at this layer returns (0, nil).
//
// spec: §12.1 line 5.
func (s *Store) DeleteByUser(_ context.Context, _, _ string) (int, error) {
	return 0, nil
}

// DeleteByTenant implements the §12.1 mandatory-erasure primitive.
// Removes every eval-result row belonging to tenantID.
//
// spec: §12.1 line 5, §12.8 Phase 4.
func (s *Store) DeleteByTenant(ctx context.Context, tenantID string) (int, error) {
	if tenantID == "" {
		return 0, errors.New("evalstore: DeleteByTenant requires a concrete tenant_id")
	}
	var deleted int64
	err := pgtenant.InTx(ctx, s.pool, tenantID, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx,
			`DELETE FROM eval_results WHERE tenant_id = $1`, tenantID)
		if err != nil {
			return err
		}
		deleted = tag.RowsAffected()
		return nil
	})
	if err != nil {
		return 0, err
	}
	return int(deleted), nil
}

// scanResult reads one row in selectList order into an EvalResult. The
// nullable score column and the scores/metadata jsonb columns collapse
// SQL NULL to a nil pointer or nil map, matching evalstore.Memory's
// clone semantics.
func scanResult(row pgx.Row) (evalstore.EvalResult, error) {
	var (
		r               evalstore.EvalResult
		score           *float64
		scoresJSON      []byte
		metadataJSON    []byte
		delegationDepth int64
		idempotencyKey  *string
	)
	if err := row.Scan(
		&r.ID, &r.TenantID, &r.SessionID, &r.ExperimentID, &r.VariantID,
		&r.Scorer, &score, &scoresJSON, &metadataJSON, &delegationDepth,
		&r.Inherited, &r.SubmittedAfterConclusion, &idempotencyKey, &r.CreatedAt,
	); err != nil {
		return evalstore.EvalResult{}, err
	}
	r.Score = score
	r.DelegationDepth = uint32(delegationDepth)
	if idempotencyKey != nil {
		r.IdempotencyKey = *idempotencyKey
	}
	if len(scoresJSON) > 0 {
		scores := map[string]float64{}
		if err := json.Unmarshal(scoresJSON, &scores); err != nil {
			return evalstore.EvalResult{}, err
		}
		r.Scores = scores
	}
	if len(metadataJSON) > 0 {
		metadata := map[string]any{}
		if err := json.Unmarshal(metadataJSON, &metadata); err != nil {
			return evalstore.EvalResult{}, err
		}
		r.Metadata = metadata
	}
	return r, nil
}

// scoresArg renders an optional §10.7 per-dimension scores map as a
// jsonb query argument. A nil or empty map becomes a SQL NULL so the
// column stays null and the scan path reads it back as a nil map.
func scoresArg(scores map[string]float64) any {
	if len(scores) == 0 {
		return nil
	}
	raw, err := json.Marshal(scores)
	if err != nil {
		return nil
	}
	return raw
}

// idempotencyArg renders the optional §10.7 idempotency key as a query
// argument. An empty key becomes a SQL NULL so the partial dedup index
// excludes keyless rows and the scan path reads it back as "".
func idempotencyArg(key string) any {
	if key == "" {
		return nil
	}
	return key
}

// metadataArg renders an optional caller metadata map as a jsonb query
// argument. A nil or empty map becomes a SQL NULL, matching the scan
// path's nil-map reconstruction.
func metadataArg(metadata map[string]any) any {
	if len(metadata) == 0 {
		return nil
	}
	raw, err := json.Marshal(metadata)
	if err != nil {
		return nil
	}
	return raw
}
