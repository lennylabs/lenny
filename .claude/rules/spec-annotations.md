---
paths:
  - "pkg/**"
  - "cmd/**"
  - "sdks/**"
  - "migrations/**"
  - "tests/**"
---

# Specification annotations in code

Project-wide rules for the `// spec:` annotations that tie Go code and tests to the specification under `spec/`. They apply to every Go file under `pkg/`, `cmd/`, `sdks/`, `migrations/`, and `tests/`, and to any agent or workflow that writes or changes code there. They state where an annotation goes, what it cites, and what a comment never carries. `spec-citations.md` states how any carrier cites the specification, and `spec-driven-development.md` states why code traces to the specification at all.

## Top-level principle

An annotation tells a reader which requirement a test verifies, or which code makes the decision a requirement states. The specification states the rule, and the code comment explains the code. No comment restates the specification.

## The rule

- **Tests.** Every test that verifies a requirement cites it in the test function's doc comment: `// spec: POOL-R5`, or several identifiers separated by commas. The coverage gate reads this annotation as the map from requirement to test. A test that verifies no requirement, such as one for error wrapping or for contributor tooling, carries no annotation and says why in a comment.
- **Test names.** A test name describes the behavior it checks. It carries no section number and no identifier.
- **Packages.** The package doc comment, in `doc.go`, says in prose what the package implements and ends with a line naming the page codes: `// spec: GWCLAIM, CLAIM`. It appears once per package.
- **Deciding code.** A function, method, or type that makes the decision a requirement states cites that requirement in its doc comment. That code is the branch that refuses, the computation of a limit, or the selection a requirement constrains. Callers, wrappers, and code that passes a value along do not cite it. A requirement that several functions carry out together, such as the state writes of one state machine, is cited on each of them.
- **Operations.** The function that maps an interface operation's outcomes to behavior cites the operation by its identifier: `// spec: KPCLAIM-create`.
- **Defined values.** A constant or default whose value a definition block defines cites the block by its anchor: `// spec: setting-maxidleseconds`. A change to the value then finds the code.
- **Partial conformance.** Code that carries out a requirement only in part cites it and names the open issue: `// spec: CLAIM-R4 (partial: CLAIM-I4)`.
- **Code awaiting removal.** Code that a change removes, but that cannot be removed yet because of code outside the package, cites the issue that tracks the removal: `// spec: removal pending CLAIM-I2`.
- **Placement.** No annotation sits inside a function body or on a struct field. A function whose body makes the decisions of several requirements lists their identifiers in its doc comment.
- **Identifiers only.** No comment in code or tests carries a section number, quoted specification text, or a paraphrase of a rule. An identifier survives any move of its page, and a section number or a copied sentence does not.

## Transition

The specification moves to identifier-addressed pages area by area, and code follows its area.

- New or changed code, tests included, cites identifiers once the page it implements has landed, which is when that page's `status` is neither `placeholder` nor `partial`. Before that, it may cite `§N.M (heading)`. The placement, test-name, and quoted-text parts of this rule apply to both forms.
- Existing `§` annotations stay valid until their area migrates. They resolve through the successor stubs of the numbered sections, and the area's change replaces them before its milestone exits.
- Existing test names that carry a section number stay until their area migrates. The area's change renames them when it re-annotates the area's packages, updates the entries that key the renamed tests by name in `tests/registers/skip-reasons.yaml`, `tests/registers/residual-skip-reasons.yaml`, and `tests/flake-budget.yaml`, and finishes before its milestone exits. The lint reads changed lines, so an existing name is refused only when a branch changes the line that declares it.

The other rules that mention section citations in code point here rather than restating this trigger.

## Gate

A static-tier lint, `TestSpecAnnotationLintPassesOnTheBranchDiff` and `TestSpecAnnotationIdentifiersResolveOnTheBranchDiff` in `tests/static`, reads the Go lines a branch adds or changes against its merge base with `main` (or `origin/main`), including uncommitted and untracked files. It leaves out `testdata/` directories, generated files, the change records, and the staged proposals. On those lines it refuses:

- a `// spec:` annotation inside a function body or a function literal, and one on a struct field, as a doc comment or a trailing comment, in test and non-test files alike;
- a section number in the name of a test function, such as `_spec_4_7` or `Spec16_5`, and a `§` or `spec N.M` in a subtest name;
- a requirement or issue identifier in the name of a test function or a subtest, with its hyphen kept, written as an underscore, or dropped, and in a subtest also written as a space, such as `TestPOOL_R5Holds`, `TestPoolR5`, or a subtest named `POOL-R5 holds`. A code of two to nine upper-case letters followed by a hyphen or an underscore, then `R` or `I` and a number, is refused by its form alone. A dropped hyphen, or a code with only its first letter in upper case, is refused for a page code or alias that `spec/registry.json` carries. A version number such as `HTTP2`, `OpenAPI31`, or `V1_R2` is not an identifier. The check reads a subtest name written as a string literal in the `t.Run` call, so a name taken from a table or a variable is held by review;
- specification text in a comment, which is a quoted span of five words or more that appears in `spec/` word for word, other than a heading or a bold paragraph label, or a run of twelve words that appears in `spec/` word for word;
- an identifier citation (a requirement `CODE-Rn`, an issue `CODE-In`, an operation `CODE-<operation>`, a definition-block anchor `<kind>-<slug>`, or a page code) that `spec/registry.json` does not carry as a live code, requirement, operation, term, or block anchor. A retired identifier fails with a message that names its successor or its removal record, and the author updates the citation. An issue also resolves through a record under `issues/<CODE>/` whose name opens with its identifier. Channel identifiers under `LNK-`, `CH-`, and `REG-` are not page identifiers and are not checked.

The lint skips with a reason when the repository carries neither `main` nor `origin/main`. Any other failure to find the merge base, such as a git command that fails or an integration branch that shares no history with the branch, fails the lint. While the tree carries no `spec/registry.json`, the identifier check skips with a reason, and the test-name check refuses only the identifier spellings it recognizes by their form.

A ratchet, `TestSpecSectionCitationRatchetHoldsEachPackage`, counts the section-number citations (the `§N.M` form and the bare `N.M` form) in the `// spec:` annotations of each Go package directory and compares the count with `tests/registers/spec-section-citations.yaml`. Packages stand in for areas until areas map to packages. The ratchet fails a package whose count rises above its registered count and a package with a citation and no entry. When new code there adds a `§` citation, as "Transition" above allows, the author raises the package's count in the register in the same commit so the rise is visible in review. When a count falls, the run writes the lower count back to the register and fails, and the author commits the lowered register with the change. Once an area has migrated, the counts of its packages are strict and may only fall; the area change that migrates them records the switch.

A coverage gate, `TestEveryChangedRequirementHasACitingTest` in `tests/static`, compares the requirements of the working tree with those at the branch's merge base with `main` (or `origin/main`). Each requirement the branch adds, or whose text it modifies, passes when a test function's doc comment cites it directly, or when an open or planned issue under `issues/<CODE>/`, for any page code the specification registers, lists it in its `requirements` front matter. A requirement that moves to another page with its text unchanged, a retired requirement, an operation, a definition block, and a numbered section are outside the gate. An annotation on a helper, inside a test body, or separated from the test function by a blank line does not count, and neither does a test in a file the go tool never builds. The gate skips with a reason when the branch has no merge base, and it passes with nothing to check while the registry holds no requirements.

Review holds the parts of the rule that the lint cannot read: that only deciding code cites a requirement, that a comment does not paraphrase the specification, and that a package's `doc.go` ends with its page codes.

## How to apply when writing code

1. Find the requirement the code carries out on its page. If none states the decision, record it as an open decision on the change.
2. Put the annotation on the doc comment of the function or type that makes the decision, and on the doc comments of the tests that verify it.
3. Do not annotate the callers, helpers, or fields around it, and do not put a section number in a test name or a comment that cites a migrated page.
4. When touching code that carries `§` annotations or section-numbered comments in an area that has migrated, replace them under this rule in the same commit, and delete the ones the rule does not keep. Commit the lowered count in `tests/registers/spec-section-citations.yaml` with them.
5. Run the static tier, which runs the lint on the branch's diff and the ratchet on the tree.

## Escape hatches

- Generated code (`*.pb.go`, `zz_generated.deepcopy.go`, and other output carrying a `Code generated ... DO NOT EDIT.` header) is outside the rule and the lint.
- Fixtures under `testdata/` present annotations as gate input rather than as citations, and are outside the lint and the ratchet.
- Build tooling, test harnesses, and developer scripts cite the specification only where a section applies, as `spec-driven-development.md` states. A test of such tooling carries no annotation and says why in a comment.
- A quotation of an external standard, an error message, or a log line is outside the quoted-text check unless the same words appear in `spec/`; when they do, cite the identifier of the definition that owns them.

## Maintenance

When review finds an annotation defect that this file does not cover, add a specific, actionable rule here, and add the check to the lint in `scripts/specshift/annotation` when the defect is mechanical. Keep the lint's refusals and the list under "Gate" in the same change, so the stated gate and the enforced one stay one statement.
