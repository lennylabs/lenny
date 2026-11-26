# Revocation cache rehydration on event-bus reconnect

Status: `In progress`, revision 5, 2026-11-26.

## Problem

§13.3 (Credential Flow) states two different guarantees for a token revocation that a gateway replica has not received, and the gateway implements neither as written.

- **Authoritative durability for revocation.** states that the in-memory revocation cache and the event-bus propagation are latency optimizations, and that correctness survives the loss of either "because every validation can fall back to `issued_tokens.revoked_at`".
- **Token rotation and revocation — no grace period.** states that a replica reads `revoked_at` from Postgres only on a cache miss for a recently issued token, immediately after the replica starts or after its event-bus subscription reconnects and before rehydration completes, behind a per-call TTL cache. The startup case is already covered by the staleness gate, which refuses every token until the first rehydration succeeds.

The gateway reads no `issued_tokens` row on validation:

- `pkg/auth/authmw/auth.go`: the middleware reads `RevocationFreshness.Stale()` and answers `503 token_validation_unavailable` when the set is stale, then reads `Revocations.IsRevoked` against the in-memory set. Nothing else consults the revocation state of a gateway-issued token.
- `pkg/credential/revocation/revocation.go`: `Cache.Stale` reports the set stale when no rehydration has succeeded, or when the last success is older than `defaultMaxStaleness` (90 seconds).
- `cmd/lenny-gateway/billing_security_workers.go`: the gateway rehydrates the cache once at startup and then every 30 seconds on a ticker. Nothing else triggers a rehydration.
- `pkg/credential/revocation/propagator/propagator.go`: the package comment states that a dropped revocation "is reconciled by the next index rehydration", and `Run` applies each `token.revoked` message to the local cache.

The gap is a lost `token.revoked` event. Redis pub/sub is at-most-once, so a message published while a replica's subscription is disconnected never reaches that replica. The replica then accepts the revoked token until the next 30-second rehydration, while the text says either that it reads Postgres on every validation or that it reads Postgres after a reconnect. Change 0169 (a concurrency model of the token rules, [decision 14](../archive/2026-11-23-0169-token-concurrency-model/decisions.md)) recorded the difference as gap G1 of its model, and property C4 holds for the code rather than for the text.

## Scope

The change covers:

- §13.3 **Authoritative durability for revocation.** and **Token rotation and revocation — no grace period.**, which state the cache, the rehydration on reconnect, the 30-second periodic rehydration, and the 90-second staleness gate, and drop the per-request Postgres read.
- The `TokenRevocationPropagationLag` row of §16.5, whose text says that peer replicas "are falling back to the Postgres check".
- The `propagation_mode` field of the `token.revoked` event in §16.7, whose text says that peer replicas "will fall back to the Postgres check".
- `pkg/storage/pubsub`, which gains a reconnect signal, `pkg/credential/revocation/propagator`, which triggers a rehydration on it, and the gateway wiring in `cmd/lenny-gateway`.
- `pkg/alerting/rules` (the alert's description) and the generated `docs/alerting/rules.yaml`.

It leaves out:

- The revocation set of `lenny-ops`, whose reads §25 states separately. `cmd/lenny-ops/revocation.go` runs `propagator.Run` without a rehydration hook and keeps its own 30-second read.
- The §10.3 mTLS deny-list propagator and the §4.9 credential deny-list propagator, which use the same bus. They receive the reconnect signal only if a later change wires it.
- The 30-second interval and the 90-second window, which keep their values. §13.3 states both as the bound.

## Terms

| Term | Meaning |
| :-- | :-- |
| Revocation cache | The per-replica in-memory set of revoked `jti` values in `pkg/credential/revocation.Cache`, which the auth middleware reads on every validation. |
| Rehydration | A read of every `issued_tokens` row with `revoked_at` set and `exp` inside the trailing hour, which replaces the revocation cache (`Cache.Rehydrate`). |
| Event bus | The Redis pub/sub channel `token:revocations` on which a replica publishes each `jti` it revokes (`pkg/storage/pubsub.Bus`, `propagator.Channel`). |
| Subscription confirmation | A `*redis.Subscription` of kind `subscribe` for the channel, which go-redis delivers when the subscription is first established and again after it re-establishes a dropped connection. |
| Reconnect | A subscription confirmation after the first one. |
| Reconnect spacing | The minimum interval between the starts of two rehydrations that subscription confirmations trigger (decision 3). |
| Staleness gate | The refusal with `503 token_validation_unavailable` when the last successful rehydration is older than 90 seconds, or when none has succeeded (`Cache.Stale`). |

## High-level design

A replica keeps validating tokens against its in-memory revocation cache and reads no `issued_tokens` row per request. When its event-bus subscription is confirmed, at start and after each reconnect, it rehydrates the cache at once rather than at the next tick, so a revocation published while the subscription was down, or before it was first established, reaches the replica as soon as the subscription is up. Two such rehydrations start at least the reconnect spacing apart, 5 seconds by default, so a flapping connection does not turn into a continuous read. The periodic rehydration every 30 seconds stays and reconciles any other lost event. A replica whose last successful rehydration is older than 90 seconds refuses validation, as it does today.

§13.3 states the bound: a lost `token.revoked` event is reconciled by the next rehydration, within 30 seconds, and a replica refuses validation after 90 seconds without a successful rehydration.

## Requirements

None by identifier. §13.3 is content of the placeholder page TOKX.

## Current state

The state on `main` at `104cdf4fa`:

- `pkg/storage/pubsub/bus.go`, `Subscribe`: calls `client.Subscribe` and ranges over `sub.Channel()`, which delivers `*redis.Message` values only. go-redis v9.19.0 re-establishes a dropped subscription inside the channel's goroutine and swallows the `*redis.Subscription` confirmation that follows, so the bus exposes no reconnect signal. go-redis drops a message whose delivery to a full channel exceeds the channel's send timeout and reports it only through its logger, so the bus exposes no dropped-message signal either.
- `pkg/credential/revocation/propagator/propagator.go`, `Run`: subscribes with a handler that calls `Cache.Revoke` and has no other hook.
- `cmd/lenny-gateway/billing_security_workers.go`: starts `revProp.Run` and the rehydration loop as separate goroutines; the loop owns the `TenantLister` and the `issuedtokenstore` source.
- `pkg/credential/revocation/revocation.go`: `Rehydrate` replaces the set and records the success time that `Stale` reads; `defaultMaxStaleness` is 90 seconds and `WithMaxStaleness` overrides it.

## Detailed design

### RR-1 A reconnect signal on the bus

`Bus.Subscribe` reads `sub.ChannelWithSubscriptions()` in place of `sub.Channel()`. On every `*redis.Subscription` of kind `subscribe` for the channel, the first one included (decision 2), the bus calls a callback the caller passes, for example through a variadic option `pubsub.OnSubscribed(func())`. The first confirmation can arrive after the startup rehydration began, as it does for a replica that starts while Redis is unreachable, and the callback then applies a revocation published in between. A `*redis.Message` reaches the handler as now. The callback runs on the loop goroutine, as the handler does, so a callback that blocks hands its work off. A nil `Bus` keeps its no-op behavior and never calls the callback. go-redis offers no signal for a dropped message, so the bus reports none, and the periodic rehydration covers that loss.

### RR-2 Rehydration on reconnect

`propagator.Run` accepts the rehydration as a callback, and passes it to `Bus.Subscribe` as the reconnect callback. The gateway wires a callback that signals the rehydration loop through a buffered channel of one (`revocation.Trigger`), so a reconnect runs one rehydration at once and a burst of reconnects runs at most one extra. The loop is `revocation.Cache.RunRehydration` in `pkg/credential/revocation`, which `billing_security_workers.go` starts, so the component and chaos tests drive the same loop. The loop rehydrates on the tick or on the signal, whichever comes first, and gives each read a deadline of one interval. A signal that arrives less than the reconnect spacing after the start of the previous signal-triggered rehydration waits until the spacing has passed, and a tick in the meantime runs as usual (decision 3). The spacing defaults to 5 seconds and is set by a gateway flag, which the chart value `auth.revocationReconnectSpacing` renders (decision 4); no metric counts the signal-triggered rehydrations. A failed rehydration is logged as a failed tick is, and leaves the staleness clock where the last success set it. The staleness clock counts from the instant a successful read began, so a slow read does not extend the 90-second window.

### RR-3 The bound in §13.3

**Authoritative durability for revocation.** states that the in-memory cache and the event bus are latency optimizations, that a replica rehydrates every 30 seconds and, no sooner than the reconnect spacing after the previous such rehydration, when its subscription is confirmed at start or after a reconnect, that a lost `token.revoked` event is therefore reconciled within 30 seconds, and that a replica whose last successful rehydration is older than 90 seconds refuses validation with `503 token_validation_unavailable`. It drops the sentence that every validation can fall back to `issued_tokens.revoked_at`. **Token rotation and revocation — no grace period.** drops the cache-miss read and its TTL cache, and states that a replica that has not received the event accepts the old token until the event arrives or the next rehydration runs, within the bound above. The `TokenRevocationPropagationLag` row of §16.5 replaces "falling back to the Postgres check" with the reconciliation by rehydration.

## Limits, and alternatives rejected

- **A window of up to 30 seconds.** A replica that misses a `token.revoked` event while its subscription stays connected, such as a message go-redis drops on a full channel, accepts the revoked token until the next periodic rehydration.
- **The reconnect spacing.** A reconnect inside the spacing of the previous one delays its rehydration by up to the spacing, 5 seconds by default, which stays inside the 30-second bound.
- **A per-token Postgres read with a short TTL cache (default 5 seconds).** It would close the window to the TTL at the cost of a database read per token per TTL on every replica. The owner did not choose it (decision 1).
- **A specification-only edit describing the current code.** It would state the 30-second window without the rehydration on reconnect. The owner did not choose it (decision 1).

## Specification and non-specification changes

- `spec/13_security-model.md`, §13.3: **Authoritative durability for revocation.** and **Token rotation and revocation — no grace period.**
- `spec/16_observability.md`, §16.5: the `TokenRevocationPropagationLag` row; §16.7: the `propagation_mode` field of `token.revoked`.
- `pkg/storage/pubsub`: the reconnect callback of `Subscribe`.
- `pkg/credential/revocation/propagator`: `Run` with the rehydration callback, and the package comment.
- `cmd/lenny-gateway`: the rehydration loop's signal channel, the wiring of `revProp.Run`, and the reconnect-spacing flag.
- `charts/lenny`: the value `auth.revocationReconnectSpacing`, default `5s`, its entry in `values.schema.json`, and its rendering as `--revocation-reconnect-spacing` in `templates/gateway-deployment.yaml`; `pkg/chart/values` and the embedded manifests regenerated (decision 4).
- `pkg/alerting/rules`: the description of `TokenRevocationPropagationLag`, and `docs/alerting/rules.yaml` regenerated with `make generate`.

## Documentation

- `docs/alerting/rules.yaml` (operator): regenerated; the alert's description names the rehydration in place of the Postgres check.
- `docs/reference/configuration.md` (operator): the security configuration lists the chart value `auth.revocationReconnectSpacing` with its default and the flag it sets.
- `docs/operator-guide/security.md` (operator): the bearer-token paragraph states that a replica that misses a revocation event applies it at its next rehydration, within 30 seconds, rehydrates at once when its event-bus subscription is confirmed at start or after a reconnect, no sooner than the reconnect spacing after the previous such rehydration, and refuses validation after 90 seconds without a successful rehydration. It names the flag that sets the reconnect spacing, the chart value `auth.revocationReconnectSpacing` that renders it, and its 5-second default.

## Tests

- Unit: `pkg/storage/pubsub` (the callback runs on the first subscription confirmation and on each later one; a nil bus never calls it), against a Redis the test restarts or a fake that delivers `*redis.Subscription` values; `pkg/credential/revocation/propagator` (a reconnect calls the rehydration callback); `pkg/credential/revocation` (the signal runs one rehydration before the next tick, a burst of signals runs at most one extra, a signal inside the reconnect spacing waits for it while a tick still runs, a failed read keeps the staleness clock, and the clock counts from the read's start).
- Component: a replica whose Redis connection drops while a peer publishes a revocation rejects the revoked token after the subscription reconnects, without waiting for the periodic tick.
- Chaos: `tests/chaos` (a Redis restart during a revocation: the replica that missed the event rejects the token after it reconnects, and a replica whose rehydration keeps failing answers `503 token_validation_unavailable` after 90 seconds).
- Security: a revoked token is refused on every replica within the bound §13.3 states.
- Chart: `charts/lenny/tests/gateway-deployment_test.yaml` (the gateway renders `--revocation-reconnect-spacing` with the default `5s` and with a value the test sets, and the schema refuses a value that is not a duration).
