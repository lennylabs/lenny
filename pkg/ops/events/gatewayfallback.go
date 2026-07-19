// SPDX-License-Identifier: MIT

package events

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"time"

	gwevents "github.com/lennylabs/lenny/pkg/events"
	"github.com/lennylabs/lenny/pkg/ops/gateway"
)

// gatewayBufferPath is the §25.3 per-replica event-buffer query the §25.5
// Redis-down fall-back fans across every gateway pod. spec: §25.3 (Gateway
// Event Buffer).
const gatewayBufferPath = "/v1/admin/events/buffer"

// gatewayFetchTimeout bounds the whole cross-replica fan-out call so a poll
// or the SSE 2-second fall-back tick never blocks unbounded on a struggling
// gateway. Each per-replica request is separately bounded by the gateway
// client's FanOutTimeout; this caps the aggregate. spec: §25.5.
const gatewayFetchTimeout = 5 * time.Second

// GatewayBufferSource fans the §25.3 gateway event-buffer query across every
// gateway replica over the lenny-gateway-pods headless Service. It is the
// §25.5 Redis-down fall-back source: with Redis
// unreachable and the gateway up, the read surface serves gateway-originated
// events from the per-replica buffers rather than the local ring buffer,
// which holds only lenny-ops-originated events.
//
// It is a consumer-side interface satisfied by *pkg/ops/gateway.Client, whose
// FanOutGet already discovers pods over the headless Service, bounds each
// per-replica call, and returns each replica's raw JSON body. spec: §25.5
// (Redis-down gateway-buffer fallback), §25.3 (cross-replica eventKey dedup).
type GatewayBufferSource interface {
	FanOutGet(ctx context.Context, path string) ([]gateway.ReplicaResult, error)
}

// fetchGatewayBuffer fans the §25.3 buffer query across every gateway replica
// and returns the merged, eventKey-deduplicated events, ordered oldest-first.
// The event-type and severity filters ride to each replica as query params so
// each pod narrows before responding; the caller applies the full §25.5
// filter (resource, time bounds) over the merged result. The whole fan-out is
// bounded by gatewayFetchTimeout. spec: §25.5 (transparent source fall-back),
// §25.3 (cross-replica eventKey dedup).
func (s *Service) fetchGatewayBuffer(ctx context.Context, filter gwevents.EventFilter) ([]gwevents.BufferedEvent, error) {
	if s.gateway == nil {
		return nil, fmt.Errorf("gateway buffer source not wired")
	}
	cctx, cancel := context.WithTimeout(ctx, gatewayFetchTimeout)
	defer cancel()
	results, err := s.gateway.FanOutGet(cctx, gatewayBufferPath+bufferFilterQuery(filter))
	if err != nil {
		return nil, fmt.Errorf("gateway buffer fan-out: %w", err)
	}
	return mergeReplicaBuffers(results), nil
}

// bufferFilterQuery renders the §25.3 buffer endpoint query string for the
// event-type and severity dimensions the endpoint honours. The remaining
// §25.5 filter dimensions (resource type/id, time bounds) are applied locally
// over the merged result because the buffer endpoint does not accept them.
func bufferFilterQuery(filter gwevents.EventFilter) string {
	q := url.Values{}
	if filter.EventType != "" {
		q.Set("eventType", filter.EventType)
	}
	if filter.Severity != "" {
		q.Set("severity", filter.Severity)
	}
	if len(q) == 0 {
		return ""
	}
	return "?" + q.Encode()
}

// mergeReplicaBuffers merges the per-replica §25.3 buffer pages and
// deduplicates by eventKey with a secondary content-hash check. The primary
// dedup key is the CloudEvents id (eventKey): two distinct events carry
// distinct eventKeys (the key embeds the replica id and a monotonic nonce),
// so two same-second alert_fired events from two replicas are preserved as
// two events. A genuine repeat delivery of one event carries the same
// eventKey and the same content, so it collapses to one. The content hash is
// the secondary guard: if two entries somehow share an eventKey but differ in
// content, both are kept rather than silently dropping a distinct event.
// Ordering is oldest-first by event time, then eventKey for a stable tie
// break. spec: §25.3 (cross-replica eventKey dedup, not a content hash).
func mergeReplicaBuffers(results []gateway.ReplicaResult) []gwevents.BufferedEvent {
	seen := make(map[string]struct{})
	out := make([]gwevents.BufferedEvent, 0)
	for _, r := range results {
		if r.Err != nil || len(r.Body) == 0 {
			// A per-replica failure lands in Err; the fan-out is best-effort,
			// so a failed or empty replica is skipped and the merge proceeds
			// with the replicas that did respond.
			continue
		}
		var page gwevents.BufferedEventPage
		if err := json.Unmarshal(r.Body, &page); err != nil {
			continue
		}
		for _, ev := range page.Events {
			key := dedupKey(ev.Event)
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			out = append(out, ev)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		ti, tj := out[i].Event.Time, out[j].Event.Time
		if !ti.Equal(tj) {
			return ti.Before(tj)
		}
		return out[i].Event.ID < out[j].Event.ID
	})
	return out
}

// dedupKey is the §25.3 cross-replica dedup key: the eventKey (CloudEvents id)
// joined with a content hash. Same eventKey and same content collapse to one
// entry; a differing content under the same eventKey is kept, so a false
// content collision never drops a distinct event. spec: §25.3.
func dedupKey(e gwevents.OperationalEvent) string {
	body, _ := json.Marshal(e)
	sum := sha256.Sum256(body)
	return e.ID + "\x00" + string(sum[:])
}

// gatewayPollPage serves the §25.5 polling page from the gateway event-buffer
// fan-out during a Redis-down / gateway-up outage. It
// merges the per-replica pages, applies the full §25.5 filter, resumes after
// the incoming cursor's eventKey, and pages at limit. The response carries a
// mixed cursorKind because the merged view spans replicas; a fan-out failure
// returns an empty page echoing the caller's cursor so a retry resumes from
// the same position. The EVENT_STREAM_DEGRADED envelope is attached by the
// caller. spec: §25.5 (Redis-down gateway-buffer fallback, eventKey dedup).
func (s *Service) gatewayPollPage(ctx context.Context, cursorKind, position string, filter gwevents.EventFilter, limit int, desc bool) EventPage {
	merged, err := s.fetchGatewayBuffer(ctx, filter)
	if err != nil {
		return EventPage{
			Items:      []gwevents.BufferedEvent{},
			Pagination: Pagination{CursorKind: SourceKindMixed, Cursor: encodeCursor(cursorKind, position)},
		}
	}

	matched := make([]gwevents.BufferedEvent, 0, len(merged))
	for _, ev := range merged {
		if filter.Matches(ev.Event) {
			matched = append(matched, ev)
		}
	}

	// Resume after the cursor's eventKey. A cursor whose eventKey is no
	// longer in the merged window serves from the start of the window: the
	// gateway buffer is an inherently bounded per-replica window, so a missing
	// position is the ordinary catch-up case rather than a stream eviction to
	// flag.
	start := 0
	if position != "" {
		for i, ev := range matched {
			if ev.Event.ID == position {
				start = i + 1
				break
			}
		}
	}
	window := matched[start:]

	hasMore := false
	if limit > 0 && len(window) > limit {
		window = window[:limit]
		hasMore = true
	}
	items := append([]gwevents.BufferedEvent{}, window...)
	if desc {
		items = reversed(items)
	}

	page := EventPage{
		Items:      items,
		Pagination: Pagination{HasMore: hasMore, CursorKind: SourceKindMixed},
	}
	if n := len(matched); n > 0 {
		page.Pagination.HeadCursor = encodeCursor(SourceKindMixed, matched[n-1].Event.ID)
	}
	if n := len(window); n > 0 {
		page.Pagination.Cursor = encodeCursor(SourceKindMixed, window[n-1].Event.ID)
	} else if position != "" {
		page.Pagination.Cursor = encodeCursor(cursorKind, position)
	}
	return page
}
