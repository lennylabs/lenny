// SPDX-License-Identifier: MIT

package partitionmaint

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

func mustTime(t *testing.T, s string) time.Time {
	t.Helper()
	tm, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatalf("parse %q: %v", s, err)
	}
	return tm.UTC()
}

func contains(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}

// spec: §16.4 line 378 — the maintainer provisions the current partition
// and a few ahead so a write never races partition creation.
func TestPlan_CreatesCurrentAndAhead_spec_16_4_378(t *testing.T) {
	spec := Spec{Table: "session_logs", Granularity: Daily, Retention: SessionLogRetention, Ahead: 2}
	now := mustTime(t, "2026-06-07T13:45:00Z")
	creates, drops := Plan(spec, now, nil)
	if len(creates) != 3 {
		t.Fatalf("want 3 creates (current + 2 ahead), got %d: %+v", len(creates), creates)
	}
	wantNames := []string{"session_logs_p20260607", "session_logs_p20260608", "session_logs_p20260609"}
	for i, b := range creates {
		if b.Child != wantNames[i] {
			t.Errorf("create[%d] child = %q, want %q", i, b.Child, wantNames[i])
		}
	}
	// The current partition's bounds are [midnight today, midnight tomorrow).
	if got := creates[0].Lower; !got.Equal(mustTime(t, "2026-06-07T00:00:00Z")) {
		t.Errorf("lower = %v, want 2026-06-07T00:00:00Z", got)
	}
	if got := creates[0].Upper; !got.Equal(mustTime(t, "2026-06-08T00:00:00Z")) {
		t.Errorf("upper = %v, want 2026-06-08T00:00:00Z", got)
	}
	if len(drops) != 0 {
		t.Errorf("want no drops on an empty table, got %v", drops)
	}
}

func TestPlan_DefaultAheadWhenUnset(t *testing.T) {
	spec := Spec{Table: "stream_cursors", Granularity: Daily, Retention: StreamCursorRetention}
	now := mustTime(t, "2026-06-07T00:00:00Z")
	creates, _ := Plan(spec, now, nil)
	if len(creates) != DefaultAhead+1 {
		t.Fatalf("want %d creates with default ahead, got %d", DefaultAhead+1, len(creates))
	}
}

func TestPlan_SkipsExistingPartitions(t *testing.T) {
	spec := Spec{Table: "session_logs", Granularity: Daily, Retention: SessionLogRetention, Ahead: 2}
	now := mustTime(t, "2026-06-07T13:45:00Z")
	existing := []string{"session_logs_p20260607", "session_logs_p20260608"}
	creates, _ := Plan(spec, now, existing)
	if len(creates) != 1 || creates[0].Child != "session_logs_p20260609" {
		t.Fatalf("want only the missing ahead partition, got %+v", creates)
	}
}

// spec: §16.4 line 378 — a background job drops partitions beyond the
// retention window (30 days for session logs).
func TestPlan_DropsExpiredPartitions_spec_16_4_378(t *testing.T) {
	spec := Spec{Table: "session_logs", Granularity: Daily, Retention: SessionLogRetention, Ahead: 1}
	now := mustTime(t, "2026-06-07T13:45:00Z")
	// 40 days ago is beyond the 30-day window; 5 days ago is inside it.
	expired := childName("session_logs", mustTime(t, "2026-04-28T00:00:00Z"), Daily)
	inWindow := childName("session_logs", mustTime(t, "2026-06-02T00:00:00Z"), Daily)
	existing := []string{expired, inWindow, "session_logs_p20260607"}
	_, drops := Plan(spec, now, existing)
	if !contains(drops, expired) {
		t.Errorf("expired partition %q should be dropped, got %v", expired, drops)
	}
	if contains(drops, inWindow) {
		t.Errorf("in-window partition %q must not be dropped", inWindow)
	}
}

// spec: §16.4 line 378 — a partition is dropped only when its entire
// range is expired (exclusive upper bound at or before the cutoff).
func TestPlan_RetentionBoundary(t *testing.T) {
	spec := Spec{Table: "stream_cursors", Granularity: Daily, Retention: StreamCursorRetention}
	now := mustTime(t, "2026-06-08T00:00:00Z") // cutoff = 2026-06-01T00:00:00Z
	// Partition for 2026-05-31 has upper bound 2026-06-01T00:00:00Z == cutoff: drop.
	atBoundary := childName("stream_cursors", mustTime(t, "2026-05-31T00:00:00Z"), Daily)
	// Partition for 2026-06-01 has upper bound 2026-06-02 > cutoff: keep.
	justInside := childName("stream_cursors", mustTime(t, "2026-06-01T00:00:00Z"), Daily)
	_, drops := Plan(spec, now, []string{atBoundary, justInside})
	if !contains(drops, atBoundary) {
		t.Errorf("partition whose upper bound equals the cutoff should drop, got %v", drops)
	}
	if contains(drops, justInside) {
		t.Errorf("partition still holding in-window time must not drop")
	}
}

func TestPlan_IgnoresDefaultAndUnknownNames(t *testing.T) {
	spec := Spec{Table: "session_logs", Granularity: Daily, Retention: SessionLogRetention}
	now := mustTime(t, "2026-06-07T13:45:00Z")
	existing := []string{
		"session_logs_default",        // the catch-all: never dropped
		"session_logs_backup20200101", // foreign name: never parsed as a date partition
		"other_table_p20200101",       // different parent
	}
	_, drops := Plan(spec, now, existing)
	if len(drops) != 0 {
		t.Errorf("default / unknown names must never be dropped, got %v", drops)
	}
}

func TestPlan_RetentionDisabledNeverDrops(t *testing.T) {
	spec := Spec{Table: "session_logs", Granularity: Daily, Retention: 0}
	now := mustTime(t, "2026-06-07T00:00:00Z")
	old := childName("session_logs", mustTime(t, "2000-01-01T00:00:00Z"), Daily)
	_, drops := Plan(spec, now, []string{old})
	if len(drops) != 0 {
		t.Errorf("Retention=0 disables dropping, got %v", drops)
	}
}

// spec: §16.4 line 378 — audit's 365-day window uses coarser monthly
// partitions; verify the maintainer's monthly arithmetic and naming.
func TestPlan_Monthly(t *testing.T) {
	spec := Spec{Table: "audit_log", Granularity: Monthly, Retention: AuditRetention, Ahead: 1}
	now := mustTime(t, "2026-06-15T00:00:00Z")
	creates, drops := Plan(spec, now, []string{"audit_log_p202505"})
	if len(creates) != 2 || creates[0].Child != "audit_log_p202606" || creates[1].Child != "audit_log_p202607" {
		t.Fatalf("monthly create names wrong: %+v", creates)
	}
	if !creates[0].Upper.Equal(mustTime(t, "2026-07-01T00:00:00Z")) {
		t.Errorf("monthly upper bound wrong: %v", creates[0].Upper)
	}
	// 2025-05 is >365 days before 2026-06-15 → expired.
	if !contains(drops, "audit_log_p202505") {
		t.Errorf("expired monthly partition should drop, got %v", drops)
	}
}

func TestChildNameParseRoundTrip(t *testing.T) {
	for _, g := range []Granularity{Daily, Monthly} {
		start := periodStart(mustTime(t, "2026-06-07T09:00:00Z"), g)
		name := childName("session_logs", start, g)
		got, ok := parseChild("session_logs", name, g)
		if !ok {
			t.Fatalf("parseChild(%q) not ok", name)
		}
		if !got.Equal(start) {
			t.Errorf("roundtrip %v: got %v", g, got)
		}
	}
}

func TestParseChild_RejectsForeign(t *testing.T) {
	for _, name := range []string{"session_logs_default", "session_logs", "other_p20260607", "session_logs_pXXXX"} {
		if _, ok := parseChild("session_logs", name, Daily); ok {
			t.Errorf("parseChild(%q) should be rejected", name)
		}
	}
}

// --- fake driver ---------------------------------------------------------

type fakeDriver struct {
	mu        sync.Mutex
	parts     map[string][]string
	calls     []string
	listErr   error
	createErr error
	dropErr   error
}

func newFakeDriver() *fakeDriver { return &fakeDriver{parts: map[string][]string{}} }

func (f *fakeDriver) ListPartitions(_ context.Context, parent string) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.listErr != nil {
		return nil, f.listErr
	}
	out := make([]string, len(f.parts[parent]))
	copy(out, f.parts[parent])
	return out, nil
}

func (f *fakeDriver) CreatePartition(_ context.Context, parent, child string, _, _ time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.createErr != nil {
		return f.createErr
	}
	f.calls = append(f.calls, "create:"+child)
	f.parts[parent] = append(f.parts[parent], child)
	return nil
}

func (f *fakeDriver) DropPartition(_ context.Context, child string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.dropErr != nil {
		return f.dropErr
	}
	f.calls = append(f.calls, "drop:"+child)
	for parent, kids := range f.parts {
		for i, k := range kids {
			if k == child {
				f.parts[parent] = append(kids[:i:i], kids[i+1:]...)
			}
		}
	}
	return nil
}

type holdGuard struct {
	hold map[string]bool
	err  error
}

func (g holdGuard) HoldDrop(_ context.Context, _, child string) (bool, error) {
	if g.err != nil {
		return false, g.err
	}
	return g.hold[child], nil
}

// spec: §16.4 line 378 — one Tick provisions ahead and reclaims expired
// partitions; creation precedes dropping.
func TestMaintainerTick_CreatesThenDrops(t *testing.T) {
	d := newFakeDriver()
	expired := childName("session_logs", mustTime(t, "2026-04-01T00:00:00Z"), Daily)
	d.parts["session_logs"] = []string{"session_logs_default", expired}
	m := New(d, []Spec{{Table: "session_logs", Granularity: Daily, Retention: SessionLogRetention, Ahead: 1}}, Options{})
	res, err := m.Tick(context.Background(), mustTime(t, "2026-06-07T12:00:00Z"))
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if len(res) != 1 || res[0].Table != "session_logs" {
		t.Fatalf("unexpected results: %+v", res)
	}
	if len(res[0].Created) != 2 {
		t.Errorf("want 2 created (today + 1 ahead), got %v", res[0].Created)
	}
	if !contains(res[0].Dropped, expired) {
		t.Errorf("expired partition not dropped: %v", res[0].Dropped)
	}
	// Creation must come before the drop in the call log.
	firstDrop, lastCreate := -1, -1
	for i, c := range d.calls {
		if len(c) >= 6 && c[:6] == "create" {
			lastCreate = i
		}
		if firstDrop == -1 && len(c) >= 4 && c[:4] == "drop" {
			firstDrop = i
		}
	}
	if firstDrop != -1 && lastCreate > firstDrop {
		t.Errorf("a create ran after a drop: %v", d.calls)
	}
}

// spec: §16.4 line 378 — the SIEM delivery guard holds a past-TTL
// partition that still has undelivered events instead of dropping it.
func TestMaintainerTick_DropGuardHolds(t *testing.T) {
	d := newFakeDriver()
	expired := childName("audit_log", mustTime(t, "2024-01-01T00:00:00Z"), Monthly)
	d.parts["audit_log"] = []string{expired}
	guard := holdGuard{hold: map[string]bool{expired: true}}
	m := New(d, []Spec{{Table: "audit_log", Granularity: Monthly, Retention: AuditRetention}},
		Options{Guards: map[string]DropGuard{"audit_log": guard}})
	res, err := m.Tick(context.Background(), mustTime(t, "2026-06-07T00:00:00Z"))
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if !contains(res[0].Held, expired) {
		t.Errorf("guard-held partition should be in Held, got %+v", res[0])
	}
	if contains(res[0].Dropped, expired) {
		t.Errorf("guard-held partition must not be dropped")
	}
	for _, c := range d.calls {
		if c == "drop:"+expired {
			t.Errorf("DropPartition was called on a held partition")
		}
	}
}

func TestMaintainerTick_DropGuardErrorHoldsConservatively(t *testing.T) {
	d := newFakeDriver()
	expired := childName("audit_log", mustTime(t, "2024-01-01T00:00:00Z"), Monthly)
	d.parts["audit_log"] = []string{expired}
	guard := holdGuard{err: errors.New("siem state unreachable")}
	m := New(d, []Spec{{Table: "audit_log", Granularity: Monthly, Retention: AuditRetention}},
		Options{Guards: map[string]DropGuard{"audit_log": guard}})
	res, err := m.Tick(context.Background(), mustTime(t, "2026-06-07T00:00:00Z"))
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if !contains(res[0].Held, expired) || contains(res[0].Dropped, expired) {
		t.Errorf("a guard error must hold, not drop: %+v", res[0])
	}
}

func TestMaintainerTick_ListErrorPropagates(t *testing.T) {
	d := newFakeDriver()
	d.listErr = errors.New("boom")
	m := New(d, []Spec{{Table: "session_logs", Granularity: Daily, Retention: SessionLogRetention}}, Options{})
	if _, err := m.Tick(context.Background(), time.Now()); err == nil {
		t.Fatal("want error from list failure")
	}
}

func TestMaintainerTick_CreateErrorPropagates(t *testing.T) {
	d := newFakeDriver()
	d.createErr = errors.New("ddl denied")
	m := New(d, []Spec{{Table: "session_logs", Granularity: Daily, Retention: SessionLogRetention}}, Options{})
	if _, err := m.Tick(context.Background(), time.Now()); err == nil {
		t.Fatal("want error from create failure")
	}
}

func TestMaintainerTick_DropErrorPropagates(t *testing.T) {
	d := newFakeDriver()
	expired := childName("session_logs", mustTime(t, "2020-01-01T00:00:00Z"), Daily)
	d.parts["session_logs"] = []string{expired}
	d.dropErr = errors.New("drop denied")
	m := New(d, []Spec{{Table: "session_logs", Granularity: Daily, Retention: SessionLogRetention}}, Options{})
	if _, err := m.Tick(context.Background(), mustTime(t, "2026-06-07T00:00:00Z")); err == nil {
		t.Fatal("want error from drop failure")
	}
}

func TestClampInterval(t *testing.T) {
	if got := clampInterval(0); got != DefaultInterval {
		t.Errorf("zero -> %v, want default", got)
	}
	if got := clampInterval(time.Second); got != MinInterval {
		t.Errorf("below floor -> %v, want %v", got, MinInterval)
	}
	if got := clampInterval(2 * time.Hour); got != 2*time.Hour {
		t.Errorf("above floor -> %v", got)
	}
}

func TestMaintainerRun_ImmediateTickThenCancel(t *testing.T) {
	d := newFakeDriver()
	m := New(d, []Spec{{Table: "session_logs", Granularity: Daily, Retention: SessionLogRetention}},
		Options{Clock: func() time.Time { return mustTime(t, "2026-06-07T00:00:00Z") }})
	ctx, cancel := context.WithCancel(context.Background())
	ticked := make(chan struct{}, 1)
	go m.Run(ctx, func(_ []Result, _ error) {
		select {
		case ticked <- struct{}{}:
		default:
		}
	})
	select {
	case <-ticked:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not perform an immediate tick")
	}
	cancel()
	d.mu.Lock()
	created := len(d.calls)
	d.mu.Unlock()
	if created == 0 {
		t.Error("immediate tick created no partitions")
	}
}

// -race coverage: concurrent Ticks are serialized by the maintainer mutex.
func TestMaintainerTick_Concurrent(t *testing.T) {
	d := newFakeDriver()
	m := New(d, []Spec{{Table: "session_logs", Granularity: Daily, Retention: SessionLogRetention}}, Options{})
	now := mustTime(t, "2026-06-07T00:00:00Z")
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := m.Tick(context.Background(), now); err != nil {
				t.Errorf("concurrent Tick: %v", err)
			}
		}()
	}
	wg.Wait()
}

func ExamplePlan() {
	spec := Spec{Table: "session_logs", Granularity: Daily, Retention: SessionLogRetention, Ahead: 1}
	now, _ := time.Parse(time.RFC3339, "2026-06-07T10:00:00Z")
	creates, _ := Plan(spec, now, nil)
	for _, b := range creates {
		fmt.Println(b.Child)
	}
	// Output:
	// session_logs_p20260607
	// session_logs_p20260608
}
