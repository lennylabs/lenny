//go:build component

// SPDX-License-Identifier: MIT

// Tier-2 component coverage for the command the §25.5 per-connection live SSE
// tail issues against a real Redis ops:events:stream, and for the dedicated
// connection it issues it on.
//
// The spec states the tail as XREAD BLOCK 0. Because go-redis does not
// interrupt a deadline-free blocked read on context cancellation, the tail
// owns a client of its own and closes it when the connection ends; that close
// is what ends the read. Both halves are observable here: the recorder sees
// the argument on the wire, and the closed flag shows the connection was
// released.
package eventstream_test

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	opsstream "github.com/lennylabs/lenny/pkg/ops/events"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
)

// tailRecordingClient wraps the real read-side client and records the XREAD
// arguments each live tail issues, plus whether the tail closed the client it
// was handed.
type tailRecordingClient struct {
	opsstream.RedisStreamClient

	mu     sync.Mutex
	blocks []time.Duration
	closed atomic.Int64
	opened atomic.Int64
}

func (c *tailRecordingClient) TailClient() (opsstream.RedisTailClient, error) {
	inner, err := c.RedisStreamClient.TailClient()
	if err != nil {
		return nil, err
	}
	c.opened.Add(1)
	return &recordingTail{parent: c, inner: inner}, nil
}

func (c *tailRecordingClient) recordBlock(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.blocks = append(c.blocks, d)
}

func (c *tailRecordingClient) recordedBlocks() []time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]time.Duration(nil), c.blocks...)
}

type recordingTail struct {
	parent *tailRecordingClient
	inner  opsstream.RedisTailClient
}

func (r *recordingTail) XRead(ctx context.Context, a *redis.XReadArgs) *redis.XStreamSliceCmd {
	r.parent.recordBlock(a.Block)
	return r.inner.XRead(ctx, a)
}

func (r *recordingTail) Close() error {
	r.parent.closed.Add(1)
	return r.inner.Close()
}

// TestOpsEventStreamLiveTailIssuesBlockZeroOnItsOwnConnection asserts the live
// tail issues XREAD BLOCK 0 against the real stream and closes the connection
// it owns when the SSE connection is cancelled.
//
// spec: 25.5 ("The SSE handler holds an open HTTP response and reads from the
// Redis stream via XREAD BLOCK 0 in a goroutine") — the blocking read is the
// governing command for live delivery. A tail that substitutes a bounded block
// redefines that contract silently, and a tail that does not close its own
// connection cannot end a deadline-free read at all, so a disconnected
// connection leaks its goroutine.
//
// diagnosis: a non-zero block means the live tail has been turned into a
// client-side poll loop against Redis, changing the §25.5 delivery command; a
// tail client left open means a disconnected SSE connection holds its Redis
// connection and its goroutine forever.
func TestOpsEventStreamLiveTailIssuesBlockZeroOnItsOwnConnection(t *testing.T) {
	rd := containers.StartRedis(t, containers.RedisOptions{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	const key = "ops:events:stream:tailcontract"
	emitter := newStreamEmitter(t, rd.Client, key, 1000)
	if err := emitter.Emit(ctx, alertEvent("pool/backlog")); err != nil {
		t.Fatalf("seed the stream: %v", err)
	}

	recorder := &tailRecordingClient{RedisStreamClient: opsstream.NewRedisStreamClient(rd.Client)}
	svc := opsstream.New(opsstream.Options{
		RedisClient:    recorder,
		RedisStreamKey: key,
		SourceHealth:   opsstream.StaticSourceHealth{Redis: true, Gateway: true},
		ReplicaID:      "ops-1",
	})

	sink := newTailSink()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "/v1/admin/events/stream", nil)
	if err != nil {
		t.Fatalf("build stream request: %v", err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		svc.HandleStream(sink, req)
	}()

	// Wait for the backlog frame so the connection is known to have reached
	// its live tail.
	waitUntil(t, 10*time.Second, "the backlog frame", func() bool {
		return strings.Contains(sink.String(), "pool/backlog")
	})
	waitUntil(t, 10*time.Second, "the live tail's first XREAD", func() bool {
		return len(recorder.recordedBlocks()) > 0
	})

	for i, block := range recorder.recordedBlocks() {
		if block != 0 {
			t.Fatalf("live tail XREAD %d used BLOCK %s; §25.5 states the per-connection live tail as XREAD BLOCK 0", i, block)
		}
	}
	if n := recorder.opened.Load(); n != 1 {
		t.Errorf("live tail checked out %d dedicated clients, want exactly 1 per connection", n)
	}

	// The tail must deliver an event XADDed while it is blocked.
	if err := emitter.Emit(ctx, alertEvent("pool/live")); err != nil {
		t.Fatalf("emit into the blocked tail: %v", err)
	}
	waitUntil(t, 10*time.Second, "the event XADDed into the blocked tail", func() bool {
		return strings.Contains(sink.String(), "pool/live")
	})

	// Cancelling the connection must close the tail's own client, which is the
	// only thing that can end a deadline-free blocked read.
	cancel()
	select {
	case <-done:
	case <-time.After(15 * time.Second):
		t.Fatal("the SSE handler did not return within 15s of cancellation")
	}
	waitUntil(t, 10*time.Second, "the tail client to be closed", func() bool {
		return recorder.closed.Load() >= 1
	})
}

// waitUntil polls cond until it holds or the budget elapses.
func waitUntil(t *testing.T, budget time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(budget)
	for {
		if cond() {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out after %s waiting for %s", budget, what)
		}
		time.Sleep(25 * time.Millisecond)
	}
}

// tailSink is a streaming ResponseWriter+Flusher readable while the SSE
// handler writes to it from another goroutine.
type tailSink struct {
	mu  sync.Mutex
	buf strings.Builder
	hdr http.Header
}

func newTailSink() *tailSink { return &tailSink{hdr: http.Header{}} }

func (s *tailSink) Header() http.Header { return s.hdr }
func (s *tailSink) WriteHeader(int)     {}
func (s *tailSink) Flush()              {}

func (s *tailSink) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.Write(p)
}

func (s *tailSink) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.String()
}
