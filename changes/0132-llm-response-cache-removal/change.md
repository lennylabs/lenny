# LLM response cache removal

Status: `Draft`, revision 8, 2026-11-16. Programs: [v1](../programs/v1.md).

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

The premise holds. Lenny specifies and ships its own cache of LLM responses, the semantic cache of [§4.9, "Semantic Caching"](../../spec/04_system-components.md#semantic-caching). A credential pool opts in with a `cachePolicy`, and the gateway's LLM proxy then answers a non-streaming request from a stored response when an earlier request from the same scope is similar enough **[source]**. The feature reaches into the storage roles, the Redis key layout, user erasure, tenant deletion, data classification, data residency, the compliance profile rules, the error catalogue, the admin API, two database columns, a gateway flag, the build sequence, the competitive comparison, and the documentation ("Current state").

The cache as built has defects beyond the cost of carrying it:

- It matches on the whole request body by lexical overlap, so in a multi-turn conversation two requests whose final instruction differs share almost every token. A request that asked to delete a code path received the cached response to a request that asked to add a test for it **[run]**.
- With `cacheScope: tenant`, the same false match hands one user's response, with its content, to another user in the tenant. §4.9 names only a timing side-channel for that scope **[source]**.
- A cache hit records no token usage, so a hit is invisible to quotas, budgets, and billing **[source]**.
- The per-pool `ttl`, `similarityThreshold`, and `backend` are validated and stored, and no code reads them **[source]**.

Provider-side prompt caching is a separate mechanism. Anthropic and OpenAI cache the computed prefix of a prompt inside their own service, and the model still generates a new response for every request **[external]**. Lenny forwards the Anthropic `cache_control` fields unchanged and does not meter the cached token counts the provider reports **[source]**. The owner's request does not cover provider-side caching. The owner settled on 2026-11-09 that Lenny keeps the passthrough and never strips or rewrites `cache_control` (D2), and this design records that Lenny undercounts input tokens whenever a runtime uses provider caching (F-11.2.38). The metering correction and the step that lands it belong to [`0133-llm-upstreams-and-cost-budgets`](../0133-llm-upstreams-and-cost-budgets/change.md) ("O2" and "RC-B" below are pointers to it).

## Scope

**Removed:**

- Lenny's semantic cache: the §4.9 "Semantic Caching" subsection, the `SemanticCache` storage role, `cachePolicy` and `cacheScope` on credential pools, the `t:{tenant_id}:scache:` Redis key family, `COMPLIANCE_CROSS_USER_CACHE_PROHIBITED`, the cache's erasure steps, and every reference in the specification ("RC-1").
- The packages `semanticcache`, `semanticcache/redisstore`, and `proxycache`, the proxy's `ProxyCache` seam, the `--llm-semantic-cache` flag, the pool fields, their validation, and the admin and bootstrap payload fields ("RC-2").
- The `cache_scope` and `cache_policy` columns, through a new migration ("RC-3").
- The tests, documentation, chart comments, and open records that cover the cache ("RC-2" and "RC-4").

**Changed:**

- §12.8's user erasure sequence and tenant deletion order lose their cache steps, and the user erasure steps after the removed one are renumbered ("RC-1").
- §11.7's compliance downgrade ratchet loses the item that re-enabled `cacheScope: tenant`. The ratchet stays.
- Phase 17b of the build sequence drops semantic caching from its title and deliverables, and the retired anchors of the retitled §18.39 heading and `TESTING.md` §13.36 heading get redirects ("RC-1").

**Added:**

- The content of a statement in §4.9 that the LLM proxy forwards provider prompt-caching fields unchanged and never strips or rewrites `cache_control`, with the isolation each provider applies and the residual risk within a shared upstream (D2, "RC-5"). RC-B lands the statement, and RC-B now belongs to [`0133-llm-upstreams-and-cost-budgets`](../0133-llm-upstreams-and-cost-budgets/change.md).

**Moved:**

- Cached input tokens in the authoritative usage count, which revision 2 placed here as O2 and RC-B. [`0133-llm-upstreams-and-cost-budgets`](../0133-llm-upstreams-and-cost-budgets/change.md) owns them.

**Out of scope:**

- Response caching for any other surface, such as idempotent replay under [§11.5](../../spec/11_policy-and-controls.md#115-idempotency), the routing cache, or the token cache. These caches hold no LLM responses.
- The OpenAI dialect endpoint of the proxy. §4.9 describes `POST {proxyUrl}/v1/chat/completions` for `proxyDialect: openai`, and the gateway mounts only `POST /llm-proxy/v1/messages` **[source]**. "Current state" records it as F-4.9.48, which step 0133.S2 of the LLM upstreams and cost budgets design closes.
- Token metering, cached tokens included. [`0121-billing-resource-attribution`](../0121-billing-resource-attribution/change.md) leaves it to §11.2, and [`0133-llm-upstreams-and-cost-budgets`](../0133-llm-upstreams-and-cost-budgets/change.md) owns the correction.

## Terms

| Term | Meaning |
| :-- | :-- |
| Semantic cache | Lenny's own LLM response cache under §4.9 "Semantic Caching". It stores a request and the provider's response, and it answers a later request from the stored response when the requests are similar. This design removes it. The owner's request calls it "caching LLM response". |
| Provider prompt caching | A cache inside the LLM provider's service that stores the computed state of a prompt prefix. A later request with an identical prefix is processed faster and billed at a reduced input rate, and the model still generates a new response. Anthropic requests it with `cache_control`, and OpenAI applies it automatically **[external]**. |
| `cache_control` | The Anthropic Messages request field that requests provider prompt caching, placed on a content block or at the top level of the request, with `type: ephemeral` and an optional `ttl` of `5m` or `1h` **[external]**. |
| Cached-token counts | The usage fields a provider returns for prompt caching. In the Anthropic Messages format they are `cache_read_input_tokens` and `cache_creation_input_tokens`, and `input_tokens` counts only the tokens after the last cache breakpoint **[external]**. |
| LLM proxy | The gateway subsystem of §4.9 that receives a proxy-mode pod's LLM request, runs the `PreLLMRequest` and `PostLLMResponse` interceptor phases of §4.8, translates the request to the upstream provider's format, injects the provider credential that the pod never sees, and records the provider-reported token usage. |
| Proxy mode | A credential pool with `deliveryMode: proxy`. The pod holds a lease token and calls the LLM proxy. Under `deliveryMode: direct`, the pod calls the provider itself and Lenny sees no LLM traffic. [`0133-llm-upstreams-and-cost-budgets`](../0133-llm-upstreams-and-cost-budgets/change.md) removes `deliveryMode` and direct mode, so every call Lenny meters goes through the proxy. |
| Credential pool | An admin-managed set of credentials, keyed by tenant and name in `credentialpoolstore` **[source]**. Today a pool holds LLM provider credentials, and the semantic cache was configured per pool. Under [`0133-llm-upstreams-and-cost-budgets`](../0133-llm-upstreams-and-cost-budgets/change.md), pools hold VCS credentials only. |
| `cacheScope` | The credential pool field that set the semantic cache's identity granularity: `per-user` (the default), `per-session`, or `tenant`. |
| Regulated tenant | A tenant whose `complianceProfile` is `soc2`, `fedramp`, or `hipaa`, as [`0142-security-oversight`](../0142-security-oversight/change.md) defines it. The cache rule that this design removes refused `cacheScope: tenant` for `hipaa` and `fedramp`. |
| Erasure step | One store's deletion in §12.8's `DeleteByUser` dependency-ordered deletion sequence, or one store in the tenant deletion order of Phase 4. Code cites a user erasure step by number, as `§12.8 step 6`. |
| Authoritative usage | The token counts the LLM proxy extracts from the provider's response. §11.2 makes them the quota and budget record for proxy-mode sessions and ignores the pod's own reports. |
| Anchor-move map | `tests/spec-anchor-moves.json`, the register of `kind: spec-anchor-moves` that the `specshift` anchor pass reads. Each entry names a retired anchor and the heading that succeeds it, and the pass rewrites every link into the retired anchor. `tests/registers/README.md` describes it, and **N3** of [§28.1 Naming law](../../spec/28_communication-channels.md#281-naming-law) states that an anchor that has to change moves through the anchor-redirect map so that a redirect exists for every inbound link **[source]**. |
| RC-n and RCR-n | The parts of this design under "Detailed design" carry RC-n, and its requirements under "Requirements" carry RCR-n. Step 0132.S1 of `steps.md` lands RC-1 to RC-4, and step 0133.S2 (LU-9) of [`0133-llm-upstreams-and-cost-budgets`](../0133-llm-upstreams-and-cost-budgets/steps.md#steps) lands the statement of RC-5, as `steps.md` records. |
| RC-A | The removal of the cache, RC-1 to RC-4, which step 0132.S1 lands. |

### Terms from other designs and the platform

| Term | Meaning |
| :-- | :-- |
| BUILD-GAPS and TEST-GAPS | `BUILD-GAPS.md` and `TEST-GAPS.md` at the repository root. The first is the audit of gaps between the specification and the code, with findings named F-n.n.n. The second is the audit of gaps between the specification and the tests, with findings named T-n.n.n. |
| `memorystore.Embedder` | The interface that turns text into a vector for the memory store of §9.4. `HashingEmbedder` is its built-in implementation, which hashes word tokens into a fixed-width vector and therefore measures shared vocabulary **[source]**. The semantic cache reuses it. |

### Labels from other designs

| Label | Meaning |
| :-- | :-- |
| [CA-10](../0123-connector-authorization-and-external-memory/change.md#ca-10-removing-built-in-memory) | Removal of the built-in memory store of §9.4, in the connector authorization design. It owns `memorystore`, which defines the embedder the semantic cache imports, and it edits Phase 17b and §12.8 as this design does. |
| CA-G | The proposal of the connector authorization design that lands CA-10 ([proposal split](../0123-connector-authorization-and-external-memory/steps.md#steps)). |
| LLM upstream | The record that [`0133-llm-upstreams-and-cost-budgets`](../0133-llm-upstreams-and-cost-budgets/change.md) adds in place of LLM credential pools: an endpoint, a credential source, a channel, a contract, a model allowlist, and a rate card. Provider cache isolation follows the provider account or workspace behind an upstream's credential. That design also defines the posture report of tenants that share an upstream, the warning when a shared upstream is granted to a `hipaa` or `fedramp` tenant, and the identity attributes the proxy forwards to an upstream. |
| Identity attributes | The attributes of a request, such as its tenant, user, and session, that the LLM proxy forwards to the upstream under [`0133-llm-upstreams-and-cost-budgets`](../0133-llm-upstreams-and-cost-budgets/change.md), which specifies them. An operator-run LLM gateway behind Lenny can use them to choose a provider workspace or key per user. |
| RC-B and O2 | Labels that revision 2 of this design gave to the metering correction and its open decision. Revision 3 moves both to [`0133-llm-upstreams-and-cost-budgets`](../0133-llm-upstreams-and-cost-budgets/change.md), as the owner decided on 2026-11-09. RC-B also lands the §4.9 passthrough statement of "RC-5". |

## High-level design

Lenny stops caching LLM responses. Every request a pod sends through the LLM proxy goes to the provider, the provider's response goes back through the `PostLLMResponse` phase, and the proxy records the provider's token usage for every request. The specification loses the semantic cache, its storage role, its key family, its erasure steps, its compliance rule, and its error code. The code loses the cache packages, the proxy seam, the flag, the pool fields, and the columns. Nothing replaces the cache inside Lenny, because no specified guarantee depended on it: it was disabled by default, no SLO counted on it, and no metric or alert measured it.

Provider prompt caching stays as it is today, as the owner settled (D2). The proxy forwards the pod's request body unchanged to Anthropic-format providers and never strips or rewrites `cache_control`, so a runtime that sets it gets the provider's cache, its lower latency, and its lower price. That cache stores no response, and the provider isolates it by organization, workspace, or project. Cache isolation therefore follows the provider account or workspace behind the LLM upstream of [`0133-llm-upstreams-and-cost-budgets`](../0133-llm-upstreams-and-cost-budgets/change.md), so a deployer gives a regulated tenant its own upstream, and the LLM upstreams and cost budgets design warns, without refusing, when a `hipaa` or `fedramp` tenant shares one. Strict isolation between users of one tenant is not a Lenny feature: the proxy forwards identity attributes to the upstream, and an operator-run LLM gateway behind Lenny can choose a provider workspace or key per user. §4.9 states the passthrough, the isolation, and the residual risk within a shared upstream. The usage count, which today ignores the cached-token counts, is corrected by the LLM upstreams and cost budgets design (O2 and RC-B moved there).

The parts:

- **Specification removal (RC-1).** Every section that names the cache drops it, §12.8's numbered user erasure steps are renumbered, and the retitled headings get anchor redirects.
- **Code removal (RC-2).** The cache packages, the proxy seam, the flag, the pool fields, the error code, the erasure wiring, and their tests are deleted.
- **Data and configuration (RC-3).** A new migration drops the two pool columns, and the chart and documentation lose their cache references.
- **Records (RC-4).** Open audit findings that exist only for the cache are resolved as obsolete, and findings that name it in passing are corrected.
- **Provider prompt caching passthrough (RC-5).** The content of the §4.9 statement of the passthrough, its isolation, the provider retention facts, and the residual risk, under D2. RC-B in the LLM upstreams and cost budgets design lands it.

## Requirements

- RCR-1. No Lenny component stores an LLM response to answer a later request. Every proxied request reaches the upstream provider.
- RCR-2. After RC-A, no specification section, pool field, admin or bootstrap payload field, flag, Helm value or comment, Redis key family, table column, storage role, error code, erasure step, metric, or test refers to the semantic cache, apart from the pool validator's refusal of the two removed field names under O3 and its tests.
- RCR-3. The LLM proxy records the provider's usage for every request it serves.
- RCR-4. User erasure and tenant deletion keep their order and their other steps after the cache steps go.
- RCR-5. Moved with RC-B to [`0133-llm-upstreams-and-cost-budgets`](../0133-llm-upstreams-and-cost-budgets/change.md), where LUR-10 states the pass-through contract, including the provider-native user field that the upstream writes. The identifier is not reused. Under D2, the pass-through keeps `cache_control` at the top level and on content blocks, does not rewrite a top-level `cache_control` into block-level breakpoints, and relays the provider's answer when the provider refuses a top-level `cache_control` for the model ("Provider prompt caching today").
- RCR-6. Moved with O2 to [`0133-llm-upstreams-and-cost-budgets`](../0133-llm-upstreams-and-cost-budgets/change.md). The identifier is not reused.

## Current state

### Specification

Each place below names the cache **[source]**:

| Section | What it says |
| :-- | :-- |
| [§4.9 Credential Leasing Service](../../spec/04_system-components.md#49-credential-leasing-service), opening | Lists "Semantic Caching" among the subsections the section covers. |
| [§4.9, "Semantic Caching"](../../spec/04_system-components.md#semantic-caching) | The optional `CachePolicy` on `CredentialPool` with `strategy: semantic`, `ttl: 300`, `similarityThreshold: 0.92`, and `backend: redis`, backed by the pluggable `SemanticCache` interface. A default Redis implementation, disabled by default and opt-in per pool. The `ValidateSemanticCacheErasure` contract helper. **Tenant and user isolation:** the key space, the `cacheScope` values, the default `per-user`, the refusal of `cacheScope: tenant` on a regulated pool with `COMPLIANCE_CROSS_USER_CACHE_PROHIBITED`, the timing side-channel trade-off, and the test `TestSemanticCacheTenantIsolation`. |
| [§11.7 Audit Logging](../../spec/11_policy-and-controls.md#117-audit-logging), **Compliance profile downgrade ratchet.** | Item 1, "Runtime controls are lost", lists re-enabling `cacheScope: tenant` among the controls a downgrade would remove. |
| [§12.1 Design Principle](../../spec/12_storage-architecture.md#121-design-principle) | Names `SemanticCache` with `MemoryStore` as a pluggable role that must expose the erasure primitives. |
| [§12.2 Storage Roles](../../spec/12_storage-architecture.md#122-storage-roles) | The `SemanticCache` row, and `SemanticCache` in the list of Redis-backed roles that must use the `t:{tenant_id}:` prefix. |
| [§12.4 Redis HA and Failure Modes](../../spec/12_storage-architecture.md#124-redis-ha-and-failure-modes) | The `t:{tenant_id}:scache:{scope}:{hash}` key row, and item (d) of `TestRedisTenantKeyIsolation`, a semantic cache write that must not hit for another tenant. |
| [§12.6 Interface Design](../../spec/12_storage-architecture.md#126-interface-design) | The comment on `RedisConcernCachePubSub` lists the semantic cache. |
| [§12.8 Compliance Interfaces](../../spec/12_storage-architecture.md#128-compliance-interfaces) | The `SemanticCache` row of the erasure scope table, step 2 of the `DeleteByUser` dependency-ordered deletion sequence, `SemanticCache` in the Phase 4 order of the tenant deletion lifecycle, and semantic cache entries in the list of Redis-stored personal data under data residency. |
| [§12.9 Data Classification](../../spec/12_storage-architecture.md#129-data-classification) | The row "Semantic cache entries", Confidential. The data classes are named Public, Internal, Confidential, and Secret under [`0150-tenant-security-settings`, "TSS-8"](../0150-tenant-security-settings/change.md#tss-8-data-classes). |
| [§15.1 REST API](../../spec/15_external-api-surface.md#151-rest-api) | The error code `COMPLIANCE_CROSS_USER_CACHE_PROHIBITED`. |
| [§18.1 Phasing Intent](../../spec/18_build-sequence.md#181-phasing-intent) | Item 6 lists "semantic caching" among the post-v1 surfaces. |
| [§18.2 Phase Map](../../spec/18_build-sequence.md#182-phase-map) | Row 17b, "Memory, semantic caching, eval hooks". |
| [§18.39 Phase 17b](../../spec/18_build-sequence.md#1839-phase-17b--memory-semantic-caching-eval-hooks) | The `SemanticCacheStore` deliverable "per §21", the custom-backend preflight for `SemanticCacheStore`, and the Tier 2 `semantic_cache_test` exit criterion. |
| [§23 Competitive Landscape](../../spec/23_competitive-landscape.md#23-competitive-landscape), **Feature Comparison Matrix** | The "Semantic cache" row. |
| [§23.1 Why Lenny?](../../spec/23_competitive-landscape.md#231-why-lenny), **Additional Platform Capabilities** | Item 9 ends with "Optional `SemanticCache` interface on credential pools for LLM response caching." |
| `spec/README.md` | The table-of-contents entry for §18.39. |

The specification contradicts itself on the cache's status. §4.9 and §12 specify it as a v1 feature, §18.1 calls it a post-v1 surface, and §18.39 cites §21 for the `SemanticCacheStore` interface while §21 says nothing about caching **[source]**. §4.9 refuses `cacheScope: tenant` on "a pool with a regulated `complianceProfile`", while `complianceProfile` is a tenant field, and the code checks the tenant **[source]**.

No section of §16 defines a metric or alert for the cache, and no SLO refers to it **[source]**.

Retitling §18.39 retires its anchor, `#1839-phase-17b--memory-semantic-caching-eval-hooks`, which `spec/README.md` links. RC-3 retitles `TESTING.md` §13.36, "Phase 17b — Memory, semantic caching, eval hooks", which retires the anchor `#1336-phase-17b--memory-semantic-caching-eval-hooks` that §18.2 row 17b and the §18.39 exit criteria link. No file under `docs/` links either anchor **[source]**. The anchor-move map holds no entry today **[source]**.

The adversarial review of revision 2 confirmed that no eval or replay feature depends on the cache, and that no migration other than 0070 and 0078 creates or reads `cache_scope` or `cache_policy`. A read of the tree on 2026-11-09 agrees: the only migrations naming either column are those two and their down files **[source]**.

### Tree

All of the following was read on 2026-11-09 **[source]**:

| Area | What exists |
| :-- | :-- |
| `pkg/gateway/llmproxy/semanticcache` | `Store` with `Get`, `Put`, `DeleteByUser`, and `DeleteByTenant`, aliased as `SemanticCache`. `Key` with tenant, scope, user, session, model, and provider. `CacheScope` with `per-user`, `per-session`, and `tenant`. `InMemory`, the store the gateway wires. `Similarity`, which uses `memorystore.CosineDistance`. `ValidateSemanticCacheErasure` in `contract.go`. Defaults `DefaultSimilarityThreshold` 0.92 and `DefaultTTLSeconds` 300. |
| `pkg/gateway/llmproxy/semanticcache/redisstore` | The Redis store under `t:{tenant_id}:scache:`. No production code imports it. Only tests do. |
| `pkg/gateway/llmproxy/proxycache` | `Adapter`, which implements the proxy's `ProxyCache` seam. It reads the pool's `CachePolicy.Enabled` and `Strategy` and its `CacheScope`, resolves the session's user for `per-user`, takes the model from the request body, and uses the whole request body as the query text. |
| `pkg/gateway/llmproxy/llmproxy/handler.go` | The `ProxyCache` interface and `Handler.Cache`. After the `PreLLMRequest` phase, a non-streaming request is looked up. A hit runs `PostLLMResponse` over the stored body and returns it, skipping translation, credential resolution, the upstream call, and usage recording. A miss stores the translated response before `PostLLMResponse`. |
| `cmd/lenny-gateway` | `--llm-semantic-cache` and `LENNY_LLM_SEMANTIC_CACHE` in `flags.go`, off by default. When set, `adminrouter.go` builds one `semanticcache.InMemory` per replica with the default embedder, TTL, and threshold, and `llmproxy.go` wraps it in `proxycache.New` for the proxy. `llmproxy_server.go` passes it to the handler, and `main.go` threads it from `buildAdminRouter` as `erasureSemanticCache`. The same instance feeds the user erasers (`userCacheErasers`) and the tenant erasers (`cacheTenantErasers` in `tenant_erasers.go`). `sessionUserLookup` in `runserver.go` serves the per-user scope. `erasure_contract.go` asserts the store satisfies the erasure contract. |
| `pkg/gateway/storage/erasure` | The rank `"semantic_cache": 20` in `order.go`, and the doc comments in `contract.go` that name `SemanticCache` beside `MemoryStore` as a pluggable role. |
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

- The Anthropic translator forwards the request body unchanged, and the Bedrock and Vertex translators re-encode it with every field kept except `model`, so `cache_control` reaches all three Anthropic-format providers **[source]**. No translator rewrites a top-level `cache_control` into block-level breakpoints **[source]**.
- A top-level `cache_control` is Anthropic's automatic caching, and not every provider accepts it for every model **[external]**. Bedrock's guide lists the Claude models that support automatic caching on InvokeModel, and states that other models return a `ValidationException` for the top-level field. Anthropic's guide states that the legacy Bedrock integration, which serves Claude Opus 4.6 and earlier, returns a 400 for it. Google Cloud's prompt-caching page for Claude documents `cache_control` on content blocks and does not document the top-level placement. Block-level `cache_control` is accepted on all three.
- The proxy mounts only `POST /llm-proxy/v1/messages` and always translates from the Anthropic dialect, so the OpenAI and Azure translators are unreachable from the proxy today **[source]** (F-4.9.48). F-4.9.10 recorded the missing route among its evidence and was closed by provider dispatch, with the dialect route placed out of scope, so F-4.9.48 records the route and F-4.9.10 stays closed. OpenAI and Azure OpenAI cache automatically, with no request field needed **[external]**.
- `extractAnthropicUsage` and the streaming `applySSEUsage` read `input_tokens` and `output_tokens` alone **[source]**. Anthropic's `input_tokens` counts only the tokens after the last cache breakpoint, and the total input is `cache_read_input_tokens + cache_creation_input_tokens + input_tokens` **[external]**. Lenny's authoritative input count therefore omits every cached token. §11.2, **Quota Update Timing**, says the proxy extracts `input_tokens` and `output_tokens` and that a runtime cannot influence quota accounting by underreporting **[source]**. F-11.2.38 records the undercount, and [`0133-llm-upstreams-and-cost-budgets`](../0133-llm-upstreams-and-cost-budgets/change.md) corrects it.
- Neither the specification nor the tree mentions `cache_control`, `cache_read_input_tokens`, or `cache_creation_input_tokens`. The Open Responses translator writes `cached_tokens: 0` because it does not track them **[source]**.
- No BUILD-GAPS finding recorded the cached-token undercount before this design **[source]**, which files it as F-11.2.38.
- Provider retention, read on 2026-11-09 **[external]**: Anthropic's cache has a 5-minute lifetime, refreshed at no cost each time the entry is used, and a 1-hour lifetime at a higher write price. Under zero data retention, Anthropic holds cache representations in memory for the lifetime and deletes them after expiry. On Bedrock and on Google Cloud, Claude caches take the same 5-minute and 1-hour lifetimes, and Bedrock resets the lifetime on each hit. OpenAI's GPT-5.6 and later models keep a cached prefix for a minimum of 30 minutes by default, or 12 hours on request in the Responses API, and OpenAI states that it may retain an entry longer than the requested minimum and that the encrypted tensors are retained for at most 24 hours. OpenAI's earlier models offer `in_memory` retention, which keeps an entry for about 5 to 10 minutes of inactivity and at most one hour, and `24h` extended retention, which is the default for an organization without zero data retention. Revision 2 stated OpenAI's retention as "up to 24 hours under extended retention", which describes the earlier models only.
- Provider isolation, read on 2026-11-09 **[external]**: Anthropic isolates caches by workspace on the Claude API, Claude Platform on AWS, and Microsoft Foundry, and by organization on Bedrock and on Claude models on Google Cloud (Google managed). Google Cloud's own page states that a cache is unique to its Google Cloud project. OpenAI does not share caches across organizations, and Azure OpenAI does not share them across subscriptions.

### Records

| Record | State | Relation |
| :-- | :-- | :-- |
| F-11.2.38 | Open | The cached-token undercount of "Provider prompt caching today", filed by revision 2. RC-B closes it, and RC-B now belongs to [`0133-llm-upstreams-and-cost-budgets`](../0133-llm-upstreams-and-cost-budgets/change.md). |
| F-4.9.48 | Open | The proxy's missing `openai` dialect route, filed by this design. Step 0133.S2 (LU-B) of the LLM upstreams and cost budgets design closes it, and that design's OpenAI metering depends on it. |
| F-12.8.53 | Open | Per-replica semantic cache and billing buffer erased only on one replica. The cache half goes with the cache, and the billing buffer half stays. |
| F-4.9.13, F-4.9.14, F-12.2.16, F-12.8.15 | Closed | Built or wired the cache. They stay as history. |
| T-4.9.15 | Open | `TestSemanticCacheTenantIsolation` for a similar query. |
| T-12.1.1 | Open, High | No startup or per-job erasure preflight for `SemanticCache`. |
| T-12.2.5 | Open | The opt-in gate and the miss-on-outage behaviour are untested. |
| T-12.1.5, T-12.1.6, T-12.1.7, T-12.7.2 | Open | Name `SemanticCache` in passing beside `MemoryStore` or other stores. |
| `BUILD-PROGRESS.md` and `BUILD-PLAN.md` | Records | Phase 17b rows mention semantic caching. |

## Detailed design

### RC-1 Specification removal

The specification loses every statement in the table under "Specification" **[design]**. §4.9's "Semantic Caching" subsection is deleted, with its entry in the section's opening list. §12.2 loses the `SemanticCache` row and its name in the Redis prefix rule, §12.1 names `MemoryStore` alone as the pluggable example, and §12.4 loses the key row and item (d) of `TestRedisTenantKeyIsolation`, whose later item takes its letter.

**Erasure.** §12.8's erasure scope table loses the `SemanticCache` row, and the tenant deletion order of Phase 4 runs `LeaseStore` and then the Redis caches. The `DeleteByUser` sequence loses step 2, and steps 3 onward move up by one. The renumbering changes the target of every `§12.8 step N` citation for N of 3 and above in the specification, the code, and the tests, so step 0132.S1 sweeps them in the same change. The alternative of keeping an empty step 2 leaves a placeholder in a normative list, and "Alternatives rejected" records it. CA-G removes the `MemoryStore` step from the same sequence, and the step that lands second rebases its renumbering on the first ("Steps" in `steps.md`). [`0121-billing-resource-attribution`](../0121-billing-resource-attribution/change.md) cites steps 5 and 15 under "Erasure and teardown.", which become 4 and 14, and 0133 adds the `llm_usage` erasure row and removes the LLM lease step from the same sequence, so each of those changes renumbers against whichever lands first. The sweep covers the `DeleteByUser` list alone: the "§12.8 sub-step" citations name the legal-hold escrow list and keep their numbers.

**Compliance.** §11.7 item 1 lists the controls that remain: the SIEM requirement, `audit.grantCheckInterval`, and `audit.pgaudit.enabled`. §15.1 loses `COMPLIANCE_CROSS_USER_CACHE_PROHIBITED`.

**Build sequence and comparison.** §18.1 item 6 and §18.2 row 17b drop "semantic caching". §18.39 drops the `SemanticCacheStore` deliverable, its preflight, and `semantic_cache_test`, which also removes the dangling citation of §21. The title of §18.39 changes, and `spec/README.md` follows it. §23 loses the matrix row, and §23.1 item 9 loses its last sentence.

**Anchor redirects.** Step 0132.S1 adds two entries to the anchor-move map **[design]**. The first maps `1839-phase-17b--memory-semantic-caching-eval-hooks` to the retitled §18.39 heading in `spec/18_build-sequence.md`. The second maps `1336-phase-17b--memory-semantic-caching-eval-hooks` to the retitled §13.36 heading in `TESTING.md`. The proposal runs the `specshift` anchor pass, which rewrites the link in `spec/README.md` and the two links in §18.2 and §18.39, and it leaves the map as `tests/registers/README.md` states for a completed pass. CA-G retitles the same two headings when it removes memory, so the proposal that lands second maps the anchor the first one left.

### RC-2 Code removal

The following are deleted **[design]**:

- `pkg/gateway/llmproxy/semanticcache`, its `redisstore`, and `pkg/gateway/llmproxy/proxycache`, with their tests.
- `ProxyCache`, `Handler.Cache`, the lookup before translation, and the store after translation in `handler.go`, with `cache_test.go`. The handler then has one path for a non-streaming request: translate, forward, translate back, run `PostLLMResponse`, and record usage.
- The `cache` parameter of `newLLMProxyServer`, the `--llm-semantic-cache` flag and its environment variable, `erasureSemanticCache` and its threading through `buildAdminRouter`, `buildUserErasers`, `buildTenantErasers`, `userCacheErasers`, `tenantErasersBeforeSessions`, and `cacheTenantErasers`, `sessionUserLookup`, and the assertion in `erasure_contract.go`.
- The `"semantic_cache"` rank in `erasure/order.go`, `semanticcache_wiring_test.go`, and the cache cases in `eraser_postures_test.go` and `release_wiring_test.go`.
- `CacheScope`, `CachePolicy`, `validateCachePolicy`, and `validCacheScope` in `credentialpoolstore`, and the two columns in `pgstore`'s reads and writes.
- `cacheScope`, `cachePolicy`, `CachePolicyPayload`, `validCacheScopes`, `crossUserCacheRegulatedProfiles`, `validateCacheScope`, `toCachePolicy`, `fromCachePolicy`, `bootstrapCacheScope`, and the cache comparison in the bootstrap upsert, in `externalapi/admin`.
- The `COMPLIANCE_CROSS_USER_CACHE_PROHIBITED` entry in `errorclassify`.

`semanticcache` is the only importer of `memorystore` outside the memory feature, for `Embedder`, `HashingEmbedder`, and `CosineDistance` **[source]**. Its removal leaves those symbols with the memory store, so CA-10's step that would move them into `semanticcache` no longer applies.

The admin API decodes pool payloads without refusing unknown fields **[source]**, so a field that only disappears from the schema would be accepted and ignored. Under O3 the pool validator in `externalapi/admin` and the bootstrap upsert refuse a pool write that names `cachePolicy` or `cacheScope`, whatever value it carries, with `400 VALIDATION_ERROR` whose `details.field` names the member and whose message states that Lenny no longer caches LLM responses **[design]**. The two names stay in the validator's list of removed fields and nowhere else.

### RC-3 Data and configuration

- A new migration drops `credential_pools.cache_scope` and `credential_pools.cache_policy`. Migrations 0070 and 0078 stay as history, as the precedent of migration 0180 does for `slot_id` **[source]**. Nothing migrates, because there are no deployments. `prod_columns_test.go` records the drop as it does for 0180 **[design]**.
- The chart drops "semantic cache" from the `cachePubSubUrl` comment and `cacheScope` from the bootstrap example, and `tests/bootstrap_test.yaml` stops asserting it **[design]**.
- `README.md`, `docs/index.md`, `docs/about/comparisons.md`, `docs/about/status.md`, `docs/getting-started/architecture.md`, `docs/reference/error-catalog.md`, `docs/testing/forward-compatibility.md`, `pkg/gateway/ARCHITECTURE.md`, and `TESTING.md` §12.2.1, §13.36, and §23.2 drop the cache **[design]**. The README and index sentence keeps credential routing. The changelog states the removal, with the semantic cache, `cachePolicy`, `cacheScope`, the `--llm-semantic-cache` flag, and `COMPLIANCE_CROSS_USER_CACHE_PROHIBITED` named as removed. This design adds no setting, default, or limit. Its one failure mode is the refusal of O3: `docs/reference/error-catalog.md` and the credential pool page of the admin API reference state that a pool write that names `cachePolicy` or `cacheScope` is refused with `400 VALIDATION_ERROR` naming the field. The runtime-facing rules it states in "RC-5", the `cache_control` passthrough, the provider's refusal of a top-level `cache_control` passed back unchanged, cache isolation by the upstream's provider account, the residual signal within a shared upstream, and provider retention, are documented through the documentation list of [`0133-llm-upstreams-and-cost-budgets`](../0133-llm-upstreams-and-cost-budgets/change.md), which owns RC-B.

### RC-4 Records

- F-12.8.53 is narrowed to the billing buffer, with a note that the cache half went with RC-A **[design]**.
- T-4.9.15, T-12.1.1, and T-12.2.5 are resolved as obsolete with a pointer to RC-A, and T-12.1.5, T-12.1.6, T-12.1.7, and T-12.7.2 drop the cache from their text **[design]**.
- The closed findings and `scripts/refactor/manifest` stay as written.
- F-11.2.38 records the cached-token undercount of "Provider prompt caching today", owned by RC-B in [`0133-llm-upstreams-and-cost-budgets`](../0133-llm-upstreams-and-cost-budgets/change.md) **[design]**.

### RC-5 Provider prompt caching passthrough

This section states what the §4.9 statement of the passthrough says under D2 **[design]**. RC-B lands it, and RC-B belongs to [`0133-llm-upstreams-and-cost-budgets`](../0133-llm-upstreams-and-cost-budgets/change.md) since revision 3, because that design rewrites the §4.9 LLM proxy around the LLM upstream record and the statement names the upstream.

**Passthrough.** §4.9's "Native translator" states that the proxy forwards an Anthropic Messages request body, including `cache_control` at the top level and on content blocks, unchanged apart from the provider rehosting the translator already performs, and that a provider's prompt cache is the provider's own mechanism. It states that the proxy does not rewrite a top-level `cache_control`, so a runtime that sends one to a provider or model that refuses it receives the provider's refusal (LUR-10 of the LLM upstreams and cost budgets design). The pass-through applies to the body as the `PreLLMRequest` interceptor phase of §4.8 leaves it: D2 binds Lenny's own translators and handler, and a deployer's interceptor may still remove `cache_control`, which is the one way inside Lenny to close the prefix signal between users who share an upstream. Bedrock refuses it for a model outside its automatic-caching list, and Anthropic's guide states a 400 for it on the legacy Bedrock integration **[external]**. Google Cloud's Claude page documents only the block placement, and "Validation owed" in `steps.md` asks whether Google-managed Vertex accepts the top-level field.

**Isolation.** It states the isolation each provider documents **[external]**:

- Anthropic isolates caches by workspace on the Claude API, Claude Platform on AWS, and Microsoft Foundry, and by organization on Bedrock and on Claude models on Google Cloud (Google managed). Google Cloud's page states that a cache is unique to its Google Cloud project.
- OpenAI does not share caches across organizations, and Azure OpenAI does not share them across subscriptions.

Cache isolation therefore follows the provider account, workspace, or project behind the credential of the LLM upstream that serves the request. Two tenants whose upstreams use one provider workspace share that provider's cache, so a deployer gives a regulated tenant an upstream of its own, with a credential from a separate provider workspace, organization, or project. [`0133-llm-upstreams-and-cost-budgets`](../0133-llm-upstreams-and-cost-budgets/change.md) reports in posture the tenants that share an upstream and warns when a shared upstream is granted to a `hipaa` or `fedramp` tenant, and §4.9 cites both. The provider returns no stored response, so no response content crosses tenants or users **[external]**.

**Isolation between users.** It states that strict isolation between the users of one tenant is not a Lenny feature. The proxy forwards identity attributes of each request, such as its tenant, user, and session, to the upstream, as the upstreams design specifies, so an operator who runs its own LLM gateway behind Lenny as the upstream can choose a provider workspace or key per user there.

**Residual risk within a shared upstream.** It states that the users of an upstream share its provider cache, which inside a tenant is the normal configuration. One user's agent can therefore detect that another user of the same upstream sent an identical prompt prefix recently, through the `cache_read_input_tokens` count in its own response as well as through latency. The signal reveals that a prefix was sent within the cache lifetime. It also reveals content an agent can guess: by testing each candidate against the count, an agent recovers a low-entropy value, such as an identifier or a yes or no, that follows a prefix it knows. Lenny does not remove the signal, because the count is the input to metering and a runtime that sets `cache_control` reads it.

**Retention.** It states that provider cache retention is the provider's, as "Provider prompt caching today" records: 5 minutes or 1 hour for Claude on every platform the proxy reaches, and for OpenAI a 30-minute or 12-hour minimum on GPT-5.6 and later models, with retention of at most 24 hours, and `in_memory` or `24h` retention on earlier models **[external]**. A deployer with a regulated tenant checks these against its provider agreement.

**Metering (moved).** The metering correction moved to [`0133-llm-upstreams-and-cost-budgets`](../0133-llm-upstreams-and-cost-budgets/change.md) with RC-B.

### What replaces the cache's guarantees

| Guarantee or effect | After removal |
| :-- | :-- |
| Lower cost and latency for a repeated non-streaming request | No replacement in Lenny. Provider prompt caching lowers the cost and latency of a repeated prefix for runtimes that use it (D2). A runtime may cache inside its own process for its own session. |
| Tenant, user, and session isolation of cached responses | No stored responses exist, so the rule has no subject. |
| Erasure of cached pairs on user erasure and tenant deletion | The gateway holds no LLM response, so the erasure steps have nothing to delete. F-12.8.53's cache half closes. |
| Refusal of cross-user sharing for `hipaa` and `fedramp` tenants | No cross-user response sharing exists. The §11.7 ratchet keeps its other controls. |
| No usage on a hit | Every request is metered from the provider's response (RCR-3). |
| SLOs, metrics, and alerts | None existed, so none change. |

### Security effects

- LLM requests and responses are no longer stored in gateway memory or, under the unwired Redis store, in Redis. §12.9 loses the "Semantic cache entries" row of the Confidential class, and §12.8's residency list loses an entry **[design]**.
- The false-match defect goes, so no session receives a response written for a different request, and no user in a `cacheScope: tenant` pool receives another user's response **[design]**.
- The cross-user timing side-channel of `cacheScope: tenant` goes. The provider-side signal of identical prefixes remains, through latency and through `cache_read_input_tokens`, between users of one upstream, and RC-5 states it (D2).
- The gap in which a raised compliance profile left `cacheScope: tenant` in force goes with the field.
- No unredacted pre-`PostLLMResponse` content is held between requests.
- Under the current usage count, provider caching lets a runtime keep most of its input outside quotas and budgets. Removing the semantic cache does not change that **[source]**, and [`0133-llm-upstreams-and-cost-budgets`](../0133-llm-upstreams-and-cost-budgets/change.md) corrects it.

### Billing effects

- A request that a hit would have answered now reaches the provider and is billed by the provider and metered by Lenny. With the cache off by default, and absent from the chart, an installation that never set `LENNY_LLM_SEMANTIC_CACHE` sees no change **[source]**.
- `tokens_input` stays an uncached-tail count until RC-B lands in [`0133-llm-upstreams-and-cost-budgets`](../0133-llm-upstreams-and-cost-budgets/change.md), which states the corrected billing fields.

## Limits

- Lenny offers no response caching. A deployer that wants it runs it outside Lenny, and this design specifies no integration for it.
- Under D2, Lenny does not control provider cache retention or the prefix signal between users or tenants that share an upstream. It documents them. A deployer gives a tenant its own upstream where the signal matters between tenants, and an operator-run LLM gateway behind Lenny can separate users by the forwarded identity attributes.
- Under D2, a runtime that sends a top-level `cache_control` to a provider or model that refuses it receives the refusal, because the proxy does not rewrite the field.

## Alternatives rejected

- **Fix the cache instead of removing it.** Exact matching on a normalized body would end the false matches, but the owner asked for removal (D1), and exact repeats are what provider prompt caching already serves at the prefix level.
- **Keep an empty step 2 in §12.8.** It would avoid renumbering, and it leaves a placeholder step in a normative sequence. The citation sweep is mechanical.
- **Disable the cache and keep the code.** The specification would still describe a feature, and the erasure, isolation, and compliance rules would remain obligations with no behaviour behind them.
- **Strip provider caching fields with the semantic cache.** Recorded under D2. It raises cost for runtimes, and it cannot turn off OpenAI's automatic caching.

## Specification changes

The following edits locate their targets by heading:

| Section | Change |
| :-- | :-- |
| [§4.9](../../spec/04_system-components.md#49-credential-leasing-service), opening | Remove "Semantic Caching" from the list of subsections. |
| [§4.9, "Semantic Caching"](../../spec/04_system-components.md#semantic-caching) | Delete the subsection. |
| [§4.9, "LLM Reverse Proxy"](../../spec/04_system-components.md#llm-reverse-proxy), **Native translator.** | Under D2, the passthrough of `cache_control` without rewriting, the provider isolation, the isolation between users, the residual risk within a shared upstream, and the retention of "RC-5". RC-B in [`0133-llm-upstreams-and-cost-budgets`](../0133-llm-upstreams-and-cost-budgets/change.md) lands this edit. |
| [§11.2 Budgets and Quotas](../../spec/11_policy-and-controls.md#112-budgets-and-quotas) and [§11.2.1 Billing Event Stream](../../spec/11_policy-and-controls.md#1121-billing-event-stream) | Moved with O2 and RC-B to [`0133-llm-upstreams-and-cost-budgets`](../0133-llm-upstreams-and-cost-budgets/change.md). |
| [§11.7 Audit Logging](../../spec/11_policy-and-controls.md#117-audit-logging), **Compliance profile downgrade ratchet.** | Remove `cacheScope: tenant` from item 1. |
| [§12.1](../../spec/12_storage-architecture.md#121-design-principle) | Remove `SemanticCache` from the pluggable example. |
| [§12.2](../../spec/12_storage-architecture.md#122-storage-roles) | Remove the `SemanticCache` row and its name in the Redis prefix rule. |
| [§12.4](../../spec/12_storage-architecture.md#124-redis-ha-and-failure-modes) | Remove the `scache` key row and item (d) of `TestRedisTenantKeyIsolation`, re-letter the item after it, and drop "semantic cache" from the sentence that requires "DLQ, inbox, semantic cache, and delegation budget key coverage". |
| [§12.6](../../spec/12_storage-architecture.md#126-interface-design) | Remove the semantic cache from the `RedisConcernCachePubSub` comment. |
| [§12.8](../../spec/12_storage-architecture.md#128-compliance-interfaces) | Remove the erasure scope row, `DeleteByUser` step 2 with the renumbering of later steps, `SemanticCache` from the Phase 4 order, and semantic cache entries from the residency list. |
| [§12.9](../../spec/12_storage-architecture.md#129-data-classification) | Remove the "Semantic cache entries" row. |
| [§15.1](../../spec/15_external-api-surface.md#151-rest-api) | Remove `COMPLIANCE_CROSS_USER_CACHE_PROHIBITED`. State that a credential pool write that names `cachePolicy` or `cacheScope` is refused with `400 VALIDATION_ERROR` naming the field (O3). |
| [§18.1](../../spec/18_build-sequence.md#181-phasing-intent), [§18.2](../../spec/18_build-sequence.md#182-phase-map), and [§18.39](../../spec/18_build-sequence.md#1839-phase-17b--memory-semantic-caching-eval-hooks) | Remove semantic caching from item 6, row 17b, and the Phase 17b title, deliverables, and exit criteria. Update `spec/README.md`. Redirect the retired §18.39 anchor and the retired `TESTING.md` §13.36 anchor through the anchor-move map ("RC-1"). |
| [§23](../../spec/23_competitive-landscape.md#23-competitive-landscape) and [§23.1](../../spec/23_competitive-landscape.md#231-why-lenny) | Remove the matrix row and the last sentence of item 9. |

## Non-specification changes

| Area | Change |
| :-- | :-- |
| `pkg/gateway/llmproxy/semanticcache`, `semanticcache/redisstore`, and `proxycache` | Delete. |
| `pkg/gateway/llmproxy/llmproxy` | Remove `ProxyCache` and the cache paths in `handler.go`, and `cache_test.go`. The usage extraction changes belong to RC-B in [`0133-llm-upstreams-and-cost-budgets`](../0133-llm-upstreams-and-cost-budgets/change.md). |
| `tests/spec-anchor-moves.json` | The two anchor redirects of "RC-1", consumed by the `specshift` anchor pass. |
| `pkg/gateway/storage/erasure/contract.go` and `cmd/lenny-gateway/main.go` | Remove `SemanticCache` from the doc comments, and the `erasureSemanticCache` threading. |
| `cmd/lenny-gateway` | Remove the flag, the in-memory store, its threading through the admin router and erasers, `sessionUserLookup`, the erasure contract assertion, and the cache cases of the wiring tests. |
| `pkg/gateway/storage/erasure` | Remove the `semantic_cache` rank and `semanticcache_wiring_test.go`. |
| `pkg/gateway/credentials/credentialpoolstore` and `pgstore` | Remove the cache fields, their validation, and the column reads and writes. |
| `pkg/gateway/externalapi/admin` and `errorclassify` | Remove the payload fields, validation, bootstrap handling, and error code. Add `cachePolicy` and `cacheScope` to the pool validator's list of removed fields, refused by name under O3 in the admin API and the bootstrap upsert. |
| `migrations` | A new migration that drops `cache_scope` and `cache_policy`, and the matching `prod_columns_test.go` rows. |
| Every `§12.8 step N` citation of the `DeleteByUser` list for N of 3 and above | Renumber in code comments, `// spec:` annotations, and test names. Leave "§12.8 sub-step" citations, which name the legal-hold escrow list. |
| `tests/tier2_component/stores` and `tests/tier4_integration` | Delete `semanticcache_test.go` and the cache subtest, and remove the cache lines in `scaffolds_test.go` and `erasure_interface_test.go`. Change the sample `scache` key names in `rediskeys_test.go` and `redis_cluster_smoke_test.go` to a key family that remains. |
| `tests/spec-map.json` | Remove the cache test entries. |
| `charts/lenny` | The values comments and `tests/bootstrap_test.yaml`. |
| Documentation and `TESTING.md` | As "RC-3" lists. |
| `BUILD-GAPS.md` and `TEST-GAPS.md` | As "RC-4" lists. |

## Tests

| Tier | Coverage |
| :-- | :-- |
| 1 | Two identical non-streaming requests through the proxy handler each reach the upstream and each record usage (RCR-1, RCR-3). A credential pool write whose payload names `cacheScope` or `cachePolicy`, with any value, is refused with `400 VALIDATION_ERROR` whose `details.field` names the member, and a write that names neither succeeds (O3). The user erasure order and the tenant deletion order contain no cache entry and keep their other entries in order (RCR-4). The pass-through tests moved with RCR-5 to [`0133-llm-upstreams-and-cost-budgets`](../0133-llm-upstreams-and-cost-budgets/change.md): a request with block-level and top-level `cache_control` reaches the upstream fake with both placements intact and no breakpoint added or moved, and a 400 or `ValidationException` for a top-level `cache_control` is relayed to the pod unchanged. |
| 2 | The migration drops both columns on a real Postgres, and the pool store round-trips a pool without them. |
| 3 | The admin pool endpoints and the bootstrap upsert return no cache fields and refuse a pool document that names `cachePolicy` or `cacheScope` with `400 VALIDATION_ERROR` naming the field (O3), and the error catalogue has no `COMPLIANCE_CROSS_USER_CACHE_PROHIBITED`. |
| 4 | On the compose stack, a session's repeated request through the LLM proxy is billed twice in `token_usage.checkpoint`. |
| Static | A repository check finds no `semanticcache`, `proxycache`, `cachePolicy`, `cacheScope`, `scache`, or `COMPLIANCE_CROSS_USER_CACHE_PROHIBITED` outside migrations 0070 and 0078, the new migration, the pool validator's list of removed fields and its tests, the §15.1 statement of the refusal, the documentation pages RC-3 edits to record the removal, `CHANGELOG.md`, the records, `changes/`, `proposals/`, and `scripts/refactor/manifest` (RCR-2). It matches `cacheScope` only as a credential pool field, because the gateway list results and the bridge of other changes in the program carry a `cacheScope` of their own. The citation resolver finds no link into either retired Phase 17b anchor. |

Every test carries the `// spec:` annotation, and tests at tier 2 and above carry `// diagnosis:`, as `test-coverage.md` requires.

## Instructions for reviewers of other designs

- [`0123-connector-authorization-and-external-memory`](../0123-connector-authorization-and-external-memory/change.md): "Out of scope" says CA-10 moves the embedder code into `semanticcache` if the cache outlives memory. Under this design the cache goes, so the embedder stays with the memory store and goes with it. CA-G and RC-A both edit Phase 17b and renumber the §12.8 `DeleteByUser` steps, and the second to land rebases on the first. Its Terms row "Semantic cache" becomes historical once RC-A lands.
- [`0121-billing-resource-attribution`](../0121-billing-resource-attribution/change.md): it leaves token metering to §11.2. The billing fields revision 2 proposed moved with O2 and RC-B to [`0133-llm-upstreams-and-cost-budgets`](../0133-llm-upstreams-and-cost-budgets/change.md).
- [`0133-llm-upstreams-and-cost-budgets`](../0133-llm-upstreams-and-cost-budgets/change.md): it takes over RC-B and O2. RC-B lands the §4.9 passthrough statement whose content "RC-5" states under this design's D2, including RCR-5 and its tier 1 test, the corrected retention facts, the isolation between users through forwarded identity attributes, and the residual risk within a shared upstream. It also carries the open validation items on Google Cloud of "Validation owed" in `steps.md`. RC-A removes `cachePolicy` and `cacheScope` from credential pools, whose LLM use that design removes, so the proposal that lands second rebases on the first.
- [`0142-security-oversight`](../0142-security-oversight/change.md): the regulated-tenant controls lose the cross-user cache refusal. No other control depends on it.
- Any design that cites `§12.8 step N` for N of 3 and above cites the number RC-A gives the step, or better, the step's store name. [`0121-billing-resource-attribution`](../0121-billing-resource-attribution/change.md) ("Erasure and teardown.", steps 5 and 15) and [`0133-llm-upstreams-and-cost-budgets`](../0133-llm-upstreams-and-cost-budgets/change.md) (the `llm_usage` erasure row and the removed lease step) are such designs.

## Sources

- Anthropic, "Prompt caching", https://platform.claude.com/docs/en/build-with-claude/prompt-caching, read 2026-11-09 (redirected from https://docs.claude.com/en/docs/build-with-claude/prompt-caching). It covers `cache_control`, the TTLs, the usage fields, the statement that `input_tokens` covers only tokens after the last breakpoint, the price multipliers, and workspace and organization isolation.
- OpenAI, "Prompt caching", https://developers.openai.com/api/docs/guides/prompt-caching, read 2026-11-09 (redirected from https://platform.openai.com/docs/guides/prompt-caching). It covers automatic caching, `prompt_cache_key`, retention, `cached_tokens`, and isolation across organizations. The page was read in part, so the claim about whether `prompt_tokens` includes cached tokens is not taken from it.
- Microsoft, "Prompt caching" for Azure OpenAI, https://learn.microsoft.com/en-us/azure/ai-foundry/openai/how-to/prompt-caching, read 2026-11-09. It covers automatic caching and isolation across subscriptions.
- Amazon Web Services, "Prompt caching for faster model inference", https://docs.aws.amazon.com/bedrock/latest/userguide/prompt-caching.html, read 2026-11-09. It covers `cache_control` in the InvokeModel body for Claude, the models that support top-level automatic caching, the `ValidationException` for other models, and the 5-minute and 1-hour lifetimes.
- Google Cloud, "Prompt caching" for Claude on Vertex AI, https://docs.cloud.google.com/vertex-ai/generative-ai/docs/partner-models/claude/prompt-caching, read 2026-11-09. It covers block-level `cache_control`, the 5-minute and 1-hour lifetimes, and isolation per Google Cloud project.
- Anthropic, "Claude Models on Google Cloud (Google managed)", https://platform.claude.com/docs/en/build-with-claude/claude-models-on-google-cloud/google-managed, read 2026-11-09. It lists prompt caching among the supported features and states no rule for a top-level `cache_control`.

Revision 3 reread the Anthropic and OpenAI prompt-caching pages on 2026-11-09 for the retention and isolation facts of "Provider prompt caching today", including Anthropic's statement that the legacy Bedrock integration returns a 400 for a top-level `cache_control`.
