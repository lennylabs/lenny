// SPDX-License-Identifier: MIT

//go:build load_local

package tier7a_load_local_test

import (
	"context"
	"fmt"
	"math/rand"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/lennylabs/lenny/pkg/gateway/storage/slotcounter"
)

const (
	rebuildRacePod     = "pod-rebuild-race"
	rebuildRaceCap     = 64
	rebuildRaceRounds  = 8
	rebuildRaceWorkers = 4
	rebuildRaceOps     = 120
)

// rebuildSource is the session-row source both Counters rehydrate from and
// list during a rebuild: a row names the pod and is terminal or not.
type rebuildSource struct {
	mu   sync.Mutex
	rows map[string]bool // session -> terminal
}

func (s *rebuildSource) set(id string, terminal bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rows[id] = terminal
}

// GetActiveSlotsByPod returns the non-terminal sessions naming the pod.
func (s *rebuildSource) GetActiveSlotsByPod(_ context.Context, pod string) ([]string, error) {
	if pod != rebuildRacePod {
		return nil, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []string
	for id, terminal := range s.rows {
		if !terminal {
			out = append(out, id)
		}
	}
	return out, nil
}

// ListPodsWithActiveSlots names the pod while a non-terminal row names it.
func (s *rebuildSource) ListPodsWithActiveSlots(ctx context.Context) ([]string, error) {
	if ids, _ := s.GetActiveSlotsByPod(ctx, rebuildRacePod); len(ids) > 0 {
		return []string{rebuildRacePod}, nil
	}
	return nil, nil
}

// rebuildFallback is the Postgres capacity gate during the outage: it admits
// under one lock while the pod's count is under the cap.
type rebuildFallback struct {
	mu    sync.Mutex
	count int32
}

func (f *rebuildFallback) ReserveSlotUnderLock(_ context.Context, _ string, maxConcurrent int32) (int32, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.count >= maxConcurrent {
		return f.count, false, nil
	}
	f.count++
	return f.count, true, nil
}

// rebuildRace is one round: two replicas' Counters on one miniredis that a
// toggler stops and restarts, workers admitting and releasing on both, and
// samplers checking the invariants while the operations overlap.
type rebuildRace struct {
	t        *testing.T
	mr       *miniredis.Miniredis
	src      *rebuildSource
	replicas [2]*slotcounter.Counter
	reader   *redis.Client

	next       atomic.Int64
	mu         sync.Mutex
	sessions   []string
	releasing  map[string]bool
	leaked     []string
	violations []string
}

// fastRedis returns a client with retries off and a short dial timeout, so
// an operation against the stopped server fails at once.
func fastRedis(t *testing.T, addr string) *redis.Client {
	cl := redis.NewClient(&redis.Options{
		Addr: addr, MaxRetries: -1, DialTimeout: 100 * time.Millisecond, ReadTimeout: 200 * time.Millisecond,
		DialerRetries: 1, DialerRetryTimeout: time.Millisecond,
	})
	t.Cleanup(func() { _ = cl.Close() })
	return cl
}

// scriptLatency delays the reply of every Lua script a Counter runs by up
// to 1.5ms, as a network round trip does. The delay widens the windows the
// interleavings target, such as the one between a rebuild's script reply and
// the moment it clears the outage clock, during which another operation can
// record a ledger entry.
type scriptLatency struct{}

func (scriptLatency) DialHook(next redis.DialHook) redis.DialHook { return next }

func (scriptLatency) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return next
}

func (scriptLatency) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		err := next(ctx, cmd)
		if name := strings.ToLower(cmd.Name()); name == "eval" || name == "evalsha" {
			time.Sleep(time.Duration(rand.Intn(1500)) * time.Microsecond)
		}
		return err
	}
}

func newRebuildRace(t *testing.T) *rebuildRace {
	t.Helper()
	mr := miniredis.RunT(t)
	if err := mr.Set("lenny:pod:{"+rebuildRacePod+"}:rehydrated", "1"); err != nil {
		t.Fatal(err)
	}
	r := &rebuildRace{t: t, mr: mr, src: &rebuildSource{rows: map[string]bool{}}, reader: fastRedis(t, mr.Addr()), releasing: map[string]bool{}}
	fb := &rebuildFallback{}
	for i := range r.replicas {
		cl := fastRedis(t, mr.Addr())
		cl.AddHook(scriptLatency{})
		r.replicas[i] = slotcounter.New(cl,
			slotcounter.WithSlotSource(r.src), slotcounter.WithFallbackSource(fb),
			slotcounter.WithRehydrationTimeout(50*time.Millisecond),
			slotcounter.WithRecoveryProbeInterval(2*time.Millisecond),
			slotcounter.WithFallbackMaxWindow(time.Hour))
	}
	return r
}

func (r *rebuildRace) violate(format string, args ...any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.violations = append(r.violations, fmt.Sprintf(format, args...))
}

// members reads the pod's set, reporting false while Redis is stopped.
func (r *rebuildRace) members(ctx context.Context) (map[string]bool, bool) {
	ms, err := r.reader.SMembers(ctx, "lenny:pod:{"+rebuildRacePod+"}:active_slots").Result()
	if err != nil {
		return nil, false
	}
	out := make(map[string]bool, len(ms))
	for _, m := range ms {
		out[m] = true
	}
	return out, true
}

// work runs one worker's random operations: a fallback-capable admission
// that may write the admitted session's row, a release (leaked or not) of a
// known session, or a terminal write of a known session's row.
func (r *rebuildRace) work(ctx context.Context, seed int64) {
	rng := rand.New(rand.NewSource(seed))
	for i := 0; i < rebuildRaceOps; i++ {
		r.op(ctx, rng)
		time.Sleep(time.Duration(rng.Intn(300)) * time.Microsecond)
	}
}

// op runs one random operation under a bound, as every gateway caller's
// context carries one.
func (r *rebuildRace) op(parent context.Context, rng *rand.Rand) {
	ctx, cancel := context.WithTimeout(parent, time.Second)
	defer cancel()
	c := r.replicas[rng.Intn(2)]
	switch op := rng.Intn(10); {
	case op < 4:
		id := fmt.Sprintf("sess-%d", r.next.Add(1))
		if _, _, err := c.Reserve(ctx, rebuildRacePod, id, rebuildRaceCap); err == nil {
			r.mu.Lock()
			r.sessions = append(r.sessions, id)
			r.mu.Unlock()
			if rng.Intn(2) == 0 {
				r.src.set(id, false)
			}
		}
	case op < 7:
		if id, ok := r.pick(rng, true); ok {
			leaked := rng.Intn(2) == 0
			if _, _, _, err := c.Release(ctx, rebuildRacePod, id, leaked); err != nil && leaked {
				r.mu.Lock()
				r.leaked = append(r.leaked, id)
				r.mu.Unlock()
			}
		}
	default:
		if id, ok := r.pick(rng, false); ok {
			r.src.set(id, true)
		}
	}
}

// pick returns a known session; a release marks it before its call, so the
// set sampler accepts its removal.
func (r *rebuildRace) pick(rng *rand.Rand, release bool) (string, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.sessions) == 0 {
		return "", false
	}
	id := r.sessions[rng.Intn(len(r.sessions))]
	if release {
		r.releasing[id] = true
	}
	return id, true
}

// toggle stops and restarts Redis at random short intervals and leaves it
// running.
func (r *rebuildRace) toggle(seed int64, stop <-chan struct{}) {
	rng := rand.New(rand.NewSource(seed))
	for {
		select {
		case <-stop:
			return
		case <-time.After(time.Duration(2+rng.Intn(8)) * time.Millisecond):
		}
		r.mr.Close()
		time.Sleep(time.Duration(1+rng.Intn(6)) * time.Millisecond)
		r.restart()
	}
}

// restart brings Redis back on its address, retrying while the port is
// still being released.
func (r *rebuildRace) restart() {
	var err error
	for i := 0; i < 50; i++ {
		if err = r.mr.Restart(); err == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	r.violate("restart Redis: %v", err)
}

// sampleSet watches the pod's set: a member leaves it only through a
// release of that session, and a leaked: member never leaves it. A rebuild
// or a rehydration seed that removed a member shows as a member gone with
// no release issued for it.
func (r *rebuildRace) sampleSet(ctx context.Context, stop <-chan struct{}) {
	seen := map[string]bool{}
	for {
		select {
		case <-stop:
			return
		default:
		}
		rctx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
		cur, ok := r.members(rctx)
		cancel()
		if !ok {
			continue
		}
		r.mu.Lock()
		for m := range seen {
			if cur[m] {
				continue
			}
			if strings.HasPrefix(m, slotcounter.LeakedMember("")) || !r.releasing[m] {
				r.violations = append(r.violations, fmt.Sprintf("member %s left the set with no release of it", m))
			}
		}
		r.mu.Unlock()
		seen = cur
	}
}

// sampleClock watches each Counter's recovery state. A read that finds the
// outage clock cleared while the ledger still holds an entry that an
// earlier read, taken with the clock set, already saw (an entry whose
// sequence number is at or below that read's) means a rebuild cleared the
// clock while that entry was in the ledger. An entry recorded after a clear
// carries a later sequence number, so a fallback admission or a failed
// release that records after a legitimate clear is not flagged.
func (r *rebuildRace) sampleClock(stop <-chan struct{}) {
	prev := [2]slotcounter.OutageState{}
	for {
		select {
		case <-stop:
			return
		default:
		}
		for i, c := range r.replicas {
			cur := c.OutageStateForLoadTest()
			if !prev[i].Since.IsZero() && cur.Since.IsZero() && cur.Entries > 0 && cur.OldestSeq <= prev[i].Seq {
				r.violate("replica %d cleared its outage clock while the ledger held an entry recorded before the clear (%d entries)", i, cur.Entries)
			}
			prev[i] = cur
		}
		time.Sleep(20 * time.Microsecond)
	}
}

// awaitRecovered waits until both Counters' rebuilds have drained the
// ledger and cleared the clock with Redis running.
func (r *rebuildRace) awaitRecovered() {
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		a, b := r.replicas[0].OutageStateForLoadTest(), r.replicas[1].OutageStateForLoadTest()
		if a.Since.IsZero() && a.Entries == 0 && b.Since.IsZero() && b.Entries == 0 {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	r.t.Fatalf("the Counters did not recover within 10s: %+v %+v", r.replicas[0].OutageStateForLoadTest(), r.replicas[1].OutageStateForLoadTest())
}

// spec: §12.4 (Redis failure behavior), §5.2 (Post-recovery rehydration
// atomicity), §6.2 (leaked slot semantics)
// diagnosis: two replicas' slot counters, driven concurrently through a
// flapping Redis by fallback admissions, failing releases, terminal writes,
// and recovery rebuilds on both the call path and the recovery probe, broke
// the add-only rebuild: a rebuild cleared the outage clock while ledger
// entries recorded before it remained, a rebuild or rehydration seed removed
// a member no release named, or a failed leaked release's leaked: member was
// missing once Redis stayed up and each Counter's rebuild ran.
func TestConcurrentRecoveryRebuildsKeepTheLedgerAndRemoveNoMember_spec_12_4(t *testing.T) {
	seed := time.Now().UnixNano()
	t.Logf("seed %d", seed)
	for round := 0; round < rebuildRaceRounds; round++ {
		r := newRebuildRace(t)
		ctx, cancel := context.WithCancel(context.Background())
		var probes sync.WaitGroup
		for _, c := range r.replicas {
			probes.Add(1)
			go func(c *slotcounter.Counter) { defer probes.Done(); c.RunRecovery(ctx) }(c)
		}
		stop := make(chan struct{})
		var bg sync.WaitGroup
		for _, fn := range []func(){
			func() { r.toggle(seed+int64(round), stop) },
			func() { r.sampleSet(ctx, stop) },
			func() { r.sampleClock(stop) },
		} {
			bg.Add(1)
			go func(fn func()) { defer bg.Done(); fn() }(fn)
		}
		var workers sync.WaitGroup
		for w := 0; w < rebuildRaceWorkers; w++ {
			workers.Add(1)
			go func(w int) { defer workers.Done(); r.work(ctx, seed+int64(round*100+w)) }(w)
		}
		workers.Wait()
		close(stop)
		bg.Wait()
		r.awaitRecovered()
		cancel()
		probes.Wait()
		r.checkFinal(t, round, seed)
	}
}

// checkFinal reports the sampled violations and checks that every failed
// leaked release's leaked: member is in the set after the last rebuilds.
func (r *rebuildRace) checkFinal(t *testing.T, round int, seed int64) {
	t.Helper()
	if len(r.violations) != 0 {
		t.Fatalf("round %d (seed %d): %d violations, first: %s", round, seed, len(r.violations), r.violations[0])
	}
	final, ok := r.members(context.Background())
	if !ok {
		t.Fatalf("round %d: read the set with Redis running", round)
	}
	for _, id := range r.leaked {
		if !final[slotcounter.LeakedMember(id)] {
			t.Fatalf("round %d (seed %d): the failed leaked release of %s left no leaked member after the last rebuilds", round, seed, id)
		}
	}
	t.Logf("round %d: %d sessions, %d failed leaked releases, %d members at the end", round, len(r.sessions), len(r.leaked), len(final))
}
