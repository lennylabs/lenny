# Summary: Open Responses conversations run on a held session; Chat Completions stays single-shot

## Summary

**Problem statement.** Both built-in OpenAI-dialect adapters claim a warm pod, dispatch one turn, and release the pod within every HTTP call, as §15 and §29.3 require. A multi-turn Open Responses conversation therefore pays a pod claim, a credential lease, and a full lineage rehydration on every message. The owner decided on 2026-10-28 that an adapter conversation is a session: it keeps its pod across turns under the ordinary session timers, and a continuation that reaches another replica is forwarded to the holder. Response identity, lineage forks, and the shared transcript make this a design change rather than a deletion.

**What changes.**

- §15: the single-shot paragraph narrows to Chat Completions and points to a new paragraph, **Open Responses conversation sessions.**, which owns the held session, response identity, warm conditions, cold path, and failure rule (SPEC-1, SPEC-2).
- §29.3: the adapter off-holder row splits so a warm turn on another replica's held session is a forwarding row under the existing **Forwarding.** rules (SPEC-3).
- §28.5.4: the `CH-HOLDERFORWARD` card's **Messages.** bullet adds a §15 link (SPEC-4).
- Stores: transcript `Append` returns the last assigned Seq, and the session row carries per-response records and the creating request's runtime ref on the existing request-envelope bundle (CODE-1, CODE-2).
- Gateway: a new holder-forward kind and listener route, and an Open Responses handler that routes each request warm, forwarded, or cold (CODE-3, CODE-4, CODE-5).
- Docs: the Open Responses API page describes the held session (DOCS-1).

**Decisions.**

1. Chat Completions stays single-shot in v1 and keeps the label **Built-in adapter single-shot compute model.**, so chat-path citations keep resolving. The dialect is stateless, the client resends the history, no identity is on the wire, and there is no rehydration cost to save. A Chat Completions identity is a follow-up proposal.
2. The conversation is an ordinary session created through the existing `BindSingleShot` to `CreateAndStartService` path, left `running` with its pod after a successful turn. Only §6.2 and §7.2 govern it afterwards, and the proposal adds no new TTL or knob.
3. A continuation of a session that is not `running` goes cold. It does not resume through §7.2 delivery path 6, because for a podless suspended session that path holds the message and answers `queued`, which a synchronous dialect cannot return. The superseded suspended session holds no pod, does not count toward §11.1, and expires under `gateway.maxSuspendedSessionSeconds`.
4. A response id is `<sessionId>.<n>`. Records live in `Session.Responses` on the request-envelope jsonb beside `ContinuationParentID`, so there is no migration, no new store, and no new §12.8 step. `ContinuationParentID` keeps one meaning: the response the session's first turn continued from.
5. End sequences come from `Append`'s return value. Per-turn entry counts break when a REST delivery appends to the same bucket between turns, and a `Page` read after the append races a concurrent REST append.
6. Each response records the session's `recovery_generation`, and the warm path requires it unchanged, because an evicted and restored session is `running` with runtime memory rolled back to a checkpoint. A coordinator handoff never increments it.
7. A session that SPEC-2 paragraph A does not hold is released at the end of its turn. A service-mode (`conversationContinuity: "none"`) session keeps no context across its messages and cannot idle-suspend. A runtime that, for the session's tenant, is not `multi_turn` with injection supported accepts no second message (§5.1), so a held pod could not serve a warm turn.
8. The transcript record is required on this path. A best-effort record could return a response with no transcript entries, which breaks every later cold continuation through it. SPEC-2 paragraph D is the one failure rule.
9. A continuation the warm path cannot serve goes cold (SPEC-2 paragraph C). The reservation is a conditional `Update` under the row lock and is one-way: no path withdraws it.
10. Off-holder warm turns forward over `CH-HOLDERFORWARD` as SPEC-3 row 3 states. Not-delivered and unknown both fail closed as `409 TARGET_NOT_READY` in the native envelope, per the owner's "forwards rather than taking the cold path". A cold fallback would need a new rule-6 exception.
11. The native `409` carries `currentState` as `error.current_state` in the OpenAI error envelope, so rules 6 and 7 hold as written.
12. Resolution precedes claim: an unresolvable `previous_response_id` returns `404` before any bind. The row read is needed for the warm check anyway.
13. Warm turns write no idle stamp. A `CH-MSGSOCK` `message` write is adapter activity under §4.7.1 **Session seal.**, and §6.2 **Idle suspension.** advances `last_agent_activity_at` from the adapter's answer. A tier-4 test confirms it.
14. Items 6 through 8 of the problem need no mechanism. A held conversation is one session for warm-pool capacity, its §4.9 lease, §11.1 concurrency, recycling `maxSessionsPerPod`, and §4.6 eviction, and the §5.2 interim pinned-idle rule applies at its eventual release.
15. Isolation: a response is visible only within its session's tenant (SPEC-2 paragraph B), and a cross-tenant id is indistinguishable from an unknown one. The §10.2 per-owner restriction is not staged (see Defects). Warm condition 4's user equality keeps billing attribution consistent with the cold path. It is not an authorization boundary, because `user` is client-asserted.

**Watch out for.**

- Ordering in `handleCreate`: resolve, then forward, then reserve, then dispatch. A store write before the forward decision breaks §29.3 rule 1 (CODE-5 step 3).
- Call `ForwardResponsesTurn` on every forwarded request, whatever its warm verdict (CODE-5 step 3). Skipping it bypasses rule 5, and a replica that lost `REG-COORDLEASE` would then create a cold session, which is an effect rule 5 forbids.
- Capture `recovery_generation` where SPEC-2 paragraph D states, never at commit. Capturing it at commit reopens the eviction hole decision 6 closes.
- pgstore `Append` returns `maxSeq` only after the transaction commits.
- Do not validate the response-id prefix as a UUID in the translator. Existing fixtures inject non-UUID ids through `IDFunc`, and the store already maps a malformed id to not-found.
- CODE-1 changes an interface signature with many test callers and one consumer interface in `tests/testinfra/lifecyclefence`; the target list names them.
- The translator cannot import `sessionserver`. The forwarder is a consumer interface wired in `cmd/lenny-gateway/credentialsurface.go`.
- An idle-then-cold tier-4 test does not confirm decision 13; it passes even if warm turns never reset the clock. The test that discriminates spaces warm turns inside the window over a span longer than it.
- The SPEC-3 rows must each stay one physical line.

## Goals

- An Open Responses conversation keeps its session, pod, and lease across turns until the ordinary §6.2 and §7.2 machinery ends or suspends it.
- A continuation of the newest completed response on a `running`, unrecovered session runs warm, and every other continuation runs cold with the same conversation context.
- Response ids, `GET /v1/responses/{id}`, the lineage walk, and §12.8 erasure keep working with multi-turn sessions.
- A warm turn that lands on another replica is forwarded to the holder and fails closed when the forward fails.
- Chat Completions keeps its single-shot behavior and citations.

## Non-goals

- A Chat Completions conversation identity (for example a history hash). The dialect is stateless with no identity on the wire and no rehydration cost; it is a follow-up proposal per problem item 1.
- A separate pod-hold TTL or idle knob (the legacy 0061 draft design). The owner decision of 2026-10-28 rules it out.
- Resuming an idle-suspended conversation through §7.2 delivery path 6 (decision 3). A synchronous variant would need a new wait-for-resume surface, and the cold path already answers correctly.
- Making adapter sessions non-idle-suspendable so they expire at the idle bound. It needs a new marker and a new §6.2 clause and departs from the owner's "same timeouts as every other session" (decision 3).
- Terminating the superseded session when a continuation forks or goes cold. It adds a rule and a write for a session decision 3 already accounts for. A still-running superseded session idle-suspends on its own, and a terminal write from a non-coordinator replica conflicts with the §29.3 terminate row.
- Routing warm turns through the `POST /v1/sessions/{id}/messages` delivery core. Its inbox, DLQ, and `queued` outcomes cannot be expressed synchronously and could later deliver a turn the client never saw.
- A response id equal to the §15.4 message id or `session_messages.id`. A lookup by id alone needs a new tenant-scoped index, a tool-only or failed turn writes no assistant entry, and the identity would be lossy.
- A response id of `<session>.<transcriptSeq>` with no per-response record. GET could not echo `previous_response_id`, because nothing marks where the previous turn ended.
- A new per-response table or a migration adding columns. It needs a new store, RLS, and new §12.8 erasure coverage. The request-envelope bundle already carries `ContinuationParentID` for by-id reads.
- Per-response transcript buckets keyed by response id. `session_messages.session_id` references `sessions(id)`, and the session transcript view would lose the turns.
- One session row per response with the pod bound to the first row, or pod handoff between rows. Pod handoff is a new lifecycle mechanism, and record-only rows bypass the §7.2 state machine and double-count sessions for quota and billing.
- Bounding the rehydrate prefix by per-turn entry counts. Interleaved REST appends to the same bucket make the count wrong (decision 5).
- Reading the end Seq with `Store.Page` after the append. It races a concurrent REST append (decision 5).
- Serializing concurrent turns on `Server.LockDelivery` or an in-process per-session turn lock. The conditional `Update` under `FOR UPDATE` already gives a durable compare-and-set that survives a replica crash, and a lock adds latency for forks without adding correctness.
- A cross-replica lock or a separate turn-in-flight boolean. The incomplete reserved response record already serves as the one-way in-flight mark.
- Withdrawing a reserved response when the executor refuses before delivery. A restorable mark adds ABA and crash-window reasoning. Leaving it incomplete only stops warm turns on that session, which the timers then reclaim.
- Refusing forks or concurrent turns with `409`. That regresses today's correct cold service of any earlier response.
- Serving a not-delivered forward on the cold path. It needs a new exception to §29.3 **Forwarding.** rule 6 and contradicts the owner decision (decision 10).
- Reusing `ForwardKindMessage` as is. Its refusal writes the Lenny envelope, while §15 requires the adapter's native envelope.
- A new inter-replica RPC or frame for the held turn. Rule 2 already forwards the handler's request verbatim; only the listener route is missing.
- Stamping `last_agent_activity_at` from the adapter on each warm turn (decision 13).
- Requiring `recovery_generation == 0` for the warm path. Recording the generation per response is equally safe.
- Requiring that the transcript hold no entry after the head, or forbidding session-route message injection into conversation sessions. Injection through `/v1/sessions/{id}/messages` is same-tenant traffic outside the response lineage, and a later cold rehydrate includes interleaved entries inside the next turn's end-sequence bound.
- Leaving transcript recording best-effort on the Open Responses path (decision 8).
- Leaving a failed warm session to the timers without ending it. The warm turn runs on the coordinator, so `endHeldSession` ends it at once through the post-terminal hook (CODE-5 step 8).
- A `DELETE /v1/responses/{id}` conversation end. Open Responses DELETE removes a stored response rather than ending a conversation, so the route keeps its `405`. Terminate, idle suspension, and `maxSessionAge` end a session.
- Edits to §6.2, §7.2, §12.8, or §5.2. Each already governs an ordinary session.
- Staging anything for the unwritten pinned-idle inventory proposal or for recycling settings. A held pod is claimed and outside §5.2 pinned idle inventory until its release.
- Fixing the client-asserted `user` field as session owner. It is a pre-existing defect, listed below; the warm path neither fixes nor widens it.

## Open decisions for human to make

No decisions are open.

## Defects in the shipped tree that this proposal does not stage

- **Client-asserted session owner on the built-in adapters.** `CreateAndStartService` uses `req.UserID` as given (`pkg/gateway/sessionserver/create_and_start.go`), and the Open Responses adapter fills it from the request body's `user` field (`pkg/gateway/environment/translator/open_responses.go`, `handleCreate`). A client can therefore attribute a session to any user id within its tenant. The fix belongs to the adapter authentication model and touches the Chat Completions path equally, so it is out of scope here. File it in BUILD-GAPS.
- **No per-session-owner check for `user`-role reads.** §10.2 limits a `user`-role caller to its own sessions, but session reads check the permission and then read tenant-scoped only (`handleGet` in `pkg/gateway/sessionserver/session_read.go`). SPEC-2 paragraph B and CODE-4 scope `GET /v1/responses/{id}` and `previous_response_id` resolution to the tenant to match, so the gap covers the staged spec text as well as the code. On the built-in adapters the check depends on the owner fix above, because the stored owner is client-asserted. File it in BUILD-GAPS.
- **Forwarded requests on a row outside the kind's forward states run locally.** `forwardTarget` (`pkg/gateway/sessionserver/holder_forward.go`) tests `forwardGated` before the `Forwarded(ctx)` branch for every existing kind, so a forwarded request whose re-read row is outside that kind's forward states runs at a non-holder, against §29.3 Forwarding rule 5. Reordering alone does not fix it: the REST and MCP message callers hold every not-delivered `ForwardKindMessageNoReplyTo` in the DLQ without checking the row state, and rule 6 holds only for a `running` or `input_required` session. File it in BUILD-GAPS.

## Impacts on other proposals

| Proposal | Status | What this change does to it | What it must do |
|:--|:--|:--|:--|
| 0061 (`proposals/0061_draft_single-shot-adapter-pod-hold-idle-ttl.md`) | Draft (legacy, unconverged) | Supersedes it; its sticky-pod-with-own-TTL design is replaced by the owner decision | Be retired as superseded by 0108 |
| 0057 (single-shot pod binding) | Implemented | Narrows its §15 single-shot paragraph to Chat Completions | Nothing; the record stands |
| 0059 (`previous_response_id` continuation) | Implemented | Replaces its single-turn-per-session transcript storage and its response-id-equals-session-id identity | Nothing; the record stands |
| 0101 (CH-HOLDERFORWARD) | Implemented | Adds one forward kind and one listener route under its rules, and applies `forwardTarget`'s rule-5 check ahead of its state gate for that kind | Nothing |
| 0105 (per-state deadlines and watchdog sweeps) | Implemented | Held adapter sessions fall under its deadlines and sweeps unchanged | Nothing |
| 0106 (delegation-tree membership and lifecycle) | Approved | Both edit rows of `terminal_writer_inventory_test.go` and `terminal_context_internal_test.go` (0106 CODE-B, CODE-C, and CODE-E, 0108 CODE-5) | A textual merge by whichever proposal lands second |
| 0079 (recycled-pod runtime start, §5.2 interim pinned-idle rule) | Implemented | A held adapter pod is claimed and meets the interim rule at its release | Nothing |
| 0107 (delegation-tree budget lifecycle) | Approved (2026-10-28, status file `approved-date`) | Leaves every 0107 deliverable with its subject. The two proposals edit different sections of spec/28 and spec/29: 0107 SPEC-10 edits §28.5.2 and §29.4, and 0108 edits §28.5.4 (SPEC-4) and §29.3 (SPEC-3). CODE-5 moves the Open Responses terminal writer from `handleCreate` to `serveCold`, which still runs no post-terminal hook, and adds `endHeldSession`, which runs `AfterTerminalWrite` with nil signals inline under its own claim (CODE-5 step 8). Both proposals edit `pkg/gateway/session/sessionstore` with its `memstore` and `pgstore` (0107 CODE-8, 0108 CODE-2) and `terminal_writer_inventory_test.go` (0107 CODE-2, 0108 CODE-5) | Whichever proposal lands second merges those files, names `serveCold` in place of `handleCreate` among 0107 CODE-3's hookless writers, and gives `endHeldSession` the dispatch of the CODE-3 `lenny/cancel_session` row |
| Pinned-idle inventory proposal | Not yet written | Held adapter sessions are claimed pods for as long as they are held | Count a held adapter session as a claimed pod, as any session |

## Deliverable index

- SPEC-1 — spec/15_external-api-surface.md — narrow **Built-in adapter single-shot compute model.** to Chat Completions and add the successor pointer.
- SPEC-2 — spec/15_external-api-surface.md — new paragraph **Open Responses conversation sessions.**
- SPEC-3 — spec/29_communication-scenarios.md — split the §29.3 adapter off-holder row and edit the lead-in clause.
- SPEC-4 — spec/28_communication-channels.md — add the §15 link to the §28.5.4 `CH-HOLDERFORWARD` card's **Messages.** bullet.
- CODE-1 — pkg/gateway/environment/transcriptstore (transcriptstore.go, pgstore/pgstore.go) and its callers — `Append` returns the last assigned Seq.
- CODE-2 — pkg/gateway/session/sessionstore (sessionstore.go, pgstore, memstore) — `Session.Responses` and `Session.RequestedRuntimeRef` on the request-envelope bundle.
- CODE-3 — pkg/gateway/sessionserver/holder_forward.go and cmd/lenny-gateway/holder_forward_listener.go — `ForwardKindResponsesTurn` and the `POST /v1/responses` listener route.
- CODE-4 — pkg/gateway/environment/translator (responseid.go, continuity.go, open_responses.go) — response-id codec, Seq-bounded rehydrate, required record, and GET.
- CODE-5 — pkg/gateway/environment/translator (open_responses.go, openai_chat.go, singleshot.go), cmd/lenny-gateway/credentialsurface.go, and the pkg/gateway/sessionserver terminal-writer inventory tests — warm, forward, and cold routing in `handleCreate`.
- DOCS-1 — docs/api/open-responses.md — describe the held conversation session.
- TEST-1 — tests across tiers 1, 2, 3, 4, 7a, 8, 9, and 11 (non-spec-changes `## Testing`) — cross-tier cases for SPEC-2 and SPEC-3.
