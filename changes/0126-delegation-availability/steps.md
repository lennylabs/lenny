# Steps: 0126 Delegation availability

## Validation owed

| Item | Why it matters | Status |
| :-- | :-- | :-- |
| Whether Claude Code and Codex list the platform server again on `notifications/tools/list_changed`, including for a tool removed that way | It sizes the stale-list window for a runtime that relays the notice through the bridge. The CA-9 spike saw Claude Code list again for an added tool only. | Open |
| How long a runtime process outlives a session on a pod | It sized the window in which a new session held the previous session's list. | Closed by session isolation decision D4: no runtime process outlives its session. |
| That a per-session `tools/list` behind one pod-wide socket conforms to `2026-07-28`'s "MUST NOT vary per-connection" | Revision 9 argued that the list varied by the calling session. | Closed by the runtime protocol design: each bridge serves one slot's session, so no socket is shared across sessions. |
| Whether the tenant default is changed through `PUT /v1/admin/tenants/{id}` alone, or a sub-resource fits, as `elicitation-content-integrity` does | "DA-1" proposes a sub-resource. | Open, for the proposal |
| Whether the per-tenant runtime record can hold `allowDelegation` beside its capability axes, or a sibling record keyed the same way fits better | "DA-1" places D1's entry on the existing record. The record's store and RV-1's narrowing rules are written for capability axes. | Open, for the proposal |

## Steps

### 0126.S1 Delegation availability
- status: Planned
- aliases: [DA-A, DA-1, DA-2, DA-3, DA-4, DA-5, DA-6, DA-7]
- lanes: [specification, code, documentation]
- depends-on: [0149.S3]

The step extends the calling-session parent, the allow-list, `DELEGATION_DISABLED`, and the stored `maxDepth` that 0149.S3 (TS-3) adds. It carries the specification edits; the levels, the resolver, and the refusal; the per-session list and its rules; the `delegation` and `questions` values for the availability of "RP-4"; the derived runtime rule and the tenant runtime entry; the derive narrowing; the explicit-zero per-hop switch and the extension rule; and the removal of the `approvalMode` argument and `DELEGATION_DENIED` with the proto reservation. Three parts land with or after the steps that add what they read: the guardrail row of the resolution with 0142.S2 (the SO-2 proposal) of [`0142-security-oversight`](../0142-security-oversight/change.md#so-2-tenant-security-record-and-posture), which adds the record; the provider-surface rules for `lenny/request_input` and `ask_question` with 0135.S2 (MD-B) of [`0135-message-delivery`](../0135-message-delivery/steps.md#steps), which lands "MD-8"; and the `capabilities.childSessions` condition and the tool names with 0135.S5 (MD-E) of the same design, which lands "MD-22" and "MD-23".

### Notes on the steps

Revision 3's DA-B, the notification work, is withdrawn under O4. The notice, the bridge's cache hints, and its `-32602` mapping land with the runtime protocol design's RP-B (0139.S2), which depends on 0126.S1.

The defect that `start_child_session` does not apply `DelegationPolicy` rules (F-8.3.14) is independent of 0126.S1 and DA-B and takes its own proposal.
