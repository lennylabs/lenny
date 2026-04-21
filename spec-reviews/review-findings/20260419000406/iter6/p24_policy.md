# Iter6 Review — Perspective 24: Policy Engine & Admission Control

**Scope:** `spec/11_policy-and-controls.md` §11.6 (Circuit Breakers) with cross-file touchpoints in `spec/16_observability.md` §16.1/§16.5/§16.7, `spec/12_storage-architecture.md` §12.4 (`cb:{name}` key schema), `spec/04_system-components.md` §4.8 (pre-chain gate callout), `spec/25_agent-operability.md` §25.3 (Event Types table), `spec/15_external-api-surface.md` §15.4 (error catalog), and docs alignment (`docs/api/admin.md`, `docs/operator-guide/*`, `docs/reference/*`, `docs/runbooks/circuit-breaker-open.md`).

**Iter5 carry-forward verification.**

- **POL-023** (High, admin API body schema did not carry scope; policy-evaluation correctness hole) — **Fixed.**
  - `spec/11_policy-and-controls.md:312` now documents the body as `{ reason, limit_tier, scope }` with the tier-specific matcher table at lines 285-290; `INVALID_BREAKER_SCOPE` (HTTP 422) enumerated for missing tier/scope, cross-tier mismatch, out-of-enum `operation_type` value, and persisted-scope mismatch (scope immutable; close-and-reopen under a distinct `{name}` to change).
  - `cb:{name}` Redis value at `spec/11_policy-and-controls.md:283` and `spec/12_storage-architecture.md:192` extended to `{state, reason, opened_at, opened_by_sub, opened_by_tenant_id, limit_tier, scope}`, with explicit cross-reference that the `limit_tier`/`scope` pair is the authoritative input to admission-time matching and the `{name}` string is an operator-facing label only.
  - "AdmissionController evaluation" paragraph at `spec/11_policy-and-controls.md:315` now states "For each open breaker, the gateway matches the request against the breaker's persisted `limit_tier`/`scope` pair (from `cb:{name}`, above) — **not** the breaker's `{name}` string" and enumerates the per-tier match rule.
  - `spec/15_external-api-surface.md:1026` lists `INVALID_BREAKER_SCOPE` (PERMANENT, HTTP 422).
  - `docs/api/admin.md:717-751` exposes the full body shape, per-tier scope shape, register-and-open-atomically semantics, scope-immutability rule, and response body including `opened_by_sub`/`opened_by_tenant_id`.
  - `docs/reference/error-catalog.md:161` adds the `INVALID_BREAKER_SCOPE` row with parallel description.
  - `docs/operator-guide/troubleshooting.md:249-272` and `docs/operator-guide/lenny-ctl.md:245,403-415` expose the `--limit-tier` / `--scope` flags with the canonical `operation_type` enum list.
  - iter5 POL-021 (Low, `operation_type` closed-enum gap) is subsumed: the enum is pinned in the matcher table at line 290, in the admin-API body definition at line 312, and at the `AdmissionController` match rule at line 315 — consistently `uploads | delegation_depth | session_creation | message_injection` across the spec and docs.

- **POL-025** (Medium, operator-identity field drift across three breaker surfaces) — **Fixed.**
  - Redis `cb:{name}` value uses `opened_by_sub`/`opened_by_tenant_id` at `spec/11_policy-and-controls.md:283` and `spec/12_storage-architecture.md:192`, with the identity-field alignment explicitly annotated against `caller_sub`/`caller_tenant_id` on `admission.circuit_breaker_rejected` and `operator_sub`/`operator_tenant_id` on the CloudEvents operational payload and the audit state-change event.
  - CloudEvents payload at `spec/11_policy-and-controls.md:304` emits `operator_sub` and `operator_tenant_id` (was `operator_user_id`).
  - `circuit_breaker.state_changed` audit payload at `spec/11_policy-and-controls.md:319` carries `operator_sub`/`operator_tenant_id` (was `operator_user_id`).
  - `grep "operator_user_id" spec/` returns zero matches; the only residual `opened_by: user_id` references in the repo are in historical review-findings files.
  - `docs/api/admin.md:748` response schema for `POST .../open` carries `opened_by_sub`/`opened_by_tenant_id`; `docs/operator-guide/observability.md:201` describes the `circuit_breaker.state_changed` payload with the canonical field set.

- **POL-026** (Medium, `circuit_breaker.state_changed` missing from §16.7 catalogue) — **Fixed.**
  - `spec/16_observability.md:655` now has a `circuit_breaker.state_changed` catalogue entry between `admission.circuit_breaker_rejected` (line 654) and `admission.circuit_breaker_cache_stale` (line 657).
  - Field set in §16.7 line 655 matches §11.6 line 319 exactly: `circuit_name`, `old_state`, `new_state`, `reason`, `limit_tier`, `scope`, `operator_sub`, `operator_tenant_id`, `timestamp`. "Not sampled" discipline noted (state transitions are rare; one row per admin action). Cross-reference back to §11.6 Storage and propagation for the full operator-identity alignment.

- **POL-024** (Low, §11.6 AdmissionController-evaluation line 315 still uses pre-iter3 prose framing, contradicting the POL-014 callout at line 317) — **NOT fixed.** Line 315 still opens with "...evaluates all active (open) circuit breakers at the start of every session-creation and delegation admission check, **before quota and policy evaluation**." Line 317 (the POL-014 callout) uses the canonical phase vocabulary (`PreAuth` → `AdmissionController` → `PostAuth`/`PreDelegation`). This is the same carry-forward pattern as iter4 POL-022 → iter5 POL-024 → iter6 POL-029 below (consistent Low severity).

- **POL-027** (Low, `spec/11_policy-and-controls.md:298` "REJECT / non-REJECT" vocabulary vs. paired metric/audit `rejected`/`admitted`) — **NOT fixed.** Line 298 still reads "Each stale-serve admission decision (both **REJECT and non-REJECT outcomes**) increments `lenny_circuit_breaker_cache_stale_serves_total`...". §16.1 line 211 uses `outcome: rejected | admitted` and §16.7 line 657 enumerates `outcome (admitted | rejected)`. The same carry-forward pattern as POL-027 iter5 → iter6 POL-030 below (consistent Low severity).

- **POL-028** (Low, `admission.circuit_breaker_cache_stale` sampling discipline documented only in §16.7, not in §11.6) — **NOT fixed.** The "Sampling under breaker storms" paragraph at `spec/11_policy-and-controls.md:321` still describes sampling only for `admission.circuit_breaker_rejected`. The different sampling key of the cache-stale sibling — `(tenant_id, caller_sub, outcome)` without `circuit_name` — lives exclusively in §16.7 line 657. Same carry-forward pattern as POL-028 iter5 → iter6 POL-031 below (consistent Low severity).

- **POL-019, POL-020** (Fixed at iter4, still fixed): the admission-path Redis-outage posture and `admission.circuit_breaker_cache_stale` audit event remain present; the POL-019 "/ equivalent" placeholder is still absent.

**Iter6 new findings.**

---

### POL-029 §11.6 "AdmissionController evaluation" line 315 still uses pre-iter3 prose framing "before quota and policy evaluation", contradicting the adjacent POL-014 callout at line 317 (iter5 POL-024 carry-forward) [Low]

**Section:** `spec/11_policy-and-controls.md:315-317`.

The iter5 POL-024 resolution did not reach this sentence. Line 315 (post-iter5) reads:

> "**AdmissionController evaluation.** The gateway evaluates all active (open) circuit breakers at the start of every session-creation and delegation admission check, **before quota and policy evaluation**. For each open breaker, the gateway matches the request against the breaker's persisted `limit_tier`/`scope` pair (from `cb:{name}`, above) — **not** the breaker's `{name}` string. A breaker with `limit_tier: "runtime"` and `scope: { "runtime": X }` matches a request whose resolved runtime equals `X`; ... If any open circuit breaker matches the request, it is rejected immediately with `CIRCUIT_BREAKER_OPEN` (HTTP 503, `retryable: false`); the error body includes `circuit_name`, `reason`, and `opened_at`."

Line 317 (the POL-014 callout, unchanged):

> "Circuit-breaker evaluation is a **pre-chain gate** and is NOT an interceptor. It runs **after `AuthEvaluator` completes at `PreAuth`** and **before the `PostAuth` and `PreDelegation` interceptor chains run**..."

The two paragraphs describe the same ordering with two different vocabularies back-to-back. A reader scanning §11.6 for "where does the circuit-breaker gate run?" must reconcile prose "before quota and policy evaluation" with canonical `PreAuth` → `AdmissionController` → `PostAuth`/`PreDelegation` using the §4.8 interceptor priority table — identical reader-experience friction that iter4 POL-022 → iter5 POL-024 identified.

Iter4 POL-022 and iter5 POL-024 both landed at Low; anchoring at Low preserves severity calibration (documentation consistency on redundant prose in back-to-back paragraphs, not a correctness bug — `QuotaEvaluator` runs at `PostAuth` and `DelegationPolicyEvaluator` runs at `PreDelegation`, so both framings describe the same ordering).

**Recommendation:** Rewrite line 315 to open with the canonical phase vocabulary, eliminating the redundant prose framing. Suggested text:

> "**AdmissionController evaluation.** The gateway evaluates all active (open) circuit breakers as a **pre-chain gate** at the start of every session-creation and delegation admission check — **after `AuthEvaluator` completes at `PreAuth`** and **before the `PostAuth` and `PreDelegation` interceptor chains run** (see the pre-chain gate callout below and [§4.8](04_system-components.md#48-gateway-policy-engine)). For each open breaker, the gateway matches the request against the breaker's persisted `limit_tier`/`scope` pair (from `cb:{name}`, above) — **not** the breaker's `{name}` string. A breaker with `limit_tier: "runtime"` and `scope: { "runtime": X }` matches ...; if any open circuit breaker matches the request, it is rejected immediately with `CIRCUIT_BREAKER_OPEN` (HTTP 503, `retryable: false`); the error body includes `circuit_name`, `reason`, and `opened_at`."

The POL-014 callout at line 317 can then be trimmed to reference-only (no re-stating of the ordering) to reduce the amount of prose that must stay synchronized on future edits — the callout already delegates the rationale to §4.8 "AdmissionController is a pre-chain gate, not an interceptor".

---

### POL-030 §11.6 "Running replica, Redis outage" bullet at line 298 still uses "REJECT / non-REJECT" vocabulary while the paired metric/audit event use `rejected`/`admitted` (iter5 POL-027 carry-forward) [Low]

**Section:** `spec/11_policy-and-controls.md:298`, `spec/16_observability.md:211`, `spec/16_observability.md:657`.

The iter5 POL-027 resolution did not reach this bullet. Line 298 still reads:

> "**Running replica, Redis outage.** ...Each stale-serve admission decision (both **REJECT and non-REJECT outcomes**) increments `lenny_circuit_breaker_cache_stale_serves_total` and is covered by the sampled `admission.circuit_breaker_cache_stale` audit event (§16.7)..."

The paired metric at `spec/16_observability.md:211` labels the counter with `outcome: rejected | admitted`. The paired audit event at `spec/16_observability.md:657` describes the payload field as `outcome (admitted | rejected)`. A deployer reading §11.6 for the metric/audit label vocabulary has to translate "non-REJECT" → `admitted` — which is not an obvious rename (the canonical term is `admitted`, not `accepted` or `non-rejected`).

Same Low severity anchoring as iter5 POL-027 (vocabulary drift across sibling sections; behaviorally correct but documentation-consistency gap).

**Recommendation:** Rewrite the parenthetical on `spec/11_policy-and-controls.md:298` to use the enum values. Suggested edit:

> "...Each stale-serve admission decision (both `outcome="rejected"` and `outcome="admitted"` — the latter being the security-salient case where the admission path could not verify a breaker's current state and served a non-rejection against a stale view) increments `lenny_circuit_breaker_cache_stale_serves_total` (`outcome` label) and is covered by the sampled `admission.circuit_breaker_cache_stale` audit event (§16.7)..."

This aligns the prose with the metric label vocabulary and the §16.7 payload field and eliminates the "non-REJECT" term.

---

### POL-031 `admission.circuit_breaker_cache_stale` sampling discipline is specified only in §16.7, not in §11.6 "Sampling under breaker storms" home section (iter5 POL-028 carry-forward) [Low]

**Section:** `spec/11_policy-and-controls.md:298, 321` (home-section sampling paragraph), `spec/16_observability.md:657` (catalogue entry with sampling rule).

The iter5 POL-028 resolution did not reach §11.6. The "Sampling under breaker storms" paragraph at `spec/11_policy-and-controls.md:321` documents sampling for `admission.circuit_breaker_rejected` (key: `(tenant_id, circuit_name, caller_sub)`, 10 s per-replica window) but not for `admission.circuit_breaker_cache_stale`. The sampling rule for the cache-stale sibling — keyed by `(tenant_id, caller_sub, outcome)` without `circuit_name` — lives exclusively in §16.7 line 657:

> "...Sampled per replica at the first stale-serve per `(tenant_id, caller_sub, outcome)` tuple within any rolling 10-second window (same discipline as `admission.circuit_breaker_rejected`); subsequent stale-serves in the window increment `lenny_circuit_breaker_cache_stale_serves_total` but do not write individual rows."

A reader scanning §11.6 for the subsystem's sampling discipline (reasonable — §11.6 IS the circuit-breaker subsystem's home section) will either (a) assume the rejection rule applies (wrong key — includes `circuit_name` where it should not), or (b) conclude there is no sampling and expect every stale-serve to produce an audit row (wrong — matches rejection-storm discipline by design). The principled distinction between the two keys (rejection is per-breaker; cache-stale is cache-wide, hence the `outcome` discriminator replaces `circuit_name`) is invisible at the home section.

iter4 POL-022 / iter5 POL-024 / iter6 POL-029 family (vocabulary) and iter5 POL-027 / iter6 POL-030 family (enum-mismatch) are all Low carry-forwards at consistent severity; POL-028 → POL-031 follows the same calibration (documentation completeness at the subsystem home section; the behavior itself is pinned in §16.7 and the metrics correctly reflect it).

**Recommendation:** Extend `spec/11_policy-and-controls.md:298` bullet (immediately after "...covered by the sampled `admission.circuit_breaker_cache_stale` audit event (§16.7)...") to state the sampling key explicitly, or add a sentence after the bullet list at line 300 along the lines of:

> "The `admission.circuit_breaker_cache_stale` audit event follows the same per-replica 10 s sampling discipline as `admission.circuit_breaker_rejected` (§11.6 Sampling under breaker storms below), with a different key: the first stale-serve per `(tenant_id, caller_sub, outcome)` tuple per replica per window is written; subsequent stale-serves in the window increment `lenny_circuit_breaker_cache_stale_serves_total` but do not write individual rows. `circuit_name` is not part of the cache-stale sampling key because the event describes the cache as a whole, not a specific breaker — see §16.7 for the authoritative payload schema."

Alternative landing site: extend the "Sampling under breaker storms" paragraph at line 321 with a trailing sentence that names the sibling event and its different key.

---

### POL-032 `spec/25_agent-operability.md:688-689` Event Types table describes `circuit_breaker_opened`/`_closed` payload as "opener identity"/"closer identity" — inconsistent with the iter5 POL-025 fix that pinned `operator_sub`/`operator_tenant_id` across §11.6 Event emission, the CloudEvents payload, and the audit event [Low]

**Section:** `spec/25_agent-operability.md:688-689` (Event Types table under §25.3 Event Emission), `spec/11_policy-and-controls.md:304` (authoritative CloudEvents emission), `spec/16_observability.md:655` (audit event payload).

The iter5 POL-025 resolution renamed the operator-identity fields to `operator_sub`/`operator_tenant_id` across the three POL surfaces (Redis state, CloudEvents emission, audit state-change event). The §25.3 Event Types table at `spec/25_agent-operability.md:688-689` still describes the same two events with the pre-iter5 framing:

> "| `circuit_breaker_opened` | Circuit breaker opened | Name, reason, opener identity |
> | `circuit_breaker_closed` | Circuit breaker closed | Name, closer identity |"

The table's "Payload highlights" column is the at-a-glance enumeration agents and deployers read when wiring SIEM consumers against the `/v1/admin/events/*` CloudEvents stream. The §11.6 line 304 authoritative text says the payload includes "`name`, `reason`, `operator_sub`, and `operator_tenant_id`"; the §25.3 table says "Name, reason, opener identity". The vocabulary diverges on two axes: (a) "opener identity"/"closer identity" is a generic placeholder rather than the canonical two-field pair, and (b) the `close` row omits `reason` entirely while §11.6 emits it for both transitions.

The divergence reproduces the exact cross-section drift pattern iter4 POL-018 and iter5 POL-025 identified: a deployer wiring a CloudEvents consumer against §25.3 as the authoritative enumeration will model the payload as "opener identity / closer identity" and miss the `operator_sub`/`operator_tenant_id` shape. A parallel issue exists in `docs/reference/cloudevents-catalog.md:55-56`, which documents the same CloudEvents with `opener`/`closer` field names (see POL-033 below).

Severity anchors to iter5 POL-025 (Medium) reduced to Low because (a) §25.3 is a §25 agent-operability table rather than the subsystem home section, and (b) the iter5 POL-025 scope specifically called out three surfaces — Redis, CloudEvents payload at §11.6, audit payload at §11.6 — and resolved them; the §25.3 / `cloudevents-catalog.md` surfaces are a sibling-drift discovery. Low is consistent with POL-018's iter5 POL-025 extension family applied to a non-home table.

**Recommendation:** Rewrite the two table rows in `spec/25_agent-operability.md:688-689`:

> "| `circuit_breaker_opened` | Circuit breaker opened | `name`, `reason`, `operator_sub`, `operator_tenant_id` (operator who opened the breaker — see [§11.6](11_policy-and-controls.md#116-circuit-breakers) Event emission) |
> | `circuit_breaker_closed` | Circuit breaker closed | `name`, `reason`, `operator_sub`, `operator_tenant_id` |"

This aligns the §25.3 table with the §11.6 authoritative payload enumeration and the §16.7 audit event catalogue, completing the POL-025 → POL-032 field-name alignment across all four breaker surfaces (Redis, CloudEvents payload at §11.6, CloudEvents Event Types table at §25.3, audit state-change event at §11.6/§16.7).

---

### POL-033 `docs/reference/cloudevents-catalog.md` documents `circuit_breaker_opened` / `_closed` payload as `opener`/`closer` — stale against iter5 POL-025 fix [Low]

**Section:** `docs/reference/cloudevents-catalog.md:55-56`.

The docs CloudEvents catalog is the deployer-facing reference for CloudEvents payload shapes. The iter5 POL-025 fix pinned the payload fields to `operator_sub`/`operator_tenant_id` at `spec/11_policy-and-controls.md:304` but did not propagate into `docs/reference/cloudevents-catalog.md`. Lines 55-56 still read:

> "| `dev.lenny.circuit_breaker_opened` | Circuit opened | `name`, `reason`, `opener` |
> | `dev.lenny.circuit_breaker_closed` | Circuit closed | `name`, `closer` |"

Consequences match the POL-025 → POL-032 pattern: a deployer wiring a SIEM consumer or CloudEvents webhook receiver against `docs/reference/cloudevents-catalog.md` as the external reference will model the payload as `{name, reason, opener}` / `{name, closer}` — wrong field names on three counts (`opener` → `operator_sub`/`operator_tenant_id`; `closer` ditto; the close event also carries `reason`). The CloudEvents catalog is the public contract for external consumers; the stale doc will cause receiver-side parse failures or SIEM mappings that never populate the operator-identity columns.

This is the docs-sync companion to POL-032. Both are Low on the POL-018 / POL-025 extension family because they're cross-reference surfaces rather than authoritative payload source definitions (§11.6 remains authoritative per the iter5 fix).

**Recommendation:** Update `docs/reference/cloudevents-catalog.md:55-56` to match the authoritative payload at `spec/11_policy-and-controls.md:304`:

> "| `dev.lenny.circuit_breaker_opened` | Circuit opened | `name`, `reason`, `operator_sub`, `operator_tenant_id` |
> | `dev.lenny.circuit_breaker_closed` | Circuit closed | `name`, `reason`, `operator_sub`, `operator_tenant_id` |"

Per the `feedback_docs_sync_after_spec_changes.md` rule (reconcile docs with spec changes after each review-fix iteration before declaring convergence), this is a mandatory docs-sync remediation for the iter5 POL-025 fix.

---

## Convergence assessment

Iter6 on the Policy Engine & Admission Control perspective **does not converge**. Five findings remain:

- **0 Critical / High / Medium.** No new correctness or subsystem-architecture findings at High or Medium. The iter5 High (POL-023 scope schema) and two Mediums (POL-025 operator identity, POL-026 §16.7 catalogue entry) are all correctly fixed end-to-end: admin API body, Redis value, admission-evaluation rule, audit state-change catalogue entry with matching field set to §11.6 line 319, operator-identity vocabulary, and docs in `docs/api/admin.md` / `docs/reference/error-catalog.md` / `docs/operator-guide/lenny-ctl.md` / `docs/operator-guide/troubleshooting.md` / `docs/operator-guide/observability.md` all aligned with the iter5 spec state.

- **5 Low:**
  - POL-029 (iter5 POL-024 → iter6 carry-forward; §11.6 line 315 prose "before quota and policy evaluation" vs. canonical `PreAuth`→`AdmissionController`→`PostAuth`/`PreDelegation` at line 317).
  - POL-030 (iter5 POL-027 → iter6 carry-forward; §11.6 line 298 "REJECT / non-REJECT" vs. metric/audit `rejected`/`admitted`).
  - POL-031 (iter5 POL-028 → iter6 carry-forward; §11.6 "Sampling under breaker storms" only documents rejection-event sampling, not cache-stale sibling at §16.7).
  - POL-032 (new sibling discovery from iter5 POL-025 field-rename; `spec/25_agent-operability.md:688-689` Event Types table still uses "opener identity"/"closer identity").
  - POL-033 (docs-sync companion to POL-032; `docs/reference/cloudevents-catalog.md:55-56` still uses `opener`/`closer`).

All five Lows are consistent-severity anchored against iter4/iter5 Low carry-forwards (POL-022 → POL-024 → POL-029; POL-027 → POL-030; POL-028 → POL-031) and against the POL-018/POL-025 identity-field alignment family reduced-from-Medium-for-non-home-surface rationale (POL-032, POL-033).

Iter4 Fixed items (POL-018, POL-019, POL-020) and iter5 Fixed items (POL-023, POL-025, POL-026) remain correctly fixed; cross-section edits in §4.8 callout, §12.4 Redis key table, §15.4 error catalog, §16.1 metric table, §16.5 alerting table, §16.7 audit event catalogue, and the docs surfaces (`docs/api/admin.md`, `docs/operator-guide/*`, `docs/reference/error-catalog.md`, `docs/reference/metrics.md`) check out against the §11.6 line references. The spec-internal §25.3 Event Types table (POL-032) and the `docs/reference/cloudevents-catalog.md` CloudEvents reference (POL-033) are the two surfaces missed by the iter5 POL-025 field-rename pass.

A sixth iteration can close POL-029/POL-030/POL-031/POL-032/POL-033 with purely mechanical text edits — no behavior change is required on any finding — so convergence is within reach once this iteration's fixes apply.
