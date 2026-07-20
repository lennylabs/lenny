// SPDX-License-Identifier: MIT

package events

import (
	"context"
	"errors"
	"testing"

	"github.com/redis/go-redis/v9"

	gwevents "github.com/lennylabs/lenny/pkg/events"
)

// spec: 25.5 (best-effort recovery flush, eventKey dedup) — on a Redis
// down-to-up edge the replica re-emits the lenny-ops-originated events it
// buffered locally during the outage to the recovered ops:events:stream, and
// skips any event whose eventKey is already on the stream so a consumer that
// already saw it is not re-delivered. This pins that only the outage-window
// event is re-emitted, not the one already present on the stream.
func TestFlushBufferedToRedis_ReEmitsOnlyAbsent_spec_25_5(t *testing.T) {
	// The recovered stream already retains X (it reached Redis before the
	// outage). The local ring holds X and Y; Y was emitted during the outage
	// and never reached Redis.
	f := &fakeStream{}
	f.add("1-0", evt("ops:1000:1", "dev.lenny.drift_detected")) // X, already on the stream
	s := New(Options{RedisClient: f, SourceHealth: newMutableHealth(true, true), Now: ts})
	s.buffer.Append(evt("ops:1000:1", "dev.lenny.drift_detected")) // X (already present)
	s.MarkRedisOutage()
	s.buffer.Append(evt("ops:1000:2", "dev.lenny.escalation_created")) // Y (outage window)

	var reEmitted []string
	s.SetRedisReEmitter(func(_ context.Context, e gwevents.OperationalEvent) error {
		reEmitted = append(reEmitted, e.ID)
		return nil
	})

	n, err := s.FlushBufferedToRedis(context.Background())
	if err != nil {
		t.Fatalf("flush error: %v", err)
	}
	if n != 1 || len(reEmitted) != 1 || reEmitted[0] != "ops:1000:2" {
		t.Fatalf("flush re-emitted %v (n=%d); want only the outage-window event ops:1000:2", reEmitted, n)
	}
}

// spec: 25.5 (best-effort recovery flush) — a re-emit failure is logged and
// does not stop the flush: the remaining buffered events are still re-emitted
// and the last failure is returned. This pins the best-effort, log-and-
// continue contract rather than aborting on the first failure.
func TestFlushBufferedToRedis_ContinuesOnError_spec_25_5(t *testing.T) {
	f := &fakeStream{} // empty stream: nothing already present
	s := New(Options{RedisClient: f, SourceHealth: newMutableHealth(true, true), Now: ts})
	s.MarkRedisOutage()
	s.buffer.Append(evt("ops:1000:1", "dev.lenny.drift_detected"))
	s.buffer.Append(evt("ops:1000:2", "dev.lenny.escalation_created"))

	var reEmitted []string
	s.SetRedisReEmitter(func(_ context.Context, e gwevents.OperationalEvent) error {
		if e.ID == "ops:1000:1" {
			return errors.New("redis write failed")
		}
		reEmitted = append(reEmitted, e.ID)
		return nil
	})

	n, err := s.FlushBufferedToRedis(context.Background())
	if err == nil {
		t.Fatal("expected the last re-emit error to be returned")
	}
	if n != 1 || len(reEmitted) != 1 || reEmitted[0] != "ops:1000:2" {
		t.Fatalf("flush re-emitted %v (n=%d); want the second event re-emitted despite the first failing", reEmitted, n)
	}
}

// spec: 25.5 (best-effort recovery flush) — with no Redis re-emit path wired
// (a no-Redis deployment) the flush is a no-op rather than a panic or error,
// so a replica with nothing to flush to stays healthy.
func TestFlushBufferedToRedis_NoOpWhenUnwired_spec_25_5(t *testing.T) {
	s := New(Options{Now: ts}) // no Redis client, no re-emitter
	s.MarkRedisOutage()
	s.buffer.Append(evt("ops:1000:1", "dev.lenny.drift_detected"))
	if n, err := s.FlushBufferedToRedis(context.Background()); n != 0 || err != nil {
		t.Fatalf("unwired flush = (%d, %v); want (0, nil)", n, err)
	}
}

// spec: 25.5 (best-effort recovery flush scoped to the outage window) — an
// event that reached Redis long before the outage, and has since been trimmed
// off the MAXLEN-bounded shared stream while still resident in this replica's
// far longer-lived local ring, must not be re-emitted on recovery. The stream
// is trimmed by every producer's traffic, so absence from its retained window
// is not evidence that an event never reached it, and re-emitting on that
// signal puts an already-delivered event back at the head of the stream where
// a consumer resuming by stream position receives it a second time. The
// pre-fix flush snapshotted the whole ring and used the retained window as its
// only guard, so it re-emitted the trimmed event; this fails against that code.
func TestFlushBufferedToRedis_SkipsPreOutageEventsTrimmedFromStream_spec_25_5(t *testing.T) {
	// The stream retains only a recent unrelated entry: the pre-outage event
	// was XADDed earlier and has been trimmed away.
	f := &fakeStream{}
	f.add("9-0", evt("gw:900:1", "dev.lenny.alert_fired"))
	s := New(Options{RedisClient: f, SourceHealth: newMutableHealth(true, true), Now: ts})

	// The local ring still holds the trimmed pre-outage event.
	s.buffer.Append(evt("ops:1000:1", "dev.lenny.drift_detected"))

	// Redis goes down, then this replica buffers one event locally.
	s.MarkRedisOutage()
	s.buffer.Append(evt("ops:1001:1", "dev.lenny.escalation_created"))

	var reEmitted []string
	s.SetRedisReEmitter(func(_ context.Context, e gwevents.OperationalEvent) error {
		reEmitted = append(reEmitted, e.ID)
		return nil
	})

	n, err := s.FlushBufferedToRedis(context.Background())
	if err != nil {
		t.Fatalf("flush error: %v", err)
	}
	if n != 1 || len(reEmitted) != 1 || reEmitted[0] != "ops:1001:1" {
		t.Fatalf("flush re-emitted %v (n=%d); want only the outage-window event ops:1001:1, never the trimmed pre-outage one", reEmitted, n)
	}
}

// spec: 25.5 (best-effort recovery flush scoped to the outage window) — a
// recovery edge with no preceding down edge, and a second edge after the
// window was already flushed, both re-emit nothing: a flapping source-health
// signal must not replay the local ring onto the stream once per edge.
func TestFlushBufferedToRedis_NoOutageWindowReEmitsNothing_spec_25_5(t *testing.T) {
	f := &fakeStream{}
	s := New(Options{RedisClient: f, SourceHealth: newMutableHealth(true, true), Now: ts})
	s.buffer.Append(evt("ops:1000:1", "dev.lenny.drift_detected"))

	var reEmitted []string
	s.SetRedisReEmitter(func(_ context.Context, e gwevents.OperationalEvent) error {
		reEmitted = append(reEmitted, e.ID)
		return nil
	})

	// A recovery with no observed outage.
	if n, err := s.FlushBufferedToRedis(context.Background()); n != 0 || err != nil || len(reEmitted) != 0 {
		t.Fatalf("flush with no outage window = (%d, %v) re-emitting %v; want nothing", n, err, reEmitted)
	}

	// One outage, flushed, then a second edge with nothing buffered since.
	s.MarkRedisOutage()
	s.buffer.Append(evt("ops:1002:1", "dev.lenny.escalation_created"))
	if n, err := s.FlushBufferedToRedis(context.Background()); n != 1 || err != nil {
		t.Fatalf("first flush = (%d, %v); want the one outage-window event", n, err)
	}
	if n, err := s.FlushBufferedToRedis(context.Background()); n != 0 || err != nil {
		t.Fatalf("repeat flush = (%d, %v); want nothing (the window was already flushed)", n, err)
	}
	if len(reEmitted) != 1 || reEmitted[0] != "ops:1002:1" {
		t.Fatalf("re-emitted %v across the flapping edges; want exactly [ops:1002:1]", reEmitted)
	}
}

// scanFailingStream is a fakeStream whose retained-key XRANGE scan fails while
// every other read succeeds, standing in for a connection reset or a cluster
// failover landing between the recovery probe's PING and the flush's scan.
type scanFailingStream struct {
	*fakeStream
	failScan bool
}

func (f *scanFailingStream) XRangeN(ctx context.Context, stream, start, stop string, count int64) *redis.XMessageSliceCmd {
	if f.failScan && start == "-" && stop == "+" && count == maxWindow {
		cmd := redis.NewXMessageSliceCmd(ctx)
		cmd.SetErr(errors.New("connection reset by peer"))
		return cmd
	}
	return f.fakeStream.XRangeN(ctx, stream, start, stop, count)
}

// spec: 25.5 (best-effort recovery flush) — the recovery flush must not lose
// the outage window to a failed retained-key scan. The down-to-up edge fires
// once per transition, so a window consumed before a failing scan is never
// re-emitted and every event this replica buffered during the outage is
// abandoned. The pre-fix flush took the window first and returned on the scan
// error, re-emitting nothing; this fails against that code by asserting the
// buffered event still reaches the re-emit path.
func TestFlushBufferedToRedis_ScanFailureDoesNotAbandonTheWindow_spec_25_5(t *testing.T) {
	f := &scanFailingStream{fakeStream: &fakeStream{}, failScan: true}
	s := New(Options{RedisClient: f, SourceHealth: newMutableHealth(true, true), Now: ts})
	s.MarkRedisOutage()
	s.buffer.Append(evt("ops:1001:1", "dev.lenny.escalation_created"))

	var reEmitted []string
	s.SetRedisReEmitter(func(_ context.Context, e gwevents.OperationalEvent) error {
		reEmitted = append(reEmitted, e.ID)
		return nil
	})

	n, err := s.FlushBufferedToRedis(context.Background())
	if n != 1 || len(reEmitted) != 1 || reEmitted[0] != "ops:1001:1" {
		t.Fatalf("flush re-emitted %v (n=%d) when the retained-key scan failed; want the outage-window event ops:1001:1", reEmitted, n)
	}
	if err == nil {
		t.Error("a failed retained-key scan must still be reported to the caller")
	}

	// The window was consumed by the flush that did re-emit it, so a later
	// edge does not replay it.
	f.failScan = false
	if n, err := s.FlushBufferedToRedis(context.Background()); n != 0 || err != nil {
		t.Fatalf("repeat flush = (%d, %v); want nothing (the window was already flushed)", n, err)
	}
}

// spec: 25.5 (best-effort recovery flush scoped to the outage window) — the
// window's two openers race. The source-health probe can observe the outage in
// the instant between an event's local publish and its failed XADD, anchoring
// the window at that event's own ring position; the write failure that follows
// then reports an earlier anchor. The window must widen to the earliest of the
// two, or the flush queries past the event whose XADD failed and abandons it.
// The pre-fix openOutageWindow kept the first anchor recorded and dropped the
// earlier one, so this fails against that code.
func TestOpenOutageWindow_WidensToTheEarliestAnchor_spec_25_5(t *testing.T) {
	f := &fakeStream{}
	s := New(Options{RedisClient: f, SourceHealth: newMutableHealth(false, true), Now: ts})
	var reEmitted []string
	s.SetRedisReEmitter(func(_ context.Context, e gwevents.OperationalEvent) error {
		reEmitted = append(reEmitted, e.ID)
		return nil
	})

	// The event is in the ring; its XADD has not failed yet.
	id := s.buffer.Append(evt("ops:1000:1", "dev.lenny.escalation_created"))

	// The probe observes the outage first and anchors at the ring head, which
	// is this event's own position.
	s.MarkRedisOutage()
	// The XADD then fails, reporting the earlier anchor.
	s.MarkRedisWriteFailure(id)

	n, err := s.FlushBufferedToRedis(context.Background())
	if err != nil {
		t.Fatalf("flush: %v", err)
	}
	if n != 1 || len(reEmitted) != 1 || reEmitted[0] != "ops:1000:1" {
		t.Fatalf("flush re-emitted %d event(s) %v; want the event whose XADD failed", n, reEmitted)
	}
}

// spec: 25.5 (best-effort recovery flush scoped to the outage window) — a later
// signal must still not narrow an open window past events it has yet to flush.
func TestOpenOutageWindow_ALaterAnchorDoesNotNarrowTheWindow_spec_25_5(t *testing.T) {
	f := &fakeStream{}
	s := New(Options{RedisClient: f, SourceHealth: newMutableHealth(false, true), Now: ts})
	var reEmitted []string
	s.SetRedisReEmitter(func(_ context.Context, e gwevents.OperationalEvent) error {
		reEmitted = append(reEmitted, e.ID)
		return nil
	})

	s.MarkRedisOutage() // anchors at the empty ring head
	first := s.buffer.Append(evt("ops:1000:1", "dev.lenny.escalation_created"))
	s.buffer.Append(evt("ops:1001:1", "dev.lenny.drift_detected"))
	// A second failed XADD reports a later anchor; the window keeps the first.
	s.MarkRedisWriteFailure(first + 1)

	if n, err := s.FlushBufferedToRedis(context.Background()); err != nil || n != 2 {
		t.Fatalf("flush = (%d, %v); want both outage-window events", n, err)
	}
	if len(reEmitted) != 2 || reEmitted[0] != "ops:1000:1" {
		t.Fatalf("re-emitted %v; want both events from the earliest anchor", reEmitted)
	}
}
