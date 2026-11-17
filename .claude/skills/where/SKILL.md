---
name: where
description: Show what the tree holds about one requirement, operation, definition block, or specification page - its text and page, the code sites and tests that cite it, the documentation pages that describe its page, the open issues that name it, and the last commit that changed its text - and answer follow-up questions about how the code meets it by reading the code it lists. Bound to the specification structure in spec/conventions.md. Use when the owner asks where a requirement is implemented, tested, or documented, or how the code meets it.
argument-hint: <identifier | page code | block anchor> [question]
---

# Where

Answer a question about one identifier from the tree. `go run ./scripts/specshift where` collects the facts, and this skill reads the code they point to.

## Arguments

- `<identifier | page code | block anchor>`: a requirement such as `POOL-R5`, an operation such as `ADMPOOLS-createPool`, a definition block's anchor such as `setting-maxidleseconds`, or a page code such as `POOL`.
- `[question]`: an optional follow-up, such as "does the claimer refuse a recycled pod of another tenant?".

## Steps

1. Run from the repository root:

   ```bash
   CGO_ENABLED=0 go run ./scripts/specshift where POOL-R5
   ```

   For an identifier it prints the text and page, the code sites and tests found by `// spec:` annotations naming it, the documentation pages whose front matter names its page, the open issues under `issues/<CODE>/` that name it, and the last commit that changed its text. For a page code it prints the page's summary, the identifiers it holds, the packages whose `doc.go` names the code, the documentation pages, the open issues, and the last commit to the page. For a retired identifier it prints the successor or the removal record.
2. Report what it printed. State plainly what it did not find: no code site, no test, or no documentation page. Most code still cites numbered sections, and most documentation pages carry no `spec` front matter yet, so an empty list means "not found by annotation" rather than "absent". For an area that has not migrated, search the code for the numbered sections listed under "Content today" on the page.
3. For a follow-up question, read the code sites and the tests it listed, and the code they call, and answer from that code. Cite each claim with a file and line. When the code and the requirement's text disagree, say so and name the issue that tracks the gap, or offer to file one.

When the command reports that a name is not registered, run `go run ./scripts/specshift ref lookup <name>`, which also resolves term anchors and numbered sections (`§N.M`).
