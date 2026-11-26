// SPDX-License-Identifier: MIT

//go:build security

// Security test for the completeness of a §12.8 user erasure whose drained
// session's terminal pipeline is slow. The session and billing stores are
// the production Postgres stores over a migrated database. The session server
// runs the real detached terminal pipeline, whose session.completed billing
// append is delayed, and the erasure job's verification read is delayed as
// well, which reproduces the interleaving of issue ERASE-I1. The job must
// complete with no billing event still carrying the erased user's raw id.
//
// spec: §12.8 (Lifecycle fence and drain), §12.8 (Compliance Interfaces),
// §7.1 (Pipeline-pending session).
package security_test

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/billing/billingstore"
	billingpg "github.com/lennylabs/lenny/pkg/billing/billingstore/pgstore"
	"github.com/lennylabs/lenny/pkg/common/seqname"
	"github.com/lennylabs/lenny/pkg/gateway/api/admin"
	"github.com/lennylabs/lenny/pkg/gateway/core/session/sessionserver"
	"github.com/lennylabs/lenny/pkg/session/sessionstore"
	sessionpg "github.com/lennylabs/lenny/pkg/session/sessionstore/pgstore"
	"github.com/lennylabs/lenny/pkg/storage/erasure"
	"github.com/lennylabs/lenny/pkg/storage/erasurejob"
	"github.com/lennylabs/lenny/pkg/tenancy/tenantstore"
	"github.com/lennylabs/lenny/pkg/tenancy/userstore"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// delayedCompletionBilling delays the terminal session.completed Append.
type delayedCompletionBilling struct {
	*billingpg.Store
	delay time.Duration
}

func (b delayedCompletionBilling) Append(ctx context.Context, e billingstore.Event) (billingstore.Event, error) {
	if e.EventType == billingstore.EventSessionCompleted {
		time.Sleep(b.delay)
	}
	return b.Store.Append(ctx, e)
}

// delayedVerification delays the erasure job's verification read.
type delayedVerification struct {
	*billingpg.Store
	delay time.Duration
}

func (b delayedVerification) CountUser(ctx context.Context, tenantID, userID string) (int, error) {
	time.Sleep(b.delay)
	return b.Store.CountUser(ctx, tenantID, userID)
}

// TestErasureIsCompleteWhenADrainedSessionsTerminalPipelineIsSlow erases a
// user whose two live sessions, neither holding a pod, the job drains
// through the session server's detached terminal pipeline, with the
// pipeline's session.completed append delayed past the start of the
// pseudonymizing step and the verification read delayed after it. The job
// completes, both session.completed events exist and carry the pseudonym,
// and no billing event of the tenant carries the raw user id, either when
// the job completes or once every pipeline has returned. Another user's
// events keep their id.
//
// spec: §12.8 (Lifecycle fence and drain), §12.8 (Compliance Interfaces),
// §7.1 (Pipeline-pending session)
// diagnosis: a failure means a user erasure completed, or failed
// verification, while a drained session's terminal pipeline could still
// append a billing event with the raw user id, so a verified erasure receipt
// can coexist with the user's id in billing_events: the pipeline mark is not
// written after the billing step, the Postgres session store drops it, or
// the store_deleting gate does not wait on it.
func TestErasureIsCompleteWhenADrainedSessionsTerminalPipelineIsSlow(t *testing.T) {
	pg := containers.StartPostgres(t, containers.PostgresOptions{MigrationsDir: schematest.RepoRoot(t) + "/migrations"})
	ctx := context.Background()
	const tenant, alice, bob = "acme", "alice@acme.com", "bob@acme.com"
	for _, stmt := range []string{
		`INSERT INTO tenants (id, genesis_nonce) VALUES ('acme', '\x01')`,
		"CREATE SEQUENCE IF NOT EXISTS " + seqname.BillingSequenceName(tenant) + " START WITH 1 INCREMENT BY 1 NO CYCLE",
		"CREATE SEQUENCE IF NOT EXISTS " + seqname.AuditSequenceName(tenant) + " START WITH 1 INCREMENT BY 1 NO CYCLE",
	} {
		if _, err := pg.Pool.Exec(ctx, stmt); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	sessions := sessionpg.New(pg.Pool)
	billing := billingpg.New(pg.Router(t))
	var aliceSessions []string
	for _, owner := range []string{alice, alice, bob} {
		id := uuid.NewString()
		if err := sessions.Create(ctx, sessionstore.Session{
			ID: id, TenantID: tenant, UserID: owner, RuntimeRef: "echo", State: session.StateRunning,
		}); err != nil {
			t.Fatalf("seed session: %v", err)
		}
		if _, err := billing.Append(ctx, billingstore.Event{
			TenantID: tenant, UserID: owner, SessionID: id, EventType: billingstore.EventSessionCreated,
		}); err != nil {
			t.Fatalf("seed billing: %v", err)
		}
		if owner == alice {
			aliceSessions = append(aliceSessions, id)
		}
	}

	srv := sessionserver.New(sessions, sessionserver.Options{
		DevTenantHeader: true,
		Billing:         delayedCompletionBilling{Store: billing, delay: 300 * time.Millisecond},
	})
	t.Cleanup(srv.Close)
	term := sessionserver.ForceTerminator{Store: sessions, BeginTerminal: srv.BeginTerminal, OnSessionTerminal: srv.OnSessionTerminal}
	users := userstore.NewMemory()
	if err := users.Create(ctx, userstore.User{TenantID: tenant, Subject: alice}); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	// The tenant registry holds the erasure salt, which the Postgres
	// registry stores only under a KMS provider; the in-memory registry
	// stands in for it, because the stores under test are the session and
	// billing stores.
	tenants := tenantstore.NewMemory()
	if err := tenants.Create(ctx, tenantstore.Tenant{ID: tenant}); err != nil {
		t.Fatalf("seed tenant registry: %v", err)
	}
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
		WithBilling(erasurejob.NewBillingEraser(delayedVerification{Store: billing, delay: 600 * time.Millisecond}, tenants)).
		WithCompletionReceipt(func(context.Context, erasurejob.Job) error { return nil })

	job, err := runner.Start(ctx, tenant, alice, "carol@acme.com", nil)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	deadline := time.Now().Add(30 * time.Second)
	var final erasurejob.Job
	for {
		_ = runner.Run(ctx, job.ID)
		if final, err = jobs.Get(ctx, erasurejob.AllTenants, job.ID); err != nil {
			t.Fatalf("Get: %v", err)
		}
		if final.Phase.Terminal() || time.Now().After(deadline) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if final.Phase != erasurejob.PhaseCompleted {
		t.Fatalf("job phase = %s (%s), want completed", final.Phase, final.Failure)
	}
	rawRows := func(user string) int {
		var n int
		if err := pg.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM billing_events WHERE tenant_id = $1 AND user_id = $2`, tenant, user).Scan(&n); err != nil {
			t.Fatalf("count billing events: %v", err)
		}
		return n
	}
	if n := rawRows(alice); n != 0 {
		t.Errorf("%d billing event(s) carry the raw user id when the job completed, want 0", n)
	}
	wctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := srv.WaitTerminalWork(wctx); err != nil {
		t.Fatalf("wait for the terminal pipelines: %v", err)
	}
	if n := rawRows(alice); n != 0 {
		t.Errorf("%d billing event(s) carry the raw user id after the pipelines returned, want 0", n)
	}
	for _, id := range aliceSessions {
		var n int
		if err := pg.Pool.QueryRow(ctx,
			`SELECT COUNT(*) FROM billing_events WHERE tenant_id = $1 AND session_id = $2::uuid AND event_type = 'session.completed' AND user_id <> $3`,
			tenant, id, alice).Scan(&n); err != nil {
			t.Fatalf("count completion events: %v", err)
		}
		if n != 1 {
			t.Errorf("session %s has %d pseudonymized session.completed events, want 1", id, n)
		}
	}
	if n := rawRows(bob); n != 1 {
		t.Errorf("another user's billing events with their id = %d, want 1", n)
	}
}
