# Decisions: 0122 Client-defined tools

## Decisions

Settled by the owner on 2026-11-09. T1 to T8 came with revision 1, and the owner revised T5 and added T9 with revision 2:

| ID | Decision | Where |
| :-- | :-- | :-- |
| T1 | The mechanism is turn-ending. The runtime's end-of-turn `message` carries `clientToolCalls`, as the `response` frame did before the message delivery design's MD-24 replaced it. The next client request reaches the runtime as a `message` with a `toolResults` member. The tools arrive in `conversation.clientTools` and `conversation.toolChoice` on every turn. | CT-1 |
| T2 | The v1 surfaces are Chat Completions, Responses, REST, and the Messages API. MCP gets client tools only as Lenny-specific tool arguments. | CT-6 |
| T3 | Lenny is not prescriptive. `capabilities.clientTools` is the runtime author's declaration, and Lenny refuses client tools only on runtimes that do not declare it. Lenny's own reference runtimes do not declare it in v1. | CT-2 |
| T4 | On Chat Completions, each tool round trip continues cold with the history. Superseded on 2026-11-14 by the conversation fingerprint rule of [`0138-rest-only-client-api`](../0138-rest-only-client-api/change.md#ro-10-the-provider-adapters): a round trip continues warm on the session the fingerprint finds, under that design's conditional send (R16), and continues cold with the history otherwise. | CT-6, CT-7 |
| T5 | A client message without the pending results is refused, apart from the REST exception of T9. For a runtime that does not declare the `steer` delivery intent of [`0135-message-delivery`](../0135-message-delivery/change.md), inter-session messages and other inbox deliveries wait until the results arrive, and are delivered right after them. For a steer-declaring runtime, inbox deliveries are not held while calls are pending, and the runtime places them after the results. The cold-history builder places a message that arrived while calls were pending after their results, so each call and its result stay adjacent. | CT-5, CT-7 |
| T6 | The gateway passes `tool_choice` and the parallel limit to the runtime. It validates the runtime's response: declared tool names, arguments against `input_schema`, `tool_choice`, and the parallel limit. A violation fails the turn with a do-not-retry error and a per-revision metric. The gateway does not repair the response. The owner confirmed with revision 2 that the JSON arguments of a function tool are still validated against its input schema. | CT-4 |
| T7 | Client tools are not delegated. | CT-9 |
| T8 | This is a standalone design, landing after the Messages design's AM-A and alongside the F-15.1.82 fix. | "Steps" in `steps.md` |
| T9 | On REST, a client message without the pending results is refused unless the request sets `abandonPendingToolCalls: true`, which turns the pending calls into "cancelled by client" error results. MCP clients reach `abandonPendingToolCalls` as a generated argument in the MCP adapter. Chat Completions, Responses, and the Messages API always refuse such a message. | CT-5 |

The owner's decisions of 2026-11-09 on [`0120-anthropic-messages-api`](../0120-anthropic-messages-api/change.md) also bind this design: that surface has no open turns, the conversation input carries ordered entries with the new input marked (its O3), warm continuation uses an HMAC-SHA256 conversation digest with an in-flight join that reattaches a retry to the running turn (its O2 and M1), and every failure after dispatch carries `x-should-retry: false`.

Settled by the owner on 2026-11-09 with revision 2, from the open decisions of revision 1. O1, O2, and O4 to O7 take revision 1's recommendation. O8 and O9 take the owner's own answers:

| ID | Question | Decision | Where |
| :-- | :-- | :-- | :-- |
| O1 | The form of the capability. | An object, `capabilities.clientTools.supported` with `reservedNames`, that requires `capabilities.conversationInput: true`. The cold path delivers earlier calls and results as structured history, which transcript rendering cannot express, so the dependency on conversation input is structural. `reservedNames` lets a runtime protect the names of its own model-visible tools, which the gateway cannot discover. O9 adds `customTools`. | CT-2 |
| O2 | Whether a runtime may declare that it accepts results only warm. | No field. A declaring runtime accepts results cold from history, and for calls it no longer holds in memory, or answers with an end-of-turn `message` carrying `error`. A harness wrapper that parks a call in memory can declare the capability and fail a cold continuation with an error. A `warmOnly` field is revisited when a harness wrapper asks for it. | CT-7 |
| O4 | REST sessions whose revision cannot accept a further message (`capabilities.interaction: one_shot`, or `capabilities.injection.supported: false`). | Client tools on such a session are refused at creation with `422 CLIENT_TOOLS_UNSUPPORTED`, naming the missing capability. On REST a session has no cold path with history, so a session that cannot take a second message can never receive its results. Chat Completions, Responses, and the Messages API are unaffected, because their cold path delivers results in a new session. | CT-2, CT-6 |
| O5 | A retried Responses continuation that carries results. | The Messages API's in-flight join extends to Responses continuations: a continuation of response `R` whose input equals the input of the running continuation of `R` reattaches to that turn. The SDKs retry a dropped connection twice by default **[external]**, and a results request is the most frequent request in a tool loop. Chat Completions stays cold under T4. REST need no join, because a second results message finds no pending calls and is refused. | CT-7 |
| O6 | How a deployer's output interceptors see call arguments. | The `PostAgentOutput` payload gains an optional `clientToolCalls` member beside the output parts, so an existing output filter covers arguments and custom tool input with no change to its registration. | CT-8 |
| O7 | The per-tenant capability override for client tools. | A tenant may clear `clientTools.supported`, may clear `clientTools.customTools`, and may append to `clientTools.reservedNames`. A widening is refused. This is the narrowing form RV-1 gives `capabilities.interaction` and `capabilities.injection`. Clearing `customTools` follows from O9 under the same narrowing rule. | CT-2 |
| O8 | A re-run of a session that used client tools. | Server-side replay no longer exists under [`0144-session-derive`](../0144-session-derive/change.md). A client that re-runs a session re-sends its messages itself and executes its own tools. A derive from a session with pending client calls copies them, and the derived session's first client request must carry the results. Revision 3 narrows the copy to `from: latest` (T10) and lets that request abandon the calls instead (T12). | CT-7 |
| O9 | OpenAI `allowed_tools` and `custom` tools. | `allowed_tools` is accepted as a restriction in `toolChoice`, `allowedNames`, which the T6 check enforces. `custom` tools are accepted for runtimes that declare `clientTools.customTools`. For a custom tool call the gateway checks the name, that the input is a string, `tool_choice`, and the parallel limit, and never checks the grammar. Grammar conformance is the runtime's responsibility, and the client verifies it. | CT-1, CT-2, CT-3, CT-4, CT-6 |

Settled by the owner on 2026-11-09 with revision 3, in the answer "Client tools: ok on everything". T10 changes revision 2's derive rule. T11 to T15 were choices revision 2 made without an owner decision, and the answer confirms them. O3 and O10 take revision 2's recommendation:

| ID | Decision | Where |
| :-- | :-- | :-- |
| T10 | A derive copies the source's pending calls only under `from: latest`. A `from: start` derive copies none, because it restarts from the state before the source's turns, where no call was issued. | CT-7 |
| T11 | A derive whose chosen revision does not declare `capabilities.clientTools`, or does not declare `customTools` while a copied call names a custom tool, is refused at the post-chain point with `422 CLIENT_TOOLS_UNSUPPORTED` before any pod is claimed. | CT-7 |
| T12 | The derived session's first client message may set `abandonPendingToolCalls: true` in place of the results, on REST, as any client message may under T9. | CT-5, CT-7 |
| T13 | A client message that sets `abandonPendingToolCalls` and also carries `toolResults` is refused with `409 CLIENT_TOOL_RESULTS_MISMATCH`. | CT-5 |
| T14 | The "cancelled by client" results that `abandonPendingToolCalls` creates pass through `PreToolResult`, as every result does. | CT-5, CT-8 |
| T15 | An OpenAI `allowed_tools` tool choice with `mode: required` maps to `toolChoice.type` `any`, and `mode: auto` maps to `auto`. | CT-3, CT-6 |

| ID | Question | Decision | Where |
| :-- | :-- | :-- | :-- |
| O3 | For a runtime that does not declare `steer`, what happens to held inbox deliveries when the results turn itself ends with new calls. A steer-declaring runtime is outside this decision under T5. | Held deliveries stay held until a turn ends with no pending calls, under their ordinary inbox TTL. The histogram `lenny_client_tool_held_delivery_age_seconds` records how long each one waited. T5 exists so that a runtime without `steer` never receives unrelated input while its model history holds an unanswered call, and delivery at the next turn end would break that for every multi-step tool loop. A runtime author who wants inbox deliveries during a tool loop declares `steer`. | CT-5 |
| O10 | The Chat Completions stream form of a custom tool call. | A Chat Completions request with `stream: true` that declares a custom tool is refused with `400 invalid_request_error` naming the combination, until OpenAI documents a stream form. The Chat Completions streaming reference types `delta.tool_calls[].type` as `"function"` alone, stating "Currently, only `function` is supported", and the `openai-python` chunk type `ChoiceDeltaToolCall` declares `type: Literal["function"]` with no `custom` member **[external]**, so a client SDK has no documented field to accumulate the input into. A plain Chat Completions response and every Responses request carry custom tools under O9. | CT-3, CT-6 |

Retraction of pending calls is settled in [`0135-message-delivery`](../0135-message-delivery/change.md) under its decision S8, and "CT-5" and "CT-6" state its effect on this design.

Settled by the owner on 2026-11-09 with revision 4, through O3 of [`0135-message-delivery`](../0135-message-delivery/change.md). The owner asked "wouldn't it be simpler to just deliver the message whenever it arrives?" and then answered "yes", and that decision carries this rule:

| ID | Decision | Where |
| :-- | :-- | :-- |
| T16 | Client tools are offered only in turns caused by a client request. A turn caused by another session's message, a system message, or an external message carries no `conversation.clientTools` and no `conversation.toolChoice`, and a call in it fails validation with `no_tools_declared`. This holds for an inbox delivery during pending calls, for a held delivery released after a turn with no pending calls, and for a message delivered on arrival to a held session on a provider surface. A turn that no client request caused could otherwise return calls that no waiting client receives. | CT-1, CT-4, CT-5 |

Settled by the owner on 2026-11-13, in the answers to the consolidated list of open decisions:

| ID | Decision | Where |
| :-- | :-- | :-- |
| T17 | The owner chose option (b) of the consolidated list, in the words "ok, go with (b)", and confirmed that the OpenAI and Anthropic adapters leave too and that the Lenny project maintains each adapter in a separate repository with an optional Helm subchart. Every client protocol adapter (MCP, A2A, OpenAI Chat Completions, Open Responses, and Anthropic Messages) moves out of Lenny into a separate first-party repository in front of the REST API. `0138-rest-only-client-api`, which is being written, carries the change and governs which of this design's surfaces leave with the adapters. This design does not apply the removal, and T2's list of v1 surfaces stands until that design lands. | Status |

**Gateway seam operations (0117, decision 8).** Settled by the owner on 2026-11-17 in [0117](../0117-gateway-package-layout/decisions.md), which raised the conflict between its seam interface page and this change: a member change that removes, reshapes, or adds a mechanism behind a gateway seam operation edits `spec/interfaces/gateway-seams.md` and `pkg/gateway/seam` in the step that changes the mechanism. In this change that is 0122.S1, whose entries in `steps.md` name the operations.
