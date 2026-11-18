# Decisions: 0116 Coverage and test selection from annotations

## Open decisions

8. **The docs tier in an unbounded `run --changed` (0116.S4).** CS-7 says that `run --changed` without `--tier` or `--gate` runs "every tier the selection reaches that runs without infrastructure: static, unit, component, and contract". The docs tier also starts no compose stack and no cluster, and `tests/non-go-inputs.yaml` routes every edit under `docs/` and `spec/` to it, so the enumerated list and the criterion disagree for it. Under the list, a documentation-only edit runs the static tier, and the selection notes name the docs tier as reached and left out.
   - (a) Keep the list of CS-7: static, unit, component, and contract. The note names the docs tier, and `--tier docs` or `--gate merge` runs it.
   - (b) Add the docs tier to the unbounded run, because it needs no infrastructure. The whole docs tier runs for any edit under `docs/` or `spec/`, including the prose-matching tests that 0155 and the areas retire, and the host Helm render test that fails on darwin.
   - **Recommendation: (a).** It is what CS-7 states, the merge gate already runs the general documentation gates on every branch, and the rest of the docs tier is the set the areas are deleting. 0116.S4 builds (a); (b) is one entry in `infraFreeTiers` in `cmd/lenny-test/tier_bounds.go`.

9. **How a test from the component tier on says it verifies no requirement (0116.S6).** CS-9 has `validate` check that every test function in the tiers `validate-diagnosis` covers "carries a `// spec:` annotation or the comment the annotation rule asks of a test that verifies no requirement". `.claude/rules/spec-annotations.md` asks such a test to carry no annotation "and say why in a comment", and fixes no form a check can read.
   - (a) A phrase: the doc comment contains "verifies no requirement", read with the comment's lines joined and in any case, so the sentence that says why also satisfies the check.
   - (b) A marker line, such as `// spec: none (<reason>)`, which the annotation lint would have to accept as an item that is neither a section nor an identifier.
   - (c) No exemption: every test from the component tier on carries an annotation, and a test that verifies no requirement moves to the unit tier or to `tests/testinfra/`.
   - **Recommendation: (a).** It reads the sentence the rule already asks for, needs no change to the annotation lint, and no test in those tiers uses it today, so it binds nothing yet. 0116.S6 builds (a); the phrase is `noRequirementPhrase` in `cmd/lenny-test/cmd_validate_annotated.go`, and the rule's "Tests" bullet would name it if the owner keeps it.

11. **The scope of the infrastructure-free bound (0116.S4).** CS-7 states the bound for `run --changed`: without `--tier` or `--gate` it runs the static, unit, component, and contract tiers. The review of 0116.S4 found the first build applied it to `--spec` and `--pkg` too, so `--spec 13.2.3`, whose tests sit in the security tier, ran only the unit tier.
    - (a) Bound `--changed` and `--since` only; `--spec` and `--pkg` name their tests and run every tier they resolve.
    - (b) Bound every discovery selector, as the first build did, with the note naming what a gate or `--tier` would add.
    - **Recommendation: (a).** It is what CS-7 states, and a developer who names a section or a package asks for its tests. 0116.S4 builds (a) after the review; (b) is the `!s.changed` guard in `selector.boundDiscovery`.

## Decisions

Settled by the owner on 2026-11-17 ("go with all your recommendations, except ..."); the questions as framed are in `review-history.md`.

1. **Coverage gate.** A requirement a diff adds or modifies passes when a test cites it or an open issue names it.
2. **Claim register.** Its 41 rows that are not `WIRED` are filed as issues now, and `tests/claim-map.json` retires.
3. **Single-package tiers.** The integration and chaos tiers run whole when selected now, and are split per area as areas migrate.

Settled by the owner on 2026-11-18, after an elaboration of the open decisions 0116.S2, 0116.S3, and 0116.S7 raised, in the words "1. ok 2. ok 3. ok but let's not forget to incrementally split it during M3 4. ok". Each is settled on option (a); the questions as framed are in `review-history.md` and in the step digests.

4. **A move that rewrites only links.** The coverage gate keeps the exact comparison of a requirement's text, so a page move that rewrites a relative link inside a requirement reads as a text change and asks for a citing test or an open issue. TESTING.md ("Coverage gate") states it. Comparing link targets resolved from the repository root can follow when an area's move meets the case.
5. **Measuring the area exit for section-numbered test names.** No register. The area change counts the test names in its packages with the test-name lint's matcher when it closes, and the area's review checks the count. A count in the coverage view (`lenny-test coverage --spec`) can be added later if an area finds the count hard to take.
6. **The merge gate's time budget for the whole integration package.** The merge gate runs the whole integration package whenever the selection reaches it, with no time budget. The owner's condition: the package is split incrementally during M3. Each area milestone moves the area's integration tests into a Go package of their own, so the import graph selects only the affected parts. The v1 program's area exit carries the condition.
7. **The merge-gate review rule and the files that now decide the selection.** The rule in `.claude/rules/git-workflow.md` stays as it is. The selection reads the base commit's rows of `tests/non-go-inputs.yaml` beside the branch's, and a specification-edit selection only adds test functions and fails safe to the merge gate's tiers, so a branch can drop only the by-name additions of its own specification edits.

Settled by the owner on 2026-11-18 in the words "0116 decision 10: (a)", after an elaboration of the open decision 0116.S6 raised; the question as framed is in `review-history.md`.

10. **Ending the selection union without coverage equivalence.** `list --spec` and `run --spec` select what the annotations cite, and the spec map's half of the union ends with 0116.S6 although the coverage-equivalence item did not show that the index selects at least what the map did for every key. The measured loss stands in `evidence/coverage-equivalence.md`: 49 of 213 keys lose a tier and 15 select no test through the index. Every such test stays reachable through `--changed`, `--pkg`, and the merge gate's import-graph selection, and each area's re-annotation narrows the gap.
