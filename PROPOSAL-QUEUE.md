# Proposal queue

Clusters of related spec-gap findings from `TEST-GAPS.md`, bundled so one
`change-proposal` resolves a whole group instead of one proposal per finding.
This is the handoff surface between the closure runners (which discover spec
gaps and escalate them to needs-human) and the proposal creator+implementor
machine (which resolves them).

## How this file is maintained

The integrator is the sole writer of this file. A proposal worker signals a
state transition by editing the one cluster it holds on its own branch and
pushing; the integrator merges that edit and advances the cluster. Runners do
not edit this file.

**Assignment model.** The integrator assigns clusters to proposal workers via
the `assigned:` field. A worker takes only clusters assigned to it (or, with a
single worker, any `open` cluster), and works highest-severity-first. When a
worker's branch lands, the integrator marks the cluster `landed:<proposal>` and
tops up that worker's assignments so it never idles on merge latency.

**Every active worker always holds at least one non-landed assigned cluster.**
On every integration tick the integrator checks each worker's assignments: if a
worker's only assigned clusters are `landed`, top it up immediately with the next
well-isolated cluster (do not wait for the worker to go idle). A worker whose
whole assignment set is landed has no runway and stalls. Keep at least one
`open`/`assigned`/in-flight cluster per worker at all times. With more
than one proposal worker, the integrator assigns each a disjoint set of clusters
(explicit per-cluster assignment is race-free because the integrator is the sole
writer of this file); clusters are grouped so each worker stays within a coherent
spec area, keeping two workers off the same spec sections and code and minimizing
integration conflicts. Current split: proposal-A holds the checkpoint and §25
clusters (C-01–C-03), proposal-B holds the §4.9 credential-leasing clusters
(C-04–C-06) and the eviction-trigger cluster C-22 (depends on proposal-A's C-01) plus the
warm-pod PDB cluster C-16 (§4.6.1, unblocked) as unblocked runway while C-22
waits on C-01 — all §4.6 kept with proposal-B, and proposal-C holds the two spec areas no
other worker or the §25 closure machine touches: the interceptor error-envelope
cluster C-18 (§4.8/§15.1) and the OpenSLO-export cluster C-19 (§16.10), both now
landed (0039, 0041); C-18/C-19/C-38 landed (0039/0041/0042); its next isolated cluster is C-20 (§8.3 cross-environment delegation credential compatibility — clear of the section-25 machine's §25 churn, proposal-A's checkpoint/pod, and in a different package from proposal-B's §4.9 credential leasing). The
integrator extends each worker's assignments from the remaining clusters as its
branches land, keeping each worker's lane clear of the others' active spec
sections. (2026-07-18 top-up: proposal-B took C-15 §4.4 + C-17 §4.6 to stay in
its checkpoint/pod-lifecycle lane; proposal-C took C-13 §15 — reassigned from
proposal-A — because its claim-per-request pod-binding is the same design as
proposal-C's C-42, consolidating pod-claim onto one worker rather than making
proposal-A a third pod-claim worker while it runs §25/§7. proposal-A keeps
C-14 §7 as its next pod/resume High.)

**Coordinate through this file before and after every proposal.** Other
runners are working proposals in parallel, so this file is the shared view of
who holds what and what has landed. Read it at two points:

- **Before starting a new proposal.** Take the highest-severity cluster whose
  `assigned:` field names you and whose status is not yet `claimed`/`in-review`/
  `landed`. Scan the other clusters' statuses first: skip anything another runner
  has `claimed`/`converging`/`in-review`, and do not open a proposal that touches
  the same spec sections or packages as another runner's in-flight cluster (the
  `assigned:`/`root spec gap`/`proposal scope` lines say which areas are live).
  If your only assigned clusters are blocked on another runner's unlanded work
  (a `depends on` note), wait or tell the integrator via the inbox rather than
  starting something in a conflicting area.
- **After a proposal completes** (lands or goes to `in-review`). Re-read the file
  before picking your next one: new clusters may have been added, assignments
  may have changed, and other runners' clusters may have landed and freed up a
  spec area that was previously off-limits. Do not assume your pre-proposal view
  is still current.

You do not edit assignments yourself (the integrator is the sole writer). If you
need a cluster reassigned, split, or unblocked, append an item to the Integrator
inbox at the bottom of this file on your own branch.

**Status lifecycle** (per cluster):

```
open → assigned:<worker> → claimed:<worker>@<ts> → converging
     → in-review:<proposal> (awaiting human approval)
     → landed:<proposal> → closed
```

`in-review` matters because `change-proposal` has a human-approval gate. While
a proposal waits on approval, its worker moves to its next assigned cluster.

**Proposal-number collisions.** Proposal workers run in isolation and each mints
the next free `proposals/00NN` number from its own branch's view, so two workers
can independently create the same number. On integration, before merging a
proposal branch, the integrator checks whether the incoming `proposals/00NN_*.md`
collides with a number already on `impl/v1-initial` or already claimed by another
in-flight proposal (grep the other proposal branches' `landed:`/`in-review:`
lines). On a collision the integrator renames the incoming file to the next free
number and updates every reference: the `landed:NNNN` line here, the `spec settled
by proposal NNNN` / RESOLVED notes in `TEST-GAPS.md`, and any citation the branch
added. The merge commit records the rename. (Proposal 0039 landed with no
collision; this is the standing rule for the next ones.)

**Feedback to the integrator.** A proposal worker or closure runner that needs
the integrator to do something outside its own branch — correct a cluster's
findings list, file a newly-discovered finding, reassign or split a cluster, flag
a cross-cluster dependency — appends an item to the **Integrator inbox** section
at the bottom of this file on its own branch, rather than relaying it out of band.
Each item is a `- [ ] from <worker> (<proposal/branch>): <ask>` line. The
integrator processes each item on integration, checks it off `- [x]` with the
action taken, and keeps a short trail there. This is the durable feedback
channel; do not rely on chat relay.

**When a proposal lands**, update each finding it covered in `TEST-GAPS.md`:
mark `[x] RESOLVED <sha>` if the proposal fully closes it (spec settled and
test written), or set it back to plain `OPEN` with a note
`spec settled by proposal NNNN; now closable` so a closure runner writes the
test at its tier. Then set the cluster to `landed:NNNN`.

Run `python3 scripts/reconcile-test-gaps.py --check` before pushing any branch
that edits `TEST-GAPS.md`.

---

## Clusters

Severity-first. All `open` and unassigned at seed time (2026-07-12).

### C-01 — Checkpoint produce-store-restore round-trip — §4.4/§6.2/§10.1
- **status:** landed:0045 (resynced onto impl/v1-initial by proposal-A 2026-07-18). Merged impl/v1-initial into `proposal-a/c-01-checkpoint-roundtrip` and resolved every conflict on the branch per `git-workflow.md`: regenerated the adapter proto after merging `schemas/lenny-adapter.proto`, unioned the `gatewayFlags` struct so every field impl added is kept plus the six checkpoint/object-store flags, unioned `tests/spec-map.json` and the migration column list, and re-ran the reached tiers (`go build ./...` green, the touched packages' tests pass). Renumbered per the Integrator inbox: proposal 0037→0045, withdrawn 0036→0046, eviction draft 0038→0047, migration `0175_checkpoint_manifest`→0178. Ready for the `--no-ff` integration into impl/v1-initial.
- **assigned:** proposal-A
- **findings:** T-4.4.12, T-4.4.13, T-JRN.9, T-DEP.5, T-4.2.1
- **root spec gap:** §4.4 promises a workspace can survive pod loss, but the produce→store→restore data pipeline is unwired and the architecture is undecided: does the pod-side adapter talk to MinIO directly (requiring §13 pod-side credential/egress delivery) or stream to the gateway, and what are the object-key layout and the atomic metadata/manifest record. (Split 2026-07-14: the eviction *trigger* is a separate root gap, moved to C-22 with T-4.4.14 and T-4.6.4.)
- **proposal scope:** Decide the checkpoint sink/source architecture and pod-side MinIO credential delivery; the object-key layout and atomic metadata plus partial-manifest schema (including `manifest_reason='terminated_during_resume'` and the generation counters); and `POST /v1/sessions/{id}/resume`, with the MinIO-outage fallback to minimal Postgres state. T-JRN.9's checkpoint-then-resume journey closes here for the periodic-loop-driven variant (drive the checkpoint from the periodic loop, then delete the pod — no eviction trigger needed); the node-drain-driven variant is tracked under T-4.6.4 in C-22.
- **severity:** High

### C-02 — Operational event-stream read source, tenant filtering, and MCP subscription — §25.5/§25.12
- **status:** landed:0053 (integrated `--no-ff` into impl/v1-initial 2026-07-21. Minted 0049 on proposal-A's branch, which collided with the landed 0049 = C-41 credentialDeny; renamed to **0053** on integration — the next free number after 0051 (C-17) and 0052 (reserved for C-22 prereq-2, in-review). The rename updated the proposal file, the ~10 TEST-GAPS resolution notes, and the spec-map note; the merge commit records it. Code merged with 0 conflicts (only PROPOSAL-QUEUE.md conflicted — inbox appends — resolved by the integrator as sole writer). Verified on integration: `go build ./...` green, validate-maps 15/15, reconcile-test-gaps clean. Implemented the §25.5 Redis read source (XRANGE poll + XREAD BLOCK live tail), the cross-process gateway-buffer fan-out fallback deduped by `eventKey`, delivery/read-time tenant filtering, and the `ListDeliveries` keyset cursor + SPEC-4 §25.5 deliveries-pagination amendment. Reached tiers green locally except tier-5 e2e-Kind (un-skipped, compiles; needs a Kind cluster — the operator-provisioned escape hatch). Closes T-25.5.1/.5/.7/.15/.20 + T-25.3.5 (RESOLVED with shas), records T-25.5.14. T-25.5.6 (single-implementor aggregator) and T-25.12.6 (MCP-native subscription) deferred to follow-on clusters — see Integrator inbox / C-45 / C-46.)
- **assigned:** proposal-A
- **findings:** T-25.5.1, T-25.5.5, T-25.5.6, T-25.5.7, T-25.12.6, T-25.3.5, T-25.5.20, T-25.5.14
- **root spec gap:** The §25.5 read side never consumes the Redis `ops:events:stream`, so the transparent Redis→gateway-buffer fallback, delivery-time tenant filtering, gap-detection on real eviction, the full `EventStreamService` interface (caller identity, `UpdateSubscription`/`ListDeliveries` cursor), and MCP-native subscription have no data source or defined caller-tenant/RBAC plumbing. The gateway-buffer fallback is only a degradation label over the lenny-ops-local ring buffer (T-25.5.20): it never fetches gateway-originated events cross-process, and the cross-replica `eventKey` merge/dedup over the `lenny-gateway-pods` headless Service (T-25.3.5) has no consumer.
- **proposal scope:** Define the Redis XREAD/XRANGE read source with mid-stream source switching and cross-source cursor translation; the cross-process gateway-buffer fetch over the `lenny-gateway-pods` headless Service with per-replica poll and `eventKey` merge/dedup (T-25.5.20, T-25.3.5); the delivery-time tenant-filter rule plus caller-identity/RBAC threading on the SSE/poll read endpoints; the `EventStreamService` caller-signature and cursor-pagination contract; and the MCP `notifications/subscribe`/`message` streaming transport and payload schema layered on that source, including the settled MCP-exposure of event-subscription CRUD (T-25.5.14).
- **severity:** High

### C-03 — MCP management tool dispatch, identity/scope forwarding, and capability classification — §25.12/§25.4
- **status:** landed:0055 (integrated `--no-ff` into impl/v1-initial 2026-07-22 on branch-recorded sign-off. Minted 0052 on proposal-A's branch, which COLLIDED with the landed C-22 prereq-2 0052 — renamed to **0055** on integration (next free after 0054 C-42) and every reference updated (proposal file, the four T-25.12.x/T-STD.8 TEST-GAPS resolution notes), preserving C-22's own 0052 refs; the merge commit records the rename. Built the §25.12 dual-routing MCP management dispatch: `opsserver.RouteSchemas()` membership is the ops-vs-gateway transport predicate, operability-vs-admin classification by the `x-lenny-scope` domain prefix as a `tools/list` discovery filter (not an authz boundary — RFC 9068 scope-claim gate + endpoint RBAC enforce every call), an additive raw-passthrough method on the §25.4 GatewayClient, and dev-header tenant/roles/groups forwarding under AllowDevHeaders. SPEC-1 §25.12 capability-classification clarification (commit 15176596). Verified on integration: `go build ./...` + `go vet` green, changed-package (`pkg/ops/...`) tests pass, validate-maps 15/15, reconcile clean. Closes T-25.12.2/.3/.7/.22 + T-STD.8; T-25.12.11 stays OPEN as now-buildable gateway-outage chaos. Worker verified tier-1/2/3/4/9 (94.1% changed-line coverage); envtest/compose tiers rely on the worker's run + CI.)
- **assigned:** proposal-A
- **findings:** T-25.12.2, T-25.12.3, T-25.12.7, T-STD.8
- **root spec gap:** The `/mcp/management` tools/call surface has no routing predicate to split ops-owned vs gateway-proxied tools, no identity/scope forwarding (the JWT `scope` claim and `Authorization` header are dropped on both the internal REST replay and gateway proxy), and no data source for the operability-vs-admin capability classification.
- **proposal scope:** Decide the ops-vs-gateway routing marker (e.g. `x-lenny-owner`/`x-lenny-scope-group` OpenAPI extension vs a `RouteSchemas` predicate); the identity-forwarding transport (per-call identity override plus raw status/body passthrough, dev-header propagation); the RFC 9068 scope-claim bridging into enforcement; and the closed/illustrative capability domain classification.
- **severity:** High

### C-04 — Token Service unavailability guard in the credential renewal path — §4.9
- **status:** landed:0036
- **assigned:** proposal-B
- **findings:** T-4.9.7, T-4.9.24
- **root spec gap:** §4.9 requires that while `now < ExpiresAt` and the Token Service breaker is open, the renewal worker extend the adapter-side lease timer and reschedule instead of triggering the Fallback Flow, but no adapter mechanism exists to extend a direct-mode lease without a re-mint.
- **proposal scope:** Define the adapter protocol surface to extend a direct-mode lease's expiry by one buffer without delivering a replacement; how the worker learns breaker-open state; and whether the extension may cap at or exceed the original `expiresAt` against the "key must not outlive the lease" invariant.
- **severity:** High

### C-05 — Credential-revocation deny-list semantics under a shared multi-replica store — §4.9
- **status:** landed:0037
- **assigned:** proposal-B
- **findings:** T-4.9.3
- **root spec gap:** Both user and pool revocation remove the lease globally from the shared Postgres store, so a post-revocation request returns `LEASE_TOKEN_INVALID` rather than the spec-mandated `CREDENTIAL_REVOKED`, and the startup rebuild never seeds user-shaped deny-list entries.
- **proposal scope:** Decide whether revocation should stop removing the lease and rely on the deny list (so `CREDENTIAL_REVOKED` is reachable in the shared-store topology) or whether that contract is single-binary only, and specify the user-credential startup-rebuild query across the credential stores.
- **severity:** High

### C-06 — Multi-tenant admission enforcement for delivery-mode/isolation combinations — §4.9
- **status:** landed:0038
- **assigned:** proposal-B
- **findings:** T-4.9.6
- **root spec gap:** The spec mandates admin-API pool-registration rejections (`DirectModeStandardIsolationMultiTenantRejected`, proxy+`spiffeBinding:disabled`) and a `lenny-preflight` CredentialPool scan in multi-tenant mode, but these enforcement points are unimplemented.
- **proposal scope:** Add the warm-pool-controller admin-API registration-rejection layer and the preflight spiffeBinding scan as product features (a BUILD gap routed through implement-proposal rather than a test-only fix).
- **severity:** High

### C-07 — lenny-ops write-back to the gateway admin API (config apply and drift reconcile) — §25.8/§25.10
- **status:** open
- **assigned:** (unassigned)
- **findings:** T-25.8.5, T-25.10.2
- **root spec gap:** Both config-apply and drift-reconcile must proxy to the gateway's admin PUT endpoints via GatewayClient, but the applier has no defined way to acquire the `If-Match` ETag or map normalized desired-state onto each resource's full-replace PUT body, and the gateway serves no `PUT /v1/admin/platform/config` nor a runtime-config schema.
- **proposal scope:** Define the GatewayClient-backed applier (ETag acquisition and desired-state-to-PUT-body mapping); the gateway config-apply mechanism and per-key `CONFIG_RESTART_REQUIRED` verdict; and the runtime flat-key config schema against which `CONFIG_VALIDATION_FAILED` is defined.
- **severity:** High

### C-08 — Platform-upgrade orchestrator architecture — §25.8
- **status:** open
- **assigned:** (unassigned)
- **findings:** T-25.8.4, T-25.8.20, T-25.15.8, T-25.8.15, T-25.8.23, T-25.8.24
- **root spec gap:** §25.8 describes lenny-ops itself watching the new pod for ImagePull/CrashLoop failures over a 60s window and running serial multi-shard schema migration with failed-shard pause/resume, but the implementation is an operator-paced orchestrator with the PodObserver seam left nil.
- **proposal scope:** Resolve whether to build the in-process lenny-ops PodObserver and multi-shard migration runner or ratify the operator-paced model with a narrower operator-reported failed-shard tracking surface, so the stuck-upgrade chaos and shard pause/resume tests have a product surface.
- **severity:** High

### C-09 — OCSF wire-format fidelity (class_uid registry and chain-hash recomputability) — §4.4/§11.7/§12.8/§16.7/§25.9
- **status:** open
- **assigned:** (unassigned)
- **findings:** T-STD.2, T-4.4.17, T-25.9.14
- **root spec gap:** Lenny's OCSF translator assigns `class_uid` values that diverge from the published OCSF v1.1.0 registry (so SIEMs misclassify most events), and separately the on-wire OCSF record cannot recompute its own chain hash because `event_type` and the canonical payload are not recoverable from the record alone.
- **proposal scope:** Correct the `class_uid` assignments across the spec sections and docs to match the real OCSF v1.1.0 registry (or document a deliberate divergence and qualify the "canonical wire format" claims), and resolve what fields the record must carry so the chain hash is recomputable from the record plus its extension.
- **severity:** High

### C-10 — Degraded-mode deferred-write post-outage reconciliation — §25.9
- **status:** open
- **assigned:** (unassigned)
- **findings:** T-25.9.3
- **root spec gap:** The `rechained_post_outage` contract is pinned but the reconciliation mechanism is unbuilt and under-specified: how the platform-scoped `audit_log_deferred_writes` maps onto per-tenant hash chains, how the affected range is re-hashed when later rows already sit above the insertion point, and how the per-row rechained state is persisted for the query API.
- **proposal scope:** Specify the deferred-write reconciler (tenant mapping, prev_hash/sequence assignment for out-of-order inserts, re-hash range, per-row `rechained_post_outage` persistence and counter wiring) before the driver is built.
- **severity:** High

### C-11 — Audit operation_id correlation convention across lenny-ops emitters — §25.17/§25.9
- **status:** open
- **assigned:** (unassigned)
- **findings:** T-25.17.1, T-25.9.12
- **root spec gap:** The remediation audit trail is not queryable by the §25.9 `?operationId=` filter because lenny-ops emitters write camelCase `operationId` (and locks drop the operation id entirely) while the gateway filter matches snake_case `operation_id`.
- **proposal scope:** Decide the canonical audit-payload correlation key (normalize to snake_case `operation_id` across all lenny-ops emitters vs accept both keys in the §25.9 filter) and fix the lock emitter to carry the operation id.
- **severity:** High

### C-12 — Gateway health-endpoint authentication — §25.3/§25.4
- **status:** open
- **assigned:** (unassigned)
- **findings:** T-25.3.1
- **root spec gap:** §25.3 requires platform-admin/tenant-admin role on the gateway health endpoints, but they are served unauthenticated by design and tier-8 chaos probes depend on that anonymous access; §25.4 arguably describes the summary/heartbeat as a public fallback.
- **proposal scope:** Resolve which artifact is authoritative — role-gate the health endpoints (and switch chaos probes to authenticated calls) or amend §25.3 to make the summary/heartbeat explicitly public.
- **severity:** High

### C-13 — OpenAI-dialect built-in adapters pod-binding model — §15
- **status:** landed:0057 (integrated `--no-ff` 2026-07-22 on branch-recorded sign-off. Minted 0055 on proposal-C's branch, which COLLIDED with the landed C-03 0055 — renamed to **0057** on integration (next free after 0056 C-48) and every reference updated (proposal file, T-STD.4 resolution note, spec-map), preserving C-03's own 0055 refs (T-STD.8 §25.12); the merge commit records the rename. Built the §15 single-shot pod-binding model: the two OpenAI-dialect adapters claim a warm pod, dispatch, and release within one HTTP call via a new CreateAndStartService wrapper reusing handleCreateAndStart — claim-per-request against the shared §5 pool, no dedicated translator pool; guaranteed synchronous release on success/failure/timeout on a detached context recording the §6.2 disposition; fail-closed two-code exhaustion mapping. Also hardened a fail-open gap the review found: handleCreateAndStart omitted the §11.1 concurrency/admission-rate + §10.6 environment-admission gates the two-step create path runs, so POST /v1/sessions/start now runs them too. SPEC-1 added the one §15 clause. Closed T-STD.4 and un-skipped the tier-5 conformance tests. Sign-off decisions: Open Responses continuity → option (b), SupportsSessionContinuity stays true with lineage+rehydration filed as a follow-on proposal (proposal-C, in flight next); pool-exhaustion envelope → 503+Retry-After. Green + review-clean: spec + build steps across tiers 0/1/2/3/7a, changed-line coverage 82.3%. Tier-5 conformance tests implemented+un-skipped but unrun locally (Docker/Kind unavailable — the documented infra escape hatch; they run in CI). Pure-reuse of shared warm-pool primitives, no shared-primitive edit → no C-22 serialization. Branch proposal-c/c-13-openai-adapter-pod-binding, ready for integrator merge.) C-13's pod-binding question — how a single-shot request claims/dispatches/releases a warm pod — is the same claim-per-request design as proposal-C's C-42 (§8.2 delegate-path pod allocation), so keeping both "claim-a-pod-per-request" clusters on one worker consolidates the pod-claim-model decision rather than making proposal-A a third pod-claim worker (proposal-A is on §25 C-02/C-03 and §7 C-14, a different area). Sequence after C-42; both share the warm-pool claim primitives (`poolstore`, `credrouter.PreClaim`), so coordinate with proposal-B's C-22 (§4.6 eviction) via the inbox if they collide.
- **assigned:** proposal-C
- **findings:** T-STD.4
- **root spec gap:** The always-available OpenAI Chat and Open Responses adapters fail on any standard Helm deployment because they bypass the pod-claim path and are never bound to a pod, and the spec does not define how a single-shot adapter request claims/dispatches/releases a pod.
- **proposal scope:** Define the pod-binding model for the OpenAI-dialect adapters under a PodExecutor (claim-per-request against the warm pool, a dedicated translator pool, or another design) so a non-continuous request completes within one HTTP call.
- **severity:** High

### C-14 — Interactive suspended-session atomic resume-and-deliver — §7 (path 6)
- **status:** landed:0058 (integrated `--no-ff` 2026-07-22 on branch-recorded sign-off. Minted 0055 on proposal-A's branch, which COLLIDED with the landed C-03 0055 (and C-13's 0055→0057) — renamed to **0058** on integration (next free after 0057 C-13) and every reference updated (proposal file, the nine §7.2/T-JRN resolution notes, inbox items), preserving C-03's own 0055 refs (§25.12); the merge commit records the rename. no spec edit — pure §7.2 path-6 build. green + review-clean at 85.8% changed-line coverage, all files >=80%; reached tiers pass locally — build/vet, tier-1 messagerouting, tier-2 sessionserver component (resumeHeldPod + fail-closed), tier-4 integration (TestInteractiveIterationInterruptThenResumeAndDeliver pins delivered+running). Router ActionResumeAndDeliver + coordinator-side atomic resumeHeldPod (guard atomic in store.Update) + queued fail-closed. Closes T-JRN.1's tier-4 half (stays OPEN for the tier-5 real-pod half, blocked on T-JRN.14 §4.7). Opened deferred follow-ons T-JRN.13/.14/.15/.16 + test-infra T-MAP-1 (18 dangling spec-map refs). Ready for --no-ff integration.)
- **assigned:** proposal-A
- **findings:** T-JRN.1
- **root spec gap:** §07 path 6 requires that an `immediate` message to a suspended session whose pod is still held atomically resume and deliver with `status: delivered`, but the code falls back to queueing (`status: queued`), leaving the session suspended.
- **proposal scope:** Confirm the atomic resume-and-deliver behavior as the required contract and route the missing product implementation through the pipeline so the test can pin the `delivered` outcome.
- **severity:** High

### C-15 — Checkpoint consistency tagging, isolation enforcement, and duration benchmarking — §4.4
- **status:** assigned:proposal-B (2026-07-18). Keeps §4.4 checkpoint work single-worker with proposal-B's active C-22 (§4.4 slot-aware checkpointing) — both touch `pkg/checkpoint`; sequence after C-22. Clear of proposal-A (§25/§7/§15) and proposal-C (§8.x).
- **assigned:** proposal-B
- **findings:** T-4.4.7, T-4.4.8
- **root spec gap:** §4.4 says checkpoints are tagged consistency, produce consistent results only under the cooperative handshake, and must meet a duration SLO, but nothing persists a consistency tag on the record, nothing refuses the embedded SIGSTOP path under sandboxed/microvm isolation, and no client-reachable checkpoint-trigger surface exists to benchmark.
- **proposal scope:** Decide whether the consistency classification is persisted on the checkpoint record, add an isolation-profile enforcement point rejecting embedded+sandboxed/microvm, and define the trigger surface (on-demand endpoint vs periodic) the duration benchmark observes. Builds on C-01.
- **severity:** Medium

### C-16 — Warm-pod PDB disruption mechanism — §4.6.1
- **status:** landed:0048 (minted 0043 on proposal-B's branch, which collided with the landed 0043 = C-20 credentialPropagation; renamed to 0048 on integration 2026-07-18, the next free number after 0045/0046/0047 from C-01 and 0044 from C-40)
- **assigned:** proposal-B
- **findings:** T-4.6.5
- **root spec gap:** A `maxUnavailable: 1` PDB requires the owning controller to expose a `/scale` subresource to resolve expectedPods, but Sandbox has only a status subresource, so every warm-pod PDB sits at `disruptionsAllowed: 0` and deadlocks node drains.
- **proposal scope:** Choose the fix — add a `/scale` subresource to Sandbox/SandboxWarmPool, replace `maxUnavailable: 1` with an integer `minAvailable` (currently forbidden by the spec), or another mechanism — and amend §4.6.1 accordingly.
- **severity:** Medium (operationally High — see the discovered finding in TEST-GAPS.md: warm-pod PDB deadlocks node drains on the live cluster)

### C-17 — SSA conflict retry policy and counter semantics — §4.6/§16
- **status:** landed:0051 (integrated `--no-ff` into impl/v1-initial 2026-07-21, 0 conflicts — impl was an ancestor of the branch). Consolidated the two divergent `retryOnConflictSSA` copies into shared `pkg/controller/ssaretry`, registered `lenny_crd_ssa_conflict_total{crd,controller}` + `crd_ssa_conflict_stuck` at the five-consecutive-409 boundary, retry continues past five via requeue. Verified on integration: `go build ./...` green, ssaretry tier-1 passes, validate-maps 15/15, reconcile-test-gaps recomputed the summary line (worker left it stale). Closes T-4.6.7, T-4.6.17, T-16.1.10 (RESOLVED in TEST-GAPS). §4.6 pod-lifecycle/reconciler retry semantics — proposal-B's warm-pod lane (it landed C-16 §4.6.1). Isolated from proposal-A and proposal-C.
- **assigned:** proposal-B
- **findings:** T-4.6.7
- **root spec gap:** §4.6 and §16 contradict each other on when `lenny_crd_ssa_conflict_total` increments (only after 5 consecutive no-progress 409s vs on every conflict), and §4.6 requires the retry loop to continue past five with backoff while the code gives up and returns the error.
- **proposal scope:** Resolve which increment semantics is authoritative and whether the loop continues past five (bounded or unbounded within one reconcile), then register the counter and the `crd_ssa_conflict_stuck` log so the behavior is implementable and testable.
- **severity:** Medium

### C-18 — Interceptor MODIFY immutable-field error envelope — §4.8/§15.1
- **status:** landed:0039
- **assigned:** proposal-C
- **findings:** T-4.8.9, T-4.8.16
- **root spec gap:** The spec mandates HTTP 400 with top-level `INTERCEPTOR_IMMUTABLE_FIELD_VIOLATION` and `details.violated_fields`, but the session-create surface returns HTTP 403 `INTERCEPTOR_REJECTED` (deliberately, pinned by two tier-1 tests), so the client-facing envelope diverges from the catalog.
- **proposal scope:** Decide whether to align the route/session-create surface to the spec envelope (and thread `violated_fields` across the connector/llmproxy/mcptools surfaces) or amend §4.8/§15.1 to ratify the wrapped-403 behavior; either way a client-visible status change and a shared `interceptor.Result` change need sign-off.
- **severity:** Medium

### C-19 — OpenSLO v1 export conformance and notification-target config — §16.10
- **status:** landed:0041 (Option B self-contained AlertNotificationTarget; deployer-configurable per-deployment name AND type; §16.10 spec edit; supersedes 0040, retained for history)
- **assigned:** proposal-C
- **findings:** T-STD.6
- **root spec gap:** The rendered OpenSLO documents violate the external OpenSLO v1 object model (two conditions per AlertPolicy, missing required `notificationTargets`, bare-string `alertPolicies`), and fixing the notification-targets requirement needs a deployer-facing config surface §16.10 does not define.
- **proposal scope:** Add a §16.10 notification-target configuration surface and formalize the one-condition-per-policy split and `alertPolicyRef`-object references so the export validates against OpenSLO v1.
- **severity:** Medium

### C-20 — Cross-environment delegation credential compatibility check — §8.3
- **status:** landed:0043 (credentialPropagation field + enum/validator, CredentialOriginSessionID origin-pool threading, the cross-env inherit compatibility gate emitting CREDENTIAL_PROVIDER_MISMATCH, AND the concrete inherit shared-pool credential assignment at finalize (origin∩child intersection); minimal SPEC-1 omit-default clarification. §8.3 availability pre-check (C-40) and deny-mode suppression out of scope — see inbox item)
- **assigned:** proposal-C
- **findings:** T-BED.8
- **root spec gap:** The §8.3 `credentialPropagation: inherit` provider-compatibility check (intersect parent pool providers with child runtime supportedProviders, reject empty with `CREDENTIAL_PROVIDER_MISMATCH` before pod allocation) is fully specified but entirely absent — there is no `credentialPropagation` field on the schema or Request and no code produces the error.
- **proposal scope:** Build the feature (add the `credentialPropagation` lease field, origin-pool provider tracking, cross-environment detection, and the pre-claim intersection check) through implement-proposal.
- **severity:** Medium

### C-21 — Stale spec build-sequence directory reference — spec/18
- **status:** open
- **assigned:** (unassigned)
- **findings:** T-STD.12
- **root spec gap:** `spec/18_build-sequence.md` Phase-5 exit criteria still names the retired `tests/tier3_contract/rest_openai_completions/` directory (renamed to `rest_openai_chat/`); the fix is editorial but any spec edit goes through the proposal pipeline.
- **proposal scope:** Correct the one-line stale path reference in `spec/18_build-sequence.md`; editorial, no behavioral change. Low priority; batch with a neighboring §18 proposal if one arises.
- **severity:** Low

### C-22 — Agent-pod eviction-checkpoint trigger and its prerequisites — §4.4/§4.6.1/§10.1
- **status:** open — prereq 1 landed:0050 (2026-07-19); prereq 2 **landed:0052** (integrated `--no-ff` 2026-07-22 on branch-recorded sign-off, per the settled trust-branch-sign-off policy). 0052 is a single §4.6.1 spec-prose clarification: an evicted agent pod's preStop cannot open the gateway-driven `Checkpoint` stream, so it signals its coordinating gateway replica over the existing per-pod control channel (`AdapterTerminating`), and that replica — the session-coordination-lease holder — drives `CheckpointWithTrigger(TriggerEviction)` under its held lease; an unreachable coordinator is recovered via the §10.1 TTL-driven handoff, fail-closed. Plus one tier-11 route-consistency test; no product code. Verified on integration: `go build ./...` green, validate-maps 15/15, the tier-11 route-consistency test passes, reconcile clean. 0052 closes no finding; unblocks-not-closes T-4.4.14/T-4.6.4. The within-window takeover of an UNREACHABLE coordinator, the forward-to-holder transport, and any takeover primitive are deferred to the trigger-wiring proposal (coordinator-direct seam resolved). prereq 1 = **0050 slot-aware checkpointing** (integrated 2026-07-19). **Prereq 3 (agent-pod grace budget) turned out a no-op** per proposal-B's analysis (2026-07-22): the grace-budget arithmetic needed no new spec/code work, but the analysis surfaced a standalone admission-correctness defect in the pool-config grace-floor webhook, fixed and landed separately as **C-48/0056** (nil-bypass + gateway/agent BarrierAck conflation). Prereq 3 is therefore considered discharged. **C-22 stays open and assigned to proposal-B** for prerequisite 4 (runtime-container preStop) + the trigger, each a separate proposal/branch.
- **assigned:** proposal-B
- **findings:** T-4.4.14, T-4.6.4
- **root spec gap:** §4.6.1 (spec/04:489) makes a preStop hook on every agent pod
  the primary protection against voluntary disruption, but no code path drives a
  checkpoint when an individual agent pod is terminated by a node drain, the
  kubelet Eviction API, a cluster upgrade, or a direct delete. `checkpoint.TriggerEviction`
  is selected by no caller and `triggerForSource` returns `TriggerPeriodic` on
  every arm. The trigger is blocked by four prerequisites that are each standing
  product defects in their own right, so it cannot be built as a wiring change.
- **proposal scope:** Land the prerequisites, then the trigger. (1) Slot-aware
  checkpointing: `CheckpointRequest` carries no `slot_id` and `checkpointRoots()`
  is pod-global, so pods on `maxConcurrentSessions > 1` pools cannot be
  checkpointed at all today, on any trigger. (2) Coordinator resolution for a
  pod-originated signal, which lands on an arbitrary replica while only the lease
  holder can checkpoint, and `leasestore.Acquire` has no compare-and-steal.
  (3) The agent-pod termination-grace budget, which needs 160s (90s tier cap +
  60s Postgres fallback + 10s drain margin) against a 120s default; the 240/300s
  figures §4.4 cites are the gateway pod's. (4) A container-termination story for
  the runtime container, whose preStop hook execs a `lenny-adapter` binary its
  third-party image does not contain. Then choose the trigger seam (pod-to-gateway
  termination signal vs gateway-side pod informer) and fix `triggerForSource` so
  `TriggerEviction` reaches the metric label and the eviction retry budget.
  Analysis and design space are drafted in
  `proposals/0038_draft_agent-pod-eviction-checkpoint-trigger.md`.
- **severity:** High
- **depends on:** C-01 (the checkpoint data path must settle first; the trigger
  drives the pipeline C-01 wires)
- **integrator note:** The "gateway-side pod informer needs new RBAC" objection is
  false — the gateway ClusterRole already grants pods get/list/watch cluster-wide.
  Reject or choose the informer on per-replica ownership and dedup, plus the race
  against the kubelet grace period, not on RBAC.

### C-23 — Internal/admin TLS transport contradictions — §25.4/§13.2/charts
- **status:** open
- **assigned:** (unassigned)
- **findings:** T-25.4.18, T-25.4.20, T-25.4.24, T-25.6.14
- **root spec gap:** The spec is internally contradictory on internal-transport TLS: `gateway.internalTLSPort` and `gateway.llmProxyPort` both default to 8443, no gateway admin-API-over-TLS listener exists, and §25.4 says lenny-ops serves TLS on :8090 while the same section terminates TLS at the Ingress.
- **proposal scope:** Assign a distinct non-colliding `internalTLSPort`, decide how/whether the gateway binds an admin-API-over-TLS listener and its cert source, and resolve whether lenny-ops :8090 is plaintext-behind-Ingress or serves TLS; then the skipped handshake tests un-skip.
- **severity:** High

### C-24 — §25.6 Postgres-unreachable K8s fallback pod-labeling and signals — §25.6/§6.2/§4.6.1
- **status:** open
- **assigned:** (unassigned)
- **findings:** T-25.6.13, T-25.6.15, T-25.6.16, T-25.6.19, T-25.6.26, T-25.6.17
- **root spec gap:** The §25.6 diagnostics K8s fallback reads pod labels/signals (`lenny.dev/session-id`, `lenny.dev/pod-state`, an `InSetupPhase` signal) that §6.2/§4.6.1 never stamp; the coarse `lenny.dev/state` (idle/active/draining) cannot reconstruct the four-bucket breakdown, and no error code is catalogued for the both-down 503.
- **proposal scope:** Reconcile §25.6 against §6.2/§4.6.1: decide which labels/values the platform stamps (or revise §25.6 to an achievable fallback), name the canonical setup-phase signal for SETUP_COMMAND_FAILED, and add the missing 503 error code/category.
- **severity:** High

### C-25 — §25.6 auto-remediation RBAC and Helm render-source — §25.4/§25.6/charts
- **status:** open
- **assigned:** (unassigned)
- **findings:** T-25.6.23
- **root spec gap:** The four auto-remediations other than certManagerExpiring are unreachable on any real deployment: lenny-ops-sa lacks the required kube-system/config/monitoring/warmpool RBAC, `--doctor-render-dir` is unwired so HelmRenderSource is nil, and the spec is silent on whether RBAC-Forbidden should map to `not_detected`.
- **proposal scope:** Amend the §25.4 RBAC block and `ops-rbac.yaml` grants, wire the render-dir + bootstrap/monitoring manifests, and decide Forbidden handling in `isAbsent()`.
- **severity:** High

### C-26 — §25.7 Path B runbook link resolution — §25.7
- **status:** open
- **assigned:** (unassigned)
- **findings:** T-25.7.11, T-25.7.10, T-25.7.12, T-25.7.8
- **root spec gap:** The gateway health service links runbook slug `siem-delivery-failure` for AUDIT_SIEM_DELIVERY_DEGRADED, but no such runbook is bundled, so Path B returns RUNBOOK_NOT_FOUND; the spec permits three resolutions and mandates none.
- **proposal scope:** Choose one: author the runbook, drop the mapping entry, or repoint to the existing audit-pipeline-degraded runbook; then enforce the whole-map convention test.
- **severity:** High

### C-27 — §25.11 lenny-backup image Postgres client tooling — §25.11/Dockerfile/CI
- **status:** open
- **assigned:** (unassigned)
- **findings:** T-25.11.15
- **root spec gap:** The §25.11 backup/restore Jobs shell out to `pg_dump`/`pg_restore`/`psql`, but the distroless lenny-backup image ships none of them, so the Job cannot run; the base-image/version/CI decision is unpinned.
- **proposal scope:** Decide the runtime base image (weighing the §13.1 distroless/non-root posture), pin the Postgres client major version, and wire a per-image Dockerfile through all four build paths; un-skip the committed tier5 assertion.
- **severity:** High

### C-28 — Total-outage runbook vs TESTING.md §15.2 API-executable rule — §25.15/TESTING.md
- **status:** open
- **assigned:** (unassigned)
- **findings:** T-25.15.2, T-25.15.9
- **root spec gap:** TESTING.md §15.2 requires every runbook's tier8 test to execute remediation through the API rather than kubectl, but §25.15 declares the total-outage recovery paths deliberately human-only kubectl operations; the two documents contradict for this runbook (and the runbook currently maps to an unrelated chaos test).
- **proposal scope:** Decide the authority: carve a §15.2 exemption for spec-declared human-only runbooks (and repoint the runbook-map to a faithful surrogate/Path-C reconciliation test) versus adding API-executable recovery paths; also settle the Path E resume-from-failed-shard claim.
- **severity:** High

### C-29 — lenny-ops cross-replica health/recommendations aggregation and Prometheus fan-out fallback — §25.4/§25.13/§25.15
- **status:** open
- **assigned:** (unassigned)
- **findings:** T-25.3.8, T-25.4.9, T-25.4.25, T-25.13.3, T-25.13.5, T-25.15.5, T-25.4.38
- **root spec gap:** The §25.4/§25.13 lenny-ops aggregator (Prometheus alerts primary + per-replica `/v1/admin/health` fan-out fallback, worst-of status merge, highest-confidence recommendation merge, `thresholdSource` stamping, RECOMMENDATIONS_UNAVAILABLE 503) is deferred in code with no serving surface, so several documented behaviors have nothing to exercise.
- **proposal scope:** Build the deferred aggregator and its serving endpoints (including `metrics.Reader` AvailabilityReporter, worst-of/union/highest-confidence merges, `thresholdSource` values, and the disableOnPrometheusOutage 503), or reclassify these as BUILD gaps; then the fan-out integration/chaos tests land.
- **severity:** Medium

### C-30 — §25.6 pool/credential diagnostics MetricSource and gateway-scrape fallback — §25.6/§25.4
- **status:** open
- **assigned:** (unassigned)
- **findings:** T-25.6.9, T-25.6.21
- **root spec gap:** §25.6 pool/credential diagnostics are meant to read warm-up-failure metrics from Prometheus via MetricSource and fall back to scraping gateway `/metrics` via the headless Service, but prodsource wires no MetricSource; §25.6 also self-contradicts on the fallback marker (`metricsSource` top-level vs `degradation.actualSource`, tracked as T-25.6.22).
- **proposal scope:** Decide whether to wire the deferred metric source + gateway-scrape fallback into prodsource (reversing the current v1 scoping) and pick the single canonical fallback-marker field.
- **severity:** Medium

### C-31 — §25.6 connectivity connector-probe semantics — §25.6
- **status:** open
- **assigned:** (unassigned)
- **findings:** T-25.6.5
- **root spec gap:** §25.6 requires the connectivity diagnostic to read connectors via `GatewayClient.ListConnectors()` and probe each, but the spec defines no per-connector reachability check, error code, or endpoint, and the code uses a static probe map with no ListConnectors method.
- **proposal scope:** Define what "probe each connector" verifies (upstream/token endpoint, registry entry, or otherwise) and its failure contract, then make the probe set dynamic and add the ListConnectors client method.
- **severity:** Medium

### C-32 — Served OpenAPI/MCP metadata vs spec (response schemas, error taxonomy, tool naming) — §25.12/§25.4/§15.1
- **status:** open
- **assigned:** (unassigned)
- **findings:** T-25.4.21, T-25.12.16, T-25.12.18
- **root spec gap:** The served OpenAPI document omits response-body schemas for the operability surface and the error-envelope taxonomy is contradictory across sections (TRANSIENT|PERMANENT|POLICY|AUTH vs UPSTREAM vs the shared §15.1 enum); separately, generated MCP tool names use `admin.{action}_{noun}` for most tools while §25.12 documents only `lenny_{domain}_{action}`.
- **proposal scope:** Reconcile the error taxonomy and decide the response-body schema source for the operability routes; decide whether the generator emits `lenny_{domain}_{action}` everywhere or §25.12 documents two naming families, then update generator/tests.
- **severity:** Medium

### C-33 — §25.5 event delivery and read-surface filtering semantics — §25.5/§25.15
- **status:** open
- **assigned:** (unassigned)
- **findings:** T-25.5.10, T-25.5.15, T-25.15.10
- **root spec gap:** §25.5 names delivery/read behaviors whose mechanisms are unpinned: the webhook worker re-delivers `event_delivery_failed` back to the failing subscription (spec forbids the loop), the read endpoints are platform-admin-only despite a tenant-scoped-caller rule, and webhook delivery from the gateway buffer during a Redis outage has no data path.
- **proposal scope:** Pin the loop-prevention exclusion mechanism (subject-based skip), define the read-endpoint role + tenant-filter threading, and decide how lenny-ops sources gateway-buffered events for webhook delivery during a Redis outage (with cursor/dedup handling). Coordinate with C-02 (§25.5 read source), which is proposal-A's.
- **severity:** Medium

### C-34 — §25.9 diagnostics audit-retention window — §25.9/§16.4
- **status:** open
- **assigned:** (unassigned)
- **findings:** T-25.9.13
- **root spec gap:** `ops.audit.retention.diagnosticsRetainDays` (default 30) requires a shorter per-event-type retention window, but the §16.4 audit model (partition-drop, 365d general, gdpr carve-out) neither mentions a diagnostics carve-out nor resolves how the two integrate.
- **proposal scope:** Amend §16.4 to define the diagnostics window mechanism (row-level DELETE branch vs separate mechanism), the SIEM-delivery-guard interaction, and the exact prefix set/config plumbing; then implement with a tier2 test.
- **severity:** Medium

### C-35 — §25.11 restore observable contracts and estimators — §25.11
- **status:** open
- **assigned:** (unassigned)
- **findings:** T-25.11.9, T-25.11.16, T-25.11.17
- **root spec gap:** §25.11 fixes restore outcomes (WORKSPACE_SNAPSHOT_MISSING on resume, the restore-status `progress` envelope, the safety-check `safe` flag) but leaves the mechanisms unpinned and unimplemented: no snapshot-absence detection path, no ETA baseline infrastructure, and no concrete DataLossEstimator/ReplicationLagSource.
- **proposal scope:** Decide the adapter/gateway contract for detecting a missing snapshot and how the error surfaces; the ETA combination + gateway-restart baseline source; and the estimator mechanisms (WAL/stat baseline, replication-lag source, orphan-row query) plus their schema changes.
- **severity:** Medium

### C-36 — §25 alerting: WarmPoolReplenishmentSlow thresholds and alert payload labels — §25.13/§16.5/§25.17
- **status:** assigned:proposal-C (2026-07-19). proposal-C's observability/alerting lane — it landed C-19 (§16.10 OpenSLO export) and this is the alerting-rules surface (`pkg/alerting/rules`, §16.5/§25.13 thresholds + §25.17 `alert_fired`/`alert_resolved` payload labels). Sequence after C-42/C-13. Its §25.17 payload work is adjacent to C-44 (§25.4 self-health event, also `pkg/alerting/rules`), which is proposal-C's natural next after this to keep the alerting-rules code single-worker. §25-tagged but the alerting-rule spec/code is distinct from the section-25 closure machine's §25.4/§25.6/§25.8 ops-service test churn; flag the inbox if they collide.
- **assigned:** proposal-C
- **findings:** T-25.13.9, T-25.17.4, T-25.17.5
- **root spec gap:** §16.5 and §25.13 define WarmPoolReplenishmentSlow with incompatible semantics (fixed 2× multiplier vs tier-dependent `ratioBelow`), and `alert_fired`/`alert_resolved` never carry the documented `labels` field, with the spec's own WarmPoolExhausted example needing firing-series labels no static `Rule.Labels` provides.
- **proposal scope:** Pick the authoritative WarmPoolReplenishmentSlow definition and align chart/values; decide the `labels` field intent (merge static labels vs extend the ExprEvaluator/Alert contract to surface matched-series labels) and whether the contract change is in scope now.
- **severity:** Medium

### C-37 — Drift endpoint cross-tenant isolation — §25.10/§12.9.1/§25.4
- **status:** open
- **assigned:** (unassigned)
- **findings:** T-25.10.3
- **root spec gap:** §12.9.1 lists drift detection among paths that must reject cross-tenant access, but §25.10 models drift as platform-scoped with no isolation error and §25.4 admits tenant-admin on every lenny-ops endpoint, so a tenant-admin currently reads the full platform-wide drift report.
- **proposal scope:** Define the control: platform-admin-only drift with a documented isolation error (mirroring LOCK_SCOPE_FORBIDDEN), tenant-scoped drift results, or another mechanism; then the kept skipped test asserts it.
- **severity:** Medium

### C-38 — "Sliding-window" rate-limit naming vs fixed-window implementation — §10.7/§8.3
- **status:** landed:0042 (correct §10.7/§8.3/§8.6 "sliding-window" wording to the shared fixed-window per-minute counter + un-skip the pre-existing tier-1 eval boundary test; explicit cross-boundary-transient doc at both sites; tier-4 clockstep tests declined as redundant to tier-1 primitive coverage; §8.6 auto-mode test left to T-8.6.7)
- **assigned:** proposal-C
- **findings:** T-8.3.16, T-ADV.12
- **root spec gap:** The spec calls both the eval-submission limit (§10.7) and messagingRateLimit (§8.3) "sliding-window," but both share the fixed-window `ratelimit.Counter` that resets on wall-clock minute boundaries, permitting classic window-boundary evasion.
- **proposal scope:** Decide whether the spec wording is imprecise and should be corrected to "fixed-window," or a true sliding-window Redis primitive must be designed and built (decoupled from the shared §11.1 admission counter).
- **severity:** Low

### C-39 — §25.4 canonical operationId cannot disambiguate backup vs backup-verification — §25.4
- **status:** open
- **assigned:** (unassigned)
- **findings:** T-25.4.33
- **root spec gap:** §25.4's canonical operationId format gives KindBackup and KindBackupVerification the same "backup" prefix and the same natural key (the ops_backups id), so a verifying backup projects as two operations sharing operationId "backup-<id>", contradicting the §25.4 Filters contract that an operationId lookup returns a single-element list.
- **proposal scope:** Disambiguate the two backup kinds in the canonical operationId format (distinct prefix or key component) so the lookup is single-valued, and reconcile the §25.4 Filters "single-element list" contract.
- **severity:** Low

### C-40 — §8.3 credential-availability pre-check at delegation time (CREDENTIAL_POOL_EXHAUSTED pre-claim wastage guard) — §8.3
- **status:** landed:0044 (delegation-time §8.3:470 availability pre-check in the delegate_task handler: a new CredentialAvailabilityChecker on *sessionserver.Server reuses resolveCredentialPools/credrouter.PreClaim via a synthetic prospective-child row — one engine for inherit/independent; rejects CREDENTIAL_POOL_EXHAUSTED (POLICY/503) / USER_CREDENTIAL_NOT_FOUND (PERMANENT/404) before the child row is committed, deny/nil-checker skip. Green, reviewClean, 84.1% changed-line coverage. Both open decisions resolved at sign-off: landed now; the racy tier-4 test re-scoped to a deterministic pre-check assertion (T-ADV.11 RESOLVED) and the one-winner/N-1 + pod-release coverage re-filed as T-ADV.14 against the future §8.2 delegate-path pod-claim cluster — see inbox. T-8.3.1 left OPEN with a progress note: deny-mode suppression + the assignment-matrix test remain, pending the integrator's split decision.)
- **assigned:** proposal-C
- **findings:** T-8.3.1
- **root spec gap:** §8.3 requires a general credential-availability pre-check before a delegation claims a pod: if no provider in the intersection has an available credential, reject with `CREDENTIAL_POOL_EXHAUSTED` before claiming (the pre-claim wastage guard), for all `inherit`/`independent` delegations, cross-env or not. This is absent from production code (per T-8.3.1) and only a skipped scaffold exists (`tests/tier4_integration/delegation_credential_pool_race_test.go`). It is DISTINCT from C-20's cross-environment provider-compatibility check (`CREDENTIAL_PROVIDER_MISMATCH`) but shares the same delegate-handler pre-claim insertion point.
- **proposal scope:** Build the §8.3 delegation-time credential-availability pre-check (`CREDENTIAL_POOL_EXHAUSTED` before the pod-claiming Delegate call), reusing the pre-claim step C-20 adds; then un-skip `delegation_credential_pool_race_test.go`.
- **severity:** High
- **relates to:** C-20 (shares the delegate-handler pre-claim insertion point; assigned to the same worker, proposal-C, to keep both on one code path and avoid cross-worker conflict)

### C-41 — §8.3 credentialPropagation deny-mode suppression + full assignment-matrix — §8.3
- **status:** landed:0049 (persisted boolean CredentialDeny marker on sessionstore.Session + migration 0179 + pgstore/memstore round-trip; stamp on the delegated child row; fail-closed suppression in resolveCredentialPools for a deny row AND an inherit hop whose origin is a deny session; SPEC-1 §8.3 origin-chain clarification that a deny hop terminates the inherit chain with no origin pool; tier-4 inherit/independent/deny assignment-matrix + deny→inherit terminator test, tier-9 zero-lease-delivery leakage test, tier-1/tier-2 coverage. Green, reviewClean, 100% changed-line coverage. Both open decisions resolved at sign-off: boolean marker (not full enum); keep SPEC-1. Closes the T-8.3.1 umbrella — RESOLVED across 0043/0044/0049.)
- **assigned:** proposal-C
- **findings:** T-8.3.1
- **root spec gap:** Two pieces of the T-8.3.1 umbrella remain after C-20 (0043: field/enum/validator + cross-env compatibility + inherit shared-pool assignment) and C-40 (availability pre-check): (1) deny-mode credential suppression, which needs a persisted `credentialPropagation` mode column that 0043 deliberately omitted, and (2) the full inherit→independent→deny assignment-matrix behavior. T-8.3.1 is the shared umbrella finding across C-20, C-40, and C-41 — it resolves only when all three land plus the matrix test.
- **proposal scope:** Add the persisted `credentialPropagation` mode column and the deny-mode credential-suppression behavior, and the full inherit→independent→deny assignment-matrix tier-4 test (T-8.3.1's suggested `delegation_credential_propagation_test.go`). Sequence after C-40 (shares the §8.3 delegate-handler path).
- **severity:** High
- **relates to:** C-20 (landed 0043), C-40 (availability) — all share the §8.3 credentialPropagation code; kept single-worker (proposal-C).

### C-42 — Delegate-path step-5 pod allocation and credential-lease assignment — §8.2
- **status:** landed:0054 (integrated `--no-ff` into impl/v1-initial 2026-07-22 on branch-recorded sign-off. Minted 0050 on proposal-C's branch, which COLLIDED with the landed C-22 prereq-1 0050 — renamed to **0054** on integration (next free after 0051 C-17, 0052 C-22-prereq-2, 0053 C-02) and every reference updated (proposal file, TEST-GAPS T-8.2.17 resolution note, spec-map note); the merge commit records the rename. Built the §8.2 delegated-child materialization, option (i) synchronous within `delegate_task`: `MaterializeDelegatedChild` on `*sessionserver.Server` composes the shared claim-and-start helpers (`claimAtCreate` → `resolveCredentialPools`/`credrouter.PreClaim` → `startOnPod` → `store.Update(created→running)` → `registerBinding`), driven from the delegate_task handler via a `ChildMaterializer` seam; fail-closed via existing `rollbackClaim`/`rollbackBinding`, no new pod-claim or release primitive, pure-reuse so no C-22 serialization. SPEC-2 qualified the §8.8:879 + §15.1:630 created-state notes. Fully closes T-8.2.17; unblocks T-ADV.14 (one-of-N race left to the ADV battery). Note: this superseded the earlier in-review spec-only option-(b) draft — it landed as a ~2,300-line code build (17 files) that closes T-8.2.17 by building the materialization rather than reconciling the spec to a deferred model. Verified on integration: `go build ./...` and `go vet` green, validate-maps 15/15, reconcile clean, changed-package tests run; the worker verified tier-1/2/4/9 (the envtest/compose tiers rely on the worker's run + CI).)
- **assigned:** proposal-C
- **findings:** T-8.2.17
- **root spec gap:** §8.2 places warm-pod allocation and credential-lease assignment at delegation step 5 (before the child session id exists), but the delegate handler commits the child in `session.StateCreated` with no `PodAssignment` and defers the warm-pod claim + lease assignment to the shared session-start path. Proposal 0044's delegation-time availability pre-check gates before the child row exists but cannot fail-fast on an actual pod claim, and the §8.2 post-claim assignment race (concurrent delegations collectively exhausting the pool, the loser's pod released, one-winner-of-N `CREDENTIAL_POOL_EXHAUSTED`) has no code path to exercise.
- **proposal scope:** Reconcile the §8.2 delegate-path allocation timing: either move the warm-pod claim + credential-lease assignment inline to step 5 (making 0044's point-in-time pre-check a true pre-allocation fail-fast and enabling the post-claim assignment-race / pod-release / one-winner-of-N outcome), or amend the spec to define delegation-time allocation as deferred to session-start. Turns 0044's gate into an actual inline pod claim where step 5 is chosen.
- **severity:** Medium
- **relates to:** C-40 (landed 0044, the delegation-time availability pre-check this builds on); T-ADV.14 (the re-filed one-winner/N-1 + pod-release race coverage, owned by the ADV battery per the skip-ADV policy — C-42 unblocks it but does not own it); C-01/C-22 (pod/resume work to coordinate the pod-claim path with).

### C-43 — §25.4 canonical RBAC block stale relative to the deployed chart — §25.4/charts
- **status:** open
- **assigned:** (unassigned)
- **findings:** T-25.4.35
- **root spec gap:** The §25.4 canonical RBAC yaml block in `spec/25_agent-operability.md` omits three rules the deployed `charts/lenny/templates/ops-rbac.yaml` actually grants `lenny-ops-sa`: `secrets` get/list/watch/delete, `cert-manager.io` `certificates` get/list/watch/patch, and `endpoints` get/list/watch. The spec's RBAC block and the chart have drifted; the spec is the stale side.
- **proposal scope:** Reconcile the §25.4 canonical RBAC block with the deployed chart — either extend the spec block to include the three chart-granted rules (with the justification for each grant), or narrow the chart if a grant is not warranted. Self-contained spec-vs-chart reconciliation, no code beyond the chart/spec.
- **severity:** Low

### C-44 — §25.4 self-health event naming and replica-identity schema — §25.4
- **status:** open
- **assigned:** (unassigned)
- **findings:** T-25.4.44, T-25.4.28
- **root spec gap:** The §25.4 Self-Monitoring surface has two spec-vs-implementation divergences on the self-health event. (1) T-25.4.44: §25.4 names the same gateway-auth self-health signal two different ways in the same section — kebab-case `gateway-auth` in the "OIDC Token Lifecycle" subsection and snake_case elsewhere — while the code defines `CheckGatewayAuth = "gateway_auth"`; the spec self-contradicts on the canonical name. (2) T-25.4.28: §25.4 ("Multi-replica scope") says self-health events carry replica identity in a `source.replicaID` field, but `pkg/events.OperationalEvent.Source` is a plain CloudEvents URI string and the code emits the identity in `data.replicaId` with `Subject` `ops/<replicaID>`, so the documented `source.replicaID` field does not exist.
- **proposal scope:** Reconcile both: settle the canonical gateway-auth self-health check name (pick kebab or snake and make §25.4 self-consistent, aligning the code's `CheckGatewayAuth` constant), and reconcile the replica-identity location (either define `source.replicaID` as a structured field on the event and emit it, or amend §25.4 to document the actual `data.replicaId` + `Subject` convention). Touches `spec/25`, `pkg/ops/opsservice/selfchecks.go`, `cmd/lenny-ops/servicebody.go`, and possibly `pkg/events.OperationalEvent`.
- **severity:** Low
- **relates to:** clear of the §25.4 aggregator cluster C-29 (health fan-out/recommendations) and C-39 (backup operationId) — this is the self-health event surface specifically. Unassigned; do not overlap with the section-25 closure machine's §25 test churn.

### C-45 — §25.12 MCP-native operational-event subscription — §25.12/§25.5
- **status:** open (filed 2026-07-21 from proposal-A's C-02/0053 inbox item; deferred out of 0053 on human sign-off)
- **assigned:** proposal-A
- **findings:** T-25.12.6
- **root spec gap:** The §25.12 MCP-native operational-event subscription (`notifications/subscribe` + `notifications/message` streaming over `/mcp/management`) has no implementation. The three sentences at spec §25.12 leave the transport, the `notifications/subscribe` param structure, and the `notifications/message` payload envelope underspecified, and the feature restructures the management server's one-request-one-response contract (`pkg/ops/mcp/server.go` decodes exactly one JSON-RPC request and writes exactly one response).
- **proposal scope:** A change-proposal in *new* mode that first stages the §25.12 spec amendment (streaming transport, `notifications/subscribe` params, `notifications/message` payload envelope), then implements the streaming management-server path layered on the §25.5 read source C-02/0053 built. Sequence after C-03 (also §25.12, same `pkg/ops/mcp` server) to keep §25.12 single-worker on proposal-A and avoid two workers in the management server. Depends on C-02 (landed 0053, the read source).
- **severity:** High

### C-46 — §25.5 `EventStreamService` single-implementor aggregator question — §25.5
- **status:** open (filed 2026-07-21 from proposal-A's C-02/0053 inbox item; leans reclassify-to-won't-build pending a spec-normativity check)
- **assigned:** (unassigned)
- **findings:** T-25.5.6 (residual half; the `ListDeliveries` cursor half closed with 0053)
- **root spec gap:** The eight-method `EventStreamService` interface (`pkg/ops/events/interface.go`) has zero runtime consumers — no production code accepts, returns, injects, or stores it, and no `var _ EventStreamService = …` assertion exists. The §25.5 read side (0053) lands against the concrete types; the HTTP routes are the contract. The open question is whether a single production type must satisfy the interface and be asserted through a canonical contract test, or whether the interface is illustrative documentation.
- **proposal scope:** Confirm against §25.5 whether the `EventStreamService` interface is normative (a required production contract) or illustrative. If illustrative, reclassify T-25.5.6 as won't-build with the spec citation (building an aggregator solely to satisfy an assertion against a consumer-less interface adds a synthetic parallel type). If normative, define the single implementor and its contract test. Small, self-contained; whoever takes it should confirm the normativity before writing code. Clear of proposal-A's active §25.12 work (C-02/C-03/C-45) since it is the §25.5 interface-normativity question, not the read path.
- **severity:** Low

### C-47 — Coordinator-unavailable eviction-checkpoint edge case: spec explicitness + docs coverage — §4.6.1/§4.4/§10.1
- **status:** open (filed 2026-07-22 by the integrator at the user's request, on review of the landed 0052)
- **assigned:** proposal-B
- **findings:** (none pre-existing — discovered on 0052 integration review 2026-07-22; this is a documentation-completeness gap, not a test-coverage gap. The tier-11 route-consistency test 0052 added already pins the §4.6.1 routing.)
- **root spec gap:** Proposal 0052 (C-22 prereq-2) landed the coordinator-direct eviction-checkpoint routing at §4.6.1 and, with human sign-off, its accepted edge case: when the coordinating gateway replica is unreachable within the pod's termination grace window, no replica drives the eviction checkpoint (fail-closed), and recovery is deferred to the §10.1 TTL-driven coordinator handoff — whose TTL can outlast the grace window, so the session can terminate before adoption and resume from the last periodic checkpoint (loss bounded by the §4.4 checkpoint-freshness SLO). Two documentation gaps remain: **(1) spec explicitness** — §4.6.1 states the recovery mechanism ("session is recovered through the TTL-driven handoff") and the fail-closed rule, but not the accepted outcome, that the eviction checkpoint may not complete and up to `periodicCheckpointIntervalSeconds` of workspace state can be lost; that explicit statement lives in the 0052 proposal, not in the spec, and a reader must stitch §4.6.1 (cause) to §4.4 (loss bound) to reconstruct it. **(2) docs coverage** — the operator-facing docs (`docs/operator-guide/disaster-recovery.md`, `docs/runbooks/*`, `docs/reference/metrics.md`) document eviction-checkpoint failure only for the storage cause (MinIO/Postgres unavailable), never for the coordinator gateway-replica-unavailable cause, so an operator cannot learn that a gateway-replica outage during an agent-pod eviction can skip the checkpoint.
- **proposal scope:** (a) **Docs** (no spec change required; the behavior is already in §4.6.1 + §4.4): add the coordinator-gateway-unavailable cause to the eviction-checkpoint failure narrative in `docs/operator-guide/disaster-recovery.md`, cross-linking the §4.4 freshness-SLO loss bound and the §10.1 handoff, so the failure story covers both the storage cause and the coordinator cause. (b) **Optional spec sharpening** via the change-proposal pipeline: extend §4.6.1 to state the accepted outcome explicitly (eviction checkpoint may not complete when the coordinator is unreachable within the grace window → resume from the last periodic checkpoint, ≤ `periodicCheckpointIntervalSeconds` loss) rather than leaving it implicit in "session is recovered." The runner decides whether (b) is warranted or whether §4.6.1 + §4.4 already suffice and only (a) is needed.
- **severity:** Low (documentation completeness; operationally relevant — an operator-facing data-loss edge case)
- **relates to:** C-22 (0052 introduced this edge case; 0050/0052 are proposal-B's). Assigned to proposal-B to keep the §4.6.1 eviction narrative single-worker; it directly extends its own 0052 and can sequence at proposal-B's discretion within its eviction lane.

### C-48 — Pool-config admission termination-grace floor: nil-bypass + gateway/agent conflation — §5.2/§10.1/§16.1/§17.2
- **status:** landed:0056 (filed and integrated together 2026-07-22 by the integrator on branch-recorded sign-off; proposal-B surfaced this standalone defect while analyzing C-22 prereq-3, which itself turned out a no-op — see C-22). The worker minted 0054, which collided with the landed C-42 0054, so the worker renumbered to **0056** on its own branch before push (next free after 0055 C-03); no integrator rename was needed. Clean `--no-ff` merge (no conflicts). Fixes the `SandboxWarmPool`/`SandboxTemplate` pool-config admission webhook (`decideTerminationBudget`): (1) nil-bypass — an omitted `terminationGracePeriodSeconds` was never vetted against the §5.2/§10.1 floor; now evaluated against the effective grace (declared value, else the §4.6.1 120s default) and rejected fail-closed when below the floor; (2) gateway-vs-agent conflation — dropped the gateway-drain `checkpointBarrierAckTimeoutSeconds` term from the agent floor (`maxConcurrentSessions × max_tiered_checkpoint_cap + 30`), staged across §5.2/§10.1/§16.1/§17.2, the CRD field doc comments + regenerated manifests, the validator, and the webhook wrapper. Verified on integration: `go build` + `go vet` green, validator+webhook `-race` tests pass, tier-2 admission envtest passes, validate-maps 15/15, CRD overlay preserved (integrator confirmed `make generate` strips the manual `lenny.dev/schema-version` overlay, so the worker's committed manifests were kept rather than a blind regen). Not live data loss today (the agent preStop only SIGTERMs the local adapter; the eviction-checkpoint routing is the unbuilt C-22 trigger T-4.4.14) — an admission-correctness fix hardening the invariant for when the trigger lands.
- **assigned:** proposal-B
- **findings:** T-5.2.15 (filed RESOLVED by the integrator on integration; the defect closed no pre-existing finding)
- **root spec gap:** none — this is an implementation defect against the existing §5.2/§10.1 grace-floor invariant, surfaced and fixed within the same proposal, not a spec gap.
- **severity:** Medium
- **relates to:** C-22 (surfaced while analyzing prereq-3; does NOT touch the C-22 trigger T-4.4.14, prereq-4, or the gateway-pod grace §17.8).

### C-49 — §7.2 path-6 remaining pieces: pod-release sweep, adapter readiness, ForwardMessage, MCP delivery field — §7.2/§6.2/§4.7/§10.1
- **status:** open (filed 2026-07-22 by the integrator from proposal-A's C-14/0058 inbox item; the deferred cross-section follow-ons of the §7.2 path-6 build 0058 landed)
- **assigned:** proposal-A
- **findings:** T-JRN.13, T-JRN.14, T-JRN.15, T-JRN.16 (also closes T-JRN.1's remaining tier-5 real-pod half, blocked on T-JRN.14)
- **root spec gap:** Proposal 0058 (C-14) built the reachable pod-held branch of §7.2 path-6 atomic resume-and-deliver against the in-process echo executor and single-replica coordinator path, deferring four cross-section prerequisites that make the remaining branches reachable: **T-JRN.13** (§6.2 pod-release-during-suspension sweep — the podless-suspended branch is unreachable until a `sweepSuspended` pass releases a pod past `maxSuspendedPodHoldSeconds`), **T-JRN.14** (§4.7 adapter `ready_for_input` readiness signal + 30s delivery timeout + real-pod in-place adapter resume — the coordinator resume returns after the state write with no readiness signal against the always-ready echo executor; also holds T-JRN.1's tier-5 real-pod half), **T-JRN.15** (§10.1 `ForwardMessage` cross-replica coordinator forwarding — untested because the single-replica path never forwards), and **T-JRN.16** (MCP `lenny/send_message` delivery-field threading — an inter-session immediate message is not expressible because the tool schema exposes no `delivery` field).
- **proposal scope:** Per the human's request, tackle the four deferred pieces as ONE bundled follow-up change-proposal (proposal-A is authoring it, branched off post-0058 code). Each piece makes an already-designed §7.2 path-6 branch reachable/testable rather than adding new behavior. **Coordinate T-JRN.13 (§6.2 pod-release-during-suspension sweep) against proposal-B's C-22 (§4.4/§4.6 eviction, warm-pod-adjacent)** via the inbox so the pod-release sweep is not double-claimed across the two workers.
- **severity:** Medium
- **relates to:** C-14 (landed 0058, the path-6 build these extend); C-22 (proposal-B's eviction lane — coordinate the §6.2 sweep, T-JRN.13).

---

## Test-infra findings to de-escalate (hand back to closure runners)

These 52-backlog findings are test-infrastructure or harness-state questions, not spec gaps. Under the `close-test-gaps` step-5a route (a new or modified test must not break or invalidate an existing test), a closure runner settles each. The integrator de-escalates them in `TEST-GAPS.md` (removes the needs-human annotation, returns them to plain OPEN) once step 5a is live, so a runner's Select re-offers them.

| Finding | Severity | Why test-infra-only |
|:--|:--|:--|
| T-4.5.2 | High | S3/Azure real-backend test tier choice; no spec change. |
| T-4.7.1 | High | Tier-10 RED from a harness spec-tag vs test-assertion mismatch; widen the assertion. |
| T-4.7.5 | High | Wire a credential-delivery fixture into Kind, or rely on existing tier-1/2 coverage. |
| T-25.16.1 | High | Already moot; confirm the orphaned commit landed and re-verify. |
| T-JRN.2 | High | Rebuild the stale Kind cluster, un-skip, run the committed test. |
| T-JRN.10 | Medium | Build a Docker-gated tier-5 `lenny up` journey test; spec is clear. |
| T-JRN.11 | Low | Test-scoping decision on auth/isolation axes; spec settled. |
| T-JRN.12 | Info | Blocked on externally published runtime images/credentials; not repo-closable. |
| T-DEP.1 | High | Needs live sandboxed-isolation cloud infra; no test-only close. |
| T-DEP.13 | Info | Cross-provider TestMain skip-vs-fail is a harness design choice. |
| T-BED.9 | Medium | Boot the real gateway composition root in-process, or reconcile TESTING.md wording. |
| T-BED.12 | Low | Already moot (air-gap test landed); re-verify the commit. |
| T-BED.13 | Low | Test-scoping decision on auth axes; spec settled. |
| T-BED.14 | Info | File-rename/unused-workload cleanup; no behavioral gap. |
| T-25.1.6 | Info | Scope enforcement is pinned at unit/contract; the ask is a live-deployment (tier-5) enforcement test. Spec settled — a runner writes the tier-5 test with a §25.12-tight final assertion (the prior attempt's assertion was looser than §25.12 requires). |
| T-25.15.6 | Info | §25.15-25.17 are classified non-normative for coverage accounting; the ask is aggregate-journey (§25.17 loop) tracking. Prior route=moot attempt landed no commitSha (orphaned). A runner re-verifies/re-lands the aggregate-journey coverage; no spec change. |
| T-MAP-1 | Low | Spec-map hygiene: 18 `tests[]` entries whose `::TestName` selector points at a renamed/removed function while the file still exists. Surfaced by the new `validateSpecMapTestFuncs` gate C-14/0058 added (currently green via a pending-allowlist). A closure runner repoints each dangling selector to the current function name (or drops the stale entry) and removes it from the allowlist; no spec change. |

---

## Integrator inbox

Feedback items from proposal workers and closure runners for the integrator to
action. Append a `- [ ] from <worker> (<proposal/branch>): <ask>` line on your
own branch; the integrator processes it on integration and checks it off `- [x]`
with the action taken, keeping a short trail here.

- [x] from proposal-C (0039): list T-4.8.16 under C-18's `findings:` — it shares C-18's root gap and 0039 closed it. → Done 2026-07-16: added T-4.8.16 to C-18 findings (already RESOLVED by 0039 in TEST-GAPS.md).
- [x] from proposal-C (0039 §5): file a new finding for the §8.7 export path mapping every `*ExportScanError` (incl. cataloged `EXPORT_FILE_SCAN_REJECTED`) to `INTERNAL_ERROR`/nil-details because it is not matched as a `*ToolError` — documented in 0039 §5, not yet a TEST-GAPS entry. → Done 2026-07-16: filed T-8.7.14 under §8.7.
- [x] re-cluster 2026-07-16: 50-finding backlog clustered. Added C-23–C-38 (16 new clusters, unassigned) and folded T-25.8.20/T-25.15.8→C-08, T-25.9.14→C-09, T-25.9.12→C-11.
- [~] pending fold-in to ASSIGNED clusters: T-25.3.5, T-25.5.20 → C-02 — Done 2026-07-19: folded into C-02 findings by proposal-A at branch start (both share T-25.5.1's read-source root gap). Still pending: T-25.12.11 → C-03 (§25.12 dispatch; its chaos test is blocked on T-25.12.3).
- [x] test-infra handback from re-cluster: de-escalated T-ADV.11, T-ADV.13, T-25.2.2, T-25.12.22 (runner-closable). Kept needs-human as operator-resource-blocked: T-25.4.37 (EKS/cloud), T-25.11.7 (tagged buckets + cloud creds), T-25.6.1 (operator helm upgrade for RBAC).
- [x] re-cluster 2026-07-17: folded T-25.4.38 into C-29 (recommendation aggregator, same root gap); added C-39 for T-25.4.33 (backup operationId disambiguation); de-escalated test-infra T-25.4.12 (orphaned-commit re-verify).
- [x] re-cluster 2026-07-17: folded T-25.6.14→C-23 (gateway :8443 TLS/HTTP probe collision) and T-25.6.17→C-24 (§25.6 both-down 503 error code) — both match existing unassigned clusters.
- [x] pending fold-in to ASSIGNED cluster C-02 (§25.5 MCP-native subscription, proposal-A): T-25.5.14 — whether webhook-subscription CRUD should be MCP-exposed or REST-only is deliberate. → Done 2026-07-19: folded into C-02 findings by proposal-A at branch start; the MCP-subscription scope now settles the event-subscription CRUD MCP exposure (the verifier showed the CRUD verbs are already MCP tools, so the REST-only premise is false).
- [x] from proposal-C (C-20): route the §8.3 credential-availability pre-check (CREDENTIAL_POOL_EXHAUSTED pre-claim guard) as its own cluster, distinct from C-20's compatibility check. → Done 2026-07-17: created C-40 (findings T-8.3.1), assigned to proposal-C since it shares C-20's insertion point.
- [x] from proposal-C (C-40): T-8.3.1 is broader than C-40 — deny-mode suppression (needs persisted mode column, omitted by 0043) and the full assignment-matrix test remain. → Done 2026-07-18: split into new cluster C-41 (proposal-C), keeping C-40 scoped to the availability pre-check. T-8.3.1 is the umbrella across C-20/C-40/C-41 and stays OPEN until all land; leave it OPEN with a note when C-40 lands, as you planned.
- [x] re-cluster 2026-07-18: folded §25.8 upgrade findings T-25.8.15/.23/.24 → C-08, and §25.7 stale-runbook-count T-25.7.10 → C-26 (all existing unassigned clusters).
- [x] to proposal-A (C-01 / `proposal-a/c-01-checkpoint-roundtrip`): the branch was 380 commits behind impl/v1-initial and could not be integrated as-is; requested an on-branch resync + renumber. → Done 2026-07-18: proposal-A merged impl/v1-initial into the branch, resolved every conflict on-branch (proto regenerated, `gatewayFlags` struct unioned, spec-map + migration column list unioned), renumbered proposal 0037→0045 / withdrawn 0036→0046 / draft 0038→0047 and migration 0175→0178, and re-pushed with a 0-conflict merge-base at impl's tip. Integrated `--no-ff` (0 conflicts): `go build ./...` green, validate-maps 15/15, changed-package tests pass, reconcile clean (1783 findings, 382 resolved). C-01 status now landed:0045.
- [x] from proposal-C (C-40): T-8.3.1 broader than C-40 — split deny-mode + assignment-matrix out. → Superseded by the 2026-07-18 C-41 split above (same ask); T-8.3.1 stays OPEN across C-20/C-40/C-41.
- [x] from proposal-C (C-40): FILE A NEW CLUSTER for the §8.2 delegate-path pod-claim and credential-lease-assignment flow (spec §8.2 steps 5-9). Spec-vs-code divergence: `spec/08_recursive-delegation.md` states pod allocation at step 5 (delegation time, before the child session id exists), but the code commits the child in `session.StateCreated` with no `PodAssignment` (`pkg/gateway/mcpfabric/delegation/service.go`) and defers the warm-pod claim + credential-lease assignment to the shared session-start path (`resolveCredentialPools` → `credrouter.PreClaim`, `pkg/gateway/sessionserver/start.go`). Pod-claim-touching → proposal-A's lane or coordinated with C-01. Depends on C-40/0044 (landed). Relates to T-8.1.3. Attach T-ADV.14 (re-filed race test) + a new production finding for the step-5 pod-allocation divergence. Upgrades 0044's point-in-time pre-check into a true inline pre-allocation fail-fast and enables the post-claim assignment-race / pod-release / one-winner-of-N outcome 0044 left out of scope. → Done 2026-07-18: created **C-42** (§8.2 delegate-path step-5 pod allocation), unassigned, root finding the newly-filed production divergence **T-8.2.17** (T-8.2.14 was already taken). T-ADV.14 already exists as a Low ADV-category finding and is handled by the separate ADV machine per the skip-ADV policy, so C-42 references it as related coverage rather than owning it. Held for proposal-A's lane per the pod-conflict rule; not assigned yet (proposal-A on C-14, and C-22 eviction is proposal-B's active §4.x pod work — assign after those quiesce to avoid a three-way pod-claim collision).
- [x] from proposal-B (0050): C-22 resolved prerequisite-first; keep C-22 open/assigned for prereqs 2-4+trigger. → Done 2026-07-19: integrated 0050 (prereq 1, slot-aware checkpointing); C-22 status set to open with prereq-1 landed:0050; C-22 stays assigned:proposal-B. Original ask: C-22 is being resolved prerequisite-first per the reviewer's decision, as a sequence of small proposals rather than one large one. **0050 = prerequisite 1 (slot-aware checkpointing)**: `slot_id` on `CheckpointStart`, slot-scoped `checkpointRoots()`, a slot-scoped op-lock (adversarial review found the pod-global op lock silently drops concurrent per-slot checkpoints on the drain path, contradicting spec/05:542), plus a §4.7 op-lock queue-depth spec edit. It closes no C-22 finding by itself (the trigger does) and it unblocks — but does not close — T-4.4.21; it leaves T-4.4.14/T-4.6.4 for the later trigger proposal. Prerequisites 2-4 (coordinator compare-and-steal, agent-pod grace budget, runtime-container preStop) and the trigger will follow as separate proposals under C-22. When 0050 lands, keep C-22 in-review/converging (not closed) until the whole sequence is done. Please keep C-22 assigned to proposal-B for the sequence.
- [x] from proposal-B (0050): note T-4.4.21 capture-implemented-but-still-OPEN, leave T-4.4.14/T-4.6.4 for the trigger.
- [x] from proposal-B (0051 / `proposal-b/c-17-ssa-conflict-retry`): **0051 signed off and IMPLEMENTED green — ready for `--no-ff` integration.** Spec applied as its own commit (§16.1 counter semantics, §4.6.3 item 3 continuation-via-requeue, §16.5 alert description), then code/doc/test commits: both byte-divergent `retryOnConflictSSA` copies consolidated into the new shared `pkg/controller/ssaretry` (registered `lenny_crd_ssa_conflict_total{crd,controller}` CounterVec + the `crd_ssa_conflict_stuck` log emitted together at the five-consecutive-409 boundary, gated on a non-empty CRD kind so the host-schedulable Pod apply stays silent), all nine call sites repointed, alert description regenerated into its three committed copies, metrics-reference row added, spec-map repointed. Independently verified: tier 0 build/vet clean and lint clean on all touched code (6 remaining lint findings all pre-exist in untouched code — `podspec_test.go` is byte-identical to baseline; warmpool's three are the same findings shifted by the deleted helper), tier 1 `ssaretry`/`sandbox` pass under `-race`, tier 2 alerting-rules currency passes, tier 11 docs suite passes. 98.4% changed-line coverage. T-4.6.7/T-4.6.17/T-16.1.10 marked RESOLVED; **the TEST-GAPS summary-counts line is deliberately left stale (recomputes to 401 resolved / 1390 open) for you to update** — `reconcile --check` reports only that, no duplicate IDs. → Done 2026-07-21 (integrator): C-17 integrated `--no-ff` as landed:0051; go build + ssaretry tier-1 green, validate-maps 15/15, TEST-GAPS summary refreshed by reconcile (409 resolved/1385 open); merged branch deleted.
- [x] from proposal-B (infra observation, not introduced by 0051): **`pkg/controller/warmpool` runs within ~2% of Go's default 600s test timeout** and flips to a timeout failure under any concurrent envtest load. Measured on this branch: 587s solo (PASS) and 600.05s (FAIL, timeout) while other envtest suites ran; the pre-0051 baseline measured 598.68s (PASS) — i.e. ~1.3s of margin. The hang is `controller-runtime .../process.pollURLUntilOK`, the envtest control plane failing to start under contention, rather than a product defect. Suggest running that package with an explicit `-timeout` (or splitting the suite) in CI and when re-running tiers on integration, so a green change does not read as red. Filed as an inbox note rather than a TEST-GAPS finding since it is test-infrastructure timing, not a spec-coverage gap. → Done 2026-07-19: T-4.4.21 given a Progress note (0050 implemented the capture side, unblocks-not-closes; stays OPEN); T-4.4.14/T-4.6.4 untouched. Integration: build green, adapter/checkpointer/barrier tests pass, static PASS. Original: **0050 signed off and IMPLEMENTED green — ready for `--no-ff` integration.** Spec applied as its own commit (§4.7 op-lock queue-depth amendment; `spec/04_system-components.md`), then the proto/code/test changes across five commits (proto `slot_id` on `CheckpointStart` + tier-3 contract pin; per-slot op-lock admit/promote-in-slot-ID-order; slot-scoped `checkpointRoots()` + unassigned-slot gate; gateway sends raw `binding.SlotID`; adapter/barrier/tier-4 tests). Independently re-verified under `-race`: tier 0 build/vet/lint clean on touched files (4 remaining lint findings pre-exist in untouched files), tiers 1/7a all adapter+checkpointer+barrier packages pass, tier 3 wire pin passes, tier 4 per-slot capture round trip passes (in-process harness, no external infra). 98.3% changed-line coverage, design-conformance review clean, no proposal-internal identifiers in code/comments/commits. On land, per this file's convention, note T-4.4.21 in TEST-GAPS.md as capture-implemented-but-still-OPEN (the restore-side companion change closes it; 0050 unblocks but does not close it), and leave T-4.4.14/T-4.6.4 for the later trigger proposal. Keep C-22 assigned/open for prereqs 2-4 + trigger. → Noted 2026-07-21 (integrator): heeded — this session verified the C-17 land via go build + ssaretry tier-1 + validate-maps (not the warmpool envtest), so the near-600s timeout did not gate integration. Recommendation to run warmpool with an explicit -timeout / not co-run under contention stands. Kept as an infra note, no TEST-GAPS finding.
- [x] from proposal-A (0053 / `proposal-a/c-02-ops-event-stream-read`): FILE A NEW CLUSTER for the §25.12 MCP-native operational-event subscription (`notifications/subscribe` + `notifications/message` streaming over `/mcp/management`), finding **T-25.12.6**. Deferred out of C-02/0053 on human sign-off: it needs a §25.12 **spec amendment first** (the three sentences at spec/25:4661-4663 leave the transport, the `notifications/subscribe` param structure, and the `notifications/message` payload envelope underspecified) and it **restructures the management server's one-request-one-response contract** (`pkg/ops/mcp/server.go:119-144` decodes exactly one JSON-RPC request and writes exactly one response). It layers on the §25.5 read source 0053 builds, so sequence it after 0053 lands. Recommend a change-proposal in *new* mode staging the §25.12 transport/param/payload amendment before the server code. T-25.12.6 stays OPEN meanwhile. Suggested cluster name: "§25.12 MCP-native event subscription". → Done 2026-07-21 (integrator): filed C-45 (§25.12 MCP-native operational-event subscription, T-25.12.6), assigned proposal-A, sequenced after C-03 to keep pkg/ops/mcp single-worker; depends on C-02/0053. T-25.12.6 stays OPEN.
- [x] from proposal-A (0053 / `proposal-a/c-02-ops-event-stream-read`): tier-7a load-scenario flake, NOT from this branch (no podclaim/slot code touched) — `TestScenarios/slot_leaked_counted_race` (§4.6/§8 pod-slot-counter release path) breaches its latency floor once (`floor_breaches=1`, `leaked_release=64`, `context deadline exceeded`, ~0.03% error) under full-suite host/Docker contention, but PASSES 2/2 in isolation. Observed while verifying 0053's tier-7a run. For a §4.6/§8 owner to assess (tune the floor or investigate the release tail latency); low severity. Not filed as a TEST-GAPS finding since it is environmental load-sensitivity in another cluster's domain, not a coverage gap. → Noted 2026-07-21 (integrator): recorded for a §4.6/§8 owner; environmental load-sensitivity, not a coverage gap, so no cluster/TEST-GAPS finding. This is the durable trail.
- [x] from proposal-A (0053 / `proposal-a/c-02-ops-event-stream-read`): FILE A NEW CLUSTER (or reclassify) for the unified `EventStreamService` production aggregator, finding **T-25.5.6**. Deferred out of C-02/0053: the eight-method interface (`pkg/ops/events/interface.go:26-35`) has zero runtime consumers — no production code accepts, returns, injects, or stores it, and no `var _ EventStreamService = …` assertion exists; the HTTP routes are the contract and 0053's read-side build lands against the concrete types. Building an aggregator solely to satisfy an assertion against a documentation-only interface adds a synthetic parallel type with no consumer, so 0053 did not build it. Note: 0053 DOES build the `ListDeliveries` keyset cursor and amends §25.5 to document deliveries pagination (the cursor sub-question of T-25.5.6/T-25.5.7 closes with 0053); the residual for this cluster is only the single-implementor-of-the-8-method-interface question. The integrator may prefer to reclassify T-25.5.6 as won't-build (interface is illustrative) rather than open a cluster. T-25.5.6 stays OPEN meanwhile. → Done 2026-07-21 (integrator): filed C-46 (§25.5 EventStreamService single-implementor question, residual T-25.5.6), unassigned, leaning reclassify-to-won't-build pending a spec-normativity confirmation. T-25.5.6 stays OPEN.
- [x] from proposal-B (0052 / `proposal-b/c-22-coordinator-compare-and-steal`): **0052 signed off and IMPLEMENTED green — ready for `--no-ff` integration.** C-22 prerequisite 2. The trigger-seam open decision was resolved as coordinator-direct, which collapsed the change to a single §4.6.1 spec-prose clarification (the evicted agent pod's preStop cannot open the gateway-driven `Checkpoint` stream, so it signals its coordinating replica over the existing per-pod control channel — the `AdapterTerminating` transport — and that replica, the session-coordination-lease holder, drives `CheckpointWithTrigger(TriggerEviction)` under its held lease; an unreachable coordinator is recovered via the §10.1 TTL-driven handoff, fail-closed). Spec applied as its own commit (`54d9791c`); one tier-11 route-consistency test added (`tests/tier11_docs/eviction_coordinator_route_consistency_test.go`) with spec-map entries under §4.6.1/§4.7/§4.4. No product code, proto, CRD, chart, or migration touched. Independently verified: tier 0 build/vet/lint clean, tier 11 full package passes, `validate-maps` 15/15. TEST-GAPS untouched — 0052 closes no finding; it unblocks but does not close T-4.4.14/T-4.6.4 (the trigger closes those). Branch merged onto current impl/v1-initial (0 behind). Keep C-22 open/assigned to proposal-B for prereqs 3-4 + the trigger; the forced-takeover of an unreachable coordinator, the forward-to-holder transport, and any takeover primitive are deferred to the trigger-wiring proposal, gated on the (now-resolved) coordinator-direct seam. → Done 2026-07-22 (integrator): C-22 prereq-2 integrated `--no-ff` as landed:0052 on branch-recorded sign-off (settled policy); spec §4.6.1 prose + tier-11 route-consistency test, no product code; go build green, validate-maps 15/15, tier-11 test passes, reconcile clean; merged branch deleted. C-22 stays open/assigned to proposal-B for prereqs 3-4 + trigger.
- [x] from proposal-B (0056 / `proposal-b/pool-grace-floor-nil-default`): **NEW standalone finding + proposal — please create/assign a cluster and file the TEST-GAPS finding.** Surfaced while analyzing C-22 prereq 3 (which turned out a no-op). Defect: the `SandboxWarmPool`/`SandboxTemplate` pool-config admission webhook (`pkg/admission/pool_config_validator/validator.go` `decideTerminationBudget`) has two coupled bugs. (1) **Nil-bypass:** the §5.2/§10.1 termination-grace floor reject is guarded by `spec.TerminationGracePeriodSeconds != nil` (`validator.go:644`); the CRD field has no `+kubebuilder:default` and no defaulting webhook, so an omitted field is never vetted and the pool admits with the §4.6.1 120s render. (2) **Gateway-vs-agent conflation:** the floor is computed as `maxConcurrent × tierCap + checkpointBarrierAckTimeoutSeconds + 30`, but `checkpointBarrierAckTimeoutSeconds` is a *gateway*-drain wait; the agent pod's own preStop drain performs no BarrierAck wait, so the term over-provisions the agent floor and (perversely) rejects a trivially-configured single-slot pool set to the documented §4.6.1 120s default (210s floor). 0056 reconciles both: evaluate the floor against the *effective* grace (declared value, else the 120s default) and drop BarrierAck from the *agent* floor → `maxConcurrentSessions × max_tiered_checkpoint_cap + 30`, staged consistently across §5.2/§10.1/§16.1/§17.2, the CRD field doc comments + regenerated manifests, the validator, and the wrapper + tier-1/tier-2 tests. **Not live data loss today** (the agent preStop `runPreStop` only SIGTERMs the local adapter; the eviction-checkpoint routing is the unbuilt C-22 trigger T-4.4.14) — it is an admission-correctness fix that hardens the invariant for when the trigger lands. Closes no existing finding; does NOT touch the C-22 trigger or prereq 4 or the gateway-pod grace (§17.8). **Status: signed off (both decisions ratified: drop BarrierAck from the agent floor; reject-omission fail-closed) and IMPLEMENTED green 2026-07-22 — ready for `--no-ff` integration.** Tiers 0/1/2 independently verified (validator+webhook -race, tier-2 admission envtest incl. omitted-field codec round-trip), 100% changed-line coverage, CRD manifests regenerated with the manual overlay preserved (charts/embedded byte-identical), TEST-GAPS untouched. This is admission/warm-pod territory (proposal-B's lane); suggest a new cluster assigned to proposal-B. → Done 2026-07-22 (integrator): filed cluster **C-48** (landed:0056) and finding **T-5.2.15** (RESOLVED) for this standalone admission-correctness defect; integrated 0056 `--no-ff` on branch-recorded sign-off (worker had already renumbered 0054→0056, no rename needed). Verified build/vet green, validator+webhook -race + tier-2 admission tests pass, validate-maps 15/15; kept the worker's committed CRD manifests (confirmed `make generate` strips the manual overlay). Noted in C-22 that prereq-3 (grace budget) is a no-op discharged by this fix; C-22 stays open for prereq-4 + trigger.

- [x] from proposal-A (0058 / `proposal-a/c-14-atomic-resume-deliver`): the §7.2 path-6 deferrals are recorded as OPEN follow-on findings **T-JRN.13** (§6.2 pod-release-during-suspension sweep), **T-JRN.14** (§4.7 adapter `ready_for_input` + 30s timeout + real-pod in-place adapter resume; also holds T-JRN.1's tier-5 half), **T-JRN.15** (§10.1 `ForwardMessage` cross-replica forwarding), **T-JRN.16** (MCP `lenny/send_message` delivery-field threading). Per the human's request these will be tackled as ONE bundled follow-up proposal (proposal-A is authoring the change-proposal next, branched off post-0058 code). **Coordinate T-JRN.13 (§6.2 sweep) against proposal-B's C-22 (§4.4/§4.6 eviction, warm-pod-adjacent)** so the pod-release sweep is not double-claimed. Suggested new cluster name: "§7.2 path-6 remaining pieces (pod-release sweep, adapter readiness, ForwardMessage, MCP delivery field)". → Done 2026-07-22 (integrator): filed cluster **C-49** (§7.2 path-6 remaining pieces — T-JRN.13/.14/.15/.16, also closes T-JRN.1's tier-5 half), assigned proposal-A per your bundled-follow-up plan; recorded the coordinate-with-C-22 note on T-JRN.13 (§6.2 sweep) in the cluster.
- [x] from proposal-A (0058 / `proposal-a/c-14-atomic-resume-deliver`): a new test-infra validator (`cmd/lenny-test validate-maps` → `validateSpecMapTestFuncs`) added to guard the test-function rename this proposal made surfaced **T-MAP-1** — 18 pre-existing spec-map `tests[]` entries whose `::TestName` selector points at a renamed/removed function while the file still exists (dangling refs). Recorded OPEN for a closure runner; not fixed here (out of C-14's scope). The validator itself is green and additive. → Done 2026-07-22 (integrator): added **T-MAP-1** to the test-infra de-escalate table (closure-runner-closable spec-map hygiene: repoint the 18 dangling ::TestName selectors, remove from the allowlist). The new validateSpecMapTestFuncs gate is live and green (16/16) via the pending-allowlist; not blocking integration.
- [ ] from proposal-B (0060 / `proposal-b/coordinator-binding-colocation`): **jaf-directed ARCHITECTURAL proposal — please create/assign a cluster.** Co-locate the session-coordination lease with the pod binding so the coordinating replica always serves the session (the spec's assumed-unified model the code never wired). Mechanism: acquire the lease at BIND time on the binding replica (registerBinding + hoisted ahead of the running-commit on single-call /start, delegated-child, checkpoint-restore); reconcile the Sweeper into a renewer + crash-takeover adopter (adopts only a lapsed lease of a still-running-pod session; probes held-channel liveness and evicts binding+executor-stream+releases-lease on a dead channel); re-establish the binding EAGERLY on the crash-takeover edge via a fence-first re-adopt (dial + CoordinatorFence before the version handshake since the pod is in §10.1 hold state, publish on fence-ack, hold the connection); relinquish+backoff on terminal fence failure; retire the (production-dead) barrier fresh-dial. NO spec edit (the invariant + re-adopt-on-handoff are already normative in §10.1/§4.6.1/§7.2/§7.3 — the gap is code wiring), NO forced-acquisition, NO checkpoint restore, NO new wire field. Converged HARD: 12 rounds, 2 clean, 19 findings fixed (deep distributed-coordination bugs: lapsed-vs-fresh lease conflation, lease-steal windows, fence-first ordering, dead-channel recovery, executor-stream-cache shadowing). Reaches tiers 0/1/2/4/7a/8; builds the two-replica gateway harness (closes T-4.2.4). **Deliberately scoped OUT: the off-holder REST forwarding (messages/interrupt/upload-to-session over ForwardMessage) = C-49/T-JRN.15 — co-location establishes the INVARIANT that makes forward-to-coordinator correct; the forward wiring is that separate cluster.** Downstream (not built here): unblocks simplifying the C-22 eviction trigger's coordinator_address/fresh-dial workaround (proposal-b/c-22-eviction-trigger, paused). Status: **IMPLEMENTED GREEN — jaf signed off 2026-07-25 and directed implementation; independently verified, ready for `--no-ff` integration.** All open decisions ratified on sign-off (D1 acquire-at-bind, D2 re-adopt-eagerly staged; D3 fresh-dial RETIRED not gated; D4 forwarding out-of-scope to C-49/T-JRN.15; resume-path `ErrHeld` fail-closed interim). NO spec edit (validate-maps unchanged). Independently verified green on reached tiers 0/1/2/4/7a/8: tier-0 build/vet clean (the 13 lint findings are all pre-existing in files this branch does not touch); tier-1 executor/barrier/adapterclient/cmd/coordination `-race`; tier-2 envtest podsession + sessionserver + tier2-component/coordination (run SOLO); tier-4 `TestCoordinationSplitBrainFenceAcrossTwoReplicas_spec_10_1` on the compose stack; tier-7a `TestColocationInvariantUnderConcurrentHandoff_spec_10_1` `-race`; tier-8 `TestCoordinatorFailoverCrashTakeover_spec_10_1` (3 subtests: lapsed-lease adopt+re-fence+fence-out-stale, dead-held-channel evict+re-adopt+re-fence, terminal-fence relinquish+backoff-without-generation-climb). Changed-line coverage 87.4%. **On integration: 0060 is free on the current tip (integrator already reserved it by skipping it for this branch); no renumber expected. Two-replica gateway harness added closes T-4.2.4.**
