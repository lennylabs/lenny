// SPDX-License-Identifier: MIT

package pgstore

import (
	"testing"
	"time"
)

// fakeScanner mirrors pgx's QueryRow.Scan: it copies each prepared value
// into the matching destination pointer by type. The values slice must be
// in the same column order as selectColumns so scanRecord's mapping is
// exercised at tier-1 without a live Postgres.
type fakeScanner struct {
	values []any
	err    error
}

func (f fakeScanner) Scan(dest ...any) error {
	if f.err != nil {
		return f.err
	}
	for i, d := range dest {
		switch p := d.(type) {
		case *string:
			*p = f.values[i].(string)
		case *bool:
			*p = f.values[i].(bool)
		case *int64:
			*p = f.values[i].(int64)
		case *[]byte:
			if f.values[i] == nil {
				*p = nil
			} else {
				*p = f.values[i].([]byte)
			}
		case *time.Time:
			*p = f.values[i].(time.Time)
		case **time.Time:
			if f.values[i] == nil {
				*p = nil
			} else {
				t := f.values[i].(time.Time)
				*p = &t
			}
		}
	}
	return nil
}

// spec: §10.5 lines 466-540 — scanRecord maps a runtime_upgrade row onto
// a Record: the canary/draining INTEGER columns narrow to int, the
// nullable paused_at normalizes to UTC, and previous_pool_spec survives.
func TestScanRecord_mapping_spec_10_5(t *testing.T) {
	loc := time.FixedZone("UTC+2", 2*3600)
	paused := time.Date(2026, 6, 1, 10, 0, 0, 0, loc)
	entered := time.Date(2026, 6, 1, 9, 0, 0, 0, loc)
	created := time.Date(2026, 6, 1, 8, 0, 0, 0, loc)
	updated := time.Date(2026, 6, 1, 11, 0, 0, 0, loc)
	row := fakeScanner{values: []any{
		"claude-worker",         // pool
		"paused",                // phase
		"draining",              // prior_phase
		"img@sha256:abc",        // new_image
		[]byte(`{"minWarm":3}`), // previous_pool_spec
		"v2",                    // schema_version
		true,                    // drain_first
		int64(25),               // canary_percent
		int64(120),              // stabilization_window_secs
		int64(900),              // drain_timeout_secs
		true,                    // auto_advance
		"regressed",             // pause_reason
		paused,                  // paused_at (nullable)
		entered,                 // phase_entered_at
		int64(4),                // draining_sessions
		int64(7),                // version
		created,                 // created_at
		updated,                 // updated_at
	}}
	rec, err := scanRecord(row)
	if err != nil {
		t.Fatalf("scanRecord: %v", err)
	}
	if rec.Pool != "claude-worker" || rec.Phase != "paused" || rec.PriorPhase != "draining" {
		t.Fatalf("string columns: %+v", rec)
	}
	if rec.CanaryPercent != 25 || rec.DrainingSessions != 4 {
		t.Fatalf("int narrowing: canary=%d draining=%d", rec.CanaryPercent, rec.DrainingSessions)
	}
	if rec.SchemaVersion != "v2" || !rec.DrainFirst || !rec.AutoAdvance || rec.Version != 7 {
		t.Fatalf("scalar columns: %+v", rec)
	}
	if string(rec.PreviousPoolSpec) != `{"minWarm":3}` {
		t.Fatalf("previous_pool_spec = %q", rec.PreviousPoolSpec)
	}
	for _, ts := range []time.Time{rec.PausedAt, rec.PhaseEnteredAt, rec.CreatedAt, rec.UpdatedAt} {
		if ts.Location() != time.UTC {
			t.Fatalf("timestamp not normalized to UTC: %v (%v)", ts, ts.Location())
		}
	}
	if !rec.PausedAt.Equal(paused) {
		t.Fatalf("paused_at = %v, want %v", rec.PausedAt, paused)
	}
}

// A NULL paused_at (never paused) leaves Record.PausedAt zero, and a NULL
// previous_pool_spec leaves PreviousPoolSpec nil.
func TestScanRecord_nulls_spec_10_5(t *testing.T) {
	now := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	row := fakeScanner{values: []any{
		"p", "pending", "", "img", nil, "",
		false, int64(0), int64(120), int64(0), false, "",
		nil, now, int64(0), int64(1), now, now,
	}}
	rec, err := scanRecord(row)
	if err != nil {
		t.Fatalf("scanRecord: %v", err)
	}
	if !rec.PausedAt.IsZero() {
		t.Fatalf("NULL paused_at should be zero, got %v", rec.PausedAt)
	}
	if rec.PreviousPoolSpec != nil {
		t.Fatalf("NULL previous_pool_spec should be nil, got %q", rec.PreviousPoolSpec)
	}
}
