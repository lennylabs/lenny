// SPDX-License-Identifier: MIT

package barrier

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/session/sessioncheckpointmeta"
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

type fakeMetrics struct {
	targetSources []string
}

func newFakeMetrics() *fakeMetrics { return &fakeMetrics{} }

func (f *fakeMetrics) IncPreStopBarrierTargetSource(source string) {
	f.targetSources = append(f.targetSources, source)
}

// spec: §10.1 lines 165-166 — Dispatch sends a barrier to every target,
// persists each ack's barrier_id and checkpoint_ref into
// session_checkpoint_meta, and emits the target-source counter once for
// the pass.
func TestDispatchHappyPath_spec_10_1(t *testing.T) {
	ctx := context.Background()
	meta := sessioncheckpointmeta.NewMemoryStore(nil)
	disp := newFakeDispatcher()
	disp.acks["s1"] = Ack{CheckpointRef: "ck1"}
	disp.acks["s2"] = Ack{CheckpointRef: "ck2"}
	mx := newFakeMetrics()
	c := New(&fakeLister{
		targets: []Target{
			{TenantID: "acme", SessionID: "s1", CoordinationGeneration: 2, PodAddr: "10.0.0.1"},
			{TenantID: "acme", SessionID: "s2", CoordinationGeneration: 2, PodAddr: "10.0.0.2"},
		},
		source: SourcePostgres,
	}, disp, meta, mx)

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
	if got.CheckpointRef != "ck1" || got.BarrierID != "1" {
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
	disp.acks["s1"] = Ack{CheckpointRef: "ck1"}
	lister := &fakeLister{targets: []Target{{TenantID: "acme", SessionID: "s1", CoordinationGeneration: 1}}, source: SourcePostgres}
	c := New(lister, disp, meta, nil)

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
	disp.acks["s2"] = Ack{CheckpointRef: "ck2"}
	c := New(&fakeLister{
		targets: []Target{
			{TenantID: "acme", SessionID: "s1"},
			{TenantID: "acme", SessionID: "s2"},
		},
		source: SourcePostgres,
	}, disp, meta, nil)

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
	c := New(&fakeLister{targets: []Target{{TenantID: "acme", SessionID: "s1"}}, source: SourceCacheFallback}, disp, meta, nil)
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
		sessioncheckpointmeta.NewMemoryStore(nil), mx)
	if _, err := c.Dispatch(context.Background()); err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if len(mx.targetSources) != 1 || mx.targetSources[0] != SourceCacheFallback {
		t.Errorf("want one cache_fallback emission, got %v", mx.targetSources)
	}
}

func TestDispatchListerError(t *testing.T) {
	c := New(&fakeLister{err: errors.New("pg down")}, newFakeDispatcher(),
		sessioncheckpointmeta.NewMemoryStore(nil), nil)
	if _, err := c.Dispatch(context.Background()); err == nil {
		t.Error("lister error should propagate")
	}
}

// TestNoResumeDedupSurface_spec_10_1 pins the §10.1 reconciliation
// (proposal 0026): the gateway is not in the per-tool-call dispatch
// path (§4.7) and a coordinator handoff re-adopts the still-running pod
// without a checkpoint restore (§4.2), so no gateway-side
// resume-deduplication surface exists. A Coordinator method named
// ResumeDedup, a DedupDecision or ManifestReader type, a
// SourceCheckpointManifest const, or an Ack/persisted field carrying a
// tool-call sequence would all be a resume-dedup consumer this
// reconciliation removed; each absence would fail against the pre-fix
// code, which declared them.
//
// spec: §10.1 lines 165-166 (no gateway-side resume-deduplication on
// handoff), §4.2 (handoff re-adopts the live pod), §4.7 (gateway not in
// the per-tool-call dispatch path).
func TestNoResumeDedupSurface_spec_10_1(t *testing.T) {
	coordT := reflect.TypeOf((*Coordinator)(nil))
	if _, ok := coordT.MethodByName("ResumeDedup"); ok {
		t.Error("Coordinator.ResumeDedup must not exist: the gateway performs no resume-deduplication on handoff (§10.1, proposal 0026)")
	}

	// Ack is the persisted CheckpointBarrierAck. It must not carry a
	// tool-call id or sequence: no gateway consumer reads them, and the
	// always-zero sequence was the dead resume-dedup input.
	ackT := reflect.TypeOf(Ack{})
	for _, forbidden := range []string{"LastToolCallID", "LastToolCallSequence"} {
		if _, ok := ackT.FieldByName(forbidden); ok {
			t.Errorf("Ack.%s must not exist: no gateway resume-dedup consumer reads it (§10.1, proposal 0026)", forbidden)
		}
	}

	// The Metrics interface must not carry the resume-dedup counter
	// method, whose only increment site was the removed ResumeDedup.
	metricsT := reflect.TypeOf((*Metrics)(nil)).Elem()
	if _, ok := metricsT.MethodByName("AddResumeDeduplicated"); ok {
		t.Error("Metrics.AddResumeDeduplicated must not exist: its only increment site was the removed ResumeDedup (§10.1, proposal 0026)")
	}
}
