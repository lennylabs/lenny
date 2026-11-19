// SPDX-License-Identifier: MIT

// Package sessionevents is the gateway's session event bus, which the
// stream proxy owns. It backs the §15.1 `GET /v1/sessions/{id}/events` SSE
// stream: session activity (message delivered, response produced, state
// transition) is published to the bus and relayed to every subscribed
// client. Each event carries a monotonic per-session sequence so a
// reconnecting client can resume with a cursor (the §15.1
// streaming-reconnect contract).
//
// In single-replica deployments the Bus uses its in-memory state alone. In
// multi-replica deployments the Bus pairs with the §4.4 / §12.4
// (lenny:events: relay row) RedisRelay (see redisrelay.go): every Publish
// fans out to Redis Streams via `XADD`, and every Subscribe pulls the
// cross-replica history via `XRANGE` so a client that reconnects to a
// different replica sees the prior events. The per-session cursors survive
// a replica restart through the durable last_seq hooks.
//
// The Bus serves the seam interface's event operations, GWSEAM-Publish,
// GWSEAM-Subscribe, GWSEAM-OldestRetainedSeq, GWSEAM-Broadcast, and
// GWSEAM-StreamStats, through which the session core and the other seams
// reach it.
//
// spec: GWSTREAM, STREAM
package sessionevents
