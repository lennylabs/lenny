# Perspective 6 — Developer Experience (Runtime Authors) — Iteration 14

## Findings

### DXP-035 · Medium · `terminate` reason enum missing `task_complete` (factual error)

**Sections:** 4.7 (line 644) vs 5.2 (line 1857) and 15.4.1 (line 6743)

Section 5.2 describes the task-mode lifecycle as: "adapter sends `terminate(task_complete)` on lifecycle channel → runtime acknowledges." Section 15.4.1 repeats: "Adapter sends `{type: "terminate", reason: "task_complete"}` on the lifecycle channel after a task completes."

However, the lifecycle channel `terminate` message schema in Section 4.7 defines `reason` as a closed enum with exactly four values:

```
"session_complete" | "budget_exhausted" | "eviction" | "operator"
```

`"task_complete"` is not among them. Either the enum must be extended to include `"task_complete"`, or the task-mode references must use one of the existing values (most likely `"session_complete"`, but that is semantically wrong — a task completion is not a session completion in task mode where the pod persists across tasks).

**Fix:** Add `"task_complete"` to the `terminate` message `reason` enum in Section 4.7 (line 644).

---

### DXP-036 · Medium · Task-mode between-task signaling undefined for Minimum/Standard tiers (design contradiction)

**Sections:** 5.2 (line 1857), 15.4.1 (line 6743), 15.4.3 (line 7252)

The only documented between-task signaling mechanism is `terminate(task_complete)` on the lifecycle channel (Sections 5.2 and 15.4.1). But the Tier Comparison Matrix (Section 15.4.3) confirms the lifecycle channel is "N/A — operates in fallback-only mode" for both Minimum and Standard tiers.

The spec never restricts task mode to Full-tier runtimes, and execution modes are configured on the pool (Section 5.2), not the runtime tier. A deployer could configure a task-mode pool with a Minimum-tier runtime. The runtime would have no way to receive the between-task `terminate` signal because it has no lifecycle channel.

The `shutdown` message on stdin (the only process-termination signal available to Minimum/Standard tiers) is documented only with `reason: "drain"` and semantically means "exit the process permanently" — not "task complete, prepare for reuse."

**Fix:** Either (a) document the Minimum/Standard-tier fallback for task-mode between-task signaling (e.g., adapter sends `{type: "shutdown", reason: "task_complete"}` on stdin, kills and respawns the process for the next task, and add `"task_complete"` to the `shutdown` reason values), or (b) explicitly restrict task mode to Full-tier runtimes in the pool validation rules.
