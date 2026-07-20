//go:build component

// SPDX-License-Identifier: MIT

// Tier-2 component tests for the §25.5 read side against a real Redis
// ops:events:stream retained above the default stream length. The read side
// resolves a cursor and reads back the retained eventKeys by scanning the
// stream, and both scans are bounded. When that bound is smaller than the
// window the writer actually retains, the scan sees only the oldest entries:
// a cursor naming a recent event resolves as evicted, and a key already on
// the stream reads as absent. These cases pin both scans to the configured
// stream length.
package eventstream_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/redis/go-redis/v9"

	gwevents "github.com/lennylabs/lenny/pkg/events"
	"github.com/lennylabs/lenny/pkg/gateway/eventbuffer"
	opsstream "github.com/lennylabs/lenny/pkg/ops/events"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
)

// overflowMaxLen is the operator-configured MAXLEN these cases run the stream
// at: above the default stream length, so a scan bounded by the default covers
// only the oldest part of the retained window.
const overflowMaxLen = int64(eventbuffer.DefaultStreamMaxLen) * 2

// overflowFill is the number of entries pushed onto the stream: more than the
// default stream length and fewer than the configured MAXLEN, so nothing is
// evicted and every entry beyond the default bound is still retained.
const overflowFill = eventbuffer.DefaultStreamMaxLen + 200

// fillStream XADDs n decodable operational events onto key in pipelined
// batches, matching the StreamEmitter's single "event" field encoding, and
// returns the canonical eventKey of the newest entry. The keys ascend so the
// stream order and the eventKey order agree.
func fillStream(t *testing.T, client redis.UniversalClient, key string, n int) string {
	t.Helper()
	ctx := context.Background()
	last := ""
	pipe := client.Pipeline()
	for i := 0; i < n; i++ {
		ev := alertEvent("pool/fill")
		ev.ID = fmt.Sprintf("gw-1:%d:1", 1_000_000+i)
		ev.SpecVersion = gwevents.CloudEventsSpecVersion
		body, err := json.Marshal(ev)
		if err != nil {
			t.Fatalf("marshal fill event %d: %v", i, err)
		}
		pipe.XAdd(ctx, &redis.XAddArgs{Stream: key, Values: map[string]any{"event": string(body)}})
		last = ev.ID
		if pipe.Len() >= 500 {
			if _, err := pipe.Exec(ctx); err != nil {
				t.Fatalf("fill stream at %d: %v", i, err)
			}
		}
	}
	if pipe.Len() > 0 {
		if _, err := pipe.Exec(ctx); err != nil {
			t.Fatalf("fill stream tail: %v", err)
		}
	}
	return last
}

// spec: 25.5 (Cursor Model — cross-source cursor translation; gapDetected on
// an evicted cursor) — a cursor is reported as a gap only when the event it
// names is no longer retained. The stream's retained window is the
// operator-configured MAXLEN, so the read side's cursor scan covers that
// window rather than a fixed length of its own.
//
// diagnosis: the §25.5 read side resolves cursors against a scan window
// narrower than the stream's configured MAXLEN. A cursor naming an event the
// stream still retains resolves as evicted, so the poll reports gapDetected
// with an oldestAvailableCursor and pages forward from a stale position,
// replaying the tail of the stream to every caller on an install whose
// events-stream-max-len is raised above the default.
func TestOpsEventStreamPollResolvesCursorBeyondDefaultStreamLength(t *testing.T) {
	rd := containers.StartRedis(t, containers.RedisOptions{})

	const key = "ops:events:stream:maxlenoverflow"
	newest := fillStream(t, rd.Client, key, overflowFill)

	gaps := 0
	svc := opsstream.New(opsstream.Options{
		RedisClient:       opsstream.NewRedisStreamClient(rd.Client),
		RedisStreamKey:    key,
		RedisStreamMaxLen: overflowMaxLen,
		SourceHealth:      opsstream.StaticSourceHealth{Redis: true, Gateway: true},
		OnGap:             func() { gaps++ },
	})

	// The head cursor names the newest retained entry, which sits past the
	// default stream length. Resuming from it must resolve to the live head:
	// nothing follows it, and nothing before it is replayed.
	first := pollRedis(t, svc, "")
	if first.Pagination.HeadCursor == "" {
		t.Fatal("poll returned no headCursor to resume from")
	}

	resumed := pollRedis(t, svc, first.Pagination.HeadCursor)
	if resumed.Pagination.GapDetected {
		t.Errorf("resuming from the newest retained event (%s) reported a gap; the stream retains it, so the cursor scan is bounded below the configured MAXLEN (reason %q)", newest, resumed.Pagination.GapReason)
	}
	if resumed.Pagination.OldestAvailableCursor != "" {
		t.Errorf("resume carried oldestAvailableCursor %q; nothing was evicted", resumed.Pagination.OldestAvailableCursor)
	}
	if len(resumed.Items) != 0 {
		t.Errorf("resuming from the newest retained event served %d item(s), want none; the read paged forward from a stale position and replayed the stream", len(resumed.Items))
	}
	if gaps != 0 {
		t.Errorf("gap counter = %d, want 0; no cursor referenced an evicted event", gaps)
	}
}

// spec: 25.5 (best-effort recovery flush, eventKey dedup) — the flush skips an
// event whose eventKey is already on the stream, so a repeated Redis
// down-to-up edge does not append a second copy. The retained-key scan that
// backs the skip covers the stream's configured MAXLEN, so a key sitting past
// the default stream length is still seen as present.
//
// diagnosis: the §25.5 recovery flush reads back the retained eventKeys over a
// window narrower than the stream's configured MAXLEN, so a recently re-emitted
// event reads as absent and the flush appends it to the stream again. A
// consumer resuming by stream position then receives the duplicate, defeating
// the eventKey dedup the flush depends on.
func TestOpsEventStreamRecoveryFlushSeesRetainedKeyBeyondDefaultStreamLength(t *testing.T) {
	rd := containers.StartRedis(t, containers.RedisOptions{})
	ctx := context.Background()

	const key = "ops:events:stream:maxlenflush"
	fillStream(t, rd.Client, key, overflowFill)

	svc := opsstream.New(opsstream.Options{
		ReplicaID:         "ops-1",
		RedisClient:       opsstream.NewRedisStreamClient(rd.Client),
		RedisStreamKey:    key,
		RedisStreamMaxLen: overflowMaxLen,
		SourceHealth:      opsstream.StaticSourceHealth{Redis: true, Gateway: true},
	})
	// The re-emitter writes the event onto the stream the way the fan-out
	// emitter does, so a flushed event lands at the tail of the retained
	// window: past the default stream length on a stream this long.
	svc.SetRedisReEmitter(func(ctx context.Context, ev gwevents.OperationalEvent) error {
		body, err := json.Marshal(ev)
		if err != nil {
			return err
		}
		return rd.Client.XAdd(ctx, &redis.XAddArgs{Stream: key, Values: map[string]any{"event": string(body)}}).Err()
	})

	// One lenny-ops-originated event buffered during the outage window.
	buffered, err := svc.PublishBuffered(ctx, alertEvent("ops/self"))
	if err != nil {
		t.Fatalf("publish outage-window event: %v", err)
	}
	svc.MarkRedisWriteFailure(buffered.ID)

	flushed, err := svc.FlushBufferedToRedis(ctx)
	if err != nil {
		t.Fatalf("first recovery flush: %v", err)
	}
	if flushed != 1 {
		t.Fatalf("first recovery flush re-emitted %d event(s), want 1", flushed)
	}

	// A second edge over the same window must re-emit nothing: the event is
	// now on the stream, at a position past the default stream length.
	svc.MarkRedisWriteFailure(buffered.ID)
	flushed, err = svc.FlushBufferedToRedis(ctx)
	if err != nil {
		t.Fatalf("second recovery flush: %v", err)
	}
	if flushed != 0 {
		t.Fatalf("second recovery flush re-emitted %d event(s) (key %s), want 0; the retained-key scan missed a key past the default stream length and duplicated it onto the stream", flushed, buffered.Event.ID)
	}
}
