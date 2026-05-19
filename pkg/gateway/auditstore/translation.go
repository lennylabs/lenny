// SPDX-License-Identifier: MIT

package auditstore

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

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
// The query reads across tenants, so it runs without the per-tenant
// guard transaction — the lenny_tenant_guard trigger gates writes, not
// the cross-tenant read the translator needs.
func (s *Store) PendingTranslation(ctx context.Context, limit int) ([]ocsf.TranslatableRow, error) {
	if limit <= 0 {
		limit = 256
	}
	rows, err := s.pool.Query(ctx, `
		SELECT a.tenant_id, a.sequence_number, a.id, a.event_type,
		       a.event_schema_version, a.created_at, a.payload,
		       a.prev_hash, a.ocsf_translation_state, a.retry_count,
		       t.genesis_nonce
		FROM audit_log a
		LEFT JOIN tenants t ON t.id = a.tenant_id
		WHERE a.ocsf_translation_state IN ('pending', 'retry_pending')
		ORDER BY a.created_at ASC, a.tenant_id, a.sequence_number
		LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("auditstore: query pending translations: %w", err)
	}
	defer rows.Close()
	var out []ocsf.TranslatableRow
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
			return nil, fmt.Errorf("auditstore: scan pending translation: %w", err)
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
		// The genesis nonce is surfaced on the first row of a tenant
		// chain only (§11.7 field mapping).
		if seq == 1 && len(genesisNonce) > 0 {
			in.GenesisNonce = hex.EncodeToString(genesisNonce)
		}
		out = append(out, ocsf.TranslatableRow{
			Input:      in,
			Topic:      string(translationTopic),
			State:      audit.OCSFTranslationState(state),
			RetryCount: retryCount,
		})
	}
	return out, rows.Err()
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
	return pgtenant.InTx(ctx, s.pool, tenantID, func(tx pgx.Tx) error {
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
	return pgtenant.InTx(ctx, s.pool, tenantID, func(tx pgx.Tx) error {
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
	var state string
	var retryCount int
	err := pgtenant.InTx(ctx, s.pool, tenantID, func(tx pgx.Tx) error {
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
	var state string
	var retryCount int
	err := pgtenant.InTx(ctx, s.pool, tenantID, func(tx pgx.Tx) error {
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
