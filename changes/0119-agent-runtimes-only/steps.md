# Steps: 0119 Agent runtimes only

## Validation owed

- Whether `kubectl apply` of a `Runtime` or `PodSet` that names a removed field fails under the default server-side field validation, or only warns and prunes (O3).
- Whether session creation and delegation refuse a runtime row that is not `registered` today. O8's check applies either way, and the answer sizes the change in the tree.
- Whether any caller outside the tree reads `/v1/stateless/{pool}` or `/mcp/runtimes/{name}`; the tree has none apart from the packages listed **[source]**.
- Whether `docs/assets/diagrams/recycle-lifecycle.svg` shows service mode.

## Steps

### 0119.S1 Removal of type: mcp
- status: Planned
- aliases: [AR-A, AR-1]
- lanes: [specification, code, documentation]

Removal of `type: mcp`: the endpoint and its implicit sessions, discovery fields, the adapter's MCP client, `mcpRuntimeFilters`, `runtimeMcpServers`, the type-specific refusals, the idle exemption, the `safety_factor` row, the reference runtime, phase 12b, and their documentation and tests ("AR-1").

### 0119.S2 Removal of service mode
- status: Planned
- aliases: [AR-B, AR-2]
- lanes: [specification, code, documentation]
- closes: [F-5.2.65, F-5.2.66]

Removal of service mode: the claimless path, tenant-affinity routing and its packages, `/v1/stateless/{pool}`, `maxConcurrent`, the scaling formula and metrics, the `sessionIsolationLevel` fields (O4), the derive equality and `INCOMPATIBLE_RUNTIME`, the service-mode pod, the tool refusal, and the service-mode clauses of the specification and documentation ("AR-2"). It withdraws egress proposal 0, which the egress design scheduled to run first, and replaces it.

It lands before 0118.S3 (PL-E), so PL-E renames no service-mode field.

### 0119.S3 Frame contract
- status: Planned
- aliases: [AR-C, AR-3]
- lanes: [specification, code, documentation]
- depends-on: [0119.S2, 0135.S5, 0139.S1]

The frame contract: `startId` removal across the specification, the JSONL schema, the adapter, the SDKs, the compliance tool, and the reference runtimes, with the `sessionId` rule of O5 ("AR-3").

It lands after 0135.S5 (MD-E) and 0139.S1 (RP-A) so that the frame tables change once.

### 0119.S4 Removal of the type and executionMode fields
- status: Planned
- aliases: [AR-D, AR-4]
- lanes: [specification, code, documentation]
- depends-on: [0119.S1, 0119.S2]

The fields themselves: `type` and `executionMode` leave every resource, row, and interface, the refusal of "AR-4", the migration, the `restartable` removal, the `registered` check of O8, and the heading renames of O7.

### Notes on the steps

0119.S1 and 0119.S2 go first because they delete code that the pod layout, runtime protocol, and egress proposals would otherwise rewrite.
