# Specification structure

Status: `Draft`, revision 1, 2026-11-16. Programs: [v1](../programs/v1.md), milestone M1.

## Problem

The specification has grown to about 493,000 words in 29 files, organized by section number, and agents struggle to piece facts together across it. The owner settled a structure organized by capability, with components and interfaces as their own page types and every fact stated once, after a spike that mapped the whole specification and migrated one area and its code ([evidence/spike/README.md](evidence/spike/README.md), [evidence/spike-migration/README.md](evidence/spike-migration/README.md)).

## Scope

This change lands the structure and its tooling: the root map and conventions, the code registry, the definition-block format, the reference tool, the gates, the generators (catalogs, indexes, `digest`, `where`, `since`, the board, and the status page), a frozen copy of the current specification so that old citations resolve, and the annotation and documentation rules. It also retires `BUILD-PLAN.md`, `BUILD-PROGRESS.md`, and `ROADMAP.md`, which the naming law in §28 and several tests and pages name. The areas migrate in their own changes.

## Open decisions

To be framed. The spike's taxonomy ([evidence/spike/TAXONOMY.md](evidence/spike/TAXONOMY.md)) and workflow record ([evidence/spike/WORKFLOW.md](evidence/spike/WORKFLOW.md)) hold the decisions the owner has settled.

## Steps

To be framed.
