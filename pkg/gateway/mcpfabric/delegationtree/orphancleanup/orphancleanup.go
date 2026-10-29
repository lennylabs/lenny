// SPDX-License-Identifier: MIT

// Package orphancleanup implements the §8.10 orphan-cleanup job. A
// periodic sweep selects the tenant's active orphans: every non-terminal
// session that lenny/delegate_task created (DelegationDepth above zero)
// and that has an ancestor that is terminal or no longer exists.
//
// The sweep ends each selected session by its direct parent's state. A
// session whose parent no longer exists is expired on the pass that
// selects it. A session whose parent is terminal with the `cancel_all`
// cascade policy is cancelled on the pass that selects it, which completes
// a cascade that a store error or a replica failure interrupted. A session
// whose parent is terminal with any other policy is expired once
// cascadeTimeoutSeconds has elapsed since the earliest terminal transition
// among its ancestors. A session whose parent is live is counted and left
// in place, and the sweep visits rows in ascending delegation depth so the
// parent is evaluated first on the same pass.
package orphancleanup

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegationtree/treearchive"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
)

// §8.10 defaults.
const (
	// DefaultCascadeTimeout bounds how long an orphan under a terminal parent
	// whose cascade policy is something other than cancel_all persists after
	// its earliest terminal ancestor terminated
	// (`delegation.cascadeTimeoutSeconds`). An orphan under a terminal
	// cancel_all parent is cancelled with no window.
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

// Orphan is why a row is in the §8.10 active orphan set.
type Orphan struct {
	// Parent is the direct parent; zero when ParentPresent is false.
	Parent sessionstore.Session
	// ParentPresent is true when the row names a parent and byID holds it.
	ParentPresent bool
	// Anchor is the minimum non-zero TerminatedAt among the row's terminal
	// ancestors; zero when none carries one.
	Anchor time.Time
}

// Orphaned reports whether row is an active orphan under §8.10: a
// non-terminal session with DelegationDepth above zero whose parent is
// absent, that has a terminal ancestor, or whose ancestor chain ends at a
// delegated session whose own parent is absent. DelegationDepth rather than
// ParentSessionID marks a delegated session, because the parent foreign key
// is cleared when the parent row is deleted. byID holds the tenant's rows by
// id; an ancestor missing from it no longer exists.
//
// spec: §8.10 (Orphan cleanup).
func Orphaned(row sessionstore.Session, byID map[string]sessionstore.Session) (Orphan, bool) {
	if session.IsTerminal(row.State) || row.DelegationDepth <= 0 {
		return Orphan{}, false
	}
	parent, present := lookup(byID, row.ParentSessionID)
	o := Orphan{Parent: parent, ParentPresent: present}
	if !present {
		return o, true
	}
	anchor, terminal, broken := walkAncestors(row.ID, parent, byID)
	o.Anchor = anchor
	return o, terminal || broken
}

// walkAncestors follows the ParentSessionID chain up from parent through
// byID to its end. It returns the minimum non-zero TerminatedAt among the
// terminal ancestors, whether any ancestor is terminal, and whether the chain
// ends at a delegated ancestor whose own parent is absent. The visited set,
// seeded with the row's own id, stops the walk on a cyclic chain.
func walkAncestors(rowID string, parent sessionstore.Session, byID map[string]sessionstore.Session) (anchor time.Time, terminal, broken bool) {
	visited := map[string]bool{rowID: true}
	cur := parent
	for {
		visited[cur.ID] = true
		if session.IsTerminal(cur.State) {
			terminal = true
			anchor = earliest(anchor, cur.TerminatedAt)
		}
		next, ok := lookup(byID, cur.ParentSessionID)
		if !ok {
			return anchor, terminal, cur.DelegationDepth > 0
		}
		if visited[next.ID] {
			return anchor, terminal, false
		}
		cur = next
	}
}

// lookup returns the row byID holds for id; an empty id names no row.
func lookup(byID map[string]sessionstore.Session, id string) (sessionstore.Session, bool) {
	if id == "" {
		return sessionstore.Session{}, false
	}
	row, ok := byID[id]
	return row, ok
}

// earliest returns the earlier of a and t, ignoring a zero t and treating a
// zero a as unset.
func earliest(a, t time.Time) time.Time {
	if t.IsZero() || (!a.IsZero() && !t.Before(a)) {
		return a
	}
	return t
}

// Tick runs one sweep at now and returns the count of orphans it ended.
// Per §16.1 every Tick also bumps the cleanup-runs counter, the cumulative
// terminated counter, the fleet-wide active gauge, and the per-tenant active
// gauge when a MetricsSink is wired.
//
// A failure to list one tenant's rows skips that tenant, and a failed write
// of one orphan skips that orphan. Each is logged with the tenant_id and, for
// a row, the session_id, and the pass goes on. Tick returns errors.Join of
// every collected error, so a non-nil error does not mean the pass stopped;
// the next pass evaluates a failed orphan again. The terminated count is the
// writes that applied; their terminal hooks run on goroutines that own the
// writes' terminal claims and may still be running when Tick returns.
//
// spec: §8.10 (Orphan cleanup); §10.1.1 (Primary:); §7.1 (Terminal pipeline context:).
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

// sweepTenant lists the tenant once, visits its rows in ascending
// delegation depth, and ends each active orphan whose disposition is due. It
// returns the number ended this Tick, the active orphans left non-terminal
// (the post-sweep gauge value), and the joined errors of the pass. An applied
// end writes the ended state and now into byID, so a child visited later in
// the pass sees its parent's end. A listing error returns at once with no
// rows swept; a failed write leaves its orphan in the active count and the
// sweep goes on to the next.
//
// spec: §8.10 (Orphan cleanup); §10.1.1 (Primary:).
func (s *Sweeper) sweepTenant(ctx context.Context, tenant string, now time.Time) (int, int, error) {
	rows, err := s.store.List(ctx, tenant, sessionstore.ListFilter{})
	if err != nil {
		slog.Warn("orphancleanup: list a tenant's sessions", "tenant_id", tenant, "error", err)
		return 0, 0, fmt.Errorf("orphancleanup: list sessions: tenant %s: %w", tenant, err)
	}
	byID := IndexByID(rows)
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].DelegationDepth < rows[j].DelegationDepth })
	terminated := 0
	activeOrphans := 0
	var errs []error
	for _, row := range rows {
		o, ok := Orphaned(row, byID)
		if !ok {
			continue
		}
		// spec: §8.10 — the active-orphan gauges count every selected
		// session, whether or not its disposition is due. F-8.10.7.
		activeOrphans++
		to, due := s.disposition(o, now)
		if !due {
			continue
		}
		applied, err := s.endOrphan(ctx, tenant, row, to)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if applied {
			terminated++
			// The row left the active-orphan set as part of this Tick.
			activeOrphans--
			ended := byID[row.ID]
			ended.State = to
			ended.TerminatedAt = now
			byID[row.ID] = ended
		}
	}
	return terminated, activeOrphans, errors.Join(errs...)
}

// IndexByID keys rows by their session id, the map Orphaned walks.
func IndexByID(rows []sessionstore.Session) map[string]sessionstore.Session {
	byID := make(map[string]sessionstore.Session, len(rows))
	for _, r := range rows {
		byID[r.ID] = r
	}
	return byID
}

// disposition returns the terminal state an active orphan ends in at now and
// whether that end is due. A session whose parent no longer exists is
// expired at once. A session under a live parent waits for it. A session
// under a terminal `cancel_all` parent is cancelled at once. Any other
// terminal parent policy expires the session once cascadeTimeout has elapsed
// from the earliest terminal ancestor's TerminatedAt; the window is inclusive
// of its end, and a zero anchor opens no window.
//
// spec: §8.10 (Orphan cleanup).
func (s *Sweeper) disposition(o Orphan, now time.Time) (session.State, bool) {
	switch {
	case !o.ParentPresent:
		return session.StateExpired, true
	case !session.IsTerminal(o.Parent.State):
		return "", false
	case o.Parent.CascadeOnFailure.Resolve() == session.CascadeCancelAll:
		return session.StateCancelled, true
	case o.Anchor.IsZero() || now.Sub(o.Anchor) <= s.cascadeTimeout:
		return "", false
	default:
		return session.StateExpired, true
	}
}

// endOrphan is the sweep's funnel terminal write for one orphan: it opens
// the terminal claim before its Update, writes to (cancelled or expired) only
// over a non-terminal locked row with the Terminated fact recorded in the
// write, and when the write applied hands the claim to dispatchTerminal,
// whose goroutine runs the terminal pipeline with fromState from the locked
// row and then ends the claim. The terminal pipeline runs the ended row's own
// cascade. A row another writer already ended, or one that no longer exists,
// is not applied and the claim ends at once. A store error ends the claim at
// once, is logged with the tenant and session IDs, and is returned for the
// pass to collect; the sweep goes on to the next orphan.
//
// spec: §8.10 (Orphan cleanup); §7.2 (Terminal writes from any non-terminal state); §10.1.1 (Primary:).
func (s *Sweeper) endOrphan(ctx context.Context, tenant string, row sessionstore.Session, to session.State) (bool, error) {
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
		switch to {
		case session.StateCancelled:
			// spec: §8.10 — the cancel completes the terminal `cancel_all`
			// parent's cascade, and a cascaded cancel stamps no reason.
			r.State = session.StateCancelled
		case session.StateExpired:
			r.State = session.StateExpired
			// spec: §8.8 — an orphan expiry is a wall-clock deadline (the
			// cascade window after an ancestor settles, or an absent parent),
			// so the MCP boundary surfaces `failed` with code
			// `expired:deadline`. F-8.8.8.
			// spec: §6.2, §7.2 — the writer stamps its own reason over any
			// reason an earlier recovery left on the row.
			r.FailureReason = string(session.FailureExpiredDeadline)
		default:
			return fmt.Errorf("orphancleanup: unsupported orphan end state %q", to)
		}
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
		slog.Warn("orphancleanup: end an orphaned session", "tenant_id", tenant, "session_id", row.ID, "state", string(to), "error", err)
		return false, fmt.Errorf("orphancleanup: end orphan: tenant %s session %s: %w", tenant, row.ID, err)
	}
	s.dispatchTerminal(ctx, end, func(hctx context.Context) {
		if s.terminal != nil {
			// Gateway-side terminal pipeline — release the executor (drains
			// the pod, returns concurrent-mode slots per §5.2), emit SSE/
			// audit/billing, run the row's own cascade, and archive it.
			// from is the locked row's pre-terminal state (§4.6).
			s.terminal.OnSessionTerminal(hctx, from, updated)
			return
		}
		s.archiveOrphan(hctx, updated)
	})
	return true, nil
}

// dispatchTerminal hands an applied orphan end's open claim to a goroutine
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

// archiveOrphan records an ended orphan in the §8.10 archive under its
// row's delegation-tree root.
func (s *Sweeper) archiveOrphan(ctx context.Context, sess sessionstore.Session) {
	if s.archive == nil {
		return
	}
	result, _ := json.Marshal(map[string]any{
		"schemaVersion": 1,
		"taskId":        sess.ID,
		"state":         string(sess.State),
		"error": map[string]any{
			"code":    "CHILD_" + strings.ToUpper(string(sess.State)),
			"message": "orphaned child terminated by orphan cleanup",
		},
	})
	_ = s.archive.Archive(ctx, treearchive.ArchivedNode{
		TenantID:        sess.TenantID,
		RootSessionID:   sess.RootSessionID,
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
