# Steps: 0126 Delegation availability

## Validation owed

| Item | Why it matters | Status |
| :-- | :-- | :-- |
| Whether Claude Code and Codex list the platform server again on `notifications/tools/list_changed`, including for a tool removed that way | It sizes the stale-list window for a runtime that relays the notice through the bridge. The CA-9 spike saw Claude Code list again for an added tool only. | Open. It informs the "Limits" text only, because the refusal enforces the setting either way, and no step of this change waits on it. It belongs to 0139.S2, whose "Validation owed" carries the Codex half. |
| How long a runtime process outlives a session on a pod | It sized the window in which a new session held the previous session's list. | Closed by session isolation decision D4: no runtime process outlives its session. |
| That a per-session `tools/list` behind one pod-wide socket conforms to `2026-07-28`'s "MUST NOT vary per-connection" | Revision 9 argued that the list varied by the calling session. | Closed by the runtime protocol design: each bridge serves one slot's session, so no socket is shared across sessions. |
| Whether the tenant default is changed through `PUT /v1/admin/tenants/{id}` alone, or a sub-resource fits, as `elicitation-content-integrity` does | "DA-1" proposes a sub-resource. | Not a measurement. Moved to O10 under "Open decisions" in `decisions.md` on 2026-11-19. |
| Whether the per-tenant runtime record can hold `allowDelegation` beside its capability axes, or a sibling record keyed the same way fits better | "DA-1" places D1's entry on the existing record. The record's store and RV-1's narrowing rules are written for capability axes. | Not a measurement. Moved to O11 under "Open decisions" in `decisions.md` on 2026-11-19. |

## Steps

### 0126.S1 Delegation availability
- status: Planned
- aliases: [DA-A, DA-1, DA-2, DA-3, DA-4, DA-5, DA-6, DA-7]
- lanes: [specification, code, documentation]
- depends-on: [0119.S1, 0149.S3, 0149.S10, 0142.S2 (part)]
- closes: [F-8.4.4 (part)]

The step extends the calling-session parent and the allow-list that 0149.S3 (TS-3) adds, and the stored `maxDepth`, `DELEGATION_DISABLED`, and the list filter that 0149.S10 adds. It reads the runtime surface after 0119.S1 of [`0119-agent-runtimes-only`](../0119-agent-runtimes-only/steps.md#steps) removes `type: mcp`, so it carries no `allowDelegation` refusal for that type. It carries the specification edits; the levels, the resolver, and the refusal; the per-session list and its rules; the resolver that the `delegation` and `questions` values of "RP-4" read; the derived runtime rule and the tenant runtime entry; the derive narrowing; the explicit-zero per-hop switch and the extension rule; and the removal of the `approvalMode` argument and `DELEGATION_DENIED` with the proto reservation. Some parts read what other steps add, and the following records where each lands:

- The guardrail row of the resolution lands with or after 0142.S2 (the SO-2 proposal) of [`0142-security-oversight`](../0142-security-oversight/change.md#so-2-tenant-security-record-and-posture), which adds the record, hence the `(part)` entry. Whether the edge moves to a separate guardrail step of 0142 is O17 in `decisions.md`.
- The `ask_question` rule reads the session's `inputPolicy`, which R12 of [`0138-rest-only-client-api`](../0138-rest-only-client-api/change.md#ro-2-rest-additions) adds in 0138.S1. Revision 11 replaced the provider-surface rule of "MD-8", which 0135.S2 lands, with R12, and `lenny/request_input` leaves under E4, so the step no longer waits on 0135.S2 for either. 0138.S1 follows this step through 0135.S5, so an edge to it would close a cycle. Where the rule lands is O9 in `decisions.md`.
- The `capabilities.childSessions` condition and the tool names of "MD-23": 0135.S5 of [`0135-message-delivery`](../0135-message-delivery/steps.md#steps) depends on this step for the capability, and this step's text placed the condition with or after 0135.S5. Which step adds the capability is O9. The `message_child_session` rule of "DA-4" holds once 0135.S5 removes messaging to the parent and to siblings. Whichever step lands the condition also adds `capabilities.childSessions: true` to the reference runtimes in `charts/lenny/templates/reference-runtimes.yaml` whose harness passes the validation DAR-7 names, and to the runtime fixtures of the delegation tests.
- The `delegation` and `questions` values in the availability computation of "RP-4", the `contract` cases for the `list_tools` frame and `TOOL_NOT_FOUND`, and the `e2e_kind` notice test land with 0139.S2 (RP-B) of [`0139-runtime-protocol`](../0139-runtime-protocol/steps.md#steps), which adds the frames and `UpdateAvailability` and depends on this step. This step supplies the resolver they read.
- The derive narrowing adds `delegationLease.maxDepth` to the derive route that 0144.S1 of [`0144-session-derive`](../0144-session-derive/change.md#sd-1-one-route) reshapes. Neither step depends on the other, and whichever lands second adds the field to that route.

The step closes the self-refusal half of F-8.4.4 by removing the `approvalMode` argument ("DA-6"). The other half, that no path stores `deny` on a lease, stays open, and O12 in `decisions.md` asks which change closes it.

Under decision 8 of [0117](../0117-gateway-package-layout/decisions.md), this step edits `spec/interfaces/gateway-seams.md` and `pkg/gateway/seam` for the gateway seam operations it changes. It adds one operation served by the session core to the platform tools seam, which returns "may delegate", `disabledBy`, and the effective settings of "DA-3" for a session. The `start_child_session` handler and the delegation service call it for the refusal of "DA-3", and the tool bridge calls it for the per-session list of "DA-4": both are crossings from the platform tools seam to the session core that have no operation yet, because the seam may not import the tenant store. The resolver in `pkg/delegation` is a pure function the session core calls. 0139.S2, which depends on this step, reuses the operation for the availability of "RP-4" and adds none for it.

### Notes on the steps

Revision 3's DA-B, the notification work, is withdrawn under O4. The notice, the bridge's cache hints, and its `-32602` mapping land with the runtime protocol design's RP-B (0139.S2), which depends on 0126.S1.

The defect that `start_child_session` does not apply `DelegationPolicy` rules (F-8.3.14) is independent of 0126.S1 and DA-B and takes its own proposal. No member change lists it under `closes`, and O12 in `decisions.md` asks which change owns it. Whichever step fixes it evaluates the `DelegationPolicy` target rules at the post-chain point on the final runtime and pool, as the delegation-gates row of the post-chain table in [`0141-runtime-routing`](../0141-runtime-routing/change.md) states.
