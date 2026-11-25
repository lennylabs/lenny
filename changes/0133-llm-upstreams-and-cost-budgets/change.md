# LLM credential removal and access through a gateway

Status: `Draft`, revision 28, 2026-11-24. Programs: [v1](../programs/v1.md).

The folder keeps its name, `0133-llm-upstreams-and-cost-budgets`, so that links into it keep resolving. Decision 23 sets the title. The change does two jobs: it removes Lenny's LLM credential machinery and its proxy ("LU-4"), and it states how the mechanisms that `0124-connector-removal`, `0127-egress-and-git`, and `0170-budgets` own combine into LLM access through an operator's gateway, with the reference integrations, the gateway guide, and the checks owed. Each rule of that path is stated once, in its owner, and this change cites it. Revision 20 replaces the metering proxy design of revisions 1 to 19 under decision 22, which the owner settled on 2026-11-24. `review-history.md` records the earlier revisions, and the decisions they rest on stay in `decisions.md`, marked where decision 22 supersedes them.

Each factual claim about behaviour carries one of these tags where the source matters:

| Tag | Meaning |
| :-- | :-- |
| **[source]** | Read in the Lenny tree or the specification on 2026-11-09. |
| **[external]** | Read on an external site. "Sources" lists the pages. |
| **[design]** | What this design requires. Nothing implements it. |
| **[unverified]** | An external product fact this design states without having read the product's page. "Validation owed" in `steps.md` lists each one. |

## Problem

"Terms", after "Scope", defines the vocabulary this document uses.

Lenny holds LLM provider credentials in credential pools, leases them to sessions, and either injects them in its LLM proxy (proxy mode) or writes them into the pod (direct mode). Around the pools sit assignment strategies, health scoring, cooldown, fallback chains, proactive renewal, rotation into running pods, the `CredentialRouter`, and a pre-claim availability check ([§4.9 Credential Leasing Service](../../spec/04_system-components.md#49-credential-leasing-service)) **[source]**. These are the functions of an LLM gateway. Operators who need them already run one, and Lenny's versions carry most of the open High findings in credential leasing ("Current state").

Revisions 1 to 19 of this change replaced the pools with a thin metering proxy: upstream records, routes and usage parsers for four API formats, rate cards per model in credits, a reservation per call with an input estimate, and usage records per call. Every one of those parts changes when a provider adds a model, a token class, a pricing dimension, or a streaming field, so Lenny would carry the work of keeping up with the providers' APIs. The owner asked on 2026-11-24 to remove the proxy, or every part of it that tracks models and APIs, while keeping budgets on model spend optional and best effort (decision 22).

What only Lenny can do is tell an operator's LLM gateway which tenant, user, session, and delegation tree each call belongs to, and count the gateway's charges against Lenny's budgets. The gateway already holds provider keys, routes, fails over, and prices calls.

## Scope

**Added:**

- The operator's LLM gateway as an ordinary egress destination. A runtime reaches it through `netagent` and `lenny-egress` like any other host, at a base URL that the pool's injected configuration gives it ("LU-1").
- Attribution on the gateway's host: an attribution binding under which `netagent` attaches a Lenny-signed attribution assertion, the `lenny-attribution+jwt` of [`0124-connector-removal`, "CR-2 Identity assertion"](../0124-connector-removal/change.md#cr-2-identity-assertion), to every request for the gateway's host. The agent process never sees it ("LU-2").
- Charges: the gateway prices each call and reports a charge to Lenny citing the `jti` of the call's assertion as proof, through the charge ingestion API of [`0170-budgets`, "BG-12"](../0170-budgets/change.md#bg-12-external-charges), and budgets count charges on the `externalCost` meter ("LU-3").
- Enforcement: an exhausted limit that counts `externalCost` suspends the session's attribution binding, so `netagent` answers the session's next call to the gateway itself, and `onExhausted` acts on the running session as for any meter ("LU-5").
- Reference integrations outside Lenny's core: a LiteLLM module, a charge relay, and the reference verifier ("LU-9").

**Changed:**

- The `llm` meter and the unit `llmCredits` of `0170-budgets` give way to the `externalCost` meter, counted in the deployment's currency ("LU-3", and 0170's decisions 57 and 67).
- The token limits of [§11.2](../../spec/11_policy-and-controls.md#112-budgets-and-quotas) and `maxTokenBudget` of [§8.3](../../spec/08_recursive-delegation.md#83-delegation-policy-and-lease) become limits on the `externalCost` meter in the budget objects of `0170-budgets`.
- The §8.6 extension starts when charges exhaust a limit of a session budget or a cap, and a call refused meanwhile is retried by the runtime ("LU-5").
- `CredentialPool` serves VCS providers alone.

**Removed:**

- LLM providers on `CredentialPool`, LLM credential leases, `credentialPolicy`, direct delivery mode, the `CredentialRouter`, assignment strategies, health scoring, cooldown, fallback, proactive renewal, rotation of LLM credentials into pods, `hotRotation`, `supportedProviders`, `credentialPropagation`, and the pre-claim credential availability check ("LU-4").
- The LLM proxy: its listener and routes, the proxy token, the `llm` member of `session_start`, the provider-specific translators, the channel `CH-LLMPROXY`, and the `PreLLMRequest` and `PostLLMResponse` interceptor phases ("LU-4").
- The direct-mode usage path: `ReportUsage`, `FINAL_USAGE_REPORT`, and the token-usage anomaly detector. `llm_request_started` and `llm_request_completed` stay as the runtime's own report of a call ("LU-6").
- `provider-direct`, NET-006, NET-044, and the `lenny-direct-mode-isolation` webhook, which [`0127-egress-and-git`](../0127-egress-and-git/change.md#consolidating-provider-direct) already proposes to remove with direct mode's last consumer.
- `vault_transit` and the `provider` path of `POST /v1/credentials` (OD-10).
- `llmFormats` on the runtime record and the check at the post-chain point that a session's upstream list serves one of them.

**Out of scope:**

- Holding provider keys, routing, failover, pooling, model allowlists, pricing, prompt caching, and guardrails on prompts. The operator's LLM gateway does each of them.
- Any parsing of a model call's body or response. Lenny reads no model name, token class, or price.
- Budgets for model calls that reach no gateway that reports charges. A runtime may call a provider directly with a key it holds, over whatever egress its pool permits, and Lenny neither blocks that traffic nor reports it as LLM traffic ("LU-7").
- Currency conversion, invoices, and rating. Charges arrive in `budgets.currency`.
- The removal of Lenny's semantic response cache, which [`0132-llm-response-cache-removal`](../0132-llm-response-cache-removal/change.md) owns as RC-A.

## Terms

| Term | Meaning |
| :-- | :-- |
| LLM gateway | An operator-run proxy, such as LiteLLM or Envoy AI Gateway, that holds provider keys, routes calls, prices them, and reports charges to Lenny. Lenny reaches it as an egress destination. |
| Gateway host | A host of an LLM gateway that an attribution binding names. |
| Attribution binding | A credential binding of [`0127-egress-and-git`](../0127-egress-and-git/change.md#credential-bindings) whose secret is an attribution assertion and whose hosts are gateway hosts. `netagent` attaches the assertion under `delivery: inject`. A `platform-admin` writes one at platform or pool scope for a gateway that serves every tenant, and a `tenant-admin` writes one for its own gateway. |
| Attribution assertion | The short Lenny-signed token `lenny-attribution+jwt` of [`0124-connector-removal`, "CR-2 Identity assertion"](../0124-connector-removal/change.md#cr-2-identity-assertion) **Attribution assertion.**, naming the session's tenant, environment, pseudonymous user, session, root session, and depth. It attributes spend and authenticates no user. |
| Charge | A priced amount that a reporter sends to Lenny for one call, naming the call's session through its attribution assertion ([`0170-budgets`, "BG-12"](../0170-budgets/change.md#bg-12-external-charges)). |
| Reporter | A registered client of the charge ingestion API, scoped to one tenant or to the platform, and bound to the gateway hosts whose assertions it may present. |
| `externalCost` meter | The meter of `0170-budgets` that counts charges in the deployment's currency, `budgets.currency`. It replaces the `llm` meter, and 0170's decision 67 renamed it from `external`. |
| Binding suspension | The state in which `netagent` stops attaching a session's attribution binding and answers the session's requests to the binding's hosts itself, and the Token Service mints no assertion for it ([`0127-egress-and-git`, "Credential bindings"](../0127-egress-and-git/change.md#credential-bindings) **Binding suspension.**). |
| Calls outside the gateway | A model call that a runtime sends to a provider or a gateway that no attribution binding names, or with a key the runtime holds. Lenny applies no budget to it ("LU-7"). |
| LU-n, LUR-n, and LU-A to LU-C | The parts of this design under "Detailed design" carry LU-n, and its requirements carry LUR-n. LU-A, LU-B, and LU-C name the steps 0133.S1, 0133.S2, and 0133.S3 of `steps.md`. The LU-n and LUR-n labels of revisions 1 to 19 are retired with their sections, and revision 20 assigns them anew. |

### Terms from other designs and the platform

| Term | Meaning |
| :-- | :-- |
| Credential pool | A tenant-scoped, admin-managed set of credentials for one provider ([§4.9](../../spec/04_system-components.md#credential-pool)). Today it holds LLM and VCS credentials. After this design it holds VCS credentials alone. |
| Credential lease | Today, the LLM credential assigned to a session, with `materializedConfig`. This design removes it for LLM credentials. |
| `deliveryMode` | Today, the pool setting `proxy` or `direct`. This design removes it. |
| `netagent` | The per-pod network agent of [`0127-egress-and-git`](../0127-egress-and-git/change.md) that terminates TLS for bound hosts and attaches credentials the agent process never sees. |
| Credential binding | The egress design's object that names a secret source, the hosts it serves, and a `delivery` mode ([`0127-egress-and-git`, "Credential bindings"](../0127-egress-and-git/change.md#credential-bindings)). |
| Identity assertion | The CR-2 token `lenny-identity+jwt`, which names the session's user to an operator service and which D11 of 0124 restricts to pods that no other session used. |
| Budget object, cap, and session budget | The records of [`0170-budgets`](../0170-budgets/change.md). |
| Lease extension | The [§8.6](../../spec/08_recursive-delegation.md#86-lease-extension) operation that raises a session's budget in `auto` mode or after a person approves an elicitation. |
| Egress halt | The halt of a slot's egress when its suspension cannot take hold ([`0124-connector-removal`, "CR-4 Egress halt at suspension"](../0124-connector-removal/change.md#cr-4-egress-halt-at-suspension)). |
| `llm_request_started` and `llm_request_completed` | The runtime frames by which a runtime reports a model call it sends, the second optionally with `inputTokens` and `outputTokens` ([§28.5.3](../../spec/28_communication-channels.md#2853-intra-pod)). |
| `platform-admin` and `tenant-admin` | Lenny's administrative roles for the platform and for one tenant. |
| BUILD-GAPS | `BUILD-GAPS.md` at the repository root, the audit of gaps between the specification and the code, with findings named F-n.n.n. |
| Test tiers | The tiers of `.claude/rules/test-coverage.md`, "The tier model", by name. |

## High-level design

Lenny stops brokering LLM credentials and stops proxying model calls. An operator that wants model spend in Lenny's budgets runs an LLM gateway, registers its host in an attribution binding, and registers the gateway as a reporter.

```
 Runtime (slot)          netagent (pod)                 Operator's LLM gateway           Lenny
 ──────────────          ──────────────                 ──────────────────────           ─────
 POST http://llm-gw/...  1. attribute the connection
   (placeholder key,        to the slot's session
    no identity)         2. binding suspended? -> answer
                            429 (extension pending)
                            or 403 (final) itself
                         3. strip any inbound copy of
                            the assertion header, attach
                            the session's assertion  ──>  4. verify assertion
                         (TLS via lenny-egress)            (signature, aud, exp)
                                                          5. own key, routing,
                                                             model policy -> provider
                                                          6. price the call
                                                          7. batch charges, ─────────>  8. look up each jti in
                                                             each citing its               the mint index, count
                                                             assertion's jti               on `externalCost`
                                                                                         9. limit exhausted ->
                                                                                            suspend the binding,
                                                                                            onExhausted, §8.6
```

**The gateway as a destination (LU-1).** The gateway is a host in the tenant's egress allow set, or a private egress upstream that the platform registers. The runtime calls it with its own SDK at the base URL the pool's injected configuration names, with no key of its own.

**Attribution (LU-2).** An attribution binding on the gateway's host makes `netagent` attach the session's attribution assertion to each request. The gateway verifies the assertion against Lenny's published keys and reads the session, tenant, and pseudonymous user from it. By default the assertion is the gateway's caller credential in `Authorization: Bearer`.

**Charges (LU-3).** The gateway prices each call and posts charges in batches to `POST /v1/charges`, each charge citing the `jti` of its call's assertion. Lenny counts accepted charges on the `externalCost` meter of the budgets of the session's set that each charge's source reaches, as 0170 "BG-12" **Counting by source.** states. The session's owner can also post charges for work outside the pod, which count in its own tree and principal budgets alone (0170's decisions 61 and 71).

**Removal (LU-4).** The proxy, the LLM credential pools, the leases, direct mode, and the machinery around them leave the specification and the tree. VCS pools stay.

**Enforcement (LU-5).** Budgets on `externalCost` are best effort. When a limit is exhausted, Lenny suspends the attribution bindings of the sessions that charge it, and `netagent` answers each session's next call to the gateway itself: `429` with `Retry-After` while an extension is pending, and `403` naming the budget once the exhaustion is final. `onExhausted.running` suspends or ends a running session as for any meter, and a session budget or a cap starts the §8.6 extension.

**Usage (LU-6).** A charge may carry the call's token counts in a neutral schema, which usage events show. A runtime's own reports stay displayed and read by no budget.

**Calls outside the gateway (LU-7).** A call to a host no attribution binding names is ordinary egress, with no budget.

"Limits" states what the change costs: no refusal before spend, an overshoot of the report delay plus the calls in flight, no interceptor or leak scan on model calls, no idle evidence from model calls, and no LLM budget without a gateway that reports charges.

## Requirements

| ID | Requirement |
| :-- | :-- |
| LUR-1 | Lenny holds no LLM provider credential and parses no model call's request or response. |
| LUR-2 | A request that `netagent` forwards to a host of an attribution binding of the session carries the session's attribution assertion in the binding's header, and no value of that header that the runtime sent. |
| LUR-3 | No assertion is attached to a request for a host that no attribution binding of the session names, and none while the binding is suspended, during which `netagent` answers the session's requests to the binding's hosts itself. |
| LUR-4 | A charge counts in a budget only as BGR-13 of `0170-budgets` states: its proof verifies, it arrives in time, and each charge identifier counts once per reporter. |
| LUR-5 | When a limit that counts `externalCost` is exhausted, Lenny suspends the attribution bindings of every session that charges it, the tree for the root's limit and the capped subtree for a cap, within the binding propagation delay, and lifts the suspension when an extension, an adjustment, a raise, or a void restores room. |
| LUR-6 | Budget enforcement on `externalCost` is best effort. A limit exceeds its amount by no more than the charges of the calls the gateway admitted before the suspension took hold, plus the report delay of the charges in flight. |
| LUR-7 | A runtime's own usage report is displayed as `source: runtime` usage, and no budget, quota, or billing event reads it. |

## Current state

### Specification

- **§4.9 Credential Leasing Service [source].** The section runs from its heading to the end of `spec/04_system-components.md`. Its subsections are **Credential Provider**, **Credential Pool**, **Credential Storage Topology**, **Pre-Claim Credential Availability Check**, **Credential Lease**, **`materializedConfig` Schema by Provider**, **Credential Policy**, **Three Credential Modes**, **Pre-Authorized Credential Flow**, **Fallback Flow** with the `rotationTrigger` enum, **Proactive Lease Renewal** with **Rotation mode resolution.**, **LLM Reverse Proxy** with **Proxy dialects.**, **Authoritative token counting.**, and **Per-pool configuration.**, **Semantic Caching**, **`CredentialRouter` Interface** with **External LLM routing proxy integration.**, **Credential Governance Boundaries**, **Emergency Credential Revocation**, **Security Boundaries**, **Credential Lifecycle and Secret Management**, §4.9.1 KMS key rotation, and §4.9.2 audit events. The **Credential Provider** table says the runtime receives "Short-lived STS session credentials" for `aws_bedrock`, a short-lived access token for `vertex_ai`, a short-lived token for `azure_openai`, a static API key for `anthropic_direct`, and a short-lived Vault token for `vault_transit`.
- **Proxy dialects [source].** §4.9 defines `openai` and `anthropic`. The [§26](../../spec/26_reference-runtime-catalog.md#262-shared-patterns-for-coding-agent-runtimes) catalog declares `proxyDialect: [google]` for `gemini-cli` and `[cursor]` for `cursor-cli`, and names providers (`openai_direct`, `gcp_vertex_gemini`, `google_ai_studio`, `cursor_direct`) that the §4.9 provider table does not list. **Authoritative token counting.** maps Anthropic, OpenAI, and Vertex input and output counts to `input_tokens` and `output_tokens`. No spec text names `cache_read_input_tokens`, `cache_creation`, or `cached_tokens`.
- **Runtime fields [source].** [§5.1](../../spec/05_runtime-registry-and-pool-model.md#51-runtime) carries `supportedProviders` and `credentialCapabilities` (`hotRotation`, `proxyDialect`) only in its YAML examples and its merge table, where `supportedProviders` may be restricted by a derived runtime and `credentialCapabilities` is an override.
- **Delegation [source].** [§8.3](../../spec/08_recursive-delegation.md#83-delegation-policy-and-lease) carries `maxTokenBudget` on the lease and on `lease_slice`, reserves tree budgets with `budget_reserve.lua`, waits for `ReportUsage` and `FINAL_USAGE_REPORT` before returning a child's budget, and defines **Credential propagation:** with `inherit`, `independent`, and `deny`. Item 5, **Over-run semantics.**, calls the slice "a **soft cap enforced at settlement**, not a per-call hard ceiling at the LLM provider".
- **Lease extension [source].** [§8.6](../../spec/08_recursive-delegation.md#86-lease-extension), **Trigger:**, as proposal 0023 left it: "The gateway LLM Proxy detects budget exhaustion for a proxy-mode session when it settles the token usage of a completed upstream call". A granted extension delivers a non-streaming response the proxy already holds. An extension still pending at `proxyExtensionWaitTimeout` returns `BUDGET_EXHAUSTED` on a non-streaming call and lets a committed stream stand. Direct mode is "not eligible for an automatic in-path extension".
- **Budgets [source].** [§11.2](../../spec/11_policy-and-controls.md#112-budgets-and-quotas) has the row "Token limits (LLM tokens)" with per-request, per-session, per-user window, per-tenant window, global window, and per-task-tree scopes. **Quota Update Timing:** makes proxy counts authoritative and pulls direct-mode counts over `ReportUsage`, of which "a malicious runtime could underreport". During a Redis outage the gateway enforces per session against the per-replica ceiling of §12.4.
- **Billing [source].** [§11.2.1](../../spec/11_policy-and-controls.md#1121-billing-event-stream) emits `token_usage.checkpoint` with `tokens_input` and `tokens_output`, and `credential.leased` with `credential_pool_id`, `credential_id`, and `delivery_mode`. **Correction semantics** replace `tokens_input`, `tokens_output`, and `pod_minutes` alone. Category 1 `GATEWAY_CRASH_RECONSTRUCTION` reconciles "from pod-reported token totals". `billing.dualControlThreshold` has no stated unit.
- **Network [source].** [§13.2.6](../../spec/13_security-model.md#1326-per-pool-egress-relaxation-and-egressprofile) pairs `deliveryMode: direct` with `egressProfile: provider-direct` (NET-006) and excludes IMDS from it (NET-044). [§13.2.5](../../spec/13_security-model.md#1325-gateway-egress-to-upstream-llm-providers) renders the gateway's upstream egress rule "whenever `credentialPools[*].deliveryMode: proxy` is present".
- **Intra-pod frames [source].** [§28.5.3](../../spec/28_communication-channels.md#2853-intra-pod) defines `credentials_rotated`, `credentials_acknowledged`, `llm_request_started`, and `llm_request_completed` with optional `inputTokens` and `outputTokens` on `CH-RUNTIMEOPS`, and the `llm` member of `session_start` on `CH-MSGSOCK` with `deliveryMode`, `dialect`, `apiKeyEnv`, and `headers`.

### Tree

| Area | What exists **[source]** |
| :-- | :-- |
| Proxy routes | `newLLMProxyServer` (`cmd/lenny-gateway/llmproxy_server.go`) mounts `POST /llm-proxy/v1/messages` alone. `Handler.ServeHTTP` (`pkg/gateway/llmproxy/llmproxy/handler.go`) builds every request with `DialectAnthropic`. `DialectOpenAI` and `DialectOpenAIResponses` exist in `translator.go` with no route (F-4.9.48). |
| Translators | `AnthropicDirectTranslator` forwards the body. `AWSBedrockTranslator` and `VertexAITranslator` rehost it with `rehostAnthropicBody`, which deletes `model` and sets `anthropic_version`. `AzureOpenAITranslator` and `OpenAIDirectTranslator` accept only `DialectOpenAI`. `OpenAIResponsesTranslator` is not registered by `buildLLMTranslatorRegistry`, and its `Provider()` returns `ProviderOpenAIDirect`, the key `OpenAIDirectTranslator` also uses. No cross-format translator exists. |
| Bedrock and Vertex | `AWSBedrockTranslator` sends a Bedrock API key as `Authorization: Bearer`, "in contrast to AWS SigV4's access-key pair", to `bedrock-runtime` `/model/{id}/invoke` for every request, streaming included. `VertexAITranslator` sends a static string as a bearer access token with no refresher. |
| Credential exchange | No STS, Google token, or Entra client serves LLM credentials. `AssumeRole` appears only in a comment in `ProviderMaxTTLSeconds` (`pkg/credential/lease_mint.go`). `azidentity` serves KMS alone (`pkg/kms/providerflags/azure.go`). A pool credential holds a static `APIKey` or an operator-supplied `Materialized` bundle (`PoolCredential`, `directConfigFor`, and `credentialSecret` in `pkg/gateway/credentials/credassign/credassign.go`). `credentialpoolstore.Credential.RoleArn` is stored and never used. |
| Usage extraction | `Usage` (`translator.go`) has `InputTokens` and `OutputTokens`. `extractAnthropicUsage` and `applySSEUsage` (`sse.go`) read only those. On a stream, input comes from `message_start` and output from the last `message_delta`. `extractOpenAIUsage` and `extractOpenAIResponsesUsage` read no cached details. No code reads `cache_read_input_tokens` or `cache_creation_input_tokens` (F-11.2.38). |
| Usage record | `proxyUsageRecorder.RecordUsage` (`cmd/lenny-gateway/llmproxy_usage.go`) writes `usagestore.Record` (`pkg/gateway/billing/usagestore`) with tenant, runtime, labels, and two token counts. No provider, model, pool, credential, or session is recorded. |
| Budget enforcement | `BudgetGate` is implemented by `sessionbudget.Enforcer` (`pkg/gateway/session/sessionbudget`), an in-process map per replica. `Allow` refuses only after a session is marked exhausted. `Enforcer.Record` detects exhaustion after the upstream answers and calls the §8.6 seam (`leaseExtendSeam`, `leasecontrol.Service.ExtendForBudget`). A non-streaming call whose extension is not granted returns `BUDGET_EXHAUSTED` in place of a response the provider has billed, and a stream is never stopped. No reservation precedes a call. |
| Reservation scripts | `budget_reserve.lua` is the embedded `reserveScript` in `pkg/gateway/mcpfabric/delegationtree/treebudget`, run at `delegate_task` admission for tree axes. `storage_quota_reserve.lua` is the embedded script in `pkg/gateway/quota/storagequota/redisstore`. Neither runs per LLM call. |
| Session authentication | `leaseToken` reads `x-api-key` or `Authorization: Bearer`. `ValidateProxyRequest` (`pkg/credential/lease.go`) checks expiry, the deny list, and the peer SPIFFE URI when the lease carries one. |
| Interceptors and breaker | `Handler.runLLMPhase` runs `PreLLMRequest` before translation and `PostLLMResponse` after the response, and once over stream metadata. One `subsystem.Breaker` is shared by every provider. |
| Upstream timeout | `Forwarder.client()` is an `http.Client` with `Timeout: defaultUpstreamTimeout` (120 seconds), which `ForwardStream` also uses. A client timeout bounds reading the body, so a stream longer than 120 seconds is cut. |
| Router and pre-claim | `credrouter.Router` and `credrouter.PreClaim` (`pkg/gateway/llmproxy/credrouter`), called from `resolveCredentialPools` in `pkg/gateway/sessionserver/credential_preclaim.go`. |
| Direct-mode usage | `ReportUsage` in `schemas/lenny-adapter.proto`, served by `pkg/adapter/usage.go` and pulled by `cmd/lenny-gateway/direct_usage.go` and `direct_usage_recovery.go`. `llm_request_completed` is read in `pkg/adapter/runtimeops.go`. `lenny_gateway_token_usage_anomaly_total` (`pkg/gateway/session/tokenanomaly`) observes direct-mode deltas alone. |
| Billing | `billingfanout.TokenUsageCheckpoint` carries tenant, session, user, and two token counts. `billingfanout.CredentialLeased` is called only by `mcpVCSLeaseAuditor.RecordVCSLease` for VCS mints, so no LLM lease emits `credential.leased`. |
| User credentials | `POST /v1/credentials` (`credentialserver`) takes a `provider` body field checked by `Provider.IsValid()`. `UserProxyDialect` maps `anthropic_direct`, `azure_openai`, `vertex_ai` (to a `google` dialect no route serves), and `cursor_direct`, so only `anthropic_direct` user credentials work through the proxy today. `vault_transit` exists only as an enum value and its required fields, and no Vault client is in the tree. |
| Open Responses and Chat Completions surfaces | `OpenResponsesTokensDetails.CachedTokens` in `pkg/gateway/environment/translator/open_responses.go` is never assigned, so the client-facing Open Responses usage reports `cached_tokens: 0`. The Chat Completions surface builds `OpenAIUsage` with zeros. |
| VCS pools | `ResolveVCSPool` (`credentialpoolstore/vcsresolve.go`) and `vcscred.StoreResolver` (`pkg/gateway/provisioning/vcscred`) read VCS pools through the Token Service. |
| Size | Non-test and test Go lines: `pkg/gateway/credentials` 7,619 and 8,258, `pkg/gateway/llmproxy` 3,365 and 4,250, `pkg/credential` 1,653 and 1,591, and `pkg/tokenservice` 2,881 and 5,185. `cmd/lenny-gateway` wires them in `cred_renewal.go`, `cred_fallback.go`, `credential_lease_sweep.go`, `credential_lease_backfill.go`, `credential_denylist_rebuild.go`, `credential_revocation.go`, `executor_credentials.go`, `llmproxy_usage.go`, and others. The adapter delivers direct-mode leases in `pkg/adapter/credentials.go`, `credexpiry.go`, `slotcreds.go`, and `pkg/adapter/credfile`. |
| Operator guide | `docs/operator-guide/external-llm-proxy.md` describes Pattern A, the external proxy's key delivered in direct mode, and Pattern B, the external proxy as the base URL of a proxy-mode pool. No per-pool base URL exists for Pattern B: `AnthropicDirectTranslator.BaseURL` is one process-wide field that `buildLLMTranslatorRegistry` never sets. |

### Records

| Record | State | Relation |
| :-- | :-- | :-- |
| F-4.9.48 | Open, High | The missing OpenAI routes. Obsolete under LU-B, which removes the proxy. |
| F-11.2.38 | Open, High | The cached-token undercount. Obsolete under LU-B, which removes the proxy and its usage parser. |
| F-4.9.32, F-4.9.33, F-4.9.37, F-4.9.39 | Open, High | Lease release, fallback exhaustion, the expired-lease sweep, and per-replica credential counts. LU-B resolves each as obsolete, because the machinery leaves. |
| F-4.9.34, F-4.9.38, F-4.9.40, F-4.9.41 | Open, Medium | Lease release retry, the user-credential availability check, lease-store errors, and the deny-list key. Obsolete under LU-B for the LLM half. F-4.9.41 is re-checked for VCS pools. |
| F-4.9.46, F-4.9.47 | Open, Medium | The inert `provider` values and the `/v1/credentials` wire names. Resolved by OD-10 and UC-1. |
| F-4.7.31, F-11.4.10 | Open | The rotation in-flight gate and the lease set `full_revoke` revokes. Obsolete for LLM leases. |
| F-8.3.9 | Open, Medium | No `FINAL_USAGE_REPORT` ends the usage quiescence wait. 0166.S2 closes it with the count of a tree at its root, which leaves nothing to return when a child ends, and LU-B removes `FINAL_USAGE_REPORT`. |
| F-11.2.34 | Open, Medium | Budget termination lost to a crash, because the enforcer counts in process memory. LU-B removes the in-process enforcer, and 0170.S1 closes the finding with budget counters in Redis. |
| F-4.9.29 | Open, Medium | No adapter code fills `llm.headers`. Obsolete under LU-B, which removes the `llm` member. |
| F-8.6.6 | Closed | Proposal 0023 placed the §8.6 trigger in the proxy at settlement. LU-B moves it to the exhaustion of a limit by charges. |
| F-4.9.49, F-4.9.50 | Open, High | The Bedrock translator sends streams to the non-streaming `invoke` operation, and the gateway mints no short-lived credential for Bedrock or Vertex AI. Obsolete under LU-B, which deletes both translators (D10). |
| F-4.9.51 | Open, High | The 120-second client timeout cuts a stream. Obsolete under LU-B, which removes the proxy. |
| F-4.9.52 | Open, Low | `OpenAIResponsesTranslator` is unregistered and shares a provider key. Obsolete under LU-B. |
| F-4.9.53 | Open, Medium | The provider and dialect names differ between §4.9, §26, and `pkg/credential`. Obsolete under LU-B, which removes providers, dialects, and `UserProxyDialect`. |
| F-4.9.54 | Open, High | Pattern B has no per-pool upstream base URL. Obsolete under LU-B: the gateway's base URL is in the pool's injected configuration. |

The defect that §11.2.1 names `credential.leased` for LLM leases while no LLM lease emits it has no finding yet. "Validation owed" in `steps.md` lists it for filing.

## Detailed design

### LU-1 The LLM gateway as an egress destination

The operator runs the gateway and makes it reachable as any egress destination: a public host in the tenant's effective allow set ([`0127-egress-and-git`, "Effective policy"](../0127-egress-and-git/change.md#effective-policy)), or a private egress upstream that the platform registers and grants ("Configuration by scope" of the egress design) **[design]**. The pool's injected configuration ([`0127-egress-and-git`, "Injected configuration"](../0127-egress-and-git/change.md#injected-configuration)) names the gateway's base URL for each SDK the runtime uses, such as `ANTHROPIC_BASE_URL`, `GOOGLE_GEMINI_BASE_URL`, or a Codex provider block, and the placeholder key `lenny-injected` where an SDK refuses to start without one. A plain `http://` base URL lets `netagent` upgrade the request to TLS ("Delivery modes", **Plain HTTP to a bound host.**), so an SDK that does not trust the per-pod CA still works, subject to the client checks in "Validation owed". The gateway holds the provider keys, and no provider key reaches a pod on this path (LUR-1). One gateway serves every tenant through a platform or pool binding, or one tenant through a tenant binding (0127's decision 84), and it applies its own keys, routing, and model policy per tenant from the verified assertion.

### LU-2 Attribution on the gateway's host

An attribution binding names the gateway's host **[design]**:

```yaml
credentialBindings:
  - name: llm-gateway-attribution
    hosts: [llm-gw.lenny-system.svc]
    secret:
      kind: attributionAssertion
      lifetimeSeconds: 300
    attach: "Authorization: Bearer {{secret}}"
    delivery: inject
    scope: always
```

[`0127-egress-and-git`, "Credential bindings"](../0127-egress-and-git/change.md#credential-bindings) states the binding, its scopes, how `netagent` attaches it per slot, and the removal of the SDK's placeholder on its hosts. [`0124-connector-removal`, "CR-2 Identity assertion"](../0124-connector-removal/change.md#cr-2-identity-assertion) **Attribution assertion.** states the assertion: its claims, key set, header (D15), lifetime, mint index, hosts bound to one tenant, the pods it is minted on, and its residual on reused pods. The gateway verifies the assertion as CR-2's verifier states, or behind the reference verifier of "LU-9", and refuses a request without a valid one. A gateway that keeps its own caller keys takes the assertion in another header, such as `Lenny-Attribution`, as D15 of 0124 states.

### LU-3 Charges and budgets

The gateway prices each call with its own prices and reports one charge per call to `POST /v1/charges`, citing the `jti` of the call's assertion **[design]**. [`0170-budgets`, "BG-12"](../0170-budgets/change.md#bg-12-external-charges) owns the reporter registry, batching, the proof checks, idempotency, the report delay, voids, and the `external_charges` record, and budgets count charges on the `externalCost` meter in the deployment's currency (0170's decisions 57 and 67). The same API serves any other paid service: one that the runtime calls through egress cites the `jti` of its own attribution or identity binding, and the cost of a service that the session's owner calls reaches budgets when the owner posts it as a direct charge, which counts in the session's tree and the owner's own principal budgets alone (0170's decisions 61 and 71).

**Two ways to enforce (decision 26).** A deployer chooses between two setups, and may use both:

- **Charges with Lenny's budgets.** The gateway reports a charge per call, and Lenny's budgets on `externalCost` count them, with caps on children, extensions, thresholds, and the `onExhausted` actions of 0170. This is the precise setup, and it needs a reporter.
- **External enforcement.** The gateway enforces its own limits, such as LiteLLM's team budgets, and reports no charge. When a session hits one, the gateway's reporter sends a limit notice ("BG-12" **Limit notices.**), or the runtime sends the `external_limit` frame of [`0139-runtime-protocol`, "RP-9"](../0139-runtime-protocol/change.md#rp-9-external-limits). Either suspends the session and its subtree as a budget suspension does, and only the root's client resumes it ([`0146-session-lifecycle`](../0146-session-lifecycle/change.md#sl-4-tree-suspension), L39). This is the simple setup, and it needs no charge reporting.

### LU-4 Removal

The following leave the specification and the tree **[design]**. "Specification changes" and "Non-specification changes" give each site.

| Mechanism | Replacement |
| :-- | :-- |
| LLM providers on `CredentialPool` (`anthropic_direct`, `aws_bedrock`, `vertex_ai`, `azure_openai`, and the catalog's other LLM names) | LLM upstreams. The admin API refuses an LLM provider on a pool. |
| `vault_transit` (OD-10) | None. |
| `credentialPolicy` on the tenant and in the workspace plan | None. The gateway chooses keys per tenant from the verified assertion. |
| LLM `CredentialLease`, `materializedConfig` for LLM providers, and the lease token | The attribution binding ("LU-2"). |
| `deliveryMode` and direct mode, with the credentials file, which [`0118-agent-pod-layout`, "PL-3"](../0118-agent-pod-layout/change.md#pl-3-runtime-visible-filesystem-layout) places at `/run/lenny/credentials.json` on the slot's run volume, the adapter's expiry timer, and `ExtendCredentialLease` | The gateway path ("LU-1"), and calls outside the gateway ("LU-7"). The `lenny-cred-readers` group that delivered the file leaves with the pod layout, under which the adapter writes the file with mode `0444` and no volume relies on group permissions ([PL-5](../0118-agent-pod-layout/change.md#pl-5-volumes-size-limits-and-eviction)). |
| `CredentialRouter`, assignment strategies, `maxConcurrentSessions` per credential, health scoring, and cooldown | An LLM gateway behind an upstream. |
| Fallback chains, `CREDENTIAL_FALLBACK_EXHAUSTED`, and `maxRotationsPerSession` | None. A failed call returns the provider's error. |
| Proactive renewal, rotation into pods, `rotationTrigger`, `credentials_rotated`, `credentials_acknowledged`, `hotRotation`, and §4.7.8 | Secret rotation in the gateway, which the next call reads. |
| The pre-claim availability check and `CREDENTIAL_POOL_EXHAUSTED` | None. A call the gateway cannot serve returns the gateway's error. |
| `supportedProviders` | None. |
| `credentialPropagation` and `CREDENTIAL_PROVIDER_MISMATCH` | Each child holds its own attribution bindings under their `scope`. |
| `ReportUsage`, `FINAL_USAGE_REPORT`, the token-usage anomaly detector, and `TokenUsageAnomaly` | Charges on the `externalCost` meter ("LU-3"). `llm_request_started` and `llm_request_completed` stay with their optional counts as the runtime's own report, shown as `source: runtime` usage under MD-20 and read by no budget, quota, or billing event ("LU-6"). |
| `provider-direct`, NET-006, NET-044, and `egressCIDRs.providers` | None, as EG-2 already proposes. 0127.S2 removes them, with check (3) of the `direct_mode_isolation` validator, which refuses `deliveryMode: proxy` with `egressProfile: provider-direct`. |
| The `lenny-direct-mode-isolation` webhook with `allowDirectModeStandardIsolation` (`charts/lenny/templates/admission-policies/direct-mode-isolation-webhook.yaml` and its chart test, `pkg/admission/webhook/direct_mode_isolation.go`), and checks (1) and (2) of the `direct_mode_isolation` validator (`pkg/admission/direct_mode_isolation`) that the admin API's pool validation calls (`pkg/gateway/api/admin/pools.go`) | None for direct mode, which this step removes. 0133.S2 deletes them with `deliveryMode`, in place of 0127.S2, which removes check (3) with `provider-direct`, so `main` never admits a `deliveryMode: direct` pool on `isolationProfile: standard`, or a multi-tenant pool with `spiffeBinding: disabled`, between the two merges. The multi-tenant refusal of `spiffeBinding: disabled` leaves with the proxy token it protected (decision 22). |
| The LLM rows of the credential deny list and LLM emergency revocation | Binding suspension, and the gateway's own key rotation. |
| The `provider` path of `POST /v1/credentials` (OD-10), with the `credentials` table and its gateway-side store (`credentialpg` in `cmd/lenny-gateway/credentialsurface.go`, which is `pkg/credential/credentialstore/pgstore`, and `pkg/credential/usercreds`), which serve only that path | None in Lenny. A downstream LLM gateway holds per-user provider keys and selects them from the forwarded user attribute (D8). The typed credentials of [`0151-user-credential-delivery`](../0151-user-credential-delivery/change.md#uc-1-credential-types-and-the-registered-source) use a new table in the Token Service's store, and 0151.S1 depends on 0133.S2 (UC-1 **Storage.**). |
| The provider-specific translators `AWSBedrockTranslator` (`bedrock_translator.go`), `VertexAITranslator` (`vertex_translator.go`), and `AzureOpenAITranslator` (`azure_translator.go`) in `pkg/gateway/meteringproxy/llmproxy` with their tests, `rehostAnthropicBody`, the provider-keyed `TranslatorRegistry` with `Translator.Provider()` and the provider constants, and the flags `--llm-bedrock-region`, `--llm-vertex-region`, `--llm-vertex-project`, `--llm-azure-endpoint`, `--llm-azure-api-version`, `--llm-openai-base-url`, `--llm-openai-organization`, and `--anthropic-version` with their environment variables (D10) | None. The proxy leaves (decision 22). |
| The provider tables of §4.9: the `anthropic_direct`, `aws_bedrock`, `vertex_ai`, and `azure_openai` rows of **Credential Provider**, the per-provider lease TTL table and the `aws_bedrock` example of **Credential Pool**, the LLM rows of **Secret shape**, the LLM provider rows of **`materializedConfig` Schema by Provider**, the user-scoped row's "user-specific Bedrock roles", and, in **LLM Reverse Proxy**, the translator list ("Anthropic Messages API, Bedrock Converse/InvokeModel, Vertex AI `generateContent`, Azure OpenAI"), **Request flow:** item 4's provider lookup, the Bedrock and Vertex version mapping of **Proxy dialects.**, the per-provider field names of **Authoritative token counting.**, **Supported upstream providers (v1 target set):**, and the provider set of **External LLM routing proxy integration.** (D10) | The subsection **LLM gateways and external charges**, which names no provider. |
| The LLM proxy: the listener, `POST /llm-proxy/...` routes for every format, the forwarders and translators, usage normalization, the stream rules, the body edits, the `CH-LLMPROXY` channel, and `pkg/gateway/meteringproxy` | The operator's LLM gateway, reached through egress ("LU-1"). |
| Upstream records, grants, tenant and pool upstream lists, `llmUpstreamsPolicy`, `llm.tenantEndpointHosts`, rate cards, credits, the input estimate, reservations per call, the per-call ceiling, `quotas.llmProxy.maxConcurrentCallsPerSession`, and `llm_usage` | The gateway's own configuration and pricing, and charges on the `externalCost` meter ("LU-3"). |
| The proxy token and the `llm` member of `session_start` | The attribution binding ("LU-2") and the gateway's base URL in the pool's injected configuration ("LU-1"). |
| `PreLLMRequest` and `PostLLMResponse` | None in Lenny. A deployer that needs guardrails on prompts runs them in its gateway. |
| `llmFormats` and the check at the post-chain point that a session's list serves one of them | None. |

**Writes that name a removed field (D9) [design].** A write that names `deliveryMode`, `credentialPolicy`, `supportedProviders`, `credentialCapabilities`, `credentialPropagation`, `rotationTrigger`, and `maxRotationsPerSession` is refused with `400 VALIDATION_ERROR`, whose `details.field` names the member, whatever value it carries. The refusal covers every surface that accepted the field: the admin API's runtime, pool, and tenant writes, the bootstrap seed, `lenny-ctl runtime validate`, derived-runtime writes, session creation for `credentialPolicy` in the workspace plan, and delegation leases for `credentialPropagation`. A nested member such as `credentialCapabilities.hotRotation` is refused through its parent. The names stay in the validators' lists of removed fields and nowhere else. A pool that names an LLM provider is refused as the first row of the table states.

`CredentialPool` keeps VCS providers, `hostPatterns`, the Secret topology, the Token Service secret read, emergency revocation of VCS credentials, KMS key rotation, and the audit events that VCS mints emit. Whether the adapter's credential file and `AssignCredentials` serve any VCS path is confirmed before deletion ("Validation owed" in `steps.md`).

### LU-5 Enforcement

Enforcement on the `externalCost` meter is best effort **[design]**. A charge counts in the session budget of the session's tree, kept at its root, in the cap of every capped session on the session's path to the root, and in the shared budgets of its set that its source reaches ([`0170-budgets`, "BG-8"](../0170-budgets/change.md#bg-8-tree-counters-and-caps), and "BG-12" **Counting by source.**). When charges exhaust a limit, [`0170-budgets`, "BG-12"](../0170-budgets/change.md#bg-12-external-charges) **Exhaustion** suspends the attribution bindings of every session under the session that holds the limit, and `netagent` answers each session's next call to the gateway as [`0127-egress-and-git`, "Credential bindings"](../0127-egress-and-git/change.md#credential-bindings) **Binding suspension.** states: `429` with `Retry-After` while a §8.6 extension of a session budget or a cap is pending, and `403` naming the budget once the exhaustion is final. `onExhausted.running` and admission act as 0170 "BG-6" states. Calls already in flight at the gateway are not cancelled, and LUR-6 bounds the overshoot.

### LU-6 Usage reporting

A charge's optional `usage` member ("BG-12") reaches the usage events of [`0135-message-delivery`](../0135-message-delivery/change.md#md-20-turn-and-usage-events) "MD-20" as `source: external` and the client-facing usage of the turn resource. No billing event of [`0121-billing-resource-attribution`](../0121-billing-resource-attribution/change.md) carries a charge: charges reach a reader through usage reports and the `external_charges` rows of 0170 "BG-12", and the operator's gateway keeps its own billing records (decision 28, 0170's decision 78). A runtime's own counts on `llm_request_completed` stay `source: runtime` usage (LUR-7).

### LU-7 Calls outside the gateway

A runtime can reach a provider, or a gateway that no attribution binding names, over egress like any other traffic: with a key it holds, through an ordinary `inject` binding, or in a format no gateway serves. Lenny neither blocks that traffic nor reports it as LLM traffic, attaches no assertion to it, and applies no budget to it (OD-8, OD-15). The egress controls of the runtime's pool apply to it as to any other connection **[design]**.

### LU-8 Security

- **No keys in Lenny.** Lenny holds no LLM provider credential. The gateway holds them, outside every pod (LUR-1).
- **The assertion.** CR-2 **Attribution assertion.** of 0124 states why a runtime that bypasses `netagent` cannot forge one, the residual on reused pods within the tenant, and why the assertion is exempt from D11. That residual is the one that open decision 21 of revision 19 found for the proxy token.
- **Charges.** "BG-12" of 0170 states which sessions a reporter can charge, the `jti` proof that keeps credentials out of gateway logs, hosts bound to one tenant or shared under per-tenant keys, the budgets that each source of a charge counts in, a platform budget counting platform reporters alone, and the reach of an owner's direct charges, which stays within its own tree and principal budgets.
- **Guardrails.** Lenny runs no interceptor and no leak scan on model calls. A deployer that needs them runs them in its gateway.

### LU-9 Reference integrations

Lenny ships three integrations outside its core, each in its own first-party repository with its own releases, as the client adapters of [`0138-rest-only-client-api`, "RO-5"](../0138-rest-only-client-api/change.md#ro-5-packaging-versioning-and-deployment) ship **[design]**. None is required: a gateway that verifies the assertion and reports charges by itself needs none of them.

- **`lenny-litellm`.** A Python module that LiteLLM loads from its configuration: a `custom_auth` function that verifies the attribution assertion and maps `tenant` to a LiteLLM team and `user` to an end user, and a batching callback that reports charges citing each call's `jti`, flushing every second or 500 charges and converting LiteLLM's USD prices to `budgets.currency` when configured, and that sends a limit notice when LiteLLM refuses a call for a spent team or key budget. LiteLLM's built-in JWT authentication requires its Enterprise licence, and its open-source `custom_auth` replaces virtual-key authentication for every client of that proxy, so the guide recommends a LiteLLM instance dedicated to Lenny's sessions **[external]**.
- **`lenny-charge-relay`.** A Go service that accepts OpenTelemetry log records or a gateway's JSON log batches, maps their fields to charges through a configuration file, applies an optional price table to token counts, and posts "BG-12" batches. It serves gateways that report token counts or cost through logs, such as Envoy AI Gateway's access logs and Kong's `http-log` **[external]**.
- **The reference verifier.** An external-authorization service that verifies the assertion under either key set of 0124's D14, including the tenant match that the `perTenant` key set needs, and passes its claims to the gateway as headers (0124's D19). A service that several tenants share on one host under the `perTenant` key set checks each assertion against its tenant's issuer, which the reference verifier does, and refuses an assertion of a tenant it is not configured for (0124's D25). The reference verifier requires a configured list of the tenants it accepts, or an explicit setting that accepts every tenant for a gateway that a platform reporter serves, under either key set, because the deployment key set verifies every tenant's assertions (0124 CR-2 **Keys and issuer (D14).**).

`docs/operator-guide/external-llm-proxy.md`, replaced by the LLM gateway guide, links each integration and gives an end-to-end configuration for LiteLLM, Envoy AI Gateway, and a gateway run by a tenant.

## Limits

- Budget enforcement on model spend is best effort. A limit can be exceeded by the charges of the calls the gateway admitted before the suspension took hold, which grow with the gateway's report interval and the binding propagation delay, plus every call in flight.
- Calls in flight are not cancelled at an exhaustion or a suspension that takes hold. The egress halt stops them only when a suspension cannot take hold.
- An extension holds no call. `netagent` answers `429` while it is pending, and the runtime's SDK retries.
- Lenny runs no `PreLLMRequest` or `PostLLMResponse` interceptor and no leak scan of user secrets on model calls ([`0151-user-credential-delivery`, "UC-6"](../0151-user-credential-delivery/change.md)).
- Model calls are no session activity. A runtime that waits on a model reports `busy: true` on its heartbeats, as for any operator service.
- A deployment without an LLM gateway that reports charges has no budget on model spend. Compute and storage budgets work without one.
- Lenny cannot refuse provider server tools that reach the network, such as `web_fetch`, because it reads no request body. The gateway refuses them, or the operator accepts the path.
- On a reused pod, a compromised adapter, sandbox kernel, or `netagent` reaches later sessions' attribution within the tenant, bounded by their budgets ("LU-8").
- Token counts reach usage events only when the reporter supplies them.

## Alternatives rejected

- **Keep the thin metering proxy of revisions 1 to 19.** It tracks every provider's routes, usage fields, stream rules, token classes, and prices, which the owner asked to leave (decision 22).
- **A relay that reads no body (option B of the analysis of 2026-11-24).** It keeps key custody, the proxy token, interceptors over bytes, and cancellation in flight, and it keeps a listener on the gateway hot path for every model stream. The owner chose the egress path with its trade-offs.
- **A session token that the deployer's gateway introspects (option D).** The token is a bearer the runtime holds, usable from any address that reaches the gateway, and every gateway needs an introspection client.
- **Plain identity headers attached by `netagent`.** Their trust rests on every request reaching the gateway through `netagent`, which raw sockets on `internet` pools defeat, and on an extra gateway key per tenant. A signed assertion needs neither.
- **The identity assertion of CR-2 as it stands.** D11 confines it to pods no other session used, which would confine LLM budgets to those pods. The attribution assertion authenticates no user, so its residual on reused pods is bounded spend within the tenant.
- **A generic gateway-side proxy for every service.** It duplicates `netagent` and `lenny-egress`, and it identifies a session from a reused pod no better than `netagent` does.
- **The assertion itself as a charge's proof.** Gateways keep credentials out of their logging paths: LiteLLM removes `Authorization` from the headers its callbacks see **[external]**. Every deployer would write code to carry a credential into its cost reports, where it would rest in log storage while it still counted as proof. The `jti` reaches the logs through the claim mapping each gateway already has.
- **The gateway refusing a suspended session's call.** A call with no assertion fails as an authentication error, which the Anthropic SDK does not retry **[external]**, and a gateway that keeps its own keys beside the assertion would serve it. `netagent` answers with a status the runtime retries, whatever the gateway's configuration.
- **Credential injection of the gateway's key by `netagent` with budget-aware minting alone (option C).** The Token Service would refuse to mint once a budget is exhausted, so the lag would be the assertion's lifetime. Suspending the binding takes effect at the next request.
- **Keeping credential pools and adding attribution and budgets (the analysis's option A of 2026-11-09).** It keeps LLM-gateway functions, their open High findings, and direct mode's key in the pod.

## Specification changes

The following edits locate their targets by heading. Subsections of §4.9 named here are its bold-labelled H4 headings. The rows name the numbered sections as they stood on 2026-11-16 and state the content to move. A step that lands after the area change that migrates a numbered section edits the page that received its content, as that section's successor stub states. The §18 and §23 rows apply to their successors under "Retired records and sections" of the [v1 program](../programs/v1.md).

| Section | Change |
| :-- | :-- |
| [§4.1 Edge Gateway Replicas](../../spec/04_system-components.md#41-edge-gateway-replicas), "Gateway Internal Subsystems", item 4 **LLM Proxy** | Remove the LLM proxy subsystem. |
| [§4.3 Token Service](../../spec/04_system-components.md#43-token-service) | Remove LLM credentials and leases. The Token Service mints attribution assertions as CR-2 of `0124-connector-removal` states. |
| [§4.7.1](../../spec/04_system-components.md#471-role-and-gateway-rpc-contract), [§4.7.3](../../spec/04_system-components.md#473-adapter-events-on-ch-adapterevents), [§4.7.6](../../spec/04_system-components.md#476-adapter-manifest-field-reference), and §4.7.7 | Remove `AssignCredentials`, `RotateCredentials`, `ExtendCredentialLease`, `ReportUsage`, the rotation frames, §4.7.8, and the `llm` member of `session_start` with `deliveryMode`, `dialect`, and `apiKeyEnv`. |
| [§4.8 Gateway Policy Engine](../../spec/04_system-components.md#48-gateway-policy-engine) | Remove the `PreLLMRequest` and `PostLLMResponse` phases and **LLM interceptor phases.** The `QuotaEvaluator` row names budgets on the `externalCost` meter. |
| [§4.9 Credential Leasing Service](../../spec/04_system-components.md#49-credential-leasing-service), opening, **Credential Provider**, **Credential Pool**, **Credential Lease**, and **`materializedConfig` Schema by Provider** | Narrow to VCS providers. Remove the LLM provider rows and the LLM lease tables. |
| §4.9 **Pre-Claim Credential Availability Check**, **Credential Policy**, **Three Credential Modes**, **Fallback Flow**, **Proactive Lease Renewal**, **`CredentialRouter` Interface**, **LLM Reverse Proxy**, and **Semantic Caching** | Remove. A successor pointer names "LLM gateways and external charges", the new subsection below. |
| §4.9, new subsection **LLM gateways and external charges** | The operator's LLM gateway as an egress destination, the attribution binding and its assertion, charges on the `externalCost` meter, enforcement by binding suspension, and calls outside the gateway, with LUR-1 to LUR-7. |
| §4.9 **Pre-Authorized Credential Flow**, **Emergency Credential Revocation**, **Security Boundaries**, and [§4.9.2](../../spec/04_system-components.md#492-credential-audit-events) | Remove the `provider` path (OD-10), the direct-mode steps, and the LLM deny-list and audit rows. |
| [§5.1 Runtime](../../spec/05_runtime-registry-and-pool-model.md#51-runtime) and [§5.2](../../spec/05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes) | Remove `supportedProviders` and `credentialCapabilities` with no replacement field, and the slot's credential lease and credentials file. |
| [§6.1](../../spec/06_warm-pod-model.md#61-what-a-pre-warmed-pod-looks-like), [§6.2](../../spec/06_warm-pod-model.md#62-pod-state-machine), [§7.1](../../spec/07_session-lifecycle.md#71-normal-flow), and [§7.3](../../spec/07_session-lifecycle.md#73-retry-and-resume) | Remove the per-session credential lease lifecycle and the pre-claim check. |
| [§8.1](../../spec/08_recursive-delegation.md#81-design-philosophy), [§8.2](../../spec/08_recursive-delegation.md#82-delegation-mechanism), [§8.3](../../spec/08_recursive-delegation.md#83-delegation-policy-and-lease), [§8.8](../../spec/08_recursive-delegation.md#taskrecord), and [§8.10](../../spec/08_recursive-delegation.md#810-delegation-tree-recovery) | Remove `credentialPropagation`, the credential pre-check, `ReportUsage`, and `FINAL_USAGE_REPORT`. Token budgets are limits on the `externalCost` meter under `0170-budgets`. |
| [§8.6 Lease Extension](../../spec/08_recursive-delegation.md#86-lease-extension) | The trigger is the exhaustion by charges of a limit of a session budget or a cap. The extension holds no call, and a granted extension lifts the binding suspension. |
| [§10.2 Authentication](../../spec/10_gateway-internals.md#102-authentication) | Remove credential lease bearers. |
| [§11.2](../../spec/11_policy-and-controls.md#112-budgets-and-quotas) and [§11.2.1](../../spec/11_policy-and-controls.md#1121-billing-event-stream) | The "Token limits" row gives way to budgets on the `externalCost` meter of `0170-budgets`. `token_usage.checkpoint` loses its LLM token counts, and charges reach billing as `0121-billing-resource-attribution` states. |
| [§11.3](../../spec/11_policy-and-controls.md#113-timeouts-and-cancellation) and [§11.4](../../spec/11_policy-and-controls.md#114-user-invalidation) | Remove the credential lease TTL and expiry warnings. |
| [§12.2](../../spec/12_storage-architecture.md#122-storage-roles) and [§12.4](../../spec/12_storage-architecture.md#124-redis-ha-and-failure-modes) | Remove the LLM lease and token-budget keys. |
| [§13.1](../../spec/13_security-model.md#131-pod-security), [§13.2.2](../../spec/13_security-model.md#1322-agent-pod-allow-list-policies), [§13.2.4](../../spec/13_security-model.md#1324-lenny-system-namespace-networkpolicies), [§13.2.5](../../spec/13_security-model.md#1325-gateway-egress-to-upstream-llm-providers), and [§13.2.6](../../spec/13_security-model.md#1326-per-pool-egress-relaxation-and-egressprofile) | Remove the pod's proxy listener allowance, the gateway's upstream egress rule `allow-gateway-egress-llm-upstream`, whose remaining purposes move to `allow-gateway-egress-external` of [`0138-rest-only-client-api`, "RO-2"](../0138-rest-only-client-api/change.md#ro-2-rest-additions) **Gateway egress.**, `provider-direct`, NET-006, and NET-044. |
| [§13.3 Credential Flow](../../spec/13_security-model.md#133-credential-flow) | Replace the direct-mode and proxy-mode flows with the gateway path of "LLM gateways and external charges". |
| [§14](../../spec/14_workspace-plan-schema.md#14-workspace-plan-schema) | Remove `credentialPolicy` from the plan. |
| [§15.1 REST API](../../spec/15_external-api-surface.md#151-rest-api) | Remove `CREDENTIAL_POOL_EXHAUSTED`, `CREDENTIAL_PROVIDER_MISMATCH`, `CREDENTIAL_RENEWAL_FAILED`, `LEASE_SPIFFE_MISMATCH`, and the `provider` field of `/v1/credentials`. The charge routes and codes are `0170-budgets`'s. A write that names `deliveryMode`, `credentialPolicy`, `supportedProviders`, `credentialCapabilities`, `credentialPropagation`, `rotationTrigger`, or `maxRotationsPerSession` is refused with `400 VALIDATION_ERROR` naming the field (D9). |
| [§16.1 Metrics](../../spec/16_observability.md#161-metrics), §16.1.1, and [§16.5](../../spec/16_observability.md#165-alerting-rules-and-slos) | Remove the lease, pool, rotation, renewal, pre-claim, proxy, and token-anomaly metrics and alerts. |
| [§18.15](../../spec/18_build-sequence.md#1815-phase-55--basic-credential-leasing-token-service) and §18.18 | Restate the build phases without LLM leasing and the proxy. |
| [§19 Resolved Decisions](../../spec/19_resolved-decisions.md#19-resolved-decisions), row 13 | The extension starts when charges exhaust a limit. |
| [§23](../../spec/23_competitive-landscape.md#23-competitive-landscape) | Restate **Credential management** as the operator's gateway with Lenny's attribution and budgets. |
| [§24.5 Credential Management](../../spec/24_lenny-ctl-command-reference.md#245-credential-management) | Keep VCS pool commands and remove the LLM ones. |
| [§26.2](../../spec/26_reference-runtime-catalog.md#262-shared-patterns-for-coding-agent-runtimes) and §26.3 to §26.11 | Replace **Credential delivery.** and **Common `credentialCapabilities`.** with the gateway base URL of the pool's injected configuration. |
| [§28.3](../../spec/28_communication-channels.md#283-registers), [§28.5.2](../../spec/28_communication-channels.md#2852-pod-to-gateway), [§28.5.3](../../spec/28_communication-channels.md#2853-intra-pod), and [§28.5.5](../../spec/28_communication-channels.md#2855-pod-egress) | Retire `CH-LLMPROXY` in the naming table, remove the lease and rotation frames, and keep `llm_request_started` and `llm_request_completed`. |
| [§29.2](../../spec/29_communication-scenarios.md#292-session-start), §29.4, §29.6, and §29.10 | Remove the pre-claim step, `AssignCredentials`, `ReportUsage`, `FINAL_USAGE_REPORT`, and per-slot leases. |

## Non-specification changes

| Area | Change |
| :-- | :-- |
| `pkg/gateway/meteringproxy` with `llmproxy` and `sessionbudget`, `cmd/lenny-gateway/llmproxy_server.go`, and `llmproxy_usage.go` | Delete. |
| `pkg/gateway/llmproxy/credrouter` and `pkg/gateway/session/tokenanomaly` | Delete. |
| `pkg/gateway/credentials` | Delete `credassign`, `credleasestore` with `credleasestore/pgstore`, `credrenewal` with `credrenewal/propagator`, `credfallback`, and `leaserevoke`. |
| `pkg/credential` and `pkg/tokenservice` | Remove the LLM providers, `vault_transit`, `CredentialPolicy`, lease minting and validation, `UserProxyDialect`, and the LLM parts of `denylist` and `revocation`. Keep VCS pool reads and writes. |
| `pkg/gateway/core/session/sessionserver` | Delete `credential_preclaim.go`. Remove `credentialPolicy` from creation and responses. |
| `pkg/gateway/platformtools/delegation`, `pkg/gateway/platformtools/treebudget`, and `pkg/delegation/lease` | Remove `credentialPropagation` and the token budget of the lease. |
| `pkg/gateway/api/admin` and `pkg/api/errorclassify` | The refusal of LLM providers on pools, the removed codes, and the lists of removed fields. |
| `pkg/adapter` and `cmd/lenny-adapter` | Remove `credentials.go`, `credexpiry.go`, `slotcreds.go`, and `credfile` to the extent they serve LLM leases, `usage.go`'s `ReportUsage`, the rotation frames, and the `llm` member. |
| `pkg/apis/lenny/v1alpha1`, `pkg/controller/runtime`, and `pkg/gateway/runtime` | Remove `supportedProviders` and `credentialCapabilities`. Regenerate the CRD manifests. |
| `schemas/lenny-adapter.proto` and `schemas/lenny-tokenservice.proto` | Remove `AssignCredentials`, `RotateCredentials`, `ExtendCredentialLease`, and `ReportUsage` with `reserved` numbers. |
| `migrations` | Drop the LLM lease tables and the tenant `credential_policy` column. |
| `charts/lenny` | Remove the direct-mode webhook, `egressCIDRs.providers`, the `deliveryMode` gates, the proxy listener and its Service port, and `allow-gateway-egress-llm-upstream`, whose remaining purposes `allow-gateway-egress-external` of 0138 takes. |
| `tests/registers/gateway-layout.yaml` | The step that deletes a package listed with `removed-by: '0133'` deletes its row in the same commit, and its diff is reviewed under "Review changes to the merge gate". |
| SDKs and reference runtimes | Read the gateway base URL from the injected configuration. Remove the rotation handling and the lease rebind loop. |
| `BUILD-GAPS.md` | Resolve the lease and proxy findings of "Records" as obsolete. |

### Documentation pages

| Page | Reader | What changes for the reader | Step |
| :-- | :-- | :-- | :-- |
| `docs/operator-guide/external-llm-proxy.md` | Operator | Replaced by a guide to LLM gateways: the gateway as an egress destination, the attribution binding with its default `Authorization: Bearer` header and the `Lenny-Attribution` configuration for a gateway that keeps its own keys, the removal of the placeholder from other headers, verifying the assertion under either key set, the rule that a gateway grants no per-user privilege from it, the check of the `tenant` claim by a gateway run by a tenant, the two setups of "LU-3", charges with Lenny's budgets and external enforcement with limit notices and the runtime's `external_limit` frame, with a session's suspension until its client resumes it; reporting batched charges by `jti` with the reporting rules of "BG-12" (the call's start as `occurredAt`, the flush every second or 500 charges, `budgets.currency`, and an alert on unpriced models), `netagent`'s `429` and `403` once attribution is suspended, the runtime configuration per client (Claude Code with `ANTHROPIC_BASE_URL` and `ANTHROPIC_AUTH_TOKEN`, Codex CLI with a `model_providers` entry, and gemini-cli with `GOOGLE_GEMINI_BASE_URL` and `GEMINI_API_KEY`), the reference integrations of "LU-9", end-to-end configurations for LiteLLM, Envoy AI Gateway, and a gateway run by a tenant, and the checks of "Validation owed". | 0133.S1 |
| `docs/runtime-author-guide/runtime-configuration.md`, `docs/reference/adapter-contract.md`, and `docs/runtime-author-guide/lifecycle.md` | Runtime author | The gateway base URL from the injected configuration in place of the `llm` member, `busy: true` heartbeats while waiting on a model, and `llm_request_completed` as the runtime's own report. | 0133.S1 |
| `docs/operator-guide/security.md`, `docs/operator-guide/security-principles.md`, and `docs/operator-guide/multi-tenancy.md` | Operator | No LLM key in Lenny, the attribution assertion and its residual on reused pods, guardrails in the gateway, and calls outside the gateway. | 0133.S1 |
| `docs/operator-guide/configuration.md`, `docs/reference/configuration.md`, `docs/operator-guide/installation.md`, and `docs/operator-guide/lenny-ctl.md` | Operator | `supportedProviders`, `credentialCapabilities`, `proxyDialect`, `deliveryMode`, `credentialPolicy`, and the LLM pool commands leave. | 0133.S2 |
| `docs/operator-guide/observability.md`, `docs/reference/metrics.md`, `docs/alerting/rules.yaml`, the runbooks of credential pools, revocation, and rotation, and `docs/operator-guide/troubleshooting.md` | Operator | The lease, pool, rotation, renewal, pre-claim, and proxy metrics, alerts, and runbooks leave. | 0133.S2 |
| `docs/reference/glossary.md`, `docs/getting-started/concepts.md`, `docs/getting-started/architecture.md`, `docs/index.md`, and `docs/about/why-lenny.md` | Evaluator and new operator | Credential leasing and the proxy give way to the operator's gateway with Lenny's attribution and budgets. | 0133.S2 |
| `docs/api/rest.md`, `docs/api/admin.md`, `docs/api/internal.md`, `docs/reference/error-catalog.md`, `docs/client-guide/error-handling.md`, `docs/client-guide/delegation-and-tasks.md`, and `docs/reference/workspace-plan.md` | Client developer and operator | The removed codes and fields. | 0133.S2 |
| `docs/runtime-author-guide/delegation.md`, `docs/tutorials/recursive-delegation.md`, and `docs/tutorials/user-credentials.md` | Runtime author and client developer | `credentialPropagation` and the `provider` path leave. | 0133.S2 |

## Tests

| Tier | Coverage |
| :-- | :-- |
| `contract` | A runtime, pool, tenant, bootstrap, derived-runtime, session-creation, or delegation-lease write that names a removed field is refused with `400 VALIDATION_ERROR` naming it (D9). |
| `integration` | On the compose stack with a gateway fake that verifies the assertion and reports charges: a call through `netagent` reaches the fake with the session's assertion and no runtime-sent value of its header (LUR-2); a call to an unbound host carries none (LUR-3); charges citing each call's `jti` count against the session's budgets; charges exhaust a session budget, the bindings of every session of the tree are suspended, and `netagent` answers the next call with `429` and `Retry-After` while the extension is pending and `403` naming the budget once it is final, with no request reaching the fake (LUR-3, LUR-5); a granted extension, or a void of the exhausting charge, lifts the suspension and the retried call reaches the fake; a request from an SDK that sends `x-api-key: lenny-injected` reaches the fake with the assertion in `Authorization` and no `x-api-key`; a call outside the gateway carries no assertion and charges nothing (LUR-7). |
| `e2e_kind` | On Kind: a runtime reaches an in-cluster gateway registered as a private egress upstream, through `netagent` and `lenny-egress`. |
| `security` | No LLM provider key appears in a pod's environment, files, frames, or logs (LUR-1). A runtime that sends its own `Lenny-Attribution` header reaches the gateway with the session's assertion alone. A raw-socket request from an `internet` pool carries no valid assertion. |
| `docs` | No document describes direct mode, LLM credential pools, the proxy, or rate cards. |
| `static` | A repository check finds no `deliveryMode`, `credentialPolicy`, `CredentialRouter`, `hotRotation`, `supportedProviders`, `credentialPropagation`, `vault_transit`, `llmFormats`, or `/llm-proxy/` outside migrations, the validators' lists of removed fields, and archived records. |

Every test carries the `// spec:` annotation, and tests of the `component` tier and the tiers after it carry `// diagnosis:`, as `test-coverage.md` requires.

## Instructions for reviewers of other designs

Decision 22 retires the metering proxy, upstream records, rate cards, credits, reservations per call, `llm_usage`, the proxy token, `PreLLMRequest`, `PostLLMResponse`, and `llmFormats`. Each change below records what it takes on in its own `decisions.md` or review history and edits the sections named. A reviewer of a change not listed searches it for those terms and the LU-n and LUR-n labels of revisions 1 to 19, and restates each mention as this revision does, or removes it.

- [`0170-budgets`](../0170-budgets/change.md): the `externalCost` meter replaces the `llm` meter and `llmCredits` (its decision 57), "BG-12" owns the charge ingestion API, and the price list loses its `llm` section and the tenant upstream prices of decision 54.
- [`0124-connector-removal`](../0124-connector-removal/change.md): CR-2 gains the attribution assertion with its D11 exemption, its residual, its key set, its header, its mint index, and hosts bound to one tenant (its decisions D13 to D20).
- [`0127-egress-and-git`](../0127-egress-and-git/change.md): bindings at platform or pool scope (its decision 84), binding suspension with `netagent`'s answers to a suspended session (its decision 85), and the removal of the placeholder on an attribution binding's hosts; the LLM host reservation and the restatement of P3 around upstream records leave, because Lenny holds no LLM credential.
- [`0166-delegation-primitives`](../0166-delegation-primitives/change.md): decision 53's required limits are `externalCost` and a compute meter, and the extension of DP-3 starts on exhaustion by charges (its decision 55).
- [`0142-security-oversight`](../0142-security-oversight/change.md): SO-5a's `llmCreditsAbove` becomes `spendAbove` in the currency on `externalCost`, and the posture `llm` section leaves (its decision 48).
- [`0146-session-lifecycle`](../0146-session-lifecycle/change.md): SLR-23's refusal in the proxy gives way to the egress halt, and SL-6 drops usage reports as activity (its decision L38).
- [`0151-user-credential-delivery`](../0151-user-credential-delivery/change.md): UC-6 scans no model call.
- [`0128-forensics-evidence-preservation`](../0128-forensics-evidence-preservation/change.md): a quarantine stops LLM access by suspending the session's attribution bindings.
- [`0141-runtime-routing`](../0141-runtime-routing/change.md) and §4.8: `PreLLMRequest` and `PostLLMResponse` leave, and the post-chain point runs no upstream-list check.
- [`0135-message-delivery`](../0135-message-delivery/change.md), [`0121-billing-resource-attribution`](../0121-billing-resource-attribution/change.md), [`0138-rest-only-client-api`](../0138-rest-only-client-api/change.md), and [`0120-anthropic-messages-api`](../0120-anthropic-messages-api/change.md): usage comes from charges' optional `usage` (`source: external`) and from runtime reports, and `llm_usage` and the credit estimate on billing events leave. 0138 also states `allow-gateway-egress-external`, the gateway's one outbound HTTPS rule, which takes the remaining purposes of `allow-gateway-egress-llm-upstream`.
- [`0147-tenant-derived-runtimes`](../0147-tenant-derived-runtimes/change.md) and [`0140-runtime-revisions`](../0140-runtime-revisions/change.md): `llmFormats` and the creation-time format check leave, and the proxy token residual becomes the attribution residual of CR-2.
- [`0132-llm-response-cache-removal`](../0132-llm-response-cache-removal/change.md): RC-B and O2, which moved here, are moot with the proxy, and RC-5's passthrough statement has nothing left to describe.
- [`0149-tenant-scoping-identity`](../0149-tenant-scoping-identity/change.md): the gateway reaches customer issuers over `allow-gateway-egress-external` of 0138, and a crossing child carries no upstream list.
- [`0117-gateway-package-layout`](../0117-gateway-package-layout/change.md), [`0144-session-derive`](../0144-session-derive/change.md), [`0148-tenant-outbound-connector`](../0148-tenant-outbound-connector/change.md), [`0139-runtime-protocol`](../0139-runtime-protocol/change.md), [`0118-agent-pod-layout`](../0118-agent-pod-layout/change.md), and [`0126-delegation-availability`](../0126-delegation-availability/change.md): the `meteringproxy` packages, examples in `llmCredits`, the LLM host reservation, the `llm` member of `session_start`, the credentials file, and credit budgets in §8.3 leave or are restated.

## Sources

- LiteLLM, Cost tracking, https://docs.litellm.ai/docs/proxy/cost_tracking, read on 2026-11-09. Spend per request, tags from headers through `extra_spend_tag_headers`.
- Portkey, Metadata, https://portkey.ai/docs/product/observability/metadata, read on 2026-11-09. `HEADERS_TO_METADATA` on self-hosted deployments.
- RFC 8725, JSON Web Token Best Current Practices, section 3.11 on explicit typing, as CR-2 of `0124-connector-removal` cites it **[external]**.
