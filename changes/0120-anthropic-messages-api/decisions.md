# Decisions: 0120 Anthropic Messages API compatibility

## Decisions

Under O8 of [`0138-rest-only-client-api`](../0138-rest-only-client-api/change.md), the decisions that govern "AM-1" to "AM-3" and "AM-5" to "AM-10" move to the Anthropic adapter project with those sections. D2 and O3 still govern Lenny as they apply to "AM-4".

Settled by project rules:

| ID | Decision | Basis |
| :-- | :-- | :-- |
| D1 | The adapter is a built-in adapter in the `ExternalAdapterRegistry`, beside MCP and the OpenAI translators, and creates sessions through the shared create-and-start service. | §15 "Three tiers of pluggability" and the single-shot compute model. One creation path keeps admission and the post-chain point uniform. |
| D2 | The design adds no compatibility mode, legacy flag, or migration path for Lenny's own surfaces. The OpenAI translators change their history delivery in place (O3). | The project rule against compatibility shims before a first deployment (`feedback_no_backward_compat`). |
| D3 | A request field or content block that Lenny cannot honor is refused with `400 invalid_request_error` naming it, unless it is a hint with no effect on output semantics, such as `cache_control`. The existing translators' practice of decoding a request and dropping unknown fields is not copied. | Fail closed on behaviour a client relies on. A silently ignored `tools` array would return prose to a client that expects `tool_use`. |
| D4 | Every claim about the Messages API in this design comes from the pages under "Sources", read on 2026-11-09. | `feedback_verify_external_standard_versions`. |

Settled by the owner on 2026-11-09. The M labels are the adversarial review's findings that the owner answered together with an open decision:

| ID | Decision |
| :-- | :-- |
| O1 | The client presents a Lenny bearer in `Authorization: Bearer` or in `x-api-key`. The auth middleware normalizes `x-api-key` into the request's credential and strips the header. A value that starts with `sk-ant-` receives a specific `401 authentication_error` that never echoes the value. Conflicting values in the two headers receive `401 authentication_error` ("AM-2"). |
| O2 and M1 | Conversation state is kept by warm continuation on a keyed digest, with a cold fallback. The digest is HMAC-SHA256 and includes `metadata.user_id`. The canonical form follows RFC 8785. Consecutive messages of one role, and adjacent text blocks, are merged before hashing. `lenny.labels` is excluded from the digest. A retry reattaches to its running turn through an in-flight join. The beta `thread` parameter stays refused, and once it is stable a thread identifier maps to a session as `previous_response_id` does ("AM-5"). |
| O3 | Cold turns deliver the conversation as structured input to runtimes that declare it, and as a transcript to others, and the OpenAI translators move onto the same input. The `conversation` member carries ordered entries with the new input marked, so a trailing system message has a place. The beta per-message fields are refused ("AM-3" and "AM-4"). |
| O4, M3, and M6 | No open turns: revision 2's AM-D is removed, and a turn ends inside its request. A long turn uses streaming. Client tools come from [`0122-client-tools`](../0122-client-tools/change.md), which replaces revision 2's AM-7 bridge. `lenny/request_input` and elicitation toward the client are refused at once on this surface, and the adapter declares `SupportsElicitation: false` ("AM-6" and "AM-7"). |
| O5 and M4 | The runtime declares which parameters it honours. A non-default `stop_sequences` or `thinking.display` for a runtime that does not declare it is refused. `effort` and `thinking` pass as hints. `max_tokens` is per message: it caps the turn's visible output, only for a runtime that declares it, and never maps to the session's budget ("AM-3"). |
| O6 | `GET /v1/models` chooses its format by `anthropic-version` or by `?adapter=anthropic-messages`, sends `Vary: anthropic-version`, dispatches `GET /v1/models/{id}` the same way, and reports a capability it cannot vouch for as false or null ("AM-9"). |
| O7 and M5 | The adapter accepts `2026-10-21` and `2023-06-01`, and refuses an unknown newer version with an error that lists the supported ones ("AM-2"). |
| O8 | Removed with open turns. Revision 2's `pause_turn` wait limit and `anthropicMessages.maxResponseWaitSeconds` go. |
| O10 | The `lenny` object is create-only. Its `labels` are excluded from the digest, and a warm turn ignores the object ("AM-3"). |
| M2 | A held session follows the Open Responses hold rule of §15 "Open Responses conversation sessions", and the adapter adds no timer of its own ("AM-5"). Superseded on 2026-11-14 by the simplified O3 of [`0138-rest-only-client-api`](../0138-rest-only-client-api/change.md), which the owner answered "yes": no session is held, and "Session lifetime" states the rule. |
| M7 | `anthropic-beta` is accepted and ignored. Field-level refusals enforce behaviour ("AM-2"). |
| MD O3 | Decided on [`0135-message-delivery`](../0135-message-delivery/change.md), and binding here. A message from another session to a held session is delivered on arrival. Client tools are offered only in turns a client request caused, and a turn that no client request caused never appears in a Messages response, whose content is the output of the client's own turn. The conversation digest is unaffected ("AM-5", "Messages from other sessions"). |
| M8 | This design asks [`0149-tenant-scoping-identity`](../0149-tenant-scoping-identity/change.md) for a `jwt-bearer` grant on `/v1/oauth/token`, so the SDKs' workload identity federation can obtain a Lenny bearer ("Instructions for reviewers of other designs"). |

Settled by the owner on 2026-11-10:

| ID | Decision |
| :-- | :-- |
| O9 | A model name that is no runtime, such as an Anthropic model ID, reaches a runtime through a runtime route whose requested name is that alias. [RT-4](../0141-runtime-routing/change.md#rt-4-runtime-routes) accepts such a route, and `GET /v1/models` lists the caller's aliases. A separate alias table would duplicate routes. The owner's words were "go with the recommendation" ("AM-3" and "AM-9"). |

Settled by the owner on 2026-11-13. The upstreams design's reviewer instruction asked this design to make the choices of O11 and O12. The owner answered the consolidated list of open decisions with "18–21 ok" on 2026-11-13, which takes the recommendation of each, and the body states the settled rules. O13 records the owner's choice of the same day on the client protocol adapters:

| ID | Decision |
| :-- | :-- |
| O11 | Settled (2026-11-13): (a). A turn that ends with `BUDGET_EXHAUSTED` answers `429 rate_limit_error`, with `details.limitType` naming the budget's scope and `x-should-retry: false` ("AM-8"). |
| O12 | Settled (2026-11-13): (a). `usage.input_tokens` reports the sum of the `input` class over every model call of the turn ("AM-6"). |
| O13 | The owner chose option (b) of the consolidated list on 2026-11-13, in the words "ok, go with (b)", and confirmed that the OpenAI and Anthropic adapters leave too and that the Lenny project maintains each adapter in a separate repository with an optional Helm subchart. Every client protocol adapter (MCP, A2A, OpenAI Chat Completions, Open Responses, and Anthropic Messages) moves out of Lenny into a separate first-party repository in front of the REST API. `0138-rest-only-client-api`, which is being written, carries the change and governs what leaves. This design does not apply the removal, and D1's placement of the adapter in the `ExternalAdapterRegistry` stands until that design lands. |

The options and reasoning of O11 and O12, kept as a record:

| ID | Question | Options | Recommendation |
| :-- | :-- | :-- | :-- |
| O11 | How the adapter answers a turn that ends with `BUDGET_EXHAUSTED` under [`0133-llm-upstreams-and-cost-budgets`](../0133-llm-upstreams-and-cost-budgets/change.md) ("AM-8"). | (a) `429 rate_limit_error`, with `details.limitType` naming the budget's scope and `x-should-retry: false`. (b) `402 billing_error`, with the same details and header. | (a). A Lenny budget is a spending limit that lifts when its window resets or an admin raises it, which a client reads as a limit condition, and `x-should-retry: false` stops the SDKs' retry. `billing_error` describes the state of an Anthropic account, which a Lenny budget is not. |
| O12 | What `usage.input_tokens` reports for a turn with several model calls ("AM-6"). | (a) The sum of the `input` class over every model call of the turn. (b) The `input` class of the turn's last model call. | (a). The sum is the turn's cost and matches the usage the other surfaces report. Its effect on a client that compacts its history by `input_tokens` is under "Limits". |
