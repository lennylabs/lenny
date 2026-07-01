// SPDX-License-Identifier: MIT

package deadletterredaction

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/audit"
	"github.com/lennylabs/lenny/pkg/gateway/audit/auditstore"
)

// fakeStore records the redaction calls and serves a fixed dead-lettered
// row set.
type fakeStore struct {
	rows      []audit.Row
	scanErr   error
	redactErr error
	redacted  []auditstore.RedactionReceiptRecord
	payloads  map[uint64]json.RawMessage
}

func (f *fakeStore) DeadLetteredForUser(_ context.Context, _, _ string) ([]audit.Row, error) {
	return f.rows, f.scanErr
}

func (f *fakeStore) RedactDeadLettered(_ context.Context, _ string, _ uint64, newPayload json.RawMessage, rec auditstore.RedactionReceiptRecord) error {
	if f.redactErr != nil {
		return f.redactErr
	}
	f.redacted = append(f.redacted, rec)
	if f.payloads == nil {
		f.payloads = map[uint64]json.RawMessage{}
	}
	f.payloads[rec.SequenceNumber] = newPayload
	return nil
}

type emitted struct {
	tenant    string
	eventType string
	payload   map[string]any
}

type fakeEmitter struct {
	events  []emitted
	emitErr error
}

func (f *fakeEmitter) Append(_ context.Context, tenantID, eventType string, payload json.RawMessage, _ time.Time) (audit.Row, error) {
	if f.emitErr != nil {
		return audit.Row{}, f.emitErr
	}
	var p map[string]any
	_ = json.Unmarshal(payload, &p)
	f.events = append(f.events, emitted{tenant: tenantID, eventType: eventType, payload: p})
	return audit.Row{}, nil
}

type fakeSigner struct {
	sig     []byte
	keyID   string
	signErr error
	signed  [][]byte
}

func (f *fakeSigner) Sign(_ context.Context, message []byte) ([]byte, string, error) {
	if f.signErr != nil {
		return nil, "", f.signErr
	}
	f.signed = append(f.signed, append([]byte(nil), message...))
	return f.sig, f.keyID, nil
}

func deadRow(seq uint64, eventType, payload string) audit.Row {
	r := audit.Row{
		ID:        "00000000-0000-0000-0000-00000000000" + string(rune('0'+seq)),
		Seq:       seq,
		TenantID:  "acme",
		EventType: eventType,
		Payload:   json.RawMessage(payload),
		Timestamp: time.Date(2026, 6, 9, 10, 0, 0, 0, time.UTC),
		PrevHash:  audit.GenesisPrevHash,
	}
	r.Hash = audit.ComputeHash(r)
	return r
}

func newSvc(store Store, emit Emitter, signer Signer) *Service {
	return New(Config{
		Store:    store,
		Emit:     emit,
		Signer:   signer,
		Classify: func(audit.Row) string { return "class_mapping_missing" },
		Clock:    func() time.Time { return time.Date(2026, 6, 9, 10, 0, 0, 0, time.UTC) },
	})
}

// spec: §12.8 lines 810-829 — each dead-lettered row is scrubbed under a
// signed receipt and the paired redacted / downstream-notified events are
// emitted, all sharing one erasure_job_id.
func TestRedactForUser_emitsPairedEvents_spec_12_8(t *testing.T) {
	t.Parallel()
	store := &fakeStore{rows: []audit.Row{
		deadRow(1, "session.created", `{"user_id":"alice@acme.com"}`),
		deadRow(2, "tool.called", `{"actor":"alice@acme.com"}`),
	}}
	emit := &fakeEmitter{}
	signer := &fakeSigner{sig: []byte("sig"), keyID: "boot"}
	n, err := newSvc(store, emit, signer).RedactForUser(context.Background(), "acme", "alice@acme.com")
	if err != nil {
		t.Fatalf("RedactForUser: %v", err)
	}
	if n != 2 {
		t.Fatalf("redacted = %d, want 2", n)
	}
	if len(store.redacted) != 2 {
		t.Fatalf("receipts written = %d, want 2", len(store.redacted))
	}
	// Two events per row.
	if len(emit.events) != 4 {
		t.Fatalf("events emitted = %d, want 4", len(emit.events))
	}
	// All receipts and both downstream events share one erasure_job_id.
	jobID := store.redacted[0].ErasureJobID
	if jobID == "" {
		t.Fatal("receipt erasure_job_id is empty")
	}
	for _, rec := range store.redacted {
		if rec.ErasureJobID != jobID {
			t.Errorf("receipt erasure_job_id = %q, want shared %q", rec.ErasureJobID, jobID)
		}
		if len(rec.Signature) == 0 || rec.SignatureKMSKey != "boot" {
			t.Errorf("receipt missing signature/keyID: %+v", rec)
		}
		if rec.LegalBasis != "gdpr_art17" {
			t.Errorf("legal_basis = %q, want gdpr_art17", rec.LegalBasis)
		}
		if rec.OriginalHash == "" || rec.NewHash == "" || rec.OriginalHash == rec.NewHash {
			t.Errorf("receipt hashes invalid: orig=%q new=%q", rec.OriginalHash, rec.NewHash)
		}
	}
	var redactedEvents, notifyEvents int
	for _, e := range emit.events {
		switch e.eventType {
		case eventRedacted:
			redactedEvents++
			for _, k := range []string{"audit_event_id", "tenant_id", "original_event_type", "original_error_class", "redacted_at", "pre_redaction_prev_hash", "post_redaction_prev_hash", "redaction_receipt_id"} {
				if _, ok := e.payload[k]; !ok {
					t.Errorf("%s payload missing %q", eventRedacted, k)
				}
			}
		case eventDownstreamNotified:
			notifyEvents++
			if e.payload["downstream_action_required"] != true {
				t.Errorf("%s downstream_action_required = %v, want true", eventDownstreamNotified, e.payload["downstream_action_required"])
			}
			if e.payload["erasure_job_id"] != jobID {
				t.Errorf("%s erasure_job_id = %v, want %q", eventDownstreamNotified, e.payload["erasure_job_id"], jobID)
			}
			for _, k := range []string{"audit_event_id", "original_sequence_number", "original_hash", "legal_basis", "redaction_receipt_id"} {
				if _, ok := e.payload[k]; !ok {
					t.Errorf("%s payload missing %q", eventDownstreamNotified, k)
				}
			}
		default:
			t.Errorf("unexpected event type %q", e.eventType)
		}
	}
	if redactedEvents != 2 || notifyEvents != 2 {
		t.Errorf("event split: redacted=%d notify=%d, want 2/2", redactedEvents, notifyEvents)
	}
	// The redacted payload carries the marker and no PII.
	for seq, p := range store.payloads {
		if !audit.IsRedactedPayload(p) {
			t.Errorf("seq %d payload not marked redacted: %s", seq, p)
		}
	}
}

// spec: §12.8 line 753 — empty scope is rejected before any work.
func TestRedactForUser_emptyScope_spec_12_8_line753(t *testing.T) {
	t.Parallel()
	svc := newSvc(&fakeStore{}, &fakeEmitter{}, &fakeSigner{})
	if _, err := svc.RedactForUser(context.Background(), "", "alice@acme.com"); !errors.Is(err, auditstore.ErrEmptyScope) {
		t.Errorf("empty tenant err = %v, want ErrEmptyScope", err)
	}
	if _, err := svc.RedactForUser(context.Background(), "acme", ""); !errors.Is(err, auditstore.ErrEmptyScope) {
		t.Errorf("empty user err = %v, want ErrEmptyScope", err)
	}
}

// No dead-lettered rows: a clean no-op that emits nothing.
func TestRedactForUser_noRows(t *testing.T) {
	t.Parallel()
	emit := &fakeEmitter{}
	n, err := newSvc(&fakeStore{}, emit, &fakeSigner{}).RedactForUser(context.Background(), "acme", "alice@acme.com")
	if err != nil || n != 0 {
		t.Fatalf("RedactForUser = (%d, %v), want (0, nil)", n, err)
	}
	if len(emit.events) != 0 {
		t.Errorf("emitted %d events on empty input, want 0", len(emit.events))
	}
}

// A signer failure aborts before any row is scrubbed: no redaction, no
// events, so a row is never left without an in-system erasure record.
func TestRedactForUser_signerError_failsClosed(t *testing.T) {
	t.Parallel()
	store := &fakeStore{rows: []audit.Row{deadRow(1, "session.created", `{"user_id":"alice@acme.com"}`)}}
	emit := &fakeEmitter{}
	n, err := newSvc(store, emit, &fakeSigner{signErr: errors.New("kms down")}).RedactForUser(context.Background(), "acme", "alice@acme.com")
	if err == nil {
		t.Fatal("expected signer error, got nil")
	}
	if n != 0 || len(store.redacted) != 0 || len(emit.events) != 0 {
		t.Errorf("partial work on signer error: n=%d redacted=%d events=%d", n, len(store.redacted), len(emit.events))
	}
}

// A redaction store error aborts the job; the count reflects rows already
// committed before the failure.
func TestRedactForUser_redactError(t *testing.T) {
	t.Parallel()
	store := &fakeStore{
		rows:      []audit.Row{deadRow(1, "session.created", `{"user_id":"alice@acme.com"}`)},
		redactErr: errors.New("permission denied"),
	}
	n, err := newSvc(store, &fakeEmitter{}, &fakeSigner{sig: []byte("s"), keyID: "boot"}).RedactForUser(context.Background(), "acme", "alice@acme.com")
	if err == nil {
		t.Fatal("expected redact error, got nil")
	}
	if n != 0 {
		t.Errorf("redacted count = %d, want 0 (the only row failed)", n)
	}
}

// spec: §12.8 line 810 — the receipt is signed over a stable JCS tuple, so
// the signer is invoked once per row with deterministic bytes.
func TestRedactForUser_signsOncePerRow(t *testing.T) {
	t.Parallel()
	store := &fakeStore{rows: []audit.Row{
		deadRow(1, "session.created", `{"user_id":"alice@acme.com"}`),
		deadRow(2, "tool.called", `{"sub":"alice@acme.com"}`),
	}}
	signer := &fakeSigner{sig: []byte("s"), keyID: "boot"}
	if _, err := newSvc(store, &fakeEmitter{}, signer).RedactForUser(context.Background(), "acme", "alice@acme.com"); err != nil {
		t.Fatalf("RedactForUser: %v", err)
	}
	if len(signer.signed) != 2 {
		t.Fatalf("signer invoked %d times, want 2", len(signer.signed))
	}
	if string(signer.signed[0]) == string(signer.signed[1]) {
		t.Error("per-row receipt tuples must differ (distinct seq/receipt_id)")
	}
}
