---
id: APICONV-I25
status: planned
kind: build-gap
severity: low
requirements: []
change: 0183.S2
---

# lenny-ops accepts an idempotency key of any length, and a key too long for the index answers 500

## Specification

[§25.4](../../spec/25_agent-operability.md#254-the-lenny-ops-service) (The `lenny-ops` Service), **Idempotency**: the key is a caller-generated UUID, and §25.4 states no length limit. [§11.5](../../spec/11_policy-and-controls.md#115-idempotency) **Mechanism.** limits the gateway's key to 128 characters.

## Evidence

- `serve` in `pkg/ops/opsidem/opsidem.go` trims the header and checks only that it is not empty.
- The key is part of the primary key `(key, caller_id)` of `ops_idempotency_keys`. A key longer than a Postgres btree index entry allows, about 2.7 kB, fails the `INSERT` in `Claim` (`pkg/ops/opsidem/pgstore/pgstore.go`) with a server error, which `classifyErr` passes through and `serve` answers with `500 INTERNAL`. This follows from the index entry limit and has not been reproduced.
- Found by the in-depth review of `pkg/ops/opsidem` against the gateway's idempotency design that decision 19 of change 0178 asked for, 2026-11-27 (its comparison table, row "Key validation"; the review listed it as a difference and not as a numbered finding), and checked against the code of `main` at `c173b3790` on the same day.

## Gap

A malformed key produces a server error where a client error is due, and §25.4 and §11.5 state different key rules for the same header name.

## Suggested resolution

Limit the key to 128 characters in §25.4, as §11.5 does, and answer a longer key with `400 INVALID_IDEMPOTENCY_KEY` before the claim. Add a unit test for a key at and over the limit.

The owner answered IP-21 on the M3 readiness decision page on 2026-11-27 with a redesign of the `lenny-ops` idempotency records as a change of its own (decision 20 of change 0178), which is change [0183](../../changes/0183-lenny-ops-idempotency/change.md). Step 0183.S2 of that change closes this issue. The change is `Draft`, and an answer to its open decisions can move the issue to another of its steps.
