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

The steps land in order.

### 0141.S1 Interceptor contract and scope
- status: Planned
- aliases: [proposal 1, first proposal, interceptor contract proposal, RT-2, RT-5, RT-6]
- lanes: [specification, code, documentation]

"RT-2" without the route interceptor, "RT-5", and "RT-6". It stands alone: it corrects the delegation path and the phase payloads, adds the session context and the last modifier, closes the plaintext dial, and gives SaaS providers tenant scope while the `ExperimentRouter` still exists. If [`0144-session-derive`](../0144-session-derive/change.md) lands first, this proposal adds `kind: derive` to its create path. Otherwise the derive proposal adopts the payload this one lands.

### 0141.S2 Runtime routes and removal
- status: Planned
- aliases: [proposal 2, second proposal, RT-1, RT-3, RT-4, RT-7]
- lanes: [specification, code, documentation]
- depends-on: [0141.S1]

"RT-1", "RT-3", "RT-4", and "RT-7". It uses the payload and the single chain of 0141.S1. [`0140-runtime-revisions`](../0140-runtime-revisions/change.md) depends on the shared bucketing function of 0141.S2 and lands after it.

Within 0141.S2, the specification edits land first, then routes and routing labels with their tests, then the removal and the migration, then the documentation and records.
