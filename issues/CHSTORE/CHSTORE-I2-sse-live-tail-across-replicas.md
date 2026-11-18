---
id: CHSTORE-I2
status: open
kind: build-gap
severity: medium
requirements: []
---

# The event stream has no live tail across replicas

## Specification

§28.5.7 (Gateway-to-store), `CH-EVENTRELAY` **Messages.**: "A reader consumes the stream with `XRANGE` for history and `XREAD BLOCK` for live delivery".

## Evidence

- Claim register row (`tests/claim-map.json`, retired by 0116.S5): claim "SSE live tail across replicas", status `UNWIRED`, anchor `#2857-gateway-to-store`, deferral `R19`.
- Surface recorded on the row: "`pkg/gateway/session/sessionevents/redisrelay.go:157` has no caller".
- `RedisRelay.LiveFromCursor` in `pkg/gateway/session/sessionevents/redisrelay.go` has no caller (triage, 2026-11-17).

## Gap

A client attached to a replica that does not hold the session receives the stream's history and no live events.

## Suggested resolution

The row deferred the work to step R19, "Cross-replica message forwarding", of `gateway-runtime-comms-remediation.md`.

0138 keeps the mechanism and renames its references to the event stream and `Last-Event-ID`; 0117.S6 moves the package. No member step wires the live tail.
