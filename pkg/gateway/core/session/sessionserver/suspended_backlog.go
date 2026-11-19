// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync/atomic"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
)

// HoldsPod is the watchdog's BacklogResumer pod check: it reports whether
// the row names its pod, or the server runs with no pod binder. The
// suspended sweep leaves a row that holds no pod to the gateway leader's
// backlog re-check. spec: §6.2 (Pod State Machine).
func (s *Server) HoldsPod(row sessionstore.Session) bool {
	return s.holdsPod(row)
}

// ResumeBacklog is the watchdog's BacklogResumer hook for a `suspended`
// session that holds its pod (§7.2 delivery path 6 Backlog resume.). It
// returns false without reading the backlog when a pod binder is wired and
// this replica holds no binding for the session, or when this replica does
// not hold the session's coordination lease or the lease read fails. On a
// held-message read error it logs and returns true, so the sweep releases
// nothing this tick. With no held message it returns false. When the resume
// admission check refuses or fails it logs at INFO and returns false, so the
// hold timer decides the release. Otherwise it returns true and starts
// ResumeSuspended off the caller, on the
// server-lifetime context, at most one per session on this replica, so a
// watchdog tick never waits on a delivery.
//
// spec: §7.2 (Message delivery routing — seven paths, path 6)
func (s *Server) ResumeBacklog(ctx context.Context, row sessionstore.Session) bool {
	if s.podBinder != nil && s.snapshotBinding(row.ID) == nil {
		return false
	}
	if !s.coordinatesSession(ctx, row.TenantID, row.ID) {
		return false
	}
	n, err := s.heldLen(ctx, row)
	if err != nil {
		slog.Warn("sessionserver: read the held messages of a suspended session", "session_id", row.ID, "error", err)
		return true
	}
	if n == 0 {
		return false
	}
	// spec: §11.1 (Resume admission) — a backlog resume the check refuses,
	// or whose count fails, starts nothing, and the hold timer decides the
	// release this tick; the next evaluation checks again.
	if err := s.admitResume(ctx, row); err != nil {
		logCapRefusal("sessionserver: backlog resume of a suspended session not admitted", row.ID, err)
		return false
	}
	if s.backlogResumes.begin(row.ID) {
		go func() {
			defer s.backlogResumes.end(row.ID)
			if err := s.ResumeSuspended(s.lifetime(), row); err != nil {
				slog.Info("sessionserver: backlog resume of a suspended session", "session_id", row.ID, "error", err)
			}
		}()
	}
	return true
}

// ResumeSuspended resumes a `suspended` session that a message is held for
// (§7.2 delivery path 6). It takes the session's delivery lock and branches
// on the row it is passed; it reads no row, and every write it makes is
// guarded on the locked row.
//
//   - A row that holds no pod moves to resume_pending, and the resume driver
//     restores it.
//   - On the coordinating replica, a row that holds its pod resumes on that
//     pod and its held messages are delivered (resumeHeldAndDrain). When the
//     pod was released after the read, the row moves to resume_pending; a
//     delivery failure after the resume returns nil, because the held entry
//     stays at the head for the next delivery point.
//   - On any other replica, the guarded resume_pending write applies only
//     when the pod was released after the read; otherwise the coordinating
//     replica's backlog resume acts.
//
// The lock is not reentrant, so a caller must not hold it.
//
// spec: §7.2 (Message delivery routing — seven paths, path 6)
func (s *Server) ResumeSuspended(ctx context.Context, row sessionstore.Session) error {
	unlock, err := s.deliveryLocks.Lock(ctx, row.ID)
	if err != nil {
		return fmt.Errorf("lock delivery of session %s: %w", row.ID, err)
	}
	defer unlock()
	if s.holdsPod(row) && s.coordinatesSession(ctx, row.TenantID, row.ID) {
		_, err := s.resumeHeldAndDrain(ctx, row)
		switch {
		case err == nil, errors.Is(err, errHeldDeliveryFailed):
			return nil
		case !errors.Is(err, errResumeHeldPodReleased):
			return err
		}
	}
	if _, err := s.enterResumePending(ctx, row); err != nil {
		return fmt.Errorf("move suspended session %s to resume_pending: %w", row.ID, err)
	}
	return nil
}

// PodlessSuspendedLister pages through a tenant's `suspended` sessions that
// name no pod, in id order. The pgstore and memstore session stores satisfy
// it; the gateway wires the backlog re-check only with a store that does. spec: §7.2 (Message delivery routing — seven paths, path 6).
type PodlessSuspendedLister interface {
	ListPodlessSuspended(ctx context.Context, tenantID, afterID string, limit int) ([]sessionstore.Session, error)
}

// Defaults of the backlog re-check.
const (
	// DefaultBacklogRecheckPageSize is how many podless rows one page of
	// the re-check holds.
	DefaultBacklogRecheckPageSize = 500
	// DefaultBacklogRecheckConcurrency bounds the held-message reads one
	// page runs at once.
	DefaultBacklogRecheckConcurrency = 32
	// DefaultSuspendedBacklogRecheckInterval is the re-check interval,
	// gateway.suspendedBacklogRecheckSeconds.
	DefaultSuspendedBacklogRecheckInterval = 300 * time.Second
	// MinSuspendedBacklogRecheckInterval is the shortest interval the
	// gateway accepts.
	MinSuspendedBacklogRecheckInterval = 30 * time.Second
)

// BacklogRechecker is the gateway leader's backlog re-check of podless
// `suspended` sessions (§7.2 delivery path 6 Backlog resume.). A podless
// suspended session has no coordinating replica, so a message held for it
// whose resume_pending write did not follow is resumed by this re-check.
// Invariant: every podless suspended session whose backlog condition holds
// reaches resume_pending within one interval plus one leader failover
// window. It applies no lease gate and takes no delivery lock; every write
// is guarded on the locked row.
//
// spec: §7.2 (Message delivery routing — seven paths, path 6), §12.5
// (Artifact Store)
type BacklogRechecker struct {
	// Server evaluates and writes each row's backlog resume.
	Server *Server
	// Lister pages through the podless rows.
	Lister PodlessSuspendedLister
	// Tenants lists the tenants to re-check.
	Tenants func(ctx context.Context) ([]string, error)
	// Interval is the re-check interval; zero selects
	// DefaultSuspendedBacklogRecheckInterval.
	Interval time.Duration
	// PageSize and Concurrency default to DefaultBacklogRecheckPageSize and
	// DefaultBacklogRecheckConcurrency.
	PageSize    int
	Concurrency int
}

// Run calls Pass once at start and then on every interval until ctx ends.
// The gateway runs it only while the replica is the leader.
func (b *BacklogRechecker) Run(ctx context.Context) {
	interval := b.Interval
	if interval <= 0 {
		interval = DefaultSuspendedBacklogRecheckInterval
	}
	b.Pass(ctx)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			b.Pass(ctx)
		}
	}
}

// Pass re-checks every tenant's podless suspended sessions once and returns
// how many it moved to resume_pending. It lists one page at a time, each
// continuing after the last id of the page before, so a pass holds at most
// one page of rows. For each row it reads the held messages, with at most
// Concurrency reads in flight, and writes resume_pending when one is held. A
// tenant list error ends the pass; a page list error ends that tenant's pass;
// a held-message or write error is logged and the pass continues. A row
// resumed and suspended again after the listing may be resumed once more
// than needed, which is accepted.
func (b *BacklogRechecker) Pass(ctx context.Context) int {
	tenants, err := b.Tenants(ctx)
	if err != nil {
		slog.Warn("sessionserver: list tenants for the suspended backlog re-check", "error", err)
		return 0
	}
	var resumed atomic.Int64
	for _, tenant := range tenants {
		b.passTenant(ctx, tenant, &resumed)
	}
	return int(resumed.Load())
}

// passTenant runs the re-check over one tenant's pages.
func (b *BacklogRechecker) passTenant(ctx context.Context, tenant string, resumed *atomic.Int64) {
	pageSize := b.PageSize
	if pageSize <= 0 {
		pageSize = DefaultBacklogRecheckPageSize
	}
	after := ""
	for {
		page, err := b.Lister.ListPodlessSuspended(ctx, tenant, after, pageSize)
		if err != nil {
			slog.Warn("sessionserver: list podless suspended sessions", "tenant_id", tenant, "error", err)
			return
		}
		if len(page) == 0 {
			return
		}
		b.recheckPage(ctx, page, resumed)
		if len(page) < pageSize {
			return
		}
		after = page[len(page)-1].ID
	}
}

// recheckPage evaluates one page with bounded concurrency.
func (b *BacklogRechecker) recheckPage(ctx context.Context, page []sessionstore.Session, resumed *atomic.Int64) {
	limit := b.Concurrency
	if limit <= 0 {
		limit = DefaultBacklogRecheckConcurrency
	}
	var g errgroup.Group
	g.SetLimit(limit)
	for _, row := range page {
		g.Go(func() error {
			if b.Server.recheckRow(ctx, row) {
				resumed.Add(1)
			}
			return nil
		})
	}
	_ = g.Wait()
}

// recheckRow evaluates one podless row's backlog and reports whether it
// moved the row to resume_pending.
func (s *Server) recheckRow(ctx context.Context, row sessionstore.Session) bool {
	n, err := s.heldLen(ctx, row)
	if err != nil {
		slog.Warn("sessionserver: read the held messages of a podless suspended session", "session_id", row.ID, "error", err)
		return false
	}
	if n == 0 {
		return false
	}
	if _, err := s.enterResumePending(ctx, row); err != nil {
		// spec: §11.1 (Resume admission) — a refused row stays suspended,
		// and the next pass checks it again.
		logCapRefusal("sessionserver: resume a podless suspended session with held messages", row.ID, err)
		return false
	}
	return true
}
