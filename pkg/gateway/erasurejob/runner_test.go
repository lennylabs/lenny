// SPDX-License-Identifier: MIT

package erasurejob_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/billingstore"
	"github.com/lennylabs/lenny/pkg/gateway/erasure"
	"github.com/lennylabs/lenny/pkg/gateway/erasurejob"
	"github.com/lennylabs/lenny/pkg/gateway/tenantstore"
)

// fixedClock returns a deterministic clock for job timestamps.
func fixedClock(at time.Time) func() time.Time {
	return func() time.Time { return at }
}

// userEraser builds a user-scoped erasure adapter returning n.
func userEraser(name string, n int) erasure.Eraser {
	return erasure.Eraser{
		Name:         name,
		DeleteByUser: func(context.Context, string, string) (int, error) { return n, nil },
	}
}

func TestRunnerStartRecordsInitiatedJob(t *testing.T) {
	jobs := erasurejob.NewMemory()
	at := time.Date(2026, 5, 16, 10, 0, 0, 0, time.UTC)
	r := erasurejob.NewRunner(jobs, erasure.New(erasure.Config{}), fixedClock(at))

	id, err := r.Start(context.Background(), "acme", "alice")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	job, err := jobs.Get(context.Background(), id)
	if err != nil {
		t.Fatalf("Get after Start: %v", err)
	}
	if job.Phase != erasurejob.PhaseInitiated {
		t.Errorf("Phase = %q, want initiated", job.Phase)
	}
	if job.TenantID != "acme" || job.UserID != "alice" {
		t.Errorf("job target = %s/%s, want acme/alice", job.TenantID, job.UserID)
	}
	if !job.StartedAt.Equal(at) {
		t.Errorf("StartedAt = %v, want %v", job.StartedAt, at)
	}
}

func TestRunnerRunCompletesJob(t *testing.T) {
	jobs := erasurejob.NewMemory()
	at := time.Date(2026, 5, 16, 11, 0, 0, 0, time.UTC)
	orch := erasure.New(erasure.Config{UserScoped: []erasure.Eraser{
		userEraser("sessions", 3),
		userEraser("interactions", 5),
	}})
	r := erasurejob.NewRunner(jobs, orch, fixedClock(at))

	id, err := r.Start(context.Background(), "acme", "alice")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := r.Run(context.Background(), id); err != nil {
		t.Fatalf("Run: %v", err)
	}
	job, _ := jobs.Get(context.Background(), id)
	if job.Phase != erasurejob.PhaseCompleted {
		t.Errorf("Phase = %q, want completed", job.Phase)
	}
	if job.Total != 8 {
		t.Errorf("Total = %d, want 8", job.Total)
	}
	if job.Deleted["sessions"] != 3 || job.Deleted["interactions"] != 5 {
		t.Errorf("Deleted = %v, want sessions=3 interactions=5", job.Deleted)
	}
	if !job.CompletedAt.Equal(at) {
		t.Errorf("CompletedAt = %v, want %v", job.CompletedAt, at)
	}
	if job.Failure != "" {
		t.Errorf("Failure = %q, want empty on a completed job", job.Failure)
	}
}

func TestRunnerRunRecordsFailure(t *testing.T) {
	jobs := erasurejob.NewMemory()
	orch := erasure.New(erasure.Config{UserScoped: []erasure.Eraser{
		userEraser("first", 2),
		{Name: "broken", DeleteByUser: func(context.Context, string, string) (int, error) {
			return 0, errors.New("store down")
		}},
		userEraser("after", 9),
	}})
	r := erasurejob.NewRunner(jobs, orch, nil)

	id, err := r.Start(context.Background(), "acme", "alice")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := r.Run(context.Background(), id); err == nil {
		t.Fatal("Run should return the erasure error")
	}
	job, _ := jobs.Get(context.Background(), id)
	if job.Phase != erasurejob.PhaseFailed {
		t.Errorf("Phase = %q, want failed", job.Phase)
	}
	if job.Failure == "" {
		t.Error("Failure should carry the store error reason")
	}
	// The §12.8 fail-fast contract preserves the partial result: the
	// store erased before the failure is recorded, the one after is not.
	if job.Deleted["first"] != 2 {
		t.Errorf("Deleted[first] = %d, want 2 (erased before the failure)", job.Deleted["first"])
	}
	if _, ok := job.Deleted["after"]; ok {
		t.Error("a store after the failed one was recorded — the job is not fail-fast")
	}
}

func TestRunnerRunIsIdempotentForTerminalJob(t *testing.T) {
	jobs := erasurejob.NewMemory()
	calls := 0
	orch := erasure.New(erasure.Config{UserScoped: []erasure.Eraser{{
		Name: "sessions",
		DeleteByUser: func(context.Context, string, string) (int, error) {
			calls++
			return 1, nil
		},
	}}})
	r := erasurejob.NewRunner(jobs, orch, nil)

	id, _ := r.Start(context.Background(), "acme", "alice")
	if err := r.Run(context.Background(), id); err != nil {
		t.Fatalf("first Run: %v", err)
	}
	if err := r.Run(context.Background(), id); err != nil {
		t.Fatalf("re-Run of a completed job: %v", err)
	}
	if calls != 1 {
		t.Errorf("orchestrator invoked %d times, want 1 — a terminal job must not re-erase", calls)
	}
}

func TestRunnerRunUnknownJob(t *testing.T) {
	r := erasurejob.NewRunner(erasurejob.NewMemory(), erasure.New(erasure.Config{}), nil)
	if err := r.Run(context.Background(), "erasure_absent"); !errors.Is(err, erasurejob.ErrNotFound) {
		t.Errorf("Run unknown job: got %v, want ErrNotFound", err)
	}
}

func TestRunnerRunEmptyOrchestratorCompletes(t *testing.T) {
	jobs := erasurejob.NewMemory()
	r := erasurejob.NewRunner(jobs, erasure.New(erasure.Config{}), nil)
	id, _ := r.Start(context.Background(), "acme", "alice")
	if err := r.Run(context.Background(), id); err != nil {
		t.Fatalf("Run: %v", err)
	}
	job, _ := jobs.Get(context.Background(), id)
	if job.Phase != erasurejob.PhaseCompleted || job.Total != 0 {
		t.Errorf("job = %+v, want completed with Total 0", job)
	}
}

// spec: §12.8 lines 743-758 (layer 3) — when the per-job MemoryStore
// erasure preflight passes, the job proceeds through store deletion to
// completion as normal.
func TestRunnerRunMemoryPreflightPasses(t *testing.T) {
	jobs := erasurejob.NewMemory()
	orch := erasure.New(erasure.Config{UserScoped: []erasure.Eraser{userEraser("memory", 2)}})
	r := erasurejob.NewRunner(jobs, orch, nil).
		WithMemoryPreflight(func(context.Context) error { return nil })

	id, _ := r.Start(context.Background(), "acme", "alice")
	if err := r.Run(context.Background(), id); err != nil {
		t.Fatalf("Run: %v", err)
	}
	job, _ := jobs.Get(context.Background(), id)
	if job.Phase != erasurejob.PhaseCompleted {
		t.Errorf("Phase = %q, want completed", job.Phase)
	}
}

// spec: §12.8 lines 743-758 (layer 3) — a failing per-job preflight aborts
// the job as memory_store_preflight_failed before any store deletion runs,
// and increments lenny_erasure_job_failed_total{failure_phase=
// memory_store_preflight} via the failure observer.
func TestRunnerRunMemoryPreflightFailsAbortsBeforeDeletion(t *testing.T) {
	jobs := erasurejob.NewMemory()
	deletions := 0
	orch := erasure.New(erasure.Config{UserScoped: []erasure.Eraser{{
		Name: "memory",
		DeleteByUser: func(context.Context, string, string) (int, error) {
			deletions++
			return 1, nil
		},
	}}})

	var gotTenant, gotPhase string
	observed := 0
	r := erasurejob.NewRunner(jobs, orch, nil).
		WithFailureObserver(func(tenantID, phase string) {
			observed++
			gotTenant, gotPhase = tenantID, phase
		}).
		WithMemoryPreflight(func(context.Context) error {
			return errors.New("backend DeleteByUser regressed to a no-op")
		})

	id, _ := r.Start(context.Background(), "acme", "alice")
	err := r.Run(context.Background(), id)
	if err == nil {
		t.Fatal("Run should return the preflight failure")
	}
	if deletions != 0 {
		t.Errorf("store deletion ran %d times — the preflight must abort before step 8", deletions)
	}
	job, _ := jobs.Get(context.Background(), id)
	if job.Phase != erasurejob.PhaseFailed {
		t.Errorf("Phase = %q, want failed", job.Phase)
	}
	if !strings.Contains(job.Failure, "memory_store_preflight_failed") {
		t.Errorf("Failure = %q, want it to record memory_store_preflight_failed", job.Failure)
	}
	if observed != 1 || gotPhase != erasurejob.FailurePhaseMemoryStorePreflight || gotTenant != "acme" {
		t.Errorf("failure observer got (%d, tenant=%q, phase=%q), want (1, acme, %q)",
			observed, gotTenant, gotPhase, erasurejob.FailurePhaseMemoryStorePreflight)
	}
}

// The failure observer receives the store_delete phase when the
// orchestrator errors, confirming the §12.8 CMP-026 phase label threads
// through every fail path.
func TestRunnerFailureObserverStoreDeletePhase(t *testing.T) {
	jobs := erasurejob.NewMemory()
	orch := erasure.New(erasure.Config{UserScoped: []erasure.Eraser{{
		Name:         "broken",
		DeleteByUser: func(context.Context, string, string) (int, error) { return 0, errors.New("store down") },
	}}})
	var gotPhase string
	r := erasurejob.NewRunner(jobs, orch, nil).
		WithFailureObserver(func(_, phase string) { gotPhase = phase })

	id, _ := r.Start(context.Background(), "acme", "alice")
	if err := r.Run(context.Background(), id); err == nil {
		t.Fatal("Run should return the store error")
	}
	if gotPhase != erasurejob.FailurePhaseStoreDelete {
		t.Errorf("failure phase = %q, want %q", gotPhase, erasurejob.FailurePhaseStoreDelete)
	}
}

// stubBillingStore is a BillingErasureStore with fixed return values,
// for exercising the runner's billing verification-failure path.
type stubBillingStore struct {
	pseudonymized int
	remaining     int
}

func (s stubBillingStore) PseudonymizeUser(context.Context, string, string, []byte) (int, error) {
	return s.pseudonymized, nil
}

func (s stubBillingStore) CountUser(context.Context, string, string) (int, error) {
	return s.remaining, nil
}

func TestRunnerRunPseudonymizesBilling(t *testing.T) {
	ctx := context.Background()
	jobs := erasurejob.NewMemory()
	tenants := tenantstore.NewMemory()
	seedTenant(t, tenants, tenantstore.Tenant{ID: "acme"})
	billing := billingstore.NewMemory()
	seedBilling(t, billing, "acme", "alice@acme", 4)

	r := erasurejob.NewRunner(jobs, erasure.New(erasure.Config{}), nil).
		WithBilling(erasurejob.NewBillingEraser(billing, tenants))
	id, _ := r.Start(ctx, "acme", "alice@acme")
	if err := r.Run(ctx, id); err != nil {
		t.Fatalf("Run: %v", err)
	}

	job, _ := jobs.Get(ctx, id)
	if job.Phase != erasurejob.PhaseCompleted {
		t.Fatalf("Phase = %q, want completed", job.Phase)
	}
	if job.Billing.Disposition != "pseudonymized" || job.Billing.Pseudonymized != 4 {
		t.Errorf("Billing = %+v, want disposition=pseudonymized pseudonymized=4", job.Billing)
	}
	if !job.Billing.Verified {
		t.Error("Billing.Verified should be true after a clean pseudonymization")
	}
	if cnt, _ := billing.CountUser(ctx, "acme", "alice@acme"); cnt != 0 {
		t.Errorf("%d billing events still carry the original user id, want 0", cnt)
	}
}

func TestRunnerRunBillingExemptTenant(t *testing.T) {
	ctx := context.Background()
	jobs := erasurejob.NewMemory()
	tenants := tenantstore.NewMemory()
	seedTenant(t, tenants, tenantstore.Tenant{
		ID: "acme", BillingErasurePolicy: tenantstore.BillingErasureExempt,
	})
	billing := billingstore.NewMemory()
	seedBilling(t, billing, "acme", "alice@acme", 2)

	r := erasurejob.NewRunner(jobs, erasure.New(erasure.Config{}), nil).
		WithBilling(erasurejob.NewBillingEraser(billing, tenants))
	id, _ := r.Start(ctx, "acme", "alice@acme")
	if err := r.Run(ctx, id); err != nil {
		t.Fatalf("Run: %v", err)
	}

	job, _ := jobs.Get(ctx, id)
	if job.Phase != erasurejob.PhaseCompleted {
		t.Fatalf("Phase = %q, want completed", job.Phase)
	}
	if job.Billing.Disposition != "exempt" {
		t.Errorf("Billing.Disposition = %q, want exempt", job.Billing.Disposition)
	}
	if cnt, _ := billing.CountUser(ctx, "acme", "alice@acme"); cnt != 2 {
		t.Errorf("exempt tenant's billing events were rewritten: CountUser=%d, want 2", cnt)
	}
}

func TestRunnerRunBillingPseudonymizeFails(t *testing.T) {
	ctx := context.Background()
	jobs := erasurejob.NewMemory()
	tenants := tenantstore.NewMemory() // "acme" intentionally not seeded

	r := erasurejob.NewRunner(jobs, erasure.New(erasure.Config{}), nil).
		WithBilling(erasurejob.NewBillingEraser(billingstore.NewMemory(), tenants))
	id, _ := r.Start(ctx, "acme", "alice@acme")
	if err := r.Run(ctx, id); err == nil {
		t.Fatal("Run should fail when the tenant is absent from the registry")
	}

	job, _ := jobs.Get(ctx, id)
	if job.Phase != erasurejob.PhaseFailed {
		t.Errorf("Phase = %q, want failed", job.Phase)
	}
	if job.Failure == "" {
		t.Error("Failure should carry the pseudonymization error")
	}
}

func TestRunnerRunBillingVerificationFails(t *testing.T) {
	ctx := context.Background()
	jobs := erasurejob.NewMemory()
	tenants := tenantstore.NewMemory()
	seedTenant(t, tenants, tenantstore.Tenant{ID: "acme"})
	// A billing store that reports events still keyed to the user
	// after pseudonymization — the §12.8 verification must fail closed.
	stub := stubBillingStore{pseudonymized: 5, remaining: 3}

	r := erasurejob.NewRunner(jobs, erasure.New(erasure.Config{}), nil).
		WithBilling(erasurejob.NewBillingEraser(stub, tenants))
	id, _ := r.Start(ctx, "acme", "alice@acme")
	if err := r.Run(ctx, id); err == nil {
		t.Fatal("Run should fail when billing erasure verification does not pass")
	}

	job, _ := jobs.Get(ctx, id)
	if job.Phase != erasurejob.PhaseFailed {
		t.Errorf("Phase = %q, want failed", job.Phase)
	}
	// The pseudonymized partial result is recorded even on a
	// verification failure.
	if job.Billing.Disposition != "pseudonymized" || job.Billing.Pseudonymized != 5 {
		t.Errorf("Billing = %+v, want the pseudonymized partial result recorded", job.Billing)
	}
	if job.Billing.Verified {
		t.Error("Billing.Verified must be false on a verification failure")
	}
}

// phaseSeq extracts the ordered phase sequence from a job's PhaseLog.
func phaseSeq(job erasurejob.Job) []erasurejob.Phase {
	seq := make([]erasurejob.Phase, 0, len(job.PhaseLog))
	for _, tr := range job.PhaseLog {
		seq = append(seq, tr.Phase)
	}
	return seq
}

// samePhases reports whether two phase sequences are identical.
func samePhases(a, b []erasurejob.Phase) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// spec: §12.8 line 762 — the job records its phase at each transition so
// the completion receipt can present the per-phase timeline. Without a
// BillingEraser the sequence is initiated → store_deleting → completed.
func TestRunnerRunRecordsPhaseLog(t *testing.T) {
	jobs := erasurejob.NewMemory()
	at := time.Date(2026, 5, 16, 12, 0, 0, 0, time.UTC)
	orch := erasure.New(erasure.Config{UserScoped: []erasure.Eraser{userEraser("sessions", 1)}})
	r := erasurejob.NewRunner(jobs, orch, fixedClock(at))

	id, _ := r.Start(context.Background(), "acme", "alice")
	if err := r.Run(context.Background(), id); err != nil {
		t.Fatalf("Run: %v", err)
	}
	job, _ := jobs.Get(context.Background(), id)
	want := []erasurejob.Phase{
		erasurejob.PhaseInitiated,
		erasurejob.PhaseStoreDeleting,
		erasurejob.PhaseCompleted,
	}
	if got := phaseSeq(job); !samePhases(got, want) {
		t.Errorf("PhaseLog sequence = %v, want %v", got, want)
	}
	for _, tr := range job.PhaseLog {
		if !tr.At.Equal(at) {
			t.Errorf("PhaseLog entry %q timestamp = %v, want %v", tr.Phase, tr.At, at)
		}
	}
}

// spec: §12.8 line 762 — a pseudonymizing run logs every phase including
// pseudonymizing and verifying.
func TestRunnerRunPhaseLogWithBilling(t *testing.T) {
	ctx := context.Background()
	jobs := erasurejob.NewMemory()
	tenants := tenantstore.NewMemory()
	seedTenant(t, tenants, tenantstore.Tenant{ID: "acme"})
	billing := billingstore.NewMemory()
	seedBilling(t, billing, "acme", "alice@acme", 2)

	r := erasurejob.NewRunner(jobs, erasure.New(erasure.Config{}), nil).
		WithBilling(erasurejob.NewBillingEraser(billing, tenants))
	id, _ := r.Start(ctx, "acme", "alice@acme")
	if err := r.Run(ctx, id); err != nil {
		t.Fatalf("Run: %v", err)
	}
	job, _ := jobs.Get(ctx, id)
	want := []erasurejob.Phase{
		erasurejob.PhaseInitiated,
		erasurejob.PhaseStoreDeleting,
		erasurejob.PhasePseudonymizing,
		erasurejob.PhaseVerifying,
		erasurejob.PhaseCompleted,
	}
	if got := phaseSeq(job); !samePhases(got, want) {
		t.Errorf("PhaseLog sequence = %v, want %v", got, want)
	}
}

// spec: §12.8 line 762 — an exempt tenant skips the verifying phase, so
// the log omits it.
func TestRunnerRunPhaseLogExemptSkipsVerifying(t *testing.T) {
	ctx := context.Background()
	jobs := erasurejob.NewMemory()
	tenants := tenantstore.NewMemory()
	seedTenant(t, tenants, tenantstore.Tenant{
		ID: "acme", BillingErasurePolicy: tenantstore.BillingErasureExempt,
	})

	r := erasurejob.NewRunner(jobs, erasure.New(erasure.Config{}), nil).
		WithBilling(erasurejob.NewBillingEraser(billingstore.NewMemory(), tenants))
	id, _ := r.Start(ctx, "acme", "alice@acme")
	if err := r.Run(ctx, id); err != nil {
		t.Fatalf("Run: %v", err)
	}
	job, _ := jobs.Get(ctx, id)
	want := []erasurejob.Phase{
		erasurejob.PhaseInitiated,
		erasurejob.PhaseStoreDeleting,
		erasurejob.PhasePseudonymizing,
		erasurejob.PhaseCompleted,
	}
	if got := phaseSeq(job); !samePhases(got, want) {
		t.Errorf("PhaseLog sequence = %v, want %v (exempt skips verifying)", got, want)
	}
}

// spec: §12.8 line 762 — a failed job's phase log ends with the failed
// transition so the timeline shows where the erasure stopped.
func TestRunnerRunPhaseLogOnFailure(t *testing.T) {
	jobs := erasurejob.NewMemory()
	orch := erasure.New(erasure.Config{UserScoped: []erasure.Eraser{
		{Name: "broken", DeleteByUser: func(context.Context, string, string) (int, error) {
			return 0, errors.New("store down")
		}},
	}})
	r := erasurejob.NewRunner(jobs, orch, nil)
	id, _ := r.Start(context.Background(), "acme", "alice")
	if err := r.Run(context.Background(), id); err == nil {
		t.Fatal("Run should return the erasure error")
	}
	job, _ := jobs.Get(context.Background(), id)
	seq := phaseSeq(job)
	if len(seq) == 0 || seq[len(seq)-1] != erasurejob.PhaseFailed {
		t.Errorf("PhaseLog = %v, want a trailing failed transition", seq)
	}
}

// spec: §12.8 line 851 — VerificationOutcome maps the billing outcome to
// the salt-removal verification result recorded in the erasure receipt.
func TestBillingErasureOutcomeVerificationOutcome(t *testing.T) {
	cases := []struct {
		name string
		in   erasurejob.BillingErasureOutcome
		want string
	}{
		{"pseudonymized verified", erasurejob.BillingErasureOutcome{Disposition: "pseudonymized", Verified: true}, "verified"},
		{"pseudonymized unverified", erasurejob.BillingErasureOutcome{Disposition: "pseudonymized", Verified: false}, "unverified"},
		{"exempt", erasurejob.BillingErasureOutcome{Disposition: "exempt"}, "exempt"},
		{"no billing eraser", erasurejob.BillingErasureOutcome{}, "not_applicable"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.in.VerificationOutcome(); got != tc.want {
				t.Errorf("VerificationOutcome() = %q, want %q", got, tc.want)
			}
		})
	}
}
