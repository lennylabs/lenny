---
code: SESAREA
type: index
area: sessions
summary: >-
  Lists the capabilities that govern a session from creation to its end, including suspension, resume, checkpoints, derive, and isolation between sessions on one pod.
---

# Sessions

Lists the capabilities that govern a session from creation to its end, including suspension, resume, checkpoints, derive, and isolation between sessions on one pod.

## Pages

| Page | Code | Summary |
| :-- | :-- | :-- |
| [Derive](derive.md) | `SDERIVE` | Starts a session from an earlier one at its start or at its latest checkpoint, with the copy and restore rules and the options a derive takes. |
| [Pod reuse and scrub](pod-reuse-and-scrub.md) | `SCRUB` | States when a pod may serve another session, the scrub that runs between sessions, the reuse prohibitions, and the acknowledgements weak reuse requires. |
| [Retry and resume](retry-and-resume.md) | `RESUME` | States the retry policy after a pod failure, session generations, the resume flow, and the states a session takes when retries run out. |
| [Scrub procedure](scrub-procedure.md) | `SCRUBPROC` | States the scrub procedure step by step, the supervised scrub sequence, what the scrub reaches, the sweep, and the Kata variant. |
| [Session isolation](session-isolation.md) | `SLOT` | States the isolation between sessions that share a pod: slots, slot identity, slot release, the per-slot resource and credential scope, and slot failure. |
| [Setup commands](setup-commands.md) | `SETUP` | States the setup commands a runtime and a plan declare, their policy, ordering, limits, and failure. |
| [Slot assignment and failure](slot-assignment-and-failure.md) | `SLOTOPS` | States how a session is assigned a slot atomically, slot liveness and release, slot failure and retry, and the rehydration of slots after recovery. |
| [Checkpoints](checkpoints/index.md) | `CKIDX` | Lists the checkpoint pages: capture, storage and retention, and restore. |
| [Session lifecycle](session-lifecycle/index.md) | `SESLC` | Lists the pages of the session lifecycle: states, creation, suspension and dormancy, the end of a session, and timeouts. |
