# Steps: 0140 Runtime revisions

## Validation owed

| Item | Why it matters | Status |
| :-- | :-- | :-- |
| Confirm that no controller replaces warm pods whose image differs from the `Runtime` resource | "Current state" infers a mixed pool from the absence of such code. | Open. |
| Confirm how a runtime registered only through the admin API reaches the pod builder, which reads the `Runtime` resource | "RV-3" moves the pod builder to the revision, and both registration paths must produce one. | Open. |
| Review where the gateway reads each versioned field, and settle `runtimeOptionsSchema` | O2 asked it. The sync review found readers of `capabilities.deferredToolDiscovery`, `type`, `requireSoPeercred`, and `deploymentModel` before selection, and "RV-2" moves them to the post-chain point. D20 has since removed `deploymentModel`, and CR-1 of [`0124-connector-removal`](../0124-connector-removal/change.md) has since removed `capabilities.deferredToolDiscovery`. | Settled by D9. "Field classes" places each versioned field by its reader in the tree, read on 2026-11-09. |
| Confirm the placement of `isolationProfile`, `workspaceDefaults`, and `description` | Revision 6 placed `isolationProfile` and `workspaceDefaults` by judgement and kept `description` live, while D9 named descriptions among the gateway-read fields. | Settled for `isolationProfile` by D11, for `workspaceDefaults` by D19, and for `agentInterface` by D12. `description` stays live and is open, for the owner. |
| Confirm that the WarmPoolController can read a runtime's floor for a runtime registered only through the admin API | "Isolation floor" makes the WarmPoolController refuse a template below the floor, and it reads the `Runtime` resource today. | Open. |
| Confirm that every session creation path can raise a session's profile to the floor before pool resolution, including a pinned pool and a derive | "Isolation floor" raises a session that names no profile. | Open. |
| Confirm that each built-in schema in §14 compiles as a standalone file with no `$ref` outside the document | "RV-7" moves them to `schemas/runtime-options/` and compiles every schema at write. | Open, for the proposal. |
| Confirm that the controller can render a warm list and the pre-bind reach per pool and revision | "RV-3" keys them on the revision, and the egress decision service reads them from the pool row today. | Open. |
| Measure the revisions table at a million rows: insert rate, the retention job's batch time, and listing latency | "Storage and performance" is an estimate. | Open. |
| Confirm that artifact references in `sharedAssets` resolve to a content digest that a revision can pin | "Field classes" pins them. | Open. |
| Confirm that resume claims by the session's recorded pool and can be extended to the revision | "RV-6" relies on it. Revision 23 states the extension: the claim takes the recorded revision's pod hash, as [`0137-pod-lifecycle-controller`](../0137-pod-lifecycle-controller/change.md) PLCR-18 states for every claim. | Open. |
| Confirm that the warm-up checkout can take its list of clones from the revision a pod's template names, and measure the first-session cost of an incremental checkout when a session's commit differs from the staged one | "Field classes" keeps `workspaceDefaults` gateway-read and relies on the helper adopting, checking out incrementally, or discarding a staged tree at bind (the owner's approval of the warm-up checkout on 2026-11-12, which the workspace materialization design owns). | Open. |
| Measure the warm pod cost of a base rollout with tenant-derived pools | "Limits" states the cost without a number. | Open. |
| Check the AWS prices and measure the warm-up time in "Worked example: an upgrade on AWS" | The example uses approximate list prices and an assumed warm-up time of 5 to 10 minutes. | Open. |

## Steps

### 0140.S1 Runtime revisions
- status: Planned
- aliases: [RV-1, RV-2, RV-3, RV-4, RV-5, RV-6, RV-7]
- lanes: [specification, code, documentation]
- depends-on: [0141.S2, 0139.S1]
- closes: [F-5.1.36]

The step lands after 0141.S2 of [`0141-runtime-routing`](../0141-runtime-routing/change.md), whose shared bucketing function it uses. It runs these tasks in order:

1. Land the specification edits.
2. Move `runtimeOptionsSchema` inline on the `Runtime` resource, ship the reference schemas as files, compile schemas at write, and move options validation to the post-chain point and their delivery to `session_start`. These close the defects under "Session options" in "Current state" and need no revision.
3. Make `isolationProfile` a live floor and narrow the per-tenant capability override. These close F-5.1.36 and need no revision. F-5.1.35 and F-6.1.29 close with the removal of pre-launched runtimes (D21), and F-5.1.37 with the removal of integration levels (D23).
4. Add the revision record, the hash, and the pod hash on both registration paths, with every runtime at one revision, and move the pod builder to the revision.
5. Add pod sets per pod-set key, the warm split, selection, discovery by revision, `agentInterface` published per caller, and the revision read of the capability check at bind. The runtime protocol design's 0139.S1 adds that check, so this task lands after it.
6. Move the rollout procedure to the runtime and retire the pool-level endpoints.
7. Update the documentation and records.
