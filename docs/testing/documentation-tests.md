---
layout: default
title: "Documentation tests"
parent: "Testing"
nav_order: 6
description: Tier-11 doc tests — link integrity, code-block parsing, runbook structure, ADR continuity, schema-validated examples.
---

# Documentation tests

§16 names ten categories of documentation tests. Tier-11 runs them.

## Where they live

| File | What it checks |
|:-----|:----------|
| `tests/tier11_docs/docs_test.go` | Directory structure, TESTING.md tier coverage, repo README references, spec README presence |
| `tests/tier11_docs/code_blocks_test.go` | Parses every fenced Go / Bash / YAML / JSON / SQL block in docs/ and spec/ |
| `tests/tier11_docs/runbooks_test.go` | docs/runbooks/ format: front matter + Symptom / Diagnosis / Procedure / Verification |
| `tests/tier11_docs/adr_test.go` | ADR catalog continuity (numbering, references) |

External scripts wired into the static tier:
- `scripts/check-adr-catalog.sh` — shell version of the ADR check
- `scripts/check-doc-examples.sh` — JSON-schema validation of `docs/examples/`
- `scripts/check-markdown-links.sh` — `markdown-link-check` over `docs/` and `spec/` (non-fatal in static tier; PR workflow has the hard gate)

## Code-block validation

Every fenced code block carries a language tag. The walker dispatches each to the language's parser:

- **`go`** — wraps the block in `package docfragment` if missing, then runs `gofmt -e` to check syntax.
- **`bash` / `sh` / `shell`** — `bash -n` syntax check.
- **`yaml` / `yml`** — parses with `gopkg.in/yaml.v3`.
- **`json`** — parses with `encoding/json`.
- **`sql`** — heuristic check for statement terminators (`;`).

Blocks with **placeholders** are exempt automatically: `<slug>`, `<pool-name>`, `...`, `${VAR}` patterns. Authors can opt out explicitly:

````markdown
```go fragment
// Not a runnable program; an illustrative shape.
```
````

The `fragment` qualifier (or `snippet` / `expect=fragment`) skips the parser entirely.

## Runbook structure

`docs/runbooks/<slug>.md` must have YAML front matter:

```yaml
---
title: <Short title>
alert: <PromQL alert name or `none`>
severity: <P0 | P1 | P2 | P3>
---
```

…and four required sections:

- `## Symptom`
- `## Diagnosis`
- `## Procedure`
- `## Verification`

The test is informational today (every runbook is a placeholder); when the runbook catalog matures the gate flips to hard-fail.

## ADR catalog

Every `docs/adr/NNNN-<slug>.md` file must be referenced from `docs/adr/index.md`. The numeric sequence has no gaps and no duplicates. Both the Go test and the shell script enforce this.

## JSON-schema example validation

When `docs/examples/<schema-name>/*.json` exists, the matching schema must live at `schemas/<schema-name>.json` (or `<schema-name>-v1.json`). Each example must parse as valid JSON and validate against the schema. The shell script (`scripts/check-doc-examples.sh`) wraps this; the schema test (`tests/tier0_static/schemas_test.go`) validates the schemas themselves.

## Adding a new doc

1. Pick the right location: `docs/<area>/<name>.md` (or `docs/runbooks/<slug>.md` for runbooks, `docs/adr/NNNN-<slug>.md` for ADRs).
2. Use the canonical Jekyll front matter when the page should appear in navigation.
3. Code blocks default to "must parse" — add the `fragment` qualifier if the block is illustrative.
4. New runbooks ship the four required sections.
5. New ADRs increment the next sequence number and update `docs/adr/index.md`.

## Updating a golden code block

When a code block is the expected output of a command (e.g. an `lenny-ctl --help` excerpt), regenerate via:

```bash
LENNY_UPDATE_BASELINE=1 go test ./tests/tier11_docs/...
```

Today the docs tier has no golden blocks; this hook is for future use.

## Time-to-Hello-World

§22.7 ties the docs gate to `lenny-test stress --test TestTimeToHelloWorld --runs 50`. The TTHW test (in `tests/tier7b_load_kind/scenarios/`) measures how fast a fresh developer can run their first session. The 5-minute target is per the spec; CI exercises it under the §13.35 phase gate.
