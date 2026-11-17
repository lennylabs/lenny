# Steps: 0132 LLM response cache removal

## Validation owed

| Item | Why it matters | Status |
| :-- | :-- | :-- |
| The metering items of revision 2: the usage field names on Bedrock InvokeModel and on Vertex, the streaming events that carry the cache counts, and whether OpenAI's totals include cached tokens | They moved with O2 and RC-B. | Moved to [`0133-llm-upstreams-and-cost-budgets`](../0133-llm-upstreams-and-cost-budgets/change.md). |
| Confirm whether Claude models on Google Cloud (Google managed) accept a top-level `cache_control` on `rawPredict` | RC-5 documents which providers refuse the top-level field. Google Cloud's prompt-caching page for Claude documents only the block placement. | Open, for RC-B. |
| Reconcile Anthropic's statement of organization-level cache isolation on Google Cloud (Google managed) with Google Cloud's statement that a cache is unique to its project | RC-5's isolation statement names both. | Open, for RC-B. |
| Count the `§12.8 step N` citations and confirm none cites step 2 for anything other than the cache | The renumbering sweep depends on it. Two code sites cite step 2, both for the cache **[source]**. | Open, for the proposal. |
| Confirm no deployer-facing document outside `docs/` describes `cachePolicy` | RCR-2. | Open. |
| Confirm whether the admin API should refuse unknown pool fields | "RC-2" refused the two removed fields by name. | Settled by O3 on 2026-11-13: the two removed fields are refused by name, and other unknown members keep the API's general handling. |

## Steps

### 0132.S1 Removal of the LLM response cache
- status: Planned
- aliases: [RC-A, RC-1, RC-2, RC-3, RC-4]
- lanes: [specification, code, documentation]
- closes: [T-4.9.15, T-12.1.1, T-12.2.5, F-12.8.53 (part)]

The removal: RC-1 to RC-4. It closes the cache half of F-12.8.53, which stays open for the billing buffer. It rebases on CA-G (0123.S2) if CA-G lands first, and 0132.S1 and 0133.S2 rebase on whichever of them lands first, because both edit `handler.go`, `credentialpoolstore`, and the admin pool validator. 0132.S1 can land first because it removes behaviour and adds none.

### Notes on the steps

RC-B moved to [`0133-llm-upstreams-and-cost-budgets`](../0133-llm-upstreams-and-cost-budgets/change.md), where 0133.S1 (LU-A) carries the usage count, the billing fields, and the closure of F-11.2.38, and 0133.S2 (LU-9) carries the §4.9 passthrough statement under this change's D2 ("RC-5") with RCR-5. It follows that change's own order, and the owner's settlement D2 for the passthrough statement. It is independent of 0132.S1. RC-B fixes a metering gap that exists with or without the semantic cache.
