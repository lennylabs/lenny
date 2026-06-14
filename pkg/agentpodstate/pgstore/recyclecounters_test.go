// SPDX-License-Identifier: MIT

package pgstore_test

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lennylabs/lenny/pkg/agentpodstate"
	"github.com/lennylabs/lenny/pkg/agentpodstate/pgstore"
	embpostgres "github.com/lennylabs/lenny/pkg/embedded/postgres"
)

// recycleSchema is the §12.6 agent_pod_state table sliced to what the
// recycle-counter accessors touch: the base table from migration 0001 plus
// the nullable sessions_served / scrub_failure_count columns added by
// migration 0167. The accessors do not depend on runtime_definitions or
// sandbox_warm_pools (which 0167 also alters), so the test applies only
// this slice rather than the full migration chain. spec: §12.6.
const recycleSchema = `
CREATE TABLE agent_pod_state (
    pod_id            TEXT        PRIMARY KEY,
    pool_id           TEXT        NOT NULL,
    state             TEXT        NOT NULL,
    tenant_id         TEXT,
    session_id        TEXT,
    isolation_profile TEXT        NOT NULL,
    execution_mode    TEXT        NOT NULL,
    resource_version  BIGINT      NOT NULL,
    node_name         TEXT,
    sessions_served     INTEGER,
    scrub_failure_count INTEGER,
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now()
);`

// setupRecycle brings up an embedded Postgres, applies the agent_pod_state
// recycle-counter schema slice, and returns a connected pool + store. It
// downloads the PostgreSQL bundle, so it is skipped under -short.
//
// diagnosis: a failure here means the agent_pod_state recycle-counter
// schema or the embedded Postgres harness is broken, not the accessor
// logic under test.
func setupRecycle(t *testing.T) (*pgstore.Store, *pgxpool.Pool, context.Context) {
	t.Helper()
	if testing.Short() {
		t.Skip("downloads the PostgreSQL bundle; skipped under -short")
	}
	pg := embpostgres.New(embpostgres.Config{
		DataDir:      t.TempDir(),
		Port:         15545,
		Database:     "lenny",
		Username:     "lenny",
		Password:     "lenny",
		StartTimeout: 3 * time.Minute,
	})
	if err := pg.Start(); err != nil {
		t.Fatalf("embedded postgres Start: %v", err)
	}
	t.Cleanup(func() { _ = pg.Stop() })

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	pool, err := pgxpool.New(ctx, pg.DSN())
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)

	if _, err := pool.Exec(ctx, recycleSchema); err != nil {
		t.Fatalf("apply recycle schema: %v", err)
	}
	return pgstore.New(pool), pool, ctx
}

// seedPod inserts one mirror row so the recycle-counter accessors have a
// target. Both counters start NULL, matching a WarmPoolController-mirrored
// row before the gateway has written either.
func seedPod(t *testing.T, pool *pgxpool.Pool, ctx context.Context, podID string) {
	t.Helper()
	_, err := pool.Exec(ctx,
		`INSERT INTO agent_pod_state
		 (pod_id, pool_id, state, isolation_profile, execution_mode, resource_version)
		 VALUES ($1, 'pool-1', 'claimed', 'standard', 'session', 1)`, podID)
	if err != nil {
		t.Fatalf("seed pod %s: %v", podID, err)
	}
}

// TestPgIncrementSessionsServedFromNull pins the §4.7 ReportSessionScrub
// counter write against a real Postgres: a NULL sessions_served is
// COALESCEd to 0, so the first increment persists and returns 1.
//
// diagnosis: a failure means the sessions_served increment UPDATE or its
// NULL handling is wrong; the gateway would miscount sessions against
// recycle.maxSessionsPerPod.
// spec: §4.7 (ReportSessionScrub increments sessionsServed), §5.2, §12.6.
func TestPgIncrementSessionsServedFromNull_spec_4_7(t *testing.T) {
	s, pool, ctx := setupRecycle(t)
	seedPod(t, pool, ctx, "pod-a")

	for want := 1; want <= 3; want++ {
		got, ok, err := s.IncrementSessionsServed(ctx, "pod-a")
		if err != nil || !ok {
			t.Fatalf("IncrementSessionsServed: (%d, %v, %v)", got, ok, err)
		}
		if got != want {
			t.Fatalf("IncrementSessionsServed = %d, want %d", got, want)
		}
	}
}

// TestPgIncrementScrubFailureCountFromNull pins the §4.7 ReportPodScrub
// counter write on a failed whole-pod scrub.
//
// diagnosis: a failure means the scrub_failure_count increment is wrong;
// the gateway would miscount against recycle.maxScrubFailures and either
// retire pods early or never.
// spec: §4.7 (ReportPodScrub increments scrubFailureCount), §5.2, §12.6.
func TestPgIncrementScrubFailureCountFromNull_spec_4_7(t *testing.T) {
	s, pool, ctx := setupRecycle(t)
	seedPod(t, pool, ctx, "pod-a")

	got, ok, err := s.IncrementScrubFailureCount(ctx, "pod-a")
	if err != nil || !ok || got != 1 {
		t.Fatalf("first IncrementScrubFailureCount = (%d, %v, %v), want (1, true, nil)", got, ok, err)
	}
	got, _, _ = s.IncrementScrubFailureCount(ctx, "pod-a")
	if got != 2 {
		t.Fatalf("second IncrementScrubFailureCount = %d, want 2", got)
	}
}

// TestPgRecycleCountersReadBack confirms RecycleCounters reads both
// gateway-written counters back for the §5.2 disposition, with each
// counter independent.
//
// diagnosis: a failure means the recycle disposition read is wrong; the
// gateway would evaluate stale or crossed counters against the recycle
// bounds.
// spec: §12.6 (agent_pod_state columns), §5.2 (recycle disposition).
func TestPgRecycleCountersReadBack_spec_12_6(t *testing.T) {
	s, pool, ctx := setupRecycle(t)
	seedPod(t, pool, ctx, "pod-a")

	_, _, _ = s.IncrementSessionsServed(ctx, "pod-a")
	_, _, _ = s.IncrementSessionsServed(ctx, "pod-a")
	_, _, _ = s.IncrementScrubFailureCount(ctx, "pod-a")

	rc, ok, err := s.RecycleCounters(ctx, "pod-a")
	if err != nil || !ok {
		t.Fatalf("RecycleCounters: (%+v, %v, %v)", rc, ok, err)
	}
	if rc.SessionsServed != 2 || rc.ScrubFailureCount != 1 {
		t.Fatalf("RecycleCounters = %+v, want {SessionsServed:2 ScrubFailureCount:1}", rc)
	}
}

// TestPgRecycleCountersNullReadsAsZero pins the §12.6 NULL-as-0 read: a
// never-written counter reads back as 0 rather than a SQL NULL the Scan
// would reject.
//
// diagnosis: a failure means the COALESCE on the read path is missing; a
// fresh pod's disposition would error on a NULL scan.
// spec: §12.6 (sessions_served / scrub_failure_count nullable), §5.2.
func TestPgRecycleCountersNullReadsAsZero_spec_12_6(t *testing.T) {
	s, pool, ctx := setupRecycle(t)
	seedPod(t, pool, ctx, "pod-a")

	rc, ok, err := s.RecycleCounters(ctx, "pod-a")
	if err != nil || !ok {
		t.Fatalf("RecycleCounters: (%+v, %v, %v)", rc, ok, err)
	}
	if rc != (agentpodstate.RecycleCounters{}) {
		t.Fatalf("RecycleCounters = %+v, want zeroes", rc)
	}
}

// TestPgRecycleCounterMissingPod fails closed: an increment or read against
// an unknown pod reports not-found and writes nothing, so the gateway does
// not fabricate a counter for a pod with no mirror row.
//
// diagnosis: a failure means the RETURNING UPDATE matched no row but the
// accessor reported success, masking a missing-pod bug.
// spec: §4.7 (counter writes target the pod's agent_pod_state row).
func TestPgRecycleCounterMissingPod_spec_4_7(t *testing.T) {
	s, _, ctx := setupRecycle(t)

	if got, ok, err := s.IncrementSessionsServed(ctx, "ghost"); ok || err != nil || got != 0 {
		t.Fatalf("IncrementSessionsServed(ghost) = (%d, %v, %v), want (0, false, nil)", got, ok, err)
	}
	if got, ok, err := s.IncrementScrubFailureCount(ctx, "ghost"); ok || err != nil || got != 0 {
		t.Fatalf("IncrementScrubFailureCount(ghost) = (%d, %v, %v), want (0, false, nil)", got, ok, err)
	}
	if rc, ok, err := s.RecycleCounters(ctx, "ghost"); ok || err != nil || rc != (agentpodstate.RecycleCounters{}) {
		t.Fatalf("RecycleCounters(ghost) = (%+v, %v, %v), want (zero, false, nil)", rc, ok, err)
	}
}

// TestPgCounterWriteAdvancesUpdatedAt confirms a counter write re-stamps
// updated_at to now(), so the §10.1 mirror-staleness gauge reflects
// gateway counter writes. The test reads updated_at directly because the
// gauge derives from it.
//
// diagnosis: a failure means the increment UPDATE omitted updated_at =
// now(); the mirror-lag gauge would treat a freshly-written pod as stale.
// spec: §10.1 (mirror lag), §12.6 (updated_at).
func TestPgCounterWriteAdvancesUpdatedAt_spec_10_1(t *testing.T) {
	s, pool, ctx := setupRecycle(t)
	seedPod(t, pool, ctx, "pod-a")
	// Backdate updated_at so a write that stamps now() is observable.
	if _, err := pool.Exec(ctx,
		`UPDATE agent_pod_state SET updated_at = now() - interval '1 hour' WHERE pod_id = 'pod-a'`); err != nil {
		t.Fatalf("backdate: %v", err)
	}

	if _, _, err := s.IncrementSessionsServed(ctx, "pod-a"); err != nil {
		t.Fatalf("IncrementSessionsServed: %v", err)
	}

	var lagSeconds float64
	if err := pool.QueryRow(ctx,
		`SELECT EXTRACT(EPOCH FROM now() - updated_at) FROM agent_pod_state WHERE pod_id = 'pod-a'`).
		Scan(&lagSeconds); err != nil {
		t.Fatalf("read updated_at: %v", err)
	}
	if lagSeconds > 60 {
		t.Fatalf("updated_at not advanced on counter write: lag %.0fs still reflects the backdate", lagSeconds)
	}
}

// TestPgConcurrentIncrementsNoLostUpdate exercises the atomic RETURNING
// UPDATE under concurrency: N goroutines each increment sessions_served
// once, and the final value is exactly N. The single UPDATE statement is
// atomic per row, so no increment is lost.
//
// diagnosis: a failure (final count < N) means the increment is not atomic
// — a read-modify-write race would drop concurrent ReportSessionScrub
// increments on a maxConcurrentSessions > 1 pod releasing slots in
// parallel.
// spec: §4.7 (concurrent counter increments), §5.2.
func TestPgConcurrentIncrementsNoLostUpdate_spec_4_7(t *testing.T) {
	s, pool, ctx := setupRecycle(t)
	seedPod(t, pool, ctx, "pod-a")

	const n = 32
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		go func() {
			_, _, err := s.IncrementSessionsServed(ctx, "pod-a")
			errs <- err
		}()
	}
	for i := 0; i < n; i++ {
		if err := <-errs; err != nil {
			t.Fatalf("concurrent IncrementSessionsServed: %v", err)
		}
	}

	rc, _, err := s.RecycleCounters(ctx, "pod-a")
	if err != nil {
		t.Fatalf("RecycleCounters: %v", err)
	}
	if rc.SessionsServed != n {
		t.Fatalf("SessionsServed = %d, want %d (lost update under concurrency)", rc.SessionsServed, n)
	}
}
