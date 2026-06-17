// SPDX-License-Identifier: MIT

package siem

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/audit/ocsf"
)

// fakeDeliveryStore is an in-memory siem.DeliveryStore. PendingForward
// returns rows whose sequence is past the per-tenant acked high-water
// mark, so a cycle that does not checkpoint re-returns the same rows.
type fakeDeliveryStore struct {
	mu        sync.Mutex
	rows      []ForwardRow
	acked     map[string]uint64
	lag       float64
	lagErr    error
	pendErr   error
	ckptErr   error
	ckptCalls []ckpt
}

type ckpt struct {
	tenant string
	seq    uint64
	at     time.Time
}

func (s *fakeDeliveryStore) PendingForward(_ context.Context, limit int) ([]ForwardRow, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pendErr != nil {
		return nil, s.pendErr
	}
	var out []ForwardRow
	for _, r := range s.rows {
		if r.Sequence > s.acked[r.TenantID] {
			out = append(out, r)
			if len(out) >= limit {
				break
			}
		}
	}
	return out, nil
}

func (s *fakeDeliveryStore) Checkpoint(_ context.Context, tenant string, seq uint64, at time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ckptErr != nil {
		return s.ckptErr
	}
	if s.acked == nil {
		s.acked = map[string]uint64{}
	}
	if seq > s.acked[tenant] {
		s.acked[tenant] = seq
	}
	s.ckptCalls = append(s.ckptCalls, ckpt{tenant, seq, at})
	return nil
}

func (s *fakeDeliveryStore) DeliveryLag(context.Context) (float64, error) { return s.lag, s.lagErr }

func (s *fakeDeliveryStore) checkpoints() []ckpt {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]ckpt(nil), s.ckptCalls...)
}

type fakeLagGauge struct {
	mu    sync.Mutex
	last  float64
	calls int
}

func (g *fakeLagGauge) SetSIEMDeliveryLagSeconds(s float64) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.last = s
	g.calls++
}

func (g *fakeLagGauge) read() (float64, int) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.last, g.calls
}

func translatableRow(tenant string, seq uint64, eventType string, at time.Time) ForwardRow {
	return ForwardRow{
		TenantID: tenant,
		Sequence: seq,
		Input: ocsf.Input{
			ID: "id", Sequence: seq, TenantID: tenant,
			EventType: eventType, Payload: []byte(`{}`),
			CreatedAtUnixMs: at.UnixMilli(),
		},
		Topic:     "session_lifecycle",
		CreatedAt: at,
	}
}

// spec: §12.3 line 97 — the outbox forwarder tails committed audit rows,
// delivers each to the SIEM, and advances the durable per-tenant
// high-water mark only after delivery. A clean cycle delivers every
// pending row, checkpoints each one, and refreshes the lag gauge.
func TestOutbox_DeliversAndCheckpoints_spec_12_3(t *testing.T) {
	at := time.Unix(1_700_000_000, 0).UTC()
	store := &fakeDeliveryStore{
		lag: 4,
		rows: []ForwardRow{
			translatableRow("acme", 1, "session.created", at),
			translatableRow("acme", 2, "session.created", at.Add(time.Second)),
			translatableRow("globex", 1, "session.created", at.Add(2*time.Second)),
		},
	}
	sink := &fakeSink{}
	fwd := NewForwarder(sink, DefaultForwarderConfig(), NewCountingMetrics())
	gauge := &fakeLagGauge{}
	ob := NewOutbox(store, fwd, OutboxConfig{}, gauge)

	res, err := ob.RunCycle(context.Background())
	if err != nil {
		t.Fatalf("RunCycle: %v", err)
	}
	if res.Delivered != 3 {
		t.Fatalf("Delivered = %d, want 3", res.Delivered)
	}
	if sink.delivered() != 3 {
		t.Errorf("sink received %d records, want 3", sink.delivered())
	}
	cps := store.checkpoints()
	if len(cps) != 3 {
		t.Fatalf("checkpoint calls = %d, want 3", len(cps))
	}
	if cps[2].tenant != "globex" || cps[2].seq != 1 {
		t.Errorf("last checkpoint = %+v, want globex/1", cps[2])
	}
	if lag, calls := gauge.read(); lag != 4 || calls != 1 {
		t.Errorf("lag gauge = (%v, %d calls), want (4, 1)", lag, calls)
	}
}

// spec: §12.3 line 97 — a SIEM delivery failure must NOT advance the
// high-water mark: the row stays pending so the next cycle re-delivers
// it. The forwarder stops at the failing row (head-of-line), and the lag
// gauge is still refreshed so AuditSIEMDeliveryLag can fire.
func TestOutbox_DoesNotCheckpointOnDeliveryFailure_spec_12_3(t *testing.T) {
	at := time.Unix(1_700_000_000, 0).UTC()
	store := &fakeDeliveryStore{
		lag: 90,
		rows: []ForwardRow{
			translatableRow("acme", 1, "session.created", at),
			translatableRow("acme", 2, "session.created", at.Add(time.Second)),
		},
	}
	sink := &fakeSink{failNext: 100} // SIEM always unreachable
	fwd := NewForwarder(sink, ForwarderConfig{MaxRetries: 1, RetryBackoff: time.Millisecond}, NewCountingMetrics())
	gauge := &fakeLagGauge{}
	ob := NewOutbox(store, fwd, OutboxConfig{}, gauge)

	res, err := ob.RunCycle(context.Background())
	if err == nil {
		t.Fatal("RunCycle: expected delivery error, got nil")
	}
	if res.Delivered != 0 {
		t.Errorf("Delivered = %d, want 0", res.Delivered)
	}
	if len(store.checkpoints()) != 0 {
		t.Errorf("checkpoint advanced despite delivery failure: %+v", store.checkpoints())
	}
	if lag, calls := gauge.read(); lag != 90 || calls != 1 {
		t.Errorf("lag gauge = (%v, %d calls), want (90, 1)", lag, calls)
	}

	// Next cycle (SIEM recovered) re-delivers both rows from the same
	// position — no gap, no loss.
	sink.mu.Lock()
	sink.failNext = 0
	sink.mu.Unlock()
	if _, err := ob.RunCycle(context.Background()); err != nil {
		t.Fatalf("recovery RunCycle: %v", err)
	}
	if got := len(store.checkpoints()); got != 2 {
		t.Errorf("after recovery checkpoints = %d, want 2", got)
	}
}

// spec: §12.3 line 95 / §11.7 — a row whose OCSF translation fails is
// delivered as a translation-failure receipt and the pointer advances
// past it, so a persistently untranslatable event cannot head-of-line
// block the SIEM stream.
func TestOutbox_DeadLettersUntranslatableRow_spec_12_3(t *testing.T) {
	at := time.Unix(1_700_000_000, 0).UTC()
	store := &fakeDeliveryStore{
		rows: []ForwardRow{
			translatableRow("acme", 1, "this.event.type.has.no.ocsf.mapping", at),
			translatableRow("acme", 2, "session.created", at.Add(time.Second)),
		},
	}
	sink := &fakeSink{}
	fwd := NewForwarder(sink, DefaultForwarderConfig(), NewCountingMetrics())
	ob := NewOutbox(store, fwd, OutboxConfig{}, &fakeLagGauge{})

	res, err := ob.RunCycle(context.Background())
	if err != nil {
		t.Fatalf("RunCycle: %v", err)
	}
	if res.DeadLettered != 1 {
		t.Errorf("DeadLettered = %d, want 1", res.DeadLettered)
	}
	if res.Delivered != 2 {
		t.Errorf("Delivered = %d, want 2 (receipt + good row)", res.Delivered)
	}
	if sink.delivered() != 2 {
		t.Errorf("sink received %d records, want 2", sink.delivered())
	}
	if got := len(store.checkpoints()); got != 2 {
		t.Errorf("checkpoints = %d, want 2 (both rows advanced)", got)
	}
}

// spec: §12.3 line 97 — an empty cycle (forwarder caught up) delivers
// nothing but still refreshes the lag gauge so a stalled SIEM is visible
// even when no new rows committed.
func TestOutbox_EmptyCycleEmitsLag_spec_12_3(t *testing.T) {
	store := &fakeDeliveryStore{lag: 0}
	sink := &fakeSink{}
	fwd := NewForwarder(sink, DefaultForwarderConfig(), nil)
	gauge := &fakeLagGauge{}
	ob := NewOutbox(store, fwd, OutboxConfig{}, gauge)

	res, err := ob.RunCycle(context.Background())
	if err != nil {
		t.Fatalf("RunCycle: %v", err)
	}
	if res.Delivered != 0 || sink.delivered() != 0 {
		t.Errorf("empty cycle delivered rows: res=%+v sink=%d", res, sink.delivered())
	}
	if _, calls := gauge.read(); calls != 1 {
		t.Errorf("lag gauge calls = %d, want 1", calls)
	}
}

// spec: §12.3 line 97 — a PendingForward read error aborts the cycle
// before any delivery; no checkpoint is advanced.
func TestOutbox_PendingForwardError_spec_12_3(t *testing.T) {
	store := &fakeDeliveryStore{pendErr: errors.New("postgres down")}
	ob := NewOutbox(store, NewForwarder(&fakeSink{}, DefaultForwarderConfig(), nil), OutboxConfig{}, nil)
	if _, err := ob.RunCycle(context.Background()); err == nil {
		t.Fatal("expected error from PendingForward, got nil")
	}
	if len(store.checkpoints()) != 0 {
		t.Errorf("checkpoint advanced despite read error")
	}
}

// spec: §12.3 line 97 — NewOutbox fills zero config fields from the
// defaults so a caller can pass OutboxConfig{}.
func TestOutbox_DefaultConfig_spec_12_3(t *testing.T) {
	ob := NewOutbox(&fakeDeliveryStore{}, NewForwarder(&fakeSink{}, DefaultForwarderConfig(), nil), OutboxConfig{}, nil)
	if ob.cfg.PollInterval != DefaultOutboxConfig().PollInterval {
		t.Errorf("PollInterval = %v, want default %v", ob.cfg.PollInterval, DefaultOutboxConfig().PollInterval)
	}
	if ob.cfg.BatchSize != DefaultOutboxConfig().BatchSize {
		t.Errorf("BatchSize = %d, want default %d", ob.cfg.BatchSize, DefaultOutboxConfig().BatchSize)
	}
}
