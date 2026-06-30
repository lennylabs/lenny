// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"errors"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/coordfence"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/adapterclient"
)

// stubFencer records its invocation and returns a fixed disposition.
type stubFencer struct {
	calls        int
	relinquished bool
	err          error
}

func (f *stubFencer) Fence(_ context.Context, _ *adapterclient.Client, _, _ string) (bool, error) {
	f.calls++
	return f.relinquished, f.err
}

// spec: §10.1 lines 33-37 — a nil fencer (dev / in-memory mode) makes the
// resume fence a no-op rather than a panic.
func TestFenceResumedPodNilFencerIsNoOp(t *testing.T) {
	s := &Server{}
	if err := s.fenceResumedPod(context.Background(), adapterclient.New(nil), "acme", "s1"); err != nil {
		t.Fatalf("nil fencer: want nil error, got %v", err)
	}
}

// spec: §10.1 — a nil pod adapter (no live connection) is skipped without
// invoking the fencer.
func TestFenceResumedPodNilAdapterSkips(t *testing.T) {
	f := &stubFencer{}
	s := &Server{fencer: f}
	if err := s.fenceResumedPod(context.Background(), nil, "acme", "s1"); err != nil {
		t.Fatalf("nil adapter: want nil error, got %v", err)
	}
	if f.calls != 0 {
		t.Errorf("fencer called %d times, want 0 for a nil adapter", f.calls)
	}
}

// spec: §11.3 line 209 — a relinquish propagates as an error so the
// caller aborts the resume (another replica owns the session).
func TestFenceResumedPodRelinquishAbortsResume(t *testing.T) {
	f := &stubFencer{relinquished: true, err: coordfence.ErrRelinquished}
	s := &Server{fencer: f}
	err := s.fenceResumedPod(context.Background(), adapterclient.New(nil), "acme", "s1")
	if !errors.Is(err, coordfence.ErrRelinquished) {
		t.Fatalf("relinquish: want ErrRelinquished, got %v", err)
	}
	// spec: §7.3 line 423 — the relinquish must classify transient so the
	// row holds in awaiting_client_action for the client's resume retry.
	if !isTransientPodClaimError(err) {
		t.Errorf("ErrRelinquished should classify as a transient pod-claim error")
	}
}

// A best-effort fence failure (non-relinquish error) is swallowed so the
// resume proceeds; the coordination lease still guards exclusive ownership.
func TestFenceResumedPodBestEffortFailureSwallowed(t *testing.T) {
	f := &stubFencer{relinquished: false, err: errors.New("generation read failed")}
	s := &Server{fencer: f}
	if err := s.fenceResumedPod(context.Background(), adapterclient.New(nil), "acme", "s1"); err != nil {
		t.Fatalf("best-effort failure: want nil error, got %v", err)
	}
	if f.calls != 1 {
		t.Errorf("fencer called %d times, want 1", f.calls)
	}
}
