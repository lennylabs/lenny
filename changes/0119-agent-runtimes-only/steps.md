# Steps: 0119 Agent runtimes only

## Validation owed

- Whether `kubectl apply` of a `Runtime` or `PodSet` that names a removed field fails under the default server-side field validation, or only warns and prunes (O3). 0119.S4 waits on it, together with open decision 4.
- Whether session creation and delegation refuse a runtime row that is not `registered` today. O8's check applies either way, and the answer sizes the change in the tree. [0147.S4](../0147-tenant-derived-runtimes/steps.md#steps), which builds the check, waits on it.
- Whether any caller outside the tree reads `/v1/stateless/{pool}` or `/mcp/runtimes/{name}`; the tree has none apart from the packages listed **[source]**. 0119.S1 waits on it for `/mcp/runtimes/{name}`, and 0119.S2 for `/v1/stateless/{pool}`.

## Steps

### 0119.S1 Removal of type: mcp
- status: Planned
- aliases: [AR-A, AR-1]
- lanes: [specification, code, documentation]
- closes: [23.1-INFO-007, T-2.5, T-9.1.4, T-9.1.10, T-10.6.5, T-8.5.4 (part), T-9.1.6 (part)]

Removal of `type: mcp`: the endpoint and its implicit sessions, discovery fields, the adapter's MCP client, `mcpRuntimeFilters` with the refusal of an environment write that names it ("AR-4", **Which step refuses which field.**), `runtimeMcpServers` from adapter manifest version 1 with the compliance fixture that asserts it, the type-specific refusals, the idle exemption, the `safety_factor` row, the reference runtime, phase 12b, and their documentation and tests ("AR-1"). Its tests include one that the idle watchdog exempts no session by runtime type.

### 0119.S2 Removal of service mode
- status: Planned
- aliases: [AR-B, AR-2]
- lanes: [specification, code, documentation]
- depends-on: [0119.S1]
- closes: [F-5.2.65, F-5.2.66, F-15.1.76 (part), T-5.2.5, T-5.2.13]

Removal of service mode: the claimless path, tenant-affinity routing and its packages, `/v1/stateless/{pool}`, `maxConcurrent` with the refusal of a pool write that names it ("AR-4", **Which step refuses which field.**), the scaling formula and metrics, the `sessionIsolationLevel` fields (O4), the derive equality and `INCOMPATIBLE_RUNTIME`, the service-mode pod, the tool refusal, and the service-mode clauses of the specification and documentation ("AR-2"). It withdraws egress proposal 0, which the egress design scheduled to run first, and replaces it.

It lands before 0118.S3 (PL-E), so PL-E renames no service-mode field. It lands after 0119.S1, because both steps edit §5.1, §5.2, §4.7.10, `runtimestore`, `sessionserver`, and the admin runtime handlers.

The derive's execution-mode equality check, `requireReplayRuntimeCompatible`, `INCOMPATIBLE_RUNTIME`, and the test `tests/contract/rest_sessions/replay_incompatible_runtime_contract_test.go` are also removed by [0144.S1](../0144-session-derive/steps.md#steps), which removes the replay route that holds part of that code. Whichever of 0119.S2 and 0144.S1 lands second skips the removal the first made. This step closes the `executionMode` half of F-15.1.76, and 0144.S1 closes its `runtimeRef` half.

### 0119.S3 Frame contract
- status: Planned
- aliases: [AR-C, AR-3]
- lanes: [specification, code, documentation]
- depends-on: [0119.S2, 0135.S5, 0139.S1]

The frame contract: `startId` removal across the specification, the JSONL schema, the adapter, the SDKs, the compliance tool, and the reference runtimes, with the `session_started` acceptance rule of "AR-3", including the `startId` clause of RPR-21 of [`0139-runtime-protocol`](../0139-runtime-protocol/change.md). The `sessionId` rule of O5 (ARR-6) lands with 0139.S1, the session-from-connection rule of RPR-18, and this step does not repeat it.

It lands after 0135.S5 (MD-E) and 0139.S1 (RP-A) so that the frame tables change once.

### 0119.S4 Removal of the type and executionMode fields
- status: Planned
- aliases: [AR-D, AR-4]
- lanes: [specification, code, documentation]
- depends-on: [0119.S1, 0119.S2, 0118.S1 (part), 0118.S2 (part), 0145.S3 (part)]

The fields themselves: `type` and `executionMode` leave every resource, row, and interface, with the refusal of "AR-4" for a runtime's `type` and `executionMode` and a pool's `executionMode`, the migration with its Phase 3 gate and down file, the `restartable` removal, and the heading renames of O7. O8's removal of SD-P4 and `target_unsupervised` is design text in 0144 and 0147, with nothing in the tree to remove; the `registered` check of O8 is built by [0147.S4](../0147-tenant-derived-runtimes/steps.md#steps) under DR-4, and this step does not build it.

The `(part)` edges carry the `restartable` removal alone: 0118.S1 makes `command` required, 0118.S2 removes `preConnect`, and 0145.S3 retires `keep` and the reader of the flag, so the flag is constant and unread only after all three ("AR-5", **`restartable` leaves.**). It waits on open decisions 1 to 5 in `decisions.md`.

### Notes on the steps

0119.S1 and 0119.S2 go first because they delete code that the pod layout, runtime protocol, and egress proposals would otherwise rewrite. The steps of other changes that assume the removal carry the edge on their own entries; the M3 readiness review of 2026-11-19 asked for it on 0118.S1, 0126.S1, 0139.S1, 0139.S2, 0144.S1, 0145.S1, 0146.S1, and 0117.S13. 0119.S1 and 0119.S2 wait on open decisions 1 and 3 in `decisions.md` only for the refusal each carries; the rest of each step waits on no open decision.

The inventory under "Current state" in `change.md` names paths as they stood on 2026-11-13. `scripts/refactor/manifest` translates each gateway package path to its current one (for example, `pkg/gateway/externalapi/admin` is `pkg/gateway/api/admin`, and `pkg/gateway/sessionserver` is `pkg/gateway/core/session/sessionserver`), `pkg/controller/sandbox` is `pkg/controller/agentpod`, unit tests sit beside their packages, the test directories are `tests/static`, `tests/component`, `tests/contract`, `tests/integration`, `tests/e2e_kind`, `tests/load_local`, `tests/security`, `tests/conformance`, and `tests/docs`, and the retired records `BUILD-PLAN.md`, `ROADMAP.md`, `tests/spec-map.json`, and `tests/change-graph.json` resolve under "Reading the program" in [`programs/v1.md`](../programs/v1.md). Files that read the removed fields and are missing from the inventory include `cmd/lenny-gateway/credentialsurface.go` (`ConversationContinuity`), `pkg/gateway/environment/translator/singleshot.go`, `pkg/controller/poolscaling/statelessdemandsource.go`, `cmd/lenny-pool-scaling-controller/main.go` (`lenny_service_*`), `pkg/tenancy/environmentstore`, `pkg/environment`, `pkg/tenancy/envaccess`, `pkg/delegation/delegationpolicystore`, and `pkg/gateway/api/admin/credential_pools.go` (`runtimestore.TypeAgent`).

Each step deletes rows of `tests/registers/gateway-layout.yaml` (`mcpruntimes` in 0119.S1; `statelessproxy`, `statelessrouting`, and `tenantaffinity` in 0119.S2) and lowers counts in `tests/registers/spec-section-citations.yaml`, and 0119.S4 retires the rows of `tests/registers/residual-skip-reasons.yaml` keyed to `migrations/runtime_definitions_execution_mode_service_test.go`, the test of migration `0167`. A branch that touches `tests/registers/` is reviewed with the `review` skill before it merges (`.claude/rules/git-workflow.md`, "Review changes to the merge gate"). This change does not remove `pkg/gateway/runtime/sdkwarm`, which holds the SDK-warm demotion decision that 0118.S2 removes, although the register row names `removed-by: '0119'`.
