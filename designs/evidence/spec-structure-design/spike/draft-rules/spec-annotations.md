# Specification annotations in code

Draft, revision 2, 2026-11-16, for the specification structure in [../TAXONOMY.md](../TAXONOMY.md). It moves to `.claude/rules/` when that structure lands. It then replaces the annotation and test-naming parts of `spec-driven-development.md`, `code-best-practices.md`, and `test-coverage.md`. Until then the current rules govern the tree, and only the spikes apply this one. Revision 2 applies what the code migration spike found when it applied revision 1 to the gateway pod claimer ([../../spike-migration/FINDINGS-code.md](../../spike-migration/FINDINGS-code.md)).

## Top-level principle

An annotation tells a reader which requirement a test verifies, or which code makes the decision a requirement states. The specification states the rule, and the code comment explains the code. No comment restates the specification.

## Measurement behind the rule

On 2026-11-16 the tree held 18,912 annotation lines: 12,075 in tests and 6,837 in non-test code, with up to 133 in one file. Most non-test annotations sit on callers, helpers, fields, and individual branches, so one requirement is cited from many places, and moving or splitting a section forces a reading of every site. In the pod claimer, the spike found 175 further comment lines in code that carried a section number outside any annotation, and 73 of 91 test names carried a section suffix.

## The rule

- **Tests.** Every test that verifies a requirement cites it in the test function's doc comment: `// spec: POOL-R5`, or several identifiers separated by commas. The coverage gate reads this annotation as the map from requirement to test. A test that verifies no requirement, such as one for error wrapping, carries no annotation.
- **Test names.** A test name describes the behavior it checks. It carries no section number and no identifier.
- **Packages.** The package doc comment, in `doc.go`, says in prose what the package implements, and ends with a line naming the page codes: `// spec: GWCLAIM, CLAIM`. It appears once per package.
- **Deciding code.** A function, method, or type that makes the decision a requirement states cites that requirement in its doc comment. That code is the branch that refuses, the computation of a limit, or the selection a requirement constrains. Callers, wrappers, and code that passes a value along do not cite. A requirement that several functions carry out together, such as the state writes of one state machine, is cited on each of them.
- **Operations.** The function that maps an interface operation's outcomes to behavior cites the operation: `// spec: kubernetes#podclaim.create`.
- **Defined values.** A constant or default whose value a definition block defines cites the block by its anchor: `// spec: setting-maxidleseconds`. That way, a change to the value finds the code.
- **Partial conformance.** Code that carries out a requirement only in part cites it and names the open issue: `// spec: CLAIM-R4 (partial: CLAIM-I4)`.
- **Code awaiting removal.** Code that a change removes, but that cannot be removed yet because of code outside the package, cites the issue that tracks the removal: `// spec: removal pending CLAIM-I2`.
- **No annotations inside function bodies or on struct fields.** A function whose body makes the decisions of several requirements lists their identifiers in its doc comment.
- **Identifiers only.** No comment in code or tests carries a section number, quoted specification text, or a paraphrase of a rule.

## Gate

A tier-0 lint enforces the rule:

- It refuses an annotation inside a function body or on a struct field in a non-test file.
- It refuses a section number in any comment or test name, and an annotation whose identifier does not resolve.
- It refuses a package with Go files and no `doc.go` annotation line.
- On test files, it refuses an unresolved identifier. The coverage gate checks that every requirement a change touches has a test that cites it.
- Existing `§` annotations are counted per area, and the count may only fall.

The digest lists each requirement cited from more than three functions, so a reviewer can check that the decision is not duplicated. The lint does not warn on it.

## Tooling

- Interface operation identifiers contain `#` and `.`, and `kubernetes#podclaim.create` reads like a Go selector. Rename and move tools treat a `<folder>#...` token as opaque. In the spike, a package rename rewrote six of them. Whether the operation format changes is an open item in TAXONOMY.md.
- The reference tool matches identifiers as whole tokens. In the spike, a substring search matched `CLAIM` inside `TENANT_CLAIM_MISSING`.

## How to apply when writing code

1. Find the requirement the code carries out on its page. If none states the decision, record it as an open decision on the change.
2. Put the annotation on the doc comment of the function or type that makes the decision, and on the tests that verify it.
3. Do not annotate the callers, helpers, or fields around it, and do not mention section numbers anywhere.
4. When touching code that carries old `§` annotations or section-numbered comments, replace them under this rule in the same commit, and delete the ones the rule does not keep.
