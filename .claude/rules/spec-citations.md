# Specification citations

Project-wide rules for citing the technical specification under `spec/`. They apply to every carrier that points a reader at a specification section: specification prose, documentation, schemas, Go comments and `// spec:` annotations, test names and comments, and the root-level records inside the domain below. They complement `code-best-practices.md`, which states the `// spec:` annotation format, and `doc-content.md`, which keeps section numbers out of published documentation.

## Top-level principle

A specification citation names a heading, never a line. Every edit to the specification shifts the lines below it, so a line citation stops pointing where its author meant as soon as another change lands. A heading moves with its content.

## The rule

- Cite a section by its number and heading, or by its anchor, such as `§N.M` or `[Section N.M](NN_file-name.md#nm-heading-slug)`. A citation that needs more precision than a section names the bold paragraph label or quotes the sentence it means.
- Do not write a specification line number in any spelling, such as `spec/NN_file-name.md:LINE`, `spec/NN:LINE`, or "line LINE of §N.M". The prohibition is on the line number rather than on one form of words, so a spelling the gates do not yet recognize is a gap in the gates rather than a permitted citation.
- A section that gives up content keeps a permanent successor pointer naming the heading that now owns the content and the identifiers that moved.

The naming law in the communication-channels section of the specification states this rule as N8. It sits in the channel naming law because the migration that retired line citations was the same migration that renamed the channels; the rule itself governs every specification citation.

## Domain

The rule applies to the domain the citation gates read, which `scripts/specshift/scope/scope.go` defines. The following are outside it:

- The `proposals/` directory. A draft proposal cites specification lines as evidence of what the tree said when it was written, and an implemented proposal is a historical record that is not edited (`spec-driven-development.md`). The staged edits of a proposal locate their targets by heading and quoted text, never by line number, because the implementor applies them against a specification that other proposals have changed since.
- The audit records `BUILD-GAPS.md`, `TEST-GAPS.md`, `gateway-runtime-comms.md`, and `gateway-runtime-comms-remediation.md`, which record findings as they were written.
- The citation registers `tests/registers/line-citations.yaml` and `tests/registers/line-citation-resolution.yaml`, which are the baselines the gates consume.
- Every `testdata/` directory, whose fixtures present the retired form verbatim.

The root planning records `BUILD-PLAN.md`, `BUILD-PROGRESS.md`, and `PROPOSAL-QUEUE.md` are inside the domain. A line citation there is a pointer that has to keep resolving.

The specification's naming law states N8 without this domain. The domain above is the one the gates enforce, and stating it in the naming law is owed to the next proposal that edits that section.

## Gates

The citation resolver and the line-citation ratchet under `scripts/specshift/gate` hold this rule. The ratchet counts the line citations each file still carries against `tests/registers/line-citations.yaml` and fails when a file's count rises. A new line citation is a defect to fix rather than an entry to add to the register.

## How to apply when editing

1. When citing the specification, name the section number and heading, or link the anchor.
2. When the section is long, name the bold paragraph label or quote the sentence rather than giving a line.
3. In a proposal, a line citation is allowed as evidence, and every staged edit locates its target by heading and quoted text.
4. When moving content out of a section, leave a successor pointer naming the heading that now owns it.

## Maintenance

When a new carrier or a new record outside the domain surfaces, update the domain list here and in `scripts/specshift/scope/scope.go` in the same change, so the stated domain and the enforced one stay one statement.
