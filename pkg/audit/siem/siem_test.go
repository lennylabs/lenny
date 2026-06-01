// SPDX-License-Identifier: MIT

package siem

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/audit"
	"github.com/lennylabs/lenny/pkg/audit/ocsf"
)

// fakeSink is an in-memory SIEM Sink. It fails the first failNext
// batches, then records every delivered batch.
type fakeSink struct {
	mu       sync.Mutex
	failNext int
	batches  [][]json.RawMessage
}

func (s *fakeSink) DeliverBatch(_ context.Context, recs []json.RawMessage) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failNext > 0 {
		s.failNext--
		return errors.New("siem unreachable")
	}
	batch := make([]json.RawMessage, len(recs))
	copy(batch, recs)
	s.batches = append(s.batches, batch)
	return nil
}

func (s *fakeSink) delivered() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, b := range s.batches {
		n += len(b)
	}
	return n
}

func sampleRecord() ocsf.Record {
	return ocsf.Record{
		ClassUID:    ocsf.ClassAuthentication,
		CategoryUID: 3,
		ActivityID:  ocsf.ActivityLogon,
		TypeUID:     ocsf.ClassAuthentication*100 + ocsf.ActivityLogon,
		Time:        1_700_000_000_000,
		SeverityID:  1,
		Metadata: ocsf.Metadata{
			UID: "id-1", Sequence: 1, TenantUID: "acme", Version: ocsf.Version,
			Product: ocsf.Product{Name: "Lenny", VendorName: "Lenny"},
		},
	}
}

// fakeTranslationStore is a minimal ocsf.TranslationStore for the
// translator → forwarder integration test. It mirrors the per-row state
// the auditstore drives in production.
type fakeTranslationStore struct {
	mu   sync.Mutex
	rows []ocsf.TranslatableRow
}

func (s *fakeTranslationStore) PendingTranslation(_ context.Context, limit int) ([]ocsf.TranslatableRow, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []ocsf.TranslatableRow
	for _, r := range s.rows {
		if r.State == audit.OCSFPending || r.State == audit.OCSFRetryPending {
			out = append(out, r)
			if len(out) >= limit {
				break
			}
		}
	}
	return out, nil
}

func (s *fakeTranslationStore) SetTranslationState(_ context.Context, tenantID string, seq uint64,
	state audit.OCSFTranslationState, retryCount int,
) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.rows {
		if s.rows[i].Input.TenantID == tenantID && s.rows[i].Input.Sequence == seq {
			s.rows[i].State = state
			s.rows[i].RetryCount = retryCount
			return nil
		}
	}
	return errors.New("fakeTranslationStore: row not found")
}

func (s *fakeTranslationStore) state(seq uint64) audit.OCSFTranslationState {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, r := range s.rows {
		if r.Input.Sequence == seq {
			return r.State
		}
	}
	return ""
}

// spec: §11.7 Wire Format — the gateway wires the OCSF translator's sink
// to the SIEM forwarder so a pending audit row flows
// translator → Forwarder → Sink and its ocsf_translation_state advances
// to succeeded. This is the exact seam cmd/lenny-gateway constructs.
// F-11.7.1 / F-11.7.11.
func TestTranslatorForwardsPendingRowToSIEM(t *testing.T) {
	store := &fakeTranslationStore{rows: []ocsf.TranslatableRow{{
		Input: ocsf.Input{
			ID: "id-1", Sequence: 1, TenantID: "acme",
			EventType: "session.created", Payload: json.RawMessage(`{}`),
		},
		Topic: "session_lifecycle",
		State: audit.OCSFPending,
	}}}
	sink := &fakeSink{}
	fwd := NewForwarder(sink, DefaultForwarderConfig(), NewCountingMetrics())
	tr := ocsf.NewTranslator(store, fwd, ocsf.DefaultTranslationConfig(), nil)

	res, err := tr.RunCycle(context.Background())
	if err != nil {
		t.Fatalf("RunCycle: %v", err)
	}
	if res.Translated != 1 {
		t.Fatalf("Translated = %d, want 1", res.Translated)
	}
	if sink.delivered() != 1 {
		t.Errorf("SIEM sink received %d records, want 1", sink.delivered())
	}
	if st := store.state(1); st != audit.OCSFSucceeded {
		t.Errorf("row state = %q, want succeeded", st)
	}
}

// spec: 11.7
// diagnosis: §11.7 says audit events are streamed to an external SIEM
// as OCSF records. The forwarder satisfies ocsf.Sink, so a translated
// record flows translator → Forwarder → Sink. A successful Deliver
// must reach the sink and count the delivered record.
func TestForwarderDeliversTranslatedRecord(t *testing.T) {
	sink := &fakeSink{}
	metrics := NewCountingMetrics()
	fwd := NewForwarder(sink, DefaultForwarderConfig(), metrics)

	if err := fwd.Deliver(context.Background(), "acme", "session_lifecycle", sampleRecord()); err != nil {
		t.Fatalf("Deliver: %v", err)
	}
	if sink.delivered() != 1 {
		t.Errorf("sink received %d records, want 1", sink.delivered())
	}
	if metrics.DeliveredCount() != 1 {
		t.Errorf("delivered metric = %d, want 1", metrics.DeliveredCount())
	}
	if !fwd.Healthy() {
		t.Error("forwarder must be healthy after a successful delivery")
	}
}

// spec: 11.7
// diagnosis: §11.7 SIEM forwarder retry/state handling — a transient
// delivery failure is retried with backoff and the batch eventually
// lands. The forwarder must not lose the record.
func TestForwarderRetriesTransientFailure(t *testing.T) {
	sink := &fakeSink{failNext: 2} // fails twice, then succeeds
	metrics := NewCountingMetrics()
	cfg := DefaultForwarderConfig()
	cfg.MaxRetries = 3
	fwd := NewForwarder(sink, cfg, metrics)
	// Replace the backoff sleep with a no-op so the test does not wait.
	fwd.sleep = func(time.Duration) {}

	if err := fwd.Deliver(context.Background(), "acme", "session_lifecycle", sampleRecord()); err != nil {
		t.Fatalf("Deliver: %v", err)
	}
	if sink.delivered() != 1 {
		t.Errorf("sink received %d records, want 1 after retries", sink.delivered())
	}
	if metrics.RetriedCount() != 2 {
		t.Errorf("retried metric = %d, want 2", metrics.RetriedCount())
	}
	if !fwd.Healthy() {
		t.Error("forwarder recovered — it must report healthy")
	}
}

// spec: 11.7
// diagnosis: §11.7 says the gateway monitors SIEM delivery success and
// reports degraded /healthz when delivery fails. When every retry is
// exhausted the forwarder must mark itself unhealthy, increment the
// failure metric, and surface the failure to the caller.
func TestForwarderMarksUnhealthyOnExhaustedRetries(t *testing.T) {
	sink := &fakeSink{failNext: 100} // always fails
	metrics := NewCountingMetrics()
	cfg := DefaultForwarderConfig()
	cfg.MaxRetries = 2
	fwd := NewForwarder(sink, cfg, metrics)
	fwd.sleep = func(time.Duration) {}

	err := fwd.Deliver(context.Background(), "acme", "session_lifecycle", sampleRecord())
	if err == nil {
		t.Fatal("Deliver must return an error when every retry fails")
	}
	if fwd.Healthy() {
		t.Error("forwarder must report unhealthy after exhausting retries")
	}
	if metrics.FailedCount() != 1 {
		t.Errorf("failed metric = %d, want 1", metrics.FailedCount())
	}
	if metrics.FailureRate() == 0 {
		t.Error("failure rate must be non-zero after a failed delivery")
	}
}

// spec: 11.7
// diagnosis: §11.7 startup SIEM connectivity validation — a test event
// is sent and the gateway refuses to start until acknowledgement is
// received. ValidateConnectivity must succeed when the sink accepts
// the probe and fail when it does not.
func TestValidateConnectivity(t *testing.T) {
	t.Run("acknowledged probe", func(t *testing.T) {
		fwd := NewForwarder(&fakeSink{}, DefaultForwarderConfig(), nil)
		if err := fwd.ValidateConnectivity(context.Background()); err != nil {
			t.Errorf("ValidateConnectivity must succeed when the sink accepts the probe: %v", err)
		}
	})
	t.Run("unreachable SIEM", func(t *testing.T) {
		fwd := NewForwarder(&fakeSink{failNext: 100}, DefaultForwarderConfig(), nil)
		if err := fwd.ValidateConnectivity(context.Background()); err == nil {
			t.Error("ValidateConnectivity must fail when the SIEM is unreachable (gateway refuses to start)")
		}
	})
}
