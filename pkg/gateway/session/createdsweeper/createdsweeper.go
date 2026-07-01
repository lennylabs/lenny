// SPDX-License-Identifier: MIT

// Package createdsweeper drops abandoned `created`-state Session rows
// whose §7.1 `maxCreatedStateTimeoutSeconds` deadline (default 300s)
// has elapsed without a /upload or /finalize landing. The §7.1 line
// 58 uploadToken TTL closes the upload window at that instant; the
// row itself stayed in `created` forever in the v1 implementation,
// so abandoned-create rows accumulated under repeated client retries
// and bloated the storage quota.
//
// The sweeper runs as a background goroutine alongside the §7.1
// artifact-retention GC (pkg/gateway/retentiongc) and the §11.3
// watchdog, on a deployer-configurable cadence (default 1 min). On
// each tick it lists every tenant's `created`-state rows, filters by
// CreatedAt + timeout, releases the warm pod each row claimed at /create
// back to the pool and revokes any assigned credential lease, then removes
// the rows. Per §15.1 line 630 a `created`-expiry release must return the
// claimed pod to the pool and revoke the lease before the row is retired,
// so an abandoned create does not strand a pod for the whole pool. The
// release runs through the injected Reclaimer, the same claimless reclaim
// /terminate uses (proposal §4.5).
//
// spec: §7.1 line 58 (`maxCreatedStateTimeoutSeconds`); §7.1 line 28
// (atomicity: the row must not survive a session that never finalized);
// §15.1 line 630 (created TTL-expiry releases the pod claim and revokes the
// lease).
package createdsweeper

import (
	"context"
	"fmt"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
)

// DefaultSweepInterval is how often the sweep runs when no interval
// is configured. Shorter than the retention GC interval (15 min)
// because the §7.1 created-state timeout itself is 300s and a
// once-per-minute sweep keeps the orphan-row backlog bounded.
const DefaultSweepInterval = 1 * time.Minute

// DefaultCreatedStateTimeout is the §7.1 `maxCreatedStateTimeoutSeconds`
// default: 300s. The sweeper drops rows whose CreatedAt is older than
// this. Operators tune it via the gateway's
// `--max-created-state-timeout` flag.
const DefaultCreatedStateTimeout = 5 * time.Minute

// MetricsSink reports the per-sweep outcome counts. Production wires
// it to a gatewaymetrics adapter; the dev/test gateway leaves it nil
// and the sweep keeps working.
type MetricsSink interface {
	// IncSweep records one Tick outcome ("success", "error").
	IncSweep(outcome string)
	// AddCollected records how many created-state rows the sweep dropped.
	AddCollected(n int)
}

// TenantLister enumerates the tenants the sweep covers. Mirrors the
// retentiongc.TenantLister contract so the gateway wires both
// sweepers to the same tenants table.
type TenantLister interface {
	ListTenants(ctx context.Context) ([]string, error)
}

// Reclaimer releases the resources an abandoned `created`-state row still
// holds before the row is retired: it deletes the per-pod SandboxClaim
// (returning the claimed pod to the pool) and revokes any §4.9 credential
// lease the session holds, keyed by sessionID. The gateway wires it to the
// podsession Binder's ReclaimClaimed, the same claimless reclaim /terminate
// runs, so the two paths release a pre-running session's pod and lease
// identically.
//
// poolRef records the pool the pod was claimed from; the underlying reclaim
// deletes the claim by pod name and does not need it, but it is carried so
// the dependency mirrors the §4.6 persisted pod↔session binding
// (PodAssignment + PoolRef) and so a future pool-scoped release can use it
// without changing this contract.
//
// spec: §15.1 line 630 (created TTL-expiry releases pod and lease); §4.5
// (proposal); §7.1 step 23 (lease release).
type Reclaimer func(ctx context.Context, podName, poolRef, sessionID string) error

// StaticTenants is a fixed-slice TenantLister for tests and the
// in-memory gateway.
type StaticTenants []string

// ListTenants implements TenantLister.
func (s StaticTenants) ListTenants(_ context.Context) ([]string, error) { return s, nil }

// Sweeper drops abandoned `created`-state rows.
type Sweeper struct {
	store    sessionstore.Store
	tenants  TenantLister
	timeout  time.Duration
	interval time.Duration
	clock    func() time.Time
	metrics  MetricsSink
	reclaim  Reclaimer
}

// Options configures a Sweeper. A zero field selects its default.
type Options struct {
	// Timeout overrides DefaultCreatedStateTimeout.
	Timeout time.Duration
	// Interval overrides DefaultSweepInterval.
	Interval time.Duration
	// Clock overrides time.Now.
	Clock func() time.Time
	// Metrics, when set, receives the per-sweep outcome counts.
	Metrics MetricsSink
	// Reclaim, when set, releases the claimed pod and revokes the lease an
	// abandoned `created`-state row holds before the row is deleted. The
	// in-memory dev/test gateway and the row-only unit tests leave it nil and
	// the sweep skips the release, dropping the row as before. spec: §15.1
	// line 630 (proposal §4.5).
	Reclaim Reclaimer
}

// New returns a Sweeper.
func New(store sessionstore.Store, tenants TenantLister, opts Options) *Sweeper {
	s := &Sweeper{
		store:    store,
		tenants:  tenants,
		timeout:  opts.Timeout,
		interval: opts.Interval,
		clock:    opts.Clock,
		metrics:  opts.Metrics,
		reclaim:  opts.Reclaim,
	}
	if s.timeout <= 0 {
		s.timeout = DefaultCreatedStateTimeout
	}
	if s.interval <= 0 {
		s.interval = DefaultSweepInterval
	}
	if s.clock == nil {
		s.clock = func() time.Time { return time.Now().UTC() }
	}
	return s
}

// Tick runs one sweep at now and returns the count of abandoned
// `created`-state rows the sweep removed across every tenant.
func (s *Sweeper) Tick(ctx context.Context, now time.Time) (int, error) {
	tenants, err := s.tenants.ListTenants(ctx)
	if err != nil {
		if s.metrics != nil {
			s.metrics.IncSweep("error")
		}
		return 0, err
	}
	dropped := 0
	for _, tenant := range tenants {
		n, err := s.sweepTenant(ctx, tenant, now)
		dropped += n
		if err != nil {
			if s.metrics != nil {
				s.metrics.IncSweep("error")
			}
			return dropped, err
		}
	}
	if s.metrics != nil {
		s.metrics.IncSweep("success")
		if dropped > 0 {
			s.metrics.AddCollected(dropped)
		}
	}
	return dropped, nil
}

// sweepTenant lists the tenant's rows, filters the abandoned
// `created`-state ones, releases the pod and lease each still holds, and
// removes each. The release runs before the row Delete so a delete failure
// does not leave a row whose pod was already released; on a per-row reclaim
// or delete error the sweep aborts so an operator sees the failure on the
// next tick (preserving the abort-on-error semantics). A `created` session
// that never finalized holds a pod claimed at /create but no lease (the lease
// is assigned at finalize, §4.3), so the reclaim's lease revoke is a defensive
// no-op there; the same dependency serves /terminate, which can run against a
// `finalizing`/`ready` session that does hold a lease.
//
// spec: §15.1 line 630 (created TTL-expiry releases the pod claim and revokes
// the lease); §7.1 line 28 (atomicity: the row must not survive a session that
// never finalized).
func (s *Sweeper) sweepTenant(ctx context.Context, tenant string, now time.Time) (int, error) {
	rows, err := s.store.List(ctx, tenant, sessionstore.ListFilter{})
	if err != nil {
		return 0, err
	}
	dropped := 0
	for _, row := range rows {
		if !eligible(row, s.timeout, now) {
			continue
		}
		if err := s.reclaimRow(ctx, row); err != nil {
			return dropped, err
		}
		if err := s.store.Delete(ctx, tenant, row.ID); err != nil {
			return dropped, fmt.Errorf("createdsweeper: delete %s/%s: %w", tenant, row.ID, err)
		}
		dropped++
	}
	return dropped, nil
}

// reclaimRow releases the claimed pod and revokes any assigned lease an
// abandoned `created`-state row holds, ahead of the row Delete. It is a no-op
// when no Reclaimer is wired (the in-memory dev/test gateway) or when the row
// carries no persisted pod binding (PodAssignment empty), so the sweep degrades
// to a plain row drop rather than failing. spec: §15.1 line 630 (proposal §4.5).
func (s *Sweeper) reclaimRow(ctx context.Context, row sessionstore.Session) error {
	if s.reclaim == nil || row.PodAssignment == "" {
		return nil
	}
	if err := s.reclaim(ctx, row.PodAssignment, row.PoolRef, row.ID); err != nil {
		return fmt.Errorf("createdsweeper: reclaim %s/%s pod %s: %w", row.TenantID, row.ID, row.PodAssignment, err)
	}
	return nil
}

// eligible reports whether row is an abandoned `created`-state row.
// The session is in `created` state (the upload window) and its
// CreatedAt is older than the §7.1 timeout. A non-zero CreatedAt is
// required so a row written with a missing timestamp never gets
// swept by accident.
func eligible(row sessionstore.Session, timeout time.Duration, now time.Time) bool {
	if row.State != session.StateCreated {
		return false
	}
	if row.CreatedAt.IsZero() {
		return false
	}
	return now.Sub(row.CreatedAt) > timeout
}

// Run drives the sweep on the configured interval until ctx is done.
// onTick, when non-nil, receives each sweep's dropped-count and error.
func (s *Sweeper) Run(ctx context.Context, onTick func(int, error)) {
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			n, err := s.Tick(ctx, s.clock())
			if onTick != nil {
				onTick(n, err)
			}
		}
	}
}
