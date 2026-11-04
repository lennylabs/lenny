# Session replay

Status: design for owner review, revision 2, 2026-11-04. Revision 2 applies the review of revision 1, under "Review of revision 1", and no review has read it. Nothing here is built or staged, and no test or spike was run for it. Statements about the tree were checked by reading the code and the specification. It reads against `tenant-derived-runtimes-design.md` revision 20, `tenant-scoping-identity-design.md` revision 21, and `egress-and-git-design.md` revision 59. Proposals carry the label RP-n.

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
- R6. The platform delivers the source's prompts to the replayed session, in order, with no client driving them.

## Security properties

- RPP1. The replayed session takes the source session's owner, as §10.2 **Session ownership.** states, and credentials are evaluated afresh. From the source it takes the plan, its prompts, its `env` and `runtimeOptions`, and what `tenant-scoping-identity-design.md` states a replay takes: its environment, `egressLockHosts`, `crossed_from_environment`, and its delegation limit.
- RPP2. Every check runs against the plan on the replayed session's user, environment, and target pool, as for a plan a client submits.
- RPP3. A plan carrying content from a stopped tenant-derived runtime is refused, on any route and through any chain of replays or derives, so neither is a route around a forbid, a suspension, or a retirement.

## Current state

`handleReplay` (`pkg/gateway/sessionserver/replay.go`) requires a terminal source with a resolvable workspace snapshot, checks `executionMode` through `requireReplayRuntimeCompatible`, copies the snapshot with `copyWorkspaceSnapshot`, and sets `ParentWorkspaceRef`. Under `prompt_history` it copies the transcript in the insert transaction through `CreateWithTranscript`, using `CopySessionTx`, which copies rows of every role, including assistant output. In `sessionserver`, the transcript store is read by the GET routes, `taskrecord.go`, and `usage.go`, and no start path reads it. The new row holds no plan, and one binds at finalize, which composes the target runtime's current defaults. §7.1 **Derive restore semantics:** then restores the snapshot at first bind and applies the plan over it.

Other facts this design relies on:

- `GET /v1/sessions/{id}` shows that a session row stores its composed plan, with `resolvedCommitSha` on each `gitClone` source. §14.1 **Plan binding.** states that a session binds at most one plan.
- §14 states that an `uploadRef` belongs to the session for which its content was stored and is valid only in a plan bound to that session. `UploadDefaultTTL` in `sessionserver/upload.go` stamps a 7-day TTL on uploads.
- `workspaceplan.Plan` holds `SchemaVersion`, `Sources`, and `SetupCommands`. `Env` and `RuntimeOptions` are fields on the session row, and `handleReplay` copies neither today.
- A message delivery is a request and a response. `recordDeliveredResponse` runs when the runtime's turn returns, and it appends the inbound messages and the text parts of the response to the transcript. That append is best-effort, and `mcptools/send_delivery.go` and the child send path make none.
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
| `no_prompts` | The transcript holds no `user` row, as for a session driven through the MCP send paths, which F-15.1.77 also records. |
| `delegated_source` | The source has a delegation parent. Its exported files and inherited inputs came from that parent, and a replay is a new root. |
| `upload_expired` | The plan names an upload past its TTL. |
| `derived_source` | The source was derived. It started from a snapshot that its own checkpoints have since replaced, so its start cannot be rebuilt. |
| `source_runtime_stopped` | "Stops" below applies. |

A commit the host no longer serves is not known before the fetch, because a pinned commit skips `ls-refs`. It fails at materialization with `SESSION_CREATION_FAILED`, after the claim.

### Checks

The stored plan passed its checks when the source bound it, under that runtime, and those checks can have changed. Before the claim, the gateway runs the same check function a client's plan goes through, on the replayed session's user and environment and the target pool, so a check added later needs no second statement here. That covers the auth bindings, named bindings, `egress.allowedHosts`, and `gitClone` sources under the environment, the rule for pods with no per-session policy, a `required` pool's check on the user's link, and `verbatimClientFiles`. `tenant-scoping-identity-design.md` lists these at replay.

**Setup commands.** The stored plan does not record which commands came from a runtime default, and §7.5 exempts operator-written defaults from `setupCommandPolicy`. A command is exempt on a replay when its text equals a command in the target runtime's operator-written `workspaceDefaults`. Every other command is checked against the target's effective policy, including a tenant's default from the source runtime, and a miss refuses the replay.

**Warm stores on the target.** The target's pods can hold warm stores for clones the plan does not name, under `tenant-derived-runtimes-design.md`, "Warm clones". The session does not materialize those clones, and its agent can still read the store. The gateway therefore runs the read checks for each warm default clone on the target runtime that the plan omits, and `coveredStores` includes them, as that design's "Resume, retry, and rebuild" row does.

### Delivering prompts

R6 needs a driver, because nothing delivers prompts today. At create, the insert transaction copies the source's `user` rows, and no other role, into `replay_prompts` for the new row. The new session's own transcript starts empty and records the replay's turns as they happen, so it holds the new runtime's responses and none of the source's.

Once the session is `running`, the gateway delivers each prompt in order through the path `POST /v1/sessions/{id}/messages` uses, and it sends the next when the previous delivery returns, which is the end of that turn. `replayCursor` on the row records progress, so the replica that next holds the session's coordination lease continues after a crash. A prompt whose delivery result was lost can be sent twice, as a client's retry can.

- A turn that waits on an elicitation or an approval stays open until the client answers it or its timeout passes.
- While the driver runs, a client message answers `409 REPLAY_IN_PROGRESS`, and an interrupt and an elicitation answer pass. A client can therefore stop a run, and it cannot add turns that the source did not have.
- A failed turn or a terminal session stops the driver and emits `replay_stopped` with the prompt's index. After the last prompt the gateway emits `replay_completed`, and the session then behaves as any session waiting for its client.

The transcript's content is what a replay can send. It holds each message's text, and its append is best-effort, so a lost append loses that prompt. Whether a message with attachments keeps them there was not established.

### Writes

A replay re-executes the source's tool calls, so a plan that could push would push again. By default the new row's plan has no `writableRepositories`, each `gitClone` write scope becomes the matching read scope, and `writeDefaultClones` is absent. With no writable entry, no `prAuthoring` binding attaches. A push answers 403 from `netagent`, and the agent sees that failure, which is a difference from the source run.

`allowWrites: true` keeps the plan's writes, under the usual checks at bind. Git is the only surface this bounds. A tool that calls another system repeats that call, as §15.1 states.

### Stops

A stored plan can hold content a tenant has since stopped. `tenant-derived-runtimes-design.md` treats a default as code, and a grant flag set false or a forbid is "the stop after a compromise". Its residual "A stored plan outlives a stop" covers a session that is still running. A replay would be a new session, so it is refused. Reading the source's runtime alone would not hold: a replay onto a platform runtime before a forbid leaves a row whose runtime is that platform runtime, and replaying that row would run the same content. Each bind therefore stamps `planOriginRuntimes`, the tenant-derived runtimes whose defaults the plan holds, and a replay or a derive copies its source's set and adds its own. The check is one function: for each name, the tenant view must resolve that runtime now, and it must not be `incompatible`, `retired`, or suspended, and a deleted one refuses too. `handleReplay` and `handleDerive` both call it.

A platform-owned source runtime has no such rule, because replaying a retired version against its successor is what the route is for. An emergency retirement of a platform runtime concerns its image, and a replay does not run that image.

## What an exact start does not cover

"Exactly where the source started" holds for what the plan states. These differ, and the documentation says so:

- **The image.** The target runtime's image is the variable under test.
- **What setup commands fetch.** `npm install` resolves against the registry on the day it runs. Commit pins do not reach it.
- **External answers.** Tool calls and model responses are not recorded inputs.
- **Egress and credentials.** These are the tenant's and the pool's settings on the day of the replay.
- **Messages the transcript lacks.** A lost append, and any part of a message the transcript does not hold.
- **A clone the target needs.** A target runtime that relies on a clone it added runs without it, and that shows up as a failed run.

## Costs

- Every replay materializes and runs setup again, where today it restores a snapshot.
- Uploads gain a second copy per replay, and those count toward the tenant's storage.
- A source older than the upload TTL that used uploads cannot be replayed.
- Replaying a derived session or a delegated child is unavailable.
- The gateway gains a driver, with its own state on the row.

## Defects found while verifying

| Finding | Severity | Defect |
| :-- | :-- | :-- |
| F-15.1.76 | Medium | `DeriveRequest.RuntimeRef` (`sessionserver/derive.go`) overrides a derived session's runtime. §15.1 and §7.1 do not state the field, and `handleDerive` does not run the `executionMode` check that `handleReplay` runs. |
| F-15.1.77 | High | Replay delivers no prompts. `handleReplay` copies the transcript, of every role, and no start path reads it, against §15.1's "replayed verbatim as the initial message sequence". The MCP send paths also write no transcript row, so a session driven through them has no prompts to replay. |

## Specification sections affected

- §15.1: the replay row in the endpoint table, which says "the same workspace", and **Session Replay Semantics**. `replayMode` goes, and the request, precondition, starting state, prompt delivery, refusals, and writes are as above. The error table gains `REPLAY_SOURCE_UNAVAILABLE` and `REPLAY_IN_PROGRESS`, and the event list gains `replay_stopped` and `replay_completed`.
- §7.1 **Derive restore semantics:**: a replayed session no longer carries a snapshot, so the paragraph names derive alone, and "the only kind of session whose record carries a workspace snapshot before its first bind" stays true of it.
- §4.5 **Workspace lineage:**: `replaySourceSessionId`.
- §14.1 **Plan binding.**: a replay binds at create from the stored plan, and step 1 of **Runtime workspace defaults.** does not run for it.
- §10.2 **Session ownership.**, §11, and §13: these name replay alongside derive for ownership, the `derive.isolation_downgrade` event, and first-bind `GET` capabilities. The first two stay, and §13.1's first-bind wording names derive alone.
- §18 and §23: the mentions of `replayMode`.

## Stored formats and wire contracts this forces

| Carrier | Change |
| :-- | :-- |
| `POST /v1/sessions/{id}/replay` | Removes `replayMode`, and adds `allowWrites`. The response drops `replayMode`. |
| Session row | `replaySourceSessionId`, `replayCursor`, and `planOriginRuntimes`, and a replay row has a bound plan, `env`, and `runtimeOptions`, and no `WorkspaceSnapshot` |
| `replay_prompts` | The source's `user` rows, written in the insert transaction |
| Upload store | A copy of an upload under the new session, with a new `uploadRef` |

## Proposals and dependencies

| Proposal | Contents | Depends on |
| :-- | :-- | :-- |
| RP-1 | Everything above, and closing F-15.1.76 and F-15.1.77 | None for a plan with no clone. A plan with a clone needs the egress design's pod-side clone, as any `gitClone` does. "Warm stores on the target" and `coveredStores` arrive with DR-11, and "Stops" with DR-4 and DR-6. |

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
- Prompts arrive in order with the next sent when the turn ends, assistant rows are not sent, and the new transcript holds the new responses alone. A replica lost mid-run continues from `replayCursor`. A client message answers `REPLAY_IN_PROGRESS`, an interrupt stops the run, and a failed turn emits `replay_stopped`.
- `env` and `runtimeOptions` copy, and an option the target's schema refuses refuses the replay.
- A replay binds no second plan at finalize.
- An upload copies to the replayed session, the original's later expiry does not affect it, and an expired upload refuses.
- A source whose snapshot has been collected replays. A replay resumed before its first checkpoint rebuilds from the plan.
- A replay of a replay starts where the first session started. A derived source and a delegated child refuse.

### Tier 9: security

- A repository off the allowlist, a user who lost access on a `required` pool, and an environment that denies the host each refuse before any claim.
- With no `allowWrites`, `git push` answers 403 and no write token is minted, and no `prAuthoring` binding attaches.
- A forbid, a suspension, a retirement, a set-false grant flag, and a deletion of a tenant-derived source runtime each refuse, including a replay of an earlier replay onto a platform runtime. A retired platform source runtime replays.
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
| 5 | A stopped tenant-derived source runtime | Refused, including a deleted one | A tenant that deletes a runtime loses replay of its sessions | Refuse only an explicit stop, which lets a deleted runtime's stored defaults run again |
| 7 | Who delivers the prompts | A driver in the gateway, sending each `user` row when the previous turn ends | New gateway state, and a run that only an interrupt can change | The client sends them, which needs no driver and leaves §15.1's statement unmet, and every caller writes a harness |
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
