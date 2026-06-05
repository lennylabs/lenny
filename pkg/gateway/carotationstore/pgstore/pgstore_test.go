// SPDX-License-Identifier: MIT

package pgstore

import (
	"testing"
	"time"
)

// fakeRow assigns a pre-set list of column values onto the Scan
// destinations in order, so scanRecord's row→Record mapping can be
// exercised at tier-1 without a live Postgres.
type fakeRow struct {
	vals []any
}

func (f fakeRow) Scan(dest ...any) error {
	for i := range dest {
		switch d := dest[i].(type) {
		case *string:
			*d = f.vals[i].(string)
		case *int64:
			*d = f.vals[i].(int64)
		case *time.Time:
			*d = f.vals[i].(time.Time)
		case **time.Time:
			if f.vals[i] == nil {
				*d = nil
			} else {
				t := f.vals[i].(time.Time)
				*d = &t
			}
		}
	}
	return nil
}

// spec: §10.3 lines 344-350 — scanRecord maps a ca_rotation row into a
// carotationstore.Record in the SELECT column order: a mid-rotation row
// carries a non-NULL overlap_started_at that round-trips as UTC, the
// version widens, and the stage/CA-id strings re-type.
func TestScanRecord_midRotationRow_spec_10_3(t *testing.T) {
	started := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	updated := time.Date(2026, 6, 1, 0, 5, 0, 0, time.UTC)
	row := fakeRow{vals: []any{
		"promoted", "ca-old", "ca-new", started, int64(86400), int64(3), updated,
	}}
	rec, err := scanRecord(row)
	if err != nil {
		t.Fatalf("scanRecord: %v", err)
	}
	if rec.Stage != "promoted" || rec.CurrentCAID != "ca-old" || rec.NewCAID != "ca-new" {
		t.Errorf("strings mismatch: %+v", rec)
	}
	if !rec.OverlapStartedAt.Equal(started) || rec.OverlapWindowSecs != 86400 {
		t.Errorf("overlap mismatch: %+v", rec)
	}
	if rec.Version != 3 || !rec.UpdatedAt.Equal(updated) {
		t.Errorf("version/updated mismatch: %+v", rec)
	}
}

// An idle row carries a NULL overlap_started_at, which scanRecord leaves
// as the zero time.
func TestScanRecord_idleRowNullOverlap(t *testing.T) {
	updated := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	row := fakeRow{vals: []any{
		"idle", "ca-old", "", nil, int64(0), int64(1), updated,
	}}
	rec, err := scanRecord(row)
	if err != nil {
		t.Fatalf("scanRecord: %v", err)
	}
	if !rec.OverlapStartedAt.IsZero() {
		t.Errorf("idle OverlapStartedAt = %v, want zero", rec.OverlapStartedAt)
	}
	if rec.NewCAID != "" {
		t.Errorf("idle NewCAID = %q, want empty", rec.NewCAID)
	}
}
