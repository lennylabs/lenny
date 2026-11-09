# LLM response cache removal

Status: design for owner review, revision 2, 2026-11-09, with no independent review yet. Nothing here is built or staged. It reads against [`connector-authorization-and-external-memory-design.md`](connector-authorization-and-external-memory-design.md) revision 12, [`billing-resource-attribution-design.md`](billing-resource-attribution-design.md) revision 11, and [`security-oversight-design.md`](security-oversight-design.md) revision 47. It stages no specification edit. The changes go through `change-proposal` after review. Proposals carry the label RC-n, and requirements carry the label RCR-n. "Review history" at the end records the revisions.

Each factual claim about behaviour carries one of these tags where the source matters:

| Tag | Meaning |
| :-- | :-- |
| **[source]** | Read in the Lenny tree or the specification on 2026-11-09. |
| **[run]** | Observed on 2026-11-09 by running code from the tree, unmodified, from a scratch Go module outside the repository. |
| **[external]** | Read on an external site on 2026-11-09. "Sources" lists the pages. |
| **[design]** | What this design requires. Nothing implements it. |

## Problem

"Terms", after "Scope", defines the vocabulary this document uses.

The owner asked, in the owner's words: "Remove caching LLM response currently provided by Lenny."

The premise holds. Lenny specifies and ships its own cache of LLM responses, the semantic cache of [§4.9, "Semantic Caching"](../spec/04_system-components.md#semantic-caching). A credential pool opts in with a `cachePolicy`, and the gateway's LLM proxy then answers a non-streaming request from a stored response when an earlier request from the same scope is similar enough **[source]**. The feature reaches into the storage roles, the Redis key layout, user erasure, tenant deletion, data classification, data residency, the compliance profile rules, the error catalogue, the admin API, two database columns, a gateway flag, the build sequence, the competitive comparison, and the documentation ("Current state").

The cache as built has defects beyond the cost of carrying it:

- It matches on the whole request body by lexical overlap, so in a multi-turn conversation two requests whose final instruction differs share almost every token. A request that asked to delete a code path received the cached response to a request that asked to add a test for it **[run]**.
- With `cacheScope: tenant`, the same false match hands one user's response, with its content, to another user in the tenant. §4.9 names only a timing side-channel for that scope **[source]**.
- A cache hit records no token usage, so a hit is invisible to quotas, budgets, and billing **[source]**.
- The per-pool `ttl`, `similarityThreshold`, and `backend` are validated and stored, and no code reads them **[source]**.

Provider-side prompt caching is a separate mechanism. Anthropic and OpenAI cache the computed prefix of a prompt inside their own service, and the model still generates a new response for every request **[external]**. Lenny forwards the Anthropic `cache_control` fields unchanged and does not meter the cached token counts the provider reports **[source]**. The owner's request does not cover provider-side caching. This design keeps the passthrough unless the owner decides otherwise (O1), and it records that Lenny undercounts input tokens whenever a runtime uses provider caching (O2, F-11.2.38).

## Scope

**Removed:**

- Lenny's semantic cache: the §4.9 "Semantic Caching" subsection, the `SemanticCache` storage role, `cachePolicy` and `cacheScope` on credential pools, the `t:{tenant_id}:scache:` Redis key family, `COMPLIANCE_CROSS_USER_CACHE_PROHIBITED`, the cache's erasure steps, and every reference in the specification ("RC-1").
- The packages `semanticcache`, `semanticcache/redisstore`, and `proxycache`, the proxy's `ProxyCache` seam, the `--llm-semantic-cache` flag, the pool fields, their validation, and the admin and bootstrap payload fields ("RC-2").
- The `cache_scope` and `cache_policy` columns, through a new migration ("RC-3").
- The tests, documentation, chart comments, and open records that cover the cache ("RC-2" and "RC-4").

**Changed:**

- §12.8's user erasure sequence and tenant deletion order lose their cache steps, and the user erasure steps after the removed one are renumbered ("RC-1").
- §11.7's compliance downgrade ratchet loses the item that re-enabled `cacheScope: tenant`. The ratchet stays.
- Phase 17b of the build sequence drops semantic caching from its title and deliverables.

**Added:**

- A statement in §4.9 that the LLM proxy forwards provider prompt-caching fields unchanged, with the isolation each provider applies, if the owner keeps the passthrough (O1, "RC-5").
- Cached input tokens in the authoritative usage count, if the owner accepts O2 ("RC-5").

**Out of scope:**

- Response caching for any other surface, such as idempotent replay under [§11.5](../spec/11_policy-and-controls.md#115-idempotency), the routing cache, or the token cache. These caches hold no LLM responses.
- The OpenAI dialect endpoint of the proxy. §4.9 describes `POST {proxyUrl}/v1/chat/completions` for `proxyDialect: openai`, and the gateway mounts only `POST /llm-proxy/v1/messages` **[source]**. "Current state" records this for filing.
- Token metering beyond cached tokens, which [`billing-resource-attribution-design.md`](billing-resource-attribution-design.md) also leaves to §11.2.

## Terms

| Term | Meaning |
| :-- | :-- |
| Semantic cache | Lenny's own LLM response cache under §4.9 "Semantic Caching". It stores a request and the provider's response, and it answers a later request from the stored response when the requests are similar. This design removes it. The owner's request calls it "caching LLM response". |
| Provider prompt caching | A cache inside the LLM provider's service that stores the computed state of a prompt prefix. A later request with an identical prefix is processed faster and billed at a reduced input rate, and the model still generates a new response. Anthropic requests it with `cache_control`, and OpenAI applies it automatically **[external]**. |
| `cache_control` | The Anthropic Messages request field that requests provider prompt caching, placed on a content block or at the top level of the request, with `type: ephemeral` and an optional `ttl` of `5m` or `1h` **[external]**. |
| Cached-token counts | The usage fields a provider returns for prompt caching. In the Anthropic Messages format they are `cache_read_input_tokens` and `cache_creation_input_tokens`, and `input_tokens` counts only the tokens after the last cache breakpoint **[external]**. |
| LLM proxy | The gateway subsystem of §4.9 that receives a proxy-mode pod's LLM request, runs the `PreLLMRequest` and `PostLLMResponse` interceptor phases of §4.8, translates the request to the upstream provider's format, injects the provider credential that the pod never sees, and records the provider-reported token usage. |
| Proxy mode | A credential pool with `deliveryMode: proxy`. The pod holds a lease token and calls the LLM proxy. Under `deliveryMode: direct`, the pod calls the provider itself and Lenny sees no LLM traffic. |
| Credential pool | An admin-managed set of LLM provider credentials, keyed by tenant and name in `credentialpoolstore` **[source]**. The semantic cache was configured per pool. |
| `cacheScope` | The credential pool field that set the semantic cache's identity granularity: `per-user` (the default), `per-session`, or `tenant`. |
| Regulated tenant | A tenant whose `complianceProfile` is `soc2`, `fedramp`, or `hipaa`, as [`security-oversight-design.md`](security-oversight-design.md) defines it. The cache rule that this design removes refused `cacheScope: tenant` for `hipaa` and `fedramp`. |
| Erasure step | One store's deletion in §12.8's `DeleteByUser` dependency-ordered deletion sequence, or one store in the tenant deletion order of Phase 4. Code cites a user erasure step by number, as `§12.8 step 6`. |
| Authoritative usage | The token counts the LLM proxy extracts from the provider's response. §11.2 makes them the quota and budget record for proxy-mode sessions and ignores the pod's own reports. |

### Terms from other designs and the platform

| Term | Meaning |
| :-- | :-- |
| BUILD-GAPS and TEST-GAPS | `BUILD-GAPS.md` and `TEST-GAPS.md` at the repository root. The first is the audit of gaps between the specification and the code, with findings named F-n.n.n. The second is the audit of gaps between the specification and the tests, with findings named T-n.n.n. |
| `memorystore.Embedder` | The interface that turns text into a vector for the memory store of §9.4. `HashingEmbedder` is its built-in implementation, which hashes word tokens into a fixed-width vector and therefore measures shared vocabulary **[source]**. The semantic cache reuses it. |

### Labels from other designs

| Label | Meaning |
| :-- | :-- |
| [CA-10](connector-authorization-and-external-memory-design.md#ca-10-removing-built-in-memory) | Removal of the built-in memory store of §9.4, in the connector authorization design. It owns `memorystore`, which defines the embedder the semantic cache imports, and it edits Phase 17b and §12.8 as this design does. |
| CA-G | The proposal of the connector authorization design that lands CA-10 ([proposal split](connector-authorization-and-external-memory-design.md#proposal-split-and-order)). |

## High-level design

Lenny stops caching LLM responses. Every request a proxy-mode pod sends goes to the provider, the provider's response goes back through the `PostLLMResponse` phase, and the proxy records the provider's token usage for every request. The specification loses the semantic cache, its storage role, its key family, its erasure steps, its compliance rule, and its error code. The code loses the cache packages, the proxy seam, the flag, the pool fields, and the columns. Nothing replaces the cache inside Lenny, because no specified guarantee depended on it: it was disabled by default, no SLO counted on it, and no metric or alert measured it.

Provider prompt caching stays as it is today unless the owner decides otherwise. The proxy forwards the pod's request body unchanged to Anthropic-format providers, so a runtime that sets `cache_control` gets the provider's cache, its lower latency, and its lower price. That cache stores no response, and the provider isolates it by organization or workspace. The design recommends keeping the passthrough and stating it in §4.9 (O1). It also recommends correcting the usage count, which today ignores the cached-token counts and so lets a runtime move most of its input tokens outside Lenny's quotas (O2).

The parts:

- **Specification removal (RC-1).** Every section that names the cache drops it, and §12.8's numbered user erasure steps are renumbered.
- **Code removal (RC-2).** The cache packages, the proxy seam, the flag, the pool fields, the error code, the erasure wiring, and their tests are deleted.
- **Data and configuration (RC-3).** A new migration drops the two pool columns, and the chart and documentation lose their cache references.
- **Records (RC-4).** Open audit findings that exist only for the cache are resolved as obsolete, and findings that name it in passing are corrected.
- **Provider prompt caching (RC-5).** §4.9 states the passthrough and its isolation, and the authoritative usage counts cached tokens, per O1 and O2.

## Decisions

Settled by the owner on 2026-11-09:

| ID | Decision |
| :-- | :-- |
| D1 | Lenny's own LLM response cache is removed. In the owner's words: "Remove caching LLM response currently provided by Lenny." |

Open, for the owner:

| ID | Question | Options | Recommendation |
| :-- | :-- | :-- | :-- |
| O1 | Does the LLM proxy keep forwarding provider prompt-caching controls such as `cache_control`? | (a) Keep the passthrough with no Lenny setting, and state it in §4.9 with each provider's isolation. (b) Strip `cache_control` in the translators. (c) Add a pool setting that allows or strips it. | (a). The provider cache stores no response, so the false-match and response-sharing defects of the semantic cache do not arise. It is isolated per provider organization or workspace **[external]**. Stripping it raises cost and latency for every runtime that uses it, and it would be incomplete, because OpenAI and Azure OpenAI cache automatically with no request field **[external]**. A deployer who must keep tenants apart at the provider gives each tenant's pools credentials from a separate provider organization or workspace, and §4.9 states this. |
| O2 | How do the provider's cached-token counts enter Lenny's authoritative usage? | (a) As today: count `input_tokens` alone, which excludes cache reads and writes on the Anthropic format. (b) Count the sum of `input_tokens`, `cache_read_input_tokens`, and `cache_creation_input_tokens` as input tokens for quotas and budgets, and carry the read and write counts as separate fields on billing events. (c) Weight cached tokens by the provider's price multipliers. | (b). Option (a) lets a runtime place a large prefix under `cache_control` and have Lenny count only the uncached tail, which defeats §11.2's statement that a runtime cannot influence quota accounting. Option (c) depends on per-model prices that change and differ across providers **[external]**. Separate fields let a deployer price cache reads and writes in its own billing. |

## Requirements

- RCR-1. No Lenny component stores an LLM response to answer a later request. Every proxied request reaches the upstream provider.
- RCR-2. After RC-A, no specification section, pool field, admin or bootstrap payload field, flag, Helm value or comment, Redis key family, table column, storage role, error code, erasure step, metric, or test refers to the semantic cache.
- RCR-3. The LLM proxy records the provider's usage for every request it serves.
- RCR-4. User erasure and tenant deletion keep their order and their other steps after the cache steps go.
- RCR-5. Under O1 option (a), the proxy forwards `cache_control` and every other provider field of an Anthropic Messages request unchanged to `anthropic_direct`, `aws_bedrock`, and `vertex_ai`.
- RCR-6. Under O2 option (b), the authoritative input count of a proxied Anthropic-format request equals `input_tokens + cache_read_input_tokens + cache_creation_input_tokens`, on the non-streaming and the streaming paths.

## Current state

### Specification

Each place below names the cache **[source]**:

| Section | What it says |
| :-- | :-- |
| [§4.9 Credential Leasing Service](../spec/04_system-components.md#49-credential-leasing-service), opening | Lists "Semantic Caching" among the subsections the section covers. |
| [§4.9, "Semantic Caching"](../spec/04_system-components.md#semantic-caching) | The optional `CachePolicy` on `CredentialPool` with `strategy: semantic`, `ttl: 300`, `similarityThreshold: 0.92`, and `backend: redis`, backed by the pluggable `SemanticCache` interface. A default Redis implementation, disabled by default and opt-in per pool. The `ValidateSemanticCacheErasure` contract helper. **Tenant and user isolation:** the key space, the `cacheScope` values, the default `per-user`, the refusal of `cacheScope: tenant` on a regulated pool with `COMPLIANCE_CROSS_USER_CACHE_PROHIBITED`, the timing side-channel trade-off, and the test `TestSemanticCacheTenantIsolation`. |
| [§11.7 Audit Logging](../spec/11_policy-and-controls.md#117-audit-logging), **Compliance profile downgrade ratchet.** | Item 1, "Runtime controls are lost", lists re-enabling `cacheScope: tenant` among the controls a downgrade would remove. |
| [§12.1 Design Principle](../spec/12_storage-architecture.md#121-design-principle) | Names `SemanticCache` with `MemoryStore` as a pluggable role that must expose the erasure primitives. |
| [§12.2 Storage Roles](../spec/12_storage-architecture.md#122-storage-roles) | The `SemanticCache` row, and `SemanticCache` in the list of Redis-backed roles that must use the `t:{tenant_id}:` prefix. |
| [§12.4 Redis HA and Failure Modes](../spec/12_storage-architecture.md#124-redis-ha-and-failure-modes) | The `t:{tenant_id}:scache:{scope}:{hash}` key row, and item (d) of `TestRedisTenantKeyIsolation`, a semantic cache write that must not hit for another tenant. |
| [§12.6 Interface Design](../spec/12_storage-architecture.md#126-interface-design) | The comment on `RedisConcernCachePubSub` lists the semantic cache. |
| [§12.8 Compliance Interfaces](../spec/12_storage-architecture.md#128-compliance-interfaces) | The `SemanticCache` row of the erasure scope table, step 2 of the `DeleteByUser` dependency-ordered deletion sequence, `SemanticCache` in the Phase 4 order of the tenant deletion lifecycle, and semantic cache entries in the list of Redis-stored personal data under data residency. |
| [§12.9 Data Classification](../spec/12_storage-architecture.md#129-data-classification) | The row "Semantic cache entries", T3 Confidential. |
| [§15.1 REST API](../spec/15_external-api-surface.md#151-rest-api) | The error code `COMPLIANCE_CROSS_USER_CACHE_PROHIBITED`. |
| [§18.1 Phasing Intent](../spec/18_build-sequence.md#181-phasing-intent) | Item 6 lists "semantic caching" among the post-v1 surfaces. |
| [§18.2 Phase Map](../spec/18_build-sequence.md#182-phase-map) | Row 17b, "Memory, semantic caching, eval hooks". |
| [§18.39 Phase 17b](../spec/18_build-sequence.md#1839-phase-17b--memory-semantic-caching-eval-hooks) | The `SemanticCacheStore` deliverable "per §21", the custom-backend preflight for `SemanticCacheStore`, and the Tier 2 `semantic_cache_test` exit criterion. |
| [§23 Competitive Landscape](../spec/23_competitive-landscape.md#23-competitive-landscape), **Feature Comparison Matrix** | The "Semantic cache" row. |
| [§23.1 Why Lenny?](../spec/23_competitive-landscape.md#231-why-lenny), **Additional Platform Capabilities** | Item 9 ends with "Optional `SemanticCache` interface on credential pools for LLM response caching." |
| `spec/README.md` | The table-of-contents entry for §18.39. |

The specification contradicts itself on the cache's status. §4.9 and §12 specify it as a v1 feature, §18.1 calls it a post-v1 surface, and §18.39 cites §21 for the `SemanticCacheStore` interface while §21 says nothing about caching **[source]**. §4.9 refuses `cacheScope: tenant` on "a pool with a regulated `complianceProfile`", while `complianceProfile` is a tenant field, and the code checks the tenant **[source]**.

No section of §16 defines a metric or alert for the cache, and no SLO refers to it **[source]**.

### Tree

All of the following was read on 2026-11-09 **[source]**:

| Area | What exists |
| :-- | :-- |
| `pkg/gateway/llmproxy/semanticcache` | `Store` with `Get`, `Put`, `DeleteByUser`, and `DeleteByTenant`, aliased as `SemanticCache`. `Key` with tenant, scope, user, session, model, and provider. `CacheScope` with `per-user`, `per-session`, and `tenant`. `InMemory`, the store the gateway wires. `Similarity`, which uses `memorystore.CosineDistance`. `ValidateSemanticCacheErasure` in `contract.go`. Defaults `DefaultSimilarityThreshold` 0.92 and `DefaultTTLSeconds` 300. |
| `pkg/gateway/llmproxy/semanticcache/redisstore` | The Redis store under `t:{tenant_id}:scache:`. No production code imports it. Only tests do. |
| `pkg/gateway/llmproxy/proxycache` | `Adapter`, which implements the proxy's `ProxyCache` seam. It reads the pool's `CachePolicy.Enabled` and `Strategy` and its `CacheScope`, resolves the session's user for `per-user`, takes the model from the request body, and uses the whole request body as the query text. |
| `pkg/gateway/llmproxy/llmproxy/handler.go` | The `ProxyCache` interface and `Handler.Cache`. After the `PreLLMRequest` phase, a non-streaming request is looked up. A hit runs `PostLLMResponse` over the stored body and returns it, skipping translation, credential resolution, the upstream call, and usage recording. A miss stores the translated response before `PostLLMResponse`. |
| `cmd/lenny-gateway` | `--llm-semantic-cache` and `LENNY_LLM_SEMANTIC_CACHE` in `flags.go`, off by default. When set, `adminrouter.go` builds one `semanticcache.InMemory` per replica with the default embedder, TTL, and threshold, and `llmproxy.go` wraps it in `proxycache.New` for the proxy. `llmproxy_server.go` passes it to the handler. The same instance feeds the user erasers (`userCacheErasers`) and the tenant erasers (`cacheTenantErasers` in `tenant_erasers.go`). `sessionUserLookup` in `runserver.go` serves the per-user scope. `erasure_contract.go` asserts the store satisfies the erasure contract. |
| `pkg/gateway/storage/erasure` | The rank `"semantic_cache": 20` in `order.go`. |
| `pkg/gateway/credentials/credentialpoolstore` | `CredentialPool.CacheScope`, `CredentialPool.CachePolicy`, the `CachePolicy` struct with `Enabled`, `Strategy`, `TTLSeconds`, `SimilarityThreshold`, and `Backend`, `validateCachePolicy`, and `validCacheScope`. `pgstore` reads and writes the `cache_scope` and `cache_policy` columns. |
| `pkg/gateway/externalapi/admin` | `cacheScope` and `cachePolicy` on `CredentialPoolPayload`, `CachePolicyPayload`, `validateCacheScope` with the `COMPLIANCE_CROSS_USER_CACHE_PROHIBITED` refusal, `toCachePolicy` and `fromCachePolicy`, and in `bootstrap.go` `bootstrapCacheScope` and the pool upsert's cache fields. |
| `pkg/gateway/externalapi/errorclassify` | The `COMPLIANCE_CROSS_USER_CACHE_PROHIBITED` entry. |
| `migrations` | `0070_credential_pool_cache_scope` adds `cache_scope`, and `0078_credential_pool_cache_policy` adds `cache_policy`. |
| `charts/lenny` | `values.yaml` lists the semantic cache in the comment on `cachePubSubUrl` and shows `cacheScope: per-user` in the commented bootstrap example, which `tests/bootstrap_test.yaml` renders and asserts. No chart value sets `LENNY_LLM_SEMANTIC_CACHE`, so a chart install runs with the cache off. |
| Documentation | `README.md` and `docs/index.md` ("Credential routing and semantic caching ... and cache LLM responses"), `docs/about/comparisons.md`, `docs/about/status.md`, the "Semantic Cache" node of the storage diagram in `docs/getting-started/architecture.md`, `docs/reference/error-catalog.md`, `docs/testing/forward-compatibility.md`, and `pkg/gateway/ARCHITECTURE.md`. `TESTING.md` names the cache in §12.2.1, §13.36, and §23.2. |
| Tests | `semanticcache_test.go`, `redisstore_test.go`, `proxycache_test.go`, `llmproxy/cache_test.go`, `storage/erasure/semanticcache_wiring_test.go`, `cmd/lenny-gateway/eraser_postures_test.go` and `release_wiring_test.go`, the cache cases in `credentialpoolstore_test.go`, `credential_pools_test.go`, and `bootstrap_test.go`, `tests/tier2_component/stores/semanticcache_test.go`, the cache lines of `scaffolds_test.go` and `erasure_interface_test.go`, the semantic cache subtest of `tests/tier4_integration/redis_tenant_isolation_test.go`, the 0070 and 0078 rows of `tests/tier2_component/migrations/prod_columns_test.go`, and the entries in `tests/spec-map.json`. `rediskeys_test.go` and `redis_cluster_smoke_test.go` use `scache` only as a sample key name. |

`scripts/refactor/manifest` maps the old `pkg/gateway/semanticcache` path to its current one. It is the record of an executed move and stays as written.

### The cache's behaviour

- **Lexical match on the whole body.** `proxycache` passes the full request body, with model, system prompt, and every message, as the query. `HashingEmbedder` measures shared word tokens. Two Anthropic Messages bodies with the same system prompt and history, whose final user turns were "Now add a unit test for the 503 path." and "Now delete the 503 path entirely.", scored 0.986 with a two-message history and 0.9995 with a twenty-two-message history, both above the 0.92 threshold, and `Get` returned the first request's stored response for the second **[run]**. A runtime that resends its conversation on each turn therefore receives a stale answer to a different instruction whenever the call is non-streaming.
- **Per-replica store.** The wired store is in process. Each gateway replica caches separately, and erasure reaches only the replica that runs it, which BUILD-GAPS F-12.8.53 records **[source]**.
- **Ignored settings.** The proxy reads `enabled` and `strategy` from a pool's `cachePolicy`. The store uses the process-wide defaults for TTL and threshold, and the `backend` value selects nothing **[source]**.
- **Unmetered hits.** A hit returns before the usage recorder, so it adds nothing to the session's tokens, the tenant's quota, the token budget, or billing events **[source]**.
- **Compliance check at pool write only.** The admin API refuses `cacheScope: tenant` when the tenant is `hipaa` or `fedramp` at the time of the pool write. No check runs when the tenant's profile is later raised, so a pool written under `none` keeps cross-user sharing after the tenant becomes `hipaa` **[source]**.
- **Stored content before policy.** A miss stores the response as translated, before `PostLLMResponse` runs, so the store holds content that an interceptor would redact. The hit path runs `PostLLMResponse` again before returning **[source]**.

### Provider prompt caching today

- The Anthropic translator forwards the request body unchanged, and the Bedrock and Vertex translators re-encode it with every field kept except `model`, so `cache_control` reaches all three Anthropic-format providers **[source]**. Bedrock accepts `cache_control` on content blocks of an InvokeModel body, and at the top level for the models its guide lists **[external]**.
- The proxy mounts only `POST /llm-proxy/v1/messages` and always translates from the Anthropic dialect, so the OpenAI and Azure translators are unreachable from the proxy today **[source]** (F-4.9.48). F-4.9.10 recorded the missing route among its evidence and was closed by provider dispatch, with the dialect route placed out of scope, so F-4.9.48 records the route and F-4.9.10 stays closed. OpenAI and Azure OpenAI cache automatically, with no request field needed **[external]**.
- `extractAnthropicUsage` and the streaming `applySSEUsage` read `input_tokens` and `output_tokens` alone **[source]**. Anthropic's `input_tokens` counts only the tokens after the last cache breakpoint, and the total input is `cache_read_input_tokens + cache_creation_input_tokens + input_tokens` **[external]**. Lenny's authoritative input count therefore omits every cached token. §11.2, **Quota Update Timing**, says the proxy extracts `input_tokens` and `output_tokens` and that a runtime cannot influence quota accounting by underreporting **[source]**. F-11.2.38 records the undercount.
- Neither the specification nor the tree mentions `cache_control`, `cache_read_input_tokens`, or `cache_creation_input_tokens`. The Open Responses translator writes `cached_tokens: 0` because it does not track them **[source]**.
- Revision 1 found no BUILD-GAPS finding for the cached-token undercount **[source]**. Revision 2 files it as F-11.2.38.

### Records

| Record | State | Relation |
| :-- | :-- | :-- |
| F-11.2.38 | Open | The cached-token undercount of "Provider prompt caching today", filed by revision 2. RC-B closes it. |
| F-4.9.48 | Open | The proxy's missing `openai` dialect route, filed by revision 2. It needs its own proposal, and RC-B's OpenAI metering depends on it. |
| F-12.8.53 | Open | Per-replica semantic cache and billing buffer erased only on one replica. The cache half goes with the cache, and the billing buffer half stays. |
| F-4.9.13, F-4.9.14, F-12.2.16, F-12.8.15 | Closed | Built or wired the cache. They stay as history. |
| T-4.9.15 | Open | `TestSemanticCacheTenantIsolation` for a similar query. |
| T-12.1.1 | Open, High | No startup or per-job erasure preflight for `SemanticCache`. |
| T-12.2.5 | Open | The opt-in gate and the miss-on-outage behaviour are untested. |
| T-12.1.5, T-12.1.6, T-12.1.7, T-12.7.2 | Open | Name `SemanticCache` in passing beside `MemoryStore` or other stores. |
| `BUILD-PROGRESS.md` and `BUILD-PLAN.md` | Records | Phase 17b rows mention semantic caching. |

## RC-1 Specification removal

The specification loses every statement in the table under "Specification" **[design]**. §4.9's "Semantic Caching" subsection is deleted, with its entry in the section's opening list. §12.2 loses the `SemanticCache` row and its name in the Redis prefix rule, §12.1 names `MemoryStore` alone as the pluggable example, and §12.4 loses the key row and item (d) of `TestRedisTenantKeyIsolation`, whose later item takes its letter.

**Erasure.** §12.8's erasure scope table loses the `SemanticCache` row, and the tenant deletion order of Phase 4 runs `LeaseStore` and then the Redis caches. The `DeleteByUser` sequence loses step 2, and steps 3 onward move up by one. The renumbering changes the target of every `§12.8 step N` citation for N of 3 and above in the specification, the code, and the tests, so the proposal sweeps them in the same change. The alternative of keeping an empty step 2 leaves a placeholder in a normative list, and "Alternatives rejected" records it. CA-G removes the `MemoryStore` step from the same sequence, and the proposal that lands second rebases its renumbering on the first ("Proposal split and order").

**Compliance.** §11.7 item 1 lists the controls that remain: the SIEM requirement, `audit.grantCheckInterval`, and `audit.pgaudit.enabled`. §15.1 loses `COMPLIANCE_CROSS_USER_CACHE_PROHIBITED`.

**Build sequence and comparison.** §18.1 item 6 and §18.2 row 17b drop "semantic caching". §18.39 drops the `SemanticCacheStore` deliverable, its preflight, and `semantic_cache_test`, which also removes the dangling citation of §21. The title of §18.39 changes, and `spec/README.md` follows it. §23 loses the matrix row, and §23.1 item 9 loses its last sentence.

## RC-2 Code removal

The following are deleted **[design]**:

- `pkg/gateway/llmproxy/semanticcache`, its `redisstore`, and `pkg/gateway/llmproxy/proxycache`, with their tests.
- `ProxyCache`, `Handler.Cache`, the lookup before translation, and the store after translation in `handler.go`, with `cache_test.go`. The handler then has one path for a non-streaming request: translate, forward, translate back, run `PostLLMResponse`, and record usage.
- The `cache` parameter of `newLLMProxyServer`, the `--llm-semantic-cache` flag and its environment variable, `erasureSemanticCache` and its threading through `buildAdminRouter`, `buildUserErasers`, `buildTenantErasers`, `userCacheErasers`, `tenantErasersBeforeSessions`, and `cacheTenantErasers`, `sessionUserLookup`, and the assertion in `erasure_contract.go`.
- The `"semantic_cache"` rank in `erasure/order.go`, `semanticcache_wiring_test.go`, and the cache cases in `eraser_postures_test.go` and `release_wiring_test.go`.
- `CacheScope`, `CachePolicy`, `validateCachePolicy`, and `validCacheScope` in `credentialpoolstore`, and the two columns in `pgstore`'s reads and writes.
- `cacheScope`, `cachePolicy`, `CachePolicyPayload`, `validCacheScopes`, `crossUserCacheRegulatedProfiles`, `validateCacheScope`, `toCachePolicy`, `fromCachePolicy`, `bootstrapCacheScope`, and the cache comparison in the bootstrap upsert, in `externalapi/admin`.
- The `COMPLIANCE_CROSS_USER_CACHE_PROHIBITED` entry in `errorclassify`.

`semanticcache` is the only importer of `memorystore` outside the memory feature, for `Embedder`, `HashingEmbedder`, and `CosineDistance` **[source]**. Its removal leaves those symbols with the memory store, so CA-10's step that would move them into `semanticcache` no longer applies.

The admin API decodes pool payloads without refusing unknown fields **[source]**, so a request that still sends `cachePolicy` or `cacheScope` is accepted and the fields are ignored. That follows the API's general handling and is not changed here.

## RC-3 Data and configuration

- A new migration drops `credential_pools.cache_scope` and `credential_pools.cache_policy`. Migrations 0070 and 0078 stay as history, as the precedent of migration 0180 does for `slot_id` **[source]**. Nothing migrates, because there are no deployments. `prod_columns_test.go` records the drop as it does for 0180 **[design]**.
- The chart drops "semantic cache" from the `cachePubSubUrl` comment and `cacheScope` from the bootstrap example, and `tests/bootstrap_test.yaml` stops asserting it **[design]**.
- `README.md`, `docs/index.md`, `docs/about/comparisons.md`, `docs/about/status.md`, `docs/getting-started/architecture.md`, `docs/reference/error-catalog.md`, `docs/testing/forward-compatibility.md`, `pkg/gateway/ARCHITECTURE.md`, and `TESTING.md` §12.2.1, §13.36, and §23.2 drop the cache **[design]**. The README and index sentence keeps credential routing.

## RC-4 Records

- F-12.8.53 is narrowed to the billing buffer, with a note that the cache half went with RC-A **[design]**.
- T-4.9.15, T-12.1.1, and T-12.2.5 are resolved as obsolete with a pointer to RC-A, and T-12.1.5, T-12.1.6, T-12.1.7, and T-12.7.2 drop the cache from their text **[design]**.
- The closed findings and `scripts/refactor/manifest` stay as written.
- F-11.2.38 records the cached-token undercount of "Provider prompt caching today", owned by RC-B **[design]**.

## RC-5 Provider prompt caching

**Passthrough (O1).** Under the recommended option (a), §4.9's "Native translator" states that the proxy forwards an Anthropic Messages request body, including `cache_control`, unchanged apart from the provider rehosting the translator already performs, and that a provider's prompt cache is the provider's own mechanism **[design]**. It states the isolation each provider documents **[external]**:

- Anthropic's API isolates caches by workspace within an organization, and Bedrock and the Google-managed Claude models on Google Cloud isolate them by organization.
- OpenAI does not share caches across organizations, and Azure OpenAI does not share them across subscriptions.

It states that pools of different tenants holding credentials of one provider organization or workspace share that provider's cache, so a request can observe from latency that another tenant sent an identical prefix, and that a deployer who must prevent this gives each tenant credentials from a separate organization or workspace. The provider returns no stored response, so no response content crosses tenants **[external]**. Provider cache retention is the provider's: five minutes or one hour on Anthropic's API, and up to 24 hours under OpenAI's extended retention **[external]**, which a deployer with a regulated tenant checks against its provider agreement.

**Metering (O2).** Under the recommended option (b) **[design]**:

- `extractAnthropicUsage` and `applySSEUsage` read `cache_read_input_tokens` and `cache_creation_input_tokens` beside `input_tokens`. On the streaming path, `applySSEUsage` reads them from whichever usage-bearing event carries them, which "Validation owed" confirms.
- `Usage` gains `CacheReadInputTokens` and `CacheCreationInputTokens`, and its input total for quotas, the token budget, and `tokens_input` is the sum of the three counts.
- §11.2, **Quota Update Timing**, names the sum. §11.2.1 adds `tokens_cache_read` and `tokens_cache_write` to `token_usage.checkpoint` and the session usage events, each a part of `tokens_input`.
- For the OpenAI formats, the proposal confirms whether `prompt_tokens` and `input_tokens` already include `cached_tokens` and adds them only where they do not ("Validation owed").

## What replaces the cache's guarantees

| Guarantee or effect | After removal |
| :-- | :-- |
| Lower cost and latency for a repeated non-streaming request | No replacement in Lenny. Provider prompt caching lowers the cost and latency of a repeated prefix for runtimes that use it (O1). A runtime may cache inside its own process for its own session. |
| Tenant, user, and session isolation of cached responses | No stored responses exist, so the rule has no subject. |
| Erasure of cached pairs on user erasure and tenant deletion | The gateway holds no LLM response, so the erasure steps have nothing to delete. F-12.8.53's cache half closes. |
| Refusal of cross-user sharing for `hipaa` and `fedramp` tenants | No cross-user response sharing exists. The §11.7 ratchet keeps its other controls. |
| No usage on a hit | Every request is metered from the provider's response (RCR-3). |
| SLOs, metrics, and alerts | None existed, so none change. |

## Security effects

- LLM requests and responses are no longer stored in gateway memory or, under the unwired Redis store, in Redis. §12.9 loses a T3 class, and §12.8's residency list loses an entry **[design]**.
- The false-match defect goes, so no session receives a response written for a different request, and no user in a `cacheScope: tenant` pool receives another user's response **[design]**.
- The cross-user timing side-channel of `cacheScope: tenant` goes. The provider-side timing signal of identical prefixes remains, and RC-5 states it (O1).
- The gap in which a raised compliance profile left `cacheScope: tenant` in force goes with the field.
- No unredacted pre-`PostLLMResponse` content is held between requests.
- Under the current usage count, provider caching lets a runtime keep most of its input outside quotas and budgets. Removing the semantic cache does not change that, and O2 does **[source]**.

## Billing effects

- A request that a hit would have answered now reaches the provider and is billed by the provider and metered by Lenny. With the cache off by default, and absent from the chart, an installation that never set `LENNY_LLM_SEMANTIC_CACHE` sees no change **[source]**.
- `tokens_input` stays an uncached-tail count until RC-B lands. Under O2 option (b) it becomes the full input count, and the cached parts are visible as separate fields for a deployer that prices them at the provider's reduced rates.

## Limits

- Lenny offers no response caching. A deployer that wants it runs it outside Lenny, and this design specifies no integration for it.
- Under O1 option (a), Lenny does not control provider cache retention or cross-tenant prefix timing. It documents them.
- Under O2 option (b), quotas count a cache read as a full input token, though providers bill it at a fraction of the base rate **[external]**. A deployer that wants price-weighted quotas reads the separate fields.

## Alternatives rejected

- **Fix the cache instead of removing it.** Exact matching on a normalized body would end the false matches, but the owner asked for removal (D1), and exact repeats are what provider prompt caching already serves at the prefix level.
- **Keep an empty step 2 in §12.8.** It would avoid renumbering, and it leaves a placeholder step in a normative sequence. The citation sweep is mechanical.
- **Disable the cache and keep the code.** The specification would still describe a feature, and the erasure, isolation, and compliance rules would remain obligations with no behaviour behind them.
- **Strip provider caching fields with the semantic cache.** Recorded under O1. It raises cost for runtimes, and it cannot turn off OpenAI's automatic caching.

## Specification changes

The following edits locate their targets by heading:

| Section | Change |
| :-- | :-- |
| [§4.9](../spec/04_system-components.md#49-credential-leasing-service), opening | Remove "Semantic Caching" from the list of subsections. |
| [§4.9, "Semantic Caching"](../spec/04_system-components.md#semantic-caching) | Delete the subsection. |
| [§4.9, "LLM Reverse Proxy"](../spec/04_system-components.md#llm-reverse-proxy), **Native translator.** | Under O1 (a), state the passthrough of `cache_control` and the provider isolation and retention of "RC-5". Under O2 (b), state that usage extraction reads the cached-token counts. |
| [§11.2 Budgets and Quotas](../spec/11_policy-and-controls.md#112-budgets-and-quotas), **Quota Update Timing:** | Under O2 (b), the proxy-mode count is the sum of uncached, cache-read, and cache-write input tokens. |
| [§11.2.1 Billing Event Stream](../spec/11_policy-and-controls.md#1121-billing-event-stream) | Under O2 (b), add `tokens_cache_read` and `tokens_cache_write`. |
| [§11.7 Audit Logging](../spec/11_policy-and-controls.md#117-audit-logging), **Compliance profile downgrade ratchet.** | Remove `cacheScope: tenant` from item 1. |
| [§12.1](../spec/12_storage-architecture.md#121-design-principle) | Remove `SemanticCache` from the pluggable example. |
| [§12.2](../spec/12_storage-architecture.md#122-storage-roles) | Remove the `SemanticCache` row and its name in the Redis prefix rule. |
| [§12.4](../spec/12_storage-architecture.md#124-redis-ha-and-failure-modes) | Remove the `scache` key row and item (d) of `TestRedisTenantKeyIsolation`, and re-letter the item after it. |
| [§12.6](../spec/12_storage-architecture.md#126-interface-design) | Remove the semantic cache from the `RedisConcernCachePubSub` comment. |
| [§12.8](../spec/12_storage-architecture.md#128-compliance-interfaces) | Remove the erasure scope row, `DeleteByUser` step 2 with the renumbering of later steps, `SemanticCache` from the Phase 4 order, and semantic cache entries from the residency list. |
| [§12.9](../spec/12_storage-architecture.md#129-data-classification) | Remove the "Semantic cache entries" row. |
| [§15.1](../spec/15_external-api-surface.md#151-rest-api) | Remove `COMPLIANCE_CROSS_USER_CACHE_PROHIBITED`. |
| [§18.1](../spec/18_build-sequence.md#181-phasing-intent), [§18.2](../spec/18_build-sequence.md#182-phase-map), and [§18.39](../spec/18_build-sequence.md#1839-phase-17b--memory-semantic-caching-eval-hooks) | Remove semantic caching from item 6, row 17b, and the Phase 17b title, deliverables, and exit criteria. Update `spec/README.md`. |
| [§23](../spec/23_competitive-landscape.md#23-competitive-landscape) and [§23.1](../spec/23_competitive-landscape.md#231-why-lenny) | Remove the matrix row and the last sentence of item 9. |

## Non-specification changes

| Area | Change |
| :-- | :-- |
| `pkg/gateway/llmproxy/semanticcache`, `semanticcache/redisstore`, and `proxycache` | Delete. |
| `pkg/gateway/llmproxy/llmproxy` | Remove `ProxyCache` and the cache paths in `handler.go`, and `cache_test.go`. Under O2 (b), extend `Usage`, `extractAnthropicUsage`, and `applySSEUsage`. |
| `cmd/lenny-gateway` | Remove the flag, the in-memory store, its threading through the admin router and erasers, `sessionUserLookup`, the erasure contract assertion, and the cache cases of the wiring tests. |
| `pkg/gateway/storage/erasure` | Remove the `semantic_cache` rank and `semanticcache_wiring_test.go`. |
| `pkg/gateway/credentials/credentialpoolstore` and `pgstore` | Remove the cache fields, their validation, and the column reads and writes. |
| `pkg/gateway/externalapi/admin` and `errorclassify` | Remove the payload fields, validation, bootstrap handling, and error code. |
| `migrations` | A new migration that drops `cache_scope` and `cache_policy`, and the matching `prod_columns_test.go` rows. |
| Every `§12.8 step N` citation for N of 3 and above | Renumber in code comments, `// spec:` annotations, and test names. |
| `tests/tier2_component/stores` and `tests/tier4_integration` | Delete `semanticcache_test.go` and the cache subtest, and remove the cache lines in `scaffolds_test.go` and `erasure_interface_test.go`. Change the sample `scache` key names in `rediskeys_test.go` and `redis_cluster_smoke_test.go` to a key family that remains. |
| `tests/spec-map.json` | Remove the cache test entries. |
| `charts/lenny` | The values comments and `tests/bootstrap_test.yaml`. |
| Documentation and `TESTING.md` | As "RC-3" lists. |
| `BUILD-GAPS.md` and `TEST-GAPS.md` | As "RC-4" lists. |

## Tests

| Tier | Coverage |
| :-- | :-- |
| 1 | Two identical non-streaming requests through the proxy handler each reach the upstream and each record usage (RCR-1, RCR-3). A credential pool written with `cacheScope` or `cachePolicy` in its payload reads back without either field. The user erasure order and the tenant deletion order contain no cache entry and keep their other entries in order (RCR-4). Under O1 (a), a request with block-level and top-level `cache_control` reaches the upstream fake byte for byte for `anthropic_direct`, and with `cache_control` intact after rehosting for `aws_bedrock` and `vertex_ai` (RCR-5). Under O2 (b), a response and a stream whose usage carries cache reads and writes record the sum as input and the parts separately (RCR-6). |
| 2 | The migration drops both columns on a real Postgres, and the pool store round-trips a pool without them. |
| 3 | The admin pool endpoints and the bootstrap upsert return no cache fields, and the error catalogue has no `COMPLIANCE_CROSS_USER_CACHE_PROHIBITED`. |
| 4 | On the compose stack, a proxy-mode session's repeated request is billed twice in `token_usage.checkpoint`, and under O2 (b) a session using `cache_control` against the upstream fake charges its quota with the full input count. |
| Static | A repository check finds no `semanticcache`, `proxycache`, `cachePolicy`, `cacheScope`, `scache`, or `COMPLIANCE_CROSS_USER_CACHE_PROHIBITED` outside migrations 0070 and 0078, the new migration, the records, `proposals/`, and `scripts/refactor/manifest` (RCR-2). |

Every test carries the `// spec:` annotation, and tests at tier 2 and above carry `// diagnosis:`, as `test-coverage.md` requires.

## Validation owed

| Item | Why it matters | Status |
| :-- | :-- | :-- |
| Confirm the usage field names of a Claude InvokeModel response on Bedrock and of a Claude response on Vertex AI | RC-5's metering reads `cache_read_input_tokens` and `cache_creation_input_tokens`. The Bedrock guide shows the Converse names and defers the InvokeModel body to the model reference, and the Vertex page was not read. | Open. |
| Confirm which Anthropic streaming events carry `cache_read_input_tokens` and `cache_creation_input_tokens` | `applySSEUsage` takes `input_tokens` from `message_start` today, and RC-5 extends it. | Open. |
| Confirm whether OpenAI's `prompt_tokens` and `input_tokens`, and Azure's, include `cached_tokens` | RC-5 adds cached tokens only where the total omits them. The guides read on 2026-11-09 did not state it verbatim. | Open. Matters once the OpenAI dialect endpoint exists (F-4.9.48). |
| Count the `§12.8 step N` citations and confirm none cites step 2 for anything other than the cache | The renumbering sweep depends on it. Two code sites cite step 2, both for the cache **[source]**. | Open, for the proposal. |
| Confirm no deployer-facing document outside `docs/` describes `cachePolicy` | RCR-2. | Open. |
| Confirm whether the admin API should refuse unknown pool fields | "RC-2" relies on today's silent acceptance. | Open, outside this design. |

## Instructions for reviewers of other designs

- [`connector-authorization-and-external-memory-design.md`](connector-authorization-and-external-memory-design.md): "Out of scope" says CA-10 moves the embedder code into `semanticcache` if the cache outlives memory. Under this design the cache goes, so the embedder stays with the memory store and goes with it. CA-G and RC-A both edit Phase 17b and renumber the §12.8 `DeleteByUser` steps, and the second to land rebases on the first. Its Terms row "Semantic cache" becomes historical once RC-A lands.
- [`billing-resource-attribution-design.md`](billing-resource-attribution-design.md): it leaves token metering to §11.2. Under O2 (b), RC-B adds `tokens_cache_read` and `tokens_cache_write` to §11.2.1, and any correction rule that names `tokens_input` and `tokens_output` decides whether the new fields are correctable.
- [`security-oversight-design.md`](security-oversight-design.md): the regulated-tenant controls lose the cross-user cache refusal. No other control depends on it.
- Any design that cites `§12.8 step N` for N of 3 and above cites the number RC-A gives the step, or better, the step's store name.

## Proposal split and order

| Proposal | Content | Depends on |
| :-- | :-- | :-- |
| RC-A | The removal: RC-1 to RC-4. | Nothing. It rebases on CA-G if CA-G lands first. |
| RC-B | Provider prompt caching under O1 and O2: the §4.9 passthrough statement, the usage count, the billing fields, and the closure of F-11.2.38 (RC-5). | The owner's answers to O1 and O2. It is independent of RC-A. |

RC-A can land first because it removes behaviour and adds none. RC-B fixes a metering gap that exists with or without the semantic cache.

## Sources

- Anthropic, "Prompt caching", https://platform.claude.com/docs/en/build-with-claude/prompt-caching, read 2026-11-09 (redirected from https://docs.claude.com/en/docs/build-with-claude/prompt-caching). It covers `cache_control`, the TTLs, the usage fields, the statement that `input_tokens` covers only tokens after the last breakpoint, the price multipliers, and workspace and organization isolation.
- OpenAI, "Prompt caching", https://developers.openai.com/api/docs/guides/prompt-caching, read 2026-11-09 (redirected from https://platform.openai.com/docs/guides/prompt-caching). It covers automatic caching, `prompt_cache_key`, retention, `cached_tokens`, and isolation across organizations. The page was read in part, so the claim about whether `prompt_tokens` includes cached tokens is not taken from it.
- Microsoft, "Prompt caching" for Azure OpenAI, https://learn.microsoft.com/en-us/azure/ai-foundry/openai/how-to/prompt-caching, read 2026-11-09. It covers automatic caching and isolation across subscriptions.
- Amazon Web Services, "Prompt caching for faster model inference", https://docs.aws.amazon.com/bedrock/latest/userguide/prompt-caching.html, read 2026-11-09. It covers `cache_control` in the InvokeModel body for Claude.

## Review history

No review has run.

### Revision notes

- Revision 1, dated 2026-11-09, follows the owner's request to remove the LLM response caching Lenny provides. It records the semantic cache as specified and built, the false-match behaviour observed by running the shipped store, the passthrough and the cached-token undercount of provider prompt caching, and the open decisions O1 and O2.
- Revision 2, dated 2026-11-09, files the defects found while writing this design in `BUILD-GAPS.md`, at the owner's request of 2026-11-09: the cached-token undercount as F-11.2.38 and the missing `openai` dialect route of the LLM proxy as F-4.9.48. F-4.9.10 stays closed, and F-4.9.48 cites it.
