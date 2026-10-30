//go:build component

// SPDX-License-Identifier: MIT

package stores_test

import (
	"context"
	"errors"
	"math/rand"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/storage/pgtenant"
)

// ptCap is the post-terminal cap the Postgres window tests run with.
const ptCap = 10 * time.Minute

// seedPT creates a per-slot session row naming pod in tenant.
func seedPT(t *testing.T, ctx context.Context, store sessionstore.Store, tenant, pod string, st session.State, terminated, released time.Time) string {
	t.Helper()
	id := newUUID(t)
	if err := store.Create(ctx, sessionstore.Session{
		ID: id, TenantID: tenant, UserID: "alice", RuntimeRef: "echo", State: st, PodAssignment: pod,
		ScrubPolicy: "best-effort-per-slot", TerminatedAt: terminated, PodReleasedAt: released,
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	return id
}

// spec: 7.1 (Post-terminal window), 5.2 (Post-recovery rehydration atomicity), 12.4 (Redis failure behavior)
// diagnosis: a failure means the Postgres slot queries do not count a
// terminal session whose pod release is not recorded within the cap (the
// rehydration seed omits it, the outage gate reissues its slot, or the
// rebuild drops its pod), keep counting it after PodReleasedAt is set or the
// cap passes, lose pod_released_at on the write and read paths, or scan the
// sessions table instead of the two partial indexes.
func TestSessionStorePostTerminalWindowQueries_spec_7_1(t *testing.T) {
	t.Parallel()
	_, pg := startStore(t)
	store := pgstore.New(pg.Pool, pgstore.WithPostTerminalCap(ptCap))
	ctx := context.Background()
	tenant := freshTenant(t, ctx, pg)
	now := time.Now().UTC().Truncate(time.Microsecond)
	pod := "pod-" + newUUID(t)[:8]

	sealing := seedPT(t, ctx, store, tenant, pod, session.StateCompleted, now.Add(-time.Minute), time.Time{})
	seedPT(t, ctx, store, tenant, pod, session.StateFailed, now.Add(-time.Minute), now.Add(-30*time.Second))
	seedPT(t, ctx, store, tenant, pod, session.StateExpired, now.Add(-2*ptCap), time.Time{})

	ids, err := store.GetActiveSlotsByPod(ctx, pod)
	if err != nil || !reflect.DeepEqual(ids, []string{sealing}) {
		t.Fatalf("GetActiveSlotsByPod = (%v, %v), want only the session in its window", ids, err)
	}
	pods, err := store.ListPodsWithActiveSlots(ctx)
	if err != nil || !containsString(pods, pod) {
		t.Errorf("ListPodsWithActiveSlots = (%v, %v), want it to list %s", pods, err, pod)
	}
	if _, admitted, err := store.ReserveSlotUnderLock(ctx, pod, 1); err != nil || admitted {
		t.Errorf("ReserveSlotUnderLock at bound 1 = (admitted %v, %v), want refused", admitted, err)
	}

	// A store with no cap counts no terminal row.
	plain := pgstore.New(pg.Pool)
	if ids, err := plain.GetActiveSlotsByPod(ctx, pod); err != nil || len(ids) != 0 {
		t.Errorf("uncapped GetActiveSlotsByPod = (%v, %v), want none", ids, err)
	}

	if err := sessionstore.RecordPodRelease(ctx, store, tenant, sealing, now); err != nil {
		t.Fatalf("RecordPodRelease: %v", err)
	}
	row, err := store.Get(ctx, tenant, sealing)
	if err != nil || !row.PodReleasedAt.Equal(now) {
		t.Fatalf("PodReleasedAt after the record = %v (%v), want %v", row.PodReleasedAt, err, now)
	}
	if ids, err := store.GetActiveSlotsByPod(ctx, pod); err != nil || len(ids) != 0 {
		t.Errorf("GetActiveSlotsByPod after the release = (%v, %v), want none", ids, err)
	}
	if pods, err := store.ListPodsWithActiveSlots(ctx); err != nil || containsString(pods, pod) {
		t.Errorf("ListPodsWithActiveSlots after the release = (%v, %v), want %s omitted", pods, err, pod)
	}
	if _, admitted, err := store.ReserveSlotUnderLock(ctx, pod, 1); err != nil || !admitted {
		t.Errorf("ReserveSlotUnderLock after the release = (admitted %v, %v), want admitted", admitted, err)
	}

	assertHolderPlans(t, ctx, pg.Pool.Begin, pod, now.Add(-ptCap))
}

// assertHolderPlans checks that each arm of the holder queries reads its
// partial index.
func assertHolderPlans(t *testing.T, ctx context.Context, begin func(context.Context) (pgx.Tx, error), pod string, cutoff time.Time) {
	t.Helper()
	tx, err := begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SET LOCAL enable_seqscan = off`); err != nil {
		t.Fatalf("set: %v", err)
	}
	const terminalArm = `pod_assignment <> '' AND pod_released_at IS NULL
		AND state IN ('completed', 'failed', 'cancelled', 'expired') AND terminated_at > $2`
	const liveArm = `pod_assignment <> '' AND state NOT IN ('completed', 'failed', 'cancelled', 'expired')`
	plan := explain(t, ctx, tx, `EXPLAIN SELECT id FROM sessions WHERE pod_assignment = $1 AND `+liveArm+
		` UNION ALL SELECT id FROM sessions WHERE pod_assignment = $1 AND `+terminalArm, pod, cutoff)
	for _, idx := range []string{"idx_sessions_active_by_pod", "idx_sessions_unreleased_terminal_by_pod"} {
		if !strings.Contains(plan, idx) {
			t.Errorf("holder plan does not read %s:\n%s", idx, plan)
		}
	}
}

// containsString reports whether ss contains s.
func containsString(ss []string, s string) bool {
	for _, v := range ss {
		if v == s {
			return true
		}
	}
	return false
}

// spec: 7.1 (Post-terminal window)
// diagnosis: a failure means the Postgres store and the in-memory store
// disagree on which sessions hold a pod, so a deployment's slot accounting
// depends on its backend.
func TestSessionStoreHoldersMatchMemstore_spec_7_1(t *testing.T) {
	t.Parallel()
	_, pg := startStore(t)
	pgs := pgstore.New(pg.Pool, pgstore.WithPostTerminalCap(ptCap))
	mem := memstore.NewWithClock(time.Now, memstore.WithPostTerminalCap(ptCap))
	ctx := context.Background()
	tenant := freshTenant(t, ctx, pg)
	seed := time.Now().UnixNano()
	t.Logf("seed %d", seed)
	rng := rand.New(rand.NewSource(seed))
	now := time.Now().UTC().Truncate(time.Microsecond)
	states := []session.State{session.StateRunning, session.StateSuspended, session.StateCompleted, session.StateFailed, session.StateCancelled, session.StateExpired}
	pods := []string{"pod-" + newUUID(t)[:8], "pod-" + newUUID(t)[:8], "pod-" + newUUID(t)[:8]}
	for range 40 {
		row := sessionstore.Session{
			ID: newUUID(t), TenantID: tenant, UserID: "alice", RuntimeRef: "echo",
			State: states[rng.Intn(len(states))], PodAssignment: pods[rng.Intn(len(pods))], ScrubPolicy: "best-effort-per-slot",
		}
		if session.IsTerminal(row.State) {
			// Offsets stay well away from the cap so the two clocks agree.
			switch rng.Intn(3) {
			case 0:
				row.TerminatedAt = now.Add(-time.Minute)
			case 1:
				row.TerminatedAt = now.Add(-3 * ptCap)
			}
			if rng.Intn(2) == 0 {
				row.PodReleasedAt = now.Add(-30 * time.Second)
			}
		}
		for _, s := range []sessionstore.Store{pgs, mem} {
			if err := s.Create(ctx, row); err != nil {
				t.Fatalf("Create: %v", err)
			}
		}
	}
	for _, pod := range pods {
		a, errA := pgs.GetActiveSlotsByPod(ctx, pod)
		b, errB := mem.GetActiveSlotsByPod(ctx, pod)
		sort.Strings(a)
		sort.Strings(b)
		if errA != nil || errB != nil || !reflect.DeepEqual(a, b) {
			t.Errorf("pod %s holders: postgres %v (%v), memstore %v (%v)", pod, a, errA, b, errB)
		}
	}
}

// spec: 7.1 (Derive session semantics)
// diagnosis: a failure means the derive-failure audit insert is not fenced
// on the source generation inside its transaction: a stale generation
// inserts a row, or an insert that raced a generation bump committed a
// failed row at the stale generation instead of waiting on the shared lock
// and seeing the new one.
func TestSessionStoreCreateIfGeneration_spec_7_1(t *testing.T) {
	t.Parallel()
	store, pg := startStore(t)
	ctx := context.Background()
	tenant := freshTenant(t, ctx, pg)
	src := newUUID(t)
	if err := store.Create(ctx, sessionstore.Session{
		ID: src, TenantID: tenant, UserID: "alice", RuntimeRef: "echo", State: session.StateRunning, CoordinationGeneration: 3,
	}); err != nil {
		t.Fatalf("seed source: %v", err)
	}
	failedRow := func() sessionstore.Session {
		row := sessionstore.Session{ID: newUUID(t), TenantID: tenant, UserID: "alice", RuntimeRef: "echo", State: session.StateFailed, ParentSessionID: src}
		sessionstore.MarkTerminated(&row, "", time.Now())
		return row
	}

	stale := failedRow()
	if err := store.CreateIfGeneration(ctx, stale, tenant, src, 2); !errors.Is(err, sessionstore.ErrGenerationAdvanced) {
		t.Fatalf("stale generation = %v, want ErrGenerationAdvanced", err)
	}
	if _, err := store.Get(ctx, tenant, stale.ID); !errors.Is(err, sessionstore.ErrNotFound) {
		t.Errorf("a fenced insert wrote a row: %v", err)
	}
	if err := store.CreateIfGeneration(ctx, failedRow(), tenant, newUUID(t), 3); !errors.Is(err, sessionstore.ErrNotFound) {
		t.Errorf("missing source = %v, want ErrNotFound", err)
	}
	ok := failedRow()
	if err := store.CreateIfGeneration(ctx, ok, tenant, src, 3); err != nil {
		t.Fatalf("matching generation = %v", err)
	}

	// A bump holding the source row lock: the insert waits on FOR SHARE and
	// then sees the new generation.
	release := make(chan struct{})
	locked := make(chan struct{})
	bumped := make(chan error, 1)
	go func() {
		bumped <- pgtenant.InTx(ctx, pg.Pool, tenant, func(tx pgx.Tx) error {
			if _, err := tx.Exec(ctx, `UPDATE sessions SET coordination_generation = coordination_generation + 1 WHERE id = $1::uuid`, src); err != nil {
				close(locked)
				return err
			}
			close(locked)
			<-release
			return nil
		})
	}()
	<-locked
	racing := failedRow()
	inserted := make(chan error, 1)
	go func() { inserted <- store.CreateIfGeneration(ctx, racing, tenant, src, 3) }()
	awaitSessionRowLockWaiter(t, ctx, pg, inserted)
	close(release)
	if err := <-bumped; err != nil {
		t.Fatalf("bump: %v", err)
	}
	if err := awaitUpdateResult(t, "racing insert", inserted); !errors.Is(err, sessionstore.ErrGenerationAdvanced) {
		t.Fatalf("insert racing a bump = %v, want ErrGenerationAdvanced", err)
	}
	if _, err := store.Get(ctx, tenant, racing.ID); !errors.Is(err, sessionstore.ErrNotFound) {
		t.Errorf("the racing insert left a failed row at the stale generation: %v", err)
	}
}
