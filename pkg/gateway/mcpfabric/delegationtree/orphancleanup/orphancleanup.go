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
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegationtree/treearchive"
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
// per §5.2 and drains pods per §6.2), §11.7 audit, §7.2 SSE,
// §11.2.1 billing, and §8.10 archive — run once through the same
// Server.recordSessionCompleted path the REST handlers use.
//
// spec: §5.2 — slot release on session end / expiry; §8.10 —
// orphan cascade.
type TerminalHook interface {
	// BeginTerminal opens the writer's terminal claim before its terminal
	// Update and returns the idempotent function that ends it.
	// spec: §7.2 (Interactive Session Model).
	BeginTerminal(sessionID string) (end func())
	// fromState is the orphan's pre-terminal state, read on the locked row
	// the sweep forced terminal, so the terminal pod-release path can
	// distinguish a pre-running claimed session from a running one (§4.6).
	OnSessionTerminal(ctx context.Context, fromState session.State, sess sessionstore.Session)
}

// MetricsSink receives the §8.10 / §16.1 orphan-cleanup and per-tenant
// orphan-gauge observations a sweep generates. Implementations are
// best-effort and must not block the sweep. spec: §8.10; §16.1. F-8.10.7.
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

	// dispatched counts the terminal-hook goroutines dispatchTerminal has
	// started and that have not returned. Tests wait on it to observe a
	// pass's hooks; production observes the same work through the terminal
	// claims each goroutine holds.
	dispatched sync.WaitGroup
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
// for longer than cascadeTimeout. Per §16.1 every Tick
// also bumps the cleanup-runs counter, the cumulative terminated
// counter, the fleet-wide active gauge, and the per-tenant active gauge
// when a MetricsSink is wired.
//
// A failure to list one tenant's rows skips that tenant, and a failed write
// of one orphan skips that orphan. Each is logged with the tenant_id and, for
// a row, the session_id, and the pass goes on. Tick returns errors.Join of
// every collected error, so a non-nil error does not mean the pass stopped;
// the next pass evaluates a failed orphan again. The terminated count is the
// writes that applied; their terminal hooks run on goroutines that own the
// writes' terminal claims and may still be running when Tick returns.
//
// spec: §8.10; §10.1.1 (Primary:); §7.1 (Terminal pipeline context:).
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
	var errs []error
	for _, tenant := range tenants {
		n, remaining, err := s.sweepTenant(ctx, tenant, now)
		terminated += n
		totalActive += remaining
		if err != nil {
			errs = append(errs, err)
		}
		if s.metrics != nil {
			s.metrics.SetOrphanTasksActivePerTenant(tenant, remaining)
		}
	}
	if s.metrics != nil {
		s.metrics.AddOrphanTasksTerminated(terminated)
		s.metrics.SetOrphanTasksActive(totalActive)
	}
	return terminated, errors.Join(errs...)
}

// sweepTenant scans tenant rows and terminates orphans whose cascade
// window has elapsed. It returns the number terminated this Tick, the
// count of orphan rows remaining in a non-terminal state under the
// tenant's tree (the post-sweep gauge value), and the joined errors of the
// pass. A listing error returns at once with no rows swept; a failed write
// leaves its orphan in the active count and the sweep goes on to the next.
//
// spec: §8.10; §10.1.1 (Primary:).
func (s *Sweeper) sweepTenant(ctx context.Context, tenant string, now time.Time) (int, int, error) {
	rows, err := s.store.List(ctx, tenant, sessionstore.ListFilter{})
	if err != nil {
		slog.Warn("orphancleanup: list a tenant's sessions", "tenant_id", tenant, "error", err)
		return 0, 0, fmt.Errorf("orphancleanup: list sessions: tenant %s: %w", tenant, err)
	}
	byID := make(map[string]sessionstore.Session, len(rows))
	for _, r := range rows {
		byID[r.ID] = r
	}
	terminated := 0
	activeOrphans := 0
	var errs []error
	for _, row := range rows {
		if session.IsTerminal(row.State) || row.ParentSessionID == "" {
			continue
		}
		root := treeRoot(row, byID)
		if !session.IsTerminal(root.State) {
			continue // the tree is still active
		}
		// spec: §8.10 — `lenny_orphan_tasks_active_per_tenant`
		// counts every non-terminal child whose root is terminal,
		// regardless of whether the cascade window has elapsed. F-8.10.7.
		activeOrphans++
		if !cascadeWindowElapsed(root, now, s.cascadeTimeout) {
			continue
		}
		applied, err := s.expireOrphan(ctx, tenant, row, root.ID)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if applied {
			terminated++
			// The row left the active-orphan set as part of this Tick.
			activeOrphans--
		}
	}
	return terminated, activeOrphans, errors.Join(errs...)
}

// expireOrphan is the sweep's funnel terminal write for one orphan: it opens
// the terminal claim before its Update, writes expired only over a
// non-terminal locked row with the Terminated fact recorded in the write, and
// when the write applied hands the claim to dispatchTerminal, whose goroutine
// runs the terminal pipeline with fromState from the locked row and then ends
// the claim. A row another writer already ended, or one that no longer
// exists, is not applied and the claim ends at once. A store error ends the
// claim at once, is logged with the tenant and session IDs, and is returned
// for the pass to collect; the sweep goes on to the next child.
//
// spec: §8.10; §7.2 (Interactive Session Model); §10.1.1 (Primary:).
func (s *Sweeper) expireOrphan(ctx context.Context, tenant string, row sessionstore.Session, rootID string) (bool, error) {
	end := func() {}
	if s.terminal != nil {
		end = s.terminal.BeginTerminal(row.ID)
	}
	var from session.State
	updated, err := s.store.Update(ctx, tenant, row.ID, func(r *sessionstore.Session) error {
		if session.IsTerminal(r.State) {
			return sessionstore.ErrSuperseded
		}
		from = r.State
		r.State = session.StateExpired
		// spec: §8.8 — orphan cleanup is a wall-clock deadline (the
		// cascade window after a root settles), so the MCP boundary
		// surfaces `failed` with code `expired:deadline`. F-8.8.8.
		// spec: §6.2, §7.2 — the writer stamps its own reason over any
		// reason an earlier recovery left on the row.
		r.FailureReason = string(session.FailureExpiredDeadline)
		// spec: §5.2 (Slot assignment atomicity) — the stamp reads the clock
		// inside this row's Update, because the dispatched hooks of earlier
		// rows, seal included, may have run past the pass's now.
		sessionstore.MarkTerminated(r, from, s.clock())
		return nil
	})
	if errors.Is(err, sessionstore.ErrSuperseded) || errors.Is(err, sessionstore.ErrNotFound) {
		end()
		return false, nil
	}
	if err != nil {
		end()
		slog.Warn("orphancleanup: expire an orphaned child", "tenant_id", tenant, "session_id", row.ID, "error", err)
		return false, fmt.Errorf("orphancleanup: expire orphan: tenant %s session %s: %w", tenant, row.ID, err)
	}
	s.dispatchTerminal(ctx, end, func(hctx context.Context) {
		if s.terminal != nil {
			// Gateway-side terminal pipeline — release the executor (drains
			// the pod, returns concurrent-mode slots per §5.2), emit SSE/
			// audit/billing, and archive the child. from is the locked row's
			// pre-terminal state (§4.6).
			s.terminal.OnSessionTerminal(hctx, from, updated)
			return
		}
		s.archiveOrphan(hctx, updated, rootID)
	})
	return true, nil
}

// dispatchTerminal hands an applied orphan expiry's open claim to a goroutine
// that runs the terminal hooks and then ends the claim. The hooks receive a
// context that keeps ctx's values and drops its cancellation, so the end of
// the pass does not cut a terminal pipeline that has started, and the open
// claim is what the gateway's shutdown wait observes.
//
// spec: §7.1 (Terminal pipeline context:); §10.1.1 (Primary:).
func (s *Sweeper) dispatchTerminal(ctx context.Context, end func(), hooks func(context.Context)) {
	hctx := context.WithoutCancel(ctx)
	s.dispatched.Add(1)
	go func() {
		defer s.dispatched.Done()
		defer end()
		hooks(hctx)
	}()
}

// cascadeWindowElapsed reports whether root has been terminal for longer than
// cascadeTimeout. The window counts from the root's TerminatedAt, which a later
// write to the root does not move, so a root with no recorded termination
// instant opens no window and its orphans are left for a later pass.
//
// spec: §8.10 (the root session has been terminated for cascadeTimeoutSeconds).
func cascadeWindowElapsed(root sessionstore.Session, now time.Time, cascadeTimeout time.Duration) bool {
	if root.TerminatedAt.IsZero() {
		return false
	}
	return now.Sub(root.TerminatedAt) > cascadeTimeout
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
