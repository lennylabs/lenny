# Problem: Gateway exits at startup when the playground is enabled

## Statement

The gateway exits at startup whenever the playground is enabled (BUILD-GAPS F-27.8.1). The §27.8 Metrics table names the label of `lenny_playground_page_views_total` as `authMode`. It is the only camelCase metric label in any spec metric table. §16.1.1 Attribute Naming requires "Lenny-specific underscore-case names" where no OpenTelemetry convention exists, and it makes its attribute table the single source of truth for label names, with other domain labels documented inline at their point of use. Every label listed there is snake_case. §16.1.1 does not use the word "snake_case". That word comes from `pkg/observability/metrics`, whose package doc and label regex `^[a-z][a-z0-9_]*$` state the convention and attribute it to §16.1.1.

`pkg/gateway/mcpfabric/playground/metrics.go` copies the spec and registers the counter with the label `authMode` through `metrics.NewCounter`. The validator rejects it with `label "authMode" is not snake_case`. `cmd/lenny-gateway/httpsurface.go` calls `playground.NewMetrics` only inside the `--playground-enabled` block and answers any error with `log.Fatalf`, before the gateway listens. The two earlier fatal checks in that block reject only invalid configurations, so every valid configuration that enables the playground reaches `NewMetrics` and exits. The Helm chart passes `--playground-enabled` whenever `playground.enabled` is true. No other playground label (`runtime`, `outcome`, `reason`) fails the validator, so `authMode` is the only cause. No test calls `playground.NewMetrics` today, which is why the defect shipped.

Proposal 0095's tier-4 tenant deletion test skips its playground-bearer refusal case with `blocked: F-27.8.1` and keeps the playground disabled in its gateway arguments for that reason. 0095's deviations file records the skip.

The fix renames the §27.8 label to `auth_mode` and the matching string in `metrics.go`. The label values stay the `playground.authMode` values `dev`, `oidc`, and `apiKey`. The Helm value `playground.authMode` is a configuration key rather than a metric label and is unchanged. The proposal may also state the snake_case label-name rule explicitly in §16.1.1, because the spec currently implies it rather than stating it. No other site names the label: the §18 playground metrics catalog item lists metric names without labels, `docs/reference/metrics.md` has no `lenny_playground_page_views_total` row, and no chart template, dashboard, alert rule, or schema names the metric. Adding a docs metrics row is outside this fix.

The fix also adds a tier-1 test that calls `playground.NewMetrics` against a registry through the real validator. It removes the tier-4 skip and enables the playground (in dev mode, since the replicas already pass `--dev-mode`) in that test's replica arguments, so the playground-bearer refusal case runs. A different failure that surfaces in that re-enabled case is filed as a new finding and does not widen this proposal. The fix closes F-27.8.1 in BUILD-GAPS.md and updates the tier-11 currency test entries that pin its OPEN record.

The tier-11 baseline is already red. 0095's adjudication commit `e653a83ec` retitled the deviations entry to `## the tier-4 playground-bearer refusal case is skipped`, dropping the `Proposed:` prefix. The `secondHalfFilings` entry for F-27.8.1 in `tests/tier11_docs/build_gaps_data_lifecycle_records_currency_test.go` and the BUILD-GAPS F-27.8.1 deviation record still quote the old title, so `TestDataLifecycleSecondHalfFilingsAreFiledOnceWithTheirOwners` fails today for F-27.8.1. The fix repairs that entry and the BUILD-GAPS record against the current heading. The 0095 file is Implemented and is not edited. The sibling F-10.2.22 entry has the same drift. It belongs to F-10.2.22's own owner and is outside this fix, although the currency test stays red until it is repaired.

Relaxing the validator to accept camelCase, or making the registration failure non-fatal, is rejected. Either change would hide the spec outlier rather than correct it, and the validator's package doc treats a validation error as a programming error. Renaming only the code string would make the code disagree with §27.8, so the spec edit goes through this proposal.

## Evidence

- verified: spec/27_web-playground.md §27.8 Metrics, row `lenny_playground_page_views_total` | Counter | `authMode`.
- verified (wording corrected): spec/16_observability.md §16.1.1 Attribute Naming requires "Lenny-specific underscore-case names", makes its table the single source of truth for attribute names, and lists other domain labels in its **Other domain labels** paragraph, all snake_case. The section does not contain the word "snake_case"; a search of spec/ for it returns no hits.
- verified: pkg/observability/metrics/metrics.go package doc rule 3 ("Every Lenny-specific label name uses snake_case"), `labelNamePattern` `^[a-z][a-z0-9_]*$` with the comment citing §16.1.1, and `Validate` returning `label %q is not snake_case`.
- verified: pkg/gateway/mcpfabric/playground/metrics.go `NewMetrics` registers `lenny_playground_page_views_total` with `[]string{"authMode"}`.
- verified: cmd/lenny-gateway/httpsurface.go calls `playground.NewMetrics` inside `if *playgroundEnabled` and calls `log.Fatalf("lenny-gateway: §27.8 playground metrics: %v", err)` on error, before `runServers` starts listening.
- verified: charts/lenny/templates/gateway-deployment.yaml passes `--playground-enabled` when `.Values.playground.enabled` is true; charts/lenny/values.yaml defaults `playground.enabled` to false.
- verified: tests/tier4_integration/tenant_deletion_phase4_test.go keeps the playground disabled (doc comment on the replica arguments) and skips the `playground bearer` subtest with `blocked: F-27.8.1`.
- verified: tests/tier11_docs/build_gaps_data_lifecycle_records_currency_test.go pins F-27.8.1 in `secondHalfFilings` (with the stale `## Proposed:` deviation title), in the P5b plan-item want list, and in the T-12.8.9 annotation check. Running `TestDataLifecycleSecondHalfFilingsAreFiledOnceWithTheirOwners` fails today for F-27.8.1 and F-10.2.22 with "has no entry".
- verified: BUILD-GAPS.md F-27.8.1 is OPEN, and its deviation record quotes the stale title "Proposed: the tier-4 playground-bearer refusal case is skipped". TEST-GAPS.md carries the 0095 annotation under T-12.8.9.
- verified (title corrected): proposals/0095 deviations entry is now `## the tier-4 playground-bearer refusal case is skipped`, Status accepted, since commit `e653a83ec` ("proposal 0095: implemented; adjudicate deviations (all accepted)"). 0095 is Implemented and is not edited.
- verified: spec/18_build-sequence.md playground metrics catalog item cites §27.8 and lists metric names without labels.
- verified: no file under docs/, charts/, or schemas/ names `lenny_playground_page_views_total` or `page_views`.
- verified: no test in pkg/, cmd/, or tests/ calls `playground.NewMetrics`; the only test-file mention is a comment in pkg/gateway/mcpfabric/playground/user_invalidation_test.go.

## Who observes it

Every deployer or developer who enables the playground (§27). The gateway process exits before it serves anything, so a Helm deployment with `playground.enabled=true` crash-loops. The first-session tutorial (`docs/tutorials/first-session.md`) presents the web playground as Path B for a new user under `lenny up`, and the operator guide, installation guide, and configuration reference describe enabling it. Each of those paths ends in a gateway that does not start. A stock install leaves the playground disabled and does not hit the defect. The platform is not yet deployed, so no production users are affected.

## What breaks if nothing changes

- No configuration that enables the playground starts, so §27 cannot run in any configuration.
- The §12.8 property that a playground bearer minted before Phase 3 is refused from Phase 3 on stays unverified at tier 4, because the tier-4 tenant deletion test skips that case.
- F-27.8.1 stays OPEN, and the tier-11 currency test stays red on the stale deviation title.

The rename has no compatibility cost: no dashboard or alert rule references the metric, and the project is pre-deployment.

## Findings this unblocks

- F-27.8.1 (closed by this proposal).
- The tier-4 playground-bearer refusal case of `TestTenantDeletionPhase4ClearsArchivedTreesAndCheckpoints_spec_12_8` (TEST-GAPS T-12.8.9 annotation by proposal 0095).

## Prior art considered

- No other proposal stages this fix. The only proposals that name the metric or F-27.8.1 are 0098 and 0095's deviations file, which records the skip. The `authMode` hits in proposals 0002 and 0015 concern the `playground.authMode` configuration and `uiConfig`, not the metric label.
- No spec mechanism resolves it. §27.8 still names `authMode`, and `auth_mode` appears nowhere in spec/ or pkg/ as a metric label. `git log -S'auth_mode'` over spec and pkg finds only an unrelated workspaceplan commit.
- No code path avoids it. The validator has no label allowlist or exception, and the gateway treats the error as fatal.
- BUILD-GAPS F-27.8.1 suggests this same rename through the proposal pipeline, and the remediation plan lists F-27.8.1 as an unchecked item. These records queue the work and do not resolve it.

## Validated premises

- premise lens, verdict stands:
  - §27.8 names the label `authMode`. Confirmed (load-bearing).
  - §16.1.1 requires snake_case label names. Holds in substance: the section requires underscore-case names and makes its table the single source of truth; the word "snake_case" comes from the validator, which cites §16.1.1.
  - `metrics.go` registers `authMode` and `NewCounter` refuses it; `authMode` is the only failing playground label. Confirmed (load-bearing).
  - The registration error is fatal before listening and only under `--playground-enabled`; every valid enabled configuration reaches it. Confirmed (load-bearing).
  - No test calls `playground.NewMetrics`. Confirmed.
  - The tier-4 test skips the playground-bearer case and leaves the playground disabled. Confirmed; removing the skip also requires enabling the playground in the replica arguments.
  - F-27.8.1 is OPEN and pinned by the tier-11 currency test and the TEST-GAPS T-12.8.9 annotation. Confirmed.
  - The list of other sites that name the label is nearly empty: only §27.8 and `metrics.go`. `auth_mode` is not a row in the §16.1.1 table, consistent with how other §27.8 labels are treated.
- evidence lens, verdict revise:
  - Spec row, validator, code registration, fatal path, tier-4 skip, tier-11 pins, BUILD-GAPS record, TEST-GAPS annotation, and §18 item: verified.
  - Refuted detail: "§16.1.1 requires snake_case" is a paraphrase; the section says "underscore-case". Corrected in the statement.
  - Refuted detail: the 0095 deviations entry is no longer titled "Proposed: ...". Commit `e653a83ec` retitled it, the tier-11 entry and BUILD-GAPS record still quote the old title, and the currency test fails today for F-27.8.1 and F-10.2.22. Verified by running the test; the statement now requires repairing the entry against the current heading rather than assuming a green baseline.
- prior-art lens, verdict stands:
  - No proposal, spec mechanism, or code path already resolves the defect. BUILD-GAPS, the remediation plan, and TEST-GAPS queue it without resolving it.
  - The §18 item and docs name no label, so the edit sites are §27.8, `metrics.go`, and the tests.
- scope lens, verdict stands:
  - One problem with one root cause; splitting the spec rename from the code rename would leave the two in disagreement.
  - No other playground label fails the validator.
  - The tier-4 re-enable, the tier-1 regression test, and the F-27.8.1 closure belong to the same change. Grain risk: a different failure in the re-enabled tier-4 case becomes a new finding rather than widening this proposal. Recorded in the statement.
  - Some listed "other sites" (§18 labels, docs row, dashboards) do not exist; adding a docs row is new scope.
  - The proposal should quote the §16.1.1 sentence rather than paraphrase it.
- impact lens, verdict stands:
  - The defect is reachable and fatal; the chart passes `--playground-enabled` whenever `playground.enabled=true`, so every enabling deployment crash-loops (load-bearing).
  - Tutorial, operator, installation, and configuration docs lead readers into it; the chart default leaves the playground off.
  - The §12.8 playground-bearer refusal property is unverified at tier 4 while the defect stands.
  - The spec is the source of the conflict, so the fix goes through the proposal pipeline.
  - Limits: pre-deployment, opt-in feature, and no dashboard or alert references, so the rename has no compatibility cost.
- alternatives lens, verdict revise:
  - No reading makes this a no-change problem.
  - Relaxing the validator is rejected: §27.8 `authMode` is the only camelCase label in any spec metric table and in any metrics constructor call.
  - Making registration non-fatal, or renaming only the code, is rejected.
  - Refuted detail: §16.1.1 does not state "snake_case" explicitly; the proposal may make the rule explicit there. Recorded in the statement.
  - The site sweep is limited to one spec cell, one code string, the tests, and the BUILD-GAPS and tier-11 entries; `assets.go` passes values without naming the label.
