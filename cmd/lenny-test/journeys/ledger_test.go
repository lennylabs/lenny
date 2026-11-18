// SPDX-License-Identifier: MIT

package journeys

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// These tests exercise contributor tooling and carry no spec annotation.

func TestLedgerRefusesARunThatCouldExceedThePilotCap(t *testing.T) {
	path := LedgerPath(t.TempDir(), "pilot")
	l, err := OpenLedger(path, "pilot", DefaultPilotCapUSD)
	if err != nil {
		t.Fatal(err)
	}
	// Eight runs at $5.90 leave $6.80, which holds one more $6 run.
	for i := 1; i <= 8; i++ {
		if err := l.CheckStart(DefaultRunCapUSD); err != nil {
			t.Fatalf("run %d refused at $%.2f spent: %v", i, l.Spent(), err)
		}
		if err := l.Record(LedgerEntry{Run: "r", Journey: "j", Trial: i, AgentUSD: 5.90, Verdict: OutcomePass, At: time.Unix(0, 0)}); err != nil {
			t.Fatal(err)
		}
	}
	if err := l.CheckStart(DefaultRunCapUSD); err != nil {
		t.Fatalf("the ninth run fits at $%.2f spent: %v", l.Spent(), err)
	}
	if err := l.Record(LedgerEntry{AgentUSD: 1.5}); err != nil {
		t.Fatal(err)
	}
	err = l.CheckStart(DefaultRunCapUSD)
	if !errors.Is(err, ErrPilotBudget) || !strings.Contains(err.Error(), "48.7") {
		t.Fatalf("a run past the cap: %v", err)
	}
}

func TestLedgerExactlyFillsThePilotWithNineRunsAtTheCap(t *testing.T) {
	l, err := OpenLedger(LedgerPath(t.TempDir(), "pilot"), "pilot", DefaultPilotCapUSD)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 9; i++ {
		if err := l.CheckStart(DefaultRunCapUSD); err != nil {
			t.Fatalf("run %d refused: %v", i+1, err)
		}
		if err := l.Record(LedgerEntry{AgentUSD: DefaultRunCapUSD}); err != nil {
			t.Fatal(err)
		}
	}
	if err := l.CheckStart(0.01); !errors.Is(err, ErrPilotBudget) {
		t.Fatalf("a tenth run at a spent pilot: %v", err)
	}
}

func TestLedgerCountsInfrastructureCostAgainstTheCap(t *testing.T) {
	l, err := OpenLedger(LedgerPath(t.TempDir(), "cloud"), "cloud", 10)
	if err != nil {
		t.Fatal(err)
	}
	if err := l.Record(LedgerEntry{AgentUSD: 2, InfraUSD: 3}); err != nil {
		t.Fatal(err)
	}
	if l.Spent() != 5 {
		t.Fatalf("spent = %.2f, want agent and infrastructure together", l.Spent())
	}
	if err := l.CheckStart(4 + 2); !errors.Is(err, ErrPilotBudget) {
		t.Fatalf("a run whose agent and infrastructure caps exceed the rest: %v", err)
	}
	if err := l.CheckStart(5); err != nil {
		t.Fatalf("a run that fits: %v", err)
	}
}

func TestLedgerPersistsAndKeepsItsCap(t *testing.T) {
	dir := t.TempDir()
	path := LedgerPath(dir, "pilot")
	l, err := OpenLedger(path, "pilot", 54)
	if err != nil {
		t.Fatal(err)
	}
	if err := l.Record(LedgerEntry{Run: "r1", Journey: "operator-install", Trial: 1, Target: "kind", AgentUSD: 1.25, AgentCostReported: true, Verdict: OutcomeFail}); err != nil {
		t.Fatal(err)
	}
	again, err := OpenLedger(path, "pilot", 54)
	if err != nil {
		t.Fatal(err)
	}
	if len(again.Entries) != 1 || again.Entries[0].Journey != "operator-install" || again.Spent() != 1.25 {
		t.Fatalf("reloaded ledger = %+v", again)
	}
	if _, err := OpenLedger(path, "pilot", 100); err == nil || !strings.Contains(err.Error(), "records a pilot cap of $54.00") {
		t.Errorf("raising the cap of a started pilot: %v", err)
	}
	if _, err := OpenLedger(path, "other", 54); err == nil || !strings.Contains(err.Error(), "belongs to pilot") {
		t.Errorf("opening another pilot's ledger: %v", err)
	}
	if _, err := OpenLedger(path, "pilot", 0); err == nil {
		t.Error("a zero cap was accepted")
	}
	if err := os.WriteFile(path, []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenLedger(path, "pilot", 54); err == nil {
		t.Error("a corrupt ledger was accepted")
	}
	leftovers, _ := filepath.Glob(filepath.Join(dir, ".ledger-*"))
	if len(leftovers) != 0 {
		t.Errorf("the atomic write left temporary files: %v", leftovers)
	}
}

func TestLockExcludesASecondRunUntilReleased(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state", "journeys.lock")
	l, err := AcquireLock(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := AcquireLock(path); err == nil || !strings.Contains(err.Error(), "another journeys run") {
		t.Fatalf("a second lock while the first is held: %v", err)
	}
	// A file a dead process left is not a lock: only a held flock is.
	if err := l.Release(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strconv.Itoa(1<<30)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	l2, err := AcquireLock(path)
	if err != nil {
		t.Fatalf("an unheld lock file was refused: %v", err)
	}
	if err := l2.Release(); err != nil {
		t.Fatal(err)
	}
	if err := l2.Release(); err != nil {
		t.Errorf("releasing a released lock: %v", err)
	}
	if lp, err := LockPath(); err != nil || !strings.HasSuffix(lp, filepath.Join(".lenny-tmp", "journeys.lock")) {
		t.Errorf("lock path = %q, %v", lp, err)
	}
	if _, err := AcquireLock(t.TempDir()); err == nil {
		t.Error("a directory was accepted as the lock file")
	}
}
