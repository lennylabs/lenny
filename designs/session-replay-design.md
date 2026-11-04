# Session replay

Status: design for owner review, revision 6, 2026-11-04. Revisions 2 to 6 apply the reviews of revisions 1 to 5, under their own sections, and no review has read revision 6. Nothing here is built or staged, and no test or spike was run for it. Statements about the tree were checked by reading the code and the specification. It reads against `tenant-derived-runtimes-design.md` revision 24, `tenant-scoping-identity-design.md` revision 21, and `egress-and-git-design.md` revision 59. Proposals carry the label RP-n.

## Problem

§15.1 **Session Replay Semantics** describes replay as "the primary mechanism for regression testing and A/B evaluation of runtime upgrades". A regression test needs the new run to start from the state the original run started from.

Replay does not do that today. Under the default mode, `prompt_history`, §15.1 states that "the workspace is populated with the source session's sealed final workspace snapshot (or last checkpoint if the source session did not seal cleanly)". The prompts therefore run a second time against a workspace that already holds their results. A prompt that says "create `feature.py`" runs where `feature.py` exists, so a difference between the two runs can come from the changed workspace as well as from the new runtime.

Replay also delivers no prompts today. §15.1 states that the prompt history "is replayed verbatim as the initial message sequence", and the code copies the transcript into the new session and never reads it back on a start path (F-15.1.77). A replayed session therefore idles on a copied workspace, and the regression test does not run.

Replay's other mode, `workspace_derive`, duplicates derive, as "Replay modes" states.

## Owner's choice

On 2026-11-04 the owner separated derive from replay: "Replay is different. It should start from the beginning, and that is whatever commits the pod originally started with." When asked how a replay should treat default clones that a newer runtime version changed, the owner answered: "A replay should start exactly where the source started with". The owner also asked for this design to have its own document, and wrote "I feel inclined to just keep derive" about `workspace_derive`.

Derive keeps its own rules, which `tenant-derived-runtimes-design.md`, DR-9, "Derive", states. A derive continues from a snapshot, and a replay starts from the beginning.

## Scope

This design covers what a replayed session starts from, what its plan may do, and the removal of `replayMode`. It applies to every session on every runtime.

These are out of scope:

- Derive, apart from removing the replay mode that duplicates it.
- Comparing the results of two runs. `evalRef` stays as it is.
- Making external systems repeat their answers. A replayed session re-executes its tool calls against whatever those systems hold at that time.

## Requirements

- R1. A replayed session starts from the plan the source session bound, with the same commits, and from no snapshot.
- R2. A replay composes nothing from the target runtime. The target contributes its image, its process, and its pods.
- R3. A check that would refuse a new session with this plan refuses the replay, before a pod is claimed.
- R4. A replay does not push to a repository unless the request asks for that.
- R5. A replay does not bring back content that a stop has ended.
- R6. The platform delivers the source's client prompts to the replayed session, in order, with no client composing them. The client still calls `finalize` and `start`, as for any session.

## Security properties

- RPP1. The replayed session takes the source session's owner, as §10.2 **Session ownership.** states, and credentials are evaluated afresh. From the source it takes the plan, its client prompts, its `env` and `runtimeOptions`, and what `tenant-scoping-identity-design.md` states a replay takes: its environment, `egressLockHosts`, `crossed_from_environment`, and its delegation limit.
- RPP2. Every check runs against the plan on the replayed session's user, environment, and target pool, as for a plan a client submits.
- RPP3. A plan carrying content from a tenant-derived runtime is refused after any later content stop on that runtime's grant entry or its tenant's security record, on any route and through any chain of replays or derives, whether or not the content is still on the runtime.
- RPP4. A plan or an export carrying content from a tenant-derived runtime is refused on a pod with no runtime supervisor, on a replay, a derive, and a delegated child, because it would run at the adapter's identity there.

## Current state

`handleReplay` (`pkg/gateway/sessionserver/replay.go`) requires a terminal source with a resolvable workspace snapshot, checks `executionMode` through `requireReplayRuntimeCompatible`, copies the snapshot with `copyWorkspaceSnapshot`, and sets `ParentWorkspaceRef`. Under `prompt_history` it copies the transcript in the insert transaction through `CreateWithTranscript`, using `CopySessionTx`, which copies rows of every role, including assistant output. In `sessionserver`, the transcript store is read by the GET routes, `taskrecord.go`, and `usage.go`, and no start path reads it. The new row holds no plan, and one binds at finalize, which composes the target runtime's current defaults. §7.1 **Derive restore semantics:** then restores the snapshot at first bind and applies the plan over it.

Other facts this design relies on:

- `GET /v1/sessions/{id}` shows that a session row stores its composed plan, with `resolvedCommitSha` on each `gitClone` source. §14.1 **Plan binding.** states that a session binds at most one plan.
- §14 states that an `uploadRef` belongs to the session for which its content was stored and is valid only in a plan bound to that session. `UploadDefaultTTL` in `sessionserver/upload.go` stamps a 7-day TTL on uploads.
- `workspaceplan.Plan` holds `SchemaVersion`, `Sources`, and `SetupCommands`. `Env` and `RuntimeOptions` are fields on the session row, and `handleReplay` copies neither today.
- `recordDeliveredResponse` runs when the runtime's turn returns, and it appends the inbound messages and the text parts of the response to the transcript. That append is best-effort, and `mcptools/send_delivery.go` and the child send path make none. `transcriptstore.Entry` records a role and no sender, and a held message from a sibling agent is recorded as `user`. `deriveMessageFrom` (`messages_list.go`) infers a sender from the role, so `GET /messages` labels such a message as the client's. `projectMessage` sends a message's text alone, so attachments do not reach the transcript.
- `deliverBuffered` (`held_delivery.go`) delivers held messages one at a time, stops on a terminal row, checks the coordination lease before each, and runs at start, resume, and takeover. `start.go`, `resume.go`, and a sibling's `sendNow` (`mcptools/send_delivery.go`) call it in line, on their request's context. `ResumeBacklog` (`suspended_backlog.go`) is the existing pattern for work a session needs off a request. `DeliverHeldAfterTakeover` returns unless the row is `running`, and `input_required → running` is no delivery point.
- `handleReplay` and `handleDerive` call none of `requireTenantClassification`, `requireConcurrencyLimits`, and `requireAdmissionRateLimit`, which `create.go` and `create_and_start.go` call (F-15.1.78).
- `resolveFinalizePlan` composes when a row holds no plan, so a row with an absent plan composes the target's defaults at finalize.
- `resumeOnPod` takes the snapshotless rebuild path when a row has no `WorkspaceSnapshot`, and that path reads the stored plan.
- F-14.1.37 means no `gitClone` works in a deployed image today, so a plan with a clone cannot be replayed until the egress design's pod-side clone lands. A plan with no clone is unaffected.

## RP-1: Replay from the stored plan

### Replay modes

`replayMode` and `workspace_derive` go. `workspace_derive` starts from the source workspace with no prompts, and §15.1 calls it "equivalent to `POST /v1/sessions/{id}/derive` with `targetRuntime` substituted".

| | `workspace_derive` | `POST .../derive` |
| :-- | :-- | :-- |
| Starting workspace | Copy of the snapshot | Copy of the snapshot |
| Prompts | None | None |
| Another runtime | `targetRuntime`, required | `runtimeRef`, optional, in the code alone (F-15.1.76) |
| `executionMode` check | Yes | None today |
| A live source | Refused | With `allowStale: true` |
| `evalRef` | Yes | None |

A caller that needs a fork on another runtime uses derive's `runtimeRef`, which F-15.1.76 brings into the specification with the `executionMode` check. A fork that needs `evalRef` has none, and adding the field to derive later is additive. The platform has no deployments, so removing the field needs no compatibility path.

### Request

```json
{
  "targetRuntime": "<runtime-name>",
  "targetPool": "<pool-name (optional)>",
  "targetIsolationProfile": "<profile (optional)>",
  "evalRef": "<eval-id (optional)>",
  "allowIsolationDowngrade": false,
  "ticketId": "<ticket (with allowIsolationDowngrade)>",
  "allowWrites": false
}
```

`targetRuntime` keeps its rule on `executionMode`, and the isolation monotonicity rule stays as §15.1 states it.

### What the new session starts from

At create, the gateway reads the source's stored plan and writes a copy on the new row, so the plan binds at create. `finalize` with a body answers as it does for any row with a bound plan.

- **Git.** Each `gitClone` keeps its `resolvedCommitSha`, and no ref resolves. The adapter clones that commit.
- **Composition.** Composition does not run. The stored plan is already composed, so the source runtime's default files, clones, and commands are in it, and the target's are not. A clone the target added is absent, and a clone the target dropped is present.
- **Inline files and directories.** These come from the plan as written.
- **`env` and `runtimeOptions`.** These copy from the source's row and pass the checks a create applies, against the target's schema.
- **An empty plan.** A source with no client plan on a runtime with no defaults has none stored. The new row then writes the canonical empty plan, so finalize composes nothing, and an empty start stays empty.
- **Uploads.** `uploadRef` is bound to its session, so at create the gateway copies each upload object to the new session and rewrites the reference in the new row's plan, as derive copies checkpoint chunks. An upload past its TTL refuses the replay. `blobstore.Copy` mints a fresh TTL, so a replay's copy lasts 7 days from that replay, and a chain of replays extends how long an upload survives. Each copy reserves against the tenant's storage quota as an upload does.
- **Setup commands.** These run from the plan at finalize, in plan order.
- **Prompts.** "Delivering prompts" states these.
- **Snapshot.** The row carries no `WorkspaceSnapshot`, so nothing is restored and no chunk is copied. A resume before the first checkpoint takes the snapshotless rebuild path, which reads the plan, so F-7.3.36 no longer applies to a replay.

The precondition changes. A replay needs a terminal source with at least one prompt in its transcript, and it no longer needs a workspace snapshot. A source whose snapshot retention has passed can therefore be replayed while its row and transcript last.

The new row sets `replaySourceSessionId` in place of `ParentWorkspaceRef`. No object is shared, so the field is lineage alone. A replay of a replay reads the plan its source copied, so it starts where the first session started.

### What it refuses

The gateway answers `422 REPLAY_SOURCE_UNAVAILABLE` with `details.reason`, before any claim, in these cases:

| Reason | Cause |
| :-- | :-- |
| `no_prompts` | The transcript holds no client prompt, as for a session driven through the MCP send paths, which F-15.1.77 also records. |
| `delegated_source` | The source has a delegation parent. Its exported files and inherited inputs came from that parent, and a replay is a new root. |
| `upload_expired` | The plan names an upload past its TTL. |
| `derived_source` | The source was derived. It started from a snapshot that its own checkpoints have since replaced, so its start cannot be rebuilt. |
| `source_runtime_stopped` | "Stops" below applies. |
| `target_unsupervised` | "Stops" below applies. |
| `target_cannot_receive` | The target has `injection.supported: false`, or it is `one_shot` and the source has more than one prompt. Either would fail after a claim and setup. |
| `service_mode` | Service-mode rows hold no coordination lease, so nothing would deliver the prompts. |

A commit the host no longer serves is not known before the fetch, because a pinned commit skips `ls-refs`. It fails at materialization with `SESSION_CREATION_FAILED`, after the claim.

### Checks

The stored plan passed its checks when the source bound it, under that runtime, and those checks can have changed. Before the claim, the gateway runs the same checks a client's plan goes through, on the replayed session's user and environment and the target pool. No such single function exists today: `resolveSubmittedPlan` takes raw JSON, `Parse` refuses `resolvedCommitSha`, `validatePlanUploadRefs` needs a session, and `enforceSetupCommandPolicy` and DR-9's default-clone run are separate steps. RP-1 therefore extracts one function over a parsed plan, which every bind path calls, so a check added later needs no second statement. It covers the auth bindings, named bindings, `egress.allowedHosts`, and `gitClone` sources under the environment, the rule for pods with no per-session policy, a `required` pool's check on the user's link, and `verbatimClientFiles`. `tenant-scoping-identity-design.md` lists these at replay.

**Admission.** `handleReplay` and `handleDerive` also run `requireTenantClassification`, `requireConcurrencyLimits`, and `requireAdmissionRateLimit` for the target, which they skip today (F-15.1.78). A replay runs setup and unattended turns on a shared pool, so it must not go around the per-runtime and per-pool limits a create meets. A refused replay releases any upload copy's quota reservation, through the same undo as its other writes.

**Setup commands.** The stored plan does not record which commands came from a runtime default, and §7.5 exempts operator-written defaults from `setupCommandPolicy`. A command is exempt on a replay when its text equals a command in the target runtime's operator-written `workspaceDefaults`. Every other command is checked against the target's effective policy, including a tenant's default from the source runtime, and a miss refuses the replay.

**Warm stores on the target.** The target's pods can hold warm stores for clones the plan does not name, under `tenant-derived-runtimes-design.md`, "Warm clones". The session does not materialize those clones, and its agent can still read the store. The gateway therefore runs the read checks for each warm default clone on the target runtime that the plan omits, and `coveredStores` includes them, as that design's "Resume, retry, and rebuild" row does.

### Delivering prompts

R6 needs a delivery, because nothing delivers prompts today. `deliverBuffered` stays as it is, because its in-line callers cannot wait for a whole run. A single-flight goroutine per session, on the server's lifetime context as `ResumeBacklog` is, delivers the next prompt once the held messages are gone. It uses the same `Send` and `recordDeliveredResponse` as a client's message, and it takes the delivery lock per prompt.

One trigger starts it: `Sweeper`'s pass over the rows this replica coordinates (`coordination/coordination.go`) starts a goroutine for a `running` row with an active run and none running locally, which also covers a takeover and a process restart. `Sweep` lists from the read pool, so it passes session IDs, and the goroutine reads the row from the primary, as `DeliverHeldAfterTakeover` does. A deployment with no Redis has no `Sweeper` today, so RP-1 builds the pass there too. `POST .../start` starts the goroutine directly as well, so the first prompt does not wait for a sweep.

The row holds one value, `replayRun`: `idle` between prompts, `in_flight` during a send, and `ended` after a run stops or completes, and every change to it is a compare-and-set of the coordination generation, `replayRun`, and `replayCursor` in `store.Update`, so a stale coordinator or a stale read can neither send nor end a healthy run, and a prompt cannot follow an interrupt.

- **Source.** The goroutine reads the next client prompt from the source session's transcript at each send, by `replaySourceSessionId` and `replayCursor`. Nothing copies the prompts, so the transcript's retention, erasure under §12.8, classification under §12.9, and legal holds apply with no new rule. When the source's rows are gone, the run stops with `source_gone`. `transcriptstore.Entry` gains a sender, empty for a client, so a sibling agent's held message is not sent as the owner's prompt, and `deriveMessageFrom` reads it. `recordDeliveredResponse` appends inbound rows before `Send`, so the prompt of a failed or rejected turn is on the record for a later replay. The new session's own transcript starts empty and records its turns.
- **When it sends.** It sends only while `replayRun` is `idle`, the row is `running`, and `RecoveryGeneration` is 0, and it writes `ended` when that last value is not 0. `commitResume` increments it on every resume, so this covers a pod replacement after a replica crash, when no goroutine was there to see `resume_pending`. A rebuild resets the workspace to the plan or an earlier checkpoint, and later prompts would run without the results of earlier ones, so the run could otherwise report `replay_completed` on an invalid result. A prompt that opens an elicitation or approval keeps its turn in flight, as a client's does.
- **In flight.** The goroutine writes `in_flight` before each send, and it writes `idle` and advances `replayCursor` on the response. §10.1 can hand a session to another coordinator with the pod and its turn still live, and the new one cannot see that turn end. A sweep that finds `in_flight` with no local goroutine stops the run with `turn_outcome_unknown`, so a prompt is never sent twice. A rolling update of the gateway can therefore stop a replay.
- **Stopping.** A delivered response advances the run. Every other exit writes `ended` and emits `replay_stopped` with the prompt's index and the reason: `dropped`, `INJECTION_REJECTED`, a `429`, a `PostAgentOutput` rejection, a read or send error, a terminal session, a `resume_pending`, and an interrupt. After the last prompt the gateway writes `ended` and emits `replay_completed`, and the session then behaves as any session waiting for its client.
- **Interrupts.** An interrupt ends the run with `replay_stopped` and cancels that turn's wait, because a turn in flight holds the delivery lock until its response or stream end, and a resume could not otherwise get through. The session can then take a client's messages, and no goroutine starts again on an `ended` run.
- **Client traffic.** A client message answers `409 REPLAY_IN_PROGRESS` on any replica while `replayRun` is `idle` or `in_flight`, and an interrupt, an elicitation answer, and a `POST /messages` carrying `inReplyTo` that answers `lenny/request_input` pass. A turn waiting on an elicitation or an approval stays open under `maxElicitationWait`, and `maxClientIdleSeconds` and `maxSessionAge` bound the whole run, so a run cannot hold a pod indefinitely.

The transcript holds each client message's text, so attachments are not replayed.

### Writes

A replay re-executes the source's tool calls, so a plan that could push would push again. By default the new row's plan has no `writableRepositories`, each `gitClone` write scope becomes the matching read scope, and `writeDefaultClones` is absent. With no writable entry, no `prAuthoring` binding attaches. A push answers 403 from `netagent`, and the agent sees that failure, which is a difference from the source run.

`allowWrites: true` keeps the plan's writes, under the usual checks at bind and under DR-9's read-only rule. A writable entry that matches a `runtimeDefault` source therefore reads as read-only where that rule says so, for example when its pool has since turned on `prAuthoring: user`, because `allowWrites` names no repository and a replay composes nothing. Git is the only surface this bounds. A tool that calls another system repeats that call, as §15.1 states.

### Stops

A stored plan can hold content a tenant has since stopped. `tenant-derived-runtimes-design.md` treats a default as code, and a grant flag set false or a forbid is "the stop after a compromise". Its residual "A stored plan outlives a stop" covers a session that is still running. A replay would be a new session, so it is refused. Reading the source's runtime alone would not hold: a replay onto a platform runtime before a forbid leaves a row whose runtime is that platform runtime, and replaying that row would run the same content. A plan therefore carries `planOriginRuntimes`, the tenant-derived runtimes whose defaults it holds. A replay or a derive copies its source's set at create, because a cancelled derive with no bind is still a valid source, and a copied entry is never overwritten. A bind adds its runtime when the plan holds that runtime's defaults, so a derive adds its target and a replay, which composes nothing, adds none. A delegated child copies its parent's at bind, because a child's exports came from that parent.

A runtime's current state would not hold either. If a `security-admin` sets a forbid or an operator sets `allowGitClones` false after the tenant has removed the clone, or before a worker could see it, the runtime looks clean, and a replay of a session from before the incident would rerun the pinned commit. Two counters therefore record stops, in a tenant-keyed `content_stop_epochs` table, outside the grant's and the security record's bodies, because a `PUT` replaces each of those documents and SO-2 stores the record as versions, so a pending weakening written before a forbid would otherwise regress a counter kept inside it. An absent row reads as 0. Each is incremented in the stop's own transaction: the grant entry's, keyed by base, when `allowWorkspaceFiles`, `allowSetupCommands`, or `allowGitClones` is set false, and the security record's, in the transaction that activates a version adding `all`, `setupCommands`, `workspaceFiles`, `gitClones`, or `floatingCloneRefs`, or setting `verbatimClientFiles: forbid`. Each `planOriginRuntimes` entry stores both values, from the same read of the tenant view that composition used, so a later read cannot stamp content from before a stop as current.

The check is one function, which `handleReplay` and `handleDerive` call at create, and which finalize calls again, because a row created before a stop can finalize after it. For each entry, the tenant view must resolve that runtime now, it must not be retired, and both stored values must equal the current ones. A deleted runtime refuses, and names stay consumed, so one cannot be recreated to pass. A runtime that is `incompatible` for another reason, such as `Registered=False`, does not refuse a replay. A plan from before a stop stays refused after the stop is lifted, because a counter only rises.

**Supervisors.** A target with no runtime supervisor runs setup at the adapter's identity, so a pusher's commit or a tenant's default, replayed onto it, would run with the service account token. `tenant-derived-runtimes-design.md` refuses tenant defaults on such a base for that reason. A replay or a derive with a non-empty `planOriginRuntimes` is refused on such a target, and so is a delegated child that receives exports from a parent with one. `runsSupervisor` is an unexported controller function over the CRD, so the gateway uses a new helper over the runtime row, using the `type` and `deploymentModel` that DR-6 mirrors. It requires `registered` to be true and fails closed, because the mirror stores an empty value as `sidecar` and leaves the row stale when it refuses a resource.

A platform-owned source runtime has no such rule, because replaying a retired version against its successor is what the route is for. An emergency retirement of a platform runtime concerns its image, and a replay does not run that image.

## What an exact start does not cover

"Exactly where the source started" holds for what the plan states. These differ, and the documentation says so:

- **The image.** The target runtime's image is the variable under test.
- **What setup commands fetch.** `npm install` resolves against the registry on the day it runs. Commit pins do not reach it.
- **External answers.** Tool calls and model responses are not recorded inputs.
- **Egress and credentials.** These are the tenant's and the pool's settings on the day of the replay.
- **Messages the transcript lacks.** A lost append, attachments, and messages from other agents.
- **A clone the target needs.** A target runtime that relies on a clone it added runs without it, and that shows up as a failed run.

## Costs

- Every replay materializes and runs setup again, where today it restores a snapshot.
- Uploads gain a second copy per replay, and those count toward the tenant's storage.
- A source older than the upload TTL that used uploads cannot be replayed.
- Replaying a derived session or a delegated child is unavailable.
- A gateway rolling update or a pod loss stops a replay in progress, and the client runs it again.
- A content stop refuses every earlier plan under that grant entry or tenant, on any runtime and whether or not that runtime held the stopped content, and it stays refused after the stop is lifted.

## Defects found while verifying

| Finding | Severity | Defect |
| :-- | :-- | :-- |
| F-15.1.76 | Medium | `DeriveRequest.RuntimeRef` (`sessionserver/derive.go`) overrides a derived session's runtime. §15.1 and §7.1 do not state the field, and `handleDerive` does not run the `executionMode` check that `handleReplay` runs. |
| F-15.1.77 | High | Replay delivers no prompts. `handleReplay` copies the transcript, of every role, and no start path reads it, against §15.1's "replayed verbatim as the initial message sequence". The MCP send paths also write no transcript row, so a session driven through them has no prompts to replay. |
| F-15.1.78 | Medium | `handleReplay` and `handleDerive` skip `requireTenantClassification`, `requireConcurrencyLimits`, and `requireAdmissionRateLimit`, so only the tenant cap holds, and repeated replays or derives go around per-runtime and per-pool limits. |

## Specification sections affected

- §15.1: the replay row in the endpoint table, which says "the same workspace", and **Session Replay Semantics**. `replayMode` goes, and the request, precondition, starting state, prompt delivery, refusals, and writes are as above. The error table gains `REPLAY_SOURCE_UNAVAILABLE` and `REPLAY_IN_PROGRESS`, and the event list gains `replay_stopped` and `replay_completed`.
- §7.1 **Derive restore semantics:**: a replayed session no longer carries a snapshot, so the paragraph names derive alone, and "the only kind of session whose record carries a workspace snapshot before its first bind" stays true of it.
- §4.5 **Workspace lineage:**: `replaySourceSessionId`.
- §14.1 **Plan binding.**: a replay binds at create from the stored plan, and step 1 of **Runtime workspace defaults.** does not run for it.
- §10.2 **Session ownership.**, §11, and §13: these name replay alongside derive for ownership, the `derive.isolation_downgrade` event, and first-bind `GET` capabilities. The first two stay, and §13.1's first-bind wording names derive alone.
- §18 and §23: the mentions of `replayMode`.
- §7.2 **Message delivery routing**: replay delivery after the held messages, and the sweep that starts it.
- §12.4 and §15.4: the sender on a `session_messages` row.

## Stored formats and wire contracts this forces

| Carrier | Change |
| :-- | :-- |
| `POST /v1/sessions/{id}/replay` | Removes `replayMode`, and adds `allowWrites`. The response drops `replayMode`. |
| Session row | `replaySourceSessionId`, `replayCursor`, `replayRun`, and `planOriginRuntimes` with each entry's two stored values, and a replay row has a bound plan, `env`, and `runtimeOptions`, and no `WorkspaceSnapshot` |
| `session_messages` | A sender on each row, empty for a client |
| `content_stop_epochs` | One row per tenant for its security record and one per grant entry's base, outside those documents |
| Upload store | A copy of an upload under the new session, with a new `uploadRef` |

## Proposals and dependencies

| Proposal | Contents | Depends on |
| :-- | :-- | :-- |
| RP-1 | Everything above, and closing F-15.1.76, F-15.1.77, and F-15.1.78 | None for a plan with no clone. A plan with a clone needs the egress design's pod-side clone, as any `gitClone` does. "Warm stores on the target" and `coveredStores` arrive with DR-11, and "Stops" with DR-4, DR-6, and SO-2. RP-1 also extracts the plan check function that DR-9 calls. |

It can land before those designs. Each adds its rule to the replay checks when it lands.

## Documentation

| Page | Change |
| :-- | :-- |
| `docs/tutorials/session-derive-replay.md`, `docs/tutorials/evaluation-scoring.md`, and `docs/client-guide/session-lifecycle.md` | States the two routes separately, and "What an exact start does not cover". |
| `docs/api/rest.md`, `docs/client-guide/sdk-examples/curl.md`, and `docs/reference/error-catalog.md` | The request, `allowWrites`, and `REPLAY_SOURCE_UNAVAILABLE`. |
| `docs/about/comparisons.md` | Removes the mode. |

## Tests

### Tier 1: unit

- The plan copy keeps each `resolvedCommitSha`, composes nothing from the target, writes an empty plan for a source with none, and strips write scopes unless `allowWrites` is set.
- `planOriginRuntimes` accumulates through a chain of replays and derives.
- A setup command matching the target's operator-written default is exempt, and any other is checked.
- Each refusal reason.

### Tier 3: contract

- A request with `replayMode` is refused, and the response carries no such field.

### Tier 4: integration

- A session creates a file under a prompt, and its replay starts with no such file, at the source's commit, with the target's added clone absent and its dropped clone present, then receives that prompt and creates the file again.
- Prompts arrive in order with the next sent when the turn ends, assistant rows and a sibling agent's held message are not sent, and the new transcript holds the new responses alone. A sweep after a takeover or a restart continues a run in `idle` from `replayCursor`, and it stops one in `in_flight` with `turn_outcome_unknown` and sends nothing twice, including during an approval wait. A `resume_pending` ends the run, and no prompt reaches the DLQ. An interrupt ends the run, and the client's next message starts no goroutine. A client message answers `REPLAY_IN_PROGRESS` on any replica during a run and never after one ends, each exit emits `replay_stopped`, and two triggers start one goroutine. A stale coordinator can neither send nor end a run. A replay meets the per-runtime concurrency and admission limits, and a refused one releases its upload reservation. A failed turn's prompt stays in the transcript. A lagging read cannot start a goroutine after an interrupt. After a replica crash and a pod replacement, the run ends rather than sending on a rebuilt tree. A sweep during `starting` starts nothing. An answer to `lenny/request_input` passes during a run. `allowWrites` does not attach a user binding for an `always` clone whose pool later turned on `prAuthoring: user`. A source purged mid-run stops with `source_gone`. `POST .../start` returns before the first turn ends.
- A target with `injection.supported: false`, a `one_shot` target with several prompts, and a service-mode source refuse before any claim.
- `env` and `runtimeOptions` copy, and an option the target's schema refuses refuses the replay.
- A replay binds no second plan at finalize.
- An upload copies to the replayed session, the original's later expiry does not affect it, and an expired upload refuses.
- A source whose snapshot has been collected replays. A replay resumed before its first checkpoint rebuilds from the plan.
- A replay of a replay starts where the first session started. A derived source and a delegated child refuse.

### Tier 9: security

- A repository off the allowlist, a user who lost access on a `required` pool, and an environment that denies the host each refuse before any claim.
- With no `allowWrites`, `git push` answers 403 and no write token is minted, and no `prAuthoring` binding attaches.
- A content forbid, a retirement, a set-false grant flag, and a deletion of a tenant-derived source runtime each refuse, including a replay of an earlier replay onto a platform runtime, including a stop that arrives after the tenant removed the clone, and including after the stop is lifted. A plan bound after the stop replays. A runtime with `Registered=False`, a non-content forbid, and a tenant suspension raise nothing. A row created before a stop refuses at finalize. A pending weakening activated after a forbid does not lower the counter. A derive cancelled before its bind, taken as a source after a stop, refuses. A retired platform source runtime replays. A plan with a tenant origin onto a target with no supervisor refuses, on replay, on derive, and for a delegated child receiving exports, and a target with `registered` false refuses. A derive of a delegated child carries its parent's origins.
- A replay keeps the source's `egressLockHosts` and environment, and a caller who left that environment answers 403.
- A target pod holding a warm store for a clone the plan omits is refused for a user who cannot read it.

### Tier 11: documentation

- Documented examples of the request pass the schema.

## Decisions

These are open for the owner, and the recommendation is the design above. The owner settled the exact start and this document. The removal of `workspace_derive` follows the owner's stated inclination, and the owner has not confirmed it.

| # | Decision | Recommendation | Cost of the recommendation | Alternative |
| :-- | :-- | :-- | :-- | :-- |
| 1 | Removing `workspace_derive` | Remove it, and use derive's `runtimeRef` for a fork on another runtime | A fork has no `evalRef` until derive gains it | Keep the mode, which leaves two routes to one behaviour |
| 2 | Uploads past their TTL | Refuse the replay | A source that used uploads cannot be replayed after 7 days | Retain a session's uploads as long as its transcript, which costs storage for every session. Or snapshot every session at first bind, which also serves uploads and derived sources, and costs storage and time on each session |
| 3 | Writes on a replay | Off, with `allowWrites` to keep them | A run that pushes fails where the source succeeded, so it differs from the source | On by default, which matches the source and repeats its pushes on every regression run |
| 4 | Derived sources | Refused | A forked session cannot be regression tested | Replay from the derived session's starting snapshot, which needs that snapshot retained past its first checkpoint |
| 5 | A stopped tenant-derived source runtime | A content stop refuses every earlier plan under that grant entry or tenant, permanently, and a deleted runtime refuses | One stop ends replay and derive for all earlier sessions with tenant content under that entry or tenant, including runtimes that held nothing bad | Track stops per runtime, which is narrower and misses content a tenant already removed. Or refuse only while a stop is in force, which reruns a bad commit once it is remediated |
| 7 | Who delivers the prompts | The gateway, in a goroutine a sweep starts | A run ends on a coordinator handoff with a prompt in flight, on a pod loss, and on an interrupt, and the client runs it again | The client sends them, which needs no gateway change, leaves §15.1's statement unmet, and has every caller write a harness |
| 6 | Exempting setup commands | By text match against the target's operator-written defaults | A changed base command in a new version falls under the policy, and a replay can be refused for it | Record each command's origin on the stored plan, which is exact and adds a stored field, or exempt nothing, which refuses most replays on a base with an allowlist |

## Review of revision 1

An independent review read revision 1. It found no Critical issue and no path across tenants. Each finding was checked against the tree, and revision 2 applies them. `tenant-derived-runtimes-design.md` records the findings on DR-9.

| Finding | Result | Check and outcome |
| :-- | :-- | :-- |
| High, replay delivers no prompts | Holds. Exists today. | `s.transcripts` has no reader on a start path, and `CopySessionTx` copies every role. Filed as F-15.1.77, and "Delivering prompts" adds a driver. The reviewer also offered having the client send them, which decision 7 records. |
| High, "Stops" can be bypassed through a replay of a replay | Holds. | The check read the source's runtime alone. `planOriginRuntimes` travels with the plan. |
| Major, `no_stored_plan` refuses ordinary sessions | Holds. | The reason is removed, and an empty plan is written, because an absent one would compose the target's defaults at finalize, against R2. |
| Major, delegated child sources | Holds. | `tenant-scoping-identity-design.md` has a replay take the source's environment and lock, against RPP1's text. RPP1 is corrected, and a delegated source refuses. |
| Major, environment settings are not in the plan | Holds. | `workspaceplan.Plan` has no such field. `env` and `runtimeOptions` copy from the row under create validation. |
| Minors: the width of the read checks, omitted checks, and upload copies | Hold. | Applied. The checks reuse a client plan's check function. |

## Review of revision 2

A stop check read revision 2 and `tenant-derived-runtimes-design.md` revision 20. It found no Critical issue and no path across tenants. Each finding was checked against the tree, and revision 3 applies them.

| Finding | Result | Check and outcome |
| :-- | :-- | :-- |
| High, the stop check passes after remediation | Holds. | DR-6's row reads `incompatible` "until the tenant removes it". `stoppedAt` is set once and compared with each plan's bind time. |
| Major, a pusher reaches the adapter's identity through an unsupervised target | Holds. | `runsSupervisor` is false on an embedded base, and DR-3 refuses tenant defaults there. Such a target is refused for a plan with a tenant origin. |
| Major, a return is not a turn end, and an interrupt does not stop the run | Holds. Fix wider. | `deliverBuffered` checks the lease and stops on a terminal row, and it runs at start, resume, and takeover. The separate driver goes, which removes both findings. |
| Major, duplicate prompts on a handoff | Holds. | `replayInFlight` stops the run, so a prompt is never sent twice. |
| Major, `user` rows are not all client prompts | Holds. | `Entry` has no sender. It gains one. |
| Major, `replay_prompts` has no compliance coverage | Holds. Fix wider. | The table goes, and each send reads the source's transcript, so existing rules apply. |
| Major, the same check function does not exist | Holds. | `resolveSubmittedPlan` takes raw JSON, and `Parse` refuses `resolvedCommitSha`. RP-1 extracts the function. |
| Major, `snapshotIsOwn` has two meanings | Holds. | Recorded in `tenant-derived-runtimes-design.md`. |
| Minors: targets that cannot receive, child origins, the client's calls, service mode, and attachments | Hold. | Applied. `projectMessage` sends text alone, so the open statement on attachments becomes a stated limit. |

## Review of revision 3

A stop check read revision 3 and `tenant-derived-runtimes-design.md` revision 21. It found no Critical issue and no path across tenants. Each finding was checked against the tree, and revision 4 applies them.

| Finding | Result | Check and outcome |
| :-- | :-- | :-- |
| High, a stop misses a runtime already remediated | Holds. Introduced by revision 3's `stoppedAt`. | DR-6 and DR-7 act on a runtime that still holds the default. `contentStopEpoch` is written in the stop's own transaction, which removes the worker stamp, the bind-time comparison, and refusals on a transient `incompatible`. |
| Major, an abandoned derive launders the origin set | Holds. | `planOriginRuntimes` is now copied at create, because a cancelled derive is a valid source. |
| Major, the prompt source breaks in-line callers | Holds. | `resume.go` and `sendNow` call the loop in line. `deliverBuffered` stays unchanged, and a single-flight goroutine runs the prompts, with every exit stopping the run. |
| Major, an interrupt during a prompt cannot resume | Holds. | A turn holds the delivery lock until its response. An interrupt now ends the run. |
| Minors: the supervisor check, child exports, the transcript's order, and the sender's readers | Hold. | Applied. The helper fails closed, a child receiving exports is checked, and inbound rows append before `Send`. |

## Review of revision 4

A stop check read revision 4 and `tenant-derived-runtimes-design.md` revision 22. It found no Critical or High issue, five Majors, and no path across tenants. Each was checked against the tree, and revision 5 applies them.

| Finding | Result | Check and outcome |
| :-- | :-- | :-- |
| Major, no durable run-ended state | Holds. | An ended run would restart at the client's next message, and another replica could not answer 409. `replayRun` replaces `replayInFlight`. |
| Major, a client can stay in `REPLAY_IN_PROGRESS` | Holds. | `DeliverHeldAfterTakeover` returns unless the row is `running`. A sweep replaces the list of delivery points. |
| Major, continuing after a pod replacement invalidates the run | Holds. | A rebuild resets the tree, so any `resume_pending` ends the run, which removes "holds its place". |
| Major, the security counter has no stable home | Holds. Fix wider. | SO-2 stores versions, and a `PUT` replaces the body. Both counters move to `content_stop_epochs`. |
| Major, replay skips the create gates | Holds. Exists today. | Filed as F-15.1.78, for derive as well. |
| Minors: the read that stamps the counters, copied entries, a row finalized after a stop, the suspension test, upload reservations, and the proposal table | Hold. | Applied. |

## Review of revision 5

A stop check read revision 5 and `tenant-derived-runtimes-design.md` revision 23. It found no Critical or High issue, four Majors, and no path across tenants. Each was checked against the tree, and revision 6 applies them.

| Finding | Result | Check and outcome |
| :-- | :-- | :-- |
| Major, stale sweep rows restart a run | Holds. | `Sweep` lists from the read pool. The goroutine reads the primary, and changes are a compare-and-set in `store.Update`. |
| Major, `idle` survives a pod loss | Holds. | After a replica crash, no goroutine sees `resume_pending`. `commitResume` increments `RecoveryGeneration`, so the send condition reads it. |
| Major, `allowWrites` skips DR-9's read-only rule | Holds. | A replay composes nothing, so the rule now runs on the stored entries. |
| Major, a `runtimeRef` onto an unsupervised target reaches the adapter's identity | Holds. Exists today. | Recorded in `tenant-derived-runtimes-design.md`, and F-15.1.76 now names it. |
| Minors: a sweep during `starting`, answers to `lenny/request_input`, stale text, and no `Sweeper` without Redis | Hold. | Applied. |
