---
name: digest
description: Generate the digest of a step's branch under changes/<folder>/digests/ - the requirements, operations, and definition blocks it adds, modifies, and retires with old and new text, their code sites and tests from the annotations, the documentation pages that describe each touched page, the catalog names the documentation still uses, and the requirements cited from more than three functions - then fill in the gate results, review outcomes, and defaults applied. Bound to the layout in changes/README.md. Use at the end of every step, or when the owner asks what a branch changed.
argument-hint: <step | step range> [--base <rev>] [--stdout]
---

# Digest

Write the digest of one step, or of several steps that share a branch, so the owner can review what the branch changed without reading the diff. `go run ./scripts/specshift digest` generates the parts it can read from the tree, and this skill fills in the rest. `changes/README.md` defines the digest.

## Arguments

- `<step | step range>`: the step, such as `0115.S8`, or a range of steps of one change that share a branch, such as `0113.S1-S3`.
- `--base <rev>`: compare with this commit instead of the merge base of `HEAD` with `main`.
- `--stdout`: print the digest instead of writing it, to read it before the step is finished.

## Steps

1. Run from the repository root, on the step's branch, after the last commit of the step's code and documentation:

   ```bash
   CGO_ENABLED=0 go run ./scripts/specshift digest 0115.S8
   ```

   It writes `changes/<folder>/digests/<step>.md` and refuses to replace a digest that exists, because the session fills in parts of it by hand. Pass `-force` to regenerate one, then fill it in again. Pass `-base <rev>` for `--base` and `-stdout` for `--stdout`.
2. Read the generated sections and act on what they show:
   - A retired item whose page records no successor and no removal fails `specshift ref check`. Add the record to the page's `retired` front matter.
   - A touched specification page whose documentation page is marked "Edited on the branch: no" needs that page edited, or the line `No reader-facing behavior changes: CODE` in the change's Documentation section, naming that page's code.
   - A catalog name the documentation still uses after the branch renamed or removed it needs each listed line edited.
   - A requirement cited from more than three functions needs a check that only one of them makes the decision and the others pass its result along. Move the annotation off the ones that do not decide.
3. Fill in the three sections the generator leaves as "Not filled in yet":
   - **Gate results.** Tier 0 and every tier the step reached, with the result of each and each pre-existing failure by name.
   - **Review outcomes.** The fixes made, the open questions raised in `decisions.md`, and the issues filed. Write "No review ran" when none did.
   - **Defaults applied.** Every choice made without asking the owner, with the option taken.
4. Commit the digest with the step.

## Steps with no specification change

The generator reports "None" in every specification section for such a step, and its commit list, documentation coverage, and placeholders still apply, so run it as above. When it cannot run, such as on a branch with no merge base with `main`, write the digest by hand under the same headings: the steps and their commits, the requirements added, modified, or retired, the documentation pages edited and their readers, the tests and tiers run with their results, the pre-existing failures, and every default applied without asking.
