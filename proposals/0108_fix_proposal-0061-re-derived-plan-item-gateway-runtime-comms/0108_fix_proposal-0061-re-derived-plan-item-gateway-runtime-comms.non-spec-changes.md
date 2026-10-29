# Non-spec changes — Built-in OpenAI-dialect adapter conversations claim a pod per turn

## Design (implementation-facing)

The normative rules are in spec-changes SPEC-2 (§15 **Open Responses conversation sessions.**) and SPEC-3 (§29.3 rows). This section maps them onto code.

- `transcriptstore.Store.Append` returns the last Seq it assigned (CODE-1). That value is a response's end sequence.
- `sessionstore.Session` gains `Responses []ConversationResponse` on the existing request-envelope jsonb bundle (CODE-2). No migration is needed.
- `sessionserver` gains `ForwardKindResponsesTurn` and an exported `ForwardResponsesTurn`, and the holder-forward listener mounts `POST /v1/responses` (CODE-3).
- The translator gains the response-id codec, an end-sequence-bounded rehydrate, a required transcript record, and a GET that answers only completed responses (CODE-4).
- `handleCreate` splits into resolve, forward, serveWarm, and serveCold (CODE-5). The order is fixed: resolve (no store write, no bind), then forward, then reservation, then dispatch.
- The Chat Completions handler (`OpenAIChatHandler.handleCreateCompletion` in `openai_chat.go`) is unchanged.

The translator cannot import `sessionserver` (import cycle). Every sessionserver capability the translator needs is a consumer interface in the translator, implemented by an adapter type in `cmd/lenny-gateway/credentialsurface.go` beside `sessionSingleShotBinder`.

## Staged code changes

### CODE-1 · transcriptstore Append returns the last assigned Seq

Change `transcriptstore.Store.Append` in place to:

```go
Append(ctx context.Context, tenantID, sessionID string, entries ...Entry) (lastSeq uint64, err error)
```

- A call with no entries returns `(0, nil)`.
- `Memory.Append` returns the Seq it assigned to the last entry.
- `pgstore.Append` returns the `maxSeq` it computed under its advisory lock, and only after `pgtenant.InTx` commits. A failed transaction returns `(0, err)`.
- The interface doc comment keeps its §15.1 citation and adds `§15 (Open Responses conversation sessions)`.

Targets:

- `pkg/gateway/environment/transcriptstore/transcriptstore.go` (interface, `Memory.Append`, doc comment).
- `pkg/gateway/environment/transcriptstore/pgstore/pgstore.go` (`Append`).
- `pkg/gateway/sessionserver/messages_delivery.go`, the `Append` call in `recordDeliveredResponse`: change to `_, _ =`. It stays best-effort.
- `pkg/gateway/environment/translator/continuity.go` (`record` consumes the value under CODE-4).
- The consumer interface `Transcripts` in `tests/testinfra/lifecyclefence/stores.go` and its caller in `tests/testinfra/lifecyclefence/world.go`.
- The fake `appendFailTranscripts` in `pkg/gateway/environment/translator/open_responses_test.go`.
- Every test call site that uses `err :=` or `_ =` with `Append`: `cmd/lenny-gateway/stores_fence_wiring_test.go`, `pkg/gateway/environment/translator/continuity_test.go`, `pkg/gateway/session/sessionstore/memstore/create_with_transcript_test.go`, `pkg/gateway/sessionserver/creation_fence_test.go`, `pkg/gateway/sessionserver/messages_list_test.go`, `pkg/gateway/sessionserver/replay_test.go`, `pkg/gateway/sessionserver/taskrecord_test.go`, `pkg/gateway/storage/sqlitestore/stores_roundtrip_test.go`, `tests/tier2_component/stores/sessionstore_create_with_transcript_test.go`, and `tests/tier7a_load_local/replay_transcript_erasure_race_test.go`.

Do not read the end sequence with `Store.Page` or `Get` after the append: that races a concurrent REST append to the same bucket. Do not add a second `AppendReturningSeq` method beside `Append`.

### CODE-2 · sessionstore: per-response records on the request-envelope bundle

Targets: `pkg/gateway/session/sessionstore/sessionstore.go`, `pkg/gateway/session/sessionstore/pgstore/pgstore.go`, and `pkg/gateway/session/sessionstore/memstore/memstore.go`.

```go
// ConversationResponse is one Open Responses turn on a session.
// spec: §15 (Open Responses conversation sessions)
type ConversationResponse struct {
	CreatedAt          time.Time `json:"createdAt"`
	EndSeq             uint64    `json:"endSeq"`
	RecoveryGeneration int64     `json:"recoveryGeneration"`
	Completed          bool      `json:"completed"`
}
```

- `Session` gains `Responses []ConversationResponse`. The ordinal of `Responses[i]` is `i+1`.
- pgstore: add `Responses` to `storedEnvelope` and `requestEnvelopeArg` with json `responses,omitempty`, include it in the all-empty NULL check, and copy it in `applyStoredEnvelope`.
- memstore clones the slice on every read and write, so a caller cannot alias stored state.
- Rewrite the `ContinuationParentID` doc comment: it is the response id (`<sessionId>.<n>`) that this session's first turn continued from, empty for a conversation's first session, and distinct from `ParentSessionID`, which belongs to delegation. Cite §15 (Open Responses conversation sessions).

**IMPLEMENTOR'S CHOICE:** the Go type of `RecoveryGeneration`. It matches the type of `Session.RecoveryGeneration` exactly, so the warm condition compares without conversion.

### CODE-3 · sessionserver: ForwardKindResponsesTurn with caller-rendered refusal; listener mounts /v1/responses

Targets: `pkg/gateway/sessionserver/holder_forward.go`, `cmd/lenny-gateway/holder_forward_listener.go` (`holderForwardInner` and its caller), and the `cmd/lenny-gateway` call site that passes the responses handler (`w.responsesHandler`).

1. Add `ForwardKindResponsesTurn` to the `ForwardKind` consts and to `forwardStates` gated on `{session.StateRunning: true}`.
2. `refuseForward`: for `ForwardKindResponsesTurn`, call `idemmw.DoNotStore(ctx)` and return without writing, on both `ForwardNotDelivered` and `ForwardUnknown`. This keeps the §11.5 no-store obligation of §29.3 **Forwarding.** rules 6 and 7 inside sessionserver while the caller renders the native envelope.
3. Add the exported production wrapper:

   ```go
   // ForwardResponsesTurn forwards an Open Responses warm turn on row to the
   // session's coordinating replica. The caller calls it on every warm-eligible
   // request, including one where Forwarded(ctx) is true: forwardTarget applies
   // §29.3 Forwarding rule 4 (never dials again) and rule 5 (ForwardNotDelivered
   // when this replica does not hold REG-COORDLEASE). On ForwardNotDelivered
   // and ForwardUnknown nothing is written, and the caller writes the native
   // 409 and returns.
   // spec: §29.3 (Interactive message send) Forwarding; §15 (Open Responses conversation sessions)
   func (s *Server) ForwardResponsesTurn(w http.ResponseWriter, r *http.Request, row sessionstore.Session, body []byte) ForwardOutcome
   ```

   It calls `forwardToHolder` with `bytes.NewReader(body)`, because `handleCreate` has already read the request body.
4. Update the `forwardToHolder` doc comment so the caller-renders exception names `ForwardKindResponsesTurn` for both refusal outcomes beside `ForwardKindMessageNoReplyTo` not-delivered.
5. `holderForwardInner` takes the responses handler as a third argument and mounts `"POST /v1/responses"` on it. `GET /v1/responses/{id}` is not mounted, because it reads stores only.

### CODE-4 · translator: response id, Seq-bounded rehydrate, required record, GET

Targets: `pkg/gateway/environment/translator/responseid.go` (new), `pkg/gateway/environment/translator/continuity.go`, and `pkg/gateway/environment/translator/open_responses.go` (`handleGet`, `buildOpenResponsesResponse`, `writeOpenResponsesStream` callers, `mapStateToResponsesStatus`).

1. `formatResponseID(sessionID string, n int) string` returns `sessionID + "." + strconv.Itoa(n)`.
2. `parseResponseID(id string) (sessionID string, n int, ok bool)` splits on the last `.`, requires a non-empty session-id prefix and a decimal ordinal `n >= 1`, and reports `ok == false` for a missing separator, an empty prefix, or a bad ordinal, before any store call. It does not validate the prefix as a UUID: the tenant-scoped `sessionstore.Get` already reads a malformed id as `ErrNotFound` (pgstore `normalizeMiss` maps SQLSTATE 22P02), so malformed, unknown, and cross-tenant prefixes answer the same native `404`, and test fixtures that inject non-UUID ids through `OpenResponsesOptions.IDFunc` keep working.
3. `rehydrate(ctx, tenantID, responseID)` walks `(S, n)` pairs:
   - First hop: an unparsable id, a session miss, `n > len(S.Responses)`, or `!S.Responses[n-1].Completed` returns `errContinuationNotFound`.
   - Each hop reads S's transcript entries with `Seq <= S.Responses[n-1].EndSeq`, prepends them, then continues at `S.ContinuationParentID` parsed the same way. An empty `ContinuationParentID` ends the walk.
   - A later hop that misses (erased session, unparsable parent, out-of-range or not-completed ordinal) stops the walk at that gap and returns what it has.
   - The visited guard is keyed by session id.
   - Export a resolve step that returns the parsed `(S row, n)` for CODE-5, so the handler reads the row once for both the warm check and the 404 decision.
4. `record(ctx, tenantID, sessionID, msgs, out) (endSeq uint64, err error)` appends the turn's input and output and returns `Append`'s last Seq. It is required on this path: CODE-5 treats an error as a post-delivery failure.
5. Rewrite the `continuity` type and `record` doc comments, which describe single-turn buckets and a best-effort write, to describe the end-sequence-bounded per-session prefix and the required record. Cite §15 (Open Responses conversation sessions).
6. `handleGet` parses the id, does a tenant-scoped `Get`, and returns the native `404` when the id is unparsable, the session is missing, the ordinal is out of range, or the response is not completed. It returns `status: "completed"` with `id = formatResponseID(S, n)` and `previous_response_id` equal to `formatResponseID(S, n-1)` for `n > 1` or `S.ContinuationParentID` for `n == 1`.
7. Delete `mapStateToResponsesStatus` unless another caller remains.

### CODE-5 · translator: handleCreate holds the session and routes warm, forward, or cold

Targets: `pkg/gateway/environment/translator/open_responses.go` (`handleCreate` split into resolve, serveWarm, and serveCold; `OpenResponsesOptions.HolderForwarder`), `pkg/gateway/environment/translator/openai_chat.go` (`endHeldSession` beside `releaseSingleShot`; `OpenAIErrorBody`), `pkg/gateway/environment/translator/singleshot.go` (consumer interface `HolderForwarder`; binder return value), and `cmd/lenny-gateway/credentialsurface.go` (adapter over `*sessionserver.Server` implementing `translator.HolderForwarder`, wired beside `sessionSingleShotBinder`).

Steps, in this order:

1. **Resolve.** Normalize input (`400` on failure) and, when `previous_response_id` is set, resolve it through CODE-4's resolve step. A not-found answers the native `404`. No bind, no store write, and no forward happen before this point.
2. **Warm eligibility.** Evaluate SPEC-2's warm conditions 1 through 4 on the resolved row against the request's `req.User`, runtime ref, and environment. A request with no `previous_response_id`, or one that fails any condition, goes to step 6.
3. **Forward.** Call `HolderForwarder.ForwardResponsesTurn(w, r, row, body)` on every warm-eligible request, whether or not `sessionserver.Forwarded(ctx)` is true. On `ForwardDelivered` return. On `ForwardNotDelivered` or `ForwardUnknown`, write `409` with `writeOpenAIErrorWithCode` code `TARGET_NOT_READY` and `current_state` set to `string(row.State)`, and return; never fall through to serveWarm or serveCold. On `ForwardLocal` continue.
4. **Native `currentState`.** `OpenAIErrorBody` gains `CurrentState string \`json:"current_state,omitempty"\``. This is the field that carries §29.3's `currentState` in the native envelope. `writeOpenAIErrorWithCode` (or a sibling writer) sets it.
5. **serveWarm.**
   - Reservation: one `sessionstore.Update` whose mutator re-checks conditions 1 through 4 under the row lock and appends `{CreatedAt: now, RecoveryGeneration: s.RecoveryGeneration, Completed: false}`, capturing that generation as `resGen`. A failed re-check returns a sentinel error, writes nothing, and sends the request to step 6.
   - Dispatch: `exec.Send` the new input only. On `executor.ErrNotBound`, or `*executor.SealedError` with `Delivered == 0`, go to step 6 in the same request; the reservation stays incomplete. On any other `Send` error, call `endHeldSession` and answer `500`.
   - Record: `endSeq, err := record(...)`. On error, `endHeldSession` and `500`.
   - Commit: one `Update` that sets `EndSeq = endSeq` and `Completed = true` on ordinal `n+1` only while `s.RecoveryGeneration == resGen`. The commit never rewrites `RecoveryGeneration`. A mismatch or a failed write is `endHeldSession` and `500`.
   - Respond with id `formatResponseID(S, n+1)` and `previous_response_id` the request's.
6. **serveCold.**
   - Bind through `BindSingleShot`. Extend `SingleShotBinder.BindSingleShot` to return the new session's `RecoveryGeneration` (`bindGen`) and a `hold` bool along with the session id. `sessionSingleShotBinder` sets `hold` only when SPEC-2 paragraph A holds the session. It takes continuity from the create response and resolves the runtime through `runtimecapoverride.ResolveForTenant` (as `requireInjectionSupported` resolves it, with the binder given `w.runtimes` and `w.capOverrides`). A resolve error or not-found sets `hold = false`. The no-op binder returns `hold = false`. The `openai_chat.go` call site ignores `hold`. Update every other implementer and caller of the changed signature: `stubSingleShotBinder` (`openai_chat_test.go`), `ssSingleShotBinder` (`tests/tier2_component/translators/openai_singleshot_lifecycle_test.go`), `singleShotBinder` (`tests/tier7a_load_local/scenarios/singleshot_admission_ordering/scenario.go`), the callers in `singleshot_test.go`, and the `sessionSingleShotBinder` literals in `credentialsurface_singleshot_test.go` and `caller_check_wiring_test.go`, which also need `runtimes` and `capOverrides`. Each stub returns the `hold` value its test expects, because a stub returning `false` silently turns a held-session test into a release test.
   - Arm the deferred `releaseSingleShot` with `disp = failed`.
   - Dispatch `exec.Send(prior + new input)`, where `prior` is the CODE-4 rehydrate of `previous_response_id` (empty for a root request).
   - Record and commit as in step 5, except that the commit appends `{CreatedAt, EndSeq, RecoveryGeneration: bindGen, Completed: true}` as ordinal 1 and sets `ContinuationParentID = previous_response_id`, conditioned on `s.RecoveryGeneration == bindGen`.
   - Hold or release: when `hold` is false, write `completed` (the existing completion write) and let the deferred release run with `disp = completed`. Otherwise disarm the deferred release so the session stays `running` with its pod and lease.
   - Respond with id `formatResponseID(newSession, 1)`.
7. **One release owner per exit.** The deferred `releaseSingleShot` stays armed until `Send` returns. On a `Send` error in serveCold, the deferred release alone ends the session: the handler writes the `failed` row and returns. `endHeldSession` is called only for a failure after a successful `Send` (or for any warm `Send` failure after delivery), and it disarms any armed deferred release first. No path releases twice.
8. `endHeldSession(ctx, tenantID, sessionID)` writes `failed` with `MarkTerminated` and calls `releaseSingleShot` with the failed disposition, on the same detached timeout-bounded context `releaseSingleShot` uses.
9. Always wire the `cmd/lenny-gateway` forwarder adapter. There is no nil-forwarder mode: `forwardToHolder` already returns `ForwardLocal` through `coordinatesEverySession()` and `ForwardNotDelivered` when no forward client exists.
10. Retarget the `// spec: §15 built-in adapter single-shot compute model` citations in `continuity.go`, `open_responses.go`, `open_responses_test.go`, and `tests/tier3_contract/rest_sessions/openapi_document_test.go` to `§15 (Open Responses conversation sessions)` wherever they describe held-session or lineage behavior. Citations that describe the creation gates, atomicity, or 503 mapping keep the single-shot label. Chat-path citations in `openai_chat.go` and `singleshot.go` keep the single-shot label.

The warm path writes no `last_agent_activity_at` stamp (SPEC-2 paragraph D).

## Staged schema, chart, and migration changes

None. The response records ride the existing request-envelope jsonb bundle (CODE-2).

## Staged docs changes

### DOCS-1 · docs/api/open-responses.md

No section numbers in any of this text.

**(a) `### POST /v1/responses` intro.** Replace "Create a response. The gateway creates a session, runs the specified runtime, and returns the result in Open Responses format." with:

```markdown
Create a response. A request without `previous_response_id` creates a conversation session, runs the specified runtime, and returns the result in Open Responses format. A request with `previous_response_id` continues the conversation, either on the session that produced that response or on a new session, as [Multi-turn via `previous_response_id`](#multi-turn-via-previous_response_id) describes.
```

**(b) `## Session mapping`.** Replace the lead-in sentence and the ASCII diagram with:

````markdown
Each Open Responses conversation maps to a Lenny session. The first request creates the session, and the session keeps its pod between turns, so the runtime keeps its memory of the conversation.

```
POST /v1/responses (no previous_response_id)
  |
  v
create_session(runtime=model)
  |
  v
finalize_workspace()  (empty workspace)
  |
  v
start_session(message=input)
  |
  v
[stream output or wait for completion]
  |
  v
Return Open Responses format response
(the session stays running and keeps its pod)
```

The session follows the same idle, suspension, and maximum-age limits as any other Lenny session. A session that stays idle past the idle limit is suspended and releases its pod. A pool that runs in service mode keeps no conversation context between requests, and a runtime that is not `multi_turn` with injection enabled for the tenant accepts no second message, so in either case the session ends after every response.
````

**(c) `### Response status mapping`.** Replace the table and any prose under it with:

```markdown
A response's status comes from its own turn. Later changes to the session, such as suspension or expiry, do not change the status of a response that was already returned.

| Turn outcome | Response status |
|:-------------|:----------------|
| The turn is running | `in_progress` |
| The turn's output was recorded | `completed` |

`GET /v1/responses/{id}` returns completed responses. An id that names no completed response that the caller can read returns `404`.
```

**(d) `### Multi-turn via previous_response_id`.** Replace the numbered list and the sentence after it with:

```markdown
When `previous_response_id` names the newest response of a conversation session that is still running, the gateway sends the new input to the same session and pod, and the runtime continues from its own memory.

In every other case the gateway starts a new session and seeds it with the stored conversation up to the named response. This covers a continuation of an earlier response (a fork), a second continuation sent while another continuation of the same response is in flight, and a continuation sent after the session was suspended, restored onto a new pod, or ended. The client sees the same result in both cases.

Response ids are opaque strings. Clients store and send them back unchanged and do not parse them.
```

**(e) `## Task lifecycle integration`, checkpointing bullet.** Replace "**Checkpointing:** Sessions are checkpointed per normal Lenny policy. Pod failures are retried transparently." with:

```markdown
- **Checkpointing:** Sessions are checkpointed per normal Lenny policy. A turn that fails after its input reached the runtime returns a server error, and the gateway ends that session; a continuation of an earlier completed response then starts a new session.
```

Leave `docs/api/openai-completions.md` unchanged.

## Testing

Every test carries a `// spec:` annotation, and every tier-2-and-up test carries a `// diagnosis:` comment. CODE-1 and CODE-3 tests are listed under TEST-1 so each case has one home.

### TEST-1 · tests across the reached tiers

Targets: `pkg/gateway/environment/translator/open_responses_test.go`, `continuity_test.go`, and a new `responseid_test.go`; `pkg/gateway/environment/transcriptstore` tests; `tests/tier2_component/stores/transcriptstore_test.go`; `pkg/gateway/session/sessionstore` memstore and pgstore tests; `pkg/gateway/sessionserver/holder_forward_test.go` and `holder_forward_sites_test.go`; `cmd/lenny-gateway` inner-mux and forwarder-adapter tests; `tests/tier3_contract/rest_openai_responses`; `tests/tier4_integration`; `tests/tier7a_load_local`; `tests/tier9_security`; `tests/tier11_docs` where page checks live. The existing single-shot assertions for Open Responses in these files are rewritten; `openai_chat_test.go` stays as the Chat Completions regression.

**Tier 1 (unit).**

- `transcriptstore` Memory: successive appends return contiguous last-seqs; an empty append returns 0. `// spec: §15.1, §15 (Open Responses conversation sessions)`
- `responseid`: table over `S.1`, `S.42`, no separator, empty prefix, `S.0`, `S.-1`, `S.x`, `S.1.2` (prefix `S.1`, ordinal 2), and a non-UUID prefix (parses; `Get` decides). `// spec: §15 (Open Responses conversation sessions)` paragraph B.
- sessionstore round-trip of `Responses` with nil, one, and many entries, and memstore copy isolation (mutating a returned slice does not change the stored row).
- `handleGet`: table over completed, not completed, out of range, unknown, cross-tenant, and malformed ids; ordinal 1 echoes `ContinuationParentID`, ordinal `n>1` echoes `S.(n-1)`. Paragraph B.
- Resolution precedes claim: an unknown, cross-tenant, malformed, or not-completed `previous_response_id` returns the native `404` with zero `BindSingleShot` calls and zero forwarder calls. Paragraph C.
- Warm conditions: table that breaks each of conditions 1 through 4 in turn (state `suspended`, `resume_pending`, `awaiting_client_action`, `completed`; `n < len`; head not completed; generation advanced; different user, runtime, environment) and asserts each goes cold with a new session whose `ContinuationParentID` is the request's id. The discriminating assertion is the bind count: 0 for warm, 1 for cold. Paragraph C rules 1-4.
- Reservation re-check: a mutator run against a row changed between read and Update (head advanced) writes nothing and the request goes cold.
- Executor refusal before delivery: `ErrNotBound` and `SealedError{Delivered: 0}` go cold in the same request; the reservation stays incomplete; a later continuation of the old head goes cold.
- Post-delivery failure: table over `Send` error after partial delivery, `record` error, commit error, and a generation advanced between `Send` and commit. Each answers `500`, leaves the response not completed, writes the session `failed`, and releases the pod exactly once (count release calls). Paragraph D.
- Release: a cold turn on a binder that returns `hold = false` writes `completed` and releases; the next continuation goes cold. Paragraph A.
- Hold: a successful cold turn on a binder that returns `hold = true` leaves the row `running`, does not release, and returns id `S.1`; a warm continuation returns `S.2`.
- Rehydrate: prefix cut at `EndSeq` with REST entries interleaved between turns (entries after `S.1`'s end and before `S.2`'s end appear in the rehydrate of `S.2` and not of `S.1`); visited guard; later-hop gap stop.
- Forwarding §29.3 rule 1: when the forwarder reports `ForwardDelivered`, `ForwardNotDelivered`, or `ForwardUnknown`, no `sessionstore.Update` is called. Not-delivered and unknown write `409` `TARGET_NOT_READY` with `current_state` in the native envelope.
- Forwarding §29.3 rule 4: a request carrying `sessionserver.WithForwarded(ctx)` on the coordinator calls `ForwardResponsesTurn`, gets `ForwardLocal`, and proceeds to serveWarm without a second dial.
- `sessionserver`: `ForwardResponsesTurn` on a forwarded request at a non-holder returns `ForwardNotDelivered`, writes nothing, and sets `DoNotStore`; at the holder it returns `ForwardLocal` without dialing; an unknown outcome writes nothing and sets `DoNotStore`. `// spec: §29.3 (Interactive message send) Forwarding rules 4-7, §11.5`
- `cmd/lenny-gateway`: the inner mux routes `POST /v1/responses` to the responses handler and does not route `GET /v1/responses/{id}`.
- `sessionSingleShotBinder` hold: table over continuity `none`, a `one_shot` runtime, a `multi_turn` runtime whose tenant override sets injection false, a resolve error, and a not-found, each returning `hold = false`, and continuity `platform` with a `multi_turn` injection runtime returning `true`. Paragraph A.

**Tier 2 (component).** `tests/tier2_component/stores/transcriptstore_test.go`: the pgstore return equals the stored `MAX(seq)`; a failed transaction returns `(0, err)`. pgstore sessionstore `Update` round-trip of `Responses`.

**Tier 3 (contract).** `tests/tier3_contract/rest_openai_responses`: response id format `<uuid>.<n>`; GET body fields and `previous_response_id` echo; native `404` and `409` envelopes, including `error.code` `TARGET_NOT_READY` and `error.current_state`; `500` envelope on post-delivery failure.

**Tier 4 (integration).**

- Release on a `one_shot` runtime: a conversation on the echo reference runtime (`one_shot`) releases its pod after each turn, and each continuation binds a new session. The held-session cases here run a `multi_turn` runtime with injection, such as the `chat` catalog runtime.
- Held session: three turns on one pod; pod name and lease unchanged; one bind.
- Idle restamp: set a short `maxClientIdleSeconds` and send K warm continuations at intervals shorter than the window, over a span longer than the window. Assert the session stays `running`, the pod and lease are unchanged, each continuation gets `S.(n+1)` with no new bind, and `last_agent_activity_at` advanced. Run it where the real adapter seal path runs (tier 4 if its harness runs the adapter, otherwise tier 5). This is the test that confirms the no-stamp decision; an idle-then-cold test does not.
- Idle suspension: after a quiet window, a continuation of the head goes cold into a new session with `ContinuationParentID` set, and the suspended session holds no pod.
- Two-replica forward: a warm continuation landing on the non-holder is served on the holder's pod; with the holder's forward listener stopped, the call returns `409` `TARGET_NOT_READY` and no DLQ entry exists.

**Tier 7a (load, local).**

- Concurrent `transcriptstore` appends to one session return disjoint, strictly ordered last-seqs, and each equals the highest Seq among that call's own entries.
- Randomized invariant checker: random interleavings of warm continuations, forks, concurrent continuations of the same head, REST message deliveries, injected executor refusals, injected post-delivery failures, injected ancestor erasure, and injected head-row erasure. Invariants: at most one completed response per `(S, n)`; for every completed response whose lineage is intact, rehydrate equals the runtime input history the turn saw; a lineage with an erased ancestor gives the truncated history; an erased head gives `404`; no pod is released twice; every failed turn left its session `failed`.

**Tier 9 (security).**

- Cross-tenant `previous_response_id` and GET return the same `404` as an unknown id, with no bind and no forward.
- Erasure: erasing an ancestor session gives a `200` whose rehydrated prefix stops at the gap; erasing the head session row gives a native `404` with no bind and no forward; a transcript erased under a surviving row gives an empty prefix for that hop and the walk continues to the parent.
- A warm continuation sent with a different `user` goes cold (condition 4).

**Tier 11 (documentation).** The doc checks run over `docs/api/open-responses.md` after DOCS-1.

## Edge cases and accepted failure modes

- The client-asserted `user` field is the session's `UserID` on both paths. Warm condition 4 keeps billing attribution consistent with the cold path and is not an authorization boundary. The summary lists the defect.
- A held session that the client abandons holds its pod until `maxClientIdleSeconds` idle-suspends it. This is the ordinary §6.2 cost of any idle session.
- A reservation orphaned by a crash or an executor refusal stops warm turns on that session permanently; continuations go cold.

## Files touched on application (non-spec)

- pkg/gateway/environment/transcriptstore/transcriptstore.go
- pkg/gateway/environment/transcriptstore/pgstore/pgstore.go
- pkg/gateway/sessionserver/messages_delivery.go
- pkg/gateway/sessionserver/holder_forward.go
- pkg/gateway/session/sessionstore/sessionstore.go
- pkg/gateway/session/sessionstore/pgstore/pgstore.go
- pkg/gateway/session/sessionstore/memstore/memstore.go
- pkg/gateway/environment/translator/responseid.go (new)
- pkg/gateway/environment/translator/continuity.go
- pkg/gateway/environment/translator/open_responses.go
- pkg/gateway/environment/translator/openai_chat.go
- pkg/gateway/environment/translator/singleshot.go
- cmd/lenny-gateway/credentialsurface.go
- cmd/lenny-gateway/holder_forward_listener.go and the call site passing the responses handler
- tests/testinfra/lifecyclefence/stores.go and world.go
- the test files CODE-1 and TEST-1 name
- docs/api/open-responses.md
