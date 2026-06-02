// SPDX-License-Identifier: MIT

package erasurejob_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/erasure"
	"github.com/lennylabs/lenny/pkg/gateway/erasurejob"
)

// spec: §12.5 line 317 — the completion hook fires once a job reaches
// PhaseCompleted, carrying the job's tenant and user, so the gateway can
// trigger a tenant-scoped GC sweep for a gcPriority:high tenant. F-12.5.18.
func TestRunnerCompletionHookFiresOnCompletion_spec_12_5_317(t *testing.T) {
	jobs := erasurejob.NewMemory()
	at := time.Date(2026, 5, 16, 11, 0, 0, 0, time.UTC)
	orch := erasure.New(erasure.Config{UserScoped: []erasure.Eraser{userEraser("sessions", 1)}})

	var gotTenant, gotUser string
	calls := 0
	r := erasurejob.NewRunner(jobs, orch, fixedClock(at)).
		WithCompletionHook(func(_ context.Context, tenantID, userID string) {
			calls++
			gotTenant, gotUser = tenantID, userID
		})

	id, err := r.Start(context.Background(), "acme", "alice")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := r.Run(context.Background(), id); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if calls != 1 {
		t.Fatalf("completion hook fired %d times, want 1", calls)
	}
	if gotTenant != "acme" || gotUser != "alice" {
		t.Errorf("hook args = %s/%s, want acme/alice", gotTenant, gotUser)
	}
}

// spec: §12.5 line 317 — a failed erasure job never fires the completion
// hook, so a tenant whose erasure aborts mid-store does not trigger a
// premature immediate sweep. F-12.5.18.
func TestRunnerCompletionHookSkippedOnFailure_spec_12_5_317(t *testing.T) {
	jobs := erasurejob.NewMemory()
	at := time.Date(2026, 5, 16, 11, 0, 0, 0, time.UTC)
	orch := erasure.New(erasure.Config{UserScoped: []erasure.Eraser{{
		Name:         "sessions",
		DeleteByUser: func(context.Context, string, string) (int, error) { return 0, errors.New("boom") },
	}}})

	fired := false
	r := erasurejob.NewRunner(jobs, orch, fixedClock(at)).
		WithCompletionHook(func(context.Context, string, string) { fired = true })

	id, err := r.Start(context.Background(), "acme", "alice")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := r.Run(context.Background(), id); err == nil {
		t.Fatal("Run on a failing eraser returned nil, want error")
	}
	if fired {
		t.Error("completion hook fired on a failed job, want skipped")
	}
	job, _ := jobs.Get(context.Background(), id)
	if job.Phase != erasurejob.PhaseFailed {
		t.Errorf("Phase = %q, want failed", job.Phase)
	}
}
