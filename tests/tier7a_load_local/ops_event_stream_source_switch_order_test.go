// SPDX-License-Identifier: MIT

//go:build load_local

// Tier-7a load_local concurrency and ordering coverage for the §25.5 read
// surface across a live source switch. Each SSE connection runs its own
// goroutine-per-connection Redis tail and, during a Redis outage, its own
// 2-second gateway-buffer fan-out poll, while the source-health signal moves
// the active source underneath every open connection at once. The correctness
// property of that transition is per-connection and concurrent: whatever the
// source, each connection must observe every published event exactly once and
// in emission order, with no drop at the boundary and no replay of the
// overlapping window. This test drives many connections through a
// Redis-to-gateway-buffer-to-Redis switch against a real Redis stream and a
// real fan-out client, and asserts that invariant per connection under the race
// detector.
//
// spec: §25.5 (independent per-connection read cursor, transparent Redis to
// gateway-buffer switch and back, cross-switch no-drop ordering).

package tier7a_load_local_test

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	gwevents "github.com/lennylabs/lenny/pkg/events"
	"github.com/lennylabs/lenny/pkg/gateway/eventbuffer"
	opsstream "github.com/lennylabs/lenny/pkg/ops/events"
	"github.com/lennylabs/lenny/pkg/ops/gateway"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
)

// switchHealth is a SourceHealth whose Redis reachability flips at runtime, so
// the test can move every open connection across a source boundary at once.
type switchHealth struct {
	redis   atomic.Bool
	gateway atomic.Bool
}

func (h *switchHealth) RedisAvailable() bool   { return h.redis.Load() }
func (h *switchHealth) GatewayAvailable() bool { return h.gateway.Load() }

// replicaWindow is one gateway replica's §25.3 buffer window, appended to as
// the test emits events so the fan-out and the Redis stream overlap the way
// they do in production (the gateway StreamEmitter writes both).
type replicaWindow struct {
	mu     sync.Mutex
	events []gwevents.BufferedEvent
}

func (r *replicaWindow) add(ev gwevents.OperationalEvent) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, gwevents.BufferedEvent{ID: uint64(len(r.events) + 1), Event: ev})
}

func (r *replicaWindow) page() gwevents.BufferedEventPage {
	r.mu.Lock()
	defer r.mu.Unlock()
	return gwevents.BufferedEventPage{Events: append([]gwevents.BufferedEvent{}, r.events...)}
}

// sseReader drives one Service.HandleStream connection through an in-memory
// pipe and records the eventKey of every frame in arrival order.
type sseReader struct {
	cancel context.CancelFunc
	done   chan struct{}

	mu   sync.Mutex
	keys []string
}

func openSSEReader(svc *opsstream.Service) *sseReader {
	ctx, cancel := context.WithCancel(context.Background())
	pr, pw := io.Pipe()
	rw := &pipeSink{hdr: http.Header{}, w: pw}
	req := httptest.NewRequest(http.MethodGet, "/v1/admin/events/stream", nil).WithContext(ctx)
	r := &sseReader{cancel: cancel, done: make(chan struct{})}
	go func() {
		svc.HandleStream(rw, req)
		_ = pw.Close()
	}()
	go func() {
		defer close(r.done)
		sc := bufio.NewScanner(pr)
		sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
		for sc.Scan() {
			key, ok := strings.CutPrefix(sc.Text(), "id: ")
			if !ok {
				continue
			}
			r.mu.Lock()
			r.keys = append(r.keys, key)
			r.mu.Unlock()
		}
	}()
	return r
}

func (r *sseReader) delivered() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string{}, r.keys...)
}

func (r *sseReader) close() {
	r.cancel()
	<-r.done
}

// pipeSink is a streaming ResponseWriter+Flusher forwarding every write to an
// io.Pipe, so a test reads SSE frames as the handler emits them.
type pipeSink struct {
	hdr http.Header
	w   *io.PipeWriter
}

func (p *pipeSink) Header() http.Header         { return p.hdr }
func (p *pipeSink) WriteHeader(int)             {}
func (p *pipeSink) Write(b []byte) (int, error) { return p.w.Write(b) }
func (p *pipeSink) Flush()                      {}

// TestOpsEventStreamSourceSwitchDeliversEachEventOnceInOrderUnderLoad opens
// many concurrent SSE connections against a real Redis ops:events:stream and a
// real gateway fan-out, moves every connection from the Redis tail into the
// gateway-buffer fall-back and back, and asserts each connection observed every
// published event exactly once and in emission order across both transitions.
//
// The two source windows overlap by construction, as they do in production: the
// gateway StreamEmitter writes each gateway-originated event to both the shared
// Redis stream and the per-replica buffer. A connection that seeds its resume
// position by an exact eventKey match, rather than by the continuation point,
// replays that overlap on every switch; one that seeds nothing drops the events
// published across the boundary. Both failures show up here as a per-connection
// sequence that is not the emission order exactly once.
//
// spec: 25.5 (independent per-connection read cursor, cross-switch no-drop
// ordering, transparent Redis to gateway-buffer switch and back).
// diagnosis: a failure means the §25.5 source switch is not exactly-once or not
// order-preserving under concurrent load: an operational event was delivered
// twice (the connection replayed the overlapping window at the boundary),
// delivered out of emission order, or dropped entirely (the resume position was
// carried across the switch incorrectly, so events published near the boundary
// reached no subscriber). Under -race, a failure may also report a data race in
// the per-connection session state shared with the fan-out poll.
func TestOpsEventStreamSourceSwitchDeliversEachEventOnceInOrderUnderLoad(t *testing.T) {
	rd := containers.StartRedis(t, containers.RedisOptions{})
	ctx := context.Background()

	const streamKey = "ops:events:stream:switchorder"
	const connections = 8

	window := &replicaWindow{}
	gwSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(window.page())
	}))
	defer gwSrv.Close()
	gwClient, err := gateway.NewClient(gateway.Config{
		BaseURL:           "http://gateway.invalid",
		Token:             gateway.StaticToken("t"),
		Discovery:         gateway.StaticDiscovery{gwSrv.URL},
		PerRequestTimeout: 3 * time.Second,
		FanOutTimeout:     2 * time.Second,
	})
	if err != nil {
		t.Fatalf("gateway client: %v", err)
	}

	health := &switchHealth{}
	health.redis.Store(true)
	health.gateway.Store(true)
	svc := opsstream.New(opsstream.Options{
		RedisClient:    opsstream.NewRedisStreamClient(rd.Client),
		RedisStreamKey: streamKey,
		SourceHealth:   health,
		ReplicaID:      "ops-1",
	})
	svc.SetGatewayBufferSource(gwClient)

	// The producer writes every event to the shared stream; the gateway window
	// mirrors it, so the two source windows overlap at each switch.
	emitter := eventbuffer.NewStreamEmitter(eventbuffer.StreamEmitterOptions{
		Client:    rd.Client,
		Buffer:    eventbuffer.NewEventBuffer(0),
		StreamKey: streamKey,
		MaxLen:    1000,
		ReplicaID: "gw-1",
	})
	var order []string
	newEvent := func(subject string) gwevents.OperationalEvent {
		return gwevents.OperationalEvent{
			Type:            gwevents.EventType("alert_fired").CloudEventsType(),
			Subject:         subject,
			Severity:        "warning",
			DataContentType: gwevents.ContentTypeJSON,
			Data:            json.RawMessage(`{"alert":"x"}`),
		}
	}
	// emitToStream XADDs an event and returns the eventKey the emitter stamped.
	emitToStream := func(subject string) gwevents.OperationalEvent {
		t.Helper()
		if err := emitter.Emit(ctx, newEvent(subject)); err != nil {
			t.Fatalf("emit %s: %v", subject, err)
		}
		page := emitter.Buffer().Query(0, gwevents.EventFilter{}, 0)
		last := page.Events[len(page.Events)-1].Event
		order = append(order, last.ID)
		return last
	}
	// emitToBoth models the gateway StreamEmitter's dual write: the event lands
	// on the shared stream and in the replica's buffer window, so the two source
	// windows overlap at every switch.
	emitToBoth := func(subject string) {
		t.Helper()
		window.add(emitToStream(subject))
	}
	// addGatewayOnly models a gateway-originated event emitted while Redis is
	// unreachable: the XADD failed, so it exists only in the replica's ring.
	addGatewayOnly := func(subject string, nonce int) {
		t.Helper()
		ev := newEvent(subject)
		ev.ID = fmt.Sprintf("gw-1:%d:%d", time.Now().UnixNano(), nonce)
		ev.SpecVersion = gwevents.CloudEventsSpecVersion
		ev.Time = time.Now().UTC()
		window.add(ev)
		order = append(order, ev.ID)
	}

	// Two events on the Redis stream before any connection opens.
	emitToBoth("pool/redis-1")
	emitToBoth("pool/redis-2")

	readers := make([]*sseReader, connections)
	for i := range readers {
		readers[i] = openSSEReader(svc)
	}
	defer func() {
		for _, r := range readers {
			r.close()
		}
	}()
	awaitAll(t, readers, len(order), "the Redis-served backlog")

	// A live event on the Redis tail, then one that no gateway replica buffers
	// (a controller or peer-replica event), so the position each connection
	// carries into the fall-back is absent from the source it switches to.
	emitToBoth("pool/redis-live")
	emitToStream("pool/redis-only")
	awaitAll(t, readers, len(order), "the live Redis tail events")

	// The outage: every connection moves to the gateway-buffer fan-out. The
	// event it serves never reached Redis, so the position each connection
	// carries back is in turn absent from the recovered stream.
	health.redis.Store(false)
	addGatewayOnly("pool/gateway-only", 900)
	awaitAll(t, readers, len(order), "the gateway-buffer fall-back event")

	// Recovery: every connection returns to the Redis tail and keeps going.
	health.redis.Store(true)
	emitToBoth("pool/redis-3")
	awaitAll(t, readers, len(order), "the post-recovery Redis tail event")

	for i, r := range readers {
		got := r.delivered()
		if len(got) != len(order) {
			t.Fatalf("connection %d received %v; want each event exactly once in emission order %v", i, got, order)
		}
		for j := range order {
			if got[j] != order[j] {
				t.Fatalf("connection %d delivered %v; want emission order %v (a duplicate, a drop, or a reorder at the source switch)", i, got, order)
			}
		}
	}
}

// awaitAll waits until every connection has received want frames, so the test
// advances only once the whole fleet has observed the current state.
func awaitAll(t *testing.T, readers []*sseReader, want int, what string) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for {
		pending := -1
		for i, r := range readers {
			if len(r.delivered()) < want {
				pending = i
				break
			}
		}
		if pending < 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("connection %d never received %s (has %v, want %d frames)", pending, what, readers[pending].delivered(), want)
		}
		time.Sleep(50 * time.Millisecond)
	}
}
