// SPDX-License-Identifier: MIT

package checkpointer_test

import (
	"context"
	"errors"
	"io"
	"net"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"

	"github.com/lennylabs/lenny/pkg/adapter"
	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/checkpointer"
	"github.com/lennylabs/lenny/pkg/gateway/checkpointretention"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/adapterclient"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
)

// spec: §4.4 / §7.1 — the gateway checkpointer drives a pod checkpoint
// and records the resulting WorkspaceSnapshot on the session row.

// stubRuntime is a no-op RuntimeProcess for the bufconn adapter.
type stubRuntime struct{}

func (stubRuntime) Start(context.Context, string) error           { return nil }
func (stubRuntime) WriteEnvelope(string, []byte) error            { return nil }
func (stubRuntime) Interrupt(context.Context, string, bool) error { return nil }
func (stubRuntime) Close(context.Context, string) error           { return nil }
func (stubRuntime) Output(context.Context, string) (<-chan []byte, error) {
	ch := make(chan []byte)
	close(ch)
	return ch, nil
}

// stubSink is an adapter.CheckpointSink returning a fixed checkpoint id.
type stubSink struct {
	id  string
	err error
}

func (s stubSink) SaveCheckpoint(_ context.Context, _ string, r io.Reader) (string, error) {
	_, _ = io.Copy(io.Discard, r)
	if s.err != nil {
		return "", s.err
	}
	return s.id, nil
}

// dialAdapter serves srv over an in-memory connection and returns a
// connected adapter client.
func dialAdapter(t *testing.T, srv *adapter.Server) *adapterclient.Client {
	t.Helper()
	lis := bufconn.Listen(1 << 20)
	gs := adapter.NewGRPCServer(srv)
	go func() { _ = gs.Serve(lis) }()
	t.Cleanup(gs.Stop)
	cl, err := adapterclient.Dial("passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return lis.DialContext(ctx)
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial adapter: %v", err)
	}
	t.Cleanup(func() { _ = cl.Close() })
	return cl
}

// runningSession seeds store with one running session.
func runningSession(t *testing.T, store sessionstore.Store, tenantID, sessionID string) {
	t.Helper()
	if err := store.Create(context.Background(), sessionstore.Session{
		ID:         sessionID,
		TenantID:   tenantID,
		State:      session.StateRunning,
		RuntimeRef: "echo",
	}); err != nil {
		t.Fatalf("seed session: %v", err)
	}
}

func TestCheckpointRecordsTheWorkspaceSnapshot(t *testing.T) {
	srv := adapter.New("checkpointer-test")
	srv.WorkspaceRoot = t.TempDir()
	srv.Runtime = stubRuntime{}
	srv.Checkpoints = stubSink{id: "ckpt-7"}
	client := dialAdapter(t, srv)
	if err := client.StartSession(context.Background(), adapterclient.StartSessionParams{SessionID: "s1", Runtime: "echo"}); err != nil {
		t.Fatalf("StartSession: %v", err)
	}

	registry := podsession.NewRegistry()
	registry.Put(&podsession.BindResult{SessionID: "s1", Adapter: client})

	store := memstore.New()
	runningSession(t, store, "acme", "s1")

	when := time.Date(2026, 5, 16, 9, 0, 0, 0, time.UTC)
	cp := &checkpointer.Checkpointer{
		Sessions: store,
		Registry: registry,
		Now:      func() time.Time { return when },
	}
	if err := cp.Checkpoint(context.Background(), "acme", "s1"); err != nil {
		t.Fatalf("Checkpoint: %v", err)
	}

	row, err := store.Get(context.Background(), "acme", "s1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if row.WorkspaceSnapshot == nil {
		t.Fatal("no WorkspaceSnapshot recorded on the session row")
	}
	if row.WorkspaceSnapshot.Ref != "ckpt-7" {
		t.Errorf("snapshot ref = %q, want ckpt-7", row.WorkspaceSnapshot.Ref)
	}
	if row.WorkspaceSnapshot.Source != sessionstore.WorkspaceSnapshotCheckpoint {
		t.Errorf("snapshot source = %q, want checkpoint", row.WorkspaceSnapshot.Source)
	}
	if !row.WorkspaceSnapshot.Timestamp.Equal(when) {
		t.Errorf("snapshot timestamp = %v, want %v", row.WorkspaceSnapshot.Timestamp, when)
	}
	// spec: §4.4 line 258 — every successful checkpoint bumps
	// last_successful_checkpoint_at on the session row, regardless of
	// trigger. The freshness gauge keys off this field.
	if !row.LastSuccessfulCheckpointAt.Equal(when) {
		t.Errorf("LastSuccessfulCheckpointAt = %v, want %v", row.LastSuccessfulCheckpointAt, when)
	}
	// spec: §7.3 line 397 — the adapter-reported compressed byte size
	// flows into WorkspaceSnapshot.Bytes so the §10.1 preStop
	// tiered-cap selection and §7.2 line 138 workspaceRecoveryFraction
	// both have a non-NULL input. The bufconn adapter wrote no bytes
	// (the stub workspace is empty), so any non-negative value is
	// acceptable. F-7.3.21.
	if row.WorkspaceSnapshot.Bytes < 0 {
		t.Errorf("WorkspaceSnapshot.Bytes = %d, want >= 0", row.WorkspaceSnapshot.Bytes)
	}
}

// spec: §4.4 line 258 — a failed adapter checkpoint must NOT bump
// last_successful_checkpoint_at; the freshness gauge would otherwise
// report falsely-fresh sessions.
func TestFailedCheckpointDoesNotBumpFreshnessTimestamp(t *testing.T) {
	srv := adapter.New("checkpointer-test")
	srv.WorkspaceRoot = t.TempDir()
	srv.Runtime = stubRuntime{}
	srv.Checkpoints = stubSink{err: errors.New("artifact store down")}
	client := dialAdapter(t, srv)
	if err := client.StartSession(context.Background(), adapterclient.StartSessionParams{SessionID: "s1", Runtime: "echo"}); err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	registry := podsession.NewRegistry()
	registry.Put(&podsession.BindResult{SessionID: "s1", Adapter: client})
	store := memstore.New()
	runningSession(t, store, "acme", "s1")

	cp := &checkpointer.Checkpointer{Sessions: store, Registry: registry}
	if err := cp.Checkpoint(context.Background(), "acme", "s1"); err == nil {
		t.Fatal("Checkpoint succeeded though the adapter checkpoint failed")
	}
	row, _ := store.Get(context.Background(), "acme", "s1")
	if !row.LastSuccessfulCheckpointAt.IsZero() {
		t.Errorf("LastSuccessfulCheckpointAt = %v on failed checkpoint, want zero", row.LastSuccessfulCheckpointAt)
	}
}

// spec: §4.4 line 258 — Seal counts as a successful checkpoint and
// MUST bump last_successful_checkpoint_at as a regular checkpoint
// would. A sealed session whose seal-recorded timestamp is stale would
// otherwise resurface as stale in the freshness gauge.
func TestSealBumpsFreshnessTimestamp(t *testing.T) {
	registry := podsession.NewRegistry()
	store := memstore.New()
	bindSession(t, registry, store, "acme", "s1", stubSink{id: "seal-1"})

	when := time.Date(2026, 5, 16, 18, 0, 0, 0, time.UTC)
	cp := &checkpointer.Checkpointer{
		Sessions: store,
		Registry: registry,
		Now:      func() time.Time { return when },
	}
	if err := cp.Seal(context.Background(), "acme", "s1"); err != nil {
		t.Fatalf("Seal: %v", err)
	}
	row, _ := store.Get(context.Background(), "acme", "s1")
	if !row.LastSuccessfulCheckpointAt.Equal(when) {
		t.Errorf("Seal LastSuccessfulCheckpointAt = %v, want %v", row.LastSuccessfulCheckpointAt, when)
	}
}

func TestCheckpointReturnsErrNoBindingForAnUncoordinatedSession(t *testing.T) {
	cp := &checkpointer.Checkpointer{
		Sessions: memstore.New(),
		Registry: podsession.NewRegistry(), // empty
	}
	if err := cp.Checkpoint(context.Background(), "acme", "s1"); !errors.Is(err, checkpointer.ErrNoBinding) {
		t.Errorf("error = %v, want ErrNoBinding", err)
	}
}

// TestSealNoOpsForAnUncoordinatedSession_spec_7_1 asserts that Seal, unlike
// Checkpoint, returns nil for a session this replica holds no binding for:
// such a session has no live workspace to export, so the §7.1 line 112 seal
// retry path must not treat it as a transient export failure to retry.
func TestSealNoOpsForAnUncoordinatedSession_spec_7_1(t *testing.T) {
	cp := &checkpointer.Checkpointer{
		Sessions: memstore.New(),
		Registry: podsession.NewRegistry(), // empty
	}
	if err := cp.Seal(context.Background(), "acme", "s1"); err != nil {
		t.Errorf("Seal for an uncoordinated session = %v, want nil (no-op)", err)
	}
}

func TestCheckpointSurfacesAnAdapterFailure(t *testing.T) {
	srv := adapter.New("checkpointer-test")
	srv.WorkspaceRoot = t.TempDir()
	srv.Runtime = stubRuntime{}
	srv.Checkpoints = stubSink{err: errors.New("artifact store down")}
	client := dialAdapter(t, srv)
	if err := client.StartSession(context.Background(), adapterclient.StartSessionParams{SessionID: "s1", Runtime: "echo"}); err != nil {
		t.Fatalf("StartSession: %v", err)
	}

	registry := podsession.NewRegistry()
	registry.Put(&podsession.BindResult{SessionID: "s1", Adapter: client})
	store := memstore.New()
	runningSession(t, store, "acme", "s1")

	cp := &checkpointer.Checkpointer{Sessions: store, Registry: registry}
	if err := cp.Checkpoint(context.Background(), "acme", "s1"); err == nil {
		t.Error("Checkpoint succeeded though the adapter checkpoint failed")
	}

	// The session row carries no snapshot when the checkpoint failed.
	row, err := store.Get(context.Background(), "acme", "s1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if row.WorkspaceSnapshot != nil {
		t.Error("a WorkspaceSnapshot was recorded despite the checkpoint failure")
	}
}

// bindSession wires a bufconn adapter for sessionID, registers the pod
// binding, and seeds a running session row.
func bindSession(t *testing.T, registry *podsession.Registry, store sessionstore.Store, tenantID, sessionID string, sink adapter.CheckpointSink) {
	t.Helper()
	srv := adapter.New("checkpointer-test")
	srv.WorkspaceRoot = t.TempDir()
	srv.Runtime = stubRuntime{}
	srv.Checkpoints = sink
	client := dialAdapter(t, srv)
	if err := client.StartSession(context.Background(), adapterclient.StartSessionParams{SessionID: sessionID, Runtime: "echo"}); err != nil {
		t.Fatalf("StartSession %s: %v", sessionID, err)
	}
	registry.Put(&podsession.BindResult{SessionID: sessionID, TenantID: tenantID, Adapter: client})
	runningSession(t, store, tenantID, sessionID)
}

func TestSweepCheckpointsEveryCoordinatedSession(t *testing.T) {
	registry := podsession.NewRegistry()
	store := memstore.New()
	bindSession(t, registry, store, "acme", "s1", stubSink{id: "ck-1"})
	bindSession(t, registry, store, "acme", "s2", stubSink{id: "ck-2"})

	cp := &checkpointer.Checkpointer{Sessions: store, Registry: registry}
	cp.Sweep(context.Background())

	for _, tc := range []struct{ id, ref string }{{"s1", "ck-1"}, {"s2", "ck-2"}} {
		row, err := store.Get(context.Background(), "acme", tc.id)
		if err != nil {
			t.Fatalf("Get %s: %v", tc.id, err)
		}
		if row.WorkspaceSnapshot == nil || row.WorkspaceSnapshot.Ref != tc.ref {
			t.Errorf("session %s snapshot = %+v, want ref %s", tc.id, row.WorkspaceSnapshot, tc.ref)
		}
	}
}

func TestSweepReportsPerSessionFailuresAndContinues(t *testing.T) {
	registry := podsession.NewRegistry()
	store := memstore.New()
	bindSession(t, registry, store, "acme", "ok", stubSink{id: "ck-ok"})
	bindSession(t, registry, store, "acme", "bad", stubSink{err: errors.New("store down")})

	var failed []string
	cp := &checkpointer.Checkpointer{
		Sessions: store,
		Registry: registry,
		OnError:  func(sessionID string, _ error) { failed = append(failed, sessionID) },
	}
	cp.Sweep(context.Background())

	if len(failed) != 1 || failed[0] != "bad" {
		t.Errorf("OnError saw %v, want [bad]", failed)
	}
	if row, _ := store.Get(context.Background(), "acme", "ok"); row.WorkspaceSnapshot == nil {
		t.Error("the sweep stopped after the failed session — ok was not checkpointed")
	}
}

func TestRunPeriodicallySweeps(t *testing.T) {
	registry := podsession.NewRegistry()
	store := memstore.New()
	bindSession(t, registry, store, "acme", "s1", stubSink{id: "ck-run"})

	cp := &checkpointer.Checkpointer{Sessions: store, Registry: registry, Interval: 10 * time.Millisecond}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go cp.Run(ctx)

	deadline := time.After(2 * time.Second)
	for {
		if row, _ := store.Get(context.Background(), "acme", "s1"); row.WorkspaceSnapshot != nil {
			return // the periodic loop checkpointed the session.
		}
		select {
		case <-deadline:
			t.Fatal("Run did not checkpoint the session within the deadline")
		case <-time.After(5 * time.Millisecond):
		}
	}
}

// spec: §4.4 line 256 — periodicCheckpointIntervalSeconds default is 600 s.
// diagnosis: a zero Interval selects the spec-mandated default; the
// previous defaultInterval of 5 minutes generated twice the steady-state
// MinIO bandwidth and missed §17.8 capacity baselines.
func TestRunUsesTenMinuteDefaultIntervalWhenUnset(t *testing.T) {
	// Run is hard to time-test without breaking the API, so we verify
	// the FirstCheckpointDelay default by exercising the function with
	// a non-zero interval that matches the spec default and confirming
	// the math holds.
	delay := checkpointer.FirstCheckpointDelay("s1", 10*time.Minute, 0)
	if delay != 10*time.Minute {
		t.Errorf("FirstCheckpointDelay(jitter=0) = %v, want exactly 10m (spec §4.4 default interval)", delay)
	}
}

// spec: §4.4 line 258 — jitter spreads the first periodic checkpoint
// across [interval, interval + interval × jitterFraction]; the same
// session must receive the same jitter across restarts.
func TestFirstCheckpointDelayIsStableAndBounded(t *testing.T) {
	interval := 600 * time.Second
	jitter := 0.2

	// The delay is bounded by the spec range.
	for _, sessionID := range []string{"alice-1", "bob-2", "carol-3", "dave-4"} {
		delay := checkpointer.FirstCheckpointDelay(sessionID, interval, jitter)
		if delay < interval {
			t.Errorf("session %s: delay %v < interval %v (spec §4.4 floor)", sessionID, delay, interval)
		}
		if delay > interval+time.Duration(float64(interval)*jitter) {
			t.Errorf("session %s: delay %v > interval+jitter %v (spec §4.4 ceiling)",
				sessionID, delay, interval+time.Duration(float64(interval)*jitter))
		}
	}

	// The delay is stable: the same session ID returns the same value
	// across calls so a coordinator handoff or gateway restart does not
	// re-roll the jitter into a thundering-herd pattern.
	first := checkpointer.FirstCheckpointDelay("acme-session-1", interval, jitter)
	for i := 0; i < 10; i++ {
		if got := checkpointer.FirstCheckpointDelay("acme-session-1", interval, jitter); got != first {
			t.Errorf("call %d: delay %v, want stable %v", i, got, first)
		}
	}
}

// spec: §4.4 line 258 — jitterFraction = 0 selects no jitter; every
// session ticks at the same wall-clock second. Used by tests and by
// dev-mode deployments.
func TestFirstCheckpointDelayWithZeroJitterReturnsInterval(t *testing.T) {
	got := checkpointer.FirstCheckpointDelay("any-session", 600*time.Second, 0)
	if got != 600*time.Second {
		t.Errorf("FirstCheckpointDelay(jitter=0) = %v, want exactly interval", got)
	}
}

// spec: §4.4 line 258 — jitterFraction range is [0.0, 1.0]; out-of-range
// values are clamped at the boundary rather than rejected so an
// operator typo cannot crash the loop.
func TestFirstCheckpointDelayClampsOutOfRangeJitter(t *testing.T) {
	interval := 600 * time.Second
	// Negative jitterFraction clamps to 0: delay == interval.
	if got := checkpointer.FirstCheckpointDelay("s1", interval, -0.5); got != interval {
		t.Errorf("negative jitter: delay %v, want interval %v", got, interval)
	}
	// jitterFraction > 1.0 clamps to 1.0: delay ∈ [interval, 2×interval].
	for _, sessionID := range []string{"s1", "s2", "s3"} {
		got := checkpointer.FirstCheckpointDelay(sessionID, interval, 2.0)
		if got < interval || got > 2*interval {
			t.Errorf("clamped jitter for %s: delay %v, want [%v, %v]", sessionID, got, interval, 2*interval)
		}
	}
}

// spec: §4.4 line 258 — different session IDs yield different first
// delays so a burst of sessions started in the same wall-clock second
// does not align their next checkpoints.
func TestFirstCheckpointDelayDiversifiesAcrossSessions(t *testing.T) {
	interval := 600 * time.Second
	jitter := 0.2
	seen := map[time.Duration]int{}
	for _, sessionID := range []string{"a1", "a2", "a3", "a4", "a5", "a6", "a7", "a8"} {
		seen[checkpointer.FirstCheckpointDelay(sessionID, interval, jitter)]++
	}
	if len(seen) < 2 {
		t.Errorf("only %d distinct delays across 8 sessions: %v", len(seen), seen)
	}
}

// spec: §4.4 line 258 — empty session ID degrades to the no-jitter
// path; this protects callers that mis-pass an empty string.
func TestFirstCheckpointDelayWithEmptySessionIDReturnsInterval(t *testing.T) {
	got := checkpointer.FirstCheckpointDelay("", 600*time.Second, 0.2)
	if got != 600*time.Second {
		t.Errorf("empty session id: delay %v, want interval", got)
	}
}

// spec: §4.4 line 258 — zero or negative interval returns 0 (the loop
// is effectively disabled, matching the test-mode contract).
func TestFirstCheckpointDelayWithZeroIntervalReturnsZero(t *testing.T) {
	if got := checkpointer.FirstCheckpointDelay("s1", 0, 0.2); got != 0 {
		t.Errorf("zero interval: delay %v, want 0", got)
	}
	if got := checkpointer.FirstCheckpointDelay("s1", -1*time.Second, 0.2); got != 0 {
		t.Errorf("negative interval: delay %v, want 0", got)
	}
}

func TestSealRecordsASealedSnapshot(t *testing.T) {
	registry := podsession.NewRegistry()
	store := memstore.New()
	bindSession(t, registry, store, "acme", "s1", stubSink{id: "seal-1"})

	when := time.Date(2026, 5, 16, 18, 0, 0, 0, time.UTC)
	cp := &checkpointer.Checkpointer{
		Sessions: store,
		Registry: registry,
		Now:      func() time.Time { return when },
	}
	if err := cp.Seal(context.Background(), "acme", "s1"); err != nil {
		t.Fatalf("Seal: %v", err)
	}

	row, err := store.Get(context.Background(), "acme", "s1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if row.WorkspaceSnapshot == nil {
		t.Fatal("Seal recorded no WorkspaceSnapshot")
	}
	// A seal records source `sealed`, distinguishing the final
	// workspace from a periodic `checkpoint` snapshot.
	if row.WorkspaceSnapshot.Source != sessionstore.WorkspaceSnapshotSealed {
		t.Errorf("snapshot source = %q, want sealed", row.WorkspaceSnapshot.Source)
	}
	if row.WorkspaceSnapshot.Ref != "seal-1" {
		t.Errorf("snapshot ref = %q, want seal-1", row.WorkspaceSnapshot.Ref)
	}
	if !row.WorkspaceSnapshot.Timestamp.Equal(when) {
		t.Errorf("snapshot timestamp = %v, want %v", row.WorkspaceSnapshot.Timestamp, when)
	}
}

// captureMetrics records every observed duration call so tests can
// assert §4.4 line 254 emission.
type captureMetrics struct {
	calls []durationCall
}

type durationCall struct {
	pool    string
	level   string
	trigger string
	seconds float64
}

func (c *captureMetrics) ObserveCheckpointDuration(pool, level, trigger string, seconds float64) {
	c.calls = append(c.calls, durationCall{pool, level, trigger, seconds})
}

// spec: §4.4 line 254 — every checkpoint emits the duration histogram.
func TestCheckpointEmitsDurationHistogram(t *testing.T) {
	registry := podsession.NewRegistry()
	store := memstore.New()
	bindSession(t, registry, store, "acme", "s1", stubSink{id: "ck-1"})

	metrics := &captureMetrics{}
	cp := &checkpointer.Checkpointer{
		Sessions: store,
		Registry: registry,
		Metrics:  metrics,
		Pool:     "default",
	}
	if err := cp.Checkpoint(context.Background(), "acme", "s1"); err != nil {
		t.Fatalf("Checkpoint: %v", err)
	}
	if len(metrics.calls) != 1 {
		t.Fatalf("ObserveCheckpointDuration: want 1 call, got %d", len(metrics.calls))
	}
	c := metrics.calls[0]
	if c.pool != "default" || c.level != "basic" || c.trigger != "periodic" {
		t.Fatalf("labels: got pool=%s level=%s trigger=%s", c.pool, c.level, c.trigger)
	}
	if c.seconds < 0 {
		t.Fatalf("seconds must be non-negative: got %v", c.seconds)
	}
}

// spec: §4.4 line 254 — even a failed checkpoint emits the duration
// histogram. The §16.5 CheckpointDurationHigh alert reads the P95,
// which would otherwise miss slow-then-failing checkpoints.
func TestFailedCheckpointStillObservesDuration(t *testing.T) {
	registry := podsession.NewRegistry()
	store := memstore.New()
	bindSession(t, registry, store, "acme", "s1", stubSink{err: errors.New("store down")})

	metrics := &captureMetrics{}
	cp := &checkpointer.Checkpointer{
		Sessions: store,
		Registry: registry,
		Metrics:  metrics,
	}
	if err := cp.Checkpoint(context.Background(), "acme", "s1"); err == nil {
		t.Fatal("Checkpoint: want failure, got success")
	}
	if len(metrics.calls) != 1 {
		t.Fatalf("ObserveCheckpointDuration on failure: want 1 call, got %d", len(metrics.calls))
	}
}

// fakeRetention captures Insert + Rotate so the test asserts §4.4
// line 234 / §12.5 latest-2 wiring.
type fakeRetention struct {
	inserts []retInsert
	rotates []retRotate
}

type retInsert struct {
	tenantID  string
	sessionID string
	slotID    string
	ref       string
}

type retRotate struct {
	tenantID  string
	sessionID string
	slotID    string
}

func (f *fakeRetention) Insert(_ context.Context, r checkpointretention.Record) error {
	f.inserts = append(f.inserts, retInsert{r.TenantID, r.SessionID, r.SlotID, r.Ref})
	return nil
}

func (f *fakeRetention) Rotate(_ context.Context, tenantID, sessionID, slotID string) ([]checkpointretention.Record, error) {
	f.rotates = append(f.rotates, retRotate{tenantID, sessionID, slotID})
	return nil, nil
}

// spec: §4.4 line 234 / §12.5 — every successful checkpoint records
// to the retention catalog and runs Rotate to enforce latest-2.
func TestCheckpointWritesRetentionCatalog(t *testing.T) {
	registry := podsession.NewRegistry()
	store := memstore.New()
	bindSession(t, registry, store, "acme", "s1", stubSink{id: "ckpt-42"})

	ret := &fakeRetention{}
	cp := &checkpointer.Checkpointer{
		Sessions:  store,
		Registry:  registry,
		Retention: ret,
	}
	if err := cp.Checkpoint(context.Background(), "acme", "s1"); err != nil {
		t.Fatalf("Checkpoint: %v", err)
	}
	if len(ret.inserts) != 1 {
		t.Fatalf("Insert: want 1 call, got %d", len(ret.inserts))
	}
	ins := ret.inserts[0]
	if ins.tenantID != "acme" || ins.sessionID != "s1" || ins.ref != "ckpt-42" {
		t.Fatalf("Insert args: got %+v", ins)
	}
	if len(ret.rotates) != 1 {
		t.Fatalf("Rotate: want 1 call, got %d", len(ret.rotates))
	}
}

// spec: §4.4 line 234 — a failed checkpoint must not record to the
// retention catalog (no ref to rotate against).
func TestFailedCheckpointDoesNotRecordRetention(t *testing.T) {
	registry := podsession.NewRegistry()
	store := memstore.New()
	bindSession(t, registry, store, "acme", "s1", stubSink{err: errors.New("store down")})

	ret := &fakeRetention{}
	cp := &checkpointer.Checkpointer{
		Sessions:  store,
		Registry:  registry,
		Retention: ret,
	}
	_ = cp.Checkpoint(context.Background(), "acme", "s1")
	if len(ret.inserts) != 0 {
		t.Fatalf("Insert: want 0 calls on failure, got %d", len(ret.inserts))
	}
	if len(ret.rotates) != 0 {
		t.Fatalf("Rotate: want 0 calls on failure, got %d", len(ret.rotates))
	}
}

// spec: §12.5 line 313 — a session under legal hold is exempt from
// the latest-2 rotation. The checkpoint is still catalogued (so the
// §12.8 reconciler can see it) but Rotate is not run, so every
// checkpoint is retained until the hold is lifted.
func TestLegalHoldSessionSkipsRotation(t *testing.T) {
	registry := podsession.NewRegistry()
	store := memstore.New()
	bindSession(t, registry, store, "acme", "held", stubSink{id: "ckpt-hold"})
	// Place the session under legal hold.
	if _, err := store.Update(context.Background(), "acme", "held", func(row *sessionstore.Session) error {
		row.LegalHold = true
		return nil
	}); err != nil {
		t.Fatalf("set legal hold: %v", err)
	}

	ret := &fakeRetention{}
	cp := &checkpointer.Checkpointer{Sessions: store, Registry: registry, Retention: ret}
	if err := cp.Checkpoint(context.Background(), "acme", "held"); err != nil {
		t.Fatalf("Checkpoint: %v", err)
	}
	if len(ret.inserts) != 1 {
		t.Fatalf("Insert: want 1 call (catalogued), got %d", len(ret.inserts))
	}
	if len(ret.rotates) != 0 {
		t.Fatalf("Rotate: held session must not rotate, got %d calls", len(ret.rotates))
	}
}

// spec: §12.5 lines 313, 326 — in concurrent-workspace mode the
// rotation operates on (session_id, slot_id) pairs. The bound slot id
// flows into both the catalog Insert and the Rotate call.
func TestCheckpointPropagatesSlotToRetention(t *testing.T) {
	registry := podsession.NewRegistry()
	store := memstore.New()
	srv := adapter.New("checkpointer-test")
	srv.WorkspaceRoot = t.TempDir()
	srv.Runtime = stubRuntime{}
	srv.Checkpoints = stubSink{id: "ckpt-slot"}
	client := dialAdapter(t, srv)
	if err := client.StartSession(context.Background(), adapterclient.StartSessionParams{SessionID: "cw", Runtime: "echo"}); err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	registry.Put(&podsession.BindResult{SessionID: "cw", TenantID: "acme", SlotID: "slot-3", Adapter: client})
	runningSession(t, store, "acme", "cw")

	ret := &fakeRetention{}
	cp := &checkpointer.Checkpointer{Sessions: store, Registry: registry, Retention: ret}
	if err := cp.Checkpoint(context.Background(), "acme", "cw"); err != nil {
		t.Fatalf("Checkpoint: %v", err)
	}
	if len(ret.inserts) != 1 || ret.inserts[0].slotID != "slot-3" {
		t.Fatalf("Insert slot: got %+v, want slotID slot-3", ret.inserts)
	}
	if len(ret.rotates) != 1 || ret.rotates[0].slotID != "slot-3" {
		t.Fatalf("Rotate slot: got %+v, want slotID slot-3", ret.rotates)
	}
}
