---
id: CHSTORE-I3
status: open
kind: test-gap
severity: low
requirements: []
legacy: T-BED.35
---

# The pub/sub load scenarios exercise a scenario-local broker instead of the product event bus

## Specification

§12.6 (Interface Design), **EventBus**, which the CHSTORE page holds in part: "Abstracts control-plane event distribution for delegation tree signals and session lifecycle events. The v1 implementation (`RedisEventBus`) publishes to Redis pub/sub channels under the tenant-prefix convention".

The `pubsub_fanout` scenario states its invariant as "every subscriber sees every published event in order, exactly once".

## Evidence

- `tests/load_local/scenarios/pubsub_fanout/scenario.go` states in its package comment that "pkg/pubsub does not yet exist in the tree; this scenario uses a scenario-local broker", and declares `type broker struct`.
- `tests/load_local/scenarios/pubsub_slow_consumer/scenario.go` declares its own `type broker struct` in the same way.
- The product packages exist: `pkg/storage/pubsub/bus.go` (`Bus.Publish`, `Bus.Subscribe`) and `pkg/storage/eventbus/eventbus.go` (`type EventBus interface`, `RedisEventBus`). No file under `tests/load_local/` imports either package.
- Recorded on branch `theme-t-bed` as T-BED.35, and checked against `main` at `092ef92f6` on 2026-11-19.

## Gap

The load_local tier's fan-out and slow-consumer scenarios measure an in-file broker, so ordering, exactly-once delivery, and slow-consumer behavior of the product event bus have no load-tier coverage. A regression in `pkg/storage/pubsub` or `pkg/storage/eventbus` passes both scenarios.

## Suggested resolution

Rewrite both scenarios against `pkg/storage/eventbus` (or `pkg/storage/pubsub` where the scenario tests the raw bus), backed by miniredis or the compose Redis, and remove the scenario-local brokers and the stale package comment. Where the product bus documents a weaker guarantee than the scenario's invariant, such as at-most-once delivery on Redis pub/sub, assert the documented guarantee and state the difference in the scenario.
