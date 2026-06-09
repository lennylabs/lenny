// SPDX-License-Identifier: MIT

// Package deadletterredaction implements the §12.8 step-14 OCSF
// dead-letter PII redaction: the sub-step of a GDPR DeleteByUser erasure
// that scrubs raw canonical PII out of audit_log rows the OCSF translator
// dead-lettered, while preserving the row for pipeline-failure forensics
// and keeping the §11.7 hash chain verifiable through a signed
// RedactionReceipt.
//
// For each dead-lettered row that names the target user, the service:
//   - rewrites the payload to the §12.8 line 810 redacted shape and
//     recomputes payload_canonical_json (the raw PII is gone; the event
//     type and OCSF error class are preserved);
//   - persists a KMS-signed RedactionReceipt pinning the (original_hash,
//     new_hash) discontinuity to a specific erasure job and legal basis;
//   - emits gdpr.erasure_deadletter_redacted per redacted row (the
//     in-system erasure record), and
//   - emits gdpr.erasure_deadletter_downstream_notified per redacted row
//     (the GDPR Art. 17(2) signal that flows through the OCSF translator
//     to every sink that ingested the pre-redaction dead-letter receipt).
//
// spec: §12.8 lines 810-829; §16.7 lines 691-692.
package deadletterredaction

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/lennylabs/lenny/pkg/audit"
	"github.com/lennylabs/lenny/pkg/audit/jcs"
	"github.com/lennylabs/lenny/pkg/gateway/auditstore"
)

// Store is the §12.8 audit-chain surface the redaction drives: it finds
// the dead-lettered rows that name a user and rewrites one in place under
// a signed receipt. *auditstore.Store satisfies it.
type Store interface {
	DeadLetteredForUser(ctx context.Context, tenantID, userID string) ([]audit.Row, error)
	RedactDeadLettered(ctx context.Context, tenantID string, seq uint64, newPayload json.RawMessage, rec auditstore.RedactionReceiptRecord) error
}

// Emitter appends the two §16.7 redaction audit events to the tenant's
// chain. The auditstore Store / admin AuditLog Append satisfies it.
type Emitter interface {
	Append(ctx context.Context, tenantID, eventType string, payload json.RawMessage, at time.Time) (audit.Row, error)
}

// Signer produces the detached signature over a RedactionReceipt's
// canonical tuple, using the KMS-held audit signing key. The signature is
// the sole provenance token that distinguishes an authorized GDPR
// redaction from a tamper that merely cleared the payload (§12.8 line 810).
type Signer interface {
	Sign(ctx context.Context, message []byte) (signature []byte, keyID string, err error)
}

// Classifier returns the OCSF translation error class preserved on a
// redacted row (`class_mapping_missing`, `schema_violation`, …). It
// re-derives the failure label for a dead-lettered row; a nil classifier
// records "unknown".
type Classifier func(row audit.Row) string

// Event type constants (§16.7 lines 691-692).
const (
	eventRedacted           = "gdpr.erasure_deadletter_redacted"
	eventDownstreamNotified = "gdpr.erasure_deadletter_downstream_notified"
)

// Service runs the §12.8 step-14 redaction for a user's dead-lettered
// audit rows.
type Service struct {
	store      Store
	emit       Emitter
	signer     Signer
	classify   Classifier
	legalBasis string
	redactor   string
	clock      func() time.Time
	observe    func(tenantID string, redacted int)
}

// Config builds a Service.
type Config struct {
	// Store finds and rewrites dead-lettered rows.
	Store Store
	// Emit appends the two redaction audit events.
	Emit Emitter
	// Signer signs each RedactionReceipt with the audit signing key.
	Signer Signer
	// Classify re-derives the preserved OCSF error class per row. Optional.
	Classify Classifier
	// LegalBasis is the receipt's legal basis. One of gdpr_art17,
	// gdpr_art17_with_art17_3_exception, operator_acknowledged_override.
	// Empty defaults to gdpr_art17.
	LegalBasis string
	// Redactor identifies the principal performing the erasure, recorded
	// as the receipt's redactor_identity. Empty defaults to
	// "system:gdpr-erasure".
	Redactor string
	// Clock supplies the redaction timestamp. Nil defaults to time.Now UTC.
	Clock func() time.Time
	// Observe receives the per-tenant redacted-row count for the
	// lenny_gdpr_deadletter_redacted_total metric. Optional.
	Observe func(tenantID string, redacted int)
}

// New builds a redaction Service from cfg. Store, Emit, and Signer are
// required.
func New(cfg Config) *Service {
	clock := cfg.Clock
	if clock == nil {
		clock = func() time.Time { return time.Now().UTC() }
	}
	basis := cfg.LegalBasis
	if basis == "" {
		basis = "gdpr_art17"
	}
	redactor := cfg.Redactor
	if redactor == "" {
		redactor = "system:gdpr-erasure"
	}
	return &Service{
		store:      cfg.Store,
		emit:       cfg.Emit,
		signer:     cfg.Signer,
		classify:   cfg.Classify,
		legalBasis: basis,
		redactor:   redactor,
		clock:      clock,
		observe:    cfg.Observe,
	}
}

// RedactForUser runs the §12.8 step-14 redaction for every dead-lettered
// audit row that names userID in tenantID. It returns the number of rows
// redacted. The whole call shares one erasure_job_id (a fresh UUID) so the
// two events emitted per row and the receipts written are joinable to the
// erasure operation. It is fail-fast: a redaction or emit error aborts and
// is returned with the rows redacted so far already durably committed (the
// redaction-then-event order means a row is never left scrubbed without an
// in-system erasure record).
//
// spec: §12.8 lines 810-829.
func (s *Service) RedactForUser(ctx context.Context, tenantID, userID string) (int, error) {
	if tenantID == "" || userID == "" {
		return 0, auditstore.ErrEmptyScope
	}
	rows, err := s.store.DeadLetteredForUser(ctx, tenantID, userID)
	if err != nil {
		return 0, fmt.Errorf("deadletterredaction: scan dead-lettered rows: %w", err)
	}
	if len(rows) == 0 {
		return 0, nil
	}
	erasureJobID := uuid.NewString()
	redacted := 0
	for _, row := range rows {
		if err := s.redactRow(ctx, tenantID, erasureJobID, row); err != nil {
			if redacted > 0 && s.observe != nil {
				s.observe(tenantID, redacted)
			}
			return redacted, err
		}
		redacted++
	}
	if s.observe != nil {
		s.observe(tenantID, redacted)
	}
	return redacted, nil
}

// redactRow scrubs one dead-lettered row, persists its signed receipt, and
// emits the paired §16.7 events.
func (s *Service) redactRow(ctx context.Context, tenantID, erasureJobID string, row audit.Row) error {
	errorClass := "unknown"
	if s.classify != nil {
		if c := s.classify(row); c != "" {
			errorClass = c
		}
	}
	redactedAt := s.clock().UTC()
	newPayload := audit.BuildRedactedPayload(row.EventType, errorClass, redactedAt)

	// The pre-redaction content hash is the row's derived hash; the
	// post-redaction hash is computed over the redacted canonical tuple.
	originalHash := row.Hash
	redactedRow := row
	redactedRow.Payload = newPayload
	newHash := audit.ComputeHash(redactedRow)

	receiptID := uuid.NewString()
	sig, keyID, err := s.signReceipt(ctx, receiptID, row.ID, tenantID, row.Seq,
		originalHash, newHash, erasureJobID, redactedAt)
	if err != nil {
		return fmt.Errorf("deadletterredaction: sign receipt for seq %d: %w", row.Seq, err)
	}

	if err := s.store.RedactDeadLettered(ctx, tenantID, row.Seq, newPayload, auditstore.RedactionReceiptRecord{
		ReceiptID:        receiptID,
		AuditEventID:     row.ID,
		SequenceNumber:   row.Seq,
		OriginalHash:     originalHash,
		NewHash:          newHash,
		ErasureJobID:     erasureJobID,
		LegalBasis:       s.legalBasis,
		RedactorIdentity: s.redactor,
		Timestamp:        redactedAt,
		Signature:        sig,
		SignatureKMSKey:  keyID,
	}); err != nil {
		return fmt.Errorf("deadletterredaction: redact seq %d: %w", row.Seq, err)
	}

	// gdpr.erasure_deadletter_redacted — the in-system erasure record.
	redactedPayload, _ := json.Marshal(map[string]any{
		"audit_event_id":           row.ID,
		"tenant_id":                tenantID,
		"original_event_type":      row.EventType,
		"original_error_class":     errorClass,
		"redacted_at":              redactedAt.Format(time.RFC3339Nano),
		"pre_redaction_prev_hash":  originalHash,
		"post_redaction_prev_hash": newHash,
		"redaction_receipt_id":     receiptID,
	})
	if _, err := s.emit.Append(ctx, tenantID, eventRedacted, redactedPayload, redactedAt); err != nil {
		return fmt.Errorf("deadletterredaction: emit %s: %w", eventRedacted, err)
	}

	// gdpr.erasure_deadletter_downstream_notified — GDPR Art. 17(2) signal
	// flowing through the OCSF translator (class 5001, activity Delete) to
	// the sinks that ingested the pre-redaction dead-letter receipt. Carries
	// only hashes and identifiers, never raw canonical PII.
	notifyPayload, _ := json.Marshal(map[string]any{
		"audit_event_id":             row.ID,
		"tenant_id":                  tenantID,
		"original_event_type":        row.EventType,
		"original_sequence_number":   row.Seq,
		"original_hash":              originalHash,
		"erasure_job_id":             erasureJobID,
		"legal_basis":                s.legalBasis,
		"redaction_receipt_id":       receiptID,
		"redacted_at":                redactedAt.Format(time.RFC3339Nano),
		"downstream_action_required": true,
	})
	if _, err := s.emit.Append(ctx, tenantID, eventDownstreamNotified, notifyPayload, redactedAt); err != nil {
		return fmt.Errorf("deadletterredaction: emit %s: %w", eventDownstreamNotified, err)
	}
	return nil
}

// signReceipt signs the RedactionReceipt's canonical tuple. The tuple is
// JCS-serialized so signer and verifier agree on the signed bytes
// regardless of key order.
func (s *Service) signReceipt(ctx context.Context, receiptID, auditEventID, tenantID string, seq uint64, originalHash, newHash, erasureJobID string, at time.Time) ([]byte, string, error) {
	tuple, _ := json.Marshal(map[string]any{
		"receipt_id":        receiptID,
		"audit_event_id":    auditEventID,
		"tenant_id":         tenantID,
		"sequence_number":   seq,
		"original_hash":     originalHash,
		"new_hash":          newHash,
		"erasure_job_id":    erasureJobID,
		"legal_basis":       s.legalBasis,
		"redactor_identity": s.redactor,
		"timestamp":         at.Format(time.RFC3339Nano),
	})
	canon, err := jcs.Canonicalize(tuple)
	if err != nil {
		canon = tuple
	}
	return s.signer.Sign(ctx, canon)
}
