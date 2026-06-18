# Spec-driven and test-driven development

This is the governing rule for how change happens in this repository. Lenny is built spec-first and test-first: the technical specification under `spec/` is the single source of truth, code implements the spec, and tests verify the spec. It frames the companion rules `code-best-practices.md`, `test-coverage.md`, and `doc-style.md`.

## Top-level principle

All new code aligns to the spec. Every behavior in `pkg/`, `cmd/`, `sdks/`, `migrations/`, and `charts/` traces to a spec section, and a test pins that behavior to the section. Code with no spec basis is not written; a test that does not encode a spec requirement is not the test to write.

## The spec is the source of truth

- Implement what the spec says. When code and spec disagree, the spec is right and the code is the defect, unless the spec itself is wrong (see below).
- Cite the spec on spec-derived logic with `// spec: §X.Y` (see `code-best-practices.md`). A reviewer traces any behavior to its section through that citation.
- Do not edit `spec/` ad hoc to match code you want to write. The guard hook blocks direct spec writes; spec changes go through the pipeline below.

## When the spec is silent, wrong, or contradictory

Change the spec first, through the proposal pipeline, then write the code:

1. `change-proposal` writes and adversarially converges a proposal under `proposals/` that stages the spec edits.
2. A human approves it.
3. `implement-proposal` lands the staged spec edits in `spec/`, verifies them, and then implements the code against the now-current spec.

Never let code lead the spec. A spec change lands and is verified before the code that depends on it is written.

## Test-driven

- Tests encode the spec's required behavior, including the empty, error, concurrent, boundary, and spec-named-failure paths, across every tier the change reaches (see `test-coverage.md`). Happy-path coverage alone does not satisfy this rule.
- A behavior is not done until a test pins it to its spec section and that test passes. Run the tests; writing them is half the work.
- Tests are first-class spec artifacts. Every test carries a `// spec:` annotation mapping it to the sections it verifies, and every behavioral spec section has at least one test. The harness maps tests to sections through that annotation.

## Where this rule applies

- All Go under `pkg/`, `cmd/`, `sdks/`, and `migrations/`, the chart under `charts/`, and the tests under `tests/`.
- It governs the proposal pipeline skills (`change-proposal`, `implement-proposal`) and the build loop (`close-build-gaps.sh`), which exist to keep code and spec in lockstep.

## How to apply when implementing a change

1. Find the spec section the change implements. If none exists or it is wrong, stop and take it through the proposal pipeline before writing code.
2. Land and verify any spec change first; implement the code against the committed spec.
3. Write the tests that encode the spec's behavior for every tier the change reaches, with the `// spec:` annotation, and run them to green.
4. Cite the spec section in the code and the tests.

## Escape hatches

- Build tooling, test harnesses, and developer scripts (`tests/testinfra/`, `scripts/`, `cmd/lenny-test`) implement the testing and operational model rather than a spec behavior; they cite the spec where one applies and are otherwise governed by `code-best-practices.md`.
- A pure refactor that changes no behavior introduces no new spec obligation; the existing spec citations and tests must still hold.
