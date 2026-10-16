# Spec changes: Release ownership for pods, slots, per-pod claims, and credential leases at and after a terminal write

## Design (as the spec must state it)

The specification states three things this proposal changes.

1. **REG-SLOTCOUNT is a per-pod set keyed by session.** The key `lenny:pod:{pod_id}:active_slots` and the register identifier stay. The value is a Redis set with one member per session holding a slot on the pod and one `leaked:<sessionId>` member per leaked slot. A reservation adds the session's member under the cap. A release removes the session's member. The occupancy-zero disposition runs only on the release that removed the last member. §5.2 **Slot assignment atomicity** is the home of these rules (SPEC-1a).
2. **The SandboxClaim writer set names the gateway's deletes that end an occupancy without reuse.** §4.6.3's ownership row is the home of the writer edge (SPEC-2c). The §4.6 CRD row, §4.6.1, §6.2, and REG-CLAIM cite §4.6.3 for it. §5.2 **Recycle lifecycle** states the pre-`running` release that deletes the claim on a recycling pool (SPEC-2e).
3. **full_revoke revokes the user's sessions' credential leases before it cancels the sessions, and releases each cancelled session through the session release.** §11.4 steps 1, 2, 4, and 6 and the **Note** state it (SPEC-3).

## Edge cases and accepted failure modes

- **Clean release, then leaked release of the same session.** A pre-running reclaim releases clean, and a peer's in-flight `/start` later releases the same slot with a cleanup that was not acknowledged clean. The second release finds no session member and still adds the `leaked` member, so the leaked slot stays counted (SPEC-1a).
- **A reseed drops leaked members and unpersisted reservations (departure).** A rehydration, or the §10.1.4 whole-pod reset, rebuilds the set from the non-terminal session rows. Those rows carry no leaked slots, and a create reserves its slot before it persists the row that names the pod. A leaked slot is therefore uncounted after a Redis restart or a whole-pod reset. A release-path rehydration (SPEC-1a) that empties the set runs the occupancy-zero disposition under a reservation the reseed dropped: the claim is deleted and the pod drains, or the claim is patched to `recycling` and the pod is scrubbed while that session binds. Both gaps predate this proposal (§6.2 **`leaked` slot semantics.** says a leaked slot persists until pod termination, today's reseed already drops it, and today's release reports zero occupancy on a counter a restart emptied).
- **A duplicate release after a Redis restart (departure).** When a session's first release emptied the set and ran the occupancy-zero disposition before a Redis restart, a duplicate release of that session after the restart and before any reservation seeds its own member (SPEC-1a), removes it, and runs the disposition again. On a recycling pod in the reserved hold, a recycling release patches the claim back to `recycling`, and the pod is scrubbed again or retires on the missing-report timeout, which fails closed. A release that does not recycle deletes the held claim.
- **A repeated reservation for a session already in the set.** The reservation adds nothing and succeeds (SPEC-1a). One session does not hold two slots on one pod.
- **Standing test clusters.** A Redis that holds a string value under `lenny:pod:*:active_slots` from a run before this change answers `WRONGTYPE` to the set scripts. The test-infrastructure preflight in CODE-1 deletes those keys. No production shim exists, because the platform has no deployments.
- **full_revoke of a session bound on a peer replica.** The handling replica publishes the release request, and the peer releases asynchronously. The response counts only the handling replica's local releases, as the §11.4 **Note** already states for its other effects.

## Staged edits

Every table row this proposal edits is one physical line, and it stays one physical line after the edit. Anchors are quoted from the tree at d05c25630.

### SPEC-1 · spec/05_runtime-registry-and-pool-model.md § 5.2; spec/12_storage-architecture.md § 12.4; spec/28_communication-channels.md § 28.3, § 28.6; spec/10_gateway-internals.md § 10.1.4; spec/06_warm-pod-model.md § 6.2; spec/29_communication-scenarios.md § 29.2, § 29.10; spec/04_system-components.md § 4.6.1

**SPEC-1a. §5.2 **Slot assignment atomicity (`maxConcurrentSessions > 1`).**** Replace the text that begins "The gateway uses an atomic Redis `INCR` with a cap check:" and ends "The counter is the intra-pod capacity gate;" with the block below. The rest of the paragraph, from "pod acquisition itself is guarded by the deterministic per-pod `SandboxClaim` CREATE" onward, is unchanged.

```
The gateway keeps a pod's slot occupancy in a Redis set, `lenny:pod:{pod_id}:active_slots`. The set holds one member per session that holds a slot on the pod, named by the session identifier, and one member `leaked:<sessionId>` per slot whose cleanup was not acknowledged clean ([Section 6.2](06_warm-pod-model.md#62-pod-state-machine) **`leaked` slot semantics.**). The set's cardinality is the pod's slot occupancy. A reservation runs a Lua script. When the session's member is already present, the script adds nothing and returns the cardinality, whatever the cardinality is. Otherwise, when the cardinality is below `maxConcurrentSessions`, it adds the member and returns the new cardinality, and when the cardinality has reached `maxConcurrentSessions`, it adds nothing and returns `nil` (slot unavailable). Because the check and the add run in one script, two concurrent assignments cannot both observe the same free slot. A release runs a Lua script keyed by the session identifier. It removes the session's member, and it returns whether it removed the member and the remaining cardinality. A release that finds the pod's `rehydrated` flag absent first rehydrates the set as **Post-recovery rehydration atomicity.** below states, adding its own session's member to the seed, and then removes that member. A release of a slot whose cleanup was not acknowledged clean removes the session's member, if present, and adds a `leaked` member for the session, so a leak reported after a clean release of the same session is still counted. A second leaked release changes nothing. A clean release that finds no member for the session changes nothing, so every path that releases the same reservation can run and, while Redis keeps the set, the reservation is returned once. The occupancy-zero disposition (**Recycle lifecycle (`recycle.enabled: true`).** above, and [Section 4.6.3](04_system-components.md#463-crd-field-ownership-and-write-boundaries) for the claim delete) runs only on a release that removed the session's member and left the set empty. The set is the intra-pod capacity gate;
```

**SPEC-1b. §5.2 **Post-recovery rehydration atomicity.**** Make four in-place substitutions in the paragraph.

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
(indicating the set has not been rehydrated since the last Redis restart)
```

Replace

```
queries `SessionStore.GetActiveSlotsByPod(pod_id)` from Postgres, writes the accurate count to `lenny:pod:{pod_id}:active_slots`, sets the `rehydrated` flag
```

with

```
queries `SessionStore.GetActiveSlotsByPod(pod_id)` from Postgres for the identifiers of the pod's non-terminal sessions, replaces `lenny:pod:{pod_id}:active_slots` with a set of those identifiers, sets the `rehydrated` flag
```

Replace

```
could both observe `counter=0` and succeed
```

with

```
could both observe an empty set and succeed
```

**SPEC-1c. §12.4 Redis HA and Failure Modes, key table.** Replace the row that begins "| `lenny:pod:{pod_id}:active_slots` | Session-mode slot counter |" with

```
| `lenny:pod:{pod_id}:active_slots` | Session-mode slot set | **Pod-scoped** (not tenant-prefixed — intentional, as pod IDs are already globally unique within the cluster). A Redis set with one member per session holding a slot on the pod and one `leaked:<sessionId>` member per leaked slot; its cardinality is the pod's slot occupancy, and the atomic Lua reservation that adds a member only while the cardinality is below `sessionPolicy.maxConcurrentSessions` is the intra-pod capacity gate for session-mode pools ([§5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes), "Slot assignment atomicity"). On Redis restart, the set is lost; the gateway performs **blocking rehydration** from `SessionStore.GetActiveSlotsByPod(pod_id)` before accepting new slot assignments — see [§5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes) ("Post-recovery rehydration atomicity") for the full protocol. The Lua script checks a per-pod `rehydrated` flag (`lenny:pod:{pod_id}:rehydrated`) atomically; if absent, all slot allocation attempts for that pod block until rehydration completes, preventing concurrent requests from observing a stale empty set. During a Redis outage the set has a Postgres fallback — see the failure-behavior table below. |
```

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

### SPEC-2 · spec/04_system-components.md § 4.6, § 4.6.1, § 4.6.3; spec/05_runtime-registry-and-pool-model.md § 5.2; spec/06_warm-pod-model.md § 6.2; spec/28_communication-channels.md § 28.3, § 28.5.1

**SPEC-2a. §4.6 CRD table, row `SandboxClaim`.** Make two in-place substitutions in the row.

Replace

```
or when the pod terminates; its spec carries `sandboxRef` and `tenantId`
```

with

```
when the gateway ends the pod's occupancy without reuse ([Section 4.6.3](#463-crd-field-ownership-and-write-boundaries)), or when the pod terminates; its spec carries `sandboxRef` and `tenantId`
```

Replace

```
(hold expiry, a hold the acquisition path ends, orphan GC, or pod termination)
```

with

```
(hold expiry, a hold the acquisition path ends, a release that ends an occupancy without reuse, orphan GC, or pod termination)
```

**SPEC-2b. §4.6.1 **Pod claim mechanism (per-pod occupancy claim):**, last sentence, and §4.6.1 **Reserved hold (claim retention across same-tenant sessions):**.** In **Pod claim mechanism**, replace

```
Claim traffic therefore scales with pod-occupancy episodes rather than with sessions: the claim is created when the gateway acquires an idle pod and deleted when the reserved hold below expires or the WarmPoolController completes the pod's termination.
```

with

```
Claim traffic therefore scales with pod-occupancy episodes rather than with sessions: the claim is created when the gateway acquires an idle pod, and it is deleted by the release that ends an occupancy the pool does not reuse ([Section 4.6.3](#463-crd-field-ownership-and-write-boundaries)), when the reserved hold below expires, or when the WarmPoolController completes the pod's termination.
```

In **Reserved hold**, replace

```
When occupancy reaches zero on a recycling pod, the gateway patches the claim's binding state to `recycling`,
```

with

```
When occupancy reaches zero on a recycling pod and the release does not delete the claim ([Section 4.6.3](#463-crd-field-ownership-and-write-boundaries)), the gateway patches the claim's binding state to `recycling`,
```

**SPEC-2c. §4.6.3 CRD Field Ownership table, row `SandboxClaim`.** This row is the home of the gateway's claim-delete edges. Replace the row that begins "| `SandboxClaim`    | `spec.*`, `status.*`" with

```
| `SandboxClaim`    | `spec.*`, `status.*`                               | Gateway (not a controller) | Created by the gateway at pod acquisition; binding state written via the status subresource; deleted by the gateway at hold expiry, when its acquisition path ends the hold on a pool outside the runtime-process acknowledgment rule ([Section 5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes)), or when a release ends a pod's occupancy without reuse. Every release on a pool with `maxConcurrentSessions: 1` ends the occupancy, and on a pool with `maxConcurrentSessions` above 1 the release that brings the slot occupancy to zero ends it ([Section 5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes)). A release ends the occupancy without reuse when the pool does not recycle, when the released slot is pre-`running` ([Section 6.2](06_warm-pod-model.md#62-pod-state-machine)), or, on a pool with `maxConcurrentSessions: 1`, when the session failed or crashed. A slot reservation withdrawn after its claim `CREATE` fails ([Section 4.6.1](#461-warm-pool-controller-pod-lifecycle)) ends no occupancy; deleted by the WarmPoolController at pod termination and orphan GC |
```

**SPEC-2d. §4.6.3 **`SandboxClaim.status.phase` enumeration**, bullet `failed`.** Replace

```
- `failed` — scrub-failure retirement under `onScrubFailure: fail`, or a failed or crashed session on a pool with `maxConcurrentSessions: 1` ([Section 5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes)); the projection drains and terminates the pod. Terminal.
```

with

```
- `failed` — scrub-failure retirement under `onScrubFailure: fail` ([Section 5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes)); the projection drains and terminates the pod. Terminal.
```

**SPEC-2e. §5.2 **Recycle lifecycle (`recycle.enabled: true`).**** Insert the sentence below immediately after the first sentence of the paragraph, the one that ends "and the adapter reports the binary outcome via `ReportPodScrub`."

```
A pre-`running` release that ends the occupancy holds no adapter connection to carry the whole-pod recycle `Shutdown`, so it deletes the claim ([Section 4.6.3](04_system-components.md#463-crd-field-ownership-and-write-boundaries)).
```

**SPEC-2f. §6.2 Pod State Machine, concurrent-occupancy diagram note.** Replace

```
  (occupancy reaches zero: the recycle edges above apply when recycle.enabled: true;
   otherwise the claim is deleted and the pod drains)
```

with

```
  (occupancy reaches zero: the recycle edges above apply unless the last release deletes
   the claim under §4.6.3)
```

**SPEC-2g. §28.3 register-entry register, row `REG-CLAIM`, writer column.** Replace

```
the deletes at hold expiry and when an acquisition ends the hold, and the WarmPoolController leader
```

with

```
the deletes at hold expiry, when an acquisition ends the hold, and when a release ends a pod's occupancy without reuse ([§4.6.3](04_system-components.md#463-crd-field-ownership-and-write-boundaries)), and the WarmPoolController leader
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
Recycle edges (recycle.enabled: true; occupancy reaches zero and the release does not delete the claim
under §4.6.3; the claim is patched bound → recycling and the whole-pod scrub runs while the pod
projects claimed, bounded by the gateway-side scrub-report timeout):
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
When occupancy reaches zero on a recycling pod and the release does not delete the claim ([Section 4.6.3](04_system-components.md#463-crd-field-ownership-and-write-boundaries)), the gateway patches the claim's binding state to `recycling`,
```

In **Concurrent pod lifecycle (`maxConcurrentSessions > 1`).**, replace

```
the pod leaves `claimed` through the recycle edges when `recycle.enabled` is true, or through `draining` otherwise.
```

with

```
the pod leaves `claimed` through `draining` when the last release deletes the claim ([Section 4.6.3](04_system-components.md#463-crd-field-ownership-and-write-boundaries)), or through the recycle edges otherwise.
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
1. The gateway looks up all active sessions for the user (via SessionStore) and, before it writes any cancel in step 2, adds the credential leases those sessions hold to the credential deny list ([Section 4.9](04_system-components.md#49-credential-leasing-service)) and returns each of those leases to its pool.
2. The gateway marks each of those sessions `cancelled` in the SessionStore. After the write that cancels a session, the gateway releases the session's pod binding on the replica that holds it, which sends the pod the [§4.7](04_system-components.md#47-runtime-adapter) `Shutdown` RPC.
3. The pod's runtime adapter tears down the session ([Section 4.7](04_system-components.md#47-runtime-adapter), `Shutdown`), which does not end the runtime process ([Section 4.7.10](04_system-components.md#4710-deployment-model), "Runtime process lifetime").
4. For a cancelled session that never started, the gateway instead reclaims the pod or the slot that the session claimed.
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
it revokes the credential leases of the user's non-terminal sessions, cancels those sessions in the SessionStore, releases the bindings that replica holds for the cancelled sessions, reclaims what the cancelled pre-running sessions claimed,
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

- spec/04_system-components.md (SPEC-1j, SPEC-2a, SPEC-2b, SPEC-2c, SPEC-2d)
- spec/05_runtime-registry-and-pool-model.md (SPEC-1a, SPEC-1b, SPEC-2e)
- spec/06_warm-pod-model.md (SPEC-1g, SPEC-2f, SPEC-2h)
- spec/10_gateway-internals.md (SPEC-1f)
- spec/11_policy-and-controls.md (SPEC-3a, SPEC-3b)
- spec/12_storage-architecture.md (SPEC-1c, SPEC-1d)
- spec/28_communication-channels.md (SPEC-1e, SPEC-1h, SPEC-2g, SPEC-2i)
- spec/29_communication-scenarios.md (SPEC-1i)
