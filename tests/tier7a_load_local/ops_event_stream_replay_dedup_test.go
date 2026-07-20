// SPDX-License-Identifier: MIT

//go:build load_local

// Tier-7a coverage for the §25.5 exactly-once invariant on the read path that
// stresses it hardest: a connection that has already been written more events
// than a local ring holds, and then has its resume point fail on a switch back
// to the Redis stream, which replays the whole retained window. The
// per-connection delivered-key set is the only defence on that path, since the
// carried resume position is forward-only and the frame writer does not consult
// it, so every event the connection already received must still be in that set
// when the replay arrives.
//
// spec: §25.5 (eventKey dedup across sources, exactly-once across the source
// switch).

package tier7a_load_local_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	gwevents "github.com/lennylabs/lenny/pkg/events"
	"github.com/lennylabs/lenny/pkg/gateway/eventbuffer"
	opsstream "github.com/lennylabs/lenny/pkg/ops/events"
)

// replayStream is an in-process ops:events:stream holding a fixed retained
// window. failNextScan makes the next range read fail once, which is how the
// test drives the read side down its resume-failure path (a connection reset
// landing on the cursor scan) deterministically.
type replayStream struct {
	mu       sync.Mutex
	msgs     []redis.XMessage
	failNext atomic.Bool
}

// add appends one decodable entry carrying eventKey, using a zero-padded
// stream ID so the ordering comparisons below are plain string comparisons.
func (r *replayStream) add(t *testing.T, eventKey string) {
	t.Helper()
	ev := gwevents.OperationalEvent{
		ID:          eventKey,
		Type:        gwevents.EventType("alert_fired").CloudEventsType(),
		Subject:     "pool/replay",
		SpecVersion: gwevents.CloudEventsSpecVersion,
	}
	body, err := json.Marshal(ev)
	if err != nil {
		t.Fatalf("marshal replay event %s: %v", eventKey, err)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.msgs = append(r.msgs, redis.XMessage{
		ID:     fmt.Sprintf("%09d-0", len(r.msgs)+1),
		Values: map[string]any{"event": string(body)},
	})
}

func (r *replayStream) snapshot() []redis.XMessage {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]redis.XMessage{}, r.msgs...)
}

// XRangeN serves the forward range read, honouring the exclusive "(" start the
// read side uses for a resume and the count bound it passes.
func (r *replayStream) XRangeN(ctx context.Context, stream, start, stop string, count int64) *redis.XMessageSliceCmd {
	cmd := redis.NewXMessageSliceCmd(ctx)
	if r.failNext.CompareAndSwap(true, false) {
		cmd.SetErr(errors.New("redis: connection reset by peer"))
		return cmd
	}
	after, exclusive := strings.CutPrefix(start, "(")
	var out []redis.XMessage
	for _, m := range r.snapshot() {
		if exclusive && m.ID <= after {
			continue
		}
		out = append(out, m)
		if count > 0 && int64(len(out)) >= count {
			break
		}
	}
	cmd.SetVal(out)
	return cmd
}

// XRevRangeN serves the newest-first bound read the head cursor needs.
func (r *replayStream) XRevRangeN(ctx context.Context, stream, start, stop string, count int64) *redis.XMessageSliceCmd {
	cmd := redis.NewXMessageSliceCmd(ctx)
	msgs := r.snapshot()
	if len(msgs) == 0 || count <= 0 {
		cmd.SetVal(nil)
		return cmd
	}
	cmd.SetVal([]redis.XMessage{msgs[len(msgs)-1]})
	return cmd
}

// TailClient hands out a tail that parks until the connection ends, so the live
// tail contributes no frames and the test observes the backlog replay alone.
func (r *replayStream) TailClient() (opsstream.RedisTailClient, error) {
	return &parkedTail{closed: make(chan struct{})}, nil
}

// parkedTail blocks its XREAD until the connection's context is cancelled or
// the tail is closed, the way a real deadline-free XREAD BLOCK 0 does on an
// idle stream.
type parkedTail struct {
	closed chan struct{}
	once   sync.Once
}

func (t *parkedTail) XRead(ctx context.Context, a *redis.XReadArgs) *redis.XStreamSliceCmd {
	cmd := redis.NewXStreamSliceCmd(ctx)
	select {
	case <-ctx.Done():
	case <-t.closed:
	}
	cmd.SetErr(errors.New("redis: client is closed"))
	return cmd
}

func (t *parkedTail) Close() error {
	t.once.Do(func() { close(t.closed) })
	return nil
}

// waitForKeys blocks until the connection has been written at least n frames,
// and fails the test when it does not reach that within the deadline.
func waitForKeys(t *testing.T, r *sseReader, n int, what string) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if len(r.delivered()) >= n {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("%s: connection was written %d frames, want at least %d", what, len(r.delivered()), n)
}

// spec: §25.5 (eventKey dedup across sources, exactly-once across the source
// switch) — "each event is delivered exactly once" holds across a source
// transition, including one where the new source cannot resolve the carried
// resume position and replays its whole retained window instead.
//
// diagnosis: the per-connection delivered-key set is sized below the window a
// session can replay, so a connection that has been written more events than
// the set remembers and then hits a resume-point failure receives every event
// past the bound a second time. The exactly-once guarantee across the §25.5
// source switch does not hold for any connection older than the set's bound.
func TestOpsEventStreamFullWindowReplayDeliversEachEventOnce_spec_25_5(t *testing.T) {
	// More entries than a local ring replay window holds, so a set sized to
	// the ring rather than to the stream cannot cover the replay.
	const retained = 4*eventbuffer.DefaultBufferCapacity + 1000

	stream := &replayStream{}
	for i := 0; i < retained; i++ {
		stream.add(t, fmt.Sprintf("gw-1:%d:1", 1_000_000+i))
	}

	health := &switchHealth{}
	health.redis.Store(true)
	health.gateway.Store(false)

	svc := opsstream.New(opsstream.Options{
		ReplicaID:         "ops-1",
		RedisClient:       stream,
		RedisStreamKey:    "ops:events:stream:replay",
		RedisStreamMaxLen: eventbuffer.DefaultStreamMaxLen,
		SourceHealth:      health,
	})

	reader := openSSEReader(svc)
	defer reader.close()

	// The whole retained window reaches the connection from the Redis source.
	waitForKeys(t, reader, retained, "initial Redis backlog")

	// Move the connection off Redis so it stops reading the stream, then arm
	// the next range read to fail and move it back. The resume scan is the
	// first range read the returning connection issues, so it takes the
	// failure and the session replays the retained window from its head.
	health.redis.Store(false)
	time.Sleep(2 * time.Second)
	stream.failNext.Store(true)
	health.redis.Store(true)

	// Give the replay time to arrive before judging the delivered sequence.
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) && !reader.sawComment(":gap") {
		time.Sleep(20 * time.Millisecond)
	}
	if !reader.sawComment(":gap") {
		t.Fatal("the returning connection never reported the unresolvable resume position, so the full-window replay under test never ran")
	}
	time.Sleep(500 * time.Millisecond)

	delivered := reader.delivered()
	seen := make(map[string]int, len(delivered))
	for _, key := range delivered {
		seen[key]++
	}
	dupes := 0
	example := ""
	for key, n := range seen {
		if n > 1 {
			dupes++
			if example == "" {
				example = fmt.Sprintf("%s written %d times", key, n)
			}
		}
	}
	if dupes != 0 {
		t.Fatalf("%d of %d eventKeys were written more than once across the full-window replay (%s); the connection's delivered-key set is smaller than the window it replayed", dupes, len(seen), example)
	}
	if len(seen) != retained {
		t.Fatalf("connection observed %d distinct eventKeys, want the %d retained", len(seen), retained)
	}
}
