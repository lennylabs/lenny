// SPDX-License-Identifier: MIT

package schemamigrate

import "testing"

// spec: §15.1 line 891 — buildStatus reports every applied migration as
// `complete` with a `not_run` gate, omitting versions above the current
// one.
func TestBuildStatusAppliedAreComplete(t *testing.T) {
	rep := buildStatus([]uint{1, 2, 3, 4}, 2, true, false)
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
	rep := buildStatus([]uint{1, 2, 3}, 3, true, true)
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
	rep := buildStatus([]uint{1, 2, 3}, 0, false, false)
	if rep.CurrentVersion != 0 || rep.Dirty || len(rep.Migrations) != 0 {
		t.Fatalf("empty-db report: %+v", rep)
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
