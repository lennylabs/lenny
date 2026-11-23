# Steps: 0141 Runtime routing

## Validation owed

| Item | Why it matters | Status |
| :-- | :-- | :-- |
| Run Kind to confirm that the gateway dials interceptors in plaintext | "Current state" infers it from the template and the dial code. | The chart render is done (2026-11-09): with `gateway.interceptorNamespaces` set, the gateway gets `--dev-mode=false` and none of the three TLS flags. Filed as F-10.3.27. The Kind run remains open. |
| Confirm the isolation check the gateway applies to a directly requested runtime, and whether any check covers an external interceptor's rewrite | The post-chain checks of "RT-2" reuse the direct check. | Open. |
| Confirm that retry and resume read the stored runtime and pool and run no `PreRoute` chain | "RT-2" and RTR-11 rely on it. | Open. |
| Confirm that each check in the post-chain table reads only the final row and can move to one call site | "RT-2" moves checks that other designs place at separate call sites. | Open, for the proposal. |
| Confirm the bounds §14 places on `labels` | "RT-3" reuses them for `routing`. If none exist, the proposal sets bounds for both. | Open. |
| Confirm that admin writes emit a generic audit record | Routes rely on it in place of `experiment.status_changed`. | Open. |
| Confirm the interceptor transport under `mtls.enabled: false` with a service mesh | "RT-5" admits plaintext to a mesh sidecar. | Open. |
| Confirm whether the create path reserves token budget at `PostAuth` today | "Before the chain" requires a post-chain refusal to release it. The comments in `createAdmissionGates` say the reservation happens there, and no reservation call was found in the create path on 2026-11-10. | Open, for the proposal. |
| Confirm that the shared Kind database's migration guard accepts the forward drop migration | "RT-1" relies on it. | Open, for the proposal. |
| Confirm the §28 link row for the gateway-to-interceptor connection | "Specification changes" assumes the verifier finds or adds it. | Open, for the proposal's verifier. |

## Steps

0141.S1 and 0141.S2 land in that order. 0141.S3 and 0141.S4 depend on no other step and may land at any point.

### 0141.S1 Interceptor contract
- status: Planned
- aliases: [proposal 1, first proposal, interceptor contract proposal, RT-2]
- lanes: [specification, code, documentation]

"RT-2" without the route interceptor. It stands alone: it corrects the delegation path and the payloads that "RT-2" lists under **Payload audit**, and adds the session context, the last modifier, and the post-chain point while the `ExperimentRouter` still exists. If [`0144-session-derive`](../0144-session-derive/change.md) lands first, this step adds `kind: derive` to its create path. Otherwise the derive step adopts the payload this one lands.

It moves to the post-chain point the checks the tree holds when it lands, the effective `maxDepth` resolution of §8.2 among them, which judges the child's final runtime and pool after the child's `PreRoute` chain. It does not move cycle detection or the three-layer self-recursion gate, which [`0166-delegation-primitives`](../0166-delegation-primitives/change.md) removes under its decision 5. 0166.S1 depends on this step and adds the other checks of a child's creation at the point, as the 0166 row of "Post-chain point" states. Each owning step that lands after it adds its own row's check at the point, as "Post-chain point", **Order against the owning steps.**, states, and until 0140.S1 lands, revision selection returns the runtime's single current definition. Open decision D19 decides whether it also moves the playground boundary, the §27.6 caps, and the §14 envelope; if it does, this step closes F-4.8.26 in part, and RV-7 of [`0140-runtime-revisions`](../0140-runtime-revisions/change.md) closes the `runtimeOptions` part.

Under decision 8 of [0117](../0117-gateway-package-layout/decisions.md), this step edits `spec/interfaces/gateway-seams.md` and `pkg/gateway/seam` for the gateway seam operations it changes. It reshapes `GWSEAM-Intercept`, through which the platform tools seam runs the `PreRoute` phase for a delegated child: its result gains the last modifier, its request carries the session context, and a `PreRoute` `MODIFY` returns the runtime, pool, and routing labels. It adds or reshapes the `seam.ChildSessions` operation through which the platform tools seam has the session core apply the child's routing and run its post-chain checks, which replaces `GWSEAM-ApplyExperimentRouting` or sits beside it until 0141.S2. It regenerates `spec/catalogs/api-operations.md`.

### 0141.S2 Runtime routes and removal
- status: Planned
- aliases: [proposal 2, second proposal, RT-1, RT-3, RT-4, RT-7]
- lanes: [specification, code, documentation]
- depends-on: [0141.S1, 0147.S1, 0139.S1 (part)]
- closes: [F-10.7.21, F-10.7.22, T-10.7.1, T-10.7.2, T-10.7.3, T-10.7.4, T-10.7.5, T-10.7.6, T-10.7.7, T-10.7.8, T-10.7.9, T-10.7.10, T-10.7.11, T-10.7.12, T-10.7.13, T-10.7.14, T-10.7.16]

"RT-1", "RT-3", "RT-4", and "RT-7". It uses the payload and the single chain of 0141.S1. [`0140-runtime-revisions`](../0140-runtime-revisions/change.md) depends on the shared bucketing function of 0141.S2 and lands after it. It closes F-10.7.22 by removing experiments and their pool effects, as DF-9 of [`0147-tenant-derived-runtimes`](../0147-tenant-derived-runtimes/change.md) records, and retires the other findings in `closes` with the subsystem they describe.

It depends on 0147.S1 because route admission reads the access rows that step adds. Admission reads those access rows until 0147.S12, which depends on this step and on 0147.S4, moves it onto the tenant view, and until 0140.S1 lands, the per-revision admission rule applies to each runtime's single current definition. [`0138-rest-only-client-api`](../0138-rest-only-client-api/change.md) 0138.S1 depends on this step for the aliases and adds them to R8's single-runtime read, which is new in that step; this step lists them on `GET /v1/runtimes`.

It replaces `experimentContext` with `routing` in the adapter manifest's per-session fields under manifest `version: 2`, as **The version 2 field set and where each change lands.** under "RP-6" of [`0139-runtime-protocol`](../0139-runtime-protocol/change.md) lists, and does not raise the version again. It depends on 0139.S1 for that part, because 0139.S1 raises the version to 2 and moves the SDKs and `pkg/runtimekit` to it. Under option (b) of open decision D23, this part moves with the adapter and SDK removal to the new step, and so does this edge.

Within 0141.S2, the specification edits land first, then routes and routing labels with their tests, then the removal and the migration, then the documentation and records. Open decision D23 asks whether the step is split.

The removal of the `pkg/gateway/experiment/` packages deletes their seven `removed-by: '0141'` rows from `tests/registers/gateway-layout.yaml`. Because the diff touches `tests/registers/`, the `review` skill runs over it before the merge, under "Review changes to the merge gate" in `.claude/rules/git-workflow.md`, and the digest records the outcome.

Under decision 8 of [0117](../0117-gateway-package-layout/decisions.md), this step edits `spec/interfaces/gateway-seams.md` and `pkg/gateway/seam` for the gateway seam operations it changes. It removes `GWSEAM-ApplyExperimentRouting` with the `ExperimentRouter`, unless 0141.S1 has already replaced it, and regenerates `spec/catalogs/api-operations.md`.

### 0141.S3 In-cluster interceptor transport
- status: Planned
- aliases: [RT-5]
- lanes: [specification, code, documentation]
- closes: [F-10.3.27]

"RT-5": the in-cluster endpoint rule, the chart's interceptor TLS flags, the refusal to start, the refusal to dial without TLS material, and the refusal of a registration write while the material is absent, outside dev mode and service-mesh mode. It stands alone and closes the plaintext dial of F-10.3.27.

### 0141.S4 Tenant-scoped interceptors
- status: Planned
- aliases: [RT-6]
- lanes: [specification, code, documentation]

"RT-6": the `tenants` field on a registration, the tenant filter in the chain, the scope rule for `contentPolicy.interceptorRef`, the removal of a deleted tenant from every registration, and the ordering guidance. It stands alone and gives SaaS providers tenant scope while the `ExperimentRouter` still exists. Open decision D21 decides the verdict for a session of an unlisted tenant. The mirror and posture entries of D15 belong to [`0142-security-oversight`](../0142-security-oversight/change.md).
