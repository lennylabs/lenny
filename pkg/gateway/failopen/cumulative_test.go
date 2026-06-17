// SPDX-License-Identifier: MIT

package failopen

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// fakeClock returns a controllable time source.
type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time          { return c.t }
func (c *fakeClock) advance(d time.Duration) { c.t = c.t.Add(d) }

func newTestTimer(t *testing.T, clk *fakeClock, max time.Duration) *CumulativeTimer {
	t.Helper()
	return NewCumulativeTimer(CumulativeConfig{
		MaxSeconds: max,
		StatePath:  "-", // persistence disabled unless a test opts in
		Now:        clk.now,
	})
}

// spec: §12.4 line 224 — cumulative time accumulates across episodes.
func TestCumulativeAccumulatesAcrossEpisodes_spec_12_4(t *testing.T) {
	clk := &fakeClock{t: time.Date(2026, 6, 2, 12, 0, 0, 0, time.UTC)}
	tm := newTestTimer(t, clk, 300*time.Second)

	// First 59s outage.
	if !tm.Enter() {
		t.Fatal("first Enter should report the leading edge")
	}
	if tm.Enter() {
		t.Fatal("a second Enter inside the same episode must not re-report the edge")
	}
	clk.advance(59 * time.Second)
	tm.Exit()
	if got := tm.CumulativeSeconds(); got != 59 {
		t.Fatalf("after one 59s episode cumulative = %v, want 59", got)
	}

	// Gap, then a second 59s outage. The spec example: five 59s outages sum.
	clk.advance(10 * time.Second)
	tm.Enter()
	clk.advance(59 * time.Second)
	tm.Exit()
	if got := tm.CumulativeSeconds(); got != 118 {
		t.Fatalf("after two 59s episodes cumulative = %v, want 118", got)
	}
}

// spec: §12.4 line 224 — Exceeded trips at the configured maximum.
func TestCumulativeExceeded_spec_12_4(t *testing.T) {
	clk := &fakeClock{t: time.Date(2026, 6, 2, 12, 0, 0, 0, time.UTC)}
	tm := newTestTimer(t, clk, 300*time.Second)
	tm.Enter()
	clk.advance(299 * time.Second)
	if tm.Exceeded() {
		t.Fatal("must not be exceeded at 299s with a 300s cap")
	}
	clk.advance(2 * time.Second) // 301s in-flight
	if !tm.Exceeded() {
		t.Fatal("must be exceeded once cumulative reaches the 300s cap, including the open episode")
	}
}

// A non-positive max disables the threshold.
func TestCumulativeMaxDisabled(t *testing.T) {
	clk := &fakeClock{t: time.Date(2026, 6, 2, 12, 0, 0, 0, time.UTC)}
	tm := newTestTimer(t, clk, -1)
	tm.Enter()
	clk.advance(10 * time.Hour)
	if tm.Exceeded() {
		t.Fatal("a negative MaxSeconds disables the cumulative threshold")
	}
}

// spec: §12.4 line 224 — a rolling window drops time older than the window.
func TestCumulativeSlidingWindowPrunes_spec_12_4(t *testing.T) {
	clk := &fakeClock{t: time.Date(2026, 6, 2, 12, 0, 0, 0, time.UTC)}
	tm := NewCumulativeTimer(CumulativeConfig{
		Window:     time.Hour,
		MaxSeconds: 300 * time.Second,
		StatePath:  "-",
		Now:        clk.now,
	})
	tm.Enter()
	clk.advance(100 * time.Second)
	tm.Exit()
	if got := tm.CumulativeSeconds(); got != 100 {
		t.Fatalf("cumulative = %v, want 100", got)
	}
	// Advance past the rolling window: the episode falls out entirely.
	clk.advance(time.Hour + time.Second)
	if got := tm.CumulativeSeconds(); got != 0 {
		t.Fatalf("after the window elapsed cumulative = %v, want 0", got)
	}
}

// spec: §12.4 line 224 — only the overlap with the rolling window counts.
func TestCumulativePartialWindowOverlap_spec_12_4(t *testing.T) {
	clk := &fakeClock{t: time.Date(2026, 6, 2, 12, 0, 0, 0, time.UTC)}
	tm := NewCumulativeTimer(CumulativeConfig{
		Window:     time.Hour,
		MaxSeconds: 300 * time.Second,
		StatePath:  "-",
		Now:        clk.now,
	})
	tm.Enter()
	clk.advance(200 * time.Second)
	tm.Exit() // a 200s episode
	// Advance so only the last 50s of the episode remain inside the window.
	clk.advance(time.Hour - 50*time.Second)
	if got := tm.CumulativeSeconds(); got != 50 {
		t.Fatalf("partial-overlap cumulative = %v, want 50", got)
	}
}

// OnChange mirrors the cumulative value on every transition.
func TestCumulativeOnChange(t *testing.T) {
	clk := &fakeClock{t: time.Date(2026, 6, 2, 12, 0, 0, 0, time.UTC)}
	var last float64
	calls := 0
	tm := NewCumulativeTimer(CumulativeConfig{
		MaxSeconds: 300 * time.Second,
		StatePath:  "-",
		Now:        clk.now,
		OnChange:   func(s float64) { last = s; calls++ },
	})
	tm.Enter() // call 1, cumulative 0
	clk.advance(30 * time.Second)
	tm.Exit() // call 2, cumulative 30
	if calls != 2 {
		t.Fatalf("OnChange calls = %d, want 2", calls)
	}
	if last != 30 {
		t.Fatalf("OnChange last value = %v, want 30", last)
	}
}

// spec: §12.4 line 224 — a fresh state file resumes the cumulative value
// across restart so a CrashLoopBackOff replica cannot reset the timer.
func TestCumulativePersistenceResumesFreshFile_spec_12_4(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "failopen-cumulative.json")
	clk := &fakeClock{t: time.Date(2026, 6, 2, 12, 0, 0, 0, time.UTC)}

	tm := NewCumulativeTimer(CumulativeConfig{MaxSeconds: 300 * time.Second, StatePath: path, Now: clk.now})
	tm.Enter()
	clk.advance(120 * time.Second)
	tm.Exit() // 120s persisted

	// Restart: a new timer on the same clock loads the file.
	clk.advance(5 * time.Second)
	resumed := NewCumulativeTimer(CumulativeConfig{MaxSeconds: 300 * time.Second, StatePath: path, Now: clk.now})
	if got := resumed.CumulativeSeconds(); got != 120 {
		t.Fatalf("resumed cumulative = %v, want 120", got)
	}
}

// spec: §12.4 line 224 — a state file older than the rolling window is a
// cold start and the timer resets to zero.
func TestCumulativePersistenceStaleFileColdStart_spec_12_4(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "failopen-cumulative.json")
	clk := &fakeClock{t: time.Date(2026, 6, 2, 12, 0, 0, 0, time.UTC)}

	tm := NewCumulativeTimer(CumulativeConfig{Window: time.Hour, MaxSeconds: 300 * time.Second, StatePath: path, Now: clk.now})
	tm.Enter()
	clk.advance(120 * time.Second)
	tm.Exit()

	// Restart more than one window later: stale file ignored.
	clk.advance(2 * time.Hour)
	resumed := NewCumulativeTimer(CumulativeConfig{Window: time.Hour, MaxSeconds: 300 * time.Second, StatePath: path, Now: clk.now})
	if got := resumed.CumulativeSeconds(); got != 0 {
		t.Fatalf("stale-file cumulative = %v, want 0 (cold start)", got)
	}
}

// A corrupt state file is treated as a cold start, not a crash.
func TestCumulativePersistenceCorruptFileColdStart(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "failopen-cumulative.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	clk := &fakeClock{t: time.Date(2026, 6, 2, 12, 0, 0, 0, time.UTC)}
	tm := NewCumulativeTimer(CumulativeConfig{MaxSeconds: 300 * time.Second, StatePath: path, Now: clk.now})
	if got := tm.CumulativeSeconds(); got != 0 {
		t.Fatalf("corrupt-file cumulative = %v, want 0", got)
	}
}

// spec: §12.4 line 224 — a crash mid-episode (OpenSince persisted) credits
// the fail-open time elapsed before the crash so the bypass is closed.
func TestCumulativePersistenceDanglingOpenEpisode_spec_12_4(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "failopen-cumulative.json")
	clk := &fakeClock{t: time.Date(2026, 6, 2, 12, 0, 0, 0, time.UTC)}

	// Simulate a crash: enter fail-open, persist while open (Enter persists),
	// advance, then persist again without Exit by entering after a manual
	// re-write. Easiest: write a state file with an open episode by hand.
	openSince := clk.t
	updatedAt := clk.t.Add(90 * time.Second)
	st := persistState{UpdatedAt: updatedAt, OpenSince: &openSince}
	raw, _ := json.Marshal(st)
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}

	clk.t = updatedAt.Add(5 * time.Second)
	resumed := NewCumulativeTimer(CumulativeConfig{MaxSeconds: 300 * time.Second, StatePath: path, Now: clk.now})
	if got := resumed.CumulativeSeconds(); got != 90 {
		t.Fatalf("dangling-open cumulative = %v, want 90 (credited pre-crash time)", got)
	}
	// And the resumed replica starts healthy (no in-flight episode).
	if resumed.Enter() == false {
		t.Fatal("resumed replica should be healthy and able to report a fresh fail-open edge")
	}
}
