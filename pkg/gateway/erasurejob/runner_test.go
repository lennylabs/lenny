// SPDX-License-Identifier: MIT

package erasurejob_test

import (
	"context"
	"errors"
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
