# Session derive

Status: design for owner review, revision 5, 2026-11-04. Revisions 2 to 5 apply the reviews of revisions 1 to 4, under their own sections, and no review has read revision 5. This document replaces `session-replay-design.md`, whose last revision is at commit `3517d6fe7` with its review history. Nothing here is built or staged, and no test or spike was run for it. Statements about the tree were checked by reading the code and the specification. It reads against `tenant-derived-runtimes-design.md` revision 30, `tenant-scoping-identity-design.md` revision 22, and `egress-and-git-design.md` revision 59. Proposals carry the label SD-n.

## Problem

§15.1 defines two routes that start a session from an earlier one. `POST /v1/sessions/{id}/derive` copies a snapshot, and `POST /v1/sessions/{id}/replay` copies the same snapshot and also claims to replay the prompts.

- **Replay starts from the end.** §15.1 populates the workspace from "the source session's sealed final workspace snapshot". A prompt that says "create `feature.py`" would run where `feature.py` exists.
- **Replay delivers no prompts.** §15.1 says the history "is replayed verbatim as the initial message sequence", and the code copies the transcript and never reads it back on a start path (F-15.1.77).
- **`replayMode: workspace_derive` duplicates derive.** Derive already takes an optional `runtimeRef` in the code, which the specification does not state (F-15.1.76).
- **Neither route has a defined way to start from the beginning**, which is what a regression test needs.

## Owner's choice

On 2026-11-04 the owner wrote: "I would change derive + replay so that everything is a derive with two options: derive at start of session or derive at now/the latest checkpoint. A new API can return the user messages from the source session and the client can choose to replay them (how it's up to the client)."

The owner also settled these: "ideally we pick up the exact state in which the source session was"; a start from the beginning is "exactly where the source started with"; setup commands run again on a derive from the latest state; writes "should be on"; and `evalRef` moves to derive. On A/B testing, the owner wrote: "I wonder if the best practical solution is to allow the client who is replaying to specify commands that will run on top of (and after) what is in the source plan. That way the client can create a new branch and execute the tests there".

## Scope

This design covers the one route, what each mode starts from, what the new session takes from its source, and the read that returns client messages. It applies to every session on every runtime. It removes `POST /v1/sessions/{id}/replay`.

Comparing two runs stays out of scope, and `evalRef` only records the link. A session's calls to external systems repeat if a client re-sends the messages that caused them.

## Requirements

- R1. A derive names its starting point: `latest` or `start`.
- R2. A derive composes nothing from the target runtime. The target contributes its image, its process, and its pods.
- R3. A check that would refuse a new session with this plan refuses the derive, before a pod is claimed.
- R4. A derive does not bring back content that a stop has ended.
- R5. A client can read a session's client messages in order, and it decides what to do with them.
- R6. A client can add setup commands to run after the source plan's.

## Security properties

- SD-P1. The new session takes the source's owner, as §10.2 **Session ownership.** states, and credentials are evaluated afresh. It takes the source's plan, `env`, and `runtimeOptions`, and what `tenant-scoping-identity-design.md` states a derive takes: its environment, `egressLockHosts`, `crossed_from_environment`, and its delegation limit.
- SD-P2. Every check runs against the plan on the new session's user, environment, and target pool.
- SD-P3. A plan carrying content from a tenant-derived runtime is refused after any later content stop on that runtime's grant entry or its tenant's security record, through any chain of derives, whether or not the content is still on the runtime.
- SD-P4. A derive onto another runtime is refused when the target has no runtime supervisor, because a file in the session's tree would reach an operator's setup command at the adapter's identity. A sidecar target gets the same protection only once DR-3 lands.

## Current state

- `handleDerive` (`pkg/gateway/sessionserver/derive.go`) copies the snapshot and writes a row with no plan, and one binds at finalize, which composes the target's defaults. §7.1 **Derive restore semantics:** restores the snapshot at first bind and applies the plan's sources over it.
- `handleReplay` (`replay.go`) does the same, checks `executionMode` through `requireReplayRuntimeCompatible`, and copies the transcript through `CopySessionTx`, of every role. In `sessionserver`, the transcript store is read by the GET routes, `taskrecord.go`, and `usage.go`.
- A snapshot archives the whole workspace, and `ArchiveTree` excludes no `.git`.
- A session row stores its composed plan, with `resolvedCommitSha` on each `gitClone` source. `workspaceplan.Plan` holds `SchemaVersion`, `Sources`, and `SetupCommands`. `Env` and `RuntimeOptions` are fields on the row.
- `resolveFinalizePlan` composes when a row holds no plan, and a session with no client plan on a runtime with no defaults stores none, so an absent plan does not tell a session that ran from one that never bound.
- §14 binds an `uploadRef` to its session, and `UploadDefaultTTL` (`upload.go`) stamps 7 days.
- `recordDeliveredResponse` appends inbound messages after `Send`, best-effort, and `mcptools/send_delivery.go` and the child send path append none. `transcriptstore.Entry` records no sender, so a sibling agent's held message reads as `user`, and `deriveMessageFrom` (`messages_list.go`) labels it as the client's. `projectMessage` sends text alone.
- `createAdmissionGates` holds the admission gates and `PLATFORM_DEGRADED`, `createAndStartGates` adds `requireTenantClassification`, and `validateAndBuildCreateRow` runs the `PreRoute` and `PostRoute` chains. `handleDerive` and `handleReplay` run none of those, and `handleDerive` makes two early policy calls of its own (F-15.1.78).
- `reserveDerivedSession` runs ahead of the snapshot copy. `cataloging.Copy` charges nothing to the tenant's storage counter, and `cataloging.DeleteBySession` releases the bytes of live rows, so undoing a copy lowers the counter.
- `resumeOnPod` takes the checkpoint branch whenever `WorkspaceSnapshot.Ref` is set, so a derived session resumed before its first checkpoint restores the copy with no first bind (F-7.3.36). It takes the rebuild path, which reads the plan, when no snapshot is set.
- F-14.1.37 means no `gitClone` works in a deployed image today.

## SD-1: One route

### Request

```json
{
  "from": "latest",
  "runtimeRef": "<runtime-name (optional)>",
  "targetPool": "<pool-name (optional)>",
  "targetIsolationProfile": "<profile (optional)>",
  "allowStale": false,
  "additionalSetupCommands": [{ "cmd": "git -C src checkout -b ab/run-17" }],
  "evalRef": "<eval-id (optional)>",
  "allowIsolationDowngrade": false,
  "ticketId": "<ticket (with allowIsolationDowngrade)>"
}
```

`from` defaults to `latest`. `runtimeRef` defaults to the source's runtime, it must have the source's `executionMode`, and the isolation monotonicity rule stays as §15.1 states it. A `from: start` of a source in any state needs `session.Validate`'s rule for the derive endpoint changed, because it refuses `created`, `finalizing`, and `ready` today, ahead of knowing the mode. `replay`, `replayMode`, and `prompt_history` are removed. The platform has no deployments, so nothing needs a compatibility path.

### What each mode starts from

At create, the gateway copies the source's stored plan to the new row, with `env` and `runtimeOptions`, so the plan binds at create and composition never runs. Every bind stores a plan, the canonical empty plan where nothing was composed, so an absent plan means the session never bound, in any state. That is new on the paths that store nothing today: `composeRuntimeDefaults` on a create-and-start, and `composeChildWorkspaceDefaults` for an unchanged child. Composition is skipped outright when `derivedFrom` is set.

| | `from: latest` | `from: start` |
| :-- | :-- | :-- |
| **Tree** | The snapshot. The adapter restores it, and `exclusiveBindRequest` and `slotBindRequest` pass the binder none of the plan's sources, so a clone's commit, local commits, and uncommitted changes, an edited default file, and a deleted path all stay as they were. | The plan's sources, at each `resolvedCommitSha`, with no snapshot. |
| **Source needs** | A snapshot, and `allowStale: true` when the source is live | A bound plan, in any state |
| **Uploads** | Dropped from the plan copy, because the snapshot holds their files, and `bindprepare.go` would otherwise fetch each one and fail once it has expired | Copied to the new session, with `uploadRef` rewritten, each reserving against the tenant's storage quota. An unreadable upload refuses. |
| **A resume before the first checkpoint** | Repeats the first bind | Takes the rebuild path, which reads the plan |

In both modes the plan stays on the row, because the new session's read checks, its readable set, the read tokens of the provisional binding, a warm repository's restore fetch, and a later derive all read it. Setup commands run at finalize in both modes, followed by `additionalSetupCommands`. A new pod lacks anything installed outside the workspace, so `from: latest` runs them too, over the restored tree. That can change the exact state, because `npm ci` resets `node_modules`, and a command that is unsafe to repeat can fail.

A client cannot add sources. `finalize` with a body answers as it does for any row with a bound plan. The derive response keeps its `uploadToken`, because `handleFinalize` runs `verifyUploadToken` first, and §14.1 leaves an upload no plan names unmaterialized, so the upload routes need no change. `claimDerivedRow` passes `Plan{}` today, which `slotBindRequest` consumes, so it passes the row's plan instead. `handleDerive` reads `WorkspaceSnapshot` for `ParentWorkspaceRef` and its response, so under `from: start` it writes `derivedFromSessionId` as the row's lineage in both modes.

A chain needs little handling. Each derive copies its source's plan, apart from additional commands, so a session with no checkpoint of its own passes on what its parent had. A `from: start` of a session that itself came from `from: latest` is refused, because that session's start was a snapshot its own checkpoints have since replaced.

### Additional setup commands

`additionalSetupCommands` appends client commands to the new row's plan, after the source's. They have the origin `derive`, they count toward `maxCommands`, and they pass the target's `setupCommandPolicy`, and the target's shell mode applies to them as to every command of the run. The plan copy of a later derive omits them, so a chain does not accumulate commands, a `from: latest` of the run does not repeat `checkout -b` on a tree that already has the branch, and a `from: start` of the run starts where the first session started. A resume or a rebuild of the same session reads them as any plan command. The usual use is to give a run its own branch before the client sends anything. They reach nothing a client's plan could not state.

A command sets where the session starts, and it bounds nothing. An agent told to push `fix-login` can still push that branch from `ab/run-17`, within what the session's writable entries allow. A hard limit is `allowedRefPatterns` on the entry, and decision 3 records that option. A repository the adapter checks out or restores is owned by UID 65532 until the egress design's proposal 3 resolves that, so git at the runtime's UID can refuse it as dubious ownership until then.

### Writes

Writes stay on. The plan's `writableRepositories` and write scopes copy to the new row, and each is read again at bind against the allowlist, the pool's `allowWrite`, and a `required` pool's check on the user. An entry that no longer passes reads as read-only, and the response lists it in `readOnlyRepositories`, because the request named nothing. An entry that a default clone added also follows the read-only rule under "Write scope" in `tenant-derived-runtimes-design.md`, which covers a target that reuses pods and a pool that has since turned on `prAuthoring: user`.

A client that re-sends a session's messages can therefore repeat its pushes and PRs on the real repository. That is the owner's stated choice, and `additionalSetupCommands` is how a run avoids a shared branch. Tool calls to other systems repeat in the same way.

### Reading client messages

`GET /v1/sessions/{id}/messages?from=client` returns the session's client messages in order, under the route's existing access rule. A filter on the existing route avoids a new one.

- `transcriptstore.Entry` gains a sender, with an explicit `client` value, so a sibling agent's held message is not returned, a writer that omits it is not read as the client, and `deriveMessageFrom` reads it.
- `recordDeliveredResponse` appends inbound rows before `Send`, with a delivery state, so a failed turn's message is on the record, and `toMessageNode`, which reports every row as `delivered` today, reads that state. `deliverHeldEntry` appends a held message once across its retries.
- The MCP send paths append client rows too, so a session driven through them has messages to read.

The read returns text, because `projectMessage` sends text alone. The transcript's retention, §12.8 erasure, §12.9 classification, and legal holds apply with no new rule, and the read returns nothing once the retention sweep has removed the rows. `extend-retention` lengthens that window. An SDK or CLI helper can re-send them in a loop, and the design requires none.

This closes F-15.1.77 by removing §15.1's statement. A client that re-sends messages is an ordinary client, so delivery, duplicates, and interrupts behave as they do for any session.

### What it refuses

The gateway answers `422 DERIVE_SOURCE_UNAVAILABLE` with `details.reason`, before any claim, apart from `no_snapshot`, which keeps today's `400 VALIDATION_ERROR` under §7.1:

| Reason | Cause |
| :-- | :-- |
| `no_snapshot` | `from: latest`, and the source has no resolvable snapshot. |
| `not_bound` | `from: start`, and the source has no plan, so it never bound, whatever its state. |
| `upload_unavailable` | `from: start`, and the plan names an upload that cannot be read, because its TTL passed or the retention sweep removed it. |
| `start_was_snapshot` | `from: start`, and the source itself came from `from: latest`. |
| `delegated_source` | `from: start`, and the source has a delegation parent, because its exports and inherited inputs came from that parent, and a derive is a new root. |
| `source_runtime_stopped` | "Stops" below applies. |
| `target_unsupervised` | `runtimeRef` names another runtime, and the target has no runtime supervisor. |

A commit the host no longer serves is not known before the fetch, because a pinned commit skips `ls-refs`. It fails at materialization with `SESSION_CREATION_FAILED`, after the claim, under `from: start`, and under `from: latest` when a warm repository's restore needs it.

### Checks

The plan passed its checks when the source bound it, and those checks can have changed. Before the claim, the gateway runs the checks a client's plan gets. No single function does that today: `resolveSubmittedPlan` takes raw JSON, `Parse` refuses `resolvedCommitSha`, `validatePlanUploadRefs` needs a session, and `enforceSetupCommandPolicy` and DR-9's default-clone run are separate steps. SD-1 extracts one function over a parsed plan, which every bind path calls. It covers the auth bindings, named bindings, `egress.allowedHosts`, and `gitClone` sources under the environment, the rule for pods with no per-session policy, a `required` pool's check on the user's link, and `verbatimClientFiles`.

**Admission.** `handleDerive` calls `createAdmissionGates` with the target, which gains `requireTenantClassification`, and it drops its own two early policy calls, which would otherwise run `PostAuth` twice. It runs the `PreRoute` and `PostRoute` chains from `validateAndBuildCreateRow`, so a deployer's interceptor that rejects a runtime also rejects a `runtimeRef` naming it. An interceptor that would rewrite a derive's runtime refuses the derive, because the request named its target. `routeExperiment` does not run on a derive, for the same reason, so an experiment neither moves nor refuses one. The tenant view resolves the name first, ahead of `requireAdmissionRateLimit`'s `ResolvePool`, so a name from another tenant creates no rate-limit entry. This closes F-15.1.78.

**Order.** Every gate, the plan checks, the stop check, and the `derive.isolation_downgrade` audit run ahead of `reserveDerivedSession` and any copy, so a refusal has nothing to undo, and a refused derive copies nothing and records no downgrade. Each copy, of a checkpoint chunk or an upload, then charges the tenant's storage counter, which `cataloging.Copy` does not do today, so `DeleteBySession` releases only what was charged. A copy that fails has no catalog row, so its reservation is released explicitly.

**Setup commands.** §7.5 exempts operator-written defaults from `setupCommandPolicy`, and the stored plan does not say which commands those are. Each stored command therefore gains an `origin`: `operator`, `tenant`, `client`, or `derive`. On a derive onto the same runtime, an `operator` command stays exempt. On a derive onto another runtime, in either mode, it keeps that origin only when its text is among the target's own operator-written defaults, and otherwise the plan copy relabels it `client`, so the target's effective policy checks it. Under either mode the tree can hold content a client or an agent chose, a plan file under `from: start` or an edited script under `from: latest`, so `npm ci` from another runtime's defaults would otherwise run client-chosen code past a policy the target's operator set. The relabel stays on the copy, so a later derive on that runtime cannot regain the exemption. A `tenant` command is also refused on a target whose policy is not `allowlist` with `shell: false`, as DR-4 requires, because an inert string would otherwise run under a shell. The source's original commands are the only ones that run, plus any the request adds, and the check decides whether the target admits them.

**Warm stores on the target.** A target's pods can hold warm stores for clones the plan does not name, under "Warm clones" in `tenant-derived-runtimes-design.md`, and the agent can read them. The gateway runs the read checks for each warm default clone on the target that the plan omits, and `coveredStores` includes them.

### Stops

A stored plan can hold content a tenant has since stopped, and a grant flag set false or a forbid is "the stop after a compromise". A derive is a new session, so it is refused. Reading the source's runtime alone would not hold, because a derive onto a platform runtime before a forbid leaves a row whose runtime is that platform runtime. A plan therefore carries `planOriginRuntimes`, the tenant-derived runtimes whose defaults it holds. A derive copies its source's set at create, and it adds nothing, because it composes nothing. A session that composes adds its runtime at bind, in the update that binds the plan, and a delegated child copies its parent's at bind, because a child's exports came from that parent.

A runtime's current state would not hold either. If a `security-admin` sets a forbid or an operator sets `allowGitClones` false after the tenant has removed the clone, the runtime looks clean, and a derive of a session from before the incident would rerun the pinned commit. Two counters therefore record stops, in a tenant-keyed `content_stop_epochs` table, outside the grant's and the security record's bodies, because a `PUT` replaces each of those documents and SO-2 stores the record as versions, so a pending weakening written before a forbid would otherwise regress a counter kept inside it. An absent row reads as 0. Each is incremented in the stop's own transaction: the grant entry's, keyed by base, when `allowWorkspaceFiles`, `allowSetupCommands`, or `allowGitClones` is set false, and the security record's, in the transaction that activates a version adding `all`, `setupCommands`, `workspaceFiles`, `gitClones`, or `floatingCloneRefs`, or setting `verbatimClientFiles: forbid`. Each `planOriginRuntimes` entry stores both values, from the same read of the tenant view that composition used.

The check is one function, which `handleDerive` calls at create and finalize calls again, because a row created before a stop can finalize after it. For each entry, the tenant view must resolve that runtime now, it must not be retired, and both stored values must equal the current ones. A deleted runtime refuses, and names stay consumed, so one cannot be recreated to pass. A runtime that is `incompatible` for another reason, such as `Registered=False`, does not refuse. A plan from before a stop stays refused after the stop is lifted, because a counter only rises. The owner can still read such a workspace through `GET /v1/sessions/{id}/workspace`.

A platform-owned source runtime has no such rule, because starting a retired version's session on its successor is the regression test.

### Supervisors

SD-P4 refuses a derive onto another runtime when the target has no supervisor, in both modes, whatever the source and the plan's origins. An agent on the source can leave a `requirements.txt` that an operator's command on the target would run at the adapter's UID, with the service account token. `runsSupervisor` is an unexported controller function over the CRD, so the gateway uses a new helper over the runtime row, reading the `type` and `deploymentModel` that DR-4 mirrors. It requires `registered` to be true and fails closed, because the mirror stores an empty value as `sidecar` and leaves the row stale when it refuses a resource.

A derive on the same runtime is not refused. On a pod with no supervisor, the source's agent already runs as the adapter process (§4.7.10), so a kept file gives it nothing new. Until DR-3 lands, setup on a sidecar pod runs at the adapter's UID and a restored file reaches it, which F-7.5.17 records.

## What an exact start does not cover

`from: start` matches what the plan states, and `from: latest` matches what the snapshot holds. These differ, and the documentation says so:

- **The image.** A target runtime's image is the variable under test.
- **What setup commands fetch.** `npm install` resolves against the registry on the day it runs.
- **External answers.** Tool calls and model responses are not recorded inputs.
- **Egress and credentials.** These are the tenant's and the pool's settings on the day of the derive.
- **A target that expects its own defaults.** Nothing composes, so a target relying on a default clone or a setup command it added runs without it.
- **`from: latest`'s moment.** A snapshot records its own moment, and on a live source that is the last checkpoint. The session files under `/sessions` are not restored, as §7.1 states.
- **Messages the read lacks.** A lost append, and attachments.

## Costs

- A client that wants a re-run writes the loop, or uses a helper, and it stays attached for the run.
- A client that re-sends messages repeats pushes and external calls on real systems.
- An additional command is not repeated by a later derive, so a package it installed outside the workspace is missing under `from: latest` of that run.
- A derive cannot add files, so a client that needs them uses an additional command. Today's derive can add them at finalize.
- A tenant's later change to a runtime reaches no derived session, including a security fix to a default file on a platform runtime that a tenant did not stop.
- A source that used uploads cannot be derived `from: start` once they are gone, 7 days after each upload, or sooner under retention, which is 24 hours by default on a T4 workspace.
- A content stop refuses every earlier plan under that grant entry or tenant, on any runtime and whether or not that runtime held the stopped content, and it stays refused after the stop is lifted.

## Defects found while verifying

| Finding | Severity | Defect |
| :-- | :-- | :-- |
| F-15.1.76 | Medium | `DeriveRequest.RuntimeRef` is in no specification section, and `handleDerive` runs no `executionMode` check or supervisor check on it. |
| F-15.1.77 | High | Replay delivers no prompts, and the MCP send paths record none. |
| F-15.1.78 | Medium | `handleReplay` and `handleDerive` skip the admission checks a create runs. |
| F-7.3.36 | Medium | Already filed. A derived session resumed before its first checkpoint restores the copy with no first bind. |

## Specification sections affected

- §15.1: the derive row and the precondition table gain `from`, `runtimeRef`, `additionalSetupCommands`, and `evalRef`. The replay row and **Session Replay Semantics** go, with a successor pointer to the derive row. The messages route gains `from=client`. The error table gains `DERIVE_SOURCE_UNAVAILABLE`, and `REPLAY_ON_LIVE_SESSION` goes.
- §7.1 **Derive session semantics** and **Derive restore semantics:**: the source's states and the rules for a bound plan, and `from: latest` materializes no source, and a resume with no checkpoint of the session's own repeats the first bind. `from: start` carries no snapshot, so "the only kind of session whose record carries a workspace snapshot before its first bind" narrows to `from: latest`.
- §7.5 **Command policy:**: `origin`, and how a derive reads it.
- §4.5 **Workspace lineage:**, §10.2, §11, §13, §18, and §23: the mentions of replay.
- §14 and §14.1 **Plan binding.**: a derive binds at create from the stored plan, step 1 of **Runtime workspace defaults.** does not run for it, and a setup command carries `origin`.
- §12.4 and §15.4: the sender on a `session_messages` row.

## Stored formats and wire contracts this forces

| Carrier | Change |
| :-- | :-- |
| `POST /v1/sessions/{id}/derive` | `from`, `runtimeRef`, `additionalSetupCommands`, and `evalRef`. The response gains `readOnlyRepositories`. |
| `POST /v1/sessions/{id}/replay` | Removed |
| `GET /v1/sessions/{id}/messages` and `GET /v1/sessions` | `from=client`, and the `evalRef` filter that §15.1 names |
| OpenAPI | `pkg/gateway/externalapi/openapi/openapi.json` for each of these |
| Setup command | `origin`, gateway-written and refused from a client, in the stored plan, `schemas/workspaceplan-v1.json`, `lenny-adapter.proto`, and the mapping in `podsession/workspaceplan.go` |
| Session row | `derivedFrom` (`latest` or `start`), `derivedFromSessionId`, `evalRef`, `snapshotIsOwn`, written by the checkpointer, and `planOriginRuntimes` with each entry's two stored values |
| `session_messages` | A sender on each row, `client` for a client, and a delivery state |
| `content_stop_epochs` | One row per tenant for its security record and one per grant entry's base |
| Upload store | A copy of an upload under the new session, with a new `uploadRef` |

## Proposals and dependencies

| Proposal | Contents | Depends on |
| :-- | :-- | :-- |
| SD-1 | Everything above, closing F-15.1.76, F-15.1.77, F-15.1.78, and F-7.3.36 | None for a plan with no clone. A plan with a clone needs the egress design's pod-side clone, as any `gitClone` does. SD-P4 on a sidecar target needs DR-3. "Warm stores on the target" and `coveredStores` arrive with DR-11, and "Stops" with DR-4, DR-6, and SO-2. SD-1 also extracts the plan check function that DR-9 calls. |

It can land before those designs, and each adds its rule to the checks when it lands.

## Documentation

| Page | Change |
| :-- | :-- |
| `docs/tutorials/session-derive-replay.md`, `docs/tutorials/evaluation-scoring.md`, and `docs/client-guide/session-lifecycle.md` | One route and two modes, how to re-send messages, how to give a run its own branch, and "What an exact start does not cover". |
| `docs/api/rest.md`, `docs/api/mcp.md`, `docs/client-guide/wire-format.md`, `docs/client-guide/sdk-examples/curl.md`, `docs/reference/glossary.md`, and `docs/reference/error-catalog.md` | The request, the read, and `DERIVE_SOURCE_UNAVAILABLE`. |
| `docs/about/comparisons.md` | Removes the replay modes. |

## Tests

### Tier 1: unit

- The plan copy keeps each `resolvedCommitSha` and composes nothing from the target. A bind with nothing composed stores the empty plan.
- An `operator` command stays exempt on the same runtime and on another whose defaults hold that text, and it is relabelled `client` and checked otherwise, in both modes, and a later derive on that runtime keeps the relabel. `tenant`, `client`, and `derive` commands are checked. A `tenant` command is refused on a target without `allowlist` and `shell: false`. A client's `origin` is refused.
- `planOriginRuntimes` copies through a chain.
- Each refusal reason.

### Tier 3: contract

- `POST .../replay` is gone. `origin` survives the stored plan, the schema, the mapping, and the adapter's decode. `from=client` returns no sibling agent's message and no assistant row.

### Tier 4: integration

- `from: latest` of a session with an expired upload binds, on the exclusive and slot paths, because the copy drops upload sources. `from: latest` keeps a default clone's commit, local commits, and uncommitted changes, an edited default file, and a deleted path, and it adds a clone new to the runtime nowhere. It survives a chain with no checkpoint. Setup commands run again, then additional ones.
- `from: start` starts with no file a prompt created, at the source's commit, with the target's added clone absent and its dropped clone present. An upload copies, and an expired or swept one refuses. It works on a live source and on one whose snapshot has been collected.
- A session resumed before its first checkpoint repeats the first bind under `from: latest`, and it rebuilds from the plan under `from: start`.
- `from: start` of a `from: latest` session and of a delegated child refuses. `from: start` of a `from: start` session starts where the first session started.
- A derive binds no second plan at finalize, finalize accepts the derive's `uploadToken`, and an upload no plan names is not materialized.
- `env` and `runtimeOptions` copy, and an option the target's schema refuses refuses the derive.
- A derive meets each create gate once and the `env` blocklist, a `PreRoute` REJECT on the target refuses it, an interceptor rewriting its runtime refuses it, and a name from another tenant creates no rate-limit entry. An experiment on the runtime neither moves nor refuses a derive. A refused derive copies nothing and records no downgrade, repeated refusals leave the storage counter unchanged, and a completed copy charges it. `from: start` of a session cancelled before binding refuses with `not_bound`, and so does `from: start` of one that never reached a bind. `from: latest` of a create-and-start session or an unchanged child composes nothing. An upload copy past the tenant's storage quota refuses.
- Messages read back in order, including a failed turn's, marked undelivered, and one sent through the MCP send paths, and a retried held message appears once. The read returns nothing after retention.
- An additional command runs after the source's, is refused by the target's policy, reruns on a rebuild of the same session, and is absent from a later derive's plan, so a chain does not accumulate commands and a `from: latest` of the run does not fail on an existing branch.

### Tier 9: security

- A repository off the allowlist, a user who lost access on a `required` pool, and an environment that denies the host each refuse before any claim, on both modes, with a kept tree included.
- A user who lost write access gets a read-only session, listed in `readOnlyRepositories`, and no write token. An entry a default clone added reads as read-only on a target that reuses pods, and when its pool later turned on `prAuthoring: user`.
- A content forbid, a retirement, a set-false grant flag, and a deletion of a tenant-derived source runtime each refuse, including through a chain onto a platform runtime, a stop that arrives after the tenant removed the clone, and after the stop is lifted. A plan bound after the stop passes. `Registered=False`, a non-content forbid, and a tenant suspension raise nothing. A row created before a stop refuses at finalize. A pending weakening activated after a forbid does not lower the counter. A retired platform source runtime passes.
- A `runtimeRef` onto a target with no supervisor refuses in both modes, and a target with `registered` false refuses. A same-runtime derive on such a pod passes.
- A derive keeps the source's `egressLockHosts` and environment, and a caller who left that environment answers 403.
- A target pod holding a warm store for a clone the plan omits is refused for a user who cannot read it.

### Tier 11: documentation

- Documented examples of the request pass the schema.

## Decisions

The owner settled the decisions below as recommended on 2026-11-04, in the words "go with your recommendations". The owner also settled one route with two modes, the exact start, the client sending messages, writes on, setup commands running again on `from: latest`, and moving `evalRef`, and settled these earlier: an expired upload refuses, a source that started from a snapshot refuses `from: start`, and a content stop refuses earlier plans permanently.

| # | Decision | Recommendation | Cost of the recommendation | Alternative |
| :-- | :-- | :-- | :-- | :-- |
| 1 | Exempting setup commands | **Settled.** The owner accepted this narrower rule after a review, in the words "keep the narrowed rule". An `origin` on each stored command. An `operator` command stays exempt on the same runtime, and on another whose operator-written defaults hold that text | An upgrade that changed an operator command depends on the new version's policy to run the old one, and a derive is refused otherwise | Exempt under `from: start` as the owner first accepted, which a client can use to run its own content past a target's policy |
| 2 | `additionalSetupCommands` | **Settled.** The request field the owner proposed, under the target's policy | A base whose allowlist omits `git` refuses it, and a command is a convention with no enforcement | Nothing added, which leaves a run on the source's branch |
| 3 | A hard limit on a run's pushes | **Settled.** None in the first version | An agent can still push outside the run's branch within the entry's patterns | A request field narrowing `allowedRefPatterns` on each writable entry, which `netagent` enforces. It is additive |
| 4 | Files on a derive | **Settled.** None. A client uses an additional command | A client that added files at finalize today needs a command to fetch them, under the target's policy and egress | Let a client add sources at finalize, which keeps the upload and `path` rules and makes the plan no longer the source's |
| 5 | The client messages read | **Settled.** `?from=client` on the existing messages route | The route's response keeps its other fields | A new route, as the owner first described, which is more explicit and adds a route |


## Review of revision 1

An independent review read revision 1. It found no Critical or High issue, three Majors, and no path across tenants. Each was checked against the tree, and revision 2 applies them.

| Finding | Result | Check and outcome |
| :-- | :-- | :-- |
| Major, a derived session cannot finalize | Holds. | `handleFinalize` runs `verifyUploadToken` first. The token stays, which removes the upload refusal. |
| Major, additional commands carry into later derives | Holds. | A later `from: latest` would fail on `checkout -b`. The origin `derive` is dropped from a plan copy. |
| Major, the `operator` exemption crosses bases | Holds. Fix changed. | The reviewer proposed storing the base, which would refuse a v1 to v2 regression test. The exemption stands under `from: start`, whose tree is the plan's, and on the source's runtime, and an agent-written tree onto another runtime is checked. |
| Minors: the command's type and shell, `tenant` on a weak target, DR-3 under SD-P4, ownership of restored repositories, code assuming the old row, missing fields, the sender's default, delivery state, and stale text | Hold. | Applied. |

## Review of revision 2

A stop check read revision 2 and `tenant-derived-runtimes-design.md` revision 29. It found no Critical or High issue, two Majors, and no path across tenants. Each was checked against the tree, and revision 3 applies them.

| Finding | Result | Check and outcome |
| :-- | :-- | :-- |
| Major, the `operator` exemption can be evaded | Holds. Narrows a settled decision. | "The tree is the plan's" was wrong, because a client writes that plan's files. A chain also regained the exemption over an agent-written tree. The relabel closes both, and an unchanged command still passes. The owner accepted this over the broader rule first settled. |
| Major, `from: latest` still fetches the source's uploads | Holds. | `stageWorkspace` in `bindprepare.go` runs on the plan before `FinalizeWorkspace`, and it calls `Blobs.Get` on each `uploadFile`. The copy drops upload sources. |
| Minors: upload quota, and the other create gates | Hold. | Applied. |

## Review of revision 3

A stop check read revision 3 and `tenant-derived-runtimes-design.md` revision 29. It found no Critical or High issue, one Major, and no path across tenants. It was checked against the tree, and revision 4 applies it. The check also read each reader of a `from: latest` plan with its upload sources dropped, and found them sound.

| Finding | Result | Check and outcome |
| :-- | :-- | :-- |
| Major, a derive skips the `PreRoute` and `PostRoute` chains | Holds. | `validateAndBuildCreateRow` runs them, and `handleDerive` does not, so a rejected runtime was reachable through `runtimeRef`. A shared admission function replaces the list, and a rewrite refuses. |
| Minors: the order of the tenant view, the upload cost, and sessions that never bound | Hold. | Applied. A bind always stores a plan, which removes the empty-plan copy rule. |

## Review of revision 4

A stop check read revision 4 and `tenant-derived-runtimes-design.md` revision 29. It found no Critical or High issue, one Major, and no path across tenants. It was checked against the tree, and revision 5 applies it.

| Finding | Result | Check and outcome |
| :-- | :-- | :-- |
| Major, a refused derive lowers the tenant's storage counter | Holds. Exists today for checkpoint chunks, and revision 3 extended it to uploads. | `DeleteBySession` releases live rows' bytes, and `Copy` charges nothing. All refusals now come ahead of the copy, and each copy charges the counter. |
| Minors: the source of the gates, the experiment refusal, and "every bind stores a plan" | Hold. Fix changed for the experiment. | Revision 4's refusal would have stopped every enrolled user's derive during an experiment. `routeExperiment` does not run on a derive. |

## Earlier history

`session-replay-design.md` went through seven review rounds before the owner replaced it, and its text at commit the status line names records them. Most of their Majors concerned the gateway's prompt driver, which this design removes. Their findings on stops, origins, unsupervised targets, the admission gates, and upload handling carry over here.
