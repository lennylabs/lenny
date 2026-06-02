// SPDX-License-Identifier: MIT

package auditstore

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lennylabs/lenny/pkg/audit"
	"github.com/lennylabs/lenny/pkg/audit/ocsf"
	"github.com/lennylabs/lenny/pkg/audit/siem"
	"github.com/lennylabs/lenny/pkg/gateway/pgtenant"
)

// PendingForward implements siem.DeliveryStore. It returns up to limit
// committed audit rows whose sequence_number is past their tenant
// chain's acknowledged high-water mark in siem_delivery_state,
// oldest-first across all tenants. The §12.3 SIEM outbox forwarder
// drives this every poll interval; the rows it returns are the
// committed-but-unacknowledged tail the forwarder delivers next.
//
// Like PendingTranslation this is a platform-admin cross-tenant read:
// it runs inside a pgtenant.InAllTenants transaction so the §4.2
// `__all__` sentinel satisfies the §12.3 lenny_tenant_isolation RLS
// policy under the non-superuser lenny_app role, and one
// cross_tenant_read audit event (category audit_siem_forwarder) is
// emitted per worker invocation per §12.3 line 141.
//
// spec: §12.3 line 97 (outbox forwarder); §12.3 line 141 (cross-tenant
// read audit).
func (s *Store) PendingForward(ctx context.Context, limit int) ([]siem.ForwardRow, error) {
	if limit <= 0 {
		limit = 256
	}
	shards, err := s.allShards(ctx)
	if err != nil {
		return nil, err
	}
	var out []siem.ForwardRow
	for _, sh := range shards {
		batch, berr := s.pendingForwardOnShard(ctx, sh.Pool, limit)
		if berr != nil {
			return nil, berr
		}
		out = append(out, batch...)
	}
	if err := s.emitCrossTenantRead(ctx, "audit_siem_forwarder", len(out)); err != nil {
		return nil, err
	}
	return out, nil
}

// pendingForwardOnShard runs the per-shard committed-tail scan that
// PendingForward fans across every audit shard.
func (s *Store) pendingForwardOnShard(ctx context.Context, pool *pgxpool.Pool, limit int) ([]siem.ForwardRow, error) {
	var out []siem.ForwardRow
	err := pgtenant.InAllTenants(ctx, pool, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
		SELECT a.tenant_id, a.sequence_number, a.id, a.event_type,
		       a.event_schema_version, a.created_at, a.payload,
		       a.prev_hash, t.genesis_nonce
		FROM audit_log a
		-- platform-admin-cross-tenant-allowed
		-- platform-admin-cross-tenant-justification: the SIEM outbox forwarder is a platform-internal background worker that tails the committed audit tail for every tenant; the join to siem_delivery_state pairs each audit row with its own tenant chain's delivery high-water mark (s.tenant_id = a.tenant_id) and the join to tenants pairs it with that tenant's genesis nonce (t.id = a.tenant_id).
		LEFT JOIN siem_delivery_state s ON s.tenant_id = a.tenant_id
		LEFT JOIN tenants t ON t.id = a.tenant_id
		WHERE a.sequence_number > COALESCE(s.last_acked_sequence, 0)
		ORDER BY a.created_at ASC, a.tenant_id, a.sequence_number
		LIMIT $1`, limit)
		if err != nil {
			return fmt.Errorf("auditstore: query pending forward rows: %w", err)
		}
		defer rows.Close()
		var inner []siem.ForwardRow
		for rows.Next() {
			var (
				tenantID, eventType, schemaVer  string
				seq                             int64
				id                              string
				createdAt                       time.Time
				payload, prevHash, genesisNonce []byte
			)
			if err := rows.Scan(&tenantID, &seq, &id, &eventType, &schemaVer,
				&createdAt, &payload, &prevHash, &genesisNonce); err != nil {
				return fmt.Errorf("auditstore: scan pending forward row: %w", err)
			}
			in := ocsf.Input{
				ID:                 id,
				Sequence:           uint64(seq),
				TenantID:           tenantID,
				EventType:          eventType,
				EventSchemaVersion: schemaVer,
				CreatedAtUnixMs:    createdAt.UTC().UnixMilli(),
				Payload:            json.RawMessage(payload),
				PrevHash:           hex.EncodeToString(prevHash),
				ChainIntegrity:     audit.ChainUnchecked,
			}
			if seq == 1 && len(genesisNonce) > 0 {
				in.GenesisNonce = hex.EncodeToString(genesisNonce)
			}
			inner = append(inner, siem.ForwardRow{
				TenantID:  tenantID,
				Sequence:  uint64(seq),
				Input:     in,
				Topic:     string(translationTopic),
				CreatedAt: createdAt,
			})
		}
		out = inner
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// Checkpoint implements siem.DeliveryStore. It advances a tenant
// chain's SIEM delivery high-water mark in siem_delivery_state to seq
// (committed at ackedAt) after the SIEM acknowledges the record. The
// ON CONFLICT guard advances the mark monotonically so an idempotent
// re-delivery of an already-acknowledged row never regresses the
// pointer (no duplication or gap on a forwarder restart).
//
// siem_delivery_state carries no lenny_tenant_guard trigger (it is
// platform-internal forwarder bookkeeping), so the upsert runs on the
// audit shard pool directly without a per-tenant transaction context.
//
// spec: §12.3 line 97.
func (s *Store) Checkpoint(ctx context.Context, tenantID string, seq uint64, ackedAt time.Time) error {
	pool, err := s.shard(ctx, tenantID)
	if err != nil {
		return err
	}
	_, err = pool.Exec(ctx, `
		INSERT INTO siem_delivery_state
			(tenant_id, last_acked_sequence, last_acked_created_at, updated_at)
		VALUES ($1, $2, $3, now())
		ON CONFLICT (tenant_id) DO UPDATE
		SET last_acked_sequence = EXCLUDED.last_acked_sequence,
		    last_acked_created_at = EXCLUDED.last_acked_created_at,
		    updated_at = now()
		WHERE siem_delivery_state.last_acked_sequence < EXCLUDED.last_acked_sequence`,
		tenantID, int64(seq), ackedAt.UTC())
	if err != nil {
		return fmt.Errorf("auditstore: checkpoint siem delivery (tenant %s seq %d): %w", tenantID, seq, err)
	}
	return nil
}

// DeliveryLag implements siem.DeliveryStore. It returns the seconds
// between the latest committed audit event and the latest
// SIEM-acknowledged event, the §16.1
// lenny_audit_siem_delivery_lag_seconds value. Before any
// acknowledgement the acknowledged point falls back to the earliest
// committed event so the lag reports the full backlog span rather than
// a misleading zero. The value is the max over audit shards.
//
// The aggregate read runs under the same `__all__` worker invocation as
// PendingForward (which emits the per-invocation cross_tenant_read), so
// it does not emit a second cross_tenant_read.
//
// spec: §16.1 line 228.
func (s *Store) DeliveryLag(ctx context.Context) (float64, error) {
	shards, err := s.allShards(ctx)
	if err != nil {
		return 0, err
	}
	var maxLag float64
	for _, sh := range shards {
		lag, lerr := s.deliveryLagOnShard(ctx, sh.Pool)
		if lerr != nil {
			return 0, lerr
		}
		if lag > maxLag {
			maxLag = lag
		}
	}
	return maxLag, nil
}

func (s *Store) deliveryLagOnShard(ctx context.Context, pool *pgxpool.Pool) (float64, error) {
	var lag float64
	err := pgtenant.InAllTenants(ctx, pool, func(tx pgx.Tx) error {
		// platform-admin-cross-tenant-allowed
		// platform-admin-cross-tenant-justification: the SIEM delivery-lag gauge is a platform-internal aggregate over the committed audit tail and the per-tenant delivery high-water marks; it reads no per-tenant row contents, only the latest committed and latest acknowledged timestamps across all chains.
		return tx.QueryRow(ctx, `
		SELECT GREATEST(0, COALESCE(EXTRACT(EPOCH FROM (
			(SELECT MAX(created_at) FROM audit_log)
			- COALESCE(
				(SELECT MAX(last_acked_created_at) FROM siem_delivery_state),
				(SELECT MIN(created_at) FROM audit_log)
			)
		)), 0))`).Scan(&lag)
	})
	if err != nil {
		return 0, fmt.Errorf("auditstore: read siem delivery lag: %w", err)
	}
	return lag, nil
}

var _ siem.DeliveryStore = (*Store)(nil)
