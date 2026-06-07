// SPDX-License-Identifier: MIT

package barrier

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/sessioncheckpointmeta"
)

// fakeLister returns a fixed target set and source.
type fakeLister struct {
	targets []Target
	source  string
	err     error
}

func (f *fakeLister) Targets(context.Context) ([]Target, string, error) {
	return f.targets, f.source, f.err
}

// fakeDispatcher returns a per-session ack or error and records the
// barrier ids it was called with.
type fakeDispatcher struct {
	mu      sync.Mutex
	acks    map[string]Ack
	errs    map[string]error
	seenIDs map[string]string
}

func newFakeDispatcher() *fakeDispatcher {
	return &fakeDispatcher{acks: map[string]Ack{}, errs: map[string]error{}, seenIDs: map[string]string{}}
}

func (f *fakeDispatcher) Send(_ context.Context, t Target, barrierID string) (Ack, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.seenIDs[t.SessionID] = barrierID
	if err := f.errs[t.SessionID]; err != nil {
		return Ack{}, err
	}
	return f.acks[t.SessionID], nil
}

type fakeManifest struct {
	acks map[string]Ack
}

func (f *fakeManifest) BarrierMeta(_ context.Context, _, sessionID string) (Ack, bool, error) {
	a, ok := f.acks[sessionID]
	return a, ok, nil
}

type fakeMetrics struct {
	targetSources []string
	dedup         map[string]int
}

func newFakeMetrics() *fakeMetrics { return &fakeMetrics{dedup: map[string]int{}} }

func (f *fakeMetrics) IncPreStopBarrierTargetSource(source string) {
	f.targetSources = append(f.targetSources, source)
}
func (f *fakeMetrics) AddResumeDeduplicated(source string, n int) { f.dedup[source] += n }

// spec: §10.1 lines 165-178 — Dispatch sends a barrier to every target,
// persists each ack into session_checkpoint_meta, and emits the
// target-source counter once for the pass.
func TestDispatchHappyPath_spec_10_1(t *testing.T) {
	ctx := context.Background()
	meta := sessioncheckpointmeta.NewMemoryStore(nil)
	disp := newFakeDispatcher()
	disp.acks["s1"] = Ack{LastToolCallID: "tc-4", LastToolCallSequence: 4, CheckpointRef: "ck1"}
	disp.acks["s2"] = Ack{LastToolCallID: "tc-7", LastToolCallSequence: 7, CheckpointRef: "ck2"}
	mx := newFakeMetrics()
	c := New(&fakeLister{
		targets: []Target{
			{TenantID: "acme", SessionID: "s1", CoordinationGeneration: 2, PodAddr: "10.0.0.1"},
			{TenantID: "acme", SessionID: "s2", CoordinationGeneration: 2, PodAddr: "10.0.0.2"},
		},
		source: SourcePostgres,
	}, disp, meta, nil, mx)

	sum, err := c.Dispatch(ctx)
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if sum.Source != SourcePostgres {
		t.Errorf("source = %q", sum.Source)
	}
	if len(mx.targetSources) != 1 || mx.targetSources[0] != SourcePostgres {
		t.Errorf("target-source counter = %v", mx.targetSources)
	}
	for _, o := range sum.Outcomes {
		if !o.Acked || o.Err != nil || o.Stale {
			t.Errorf("outcome %s not cleanly acked: %+v", o.Target.SessionID, o)
		}
	}
	got, err := meta.Get(ctx, "acme", "s1")
	if err != nil {
		t.Fatalf("meta Get s1: %v", err)
	}
	if got.LastToolCallSequence != 4 || got.CheckpointRef != "ck1" || got.BarrierID != "1" {
		t.Errorf("s1 meta = %+v", got)
	}
}

// spec: §10.1 line 165 — the barrier id is monotonically increasing
// per session: a second barrier reads the prior persisted id and
// advances it.
func TestDispatchBarrierIDMonotonic_spec_10_1(t *testing.T) {
	ctx := context.Background()
	meta := sessioncheckpointmeta.NewMemoryStore(nil)
	disp := newFakeDispatcher()
	disp.acks["s1"] = Ack{LastToolCallSequence: 1}
	lister := &fakeLister{targets: []Target{{TenantID: "acme", SessionID: "s1", CoordinationGeneration: 1}}, source: SourcePostgres}
	c := New(lister, disp, meta, nil, nil)

	if _, err := c.Dispatch(ctx); err != nil {
		t.Fatalf("first Dispatch: %v", err)
	}
	if disp.seenIDs["s1"] != "1" {
		t.Errorf("first barrier id = %q, want 1", disp.seenIDs["s1"])
	}
	if _, err := c.Dispatch(ctx); err != nil {
		t.Fatalf("second Dispatch: %v", err)
	}
	if disp.seenIDs["s1"] != "2" {
		t.Errorf("second barrier id = %q, want 2", disp.seenIDs["s1"])
	}
}

// spec: §10.1 line 165 — a pod that rejects the barrier as
// generation-stale (a false-positive surviving the cache fallback) is
// recorded as Stale and does not abort the drain; its meta is not
// written.
func TestDispatchGenerationStaleNonFatal_spec_10_1(t *testing.T) {
	ctx := context.Background()
	meta := sessioncheckpointmeta.NewMemoryStore(nil)
	disp := newFakeDispatcher()
	disp.errs["s1"] = ErrGenerationStale
	disp.acks["s2"] = Ack{LastToolCallSequence: 5}
	c := New(&fakeLister{
		targets: []Target{
			{TenantID: "acme", SessionID: "s1"},
			{TenantID: "acme", SessionID: "s2"},
		},
		source: SourcePostgres,
	}, disp, meta, nil, nil)

	sum, err := c.Dispatch(ctx)
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	byID := map[string]Outcome{}
	for _, o := range sum.Outcomes {
		byID[o.Target.SessionID] = o
	}
	if !byID["s1"].Stale || byID["s1"].Acked {
		t.Errorf("s1 should be stale not acked: %+v", byID["s1"])
	}
	if _, err := meta.Get(ctx, "acme", "s1"); !errors.Is(err, sessioncheckpointmeta.ErrNotFound) {
		t.Error("stale target must not persist meta")
	}
	if !byID["s2"].Acked {
		t.Errorf("s2 should still ack despite s1 stale: %+v", byID["s2"])
	}
}

// spec: §10.1 — a transport/deadline error on one target is recorded
// and does not abort the pass.
func TestDispatchTransportErrorNonFatal_spec_10_1(t *testing.T) {
	ctx := context.Background()
	meta := sessioncheckpointmeta.NewMemoryStore(nil)
	disp := newFakeDispatcher()
	disp.errs["s1"] = errors.New("deadline exceeded")
	c := New(&fakeLister{targets: []Target{{TenantID: "acme", SessionID: "s1"}}, source: SourceCacheFallback}, disp, meta, nil, nil)
	sum, err := c.Dispatch(ctx)
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if sum.Outcomes[0].Err == nil || sum.Outcomes[0].Acked {
		t.Errorf("transport error not recorded: %+v", sum.Outcomes[0])
	}
}

// spec: §10.1 line 165 — the degraded cache-fallback target source is
// reported on the counter.
func TestDispatchCacheFallbackSource_spec_10_1(t *testing.T) {
	mx := newFakeMetrics()
	c := New(&fakeLister{targets: nil, source: SourceCacheFallback}, newFakeDispatcher(),
		sessioncheckpointmeta.NewMemoryStore(nil), nil, mx)
	if _, err := c.Dispatch(context.Background()); err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if len(mx.targetSources) != 1 || mx.targetSources[0] != SourceCacheFallback {
		t.Errorf("want one cache_fallback emission, got %v", mx.targetSources)
	}
}

func TestDispatchListerError(t *testing.T) {
	c := New(&fakeLister{err: errors.New("pg down")}, newFakeDispatcher(),
		sessioncheckpointmeta.NewMemoryStore(nil), nil, nil)
	if _, err := c.Dispatch(context.Background()); err == nil {
		t.Error("lister error should propagate")
	}
}

// spec: §10.1 line 179 — on handoff the new coordinator reads
// session_checkpoint_meta and skips every tool call at or below the
// prior pod's last completed sequence.
func TestResumeDedupFromPostgres_spec_10_1(t *testing.T) {
	ctx := context.Background()
	meta := sessioncheckpointmeta.NewMemoryStore(nil)
	_ = meta.Upsert(ctx, sessioncheckpointmeta.Record{TenantID: "acme", SessionID: "s1", LastToolCallID: "tc-9", LastToolCallSequence: 9})
	mx := newFakeMetrics()
	c := New(nil, nil, meta, nil, mx)

	d, err := c.ResumeDedup(ctx, "acme", "s1", 0)
	if err != nil {
		t.Fatalf("ResumeDedup: %v", err)
	}
	if d.Source != SourcePostgres || d.LastCompletedSequence != 9 || d.SkippedCount != 9 || d.FirstSequenceToRedispatch != 10 {
		t.Errorf("decision = %+v", d)
	}
	if mx.dedup[SourcePostgres] != 9 {
		t.Errorf("postgres dedup counter = %d, want 9", mx.dedup[SourcePostgres])
	}
}

// spec: §10.1 line 178/179 — the skipped count is bounded by the calls
// this coordinator actually dispatched, so a stale-higher persisted
// value cannot over-count.
func TestResumeDedupBoundedByOwnDispatch_spec_10_1(t *testing.T) {
	ctx := context.Background()
	meta := sessioncheckpointmeta.NewMemoryStore(nil)
	_ = meta.Upsert(ctx, sessioncheckpointmeta.Record{TenantID: "acme", SessionID: "s1", LastToolCallSequence: 9})
	c := New(nil, nil, meta, nil, nil)
	d, err := c.ResumeDedup(ctx, "acme", "s1", 5)
	if err != nil {
		t.Fatalf("ResumeDedup: %v", err)
	}
	if d.SkippedCount != 5 {
		t.Errorf("skipped = %d, want 5 (bounded by own dispatch)", d.SkippedCount)
	}
	// FirstSequenceToRedispatch still reflects the prior pod's progress.
	if d.FirstSequenceToRedispatch != 10 {
		t.Errorf("first-to-redispatch = %d, want 10", d.FirstSequenceToRedispatch)
	}
}

// spec: §10.1 line 179 — when session_checkpoint_meta is absent the
// resume path falls back to the MinIO checkpoint manifest barrier_meta.
func TestResumeDedupManifestFallback_spec_10_1(t *testing.T) {
	ctx := context.Background()
	meta := sessioncheckpointmeta.NewMemoryStore(nil)
	man := &fakeManifest{acks: map[string]Ack{"s1": {LastToolCallSequence: 3}}}
	mx := newFakeMetrics()
	c := New(nil, nil, meta, man, mx)

	d, err := c.ResumeDedup(ctx, "acme", "s1", 0)
	if err != nil {
		t.Fatalf("ResumeDedup: %v", err)
	}
	if d.Source != SourceCheckpointManifest || d.SkippedCount != 3 || d.FirstSequenceToRedispatch != 4 {
		t.Errorf("decision = %+v", d)
	}
	if mx.dedup[SourceCheckpointManifest] != 3 {
		t.Errorf("manifest dedup counter = %d, want 3", mx.dedup[SourceCheckpointManifest])
	}
}

// spec: §10.1 line 179 — no durable barrier metadata anywhere means
// the new coordinator re-dispatches from the first call and skips
// nothing.
func TestResumeDedupNoMetaRedispatchesAll_spec_10_1(t *testing.T) {
	ctx := context.Background()
	meta := sessioncheckpointmeta.NewMemoryStore(nil)
	man := &fakeManifest{acks: map[string]Ack{}}
	c := New(nil, nil, meta, man, nil)
	d, err := c.ResumeDedup(ctx, "acme", "missing", 4)
	if err != nil {
		t.Fatalf("ResumeDedup: %v", err)
	}
	if d.Source != "" || d.SkippedCount != 0 || d.FirstSequenceToRedispatch != 1 {
		t.Errorf("decision = %+v, want re-dispatch from 1 with no source", d)
	}
}

func TestResumeDedupNoMetaNoManifest(t *testing.T) {
	meta := sessioncheckpointmeta.NewMemoryStore(nil)
	c := New(nil, nil, meta, nil, nil)
	d, err := c.ResumeDedup(context.Background(), "acme", "missing", 0)
	if err != nil {
		t.Fatalf("ResumeDedup: %v", err)
	}
	if d.SkippedCount != 0 {
		t.Errorf("skipped = %d, want 0", d.SkippedCount)
	}
}
