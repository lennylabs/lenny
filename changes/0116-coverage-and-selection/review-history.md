# Review history: 0116 Coverage and test selection from annotations

- 2026-11-16, revision 1. Created with the scope the v1 program gives it, widened to test selection by the test infrastructure review in 0154's evidence.
- 2026-11-16, revision 2. Framed from the test infrastructure review in 0154's evidence ("Registers", "Registers and maps with 0116", and "Candidate changes"), on 0115's annotation rule, registry, and annotation parser on `change/0115-spec-structure`. The current state was measured on the tree: the maps, their producers and readers, `--changed` selection, the import graph with every tier's build tags, and the annotations of 18,213 test functions. The design settles the index, the coverage view, selection from three sources, the removal of `--max-tier`, the claim register's move to issues, and the retirements with their replacements. Three decisions are open: what satisfies the coverage gate (O1), the claim register (O2), and selection inside the single-package tiers (O3). Steps 0116.S1 to 0116.S7 depend on 0115.S1, 0115.S5, 0115.S7, 0115.S8, and 0154.S2.

- 2026-11-17, revision 3. The owner settled the open decisions. Status `Decided`. The questions as framed before:

  **Open decisions before this revision**

  **O1. What satisfies the coverage gate.** The gate of CS-3 checks every requirement a branch adds or modifies. An area change writes its pages at the target state, so it adds requirements whose code steps have not landed and which no test can verify yet.

  - (a) Only a test whose annotation cites the requirement directly. An area change then lands only the requirements the tree already carries out and tests, or it waits for the code steps of every change touching the area.
  - (b) A test that cites the requirement directly, or an open or planned issue under `issues/<CODE>/` that names the requirement in its `requirements` list. A requirement with no test lands with a record of the gap, and the issue closes when the step that implements and tests it lands.
  - (c) As (b), and also a test that cites a numbered section whose successor stub lists the requirement.

  **Recommendation: (b).** It lets an area land at the target state, as the v1 program requires, while every untested requirement stays visible as an issue that a step has to close. (a) blocks area changes on unrelated code steps. (c) counts a citation that names a whole section as evidence for one requirement inside it, and the area's exit removes those citations anyway.

  **O2. The claim register.** `tests/claim-map.json` holds 102 rows for §28: 61 `WIRED`, 23 `UNWIRED`, and 18 `ABSENT`. §28.4 requires it, and 16 tier-0 tests and a generator hold it.

  - (a) In this change, file the 41 rows that are not `WIRED` as issues under the page code that will own each statement, triaged against the v1 member changes, retire the 61 `WIRED` rows with the file, and rewrite §28.4 to say that issues hold the record (CS-8).
  - (b) Freeze `tests/claim-map.json` as a historical record beside `BUILD-GAPS.md`, take it out of the gates, rewrite §28.4 the same way, and file its open rows when each channel page migrates.
  - (c) Keep the claim register and its gates until the channel area migrates, and retire only the spec map and the change graph here.

  **Recommendation: (a).** The 41 open rows are few enough to file once, each issue then carries the requirement it names when the area lands, and the gates that only hold the register's format retire now. (b) leaves a frozen record that nothing checks while code changes under it, and (c) keeps 16 tests and a generator alive for a record that issues already replace.

  **O3. Selection inside the single-package tiers.** The integration tier is one Go package of 171 test files, and the chaos tier one of 67. The import graph reaches the integration package from 292 of the 479 packages under `pkg/`, where the change graph selected it for 82. The merge gate of 0154 runs the integration tier as `--changed` selects it.

  - (a) Run the whole package whenever an import reaches it.
  - (b) Narrow to the test functions declared in files whose own imports reach the changed package, and run the whole package when a file shared by the package's tests reaches it. Measured on the integration package, a change to a `pkg/` package selects a median of 37 of the 171 files when it selects any.
  - (c) Run the whole package now, and split the integration and chaos tiers into one Go package per area as each area migrates, so package selection narrows them.

  **Recommendation: (c).** (a) is sound and is what the merge gate runs on the first day. (b) is unsound: a test calls helpers in other files of its package, and the file-level reading misses an import a helper makes. Splitting by area gives the import graph packages to select, and each area change already edits its tests. The time the whole package takes on the merge gate is measured first (see "Validation owed" in `steps.md`), and if it exceeds the gate's budget, the split moves ahead of the areas.

  The design settles every other choice without an owner decision. Each is stated in `change.md`:

  - The index is computed on each run and never committed, and `survey`'s site finder reads it (CS-1).
  - Numbered sections are outside the coverage gate, the 102 exceptions retire with no successor, and headings with no citing test are reported (CS-2, CS-3).
  - Unit selection walks the full closure of dependents, as 0154's merge gate states, with `--rdeps-depth` kept as an operator flag (CS-4).
  - A specification edit selects tests by the identifiers or headings its hunks touch, and the code citing a changed identifier counts as changed (CS-5).
  - Inputs outside Go packages live in `tests/non-go-inputs.yaml`, checked for dead rows rather than for completeness (CS-6).
  - `--max-tier` is removed, `--gate` and a list-valued `--tier` bound a selection, and `run --changed` without a bound runs only the tiers that need no infrastructure (CS-7).
  - §28.4 keeps its number under the heading "Implementation status" (CS-8).
  - Every test in the tiers `validate-diagnosis` covers carries an annotation, replacing the rule that each such file appear in the spec map (CS-9).
  - Existing test names with section numbers are renamed by each area, and the v1 program's area exit says so (CS-10).
- 2026-11-17, review of 0116.S5 (general and security). Fixed: CHEGRESS-I2 reopened (the proxy credential is not bound to a pod identity), CHEGRESS-I1, CHGWPOD-I9, CHGWPOD-I12, and CHGWPOD-I15 planned under 0133.S2, closures settled by the tree attributed to 0116.S5, the wording of §28.4 and N4, a reading rule in the program for claim-register edits, and a static check that every gateway-to-pod request carrying `coordination_generation` is read or recorded. The question about the multi-tenant `spiffeBinding` refusal went to 0133 as its open decision 4.
- 2026-11-17, build. 0116.S5 landed; gate run `1c1b7cd6604178e60db48b3b`. [`digests/0116.S5.md`](digests/0116.S5.md) records the result.
- 2026-11-17, build. 0116.S1 landed; gate run `2702cc229509ac15b9388b90`. [`digests/0116.S1.md`](digests/0116.S1.md) records the result.
- 2026-11-18, 0116.S7, review (general and security) and revision 4 of the branch `change/0116.S7-test-name-identifiers`, which touches `tests/static/` and falls under the merge-gate review rule. No check is removed, weakened, or skipped. Fixed: an identifier spelled with a hyphen or an underscore is refused whatever follows it; a registry code outside the page-code form is left out of the matcher; the form-only code is two to nine upper-case letters; a code after an acronym and a space in a subtest name are read; the rule states that table-driven subtest names are held by review. CS-10 is revised to name every file that keys a test by name (`tests/registers/skip-reasons.yaml`, `tests/registers/residual-skip-reasons.yaml`, and `tests/flake-budget.yaml`). Open decision 1 (how an area measures its section-numbered test names) is raised for the owner.
- 2026-11-18, build. 0116.S2, 0116.S3, and 0116.S7 landed together from `change/0116.S2-S3-S7`; gate run `5e3c51e8bb9372cabc8647c5`. Each step's digest records the result.
- 2026-11-18, decisions. The owner settled open decisions 1 to 4, raised by 0116.S2, 0116.S3, and 0116.S7, on option (a) after an elaboration: "1. ok 2. ok 3. ok but let's not forget to incrementally split it during M3 4. ok". They are decisions 4 to 7 in `decisions.md`. Decision 6 carries the owner's condition that the integration package is split incrementally during M3, each area milestone moving its integration tests into a package of their own, and the v1 program's area exit names it. The open decisions 0116.S4 and 0116.S6 raise are numbered 8 and 9.
- 2026-11-18, review of the branch `change/0116.S4-S6` (general and security, one round), which touches `cmd/lenny-test/`, `tests/static/`, and `tests/registers/` and falls under the merge-gate review rule. The merge gate runs the same tiers as before, the static tier runs `validate`, and every removed check is one CS-9 or the step names. Fixed: the board and the status page regenerated; `--tier` documented as replacing the default set; the move driver rewrites `tests/non-go-inputs.yaml`; the program reads member edits to the retired maps; the gate's general documentation gates stated beside `run --changed --gate merge`; stale wording in the rule, TESTING.md, the selection note, `pr.yml`, and the Makefile; `validateTierTestsAnnotated` registered in the gate-integrity list; the annotation check reads the doc comment the parser attaches, refuses a bare `// spec:` line, and reads the component-tagged tests of `cmd/lenny-ops` and the container helpers; `--tier` and `--gate`, and a group's `gate` and tiers, that share no tier are refused. Raised for the owner: open decision 10 (ending the selection union on the recorded coverage shortfall) and open decision 11 (the scope of the infrastructure-free bound, built as CS-7 states it).
