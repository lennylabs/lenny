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
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/treearchive"
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

// Sweeper runs the periodic §8.10 orphan-cleanup sweep.
type Sweeper struct {
	store          sessionstore.Store
	tenants        TenantLister
	archive        treearchive.Store
	cascadeTimeout time.Duration
	interval       time.Duration
	clock          func() time.Time
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
// for longer than cascadeTimeout.
func (s *Sweeper) Tick(ctx context.Context, now time.Time) (int, error) {
	tenants, err := s.tenants.ListTenants(ctx)
	if err != nil {
		return 0, err
	}
	terminated := 0
	for _, tenant := range tenants {
		n, err := s.sweepTenant(ctx, tenant, now)
		terminated += n
		if err != nil {
			return terminated, err
		}
	}
	return terminated, nil
}

func (s *Sweeper) sweepTenant(ctx context.Context, tenant string, now time.Time) (int, error) {
	rows, err := s.store.List(ctx, tenant, sessionstore.ListFilter{})
	if err != nil {
		return 0, err
	}
	byID := make(map[string]sessionstore.Session, len(rows))
	for _, r := range rows {
		byID[r.ID] = r
	}
	terminated := 0
	for _, row := range rows {
		if session.IsTerminal(row.State) || row.ParentSessionID == "" {
			continue
		}
		root := treeRoot(row, byID)
		if !session.IsTerminal(root.State) {
			continue // the tree is still active
		}
		if now.Sub(root.UpdatedAt) <= s.cascadeTimeout {
			continue // still inside the cascade window
		}
		updated, err := s.store.Update(ctx, tenant, row.ID, func(r *sessionstore.Session) error {
			if session.IsTerminal(r.State) {
				return nil // concurrent transition — leave it alone
			}
			r.State = session.StateExpired
			return nil
		})
		if err != nil {
			return terminated, err
		}
		if updated.State == session.StateExpired {
			terminated++
			s.archiveOrphan(ctx, updated, root.ID)
		}
	}
	return terminated, nil
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
