# Steps: 0120 Anthropic Messages API compatibility

## Validation owed

Under the banner of `change.md`, the item on a cancelled wait is owed before 0120.S1 is trusted, and the other open items belong to the Anthropic adapter project and wait on no Lenny step.

- Anthropic adapter project: which header each current Anthropic SDK sends when configured with an API key, and whether the SDKs send `anthropic-version` on the models routes. "Sources" records the documentation only.
- Anthropic adapter project: whether the SDKs' typed parsers accept an `error.details` whose `error_code` is not one of Anthropic's values, and an empty `signature` value. The Python SDK's union is discriminated by `error_code`.
- Anthropic adapter project: whether the TypeScript and Go SDKs honour `x-should-retry` as the Python SDK does.
- 0120.S1 waits on it: what a runtime does with a turn when the gateway's wait is cancelled, so the executor can make a turn's completion independent of the HTTP request's context. If open decision O15 is settled with 0120.S1 after 0135.S1, the item is owed against `Deliver`.
- Anthropic adapter project: whether common agent clients read `usage.input_tokens` to decide when to compact.
- Anthropic adapter project: the label key rules of §14 for `anthropic-user-id`.
- Anthropic adapter project: whether the session event bus delivers `output` events to an adapter on the replica serving the request during a forwarded warm turn or a join.
- Answered by reading the tree on 2026-11-19: `CapabilityOverride` in `pkg/runtime/runtimestore/capoverride.go` has typed fields for `interaction`, `injection`, `preConnect`, and `sdkWarmBlockingPaths` and none for a new key, so it cannot clear `conversationInput` today, and 0120.S1 adds the fields ("AM-4", "Capability").

## Steps

Under O8 of [`0138-rest-only-client-api`](../0138-rest-only-client-api/change.md), AM-A is Lenny's apart from the OpenAI translators' move, and AM-B and AM-C move to the Anthropic adapter project.

### 0120.S1 Conversation input
- status: Planned
- aliases: [AM-A, AM-4]
- lanes: [specification, code, documentation]
- closes: [F-15.1.80 (part)]

Conversation input (AM-4) for the runtime contract: the `conversation` member with ordered entries, the entry schema, and the new-input mark, which a REST client writes under R15 of [`0138-rest-only-client-api`](../0138-rest-only-client-api/change.md); its validation, custody, and redelivery; the capability keys `conversationInput` and `conversationParameters` with the per-tenant override; the transcript rendering; and requirements AMR-14 to AMR-21. It closes the executor half of DF-1 (F-15.1.80): the executor writes one envelope per delivery call, with each message's role kept as the entry's role, so a call with several messages runs one turn ("AM-4", "Executor"). The OpenAI translators' move onto conversation input, and the refusals of DF-3 and DF-4 on them, leave with the translators, and 0138.S6 closes the translator half of F-15.1.80 and F-15.1.81 to F-15.1.83 by removal. Open decision O14 settles whether the step also keeps the in-tree translators working until then.

When this step lands before 0139.S1 and 0140.S1, it adds the two keys to the `capabilities` block of §5.1 as that section stands and reads them from the runtime record; 0139.S1 moves them into the capability catalog of RP-2, and 0140.S1 moves the read to the chosen revision. It leaves `sessionId` on the envelope, which 0139.S1 removes under O5 of [`0119-agent-runtimes-only`](../0119-agent-runtimes-only/change.md). Open decision O15 settles its order against 0135.S1.

Under decision 8 of [0117](../0117-gateway-package-layout/decisions.md), this step edits `spec/interfaces/gateway-seams.md` and `pkg/gateway/seam` for the gateway seam operations it changes. It reshapes `GWSEAM-SendMessages`, through which the platform tools seam delivers messages to a running session, whose mechanism changes from one turn per message to one envelope per call; when the step lands after 0135.S1 (open decision O15), it reshapes the delivery operation that replaces `GWSEAM-SendMessages` instead. The step records this addition to decision 8's list in its digest, as the amendment of 2026-11-19 states.

Revision 2's AM-E, client tools, is replaced by [`0122-client-tools`](../0122-client-tools/change.md), which lands after AM-A (T8).

### 0120.S2 Messages adapter endpoint and request mapping
- status: Planned
- aliases: [AM-B, AM-1, AM-2, AM-3, AM-6, AM-8, AM-9]
- lanes: [specification, code, documentation]
- depends-on: [0120.S1]

The adapter's endpoint, authentication, headers, request mapping, errors, models routes, and refused endpoints (AM-1, AM-2, AM-3, AM-8, and AM-9), with cold single-shot turns, incremental streaming, and the immediate refusal of `lenny/request_input` and elicitation (AM-6).

The Messages mapping of "AM-7" lands with 0122.S2 (CT-B) of [`0122-client-tools`](../0122-client-tools/change.md) once this step has landed.

### 0120.S3 Continuity
- status: Planned
- aliases: [AM-C, AM-5]
- lanes: [specification, code, documentation]
- depends-on: [0120.S2]

Continuity (AM-5): keyed digests, warm turns, the session lifetime of "Session lifetime", forwarding with the credential and `anthropic-version`, the in-flight join, request records, and erasure.

### Notes on the steps

Revision 2's AM-D, open turns, is removed (O4).
