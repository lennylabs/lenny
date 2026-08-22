// SPDX-License-Identifier: MIT

package main

import (
	"slices"
	"strings"
	"testing"
	"time"
)

// spec: §17.4 — the local load tier is where concurrency, ordering, and
// atomicity behavior is asserted, and the violations those cases hunt are
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

// spec: §17.4 — a stress budget is spent on a concurrency-sensitive case,
// so each iteration runs with the race detector on. A budget run with the
// detector off counts passes without checking the property the budget
// exists to protect.
func TestStressIterationsRunUnderTheRaceDetector(t *testing.T) {
	cmd := buildGoTestStressCmd("^TestX$", "./tests/tier7a_load_local/...", "load_local", 60)
	if !slices.Contains(cmd.Args, "-race") {
		t.Errorf("stress iteration argv = %v, want it to carry -race", cmd.Args)
	}
	if !strings.HasSuffix(cmd.Path, "go") && !strings.Contains(cmd.Path, "go") {
		t.Errorf("stress iteration runs %q, want the go tool", cmd.Path)
	}
}
