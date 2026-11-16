---
code: SLOT
type: capability
area: sessions
status: partial
summary: >-
  Sessions that share an agent pod each run in their own slot. This page defines the slot, the
  shared pod and the acknowledgment it requires, the dead and leaked slot, and the rule that slots
  on one pod never serve two tenants. The spike migrated only the parts the pod claimer needs.
owns: [objects, requirements, settings, metrics]
depends-on: [APOD, CLAIM, SLOTOPS, SCRUB, KAPOD]
---

# Session isolation

<!-- partial: this spike migrated the terms, settings, and rules the gateway pod claimer reads. The slot release (SI-3), the per-slot resource and credential scope (SI-9, SI-10 scopes), the slot identifier reclaim hold, the disposition table of the scrub model, and the client visibility of shared pods are not migrated; the ledger records each paragraph. -->

## Terms {#terms}

**Owned here.**

| Term | Definition |
| :-- | :-- |
| <a id="slot"></a>Slot | One session's place on an agent pod: one slot container, with its own runtime process, volumes, and credentials, serving at most one session at a time. |
| <a id="slot-identifier"></a>Slot identifier | The identifier the gateway mints for a session's slot at claim. The adapter binds it to the lowest slot index whose slot is live and free. |
| <a id="shared-pod"></a>Shared pod | An agent pod that can serve more than one session at the same time: a pod whose slot count is above 1. |
| <a id="dead-slot"></a>Dead slot | A slot that can serve no session for the rest of its pod's life (SLOT-R2). |
| <a id="leaked-slot"></a>Leaked slot | A slot whose cleanup was not acknowledged clean, and which stays counted in its pod's occupancy until the pod terminates (SLOT-R4). |

**Borrowed.**

| Term | Gloss |
| :-- | :-- |
| Agent pod | A Kubernetes pod that serves sessions for one pod set of one pool ([APOD](../runtimes-and-pools/agent-pod-lifecycle.md#agent-pod)). |
| Slot count | The number of slot containers rendered into a pod ([APOD](../runtimes-and-pools/agent-pod-lifecycle.md#slot-count)). |
| Tenant pin | The binding of an agent pod to one tenant for the rest of its life ([CLAIM](../runtimes-and-pools/pod-claims.md#tenant-pin)). |
| Slot occupancy | The count of a pod's slots that are held or unusable ([SLOTOPS](slot-assignment-and-failure.md#slot-occupancy)). |

## Overview {#overview}

Every agent pod has one slot container per slot, including a pod with one slot, so one implementation serves both. A pool runs pods with several slots only when the deployer acknowledges what the slots of one pod still share. A slot that fails in a way the adapter cannot recover from stays unusable until its pod ends, and a slot whose cleanup was not confirmed stays counted, so the gateway never assigns a session onto a slot whose resources may still be held.

## Requirements {#requirements}

<a id="slot-r1"></a>**SLOT-R1. A shared pod requires the deployer's acknowledgment.** A pool runs with [`sessionPolicy.maxConcurrentSessionsPerPod`](#setting-sessionpolicy-maxconcurrentsessionsperpod) above 1 only with [`sessionPolicy.acknowledgeSharedPod: true`](#setting-sessionpolicy-acknowledgesharedpod), set on the pool by the deployer and never per session. Pool admission refuses any other such pool with `422 INVALID_POOL_CONFIGURATION`, and the error lists what the slots of one pod still share: the network namespace with its side channels, ports, DNS cache, and timing, with `CAP_NET_RAW` dropped; the IPC namespace; the sandbox kernel under gVisor and Kata; the adapter and `netagent`; pod-wide eviction and failure; and contention for CPU, memory bandwidth, disk, and network. A pool whose value is 1 needs no acknowledgment. <!-- src: §5.2 "Deployer acknowledgment (concurrent sessions)."; PL-9; SI-10 "The acknowledgment that remains (D20, D21)." --> <!-- unresolved: SI-10 and PL-9 word the shared list differently ("memory bandwidth" against "memory"; PL-9 adds the fallback's refusal of `egress.allowedHosts` and `egressLockHosts`); the SI-10 list is used. -->

<a id="slot-r2"></a>**SLOT-R2. A dead slot stays dead for its pod's life.** A slot whose container ends, whose supervisor link fails, whose broker misses a deadline, whose reply the adapter refuses, or whose release leaves a brokered volume not empty is dead for the rest of its pod's life. The adapter reports the dead slot, its session fails with `runtime_crash` and follows its retry policy, and the gateway assigns no session to it and counts it in the pod's occupancy ([SLOTOPS-R1](slot-assignment-and-failure.md#slotops-r1)). A pod with one slot retires when its slot dies. A slot that fails before the pod reports READY fails the pod. <!-- src: SIR-15; SI-4 "What follows." -->

<a id="slot-r3"></a>**SLOT-R3. Slots on one pod never serve two tenants.** The slots of one pod serve sessions of one tenant, whatever the pool's isolation profile, because they share the pod's network and IPC namespaces, its adapter, and under gVisor and Kata its kernel. The tenant pin enforces the rule ([CLAIM-R6](../runtimes-and-pools/pod-claims.md#claim-r6)). <!-- src: §5.2 "Cross-tenant prohibition (concurrent sessions)."; PM "Removals" (the prohibition stays as a statement enforced by the tenant pin) -->

<a id="slot-r4"></a>**SLOT-R4. A leaked slot stays counted until its pod ends.** A slot whose cleanup was not acknowledged clean is leaked. It stays counted in its pod's slot occupancy until the pod terminates, so the gateway does not assign a session that would conflict with the leaked slot's unreleased resources, and it counts toward the pod's replacement trigger for as long as it stays leaked ([APOD-R11](../runtimes-and-pools/agent-pod-lifecycle.md#apod-r11)). The adapter reports its leaked slots in its health response and in [`lenny_adapter_leaked_slots`](#metric-lenny-adapter-leaked-slots). <!-- src: §6.2 "`leaked` slot semantics." --> <!-- unresolved: SI-4 adds the dead slot "as a `leaked` slot does today" and does not say whether the leaked state, its report, or the disposition table survive the per-slot release of SI-3. -->

Counting leaks for as long as they last, rather than within the rolling window that counts failed slots, keeps a pod that leaks slowly from aging each leak out of the count.

## Settings {#settings}

<a id="setting-sessionpolicy-maxconcurrentsessionsperpod"></a>

```lenny-def
kind: setting
name: sessionPolicy.maxConcurrentSessionsPerPod
type: integer
default: 1
range: ">= 1; above 1 requires sessionPolicy.acknowledgeSharedPod"
scopes: [pool]
read-by: [WPC, GWCLAIM, PSC]
summary: The number of sessions one pod serves at the same time, and the number of slot containers each pod created from the pool renders (SLOT-R1).
```

<a id="setting-sessionpolicy-acknowledgesharedpod"></a>

```lenny-def
kind: setting
name: sessionPolicy.acknowledgeSharedPod
type: boolean
default: false
range: "true required when maxConcurrentSessionsPerPod is above 1"
scopes: [pool]
read-by: [gateway pool admission]
summary: The deployer's acknowledgment that the slots of a shared pod still share some resources (SLOT-R1).
```

## Metrics and alerts {#metrics-and-alerts}

<a id="metric-lenny-adapter-leaked-slots"></a>

```lenny-def
kind: metric
name: lenny_adapter_leaked_slots
type: gauge
labels: [pod_id, pool]
emitter: ADAPTER
summary: The pod's leaked slots (SLOT-R4).
```

## Related capabilities {#related}

- [Slot assignment and failure](slot-assignment-and-failure.md) assigns sessions to slots and handles slot failure.
- [Agent pod lifecycle](../runtimes-and-pools/agent-pod-lifecycle.md) fixes each pod's slot count.
- [Pod claims](../runtimes-and-pools/pod-claims.md) pins each shared pod to one tenant.
