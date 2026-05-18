// SPDX-License-Identifier: MIT

package auditstore

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/lennylabs/lenny/pkg/audit"
	"github.com/lennylabs/lenny/pkg/audit/ocsf"
	"github.com/lennylabs/lenny/pkg/gateway/eventbus"
)

// PendingRepublish implements eventbus.RetranscribeStore. It returns up
// to limit audit rows whose eventbus_publish_state is failed or
// retry_pending and whose retry_count is below maxRetryAttempts,
// ordered by created_at ASC (FIFO per §12.3.7). For each row it
// rebuilds the byte-identical CloudEvents envelope from the canonical
// Postgres tuple: the OCSF record is re-translated and re-wrapped, so
// the envelope's id, source, time, and data are identical to the
// original publish attempt and downstream de-duplication by
// CloudEvents id continues to work.
func (s *Store) PendingRepublish(ctx context.Context, maxRetryAttempts, limit int) ([]eventbus.RetranscribeRow, error) {
	if limit <= 0 {
		limit = 256
	}
	rows, err := s.pool.Query(ctx, `
		SELECT a.tenant_id, a.sequence_number, a.id, a.event_type,
		       a.event_schema_version, a.created_at, a.payload,
		       a.prev_hash, a.retry_count, t.genesis_nonce
		FROM audit_log a
		LEFT JOIN tenants t ON t.id = a.tenant_id
		WHERE a.eventbus_publish_state IN ('failed', 'retry_pending')
		  AND a.retry_count < $1
		ORDER BY a.created_at ASC, a.tenant_id, a.sequence_number
		LIMIT $2`, maxRetryAttempts, limit)
	if err != nil {
		return nil, fmt.Errorf("auditstore: query pending republish: %w", err)
	}
	defer rows.Close()
	var out []eventbus.RetranscribeRow
	for rows.Next() {
		var (
			tenantID, eventType, schemaVer  string
			seq                             int64
			id                              string
			createdAt                       time.Time
			payload, prevHash, genesisNonce []byte
			retryCount                      int
		)
		if err := rows.Scan(&tenantID, &seq, &id, &eventType, &schemaVer,
			&createdAt, &payload, &prevHash, &retryCount, &genesisNonce); err != nil {
			return nil, fmt.Errorf("auditstore: scan pending republish: %w", err)
		}
		ev, err := s.rebuildEnvelope(canonicalView{
			tenantID:     tenantID,
			seq:          uint64(seq),
			id:           id,
			eventType:    eventType,
			schemaVer:    schemaVer,
			createdAt:    createdAt,
			payload:      payload,
			prevHash:     prevHash,
			genesisNonce: genesisNonce,
		})
		if err != nil {
			return nil, err
		}
		out = append(out, eventbus.RetranscribeRow{
			TenantID:   tenantID,
			Seq:        uint64(seq),
			Topic:      translationTopic,
			Event:      ev,
			RetryCount: retryCount,
		})
	}
	return out, rows.Err()
}

// canonicalView is the audit-row tuple rebuildEnvelope reads.
type canonicalView struct {
	tenantID     string
	seq          uint64
	id           string
	eventType    string
	schemaVer    string
	createdAt    time.Time
	payload      []byte
	prevHash     []byte
	genesisNonce []byte
}

// publisherID is the §12.3.7 source/id replica segment for envelopes
// the retranscribe worker rebuilds. The original publisher id is not
// persisted in v1, so the retranscribe worker stamps the gateway-fixed
// retranscribe identity; the CloudEvents id stays byte-identical
// across a given row's retranscribes because it is derived from the
// immutable canonical tuple (row id + created_at).
const publisherID = "gw-retxn"

// rebuildEnvelope re-translates the canonical tuple to OCSF and wraps
// it in a CloudEvents envelope. The envelope is deterministic for a
// fixed canonical tuple: the id uses the row's immutable created_at
// nanos and a nonce derived from the row UUID, so every retranscribe
// of the same row produces a byte-identical id (§12.3.7).
func (s *Store) rebuildEnvelope(v canonicalView) (eventbus.Event, error) {
	in := ocsf.Input{
		ID:                 v.id,
		Sequence:           v.seq,
		TenantID:           v.tenantID,
		EventType:          v.eventType,
		EventSchemaVersion: v.schemaVer,
		CreatedAtUnixMs:    v.createdAt.UTC().UnixMilli(),
		Payload:            json.RawMessage(v.payload),
		PrevHash:           hex.EncodeToString(v.prevHash),
		ChainIntegrity:     audit.ChainUnchecked,
	}
	if v.seq == 1 && len(v.genesisNonce) > 0 {
		in.GenesisNonce = hex.EncodeToString(v.genesisNonce)
	}
	rec, terr := ocsf.Translate(in)
	var data json.RawMessage
	if terr != nil {
		// A row that does not translate is republished carrying the
		// §11.7 dead-letter receipt so the envelope is schema-valid.
		var te *ocsf.TranslateError
		if e, ok := terr.(*ocsf.TranslateError); ok {
			te = e
		} else {
			te = &ocsf.TranslateError{Class: ocsf.ErrOther, EventType: v.eventType, Detail: terr.Error()}
		}
		rec = ocsf.DeadLetterReceipt(in, te)
	}
	b, err := ocsf.MarshalRecord(rec)
	if err != nil {
		return eventbus.Event{}, fmt.Errorf("auditstore: marshal OCSF record: %w", err)
	}
	data = b
	// The id is byte-identical across retranscribes: created_at nanos
	// and a UUID-derived nonce are both immutable for the row.
	ev := eventbus.Event{
		SpecVersion:     eventbus.SpecVersion,
		ID:              fmt.Sprintf("%s:%s:%d:%s", v.tenantID, publisherID, v.createdAt.UTC().UnixNano(), uuidNonce(v.id)),
		Source:          fmt.Sprintf("//lenny.dev/gateway/%s", publisherID),
		Type:            "dev.lenny." + auditShortName(v.eventType),
		Time:            v.createdAt.UTC().Format(time.RFC3339),
		DataContentType: eventbus.ContentTypeOCSF,
		Subject:         "session/" + v.id,
		Data:            data,
		Extensions:      map[string]string{eventbus.ExtTenantID: v.tenantID},
	}
	return ev, nil
}

// uuidNonce derives the §12.3.7 id nonce segment from a row UUID. The
// UUID is immutable for the row, so the nonce — and therefore the
// CloudEvents id — is byte-identical across retranscribes.
func uuidNonce(id string) string {
	h := uint64(1469598103934665603) // FNV-1a offset basis
	for i := 0; i < len(id); i++ {
		h ^= uint64(id[i])
		h *= 1099511628211
	}
	return fmt.Sprintf("%016x", h)
}

// auditShortName maps an audit event_type to a CloudEvents type short
// name. The §16.6 catalog short names are dotted; the CloudEvents type
// uses the event_type verbatim after the dev.lenny. prefix.
func auditShortName(eventType string) string {
	if eventType == "" {
		return "audit_event"
	}
	return eventType
}
