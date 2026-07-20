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
	sortByTimeThenKey(out)
	return out
}

// sortByTimeThenKey orders a merged window oldest-first by event time, with the
// eventKey as a stable tie break so two same-instant events from two sources
// keep a deterministic order.
func sortByTimeThenKey(events []gwevents.BufferedEvent) {
	sort.SliceStable(events, func(i, j int) bool {
		ti, tj := events[i].Event.Time, events[j].Event.Time
		if !ti.Equal(tj) {
			return ti.Before(tj)
		}
		return events[i].Event.ID < events[j].Event.ID
	})
}

// unionLocalOrigin merges this replica's local ring into a gateway fan-out
// window, deduplicated by eventKey and re-ordered oldest-first.
//
// The gateway rings buffer gateway-originated events only, and during a
// Redis-down outage the XADD that would carry a lenny-ops-originated event to
// the shared stream is failing, so the local ring is the only place those
// events exist. Serving the fan-out alone would drop them for the whole outage,
// making a Redis-only outage lose more than the strictly worse dual outage,
// where the local ring is served. spec: §25.5 (the in-memory buffer stays the
// canonical source of lenny-ops-originated events; eventKey dedup).
func (s *Service) unionLocalOrigin(merged []gwevents.BufferedEvent, filter gwevents.EventFilter) []gwevents.BufferedEvent {
	local := s.buffer.Query(0, filter, DefaultBufferCapacity).Events
	if len(local) == 0 {
		return merged
	}
	seen := make(map[string]struct{}, len(merged))
	for _, ev := range merged {
		seen[ev.Event.ID] = struct{}{}
	}
	out := make([]gwevents.BufferedEvent, 0, len(merged)+len(local))
	out = append(out, merged...)
	for _, ev := range local {
		if _, ok := seen[ev.Event.ID]; ok {
			continue
		}
		seen[ev.Event.ID] = struct{}{}
		out = append(out, ev)
	}
	sortByTimeThenKey(out)
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
// merges the per-replica pages with this replica's local ring (the
// lenny-ops-origin source, which no gateway replica buffers), applies the full
// §25.5 filter, resumes after
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

	merged = s.unionLocalOrigin(merged, filter)

	matched := make([]gwevents.BufferedEvent, 0, len(merged))
	for _, ev := range merged {
		if filter.Matches(ev.Event) {
			matched = append(matched, ev)
		}
	}

	// Resume at the continuation point: the first matching event ordering
	// after the cursor's eventKey. A cursor absent from the window but inside
	// its range is the ordinary source-switch case (the caller's last event
	// originated where no gateway replica buffers it), so it resumes without a
	// gap. A cursor ordering before the oldest entry the replicas still retain
	// has aged out of the window, which the response reports with gapDetected
	// and oldestAvailableCursor so the caller resyncs rather than silently
	// re-consuming the window. The eviction test runs over the raw merged
	// window rather than the filtered one, so narrowing ?eventType= or
	// ?severity= is never misread as an eviction. spec: §25.5 (cross-source
	// cursor translation with gapDetected and oldestAvailableCursor on a miss).
	start := gatewayResumeIndex(matched, position)
	gap := position != "" && len(merged) > 0 && eventKeyLess(position, merged[0].Event.ID)
	if gap {
		// Serve the whole retained window alongside the gap flag, matching the
		// buffer- and Redis-served gap paths.
		start = 0
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
	} else if position != "" && !gap {
		page.Pagination.Cursor = encodeCursor(cursorKind, position)
	}
	if gap {
		page.Pagination.GapDetected = true
		page.Pagination.GapReason = "cursor could not be resolved against the current gateway-buffer window: the referenced event aged out of every replica's ring"
		page.Pagination.OldestAvailableCursor = encodeCursor(SourceKindMixed, merged[0].Event.ID)
		page.Pagination.SuggestedAction = "resync"
		s.observeGap()
	}
	return page
}

// gatewayResumeIndex returns the index in window of the first event ordering
// after position, which is where a page resuming from that cursor starts. An
// exact match resumes after it; an absent cursor resumes before the first
// greater event so nothing after it is skipped; a cursor ordering after the
// whole window yields an empty page. spec: §25.5 (cross-source cursor
// translation, continuation at the first greater-or-equal eventKey).
func gatewayResumeIndex(window []gwevents.BufferedEvent, position string) int {
	if position == "" {
		return 0
	}
	for i, ev := range window {
		if ev.Event.ID == position {
			return i + 1
		}
		if eventKeyLess(position, ev.Event.ID) {
			return i
		}
	}
	return len(window)
}
