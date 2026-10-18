# Spec changes: Release ownership for pods, slots, per-pod claims, and credential leases at and after a terminal write

## Design (as the spec must state it)

The specification states three things this proposal changes.

1. **REG-SLOTCOUNT is a per-pod set keyed by session.** The key `lenny:pod:{pod_id}:active_slots` and the register identifier stay. The value is a Redis set with one member per session holding a slot on the pod and one `leaked:<sessionId>` member per leaked slot. A reservation adds the session's member under the cap. A release removes the session's member. The occupancy-zero disposition runs only on the release that removed the last member. §5.2 **Slot assignment atomicity** is the home of these rules (SPEC-1a).
2. **The SandboxClaim writer set names the gateway's delete by a release that ends the occupancy without reuse.** §4.6.3's ownership row is the home of the rule (SPEC-2c). The non-spec **SandboxClaim end-of-occupancy disposition table** derives it path by path from the tree. SPEC-2 **Site classification.** lists the other spec sites SPEC-2 edits or leaves to an owner.
3. **full_revoke returns and deny-lists the credential leases of the sessions it cancels, and releases each cancelled session through the session release.** §11.4 steps 1, 2, 4, and 6 and the **Note** state it (SPEC-3). The non-spec **full_revoke release table** derives it for each pre-revoke session state.

## Edge cases and accepted failure modes

- **Clean release, then leaked release of the same session.** A pre-running reclaim releases clean, and a peer's in-flight `/start` later releases the same slot with a cleanup that was not acknowledged clean. The second release finds no session member and still adds the `leaked` member, so the leaked slot stays counted (SPEC-1a).
- **A reseed drops leaked members and unpersisted reservations (departure).** A rehydration after a Redis restart, or the §10.1.4 whole-pod reset, rebuilds an emptied set from the non-terminal session rows. Those rows carry no leaked slots, and a reservation precedes the row that names the pod: a create reserves its slot before it persists the row, and a resume reserves before its commit names the pod. Both drops predate this proposal (§6.2 **`leaked` slot semantics.** says a leaked slot persists until pod termination, today's reseed already drops it, and today's release reports zero occupancy on a counter a restart emptied). The rebuild that follows a Redis outage drops neither (non-spec **Slot-set outage recovery case table** rows O13 and O14). Their bounds are non-spec **Accepted-departure register** rows A11 and A12.
- **A failed release the recovery rebuild does not replay (departure).** The rebuild that follows a Redis outage replays a release that failed during the outage only in the cases the non-spec **Slot-set outage recovery case table** states, and a rehydration can add a member that no release removes. Its cases and bound are non-spec **Accepted-departure register** row A27.
- **Fast-path enforcement before the rebuild after a Redis outage (departure).** §12.4 rebuilds the slot set before fast-path enforcement resumes (SPEC-1d). A gateway replica that did not observe the outage resumes the fast path when Redis answers it, before a replica that observed the outage has run the rebuild. Its cases and bound are non-spec **Accepted-departure register** row A26.
- **A duplicate release after a Redis restart (departure).** When a session's first release emptied the set and ran the occupancy-zero disposition before a Redis restart, a duplicate release of that session after the restart and before any reservation seeds its own member (SPEC-1a), removes it, and runs the disposition again. The rebuild that follows a Redis outage (non-spec CODE-1 item 8) exposes the same duplicate. Its bound is non-spec **Accepted-departure register** row A13.
- **A release delete before the pod projects `claimed` (departure).** A claim the gateway deletes before the WarmPoolController has projected the acquisition or the `reserved → bound` rebind is projected from the pod's earlier `idle` or `reserved` level (§4.6.1 **Occupancy projection**). This includes the delete after a failed or crashed session on a pool with `maxConcurrentSessions: 1`. Its bound is non-spec **Accepted-departure register** row A15. The gap predates this proposal: the tree deletes on these paths, and nothing implements the §4.6.3 `failed` write that SPEC-2d removes.
- **A repeated reservation for a session already in the set.** The reservation adds nothing and succeeds (SPEC-1a). One session does not hold two slots on one pod.
- **Standing test clusters.** A Redis that holds a string value under `lenny:pod:*:active_slots` from a run before this change answers `WRONGTYPE` to the set scripts. The test-infrastructure preflight in CODE-1 deletes those keys. No production shim exists, because the platform has no deployments.
- **full_revoke of a session bound on a peer replica.** The handling replica publishes the release request, and the peer releases asynchronously. The response counts only the handling replica's local releases, as the §11.4 **Note** already states for its other effects.

## Staged edits

Every table row this proposal edits is one physical line, and it stays one physical line after the edit. Anchors are quoted from the tree at 85c9ab19a, and each matches its target file exactly once.

### SPEC-1 · spec/05_runtime-registry-and-pool-model.md § 5.2; spec/12_storage-architecture.md § 12.4; spec/28_communication-channels.md § 28.3, § 28.6; spec/10_gateway-internals.md § 10.1.4; spec/06_warm-pod-model.md § 6.2; spec/29_communication-scenarios.md § 29.2, § 29.10; spec/04_system-components.md § 4.6.1

**SPEC-1a. §5.2 **Slot assignment atomicity (`maxConcurrentSessions > 1`).**** Replace the text that begins "The gateway uses an atomic Redis `INCR` with a cap check:" and ends "The counter is the intra-pod capacity gate;" with the block below. The rest of the paragraph, from "pod acquisition itself is guarded by the deterministic per-pod `SandboxClaim` CREATE" onward, is unchanged.

```
The gateway keeps a pod's slot occupancy in a Redis set, `lenny:pod:{pod_id}:active_slots`. The set holds one member per session that holds a slot on the pod, named by the session identifier, and one member `leaked:<sessionId>` per slot whose cleanup was not acknowledged clean ([Section 6.2](06_warm-pod-model.md#62-pod-state-machine) **`leaked` slot semantics.**). The set's cardinality is the pod's slot occupancy. A reservation runs a Lua script. When the session's member is already present, the script adds nothing and returns the cardinality, whatever the cardinality is. Otherwise, when the cardinality is below `maxConcurrentSessions`, it adds the member and returns the new cardinality, and when the cardinality has reached `maxConcurrentSessions`, it adds nothing and returns `nil` (slot unavailable). Because the check and the add run in one script, two concurrent assignments cannot both observe the same free slot. A release runs a Lua script keyed by the session identifier. It removes the session's member, and it returns whether it removed the member and the remaining cardinality. A release that finds the pod's `rehydrated` flag absent first rehydrates the set as **Post-recovery rehydration atomicity.** below states, adding its own session's member to the seed, and then removes that member. A release of a slot whose cleanup was not acknowledged clean removes the session's member, if present, and adds a `leaked` member for the session, so a leak reported after a clean release of the same session is still counted. A second leaked release changes nothing. A clean release that finds no member for the session changes nothing, so every path that releases the same reservation can run and, while Redis keeps the set and its `rehydrated` flag, the reservation is returned once. The occupancy-zero disposition (**Recycle lifecycle (`recycle.enabled: true`).** above, and [Section 4.6.3](04_system-components.md#463-crd-field-ownership-and-write-boundaries) for the claim delete) runs only on a release that removed the session's member and left the set empty. The set is the intra-pod capacity gate;
```

**SPEC-1b. §5.2 **Post-recovery rehydration atomicity.**** Make five in-place substitutions in the paragraph.

Replace

```
After a Redis restart, slot counters reset to zero but pods may still have active slots.
```

with

```
After a Redis restart, slot sets are empty but pods may still have active slots.
```

Replace

```
(indicating the counter has not been rehydrated since the last Redis restart)
```

with

```
(indicating the set must be rehydrated)
```

Replace

```
queries `SessionStore.GetActiveSlotsByPod(pod_id)` from Postgres, writes the accurate count to `lenny:pod:{pod_id}:active_slots`, sets the `rehydrated` flag
```

with

```
queries `SessionStore.GetActiveSlotsByPod(pod_id)` from Postgres for the identifiers of the pod's non-terminal sessions, adds those identifiers to `lenny:pod:{pod_id}:active_slots`, keeping the members the set already holds, sets the `rehydrated` flag
```

Replace

```
could both observe `counter=0` and succeed
```

with

```
could both observe an empty set and succeed
```

Replace

```
it persists until the next Redis restart, at which point all flags are naturally cleared and rehydration is triggered again on first access
```

with

```
a Redis restart clears it, the [Section 10.1.4](10_gateway-internals.md#1014-coordinator-loss-detection-and-hold-state) whole-pod reset deletes it, and the gateway deletes it for each pod a non-terminal session names when a Redis outage ends ([Section 12.4](12_storage-architecture.md#124-redis-ha-and-failure-modes)); the pod's next slot reservation or release then rehydrates the set
```

**SPEC-1c. §12.4 Redis HA and Failure Modes, key table.** Replace the row that begins "| `lenny:pod:{pod_id}:active_slots` | Session-mode slot counter |" with

```
| `lenny:pod:{pod_id}:active_slots` | Session-mode slot set | **Pod-scoped** (not tenant-prefixed — intentional, as pod IDs are already globally unique within the cluster). A Redis set with one member per session holding a slot on the pod and one `leaked:<sessionId>` member per leaked slot; its cardinality is the pod's slot occupancy, and the atomic Lua reservation that adds a member only while the cardinality is below `sessionPolicy.maxConcurrentSessions` is the intra-pod capacity gate for session-mode pools ([§5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes), "Slot assignment atomicity"). On Redis restart, the set is lost; the gateway performs **blocking rehydration** from `SessionStore.GetActiveSlotsByPod(pod_id)` before accepting new slot assignments — see [§5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes) ("Post-recovery rehydration atomicity") for the full protocol. The Lua script checks a per-pod `rehydrated` flag (`lenny:pod:{pod_id}:rehydrated`) atomically; if absent, all slot allocation attempts for that pod block until rehydration completes, preventing concurrent requests from observing a stale empty set. During a Redis outage the set has a Postgres fallback — see the failure-behavior table below. |
```

In the row that begins "| `lenny:pod:{pod_id}:rehydrated` |", replace "from Postgres post-Redis-restart. No TTL — naturally cleared on next Redis restart." with "from Postgres. No TTL."

**SPEC-1d. §12.4 failure-behavior table.** In the row that begins "| Session slot counter (`lenny:pod:{pod_id}:active_slots`) |", make two in-place substitutions. Replace "Session slot counter (`lenny:pod:{pod_id}:active_slots`)" with "Session slot set (`lenny:pod:{pod_id}:active_slots`)", and replace "On Redis recovery, the counter is rebuilt" with "On Redis recovery, the slot set is rebuilt". The rest of the row is unchanged.

**SPEC-1e. §28.3 register-entry register, row `REG-SLOTCOUNT`.** Replace the semantics cell

```
An atomic per-pod counter that ceilings concurrent slots
```

with

```
An atomic per-pod set of the sessions holding slots and of the leaked slots, whose cardinality ceilings concurrent slots ([§5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes))
```

**SPEC-1f. §10.1.4 Coordinator-Loss Detection and Hold State, **Whole-pod connection loss when `maxConcurrentSessions > 1`.**** Replace the last sentence

```
The gateway atomically resets the Redis slot counter (`lenny:pod:{pod_id}:active_slots` → 0) and rehydrates it from `SessionStore.GetActiveSlotsByPod(pod_id)` on the replacement pod's first slot allocation after recovery, preventing stale counters from blocking new slot assignments on the replacement pod.
```

with

```
The gateway atomically clears the pod's Redis slot set (`lenny:pod:{pod_id}:active_slots`) and rehydrates it from `SessionStore.GetActiveSlotsByPod(pod_id)` on the replacement pod's first slot allocation after recovery, preventing stale members from blocking new slot assignments on the replacement pod.
```

**SPEC-1g. §6.2 Pod State Machine.** In the concurrent-occupancy fenced diagram, replace the edge label

```
  claimed ──→ claimed               (a session completes or fails — per-slot cleanup runs, Redis-counter
                                     occupancy decremented, occupancy still nonzero)
```

with

```
  claimed ──→ claimed               (a session completes or fails — per-slot cleanup runs, Redis-counter
                                     occupancy reduced, occupancy still nonzero)
```

In the same diagram's `idle ──→ claimed` edge label, replace "Redis counter increment succeeds" with "Redis slot reservation succeeds". In the **Concurrent pod lifecycle** **Partial occupancy.** bullet, replace "the atomic Redis counter ([Section 5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes)) gates assignment" with "the Redis slot set ([Section 5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes)) gates assignment".

**SPEC-1h. §28.6 Exclusivity and concurrency model, **Units carried outside the channel register.**** Replace "`REG-SLOTCOUNT` is an atomic per-pod counter that ceilings concurrent slots, and" with "`REG-SLOTCOUNT` ceilings a pod's concurrent slots, and".

**SPEC-1i. §29.2 Session start, step 8, and §29.10 Co-tenancy on a concurrent-session pod.** The anchors are hard-wrapped; quote them with their line breaks and indentation. In §29.2 step 8, replace

```
   `REG-SLOTCOUNT`, Redis. The gateway reserves the intra-pod slot with a Lua script that checks the
   counter and increments it only when the result would not exceed `maxConcurrentSessions`, returning a
   slot-unavailable result otherwise. The counter is the intra-pod capacity gate and the per-pod claim
```

with

```
   `REG-SLOTCOUNT`, Redis. The gateway reserves the intra-pod slot through the atomic reservation of
   [§5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes)
   **Slot assignment atomicity**, which is the intra-pod capacity gate, and the per-pod claim
```

In the §29.10 bullet that begins "The occupancy and claim primitives.", replace

```
- The occupancy and claim primitives. `REG-SLOTCOUNT` is an atomic per-pod counter that ceilings
  concurrent slots and is the occupancy authority, and `REG-CLAIM` is a cluster-wide per-pod acquisition
```

with

```
- The occupancy and claim primitives. `REG-SLOTCOUNT` ceilings a pod's concurrent slots and is the
  occupancy authority, and `REG-CLAIM` is a cluster-wide per-pod acquisition
```

**SPEC-1j. §4.6.1 **Pod claim mechanism (per-pod occupancy claim):**.** Replace "is gated by the atomic Redis slot counter, with a Postgres fallback" with "is gated by the Redis slot set, with a Postgres fallback".

### SPEC-2 · spec/04_system-components.md § 4.6, § 4.6.1, § 4.6.3, § 4.7, § 4.9; spec/05_runtime-registry-and-pool-model.md § 5.2; spec/06_warm-pod-model.md § 6.2; spec/07_session-lifecycle.md § 7.1, § 7.2; spec/08_recursive-delegation.md § 8.3; spec/15_external-api-surface.md § 15.1; spec/24_lenny-ctl-command-reference.md § 24.11; spec/28_communication-channels.md § 28.3, § 28.5.1, § 28.8; spec/29_communication-scenarios.md § 29.4

SPEC-2 is regenerated from the non-spec **SandboxClaim end-of-occupancy disposition table**, which maps every gateway release path to its claim effect. SPEC-2c states the rule in §4.6.3. An edit that states the predicate uses the one wording "ends the occupancy without reuse", and no edit states the pod's resulting phase, which the §4.6.1 **Occupancy projection** owns.

**Site classification.** Every spec site whose statement of the claim's or the pod's outcome at the end of an occupancy contradicts §4.6.3 once SPEC-2c and SPEC-2d land is listed here once, as an edit or as out of scope with an owner. A site that contradicts it only through a departure that **Edge cases and accepted failure modes** records is not listed.

- Home: the §4.6.3 SandboxClaim ownership row (SPEC-2c).
- Edits: the §4.6 CRD row (SPEC-2a); §4.6.1 **Pod claim mechanism** and **Reserved hold** (SPEC-2b); the §4.6.3 `failed` binding-state bullet (SPEC-2d); §5.2 **Recycle lifecycle** (SPEC-2e); the §6.2 concurrent-occupancy diagram note (SPEC-2f); the §28.3 `REG-CLAIM` writer cell (SPEC-2g); the §6.2 Recycle edges header and recycle-disposition label, **`reserved` hold semantics.**, and **Concurrent pod lifecycle** (SPEC-2h); §28.5.1 `CH-ATTACH` **Degradation.** (SPEC-2i); §29.4 step 17 (SPEC-2j); §29.4 step 12 (SPEC-2k); §7.2 **Mid-resume terminal transitions** step 3 (SPEC-2l); the §4.7 `Shutdown` row (SPEC-2m); the §6.2 `resuming → cancelled` bullet (SPEC-2n); the §7.1 `scrubPolicy` row (SPEC-2o); §4.9 **Pre-Claim Credential Availability Check** (SPEC-2p); §8.3 **Credential availability pre-check at delegation time:** (SPEC-2q); the §15.1 `created` row (SPEC-2r); the §24.11 `force-terminate` row (SPEC-2s); the §5.2 `sessionPolicy` common-configurations row `Concurrent` (SPEC-2t); the §28.8 `CH-ATTACH` matrix row (SPEC-2u).
- Out of scope, each with its owner:
  - §6.2 **Pre-attached failure retry policy:**, "The pod is marked `failed` and released back to the pool (or terminated if unhealthy)." Owner: draft proposal 0085, because the problem statement excludes the pre-attached seam disposition. The code deletes the claim on that path (table row D15) and never writes `failed`, so 0085 must re-baseline this sentence on §4.6.3 (summary **Impacts on other proposals**).

**SPEC-2a. §4.6 CRD table, row `SandboxClaim`.** Make two in-place substitutions in the row.

Replace

```
or when the pod terminates; its spec carries `sandboxRef` and `tenantId`
```

with

```
when a gateway release ends the occupancy without reuse ([Section 4.6.3](#463-crd-field-ownership-and-write-boundaries)), or when the pod terminates; its spec carries `sandboxRef` and `tenantId`
```

Replace

```
(hold expiry, a hold the acquisition path ends, orphan GC, or pod termination)
```

with

```
(hold expiry, a hold the acquisition path ends, a release that ends the occupancy without reuse, orphan GC, or pod termination)
```

**SPEC-2b. §4.6.1 **Pod claim mechanism (per-pod occupancy claim):**, last sentence, and §4.6.1 **Reserved hold (claim retention across same-tenant sessions):**.** In **Pod claim mechanism**, replace

```
Claim traffic therefore scales with pod-occupancy episodes rather than with sessions: the claim is created when the gateway acquires an idle pod and deleted when the reserved hold below expires or the WarmPoolController completes the pod's termination.
```

with

```
Claim traffic therefore scales with pod-occupancy episodes rather than with sessions: the claim is created when the gateway acquires an idle pod, and it is deleted by the release that ends the occupancy without reuse ([Section 4.6.3](#463-crd-field-ownership-and-write-boundaries)), when the reserved hold below expires, or when the WarmPoolController completes the pod's termination.
```

In **Reserved hold**, replace

```
When occupancy reaches zero on a recycling pod, the gateway patches the claim's binding state to `recycling`,
```

with

```
When occupancy reaches zero on a recycling pod, unless the release ends the occupancy without reuse ([Section 4.6.3](#463-crd-field-ownership-and-write-boundaries)), the gateway patches the claim's binding state to `recycling`,
```

**SPEC-2c. §4.6.3 CRD Field Ownership table, row `SandboxClaim`.** This row is the home of the gateway's claim-delete edges, and the non-spec disposition table is its derivation. Replace the row that begins "| `SandboxClaim`    | `spec.*`, `status.*`" with

```
| `SandboxClaim`    | `spec.*`, `status.*`                               | Gateway (not a controller) | Created by the gateway at pod acquisition; binding state written via the status subresource; deleted by the gateway at hold expiry, when its acquisition path ends the hold on a pool outside the runtime-process acknowledgment rule ([Section 5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes)), or when a release ends the occupancy without reuse. Every release on a pool with `maxConcurrentSessions: 1` ends the occupancy, and on a pool with `maxConcurrentSessions` above 1 the release that brings the slot occupancy to zero ends it ([Section 5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes)). A release ends the occupancy without reuse when the pool does not recycle, when no gateway replica published the session's binding to the pod, the gateway step that follows the pod's acceptance of the session's start or resume and from which the session's message delivery and its release reach the pod ([Section 7.3](07_session-lifecycle.md#73-retry-and-resume)), or, on a pool with `maxConcurrentSessions: 1`, when the session failed or crashed. A slot reservation withdrawn after its claim `CREATE` fails ([Section 4.6.1](#461-warm-pool-controller-pod-lifecycle)) ends no occupancy; deleted by the WarmPoolController at pod termination and orphan GC |
```

**SPEC-2d. §4.6.3 **`SandboxClaim.status.phase` enumeration**, bullet `failed`.** Replace

```
- `failed` — scrub-failure retirement under `onScrubFailure: fail`, or a failed or crashed session on a pool with `maxConcurrentSessions: 1` ([Section 5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes)); the projection drains and terminates the pod. Terminal.
```

with

```
- `failed` — scrub-failure retirement under `onScrubFailure: fail` ([Section 5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes)); the projection drains and terminates the pod. Terminal.
```

**SPEC-2e. §5.2 **Recycle lifecycle (`recycle.enabled: true`).**, first sentence.** Replace

```
When the pod's occupancy reaches zero, the gateway patches the pod's `SandboxClaim` to `recycling` and the whole-pod boundary runs:
```

with

```
When the pod's occupancy reaches zero, unless the release ends the occupancy without reuse ([Section 4.6.3](04_system-components.md#463-crd-field-ownership-and-write-boundaries)), the gateway patches the pod's `SandboxClaim` to `recycling` and the whole-pod boundary runs:
```

**SPEC-2f. §6.2 Pod State Machine, concurrent-occupancy diagram note.** Replace

```
  (occupancy reaches zero: the recycle edges above apply when recycle.enabled: true;
   otherwise the claim is deleted and the pod drains)
```

with

```
  (occupancy reaches zero: the recycle edges above apply unless the last release ends the
   occupancy without reuse, which deletes the claim under §4.6.3)
```

**SPEC-2g. §28.3 register-entry register, row `REG-CLAIM`, writer column.** Replace

```
the deletes at hold expiry and when an acquisition ends the hold, and the WarmPoolController leader
```

with

```
the deletes at hold expiry, when an acquisition ends the hold, and when a release ends the occupancy without reuse ([§4.6.3](04_system-components.md#463-crd-field-ownership-and-write-boundaries)), and the WarmPoolController leader
```

**SPEC-2h. §6.2 Pod State Machine: recycle-edges header and label, **`reserved` hold semantics.**, and **Concurrent pod lifecycle (`maxConcurrentSessions > 1`).**.** Make these in-place substitutions.

In the Recycle edges block header of the fenced diagram, replace the three lines

```
Recycle edges (recycle.enabled: true; occupancy reaches zero after clean session termination; the claim
is patched bound → recycling and the whole-pod scrub runs while the pod projects claimed, bounded by
the gateway-side scrub-report timeout):
```

with

```
Recycle edges (recycle.enabled: true; occupancy reaches zero, unless the release ends the occupancy
without reuse under §4.6.3; the claim is patched bound → recycling and the whole-pod scrub runs while
the pod projects claimed, bounded by the gateway-side scrub-report timeout):
```

In the Recycle edges block of the fenced diagram, in the `claimed ──→ draining` recycle-disposition label, replace the two lines

```
                                     a failed session on a maxConcurrentSessions: 1 pool (see §5.2); a
                                     missing scrub report at the gateway-side timeout; an unschedulable host
```

with the single line

```
                                     a missing scrub report at the gateway-side timeout; an unschedulable host
```

In **`reserved` hold semantics.**, replace

```
When occupancy reaches zero on a recycling pod, the gateway patches the claim's binding state to `recycling`,
```

with

```
When occupancy reaches zero on a recycling pod, unless the release ends the occupancy without reuse ([Section 4.6.3](04_system-components.md#463-crd-field-ownership-and-write-boundaries)), the gateway patches the claim's binding state to `recycling`,
```

In **Concurrent pod lifecycle (`maxConcurrentSessions > 1`).**, replace

```
the pod leaves `claimed` through the recycle edges when `recycle.enabled` is true, or through `draining` otherwise.
```

with

```
the last release deletes the claim when it ends the occupancy without reuse ([Section 4.6.3](04_system-components.md#463-crd-field-ownership-and-write-boundaries)), and the pod otherwise leaves `claimed` through the recycle edges.
```

**SPEC-2i. §28.5.1 Gateway-to-pod, `CH-ATTACH` **Degradation.**.** The anchor is hard-wrapped; quote it with its line breaks and indentation. Replace

```
  transition the replica releases the session's binding. A pod serving one session is released with the
  `failed` disposition ([§6.2](06_warm-pod-model.md#62-pod-state-machine) "Pod crash during an active
```

with

```
  transition the replica releases the session's binding. A pod serving one session is released
  ([§6.2](06_warm-pod-model.md#62-pod-state-machine) "Pod crash during an active
```

**SPEC-2j. §29.4, step 17.** The anchor is hard-wrapped with a 4-space indent; quote it with its line breaks. Replace

```
    pod. On the default disposition the claim is deleted and the pod projects `draining` and then
    `terminated` ([§6.2](06_warm-pod-model.md#62-pod-state-machine),
    [§4.6.1](04_system-components.md#461-warm-pool-controller-pod-lifecycle), §28.3). On the recycle
```

with

```
    pod. When the release ends the occupancy without reuse, the gateway deletes the claim
    ([§4.6.3](04_system-components.md#463-crd-field-ownership-and-write-boundaries), §28.3), and the
    pod's phase follows the [§4.6.1](04_system-components.md#461-warm-pool-controller-pod-lifecycle)
    occupancy projection. On the recycle
```

**SPEC-2k. §29.4, step 12.** The anchor is hard-wrapped with a 4-space indent; quote it with its line breaks. Replace

```
    `Shutdown`, the graceful end-of-session teardown of the named session. On the default disposition the
    pod is replaced. On the recycle disposition, which applies
    when occupancy reaches zero on a recycling pod, the request also carries the pod identity and the
```

with

```
    `Shutdown`, the graceful end-of-session teardown of the named session. On the recycle disposition,
    which applies when occupancy reaches zero on a recycling pod unless the release ends the occupancy
    without reuse ([§4.6.3](04_system-components.md#463-crd-field-ownership-and-write-boundaries)),
    the request also carries the pod identity and the
```

**SPEC-2l. §7.2 **Mid-resume terminal transitions — snapshot-close semantics.**, step 3.** Replace

```
before the half-claimed replacement pod is released back to the pool via the standard pod release path ([§6.2](06_warm-pod-model.md#62-pod-state-machine)).
```

with

```
before the gateway releases the half-claimed replacement pod ([§4.6.3](04_system-components.md#463-crd-field-ownership-and-write-boundaries) states what that release does to the pod's claim).
```

**SPEC-2m. §4.7 *Gateway → Adapter RPCs:* table, row `Shutdown`.** The row is one physical table line and stays one. Replace

```
The request carries the recycle disposition beside that teardown rather than selecting a scope. On the default disposition the pod is replaced. On the **recycle disposition** (occupancy zero on a recycling pod, [Section 5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes)) the request
```

with

```
The request carries the recycle disposition beside that teardown rather than selecting a scope. On the **recycle disposition** (occupancy zero on a recycling pod unless the release ends the occupancy without reuse, [Section 4.6.3](#463-crd-field-ownership-and-write-boundaries)) the request
```

**SPEC-2n. §6.2 Pod State Machine, **Client / parent / admin cancel mid-resume (`resuming → cancelled`):**.** Replace

```
the half-claimed replacement pod, which is then released to the pool,
```

with

```
the half-claimed replacement pod, which the gateway then releases ([§4.6.3](04_system-components.md#463-crd-field-ownership-and-write-boundaries)),
```

**SPEC-2o. §7.1 `sessionIsolationLevel` table, row `scrubPolicy`.** The row is one physical table line and stays one. Replace

```
and a whole-pod scrub runs when occupancy reaches zero on a recycling pool
```

with

```
and a whole-pod scrub runs when occupancy reaches zero on a recycling pool unless the release ends the occupancy without reuse ([Section 4.6.3](04_system-components.md#463-crd-field-ownership-and-write-boundaries))
```

**SPEC-2p. §4.9 **Pre-Claim Credential Availability Check**, finalize mismatch.** Replace

```
it releases the claimed pod back to the warm pool and returns
```

with

```
it releases the claimed pod ([Section 4.6.3](#463-crd-field-ownership-and-write-boundaries)) and returns
```

**SPEC-2q. §8.3 **Credential availability pre-check at delegation time:**.** Replace

```
the gateway releases the pod back to the warm pool and returns
```

with

```
the gateway releases the pod ([Section 4.6.3](04_system-components.md#463-crd-field-ownership-and-write-boundaries)) and returns
```

**SPEC-2r. §15.1 session-state table, row `created`.** The row is one physical table line and stays one. Replace

```
releases the pod claim back to the pool,
```

with

```
releases the pod claim ([§4.6.3](04_system-components.md#463-crd-field-ownership-and-write-boundaries)),
```

**SPEC-2s. §24.11 Session Investigation table, row `lenny-ctl admin sessions force-terminate <id>`.** The row is one physical table line and stays one. Replace

```
and the assigned pod is released to the pool
```

with

```
and the gateway releases the assigned pod ([§4.6.3](04_system-components.md#463-crd-field-ownership-and-write-boundaries))
```

**SPEC-2t. §5.2 `sessionPolicy` common-configurations table, row `Concurrent`.** The row is one physical table line and stays one. Replace

```
and recycles when occupancy reaches zero.
```

with

```
and recycles when occupancy reaches zero unless the release ends the occupancy without reuse ([Section 4.6.3](04_system-components.md#463-crd-field-ownership-and-write-boundaries)).
```

**SPEC-2u. §28.8 Failure and degradation matrix, row `CH-ATTACH`.** The row is one physical table line and stays one. Replace

```
names no retirement-counter reason for a `failed`-disposition retirement of a pod serving one session
```

with

```
names no retirement-counter reason for the retirement of a pod serving one session whose session failed or crashed
```

### SPEC-3 · spec/11_policy-and-controls.md § 11.4

**SPEC-3a. §11.4 User Invalidation, **Full revoke propagation mechanism:**, steps 1 to 4 and 6.** Steps 5 and 7 are unchanged, and the step numbers do not change. Replace

```
1. Gateway looks up all active sessions for the user (via SessionStore).
2. For each session in the user's task tree: gateway sends a `Shutdown` RPC to the pod with reason `USER_REVOKED`.
3. The pod's runtime adapter tears down the session ([Section 4.7](04_system-components.md#47-runtime-adapter), `Shutdown`), which does not end the runtime process ([Section 4.7.10](04_system-components.md#4710-deployment-model), "Runtime process lifetime").
4. Gateway marks all sessions as terminated in SessionStore.
```

with

```
1. The gateway looks up all active sessions for the user (via SessionStore) and, before it writes any cancel in step 2, returns every credential lease those sessions hold to its pool and, after the last return, adds those leases to the credential deny list ([Section 4.9](04_system-components.md#49-credential-leasing-service)).
2. The gateway marks each of those sessions `cancelled` in the SessionStore. After the write that cancels a session, the gateway releases the session's pod binding on the replica that holds it, which sends the pod the [§4.7](04_system-components.md#47-runtime-adapter) `Shutdown` RPC.
3. The pod's runtime adapter tears down the session ([Section 4.7](04_system-components.md#47-runtime-adapter), `Shutdown`), which does not end the runtime process ([Section 4.7.10](04_system-components.md#4710-deployment-model), "Runtime process lifetime").
4. For a cancelled session that never started and whose pod binding the handling gateway replica does not hold, the gateway reclaims the pod or the slot that the session claimed, before that replica's step-2 releases.
```

Replace

```
6. Credential leases held by the user's sessions are revoked (returned to pool).
```

with

```
6. Step 1 revokes the credential leases held by those sessions.
```

**SPEC-3b. §11.4 **Note**.** Make three in-place substitutions and one insertion.

Replace

```
it cancels the user's non-terminal sessions in the SessionStore, sends the [§4.7](04_system-components.md#47-runtime-adapter) `Shutdown` RPC to the pods that replica coordinates, revokes the user's credential leases,
```

with

```
it revokes the credential leases of the user's non-terminal sessions, cancels those sessions in the SessionStore, releases the bindings that replica holds for the cancelled sessions,
```

Replace

```
Because each local pod `Shutdown` RPC is bounded, the call blocks for a bounded duration rather than returning immediately.
```

with

```
Because each local release is bounded, the call blocks for a bounded duration rather than returning immediately.
```

Replace

```
the gateway publishes the step-2 `Shutdown` request,
```

with

```
the gateway publishes the step-2 release request,
```

Insert the sentence below immediately after the sentence that begins "The response body reports the per-effect counts".

```
The reported lease count is the number of leases step 1 adds to the deny list.
```

## Spec files touched

- spec/04_system-components.md (SPEC-1j, SPEC-2a, SPEC-2b, SPEC-2c, SPEC-2d, SPEC-2m, SPEC-2p)
- spec/05_runtime-registry-and-pool-model.md (SPEC-1a, SPEC-1b, SPEC-2e, SPEC-2t)
- spec/06_warm-pod-model.md (SPEC-1g, SPEC-2f, SPEC-2h, SPEC-2n)
- spec/07_session-lifecycle.md (SPEC-2l, SPEC-2o)
- spec/08_recursive-delegation.md (SPEC-2q)
- spec/10_gateway-internals.md (SPEC-1f)
- spec/11_policy-and-controls.md (SPEC-3a, SPEC-3b)
- spec/12_storage-architecture.md (SPEC-1c, SPEC-1d)
- spec/15_external-api-surface.md (SPEC-2r)
- spec/24_lenny-ctl-command-reference.md (SPEC-2s)
- spec/28_communication-channels.md (SPEC-1e, SPEC-1h, SPEC-2g, SPEC-2i, SPEC-2u)
- spec/29_communication-scenarios.md (SPEC-1i, SPEC-2j, SPEC-2k)
