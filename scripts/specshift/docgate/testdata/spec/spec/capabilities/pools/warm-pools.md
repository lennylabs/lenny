---
code: POOL
type: capability
area: pools
summary: Keeps a pool's pods warm.
---

# Warm pools

## Settings

<a id="setting-minwarm"></a>

```lenny-def
kind: setting
name: minWarm
type: integer
default: 2
unit: pods
scopes: [pool]
read-by: [POOL]
summary: The idle pods a pool keeps.
```

<a id="setting-claimholdttl"></a>

```lenny-def
kind: setting
name: pool.claimHoldTTL
type: duration
default: 30
unit: seconds
scopes: [pool]
read-by: [POOL]
summary: How long a claim holds a pod.
```

<a id="setting-recycle-mode"></a>

```lenny-def
kind: setting
name: recycle.mode
type: enum
default: never
values: [never, always]
scopes: [pool]
read-by: [POOL]
summary: Whether a pod serves a second session.
```

<a id="limit-maxpodsperpool"></a>

```lenny-def
kind: limit
name: maxPodsPerPool
value: 10000
unit: pods
summary: The most pods one pool holds.
```

## Errors, metrics, and events

<a id="error-warm-pool-exhausted"></a>

```lenny-def
kind: error
name: WARM_POOL_EXHAUSTED
status: 503
summary: No pod could be claimed.
```

<a id="metric-lenny-warmpool-idle-pods"></a>

```lenny-def
kind: metric
name: lenny_warmpool_idle_pods
type: gauge
labels: [pool]
emitter: [POOL]
summary: The idle pods of a pool.
```

<a id="metric-lenny-warmpool-claim-duration-seconds"></a>

```lenny-def
kind: metric
name: lenny_warmpool_claim_duration_seconds
type: histogram
unit: seconds
labels: [pool]
emitter: [POOL]
summary: How long a claim takes.
```

<a id="alert-warmpoollow"></a>

```lenny-def
kind: alert
name: WarmPoolLow
expr: lenny_warmpool_idle_pods < 1
severity: warning
summary: A pool has almost no idle pods.
```

<a id="label-lenny-dev-pool"></a>

```lenny-def
kind: label
key: lenny.dev/pool
carriers: [agent pod]
values: the pool's name
mutability: required; immutable
writers: [POOL]
readers: [POOL]
summary: The pool a pod belongs to.
```

<a id="event-dev-lenny-pool-drained"></a>

```lenny-def
kind: event
type: dev.lenny.pool_drained
emitter: [POOL]
payload: The pool's name.
summary: A pool finished draining.
```
