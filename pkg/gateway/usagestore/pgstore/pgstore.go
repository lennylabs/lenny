// SPDX-License-Identifier: MIT

// Package pgstore is the Postgres-backed usagestore.Store over the
// append-only usage_events table. It is the durable §15.1 usage /
// metering accumulator; usagestore.Memory is the in-memory
// alternative.
//
// usage_events is tenant-scoped: every operation runs inside a
// transaction that sets app.current_tenant for the §12.3
// lenny_tenant_guard trigger and the RLS policy. The table is
// append-only — Record inserts an event and there is no update or
// delete.
package pgstore

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lennylabs/lenny/pkg/gateway/storage/pgtenant"
	"github.com/lennylabs/lenny/pkg/gateway/usagestore"
)

// Store is the Postgres-backed usage accumulator. Construct with New.
type Store struct {
	pool *pgxpool.Pool
	// read is the §12.3 line 146 read-replica pool for usage reports.
	// It is set to pool unless WithReadPool wires a separate reader; the
	// Record write always uses pool. spec: §12.3 line 146.
	read *pgxpool.Pool
}

// Option configures a Store at construction time.
type Option func(*Store)

// WithReadPool routes the read-heavy usage-report aggregation (§12.3
// line 146) to a separate read-replica pool. A nil pool keeps reads on
// the primary. spec: §12.3 line 146.
func WithReadPool(read *pgxpool.Pool) Option {
	return func(s *Store) {
		if read != nil {
			s.read = read
		}
	}
}

// New returns a Store backed by pool. The pool must point at a
// database that has the migrations/ schema applied. Without WithReadPool
// the usage-report read path shares pool.
func New(pool *pgxpool.Pool, opts ...Option) *Store {
	s := &Store{pool: pool, read: pool}
	for _, o := range opts {
		o(s)
	}
	return s
}

var _ usagestore.Store = (*Store)(nil)

// Record appends a usage event to the tenant's accumulator. The row's
// synthetic id is assigned by the usage_events DEFAULT.
func (s *Store) Record(ctx context.Context, r usagestore.Record) error {
	labels, err := labelsValue(r.Labels)
	if err != nil {
		return err
	}
	return pgtenant.InTx(ctx, s.pool, r.TenantID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO usage_events (
			tenant_id, runtime, sessions, tokens_input, tokens_output, pod_minutes, labels
		) VALUES ($1, $2, $3, $4, $5, $6, $7)`,
			r.TenantID, r.Runtime, r.Sessions,
			r.Tokens.Input, r.Tokens.Output, r.PodMinutes, labels)
		return err
	})
}

// labelsValue maps a §14 label map to the nullable labels JSONB column
// value: nil (SQL NULL) when the map is empty so a NULL-labels row never
// matches a non-empty containment filter, the JSON encoding otherwise.
// spec: §14 line 106. F-14.1.13.
func labelsValue(m map[string]string) (any, error) {
	if len(m) == 0 {
		return nil, nil
	}
	b, err := json.Marshal(m)
	if err != nil {
		return nil, err
	}
	return b, nil
}

// Aggregate returns the §15.1 usage report, computing the same rollup
// as usagestore.Memory: the per-tenant and per-runtime session and
// token sums, the grand totals, the by-runtime list excluding events
// with no runtime, and both lists sorted by their key ascending.
//
// When tenantFilter is non-empty the report covers that one tenant and
// runs inside that tenant's transaction context. When tenantFilter is
// empty the report is platform-wide: usage_events is RLS-protected and
// a transaction can only set one app.current_tenant, so the
// platform-wide path enumerates the tenants table (which is
// platform-global and unguarded), aggregates each tenant under its own
// transaction context, and merges the per-tenant reports.
func (s *Store) Aggregate(ctx context.Context, tenantFilter string, labelFilter map[string]string) (usagestore.Report, error) {
	if tenantFilter != "" {
		return s.aggregateTenant(ctx, tenantFilter, labelFilter)
	}
	return s.aggregateAll(ctx, labelFilter)
}

// aggregateTenant computes the report for one tenant. The rollup
// queries run inside that tenant's transaction context and also carry
// an explicit tenant_id predicate: the predicate is the load-bearing
// scoping, with the RLS policy as the §12.3 defence-in-depth backstop,
// matching userstore/pgstore and sessionstore/pgstore.
func (s *Store) aggregateTenant(ctx context.Context, tenantID string, labelFilter map[string]string) (usagestore.Report, error) {
	var report usagestore.Report
	// spec: §12.3 line 146 — usage-report aggregation routes to the read replica.
	err := pgtenant.InTx(ctx, s.read, tenantID, func(tx pgx.Tx) error {
		r, err := aggregateTx(ctx, tx, tenantID, labelFilter)
		if err != nil {
			return err
		}
		report = r
		return nil
	})
	if err != nil {
		return usagestore.Report{}, err
	}
	return report, nil
}

// aggregateAll computes the platform-wide report by aggregating every
// tenant individually and merging. Tenants with no usage events
// contribute nothing, matching usagestore.Memory, which only ever
// emits a per-tenant or per-runtime rollup entry for a tenant or
// runtime that appears in at least one event.
func (s *Store) aggregateAll(ctx context.Context, labelFilter map[string]string) (usagestore.Report, error) {
	tenantIDs, err := s.tenantIDs(ctx)
	if err != nil {
		return usagestore.Report{}, err
	}
	var (
		report    usagestore.Report
		byRuntime = map[string]*usagestore.RuntimeUsage{}
	)
	report.ByTenant = make([]usagestore.TenantUsage, 0)
	for _, tenantID := range tenantIDs {
		one, err := s.aggregateTenant(ctx, tenantID, labelFilter)
		if err != nil {
			return usagestore.Report{}, err
		}
		report.TotalSessions += one.TotalSessions
		report.TotalTokens.Input += one.TotalTokens.Input
		report.TotalTokens.Output += one.TotalTokens.Output
		report.TotalPodMinutes += one.TotalPodMinutes
		// A per-tenant aggregate emits at most one ByTenant row, for the
		// tenant itself; carry it through only when the tenant had events.
		report.ByTenant = append(report.ByTenant, one.ByTenant...)
		for _, ru := range one.ByRuntime {
			acc := byRuntime[ru.Runtime]
			if acc == nil {
				acc = &usagestore.RuntimeUsage{Runtime: ru.Runtime}
				byRuntime[ru.Runtime] = acc
			}
			acc.Sessions += ru.Sessions
			acc.Tokens.Input += ru.Tokens.Input
			acc.Tokens.Output += ru.Tokens.Output
		}
	}
	// ByTenant is already tenant-id ascending because tenantIDs is
	// returned ordered; the per-runtime map is collapsed and sorted here.
	report.ByRuntime = make([]usagestore.RuntimeUsage, 0, len(byRuntime))
	for _, ru := range byRuntime {
		report.ByRuntime = append(report.ByRuntime, *ru)
	}
	sort.Slice(report.ByRuntime, func(i, j int) bool {
		return report.ByRuntime[i].Runtime < report.ByRuntime[j].Runtime
	})
	return report, nil
}

// tenantIDs returns every tenant id, ascending. The tenants table is
// platform-global and unguarded, so the read needs no tenant context.
func (s *Store) tenantIDs(ctx context.Context) ([]string, error) {
	// spec: §12.3 line 146 — usage-report tenant enumeration routes to the read replica.
	rows, err := s.read.Query(ctx, `SELECT id FROM tenants ORDER BY id`)
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

// aggregateTx runs the §15.1 rollup over tenantID's usage_events rows.
// The explicit tenant_id predicate scopes every query; the RLS policy
// on the transaction is the defence-in-depth backstop. It mirrors
// usagestore.Memory: the totals are the sums of the tenant's events,
// the by-tenant rollup is the single tenant row (absent when the
// tenant has no events), and the by-runtime rollup drops events whose
// runtime is empty and is runtime ascending.
func aggregateTx(ctx context.Context, tx pgx.Tx, tenantID string, labelFilter map[string]string) (usagestore.Report, error) {
	var report usagestore.Report

	// spec: §14 line 106 — a non-empty label filter narrows both rollups
	// to the events whose denormalized labels contain every requested
	// pair. The predicate is pushed into SQL via the labels GIN index; a
	// NULL-labels row never matches a non-empty filter, which is the
	// desired "row lacks the label" semantics.
	labelPred := ""
	labelArgs := []any{}
	if len(labelFilter) > 0 {
		want, err := json.Marshal(labelFilter)
		if err != nil {
			return usagestore.Report{}, err
		}
		labelPred = " AND labels @> $2::jsonb"
		labelArgs = append(labelArgs, string(want))
	}

	// Per-tenant rollup. Scoped to tenantID, this yields at most one
	// group. The grand totals are the sums of its rows, derived in the
	// same pass.
	tenantRows, err := tx.Query(ctx, fmt.Sprintf(`SELECT
		tenant_id,
		COALESCE(SUM(sessions), 0),
		COALESCE(SUM(tokens_input), 0),
		COALESCE(SUM(tokens_output), 0),
		COALESCE(SUM(pod_minutes), 0)
	FROM usage_events
	WHERE tenant_id = $1%s
	GROUP BY tenant_id
	ORDER BY tenant_id`, labelPred), append([]any{tenantID}, labelArgs...)...)
	if err != nil {
		return usagestore.Report{}, err
	}
	defer tenantRows.Close()
	report.ByTenant = make([]usagestore.TenantUsage, 0)
	for tenantRows.Next() {
		var (
			tu         usagestore.TenantUsage
			podMinutes float64
		)
		if err := tenantRows.Scan(&tu.TenantID, &tu.Sessions,
			&tu.Tokens.Input, &tu.Tokens.Output, &podMinutes); err != nil {
			return usagestore.Report{}, err
		}
		report.TotalSessions += tu.Sessions
		report.TotalTokens.Input += tu.Tokens.Input
		report.TotalTokens.Output += tu.Tokens.Output
		report.TotalPodMinutes += podMinutes
		report.ByTenant = append(report.ByTenant, tu)
	}
	if err := tenantRows.Err(); err != nil {
		return usagestore.Report{}, err
	}

	// Per-runtime rollup, runtime ascending. Scoped to tenantID; events
	// with no runtime are excluded, matching usagestore.Memory's
	// `if rec.Runtime != ""`.
	runtimeRows, err := tx.Query(ctx, fmt.Sprintf(`SELECT
		runtime,
		COALESCE(SUM(sessions), 0),
		COALESCE(SUM(tokens_input), 0),
		COALESCE(SUM(tokens_output), 0)
	FROM usage_events
	WHERE tenant_id = $1 AND runtime <> ''%s
	GROUP BY runtime
	ORDER BY runtime`, labelPred), append([]any{tenantID}, labelArgs...)...)
	if err != nil {
		return usagestore.Report{}, err
	}
	defer runtimeRows.Close()
	report.ByRuntime = make([]usagestore.RuntimeUsage, 0)
	for runtimeRows.Next() {
		var ru usagestore.RuntimeUsage
		if err := runtimeRows.Scan(&ru.Runtime, &ru.Sessions,
			&ru.Tokens.Input, &ru.Tokens.Output); err != nil {
			return usagestore.Report{}, err
		}
		report.ByRuntime = append(report.ByRuntime, ru)
	}
	if err := runtimeRows.Err(); err != nil {
		return usagestore.Report{}, err
	}
	return report, nil
}
