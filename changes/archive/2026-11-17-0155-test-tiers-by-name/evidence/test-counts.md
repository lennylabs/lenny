# Test counts before and after the rename

This record compares the test functions of each tier before change 0155 renamed the tier directories, split the static tier, and moved the unit tests of `tests/tier1_unit/` beside their code, and after all three steps.

## Method

`list_tests.py` reads every git-tracked `_test.go` file outside `testdata/` and records each top-level `Test`, `Fuzz`, `Example`, and `Benchmark` function, keyed by the file's path inside its tier directory.

- Before: `python3 list_tests.py <root> before tests-before.txt` on `main` at `90da9d66c`, with each tier at its numbered directory (`tests/tier0_static/` and so on). The integration tier counts `tests/integration/` as well, whose four files the rename merged with `tests/tier4_integration/`.
- After: `python3 list_tests.py <root> after tests-after.txt` on the branch after step S3. A tier is `tests/<name>/`, and the unit tier is the files `unit-moves.tsv` names, which `tests/tier1_unit/` held.

The two lists are `tests-before.txt` and `tests-after.txt`. A static-tier entry carries its new package directory after the split (`build/crds_test.go`), so the comparison below matches test function names within each tier rather than lines.

Every renamed and split package also compiles: `go vet` with each tier's build tag passes for every tier directory (`GOOS=linux` for the tiers whose helpers build Linux-only code), and `go vet` passes for each package a unit test moved into.

## Counts

| Tier | Former directory | Before | After | Difference |
|:--|:--|--:|--:|:--|
| `static` | `tests/tier0_static/` | 312 | 312 | None. The tests moved into `tests/static/{build,chart,lint,structure}/`. |
| `unit` (moved files) | `tests/tier1_unit/` | 18 | 18 | None. The tests moved beside the code they test. |
| `component` | `tests/tier2_component/` | 414 | 414 | None. |
| `contract` | `tests/tier3_contract/` | 574 | 574 | None. |
| `integration` | `tests/tier4_integration/` and `tests/integration/` | 324 | 324 | None. |
| `e2e_kind` | `tests/tier5_e2e_kind/` | 109 | 109 | None. |
| `e2e_cloud` | `tests/tier6_e2e_cloud/` | 70 | 70 | None. |
| `load_local` | `tests/tier7a_load_local/` | 131 | 131 | None. |
| `load_kind` | `tests/tier7b_load_kind/` | 24 | 24 | None. |
| `chaos` | `tests/tier8_chaos/` | 162 | 162 | None. |
| `security` | `tests/tier9_security/` | 238 | 238 | None. |
| `conformance` | `tests/tier10_conformance/` | 58 | 58 | None. |
| `docs` | `tests/tier11_docs/` | 457 | 458 | One added: `TestCurrentTierPathsRewritesOnlyRetiredTierDirectories` pins the translation through which the gap-record tests read the historical records' tier paths. |
| `load_cloud` | `tests/tier12_load_cloud/` | 14 | 14 | None. |
| Total | | 2905 | 2906 | |

Matched by name within each tier, the only difference between the two lists is the added `docs` test. No test was renamed, lost, or moved between tiers.

The static tier retires no gate: TESTING.md ("Static-tier packages") names, for each migration gate in `tests/static/structure/`, the change 0115 gate that covers it, and none is covered in full.

Two tests the change adds sit outside the tier directories: `TestStaticPackageChecksCoverEveryStaticPackage` and `TestStaticCheckOverBudgetIsReportedWithoutChangingItsStatus` in `cmd/lenny-test/static_budget_test.go`, which pin the static tier's package checks and their time budgets.
