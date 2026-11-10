# LLM upstreams and cost budgets

Status: design for owner review, revision 2, 2026-11-09, with no independent review yet. Nothing here is built or staged. It reads against [`llm-response-cache-removal-design.md`](llm-response-cache-removal-design.md) revision 3, [`billing-resource-attribution-design.md`](billing-resource-attribution-design.md) revision 11, [`egress-and-git-design.md`](egress-and-git-design.md) revision 74, [`tenant-derived-runtimes-design.md`](tenant-derived-runtimes-design.md) revision 65, [`runtime-revisions-design.md`](runtime-revisions-design.md) revision 9, [`security-oversight-design.md`](security-oversight-design.md) revision 47, [`user-credential-delivery-design.md`](user-credential-delivery-design.md) revision 26, [`delegation-availability-design.md`](delegation-availability-design.md) revision 3, [`anthropic-messages-api-design.md`](anthropic-messages-api-design.md) revision 4, [`connector-authorization-and-external-memory-design.md`](connector-authorization-and-external-memory-design.md) revision 12, [`tenant-scoping-identity-design.md`](tenant-scoping-identity-design.md) revision 32, [`runtime-routing-design.md`](runtime-routing-design.md) revision 11, and [`tenant-outbound-connector-design.md`](tenant-outbound-connector-design.md) revision 28. It develops option D1 of the owner's analysis of LLM credential pools, metering, and cost budgets, dated 2026-11-09, which the owner settled in the words "Thin LLM proxy: sounds good". "Decisions" lists what the owner has settled and what remains open. It stages no specification edit. The changes go through `change-proposal` after review. Parts carry the label LU-n, proposals carry LU-A to LU-E, and requirements carry LUR-n. "Review history" at the end records the revisions.

Each factual claim about behaviour carries one of these tags where the source matters:

| Tag | Meaning |
| :-- | :-- |
| **[source]** | Read in the Lenny tree or the specification on 2026-11-09. |
| **[external]** | Read on an external site on 2026-11-09. "Sources" lists the pages and says which this design read again. |
| **[design]** | What this design requires. Nothing implements it. |

## Problem

"Terms", after "Scope", defines the vocabulary this document uses.

Lenny holds LLM provider credentials in credential pools, leases them to sessions, and either injects them in its LLM proxy (proxy mode) or writes them into the pod (direct mode). Around the pools sit assignment strategies, health scoring, cooldown, fallback chains, proactive renewal, rotation into running pods, the `CredentialRouter`, and a pre-claim availability check ([§4.9 Credential Leasing Service](../spec/04_system-components.md#49-credential-leasing-service)) **[source]**. These are the functions of an LLM gateway. Operators who need them already run one, and Lenny's versions carry most of the open High findings in credential leasing ("Current state").

What only Lenny can do is attribute LLM spend to its own identities (tenant, environment, user, session, delegation tree, and runtime revision) and stop spend on those identities before it happens. Today it does neither well:

- The usage record names no provider, model, channel, or contract, so a cost cannot be computed from Lenny's records even with prices in hand **[source]**.
- The proxy counts Anthropic `input_tokens` alone, so cache reads and writes escape quotas, budgets, and billing (F-11.2.38) **[source]**.
- The proxy serves the Anthropic format alone, so an OpenAI-format runtime receives 404 (F-4.9.48) **[source]**.
- Budgets are raw token counts. A token of one model can cost many times a token of another, cache reads are billed at a fraction of input, and a new tokenizer changes the count for the same text **[external]**.
- Enforcement happens after the provider answers. A call that exhausts a budget has already been billed, and a stream is never stopped **[source]**.
- Direct mode places a long-lived provider key in a file the runtime reads, and its usage comes from the runtime's own optional reports **[source]**.

The owner settled the direction: keep a thin proxy that meters usage and enforces budgets, remove the LLM use of credential pools, add an LLM upstream record, express budgets as estimated cost in unitless credits from operator rate cards, and leave pooling and failover to an operator-run LLM gateway placed behind Lenny as an upstream ("Decisions").

## Scope

**Added:**

- The LLM upstream record: endpoint, provider, channel, contract, API format, credential source, model allowlist, and rate card, written by a `platform-admin` for the platform or by a `tenant-admin` for one tenant ("LU-1").
- Upstream lists on tenants and pools, grants of platform upstreams to tenants, and resolution of each call to one upstream by API format and model ("LU-2").
- Proxy routes for OpenAI Chat Completions and OpenAI Responses beside Anthropic Messages, one proxy token per session, and usage normalization into token classes with cache writes by lifetime ("LU-3").
- Rate cards: per upstream, per model, and per token class, with multipliers, a discount, and versions by `effectiveFrom` ("LU-5").
- Credit budgets with reservation before a call and settlement after it, across the per-call, session, tree, user, tenant, and global scopes, with the [§8.6](../spec/08_recursive-delegation.md#86-lease-extension) extension requested before the call ("LU-6").
- One usage record per model call, and LLM usage entries with a labelled estimated cost on `token_usage.checkpoint` ("LU-7").
- Workload identity as a credential source for platform upstreams on Amazon Bedrock, Google Cloud, and Azure ("LU-8").
- An unmetered path for API formats the proxy does not parse, through an egress credential binding under a platform-admin grant, reported in posture ("LU-10").
- Attribution forwarded to the upstream: the proxy sets identity attributes on every forwarded request, with user identifiers pseudonymized per upstream, so that an operator-run LLM gateway behind Lenny can choose per-user keys, workspaces, caches, or budgets itself ("LU-11").
- Posture rows that list the other tenants sharing each of a tenant's upstreams, and a warning when a grant makes a `hipaa` or `fedramp` tenant share an upstream ("LU-9").

**Changed:**

- `credentialCapabilities.proxyDialect` becomes the runtime field `llmFormats`, the API formats the runtime speaks to the proxy.
- The `llm` member of `session_start` carries one base URL per format and the proxy token in place of the delivery mode, dialect, and lease token.
- The token limits of [§11.2](../spec/11_policy-and-controls.md#112-budgets-and-quotas) and `maxTokenBudget` of [§8.3](../spec/08_recursive-delegation.md#83-delegation-policy-and-lease) become credit limits and `maxCreditBudget`.
- The §8.6 trigger moves from the settlement of a completed call to the refused reservation of the next call.
- `billing_correction` covers the new LLM fields, and LLM corrections get a dual-control threshold in credits.
- `CredentialPool` serves VCS providers alone.

**Removed:**

- LLM providers on `CredentialPool`, LLM credential leases, `credentialPolicy`, direct delivery mode, the `CredentialRouter`, assignment strategies, health scoring, cooldown, fallback, proactive renewal, rotation of LLM credentials into pods, `hotRotation`, `supportedProviders`, `credentialPropagation`, and the pre-claim credential availability check ("LU-4").
- The direct-mode usage path: `ReportUsage`, the usage fields of `llm_request_completed`, `FINAL_USAGE_REPORT`, and the token-usage anomaly detector.
- `provider-direct`, NET-006, NET-044, and the `lenny-direct-mode-isolation` webhook, which [`egress-and-git-design.md`](egress-and-git-design.md#consolidating-provider-direct) already proposes to remove with direct mode's last consumer.
- `vault_transit` and the `provider` path of `POST /v1/credentials`, if the owner accepts OD-10.

**Out of scope:**

- Failover between upstreams, multi-key pooling, and routing across providers. An operator places an LLM gateway behind Lenny as an upstream.
- Per-user upstreams and per-user provider keys inside Lenny. A downstream LLM gateway makes that choice from the forwarded user attribute ("LU-11").
- Translation between API formats. Each call reaches an upstream in the format the runtime sent.
- Per-token-class budgets, which the owner rejected.
- Currency, invoices, and rating. Credits are unitless, and the operator's billing system prices quantities as it chooses.
- Metering of the `google-generate` format, which OD-9 defers.
- The removal of Lenny's semantic response cache, which [`llm-response-cache-removal-design.md`](llm-response-cache-removal-design.md) owns as RC-A. Its RC-B and O2 move into this design.

## Terms

| Term | Meaning |
| :-- | :-- |
| LLM upstream | A record that binds one endpoint, one provider, one channel, one contract, one API format, one credential source, a model allowlist, and a rate card. Every proxied call is sent to exactly one upstream. "LU-1" defines it. |
| Platform upstream and tenant upstream | A platform upstream is written by a `platform-admin` and serves tenants it is granted to. A tenant upstream is written by a `tenant-admin` and serves its own tenant alone. |
| Upstream list | The ordered set of upstream names a tenant or a pool names. A session uses its pool's list when the pool has one, and its tenant's list otherwise. |
| API format | The wire format of an LLM call: `anthropic-messages`, `openai-chat` (OpenAI Chat Completions), or `openai-responses` (OpenAI Responses). The proxy meters these formats. `google-generate` is a later addition (OD-9). |
| Provider | The company whose models an upstream serves, such as `anthropic` or `openai`. |
| Channel | The route by which an upstream reaches the provider: `anthropic-api`, `aws-bedrock`, `gcp-vertex`, `azure-foundry`, `openai-api`, or `gateway` for an operator-run LLM gateway. |
| Contract | An operator label for the commercial terms behind an upstream, such as `acme-anthropic-2026`. It is carried on every usage record so that usage can be split by the agreement it was bought under. |
| LLM gateway | An operator-run proxy, such as LiteLLM or Envoy AI Gateway, that pools keys, fails over, or routes across providers. Lenny reaches it as an upstream with `channel: gateway`. |
| Metered path | An LLM call that a runtime sends to the proxy, which resolves an upstream, reserves credits, injects the upstream's credential, forwards the call, and records its usage. |
| Unmetered path | An LLM call that reaches a provider through an egress credential binding that `netagent` attaches, with no usage record and no budget. "LU-10" defines it. |
| Proxy token | The bearer a session's runtime presents to the proxy. One is minted per session, bound to the pod's SPIFFE identity when the pool requires it, and revoked when the session ends. It replaces the lease token. |
| Token class | A category of tokens a provider prices separately: `input` (uncached input), `cacheRead`, `cacheWrite` by lifetime (`5m`, `1h`, `30m`, or `12h`), and `output`. `reasoning` is recorded inside `output` for information. |
| Rate card | An operator-written price list for one upstream: per model, credits per million tokens of each token class, with multipliers, a discount, and versions. "LU-5" defines it. |
| Rate-card version | One entry of a rate card with an `effectiveFrom` time. Versions are append-only, and a call is priced at the version in force when it started. |
| Credits | The unitless budget unit. A rate card prices tokens in credits, and every budget is a credit amount. Lenny stores credits as integer micro-credits, one millionth of a credit. |
| Estimated cost | The credits a call costs under its rate-card version. It is Lenny's enforcement figure, labelled as an estimate wherever it is reported. |
| Reservation | The credits the proxy holds against every budget scope of a call before forwarding it, computed from the call's worst case. |
| Settlement | The replacement of a reservation with the call's estimated cost once the provider reports usage. |
| Budget scope | A level at which credits are limited: per call, session, delegation tree, user window, tenant window, and global window. |
| Requested model and served model | The model the request names, and the model the response names. They differ when an alias or an LLM gateway picks the model. |
| Usage record | The per-call record of a metered call: identities, upstream, models, token classes, rate-card version, reservation, and estimated cost. "LU-7" defines it. |
| Forwarded attribution | The identity attributes the proxy sets on each request it forwards to an upstream: tenant, environment, user, session, root session, runtime, runtime revision, pool, and upstream. The upstream's `attribution` settings choose which are sent. "LU-11" defines it. |
| Pseudonymous identifier | An attribute value replaced by an HMAC-SHA256 of the raw value under a key held per upstream. It is stable for one user on one upstream and differs between upstreams. |
| Downstream LLM gateway | An LLM gateway that Lenny's proxy calls as an upstream with `channel: gateway`, downstream of Lenny's proxy in the request path. |
| Shared upstream | A platform upstream granted to more than one tenant. Its tenants share the provider's prompt cache. |

### Terms from other designs and the platform

| Term | Meaning |
| :-- | :-- |
| Credential pool | A tenant-scoped, admin-managed set of credentials for one provider, stored in `credentialpoolstore` with each credential's Secret in the credentials namespace ([§4.9](../spec/04_system-components.md#credential-pool)). Today it holds LLM and VCS credentials. After this design it holds VCS credentials alone. |
| VCS pool | A credential pool whose provider serves git hosts, selected by `hostPatterns` (`ResolveVCSPool`). [`egress-and-git-design.md`](egress-and-git-design.md), [`user-git-identity-design.md`](user-git-identity-design.md), and the GitHub and GitLab add-on designs build on it. |
| Credential lease | Today, the LLM credential assigned to a session, with `materializedConfig`. In proxy mode it carries `proxyUrl`, `proxyDialect`, `leaseToken`, and `upstreamModel`, and in direct mode the provider's secret fields. This design removes it for LLM credentials. |
| `deliveryMode` | Today, the pool setting `proxy` (the gateway calls the provider) or `direct` (the pod receives a short-lived lease and calls the provider itself). This design removes it. |
| LLM proxy | The gateway subsystem that serves `POST /llm-proxy/v1/messages` on its own listener, runs the [§4.8](../spec/04_system-components.md#48-gateway-policy-engine) `PreLLMRequest` and `PostLLMResponse` interceptor phases, translates to the upstream provider, injects the credential, and records usage. Its channel identifier is `CH-LLMPROXY` ([§28.5.5](../spec/28_communication-channels.md#2855-pod-egress)). |
| Token Service | The separate Deployment that holds credentials and resolves Secrets in the credentials namespace on behalf of the gateway ([§4.3](../spec/04_system-components.md#43-token-service)). |
| Credentials namespace | The namespace that holds one Kubernetes Secret per credential, readable only through the Token Service (§4.9, **Credential Storage Topology**). |
| `netagent` | The per-pod network agent of [`egress-and-git-design.md`](egress-and-git-design.md) that terminates TLS for bound hosts and attaches credentials the agent process never sees. |
| Credential binding | The egress design's tenant object that names a secret source, the hosts it serves, and a `delivery` mode. Under `delivery: inject`, `netagent` attaches the credential to requests ([`egress-and-git-design.md`, "Credential bindings"](egress-and-git-design.md#credential-bindings)). |
| Gate | A per-tenant grant by a `platform-admin` for one risk class of tenant opt-in, such as `storedTokenPush` ([`egress-and-git-design.md`, "Delivery modes"](egress-and-git-design.md#delivery-modes)). |
| Guardrail and posture | The tenant security record of SO-2, written by `security-admin`, whose keys forbid opt-ins, and the read-only posture report at `GET /v1/admin/tenants/{id}/security-posture` ([`security-oversight-design.md`, "SO-2"](security-oversight-design.md#so-2-tenant-security-record-and-posture)). |
| Post-chain point | The step at session creation, after the `PreRoute` chain fixes the runtime and pool and a revision is chosen, where every check that depends on them runs ([`runtime-routing-design.md`, "RT-2"](runtime-routing-design.md#rt-2-the-interceptor-contract)). |
| Delegation lease and `lease_slice` | The limits a delegated child runs under, carved from its parent, including `maxTokenBudget` today ([§8.2](../spec/08_recursive-delegation.md#82-delegation-mechanism) and [§8.3](../spec/08_recursive-delegation.md#83-delegation-policy-and-lease)). |
| Lease extension | The [§8.6](../spec/08_recursive-delegation.md#86-lease-extension) operation that raises a session's budget in `auto` mode or after a user approves an elicitation, with `GRANTED`, `PARTIALLY_GRANTED`, `CEILING_REACHED`, and `REJECTED` outcomes. |
| `billing_correction` | A billing event that replaces the quantity fields of an earlier event, latest first, with dual control above `billing.dualControlThreshold` ([§11.2.1](../spec/11_policy-and-controls.md#1121-billing-event-stream)). |
| Regulated tenant | A tenant whose `complianceProfile` is `soc2`, `fedramp`, or `hipaa`. |
| Runtime revision | An immutable copy of a runtime's versioned fields ([`runtime-revisions-design.md`](runtime-revisions-design.md)). A gateway-read field reaches a session through the gateway. |
| `platform-admin`, `tenant-admin`, and `security-admin` | Lenny's administrative roles for the platform, for one tenant, and for one tenant's security record. |
| BUILD-GAPS | `BUILD-GAPS.md` at the repository root, the audit of gaps between the specification and the code, with findings named F-n.n.n. |
| Test tiers | The stages of `.claude/rules/test-coverage.md`: 0 static checks, 1 unit, 2 component, 3 contract, 4 integration on the compose stack, 5 end to end on Kind, 7a local concurrency, 8 chaos, 9 security, and 11 documentation. |

### Labels from other designs

| Labels | Source | What they name |
| :-- | :-- | :-- |
| RC-A, RC-B, RC-5, O1, and O2 | [`llm-response-cache-removal-design.md`](llm-response-cache-removal-design.md#proposal-split-and-order) | RC-A removes the semantic cache. RC-B and its part RC-5 state the `cache_control` passthrough (O1) and the cached-token metering (O2). RC-B and O2 move here. |
| EG-2, EG-4, EG-9, and P3 | [`egress-and-git-design.md`](egress-and-git-design.md#security-properties) | `netagent` and the removal of `provider-direct` (EG-2), credential delivery (EG-4), egress policy objects and the gated opt-in write path (EG-9), and the property that LLM credentials keep their §4.7 delivery rules (P3). |
| SO-2 and SO-3 | [`security-oversight-design.md`](security-oversight-design.md#so-2-tenant-security-record-and-posture) | The tenant security record with guardrails and posture (SO-2), and the per-tenant SIEM destination whose secrets sit in the credentials namespace (SO-3). |
| UC-1 and UC-6 | [`user-credential-delivery-design.md`](user-credential-delivery-design.md#uc-1-credential-types-and-the-registered-source) | Typed user credentials registered with a `type` field (UC-1), and leak scanning (UC-6). |
| DR-4, DR-5, DR-6, and decision 14 | [`tenant-derived-runtimes-design.md`](tenant-derived-runtimes-design.md#dr-4-tenant-runtime-records) | Tenant runtime records and their field table, owned pools and their template copy, base changes, and the overlay decision that names the LLM lease residual. |
| RV-1 and RV-5 | [`runtime-revisions-design.md`](runtime-revisions-design.md#rv-1-revisions) | Field classes of revisions, and derived runtimes over base revisions. |
| RT-2 | [`runtime-routing-design.md`](runtime-routing-design.md#rt-2-the-interceptor-contract) | The interceptor contract, the `PostRoute` payload, and the post-chain point. |
| TS-3 and TS-6 | [`tenant-scoping-identity-design.md`](tenant-scoping-identity-design.md#ts-3-session-environment-context-and-cross-environment-sharing) | Cross-environment sharing with its `credentialPropagation` row (TS-3), and the identity source whose issuer is reached through the gateway's LLM upstream egress rule (TS-6). |
| AM-6 and AM-8 | [`anthropic-messages-api-design.md`](anthropic-messages-api-design.md#am-6-responses-and-streaming) | Response usage mapping, and the error table with `CREDENTIAL_POOL_EXHAUSTED`. |
| CA-7 | [`connector-authorization-and-external-memory-design.md`](connector-authorization-and-external-memory-design.md) | Delegated children and the check at delegation. |
| DA-6 | [`delegation-availability-design.md`](delegation-availability-design.md#da-6-per-hop-narrowing) | Per-hop narrowing through `lease_slice`. |

## High-level design

Lenny stops being an LLM credential broker and keeps the part of LLM traffic only it can do. A runtime sends each model call to Lenny's proxy with a per-session proxy token. The proxy picks the one upstream that serves the call's API format and model for the session's tenant or pool, checks that the session, its delegation tree, its user, its tenant, and the platform each have enough credits for the call's worst case, holds those credits, adds the upstream's credential and the session's attribution headers, and forwards the body unchanged. When the response arrives, it reads every token class the provider reports, prices them with the upstream's rate card, replaces the held credits with the estimated cost, and writes one usage record. The provider key stays in the gateway, and the runtime never sees it.

An operator who wants several keys behind one upstream, failover, or routing across providers runs an LLM gateway and registers it as an upstream. Lenny meters what comes back and attributes it to that upstream's provider, channel, and contract.

The rest of this section states each part in the design's terms, which "Terms" above defines.

**Upstreams (LU-1).** An upstream binds an endpoint, a credential source, a provider, a channel, a contract, a format, a model allowlist, and a rate card. A `platform-admin` writes platform upstreams and grants them to tenants. A `tenant-admin` writes the tenant's own. The credential source is a Secret or the gateway's workload identity, so Lenny holds no provider key per user.

**Assignment and resolution (LU-2).** A tenant names an upstream list, and a pool may name one that replaces it, for example to keep a pool's traffic on an EU endpoint. At each call the proxy takes the format from the route and the model from the body, and the list holds at most one upstream for that pair, because a write that would put two in one list is refused.

**The proxy (LU-3).** The proxy serves Anthropic Messages, OpenAI Chat Completions, and OpenAI Responses, forwards each body unchanged apart from provider rehosting and turning on stream usage reports, and maps each format's usage fields to token classes. Cache writes are recorded by lifetime, and stream totals come from the last cumulative report.

**Removal (LU-4).** LLM credential pools, leases, `credentialPolicy`, direct mode, the router, health, cooldown, fallback, renewal, rotation, `hotRotation`, `supportedProviders`, `credentialPropagation`, and the pre-claim check leave the specification and the tree. VCS pools stay.

**Rate cards (LU-5).** The operator writes a card per upstream that prices each model's token classes in credits, with multipliers and a discount. Versions are append-only by `effectiveFrom`, and Lenny ships no prices.

**Credit budgets (LU-6).** The proxy reserves the worst case before it forwards a call and settles to the estimated cost afterwards. A session or tree scope that refuses a reservation triggers the §8.6 extension before the provider is called, so no tokens are spent on a refused call.

**Usage and billing (LU-7).** Each metered call writes one usage record. `token_usage.checkpoint` rolls the records up per upstream, model, and rate-card version, with the token classes and a labelled estimated cost.

**Security (LU-8).** No provider key reaches a pod on the metered path. Platform upstreams on Bedrock, Google Cloud, and Azure can use the gateway's workload identity, so no long-lived key exists for them. Posture reports each tenant's upstreams, shared cache domains, and unmetered grants.

**Cache isolation (LU-9).** Lenny never strips or rewrites `cache_control`. A provider's prompt cache is isolated by the provider account or workspace behind the upstream, so a regulated tenant gets its own upstream. Posture lists the other tenants that share each of a tenant's upstreams, and a grant that makes a `hipaa` or `fedramp` tenant share one emits a warning.

**Unmetered path (LU-10).** A runtime whose format the proxy does not parse reaches the provider through an egress credential binding under `delivery: inject`, allowed only for a tenant with the `unmeteredLlmEgress` grant and reported in posture.

**Forwarded attribution (LU-11).** The proxy sets `Lenny-*` headers on every forwarded request naming the tenant, environment, user, session, root session, runtime and revision, pool, and upstream, after removing any same-named header the runtime sent. User identifiers are pseudonymized per upstream by default. A downstream LLM gateway reads them to choose a per-user key or workspace, which also isolates each user's prompt cache, or to apply its own budgets.

```
 Runtime (pod)                       Gateway: LLM proxy                                Upstream
 ─────────────                       ──────────────────                                ────────
 POST /llm-proxy/v1/messages  ──>  1. proxy token -> session (SPIFFE check)
   model: claude-sonnet-5-5         2. format + model -> one upstream of the session's
   max_tokens: 8000                    list (pool list, else tenant list)
                                    3. rate card version in force; unpriced -> refuse
                                    4. reserve worst case in session, tree, user,
                                       tenant, and global scopes (one Redis script)
                                         refused in session or tree -> §8.6 extension
                                         terminal -> BUDGET_EXHAUSTED, no provider call
                                    5. PreLLMRequest, inject upstream credential,
                                       strip and set Lenny-* attribution        ──>  Anthropic API,
                                                                                       Bedrock, Vertex,
                                    6. read usage: input, cacheRead,             <──   OpenAI, Azure,
                                       cacheWrite by lifetime, output                  or an LLM gateway
                                    7. PostLLMResponse, settle reservation to
                                       estimated cost, write usage record
                 <──  response
```

"Limits" states what the change costs: rate cards for the operator to maintain, an estimate that differs from the invoice, a reservation that can refuse a call that would have fit, the loss of Lenny-side failover, and no metering on the unmetered path.

## Decisions

Settled by the owner on 2026-11-09. D1 settles the analysis's OD-1, D3 settles its OD-2 and OD-4, and D4 settles its OD-3. D7 and D8 were settled later that day, while this revision was written, and D8 narrows D1's list of credential sources:

| ID | Decision |
| :-- | :-- |
| D1 | Option D1 of the analysis, in the owner's words "Thin LLM proxy: sounds good". Keep a thin LLM proxy that meters usage and enforces budgets. Remove the LLM use of credential pools: leases, `credentialPolicy`, direct delivery mode, `CredentialRouter`, health, cooldown, fallback, renewal and rotation, `hotRotation`, `supportedProviders`, `credentialPropagation`, and the pre-claim check. Add an LLM upstream record with endpoint, credential source (`secretRef`, workload identity, or user credential), channel, contract, model allowlist, and rate card. Keep `CredentialPool` for VCS credentials. Pooling and failover go to an operator-run LLM gateway behind Lenny as an upstream. |
| D2 | Usage is attributed by provider, channel, contract (the upstream), and model. |
| D3 | Budgets are estimated cost in unitless credits, tied to no currency. The operator maintains a rate card per upstream, priced per model and per token class (input, cache read, cache write at 5 minutes, cache write at 1 hour, and output), with multipliers and a discount, versioned with `effectiveFrom`. Per-token-class budgets are rejected. The proxy reserves the worst case before forwarding and settles to the actual cost afterwards. Lenny ships no prices. |
| D4 | A tenant or a pool can be assigned several upstreams. Each call resolves to exactly one upstream by API format and model, and two upstreams in one list may not serve the same format and model. Failover between upstreams is out of scope. A tenant list can name tenant upstreams and platform upstreams granted to the tenant. An optional pool list overrides the tenant list, for placement reasons such as an EU endpoint. |
| D5 | Cache isolation follows the upstream's provider account or workspace. A regulated tenant gets its own upstream. Lenny does not strip `cache_control`. |
| D6 | The metering corrections of the adversarial review of the cache design are folded in: cache-write tokens are recorded by lifetime, billing corrections cover the new fields, streaming usage is taken from the last `message_delta`, OpenAI cached tokens are parts of the total input rather than additions to it, and the Open Responses `cached_tokens` value stops being hard-coded. RC-B and O2 of [`llm-response-cache-removal-design.md`](llm-response-cache-removal-design.md) move into this design. |
| D7 | Cache isolation rules (C1), settled in the owner's word "ok". Lenny never strips or rewrites `cache_control`. §4.9 states that provider cache isolation follows the upstream's provider account or workspace. Each tenant's security posture lists the other tenants that share each of its upstreams, and so share a provider cache. Granting a shared platform upstream to a tenant whose `complianceProfile` is `hipaa` or `fedramp` emits a warning rather than a refusal, and the posture shows it. Users of a shared tenant upstream can detect each other's cached prompt prefixes through `cache_read_input_tokens` and latency, which is a residual risk ("LU-9"). |
| D8 | No per-user upstreams, in the owner's words: "i would not include per-user upstreams as a requirement. What I would do instead is provide enough info that a downstream LLM proxy (downstream from Lenny's own proxy) could make that determination itself." An upstream's credential source is `secretRef` or `workloadIdentity`, which narrows D1's list, and Lenny resolves no provider key per user. The proxy forwards identity attributes that a downstream LLM gateway can use to choose per-user keys, workspaces, caches, or budgets, and strict cache isolation within a tenant is that gateway's choice ("LU-11"). |

Open, for the owner:

| ID | Question | Options | Recommendation |
| :-- | :-- | :-- | :-- |
| OD-5 | What does the proxy do with a call whose model the upstream's rate card does not price? | (a) Refuse it with `LLM_MODEL_UNPRICED`. (b) Price it as a configured fallback model and raise an alert. | (a). A call that cannot be priced cannot be reserved, and every scope that has a budget would otherwise spend at an assumed price. The refusal names the upstream and model, and `lenny-ctl llm unpriced` lists recently refused models, so the operator adds the price and the next call passes. |
| OD-6 | Do billing events carry an estimated cost? | (a) Quantities only. (b) Quantities and an estimated cost in credits, labelled as an estimate. | (b), as a stated exception to the principle of [`billing-resource-attribution-design.md`](billing-resource-attribution-design.md) that Lenny records quantities and the operator prices them. The quantities remain the billing record. The estimate is the figure Lenny enforced, so an operator who shows it to a tenant shows the number the tenant's budget was charged. |
| OD-7 | How does the proxy estimate a call's input before it runs? | (a) The request body's byte count. (b) The session's last reported input for the same upstream and model plus the bytes added since, with the byte count for a session's first call. (c) The provider's token-count endpoint. | (b). Agents resend a growing conversation, so the last count plus the growth is close to the true count, and the byte count bounds the first call. Option (a) refuses calls near a limit that would have fit, by several times on long text. Option (c) adds a round trip and its own rate limit to every call. "LU-6" states the estimator and its error. |
| OD-8 | Can a runtime reach an LLM provider through an egress credential binding that the proxy does not meter? | (a) Allowed only for a tenant holding the `unmeteredLlmEgress` grant from a `platform-admin`, and reported in posture. (b) Forbidden. | (a). Runtimes whose format the proxy does not parse (the `gemini-cli` and `cursor-cli` reference runtimes today) have no other path that keeps the key out of the pod. The grant keeps the decision with the operator, and posture shows the tenant and its security administrator that budgets do not cover that traffic. |
| OD-9 | Which formats does the proxy meter? | (a) `anthropic-messages` alone, as built. (b) `anthropic-messages`, `openai-chat`, and `openai-responses`. (c) (b) and `google-generate`. | (b) now and `google-generate` later. The OpenAI formats close F-4.9.48, and their translators exist. `google-generate` needs a parser and Google Cloud workload identity, and `gemini-cli` uses the unmetered path until it lands. |
| OD-10 | What happens to `vault_transit` and the `provider` path of `POST /v1/credentials`? | (a) Keep both. (b) Remove both. Lenny stores no user LLM key, and per-user provider keys live in a downstream LLM gateway keyed on the forwarded user attribute (D8). | (b). `vault_transit` has no Vault client in the tree and no consumer in any design. The `provider` path exists to feed the LLM credential router, which this design removes, and under D8 no upstream reads a user's key. |
| OD-11 | At what granularity does LLM usage reach records and events? | (a) One usage record per model call, rolled up into `token_usage.checkpoint`. (b) Checkpoint windows alone. (c) One billing event per call. | (a). Per-call records give the attribution D2 asks for and let a correction or an audit reach one call. Rolling them up keeps the billing stream at its current rate. |
| OD-12 | Where may a tenant upstream's endpoint point? | (a) Only at hosts in the platform's LLM host catalog and hosts a `platform-admin` adds to `llm.tenantEndpointHosts`. (b) At any public HTTPS host. | (a). The gateway sends the tenant's secret and its agents' prompts to the endpoint, from the gateway's own network position. An allowlist keeps a tenant from pointing the gateway at an arbitrary host, and the platform admin can add a tenant's LLM gateway by name. |
| OD-13 | How does the proxy carry forwarded attribution, and is it signed? | (a) Plain `Lenny-*` request headers, which the proxy strips and sets. (b) One signed `Lenny-Attribution` header, a JWT alone. (c) Plain headers on every upstream that sends attribution, and a signed `Lenny-Attribution` JWT beside them where the upstream sets `attribution.signed: true`. | (c). The downstream gateway already authenticates the caller as Lenny by the credential Lenny injects, which no pod holds (LUR-1), so a request bearing that credential carries headers only Lenny's proxy set. Off-the-shelf gateways read plain headers: LiteLLM records configured headers as spend tags and routes on header patterns, and Portkey maps headers into its metadata **[external]**. A signature adds protection where other clients share the gateway's credential or path, and a custom gateway policy can verify it. "LU-11" states both. |
| OD-14 | What is the default for user identifiers in forwarded attribution? | (a) Pseudonymous: an HMAC per upstream, with raw identifiers sent only when the upstream sets `attribution.rawIdentifiers`. (b) Raw identifiers by default. | (a). User identifiers in Lenny are often email addresses, and the provider guidance asks for opaque or hashed identifiers: Anthropic asks that `metadata.user_id` carry no name, email address, or phone number, and OpenAI recommends hashing the username or email for `safety_identifier` **[external]**. A gateway needs a stable key per user, which an HMAC gives, and a key per upstream keeps two upstreams from correlating one user. |

## Requirements

| ID | Requirement |
| :-- | :-- |
| LUR-1 | No LLM provider credential used on the metered path enters a pod, its environment, its files, or a frame the runtime receives. |
| LUR-2 | Each proxied call resolves to exactly one upstream of the session's effective list by API format and model, or is refused before any upstream is contacted. |
| LUR-3 | Every proxied call that reaches an upstream produces exactly one usage record, attributed to the tenant, environment, user, session, root session, runtime revision, routing labels, upstream, provider, channel, contract, requested model, served model, and rate-card version. |
| LUR-4 | A usage record carries every token class the format reports, with cache writes split by lifetime. Uncached input excludes cached and cache-write tokens on every format. On a stream, the totals are the last cumulative values the provider sent. |
| LUR-5 | No proxied call reaches an upstream unless its reservation was admitted in every budget scope the call belongs to. |
| LUR-6 | Settlement replaces each reservation with the call's estimated cost under the rate-card version in force when the call started. A call that reached the upstream and returned no usage settles at its reservation. |
| LUR-7 | Outside a Redis fail-open window, a scope's consumption exceeds its limit by no more than the estimator error of the calls in flight in that scope. |
| LUR-8 | Rate-card versions are append-only, and no release of Lenny carries a price. |
| LUR-9 | A session or tree scope that refuses a reservation triggers the §8.6 extension before the upstream is called, and a terminal extension returns `BUDGET_EXHAUSTED` with no upstream call. |
| LUR-10 | The proxy forwards `cache_control` and every other request field unchanged, apart from provider rehosting and the stream usage option. |
| LUR-11 | A session reaches an LLM provider host through an egress binding only when its tenant holds the `unmeteredLlmEgress` grant, and posture reports the grant and the bindings that use it. |
| LUR-12 | A tenant sees and names only its own upstreams and platform upstreams granted to it, and never reads an upstream's secret. |
| LUR-13 | Every request the proxy forwards to an upstream carries exactly the attribution that upstream's `attribution` settings select, with values the proxy computed from the session record. No header or body field of the same name that the runtime sent reaches the upstream. |
| LUR-14 | Each tenant's posture lists, for each of its upstreams, the other tenants that share it, and shows a warning for every shared upstream of a `hipaa` or `fedramp` tenant. |

## Current state

### Specification

- **§4.9 Credential Leasing Service [source].** The section runs from its heading to the end of `spec/04_system-components.md`. Its subsections are **Credential Provider**, **Credential Pool**, **Credential Storage Topology**, **Pre-Claim Credential Availability Check**, **Credential Lease**, **`materializedConfig` Schema by Provider**, **Credential Policy**, **Three Credential Modes**, **Pre-Authorized Credential Flow**, **Fallback Flow** with the `rotationTrigger` enum, **Proactive Lease Renewal** with **Rotation mode resolution.**, **LLM Reverse Proxy** with **Proxy dialects.**, **Authoritative token counting.**, and **Per-pool configuration.**, **Semantic Caching**, **`CredentialRouter` Interface** with **External LLM routing proxy integration.**, **Credential Governance Boundaries**, **Emergency Credential Revocation**, **Security Boundaries**, **Credential Lifecycle and Secret Management**, §4.9.1 KMS key rotation, and §4.9.2 audit events. The **Credential Provider** table says the runtime receives "Short-lived STS session credentials" for `aws_bedrock`, a short-lived access token for `vertex_ai`, a short-lived token for `azure_openai`, a static API key for `anthropic_direct`, and a short-lived Vault token for `vault_transit`.
- **Proxy dialects [source].** §4.9 defines `openai` and `anthropic`. The [§26](../spec/26_reference-runtime-catalog.md#262-shared-patterns-for-coding-agent-runtimes) catalog declares `proxyDialect: [google]` for `gemini-cli` and `[cursor]` for `cursor-cli`, and names providers (`openai_direct`, `gcp_vertex_gemini`, `google_ai_studio`, `cursor_direct`) that the §4.9 provider table does not list. **Authoritative token counting.** maps Anthropic, OpenAI, and Vertex input and output counts to `input_tokens` and `output_tokens`. No spec text names `cache_read_input_tokens`, `cache_creation`, or `cached_tokens`.
- **Runtime fields [source].** [§5.1](../spec/05_runtime-registry-and-pool-model.md#51-runtime) carries `supportedProviders` and `credentialCapabilities` (`hotRotation`, `proxyDialect`) only in its YAML examples and its merge table, where `supportedProviders` may be restricted by a derived runtime and `credentialCapabilities` is an override.
- **Delegation [source].** [§8.3](../spec/08_recursive-delegation.md#83-delegation-policy-and-lease) carries `maxTokenBudget` on the lease and on `lease_slice`, reserves tree budgets with `budget_reserve.lua`, waits for `ReportUsage` and `FINAL_USAGE_REPORT` before returning a child's budget, and defines **Credential propagation:** with `inherit`, `independent`, and `deny`. Item 5, **Over-run semantics.**, calls the slice "a **soft cap enforced at settlement**, not a per-call hard ceiling at the LLM provider".
- **Lease extension [source].** [§8.6](../spec/08_recursive-delegation.md#86-lease-extension), **Trigger:**, as proposal 0023 left it: "The gateway LLM Proxy detects budget exhaustion for a proxy-mode session when it settles the token usage of a completed upstream call". A granted extension delivers a non-streaming response the proxy already holds. An extension still pending at `proxyExtensionWaitTimeout` returns `BUDGET_EXHAUSTED` on a non-streaming call and lets a committed stream stand. Direct mode is "not eligible for an automatic in-path extension".
- **Budgets [source].** [§11.2](../spec/11_policy-and-controls.md#112-budgets-and-quotas) has the row "Token limits (LLM tokens)" with per-request, per-session, per-user window, per-tenant window, global window, and per-task-tree scopes. **Quota Update Timing:** makes proxy counts authoritative and pulls direct-mode counts over `ReportUsage`, of which "a malicious runtime could underreport". During a Redis outage the gateway enforces per session against the per-replica ceiling of §12.4.
- **Billing [source].** [§11.2.1](../spec/11_policy-and-controls.md#1121-billing-event-stream) emits `token_usage.checkpoint` with `tokens_input` and `tokens_output`, and `credential.leased` with `credential_pool_id`, `credential_id`, and `delivery_mode`. **Correction semantics** replace `tokens_input`, `tokens_output`, and `pod_minutes` alone. Category 1 `GATEWAY_CRASH_RECONSTRUCTION` reconciles "from pod-reported token totals". `billing.dualControlThreshold` has no stated unit.
- **Network [source].** [§13.2.6](../spec/13_security-model.md#1326-per-pool-egress-relaxation-and-egressprofile) pairs `deliveryMode: direct` with `egressProfile: provider-direct` (NET-006) and excludes IMDS from it (NET-044). [§13.2.5](../spec/13_security-model.md#1325-gateway-egress-to-upstream-llm-providers) renders the gateway's upstream egress rule "whenever `credentialPools[*].deliveryMode: proxy` is present".
- **Intra-pod frames [source].** [§28.5.3](../spec/28_communication-channels.md#2853-intra-pod) defines `credentials_rotated`, `credentials_acknowledged`, `llm_request_started`, and `llm_request_completed` with optional `inputTokens` and `outputTokens` on `CH-RUNTIMEOPS`, and the `llm` member of `session_start` on `CH-MSGSOCK` with `deliveryMode`, `dialect`, `apiKeyEnv`, and `headers`.

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
| F-4.9.48 | Open, High | The missing OpenAI routes. LU-B closes it. |
| F-11.2.38 | Open, High | The cached-token undercount. LU-A closes it. |
| F-4.9.32, F-4.9.33, F-4.9.37, F-4.9.39 | Open, High | Lease release, fallback exhaustion, the expired-lease sweep, and per-replica credential counts. LU-B resolves each as obsolete, because the machinery leaves. |
| F-4.9.34, F-4.9.38, F-4.9.40, F-4.9.41 | Open, Medium | Lease release retry, the user-credential availability check, lease-store errors, and the deny-list key. Obsolete under LU-B for the LLM half. F-4.9.41 is re-checked for VCS pools. |
| F-4.9.46, F-4.9.47 | Open, Medium | The inert `provider` values and the `/v1/credentials` wire names. Resolved by OD-10 and UC-1. |
| F-4.7.31, F-11.4.10 | Open | The rotation in-flight gate and the lease set `full_revoke` revokes. Obsolete for LLM leases. |
| F-8.3.9 | Open, Medium | No `FINAL_USAGE_REPORT` ends the usage quiescence wait. Obsolete, because quiescence becomes the settlement of in-flight reservations ("LU-6"). |
| F-11.2.34 | Open, Medium | Budget termination lost to a crash, because the enforcer counts in process memory. The same per-replica count lets a session whose calls land on several replicas spend up to about its budget times the number of replicas, because pods reach the proxy through one URL that the gateway Service balances across replicas. LU-C closes it through the Redis reservation and settlement of "LU-6". |
| F-4.9.29 | Open, Medium | No adapter code fills `llm.headers`. It stays, under the new `llm` member. |
| F-8.6.6 | Closed | Proposal 0023 placed the §8.6 trigger in the proxy at settlement. LU-C moves it to the refused reservation. |

The Bedrock streaming endpoint, the 120-second stream cut, the unregistered Responses translator, and the missing Pattern B base URL have no finding yet. "Validation owed" lists them for filing.

## LU-1 LLM upstreams

### Record

An upstream is a named record **[design]**:

```yaml
name: anthropic-acme
scope: tenant                      # tenant or platform
provider: anthropic                # the model vendor
channel: anthropic-api             # anthropic-api | aws-bedrock | gcp-vertex | azure-foundry | openai-api | gateway
contract: acme-anthropic-2026      # operator label on every usage record
format: anthropic-messages         # anthropic-messages | openai-chat | openai-responses
endpoint: https://api.anthropic.com
credential:
  secretRef: {name: llm-anthropic-acme, key: apiKey}   # or workloadIdentity
models: ["claude-sonnet-5-5*", "claude-haiku-5-5"]     # exact names or one trailing *
rateCard: acme-anthropic-2026
headers: {anthropic-version: "2023-06-01"}             # defaults the proxy adds when the call sends none
attribution:                       # "LU-11"; the defaults depend on the channel
  attributes: []                   # none toward a provider by default
state: enabled                     # enabled | disabled
```

- **Identity.** A tenant upstream's name is unique within its tenant, and a platform upstream's name is unique on the platform. A tenant upstream may not take a platform upstream's name, so a name in a list resolves to one record.
- **Format and channel.** The pair selects the translator: `anthropic-messages` on `anthropic-api` forwards the body, on `aws-bedrock` and `gcp-vertex` rehosts it as `rehostAnthropicBody` does today, and on `gateway` forwards it to the endpoint. `openai-chat` and `openai-responses` on `openai-api`, `azure-foundry`, and `gateway` forward the body with the channel's authentication and path. A pair with no translator is refused at write.
- **Provider.** One per upstream. An LLM gateway that routes to several providers is registered as one upstream per provider, each with its own model allowlist.
- **Model allowlist.** Each entry is an exact model name or a prefix followed by one `*`. A call naming another model is refused with `LLM_MODEL_NOT_ALLOWED`.
- **Disabling.** A disabled upstream refuses its calls with `LLM_UPSTREAM_DISABLED`. Revoking a credential is rotating its Secret or disabling the upstream.

### Credential sources

| Source | What the gateway holds | Who may use it |
| :-- | :-- | :-- |
| `secretRef` | A Secret in the credentials namespace, read through **Token Service secret resolution** as VCS tokens are, and cached in gateway memory per upstream. A `platform-admin` may register an existing Secret by reference. A `tenant-admin` submits `secretData`, which the gateway writes under `lenny-gateway-credential-writer`, as the admin API does for pool credentials today. | Platform and tenant upstreams. |
| `workloadIdentity` | The gateway's Kubernetes service account, federated to an AWS IAM role, a Google service account, or an Entra identity. The gateway exchanges it for short-lived provider credentials and signs or bears them per call. On Bedrock it can assume a role per tenant with the tenant as a session tag, as AWS documents for gateways **[external]**. | Platform upstreams alone, because the identity is the gateway's. |

No credential source resolves per user (D8). An operator who wants each user billed to, or cached in, the user's own provider account runs a downstream LLM gateway that holds the per-user keys and selects one from the forwarded user attribute ("LU-11").

The proxy never forwards a source's secret to a pod, and a `secretRef` Secret is named `lenny-llmupstream-` so the Token Service can admit its reads by prefix, as SO-3 does for `lenny-siemdest-` **[design]**.

### Admin API

| Route | Role | Effect |
| :-- | :-- | :-- |
| `POST`, `GET`, `PUT`, and `DELETE /v1/admin/llm-upstreams[/{name}]` | `platform-admin` for `scope: platform`, `tenant-admin` for the caller's tenant, under a new `manage_llm_upstreams` permission | Write and read upstreams. A read never returns secret material. `DELETE` is refused while a tenant or pool list names the upstream. |
| `PUT` and `DELETE /v1/admin/llm-upstreams/{name}/grants/{tenantId}` | `platform-admin` | Grant a platform upstream to a tenant, or withdraw the grant. A withdrawal removes the name from the tenant's lists and refuses the tenant's next call to it. |
| `GET /v1/admin/llm-upstreams?visibleTo={tenantId}` | `platform-admin`, `tenant-admin`, `tenant-viewer` | The tenant's own upstreams and the platform upstreams granted to it, with name, provider, channel, contract, format, models, rate card, and whether the upstream serves other tenants. |

Bootstrap gains `bootstrap.llmUpstreams` and `bootstrap.llmRateCards` beside `bootstrap.credentialPools`, which keeps VCS pools.

## LU-2 Assignment and resolution

**Lists.** The tenant record gains `llm.upstreams`, an ordered list that a `tenant-admin` writes and that may name the tenant's upstreams and platform upstreams granted to it. A pool gains an optional `llmUpstreams` list **[design]**:

- On a shared pool, the list may name platform upstreams alone, and at a session's creation it is filtered to those granted to the session's tenant.
- On an owned pool of [`tenant-derived-runtimes-design.md`](tenant-derived-runtimes-design.md#dr-5-owned-pools), the owning tenant writes the list and may name its upstreams and granted platform upstreams. The setting is gateway-read and adds nothing to the pod-set key.
- When the pool has a list, it replaces the tenant list for the session. The lists are never merged, so a pool placed on an EU endpoint never sends a call elsewhere.

**Uniqueness.** A list may not hold two upstreams that serve one format and one model. Two allowlist entries of one format conflict when they are equal, or when one is a prefix pattern whose prefix the other entry starts with. A write that would create a conflict in any list is refused with `LLM_UPSTREAM_CONFLICT` naming both upstreams and the overlapping entries. The check runs on a write to a list, to an upstream's `models` or `format`, and to a grant, against every list that names the upstream **[design]**.

**Session creation.** At the post-chain point, the gateway fixes the session's effective list and checks that it serves at least one format in the chosen revision's `llmFormats`. A runtime with an empty `llmFormats` needs no upstream. A session whose list serves none of its runtime's formats is refused with `LLM_UPSTREAM_NOT_CONFIGURED` (422), which replaces `CREDENTIAL_POOL_EXHAUSTED` at creation. The check reads configuration alone and replaces the pre-claim availability check. The session record keeps the effective list's names, so later list edits apply to new sessions only, and a withdrawn grant or a disabled upstream still refuses calls at once.

**Each call.** The route fixes the format, and the body's `model` fixes the model. The proxy finds the one upstream of the session's list whose format matches and whose allowlist matches the model, and refuses with `LLM_MODEL_NOT_ALLOWED` when none does. No call is retried on another upstream.

**Delegation.** A delegated child resolves its own list from its own tenant and pool, as any session does. `credentialPropagation` goes, so a child never draws on its parent's credential. Under cross-environment sharing (TS-3), the child uses the list of the environment it runs in.

## LU-3 The metering proxy

### Routes

| Format | Route on the proxy listener | Upstream path |
| :-- | :-- | :-- |
| `anthropic-messages` | `POST /llm-proxy/v1/messages` and `POST /llm-proxy/v1/messages/count_tokens` | `/v1/messages`, rehosted on Bedrock and Vertex. `count_tokens` is forwarded and charges nothing. |
| `openai-chat` | `POST /llm-proxy/v1/chat/completions` | `/v1/chat/completions`, or the Azure deployment path. |
| `openai-responses` | `POST /llm-proxy/v1/responses` | `/v1/responses`. |

Every other path, including batch, file, and realtime APIs, is answered with 404, because an unmetered path on the proxy could spend without a usage record. `OpenAIResponsesTranslator` is registered under its own key, so it no longer collides with `OpenAIDirectTranslator` **[design]**.

### Session binding

At bind, the gateway mints one proxy token per session, a random value stored as a hash with the session, its tenant, and the pod's SPIFFE URI, and revoked when the session ends or its pod is released. The pool's existing `spiffeBinding` setting keeps its meaning: when it is on, a request whose peer SPIFFE URI differs is refused **[design]**.

`session_start` carries an `llm` member with one entry per format the session's list serves and its runtime declares **[design]**:

```json
"llm": {
  "token": "<proxy token>",
  "formats": [
    {"format": "anthropic-messages", "baseUrl": "https://gateway-internal:8443/llm-proxy", "apiKeyEnv": "ANTHROPIC_API_KEY",
     "models": ["claude-sonnet-5-5*", "claude-haiku-5-5"], "headers": {"anthropic-version": "2023-06-01"}},
    {"format": "openai-chat", "baseUrl": "https://gateway-internal:8443/llm-proxy/v1", "apiKeyEnv": "OPENAI_API_KEY",
     "models": ["gpt-6.1*"]}
  ]
}
```

The runtime keeps the token out of its process environment, as §28.5.3 requires of the lease token today. `deliveryMode`, `dialect`, and the credentials file leave the frame and the adapter manifest.

### Pass-through

The proxy forwards the body unchanged apart from these edits **[design]**:

- Bedrock and Vertex rehosting, as today.
- On `openai-chat`, a streaming request without `stream_options.include_usage` gains it, because OpenAI reports stream usage only when asked **[external]**. The extra final chunk carries an empty `choices` array, which OpenAI SDKs read as the end of the stream.
- When the upstream sets `attribution.nativeUserField`, the provider-native user field carries the forwarded user value ("LU-11").

The proxy never strips or rewrites `cache_control`, and never sets or rewrites OpenAI's `prompt_cache_key`, so a runtime's caching requests reach the provider as sent (D7). It removes inbound `Lenny-*` headers and the headers an upstream's `attribution.headerMap` names, and sets its own ("LU-11").

`PreLLMRequest` and `PostLLMResponse` run on every metered call, and no longer carry the qualifier "proxy mode only". The upstream's `headers` are added when the request sends none, as the proxy adds `anthropic-version` today. A streaming call reads its body without the 120-second client timeout of `Forwarder`, under a deadline per format that the operator sets, so a long stream is not cut.

On `aws-bedrock`, the translator calls the Messages API at the `bedrock-mantle` endpoint when the upstream's endpoint names it, and InvokeModel otherwise, with `invoke-with-response-stream` for a stream. Anthropic's documentation places Opus 4.7 and later on the Messages API endpoint **[external]**. Under `workloadIdentity` it signs with SigV4, and under `secretRef` it sends a Bedrock API key as a bearer, as today.

### Usage normalization

The proxy maps each format's usage to token classes, so a rate card sees the same classes from every upstream **[design]**:

| Class | `anthropic-messages` | `openai-chat` | `openai-responses` |
| :-- | :-- | :-- | :-- |
| `input` | `input_tokens` | `prompt_tokens − cached_tokens − cache_write_tokens` (under `prompt_tokens_details`) | `input_tokens − cached_tokens − cache_write_tokens` (under `input_tokens_details`) |
| `cacheRead` | `cache_read_input_tokens` | `prompt_tokens_details.cached_tokens` | `input_tokens_details.cached_tokens` |
| `cacheWrite` by lifetime | `cache_creation.ephemeral_5m_input_tokens` as `5m` and `ephemeral_1h_input_tokens` as `1h` | `cache_write_tokens`, with its lifetime split where the response reports one | `cache_write_tokens_details.ttl_30m_tokens` as `30m` and `ttl_12h_tokens` as `12h` |
| `output` | `output_tokens` | `completion_tokens` | `output_tokens` |
| `reasoning`, inside `output` | `output_tokens_details.thinking_tokens` | `completion_tokens_details.reasoning_tokens` | `output_tokens_details.reasoning_tokens` |
| Per-request tools | `server_tool_use.web_search_requests` and `web_fetch_requests` | none metered | web search request counts |
| Modifiers | `service_tier`, `inference_geo`, and `speed` | `service_tier` | `service_tier` |

The Anthropic reference states that total input "is the summation of `input_tokens`, `cache_creation_input_tokens`, and `cache_read_input_tokens`", so `input_tokens` excludes cached tokens **[external]**. OpenAI's caching guide computes ordinary input as `input_tokens - cached_tokens - cache_write_tokens`, so OpenAI's cached and cache-write tokens are parts of the total input rather than additions to it **[external]**. A parser that added OpenAI's cached tokens to the input count would count each one twice, and one that read Anthropic's `input_tokens` alone undercounts, which is F-11.2.38. The table is the contract each parser is tested against.

**Streams.** The counts on Anthropic's `message_delta` are cumulative **[external]**, so the parser keeps the last value of each count it carries (`input_tokens`, `cache_read_input_tokens`, `cache_creation_input_tokens`, `output_tokens`, and `server_tool_use`). `message_delta` carries no `cache_creation` split by lifetime **[external]**, so the split comes from `message_start`. When the last `cache_creation_input_tokens` exceeds the sum of `message_start`'s split, the remainder is priced in the most expensive write lifetime the rate card's model entry defines. On `openai-chat` and `openai-responses`, usage comes from the final usage chunk and the `response.completed` event. A stream whose usage never arrives settles at its reservation ("LU-6").

**Client-facing surfaces.** The Open Responses and Chat Completions surfaces of the gateway report a turn's usage from the session's usage records in the turn, including `input_tokens_details.cached_tokens`, so neither hard-codes zero **[design]**.

## LU-4 Removal

The following leave the specification and the tree **[design]**. "Specification changes" and "Non-specification changes" give each site.

| Mechanism | Replacement |
| :-- | :-- |
| LLM providers on `CredentialPool` (`anthropic_direct`, `aws_bedrock`, `vertex_ai`, `azure_openai`, and the catalog's other LLM names) | LLM upstreams. The admin API refuses an LLM provider on a pool. |
| `vault_transit` (OD-10) | None. |
| `credentialPolicy` on the tenant and in the workspace plan | The tenant's `llm.upstreams` list. |
| LLM `CredentialLease`, `materializedConfig` for LLM providers, and the lease token | The proxy token. |
| `deliveryMode` and direct mode, with the credentials file, `lenny-cred-readers`, the adapter's expiry timer, and `ExtendCredentialLease` | The metered path, and the unmetered path of "LU-10". |
| `CredentialRouter`, assignment strategies, `maxConcurrentSessions` per credential, health scoring, and cooldown | An LLM gateway behind an upstream. |
| Fallback chains, `CREDENTIAL_FALLBACK_EXHAUSTED`, and `maxRotationsPerSession` | None. A failed call returns the provider's error. |
| Proactive renewal, rotation into pods, `rotationTrigger`, `credentials_rotated`, `credentials_acknowledged`, `hotRotation`, and §4.7.8 | Secret rotation in the gateway, which the next call reads. |
| The pre-claim availability check and `CREDENTIAL_POOL_EXHAUSTED` | The configuration check at the post-chain point and `LLM_UPSTREAM_NOT_CONFIGURED`. |
| `supportedProviders` | `llmFormats` with the session's list. |
| `credentialPropagation` and `CREDENTIAL_PROVIDER_MISMATCH` | Each child resolves its own list. |
| `ReportUsage`, the usage fields of `llm_request_completed`, `llm_request_started`, `FINAL_USAGE_REPORT`, the token-usage anomaly detector, and `TokenUsageAnomaly` | Proxy usage records alone. Activity is stamped by the proxy on each call, as `activity_stamp` does today. |
| `provider-direct`, NET-006, NET-044, `egressCIDRs.providers`, and the `lenny-direct-mode-isolation` webhook with `allowDirectModeStandardIsolation` | None, as EG-2 already proposes. |
| The LLM rows of the credential deny list and LLM emergency revocation | Disabling an upstream or rotating its Secret. |
| The `provider` path of `POST /v1/credentials` (OD-10) | None in Lenny. A downstream LLM gateway holds per-user provider keys and selects them from the forwarded user attribute (D8). |

`CredentialPool` keeps VCS providers, `hostPatterns`, the Secret topology, the Token Service secret read, emergency revocation of VCS credentials, KMS key rotation, and the audit events that VCS mints emit. Whether the adapter's credential file and `AssignCredentials` serve any VCS path is confirmed before deletion ("Validation owed").

## LU-5 Rate cards

### Card

A rate card belongs to one upstream, which fixes provider, channel, and contract, and holds versions **[design]**:

```yaml
name: acme-anthropic-2026
versions:
  - version: 3
    effectiveFrom: 2026-12-01T00:00:00Z
    endpointMultiplier: 1.0              # for example 1.1 on a regional endpoint
    discount: 0.0                        # applied after every multiplier
    models:
      - match: "claude-sonnet-5-5*"      # exact name or prefix with one trailing *
        creditsPerMillionTokens:
          input: 2.0
          output: 10.0
          cacheRead: 0.10
          cacheWrite: {5m: 2.5, 1h: 4.0}
        maxOutputTokens: 64000           # reservation ceiling when the call names no limit
        longContext:                     # optional higher rates above a prompt size
          aboveInputTokens: 200000
          creditsPerMillionTokens: {input: 4.0, output: 15.0, cacheRead: 0.20, cacheWrite: {5m: 5.0, 1h: 8.0}}
    responseModifiers:                   # multipliers from fields the response reports
      service_tier: {batch: 0.5, priority: 2.0}
      inference_geo: {us: 1.1}
      speed: {fast: 2.0}
    serverTools:
      creditsPerRequest: {web_search_requests: 0.01}
      reserveRequestsPerCall: 5          # reservation ceiling when the call sets no max_uses
```

The figures are illustrative credits, and no figure in this document is a price Lenny ships.

- **Validation.** The admin API refuses a card whose classes the upstream's format does not report, such as `cacheWrite.30m` on an `anthropic-messages` upstream, a model entry whose `match` the upstream's allowlist can never match, and a version without `effectiveFrom`.
- **Owner of prices.** A `platform-admin` writes cards for platform upstreams. A `tenant-admin` writes cards for the tenant's upstreams, where the tenant pays its own provider. A tenant reads the cards of its platform upstreams so that it can predict its budget use.
- **Versions.** A version is appended with `POST /v1/admin/llm-rate-cards/{name}/versions` and never edited or deleted while a usage record cites it. `effectiveFrom` may lie in the future and not in the past, so a recorded call's price never changes. Each usage record carries the card name and version.
- **Matching.** Settlement prices the served model when the version has an entry for it, and the requested model's entry otherwise. Both models are recorded.
- **No shipped prices.** Prices compiled into a release would go stale between releases, and an operator's contract differs from list prices. The documentation carries an example card, and `lenny-ctl llm rate-card validate` checks a card against its upstream.
- **Unpriced models (OD-5).** A call whose requested model no entry of the version in force matches is refused with `LLM_MODEL_UNPRICED` before reservation, and counted in `lenny_llm_unpriced_model_refusals_total` by upstream and model. `lenny-ctl llm unpriced` lists the pairs refused in a period.

### Pricing a call

The estimated cost of a call, in micro-credits, is the sum over its token classes of count times the class rate, plus server-tool requests times their rate, multiplied by the endpoint multiplier and each response modifier the response reports, and reduced by the discount. The long-context rates apply when the call's total input, cached and cache-write tokens included, exceeds `aboveInputTokens`. The computation uses integers with one rounding up at the end of each call, so no drift accumulates **[design]**.

The estimate leaves out volume tiers, committed-use discounts, provider credits, provisioned throughput, and taxes. An upstream on provisioned throughput carries a card with the operator's shadow price.

## LU-6 Credit budgets

### Scopes

Every token limit of §11.2 becomes a credit limit at the same scope, and `maxTokenBudget` becomes `maxCreditBudget` on the delegation lease, on `lease_slice`, and in the delegation presets **[design]**:

| Scope | Setting | Kept in |
| :-- | :-- | :-- |
| Per call | `quotas.llmCredits.perCall` | Checked against the reservation alone. |
| Session | The session's budget, or its `maxCreditBudget` slice | Redis, per session. |
| Delegation tree | The root's `maxCreditBudget` | Redis, per root session, beside the tree counters `budget_reserve.lua` keeps. |
| User window | `quotas.llmCredits.perUser` with its window | Redis, per tenant and user. |
| Tenant window | `quotas.llmCredits.perTenant` with its window | Redis, per tenant. |
| Global window | `quotas.llmCredits.global` with its window | Redis. |

### Reservation

Before forwarding a call, the proxy computes its worst case **[design]**:

1. **Output.** The call's output limit (`max_tokens`, `max_completion_tokens`, or `max_output_tokens`), else the model entry's `maxOutputTokens`. The proxy does not add a limit to the body.
2. **Input estimate (OD-7).** For a session's first call to an upstream and model, the body's byte count. For later calls, the total input of the session's last settled call to the same upstream and model plus the growth of the body's byte count since that call, with a shrinking body adding nothing.
3. **Price the ceiling.** The input estimate at the highest input-class rate the call can incur: the 1-hour write rate when a `cache_control` asks for `ttl: 1h`, the 5-minute write rate when one asks for the default, the highest write rate of the entry on an OpenAI format, and the input rate otherwise. The output limit at the output rate, the long-context rates when the estimate exceeds the threshold, server tools at `max_uses` or `reserveRequestsPerCall`, and the largest multiplier the card defines for each modifier the request does not fix.
4. **Reserve.** One Redis script, `llm_credit_reserve.lua`, checks the per-call ceiling and adds the amount to every scope's counter, or adds nothing and names the first scope that refuses. A reservation is recorded under a key with a time to live of the call's deadline plus a margin, so a reservation whose replica dies is released.
5. **Extension.** When the refusing scope is the session or the tree, the proxy requests the §8.6 extension for the session, waits up to `proxyExtensionWaitTimeout`, and on `GRANTED` or `PARTIALLY_GRANTED` reserves again. On `CEILING_REACHED`, `REJECTED`, an extension still pending at the deadline, or a refusal by a user, tenant, or global window, the proxy returns `BUDGET_EXHAUSTED` with `details.limitType` naming the scope and does not call the upstream.

### Settlement

When the response's usage arrives, the proxy prices it, and a second script replaces the reservation with the estimated cost in every scope it was added to. A call that returned an upstream error with no usage settles at zero. A call that reached the upstream and ended without usage, such as an interrupted stream, settles at its reservation, and its usage record says so. After a replica restart, a recovery sweep settles each expired reservation at its reserved amount and records `usage_source: reservation_recovered` **[design]**.

Settlement can take a scope past its limit when the input estimate undershot. The next call's reservation then refuses, and the extension runs before that call. No call is refused after it has run, so the §8.6 paths that deliver an already-computed response and that let a committed stream stand are no longer needed.

### Bounds

With reservations, a scope overshoots its limit only by the estimator's undershoot on the calls in flight in it, plus fail-open drift (LUR-7). The byte count is treated as an upper bound on a body's input tokens, and "Validation owed" confirms it per format, including images. Today's handler lets every in-flight call complete after a scope is exhausted.

During a Redis outage the proxy keeps the §11.2 fail-open model: it reserves against in-memory counters per session under the per-replica ceiling of §12.4, in credits, and reconciles with the MAX rule on recovery.

The session scope lives in Redis, so every replica that serves a session's calls reserves against and settles into the same counter, and a replica crash loses no count. This closes F-11.2.34, where today's in-process `sessionbudget.Enforcer` counts each replica's share of a session's calls separately and starts from zero after a crash **[design]**.

### Delegation

`budget_reserve.lua` carves a child's `maxCreditBudget` from its parent at `delegate_task`, as it carves tokens today. A child's budget return waits for the session's in-flight reservations to settle or expire, which replaces the `ReportUsage` and `FINAL_USAGE_REPORT` quiescence wait. §8.3 item 5, **Over-run semantics.**, becomes a hard ceiling per call up to the estimator bound.

### Worked example

A session's card prices `claude-sonnet-5-5` at 2.0 credits per million input tokens, 10.0 output, 0.10 cache read, and 2.5 for a 5-minute write. Its last call to that upstream and model settled 118,000 total input tokens, and the new body is 8,000 bytes longer and sets `max_tokens: 8000` with a 5-minute `cache_control` **[design]**.

- Reservation: input 126,000 at 2.5 is 0.315 credits, output 8,000 at 10.0 is 0.080 credits, for 0.395 credits held in every scope.
- The provider reports 2,000 input, 110,000 cache read, 6,000 5-minute write, and 1,500 output tokens.
- Settlement: 0.004 for input, 0.011 for cache reads, 0.015 for writes, and 0.015 for output, for 0.045 credits, and 0.350 credits return to every scope.

## LU-7 Usage records and billing events

### Usage record (OD-11)

Each metered call that reaches an upstream writes one row to a new `llm_usage` table in Postgres, keyed by an upstream request ID **[design]**:

- Identities: tenant, environment, user, session, root session, runtime, runtime revision, and routing labels.
- Upstream: name, scope, provider, channel, contract, and format.
- Models: requested and served.
- Counts: each token class with cache writes by lifetime, reasoning, and server-tool requests.
- Modifiers as reported: service tier, inference geography, and speed.
- Pricing: rate card, version, reserved micro-credits, estimated micro-credits, and `usage_source` of `response`, `reservation`, or `reservation_recovered`.
- The provider's request ID, so a call can be matched against the provider's records.

Rows follow the retention, erasure, and residency rules of the usage store, and a user erasure deletes the user's rows.

### Billing events

`token_usage.checkpoint` keeps its window and gains `llm_usage`, one entry per upstream, served model, and rate-card version in the window, with the token class counts, server-tool counts, and `estimated_cost_credits`, which the schema documents as Lenny's estimate under the named rate-card version (OD-6). `tokens_input` and `tokens_output` leave the event in favour of the class counts, with no duplicate fields **[design]**.

`credential.leased` keeps its VCS meaning, and its `delivery_mode` field names the VCS delivery mode. No LLM event replaces it, because no LLM lease exists.

### Corrections

- **Replacement fields.** A `billing_correction` of a `token_usage.checkpoint` replaces `llm_usage` as a whole, entry by entry, with counts and estimated cost together.
- **Dual control.** `billing.dualControlThreshold` keeps tokens and pod minutes. A new `billing.dualControlCreditThreshold`, in credits, governs operator corrections of `llm_usage`, with the same default of `0`.
- **Reconstruction.** Category 1 `GATEWAY_CRASH_RECONSTRUCTION` rebuilds a lost checkpoint from `llm_usage` rows, and `METERING_BUG` names a disagreement with the provider's records, in place of pod-reported totals.

This is the stated exception to the quantities principle of [`billing-resource-attribution-design.md`](billing-resource-attribution-design.md): Lenny prices LLM calls for enforcement and reports the price as an estimate, and the operator's billing system decides whether to use it.

## LU-8 Security

**Keys stay in the gateway.** On the metered path, a provider secret is read by the gateway through the Token Service or exchanged from its workload identity, and is held in gateway memory alone. The pod holds the proxy token, which spends credits only through the proxy, within the session's budgets, on the session's upstreams. With `spiffeBinding` on, a token copied out of the pod is refused at the proxy. No LLM credential is minted per pod, so the egress mint checks have nothing to check on this path **[design]**.

**No long-lived key for cloud channels.** A platform upstream on `aws-bedrock`, `gcp-vertex`, or `azure-foundry` can use `workloadIdentity`, so no long-lived provider key exists for it. Tenant upstreams use `secretRef`, because the gateway's identity cannot act for a tenant's cloud account without a trust relationship this design does not specify.

**Tenant boundaries.** A tenant names only its own upstreams and granted platform upstreams (LUR-12). A tenant upstream's endpoint must match the platform's allowlist under OD-12, and the gateway refuses a connection whose resolved address is in a cluster, private, or link-local range, checked at each connection.

**Network.** The gateway's upstream egress rule of §13.2.5, `allow-gateway-egress-llm-upstream`, renders from the upstreams' endpoints, or from the operator's CIDR list, in place of `credentialPools[*]`. The agent pod's egress to the proxy listener becomes unconditional for pods whose runtime declares a format, because every LLM call on the metered path goes there. The proxy still bypasses `netagent`, as [`egress-and-git-design.md`](egress-and-git-design.md) states today.

**Posture.** The SO-2 posture report gains an `llm` section: the tenant's effective upstream list per pool, each upstream's scope, provider, channel, contract, and credential source kind, the other tenants that share each upstream with their shared-cache warning (LUR-14), each upstream's attribution settings with the attributes sent and whether user identifiers are pseudonymous, the rate-card versions in force, the budgets per scope, the `unmeteredLlmEgress` grant, and the bindings that use it.

**Interceptors.** `PreLLMRequest` and `PostLLMResponse` stay, and with direct mode gone they see every metered call. The interceptor residual of [`security-oversight-design.md`](security-oversight-design.md#so-2-tenant-security-record-and-posture) is unchanged.

**Leak scanning.** UC-6's proxy-mode scan of `PreLLMRequest` covers every metered call, and its direct-mode gap closes. The unmetered path is not scanned.

## LU-9 Cache isolation

Lenny never strips or rewrites `cache_control` (D7) **[design]**. §4.9 states that provider cache isolation follows the upstream's provider account or workspace. Anthropic isolates caches by workspace on the Claude API, Claude Platform on AWS, and Microsoft Foundry, and by organization on Bedrock and Claude models on Google Cloud **[external]**. The isolation therefore follows the upstream:

- **Between tenants.** A shared upstream is one cache domain for every tenant it is granted to. A regulated tenant, or any tenant that must not share a cache, gets its own upstream backed by its own provider workspace or account. On the Claude API, a workspace per tenant also splits Anthropic's cost report by tenant **[external]**.
- **Posture.** Each tenant's posture lists, for each of its upstreams, the other tenants that share it, and so share its provider cache.
- **Warning on grant.** A grant that makes a tenant whose `complianceProfile` is `hipaa` or `fedramp` share an upstream, either by granting a shared upstream to that tenant or by granting that tenant's upstream to another tenant, succeeds with a `Warning` header and an audit event naming the tenants. The tenant's posture shows the warning while the sharing lasts, including after a later raise of the tenant's profile.
- **Within a tenant.** Sessions on one upstream share its cache. Lenny does not isolate users from one another. A downstream LLM gateway that uses the forwarded user attribute to choose a per-user provider workspace or key gives each user a separate cache ("LU-11"), and that is the documented option for strict isolation within a tenant.

The residual: users of a shared upstream without such a gateway can detect each other's cached prompt prefixes, through `cache_read_input_tokens` in their own usage and through latency. The provider returns no stored response, so no content crosses.

The proxy does not rewrite a top-level `cache_control`. A provider that refuses one returns its error to the runtime unchanged. Anthropic's caching guide states that the legacy Bedrock integration for Opus 4.6 and earlier returns 400 for a top-level `cache_control`, and states no such restriction for Google Cloud **[external]**.

## LU-10 Unmetered egress path

A runtime whose format the proxy does not parse, such as `gemini-cli` (Gemini) and `cursor-cli` (Cursor's API), reaches its provider through an egress credential binding with `delivery: inject` on the provider's host set, which `netagent` attaches so the agent process sees only a placeholder **[design]**:

- A binding whose hosts intersect an LLM host set of the platform catalog, or the endpoint host of any upstream, requires the `unmeteredLlmEgress` gate, which a `platform-admin` grants per tenant through EG-9's write path. Without the grant the binding write is refused.
- SO-2 gains the guardrail `unmeteredLlmEgress: forbid`, which a `security-admin` sets to refuse such bindings regardless of the grant, as `storedTokenPush` and `verbatimClientFiles` pair a gate with a guardrail.
- Lenny records no tokens and enforces no budget on this path. The egress decision log records each connection, and `lenny_llm_unmetered_connections_total` counts them per tenant and host set, so an operator sees the traffic's volume without its cost.
- Posture lists the grant and every binding that uses it.
- A session using the path carries `llm.unmetered: true` on its record, which `GET /v1/usage` reports beside the session's credits.

The LLM host reservation of the egress design names the endpoint hosts of upstream records, so a tenant egress route cannot claim a host that carries an operator's LLM credential.

## LU-11 Forwarded attribution

The proxy tells the upstream whom each call is for, so a downstream LLM gateway can choose per-user provider keys, workspaces, caches, or budgets without Lenny resolving any of them (D8) **[design]**.

### Attributes

| Attribute | Header | Value |
| :-- | :-- | :-- |
| `tenant` | `Lenny-Tenant` | The tenant ID. |
| `environment` | `Lenny-Environment` | The session's environment, or empty for a session with none. |
| `user` | `Lenny-User` | The session's user ID, pseudonymous by default. A delegated child sends the user its own session record names. |
| `session` | `Lenny-Session` | The session ID. |
| `rootSession` | `Lenny-Root-Session` | The root session ID, which names the delegation tree. |
| `runtime` and `runtimeRevision` | `Lenny-Runtime` and `Lenny-Runtime-Revision` | The runtime name and the session's revision number. |
| `pool` | `Lenny-Pool` | The pool name. |
| `upstream` | `Lenny-Upstream` | The upstream's name, prefixed with `platform/` or `tenant/`. |

The values come from the session record that the proxy token resolves to, never from the request. A header value carries only printable ASCII, and an attribute that would carry anything else is sent percent-encoded.

### Carrier (OD-13)

Under the recommended option (c):

- **Headers.** The proxy sets one `Lenny-*` header per selected attribute. Headers carry the attribution because they are the same for every format, they leave the body untouched, and off-the-shelf gateways read them: LiteLLM records the headers listed in `extra_spend_tag_headers` as spend tags and can route deployments on a header pattern with `tag_regex`, and self-hosted Portkey merges headers into request metadata through `HEADERS_TO_METADATA` **[external]**.
- **Gateway-native headers.** `attribution.headerMap` copies attributes into a gateway's own headers, such as `x-litellm-tags`, which LiteLLM reads as a comma-separated tag list for routing and spend, or `x-portkey-metadata`, whose `_user` key drives Portkey's per-user analytics **[external]**. Each entry is a template over the attributes, and the proxy escapes substituted values for the template's form, a comma-separated list or a JSON object, and refuses any other form at write.
- **Signature.** With `attribution.signed: true`, the proxy also sets `Lenny-Attribution`, a JWT valid for 60 seconds with `typ: lenny-llm-attribution+jwt`, the audience `lenny-llm-attribution:<upstream>`, and the selected attributes as claims, signed with a deployment attribution key. `GET /v1/admin/llm-attribution-keys` publishes the public keys as a JWK Set for the gateway's verifier. The key is separate from the gateway's signing key, and the type and audience follow the pattern of the egress design's gateway cause assertion (`lenny-cause+jwt`), so the assertion passes as no other Lenny token and no other Lenny token passes as it.

### Provider-native fields

Providers define their own user fields: Anthropic's `metadata.user_id`, "an external identifier for the user", at most 512 characters, which must carry no name, email address, or phone number, and OpenAI's `safety_identifier`, at most 128 characters, which replaces `user` with `prompt_cache_key`. OpenAI recommends hashing usernames or email addresses for it. LiteLLM keys its end-user budgets on the request's `user` field and warns that a client can set that field itself **[external]**.

With `attribution.nativeUserField: true`, the proxy writes the forwarded user value into `metadata.user_id` on `anthropic-messages`, into `safety_identifier` on the OpenAI formats, and also into `user` on an `openai-chat` upstream with `channel: gateway`, replacing any value the runtime sent. With it off, the default, the proxy leaves those fields as the runtime sent them, and a downstream gateway keys on the `Lenny-*` headers. An operator whose gateway keys budgets on the body's `user` turns the setting on, so a runtime cannot pick the identity its spend is charged to. The proxy never sets `prompt_cache_key`, because it steers the provider's cache (D7).

### Trust

Before forwarding, the proxy removes every inbound header whose name starts with `Lenny-`, case-insensitively, and every header an upstream's `headerMap` names, and then sets its own (LUR-13). With `nativeUserField` on, the runtime's values of the native fields are replaced. A downstream gateway trusts the headers because the request carries the upstream's credential, which only the gateway's proxy holds, and where other clients share that credential or path, it verifies `Lenny-Attribution`.

### Pseudonymization (OD-14)

Under the recommended option (a), `user` is sent as the base64url HMAC-SHA256 of the user ID under a key held per upstream, 43 characters, which fits every native field above. The key is a Secret named `lenny-llmupstream-<name>-attribution` that the gateway creates with the upstream. The value is stable for one user on one upstream and differs between upstreams. `attribution.pseudonymize` lists the attributes that are hashed, `[user]` by default, and an upstream's writer may add `tenant` or `environment`. `attribution.rawIdentifiers: true` sends every attribute raw. A `tenant-admin` may set it on the tenant's upstreams, and a `platform-admin` on platform upstreams. Rotating the key changes every pseudonym on that upstream, which resets a gateway's per-user state.

### Per-upstream configuration

```yaml
attribution:
  attributes: [tenant, environment, user, session, rootSession, runtime, runtimeRevision, pool, upstream]
  pseudonymize: [user]
  rawIdentifiers: false
  nativeUserField: false
  signed: false
  headerMap:
    x-litellm-tags: "lenny-tenant:{tenant},lenny-user:{user}"
```

| Channel | Default `attributes` | Reason |
| :-- | :-- | :-- |
| `gateway` | Every attribute, with `user` pseudonymous | The operator runs the gateway and placed it there to act on them. |
| Any provider channel | None | A provider has no use for Lenny's identifiers. An operator who wants per-user abuse attribution at the provider turns on `nativeUserField` with `attributes: [user]`. |

Posture reports each upstream's attribution settings, and the usage record names the upstream, so an operator can match a gateway's per-user spend against Lenny's records by pseudonym.

## Limits

- The operator maintains rate cards. A price change the operator misses is enforced at the old price until a new version is added.
- The estimate differs from the invoice by volume tiers, committed-use terms, credits, provisioned throughput, taxes, and provider rounding.
- A reservation sized from a byte count, on a session's first call, can refuse a call near a limit that would have fit.
- Lenny no longer fails over between credentials or providers. An operator who needs it runs an LLM gateway, and a gateway's choice of deployment reaches Lenny's records only through the served model it reports.
- A rate-card entry priced per model cannot tell two deployments of one model apart behind one gateway upstream. The operator registers each as its own upstream when their prices differ.
- Tenant upstreams cannot use workload identity.
- The unmetered path carries no budget, no usage record, and no `PreLLMRequest` scan.
- Within an upstream, cache timing and `cache_read_input_tokens` reveal shared prompt prefixes across users, unless a downstream LLM gateway separates users by the forwarded user attribute ("LU-9").
- Lenny holds no per-user provider key. An operator who bills users to their own provider accounts runs a downstream LLM gateway for it, and Lenny's rate card then prices calls the user's account pays for, so the operator writes that upstream's card to suit its own budgeting.
- With `nativeUserField` off, a runtime can set a provider-native user field itself, and a gateway that keys on that field trusts a value the runtime chose.
- Rotating an upstream's attribution key changes every pseudonym on it.
- Settlement at the reservation for a stream that ends without usage can overcharge a budget, by design in the safe direction.
- A runtime whose SDK cannot present a client certificate runs with `spiffeBinding` off, and its proxy token is a bearer that works from any address that reaches the proxy listener.

## Alternatives rejected

- **Keep credential pools and add attribution and budgets (the analysis's option A).** It keeps LLM-gateway functions, their open High findings, and direct mode's key in the pod.
- **Remove the proxy and the pools (option C).** Lenny would lose enforcement before spend, delegation-tree budgets, the §8.6 extension, and trusted per-session usage.
- **Meter at egress through an operator gateway (option D2).** `netagent` would run per-format parsers in every pod and need a reservation round trip per call, or Lenny would depend on one gateway product's spend schema. Neither gateway nor egress knows delegation trees.
- **Per-token-class budgets.** The owner rejected them. They give a token of a cheap model and one of an expensive model the same weight, and a cache-read budget penalizes caching.
- **Weighted tokens with fixed class weights.** Cache-read and cache-write multipliers differ by model and lifetime **[external]**, so fixed weights misprice models, and per-model weights are a rate card in another unit. A card in credits expresses either.
- **A currency unit per deployment or per tenant.** The owner chose unitless credits. A currency implies conversion and invoices that Lenny does not produce.
- **Failover across the upstreams of one list.** Two upstreams that serve one format and model would make attribution and cache isolation depend on which answered. A gateway upstream gives failover with one attribution.
- **Injecting a default output limit into the body.** Changing a runtime's request changes its behaviour. The card's `maxOutputTokens` bounds the reservation without touching the body.
- **Moving the proxy token into a `netagent` binding.** It would keep the token from the agent process, and it would route every LLM call through `netagent`, which the egress design keeps off the LLM path.
- **Per-user upstreams in Lenny.** An upstream whose credential is the session user's own key would isolate each user's cache, and the owner declined it as a requirement (D8). A downstream gateway gives the same isolation from the forwarded user attribute and keeps per-user keys out of Lenny.
- **Provider-native fields as the only carrier.** Each format has a different field with a different length limit, OpenAI's `metadata` is limited to 16 pairs, and writing them changes the body. Headers carry the same attributes for every format, and the native field remains a per-upstream option.
- **Raw user identifiers by default.** Provider guidance asks for opaque or hashed identifiers, and a raw email address would reach every upstream's logs.
- **Keeping §8.6's settlement-time trigger.** It lets a call run past a budget and then refuses its response, which the provider has billed.

## Specification changes

The following edits locate their targets by heading. Subsections of §4.9 named here are its bold-labelled H4 headings.

| Section | Change |
| :-- | :-- |
| [§4.1 Edge Gateway Replicas](../spec/04_system-components.md#41-edge-gateway-replicas), "Gateway Internal Subsystems", item 4 **LLM Proxy** | Describe the proxy as the metering, budget-enforcing pass-through that injects the upstream's credential. |
| [§4.3 Token Service](../spec/04_system-components.md#43-token-service), **Role:** item 2 and the availability bullets | LLM credentials become the Secrets that upstreams name, read through Token Service secret resolution. Remove credential leases from the availability bullets. |
| [§4.7.1](../spec/04_system-components.md#471-role-and-gateway-rpc-contract), [§4.7.3](../spec/04_system-components.md#473-adapter-events-on-ch-adapterevents), [§4.7.7](../spec/04_system-components.md#477-runtime-integration-levels-agent-type-only), [§4.7.8](../spec/04_system-components.md#478-credential-rotation-by-integration-level), [§4.7.9](../spec/04_system-components.md#479-startup-sequence-for-type-agent-runtimes), and [§4.7.11](../spec/04_system-components.md#4711-adapter-agent-security-boundary) | Remove `AssignCredentials`, `RotateCredentials`, `ExtendCredentialLease`, `ReportUsage`, and the **`CH-RUNTIMEOPS` completions.** activity rule, the `FINAL_USAGE_REPORT` row, rotation from the Full level, §4.7.8, startup step 5, and the LLM entries of **Runtime credential file contract.**, keeping any VCS use that validation finds. |
| [§4.7.6 Adapter Manifest Field Reference](../spec/04_system-components.md#476-adapter-manifest-field-reference), **Per-session fields.** | Replace `llm.deliveryMode`, `llm.dialect`, and `llm.apiKeyEnv` with `llm.token` and `llm.formats` entries carrying `format`, `baseUrl`, `apiKeyEnv`, `models`, and `headers`. |
| [§4.8 Gateway Policy Engine](../spec/04_system-components.md#48-gateway-policy-engine) | The `QuotaEvaluator` row names credit budgets. The `PreLLMRequest` and `PostLLMResponse` rows and **LLM interceptor phases.** state that the phases fire on every metered call and on no unmetered call. The `PostRoute` row drops the credential pool assignment. |
| [§4.9 Credential Leasing Service](../spec/04_system-components.md#49-credential-leasing-service), opening, **Credential Provider**, and **Credential Pool** | Narrow to VCS providers. Remove the LLM providers, `vault_transit`, the per-provider lease TTL rows for LLM providers, and the direct-mode text. |
| §4.9 **Credential Storage Topology** | Add `lenny-llmupstream-` Secrets beside pool credentials, and keep the topology. Remove the LLM rows of **Secret shape**. |
| §4.9 **Pre-Claim Credential Availability Check**, **Credential Policy**, **Three Credential Modes**, **Fallback Flow**, **Proactive Lease Renewal**, **`CredentialRouter` Interface**, and **Credential Governance Boundaries** | Delete, leaving a successor pointer to the new **LLM Upstreams** subsection for each identifier that moves. |
| §4.9 **Credential Lease** and **`materializedConfig` Schema by Provider** | Keep only what VCS mints use. Remove the proxy-mode and LLM direct-mode tables. |
| §4.9 **Pre-Authorized Credential Flow** | Under OD-10, remove the `provider` path. |
| §4.9, new subsection **LLM Upstreams** | The upstream record, credential sources, grants with the shared-upstream warning for `hipaa` and `fedramp` tenants, tenant and pool lists, resolution, and forwarded attribution with pseudonymization and the signed assertion. |
| §4.9 **LLM Reverse Proxy** | Rewrite in place: the routes per format, the pass-through and its edits, session binding by proxy token, usage normalization with the class table and the stream rules, the statement that Lenny never strips or rewrites `cache_control` and that provider cache isolation follows the upstream's provider account or workspace, with the residual within a tenant, and the unmetered path. **Proxy dialects.**, **Authoritative token counting.**, and **Per-pool configuration.** give way to formats, normalization, and lists. |
| §4.9 **Emergency Credential Revocation**, **Security Boundaries**, and [§4.9.2](../spec/04_system-components.md#492-credential-audit-events) | Remove the direct-mode steps, the LLM deny-list rows, `credential.renewed`, `credential.rotation_ceiling_hit`, `credential.fallback_exhausted`, the LLM meaning of `credential.rotated`, and `credential.proxy_mode_spiffe_binding_disabled`. |
| [§5.1 Runtime](../spec/05_runtime-registry-and-pool-model.md#51-runtime), examples and "Normative Merge Algorithm" | Replace `supportedProviders` and `credentialCapabilities` with `llmFormats`, a list a derived runtime may narrow. |
| [§5.2](../spec/05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes), **Slot (session mode)** | Remove the slot's credential lease and the per-slot credentials file from the scrub. Add the pool's `llmUpstreams`. |
| [§6.1](../spec/06_warm-pod-model.md#61-what-a-pre-warmed-pod-looks-like) and [§6.2](../spec/06_warm-pod-model.md#62-pod-state-machine) | Remove **Per-session credential lease lifecycle.**, the direct-mode idle-clock bullet, and lease assignment from the `resuming` failures. |
| [§7.1 Normal Flow](../spec/07_session-lifecycle.md#71-normal-flow) and [§7.3 Retry and Resume](../spec/07_session-lifecycle.md#73-retry-and-resume) | Replace step 3's pre-claim check with the list check at the post-chain point, and remove the lease map at finalize, rotation on `RATE_LIMITED`, lease release, derive rule 3's credential handling, and **Credential leases on the replacement pod.** |
| [§8.1](../spec/08_recursive-delegation.md#81-design-philosophy), [§8.2](../spec/08_recursive-delegation.md#82-delegation-mechanism), and [§8.3](../spec/08_recursive-delegation.md#83-delegation-policy-and-lease) | `maxTokenBudget` becomes `maxCreditBudget` on the lease, `lease_slice`, and the presets. Remove **Credential propagation:** with its pre-check and `CREDENTIAL_PROVIDER_MISMATCH`. Quiescence waits for in-flight reservations. Restate item 5, **Over-run semantics.**, as the reservation bound. |
| [§8.6 Lease Extension](../spec/08_recursive-delegation.md#86-lease-extension), opening, **Trigger:**, **Request:**, **Extendable fields:**, and the approval modes | The trigger is a session or tree reservation refused before the call. Remove the delivery of an already-computed response, the committed-stream case, and the direct-mode paragraph. `additionalTokenBudget`, `grantedTokenBudget`, and `maxExtendableBudget` are in credits. |
| [§8.8](../spec/08_recursive-delegation.md#taskrecord) and [§8.10](../spec/08_recursive-delegation.md#810-delegation-tree-recovery) | Remove the credential pre-check and lease from `created`, and state tree charging in credits. |
| [§10.2 Authentication](../spec/10_gateway-internals.md#102-authentication) | Remove credential lease bearers in direct mode. Add `manage_llm_upstreams`. |
| [§11.2 Budgets and Quotas](../spec/11_policy-and-controls.md#112-budgets-and-quotas) | The "Token limits" row becomes "Credit limits" with the same scopes. **Quota Update Timing:** names the reservation and settlement and drops direct mode. The fail-open paragraph, **Crash Recovery for Quota Counters:**, **Crash Recovery for Delegation Budget Counters:**, and **Maximum Overshoot Formula:** restate in credits with the reservation bound. |
| [§11.2.1 Billing Event Stream](../spec/11_policy-and-controls.md#1121-billing-event-stream) | `llm_usage` on `token_usage.checkpoint` in place of `tokens_input` and `tokens_output`, the estimate label, `credential.leased` for VCS, the correction replacement of `llm_usage`, `billing.dualControlCreditThreshold`, and reconstruction from `llm_usage` rows. |
| [§11.3](../spec/11_policy-and-controls.md#113-timeouts-and-cancellation) and [§11.4](../spec/11_policy-and-controls.md#114-user-invalidation) | Remove the credential lease TTL and expiry warning rows, and lease return from user invalidation. Add the stream deadline per format. |
| [§12.2](../spec/12_storage-architecture.md#122-storage-roles), [§12.4](../spec/12_storage-architecture.md#124-redis-ha-and-failure-modes), [§12.6](../spec/12_storage-architecture.md#126-interface-design), and [§12.8](../spec/12_storage-architecture.md#128-compliance-interfaces) | `CredentialPoolStore` loses lease, health, and cooldown state. Add the `llm_usage` store with its erasure row, the reservation keys, and credit counters in the fail-open rules. `QuotaStore` counts credits. Remove the LLM `CredentialLease` type. |
| [§13.1](../spec/13_security-model.md#131-pod-security), [§13.2.2](../spec/13_security-model.md#1322-agent-pod-allow-list-policies), [§13.2.4](../spec/13_security-model.md#1324-lenny-system-namespace-networkpolicies), and [§13.2.5](../spec/13_security-model.md#1325-gateway-egress-to-upstream-llm-providers) | The pod holds a proxy token. The proxy egress policy applies to pods whose runtime declares a format, without a delivery-mode label. Remove the `lenny-direct-mode-isolation` webhook row. NET-046 renders from upstream endpoints. |
| [§13.2.6](../spec/13_security-model.md#1326-per-pool-egress-relaxation-and-egressprofile) | Remove `provider-direct`, NET-006, and NET-044, as EG-2 stages. |
| [§13.3 Credential Flow](../spec/13_security-model.md#133-credential-flow) and [§13.5](../spec/13_security-model.md#135-delegation-chain-content-security) | Replace the direct-mode and proxy-mode LLM flows with the metered path and the unmetered path, restate **Translator trust boundary:** and **Key distinction:**, and name `maxCreditBudget`. |
| [§14](../spec/14_workspace-plan-schema.md#14-workspace-plan-schema) and [§14.1](../spec/14_workspace-plan-schema.md#141-workspaceplan-schema-versioning) | Remove `credentialPolicy` from the plan and from `CreateSessionRequest`. |
| [§15.1 REST API](../spec/15_external-api-surface.md#151-rest-api) | Add the upstream, grant, rate-card, and `GET /v1/admin/llm-attribution-keys` endpoints, the `Warning` on a grant that makes a `hipaa` or `fedramp` tenant share an upstream, the tenant `llm.upstreams` and pool `llmUpstreams` fields, and the codes `LLM_UPSTREAM_NOT_CONFIGURED`, `LLM_UPSTREAM_CONFLICT`, `LLM_UPSTREAM_DISABLED`, `LLM_MODEL_NOT_ALLOWED`, and `LLM_MODEL_UNPRICED`. `BUDGET_EXHAUSTED` gains `credit_budget` and the scope. Remove `CREDENTIAL_POOL_EXHAUSTED`, `CREDENTIAL_PROVIDER_MISMATCH`, `CREDENTIAL_RENEWAL_FAILED`, `LEASE_SPIFFE_MISMATCH` in favour of a proxy-token refusal, the lease delete blocker, and under OD-10 the `provider` field of `/v1/credentials`. `GET /v1/usage` reports credits and per-upstream counts. |
| [§15.2](../spec/15_external-api-surface.md#152-mcp-api), §15.2.1, [§15.4.3](../spec/15_external-api-surface.md#1543-runtime-integration-levels), §15.4.4, §15.4.6, and [§15.7](../spec/15_external-api-surface.md#157-runtime-author-sdks) | `get_token_usage` reports token classes and credits. Remove `CREDENTIAL_POOL_EXHAUSTED` from the error classes, credential rotation from the integration levels, the sample runtime, and the conformance suite, and the SDKs' rebind loop. |
| [§16.1 Metrics](../spec/16_observability.md#161-metrics), §16.1.1, [§16.3](../spec/16_observability.md#163-distributed-tracing), [§16.4](../spec/16_observability.md#164-logging), [§16.5](../spec/16_observability.md#165-alerting-rules-and-slos), and [§16.6](../spec/16_observability.md#166-operational-events-catalog) | Remove the LLM lease, pool, rotation, renewal, pre-claim, and anomaly metrics, the `proxy_dialect` and `delivery_mode` labels, `CredentialPoolExhausted`, `CredentialProactiveRenewalExhausted`, `OutstandingInflightAtRotationCeiling`, `DirectModeIsolationWebhookUnavailable`, and `TokenUsageAnomaly`, and the LLM meaning of `credential_rotated` and `credential_pool_exhausted`. Add `lenny_llm_calls_total`, `lenny_llm_credits_reserved_total`, `lenny_llm_credits_settled_total`, `lenny_llm_reservation_undershoot_credits`, `lenny_llm_usage_missing_total`, `lenny_llm_unpriced_model_refusals_total`, and `lenny_llm_unmetered_connections_total`, labelled by upstream, `format`, and model, with alerts for missing usage and frequent unpriced refusals. |
| [§17.2](../spec/17_deployment-topology.md#172-namespace-layout), [§17.7](../spec/17_deployment-topology.md#177-operational-runbooks), and [§17.8.2](../spec/17_deployment-topology.md#1782-capacity-tier-reference) | Remove webhook item 6 and its feature-flag row, the LLM parts of **Credential pool exhaustion** and **Emergency credential revocation**, and the LLM rows of **Credential pool sizing:**. Add a runbook for unpriced models and missing usage. |
| [§18.15](../spec/18_build-sequence.md#1815-phase-55--basic-credential-leasing-token-service), [§18.18](../spec/18_build-sequence.md#1818-phase-58--llm-proxy-lenny-direct-mode-isolation-admission-webhook), §18.23, and [§18.26](../spec/18_build-sequence.md#1826-phase-11--advanced-credentials-multi-provider-translators-revocation) | Restate the phases for upstreams and the metering proxy, retitle §18.18 without the webhook with an anchor redirect, and remove hot rotation, fallback chains, and the direct-mode runbook. Update `spec/README.md`. |
| [§19 Resolved Decisions](../spec/19_resolved-decisions.md#19-resolved-decisions), row 13 | The extension is triggered by a refused reservation. |
| [§23](../spec/23_competitive-landscape.md#23-competitive-landscape) and [§23.1](../spec/23_competitive-landscape.md#231-why-lenny) | Restate **Credential management** and item 9 as upstreams, metering, and credit budgets, without pooling, health scoring, or failover. |
| [§24.5 Credential Management](../spec/24_lenny-ctl-command-reference.md#245-credential-management) | Keep VCS pool commands, remove per-credential health scores and direct-mode text, and add `lenny-ctl llm` commands for upstreams, rate cards, and unpriced models. |
| [§25.3](../spec/25_agent-operability.md#253-gateway-side-ops-endpoints), [§25.6](../spec/25_agent-operability.md#256-diagnostic-endpoints), and §25.7 | Restate pool sizing and diagnosis for VCS, and map the new LLM codes to runbooks. |
| [§26.2](../spec/26_reference-runtime-catalog.md#262-shared-patterns-for-coding-agent-runtimes) and §26.3 to §26.11 | Replace **Credential delivery.**, **Common `credentialCapabilities`.**, and the LLM lease scopes with `llmFormats` and the metered path. Each runtime declares its formats. `gemini-cli` and `cursor-cli` declare none and use the unmetered path until a format exists, and §26.6's **Note on LLM-proxy dialect.** is restated. |
| [§28.3](../spec/28_communication-channels.md#283-registers), [§28.5.2](../spec/28_communication-channels.md#2852-pod-to-gateway), [§28.5.3](../spec/28_communication-channels.md#2853-intra-pod), [§28.5.5](../spec/28_communication-channels.md#2855-pod-egress), and [§28.8](../spec/28_communication-channels.md#288-failure-and-degradation-matrix) | Remove credential rotation from the `CH-RUNTIMEOPS` register row, `FINAL_USAGE_REPORT` and direct-mode `AUTH_EXPIRED`, the rotation frames, `llm_request_started`, and `llm_request_completed`, and `credential_rotation` from `lifecycle_capabilities`. Restate `session_start`'s `llm` member. `CH-LLMPROXY` keeps its identifier, and its card names formats, the proxy token, and the reservation refusal. |
| [§29.2](../spec/29_communication-scenarios.md#292-session-start), §29.4, §29.6, and §29.10 | Remove the pre-claim step, `AssignCredentials`, `ReportUsage`, `FINAL_USAGE_REPORT`, and per-slot leases. |
| §2, §12.9, §17 compose warnings, §24.2, §24.15, and §24.18 | Restate the single mentions of token budgets, LLM keys in pools, pool diagnosis, and the `chat` template's lease rebind loop. |

## Non-specification changes

| Area | Change |
| :-- | :-- |
| `pkg/gateway/llmproxy/llmproxy` | Routes per format, the translator registry keyed by format and channel with `OpenAIResponsesTranslator` registered, SigV4 and the Messages API endpoint on Bedrock, the streaming endpoint fix, the stream deadline in place of the client timeout, `Usage` with token classes, the parsers of "Usage normalization", `stream_options.include_usage`, upstream resolution, and the proxy-token check. |
| `pkg/gateway/llmproxy/upstreams` (new) | The upstream and rate-card stores in Postgres, grants with the shared-upstream warning, list conflict checks, the pricing function, the workload identity exchangers, and forwarded attribution: the header strip and set, `headerMap` templates with escaping, the native user field, the per-upstream HMAC keys, and the `Lenny-Attribution` signer with its key set. |
| `pkg/gateway/llmproxy/credrouter` | Delete. |
| `pkg/gateway/session/sessionbudget` | Replace the in-process enforcer with `llm_credit_reserve.lua`, the settlement script, the recovery sweep, and the §8.6 call at reservation. |
| `pkg/gateway/billing` | `llm_usage` rows, the checkpoint rollup, the correction fields, and the credit dual-control threshold. |
| `pkg/gateway/credentials` | Delete `credassign`, `credleasestore`, `credrenewal`, `credfallback`, `leaserevoke`, the LLM parts of `denylist` and `revocation`, and LLM providers in `credentialpoolstore`. Keep VCS pool reads and writes, `credentialstore` as UC-1 decides, and `impersonation`. |
| `pkg/credential` and `pkg/tokenservice` | Remove the LLM providers, `vault_transit`, `CredentialPolicy`, lease minting and validation, and `UserProxyDialect`. Admit `lenny-llmupstream-` reads. |
| `pkg/gateway/sessionserver` | Replace `credential_preclaim.go` with the list check at the post-chain point. Remove `credentialPolicy` from creation and responses. |
| `pkg/gateway/mcpfabric` and `pkg/delegation/lease` | Remove `credentialPropagation`, and rename the budget to `maxCreditBudget`. |
| `pkg/gateway/environment` | The tenant's `llm.upstreams`, the Open Responses and Chat Completions usage from records. |
| `pkg/gateway/externalapi/admin` and `errorclassify` | The new routes and codes, the refusal of LLM providers on pools, and the removed codes. |
| `pkg/adapter` and `cmd/lenny-adapter` | Remove `credentials.go`, `credexpiry.go`, `slotcreds.go`, and `credfile` to the extent they serve LLM leases, `usage.go`'s `ReportUsage`, the rotation and usage frames in `runtimeops.go`, and the `ManifestLLM` delivery fields. Add the `llm` member's new fields. |
| `cmd/lenny-gateway` | Remove `cred_renewal.go`, `cred_fallback.go`, `credential_lease_sweep.go`, `credential_lease_backfill.go`, `credential_denylist_rebuild.go`'s LLM part, `direct_usage.go`, `direct_usage_recovery.go`, and the LLM parts of `executor_credentials.go`, `credential_revocation.go`, and `credentialsurface.go`. Wire the upstream stores, the reservation, and the recovery sweep. |
| `pkg/apis/lenny/v1alpha1`, `pkg/controller/runtime`, and `pkg/gateway/runtime` | `llmFormats` in place of `supportedProviders` and `credentialCapabilities`. Regenerate the CRD manifests. |
| `schemas/lenny-adapter.proto` and `schemas/lenny-tokenservice.proto` | Remove `AssignCredentials`, `RotateCredentials`, `ExtendCredentialLease`, and `ReportUsage` with `reserved` numbers, and the LLM lease messages. |
| `migrations` | New tables `llm_upstreams`, `llm_upstream_grants`, `llm_rate_card_versions`, and `llm_usage`. Drop the LLM lease tables and the tenant `credential_policy` column. |
| `charts/lenny` | Remove the direct-mode webhook, `egressCIDRs.providers`, and the `deliveryMode` gates. Render `allow-gateway-egress-llm-upstream` from upstreams. Add `bootstrap.llmUpstreams`, `bootstrap.llmRateCards`, and `llm.tenantEndpointHosts`. |
| SDKs and reference runtimes | Read `llm.formats` and `llm.token`. Remove the rotation handling and the lease rebind loop. |
| Documentation | Replace `docs/operator-guide/external-llm-proxy.md` with a guide to gateway upstreams, add an operator guide to rate cards and credit budgets with an example card, and update the glossary, `security.md`, `metrics.md`, and the runbooks the egress design lists. |
| `BUILD-GAPS.md` | Close F-4.9.48, F-11.2.38, and F-11.2.34, resolve the lease-accounting findings of "Records" as obsolete, and file the findings "Validation owed" lists. |

## Tests

| Tier | Coverage |
| :-- | :-- |
| 1 | Each parser against the class table, on fixtures for every format, non-streaming and streaming: Anthropic input excludes cached tokens, OpenAI input subtracts cached and cache-write tokens, the last `message_delta` wins, the write split comes from `message_start` with the remainder priced at the higher lifetime, and a stream without usage yields no counts (LUR-4). Allowlist matching and the prefix conflict rule, the refusal of a conflicting list, upstream, or grant write, and resolution to one upstream or a refusal (LUR-2). The pool list replaces the tenant list, and a shared pool's list is filtered by grant. Pricing in integer micro-credits with long-context rates, modifiers, server tools, the endpoint multiplier, and the discount. Version selection by call start, refusal of a past `effectiveFrom`, and served-model pricing with the requested-model fallback (LUR-8). An unpriced model refused before reservation (OD-5). The reservation's input estimate for a first call and a later call, and the worst-case price for each `cache_control` lifetime. Settlement at the reservation for a missing usage report and at zero for an upstream error. The worked example's figures. Forwarded attribution: each channel's default attribute set, the pseudonym stable for one user on one upstream and different on another, `rawIdentifiers` sending raw values, `headerMap` templates escaped for a tag list and for a JSON object and a template of another form refused at write, the native user field written per format when set and left alone when not, `prompt_cache_key` and `cache_control` never changed, and the `Lenny-Attribution` JWT's type, audience, expiry, and claims (LUR-13). The grant warning fires when a grant makes a `hipaa` or `fedramp` tenant share an upstream in either direction, and not otherwise. |
| 2 | `llm_credit_reserve.lua` and the settlement script against Redis: all scopes or none, the per-call ceiling, the refusing scope named, expiry of an abandoned reservation, and the recovery sweep. The new tables and the dropped columns on Postgres, and `llm_usage` erasure by user. |
| 3 | The admin schemas for upstreams, grants, and rate cards, with no secret in any read. The `llm` member of `session_start` and the manifest without delivery fields. The new error codes and `BUDGET_EXHAUSTED` details. `token_usage.checkpoint` with `llm_usage` and the estimate label, and the correction body. The proto files with reserved numbers. |
| 4 | On the compose stack against an upstream fake per format: a session's calls through each route record usage with every attribution field (LUR-3). A call over the session budget triggers the extension before the fake is called, and a terminal extension returns `BUDGET_EXHAUSTED` with no call reaching the fake (LUR-5, LUR-9). A user, tenant, or global window refusal returns `BUDGET_EXHAUSTED` with no extension. A delegated child carves `maxCreditBudget`, resolves its own list, and returns its budget after its reservations settle. A checkpoint rolls up per upstream, model, and version. A session whose list serves none of its runtime's formats is refused at creation. `cache_control` reaches the fake byte for byte (LUR-10). A binding on an LLM host is refused without the grant, accepted with it, refused under the guardrail, and reported in posture (LUR-11). A runtime that sends `Lenny-User`, `lenny-tenant`, an `x-litellm-tags` header that the upstream's `headerMap` names, or, with `nativeUserField` on, its own `metadata.user_id` reaches the gateway fake with the proxy's values alone, and a delegated child's request carries its own session and its root (LUR-13). The `Lenny-Attribution` JWT verifies against `GET /v1/admin/llm-attribution-keys` and fails for another upstream's audience. Posture lists the tenants sharing each upstream and the warning for a shared upstream of a `hipaa` tenant (LUR-14). |
| 5 | On Kind: the agent pod reaches the proxy listener without a delivery-mode label, the gateway reaches an upstream through the rendered egress rule, and a Bedrock upstream under workload identity signs with the gateway's role against a recorded endpoint. |
| 7a | Concurrent calls in one session and one tree never take a scope past its limit by more than the estimator bound, under `-race`. A withdrawn grant during concurrent calls refuses every later call. |
| 8 | A replica killed mid-stream leaves a reservation that the sweep settles at its amount. A Redis outage falls back to the per-replica ceiling in credits and reconciles on recovery. |
| 9 | No provider secret appears in a pod's environment, files, `session_start`, or logs (LUR-1). A proxy token from another pod is refused under `spiffeBinding`. A tenant cannot name another tenant's upstream or an ungranted platform upstream, cannot read a secret, and cannot point an upstream at a host outside the allowlist or at a cluster, private, or link-local address (LUR-12). A runtime cannot raise its budget by omitting `max_tokens` or by streaming. A runtime cannot forge or remove any forwarded attribute, and a raw user ID never reaches an upstream whose settings make it pseudonymous (LUR-13). |
| 11 | The rate-card guide's example validates with `lenny-ctl llm rate-card validate`, every new code maps to a runbook, and no document describes direct mode or LLM credential pools. |
| Static | A repository check finds no `deliveryMode`, `credentialPolicy`, `CredentialRouter`, `hotRotation`, `supportedProviders`, `credentialPropagation`, or `vault_transit` outside migrations, the records, and `proposals/`. |

Every test carries the `// spec:` annotation, and tests at tier 2 and above carry `// diagnosis:`, as `test-coverage.md` requires.

## Validation owed

| Item | Why it matters | Status |
| :-- | :-- | :-- |
| Read the usage of a Claude response on Bedrock InvokeModel, on the Bedrock Messages API, and on Vertex `rawPredict`, streaming and non-streaming, including `cache_creation` and its split | The class table assumes the Anthropic field names on every Anthropic-format channel. | Open. The cache removal design lists the same gap. |
| Confirm that `message_start`'s `cache_creation` split is final for the call | "Streams" takes the split from `message_start` and the total from the last `message_delta`. | Open. |
| Confirm whether OpenAI Chat Completions includes `cache_write_tokens` in `prompt_tokens`, and how it reports the write lifetime | The caching guide's worked example uses the Responses names alone **[external]**. | Open. |
| Confirm what each provider reports and bills for a stream the client cancels | Settlement at the reservation assumes a cancelled stream can carry no usage. | Open. |
| Confirm that a body's byte count bounds its input tokens for each format's tokenizer, and bound image tokens | The first-call estimate and LUR-7 rest on it. | Open. |
| Measure the OD-7 estimator on recorded agent traffic: refusals near a limit and undershoot per call | The bound of LUR-7 depends on the estimator's error. | Open. |
| Measure `llm_credit_reserve.lua` latency at the proxy's request rate | Each call adds two script runs. | Open. |
| Confirm Bedrock's endpoints for Opus 4.7 and later, SigV4 from the gateway's workload identity, and per-tenant session tags on a live account | "Pass-through" and "Credential sources" rely on them. | Open. |
| Confirm Azure Foundry's Responses API path and its usage fields | `openai-responses` on `azure-foundry` is assumed. | Open. |
| Confirm which gateways preserve provider usage fields and report the served model | `channel: gateway` meters what the gateway returns. | Open. |
| Confirm that no VCS path uses `AssignCredentials` or the adapter's credentials file | "LU-4" deletes them for LLM leases. | Open. |
| Confirm, against a running LiteLLM and a self-hosted Portkey, that `extra_spend_tag_headers`, `tag_regex`, `x-litellm-tags`, and `HEADERS_TO_METADATA` act on the `Lenny-*` headers as "LU-11" states | The recommendation of OD-13 rests on the documentation alone. | Open. |
| File the defects found while verifying: Bedrock streams sent to the non-streaming `invoke` path, the 120-second client timeout on streams, the unregistered `OpenAIResponsesTranslator` and its provider key collision, the missing Pattern B base URL, `UserProxyDialect` mapping `vertex_ai` to an unserved dialect, and `credential.leased` emitted for no LLM lease | They are defects in the tree today, independent of this design. | Open. This design edits no record. |
| Read again the external facts this design carries from the analysis: the Bedrock and Vertex endpoint facts, Anthropic's workspace limits and cost reports, Azure's deployment types, and AWS's per-user role sessions for gateways | "Sources" marks them. | Open. |

## Instructions for reviewers of other designs

- [`llm-response-cache-removal-design.md`](llm-response-cache-removal-design.md): its reviewer:
  - replaces RC-B in "Proposal split and order", the "Metering (O2)" half of RC-5, O2, RCR-6, the O2 rows of "Specification changes", and the O2 tests with a pointer to LU-A of this design, which adopts the cost weighting of O2's option (c) through rate cards in credits and keeps every class as a separate count;
  - keeps O1 and the passthrough half of RC-5, restated by D7 as settled: Lenny never strips or rewrites `cache_control`, isolation follows the upstream's provider account or workspace, posture lists the tenants sharing each upstream, a grant that makes a `hipaa` or `fedramp` tenant share one emits a warning, and users of a shared upstream can detect each other's cached prompt prefixes through `cache_read_input_tokens` and latency unless a downstream LLM gateway separates them by the forwarded user attribute (LU-11). Its advice to give each tenant's pools a separate provider organization or workspace becomes a separate upstream per tenant;
  - corrects the provider facts against the pages read on 2026-11-09: Anthropic's caching guide states that the legacy Bedrock integration for Opus 4.6 and earlier returns 400 for a top-level `cache_control` and states no restriction for Google Cloud, so RCR-5 and its test expect the provider's error to pass through unchanged on that integration, and the OpenAI retention figure is checked against the guide's 30-minute and 12-hour write lifetimes **[external]**;
  - adds the anchor redirect for the retitled §18.39, which `TESTING.md` links.
- [`billing-resource-attribution-design.md`](billing-resource-attribution-design.md): its reviewer states the exception to "Lenny records quantities, and the operator's billing system prices them" for LLM calls (OD-6), names `llm_usage` among the fields a `billing_correction` replaces, adds `billing.dualControlCreditThreshold` beside the threshold that "adds tokens and pod minutes", and keeps "Token metering, which §11.2 already covers" out of its scope, pointing at this design.
- [`egress-and-git-design.md`](egress-and-git-design.md): its reviewer:
  - restates P3 as "LLM provider keys reach a provider only from the gateway, or through an `inject` binding under the `unmeteredLlmEgress` gate";
  - drops the `deliveryMode: direct` pairing from "Consolidating `provider-direct`" and decision 1, the "Long LLM streams in direct mode" scenario, and the residuals that rest on direct mode;
  - adds the `unmeteredLlmEgress` gate to "Delivery modes" with LU-10's rule for bindings on LLM hosts;
  - restates the LLM host reservation in "Configuration by scope" as the endpoint hosts of upstream records;
  - restates the Token Service readiness text without "the gateway lease path" for LLM credentials, the quota sentence on "`maxConcurrentSessions` times the LLM streams", and `credential.leased` as a VCS event;
  - keeps the LLM proxy outside `netagent` and keeps `CH-LLMPROXY` unchanged.
- [`tenant-derived-runtimes-design.md`](tenant-derived-runtimes-design.md): its reviewer removes the `supportedProviders` row from "Fields", replaces the `credentialCapabilities` row with `llmFormats` (narrow, a subset of the base's), removes `deliveryMode` and `allowDirectModeStandardIsolation` from DR-5's template copy row, adds the owned pool's `llmUpstreams` as a tenant-settable, gateway-read setting, replaces DR-6's "no provider left" case with an emptied `llmFormats`, removes `validatePoolProxyDialect` from DR-4 "Reading", and records a change to settled decision 14: the residual is the session's proxy token on a pool with `egressProfile: internet`, bounded by its budgets, since no LLM credential lease exists.
- [`runtime-revisions-design.md`](runtime-revisions-design.md): its reviewer replaces `credentialCapabilities` with `llmFormats` (versioned, gateway-read) in "Field classes", removes `supportedProviders` from the live class, replaces the note that "A pool with `deliveryMode: proxy` requires `credentialCapabilities.proxyDialect`" with the list check at the post-chain point on the chosen revision, restates RV-5's narrowing examples with `llmFormats`, and changes the tier 1 test row accordingly.
- [`security-oversight-design.md`](security-oversight-design.md): its reviewer adds the `llm` posture section of LU-8, with the other tenants that share each upstream and the shared-upstream warning for `hipaa` and `fedramp` tenants of D7, lists `unmeteredLlmEgress` in the posture's `gates` section, adds the guardrail `unmeteredLlmEgress: forbid` to SO-2, and confirms that SO-3's citations of **Credential Storage Topology** and **Token Service secret resolution** stay in §4.9. The interceptor residual is unchanged.
- [`user-credential-delivery-design.md`](user-credential-delivery-design.md): its reviewer, under OD-10, makes decision 3 the `type` path alone, with no LLM provider among the types, because Lenny stores no user LLM key and a downstream LLM gateway holds per-user keys (D8); drops decision 5's LLM-path meaning and the statement that the `credentials` table stays for LLM credentials; drops "`RotateUser` only re-caches for the LLM proxy" and the `credentialPropagation` remark in UC-3; and removes the direct-mode gap from UC-6, adding the unmetered path to its limits.
- [`delegation-availability-design.md`](delegation-availability-design.md): its reviewer reads the budgets of §8.3 in its terms and its O3 recommendation as credit budgets. D4's per-hop switch `lease_slice.maxChildrenTotal: 0` is unaffected, and `lease_slice.maxTokenBudget` becomes `maxCreditBudget`.
- [`anthropic-messages-api-design.md`](anthropic-messages-api-design.md): its reviewer:
  - replaces the AM-8 row `503 CREDENTIAL_POOL_EXHAUSTED` with `422 LLM_UPSTREAM_NOT_CONFIGURED` mapped to `invalid_request_error` with `x-should-retry: false`, and adds `BUDGET_EXHAUSTED` with its scope;
  - restates AM-6's usage from the session's usage records in the turn, so `cache_read_input_tokens` and `cache_creation_input_tokens` report the turn's values, and decides whether `input_tokens` reports the sum over the turn's model calls or the last call's input, because a summed figure can trigger a client's compaction;
  - keeps "Kept" for the proxy's `anthropic-messages` route and listener, now one of several formats, and states that the proxy's wire types stay shared with the client surface.
- [`connector-authorization-and-external-memory-design.md`](connector-authorization-and-external-memory-design.md): its reviewer replaces the `CREDENTIAL_POOL_EXHAUSTED` analogy in "Check at delegation" under CA-7 with `LLM_UPSTREAM_NOT_CONFIGURED`.
- [`tenant-outbound-connector-design.md`](tenant-outbound-connector-design.md): its reviewer replaces "any LLM host a pool names" in the reserved set and the `outboundGateway.reservedHosts` row with the endpoint hosts of upstream records.
- [`runtime-routing-design.md`](runtime-routing-design.md): its reviewer removes `credential_pool_id` from the `PostRoute` payload rows and "the credential pool assignment" from RT-2's payload audit.
- [`tenant-scoping-identity-design.md`](tenant-scoping-identity-design.md): its reviewer removes the `credentialPropagation: inherit` row from TS-3's interactions table and states that a crossing child uses the target environment's upstream list. TS-6 keeps `allow-gateway-egress-llm-upstream`, which now renders from upstream endpoints.

## Proposal split and order

| Proposal | Content | Depends on |
| :-- | :-- | :-- |
| LU-A | Metering corrections: token classes with cache writes by lifetime, the parsers and streaming rules of "Usage normalization", the `llm_usage` records, the class counts on `token_usage.checkpoint`, the correction fields, and the client-facing usage of the Open Responses and Chat Completions surfaces. Closes F-11.2.38 and absorbs RC-B and O2 of the cache removal design. | Nothing. It runs on today's proxy and records the pool in place of the upstream until LU-B. |
| LU-B | Upstreams and removal: LU-1, LU-2, the routes and session binding of LU-3, LU-4, LU-8, LU-9, and LU-11, with the specification rewrite of §4.9's LLM half and every section of "Specification changes" that the removal reaches. Closes F-4.9.48 and resolves the lease findings. | LU-A, and EG-2 for the removal of `provider-direct`. |
| LU-C | Rate cards and credit budgets: LU-5, LU-6, and the estimated cost of LU-7, with §8.3, §8.6, §11.2, and §11.2.1 in credits. Closes F-11.2.34. | LU-B, and the owner's answers to OD-5, OD-6, and OD-7. |
| LU-D | The unmetered path: LU-10, the gate, the guardrail, and posture. | LU-B, EG-4, EG-9, and SO-2. |
| LU-E | The `google-generate` format and Google Cloud workload identity. | LU-C, and the owner's answer to OD-9. |

LU-A lands first because it fixes a metering defect that exists today. LU-B and LU-C can merge into one proposal if the owner prefers to change budgets in the same step as their anchor.

## Sources

This design read the pages marked "re-read" on 2026-11-09. The others were read on the same date by the owner's analysis that this design develops, and "Validation owed" lists them for a second reading.

- Anthropic, Messages API reference, https://platform.claude.com/docs/en/api/messages/create, re-read. The `usage` fields and the statement that total input is the sum of `input_tokens`, `cache_creation_input_tokens`, and `cache_read_input_tokens`.
- Anthropic, Streaming messages, https://platform.claude.com/docs/en/build-with-claude/streaming, re-read. "The token counts shown in the `usage` field of the `message_delta` event are *cumulative*", and the fields of `MessageDeltaUsage`.
- Anthropic, Prompt caching, https://platform.claude.com/docs/en/build-with-claude/prompt-caching, re-read. Isolation by workspace and by organization per platform, and the legacy Bedrock refusal of a top-level `cache_control`.
- Anthropic, Pricing, https://platform.claude.com/docs/en/about-claude/pricing, re-read. Cache writes at 1.25 times and 2 times base input, and cache reads at 0.1 times with per-model exceptions.
- OpenAI, Prompt caching guide, https://developers.openai.com/api/docs/guides/prompt-caching, re-read. Ordinary input computed as `input_tokens - cached_tokens - cache_write_tokens`, and `cache_write_tokens_details` with `ttl_30m_tokens` and `ttl_12h_tokens`.
- OpenAI, Chat Completions create, https://developers.openai.com/api/reference/resources/chat/subresources/completions/methods/create, re-read. Stream usage under `stream_options.include_usage`, and the final usage chunk that may not arrive on an interrupted stream.
- Anthropic, Claude in Amazon Bedrock, https://platform.claude.com/docs/en/build-with-claude/claude-in-amazon-bedrock, from the analysis. The Messages API endpoint on `bedrock-mantle`.
- Amazon Web Services, IAM principal cost tracking, https://docs.aws.amazon.com/bedrock/latest/userguide/cost-mgmt-iam-principal-tracking.html, from the analysis. Per-user role sessions with session tags for gateways.
- Anthropic, Workspaces, https://platform.claude.com/docs/en/manage-claude/workspaces, and Usage and Cost API, https://platform.claude.com/docs/en/manage-claude/usage-cost-api, from the analysis. Workspace cost reports and limits.
- Microsoft, Deployment types, https://learn.microsoft.com/en-us/azure/ai-foundry/foundry-models/concepts/deployment-types, from the analysis.
- Anthropic, Messages API reference, https://platform.claude.com/docs/en/api/messages/create, re-read for `metadata.user_id`: "An external identifier for the user who is associated with the request", at most 512 characters, "a uuid, hash value, or other opaque identifier", with no name, email address, or phone number.
- OpenAI, Chat Completions create, https://developers.openai.com/api/reference/resources/chat/subresources/completions/methods/create, re-read for `user`, which "is being replaced by `safety_identifier` and `prompt_cache_key`", `safety_identifier` at most 128 characters with hashing recommended, and `metadata` as 16 pairs.
- LiteLLM, Cost tracking, https://docs.litellm.ai/docs/proxy/cost_tracking, read. The `user` field recorded as the end user, with the warning that end users can set it themselves, `metadata.tags`, `x-litellm-tags`, `extra_spend_tag_headers`, and `x-litellm-spend-logs-metadata`.
- LiteLLM, Users and budgets, https://docs.litellm.ai/docs/proxy/users, read. End-user budgets keyed by the request's `user` through `max_end_user_budget_id`.
- LiteLLM, Tag routing, https://docs.litellm.ai/docs/proxy/tag_routing, read. Tags from the body or `x-litellm-tags`, deployment tags, and `tag_regex` over request headers.
- Portkey, Metadata, https://portkey.ai/docs/product/observability/metadata, read. `x-portkey-metadata` with the `_user` key, string values of at most 128 characters, and `HEADERS_TO_METADATA` on self-hosted Enterprise gateways.

## Review history

No review has run.

### Revision notes

- Revision 1, dated 2026-11-09, develops option D1 of the owner's analysis of LLM credential pools, metering, and cost budgets, with the owner's settlements of that date as D1 to D5 and the adversarial cache review's metering corrections as D6, and records OD-5 to OD-11 with the recommendations the owner's decisions state. It adds OD-12 for the endpoints of tenant upstreams. Its tree and specification claims were read again on 2026-11-09, which corrected the analysis on these points: a non-streaming call that exhausts a budget returns `BUDGET_EXHAUSTED` only when its §8.6 extension is not granted, because proposal 0023 placed the trigger at settlement; the OpenAI Responses translator exists and is unregistered; Bedrock streams go to the non-streaming endpoint; and `message_delta` carries no cache-write split by lifetime. It takes RC-B and O2 from [`llm-response-cache-removal-design.md`](llm-response-cache-removal-design.md). Before delivery it applied two later settlements of the same day. D7 (C1, the owner's "ok") states that Lenny never strips or rewrites `cache_control`, that §4.9 states isolation by the upstream's provider account or workspace, that posture lists the tenants sharing each upstream, and that a grant making a `hipaa` or `fedramp` tenant share an upstream warns. D8 replaces per-user upstreams, which a coordinator instruction of that day had asked for and the owner then declined, with forwarded attribution: the `userCredential` credential source and its missing-credential refusal leave "LU-1", OD-10's alternative no longer routes user keys to upstreams, "LU-9" places isolation within a tenant with a downstream LLM gateway, "LU-11", LUR-13, LUR-14, OD-13, and OD-14 are added, the LLM proxy's specification moves stay in §4.9 in place of a new §4.10, and the tests, limits, alternatives, reviewer instructions, and sources follow.
- Revision 2, dated 2026-11-09, lists F-11.2.34 among the findings the design closes: "Records" under "Current state" states that LU-C closes it and records its per-replica overspend, "LU-6" states how the Redis session scope closes it, and the LU-C row and the `BUILD-GAPS.md` row of "Non-specification changes" name it.
