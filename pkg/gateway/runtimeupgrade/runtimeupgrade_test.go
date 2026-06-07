// SPDX-License-Identifier: MIT

package runtimeupgrade_test

import (
	"context"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/runtimeupgrade"
	"github.com/lennylabs/lenny/pkg/gateway/runtimeupgradestore"
)

// fakePool is a runtimeupgrade.PoolReader whose pools and spec are fixed
// by the test. An absent pool returns ok=false so Start maps it to 404.
type fakePool struct {
	specs map[string][]byte
}

func (f fakePool) PoolSpec(_ context.Context, pool string) ([]byte, bool, error) {
	spec, ok := f.specs[pool]
	return spec, ok, nil
}

// recordingMetrics captures the last gauge values the Manager emitted so
// a test asserts emission happens only after a durable commit.
type recordingMetrics struct {
	state    map[string]string
	duration map[string]float64
	draining map[string]int
	calls    int
}

func newRecordingMetrics() *recordingMetrics {
	return &recordingMetrics{state: map[string]string{}, duration: map[string]float64{}, draining: map[string]int{}}
}

func (m *recordingMetrics) SetRuntimeUpgradeState(pool, phase string) {
	m.state[pool] = phase
	m.calls++
}
func (m *recordingMetrics) SetRuntimeUpgradePhaseDuration(pool, phase string, seconds float64) {
	m.duration[pool] = seconds
}
func (m *recordingMetrics) SetRuntimeUpgradeDrainingSessions(pool string, n int) {
	m.draining[pool] = n
}

func newManager(t *testing.T, pools runtimeupgrade.PoolReader, metrics runtimeupgrade.MetricsEmitter) (*runtimeupgrade.Manager, *runtimeupgradestore.Memory) {
	t.Helper()
	clk := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	store := runtimeupgradestore.NewMemory().WithClock(func() time.Time { return clk })
	opts := []runtimeupgrade.Option{runtimeupgrade.WithClock(func() time.Time { return clk })}
	if pools != nil {
		opts = append(opts, runtimeupgrade.WithPoolReader(pools))
	}
	if metrics != nil {
		opts = append(opts, runtimeupgrade.WithMetrics(metrics))
	}
	return runtimeupgrade.NewManager(store, opts...), store
}

// spec: §10.5 lines 466-540 — the operator drives a full rollout through
// the linear progression pending -> expanding -> draining -> contracting
// -> complete; each Proceed is a legal edge and Complete is terminal.
func TestManager_fullLifecycle_spec_10_5(t *testing.T) {
	pools := fakePool{specs: map[string][]byte{"claude-worker": []byte(`{"minWarm":3}`)}}
	m, _ := newManager(t, pools, nil)
	ctx := t.Context()

	snap, err := m.Start(ctx, "claude-worker", runtimeupgrade.StartOptions{NewImage: "img@sha256:abc", CanaryPercent: 10})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if snap.Phase != "pending" || !snap.HasPreviousPoolSpec || snap.CanaryPercent != 10 {
		t.Fatalf("start snapshot = %+v", snap)
	}
	if snap.StabilizationWindowSeconds != 120 {
		t.Fatalf("default stabilization window = %d, want 120", snap.StabilizationWindowSeconds)
	}

	for _, want := range []string{"expanding", "draining", "contracting", "complete"} {
		snap, err = m.Proceed(ctx, "claude-worker")
		if err != nil {
			t.Fatalf("proceed to %s: %v", want, err)
		}
		if snap.Phase != want {
			t.Fatalf("phase = %q, want %q", snap.Phase, want)
		}
	}

	// Complete is terminal: a further proceed is rejected.
	if _, err := m.Proceed(ctx, "claude-worker"); err != runtimeupgrade.ErrTerminal {
		t.Fatalf("proceed past complete: %v, want ErrTerminal", err)
	}
}

// Start rejects an empty image (400) and an out-of-range canary (400).
func TestManager_startValidation_spec_10_5(t *testing.T) {
	m, _ := newManager(t, fakePool{specs: map[string][]byte{"p": []byte(`{}`)}}, nil)
	if _, err := m.Start(t.Context(), "p", runtimeupgrade.StartOptions{NewImage: ""}); err != runtimeupgrade.ErrInvalidImage {
		t.Fatalf("empty image: %v, want ErrInvalidImage", err)
	}
	if _, err := m.Start(t.Context(), "p", runtimeupgrade.StartOptions{NewImage: "img", CanaryPercent: 101}); err == nil {
		t.Fatalf("canary 101 accepted, want error")
	}
}

// Start against a pool the PoolReader does not know returns ErrPoolNotFound.
func TestManager_startUnknownPool_spec_10_5(t *testing.T) {
	m, _ := newManager(t, fakePool{specs: map[string][]byte{}}, nil)
	if _, err := m.Start(t.Context(), "ghost", runtimeupgrade.StartOptions{NewImage: "img"}); err != runtimeupgrade.ErrPoolNotFound {
		t.Fatalf("unknown pool: %v, want ErrPoolNotFound", err)
	}
}

// A second Start while a non-terminal upgrade is in flight is rejected
// with ErrUpgradeActive; a Start after the prior upgrade reached Complete
// is accepted and overwrites the record.
func TestManager_startActiveAndRestart_spec_10_5(t *testing.T) {
	pools := fakePool{specs: map[string][]byte{"p": []byte(`{}`)}}
	m, _ := newManager(t, pools, nil)
	ctx := t.Context()
	if _, err := m.Start(ctx, "p", runtimeupgrade.StartOptions{NewImage: "v1"}); err != nil {
		t.Fatalf("first start: %v", err)
	}
	if _, err := m.Start(ctx, "p", runtimeupgrade.StartOptions{NewImage: "v2"}); err != runtimeupgrade.ErrUpgradeActive {
		t.Fatalf("second start: %v, want ErrUpgradeActive", err)
	}
	// Drive to Complete, then a fresh Start is allowed.
	for i := 0; i < 4; i++ {
		if _, err := m.Proceed(ctx, "p"); err != nil {
			t.Fatalf("proceed %d: %v", i, err)
		}
	}
	snap, err := m.Start(ctx, "p", runtimeupgrade.StartOptions{NewImage: "v2"})
	if err != nil {
		t.Fatalf("restart after complete: %v", err)
	}
	if snap.Phase != "pending" || snap.NewImage != "v2" {
		t.Fatalf("restart snapshot = %+v", snap)
	}
}

// spec: §10.5 line 494 — pause from a non-terminal phase captures the
// prior phase and reason; resume restores the captured phase.
func TestManager_pauseResume_spec_10_5(t *testing.T) {
	m, _ := newManager(t, fakePool{specs: map[string][]byte{"p": []byte(`{}`)}}, nil)
	ctx := t.Context()
	if _, err := m.Start(ctx, "p", runtimeupgrade.StartOptions{NewImage: "v1"}); err != nil {
		t.Fatalf("start: %v", err)
	}
	if _, err := m.Proceed(ctx, "p"); err != nil { // -> expanding
		t.Fatalf("proceed: %v", err)
	}
	snap, err := m.Pause(ctx, "p", "canary regressed")
	if err != nil {
		t.Fatalf("pause: %v", err)
	}
	if snap.Phase != "paused" || snap.PriorPhase != "expanding" || snap.PauseReason != "canary regressed" {
		t.Fatalf("pause snapshot = %+v", snap)
	}
	// Proceed while paused is rejected.
	if _, err := m.Proceed(ctx, "p"); err != runtimeupgrade.ErrPaused {
		t.Fatalf("proceed while paused: %v, want ErrPaused", err)
	}
	snap, err = m.Resume(ctx, "p")
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	if snap.Phase != "expanding" || snap.PriorPhase != "" || snap.PauseReason != "" {
		t.Fatalf("resume snapshot = %+v", snap)
	}
}

// spec: §10.5 lines 506-507 — rollback from Expanding always succeeds and
// moves to Paused with a rollback reason.
func TestManager_rollbackFromExpanding_spec_10_5(t *testing.T) {
	m, _ := newManager(t, fakePool{specs: map[string][]byte{"p": []byte(`{"minWarm":2}`)}}, nil)
	ctx := t.Context()
	mustStart(t, m, "p", "v1")
	if _, err := m.Proceed(ctx, "p"); err != nil { // -> expanding
		t.Fatalf("proceed: %v", err)
	}
	snap, err := m.Rollback(ctx, "p", false)
	if err != nil {
		t.Fatalf("rollback from expanding: %v", err)
	}
	if snap.Phase != "paused" || snap.PriorPhase != "expanding" || snap.PauseReason != "rollback" {
		t.Fatalf("rollback snapshot = %+v", snap)
	}
}

// spec: §10.5 line 507 — rollback from Draining/Contracting requires
// restoreOldPool and a preserved previousPoolSpec.
func TestManager_rollbackFromDraining_spec_10_5(t *testing.T) {
	m, _ := newManager(t, fakePool{specs: map[string][]byte{"p": []byte(`{"minWarm":2}`)}}, nil)
	ctx := t.Context()
	mustStart(t, m, "p", "v1")
	if _, err := m.Proceed(ctx, "p"); err != nil { // expanding
		t.Fatalf("proceed: %v", err)
	}
	if _, err := m.Proceed(ctx, "p"); err != nil { // draining
		t.Fatalf("proceed: %v", err)
	}
	// Without restoreOldPool, rollback is refused.
	if _, err := m.Rollback(ctx, "p", false); err != runtimeupgrade.ErrRollbackNotAllowed {
		t.Fatalf("rollback w/o restore: %v, want ErrRollbackNotAllowed", err)
	}
	snap, err := m.Rollback(ctx, "p", true)
	if err != nil {
		t.Fatalf("rollback w/ restore: %v", err)
	}
	if snap.Phase != "paused" || snap.PriorPhase != "draining" {
		t.Fatalf("rollback snapshot = %+v", snap)
	}
}

// A rollback from Draining without a preserved previousPoolSpec is
// refused even with restoreOldPool: the old pool cannot be recreated.
func TestManager_rollbackNoPreviousSpec_spec_10_5(t *testing.T) {
	// No PoolReader, so Start captures no spec.
	m, _ := newManager(t, nil, nil)
	ctx := t.Context()
	mustStart(t, m, "p", "v1")
	if _, err := m.Proceed(ctx, "p"); err != nil { // expanding
		t.Fatalf("proceed: %v", err)
	}
	if _, err := m.Proceed(ctx, "p"); err != nil { // draining
		t.Fatalf("proceed: %v", err)
	}
	if _, err := m.Rollback(ctx, "p", true); err != runtimeupgrade.ErrRollbackNotAllowed {
		t.Fatalf("rollback w/o preserved spec: %v, want ErrRollbackNotAllowed", err)
	}
}

// Rollback from Pending or Complete is not allowed.
func TestManager_rollbackFromPendingAndComplete_spec_10_5(t *testing.T) {
	m, _ := newManager(t, fakePool{specs: map[string][]byte{"p": []byte(`{}`)}}, nil)
	ctx := t.Context()
	mustStart(t, m, "p", "v1")
	if _, err := m.Rollback(ctx, "p", true); err != runtimeupgrade.ErrRollbackNotAllowed {
		t.Fatalf("rollback from pending: %v, want ErrRollbackNotAllowed", err)
	}
	for i := 0; i < 4; i++ {
		if _, err := m.Proceed(ctx, "p"); err != nil {
			t.Fatalf("proceed %d: %v", i, err)
		}
	}
	if _, err := m.Rollback(ctx, "p", true); err != runtimeupgrade.ErrRollbackNotAllowed {
		t.Fatalf("rollback from complete: %v, want ErrRollbackNotAllowed", err)
	}
}

// Proceed/Pause/Resume/Rollback/Status against a pool with no registered
// upgrade return ErrUpgradeNotFound / ok=false.
func TestManager_noUpgradeRegistered_spec_10_5(t *testing.T) {
	m, _ := newManager(t, nil, nil)
	ctx := t.Context()
	if _, err := m.Proceed(ctx, "absent"); err != runtimeupgrade.ErrUpgradeNotFound {
		t.Fatalf("proceed absent: %v, want ErrUpgradeNotFound", err)
	}
	if _, ok, err := m.Status(ctx, "absent"); err != nil || ok {
		t.Fatalf("status absent: ok=%v err=%v", ok, err)
	}
}

// spec: §16.1 lines 184-186 — a committed transition emits the gauge
// family; emission count rises only on a durable write.
func TestManager_metricsEmittedOnCommit_spec_16_1(t *testing.T) {
	metrics := newRecordingMetrics()
	m, _ := newManager(t, fakePool{specs: map[string][]byte{"p": []byte(`{}`)}}, metrics)
	ctx := t.Context()
	mustStart(t, m, "p", "v1")
	if metrics.state["p"] != "pending" {
		t.Fatalf("state gauge after start = %q, want pending", metrics.state["p"])
	}
	before := metrics.calls
	// A rejected proceed (none here) would not bump calls; a valid one does.
	if _, err := m.Proceed(ctx, "p"); err != nil {
		t.Fatalf("proceed: %v", err)
	}
	if metrics.state["p"] != "expanding" || metrics.calls != before+1 {
		t.Fatalf("after proceed: state=%q calls=%d", metrics.state["p"], metrics.calls)
	}
	// A rejected transition does not emit.
	for i := 0; i < 3; i++ {
		if _, err := m.Proceed(ctx, "p"); err != nil {
			t.Fatalf("proceed %d: %v", i, err)
		}
	}
	stuck := metrics.calls
	if _, err := m.Proceed(ctx, "p"); err != runtimeupgrade.ErrTerminal {
		t.Fatalf("proceed past complete: %v", err)
	}
	if metrics.calls != stuck {
		t.Fatalf("rejected proceed emitted a gauge: calls %d -> %d", stuck, metrics.calls)
	}
}

// EmitAll primes the gauge family across every recorded upgrade so the
// §16.5 RuntimeUpgradeStuck alert evaluates the durable phase after a
// gateway restart.
func TestManager_emitAll_spec_16_5(t *testing.T) {
	metrics := newRecordingMetrics()
	m, _ := newManager(t, fakePool{specs: map[string][]byte{"a": []byte(`{}`), "b": []byte(`{}`)}}, metrics)
	ctx := t.Context()
	mustStart(t, m, "a", "v1")
	mustStart(t, m, "b", "v1")
	metrics.state = map[string]string{} // clear the start-time emissions
	if err := m.EmitAll(ctx); err != nil {
		t.Fatalf("emitAll: %v", err)
	}
	if metrics.state["a"] != "pending" || metrics.state["b"] != "pending" {
		t.Fatalf("emitAll gauges = %+v", metrics.state)
	}
}

func mustStart(t *testing.T, m *runtimeupgrade.Manager, pool, image string) {
	t.Helper()
	if _, err := m.Start(t.Context(), pool, runtimeupgrade.StartOptions{NewImage: image}); err != nil {
		t.Fatalf("start %s: %v", pool, err)
	}
}
