// SPDX-License-Identifier: MIT

package podregistry

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"k8s.io/apimachinery/pkg/util/uuid"
)

// PostgresPodRegistry is the §12.6 line 436 Tier-4 PodRegistry
// implementation: it reads and writes the agent_pod_state Postgres table
// directly instead of Sandbox CRD status, eliminating the etcd write
// pressure the CRD-backed registry incurs at scale. It sits over the
// same agent_pod_state table the WarmPoolController mirrors in v1 (see
// pkg/agentpodstate); at Tier 4 that table is the primary store and this
// adapter is the read/write surface the §4.6.1 PodLifecycleManager and
// PoolManager delegate to.
//
// v1 deployments wire the CRDPodRegistry; this implementation exists so
// the §12.6 "the swap is a configuration-only change at the store-router
// level — no callers change" guarantee holds structurally rather than
// requiring a from-scratch build at Tier 4. The interface is satisfied
// in full; the optimistic-locking CAS uses the agent_pod_state
// resource_version column rather than a CRD generation.
//
// agent_pod_state is platform-global (§12.6): the table carries no RLS
// and tenant_id is a denormalized convenience column, so operations run
// as plain queries without an app.current_tenant context.
//
// The agent_pod_state schema (§12.6) does not carry the runtime, the
// workspace plan, or the resource class, so CreatePod records only the
// columns the table has. A Tier-4 deployment that needs those persisted
// extends the schema; the §12.6 v1 table is a mirror of Sandbox status,
// which carries them on the CRD spec, not on the mirror.
type PostgresPodRegistry struct {
	pool    *pgxpool.Pool
	metrics *Metrics

	// pollInterval is the WatchPods polling cadence. The §12.6 line 484
	// LISTEN/NOTIFY trigger (migration 0108) is the lower-latency path a
	// future build consumes; the v1 watch falls back to polling, per the
	// spec's PgBouncer-transaction-mode caveat. Zero falls back to
	// watchTickerInterval.
	pollInterval time.Duration
	// watchBufferSize bounds each WatchPods event channel. Zero falls
	// back to defaultWatchBufferSize.
	watchBufferSize int
	// now sources the watch-lag clock; nil selects time.Now. Tests
	// override it for deterministic lag samples.
	now func() time.Time
}

var _ PodRegistry = (*PostgresPodRegistry)(nil)

// NewPostgres returns a PostgresPodRegistry over pool. The pool must
// point at a database with the migrations/ schema (including the 0108
// notify trigger) applied.
func NewPostgres(pool *pgxpool.Pool) (*PostgresPodRegistry, error) {
	if pool == nil {
		return nil, errors.New("podregistry: nil pool")
	}
	return &PostgresPodRegistry{
		pool:            pool,
		pollInterval:    watchTickerInterval,
		watchBufferSize: defaultWatchBufferSize,
		now:             time.Now,
	}, nil
}

// SetMetrics attaches the §12.6 line 478 / line 484 observability
// metrics. A nil Metrics (the default) disables emission.
func (r *PostgresPodRegistry) SetMetrics(m *Metrics) { r.metrics = m }

// SetWatchTuningForTest overrides the poll cadence and buffer depth so
// the watch tests do not wait the production interval.
func (r *PostgresPodRegistry) SetWatchTuningForTest(interval time.Duration, buffer int) {
	r.pollInterval = interval
	r.watchBufferSize = buffer
}

// SetNowForTest overrides the watch-lag clock.
func (r *PostgresPodRegistry) SetNowForTest(now func() time.Time) { r.now = now }

func (r *PostgresPodRegistry) recordOp(operation string, pool PoolID, start time.Time, err error) {
	if r.metrics == nil {
		return
	}
	r.metrics.observe(operation, string(pool), time.Since(start).Seconds())
	if err != nil {
		r.metrics.incError(operation, string(pool))
	}
}

const selectColumns = `pod_id, pool_id, state, tenant_id, session_id,
	isolation_profile, execution_mode, resource_version, node_name`

// scanRecord scans one agent_pod_state row into a PodRecord. The
// nullable tenant_id, session_id, and node_name columns become empty
// strings; resource_version (BIGINT) is formatted as the decimal string
// the §4.6.1 CAS loop carries forward.
func scanRecord(row pgx.Row) (PodRecord, error) {
	var (
		rec     PodRecord
		tenant  *string
		session *string
		node    *string
		rv      int64
		podID   string
		poolID  string
	)
	if err := row.Scan(&podID, &poolID, &rec.State, &tenant, &session,
		&rec.IsolationProfile, &rec.ExecutionMode, &rv, &node); err != nil {
		return PodRecord{}, err
	}
	rec.PodID = PodID(podID)
	rec.PoolID = PoolID(poolID)
	rec.ResourceVersion = strconv.FormatInt(rv, 10)
	if tenant != nil {
		rec.TenantID = *tenant
	}
	if session != nil {
		rec.SessionID = *session
	}
	if node != nil {
		rec.NodeName = *node
	}
	return rec, nil
}

// GetPod returns the pod's authoritative record. spec: §12.6 — ErrNotFound
// when no row exists.
func (r *PostgresPodRegistry) GetPod(ctx context.Context, podID PodID) (rec *PodRecord, err error) {
	start := time.Now()
	var pool PoolID
	defer func() { r.recordOp(opGet, pool, start, err) }()
	out, err := scanRecord(r.pool.QueryRow(ctx,
		`SELECT `+selectColumns+` FROM agent_pod_state WHERE pod_id = $1`, string(podID)))
	if errors.Is(err, pgx.ErrNoRows) {
		err = ErrNotFound
		return nil, err
	}
	if err != nil {
		err = fmt.Errorf("podregistry: get %s: %w", podID, err)
		return nil, err
	}
	pool = out.PoolID
	return &out, nil
}

// UpdatePodState writes a §6.2 state transition under the §12.6 line 476
// optimistic CAS: UPDATE ... SET state, resource_version = resource_version
// + 1 WHERE pod_id AND resource_version = expected. A transition whose
// From does not match the current state returns ErrInvalidTransition; a
// concurrent write that already bumped resource_version returns
// ErrResourceConflict so the §4.6.1 caller refreshes and retries.
func (r *PostgresPodRegistry) UpdatePodState(ctx context.Context, podID PodID, transition StateTransition) (err error) {
	start := time.Now()
	var pool PoolID
	defer func() { r.recordOp(opUpdateState, pool, start, err) }()

	var (
		curState string
		curRV    int64
		poolID   string
	)
	err = r.pool.QueryRow(ctx,
		`SELECT pool_id, state, resource_version FROM agent_pod_state WHERE pod_id = $1`,
		string(podID)).Scan(&poolID, &curState, &curRV)
	if errors.Is(err, pgx.ErrNoRows) {
		err = ErrNotFound
		return err
	}
	if err != nil {
		err = fmt.Errorf("podregistry: read for update %s: %w", podID, err)
		return err
	}
	pool = PoolID(poolID)
	if transition.From != "" && curState != transition.From {
		err = fmt.Errorf("%w: current state %q, transition.From %q",
			ErrInvalidTransition, curState, transition.From)
		return err
	}
	tag, uerr := r.pool.Exec(ctx,
		`UPDATE agent_pod_state
		 SET state = $2, resource_version = resource_version + 1, updated_at = now()
		 WHERE pod_id = $1 AND resource_version = $3`,
		string(podID), transition.To, curRV)
	if uerr != nil {
		err = fmt.Errorf("podregistry: update state %s: %w", podID, uerr)
		return err
	}
	if tag.RowsAffected() == 0 {
		// The row either vanished or had its resource_version bumped by a
		// concurrent writer between the read and the CAS.
		err = ErrResourceConflict
		return err
	}
	return nil
}

// ClaimPod runs the §4.6.1 SELECT ... FOR UPDATE SKIP LOCKED claim: it
// locks the pool's oldest idle row, transitions it to claimed, and pins
// the session and tenant. ErrPoolExhausted reports no idle pod was
// available. SKIP LOCKED makes concurrent claims select distinct pods.
func (r *PostgresPodRegistry) ClaimPod(ctx context.Context, opts ClaimOpts) (rec *PodRecord, err error) {
	start := time.Now()
	defer func() { r.recordOp(opClaim, opts.PoolID, start, err) }()
	if opts.PoolID == "" {
		err = errors.New("podregistry: ClaimOpts.PoolID is required")
		return nil, err
	}
	if opts.SessionID == "" {
		err = errors.New("podregistry: ClaimOpts.SessionID is required")
		return nil, err
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		err = fmt.Errorf("podregistry: claim begin: %w", err)
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	out, serr := scanRecord(tx.QueryRow(ctx,
		`SELECT `+selectColumns+` FROM agent_pod_state
		 WHERE pool_id = $1 AND state = 'idle'
		 ORDER BY updated_at
		 LIMIT 1 FOR UPDATE SKIP LOCKED`, string(opts.PoolID)))
	if errors.Is(serr, pgx.ErrNoRows) {
		err = ErrPoolExhausted
		return nil, err
	}
	if serr != nil {
		err = fmt.Errorf("podregistry: claim select: %w", serr)
		return nil, err
	}
	if _, uerr := tx.Exec(ctx,
		`UPDATE agent_pod_state
		 SET state = 'claimed', session_id = $2, tenant_id = $3,
		     resource_version = resource_version + 1, updated_at = now()
		 WHERE pod_id = $1`,
		string(out.PodID), opts.SessionID, nullableID(opts.TenantID)); uerr != nil {
		err = fmt.Errorf("podregistry: claim update: %w", uerr)
		return nil, err
	}
	if cerr := tx.Commit(ctx); cerr != nil {
		err = fmt.Errorf("podregistry: claim commit: %w", cerr)
		return nil, err
	}
	out.State = "claimed"
	out.SessionID = opts.SessionID
	out.TenantID = opts.TenantID
	return &out, nil
}

// ReleasePod transitions a pod out of an active phase per the release
// reason and clears its session and tenant binding (§12.6 line 442), so
// the released pod no longer reports as bound to the orphan-session
// reconciler. ErrNotFound when no row exists.
func (r *PostgresPodRegistry) ReleasePod(ctx context.Context, podID PodID, reason ReleaseReason) (err error) {
	start := time.Now()
	var pool PoolID
	defer func() { r.recordOp(opRelease, pool, start, err) }()

	phase := "task_cleanup"
	switch reason {
	case ReleaseFailed:
		phase = "failed"
	case ReleaseCancelled:
		phase = "cancelled"
	}
	tag, uerr := r.pool.Exec(ctx,
		`UPDATE agent_pod_state
		 SET state = $2, session_id = NULL, tenant_id = NULL,
		     resource_version = resource_version + 1, updated_at = now()
		 WHERE pod_id = $1`,
		string(podID), phase)
	if uerr != nil {
		err = fmt.Errorf("podregistry: release %s: %w", podID, uerr)
		return err
	}
	if tag.RowsAffected() == 0 {
		err = ErrNotFound
		return err
	}
	return nil
}

// ListPodsByPool returns every pod in the pool, optionally filtered by
// state, ordered by pod_id so a paginated read is deterministic.
func (r *PostgresPodRegistry) ListPodsByPool(ctx context.Context, poolID PoolID, filter PodFilter) (_ []PodRecord, err error) {
	start := time.Now()
	defer func() { r.recordOp(opList, poolID, start, err) }()
	recs, _, err := r.listRows(ctx, poolID, filter.State)
	return recs, err
}

// listRows is the shared list query. It returns each record alongside
// its updated_at so the watch loop can compute the §12.6 line 484 lag.
func (r *PostgresPodRegistry) listRows(ctx context.Context, poolID PoolID, state string) ([]PodRecord, []time.Time, error) {
	q := `SELECT ` + selectColumns + `, updated_at FROM agent_pod_state WHERE pool_id = $1`
	args := []any{string(poolID)}
	if state != "" {
		q += ` AND state = $2`
		args = append(args, state)
	}
	q += ` ORDER BY pod_id`
	rows, err := r.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, nil, fmt.Errorf("podregistry: list %s: %w", poolID, err)
	}
	defer rows.Close()
	var (
		recs []PodRecord
		ats  []time.Time
	)
	for rows.Next() {
		var (
			rec     PodRecord
			tenant  *string
			session *string
			node    *string
			rv      int64
			podID   string
			poolStr string
			at      time.Time
		)
		if err := rows.Scan(&podID, &poolStr, &rec.State, &tenant, &session,
			&rec.IsolationProfile, &rec.ExecutionMode, &rv, &node, &at); err != nil {
			return nil, nil, fmt.Errorf("podregistry: scan %s: %w", poolID, err)
		}
		rec.PodID = PodID(podID)
		rec.PoolID = PoolID(poolStr)
		rec.ResourceVersion = strconv.FormatInt(rv, 10)
		if tenant != nil {
			rec.TenantID = *tenant
		}
		if session != nil {
			rec.SessionID = *session
		}
		if node != nil {
			rec.NodeName = *node
		}
		recs = append(recs, rec)
		ats = append(ats, at)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("podregistry: list rows %s: %w", poolID, err)
	}
	return recs, ats, nil
}

// CountByState returns the §6.2 pod-state histogram for the pool, used by
// the §4.6.2 PoolScalingController.
func (r *PostgresPodRegistry) CountByState(ctx context.Context, poolID PoolID) (_ StateCounts, err error) {
	start := time.Now()
	defer func() { r.recordOp(opCount, poolID, start, err) }()
	rows, qerr := r.pool.Query(ctx,
		`SELECT state, COUNT(*) FROM agent_pod_state WHERE pool_id = $1 GROUP BY state`,
		string(poolID))
	if qerr != nil {
		err = fmt.Errorf("podregistry: count %s: %w", poolID, qerr)
		return nil, err
	}
	defer rows.Close()
	counts := StateCounts{}
	for rows.Next() {
		var state string
		var n int
		if serr := rows.Scan(&state, &n); serr != nil {
			err = fmt.Errorf("podregistry: count scan %s: %w", poolID, serr)
			return nil, err
		}
		counts[state] = n
	}
	if rerr := rows.Err(); rerr != nil {
		err = fmt.Errorf("podregistry: count rows %s: %w", poolID, rerr)
		return nil, err
	}
	return counts, nil
}

// CreatePod inserts a new warming pod row. The agent_pod_state schema
// (§12.6) carries pool, isolation profile, and execution mode but not
// the runtime, workspace plan, or resource class, so those PodSpec
// fields are not persisted by the Postgres backend; CreatePod records
// what the table holds. resource_version starts at 1.
func (r *PostgresPodRegistry) CreatePod(ctx context.Context, poolID PoolID, spec PodSpec) (_ *PodRecord, err error) {
	start := time.Now()
	defer func() { r.recordOp(opCreate, poolID, start, err) }()
	if poolID == "" {
		err = errors.New("podregistry: poolID is required")
		return nil, err
	}
	name := fmt.Sprintf("%s-%s", poolID, uuid.NewUUID()[:8])
	if _, ierr := r.pool.Exec(ctx,
		`INSERT INTO agent_pod_state (
			pod_id, pool_id, state, isolation_profile, execution_mode,
			resource_version, updated_at)
		 VALUES ($1, $2, 'warming', $3, $4, 1, now())`,
		name, string(poolID), spec.IsolationProfile, spec.ExecutionMode); ierr != nil {
		err = fmt.Errorf("podregistry: create %s: %w", name, ierr)
		return nil, err
	}
	rec := PodRecord{
		PodID:            PodID(name),
		PoolID:           poolID,
		State:            "warming",
		IsolationProfile: spec.IsolationProfile,
		ExecutionMode:    spec.ExecutionMode,
		ResourceVersion:  "1",
	}
	return &rec, nil
}

// DeletePod removes the pod row. ErrNotFound when no row exists.
func (r *PostgresPodRegistry) DeletePod(ctx context.Context, podID PodID) (err error) {
	start := time.Now()
	defer func() { r.recordOp(opDelete, "", start, err) }()
	tag, derr := r.pool.Exec(ctx, `DELETE FROM agent_pod_state WHERE pod_id = $1`, string(podID))
	if derr != nil {
		err = fmt.Errorf("podregistry: delete %s: %w", podID, derr)
		return err
	}
	if tag.RowsAffected() == 0 {
		err = ErrNotFound
		return err
	}
	return nil
}

// WatchPods returns an eventually-consistent change stream for the pool.
// The v1 implementation polls agent_pod_state; the §12.6 line 484
// LISTEN/NOTIFY trigger (migration 0108) is the lower-latency substrate a
// future build consumes. The channel closes when ctx is cancelled.
//
// spec: §12.6 line 482 — no initial-state snapshot (consumers seed via
// ListPodsByPool); a backed-up channel yields a resync frame instead of
// blocking. spec: §12.6 line 484 — every delivered event records the
// updated_at → delivery lag on lenny_pod_registry_watch_lag_seconds
// labeled implementation="postgres".
func (r *PostgresPodRegistry) WatchPods(ctx context.Context, poolID PoolID) (_ <-chan PodEvent, err error) {
	start := time.Now()
	defer func() { r.recordOp(opWatch, poolID, start, err) }()
	if poolID == "" {
		err = errors.New("podregistry: poolID is required")
		return nil, err
	}
	bufSize := r.watchBufferSize
	if bufSize <= 0 {
		bufSize = defaultWatchBufferSize
	}
	out := make(chan PodEvent, bufSize)
	go r.watchLoop(ctx, poolID, out)
	return out, nil
}

func (r *PostgresPodRegistry) nowFn() time.Time {
	if r.now != nil {
		return r.now()
	}
	return time.Now()
}

// watchLoop polls listRows and emits PodEvent deltas, recording the
// updated_at → delivery lag per delivered event. It mirrors the CRD
// watch semantics (no initial snapshot, resync on backpressure).
func (r *PostgresPodRegistry) watchLoop(ctx context.Context, poolID PoolID, out chan<- PodEvent) {
	defer close(out)
	known := map[PodID]PodRecord{}
	if recs, _, err := r.listRows(ctx, poolID, ""); err == nil {
		for i := range recs {
			known[recs[i].PodID] = recs[i]
		}
	}
	interval := r.pollInterval
	if interval <= 0 {
		interval = watchTickerInterval
	}
	ticker := newWatchTicker(interval)
	defer ticker.Stop()
	pendingResync := false
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C():
		}
		recs, ats, err := r.listRows(ctx, poolID, "")
		if err != nil {
			continue
		}
		if pendingResync {
			syncKnown(known, recs)
			if trySend(out, PodEvent{EventType: EventResync}) {
				pendingResync = false
			}
			continue
		}
		seen := make(map[PodID]bool, len(recs))
		for i := range recs {
			rec := recs[i]
			seen[rec.PodID] = true
			prev, ok := known[rec.PodID]
			known[rec.PodID] = rec
			var ev PodEvent
			switch {
			case !ok:
				ev = PodEvent{PodID: rec.PodID, EventType: EventCreated, PodRecord: rec}
			case prev.State != rec.State || prev.ResourceVersion != rec.ResourceVersion:
				ev = PodEvent{PodID: rec.PodID, EventType: EventUpdated, PodRecord: rec}
			default:
				continue
			}
			if !trySend(out, ev) {
				pendingResync = true
				continue
			}
			if r.metrics != nil {
				r.metrics.observeWatchLag(string(poolID), implPostgres, r.nowFn().Sub(ats[i]).Seconds())
			}
		}
		for podID, rec := range known {
			if !seen[podID] {
				delete(known, podID)
				if !trySend(out, PodEvent{PodID: podID, EventType: EventDeleted, PodRecord: rec}) {
					pendingResync = true
				}
			}
		}
	}
}

// nullableID maps an empty id to a SQL NULL so the nullable tenant_id
// column stores NULL rather than an empty string, keeping the partial
// index idx_agent_pod_state_tenant accurate.
func nullableID(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
