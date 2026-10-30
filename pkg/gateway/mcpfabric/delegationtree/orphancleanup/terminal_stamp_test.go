// SPDX-License-Identifier: MIT

package orphancleanup_test

import (
	"context"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegationtree/orphancleanup"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
)

// steppingClock is a fake clock the stepping store advances.
type steppingClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *steppingClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

// steppingStore advances the clock by ten minutes before each Update, so each
// orphan's write lands after an earlier orphan's dispatched hook has run for
// ten minutes past the pass's now.
type steppingStore struct {
	sessionstore.Store
	clock *steppingClock
}

func (s *steppingStore) Update(ctx context.Context, tenantID, id string, mut func(*sessionstore.Session) error) (sessionstore.Session, error) {
	s.clock.mu.Lock()
	s.clock.now = s.clock.now.Add(10 * time.Minute)
	s.clock.mu.Unlock()
	return s.Store.Update(ctx, tenantID, id, mut)
}

// spec: §5.2 (Slot assignment atomicity)
//
// The orphan cleanup stamps each orphan's terminated_at with the clock read
// inside that row's Update. An earlier orphan's terminal hook runs on a
// dispatched goroutine while the pass goes on, so each write is dated by the
// clock at its own Update rather than at the pass start.
func TestOrphanCleanupStampsEachTerminalWriteWithItsOwnClockRead_spec_5_2(t *testing.T) {
	store := memstore.New()
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	seed(t, store, "sess_root", "", session.StateCompleted, base)
	seed(t, store, "sess_a", "sess_root", session.StateRunning, base)
	seed(t, store, "sess_b", "sess_root", session.StateRunning, base)
	passStart := base.Add(2 * time.Hour)
	clk := &steppingClock{now: passStart}
	sw := orphancleanup.New(&steppingStore{Store: store, clock: clk}, orphancleanup.StaticTenants{"acme"},
		orphancleanup.Options{Terminal: &fakeTerminalHook{}, Clock: clk.Now})
	if n, err := sw.Tick(context.Background(), passStart); err != nil || n != 2 {
		t.Fatalf("Tick = %d, %v, want 2 orphans ended", n, err)
	}
	sw.WaitDispatched()
	var stamps []time.Time
	for _, id := range []string{"sess_a", "sess_b"} {
		row, _ := store.Get(context.Background(), "acme", id)
		stamps = append(stamps, row.TerminatedAt)
	}
	sort.Slice(stamps, func(i, j int) bool { return stamps[i].Before(stamps[j]) })
	if !stamps[0].Equal(passStart.Add(10*time.Minute)) || !stamps[1].Equal(passStart.Add(20*time.Minute)) {
		t.Errorf("terminated_at = %v, want the clock at each orphan's own Update", stamps)
	}
}

// spec: §6.2, §7.2
//
// The orphan writer stamps its own expired:deadline reason over a reason an
// earlier recovery left on the row. An orphan that passed through
// resume_pending or awaiting_client_action carries that funnel's reason, and
// the expiry replaces it.
func TestOrphanCleanupStampsItsOwnReasonOverAFunnelReason_spec_7_2(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, st := range []session.State{session.StateResumePending, session.StateAwaitingClientAction} {
		t.Run(string(st), func(t *testing.T) {
			store := memstore.New()
			seed(t, store, "sess_root", "", session.StateCompleted, base)
			seedRow(t, store, sessionstore.Session{
				ID: "sess_child", TenantID: "acme", State: st, ParentSessionID: "sess_root",
				DelegationDepth: 1, CreatedAt: base, UpdatedAt: base,
				FailureReason: string(session.FailurePodEvicted),
			})
			sw := orphancleanup.New(store, orphancleanup.StaticTenants{"acme"}, orphancleanup.Options{})
			if n, err := sw.Tick(context.Background(), base.Add(2*time.Hour)); err != nil || n != 1 {
				t.Fatalf("Tick = %d, %v, want 1 orphan ended", n, err)
			}
			row, _ := store.Get(context.Background(), "acme", "sess_child")
			if row.State != session.StateExpired || row.FailureReason != string(session.FailureExpiredDeadline) {
				t.Errorf("orphan = %q reason %q, want expired with %q",
					row.State, row.FailureReason, session.FailureExpiredDeadline)
			}
		})
	}
}
