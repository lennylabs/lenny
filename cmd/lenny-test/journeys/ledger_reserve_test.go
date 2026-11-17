// SPDX-License-Identifier: MIT

package journeys

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// These tests exercise contributor tooling and carry no spec annotation.

func TestLedgerReservesAtTheCapAndSettlesAtTheCost(t *testing.T) {
	path := LedgerPath(t.TempDir(), "pilot")
	l, err := OpenLedger(path, "pilot", DefaultPilotCapUSD)
	if err != nil {
		t.Fatal(err)
	}
	e := LedgerEntry{ID: EntryID("r1", "operator-install", 1), Run: "r1", Journey: "operator-install", Trial: 1}
	if err := l.Reserve(e, DefaultRunCapUSD, 0.5); err != nil {
		t.Fatal(err)
	}
	if l.Spent() != 6.5 || !l.Entries[0].Pending || l.Entries[0].PID != os.Getpid() {
		t.Fatalf("after the reservation: spent $%.2f, entry %+v", l.Spent(), l.Entries[0])
	}
	// A second process reading the file sees the reservation charged.
	again, err := OpenLedger(path, "pilot", DefaultPilotCapUSD)
	if err != nil || again.Spent() != 6.5 {
		t.Fatalf("the reservation is not on disk: %v %v", again, err)
	}
	e.AgentUSD, e.InfraUSD, e.Verdict = 1.25, 0, OutcomeFail
	if err := l.Settle(e); err != nil {
		t.Fatal(err)
	}
	if len(l.Entries) != 1 || l.Entries[0].Pending || l.Spent() != 1.25 {
		t.Fatalf("after settling: %+v", l.Entries)
	}
	other := LedgerEntry{ID: "unreserved", AgentUSD: 0.01}
	if err := l.Settle(other); err != nil || len(l.Entries) != 2 {
		t.Fatalf("settling an unreserved entry keeps its cost: %v %+v", err, l.Entries)
	}
	if err := l.Reserve(LedgerEntry{}, 1, 0); err == nil {
		t.Error("a reservation without an ID was accepted")
	}
}

func TestLedgerKeepsADeadRunsReservationCharged(t *testing.T) {
	path := LedgerPath(t.TempDir(), "pilot")
	l, err := OpenLedger(path, "pilot", 12)
	if err != nil {
		t.Fatal(err)
	}
	if err := l.Record(LedgerEntry{ID: "r0/j/1", AgentUSD: 6, Pending: true, PID: 1 << 30}); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenLedger(path, "pilot", 12)
	if err != nil {
		t.Fatal(err)
	}
	live, dead := reopened.PendingEntries()
	if len(live) != 0 || len(dead) != 1 {
		t.Fatalf("live=%v dead=%v", live, dead)
	}
	if reopened.Spent() != 6 {
		t.Fatalf("a dead run's reservation is not charged: $%.2f", reopened.Spent())
	}
	if err := reopened.Reserve(LedgerEntry{ID: "r1/j/1"}, 6, 0); err != nil {
		t.Fatalf("a run that fits beside the dead reservation: %v", err)
	}
	if err := reopened.Reserve(LedgerEntry{ID: "r1/j/2"}, 6, 0); !errors.Is(err, ErrPilotBudget) {
		t.Fatalf("a run past the cap with a dead reservation charged: %v", err)
	}
	live, _ = reopened.PendingEntries()
	if len(live) != 1 {
		t.Errorf("this process's reservation is not live: %v", live)
	}
}

func TestLedgerAndLockAreWrittenPrivately(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "state")
	lock, err := AcquireLock(filepath.Join(dir, "journeys.lock"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lock.Release() }()
	l, err := OpenLedger(LedgerPath(dir, "pilot"), "pilot", 54)
	if err != nil {
		t.Fatal(err)
	}
	if err := l.Record(LedgerEntry{ID: "x"}); err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]os.FileMode{dir: 0o700, LedgerPath(dir, "pilot"): 0o600, filepath.Join(dir, "journeys.lock"): 0o600} {
		if fi, err := os.Stat(path); err != nil || fi.Mode().Perm() != want {
			t.Errorf("%s: %v, %v; want %04o", path, fi, err, want)
		}
	}
	if d, err := DefaultStateDir(); err != nil || !strings.HasSuffix(d, filepath.Join(".lenny-tmp", "journeys")) {
		t.Errorf("default state directory = %q, %v", d, err)
	}
}
