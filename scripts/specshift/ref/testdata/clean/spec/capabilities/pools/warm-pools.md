---
code: POOL
type: capability
area: runtimes-and-pools
summary: >-
  Warm pools of agent pods.
aliases: [WPOOL]
retired:
  POOL-R3: CLAIM-R1
  POOL-R4: removed (the execution modes left the design)
---

# Warm pools

## Terms {#terms}

| Term | Definition |
| :-- | :-- |
| <a id="pool"></a>Pool | A warmable deployment target. |
| <a id="reserved-hold"></a>Reserved hold | The period a recycled pod waits for its tenant ([CLAIM-R1](pod-claims.md#claim-r1)). |

## Overview {#overview}

A pool keeps pods warm. The claim mechanism is [Pod claims](pod-claims.md), and the claim operation is [`KPCLAIM-create`](../../interfaces/kubernetes/podclaim.md#kpclaim-create).

## Requirements {#requirements}

<a id="pool-r1"></a>**POOL-R1. A pool names one runtime.** Text.

<a id="pool-r2"></a>**POOL-R2. A pool keeps its minimum warm.** See [POOL-R1](#pool-r1) and the [setting](#setting-pool-minwarm).

<a id="pool-r3"></a>**POOL-R3. Retired.** Its successor is [CLAIM-R1](pod-claims.md#claim-r1).

## Settings {#settings}

<a id="setting-pool-minwarm"></a>

```lenny-def
kind: setting
name: pool.minWarm
type: integer
summary: The number of idle pods a pool keeps.
```

An example of the block format, which declares nothing:

````markdown
```lenny-def
kind: setting
name: example.notRegistered
```
````

The numbered section that held this content is [§4.6](../../04_system-components.md#46-pod-lifecycle-controllers).
