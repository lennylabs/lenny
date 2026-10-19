// SPDX-License-Identifier: MIT

package slotcounter_test

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/lennylabs/lenny/pkg/gateway/storage/slotcounter"
)

func newCounter(t *testing.T) *slotcounter.Counter {
	t.Helper()
	c, _, _ := newCounterNoSource(t)
	return c
}

// newCounterNoSource returns a Counter with no SlotSource and its backing
// miniredis and client.
func newCounterNoSource(t *testing.T, opts ...slotcounter.Option) (*slotcounter.Counter, *miniredis.Miniredis, *redis.Client) {
	t.Helper()
	mr := miniredis.RunT(t)
	cl := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = cl.Close() })
	return slotcounter.New(cl, opts...), mr, cl
}

// fakeRow is one session row of fakeSource: the pod its pod_assignment
// names and whether its state is terminal.
type fakeRow struct {
	pod      string
	terminal bool
}

// fakeSource is a test SlotSource over a table of session rows. It applies
// the slot-holder predicate: a session holds a slot on pod P exactly when its
// row is non-terminal and names P. It records per-pod seed calls and listing
// calls, and returns a configured error.
type fakeSource struct {
	mu        sync.Mutex
	rows      map[string]fakeRow
	calls     map[string]int
	err       error
	listErr   error
	listCalls int
	onList    func()
}

func newFakeSource() *fakeSource {
	return &fakeSource{rows: map[string]fakeRow{}, calls: map[string]int{}}
}

// set writes the row of session id.
func (f *fakeSource) set(id, pod string, terminal bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rows[id] = fakeRow{pod: pod, terminal: terminal}
}

// holders adds n live rows named <pod>-live-<i> naming pod.
func (f *fakeSource) holders(pod string, n int) {
	for i := 0; i < n; i++ {
		f.set(fmt.Sprintf("%s-live-%d", pod, i), pod, false)
	}
}

func (f *fakeSource) setErr(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.err = err
	f.listErr = err
}

func (f *fakeSource) GetActiveSlotsByPod(_ context.Context, podID string) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls[podID]++
	if f.err != nil {
		return nil, f.err
	}
	var ids []string
	for id, row := range f.rows {
		if row.pod == podID && !row.terminal {
			ids = append(ids, id)
		}
	}
	return ids, nil
}

func (f *fakeSource) ListPodsWithActiveSlots(_ context.Context) ([]string, error) {
	f.mu.Lock()
	f.listCalls++
	hook := f.onList
	listErr := f.listErr
	seen := map[string]bool{}
	for _, row := range f.rows {
		if row.pod != "" && !row.terminal {
			seen[row.pod] = true
		}
	}
	f.mu.Unlock()
	if hook != nil {
		hook()
	}
	if listErr != nil {
		return nil, listErr
	}
	pods := make([]string, 0, len(seen))
	for pod := range seen {
		pods = append(pods, pod)
	}
	return pods, nil
}

func (f *fakeSource) callCount(podID string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[podID]
}

func (f *fakeSource) listCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.listCalls
}

// newCounterWithSource returns a Counter wired to src and the backing
// miniredis so a test can simulate a Redis restart or outage.
func newCounterWithSource(t *testing.T, src slotcounter.SlotSource, opts ...slotcounter.Option) (*slotcounter.Counter, *miniredis.Miniredis, *redis.Client) {
	t.Helper()
	all := append([]slotcounter.Option{slotcounter.WithSlotSource(src)}, opts...)
	return newCounterNoSource(t, all...)
}

// members returns the sorted members of pod's slot set.
func members(t *testing.T, cl *redis.Client, pod string) []string {
	t.Helper()
	got, err := cl.SMembers(context.Background(), "lenny:pod:{"+pod+"}:active_slots").Result()
	if err != nil {
		t.Fatalf("SMEMBERS %s: %v", pod, err)
	}
	sort.Strings(got)
	return got
}

// flagSet reports whether pod's rehydrated flag exists.
func flagSet(t *testing.T, cl *redis.Client, pod string) bool {
	t.Helper()
	n, err := cl.Exists(context.Background(), "lenny:pod:{"+pod+"}:rehydrated").Result()
	if err != nil {
		t.Fatalf("EXISTS flag %s: %v", pod, err)
	}
	return n == 1
}

func wantMembers(t *testing.T, cl *redis.Client, pod string, want ...string) {
	t.Helper()
	sort.Strings(want)
	got := members(t, cl, pod)
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("members of %s = %v, want %v", pod, got, want)
	}
}

// slotOp is one step of the keyed slot table: a reservation, a clean
// release, or a leaked release of session id.
type slotOp struct {
	op          string // "reserve", "release", "leak"
	id          string
	cap         int32
	wantErr     error
	wantCount   int32
	wantRemoved bool
	wantRemain  int32
}

// TestSlotSetKeyedReleaseTable_spec_5_2 drives reservation and release
// sequences for one and two sessions and asserts the returned removed and
// remaining values and the resulting set members against the keyed-release
// rules.
//
// spec: §5.2 (Slot assignment atomicity), §6.2 (leaked slot semantics)
func TestSlotSetKeyedReleaseTable_spec_5_2(t *testing.T) {
	cases := []struct {
		name string
		ops  []slotOp
		want []string
	}{
		{"reserve then clean release empties the set", []slotOp{
			{op: "reserve", id: "s1", cap: 2, wantCount: 1},
			{op: "release", id: "s1", wantRemoved: true, wantRemain: 0},
		}, nil},
		{"a duplicate clean release removes nothing", []slotOp{
			{op: "reserve", id: "s1", cap: 2, wantCount: 1},
			{op: "release", id: "s1", wantRemoved: true, wantRemain: 0},
			{op: "release", id: "s1", wantRemoved: false, wantRemain: 0},
		}, nil},
		{"clean then leaked release of one session leaves one leaked member", []slotOp{
			{op: "reserve", id: "s1", cap: 2, wantCount: 1},
			{op: "release", id: "s1", wantRemoved: true, wantRemain: 0},
			{op: "leak", id: "s1", wantRemoved: false, wantRemain: 1},
		}, []string{"leaked:s1"}},
		{"a second leaked release changes nothing", []slotOp{
			{op: "reserve", id: "s1", cap: 2, wantCount: 1},
			{op: "leak", id: "s1", wantRemoved: true, wantRemain: 1},
			{op: "leak", id: "s1", wantRemoved: false, wantRemain: 1},
		}, []string{"leaked:s1"}},
		{"a repeated reservation of a present member adds nothing, at the cap too", []slotOp{
			{op: "reserve", id: "s1", cap: 2, wantCount: 1},
			{op: "reserve", id: "s2", cap: 2, wantCount: 2},
			{op: "reserve", id: "s1", cap: 2, wantCount: 2},
			{op: "reserve", id: "s2", cap: 2, wantCount: 2},
		}, []string{"s1", "s2"}},
		{"a reservation at the cap is exhausted", []slotOp{
			{op: "reserve", id: "s1", cap: 1, wantCount: 1},
			{op: "reserve", id: "s2", cap: 1, wantErr: slotcounter.ErrSlotsExhausted},
		}, []string{"s1"}},
		{"a sibling keeps the set non-empty", []slotOp{
			{op: "reserve", id: "s1", cap: 2, wantCount: 1},
			{op: "reserve", id: "s2", cap: 2, wantCount: 2},
			{op: "release", id: "s1", wantRemoved: true, wantRemain: 1},
			{op: "release", id: "s1", wantRemoved: false, wantRemain: 1},
		}, []string{"s2"}},
		{"a leaked member counts toward the cap", []slotOp{
			{op: "reserve", id: "s1", cap: 2, wantCount: 1},
			{op: "leak", id: "s1", wantRemoved: true, wantRemain: 1},
			{op: "reserve", id: "s2", cap: 2, wantCount: 2},
			{op: "reserve", id: "s3", cap: 2, wantErr: slotcounter.ErrSlotsExhausted},
		}, []string{"leaked:s1", "s2"}},
		{"a release of a never-reserved session on a seeded pod removes nothing", []slotOp{
			{op: "reserve", id: "s1", cap: 2, wantCount: 1},
			{op: "release", id: "s9", wantRemoved: false, wantRemain: 1},
		}, []string{"s1"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, _, cl := newCounterNoSource(t)
			ctx := context.Background()
			for i, op := range tc.ops {
				switch op.op {
				case "reserve":
					got, _, err := c.Reserve(ctx, "pod", op.id, op.cap)
					if op.wantErr != nil {
						if !errors.Is(err, op.wantErr) {
							t.Fatalf("step %d reserve %s: err %v, want %v", i, op.id, err, op.wantErr)
						}
						continue
					}
					if err != nil || got != op.wantCount {
						t.Fatalf("step %d reserve %s = (%d, %v), want %d", i, op.id, got, err, op.wantCount)
					}
				case "release", "leak":
					removed, remain, _, err := c.Release(ctx, "pod", op.id, op.op == "leak")
					if err != nil {
						t.Fatalf("step %d %s %s: %v", i, op.op, op.id, err)
					}
					if removed != op.wantRemoved || remain != op.wantRemain {
						t.Fatalf("step %d %s %s = (removed %v, remaining %d), want (%v, %d)",
							i, op.op, op.id, removed, remain, op.wantRemoved, op.wantRemain)
					}
				}
			}
			wantMembers(t, cl, "pod", tc.want...)
		})
	}
}

// TestEmptySessionIDRunsNoScript_spec_5_2 pins that a reservation or release
// without a session identifier is an error and touches no key, so a keyed
// release with no key cannot report a false zero.
//
// spec: §5.2 (Slot assignment atomicity)
func TestEmptySessionIDRunsNoScript_spec_5_2(t *testing.T) {
	c, mr, _ := newCounterNoSource(t)
	ctx := context.Background()
	if _, _, err := c.Reserve(ctx, "pod", "", 2); err == nil {
		t.Fatal("Reserve with an empty session identifier must fail")
	}
	if _, _, _, err := c.Release(ctx, "pod", "", false); err == nil {
		t.Fatal("Release with an empty session identifier must fail")
	}
	if keys := mr.Keys(); len(keys) != 0 {
		t.Fatalf("keys after empty-identifier calls = %v, want none (no script ran)", keys)
	}
}

// TestReleaseRehydratesWithOwnMember_spec_5_2 pins the release arm of the
// §5.2 rehydration: a release that finds the rehydrated flag absent adds the
// source's identifiers and its own member to the members already present,
// removes its own member, and reports removed, rehydrated, and the remaining
// cardinality.
//
// spec: §5.2 (Post-recovery rehydration atomicity)
func TestReleaseRehydratesWithOwnMember_spec_5_2(t *testing.T) {
	src := newFakeSource()
	src.set("live-1", "pod", false)
	src.set("ended", "pod", true)
	c, _, cl := newCounterWithSource(t, src)
	ctx := context.Background()
	if err := cl.SAdd(ctx, "lenny:pod:{pod}:active_slots", "leaked:old").Err(); err != nil {
		t.Fatal(err)
	}
	removed, remain, reh, err := c.Release(ctx, "pod", "ended", false)
	if err != nil {
		t.Fatalf("Release: %v", err)
	}
	if !removed || !reh || remain != 2 {
		t.Fatalf("Release = (removed %v, remaining %d, rehydrated %v), want (true, 2, true)", removed, remain, reh)
	}
	wantMembers(t, cl, "pod", "leaked:old", "live-1")
	if !flagSet(t, cl, "pod") {
		t.Fatal("the release's rehydration must set the flag")
	}
}

// TestReserveIsAtomicUnderRace pins that racing reservers for distinct
// sessions never exceed the cap. Run under -race.
//
// spec: §5.2 (Slot assignment atomicity)
func TestReserveIsAtomicUnderRace(t *testing.T) {
	c, _, cl := newCounterNoSource(t)
	ctx := context.Background()
	const n = 50
	const cap = int32(10)
	var wg sync.WaitGroup
	var successes, exhausted int64
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, _, err := c.Reserve(ctx, "pod-r", fmt.Sprintf("s%d", i), cap)
			switch {
			case err == nil:
				atomic.AddInt64(&successes, 1)
			case errors.Is(err, slotcounter.ErrSlotsExhausted):
				atomic.AddInt64(&exhausted, 1)
			default:
				t.Errorf("unexpected error: %v", err)
			}
		}(i)
	}
	wg.Wait()
	if successes != int64(cap) || exhausted != int64(n)-int64(cap) {
		t.Errorf("successes %d exhausted %d, want %d and %d", successes, exhausted, cap, n-int(cap))
	}
	if got := len(members(t, cl, "pod-r")); got != int(cap) {
		t.Errorf("set cardinality %d, want %d", got, cap)
	}
}

// TestReserveScopesPerPod pins that different pods have independent sets.
//
// spec: §5.2 (Slot assignment atomicity)
func TestReserveScopesPerPod(t *testing.T) {
	c := newCounter(t)
	ctx := context.Background()
	if _, _, err := c.Reserve(ctx, "pod-x", "s1", 1); err != nil {
		t.Fatalf("Reserve pod-x: %v", err)
	}
	if _, _, err := c.Reserve(ctx, "pod-x", "s2", 1); !errors.Is(err, slotcounter.ErrSlotsExhausted) {
		t.Errorf("second Reserve on pod-x = %v, want ErrSlotsExhausted", err)
	}
	if _, _, err := c.Reserve(ctx, "pod-y", "s2", 1); err != nil {
		t.Errorf("Reserve pod-y must succeed independently of pod-x: %v", err)
	}
}

// TestResetClearsSet_spec_5_2 pins that Reset deletes the pod's set and
// flag, so the next reservation rehydrates from the source identifiers.
//
// spec: §5.2 (Post-recovery rehydration atomicity)
func TestResetClearsSet_spec_5_2(t *testing.T) {
	src := newFakeSource()
	c, _, cl := newCounterWithSource(t, src)
	ctx := context.Background()
	if _, reh, err := c.Reserve(ctx, "pod-z", "s1", 2); err != nil || !reh {
		t.Fatalf("Reserve = (_, %v, %v), want rehydrated", reh, err)
	}
	if err := c.Reset(ctx, "pod-z"); err != nil {
		t.Fatalf("Reset: %v", err)
	}
	wantMembers(t, cl, "pod-z")
	src.set("s1", "pod-z", false)
	got, reh, err := c.Reserve(ctx, "pod-z", "s2", 2)
	if err != nil || !reh || got != 2 {
		t.Fatalf("Reserve after Reset = (%d, %v, %v), want (2, true, nil)", got, reh, err)
	}
	wantMembers(t, cl, "pod-z", "s1", "s2")
}

// TestReserveRehydratesFromSource_spec_5_2_521 pins that the first
// reservation on a pod whose flag is absent seeds the set from the source's
// identifiers before reserving, and that a second reservation does not
// re-query the source.
//
// spec: §5.2 (Post-recovery rehydration atomicity)
func TestReserveRehydratesFromSource_spec_5_2_521(t *testing.T) {
	src := newFakeSource()
	src.holders("pod-h", 2)
	c, _, cl := newCounterWithSource(t, src)
	ctx := context.Background()

	got, rehydrated, err := c.Reserve(ctx, "pod-h", "new-1", 5)
	if err != nil || got != 3 || !rehydrated {
		t.Fatalf("first Reserve = (%d, %v, %v), want (3, true, nil)", got, rehydrated, err)
	}
	if !flagSet(t, cl, "pod-h") {
		t.Error("rehydrated flag must be set after rehydration")
	}
	got, rehydrated, err = c.Reserve(ctx, "pod-h", "new-2", 5)
	if err != nil || got != 4 || rehydrated {
		t.Fatalf("second Reserve = (%d, %v, %v), want (4, false, nil)", got, rehydrated, err)
	}
	if n := src.callCount("pod-h"); n != 1 {
		t.Errorf("source queried %d times, want 1", n)
	}
}

// TestReserveRehydratesToCapThenExhausted pins that a seed reaching the cap
// refuses the reservation and still reports the rehydration.
//
// spec: §5.2 (Post-recovery rehydration atomicity)
func TestReserveRehydratesToCapThenExhausted(t *testing.T) {
	src := newFakeSource()
	src.holders("pod-f", 3)
	c, _, _ := newCounterWithSource(t, src)
	_, reh, err := c.Reserve(context.Background(), "pod-f", "new", 3)
	if !errors.Is(err, slotcounter.ErrSlotsExhausted) || !reh {
		t.Errorf("Reserve = (_, %v, %v), want (_, true, ErrSlotsExhausted)", reh, err)
	}
}

// TestRehydrationKeepsExistingMembers_spec_5_2 pins that the rehydration
// adds the source's identifiers and removes no member: a leaked: member and
// a reservation whose row does not yet name the pod stay.
//
// spec: §5.2 (Post-recovery rehydration atomicity)
func TestRehydrationKeepsExistingMembers_spec_5_2(t *testing.T) {
	src := newFakeSource()
	src.set("row-1", "pod", false)
	c, _, cl := newCounterWithSource(t, src)
	ctx := context.Background()
	if err := cl.SAdd(ctx, "lenny:pod:{pod}:active_slots", "leaked:x", "creating").Err(); err != nil {
		t.Fatal(err)
	}
	if _, _, err := c.Reserve(ctx, "pod", "new", 8); err != nil {
		t.Fatalf("Reserve: %v", err)
	}
	wantMembers(t, cl, "pod", "creating", "leaked:x", "new", "row-1")
}

// TestReserveRehydrationIsPerPodScoped pins that each pod seeds once, with
// no global lock.
//
// spec: §5.2 (Post-recovery rehydration atomicity)
func TestReserveRehydrationIsPerPodScoped(t *testing.T) {
	src := newFakeSource()
	src.holders("pod-1", 1)
	c, _, _ := newCounterWithSource(t, src)
	ctx := context.Background()
	for _, pod := range []string{"pod-1", "pod-2"} {
		if _, reh, err := c.Reserve(ctx, pod, "s", 4); err != nil || !reh {
			t.Fatalf("Reserve %s = (_, %v, %v), want (_, true, nil)", pod, reh, err)
		}
		if n := src.callCount(pod); n != 1 {
			t.Errorf("%s source calls = %d, want 1", pod, n)
		}
	}
}

// TestReserveRehydrationConcurrentSamePod pins that concurrent reservations
// on an un-rehydrated pod seed once and never exceed the cap. Run under
// -race.
//
// spec: §5.2 (Post-recovery rehydration atomicity)
func TestReserveRehydrationConcurrentSamePod(t *testing.T) {
	src := newFakeSource()
	c, _, _ := newCounterWithSource(t, src)
	ctx := context.Background()
	const n = 40
	const cap = int32(8)
	var wg sync.WaitGroup
	var successes, exhausted, rehydrations int64
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, reh, err := c.Reserve(ctx, "pod-c", fmt.Sprintf("s%d", i), cap)
			if reh {
				atomic.AddInt64(&rehydrations, 1)
			}
			switch {
			case err == nil:
				atomic.AddInt64(&successes, 1)
			case errors.Is(err, slotcounter.ErrSlotsExhausted):
				atomic.AddInt64(&exhausted, 1)
			default:
				t.Errorf("unexpected error: %v", err)
			}
		}(i)
	}
	wg.Wait()
	if successes != int64(cap) || exhausted != int64(n)-int64(cap) || rehydrations != 1 {
		t.Errorf("successes %d exhausted %d rehydrations %d, want %d, %d, 1", successes, exhausted, rehydrations, cap, n-int(cap))
	}
	if got := src.callCount("pod-c"); got != 1 {
		t.Errorf("source queried %d times, want exactly 1", got)
	}
}

// TestReserveRehydrationCrossReplicaSeedsOnce pins that two replicas racing
// to rehydrate one pod query the source once (SET NX lock). Run under -race.
//
// spec: §5.2 (Post-recovery rehydration atomicity)
func TestReserveRehydrationCrossReplicaSeedsOnce(t *testing.T) {
	src := newFakeSource()
	mr := miniredis.RunT(t)
	newReplica := func() *slotcounter.Counter {
		cl := redis.NewClient(&redis.Options{Addr: mr.Addr()})
		t.Cleanup(func() { _ = cl.Close() })
		return slotcounter.New(cl, slotcounter.WithSlotSource(src))
	}
	a, b := newReplica(), newReplica()
	ctx := context.Background()
	var wg sync.WaitGroup
	var rehydrations int64
	reserve := func(c *slotcounter.Counter, id string) {
		defer wg.Done()
		_, reh, err := c.Reserve(ctx, "pod-x", id, 4)
		if err != nil {
			t.Errorf("Reserve: %v", err)
		}
		if reh {
			atomic.AddInt64(&rehydrations, 1)
		}
	}
	wg.Add(2)
	go reserve(a, "sa")
	go reserve(b, "sb")
	wg.Wait()
	if got := src.callCount("pod-x"); got != 1 || rehydrations != 1 {
		t.Errorf("source calls %d rehydrations %d, want 1 and 1", got, rehydrations)
	}
}

// TestReserveRehydrationSourceErrorRetries pins that a source error leaves
// the flag unset so the next reservation retries the seed.
//
// spec: §5.2 (Post-recovery rehydration atomicity)
func TestReserveRehydrationSourceErrorRetries(t *testing.T) {
	src := newFakeSource()
	src.err = errors.New("postgres unavailable")
	c, _, cl := newCounterWithSource(t, src)
	ctx := context.Background()
	if _, _, err := c.Reserve(ctx, "pod-e", "s1", 3); err == nil {
		t.Fatal("Reserve must fail when the source errors")
	}
	if flagSet(t, cl, "pod-e") {
		t.Error("rehydrated flag must not be set on source error")
	}
	src.mu.Lock()
	src.err = nil
	src.mu.Unlock()
	src.set("live", "pod-e", false)
	got, reh, err := c.Reserve(ctx, "pod-e", "s1", 3)
	if err != nil || got != 2 || !reh {
		t.Fatalf("Reserve after recovery = (%d, %v, %v), want (2, true, nil)", got, reh, err)
	}
}

// TestReserveNilSourceSeedsEmpty pins that with no SlotSource the seed adds
// no member and is not a rehydration event.
//
// spec: §5.2 (Post-recovery rehydration atomicity)
func TestReserveNilSourceSeedsEmpty(t *testing.T) {
	c := newCounter(t)
	got, reh, err := c.Reserve(context.Background(), "pod-n", "s1", 2)
	if err != nil || got != 1 || reh {
		t.Fatalf("Reserve = (%d, %v, %v), want (1, false, nil)", got, reh, err)
	}
}

// TestReserveRehydratesAfterRedisRestart pins that after a restart that loses
// every key the next reservation re-seeds from the rows before admitting.
//
// spec: §5.2 (Post-recovery rehydration atomicity)
func TestReserveRehydratesAfterRedisRestart(t *testing.T) {
	src := newFakeSource()
	c, _, cl := newCounterWithSource(t, src)
	ctx := context.Background()
	for _, id := range []string{"a", "b"} {
		if _, _, err := c.Reserve(ctx, "pod-rr", id, 4); err != nil {
			t.Fatalf("pre-restart Reserve %s: %v", id, err)
		}
		src.set(id, "pod-rr", false)
	}
	if err := cl.FlushAll(ctx).Err(); err != nil {
		t.Fatalf("flush: %v", err)
	}
	got, reh, err := c.Reserve(ctx, "pod-rr", "c", 4)
	if err != nil || got != 3 || !reh {
		t.Fatalf("post-restart Reserve = (%d, %v, %v), want (3, true, nil)", got, reh, err)
	}
	wantMembers(t, cl, "pod-rr", "a", "b", "c")
}

// TestReserveStallsWhenLockHeldAndFlagNeverSet pins the bounded spin-wait
// against a peer's lock that never sets the flag.
//
// spec: §5.2 (Post-recovery rehydration atomicity)
func TestReserveStallsWhenLockHeldAndFlagNeverSet(t *testing.T) {
	src := newFakeSource()
	c, _, cl := newCounterWithSource(t, src, slotcounter.WithRehydrationTimeout(40*time.Millisecond))
	ctx := context.Background()
	if err := cl.Set(ctx, "lenny:pod:{pod-s}:rehydrating", "peer-token", 0).Err(); err != nil {
		t.Fatalf("hold lock: %v", err)
	}
	if _, _, err := c.Reserve(ctx, "pod-s", "s1", 2); !errors.Is(err, slotcounter.ErrRehydrationStalled) {
		t.Errorf("Reserve = %v, want ErrRehydrationStalled", err)
	}
	if _, _, _, err := c.Release(ctx, "pod-s", "s1", false); !errors.Is(err, slotcounter.ErrRehydrationStalled) {
		t.Errorf("Release = %v, want ErrRehydrationStalled", err)
	}
	if n := src.callCount("pod-s"); n != 0 {
		t.Errorf("source queried %d times, want 0 (peer holds the lock)", n)
	}
}

// TestReserveWaitsForPeerFlagThenSucceeds pins that a waiter resumes once the
// peer's rehydration sets the flag.
//
// spec: §5.2 (Post-recovery rehydration atomicity)
func TestReserveWaitsForPeerFlagThenSucceeds(t *testing.T) {
	src := newFakeSource()
	c, _, cl := newCounterWithSource(t, src, slotcounter.WithRehydrationTimeout(2*time.Second))
	ctx := context.Background()
	if err := cl.Set(ctx, "lenny:pod:{pod-w}:rehydrating", "peer-token", 0).Err(); err != nil {
		t.Fatalf("hold lock: %v", err)
	}
	go func() {
		time.Sleep(40 * time.Millisecond)
		_ = cl.SAdd(ctx, "lenny:pod:{pod-w}:active_slots", "peer-seeded").Err()
		_ = cl.Set(ctx, "lenny:pod:{pod-w}:rehydrated", "1", 0).Err()
	}()
	got, reh, err := c.Reserve(ctx, "pod-w", "s1", 3)
	if err != nil || got != 2 || reh {
		t.Fatalf("Reserve = (%d, %v, %v), want (2, false, nil)", got, reh, err)
	}
	if n := src.callCount("pod-w"); n != 0 {
		t.Errorf("source queried %d times, want 0 (peer seeded)", n)
	}
}
