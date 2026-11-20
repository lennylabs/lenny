# Steps: 0140 Runtime revisions

## Validation owed

| Item | Why it matters | Status |
| :-- | :-- | :-- |
| Confirm that no controller replaces warm pods whose image differs from the `Runtime` resource | "Current state" infers a mixed pool from the absence of such code. | Open. |
| Confirm how a runtime registered only through the admin API reaches the pod builder, which reads the `Runtime` resource | "RV-3" moves the pod builder to the revision, and both registration paths must produce one. | Open. The path is open decision O8, and 0140.S1 waits on it. |
| Review where the gateway reads each versioned field, and settle `runtimeOptionsSchema` | O2 asked it. The sync review found readers of `capabilities.deferredToolDiscovery`, `type`, `requireSoPeercred`, and `deploymentModel` before selection, and "RV-2" moves them to the post-chain point. D20 has since removed `deploymentModel`, and CR-1 of [`0124-connector-removal`](../0124-connector-removal/change.md) has since removed `capabilities.deferredToolDiscovery`. | Settled by D9. "Field classes" places each versioned field by its reader in the tree, read on 2026-11-09. |
| Confirm the placement of `isolationProfile`, `workspaceDefaults`, and `description` | Revision 6 placed `isolationProfile` and `workspaceDefaults` by judgement and kept `description` live, while D9 named descriptions among the gateway-read fields. | Settled for `isolationProfile` by D11, for `workspaceDefaults` by D19, and for `agentInterface` by D12. `description` stays live, and its placement is open decision O7. |
| Confirm that the WarmPoolController can read a runtime's floor for a runtime registered only through the admin API | "Isolation floor" makes the WarmPoolController refuse a template below the floor, and it reads the `Runtime` resource today. | Open. |
| Confirm that every session creation path can raise a session's profile to the floor before pool resolution, including a pinned pool and a derive | "Isolation floor" raises a session that names no profile. | Open. |
| Confirm that each built-in schema in §14 compiles as a standalone file with no `$ref` outside the document | "RV-7" moves them to `schemas/runtime-options/` and compiles every schema at write. | Open. 0140.S2 waits on it. |
| Confirm that the controller can render a warm list and the pre-bind reach per pool and revision | "RV-3" keys them on the revision, and the egress decision service reads them from the pool row today. | Open. |
| Measure the revisions table at a million rows: insert rate, the retention job's batch time, and listing latency | "Storage and performance" is an estimate. | Open. |
| Confirm that artifact references in `sharedAssets` resolve to a content digest that a revision can pin | "Field classes" pins them. | Open. |
| Confirm that resume claims by the session's recorded pool and can be extended to the revision | "RV-6" relies on it. Revision 23 states the extension: the claim takes the recorded revision's pod hash, as [`0137-pod-lifecycle-controller`](../0137-pod-lifecycle-controller/change.md) PLCR-18 states for every claim. | Open. |
| Confirm that the warm-up checkout can take its list of clones from the revision a pod's `PodSet` names, and measure the first-session cost of an incremental checkout when a session's commit differs from the staged one | "Field classes" keeps `workspaceDefaults` gateway-read and relies on the helper adopting, checking out incrementally, or discarding a staged tree at bind (the owner's approval of the warm-up checkout on 2026-11-12, which the workspace materialization design owns). | Open. |
| Measure the warm pod cost of a base rollout with tenant-derived pools | "Limits" states the cost without a number. | Open. |
| Check the AWS prices and measure the warm-up time in "Worked example: an upgrade on AWS" | The example uses approximate list prices and an assumed warm-up time of 5 to 10 minutes. | Open. |

## Steps

### 0140.S1 Runtime revisions
- status: Planned
- aliases: [RV-1, RV-2, RV-3, RV-4, RV-5, RV-6]
- lanes: [specification, code, documentation]
- depends-on: [0140.S2, 0140.S3, 0141.S2, 0139.S1, 0147.S1, 0147.S2 (part), 0147.S5 (part), 0127.S5 (part), 0128.S2 (part), 0118.S4 (part)]
- closes: []

The step lands after 0141.S2 of [`0141-runtime-routing`](../0141-runtime-routing/change.md), whose shared bucketing function it uses, and after 0140.S2 and 0140.S3, so that the revision stores an inline, compiled schema and checks the narrowed override against every revision in the traffic. It runs these tasks in order:

1. Land the specification edits.
2. Add the revision record, the hash, and the pod hash on both registration paths, with every runtime at one revision, the retention job on the gateway's leader gate, and the cache of revisions for reads after creation, and move the pod builder to the revision by the path open decision O8 settles.
3. Add pod sets per pod-set key, the warm split, selection, discovery by revision, `agentInterface` published per caller, and the revision read of the capability checks of RP-2, which 0139.S1 adds.
4. Merge a derived runtime's fields over the selected base revision, and apply the revision rules for resume, retry, delegation, and derive.
5. Move the rollout procedure to the runtime and retire the pool-level endpoints, the `lenny-ops` tools, and the CLI commands.
6. Update the documentation and records.

The edge to 0147.S1 holds the whole step, because DR-1 of [`0147-tenant-derived-runtimes`](../0147-tenant-derived-runtimes/change.md) removes a `tenant-admin`'s writes on a platform runtime, and without it a granted `tenant-admin` could create a revision of a shared runtime and force it to full weight with `?rollout=immediate`. The `(part)` edges hold these parts: `podRuntimeRef` for the pod builder of an owned pool (0147.S2), owned pools and the exemption of D8 (0147.S5), the warm list per pod set (0127.S5), the quarantine skip of RVR-10, which reads `lenny.dev/quarantine` (0128.S2), and the `sizeLimit` of `lenny-assets` from `assetsBytes` under PL-5 (0118.S4). The rest of the step can land first.

The field classes assume that the fields other changes remove have left: `deploymentModel` (0118.S1), `capabilities.preConnect` and `sdkWarmBlockingPaths` (0118.S2), `type` and `executionMode` (0119.S4), `integrationLevel` (0139.S1), `capabilities.deferredToolDiscovery`, `capabilities.toolProgress`, `capabilityInferenceMode`, and `toolCapabilityOverrides` (0124.S1), `credentialCapabilities` and `supportedProviders` (0133.S2), and `workspaceTier` (0150.S3). The step lands after those steps, or drops the rows it would otherwise add for a field still present.

Steps of other changes that land before this step read versioned fields from the runtime record, and this step moves each of those reads onto the session's chosen revision: the list check of LU-2 **Session creation.** in [`0133-llm-upstreams-and-cost-budgets`](../0133-llm-upstreams-and-cost-budgets/change.md#lu-2-assignment-and-resolution), which 0133.S2 makes against the runtime record's `llmFormats`; `injection.modes`, `injection.defaultMode`, `capabilities.output.maxFileBytes`, `harnessApprovals`, and `maxWorkspaceSealDurationSeconds`, which the steps of [`0135-message-delivery`](../0135-message-delivery/change.md) read from the runtime record; and `capabilities.conversationInput` and `capabilities.conversationParameters`, which 0120.S1 of [`0120-anthropic-messages-api`](../0120-anthropic-messages-api/change.md) reads from the runtime record. The step also adds the per-session `runtimeRevision` to the adapter manifest under the `version: 2` that 0139.S1 raises ("Delivery" under "RV-7").

Task 3's pod sets per pod-set key, their names and labels, and the claim scope overlap [0137.S2](../0137-pod-lifecycle-controller/steps.md#steps), which depends on this step for `spec.podHash` and `spec.podSetKey`. How the two land relative to each other, and which of them owns the claim-scope code, is open decision O10, which [`0137-pod-lifecycle-controller`](../0137-pod-lifecycle-controller/decisions.md#open-decisions) records as its O6.

Under decision 8 of [0117](../0117-gateway-package-layout/decisions.md), this step edits `spec/interfaces/gateway-seams.md` and `pkg/gateway/seam` for the gateway seam operations whose mechanism it changes. It reshapes `GWSEAM-MaterializeChild`, which claims a delegated child's pod from the pod sets of the child's revision's pod hash after the child's revision is selected; `GWSEAM-CreateSession`, whose session gains a selected revision; and `GWSEAM-ResumeSuspended` and `GWSEAM-AdmitResume`, whose resume claims by the recorded revision's pod hash and moves revision under D7 when no pod set of it remains. `seam.Session` gains `runtimeRevision`. 0141.S2 removes `GWSEAM-ApplyExperimentRouting` before this step. The step records this addition to decision 8's list in its digest, as the amendment of 2026-11-19 states.

### 0140.S2 Session options
- status: Planned
- aliases: [RV-7]
- lanes: [specification, code, documentation]
- depends-on: [0141.S1, 0139.S1 (part)]
- closes: [F-14.1.38, F-14.1.39, F-14.1.40, F-5.1.33, F-5.1.34, F-4.8.26 (part)]

"RV-7" without revisions: `runtimeOptionsSchema` inline on the `Runtime` resource in place of `runtimeOptionsSchemaRef`, the reference schemas as files under `schemas/runtime-options/`, the compile at write and its refusals, the mirror's base-write checks, validation at the post-chain point of RT-2, which 0141.S1 adds, on every creation kind, with absent options validated as an empty object and a derived runtime's schema checked separately, the refusal details and counter, `runtimeOptions` on create-and-start and on the delegation `TaskSpec`, and delivery in `session_start` in place of the manifest field. Until 0140.S1 lands, every runtime has one schema, and the post-chain point validates against it; 0140.S1 then reads the chosen revision's schema. F-4.8.26 closes in part: its `runtimeOptions` check moves to the post-chain point, and its other checks belong to the post-chain point of 0141. The `(part)` edge to 0139.S1 holds the removal of the manifest's `runtimeOptions` field, which lands under the adapter manifest's `version: 2` that 0139.S1 raises, as "RP-6" of [`0139-runtime-protocol`](../0139-runtime-protocol/change.md#rp-6-what-the-adapter-enforces) states; the rest of the step can land first.

Under decision 8 of [0117](../0117-gateway-package-layout/decisions.md), this step names the gateway seam operations its mechanism sits behind. Options validation moves to the post-chain point behind `GWSEAM-CreateSession`, whose body carries `runtimeOptions`, and behind `GWSEAM-MaterializeChild`, which starts a delegated child whose `TaskSpec` now carries them. Each operation keeps its signature, and a refusal still carries the client API error code `RUNTIME_OPTIONS_INVALID`, so the step edits the operations' summaries in `spec/interfaces/gateway-seams.md` where they name the check and changes nothing in `pkg/gateway/seam`. The step records this addition to decision 8's list in its digest.

### 0140.S3 Isolation floor and tenant capability override
- status: Planned
- aliases: []
- lanes: [specification, code, documentation]
- depends-on: [0139.S1 (part)]
- closes: [F-5.1.36]

The isolation floor of "Isolation floor" under "RV-1" and the restrict-only per-tenant capability override of "Per-tenant capability override". Neither needs a revision: the floor is live, and until 0140.S1 lands, the override's write check reads the runtime's one set of declared capabilities. The `(part)` edge holds the rule that a narrowing removes the capabilities that depend on what it removes, which reads the dependency rules of RP-2 in 0139.S1; the narrowing of `interaction` and `injection` and the floor can land first. Which keys besides `interaction` and `injection` the override may narrow is open decision O15. F-5.1.35 and F-6.1.29 close with the removal of pre-launched runtimes in 0118.S2, and F-5.1.37 closes with the removal of integration levels (D23).

Under decision 8 of [0117](../0117-gateway-package-layout/decisions.md), this step names the gateway seam operations its mechanism sits behind. Raising a session that names no profile to its runtime's floor changes the pool resolution behind `GWSEAM-CreateSession` and `GWSEAM-MaterializeChild`. Neither operation's signature or refusal set changes, so the step edits neither `spec/interfaces/gateway-seams.md` nor `pkg/gateway/seam`.
