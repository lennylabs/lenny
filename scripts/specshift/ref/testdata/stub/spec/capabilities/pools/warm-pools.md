---
code: POOL
type: capability
area: runtimes-and-pools
summary: >-
  Warm pools of agent pods.
aliases: [WPOOL]
retired:
  POOL-R3: POOL-R2
---

# Warm pools

## Terms {#terms}

| Term | Definition |
| :-- | :-- |
| <a id="reserved-hold"></a>Reserved hold | The period a recycled pod waits for its tenant. |

## Requirements {#requirements}

<a id="pool-r1"></a>**POOL-R1. A pool names one runtime.** Text.

<a id="pool-r2"></a>**POOL-R2. A pool keeps its minimum warm.** Text.

<a id="pool-r3"></a>**POOL-R3. Retired.** Its successor is [POOL-R2](#pool-r2).

## Settings {#settings}

<a id="setting-pool-minwarm"></a>

```lenny-def
kind: setting
name: pool.minWarm
type: integer
summary: The number of idle pods a pool keeps.
```
