# Steps: 0122 Client-defined tools

## Validation owed

> **Adapter-facing section.** Every entry below concerns a provider API, a provider client SDK, or the session handling of a provider adapter, so each one moves to the adapter that serves that API, under O8 of [`0138-rest-only-client-api`](../0138-rest-only-client-api/change.md). No step of this change waits on any of them, because 0122.S1 carries no provider mapping and 0122.S2 is the Messages adapter's work (open decision O11).

- The `status` of an OpenAI Responses response whose output ends with a function call, and whether any client SDK reads a value other than `completed` for it.
- Whether the OpenAI Responses API carries `tools` across `previous_response_id`, which decides whether clients already resend tools as T1 requires.
- The Open Responses specification's `function_call_output` item and `parallel_tool_calls`, which the specification page does not define, and whether Open Responses clients accept the `response.function_call_arguments.done` event it does not name.
- Whether the OpenAI TypeScript SDK and the Anthropic SDKs accept one delta carrying a whole call, as `openai-python` does.
- A stream form for a custom tool call on Chat Completions, whose publication would let the refusal of O10 be lifted. The streaming reference and the `openai-python` chunk type read on 2026-11-09 define none.
- Whether an OpenAI `allowed_tools` list may name a custom tool, and in what entry form. The references read on 2026-11-09 type `tools` as a list of tool definitions and show function entries alone.
- Whether `parallel_tool_calls: false` limits custom tool calls as it limits function calls. The function calling guide covers function tools alone.
- Whether Open Responses clients send `custom` tools or `allowed_tools` with a `mode`, which the Open Responses specification does not define.
- The `x-should-retry` handling of the OpenAI and Anthropic TypeScript SDKs, which this design read only for the Python SDKs.
- Whether the session event bus delivers `tool_call` events to an adapter on the replica serving a forwarded warm turn, which the Messages design also lists.

The entry below governs Lenny rather than an adapter:

- A check, for each reference harness of §26, that it refreshes its MCP tool list when the reference bridge's `client` server changes its tools mid-session. 0166's "Validation owed" lists the same check. 0122.S1 waits on it for the bridge's part of the blocking interface, and open decision O22 reads its outcome.

## Steps

> **Adapter-facing section.** CT-A still governs Lenny for the runtime contract, the gateway core, and REST. Its MCP surface and its Chat Completions and Responses mappings of "CT-6", and all of CT-B, move to the adapters. The parts that move govern the adapters rather than Lenny, stay here unchanged until each adapter's repository takes them, and stage no edit to Lenny's specification, under O8 of [`0138-rest-only-client-api`](../0138-rest-only-client-api/change.md).

### 0122.S1 Runtime contract and gateway core
- status: Planned
- aliases: [CT-A, CT-1, CT-2, CT-3, CT-4, CT-5, CT-6 (part), CT-7, CT-8, CT-9, CT-10]
- lanes: [specification, code, documentation]
- depends-on: [0120.S1, 0135.S1, 0135.S5, 0146.S1, 0139.S1, 0139.S2, 0140.S1, 0141.S1]

The runtime contract and the gateway core (CT-1 to CT-5, CT-7 to CT-10) and the REST rows of CT-6, which are the parts of CT-A that still govern Lenny under O8 of [`0138-rest-only-client-api`](../0138-rest-only-client-api/change.md). The Chat Completions and Responses mappings of CT-6, the MCP arguments, and the parts of CT-3 and CT-7 that their banners move belong to the adapters, so this step adds no client tool mapping to the OpenAI translators or to the MCP tools. Open decision O12 states what this step does to the in-gateway translators and MCP tools until 0138.S6 removes them.

Revision 13 replaced the turn-ending exchange with the blocking interface that [`0166-delegation-primitives`](../0166-delegation-primitives/change.md#dp-7-what-each-change-takes-on) assigns to this change (0166, decisions 23, 24, 35, 42, and 48), so this step builds the open calls, their hold and withdrawal, the answer route, late results, the turn rule with provenance, and the audit of every client tool call, a parent's included. What is specific to a parent, `tools` on `session.create` and `session.send`, `tools.result`, and the parent's switches, lands with 0166.S3, which depends on this step.

The design is written in terms that the steps in `depends-on` deliver: the conversation input (0120.S1, AM-4); the named delivery paths, the open turn, and the `steer` intent (0135.S1, MD-1 to MD-7); the end-of-turn `message` frame (0135.S5, MD-24); the states `idle` and `dormant`, qualifying activity, and the close rule (0146.S1, SL-1, SL-6, and SL-7); the capability catalog, its dependency rule, and the effective capabilities (0139.S1, RP-2); the `tool_call` frame with `server`, `tool_cancel`, the `toolServers` list, and the reference bridge (0139.S2, RP-3 to RP-5), to which this step adds the `client` server and `group`; revisions, the `runtime_revision` label, and the per-tenant override (0140.S1, RV-1 and RV-2); and the post-chain point (0141.S1, RT-2). 0139.S1 and 0140.S1 already wait for 0135.S5, so this step names the frame `message` throughout and needs no interim name for it.

No pending set exists, so 0135.S2, which depends on this step, carries no client-call rows of MD-11, and a runtime cancels a client call with `tool_cancel` under RP-3. Open decision O14 asks what an answer after a cancel does.

A derive copies no open call and no late result, so 0144.S1's rewrite of the derive handler waits on this step only for `toolResults` on the client-message read. [`0144-session-derive`](../0144-session-derive/change.md) carries the matching edge.

Under decision 8 of [0117](../0117-gateway-package-layout/decisions.md), this step edits `spec/interfaces/gateway-seams.md` and `pkg/gateway/seam` for the gateway seam operations it changes. It reshapes `GWSEAM-RouteMessage`, which chooses the delivery path of a message and gains late results on the client's next message and the turn rule's removal of client tools from a turn another sender starts (CT-5). The hold operations `GWSEAM-HoldMessage`, `GWSEAM-HoldUndeliveredForward`, and `GWSEAM-DeliverHeld` gain no client-tool case, because a message that arrives while a call is open waits as it does behind any open turn. 0135.S1 and 0146.S1 reshape the same operations first, and 0135.S1 removes `GWSEAM-SendMessages`; this step, which depends on both, adds its cases to the form they leave.

### 0122.S2 Messages API mapping
- status: Planned
- aliases: [CT-B, CT-6 (part)]
- lanes: [code, documentation]
- depends-on: [0122.S1, 0120.S2, 0120.S3]

The Messages API rows of CT-6, replacing AM-E. The whole step is work of the Anthropic Messages adapter, which leaves Lenny under O8 of [`0138-rest-only-client-api`](../0138-rest-only-client-api/change.md), and its dependencies 0120.S2 and 0120.S3 move to the same adapter project. Open decision O11 asks how this step is recorded.
