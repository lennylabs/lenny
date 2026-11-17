# Decisions: 0132 LLM response cache removal

## Decisions

Settled by the owner on 2026-11-09:

| ID | Decision |
| :-- | :-- |
| D1 | Lenny's own LLM response cache is removed. In the owner's words: "Remove caching LLM response currently provided by Lenny." |
| D2 | Provider prompt caching passes through. The owner answered "ok" to the recommendation of revision 2's O1, which the adversarial review labelled C1, and the rule reads: Lenny never strips or rewrites `cache_control`. Provider cache isolation follows the LLM upstream's provider account or workspace, and [`0133-llm-upstreams-and-cost-budgets`](../0133-llm-upstreams-and-cost-budgets/change.md) defines upstreams, the posture report of tenants that share an upstream, and the warning when a shared upstream is granted to a `hipaa` or `fedramp` tenant. A regulated tenant gets its own upstream. Strict isolation between users within a tenant is not a Lenny feature: the proxy forwards identity attributes, such as tenant, user, and session, to the upstream, so an operator-run LLM gateway behind Lenny can choose a provider workspace or key per user, and the upstreams design specifies the attributes. The residual risk within a shared upstream stays documented: `cache_read_input_tokens` and latency let users detect each other's prompt prefixes ("RC-5"). The rejected options were stripping `cache_control` in the translators and a pool setting that allows or strips it. Stripping raises cost and latency for every runtime that uses the cache, and it would be incomplete, because OpenAI and Azure OpenAI cache automatically with no request field **[external]**. |

Moved:

| ID | Question | Options | Recommendation |
| :-- | :-- | :-- | :-- |
| O1 | Settled as D2. | | Revision 2 recorded the passthrough as open decision O1. |
| O2 | Moved. | | Revision 3 moves O2, the treatment of the provider's cached-token counts in Lenny's authoritative usage, to [`0133-llm-upstreams-and-cost-budgets`](../0133-llm-upstreams-and-cost-budgets/change.md), as the owner decided on 2026-11-09. Revision 2 recommended counting the sum of the uncached, cache-read, and cache-write tokens and rejected weighting by price; the owner's settlement of budgets in estimated cost by a per-upstream rate card changes that ground, and the moved decision is answered there. |

Settled by the owner on 2026-11-13, in the answers to the consolidated list of open decisions: "ok" to one rule for removed fields everywhere, recorded as D6 of [`0119-agent-runtimes-only`](../0119-agent-runtimes-only/decisions.md#decisions). A write that names a removed field is refused with `400 VALIDATION_ERROR` naming the field, so O3 takes option (b). The pool validator and the bootstrap seed refuse a pool write that names `cachePolicy` or `cacheScope` ("RC-2"). The options and reasoning, kept as a record:

| ID | Question | Options | Recommendation |
| :-- | :-- | :-- | :-- |
| O3 | How a pool write that still names `cachePolicy` or `cacheScope` is answered. The designs answer a removed field in different ways: this design lets the admin API ignore it as any unknown member, and decision O3 of [`0119-agent-runtimes-only`](../0119-agent-runtimes-only/decisions.md#decisions), settled on 2026-11-13, refuses a write that names a removed runtime field with `400 VALIDATION_ERROR` naming the field. | (a) Ignore the fields, as today. (b) Refuse a write that names either field with `400 VALIDATION_ERROR` whose `details.field` names it, in the admin API and the bootstrap seed. | (b), so one rule covers every field a design removes. It turns a silent no-op into an immediate error that names the field, at the cost of two names in the pool validator, and it adds no alias or second code path. **Settled (2026-11-13): (b).** |
