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

// steppingClock is a fake clock the post-terminal hook advances.
type steppingClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *steppingClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

// slowHook is a post-terminal hook that takes ten minutes on the clock.
type slowHook struct{ clock *steppingClock }

func (*slowHook) BeginTerminal(string) func() { return func() {} }

func (h *slowHook) OnSessionTerminal(context.Context, session.State, sessionstore.Session) {
	h.clock.mu.Lock()
	defer h.clock.mu.Unlock()
	h.clock.now = h.clock.now.Add(10 * time.Minute)
}

// spec: §5.2 (Slot assignment atomicity)
//
// The orphan cleanup stamps each orphan's terminated_at with the clock read
// inside that row's Update, so the second orphan of a pass is dated after
// the first orphan's post-terminal hook returned rather than at the pass
// start.
func TestOrphanCleanupStampsEachTerminalWriteWithItsOwnClockRead_spec_5_2(t *testing.T) {
	store := memstore.New()
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	seed(t, store, "sess_root", "", session.StateCompleted, base)
	seed(t, store, "sess_a", "sess_root", session.StateRunning, base)
	seed(t, store, "sess_b", "sess_root", session.StateRunning, base)
	passStart := base.Add(2 * time.Hour)
	clk := &steppingClock{now: passStart}
	sw := orphancleanup.New(store, orphancleanup.StaticTenants{"acme"},
		orphancleanup.Options{Terminal: &slowHook{clock: clk}, Clock: clk.Now})
	if n, err := sw.Tick(context.Background(), passStart); err != nil || n != 2 {
		t.Fatalf("Tick = %d, %v, want 2 orphans ended", n, err)
	}
	var stamps []time.Time
	for _, id := range []string{"sess_a", "sess_b"} {
		row, _ := store.Get(context.Background(), "acme", id)
		stamps = append(stamps, row.TerminatedAt)
	}
	sort.Slice(stamps, func(i, j int) bool { return stamps[i].Before(stamps[j]) })
	if !stamps[0].Equal(passStart) || !stamps[1].Equal(passStart.Add(10*time.Minute)) {
		t.Errorf("terminated_at = %v, want the pass start and ten minutes later", stamps)
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
				CreatedAt: base, UpdatedAt: base,
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
