# Proposal: Pre-materialize a workspace on a warm pod

- **Status:** **EARLY DRAFT OF A NEW CAPABILITY. Not reviewed, not converged, not ready for sign-off.**
  This document explores a capability the platform does not have. It states a problem, sketches a design,
  and names the questions that decide whether the design is sound. Several of those questions are load
  bearing and none of them is answered here. Treat every mechanism below as a candidate rather than a
  decision.
- **Date:** 2026-08-19
- **Scope:** A warm pod holds no session workspace content, so everything a session's `WorkspacePlan`
  materializes (a repository clone, uploaded archives, inline files, and the setup commands that follow)
  runs on the hot path after the pod is claimed. This proposal explores pre-materializing that content at
  warm time, before any session is bound, and deriving each session's writable workspace from it cheaply.

This document stages nothing yet. It carries no fenced edit blocks, because the design is not settled
enough for an edit block to mean anything. Its Proposed changes section names targets and leaves the text
to a later revision.

## Why this document exists and what it is not

This is not a defect report. Nothing in the platform is broken, no specification sentence is false, and no
code diverges from what the specification states. Warm pods behave exactly as `spec/06` §6.1 describes:
`/workspace` is empty, no client files are present, and the workspace is materialized after the pod is
claimed. That is a deliberate design, and the reasons for it are good ones, which §1.3 records.

The proposal asks whether a second mode is worth building beside it. A reader evaluating this document
should be willing to conclude that it is not, and §7 states what would settle that.

## Summary

**What this would add.** A pool could declare a workspace template that the platform materializes on each
warm pod before any session is bound. When a session is assigned, its slot's workspace is derived from that
pre-materialized content rather than built from nothing, and the session's own plan is applied on top.

**What it is for.** A repository clone, a large archive extraction, and a dependency install are the three
things that dominate time-to-first-token, they are identical across every session on a pool, and today each
session pays for all three.

**The three hard questions.** How a per-slot writable workspace is derived from shared content without
copying it and without letting one session's writes reach another's; how a session's plan reconciles
against pre-materialized content that may be stale, in particular a `gitClone` whose commit is pinned per
session; and whether a pod-wide writable cache is an isolation regression the platform should not accept.

**What this is not.** It is not a change to how a session's own `WorkspacePlan` is validated, resolved, or
applied. A pool with no template behaves exactly as it does today.

## Implementation checklist

Deliberately absent. A checklist asserts an implementation sequence, and this document has no design to
sequence. It gains one when the §7 questions are answered.

## 1. Problem

### 1.1 Everything a session materializes runs after the pod is claimed

`spec/06` §6.1 states what a warm pod holds: the pod is scheduled, the adapter is listening, agent binary
dependencies are installed, `/workspace` and its staging directory exist, and **no client files are
present**. `spec/07` §7.1 then orders the hot path: the gateway streams buffered upload content into the
claimed pod and materializes the plan, the pod runs setup commands, credentials are assigned, and only then
does `StartSession` run.

Every source type in `spec/14`'s plan runs in that window: `gitClone`, `uploadFile`, `uploadArchive`,
`inlineFile`, and `mkdir`, followed by `setupCommands`. For a pool whose sessions all clone the same
repository and run the same dependency install, that work is identical every time and is paid every time.

### 1.2 The two things that exist today do not cover this

**Shared assets are read-only and small.** A Runtime may declare `sharedAssets`
(`pkg/apis/lenny/v1alpha1/runtime_types.go:272`), which `ensureSharedAssets` materializes into
`/workspace/shared/` at warm time before any slot exists. They are inline file bodies capped at 32 KB each
because the content rides in the rendered pod spec, and the directory is mounted read-only into the runtime
container so an agent write returns `EROFS`. The CRD's own comment marks the boundary: larger cross-slot
assets are delivered by the gateway as artifact references during pod initialization.

Read-only is the disqualifying property here rather than the size cap. A repository a session works in is
part of that session's workspace: the agent edits it, commits to it, and may create branches in it. Content
the session cannot write is not the session's workspace.

**SDK-warm pre-connects a process, not a workspace.** `capabilities.preConnect: true` starts the agent
process during the warm phase and leaves it waiting for its first prompt, explicitly *before workspace
finalization*. It removes SDK cold start from the hot path and nothing else. `spec/06` §6.1 also admits
`preConnect: true` only with `maxConcurrentSessions: 1`.

### 1.3 Why the current design is the way it is, which this proposal must respect

Three reasons, and a design that breaks any of them is worse than the status quo.

**Workspace contents are unknown until request time.** `spec/06` §6.1 states this as the reason the
pod-warm default does not start the agent: the agent's behavior depends on files such as `CLAUDE.md` and
`.claude/*` that arrive with the session.

**A clone is pinned per session, not per pool.** `spec/14` resolves each `gitClone.ref` to an immutable
`resolvedCommitSha` at session creation and states that the immutability guarantee is per-session rather
than per-plan: two sessions built from the same plan body may resolve to different commits. Content
materialized before any session exists is pinned to nothing.

**A clone may need credentials the warm pod does not have.** `gitClone.auth` names a `vcs.<provider>.read`
credential lease, and `spec/06` §6.1 states that a warm pod holds no credential lease and that assignment
happens per session at dispatch. The gateway performs the clone over its own network path precisely so the
pod never sees raw credentials.

## 2. What the constraints are, before any design

These come from the request that prompted this draft and from the code. They are constraints rather than
decisions: a design that violates one is out.

**C1. The pre-materialized content ends up in a read-write mount.** It is part of the session's workspace.
`/workspace/shared` is not a candidate, because its read-only mount is the property that makes it safe.

**C2. Concurrent slots get independent working trees.** On a pod with `maxConcurrentSessions > 1`, two
sessions working in the same repository need separate working trees. Managing those is the runtime binary's
job rather than the platform's, and this proposal's obligation is to leave that possible rather than to
implement it. What the platform hands a slot must therefore be something a runtime can build a worktree
from, and must not be a tree two slots share by writing.

**C3. It is not only `gitClone`.** Whatever is built covers the plan's other source types, in particular a
large `uploadArchive`, and should account for `setupCommands`, which are frequently the most expensive step.

**C4. Per-session isolation survives.** Proposal 0073's subject is that a session's files live under its own
slot and nothing resolves to a pod-global tree by accident. A design that reintroduces a shared writable
tree undoes that, and §7's third question is whether any version of this avoids it.

## 3. Design sketch

**This is a sketch. Each numbered piece is a candidate.**

**A pool-level workspace template.** A new optional field on the pool or the Runtime declares content to
materialize at warm time. It is not a `WorkspacePlan`: a plan belongs to a session, carries per-session
resolution, and may reference session uploads. The template's sources must be resolvable with no session,
which rules out `uploadFile` and `uploadArchive` in their session-scoped form and suggests an
artifact-reference form instead, naming content already in the Artifact Store.

**A pod-wide cache, materialized at warm time.** The adapter materializes the template into a pod-wide
location on a read-write volume during the warm phase, in the same place in the lifecycle where
`ensureSharedAssets` runs today.

**A per-slot derivation at assignment.** When a slot is assigned, its workspace is derived from the cache
rather than copied. Three candidate mechanisms, and choosing among them is §7's first question:

- **Git alternates.** The cache holds a bare object store and each slot gets its own repository borrowing
  objects through `objects/info/alternates`. Reading objects from an alternate does not write to it, so the
  cache can stay read-only, which answers C4 cleanly. Each slot's repository is fully writable and a runtime
  can create worktrees inside it. This works only for the repository, not for an extracted archive.
- **Hardlink or reflink copy.** The slot tree is populated with hardlinks (or filesystem-level reflinks)
  into the cache. Cheap and general across every source type. Hardlinks are unsafe for content the session
  edits in place, because a write through one link is visible through the other unless the writer replaces
  the file rather than modifying it; reflinks are copy-on-write and safe but need a filesystem that supports
  them, and the workspace volume is an `emptyDir` whose backing filesystem the platform does not choose.
- **Plain copy.** Correct and boring. Whether it is fast enough is an empirical question, and it should be
  measured before the cleverer options are taken, because a copy from local disk may already beat a network
  clone by enough to make this proposal worth shipping in its simplest form.

**Reconciliation with the session's own plan.** The session's plan is applied on top of the derived tree.
When the plan's `gitClone` names the same repository the cache holds, the pinned `resolvedCommitSha` decides
what happens: a cache holding that commit is used as-is, and a cache that does not must fetch the difference
or fall back to a full clone. §7's second question is whether that fallback is a correctness hazard or
merely a performance one.

## 4. Detailed design

Absent. Writing one before §7 is answered would be inventing a mechanism to fit a sketch.

## 5. Proposed changes

Targets only, with no text staged.

- `spec/06`: the warm-pod checklist gains a statement of what a pod with a template holds, alongside the
  existing statement of what a pod without one holds.
- `spec/05`: the pool configuration gains the template field.
- `spec/14`: the relationship between a pool template and a session's own plan.
- `pkg/apis/lenny/v1alpha1/`: the CRD field and its generated manifests.
- `pkg/adapter/`: warm-time materialization beside `ensureSharedAssets`, and per-slot derivation at
  assignment.
- `pkg/controller/sandbox/podspec/`: any new volume the cache needs.
- `docs/`: the pool-configuration and warm-pod pages.

## 6. Non-goals

- **Worktree management.** The runtime binary owns it (C2). This proposal's obligation is compatibility.
- **Changing how a session's own `WorkspacePlan` is validated, resolved, or applied.**
- **Changing `sharedAssets`.** It keeps its read-only mount and its purpose.
- **Pre-running anything that needs a credential lease.** A warm pod holds none, and this proposal does not
  propose giving it one.
- **A cross-pod or cross-node cache.** The cache is per pod.

## 7. The questions that decide whether this is buildable

1. **Which derivation mechanism, and is the simplest one already enough?** Measure a plain copy from local
   disk against a network clone plus archive extraction before adopting alternates or hardlinks. If a copy
   is fast enough, the design collapses to something much smaller. If it is not, alternates answer the
   repository case and leave the archive case open.

2. **What happens when the cache holds the wrong commit.** A pinned `resolvedCommitSha` the cache lacks
   must produce the same workspace a cold clone would. Establish whether a fetch-the-difference path can
   guarantee that, or whether the honest answer is to fall back to a full clone and accept that a stale pool
   loses the benefit it exists for. Related: what refreshes the cache, how a pool operator reasons about
   staleness, and whether a pool should re-warm on a schedule.

3. **Whether a pod-wide writable cache is an isolation regression the platform should refuse.** Pods are
   tenant-pinned (`stampPodTenant`, `pkg/gateway/podlifecycle/podclaim/slotclaimer.go:64`), so the exposure
   is between sessions of one tenant rather than across tenants. It is still a shared mutable surface on a
   pod whose entire recent design direction has been to give each session its own tree. If the cache is
   writable by slots, one session can poison every later session on that pod. If it is read-only to slots
   and written only by the adapter at warm time, that is answered, and the derivation mechanism must then
   work from a read-only source, which alternates do and in-place hardlink editing does not. **This is the
   question most likely to kill the proposal, and it should be answered first.**

4. **Whether `setupCommands` can be pre-run at all.** They are often the largest cost. They may read
   session `env`, may expect credentials, and may not be idempotent. Establish whether a template can
   declare a subset that is safe to pre-run, or whether pre-running is out of reach and this proposal covers
   sources alone.

5. **What the recycle boundary does to the cache.** A recycled pod scrubs session state between sessions.
   The cache must survive that scrub or the benefit is lost on every recycled pod, and stating that it
   survives means stating that it is not session state, which needs to be true rather than asserted.

6. **Whether the capability pays for itself.** Time-to-first-token improvement against the cost of a warm
   pod holding a populated cache, and how many pools would use it. If the answer is a pool or two, a
   deployer-side base image with the repository baked in is a cheaper answer that needs no platform change.

## 8. Testing

Not written. The tiers a change of this size would reach are 0, 1, 2, 5, 7a, and 9, and the isolation cases
under tier 9 are the ones that matter most, because §7's third question is a security question.

## 9. Files touched on application

Not enumerable until §7 is answered.

## 10. Dependencies and relationships

Sequences after proposal 0073, which makes the per-slot tree the only layout and is the model any per-slot
derivation builds on. Independent of proposals 0075 and 0076.

Related but distinct: a caller can approximate this today outside the platform by pre-starting sessions,
letting them materialize, and parking them in `suspended`, where `spec/06` §6.2 states that both
`maxSessionAge` and `maxClientIdleSeconds` are paused and a root session may remain indefinitely. That
workaround holds a real warm pod only until `maxSuspendedPodHoldSeconds` (default 900s) releases it, after
which what is held is a checkpoint rather than a pod. If that workaround proves good enough in practice, it
is evidence against building this.
