//go:build component

// SPDX-License-Identifier: MIT

// Component test for the §25.6 production diagnostic Postgres reader
// (pkg/ops/diagnostics/prodsource.PGReader) against a real Postgres
// container with the production migrations applied. It covers the
// session+pod join, the warm-pool pod-count breakdown read from
// agent_pod_state, the credential-pool load read from the platform-global
// credential_leases table, and the not-found / invalid-id paths. F-25.6.1.
package stores_test

import (
	"context"
	"testing"

	"github.com/lennylabs/lenny/pkg/ops/diagnostics/prodsource"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

func startDiagPG(t *testing.T) *containers.Postgres {
	t.Helper()
	return containers.StartPostgres(t, containers.PostgresOptions{
		MigrationsDir: schematest.RepoRoot(t) + "/migrations",
	})
}

// TestProdSourceSessionJoin seeds a tenant, a failed session, and its
// agent_pod_state row, then reads them back through the §25.6 reader.
//
// spec: §25.6 line 2885.
// diagnosis: a failure means the prod-source diagnostics reader joins
// the session and its agent_pod_state row incorrectly, so operator
// diagnostics would show wrong pod state for a failed session.
func TestProdSourceSessionJoin_spec_25_6_2885(t *testing.T) {
	pg := startDiagPG(t)
	ctx := context.Background()
	r := prodsource.NewPGReader(pg.Pool)

	const sessID = "11111111-1111-1111-1111-111111111111"
	if _, err := pg.Pool.Exec(ctx,
		`INSERT INTO tenants (id, genesis_nonce) VALUES ($1, '\x00')`, "acme"); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	if _, err := pg.Pool.Exec(ctx,
		`INSERT INTO sessions (id, tenant_id, state, runtime_ref, pool_ref, root_session_id,
			failure_class, failure_reason)
		 VALUES ($1, 'acme', 'failed', 'claude', 'default-gvisor', $1, 'runtime_crash', 'budget_exceeded')`,
		sessID); err != nil {
		t.Fatalf("seed session: %v", err)
	}
	if _, err := pg.Pool.Exec(ctx,
		`INSERT INTO agent_pod_state (pod_id, pool_id, state, tenant_id, session_id, isolation_profile,
			execution_mode, resource_version, node_name)
		 VALUES ('pod-1', 'default-gvisor', 'claimed', 'acme', $1, 'sandboxed', 'session', 1, 'node-a')`,
		sessID); err != nil {
		t.Fatalf("seed pod: %v", err)
	}

	row, err := r.Session(ctx, sessID)
	if err != nil {
		t.Fatalf("Session: %v", err)
	}
	if !row.Found || row.State != "failed" || row.Runtime != "claude" || row.Pool != "default-gvisor" {
		t.Fatalf("unexpected session row: %+v", row)
	}
	if row.FailureReason != "budget_exceeded" {
		t.Errorf("FailureReason = %q, want budget_exceeded", row.FailureReason)
	}
	if row.PodID != "pod-1" || row.PodState != "claimed" || row.NodeName != "node-a" {
		t.Errorf("pod join wrong: %+v", row)
	}
}

// TestProdSourceSessionNotFound covers the unknown-id and invalid-UUID
// paths: both report Found=false without an error.
//
// spec: §25.6 line 2885.
// diagnosis: a failure means the reader errors or reports Found=true for
// an unknown or malformed session id, so a missing session would surface
// as an error rather than a clean not-found.
func TestProdSourceSessionNotFound_spec_25_6_2885(t *testing.T) {
	pg := startDiagPG(t)
	ctx := context.Background()
	r := prodsource.NewPGReader(pg.Pool)

	if row, err := r.Session(ctx, "22222222-2222-2222-2222-222222222222"); err != nil || row.Found {
		t.Fatalf("unknown id: row=%+v err=%v", row, err)
	}
	if row, err := r.Session(ctx, "not-a-uuid"); err != nil || row.Found {
		t.Fatalf("invalid uuid must be not-found, got row=%+v err=%v", row, err)
	}
}

// TestProdSourcePoolPodCounts seeds pods in several states and reads the
// §25.6 pod-count breakdown.
//
// spec: §25.6 line 2861.
// diagnosis: a failure means the pod-count breakdown miscounts pods by
// state, so the operator pool diagnostics would misreport capacity.
func TestProdSourcePoolPodCounts_spec_25_6_2861(t *testing.T) {
	pg := startDiagPG(t)
	ctx := context.Background()
	r := prodsource.NewPGReader(pg.Pool)

	states := []struct {
		pod, state string
	}{
		{"p-idle-1", "idle"},
		{"p-idle-2", "idle"},
		{"p-warm-1", "warming"},
		{"p-claim-1", "claimed"},
		{"p-claim-2", "claimed"},
		{"p-claim-3", "claimed"},
		{"p-fail-1", "failed"},
		{"p-drain-1", "draining"}, // not counted in the breakdown
	}
	for _, s := range states {
		if _, err := pg.Pool.Exec(ctx,
			`INSERT INTO agent_pod_state (pod_id, pool_id, state, isolation_profile,
				execution_mode, resource_version)
			 VALUES ($1, 'default-gvisor', $2, 'sandboxed', 'session', 1)`,
			s.pod, s.state); err != nil {
			t.Fatalf("seed pod %s: %v", s.pod, err)
		}
	}

	counts, found, err := r.PoolPodCounts(ctx, "default-gvisor")
	if err != nil || !found {
		t.Fatalf("PoolPodCounts: found=%v err=%v", found, err)
	}
	if counts.Idle != 2 || counts.Warming != 1 || counts.Claimed != 3 || counts.Failed != 1 {
		t.Fatalf("breakdown wrong: %+v", counts)
	}

	if _, found, err := r.PoolPodCounts(ctx, "no-such-pool"); err != nil || found {
		t.Fatalf("unknown pool must be not-found, got found=%v err=%v", found, err)
	}
}

// TestProdSourceCredentialPoolLoad seeds leases on a pool and reads the
// per-credential load the hot-key analysis ranks.
//
// spec: §25.6.
// diagnosis: a failure means the per-credential load read is wrong, so
// the hot-key analysis would rank the wrong credentials.
func TestProdSourceCredentialPoolLoad_spec_25_6(t *testing.T) {
	pg := startDiagPG(t)
	ctx := context.Background()
	r := prodsource.NewPGReader(pg.Pool)

	leases := []struct {
		id, pool, cred string
	}{
		{"l1", "cp-1", "cred-a"},
		{"l2", "cp-1", "cred-a"},
		{"l3", "cp-1", "cred-a"},
		{"l4", "cp-1", "cred-b"},
		{"l5", "cp-2", "cred-z"}, // a different pool
	}
	for _, l := range leases {
		if _, err := pg.Pool.Exec(ctx,
			`INSERT INTO credential_leases (lease_id, delivery_mode, lease, pool_id, credential_id)
			 VALUES ($1, 'proxy', '{}'::jsonb, $2, $3)`,
			l.id, l.pool, l.cred); err != nil {
			t.Fatalf("seed lease %s: %v", l.id, err)
		}
	}

	load, err := r.CredentialPoolLoad(ctx, "cp-1")
	if err != nil || !load.Found {
		t.Fatalf("CredentialPoolLoad: found=%v err=%v", load.Found, err)
	}
	if load.ActiveLeases != 4 || load.LeasesByCredential["cred-a"] != 3 || load.LeasesByCredential["cred-b"] != 1 {
		t.Fatalf("load wrong: %+v", load)
	}

	if load, err := r.CredentialPoolLoad(ctx, "cp-empty"); err != nil || load.Found {
		t.Fatalf("pool with no leases must be not-found, got %+v err=%v", load, err)
	}
}
