# Specification structure

Status: `In progress`, revision 3, 2026-11-16. Programs: [v1](../programs/v1.md), milestone M1.

## Problem

The specification is about 493,000 words in 29 files, numbered by section, and agents struggle to piece facts together across it. One fact is often stated in several sections, so a change that edits one leaves the others stale, and a reader cannot tell which statement governs. Code cites it 18,912 times by section number, so any reorganization breaks the citations, and the registers keyed by section number or path (`tests/spec-map.json` is 687,000 lines) turn every move into hand edits.

The owner settled a structure organized by capability on 2026-11-16, after a spike that mapped the whole specification to 277 pages and migrated one area and its code ([evidence/spike/README.md](evidence/spike/README.md), [evidence/spike-migration/README.md](evidence/spike-migration/README.md)). [TAXONOMY.md](evidence/spike/TAXONOMY.md) version 2 holds the rules and the owner's decisions on them. The migration runs area by area at the target state (option C of [WORKFLOW.md](evidence/spike/WORKFLOW.md), "Migration order"), and it needs the structure and its tooling first.

## Scope

- **In scope.**
  - The conventions: the root map, `conventions.md` with the page types, the placement procedure, identifiers, the gloss rule, the definition-block kinds, and the page templates.
  - The code registry, and a placeholder page with its code and summary for every page of the map.
  - The reference tool: the registry of identifiers, link checking, and link rewriting after a move.
  - The definition-block format and the generators of the catalogs and the folder indexes.
  - The gates that hold the structure.
  - The annotation rule for code and the documentation rule, with their lints.
  - The helpers that read the structure: `digest`, `where`, and `since`, and the status page generated from the programs.
  - The successor-stub format for the numbered files and the frozen reference for old citations.
  - The retirement of `BUILD-PLAN.md`, `BUILD-PROGRESS.md`, and `ROADMAP.md`, which the naming law in §28, `spec-citations.md`, tests in tiers 0, 4, 8, and 9, three load baselines, and several pages and scripts name.
- **Out of scope.**
  - Writing the content of any area. Each area migrates in its own change, one milestone each.
  - Coverage derived from annotations, which replaces `tests/spec-map.json` (0116).
  - The kind renames (0137.S5) and the gateway package layout (0117).
  - Re-annotating existing code. Old `§` citations keep resolving, and each area's count of them reaches zero before the area's milestone exits.

## Terms

| Term | Meaning |
| :-- | :-- |
| Page | One file of the new specification tree, of one type: overview, walkthrough, capability, component, interface, cross-cutting, catalog, index, or conventions ([TAXONOMY.md](evidence/spike/TAXONOMY.md), "Page types"). |
| Area | A group of capability pages under `capabilities/<area>/`, such as runtimes and pools. Each area migrates as one change. |
| Code | The identifier of a page: two to nine upper-case letters, registered in `conventions.md`, such as `POOL`. |
| Identifier | A code with a suffix: `POOL-R5` is a requirement, `POOL-I2` an issue, and `ADMPOOLS-createPool` an operation. |
| Definition block | A fenced `lenny-def` block on its owner page that defines one enumerable fact, such as a setting or a metric. Catalogs are generated from these blocks. |
| Registry | The generated table of every code, identifier, anchor, and definition block with the file that holds it, and the history of moves and aliases. |
| Reference tool | A new pass of `scripts/specshift` that builds the registry, checks every link into the specification, and rewrites link paths after a page moves. |
| Placeholder page | A page with front matter, a summary, and a pointer to the numbered sections that still hold its content, and no other text (`status: placeholder`). |
| Numbered file | One of the current files `spec/01_*.md` to `spec/29_*.md`. |
| Successor stub | What remains of a numbered section once its content has migrated: the heading and a list of links to the identifiers that now hold its content. |
| Frozen reference | The specification as it stood before the first area migrated, kept so that a `§` citation can be read in its original context. |
| Labels | The steps of this change carry `0115.Sn`. |

## High-level design

The new tree grows inside `spec/` beside the numbered files. 0115 lands the conventions, every page of the map as a placeholder with its code, and the tooling. Each area change then writes its pages over the placeholders at the target state and turns the numbered sections it consumed into successor stubs. When the last area lands, only stubs remain in the numbered files, and a final change removes them.

Every link into the specification targets an identifier, and its path is derived data: the reference tool rewrites paths when a page moves, so areas can move later at the cost of one command. Enumerable facts are defined once in definition blocks, and catalogs, folder indexes, the component lists of settings and metrics, and the reference pages of the documentation are generated from them. Code cites identifiers under a narrower annotation rule, and old `§` citations resolve through the successor stubs until each area re-annotates its code.

The gates hold all of this on every commit without a model. They apply to the new pages from the start, and to a numbered file once it has been turned into stubs.

## Detailed design

### Conventions and the code registry

`spec/README.md` becomes the root map: what Lenny is in one paragraph, the areas, and reading paths by task. The current `spec/README.md`, which lists the numbered files, moves under a "Numbered files" heading of the root map until they retire. `spec/conventions.md` states the rules of [TAXONOMY.md](evidence/spike/TAXONOMY.md) version 2 as the specification's own text, with the items the migration spike settled: definition-block anchors as `<kind>-<slug>`, term anchors unique across the specification, placeholder and partial pages, register ownership, admission refusals with no code, contribution blocks, review comments allowed only while an area migrates, and operation identifiers as `<CODE>-<operation name>`. It also holds the code registry: one row per page code, from the map's 277 pages, with its path and summary. The spike's `conventions.md` ([spec-next/conventions.md](evidence/spike-migration/spec-next/conventions.md)) is the starting draft.

Every page of the map exists after this change, as a placeholder: front matter with `code`, `type`, `area`, `summary`, and `status: placeholder`, and a list of the numbered sections that hold its content today, taken from the map's assignment ([proposed/ASSIGNMENT.md](evidence/spike/proposed/ASSIGNMENT.md)). Folder index pages are generated from the front matter.

### The reference tool

A new pass of `scripts/specshift`, as TAXONOMY states under "References and moves", driven by a generated registry instead of a hand-written register.

- `registry` builds the registry from front matter, requirement and term anchors, operation identifiers, and definition blocks, and writes it as `spec/registry.json`. The committed file is the record of where every identifier was, so a later run detects a move by comparing with it.
- `check` reports every link into the specification that does not resolve, every fragment that is not a registered identifier, every link to a heading anchor on another page, every link with a fragment into a placeholder, every duplicate code, anchor, or block, and every identifier retired without a successor or a "removed" record.
- `fix` rewrites the path of every link whose target or fragment is a registered identifier that moved, across the tracked tree except `changes/archive/`, `proposals/`, and the frozen gap records.
- `lookup <identifier or §N.M>` prints the file and anchor of an identifier, or for a `§` citation the successor identifiers its stub names and the frozen text.

The spike's prototype ([specref.py](evidence/spike-migration/spec-next/tools/specref.py)) caught planted errors and rewrote 69 links after a move. Its two defects, a lost path prefix and a dependence on a hand-committed snapshot, are fixed by building in `specshift` with the committed registry as the snapshot.

### Definition blocks and generators

The `lenny-def` block format of TAXONOMY ("Owners of enumerable facts"), with one schema per kind: setting, limit, error, metric, alert, event, label, annotation, custom resource, operation, state, register, grant, admission refusal, and the contribution kinds (RBAC grant and network rule first). A generator builds `spec/catalogs/` (settings with tier presets, limits, errors, metrics, alerts, events, labels and annotations, custom resources, operations, requirements, state machines, channels, and the glossary), the generated sections of component pages (settings read, metrics emitted, contributed grants and rules), and the folder indexes. A gate checks each generated file against a fresh run.

### Gates

Each gate runs in tier 0 and costs no model tokens. They apply to pages of the new tree, and to a numbered file once its sections are stubs:

- links and identifiers resolve, fragments are registered identifiers, and no link targets a heading on another page (`specshift ref check`);
- every definition block is defined once and validates against its kind's schema;
- every gloss holds no number, no normative keyword, and exactly one link;
- a walkthrough page holds no normative keyword;
- the documentation style lint passes on new pages;
- `src`, `judgment`, and `unresolved` review comments are refused on any page whose area has landed;
- each contribution block agrees with the chart's rendered Role and NetworkPolicy;
- generated catalogs, indexes, and component sections are current;
- a page over about 6,000 words, or an index over about 1,500, is reported.

The gate that every added or modified requirement has a test citing it needs coverage derived from annotations, and lands with 0116.

### Annotation rule

[draft-rules/spec-annotations.md](evidence/spike/draft-rules/spec-annotations.md) becomes `.claude/rules/spec-annotations.md`: tests cite the requirements they verify in their doc comment, a package's `doc.go` ends with a line naming its page codes, only the code that makes a decision cites the requirement, operations and defined values are cited by identifier, partial conformance and pending removal cite the issue, and nothing cites a section number or sits inside a function body. It replaces the citation part of `spec-driven-development.md` and the format part of `code-best-practices.md`, and `spec-citations.md` states that a citation in new or changed code names an identifier. A tier-0 lint holds the rule on new and changed lines, and a ratchet register records each area's count of old `§` citations, which may fall and never rise.

### Documentation rule

[draft-rules/doc-spec-links.md](evidence/spike/draft-rules/doc-spec-links.md) extends `.claude/rules/doc-content.md`: every page under `docs/` names in front matter the specification pages it describes (`spec: [POOL, CLAIM]`, or `spec: []`), names and values it quotes come from the catalogs, the reference pages that list catalog entries are generated, and a change carries its documentation. Front matter is added per area, and each gate turns on for a page once the area it describes has landed (decision 11). The generated reference pages replace the hand-written lists of `docs/reference/` area by area, as the catalogs fill; until an area's blocks exist, its rows stay hand-written.

### Helpers that read the structure

- `digest` generates `digests/<step>.md` from a branch: requirements added, modified, and retired with old and new text, their code sites and tests from the annotations, the documentation pages that name each touched page, the gate results, and the defaults applied.
- `where <identifier>` shows a requirement's text, its code sites, its tests, the documentation pages that describe its page, its open issues, and the last change that touched it.
- `since <date or commit>` lists the requirements added, modified, and retired, and the packages changed under each area.
- `docs/about/status.md` is generated from the programs' milestones, in place of the roadmap.

Each is a skill over a command of `scripts/changes/changes.py` or `scripts/specshift`, as 0114 set for helpers.

### Successor stubs and the frozen reference

An area change replaces each migrated paragraph of a numbered section with a successor list under the same heading ([legacy-stubs.md](evidence/spike-migration/spec-next/legacy-stubs.md) shows the format), so a `§` citation still resolves to its heading and the heading names where the content went. `specshift ref lookup §N.M` follows the stub. The frozen reference keeps the original text readable: the git tag `spec-frozen` marks the commit before the first area migrates, and `lookup` prints the section's text from that tag (decision 7).

### Retiring the root plan files

`BUILD-PLAN.md` and `BUILD-PROGRESS.md` give way to the v1 program and the board, and `ROADMAP.md` to the program's milestones and the generated status page. The naming law's N3 domain and N8 in §28, `spec-citations.md`, `channel-naming.md`, `.github/PULL_REQUEST_TEMPLATE.md`, `README.md`, `CONTRIBUTING.md`, `docs/about/governance.md`, `docs/about/status.md`, `scripts/specshift/scope/scope.go` with its tests and test data, the scripts under `scripts/review-migration-diff/` and `scripts/restore-requirement-ids.py`, `tests/tier0_static/citation_document_test.go` and `naming_lint_test.go`, the scaffold tests of tiers 4, 8, and 9, and the three tier-7b load baselines that name them stop naming them. `CHANGELOG.md`, `BUILD-GAPS.md`, `TEST-GAPS.md`, and `CODE-REVIEW.md` keep the words they were written with.

## Current state

- `spec/` holds `README.md` and 29 numbered files, 493,345 words. §18 is the build sequence, §19 resolved decisions, §20 open questions, §21 planned post-v1 work, §22 explicit non-decisions, §23 the competitive landscape, and §26 the reference runtime catalog: content that TAXONOMY sends outside the specification.
- `scripts/specshift` has passes for anchors, citations, gates, identifiers, line citations, names, registers, and scope. The anchor pass rewrites retired anchors from a hand-written move map and fails closed on a site the map does not cover.
- 18,912 annotation lines cite the specification by section number, 12,075 in tests and 6,837 in other code.
- `docs/` holds 228 Markdown pages. `docs/reference/` lists settings, metrics, errors, events, and state machines by hand.
- `BUILD-PLAN.md`, `BUILD-PROGRESS.md`, and `ROADMAP.md` sit at the repository root, and 30 tracked files outside `changes/` and `proposals/` name them.

## Specification and non-specification changes

- Specification: `spec/README.md` as the root map, `spec/conventions.md`, the placeholder pages and generated indexes of the new tree, `spec/registry.json`, and `spec/catalogs/` (empty catalogs until areas add blocks). §28's naming law N3 and N8 lose the root plan files. §18 and §21 retire, and §23 moves to `docs/about/` (decision 9).
- Rules: `.claude/rules/spec-annotations.md` (new), `spec-citations.md`, `spec-driven-development.md`, `code-best-practices.md`, `doc-content.md`, and `channel-naming.md`.
- Code: `scripts/specshift` gains the reference pass, the block parser and generators, and the gates; `scripts/changes/changes.py` gains `digest` and `since`; tier-0 tests in `tests/tier0_static/`.
- Skills: `digest`, `where`, and `since`.
- Removed: `BUILD-PLAN.md`, `BUILD-PROGRESS.md`, and `ROADMAP.md`.

## Documentation

- Contributors: `CONTRIBUTING.md` and `docs/about/contributing.md` describe the specification's layout and point at `spec/README.md`; `docs/about/governance.md` names the programs in place of the roadmap.
- Every reader of `docs/about/status.md`: the page becomes generated from the program's milestones, with the same purpose.
- Pages under `docs/` gain front matter that names the specification pages they describe, area by area (decision 11). Readers see no change, because the renderer does not show it.

## Tests

- Tier 0: the reference pass (registry, `check` with planted errors, `fix` after a move, `lookup` of an identifier and of a `§` citation), the block parser and each kind's schema, the generators against fresh runs, each gate with a failing and a passing fixture, the annotation lint on a diff, the ratchet, the documentation front-matter gate, and the status page generator.
- Tier 11: the documentation gates over `docs/`.
- The existing citation gates keep passing: no `§` citation breaks, because no numbered heading changes in this change.

## Validation owed

See `steps.md`.
