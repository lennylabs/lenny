# Iter5 Review — Perspective 24: Policy Engine & Admission Control

**Scope:** `spec/11_policy-and-controls.md` §11.6 (Circuit Breakers) with cross-file touchpoints in `spec/16_observability.md` §16.1/§16.5/§16.7 and `spec/12_storage-architecture.md` §12.4 (`cb:{name}` key schema) and `spec/04_system-components.md` §4.8 (pre-chain gate callout).

**Iter4 carry-forward verification.**

- **POL-018** (Medium, audit-field alignment between §11.6 and §16.7 for `admission.circuit_breaker_rejected`) — **Fixed.** `spec/11_policy-and-controls.md:310` now lists `caller_sub`, `caller_tenant_id`, `limit_tier`, `replica_service_instance_id`, `parent_session_id`, `delegation_depth`, and the continuation-vs-delegation snapshot split; the sentence ends with "§16.7 is the authoritative schema source for this event." The POL-020 side-effect ("/ equivalent" placeholder) is also removed — `grep` across the spec returns no matches.
- **POL-019** (Medium, Admission-path Redis-outage posture) — **Fixed.** `spec/11_policy-and-controls.md:287-293` adds the "Admission-path Redis-outage posture" paragraph with running-replica / fresh-replica / recovery bullets and the `CIRCUIT_BREAKER_CACHE_UNINITIALIZED` readiness-refusal reason. `spec/16_observability.md:205-207` adds `lenny_circuit_breaker_cache_stale_seconds`, `lenny_circuit_breaker_cache_stale_serves_total`, and `lenny_circuit_breaker_cache_initialized`. `spec/16_observability.md:505` adds `CircuitBreakerStale`. `spec/16_observability.md:628` adds the `admission.circuit_breaker_cache_stale` audit event.
- **POL-020** (Low, "/ equivalent" placeholder) — **Fixed** as a side-effect of POL-018 (see above).
- **POL-021** (Low, `limit_tier=operation_type` value set never enumerated) — **NOT fixed.** `grep "operation_type" spec/` returns only the metric-label callouts at `spec/16_observability.md:203-204`, `spec/11_policy-and-controls.md:306,308,310,312`, and `spec/16_observability.md:627`. All treat `operation_type` as a free-form string; none enumerate the closed value set. The two operator-declarable states at §11.6 lines 280-281 ("Uploads temporarily disabled", "Delegation depth > N disabled during incident") still have no canonical mapping. Carried into iter5 as POL-023 below with broadened scope.
- **POL-022** (Low, prose "before quota and policy evaluation" at the former line 298) — **NOT fixed.** The same prose framing now lives at `spec/11_policy-and-controls.md:306` ("before quota and policy evaluation"). The back-to-back paragraph at line 308 (the POL-014 callout) uses the canonical phase vocabulary (`PreAuth` → `AdmissionController` → `PostAuth`/`PreDelegation`), producing the exact reconcile-in-reader's-head problem iter4 identified. Carried into iter5 as POL-024 below.

**Iter5 new findings.**

---

### POL-023 Admin API `POST /v1/admin/circuit-breakers/{name}/open` body schema is under-specified — no way to declare which scope (`runtime` / `pool` / `connector` / `operation_type`) the breaker covers [High]

**Section:** `spec/11_policy-and-controls.md:303` (admin API body definition), `spec/11_policy-and-controls.md:283` (`cb:{name}` Redis value schema), `spec/11_policy-and-controls.md:306` ("applies to the requested runtime, pool, connector, or operation type"), `spec/12_storage-architecture.md:192` (Redis key-prefix table confirms the stored value shape).

`spec/11_policy-and-controls.md:306` states that the `AdmissionController` rejects a request when "any open circuit breaker applies to the requested runtime, pool, connector, or operation type." But nothing in the admission spec, the Redis value schema, or the admin API body tells the gateway **which scope a named breaker covers**:

1. The admin API body at `spec/11_policy-and-controls.md:303` is defined exclusively as `{ "reason": string }`. There is no field for the breaker's `limit_tier`, no field for the matched runtime name, no field for the matched pool name, no field for the matched connector identifier, no field for the `operation_type` value.
2. The `cb:{name}` Redis value at `spec/11_policy-and-controls.md:283` and `spec/12_storage-architecture.md:192` is `{state: "open"|"closed", reason: string, opened_at: ISO8601, opened_by: user_id}`. No scope-bearing fields.
3. The iter3 POL-017 fix introduced `limit_tier` as a metric and audit label with vocabulary `runtime | pool | connector | operation_type`, and §16.7 line 627 records that value in the rejection audit event — but the admin API never lets the operator **set** a scope, so the admission path has no authoritative source for "does this breaker match this runtime?" beyond inferring from the breaker's `{name}` string.

The practical consequences are security-salient:

- **Policy-evaluation correctness hole.** An operator opens a breaker at `/v1/admin/circuit-breakers/runtime_python_ml_degraded/open` with `{ "reason": "runtime degraded" }`. The admission path at line 306 is supposed to reject requests for the `runtime_python_ml` runtime, but there is no specified mechanism to turn `runtime_python_ml_degraded` into "matches `runtime=runtime_python_ml`". Either (a) the name-to-scope mapping is conventional/by-naming-convention (unspecified and fragile), or (b) the breaker matches every admission check regardless of the requested runtime/pool/connector (over-broad fail-closed), or (c) the breaker matches nothing and the admission path is silently a no-op (fail-open against the operator's intent). The spec does not pin which of these holds.
- **`limit_tier` label integrity.** The `admission.circuit_breaker_rejected` audit event populates `limit_tier` with one of `runtime | pool | connector | operation_type`. For the gateway to emit the correct value, it must know which scope the breaker covers — but there is no input path that communicates that scope. The label will either be populated heuristically from the breaker name (a brittle convention that iter3 POL-017 did not specify) or a single default value (which breaks the "metric spike correlates 1:1 with sampled audit rows" guarantee that the same §16.7 paragraph documents).
- **Pre-existing registration vs. ad-hoc creation.** The admin API surface enumerates `GET /v1/admin/circuit-breakers` (list), `GET /v1/admin/circuit-breakers/{name}` (read), `POST .../open`, `POST .../close`. There is no registration endpoint — the list endpoint implies some `{name}`-s are known a priori, but the spec does not say how a breaker's scope was declared when it was registered, nor whether `POST .../open` against an unknown name is an implicit registration with no scope information.
- **Interaction with POL-021.** The iter4 POL-021 Low-severity finding flagged a closed-enum gap for `operation_type`. That finding is a subset of this one: even if `operation_type` had a closed enum, the admin API would still have no way to declare **which** `operation_type` value the breaker binds to for a given `{name}`. This is why the severity escalates to High for iter5 — the iter4 narrow framing as an enum omission under-represented the body-schema gap.

This is material for the POL perspective because circuit breakers are the incident-response primitive. An operator who runs `POST /v1/admin/circuit-breakers/uploads_disabled/open` during an incident needs the platform to actually block uploads — not to block everything, not to block nothing, and not to depend on an unspecified naming convention to work.

**Recommendation:** Define a structured body schema for `POST /v1/admin/circuit-breakers/{name}/open` that captures the breaker's scope at open-time and persists it into `cb:{name}`. Concretely:

1. Extend `cb:{name}` value to `{state, reason, opened_at, opened_by, limit_tier, scope}` where `limit_tier ∈ {runtime | pool | connector | operation_type}` (same closed vocabulary as the metric/audit label) and `scope` carries the tier-specific matcher (`{ "runtime": "runtime_python_ml" }`, `{ "pool": "gpu-pool-a" }`, `{ "connector": "github-app" }`, or `{ "operation_type": "uploads" | "delegation_depth" | ... }`).
2. Rewrite the admin API body at `spec/11_policy-and-controls.md:303` to:
   ```
   { "reason": string, "limit_tier": "runtime"|"pool"|"connector"|"operation_type", "scope": { <tier-specific matcher> } }
   ```
   Reject requests with an unknown `limit_tier` or an ill-formed `scope` at the API boundary with `INVALID_BREAKER_SCOPE` (HTTP 422).
3. Add a `POST /v1/admin/circuit-breakers` registration endpoint (or document that `POST .../open` against an unknown name registers and opens atomically with the provided `limit_tier`/`scope`). Either way, pin the behavior explicitly so the list endpoint and the open endpoint share a well-defined breaker-lifecycle model.
4. In `spec/11_policy-and-controls.md:306`, amend the AdmissionController-evaluation sentence to state that the match is against the persisted `limit_tier`/`scope` of the breaker (not the `{name}` string), so the admission path's policy-evaluation rule is authoritative and not naming-convention-bound.
5. Update `spec/12_storage-architecture.md:192` to reflect the extended value shape.
6. This subsumes POL-021's `operation_type` enumeration requirement: declaring `operation_type` as one of the `limit_tier` values with a tier-specific closed-enum matcher (`{ "operation_type": "uploads" | "delegation_depth" | ... }`, extensible via a platform-level admin endpoint) is the natural home for the enumeration.

---

### POL-024 §11.6 "AdmissionController evaluation" line 306 still uses pre-iter3 prose framing, contradicting the adjacent POL-014 callout at line 308 [Low]

**Section:** `spec/11_policy-and-controls.md:306-308`.

The iter4 POL-022 finding (Low) identified this exact issue at the pre-POL-019-edit line 298; the iter4 fix added the "Admission-path Redis-outage posture" paragraph above, pushing the unchanged sentence to line 306 but not revising its framing. Line 306 now reads:

> "**AdmissionController evaluation.** The gateway evaluates all active (open) circuit breakers at the start of every session-creation and delegation admission check, **before quota and policy evaluation**. If any open circuit breaker applies to the requested runtime, pool, connector, or operation type, the request is rejected immediately with `CIRCUIT_BREAKER_OPEN` (HTTP 503, `retryable: false`)."

The immediately-following callout at line 308 uses the canonical phase vocabulary:

> "Circuit-breaker evaluation is a **pre-chain gate** and is NOT an interceptor. It runs **after `AuthEvaluator` completes at `PreAuth`** and **before the `PostAuth` and `PreDelegation` interceptor chains run**..."

The two framings describe the same ordering but with different vocabularies (prose "before quota and policy evaluation" vs. canonical `PreAuth` → `AdmissionController` → `PostAuth`/`PreDelegation`). `QuotaEvaluator` runs at `PostAuth` and `DelegationPolicyEvaluator` runs at `PreDelegation`, so "before quota and policy evaluation" is technically consistent — but a reader scanning the section for "where does the circuit-breaker gate run?" has to reconcile two different framings in back-to-back paragraphs and to know the §4.8 interceptor priority table to verify the reconciliation.

This is Low-severity (documentation consistency, not a correctness bug) and its framing is unchanged from iter4 POL-022 — consistent severity anchoring.

**Recommendation:** Rewrite line 306 to use the canonical phase vocabulary, eliminating the redundant prose framing. Suggested text:

> "**AdmissionController evaluation.** The gateway evaluates all active (open) circuit breakers as a **pre-chain gate** at the start of every session-creation and delegation admission check — **after `AuthEvaluator` completes at `PreAuth`** and **before the `PostAuth` and `PreDelegation` interceptor chains run** (see the pre-chain gate callout below and [§4.8](04_system-components.md#48-gateway-policy-engine)). If any open circuit breaker's `limit_tier`/`scope` matches the requested runtime, pool, connector, or operation type, the request is rejected immediately with `CIRCUIT_BREAKER_OPEN` (HTTP 503, `retryable: false`). The error body includes `circuit_name`, `reason`, and `opened_at`."

The POL-014 callout at line 308 can then be trimmed to reference-only (no re-stating of the ordering) to reduce the amount of prose that must stay synchronized on future edits.

---

### POL-025 Operator-identity field name drifts across three circuit-breaker surfaces (`opened_by` vs. `operator_user_id` vs. `caller_sub`) [Medium]

**Section:** `spec/11_policy-and-controls.md:283` (`cb:{name}` Redis value), `spec/11_policy-and-controls.md:295` (operational event emission payload), `spec/11_policy-and-controls.md:310` (audit-event payload for `circuit_breaker.state_changed`), `spec/12_storage-architecture.md:192` (same `cb:{name}` schema).

The iter4 POL-018 fix aligned the `admission.circuit_breaker_rejected` audit event on `caller_sub`/`caller_tenant_id`, making §11.6 line 310 consistent with §16.7 line 627. The fix did **not** touch three sibling surfaces that still use pre-iter3 field names:

1. **`cb:{name}` Redis value.** `spec/11_policy-and-controls.md:283` and `spec/12_storage-architecture.md:192` both pin the value to `{state, reason, opened_at, opened_by: user_id}`. The comment specifically annotates `opened_by` as `user_id` — gateway-internal, not an OIDC subject claim.
2. **Operational event (CloudEvents `dev.lenny.circuit_breaker_opened`/`_closed`).** `spec/11_policy-and-controls.md:295` says the emitted payload includes "`name`, `reason`, and `operator_user_id`".
3. **`circuit_breaker.state_changed` audit event.** `spec/11_policy-and-controls.md:310` says the payload contains "`circuit_name`, `old_state`, `new_state`, `reason`, `operator_user_id`, `timestamp`".

The three surfaces describe the same operator identity three different ways. POL-018's rationale for `caller_sub`/`caller_tenant_id` on the rejection event ("OIDC subject identifier with different GDPR exposure than a gateway-internal `user_id`") applies identically to all three: the operator who opened the breaker is authenticated via the admin API and their identity propagates from the same JWT claim source. Using `user_id` / `operator_user_id` here means:

- The Redis-stored identity has different GDPR/retention semantics than the audit identity of the `admission.circuit_breaker_rejected` event, for the same person performing the same operation.
- The operational CloudEvents payload at `/v1/admin/events/*` uses one schema for the operator identity; the audit payload at §16.7 (when `circuit_breaker.state_changed` is catalogued there — see POL-026 below) will use another; the `admission.circuit_breaker_rejected` event uses a third — SIEM consumers and operator tooling must carry three parallel mappings for the same concept.
- `opened_by: user_id` in the `cb:{name}` Redis value is surfaced on the admin-API `GET /v1/admin/circuit-breakers/{name}` response; operators reading that response see `user_id` and have to know it is a gateway-internal identifier rather than the `sub` claim they would see on the rejection audit row.

This is Medium because it is the exact POL-018 drift (identity-field vocabulary inconsistency across POL surfaces) that iter4 fixed for the rejection event but that still exists for the state-change event and the Redis-stored value. Consistent severity anchoring to POL-018.

**Recommendation:** Rename `opened_by` → `opened_by_sub`/`opened_by_tenant_id` in the `cb:{name}` Redis value (both `spec/11_policy-and-controls.md:283` and `spec/12_storage-architecture.md:192`). Rename `operator_user_id` → `operator_sub`/`operator_tenant_id` on the CloudEvents operational payload at `spec/11_policy-and-controls.md:295` and on the `circuit_breaker.state_changed` audit payload at `spec/11_policy-and-controls.md:310`. This aligns the operator-identity vocabulary with POL-018's `caller_sub`/`caller_tenant_id` and with the §13.3 Token Service audit events.

---

### POL-026 `circuit_breaker.state_changed` audit event is not catalogued in §16.7 [Medium]

**Section:** `spec/11_policy-and-controls.md:310`, `spec/16_observability.md:620-642` (§16.7 audit event catalogue).

`spec/11_policy-and-controls.md:310` declares that every breaker state change emits a `circuit_breaker.state_changed` audit event with payload `{circuit_name, old_state, new_state, reason, operator_user_id, timestamp}`. The last sentence of the same paragraph says:

> "Both events are written to the append-only audit tables ([§11.7](#117-audit-logging)) and appear in the catalogued audit event list in [§16.7](16_observability.md#167-section-25-audit-events)."

But `grep circuit_breaker.state_changed spec/16_observability.md` returns zero matches. The §16.7 catalogue (lines 620-642) enumerates `admission.circuit_breaker_rejected` (line 627) and `admission.circuit_breaker_cache_stale` (line 628), but not `circuit_breaker.state_changed`. The iter4 POL-018 resolution text explicitly flagged this: "the `circuit_breaker.state_changed` state-change event in the same sentence should also be cross-referenced to §16.7 (currently §16.7 line 566 does not enumerate it, which is a separate gap worth filing if §16.7 intends to be comprehensive)." No iter5 finding has carried it through.

The practical consequence matches the POL-018 rationale: a deployer wiring SIEM consumers against §16.7 as the authoritative catalogue will omit the state-change event and receive unexpected OCSF records at runtime, or will write an alerting rule that never fires because they did not know the event shape existed. The catalogue is explicitly referenced from §11.6 line 310 as authoritative, and the omission produces the exact same cross-section drift pattern that POL-018 solved for the rejection event.

Medium severity (catalogue completeness on an authoritative cross-reference — same pattern as POL-018).

**Recommendation:** Add a `circuit_breaker.state_changed` entry to the §16.7 audit event catalogue between `admission.circuit_breaker_rejected` (line 627) and `admission.circuit_breaker_cache_stale` (line 628). Suggested text:

> - `circuit_breaker.state_changed` ([§11.6](11_policy-and-controls.md#116-circuit-breakers)) — emitted on every operator-managed circuit-breaker state transition via `POST /v1/admin/circuit-breakers/{name}/open` or `.../close`. Payload fields: `circuit_name`, `old_state` (`open` \| `closed`), `new_state` (`open` \| `closed`), `reason` (free-text from the admin API body for `open` transitions; platform-generated "operator close" for `close` transitions), `limit_tier` (breaker scope — `runtime` \| `pool` \| `connector` \| `operation_type`; see POL-023 for body schema), `scope` (tier-specific matcher), `operator_sub`, `operator_tenant_id` (POL-025 naming), `timestamp`. Not sampled (state transitions are rare; one row per admin action).

The recommendation assumes POL-023 and POL-025 are also adopted; if either is deferred, trim the corresponding fields from the §16.7 entry to match the state actually pinned in §11.6.

---

### POL-027 §11.6 "Running replica, Redis outage" bullet uses "REJECT / non-REJECT" vocabulary while the paired metric / audit event use `rejected` / `admitted` [Low]

**Section:** `spec/11_policy-and-controls.md:289`, `spec/16_observability.md:206`, `spec/16_observability.md:628`.

The POL-019 fix added the "Admission-path Redis-outage posture" paragraph with a bullet at line 289 that reads:

> "Each stale-serve admission decision (**both REJECT and non-REJECT outcomes**) increments `lenny_circuit_breaker_cache_stale_serves_total` and is covered by the sampled `admission.circuit_breaker_cache_stale` audit event (§16.7)..."

The paired metric at `spec/16_observability.md:206` uses `outcome`: `rejected` \| `admitted`. The paired audit event at `spec/16_observability.md:628` uses `outcome` (`admitted` \| `rejected`). The §11.6 bullet describes the same dimension with two different token conventions ("REJECT / non-REJECT" — uppercase, prose) while §16.1 and §16.7 use lowercase enum values (`rejected` / `admitted`). A deployer reading the §11.6 bullet for the metric label has to translate "non-REJECT" → `admitted`, which is not an obvious rename (it is not "non-rejected" or "accepted" — the spec elsewhere uses `admitted`).

Similar pattern to POL-022/POL-024 (vocabulary drift across sibling sections), Low severity, consistent anchoring.

**Recommendation:** Rewrite the parenthetical on `spec/11_policy-and-controls.md:289` to use the enum values. Suggested edit:

> "Each stale-serve admission decision (both `outcome="rejected"` and `outcome="admitted"` — the latter being the security-salient case where the admission path could not verify a breaker's current state and served a non-rejection against a stale view) increments `lenny_circuit_breaker_cache_stale_serves_total` (`outcome` label) and is covered by the sampled `admission.circuit_breaker_cache_stale` audit event (§16.7)..."

This aligns the prose with the metric and audit labels and eliminates the "non-REJECT" term.

---

### POL-028 `admission.circuit_breaker_cache_stale` sampling discipline is specified only in §16.7, not in §11.6 [Low]

**Section:** `spec/11_policy-and-controls.md:287-293` (Admission-path Redis-outage posture paragraph), `spec/16_observability.md:628` (audit event catalogue entry).

§11.6 line 312 ("Sampling under breaker storms") specifies the sampling discipline for `admission.circuit_breaker_rejected` — first rejection per `(tenant_id, circuit_name, caller_sub)` per rolling 10 s window per replica. The POL-019 fix added an adjacent sibling event, `admission.circuit_breaker_cache_stale`, that is described as "sampled" at line 289 ("the sampled `admission.circuit_breaker_cache_stale` audit event") but whose sampling key is specified **only in §16.7**:

> §16.7 line 628: "Sampled per replica at the first stale-serve per `(tenant_id, caller_sub, outcome)` tuple within any rolling 10-second window (same discipline as `admission.circuit_breaker_rejected`)..."

Note the sampling keys differ — `admission.circuit_breaker_rejected` keys on `(tenant_id, circuit_name, caller_sub)` (circuit_name present); `admission.circuit_breaker_cache_stale` keys on `(tenant_id, caller_sub, outcome)` (no circuit_name — by design, since cache-stale is orthogonal to a specific circuit, and `outcome` is the security-salient discriminator). This is a principled distinction, but §11.6 does not state it. A reader using §11.6 as the subsystem home (reasonable — §11.6 IS the circuit-breaker subsystem's home and iter3/iter4 carried that framing) will miss the cache-stale sampling rule entirely and either (a) assume the rejection rule applies (wrong key — includes `circuit_name`), or (b) conclude there is no sampling and expect every stale-serve to produce an audit row (wrong — the rule matches the rejection-storm discipline by design).

The POL-018 fix established the pattern "§11.6 is consistent with §16.7 for per-event payload schemas; §16.7 is authoritative for schema details". The sampling-key specification is symmetrical and should follow the same pattern.

Low severity (documentation completeness; the behavior is pinned in §16.7 and the metrics correctly reflect it — but the home-section reader experience is incomplete).

**Recommendation:** Extend `spec/11_policy-and-controls.md:289` to state the sampling key explicitly, or add a sentence after the bullet list at line 292 along the lines of:

> "The `admission.circuit_breaker_cache_stale` audit event follows the same per-replica 10 s sampling discipline as `admission.circuit_breaker_rejected` (§11.6 Sampling under breaker storms), with a different key: the first stale-serve per `(tenant_id, caller_sub, outcome)` tuple per replica per window is written; subsequent stale-serves in the window increment `lenny_circuit_breaker_cache_stale_serves_total` but do not write individual rows. `circuit_name` is not part of the cache-stale sampling key because the event describes the cache as a whole, not a specific breaker — see §16.7 for the authoritative payload schema."

---

## Convergence assessment

Iter5 on the Policy Engine & Admission Control perspective **does not converge**. Six findings remain:

- **1 High:** POL-023 (admin API body schema does not let the operator declare breaker scope; policy-evaluation correctness hole).
- **2 Medium:** POL-025 (operator-identity vocabulary drifts across three circuit-breaker surfaces; POL-018 half-fix), POL-026 (`circuit_breaker.state_changed` missing from the §16.7 catalogue; iter4 POL-018 resolution explicitly flagged it as worth filing).
- **3 Low:** POL-024 (iter4 POL-022 carry-forward; prose/phase vocabulary drift), POL-027 (REJECT / non-REJECT vs. `rejected`/`admitted` token mismatch in the POL-019 bullet), POL-028 (cache-stale sampling discipline absent from §11.6 home section).

POL-023 is the load-bearing blocker: without a scope-declaration field in the admin API body, the `AdmissionController` cannot deterministically evaluate "does this breaker apply to this request?", and the `limit_tier` label introduced by iter3 POL-017 has no authoritative input path. POL-025 and POL-026 are catalogue-drift follow-ons in the same POL-018 family. POL-024/POL-027/POL-028 are iter-carry-forward polish at consistent severity with their iter4 siblings.

Iter4 Fixed items (POL-018, POL-019, POL-020) remain correctly fixed and their cross-section edits in §16.1/§16.5/§16.7 check out against the §11.6 references.
