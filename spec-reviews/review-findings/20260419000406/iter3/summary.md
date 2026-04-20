# Technical Design Review Findings — 2026-04-19 (Iteration 3)

**Document reviewed:** `spec/`
**Review framework:** `spec-reviews/review-guidelines.md` (26 perspectives)
**Iteration:** 3 of 3
**Total findings:** 107 across 26 review perspectives

## Findings Summary

| Severity | Count |
| -------- | ----- |
| Critical | 1     |
| High     | 20    |
| Medium   | 48    |
| Low      | 38    |

### Critical Findings

| #       | Perspective    | Finding                                                                                    | Section       | Status |
| ------- | -------------- | ------------------------------------------------------------------------------------------ | ------------- | ------ |
| NET-051 | Network policy | `lenny-ops` entirely absent from `lenny-system` NetworkPolicy allow-lists; default-deny blocks every lenny-ops admin-API, datastore, and prometheus flow | 13.2 + 25.4 | Fixed  |

### High Findings

| #       | Perspective             | Finding                                                                                  | Section |
| ------- | ----------------------- | ---------------------------------------------------------------------------------------- | ------- |
| API-005 | External API            | Duplicate `ISOLATION_MONOTONICITY_VIOLATED` catalog rows (403 vs 422)                   | 15.1 |
| BLD-005 | Build sequence          | Phase 3.5 admission-policy list incomplete against §17.2's 12-item enumeration           | 18.4 |
| BLD-006 | Build sequence          | Phase 4.5 bootstrap seed missing `global.noEnvironmentPolicy` (TNT-002 propagation)      | 18.4 |
| CMP-046 | Compliance              | Post-restore erasure reconciler can destroy legally-held data                            | 19.4 |
| DEL-009 | Delegation              | `DELEGATION_PARENT_REVOKED` / `DELEGATION_AUDIT_CONTENTION` not catalogued in §15.1       | 8 + 15.1 |
| FLR-006 | Failure/resilience      | Gateway PDB `minAvailable: 2` at Tier 1 blocks node drains and rolling updates           | 17.5 |
| K8S-038 | Kubernetes              | Per-webhook unavailability alerts missing for five of the eight enumerated webhooks      | 17.2 + 16.5 |
| MSG-007 | Messaging               | Durable-inbox Redis command set contradicted between §7.2 (RPUSH/LPOP) and §12.4 (LPUSH) | 7.2 + 12.4 |
| NET-052 | Network policy          | `lenny-ops-egress` Redis rule targets plaintext 6379 (Redis plaintext disabled)          | 25.4 |
| NET-053 | Network policy          | `lenny-ops-egress` MinIO rule targets 9000 while §13.2 mandates 9443 TLS                  | 25.4 |
| NET-054 | Network policy          | `lenny-ops-egress` uses non-immutable `name:` label against §13.2 guidance               | 25.4 |
| NET-055 | Network policy          | `lenny-ops-egress` / `lenny-backup-job` K8s-API rules use empty `namespaceSelector`       | 25.4 |
| NET-056 | Network policy          | `lenny-backup-job` egress to Postgres/MinIO omits `namespaceSelector`                     | 25.4 |
| OBS-012 | Observability           | Alerts reference unregistered metrics (`siem_delivery_lag_seconds`, `CredentialCompromised`, `lenny_controller_workqueue_depth`) | 16.5 |
| OBS-016 | Observability           | Alert-name drift between §16.5 and §25.13 Tier Table                                       | 16.5 + 25.13 |
| PRT-008 | Protocols               | Shared Adapter Types fix introduces undefined `CallerIdentity` and `PublishedMetadataRef` | 15.2 |
| SEC-003 | Security                | Duplicate `ISOLATION_MONOTONICITY_VIOLATED` catalog rows — regression from SEC-001 fix    | 15.1 |
| SEC-005 | Security                | `/run/lenny/credentials.json` mode 0400 w/ cross-UID ownership unsatisfiable under §13.1 | 4.8 + 13.1 |
| STR-007 | Streaming               | `SessionEvent.SeqNum` has no client-facing resume path — clients lose events on drop (**Fixed**)     | 15.2 |
| TNT-005 | Tenancy                 | Playground `apiKey` auth references an undefined "standard API-key auth path"            | 27.2 + 10.2 |

---

## Detailed Findings by Perspective

---

## 1. Kubernetes (K8S)

### K8S-038. Per-webhook unavailability alerts missing for five of eight webhooks [High] — **Fixed**
**Section:** 17.2, 16.5

Iter2 K8S-037 expanded §17.2's admission-plane inventory from 5 to 12 items (8 webhook Deployments, 4 policy manifests) and added a preflight inventory check. §16.5 defines alerts only for `lenny-pool-config-validator`, `lenny-label-immutability`, and `lenny-sandboxclaim-guard`. The remaining five (`lenny-direct-mode-isolation`, `lenny-t4-node-isolation`, `lenny-drain-readiness`, `lenny-crd-conversion`, `lenny-data-residency-validator`) have no unavailability alert — operators lose observability on the very webhooks that can silently fail-open for secondary admission decisions.

**Recommendation:** Add `…WebhookUnavailable` alerts (warning severity, `up{job="…"} == 0 for 5m`) for each of the five missing webhook deployments; cross-reference §17.2 inventory row for mutual completeness.

**Status:** Fixed. Added five new Warning-severity alerts in §16.5 (`LabelImmutabilityWebhookUnavailable`, `DirectModeIsolationWebhookUnavailable`, `T4NodeIsolationWebhookUnavailable`, `DrainReadinessWebhookUnavailable`, `CrdConversionWebhookUnavailable`) using the `up{job="…"} == 0` / 5 m pattern. Note: the original finding text is slightly off on the inventory of pre-existing alerts — §16.5 had `SandboxClaimGuardUnavailable`, `PoolConfigValidatorUnavailable`, and `DataResidencyWebhookUnavailable` (plus the generic `AdmissionWebhookUnavailable`), while `LabelImmutabilityWebhookUnavailable` was among the missing, not present; the five newly added alerts cover the actual gap. Updated §17.2's "per-webhook unavailability alerts enumerated in §16.5" cross-reference to list all eight webhook alerts plus the generic one.

### K8S-039. §13.2 NetworkPolicy admission-webhook enumeration disagrees with §17.2 [Medium] — **Fixed**
**Section:** 13.2, 17.2

The §13.2 NetworkPolicy allow-list names "admission-webhook pods" as a single origin for port 8080 ingress. §17.2 now enumerates 8 distinct webhook deployments. A single `app: admission-webhook` label cannot match all eight without a label convention spec'd in §13.2 or §17.2.

**Recommendation:** In §13.2, either broaden the selector to `{ lenny.dev/component: admission-webhook }` (and require all 8 deployments to carry that label in §17.2), or enumerate each individually.

**Status:** Fixed. Updated the §13.2 admission-webhook NetworkPolicy row to explicitly enumerate all 8 webhook Deployments (`lenny-label-immutability`, `lenny-direct-mode-isolation`, `lenny-sandboxclaim-guard`, `lenny-data-residency-validator`, `lenny-pool-config-validator`, `lenny-t4-node-isolation`, `lenny-drain-readiness`, `lenny-crd-conversion`) by name and to state that all 8 carry the `lenny.dev/component: admission-webhook` label so the single `podSelector` matches every webhook pod, with a cross-reference to the NET-047/NET-050 selector-consistency audit. Added a matching statement in §17.2's HA paragraph requiring all 8 Deployments to carry the `lenny.dev/component: admission-webhook` pod label, cross-referencing §13.2 and stating that the `lenny-preflight` Job fails if any webhook Deployment's pods are missing the label. The existing NET-047/NET-050 normative selector requirement already mandates `lenny.dev/component: admission-webhook` for this component; the fix closes the enumeration and label-convention gap across the two sections.

---

## 2. Security (SEC)

### SEC-003. Duplicate `ISOLATION_MONOTONICITY_VIOLATED` catalog rows with conflicting HTTP status [High]
**Section:** 15.1 (lines 771 + 800)

Iter2 SEC-001 fix added a derive-time row but left the pre-existing delegation-time row in place. Both share the same error code but map to different HTTP statuses (403 vs 422), violating §15.1's stated invariant that codes are globally unique.

**Recommendation:** Collapse to one row with the status that applies to *both* endpoints, or split into two distinct codes (`ISOLATION_MONOTONICITY_VIOLATED_DELEGATE` / `…_DERIVE`).

**Status:** Fixed. Deleted the duplicate delegation-time row at old line 771 (the 403 variant) and expanded the surviving row in §15.1's error-code catalog to explicitly cover all three triggering paths: `delegate_task` (delegation), `POST /v1/sessions/{id}/derive`, and `POST /v1/sessions/{id}/replay` with `replayMode: workspace_derive`. Canonical HTTP status is now **422** uniformly — matching the already-stated 422 in §7.1 (derive semantics) and §15.1 replay semantics, and consistent with 422 "unprocessable entity" semantics (the request body names a pool whose isolation profile is incompatible with the source session — a semantic validation failure, not an identity-authorization failure). `details` field set standardized to `sourceIsolationProfile` / `targetIsolationProfile` / `targetPool`, with a note preserving backward-compatible read-side alias of the legacy delegation keys (`parentIsolation` / `targetIsolation`). Admin-override path (`allowIsolationDowngrade: true`) explicitly scoped to derive/replay only, not delegation. No prose edits needed in §8.3 or §10.7 (neither stated an HTTP status for the delegation-time rejection). Test matrix at §15.1 line 1088 unaffected — it asserts only `code`, `category`, `retryable` equivalence.

### SEC-004. Self-contradictory `prompt_history` clause in replay endpoint [Medium] — **Fixed**
**Section:** 15.1 replay semantics

The new replay paragraph both prohibits and permits `prompt_history` in the same sentence ("MUST NOT include prompt history; MAY stream prior prompts for audit reconstruction"). Implementers get no guidance.

**Recommendation:** Pick one. If audit reconstruction must stream prompts, flag the response as audit-only and mark it with a dedicated envelope field.

**Status:** Fixed. Note: `summary.md` mis-summarized the finding — the actual contradiction (per `SEC.md` detail) is in §15.1 line 514's `allowIsolationDowngrade` description, which first asserted "The flag has no effect when `replayMode: prompt_history`" and then immediately qualified "any `targetPool` override in `prompt_history` mode is also subject to the monotonicity rule and the same `allowIsolationDowngrade` flag." Fixed by rewriting line 514 to state one unified rule: the monotonicity check + `allowIsolationDowngrade` override apply whenever the replay targets a pool different from the source session's, with three explicit cases spelled out — (a) `workspace_derive`: check runs against resolved `targetPool`; (b) `prompt_history` without `targetPool`: source pool inherited, check trivially satisfied, flag is a no-op; (c) `prompt_history` with `targetPool`: rule and flag behave identically to `workspace_derive`. Also updated §7.1 item 5 to remove the narrower "only when `replayMode: workspace_derive` is set" scoping and replace it with "whenever the resolved `targetPool` differs from the source session's pool" so §7.1 and §15.1 agree. No envelope-field change needed — the finding's "audit-only / envelope flag" remediation was based on the misphrased summary and doesn't apply to the real bug.

### SEC-005. `/run/lenny/credentials.json` cross-UID ownership unsatisfiable under §13.1 capability rules [High] — **Fixed**
**Section:** 4.7 + 13.1 (note: finding cited §4.8, but the credential-file ownership clause is in §4.7 item 4 of the Adapter-Agent Security Boundary)

§4.7 required the file to be mode 0400 and owned by the agent UID (the finding text says "adapter UID"; actual prose says "agent UID") written by the adapter running as a distinct UID, while §13.1 drops all capabilities (including `CAP_CHOWN`). No `chown(2)` can execute in any agent-pod container, so a file written by the adapter UID cannot be reassigned to the agent UID.

**Recommendation:** Either relax the file to group-readable and rely on supplementary group membership, or explicitly permit the init container to retain `CAP_CHOWN` via `securityContext.capabilities.add` with admission-policy justification.

**Status:** Fixed via the fsGroup + supplementary-group approach (recommendation option a), chosen because it preserves the "All capabilities dropped" invariant in §13.1 intact and uses Kubernetes-native primitives rather than weakening the capability posture.

**Edits:**
1. `04_system-components.md` §4.7 item 4 (line 867): changed mode `0400` to `0440`; rewrote the ownership clause to state that the file is owned by the adapter UID (the writer) and group-owned by the shared `lenny-cred-readers` supplementary group (of which the agent UID is also a member); documented that the tmpfs volume is mounted with `fsGroup: <lenny-cred-readers GID>` at the pod `securityContext` level, so the kubelet sets group ownership at mount time with no `chown` syscall required in any container. Preserves the "all capabilities dropped" invariant explicitly.
2. `04_system-components.md` §4.9 encoding conventions (line 1211): updated the mode reference from `0400` to `0440` with a cross-reference to §4.7 item 4 for the full ownership scheme.
3. `04_system-components.md` §4.9 Security Boundaries (line 1613): updated the mode reference from `0400` to `0440` with the `lenny-cred-readers` group-read restriction and cross-reference to §4.7 item 4.
4. `04_system-components.md` §4.9 SPIFFE-binding deployment note (line 1465): updated the defense-in-depth wording from "tmpfs mode 0400" to "tmpfs mode `0440` with group-read restricted to the `lenny-cred-readers` supplementary group" for consistency.
5. `06_warm-pod-model.md` concurrent-mode credential lease lifecycle (line 28): updated per-slot credentials file mode from `0400` to `0440` with a cross-reference to §4.7 item 4 for the ownership scheme.
6. `13_security-model.md` §13.1: added a new paragraph "**Cross-UID file delivery without `CAP_CHOWN` (fsGroup-based)**" immediately after the pod-namespace-isolation paragraph. Explains why "All dropped" (including `CAP_CHOWN`) does not conflict with the §4.7 credential-file ownership requirement: the tmpfs volume uses `spec.securityContext.fsGroup: <lenny-cred-readers GID>`, both containers declare the group via `supplementalGroups`/`runAsGroup`, and the file is written mode `0440` (owner-read-write, group-read, no access for others). Adds a new admission-webhook failure code `POD_SPEC_CRED_FSGROUP_MISSING` that is returned if a generated pod template omits the `lenny-cred-readers` fsGroup (validated by the admission webhook and `lenny-preflight` Job).

**Why option (a) over option (b):** The finding offered either (a) fsGroup + supplementary-group membership, or (b) adding `CAP_CHOWN` back on an init container with admission-policy justification. Option (a) is strictly better: it preserves the "All capabilities dropped" invariant intact across every agent-pod container, avoids introducing a capability carve-out that would become a future maintenance burden (admission policy for the capability, audit events, runbook for accidental misconfiguration), and is the canonical Kubernetes-native mechanism for exactly this use case. The file's mode tightens from "owner-only read" (`0400`) to "owner-write, group-read" (`0440`), with the group restricted to the single-purpose `lenny-cred-readers` group shared only between the adapter and agent — so the blast radius is unchanged in practice (both the adapter and agent UIDs already have access to the credential material by design; no third UID gains access).

**Other findings to note from this fix:** None of the §13.1 "Capabilities: All dropped" table cell or any §4.9 security-boundaries bullet are weakened; the fix tightens the spec by making the ownership requirement actually realizable under the existing capability posture.

### SEC-006. SPIFFE-binding disablement not enforced in multi-tenant mode [Medium] — **Fixed**
**Section:** 4.9 + 17.2 (note: the finding summary says §13.x, but the SPIFFE-binding deployment note lives in §4.9's LLM Reverse Proxy description; the §13 admission-webhook inventory is the cross-reference surface)

Spec asserts SPIFFE-binding is enforced in multi-tenant mode and can be disabled "only for single-tenant or development deployments," but §4.9 line 1465 wired this only to a `ProxyModeSpiffeBindingDisabled` warning event at pool registration time — no admission webhook, no CRD validation, no preflight check. An operator could silently weaken multi-tenant proxy-mode's cross-pod lease-token replay defense by setting `credentialPool.spiffeBinding: disabled` under `tenancy.mode: multi` with only a warning emitted. The summary's phrasing ("a tenant with `SpiffeBindingEnabled: true` in a CR proceeds silently" / "rejects SpiffeBinding activation outside single-tenant deployments") has the polarity reversed relative to the actual spec — the real gap is *disablement* going unchecked in multi-tenant mode, not *enablement* proceeding silently outside single-tenant. Fix addresses the real gap.

**Recommendation (as stated):** Add a validating admission rule that rejects SpiffeBinding activation outside single-tenant deployments, with error `SPIFFE_BINDING_FORBIDDEN_MULTITENANT`.

**Status:** Fixed via the template already used for the sibling multi-tenant admission control (`DirectModeStandardIsolationMultiTenantRejected` at §4.9 line 1434) — two-layer enforcement (warm pool controller pool-registration validation + `lenny-direct-mode-isolation` `ValidatingAdmissionWebhook` with `failurePolicy: Fail`), explicit opt-in field in single-tenant/dev, and startup preflight scan of all registered `CredentialPool`s. Chose to extend the existing `lenny-direct-mode-isolation` webhook rather than add a new `lenny-proxy-mode-spiffe-binding` webhook — avoids rippling the admission-plane inventory count (the 12-item enumeration in §17.2 + 8 per-webhook alerts in §16.5 + BLD-007 Phase 3.5 deliverables would all need updates otherwise), matches the finding's stated "extend or add" allowance, and keeps edits minimal.

**Edits:**
1. `04_system-components.md` §4.9 "Deployment note" (line 1465): rewrote the note from a warning-event-only control to a full admission-enforced control mirroring the `DirectModeStandardIsolationMultiTenantRejected` template (line 1434). Added the `ProxyModeSpiffeBindingDisabledMultiTenantRejected` validation error, specified two-layer enforcement (warm pool controller + extended `lenny-direct-mode-isolation` webhook), the `allowProxyModeSpiffeBindingDisabled: true` single-tenant/dev opt-in field, upgrade of the disablement signal to both a Kubernetes warning event and a new audit event, and a startup preflight scan that fails multi-tenant installs where any pool has `spiffeBinding: disabled`. Error code naming follows the spec's existing CamelCase admission-error convention (`InvalidPoolEgressDeliveryCombo`, `DirectModeStandardIsolationMultiTenantRejected`) rather than the finding's suggested `SPIFFE_BINDING_FORBIDDEN_MULTITENANT`, since the upstream convention at this enforcement layer is CamelCase validation identifiers, not SCREAMING_SNAKE_CASE wire codes.
2. `04_system-components.md` §4.9.2 credential audit-events table: added a new row for `credential.proxy_mode_spiffe_binding_disabled` with key fields (`tenant_id`, `pool_id`, `tenancy_mode`, `authorizing_user_sub`), so disablement in single-tenant/dev lands in the `credential.*` audit stream as the recommendation required.
3. `17_deployment-topology.md` §17.2 admission-plane enumeration item 6: updated the `lenny-direct-mode-isolation` description to reflect the extended scope — now rejects both `(a) direct + standard` and `(b) proxy + spiffeBinding: disabled` when `tenancy.mode: multi`, with a cross-reference to the new §4.9 deployment note.

**Why extending the existing webhook over adding a new one:** The finding explicitly permits either ("Extend `lenny-direct-mode-isolation` (or add `lenny-proxy-mode-spiffe-binding`…)"). Extending is strictly less disruptive: the 12-item admission-plane enumeration in §17.2, the 8 per-webhook unavailability alerts in §16.5 (K8S-038 closed this set in iter2), Phase 3.5 build-sequence deliverables (BLD-005/BLD-007), the `lenny-preflight` hard-coded webhook list, and the `tests/integration/admission_webhook_inventory_test.go` expected-set all remain structurally intact. A new webhook would require touching all five of those surfaces in parallel to this fix. The enforcement semantics are identical either way — both are pool-registration + `ValidatingAdmissionWebhook` checks keyed on `tenancy.mode: multi`.

**Regression check:** No contradiction introduced with existing prose — the base §4.9 line 1460's "v1 requirement for multi-tenant deployments" assertion, the line 1467 threat-model paragraph, and the existing single-tenant defense-in-depth wording (`0440` tmpfs mode, group-read restricted to `lenny-cred-readers`) all remain consistent with the tightened admission posture. The existing `ProxyModeSpiffeBindingDisabled` Kubernetes warning event is preserved (now emitted alongside the new audit event in single-tenant/dev, and alongside the hard rejection in multi-tenant mode). No cross-reference to §4.9's deployment note in §11.7, §16.5, §18, or elsewhere needs updating — the `credential.*` audit-stream pattern already routes through §11.7 hash-chain and SIEM controls.

### SEC-007. Interceptor `failPolicy` oscillation enables bulk prompt-injection across delegation trees [Medium] — **Fixed**
**Section:** Interceptor lifecycle (§8.3 rule 5, §4.8, §11.7, §15.1)

Interceptor lease renewals that flip `failPolicy` between `Fail` and `Ignore` at each renewal window expose a race where an attacker observing renewal timing can inject prompts during the `Ignore` slice.

**Recommendation:** Pin `failPolicy` for the lease lifetime; changes require lease revocation and a fresh delegation tree.

**Status:** Fixed — note: finding's premise was partially mis-phrased. The spec does not model interceptors as "leased" with "renewal windows" and does not use K8s-webhook terminology (`Fail`/`Ignore`); interceptors are registered via admin API (`PUT /v1/admin/interceptors/{name}`) with `failPolicy: fail-closed | fail-open`. The real underlying concern (detailed in `SEC.md` for SEC-007) is that a `platform-admin` (or compromised admin token) can execute a timing-observable `fail-closed → fail-open` flip and — after the ≤5 s propagation SLO — flood `delegate_task` calls through the fail-open window, then flip back. Existing leases approved during the window survive per §8.3 rule 5. Finding's literal recommendation ("pin for lease lifetime, require lease revocation and fresh delegation tree on changes") directly contradicts the explicit normative design in §8.3 rules 1 and 3 (no snapshotting, per-invocation live config) and would make operational interceptor adjustments impossible — rejected as written.

**Selected fix (surgical, preserves existing design):** Add a mandatory **weakening cooldown** on `fail-closed → fail-open` transitions. During the cooldown, affected `delegate_task` / `lenny/send_message` calls reject with a new transient error so the attacker cannot exploit the fail-open window for bulk approval. Strengthening transitions (`fail-open → fail-closed`) are not rate-limited — tightening posture must always take effect immediately.

**Edits:**
1. `08_recursive-delegation.md` §8.3 "Interceptor configuration lifecycle" rule 5: appended a new "Weakening cooldown" paragraph specifying (a) the cooldown applies to `fail-closed → fail-open` transitions on interceptors referenced by any active `DelegationPolicy`; (b) `gateway.interceptorWeakeningCooldownSeconds` tunable (default 60 s, min 30 s, max 600 s); (c) during the window, affected requests reject with `INTERCEPTOR_WEAKENING_COOLDOWN`; (d) replica-uniform enforcement via transition timestamp recorded in the shared interceptor registry (not local clocks); (e) back-to-back weakening transitions reset the window (so oscillation extends, not shortens, exposure); (f) the one-time-per-window `interceptor.weakening_cooldown_active` audit event; (g) strengthening transitions are never rate-limited. Preserves rule 5's existing "never invalidates existing leases / never triggers retroactive rejection" guarantee — the cooldown blocks NEW approvals only.
2. `04_system-components.md` §4.8 "`failPolicy` change audit event" paragraph: added a one-sentence cross-reference noting that a weakening transition additionally triggers the mandatory cooldown + `INTERCEPTOR_WEAKENING_COOLDOWN` rejection, with a pointer to §8.3 rule 5 for full semantics; clarified that the reverse transition is not subject to the cooldown.
3. `15_external-api-surface.md` §15.1 error-code catalog: added `INTERCEPTOR_WEAKENING_COOLDOWN` row (category `TRANSIENT`, HTTP 503, retryable after `details.cooldown_remaining_seconds`); detail fields `interceptor_ref`, `transition_ts`, `cooldown_remaining_seconds`, `affected_policy`.
4. `11_policy-and-controls.md` §11.7 audit-event catalog: added `interceptor.weakening_cooldown_active` row. Extended the `affected_policy_count` and `affected_policy_names` schema rows to cover the new event, and added two new schema-field rows for `transition_ts` (RFC 3339) and `cooldown_seconds` (uint32) scoped to the new event.

**Why this over the finding's recommendation:** The finding's "pin for lease lifetime, fresh delegation tree on change" directly reverses the spec's explicit normative choices in §8.3 rules 1 and 3 (live config, no snapshotting, per-invocation evaluation), which were themselves added to resolve iter2 ambiguity about interceptor config lifecycle. Reversing those rules would either (a) require snapshotting interceptor config into every lease (contradicts rule 1 verbatim) or (b) force full delegation-tree teardown on every operational interceptor adjustment — both are severe regressions against the intended operability posture. The cooldown approach closes the real attack window identified in the `SEC.md` detail (the ≤5 s propagation + flood window) without touching the lease-binding model. It is strictly a new defensive layer, not a design change. The finding's detail (SEC.md) explicitly listed "Cooldown" as recommendation item 1 with a default of 60 s — which is what this fix implements. Other SEC.md recommendation items (dual-control, lease cancellation, per-policy opt-out of oscillation pardon, posture metadata on leases) were evaluated and deferred: dual-control on admin API is a broader platform control orthogonal to this finding's core race and would ripple into every admin endpoint's auth model; lease cancellation + posture metadata are larger schema changes with their own regression surface; per-policy opt-out reverses rule 4 for some callers and reintroduces the ambiguity those rules were added to close. The cooldown alone eliminates the timing-observable bulk-injection window — the specific race the finding flags.

**Regression check:** No contradiction with §8.3 rules 1–4 (no snapshotting, live config, per-invocation, no retroactivity) — the cooldown reads the live registry at each invocation and gates **new** approvals only; already-approved leases continue unchanged (rule 4 preserved). No contradiction with the existing "cumulative fail-open escalation" at §4.8 line 1003 (`interceptorFailOpenMaxConsecutive`) — that control defuses chronic degradation under load; this one defuses timed admin-flip attacks. The `interceptor.fail_policy_weakened` / `interceptor.fail_policy_strengthened` audit events remain as-is; the new `interceptor.weakening_cooldown_active` event is additive. §11.7's audit-event schema null/absent-field contract (lines around 117) still holds — new conditional fields (`transition_ts`, `cooldown_seconds`) are scoped to the new event only and follow the same "absent for non-applicable event types" rule. No build-sequence or observability catalog changes required — the cooldown tunable follows the existing §4.8 prose pattern for `interceptorFailOpenMaxConsecutive` (no §17.8.1 defaults row), and the new audit event routes through the existing `interceptor.*` SIEM stream.

---

## 3. Network Policy (NET)

### NET-051. `lenny-ops` absent from `lenny-system` NetworkPolicy allow-lists [Critical] — **Fixed**
**Section:** 13.2, 25.4

The §13.2 enumeration lists gateway, token-service, controller, pgbouncer, minio, admission-webhook, coredns, and OTLP — but no `lenny-ops` row. Every `lenny-ops` → gateway admin-API (8080), Postgres, Redis, MinIO, Prometheus call is blocked by the default-deny policy. This breaks every documented operability flow at install time.

**Recommendation:** Add a full `lenny-ops` row and matching ingress rows to Gateway, PgBouncer, MinIO, Redis allow-lists.

**Status:** Fixed. Edits to `13_security-model.md` §13.2:

1. **New `lenny-ops` row** appended to the component allow-list table after the OTLP Collector row. Selector uses `app: lenny-ops` per the line 201 selector exception. Egress enumerated: Gateway admin-API (TCP 8080), PgBouncer (TCP 5432), Redis (TCP 6380 TLS), MinIO (TCP 9443 TLS), Prometheus (TCP 9090 in the monitoring namespace), kube-apiserver (TCP 443 via `kubeApiServerCIDR`), `kube-system` CoreDNS (UDP/TCP 53), external HTTPS (TCP 443 for webhook delivery with RFC1918/link-local/ULA/cluster-internal `except` clauses via the `lenny-ops-egress` rule in §25.4). Ingress enumerated: Ingress controller namespace on TCP 8090 (the `lenny-ops` Service port) and monitoring namespace on TCP 9090 (Prometheus scrape). Also states that no in-cluster workload other than the Ingress controller may reach `lenny-ops`, preserving the "external by design" property of §25.4.
2. **Gateway row (§13.2 line 206) ingress amended** to add `lenny-ops` pods on TCP `{{ .Values.gateway.internalPort }}` (default 8080) for all the admin-API flows enumerated in §25.3 (health aggregation, configuration, backup orchestration, remediation, upgrade, diagnostics, connector probes). Rendered-rule shape documented inline: `from: [{ namespaceSelector: { matchLabels: { kubernetes.io/metadata.name: "{{ .Release.Namespace }}" } }, podSelector: { matchLabels: { app: lenny-ops } } }]` per the line 201 selector exception.
3. **PgBouncer row (§13.2 line 209) ingress amended** to add `lenny-ops` pods on TCP 5432 (audit queries, upgrade state, backup metadata), with the same `app: lenny-ops` + `kubernetes.io/metadata.name: "{{ .Release.Namespace }}"` selector shape.
4. **MinIO row (§13.2 line 210) ingress amended** to add `lenny-ops` pods AND `lenny-backup` Job pods on TCP 9443 TLS (backup/restore object operations), with selector shapes for both (`app: lenny-ops` / `app: lenny-backup`) paired with the release-namespace selector.
5. **New cross-cutting note (`lenny-ops` counterparty rules, NET-051)** inserted after the table, stating that the rendered chart emits three categories of rules (egress on `lenny-ops` pods, ingress clauses on Gateway/PgBouncer/MinIO and — when Redis is self-managed — Redis, plus the monitoring-scrape ingress), and extending the NET-047/050 `lenny-preflight` selector-consistency audit to validate every `lenny-ops` counterparty clause uses the canonical selector shape and fails install when any clause is missing (since `lenny-ops` is mandatory per §25.1).
6. **NET-045 Prometheus monitoring ingress note updated** to include `lenny-ops` (TCP 9090, `app: lenny-ops`) in the list of `lenny-system` components whose metrics port is scrapeable from `{{ .Values.monitoring.namespace }}`.

**Why these specific edits over alternatives:**

- *Row placement:* appended after OTLP Collector to match the chronological order components are introduced in prose, avoiding disruption of the existing Gateway/Token Service/Controller ordering.
- *Selector idiom:* used `{{ .Release.Namespace }}` rather than a non-existent `ops.namespace` Helm value, matching §25.4's use of `{Release.Namespace}` throughout (line 993, line 1041, line 1175).
- *Redis handling:* the existing §13.2 table has no standalone Redis row (only Gateway and Token Service egress reference Redis); creating one is outside NET-051 scope. The NET-051 cross-cutting note instead explicitly calls out that a Redis ingress clause "is emitted alongside the Gateway and Token Service allow-rules when Redis is self-managed," leaving the structural Redis-row gap to a separate future finding.
- *§25.4 `lenny-ops-egress` port-value corrections (6379→6380 and 9000→9443 for Redis/MinIO):* these are NET-052 and NET-053 respectively and explicitly out of scope for NET-051; not touched.

**Regression check:** No contradiction with §13.2's normative selector requirement at line 201 (which explicitly excepts `lenny-ops` from the `lenny.dev/component` rule). The `lenny-backup` Job ingress row addition to MinIO aligns with §25.4's `lenny-backup-job` NetworkPolicy (line 1194), closing the same class of gap on the counterparty side. The new row preserves `lenny-ops`'s "external by design" property (§25.4 line 50) by specifying that in-cluster ingress is allowed only from the Ingress controller and monitoring namespaces. Cross-reference integrity: `[Section 25.1](25_agent-operability.md#251-overview)`, `[Section 25.3](25_agent-operability.md#253-endpoint-split-between-gateway-and-lenny-ops)`, and `[Section 25.4](25_agent-operability.md#254-the-lenny-ops-service)` all resolve. No other findings (NET-052..060) are affected — they remain independent issues within the §25.4 chart itself.

### NET-052. `lenny-ops-egress` Redis rule targets plaintext 6379 [High]
**Section:** 25.4

Rule permits TCP 6379 egress; Redis is configured with plaintext disabled in §12.4. Connection always fails.

**Recommendation:** Change to 6380 (Redis TLS port).

**Status:** Fixed. Changed the `lenny-ops-egress` Redis egress rule port from 6379 to 6380 in §25.4 (line 1138) and amended the inline comment to note that plaintext port 6379 is disabled per §12.4 / §10.7. This aligns the NetworkPolicy with the self-managed Redis TLS posture already specified for the Token Service in §13.2 and the `rediss://…:6380` DSNs in §17.9. No other 6379 references in §25.4; the 26379 Sentinel ports and the AWS ElastiCache `rediss://…:6379` example in §17.9 are out of scope (ElastiCache uses 6379 for TLS by provider convention).

### NET-053. `lenny-ops-egress` MinIO rule targets 9000 while §13.2 requires 9443 TLS [High]
**Section:** 25.4

Rule permits 9000; §13.2 normative rule is 9443/TLS. Breaks uploads + backup writes.

**Recommendation:** Change to 9443.

**Status:** Fixed. Changed the `lenny-ops-egress` MinIO egress rule port from 9000 to 9443 in §25.4 (line 1141) and added an inline TLS comment matching NET-052's Redis row style. Applied the identical port change to the sibling `lenny-backup-job` MinIO egress rule (line 1204), since the finding's rationale explicitly cites backup writes as broken and the NET.md Files list enumerates both rules. Literal port `9443` was used to match the surrounding block's numeric-literal style (5432, 8080, 9090); the Helm template `{{ .Values.minio.tlsPort }}` was not substituted in because §25.4's YAML blocks consistently use literal ports rather than chart templating. The two remaining `:9000` references in §25.4 (lines 954/958) are illustrative multi-region `minioEndpoint` URL strings in answer-file examples — out of scope for a NetworkPolicy-only finding and tied to a separate pre-existing prose inconsistency between §12.279 (`https://minio.lenny-system:9000`) and §13.2's 9443 listener, not addressed by NET-053.

### NET-054. `lenny-ops-egress` uses non-immutable `name:` label [High] — **Fixed**
**Section:** 25.4

`namespaceSelector` uses `matchLabels: { name: lenny-system }`. `name` is mutable and not guaranteed on kube namespaces. §13.2 mandates `kubernetes.io/metadata.name` (auto-populated, immutable).

**Recommendation:** Replace with `kubernetes.io/metadata.name`.

**Resolution:** Replaced the mutable `name:` key with the immutable `kubernetes.io/metadata.name:` key in all four `namespaceSelector` entries of the `lenny-ops-egress` NetworkPolicy (Postgres 5432, Redis 6380, MinIO 9443, Prometheus 9090) in §25.4 at `spec/25_agent-operability.md:1140,1143,1146,1149`. Added an inline comment citing NET-054 and the §13.2 normative rationale. The gateway (line 1129) and kube-system (line 1155) selectors in the same block already used the immutable key (fixed under NET-047).

### NET-055. K8s-API rules use empty `namespaceSelector` [High] — Fixed

**Section:** 25.4

`lenny-ops-egress` and `lenny-backup-job` K8s-API rules specify empty `namespaceSelector: {}`, permitting egress to every pod in every namespace on 443 — wildly overbroad; includes unintended targets.

**Recommendation:** Scope to `kubernetes.io/metadata.name: default` with `podSelector: { component: apiserver }` or use `kube-apiserver.default.svc` IP carve-out.

**Resolution:** Replaced both `namespaceSelector: {}` K8s-API egress rules with `ipBlock: { cidr: "{{ .Values.kubeApiServerCIDR }}" }`, matching the §13.2 NET-040 idiom already used by every other `lenny-system` component's kube-apiserver egress rule (gateway, controller, webhooks). Chose `ipBlock` over the `namespaceSelector + podSelector` alternative because in managed Kubernetes (GKE/EKS/AKS) the kube-apiserver runs outside the cluster and is reached via the `kubernetes.default` Service ClusterIP — not a labelled pod — so a pod-selector-based rule would match zero pods. The `ipBlock` form works uniformly on self-hosted and managed clusters. Added an inline comment in `lenny-ops-egress` (§25.4, `spec/25_agent-operability.md:1151-1163`) documenting that `namespaceSelector: {}` is a forbidden idiom in the Lenny chart and that the `lenny-preflight` Job rejects any rule combining an empty `namespaceSelector` with a non-loopback port. Added a shorter comment on the `lenny-backup-job` K8s-API rule (`:1221-1224`) citing NET-055 and the §13.2 NET-040 source.

### NET-056. `lenny-backup-job` egress to Postgres/MinIO omits `namespaceSelector` [High] — Fixed
**Section:** 25.4

Selectors imply "in the same namespace" — wrong for cross-namespace or cloud-managed endpoints.

**Recommendation:** Make `namespaceSelector` explicit; document that lenny-system is the default target.

**Resolution:** Added explicit `namespaceSelector` with the immutable `kubernetes.io/metadata.name: { storage.namespace }` key alongside the existing `podSelector` on both the Postgres (5432) and MinIO (9443) egress rules in the `lenny-backup-job` NetworkPolicy (§25.4, `spec/25_agent-operability.md:1230-1241`). Rules now use the same two-selector shape as the NET-050 cross-namespace gateway rule and the immutable-key convention from the NET-054 `lenny-ops-egress` fix. Added an inline comment explaining the K8s NetworkPolicy semantic (a `to:` clause with only a `podSelector` is scoped to the source pod's namespace, which silently matches zero pods when storage lives elsewhere) and documenting the cloud-managed / per-region pattern: the chart replaces these rules with `ipBlock` egress entries resolved at render time, and `lenny-preflight` fails the install if any configured backup endpoint is not covered. Updated the trailing prose accordingly.

### NET-057. Gateway external HTTPS egress omits RFC1918 exclusions applied to lenny-ops [Medium] — Fixed
**Section:** 13.2

Gateway path allows 0.0.0.0/0:443 without the 10.0.0.0/8 / 172.16.0.0/12 / 192.168.0.0/16 SSRF carve-outs that §25.4 applies to `lenny-ops`. The higher-risk surface is weaker.

**Recommendation:** Mirror the RFC1918 exclusions on gateway egress.

**Resolution:** Introduced a shared Helm value `egressCIDRs.excludePrivate` (default: `["10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16", "169.254.0.0/16", "fc00::/7", "fe80::/10"]`) that is rendered into the `except` block of every `0.0.0.0/0`-based egress rule originating from a `lenny-system` surface with tenant-influenced outbound URLs. Applied it to: (1) the gateway `allow-gateway-egress-llm-upstream` NetworkPolicy in §13.2 (`spec/13_security-model.md` lines 317-356), picking up RFC1918 (10/8, 172.16/12, 192.168/16), IPv4 link-local (169.254/16), IPv6 ULA (fc00::/7), and IPv6 link-local (fe80::/10) in addition to the existing cluster-CIDR and IMDS exclusions; (2) the `lenny-ops-egress` webhook rule in §25.4 (`spec/25_agent-operability.md` line 1168), adding fc00::/7 and fe80::/10 so the rule matches the §13.2 table row at line 214 which already claimed "RFC1918, link-local, ULA, and cluster-internal CIDRs" coverage. Updated the §13.2 gateway-row table entry (line 206) and the "Default scope caveat" paragraph to document the symmetric exclusion list and the SSRF-threat-model rationale. Added a new normative note immediately below `allow-gateway-egress-llm-upstream` stating that `egressCIDRs.excludePrivate` is the single source of truth for both surfaces, that application-layer SSRF checks are required on top of NetworkPolicy (defense in depth), and that any override must apply uniformly to preserve symmetry. Added a new `lenny-preflight` check in §17 (`spec/17_deployment-topology.md` line 472) that parses both rendered NetworkPolicies and fails the install with `SSRF private-range `except` list drift detected…` if the two `except` blocks are not set-equal — a guard against future edits that weaken only one surface. Chose the "shared Helm value + symmetric rendering + preflight drift check" approach over a minimal gateway-only patch because the finding explicitly recommended a shared value and rendering it "in every `0.0.0.0/0`-based rule across §13.2 and §25.4"; the drift check closes the class of defects where a future PR weakens the block list on one rule without noticing the other. Left the agent-pod `internet` egress profile untouched — its broader internet egress is intentional for pods that need package install / web access and is a distinct threat model from gateway- or `lenny-ops`-initiated SSRF.

### NET-058. `allow-gateway-egress-interceptor-{namespace}` lacks podSelector [Medium]
**Section:** 13.2
**Status:** Fixed

The rule allows gateway egress to every pod in the interceptor namespace. Interceptor namespaces may host other tenant-operated pods.

**Recommendation:** Require a `podSelector: { lenny.dev/component: interceptor }` on the destination.

**Resolution:** Added `podSelector: { matchLabels: { lenny.dev/component: interceptor } }` paired with `namespaceSelector` (AND semantics — both must match) in the rendered `allow-gateway-egress-interceptor-{namespace}` NetworkPolicy in §13.2 (`spec/13_security-model.md` lines 303-305). Updated surrounding prose to require interceptor Deployments to carry the `lenny.dev/component: interceptor` label and to extend the `lenny-preflight` Job to warn if no pod in a declared interceptor namespace carries that label. Cross-referenced the label requirement from the §4.8 interceptor `endpoint` row (`spec/04_system-components.md` line 992, NET-039 + NET-058) so deployers configuring interceptor endpoints see both the namespace-declaration and label requirements together.

### NET-059. OTLP egress permits plaintext gRPC (4317) with no TLS requirement [Medium]
**Section:** 13.2
**Status:** Fixed

Trace payloads carry session metadata and occasional error bodies; intra-cluster interception feasible.

**Recommendation:** Require TLS (4318/OTLP-HTTP or 4317 with TLS); document collector expectations.

**Resolution:** Added an "OTLP TLS requirement (NET-059)" note in §13.2 immediately after the existing OTLP egress policy, mandating TLS on the collector hop, introducing Helm value `observability.otlpTlsEnabled` (default `true` in production; `false` only in dev/`make run`), documenting collector certificate expectations (trust-bundle injection plus optional `observability.otlpCaBundle`, SAN must cover the endpoint hostname), and extending the `lenny-preflight` Job to fail on `http://` endpoints when TLS is enabled. Kept port 4317 with TLS-over-gRPC (matches OTel upstream design) while allowing deployers to switch to 4318/OTLP-HTTP via `observability.otlpPort`. Updated the §04 adapter-manifest example endpoint from `http://` to `https://` and expanded the `observability.otlpEndpoint` field description to reference the new TLS requirement. Existing port comment in the NetworkPolicy YAML now flags "TLS required, see note below".

### NET-060. Pod→gateway mTLS lacks symmetric SAN validation [Medium]
**Section:** 13.x
**Status:** Fixed

Pods validate gateway SAN; no documented rule for gateway validating pod identity SAN. Impersonation surface.

**Recommendation:** Specify mTLS peer identity validation on both sides with SPIFFE IDs as SANs.

**Resolution:** Added a "Pod ↔ Gateway mTLS peer validation (NET-060)" paragraph in §10.3 (`spec/10_gateway-internals.md` lines 248-252), immediately after the existing Token Service ↔ Gateway peer-validation treatment, spelling out symmetric SAN validation on both sides of the adapter→gateway gRPC link: (a) gateway validates pod SPIFFE URI `spiffe://<trust-domain>/agent/{pool}/{pod-name}` against the expected pool/pod with trust-domain anchoring from `global.spiffeTrustDomain` (rejects on handshake, logs `pod_identity_mismatch`); (b) pod adapter validates gateway DNS SAN `lenny-gateway.lenny-system.svc` via explicit `tls.Config.ServerName` so that any cluster-CA-signed certificate issued to Token Service, controller, or other `lenny-system` workloads is rejected at handshake; (c) handshake-time rejection before any gRPC frame; (d) neither side falls back to CA-only trust. This upgrades the existing "gateway validates the SPIFFE URI" line and the existing defense-in-depth "gateway validates the pod's SPIFFE certificate" line to an explicit bidirectional contract matching the clarity of the Token Service paragraph. No new Helm values, no new CRDs — the fix is a specification-level tightening of already-in-place TLS mechanics using the existing SPIFFE trust-domain and DNS-SAN anchors.

---

## 4. Performance (PRF)

### PRF-004. `minWarm: 1,050` baseline re-surfaces in two prose locations after iter2 PRF-003 table fix [Medium]
**Section:** 6.x, 17.8
**Status:** Fixed

Prose references the old figure (not updated when the capacity-tier table was corrected).

**Recommendation:** Update prose references to match the corrected figure.

**Resolution:** Updated two prose references in `17_deployment-topology.md` to cite the production baseline of `minWarm: 1,260` (the "Production `minWarm`" table row) instead of the raw-demand estimate of 1,050. §17.8.2 line 971 (delegation fan-out prose) and §17.8.3 Step 3 line 1183 (Tier 3 promotion checklist) both now point operators at the safety-factor-adjusted row. The table row labeled "Raw demand estimate" at line 928 still carries 1,050 as intended by PRF-003.

### PRF-005. Gateway PDB allows voluntary-disruption burst exceeding MinIO budget at Tier 3 [Medium]
**Section:** 17.1 (Kubernetes Resources — gateway row)
**Status:** Fixed

Even with iter2 FLR-002 tightening, a `ceil(replicas/2)` PDB at Tier 3 (20 replicas) still allows 10 simultaneous drains → 4000 concurrent checkpoint uploads against the 400-upload MinIO budget.

**Recommendation:** Cap the burst via `maxUnavailable: 1` + `maxSurge: 25%`; specify a hard ceiling of concurrent gateway evictions independent of replica count.

**Resolution:** Replaced the tiered PDB formula (`minAvailable: 2` at Tier 1/2 + `minAvailable: ceil(replicas/2)` at Tier 3) with a flat `maxUnavailable: 1` PDB at **every tier**, decoupling the voluntary-eviction ceiling from replica count. The rolling-update strategy `maxUnavailable: 1, maxSurge: 25%` is preserved unchanged. The rationale paragraph on the gateway row in §17.1 now makes the PDB-vs-rolling-update distinction explicit (rollouts vs. `kubectl drain`/autoscaler consolidation/preemption) and quantifies the Tier 3 failure modes that the flat cap eliminates (2,000 uploads under rollout default, 4,000 uploads under replica-proportional PDB, both reduced to 400 under `maxUnavailable: 1`). The same change simultaneously resolves the expressibility and tier-deadlock concerns flagged in FLR-006 and FLR-007 — the new value is a constant integer (valid PDB YAML, no formula) and does not deadlock at Tier 1's `minReplicas: 2`.

---

## 5. Protocols (PRT)

### PRT-008. Shared Adapter Types fix introduces two undefined types [High]
**Section:** 15.2
**Status:** Fixed

Iter2 PRT-005 introduced `CallerIdentity` and `PublishedMetadataRef` in the type catalog without schemas anywhere in the spec.

**Recommendation:** Add schema definitions in §15.2 with required fields, validation rules, and examples.

**Fix:** Added Go struct definitions for `CallerIdentity` (with nested `ActorClaim` for RFC 8693 `act` projection) and `PublishedMetadataRef` in the §15.2 Shared Adapter Types block. `CallerIdentity` fields derived from the Lenny JWT claim structure in §13.3 (`Sub`, `CallerType`, `Scope`, `Act *ActorClaim`); `ActorClaim` mirrors the RFC 8693 `act` claim (`Sub`, `TenantID`, `SessionID`, `DelegationDepth`). `PublishedMetadataRef` fields mirror §5.1 `publishedMetadata` YAML shape plus fetch contract (`Key`, `ContentType`, `Visibility`, `URI`, `ETag`) with explicit reference to the `GET /v1/runtimes/{name}/meta/{key}` (public) and `GET /internal/runtimes/{name}/meta/{key}` (tenant/internal) endpoints from §15.1. Also updated the `SessionMetadata.CallerIdentity` field comment from "Schema defined in §13" to "Derived from the Lenny JWT claim structure in §13".

### PRT-009. `OutboundCapabilitySet.SupportedEventKinds` type + comment contradict closed-enum claim [Medium]
**Section:** 15.2
**Status:** Fixed

Field is typed `string` but comment says "closed enum — MUST be one of the registry values". No type-level enum; clients lose compile-time check.

**Recommendation:** Retype to a generated enum (e.g., `EventKind`) or reflect the set in a constants file.

**Resolution:** Retyped `SupportedEventKinds` from `[]string` to `[]SessionEventKind` in §15.2 "Shared Adapter Types", reusing the existing closed-enum type (`type SessionEventKind string` with `SessionEventStateChange`, `SessionEventOutput`, `SessionEventElicitation`, `SessionEventToolUse`, `SessionEventError`, `SessionEventTerminated` constants) already defined immediately below in the same section. Updated the field-level comment to cite the enum constants rather than bare string literals. A2A's `SupportedEventKinds: ["state_change", "output", "error", "terminated"]` declaration in §21.1 remains valid — Go auto-converts untyped string literals in a `[]SessionEventKind` composite literal — and the surrounding prose already names `SessionEventKind` as the source enum.

### PRT-010. `AuthorizedRuntime` fields do not match `GET /v1/runtimes` response [Medium]
**Section:** 15.2 + 15.1
**Status:** Fixed

Type catalog lists fields A, B, C; response-schema example lists A, B, D.

**Recommendation:** Reconcile — make the type the normative schema.

**Fix:** Made `AuthorizedRuntime` (§15 Shared Adapter Types) the normative schema for the `GET /v1/runtimes` response. Updated the `GET /v1/runtimes` row in the §15.1 Discovery and introspection table (formerly line 573) to list the actual `AuthorizedRuntime` fields (`name`, `agentInterface`, `mcpEndpoint`, `adapterCapabilities`, `publishedMetadata`) and to direct readers to the type definition as the source of truth. Removed the stale `mcpCapabilities`, `capabilities`, and `labels` bare-field claims; per-runtime extras are now described as `publishedMetadata` entries consistent with §5.1's "generic metadata publication mechanism … replacing any named protocol-specific fields" design. Also reconciled the `type: mcp` discovery paragraph (formerly line 478) to cite `mcpEndpoint` on the `AuthorizedRuntime` schema and to describe the MCP tools preview as a `mcp-capabilities` `publishedMetadata` entry fetched via `GET /v1/runtimes/{name}/meta/{key}`, rather than a bare `mcpCapabilities.tools` field.

### PRT-011. MCP target-version currency note still stale [Low]
**Section:** 15.2

Iter2 claimed fix did not apply — note still cites 2025-03-26 without the "current-as-of" qualifier.

**Recommendation:** Update the version-currency note consistently across §15.2 and §15.4.

---

## 6. Developer Experience / Platform (DXP)

### DXP-006. §15.7 scaffolder description contradicts §24.18 `binary`/`minimal` no-SDK promise [Low]
**Section:** 15.7

The scaffolder description still says "uses the Runtime Author SDK" even when emitting the `binary` or `minimal` template, which iter2 DXP-002 promised to be SDK-free.

**Recommendation:** Qualify the scaffolder description with the template-dependent SDK usage.

### DXP-007. §26.1 "`local` profile installations" is undefined terminology [Low]
**Section:** 26.1

`local` is not an Operating Mode named anywhere in §17.4.

**Recommendation:** Replace with "Embedded Mode" or define `local` as an Install Profile and cross-reference.

### DXP-008. §17.4 Embedded Mode custom-runtime path omits tenant-access grant for non-`default` tenants [Low]
**Section:** 17.4

Iter2 DXP-001 added the Embedded Mode path but did not spell out `lenny-ctl tenant add-runtime-access`.

**Recommendation:** Add the grant step to the bullet list.

---

## 7. Operations (OPS)

### OPS-007. Embedded Mode has no Postgres major-version mismatch fail-safe [Low]
**Section:** 17.4

If `lenny up` bundled PG version mismatches persisted data from a previous install, silent failure modes.

**Recommendation:** Add explicit version-probe + refuse-to-start semantics to Embedded Mode boot path.

### OPS-008. §17.8.1 operational defaults omits `ops.drift.*` tunables [Low]
**Section:** 17.8.1

Tunables are referenced but not in the defaults table.

**Recommendation:** Add `ops.drift.scanIntervalSeconds`, `ops.drift.snapshotTTLSeconds`, `ops.drift.alertThreshold` rows.

### OPS-009. `issueRunbooks` lookup missing `DRIFT_SNAPSHOT_STALE` → `drift-snapshot-refresh` [Low]
**Section:** 25.x

Runbook catalog refers to it; issueRunbooks lookup table omits.

**Recommendation:** Add the row.

---

## 8. Tenancy (TNT)

### TNT-005. Playground `apiKey` mode references an undefined "standard API-key auth path" [High] — **Fixed**
**Section:** 27.2 + 10.2

§10.2 documents OIDC and per-tenant service-account JWTs; no "standard API-key auth path" exists anywhere in the spec.

**Recommendation:** Either (a) define the standard API-key path in §10.2 with tenant-identity source and RBAC binding, or (b) retarget `apiKey` mode to the tenant service-account JWT issuance flow.

**Resolution:** Applied option (b). `apiKey` mode is retargeted to accept a **standard gateway bearer token** (OIDC ID token or service-account bearer token — same credential the Client→Gateway and Automated-clients rows of the §10.2 boundary table already specify). The `/playground/*` handler runs the standard auth chain (signature, `iss`/`aud`/`exp`/`nbf`) and applies the same `tenant_id` claim extraction and `TENANT_CLAIM_MISSING` / `TENANT_NOT_FOUND` rejections that OIDC traffic uses — no silent fallback to `default`. No new auth primitive is introduced; "apiKey" becomes a UI label for "paste a bearer token", suitable for operator / headless workflows. Edits: §10.2 line 180 rewritten to spell out the bearer semantics; §27.2 table row for `playground.authMode` clarified; §27.3 bullets for `apiKey` mode (lines 51, 56) rewritten to point to §10.2 validation; §27.5 chat-stream bullet generalized to cover all three modes.

### TNT-006. Playground `dev` HMAC JWT `tenant_id` source unspecified [Medium]
**Section:** 27.2

The dev JWT binds a `tenant_id` claim but the spec never says where it comes from (env, CR, flag).

**Recommendation:** Require explicit tenant scope at `lenny up` time; reject if ambiguous.

**Status: Fixed.** §27.2 adds a new `playground.devTenantId` Helm value (default `default`, matching the built-in `default` tenant that Embedded Mode auto-provisions per §17.4). §27.3's `authMode=dev` bullet now specifies: the dev HMAC JWT's `tenant_id` claim is sourced from `playground.devTenantId`; the value must satisfy the `^[a-zA-Z0-9_-]{1,128}$` format constraint (§10.2) and must name a registered tenant at startup (gateway refuses to start with `LENNY_PLAYGROUND_DEV_TENANT_INVALID` otherwise); and when `auth.multiTenant=true` with multiple tenants seeded, leaving `playground.devTenantId` at the `default` value causes Helm-validate rejection with `LENNY_PLAYGROUND_DEV_TENANT_REQUIRED` — so dev-mode JWTs never silently bind to an ambiguous tenant.

### TNT-007. `tenant-admin` authorization ambiguous when `tenantId` is absent [Medium]
**Section:** 25.4 / 10.2

Operations-inventory rule allows `tenant-admin` on calls with no `tenantId` field (e.g., platform-scoped backup). Allows cross-tenant escalation.

**Recommendation:** Require `tenant-admin` + `tenantId` match, or elevate to `platform-admin` for tenant-less operations.

**Status: Fixed.** §25.4 Operations Inventory Authorization rule was rewritten to resolve the ambiguity: `tenant-admin` now sees an operation only if (a) `started_by` is themselves, OR (b) the operation carries a `tenantId` field AND it matches the caller's tenant. Platform-scoped operations (no `tenantId` — `platform_upgrade`, platform-level `restore`/`backup`, drift reconciliation) are visible only when `started_by` matches the caller; a tenant-admin never sees platform-scoped operations started by other principals. This mirrors the event-subscription semantics for platform-scoped events (Section 25.5, line 2612), closing the cross-tenant information-leak vector while preserving legitimate self-visibility.

---

## 9. Storage (STR)

### STR-007. `SessionEvent.SeqNum` has no client-facing resume path [High] — **Fixed**
**Section:** 15.2

Iter2 added `SeqNum` to the event schema; clients can observe it but cannot resume with it. No `resumeFromSeq` on MCP stream or REST `/events?since=`.

**Recommendation:** Add `resumeFromSeq` query param on `/sessions/{id}/events` and the MCP stream open-frame.

**Resolution (iter3):** `attach_session` gained an optional `resumeFromSeq: uint64` parameter (§15.2 table + new "Event-stream resume" paragraph); MCPAdapter surfaces `SeqNum` as the SSE `id:` line and honors `Last-Event-ID` equivalently. §10.4 now defines a per-session event replay buffer (`gateway.sessionEventReplayBufferDepth`, default 512, range 64–4096) with a `gap_detected` protocol-level frame when the requested seq has been evicted. §7.2 and the §15.2.1 contract-test matrix document the client-replay contract. `SessionEventKind` closed enum is unchanged — `gap_detected` is a stream-control frame, not a SessionEvent. No REST `/sessions/{id}/events` endpoint exists in the spec, so the fix is anchored on the MCP `attach_session` surface, which is the primary client-facing session stream.

### STR-008. No client-facing keepalive/heartbeat frames on MCP session stream [Medium] — **Fixed**
**Section:** 15.2 (the client-facing MCP stream is defined in §15.2, not §15.4 as originally filed — §15.4 is the internal Runtime Adapter Specification)

Idle disconnects by intermediaries (Cloudflare, ELB) will terminate long-idle streams with no renegotiation contract.

**Recommendation:** Specify a server-side `keepalive` event every 20 s idle; document client reconnect semantics.

**Resolution (iter3):** Added a "Stream keepalive" paragraph to §15.2 alongside the existing "Event-stream resume" paragraph. The `MCPAdapter` now writes an SSE comment line `:keepalive\n\n` on the Streamable HTTP response whenever no `SessionEvent` frame has been written for 20 seconds. The interval is protocol-fixed. Comment lines are invisible to conforming SSE parsers, carry no `id:`, and do not perturb `SeqNum` / `Last-Event-ID` / `gap_detected` tracking. Clients SHOULD treat 60 s of silence (event or keepalive) as a broken connection and reattach via `attach_session` with `resumeFromSeq` set to the last observed `SeqNum` (STR-007), or rely on SSE built-in `Last-Event-ID` reconnect. The replay buffer and `gap_detected` contract from STR-007 cover any events emitted during the disconnect window.

### STR-009. Adapter buffered-drop head-eviction loses events silently from subscriber's view [Medium] — **Fixed**
**Section:** 15.4 adapter buffering

On buffer overflow, head-eviction drops oldest in-flight events — but the subscriber never sees a `gap` marker.

**Recommendation:** Emit a synthetic `event_dropped` sentinel with the dropped range; requires `SeqNum` (STR-007).

**Resolution:** The buffered-drop policy prose in §15 now requires the `OutboundChannel` to emit a `gap_detected` protocol-level frame (reusing the `{lastSeenSeq, nextSeq}` shape introduced by STR-007 in §10.4) to the subscriber before the next successfully-delivered event after an eviction, collapsing consecutive drops into a single frame. The frame rides the same out-of-band channel the adapter uses for replay-buffer gaps and is explicitly not a `SessionEvent` (no `SeqNum`, not in the `SessionEventKind` enum).

### STR-010. Stream-drain preStop stage-3 interaction with slow-subscriber policy unspecified [Low]
**Section:** 10.8

If a subscriber is slow, stage-3 drain can block indefinitely within the grace period.

**Recommendation:** Specify subscriber timeout during drain; default 5 s per subscriber.

---

## 10. Delegation (DEL)

### DEL-008. Orphan tenant-cap fallback emits no audit event or parent signal [Low]
**Section:** 8.x

Orphan-capacity fallback silently repoints lineage without telling the parent.

**Recommendation:** Emit `delegation.orphan_fallback` audit + push a `delegation.orphaned` event to the parent.

### DEL-009. `DELEGATION_PARENT_REVOKED` / `DELEGATION_AUDIT_CONTENTION` not catalogued in §15.1 [High]
**Status:** Fixed — Added both rows to §15.1 error catalog after `DELEGATION_CYCLE_DETECTED`. `DELEGATION_PARENT_REVOKED` = `PERMANENT`/409/non-retryable with `details.parentSessionId` and `details.revocationReason`. `DELEGATION_AUDIT_CONTENTION` = `TRANSIENT`/503/retryable with `Retry-After`, `details.tenantId` and `details.retryAfterSeconds`, plus explicit instruction that clients must retry the entire `lenny/delegate_task` call.

**Section:** 8 + 15.1

Iter2 DEL-007 fix referenced both codes but never added rows to §15.1.

**Recommendation:** Add both to §15.1 with status codes and retry-after semantics.

### DEL-010. `TREE_VISIBILITY_INSUFFICIENT_FOR_MESSAGING_SCOPE` not catalogued [Medium]
**Status:** Fixed — Added a `TREE_VISIBILITY_INSUFFICIENT_FOR_MESSAGING_SCOPE` row to the §15.1 error catalog, placed immediately after `SCOPE_DENIED`. Classified `POLICY`/403/not-retryable-as-is, with `details.messagingScope`, `details.treeVisibility`, `details.requiredTreeVisibility` (`"full"`), and an explicit scope-upgrade hint (upgrade `treeVisibility` to `full` or narrow `messagingScope`). Cross-references §8.5 (Delegation Tools) and §7.2 (sibling coordination).

**Section:** 15.1

Same class of gap as DEL-009 for cross-tree messaging.

**Recommendation:** Add to §15.1 with 403 status and scope-upgrade hint.

### DEL-011. `tracingContext` validation error codes not catalogued [Low]
**Section:** 15.1

Context-propagation spec mentions validation errors; no codes listed.

**Recommendation:** Add `INVALID_TRACING_CONTEXT`, `TRACING_CONTEXT_DEPTH_EXCEEDED`.

---

## 11. Session (SES)

### SES-009. Derive pre-running `failed` persistence still contradicts §7.1 atomicity [Medium]
**Section:** 7.1

Two-phase derive leaves `failed` row visible before the swap is abandoned; breaks atomicity.

**Recommendation:** Move the failure-row write into the same transaction; guard with coordinator_generation.

**Status:** Fixed. Rewrote the partial-copy paragraph in §7.1 (rule 2) to specify atomic row persistence: the derived session row is never persisted before the copy outcome is known, eliminating the two-phase "create-then-mark-failed" path. On copy failure the default (`gateway.persistDeriveFailureRows: false`) is option (a) — write nothing and return an error with no `session_id`, matching §7.1 atomicity line 28. Deployers that opt into derive-failure auditing get option (b) — a terminal `failed` row INSERTed in a single transaction guarded by a `coordination_generation` CAS fence (cross-references §10.1) so a stale coordinator cannot leave an orphan `failed` row visible after a handoff. Field name canonicalized to `coordination_generation` (the spec-wide form in §10.1/§4.4/§12).

### SES-010. `resuming → cancelled` / `resuming → completed` still undefined [Medium]
**Section:** 7.2

State-machine edges missing for operator-initiated cancel during resume.

**Recommendation:** Add both; specify the snapshot-close semantics.

**Status:** Fixed. Added both `resuming → cancelled` and `resuming → completed` rows to the §7.2 state-machine table, and inserted a "Mid-resume terminal transitions — snapshot-close semantics" paragraph that specifies: (1) abort in-flight restoration RPCs and stop the 300s resuming watchdog, (2) skip the live seal and use the latest checkpoint snapshot as `final_workspace_ref` with `workspaceSnapshotSource: "checkpoint"`, (3) release the half-claimed replacement pod via the standard pool-release path, (4) run standard terminal handling (credential release, DLQ drain, cascade, event emission). Also documented the trigger for each edge (client/parent/operator cancel vs. replayed event log showing prior `session_complete`) and the API-reported collapse to `resume_pending → {cancelled|completed}`.

### SES-011. `starting → resume_pending` not reflected in §15.1 preconditions [Low]
**Section:** 15.1

Endpoint preconditions don't enumerate this edge.

**Recommendation:** Add to preconditions with error code `SESSION_STARTING`.

### SES-012. SSE reconnect storm still unmitigated [Low]
**Section:** 15.4

No jitter/backoff contract on the client side; iter2 SES-008 not addressed.

**Recommendation:** Document exponential backoff 1s/2s/4s/8s, max 30s; require clients to honour.

### SES-013. `resuming` failure-transition inconsistency between §7.2 and §6.2 [Medium] — **Fixed**
**Section:** 7.2 + 6.2

§7.2 says resume failure returns to `checkpointed`; §6.2 says `failed`.

**Recommendation:** Reconcile; checkpoint-survivable resume failures return to `checkpointed`; others → `failed`.

**Resolution:** The actual inconsistency (summary paraphrase aside) was §7.2 line 179 stating `resuming → failed (re-attach fails after retries exhausted)` while §6.2 lines 116–120 and 223–229, plus §7.3 lines 383 and 395, all direct retry-exhaustion (and the 300s watchdog) from `resuming` to `awaiting_client_action`. Replaced the §7.2 edge with `resuming → awaiting_client_action (re-attach fails after retries exhausted, resuming watchdog fires, or non-retryable error — see §6.2 "`resuming` failure transitions")`, adopting the finding's Option 1. No `checkpointed` state exists in the spec, so the summary's paraphrase was reconciled to the underlying `awaiting_client_action` target. §6.2 retains the single-rule authoritative table; §7.2 now cross-references it.

### SES-014. `awaiting_client_action` expiry trigger under-specified [Low]
**Section:** 7.2

No timeout value or audit emission specified.

**Recommendation:** Default `awaiting_client_action_timeout_seconds: 300`; emit `session.awaiting_client_action_expired`.

---

## 12. Observability (OBS)

### OBS-012. Alerts reference still-unregistered metrics [High] — Fixed
**Section:** 16.5

Alerts `SiemDeliveryLag`, `CredentialCompromised`, `ControllerWorkQueueDepth` reference metrics not in §16.1 registry.

**Recommendation:** Add metric rows or remove the alerts.

**Resolution:** Registered three new metrics in §16.1 and updated §16.5 alert conditions and cross-references to cite them:
- `lenny_controller_workqueue_depth` (gauge labeled by `controller`, `queue`) — added at §16.1 line 108; `ControllerWorkQueueDepthHigh` alert (§16.5 line 418) now cites the canonical name; §4.6.1 narrative (04_system-components.md:433) updated to the same name.
- `lenny_audit_siem_delivery_lag_seconds` (gauge) — added at §16.1 line 179; `AuditSIEMDeliveryLag` alert (§16.5 line 423) and §12.3 outbox-forwarder narrative (12_storage-architecture.md:97) updated to cite the prefixed name.
- `lenny_credential_revoked_with_active_leases` (gauge labeled by `pool`, `provider` — `credential_id` avoided per §16.1.1 high-cardinality rule) — added at §16.1 line 46; `CredentialCompromised` alert (§16.5 line 344) rewritten as an evaluable PromQL expression (`max by (pool, provider) (lenny_credential_revoked_with_active_leases) > 0` for 30s).

### OBS-013. Last unnamed metric row in §16.1 [Medium]
**Section:** 16.1

One row in the registry table has no metric name.

**Recommendation:** Name the metric or drop the row.

**Status:** Fixed

**Resolution:** Renamed line 38's unnamed `mTLS handshake latency (gateway-to-pod)` row to the canonical `lenny_mtls_handshake_duration_seconds` (histogram labeled by `direction`: `gateway_to_pod`, `pod_to_gateway`) with a cross-reference to [§13.2](13_security-model.md#132-network-isolation) where the gateway↔pod NetworkPolicies defining both handshake directions live. The `direction` label is inline-documented per the §16.1.1 "other domain labels" convention and is consistent with existing uses on `lenny_gateway_llm_translation_duration_seconds` (line 82) and `lenny_network_policy_cidr_drift_total` (line 205).

### OBS-014. `level` label value set still not enumerated [Medium]
**Section:** 16.1.1

Multiple metrics carry a `level` label; enumeration missing.

**Recommendation:** Add an enumeration row (`basic|standard|full`).

**Status:** Fixed — Added an inline enumeration sentence to §16.1.1's "Other domain labels" paragraph specifying that the `level` label always carries one of `basic`, `standard`, or `full` (the runtime integration levels from §15.4.3), and listing the checkpoint metrics in §16.1 that use it (`lenny_checkpoint_duration_seconds`, `lenny_checkpoint_size_bytes`, `lenny_checkpoint_size_exceeded_total`, `lenny_checkpoint_storage_failure_total`, `lenny_checkpoint_stale_sessions`).

### OBS-015. `GatewaySubsystemCircuitOpen` templated-name vs label mismatch persists [Medium]
**Section:** 16.5

Alert name templates `{{ $labels.subsystem }}` into the alert name, but the metric's label set doesn't contain `subsystem`.

**Recommendation:** Rename alert or add the label.

**Status:** Fixed — Unified the per-subsystem circuit breaker metric into a single `lenny_gateway_subsystem_circuit_state` gauge carrying a `subsystem` label ∈ {`stream_proxy`, `upload_handler`, `mcp_fabric`, `llm_proxy`} (§16.1 row at line 79). Rewrote the `GatewaySubsystemCircuitOpen` alert condition (§16.5 line 387) as the PromQL-valid `max by (subsystem) (lenny_gateway_subsystem_circuit_state) == 2` sustained for > 60s. Added `subsystem` to the §16.1.1 "other domain labels" enumeration. Propagated the labeled form to the three cross-references in §4.9 (lines 1438, 1440, 1446) and §18 (line 44) that previously used the concrete `lenny_gateway_llm_proxy_circuit_state` name. The three remaining templated metrics on lines 76–78 (`request_duration_seconds`, `errors_total`, `queue_depth`) are left as-is because no alert references them and no regression condition depends on a labeled form there.

### OBS-016. Alert-name drift between §16.5 and §25.13 [High]
**Section:** 16.5 + 25.13

`PostgresReplicationLagHigh` in §25.13 vs `PostgresReplicationLag` in §16.5. Operators see mismatched names between runbook catalog and alert catalog.

**Recommendation:** Pick one canonical name across both.

**Status:** Fixed — The actual drift was between §16.1 (metric row cross-reference to `PostgresReplicationLagHigh` alert) and §16.5 (alert defined as `PostgresReplicationLag`); the §25.13 Tier-Aware Defaults table never lists this alert, so the finding's §25.13 reference was a mis-locator for the §16.1 cross-reference. Adopted `PostgresReplicationLagHigh` as canonical (matches the `...High` suffix convention used for `>`-threshold alerts in §16.5 — see `RedisMemoryHigh`, `CheckpointStorageHigh`, `Tier3GCPressureHigh`, `CheckpointDurationHigh`, `StorageQuotaHigh`, `BillingStreamEntryAgeHigh`, `GatewayActiveStreamsHigh`). Changed: §16.5 alert row at line 338 renamed `PostgresReplicationLag` → `PostgresReplicationLagHigh`; §17.7 runbook trigger at line 710 updated to match. §16.1 metric row (line 190) already used the canonical name and is unchanged. This also incidentally resolves OBS-017.

### OBS-017. `PostgresReplicationLagHigh` metric row refers to non-existent alert [Low]
**Section:** 16.1

Cross-reference breaks.

**Recommendation:** Fix cross-reference after OBS-016 resolution.

**Status:** Already Fixed — Resolved as a side-effect of OBS-016, which canonicalized the alert name to `PostgresReplicationLagHigh` in §16.5 (line 338) and §17.7 (line 710). The §16.1 metric row cross-reference at line 190 now resolves correctly to the renamed §16.5 alert. No additional change required.

### OBS-018. `deployment_tier` label used in alerts but not in §16.1.1 [Medium]
**Section:** 16.1.1

Label referenced in multiple alert expressions; not listed in label-convention table.

**Recommendation:** Add the label with tier-1/2/3 value set.

### OBS-019. `lenny_gateway_request_queue_depth` referenced but not registered [Medium]
**Section:** 16.1 + 10.x

Metric used in prose; no §16.1 row.

**Recommendation:** Add the metric with description, labels, source.

### OBS-020. `lenny_controller_leader_lease_renewal_age_seconds` label name unspecified [Low]
**Section:** 16.1

Labels column empty.

**Recommendation:** Specify `controller` label.

### OBS-021. `lenny_warmpool_pod_startup_duration_seconds` uses non-canonical label description [Low]
**Section:** 16.1

Label description deviates from template.

**Recommendation:** Use the canonical "capacity tier of the source pool" description.

---

## 13. Compliance (CMP)

### CMP-046. Post-restore erasure reconciler can destroy legally-held data [High]
**Section:** 19.4 + 20.4

Reconciler replays the erasure journal from before the restore point — but legal-hold overlays applied *after* backup time are missing, so data released from hold post-backup may be erased mistakenly, and data placed under hold post-backup may be erased despite the hold.

**Recommendation:** Reconcile against the current legal-hold ledger in addition to the erasure journal; block erasure replays if ledger state is older than restore point.

### CMP-047. OCSF dead-letter rows retain raw canonical PII past user erasure [Medium]
**Section:** 19.4

Dead-letter storage for un-ingestable OCSF events persists for 14 days; user erasure cascades don't touch it.

**Recommendation:** Include dead-letter table in erasure cascade; emit `erasure.deadletter_redacted` receipt.

### CMP-048. ArtifactStore (MinIO) not covered by backup pipeline [Medium]
**Section:** 20.4

Backup encompasses Postgres + Redis AOF + credential vault snapshot; MinIO is excluded. Restore-time GDPR + residency claims are incomplete if workspace artifacts are absent.

**Recommendation:** Add MinIO snapshot to backup pipeline (native backup or bucket replication); cross-document in §20.4.

---

## 14. External API (API)

### API-005. Duplicate `ISOLATION_MONOTONICITY_VIOLATED` catalog rows [High]
**Section:** 15.1 (lines 771 + 800)

Same code, two rows, different statuses (403 vs 422). See SEC-003 for security framing.

**Recommendation:** Collapse to one canonical status; document in one row.

**Status:** Fixed via SEC-003. Duplicate row removed; single unified catalog entry now covers delegation, derive, and replay paths with canonical HTTP 422. See SEC-003 for edit details.

### API-006. Duplicate-key ordering in §15.1 catalog has no invariant [Low]
**Section:** 15.1

Table order is arbitrary; readers can't predict where to add new codes.

**Recommendation:** Sort alphabetically or by HTTP status; document the invariant at the top of the table.

---

## 15. Credentials (CRD)

### CRD-006. New `credential_rotation_inflight_ceiling_hit_total` metric missing `lenny_` prefix [Medium]
**Section:** 16.1

Regression from iter2 CRD-005 fix — metric added without canonical prefix.

**Recommendation:** Rename to `lenny_credential_rotation_inflight_ceiling_hit_total`.

### CRD-007. `rotationTrigger: scheduled_renewal` contradicts canonical `proactive_renewal` [Medium]
**Section:** 4.9

New value introduced in one place; canonical enum uses `proactive_renewal` elsewhere.

**Recommendation:** Replace `scheduled_renewal` with `proactive_renewal`.

### CRD-008. `rotationTrigger: fault_driven_rate_limited` not defined in §4.9 fallback flow enum [Low]
**Section:** 4.9

Missing enum row.

**Recommendation:** Add to the Fallback Flow rotation-trigger enumeration.

### CRD-009. Admin-time RBAC live-probe covers only add-credential path [Medium]
**Section:** 4.9

Pool creation and update paths still retain the original CRD-004 gap — operator can create a pool whose credential reference lacks RBAC.

**Recommendation:** Extend live-probe to create/update; surface `CREDENTIAL_SECRET_RBAC_MISSING`.

### CRD-010. 300 s revocation-triggered rotation ceiling emits metric but no audit event [Low]
**Section:** 4.9

Silently shaping emissions.

**Recommendation:** Emit `credential.rotation_rate_limited` audit event when the ceiling is hit.

### CRD-011. Admin API + CLI reference do not document `CREDENTIAL_SECRET_RBAC_MISSING` error [Low]
**Section:** 15.1 + 24.x

Error surfaces at runtime; operators have no documented recovery path.

**Recommendation:** Document in §15.1 and `lenny-ctl credentials` CLI reference.

---

## 16. Containerization (CNT)

### CNT-005. `gitClone` auth scope hardcoded to GitHub while URL accepts any host [Medium — real error]
**Section:** 14.x

`auth` scope mentions only `github.com` but URL validator accepts any git host (GitLab, Bitbucket, self-hosted).

**Recommendation:** Generalise to host-agnostic; enumerate supported auth providers.

### CNT-006. `uploadArchive` format list mismatch between §14 and §7.4 [Medium — real error]
**Section:** 14 + 7.4

§14 lists `{tar.gz, zip}`; §7.4 lists `{tar.gz, tar.zst, zip}`.

**Recommendation:** Reconcile; add `tar.zst` to both or remove from §7.4.

### CNT-007. `gitClone.auth` paragraph does not bind credential-pool identity to session [Minor — clarity]
**Section:** 14.x

Spec references pool identity but does not formalise the binding during clone.

**Recommendation:** Add one sentence binding the lease to the session for audit traceability.

---

## 17. Capacity / Pool System (CPS)

### CPS-003. `partial_recovery_threshold_bytes` configuration surface undefined [Low]
**Section:** 10.x

Referenced; no configuration row.

**Recommendation:** Add to §17.8.1 defaults.

### CPS-004. Partial-manifest reassembly semantics ambiguous (multipart vs separate objects) [Medium]
**Section:** 10.x

Spec says "partial manifests" but doesn't say whether MinIO stores one multipart object or separate objects.

**Recommendation:** Specify separate objects with indexed names (`partial-{n}.tar.gz`) and a manifest pointing to them.

### CPS-005. Partial-manifest truncation lies on arbitrary byte offset, not tar-member boundary [Medium]
**Section:** 10.x

Recovery produces corrupt tars.

**Recommendation:** Align truncation to the last completed tar-member offset; record offset in the manifest.

### CPS-006. Partial-manifest resume path lacks coordinator_generation guard against split-brain [Low]
**Section:** 10.x

Two coordinators could both resume the same partial manifest.

**Recommendation:** Add generation-fence on the manifest claim.

### CPS-007. CheckpointBarrier fan-out during drain does not address recently-handed-off sessions [Low]
**Section:** 10.8

Sessions handed off < 5 s before drain may be missed.

**Recommendation:** Extend barrier scope to cover the handoff-window set.

---

## 18. Runtime Protocols (PRT)

See perspective 5.

---

## 19. Build Sequence (BLD)

### BLD-005. Phase 3.5 admission-policy list incomplete against §17.2 12-item enumeration [High]
**Section:** 18.4

Phase 3.5 still names only 3 of 8 webhooks + 0 of 4 policy manifests.

**Recommendation:** Update Phase 3.5 deliverables to enumerate all 12 items from §17.2; cross-reference.

### BLD-006. Phase 4.5 bootstrap seed missing `global.noEnvironmentPolicy` after iter2 TNT fix [High]
**Section:** 18.4

Iter2 TNT-002 fix added the Helm value; build-sequence's bootstrap inventory did not update.

**Recommendation:** Add the value to Phase 4.5's required Helm values list.

### BLD-007. Phase 5.8 LLM Proxy deliverables reference `lenny-direct-mode-isolation` as existing, but Phase 3.5 hasn't deployed it [Medium]
**Section:** 18.4

Phase sequencing: 3.5 must deploy the webhook before 5.8 relies on it.

**Recommendation:** Move `lenny-direct-mode-isolation` into Phase 3.5 or split into 3.5a/3.5b.

### BLD-008. Phase 13 compliance work does not enumerate `lenny-data-residency-validator` / `lenny-t4-node-isolation` deployments [Medium]
**Section:** 18.4

These webhooks are §13-new and need a deployment phase.

**Recommendation:** Add both to Phase 13 deliverables.

### BLD-009. Phase 8 checkpoint/resume work does not enumerate `lenny-drain-readiness` deployment [Low]
**Section:** 18.4

Webhook is new; needs phase coverage.

**Recommendation:** Add to Phase 8.

### BLD-010. Phase 1 wire-contract artifacts do not cover Shared Adapter Types / SessionEvent Kind Registry [Low]
**Section:** 18.4

Iter2 PRT-005 added these contracts; Phase 1 artifact list pre-dates them.

**Recommendation:** Add both to Phase 1 deliverables (OpenAPI + protobuf + JSON schema).

---

## 20. Failure / Resilience (FLR)

### FLR-006. Gateway PDB `minAvailable: 2` at Tier 1 blocks node drains and rolling updates [High]
**Section:** 17.5

Iter2 FLR-002 fix set `minAvailable: 2` + Tier 1 `minReplicas: 2` → node drain blocks indefinitely (no disruption budget).

**Recommendation:** Use `maxUnavailable: 1` for Tier 1 instead, or raise Tier 1 `minReplicas` to 3.

### FLR-007. Gateway PDB formula `ceil(replicas/2)` not expressible as PDB YAML [Medium]
**Section:** 17.5

PodDisruptionBudget allows integer or percentage; dynamic formulas aren't native.

**Recommendation:** Express as `minAvailable: 50%` (K8s rounds up) and document the semantics.

### FLR-008. preStop cache population gap on coordinator handoff [Medium]
**Section:** 10.8

Iter2 FLR-004 fix says "use cached value if Postgres unreachable" — but handoff to a new coordinator means an empty cache.

**Recommendation:** Prime cache on coordinator assume-leader; document cold-start behaviour.

### FLR-009. `InboxDrainFailure` alert rule text not evaluable PromQL [Low]
**Section:** 16.5

Text is English prose; no `expr:`.

**Recommendation:** Replace with `increase(lenny_inbox_drain_failure_total[5m]) > 0`.

### FLR-010. PgBouncer readiness probe — FLR-005 still unresolved [Low]
**Section:** 12.x

No change to `failureThreshold`.

**Recommendation:** Widen to `failureThreshold: 8` or decouple readiness.

### FLR-011. No regression on billing MAXLEN (FLR-001) [Partial — note only]
**Section:** N/A

FLR-001 resolved in iter2; no new regression. No action required.

---

## 21. Experimentation (EXP)

### EXP-005. Results API filters reference undocumented error `INVALID_QUERY_PARAMS` [Low]
**Section:** 15.x

Not in §15.1.

**Recommendation:** Add to catalog or replace with canonical code.

### EXP-006. Results API response shape undefined for `breakdown_by` requests [Medium]
**Section:** 15.x

Query parameter accepted; response shape unspecified.

**Recommendation:** Define a `BreakdownResponse` schema with typed aggregations.

### EXP-007. Variant count still unbounded [Low]
**Section:** 22.x

Iter2 EXP-003 unresolved.

**Recommendation:** Cap at 16; return `TOO_MANY_VARIANTS` past ceiling.

### EXP-008. Sticky cache wording still internally contradictory [Low]
**Section:** 22.x

Iter2 EXP-004 unresolved.

**Recommendation:** Resolve to a single TTL/refresh semantic.

### EXP-009. Isolation-mismatch fallthrough silently contaminates control bucket [Medium]
**Section:** 22.x

When pod isolation profile doesn't match variant's target, request silently falls through to control.

**Recommendation:** Fail closed with `VARIANT_ISOLATION_UNAVAILABLE` instead of silent fallthrough.

---

## 22. Documentation (DOC)

### DOC-008. Intra-file anchor `#73-retry-and-resume` does not resolve in `06_warm-pod-model.md` [Medium]
**Section:** 06

Anchor broken.

**Recommendation:** Fix heading or anchor reference.

### DOC-009. Cross-file anchor `16_observability.md#167-audit-event-catalogue` does not exist [Medium]
**Section:** 16

No such heading.

**Recommendation:** Rename §16.7 to include "Audit Event Catalogue" or fix the reference to the actual heading.

### DOC-010. Two broken `06/12` anchors in admission-policies enumeration [Medium]
**Section:** 13

Links to `#6-warm-pod-model` / `#12-data` fail.

**Recommendation:** Fix anchors to resolve.

### DOC-011. Headings "16.7 Section 25 Audit Events" / "16.8 Section 25 Metrics" still confusing [Low]
**Section:** 16

Heading names confuse cross-reference readers.

**Recommendation:** Rename to "16.7 Audit-Event Catalogue" / "16.8 Metric Catalogue".

### DOC-012. README TOC omits first subsection of three sections [Low]
**Section:** README

TOC miss.

**Recommendation:** Regenerate TOC.

---

## 23. Messaging (MSG)

### MSG-007. Durable-inbox Redis command set contradicted between §7.2 and §12.4 [High]
**Section:** 7.2 + 12.4

§7.2 uses `RPUSH` (writer) + `LPOP` (reader) = FIFO. §12.4 uses `LPUSH` + `LPOP` = LIFO.

**Recommendation:** Use FIFO everywhere; `LPUSH` / `RPOP` or `RPUSH` / `LPOP`. Pick one set and propagate.

### MSG-008. `message_expired` reason codes diverge across three drain paths [Medium]
**Section:** 7.2

Three prose passages use subtly different reason strings.

**Recommendation:** Standardise on `message_expired` everywhere.

### MSG-009. Metrics referenced by inbox prose not declared in §16.1 [Medium]
**Section:** 16.1

Several `lenny_inbox_*` metrics only exist in prose.

**Recommendation:** Add to §16.1 registry.

### MSG-010. Pre-receipt rejections enumerated only in §15 catalog, not reconciled in §15.4.1 [Low]
**Section:** 15.4.1

"Every call returns a receipt" is contradicted by pre-receipt rejection codes.

**Recommendation:** Amend the clause: "except where rejected by syntactic pre-checks".

---

## 24. Policy / Admission (POL)

### POL-015. POL-014 fix introduces contradictory chain-ordering statements [Medium]
**Section:** 13.x + 21.x

AdmissionController says "AuthEvaluator runs first"; AuthEvaluator docs say "AdmissionController runs first".

**Recommendation:** Canonicalize: AdmissionController → AuthEvaluator → ConnectorEvaluator; update both prose blocks.

### POL-016. Broken anchor in POL-014 audit-catalog cross-reference [Low]
**Section:** 13

Link to `#25-audit-events` fails.

**Recommendation:** Fix anchor.

### POL-017. `admission.circuit_breaker_rejected` lacks audit-sampling discipline [Medium]
**Section:** 13.x

Storm scenario can flood the audit pipeline.

**Recommendation:** Sample 1:100 for this event during storm; suppress individually with aggregated `circuit_breaker_storm` summary event every 60 s.

---

## 25. Extensions / Session state machine (EXM)

### EXM-007. `warn_within_budget` introduced but never defined [Medium]
**Section:** 7.x

Used as a state; no semantic.

**Recommendation:** Define or remove.

### EXM-008. `node not draining` precondition introduced but undefined [Medium]
**Section:** 7.x

No formal definition.

**Recommendation:** Tie to node annotation `node.kubernetes.io/unschedulable == false`.

### EXM-009. `cancelled → task_cleanup` transition skips retirement-check semantics [Low]
**Section:** 7.x

Bypasses retirement checks.

**Recommendation:** Add retirement check as a guard.

### EXM-010. Retirement-policy config-change staleness re-surfaces after EXM-006 partial fix [Low]
**Section:** 7.x

Config changes not picked up until next cycle.

**Recommendation:** Subscribe to config-change events.

---

## 26. Web playground (WPP)

### WPP-008. `apiKey` mode invokes "standard API-key auth path" §10.2 does not document [Medium] — **Fixed via TNT-005**
**Section:** 27.2 + 10.2

Same root cause as TNT-005.

**Recommendation:** Define the standard API-key path or retarget to tenant JWT.

**Resolution:** Resolved by the TNT-005 fix (retarget option). `apiKey` mode is now documented as a bearer-token paste flow validated by the existing standard gateway auth chain (§10.2) — no new "API-key auth path" is introduced. See TNT-005 resolution note for the list of edited sections.

### WPP-009. `playground.maxIdleTimeSeconds` bound not validatable at Helm install time [Low]
**Section:** 27.2

Bound `60 ≤ v ≤ runtime's maxIdleTimeSeconds` depends on runtime admission, which happens later.

**Recommendation:** Validate at runtime-registration time; reject config values > any registered runtime's max.

---

## Cross-Cutting Themes

1. **Catalog hygiene degraded across iter2 fixes.** Four high-severity findings (API-005, SEC-003, DEL-009, OBS-012) trace to iter2 fixes that added prose without touching the canonical error-code / metric / audit-event catalogs. Catalog-first edits would have prevented these regressions.

2. **NetworkPolicy coverage for lenny-ops is structurally incomplete.** One Critical (NET-051) plus five High findings (NET-052..056) all surface the same root cause: `lenny-ops` — a post-iter1 addition — was never propagated into §13.2's allow-list enumeration. This also echoes into the build sequence (BLD-005/008) and K8S-038/039 webhook alert coverage.

3. **Iter2 fixes introduced new unsatisfiable constraints.** SEC-005 (0400 file cross-UID ownership vs dropped CAP_CHOWN), FLR-006 (PDB `minAvailable: 2` vs `minReplicas: 2`), and PRT-008 (undefined type names) all describe iter2 fixes whose side effects weren't exercised.

4. **State-machine edges are still partial.** SES-009..014 cover six distinct edges that the §7 state-machine diagram either omits or specifies inconsistently with §15.1 preconditions. SES-013's `resuming` terminal-state disagreement between §7.2 and §6.2 is the most concrete.

5. **Observability catalog has structural holes.** 10 OBS findings (OBS-012..021) plus 4 MSG findings reveal alerts referencing unregistered metrics, label-set drift, and unnamed rows. §16.1 / §16.5 / §25.13 lack a single-source-of-truth regime.

6. **Client-visible resume is unfinished.** STR-007 (`SeqNum` without `resumeFromSeq`), STR-008 (no keepalives), STR-009 (silent event-drop on overflow), STR-010 (drain-time slow-subscriber policy) together form a coherent protocol gap: the server exposes ordinals but clients have no way to use them.

7. **Playground authentication is underspecified.** TNT-005, TNT-006, WPP-008 converge on the playground's auth story: `apiKey` mode, `dev` mode HMAC, and "standard API-key auth path" all point to §10.2 content that does not exist.

---

*End of Iteration 3 summary. 107 findings catalogued; iter3 is the final iteration per the review-and-fix skill's 3-iteration default.*
