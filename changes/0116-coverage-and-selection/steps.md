# Steps: 0116 Coverage and test selection from annotations

## Validation owed

| Item | Waits on it |
| :-- | :-- |
| The cost of selection by import graph. Replay the new selector and the change graph over the last 50 merges into `main`, and record per merge the packages each selects per tier and the test time of the merge-gate tiers, in particular the whole integration package. 0116.S3 ran the replay and recorded it in its digest ("Validation"): no merge loses a merge-gate tier, the integration package is selected for 26 of the 50 merges against 17, and it takes 396 to 494 seconds in the gate container. Decision open decision 3 asks whether the merge gate has a time budget for it. | The default bound of `run --changed` in 0116.S4, and decision O3. |
| The selections the import graph misses. For each `pkg/` package the change graph sends to a tier no import reaches (6 component, 34 contract, 21 integration, 13 security, and 1 chaos), find how the test reaches the package: a non-Go input gets a row in `tests/non-go-inputs.yaml`, and a test through a running binary is left to the gates. 0116.S3 measured the selector with every tier's build tag against the change graph's 106 `pkg/` keys that name one of those tiers: 19 of the 25 package and tier pairs it misses name packages that no longer exist, and the remaining six (one chaos, `pkg/admission/drain_readiness`, through the images the chaos tier deploys on Kind, and five integration, through the `cmd/lenny-gateway` and `cmd/lenny-ops` binaries the integration tests build) reach the package through a running binary, so no row is owed. | 0116.S6, which deletes the change graph. |
| Coverage equivalence. For the 17 spec-map keys with tests that the annotations do not cover, find whether the tests exist and what their annotations cite, and fix the annotation where it names the wrong section. | 0116.S6, which deletes the spec map. |
| The selection union. Since 0116.S1, `list --spec` and `run --spec` resolve each query as the union of the annotation index and `tests/spec-map.json`, because the index alone selected less than the map for 48 of its 231 keys (tiers lost, keys with no numbered heading, and a unit tier narrowed from `./...`). The union ends with the map: 0116.S6 removes the map's half of the union when it deletes the map, after the coverage-equivalence item above shows that the index selects at least what the map did for every key. The specification-edit selection of 0116.S3 reads the index alone: the change graph selected only the static and documentation tiers for an edit under `spec/`, and `tests/non-go-inputs.yaml` keeps the documentation tier, so the index adds tests and the union has nothing to add there. | 0116.S6, which deletes the spec map. |
| The 46 cited section numbers that name no heading, in 234 test citations. The coverage view lists them with their sites. | Nothing; each area change fixes the ones in its packages. |

## Steps

### 0116.S1 Annotation index and coverage view
- status: Landed
- lanes: [code, documentation]
- depends-on: [0115.S5, 0115.S7, 0115.S8]

The annotation index in `scripts/specshift/annotation` (CS-1), with `survey`'s site finder reading it. `lenny-test coverage --spec` over the index, the registry, the successor stubs, and the issues (CS-2). `list --spec` and `run --spec` resolving identifiers, page codes, and `§N.M` through the index, and the verdict's annotation lookup in `gotest_json.go` replaced. `TESTING.md` §5 and `cmd/lenny-test/README.md` describe the view. It needs the annotation parser of 0115.S5, the stub lookup of 0115.S7 for indirect coverage, and the site finder of 0115.S8.

### 0116.S2 Coverage gate
- status: Building
- lanes: [code, documentation]
- depends-on: [0116.S1]

`TestEveryChangedRequirementHasACitingTest` in `tests/static` (CS-3), with fixtures for each case under "Tests", and the "Gate" section of `.claude/rules/spec-annotations.md`. Decision O1 settles whether an issue satisfies the gate.

### 0116.S3 Selection from the import graph, specification edits, and non-Go inputs
- status: Building
- lanes: [code, documentation]
- depends-on: [0116.S1]

The selector of CS-4 and CS-5 in `cmd/lenny-test` (`changed_select.go`, `cmd_run_resolve.go`), with every tier's build tags in the `go list` call and the full closure of dependents by default. `tests/non-go-inputs.yaml` and its `validate` check (CS-6), with the rows the build finds by reading which tier packages open the chart, the compose files, the supervisor sources, and the Kind install scripts. `--pkg` resolves through the import graph. After this step no selector reads the change graph, and 0116.S6 deletes it. `TESTING.md`'s selection algorithm and `docs/testing/index.md` describe the sources. Whole packages run under every option of decision O3 except (b), which narrows the single-package tiers here.

### 0116.S4 Tier bounds by gate
- status: Planned
- lanes: [code, documentation]
- depends-on: [0116.S3, 0154.S2]

`--max-tier` removed, `--tier` taking a list, `--gate` bounding a selection by the gates 0154.S2 defines, and `run --changed` without a bound running the tiers that need no infrastructure (CS-7). `tests/groups.yaml`, `.github/workflows/reusable/lenny-test.yml`, `.claude/rules/test-coverage.md`, the `build` skill, and `TESTING.md` follow. The first validation item sets the default bound.

### 0116.S5 Claim register into issues
- status: Landed
- lanes: [specification, code, documentation]
- depends-on: [0115.S1]

The edits of CS-8 to the §28 lead, N4 of §28.1, §28.4, the §28.5 lead, §29.1, and `spec/README.md`, with the anchor move of §28.4; `.claude/rules/channel-naming.md`; the open claim rows filed as issues under the page codes 0115.S1 registers, each triaged against the v1 member changes; and the removal of `tests/claim-map.json`, `scripts/seed-claim-register.py`, the three claim-register test files, their entries in `gate_integrity_test.go`, and the claim-register reads of the tier-11 tests. Decision O2 settles whether the step files the rows now, freezes the file, or waits for the channel area.

### 0116.S6 Retire the maps, their registers, and their self-tests
- status: Planned
- lanes: [code, documentation]
- depends-on: [0116.S2, 0116.S3]

The retirements of CS-9: `tests/spec-map.json`, `tests/spec-map-exceptions.yaml`, `tests/spec-map-pending.txt`, `tests/change-graph.json`, `tests/change-graph-pending.txt`, the two change-graph registers and their residual class, `cmd/lenny-test/changegraph`, `cmd_validate_testdirs.go`, the map checks of `validate-maps` (renamed `validate`) with the annotation check that replaces `validateTestFilesMapped`, the nine category F files of tier 0, the spec-map conjunct of the heading walker, the spec-map read of `compliance_check_channel_naming_test.go`, `delegation_tree_spec_map_currency_test.go`, and the map steps of `scripts/refactor` and `scope.go`. The documentation pages under "Documentation" in `change.md` drop the maps. The second and third validation items land before the deletion.

### 0116.S7 Identifiers in test names
- status: Building
- lanes: [code, documentation]
- depends-on: [0115.S5]

The test-name check of CS-10 in `scripts/specshift/annotation`, its fixtures, the "Gate" list of `.claude/rules/spec-annotations.md`, and the area exit of the v1 program, which gains the renaming of each area's test names that carry a section number. It can share a branch with 0116.S1.
