# Test tiers by name and the static and documentation tiers

Status: `Implemented`, revision 2, 2026-11-17. Programs: [v1](../../programs/v1.md), milestone M1.

## Problem

The test tiers are numbered 0 to 12, and tier 7 is split into 7a and 7b, so a reader has to map numbers to meanings. `lenny-test` already selects tiers by name (`static`, `unit`, `component`, `contract`, `integration`, `e2e_kind`, `e2e_cloud`, `load_local`, `load_kind`, `chaos`, `security`, `conformance`, `docs`, and `load_cloud`), and the directories and prose use the numbers.

The static tier is also heavy and brittle under the specification restructure. 190 of its 322 tests check citations, naming, comment wording, or the maps, and one test takes 35 of its 56 seconds. Sixty documentation-tier files match prose inside numbered specification sections, so they break as each area migrates, and `tests/unit/` holds 13 files of unit tests away from the code they test ([test infrastructure review](../2026-11-17-0154-merge-gate/evidence/test-infrastructure-review.md), "Question 1", "Keep", "Cut", and "Merge").

## Scope

- **In scope.**
  - Tiers named instead of numbered, in directories, Go package names, `lenny-test`, the maps and registers, CI workflows, the rules, `TESTING.md`, and the documentation, with no split tiers: tier 7a and 7b are already the named tiers `load_local` and `load_kind`.
  - The static tier split into four packages (`build`, `chart`, `lint`, and `structure`) with a time budget.
  - The tier-0 category E gates retired as 0115's reference tool and gates cover them, and the 14 migration registers retired when the channel-naming migration closes.
  - A rule that a new documentation check is a general gate or a generator check, and that no test matches specification prose.
  - `tests/unit/` moved beside the code it tests.
  - The v1 program's area exit requiring each area to delete or replace its documentation-tier reconciliation tests and its gap-record tests.
- **Out of scope.** The merge gate (0154), coverage and selection (0116), and new kinds of tests (0156, 0157).

## High-level design

Each tier keeps the name `lenny-test` already gives it, and its directory takes that name: `tests/static/`, `tests/component/`, `tests/contract/`, and so on. Unit tests live beside their code, so `tests/unit/` does not exist. Ordering between tiers, which `--max-tier` expresses today, moves to the merge, area, and release gates of 0154. Old numbers stay in historical records (`changes/archive/`, `proposals/`, the gap records, and `CHANGELOG.md`), and `TESTING.md` keeps a table from each old number to its name.

## Current state

- 14 directories `tests/static/` to `tests/load_cloud/`, including `tests/load_local/` and `tests/load_kind/`.
- 760 Go files declare a `package tierN_...` clause, and about 1,460 tracked files outside the historical records mention a tier number in a path, a package name, or prose, in about 4,500 lines. Almost all of it is mechanical to rewrite.
- `cmd/lenny-test` maps each tier name to its numbered directory in `cmd_list_resolve.go` and `cmd_run_resolve.go`.

## Specification and non-specification changes

The specification names test tiers in §17 and in requirements that name their test tiers; those references move to the names, area by area where the text has migrated and here for the numbered files. Non-specification: the test directories and packages, `cmd/lenny-test`, `tests/change-graph.json` and the registers, `.github/workflows/`, `Makefile`, `.claude/rules/test-coverage.md`, the skills, and `TESTING.md`.

## Documentation

Contributors: `TESTING.md`, `docs/about/contributing.md`, and the testing pages under `docs/` use the tier names.

## Tests

The renamed tiers run under their names with the same test counts before and after; `lenny-test validate-maps` passes; the static tier's packages run concurrently within their time budget.
