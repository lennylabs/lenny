---
name: since
description: List the requirements and operations added, modified, and retired since a date or a commit, and the Go packages changed since then grouped by the specification area their doc.go names. Bound to the specification structure in spec/conventions.md. Use when the owner asks what changed in the specification or the code over a period, or before a review that covers a range.
argument-hint: <YYYY-MM-DD | commit>
---

# Since

Summarize what changed in the specification and the code since a point in history. `go run ./scripts/specshift since` reads the specification at that point and at the working tree, and the Go files changed between them.

## Arguments

- `<YYYY-MM-DD | commit>`: a date, read as the last commit on the first-parent line of `HEAD` before that day, or any commit, tag, or branch.

## Steps

1. Run from the repository root:

   ```bash
   CGO_ENABLED=0 go run ./scripts/specshift since 2026-11-01
   ```

   It prints the requirements and the operations added, modified, and retired, each with its title and page, a moved item with its old page, and a retired item with its successor or removal record. It then lists the Go packages whose files changed, grouped by the area of the specification pages their `doc.go` names. A package whose `doc.go` names no page code is listed as unmapped, and a package whose files were all deleted is marked removed.
2. Report the lists. Most packages name no page code yet, so most of them are unmapped until their area migrates; say so rather than guessing an area.
3. When the owner asks for detail on one item, run the `where` skill on it, or `git log` on its page for the commits in the range.
