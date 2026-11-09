# Session derive

Status: design for owner review, revision 14, 2026-11-09. Nothing here is built or staged, and no test or spike was run for it. Statements about the tree were checked by reading the code and the specification. It reads against [`tenant-derived-runtimes-design.md`](tenant-derived-runtimes-design.md) revision 62, [`tenant-scoping-identity-design.md`](tenant-scoping-identity-design.md) revision 32, [`egress-and-git-design.md`](egress-and-git-design.md) revision 74, [`runtime-routing-design.md`](runtime-routing-design.md) revision 9, [`runtime-revisions-design.md`](runtime-revisions-design.md) revision 5, [`incremental-checkpoint-design.md`](incremental-checkpoint-design.md) revision 8, and [`connector-authorization-and-external-memory-design.md`](connector-authorization-and-external-memory-design.md) revision 12. The owner has settled the decisions under "Decisions". This document replaces `session-replay-design.md`, whose last revision is at commit `3517d6fe7` with its review history. Proposals carry the label SD-n. "Review history" at the end records how the design reached this form.

## Problem

[§15.1 REST API](../spec/15_external-api-surface.md#151-rest-api) defines two routes that start a session from an earlier one. `POST /v1/sessions/{id}/derive` copies a snapshot, and `POST /v1/sessions/{id}/replay` copies the same snapshot and also claims to replay the prompts.

- **Replay starts from the end.** §15.1 populates the workspace from "the source session's sealed final workspace snapshot". A prompt that says "create `feature.py`" would run where `feature.py` exists.
- **Replay delivers no prompts.** Under replay's default `replayMode: prompt_history`, §15.1 says the source session's prompt history, read from its transcript, "is replayed verbatim as the initial message sequence", and the code copies the transcript and never reads it back on a start path (F-15.1.77).
- **`replayMode: workspace_derive` duplicates derive.** §15.1 states that this replay mode equals a derive onto another runtime, with no prompts delivered. Derive already takes an optional `runtimeRef` in the code, which the specification does not state (F-15.1.76).
- **Neither route has a defined way to start from the beginning**, which is what a regression test needs.

Identifiers of the form F-n.n.n, such as F-15.1.77, are findings filed in `BUILD-GAPS.md` at the repository root.

## Owner's choice

On 2026-11-04 the owner wrote: "I would change derive + replay so that everything is a derive with two options: derive at start of session or derive at now/the latest checkpoint. A new API can return the user messages from the source session and the client can choose to replay them (how it's up to the client)."

The owner also settled these: "ideally we pick up the exact state in which the source session was"; a start from the beginning is "exactly where the source started with"; setup commands run again on a derive from the latest state; and writes "should be on". The owner first moved `evalRef` to derive and later retired it, under decision D11 of [`runtime-routing-design.md`](runtime-routing-design.md#decisions). On A/B testing, the owner wrote: "I wonder if the best practical solution is to allow the client who is replaying to specify commands that will run on top of (and after) what is in the source plan. That way the client can create a new branch and execute the tests there".

## Scope

This design covers the single derive route that replaces both routes, what each of its modes starts from, what the new session takes from its source, and the read that returns client messages. It applies to every session on every runtime. It removes `POST /v1/sessions/{id}/replay`.

Comparing two runs stays out of scope, and a derive records no evaluation identifier. A session's calls to external systems repeat if a client re-sends the messages that caused them.

## Terms

| Term | Meaning |
| :-- | :-- |
| Derive | `POST /v1/sessions/{id}/derive`, which creates a new session from an earlier one. Under this design it is the only route of its kind. |
| Source session | The session a derive starts from. The new session is the derived session. |
| Mode | A derive's `from` field. `from: latest` starts from the source's latest snapshot, and `from: start` starts from the source's stored plan with no snapshot. "What each mode starts from" states both. |
| Exact start | The owner's aim that a derived session begin in the state its source was in: at the source's beginning under `from: start`, or at its latest snapshot under `from: latest`. "What an exact start does not cover" lists what can differ. |
| Target runtime | The runtime the derived session runs on. The request names it in `runtimeRef`, by default the source's runtime, and a `PreRoute` interceptor can change it before the post-chain point, so the target is the runtime the chain leaves. "Another runtime" in this document means a target other than the source's runtime. |
| Stored plan | The workspace plan a session row holds once the plan binds, with `resolvedCommitSha`, the commit each `gitClone` source resolved to. Under this design a derive copies its source's stored plan. |
| `origin` | A field this design adds to each stored setup command: `operator`, `tenant`, `client`, or `derive`. It decides which policy checks the command, and it decides no folder, because the command's stored `cwd` holds its folder. "Checks", **Setup commands.**, states how a derive reads it. |
| `additionalSetupCommands` | Setup commands a client adds to a derive, which run after the source's, in the plan's working directory unless a command sets its own `cwd`. See "Additional setup commands". |
| Content stop | A change that ends a tenant-derived runtime's content after a compromise: a grant flag set false, or a forbid in the tenant's security record. See "Stops". |
| Content stop check | The function under "Stops" that refuses a derive, at create and at finalize, when a runtime in `planOriginRuntimes` no longer resolves, is retired, or carries a changed stop counter. It is unrelated to the review rounds that "Review history" calls stop checks. |
| `planOriginRuntimes` | The tenant-derived runtimes whose defaults a plan holds, each with the two stop counters read when the plan was composed. See "Stops". |
| `content_stop_epochs` | The tenant-keyed table that holds the stop counters. See "Stops". |
| Client messages | The messages a client sent to a session, as the transcript records them. "Reading client messages" adds a read that returns them. |
| Held message | A message the gateway holds in a session's inbox or dead-letter queue until the session can take it ([§7.2](../spec/07_session-lifecycle.md#72-interactive-session-model)), such as one another session's agent sends with `lenny/send_message`. |
| Transcript | A session's message record, the `session_messages` table, which the `transcriptstore` package writes and reads. |
| Lineage | `derivedFromSessionId` on the derived session's row, which names its source session. |
| `derivedFrom` | A field this design adds to the derived session's row, holding the mode, `latest` or `start`, that created it. It is unrelated to `derivedFromSessionId`, which names the source. "What it refuses" reads it for `start_was_snapshot`. |
| `snapshotIsOwn` | A field this design adds to the session row, which the checkpointer sets once the session's own checkpoint has written its snapshot. Until then a derived session's snapshot is the one its derive copied, so a resume repeats the first bind, and a later derive of it passes on what its parent had. |

### Terms from other designs and the platform

This design builds on mechanisms other designs and the specification define. The table gives each one the meaning this document relies on.

| Term | Meaning |
| :-- | :-- |
| Workspace plan | The `WorkspacePlan` of [§14 Workspace Plan Schema](../spec/14_workspace-plan-schema.md#14-workspace-plan-schema). Its `sources` are files, uploads, and `gitClone` clones of git repositories, each written at a workspace-relative `path`, and it lists `setupCommands`. |
| `env` and `runtimeOptions` | Session fields of the create request, stored on the session row beside the plan. `env` holds environment variables injected into the agent session, and the `env` blocklist is a deployer-configured list of variable names and patterns that a create refuses ([§14](../spec/14_workspace-plan-schema.md#14-workspace-plan-schema)). `env` is unrelated to an environment, below. `runtimeOptions` holds runtime-specific options, checked against the target runtime's `runtimeOptionsSchema`. |
| Session states | A session moves through `created`, `finalizing`, `ready`, `starting`, and `running`, and while live it can also be `input_required`, `suspended`, `resume_pending`, `resuming`, or `awaiting_client_action`. `completed`, `failed`, `cancelled`, and `expired` are terminal. A live source is one in a non-terminal state. Today `session.Validate` refuses a derive of a source in `created`, `finalizing`, or `ready`. Under this design `from: latest` needs a resolvable snapshot, and `from: start` needs only a bound plan. |
| Plan binding | The step that fixes a session's plan and stores it on the session row ([§14.1](../spec/14_workspace-plan-schema.md#141-workspaceplan-schema-versioning) **Plan binding.**). Today a plan binds at create or at finalize. |
| Composition | The gateway merging the target runtime's `workspaceDefaults`, which are its default files, default clones, and setup commands, into the plan when the plan binds ([§14.1](../spec/14_workspace-plan-schema.md#141-workspaceplan-schema-versioning) **Runtime workspace defaults.**). |
| Finalize | `POST /v1/sessions/{id}/finalize`, which ends a session's `created` state. It binds a plan if none is bound, materializes the workspace, and runs the setup commands. |
| Claim and bind | A claim reserves a warm pod for a session, and bind attaches the session to that pod and builds its workspace. A session's first bind is the first time this happens. A resume binds again. The exclusive bind path (`exclusiveBindRequest`) binds a session to a whole pod, and the slot bind path (`slotBindRequest`) binds it to one slot of a pod that serves several sessions at once. |
| Credential binding | An object of the [egress design](egress-and-git-design.md) that gives sessions a secret for named hosts. A binding with `delivery: inject` has `netagent` attach the secret to requests, so the agent never holds it. A named binding is one with `scope: onRequest`, which attaches only to a session whose plan names it. |
| User link | A Lenny user's stored authorization at a provider, such as their GitHub account, which [`user-credential-delivery-design.md`](user-credential-delivery-design.md) defines. On a VCS pool with `userAuthorization: required`, Lenny checks through the link that the user can reach each repository. |
| Read checks | The checks at bind that a session may read each repository it can reach: the repository allowlist, the session's environment, and, on a VCS pool with `userAuthorization: required`, the user's own access through their link. |
| Provisional binding | The state of a pod during a resume rebuild, before the session returns to `running`, in which the pod receives only read tokens and the secrets of bindings with `delivery: inject`. |
| Readable set | The repositories a session may read, recorded as entries with a state, beside its writable entries ([`egress-and-git-design.md`](egress-and-git-design.md)). |
| Materialization | The adapter writing the plan's sources into the pod's workspace. |
| Snapshot and checkpoint | A workspace snapshot is an archive of a session's whole workspace. A checkpoint writes one while the session runs, and a session that ends seals a final one ([§4.5](../spec/04_system-components.md#45-artifact-store) **Workspace lineage:**). [§7.1](../spec/07_session-lifecycle.md#71-normal-flow) **Derive copy semantics:** copies the source's snapshot to a derived session today. A rebuild is a resume with no snapshot, which builds the workspace from the plan. |
| Upload | A file a client sends before its session starts. The plan names it by `uploadRef`, and finalize accepts the `uploadToken` the create or derive response returned. An upload expires 7 days after it is made, under `UploadDefaultTTL`. |
| Setup command and `setupCommandPolicy` | A setup command is a command the plan lists, which runs in the pod at finalize ([§7.5 Setup Commands](../spec/07_session-lifecycle.md#75-setup-commands)). The runtime's `setupCommandPolicy` admits each one through its mode, such as `allowlist`, its `shell` setting, and `maxCommands`. §7.5 **Command policy:** exempts the defaults an operator wrote on the runtime. |
| Runtime and `executionMode` | A runtime is a registered agent definition, with its image, its process, its `executionMode`, its `workspaceDefaults`, and its `setupCommandPolicy`. `executionMode` states how the runtime serves sessions, such as `session`. |
| Pool and isolation profile | A pool keeps warm pods of one runtime. A pool's `sessionIsolationLevel.isolationProfile` is `standard`, `sandboxed`, or `microvm`, in increasing strength. §15.1's isolation monotonicity rule refuses a target weaker than the source unless the request sets `allowIsolationDowngrade`. |
| Pods that serve several sessions | A target that reuses pods is a pool whose pods serve more than one session, at once or one after another. A pod with no per-session policy is a concurrent or service-mode pod, which applies only the tenant's unscoped egress objects, those that name no environment, to every session it serves ([`tenant-scoping-identity-design.md`](tenant-scoping-identity-design.md)). |
| Adapter | The Lenny process in each agent pod that talks to the gateway and materializes the workspace. It runs at its own UID and holds the pod's service account token. |
| Runtime supervisor, sidecar, and embedded | A runtime's `deploymentModel` is `sidecar` or `embedded`. On a sidecar pod of a `type: agent` runtime, the runtime supervisor is the first process in the runtime container, and it launches the runtime's process when the adapter asks ([§4.7.10](../spec/04_system-components.md#4710-deployment-model) **Runtime supervisor.**). An embedded pod has no supervisor, and the agent runs as the adapter process. |
| Tenant-derived runtime and platform runtime | A tenant-derived runtime is one a `tenant-admin` derives from a platform runtime, its base, and it can add default files, setup commands, and default clones ([`tenant-derived-runtimes-design.md`](tenant-derived-runtimes-design.md)). A platform runtime is one the platform operator registers. |
| Derivation grant and grant entry | A platform admin's grant that lets a tenant derive runtimes. It holds one entry per base, with the flags `allowWorkspaceFiles`, `allowSetupCommands`, and `allowGitClones` ([`tenant-derived-runtimes-design.md`, "Who turns it on"](tenant-derived-runtimes-design.md#who-turns-it-on)). |
| Tenant view | The runtime store's read for one tenant, which resolves a runtime name to a platform runtime the tenant is granted or to one of the tenant's own ([DR-4](tenant-derived-runtimes-design.md#dr-4-tenant-runtime-records)). A retired runtime is one the base lifecycle of [DR-6](tenant-derived-runtimes-design.md#dr-6-base-changes) has retired, which is unrelated to a retired revision under [`runtime-revisions-design.md`](runtime-revisions-design.md), and an `incompatible` one is one whose live base or grant entry no longer admits its settings. |
| Default clone, warm store, `coveredStores`, and `withheldStores` | A default clone is a `gitClone` source a tenant-derived runtime adds to every session's plan ([DR-9](tenant-derived-runtimes-design.md#dr-9-default-git-clones)). A warm store is a copy of a repository's objects that a pod fetches before any session claims it, for an entry on its pool's warm list: a tenant's warm default clone, a tenant's cache-only entry, which adds no checkout, or an operator's entry ([`tenant-derived-runtimes-design.md`, "Warm clones"](tenant-derived-runtimes-design.md#warm-clones), **Warm list.**). A store serves any `gitClone` source in the plan whose URL matches it. When a session resumes onto a pod whose store differs, the adapter's restore fetch gets the clone's base commit through `netagent`. `coveredStores` lists on a claim the stores the session's read checks cover, together with the stores of the pool's public platform entries. `withheldStores` lists the stores of entries whose checks failed, which the adapter deletes before the session starts (**Withheld stores.** in the same section). |
| Tenant security record and forbid | The record in which a `security-admin` sets a tenant's guardrails ([SO-2](security-oversight-design.md#so-2-tenant-security-record-and-posture)). `derivedRuntimes.forbid` names the derived-runtime features the tenant may not use, and `verbatimClientFiles: forbid` forbids the `verbatimClientFiles` gate of the [egress design](egress-and-git-design.md). The record is stored as versions, and a change that weakens a guardrail waits as a pending version until a delay passes. |
| `verbatimClientFiles` | The egress design's per-tenant gate, a grant a platform admin gives, on the tenant's client config files, which are tool configuration files such as an `.npmrc`, and on the file templates a tenant's pool overlay adds to the client proxy profile. `netagent`, the Lenny container the egress design adds to an agent pod, writes both into the pod verbatim. |
| Environment | A named scope inside a tenant with its own hosts, routes, and denies, which a session records ([`tenant-scoping-identity-design.md`, "TS-3: Session environment context and cross-environment sharing"](tenant-scoping-identity-design.md#ts-3-session-environment-context-and-cross-environment-sharing)). `egressLockHosts` is a lock that limits a session and its descendants to named hosts, `crossed_from_environment` records that a delegated child crossed from another environment, and the delegation limit bounds the depth of a session's delegation tree. |
| Delegation | A session can start child sessions, which form a delegation tree under a root session. A child's exports and inherited inputs come from its parent. |
| Admission gates and interceptors | The checks a create runs before it claims a pod, such as rate limits, `PLATFORM_DEGRADED`, and tenant classification. `PostAuth`, `PreRoute`, and `PostRoute` are interceptor phases at which deployer-supplied interceptors can reject or rewrite a request ([§4.8](../spec/04_system-components.md#48-gateway-policy-engine)). |
| `PreRoute` chain, creation kind, and last modifier | The `PreRoute` chain is the single run of every `PreRoute` interceptor, in priority order, on every session creation path, including the built-in route interceptor that applies runtime routes ([`runtime-routing-design.md`, "RT-2 The interceptor contract"](runtime-routing-design.md#rt-2-the-interceptor-contract)). Its payload's `kind` field is `create`, `delegate`, or `derive`, and an interceptor's `MODIFY` may change the payload's `requested_runtime`, `requested_pool`, and `routing`. The last modifier is the interceptor whose `MODIFY` last changed a field, which a refusal after the chain names in `details.interceptor_ref`. |
| Runtime route | A tenant's admin resource that sends the sessions requesting one runtime to target runtimes by weight, chosen by a stable hash of a routing key such as the user ID ([`runtime-routing-design.md`, "RT-4 Runtime routes"](runtime-routing-design.md#rt-4-runtime-routes)). |
| Routing labels | The `routing` map on a session, which `PreRoute` interceptors write and the session record then keeps, and which the runtime, billing events, and usage reports read ([`runtime-routing-design.md`, "RT-3 Routing labels"](runtime-routing-design.md#rt-3-routing-labels)). |
| Runtime revision and traffic | A revision is an immutable, numbered version of the runtime fields that define what runs in its pods, such as the image and `deploymentModel`. A runtime's traffic is its active revisions and their weights, and the gateway places each new session on one revision by a hash of its rollout key over that traffic ([`runtime-revisions-design.md`, "RV-2 Selection"](runtime-revisions-design.md#rv-2-selection)). No request can name a revision. |
| Post-chain point | The step of session creation that follows the `PreRoute` chain and revision selection and precedes the pod claim. Every check that depends on the runtime, the revision, or the pool runs there, on the final runtime and pool and the chosen revision, whichever interceptor chose them ([`runtime-routing-design.md`, "Post-chain point"](runtime-routing-design.md#post-chain-point)). A check that reads a versioned field, such as `deploymentModel`, reads the chosen revision. |
| Connector link check and `deferConnectors` | The check at session creation that refuses a session on a runtime with `capabilities.deferredToolDiscovery: false` when a connector the session may use needs the user to link an account, unless the request names that connector in `deferConnectors` ([`connector-authorization-and-external-memory-design.md`, "CA-4: Link check"](connector-authorization-and-external-memory-design.md#ca-4-link-check)). |
| `checkpointPolicy` | The effective checkpoint policy of a session's runtime and pool, which [`incremental-checkpoint-design.md`, "IC-8: Checkpoint policy, interval, and change tracker"](incremental-checkpoint-design.md#ic-8-checkpoint-policy-interval-and-change-tracker) adds. Under `checkpointPolicy: none` a session never has a workspace snapshot. |
| Pack reference | How [`incremental-checkpoint-design.md`, "IC-5: Restore, derive, download, and seal"](incremental-checkpoint-design.md#ic-5-restore-derive-download-and-seal) stores a derive's starting snapshot: a snapshot row that references the source snapshot's packs, the stored blocks of workspace content, and copies no object. The tenant is charged once for each pack, when its checkpoint confirms it. |
| Playground caps | The limits [§27.6](../spec/27_web-playground.md#276-session-lifecycle-and-cleanup) places on sessions a web playground caller creates. |
| VCS pool | A credential pool for git hosts, separate from the pools of warm pods above, that names repository prefixes and a provider credential ([`user-git-identity-design.md`](user-git-identity-design.md)). Its repository allowlist names the repositories sessions can use, and `allowWrite` lets them push. `userAuthorization: required` makes each repository need the user's own access at the provider, and `prAuthoring: user` has pull requests authored as the user. |
| Writable entries and `allowedRefPatterns` | A plan's `writableRepositories` names the repositories a session may push to, and `allowedRefPatterns` on an entry limits the branches it may push. `netagent`, the egress proxy in each agent pod, enforces both. |
| Storage counter | The tenant's storage quota counter. `Reserve` charges it, and `cataloging.DeleteBySession` releases the bytes of a session's live rows. |

### Labels from other designs

Proposals in other designs carry a label whose prefix names the design. This document cites them by label and links each citation to its definition.

| Labels | Design | What they name |
| :-- | :-- | :-- |
| DR-3, DR-4, DR-6, DR-9, DR-11, and DR-12 | [`tenant-derived-runtimes-design.md`](tenant-derived-runtimes-design.md#proposals-and-dependencies) | Proposals of the derived-runtimes design: [DR-3](tenant-derived-runtimes-design.md#dr-1-to-dr-3-prerequisites) setup command identity, which runs setup commands at the agent's UID through the runtime supervisor; [DR-4](tenant-derived-runtimes-design.md#dr-4-tenant-runtime-records) tenant runtime records; [DR-6](tenant-derived-runtimes-design.md#dr-6-base-changes) base changes, including retirement; [DR-9](tenant-derived-runtimes-design.md#dr-9-default-git-clones) default git clones; [DR-11](tenant-derived-runtimes-design.md#warm-clones) warm clones; and [DR-12](tenant-derived-runtimes-design.md#dr-12-repository-folders-and-the-working-directory) repository folders, which adds a plan's `workingDirectory` and a setup command's `cwd`. |
| SO-2 | [`security-oversight-design.md`](security-oversight-design.md#so-2-tenant-security-record-and-posture) | The tenant security record, which holds a tenant's guardrails and delays any change that weakens one. |
| EG-3 | [`egress-and-git-design.md`, "Proposal split"](egress-and-git-design.md#proposal-split) | The egress design's pod git path. |
| RT-2, RT-3, RT-4, and D11 | [`runtime-routing-design.md`](runtime-routing-design.md) | Proposals and a decision of the runtime routing design: [RT-2](runtime-routing-design.md#rt-2-the-interceptor-contract) the `PreRoute` payload, the single chain, and the post-chain point; [RT-3](runtime-routing-design.md#rt-3-routing-labels) routing labels; [RT-4](runtime-routing-design.md#rt-4-runtime-routes) runtime routes; and [D11](runtime-routing-design.md#decisions), the decision that retires `evalRef` and runs a derive through the `PreRoute` chain with `kind: derive`, with its own checks after the chain. |
| RV-2 and RV-6 | [`runtime-revisions-design.md`](runtime-revisions-design.md) | Proposals of the runtime revisions design: [RV-2](runtime-revisions-design.md#rv-2-selection) revision selection after the `PreRoute` chain, and [RV-6](runtime-revisions-design.md#rv-6-sessions-over-time) the revision rules for resume, delegation, and derive. |
| CA-4 | [`connector-authorization-and-external-memory-design.md`, "CA-4: Link check"](connector-authorization-and-external-memory-design.md#ca-4-link-check) | The connector link check at session creation, including derive, and `deferConnectors`. |
| IC-5 and IC-8 | [`incremental-checkpoint-design.md`](incremental-checkpoint-design.md) | Proposals of the incremental checkpoint design: [IC-5](incremental-checkpoint-design.md#ic-5-restore-derive-download-and-seal) restore and derive by pack reference, and [IC-8](incremental-checkpoint-design.md#ic-8-checkpoint-policy-interval-and-change-tracker) the checkpoint policy, including `checkpointPolicy: none`. |

## High-level design

A Lenny session runs an agent in a pod against a workspace that a plan describes: files, git clones, and setup commands. A client sometimes wants a new session that starts from an earlier one, for example to run the same work on another runtime as a regression test, or to continue from a session's latest state. The API has two routes for this today, derive and replay. Both start the new session from the earlier session's final workspace, neither can start from the beginning, and replay's promise to repeat the earlier prompts is not implemented.

This design keeps one route, derive, with two starting points. `from: latest` starts from the earlier session's last saved workspace. `from: start` starts from the workspace the earlier session began with, rebuilt from its stored plan at the same git commits. The new session copies the earlier session's plan rather than composing a new one from the target runtime's defaults, and it passes the admission and security checks a new session passes. A derive is routed and placed on a runtime revision as a create is, so a runtime route or an interceptor can move it, and it runs on the revision its runtime's traffic gives it rather than on its source's revision. Lenny does not repeat the prompts itself: a read returns the messages the client sent to the earlier session, and the client decides whether to send them again. The main limit is that a derive reproduces the workspace alone. The image, what setup commands download, and the answers of external systems and models can differ, and a client that re-sends messages repeats the pushes and external calls those messages caused.

The rest of this section states each part in the design's terms, which "Terms" above defines. All of it is one proposal, SD-1.

**Request and modes (SD-1, "Request" and "What each mode starts from").** `POST /v1/sessions/{id}/derive` takes `from`, an optional target runtime, pool, and isolation profile, `additionalSetupCommands`, and `deferConnectors`, and the replay route goes. At create the gateway copies the source's stored plan, `env`, and `runtimeOptions` to the new row, so nothing composes. It also copies the source's `deferConnectors` list when the request omits the field, and a request that carries the field, an empty list included, replaces that list. Under `from: latest` the adapter restores the source's snapshot and fetches none of the plan's sources. Under `from: start` the plan's sources materialize at each `resolvedCommitSha`, with the plan's uploads copied to the new session. Setup commands run again in both modes.

**Additional setup commands and writes (SD-1, "Additional setup commands" and "Writes").** A client can append setup commands, such as one that creates a branch for the run, under the target's `setupCommandPolicy`. A later derive does not copy them. Repository writes, meaning pushes and pull requests, stay on, and each writable entry is checked again at bind, so an entry that no longer passes reads as read-only. The derive response lists such an entry in `readOnlyDefaultClones` when a default clone added it, and in `readOnlyRepositories` otherwise.

**Reading client messages (SD-1).** `GET /v1/sessions/{id}/messages?from=client` returns the client's messages in order. The transcript gains a sender and a delivery state on each row, and the REST and Model Context Protocol (MCP) send paths record the client's messages.

**Refusals and checks (SD-1, "What it refuses" and "Checks").** A derive runs the `PreRoute` chain with `kind: derive`, and then the checks a new session with the copied plan would meet, on the new session's user and environment and on the runtime, revision, and pool the chain and revision selection chose. A refused check stops the derive before any pod claim or copy. One storage reservation covers the whole derive. Each stored setup command carries an `origin`, so an operator's command keeps its policy exemption only where the target's operator wrote the same command. Each also carries the `cwd` that composition wrote, so a derive does not move a command to another folder.

**Stops and supervisors (SD-1, "Stops" and "Supervisors").** A plan records the tenant-derived runtimes whose content it holds, with counters that rise at each content stop. A derive is refused once a stop follows the plan, through any chain of derives. A derive onto another runtime is refused when the chosen revision of the target has no runtime supervisor, and a derive that stays on the source's runtime counts as onto another runtime when its chosen revision differs from the source's recorded revision.

```
 Client ──POST /derive {from}──> Gateway: PreRoute chain (kind: derive), revision selection,
                                   │ post-chain checks, content stop check, storage reservation
                                   │ copies the stored plan, env, and runtimeOptions
                                   │ from: latest: also the snapshot
                                   │ from: start:  also the plan's uploads
                                   ▼
                          Derived session on the target runtime
 Client ──GET /messages?from=client──> source's client messages
 Client ──(re-sends them, if it chooses)──> derived session
```

## Requirements

- R1. A derive names its starting point: `latest` or `start`.
- R2. A derive composes nothing from the target runtime. The target contributes its image, its process, and its pods.
- R3. A check that would refuse a new session with this plan refuses the derive, before a pod is claimed.
- R4. A derive does not bring back content that a stop has ended.
- R5. A client can read a session's client messages in order, and it decides what to do with them.
- R6. A client can add setup commands to run after the source plan's.

## Security properties

- SD-P1. The new session takes the source's owner, as [§10.2](../spec/10_gateway-internals.md#102-authentication) **Session ownership.** states, and its credentials, meaning credential leases and the credential bindings it receives, are evaluated afresh rather than inherited. It takes the source's plan, `env`, and `runtimeOptions`, and what [`tenant-scoping-identity-design.md`, "TS-3: Session environment context and cross-environment sharing"](tenant-scoping-identity-design.md#ts-3-session-environment-context-and-cross-environment-sharing) states a derive takes: its environment, `egressLockHosts`, `crossed_from_environment`, and its delegation limit.
- SD-P2. Every check runs against the plan on the new session's user and environment, and on the final runtime, its chosen revision, and the final pool that the post-chain point sees.
- SD-P3. A plan carrying content from a tenant-derived runtime is refused after any later content stop on that runtime's grant entry or its tenant's security record, through any chain of derives, whether or not the content is still on the runtime.
- SD-P4. A derive onto another runtime is refused when the chosen revision of the target has no runtime supervisor. A derive that stays on the source's runtime is exempt only when its chosen revision equals the source's recorded `runtimeRevision`, and one that lands on a different revision is treated as a runtime change. The refusal holds because a file in the session's tree would reach an operator's setup command at the adapter's identity. For example, the source's agent can leave a `requirements.txt`, and an operator's default command on the target, such as `pip install -r requirements.txt`, would install what it names at the adapter's UID, with the pod's service account token. A sidecar target has a supervisor, so SD-P4 admits it, but until [DR-3](tenant-derived-runtimes-design.md#dr-1-to-dr-3-prerequisites) moves setup commands to the agent's UID through the runtime supervisor, setup there still runs at the adapter's UID, and the same file reaches it.

## Current state

This section records what the tree does today. Function and file names are the code's own, under `pkg/gateway/sessionserver` unless a path says otherwise.

- `handleDerive` (`pkg/gateway/sessionserver/derive.go`) copies the snapshot and writes a row with no plan, and one binds at finalize, which composes the target's defaults. [§7.1](../spec/07_session-lifecycle.md#71-normal-flow) **Derive restore semantics:** restores the snapshot at first bind and applies the plan's sources over it.
- `handleReplay` (`replay.go`) does the same, checks `executionMode` through `requireReplayRuntimeCompatible`, and copies the transcript through `CopySessionTx`, of every role. In `sessionserver`, the transcript store is read by the GET routes, `taskrecord.go`, and `usage.go`.
- A snapshot archives the whole workspace, and `ArchiveTree` excludes no `.git`.
- A session row stores its composed plan, with `resolvedCommitSha` on each `gitClone` source. `workspaceplan.Plan` holds `SchemaVersion`, `Sources`, and `SetupCommands`. `Env` and `RuntimeOptions` are fields on the row.
- `resolveFinalizePlan` composes when a row holds no plan, and a session with no client plan on a runtime with no defaults stores none, so an absent plan does not tell a session that ran from one that never bound.
- §14 binds an `uploadRef` to its session, and `UploadDefaultTTL` (`upload.go`) stamps 7 days.
- `recordDeliveredResponse` appends inbound messages after `Send`, best-effort, and `mcptools/send_delivery.go` and the child send path append none. `transcriptstore.Entry` records no sender, so a held message from a sibling agent, meaning another session's agent in the same delegation tree, reads as `user`, and `deriveMessageFrom` (`messages_list.go`) labels it as the client's. `projectMessage` sends text alone.
- `createAdmissionGates` holds the admission gates and `PLATFORM_DEGRADED`, `createAndStartGates` adds `requireTenantClassification`, and `validateAndBuildCreateRow` runs the `PreRoute` and `PostRoute` chains. `handleDerive` and `handleReplay` run none of those, and `handleDerive` makes two early policy calls of its own (F-15.1.78).
- `reserveDerivedSession` reserves the derived session's ID for its owner ahead of the snapshot copy, so the owner's erasure reaches what is copied under it. It does not touch the storage counter. `cataloging` is the blob store layer that records each stored object as a catalog row in Postgres, which tenant storage accounting reads. `cataloging.Copy` charges nothing to the tenant's storage counter, and `cataloging.DeleteBySession` releases the bytes of live rows, those not yet deleted, so undoing a copy lowers the counter.
- `resumeOnPod` takes the checkpoint branch whenever `WorkspaceSnapshot.Ref` is set, so a derived session resumed before its first checkpoint restores the copy with no first bind (F-7.3.36). It takes the rebuild path, which reads the plan, when no snapshot is set.
- F-14.1.37 means no `gitClone` works in a deployed image today, because the deployed gateway image has no `git` binary.

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
  "deferConnectors": ["<connector-id (optional)>"],
  "allowIsolationDowngrade": false,
  "ticketId": "<ticket (with allowIsolationDowngrade)>"
}
```

`allowStale: true` admits a live source, and the derive then uses the source's most recent successful checkpoint, which can lag the live workspace by up to the checkpoint interval. `ticketId` is an optional free-text justification that the gateway copies into the `derive.isolation_downgrade` audit event. `allowIsolationDowngrade` needs the `platform-admin` role, and no component checks the ticket. `deferConnectors` names the connectors the derived session runs without, as [CA-4](connector-authorization-and-external-memory-design.md#ca-4-link-check) defines it for every session creation path. When the request omits the field, the derived session copies the source session's stored list. When the request carries the field, its value replaces the source's list, and an empty list `[]` leaves the derived session with no deferred connector. A deferred connector that the chosen target runtime does not use is ignored.

`from` defaults to `latest`. `runtimeRef` defaults to the source's runtime. The final runtime, after the `PreRoute` chain, must have the source's `executionMode`, and the isolation monotonicity rule stays as §15.1 states it, applied to the final pool. "Routing and revision" under "Checks" states how the chain and revision selection treat a derive. A `from: start` of a source in any state needs `session.Validate`'s rule for the derive endpoint changed, because it refuses `created`, `finalizing`, and `ready` today, ahead of knowing the mode. The replay route, its `replayMode` field, and the `prompt_history` mode are removed. The platform has no deployments, so nothing needs a compatibility path.

### What each mode starts from

At create, the gateway copies the source's stored plan to the new row, with `env` and `runtimeOptions`, so the plan binds at create and composition never runs. Every bind stores a plan, the canonical empty plan where nothing was composed, so an absent plan means the session never bound, in any state. That is new on the paths that store nothing today: `composeRuntimeDefaults` on a create-and-start (`POST /v1/sessions/start`, which creates and starts a session in one call), and `composeChildWorkspaceDefaults` for a delegated child whose runtime has no defaults, which leaves the child's plan unchanged. Composition is skipped outright when the row's `derivedFrom` field is set.

| | `from: latest` | `from: start` |
| :-- | :-- | :-- |
| **Tree** | The snapshot. The adapter restores it, and `exclusiveBindRequest` and `slotBindRequest` pass the binder none of the plan's sources, so a clone's commit, local commits, and uncommitted changes, an edited default file, and a deleted path all stay as they were. | The plan's sources, at each `resolvedCommitSha`, with no snapshot. |
| **Source needs** | A snapshot, and `allowStale: true` when the source is live. A source whose effective `checkpointPolicy` is `none` has none ([IC-8](incremental-checkpoint-design.md#ic-8-checkpoint-policy-interval-and-change-tracker)). | A bound plan, in any state |
| **Uploads** | Dropped from the plan copy, because the snapshot holds their files, and `bindprepare.go` would otherwise fetch each one and fail once it has expired | Copied to the new session, with `uploadRef` rewritten, each reserving against the tenant's storage quota. An unreadable upload refuses. |
| **A resume before the first checkpoint** | Repeats the first bind, because `snapshotIsOwn` is still false | Takes the rebuild path, which reads the plan |

In both modes the plan stays on the row, because the new session's read checks, its readable set of repositories, the read tokens of the provisional binding, a warm repository's restore fetch, and a later derive all read it. Setup commands run at finalize in both modes, followed by `additionalSetupCommands`. A new pod lacks anything installed outside the workspace, so `from: latest` runs them too, over the restored tree. That can change the exact state, because `npm ci` resets `node_modules`, and a command that is unsafe to repeat can fail.

A client cannot add sources. A `finalize` whose body carries a plan answers `400 VALIDATION_ERROR` with `details.reason = "plan_already_set"`, as it does for any row with a bound plan. The derive response keeps its `uploadToken`, because `handleFinalize` runs `verifyUploadToken` first, and §14.1 leaves an upload no plan names unmaterialized, so the upload routes need no change. `claimDerivedRow` passes `Plan{}` today, which `slotBindRequest` consumes, so it passes the row's plan instead. Today `handleDerive` takes the row's lineage, `ParentWorkspaceRef`, and part of its response from the copied snapshot, `WorkspaceSnapshot`. A `from: start` derive copies no snapshot, so `handleDerive` records the lineage as `derivedFromSessionId` instead, and it does so in both modes.

A chain needs little handling. Each derive copies its source's plan, apart from additional commands, so a session with no checkpoint of its own, whose `snapshotIsOwn` is false, passes on what its parent had. A `from: start` of a session that itself came from `from: latest` is refused, because that session's start was a snapshot its own checkpoints have since replaced.

### Additional setup commands

`additionalSetupCommands` appends client commands to the new row's plan, after the source's. They have the origin `derive`, they count toward `maxCommands`, and they pass the target's `setupCommandPolicy`, and the `shell` setting of the target's `setupCommandPolicy` applies to them as to every command of the run. The plan copy of a later derive omits them, so a chain does not accumulate commands, a `from: latest` of the run does not repeat `checkout -b` on a tree that already has the branch, and a `from: start` of the run starts where the first session started. A resume or a rebuild of the same session reads them as any plan command. The usual use is to give a run its own branch before the client sends anything. They reach nothing a client's plan could not state.

A command can set `cwd`, under the checks of [DR-12](tenant-derived-runtimes-design.md#dr-12-repository-folders-and-the-working-directory). A command that sets none runs in the plan's working directory, as a client's command does, and the gateway writes that folder on the command as its `cwd` when it appends the command to the plan.

A command sets where the session starts, and it bounds nothing. An agent told to push `fix-login` can still push that branch from `ab/run-17`, within what the session's writable entries allow. A hard limit is `allowedRefPatterns` on the entry, and decision 3 under "Decisions" records that option. A repository the adapter checks out or restores is owned by UID 65532, the adapter's UID, until the pod git path, the egress design's proposal [EG-3](egress-and-git-design.md#proposal-split), resolves that, so git at the runtime's UID can refuse it as dubious ownership, git's refusal to work in a repository another user owns, until then.

### Writes

Writes stay on. The plan's `writableRepositories` and write scopes, which are `gitClone` sources whose `auth.leaseScope` is `vcs.<provider>.write`, copy to the new row, and each is read again at bind against the VCS pool's repository allowlist, the VCS pool's `allowWrite`, and, on a VCS pool with `userAuthorization: required`, the check of the user's own access. An entry that no longer passes reads as read-only rather than refusing the derive, because the derive request named no repository. An entry that a default clone added also follows the read-only rule under [`tenant-derived-runtimes-design.md`, "Write scope"](tenant-derived-runtimes-design.md#write-scope), which covers a target that reuses pods and a VCS pool that has since set `prAuthoring: user`.

The derive response lists each entry that reads as read-only, in one of two fields. An entry that a default clone added goes in `readOnlyDefaultClones`, with the clone's `path` and a reason and without the repository's name, as "Write scope" states for a new session, because the client never named that repository. Every other entry goes in `readOnlyRepositories`.

A client that re-sends a session's messages can therefore repeat its pushes and pull requests on the repository itself. That is the owner's stated choice, and `additionalSetupCommands` is how a run avoids a shared branch. Tool calls to other systems repeat in the same way.

### Reading client messages

`GET /v1/sessions/{id}/messages?from=client` returns the session's client messages in order, under the route's existing access rule. A filter on the existing route avoids a new one.

- `transcriptstore.Entry` gains a sender, with an explicit `client` value, so a sibling agent's held message is not returned, a writer that omits it is not read as the client, and `deriveMessageFrom` reads it.
- `recordDeliveredResponse` appends inbound rows before `Send`, with a delivery state, so a failed turn's message is on the record, and `toMessageNode`, which reports every row as `delivered` today, reads that state. `deliverHeldEntry` appends a held message once across its retries.
- The MCP send paths append client rows too, so a session driven through them has messages to read.

The read returns text, because `projectMessage` sends text alone. The transcript's retention, [§12.8](../spec/12_storage-architecture.md#128-compliance-interfaces) erasure, [§12.9](../spec/12_storage-architecture.md#129-data-classification) classification, and legal holds apply with no new rule, and the read returns nothing once the retention sweep has removed the rows. `POST /v1/sessions/{id}/extend-retention` lengthens that window. A client SDK or CLI helper can re-send them in a loop, and the design requires none.

This closes F-15.1.77 by removing §15.1's statement. A client that re-sends messages is an ordinary client, so delivery, duplicates, and interrupts behave as they do for any session.

### What it refuses

The gateway answers `422 DERIVE_SOURCE_UNAVAILABLE` with `details.reason`, before any claim, apart from `no_snapshot`, which keeps today's `400 VALIDATION_ERROR` under §7.1. The reasons that read the target runtime or pool are decided at the post-chain point, and when the `PreRoute` chain changed the runtime or pool, the refusal names the last modifier in `details.interceptor_ref`:

| Reason | Cause |
| :-- | :-- |
| `no_snapshot` | `from: latest`, and the source has no resolvable snapshot. |
| `checkpointing_disabled` | `from: latest`, and the source's effective `checkpointPolicy` is `none`, so it never had a snapshot. [IC-8](incremental-checkpoint-design.md#ic-8-checkpoint-policy-interval-and-change-tracker) adds this reason, and a `from: start` of the same source still rebuilds from the plan. |
| `not_bound` | `from: start`, and the source has no plan, so it never bound, whatever its state. |
| `upload_unavailable` | `from: start`, and the plan names an upload that cannot be read, because its TTL passed or the retention sweep removed it. |
| `start_was_snapshot` | `from: start`, and the source itself came from `from: latest`. |
| `delegated_source` | `from: start`, and the source has a delegation parent, because its exports and inherited inputs came from that parent, and a derive is a new root. |
| `source_runtime_stopped` | "Stops" below applies. |
| `target_unsupervised` | The final runtime differs from the source's runtime, or it is the source's runtime and the chosen revision differs from the source's recorded `runtimeRevision`, and the chosen revision has no runtime supervisor ("Supervisors"). |

A commit the host no longer serves is not known before the fetch, because a pinned commit skips `ls-refs`, git's request that lists a repository's refs. It fails at materialization with `SESSION_CREATION_FAILED`, after the claim, under `from: start`, and under `from: latest` when a warm repository's restore needs it.

### Checks

The plan passed its checks when the source bound it, and those checks can have changed. At the post-chain point of [RT-2](runtime-routing-design.md#post-chain-point), after the `PreRoute` chain and revision selection and before the claim, the gateway runs the checks a client's plan gets, on the final runtime and pool and the chosen revision. No single function does that today: `resolveSubmittedPlan` takes raw JSON, `Parse` refuses `resolvedCommitSha`, `validatePlanUploadRefs` needs a session, and `enforceSetupCommandPolicy` and the default-clone run of [DR-9](tenant-derived-runtimes-design.md#dr-9-default-git-clones) are separate steps. SD-1 extracts one function over a parsed plan, which every bind path calls. It covers the auth bindings, under which each `gitClone` source with an `auth` block maps its host to exactly one of the tenant's VCS pools whose provider matches its `leaseScope`, the named credential bindings, `egress.allowedHosts`, and `gitClone` sources under the environment, the rule for pods with no per-session policy, the check of the user's link on a VCS pool with `userAuthorization: required`, and `verbatimClientFiles`.

**Admission.** A derive passes the admission steps a create passes, judged against the derive's target runtime and pool, apart from the steps the list below leaves out. Each step that reads the runtime, its revision, or the pool runs at the post-chain point on what the chain and revision selection chose. On a session row, `Pool` is the pool name the client gave at create, and `PoolRef` is the pool the gateway resolved and scheduled the session on.

- **Pool and profile.** `handleDerive` passes `targetPool` as the create's `pool`, or the source's client-named `Pool` when the derive stays on the source's runtime and profile, and otherwise none, so the gates and the claim resolve a pool as a create does. It passes the derive's resolved target as the create's `isolationProfile`, so a weaker `targetPool` cannot pass the downgrade check, and a refusal's `details.field` names the derive's field. The `source.PoolRef` fallback goes, because the row defaults to it while `claimAtCreate` resolves `Pool`, so checks could judge a pool the claim does not use, and it names no pool of another runtime.
- **Gates.** It calls `createAdmissionGates` with the target, which gains `requireTenantClassification`, and it drops its own two early policy calls, which would otherwise run `PostAuth` twice.
- **Row build.** SD-1 extracts the row assembly, `validateRequestEnvelope`, `applyPlaygroundCaps`, `requirePoolNotDraining`, and the `PreRoute` and `PostRoute` chains from `validateAndBuildCreateRow` into one function that a create and a derive both call. It leaves out `composeRuntimeDefaults` and `enforceSetupCommandPolicy`, which sit in that same region today, because a derive runs neither and the second would check exempt commands. A deployer's interceptor that rejects a runtime therefore also rejects a `runtimeRef` naming it, and a playground caller's derive meets §27.6's caps, which `applyPlaygroundCaps` reads from the caller's principal. The drain gate runs once, on the final pool, at the post-chain point.
- **Rate limit.** The tenant view resolves the name first, ahead of `requireAdmissionRateLimit`'s `ResolvePool`, so a name from another tenant creates no rate-limit entry.

This closes F-15.1.78.

**Routing and revision.** A derive runs the single `PreRoute` chain of [RT-2](runtime-routing-design.md#rt-2-the-interceptor-contract) with `kind: derive`, as decision [D11](runtime-routing-design.md#decisions) of the routing design settles, and the route interceptor treats it as any creation. The payload starts as follows:

- `requested_runtime` is `runtimeRef`, or the source session's resolved runtime when the request omits it. A derive that omits `runtimeRef` therefore asks for the runtime its source ran, and a route keyed on the runtime the source first asked for does not move it.
- `requested_pool` is the pool that **Pool and profile.** above passes.
- `routing` is the source session's routing labels ([RT-3](runtime-routing-design.md#rt-3-routing-labels)), and the chain may change any key.
- `environment` is the source's environment, which the derive copies under SD-P1.
- `labels` is empty, because the derive request carries no client session labels and the derived session has none, as today.
- `input` is empty.

An interceptor's `MODIFY` may change the runtime, the pool, and the routing labels, so a runtime route can place a derive on another target. Revision selection ([RV-2](runtime-revisions-design.md#rv-2-selection)) then picks a revision of the final runtime by its traffic and the derive's rollout key, as for a create, and a derive does not inherit its source's revision ([RV-6](runtime-revisions-design.md#rv-6-sessions-over-time)). The gateway then runs every target check of this section, the content stop check of "Stops", and the supervisor check of "Supervisors" at the post-chain point, on the final runtime and pool and the chosen revision. A check that reads a versioned field, such as `deploymentModel` or the `workspaceDefaults` that the relabel under **Setup commands.** compares, reads the chosen revision. A failed check refuses the derive with its own code and names the last modifier when the chain changed the runtime or pool. The gateway never falls back to the requested runtime.

**Connector links.** The connector link check of [CA-4](connector-authorization-and-external-memory-design.md#ca-4-link-check) runs at the post-chain point, reads `capabilities.deferredToolDiscovery` from the chosen revision, and honours the derived session's `deferConnectors`: the request's list when the request carries the field, an empty list included, and the source session's stored list otherwise. A deferred connector that the chosen target runtime does not use is ignored. A missing link joins the combined missing-links response, `403 LINKS_REQUIRED`, which reports every missing link whichever kinds are missing, as the post-chain table of [RT-2](runtime-routing-design.md#post-chain-point) states.

**Order.** Every gate, the `PreRoute` chain, revision selection, the plan checks, and the content stop check of "Stops" run ahead of `reserveDerivedSession` and any copy, so a refusal there has nothing to undo. The `derive.isolation_downgrade` audit follows them, so those refusals record no downgrade. `cataloging.Copy` charges nothing today, and `DeleteBySession` releases live rows' bytes, so undoing a copy would lower the tenant's storage counter. One `Reserve` on the storage counter therefore covers the whole derive, ahead of the first copy, for `WorkspaceBytesUploaded`, the bytes of the snapshot's chunks that its checkpoint manifest records, under `from: latest`, or the upload objects' sizes under `from: start`, skipped when the limit is 0 or below. A refusal there answers `STORAGE_QUOTA_EXCEEDED` with nothing copied. Later failures in the copy, the claim, the mint of the derive's `uploadToken`, or the fence go through `undoDerivedWrites`. The fence is the coordinator-handoff check of [§10.1](../spec/10_gateway-internals.md#101-horizontal-scaling) on the insert, which skips the write when the source's `coordination_generation` has advanced since admission, as it does after a coordinator handoff. `undoDerivedWrites` releases what the `Reserve` took and what `DeleteBySession` did not. `DeleteBySession` returns an object count today, so it gains the bytes it freed.

Once [IC-5](incremental-checkpoint-design.md#ic-5-restore-derive-download-and-seal) lands, a `from: latest` derive writes a snapshot row that references the source snapshot's packs and copies no object, and the tenant was already charged for each pack when its checkpoint confirmed it ([`incremental-checkpoint-design.md`, "Quota"](incremental-checkpoint-design.md#quota)). The `Reserve` then covers the upload copies of `from: start` alone, a `from: latest` derive reserves nothing, and `undoDerivedWrites` deletes the reference row of a `from: latest` derive and releases only what a `from: start` derive reserved for its uploads.

**Setup commands.** §7.5 exempts operator-written defaults from `setupCommandPolicy`, and the stored plan does not say which commands those are. Each stored command therefore gains an `origin`: `operator`, `tenant`, `client`, or `derive`. On a derive that stays on the source's runtime and whose chosen revision equals the source's recorded `runtimeRevision`, an `operator` command stays exempt. On a derive onto another runtime, or onto the source's runtime at a different revision, in either mode, it keeps that origin only when its text is among the operator-written defaults of the target's chosen revision, and otherwise the plan copy relabels it `client`, so the target's effective policy checks it. Under either mode the tree can hold content a client or an agent chose, a plan file under `from: start` or an edited script under `from: latest`, so `npm ci` from another runtime's defaults would otherwise run client-chosen code past a policy the target's operator set. The relabel stays on the copy, so a later derive on that runtime cannot regain the exemption. The relabel changes which policy checks a command, and it leaves the command's folder unchanged. When a session's plan is composed, the gateway resolves each command's folder under [DR-12](tenant-derived-runtimes-design.md#dr-12-repository-folders-and-the-working-directory) and writes it on the stored command as an explicit `cwd`, a derive copies each command with its `cwd`, and `origin` decides no folder. For example, the platform runtime `web-dev`, which tenant `acme` uses, names `workingDirectory: app`, so its operator's default `npm ci` is stored with `cwd: app` and runs in `current/app`. When bob derives a session of `web-dev` onto `web-test`, whose operator-written defaults lack `npm ci`, the copy relabels the command `client`, `web-test`'s policy checks it, and it still runs in `current/app`. A `tenant` command is also refused on a target whose `setupCommandPolicy` is not mode `allowlist` with `shell: false`, as [DR-4](tenant-derived-runtimes-design.md#dr-4-tenant-runtime-records) requires, because an inert string would otherwise run under a shell. Under `shell: false` a command runs through `exec` as an argument list, so pipes, substitutions, and redirects in it are inert text ([§7.5](../spec/07_session-lifecycle.md#75-setup-commands)), and DR-4 admits tenant-written commands only on such a base. The source's original commands are the only ones that run, plus any the request adds, and the check decides whether the target admits them.

**Warm stores on the target.** A target's pods can hold warm stores for repositories the plan does not name, from a cache-only entry, a private platform entry, or a warm default clone the stored plan omits, under [`tenant-derived-runtimes-design.md`, "Warm clones"](tenant-derived-runtimes-design.md#warm-clones), and the agent can read them. Ahead of `reserveDerivedSession` and any copy, the gateway runs the read checks on each tenant entry and each private platform entry of the target pool's warm list for the chosen revision that the plan does not name, because the warm list is kept per pool and revision ([RV-3](runtime-revisions-design.md#rv-3-pods)), as that design's ["Binding"](tenant-derived-runtimes-design.md#binding) states for a derive. `coveredStores` includes each entry whose checks pass. For the new session each such entry is cache-only, so a failed check withholds its store through `withheldStores` and the derive proceeds, and `GET /v1/sessions/{id}` lists the entry in `withheldWarmEntries` without its URL, as **Withheld stores.** in that section states. On an SDK-warm target, a failed check refuses the derive.

### Stops

A stored plan can hold content a tenant has since stopped, and a grant flag set false or a forbid is "the stop after a compromise". A derive is a new session, so it is refused. Reading the source's runtime alone would not hold, because a derive onto a platform runtime before a forbid leaves a row whose runtime is that platform runtime. A plan therefore carries `planOriginRuntimes`, the tenant-derived runtimes whose defaults it holds. A derive copies its source's set at create, and it adds nothing, because it composes nothing. A session that composes adds its runtime at bind, in the update that binds the plan, and a delegated child copies its parent's at bind, because a child's exports came from that parent.

A runtime's current state would not hold either. If a `security-admin` sets a forbid or an operator sets a grant entry's `allowGitClones` to false after the tenant has removed the clone, the runtime looks clean, and a derive of a session from before the incident would rerun the pinned commit. Two counters therefore record stops, in a tenant-keyed `content_stop_epochs` table, outside the grant's and the security record's bodies, because a `PUT` replaces each of those documents and [SO-2](security-oversight-design.md#so-2-tenant-security-record-and-posture) stores the record as versions, so a pending weakening written before a forbid would otherwise regress a counter kept inside it. An absent row reads as 0. Each is incremented in the stop's own transaction: the grant entry's, keyed by base, when `allowWorkspaceFiles`, `allowSetupCommands`, or `allowGitClones` is set false, and the security record's, in the transaction that activates a version adding `all`, `setupCommands`, `workspaceFiles`, `gitClones`, or `floatingCloneRefs`, which forbids default clones at a branch or tag rather than a full commit, to `derivedRuntimes.forbid`, or setting `verbatimClientFiles: forbid`. Each `planOriginRuntimes` entry stores both values, from the same read of the tenant view that composition used.

The content stop check is one function, which `handleDerive` calls at create and finalize calls again, because a row created before a stop can finalize after it. For each entry, the tenant view must resolve that runtime now, it must not be retired, and both stored values must equal the current ones. A deleted runtime refuses, and names stay consumed, so one cannot be recreated to pass. A runtime that is `incompatible` for another reason, such as a `Registered=False` condition, does not refuse. A plan from before a stop stays refused after the stop is lifted, because a counter only rises. The owner can still read such a workspace through `GET /v1/sessions/{id}/workspace`.

A platform-owned source runtime has no such rule, because starting the session of a platform runtime that [DR-6](tenant-derived-runtimes-design.md#dr-6-base-changes) has retired on a successor runtime is a regression test.

### Supervisors

SD-P4 refuses a derive onto another runtime when the chosen revision of the target has no supervisor, in both modes, and a derive onto the source's runtime whose chosen revision differs from the source's recorded `runtimeRevision` counts as onto another runtime, whatever the source and the plan's origins. The target is the final runtime after the `PreRoute` chain, so a derive that a route or an interceptor moves onto another runtime meets the check, and the check runs at the post-chain point. An agent on the source can leave a `requirements.txt` that an operator's command on the target would run at the adapter's UID, with the service account token. `runsSupervisor` is an unexported controller function over the `Runtime` custom resource definition (CRD), so the gateway uses a new helper that reads `type` from the runtime row and `deploymentModel` from the chosen revision, merged with a derived runtime's own fields, as [RV-2](runtime-revisions-design.md#rv-2-selection) states for every check at the post-chain point. `type` is fixed under [`runtime-revisions-design.md`, "Field classes"](runtime-revisions-design.md#field-classes), and `deploymentModel` is versioned. [DR-4](tenant-derived-runtimes-design.md#dr-4-tenant-runtime-records) mirrors both. It requires the runtime row's `registered` field to be true and fails closed, because DR-4's mirror, which copies the `Runtime` resource's fields into the runtime row, stores an empty value as `sidecar` and leaves the row stale when it refuses a resource.

A derive that stays on the source's runtime and lands on the source's recorded revision is not refused. On a pod with no supervisor, the source's agent already runs as the adapter process ([§4.7.10](../spec/04_system-components.md#4710-deployment-model)), so a kept file gives it nothing new. A different revision of the same runtime can carry different operator setup commands and a different `deploymentModel`, so a file the source's agent left can reach a command it never reached on the source, and such a derive meets the check. Until [DR-3](tenant-derived-runtimes-design.md#dr-1-to-dr-3-prerequisites) lands, setup on a sidecar pod runs at the adapter's UID and a restored file reaches it, which F-7.5.17 records.

## What an exact start does not cover

`from: start` matches what the plan states, and `from: latest` matches what the snapshot holds. These differ, and the documentation says so:

- **The image.** A target runtime's image is the variable under test. On the source's own runtime, the derive's revision comes from the runtime's traffic and can differ from the source's revision.
- **What setup commands fetch.** `npm install` resolves against the registry on the day it runs.
- **External answers.** Tool calls and model responses are not recorded inputs.
- **Egress and credentials.** These are the tenant's and the pool's settings on the day of the derive.
- **A target that expects its own defaults.** Nothing composes, so a target relying on a default clone or a setup command it added runs without it.
- **`from: latest`'s moment.** A snapshot records its own moment, and on a live source that is the last checkpoint. The session files under `/sessions`, the per-session directory outside the workspace where the runtime keeps its own session state, are not restored, as §7.1 states.
- **Messages the read lacks.** A lost append, and attachments.

## Costs

- A client that wants a re-run writes the loop, or uses a helper, and it stays attached for the run.
- A client that re-sends messages repeats pushes and external calls on real systems.
- An additional command is not repeated by a later derive, so a package it installed outside the workspace is missing under `from: latest` of that run.
- A derive cannot add files, so a client that needs them uses an additional command. Today's derive can add them at finalize.
- A tenant's later change to a runtime reaches no derived session, including a security fix to a default file on a platform runtime that a tenant did not stop.
- A derive cannot name a revision ([`runtime-revisions-design.md`](runtime-revisions-design.md), D2). A regression test against a new revision of the same runtime runs once the rollout gives the derive's rollout key that revision, and a test against a fixed version names a runtime registered for it.
- A derive that names a runtime a route matches can run on another of the route's targets. A client that needs a specific runtime names one that no route matches.
- A source that used uploads cannot be derived `from: start` once they are gone, 7 days after each upload, or sooner under retention, which is 24 hours by default on a workspace classified T4 (Restricted) under §12.9.
- A content stop refuses every earlier plan under that grant entry or tenant, on any runtime and whether or not that runtime held the stopped content, and it stays refused after the stop is lifted.

## Defects found while verifying

| Finding | Severity | Defect |
| :-- | :-- | :-- |
| F-15.1.76 | Medium | `DeriveRequest.RuntimeRef` is in no specification section, and `handleDerive` runs no `executionMode` check or supervisor check on it. |
| F-15.1.77 | High | Replay delivers no prompts, and the MCP send paths record none. |
| F-15.1.78 | Medium | `handleReplay` and `handleDerive` skip the admission checks a create runs. |
| F-7.3.36 | Medium | Already filed. A derived session resumed before its first checkpoint restores the copy with no first bind. |

## Specification sections affected

- §15.1: the derive row and the precondition table gain `from`, `runtimeRef`, `additionalSetupCommands`, and `deferConnectors`, the derive refusal gains the `checkpointing_disabled` reason of [IC-8](incremental-checkpoint-design.md#ic-8-checkpoint-policy-interval-and-change-tracker), and the derive response gains `readOnlyRepositories` and `readOnlyDefaultClones`. The replay row and **Session Replay Semantics** go, with a successor pointer to the derive row. The messages route gains `from=client`. The error table gains `DERIVE_SOURCE_UNAVAILABLE`, and `REPLAY_ON_LIVE_SESSION` goes. The derive row lists `LINKS_REQUIRED`, the code of the combined missing-links response that CA-4 adds to the error table, among its refusals, and states that an omitted `deferConnectors` copies the source's list.
- §7.1 **Derive session semantics** and **Derive restore semantics:**: a derive runs the `PreRoute` chain with `kind: derive`, revision selection, and the post-chain point that [`runtime-routing-design.md`](runtime-routing-design.md#specification-changes) and [`runtime-revisions-design.md`](runtime-revisions-design.md#specification-changes) state in §7.1 for every creation path, and its target checks run at that point. The source's states and the rules for a bound plan, and `from: latest` materializes no source, and a resume with no checkpoint of the session's own repeats the first bind. `from: start` carries no snapshot, so "the only kind of session whose record carries a workspace snapshot before its first bind" narrows to `from: latest`.
- §7.5 **Command policy:**: `origin`, and how a derive reads it.
- §4.5 **Workspace lineage:**, §10.2, §11, §13, §18, and §23: the mentions of replay.
- §14 and §14.1 **Plan binding.**: a derive binds at create from the stored plan, step 1 of **Runtime workspace defaults.** does not run for it, a setup command carries `origin`, and a derive copies each setup command's `cwd`.
- §15.4, [**`MessageEnvelope` — Unified Message Format**](../spec/15_external-api-surface.md#messageenvelope--unified-message-format): the sender and the delivery state on a `session_messages` row.

## Stored formats and wire contracts this forces

| Carrier | Change |
| :-- | :-- |
| `POST /v1/sessions/{id}/derive` | `from`, `runtimeRef`, `additionalSetupCommands`, and `deferConnectors`. The response gains `readOnlyRepositories`, which names each read-only entry the client's plan stated, and `readOnlyDefaultClones`, which lists each read-only entry a default clone added by its `path` and a reason, as [DR-9](tenant-derived-runtimes-design.md#write-scope) defines it. |
| `POST /v1/sessions/{id}/replay` | Removed |
| `GET /v1/sessions/{id}/messages` | `from=client` |
| OpenAPI | `pkg/gateway/externalapi/openapi/openapi.json` for each of these |
| Setup command | `origin`, gateway-written and refused from a client, in the stored plan, `schemas/workspaceplan-v1.json`, `lenny-adapter.proto`, and the mapping in `podsession/workspaceplan.go`. A derive copies each command's `cwd`, which [DR-12](tenant-derived-runtimes-design.md#dr-12-repository-folders-and-the-working-directory) has the gateway write at composition, and the gateway writes a `cwd` on each `additionalSetupCommands` entry that sets none |
| Session row | `derivedFrom` (`latest` or `start`), `derivedFromSessionId`, `snapshotIsOwn`, written by the checkpointer once the session's own checkpoint has written its snapshot, and `planOriginRuntimes` with each entry's two stored values |
| `session_messages` | A sender on each row, `client` for a client, and a delivery state |
| `content_stop_epochs` | One row per tenant for its security record and one per grant entry's base |
| Upload store | A copy of an upload under the new session, with a new `uploadRef` |

## Proposals and dependencies

| Proposal | Contents | Depends on |
| :-- | :-- | :-- |
| SD-1 | Everything above, closing F-15.1.76, F-15.1.77, F-15.1.78, and F-7.3.36 | None for a plan with no clone. A plan with a clone needs the [egress design](egress-and-git-design.md)'s pod-side clone, as any `gitClone` does. SD-P4 on a sidecar target needs [DR-3](tenant-derived-runtimes-design.md#dr-1-to-dr-3-prerequisites). "Warm stores on the target", `coveredStores`, and `withheldStores` arrive with [DR-11](tenant-derived-runtimes-design.md#warm-clones), and "Stops" with [DR-4](tenant-derived-runtimes-design.md#dr-4-tenant-runtime-records), [DR-6](tenant-derived-runtimes-design.md#dr-6-base-changes), and [SO-2](security-oversight-design.md#so-2-tenant-security-record-and-posture). SD-1 also extracts the plan check function that [DR-9](tenant-derived-runtimes-design.md#dr-9-default-git-clones) calls. The `PreRoute` payload with `kind: derive` comes from the first proposal of [`runtime-routing-design.md`](runtime-routing-design.md#proposal-split-and-order): if SD-1 lands first, that proposal adds `kind: derive` to the derive path, and otherwise SD-1 adopts the payload it landed. Revision selection arrives with [`runtime-revisions-design.md`](runtime-revisions-design.md#proposal-split-and-order), the connector link check and `deferConnectors` with [CA-4](connector-authorization-and-external-memory-design.md#ca-4-link-check), and the pack reference and `checkpointing_disabled` with [IC-5](incremental-checkpoint-design.md#ic-5-restore-derive-download-and-seal) and [IC-8](incremental-checkpoint-design.md#ic-8-checkpoint-policy-interval-and-change-tracker). |

It can land before those designs, and each adds its rule to the checks when it lands.

## Documentation

| Page | Change |
| :-- | :-- |
| `docs/tutorials/session-derive-replay.md` and `docs/client-guide/session-lifecycle.md` | One route and two modes, how to re-send messages, how to give a run its own branch, how routes and revisions place a derive, and "What an exact start does not cover". [`runtime-routing-design.md`](runtime-routing-design.md#rt-7-documentation) deletes `docs/tutorials/evaluation-scoring.md`. |
| `docs/api/rest.md`, `docs/api/mcp.md`, `docs/client-guide/wire-format.md`, `docs/client-guide/sdk-examples/curl.md`, `docs/reference/glossary.md`, and `docs/reference/error-catalog.md` | The request, the read, and `DERIVE_SOURCE_UNAVAILABLE`. |
| `docs/about/comparisons.md` | Removes the replay modes. |

## Tests

### Tier 1: unit

- The plan copy keeps each `resolvedCommitSha` and composes nothing from the target. A bind with nothing composed stores the empty plan.
- An `operator` command stays exempt on the source's runtime at the source's recorded revision, and on another runtime or revision whose defaults hold that text, and it is relabelled `client` and checked otherwise, in both modes, including on a derive onto the source's runtime that lands on a different revision whose defaults lack that text, and a later derive on that runtime keeps the relabel. A relabelled command keeps the `cwd` its source's composition wrote. An `additionalSetupCommands` entry with no `cwd` is stored with the plan's working directory as its `cwd`, and one that sets a `cwd` keeps it. `tenant`, `client`, and `derive` commands are checked. A `tenant` command is refused on a target without `allowlist` and `shell: false`. A client's `origin` is refused.
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
- `from: latest` of a source whose effective `checkpointPolicy` is `none` refuses with `checkpointing_disabled`, and `from: start` of it passes. A derive onto a runtime with `deferredToolDiscovery: false` whose user lacks a connector link refuses with `403 LINKS_REQUIRED`, carrying the connector under `details.connectors` and an empty `details.userLinks`, and passes when the request names the connector in `deferConnectors`. A derive that omits `deferConnectors` copies the source's list and passes for a connector the source deferred, a derive that sends `[]` clears it and refuses, and a deferred connector that the target runtime does not use is ignored.
- With the pack reference of IC-5, a `from: latest` derive reserves no storage and copies no object, a `from: start` derive reserves for its uploads, and a failure after the reference row is written leaves the storage counter where it started.
- A derive meets each create gate once and the `env` blocklist, a playground caller's derive meets its caps, the pool the gates judge is the one the claim uses, a same-runtime derive with an `operator` command passes the row build, a `PreRoute` REJECT on the target refuses it, and a name from another tenant creates no rate-limit entry. The `PreRoute` payload of a derive carries `kind: derive`, the source's routing labels, and the source's environment. A route on the requested runtime moves a derive to its target, an interceptor that changes the runtime or pool moves it, and the moved derive meets the target checks, the supervisor check, and the content stop check on the final runtime and pool, with a refusal naming the last modifier. A derive that omits `runtimeRef` asks for the source's resolved runtime, which a route keyed on the runtime the source first asked for does not move. A derive selects a revision by its runtime's traffic, including a derive onto the source's own runtime during a rollout, and a check that reads `deploymentModel` reads the chosen revision. A derive refused by a gate or a check copies nothing and records no downgrade. A derive over the storage quota answers `STORAGE_QUOTA_EXCEEDED` with nothing copied. A failure after the copy leaves the storage counter where it started, and a completed derive charges it once. `from: start` of a session cancelled before binding refuses with `not_bound`, and so does `from: start` of one that never reached a bind. `from: latest` of a create-and-start session or an unchanged child composes nothing. An upload copy past the tenant's storage quota refuses.
- Messages read back in order, including a failed turn's, marked undelivered, and one sent through the MCP send paths, and a retried held message appears once. The read returns nothing after retention.
- An additional command runs after the source's, is refused by the target's policy, reruns on a rebuild of the same session, and is absent from a later derive's plan, so a chain does not accumulate commands and a `from: latest` of the run does not fail on an existing branch.
- An additional command with no `cwd` runs in the plan's working directory. An `operator` command relabelled `client` on a derive onto another runtime runs in the folder its source's composition wrote, under both modes, and the target's `workingDirectory` does not move it.

### Tier 9: security

- A repository off the VCS pool's repository allowlist, a user who lost access on a VCS pool with `userAuthorization: required`, and an environment that denies the host each refuse before any claim, on both modes, with a kept tree included.
- A user who lost write access gets a read-only session, listed in `readOnlyRepositories`, and no write token. An entry a default clone added reads as read-only on a target that reuses pods, and when its VCS pool later set `prAuthoring: user`. The derive response lists that entry in `readOnlyDefaultClones` by its `path` and a reason, with no repository name, and leaves it out of `readOnlyRepositories`.
- A content forbid, a retirement, a set-false grant flag, and a deletion of a tenant-derived source runtime each refuse, including through a chain onto a platform runtime, a stop that arrives after the tenant removed the clone, and after the stop is lifted. A plan bound after the stop passes. `Registered=False`, a non-content forbid, and a tenant suspension raise nothing. A row created before a stop refuses at finalize. A pending weakening activated after a forbid does not lower the counter. A retired platform source runtime passes.
- A derive whose final runtime differs from the source's and whose chosen revision has no supervisor refuses in both modes, whether `runtimeRef` named that runtime or a route or an interceptor moved the derive there, and a target whose runtime row has `registered` false refuses. A derive onto the source's runtime that lands on the source's recorded revision passes on such a pod, and one that lands on a different revision with no supervisor refuses with `target_unsupervised`.
- A derive keeps the source's `egressLockHosts` and environment, and a caller who left that environment answers 403.
- A target pod holding a warm store for an entry the plan omits withholds that store for a user who cannot read it, and the derive proceeds. On an SDK-warm target the derive is refused.

### Tier 11: documentation

- Documented examples of the request pass the schema.

## Decisions

The owner settled decisions 1 to 5 as recommended on 2026-11-04, in the words "go with your recommendations", and settled decisions 6 and 7 as recommended on 2026-11-08, in the same words. The owner also settled one route with two modes, the exact start, the client sending messages, writes on, and setup commands running again on `from: latest`. The owner also moved `evalRef` to derive, which decision D11 of [`runtime-routing-design.md`](runtime-routing-design.md#decisions) later retired together with the rewrite refusal, and settled these earlier: an expired upload refuses, a source that started from a snapshot refuses `from: start`, and a content stop refuses earlier plans permanently.

| # | Decision | Recommendation | Cost of the recommendation | Alternative |
| :-- | :-- | :-- | :-- | :-- |
| 1 | Exempting setup commands | **Settled.** The owner accepted this narrower rule after a review, in the words "keep the narrowed rule". An `origin` on each stored command. An `operator` command stays exempt on the same runtime when the derive's chosen revision equals the source's recorded revision, and on another runtime or revision whose operator-written defaults hold that text. On 2026-11-09 the owner settled, in the word "ok", that a derive on the same runtime that lands on a different revision is treated as a runtime change for this exemption and for SD-P4 | An upgrade that changed an operator command depends on the new version's policy to run the old one, and a derive is refused otherwise | Exempt under `from: start` as the owner first accepted, which a client can use to run its own content past a target's policy |
| 2 | `additionalSetupCommands` | **Settled.** The request field the owner proposed, under the target's policy | A base whose allowlist omits `git` refuses it, and a command is a convention with no enforcement | Nothing added, which leaves a run on the source's branch |
| 3 | A hard limit on a run's pushes | **Settled.** None in the first version | An agent can still push outside the run's branch within the entry's patterns | A request field narrowing `allowedRefPatterns` on each writable entry, which `netagent` enforces. It is additive |
| 4 | Files on a derive | **Settled.** None. A client uses an additional command | A client that added files at finalize today needs a command to fetch them, under the target's policy and egress | Let a client add sources at finalize, which keeps the upload rules and the `path` rules, the workspace-relative `path` of each source and §14's **Path collision rule**, under which a later source wins a shared path, and makes the plan no longer the source's |
| 5 | The client messages read | **Settled.** `?from=client` on the existing messages route | The route's response keeps its other fields | A new route, as the owner first described, which is more explicit and adds a route |
| 6 | A setup command's folder on a derive | **Settled.** The gateway writes each command's folder on the stored command as an explicit `cwd` when the plan is composed, under [DR-12](tenant-derived-runtimes-design.md#dr-12-repository-folders-and-the-working-directory), and a derive copies it, so a relabel does not move a command. `origin` decides only which policy checks a command. An `additionalSetupCommands` entry with no `cwd` runs in the plan's working directory | Every stored command carries a `cwd`, and a target's own `workingDirectory` moves no copied command | Derive the folder from `origin` at each run, under which a relabel to `client` moves an operator's command to the plan's working directory, and "its own runtime" has no single reading on a derive |
| 7 | Which response field lists a read-only default clone on a derive | **Settled.** The derive response carries `readOnlyDefaultClones` for an entry a default clone added, with its `path` and a reason and without the repository's name, and `readOnlyRepositories` for every other entry | A client reads two fields to learn which writes a derive lost | List every entry in `readOnlyRepositories`, which names a repository the client never named |

## Review history

This section records the reviews of earlier revisions and what each revision changed. The current text above supersedes any statement here that differs from it. Review findings are ranked Critical, High, Major, or Minor, from most to least serious. A stop check is a review round that decides whether the previous round's changes need another full round.

### Revision notes

These notes were carried in the status line of earlier revisions.

- Revisions 2 to 7 apply the reviews of revisions 1 to 6, under their own sections. A stop check on revision 7 found no Critical, High, or Major issue, and revision 8 applies its Minors, which no review has read.
- Revision 9, dated 2026-11-07, followed a pass for a reader new to the project. It added "Terms", "Terms from other designs and the platform", "Labels from other designs", and "High-level design", linked each citation of another design and each label from another design to its definition, named the object behind each configuration value the text cites, and moved the revision notes and the review records into this section. It changed no rule.
- Revision 10, dated 2026-11-08, corrected the specification section cited for the sender on a `session_messages` row, which §12.4 does not hold, the name of the derive's check under "Stops", which shared "stop check" with the review rounds and is now the content stop check with its own term, and the `verbatimClientFiles` term, which read "client" as the API client, that a validation pass found, and changed no rule.
- Revision 11, dated 2026-11-08, applies decisions 6 and 7, which the owner settled that day in the words "go with your recommendations", and changes these rules. Under "Checks", **Setup commands.**, the gateway writes each setup command's folder on the stored command as an explicit `cwd` when the plan is composed, a derive copies that `cwd`, and `origin` decides only which policy checks a command, so the relabel of an `operator` command to `client` no longer moves it. Under "Additional setup commands", an entry with no `cwd` runs in the plan's working directory. Under "Writes", the derive response lists a read-only entry that a default clone added in `readOnlyDefaultClones`, by its `path` and a reason and without the repository's name, and every other read-only entry in `readOnlyRepositories`. "Terms", "Labels from other designs", "High-level design", "Specification sections affected", "Stored formats and wire contracts this forces", the Tier 1, Tier 4, and Tier 9 tests, and "Decisions" state both rules.
- Revision 12, dated 2026-11-08, updated **Warm stores on the target.**, the "Default clone, warm store, and `coveredStores`" row of "Terms", the SD-1 dependencies row, and the matching test to match the warm-list decisions of `tenant-derived-runtimes-design.md` revision 58, and changed no rule. A warm store comes from any entry on the pool's warm list, a cache-only entry and an operator's entry included, and serves any `gitClone` source whose URL matches. The read checks on a derive cover each tenant entry and private platform entry of the target pool's warm list that the plan does not name, and a failed check withholds the store through `withheldStores` in place of refusing the derive, except on an SDK-warm target. The row of "Terms" becomes "Default clone, warm store, `coveredStores`, and `withheldStores`".
- Revision 13, dated 2026-11-09, applies the cross-design sync review of 2026-11-08 and the reviewer instructions of `runtime-routing-design.md` revision 7 and `runtime-revisions-design.md` revision 3, and changes these rules. Under decision D11 of the routing design, a derive runs the `PreRoute` chain with `kind: derive`, starting from `runtimeRef` or the source's resolved runtime, the pool of **Pool and profile.**, the source's routing labels, and the source's environment, and a route or an interceptor can move it. The **Rewrites.** refusal is replaced by **Routing and revision.** under "Checks", and every target check, the content stop check, and the supervisor check run at the post-chain point on the final runtime and pool and the chosen revision, with a refusal naming the last modifier. A derive selects a revision by its runtime's traffic and does not inherit its source's revision, the supervisor check reads `deploymentModel` from the chosen revision, the relabel compares against the chosen revision's defaults, and the warm list read on the target is the one for the chosen revision. `evalRef` is removed from the request, the session row, the session list filter, and the §15.1 edits, together with the "Experiment" and `evalRef` terms, the `routeExperiment` mentions, and `docs/tutorials/evaluation-scoring.md` under "Documentation". The request gains `deferConnectors`, and the connector link check of CA-4 runs at the post-chain point. "What it refuses" gains `checkpointing_disabled` from IC-8 and states `target_unsupervised` on the final runtime. **Order.** states that under the pack reference of IC-5 a `from: latest` derive copies nothing, so the storage reservation and the undo cover the uploads of `from: start` alone. "High-level design", "What an exact start does not cover", and "Costs" state that a regression test cannot target a revision, and "Stops" names DR-6's retirement apart from a retired revision. "Terms from other designs and the platform", "Labels from other designs", "Specification sections affected", "Stored formats and wire contracts this forces", "Proposals and dependencies", and the Tier 4 and Tier 9 tests follow.
- Revision 14, dated 2026-11-09, applies the owner's decisions N1, N3, and N4 of that date, each settled in the owner's word "ok", and changes these rules. Under N1, a derive refused for a missing link receives the combined missing-links response with its own code, `403 LINKS_REQUIRED`, and **Connector links.**, the §15.1 item of "Specification sections affected", and the tier 4 test name it. Under N3, the same-runtime exemptions of SD-P4 and of decision 1's `operator` setup command exemption apply only when the derive's chosen revision equals the source's recorded `runtimeRevision`, and a derive on the same runtime that lands on a different revision is treated as a runtime change for both rules. "High-level design", SD-P4, the `target_unsupervised` reason, **Setup commands.**, "Supervisors", decision 1, and the tier 1 and tier 9 tests state it. Under N4, a derive that omits `deferConnectors` copies the source session's list, a derive that carries the field, `[]` included, replaces it, and a deferred connector that the chosen target runtime does not use is ignored. "High-level design", "Request", **Connector links.**, the §15.1 item, and the tier 4 test state it.

### Review of revision 1

An independent review read revision 1. It found no Critical or High issue, three Majors, and no path across tenants. Each was checked against the tree, and revision 2 applies them.

| Finding | Result | Check and outcome |
| :-- | :-- | :-- |
| Major, a derived session cannot finalize | Holds. | `handleFinalize` runs `verifyUploadToken` first. The token stays, which removes the upload refusal. |
| Major, additional commands carry into later derives | Holds. | A later `from: latest` would fail on `checkout -b`. The origin `derive` is dropped from a plan copy. |
| Major, the `operator` exemption crosses bases | Holds. Fix changed. | The reviewer proposed storing the base, which would refuse a v1 to v2 regression test. The exemption stands under `from: start`, whose tree is the plan's, and on the source's runtime, and an agent-written tree onto another runtime is checked. |
| Minors: the command's type and shell, `tenant` on a weak target, DR-3 under SD-P4, ownership of restored repositories, code assuming the old row, missing fields, the sender's default, delivery state, and stale text | Hold. | Applied. |

### Review of revision 2

A stop check read revision 2 and `tenant-derived-runtimes-design.md` revision 29. It found no Critical or High issue, two Majors, and no path across tenants. Each was checked against the tree, and revision 3 applies them.

| Finding | Result | Check and outcome |
| :-- | :-- | :-- |
| Major, the `operator` exemption can be evaded | Holds. Narrows a settled decision. | "The tree is the plan's" was wrong, because a client writes that plan's files. A chain also regained the exemption over an agent-written tree. The relabel closes both, and an unchanged command still passes. The owner accepted this over the broader rule first settled. |
| Major, `from: latest` still fetches the source's uploads | Holds. | `stageWorkspace` in `bindprepare.go` runs on the plan before `FinalizeWorkspace`, and it calls `Blobs.Get` on each `uploadFile`. The copy drops upload sources. |
| Minors: upload quota, and the other create gates | Hold. | Applied. |

### Review of revision 3

A stop check read revision 3 and `tenant-derived-runtimes-design.md` revision 29. It found no Critical or High issue, one Major, and no path across tenants. It was checked against the tree, and revision 4 applies it. The check also read each reader of a `from: latest` plan with its upload sources dropped, and found them sound.

| Finding | Result | Check and outcome |
| :-- | :-- | :-- |
| Major, a derive skips the `PreRoute` and `PostRoute` chains | Holds. | `validateAndBuildCreateRow` runs them, and `handleDerive` does not, so a rejected runtime was reachable through `runtimeRef`. A shared admission function replaces the list, and a rewrite refuses. |
| Minors: the order of the tenant view, the upload cost, and sessions that never bound | Hold. | Applied. A bind always stores a plan, which removes the empty-plan copy rule. |

### Review of revision 4

A stop check read revision 4 and `tenant-derived-runtimes-design.md` revision 29. It found no Critical or High issue, one Major, and no path across tenants. It was checked against the tree, and revision 5 applies it.

| Finding | Result | Check and outcome |
| :-- | :-- | :-- |
| Major, a refused derive lowers the tenant's storage counter | Holds. Exists today for checkpoint chunks, and revision 3 extended it to uploads. | `DeleteBySession` releases live rows' bytes, and `Copy` charges nothing. All refusals now come ahead of the copy, and each copy charges the counter. |
| Minors: the source of the gates, the experiment refusal, and "every bind stores a plan" | Hold. Fix changed for the experiment. | Revision 4's refusal would have stopped every enrolled user's derive during an experiment. `routeExperiment` does not run on a derive. |

### Review of revision 5

A stop check read revision 5 and `tenant-derived-runtimes-design.md` revision 30. It found no Critical or High issue, one Major, and no path across tenants. It was checked against the tree, and revision 6 applies it. The check confirmed that the gates can run ahead of the reservation.

| Finding | Result | Check and outcome |
| :-- | :-- | :-- |
| Major, "Admission" omits `applyPlaygroundCaps` | Holds. Introduced by revision 5, which rewrote the list. | One extracted row-build function replaces the list, which also covers `requirePoolNotDraining` and the `env` blocklist. |
| Minors: the quota refusal mid-copy, the target pool, and two tests | Hold. Fix changed for the quota. | One `Reserve` ahead of the first copy replaces a per-copy charge. The pool resolves once and is pinned. |

### Review of revision 6

A stop check read revision 6 and `tenant-derived-runtimes-design.md` revision 30. It found no Critical or High issue, one Major, and no path across tenants. It was checked against the tree, and revision 7 applies it.

| Finding | Result | Check and outcome |
| :-- | :-- | :-- |
| Major, pinning a resolved pool | Holds. Introduced by revision 6. | A pin through `req.Pool` meets `requirePoolSelectable` and refuses CRD-only pools, and a pin after it skips a revoked grant. The pin goes, and the pool passes through as a create's does. |
| Minors: what the extraction takes, the bytes `DeleteBySession` returns, and the playground statement | Hold. | Applied. |

### Review of revision 7

A narrow stop check read the change in revision 7 and the whole document once. It found no Critical, High, or Major issue, and no security property lost. It traced a same-runtime derive with no pool, a derive onto another runtime, a lost grant, and a removed source pool. Revision 8 applies its Minors on the resolved profile, a changed profile on a pinned source, and the refusal's field name.

### Earlier history

`session-replay-design.md` went through seven review rounds before the owner replaced it, and its text at commit the status line names records them. Most of their Majors concerned the gateway's prompt driver, which this design removes. Their findings on stops, origins, unsupervised targets, the admission gates, and upload handling carry over here.
