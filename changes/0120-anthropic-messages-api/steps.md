# Steps: 0120 Anthropic Messages API compatibility

## Validation owed

- Which header each current Anthropic SDK sends when configured with an API key, and whether the SDKs send `anthropic-version` on the models routes. "Sources" records the documentation only.
- Whether the SDKs' typed parsers accept an `error.details` whose `error_code` is not one of Anthropic's values, and an empty `signature` value. The Python SDK's union is discriminated by `error_code`.
- Whether the TypeScript and Go SDKs honour `x-should-retry` as the Python SDK does.
- What a runtime does with a turn when the gateway's wait is cancelled, so the proposal can make a turn's completion independent of the HTTP request's context.
- Whether common agent clients read `usage.input_tokens` to decide when to compact.
- The label key rules of §14 for `anthropic-user-id`.
- Whether the session event bus delivers `output` events to an adapter on the replica serving the request during a forwarded warm turn or a join.
- Whether `runtimecapoverride` can clear a capability flag that a revision declares, as AM-4 requires.

## Steps

Under O8 of [`0138-rest-only-client-api`](../0138-rest-only-client-api/change.md), AM-A is Lenny's apart from the OpenAI translators' move, and AM-B and AM-C move to the Anthropic adapter project.

### 0120.S1 Conversation input
- status: Planned
- aliases: [AM-A, AM-4]
- lanes: [specification, code, documentation]
- closes: [F-15.1.80, F-15.1.81, F-15.1.82, F-15.1.83]

Conversation input (AM-4) for the runtime contract, with ordered entries and the new-input mark, and the OpenAI translators moved onto it, closing DF-1 and DF-2.

AM-A also closes DF-3, by refusing the fields the OpenAI translators cannot honor under D3, and DF-4, by refusing a request with no `model`. Revision 2's AM-E, client tools, is replaced by [`0122-client-tools`](../0122-client-tools/change.md), which lands after AM-A and alongside AM-A's fix of F-15.1.82 (T8).

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
