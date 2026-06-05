// SPDX-License-Identifier: MIT

package mtls

import (
	"testing"
	"time"
)

// spec: §10.3 line 347 — a durable rotation resumes from the recorded
// stage. RestoreCARotation rebuilds the linear state machine and lets
// the operator continue the procedure across a gateway restart.
func TestRestoreCARotation_resumesAndContinues(t *testing.T) {
	started := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	clk := started.Add(48 * time.Hour) // overlap window already closed
	r, err := RestoreCARotation(RestoredCARotation{
		Stage:            CAStagePromoted,
		OldCAID:          "ca-old",
		NewCAID:          "ca-new",
		OverlapStartedAt: started,
	}, CARotationOptions{
		OverlapWindow: 24 * time.Hour,
		Now:           func() time.Time { return clk },
	})
	if err != nil {
		t.Fatalf("restore: %v", err)
	}
	snap := r.Snapshot()
	if snap.Stage != CAStagePromoted {
		t.Fatalf("stage = %q, want promoted", snap.Stage)
	}
	if snap.CurrentCAID != "ca-new" {
		t.Errorf("currentCAID = %q, want ca-new (new CA issues at promoted)", snap.CurrentCAID)
	}
	if len(snap.TrustedCAIDs) != 2 {
		t.Errorf("trusted = %v, want both CAs during overlap", snap.TrustedCAIDs)
	}
	// The overlap window closed, so RetireOldCA proceeds from the
	// restored stage rather than restarting.
	if err := r.RetireOldCA(); err != nil {
		t.Fatalf("retire after restore: %v", err)
	}
	if got := r.Snapshot().Stage; got != CAStageOldCARetired {
		t.Fatalf("stage = %q, want old_ca_retired", got)
	}
}

// spec: §10.3 — the overlap guard survives a restart: a restored
// promoted rotation still refuses RetireOldCA before the window closes.
func TestRestoreCARotation_overlapGuardSurvivesRestart(t *testing.T) {
	started := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	r, err := RestoreCARotation(RestoredCARotation{
		Stage:            CAStagePromoted,
		OldCAID:          "ca-old",
		NewCAID:          "ca-new",
		OverlapStartedAt: started,
	}, CARotationOptions{
		OverlapWindow: 24 * time.Hour,
		Now:           func() time.Time { return started.Add(time.Hour) },
	})
	if err != nil {
		t.Fatalf("restore: %v", err)
	}
	if err := r.RetireOldCA(); !IsOverlapOpen(err) {
		t.Fatalf("RetireOldCA err = %v, want overlap_open", err)
	}
}

func TestRestoreCARotation_invariants(t *testing.T) {
	started := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	cases := []struct {
		name string
		in   RestoredCARotation
	}{
		{"unknown stage", RestoredCARotation{Stage: "bogus", OldCAID: "ca"}},
		{"empty oldCAID", RestoredCARotation{Stage: CAStageIdle, OldCAID: ""}},
		{"idle with newCAID", RestoredCARotation{Stage: CAStageIdle, OldCAID: "ca", NewCAID: "ca2"}},
		{"non-idle without newCAID", RestoredCARotation{Stage: CAStageNewCADeployed, OldCAID: "ca", OverlapStartedAt: started}},
		{"newCAID equals oldCAID", RestoredCARotation{Stage: CAStagePromoted, OldCAID: "ca", NewCAID: "ca", OverlapStartedAt: started}},
		{"overlap stage without start", RestoredCARotation{Stage: CAStageNewCADeployed, OldCAID: "ca", NewCAID: "ca2"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := RestoreCARotation(tc.in, CARotationOptions{}); err == nil {
				t.Fatalf("RestoreCARotation(%+v) = nil error, want rejection", tc.in)
			}
		})
	}
}

// A restored idle rotation has no overlap start and one trusted CA, and
// a fresh Begin re-opens the window.
func TestRestoreCARotation_idleRoundTrip(t *testing.T) {
	r, err := RestoreCARotation(RestoredCARotation{Stage: CAStageIdle, OldCAID: "ca-old"}, CARotationOptions{})
	if err != nil {
		t.Fatalf("restore idle: %v", err)
	}
	if r.OldCAID() != "ca-old" || r.NewCAID() != "" {
		t.Fatalf("OldCAID=%q NewCAID=%q", r.OldCAID(), r.NewCAID())
	}
	if err := r.BeginNewCARotation("ca-new"); err != nil {
		t.Fatalf("begin: %v", err)
	}
	if r.NewCAID() != "ca-new" {
		t.Errorf("NewCAID = %q, want ca-new", r.NewCAID())
	}
}
