// SPDX-License-Identifier: MIT

package watchdog_test

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/watchdog"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
)

// syncWarnings is a concurrency-safe TerminalHook that also implements
// watchdog.ExpiryWarningNotifier. Several watchdogs, one per simulated
// gateway replica, share one instance so the test counts the warnings every
// replica sent for a session.
type syncWarnings struct {
	mu       sync.Mutex
	perID    map[string]int
	warnings []expiryWarningCall
}

func newSyncWarnings() *syncWarnings { return &syncWarnings{perID: map[string]int{}} }

func (*syncWarnings) BeginTerminal(string) func() { return func() {} }

func (*syncWarnings) OnSessionTerminal(context.Context, session.State, sessionstore.Session) {}

func (c *syncWarnings) OnSessionExpiringSoon(_ context.Context, sess sessionstore.Session, maxAge, remaining int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.perID[sess.ID]++
	c.warnings = append(c.warnings, expiryWarningCall{sess.ID, maxAge, remaining})
}

func (c *syncWarnings) count(id string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.perID[id]
}

func (c *syncWarnings) total() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.warnings)
}

// inBandRow is a running row whose active age is 6900s at now, 300s inside
// the default warning window of a 7200s cap, with a recent activity stamp so
// the idle sweep leaves it alone.
func inBandRow(id string, now time.Time) sessionstore.Session {
	return sessionstore.Session{
		ID: id, TenantID: "acme", State: session.StateRunning,
		CreatedAt: now.Add(-3 * time.Hour), UpdatedAt: now,
		LastAgentActivityAt:          now.Add(-time.Second),
		AccumulatedSessionAgeSeconds: 6900,
	}
}

// spec: 11.3 (Session expiry warning), 10.1.1 (Primary:)
//
// The listing only selects a candidate; the warning is re-evaluated on the
// locked row. A row the listing shows inside the band but whose live row has
// since become terminal, paused its age timer, passed its deadline, already
// recorded a warning for this deadline, or disappeared is not warned and is
// not rewritten. A live row still in the band is warned once and records the
// warned deadline.
func TestExpiryWarningRechecksTheLockedRow_spec_11_3(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name string
		// live mutates the seeded live row; nil deletes it (the row is
		// listed but absent from the store).
		live     func(r *sessionstore.Session)
		absent   bool
		wantWarn bool
	}{
		{name: "still in band and never warned", live: func(*sessionstore.Session) {}, wantWarn: true},
		{name: "in band and warned for an earlier deadline", live: func(r *sessionstore.Session) { r.ExpiryWarnedCapSeconds = 3600 }, wantWarn: true},
		{name: "already warned for this deadline", live: func(r *sessionstore.Session) { r.ExpiryWarnedCapSeconds = 7200 }},
		{name: "completed after the listing", live: func(r *sessionstore.Session) { r.State = session.StateCompleted }},
		{name: "failed after the listing", live: func(r *sessionstore.Session) { r.State = session.StateFailed }},
		{name: "cancelled after the listing", live: func(r *sessionstore.Session) { r.State = session.StateCancelled }},
		{name: "expired after the listing", live: func(r *sessionstore.Session) { r.State = session.StateExpired }},
		{name: "suspended after the listing", live: func(r *sessionstore.Session) { r.State = session.StateSuspended }},
		{name: "resume_pending after the listing", live: func(r *sessionstore.Session) { r.State = session.StateResumePending }},
		{name: "awaiting_client_action after the listing", live: func(r *sessionstore.Session) { r.State = session.StateAwaitingClientAction }},
		{name: "input_required after the listing", live: func(r *sessionstore.Session) { r.State = session.StateInputRequired }, wantWarn: true},
		{name: "past its deadline on the locked row", live: func(r *sessionstore.Session) { r.AccumulatedSessionAgeSeconds = 7300 }},
		{name: "outside the window on the locked row", live: func(r *sessionstore.Session) { r.AccumulatedSessionAgeSeconds = 6000 }},
		{name: "deleted after the listing", absent: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			mem := memstore.NewWithClock(func() time.Time { return now })
			listed := inBandRow("sess", now)
			if !c.absent {
				live := listed
				c.live(&live)
				mustCreate(t, mem, live)
			}
			var before sessionstore.Session
			if !c.absent {
				before = mustGet(t, mem, "sess")
			}
			hook := newSyncWarnings()
			store := &staleListStore{Store: mem, listed: []sessionstore.Session{listed}}
			w := watchdog.New(store, watchdog.StaticTenants{"acme"}, ageCfg(7200), func() time.Time { return now }).
				WithTerminalHook(hook)
			res, err := w.Tick(context.Background(), now)
			if err != nil {
				t.Fatalf("Tick: %v", err)
			}
			want := 0
			if c.wantWarn {
				want = 1
			}
			if got := hook.count("sess"); got != want || res.ExpiryWarnings != want {
				t.Fatalf("warnings = %d, ExpiryWarnings = %d, want %d", got, res.ExpiryWarnings, want)
			}
			if c.absent {
				return
			}
			after := mustGet(t, mem, "sess")
			if c.wantWarn {
				if after.ExpiryWarnedCapSeconds != 7200 {
					t.Fatalf("ExpiryWarnedCapSeconds = %d, want 7200", after.ExpiryWarnedCapSeconds)
				}
				if wn := hook.warnings[0]; wn.maxSessionAge != 7200 || wn.remainingSeconds != 300 {
					t.Fatalf("warning = %+v, want maxSessionAge 7200 remaining 300", wn)
				}
				return
			}
			if after.ExpiryWarnedCapSeconds != before.ExpiryWarnedCapSeconds || after.State != before.State {
				t.Fatalf("superseded row was rewritten: before %+v after %+v", before, after)
			}
		})
	}
}

// spec: 11.3 (Session expiry warning)
//
// The warning goes out once per deadline. A row warned for one cap is warned
// again when its effective cap changes to a deadline it has not been warned
// for, and not again while the cap stays the same.
func TestExpiryWarningOncePerDeadline_spec_11_3(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	store := memstore.NewWithClock(func() time.Time { return now })
	row := inBandRow("sess", now)
	row.RuntimeRef = "rt"
	mustCreate(t, store, row)
	hook := newSyncWarnings()
	clock := func() time.Time { return now }
	tick := func(resolver watchdog.SessionAgeResolver) {
		t.Helper()
		w := watchdog.New(store, watchdog.StaticTenants{"acme"}, ageCfg(7200), clock).WithTerminalHook(hook)
		if resolver != nil {
			w = w.WithSessionAgeResolver(resolver)
		}
		if _, err := w.Tick(context.Background(), now); err != nil {
			t.Fatalf("Tick: %v", err)
		}
	}
	tick(nil)
	tick(nil)
	if got := hook.count("sess"); got != 1 {
		t.Fatalf("after two passes under the 7200s cap: %d warnings, want 1", got)
	}
	// A tighter per-runtime cap of 7000s puts the deadline 100s away: a new
	// deadline the row has not been warned for.
	tick(fakeAgeResolver{"rt": 7000})
	tick(fakeAgeResolver{"rt": 7000})
	if got := hook.count("sess"); got != 2 {
		t.Fatalf("after the cap moved to 7000s: %d warnings, want 2", got)
	}
	// The first claim write advanced the row's updated_at by the store's
	// monotonic step, so the remaining time truncates to 99s or 100s.
	if wn := hook.warnings[1]; wn.maxSessionAge != 7000 || wn.remainingSeconds < 99 || wn.remainingSeconds > 100 {
		t.Fatalf("second warning = %+v, want maxSessionAge 7000 remaining about 100", wn)
	}
	if got := mustGet(t, store, "sess").ExpiryWarnedCapSeconds; got != 7000 {
		t.Fatalf("ExpiryWarnedCapSeconds = %d, want 7000", got)
	}
}

// spec: 11.3 (Session expiry warning), 10.1.1 (Primary:)
//
// A failed claim write sends no warning, is returned as the pass's error, and
// leaves the row unwarned, so the next pass warns it once.
func TestExpiryWarningClaimWriteFailure_spec_11_3(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	mem := memstore.NewWithClock(func() time.Time { return now })
	mustCreate(t, mem, inBandRow("sess", now))
	hook := newSyncWarnings()
	boom := errors.New("primary unavailable")
	clock := func() time.Time { return now }
	broken := watchdog.New(&failingUpdateStore{Store: mem, err: boom}, watchdog.StaticTenants{"acme"}, ageCfg(7200), clock).
		WithTerminalHook(hook)
	if _, err := broken.Tick(context.Background(), now); !errors.Is(err, boom) {
		t.Fatalf("Tick error = %v, want %v", err, boom)
	}
	if hook.total() != 0 {
		t.Fatalf("a failed claim write sent %d warnings, want 0", hook.total())
	}
	if got := mustGet(t, mem, "sess").ExpiryWarnedCapSeconds; got != 0 {
		t.Fatalf("ExpiryWarnedCapSeconds after a failed write = %d, want 0", got)
	}
	healthy := watchdog.New(mem, watchdog.StaticTenants{"acme"}, ageCfg(7200), clock).WithTerminalHook(hook)
	if _, err := healthy.Tick(context.Background(), now); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if got := hook.count("sess"); got != 1 {
		t.Fatalf("after recovery: %d warnings, want 1", got)
	}
}

// spec: 11.3 (Session expiry warning), 10.1.1 (Primary:)
//
// Every replica runs the watchdog over every row. Several watchdogs sharing
// one store and sweeping concurrently, over several passes, send exactly one
// warning per in-band session between them, and none for a session outside
// the band.
func TestExpiryWarningOncePerSessionAcrossReplicas_spec_11_3(t *testing.T) {
	seed := time.Now().UnixNano()
	t.Logf("seed %d", seed)
	rng := rand.New(rand.NewSource(seed))
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	for _, replicas := range []int{2, 3, 5} {
		t.Run(fmt.Sprintf("%d replicas", replicas), func(t *testing.T) {
			store := memstore.NewWithClock(func() time.Time { return now })
			const n = 40
			inBand := make(map[string]bool, n)
			for i := 0; i < n; i++ {
				id := fmt.Sprintf("s%d", i)
				row := inBandRow(id, now)
				if rng.Intn(4) == 0 {
					row.AccumulatedSessionAgeSeconds = 5000 // outside the window
				} else {
					inBand[id] = true
				}
				mustCreate(t, store, row)
			}
			hook := newSyncWarnings()
			var wg sync.WaitGroup
			start := make(chan struct{})
			for r := 0; r < replicas; r++ {
				w := watchdog.New(store, watchdog.StaticTenants{"acme"}, ageCfg(7200), func() time.Time { return now }).
					WithTerminalHook(hook)
				passes := 1 + rng.Intn(3)
				wg.Add(1)
				go func() {
					defer wg.Done()
					<-start
					for p := 0; p < passes; p++ {
						if _, err := w.Tick(context.Background(), now); err != nil {
							t.Errorf("Tick: %v", err)
						}
					}
				}()
			}
			close(start)
			wg.Wait()
			for i := 0; i < n; i++ {
				id := fmt.Sprintf("s%d", i)
				want := 0
				if inBand[id] {
					want = 1
				}
				if got := hook.count(id); got != want {
					t.Errorf("%s: %d warnings across %d replicas, want %d", id, got, replicas, want)
				}
			}
		})
	}
}
