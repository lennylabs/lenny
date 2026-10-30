// SPDX-License-Identifier: MIT

//go:build load_local

// Tier-7a load_local no-drop coverage for the §25.5 SSE gateway-buffer stint's
// one-shot resume seed. The seed marks every event in the first fan-out window
// that orders at or before the connection's resume position as already
// delivered, so the stint emits only the continuation. The position it seeds
// against must be the one the connection held when it entered the stint. A
// Redis-only outage is exactly when a transient fan-out failure is expected, so
// the first tick routinely answers nothing while this replica's own local
// publishes keep being delivered on the same connection. Each of those
// deliveries advances the session's last delivered key, and a seed that reads
// that live key instead of the entry position marks the gateway-originated
// events emitted before the local publish as already sent and drops them, with
// no :gap comment, for the rest of the outage.
//
// spec: §25.5 (SSE fall-back polls the gateway-buffer fan-out every 2 seconds;
// the last delivered eventKey is tracked across the switch so no event is
// dropped or duplicated at the source boundary).

package tier7a_load_local_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	gwevents "github.com/lennylabs/lenny/pkg/events"
	opsstream "github.com/lennylabs/lenny/pkg/ops/events"
	"github.com/lennylabs/lenny/pkg/ops/gateway"
)

// TestOpsEventStreamGatewayStintDeliversEventsOlderThanAnInterleavedLocalPublish
// opens an SSE connection directly into the §25.5 Redis-down gateway-buffer
// fall-back carrying a Last-Event-ID resume position, fails the first fan-out
// tick the way a struggling gateway does during a Redis outage, lands a
// lenny-ops-originated local publish while that tick is failing, and then lets
// the fan-out answer with a gateway-originated event whose eventKey orders
// before the local publish but after the resume position.
//
// That gateway event is a continuation of the connection's position and must be
// written exactly once. A stint that seeds its resume marks from the session's
// live last delivered key rather than from its entry position pre-marks the
// event as delivered and never writes it, and because the seed treats it as
// already sent no :gap comment is emitted either, so the loss is silent.
//
// spec: 25.5 (cross-switch no-drop ordering, gateway-buffer fall-back resume)
// diagnosis: a failure means the SSE gateway-buffer stint drops
// gateway-originated events during a Redis outage. Every event whose eventKey
// orders before a local publish that landed while the fan-out was failing is
// suppressed by the resume seed and never reaches the connection, with no gap
// signal, so a consumer reading across a Redis outage silently loses the
// gateway's events for the whole outage.
func TestOpsEventStreamGatewayStintDeliversEventsOlderThanAnInterleavedLocalPublish(t *testing.T) {
	// The fan-out fails until the test opens it, modelling the transient
	// gateway failure a Redis-only outage makes likely on the first tick.
	var serving atomic.Bool
	var window atomic.Value // gwevents.BufferedEventPage
	window.Store(gwevents.BufferedEventPage{})
	gwSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !serving.Load() {
			http.Error(w, "gateway buffer unavailable", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(window.Load().(gwevents.BufferedEventPage))
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

	svc := opsstream.New(opsstream.Options{
		// Redis down, gateway up: the §25.5 case-1 fall-back, which the
		// connection opens directly into.
		SourceHealth: opsstream.StaticSourceHealth{Redis: false, Gateway: true},
		ReplicaID:    "ops-1",
	})
	svc.SetGatewayBufferSource(gwClient)

	// The connection resumes from a position older than everything below, so
	// both the gateway event and the local publish are continuations of it.
	base := time.Now().Add(-time.Minute)
	resumeKey := fmt.Sprintf("gw-1:%d:1", base.UnixNano())
	gatewayKey := fmt.Sprintf("gw-1:%d:2", base.Add(time.Second).UnixNano())

	reader := openSSEReaderResuming(svc, resumeKey)
	defer reader.close()

	// While the fan-out is failing, a lenny-ops-originated event lands in this
	// replica's local ring and is delivered on the open connection, advancing
	// the session's live last delivered key past the gateway event below.
	local, err := svc.PublishBuffered(t.Context(), gwevents.OperationalEvent{
		Type:            gwevents.EventType("escalation_created").CloudEventsType(),
		Subject:         "ops/local-1",
		Severity:        "warning",
		DataContentType: gwevents.ContentTypeJSON,
		Data:            json.RawMessage(`{"escalation":"x"}`),
	})
	if err != nil {
		t.Fatalf("publish local event: %v", err)
	}
	awaitKey(t, reader, local.Event.ID, "the lenny-ops-originated local publish")

	// The fan-out recovers and answers with a gateway-originated event whose
	// eventKey orders before the local publish already delivered.
	window.Store(gwevents.BufferedEventPage{Events: []gwevents.BufferedEvent{{
		ID: 1,
		Event: gwevents.OperationalEvent{
			ID:              gatewayKey,
			Type:            gwevents.EventType("alert_fired").CloudEventsType(),
			SpecVersion:     gwevents.CloudEventsSpecVersion,
			Subject:         "pool/gateway-1",
			Severity:        "warning",
			DataContentType: gwevents.ContentTypeJSON,
			Data:            json.RawMessage(`{"alert":"x"}`),
			Time:            base.Add(time.Second).UTC(),
		},
	}}})
	serving.Store(true)

	awaitKey(t, reader, gatewayKey, "the gateway-originated event older than the interleaved local publish")

	// Exactly once: the stint re-fetches the same window every 2 seconds, so a
	// missing dedup would show up as a repeat of the same key.
	time.Sleep(3 * gatewayPollIntervalForTest)
	seen := 0
	for _, k := range reader.delivered() {
		if k == gatewayKey {
			seen++
		}
	}
	if seen != 1 {
		t.Fatalf("gateway event delivered %d times across %v, want exactly once", seen, reader.delivered())
	}
}

// gatewayPollIntervalForTest mirrors the §25.5 SSE fall-back poll interval so
// the assertions above wait out whole fan-out ticks.
const gatewayPollIntervalForTest = 2 * time.Second

// openSSEReaderResuming opens an SSE connection carrying the SSE-standard
// Last-Event-ID resume position, so the session enters its first stint with a
// non-empty last delivered key.
func openSSEReaderResuming(svc *opsstream.Service, lastEventID string) *sseReader {
	return openSSEReaderWith(svc, func(req *http.Request) {
		req.Header.Set("Last-Event-ID", lastEventID)
	})
}

// awaitKey waits until the connection has written a frame for key.
func awaitKey(t *testing.T, r *sseReader, key, what string) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for {
		for _, got := range r.delivered() {
			if got == key {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("the connection never received %s (key %s); it received %v", what, key, r.delivered())
		}
		time.Sleep(50 * time.Millisecond)
	}
}
