// SPDX-License-Identifier: MIT

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/cmd/lenny-test/verdictstatus"
)

// The static tier's package checks and their time budgets implement the
// test harness rather than a specification behavior, so these tests carry
// no spec annotation.

// TestStaticPackageChecksCoverEveryStaticPackage pins one check per Go
// test package of the static tier, each with a budget and the unverified
// classifier, and that every package directory under tests/static is in
// the table, so a fifth package cannot be added without a check that runs
// it.
func TestStaticPackageChecksCoverEveryStaticPackage(t *testing.T) {
	listed := map[string]bool{}
	for _, c := range staticPackageChecks(repoRoot()) {
		dir, ok := strings.CutPrefix(c.name, "go test ./")
		if !ok || !strings.HasSuffix(dir, "/...") || !strings.HasPrefix(dir, staticTierTree+"/") {
			t.Errorf("check %q does not name a static-tier package", c.name)
			continue
		}
		if c.budget <= 0 {
			t.Errorf("%s has no time budget", c.name)
		}
		if c.classify == nil {
			t.Errorf("%s has no classifier, so an unverified run would read as a pass", c.name)
		}
		listed[strings.TrimSuffix(dir, "/...")] = true
	}
	entries, err := os.ReadDir(filepath.Join(repoRoot(), staticTierTree))
	if err != nil {
		t.Fatalf("read %s: %v", staticTierTree, err)
	}
	for _, e := range entries {
		if !e.IsDir() || e.Name() == "internal" || e.Name() == "testdata" {
			continue
		}
		dir := staticTierTree + "/" + e.Name()
		if !listed[dir] {
			t.Errorf("%s holds a static-tier package that no check runs", dir)
		}
	}
	if len(listed) == 0 {
		t.Fatal("the static tier runs no Go test package")
	}
}

// TestStaticCheckOverBudgetIsReportedWithoutChangingItsStatus pins the
// budget report: a check that exceeds its budget keeps its own status,
// and the tier message names the overrun; a check within its budget, or
// with none, adds nothing.
func TestStaticCheckOverBudgetIsReportedWithoutChangingItsStatus(t *testing.T) {
	over := staticOutcome{name: "go test ./tests/static/structure/...", duration: 90 * time.Second, budget: 60 * time.Second}
	within := staticOutcome{name: "go test ./tests/static/build/...", duration: 5 * time.Second, budget: 30 * time.Second}
	none := staticOutcome{name: "buf lint", duration: time.Hour}

	status, msg := reduceStaticOutcomes([]staticOutcome{over, within, none})
	if status != verdictstatus.Pass {
		t.Fatalf("status %q; an overrun alone must not change the tier status", status)
	}
	want := "go test ./tests/static/structure/... exceeded its time budget: 1m30s, budget 1m0s"
	if !strings.Contains(msg, want) {
		t.Errorf("message %q lacks %q", msg, want)
	}
	if strings.Contains(msg, "build") || strings.Contains(msg, "buf lint") {
		t.Errorf("message names a check that kept within its budget or has none: %q", msg)
	}

	failing := over
	failing.err = errExit
	status, msg = reduceStaticOutcomes([]staticOutcome{failing})
	if status != verdictstatus.Fail || !strings.Contains(msg, "exceeded its time budget") {
		t.Errorf("a failing check over budget: status %q, message %q", status, msg)
	}
}

// errExit stands in for a check's non-zero exit.
var errExit = &os.PathError{Op: "exec", Path: "go", Err: os.ErrInvalid}
