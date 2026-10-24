# Summary: Rename the §27.8 page-views label to auth_mode so a playground-enabled gateway starts

## Summary

**Problem statement.** The gateway exits at startup in every valid configuration that enables the playground (BUILD-GAPS F-27.8.1). The §27.8 Metrics table names the label of `lenny_playground_page_views_total` as `authMode`, which contradicts the underscore-case label names §16.1.1 requires. The code copies the spec cell, the metric label validator refuses the label, and the gateway treats the refusal as fatal before it listens. Because of the same defect, proposal 0095's tier-4 tenant-deletion test skips its playground-bearer refusal case, so the §12.8 refusal of a playground bearer minted before Phase 3 has no verification. The tier-11 records currency test is also red on a stale deviation title in the F-27.8.1 records.

**What changes.**

- SPEC-1 changes the Labels cell of the `lenny_playground_page_views_total` row in §27.8 from `authMode` to `auth_mode`.
- CODE-1 registers the counter in `pkg/gateway/mcpfabric/playground/metrics.go` with `auth_mode`, and lands a tier-1 test that calls `NewMetrics` through the real validator.
- TEST-1 enables the dev-mode playground on the tier-4 tenant-deletion replicas and replaces the skipped playground-bearer case with minted playground-bearer probes.
- REC-1 closes F-27.8.1 in `BUILD-GAPS.md`, repairs the stale deviation title there and in the tier-11 currency test, annotates TEST-GAPS T-12.8.9, and ticks the remediation-plan item.

**Decisions.**

- D1. The fix is the minimal edit: one §27.8 cell and one code literal change from `authMode` to `auth_mode`. It adds no rule, field, code path, or mechanism, and the stances considered during drafting converge on this edit-site set.
- D2. The label values stay the `playground.authMode` values `dev`, `oidc`, and `apiKey`, because `pageView` already passes `string(mode)`. The Helm key `playground.authMode`, the uiConfig `authMode` JSON key, and the `authMode=dev` prose in the `lenny_playground_dev_tenant_not_seeded_total` row name configuration rather than the label, so they stay unchanged.
- D3. §16.1.1 is not edited. Its phrase "Lenny-specific underscore-case names elsewhere" already excludes `authMode`, and the validator already cites §16.1.1. The defect is the outlier §27.8 cell. An explicit clause would add a rule surface without resolving more of the problem.
- D4. The validator and the fatal startup path stay as they are. The rename makes the fatal path unreachable for every valid configuration, and the path stays fail-closed for a future label that violates the spec.
- D5. The tier-4 replicas need `--playground-dev-tenant-id=acme` in addition to `--playground-enabled` and `--playground-auth-mode=dev`. `Config.Validate` refuses dev mode under `--multi-tenant=true` while the dev tenant is `default`, so without the flag the gateway still exits at startup, with `LENNY_PLAYGROUND_DEV_TENANT_REQUIRED`. `--dev-mode` is already passed and satisfies the dev-mode backstop.
- D6. Each playground bearer is probed only on the replica that minted it. The playground handler signs with the per-replica Token Service signer, the same limitation `lennyIssuedProbes` already respects. F-10.2.22 owns the cross-replica gap.
- D7. The mint uses the dev-mode `POST /v1/playground/token` with body `{}`, after bootstrap seeds `acme` (which satisfies the §27.3 dev-tenant Ready gate) and before the DELETE. The probes join the existing `probes` slice, so the existing admitted and refused checks cover them with no new assertion code.
- D8. The tier-11 `secondHalfFilings` F-27.8.1 `deviation` value and the BUILD-GAPS deviation-record quote are rewritten to the current 0095 heading, because the problem statement requires both repaired. Once F-27.8.1 is closed, the currency test takes its closed-finding branch, checks only for a `**Resolution` note, and never reads the `deviation` field, so the shared `TrimPrefix` logic stays untouched.
- D9. The tier-1 test gathers the registry after one `pageView` and asserts the label name `auth_mode`. A nil-error assertion alone passes for any label the validator accepts and would not pin the §27.8 cell the test cites.
- D10. A proposal-0098 annotation goes under TEST-GAPS T-12.8.9, because the 0095 annotation says the playground-bearer case "is skipped pending F-27.8.1", which this change makes false. The 0095 annotation stays verbatim, so the tier-11 T-12.8.9 pin on `F-27.8.1` still matches. T-12.8.9 stays OPEN for its tier-5 residuals.
- D11. The records closure and the tier-11 entry edit land in one commit, so the closed-finding branch applies atomically. The tier-11 currency test then stays red on F-10.2.22 alone, which is outside this fix.

**Watch out for.**

- `--playground-dev-tenant-id=acme` is mandatory on the tier-4 replicas (D5). Dropping it reproduces a startup exit with a different error code, which reads like the original defect.
- Probing a playground bearer on the peer replica fails on F-10.2.22 rather than on anything this fix owns (D6).
- The playground-bearer mint must run after the bootstrap seed creates `acme` and before the DELETE. Minting earlier fails the dev-tenant Ready gate, and minting later does not test a bearer minted before Phase 3.
- A failure other than F-27.8.1 that surfaces in the re-enabled tier-4 case is filed as a new BUILD-GAPS finding. It does not widen 0098.
- Leave proposal 0095's deviations file and its TEST-GAPS annotation untouched. 0095 is Implemented, and the tier-11 T-12.8.9 pin matches the 0095 annotation text.

## Goals

- Every valid configuration that enables the playground starts the gateway.
- §27.8 and `metrics.go` name the same label, and the label satisfies §16.1.1.
- A tier-1 test fails if `NewMetrics` cannot register the §27.8 set through the real validator.
- The tier-4 tenant-deletion test verifies the §12.8 refusal of a playground bearer minted before Phase 3.
- F-27.8.1 is closed, and its records and the tier-11 entry match the current 0095 heading.

## Non-goals

- Deleting the label from `lenny_playground_page_views_total`. The label carries one value per installation (§27.3.1 "Mode enforcement is route-stamped"), but deletion changes the `Metrics` field type, the `pageView` signature, and the `assets.go` caller, and it removes a dimension §27.8 states. The rename is a one-literal edit at the same spec cell. A later proposal can revisit whether the dimension belongs in scrape-time external labels.
- Adding an explicit snake_case sentence or regex to §16.1.1 (D3).
- Updating the wording of rule 3 in the `pkg/observability/metrics` package doc, which depends on the declined §16.1.1 clause.
- Writing the `auth_mode` value set into the §27.8 row description. The values are unchanged, and listing them is new content outside the fix.
- Adding `auth_mode` to the §16.1.1 attribute table or its **Other domain labels** list. The other §27.8 labels (`runtime`, `outcome`, and `reason`) are documented inline at their point of use.
- Relaxing the validator, adding a label allowlist, or making the `NewMetrics` failure non-fatal. Each one hides the spec outlier, and a non-fatal failure fails open with the §27.8 metrics silently missing.
- Renaming only the code string, which would leave the code contradicting §27.8.
- Renaming the label values, for example `apiKey` to `api_key`. §16.1.1 governs names, and the values are the `playground.authMode` enumeration.
- A recording rule, relabel config, or dual-label transition. No `authMode` series ever existed, because registration failed before any collector was registered, and no dashboard or alert names the metric.
- Rerouting `newRevokeHandler` in `user_invalidation_test.go` through `NewMetrics`. The new tier-1 test covers the constructor.
- Table-driven assertions over every §27.8 family's label names, a duplicate-registration case, or a nil-receiver case in the tier-1 test. No other §27.8 label fails, and those cases go beyond pinning the defective cell.
- Generalizing the tier-11 `TrimPrefix` to strip `## ` and an optional `Proposed: `. The logic is shared with the F-10.2.22 entry, and the closed-finding branch never reads the field (D8).
- Dropping the F-27.8.1 `deviation` field from `secondHalfFilings`. The problem statement asks for the entry to be repaired against the current heading, and the entry still records the 0095 filing.
- Leaving the stale tier-11 string and the BUILD-GAPS quote as they are. The problem statement requires both repaired.
- Repairing the sibling F-10.2.22 `secondHalfFilings` entry, which belongs to F-10.2.22's owner.
- Editing proposal 0095's deviations file. 0095 is Implemented and immutable.
- Adding a `docs/reference/metrics.md` row for the playground metrics, which the problem statement places outside the fix.
- A new tier-11 lint that scans every spec metric table for label names that fail the regex. The validated problem does not exhibit a recurrence that needs it.
- Extending the §16.1 MetricCatalog or its catalog test to cover §27.8 labels. The catalog carries names only, so this would be a new catalog mechanism.
- A `cmd/lenny-gateway` startup smoke test that boots `main` with `--playground-enabled`. The tier-1 test pins the root cause, and the tier-4 replicas boot the gateway binary with the playground on.
- Probing a playground bearer on the peer replica (D6).
- Minting through the oidc cookie path, or adding a `globex` playground probe. The replicas already run `--dev-mode`, the dev mint needs no IdP fixture, and `devTenantId` binds one tenant per gateway.

## Open decisions for human to make

None at drafting. The review loops add entries here.

## Defects in the shipped tree that this proposal does not stage

- The tier-11 `secondHalfFilings` F-10.2.22 entry quotes `## Proposed: the tier-4 peer-replica refusal probe checks each Lenny-issued bearer only on its minting replica`, a title commit `e653a83ec` retitled in 0095's deviations file. `TestDataLifecycleSecondHalfFilingsAreFiledOnceWithTheirOwners` stays red on that subtest after this proposal lands. The entry belongs to F-10.2.22's owner.
- F-10.2.22: Lenny-issued and playground bearers verify only on the replica that minted them, because each replica signs with its own Token Service key. This proposal probes each playground bearer on its minting replica only and leaves the cross-replica gap to F-10.2.22.

## Impacts on other proposals

| Proposal | Status | What this change does to it | What it must do |
|:--|:--|:--|:--|
| 0095 | Implemented | Runs the tier-4 playground-bearer refusal case 0095 skipped, and makes 0095's TEST-GAPS T-12.8.9 statement that the case is skipped historical. | Nothing. 0095 is not edited, and its annotation stays verbatim. |

## Deliverable index

- SPEC-1 — spec/27_web-playground.md — Rename the `lenny_playground_page_views_total` label in §27.8 Metrics to `auth_mode`.
- CODE-1 — pkg/gateway/mcpfabric/playground/metrics.go — Register the counter with `auth_mode`, with its tier-1 test and spec-map entry.
- TEST-1 — tests/tier4_integration/tenant_deletion_phase4_test.go — Enable the dev-mode playground on the replicas and probe minted playground bearers.
- REC-1 — BUILD-GAPS.md, TEST-GAPS.md, gateway-runtime-comms-remediation.md, tests/tier11_docs/build_gaps_data_lifecycle_records_currency_test.go — Close F-27.8.1 and bring its records current.
