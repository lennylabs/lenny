// SPDX-License-Identifier: MIT

//go:build integration

// Tier-4 integration tests for the slot-holder predicate on the Postgres
// session store: GetActiveSlotsByPod, the §5.2 rehydration seed, and
// ListPodsWithActiveSlots, the §12.4 recovery rebuild's pod listing, run
// against the production migrations.
package tier4_integration_test

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"testing"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	sessionpg "github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/pgstore"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// slotRow is one seeded session row.
type slotRow struct {
	tenant string
	state  session.State
	pod    string
	scrub  string
}

// seedSlotRows creates rows and returns their generated identifiers in order.
func seedSlotRows(t *testing.T, ctx context.Context, store *sessionpg.Store, rows []slotRow) []string {
	t.Helper()
	ids := make([]string, len(rows))
	for i, r := range rows {
		ids[i] = newSessionUUID(t)
		if err := store.Create(ctx, sessionstore.Session{
			ID: ids[i], TenantID: r.tenant, State: r.state, RuntimeRef: "echo",
			PodAssignment: r.pod, ScrubPolicy: r.scrub,
		}); err != nil {
			t.Fatalf("seed row %d: %v", i, err)
		}
	}
	return ids
}

func sortedJoin(v []string) string {
	out := append([]string(nil), v...)
	sort.Strings(out)
	return fmt.Sprint(out)
}

// spec: 5.2 (Post-recovery rehydration atomicity), 12.4 (Redis failure behavior), 7.3 (Resume flow after pod failure)
// diagnosis: the Postgres slot-holder queries disagree with the slot-holder
// predicate. GetActiveSlotsByPod must return exactly the non-terminal
// sessions naming the pod across tenants, and ListPodsWithActiveSlots each
// pod a non-terminal best-effort-per-slot row names, once, with no pod that
// only terminal or exclusive-pool rows name and no empty string. A row the
// failure funnel's edge write moved to resume_pending names no pod, so the
// seed, the §12.4 fallback gate, and the listing all exclude it.
func TestSlotHolderPredicateOnPostgres_spec_5_2(t *testing.T) {
	ctx := context.Background()
	pg := containers.StartPostgres(t, containers.PostgresOptions{
		MigrationsDir: filepath.Join(schematest.RepoRoot(t), "migrations"),
	})
	seedTenant(t, pg, "acme")
	seedTenant(t, pg, "globex")
	store := sessionpg.New(pg.Pool)
	const perSlot = "best-effort-per-slot"

	ids := seedSlotRows(t, ctx, store, []slotRow{
		{"acme", session.StateRunning, "pod-a", perSlot},       // 0: holder of pod-a
		{"globex", session.StateReady, "pod-a", perSlot},       // 1: holder of pod-a, other tenant
		{"acme", session.StateCompleted, "pod-a", perSlot},     // 2: terminal
		{"acme", session.StateRunning, "pod-b", perSlot},       // 3: funnel edge clears it below
		{"acme", session.StateCompleted, "pod-c", perSlot},     // 4: terminal only
		{"acme", session.StateRunning, "pod-x", "best-effort"}, // 5: exclusive pool
		{"globex", session.StateRunning, "pod-e", perSlot},     // 6: holder of pod-e
	})
	// The failure funnel's resume_pending edge clears pod_assignment.
	if _, err := store.Update(ctx, "acme", ids[3], func(r *sessionstore.Session) error {
		r.State = session.StateResumePending
		r.PodAssignment = ""
		return nil
	}); err != nil {
		t.Fatalf("funnel edge write: %v", err)
	}

	got, err := store.GetActiveSlotsByPod(ctx, "pod-a")
	if err != nil || sortedJoin(got) != sortedJoin([]string{ids[0], ids[1]}) {
		t.Fatalf("GetActiveSlotsByPod(pod-a) = (%v, %v), want rows 0 and 1", got, err)
	}
	for _, pod := range []string{"pod-b", "pod-c", ""} {
		if got, err := store.GetActiveSlotsByPod(ctx, pod); err != nil || len(got) != 0 {
			t.Fatalf("GetActiveSlotsByPod(%q) = (%v, %v), want none", pod, got, err)
		}
	}
	if count, admitted, err := store.ReserveSlotUnderLock(ctx, "pod-b", 1); err != nil || !admitted || count != 1 {
		t.Fatalf("ReserveSlotUnderLock(pod-b) = (%d, %v, %v), want (1, true, nil): the cleared row holds no slot", count, admitted, err)
	}
	pods, err := store.ListPodsWithActiveSlots(ctx)
	if err != nil {
		t.Fatalf("ListPodsWithActiveSlots: %v", err)
	}
	if got := sortedJoin(pods); got != "[pod-a pod-e]" {
		t.Fatalf("ListPodsWithActiveSlots = %s, want [pod-a pod-e]", got)
	}
}
