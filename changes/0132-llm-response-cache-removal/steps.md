# Steps: 0132 LLM response cache removal

## Validation owed

| Item | Why it matters | Status |
| :-- | :-- | :-- |
| The metering items of revision 2: the usage field names on Bedrock InvokeModel and on Vertex, the streaming events that carry the cache counts, and whether OpenAI's totals include cached tokens | They moved with O2 and RC-B. | Moved to [`0133-llm-upstreams-and-cost-budgets`](../0133-llm-upstreams-and-cost-budgets/change.md). |
| Confirm whether Claude models on Google Cloud (Google managed) accept a top-level `cache_control` on `rawPredict` | RC-5 documents which providers refuse the top-level field. Google Cloud's prompt-caching page for Claude documents only the block placement. | Moved to "Validation owed" of [`0133-llm-upstreams-and-cost-budgets`](../0133-llm-upstreams-and-cost-budgets/steps.md), against 0133.S2 (LU-9), on 2026-11-19. Dropped there on 2026-11-19 under 0133's D10. |
| Reconcile Anthropic's statement of organization-level cache isolation on Google Cloud (Google managed) with Google Cloud's statement that a cache is unique to its project | RC-5's isolation statement names both. | Moved to "Validation owed" of [`0133-llm-upstreams-and-cost-budgets`](../0133-llm-upstreams-and-cost-budgets/steps.md), against 0133.S2 (LU-9), on 2026-11-19. Dropped there on 2026-11-19 under 0133's D10. |
| Count the `§12.8 step N` citations and confirm none cites step 2 for anything other than the cache | The renumbering sweep depends on it. Two code sites cite step 2, both for the cache **[source]**. | Settled from the tree on 2026-11-19: only `cmd/lenny-gateway/adminrouter.go` and `pkg/storage/erasure/semanticcache_wiring_test.go` cite step 2, and both cite it for the cache. The other citations name later steps, and the test functions whose names carry a step number are renamed as "RC-1" (**Erasure.**) states. |
| Confirm no deployer-facing document outside `docs/` describes `cachePolicy` | RCR-2. | Settled from the tree on 2026-11-19: outside `docs/`, `changes/`, and `proposals/`, the deployer-facing files that name `cachePolicy` or `cacheScope` are `charts/lenny/values.yaml` and `charts/lenny/tests/bootstrap_test.yaml`, which "RC-3" covers. The other files are the specification sections "RC-1" edits, the code and golden files "RC-2" edits, the two migrations, and the gap records. |
| Confirm whether the admin API should refuse unknown pool fields | "RC-2" refused the two removed fields by name. | Settled by O3 on 2026-11-13: the two removed fields are refused by name, and other unknown members keep the API's general handling. |

## Steps

### 0132.S1 Removal of the LLM response cache
- status: Planned
- aliases: [RC-A, RC-1, RC-2, RC-3, RC-4]
- lanes: [specification, code, documentation]
- closes: [T-4.9.15, T-12.1.1, T-12.2.5, F-12.8.53 (part)]

The removal: RC-1 to RC-4. It closes the cache half of F-12.8.53, which stays open for the billing buffer. CA-G (0123.S2) depends on 0132.S1, so the embedder never moves into `semanticcache`, and CA-G rebases its edits of `TESTING.md` §13.36, the Phase 17b successors, and the §12.8 `DeleteByUser` list on this step. 0132.S1 and 0133.S2 rebase on whichever of them lands first, because both edit `pkg/gateway/meteringproxy/llmproxy/handler.go`, `pkg/credential/credentialpoolstore`, and the admin pool validator. 0132.S1 can land first because it removes behaviour and adds none. The step deletes the three `removed-by: '0132'` rows of `tests/registers/gateway-layout.yaml` and the three package rows of `tests/registers/spec-section-citations.yaml`, so the `review` skill runs over its diff before the merge, as "Review changes to the merge gate" in `.claude/rules/git-workflow.md` requires. The refusal of the removed pool fields, its tests, and its documentation wait on open decision 1 for the error details and the bootstrap seed's answer, and whether the step edits the `source` values of §16.1 waits on open decision 2.

### Notes on the steps

RC-B moved to [`0133-llm-upstreams-and-cost-budgets`](../0133-llm-upstreams-and-cost-budgets/change.md), where 0133.S1 (LU-A) carries the usage count, the billing fields, and the closure of F-11.2.38, and 0133.S2 (LU-9) carries the §4.9 passthrough statement under this change's D2 ("RC-5") with RCR-5. It follows that change's own order, and the owner's settlement D2 for the passthrough statement. It is independent of 0132.S1. RC-B fixes a metering gap that exists with or without the semantic cache.
