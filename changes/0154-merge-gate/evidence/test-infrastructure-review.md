# Test infrastructure review

Date: 2026-11-16. Branch at the time of measurement: `change/0115-spec-structure` (head `47747b903`).

This review covers the test tiers, the harness, the maps and registers, and the merge practice of the Lenny repository, and it answers four questions the owner asked. Every count below was measured on the tree with `git ls-files`, `grep`, `go test -json`, and the recorded run history, unless the text marks it as an estimate. Timings were taken on an Apple M3 Pro with 12 cores, Go 1.26.3, macOS.

## Summary of recommendations

**R1. Make the main line green and define a merge gate before any other test work.** Tier 0 fails today with four test failures. Three are environment failures on macOS, and one is a defect in the tree: commit `8ef08bfa8` (2026-11-07, a design edit) deleted `hack/boilerplate.go.txt`, which `make generate` and `TestDeepCopyInSyncWithGoTypes` need. The red tier hid that defect for nine days, because the build skill records a failure that also occurs on `main` as pre-existing. No rule states which tiers must pass before a merge, and the GitHub workflows have not produced a passing PR run since at least 2026-10-15. Define one merge gate (tier 0, tier 1 on changed packages, and the tiers `--changed` selects up to tier 4), run it in one canonical Linux environment, and refuse a merge whose gate verdict is not green.

**R2. Split tier 0 into durable checks and migration bookkeeping, and retire the bookkeeping as 0115 lands.** Of the 322 top-level tier-0 tests, 190 (59%) check specification citations, naming, comment wording, or the maps themselves. These encode the bookkeeping of the channel-naming migration and earlier proposals. The other 132 guard the build, generated artifacts, the Helm chart, code invariants, and test hygiene, and they are worth keeping. The bookkeeping half also dominates the time: one gate (`TestResidualGateCertifiesTheTree`) takes 35 of the 56 seconds the tier-0 Go tests take. The 0115 reference tool supersedes most of category E. Stop adding comment-wording gates now.

**R3. Retire the bespoke tier-11 reconciliation tests and the gap-record tests area by area.** Of the 137 tier-11 files, 75 are reconciliation tests written for one past fix, and 16 more check that `BUILD-GAPS.md` and `TEST-GAPS.md` entries stay as written. Sixty tier-11 files read a numbered specification file by heading and match literal phrases (687 `strings.Contains` calls). Each area migration will break the tests for that area, because the area replaces the matched paragraphs with successor stubs. Make deleting or replacing them part of each area's exit, and replace them with the generated catalogs and the documentation front-matter gates 0115 designs. Keep the general documentation gates.

**R4. Keep unit tests that test logic, and put new effort into component, store-conformance, and contract tests at the seams.** A seeded random sample of 80 of the 14,503 unit test functions found 50% testing logic and 27.5% already running in-process integration (httptest, bufconn gRPC, miniredis, envtest). Only 22.5% test wiring, fakes, trivia, or text. The unit tier is mostly useful. The weak spots are specific: store round trips through in-memory stores (41 store packages have both a memory and a Postgres implementation), and clients tested against hand-written servers. Published guidance does not support one universal ratio of test levels. Do not delete unit tests wholesale. Prune wiring tests when the v1 refactors (0117 and 0137.S5) move the code they pin, and add shared conformance suites that run each store interface against memory and Postgres.

**R5. Add a persona journey tier as a scheduled release check, and keep it out of the merge gate.** Agent-driven acceptance testing from documentation is an emerging practice with no established track record. The closest published evaluation reports about 60% correct verdicts. Its value for Lenny is that it tests what no other tier tests: whether an operator, tenant administrator, client, or runtime author can succeed by following the documentation. Make it trustworthy by having the harness check deterministic postconditions after the agent finishes, by running each journey several times, by recording full transcripts, and by having a human review every failure. Every product defect it finds becomes a deterministic test in a lower tier.

**R6. Let 0116 replace the hand-kept maps and most registers with data derived from annotations and the import graph.** `tests/spec-map.json` was touched by 363 of the 2,710 commits since 2026-09-01, close to two thirds as many as the 586 that touched `pkg/` or `cmd/`. Derive the coverage view from `// spec:` annotations, derive `--changed` selection from `go list -deps` plus annotations, and reduce the 20 register files to the few that record product exemptions or test hygiene. Fourteen of the 20 belong to the channel-naming and line-citation migration and retire when it closes.

**R7. Regroup the thirteen tiers into three gates.** Keep the tier names, which describe what a test needs, and add three gate definitions that say when tests run: the merge gate (R1), an area gate run at each milestone exit (tiers 5, 8 core, 9, 10, and 11), and a release gate (tiers 6, 7, 12, full 8, and the journey tier). Run the static checks concurrently and report all failures, instead of stopping at the first.

**R8. Order of work.** First the green main line and the merge gate (days). Then, during M1, the tier-0 split and the freeze on new bespoke text tests, followed by 0116 with the derived maps. During M3 and later, the per-area retirement of tier-11 and gap-record tests and the store conformance suites. The journey tier pilot starts after M1 with three journeys and grows one area at a time.

## Corrections to the earlier measurements

Several figures given earlier in the session do not match the tree:

| Earlier figure | Measured | Note |
| :-- | :-- | :-- |
| `tests/spec-map.json` about 687,000 lines | 686,944 bytes, 8,618 lines | The earlier figure was the byte count. The file maps 231 sections to 5,686 test references. |
| `tests/change-graph.json` about 73,000 lines | 73,645 bytes, 1,619 lines | 343 glob keys. |
| `tests/claim-map.json` about 38,000 lines | 38,543 bytes, 709 lines | 102 claims. |
| About 365,000 lines in the tier directories | 317,543 lines of Go | The earlier figure may have counted fixtures that are not Go. |
| About 527,000 lines of unit tests | 526,629 lines in 2,249 files under `pkg/`, `cmd/`, and `sdks/` | Confirmed. `scripts/` holds another 14,721 lines in 25 files. |
| `tests/testinfra/` about 50,000 lines | 49,667 lines in 309 Go files | Confirmed. |
| About 21 registers | 20 YAML registers and a README | Confirmed. |
| Tier 0 about 97 files and 315 test functions | 98 Go files, 81 outside `testdata/`; 322 top-level tests, of which 315 pass | The 315 is the passing count. |
| Tier 11 about 137 files | 137 files, 458 top-level tests | Confirmed. |
| Three tier-0 failures, all environment-specific on macOS | Four failures, three environmental and one a defect in the tree | See Question 1. |

## Question 1: test organization, and whether tier 0 is too heavy

### What tier 0 runs

`lenny-test --tier static` (`runStaticTier` in `cmd/lenny-test/cmd_run.go`) runs about twenty checks in sequence and stops at the first failure: `go vet` (twice), `buf lint`, `buf breaking`, `gofumpt`, `goimports`, `golangci-lint`, eleven shell scripts under `scripts/`, `go test ./tests/tier0_static/...`, `validate-diagnosis`, and `validate-maps`. Because it stops at the first failure, an environment failure early in the sequence hides every check after it.

The Go tests under `tests/tier0_static/` are 78 test files plus the `errorprop` analyzer package. I read the doc comment of every test function and sorted the files into categories:

| Category | Files | Lines | Tests | Guards |
| :-- | --: | --: | --: | :-- |
| A. Build and generated-artifact drift (CRDs, proto stubs, API codegen, schemas, SPDX headers, migration numbering, supervisor binary, CI tool staging, change records) | 10 | 2,325 | 33 | Defects in the tree. Keep. |
| B. Helm chart render and deployment boundary (flags parse against binaries, profiles, RBAC, adapter inputs rendered) | 17 | 5,708 | 53 | Defects in the tree. Keep. |
| C. Code-invariant lints over Go source (coordinator-hold allowlist, Redis cluster safety, erasure writers, production-mode carrier, dead code, retired JWT symbols, proto addressing, claim-to-proto agreement, teardown error propagation) | 11 | 2,255 | 22 | Defects in the tree. Keep. |
| H. Test-hygiene lints (ephemeral ports, host scrub, temp roots, skip reasons, tier-6 provider coverage, calibration scaffold) | 6 | 1,867 | 22 | Test-suite defects. Keep, but review the skip-reason baseline. |
| G. Specification-to-artifact sync (ops RBAC block, OpenSLO fragment) | 2 | 364 | 2 | Drift between the specification and the chart. Replace with the 0115 contribution-block gate. |
| D. Comment-wording and citation-wording gates (barrier, generation, and slot comments; proposal labels in comments; address-rule and degradation citations) | 13 | 2,451 | 28 | Wording that past proposals corrected. Retire. |
| E. Specification citation, naming, and migration gates (citation resolution, line-citation ratchet, fragment links, heading walker, identifier resolution, naming lint, residual gate, gate integrity, degradation matrix, claim register) | 13 | 9,095 | 114 | Migration infrastructure. Keep until the 0115 reference tool and each area's citation count at zero replace it. |
| F. Self-tests of `spec-map.json` and `change-graph.json` | 9 | 3,389 | 48 | The maps themselves. Retire with 0116. |

Categories A, B, C, and H (130 tests) and G (2 tests) guard defects in the tree, in the test suite, or between the specification and the chart. Categories D, E, and F (190 tests) guard the bookkeeping of earlier migrations and the maps. The 322 tests match the top-level count from `go test -json`. The classification is mine, from reading each test's doc comment; a different reader would move a few files between C and D.

### Time

- `go test -count=1 ./tests/tier0_static/...` took 56 s wall time on 2026-11-16.
- `TestResidualGateCertifiesTheTree` alone took 35 s. Five more tree-certifying gates of category E took 8.4 s, 8.4 s, 8.0 s, 6.9 s, and 2.3 s. Category E sets the wall time of the Go part of tier 0.
- The whole static tier took a median of 189 s and a maximum of 972 s over 173 static-only runs recorded in `tests/results/history.jsonl` between 2026-05-15 and 2026-07-19. The 20 verdict files on disk give a median of 209 s. No later history is on disk.
- The static tier failed 85 of its 230 recorded runs (37%) in that period, and the unit tier failed 29 of 59.

### Failures today

| Test | Cause | Kind |
| :-- | :-- | :-- |
| `TestDeepCopyInSyncWithGoTypes` | `open hack/boilerplate.go.txt: no such file or directory`. Commit `8ef08bfa8` ("designs: add allow-list mode to user credential request rules", 2026-11-07) deleted the file together with a design edit. `make generate` (`Makefile` line 79) needs it too. | Defect in the tree |
| `TestAPICodegenMatchesCommittedOutput` | The cgo link step fails against the macOS 27.2 SDK (`libSystem.B.tbd: unknown architecture arm64e.x1-macos`). | Host toolchain |
| `TestRuntimeSupervisorBinaryIsFreestandingStatic_spec_4_7_11` and its predicate test | The test builds a Linux freestanding static binary with flags the macOS linker rejects. | Test not portable to macOS |

Tier 11 does not link at all on this host with cgo enabled, for the same SDK reason. With `CGO_ENABLED=0` it runs in 25 s: 456 pass, 1 skip, and 1 failure (`TestEmbeddedManifestsMatchDevProfileRender_spec_17_4`, a drift between `pkg/embedded/manifests/manifests.yaml` and a fresh `helm template` render). I did not establish whether that drift comes from the local Helm version or from the tree.

### What breaks as 0115 and the area migrations move text

0115 itself changes no numbered heading, so the citation gates keep passing through it (0115 `change.md`, "Tests"). The breakage comes with the area migrations, which replace each migrated paragraph with a successor stub under the same heading.

| Reader of specification or record text | Tier 0 files | Tier 11 files |
| :-- | --: | --: |
| Reads a specification file | 20 | 65 |
| Names a numbered specification file (`NN_name.md`) | 13 | 60 |
| Holds a numbered heading literal such as `"### 8.6 "` | 6 | 30 |
| Reads `docs/` | 5 | 72 |
| Reads `BUILD-GAPS.md` or `TEST-GAPS.md` | 13 | 18 |
| Reads `spec-map.json`, `change-graph.json`, or `claim-map.json` | 16 | 5 |
| `strings.Contains` calls | 119 | 687 |

The typical tier-11 test calls `specSection(t, filepath.Join(specDir, "08_recursive-delegation.md"), "### 8.6 ")` (69 such calls) and then asserts that a phrase appears in the returned text. After an area migrates, the heading remains and the phrase is gone, so the test fails. Each area change will have to delete or rewrite the tests that read its sections. The 16 gap-record tests fail when the findings they name move to `issues/`, which the v1 program requires for each area's exit.

Outside tiers 0 and 11, 18 more test files in tiers 3, 4, 5, 8, 9, and 10 read specification text, and 18 files in `tests/testinfra/` name the gap records.

Two naming conventions in test function names also carry section numbers. 8,019 test functions end in `_spec_N_M`, and about 1,865 add a further number after the section. In five spot checks, four of those trailing numbers were the line on which the cited section stood in the specification file, and the fifth had drifted to a different section. These names are line citations under another spelling, which `spec-citations.md` forbids. They do not break any gate, but they go stale as text moves. Rename them in the area that owns them rather than in a tree-wide pass.

### Registers

The 20 YAML registers under `tests/registers/` fall into four groups:

| Group | Registers | Retires when |
| :-- | :-- | :-- |
| Channel-naming and line-citation migration | `anchor-senses`, `citation-document-senses`, `identifier-senses`, `reserved-phrase-senses`, `residual-anchors`, `residual-identifiers`, `residual-reserved-phrases`, `residual-line-citations`, `residual-line-citation-resolution`, `line-citations`, `line-citation-resolution`, `residual-generated-artifacts`, `pinned-spec-literals`, `proposal-label-comments` (14) | The migration closes and 0115's ratchet on old `§` citations takes over. |
| Product exemptions | `exceptions-deployment-boundary`, `deployment-boundary-defaults` (2) | Kept. |
| Test hygiene | `skip-reasons`, `residual-skip-reasons` (2) | Kept until the baseline is empty. |
| Harness | `change-graph-coverage`, `residual-change-graph-coverage` (2) | Replaced by a derived change graph (R6). |

`pinned-spec-literals.yaml` records the ordinal position of each string literal in tier-11 files that pins specification prose. A register keyed by the position of a literal inside a test file is maintenance cost with no product defect behind it.

### Answer

Tier 0 is heavier than it needs to be, and 59% of its tests guard bookkeeping instead of the product. It is also red, which makes it ineffective as a gate. The durable half (categories A, B, C, and H) is fast and catches defects such as a chart flag the binary does not define. The migration half (D, E, and F) and the bespoke tier-11 tests read specification prose, section numbers, and record wording, and they turn every text move into a test edit. These are change-detector tests in the sense of the Google Testing Blog: "Change detectors provide negative value ... These tests should be re-written or deleted" ([Eagle, 2015](https://testing.googleblog.com/2015/01/testing-on-toilet-change-detector-tests.html)). *Software Engineering at Google* defines a brittle test as "one that fails in the face of an unrelated change to production code that does not introduce any real bugs", and warns that with such tests "test maintenance will inevitably consume a larger and larger proportion of the team's time" ([ch. 12](https://abseil.io/resources/swe-book/html/ch12.html)).

Organize tier 0 as four Go packages that run in parallel: `build` (A), `chart` (B and G), `lint` (C and H), and `structure` (E, which 0115's gates replace). Delete D now, after confirming each comment it pins has been corrected in the tree. Delete F when 0116 lands. Give the Go part of tier 0 a time budget, for example 30 s on a developer machine, and treat a gate that exceeds it as a defect.

## Question 2: should tests focus on tiers 2 and above?

### What published sources say

No primary source supports one universal ratio of unit, integration, and end-to-end tests.

- Mike Cohn's pyramid puts unit tests at "the foundation of a solid test automation strategy" and asks for as little UI testing as possible, because UI tests are "brittle, expensive, and time consuming" ([Cohn](https://www.mountaingoatsoftware.com/blog/the-forgotten-layer-of-the-test-automation-pyramid)). Ham Vocke's restatement says "Push your tests as far down the test pyramid as you can" and "Every single test in your test suite is additional baggage and doesn't come for free" ([Vocke, 2018](https://martinfowler.com/articles/practical-test-pyramid.html)).
- Google suggested "a 70/20/10 split: 70% unit tests, 20% integration tests, and 10% end-to-end tests. The exact mix will be different for each team" ([Wacker, 2015](https://testing.googleblog.com/2015/04/just-say-no-to-more-end-to-end-tests.html)). *Software Engineering at Google* gives about 80% narrow unit tests, 15% medium, and 5% end-to-end, and defines test size by resources (network, processes, time) rather than by code scope ([ch. 11](https://abseil.io/resources/swe-book/html/ch11.html); [Test Sizes, 2010](https://testing.googleblog.com/2010/12/test-sizes.html)).
- Kent C. Dodds' testing trophy (static, unit, integration, end to end) puts most weight on integration tests, because they "strike a great balance on the trade-offs between confidence and speed/expense". He also writes that "When you mock something you're removing all confidence in the integration between what you're testing and what's being mocked" ([Dodds, 2017](https://kentcdodds.com/blog/write-tests); [Dodds, 2021](https://kentcdodds.com/blog/the-testing-trophy-and-testing-classifications)).
- Spotify's testing honeycomb for microservices favors integration tests, keeps implementation-detail tests for code that is "naturally isolated and have an internal complexity of their own", and calls the pyramid "actively harmful" for microservices ([Spotify, 2018](https://engineering.atspotify.com/2018/01/testing-of-microservices)).
- Martin Fowler notes that "unit" has no agreed meaning (sociable or solitary), which "makes the pyramid versus honeycomb discussion moot", and quotes Justin Searls: "People love debating what percentage of which type of tests to write, but it's a distraction" ([Fowler, 2021](https://martinfowler.com/articles/2021-test-shapes.html)).

The empirical evidence is narrower:

- Trautsch, Herbold, and Grabowski classified 38,782 Java tests and assessed them with mutation testing. They "could not find any evidence that one test type is more capable of detecting certain defect types than the other one" ([2019](https://www.swe.informatik.uni-goettingen.de/node/2878)).
- Inozemtseva and Holmes found "low to moderate correlation between coverage and effectiveness when the number of test cases in the suite is controlled for", and concluded that coverage "should not be used as a quality target" ([ICSE 2014](https://cs.ubc.ca/~rtholmes/papers/icse_2014_inozemtseva.pdf)).
- Yuan et al. studied 198 failures in Cassandra, HBase, HDFS, MapReduce, and Redis. 92% of catastrophic failures came from incorrect handling of non-fatal errors, and in 58% the fault "could easily have been detected through simple testing of error handling code" ([OSDI 2014](https://www.usenix.org/conference/osdi14/technical-sessions/presentation/yuan); [;login: summary](https://www.usenix.org/publications/login/feb15/yuan); [review](https://neverworkintheory.org/2014/10/09/simple-testing-can-prevent-most-critical-failures.html)). A secondary summary reports that 77% of the failures can be reproduced by a unit test ([Morning Paper](https://blog.acolyer.org/2016/10/06/simple-testing-can-prevent-most-critical-failures/)); I did not confirm that figure in the paper.
- *Software Engineering at Google* states that "configuration changes are the number one reason for our major outages" and that "Unit tests alone cannot verify this compatibility" ([ch. 14](https://abseil.io/resources/swe-book/html/ch14.html)). The same chapter records that larger tests are flakier and more expensive to maintain, and that they need documented owners.
- Capers Jones reports that "Most forms of testing are only about 35 percent efficient" at removing defects, static analysis averages about 55%, and formal inspections about 85% ([CIO Review, 2016](https://www.cioreview.com/leadership-perspectives/software-quality-in-2016-the-state-of-the-art-nid-15138-cid-112.html)).
- Google's mutation testing reached "6,000 engineers in Google on all code changes they author or review" ([Petrović and Ivanković, 2018](https://research.google/pubs/state-of-mutation-testing-at-google/)), and a study of almost 15 million mutants found that developers exposed to it write more and better tests, and that mutants couple with faults found in production ([Petrović et al., 2021](https://arxiv.org/abs/2103.07189)).

Taken together, the sources favor tests that resemble use at the seams where components meet, unit tests for isolated logic with internal complexity, explicit tests of error handling, and a measure of test value stronger than line coverage. The ratio between levels matters less than whether each test fails only for a defect.

### What Lenny's unit tests are

| Measure | Value |
| :-- | :-- |
| Unit test files beside the code (`pkg/`, `cmd/`, `sdks/`) | 2,249 |
| Lines | 526,629, against 445,807 lines of non-test code (ratio 1.18) |
| Test functions | 14,503 |
| `pkg/gateway` | 287,993 test lines against 199,323 code lines (ratio 1.44) |
| Black-box files (`package x_test`) / white-box files (`package x`) | 1,149 / 1,100 |
| Files using `httptest` / hand-written fake types / client-go or controller-runtime fake clients / miniredis / envtest / bufconn or a gRPC server | 437 / 342 / 88 / 83 / 55 / 46 |
| Files using gomock or mockery | 0 |
| Files over 1,000 lines | 41 (largest: `pkg/gateway/podlifecycle/podsession/binder_test.go`, 3,277 lines) |

Many of these tests are medium-sized in Google's terms: they open local ports, run miniredis, or start envtest. The tier-1 label covers much more than pure functions.

A subagent classified a seeded random sample of 80 test functions (seed `20261116`, listed in the session scratchpad) by reading each one:

| Category | Count | Share |
| :-- | --: | --: |
| Logic: asserts the computed outcome of a decision, parser, state transition, or policy | 40 | 50.0% |
| In-process integration: drives a handler, server, or reconciler with high-fidelity collaborators | 22 | 27.5% |
| Wiring: asserts a value crosses one seam | 8 | 10.0% |
| Interaction with a fake: asserts recorded calls | 4 | 5.0% |
| Trivial: nil-safety, constants, or a panic check | 4 | 5.0% |
| Text scanning | 2 | 2.5% |

In the same sample, 51 tests (64%) would catch a realistic regression, 26 (32%) would catch part of one, and 3 would catch none. The median test is 21 lines. 33 of the 80 carry a `// spec:` annotation in the required place, and 24 have no specification reference at all, so the annotation convention is not yet uniform enough for 0116 to rely on without a backfill or a ratchet.

The sample points to specific weaknesses:

- **In-memory store round trips.** `TestMemoryRoundTripsSDKWarmConfig_spec_6_1` (`pkg/gateway/runtime/poolstore/sdk_warm_config_test.go:65`) and `TestRuntimeToolCapabilityOverridesRoundTrip` (`pkg/gateway/externalapi/admin/runtimes_test.go:704`) pass even if the Postgres store drops the column. 53 packages under `pkg/` define `NewMemory`, and 41 of them also have a Postgres implementation. No test file names both a `NewMemory` and a `NewPostgres` constructor, which suggests that no shared suite runs one set of cases against both. A suite parameterized another way would not show in this count.
- **Clients tested against hand-written servers.** `pkg/ctlcli/ops_test.go:108` asserts a method and path against a canned server, `pkg/embedded/localcli/attach_test.go:180` drives a fake gateway, and `cmd/lenny-ctl/runtimescaffold/scaffold_test.go:201` checks for a `"labels:"` substring instead of calling the admin validator it describes. Both sides of each pair can drift together.
- **One metric chain tested seam by seam.** `lenny_session_expiry_total` is asserted in four unit files (the watchdog calls a notifier, the session server copies labels into a callback, the exposition carries the name, and the catalog lists it) plus one tier-2 test that covers the whole chain. The tier-2 test makes the four seam tests redundant.
- **Weak assertions on expensive fixtures.** `TestReconcileWithoutEventsIsSilent` (`pkg/controller/warmpool/poolphase_test.go:189`) starts envtest and asserts only `err == nil`, although its comment says it checks that no events are emitted.

### Answer

Unit tests in Lenny are useful: about three quarters of the sample test logic or in-process behavior and would catch regressions. Moving effort wholesale to tiers 2 and above would discard that value and slow feedback. The owner's concern is right in three places, and the response is targeted:

1. **Seams.** Add one conformance suite per store interface, run against the memory and the Postgres implementation (the Postgres half in tier 2 with the existing container profile). Add contract tests that run `pkg/ctlcli` and `pkg/embedded/localcli` against the production gateway and ops routers. These replace round-trip and canned-server unit tests as they are written.
2. **Wiring tests and the v1 refactors.** 0117 moves gateway packages, and 0137.S5 renames kinds across 463 Go files. White-box tests (1,100 files) move and break with the code they pin. When a refactor moves a package, delete its single-seam wiring tests where a tier-2 or tier-4 test covers the chain, instead of porting them.
3. **The coverage target.** `.claude/rules/test-coverage.md` requires 80% line coverage on new code. The evidence that coverage is a weak target, and Dodds' "diminishing returns ... much beyond 70%", support turning the line target into a floor that reviewers can waive for wiring, and adding mutation testing as the measure of test value on critical packages. `lenny-test mutation` already exists (70% kill-rate default, 85% for `pkg/audit`, `pkg/circuitbreaker`, `pkg/idempotency`, and `pkg/quota`), and I found no recorded run of it.

The tier rule in `test-coverage.md` ("Always run tier 0 and tier 1 on every touched package") is sound. The rule that needs changing is the implied requirement to write a tier-1 test for every change; for a change at a seam, a tier-2 or tier-3 test should satisfy it.

## Question 3: an agentic end-to-end validation tier

### What exists in practice

Three kinds of practice come close:

- **Executable documentation, deterministic.** Python doctest checks "that a module's docstrings are up-to-date by verifying that all interactive examples still work as documented" ([doctest](https://docs.python.org/3/library/doctest.html)); rustdoc runs documentation examples as tests ([rustdoc](https://doc.rust-lang.org/rustdoc/write-documentation/documentation-tests.html)); Go examples are "compiled (and optionally executed) as part of a package's test suite" ([Gerrand, 2015](https://go.dev/blog/examples)). Doc Detective "performs your instructions step-by-step, just like your users would" ([Doc Detective](https://docs.doc-detective.com/)), and Microsoft's Innovation Engine executes Azure documentation so that "our documentation is also part of our test suite" ([Innovation Engine](https://github.com/Azure/InnovationEngine)). Runme makes Markdown runbooks executable ([Runme](https://docs.runme.dev/)). Lenny already has three such tests in tier 11: `TestTimeToHelloWorld`, `TestRuntimeAuthorQuickStart`, and `TestOperatorInstallNonInteractive` (`tests/tier11_docs/time_to_hello_world_test.go`).
- **Journeys and exploratory testing by people.** Google's SRE workbook defines a critical user journey as "a sequence of tasks that is a core part of a given user's experience" ([SRE workbook](https://sre.google/workbook/implementing-slos/)). Microsoft's bug bashes assign people end-user scenarios; one team saw bugs opened jump to "400% of the daily average", and the bugs found were "on the seams", "highly unlikely to be found in test automation" ([Rowe, 2009](https://learn.microsoft.com/en-us/archive/blogs/steverowe/why-we-conduct-bug-bashes)). Behaviour-driven development writes scenarios as "an executable specification" ([Cucumber](https://cucumber.io/docs/bdd/)), and Fowler adds that "Tests are always going to be incomplete" ([Fowler, 2004](https://martinfowler.com/bliki/SpecificationByExample.html)).
- **LLM agents as testers.** Chevrot et al. evaluated web agents that execute natural-language test cases against running applications; the best "obtains around 60% of correct verdict and up to a promising 94% specificity" ([2025](https://arxiv.org/abs/2504.01495)). GPTDroid reached 71% activity coverage on 86 Android apps and found 48 new bugs ([Liu et al.](https://arxiv.org/abs/2305.09434)). Playwright's test agents have a planner that explores the application and writes a Markdown plan, a generator that turns the plan into deterministic tests, and a healer; the documentation warns that "Generated tests may include initial errors" ([Playwright](https://playwright.dev/docs/test-agents)). At Meta, LLM-generated tests are kept only when they build, pass repeatedly, and add coverage (TestGen-LLM: 57% "passed reliably", 73% of recommendations accepted) or kill a targeted mutant (ACH) ([Alshahwan et al., 2024](https://arxiv.org/abs/2402.09171); [Foster et al., 2025](https://arxiv.org/abs/2501.12862)).

The pattern in every production use is the same: the model explores and proposes, and a deterministic check decides. LLM output is also nondeterministic even at temperature 0 ([Ouyang et al., 2023](https://arxiv.org/abs/2308.02828)), and LLM judges show position, verbosity, and self-enhancement biases while reaching "over 80% agreement" with humans on chat evaluation ([Zheng et al., 2023](https://arxiv.org/abs/2306.05685); [Wang et al., 2023](https://arxiv.org/abs/2305.17926)). Anthropic's guidance on agent evaluation recommends combining code-based, model-based, and human graders, running multiple trials, reporting pass^k ("the probability that all k trials succeed"), and reading transcripts ([Anthropic, 2026](https://www.anthropic.com/engineering/demystifying-evals-for-ai-agents)). I found no rigorous published data on the cost of agent-driven end-to-end testing.

### Proposed design for Lenny

**Purpose.** The tier answers one question for each persona: can a person who has only the documentation achieve the goal, and does the system behave as the documentation says? It finds missing steps, wrong commands, unhelpful error messages, and behavior that differs from the documentation. The deterministic tiers do not test any of these.

**Personas and first journeys.** The tutorials under `docs/tutorials/` (22 pages) and the persona guides (`docs/operator-guide/`, `docs/client-guide/`, `docs/runtime-author-guide/`) supply the first scenarios:

| Persona | First journeys | Source pages |
| :-- | :-- | :-- |
| Operator | Install on Kind with `lenny-ctl`, configure a pool, enable TLS, upgrade, and recover from a failed upgrade | `tutorials/deploy-to-cluster.md`, `operator-guide/installation.md`, `operator-guide/upgrades.md` |
| Tenant administrator | Create a tenant, register a runtime, set quotas, and deliver a user credential | `tutorials/multi-tenant-setup.md`, `tutorials/user-credentials.md` |
| Client | Start a session over REST and MCP, send messages, suspend and resume, and derive a session | `tutorials/first-session.md`, `tutorials/mcp-client-integration.md`, `tutorials/session-derive-replay.md` |
| Runtime author | Scaffold, build, validate, register, and pass conformance | `tutorials/scaffold-a-runtime.md`, `tutorials/build-a-runtime.md` |
| On-call operator | Receive an induced alert and follow its runbook to recovery | `docs/runbooks/` (113 pages) |

The walkthrough pages 0115 plans under `spec/overview/walkthroughs/` (for example `session-start.md`, `checkpoint-capture.md`, and `gateway-drain.md`) supply the expected outcomes for client journeys, because each step names a requirement and an operation.

**Where scenarios live.** `tests/journeys/<persona>/<journey>.md`, one file per journey, each with front matter naming the persona, the environment profile (`kind` first, `cloud` later), the documentation pages the agent starts from, and the requirement identifiers the journey exercises. The body states the goal in the persona's words, the preconditions, and a list of postconditions. Each postcondition is a command the harness runs after the agent finishes (an API call, a `kubectl` query, or a metric read) with its expected result. An agent may draft a scenario from a tutorial or a walkthrough page, and a person reviews and commits it.

**How a run works.** `lenny-test --tier journeys` installs the chart on a fresh Kind cluster from the tree under test, then starts one agent per journey with a fixed toolset: a shell holding `lenny-ctl`, `kubectl`, `curl`, and an MCP client, and read access to `docs/` and nothing else. The agent does not read `spec/`, `pkg/`, or the tests, because a user cannot. When the agent declares the goal reached or blocked, the harness runs the postconditions.

**What a run records.** `tests/results/journeys/<run-id>/<journey>/` holds the full transcript (every command, its output, and every documentation page the agent read), the agent's report, and the postcondition results. The verdict is one of `pass` (every postcondition holds), `product-defect` (a postcondition fails or the system contradicts the documentation), `doc-defect` (the agent needed knowledge the documentation does not give, or a documented command is wrong), `blocked` (environment), or `inconclusive`. Each finding names the documentation page and heading and quotes the transcript lines that support it.

**What makes a result trustworthy.**

1. The verdict comes from deterministic postconditions run by the harness. The agent's judgment classifies a failure; it never turns a failed postcondition into a pass.
2. Each journey runs three times per run, and a journey passes only when all three pass (pass^3). A journey that passes in some trials and fails in others is reported as unstable, which itself points to an ambiguous page or a race.
3. A second model reviews each transcript against the scenario and the documentation and must cite transcript lines for every claim. Disagreement with the first agent's report sends the journey to human review.
4. A person reviews every non-pass finding before it becomes an issue, and reads a sample of passing transcripts each run.
5. Every confirmed product defect gets a deterministic regression test in tiers 3 to 5 before the issue closes, so the journey tier finds problems and the deterministic tiers hold the fix.
6. The scenario, the documentation, the chart, and the model version are pinned by commit and recorded in the run, so a result can be reproduced.

**How often.** At the exit of each area milestone (M3 and later) for that area's journeys, and weekly on `main` for all journeys. It never runs in the merge gate, because it is slow and nondeterministic; *Software Engineering at Google* keeps presubmit to "only fast, reliable ones" ([ch. 23](https://abseil.io/resources/swe-book/html/ch23.html)).

**Cost (estimate).** I have no measured cost. A journey of the size above takes an estimated 30 to 80 agent tool calls and 10 to 30 minutes of Kind cluster time, and three trials triple both. With 15 journeys, a full weekly run is an estimated 45 agent sessions and 8 to 20 cluster-hours, run in parallel on separate clusters or in sequence on one. The pilot should measure tokens and cluster time per journey and set a budget cap per run before the tier grows.

**Pilot.** Start after M1 with three journeys: the operator install on Kind, the client first session, and the runtime-author scaffold to conformance. These overlap the three scripted tier-11 tests, so the pilot can compare what the agent finds with what the scripts check.

## Question 4: recommendations for the infrastructure as a whole

### Keep

- The tier names and the tier directories for tiers 2 to 10 and 12. Higher tiers carry few skip-only stubs (17 of 2,090 top-level tests in tiers 2 to 10 open with an unconditional `t.Skip`), and their recorded pass rates are high (tier 3 passed all 36 recorded runs).
- The `// diagnosis:` convention for tier 2 and above (2,815 annotations).
- The environment preflight in `test-coverage.md`, which records lessons from failures that cost hours.
- Tier-0 categories A, B, C, and H, and the general tier-11 documentation gates (`docs_test.go`, `code_blocks_test.go`, `runbooks_test.go`, `runbook_steps_test.go`, `alert_runbook_test.go`, `adr_test.go`, `time_to_hello_world_test.go`, and similar; 17 files and 45 tests).

### Cut

- Tier-0 category D (13 files, 28 tests) now, and category F (9 files, 48 tests) with 0116.
- Tier-0 category E and the 20 tier-11 files that check specification-internal pointers and structure, as 0115's reference tool and gates replace them.
- The 75 bespoke tier-11 reconciliation files and the 16 gap-record files, area by area.
- The 14 migration registers, when the channel-naming migration closes.
- `tests/tier1_unit/` (13 files, 1,362 lines). Its tests belong beside the code they test, which is where the other 14,503 unit tests already sit.

### Merge

- Tier 0 into four parallel packages (`build`, `chart`, `lint`, and `structure`), with the static checks run concurrently and all failures reported.
- The tier-11 catalog cross-checks (9 files) into the 0115 generators: once settings, metrics, and alerts are definition blocks, the reference pages are generated and the cross-check becomes the generator's own "is current" gate.

### Add

- The merge gate and one canonical Linux environment for it (R1).
- Store conformance suites and router contract tests (Question 2).
- The journey tier (Question 3).
- A scheduled mutation run on the critical packages, reported per package.

### Tier boundaries and gates

| Gate | When | Tiers | Must be green to |
| :-- | :-- | :-- | :-- |
| Merge | Every branch before it merges | 0, 1 on changed packages and dependents, 2 to 4 as `--changed` selects, 11 general gates | Merge into `main` |
| Area | Each M3+ milestone exit | 5, 8 core, 9, 10, and 11 for the area, plus the area's journeys | Close the milestone |
| Release | Before a release, and weekly on `main` | 6, 7, 12, 8 full, and all journeys | Tag a release |

### Merge policy

1. The merge gate runs in one canonical environment: a Linux container image built from the repository, with pinned Go, Helm, `buf`, `controller-gen`, and C toolchain versions. Developers on macOS run the gate inside it. This removes the host-specific failures listed under Question 1.
2. A branch merges only when the gate verdict for the branch head is green. The digest records the verdict file's run ID.
3. `main` stays green. A failure on `main` is fixed or reverted before other merges proceed, following the Google norm that "strongly discourages committing any new work on top of known failing tests" ([ch. 23](https://abseil.io/resources/swe-book/html/ch23.html)). The build skill's rule that records a failure also present on `main` as pre-existing then applies only to tiers outside the merge gate.
4. A flaky test moves to quarantine with a registered reason and an owner, and the quarantine has a cap ([Fowler, 2011](https://martinfowler.com/articles/nonDeterminism.html); [Micco, 2016](https://testing.googleblog.com/2016/05/flaky-tests-at-google-and-how-we.html)). `tests/flake-budget.yaml` and the skip-reason register are the places to record it.
5. Repair `.github/workflows/pr.yml` to run the merge gate in the same container, or disable it until pull requests replace direct merges. In the 200 runs between 2026-10-15 and 2026-11-12, `pr` was cancelled 27 times and failed 4 times with no success, `nightly` failed 15 times, and `pre-release` failed 94 times in 0 s on pushes to a design branch. A workflow that always fails teaches everyone to ignore it.

### Registers and maps with 0116

- The coverage view (section or identifier to tests) is derived from `// spec:` annotations and generated on demand, with a gate that every added or modified requirement has a citing test, as 0115 already plans. `spec-map.json`, `spec-map-exceptions.yaml`, `spec-map-pending.txt`, and the tier-0 F tests retire.
- `--changed` selection is derived from the Go import graph (`go list -deps -test`) for Go sources, from annotations for specification pages, and from a short hand-kept list for non-Go inputs (chart templates, migrations, schemas). `change-graph.json` and its two coverage registers retire.
- `claim-map.json` claims become issues under `issues/`, and the file retires.
- The registers that remain record product exemptions (deployment boundary), test hygiene (skip reasons and quarantine), and the per-area ratchet of old `§` citations from 0115.
- The annotation lint 0115 adds should apply to test names as well, so new tests do not embed section or line numbers in their names.

### Order of work

1. **Now (days).** Restore `hack/boilerplate.go.txt`. Make the supervisor tests skip with a registered reason off Linux. Build the canonical gate container. Define the merge gate and write the policy into `test-coverage.md` and `git-workflow.md`. Make the static tier concurrent and report all failures. Delete tier-0 category D.
2. **M1, with 0115.** Freeze new bespoke tier-11 tests and comment-wording gates: a new documentation check is either a general gate or a generator check. Split tier 0 into its four packages. Retire category E gates as 0115's reference tool covers them.
3. **M1, 0116.** Derive coverage and selection, retire the maps and their self-tests, and consolidate the registers.
4. **M3 and later, per area.** Delete or replace the area's tier-11 reconciliation and gap-record tests, add store conformance suites for the area's stores, prune wiring tests in moved packages, rename tests carrying section or line numbers, and run the area's journeys at the milestone exit.
5. **After M1, in parallel.** Pilot the journey tier with three journeys and measure its cost. Add one or more journeys per area.

## Candidate changes

Numbers are left for the `change` skill to assign.

**Green main line and merge gate (M0 or M1).** Restore `hack/boilerplate.go.txt` and fix the tier-0 tests that cannot run on macOS. Add a container image that pins the gate's toolchain and a `lenny-test gate` command that runs the merge-gate tiers in it and writes a verdict. Make `runStaticTier` run its checks concurrently and report every failure. State the merge policy in `.claude/rules/test-coverage.md`, `.claude/rules/git-workflow.md`, and the build skill, including the rule that `main` stays green and the quarantine cap. Repair or disable `.github/workflows/pr.yml`. Delete tier-0 category D after confirming the comments it pins are correct. This change touches no specification behavior and falls under the escape hatch for test harnesses in `spec-driven-development.md`.

**Static and documentation tier restructure (M1, alongside 0115).** Split `tests/tier0_static/` into `build`, `chart`, `lint`, and `structure` packages with a time budget. Retire the category E gates as each is covered by a 0115 gate, and retire the 14 migration registers when the channel-naming migration closes. Add a rule that a new documentation check is either a general gate or a generator check, and that no test matches specification prose. Move `tests/tier1_unit/` beside the code. Amend the v1 program's M3+ exit to require that each area delete or replace its tier-11 reconciliation tests and its gap-record tests.

**0116 scope (M1).** Extend the planned scope of 0116 beyond coverage: derive `--changed` selection from the import graph and annotations, retire `change-graph.json`, `claim-map.json`, `spec-map-exceptions.yaml`, and their registers and self-tests, and extend the annotation lint to test names.

**Store conformance and router contracts (M3+, per area or as one change).** One conformance suite per store interface with both a memory and a Postgres implementation (41 packages), run in tier 1 against memory and in tier 2 against Postgres. Contract tests that run `pkg/ctlcli` and `pkg/embedded/localcli` against the production gateway and ops routers. Delete the round-trip and canned-server unit tests the suites replace.

**Persona journey tier (after M1).** Add `tests/journeys/`, the scenario format, the `journeys` tier in `lenny-test` with Kind provisioning, the restricted agent toolset, deterministic postconditions, three trials per journey, the transcript and verdict records under `tests/results/journeys/`, the review procedure, and three pilot journeys. Measure cost per journey and set a run budget before adding more. Name the tier in `TESTING.md` and in the release gate.

## Open questions for the owner

1. Which environment is canonical for the merge gate: a Linux container that macOS developers run the gate in, or the macOS host with every Linux-only test skipping off Linux?
2. Should `.github/workflows/pr.yml` be repaired now, or disabled until pull requests replace direct merges?
3. Is it acceptable to delete the tier-0 category D gates, the bespoke tier-11 reconciliation tests, and the gap-record tests without a one-for-one replacement? Some carry `// spec:` annotations, so the derived coverage view will show sections losing a test.
4. Should the 80% line-coverage target on new code stay a hard rule, become a floor that reviewers can waive for wiring, or give way to a mutation score on critical packages?
5. For the journey tier: may the agent read `spec/`, or only `docs/`? Does a failing journey block an area's milestone exit, or only open issues? What budget per run is acceptable?
6. Should the regrouping into merge, area, and release gates replace the "Which tiers a change must touch" list in `test-coverage.md`, or sit beside it?
7. Does the 2026-11-07 deletion of `hack/boilerplate.go.txt` indicate other files that commits on design branches removed by accident? A sweep of deletions in design-only commits since 2026-10-01 would answer it.
