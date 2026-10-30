// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"errors"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/coordination/coordfence"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/adapterclient"
)

// stubFencer records its invocations and returns a fixed disposition.
type stubFencer struct {
	calls        int
	fenceAtCalls int
	generations  []int64
	relinquished bool
	err          error
}

func (f *stubFencer) Fence(_ context.Context, _ *adapterclient.Client, _, _ string) (bool, error) {
	f.calls++
	return f.relinquished, f.err
}

func (f *stubFencer) FenceAt(_ context.Context, _ *adapterclient.Client, _, _ string, generation int64) (bool, error) {
	f.calls++
	f.fenceAtCalls++
	f.generations = append(f.generations, generation)
	return f.relinquished, f.err
}

// spec: §10.1 — a nil fencer (dev / in-memory mode) makes the
// resume fence a no-op rather than a panic.
func TestFenceResumedPodNilFencerIsNoOp(t *testing.T) {
	s := &Server{}
	if err := s.fenceResumedPod(context.Background(), adapterclient.New(nil), "acme", "s1", 4); err != nil {
		t.Fatalf("nil fencer: want nil error, got %v", err)
	}
}

// spec: §10.1 — a nil pod adapter (no live connection) is skipped without
// invoking the fencer.
func TestFenceResumedPodNilAdapterSkips(t *testing.T) {
	f := &stubFencer{}
	s := &Server{fencer: f}
	if err := s.fenceResumedPod(context.Background(), nil, "acme", "s1", 4); err != nil {
		t.Fatalf("nil adapter: want nil error, got %v", err)
	}
	if f.calls != 0 {
		t.Errorf("fencer called %d times, want 0 for a nil adapter", f.calls)
	}
}

// TestFenceResumedPodRelinquishAbortsResume_spec_10_1_2 asserts that a
// relinquish propagates as an error, so the resume driver takes the
// Relinquished exit (the adopter owns the pod), and that the resume fences at
// the generation it committed at, through FenceAt rather than a re-read
// value.
//
// spec: §10.1.2 (Coordinator Handoff Protocol), §10.1.5 (Stale Replica
// Behavior), §11.3.
func TestFenceResumedPodRelinquishAbortsResume_spec_10_1_2(t *testing.T) {
	f := &stubFencer{relinquished: true, err: coordfence.ErrRelinquished}
	s := &Server{fencer: f}
	err := s.fenceResumedPod(context.Background(), adapterclient.New(nil), "acme", "s1", 7)
	if !errors.Is(err, coordfence.ErrRelinquished) {
		t.Fatalf("relinquish: want ErrRelinquished, got %v", err)
	}
	if f.fenceAtCalls != 1 || len(f.generations) != 1 || f.generations[0] != 7 {
		t.Errorf("FenceAt calls = %d at generations %v, want one call at generation 7", f.fenceAtCalls, f.generations)
	}
}

// A best-effort fence failure (non-relinquish error) is swallowed so the
// resume proceeds; the coordination lease still guards exclusive ownership.
func TestFenceResumedPodBestEffortFailureSwallowed(t *testing.T) {
	f := &stubFencer{relinquished: false, err: errors.New("generation read failed")}
	s := &Server{fencer: f}
	if err := s.fenceResumedPod(context.Background(), adapterclient.New(nil), "acme", "s1", 3); err != nil {
		t.Fatalf("best-effort failure: want nil error, got %v", err)
	}
	if f.calls != 1 {
		t.Errorf("fencer called %d times, want 1", f.calls)
	}
}
