// SPDX-License-Identifier: MIT

package schemamigrate

import (
	"testing"
	"time"
)

// spec: §15.1 line 891 — buildStatus reports every applied migration as
// `complete` with a `not_run` gate, omitting versions above the current
// one.
func TestBuildStatusAppliedAreComplete(t *testing.T) {
	rep := buildStatus([]uint{1, 2, 3, 4}, 2, true, false, nil)
	if rep.CurrentVersion != 2 || rep.Dirty {
		t.Fatalf("header: %+v", rep)
	}
	if len(rep.Migrations) != 2 {
		t.Fatalf("want 2 applied migrations, got %d: %+v", len(rep.Migrations), rep.Migrations)
	}
	for _, m := range rep.Migrations {
		if m.Phase != string(PhaseComplete) || m.GateCheckResult != string(GateNotRun) {
			t.Errorf("migration %d: phase=%q gate=%q", m.Version, m.Phase, m.GateCheckResult)
		}
		if m.Dirty {
			t.Errorf("clean migration %d marked dirty", m.Version)
		}
	}
}

// spec: §17.7 — a dirty current version is surfaced so the operator can
// roll it back; the prior applied versions remain clean.
func TestBuildStatusDirtyCurrent(t *testing.T) {
	rep := buildStatus([]uint{1, 2, 3}, 3, true, true, nil)
	if !rep.Dirty || rep.CurrentVersion != 3 {
		t.Fatalf("header: %+v", rep)
	}
	if len(rep.Migrations) != 3 {
		t.Fatalf("want 3 migrations, got %d", len(rep.Migrations))
	}
	for _, m := range rep.Migrations {
		wantDirty := m.Version == 3
		if m.Dirty != wantDirty {
			t.Errorf("migration %d dirty=%v want %v", m.Version, m.Dirty, wantDirty)
		}
	}
}

// A database with no applied migration reports an empty list and a zero
// current version.
func TestBuildStatusNoVersion(t *testing.T) {
	rep := buildStatus([]uint{1, 2, 3}, 0, false, false, nil)
	if rep.CurrentVersion != 0 || rep.Dirty || len(rep.Migrations) != 0 {
		t.Fatalf("empty-db report: %+v", rep)
	}
}

// spec: §24.13 line 150 — a Job-recorded phase row overlays the real
// applied-at timestamp, the resolved phase, the Phase 3 gate result, and
// the Job name onto the synthesized projection; versions with no row keep
// the synthesized `complete` / `not_run` values.
func TestBuildStatusOverlaysRecordedPhase(t *testing.T) {
	applied := time.Date(2026, 6, 4, 12, 0, 0, 0, time.UTC)
	phases := map[uint]PhaseRow{
		2: {
			Version:          2,
			Phase:            string(PhasePhase3Applied),
			AppliedAt:        applied,
			GateCheckResult:  "fail:7_rows",
			MigrationJobName: "lenny-migrate-3",
		},
	}
	rep := buildStatus([]uint{1, 2, 3}, 3, true, false, phases)
	if len(rep.Migrations) != 3 {
		t.Fatalf("want 3 migrations, got %d", len(rep.Migrations))
	}
	byVersion := map[uint]MigrationStatus{}
	for _, m := range rep.Migrations {
		byVersion[m.Version] = m
	}
	got := byVersion[2]
	if got.Phase != string(PhasePhase3Applied) {
		t.Errorf("version 2 phase = %q, want phase3_applied", got.Phase)
	}
	if got.GateCheckResult != "fail:7_rows" {
		t.Errorf("version 2 gate = %q, want fail:7_rows", got.GateCheckResult)
	}
	if got.MigrationJobName != "lenny-migrate-3" {
		t.Errorf("version 2 job = %q, want lenny-migrate-3", got.MigrationJobName)
	}
	if got.AppliedAt != "2026-06-04T12:00:00Z" {
		t.Errorf("version 2 appliedAt = %q, want 2026-06-04T12:00:00Z", got.AppliedAt)
	}
	// A version without a recorded row keeps the synthesized projection.
	if other := byVersion[1]; other.Phase != string(PhaseComplete) ||
		other.GateCheckResult != string(GateNotRun) || other.AppliedAt != "" || other.MigrationJobName != "" {
		t.Errorf("version 1 should fall back to synthesized projection, got %+v", other)
	}
}

// embeddedVersions parses the numbered migration file names into a
// sorted ascending version list. It exercises the real embed so a
// malformed filename surfaces here.
func TestEmbeddedVersionsSortedAscending(t *testing.T) {
	versions, err := embeddedVersions()
	if err != nil {
		t.Fatalf("embeddedVersions: %v", err)
	}
	if len(versions) == 0 {
		t.Fatal("no embedded migrations found")
	}
	if versions[0] != 1 {
		t.Errorf("first version = %d, want 1", versions[0])
	}
	for i := 1; i < len(versions); i++ {
		if versions[i] <= versions[i-1] {
			t.Fatalf("not ascending at %d: %d <= %d", i, versions[i], versions[i-1])
		}
	}
}
