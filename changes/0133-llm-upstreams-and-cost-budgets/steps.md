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
| Read the hosted tool types that `openai-responses` and `google-generate` offer (for example a remote MCP tool with a caller-supplied server URL, and URL and search grounding tools), and which of them make the provider reach the network | Open decision 1 is settled for every metered format, and its allowlist of tool types needs the list per format **[unverified]**. | Open. Waits on 0133.S2. |
| Confirm whether Claude models on Google Cloud (Google managed) accept a top-level `cache_control` on `rawPredict` | "LU-9" states that the proxy passes a provider's refusal of a top-level `cache_control` back unchanged and that Anthropic's guide states no such restriction for Google Cloud; Google Cloud's prompt-caching page for Claude documents only the block placement. Moved from [`0132-llm-response-cache-removal`](../0132-llm-response-cache-removal/steps.md) on 2026-11-19. | Open. Waits on 0133.S2 (LU-9). |
| Reconcile Anthropic's statement of organization-level cache isolation on Google Cloud (Google managed) with Google Cloud's statement that a cache is unique to its project | "LU-9" states that isolation follows the upstream's provider account or workspace, and names organization-level isolation on Claude models on Google Cloud. Moved from `0132-llm-response-cache-removal` on 2026-11-19. | Open. Waits on 0133.S2 (LU-9). |
| Read again the external facts this design carries from the analysis: the Bedrock and Vertex endpoint facts, Anthropic's workspace limits and cost reports, Azure's deployment types, and AWS's per-user role sessions for gateways | "Sources" marks them. | Open. |

## Steps

### 0133.S1 Metering corrections
- status: Planned
- aliases: [LU-A]
- lanes: [specification, code, documentation]
- closes: [F-11.2.38]

Metering corrections: token classes with cache writes by lifetime, the parsers and streaming rules of "Usage normalization", the `llm_usage` records, the class counts on `token_usage.checkpoint`, and the correction fields. The client-facing usage of the Open Responses and Chat Completions surfaces, which revision 6 placed here, moves to the provider adapters under [`0138-rest-only-client-api`](../0138-rest-only-client-api/change.md), which read R3. It absorbs RC-B and O2 of the cache removal design.

It has no dependency. It runs on today's proxy and records the pool in place of the upstream until 0133.S2. It lands first because it fixes a metering defect that exists today.

Until 0133.S3 converts budgets to credits, the session token budget, the §8.6 extension trigger, and the Redis quota counters count a call's total input (`input`, `cacheRead`, and every `cacheWrite` lifetime) plus its output, so the budget and quota half of F-11.2.38 closes in this step with the records half. The §11.2 **Quota Update Timing:** and §4.9 **Request flow:** item 6 edits of this step state that count.

### 0133.S2 Upstreams and removal
- status: Planned
- aliases: [LU-B, LU-1, LU-2, LU-3, LU-4, LU-8, LU-9, LU-10, LU-11]
- lanes: [specification, code, documentation]
- depends-on: [0133.S1, 0127.S2, 0141.S1 (part)]
- closes: [F-4.9.48, CHEGRESS-I1, CHGWPOD-I9, CHGWPOD-I12, CHGWPOD-I15, F-4.9.32, F-4.9.33, F-4.9.34, F-4.9.37, F-4.9.38, F-4.9.39, F-4.9.40, F-4.9.41 (part), F-4.9.47 (part), F-4.7.31, F-11.4.10]

Upstreams and removal: LU-1, LU-2, the routes and session binding of LU-3, LU-4, LU-8, LU-9, LU-10, and LU-11, with the specification rewrite of §4.9's LLM half and every section of "Specification changes" that the removal reaches. It includes the `google-generate` format with its routes, its parser, and the `gemini-api` channel, Google Cloud workload identity beside the AWS and Azure exchangers, the §26 format declarations with `gemini-cli` on `google-generate`, and the statement of calls outside the proxy. It resolves the lease findings.

It needs EG-2 for the removal of `provider-direct`. It deletes the `lenny-direct-mode-isolation` webhook and checks (1) and (2) of the `direct_mode_isolation` validator with `deliveryMode`, in place of 0127.S2, which removes check (3) with `provider-direct`, so that `main` does not admit a direct-mode pool on `isolationProfile: standard` or a multi-tenant pool with `spiffeBinding: disabled` between the two merges ("LU-4"); open decision 4 settles whether it restates the `spiffeBinding` refusal in the admin API's pool validation, and it decides CHEGRESS-I2.

It lands the precedence of a tenant upstream over a platform upstream, the `Warning` on a write that shadows one, and a shared pool's `llmUpstreamsPolicy` (decision 15), the rule that a host in `llm.tenantEndpointHosts` authenticates each call by its credential (decision 14), and `quotas.llmProxy.maxConcurrentCallsPerSession` (decision 13).

It needs the post-chain point of RT-2 in 0141.S1 for the list check of "LU-2" **Session creation.** Until 0140.S1 lands, the check reads `llmFormats` from the runtime record, and 0140.S1 moves it onto the chosen revision as 0140's instruction from this change states. The `llm` posture section and its tests land in whichever of 0133.S2 and 0142.S2 merges second, because 0142.S2 builds the posture endpoint ("LU-8" **Posture.**). If the owner settles option (a) of open decision 41 of [`0142-security-oversight`](../0142-security-oversight/decisions.md), which splits 0142.S2, the `llm` section lands with whichever of 0133.S2 and 0142's posture step merges second.

0132.S1 and 0133.S2 rebase on whichever of them lands first, because both edit `pkg/gateway/meteringproxy/llmproxy/handler.go`, `pkg/credential/credentialpoolstore`, and the admin pool validator (0132-L8).

The metering proxy of this step keeps the refusal of a session's new calls and the cancellation of its calls in flight when the session's suspension cannot take hold, from the pod in use until that pod is released, as SLR-23 of [`0146-session-lifecycle`](../0146-session-lifecycle/change.md) states (L24). 0146.S1 lands the refusal in the proxy the tree has when it lands, so whichever of 0146.S1 and 0133.S2 lands second carries it over ("LU-3" **Session binding**).

With the `provider` path of `POST /v1/credentials` (OD-10), the step removes the `credentials` table and its gateway-side store, `credentialpg` in `cmd/lenny-gateway/credentialsurface.go` (`pkg/credential/credentialstore/pgstore`) and `pkg/credential/usercreds`, which serve only that path. 0151.S1 depends on this step for that removal (0151-F4).

The resolved lease findings are those of "Records" in `change.md`. F-4.9.41 (part) keeps its VCS half open. F-4.9.47 (part) is shared with 0151, whose UC-1 settles the wire names. F-4.9.46 is closed by 0151.S1. CHINTRA-I1 and CHGWPOD-I14 become obsolete in this step only if the validation item on `AssignCredentials` and the credentials file finds no VCS use, so they are not listed under `closes`; the step closes them when that item is resolved that way. Open decision 8 asks whether this step splits into smaller steps.

It deletes the rows of `tests/registers/gateway-layout.yaml` with `removed-by: '0133'` as it deletes their packages, and its diff is reviewed under "Review changes to the merge gate" of `.claude/rules/git-workflow.md` because it touches `tests/registers/`.

Under decision 8 of [0117](../0117-gateway-package-layout/decisions.md), this step edits `spec/interfaces/gateway-seams.md` and `pkg/gateway/seam` for the gateway seam operations it changes. It removes `GWSEAM-CheckChildCredentials` with the pre-claim credential check and `GWSEAM-RotateCredential` with the credential fallback (LU-4), reshapes `GWSEAM-MaterializeChild`, which no longer assigns an LLM credential lease, and removes `GWSEAM-TenantCredentialProviders` with the tenant's `credentialPolicy`. It also reshapes or removes `GWSEAM-RevokePoolCredentials` and `GWSEAM-RevokeUserLeases`, through which the admin API revokes a pool credential's leases and returns a revoked user's leases, with the credential renewal propagator and the lease revoker it removes. Removing the renewal propagator removes one of the two callers of `GWSEAM-ExpireSession` (`cmd/lenny-gateway/cred_renewal.go`), and 0133.S3 removes the operation with the other.

### 0133.S3 Rate cards and credit budgets
- status: Planned
- aliases: [LU-C, LU-5, LU-6, LU-7]
- lanes: [specification, code, documentation]
- depends-on: [0133.S2]

Rate cards and credit pricing: LU-5 with the fallback entry, the per-call ceiling, the input estimate with its ratio hash, settlement, the recovery sweep, and the best-effort bounds of LU-6, and the estimated cost and usage records of LU-7, with §8.6 and §11.2.1 in credits. Under decision 11, this step builds no session, tree, user, tenant, or global counter: 0170.S1 of [`0170-budgets`](../0170-budgets/steps.md) adds budget objects as the only budgets the proxy reserves against, and wires this step's reservation and settlement to them. Until 0170.S1 lands, the existing counters stay as they are and count tokens. This step's tests therefore cover the pricing of each call, the per-call ceiling, the input estimate and its ratio hash, the settlement and recovery of a reservation against a test store of reservations, and the usage record, and 0170.S1 runs them against session budgets. F-11.2.34 closes with 0170.S1, which moves the session budget into Redis, and F-8.3.9 with 0166.S2, which returns a carve after its child's reservations settle.

Under decision 8 of [0117](../0117-gateway-package-layout/decisions.md), this step edits `spec/interfaces/gateway-seams.md` and `pkg/gateway/seam` for the gateway seam operations it changes. It reshapes `GWSEAM-ExtendForBudget` and `GWSEAM-RaiseBudget` for the extension in credits. The delegation budget operations (`GWSEAM-ReturnBudget`, `GWSEAM-RearmBudget`, `GWSEAM-ObserveHighWatermark`, `GWSEAM-NodeUsage`, `GWSEAM-TreeUsage`, and `GWSEAM-SubtreeTokens`) and `GWSEAM-HandleBudgetKeysExpired` keep counting tokens over the tree budget, which 0166.S2 replaces with the carve and removes with them (0170 decision 43). Under decision 5, a refused extension no longer ends the session, so the step stops the in-process enforcer's call to `GWSEAM-TerminateForBudget`. 0170.S1 removes the in-process enforcer with `GWSEAM-TerminateForBudget`, `GWSEAM-TrackedBudgetSessions`, and the state that `budget_state_sweep.go` sweeps, and reshapes or removes `GWSEAM-ForgetBudget`, when the session budget moves to Redis.

Decision 6 settles the reservation protocol under Redis Cluster, and open decision 9 settles the future of `billing.dualControlThreshold` before this step's §11.2.1 edits.

### Notes on the steps

0133.S2 and 0133.S3 can merge into one step if the owner prefers to change budgets in the same step as their anchor. Revision 2's LU-D, which carried the gate, the guardrail, and the posture entries of the unmetered path, and its LU-E, which carried `google-generate` and Google Cloud workload identity, are retired: OD-8 removes LU-D's mechanisms and 0133.S2 (LU-B) carries the statement of calls outside the proxy, and OD-9 moves LU-E's content into 0133.S2. The labels are not reused.
