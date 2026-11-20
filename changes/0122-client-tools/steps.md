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
- Whether the session event bus delivers `tool_use` events to an adapter on the replica serving a forwarded warm turn, which the Messages design also lists.

## Steps

> **Adapter-facing section.** CT-A still governs Lenny for the runtime contract, the gateway core, and REST. Its MCP surface and its Chat Completions and Responses mappings of "CT-6", and all of CT-B, move to the adapters. The parts that move govern the adapters rather than Lenny, stay here unchanged until each adapter's repository takes them, and stage no edit to Lenny's specification, under O8 of [`0138-rest-only-client-api`](../0138-rest-only-client-api/change.md).

### 0122.S1 Runtime contract and gateway core
- status: Planned
- aliases: [CT-A, CT-1, CT-2, CT-3, CT-4, CT-5, CT-6 (part), CT-7, CT-8, CT-9, CT-10]
- lanes: [specification, code, documentation]
- depends-on: [0120.S1, 0135.S1, 0135.S5, 0146.S1, 0139.S1, 0140.S1, 0141.S1]

The runtime contract and the gateway core (CT-1 to CT-5, CT-7 to CT-10) and the REST rows of CT-6, which are the parts of CT-A that still govern Lenny under O8 of [`0138-rest-only-client-api`](../0138-rest-only-client-api/change.md). The Chat Completions and Responses mappings of CT-6, the MCP arguments, and the parts of CT-3 and CT-7 that their banners move belong to the adapters, so this step adds no client tool mapping to the OpenAI translators or to the MCP tools. Open decision O12 states what this step does to the in-gateway translators and MCP tools until 0138.S6 removes them.

The design is written in terms that the steps in `depends-on` deliver: the conversation input (0120.S1, AM-4); the named delivery paths, the open turn, the `steer` intent, and the `retract` frame (0135.S1, MD-1 to MD-7 and MD-11); the end-of-turn `message` frame (0135.S5, MD-24); the states `idle` and `dormant` and the close rule (0146.S1, SL-1, SL-6, and SL-7); the capability catalog, its dependency rule, and the effective capabilities (0139.S1, RP-2); revisions, the `runtime_revision` label, and the per-tenant override (0140.S1, RV-1 and RV-2); and the post-chain point (0141.S1, RT-2). 0139.S1 and 0140.S1 already wait for 0135.S5, so this step names the frame `message` throughout and needs no interim name for it.

The retraction rules of CT-5 ("Retraction") and rule 3 of its delivery rule land with 0135.S2, which carries the client-call rows of MD-11 and depends on this step. This step records the pending set and the conditional write that a retraction uses.

The copy of the pending set on a `from: latest` derive, the refusal of T11, and `toolResults` on the client-message read land with 0144.S1's rewrite of the derive handler, which waits on this step for the pending set (consistency conflict client-api-sessions-C7). [`0144-session-derive`](../0144-session-derive/change.md) carries the matching edge and rules.

Under decision 8 of [0117](../0117-gateway-package-layout/decisions.md), this step edits `spec/interfaces/gateway-seams.md` and `pkg/gateway/seam` for the gateway seam operations it changes. It reshapes `GWSEAM-EnqueueInbox`, `GWSEAM-RouteMessage`, which chooses the delivery path of a message from another session and gains the case of a session with pending client tool calls, and the hold operations `GWSEAM-HoldMessage`, `GWSEAM-HoldUndeliveredForward`, and `GWSEAM-DeliverHeld`, with the hold rule for inbox deliveries while client tool calls are pending (CT-5). 0135.S1 and 0146.S1 reshape the same operations first, and 0135.S1 removes `GWSEAM-SendMessages`; this step, which depends on both, adds the pending-call case to the form they leave.

### 0122.S2 Messages API mapping
- status: Planned
- aliases: [CT-B, CT-6 (part)]
- lanes: [code, documentation]
- depends-on: [0122.S1, 0120.S2, 0120.S3]

The Messages API rows of CT-6, replacing AM-E. The whole step is work of the Anthropic Messages adapter, which leaves Lenny under O8 of [`0138-rest-only-client-api`](../0138-rest-only-client-api/change.md), and its dependencies 0120.S2 and 0120.S3 move to the same adapter project. Open decision O11 asks how this step is recorded.
