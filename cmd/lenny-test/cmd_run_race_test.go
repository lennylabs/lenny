// SPDX-License-Identifier: MIT

package main

import (
	"slices"
	"strings"
	"testing"
	"time"
)

// TESTING.md §17.4 (determinism) — the local load tier is where
// concurrency, ordering, and atomicity behavior is asserted, and the violations those cases hunt are
// data races on state two goroutines reach at once. The tier therefore
// invokes `go test` with the detector enabled; without it the cases run
// but the property half of what they assert goes unchecked.
func TestLoadLocalTierRunsUnderTheRaceDetector(t *testing.T) {
	args := taggedTierArgs("load_local", "./tests/tier7a_load_local/...", time.Minute, true)
	if !slices.Contains(args, "-race") {
		t.Errorf("load_local tier argv = %v, want it to carry -race", args)
	}
	if !slices.Contains(args, "-tags=load_local") {
		t.Errorf("load_local tier argv = %v, want it to carry -tags=load_local", args)
	}
	plain := taggedTierArgs("chaos", "./tests/tier8_chaos/...", time.Minute, false)
	if slices.Contains(plain, "-race") {
		t.Errorf("a tagged tier that did not ask for the detector got argv %v", plain)
	}
}

// TESTING.md §17.10 (flake budget) — a stress budget spent on a
// concurrency-sensitive case runs each iteration with the race detector on, because a budget run
// with the detector off counts passes without checking the property the
// budget exists to protect. The detector is a per-budget decision taken
// from the tier being stressed: a budget on any other test keeps a plain
// argv, since the detector needs cgo and distorts the wall-clock timing a
// latency scenario measures.
func TestStressIterationsRunUnderTheRaceDetector(t *testing.T) {
	cmd := buildGoTestStressCmd("^TestX$", "./tests/tier7a_load_local/...", "load_local", 60, true)
	if !slices.Contains(cmd.Args, "-race") {
		t.Errorf("stress iteration argv = %v, want it to carry -race", cmd.Args)
	}
	if !strings.HasSuffix(cmd.Path, "go") && !strings.Contains(cmd.Path, "go") {
		t.Errorf("stress iteration runs %q, want the go tool", cmd.Path)
	}
	plain := buildGoTestStressCmd("^TestX$", "./pkg/sandboxclaim/...", "", 60, false)
	if slices.Contains(plain.Args, "-race") {
		t.Errorf("a stress budget that did not ask for the detector got argv %v", plain.Args)
	}
}

// TESTING.md §17.10 (flake budget) — the detector default follows the
// tier a budget is spent on. The concurrency tiers get it; a budget against an ordinary package
// or a wall-clock scenario tier does not, so a routine quarantine check
// keeps working on a builder with no C toolchain and a latency budget
// measures un-instrumented timing. An explicit --race overrides either
// way, and an unknown value is rejected rather than silently defaulted.
func TestStressRaceDefaultFollowsTheTierBeingStressed(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mode   string
		tag    string
		target string
		want   bool
	}{
		{"auto on the local load tag", "auto", "load_local", "./...", true},
		{"auto on the local load package", "auto", "", "./tests/tier7a_load_local/...", true},
		{"auto on an ordinary package", "auto", "", "./pkg/...", false},
		{"auto on the kind load tier", "auto", "load_kind", "./tests/tier7b_load_kind/...", false},
		{"auto on the cloud load tier", "auto", "load_cloud", "./tests/tier12_load_cloud/...", false},
		{"explicit on", "on", "", "./pkg/...", true},
		{"explicit off", "off", "load_local", "./...", false},
	} {
		got, err := resolveStressRace(tc.mode, tc.tag, tc.target)
		if err != nil {
			t.Fatalf("%s: resolveStressRace returned %v", tc.name, err)
		}
		if got != tc.want {
			t.Errorf("%s: detector = %v, want %v", tc.name, got, tc.want)
		}
	}
	if _, err := resolveStressRace("sometimes", "", "./..."); err == nil {
		t.Error("resolveStressRace accepted an unknown --race value, want an error")
	}
}
