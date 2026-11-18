# Steps: 0122 Client-defined tools

## Validation owed

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
- depends-on: [0120.S1]

The runtime contract and the gateway core (CT-1 to CT-5, CT-7 to CT-10), REST, and the Chat Completions and Responses mappings of CT-6. It turns AM-A's refusal of `tools` on the OpenAI translators into the mapping for declaring revisions, and lands alongside the F-15.1.82 fix.

Under decision 8 of [0117](../0117-gateway-package-layout/decisions.md), this step edits `spec/interfaces/gateway-seams.md` and `pkg/gateway/seam` for the gateway seam operations it changes. It reshapes `GWSEAM-EnqueueInbox` and the hold operations `GWSEAM-HoldMessage`, `GWSEAM-HoldUndeliveredForward`, and `GWSEAM-DeliverHeld` with the hold rule for inbox deliveries while client tool calls are pending (CT-5).

### 0122.S2 Messages API mapping
- status: Planned
- aliases: [CT-B, CT-6 (part)]
- lanes: [code, documentation]
- depends-on: [0122.S1, 0120.S2, 0120.S3]

The Messages API rows of CT-6, replacing AM-E.
