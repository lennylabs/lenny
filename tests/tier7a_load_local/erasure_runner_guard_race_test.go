// SPDX-License-Identifier: MIT

//go:build load_local

// Tier-7a coverage for the erasure job-write guard under randomized
// interleavings. Two runners sweep one job in one Memory store while an
// operator retries it and clears its restriction concurrently, and every
// hook and eraser fails or yields at random points. After each round a
// checker reads the job's PhaseLog, the failure counter, the receipts, and
// the user's processing_restricted flag and asserts the guard's invariants.
//
// spec: §12.8 (Erasure job phase field (crash recovery)), §12.8 (Processing
// restriction during erasure (GDPR Article 18)), §24.12 (Erasure Job
// Management).
package tier7a_load_local_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"math/rand"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/environment/userstore"
	"github.com/lennylabs/lenny/pkg/gateway/storage/erasure"
	"github.com/lennylabs/lenny/pkg/gateway/storage/erasurejob"
)

// guardRound is one randomized round's shared state.
type guardRound struct {
	rng *rand.Rand
	mu  sync.Mutex

	failRate float64
	failures int
	receipts map[time.Time][][]byte
}

var errInjected = errors.New("injected fault")

// fault yields at random and fails with probability failRate.
func (g *guardRound) fault() error {
	g.mu.Lock()
	roll := g.rng.Float64()
	sleep := g.rng.Intn(3)
	rate := g.failRate
	g.mu.Unlock()
	switch sleep {
	case 0:
		runtime.Gosched()
	case 1:
		time.Sleep(time.Duration(roll*200) * time.Microsecond)
	}
	if roll < rate {
		return errInjected
	}
	return nil
}

func (g *guardRound) newRunner(jobs erasurejob.Store) *erasurejob.Runner {
	orch := erasure.New(erasure.Config{UserScoped: []erasure.Eraser{{
		Name: "sessions",
		DeleteByUser: func(context.Context, string, string) (int, error) {
			if err := g.fault(); err != nil {
				return 0, err
			}
			return 1, nil
		},
	}}})
	return erasurejob.NewRunner(jobs, orch, nil).
		WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))).
		WithSessionDrain(func(context.Context, string, string) error { return g.fault() }).
		WithHoldCheck(func(context.Context, string, string) ([]erasurejob.Hold, error) { return nil, g.fault() }).
		WithReleaseGate(func(context.Context, string, string) ([]string, error) { return nil, nil }).
		WithCompletionReceipt(func(_ context.Context, j erasurejob.Job) error {
			b, err := json.Marshal(j)
			if err != nil {
				return err
			}
			g.mu.Lock()
			g.receipts[j.ReceiptAt] = append(g.receipts[j.ReceiptAt], b)
			g.mu.Unlock()
			return g.fault()
		}).
		WithFailureObserver(func(string, string) {
			g.mu.Lock()
			g.failures++
			g.mu.Unlock()
		})
}

// checkGuardInvariants asserts the job-write guard's invariants on the
// round's final state.
func checkGuardInvariants(t *testing.T, round int, g *guardRound, job erasurejob.Job, user userstore.User) {
	t.Helper()
	failed := 0
	seen := map[erasurejob.Phase]bool{}
	for i, tr := range job.PhaseLog {
		switch tr.Phase {
		case erasurejob.PhaseInitiated:
			seen = map[erasurejob.Phase]bool{}
		case erasurejob.PhaseFailed:
			failed++
		case erasurejob.PhaseCompleted:
			if i != len(job.PhaseLog)-1 {
				t.Errorf("round %d: the job was written after it completed: %v", round, job.PhaseLog)
			}
		}
		if tr.Phase != erasurejob.PhaseInitiated && seen[tr.Phase] {
			t.Errorf("round %d: phase %s repeats within one attempt: %v", round, tr.Phase, job.PhaseLog)
		}
		seen[tr.Phase] = true
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.failures != failed {
		t.Errorf("round %d: failure counter %d, failed entries in PhaseLog %d", round, g.failures, failed)
	}
	for at, rs := range g.receipts {
		for _, r := range rs[1:] {
			if string(r) != string(rs[0]) {
				t.Errorf("round %d: receipts sharing ReceiptAt %v differ", round, at)
			}
		}
	}
	switch {
	case job.Phase == erasurejob.PhaseCompleted:
		if user.ProcessingRestricted {
			t.Errorf("round %d: restriction still set after completion", round)
		}
	case !job.Phase.Terminal():
		if !user.ProcessingRestricted || user.ErasureJobID != job.ID {
			t.Errorf("round %d: in-progress job but flag %v naming %q", round, user.ProcessingRestricted, user.ErasureJobID)
		}
	}
}

// TestErasureRunnerGuardUnderInterleavings_spec_12_8 runs randomized
// rounds of two runners sweeping one job while the operator retries and
// clears it, with faults and yields injected into every hook.
//
// spec: 12.8 (Erasure job phase field (crash recovery)), 12.8 (Processing restriction during erasure (GDPR Article 18)), 24.12 (Erasure Job Management)
// diagnosis: a failure means two executors, or an executor and an operator
// retry or clear, both wrote one attempt of an erasure job: a phase ran
// twice in an attempt, a failure was counted without its failed transition
// or twice for one, the job changed after it completed, two receipts of
// one attempt differ, or the restriction did not track the job.
func TestErasureRunnerGuardUnderInterleavings_spec_12_8(t *testing.T) {
	seed := time.Now().UnixNano()
	t.Logf("seed %d", seed)
	master := rand.New(rand.NewSource(seed))
	ctx := context.Background()
	for round := 0; round < 150; round++ {
		g := &guardRound{
			rng:      rand.New(rand.NewSource(master.Int63())),
			failRate: 0.15,
			receipts: map[time.Time][][]byte{},
		}
		users := userstore.NewMemory()
		if err := users.Create(ctx, userstore.User{TenantID: "acme", Subject: "alice"}); err != nil {
			t.Fatalf("seed user: %v", err)
		}
		jobs := erasurejob.NewMemory(erasurejob.WithRestrictionWriter(users))
		a, b := g.newRunner(jobs), g.newRunner(jobs)
		job, err := a.Start(ctx, "acme", "alice", "carol", nil)
		if err != nil {
			t.Fatalf("Start: %v", err)
		}

		var wg sync.WaitGroup
		for _, r := range []*erasurejob.Runner{a, b} {
			wg.Add(1)
			go func(r *erasurejob.Runner) {
				defer wg.Done()
				for i := 0; i < 6; i++ {
					_ = r.RunPending(ctx)
				}
			}(r)
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 6; i++ {
				_, _ = jobs.ClearRestriction(ctx, job.ID)
				runtime.Gosched()
				_, _ = jobs.Restart(ctx, job.ID)
				runtime.Gosched()
			}
		}()
		wg.Wait()

		// Drive the job to completion with faults off, so every round ends in
		// a terminal state the checker can judge in full.
		g.mu.Lock()
		g.failRate = 0
		g.mu.Unlock()
		for i := 0; i < 10; i++ {
			cur, err := jobs.Get(ctx, erasurejob.AllTenants, job.ID)
			if err != nil {
				t.Fatalf("Get: %v", err)
			}
			if cur.Phase == erasurejob.PhaseCompleted {
				break
			}
			if cur.Phase == erasurejob.PhaseFailed {
				if _, err := jobs.Restart(ctx, job.ID); err != nil {
					t.Fatalf("round %d: Restart: %v", round, err)
				}
			}
			_ = a.RunPending(ctx)
		}
		final, err := jobs.Get(ctx, erasurejob.AllTenants, job.ID)
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if final.Phase != erasurejob.PhaseCompleted {
			t.Fatalf("round %d: job ended %s, want completed", round, final.Phase)
		}
		user, err := users.Get(ctx, "acme", "alice")
		if err != nil {
			t.Fatalf("Get user: %v", err)
		}
		checkGuardInvariants(t, round, g, final, user)
	}
}
