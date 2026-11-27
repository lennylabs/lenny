---
id: SEND-I1
status: open
kind: spec-defect
severity: medium
requirements: []
---

# Some terminal writers run no quota checkpoint, billing event, or session-log close

## Specification

§7.1 "Terminal pipeline context:", as step 0176.S1 edits it, lists the final quota checkpoint and the `session.completed` billing event among the steps of the terminal pipeline.

## Evidence

- Found by the review of step 0176.S1 (2026-11-25), recorded as open decision O4 of change 0176.
- Step 0176.S2 (decision 4 of change 0176) qualifies §7.1: the new paragraph **Terminal writers and the pipeline steps:** names the writers that run the whole pipeline, those that run the steps up to the delegation budget return and the pipeline mark, and those that run no pipeline. The question of whether every writer should run the whole pipeline stays with this issue.
- The writers that call `AfterTerminalWrite` directly rather than `recordSessionCompleted` run none of the later steps: `failSession`, `failFinalizing`, `failStarting`, the cascade cancel, the cancel in `mcptools`, and `endHeldSession` of the Open Responses surface (`pkg/gateway/core/session/sessionserver/` and the platform tools seam).

## Gap

The specification implies that every terminal session gets a final quota checkpoint and a `session.completed` billing event, and the code gives neither to a session that ends through one of those writers, so its usage is not closed out in billing or quota.

## Suggested resolution

Decide whether every terminal writer runs the full pipeline. If not, qualify §7.1 to name the writers that run only the terminal write; if so, route those writers through the full pipeline, with a test per writer.
