// SPDX-License-Identifier: MIT

//go:build load_local

package loadlocal_test

import (
	"context"
	"io"
	"log/slog"
	"math/rand"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/billing/billingstore"
	"github.com/lennylabs/lenny/pkg/gateway/api/admin"
	"github.com/lennylabs/lenny/pkg/gateway/core/session/sessionserver"
	"github.com/lennylabs/lenny/pkg/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/storage/erasure"
	"github.com/lennylabs/lenny/pkg/storage/erasurejob"
	"github.com/lennylabs/lenny/pkg/tenancy/tenantstore"
	"github.com/lennylabs/lenny/pkg/tenancy/userstore"
)

// slowTerminalBilling is a billing store whose Append of the terminal
// session.completed event waits delay first, as a loaded Postgres insert in
// the detached terminal pipeline can.
type slowTerminalBilling struct {
	*billingstore.Memory
	delay time.Duration
}

func (b slowTerminalBilling) Append(ctx context.Context, e billingstore.Event) (billingstore.Event, error) {
	if e.EventType == billingstore.EventSessionCompleted {
		time.Sleep(b.delay)
	}
	return b.Memory.Append(ctx, e)
}

// slowVerifyBilling is the erasure job's billing store, whose CountUser (the
// verification read) waits delay first, which widens the interval between
// the pseudonymizing step and the verification.
type slowVerifyBilling struct {
	*billingstore.Memory
	delay time.Duration
}

func (b slowVerifyBilling) CountUser(ctx context.Context, tenantID, userID string) (int, error) {
	time.Sleep(b.delay)
	return b.Memory.CountUser(ctx, tenantID, userID)
}

// rawUserEvents counts the tenant's billing events that carry userID.
func rawUserEvents(t *testing.T, billing *billingstore.Memory, tenantID, userID string) int {
	t.Helper()
	events, err := billing.Since(context.Background(), tenantID, 0, 0)
	if err != nil {
		t.Fatalf("Since: %v", err)
	}
	n := 0
	for _, e := range events {
		if e.UserID == userID {
			n++
		}
	}
	return n
}

// terminalBillingRound is one erasure of alice, whose live session the job
// drains through the session server's detached terminal pipeline.
type terminalBillingRound struct {
	billing *billingstore.Memory
	srv     *sessionserver.Server
	jobs    erasurejob.Store
	runner  *erasurejob.Runner
}

// newTerminalBillingRound builds the session server, the stores, and the
// erasure runner of one round, and seeds alice's live session and its
// session.created billing event.
func newTerminalBillingRound(t *testing.T, appendDelay, verifyDelay time.Duration) *terminalBillingRound {
	t.Helper()
	ctx := context.Background()
	sessions := memstore.New()
	if err := sessions.Create(ctx, sessionstore.Session{
		ID: "sess-live", TenantID: "acme", UserID: "alice", RuntimeRef: "echo", State: session.StateRunning,
	}); err != nil {
		t.Fatalf("seed session: %v", err)
	}
	billing := billingstore.NewMemory()
	if _, err := billing.Append(ctx, billingstore.Event{
		TenantID: "acme", UserID: "alice", SessionID: "sess-live", EventType: billingstore.EventSessionCreated,
	}); err != nil {
		t.Fatalf("seed billing: %v", err)
	}
	tenants := tenantstore.NewMemory()
	if err := tenants.Create(ctx, tenantstore.Tenant{ID: "acme"}); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	users := userstore.NewMemory()
	if err := users.Create(ctx, userstore.User{TenantID: "acme", Subject: "alice"}); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	srv := sessionserver.New(sessions, sessionserver.Options{
		DevTenantHeader: true,
		Billing:         slowTerminalBilling{Memory: billing, delay: appendDelay},
	})
	t.Cleanup(srv.Close)
	term := sessionserver.ForceTerminator{Store: sessions, BeginTerminal: srv.BeginTerminal, OnSessionTerminal: srv.OnSessionTerminal}
	jobs := erasurejob.NewMemory(erasurejob.WithRestrictionWriter(users))
	runner := erasurejob.NewRunner(jobs, erasure.New(erasure.Config{}), nil).
		WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))).
		WithSessionDrain(func(ctx context.Context, tenantID, userID string) error {
			_, err := admin.DrainSessions(ctx, sessions, term, nil, tenantID, userID)
			return err
		}).
		WithHoldCheck(func(context.Context, string, string) ([]erasurejob.Hold, error) { return nil, nil }).
		WithPipelineGate(func(ctx context.Context, tenantID, userID string) ([]string, error) {
			return admin.TerminalPipelinesPending(ctx, sessions, tenantID, userID, time.Now(), srv.TerminalPipelineBudget())
		}).
		WithBilling(erasurejob.NewBillingEraser(slowVerifyBilling{Memory: billing, delay: verifyDelay}, tenants)).
		WithCompletionReceipt(func(context.Context, erasurejob.Job) error { return nil })
	return &terminalBillingRound{billing: billing, srv: srv, jobs: jobs, runner: runner}
}

// runToTerminal runs the job, as the leader's sweep ticks do, until it
// reaches a terminal phase or the deadline passes, and returns the job.
func (r *terminalBillingRound) runToTerminal(t *testing.T, jobID string, rng *rand.Rand) erasurejob.Job {
	t.Helper()
	ctx := context.Background()
	deadline := time.Now().Add(20 * time.Second)
	for {
		_ = r.runner.Run(ctx, jobID)
		job, err := r.jobs.Get(ctx, erasurejob.AllTenants, jobID)
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if job.Phase.Terminal() || time.Now().After(deadline) {
			return job
		}
		time.Sleep(time.Duration(rng.Intn(500)) * time.Microsecond)
	}
}

// TestErasureLeavesNoRawUserIDInBillingAfterTheDetachedTerminalPipeline
// drains a live session of the erased user through the session server's
// detached terminal pipeline while the erasure runner ticks, with an injected
// delay before the pipeline's session.completed billing append and another
// before the job's verification read. The session has no pod, so the
// post-terminal window never holds it. In every round the job completes, and
// once the pipeline has returned no billing event carries alice's raw user
// id. On the runner before the pipeline mark, a delayed append landed after
// the pseudonymizing step, so the job failed verification or completed with
// the raw id left in the ledger.
//
// spec: §12.8 (Lifecycle fence and drain), §12.8 (Compliance Interfaces),
// §7.1 (Pipeline-pending session)
// diagnosis: a failure means the erasure job left store_deleting while a
// drained session's terminal pipeline could still append its billing event:
// the pipeline mark is written before the billing step or not at all, the
// gate does not read it (admin.TerminalPipelinesPending), or the runner does
// not run the gate before DeleteByUser and the pseudonymizing step.
func TestErasureLeavesNoRawUserIDInBillingAfterTheDetachedTerminalPipeline(t *testing.T) {
	seed := time.Now().UnixNano()
	t.Logf("seed %d", seed)
	master := rand.New(rand.NewSource(seed))
	ctx := context.Background()
	const rounds = 40
	for round := 0; round < rounds; round++ {
		rng := rand.New(rand.NewSource(master.Int63()))
		appendDelay := time.Duration(rng.Intn(30)) * time.Millisecond
		verifyDelay := time.Duration(rng.Intn(10)) * time.Millisecond
		if round == 0 {
			// The interleaving the merge gate failed on: the append lands
			// between the pseudonymizing step and the verification.
			appendDelay, verifyDelay = 20*time.Millisecond, 40*time.Millisecond
		}
		r := newTerminalBillingRound(t, appendDelay, verifyDelay)
		job, err := r.runner.Start(ctx, "acme", "alice", "carol", nil)
		if err != nil {
			t.Fatalf("round %d: Start: %v", round, err)
		}
		final := r.runToTerminal(t, job.ID, rng)
		if final.Phase != erasurejob.PhaseCompleted {
			t.Fatalf("round %d (append delay %v, verify delay %v): job phase = %s (%s), want completed",
				round, appendDelay, verifyDelay, final.Phase, final.Failure)
		}
		wctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		err = r.srv.WaitTerminalWork(wctx)
		cancel()
		if err != nil {
			t.Fatalf("round %d: wait for the terminal pipeline: %v", round, err)
		}
		if n := rawUserEvents(t, r.billing, "acme", "alice"); n != 0 {
			t.Fatalf("round %d (append delay %v, verify delay %v): %d billing event(s) carry the raw user id after the job completed",
				round, appendDelay, verifyDelay, n)
		}
		if n := len(mustSince(t, r.billing)); n != 2 {
			t.Errorf("round %d: billing events = %d, want session.created and session.completed", round, n)
		}
	}
}

// mustSince returns every billing event of acme.
func mustSince(t *testing.T, billing *billingstore.Memory) []billingstore.Event {
	t.Helper()
	events, err := billing.Since(context.Background(), "acme", 0, 0)
	if err != nil {
		t.Fatalf("Since: %v", err)
	}
	return events
}
