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
	"time"

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

// terminateAt moves a seeded row to state through the production stamp,
// with the terminal write dated at.
func terminateAt(t *testing.T, ctx context.Context, store *sessionpg.Store, tenant, id string, state session.State, at time.Time) {
	t.Helper()
	if _, err := store.Update(ctx, tenant, id, func(r *sessionstore.Session) error {
		from := r.State
		r.State = state
		sessionstore.MarkTerminated(r, from, at)
		return nil
	}); err != nil {
		t.Fatalf("terminal write of %s: %v", id, err)
	}
}

// spec: 5.2 (Slot assignment atomicity), 7.2 (Interactive Session Model)
// diagnosis: the stale-slot reaper's Postgres read returns the wrong
// members. It must return exactly the identifiers whose rows are terminal
// with terminated_at before the cutoff, across tenants, and never a
// non-terminal row, a terminal row with no terminated_at, a missing
// identifier, or a string that is not a UUID; a relabel to failed must keep
// the first terminated_at, or a relabelled session's slot is reaped late.
func TestListStaleTerminalSessionsOnPostgres_spec_5_2(t *testing.T) {
	ctx := context.Background()
	pg := containers.StartPostgres(t, containers.PostgresOptions{
		MigrationsDir: filepath.Join(schematest.RepoRoot(t), "migrations"),
	})
	seedTenant(t, pg, "acme")
	seedTenant(t, pg, "globex")
	store := sessionpg.New(pg.Pool)
	const perSlot = "best-effort-per-slot"
	cutoff := time.Now().UTC().Add(-time.Hour).Truncate(time.Microsecond)

	ids := seedSlotRows(t, ctx, store, []slotRow{
		{"acme", session.StateRunning, "pod-a", perSlot},   // 0: ended before the cutoff
		{"globex", session.StateRunning, "pod-a", perSlot}, // 1: ended before the cutoff, other tenant
		{"acme", session.StateRunning, "pod-a", perSlot},   // 2: ended after the cutoff
		{"acme", session.StateRunning, "pod-a", perSlot},   // 3: non-terminal
		{"acme", session.StateRunning, "pod-a", perSlot},   // 4: terminal with no terminated_at
		{"acme", session.StateRunning, "pod-a", perSlot},   // 5: relabelled after the cutoff
	})
	terminateAt(t, ctx, store, "acme", ids[0], session.StateCompleted, cutoff.Add(-time.Minute))
	terminateAt(t, ctx, store, "globex", ids[1], session.StateCancelled, cutoff.Add(-time.Second))
	terminateAt(t, ctx, store, "acme", ids[2], session.StateExpired, cutoff.Add(time.Second))
	if _, err := store.Update(ctx, "acme", ids[4], func(r *sessionstore.Session) error {
		r.State = session.StateCompleted
		return nil
	}); err != nil {
		t.Fatalf("unstamped terminal write: %v", err)
	}
	terminateAt(t, ctx, store, "acme", ids[5], session.StateCompleted, cutoff.Add(-time.Minute))
	terminateAt(t, ctx, store, "acme", ids[5], session.StateFailed, cutoff.Add(time.Hour))

	query := append(append([]string(nil), ids...), newSessionUUID(t), "leaked:"+ids[3], "not-a-uuid", "")
	got, err := store.ListStaleTerminalSessions(ctx, query, cutoff)
	if err != nil {
		t.Fatalf("ListStaleTerminalSessions: %v", err)
	}
	if want := sortedJoin([]string{ids[0], ids[1], ids[5]}); sortedJoin(got) != want {
		t.Fatalf("ListStaleTerminalSessions = %s, want %s (rows 0, 1, and 5)", sortedJoin(got), want)
	}
	if got, err := store.ListStaleTerminalSessions(ctx, []string{"not-a-uuid"}, cutoff); err != nil || len(got) != 0 {
		t.Fatalf("ListStaleTerminalSessions(non-UUIDs only) = (%v, %v), want none", got, err)
	}
}
