# Steps: 0133 LLM upstreams and cost budgets

## Validation owed

| Item | Why it matters | Status |
| :-- | :-- | :-- |
| Confirm that `message_start`'s `cache_creation` split is final for the call | "Streams" takes the split from `message_start` and the total from the last `message_delta`. | Open. |
| Confirm whether OpenAI Chat Completions includes `cache_write_tokens` in `prompt_tokens`, and how it reports the write lifetime | The caching guide's worked example uses the Responses names alone **[external]**. | Open. |
| Confirm, per format, what the format's native API reports and bills for a stream the client cancels | Settlement at the reservation assumes a cancelled stream can carry no usage. | Open. Narrowed by D10 on 2026-11-19 to the native API of each format. |
| Measure the ratio estimator of OD-7 on recorded agent traffic per format: the error of the first-call estimate at `k` of 3, the error of the stored-ratio estimate on later calls, the undershoot per call, and refusals near a limit that the call would have fit | The overshoot that LUR-7 accepts is the estimator's error, and the default `k` rests on the stated bytes per token alone. | Open. |
| Measure the image and document block estimates per format against the tokens each provider reports for images and PDFs of known size and page count | "Input estimate" states the defaults **[unverified]**. | Open. |
| Read the Gemini API's documentation of `generateContent`, `streamGenerateContent`, and `countTokens`: the paths, the `x-goog-api-key` header, the `usageMetadata` fields and whether `promptTokenCount` includes `cachedContentTokenCount`, whether `thoughtsTokenCount` is apart from `candidatesTokenCount` and billed as output, how `toolUsePromptTokenCount` is billed, whether a stream's last `usageMetadata` carries the totals, and `generationConfig.maxOutputTokens` | The `google-generate` column, routes, and output limit are **[unverified]**. | Open. Narrowed by D10 on 2026-11-19 to the Gemini API, because the proxy reaches no Vertex AI path. Desk validation VAL-002 answered the paths, the header, and the field definitions; whether the last streamed chunk carries the totals and whether `maxOutputTokens` bounds thinking tokens wait for a live call. |
| Confirm that `gemini-cli` accepts a base URL and sends the proxy token in a header the proxy reads, against the Gemini API format | OD-9 moves `gemini-cli` onto the proxy. | Open. Narrowed by D10 on 2026-11-19 to the Gemini API; `gemini-cli` on Vertex AI is reached through an LLM gateway. |
| Measure `llm_credit_reserve.lua` latency at the proxy's request rate | Each call adds a read of the input ratio and two script runs. | Open. |
| Confirm which LLM gateways preserve the usage fields of the format they serve and report the served model, including a gateway in front of a provider API that needs signing or rehosting | The proxy meters what an upstream returns, and under D10 every signed or rehosted provider API is reached through a gateway. | Open. Widened by D10 on 2026-11-19 to cover the usage that a gateway returns for the provider APIs Lenny no longer reaches itself. |
| Confirm that no VCS path uses `AssignCredentials` or the adapter's credentials file at `/run/lenny/credentials.json` | "LU-4" deletes them for LLM leases. If a VCS path still uses the file, it stays at that path on the slot's run volume, and `session_start` keeps naming it in `credentialsPath` ([PL-3](../0118-agent-pod-layout/change.md#pl-3-runtime-visible-filesystem-layout)). | Open. |
| Confirm, against a running LiteLLM and a self-hosted Portkey, that `extra_spend_tag_headers`, `tag_regex`, `x-litellm-tags`, and `HEADERS_TO_METADATA` act on the `Lenny-*` headers as "LU-11" states | The plain headers that OD-13 settled rest on the documentation alone. | Open. |
| File the defect found while verifying that has no finding yet: `credential.leased` named for LLM leases that never emit it | It is a defect in the tree today, independent of this design. | Open. The other defects were filed as F-4.9.49, F-4.9.51, F-4.9.52, F-4.9.53, and F-4.9.54, with F-4.9.50 beside them, and 0133.S2 lists each under `closes`. |
| Read the hosted tool types that `openai-responses` and `google-generate` offer and which make the provider reach the network | Open decision 1 is settled for every metered format, and its allowlist of tool types needs the list per format **[unverified]**. | Open. Waits on 0133.S2. Desk validation VAL-032 answered the desk half; Gemini `codeExecution` and whether an OpenAI stored `prompt` or `previous_response_id` enables a hosted tool absent from the body wait for a live call. D10 on 2026-11-19 made the members of one cloud's variant of a format moot. |

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
- closes: [F-4.9.48, CHEGRESS-I1, CHGWPOD-I9, CHGWPOD-I12, CHGWPOD-I15, F-4.9.32, F-4.9.33, F-4.9.34, F-4.9.37, F-4.9.38, F-4.9.39, F-4.9.40, F-4.9.41 (part), F-4.9.47 (part), F-4.7.31, F-11.4.10, F-4.9.49, F-4.9.50, F-4.9.51, F-4.9.52, F-4.9.53, F-4.9.54]

Upstreams and removal: LU-1, LU-2, the routes and session binding of LU-3, LU-4, LU-8, LU-9, LU-10, and LU-11, with the specification rewrite of §4.9's LLM half and every section of "Specification changes" that the removal reaches. It includes the `google-generate` format with its routes and its parser, one forwarder per format that sends a call to the upstream's endpoint with its credential header and extra headers, the §26 format declarations with `gemini-cli` on `google-generate`, and the statement of calls outside the proxy. It resolves the lease findings.

Its scope changed on 2026-11-19 under D10. The step no longer builds a `channel`, a `gemini-api` channel, the `workloadIdentity` credential source, or the AWS, Google Cloud, and Azure workload identity exchangers. It gains the removal of the provider-specific code and text that exist today: `AWSBedrockTranslator`, `VertexAITranslator`, and `AzureOpenAITranslator` with their tests, `rehostAnthropicBody`, the provider-keyed translator registry, the flags `--llm-bedrock-region`, `--llm-vertex-region`, `--llm-vertex-project`, `--llm-azure-endpoint`, `--llm-azure-api-version`, `--llm-openai-base-url`, `--llm-openai-organization`, and `--anthropic-version` with `cmd/lenny-gateway/testdata/flag-surface.golden`, and the provider tables of §4.9 that "LU-4" lists. With the removal it closes F-4.9.49 and F-4.9.50 as obsolete, F-4.9.51 with the stream deadline, F-4.9.52 with the forwarder keyed by format, F-4.9.53 as obsolete with the provider and dialect vocabulary, and F-4.9.54 with the endpoint of each upstream. Open decisions 11 to 15 settle the record's labels, credential header, extra headers, cache wording, and attribution defaults before the step's "LU-1", "LU-9", and "LU-11" edits.

It needs EG-2 for the removal of `provider-direct`. It deletes the `lenny-direct-mode-isolation` webhook and checks (1) and (2) of the `direct_mode_isolation` validator with `deliveryMode`, in place of 0127.S2, which removes check (3) with `provider-direct`, so that `main` does not admit a direct-mode pool on `isolationProfile: standard` or a multi-tenant pool with `spiffeBinding: disabled` between the two merges ("LU-4"); open decision 4 settles whether it restates the `spiffeBinding` refusal in the admin API's pool validation, and it decides CHEGRESS-I2.

It needs the post-chain point of RT-2 in 0141.S1 for the list check of "LU-2" **Session creation.** Until 0140.S1 lands, the check reads `llmFormats` from the runtime record, and 0140.S1 moves it onto the chosen revision as 0140's instruction from this change states. The `llm` posture section and its tests land in whichever of 0133.S2 and 0142.S2 merges second, because 0142.S2 builds the posture endpoint ("LU-8" **Posture.**). If the owner settles option (a) of open decision 41 of [`0142-security-oversight`](../0142-security-oversight/decisions.md), which splits 0142.S2, the `llm` section lands with whichever of 0133.S2 and 0142's posture step merges second.

0132.S1 and 0133.S2 rebase on whichever of them lands first, because both edit `pkg/gateway/meteringproxy/llmproxy/handler.go`, `pkg/credential/credentialpoolstore`, and the admin pool validator (0132-L8).

The metering proxy of this step keeps the refusal of a session's new calls and the cancellation of its calls in flight when the session's suspension cannot take hold, from the pod in use until that pod is released, as SLR-23 of [`0146-session-lifecycle`](../0146-session-lifecycle/change.md) states (L24). 0146.S1 lands the refusal in the proxy the tree has when it lands, so whichever of 0146.S1 and 0133.S2 lands second carries it over ("LU-3" **Session binding**).

With the `provider` path of `POST /v1/credentials` (OD-10), the step removes the `credentials` table and its gateway-side store, `credentialpg` in `cmd/lenny-gateway/credentialsurface.go` (`pkg/credential/credentialstore/pgstore`) and `pkg/credential/usercreds`, which serve only that path. 0151.S1 depends on this step for that removal (0151-F4).

The resolved lease findings are those of "Records" in `change.md`. F-4.9.41 (part) keeps its VCS half open. F-4.9.47 (part) is shared with 0151, whose UC-1 settles the wire names. F-4.9.46 is closed by 0151.S1. CHINTRA-I1 and CHGWPOD-I14 become obsolete in this step only if the validation item on `AssignCredentials` and the credentials file finds no VCS use, so they are not listed under `closes`; the step closes them when that item is resolved that way. Open decision 8, restated under D10, asks whether this step splits into smaller steps.

It deletes the rows of `tests/registers/gateway-layout.yaml` with `removed-by: '0133'` as it deletes their packages, and its diff is reviewed under "Review changes to the merge gate" of `.claude/rules/git-workflow.md` because it touches `tests/registers/`.

Under decision 8 of [0117](../0117-gateway-package-layout/decisions.md), this step edits `spec/interfaces/gateway-seams.md` and `pkg/gateway/seam` for the gateway seam operations it changes. It removes `GWSEAM-CheckChildCredentials` with the pre-claim credential check and `GWSEAM-RotateCredential` with the credential fallback (LU-4), reshapes `GWSEAM-MaterializeChild`, which no longer assigns an LLM credential lease, and removes `GWSEAM-TenantCredentialProviders` with the tenant's `credentialPolicy`. It also reshapes or removes `GWSEAM-RevokePoolCredentials` and `GWSEAM-RevokeUserLeases`, through which the admin API revokes a pool credential's leases and returns a revoked user's leases, with the credential renewal propagator and the lease revoker it removes. Removing the renewal propagator removes one of the two callers of `GWSEAM-ExpireSession` (`cmd/lenny-gateway/cred_renewal.go`), and 0133.S3 removes the operation with the other.

### 0133.S3 Rate cards and credit budgets
- status: Planned
- aliases: [LU-C, LU-5, LU-6, LU-7]
- lanes: [specification, code, documentation]
- depends-on: [0133.S2]
- closes: [F-11.2.34, F-8.3.9]

Rate cards and credit budgets: LU-5 with the fallback entry, LU-6 with the input estimate and best-effort bounds, and the estimated cost of LU-7, with §8.3, §8.6, §11.2, and §11.2.1 in credits.

Under decision 8 of [0117](../0117-gateway-package-layout/decisions.md), this step edits `spec/interfaces/gateway-seams.md` and `pkg/gateway/seam` for the gateway seam operations it changes. It reshapes `GWSEAM-ExtendForBudget` and `GWSEAM-RaiseBudget`, and the delegation budget operations (`GWSEAM-ReturnBudget`, `GWSEAM-RearmBudget`, `GWSEAM-ObserveHighWatermark`, `GWSEAM-NodeUsage`, `GWSEAM-TreeUsage`, and `GWSEAM-SubtreeTokens`), which count credits (LU-6), and `GWSEAM-HandleBudgetKeysExpired`, the tree cleanup a reservation runs when the tree's budget keys expired. Under decision 5, a refused extension no longer ends the session, so the step removes `GWSEAM-TerminateForBudget` and `GWSEAM-ExpireSession` with the in-process enforcer; 0133.S2 has already removed the operation's other caller. It removes `GWSEAM-TrackedBudgetSessions` with the in-process budget state that `budget_state_sweep.go` sweeps, and reshapes or removes `GWSEAM-ForgetBudget` according to whether the session's Redis budget record is deleted when the session ends or expires.

Open decision 6 settles the reservation protocol under Redis Cluster, and open decision 9 the future of `billing.dualControlThreshold`, before this step's §12.4, "LU-6", and §11.2.1 edits.

### Notes on the steps

0133.S2 and 0133.S3 can merge into one step if the owner prefers to change budgets in the same step as their anchor. Revision 2's LU-D, which carried the gate, the guardrail, and the posture entries of the unmetered path, and its LU-E, which carried `google-generate` and Google Cloud workload identity, are retired: OD-8 removes LU-D's mechanisms and 0133.S2 (LU-B) carries the statement of calls outside the proxy, and OD-9 moves LU-E's content into 0133.S2. D10 later removed Google Cloud workload identity from that content, so 0133.S2 carries `google-generate` alone. The labels are not reused.
