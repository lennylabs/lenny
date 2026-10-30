// SPDX-License-Identifier: MIT

package ocsf

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"

	"github.com/lennylabs/lenny/pkg/audit"
)

// fakeStore is an in-memory ocsf.TranslationStore for the state-machine
// tests. It tracks each row's state and retry count.
type fakeStore struct {
	mu   sync.Mutex
	rows []TranslatableRow
}

func (f *fakeStore) PendingTranslation(_ context.Context, limit int) ([]TranslatableRow, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []TranslatableRow
	for _, r := range f.rows {
		if r.State == audit.OCSFPending || r.State == audit.OCSFRetryPending {
			out = append(out, r)
			if len(out) >= limit {
				break
			}
		}
	}
	return out, nil
}

func (f *fakeStore) SetTranslationState(_ context.Context, tenantID string, seq uint64,
	state audit.OCSFTranslationState, retryCount int,
) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := range f.rows {
		if f.rows[i].Input.TenantID == tenantID && f.rows[i].Input.Sequence == seq {
			f.rows[i].State = state
			f.rows[i].RetryCount = retryCount
			return nil
		}
	}
	return errors.New("fakeStore: row not found")
}

func (f *fakeStore) state(seq uint64) (audit.OCSFTranslationState, int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, r := range f.rows {
		if r.Input.Sequence == seq {
			return r.State, r.RetryCount
		}
	}
	return "", 0
}

// recordingSink captures every delivered OCSF record.
type recordingSink struct {
	mu      sync.Mutex
	records []Record
}

func (s *recordingSink) Deliver(_ context.Context, _, _ string, rec Record) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.records = append(s.records, rec)
	return nil
}

func (s *recordingSink) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.records)
}

// spec: 11.7
// diagnosis: §11.7 says ocsf_translation_state runs pending → succeeded
// on a clean translation, and the translated record is multicast to
// the downstream sink. RunCycle must drive that transition.
func TestRunCycleTranslatesPendingRowToSucceeded(t *testing.T) {
	store := &fakeStore{rows: []TranslatableRow{{
		Input: Input{
			ID: "id-1", Sequence: 1, TenantID: "acme",
			EventType: "session.created", Payload: json.RawMessage(`{}`),
		},
		Topic: "session_lifecycle",
		State: audit.OCSFPending,
	}}}
	sink := &recordingSink{}
	tr := NewTranslator(store, sink, DefaultTranslationConfig(), nil)

	res, err := tr.RunCycle(context.Background())
	if err != nil {
		t.Fatalf("RunCycle: %v", err)
	}
	if res.Translated != 1 {
		t.Errorf("Translated = %d, want 1", res.Translated)
	}
	if st, _ := store.state(1); st != audit.OCSFSucceeded {
		t.Errorf("row state = %q, want succeeded", st)
	}
	if sink.count() != 1 {
		t.Errorf("sink received %d records, want 1 (multicast delivery)", sink.count())
	}
}

// spec: 11.7
// diagnosis: §11.7 says on first-attempt failure the translator writes
// retry_pending; the background retry loop re-attempts up to
// maxAttempts; on the final attempt's failure the row transitions to
// dead_lettered. RunCycle driven maxAttempts times must walk that path.
func TestRunCycleDeadLettersAfterMaxAttempts(t *testing.T) {
	store := &fakeStore{rows: []TranslatableRow{{
		Input: Input{
			ID: "id-bad", Sequence: 1, TenantID: "acme",
			// An unmapped event type fails every translation attempt.
			EventType: "permanently.unmapped", Payload: json.RawMessage(`{}`),
		},
		Topic: "session_lifecycle",
		State: audit.OCSFPending,
	}}}
	sink := &recordingSink{}
	metrics := NewCountingMetrics()
	cfg := DefaultTranslationConfig()
	cfg.MaxAttempts = 3
	tr := NewTranslator(store, sink, cfg, metrics)
	ctx := context.Background()

	// Attempts 1 and 2 fail → retry_pending with the retry count rising.
	for attempt := 1; attempt < cfg.MaxAttempts; attempt++ {
		if _, err := tr.RunCycle(ctx); err != nil {
			t.Fatalf("RunCycle attempt %d: %v", attempt, err)
		}
		st, rc := store.state(1)
		if st != audit.OCSFRetryPending {
			t.Errorf("after attempt %d state = %q, want retry_pending", attempt, st)
		}
		if rc != attempt {
			t.Errorf("after attempt %d retry_count = %d, want %d", attempt, rc, attempt)
		}
	}
	// The final attempt dead-letters the row.
	res, err := tr.RunCycle(ctx)
	if err != nil {
		t.Fatalf("RunCycle final: %v", err)
	}
	if res.DeadLettered != 1 {
		t.Errorf("DeadLettered = %d, want 1", res.DeadLettered)
	}
	if st, _ := store.state(1); st != audit.OCSFDeadLettered {
		t.Errorf("row state = %q, want dead_lettered", st)
	}
	// The §11.7 translation-failure receipt was emitted in place of the
	// untranslatable event so the SIEM pointer advances past it.
	if sink.count() != 1 {
		t.Errorf("sink received %d records, want 1 (the dead-letter receipt)", sink.count())
	}
	if sink.records[0].ClassUID != ClassAppSecurityFinding {
		t.Errorf("dead-letter sink record class = %d, want 2004", sink.records[0].ClassUID)
	}
	if metrics.DeadLetters() != 1 {
		t.Errorf("DeadLettered metric = %d, want 1", metrics.DeadLetters())
	}
	if metrics.Failed("permanently.unmapped", ErrClassMappingMissing) != cfg.MaxAttempts {
		t.Errorf("translation-failed metric = %d, want %d",
			metrics.Failed("permanently.unmapped", ErrClassMappingMissing), cfg.MaxAttempts)
	}
}

// spec: 11.7
// diagnosis: §11.7 says a dead_lettered row is terminal — the
// translator must not advance it further. Once a row reaches
// dead_lettered or succeeded, a subsequent RunCycle must leave it
// untouched (it is not in the pending set).
func TestRunCycleSkipsTerminalRows(t *testing.T) {
	store := &fakeStore{rows: []TranslatableRow{
		{Input: Input{ID: "ok", Sequence: 1, TenantID: "acme", EventType: "session.created", Payload: json.RawMessage(`{}`)}, State: audit.OCSFSucceeded},
		{Input: Input{ID: "dl", Sequence: 2, TenantID: "acme", EventType: "session.created", Payload: json.RawMessage(`{}`)}, State: audit.OCSFDeadLettered},
	}}
	tr := NewTranslator(store, &recordingSink{}, DefaultTranslationConfig(), nil)
	res, err := tr.RunCycle(context.Background())
	if err != nil {
		t.Fatalf("RunCycle: %v", err)
	}
	if res.Translated != 0 || res.RetryScheduled != 0 || res.DeadLettered != 0 {
		t.Errorf("a cycle with only terminal rows did work: %+v", res)
	}
}
