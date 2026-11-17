# Steps: 0147 Tenant-derived runtimes

## Steps

### 0147.S1 Shared-record boundary
- status: Planned
- aliases: [DR-1]
- lanes: [specification, code, documentation]
- closes: [F-5.2.67 (part)]

Removes `tenant-admin` writes on platform-global runtimes and pools, including the permissions, reads access rows on session, discovery, and delegation paths, implements the seed's `tenantAccess`, reserves `--`, and limits names to 63 characters. Closes DF-1, DF-2, DF-10, DF-14, and DF-19, and the remainder of F-5.2.67 that the overlay of [EG-9](../0127-egress-and-git/steps.md#steps) (0127.S9) does not cover.

It has no dependency. It should land first, because it closes a Critical defect. The `--` reservation lands early, because a stored format, a wire contract, or a shipped enforcement point would otherwise need rework. This step stands on its own and fixes today's tree, whether or not the rest lands.

### 0147.S2 Pods for derived runtimes
- status: Planned
- aliases: [DR-2]
- lanes: [specification, code, documentation]

`podRuntimeRef` on the CRD and pool row, every `Runtime` read in the AgentPod controller, and the §5.1 text on fallback and `sessionPolicy`. It also covers a pool on a platform derived runtime. Closes the derived half of DF-3, DF-5, and DF-6, and DF-7 for platform-derived runtimes.

It has no dependency. `podRuntimeRef` lands early, because a stored format, a wire contract, or a shipped enforcement point would otherwise need rework. This step stands on its own and fixes today's tree, whether or not the rest lands.

### 0147.S3 Setup command identity
- status: Planned
- aliases: [DR-3]
- lanes: [specification, code, documentation]

Runs setup commands at the slot UID through the slot's runtime supervisor, as session isolation states for every slot. Closes DF-4 for the adapter's identity.

It has no dependency. This step stands on its own and fixes today's tree, whether or not the rest lands.

### 0147.S4 Tenant runtime records
- status: Planned
- aliases: [DR-4]
- lanes: [specification, code, documentation]
- depends-on: [0147.S1, 0127.S9, 0149.S3, 0147.S3 (part), 0119.S4 (part)]

The table, tenant-view interface and the holders that change type, `ErrTenantScopedName`, grant, prefix, permission, routes, field rules, Helm values, erasure, and classification, and the `registered` check at the post-chain point under "Registered rows" (AR O8), which lands with or after proposal AR-D (0119.S4) of [`0119-agent-runtimes-only`](../0119-agent-runtimes-only/steps.md#steps) removes SD-P4. Closes DF-13, the table half of DF-12, and DF-7 for tenant-derived runtimes.

It needs 0147.S1 for the name rule, [0127.S9](../0127-egress-and-git/steps.md#steps) (EG-9) for the write path and gate model, and [0149.S3](../0149-tenant-scoping-identity/change.md#ts-3-session-environment-context-and-cross-environment-sharing) (TS-3) for the fail-closed gate. Its tenant defaults step, for files and commands, needs 0147.S3, because neither a tenant command nor a base command reading a tenant file can run at the adapter's UID.

### 0147.S5 Owned pools
- status: Planned
- aliases: [DR-5]
- lanes: [specification, code, documentation]
- depends-on: [0147.S2, 0147.S4, 0127.S9]

The template copy and its propagation, `ownedOverlays`, the readers of `lenny.dev/served`, the bounds with the base-rollout exemption, the PoolScalingController clamp, tenant operations, and tenant suspension and deletion.

It needs 0147.S2 for the pod path, 0147.S4 for the records, and [0127.S9](../0127-egress-and-git/steps.md#steps) (EG-9) for `ownerTenantId` with the label rule.

### 0147.S6 Repository folders
- status: Planned
- aliases: [DR-12]
- lanes: [specification, code, documentation]

`gitClone.path` required and never `.`, the overlap refusals, `workingDirectory` with its start check and resume fallback, `setupCommands[].cwd`, and the runtime `cwd` from session start.

The required `gitClone.path` and `workingDirectory` land early, because a stored format, a wire contract, or a shipped enforcement point would otherwise need rework. This step stands on its own and fixes today's tree, whether or not the rest lands.

### 0147.S7 Default git clones
- status: Planned
- aliases: [DR-9]
- lanes: [specification, code, documentation]
- depends-on: [0147.S3, 0147.S4, 0147.S5, 0127.S3, 0144.S1]

The field on every runtime, the write checks, the composition order on every path, the refusal details, the write scope, and the guardrail values.

It depends on 0147.S3, 0147.S4, and 0147.S5, on the [egress design](../0127-egress-and-git/change.md)'s pod-side clone (0127.S3), on the materialization design's helper with the per-slot disk watch, and on the plan check function of [SD-1](../0144-session-derive/change.md#sd-1-one-route) (0144.S1), the derive route. A clone is code, it needs an owned pool's git path, and F-14.1.37 means no `gitClone` works in a deployed image today. A clone under a VCS pool with `userAuthorization: required` also needs [UG-2](../0152-user-git-identity/change.md#ug-2-repository-authorization) (0152.S2).

### 0147.S8 Shared assets and warm stores
- status: Planned
- aliases: [DR-11]
- lanes: [specification, code, documentation]
- depends-on: [0147.S7, 0118.S4, 0153.S9]

A tenant-derived runtime's `sharedAssets` with `TENANT_ASSETS_REQUIRE_OWNED_POOL`, the artifact upload route with its quota and garbage collection, the asset Helm values, the grant's `allowAssets`, `gitRepository` assets with their declarer and the protections of `platform`-declared ones, `maxWarmCloneBytes`, the owned pool's share of the warm list and its carrier, `prestage: true` with adoption by repository, `coveredStores` and `withheldStores` on the claim and the session row, the withholding of a store, the fill in `lenny-store-fill`, the volume, the pre-session reach, the age limit, the backoff, the fill limits, the environment rule at write and at render, and the exclusion from T of pods a platform-declared entry failed.

It depends on 0147.S7, on draft 0111 as the egress design rewrites it and on the requirements under "Relationship to draft proposal 0111", on the materialization design's helper with the warm-up checkout, on the pod layout's store volume, and on the `lenny-store-fill` container of the egress design's decision 61. These supply the clone it warms, the warm list, store, slot repository, capture copy, and restore completion it reuses, the staged checkout, the fill, and the backstop under a checkout. This step adds no field to a default clone, so it can land after 0147.S7 with no format change to the clone. The session row's `coveredStores` lands with this step or with the platform chunk sets of [`0131-incremental-checkpoint`](../0131-incremental-checkpoint/change.md#platform-chunk-sets), whichever is first.

### 0147.S9 Capacity reservation
- status: Planned
- aliases: [DR-10]
- lanes: [specification, code, documentation]
- depends-on: [0147.S5]

The Helm values, allowance, `reservedPods`, warm target, split quota, the reserved `PriorityClass` and its quota scope, the use of the placement design's reserved overlay, budget figure, reporting, and alert.

It needs 0147.S5 for the owned pool and its budget.

### 0147.S10 Base lifecycle
- status: Planned
- aliases: [DR-6]
- lanes: [specification, code, documentation]
- depends-on: [0147.S4, 0147.S5]

The live merge against base and grant, `baseStatus`, the outbox, retirement, emergency, and the deletion rule. Closes DF-8.

It needs 0147.S4 and 0147.S5 for the merge and pools it acts on.

### 0147.S11 Oversight and records
- status: Planned
- aliases: [DR-7]
- lanes: [specification, code, documentation]
- depends-on: [0147.S4]

The guardrail, posture, and events.

It needs 0147.S4. It also depends on [SO-1](../0142-security-oversight/change.md#so-1-security-roles) (0142.S1) and [SO-2](../0142-security-oversight/change.md#so-2-tenant-security-record-and-posture) (0142.S2), whichever lands second adding the case. These supply the role and the record.

### 0147.S12 Interactions
- status: Planned
- aliases: [DR-8]
- lanes: [specification, code, documentation]
- depends-on: [0147.S4, 0149.S1, 0149.S3, 0141.S1]

Environment warnings, the checks at the post-chain point on the final row, and delegation resolution.

It needs 0147.S4, [0149.S1](../0149-tenant-scoping-identity/change.md#ts-1-environment-scoping) (TS-1), and [0149.S3](../0149-tenant-scoping-identity/change.md#ts-3-session-environment-context-and-cross-environment-sharing) (TS-3) for the checks on the final row, and the first proposal of [`0141-runtime-routing`](../0141-runtime-routing/steps.md#steps) (0141.S1) for the post-chain point where they run.

### Notes on the steps

`lenny.dev/served` with `ownerTenantId` in [EG-9](../0127-egress-and-git/steps.md#steps) (0127.S9) lands early, because a stored format, a wire contract, or a shipped enforcement point would otherwise need rework.

The way [`0149-tenant-scoping-identity` decision 15](../0149-tenant-scoping-identity/decisions.md#decisions) gives needs 0147.S1, 0147.S2, 0147.S4 with neither default flag, 0147.S5, and [TS-1](../0149-tenant-scoping-identity/change.md#ts-1-environment-scoping) (0149.S1). It does not need 0147.S3. A platform-created derived runtime needs 0147.S2 alone, so that way has an operator's version before the rest lands. No `EG-`, `TS-`, or `SO-` proposal depends on a DR step, apart from the requests under "Changes requested of other design documents".
