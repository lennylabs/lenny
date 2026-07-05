// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/credential"
	"github.com/lennylabs/lenny/pkg/gateway/billing/usagestore"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/adapterclient"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionidle"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
)

// stubLeaseLookup returns a fixed set of leases keyed by session, standing in
// for the credential-lease store the loop reads delivery mode and tenant from.
type stubLeaseLookup struct {
	leases map[string]credential.Lease
}

func (s stubLeaseLookup) LeasesBySession(sessionIDs []string) []credential.Lease {
	out := make([]credential.Lease, 0, len(sessionIDs))
	for _, id := range sessionIDs {
		if l, ok := s.leases[id]; ok {
			out = append(out, l)
		}
	}
	return out
}

// stubPuller is a directUsagePuller test double recording each pull and
// returning a scripted report (or error) per session. Concurrency-safe so the
// loop goroutine and the test can both touch it.
type stubPuller struct {
	mu      sync.Mutex
	reports map[string]adapterclient.UsageReport
	err     error
	pulls   []string
}

func (p *stubPuller) ReportUsageForLease(_ context.Context, sessionID string, deliveryMode credential.DeliveryMode, cumulative bool) (adapterclient.UsageReport, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.pulls = append(p.pulls, sessionID)
	if p.err != nil {
		return adapterclient.UsageReport{}, p.err
	}
	return p.reports[sessionID], nil
}

func (p *stubPuller) pullCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.pulls)
}

// newLoopUnderTest builds a directUsageLoop over a real registry and recorder,
// injecting a stub puller so the registry-keyed teardown logic runs without a
// live gRPC adapter. The interval is the (post-clamp) minimum so a ticker-based
// test does not wait long.
func newLoopUnderTest(t *testing.T, registry *podsession.Registry, leases directUsageLeaseLookup, rec *proxyUsageRecorder, puller directUsagePuller) *directUsageLoop {
	t.Helper()
	loop := newDirectUsageLoop(registry, leases, rec, minDirectUsagePollIntervalSeconds, nil)
	if loop == nil {
		t.Fatal("newDirectUsageLoop returned nil with all dependencies set")
	}
	// The registry entries in these tests carry no live adapter, so the stub
	// stands in for every pull. This keeps the registry-keyed teardown logic
	// (Snapshot / Get) real while substituting the ReportUsage transport.
	loop.pullerFor = func(*podsession.BindResult) directUsagePuller { return puller }
	return loop
}

// TestDirectUsageLoopStopsAtTeardown_spec_11_2 proves the loop keys off the
// session-scoped pod registry: a session removed from the registry between the
// snapshot and the per-session pull is not pulled, so the loop stops pulling a
// session the moment its binding is torn down. This is the F-15.3.7 lifetime
// invariant — the loop reaches the pod over podRegistry.Get, never the transient
// BindResult the bind caller closes at teardown.
//
// spec: §11.2 line 42 (direct-mode usage recording over the session-scoped
// registry), §4.7 (ReportUsage pull).
// diagnosis: the direct-mode usage loop kept pulling a session after its binding was removed at teardown, dialing a closed adapter, because it iterated the transient BindResult snapshot rather than re-reading the session-scoped registry (proposal 0024 S9 registry-keyed teardown broken).
func TestDirectUsageLoopStopsAtTeardown_spec_11_2(t *testing.T) {
	ctx := context.Background()
	registry := podsession.NewRegistry()
	registry.Put(&podsession.BindResult{SessionID: "s_live", TenantID: "acme"})
	leases := stubLeaseLookup{leases: map[string]credential.Lease{
		"s_live": directUsage("s_live", "acme"),
	}}
	puller := &stubPuller{reports: map[string]adapterclient.UsageReport{
		"s_live": {InputTokens: 10, OutputTokens: 5},
	}}
	rec := newProxyUsageRecorder(usagestore.NewMemory(), memstore.New(), nil, nil, nil, nil)
	loop := newLoopUnderTest(t, registry, leases, rec, puller)

	// A poll with the session bound pulls it.
	loop.pollOnce(ctx)
	if puller.pullCount() != 1 || puller.pulls[0] != "s_live" {
		t.Fatalf("bound session must be pulled once, got pulls=%v", puller.pulls)
	}

	// Tear the session down: remove its binding from the registry.
	registry.Remove("s_live")

	// A subsequent poll must not pull the torn-down session. The snapshot is
	// empty now, so pollOnce returns before reaching the puller.
	loop.pollOnce(ctx)
	if puller.pullCount() != 1 {
		t.Fatalf("torn-down session must not be pulled again, got pulls=%v", puller.pulls)
	}
}

// TestDirectUsageLoopSkipsSessionRemovedMidPoll_spec_11_2 proves the
// per-session re-read through the registry (pullSession's Get) skips a session
// unbound between the Snapshot and the pull, so a session torn down mid-cycle is
// never dialed. It removes the binding after the snapshot is taken but before
// the pull, exercising the exact race the registry re-read defends against.
//
// spec: §11.2 line 42 (session-scoped pull), §4.7 (ReportUsage).
// diagnosis: the loop dialed a session's adapter after it unbound mid-poll because pullSession pulled from the stale snapshot binding instead of re-reading the registry (proposal 0024 S9 mid-poll teardown race).
func TestDirectUsageLoopSkipsSessionRemovedMidPoll_spec_11_2(t *testing.T) {
	registry := podsession.NewRegistry()
	registry.Put(&podsession.BindResult{SessionID: "s_gone", TenantID: "acme"})
	leases := stubLeaseLookup{leases: map[string]credential.Lease{
		"s_gone": directUsage("s_gone", "acme"),
	}}
	puller := &stubPuller{reports: map[string]adapterclient.UsageReport{}}
	rec := newProxyUsageRecorder(usagestore.NewMemory(), memstore.New(), nil, nil, nil, nil)
	loop := newLoopUnderTest(t, registry, leases, rec, puller)

	// Simulate the race directly: pullSession re-reads the registry, so removing
	// the binding before the per-session pull means the pull is skipped.
	registry.Remove("s_gone")
	loop.pullSession(context.Background(), "s_gone", directUsage("s_gone", "acme"), time.Second)
	if puller.pullCount() != 0 {
		t.Fatalf("a session unbound before its per-session pull must be skipped, got pulls=%v", puller.pulls)
	}
}

// TestDirectUsageLoopHungPodIdleTerminates_spec_6_2_253 proves a hung
// direct-mode pod still idle-terminates under the pull: the gateway polls it,
// the pull returns a zero token delta (no tokens produced by a wedged agent),
// and the §6.2 idle clock is left untouched, so the §11.3 idle watchdog reaps
// the session. It drives the full loop → recorder path with a zero-delta report
// and asserts the session's LastAgentActivityAt stays zero. It would fail
// against a loop that stamped the idle clock on every poll (a timer tick), which
// would keep a hung pod alive forever.
//
// spec: §6.2 line 253 (direct-mode idle reset on non-zero delta only), §8.3
// line 435 (over-run bounded against the poll interval), §11.2 (direct-mode
// usage).
// diagnosis: a hung direct-mode pod never idle-terminated because the poll loop reset its idle clock on every zero-delta tick, so the pod, lease, and session leaked (proposal 0024 S9 hung-pod idle bound broken).
func TestDirectUsageLoopHungPodIdleTerminates_spec_6_2_253(t *testing.T) {
	ctx := context.Background()
	store := memstore.New()
	t0 := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	if err := store.Create(ctx, sessionstore.Session{
		ID: "s_hung", TenantID: "acme", State: session.StateRunning, CreatedAt: t0, UpdatedAt: t0,
	}); err != nil {
		t.Fatalf("sessions.Create: %v", err)
	}
	registry := podsession.NewRegistry()
	registry.Put(&podsession.BindResult{SessionID: "s_hung", TenantID: "acme"})
	leases := stubLeaseLookup{leases: map[string]credential.Lease{
		"s_hung": directUsage("s_hung", "acme"),
	}}
	// A hung pod: the adapter answers the pull but reports zero new tokens.
	puller := &stubPuller{reports: map[string]adapterclient.UsageReport{
		"s_hung": {InputTokens: 0, OutputTokens: 0},
	}}
	rec := newProxyUsageRecorder(usagestore.NewMemory(), store, nil, nil, nil, nil)
	stamper := sessionidle.NewStamper(store, func() time.Time { return t0 })
	rec.setActivityStamper(stamper)
	loop := newLoopUnderTest(t, registry, leases, rec, puller)

	// Poll the hung pod several times; every pull returns a zero delta.
	for i := 0; i < 5; i++ {
		loop.pollOnce(ctx)
	}
	// Give any background persist that must NOT happen a chance to run.
	time.Sleep(50 * time.Millisecond)

	row, _ := store.Get(ctx, "acme", "s_hung")
	if !row.LastAgentActivityAt.IsZero() {
		t.Fatalf("hung pod (zero-delta pulls) must not reset the §6.2 idle clock, got %v", row.LastAgentActivityAt)
	}
	if puller.pullCount() != 5 {
		t.Fatalf("expected 5 pulls of the hung pod, got %d", puller.pullCount())
	}
}

// TestDirectUsageLoopSkipsProxyLease_spec_4_9_1468 proves the loop pulls only
// direct-delivery sessions: a proxy-mode session's counts are already
// authoritative on the §4.9 path, so the loop must not pull it (which would
// double-count). It binds one direct and one proxy session and asserts only the
// direct one is pulled.
//
// spec: §4.9 line 1468 (proxy-extracted counts authoritative), §11.2
// (direct-mode usage).
func TestDirectUsageLoopSkipsProxyLease_spec_4_9_1468(t *testing.T) {
	ctx := context.Background()
	registry := podsession.NewRegistry()
	registry.Put(&podsession.BindResult{SessionID: "s_direct", TenantID: "acme"})
	registry.Put(&podsession.BindResult{SessionID: "s_proxy", TenantID: "acme"})
	leases := stubLeaseLookup{leases: map[string]credential.Lease{
		"s_direct": directUsage("s_direct", "acme"),
		"s_proxy": {
			LeaseID: "cl-p", SessionID: "s_proxy", TenantID: "acme",
			Source: credential.SourcePool, DeliveryMode: credential.DeliveryProxy,
		},
	}}
	puller := &stubPuller{reports: map[string]adapterclient.UsageReport{
		"s_direct": {InputTokens: 7, OutputTokens: 3},
	}}
	rec := newProxyUsageRecorder(usagestore.NewMemory(), memstore.New(), nil, nil, nil, nil)
	loop := newLoopUnderTest(t, registry, leases, rec, puller)

	loop.pollOnce(ctx)
	if puller.pullCount() != 1 || puller.pulls[0] != "s_direct" {
		t.Fatalf("only the direct-mode session must be pulled, got pulls=%v", puller.pulls)
	}
}

// TestDirectUsageLoopFailedPullDoesNotStamp_spec_6_2_253 proves a failed or
// timed-out pull (a wedged adapter that never answers) records no delta and so
// leaves the §6.2 idle clock untouched, the same hung-pod outcome as a
// zero-delta answer. The stub returns a transport error for every pull.
//
// spec: §6.2 line 253 (idle reset on recorded non-zero delta only), §4.7
// (ReportUsage pull).
// diagnosis: a wedged direct-mode adapter that failed the pull still reset the idle clock (or crashed the loop) rather than being treated as no activity, so a wedged pod leaked (proposal 0024 S9 failed-pull handling broken).
func TestDirectUsageLoopFailedPullDoesNotStamp_spec_6_2_253(t *testing.T) {
	ctx := context.Background()
	store := memstore.New()
	t0 := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	if err := store.Create(ctx, sessionstore.Session{
		ID: "s_wedged", TenantID: "acme", State: session.StateRunning, CreatedAt: t0, UpdatedAt: t0,
	}); err != nil {
		t.Fatalf("sessions.Create: %v", err)
	}
	registry := podsession.NewRegistry()
	registry.Put(&podsession.BindResult{SessionID: "s_wedged", TenantID: "acme"})
	leases := stubLeaseLookup{leases: map[string]credential.Lease{
		"s_wedged": directUsage("s_wedged", "acme"),
	}}
	puller := &stubPuller{err: errors.New("adapter unreachable")}
	rec := newProxyUsageRecorder(usagestore.NewMemory(), store, nil, nil, nil, nil)
	stamper := sessionidle.NewStamper(store, func() time.Time { return t0 })
	rec.setActivityStamper(stamper)
	loop := newLoopUnderTest(t, registry, leases, rec, puller)

	loop.pollOnce(ctx)
	time.Sleep(50 * time.Millisecond)
	row, _ := store.Get(ctx, "acme", "s_wedged")
	if !row.LastAgentActivityAt.IsZero() {
		t.Fatalf("a failed pull must not reset the §6.2 idle clock, got %v", row.LastAgentActivityAt)
	}
}

// TestDirectUsageLoopRunExitsOnContextCancel_spec_4_1 proves the poll loop's
// goroutine exits when its context is cancelled: the only exit is ctx.Done, so
// it leaks no goroutine at gateway shutdown. Run is launched on a goroutine and
// must return promptly after cancel.
//
// spec: §4.1 (gateway background subsystems tied to the watchdog context),
// §11.2 (direct-mode usage loop).
// diagnosis: the direct-mode poll loop leaked its goroutine at shutdown because Run did not tie its exit to context cancellation (proposal 0024 S9 goroutine-exit invariant broken).
func TestDirectUsageLoopRunExitsOnContextCancel_spec_4_1(t *testing.T) {
	registry := podsession.NewRegistry()
	leases := stubLeaseLookup{leases: map[string]credential.Lease{}}
	puller := &stubPuller{reports: map[string]adapterclient.UsageReport{}}
	rec := newProxyUsageRecorder(usagestore.NewMemory(), memstore.New(), nil, nil, nil, nil)
	loop := newLoopUnderTest(t, registry, leases, rec, puller)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		loop.Run(ctx)
		close(done)
	}()
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not exit within 2s of context cancel (goroutine leak)")
	}

	// Run is a no-op on a nil loop so the launch site need not nil-check.
	var nilLoop *directUsageLoop
	nilLoop.Run(context.Background())
}

// TestNewDirectUsageLoopNilOnMissingDeps proves newDirectUsageLoop returns nil
// when any dependency the pull needs is absent, so the caller can launch it
// unconditionally on a minimal gateway that records no usage.
func TestNewDirectUsageLoopNilOnMissingDeps(t *testing.T) {
	registry := podsession.NewRegistry()
	leases := stubLeaseLookup{}
	rec := newProxyUsageRecorder(usagestore.NewMemory(), memstore.New(), nil, nil, nil, nil)
	if l := newDirectUsageLoop(nil, leases, rec, 30, nil); l != nil {
		t.Error("nil registry must yield a nil loop")
	}
	if l := newDirectUsageLoop(registry, nil, rec, 30, nil); l != nil {
		t.Error("nil lease lookup must yield a nil loop")
	}
	if l := newDirectUsageLoop(registry, leases, nil, 30, nil); l != nil {
		t.Error("nil recorder must yield a nil loop")
	}
}

// TestDirectUsageRecorderOrNil_TypedNilRecorderYieldsNilLoop proves the
// typed-nil normalization at the loop's construction site: a nil
// *proxyUsageRecorder (what newProxyUsageRecorder returns on a usagestore-less
// gateway) is normalized to a genuinely nil directUsageRecorder interface, so
// newDirectUsageLoop's recorder==nil guard trips and the loop is not
// constructed on a minimal gateway. It would fail against the pre-fix code that
// passed the concrete *proxyUsageRecorder straight into the interface parameter:
// Go wraps a typed nil into a non-nil interface, so newDirectUsageLoop would
// return a live loop and run an empty ticker every tick instead of a no-op.
//
// spec: §11.2 line 42 (direct-mode usage loop; a minimal gateway records no
// usage and runs no pull), §4.1 (background subsystems are constructed only
// when their dependencies are present).
// diagnosis: the direct-mode poll loop was constructed and ran a live ticker on a usagestore-less gateway because a nil *proxyUsageRecorder wrapped into a non-nil directUsageRecorder interface, defeating the recorder==nil short-circuit (proposal 0024 S9 typed-nil no-op contract broken).
func TestDirectUsageRecorderOrNil_TypedNilRecorderYieldsNilLoop(t *testing.T) {
	registry := podsession.NewRegistry()
	leases := stubLeaseLookup{}

	// A usagestore-less gateway: newProxyUsageRecorder returns a nil
	// *proxyUsageRecorder. This is exactly the value workers.go passes as
	// w.proxyUsageRec on a minimal gateway.
	nilRec := newProxyUsageRecorder(nil, nil, nil, nil, nil, nil)
	if nilRec != nil {
		t.Fatal("newProxyUsageRecorder(nil, ...) must return a nil *proxyUsageRecorder")
	}

	// Reproduce the pre-fix defect: passing the concrete typed nil straight
	// into the interface parameter wraps it into a non-nil interface, so the
	// guard does not trip and the loop is constructed.
	if loop := newDirectUsageLoop(registry, leases, nilRec, 30, nil); loop == nil {
		t.Fatal("guard sanity: a concrete typed-nil recorder wraps into a non-nil interface, so this pre-fix path yields a live loop")
	}

	// The fix: normalizing through directUsageRecorderOrNil yields a genuinely
	// nil interface, so the guard trips and no loop (and no live ticker) is
	// constructed.
	if loop := newDirectUsageLoop(registry, leases, directUsageRecorderOrNil(nilRec), 30, nil); loop != nil {
		t.Fatal("directUsageRecorderOrNil must normalize a nil *proxyUsageRecorder so the loop is not constructed on a minimal gateway")
	}

	// A non-nil recorder passes through unchanged and yields a live loop.
	realRec := newProxyUsageRecorder(usagestore.NewMemory(), memstore.New(), nil, nil, nil, nil)
	if loop := newDirectUsageLoop(registry, leases, directUsageRecorderOrNil(realRec), 30, nil); loop == nil {
		t.Fatal("a present recorder must yield a live loop through directUsageRecorderOrNil")
	}
}

// TestClampDirectUsagePollIntervalSeconds pins the §11.2 poll-interval bounds:
// a non-positive value selects the 30s default, a value below the 10s minimum
// is clamped up, and a value at or above the minimum is unchanged.
// spec: §11.2 line 44 (default 30, minimum 10).
func TestClampDirectUsagePollIntervalSeconds(t *testing.T) {
	cases := []struct {
		in, want int
	}{
		{0, 30}, {-5, 30}, {1, 10}, {9, 10}, {10, 10}, {30, 30}, {120, 120},
	}
	for _, c := range cases {
		if got := clampDirectUsagePollIntervalSeconds(c.in); got != c.want {
			t.Errorf("clampDirectUsagePollIntervalSeconds(%d) = %d, want %d", c.in, got, c.want)
		}
	}
}

// TestDirectUsagePullTimeout pins the per-call pull deadline derivation: it is
// half the poll interval, capped at 5s, and never non-positive.
func TestDirectUsagePullTimeout(t *testing.T) {
	cases := []struct {
		interval, want time.Duration
	}{
		{30 * time.Second, 5 * time.Second}, // half (15s) exceeds the 5s cap
		{10 * time.Second, 5 * time.Second}, // half (5s) equals the cap
		{6 * time.Second, 3 * time.Second},  // half under the cap
		{0, 5 * time.Second},                // degenerate interval falls back to the cap
	}
	for _, c := range cases {
		if got := directUsagePullTimeout(c.interval); got != c.want {
			t.Errorf("directUsagePullTimeout(%s) = %s, want %s", c.interval, got, c.want)
		}
	}
}
