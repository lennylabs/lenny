// SPDX-License-Identifier: MIT

package coordfence

import (
	"context"
	"errors"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/lennylabs/lenny/pkg/gateway/runtime/adapterclient"
)

// fakeFenceClient drives the CoordinatorFence outcomes per attempt.
type fakeFenceClient struct {
	calls    int
	results  []adapterclient.CoordinatorFenceResult
	errs     []error
	lastGens []int64
}

func (f *fakeFenceClient) CoordinatorFence(_ context.Context, _ string, gen int64) (adapterclient.CoordinatorFenceResult, error) {
	f.lastGens = append(f.lastGens, gen)
	i := f.calls
	f.calls++
	var res adapterclient.CoordinatorFenceResult
	if i < len(f.results) {
		res = f.results[i]
	}
	var err error
	if i < len(f.errs) {
		err = f.errs[i]
	}
	return res, err
}

// genReader returns a sequence of generations across reads (initial,
// then each post-stale re-read).
type genReader struct {
	gens []int64
	idx  int
	err  error
}

func (g *genReader) CoordinationGeneration(_ context.Context, _, _ string) (int64, error) {
	if g.err != nil {
		return 0, g.err
	}
	v := g.gens[len(g.gens)-1]
	if g.idx < len(g.gens) {
		v = g.gens[g.idx]
	}
	g.idx++
	return v, nil
}

type fakeReleaser struct{ released int }

func (r *fakeReleaser) Release(_ context.Context, _, _, _ string) error {
	r.released++
	return nil
}

type fakeMetrics struct{ stale, retry, relinquish int }

func (m *fakeMetrics) IncCoordinatorHandoffStale()      { m.stale++ }
func (m *fakeMetrics) IncCoordinatorFenceRetry()        { m.retry++ }
func (m *fakeMetrics) IncCoordinatorFenceRelinquished() { m.relinquish++ }

func staleErr() error { return status.Error(codes.FailedPrecondition, "coordinator_handoff_stale") }

// spec: §10.1 lines 33-37 — the fence is accepted on the first attempt;
// no retry, no relinquish, no lease release.
func TestFenceAcceptedFirstAttempt_spec_10_1(t *testing.T) {
	fc := &fakeFenceClient{results: []adapterclient.CoordinatorFenceResult{{Accepted: true, LastFencedGeneration: 4}}}
	rel := &fakeReleaser{}
	m := &fakeMetrics{}
	f := New(&genReader{gens: []int64{4}}, rel, "replica-a", m, Options{})
	relinquished, err := f.fence(context.Background(), fc, "acme", "s1")
	if err != nil || relinquished {
		t.Fatalf("fence: relinquished=%v err=%v, want false/nil", relinquished, err)
	}
	if fc.calls != 1 || fc.lastGens[0] != 4 {
		t.Errorf("calls=%d gens=%v, want 1 call at gen 4", fc.calls, fc.lastGens)
	}
	if m.stale != 0 || m.retry != 0 || m.relinquish != 0 || rel.released != 0 {
		t.Errorf("metrics stale=%d retry=%d relinquish=%d released=%d, want all 0", m.stale, m.retry, m.relinquish, rel.released)
	}
}

// spec: §10.1 line 61 / §11.3 line 209 — a stale rejection with no
// generation advance relinquishes: handoff_stale + relinquished counters
// fire and the lease is released.
func TestFenceStaleNoAdvanceRelinquishes_spec_11_3_209(t *testing.T) {
	fc := &fakeFenceClient{errs: []error{staleErr()}}
	rel := &fakeReleaser{}
	m := &fakeMetrics{}
	f := New(&genReader{gens: []int64{5}}, rel, "replica-a", m, Options{})
	relinquished, err := f.fence(context.Background(), fc, "acme", "s1")
	if !relinquished || !errors.Is(err, ErrRelinquished) {
		t.Fatalf("fence: relinquished=%v err=%v, want true/ErrRelinquished", relinquished, err)
	}
	if m.stale != 1 || m.relinquish != 1 || rel.released != 1 {
		t.Errorf("stale=%d relinquish=%d released=%d, want 1/1/1", m.stale, m.relinquish, rel.released)
	}
	if m.retry != 0 {
		t.Errorf("retry=%d, want 0 (no advance to retry)", m.retry)
	}
}

// spec: §10.1 line 165 — a stale rejection whose authoritative generation
// advanced mid-handoff is retried at the new value and then accepted.
func TestFenceStaleThenAdvanceRetriesAndAccepts_spec_10_1(t *testing.T) {
	fc := &fakeFenceClient{
		errs:    []error{staleErr(), nil},
		results: []adapterclient.CoordinatorFenceResult{{}, {Accepted: true, LastFencedGeneration: 8}},
	}
	rel := &fakeReleaser{}
	m := &fakeMetrics{}
	// initial read 6, post-stale re-read 8 (a handoff bump landed).
	f := New(&genReader{gens: []int64{6, 8}}, rel, "replica-a", m, Options{})
	relinquished, err := f.fence(context.Background(), fc, "acme", "s1")
	if err != nil || relinquished {
		t.Fatalf("fence: relinquished=%v err=%v, want false/nil", relinquished, err)
	}
	if fc.calls != 2 || fc.lastGens[1] != 8 {
		t.Errorf("calls=%d gens=%v, want 2 calls, retry at gen 8", fc.calls, fc.lastGens)
	}
	if m.stale != 1 || m.retry != 1 || m.relinquish != 0 || rel.released != 0 {
		t.Errorf("stale=%d retry=%d relinquish=%d released=%d, want 1/1/0/0", m.stale, m.retry, m.relinquish, rel.released)
	}
}

// spec: §11.3 line 209 — repeated transient transport faults are retried
// up to the attempt budget, then the coordinator relinquishes.
func TestFenceTransientExhaustedRelinquishes_spec_11_3_209(t *testing.T) {
	transient := status.Error(codes.Unavailable, "pod unreachable")
	fc := &fakeFenceClient{errs: []error{transient, transient, transient}}
	rel := &fakeReleaser{}
	m := &fakeMetrics{}
	f := New(&genReader{gens: []int64{2}}, rel, "replica-a", m, Options{MaxAttempts: 3})
	relinquished, err := f.fence(context.Background(), fc, "acme", "s1")
	if !relinquished || !errors.Is(err, ErrRelinquished) {
		t.Fatalf("fence: relinquished=%v err=%v, want true/ErrRelinquished", relinquished, err)
	}
	if fc.calls != 3 {
		t.Errorf("calls=%d, want 3 (attempt budget)", fc.calls)
	}
	if m.retry != 3 || m.relinquish != 1 || rel.released != 1 {
		t.Errorf("retry=%d relinquish=%d released=%d, want 3/1/1", m.retry, m.relinquish, rel.released)
	}
}

// A generation-read fault is a best-effort fence failure: it returns a
// non-relinquish error so the caller logs and proceeds (the lease still
// guards coordination); no lease is released.
func TestFenceGenerationReadErrorIsBestEffort(t *testing.T) {
	fc := &fakeFenceClient{}
	rel := &fakeReleaser{}
	m := &fakeMetrics{}
	f := New(&genReader{err: errors.New("store down")}, rel, "replica-a", m, Options{})
	relinquished, err := f.fence(context.Background(), fc, "acme", "s1")
	if relinquished || err == nil || errors.Is(err, ErrRelinquished) {
		t.Fatalf("fence: relinquished=%v err=%v, want false/non-relinquish error", relinquished, err)
	}
	if fc.calls != 0 || rel.released != 0 {
		t.Errorf("calls=%d released=%d, want 0/0", fc.calls, rel.released)
	}
}

// A zero/unstamped generation fences at the baseline 1 rather than the
// adapter-rejected zero.
func TestFenceZeroGenerationFencesAtBaseline(t *testing.T) {
	fc := &fakeFenceClient{results: []adapterclient.CoordinatorFenceResult{{Accepted: true, LastFencedGeneration: 1}}}
	f := New(&genReader{gens: []int64{0}}, &fakeReleaser{}, "replica-a", &fakeMetrics{}, Options{})
	if _, err := f.fence(context.Background(), fc, "acme", "s1"); err != nil {
		t.Fatalf("fence: %v", err)
	}
	if fc.lastGens[0] != 1 {
		t.Errorf("fenced at gen %d, want baseline 1", fc.lastGens[0])
	}
}
