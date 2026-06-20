# Proposal: Reconcile §8.10 cascade semantics: reword `await_completion` to observable v1 behavior and amend the orphan quota carve-out to match the per-user count

- **Status:** Approved (2026-06-19). Converged after 4 adversarial review rounds (1 finding fixed); signed off by the human approver. Open decision (DEFECT 2 prose register) resolved in favor of the staged §3.3 wording, which keeps the explicit per-user fairness rationale (per-user concurrency quota versus per-tenant orphan cap) as the default-posture exposure. Awaiting `implement-proposal` to land the §8.10 / docs edits and close F-8.10.3.
- **Date:** 2026-06-19.
- **Scope:** Two reconciling spec defects in `spec/08_recursive-delegation.md` §8.10 (Delegation Tree Recovery) contradict the v1 code, which already implements the clarified behavior. DEFECT 1 is the phantom "then collect results" artifact in the `await_completion` cascade-table row at `spec/08_recursive-delegation.md:1073`, which implies a parent-collected aggregate that no spec field, event, or schema defines and the code never produces (closes F-8.10.3, High, DEFERRED under Rule P at `BUILD-GAPS.md:10765`). DEFECT 2 is the unimplemented quota carve-out at `spec/08_recursive-delegation.md:1103`, which states detached orphans are not counted toward the originating user's concurrency quota, while `CountActiveSessionsByUser` counts every non-terminal session. Both fixes reconcile the spec to the v1-correct code. DEFECT 1 carries a companion docs reconciliation in `docs/runtime-author-guide/delegation.md`; DEFECT 2 is spec-only. The change touches no v1 behavior, schema, code, proto, CRD, or chart.

This document stages the proposed spec and docs changes. It does not modify any spec, code, or doc file. Apply the changes in the "Proposed changes" section after sign-off.

## 1. Problem

Two reconciling defects in `spec/08_recursive-delegation.md` §8.10 contradict the v1 code, which already implements the clarified behavior.

### DEFECT 1 — phantom "collect results" artifact (closes F-8.10.3)

The §8.10 cascade table row at `spec/08_recursive-delegation.md:1073` reads:

```
| `await_completion` | Let running children finish (up to `cascadeTimeoutSeconds`), then collect results |
```

The phrase "then collect results" implies a post-terminal, parent-collected aggregate artifact. No spec field, event, or schema defines that artifact, and the code never produces it.

**No spec surface defines a parent-collected aggregate.** The §8.8 `TaskResult` schema (`spec/08_recursive-delegation.md:889-915`, mirrored by `pkg/sessionrecord/record.go:133-141`) has no parent-collected aggregate field. `treeUsage` (`spec/08_recursive-delegation.md:917`) is a policy-agnostic subtree usage rollup populated only after all descendants settle (`pkg/gateway/resultrollup/resultrollup.go:123-189` references no cascade policy), so it cannot discriminate `await_completion` from `detach`.

**The code never produces a collection.** `cascadeToChildren` (`pkg/gateway/sessionserver/usage.go:763-779`) early-returns for any policy other than `cancel_all` (the guard at `usage.go:777`), leaving children running under both `await_completion` and `detach` with no parent collection. The cascade runs only after the parent is terminal (`usage.go:476`), so no live agent or open `lenny/await_children` stream exists to receive a collection.

**The observable v1 distinction between the two policies is the orphan-cap fallback.** The single observable resource-accounting distinction between `await_completion` and `detach` is the detach-only orphan-cap fallback to `cancel_all` (`usage.go:767`): when a `detach` cascade would push the tenant's active orphan count past `maxOrphanTasksPerTenant`, the gateway cancels the children instead of orphaning them. `await_completion` has no such fallback.

**Result retrieval is identical under both policies.** `ListByRoot` (`pkg/gateway/sessionstore/pgstore/pgstore.go:606`) applies no state filter; `detach` never clears `root_session_id` (it is written only at Create and is absent from the Update SET clause; `cascadeToChildren` early-returns without mutating non-cancel children); and terminal rows persist (deleted only by Delete/DeleteByUser/DeleteByTenant). An external client enumerates the subtree via the client-facing `get_task_tree` MCP tool (`spec/15_external-api-surface.md:1297`) or `GET /v1/sessions/{id}/tree` (`spec/15_external-api-surface.md:675`), then reads per-session `/transcript`, `/artifacts`, and `/usage` (`spec/15_external-api-surface.md:669-676`) and the referenced blobs.

The `detach` cascade-table row (`spec/08_recursive-delegation.md:1074`) and its "Client can query via `get_task_tree`" pointer are valid and stay unchanged: `get_task_tree` exists as both the in-pod runtime tool `lenny/get_task_tree` (§8.5) and the unprefixed client-facing MCP tool (§15.2, `spec/15_external-api-surface.md:1297`).

**The mismatch is a recurring finding.** F-8.10.3 (High) is DEFERRED under Rule P at `BUILD-GAPS.md:10765`, which records that §8.10 never defines the observable artifact "collect results" produces (no wire field, no synthetic event, and no archive mutation distinguishes a collected `await_completion` parent from a `detach` parent), and that the natural discriminator (re-stamping the parent's `treeUsage`) would contradict §8.8 because that rollup populates for any settled tree regardless of cascade policy. The finding is closed by reconciling the spec to the observed v1 behavior, per its Rule-P deferral.

### DEFECT 2 — unimplemented quota carve-out

The Note at `spec/08_recursive-delegation.md:1103` states:

```
> **Note:** Detached orphan pods are **not** counted toward the originating user's concurrency quota during the detached window.
```

This carve-out is unimplemented. `CountActiveSessionsByUser` (`pkg/gateway/sessionstore/pgstore/pgstore.go:840`), called from the quota check, counts every non-terminal session via `state NOT IN ('completed','failed','cancelled','expired')`. A detached orphan is non-terminal, so it is counted. The carve-out is the only spec text asserting the exemption.

The per-user concurrency quota (`CountActiveSessionsByUser`, keyed by tenant and user) is per-user. It is enforced only when the deployer sets `max-concurrent-sessions-per-user` greater than 0, which defaults to 0 (disabled). `maxOrphanTasksPerTenant` (`detachExceedsOrphanCap`, default 100, always on) is per-tenant. In the default posture, the per-tenant orphan cap is the only active bound on a single user's orphans. A second per-user bound exists, the §11.1 per-user active-delegated-children cap (`CountActiveDelegatedChildrenByUser`, `pkg/gateway/sessionstore/pgstore/pgstore.go:926`), which also counts orphans when a deployer enables it; it likewise defaults to 0 (disabled).

The resolution amends the spec to match the code: detached orphans are not exempt from the per-user concurrent-session count. When a deployer enables the per-user cap, orphans count toward it. Counting orphans at per-user scope is the more-restrictive, safe-fail behavior, because the orphan carries the originating user's identity.

Both defects are spec text matching the code, plus a companion docs reconciliation for DEFECT 1. The v1 code is correct by construction; the §8.10 spec text is the defect in each case.

## 2. Decisions

- **Fix the spec to match the code in both defects.** The v1 code is correct by construction. DEFECT 1: `cascadeToChildren` leaves both `await_completion` and `detach` children running with no parent collection (`usage.go:777`), so the `await_completion` row must describe only the observable v1 behavior. DEFECT 2: `CountActiveSessionsByUser` counts non-terminal orphans (`pgstore.go:840`), so the line-1103 carve-out is amended to state orphans remain counted.
- **Leave the `detach` row (`spec/08_recursive-delegation.md:1074`) unchanged.** Its "Client can query via `get_task_tree`" pointer is valid, because `get_task_tree` is both the in-pod runtime tool `lenny/get_task_tree` (§8.5) and the unprefixed client-facing MCP tool (§15.2, `spec/15_external-api-surface.md:1297`). Do not "correct" it to a REST endpoint.
- **State the retrieval model as policy-agnostic.** Result retrieval is identical under both policies, because `ListByRoot` applies no state filter (`pgstore.go:606`), `root_session_id` is never cleared (absent from the Update SET clause), and terminal rows persist. The retrieval surfaces are the subtree enumeration tools and then per-session `/transcript`, `/artifacts`, and `/usage`. MCP, OpenAI, and A2A clients receive inlined blob content rather than `lenny-blob://` URIs (`spec/15_external-api-surface.md:684`, `spec/15_external-api-surface.md:1578`), so the spec text names the retrieval surfaces without anchoring to a single adapter.
- **Frame the two policies as differing only in orphan resource accounting.** The single observable resource-accounting distinction is the detach-only orphan-cap fallback to `cancel_all` (`usage.go:767`). Both policies' orphans count toward the per-user concurrency quota when it is enabled.
- **State the per-user fairness rationale as a default-posture risk rather than an absolute.** Both per-user caps default to 0 (disabled); the per-tenant orphan cap defaults to 100 and is always on. In the default posture the per-tenant cap is the only active bound on a single user's orphans. The rationale states this as the default-posture exposure rather than asserting the per-tenant cap is the only possible per-user bound, because the §11.1 per-user active-delegated-children cap also counts orphans when enabled.
- **Shrink DEFECT 1's clarifying text to a single sentence.** C1 alone removes the phantom artifact from the cascade table; the existing `detach` row, §8.9 (task tree), and §15 already document tree-based retrieval, and the existing line-1103 Note already states the orphan-cap fallback. The one statement no existing surface carries is that no parent-collected aggregate is synthesized under any policy. That sentence is appended to existing prose rather than added as a new multi-paragraph block.
- **Reconcile the companion docs cascade table.** `docs/runtime-author-guide/delegation.md:346` carries the same "then collect results" phrasing. Drop it so the docs match the reworded spec. The docs file carries no quota or orphan note, so the docs edit is scoped to DEFECT 1; DEFECT 2 is spec-only.
- **Spec-only with a docs reconciliation; no code, schema, proto, CRD, or chart change.** The `CascadePolicy` enum (`pkg/api/v1/session/session.go`) already admits `cancel_all`, `await_completion`, and `detach`, and no proto, OpenAPI, or schema surface carries the policy semantics the reword touches. F-8.10.3 and the line-1103 mismatch both close on application.

## 3. Proposed changes

### 3.1 Spec change: reword the §8.10 `await_completion` cascade-table row (line 1073)

Anchor on the cascade table under "Cascading behavior (configurable per delegation lease):" in §8.10. The current row is:

```
| `await_completion` | Let running children finish (up to `cascadeTimeoutSeconds`), then collect results                               |
```

Replace it with the following. The new row states the running-to-timeout behavior, the orphan-cleanup termination of any still running, and per-child retrievability, with no parent-collected aggregate:

```
| `await_completion` | Children keep running (up to `cascadeTimeoutSeconds`); when the timeout elapses, orphan cleanup terminates any still running. Each child's result stays retrievable from the session tree. |
```

Notes for the applier:

- Leave the `cancel_all` row (`spec/08_recursive-delegation.md:1072`) and the `detach` row (`spec/08_recursive-delegation.md:1074`) unchanged. The `detach` row's `get_task_tree` pointer is valid, because that tool is both the in-pod `lenny/get_task_tree` (§8.5) and the client-facing `get_task_tree` (§15.2, `spec/15_external-api-surface.md:1297`); do not change it to a REST endpoint.
- Confirm the exact pre-edit text of the `await_completion` row before replacing, because line numbers shift. The table cell uses padding spaces for column alignment; re-pad the replacement cell to match the table's column width so the table renders correctly.

### 3.2 Spec change: append a single clarifying sentence to the §8.10 line-1103 Note (DEFECT 1)

Anchor on the Note at `spec/08_recursive-delegation.md:1103` (the orphan-quota Note that begins "**Note:** Detached orphan pods …"). After C1 removes the phantom "collect results" from the cascade-table row, one statement remains uncarried by any existing surface: that no parent-collected aggregate is synthesized under any cascade policy. Append exactly this sentence to the end of the line-1103 Note (after the existing orphan-cap text), rather than adding a new paragraph:

```
No parent-collected aggregate result is synthesized under any cascade policy; each child result is retrieved per node from the task tree (see Section 8.9 and the client-facing retrieval surfaces in Section 15.2), identically under `await_completion` and `detach`.
```

Notes for the applier:

- Do not restate the orphan-cap fallback; the line-1103 Note already states it. Do not re-list `/transcript`, `/artifacts`, `/usage`, or both `get_task_tree` surfaces; the `detach` row, §8.9, and §15 already carry them. A single cross-reference suffices.
- This sentence is the entire DEFECT 1 clarifying delta beyond the C1 row reword. Do not add a separate multi-paragraph clarifying block.
- The §3.3 amendment edits the first sentence of this same Note. Apply §3.3 and §3.2 together: §3.3 rewrites the opening sentence and the connecting prose, and §3.2 appends this closing sentence.

### 3.3 Spec change: amend the §8.10 line-1103 orphan quota carve-out (DEFECT 2)

Anchor on the same Note at `spec/08_recursive-delegation.md:1103`. The current first sentence is:

```
> **Note:** Detached orphan pods are **not** counted toward the originating user's concurrency quota during the detached window.
```

Replace that first sentence with the following, which states the code-accurate rule that orphans are not exempt from the per-user concurrent-session count:

```
> **Note:** Detached orphans are **not** exempt from the originating user's per-user concurrent-session count. An orphan carries the originating user's identity, and for as long as it remains non-terminal it counts toward that user's per-user concurrent-session admission cap (the [Section 11.1](11_policy-and-controls.md#111-admission-and-fairness) per-user concurrency limit, configurable via Helm `gateway.concurrentSessions.perUser`) when a deployer has enabled it (the cap defaults to 0, disabled). Counting orphans at per-user scope preserves per-user fairness.
```

Then adjust the connecting prose that follows so the per-tenant orphan cap reads as an additional tenant-wide bound layered on top of the per-user count, rather than the sole mitigation. Keep the rest of the Note (the `maxOrphanTasksPerTenant` mechanics, the default of 100, the `detach`→`cancel_all` fallback, the `lenny_orphan_tasks_active_per_tenant` gauge, and the `OrphanTasksPerTenantHigh` alert) unchanged in substance.

Notes for the applier:

- Do not assert that the per-user count is the only bound on one user's orphans. In the default posture both per-user caps default to 0 (disabled), so the per-tenant `maxOrphanTasksPerTenant` cap (default 100, always on) is the only active bound. State the per-tenant-only exposure as the default-posture risk. The §11.1 per-user active-delegated-children cap (`CountActiveDelegatedChildrenByUser`) also counts orphans when a deployer enables it.
- The lifetime bound (`cascadeTimeoutSeconds`, default 3600s) is unchanged and stays in the Note; only the per-user-quota exemption claim is corrected.
- §3.2 appends one sentence to the end of this same Note. Apply §3.2 and §3.3 together.

### 3.4 Docs change: reconcile the cascade table `await_completion` row (DEFECT 1)

Anchor on the "Cascade Policies" cascade table in `docs/runtime-author-guide/delegation.md`. The current row at `docs/runtime-author-guide/delegation.md:346` is:

```
| `await_completion` | Let running children finish (up to `cascadeTimeoutSeconds`), then collect results |
```

Replace it with wording parallel to the reworded spec row from §3.1, with no "collect results" and no parent-collected aggregate:

```
| `await_completion` | Children keep running (up to `cascadeTimeoutSeconds`); when the timeout elapses, orphan cleanup terminates any still running. Each child's result stays retrievable from the session tree. |
```

Notes for the applier:

- Leave the `cancel_all` row (`docs/runtime-author-guide/delegation.md:345`) and the `detach` row (`docs/runtime-author-guide/delegation.md:347`) unchanged. The docs `detach` row does not mention `get_task_tree`, so no change is needed there.
- The docs file has no quota or orphan note, so DEFECT 2 requires no docs edit.
- Keep the docs prose neutral and declarative per `doc-style.md`. Confirm the exact pre-edit row text before replacing.

## 4. Non-goals

- **No code change.** `cascadeToChildren` (`usage.go:763-779`), `CountActiveSessionsByUser` (`pgstore.go:840`), and the retrieval path (`ListByRoot` and the tree, transcript, artifacts, and usage handlers) already implement the clarified behavior. Both defects are spec text matching the code, plus a docs reconciliation.
- **No schema, proto, CRD, or chart change.** The `CascadePolicy` enum (`pkg/api/v1/session/session.go`) already admits `cancel_all`, `await_completion`, and `detach`; no `*.proto`, `openapi.json`, or `schemas/` file carries the policy semantics the reword touches; `charts/lenny/values.yaml` references only the config knobs (`cascadeTimeoutSeconds`, `maxOrphanTasksPerTenant`), which are unchanged.
- **Do not build an `await_completion` collection feature.** F-8.10.3's collection-synthesis workstream (a parent-collected boundary aggregating settled children into the parent's archived `TaskResult`) is out of scope. The finding is closed by reconciling the spec to the observed v1 behavior, per its Rule-P deferral at `BUILD-GAPS.md:10765`.
- **Do not change the `detach` cascade-table row (`spec/08_recursive-delegation.md:1074`) or its `get_task_tree` pointer.** The pointer is valid (client-facing `get_task_tree` exists at `spec/15_external-api-surface.md:1297`) and must not be "corrected" to a REST endpoint.
- **Do not alter the per-tenant orphan cap mechanics, the orphan-cleanup job, the budget and usage-charging semantics (`spec/08_recursive-delegation.md:1105-1109`), or the §11.1 per-user active-delegated-children cap.** DEFECT 2 amends only the quota-counting sentence in the line-1103 Note and the prose connecting it to the per-tenant cap.
- **No reader-facing docs edit beyond the cascade table row.** The docs carry no quota or orphan note for DEFECT 2, and the docs `detach` row needs no change.

## 5. Testing

- **Tier 0 (static):** confirm the edited spec and docs render and the §8.9 and §15.2 cross-references in the §3.2 sentence resolve to live headings. The spec lint and link-check stage flags a broken anchor.
- **Tier 1 (unit), already covered:** the existing `cascadeToChildren` tests assert that `await_completion` and `detach` both fall through the `policy != cancel_all` early return and leave children running, and that `detach` falls back to `cancel_all` over `maxOrphanTasksPerTenant`. These continue to pin the observable v1 behavior the reworded §8.10 row now describes. The existing `CountActiveSessionsByUser` tests assert that non-terminal sessions are counted with no orphan exemption, pinning the amended line-1103 rule. No new unit test is required, because behavior is unchanged.
- **Tier 11 (docs):** confirm the edited §8.10 cascade table and the docs cascade table (`docs/runtime-author-guide/delegation.md`) agree on the `await_completion` behavior and neither asserts a parent-collected aggregate. The check confirms convergence rather than requiring further docs edits beyond §3.4.
- **No tier-2-or-higher behavioral test is added.** The change is wording-only with no behavior, schema, or wire-contract change, so no envtest, contract, integration, e2e, chaos, or security tier is reached.

## 6. Findings closed on application

- **F-8.10.3** (High, DEFERRED under Rule P at `BUILD-GAPS.md:10765`): the §8.10 `await_completion` row asserts "then collect results", which implies a parent-collected aggregate the spec never defines and the v1 code never produces. The §3.1 reword and the §3.2 clarifying sentence remove the phantom artifact and state that no parent-collected aggregate is synthesized under any cascade policy, with retrieval identical under both policies. This reconciles the spec to the observed v1 behavior (`cascadeToChildren` early-returns for any non-`cancel_all` policy; `ListByRoot` applies no state filter; terminal rows persist). The collection-synthesis feature remains out of scope per the Rule-P deferral.
- **The line-1103 quota carve-out mismatch** (untracked by a numbered finding; surfaced in this proposal's analysis): the Note claims detached orphans are not counted toward the originating user's concurrency quota, while `CountActiveSessionsByUser` counts every non-terminal session with no orphan exemption. The §3.3 amendment states the code-accurate rule that orphans are not exempt from the per-user concurrent-session count.

## 7. Resolved in adversarial review

Adversarial review rounds populate this section.

### Pass 1 (2026-06-19, automated)

- **Staged §3.3 spec text named a config key `maxConcurrentSessionsPerUser` that exists on no authored surface.** A full-spec and full-chart grep for `maxConcurrentSessionsPerUser` returns no matches (the only repo occurrence is a stale Go field and comment at `pkg/gateway/sessionserver/sessionserver.go:1153-1159`, which itself misnames the chart key and is a code defect outside this spec-only proposal). The backtick-quoted camelCase form also collided with the distinct per-pod `sessionPolicy.maxConcurrentSessions` slot count (`spec/05_runtime-registry-and-pool-model.md:397`, `spec/06_warm-pod-model.md:24`). Replaced the invented identifier in the staged line-1103 Note (§3.3) with the real authored Helm key `gateway.concurrentSessions.perUser` (verified at `charts/lenny/values.yaml:2296-2298`, default 0, rendered to env `LENNY_MAX_CONCURRENT_SESSIONS_PER_USER` at `charts/lenny/templates/gateway-deployment.yaml:838` and CLI flag `--max-concurrent-sessions-per-user` at `cmd/lenny-gateway/main.go:525`), matching the same Note's convention of backtick-quoting the real Helm key (`delegation.maxOrphanTasksPerTenant`). Added a relative `[Section 11.1](11_policy-and-controls.md#111-admission-and-fairness)` cross-reference (heading verified at `spec/11_policy-and-controls.md:3`) so the staged spec text names the cap by its §11.1 granularity rather than an invented field. The "defaults to 0, disabled" framing is accurate against `charts/lenny/values.yaml:2298` and is retained. The Problem section (line 45) already used the correct CLI flag form `max-concurrent-sessions-per-user`, so the body and the staged spec text now agree on one real name each for the same cap.

## 8. Open decisions for review

- **DEFECT 2 prose register — RESOLVED (2026-06-19, at sign-off).** The choice was between the staged §3.3 wording (explicit per-user fairness rationale: per-user concurrency quota versus per-tenant orphan cap, default-posture exposure) and a factual-rule-only amendment that leaves the rationale to this proposal body. Resolved in favor of the staged §3.3 wording — the per-user fairness rationale is the reason the carve-out is being removed rather than implemented, so the spec should carry it. Apply §3.3 as staged.

## 9. Files touched on application

- `spec/08_recursive-delegation.md`: §8.10 `await_completion` cascade-table row (line 1073) reworded to the observable v1 behavior (§3.1); the line-1103 orphan-quota Note amended so detached orphans are not exempt from the per-user concurrent-session count (§3.3), with one appended sentence stating no parent-collected aggregate is synthesized under any cascade policy (§3.2).
- `docs/runtime-author-guide/delegation.md`: the cascade table `await_completion` row (line 346) reworded to match the spec, dropping "then collect results" (§3.4).
- No code, schema, proto, CRD, or chart file is touched. The existing tier-1 cascade and quota-count tests and the tier-11 doc-consistency check verify the change.
