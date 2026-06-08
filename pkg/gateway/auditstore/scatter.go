// SPDX-License-Identifier: MIT

package auditstore

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"sync"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lennylabs/lenny/pkg/audit"
	"github.com/lennylabs/lenny/pkg/gateway/pgtenant"
	"github.com/lennylabs/lenny/pkg/storerouter"
)

// ErrAllAuditShardsUnreachable reports that every audit shard failed the
// §25.9 cross-tenant scatter read. The query handler maps it to
// 503 AUDIT_STORE_UNAVAILABLE (a total Postgres outage), distinct from a
// partial-shard outage which surfaces as 207 AUDIT_PARTIAL_RESULTS.
//
// spec: §25.9 "Degradation" — full outage → 503, partial-shard → 207.
var ErrAllAuditShardsUnreachable = errors.New("auditstore: all audit shards unreachable")

// WithScatterConfig pins the §25.9 line 3710 scatter-gather fan-out
// bounds (max concurrency, per-shard timeout, aggregate timeout) the
// cross-tenant query uses. A zero value resolves to the storerouter
// defaults; v1 is single-shard so the bounds are inert until a
// multi-shard router is deployed.
func WithScatterConfig(cfg storerouter.ScatterConfig) Option {
	return func(s *Store) { s.scatterCfg = cfg }
}

// ScatterGatherRows runs the §25.9 line 3668 platform-admin cross-tenant
// audit query: it reads every tenant's chain across all audit shards via
// AllAuditShards, merged in memory and ordered by (tenant_id,
// sequence_number) so each tenant's §11.7 chain stays contiguous for
// verification. Shards are scanned in parallel under the configured
// concurrency limit with a per-shard timeout (§25.9 line 3710); a shard
// that is unreachable or times out is reported in missingShards rather
// than failing the whole query, driving the §25.9 207
// AUDIT_PARTIAL_RESULTS path. When every shard is unreachable the read
// returns ErrAllAuditShardsUnreachable so the handler emits 503. One
// §12.3 line 141 cross_tenant_read receipt is emitted per invocation.
func (s *Store) ScatterGatherRows(ctx context.Context) (rows []audit.Row, missingShards []string, err error) {
	shards, err := s.allShards(ctx)
	if err != nil {
		return nil, nil, err
	}
	cfg := s.scatterCfg
	def := storerouter.DefaultScatterConfig()
	maxConc := cfg.MaxConcurrency
	if maxConc <= 0 {
		maxConc = def.MaxConcurrency
	}
	perShard := cfg.PerShardTimeout
	if perShard <= 0 {
		perShard = def.PerShardTimeout
	}
	aggTimeout := cfg.AggregateTimeout
	if aggTimeout <= 0 {
		aggTimeout = def.AggregateTimeout
	}

	aggCtx, cancel := context.WithTimeout(ctx, aggTimeout)
	defer cancel()

	type shardRead struct {
		id   string
		rows []audit.Row
		ok   bool
	}
	out := make([]shardRead, len(shards))
	sem := make(chan struct{}, maxConc)
	var wg sync.WaitGroup
	for i := range shards {
		wg.Add(1)
		sem <- struct{}{}
		go func(idx int, sh storerouter.ShardHandle) {
			defer wg.Done()
			defer func() { <-sem }()
			shardCtx, c := context.WithTimeout(aggCtx, perShard)
			defer c()
			r, rerr := rowsAcrossTenantsOnShard(shardCtx, sh.Pool)
			if rerr != nil {
				// A per-shard read failure (connection loss or per-shard
				// timeout) is a partial-result miss, not a hard error:
				// §25.9 includes events from the reachable shards and lists
				// the missing ones in the 207 envelope.
				out[idx] = shardRead{id: string(sh.ID), ok: false}
				return
			}
			out[idx] = shardRead{id: string(sh.ID), rows: r, ok: true}
		}(i, shards[i])
	}
	wg.Wait()

	for _, r := range out {
		if !r.ok {
			missingShards = append(missingShards, r.id)
			continue
		}
		rows = append(rows, r.rows...)
	}
	if len(shards) > 0 && len(missingShards) == len(shards) {
		return nil, missingShards, ErrAllAuditShardsUnreachable
	}
	// Stable merge order: group each tenant's chain together (ascending
	// tenant_id) and order each chain by sequence number so the
	// per-tenant §11.7 verification sees contiguous rows.
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].TenantID != rows[j].TenantID {
			return rows[i].TenantID < rows[j].TenantID
		}
		return rows[i].Seq < rows[j].Seq
	})

	// spec: §12.3 line 141 — a SET lenny.tenant_id = '__all__' read MUST
	// emit one cross_tenant_read receipt per invocation.
	if cerr := s.emitCrossTenantRead(ctx, "audit_query", len(rows)); cerr != nil {
		return nil, nil, cerr
	}
	return rows, missingShards, nil
}

// rowsAcrossTenantsOnShard reads every tenant's audit rows on one shard
// inside an InAllTenants (`__all__` sentinel) transaction so the §12.3
// RLS predicate admits all tenants under the non-superuser lenny_app
// role. Rows are returned ordered by (tenant_id, sequence_number).
func rowsAcrossTenantsOnShard(ctx context.Context, pool *pgxpool.Pool) ([]audit.Row, error) {
	var out []audit.Row
	err := pgtenant.InAllTenants(ctx, pool, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx,
			// platform-admin-cross-tenant-allowed
			// platform-admin-cross-tenant-justification: §25.9 line 3668 — the
			// platform-admin cross-tenant audit query reads every tenant's
			// §11.7 chain across the shard via AllAuditShards scatter-gather,
			// ordered by tenant so each per-tenant chain stays contiguous for
			// verification.
			`SELECT tenant_id, `+selectList+` FROM audit_log
			 ORDER BY tenant_id, sequence_number`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			r, serr := scanRowWithTenant(rows)
			if serr != nil {
				return serr
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

// scanRowWithTenant scans a row whose leading column is tenant_id,
// followed by the canonical selectList columns. It is the cross-tenant
// counterpart of scanRow, which takes the tenant as a fixed argument.
func scanRowWithTenant(row pgx.Row) (audit.Row, error) {
	var (
		r        audit.Row
		tenantID string
		seq      int64
		payload  []byte
		prevHash []byte
	)
	if err := row.Scan(&tenantID, &seq, &r.EventType, &payload, &r.Timestamp, &prevHash, &r.ID, &r.EventSchemaVersion); err != nil {
		return audit.Row{}, err
	}
	r.TenantID = tenantID
	r.Seq = uint64(seq)
	r.Payload = json.RawMessage(payload)
	r.Timestamp = r.Timestamp.UTC()
	r.PrevHash = hex.EncodeToString(prevHash)
	r.Hash = audit.ComputeHash(r)
	return r, nil
}
