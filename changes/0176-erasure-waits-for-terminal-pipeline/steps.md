# Steps: 0176 Erasure waits for the terminal pipeline

## Validation owed

None.

## Steps

### 0176.S1 The pipeline mark and the erasure wait
- status: Landed
- lanes: [specification, code, documentation]
- depends-on: []
- closes: [ERASE-I1]

§7.1 **Terminal pipeline context:** and **Post-terminal window:**, and §12.8 **Lifecycle fence and drain.**, as EW-4 in `change.md` states (decision 1). Then the migration adding `sessions.terminal_pipeline_done_at`, the field and the mutator in `pkg/session/sessionstore` (EW-1), the mark write at the end of `recordSessionCompleted`, `TerminalPipelinePending` and `admin.TerminalPipelinesPending` in place of `ReleasePending` for user erasure and tenant deletion (EW-2), and the unit, component, load_local, integration, and security tests. The digest lists the terminal writers that run no pipeline (EW-3). Then the "Erasure Process" list of `docs/operator-guide/security.md`.

### 0176.S2 The budget margin and the terminal writers in §7.1
- status: Planned
- lanes: [specification, code, documentation]
- depends-on: [0176.S1]
- closes: []

Decisions 3 and 4, and the owner's answer to open decision O2 once it is settled. §7.1 "Terminal pipeline context:" names the terminal writers that run the session-log close, the final quota checkpoint, and the `session.completed` billing event, and those that run the terminal write and the pipeline mark alone (decision 4, issue SEND-I1 for the behaviour); §7.1 and §12.8 state the margin. Then an operator-tunable margin, default 30 seconds, on the budget arm of the erasure gate and the tenant-deletion terminator only (decision 3), with unit tests of the margin and the gate, and the configuration page and `docs/operator-guide/security.md`.
