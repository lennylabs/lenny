// SPDX-License-Identifier: MIT

package events

import (
	"context"
	"errors"
	"testing"

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
	s.buffer.Append(evt("ops:1000:1", "dev.lenny.drift_detected"))     // X (already present)
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
	s.buffer.Append(evt("ops:1000:1", "dev.lenny.drift_detected"))
	if n, err := s.FlushBufferedToRedis(context.Background()); n != 0 || err != nil {
		t.Fatalf("unwired flush = (%d, %v); want (0, nil)", n, err)
	}
}
