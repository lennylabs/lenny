# Specification annotations in code

Draft, 2026-11-16, for the specification structure in [../TAXONOMY.md](../TAXONOMY.md). It moves to `.claude/rules/` when that structure lands, and replaces the annotation parts of `spec-driven-development.md` and `code-best-practices.md`. Until then the current rules govern the tree, and only the spike applies this one.

## Top-level principle

An annotation tells a reader which requirement a test verifies, or which code makes the decision a requirement states. The specification states the rule, and the code comment explains the code. An annotation never restates the specification.

## Measurement behind the rule

On 2026-11-16 the tree held 18,912 annotation lines: 12,075 in tests and 6,837 in non-test code, with up to 133 in one file. Most non-test annotations sit on callers, helpers, fields, and individual branches, so one requirement is cited from many places, and moving or splitting a section forces a reading of every site.

## The rule

- **Tests.** Every test that verifies a requirement cites it in the test function's doc comment: `// spec: POOL-R5`, or several identifiers separated by commas. This annotation is mandatory, and the coverage gate reads it as the map from requirement to test.
- **Packages.** The package doc comment, in `doc.go`, names the pages the package implements: `// Package podclaimer implements GWCLAIM and the claim rules of CLAIM.` It appears once per package.
- **Deciding code.** A function, method, or type that makes the decision a requirement states cites that requirement in its doc comment. That code is the branch that refuses, the computation of a limit, or the selection a requirement constrains. Callers, wrappers, helpers, and code that passes a value along do not cite.
- **Defined values.** A constant or default whose value a definition block defines cites the block: `// spec: setting maxIdleSeconds`. That way, a change to the value finds the code.
- **No annotations inside function bodies or on struct fields.** A function whose body makes the decisions of several requirements lists their identifiers in its doc comment. Moving a decision into its own function is often clearer.
- **Identifiers only.** An annotation names identifiers: requirements, operations, page codes, or definition blocks. It does not carry section numbers, quoted specification text, or a paraphrase of the rule.
- **One decision, few sites.** A requirement cited from more than three non-test functions signals that its decision is made in several places. Consolidate the decision, or state in the doc comment why each site makes it.

## Gate

A tier-0 lint enforces the rule on non-test files:

- It refuses an annotation inside a function body or on a struct field.
- It refuses an annotation that names a section number, and one whose identifier does not resolve.
- It warns when a file carries more than ten annotations or a requirement is cited from more than three functions.

On test files it refuses an unresolved identifier, and the coverage gate checks that every requirement a change touches has a test that cites it. Existing `§` annotations are counted per area, and the count may only fall.

## How to apply when writing code

1. Find the requirement the code carries out on its page. If none states the decision, stop and raise it as an open decision on the change.
2. Put the annotation on the doc comment of the function or type that makes the decision, and on the tests that verify it.
3. Do not annotate the callers, helpers, or fields around it.
4. When touching code that carries old `§` annotations, replace them with identifiers under this rule in the same commit, and delete the ones the rule does not keep.
