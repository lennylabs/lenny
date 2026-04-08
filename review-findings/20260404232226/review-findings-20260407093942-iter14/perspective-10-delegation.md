# Technical Design Review Findings — 2026-04-07 (Iteration 14)

**Document reviewed:** `technical-design.md` (8,691 lines)
**Perspective:** 10 — Recursive Delegation & Task Trees
**Iteration:** 14
**Prior finding status:** DEL-039 (`settled=all` redundant mode) — STILL PRESENT (lines 3395, 3104)
**New findings:** 1

## Medium

| # | ID | Finding | Section | Lines |
|---|-----|---------|---------|-------|
| 1 | DEL-039 | `lenny/await_children` mode `settled` is defined as "equivalent to `all`" (line 3395) and has identical semantics. It appears in the tool table (line 3104) and the modes list (line 3395) but provides no distinct behavior. Runtime authors must learn three mode names where two suffice, and the redundancy invites future divergence if one definition is updated without the other. Either remove `settled` or give it distinct semantics (e.g., include `input_required` as a settled state). | 8.5, 8.8 | 3104, 3395 |
| 2 | DEL-040 | Orphan cleanup job scope is too narrow for mid-tree `detach` cascades. Line 3530 defines the cleanup job as detecting "task tree nodes whose **root session** has been terminated." But `cascadeOnFailure: detach` can trigger at any depth: if a depth-1 node terminates with `detach`, its depth-2 children become orphans while the root session (depth 0) is still alive and running. These mid-tree orphans are invisible to the cleanup job until the root eventually terminates, leaving them running indefinitely (bounded only by `perChildMaxAge` if set). Fix: change the cleanup predicate to "nodes whose **parent session** has reached a terminal state" or add a separate mid-tree orphan scan that checks each node's direct parent status. | 8.10 | 3530 |

## Verification notes

Checked the following areas for issues; all were internally consistent:

- **Lease extension rejection scope**: Rejection is subtree-scoped with cool-off (lines 3219-3221), not tree-permanent. Admin API can clear the denial. Internally consistent.
- **Deep delegation tree recovery**: Bottom-up recovery ordering (line 3462), per-level and total timeouts (lines 3466-3471), interaction with `maxResumeWindowSeconds` (line 3473), non-adjacent failure handling (line 3490), and the deployer formula (lines 3477-3484) are all internally consistent. The formula example (depth-6 tree, 1620s) checks out mathematically.
- **Credential propagation through delegation chains**: Per-hop model (lines 3039-3060), worked example (lines 3043-3058), pre-check at delegation time (line 3062), fan-out guidance for `inherit` mode (line 3064) — all consistent.
- **Cross-environment delegation**: Bilateral declaration model (lines 4195-4211), gateway enforcement steps 1-4 (lines 4216-4221), isolation monotonicity preserved across environments (line 3220), connectors never cross-environment (line 4223), data residency transitivity (line 5280) — all consistent.
- **Cycle detection**: Runtime-identity lineage check (line 2837) distinct from and complementary to subtree deadlock detection (line 3427) — correctly covers both cases.
- **Budget reservation model**: Atomic Lua script for reservation (line 2994), return-on-completion with quiescence (lines 3019-3025), over-run semantics for proxy vs direct mode (line 3029), concurrency safety analysis (line 3027) — all consistent.
- **Cascade policy semantics**: `cascadeOnFailure` applies on all terminal states including `completed` (line 3507), `await_completion` bounded by `cascadeTimeoutSeconds` (line 3517), detached orphan budget return is no-op (line 3547), per-tenant orphan cap with fallback to `cancel_all` (line 3542) — all consistent.
- **Policy propagation**: `DelegationPolicy` intersection model (line 2918), `maxDelegationPolicy` restriction-only layering (lines 2930-2935), `contentPolicy` inheritance (line 2901), `interceptorRef` restrictiveness rules (lines 2903-2909), `snapshotPolicyAtLease` tree-wide snapshot (line 2928) — all consistent.
- **Isolation monotonicity**: Enforcement at delegation time (line 2982), proactive pool-registration audit (line 2984), cross-environment preservation (line 4220) — all consistent.
- **Completed subtree offloading**: Offload to `session_tree_archive` (line 2876), stub replacement (200B), re-await protocol streams from archive in order (line 3501) — internally consistent.
