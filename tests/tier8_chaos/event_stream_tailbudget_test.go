// SPDX-License-Identifier: MIT

//go:build chaos

// Tier-8 chaos test for the §25.5 per-connection Redis tail-failure budget:
// an SSE connection that exhausts its live-tail retries must still return to
// the Redis XREAD source once SourceHealth reports a genuine transition off
// Redis and back, rather than staying pinned to the fall-back for its life.
package tier8_chaos

import (
	"context"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	gwevents "github.com/lennylabs/lenny/pkg/events"
	"github.com/lennylabs/lenny/pkg/gateway/eventbuffer"
	opsstream "github.com/lennylabs/lenny/pkg/ops/events"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
)

// flakyTailStream is a real Redis-backed RedisStreamClient whose live-tail
// checkout can be made to fail, which is the client-level condition the §25.5
// per-connection tail-failure budget bounds. The range reads keep hitting the
// real Redis, so a connection that returns to the Redis source resumes and
// tails the production paths.
type flakyTailStream struct {
	opsstream.RedisStreamClient
	tailBroken atomic.Bool
}

func (f *flakyTailStream) TailClient() (opsstream.RedisTailClient, error) {
	if f.tailBroken.Load() {
		return nil, errTailUnavailable
	}
	return f.RedisStreamClient.TailClient()
}

// errTailUnavailable stands in for the client-level conditions (an exhausted
// pool, an unsupported client type) that leave a live tail unstartable.
var errTailUnavailable = &tailUnavailableError{}

type tailUnavailableError struct{}

func (*tailUnavailableError) Error() string { return "tail client unavailable" }

// spec: 25.5 (Redis-unavailable fallback — the handler switches back to XREAD
// transparently when Redis recovers and emits :degradation {"level":"healthy"})
// — the tail-failure budget that moves a connection off an untailable Redis
// source is per-stint. A SourceHealth transition off Redis and back clears it,
// so the connection re-enters the XREAD tail, announces recovery, and receives
// events XADDed after it returned.
//
// diagnosis: a failure means the tail-failure budget is cleared only by a Redis
// stint that runs, which is unreachable once the budget is exhausted: the
// source guard rewrites the selected source on every later loop iteration, so
// the connection is pinned to the degraded fall-back for its whole life, never
// writes :degradation {"level":"healthy"}, and silently stops receiving
// gateway- and peer-replica-originated events even though Redis is healthy.
func TestOpsEventStreamReturnsToRedisAfterTheTailBudgetIsExhausted(t *testing.T) {
	rd := containers.StartRedis(t, containers.RedisOptions{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	streamKey := "ops:events:stream:tailbudget"
	client := &flakyTailStream{RedisStreamClient: opsstream.NewRedisStreamClient(rd.Client)}
	client.tailBroken.Store(true)

	// Redis reachable, no gateway fan-out wired: a connection reclassified off
	// an untailable Redis source falls back to this replica's own ring.
	health := &toggleHealth{}
	health.redis.Store(true)
	health.gateway.Store(false)

	svc := opsstream.New(opsstream.Options{
		RedisClient:    client,
		RedisStreamKey: streamKey,
		SourceHealth:   health,
		ReplicaID:      "ops-1",
	})

	rec := newSyncBuffer()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "/v1/admin/events/stream", nil)
	done := make(chan struct{})
	go func() {
		defer close(done)
		svc.HandleStream(rec, platformAdminReq(req))
	}()

	// The tail cannot start, so the connection burns its budget and is moved
	// onto the degraded local-ring fall-back.
	waitContains(t, rec, ":degradation", 20*time.Second, "the fall-back degradation envelope after the tail budget was exhausted")

	// A genuine outage moves the connection off Redis, then Redis recovers with
	// a tail that works again.
	health.redis.Store(false)
	time.Sleep(2 * time.Second)
	client.tailBroken.Store(false)
	health.redis.Store(true)

	// The connection re-enters XREAD: it announces recovery and delivers an
	// event XADDed to the shared stream after the switch back.
	waitContains(t, rec, "\"level\":\"healthy\"", 30*time.Second, "the recovery announcement on the switch back to the Redis tail")

	emitter := eventbuffer.NewStreamEmitter(eventbuffer.StreamEmitterOptions{
		Client:    rd.Client,
		Buffer:    eventbuffer.NewEventBuffer(0),
		StreamKey: streamKey,
		ReplicaID: "peer-1",
	})
	if err := emitter.Emit(ctx, gwevents.OperationalEvent{
		ID:          "peer-1:9000:1",
		Type:        "dev.lenny.alert_fired",
		SpecVersion: gwevents.CloudEventsSpecVersion,
		Time:        time.Unix(9000, 0).UTC(),
	}); err != nil {
		t.Fatalf("XADD a post-recovery event: %v", err)
	}
	waitContains(t, rec, "peer-1:9000:1", 20*time.Second, "a post-recovery event delivered from the Redis XREAD tail")

	cancel()
	<-done

	if strings.Count(rec.String(), "\"level\":\"healthy\"") != 1 {
		t.Errorf("recovery announced %d times for one recovery edge:\n%s", strings.Count(rec.String(), "\"level\":\"healthy\""), rec.String())
	}
}
