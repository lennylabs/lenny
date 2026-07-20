// SPDX-License-Identifier: MIT

package events

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	gwevents "github.com/lennylabs/lenny/pkg/events"
)

// flippingHealth is a SourceHealth whose Redis answer changes between two
// consecutive reads: the first read reports Redis reachable and every later
// read reports it unreachable. It stands in for the background probe
// refreshing between two source resolutions inside one request. The gateway
// stays reachable so the second answer classifies as the Redis-down
// gateway-buffer fall-back rather than the dual outage.
type flippingHealth struct {
	reads atomic.Int64
}

func (h *flippingHealth) RedisAvailable() bool   { return h.reads.Add(1) == 1 }
func (h *flippingHealth) GatewayAvailable() bool { return true }

// spec: 25.5 (the degradation envelope's actualSource names the source the
// response was served from) — HandlePoll must resolve the read source once
// per request. Resolving it a second time for the data path lets a
// SourceHealth refresh landing between the two resolutions serve a page from
// the gateway-buffer fan-out while attaching no degradation envelope, so the
// caller reads a Redis-stream view that it never received. The pre-fix
// HandlePoll called selectSource once for the envelope and pollPage called it
// again for the data path; against that code this test sees the
// gateway-originated event with a nil degradation envelope and fails.
func TestHandlePoll_SourceLabelAndDataPathAgree_spec_25_5(t *testing.T) {
	health := &flippingHealth{}
	f := &fakeStream{}
	f.add("1-0", evt("ops:1000:1", "dev.lenny.alert_fired"))
	s := New(Options{RedisClient: f, SourceHealth: health, Now: ts})
	s.SetGatewayBufferSource(&fakeGatewaySource{pages: [][]gwevents.BufferedEvent{{
		bufEvt("gw-a:2000:1", "dev.lenny.alert_fired"),
	}}})

	rec := httptest.NewRecorder()
	s.HandlePoll(rec, httptest.NewRequest("GET", "/v1/admin/events", nil))

	var page EventPage
	if err := json.NewDecoder(rec.Body).Decode(&page); err != nil {
		t.Fatalf("decode poll page: %v", err)
	}

	served := make([]string, 0, len(page.Items))
	for _, it := range page.Items {
		served = append(served, it.Event.ID)
	}
	fromGateway := false
	fromRedis := false
	for _, key := range served {
		switch {
		case strings.HasPrefix(key, "gw-"):
			fromGateway = true
		case strings.HasPrefix(key, "ops:"):
			fromRedis = true
		}
	}

	if page.Degradation == nil {
		if fromGateway {
			t.Fatalf("page served the gateway-buffer fan-out (%v) with no degradation envelope; the label and the data path disagree", served)
		}
		if !fromRedis {
			t.Fatalf("undegraded page served no Redis-stream event; items = %v", served)
		}
		return
	}
	if page.Degradation.ActualSource != sourceGatewayBuffer {
		t.Fatalf("actualSource = %q, want %q", page.Degradation.ActualSource, sourceGatewayBuffer)
	}
	if !fromGateway {
		t.Fatalf("page labelled %q served no gateway-buffer event; items = %v", sourceGatewayBuffer, served)
	}
}
