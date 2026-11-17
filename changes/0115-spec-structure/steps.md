# Steps: 0115 Specification structure

## Validation owed

| Item | Waits on it |
| :-- | :-- |
| A question-answering comparison: the same questions answered by an agent from the numbered files and from the migrated pages of the first area, measuring correctness, tokens read, and time. The spike did not run one ([spike README](evidence/spike/README.md), "Limits"). | The order of the area milestones, which the program sets once this change lands. |
| The page sizes of the map are estimates; the slice's measured pages came out at 72 percent of them. | Nothing; the page-size report of the gates tracks it. |

## Steps

### 0115.S1 Conventions, the code registry, and placeholder pages
- status: Building
- lanes: [specification, documentation]

`spec/README.md` as the root map, `spec/conventions.md` from TAXONOMY version 2 and the spike's draft, the code registry with the map's 277 codes, and a placeholder page for each page of the map (decision 8), with front matter, a summary, and the numbered sections that hold its content. Contributor documentation names the layout.

### 0115.S2 The reference tool
- status: Building
- lanes: [code]
- depends-on: [0115.S1]

The `ref` pass of `scripts/specshift`: `registry`, `check`, `fix`, and `lookup`, the committed `spec/registry.json`, and tier-0 tests with planted errors and a move.

### 0115.S3 Definition blocks and generators
- status: Building
- lanes: [specification, code]
- depends-on: [0115.S1]

The `lenny-def` schemas per kind, the parser, the generators of `spec/catalogs/`, the folder indexes, and the generated component sections, and tier-0 tests that compare each generated file with a fresh run.

### 0115.S4 Gates
- status: Planned
- lanes: [code]
- depends-on: [0115.S2, 0115.S3]

The tier-0 gates of "Gates" in `change.md`, applied to pages of the new tree and to stubbed numbered sections, each with a failing and a passing fixture, and the scope of `scripts/specshift/scope/scope.go` extended to the new tree.

### 0115.S5 Annotation rule and its lint
- status: Building
- lanes: [code, documentation]
- depends-on: [0115.S2]

`.claude/rules/spec-annotations.md`, the edits to `spec-citations.md`, `spec-driven-development.md`, and `code-best-practices.md`, the tier-0 lint on new and changed lines, and the ratchet register of each area's old `§` citations.

### 0115.S6 Documentation rule and gates
- status: Planned
- lanes: [code, documentation]
- depends-on: [0115.S3, 0115.S4]

The extension of `doc-content.md`, the front-matter, catalog-name, and documentation-impact gates, and the generator of reference pages from the catalogs, turned on for a page once the area it describes has landed (decision 11).

### 0115.S7 Successor stubs and the frozen reference
- status: Planned
- lanes: [specification, code]
- depends-on: [0115.S2]

The stub format in `conventions.md`, a `specshift ref stub` command that replaces migrated paragraphs with a successor list, `lookup` of a `§` citation through the stub, and the `spec-frozen` tag (decision 7), created when the first area change starts.

### 0115.S8 Helpers that read the structure
- status: Planned
- lanes: [code, documentation]
- depends-on: [0115.S2, 0115.S3]

The `digest`, `where`, and `since` skills and their commands, and `docs/about/status.md` generated from the programs' milestones.

### 0115.S9 Retire the root plan files
- status: Planned
- lanes: [specification, code, documentation]
- depends-on: [0115.S8]

Remove `BUILD-PLAN.md`, `BUILD-PROGRESS.md`, and `ROADMAP.md`, §18 and §21, move §23 to `docs/about/` (decision 9), and edit §28, the rules, the scope and its tests, the scripts, the pull request template, and the pages listed under "Retiring the root plan files" in `change.md`. It follows 0115.S8 so the status page exists before the roadmap goes.
