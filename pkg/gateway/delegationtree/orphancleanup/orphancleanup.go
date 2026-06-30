// SPDX-License-Identifier: MIT

// Package orphancleanup implements the §8.10 orphan-cleanup job. A
// periodic sweep terminates delegation-tree children left running
// after their root session reached a terminal state and the
// cascadeTimeoutSeconds window elapsed.
//
// A child survives a parent termination only under the `detach` and
// `await_completion` cascade policies (the `cancel_all` default
// cancels descendants immediately, see §8.10 cascadeOnFailure). This
// job bounds how long such orphans persist: once a node's tree root
// has been terminal for longer than cascadeTimeoutSeconds, the orphan
// is expired and archived.
package orphancleanup

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/delegationtree/treearchive"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
)

// §8.10 defaults.
const (
	// DefaultCascadeTimeout bounds how long an orphaned child persists
	// after its root session terminates (`delegation.cascadeTimeoutSeconds`).
	DefaultCascadeTimeout = 3600 * time.Second
	// DefaultSweepInterval is how often the cleanup job runs
	// (`delegation.orphanCleanupIntervalSeconds`).
	DefaultSweepInterval = 60 * time.Second
)

// TenantLister enumerates the tenants the sweep covers.
type TenantLister interface {
	ListTenants(ctx context.Context) ([]string, error)
}

// StaticTenants is a fixed-slice TenantLister for tests and the
// minimal gateway.
type StaticTenants []string

// ListTenants implements TenantLister.
func (s StaticTenants) ListTenants(_ context.Context) ([]string, error) { return s, nil }

// TerminalHook is invoked when the orphan-cleanup sweep forces an
// orphaned child session to a terminal state. It supersedes the
// in-package archive emission when wired so terminal side effects —
// workspace seal, executor release (which releases concurrent-mode slots
// per §5.2 line 519 and drains pods per §6.2), §11.7 audit, §7.2 SSE,
// §11.2.1 billing, and §8.10 archive — run once through the same
// Server.recordSessionCompleted path the REST handlers use.
//
// spec: §5.2 line 519 — slot release on session end / expiry; §8.10 —
// orphan cascade.
type TerminalHook interface {
	// fromState is the orphan's pre-terminal state, captured before the sweep
	// forced the row terminal, so the terminal pod-release path can
	// distinguish a pre-running claimed session from a running one (§4.6).
	OnSessionTerminal(ctx context.Context, fromState session.State, sess sessionstore.Session)
}

// MetricsSink receives the §8.10 / §16.1 orphan-cleanup and per-tenant
// orphan-gauge observations a sweep generates. Implementations are
// best-effort and must not block the sweep. spec: §8.10 lines 1091,
// 1093-1101, 1103; §16.1 lines 146-149. F-8.10.7.
type MetricsSink interface {
	// IncOrphanCleanupRun bumps the per-sweep counter; one Inc per Tick.
	IncOrphanCleanupRun()
	// AddOrphanTasksTerminated bumps the cumulative terminated counter by
	// the per-sweep return value.
	AddOrphanTasksTerminated(n int)
	// SetOrphanTasksActive publishes the fleet-wide active orphan count.
	SetOrphanTasksActive(value int)
	// SetOrphanTasksActivePerTenant publishes the per-tenant active orphan
	// count; the sweep calls it for every tenant per Tick so a tenant
	// whose count drops to zero re-publishes zero.
	SetOrphanTasksActivePerTenant(tenantID string, value int)
}

// Sweeper runs the periodic §8.10 orphan-cleanup sweep.
type Sweeper struct {
	store          sessionstore.Store
	tenants        TenantLister
	archive        treearchive.Store
	cascadeTimeout time.Duration
	interval       time.Duration
	clock          func() time.Time
	terminal       TerminalHook
	metrics        MetricsSink
}

// Options configures a Sweeper. A zero field selects its §8.10
// default.
type Options struct {
	// CascadeTimeout overrides DefaultCascadeTimeout.
	CascadeTimeout time.Duration
	// Interval overrides DefaultSweepInterval.
	Interval time.Duration
	// Clock overrides time.Now.
	Clock func() time.Time
	// Archive, when set, receives a §8.10 archive record for each
	// orphan the sweep terminates.
	Archive treearchive.Store
	// Terminal, when set, supersedes the in-package archive emission with
	// the gateway-side terminal-side-effects pipeline. See TerminalHook.
	Terminal TerminalHook
	// Metrics, when set, receives the §8.10 / §16.1 orphan-cleanup
	// observability hooks each Tick. spec: §8.10 / §16.1; F-8.10.7.
	Metrics MetricsSink
}

// New returns a Sweeper.
func New(store sessionstore.Store, tenants TenantLister, opts Options) *Sweeper {
	s := &Sweeper{
		store:          store,
		tenants:        tenants,
		archive:        opts.Archive,
		cascadeTimeout: opts.CascadeTimeout,
		interval:       opts.Interval,
		clock:          opts.Clock,
		terminal:       opts.Terminal,
		metrics:        opts.Metrics,
	}
	if s.cascadeTimeout <= 0 {
		s.cascadeTimeout = DefaultCascadeTimeout
	}
	if s.interval <= 0 {
		s.interval = DefaultSweepInterval
	}
	if s.clock == nil {
		s.clock = func() time.Time { return time.Now().UTC() }
	}
	return s
}

// Tick runs one sweep at now and returns the count of orphaned
// children it terminated. An orphan is a non-terminal session whose
// delegation tree root is terminal and whose root has been terminal
// for longer than cascadeTimeout. Per §16.1 lines 146-149 every Tick
// also bumps the cleanup-runs counter, the cumulative terminated
// counter, the fleet-wide active gauge, and the per-tenant active gauge
// when a MetricsSink is wired.
func (s *Sweeper) Tick(ctx context.Context, now time.Time) (int, error) {
	if s.metrics != nil {
		s.metrics.IncOrphanCleanupRun()
	}
	tenants, err := s.tenants.ListTenants(ctx)
	if err != nil {
		return 0, err
	}
	terminated := 0
	totalActive := 0
	for _, tenant := range tenants {
		n, remaining, err := s.sweepTenant(ctx, tenant, now)
		terminated += n
		totalActive += remaining
		if s.metrics != nil {
			s.metrics.SetOrphanTasksActivePerTenant(tenant, remaining)
		}
		if err != nil {
			if s.metrics != nil {
				s.metrics.AddOrphanTasksTerminated(terminated)
				s.metrics.SetOrphanTasksActive(totalActive)
			}
			return terminated, err
		}
	}
	if s.metrics != nil {
		s.metrics.AddOrphanTasksTerminated(terminated)
		s.metrics.SetOrphanTasksActive(totalActive)
	}
	return terminated, nil
}

// sweepTenant scans tenant rows and terminates orphans whose cascade
// window has elapsed. It returns the number terminated this Tick and
// the count of orphan rows remaining in a non-terminal state under the
// tenant's tree (the post-sweep gauge value).
func (s *Sweeper) sweepTenant(ctx context.Context, tenant string, now time.Time) (int, int, error) {
	rows, err := s.store.List(ctx, tenant, sessionstore.ListFilter{})
	if err != nil {
		return 0, 0, err
	}
	byID := make(map[string]sessionstore.Session, len(rows))
	for _, r := range rows {
		byID[r.ID] = r
	}
	terminated := 0
	activeOrphans := 0
	for _, row := range rows {
		if session.IsTerminal(row.State) || row.ParentSessionID == "" {
			continue
		}
		root := treeRoot(row, byID)
		if !session.IsTerminal(root.State) {
			continue // the tree is still active
		}
		// spec: §8.10 line 1103 — `lenny_orphan_tasks_active_per_tenant`
		// counts every non-terminal child whose root is terminal,
		// regardless of whether the cascade window has elapsed. F-8.10.7.
		activeOrphans++
		if now.Sub(root.UpdatedAt) <= s.cascadeTimeout {
			continue // still inside the cascade window
		}
		updated, err := s.store.Update(ctx, tenant, row.ID, func(r *sessionstore.Session) error {
			if session.IsTerminal(r.State) {
				return nil // concurrent transition — leave it alone
			}
			r.State = session.StateExpired
			// spec: §8.8 line 867 — orphan cleanup is a wall-clock
			// deadline (the cascade window after a root settles), so
			// the MCP boundary surfaces `failed` with code
			// `expired:deadline`. F-8.8.8.
			if r.FailureReason == "" {
				r.FailureReason = string(session.FailureExpiredDeadline)
			}
			return nil
		})
		if err != nil {
			return terminated, activeOrphans, err
		}
		if updated.State == session.StateExpired {
			terminated++
			// The row left the active-orphan set as part of this Tick.
			activeOrphans--
			if s.terminal != nil {
				// Gateway-side terminal pipeline — release the executor
				// (drains the pod, returns concurrent-mode slots per §5.2
				// line 519), emit SSE/audit/billing, and archive the child.
				// row.State is the pre-terminal state (§4.6).
				s.terminal.OnSessionTerminal(ctx, row.State, updated)
			} else {
				s.archiveOrphan(ctx, updated, root.ID)
			}
		}
	}
	return terminated, activeOrphans, nil
}

// treeRoot walks the ParentSessionID chain up from row to the tree
// root. The visited set guards against a malformed cyclic chain, and
// an ancestor absent from byID ends the walk at the deepest reachable
// node.
func treeRoot(row sessionstore.Session, byID map[string]sessionstore.Session) sessionstore.Session {
	cur := row
	visited := map[string]bool{}
	for cur.ParentSessionID != "" && !visited[cur.ID] {
		visited[cur.ID] = true
		next, ok := byID[cur.ParentSessionID]
		if !ok {
			break
		}
		cur = next
	}
	return cur
}

// archiveOrphan records a terminated orphan in the §8.10 archive.
func (s *Sweeper) archiveOrphan(ctx context.Context, sess sessionstore.Session, rootID string) {
	if s.archive == nil {
		return
	}
	result, _ := json.Marshal(map[string]any{
		"schemaVersion": 1,
		"taskId":        sess.ID,
		"state":         string(sess.State),
		"error": map[string]any{
			"code":    "CHILD_" + strings.ToUpper(string(sess.State)),
			"message": "orphaned child terminated after the cascade timeout elapsed",
		},
	})
	_ = s.archive.Archive(ctx, treearchive.ArchivedNode{
		TenantID:        sess.TenantID,
		RootSessionID:   rootID,
		NodeSessionID:   sess.ID,
		ParentSessionID: sess.ParentSessionID,
		State:           string(sess.State),
		Result:          string(result),
		SettledAt:       s.clock(),
	})
}

// Run drives the sweep on the configured interval until ctx is done.
// onTick, when non-nil, receives each sweep's terminated-count and
// error.
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
