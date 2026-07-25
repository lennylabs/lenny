# Proposal: Build server-side previous_response_id continuation for OpenResponsesAdapter: persist the continuation lineage in the §14.1 envelope and rehydrate the full prior conversation onto the freshly-claimed pod by walking the continuation chain of per-response single-turn transcripts

- **Status:** Approved (2026-07-25). Human sign-off with the settled design: chain-walk single-turn transcript storage, `truncation` ignored (runtime owns context), no replay cap (full-history replay), translator-local continuity helper, §14.1-envelope lineage (no migration). Converged after 3 adversarial review rounds.
- **Date:** 2026-07-25.
- **Scope:** A code-and-test build that makes `previous_response_id` continuation functional on `OpenResponsesAdapter` (`POST /v1/responses`), plus one small §15 spec amendment that documents the server-side continuation behavior the §15 proper-superset coverage clause already obligates. Proposal 0055 (merged as 0057) built the single-shot pod-binding model but deferred continuity: `previous_response_id` is accepted and echoed on the create response yet does nothing server-side. This build persists the continuation lineage in the §14.1 request-envelope bundle so `GET /v1/responses/{id}` echoes it, and rehydrates the prior conversation onto the freshly-claimed single-shot pod by resolving `previous_response_id` and walking the `ContinuationParentID` chain of per-response single-turn transcript buckets from the referenced response to the chain root, then prepending the assembled turns in chronological order ahead of the new turn within the one `exec.Send`. An unknown or cross-tenant `previous_response_id` is rejected fail-closed as a native `404`. The single §15 amendment is the only spec edit. This proposal is the continuity follow-on 0055's Open decision 1 filed as option (b).

This document stages the proposed spec, code, and test changes. It does not modify any spec, code, or doc file. Apply the changes in the "Proposed changes" section after sign-off.

## 1. Problem

Spec §15 declares `OpenResponsesAdapter` covers OpenAI Responses API clients, and states OpenAI's Responses API is a proper superset of Open Responses whose only unsupported difference is proprietary hosted tools (`spec/15_external-api-surface.md:581`). `previous_response_id` is a core top-level Responses-API field for server-side conversation-state chaining, rather than a hosted tool, so the coverage clause at `:581` obligates the gateway to implement server-side `previous_response_id` chaining.

Proposal 0055 (merged as 0057) built the single-shot claim-dispatch-release model but deferred continuity, so today `previous_response_id` is accepted and echoed but does nothing server-side.

**1. The create path dispatches only the current turn.** `handleCreate` (`pkg/gateway/environment/translator/open_responses.go:231-317`) claims a fresh pod through the single-shot binder and dispatches only the current turn via `h.exec.Send(r.Context(), sessionID, msgs)` (`open_responses.go:289-300`) with no prior conversation prepended. It echoes `req.PreviousResponseID` into the create response (`open_responses.go:310,313,465`) but never persists it: the post-create `store.Update` closure sets only `s.State` (`open_responses.go:303-306`).

**2. `GET /v1/responses/{id}` reads a field the single-shot path never populates.** `handleGet` reads `previous_response_id` from `row.ParentSessionID` (`open_responses.go:397`), which the single-shot path deliberately never sets (`SingleShotSpec` carries no lineage pointer, `pkg/gateway/environment/translator/singleshot.go:49-54`; setting `ParentSessionID` would misclassify the ephemeral session as a §8.2/§8.6 delegated child). So GET echoes empty.

**3. The freshly-claimed pod starts with empty runtime memory.** Every single-shot pod is claimed fresh and released per request (SPEC §15 single-shot compute model; §6.2 pod state machine). Without gateway-side replay the runtime has no prior context, so a Responses SDK client running multi-turn with `previous_response_id` gets the model answering the new turn with zero prior conversation and no error. This is a silent wrong-answer defect.

The current test pins the broken behavior: `TestOpenResponsesSingleShotDropsPreviousResponseIDLineage` (`pkg/gateway/environment/translator/open_responses_test.go:202-234`) asserts an empty `ParentSessionID` and an empty GET echo. That test is the deliberate marker of the deferred capability, and it must be replaced once the capability lands.

This proposal discharges 0055's Open decision 1 (`proposals/0055_fix_build-the-15-single-shot-pod-binding-model-so-the-built-in-o.md:3,263`), resolved as option (b): keep `SupportsSessionContinuity: true` (`open_responses.go:30`) and file lineage persistence plus conversation rehydration as a bounded follow-on. `kind` is `fix`, matching 0055/0057: the capability is already promised by the `:581` proper-superset coverage clause and the code does not deliver it.

## 2. Decisions

- **`kind` is `fix`.** The capability is already promised by the proper-superset coverage clause at `spec/15_external-api-surface.md:581`; the code does not deliver it. This proposal does not narrow line 581 and does not add a second exception to it.
- **Persist the continuation lineage in a new dedicated field `Session.ContinuationParentID`, distinct from `Session.ParentSessionID`.** Reusing `ParentSessionID` is rejected because it is overloaded by the delegation machinery: the delegation-tree orphan-cleanup sweep walks the `ParentSessionID` chain (`treeRoot`, `pkg/gateway/mcpfabric/delegationtree/orphancleanup/orphancleanup.go:248`) and force-expires a non-terminal row whose chain-root is terminal past the cascade window. A continuation's referenced prior response is always already terminal, so a continuation that set `ParentSessionID` would have a terminal chain-root and could be swept to `StateExpired`. `ParentSessionID` also drives lease-tree suppression, usage archival, messaging scope, the watchdog, and credential-origin walks.
- **Persist `ContinuationParentID` through the §14.1 request-envelope JSONB bundle, with no dedicated column and no migration.** The bundle (`storedEnvelope`, `pkg/gateway/session/sessionstore/pgstore/pgstore.go:1308-1394`) already carries the `Origin`, `Labels`, and callback lineage fields wholesale on Create and Update. The continuation flow does only a by-id `Get` and a self-row GET echo, never a `WHERE` query, so no column or index is needed. The in-memory store round-trips the whole `Session` struct by value, so a new field persists unchanged with no per-field handling.
- **Rehydrate the full prior conversation with no cap.** Context-window management is the runtime's job: the runtime accumulates the conversation over the attach stream and calls the model. The gateway must not pre-truncate. An arbitrary turn cap would silently drop old context and reintroduce the exact silent-wrong-answer defect this fixes.
- **Storage is chain-walk single-turn buckets (human sign-off decision; do not re-litigate).** Each response's per-session transcript bucket holds only that response's own turn (the inbound normalized input plus the assistant output). On a continuation the gateway walks the `ContinuationParentID` chain from the referenced prior response back to the chain root, reading each ancestor's single-turn bucket with one tenant-scoped `transcriptstore.Get` per hop, and assembles the turns in chronological order (root first). This is O(N) aggregate storage across a conversation of N turns, an O(1) write per turn (each response records only its own turn), and a single source of truth: each turn lives in exactly one bucket, so redaction or erasure of a turn touches one bucket rather than every descendant copy. The trade-off is O(chain-length) reads per continuation, accepted because single-shot adapter turns are heavyweight, so conversations stay modest; a batched query keyed on a shared conversation root is a possible future optimization, but the naive pointer walk is sufficient for v1. Copy-forward (each bucket holding the full prior conversation copied forward, reusing the §15.1 `prompt_history` copy in `replay.go:186-191` for a single-`Get` rehydration) was considered and rejected: its code-reuse benefit does not justify O(N²) aggregate storage, an O(N) synchronous write on the Nth turn, and an erasure model where a turn's text is duplicated into every descendant bucket (see "Resolved in adversarial review").
- **Resolve `previous_response_id` fail-closed.** A tenant-scoped `sessionstore.Get` for an unknown or cross-tenant id (both map to `ErrNotFound` under the session-store isolation model) yields a native OpenAI `404` with no rehydration and no dispatch. A referenced response that exists but has an empty transcript rehydrates as empty prior history and dispatches normally.
- **Truncation and context-window overflow are ignored (human decision).** The gateway adds no `Truncation` request-field handler, no `context_length_exceeded` error code, no §15.4 error-registry edit, no gateway-side drop, retry, or truncation logic, no runtime conformance obligation, and no tier-10 context-overflow test. An unknown OpenAI `truncation` field is dropped by JSON decoding rather than rejected. A runtime context-window overflow flows out through the existing 0055 runtime-error-to-OpenAI-envelope path unchanged.
- **Place the continuity logic in a translator-local file (`continuity.go`) scoped to `OpenResponsesHandler`.** There is no standalone `pkg/gateway/environment/continuity` package: A2A and Agent Protocol are Post-V1 and not wired. The logic accepts the `sessionstore.Store` and `transcriptstore.Store` interfaces. The translator already imports `sessionstore` and `executor` and can import `transcriptstore` with no cycle, and it must not import `sessionserver` (which imports `translator` at `pkg/gateway/sessionserver/runtimes.go:13`).
- **Do not key on or redefine the `SupportsSessionContinuity` capability** (`pkg/gateway/runtime/adapter`; failover and restart reconstruction, a separate concern).
- **Record every completed turn unconditionally on its own response session id,** including a chain-root first turn that carries no `previous_response_id`, so a later continuation can rehydrate it. The record is best-effort, matching the canonical transcript-write path (`pkg/gateway/sessionserver/messages.go:573-591`).
- **Prepend the rehydrated prior conversation as leading `executor.Message` values ahead of the new turn's `normalizeInput` messages within the one `exec.Send` call.** The `Executor` interface exposes only `Send([]Message)` and `Close`, with no restore-without-dispatch primitive (`pkg/gateway/session/executor/executor.go:117-132`).
- **OpenAI Chat Completions stays out of scope.** It is stateless, the client resends the full history, and `SupportsSessionContinuity` is false.

## 3. The continuation request path after the change

`handleCreate` runs the following sequence. Steps 1, 2, and 6 are the current 0055 single-shot path; steps 3, 5, and the record in step 6 are new.

1. Decode and validate the request body, resolve the tenant, and split the `model` into an environment scope and a runtime reference, exactly as today.
2. Claim a warm pod, launch the runtime, and register the binding through the single-shot binder, exactly as today. The returned `sessionID` names this response.
3. When `req.PreviousResponseID` is non-empty, resolve it fail-closed: a tenant-scoped `sessionstore.Get` for the referenced response. On `ErrNotFound` (an unknown or cross-tenant id) write a native `404` and return with no dispatch; the deferred release still drains the freshly-claimed pod. Otherwise assemble the prior conversation by walking the `ContinuationParentID` chain from the referenced response back to the chain root, reading each ancestor's single-turn transcript bucket with one tenant-scoped `transcriptstore.Get` per hop (guarded by a visited-set cycle check), and convert the turns, in chronological order (root first), to leading `executor.Message` values. A hop whose transcript bucket is empty contributes no messages and the walk continues to its ancestors.
4. Normalize the new turn's input into `executor.Message` values, exactly as today.
5. Dispatch the turn: call `h.exec.Send(r.Context(), sessionID, prior+msgs)` in one call, with the rehydrated prior conversation prepended ahead of the new-turn messages. On a `Send` error return `500 server_error`; the deferred release drains the claimed pod.
6. On success, mark the session completed (`store.Update`, extended to also set `ContinuationParentID` from `req.PreviousResponseID` when it is non-empty), record this turn's own transcript on `sessionID` (the new inbound input plus the assistant output only, excluding the rehydrated prior conversation), best-effort, and write the response body. The deferred release fires as the handler returns.

`GET /v1/responses/{id}` reads `previous_response_id` from `row.ContinuationParentID`, so a persisted lineage echoes the originating `previous_response_id`; a chain-root response echoes empty.

## 4. Edge cases and accepted failure modes

Each row names the observable outcome and the spec text that states it. The SPEC-1 continuation clause is the spec basis for the rehydration and fail-closed rows. The reader-facing documentation of the continuation behavior on `docs/api/open-responses.md` is a follow-on outside this proposal's staged edits, mirroring how 0055 deferred its reader-facing page.

| Scenario | Observable outcome | Spec text |
|:--|:--|:--|
| Continuation with a known, same-tenant `previous_response_id` | The gateway walks the continuation chain to assemble the prior conversation as leading context ahead of the new input, dispatches on the freshly-claimed pod, and the runtime continues the conversation despite the pod's empty runtime memory; `GET /v1/responses/{id}` echoes the `previous_response_id` | SPEC-1 continuation clause; proper-superset coverage (`spec/15_external-api-surface.md:581`) |
| Unknown `previous_response_id` | Native `404` in the OpenAI envelope; no rehydration and no dispatch; the freshly-claimed pod is drained by the deferred release | SPEC-1 fail-closed continuation clause; session-store `ErrNotFound` isolation |
| Cross-tenant `previous_response_id` | Native `404` (a cross-tenant id maps to `ErrNotFound` under session-store isolation); no rehydration and no dispatch | SPEC-1 fail-closed continuation clause; §4.2 session-store tenant isolation |
| A hop in the chain has an empty transcript bucket (e.g., its best-effort record failed) | That hop contributes no messages; the walk continues to its ancestors, so the remaining history still rehydrates; if the whole chain is empty the new turn dispatches with no prepend | SPEC-1 continuation clause; `transcriptstore` `ErrNotFound` tolerated (`transcriptstore.go:66-67,82-84`) |
| Chain-root turn (no `previous_response_id`) | Recorded unconditionally on its own session id so a later continuation can rehydrate it; no prepend; `GET` echoes empty `previous_response_id` | SPEC-1 (continuation lineage persisted); §15.1 session transcript |
| Best-effort transcript append failure on record | The response still returns `200`; the record is best-effort, matching the canonical transcript-write path | §15.1 session transcript best-effort write (`messages.go:573-591`) |
| Unknown OpenAI `truncation` request field present | Dropped by JSON decoding rather than rejected; no gateway-side truncation handling | SPEC-1 (no truncation clause; §15.4 error registry unchanged; accepted, human decision) |
| Runtime context-window overflow on a large rehydrated conversation | Flows out through the existing 0055 runtime-error-to-OpenAI-envelope path unchanged; no gateway-side drop, retry, or truncation | SPEC-1 (no gateway-side context management; §15 single-shot runtime-error mapping; accepted, human decision) |
| Full-history replay with no cap | The entire prior conversation is replayed; the gateway applies no cap or most-recent-N slice | SPEC-1 continuation clause (context management is the runtime's job) |

## 5. Proposed changes

### SPEC-1. Document server-side previous_response_id continuation in the §15 single-shot compute-model clause

**Target:** `spec/15_external-api-surface.md`, the continuation sentence in the built-in adapter single-shot compute-model paragraph (the `OpenResponsesAdapter` continuation clause, currently "re-claims a fresh pod per request and does not retain a pod bind across calls").

**Rationale:** The continuation clause today describes only the pod lifecycle across calls and is silent on conversation state, which reads as if `previous_response_id` were a server-side no-op. The proper-superset coverage clause at `:581` obligates server-side chaining. Extending this continuation clause (rather than narrowing `:581` or adding a second exception to it) is the single spec edit. The clause states observable behavior and leaves the copy-forward-versus-chain-walk retrieval strategy and the prepend mechanics to CODE-2/CODE-3/CODE-4.

**Anchor:** Replace the continuation sentence in the built-in adapter single-shot compute-model paragraph.

**Change (staged spec text).** Replace:

```markdown
`OpenResponsesAdapter` continuation
(`previous_response_id`) re-claims a fresh pod per request and does not retain a
pod bind across calls.
```

with:

```markdown
`OpenResponsesAdapter` continuation (`previous_response_id`) re-claims a fresh
pod per request and does not retain a pod bind across calls. On a continuation
the gateway resolves `previous_response_id` to its prior response and applies
that response's prior conversation context ahead of the new input before
dispatching it onto the freshly-claimed pod, so the runtime continues the
conversation despite the fresh pod's empty runtime memory. The continuation
lineage is persisted, so `GET /v1/responses/{id}` echoes the originating
`previous_response_id`. An unknown or cross-tenant `previous_response_id` is
rejected fail-closed as a native `404`, with no rehydration and no dispatch.
```

### CODE-1. Add `Session.ContinuationParentID` and persist it through the §14.1 request-envelope bundle

**Target:** `pkg/gateway/session/sessionstore/sessionstore.go` (new `ContinuationParentID` field near `ParentSessionID` at `:145`); `pkg/gateway/session/sessionstore/pgstore/pgstore.go` (`storedEnvelope` at `:1308-1337`, `requestEnvelopeArg` including the all-empty nil guard at `:1343-1372`, `applyStoredEnvelope` at `:1374-1394`).

**Rationale:** The lineage must survive so `GET` echoes the real value. A dedicated field distinct from `ParentSessionID` avoids the delegation-tree orphan-cleanup sweep and the other `ParentSessionID`-keyed machinery (Decision). The `storedEnvelope` bundle already carries non-columnar lineage fields and is rewritten wholesale on Create and Update, so a bundled field needs no SQL column and no migration. The in-memory store round-trips the whole struct by value, so it needs no change.

**Anchor:** Add the struct field immediately after `ParentSessionID` in `sessionstore.Session`.

**Change (staged Go).** Add to `sessionstore.Session`:

```go
// ContinuationParentID is the OpenResponsesAdapter previous_response_id of
// the prior response this single-shot continuation chains from. It is
// distinct from ParentSessionID, which the §8.2/§8.6 delegation machinery
// and the delegation-tree orphan-cleanup sweep own: a single-shot
// continuation must not set ParentSessionID or it would be misclassified as
// a delegated child and swept. The pointer rides the §14.1 request-envelope
// bundle rather than a dedicated column; GET /v1/responses/{id} echoes it as
// previous_response_id.
// spec: §15 built-in adapter single-shot compute model.
ContinuationParentID string
```

**Change (staged description) for `pgstore.go`.**

1. Add `ContinuationParentID string `json:"continuationParentId,omitempty"`` to `storedEnvelope`.
2. In `requestEnvelopeArg`, set `ContinuationParentID: sess.ContinuationParentID` on the `storedEnvelope` literal, and extend the all-empty short-circuit guard with `&& env.ContinuationParentID == ""` so a session carrying only a continuation pointer still persists the bundle.
3. In `applyStoredEnvelope`, add `s.ContinuationParentID = env.ContinuationParentID`.

The `Memory` store needs no edit. Carry a `// spec: §15 built-in adapter single-shot compute model` tie on the new field.

### CODE-2. Persist the continuation lineage on create and read it back in GET

**Target:** `pkg/gateway/environment/translator/open_responses.go` (post-create `store.Update` completion closure at `:303-306`; GET envelope at `:397`).

**Rationale:** The single-shot path today persists no lineage (the `Update` closure sets only `State`) and `GET` reads the never-populated `row.ParentSessionID`. Persisting `ContinuationParentID` on the completion closure and reading it in `GET` makes `GET` echo the real `previous_response_id`.

**Change (staged description).**

1. In the post-create `store.Update` completion closure (`open_responses.go:303-306`), set `s.ContinuationParentID = req.PreviousResponseID` alongside `s.State = session.StateCompleted`, only when `req.PreviousResponseID` is non-empty, so chain-root rows stay clean.
2. In `handleGet`, change `PreviousResponseID: row.ParentSessionID` (`open_responses.go:397`) to `PreviousResponseID: row.ContinuationParentID`.
3. Leave the create-response echo unchanged (it echoes `req.PreviousResponseID` directly at `:310`, `:313`, and `:465`).

### CODE-3. Add a translator-local continuity helper: resolve, tenant-check, chain-walk rehydrate, and per-turn record

**Target:** new `pkg/gateway/environment/translator/continuity.go` (package `translator`); consumes `pkg/gateway/environment/transcriptstore` (`Store.Append`/`Get`, tenant-scoped `ErrNotFound`, `transcriptstore.go:66-98`) and `pkg/gateway/session/sessionstore`.

**Rationale:** The resolve, ownership-check, rehydrate, and record logic is scoped to `OpenResponsesHandler` and must live in package `translator` (no standalone continuity package; the translator cannot import `sessionserver` but can import `transcriptstore` with no cycle). The sole transcript writers today (`messages.go:591`, `replay.go:189`) are in package `sessionserver` and not on the adapter path, so the adapter must record its own turns. Each response records only its own turn on its own bucket; a continuation walks the `ContinuationParentID` chain and reads each ancestor's single-turn bucket, so a turn is stored once (O(N) storage, single source of truth for erasure) rather than copied forward into every descendant (Decision).

**Anchor:** New file `continuity.go` in package `translator`.

**Change (staged Go sketch).** Define an unexported helper and its typed not-found sentinel:

```go
// errContinuationNotFound marks an unknown or cross-tenant
// previous_response_id. The handler maps it to a native OpenAI 404 with no
// rehydration and no dispatch (fail-closed continuation resolution).
// spec: §15 built-in adapter single-shot compute model.
var errContinuationNotFound = errors.New("continuity: previous_response_id not found")

// continuity resolves an OpenResponsesAdapter previous_response_id to its
// prior conversation and records each response's own turn. Storage is
// chain-walk: each response's bucket holds only its own turn, and a
// continuation walks the ContinuationParentID chain to the root, reading each
// ancestor's single-turn bucket. A turn is stored once (O(N) storage, single
// source of truth), not copied forward into every descendant.
// spec: §15 built-in adapter single-shot compute model.
type continuity struct {
	sessions    sessionstore.Store
	transcripts transcriptstore.Store
}

// rehydrate resolves prevID fail-closed, then walks the ContinuationParentID
// chain from prevID back to the chain root, and returns the prior conversation
// as leading executor.Message values in chronological order (root first). An
// unknown or cross-tenant prevID returns errContinuationNotFound. A hop whose
// transcript bucket is empty (ErrNotFound) contributes nothing; the walk
// continues to its ancestors. A visited set guards against a cycle.
func (c *continuity) rehydrate(ctx context.Context, tenantID, prevID string) ([]executor.Message, error) {
	// perTurn[i] is the messages of one response, collected newest-first.
	var perTurn [][]executor.Message
	visited := map[string]bool{}
	for id := prevID; id != ""; {
		if visited[id] {
			break // cycle guard
		}
		visited[id] = true
		row, err := c.sessions.Get(ctx, tenantID, id)
		if err != nil {
			if errors.Is(err, sessionstore.ErrNotFound) {
				if id == prevID {
					return nil, errContinuationNotFound // referenced id fails closed
				}
				break // a missing ancestor (e.g., erased) ends the walk
			}
			return nil, err
		}
		entries, err := c.transcripts.Get(ctx, tenantID, id)
		if err != nil && !errors.Is(err, transcriptstore.ErrNotFound) {
			return nil, err
		}
		msgs := make([]executor.Message, 0, len(entries))
		for _, e := range entries {
			msgs = append(msgs, executor.Message{Role: e.Role, Content: e.Content})
		}
		perTurn = append(perTurn, msgs)
		id = row.ContinuationParentID
	}
	// reverse to chronological (root first) and flatten.
	var out []executor.Message
	for i := len(perTurn) - 1; i >= 0; i-- {
		out = append(out, perTurn[i]...)
	}
	return out, nil
}

// record writes this response's own turn on its own session id: the new
// inbound input, then the assistant text output. It does NOT copy the prior
// conversation forward. Best-effort, matching the canonical transcript-write
// path (messages.go).
func (c *continuity) record(ctx context.Context, tenantID, sessionID string,
	in []executor.Message, out []executor.MessagePart) {
	// build transcriptstore.Entry values from in (by role) + out text,
	// then best-effort c.transcripts.Append(ctx, tenantID, sessionID, entries...).
}
```

The file comment notes that under chain-walk each response's bucket is an ordinary single-turn per-session transcript that the §12.8 erasure orchestrator already covers by walking the user's sessions, and because each turn is stored in exactly one bucket, erasing or redacting a turn touches one bucket rather than every descendant copy; no adapter-specific erasure handling is added.

### CODE-4. Wire the continuity helper into `OpenResponsesHandler`: rehydrate and prepend on a continuation, record every turn

**Target:** `pkg/gateway/environment/translator/open_responses.go` (`OpenResponsesHandler` struct at `:161-169`; `OpenResponsesOptions` at `:171-188`; `NewOpenResponsesHandler` at `:190-220`; `handleCreate` at `:231-317`).

**Rationale:** `OpenResponsesHandler` has no transcripts field today and dispatches only the current turn. It must gain a `transcriptstore.Store` dependency, rehydrate on a continuation, prepend the rehydrated prior conversation ahead of the new-turn messages in the one `exec.Send` call (no restore-without-dispatch primitive exists), and record the completed turn on its own session id.

**Change (staged description).**

1. Add a `transcripts transcriptstore.Store` field to `OpenResponsesHandler` and a `Transcripts transcriptstore.Store` option to `OpenResponsesOptions`. In `NewOpenResponsesHandler`, build a `continuity` helper over `store` and `transcripts`. Tolerate a nil transcripts store: the helper's rehydrate is a no-op that returns empty prior history and its record is a no-op, preserving the in-memory and unit path.
2. In `handleCreate`, after `normalizeInput` (`:289`) and before `exec.Send` (`:295`): when `req.PreviousResponseID != ""`, call `continuity.rehydrate(r.Context(), tenantID, req.PreviousResponseID)`. On `errContinuationNotFound`, write `writeOpenAIError(w, http.StatusNotFound, "not_found_error", ...)` and return with no dispatch (the deferred release still drains the freshly-claimed pod). On any other error, write `500 server_error`. Otherwise prepend the returned ancestor messages ahead of `msgs` in the single `h.exec.Send(r.Context(), sessionID, append(prior, msgs...))` call.
3. After the successful completion `store.Update` (`:303-306`, extended by CODE-2), call `continuity.record` with the new inbound normalized input (the new turn only) and the assistant output (excluding the rehydrated prior conversation, which stays in the ancestors' own buckets), unconditionally, including a chain-root turn that carries no `previous_response_id`.
4. Do not add a `Truncation` field to `OpenResponsesRequest`; an unknown `truncation` field is dropped by JSON decoding.

Run `gofumpt` and `goimports`. Carry `// spec: §15 built-in adapter single-shot compute model` ties on the rehydrate, prepend, and record logic.

### CODE-5. Inject the transcript store into the Open Responses adapter at the gateway wiring boundary

**Target:** `cmd/lenny-gateway/credentialsurface.go` (`NewOpenResponsesHandler` construction at `:66`); `cmd/lenny-gateway/wiring_fields.go` (`w.transcripts transcriptstore.Store` at `:335`).

**Rationale:** The wiring struct already holds `transcripts transcriptstore.Store` (`wiring_fields.go:335`) and `cmd/lenny-gateway` already imports `transcriptstore`. Passing it into the handler enables rehydration and recording on the deployed `PodExecutor` path; the OpenAI Chat Completions handler stays unchanged.

**Change (staged description).**

1. At `credentialsurface.go:66`, add `Transcripts: w.transcripts` to the `translator.OpenResponsesOptions` literal passed to `NewOpenResponsesHandler`.
2. Leave the `NewOpenAIChatHandler` construction at `:65` unchanged.

## 6. Non-goals

- **Truncation handling of any kind.** This proposal adds no `Truncation` request-field handler, no `context_length_exceeded` error code, no §15.4 error-registry edit, no gateway-side drop, retry, or truncation logic, and no tier-10 conformance test for context overflow. A runtime context-window overflow flows out through the existing 0055 runtime-error-to-OpenAI-envelope path unchanged. A separate runtime-owned auto-truncation or context-reset design is out of scope and is not filed by this proposal.
- **A replayed-history cap or most-recent-N slice.** The full conversation is replayed.
- **Copy-forward transcript storage.** The design records each response's own turn on its own bucket and walks the `ContinuationParentID` chain on a continuation, rather than copying the prior response's transcript forward into each descendant bucket (the §15.1 `prompt_history` copy in `replay.go`). Copy-forward was considered and rejected for its O(N²) storage and per-descendant erasure cost (see "Resolved in adversarial review").
- **A standalone `pkg/gateway/environment/continuity` package.** The logic is translator-local.
- **A dedicated `ContinuationParentID` SQL column or database migration.** The field rides the existing §14.1 request-envelope bundle.
- **OpenAI Chat Completions continuation.** The adapter is stateless, the client resends the full history, and `SupportsSessionContinuity` is false.
- **Keying on or redefining the `SupportsSessionContinuity` capability** (failover and restart reconstruction, a separate concern).
- **Narrowing `spec/15_external-api-surface.md:581` or adding any second exception to its proper-superset coverage clause.**
- **Retaining a pod bind across continuation calls or any change to the 0055 single-shot claim, dispatch, and release lifecycle.**
- **A cross-reference edit in `transcriptstore.go` documenting continuity erasure.** The store already documents the §12.8 per-session erasure model (`transcriptstore.go:70-77,184-187`), and under chain-walk a continued response's transcript is an ordinary single-turn per-session bucket the erasure orchestrator's existing walk already covers, so the note would document a non-event and would be the first adapter-specific back-reference into a concern-agnostic store. Dropped (see "Resolved in adversarial review").

## 7. Testing

The change reaches tier 0 (static), tier 1 (the chain-walk rehydrate and prepend, the fail-closed resolution, the per-turn record, and the `GET` echo mapping, in-process with a fake executor and the Memory stores), and tier 2 (the `ContinuationParentID` envelope round-trip and the chain-walk transcript persistence across per-response buckets against a single Postgres container) per `.claude/rules/test-coverage.md`. The continuity helper is datastore-only (`sessionstore.Store` plus `transcriptstore.Store`, both Memory- and Postgres-backed, with no kube-apiserver read or write), so message ordering into `exec.Send` is pinned deterministically at tier 1 with a fake executor rather than by driving a `PodExecutor` on envtest. Each test below covers a non-happy path and carries a `// spec:` tie; every tier-2 test carries a `// diagnosis:` comment.

- **tier-1 fail-closed continuation resolution (spec-named-failure, boundary):** In `pkg/gateway/environment/translator`, assert that a `POST /v1/responses` carrying an unknown `previous_response_id` returns a native `404` with no `exec.Send` call, and that a cross-tenant `previous_response_id` (a valid response id owned by another tenant) also returns `404` with no dispatch. The non-happy paths are the unknown id and the cross-tenant id. `// spec: §15 (fail-closed continuation resolution); §4.2 (session-store tenant isolation)`.
- **tier-1 chain-walk rehydration and prepend order (boundary):** With a fake executor that captures the `[]executor.Message` slice it receives, drive a chain-root turn, then a continuation off it, and assert the continuation's `exec.Send` receives the prior conversation prepended ahead of the new-turn messages, in chronological order (root first). Drive a third continuation and assert its dispatch receives the full multi-turn history in order, confirming the chain-walk assembles turns across the per-response buckets. Assert that each response's own bucket holds only its own turn (no copy-forward duplication). The non-happy path is the multi-turn ordering boundary. `// spec: §15 (server-side previous_response_id continuation, full-history replay)`.
- **tier-1 referenced response with an empty transcript (boundary):** Assert a continuation whose referenced response exists but recorded no transcript dispatches with an empty prior history (no prepend) and returns `200`, rather than a `404`. The non-happy path is the empty-transcript continuation. `// spec: §15 (continuation resolution); transcriptstore ErrNotFound tolerated`.
- **tier-1 mid-chain empty transcript bucket continues the walk (boundary):** Build a chain root -> middle -> referenced where the middle hop's session row exists but its transcript bucket is empty (its best-effort record failed, so `transcriptstore.Get` returns `ErrNotFound`). Assert the continuation's `exec.Send` still receives the root's turns in chronological order (root first), the empty middle hop contributes no messages, and the response returns `200`. This pins the branch at which a mid-chain transcript `ErrNotFound` is tolerated and the walk continues to the ancestors via `id = row.ContinuationParentID`, distinguished from the referenced-id fail-closed path. The non-happy path is the empty middle hop. `// spec: §15 (chain-walk rehydration, empty-hop tolerated); transcriptstore ErrNotFound tolerated`.
- **tier-1 missing mid-chain ancestor session terminates the walk (spec-named-failure, boundary):** Build a chain root -> middle -> referenced where the middle hop's session row is absent (an erased ancestor, so `sessionstore.Get` returns `ErrNotFound` on a non-referenced hop). Assert the walk terminates at the gap, dropping ancestors older than the gap while keeping the newer collected turns (the referenced response's own turn), and the continuation returns `200` rather than `404`. This pins the `break` branch for a missing ancestor as distinct from the referenced-id fail-closed `404` (which fires only when `id == prevID`). The non-happy path is the missing mid-chain ancestor. `// spec: §15 (chain-walk rehydration, erased-ancestor walk termination); §4.2 (session-store tenant isolation)`.
- **tier-1 per-turn record including a chain-root turn (boundary):** Assert that a chain-root turn carrying no `previous_response_id` is recorded on its own session id (so a later continuation rehydrates it), and that a best-effort transcript append failure does not fail the response (the handler still returns `200`). The non-happy paths are the chain-root record and the append failure. `// spec: §15 (continuation lineage persisted); §15.1 (best-effort transcript write)`.
- **tier-1 GET echoes the persisted lineage; replaces the drops-lineage test (boundary):** Replace `TestOpenResponsesSingleShotDropsPreviousResponseIDLineage` (`open_responses_test.go:202-234`), which pins the broken behavior. Assert that a continuation stores `ContinuationParentID = previous_response_id` on the session row and that `GET /v1/responses/{id}` echoes the originating `previous_response_id`, and that a chain-root response stores an empty `ContinuationParentID` and echoes empty. The non-happy path is the chain-root row that must echo empty. `// spec: §15 (continuation lineage persisted, GET echo)`.
- **tier-2 `ContinuationParentID` envelope round-trip (boundary):** Against a single Postgres container, persist a session carrying only a `ContinuationParentID` (no other bundled field) through the pgstore and read it back, asserting the value survives the §14.1 request-envelope bundle and that the all-empty nil guard does not drop the bundle. The non-happy path is the continuation-pointer-only session that the guard must not treat as empty. `// spec: §14.1 (request-envelope bundle); §15 (continuation lineage persisted)`.
- **tier-2 chain-walk transcript persistence (boundary):** Against a single Postgres-backed `transcriptstore`, record a chain-root turn and a continuation (each on its own bucket), then rehydrate the continuation by walking the chain and assert the prior conversation reads back in chronological order across the per-response buckets, and that each bucket holds only its own single turn. The non-happy path is the cross-datastore round-trip that a Memory store cannot exercise. `// spec: §15 (chain-walk rehydration across per-response single-turn buckets)`.

The existing tier-3 in-process Open Responses contract suite remains the wire-body coverage for the `/v1/responses` surface this proposal does not change; it runs as a regression check against the reshaped handler, and no parallel copies are authored.

## 8. Findings closed on application

This proposal closes no `TEST-GAPS.md` finding. It discharges 0055's Open decision 1 (`proposals/0055_...md:3,263`), resolved as option (b): the deferred lineage-persistence-plus-rehydration follow-on. On application, the marker test `TestOpenResponsesSingleShotDropsPreviousResponseIDLineage` (`open_responses_test.go:202-234`), which pins the deliberately-deferred behavior, is replaced by the tier-1 GET-echo test in TEST-1, so the applied change leaves no failing test and the new behavior is pinned.

## 9. Resolved in adversarial review

Subsequent adversarial review rounds populate this section. The challenge-round revisions carried in the draft narrowed the proposal from its original form:

- **SPEC-1 was trimmed to behavioral prose.** The original continuation clause baked the chain-walk retrieval strategy into the spec ("by walking the continuation chain"), yet the retrieval strategy is a code-level choice. The clause now states only what a client observes (prior context applied ahead of the new input, lineage persisted for the GET echo, unknown or cross-tenant id rejected as `404`) and leaves copy-forward-versus-chain-walk and the prepend mechanics to CODE-2/CODE-3/CODE-4.
- **CODE-3 uses chain-walk single-turn buckets (human sign-off decision).** A challenge round proposed switching the helper to copy-forward (each bucket holding the full prior conversation copied forward, reusing the `replayMode: prompt_history` copy at `replay.go:186-191` for a single-`Get` rehydration) on the grounds that it reuses one existing rehydration surface. That switch is rejected by human sign-off: the code-reuse benefit does not justify O(N²) aggregate storage, an O(N) synchronous write on the Nth turn, and an erasure model where a turn's text is duplicated into every descendant bucket. The helper records each response's own turn on its own bucket and, on a continuation, walks the `ContinuationParentID` chain from the referenced response to the root, reading each ancestor's single-turn bucket, guarded by a visited-set cycle check. This is O(N) storage, an O(1) write per turn, and a single source of truth so erasure or redaction of a turn touches one bucket; the trade-off is O(chain-length) reads per continuation, accepted because single-shot turns are heavyweight so conversations stay modest.
- **TEST-1's tier-2 scope was narrowed.** The original plan drove one adapter handler against a real `PodExecutor`-backed `sessionserver.Server` on envtest to re-check that ancestor messages reach the pod ahead of the new turn. That mislabels a Postgres-only datastore flow as an envtest test: the continuity helper is datastore-only and message ordering into `exec.Send` is verified deterministically at tier 1 with a fake executor. The tier-2 coverage is now a single-Postgres-container `ContinuationParentID` envelope round-trip and a chain-walk transcript round-trip across per-response single-turn buckets.
- **DOC-1 was dropped.** The proposed continuity GDPR note had two targets. The `continuity.go` file-level note is already placed by CODE-3, and under chain-walk the store-level cross-reference in `transcriptstore.go` would document a non-event (a continued response's bucket is an ordinary single-turn per-session transcript the §12.8 orchestrator already erases) and would introduce the first adapter-specific back-reference into a deliberately concern-agnostic store. The note stays only in `continuity.go`.

### Pass 1 (2026-07-25, automated)

- **Scope bullet corrected to chain-walk.** The Scope bullet (line 5) still described the rejected copy-forward design, stating the build rehydrates "by copying the prior response's transcript forward ahead of the new turn, reusing the §15.1 `prompt_history` rehydration model the replay path already implements." That contradicted the settled chain-walk design carried by the title, the Decisions, CODE-3, and the Non-goals, and named the reuse surface (`replay.go:186-191`) the design deliberately avoids. The bullet now states the chain-walk mechanism: resolve `previous_response_id`, walk the `ContinuationParentID` chain of per-response single-turn transcript buckets from the referenced response to the chain root, and prepend the assembled turns in chronological order ahead of the new turn within the one `exec.Send`.
- **Tier-1 coverage added for the two mid-chain missing-hop branches.** The rehydrate sketch has two distinct branches the prior test list did not pin: a mid-chain transcript bucket that is empty (`transcriptstore.Get` returns `ErrNotFound`) tolerates the gap and continues the walk to the ancestors via `id = row.ContinuationParentID`, while a missing mid-chain session row (`sessionstore.Get` returns `ErrNotFound` on a non-referenced hop) breaks the walk at the gap. Section 7 now adds a tier-1 test that builds a root -> middle -> referenced chain with the middle hop's transcript bucket empty and asserts the root's turns still rehydrate in chronological order with a `200`, and a companion tier-1 test with the middle hop's session row absent (erased ancestor) that asserts the walk terminates at the gap, keeps the newer collected turns, and returns `200` rather than the referenced-id fail-closed `404`.

## 10. Files touched on application

- `spec/15_external-api-surface.md`: SPEC-1 (extend the continuation sentence in the built-in adapter single-shot compute-model paragraph). The only spec edit in this proposal.
- `pkg/gateway/session/sessionstore/sessionstore.go`: CODE-1 (the new `ContinuationParentID` field on `Session`).
- `pkg/gateway/session/sessionstore/pgstore/pgstore.go`: CODE-1 (`storedEnvelope` field, `requestEnvelopeArg` including the all-empty nil guard, and `applyStoredEnvelope`).
- `pkg/gateway/environment/translator/open_responses.go`: CODE-2 (persist `ContinuationParentID` on completion, read it in `GET`) and CODE-4 (the transcripts field and option, the continuity helper, the rehydrate-and-prepend on a continuation, and the per-turn record).
- `pkg/gateway/environment/translator/continuity.go`: CODE-3 (new file; the chain-walk `continuity` helper, its `errContinuationNotFound` sentinel, the chain-walking `rehydrate`, and the own-turn-only `record`).
- `pkg/gateway/environment/translator/open_responses_test.go`: TEST-1 (replace `TestOpenResponsesSingleShotDropsPreviousResponseIDLineage` with the continuation rehydration, fail-closed, per-turn record, and GET-echo tier-1 tests).
- `cmd/lenny-gateway/credentialsurface.go`: CODE-5 (pass `Transcripts: w.transcripts` into `NewOpenResponsesHandler`).
- `tests/tier2_component/...`: TEST-1 (the single-Postgres `ContinuationParentID` envelope round-trip and the chain-walk transcript persistence round-trip across per-response single-turn buckets).
