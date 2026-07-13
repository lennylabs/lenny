// SPDX-License-Identifier: MIT

package escalation_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/ops/escalation"
)

// fakeStore is an in-test escalation.Store that can be toggled
// unreachable to exercise the §25.4 tier-fallback contract.
type fakeStore struct {
	mu   sync.Mutex
	tier string
	down bool
	recs map[string]escalation.Escalation
}

func newFakeStore(tier string) *fakeStore {
	return &fakeStore{tier: tier, recs: map[string]escalation.Escalation{}}
}

func (f *fakeStore) setDown(down bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.down = down
}

func (f *fakeStore) Tier() string { return f.tier }

func (f *fakeStore) Put(_ context.Context, esc escalation.Escalation) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.down {
		return escalation.ErrStoreUnavailable
	}
	f.recs[esc.ID] = esc
	return nil
}

func (f *fakeStore) Get(_ context.Context, id string) (*escalation.Escalation, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.down {
		return nil, escalation.ErrStoreUnavailable
	}
	esc, ok := f.recs[id]
	if !ok {
		return nil, nil
	}
	cp := esc
	return &cp, nil
}

func (f *fakeStore) List(_ context.Context, _ escalation.Filter, _ string, _ int) (escalation.ListPage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.down {
		return escalation.ListPage{}, escalation.ErrStoreUnavailable
	}
	out := make([]escalation.Escalation, 0, len(f.recs))
	for _, e := range f.recs {
		out = append(out, e)
	}
	return escalation.ListPage{Items: out, CursorKind: escalation.CursorKindNone}, nil
}

func (f *fakeStore) SetStatus(_ context.Context, id, status string, now time.Time) (*escalation.Escalation, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.down {
		return nil, escalation.ErrStoreUnavailable
	}
	esc, ok := f.recs[id]
	if !ok {
		return nil, nil
	}
	escalation.ApplyStatus(&esc, status, now)
	f.recs[id] = esc
	cp := esc
	return &cp, nil
}

func (f *fakeStore) SetEmitted(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.down {
		return escalation.ErrStoreUnavailable
	}
	if esc, ok := f.recs[id]; ok {
		esc.Emitted = true
		f.recs[id] = esc
	}
	return nil
}

func (f *fakeStore) PendingEmission(_ context.Context) ([]escalation.Escalation, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.down {
		return nil, escalation.ErrStoreUnavailable
	}
	var out []escalation.Escalation
	for _, e := range f.recs {
		if !e.Emitted {
			out = append(out, e)
		}
	}
	return out, nil
}

func (f *fakeStore) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.recs)
}

// recordingAudit captures remediation.escalation_persisted events.
type recordingAudit struct {
	mu     sync.Mutex
	events []string // "source->dest"
}

func (a *recordingAudit) EscalationPersisted(_ context.Context, _, src, dst string, _ int64) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.events = append(a.events, src+"->"+dst)
}

func (a *recordingAudit) len() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.events)
}

func mkCreate(t *testing.T, s *escalation.Service) *escalation.Escalation {
	t.Helper()
	esc, err := s.Create(context.Background(), escalation.CreateRequest{
		Severity: escalation.SeverityCritical, Summary: "x", Source: "watchdog",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	return esc
}

// TestTieredCreatePrefersPostgres asserts a create with both durable
// tiers reachable lands at Tier 1 (durable-postgres).
// spec: §25.4 lines 2380-2384.
func TestTieredCreatePrefersPostgres_spec_25_4(t *testing.T) {
	pg := newFakeStore(escalation.PersistenceDurablePostgres)
	rd := newFakeStore(escalation.PersistenceDurableRedis)
	s := escalation.NewWithStores(escalation.Options{Durable: []escalation.Store{pg, rd}})
	esc := mkCreate(t, s)
	if esc.Persistence != escalation.PersistenceDurablePostgres {
		t.Fatalf("persistence = %q, want durable-postgres", esc.Persistence)
	}
	if pg.count() != 1 || rd.count() != 0 {
		t.Errorf("pg=%d redis=%d, want the record only in Postgres", pg.count(), rd.count())
	}
}

// TestTieredCreateFallsBackToRedisThenMemory walks the §25.4 fallback
// ladder as each higher tier goes unreachable.
// spec: §25.4 lines 2376-2384.
func TestTieredCreateFallsBackToRedisThenMemory_spec_25_4(t *testing.T) {
	pg := newFakeStore(escalation.PersistenceDurablePostgres)
	rd := newFakeStore(escalation.PersistenceDurableRedis)
	s := escalation.NewWithStores(escalation.Options{Durable: []escalation.Store{pg, rd}})

	pg.setDown(true)
	esc := mkCreate(t, s)
	if esc.Persistence != escalation.PersistenceDurableRedis {
		t.Errorf("persistence = %q, want durable-redis when Postgres is down", esc.Persistence)
	}

	rd.setDown(true)
	esc = mkCreate(t, s)
	if esc.Persistence != escalation.PersistenceBufferedMemory {
		t.Errorf("persistence = %q, want buffered-memory when both durable tiers are down", esc.Persistence)
	}
}

// TestRequireDurableRejectsWhenNoDurableStore asserts the §25.4 line 2396
// conservative posture: with requireDurable set and both durable tiers
// down, a create fails with ESCALATION_NO_DURABLE_STORE rather than
// buffering in memory.
func TestRequireDurableRejectsWhenNoDurableStore_spec_25_4(t *testing.T) {
	pg := newFakeStore(escalation.PersistenceDurablePostgres)
	pg.setDown(true)
	s := escalation.NewWithStores(escalation.Options{
		Durable:        []escalation.Store{pg},
		RequireDurable: true,
	})
	_, err := s.Create(context.Background(), escalation.CreateRequest{
		Severity: escalation.SeverityWarning, Summary: "x", Source: "watchdog",
	})
	if escalation.CodeOf(err) != escalation.ErrCodeNoDurableStore {
		t.Fatalf("err code = %q, want ESCALATION_NO_DURABLE_STORE", escalation.CodeOf(err))
	}
	// Without requireDurable the same outage buffers in memory.
	s2 := escalation.NewWithStores(escalation.Options{Durable: []escalation.Store{pg}})
	esc := mkCreate(t, s2)
	if esc.Persistence != escalation.PersistenceBufferedMemory {
		t.Errorf("persistence = %q, want buffered-memory without requireDurable", esc.Persistence)
	}
}

// TestFlushPromotesBufferedToPostgres asserts the §25.4 reconciliation
// flush: a record buffered during an outage is promoted to Postgres once
// it recovers, preserving the authoring timestamp and the emitted flag,
// and emitting one remediation.escalation_persisted audit event.
// spec: §25.4 lines 2407-2415.
func TestFlushPromotesBufferedToPostgres_spec_25_4(t *testing.T) {
	pg := newFakeStore(escalation.PersistenceDurablePostgres)
	em := &recordingEmitter{}
	audit := &recordingAudit{}
	s := escalation.NewWithStores(escalation.Options{
		Durable:                       []escalation.Store{pg},
		Emitter:                       em,
		Audit:                         audit,
		ReconciliationWritesPerSecond: -1, // no pacing in test
	})
	created := time.Date(2026, 5, 18, 9, 0, 0, 0, time.UTC)
	s.SetClock(func() time.Time { return created })

	pg.setDown(true)
	esc := mkCreate(t, s) // buffers in memory, emitted=true
	if esc.Persistence != escalation.PersistenceBufferedMemory || !esc.Emitted {
		t.Fatalf("setup: persistence=%q emitted=%v, want buffered-memory + emitted", esc.Persistence, esc.Emitted)
	}

	pg.setDown(false)
	n, err := s.Flush(context.Background())
	if err != nil {
		t.Fatalf("flush: %v", err)
	}
	if n != 1 {
		t.Fatalf("flushed %d, want 1", n)
	}
	if pg.count() != 1 {
		t.Fatalf("postgres holds %d, want 1 after flush", pg.count())
	}
	got, _ := pg.Get(context.Background(), esc.ID)
	if got == nil {
		t.Fatal("flushed escalation missing from Postgres")
	}
	if !got.CreatedAt.Equal(created) {
		t.Errorf("createdAt = %v, want the preserved authoring time %v", got.CreatedAt, created)
	}
	if !got.Emitted {
		t.Error("emitted flag was reset during flush")
	}
	if got.Persistence != escalation.PersistenceDurablePostgres {
		t.Errorf("persistence = %q, want durable-postgres after flush", got.Persistence)
	}
	if audit.len() != 1 {
		t.Errorf("audit events = %d, want 1 escalation_persisted", audit.len())
	}
	// The buffer is now drained, so a second flush is a no-op.
	if n2, _ := s.Flush(context.Background()); n2 != 0 {
		t.Errorf("second flush promoted %d, want 0 (buffer drained)", n2)
	}
	// The escalation is still listable (now from Postgres).
	if list, _ := s.List(context.Background(), escalation.Filter{}, "", 0); len(list.Items) != 1 {
		t.Errorf("list returned %d after flush, want 1", len(list.Items))
	}
}

// TestFlushStopsWhenNoDurableStoreReachable asserts the flush leaves the
// buffer intact when no durable tier is reachable, so the next pass can
// retry.
// spec: §25.4 lines 2407-2409.
func TestFlushStopsWhenNoDurableStoreReachable_spec_25_4(t *testing.T) {
	pg := newFakeStore(escalation.PersistenceDurablePostgres)
	s := escalation.NewWithStores(escalation.Options{Durable: []escalation.Store{pg}})
	pg.setDown(true)
	mkCreate(t, s) // buffers
	n, err := s.Flush(context.Background())
	if err != nil {
		t.Fatalf("flush: %v", err)
	}
	if n != 0 {
		t.Errorf("flushed %d while Postgres down, want 0", n)
	}
	// Record is still readable from the in-memory tier.
	if list, _ := s.List(context.Background(), escalation.Filter{}, "", 0); len(list.Items) != 1 {
		t.Errorf("buffered record lost: list returned %d, want 1", len(list.Items))
	}
}

// TestUpdateFindsRecordInLowerTier asserts a status update reaches a
// record buffered in memory even when a higher durable tier is reachable
// but does not hold it (§25.4 query-path "status updates work").
func TestUpdateFindsRecordInLowerTier_spec_25_4(t *testing.T) {
	pg := newFakeStore(escalation.PersistenceDurablePostgres)
	s := escalation.NewWithStores(escalation.Options{Durable: []escalation.Store{pg}})
	pg.setDown(true)
	esc := mkCreate(t, s) // buffered in memory
	pg.setDown(false)     // Postgres up, but the record is still buffered
	got, err := s.Update(context.Background(), esc.ID, escalation.UpdateRequest{Status: escalation.StatusResolved})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if got.Status != escalation.StatusResolved || got.ResolvedAt == nil {
		t.Errorf("status=%q resolvedAt=%v, want resolved with a timestamp", got.Status, got.ResolvedAt)
	}
}
