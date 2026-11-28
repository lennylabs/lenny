---
id: ERASE-I9
status: planned
kind: question
severity: low
requirements: []
change: 0176.S3
---

# The pipeline wait margin has no upper bound

## Specification

§12.8 **Lifecycle fence and drain.**: user erasure and tenant deletion wait for the terminal pipeline budget plus the pipeline wait margin, "a gateway setting the deployer configures (default 30 s, and not negative)".

## Evidence

- Found by the security review of step 0176.S2 on 2026-11-27.
- `cmd/lenny-gateway/startupgates.go`, `validatePipelineWaitMargin`, refuses a negative margin and one whose sum with the budget overflows, and accepts any other value.
- A session whose writer runs no terminal pipeline (§7.1 **Terminal writers and the pipeline steps:**, item 3), and a pipeline cut by a gateway restart, record no pipeline mark, so each such session holds an erasure or a tenant deletion for the whole budget plus the margin.

## Gap

A margin entered in the wrong unit, such as `720h` meant as minutes, holds every erasure that drained such a session for 30 days, past the 72-hour T3 erasure deadline, with no startup error or warning.

## Suggested resolution

Open decision O7 of change [0176](../../changes/0176-erasure-waits-for-terminal-pipeline/decisions.md). Options: (a) refuse a margin above a fixed ceiling, such as the budget itself (recommended); (b) log a startup warning above a threshold; (c) keep the value unbounded and rely on the `ErasureJobOverdue` alert.
