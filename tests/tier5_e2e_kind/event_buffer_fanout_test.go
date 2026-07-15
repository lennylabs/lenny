// SPDX-License-Identifier: MIT

//go:build e2e_kind

// Tier-5 e2e Kind coverage for the §25.3 cross-replica event-buffer
// fan-out and eventKey deduplication path.
//
// This test is currently a placeholder. §25.3 describes lenny-ops
// falling back to the per-replica gateway event buffer during a Redis
// outage: it discovers every gateway pod IP via the headless Service
// (lenny-gateway-pods), polls each replica's GET
// /v1/admin/events/buffer individually, and deduplicates the merged
// result by eventKey rather than a content hash, so two distinct
// same-second alert_fired events from two different replicas are kept
// as two events rather than collapsed into one.
//
// The consumer that performs this merge does not exist yet.
// pkg/ops/gateway.Client.FanOutGet dials every replica the headless
// Service returns and hands back each replica's raw response body, but
// nothing merges those bodies or deduplicates them by eventKey.
// pkg/ops/events.Service (the lenny-ops read surface behind GET
// /v1/admin/events and /v1/admin/events/stream) never calls FanOutGet
// at all: its own package comment states that "the Redis stream source
// and the Redis-down / gateway-down degradation matrix" remain
// unimplemented, and its SourceHealth-driven degradation envelope only
// relabels which source a response is reported as coming from — it
// still always serves from the local in-memory ring buffer, which only
// ever holds lenny-ops-originated events, never gateway-originated
// ones. So there is currently no code path in which lenny-ops actually
// discovers gateway pod IPs, polls each one's buffer, and merges the
// results by eventKey; "lenny-ops falls back to the buffer" during a
// Redis outage is a documented behavior with no implementation to
// drive from a real two-replica gateway and a real Redis outage.
//
// Building the read-side Redis source and the gateway-buffer fan-out
// fallback it degrades to is the same product change tracked against
// the §25.5 operational event stream (pkg/ops/events.Service never
// consuming Redis or the gateway buffer fallback). Until that consumer
// exists, the assertions below cannot run against a real cluster, so
// the test skips rather than assert a path the current binaries cannot
// reach.
package tier5_e2e_kind_test

import "testing"

// spec: §25.3 (spec/25_agent-operability.md, Gateway Event Buffer,
// Behavior) "The buffer is per-gateway-replica (not shared). When
// `lenny-ops` uses the buffer fallback, it discovers all gateway pod
// IPs via the headless Service `lenny-gateway-pods` (see below) and
// polls each replica individually. **Deduplication across replicas
// uses `eventKey`**, not a content hash — this avoids the collision
// class where two distinct events happen to have identical (type,
// timestamp, payload) across replicas (e.g., two replicas both
// emitting `alert_fired` for the same alert in the same second, or two
// replicas both reporting a `credential_rotated` for a broadcast
// rotation)." + "**Headless Service for buffer polling.** ... `lenny-
// ops` uses DNS SRV lookup (`lenny-gateway-pods.{namespace}.svc`) to
// discover all gateway pod IPs. This Service is used exclusively for
// the event buffer fallback."
//
// diagnosis: a failure means the §25.3 cross-replica buffer-fallback
// contract is broken on a real two-replica cluster: either lenny-ops
// does not discover both gateway pod IPs via the headless Service
// during a real Redis outage, or the merged result it returns loses a
// distinct same-second alert from one of the two replicas (a false
// content-hash collision), or it fails to collapse a genuine repeat
// delivery of the same eventKey. This test is skipped until the
// underlying consumer exists to reach any of those failure modes; see
// the file comment.
func TestEventBufferFanOutDeduplicatesAcrossReplicasByEventKey(t *testing.T) {
	t.Skip("lenny-ops has no consumer that discovers gateway pod IPs via the headless Service, " +
		"polls each replica's event buffer, and merges the result deduplicated by eventKey; the " +
		"read-side Redis/gateway-buffer fallback this depends on is unbuilt (pkg/ops/events.Service " +
		"still serves only its own local ring buffer), so the buffer-fallback fan-out path cannot be " +
		"exercised against a real two-replica cluster yet")
}
