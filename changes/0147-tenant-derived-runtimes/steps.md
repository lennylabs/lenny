# Steps: 0147 Tenant-derived runtimes

## Steps

### 0147.S1 Shared-record boundary
- status: Planned
- aliases: [DR-1]
- lanes: [specification, code, documentation]
- closes: [F-5.1.26, F-10.6.25, F-5.1.31, F-17.6.27, F-17.6.28, F-5.2.67 (part)]

Removes `tenant-admin` writes on platform-global runtimes and pools, including the permissions, reads access rows on session, discovery, and delegation paths, implements the seed's `tenantAccess`, reserves `--`, and limits names to 63 characters. Closes DF-1, DF-2, DF-10, DF-14, and DF-19, and the remainder of F-5.2.67 that the overlay of [EG-9](../0127-egress-and-git/steps.md#steps) (0127.S9) does not cover.

It has no dependency. It should land first, because it closes a Critical defect (F-5.1.26). The post-chain point of [0141.S1](../0141-runtime-routing/steps.md#steps) does not exist yet, so the pool access check runs on each create, derive, and delegated child path after the experiment routing step and before the claim, and 0141.S1 moves it to the post-chain point, as DR-1 states. [0137.S4](../0137-pod-lifecycle-controller/steps.md#steps) also lists F-5.1.31, which both steps close by limiting names to 63 characters; whichever lands first closes it, and whichever of 0147.S1 and 0137.S4 lands second removes F-5.1.31 from its `closes`. The `--` reservation lands early, because a stored format, a wire contract, or a shipped enforcement point would otherwise need rework. This step stands on its own and fixes today's tree, whether or not the rest lands.

### 0147.S2 Pods for derived runtimes
- status: Planned
- aliases: [DR-2]
- lanes: [specification, code, documentation]
- closes: [F-5.1.27 (part), F-5.1.28, F-5.1.29, F-5.1.30 (part)]

`podRuntimeRef` on the CRD and pool row, every `Runtime` read in the AgentPod controller, and the §5.1 text on fallback and `sessionPolicy`, with the copy of a platform-derived runtime's `sessionPolicy` to its pools' rows that DR-2 states. It also covers a pool on a platform derived runtime. Closes the derived half of DF-3, DF-5, and DF-6, and DF-7 for platform-derived runtimes.

It has no dependency. `podRuntimeRef` lands early, because a stored format, a wire contract, or a shipped enforcement point would otherwise need rework. This step stands on its own and fixes today's tree, whether or not the rest lands.

### 0147.S3 Setup command identity
- status: Planned
- aliases: [DR-3]
- lanes: [specification, code, documentation]
- depends-on: [0153.S1]
- closes: [F-7.5.17, F-8.7.16 (part)]

Runs setup commands at the slot UID through the slot's runtime supervisor, as session isolation states for every slot. Closes DF-4 for the adapter's identity, and the setup-command half of DF-16, whose materializer half [0153.S2](../0153-workspace-materialization/steps.md#steps) closes.

The supervisor's `setup` request is built by [0145.S3](../0145-session-isolation/steps.md#steps) (SI-3), and the adapter's per-slot run queue that sends each command through it by [0153.S1](../0153-workspace-materialization/steps.md#steps), which depends on 0145.S3. This step lands after 0153.S1 and adds what DR-3 states beyond them: the removal of `RunSetup`'s exec in the adapter's container (`pkg/adapter/workspace/setup.go`), so no setup command runs at the adapter's UID, the §7.5 **Security model:** and §4.7.10 text, and the tests of DF-4.

### 0147.S4 Tenant runtime records
- status: Planned
- aliases: [DR-4]
- lanes: [specification, code, documentation]
- depends-on: [0147.S1, 0127.S9, 0149.S3, 0141.S1, 0147.S3 (part), 0137.S4 (part)]
- closes: [F-8.3.13, F-5.1.32, F-5.1.30 (part)]

The table, tenant-view interface and the holders that change type, `ErrTenantScopedName`, grant, prefix, permission, routes, field rules, Helm values, erasure, and classification, and the `registered` check at the post-chain point under "Registered rows" (AR O8). Under AR O8 this step builds the `registered` column and the check, and 0119.S4 of [`0119-agent-runtimes-only`](../0119-agent-runtimes-only/steps.md#steps) builds no part of it. SD-P4 and `target_unsupervised` exist only in the text of 0144 and of this change, with nothing in the tree to remove, so this step has no edge to 0119.S4. The holders that change type include `runtimecapoverride.ResolveForTenant` and the capability override `PUT` handler. Closes DF-13, the table half of DF-12, and DF-7 for tenant-derived runtimes.

It needs 0147.S1 for the name rule, [0127.S9](../0127-egress-and-git/steps.md#steps) (EG-9) for the write path and gate model, [0149.S3](../0149-tenant-scoping-identity/change.md#ts-3-session-environment-context-and-cross-environment-sharing) (TS-3) for the fail-closed gate, [0141.S1](../0141-runtime-routing/steps.md#steps) for the post-chain point where the tenant-view refusal and the `registered` check run, and the name check of [0137.S4](../0137-pod-lifecycle-controller/steps.md#steps) (`runtimestore.ValidateName`, PLC-7) for its create route. Its tenant defaults step, for files and commands, needs 0147.S3, because neither a tenant command nor a base command reading a tenant file can run at the adapter's UID.

Its writes for the content stops of [`0144-session-derive`, "Stops"](../0144-session-derive/change.md#stops) need the `content_stop_epochs` table and `planOriginRuntimes` that [0144.S1](../0144-session-derive/steps.md#steps) adds. The writes are: this step raises the grant entry's counter in `content_stop_epochs` in the transaction that sets `allowWorkspaceFiles`, `allowSetupCommands`, or `allowGitClones` false, and appends a tenant-derived runtime to `planOriginRuntimes` when a plan composes its defaults. 0144.S1 reaches this step through partial dependencies (0122.S1, 0140.S1, and 0147.S5), so an edge from this step to 0144.S1 would close a loop, and whichever of 0147.S4 and 0144.S1 lands second adds these writes. Whether setting `allowGitCloneWrites` false also raises the counter is open decision 56 in `decisions.md`.

Under decision 8 of [0117](../0117-gateway-package-layout/decisions.md), this step changes no gateway seam operation: the tenant view lives in `pkg/runtime/runtimestore`, which is outside the confined packages, so the platform tools seam's holders change type without a seam operation.

### 0147.S5 Owned pools
- status: Planned
- aliases: [DR-5]
- lanes: [specification, code, documentation]
- depends-on: [0147.S2, 0147.S4, 0127.S9]

The template copy and its propagation, `ownedOverlays`, the readers of `lenny.dev/served`, the bounds with the base-rollout exemption, the PoolScalingController clamp, tenant operations, and tenant suspension and deletion.

It needs 0147.S2 for the pod path, 0147.S4 for the records, and [0127.S9](../0127-egress-and-git/steps.md#steps) (EG-9) for `ownerTenantId` with the label rule. The `placement.offering`, `placement.region`, and `placement.reservedOverlay` rows of the template copy land with the pool `placement` block in [0136.S5](../0136-placement/steps.md#steps), which depends on this step through 0136.S4.

### 0147.S6 Repository folders
- status: Planned
- aliases: [DR-12]
- lanes: [specification, code, documentation]

The plan and wire contract of DR-12: `gitClone.path` required and never `.`, the overlap refusals, `workingDirectory` with its composition rules, `setupCommands[].cwd` with the folder written on each stored command, and `cwd` on `session_start` and `StartSessionRequest`. The start check through the slot's broker, the resume fallback, and the constant root land in 0147.S13.

The consumers of these fields, the `setup` request's `cwd` in [0153.S1](../0153-workspace-materialization/steps.md#steps) and `session_start` in [0139.S1](../0139-runtime-protocol/steps.md#steps), land after this step. The required `gitClone.path` and `workingDirectory` land early, because a stored format, a wire contract, or a shipped enforcement point would otherwise need rework. This step stands on its own and fixes today's tree, whether or not the rest lands.

### 0147.S7 Default git clones
- status: Planned
- aliases: [DR-9]
- lanes: [specification, code, documentation]
- depends-on: [0147.S3, 0147.S4, 0147.S5, 0147.S6, 0141.S1, 0127.S3, 0144.S1, 0153.S4, 0118.S4, 0152.S2 (part)]

The field on every runtime, the write checks, the composition order on every path, the refusal details, the write scope, and the guardrail values.

It depends on 0147.S3, 0147.S4, and 0147.S5, on the [egress design](../0127-egress-and-git/change.md)'s pod-side clone (0127.S3), on git in the materialization design's helper ([0153.S4](../0153-workspace-materialization/steps.md#steps)), which runs every clone at the slot UID, on the per-slot disk watch of the pod layout ([0118.S4](../0118-agent-pod-layout/steps.md#steps)), and on the plan check function of [SD-1](../0144-session-derive/change.md#sd-1-one-route) (0144.S1), the derive route. A clone is code, it needs an owned pool's git path, and F-14.1.37 means no `gitClone` works in a deployed image today. A clone under a VCS pool with `userAuthorization: required` also needs [UG-2](../0152-user-git-identity/change.md#ug-2-repository-authorization) (0152.S2) for the repository authorization check, which is the part of 0152.S2 this step needs. It needs DR-12's required `path` (0147.S6), because DR-12 lands before DR-9, and the post-chain point of [0141.S1](../0141-runtime-routing/steps.md#steps), where composition and the clone checks run. The refusal of a default clone's `write: always` under a pool with `prAuthoring: user`, under "Write scope", reads the setting that [UG-3](../0152-user-git-identity/steps.md#steps) (0152.S3) adds, and whichever of 0147.S7 and 0152.S3 lands second adds that refusal, so neither step depends on the other.

Under decision 8 of [0117](../0117-gateway-package-layout/decisions.md), this step edits `spec/interfaces/gateway-seams.md` and `pkg/gateway/seam` for the gateway seam operations it changes. The delegated-child row of DR-9's "Binding" runs in the platform tools seam, so the seam reaches the session core's checks through `GWSEAM-ResolveEnvironments` for the child's environment and `GWSEAM-IssueVCSToken` for a credential, and this step adds one operation served by the session core that runs the auth binding, environment and allowlist, repository authorization, and commit pin checks on a child's composed default clones.

### 0147.S8 Shared assets and warm stores
- status: Planned
- aliases: [DR-11]
- lanes: [specification, code, documentation]
- depends-on: [0147.S7, 0118.S4, 0127.S5, 0153.S9, 0153.S8 (part)]

A tenant-derived runtime's `sharedAssets` with `TENANT_ASSETS_REQUIRE_OWNED_POOL`, the artifact upload route with its quota and garbage collection, the asset Helm values, the grant's `allowAssets`, `gitRepository` assets with their declarer and the protections of `platform`-declared ones, `maxWarmCloneBytes`, the owned pool's share of the warm list on `PodSet.spec.warmClones` (the carrier itself, rendered for every pool, lands with the rewritten draft 0111 in 0127.S5), `prestage: true` with adoption by repository, the read checks with `coveredStores` and `withheldStores` on the claim and the session row, the withholding of a store, the pre-session reach, the per-tenant fill limit, the `lenny.dev/owner-tenant` label, the environment rule at write and at render, and the exclusion from T of pods a platform-declared entry failed. The warm list's carrier and the rules that hold on every pool (the volume and its sizing, the age limit, the drain, the backoff, the fill limit for shared pools, and the tree bound) land in 0127.S5, under the split that "Relationship to draft proposal 0111" states. This step owns the warm-list environment predicate at write and at render, with the `environmentScoped` suspension under "Scoped repositories". The predicate reads the environment fields of [TS-1](../0149-tenant-scoping-identity/change.md#ts-1-environment-scoping) (0149.S1), and whichever of 0147.S8 and 0149.S1 lands second adds the environment case. The repository check of [UG-2](../0152-user-git-identity/steps.md#steps) (0152.S2) on the warm list, with the withholding of a store and `withheldWarmEntries`, is wired by whichever of 0147.S8 and 0152.S2 lands second, so this step has no edge to 0152.S2 and 0152.S2 has none to it.

It depends on 0147.S7, on draft 0111 as the egress design rewrites it in [0127.S5](../0127-egress-and-git/steps.md#steps) and on the requirements under "Relationship to draft proposal 0111", on the pod layout's store volume, and on the `lenny-store-fill` container of the egress design's decision 61. These supply the clone it warms, the warm list, store, slot repository, capture copy, and restore completion it reuses, the staged checkout, the fill, and the backstop under a checkout. Its staging part, `prestage: true` on a tenant's `gitRepository` assets, staging on the owned pool, and the removal of a tree staged from a withheld store, needs the warm-up checkout of [0153.S8](../0153-workspace-materialization/steps.md#steps) and lands with it or after it, with the `integration`-tier test of the warm-up checkout on an owned pool under "Tests" in `change.md`. The rest of the step lands first. This step adds no field to a default clone, so it can land after 0147.S7 with no format change to the clone. Under decision 8 of [0117](../0117-gateway-package-layout/decisions.md), this step edits `spec/interfaces/gateway-seams.md` and `pkg/gateway/seam` for the gateway seam operations it changes. It reshapes `GWSEAM-MaterializeChild`, whose claim carries the child's `coveredStores` and `withheldStores`, and adds one operation served by the session core that runs the read checks on the target pool's warm list for a delegated child. The session row's `coveredStores` lands with this step or with the platform chunk sets of [`0131-incremental-checkpoint`](../0131-incremental-checkpoint/change.md#platform-chunk-sets), whichever is first.

### 0147.S9 Capacity reservation
- status: Planned
- aliases: [DR-10]
- lanes: [specification, code, documentation]
- depends-on: [0147.S5, 0136.S2]

The Helm values, allowance, `reservedPods`, warm target, split quota, the reserved `PriorityClass` and its quota scope, budget figure, reporting, and alert.

It needs 0147.S5 for the owned pool and its budget, and [0136.S2](../0136-placement/steps.md#steps) for the priority-class scheme in which `lenny-reserved` and `placement.priorityClasses.reserved` sit. It renders no node placement for reserved pods: the placement design's reserved overlay lands in [0136.S5](../0136-placement/steps.md#steps), which depends on this step and applies the overlay to marked pods.

### 0147.S10 Base lifecycle
- status: Planned
- aliases: [DR-6]
- lanes: [specification, code, documentation]
- depends-on: [0147.S4, 0147.S5]
- closes: [F-15.1.75]

The live merge against base and grant, `baseStatus`, the outbox with its leader job, retirement with its cancel route, emergency, and the deletion rule. Closes DF-8.

It needs 0147.S4 and 0147.S5 for the merge and pools it acts on. The retirement it adds is the one that the content stop check of [`0144-session-derive`, "Stops"](../0144-session-derive/change.md#stops) reads.

### 0147.S11 Oversight and records
- status: Planned
- aliases: [DR-7]
- lanes: [specification, code, documentation]
- depends-on: [0147.S4, 0142.S1, 0142.S2, 0144.S1 (part)]

The guardrail, posture, and events.

It needs 0147.S4, [SO-1](../0142-security-oversight/change.md#so-1-security-roles) (0142.S1), and [SO-2](../0142-security-oversight/change.md#so-2-tenant-security-record-and-posture) (0142.S2). These supply the role and the record. This step lands after 0142.S2, so it raises the security counter of `content_stop_epochs`, in the table that [0144.S1](../0144-session-derive/steps.md#steps) adds, in the transaction that activates a security record version adding `all`, `setupCommands`, `workspaceFiles`, `gitClones`, or `floatingCloneRefs` to `derivedRuntimes.forbid`, as [`0144-session-derive`, "Stops"](../0144-session-derive/change.md#stops) states; that write lands with or after 0144.S1. If the owner chooses option (a) of [`0142-security-oversight` decision 41](../0142-security-oversight/decisions.md), which splits 0142.S2, the edge on 0142.S2 moves to the parts this step reads: the record, the guardrail step, and the posture step.

### 0147.S12 Interactions
- status: Planned
- aliases: [DR-8]
- lanes: [specification, code, documentation]
- depends-on: [0147.S4, 0149.S1, 0149.S3, 0141.S1, 0141.S2]

Environment warnings, the checks at the post-chain point on the final row, delegation resolution, and route admission on the tenant view under DR-8's "Runtime routes".

It needs 0147.S4, [0149.S1](../0149-tenant-scoping-identity/change.md#ts-1-environment-scoping) (TS-1), and [0149.S3](../0149-tenant-scoping-identity/change.md#ts-3-session-environment-context-and-cross-environment-sharing) (TS-3) for the checks on the final row, and the first proposal of [`0141-runtime-routing`](../0141-runtime-routing/steps.md#steps) (0141.S1) for the post-chain point where they run. It needs 0141.S2 for route admission, which reads the access rows of 0147.S1 until this step moves it onto the tenant view.

### 0147.S13 Working directory start check
- status: Planned
- aliases: [DR-12 (part)]
- lanes: [specification, code, documentation]
- depends-on: [0147.S6, 0118.S4, 0145.S2]

The rest of DR-12: the start check on `workingDirectory` and on each command's `cwd` through the slot's broker, before setup and again before the runtime starts, with `SESSION_CREATION_FAILED` and `working_directory_invalid`, the slot container's working directory at `/workspace`, the constant `/workspace` in the session row's `cwd` and the resume driver's `expected_workspace_root`, and the resume fallback with `workspace.working_directory_fallback`.

It needs 0147.S6 for the fields, the runtime-visible layout of [PL-3](../0118-agent-pod-layout/change.md#pl-3-runtime-visible-filesystem-layout) ([0118.S4](../0118-agent-pod-layout/steps.md#steps)), and the broker of [SI-5](../0145-session-isolation/change.md#si-5-the-supervisor-file-descriptor-broker) ([0145.S2](../0145-session-isolation/steps.md#steps)). Today's slot working directory is `/workspace/slots/{sessionId}/current/` (`pkg/adapter/slotlayout/slotlayout.go`).

### Notes on the steps

`lenny.dev/served` with `ownerTenantId` in [EG-9](../0127-egress-and-git/steps.md#steps) (0127.S9) lands early, because a stored format, a wire contract, or a shipped enforcement point would otherwise need rework.

The way [`0149-tenant-scoping-identity` decision 15](../0149-tenant-scoping-identity/decisions.md#decisions) gives needs 0147.S1, 0147.S2, 0147.S4 with neither default flag, 0147.S5, and [TS-1](../0149-tenant-scoping-identity/change.md#ts-1-environment-scoping) (0149.S1). It does not need 0147.S3. A platform-created derived runtime needs 0147.S2 alone, so that way has an operator's version before the rest lands. No `EG-`, `TS-`, or `SO-` proposal depends on a DR step, apart from the requests under "Changes requested of other design documents".
