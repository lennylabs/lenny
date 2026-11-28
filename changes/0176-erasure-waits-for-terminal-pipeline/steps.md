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
- status: Landed
- lanes: [specification, code, documentation]
- depends-on: [0176.S1]
- closes: [ERASE-I3]

Decisions 3, 4, and 6. The `session.completed` billing event of the terminal pipeline is written with `user_id` empty, §11.2.1's `user_id` row and the billing documentation state that a consumer joins it to `session.created` on `session_id`, and a test shows that a `session.completed` event appended after the erasure of its session's user carries no user identifier (decision 6). §7.1 names, in a new paragraph "Terminal writers and the pipeline steps:" after "Terminal pipeline context:", the terminal writers that run the session-log close, the final quota checkpoint, and the `session.completed` billing event, those that run the post-terminal hook and the pipeline mark without them, and those that run no pipeline (decision 4, issue SEND-I1 for the behaviour); §7.1 and §12.8 state the margin. Then an operator-tunable margin, default 30 seconds, on the budget arm of the erasure gate and the tenant-deletion terminator only (decision 3), with unit tests of the margin and the gate, and the configuration page and `docs/operator-guide/security.md`.

### 0176.S3 The periodic writers after an erasure, and the margin ceiling
- status: Planned
- lanes: [specification, code, documentation]
- depends-on: [0176.S2]
- closes: [ERASE-I7, ERASE-I8, ERASE-I9]

Decisions 7, 8, and 9. §11.2.1's `user_id` row states that `token_usage.checkpoint` leaves it empty, as `session.completed` does. Then `billingcheckpoint.Checkpoint` emits the event with an empty user id, with a unit test that the emitted event carries no user id and a test that a checkpoint event appended after the erasure of its session's user carries no user identifier. §12.8 names the periodic quota checkpoint among the fenced writers (decision 8), and §7.1 and §12.8 state the margin's ceiling (decision 9). Then the fence on the periodic quota write, with a test that a pass whose write follows the erasure leaves no quota row for the user, and the startup refusal of a margin above the terminal pipeline budget, with a unit test. Then the billing documentation's join on `session_id`, and the ceiling on the configuration page.
