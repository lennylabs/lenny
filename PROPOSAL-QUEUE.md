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
tops up that worker's assignments so it never idles on merge latency. With more
than one proposal worker, the integrator assigns each a disjoint set of clusters
(explicit per-cluster assignment is race-free because the integrator is the sole
writer of this file); clusters are grouped so each worker stays within a coherent
spec area, keeping two workers off the same spec sections and code and minimizing
integration conflicts. Current split: proposal-A holds the checkpoint and §25
clusters (C-01–C-03), proposal-B holds the §4.9 credential-leasing clusters
(C-04–C-06) and the eviction-trigger cluster C-22 (which depends on proposal-A's
C-01, so B works C-04–C-06 first), and proposal-C holds the two spec areas no
other worker or the §25 closure machine touches: the interceptor error-envelope
cluster C-18 (§4.8/§15.1) and the OpenSLO-export cluster C-19 (§16.10), both now
landed (0039, 0041); its next isolated cluster is C-38 (§10.7/§8.3 rate-limit). The
integrator extends each worker's assignments from the remaining clusters as its
branches land, keeping each worker's lane clear of the others' active spec
sections; the §15 (C-13) and §7 (C-14) Highs are held back until proposal-A's
C-01 pod/resume work lands, to avoid pod-claim/resume conflicts.

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
- **status:** claimed:proposal-A — converging proposal 0037. Not yet in-review: the first attempt never converged. Proposal 0036 is withdrawn (renamed/retained; its status note explains why). Goes to in-review:0037 when the current loop converges.
- **assigned:** proposal-A
- **findings:** T-4.4.12, T-4.4.13, T-JRN.9, T-DEP.5, T-4.2.1
- **root spec gap:** §4.4 promises a workspace can survive pod loss, but the produce→store→restore data pipeline is unwired and the architecture is undecided: does the pod-side adapter talk to MinIO directly (requiring §13 pod-side credential/egress delivery) or stream to the gateway, and what are the object-key layout and the atomic metadata/manifest record. (Split 2026-07-14: the eviction *trigger* is a separate root gap, moved to C-22 with T-4.4.14 and T-4.6.4.)
- **proposal scope:** Decide the checkpoint sink/source architecture and pod-side MinIO credential delivery; the object-key layout and atomic metadata plus partial-manifest schema (including `manifest_reason='terminated_during_resume'` and the generation counters); and `POST /v1/sessions/{id}/resume`, with the MinIO-outage fallback to minimal Postgres state. T-JRN.9's checkpoint-then-resume journey closes here for the periodic-loop-driven variant (drive the checkpoint from the periodic loop, then delete the pod — no eviction trigger needed); the node-drain-driven variant is tracked under T-4.6.4 in C-22.
- **severity:** High

### C-02 — Operational event-stream read source, tenant filtering, and MCP subscription — §25.5/§25.12
- **status:** open
- **assigned:** proposal-A
- **findings:** T-25.5.1, T-25.5.5, T-25.5.6, T-25.5.7, T-25.12.6
- **root spec gap:** The §25.5 read side never consumes the Redis `ops:events:stream`, so the transparent Redis→gateway-buffer fallback, delivery-time tenant filtering, gap-detection on real eviction, the full `EventStreamService` interface (caller identity, `UpdateSubscription`/`ListDeliveries` cursor), and MCP-native subscription have no data source or defined caller-tenant/RBAC plumbing.
- **proposal scope:** Define the Redis XREAD/XRANGE read source with mid-stream source switching and cross-source cursor translation; the delivery-time tenant-filter rule plus caller-identity/RBAC threading on the SSE/poll read endpoints; the `EventStreamService` caller-signature and cursor-pagination contract; and the MCP `notifications/subscribe`/`message` streaming transport and payload schema layered on that source.
- **severity:** High

### C-03 — MCP management tool dispatch, identity/scope forwarding, and capability classification — §25.12/§25.4
- **status:** open
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
- **status:** open
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
- **findings:** T-25.8.4, T-25.8.20, T-25.15.8
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
- **status:** open
- **assigned:** (unassigned)
- **findings:** T-STD.4
- **root spec gap:** The always-available OpenAI Chat and Open Responses adapters fail on any standard Helm deployment because they bypass the pod-claim path and are never bound to a pod, and the spec does not define how a single-shot adapter request claims/dispatches/releases a pod.
- **proposal scope:** Define the pod-binding model for the OpenAI-dialect adapters under a PodExecutor (claim-per-request against the warm pool, a dedicated translator pool, or another design) so a non-continuous request completes within one HTTP call.
- **severity:** High

### C-14 — Interactive suspended-session atomic resume-and-deliver — §7 (path 6)
- **status:** open
- **assigned:** (unassigned)
- **findings:** T-JRN.1
- **root spec gap:** §07 path 6 requires that an `immediate` message to a suspended session whose pod is still held atomically resume and deliver with `status: delivered`, but the code falls back to queueing (`status: queued`), leaving the session suspended.
- **proposal scope:** Confirm the atomic resume-and-deliver behavior as the required contract and route the missing product implementation through the pipeline so the test can pin the `delivered` outcome.
- **severity:** High

### C-15 — Checkpoint consistency tagging, isolation enforcement, and duration benchmarking — §4.4
- **status:** open
- **assigned:** (unassigned)
- **findings:** T-4.4.7, T-4.4.8
- **root spec gap:** §4.4 says checkpoints are tagged consistency, produce consistent results only under the cooperative handshake, and must meet a duration SLO, but nothing persists a consistency tag on the record, nothing refuses the embedded SIGSTOP path under sandboxed/microvm isolation, and no client-reachable checkpoint-trigger surface exists to benchmark.
- **proposal scope:** Decide whether the consistency classification is persisted on the checkpoint record, add an isolation-profile enforcement point rejecting embedded+sandboxed/microvm, and define the trigger surface (on-demand endpoint vs periodic) the duration benchmark observes. Builds on C-01.
- **severity:** Medium

### C-16 — Warm-pod PDB disruption mechanism — §4.6.1
- **status:** open
- **assigned:** (unassigned)
- **findings:** T-4.6.5
- **root spec gap:** A `maxUnavailable: 1` PDB requires the owning controller to expose a `/scale` subresource to resolve expectedPods, but Sandbox has only a status subresource, so every warm-pod PDB sits at `disruptionsAllowed: 0` and deadlocks node drains.
- **proposal scope:** Choose the fix — add a `/scale` subresource to Sandbox/SandboxWarmPool, replace `maxUnavailable: 1` with an integer `minAvailable` (currently forbidden by the spec), or another mechanism — and amend §4.6.1 accordingly.
- **severity:** Medium (operationally High — see the discovered finding in TEST-GAPS.md: warm-pod PDB deadlocks node drains on the live cluster)

### C-17 — SSA conflict retry policy and counter semantics — §4.6/§16
- **status:** open
- **assigned:** (unassigned)
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
- **status:** open
- **assigned:** (unassigned)
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
- **status:** open
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
- **findings:** T-25.7.11
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
- **findings:** T-25.4.21, T-25.12.16
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
- **status:** open
- **assigned:** (unassigned)
- **findings:** T-25.13.9, T-25.17.4
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

---

## Integrator inbox

Feedback items from proposal workers and closure runners for the integrator to
action. Append a `- [ ] from <worker> (<proposal/branch>): <ask>` line on your
own branch; the integrator processes it on integration and checks it off `- [x]`
with the action taken, keeping a short trail here.

- [x] from proposal-C (0039): list T-4.8.16 under C-18's `findings:` — it shares C-18's root gap and 0039 closed it. → Done 2026-07-16: added T-4.8.16 to C-18 findings (already RESOLVED by 0039 in TEST-GAPS.md).
- [x] from proposal-C (0039 §5): file a new finding for the §8.7 export path mapping every `*ExportScanError` (incl. cataloged `EXPORT_FILE_SCAN_REJECTED`) to `INTERNAL_ERROR`/nil-details because it is not matched as a `*ToolError` — documented in 0039 §5, not yet a TEST-GAPS entry. → Done 2026-07-16: filed T-8.7.14 under §8.7.
- [x] re-cluster 2026-07-16: 50-finding backlog clustered. Added C-23–C-38 (16 new clusters, unassigned) and folded T-25.8.20/T-25.15.8→C-08, T-25.9.14→C-09, T-25.9.12→C-11.
- [ ] pending fold-in to ASSIGNED clusters (apply when C-02/C-03 reopen or before proposal-A starts them; not applied now per "don't alter assigned clusters"): T-25.3.5, T-25.5.20 → C-02 (§25.5 read source); T-25.12.11 → C-03 (§25.12 dispatch; its chaos test is blocked on T-25.12.3).
- [x] test-infra handback from re-cluster: de-escalated T-ADV.11, T-ADV.13, T-25.2.2, T-25.12.22 (runner-closable). Kept needs-human as operator-resource-blocked: T-25.4.37 (EKS/cloud), T-25.11.7 (tagged buckets + cloud creds), T-25.6.1 (operator helm upgrade for RBAC).
- [x] re-cluster 2026-07-17: folded T-25.4.38 into C-29 (recommendation aggregator, same root gap); added C-39 for T-25.4.33 (backup operationId disambiguation); de-escalated test-infra T-25.4.12 (orphaned-commit re-verify).
- [x] re-cluster 2026-07-17: folded T-25.6.14→C-23 (gateway :8443 TLS/HTTP probe collision) and T-25.6.17→C-24 (§25.6 both-down 503 error code) — both match existing unassigned clusters.
- [ ] pending fold-in to ASSIGNED cluster C-02 (§25.5 MCP-native subscription, proposal-A): T-25.5.14 — whether webhook-subscription CRUD should be MCP-exposed or REST-only is deliberate. Not applied now (C-02 assigned); fold in when C-02 reopens or before proposal-A starts it.
