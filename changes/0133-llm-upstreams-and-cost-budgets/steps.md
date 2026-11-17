# Steps: 0133 LLM upstreams and cost budgets

## Validation owed

| Item | Why it matters | Status |
| :-- | :-- | :-- |
| Read the usage of a Claude response on Bedrock InvokeModel, on the Bedrock Messages API, and on Vertex `rawPredict`, streaming and non-streaming, including `cache_creation` and its split | The class table assumes the Anthropic field names on every Anthropic-format channel. | Open. The cache removal design lists the same gap. |
| Confirm that `message_start`'s `cache_creation` split is final for the call | "Streams" takes the split from `message_start` and the total from the last `message_delta`. | Open. |
| Confirm whether OpenAI Chat Completions includes `cache_write_tokens` in `prompt_tokens`, and how it reports the write lifetime | The caching guide's worked example uses the Responses names alone **[external]**. | Open. |
| Confirm what each provider reports and bills for a stream the client cancels | Settlement at the reservation assumes a cancelled stream can carry no usage. | Open. |
| Measure the ratio estimator of OD-7 on recorded agent traffic per format: the error of the first-call estimate at `k` of 3, the error of the stored-ratio estimate on later calls, the undershoot per call, and refusals near a limit that the call would have fit | The overshoot that LUR-7 accepts is the estimator's error, and the default `k` rests on the stated bytes per token alone. | Open. |
| Measure the image and document block estimates per format against the tokens each provider reports for images and PDFs of known size and page count | "Input estimate" states the defaults **[unverified]**. | Open. |
| Read Google's documentation of `generateContent`, `streamGenerateContent`, and `countTokens` on the Gemini API and on Vertex AI: the paths, the `x-goog-api-key` header, the `usageMetadata` fields and whether `promptTokenCount` includes `cachedContentTokenCount`, whether `thoughtsTokenCount` is apart from `candidatesTokenCount` and billed as output, how `toolUsePromptTokenCount` is billed, whether a stream's last `usageMetadata` carries the totals, and `generationConfig.maxOutputTokens` | The `google-generate` column, routes, and output limit are **[unverified]**. | Open. |
| Confirm that `gemini-cli` accepts a base URL and sends the proxy token in a header the proxy reads, for the Gemini API and for Vertex AI | OD-9 moves `gemini-cli` onto the proxy. | Open. |
| Confirm the token exchange from the gateway's Kubernetes service account to a Google service account for Vertex AI on a live project | "No long-lived key for cloud channels" relies on it. | Open. |
| Measure `llm_credit_reserve.lua` latency at the proxy's request rate | Each call adds a read of the input ratio and two script runs. | Open. |
| Confirm Bedrock's endpoints for Opus 4.7 and later, SigV4 from the gateway's workload identity, and per-tenant session tags on a live account | "Pass-through" and "Credential sources" rely on them. | Open. |
| Confirm Azure Foundry's Responses API path and its usage fields | `openai-responses` on `azure-foundry` is assumed. | Open. |
| Confirm which gateways preserve provider usage fields and report the served model | `channel: gateway` meters what the gateway returns. | Open. |
| Confirm that no VCS path uses `AssignCredentials` or the adapter's credentials file at `/run/lenny/credentials.json` | "LU-4" deletes them for LLM leases. If a VCS path still uses the file, it stays at that path on the slot's run volume, and `session_start` keeps naming it in `credentialsPath` ([PL-3](../0118-agent-pod-layout/change.md#pl-3-runtime-visible-filesystem-layout)). | Open. |
| Confirm, against a running LiteLLM and a self-hosted Portkey, that `extra_spend_tag_headers`, `tag_regex`, `x-litellm-tags`, and `HEADERS_TO_METADATA` act on the `Lenny-*` headers as "LU-11" states | The plain headers that OD-13 settled rest on the documentation alone. | Open. |
| File the defects found while verifying: Bedrock streams sent to the non-streaming `invoke` path, the 120-second client timeout on streams, the unregistered `OpenAIResponsesTranslator` and its provider key collision, the missing Pattern B base URL, `UserProxyDialect` mapping `vertex_ai` to an unserved dialect, and `credential.leased` emitted for no LLM lease | They are defects in the tree today, independent of this design. | Open. This design edits no record. |
| Read again the external facts this design carries from the analysis: the Bedrock and Vertex endpoint facts, Anthropic's workspace limits and cost reports, Azure's deployment types, and AWS's per-user role sessions for gateways | "Sources" marks them. | Open. |

## Steps

### 0133.S1 Metering corrections
- status: Planned
- aliases: [LU-A]
- lanes: [specification, code, documentation]
- closes: [F-11.2.38]

Metering corrections: token classes with cache writes by lifetime, the parsers and streaming rules of "Usage normalization", the `llm_usage` records, the class counts on `token_usage.checkpoint`, and the correction fields. The client-facing usage of the Open Responses and Chat Completions surfaces, which revision 6 placed here, moves to the provider adapters under [`0138-rest-only-client-api`](../0138-rest-only-client-api/change.md), which read R3. It absorbs RC-B and O2 of the cache removal design.

It has no dependency. It runs on today's proxy and records the pool in place of the upstream until 0133.S2. It lands first because it fixes a metering defect that exists today.

### 0133.S2 Upstreams and removal
- status: Planned
- aliases: [LU-B, LU-1, LU-2, LU-3, LU-4, LU-8, LU-9, LU-10, LU-11]
- lanes: [specification, code, documentation]
- depends-on: [0133.S1, 0127.S2]
- closes: [F-4.9.48]

Upstreams and removal: LU-1, LU-2, the routes and session binding of LU-3, LU-4, LU-8, LU-9, LU-10, and LU-11, with the specification rewrite of §4.9's LLM half and every section of "Specification changes" that the removal reaches. It includes the `google-generate` format with its routes, its parser, and the `gemini-api` channel, Google Cloud workload identity beside the AWS and Azure exchangers, the §26 format declarations with `gemini-cli` on `google-generate`, and the statement of calls outside the proxy. It resolves the lease findings.

It needs EG-2 for the removal of `provider-direct`.

### 0133.S3 Rate cards and credit budgets
- status: Planned
- aliases: [LU-C, LU-5, LU-6, LU-7]
- lanes: [specification, code, documentation]
- depends-on: [0133.S2]
- closes: [F-11.2.34]

Rate cards and credit budgets: LU-5 with the fallback entry, LU-6 with the input estimate and best-effort bounds, and the estimated cost of LU-7, with §8.3, §8.6, §11.2, and §11.2.1 in credits.

### Notes on the steps

0133.S2 and 0133.S3 can merge into one step if the owner prefers to change budgets in the same step as their anchor. Revision 2's LU-D, which carried the gate, the guardrail, and the posture entries of the unmetered path, and its LU-E, which carried `google-generate` and Google Cloud workload identity, are retired: OD-8 removes LU-D's mechanisms and 0133.S2 (LU-B) carries the statement of calls outside the proxy, and OD-9 moves LU-E's content into 0133.S2. The labels are not reused.
