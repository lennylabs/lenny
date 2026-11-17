# Documentation and the specification

Draft, 2026-11-16, for the specification structure in [../TAXONOMY.md](../TAXONOMY.md). It moves to `.claude/rules/` when that structure lands and extends `doc-content.md`, which keeps governing what a documentation page says and how it reads. This rule governs how a page stays in step with the specification. Until the structure lands, the current rules govern the tree.

## Top-level principle

The specification states what Lenny does, and the documentation explains it to a reader. A change that alters what a reader can observe updates the documentation in the same change. A tool can find every documentation page a specification change affects, and every name or value a page quotes is checked against the specification.

## The rule

- **Front matter names the specification pages.** Every page under `docs/` lists the codes of the specification pages it describes: `spec: [POOL, CLAIM, ADMPOOLS]`. The list stays in front matter, so readers never see a code, a requirement identifier, or a section number, as `doc-content.md` requires. A page that describes no specification page, such as a contribution guide, lists none and says so with `spec: []`.
- **Names and values come from the catalogs.** A page that names a setting, metric, alert, error, label, annotation, event, or CLI command uses the name the catalog defines. A default, a limit, or a unit it quotes matches the definition block.
- **Reference pages are generated.** The pages under `docs/reference/` that list settings, metrics, alerts, errors, events, state machines, and glossary terms are generated from the specification's catalogs, with a hand-written introduction for the reader. They are never edited by hand.
- **A change carries its documentation.** A change that adds, modifies, or retires a requirement, an operation, or a definition block on a specification page edits the documentation pages that name that page, or states in its Documentation section that no reader-facing behavior changes. A gap found later is an issue of kind `doc-gap`.

## Gate

A tier-0 check enforces the rule:

- It refuses a documentation page with no `spec` front matter, and a code that does not resolve.
- It refuses a name a page uses that the catalogs do not define, and a quoted default, limit, or unit that differs from the definition block. The check reads code spans and tables, where pages state names and values.
- On a diff, it lists the documentation pages that name each touched specification page. It refuses the diff when none of them changed and the change's Documentation section does not state that no reader-facing behavior changes. Edits that need no change are checked the same way against the commit.
- It refuses a generated reference page that differs from what the generator writes.

## How to apply when changing Lenny

1. When framing a change, list in its Documentation section each page the change affects and its reader. `where <CODE>` lists the pages that describe a specification page.
2. When building, edit the documentation after the code and the tests, in the same step or a documentation step that the code step names.
3. When adding a documentation page, give it `spec` front matter before writing its content.
4. When renaming or removing a catalog name, run the check: it lists every page that still uses the old name.
