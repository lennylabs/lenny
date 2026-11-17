# Helpers

Status: `Draft`, revision 1, 2026-11-16. Programs: [v1](../programs/v1.md), milestone M0.

## Problem

The proposal pipeline under `.claude/workflows/` (11 workflows, 19,453 lines) converges each proposal through many reviewer lenses and rounds, and in proposals 0082 to 0112 the review log is 70 to 85 percent of a proposal's words. The owner replaced it on 2026-11-16 with a lighter loop and a set of helpers that the owner uses as needed ([changes README](../README.md), "Workflow" and "Helpers").

## Scope

This change builds the first helpers: `review`, `tidy`, `fact-check`, `ask`, `change`, and `build`. They run against the current specification, so later changes can use them before the specification structure lands. The helpers that read the new structure (`digest`, `where`, `since`, and `board`) come with 0115, and `triage` and `issue` come when the first area's findings migrate. The old workflows retire once nothing in flight depends on them.
