# Proposal: Build server-side Open Responses previous_response_id chaining so the §15 OpenResponsesAdapter honors OpenAI Responses API conversation state (lineage persistence plus fresh-pod transcript rehydration)

- **Status:** Verified (2026-07-23). Converged after 3 adversarial review rounds (3 findings fixed); awaiting sign-off.
- **Date:** 2026-07-23.
- **Scope:** A code-and-test build that makes an Open Responses multi-turn conversation continue correctly across the single-shot pods proposal 0055 introduced, plus one small §15 spec amendment that documents the server-side `previous_response_id` chaining the spec's superset compatibility claim already implies. Proposal 0055 built the §15 single-shot pod-binding model but deferred Open Responses conversation continuity, leaving a silent wrong-answer bug: an OpenAI Responses SDK client running a multi-turn conversation with `previous_response_id` gets the model answering the new turn with zero prior context and no error surfaced. The build persists the `previous_response_id` lineage on a new dedicated session-row field (never `ParentSessionID`, which would trip the delegation-tree machinery), gives the Open Responses handler a transcript store so each turn is recorded, and rehydrates the capped parent conversation onto the freshly-claimed pod by prepending the prior turns ahead of the new input in the same `exec.Send` call. An unknown or cross-tenant `previous_response_id` is rejected fail-closed. The single §15 amendment extends the existing single-shot compute-model continuation clause and is the only spec edit.

This document stages the proposed spec, code, and test changes. It does not modify any spec, code, or doc file. Apply the changes in the "Proposed changes" section after sign-off.

## 1. Problem

§15 declares `OpenResponsesAdapter` covers OpenAI Responses API clients, and states that OpenAI's Responses API is a proper superset of Open Responses whose only unsupported difference is OpenAI's proprietary hosted tools (`spec/15_external-api-surface.md:581`). The adapter is declared Always-available (`spec/15_external-api-surface.md:567`) and V1 in the inventory table (`spec/15_external-api-surface.md:573-577`). `previous_response_id` is a core top-level field of the OpenAI Responses API and is that API's server-side conversation-state mechanism. It is not a hosted tool, so server-side chaining falls inside the superset the compatibility claim promises. The published docs already describe the intended server-side path (`docs/api/open-responses.md:235-242`: retrieve the previous response's session transcript, prepend the full conversation history to the new input, create a new session with the combined context) and list Multi-turn via `previous_response_id` as Supported (`docs/api/open-responses.md:252`). This is the product mandate: line 581 and the docs are currently false, and the resolution is to make them true.

Proposal 0055 resolved its Open decision 1 (Open Responses continuity) as option (b): `SupportsSessionContinuity` stays `true`, with lineage persistence and conversation rehydration filed as a bounded follow-on (`proposals/0055_...md:3,263`). This proposal is that follow-on. The deferral left `SupportsSessionContinuity: true` declared but substantively empty, so a continuation runs against a fresh pod with no prior context.

The defect has three confirmed facets in `pkg/gateway/environment/translator/open_responses.go`.

**1. `handleCreate` dispatches only the current turn.** `handleCreate` (`open_responses.go:231-317`) claims a fresh pod via `h.binder.BindSingleShot` (`:269-274`) and dispatches only the current turn via `h.exec.Send(r.Context(), sessionID, msgs)` with `normalizeInput(req.Input)` (`:289-295`). No prior conversation is retrieved or prepended. Every pod starts with empty runtime memory, so the model answers the new turn with nothing from the parent conversation.

**2. The lineage is echoed back but never persisted.** `handleCreate` echoes `req.PreviousResponseID` straight onto the create response (`:313`) and the stream response (`:310`, then `buildOpenResponsesResponse` at `:458-465`) without persisting it. The only store write is `store.Update` setting `State=Completed` (`:303-306`).

**3. `GET /v1/responses/{id}` reads the wrong field.** `handleGet` reads `PreviousResponseID` from `row.ParentSessionID` (`:397`), which the single-shot bind path never populates (`SingleShotSpec` carries no lineage field, `singleshot.go:49-54`), so GET always returns an empty `previous_response_id`.

The existing test `TestOpenResponsesSingleShotDropsPreviousResponseIDLineage` (`open_responses_test.go:202-234`) pins exactly this deferred-and-now-defective behavior, asserting the row stores an empty `ParentSessionID` and GET echoes empty.

The build has two parts, and both have a confirmed structural obstacle.

**Lineage persistence must not reuse `Session.ParentSessionID`** (`sessionstore.go:143-145`). A single-shot pod, and thus the prior response's session, is released synchronously within the prior HTTP call (`spec/15_external-api-surface.md:603-605`), so the referenced prior response is already terminal when a continuation starts. `orphancleanup` terminates any non-terminal row whose delegation-tree root is terminal past the cascade window (`orphancleanup.go:195-209`, `treeRoot` walking `ParentSessionID` at `:248-259`), so a continuation that set `ParentSessionID` to the terminal prior response would be swept mid-flight. `ParentSessionID` is also branched on by `registerLeaseTree` lease-tree-root suppression (`sessionserver.go:828`), tree archival and `child_failed` routing (`usage.go:616,727,751`), inter-session messaging scope (`messaging.go:142-151`), and the §8.2/§8.6 delegation-tree walks. A dedicated `Session` field that no delegation-tree code branches on is required, with a migration and a pgstore round-trip.

**Conversation rehydration must replay the parent turns onto the freshly-claimed pod.** Every pod starts with empty runtime memory and no existing code re-injects prior turns into a live pod: `replay.go:185-191` copies transcript rows store-to-store for readback only, and `executor.Executor` exposes only `Send(ctx, sessionID, messages)` with no restore-without-dispatch primitive. The Open Responses handler writes no transcripts today (`OpenResponsesHandler` has no `transcripts` field, `open_responses.go:162-169`), unlike the canonical messages path (`messages.go:575-591`), so the durable source rehydration needs does not yet exist for this handler.

## 2. Decisions

- **Build server-side chaining; do not narrow the spec.** Per the product mandate, the resolution is to make line 581 and the docs true. Narrowing line 581 to admit a second exception, or marking chaining unsupported, is rejected. `kind` is `fix`: the capability is already promised by the superset claim plus the Always-available V1 declaration, and correcting non-functional code to honor that promise is a reconciliation.
- **A dedicated `Session` lineage field that no delegation-tree code branches on.** A confirmed set of delegation-tree machinery branches on `ParentSessionID` (the `orphancleanup` sweep at `orphancleanup.go:195-209,248-259`; `registerLeaseTree` suppression at `sessionserver.go:828`; tree archival and `child_failed` routing at `usage.go:616,727,751`; messaging scope at `messaging.go:142-151`), and a continuation's referenced prior response is always already terminal (`spec/15:603-605`), so reusing `ParentSessionID` would get the continuation swept and mis-routed. The new field (`Session.OpenResponsesParentID`) is written and read only by the Open Responses path; no delegation-tree code branches on it. This mirrors the established pattern of purpose-specific lineage columns already on the row (`RootSessionID`, `CredentialOriginSessionID`, `sessionstore.go:147-173`).
- **The response id is the session id, so `previous_response_id` resolves directly to the prior session row.** `buildOpenResponsesResponse` and `handleGet` already key the response id to the session id (`open_responses.go:313,392,397`), so a continuation's `previous_response_id` is the prior turn's session id. The lineage field stores it, and the transcript keyed on it is the parent's history.
- **The lineage is persisted on the post-dispatch `store.Update` rather than threaded through the create surface.** `handleCreate` already calls `h.store.Update` on the freshly-bound child session after a successful dispatch (`open_responses.go:303-306`) to flip `State=Completed`, and the store's `Update` takes an arbitrary mutate closure. Setting `s.OpenResponsesParentID = req.PreviousResponseID` inside that existing closure persists the lineage with no new request-threading plumbing. The child's lineage field is write-for-GET-only: nothing in the request flow reads it. Rehydration resolves the parent directly from the incoming `previous_response_id`, never from the child's field. Persisting on the post-dispatch `Update` also means a turn that fails before completion records no lineage anchor to a turn with no assistant output, and it accepts the identical crash window the existing `State=Completed` write already accepts. This descopes the create-surface threading the initial draft carried (see "Resolved in adversarial review").
- **Rehydration prepends the capped parent transcript as leading `executor.Message`s in the same `exec.Send` call.** The executor interface exposes only `Send(ctx, sessionID, messages)` with no restore-without-dispatch primitive, and `replay.go` never re-feeds a live pod, so the only way to continue on a fresh pod is to prepend the prior turns ahead of the new input in the single `Send`. This matches the documented server-side path (`docs/api/open-responses.md:235-242`).
- **The Open Responses handler gets its own transcript store and copies the parent transcript forward per turn.** The canonical messages path records an `Entry` per inbound message plus each assistant text part (`messages.go:575-591`), and `replay.go`'s `prompt_history` mode copies a source transcript into a new session bucket (`replay.go:185-191`). The handler records each turn (inbound input plus assistant output) on the response's session id, and on a continuation copies the parent bucket forward into the child bucket, so every session's bucket is self-contained and rehydration reads one `Get` rather than walking the whole chain.
- **Fail closed on the `previous_response_id` reference.** The lineage resolve is a tenant-scoped `store.Get` / `transcripts.Get` (both stores return `ErrNotFound` cross-tenant, `transcriptstore.go:66-67`, and the session store is tenant-scoped), so an unknown or cross-tenant `previous_response_id` returns a 404-class error rather than silently continuing with empty context. This satisfies the §13 fail-closed rule for a cross-tenant reference.
- **An operator-tunable cap bounds the replayed history.** Prepending unbounded prior turns makes per-turn cost and latency grow without limit, so the replayed history is capped (most-recent-N turns) with an operator-tunable knob, defaulted in the adapter options, per the config rule that a non-spec default must be overridable.
- **OpenAI Chat Completions is out of scope.** It is stateless (`SupportsSessionContinuity` false, `openai_chat.go`), the client resends the full history each call, so it needs neither lineage persistence nor rehydration. This proposal touches only the Open Responses adapter.
- **Amend §15 to document the chaining mechanism the compatibility claim implies.** The current single-shot compute-model continuation clause says continuation re-claims a fresh pod per request (`spec/15:603-605`) but is silent on lineage persistence and transcript rehydration. A small amendment states that the prior response's transcript is retrieved and prepended onto the fresh pod, that the lineage is persisted so GET echoes the real value, that an unknown or cross-tenant reference fails closed, and that the replayed history is capped, so the behavior traces to a spec section per `spec-driven-development.md`.

## 3. The continuation request path after the change

`handleCreate` runs the same single-shot bind-dispatch-release sequence proposal 0055 built. This proposal changes the message assembly before `exec.Send`, the post-dispatch `store.Update`, and adds a fail-closed resolve when `req.PreviousResponseID != ""`.

1. Decode and validate the request body and resolve the tenant, exactly as today.
2. When `req.PreviousResponseID != ""`, resolve it with a tenant-scoped `h.store.Get(tenantID, prevID)`. On `ErrNotFound` (an unknown id, or a cross-tenant id the tenant-scoped store hides), return `404 not_found_error` and dispatch nothing. When `req.PreviousResponseID == ""`, this step is skipped and behavior is unchanged from the single-shot happy path.
3. Claim a fresh pod through `h.binder.BindSingleShot`, exactly as today.
4. Read the parent transcript with `h.transcripts.Get(tenantID, prevID)`, map the entries to `[]executor.Message`, apply the operator-tunable most-recent-N cap, and prepend the capped prior turns ahead of `normalizeInput(req.Input)` in the slice passed to `h.exec.Send`. On an empty or missing parent transcript the prepend is empty and the new turn dispatches alone.
5. On a successful dispatch, in the existing completion `store.Update` closure, set `s.State = StateCompleted` and `s.OpenResponsesParentID = req.PreviousResponseID`, so the lineage persists for GET.
6. Record the turn: append the inbound normalized messages and the assistant text output parts on the response's session id. On a continuation, first copy the resolved parent bucket forward into this session's bucket, then append the new turn, so the child bucket is self-contained. The transcript write is best-effort and does not fail the response.
7. Write the create or stream response, echoing the real `previous_response_id`, and release the pod on the deferred exit as today.
8. `GET /v1/responses/{id}` reads `PreviousResponseID` from `row.OpenResponsesParentID`, so it echoes the persisted lineage rather than the always-empty `ParentSessionID`.

Against the in-memory `EchoExecutor` wiring and the in-process unit tests (no transcript store injected), the handler skips the transcript work best-effort, matching the messages path, and a continuation dispatches the new turn alone.

## 4. Edge cases and accepted failure modes

Each row names the observable outcome and the spec or docs text that states it. The SPEC-1 continuation clause is the spec basis for the rehydration, fail-closed, and cap rows; `docs/api/open-responses.md` is the reader-facing statement.

| Scenario | Observable outcome | Spec or docs text |
|:--|:--|:--|
| Continuation with a valid same-tenant `previous_response_id` | The capped parent transcript is prepended ahead of the new input in the one `exec.Send`, so the runtime continues the conversation despite the fresh pod's empty runtime memory; GET echoes the real lineage | SPEC-1 continuation clause; "The gateway retrieves the previous response's session transcript ... prepended to the new input" (`docs/api/open-responses.md:237-242`) |
| Unknown `previous_response_id` (no such session for the tenant) | `404 not_found_error`; no pod dispatched, no context leaked | SPEC-1 fail-closed clause; §13 fail-closed on a missing reference |
| Cross-tenant `previous_response_id` (tenant B references a tenant A response) | The tenant-scoped `store.Get` returns `ErrNotFound`, so `404 not_found_error`; tenant A's transcript never reaches tenant B | SPEC-1 fail-closed clause; tenant-scoped store isolation (`transcriptstore.go:9-11,66-67`); §13 security model |
| Parent transcript longer than the cap | Only the most-recent-N turns are prepended; per-turn cost and latency stay bounded | SPEC-1 capped-replay clause; the cap softens "The full conversation history is prepended" (`docs/api/open-responses.md:239`) once truncation applies |
| Parent session exists but its transcript is empty or missing | The prepend is empty; the new turn dispatches alone; no error | SPEC-1 continuation clause (rehydration is best-effort over the recorded transcript) |
| Turn fails before the completion `store.Update` (dispatch error or timeout) | `State` and `OpenResponsesParentID` are not written, so no lineage anchor points at a turn with no assistant output; the pod is released with the failed §6.2 disposition | SPEC-1 lineage-persistence clause (persisted on completion); §6.2 pre-attached disposition reclaim |
| Empty `previous_response_id` (a first turn) | Behavior is unchanged from the single-shot happy path: one turn dispatched, no resolve, no prepend, empty lineage persisted | `proposals/0055` single-shot compute model; SPEC-1 |
| Transcript write failure on record or copy-forward | The response still returns; the write is best-effort, matching the messages path | Canonical messages best-effort transcript write (`messages.go:572-591`) |
| OpenAI Chat Completions request | Unaffected: stateless, the client resends the full history, no lineage or rehydration | `SupportsSessionContinuity: false` (`openai_chat.go`); this proposal touches only Open Responses |

## 5. Proposed changes

### SPEC-1. Document server-side previous_response_id chaining in the §15 single-shot continuation clause

**Target:** `spec/15_external-api-surface.md`, the "Built-in adapter single-shot compute model" paragraph, the continuation sentence at `:603-605`.

**Rationale:** `spec/15:581` already claims `OpenResponsesAdapter` covers OpenAI Responses API clients as a proper superset whose only exception is proprietary hosted tools, and `:567,573-577` declare it Always-available and V1, so server-side `previous_response_id` chaining is required. The current continuation clause ("`OpenResponsesAdapter` continuation (`previous_response_id`) re-claims a fresh pod per request and does not retain a pod bind across calls") is silent on how continuity is preserved across the fresh pod, so the behavior this proposal builds has no spec section to trace to. Extending the clause makes the compatibility claim and the docs true and gives the code a citable section, without adding a second exception to `:581`. This is the only spec edit.

**Change (staged spec text).** Replace the continuation sentence at `spec/15:603-605`:

```markdown
`OpenResponsesAdapter` continuation
(`previous_response_id`) re-claims a fresh pod per request and does not retain a
pod bind across calls.
```

with:

```markdown
`OpenResponsesAdapter` continuation (`previous_response_id`) re-claims a fresh
pod per request and does not retain a pod bind across calls. On a continuation
the gateway resolves the `previous_response_id` to its prior session, retrieves
that session's transcript, and prepends the prior conversation turns onto the
freshly-claimed pod before dispatching the new input, so the runtime continues
the conversation even though the pod starts with empty runtime memory. The
`previous_response_id` lineage is persisted on the new session so
`GET /v1/responses/{id}` echoes it. An unknown or cross-tenant
`previous_response_id` is rejected (fail closed per
[Section 13](13_security-model.md)) rather than continuing with empty context.
The replayed history is bounded by an operator-tunable cap.
```

### CODE-1. Add a dedicated Open Responses lineage field to the session row, with migration and pgstore round-trip

**Target:** `pkg/gateway/session/sessionstore/sessionstore.go` (new `Session` field, in the lineage-field cluster near `:143-173`); `migrations/0180_sessions_open_responses_parent.up.sql` and `.down.sql` (new nullable column); `pkg/gateway/session/sessionstore/pgstore/pgstore.go` (SELECT column lists at `:105,:174`; INSERT at `:254`; UPDATE at `:419,:505`; Scan at `:1032`); `pkg/gateway/session/sessionstore/memstore` (verify the struct-copy store needs no change).

**Rationale:** Lineage must not reuse `ParentSessionID`: a continuation's referenced prior response is already terminal (`spec/15:603-605`), so `orphancleanup` would sweep it (`orphancleanup.go:195-209,248-259`) and `registerLeaseTree`/`usage`/`messaging` would mis-branch on it (`sessionserver.go:828`; `usage.go:616,727,751`; `messaging.go:142-151`). No delegation-tree code may branch on the new field. No existing `Session` field carries response lineage without a delegation or credential concern (`ParentSessionID`, `RootSessionID`, `CredentialOriginSessionID`, `ConversationContinuity` all serve other concerns, `sessionstore.go:120-173`), and `Session.Metadata` is client-supplied and spoofable, so a new column is required. Migration 0180 is the next free number (the latest on disk is 0179).

**Change (staged description).**

1. Add `OpenResponsesParentID string` to `sessionstore.Session`, adjacent to the lineage-field cluster, with a doc comment stating it is the Open Responses `previous_response_id` lineage pointer, written and read only by the `/v1/responses` path, and explicitly NOT `ParentSessionID` because no delegation-tree code branches on it. Cite why (`// spec: §15 continuation; §8.2 delegated-child semantics avoided`).
2. Add migration `0180_sessions_open_responses_parent.up.sql`:

   ```sql
   ALTER TABLE sessions ADD COLUMN IF NOT EXISTS open_responses_parent_id UUID NULL;
   ```

   and `.down.sql` dropping the column, following the credential-origin migration pattern.
3. Thread the column through pgstore at the five touchpoints, using the same `COALESCE(...::text,'')` read and `NULLIF($n,'')::uuid` write convention `parent_session_id` uses (`pgstore.go:105,174,254,419,505,1032`). The UPDATE touchpoints (`:419,:505`) carry the write CODE-2 relies on; the INSERT touchpoint (`:254`) is for the column's existence and round-trip.
4. Confirm `memstore` stores the full `Session` struct so no change is needed.

### CODE-2. Persist the lineage on the post-dispatch Update and read it on GET

**Target:** `pkg/gateway/environment/translator/open_responses.go` (the completion `store.Update` closure in `handleCreate` at `:303-306`; `handleGet` at `:397`).

**Rationale:** The child session must persist `previous_response_id` so GET echoes the real value rather than the always-empty `ParentSessionID`. The child's lineage field is write-for-GET-only: nothing in the request flow reads it (CODE-4 resolves the parent directly from the incoming `previous_response_id`, never from the child's field), so it does not need to exist before the pod bind or the dispatch. `handleCreate` already runs `h.store.Update` on the child after a successful dispatch (`:303-306`), and the store's `Update` takes an arbitrary mutate closure, so setting the field inside that closure persists the lineage with no request-threading plumbing. This descopes the create-surface threading the initial draft carried (see "Resolved in adversarial review").

**Change (staged description).**

1. In the existing completion closure (`:303-306`), set the lineage alongside the state flip:

   ```go
   _, _ = h.store.Update(r.Context(), tenantID, sessionID, func(s *sessionstore.Session) error {
       s.State = session.StateCompleted
       s.OpenResponsesParentID = req.PreviousResponseID
       return nil
   })
   ```

   This runs for both the streaming and non-streaming paths, because the `Update` precedes the `if req.Stream` branch.
2. In `handleGet`, read the lineage from the new field:

   ```go
   PreviousResponseID: row.OpenResponsesParentID,
   ```

   replacing `row.ParentSessionID` at `:397`, so GET echoes the persisted value.
3. Carry a `// spec: §15 continuation (previous_response_id lineage persistence)` tie on the closure assignment.

`singleshot.go`, `sessionserver.go`, `start.go`, and `credentialsurface.go` are untouched by lineage persistence: no create-surface threading is added.

### CODE-3. Give the Open Responses handler a transcript store and record each turn on the response session id

**Target:** `pkg/gateway/environment/translator/open_responses.go` (`OpenResponsesHandler` struct at `:162-169`; `OpenResponsesOptions` at `:172-188`; `NewOpenResponsesHandler` at `:191-220`; the turn-record after the completion `store.Update` in `handleCreate`, near `:303-307`); `cmd/lenny-gateway/credentialsurface.go` (adapter construction at `:66`, inject `w.transcripts`).

**Rationale:** The handler holds no transcripts reference today (`open_responses.go:162-169`) and writes no transcript per turn, so the durable source rehydration needs does not exist for this handler, unlike the canonical messages path (`messages.go:575-591`). The gateway already constructs a `transcriptstore.Store` and injects it into `sessionserver` (`Options.Transcripts`, `sessionserver.go:1065,1751`; wired at `sessionsrv.go:183`), and it is reachable as `w.transcripts` in the credential-surface wiring, so the same store is injected here.

**Change (staged description).**

1. Add a `transcripts transcriptstore.Store` field to `OpenResponsesHandler` and a `Transcripts transcriptstore.Store` option to `OpenResponsesOptions`, defaulting nil in `NewOpenResponsesHandler`. When nil (in-process tests and in-memory mode), the handler skips transcript work best-effort, matching the messages path.
2. After the successful `exec.Send` and completion `store.Update` in `handleCreate` (`:303-307`), when `h.transcripts != nil`:
   - On a continuation (`req.PreviousResponseID != ""`), first copy the resolved parent bucket forward into this session's bucket: `entries, _ := h.transcripts.Get(ctx, tenantID, prevID)`, then `h.transcripts.Append(ctx, tenantID, sessionID, entries...)`, mirroring the `replay.go:187-190` `prompt_history` copy-forward.
   - Then build `transcriptstore.Entry` rows for the new turn (one per inbound normalized message plus one per assistant text output part, mirroring `messages.go:576-590`) and `Append` them on the response session id.
   - Best-effort: a transcript write failure does not fail the response (`messages.go:573-574`).
3. In `cmd/lenny-gateway/credentialsurface.go`, pass `w.transcripts` into `NewOpenResponsesHandler` via the new `Transcripts` option (`:66`).
4. Carry a `// spec: §15.1 transcript; §15 continuation (rehydration source)` tie.

### CODE-4. Rehydrate the parent conversation onto the fresh pod in handleCreate

**Target:** `pkg/gateway/environment/translator/open_responses.go` (`handleCreate` at `:231-317`, specifically the resolve and ownership check before the bind at `:269` and the message assembly before `exec.Send` at `:289-295`; `OpenResponsesOptions`: add an operator-tunable replay cap).

**Rationale:** Every pod starts with empty runtime memory and no code re-injects prior turns into a live pod (`replay.go` copies for readback only; `executor.Send` has no restore primitive), so a continuation only continues if the capped prior turns are prepended as leading `executor.Message`s in the same `Send` call (`docs/api/open-responses.md:235-242`). The reference must be validated same-tenant and fail closed.

**Change (staged description).**

1. Add an operator-tunable `MaxReplayTurns int` field to `OpenResponsesOptions` (and a matching handler field), defaulted in `NewOpenResponsesHandler` to a documented constant, per the config-overridability rule.
2. In `handleCreate`, when `req.PreviousResponseID != ""`, before the bind (`:269`): resolve it via a tenant-scoped `h.store.Get(r.Context(), tenantID, req.PreviousResponseID)`. On `ErrNotFound` (unknown or cross-tenant), `writeOpenAIError(w, http.StatusNotFound, "not_found_error", "previous_response_id not found")` and return, dispatching nothing.
3. After the bind and after `normalizeInput(req.Input)`, when `req.PreviousResponseID != ""`: read the parent transcript via `h.transcripts.Get(r.Context(), tenantID, req.PreviousResponseID)`, map the entries to `[]executor.Message` (role plus content), apply the `MaxReplayTurns` cap keeping the most-recent turns, and prepend the mapped messages ahead of the normalized new input in the slice passed to `h.exec.Send` (`:295`). On an empty or missing transcript the prepend is empty.
4. Record the turn per CODE-3 (copy-forward then append). When `previous_response_id` is empty, behavior is unchanged from the single-shot happy path.
5. Remove the now-obsolete comment block at `:261-267` that documents the deferred no-op, and replace it with the retrieve-and-prepend rationale.
6. Carry `// spec: §15 continuation (retrieve-and-prepend, capped, fail-closed cross-tenant); §13 fail-closed` ties.

### DOC-1. Reconcile the Open Responses multi-turn docs with the capped, fail-closed behavior

**Target:** `docs/api/open-responses.md` (`:235-242` multi-turn section; `:239` wording).

**Rationale:** `docs/api/open-responses.md:237-242` already describes the server-side retrieve-and-prepend path and `:252` already lists Multi-turn via `previous_response_id` as Supported, so the build makes the existing feature-table row true and `:252` needs no edit. CODE-4 adds two reader-visible behaviors the page does not cover: the operator-tunable replay cap (which also makes the existing wording "The full conversation history is prepended" at `:239` inaccurate once truncation applies) and the unknown or cross-tenant 404 reject. `doc-content.md` requires documenting error paths and covering the whole subject, and the page already documents the analogous proprietary-tool 400 at `:265`, so adding these two statements is warranted and minimal. No spec section numbers appear in the reader-facing prose, per `doc-content.md`.

**Change (staged description).**

1. Soften `:239` so it states the capped replay rather than an unbounded "full":

   ```markdown
   2. The prior conversation history is prepended to the new input, bounded by
      an operator-configurable cap on the number of replayed turns.
   ```
2. Add a sentence after the numbered list (before `:244`) stating the fail-closed behavior:

   ```markdown
   An unknown `previous_response_id`, or one that belongs to a different tenant,
   is rejected with `404 not_found_error`; the request is not continued with
   empty context.
   ```

No `TEST-GAPS.md` edit is staged: no `TEST-GAPS.md` or `BUILD-GAPS.md` finding records the 0055 Open Responses `previous_response_id` continuity deferral (the recorded Open Responses gaps concern streaming events, the status-mapping table, and the error-envelope fields, and the only "continuity" finding is an unrelated §4.1 cross-replica one), so there is nothing to flip to RESOLVED.

## 6. Non-goals

- **OpenAI Chat Completions continuity.** It is stateless (`SupportsSessionContinuity` false); the client resends the full history, so it needs neither lineage persistence nor rehydration. This proposal touches only the Open Responses adapter.
- **Reusing `Session.ParentSessionID` for response lineage.** Rejected: it would trip the `orphancleanup` sweep, `registerLeaseTree` suppression, tree archival, `child_failed` routing, and messaging scope. A dedicated field is added instead.
- **Threading the lineage through the single-shot create surface.** Descoped in review: the lineage is persisted on the existing post-dispatch `store.Update` closure, so `SingleShotSpec`, `CreateSessionRequest`, `CreateAndStartRequest`, `buildCreateAndStartRow`, and the credential-surface binder are untouched (see "Resolved in adversarial review").
- **Retaining a warm pod bind across continuation calls.** The single-shot model (proposal 0055, `spec/15:603-605`) re-claims a fresh pod per request; this proposal rehydrates onto that fresh pod rather than holding a pod idle between calls.
- **Narrowing spec line 581 to admit a second exception, or marking `previous_response_id` chaining unsupported.** Rejected by the product mandate; the resolution is to build chaining so line 581 and the docs become true.
- **A new executor restore-without-dispatch primitive or any change to the `executor.Executor` interface.** Rehydration prepends prior turns as leading messages in the existing `exec.Send` call.
- **Changing the §15.1 replay (`prompt_history`) path or the canonical messages transcript-write path.** This proposal reuses those patterns for the Open Responses handler; it does not modify them.
- **Persisting raw model output for GET readback of prior response bodies.** GET continues to return the metadata envelope; the transcript store is the rehydration source rather than a response-body archive.
- **Generalizing the lineage column and copy-forward rehydration for future `SupportsSessionContinuity` adapters.** A2A and Agent Protocol are Post-V1; the minimal-surface principle keeps this Open-Responses-specific until a second consumer exists (see Open decisions).

## 7. Testing

The change reaches tier 0 (static), tier 1 (the rehydration prepend, the cap boundary, the fail-closed resolve, and the lineage persist plus GET echo, in-process), tier 2 (the new column round-trip and the cross-tenant reject through the store, against envtest or compose), tier 3 (the GET wire body now carries `previous_response_id`), tier 5 (a real multi-turn conversation continuing across fresh pods on Kind), and tier 9 (a cross-tenant `previous_response_id` reference is rejected) per `.claude/rules/test-coverage.md`. Each test below covers a non-happy path and carries a `// spec:` tie; every tier-2-and-up test carries a `// diagnosis:` comment.

- **tier-1 lineage persisted and echoed (boundary):** Replace `TestOpenResponsesSingleShotDropsPreviousResponseIDLineage` (`open_responses_test.go:202-234`), which pins the now-defective empty-echo behavior. Assert that a create carrying `previous_response_id` persists `Session.OpenResponsesParentID` (and leaves `ParentSessionID` empty, so no delegation-tree code trips) and that `GET /v1/responses/{id}` echoes the real value. The non-happy path is the continuation whose lineage was previously dropped. `// spec: §15 continuation (lineage persistence); §8.2 (delegated-child field avoided)`.
- **tier-1 rehydration prepends the capped parent transcript (boundary):** With a stub executor that captures the dispatched message slice and a seeded parent transcript, assert a continuation prepends the parent turns ahead of the new input, in order, and that the cap truncates to the configured most-recent-N. The non-happy paths are the over-cap transcript and the boundary at exactly N turns. `// spec: §15 continuation (retrieve-and-prepend, capped)`.
- **tier-1 unknown previous_response_id fails closed (spec-named-failure):** Assert a create carrying a `previous_response_id` with no matching session returns `404 not_found_error` and never calls `exec.Send`. The non-happy path is the dangling reference. `// spec: §15 continuation (fail closed); §13`.
- **tier-1 empty parent transcript (empty):** Assert a continuation whose parent session exists but has an empty transcript dispatches the new turn alone with no error. `// spec: §15 continuation (best-effort rehydration)`.
- **tier-1 copy-forward makes the child bucket self-contained (boundary):** With an injected transcript store, seed a parent bucket, drive one continuation, and assert the child session's bucket holds the copied-forward parent entries followed by the new turn's entries in that order (CODE-3 step 2). This pins the copy-forward write CODE-3 introduces, which no read-side or single-hop test exercises. The non-happy path is a copy-forward that appends to the wrong bucket, drops the parent entries, or mis-orders them. `// spec: §15.1 transcript; §15 continuation (copy-forward self-containment)`.
- **tier-1 three-turn chain stays self-contained (boundary):** With a stub executor that captures each dispatched message slice and an injected transcript store, drive three turns chained by `previous_response_id` (turn 2 references turn 1, turn 3 references turn 2) and assert turn 3's dispatched slice still carries turn 1's context, so the copy-forward accumulates correctly beyond a single hop. The non-happy path is a chain that loses the earliest turn at the third hop. `// spec: §15 continuation (multi-hop copy-forward)`.
- **tier-1 transcript write failure fails open (spec-named-failure):** Inject a transcript store stub whose `Append` (and `Get` on the copy-forward) returns an error, drive a continuation, and assert the create still returns `200` with a well-formed body, pinning the best-effort write contract CODE-3 step 3 states (`messages.go:573-574`). The non-happy path is a transcript error propagated into `writeOpenAIError` instead of swallowed. `// spec: §15.1 transcript (best-effort write)`.
- **tier-2 lineage column round-trip and cross-tenant reject (spec-named-failure):** Against a pgstore on envtest or compose, assert a `Create` then `Update` then `Get` round-trips `open_responses_parent_id`, and that a cross-tenant `Get` on the parent id returns `ErrNotFound`. The non-happy path is the cross-tenant read. `// spec: §15 continuation; §4.2 store isolation`. `// diagnosis:` a failure means the lineage column does not persist or the tenant scope leaks.
- **tier-3 GET wire body carries previous_response_id (contract):** Assert the `GET /v1/responses/{id}` JSON body carries the persisted `previous_response_id` after a continuation. `// spec: §15.1 GET /v1/responses`.
- **tier-3 update the existing fidelity passthrough test for the fail-closed resolve (contract):** The existing `TestRESTOpenAIResponsesIDFieldExtendedBehavior` passthrough assertion (`tests/tier3_contract/rest_openai_responses/fidelity_test.go:239-251`) sends `previous_response_id:"resp_parent_42"` against a fresh memstore with no session seeded for that id and asserts a `200` echo. CODE-4's fail-closed resolve makes an unseeded parent id return `404 not_found_error`, so this assertion would fail. Seed a `resp_parent_42` session in the store before the create (capture the store from `newResponsesServers`, which already returns it) so the resolve succeeds and the passthrough assertion (`200` plus `previous_response_id` echo) stays the fidelity check it was written to be. The fail-closed `404` path is pinned separately by the tier-1 unknown-id and tier-9 cross-tenant tests. `// spec: §15.2.1`.
- **tier-5 multi-turn conversation continues across fresh pods (spec-named-failure):** Drive two `/v1/responses` turns chained by `previous_response_id` against the deployed `PodExecutor` gateway on Kind and assert the second answer reflects the first turn's context, so the fresh-pod rehydration works end to end. The non-happy path is the second turn that would answer with empty context before this change. `// spec: §15 continuation`. `// diagnosis:` a failure means rehydration did not reach the fresh pod.
- **tier-9 cross-tenant previous_response_id isolation (spec-named-failure):** A tenant B `previous_response_id` referencing a tenant A response is rejected with `404` and no tenant A context appears in the response. The non-happy path is the cross-tenant continuation attempt. `// spec: §13 fail closed; §15 continuation`. `// diagnosis:` a failure means a tenant can read another tenant's transcript through a continuation.

## 8. Findings closed on application

No `TEST-GAPS.md` or `BUILD-GAPS.md` finding records the 0055 Open Responses `previous_response_id` continuity deferral, so no finding is flipped to RESOLVED on application. This proposal resolves proposal 0055's Open decision 1 option (a): it rehydrates the conversation onto the freshly-claimed pod and persists the lineage through a dedicated field that does not trigger the delegated-child lease suppression.

## 9. Resolved in adversarial review

The challenge-round revisions carried in the draft narrowed the proposal from its original form:

- **The create-surface lineage threading was descoped in favor of the existing post-dispatch `store.Update` closure.** The original CODE-2 threaded the lineage value through five surfaces (`SingleShotSpec`, `CreateSessionRequest` and its JSON round-trip, `CreateAndStartRequest`, `buildCreateAndStartRow`, and the credential-surface binder) on the assumption that the lineage must be set at row-creation time. That assumption is false: the child's `OpenResponsesParentID` is write-for-GET-only, and CODE-4's rehydration resolves the parent directly from the incoming `previous_response_id`, never from the child's field. `handleCreate` already calls `h.store.Update` on the child after a successful dispatch (`open_responses.go:303-306`), and the store's `Update` takes an arbitrary mutate closure, so setting `s.OpenResponsesParentID = req.PreviousResponseID` inside that closure persists the lineage with no new plumbing, for both the streaming and non-streaming paths. Because the JSON marshal-then-re-decode round-trip in `CreateAndStartService` forces any create-time value onto both request types, the create-time route was strictly the more expensive one. Persisting on the post-dispatch `Update` also means a turn that fails before completion records no lineage anchor to a turn with no assistant output. CODE-2 now collapses to the `store.Update`-closure write plus the `handleGet` read change; `singleshot.go`, `sessionserver.go`, `start.go`, and `credentialsurface.go` are untouched.
- **CODE-1's dedicated column is retained because SPEC-1 mandates GET echo.** The challenge noted that the silent wrong-answer defect is fixed entirely by CODE-3 plus CODE-4, and that the column serves only the secondary GET-echo fidelity. SPEC-1 newly mandates that `GET /v1/responses/{id}` echoes the real `previous_response_id`, so the GET-echo is a required behavior rather than an optional add, and the dedicated column (never `ParentSessionID`) is the correct mechanism for it. The column is kept, with the create-surface plumbing that rode on it removed per the CODE-2 revision.
- **The DOC-1 `TEST-GAPS.md` target was struck.** The original DOC-1 proposed flipping a finding that recorded the 0055 continuity deferral to RESOLVED, guarded by "if a finding references...". No such finding exists in `TEST-GAPS.md` or `BUILD-GAPS.md`: the recorded Open Responses gaps concern streaming events, the status-mapping table, and the error-envelope fields, and the only "continuity" finding is an unrelated §4.1 cross-replica one. The clause was a confirmed no-op and is removed so the change does not imply nonexistent work. DOC-1 also now softens the "The full conversation history is prepended" wording at `:239`, which the cap makes inaccurate once truncation applies.

Subsequent adversarial review rounds populate this section further.

### Pass 1 (2026-07-23, automated)

- **The existing tier-3 fidelity passthrough test is added to the edit lists and reconciled with the fail-closed resolve.** `TestRESTOpenAIResponsesIDFieldExtendedBehavior` (`tests/tier3_contract/rest_openai_responses/fidelity_test.go:239-251`) sends `previous_response_id:"resp_parent_42"` against a fresh memstore with no matching session seeded and asserts a `200` echo. CODE-4's resolve makes an unseeded parent id return `404 not_found_error`, so the passthrough assertion would fail once the proposal lands. The tier-3 testing bullet and §11 now direct seeding a `resp_parent_42` session (captured from `newResponsesServers`, which already returns the store) before the create, so the resolve succeeds and the passthrough assertion stays the fidelity check it was written to be. The fail-closed `404` path is pinned by the tier-1 unknown-id and tier-9 cross-tenant tests, so no coverage is lost.
- **A tier-1 copy-forward self-containment test and a three-turn chain test are added.** CODE-3 step 2 copies the resolved parent bucket forward into the child bucket so each session's transcript is self-contained across more than one hop, yet the prior test list exercised only the read/prepend side against a directly seeded parent bucket and a two-turn e2e. The copy-forward test asserts the child bucket holds the parent entries followed by the new turn in order, and the three-turn chain test asserts turn 3's dispatched slice still carries turn 1's context, so a copy-forward that appends to the wrong bucket, drops the parent entries, or mis-orders them is caught.
- **A tier-1 best-effort transcript-write-failure test is added.** CODE-3 step 3 makes the new transcript writes fail open (a write failure does not fail the response), a new error-handling behavior in a handler that previously wrote no transcripts. The added test injects a transcript store stub whose `Append` and `Get` return an error and asserts the create still returns `200` with a well-formed body, so an implementation that propagated the transcript error into `writeOpenAIError` is caught.

## 10. Open decisions for review

- **Replay cap default and unit.** The spec fixes neither the unit (most-recent-N turns versus a token budget) nor the numeric default, so the proposal makes it operator-tunable with a documented default. The human reviewer may prefer a specific default (for example the last 20 turns, or a per-runtime context-window fraction).
- **Whether to generalize the lineage column and copy-forward rehydration now.** The same dedicated field and copy-forward transcript rehydration could be generalized for any future `SupportsSessionContinuity` adapter, but A2A and Agent Protocol are Post-V1. The minimal-surface principle argues for keeping the mechanism Open-Responses-specific until a second consumer exists. Recommended: keep it specific now.

## 11. Files touched on application

- `spec/15_external-api-surface.md`: SPEC-1 (extend the single-shot continuation clause at `:603-605`). The only spec edit in this proposal.
- `pkg/gateway/session/sessionstore/sessionstore.go`: CODE-1 (the `OpenResponsesParentID` field).
- `migrations/0180_sessions_open_responses_parent.up.sql`, `migrations/0180_sessions_open_responses_parent.down.sql`: CODE-1 (the nullable column).
- `pkg/gateway/session/sessionstore/pgstore/pgstore.go`: CODE-1 (SELECT, INSERT, UPDATE, and Scan touchpoints for the new column).
- `pkg/gateway/environment/translator/open_responses.go`: CODE-2 (persist the lineage on the completion `store.Update`; read it on `handleGet`), CODE-3 (the transcript store field, per-turn record, and copy-forward), CODE-4 (the fail-closed resolve, the capped retrieve-and-prepend rehydration, and the replay-cap option).
- `pkg/gateway/environment/translator/open_responses_test.go`: TEST-1 (invert the lineage-drop test; add rehydration, cap, fail-closed, empty-transcript, copy-forward self-containment, three-turn chain, and best-effort transcript-write-failure cases).
- `cmd/lenny-gateway/credentialsurface.go`: CODE-3 (inject `w.transcripts` into `NewOpenResponsesHandler`).
- `docs/api/open-responses.md`: DOC-1 (soften the `:239` wording to the capped replay; add the fail-closed 404 statement).
- `pkg/gateway/session/sessionstore/pgstore/...` tier-2 test: TEST-1 (the column round-trip and cross-tenant reject).
- `tests/tier3_contract/rest_openai_responses/...`: TEST-1 (the GET wire body carries `previous_response_id`; seed `resp_parent_42` in the existing `fidelity_test.go:239-251` passthrough assertion so it stays green under CODE-4's fail-closed resolve).
- `tests/tier5_e2e_kind/...`: TEST-1 (the multi-turn conversation continuing across fresh pods on the deployed gateway).
- `tests/tier9_security/...`: TEST-1 (the cross-tenant `previous_response_id` reject).
