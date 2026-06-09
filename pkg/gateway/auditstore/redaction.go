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
	"github.com/lennylabs/lenny/pkg/gateway/pgtenant"
)

// userMatchKeys are the top-level audit payload keys that carry the acting
// or subject user identifier across the §11.7 Lenny → OCSF field mapping.
// The §12.8 step-14 dead-letter scan redacts a dead_lettered row whose
// payload names the target user in any of these positions ("equivalently,
// whose `payload` carries the target user as actor or subject" — the
// audit_log table has no `user_id` column).
//
// spec: §12.8 line 810.
var userMatchKeys = []string{
	"user_id", "userId", "sub", "actor", "actor_sub",
	"subject", "subject_user_id", "target_user_id", "targetUserId",
}

// deadLetteredUserPredicate builds the SQL OR-predicate that matches a
// dead-lettered row whose payload names userID in any user-identifying
// position. The keys are a fixed allow-list (userMatchKeys), so the
// predicate is parameterized only on the user id ($2); the column
// expressions are static and not attacker-controlled.
func deadLetteredUserPredicate() string {
	expr := ""
	for i, k := range userMatchKeys {
		if i > 0 {
			expr += " OR "
		}
		expr += fmt.Sprintf("payload->>'%s' = $2", k)
	}
	return expr
}

// DeadLetteredForUser returns the tenant's audit rows that the §12.8
// step-14 OCSF dead-letter PII redaction must scrub for userID: rows whose
// ocsf_translation_state is 'dead_lettered' and whose payload names the
// user as actor or subject. Already-redacted rows (payload carries the
// redaction marker) are excluded so a re-run of the erasure job is
// idempotent. The rows are returned in sequence order with their derived
// content hash, which the redaction service captures as the receipt's
// pre-redaction original_hash.
//
// spec: §12.8 line 810.
func (s *Store) DeadLetteredForUser(ctx context.Context, tenantID, userID string) ([]audit.Row, error) {
	if tenantID == "" || userID == "" {
		return nil, ErrEmptyScope
	}
	pool, err := s.readShard(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	var out []audit.Row
	q := `SELECT ` + selectList + ` FROM audit_log
		WHERE tenant_id = $1
		  AND ocsf_translation_state = 'dead_lettered'
		  AND (payload->>'` + audit.RedactedPayloadMarker + `') IS DISTINCT FROM 'true'
		  AND (` + deadLetteredUserPredicate() + `)
		ORDER BY sequence_number`
	err = pgtenant.InTx(ctx, pool, tenantID, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, q, tenantID, userID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			r, err := scanRow(rows, tenantID)
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

// RedactionReceiptRecord is one row of the §12.8 lines 812-827
// audit_redaction_receipts table. The redaction service computes the
// hashes and the detached signature; RedactDeadLettered persists it in the
// same transaction as the in-place payload rewrite so the row and its
// provenance token commit atomically.
type RedactionReceiptRecord struct {
	ReceiptID        string
	AuditEventID     string
	SequenceNumber   uint64
	OriginalHash     string // hex SHA-256 of the canonical tuple before redaction
	NewHash          string // hex SHA-256 of the canonical tuple after redaction
	ErasureJobID     string
	LegalBasis       string
	RedactorIdentity string
	Timestamp        time.Time
	Signature        []byte
	SignatureKMSKey  string
}

// RedactDeadLettered performs the §12.8 step-14 in-place redaction of one
// dead-lettered row and persists its signed RedactionReceipt in a single
// transaction. The payload is rewritten to the redacted shape and
// payload_canonical_json is recomputed from it; the (tenant_id,
// sequence_number, prev_hash, …) hash-input columns are left untouched, so
// the row's preserved pre-redaction hash (carried as the receipt's
// original_hash) keeps the chain linked while the content-hash break the
// rewrite introduces is authenticated by the receipt. The UPDATE runs
// under SET LOCAL lenny.erasure_mode = 'true' so the lenny_audit_immutability
// trigger admits the payload rewrite; the connection must run as the
// lenny_erasure role (migration 0165 grants it UPDATE on the two payload
// columns).
//
// spec: §12.8 lines 810-827; §11.7 item 7 (erasure_mode escape).
func (s *Store) RedactDeadLettered(ctx context.Context, tenantID string, seq uint64, newPayload json.RawMessage, rec RedactionReceiptRecord) error {
	if tenantID == "" {
		return ErrEmptyScope
	}
	pool, err := s.shard(ctx, tenantID)
	if err != nil {
		return err
	}
	origHash, err := hex.DecodeString(rec.OriginalHash)
	if err != nil {
		return fmt.Errorf("auditstore: decode original_hash: %w", err)
	}
	newHash, err := hex.DecodeString(rec.NewHash)
	if err != nil {
		return fmt.Errorf("auditstore: decode new_hash: %w", err)
	}
	canonical := string(audit.CanonicalPayload(newPayload))
	return pgtenant.InTx(ctx, pool, tenantID, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, "SET LOCAL lenny.erasure_mode = 'true'"); err != nil {
			return err
		}
		tag, err := tx.Exec(ctx, `UPDATE audit_log
			SET payload = $3::jsonb, payload_canonical_json = $4::jsonb
			WHERE tenant_id = $1 AND sequence_number = $2`,
			tenantID, int64(seq), string(newPayload), canonical)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrNotFound
		}
		_, err = tx.Exec(ctx, `INSERT INTO audit_redaction_receipts (
			receipt_id, audit_event_id, tenant_id, sequence_number,
			original_hash, new_hash, erasure_job_id, legal_basis,
			redactor_identity, "timestamp", signature, signature_kms_key_id
		) VALUES ($1::uuid, $2::uuid, $3, $4, $5, $6, $7::uuid, $8, $9, $10, $11, $12)`,
			rec.ReceiptID, rec.AuditEventID, tenantID, int64(seq),
			origHash, newHash, rec.ErasureJobID, rec.LegalBasis,
			rec.RedactorIdentity, rec.Timestamp.UTC(), rec.Signature, rec.SignatureKMSKey)
		return err
	})
}

// Receipts returns the tenant's §12.8 RedactionReceipts keyed by the
// redacted row's sequence number, so the chain verifier can reclassify the
// content-hash break a redaction introduces as a lawful redacted_gdpr
// discontinuity rather than a tamper. A row without a signature-bearing
// receipt is reported `broken` by the verifier (the missing-receipt
// incident the §16.5 AuditRedactionReceiptMissing alert pages on).
//
// spec: §12.8 lines 812-827; §11.7 line 370.
func (s *Store) Receipts(ctx context.Context, tenantID string) (map[uint64]audit.RedactionReceipt, error) {
	if tenantID == "" {
		return nil, ErrEmptyScope
	}
	pool, err := s.readShard(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	out := map[uint64]audit.RedactionReceipt{}
	err = pgtenant.InTx(ctx, pool, tenantID, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx,
			`SELECT sequence_number, original_hash, signature
			 FROM audit_redaction_receipts WHERE tenant_id = $1`, tenantID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var (
				seq      int64
				origHash []byte
				sig      []byte
			)
			if err := rows.Scan(&seq, &origHash, &sig); err != nil {
				return err
			}
			out[uint64(seq)] = audit.RedactionReceipt{
				RedactedSeq:  uint64(seq),
				OriginalHash: hex.EncodeToString(origHash),
				Signature:    string(sig),
			}
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// applyReceipts rewrites the Hash of each redacted row to the preserved
// pre-redaction hash the receipt carries, so the chain stays linked and
// the verifier sees a content-hash break that the receipt explains. A
// redaction-marked row with no receipt keeps its recomputed (post-rewrite)
// hash, which the verifier reports `broken` — the missing-receipt incident.
func applyReceipts(rows []audit.Row, receipts map[uint64]audit.RedactionReceipt) {
	if len(receipts) == 0 {
		return
	}
	for i := range rows {
		if !audit.IsRedactedPayload(rows[i].Payload) {
			continue
		}
		rows[i].Redacted = true
		if rcpt, ok := receipts[rows[i].Seq]; ok && rcpt.OriginalHash != "" {
			rows[i].Hash = rcpt.OriginalHash
		}
	}
}
