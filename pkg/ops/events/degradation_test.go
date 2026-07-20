// SPDX-License-Identifier: MIT

package events_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/redis/go-redis/v9"

	"github.com/lennylabs/lenny/pkg/events"
	opsstream "github.com/lennylabs/lenny/pkg/ops/events"
	"github.com/lennylabs/lenny/pkg/ops/gateway"
)

// emptyRedisStream is a wired but empty §25.5 Redis read source. Wiring it is
// what makes a healthy source-health signal resolve to the Redis stream: with
// no client wired the read surface has no cross-replica source at all and
// reports the local-buffer degradation instead.
type emptyRedisStream struct{}

func (emptyRedisStream) XRangeN(ctx context.Context, _, _, _ string, _ int64) *redis.XMessageSliceCmd {
	return redis.NewXMessageSliceCmd(ctx)
}

func (emptyRedisStream) XRevRangeN(ctx context.Context, _, _, _ string, _ int64) *redis.XMessageSliceCmd {
	return redis.NewXMessageSliceCmd(ctx)
}

func (emptyRedisStream) TailClient() (opsstream.RedisTailClient, error) {
	return idleTail{}, nil
}

// idleTail parks its blocking read until the connection's context is done, the
// way an XREAD BLOCK 0 on an idle stream does.
type idleTail struct{}

func (idleTail) XRead(ctx context.Context, _ *redis.XReadArgs) *redis.XStreamSliceCmd {
	<-ctx.Done()
	cmd := redis.NewXStreamSliceCmd(ctx)
	cmd.SetErr(ctx.Err())
	return cmd
}

func (idleTail) Close() error { return nil }

// stubGatewaySource is a wired §25.5 gateway-buffer fall-back source that
// reports no gateway-originated events. Wiring it is what makes a Redis outage
// the case-1 gateway-buffer fall-back: with no source wired the read surface
// resolves to the case-4 dual outage, because gateway-originated events then
// have nowhere to fetch from.
type stubGatewaySource struct{}

func (stubGatewaySource) FanOutGet(context.Context, string) ([]gateway.ReplicaResult, error) {
	return nil, nil
}

// publishOne records one lenny-ops-originated event into the service buffer.
func publishOne(s *opsstream.Service, typ string) {
	s.Publish(context.Background(), events.OperationalEvent{Type: typ, Severity: "warning"})
}

// spec: §25.5 lines 2768-2772 (case 1) — Redis unreachable, gateway up:
// the poll surface serves normally and attaches the canonical
// degradation envelope with actualSource "gateway-buffer" (HTTP 200,
// EVENT_STREAM_DEGRADED returned as response metadata).
func TestHandlePoll_RedisDown_AttachesDegradation_spec_25_5(t *testing.T) {
	s := opsstream.New(opsstream.Options{
		Capacity:     16,
		Now:          fixedNow,
		SourceHealth: opsstream.StaticSourceHealth{Redis: false, Gateway: true},
	})
	s.SetGatewayBufferSource(stubGatewaySource{})
	publishOne(s, "dev.lenny.alert_fired")

	rec := httptest.NewRecorder()
	s.HandlePoll(rec, platformAdminReq(httptest.NewRequest(http.MethodGet, "/v1/admin/events", nil)))

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d, want 200", rec.Code)
	}
	var page opsstream.EventPage
	if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if page.Degradation == nil {
		t.Fatal("expected a degradation envelope on the Redis-down poll")
	}
	if page.Degradation.ActualSource != "gateway-buffer" {
		t.Errorf("actualSource=%q, want gateway-buffer", page.Degradation.ActualSource)
	}
	if page.Degradation.PrimarySource != "redis-stream" {
		t.Errorf("primarySource=%q, want redis-stream", page.Degradation.PrimarySource)
	}
	if len(page.Items) != 1 {
		t.Errorf("poll should still serve buffered events: got %d", len(page.Items))
	}
}

// spec: §25.5 lines 2775-2780 (case 4) — Redis AND gateway unreachable:
// polling for the stream returns 503 EVENT_STREAM_UNAVAILABLE because
// gateway-originated events have nowhere to land and polling cannot
// partial-serve.
func TestHandlePoll_DualOutage_503_spec_25_5(t *testing.T) {
	s := opsstream.New(opsstream.Options{
		Capacity:     16,
		Now:          fixedNow,
		SourceHealth: opsstream.StaticSourceHealth{Redis: false, Gateway: false},
	})
	publishOne(s, "dev.lenny.escalation_created")

	rec := httptest.NewRecorder()
	s.HandlePoll(rec, platformAdminReq(httptest.NewRequest(http.MethodGet, "/v1/admin/events", nil)))

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d, want 503", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "EVENT_STREAM_UNAVAILABLE") {
		t.Errorf("body should carry EVENT_STREAM_UNAVAILABLE: %s", rec.Body.String())
	}
}

// spec: §25.5 line 2769 (case 2) — Redis up: no fall-back is needed, so
// the poll carries no degradation envelope regardless of gateway state.
func TestHandlePoll_RedisUp_NoDegradation_spec_25_5(t *testing.T) {
	for _, gw := range []bool{true, false} {
		s := opsstream.New(opsstream.Options{
			Capacity:     16,
			Now:          fixedNow,
			SourceHealth: opsstream.StaticSourceHealth{Redis: true, Gateway: gw},
			RedisClient:  emptyRedisStream{},
		})
		publishOne(s, "dev.lenny.alert_fired")
		rec := httptest.NewRecorder()
		s.HandlePoll(rec, platformAdminReq(httptest.NewRequest(http.MethodGet, "/v1/admin/events", nil)))
		if rec.Code != http.StatusOK {
			t.Fatalf("gateway=%v: status=%d, want 200", gw, rec.Code)
		}
		var page opsstream.EventPage
		if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if page.Degradation != nil {
			t.Errorf("gateway=%v: unexpected degradation envelope when Redis is up: %+v", gw, page.Degradation)
		}
	}
}

// A nil SourceHealth preserves the pre-degradation behavior: no envelope,
// no 503.
func TestHandlePoll_NilHealth_NoDegradation(t *testing.T) {
	s := opsstream.New(opsstream.Options{Capacity: 16, Now: fixedNow, RedisClient: emptyRedisStream{}})
	publishOne(s, "dev.lenny.alert_fired")
	rec := httptest.NewRecorder()
	s.HandlePoll(rec, platformAdminReq(httptest.NewRequest(http.MethodGet, "/v1/admin/events", nil)))
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d, want 200", rec.Code)
	}
	var page opsstream.EventPage
	if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if page.Degradation != nil {
		t.Errorf("nil SourceHealth must not attach a degradation envelope: %+v", page.Degradation)
	}
}

// spec: §25.5 line 2779 (case 4) — the SSE surface writes the
// :degradation comment carrying actualSource lenny-ops-local-buffer and
// unavailableFields ["gateway-events"], then still serves this replica's
// own (lenny-ops-originated) events from the local buffer.
func TestHandleStream_DualOutage_DegradationComment_spec_25_5(t *testing.T) {
	s := opsstream.New(opsstream.Options{
		Capacity:     16,
		Now:          fixedNow,
		SourceHealth: opsstream.StaticSourceHealth{Redis: false, Gateway: false},
	})
	publishOne(s, "dev.lenny.escalation_created")

	req := httptest.NewRequest(http.MethodGet, "/v1/admin/events/stream", nil)
	rec := newStreamingRecorder()
	ctx, cancel := context.WithCancel(req.Context())
	done := make(chan struct{})
	go func() {
		s.HandleStream(rec, platformAdminReq(req.WithContext(ctx)))
		close(done)
	}()
	// The backlog (degradation comment + the ops event) is written
	// synchronously before live streaming begins; cancel to end the loop.
	cancel()
	<-done

	body := rec.Body.String()
	if !strings.Contains(body, ":degradation ") {
		t.Fatalf("missing :degradation comment line: %q", body)
	}
	if !strings.Contains(body, "lenny-ops-local-buffer") {
		t.Errorf("degradation comment should name lenny-ops-local-buffer: %q", body)
	}
	if !strings.Contains(body, "gateway-events") {
		t.Errorf("degradation comment should list gateway-events as unavailable: %q", body)
	}
	// The replica's own ops-originated event is still delivered.
	if !strings.Contains(body, "escalation_created") {
		t.Errorf("dual-outage SSE should still serve lenny-ops-originated events: %q", body)
	}
}

// spec: §25.5 line 2772 (case 1) — the SSE surface announces the
// gateway-buffer fall-back via the :degradation comment during a Redis
// outage.
func TestHandleStream_RedisDown_DegradationComment_spec_25_5(t *testing.T) {
	s := opsstream.New(opsstream.Options{
		Capacity:     16,
		Now:          fixedNow,
		SourceHealth: opsstream.StaticSourceHealth{Redis: false, Gateway: true},
	})
	s.SetGatewayBufferSource(stubGatewaySource{})

	req := httptest.NewRequest(http.MethodGet, "/v1/admin/events/stream", nil)
	rec := newStreamingRecorder()
	ctx, cancel := context.WithCancel(req.Context())
	done := make(chan struct{})
	go func() {
		s.HandleStream(rec, platformAdminReq(req.WithContext(ctx)))
		close(done)
	}()
	cancel()
	<-done

	body := rec.Body.String()
	if !strings.Contains(body, ":degradation ") || !strings.Contains(body, "gateway-buffer") {
		t.Fatalf("expected a gateway-buffer :degradation comment: %q", body)
	}
}

// spec: §25.5 line 2769 — Redis up: the SSE surface emits no degradation
// comment.
func TestHandleStream_Healthy_NoComment_spec_25_5(t *testing.T) {
	s := opsstream.New(opsstream.Options{
		Capacity:     16,
		Now:          fixedNow,
		SourceHealth: opsstream.StaticSourceHealth{Redis: true, Gateway: true},
		RedisClient:  emptyRedisStream{},
	})
	req := httptest.NewRequest(http.MethodGet, "/v1/admin/events/stream", nil)
	rec := newStreamingRecorder()
	ctx, cancel := context.WithCancel(req.Context())
	done := make(chan struct{})
	go func() {
		s.HandleStream(rec, platformAdminReq(req.WithContext(ctx)))
		close(done)
	}()
	cancel()
	<-done
	if strings.Contains(rec.Body.String(), ":degradation") {
		t.Errorf("healthy stream must not write a degradation comment: %q", rec.Body.String())
	}
}

// StaticSourceHealth reports its configured flags; the zero value is
// both-down (the conservative dual-outage state).
func TestStaticSourceHealth(t *testing.T) {
	h := opsstream.StaticSourceHealth{Redis: true, Gateway: false}
	if !h.RedisAvailable() || h.GatewayAvailable() {
		t.Fatalf("StaticSourceHealth flags not reported: %+v", h)
	}
	var zero opsstream.StaticSourceHealth
	if zero.RedisAvailable() || zero.GatewayAvailable() {
		t.Errorf("zero StaticSourceHealth should report both sources down")
	}
}

// spec: §25.5 lines 2768-2780 (the degradation matrix keys on the two source-
// health signals and enumerates Redis-reachable as the healthy, envelope-free
// state) — a replica with no Redis client wired is healthy, not degraded. Its
// poll response carries no degradation envelope and its SSE connection emits no
// :degradation comment, so the three-case matrix stays the whole matrix.
func TestReadSurface_NoRedisWired_ServesHealthyWithNoDegradationEnvelope_spec_25_5(t *testing.T) {
	s := opsstream.New(opsstream.Options{Capacity: 16, Now: fixedNow})
	publishOne(s, "dev.lenny.alert_fired")

	rec := httptest.NewRecorder()
	s.HandlePoll(rec, platformAdminReq(httptest.NewRequest(http.MethodGet, "/v1/admin/events", nil)))
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d, want 200: the surface still serves this replica's own events", rec.Code)
	}
	var page opsstream.EventPage
	if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if page.Degradation != nil {
		t.Errorf("poll carried a degradation envelope (%+v) on a healthy replica with no Redis client wired; §25.5 classifies that state healthy", page.Degradation)
	}
	if len(page.Items) != 1 {
		t.Errorf("page served %d item(s), want the one locally published event", len(page.Items))
	}

	req := httptest.NewRequest(http.MethodGet, "/v1/admin/events/stream", nil)
	sink := newStreamingRecorder()
	ctx, cancel := context.WithCancel(req.Context())
	done := make(chan struct{})
	go func() {
		s.HandleStream(sink, platformAdminReq(req.WithContext(ctx)))
		close(done)
	}()
	cancel()
	<-done
	if strings.Contains(sink.Body.String(), ":degradation") {
		t.Errorf("the stream emitted a :degradation comment on a healthy replica with no Redis client wired: %q", sink.Body.String())
	}
}
