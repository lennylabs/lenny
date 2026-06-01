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
	"github.com/lennylabs/lenny/pkg/gateway/eventbus"
	"github.com/lennylabs/lenny/pkg/gateway/pgtenant"
)

// translationTopic is the §12.3.7 EventTopic stamped on audit-bearing
// events the translator and retranscribe worker re-publish. Audit
// events are session-scoped lifecycle records, so they ride the
// session_lifecycle topic.
const translationTopic = eventbus.TopicSessionLifecycle

// PendingTranslation implements ocsf.TranslationStore. It returns up to
// limit audit rows whose ocsf_translation_state is pending or
// retry_pending, oldest-first across all tenants, as ocsf.Input values
// built from the canonical Postgres tuple (§11.7).
//
// The query is a platform-admin cross-tenant read: it runs inside a
// pgtenant.InAllTenants transaction so the §4.2 `__all__` sentinel
// satisfies the §12.3 lenny_tenant_isolation RLS policy under the
// non-superuser lenny_app role, and a cross_tenant_read audit event
// is emitted (§12.3 line 141) recording the worker identity and the
// query category (audit_ocsf_translation_worker).
func (s *Store) PendingTranslation(ctx context.Context, limit int) ([]ocsf.TranslatableRow, error) {
	if limit <= 0 {
		limit = 256
	}
	shards, err := s.allShards(ctx)
	if err != nil {
		return nil, err
	}
	var out []ocsf.TranslatableRow
	for _, sh := range shards {
		batch, berr := s.pendingTranslationOnShard(ctx, sh.Pool, limit)
		if berr != nil {
			return nil, berr
		}
		out = append(out, batch...)
	}
	// §12.3 line 141: every code path that sets app.current_tenant
	// = '__all__' MUST emit one cross_tenant_read audit event per
	// API/worker invocation. The OCSF translation worker uses the
	// `audit_ocsf_translation_worker` category.
	if err := s.emitCrossTenantRead(ctx, "audit_ocsf_translation_worker", len(out)); err != nil {
		return nil, err
	}
	return out, nil
}

// pendingTranslationOnShard runs the §11.7 pending-translation scan on a
// single audit shard pool. PendingTranslation fans it across every
// shard returned by the §12.3 R-03 router.
func (s *Store) pendingTranslationOnShard(ctx context.Context, pool *pgxpool.Pool, limit int) ([]ocsf.TranslatableRow, error) {
	var out []ocsf.TranslatableRow
	err := pgtenant.InAllTenants(ctx, pool, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
		SELECT a.tenant_id, a.sequence_number, a.id, a.event_type,
		       a.event_schema_version, a.created_at, a.payload,
		       a.prev_hash, a.ocsf_translation_state, a.retry_count,
		       t.genesis_nonce
		FROM audit_log a
		-- platform-admin-cross-tenant-allowed
		-- platform-admin-cross-tenant-justification: the OCSF translation worker is a platform-internal background worker that drains the pending-translation queue for every tenant; the join pairs each audit row with its own tenant row (t.id = a.tenant_id) to read that tenant's genesis nonce.
		LEFT JOIN tenants t ON t.id = a.tenant_id
		WHERE a.ocsf_translation_state IN ('pending', 'retry_pending')
		ORDER BY a.created_at ASC, a.tenant_id, a.sequence_number
		LIMIT $1`, limit)
		if err != nil {
			return fmt.Errorf("auditstore: query pending translations: %w", err)
		}
		defer rows.Close()
		var inner []ocsf.TranslatableRow
		for rows.Next() {
			var (
				tenantID, eventType, schemaVer, state string
				seq                                   int64
				id                                    string
				createdAt                             time.Time
				payload, prevHash, genesisNonce       []byte
				retryCount                            int
			)
			if err := rows.Scan(&tenantID, &seq, &id, &eventType, &schemaVer,
				&createdAt, &payload, &prevHash, &state, &retryCount, &genesisNonce); err != nil {
				return fmt.Errorf("auditstore: scan pending translation: %w", err)
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
			inner = append(inner, ocsf.TranslatableRow{
				Input:      in,
				Topic:      string(translationTopic),
				State:      audit.OCSFTranslationState(state),
				RetryCount: retryCount,
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

// emitCrossTenantRead writes the §12.3 line 141 cross_tenant_read
// audit event to the `platform` audit chain. category names the
// background worker category that ran the cross-tenant SELECT;
// rowCount records how many rows the worker observed (purely
// observability — it is not load-bearing for the audit chain).
func (s *Store) emitCrossTenantRead(ctx context.Context, category string, rowCount int) error {
	payload := []byte(fmt.Sprintf(`{"category":%q,"row_count":%d}`, category, rowCount))
	_, err := s.Append(ctx, "platform", "cross_tenant_read", payload, time.Time{})
	if err != nil {
		return fmt.Errorf("auditstore: emit cross_tenant_read (%s): %w", category, err)
	}
	return nil
}

// SetTranslationState implements ocsf.TranslationStore. It transitions
// a row's ocsf_translation_state and sets retry_count under the
// per-tenant audit advisory lock so the write is serialized with the
// audit-write path. Neither column is part of the §11.7
// payload_canonical_json hash input, so the chain is never re-hashed.
func (s *Store) SetTranslationState(ctx context.Context, tenantID string, seq uint64,
	state audit.OCSFTranslationState, retryCount int,
) error {
	if !state.IsValid() {
		return fmt.Errorf("auditstore: %q is not a §11.7 OCSF translation state", state)
	}
	pool, err := s.shard(ctx, tenantID)
	if err != nil {
		return err
	}
	return pgtenant.InTx(ctx, pool, tenantID, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx,
			`SELECT pg_advisory_xact_lock(hashtext($1))`, "audit:"+tenantID); err != nil {
			return err
		}
		ct, err := tx.Exec(ctx, `
			UPDATE audit_log
			SET ocsf_translation_state = $3, retry_count = $4
			WHERE tenant_id = $1 AND sequence_number = $2`,
			tenantID, int64(seq), string(state), retryCount)
		if err != nil {
			return err
		}
		if ct.RowsAffected() == 0 {
			return ErrNotFound
		}
		return nil
	})
}

// SetPublishState implements eventbus.PublishStateStore and
// eventbus.RetranscribeStore's writer half. It transitions a row's
// eventbus_publish_state and sets retry_count under the per-tenant
// audit advisory lock. Neither column is in the §11.7 hash input.
func (s *Store) SetPublishState(ctx context.Context, tenantID string, seq uint64,
	state eventbus.PublishState, retryCount int,
) error {
	if !state.IsValid() {
		return fmt.Errorf("auditstore: %q is not a §12.3.7 publish state", state)
	}
	pool, err := s.shard(ctx, tenantID)
	if err != nil {
		return err
	}
	return pgtenant.InTx(ctx, pool, tenantID, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx,
			`SELECT pg_advisory_xact_lock(hashtext($1))`, "audit:"+tenantID); err != nil {
			return err
		}
		ct, err := tx.Exec(ctx, `
			UPDATE audit_log
			SET eventbus_publish_state = $3, retry_count = $4
			WHERE tenant_id = $1 AND sequence_number = $2`,
			tenantID, int64(seq), string(state), retryCount)
		if err != nil {
			return err
		}
		if ct.RowsAffected() == 0 {
			return ErrNotFound
		}
		return nil
	})
}

// TranslationState returns a row's current ocsf_translation_state and
// retry_count. Callers use it to confirm a state-machine transition.
func (s *Store) TranslationState(ctx context.Context, tenantID string, seq uint64) (audit.OCSFTranslationState, int, error) {
	pool, err := s.shard(ctx, tenantID)
	if err != nil {
		return "", 0, err
	}
	var state string
	var retryCount int
	err = pgtenant.InTx(ctx, pool, tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			SELECT ocsf_translation_state, retry_count FROM audit_log
			WHERE tenant_id = $1 AND sequence_number = $2`,
			tenantID, int64(seq)).Scan(&state, &retryCount)
	})
	if err == pgx.ErrNoRows {
		return "", 0, ErrNotFound
	}
	if err != nil {
		return "", 0, err
	}
	return audit.OCSFTranslationState(state), retryCount, nil
}

// PublishState returns a row's current eventbus_publish_state and
// retry_count.
func (s *Store) PublishState(ctx context.Context, tenantID string, seq uint64) (eventbus.PublishState, int, error) {
	pool, err := s.shard(ctx, tenantID)
	if err != nil {
		return "", 0, err
	}
	var state string
	var retryCount int
	err = pgtenant.InTx(ctx, pool, tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			SELECT eventbus_publish_state, retry_count FROM audit_log
			WHERE tenant_id = $1 AND sequence_number = $2`,
			tenantID, int64(seq)).Scan(&state, &retryCount)
	})
	if err == pgx.ErrNoRows {
		return "", 0, ErrNotFound
	}
	if err != nil {
		return "", 0, err
	}
	return eventbus.PublishState(state), retryCount, nil
}
