---
id: SUSP-I1
status: open
kind: spec-defect
severity: medium
requirements: []
---

# A resume the root's client asks for is undone at the restore commit of a session without a pod

## Specification

Change 0146 (session lifecycle), revision 24:

- SL-2: "`blocked` | The root's client resumes this session, admitted by resume admission | `idle` with a pod, or `resume_pending` without one", and the same for `suspended`.
- SL-2: "`resuming` | The restore commits | ... otherwise `blocked` under the nearest paused ancestor when the session has one."
- SLR-4: "A session that becomes live while it has a paused ancestor, at a setup commit, a restore commit, or a resume that the root's client did not request for that session, lands in `blocked` under its nearest paused ancestor."

## Evidence

- `tests/formal/sessionlifecycle/`, every configuration with a suspension: `DirectResumeHonoured` fails (counterexample R1 in `changes/0175-session-lifecycle-model/change.md`). Trace on the chain r, a: a's turn is cut by the idle clock and a becomes `dormant`, with no pod; the root's client suspends r, and a becomes `blocked` under r; the root's client resumes a, which goes to `resume_pending`; the restore commits and lands a in `blocked` under r again. A `blocked` session whose pod was released at the end of its hold reaches the same outcome.
- With a pod, the same resume moves a to `idle`, and it stays live.
- Found by step 0175.S1 on 2026-11-27.

## Gap

The outcome of the root's client's resume of a descendant depends on whether the descendant held its pod. The client sees the resume accepted and the session paused again once its restore commits, and the same happens when a resumed session's pod is lost before the ancestor resumes.

## Suggested resolution

Open decision O6 of change 0175: record the requested resume on the row so a restore commit lands the session live (recommended), refuse a direct resume under a paused ancestor, or state the limit.
