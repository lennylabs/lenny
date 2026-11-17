---
code: POOL
type: capability
area: pools
summary: >-
  Keeps a pool's pods warm. This page defines the pool and its settings.
---

# Warm pools

## Terms

| Term | Definition |
| :-- | :-- |
| <a id="warm-pool"></a>Warm pool | A set of pods kept ready. Each pod waits for a claim. |
| <a id="pod-set"></a>Pod set | The pods of one pool that share a pod hash. |

## Requirements

<a id="pool-r1"></a>**POOL-R1. A pool keeps minWarm idle pods.** The controller creates pods until the pool holds minWarm idle pods.

<a id="pool-r2"></a>**POOL-R2. A pool refuses a bad name.** Text.

<a id="pool-r10"></a>**POOL-R10. Requirements sort by number.** Text.

## Model

<a id="state-pod-phase"></a>

```lenny-def
kind: state
name: pod phase
object: agent pod
states: [warming, idle, active, draining]
initial: warming
terminal: [draining]
transitions:
  - {from: warming, to: idle, on: ready}
  - {from: [idle], to: active, rule: POOL-R1}
  - {from: [idle, active], to: draining}
summary: The phase of an agent pod.
```

## Settings

<a id="setting-minwarm"></a>

```lenny-def
kind: setting
name: minWarm
type: integer
default: 0
range: [0, 10000]
scopes: [pool]
defaults-by-tier: {tier1: 5, tier2: 50, tier10: 500}
read-by: [WPC, GWCLAIM]
summary: The number of idle pods a pool keeps.
```

<a id="setting-recycle-mode"></a>

```lenny-def
kind: setting
name: recycle.mode
type: enum
default: never
values: [never, always]
scopes: [pool]
read-by: WPC
summary: Whether a pod serves a second session.
```

<a id="limit-maxpodsperpool"></a>

```lenny-def
kind: limit
name: maxPodsPerPool
value: 10000
unit: pods
scopes: [pool]
setting: minWarm
enforced-by: [WPC]
summary: The most pods one pool holds.
```

## Errors

<a id="error-warm-pool-exhausted"></a>

```lenny-def
kind: error
name: WARM_POOL_EXHAUSTED
status: 503
category: TRANSIENT
retryable: true
details: {reason: no_idle_pods}
returned-by: [CAPISES-createSession]
rule: POOL-R1
summary: No pod could be claimed.
```

<a id="admission-refusal-pool-name-invalid"></a>

```lenny-def
kind: admission-refusal
name: pool name invalid
message: "pool name must be a valid label value"
resource: PodSet
operations: [CREATE]
webhook: lenny-podset-guard
rule: POOL-R2
summary: The webhook refused a pool with a bad name.
```

<a id="condition-reason-podsetkeycollision"></a>

```lenny-def
kind: condition-reason
name: PodSetKeyCollision
resource: PodSet
condition: Degraded
rule: POOL-R1
summary: Two pod-set keys gave one name.
```

## Metrics and alerts

<a id="metric-lenny-warmpool-idle-pods"></a>

```lenny-def
kind: metric
name: lenny_warmpool_idle_pods
type: gauge
labels: [pool, pod_set]
emitter: [WPC, PSC]
summary: The idle pods of a pod set.
```

<a id="alert-warmpoollow"></a>

```lenny-def
kind: alert
name: WarmPoolLow
expr: lenny_warmpool_idle_pods < 0.25 * minWarm
severity: warning
for: 5m
detects: POOL-R1
runbook: warm-pool-low
summary: A pod set's idle pods dropped below a quarter of minWarm.
```
