// SPDX-License-Identifier: MIT

package events

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/lennylabs/lenny/pkg/ops/conventions"
)

// The §25.5 degradation-matrix source labels. They name the source a
// response was actually served from so an agent can reason about the
// history window and completeness of the events it received. spec:
// §25.5 lines 2768-2780.
const (
	// sourceRedisStream is the primary cross-replica source: the Redis
	// ops:events:stream every replica XADDs to.
	sourceRedisStream = "redis-stream"
	// sourceGatewayBuffer is the §25.3 gateway in-memory event buffer the
	// read surface falls back to when Redis is unreachable (case 1).
	sourceGatewayBuffer = "gateway-buffer"
	// sourceOpsLocalBuffer is this lenny-ops replica's own ring buffer,
	// the only observable source for lenny-ops-originated events during a
	// dual Redis + gateway outage (case 4).
	sourceOpsLocalBuffer = "lenny-ops-local-buffer"
)

// codeEventStreamUnavailable is the §25.5 error code returned when both
// the Redis stream and the gateway event buffer are unreachable, so
// gateway-originated events have nowhere to land. spec: §25.5 error
// table ("EVENT_STREAM_UNAVAILABLE | TRANSIENT | 503").
const codeEventStreamUnavailable = "EVENT_STREAM_UNAVAILABLE"

// SourceHealth reports the reachability of the two §25.5 event sources
// the lenny-ops read surface depends on: the Redis ops:events:stream
// (the primary cross-replica source) and the gateway's §25.3 in-memory
// event buffer (the fall-back the read surface drops to when Redis is
// unreachable). The Service consults it on every poll and stream request
// to compute the §25.5 degradation envelope. A nil SourceHealth is
// treated as fully healthy, preserving the pre-degradation behavior.
// spec: §25.5 lines 2768-2780.
type SourceHealth interface {
	// RedisAvailable reports whether the Redis ops:events:stream is
	// reachable.
	RedisAvailable() bool
	// GatewayAvailable reports whether the gateway event buffer is
	// reachable.
	GatewayAvailable() bool
}

// StaticSourceHealth is a fixed SourceHealth, used in tests and for a
// deployment that has no live probe wired. The zero value reports both
// sources down.
type StaticSourceHealth struct {
	Redis   bool
	Gateway bool
}

// RedisAvailable reports the configured Redis reachability.
func (h StaticSourceHealth) RedisAvailable() bool { return h.Redis }

// GatewayAvailable reports the configured gateway reachability.
func (h StaticSourceHealth) GatewayAvailable() bool { return h.Gateway }

// streamState classifies the §25.5 degradation case from the injected
// source health and returns the source the response is served from, the
// degradation envelope to attach (nil when not degraded), and whether
// the dual Redis + gateway outage holds (case 4), in which only
// lenny-ops-originated events are observable. spec: §25.5 lines
// 2768-2780.
func (s *Service) streamState() (actualSource string, deg *conventions.Degradation, dualDown bool) {
	if s.health == nil {
		return sourceRedisStream, nil, false
	}
	redisUp := s.health.RedisAvailable()
	gatewayUp := s.health.GatewayAvailable()
	switch {
	case redisUp:
		// Redis up: lenny-ops reads the stream directly. A gateway-down
		// (case 2) window needs no fallback — gateway-originated events
		// stop arriving, but that is not a read-surface degradation, so no
		// envelope is attached and lenny-ops-originated events flow
		// normally.
		return sourceRedisStream, nil, false
	case gatewayUp:
		return gatewayFallbackState()
	default:
		// Case 4 — Redis AND gateway unreachable: only this replica's
		// local ring buffer (lenny-ops-originated events) is observable.
		// Gateway-originated events have nowhere to land.
		return dualOutageState()
	}
}

// gatewayFallbackState returns the §25.5 case-1 classification: Redis is
// unreachable and the gateway event buffer serves in its place, reported as
// EVENT_STREAM_DEGRADED response metadata on an HTTP 200 rather than as an
// HTTP error. It is reached both from the health matrix and from a request
// whose Redis read failed inside the source-health refresh window, which is
// served from the same fall-back and must carry the same label. spec: §25.5
// lines 2768-2780 (Redis-down gateway-buffer fall-back).
func gatewayFallbackState() (actualSource string, deg *conventions.Degradation, dualDown bool) {
	return sourceGatewayBuffer, &conventions.Degradation{
		Level:         conventions.DegradationDegraded,
		PrimarySource: sourceRedisStream,
		ActualSource:  sourceGatewayBuffer,
		FallbackPath:  []string{sourceRedisStream, sourceGatewayBuffer},
		Warnings:      []string{"serving from the gateway event buffer; the Redis ops:events:stream is unreachable, so history is limited to the buffer window"},
	}, false
}

// dualOutageState returns the §25.5 case-4 classification: the local ring is
// the only observable source and gateway-originated events are unavailable.
// It is reached both from the health matrix (Redis and gateway both
// unreachable) and from source selection, where a Redis-down classification
// with no gateway source wired resolves to the same data path and must carry
// the same label. spec: §25.5 lines 2768-2780 (dual-outage case).
func dualOutageState() (actualSource string, deg *conventions.Degradation, dualDown bool) {
	return sourceOpsLocalBuffer, &conventions.Degradation{
		Level:             conventions.DegradationFailed,
		PrimarySource:     sourceRedisStream,
		ActualSource:      sourceOpsLocalBuffer,
		FallbackPath:      []string{sourceRedisStream, sourceGatewayBuffer, sourceOpsLocalBuffer},
		UnavailableFields: []string{"gateway-events"},
		Warnings:          []string{"both the Redis stream and the gateway buffer are unreachable; serving lenny-ops-originated events only"},
	}, true
}

// writeStreamUnavailable writes the §25.5 503 EVENT_STREAM_UNAVAILABLE
// error. It is the polling outcome during a dual Redis + gateway outage:
// the stream's gateway-originated events cannot be served and polling
// cannot partial-serve, so the request fails with a transient error the
// agent retries. spec: §25.5 error table.
func writeStreamUnavailable(w http.ResponseWriter) {
	conventions.WriteError(w, http.StatusServiceUnavailable, codeEventStreamUnavailable,
		conventions.CategoryTransient,
		"both the Redis ops:events:stream and the gateway event buffer are unreachable")
}

// writeSSEDegradation writes the §25.5 :degradation SSE comment line so a
// connected stream consumer learns it is receiving a degraded view (the
// gateway-buffer fall-back, or the lenny-ops-local-buffer during a dual
// outage) before the events arrive. The payload mirrors the canonical
// degradation envelope's actualSource / unavailableFields fields. spec:
// §25.5 line 2779 (":degradation {...}" comment).
func writeSSEDegradation(w http.ResponseWriter, deg *conventions.Degradation) {
	if deg == nil {
		return
	}
	payload := map[string]any{"actualSource": deg.ActualSource}
	if len(deg.UnavailableFields) > 0 {
		payload["unavailableFields"] = deg.UnavailableFields
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return
	}
	fmt.Fprintf(w, ":degradation %s\n", body)
}
