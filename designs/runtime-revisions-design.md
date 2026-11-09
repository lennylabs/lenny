# Runtime revisions

Status: design for owner review, revision 3, 2026-11-09. A cross-design sync review ran on 2026-11-08, and revision 3 applies its findings. Nothing here is built or staged. It reads against [`runtime-routing-design.md`](runtime-routing-design.md) revision 7, [`tenant-derived-runtimes-design.md`](tenant-derived-runtimes-design.md) revision 60, [`session-derive-design.md`](session-derive-design.md) revision 12, [`egress-and-git-design.md`](egress-and-git-design.md) revision 72, [`forensics-evidence-preservation-design.md`](forensics-evidence-preservation-design.md) revision 28, and [`incremental-checkpoint-design.md`](incremental-checkpoint-design.md) revision 6. "Decisions" lists what the owner has settled and what remains open. It stages no specification edit. The changes go through `change-proposal` after review. Proposals carry the label RV-n, and requirements carry the label RVR-n. "Review history" at the end records the revisions.

Each factual claim about behaviour carries one of these tags where the source matters:

| Tag | Meaning |
| :-- | :-- |
| **[source]** | Read in the Lenny tree or the specification on 2026-11-08. |
| **[design]** | What this design requires. Nothing implements it. |

## Problem

"Terms", after "Scope", defines the vocabulary this document uses.

An operator who ships a new version of a runtime wants clients to keep asking for the same runtime name while new sessions move to the new version gradually, with the option to stop and go back. Lenny has no mechanism that does this for the whole runtime:

- [§10.5](../spec/10_gateway-internals.md#105-upgrade-and-rollback-strategy) defines a `RuntimeUpgrade` state machine with a canary split, but it changes only a pool's image, and the session creation path never reads it ("Current state").
- Every other field of a runtime, such as its command, environment, or capabilities, changes in place. The change reaches every pool of the runtime at once, and under [`tenant-derived-runtimes-design.md`](tenant-derived-runtimes-design.md) every tenant's derived runtime as well.
- Versioning by name, as `claude-worker-v2` beside `claude-worker`, is not transparent. Clients must change the name they ask for, every tenant needs a new access row, and every derived runtime still names the old base in `baseRuntime`.

This design adds revisions to runtimes. A revision is an immutable copy of the fields that define what runs in a runtime's pods. The runtime keeps its name, holds a weight per active revision, and the gateway places each new session on a revision by those weights. A rollout moves the weights and retires the old revision's pods.

## Scope

**Added:**

- Immutable runtime revisions for the fields that define a runtime's pods ("RV-1").
- Revision selection at session creation, and `runtimeRevision` on the session record, `session_start`, billing events, and session metrics ("RV-2").
- Pods per pool and revision, with the warm count split by weight ("RV-3").
- A rollout procedure at the runtime level that replaces the pool-level `RuntimeUpgrade` ("RV-4").
- Base revisions for derived runtimes ("RV-5").
- Revision rules for resume, retry, delegation, and derive ("RV-6").

**Changed:**

- The pod builder reads the image, command, and environment from the revision rather than from the live `Runtime` resource.
- §5.1's statement that `image` is immutable through the API. A change to `image` creates a revision.
- `type` and `executionMode`, which §5.1 lets an API write change today, become fixed. A different value needs a new runtime.
- Checks at session creation that read a versioned field read it from the chosen revision, at the post-chain point of [`runtime-routing-design.md`](runtime-routing-design.md#rt-2-the-interceptor-contract).
- The warm list of a pool, its hosts, and the readable set of a pod before binding are kept per pool and revision.

**Kept:**

- The runtime name, access rows, the tenant view, `baseRuntime` references, and pools as the unit an operator configures.
- Runtime routes and the `PreRoute` chain of [`runtime-routing-design.md`](runtime-routing-design.md), which choose a runtime before this design chooses a revision.

**Out of scope:**

- Choosing a revision by name in a request (D2).
- Statistical comparison of revisions. A runtime tags its traces with `runtimeRevision`, and the comparison happens in the deployer's eval platform.

## Terms

| Term | Meaning |
| :-- | :-- |
| Revision | An immutable, numbered copy of a runtime's versioned fields, written `claude-worker` revision 7. Revision numbers increase by one per runtime. |
| Versioned fields | The runtime fields that define what runs in a pod. A change to any of them creates a revision. "RV-1" lists them. |
| Live fields | The runtime fields the gateway enforces or publishes. A change to any of them applies to new sessions at once and creates no revision. |
| Write | Any operation that creates or changes a runtime record: `POST /v1/admin/runtimes`, `PUT /v1/admin/runtimes/{name}`, an apply of a `Runtime` custom resource that the RuntimeReconciler mirrors, and the bootstrap upsert of `lenny-ctl bootstrap` with `--force-update`. |
| Traffic | The runtime's list of active revisions and their integer weights. A runtime with no rollout in progress has one active revision at full weight. |
| Rollout | The procedure that moves traffic from one revision to another and retires the old revision's pods. "RV-4" defines it. |
| Rollout policy | The runtime's `rollout.policy` field, which states how a new revision reaches traffic: `automatic`, `manual`, or `immediate`. "RV-1" defines each. |
| Rollout settings | The runtime's `rollout` object: `policy`, `key`, `canaryPercent`, `drainTimeoutSeconds`, and `ownedPoolWarmTimeoutSeconds`. "Rollout settings" under "RV-1" defines each. |
| Post-chain point | The step at session creation, after the `PreRoute` chain has fixed the runtime and pool and after this design has chosen the revision, where the gateway runs every check that depends on the runtime, the revision, or the pool. [`runtime-routing-design.md`](runtime-routing-design.md#rt-2-the-interceptor-contract) defines it as RT-2's post-chain checks. |
| Promote | The operator action that gives a revision the full weight at once, without waiting for its pods to warm. A write can request it with `?rollout=immediate`. |
| Retention | The platform-level period after which an unreferenced revision record is deleted, 365 days by default. |
| Rollout key | The value the gateway hashes to choose a revision: the user ID, the root session ID, or a random value, set by the runtime's `rollout.key`. |
| Pod set | The warm pods of one pool built from one revision. A pool has one pod set outside a rollout and one per active revision during a rollout. |

### Terms from other designs and the platform

| Term | Meaning |
| :-- | :-- |
| Runtime | A registered agent image and its configuration ([§5.1](../spec/05_runtime-registry-and-pool-model.md#51-runtime)). A runtime is registered either through a `Runtime` custom resource, which the RuntimeReconciler validates and mirrors into the gateway's runtime registry, or through the admin API, which writes the registry directly. |
| Derived runtime | A runtime that names another in `baseRuntime` and changes some fields under §5.1's merge rules. A derived runtime never carries its own `image`, `command`, or `env`, but it can override or append to other fields that this design versions, such as `credentialCapabilities` and `sharedAssets`. |
| Tenant-derived runtime | A derived runtime that a tenant creates, which [`tenant-derived-runtimes-design.md`](tenant-derived-runtimes-design.md) adds, together with an owned pool on it (DR-5) and the notice to tenants when a base changes (DR-6). |
| Owned pool and the tenant's charge | The one pool of a tenant-derived runtime, copied from a platform template pool (DR-5). The tenant's charge is the pod figure checked against the grant's `maxOwnedPods` budget, which DR-5 "Bounds" defines, and a reservation (DR-10) is a floor of pods held for an owned pool within the deployment's `reservations.totalPods`. |
| Warm list | The list per pool of repository stores that the pool's pods fill before they report ready, which [`egress-and-git-design.md`](egress-and-git-design.md) defines in its "Terms". It includes the `gitRepository` entries of the runtime's `sharedAssets`. |
| Quarantine | The forensics state of one agent pod, carried by the `lenny.dev/quarantine` label, in which the pod and its Sandbox stay until a recorded release ([`forensics-evidence-preservation-design.md`](forensics-evidence-preservation-design.md), FE-1). |
| Pool, `SandboxTemplate`, and `SandboxWarmPool` | A pool is a set of pre-warmed pods for one runtime at one isolation profile and resource class ([§5.2](../spec/05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes)). `SandboxTemplate` and `SandboxWarmPool` are the custom resources that describe a pool's pods and its warm count. |
| PoolScalingController | The controller that sets each pool's warm count (`minWarm`) from its demand history ([§4.6.2](../spec/04_system-components.md#462-poolscalingcontroller-pool-configuration)). |
| `RuntimeUpgrade` | The §10.5 state machine for replacing a pool's image, with the phases `Pending`, `Expanding`, `Draining`, `Contracting`, and `Complete`, a `Paused` state, `canaryPercent`, and rollback. |
| `PreRoute` chain | The interceptor phase that chooses a session's runtime and pool, including the route interceptor that applies runtime routes, as [`runtime-routing-design.md`](runtime-routing-design.md) defines it. |
| Routing labels | The `routing` map on a session that records the `PreRoute` chain's decisions ([`runtime-routing-design.md`](runtime-routing-design.md)). |
| Root session | The top-level session of a delegation tree. A derived session is a new root. |
| `session_start` | The first frame the adapter sends a runtime for a session ([§28.5.3](../spec/28_communication-channels.md#2853-intra-pod)). |
| Resume | The reattachment of a session to a new pod from its checkpoint after a pod loss or a suspension ([§7.3](../spec/07_session-lifecycle.md#73-retry-and-resume)). |
| Derive | A new session that starts from an earlier session's snapshot ([`session-derive-design.md`](session-derive-design.md)). |
| `platform-admin` and `tenant-admin` | Lenny's administrative roles for the whole platform and for one tenant. |
| Test tiers | The stages of the test suite that `.claude/rules/test-coverage.md` defines: 0 static checks, 1 unit, 2 component, 3 contract, 4 integration on the compose stack, 5 end to end on Kind, 7a local concurrency, 8 chaos, 9 security, and 11 documentation. |

### Labels from other designs

| Labels | Source | What they name |
| :-- | :-- | :-- |
| RT-2, RT-3, and RT-4 | [`runtime-routing-design.md`](runtime-routing-design.md) | The `PreRoute` contract and the post-chain point, routing labels, and runtime routes with their bucketing function. |
| DR-2 | [`tenant-derived-runtimes-design.md`](tenant-derived-runtimes-design.md) | A prerequisite that adds `SandboxTemplate.spec.podRuntimeRef`, the base runtime whose `Runtime` resource an owned pool's pods are built from. |
| DR-5 | [`tenant-derived-runtimes-design.md`](tenant-derived-runtimes-design.md#dr-5-owned-pools) | Owned pools, their template copy, and their "Bounds": the tenant's charge against `maxOwnedPods` and the rule that creates no pod past it. |
| DR-6 | [`tenant-derived-runtimes-design.md`](tenant-derived-runtimes-design.md#dr-6-base-changes) | Base changes: a platform write to a base always succeeds, a tenant-derived runtime it breaks reads as `incompatible`, and `runtime.base_changed` notifies the tenant. |
| DR-10 and decision 35 | [`tenant-derived-runtimes-design.md`](tenant-derived-runtimes-design.md#dr-10-capacity-reservation) | Capacity reservations for owned pools, and the decision that a pod failed by a platform-declared cause does not count in the tenant's charge. |
| SD-1 | [`session-derive-design.md`](session-derive-design.md) | The derive route and its checks on the target runtime. |
| FE-1 | [`forensics-evidence-preservation-design.md`](forensics-evidence-preservation-design.md#fe-1-quarantine) | Quarantine of an agent pod and the retirement paths that skip a quarantined pod. |

## High-level design

A runtime today is one mutable record. Changing its image or command changes what the next pod runs, for every pool and every tenant at once, and no switch moves only some sessions onto the new version. This design keeps the runtime's name and record, and gives the part of the record that defines the pod a history. Each change to that part creates a numbered revision. The runtime holds a weight for each revision in use, and the gateway places each new session on one of them by a stable hash, so an operator can send 10% of sessions to revision 7, watch, and then move the rest or move back. Clients keep asking for `claude-worker` and never see the rollout, apart from the revision number their session reports.

Fields the gateway enforces, such as setup command policy and limits, stay outside revisions and change at once, so a security tightening never waits for a rollout.

The rest of this section states each part in the design's terms, which "Terms" above defines.

**Revisions (RV-1).** A write that changes a versioned field creates the next revision. Live fields change in place. The runtime's `rollout` policy decides whether the new revision reaches traffic once its pods are warm, waits for an operator, or takes every new session at once. A write or an operator can request the last of these for one urgent change. Revisions are kept until a platform-wide retention period passes and nothing references them.

**Selection (RV-2).** After the `PreRoute` chain has fixed the runtime and pool, the gateway hashes the rollout key over the runtime's traffic to choose a revision. The post-chain point then runs every check that depends on the runtime, the revision, or the pool, and a check that reads a versioned field reads the chosen revision. The session records `runtimeRevision`, and `session_start`, billing events, and session metrics carry it.

**Pods (RV-3).** During a rollout each pool keeps a pod set per active revision. The PoolScalingController splits the pool's warm count by weight. The pod builder reads the image, command, and environment from the revision, and the warm list comes from the revision's `sharedAssets`. A rollout's extra pods on a tenant's owned pool do not count against the tenant's budget.

**Rollout (RV-4).** The `RuntimeUpgrade` phases move from a pool to a runtime and act on traffic: warm the new revision, shift weight, stop new sessions on the old revision, wait for its sessions, and delete its pod sets.

**Derived runtimes (RV-5).** A derived runtime has no revisions of its own. A session on it uses a revision of the base chosen by the base's traffic, with the derived runtime's fields merged on top. A narrowing that the derived runtime declares has to hold against every revision in the base's traffic.

**Sessions over time (RV-6).** A resumed or retried session stays on its revision while that revision has pods. A delegated child and a derive choose a revision as a create does.

```
 Write to runtime claude-worker (API, custom resource, or bootstrap)
        │
        ├── live fields changed ──> apply at once
        └── versioned fields changed ──> revision 7
                                          │
               rollout policy: automatic ─┐   manual ─┐   immediate ─┐
                                          ▼           ▼              ▼
                    warm rev 7, then 100%   rev 7 at weight 0,   rev 7 at 100% now,
                                            operator shifts      cold starts until
                                            weight               its pods warm

 Session creation
        │
        ▼
 PreRoute chain, including the route interceptor (runtime-routing-design.md)
        │   runtime: claude-worker, pool: claude-worker-sandboxed-medium
        ▼
 Revision selection: HMAC("revision/" + runtime name, rollout key) over traffic
        │   traffic: rev 6 weight 90, rev 7 weight 10
        ▼
 Post-chain point: every runtime, revision, and pool check, on the chosen revision
        │
        ▼
 Claim from the pool's pod set for the chosen revision
        │
        ▼
 Session record: runtime, runtimeRevision, pool, routing labels
```

"Limits" states what the change costs: a pod set per active revision during a rollout, more pod sets for owned pools outside their tenants' budgets, and a resumed session that can change revision once its revision is retired.

## Decisions

Settled by the owner on 2026-11-08:

| ID | Decision |
| :-- | :-- |
| D1 | Operator rollouts of a new version of one runtime are designed here, separately from [`runtime-routing-design.md`](runtime-routing-design.md), which adds no routes across tenants. |
| D2 | No caller can choose a revision. Every session receives the revision the traffic selects. |
| D3 | `automatic` is the default rollout policy. A change that must reach the next session uses `immediate`, either as the runtime's policy, as `?rollout=immediate` on a write, or as an operator's promote. "RV-1" states the policies (formerly O1). |
| D4 | Every revision is kept until a platform-level retention period passes, 365 days by default, and is then deleted only when no traffic or live session references it (formerly O3). |
| D5 | Derived runtimes have no revisions of their own and follow their base's revisions (formerly O4). A derived runtime's overrides merge over the selected base revision, so the base's traffic alone decides which binary a session runs. |
| D6 | The default rollout key is `user`, with the root session ID for an anonymous session (formerly O5). |
| D7 | A resume whose revision has been retired runs on the revision with the highest weight, and the change is recorded on the session and in an audit event (formerly O6). |

Settled by the owner on 2026-11-09, as Q1 of the cross-design sync review:

| ID | Decision |
| :-- | :-- |
| D8 | While a platform rollout is in progress, the rollout's extra pod set on an owned pool is exempt from the tenant's `maxOwnedPods`, its warm target, and `reservations.totalPods`, as decision 35 of [`tenant-derived-runtimes-design.md`](tenant-derived-runtimes-design.md#decisions) exempts pods that a platform cause failed. Under `automatic`, the rollout waits for each owned pool's new pod set with a timeout, and then moves traffic with cold starts on that pool. In the owner's word, "ok". |

Open:

| ID | Question | Status |
| :-- | :-- | :-- |
| O2 | Which fields are versioned? | "RV-1" proposes a classification and the rule behind it. `runtimeOptionsSchema` has no class and waits for a review of where the gateway reads it. The owner asked for more context before deciding, which "Field classes" gives. |

## Requirements

| ID | Requirement |
| :-- | :-- |
| RVR-1 | A change to a versioned field never changes the pods of a revision that already exists. |
| RVR-2 | A client asks for a runtime by name alone, and a rollout changes no request or response field apart from the reported revision. |
| RVR-3 | During a rollout, new sessions reach each active revision in proportion to its weight, and the same rollout key reaches the same revision while the traffic is unchanged. |
| RVR-4 | A rollout can be paused and rolled back until the old revision's pod sets are deleted. |
| RVR-5 | A change to a live field applies to the next session on every revision. |
| RVR-6 | A session on a derived runtime runs on a base revision chosen by the base's traffic. |
| RVR-7 | A resumed or retried session keeps its revision while that revision has pods. |
| RVR-8 | The session record, `session_start`, billing events, and session metrics carry the revision. |
| RVR-9 | A check at session creation that reads a versioned field reads the chosen revision. A check at a write or at pool admission that reads a versioned field holds for every revision in the runtime's traffic. |
| RVR-10 | A rollout never deletes a quarantined pod or its Sandbox. |

## Current state

**Registration [source].** §5.1 registers a runtime through a `Runtime` custom resource or the admin API. The `Runtime` resource carries `image`, `command`, `env`, `capabilities`, `credentialCapabilities`, `sharedAssets`, the policy fields, and others (`pkg/apis/lenny/v1alpha1/runtime_types.go`). `applyCRDFields` in `pkg/controller/runtime/controller.go` mirrors most of them into the registry and notes that `command` and `env` are read by the pod builder from the live resource.

**Pods read the live resource [source].** The sandbox controller builds each pod with `RuntimeImage: rt.Spec.Image` from the `Runtime` resource it reads at pod creation (`pkg/controller/sandbox/controller.go`). `SandboxTemplate` carries `runtimeRef` and no image. No code was found that replaces warm pods whose image differs from the resource, so an edit to the resource's `image` produces a pool of mixed images until the old pods are recycled ("Validation owed").

**API mutability [source].** §5.1 states that `image` and `name` are immutable through the API and that every other field is mutable, with changes that would invalidate a derived runtime refused. A change through the API applies to every new session at once.

**`RuntimeUpgrade` [source].** §10.5 runs the state machine per pool with `--new-image <digest>` and `canaryPercent`. `pkg/gateway/upgrade/runtimeupgrade` delivers the durable store, the admin endpoints under `/v1/admin/pools/{name}/upgrade-*`, the CLI, and the metrics. Its package comment states that the Kubernetes side "remains a follow-on". No code under `pkg/gateway/sessionserver` reads the upgrade state, so `canaryPercent` places no session. A pool-level image also has no field to live in, because `SandboxTemplate` has no image.

**Derived runtimes [source].** A derived runtime inherits `image`, `command`, `env`, `integrationLevel`, and `requireSoPeercred` from its base and may not set them, and it may not set `type`, `executionMode`, `isolationProfile`, `allowedResourceClasses`, `capabilities.interaction`, or `capabilities.injection` (§5.1 "Inheritance Rules" and "Normative Merge Algorithm"). It may override `credentialCapabilities`, `runtimeOptionsSchema`, and `capabilityInferenceMode`, and append to `sharedAssets` and `workspaceDefaults`. [DR-6](tenant-derived-runtimes-design.md#dr-6-base-changes) lets a platform write to a base succeed whatever tenant-derived runtimes exist and notifies the affected tenants. §5.1's refusal of a base write that would invalidate a derived runtime stays for platform-owned derived runtimes.

**Readers of versioned fields at session creation [source, from the sync review].** Several checks read fields that this design versions from the runtime row: connector authorization reads `capabilities.deferredToolDiscovery`, the export refusal of DR-8 and session derive's supervisor refusal read `deploymentModel` and `type`, the grant conditions of tenant-derived runtimes read `requireSoPeercred`, and pool admission reads `credentialCapabilities.proxyDialect`. "RV-2" states where each reads once revisions exist.

## RV-1 Revisions

### Field classes

A runtime's fields fall into three groups by what they describe:

- **What runs in the pod:** the image, the process command and environment, the files placed in the pod at warm time, the pod's isolation, and the files and commands a session's plan starts from. Two revisions differ by these.
- **What the binary can do:** fields that describe the program inside the image, which the gateway reads to decide how to treat a session, such as whether it accepts injected messages (`capabilities`), which LLM dialects it speaks (`credentialCapabilities.proxyDialect`), how much of the adapter protocol it implements (`integrationLevel`), whether it can restart (`restartable`), and which session options it accepts (`runtimeOptionsSchema`). A new binary can change any of them, so they travel with the image.
- **What the platform enforces or publishes:** policy the gateway applies regardless of the binary, such as which setup commands a session may run, session limits, which delegation policy applies, how tools are classified, how often a session is checkpointed, and the discovery description.

The first two groups are versioned, and the gateway reads them from the session's revision. The third group is live, so a security tightening reaches the next session on every revision without a rollout.

| Class | Fields | On change |
| :-- | :-- | :-- |
| Versioned | `image`, `command`, `env`, `deploymentModel`, `isolationProfile`, `sharedAssets`, `sdkWarmBlockingPaths`, `workspaceDefaults`, `workspaceTier`, `integrationLevel`, `requireSoPeercred`, `restartable`, `minPlatformVersion`, `checkpointExcludes`, `capabilities`, and `credentialCapabilities` | A new revision. |
| Live | `setupCommandPolicy`, `setupPolicy`, `limits`, `sessionPolicy`, `archivePolicy`, `delegationPolicyRef`, `allowSelfRecursion`, `supportedProviders`, `allowedResourceClasses`, `defaultPoolConfig`, `capabilityInferenceMode`, `toolCapabilityOverrides`, `checkpointPolicy`, `periodicCheckpointIntervalSeconds`, `agentInterface`, `publishedMetadata`, `labels`, and `rollout` | Applied to the next session on every revision. |
| Pending review (O2) | `runtimeOptionsSchema`, which the `Runtime` resource spells `runtimeOptionsSchemaRef` | Unclassified. See below. |
| Fixed | `name`, `type`, `executionMode`, and `baseRuntime` | Refused. A different value needs a new runtime. |

Notes on the classification:

- `workspaceTier` sets the data classification of the pods and, at `T4`, forbids cross-tenant pod reuse and requires dedicated nodes. A pod built under one tier cannot become another, so it is versioned. A tightening such as `T3` to `T4` uses `immediate`.
- `isolationProfile` selects the pod's runtime class, which a running pod cannot change, so it is versioned like `workspaceTier`, and a tightening uses `immediate`.
- `workspaceDefaults` holds the files and setup commands a session's plan starts from. A new binary often needs different setup commands, so they travel with it. The plan is composed at the post-chain point from the chosen revision.
- `sdkWarmBlockingPaths`, `restartable`, and `checkpointExcludes` describe the binary's warm behaviour, its restart support, and the regenerable directories it writes, such as `node_modules/`. `minPlatformVersion` states what the binary needs from Lenny, and a write that creates a revision checks it.
- `checkpointPolicy` and `periodicCheckpointIntervalSeconds` are policy that [`incremental-checkpoint-design.md`](incremental-checkpoint-design.md) lets a runtime and a pool each set, with the stricter policy and the pool's interval winning. They are live.
- `capabilityInferenceMode` and `toolCapabilityOverrides` decide the capability the gateway assigns each MCP tool, which environment filters enforce, so they are live.
- `archivePolicy` governs how the gateway extracts uploaded archives, so it is live.
- `type` and `executionMode` change how the gateway dispatches every session and which pools can serve the runtime, so a revision cannot carry them. Making them fixed is a change from §5.1, which lets an API write change them today.
- A check at a write or at pool admission that reads a versioned field holds for every revision in the runtime's traffic, because the pool serves all of them. A pool with `deliveryMode: proxy` requires `credentialCapabilities.proxyDialect`, so admission checks every revision in the traffic, and a write that creates a revision without it is refused while such a pool references the runtime. A check at session creation reads the chosen revision at the post-chain point ("RV-2").
- `runtimeOptionsSchema` is the hard case. It describes the options the binary accepts, which argues for versioned. The gateway validates a session's `runtimeOptions` at create. With the post-chain point after revision selection, validation can read the chosen revision. The review decides among three outcomes: live, with the operator keeping the schema compatible with every running binary; versioned, with validation at the post-chain point against the chosen revision; or versioned, with validation against every revision in the traffic.
- `sharedAssets` entries that reference an artifact are pinned to the artifact's content digest in the revision, so a later change to the artifact behind the same reference does not change an existing revision.
- A live field that the platform already validates against derived runtimes keeps that validation.

### Rollout settings

The `rollout` object is live, and only a `platform-admin` writes it on a platform runtime.

| Field | Meaning | Default |
| :-- | :-- | :-- |
| `policy` | `automatic`, `manual`, or `immediate`, as "A write" states. | `automatic` |
| `key` | `user`, `session`, or `random`, as "RV-2" states. | `user` |
| `canaryPercent` | The weight a `manual` rollout gives the new revision when the operator first proceeds, and the share of the warm target its pod sets warm to in `Pending`. Unset, the operator sets each weight with `set-weight`. `automatic` ignores it. | Unset |
| `drainTimeoutSeconds` | How long `Draining` waits for the old revision's sessions, after which the remaining ones end with a checkpoint, as §10.5 states for the pool-level procedure today. | The runtime's `maxSessionAge` |
| `ownedPoolWarmTimeoutSeconds` | How long `automatic` waits for an owned pool's new pod set to warm before it moves traffic with cold starts on that pool (D8). | 600 |

### A write

A write is any of the operations "Terms" lists. Each one computes a hash over the versioned fields:

- The first write of a runtime creates revision 1 at full weight.
- A write whose hash equals the latest revision's creates no revision. A `PUT` that changes only live fields, a re-apply of an unchanged custom resource, and a bootstrap upsert with no change all fall here.
- A write whose hash differs creates the next revision, records the writer and the time, and starts a rollout under the runtime's `rollout.policy`:
  - `automatic` (default, D3): the new revision enters at weight 0, its pod sets warm to their target, and traffic then moves to it in one step. An owned pool whose new pod set is not warm after `rollout.ownedPoolWarmTimeoutSeconds` no longer holds the move, and that pool's sessions on the new revision wait on pod creation until its pod set warms (D8). An operator can still pause or roll back.
  - `manual`: the new revision enters at weight 0 and waits for an operator to drive "RV-4".
  - `immediate`: the new revision takes the full weight at once, and every earlier revision drains. New sessions that arrive before the new pod sets are warm wait on pod creation, as a session does today when a pool has no idle pod. Sessions already running on earlier revisions continue, and an operator who needs them stopped uses the existing force-terminate action.
- **Urgent changes.** A change that must reach the next session, such as an image with a security fix, uses `immediate` without changing the runtime's policy, in one of two ways:
  - `PUT /v1/admin/runtimes/{name}?rollout=immediate`, or `lenny-ctl bootstrap --force-update --rollout immediate`, applies `immediate` to the revision this write creates.
  - `lenny-ctl admin runtimes rollout promote` (`POST /v1/admin/runtimes/{name}/rollout/promote`) gives the newest revision the full weight at once. This serves a change applied through the `Runtime` custom resource, whose apply carries no per-write option.
- A write that creates a revision while a rollout is in progress is refused with `409 ROLLOUT_IN_PROGRESS`, naming the rollout, unless it requests `immediate`. An `immediate` write or a promote ends the rollout in progress: the new revision takes the full weight and every earlier revision drains.

A write through the `Runtime` custom resource follows the same rules. The RuntimeReconciler computes the hash when it mirrors the resource and reports `latestRevision` and the traffic in the resource's status. The rollout's progress is state the gateway owns, so a GitOps tool that applies the resource does not fight the operator's weight changes.

### Record

The registry gains a `runtime_revisions` table keyed on runtime name and revision number. Each row holds the versioned fields, their hash, the writer, and the creation time. A row is never updated. Inline `sharedAssets` content is stored once per content digest in a separate table that rows reference, because most revisions change only the image and would otherwise copy the same files.

`GET /v1/admin/runtimes/{name}` returns the live fields, the latest revision's versioned fields, the traffic, and the rollout state. `GET /v1/admin/runtimes/{name}/revisions` lists revisions with pagination.

### Retention

- Every revision is kept until the retention period passes (D4). The gateway flag `--runtime-revision-retention`, default 365 days, sets the period for the whole platform.
- A daily job deletes revisions older than the period that no traffic entry and no live session references, in batches, and deletes stored asset content no remaining revision references.
- A finished session keeps its `runtimeRevision` number after the revision's record is deleted. `GET` of a deleted revision returns `410 REVISION_EXPIRED`.
- A `platform-admin` can delete one revision early with `DELETE /v1/admin/runtimes/{name}/revisions/{n}` when no traffic entry and no live session references it. This removes a revision whose `env` held a value that should not have been stored.

### Storage and performance

The estimates below are unmeasured and assume Postgres compresses large JSON values, which it does above about 2 KB:

| Case | Revisions per year | Size per revision | Storage per year |
| :-- | --: | --: | --: |
| 100 platform runtimes, one versioned change a week | 5,200 | 2 to 10 KB | 10 to 50 MB |
| 100 platform runtimes, one versioned change a day | 36,500 | 2 to 10 KB | 70 to 370 MB |
| 100 platform runtimes, an image build pushed 20 times a day by CI | 730,000 | 0.5 to 2 KB with assets stored once | 0.4 to 1.5 GB |

- Revisions grow with platform runtimes and their change rate. Derived runtimes, including every tenant-derived runtime, create none (D5), so the count does not grow with tenants. A derived runtime's own fields stay on its runtime row.
- A `Runtime` resource cannot exceed the Kubernetes object size limit of about 1.5 MB, which bounds a single revision written through that path. The admin API applies the same bound.
- Session creation reads only the active revisions, at most a few per runtime, from the gateway's in-memory registry view, which refreshes on change. It never queries the revisions table, so the table's size does not reach the session path.
- The table's indexes are the primary key on runtime name and revision number, and the creation time for the retention job. Listing is paginated.
- "Validation owed" lists a measurement against a generated table of a million revisions.

## RV-2 Selection

Session creation runs in this order, which [RT-2](runtime-routing-design.md#rt-2-the-interceptor-contract) states in the same terms:

1. The `PreRoute` chain, including the route interceptor, produces the final runtime and pool.
2. Revision selection runs on the final runtime, as the steps below state.
3. The post-chain point of RT-2 runs every check that depends on the runtime, the revision, or the pool, on the chosen revision.
4. The gateway claims a pod.

Revision selection:

1. Reads the runtime's traffic from its cached registry view. With one active revision, it takes that revision and skips the hash. For a derived runtime it reads the base's traffic ("RV-5").
2. Picks the rollout key under `rollout.key`: the user ID under `user`, the root session ID under `session` or for an anonymous session under `user`, and a random UUID under `random`.
3. Computes `HMAC-SHA256(key = "revision/" + runtime name, message = rollout key)` with the bucketing function that [RT-4](runtime-routing-design.md#rt-4-runtime-routes) defines, and walks the active revisions in ascending order by cumulative weight.

**Checks on the chosen revision.** Every check at the post-chain point that reads a versioned field reads the chosen revision, merged with the derived runtime's fields where one applies. That covers, among others, `capabilities.deferredToolDiscovery` for the link checks of [`connector-authorization-and-external-memory-design.md`](connector-authorization-and-external-memory-design.md), `deploymentModel` and `type` for the delegation export refusal of DR-8 and the supervisor refusal of [`session-derive-design.md`](session-derive-design.md), `requireSoPeercred` for the grant conditions of tenant-derived runtimes, and `runtimeOptionsSchema` once O2 places it. A check that fails refuses the session as it does today. The gateway does not try another revision, because the session's revision follows its rollout key.

**Claim.** The gateway claims from the pool's pod set for the chosen revision. An empty pod set takes the existing path for a pool with no idle pod, and the gateway does not substitute another revision, because substitution would move the split away from the weights under load.

The session record gains `runtimeRevision`. The session response, `session_start` (as `runtimeRevision`), the adapter manifest's per-session fields, billing events (as `runtime_revision`), and the session metrics `lenny_session_total`, `lenny_session_error_total`, and `lenny_session_duration_seconds` (as the label `runtime_revision`) carry it. Active revisions bound the label's values.

The revision is not written into the routing labels, because the `PreRoute` chain did not choose it.

## RV-3 Pods

- `SandboxTemplate` gains `runtimeRevision`. The warm pool controller keeps one `SandboxWarmPool` and `SandboxTemplate` pair per pool and active revision, labelled with the revision, and deletes a pair when its revision leaves traffic and no session uses it. It never deletes a Sandbox whose pod carries the `lenny.dev/quarantine` label: the pair stays while such a Sandbox exists, and the deletion reports it to the forensics reconciler, as FE-1's "Retirement paths" states for pool deletion (RVR-10).
- The pod builder reads `image`, `command`, `env`, and the other versioned fields from the revision named by the pod's template. It no longer reads them from the live `Runtime` resource, so no edit reaches an existing revision's pods (RVR-1). For an owned pool, the template's `podRuntimeRef` (DR-2) names the base and `runtimeRevision` names the base revision.
- **Warm list per revision.** The warm list comes partly from the revision's `sharedAssets`, so the PoolScalingController renders one warm list per pool and revision, onto that revision's template. The hosts the list adds to a warm pod's reach and the readable set of a pod before binding, which [`egress-and-git-design.md`](egress-and-git-design.md#effective-policy) and "Warm clones" under [`tenant-derived-runtimes-design.md`](tenant-derived-runtimes-design.md#warm-clones) read from the pool row today, key on pool and revision, so a pod warming for revision 7 reaches only revision 7's repositories.
- The PoolScalingController computes the pool's warm target from the pool's demand history, as §4.6.2 states, and splits it across pod sets by weight, with a floor of one pod for an active revision at positive weight. Under `automatic`, a new revision's pod set warms to the full target before traffic moves. The pool's demand history is shared by its revisions, so a new revision has no cold-start gap.
- During a rollout a pool holds up to the sum of its pod sets' targets. Outside a rollout it holds one pod set.
- **Owned pools during a base rollout (D8).** On an owned pool, the pods of every pod set other than the one for the revision with the highest weight, the older revision on a tie, count in neither the tenant's charge nor the creation rule of `maxOwnedPods`, and a pool with a reservation places that extra pod set outside its reserved pods, so `reservations.totalPods` is unchanged. This holds while the platform rollout is in progress, as decision 35 exempts pods failed by a platform cause. The namespace `ResourceQuota` still bounds the extra pods. When the rollout ends, the remaining pod set is the pool's only one and counts as before. A rollout cannot raise what a tenant is charged, and a tenant at its bound still gets the new revision.

## RV-4 Rollout

The §10.5 phases move from a pool to a runtime and act on traffic. Every pool of the runtime moves together.

| Phase | Traffic and pods |
| :-- | :-- |
| `Pending` | The new revision exists at weight 0. Its pod sets warm to the share of the target that `rollout.canaryPercent` sets, or to the full target under `automatic`, with an owned pool waited on for at most `rollout.ownedPoolWarmTimeoutSeconds`. |
| `Expanding` | The new revision's weight rises to `rollout.canaryPercent`, then by operator steps, or to the full weight in one step under `automatic`. |
| `Draining` | The new revision holds the full weight. The old revision receives no new sessions, and its sessions run to completion or until `rollout.drainTimeoutSeconds`, after which the remaining ones end with a checkpoint. |
| `Contracting` | The old revision's pod sets are deleted, apart from any quarantined Sandbox, which stays with its pair until FE-1 releases it (RVR-10). |
| `Complete` | One revision remains active. |
| `Paused` | Weights and pod sets hold. Reachable from every phase except `Complete`. |

- **Rollback.** Until `Contracting`, rollback returns the full weight to the old revision and drains the new one. After `Complete`, an operator rolls back by writing the old revision's versioned fields, which creates a new revision with the old content and starts a rollout.
- **Promote.** A promote moves the rollout from any phase before `Draining` straight to `Draining`, with the newest revision at full weight. It does not wait for the new pod sets to warm.
- **Surface.** `/v1/admin/runtimes/{name}/rollout` replaces `/v1/admin/pools/{name}/upgrade-*`, and `lenny-ctl admin runtimes rollout` with `status`, `proceed`, `set-weight`, `promote`, `pause`, `resume`, and `rollback` replaces `lenny-ctl admin pools upgrade`. Only a `platform-admin` drives a rollout of a platform runtime.
- **Schema migrations.** The `drainFirst` and `schemaVersion` fields of §10.5 move to the runtime rollout unchanged.
- **Events.** Each phase transition emits the existing `upgrade_progressed` event with the runtime, the revisions, and their weights. The `RuntimeUpgrade` alerts and metrics move from pool labels to runtime labels.

## RV-5 Derived runtimes

- **What a derived runtime carries.** §5.1 forbids a derived runtime to set `image`, `command`, `env`, `integrationLevel`, `requireSoPeercred`, `type`, `executionMode`, `isolationProfile`, and the interaction and injection capabilities. It lets one override `credentialCapabilities` and set the other `capabilities` entries, and append to `sharedAssets` and `workspaceDefaults`. Tenant-derived runtimes may narrow `credentialCapabilities` and append `workspaceDefaults` under their grant, and [`incremental-checkpoint-design.md`](incremental-checkpoint-design.md) lets a tenant set `checkpointExcludes` on a derived runtime. A derived runtime therefore carries values for versioned fields, kept on its own runtime row.
- **Merge.** A session on a derived runtime chooses a revision from the base's traffic, using the base's name in the hash, and merges the derived runtime's fields over that revision's fields under §5.1's merge algorithm. An override replaces the revision's value, and an append adds to it. The merged result is what the post-chain point and the pod builder read.
- **Why no derived revisions (D5).** The binary comes from the base, so the base's traffic alone decides which binary a session runs. A derived runtime's own fields change in place, and a write to them applies to the next session on every base revision, as a live field does.
- **Narrowing holds against every base revision.** A restrict-only or narrowing value, such as a narrowed `credentialCapabilities.proxyDialect` or a subset of `supportedProviders`, has to hold against every revision in the base's traffic, because any of them can serve the next session. A write to the derived runtime checks every revision in the traffic, and a new base revision is checked against every derived runtime.
- **Pools.** Pools on a derived runtime, including owned pools under [DR-5](tenant-derived-runtimes-design.md#dr-5-owned-pools), keep a pod set per active base revision during a base rollout, sized as "RV-3" states, with the owned-pool exemption of D8.
- **Base writes and platform-owned derived runtimes.** A base write that creates a revision invalidating a platform-owned derived runtime is refused and names the runtime, as §5.1 refuses such a change today.
- **Base writes and tenant-derived runtimes.** A base write always succeeds under [DR-6](tenant-derived-runtimes-design.md#dr-6-base-changes). A tenant-derived runtime is evaluated against every revision in the base's traffic, the new one included, and reads as `incompatible` while any of them breaks it. New sessions then answer `409 DERIVED_RUNTIME_UNAVAILABLE` until the tenant edits the runtime or the breaking revision leaves the traffic. DR-6's notice fires when the write creates the base revision, and its text names the revision, the rollout policy, and whether the revision makes the runtime `incompatible`. The notice no longer means the change has already reached the tenant's sessions.

## RV-6 Sessions over time

- **Retry and resume.** A retried or resumed session claims from its recorded revision's pod set while that pod set exists. A revision does not leave the registry while a live session references it. A resumable session can outlive `rollout.drainTimeoutSeconds`, and when its pod set is gone, it resumes on the revision with the highest weight, the gateway updates `runtimeRevision`, and it emits `session.runtime_revision_changed` (D7).
- **Delegation.** A delegated child chooses a revision of its own runtime by "RV-2". Under `rollout.key: session`, the key is the root session ID, so a delegation tree stays on one revision of each runtime it uses.
- **Derive.** A derive chooses a revision by "RV-2" like a create. It does not inherit its source's revision (D2).

## Limits

- **Pods during a rollout.** A pool holds a pod set per active revision, so a rollout raises the warm pod count until `Contracting`.
- **Tenant-derived pools multiply the cost.** A base rollout gives every pool on every derived runtime of that base a pod set per active revision. On owned pools the platform carries that cost outside the tenants' budgets (D8), bounded by the namespace `ResourceQuota`.
- **Cold starts on owned pools.** An owned pool whose new pod set does not warm within `rollout.ownedPoolWarmTimeoutSeconds` serves its first sessions on the new revision cold.
- **A breaking base revision stops tenant runtimes during the canary.** A tenant-derived runtime that one revision in the traffic breaks reads as `incompatible` for the whole rollout, including while that revision holds a small weight.
- **No revision choice.** A client cannot send a session, or a derive, to a specific revision (D2). A regression test against a new revision runs once the rollout gives the test's rollout key that revision.
- **Live fields are not staged.** A change to a live field reaches every revision at once. A live-field change that a new binary needs has to stay compatible with the old revision while both run.
- **A retired revision moves resumable sessions.** A session suspended longer than the drain timeout resumes on another revision (D7).
- **One rollout at a time.** A runtime has at most one rollout in progress. A non-urgent change during a canary waits for a rollback or completion, and an `immediate` change ends the canary.
- **Cold starts under `immediate`.** Sessions created before the new pod sets are warm wait for pod creation.
- **Revision history costs storage.** "Storage and performance" estimates it, and a CI pipeline that writes an image on every build produces the largest case.

## Alternatives rejected

- **Finish the pool-level `RuntimeUpgrade` as specified.** It covers only the image, has no field to hold the image in `SandboxTemplate`, and lets pools of one runtime run different images with no runtime-level record.
- **Version by name with aliases.** `claude-worker` as a route to `claude-worker-v1` and `claude-worker-v2` breaks `baseRuntime` references and access rows, and needs routes across tenants.
- **Version every field.** A security tightening of `setupCommandPolicy` or `limits` would wait for a rollout, and a live-field change would warm a new pod set for nothing.
- **Let callers choose a revision.** The owner declined it (D2). It lets a caller step outside the rollout.
- **Substitute another revision when a pod set is empty.** It moves the split away from the weights under load and hides an undersized pod set.
- **Give derived runtimes their own revisions.** The binary comes from the base, so a derived revision would add a second traffic table that cannot change which image a session runs, and every base revision would multiply by every derived revision. A derived runtime's own values merge over the selected base revision instead (D5).
- **Count the rollout's extra pods in the tenant's budget.** A platform rollout would push a tenant at its bound into `QUOTA_EXCEEDED` for a change the tenant did not make (D8).

## Specification changes

The following edits locate their targets by heading:

| Section | Change |
| :-- | :-- |
| [§5.1 Runtime](../spec/05_runtime-registry-and-pool-model.md#51-runtime), opening, "Inheritance Rules", and "Normative Merge Algorithm" | Add revisions, the field classes, the write rule, `rollout`, and the revision record. Make `type` and `executionMode` fixed. Replace "Base runtime mutability" with the field classes. State that a derived runtime's fields merge over the selected base revision, that a narrowing holds against every revision in the base's traffic, and that the refusal of an invalidating base write applies to platform-owned derived runtimes. |
| [§5.2](../spec/05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes) and [§4.6.2](../spec/04_system-components.md#462-poolscalingcontroller-pool-configuration) | Add pod sets per revision, the warm split, the warm list per pool and revision, and pool admission against every revision in the traffic. |
| [§4.6.1](../spec/04_system-components.md#461-warm-pool-controller-pod-lifecycle) | The warm pool controller keeps a template and warm pool pair per pool and active revision, skips a quarantined Sandbox when it deletes a pair, and the pod builder reads the revision. |
| [§4.7.6](../spec/04_system-components.md#476-adapter-manifest-field-reference) and [§28.5.3](../spec/28_communication-channels.md#2853-intra-pod) | Add `runtimeRevision` to the per-session fields and `session_start`. |
| [§7.1 Normal Flow](../spec/07_session-lifecycle.md#71-normal-flow) | Add revision selection after the `PreRoute` chain and before the post-chain point, and state that the post-chain point reads versioned fields from the chosen revision. |
| [§7.3 Retry and Resume](../spec/07_session-lifecycle.md#73-retry-and-resume) | Add the revision rule of "RV-6". |
| [§10.5](../spec/10_gateway-internals.md#105-upgrade-and-rollback-strategy) | Replace the pool-level `RuntimeUpgrade` with the runtime rollout of "RV-4". |
| [§11.2.1 Billing Event Stream](../spec/11_policy-and-controls.md#1121-billing-event-stream) | Add `runtime_revision`. |
| [§12.2](../spec/12_storage-architecture.md#122-storage-roles) | Add the revision table, the asset content table, and the retention rule with its flag. |
| [§15.1 REST API](../spec/15_external-api-surface.md#151-rest-api) | Add the revision, rollout, promote, and revision delete endpoints, `?rollout=immediate` on runtime writes, `runtimeRevision` on session responses, `ROLLOUT_IN_PROGRESS`, and `REVISION_EXPIRED`. Remove the pool upgrade endpoints. |
| [§15.5 API Versioning and Stability](../spec/15_external-api-surface.md#155-api-versioning-and-stability), the `Runtime` resource | Add `rollout` to the spec and `latestRevision` and the traffic to the status. The proposal's verifier confirms that this is where the resource's schema is stated, or finds the section that states it. |
| [§16.1](../spec/16_observability.md#161-metrics), [§16.5](../spec/16_observability.md#165-alerting-rules-and-slos), and [§16.6](../spec/16_observability.md#166-operational-events-catalog) | Add the `runtime_revision` label, `session.runtime_revision_changed`, and the runtime labels on the rollout metrics and alerts. |
| [§24](../spec/24_lenny-ctl-command-reference.md) | Replace `lenny-ctl admin pools upgrade` with `lenny-ctl admin runtimes rollout`. |

## Non-specification changes

| Area | Change |
| :-- | :-- |
| `pkg/gateway/runtime/runtimestore` | The revision table, the hash, traffic, and rollout state. |
| `pkg/controller/runtime` | The hash on mirror, and `latestRevision` and traffic in the resource status. |
| `pkg/apis/lenny/v1alpha1` | `runtimeRevision` on `SandboxTemplate`, the status fields on `Runtime`, and `rollout` on the spec. Regenerate the CRD manifests. |
| `pkg/controller/sandbox` and `podspec` | Read versioned fields from the revision. |
| `pkg/controller/warmpool` and `pkg/controller/poolscaling` | Pod sets per revision, the warm split, the warm list per revision, the owned-pool exemption of D8, and the quarantine skip. |
| `pkg/gateway/sessionserver` post-chain point | The checks that read versioned fields read the chosen revision. |
| `pkg/gateway/upgrade/runtimeupgrade` and `pkg/runtime/upgrade/state` | Runtime-level rollout on traffic. |
| `pkg/gateway/sessionserver` and `pkg/gateway/mcpfabric` | Revision selection, `runtimeRevision` on the record, resume by revision, and billing. |
| `pkg/gateway/externalapi/admin` and `pkg/ctlcli` | The revision and rollout endpoints and commands. |
| `pkg/adapter`, `schemas/lenny-adapter.proto`, and the runtime SDKs | `runtimeRevision`. |
| `migrations` | The revision table, `sessions.runtime_revision`, and the rollout state. |
| `docs/` | An operator guide page on revisions and rollouts, linked from the routing guide of [`runtime-routing-design.md`](runtime-routing-design.md). |

## Tests

| Tier | Coverage |
| :-- | :-- |
| 1 | Field classification and the hash, and a write changing `type` or `executionMode` refused. A live-field write creates no revision. Selection shares and stability per key, and the single-revision shortcut (RVR-3). The resume rule for a retired revision. The retention job keeps referenced revisions and deletes unreferenced ones past the period, and an early delete of a referenced revision is refused. An `immediate` write during a rollout ends it. A derived runtime's override and append merge over the selected base revision, and a narrowing that one revision in the traffic breaks is refused at the derived write and reads as `incompatible` on a base write. The charge of an owned pool during a base rollout leaves out the extra pod set (D8). |
| 2 | Against envtest: a `Runtime` resource change creates a revision and leaves existing pods' templates unchanged (RVR-1), the warm pool controller creates and deletes pod sets per revision, and the resource status reports the traffic. |
| 3 | `session_start` and the manifest carry `runtimeRevision`. The rollout endpoints. |
| 4 | On the compose stack: sessions split by weight across revisions, a delegated child under `rollout.key: session` matches its root, a derive selects by traffic, a resume stays on its revision, billing events and metrics carry it, and a live-field change reaches both revisions at once (RVR-2, RVR-5, RVR-7, RVR-8). A session on a derived runtime runs a base revision (RVR-6). With revision 6 at `deploymentModel: sidecar` and revision 7 at `embedded`, a delegated child with exports passes on revision 6 and is refused on revision 7, and a session on a runtime whose revisions differ in `capabilities.deferredToolDiscovery` takes the link check of its chosen revision (RVR-9). |
| 5, owned pools and forensics | On Kind: a base rollout on a tenant at its `maxOwnedPods` bound warms the new pod set without a `QUOTA_EXCEEDED`, an owned pool that cannot warm moves after `ownedPoolWarmTimeoutSeconds`, a pod warming for one revision reaches only that revision's warm list, and `Contracting` leaves a quarantined Sandbox and its pair in place (RVR-10). |
| 5 | On Kind: an `automatic` rollout warms the new pod set before moving traffic, a `manual` rollout pauses and rolls back with the old pod set intact, `Contracting` deletes the old pod set only after its sessions end (RVR-4), and an `immediate` write or a promote sends the next session to the new revision before its pod set is warm. |
| 7a | Concurrent creates during a weight change keep each key on one revision per traffic version, under `-race`. |
| 8 | A gateway restart during `Expanding` keeps the traffic and the phase. |
| 11 | The rollout guide's commands match the CLI, and the rollout alerts resolve to runbooks. |

Every test carries the `// spec:` annotation, and tests at tier 2 and above carry `// diagnosis:`, as `test-coverage.md` requires.

## Validation owed

| Item | Why it matters | Status |
| :-- | :-- | :-- |
| Confirm that no controller replaces warm pods whose image differs from the `Runtime` resource | "Current state" infers a mixed pool from the absence of such code. | Open. |
| Confirm how a runtime registered only through the admin API reaches the pod builder, which reads the `Runtime` resource | "RV-3" moves the pod builder to the revision, and both registration paths must produce one. | Open. |
| Review where the gateway reads each versioned field, and settle `runtimeOptionsSchema` | O2 and "RV-1" place the fields by reading their descriptions. The sync review found readers of `capabilities.deferredToolDiscovery`, `deploymentModel`, `type`, and `requireSoPeercred` before selection, which "RV-2" moves to the post-chain point. Other readers may exist. | Open, for the owner's review. |
| Confirm that the controller can render a warm list and the pre-bind reach per pool and revision | "RV-3" keys them on the revision, and the egress decision service reads them from the pool row today. | Open. |
| Measure the revisions table at a million rows: insert rate, the retention job's batch time, and listing latency | "Storage and performance" is an estimate. | Open. |
| Confirm that artifact references in `sharedAssets` resolve to a content digest that a revision can pin | "Field classes" pins them. | Open. |
| Confirm that resume claims by the session's recorded pool and can be extended to the revision | "RV-6" relies on it. | Open. |
| Measure the warm pod cost of a base rollout with tenant-derived pools | "Limits" states the cost without a number. | Open. |

## Instructions for reviewers of other designs

- [`runtime-routing-design.md`](runtime-routing-design.md): its reviewer confirms that RT-2 states the order of "RV-2" (the `PreRoute` chain, revision selection, the post-chain point on the chosen revision, then the claim), that `runtimeRevision` stays out of the routing labels, and that the bucketing function is shared.
- [`tenant-derived-runtimes-design.md`](tenant-derived-runtimes-design.md): its reviewer:
  - restates DR-6's notice as firing when a base revision is created, and states that `baseStatus` and `incompatible` evaluate against every revision in the base's traffic;
  - drops the DR-6 rows for `type` and `executionMode`, which become fixed, and states that the rows for `deploymentModel` and `requireSoPeercred` evaluate per revision;
  - states that the grant conditions hold for every revision in the base's traffic;
  - adds pod sets per base revision to DR-5's owned pools, with `podRuntimeRef` (DR-2) naming the base and `runtimeRevision` the base revision;
  - adds to DR-5 "Bounds" and DR-10 the exemption of D8: a base rollout's extra pod set counts in neither the tenant's charge, nor the creation rule of `maxOwnedPods`, nor the pool's reserved pods, while the rollout is in progress;
  - keys the warm list under "Warm clones", its hosts, and the readable set before binding on pool and revision;
  - moves DR-8's delegation export refusal to the post-chain point on the chosen revision's `deploymentModel`;
  - restates DR-6's row for base `workspaceDefaults`, which is versioned, as applying to plans composed from a revision created after the write;
  - replaces the pool-level `upgrade` route and the §10.5 row in "Specification sections affected" with the runtime rollout, and the "Removes the base's `Runtime` resource" row with its effect on every revision in the traffic.
- [`session-derive-design.md`](session-derive-design.md): its reviewer states that a derive selects a revision by traffic and does not inherit its source's revision, and that SD-1's target checks, including the supervisor refusal on `deploymentModel`, run at the post-chain point on the chosen revision.
- [`connector-authorization-and-external-memory-design.md`](connector-authorization-and-external-memory-design.md): its reviewer states that the link checks read `capabilities.deferredToolDiscovery` from the chosen revision at the post-chain point.
- [`egress-and-git-design.md`](egress-and-git-design.md): its reviewer keys the warm list, the warm-list hosts in "Effective policy", and the pre-bind readable set on pool and revision.
- [`forensics-evidence-preservation-design.md`](forensics-evidence-preservation-design.md): its reviewer adds a row to FE-1's "Retirement paths" for a rollout's `Contracting`, which skips a quarantined Sandbox and its pair.
- [`incremental-checkpoint-design.md`](incremental-checkpoint-design.md): its reviewer states that `checkpointPolicy` and `periodicCheckpointIntervalSeconds` are live, that `checkpointExcludes` is versioned, and that a tenant's `checkpointExcludes` on a derived runtime merges over the selected base revision.
- [`billing-resource-attribution-design.md`](billing-resource-attribution-design.md): its reviewer adds `runtime_revision` to the common envelope of `session.pod_usage`.

## Proposal split and order

One proposal, after the second proposal of [`runtime-routing-design.md`](runtime-routing-design.md), whose shared bucketing function it uses:

1. Land the specification edits.
2. Add the revision record and the hash on both registration paths, with every runtime at one revision, and move the pod builder to the revision.
3. Add pod sets per revision, the warm split, and selection.
4. Move the rollout procedure to the runtime and retire the pool-level endpoints.
5. Update the documentation and records.

## Review history

This section records how the design reached its current form. A cross-design sync review ran on 2026-11-08 across every design under `designs/`.

### Revision notes

- Revision 1, dated 2026-11-08, follows the owner's discussion of [`runtime-routing-design.md`](runtime-routing-design.md) O5, which asked for a transparent rollout of a new runtime version. The owner chose a separate design (D1) and declined revision pinning (D2).
- Revision 2, dated 2026-11-08, records the owner's settlement of O1 and O3 to O6 as D3 to D7. It adds the `immediate` policy, the per-write option, and promote for changes that must reach the next session, the retention period with its storage estimate and early delete, and asset content stored once per digest. It explains the field classes, moves `archivePolicy` to the live class, and leaves `runtimeOptionsSchemaRef` unclassified pending the owner's review.
- Revision 3, dated 2026-11-09, applies the sync review and the owner's answer to its question Q1:
  - "RV-2" states the creation order shared with [`runtime-routing-design.md`](runtime-routing-design.md) revision 7: revision selection runs after the `PreRoute` chain and before the post-chain point, which reads versioned fields from the chosen revision (RVR-9). Revision 2 placed selection after the post-chain checks, so checks read versioned fields before a revision existed.
  - "RV-5" corrects revision 2's claim that a derived runtime carries no versioned field. Overrides and appends merge over the selected base revision, a narrowing holds against every revision in the traffic, and D5 stays with that reason. The refusal of an invalidating base write applies to platform-owned derived runtimes only, and a tenant-derived runtime reads as `incompatible` under DR-6.
  - "RV-3" keys the warm list and the pre-bind reach on pool and revision, adds the owned-pool exemption (D8), and keeps quarantined Sandboxes through a rollout (RVR-10).
  - "RV-1" classifies the fields revision 2 left out, uses the API name `runtimeOptionsSchema`, lists `type` and `executionMode` under "Changed", and defines the rollout settings, including `canaryPercent`, `drainTimeoutSeconds`, and `ownedPoolWarmTimeoutSeconds`.
  - The diagram's hash key matches "RV-2", owned pools cite DR-5 in place of DR-2, and the reviewer instructions cover the designs the sync review found affected.
