# Steps: 0139 Runtime protocol

## Validation owed

- The time to write a line at the 50 MiB limit on a local socket, and whether it fits inside the shortest `interrupt_request` deadline (O7).
- Whether the Claude Agent SDK serves in-process MCP servers that a wrapper can back with frames ("RP-5").
- Whether Codex honours `notifications/tools/list_changed` on a stdio server, which "CA-9" could not measure.
- Whether a harness sends MCP `notifications/cancelled` for a call it abandons, so the bridge can write `tool_cancel`.
- Whether Claude Code and Codex apply a timeout to a long platform tool call, such as `await_child_sessions`, under which the bridge sees `notifications/cancelled` (D11).
- How many tool connections a real harness opens per slot, to set the per-slot bound ("RP-1").

## Steps

### 0139.S1 Handshake, capabilities, and versioning
- status: Planned
- aliases: [RP-A, RP-1, RP-2, RP-6, RP-7, RP-8]
- lanes: [specification, code, documentation]
- depends-on: [0135.S5, 0146.S1, 0145.S1, 0145.S3]

The handshake per slot with its refusal handling, the capability catalog with its dependency rules, the bind check at the gateway and the slot, versioning, conformance by capability, the removal of the integration levels from the specification, and the session-from-connection rule of RPR-18 with `sessionId` on `session_start` alone, with O7 and O9 as decided. AR's proposal AR-C (0119.S3) removes `startId` in the same release.

It lands after 0135.S5 (MD-E), so the frame tables change once; after 0146.S1 (SL-A), so the lifecycle's level statements are restated once; after 0145.S1 (SI-A), for per-slot endpoints, the slot binding, and per-slot manifests; and after 0145.S3 (SI-C), for one session per runtime process, on which RPR-18 and the refusal handling rest.

### 0139.S2 Tool calls on frames and the reference bridge
- status: Planned
- aliases: [RP-B, RP-3, RP-4, RP-5]
- lanes: [specification, code, documentation]
- depends-on: [0139.S1, 0145.S1, 0126.S1, 0127.S3]

Tool calls on frames, tool connections under O3, the platform tools on every pod, availability with `UpdateAvailability`, manifest version 2, the retirement of the MCP channels under O11, the removal of the adapter-local tools under D5, and the reference bridge section.

It supersedes the intra-pod part of MP-5 (0134.S3). CA-E, CA-H, and CA-I are withdrawn with connectors by [`0124-connector-removal`](../0124-connector-removal/change.md).

### 0139.S3 SDKs, executor, and reference runtimes
- status: Planned
- aliases: [RP-C]
- lanes: [code, documentation]
- depends-on: [0139.S1, 0139.S2]

The SDKs' handshake and bridge, the subprocess executor with its tool socket, the sample runtimes, the reference runtimes' wrappers, and the documentation.
