# Iter3 Security & Threat Modeling Review (SEC)

Iter2 fixes verified: SEC-002 (SIGCONT/shareProcessNamespace) is now consistent between §4.4 and §13.1. SEC-001 landed but introduced two regressions reported here as SEC-003 and SEC-004. Three new items (SEC-005, SEC-006, SEC-007) missed by prior iterations.

---

## SEC-003 [High] Duplicate `ISOLATION_MONOTONICITY_VIOLATED` catalog rows with conflicting HTTP status codes — REGRESSION from iter2 SEC-001 fix

**Files:** `spec/15_external-api-surface.md` §15.1 error code catalog lines 771 and 800; `spec/15_external-api-surface.md` §15.2.1 REST/MCP consistency contract test line 1089.

**Issue.** The iter2 fix for SEC-001 added a derive/replay row to the error-code catalog at line 800 without reconciling the pre-existing delegation row at line 771. The catalog now has two rows for the same code with different HTTP statuses:

- Line 771: `| ISOLATION_MONOTONICITY_VIOLATED | POLICY | 403 | Delegation rejected because the target pool's isolation profile is less restrictive than the calling session's minIsolationProfile. ...`
- Line 800: `| ISOLATION_MONOTONICITY_VIOLATED | POLICY | 422 | POST /v1/sessions/{id}/derive or POST /v1/sessions/{id}/replay (with replayMode: workspace_derive) rejected because the target pool's sessionIsolationLevel.isolationProfile is weaker than the source session's ... Overridable by platform-admin callers via allowIsolationDowngrade: true ...`

Every other catalog row is unique per code. This one code now maps to both 403 and 422. The REST/MCP consistency contract test at line 1089 breaks by construction: *"All error classes: ... `ISOLATION_MONOTONICITY_VIOLATED` — each exercised with a canonical triggering input. For each, the test asserts identical `code`, `category`, and `retryable` values."* Two canonical triggering inputs producing different HTTP statuses for the same code makes the single-`(code,http_status)`-tuple assumption unimplementable. The same semantic condition (monotonicity violation) returns different HTTP codes in different call contexts with no documented rationale.

**Recommendation.** Collapse the catalog to a single row for `ISOLATION_MONOTONICITY_VIOLATED`. Either unify on 403 (update §7.1 item 5 and §15.1 line 514 to HTTP 403, matches delegation §8.3 and the idiomatic "POLICY = 403" pattern) or unify on 422 (update §8.3 monotonicity prose to 422, matches newly added derive/replay wording). Also: (a) merge the `details.*` fields list — `details.parentIsolation`/`targetIsolation` (delegation, line 771) vs `details.sourceIsolationProfile`/`targetIsolationProfile`/`targetPool` (derive/replay, line 800) must become a single field vocabulary (two naming schemes for the same concept is an additional REST/MCP contract regression); (b) add a §15.2.1 contract-test clause explicitly asserting a single `(code, http_status, category, retryable)` tuple per error code.

---

## SEC-004 [Medium] Self-contradictory `prompt_history` clause in replay endpoint — REGRESSION from iter2 SEC-001 fix

**Files:** `spec/15_external-api-surface.md` §15.1 Session Replay Semantics line 514.

**Issue.** Line 514 documents `allowIsolationDowngrade` semantics for `POST /v1/sessions/{id}/replay`. The final sentence of the parameter description is internally contradictory:

*"The flag has no effect when `replayMode: prompt_history` because that mode inherits the source session's pool unless `targetPool` is explicitly supplied — any `targetPool` override in `prompt_history` mode is also subject to the monotonicity rule and the same `allowIsolationDowngrade` flag."*

The first clause asserts "the flag has no effect when `replayMode: prompt_history`." The second clause (after the em-dash) asserts the opposite: "any `targetPool` override in `prompt_history` mode is also subject to the monotonicity rule and the same `allowIsolationDowngrade` flag" — i.e., the flag *does* have effect in `prompt_history` mode, specifically when `targetPool` is set. A reader trying to implement the endpoint cannot determine from this sentence what the gateway actually does when a caller sends `{ "replayMode": "prompt_history", "targetPool": "<weaker-pool>", "allowIsolationDowngrade": true }`:

- If the first clause is normative, the flag is ignored and the request is rejected with `ISOLATION_MONOTONICITY_VIOLATED` regardless of who calls it (a surprising and undocumented security regression — the flag was added precisely to let `platform-admin` downgrade, and there is no principled reason to disable the override for `prompt_history` but allow it for `workspace_derive`).
- If the second clause is normative, the flag works identically in both modes (and the first clause is misleading).

The iter2 audit-event trail in §11.7 (`derive.isolation_downgrade`, schema fields `source_session_id`, `target_pool`, `authorizing_user_sub`, `ticket_id`) does not distinguish `prompt_history` from `workspace_derive` — the event shape presumes both modes can emit it. §7.1 item 5 only addresses `workspace_derive` wording for replay ("The same rule applies to `POST /v1/sessions/{id}/replay` when `replayMode: workspace_derive` is set"). The inconsistency across §7.1, §11.7, and the conflicting clauses in §15.1 line 514 collectively underspecify the admin override for `prompt_history`+`targetPool`.

**Recommendation.** Rewrite line 514's final sentence to state one rule. Substantive intent appears to be: the monotonicity check and `allowIsolationDowngrade` override apply whenever replay targets a pool weaker than the source session's pool, regardless of `replayMode`. Clean rewrite: *"When `replayMode: prompt_history` and no `targetPool` is supplied, the replay reuses the source session's pool, the monotonicity check is trivially satisfied, and `allowIsolationDowngrade` has no effect. When `replayMode: prompt_history` and `targetPool` is supplied, the monotonicity rule and `allowIsolationDowngrade` apply exactly as in `workspace_derive` mode — the same `derive.isolation_downgrade` audit event is emitted and the same `platform-admin` role check gates the flag."* Update §7.1 item 5 to remove the "only `workspace_derive`" scoping. Add a contract-test case covering the four `(replayMode) × (targetPool same/weaker)` combinations.

---

## SEC-005 [High] Credential file cross-UID ownership requirement is unsatisfiable under §13.1 capability rules

**Files:** `spec/04_system-components.md` §4.7 lines 841, 867; `spec/06_warm-pod-model.md` §6.1 line 28; `spec/13_security-model.md` §13.1 lines 5–14.

**Issue.** §4.7 specifies `/run/lenny/credentials.json` (and, in concurrent-workspace mode, `/run/lenny/slots/{slotId}/credentials.json`) as "a tmpfs-backed file (`/run/lenny/credentials.json`, mode `0400`, owned by the agent UID) written by the adapter before spawning the runtime binary." §4.7 line 841 separately requires adapter and agent to run as distinct UIDs (example: adapter `1000`, agent `1001`) and relies on that separation for the `SO_PEERCRED` peer check. §13.1 states "Capabilities: All dropped" without carve-out, removing `CAP_CHOWN` and `CAP_FOWNER` from both containers. None of the mechanisms that would make "adapter-UID process creates a file owned by the agent UID" satisfiable are documented:

- No `securityContext.fsGroup` or shared supplemental group is specified anywhere in §4.7, §6.1, §13.1, §14, or §17. The file mode `0400` has no group-read bit so a group workaround is excluded by the stated mode.
- `emptyDir.medium: Memory` does not accept per-volume ownership overrides.
- No init container pre-creates the file as the agent UID.
- `CAP_CHOWN` is dropped by §13.1's blanket rule.
- Running the adapter with the same UID as the agent would null out the `SO_PEERCRED` UID check.

Sidecar mode is the default for third-party runtimes (§4.7 line 833). Implementers will silently weaken the requirement (use mode `0440` + shared group, or merge UIDs) or re-add `CAP_CHOWN` (violating §13.1). Either outcome contradicts the spec's own security invariants. Secondary: the §5.2 scrub contract at line 417 (stat `/run/lenny/credentials.json` after task completion; mark scrub failed if present) assumes the adapter UID can `unlink()` the file — true only if the adapter owns `/run/lenny/`, which nothing in the spec pins.

**Recommendation.** Pick one resolution and document normatively in §4.7, cross-linked from §13.1:

1. **Shared group + mode `0440`.** Dedicated `lenny-creds` supplemental group on both containers; `spec.securityContext.fsGroup` set to that GID; change file mode to `0440`. Simplest fix, preserves UID separation.
2. **Init-container ownership seed.** Short-lived init container running as agent UID creates empty `/run/lenny/credentials.json` (mode `0600`) before adapter starts. Adapter opens with `O_WRONLY|O_TRUNC` — no ownership change required.
3. **User-namespace remapping.** If Lenny assumes `userns: hostUsers: false`, document it in §13.1 and scope `CAP_CHOWN` to the pod user namespace with admission bounds.

Add an adapter startup self-test (§4.7 line 843) that stats the credential file and verifies ownership + mode, exiting non-zero on mismatch (matching the `SO_PEERCRED` self-test pattern).

---

## SEC-006 [Medium] SPIFFE-binding disablement not enforced in multi-tenant mode — assertion without admission control

**Files:** `spec/04_system-components.md` §4.9 lines 1434, 1460–1465, 1467; `spec/13_security-model.md` §13.1 (admission webhook inventory).

**Issue.** §4.9 line 1460 asserts SPIFFE-binding "is a v1 requirement for multi-tenant deployments" and line 1467's threat-model paragraph calls it the primary defense against cross-pod lease-token replay. Line 1465: *"SPIFFE-binding is enforced by default when `deliveryMode: proxy` is set on a pool. It can be disabled only for single-tenant or development deployments via `credentialPool.spiffeBinding: disabled`, which emits a `ProxyModeSpiffeBindingDisabled` warning event at pool registration time."*

The "only for single-tenant or development deployments" clause is enforced by nothing but that warning event. The spec has no admission webhook, no pool-registration validation error, no Helm preflight, and no CRD CEL validation rejecting `credentialPool.spiffeBinding: disabled` under `tenancy.mode: multi`. The control plane accepts the disablement silently in any mode.

Directly contrasted in the same section: `DirectModeStandardIsolationMultiTenantRejected` at line 1434 implements two-layer enforcement — warm-pool controller rejects the combination, plus a `ValidatingAdmissionWebhook` `lenny-direct-mode-isolation` with `failurePolicy: Fail`. That is the template the SPIFFE-binding control should follow. Without it, an operator can silently weaken multi-tenant proxy-mode's cross-pod replay defense by editing a single Helm value — the exact attack §4.9 line 1467 enumerates (lease token extracted from one pod, replayed from another with valid mTLS cert, succeeds across tenants).

**Recommendation.** Mirror the `DirectModeStandardIsolationMultiTenantRejected` control:

1. **Pool-registration validation:** Warm-pool controller MUST reject any `CredentialPool` combining `deliveryMode: proxy` + `spiffeBinding: disabled` when `tenancy.mode: multi`, with error `ProxyModeSpiffeBindingDisabledMultiTenantRejected`.
2. **`ValidatingAdmissionWebhook`:** Extend `lenny-direct-mode-isolation` (or add `lenny-proxy-mode-spiffe-binding`, `failurePolicy: Fail`) to reject the combination.
3. **Explicit opt-in in single-tenant/dev:** Require `allowProxyModeSpiffeBindingDisabled: true` on the pool, mirroring `allowDirectModeStandardIsolation`. Webhook rejects this field in multi-tenant mode.
4. **Audit event:** Upgrade the existing `ProxyModeSpiffeBindingDisabled` Kubernetes event to an audit event (appears in the §4.9.2 `credential.*` audit stream).
5. **Startup preflight:** Reject multi-tenant deployments where any registered `CredentialPool` has `spiffeBinding: disabled`, matching §13.1's `shareProcessNamespace` preflight pattern.

---

## SEC-007 [Medium] Interceptor `failPolicy` oscillation admits bulk prompt-injection across the lease lifetime of affected delegation trees

**Files:** `spec/08_recursive-delegation.md` §8.3 lines 157, 159–166 (rules 1–6, esp. 4 "Retroactivity" and 5 "Oscillation and rollback semantics"); `spec/04_system-components.md` §4.8 lines 1003, 1007; `spec/11_policy-and-controls.md` §11.7 lines 66, 107–111.

**Issue.** §8.3 rules 4–5 explicitly state interceptor config changes are "never retroactive against already-approved delegations" and toggling `failPolicy` "in either direction, any number of times, never invalidates existing leases and never triggers retroactive rejection or approval of past delegations." The gateway's only reaction to a `fail-closed → fail-open` transition is an asynchronous `interceptor.fail_policy_weakened` audit event (§4.8 line 1007) and a best-effort admin alert. No automatic cool-off, no hold on new `delegate_task` calls during the transition window, no multi-admin approval, no lease invalidation, no re-inspection of children approved while fail-open.

This defines a bulk-injection path for an attacker with admin-API access (or a rogue platform operator):

1. **T0:** `failPolicy: fail-closed`. Normal delegation tree running.
2. **T0+ε:** Attacker flips interceptor to `failPolicy: fail-open` via `PUT /v1/admin/interceptors/{name}`.
3. **T0+5s:** Propagation SLO elapses (§8.3 rule 2). Attacker renders interceptor unreachable (kill pod / NetworkPolicy tweak) so `PreDelegation` → `INTERCEPTOR_TIMEOUT` → fail-open → approve.
4. **T0+5s..T0+N:** Attacker floods `delegate_task` with adversarial prompts. All approved, each allocates a child lease.
5. **T0+N:** Attacker flips back to `fail-closed`. `interceptor.fail_policy_strengthened` fires. Logs look normal.
6. **T0+N..T0+N+perChildMaxAge:** Children approved during the window continue under their leases up to `perChildMaxAge` (3600s default). Rule 5 guarantees existing leases are not invalidated. `deriveSession` / replay paths can propagate workspace state further.

Aggravating: (a) any `platform-admin` can flip `failPolicy` via a single `PUT`, no dual-control; (b) §8.3 line 157 does not snapshot interceptor configuration ("always invoked with its live configuration at the time of each `delegate_task` call"), so `snapshotPolicyAtLease: true` trees are equally exposed; (c) §4.8 line 1003 `interceptorFailOpenMaxConsecutive` defuses chronic degradation, not a timed flip-attack.

**Recommendation.** Make `fail-closed → fail-open` (and any materially-weakening change to interceptors referenced by active `DelegationPolicy`) a blocking, reviewable operation:

1. **Cooldown:** mandatory `gateway.interceptorWeakeningCooldownSeconds` (default `60s`) during which `delegate_task`/`lenny/send_message` on affected policies reject with `INTERCEPTOR_WEAKENING_COOLDOWN` (TRANSIENT 503).
2. **Lease-cancellation option:** `DelegationPolicy.contentPolicy.cancelLeasesOnFailPolicyWeaken: bool` — when set, affected child leases cancel with `LEASE_INTERCEPTOR_POSTURE_CHANGED`; subtree may re-enter fresh.
3. **Dual-control** for weakening `PUT /v1/admin/interceptors/{name}` (two distinct `platform-admin`, reuses the two-man-rule mechanism). Strengthening stays single-admin.
4. **Per-policy opt-out of oscillation pardon:** `contentPolicy.enforceInterceptorMonotonicity: true` reverses §8.3 rule 5 for that policy — weakening invalidates leases within `retroactiveWindowSeconds` (default `300s`).
5. **Surface posture on lease:** record `interceptor_fail_policy_at_issue` / `interceptor_posture_transitions_observed` on leases; expose via §15.1 tree inspection so audit tooling can identify fail-open-window approvals without event-stream correlation.
