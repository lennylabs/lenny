// SPDX-License-Identifier: MIT

package events_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/pkg/events"
	opsstream "github.com/lennylabs/lenny/pkg/ops/events"
	"github.com/lennylabs/lenny/pkg/ops/gateway"
)

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
	s := opsstream.New(opsstream.Options{Capacity: 16, Now: fixedNow})
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
