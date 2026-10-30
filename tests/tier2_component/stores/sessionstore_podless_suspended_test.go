//go:build component

// SPDX-License-Identifier: MIT

package stores_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
)

// spec: 7.2 (Message delivery routing — seven paths, path 6)
// diagnosis: a failure means the leader's backlog re-check lists the wrong
// rows (a row that holds a pod, a non-suspended row, or another tenant's
// row), pages out of id order, or scans the sessions table instead of the
// podless-suspended partial index, so a large tenant's re-check slows the
// gateway leader.
func TestSessionStoreListPodlessSuspended_spec_7_2(t *testing.T) {
	t.Parallel()
	store, pg := startStore(t)
	ctx := context.Background()
	tenant := freshTenant(t, ctx, pg)
	other := freshTenant(t, ctx, pg)
	now := time.Now().UTC().Truncate(time.Microsecond)

	var podless []string
	seed := func(tenantID string, state session.State, pod string) string {
		id := newUUID(t)
		if err := store.Create(ctx, sessionstore.Session{
			ID: id, TenantID: tenantID, UserID: "alice", RuntimeRef: "echo", State: state,
			PodAssignment: pod, CreatedAt: now, UpdatedAt: now,
		}); err != nil {
			t.Fatalf("Create: %v", err)
		}
		return id
	}
	for range 5 {
		podless = append(podless, seed(tenant, session.StateSuspended, ""))
	}
	seed(tenant, session.StateSuspended, "pod-1")
	seed(tenant, session.StateRunning, "")
	seed(tenant, session.StateResumePending, "")
	seed(other, session.StateSuspended, "")

	var got []string
	after := ""
	for {
		page, err := store.ListPodlessSuspended(ctx, tenant, after, 2)
		if err != nil {
			t.Fatalf("ListPodlessSuspended: %v", err)
		}
		if len(page) > 2 {
			t.Fatalf("page of %d rows, want at most 2", len(page))
		}
		for _, row := range page {
			if row.State != session.StateSuspended || row.PodAssignment != "" || row.TenantID != tenant {
				t.Fatalf("listed row %+v, want the tenant's podless suspended rows only", row)
			}
			got = append(got, row.ID)
		}
		if len(page) < 2 {
			break
		}
		after = page[len(page)-1].ID
	}
	if len(got) != len(podless) {
		t.Fatalf("listed %d rows, want %d", len(got), len(podless))
	}
	for i := 1; i < len(got); i++ {
		if got[i-1] >= got[i] {
			t.Fatalf("rows out of id order: %v", got)
		}
	}

	// The planner serves the listing from the partial index with no sort.
	tx, err := pg.Pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SET LOCAL enable_seqscan = off`); err != nil {
		t.Fatalf("set: %v", err)
	}
	plan := explain(t, ctx, tx, `EXPLAIN SELECT id FROM sessions WHERE tenant_id = $1
		AND state = 'suspended' AND pod_assignment = '' AND id > $2::uuid ORDER BY id LIMIT 500`, tenant, got[0])
	if !strings.Contains(plan, "idx_sessions_podless_suspended") {
		t.Errorf("plan does not use idx_sessions_podless_suspended:\n%s", plan)
	}
	if strings.Contains(plan, "Sort") {
		t.Errorf("plan sorts; the index order serves ORDER BY id:\n%s", plan)
	}
}

// explain runs an EXPLAIN query and returns its plan text.
func explain(t *testing.T, ctx context.Context, tx pgx.Tx, q string, args ...any) string {
	t.Helper()
	rows, err := tx.Query(ctx, q, args...)
	if err != nil {
		t.Fatalf("explain: %v", err)
	}
	defer rows.Close()
	var b strings.Builder
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			t.Fatalf("scan plan: %v", err)
		}
		b.WriteString(line)
		b.WriteString("\n")
	}
	return b.String()
}
