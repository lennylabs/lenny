// SPDX-License-Identifier: MIT

//go:build load_local

// Tier-7a load_local coverage for the §11.3 session expiry warning under the
// watchdog running on every gateway replica. Several watchdogs sweep one
// session set at the same instant, over several passes each, while a writer
// ends or pauses some of the same rows. The warning is claimed by a
// locked-row write that records the warned deadline, so each session is
// warned at most once across every replica, a session whose terminal or
// pausing write landed before the claim is never warned, and every other
// session inside the band is warned exactly once.
//
// spec: §11.3 (Session expiry warning), §10.1.1 (Primary:), §6.2
// (maxSessionAge timer behavior across states)
package tier7a_load_local_test

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

// expiryWarningCap is the maxSessionAge cap every replica applies.
const expiryWarningCap = 7200

// warningCounter is a terminal hook and expiry-warning notifier shared by
// every replica's watchdog.
type warningCounter struct {
	mu       sync.Mutex
	warnings map[string]int
}

func (*warningCounter) BeginTerminal(string) func() { return func() {} }

func (*warningCounter) OnSessionTerminal(context.Context, session.State, sessionstore.Session) {}

func (c *warningCounter) OnSessionExpiringSoon(_ context.Context, sess sessionstore.Session, _, _ int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.warnings[sess.ID]++
}

func (c *warningCounter) count(id string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.warnings[id]
}

// expiryWarningRaceConfig applies the 7200s age cap and moves every other
// deadline out of the way.
func expiryWarningRaceConfig() watchdog.Config {
	const off = 100 * 24 * 3600
	return watchdog.Config{
		MaxSessionAgeSeconds:           expiryWarningCap,
		MaxIdleSeconds:                 off,
		MaxAwaitingClientActionSeconds: off,
		MaxSuspendedPodHoldSeconds:     off,
		MaxSuspendedSessionSeconds:     off,
		MaxResumePendingSeconds:        off,
		MaxResumingSeconds:             off,
	}
}

// interruptKind is what the concurrent writer does to a victim row.
type interruptKind int

const (
	interruptEnd interruptKind = iota
	interruptSuspend
)

// spec: §11.3 (Session expiry warning), §10.1.1 (Primary:)
//
// diagnosis: a failure means watchdogs on several replicas sent more than one
// session_expiring_soon warning for one session and deadline, sent one after
// a terminal or pausing write that landed before the warning's claim, or
// missed a session inside the band: the warning is not claimed by a
// locked-row write that re-checks the band and records the warned deadline
// (§11.3, §10.1.1 Primary:).
func TestReplicasWarnEachSessionOncePerDeadline_spec_11_3(t *testing.T) {
	seed := time.Now().UnixNano()
	t.Logf("seed %d", seed)
	rng := rand.New(rand.NewSource(seed))
	now := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	for round := 0; round < 30; round++ {
		runExpiryWarningRound(t, rng, now, round)
	}
}

// runExpiryWarningRound seeds a session set, most of it inside the warning
// band, races two to four watchdogs against a writer that ends or suspends
// a random third of the rows in a random start order, and checks the
// per-session invariants.
func runExpiryWarningRound(t *testing.T, rng *rand.Rand, now time.Time, round int) {
	t.Helper()
	ctx := context.Background()
	store := memstore.NewWithClock(func() time.Time { return now })
	const n = 50
	ids := make([]string, n)
	inBand := make([]bool, n)
	for i := range ids {
		ids[i] = fmt.Sprintf("r%d-s%d", round, i)
		age := 6900.0 // 300s left: inside the window.
		inBand[i] = rng.Intn(5) != 0
		if !inBand[i] {
			age = 5000 // outside the window.
		}
		if err := store.Create(ctx, sessionstore.Session{
			ID: ids[i], TenantID: "acme", State: session.StateRunning,
			CreatedAt: now.Add(-3 * time.Hour), UpdatedAt: now,
			LastAgentActivityAt:          now,
			AccumulatedSessionAgeSeconds: age,
		}); err != nil {
			t.Fatalf("seed %s: %v", ids[i], err)
		}
	}
	// beforeClaim records the victims whose write landed while the row
	// recorded no warning, so no claim may follow it.
	var beforeClaim sync.Map
	victims := rng.Perm(n)[:n/3]
	kinds := make(map[int]interruptKind, len(victims))
	for _, i := range victims {
		kinds[i] = interruptKind(rng.Intn(2))
	}
	hook := &warningCounter{warnings: map[string]int{}}
	clock := func() time.Time { return now }
	var passes []func()
	replicas := 2 + rng.Intn(3)
	for r := 0; r < replicas; r++ {
		w := watchdog.New(store, watchdog.StaticTenants{"acme"}, expiryWarningRaceConfig(), clock).WithTerminalHook(hook)
		ticks := 1 + rng.Intn(3)
		passes = append(passes, func() {
			for p := 0; p < ticks; p++ {
				if _, err := w.Tick(ctx, now); err != nil {
					t.Errorf("watchdog Tick: %v", err)
				}
			}
		})
	}
	passes = append(passes, func() {
		for _, i := range victims {
			interruptRow(ctx, t, store, ids[i], kinds[i], &beforeClaim)
		}
	})
	rng.Shuffle(len(passes), func(i, j int) { passes[i], passes[j] = passes[j], passes[i] })

	var wg sync.WaitGroup
	start := make(chan struct{})
	for _, pass := range passes {
		wg.Add(1)
		go func(pass func()) {
			defer wg.Done()
			<-start
			pass()
		}(pass)
	}
	close(start)
	wg.Wait()

	victim := make(map[int]bool, len(victims))
	for _, i := range victims {
		victim[i] = true
	}
	for i, id := range ids {
		row, err := store.Get(ctx, "acme", id)
		if err != nil {
			t.Fatalf("Get %s: %v", id, err)
		}
		got := hook.count(id)
		if got > 1 {
			t.Errorf("%s warned %d times across %d replicas, want at most 1", id, got, replicas)
		}
		recorded := row.ExpiryWarnedCapSeconds == expiryWarningCap
		if (got == 1) != recorded {
			t.Errorf("%s: %d warnings but ExpiryWarnedCapSeconds = %d", id, got, row.ExpiryWarnedCapSeconds)
		}
		_, early := beforeClaim.Load(id)
		switch {
		case !inBand[i] && got != 0:
			t.Errorf("%s is outside the band and was warned", id)
		case early && got != 0:
			t.Errorf("%s was warned after its %v write landed ahead of the claim", id, kinds[i])
		case inBand[i] && !victim[i] && got != 1:
			t.Errorf("%s is inside the band and was warned %d times, want 1", id, got)
		case inBand[i] && victim[i] && !early && got != 1:
			t.Errorf("%s was claimed before its %v write and was warned %d times, want 1", id, kinds[i], got)
		}
	}
}

// interruptRow ends or suspends a running row and records, when the write
// lands while the row records no warning, that no warning may follow it.
func interruptRow(ctx context.Context, t *testing.T, store sessionstore.Store, id string, kind interruptKind, beforeClaim *sync.Map) {
	t.Helper()
	var early bool
	_, err := store.Update(ctx, "acme", id, func(r *sessionstore.Session) error {
		if session.IsTerminal(r.State) {
			return sessionstore.ErrSuperseded
		}
		early = r.ExpiryWarnedCapSeconds == 0
		switch kind {
		case interruptEnd:
			r.State = session.StateCompleted
		case interruptSuspend:
			r.State = session.StateSuspended
		}
		return nil
	})
	if errors.Is(err, sessionstore.ErrSuperseded) {
		return
	}
	if err != nil {
		t.Errorf("interrupt %s: %v", id, err)
		return
	}
	if early {
		beforeClaim.Store(id, true)
	}
}

func (k interruptKind) String() string {
	if k == interruptSuspend {
		return "suspend"
	}
	return "end"
}
