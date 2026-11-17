# Spec-driven and test-driven development

This is the governing rule for how change happens in this repository. Lenny is built spec-first and test-first: the technical specification under `spec/` is the single source of truth, code implements the spec, and tests verify the spec. It frames the companion rules `code-best-practices.md`, `test-coverage.md`, `spec-annotations.md`, and `doc-style.md`.

## Top-level principle

All new code aligns to the spec. Every behavior in `pkg/`, `cmd/`, `sdks/`, `migrations/`, and `charts/` traces to a spec section, and a test pins that behavior to the section. Code with no spec basis is not written; a test that does not encode a spec requirement is not the test to write.

## The spec is the source of truth

- Implement what the spec says. When code and spec disagree, the spec is right and the code is the defect, unless the spec itself is wrong (see below).
- Cite the spec on the code that makes a decision the spec states, with a `// spec:` annotation in the form `spec-annotations.md` gives: a requirement or operation identifier, or `§N.M (heading)` while the page the code implements has not landed, as its "Transition" section states. A reviewer traces any behavior to its requirement through that citation.
- Do not edit `spec/` ad hoc to match code you want to write. A specification change goes through a change, as below.

## When the spec is silent, wrong, or contradictory

Change the spec first, through a change folder under `changes/`, then write the code. `changes/README.md` describes the records, the workflow, and the helpers:

1. **Frame.** The change states the problem, the scope, and the open decisions, with evidence from the tree.
2. **Decide.** The owner settles the open decisions in the change's `decisions.md`.
3. **Build.** Each step lands on its own branch: the specification edits first, then the code and the tests against the specification as edited, then the documentation.

Never let code lead the spec. On a step's branch, the specification edit is committed before the code that depends on it.

An edit that changes no requirement, no defined value, and no operation, such as a typo, a gloss, a link, or a documentation page brought in line with the current specification, needs no change folder and lands as a plain commit with the gates green.

When the change belongs to a program under `changes/programs/`, read the program's "Reading the program" section before working on it: it states how the program's changes are read together and how a conflict between them is raised.

## Test-driven

- Tests encode the spec's required behavior, including the empty, error, concurrent, boundary, and spec-named-failure paths, across every tier the change reaches (see `test-coverage.md`). Happy-path coverage alone does not satisfy this rule.
- A behavior is not done until a test pins it to its spec section and that test passes. Run the tests; writing them is half the work.
- Tests are first-class spec artifacts. Every test that verifies a requirement carries a `// spec:` annotation in its doc comment, in the form `spec-annotations.md` gives, and every behavioral requirement has at least one test. The harness maps tests to requirements through that annotation.

## A finished record is not edited

An archived change under `changes/archive/`, and a proposal under `proposals/` whose status records it as implemented, are historical records of what was decided and done at the time. They are not edited afterwards, and that includes adding a pointer recording that a later change retired or reverted part of what they delivered. The one exception is a link inside them that a later move rewrites so it still resolves.

The codebase evolves, and a change that landed may be undone by a later one. The earlier record keeps the words it was written with, because its value is as evidence of what was true when it landed. Amending it to track later reversals turns it into a running summary of current state, which `spec/` and the tree already are. A change that reverts or retires part of an earlier one records that in its own text, and lands it in `spec/`, in the code, and in the issues it closes.

A change that has not finished is different: it is still being written or built, so editing it is expected, and each revision is recorded in its `review-history.md`.

## Where this rule applies

- All Go under `pkg/`, `cmd/`, `sdks/`, and `migrations/`, the chart under `charts/`, and the tests under `tests/`.
- It governs the helpers under `.claude/skills/` that edit the specification or the code, `build` in particular, which exist to keep code and spec in lockstep.

## How to apply when implementing a change

1. Find the spec section the change implements. If none exists or it is wrong, stop and raise it as a change, or as an open decision on the change you are building.
2. Land the spec edit first on the step's branch; implement the code against the edited spec.
3. Write the tests that encode the spec's behavior for every tier the change reaches, with the `// spec:` annotation, and run them to green.
4. Cite the requirement in the code that makes the decision and in the tests, as `spec-annotations.md` states.

## Escape hatches

- Build tooling, test harnesses, and developer scripts (`tests/testinfra/`, `scripts/`, `cmd/lenny-test`) implement the testing and operational model rather than a spec behavior; they cite the spec where one applies and are otherwise governed by `code-best-practices.md`.
- A pure refactor that changes no behavior introduces no new spec obligation; the existing spec citations and tests must still hold.
