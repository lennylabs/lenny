// SPDX-License-Identifier: MIT

//go:build load_local

// Tier-7a load_local coverage for the §25.5 per-connection Redis tail-failure
// budget under repeated source transitions. The budget bounds how many times one
// connection re-enters a Redis source whose live tail cannot be started. The
// correctness property is that the bound is per-stint: every SourceHealth
// transition off Redis and back re-arms it, so a long-lived connection returns
// to the XREAD tail on every recovery rather than being pinned to the degraded
// fall-back the first time its tail happens to fail.
//
// spec: §25.5 (Redis-unavailable fallback — the handler switches back to XREAD
// transparently when Redis recovers and emits :degradation {"level":"healthy"};
// XREAD BLOCK 0 per-connection live tail).

package tier7a_load_local_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	opsstream "github.com/lennylabs/lenny/pkg/ops/events"
)

// rearmHealth is a SourceHealth whose Redis reachability the test flips to
// drive repeated transitions on an open connection.
type rearmHealth struct {
	redis   atomic.Bool
	gateway atomic.Bool
}

func (h *rearmHealth) RedisAvailable() bool   { return h.redis.Load() }
func (h *rearmHealth) GatewayAvailable() bool { return h.gateway.Load() }

// flakyTailRearmStream is a §25.5 RedisStreamClient serving an empty stream
// whose per-connection tail checkout fails while tailBroken is set. Failing the
// checkout is the client-level condition the tail-failure budget bounds, and
// toggling it lets one test exhaust the budget and then verify it was re-armed.
type flakyTailRearmStream struct {
	tailBroken atomic.Bool
	tailsGiven atomic.Int64
}

func (f *flakyTailRearmStream) XRangeN(ctx context.Context, _, _, _ string, _ int64) *redis.XMessageSliceCmd {
	return redis.NewXMessageSliceCmd(ctx)
}

func (f *flakyTailRearmStream) XRevRangeN(ctx context.Context, _, _, _ string, _ int64) *redis.XMessageSliceCmd {
	return redis.NewXMessageSliceCmd(ctx)
}

func (f *flakyTailRearmStream) TailClient() (opsstream.RedisTailClient, error) {
	if f.tailBroken.Load() {
		return nil, errors.New("no per-tail client can be checked out")
	}
	f.tailsGiven.Add(1)
	return &parkedTail{closed: make(chan struct{})}, nil
}

// TestOpsEventStreamRearmsTheTailBudgetOnEverySourceTransition holds one SSE
// connection open across several Redis outage-and-recovery cycles, with the
// live tail failing at the start of the run so the connection's tail-failure
// budget is exhausted before the first outage. Each recovery must put the
// connection back on the Redis XREAD tail and announce it.
//
// spec: 25.5 (Redis-unavailable fallback — the handler switches back to XREAD
// transparently when Redis recovers and emits :degradation {"level":"healthy"})
// diagnosis: A failure means the tail-failure budget is cleared only by a Redis
// stint that runs, which is unreachable once the budget is exhausted: the source
// guard rewrites the selected source on every later loop iteration, so
// serveRedis is never entered again. The connection is pinned to the degraded
// fall-back for the rest of its life, stops receiving gateway- and
// peer-replica-originated events even though Redis is healthy, and never writes
// the recovery announcement a consumer uses to learn its view is whole again.
func TestOpsEventStreamRearmsTheTailBudgetOnEverySourceTransition(t *testing.T) {
	stream := &flakyTailRearmStream{}
	stream.tailBroken.Store(true)

	health := &rearmHealth{}
	health.redis.Store(true)
	// No gateway fan-out source is wired, so the fall-back is this replica's own
	// ring under the dual-outage envelope.
	health.gateway.Store(false)

	svc := opsstream.New(opsstream.Options{
		RedisClient:  stream,
		SourceHealth: health,
		ReplicaID:    "ops-1",
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	rec := &blockingRecorder{}
	done := make(chan struct{})
	go func() {
		defer close(done)
		req := httptest.NewRequest(http.MethodGet, "/v1/admin/events/stream", nil)
		svc.HandleStream(rec, platformAdminReq(req.WithContext(ctx)))
	}()

	// The tail cannot start, so the connection burns its budget and falls
	// through to the degraded local ring.
	waitForBody(t, rec, sourceOpsLocalBuffer, "the fall-back announcement after the tail budget was exhausted")
	stream.tailBroken.Store(false)

	// Every cycle is a genuine SourceHealth transition off Redis and back. Each
	// one must re-arm the budget, so the connection re-enters the XREAD tail and
	// announces its recovery.
	const cycles = 3
	for i := 0; i < cycles; i++ {
		health.redis.Store(false)
		// Hold the outage past the source-check interval so the connection
		// observes the matrix move off Redis rather than sleeping through it.
		time.Sleep(2 * time.Second)
		health.redis.Store(true)
		waitForTails(t, stream, int64(i), "the connection to re-enter the Redis XREAD tail after recovery")
	}

	cancel()
	<-done

	if got := strings.Count(rec.body(), `"level":"healthy"`); got < cycles {
		t.Errorf("connection announced recovery %d times over %d outage cycles, want at least %d; the tail-failure budget was not re-armed on the source transition:\n%s",
			got, cycles, cycles, rec.body())
	}
}

// waitForBody blocks until the connection has been written want, and fails the
// test when it does not appear within the deadline.
func waitForBody(t *testing.T, rec *blockingRecorder, want, what string) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(rec.body(), want) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s (%q):\n%s", what, want, rec.body())
}

// waitForTails blocks until the stream has handed out more than n tail clients,
// which is how the test observes that the connection re-entered the Redis
// source rather than inferring it from the wire.
func waitForTails(t *testing.T, stream *flakyTailRearmStream, n int64, what string) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if stream.tailsGiven.Load() > n {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s: the connection checked out %d tail clients, want more than %d", what, stream.tailsGiven.Load(), n)
}
