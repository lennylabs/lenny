// SPDX-License-Identifier: MIT

package lifecyclefence

import (
	"context"
	"strings"
	"testing"
)

// TestRacedSuspendDrainsEverySessionBeforeTheResume_spec_12_8 runs a suspend
// whose drain a resume overtakes on the in-memory posture and asserts the
// harness observes the resume-during-drain rule holding.
//
// spec: 12.8 (Compliance Interfaces: Lifecycle fence and drain), 15.1 (REST API)
func TestRacedSuspendDrainsEverySessionBeforeTheResume_spec_12_8(t *testing.T) {
	ctx := context.Background()
	w := NewWorld(t, MemoryStores(), 2)
	raced := w.SuspendRacingResume(ctx)
	if raced.ResumeSeq == 0 || raced.PostResume == "" {
		t.Fatalf("the resume or the post-resume create did not run: %+v", raced)
	}
	if len(raced.FailedSessions) != 1 || raced.FailedSessions[0] != raced.PostResume {
		t.Fatalf("failedSessions = %v, want only %s", raced.FailedSessions, raced.PostResume)
	}
	if !strings.Contains(w.Summary(), "raced suspends=1") {
		t.Fatalf("summary %q does not count the raced suspend", w.Summary())
	}
	w.Check(t)
}

// TestResumeDuringDrainViolations_spec_12_8 pins each misreport the raced
// suspend's check names, so a checker that stops judging one of them fails.
//
// spec: 12.8 (Compliance Interfaces: Lifecycle fence and drain)
func TestResumeDuringDrainViolations_spec_12_8(t *testing.T) {
	w := NewWorld(t, MemoryStores(), 0)
	w.record(CreateOutcome{ID: "before", Path: PathCreate, Owner: "bob", CommittedSeq: 5})
	w.record(CreateOutcome{ID: "after", Path: PathCreate, Owner: "bob", CommittedSeq: 15})
	w.record(CreateOutcome{ID: "later", Path: PathCreate, Owner: "bob", CommittedSeq: 20})
	raced := func(failed ...string) RacedSuspendResult {
		return RacedSuspendResult{SuspendResult: SuspendResult{Status: 200, FailedSessions: failed}, ResumeSeq: 10, PostResume: "after"}
	}
	cases := []struct {
		name string
		obs  racedObservation
		want string
	}{
		{"post-resume session reported", racedObservation{raced("after"), []string{"after"}}, ""},
		{"post-resume session terminal and unreported", racedObservation{raced(), nil}, ""},
		{"later create live and unreported", racedObservation{raced("after"), []string{"after", "later"}}, ""},
		{"pre-resume session reported", racedObservation{raced("before", "after"), []string{"after"}}, "predates the resume"},
		{"pre-resume session left live", racedObservation{raced("after"), []string{"before", "after"}}, "neither reported"},
		{"seeded session left live", racedObservation{raced("after"), []string{"seed", "after"}}, "neither reported"},
		{"post-resume session live and unreported", racedObservation{raced(), []string{"after"}}, "absent from failedSessions"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := strings.Join(w.resumeDuringDrainViolations(tc.obs), "; ")
			if tc.want == "" && got != "" || tc.want != "" && !strings.Contains(got, tc.want) {
				t.Fatalf("violations = %q, want %q", got, tc.want)
			}
		})
	}
}
