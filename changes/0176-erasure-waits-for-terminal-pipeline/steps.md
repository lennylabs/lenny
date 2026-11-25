# Steps: 0176 Erasure waits for the terminal pipeline

## Validation owed

None.

## Steps

### 0176.S1 The pipeline mark and the erasure wait
- status: Building
- lanes: [specification, code, documentation]
- depends-on: []
- closes: [ERASE-I1]

§7.1 **Terminal pipeline context:** and **Post-terminal window:**, and §12.8 **Lifecycle fence and drain.**, as EW-4 in `change.md` states (decision 1). Then the migration adding `sessions.terminal_pipeline_done_at`, the field and the mutator in `pkg/session/sessionstore` (EW-1), the mark write at the end of `recordSessionCompleted`, `TerminalPipelinePending` and `admin.TerminalPipelinesPending` in place of `ReleasePending` for user erasure and tenant deletion (EW-2), and the unit, component, load_local, integration, and security tests. The digest lists the terminal writers that run no pipeline (EW-3). Then the "Erasure Process" list of `docs/operator-guide/security.md`.
