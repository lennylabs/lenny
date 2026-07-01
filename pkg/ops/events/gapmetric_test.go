// SPDX-License-Identifier: MIT

package events

import (
	"context"
	"net/url"
	"testing"

	gwevents "github.com/lennylabs/lenny/pkg/events"
)

// TestOnGapFiresOnEvictedCursor is the §25.5 line 2788 contract: a poll
// (or stream) request that resolves an evicted cursor — returning
// gapDetected — invokes the OnGap hook that backs
// lenny_ops_events_stream_gaps_total.
func TestOnGapFiresOnEvictedCursor_spec_25_5_2788(t *testing.T) {
	var gaps int
	s := New(Options{Capacity: 4, Now: ts, OnGap: func() { gaps++ }})

	s.Publish(context.Background(), gwevents.OperationalEvent{Type: "alert_fired"})
	staleKey := s.Query(0, gwevents.EventFilter{}, 0).Events[0].Event.ID
	staleCursor := encodeCursor(SourceKindBuffer, staleKey)
	for i := 0; i < 8; i++ {
		s.Publish(context.Background(), gwevents.OperationalEvent{Type: "alert_fired"})
	}

	page := poll(t, s, "/v1/admin/events?cursor="+url.QueryEscape(staleCursor))
	if !page.Pagination.GapDetected {
		t.Fatal("expected gapDetected for an evicted cursor")
	}
	if gaps != 1 {
		t.Errorf("OnGap fired %d times, want 1", gaps)
	}

	// A normal poll from the head does not register a gap.
	s.Publish(context.Background(), gwevents.OperationalEvent{Type: "alert_fired"})
	headKey := s.Query(0, gwevents.EventFilter{}, 0).Events[0].Event.ID
	poll(t, s, "/v1/admin/events?cursor="+url.QueryEscape(encodeCursor(SourceKindBuffer, headKey)))
	if gaps != 1 {
		t.Errorf("OnGap fired again on a live cursor: total %d, want 1", gaps)
	}
}
