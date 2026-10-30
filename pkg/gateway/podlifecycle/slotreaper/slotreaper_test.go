// SPDX-License-Identifier: MIT

package slotreaper_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/slotreaper"
)

// row is one session row of the fake world.
type row struct {
	terminal     bool
	terminatedAt time.Time
}

// world is the fake pod lister, slot sets, session store, and releaser the
// reaper runs against.
type world struct {
	mu       sync.Mutex
	sets     map[string][]string
	rows     map[string]row
	listErr  error
	memErr   map[string]error
	staleErr error
	// releaseErr fails every release of the named session.
	releaseErr map[string]error
	// block, when set, is called at the start of every release.
	block                 func(pod string)
	calls                 []string
	inFlight, maxInFlight int
}

func newWorld() *world {
	return &world{
		sets: map[string][]string{}, rows: map[string]row{},
		memErr: map[string]error{}, releaseErr: map[string]error{},
	}
}

func (w *world) ListPodsWithActiveSlots(context.Context) ([]string, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.listErr != nil {
		return nil, w.listErr
	}
	pods := make([]string, 0, len(w.sets))
	for p := range w.sets {
		pods = append(pods, p)
	}
	sort.Strings(pods)
	return pods, nil
}

func (w *world) Members(_ context.Context, pod string) ([]string, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if err := w.memErr[pod]; err != nil {
		return nil, err
	}
	return append([]string(nil), w.sets[pod]...), nil
}

func (w *world) ListStaleTerminalSessions(_ context.Context, ids []string, before time.Time) ([]string, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.staleErr != nil {
		return nil, w.staleErr
	}
	var out []string
	for _, id := range ids {
		r, ok := w.rows[id]
		if ok && r.terminal && !r.terminatedAt.IsZero() && r.terminatedAt.Before(before) {
			out = append(out, id)
		}
	}
	return out, nil
}

func (w *world) ReapStaleSlots(ctx context.Context, pod string, ids []string, wait func(context.Context) error) (int, error) {
	w.mu.Lock()
	w.calls = append(w.calls, pod)
	w.inFlight++
	if w.inFlight > w.maxInFlight {
		w.maxInFlight = w.inFlight
	}
	block := w.block
	w.mu.Unlock()
	defer func() {
		w.mu.Lock()
		w.inFlight--
		w.mu.Unlock()
	}()
	if block != nil {
		block(pod)
	}
	released := 0
	var errs []error
	for _, id := range ids {
		if err := wait(ctx); err != nil {
			return released, errors.Join(append(errs, err)...)
		}
		w.mu.Lock()
		if err := w.releaseErr[id]; err != nil {
			errs = append(errs, &memberErr{id: id, err: err})
			w.mu.Unlock()
			continue
		}
		w.sets[pod] = remove(w.sets[pod], id)
		w.mu.Unlock()
		released++
	}
	return released, errors.Join(errs...)
}

// memberErr is one member's release failure, naming its session as the
// binder's failure does.
type memberErr struct {
	id  string
	err error
}

func (e *memberErr) Error() string     { return e.id + ": " + e.err.Error() }
func (e *memberErr) Unwrap() error     { return e.err }
func (e *memberErr) SessionID() string { return e.id }

func remove(set []string, id string) []string {
	out := set[:0:0]
	for _, m := range set {
		if m != id {
			out = append(out, m)
		}
	}
	return out
}

func (w *world) has(pod, id string) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, m := range w.sets[pod] {
		if m == id {
			return true
		}
	}
	return false
}

func (w *world) callList() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]string(nil), w.calls...)
}

// countingLimiter admits every call and counts them.
type countingLimiter struct {
	mu sync.Mutex
	n  int
}

func (l *countingLimiter) Wait(ctx context.Context) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.n++
	return ctx.Err()
}

func (l *countingLimiter) count() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.n
}

// fakeClock is the reaper's injected clock.
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) set(t time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = t
}

const terminalAge = 15 * time.Minute

var t0 = time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC)

// newReaper builds a reaper over w with the fake clock and a counting
// limiter.
func newReaper(t *testing.T, w *world, rateLimit int) (*slotreaper.Reaper, *fakeClock, *countingLimiter) {
	t.Helper()
	clk := &fakeClock{now: t0}
	lim := &countingLimiter{}
	r, err := slotreaper.New(slotreaper.Config{
		Pods: w, Members: w, Sessions: w, Releaser: w,
		Rate: rateLimit, Interval: time.Minute, TerminalAge: terminalAge,
		BatchSize: 2, Limiter: lim, Now: clk.Now,
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err != nil {
		t.Fatal(err)
	}
	return r, clk, lim
}

// seedStale puts a member on pod whose row ended at t0.
func (w *world) seedStale(pod, id string) {
	w.sets[pod] = append(w.sets[pod], id)
	w.rows[id] = row{terminal: true, terminatedAt: t0}
}

// spec: §5.2 (Slot assignment atomicity)
//
// A member becomes eligible only once its row has been terminal for longer
// than the terminal age: it is kept at T plus the age minus one second and
// released after T plus the age.
func TestReaperReleasesOnlyAfterTheTerminalAge_spec_5_2(t *testing.T) {
	ctx := context.Background()
	w := newWorld()
	w.seedStale("pod-a", "s1")
	w.sets["pod-a"] = append(w.sets["pod-a"], "live")
	w.rows["live"] = row{}
	r, clk, _ := newReaper(t, w, 50)

	clk.set(t0.Add(terminalAge - time.Second))
	if st := r.Pass(ctx); st.Released != 0 || !w.has("pod-a", "s1") {
		t.Fatalf("pass at T+age-1s = %+v, member present %v, want kept", st, w.has("pod-a", "s1"))
	}
	if calls := w.callList(); len(calls) != 0 {
		t.Errorf("releaser calls = %v, want none for a pod with no eligible member", calls)
	}
	clk.set(t0.Add(terminalAge + time.Second))
	if st := r.Pass(ctx); st.Released != 1 || w.has("pod-a", "s1") {
		t.Fatalf("pass after T+age = %+v, want s1 released", st)
	}
	if !w.has("pod-a", "live") {
		t.Error("a live member was released")
	}
}

// spec: §5.2 (Slot assignment atomicity)
//
// The reaper releases only plain members whose row is terminal with a
// terminated_at. A leaked: member never reaches the releaser, and a member
// whose row is missing, non-terminal, or has no terminated_at is kept.
func TestReaperKeepsIneligibleMembers_spec_5_2(t *testing.T) {
	ctx := context.Background()
	w := newWorld()
	w.seedStale("pod-a", "stale")
	w.sets["pod-a"] = append(w.sets["pod-a"], "leaked:stale-2", "missing", "running", "unstamped")
	w.rows["stale-2"] = row{terminal: true, terminatedAt: t0}
	w.rows["running"] = row{}
	w.rows["unstamped"] = row{terminal: true}
	r, clk, lim := newReaper(t, w, 50)
	clk.set(t0.Add(time.Hour))
	st := r.Pass(ctx)
	if st.Eligible != 1 || st.Released != 1 {
		t.Fatalf("pass = %+v, want one eligible member released", st)
	}
	for _, id := range []string{"leaked:stale-2", "missing", "running", "unstamped"} {
		if !w.has("pod-a", id) {
			t.Errorf("member %s was released", id)
		}
	}
	if lim.count() != 1 {
		t.Errorf("limiter waits = %d, want one per attempted member", lim.count())
	}
}

// spec: §5.2 (Slot assignment atomicity)
//
// A member whose release fails is offered again on the next pass, and a
// listing, member-read, or resolution error releases no member of the pods
// it affects until the next pass after the dependency recovers.
func TestReaperRetriesAfterAFailure_spec_5_2(t *testing.T) {
	ctx := context.Background()
	boom := errors.New("injected")

	t.Run("a failed release is offered again", func(t *testing.T) {
		w := newWorld()
		w.seedStale("pod-a", "s1")
		w.seedStale("pod-a", "s2")
		w.releaseErr["s1"] = boom
		r, clk, lim := newReaper(t, w, 50)
		clk.set(t0.Add(time.Hour))
		if st := r.Pass(ctx); st.Released != 1 || st.Kept() != 1 {
			t.Fatalf("first pass = %+v, want one released and one kept", st)
		}
		delete(w.releaseErr, "s1")
		w.sets["pod-a"] = append(w.sets["pod-a"], "live")
		if st := r.Pass(ctx); st.Released != 1 || w.has("pod-a", "s1") {
			t.Fatalf("second pass = %+v, want s1 released", st)
		}
		if lim.count() != 3 {
			t.Errorf("limiter waits = %d, want 3 (one per attempted member)", lim.count())
		}
	})

	for _, tc := range []struct {
		name   string
		inject func(w *world)
		clear  func(w *world)
	}{
		{"the pod listing fails", func(w *world) { w.listErr = boom }, func(w *world) { w.listErr = nil }},
		{"a member read fails", func(w *world) { w.memErr["pod-a"] = boom }, func(w *world) { delete(w.memErr, "pod-a") }},
		{"a resolution fails", func(w *world) { w.staleErr = boom }, func(w *world) { w.staleErr = nil }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := newWorld()
			w.seedStale("pod-a", "s1")
			w.seedStale("pod-a", "s2")
			tc.inject(w)
			r, clk, _ := newReaper(t, w, 50)
			clk.set(t0.Add(time.Hour))
			if st := r.Pass(ctx); st.Released != 0 || len(w.callList()) != 0 {
				t.Fatalf("pass with the dependency down = %+v, calls %v, want nothing released", st, w.callList())
			}
			tc.clear(w)
			if st := r.Pass(ctx); st.Released != 2 {
				t.Fatalf("pass after recovery = %+v, want both released", st)
			}
		})
	}

	t.Run("a member read failure on one pod leaves the others", func(t *testing.T) {
		w := newWorld()
		w.seedStale("pod-a", "s1")
		w.seedStale("pod-b", "s2")
		w.memErr["pod-a"] = boom
		r, clk, _ := newReaper(t, w, 50)
		clk.set(t0.Add(time.Hour))
		if st := r.Pass(ctx); st.Released != 1 || !w.has("pod-a", "s1") || w.has("pod-b", "s2") {
			t.Fatalf("pass = %+v, want only pod-b's member released", st)
		}
	})
}

// spec: §5.2 (Slot assignment atomicity)
//
// Cancelling Run's context mid-pass starts no release after the
// cancellation, and Run returns.
func TestReaperRunStopsOnCancellation_spec_5_2(t *testing.T) {
	w := newWorld()
	for _, p := range []string{"pod-a", "pod-b", "pod-c"} {
		w.seedStale(p, p+"-s1")
	}
	ctx, cancel := context.WithCancel(context.Background())
	w.block = func(pod string) {
		if pod == "pod-a" {
			cancel()
		}
	}
	r, clk, _ := newReaper(t, w, 1)
	clk.set(t0.Add(time.Hour))
	done := make(chan struct{})
	go func() {
		r.Run(ctx)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Run did not return after its context was cancelled")
	}
	if calls := w.callList(); len(calls) != 1 || calls[0] != "pod-a" {
		t.Errorf("releaser calls = %v, want only pod-a's, started before the cancellation", calls)
	}
}

// spec: §5.2 (Slot assignment atomicity)
//
// At most Rate pods have a release in flight at once.
func TestReaperBoundsReleasesInFlight_spec_5_2(t *testing.T) {
	w := newWorld()
	pods := []string{"pod-1", "pod-2", "pod-3", "pod-4", "pod-5"}
	for _, p := range pods {
		w.seedStale(p, p+"-s1")
	}
	gate := make(chan struct{})
	entered := make(chan struct{}, len(pods))
	w.block = func(string) {
		entered <- struct{}{}
		<-gate
	}
	r, clk, _ := newReaper(t, w, 2)
	clk.set(t0.Add(time.Hour))
	done := make(chan slotreaper.PassStats, 1)
	go func() { done <- r.Pass(context.Background()) }()
	<-entered
	<-entered
	select {
	case <-entered:
		t.Fatal("a third release started while two were in flight")
	case <-time.After(100 * time.Millisecond):
	}
	close(gate)
	st := <-done
	if st.Released != len(pods) {
		t.Errorf("released = %d, want %d", st.Released, len(pods))
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.maxInFlight > 2 {
		t.Errorf("max releases in flight = %d, want at most 2", w.maxInFlight)
	}
}

// spec: §5.2 (Slot assignment atomicity)
//
// New refuses a non-positive rate, interval, or terminal age, and a missing
// dependency.
func TestNewRejectsAnInvalidConfig_spec_5_2(t *testing.T) {
	w := newWorld()
	good := slotreaper.Config{Pods: w, Members: w, Sessions: w, Releaser: w, Rate: 1, Interval: time.Second, TerminalAge: time.Second}
	if _, err := slotreaper.New(good); err != nil {
		t.Fatalf("New(valid) = %v", err)
	}
	for name, mutate := range map[string]func(*slotreaper.Config){
		"zero rate":         func(c *slotreaper.Config) { c.Rate = 0 },
		"negative rate":     func(c *slotreaper.Config) { c.Rate = -1 },
		"zero interval":     func(c *slotreaper.Config) { c.Interval = 0 },
		"zero terminal age": func(c *slotreaper.Config) { c.TerminalAge = 0 },
		"no releaser":       func(c *slotreaper.Config) { c.Releaser = nil },
	} {
		c := good
		mutate(&c)
		if _, err := slotreaper.New(c); err == nil {
			t.Errorf("New(%s) returned no error", name)
		}
	}
}

// spec: §5.2 (Slot assignment atomicity)
//
// A pass logs each member's release failure against the pod and that
// member's session, including failures after the first on the same pod. A
// failure that names no session (a dial or negotiation failure) is logged
// once against the pod's whole eligible list.
func TestReaperLogsEachReleaseFailureWithItsSession_spec_5_2(t *testing.T) {
	ctx := context.Background()
	boom := errors.New("injected")
	w := newWorld()
	for _, id := range []string{"s1", "s2", "s3"} {
		w.seedStale("pod-a", id)
	}
	w.seedStale("pod-b", "s4")
	w.releaseErr["s1"] = boom
	w.releaseErr["s3"] = boom
	var buf syncBuffer
	clk := &fakeClock{now: t0.Add(time.Hour)}
	r, err := slotreaper.New(slotreaper.Config{
		Pods: w, Members: w, Sessions: w, Releaser: podFailer{world: w, fail: map[string]error{"pod-b": boom}},
		Rate: 50, Interval: time.Minute, TerminalAge: terminalAge,
		Limiter: &countingLimiter{}, Now: clk.Now,
		Logger: slog.New(slog.NewJSONHandler(&buf, nil)),
	})
	if err != nil {
		t.Fatal(err)
	}
	r.Pass(ctx)

	perSession := map[string]string{}
	var podWide []string
	for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		var rec map[string]any
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("log line %q: %v", line, err)
		}
		if rec["msg"] != "stale-slot reaper: release failed" {
			continue
		}
		if id, ok := rec["session_id"].(string); ok {
			perSession[id], _ = rec["pod"].(string)
			continue
		}
		pod, _ := rec["pod"].(string)
		podWide = append(podWide, pod)
	}
	if len(perSession) != 2 || perSession["s1"] != "pod-a" || perSession["s3"] != "pod-a" {
		t.Errorf("per-session failure lines = %v, want s1 and s3 on pod-a", perSession)
	}
	if len(podWide) != 1 || podWide[0] != "pod-b" {
		t.Errorf("pod-wide failure lines = %v, want one for pod-b", podWide)
	}
}

// podFailer fails the whole release of the named pods with an error that
// names no session, as a dial failure does, and delegates the rest.
type podFailer struct {
	*world
	fail map[string]error
}

func (p podFailer) ReapStaleSlots(ctx context.Context, pod string, ids []string, wait func(context.Context) error) (int, error) {
	if err := p.fail[pod]; err != nil {
		return 0, err
	}
	return p.world.ReapStaleSlots(ctx, pod, ids, wait)
}

// syncBuffer is a bytes.Buffer safe for the concurrent release goroutines.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// recordingStore wraps the world's session store, records every batch the
// reaper sends, and returns each stale ID twice, as a store that does not
// de-duplicate its answer would.
type recordingStore struct {
	*world
	batches [][]string
}

func (s *recordingStore) ListStaleTerminalSessions(ctx context.Context, ids []string, before time.Time) ([]string, error) {
	s.batches = append(s.batches, append([]string(nil), ids...))
	stale, err := s.world.ListStaleTerminalSessions(ctx, ids, before)
	return append(stale, stale...), err
}

// spec: §5.2 (Slot assignment atomicity)
//
// A session whose release failed on one pod before its later release
// targeted another pod is a member of both pods' sets. One pass releases
// it from every pod that holds it, sends its ID to the store once, and
// offers it to each pod's releaser once even when the store answers with a
// duplicate.
func TestReaperReleasesAMemberHeldOnSeveralPodsInOnePass_spec_5_2(t *testing.T) {
	ctx := context.Background()
	w := newWorld()
	w.seedStale("pod-a", "s1")
	w.seedStale("pod-b", "s1")
	w.seedStale("pod-c", "s1")
	// Each pod also keeps a live member, so the stale one is not its last.
	for _, pod := range []string{"pod-a", "pod-b", "pod-c"} {
		w.sets[pod] = append(w.sets[pod], "live-"+pod)
		w.rows["live-"+pod] = row{}
	}
	store := &recordingStore{world: w}
	lim := &countingLimiter{}
	r, err := slotreaper.New(slotreaper.Config{
		Pods: w, Members: w, Sessions: store, Releaser: w,
		Rate: 50, Interval: time.Minute, TerminalAge: terminalAge,
		BatchSize: 2, Limiter: lim, Now: func() time.Time { return t0.Add(time.Hour) },
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err != nil {
		t.Fatal(err)
	}
	st := r.Pass(ctx)
	if st.Eligible != 3 || st.Released != 3 {
		t.Fatalf("pass = %+v, want s1 eligible and released on all three pods", st)
	}
	for _, pod := range []string{"pod-a", "pod-b", "pod-c"} {
		if w.has(pod, "s1") {
			t.Errorf("s1 still a member of %s after one pass", pod)
		}
		if !w.has(pod, "live-"+pod) {
			t.Errorf("live member of %s was released", pod)
		}
	}
	sent := map[string]int{}
	for _, b := range store.batches {
		for _, id := range b {
			sent[id]++
		}
	}
	if sent["s1"] != 1 {
		t.Errorf("s1 sent to the store %d times, want once (batches %v)", sent["s1"], store.batches)
	}
	if lim.count() != 3 {
		t.Errorf("limiter waits = %d, want one per pod holding s1", lim.count())
	}
}
