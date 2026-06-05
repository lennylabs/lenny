// SPDX-License-Identifier: MIT

package auditretention

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/auditstore"
)

// fakeStore records the PruneRetention/RetentionWindowStats calls it
// receives so a test can assert the §16.4 windows and force flag.
type fakeStore struct {
	calls     []auditstore.PruneOptions
	callTenan []string
	perTenant map[string]int
	pruneErr  error
	failAfter int // when >0, the Nth (1-based) PruneRetention call returns pruneErr
	window    auditstore.RetentionWindow
	windowErr error
	// held maps tenant -> SIEM-held row count returned by SIEMHeldCount.
	held    map[string]int
	heldErr error
	// heldCalls records the tenants SIEMHeldCount was queried for.
	heldCalls []string
}

func (f *fakeStore) PruneRetention(_ context.Context, tenantID string, opts auditstore.PruneOptions) (int, error) {
	f.calls = append(f.calls, opts)
	f.callTenan = append(f.callTenan, tenantID)
	if f.failAfter > 0 && len(f.calls) == f.failAfter {
		return 0, f.pruneErr
	}
	if f.pruneErr != nil && f.failAfter == 0 {
		return 0, f.pruneErr
	}
	return f.perTenant[tenantID], nil
}

func (f *fakeStore) RetentionWindowStats(_ context.Context, _ string, _ time.Time) (auditstore.RetentionWindow, error) {
	return f.window, f.windowErr
}

func (f *fakeStore) SIEMHeldCount(_ context.Context, tenantID string, _ time.Time) (int, error) {
	f.heldCalls = append(f.heldCalls, tenantID)
	if f.heldErr != nil {
		return 0, f.heldErr
	}
	return f.held[tenantID], nil
}

type staticTenants []string

func (s staticTenants) ListTenants(context.Context) ([]string, error) { return []string(s), nil }

type errTenants struct{ err error }

func (e errTenants) ListTenants(context.Context) ([]string, error) { return nil, e.err }

type recordingMetrics struct {
	pruned int
	runs   map[string]int
	// blocked records the latest gauge value set per partition and the
	// ordered sequence of (partition, blocked) calls so a test can assert
	// the set-once / clear-once discipline.
	blocked     map[string]bool
	blockedSets []blockSet
}

type blockSet struct {
	partition string
	blocked   bool
}

func newMetrics() *recordingMetrics {
	return &recordingMetrics{runs: map[string]int{}, blocked: map[string]bool{}}
}

func (m *recordingMetrics) AddAuditRowsPruned(n int)        { m.pruned += n }
func (m *recordingMetrics) IncAuditRetentionRun(out string) { m.runs[out]++ }
func (m *recordingMetrics) SetAuditPartitionDropBlocked(partition string, blocked bool) {
	m.blocked[partition] = blocked
	m.blockedSets = append(m.blockedSets, blockSet{partition, blocked})
}

var now = time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)

// spec: §16.4 lines 378-382 — Tick passes the general window
// (retentionDays) and the separate gdpr.* window to every tenant.
func TestTickComputesWindowsAndSums(t *testing.T) {
	store := &fakeStore{perTenant: map[string]int{"platform": 3, "acme": 5}}
	m := newMetrics()
	p := New(store, staticTenants{"platform", "acme"}, nil, Options{
		RetentionDays:     365,
		GDPRRetentionDays: 2555,
		SIEMConfigured:    true,
		Metrics:           m,
	})
	got, err := p.Tick(context.Background(), now)
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if got != 8 {
		t.Fatalf("pruned = %d, want 8", got)
	}
	if len(store.calls) != 2 {
		t.Fatalf("PruneRetention calls = %d, want 2", len(store.calls))
	}
	wantGeneral := now.Add(-365 * 24 * time.Hour)
	wantGDPR := now.Add(-2555 * 24 * time.Hour)
	for i, c := range store.calls {
		if !c.GeneralCutoff.Equal(wantGeneral) {
			t.Errorf("call %d GeneralCutoff = %v, want %v", i, c.GeneralCutoff, wantGeneral)
		}
		if !c.GDPRCutoff.Equal(wantGDPR) {
			t.Errorf("call %d GDPRCutoff = %v, want %v", i, c.GDPRCutoff, wantGDPR)
		}
		if !c.SIEMConfigured {
			t.Errorf("call %d SIEMConfigured = false, want true", i)
		}
		if c.Force {
			t.Errorf("call %d Force = true, want false (Tick never forces)", i)
		}
	}
	if m.pruned != 8 {
		t.Errorf("metrics pruned = %d, want 8", m.pruned)
	}
	if m.runs["success"] != 1 {
		t.Errorf("success runs = %d, want 1", m.runs["success"])
	}
}

// spec: §16.4 line 380 — a zero GDPRRetentionDays holds every gdpr.*
// row indefinitely (the gdpr cutoff is the zero time, which
// PruneRetention treats as match-nothing).
func TestTickZeroGDPRWindowHoldsReceipts(t *testing.T) {
	store := &fakeStore{perTenant: map[string]int{"acme": 1}}
	p := New(store, staticTenants{"acme"}, nil, Options{RetentionDays: 365})
	if _, err := p.Tick(context.Background(), now); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if !store.calls[0].GDPRCutoff.IsZero() {
		t.Errorf("GDPRCutoff = %v, want zero", store.calls[0].GDPRCutoff)
	}
}

// A non-positive general window disables the sweep without touching the
// store, so an unconfigured retention day count cannot delete rows.
func TestTickNoGeneralWindowIsNoOp(t *testing.T) {
	store := &fakeStore{perTenant: map[string]int{"acme": 9}}
	p := New(store, staticTenants{"acme"}, nil, Options{RetentionDays: 0})
	got, err := p.Tick(context.Background(), now)
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if got != 0 || len(store.calls) != 0 {
		t.Fatalf("pruned = %d, calls = %d; want 0 pruned and no store calls", got, len(store.calls))
	}
}

// spec: §16.4 — a per-tenant prune error stops the sweep and records
// the error outcome, returning the count pruned so far.
func TestTickPruneErrorStopsAndCounts(t *testing.T) {
	store := &fakeStore{
		perTenant: map[string]int{"a": 2, "b": 4, "c": 6},
		pruneErr:  errors.New("boom"),
		failAfter: 2,
	}
	m := newMetrics()
	p := New(store, staticTenants{"a", "b", "c"}, nil, Options{RetentionDays: 30, Metrics: m})
	got, err := p.Tick(context.Background(), now)
	if err == nil {
		t.Fatal("expected error")
	}
	if got != 2 {
		t.Errorf("pruned = %d, want 2 (tenant a only)", got)
	}
	if len(store.calls) != 2 {
		t.Errorf("calls = %d, want 2 (stopped after b failed)", len(store.calls))
	}
	if m.runs["error"] != 1 || m.runs["success"] != 0 {
		t.Errorf("runs = %v, want one error and no success", m.runs)
	}
}

func TestTickTenantListErrorIsReported(t *testing.T) {
	m := newMetrics()
	p := New(&fakeStore{}, errTenants{errors.New("no tenants")}, nil, Options{RetentionDays: 30, Metrics: m})
	if _, err := p.Tick(context.Background(), now); err == nil {
		t.Fatal("expected error")
	}
	if m.runs["error"] != 1 {
		t.Errorf("error runs = %d, want 1", m.runs["error"])
	}
}

// spec: §16.7 line 687 — ForceDrop records audit.partition_drop_forced
// with the window statistics and the operator's acknowledgement before
// it deletes, and forces past the SIEM guard.
func TestForceDropEmitsEventAndForces(t *testing.T) {
	store := &fakeStore{
		perTenant: map[string]int{"acme": 7},
		window: auditstore.RetentionWindow{
			OldestEvent:   now.Add(-400 * 24 * time.Hour),
			NewestEvent:   now.Add(-366 * 24 * time.Hour),
			Count:         7,
			SIEMHighWater: 42,
		},
	}
	var (
		emittedType    string
		emittedTenant  string
		emittedPayload map[string]any
		emitBeforeDel  bool
	)
	emit := func(_ context.Context, tenantID, eventType string, payload json.RawMessage, _ time.Time) error {
		emittedType = eventType
		emittedTenant = tenantID
		emitBeforeDel = len(store.calls) == 0 // delete has not run yet
		_ = json.Unmarshal(payload, &emittedPayload)
		return nil
	}
	p := New(store, staticTenants{"acme"}, emit, Options{RetentionDays: 365, SIEMConfigured: true})
	res, err := p.ForceDrop(context.Background(), "acme", "alice@acme.com", now)
	if err != nil {
		t.Fatalf("ForceDrop: %v", err)
	}
	if emittedType != "audit.partition_drop_forced" {
		t.Errorf("event type = %q", emittedType)
	}
	if emittedTenant != "platform" {
		t.Errorf("event tenant = %q, want platform", emittedTenant)
	}
	if !emitBeforeDel {
		t.Error("event must be emitted before the delete")
	}
	if !store.calls[0].Force {
		t.Error("forced prune must set Force=true")
	}
	if emittedPayload["acknowledged_data_loss"] != true {
		t.Errorf("acknowledged_data_loss = %v, want true", emittedPayload["acknowledged_data_loss"])
	}
	if emittedPayload["requester_sub"] != "alice@acme.com" {
		t.Errorf("requester_sub = %v", emittedPayload["requester_sub"])
	}
	if emittedPayload["siem_high_water_mark_at_drop"].(float64) != 42 {
		t.Errorf("siem_high_water_mark_at_drop = %v, want 42", emittedPayload["siem_high_water_mark_at_drop"])
	}
	if res.EventsLost != 7 {
		t.Errorf("EventsLost = %d, want 7 (actual deleted)", res.EventsLost)
	}
}

func TestForceDropNilEmitterRejected(t *testing.T) {
	p := New(&fakeStore{}, staticTenants{"acme"}, nil, Options{RetentionDays: 365})
	if _, err := p.ForceDrop(context.Background(), "acme", "alice", now); err == nil {
		t.Fatal("expected error when no emitter is configured")
	}
}

func TestForceDropEmptyTenantRejected(t *testing.T) {
	emit := func(context.Context, string, string, json.RawMessage, time.Time) error { return nil }
	p := New(&fakeStore{}, staticTenants{}, emit, Options{RetentionDays: 365})
	if _, err := p.ForceDrop(context.Background(), "", "alice", now); err == nil {
		t.Fatal("expected error on empty tenant")
	}
}

func TestForceDropNoWindowRejected(t *testing.T) {
	emit := func(context.Context, string, string, json.RawMessage, time.Time) error { return nil }
	p := New(&fakeStore{}, staticTenants{}, emit, Options{RetentionDays: 0})
	if _, err := p.ForceDrop(context.Background(), "acme", "alice", now); err == nil {
		t.Fatal("expected error when no retention window is configured")
	}
}

// spec: §16.4 line 378 — a tenant whose SIEM forwarder is stalled (held
// rows past the retention TTL) raises the partition-drop-blocked gauge
// to 1; a tenant with nothing held never creates a series.
func TestTickSetsPartitionDropBlockedGauge(t *testing.T) {
	store := &fakeStore{
		perTenant: map[string]int{"platform": 1, "acme": 0},
		held:      map[string]int{"acme": 4}, // SIEM holding 4 rows for acme
	}
	m := newMetrics()
	p := New(store, staticTenants{"platform", "acme"}, nil, Options{
		RetentionDays:  365,
		SIEMConfigured: true,
		Metrics:        m,
	})
	if _, err := p.Tick(context.Background(), now); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if !m.blocked["acme"] {
		t.Errorf("acme gauge = %v, want blocked=true", m.blocked["acme"])
	}
	// platform has zero held rows and was never blocked, so no series.
	if _, ok := m.blocked["platform"]; ok {
		t.Errorf("platform gauge was set to %v; an unblocked partition must not create a series", m.blocked["platform"])
	}
	if len(store.heldCalls) != 2 {
		t.Errorf("SIEMHeldCount calls = %d, want 2 (one per tenant)", len(store.heldCalls))
	}
}

// spec: §16.4 line 378 — once the forwarder catches up the held count
// returns to zero and the gauge is cleared exactly once; a partition
// that was never blocked is never cleared (no spurious 0 series).
func TestTickClearsPartitionDropBlockedOnRecovery(t *testing.T) {
	store := &fakeStore{
		perTenant: map[string]int{"acme": 0},
		held:      map[string]int{"acme": 3},
	}
	m := newMetrics()
	p := New(store, staticTenants{"acme"}, nil, Options{
		RetentionDays:  365,
		SIEMConfigured: true,
		Metrics:        m,
	})
	// First sweep: blocked.
	if _, err := p.Tick(context.Background(), now); err != nil {
		t.Fatalf("Tick 1: %v", err)
	}
	if !m.blocked["acme"] {
		t.Fatalf("after sweep 1 acme gauge = %v, want true", m.blocked["acme"])
	}
	// Forwarder catches up: held drops to 0.
	store.held["acme"] = 0
	if _, err := p.Tick(context.Background(), now); err != nil {
		t.Fatalf("Tick 2: %v", err)
	}
	if m.blocked["acme"] {
		t.Errorf("after recovery acme gauge = %v, want false", m.blocked["acme"])
	}
	// Third sweep, still recovered: must NOT re-emit a 0 (clear once).
	if _, err := p.Tick(context.Background(), now); err != nil {
		t.Fatalf("Tick 3: %v", err)
	}
	clears := 0
	for _, b := range m.blockedSets {
		if b.partition == "acme" && !b.blocked {
			clears++
		}
	}
	if clears != 1 {
		t.Errorf("acme cleared %d times, want exactly 1 (set-once / clear-once)", clears)
	}
}

// The SIEM delivery guard is inactive when no SIEM endpoint is
// configured, so the gauge is never touched even if the store would
// report held rows.
func TestTickSkipsGaugeWhenSIEMUnconfigured(t *testing.T) {
	store := &fakeStore{
		perTenant: map[string]int{"acme": 2},
		held:      map[string]int{"acme": 9},
	}
	m := newMetrics()
	p := New(store, staticTenants{"acme"}, nil, Options{
		RetentionDays:  365,
		SIEMConfigured: false,
		Metrics:        m,
	})
	if _, err := p.Tick(context.Background(), now); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if len(store.heldCalls) != 0 {
		t.Errorf("SIEMHeldCount called %d times with SIEM unconfigured, want 0", len(store.heldCalls))
	}
	if len(m.blockedSets) != 0 {
		t.Errorf("gauge set %d times with SIEM unconfigured, want 0", len(m.blockedSets))
	}
}

// A transient held-count read error must not flip the gauge: the
// destructive prune already succeeded, so the prior gauge state is held.
func TestTickHeldCountErrorLeavesGaugeUntouched(t *testing.T) {
	store := &fakeStore{
		perTenant: map[string]int{"acme": 0},
		heldErr:   errors.New("postgres unreachable"),
	}
	m := newMetrics()
	p := New(store, staticTenants{"acme"}, nil, Options{
		RetentionDays:  365,
		SIEMConfigured: true,
		Metrics:        m,
	})
	pruned, err := p.Tick(context.Background(), now)
	if err != nil {
		t.Fatalf("Tick must not fail on a held-count error: %v", err)
	}
	if pruned != 0 {
		t.Errorf("pruned = %d, want 0", pruned)
	}
	if len(m.blockedSets) != 0 {
		t.Errorf("gauge set %d times despite held-count error, want 0", len(m.blockedSets))
	}
}

func TestClampInterval(t *testing.T) {
	if got := clampInterval(0); got != DefaultPruneInterval {
		t.Errorf("clamp(0) = %v, want %v", got, DefaultPruneInterval)
	}
	if got := clampInterval(time.Second); got != MinPruneInterval {
		t.Errorf("clamp(1s) = %v, want %v", got, MinPruneInterval)
	}
	if got := clampInterval(2 * time.Hour); got != 2*time.Hour {
		t.Errorf("clamp(2h) = %v, want 2h", got)
	}
}
