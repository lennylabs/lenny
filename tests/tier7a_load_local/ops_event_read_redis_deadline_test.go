// SPDX-License-Identifier: MIT

//go:build load_local

// Tier-7a load_local reliability coverage for the §25.5 read surface when
// the Redis ops:events:stream becomes unreachable inside the window between
// the outage starting and the source-health signal observing it.
//
// The source-health signal that drives the §25.5 degradation matrix is
// refreshed by a background loop on an interval, so it is deliberately stale
// for up to one interval. Every poll and SSE connection arriving in that
// window still selects the Redis source and issues a read against a Redis
// that is already gone. The Redis client retries its connection internally
// for tens of seconds, so a read with no deadline of its own outlives any
// caller: the request never answers, the caller times out, and the read
// surface neither serves a page nor reports its degradation. The per-request
// Redis reads therefore carry their own deadline, and the request answers
// (empty, cursor echoed) while the next source-health refresh moves the
// surface onto the gateway-buffer fall-back.
//
// spec: §25.5 (the read surface degrades rather than blocking; the
// per-request poll and SSE path does not block on source health).

package tier7a_load_local_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	opsstream "github.com/lennylabs/lenny/pkg/ops/events"
)

// staleHealthRedisUp is the source-health signal as it reads inside the
// stale window: Redis is reported reachable (the last refresh saw it up)
// although the stream is in fact unreachable.
type staleHealthRedisUp struct{}

func (staleHealthRedisUp) RedisAvailable() bool   { return true }
func (staleHealthRedisUp) GatewayAvailable() bool { return true }

// unreachableRedis stands in for a Redis client whose connection retries are
// in progress: every read parks until its context is done or until
// retryCeiling elapses, whichever comes first. retryCeiling models the
// client's own retry budget, which is far longer than any caller is willing
// to wait, so a read that answers before it proves the read carried a
// deadline of its own.
type unreachableRedis struct {
	retryCeiling time.Duration

	mu    sync.Mutex
	calls int
}

// park blocks the way an in-progress connection retry does and reports how
// long it blocked for.
func (u *unreachableRedis) park(ctx context.Context) {
	u.mu.Lock()
	u.calls++
	u.mu.Unlock()
	t := time.NewTimer(u.retryCeiling)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
}

func (u *unreachableRedis) callCount() int {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.calls
}

func (u *unreachableRedis) XRangeN(ctx context.Context, _, _, _ string, _ int64) *redis.XMessageSliceCmd {
	u.park(ctx)
	cmd := redis.NewXMessageSliceCmd(ctx)
	cmd.SetErr(context.DeadlineExceeded)
	return cmd
}

func (u *unreachableRedis) XRevRangeN(ctx context.Context, _, _, _ string, _ int64) *redis.XMessageSliceCmd {
	u.park(ctx)
	cmd := redis.NewXMessageSliceCmd(ctx)
	cmd.SetErr(context.DeadlineExceeded)
	return cmd
}

func (u *unreachableRedis) XRead(ctx context.Context, _ *redis.XReadArgs) *redis.XStreamSliceCmd {
	u.park(ctx)
	cmd := redis.NewXStreamSliceCmd(ctx)
	cmd.SetErr(context.DeadlineExceeded)
	return cmd
}

// pollDeadlineBudget is the ceiling this test holds the read surface to: the
// per-request Redis deadline plus generous slack for a loaded machine. It is
// far below the modelled client retry ceiling, so a request that answers
// within it answered because it bounded its own read.
const pollDeadlineBudget = 8 * time.Second

// spec: 25.5 (operational event stream read side — degradation matrix, the
// per-request poll and SSE path does not block on source health)
//
// diagnosis: a failure means a poll arriving while the source-health signal
// still reports Redis reachable, against a Redis that is already
// unreachable, blocks on the Redis client's connection retries instead of
// answering. Every caller in that window times out rather than receiving a
// page, and the surface never reports the degradation that would tell the
// caller to expect the gateway-buffer fall-back. The per-request Redis reads
// have lost their deadline.
func TestRedisReadDeadlineKeepsPollAnsweringWhenRedisIsUnreachable(t *testing.T) {
	t.Parallel()

	unreachable := &unreachableRedis{retryCeiling: 60 * time.Second}
	svc := opsstream.New(opsstream.Options{
		ReplicaID:    "ops-a",
		SourceHealth: staleHealthRedisUp{},
		RedisClient:  unreachable,
	})

	// Many callers poll at once: a burst of admins plus every open SSE
	// stream re-polling. Each must answer on its own deadline rather than
	// queueing behind the others.
	const callers = 8
	done := make(chan time.Duration, callers)
	for i := 0; i < callers; i++ {
		go func() {
			start := time.Now()
			req := httptest.NewRequest(http.MethodGet, "/v1/admin/events", nil)
			rec := httptest.NewRecorder()
			svc.HandlePoll(rec, req)
			done <- time.Since(start)
			if rec.Code != http.StatusOK {
				t.Errorf("poll during an unobserved Redis outage returned HTTP %d, want 200: the "+
					"read surface must serve an empty page with the caller's cursor echoed rather "+
					"than failing", rec.Code)
			}
		}()
	}

	deadline := time.After(pollDeadlineBudget)
	for i := 0; i < callers; i++ {
		select {
		case took := <-done:
			if took > pollDeadlineBudget {
				t.Fatalf("poll answered after %s, want under %s: the per-request Redis read did not "+
					"bound itself and rode the client's connection retries", took, pollDeadlineBudget)
			}
		case <-deadline:
			t.Fatalf("only %d of %d polls answered within %s; a poll issued against an unreachable "+
				"Redis inside the stale source-health window blocked on the client's connection "+
				"retries instead of degrading promptly (%d Redis reads issued)",
				i, callers, pollDeadlineBudget, unreachable.callCount())
		}
	}

	if unreachable.callCount() == 0 {
		t.Fatal("no Redis read was issued: the test did not exercise the Redis read path, so it " +
			"proves nothing about its deadline")
	}
}
