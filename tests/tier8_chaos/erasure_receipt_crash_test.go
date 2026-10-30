// SPDX-License-Identifier: MIT

//go:build chaos

// Tier-8 chaos coverage for an erasure executor that dies between writing
// the completion receipt and completing the job. The job store, the user
// store, and the audit chain are the production Postgres stores. The first
// executor's receipt sink appends the receipt to the audit chain and then
// the executor dies: its context ends and no Complete runs. A second
// executor, built over a fresh pool as a restarted replica would be, then
// resumes the job from its persisted state.
//
// spec: §12.8 (Processing restriction during erasure (GDPR Article 18)),
// §12.8 (Erasure job phase field (crash recovery)).
package tier8_chaos_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lennylabs/lenny/pkg/common/seqname"
	"github.com/lennylabs/lenny/pkg/gateway/audit/auditstore"
	"github.com/lennylabs/lenny/pkg/gateway/environment/userstore"
	userpg "github.com/lennylabs/lenny/pkg/gateway/environment/userstore/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/externalapi/admin"
	"github.com/lennylabs/lenny/pkg/gateway/storage/erasure"
	"github.com/lennylabs/lenny/pkg/gateway/storage/erasurejob"
	erasurepg "github.com/lennylabs/lenny/pkg/gateway/storage/erasurejob/pgstore"
	"github.com/lennylabs/lenny/pkg/storerouter"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// errExecutorDied stands for the process exit between the receipt and
// Complete.
var errExecutorDied = errors.New("executor died after the receipt")

// receiptSinkOver appends the receipt as the gateway's receipt sink does:
// the EmitAdminEvent payload layout on the job's tenant chain at
// ReceiptAt.
func receiptSinkOver(store *auditstore.Store) func(context.Context, erasurejob.Job) error {
	return func(ctx context.Context, j erasurejob.Job) error {
		payload, err := json.Marshal(map[string]any{
			"actor_subject":   j.InitiatedBy,
			"actor_tenant_id": j.TenantID,
			"target_resource": j.UserID,
			"detail":          admin.ErasureCompletionReceipt(j),
		})
		if err != nil {
			return err
		}
		_, err = store.Append(ctx, j.TenantID, "gdpr.erasure_completed", payload, j.ReceiptAt)
		return err
	}
}

// erasureExecutor builds a runner over pool, as one gateway replica would.
func erasureExecutor(pool *pgxpool.Pool, sink func(context.Context, erasurejob.Job) error) *erasurejob.Runner {
	orch := erasure.New(erasure.Config{UserScoped: []erasure.Eraser{{
		Name:         "sessions",
		DeleteByUser: func(context.Context, string, string) (int, error) { return 2, nil },
	}}})
	return erasurejob.NewRunner(erasurepg.New(pool), orch, nil).
		WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))).
		WithSessionDrain(func(context.Context, string, string) error { return nil }).
		WithHoldCheck(func(context.Context, string, string) ([]erasurejob.Hold, error) { return nil, nil }).
		WithReleaseGate(func(context.Context, string, string) ([]string, error) { return nil, nil }).
		WithCompletionReceipt(sink)
}

// TestErasureReceiptThenCrashResumes_spec_12_8 drives the crash between the
// receipt and Complete and asserts the resumed job writes a byte-identical
// second receipt, completes, and clears the restriction.
//
// spec: 12.8 (Processing restriction during erasure (GDPR Article 18)), 12.8 (Erasure job phase field (crash recovery))
// diagnosis: a failure means a crash after the completion receipt either
// leaves the job stuck in progress with the user restricted, completes it
// without a receipt, or writes a second receipt whose content differs from
// the first, so the audit trail holds two different accounts of one
// erasure.
func TestErasureReceiptThenCrashResumes_spec_12_8(t *testing.T) {
	pg := containers.StartPostgres(t, containers.PostgresOptions{
		MigrationsDir: schematest.RepoRoot(t) + "/migrations",
	})
	ctx := context.Background()
	const tenant = "acme-erasure-crash"
	if _, err := pg.Pool.Exec(ctx, `INSERT INTO tenants (id, genesis_nonce) VALUES ($1, '\x01')`, tenant); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	if err := userpg.New(pg.Pool).Create(ctx, userstore.User{TenantID: tenant, Subject: "alice"}); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	if _, err := pg.Pool.Exec(ctx, "CREATE SEQUENCE IF NOT EXISTS "+seqname.AuditSequenceName(tenant)+
		" START WITH 1 INCREMENT BY 1 NO CYCLE"); err != nil {
		t.Fatalf("provision audit sequence: %v", err)
	}
	audit := auditstore.New(pg.Router(t))

	// The first executor writes the receipt and dies before Complete.
	crashCtx, crash := context.WithCancel(ctx)
	sink := receiptSinkOver(audit)
	first := erasureExecutor(pg.Pool, func(ctx context.Context, j erasurejob.Job) error {
		if err := sink(ctx, j); err != nil {
			return err
		}
		crash()
		return errExecutorDied
	})
	job, err := first.Start(ctx, tenant, "alice", "carol", nil)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := first.Run(crashCtx, job.ID); !errors.Is(err, errExecutorDied) {
		t.Fatalf("first executor Run = %v, want the crash", err)
	}
	if u, _ := userpg.New(pg.Pool).Get(ctx, tenant, "alice"); !u.ProcessingRestricted {
		t.Fatal("the restriction was lifted before Complete ran")
	}

	// A restarted replica: a fresh pool and fresh stores over the same
	// database.
	pool2, err := pgxpool.New(ctx, pg.DSN)
	if err != nil {
		t.Fatalf("second pool: %v", err)
	}
	defer pool2.Close()
	router2, err := storerouter.NewSingleShardRouter(storerouter.Config{Postgres: pool2})
	if err != nil {
		t.Fatalf("second router: %v", err)
	}
	second := erasureExecutor(pool2, receiptSinkOver(auditstore.New(router2)))
	if err := second.RunPending(ctx); err != nil {
		t.Fatalf("RunPending on the restarted replica: %v", err)
	}

	got, err := erasurepg.New(pool2).Get(ctx, erasurejob.AllTenants, job.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Phase != erasurejob.PhaseCompleted {
		t.Fatalf("phase after resume = %s, want completed", got.Phase)
	}
	if u, _ := userpg.New(pool2).Get(ctx, tenant, "alice"); u.ProcessingRestricted {
		t.Error("restriction still set after the resumed completion")
	}
	rows, err := audit.Rows(ctx, tenant)
	if err != nil {
		t.Fatalf("Rows: %v", err)
	}
	var receipts [][]byte
	for _, r := range rows {
		if r.EventType == "gdpr.erasure_completed" {
			receipts = append(receipts, r.Payload)
		}
	}
	if len(receipts) != 2 {
		t.Fatalf("receipts = %d, want 2 (one per executor)", len(receipts))
	}
	if !bytes.Equal(receipts[0], receipts[1]) {
		t.Errorf("the second receipt differs from the first:\n%s\n%s", receipts[0], receipts[1])
	}
}
