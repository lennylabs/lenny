# Perspective 11 — Session Lifecycle & State Management (iter8, regressions-only)

**Scope directive (iter8+):** Review only regressions introduced by the iter7 fix commit `bed7961`. Pre-existing issues, long-lived Low/Info carry-forwards, and new-coverage gaps unrelated to the fix envelope are out of scope.

**SES-relevant surfaces touched by `bed7961`:** The iter7 fix commit edited a single SES-relevant surface — `spec/09_mcp-integration.md` §9.2, inserting a new "Elicitation content integrity (gateway-origin binding)" paragraph between the "Response flows back down the same chain…" sentence and the "Elicitation provenance" table. Ancillary surfaces were the paired error-catalog row in `spec/15_external-api-surface.md` (§15.1 `ELICITATION_CONTENT_TAMPERED`), the paired metric row in `spec/16_observability.md` §16.1, the paired alert row in §16.5, and the paired audit-event row in §16.7. None of the other spec files touched by `bed7961` (§11, §13, §14, §17, §25) altered session/protocol surfaces under SES.

**Numbering:** SES-011 onwards (iter7 last SES finding was SES-010 per the iter7 rubric's new-finding numbering).

**Severity anchoring:** iter1–iter7 rubric preserved per `feedback_severity_calibration_iter5.md`. Documentation/completeness-only inconsistencies with no cross-surface implementation ambiguity stay Low and are therefore out of iter8's regression-flag envelope. An implementation-blocking contradiction inside §9 itself (wire mechanism and field vocabulary unreconcilable with the authoritative `lenny/request_elicitation` tool schema in §8.5) is Medium per the iter4/iter5 rubric — higher than the Low "external-reference-vs-authoritative-inconsistency" class because both surfaces are authoritative in their own section, and the contradiction cannot be resolved by following a cross-reference.

---

## 1. Regression review of the new §9.2 paragraph

### SES-011 (new in iter8, regression introduced by `bed7961`) — New gateway-origin-binding paragraph specifies a forward-hop wire mechanism and field vocabulary unreconcilable with the authoritative `lenny/request_elicitation` tool schema in §8.5 **[Fixed]**
**Severity:** Medium
**Status:** Fixed — §9.2 paragraph normalized to §8.5's `{message, schema}` tuple; forward-hop wire mechanism reframed as re-emission of the native MCP `elicitation/create` frame per the §15.2.1 per-kind wire projection. Propagated to §15.1 `ELICITATION_CONTENT_TAMPERED` row, §16.1 metric row, §16.5 alert row, §16.7 audit event (`divergent_fields` enum → `{message, schema}`; `original_sha256`/`attempted_sha256` over canonicalized `{message, schema}` pair). Docs resync: `docs/runtime-author-guide/platform-tools.md:264`, `docs/reference/metrics.md:251`, `docs/reference/error-catalog.md:108`, `docs/operator-guide/observability.md:189`. §8.5 tool schema unchanged.
**Location:** `spec/09_mcp-integration.md` §9.2 line 56 (the paragraph inserted by `bed7961`); cross-reference with `spec/08_recursive-delegation.md` §8.5 lines 489–505 (`lenny/request_elicitation` input schema).
**Finding:** The new §9.2 "Elicitation content integrity (gateway-origin binding)" paragraph — the sole SES-relevant prose added by the iter7 fix commit — specifies the elicitation-content-integrity invariant using two contracts that do not exist elsewhere in the spec:

1. **Content-field vocabulary.** The paragraph repeatedly identifies the gateway-recorded original content as the tuple `{title, description, schema, inputs}`. The authoritative `lenny/request_elicitation` input schema in §8.5 lines 489–505 declares only two properties — `schema` (JSON Schema for input collection) and `message` (human-readable prompt) — with both `required`. `title`, `description`, and `inputs` are not defined anywhere else in the spec as elicitation fields (greps for `"inputs"`, `{title, description, schema, inputs}`, and `title.*description.*schema` return no other definition points outside this paragraph and its derivative mentions in §15.1, §16.1, §16.5, §16.7). The paragraph does not reconcile the two vocabularies — it does not say "`title`/`description`/`inputs` are the MCP `elicitation/create` wire projection of the `lenny/request_elicitation` tool's `{message, schema}` inputs" or similar, and §15 wire-projection prose (lines 1353–1354) does not define the MCP frame's field mapping either.

2. **Forward-hop re-issuance mechanism.** The paragraph says: "If a forwarding pod emits a `lenny/request_elicitation` re-issuance that references an existing `elicitation_id` with divergent `{title, description, schema, inputs}`, the gateway drops the attempt with `ELICITATION_CONTENT_TAMPERED`…". This treats `lenny/request_elicitation` as the forward-hop wire mechanism an intermediate pod uses to relay an existing elicitation upstream. But (a) the `lenny/request_elicitation` tool schema in §8.5 has no `elicitation_id` input field — it is the *originator's* tool for creating a new elicitation, not a forwarder's tool carrying an existing identifier; (b) the pre-existing §9.2 hop-by-hop description ("servers elicit from their direct client, never skipping levels") and the §15.2.1 wire projection (lines 1353–1354: "`SessionEventElicitation` … Native MCP `elicitation/create` request") describe the inter-hop wire as the native MCP `elicitation/create` protocol method, not a re-invocation of the platform MCP tool `lenny/request_elicitation`. The paragraph's "re-issuance that references an existing `elicitation_id`" therefore specifies a wire mechanism that is neither documented in §8.5 (where the tool lives) nor consistent with §15.2.1's MCP-native elicitation-chain projection. An implementer asking "how does an intermediate pod forward an elicitation upstream by `elicitation_id` only?" cannot answer the question from §9.2 plus §8.5 plus §15.2.1 without guessing — the three surfaces give three different answers.

**Impact:** Implementation ambiguity concentrated inside §9, not merely a cross-reference polish gap. Specifically:

- The gateway-side invariant cannot be implemented without knowing *which* fields to canonicalize, hash, and compare (`{title, description, schema, inputs}` as §9.2 says, or `{schema, message}` as §8.5 says, or the native MCP `elicitation/create` wire fields per §15.2.1 — not defined in this spec).
- The `ELICITATION_CONTENT_TAMPERED` rejection path (§15.1 line 1079) references `details.elicitationId`, `details.originPod`, and `details.tamperingPod` but does not describe which fields diverged — it defers to the §9.2 paragraph, which uses the unreconciled `{title, description, schema, inputs}` vocabulary.
- The audit event `elicitation.content_tamper_detected` (§16.7 line 664) defines a `divergent_fields` payload field as "an array naming which of `title`, `description`, `schema`, `inputs` differed" — directly adopting the §9.2 paragraph's vocabulary. If an implementer normalizes the runtime comparison to `{schema, message}` (the §8.5 schema), the audit event's `divergent_fields` enum cannot carry faithful values (no `message` option; no `title`/`description`/`inputs` sources).
- The `original_sha256` / `attempted_sha256` payload fields on the audit event are defined as SHA-256 over "the canonicalized original `{title, description, schema, inputs}`" — an implementer cannot produce stable hashes without a canonicalization specification over fields that do not exist in the authoritative input schema.

This is a concrete internal-to-§9 contradiction: the new paragraph's wire/content contract cannot be implemented faithfully against the authoritative tool schema it references. The contradiction was introduced by `bed7961` — pre-fix §9.2 did not use `title`/`description`/`inputs` terminology.

**Recommendation:** Pick one of these resolutions and propagate consistently across §9.2, §15.1 error-catalog row, §15.2.1 wire-projection table, §16.1 metric row, §16.5 alert row, and §16.7 audit-event row:

- **Option A (preferred):** Normalize the §9.2 paragraph to the §8.5 tool-schema vocabulary. Replace `{title, description, schema, inputs}` with `{message, schema}` throughout §9.2, §15.1, §16.1, §16.5, and §16.7. Replace "a `lenny/request_elicitation` re-issuance that references an existing `elicitation_id`" with either (i) "an intermediate pod re-emits the MCP `elicitation/create` wire frame ([§15.2.1](15_external-api-surface.md#1521-restmcp-consistency-contract)) with divergent `{message, schema}`" (the MCP-native forwarding model, if forwarding is re-emission-on-the-wire) or (ii) an explicit internal gRPC forwarding contract documented in §10 that carries `elicitation_id` as a first-class field.
- **Option B:** Extend the §8.5 `lenny/request_elicitation` tool schema to include `title`, `description`, `inputs`, and an optional `elicitation_id` (for forward-hop re-issuance), and document the forward-hop-vs-originator distinction explicitly. This is a larger contract change that also requires reconciling with the MCP `elicitation/create` standard wire fields.
- **Option C:** Introduce a new platform MCP tool (e.g., `lenny/forward_elicitation`) with an input schema that takes `elicitation_id` plus the forward-specific fields, make §9.2 reference it explicitly, and update §8.5 to document it alongside `lenny/request_elicitation`.

Option A is smallest and aligns §9.2 with the existing tool schema. The iter7 fix commit's docs-sync updates (`docs/runtime-author-guide/platform-tools.md:264`, `docs/reference/metrics.md:251`) also use the `{title, description, schema, inputs}` vocabulary and should be resynchronized in the same pass.

**Why Medium (not Low):** The contradiction is internal to §9 (the paragraph asserts a mechanism unreconcilable with §8.5 two sections later), not an external-reference-vs-authoritative gap. An implementer reading §9.2 alone cannot build the gateway-origin-binding check — they must guess which field set to canonicalize. Per the iter4/iter5 rubric, this rises above Low documentation completeness because the invariant is safety-critical (it underpins the `ElicitationContentTamperDetected` critical alert and the `ELICITATION_CONTENT_TAMPERED` 409 rejection) and cannot be implemented as written. It does not rise to High because the gateway-origin-binding intent is clear and the runtime security posture is defensible under either field set once the implementer picks one.

**Why a regression (not a carry-forward):** The `{title, description, schema, inputs}` vocabulary and the "forwarding pod emits a `lenny/request_elicitation` re-issuance that references an existing `elicitation_id`" prose were both introduced by `bed7961`. The pre-fix §9.2 at `bed7961^` does not contain these terms (verified via `git show bed7961^:spec/09_mcp-integration.md | grep -iE "title|description|inputs"` — zero matches on elicitation content fields).

---

## 2. Consistency checks that passed (no regression)

The following cross-surface references introduced or amended by `bed7961` were verified and are internally consistent:

- **`elicitation_id` reference.** Used as a first-class identifier in the new paragraph; pre-existing in §9.2 (`respond_to_elicitation` authorization paragraph at line 96 already validated `(session_id, user_id, elicitation_id)`), in §15.1 (`/v1/sessions/{id}/elicitations/{elicitation_id}/respond` path), and in §15.1 `ELICITATION_NOT_FOUND` catalog row. The new paragraph does not redefine the identifier, only adds a new invariant about content bound to it.
- **`origin_pod` reference.** Pre-existing in the §9.2 provenance table at line 62. The new paragraph uses it consistently as "the pod that legitimately originated the elicitation".
- **`lenny_elicitation_content_tamper_detected_total` metric.** Defined at §16.1 line 64 with matching `{origin_pod, tampering_pod}` label set and cardinality justification. No regression.
- **`ElicitationContentTamperDetected` alert.** Defined at §16.5 line 434 with matching wire reference, `PERMANENT`/409 classification, and `increase(… [5m]) > 0` expression. No regression.
- **`elicitation.content_tamper_detected` audit event.** Defined at §16.7 line 664 with the same `origin_pod`/`tampering_pod`/`elicitation_id`/etc. payload shape. No regression on the audit-event existence — but the `divergent_fields`/`original_sha256`/`attempted_sha256` fields inherit the §9.2 field-vocabulary contradiction documented in SES-011 above.
- **`ELICITATION_CONTENT_TAMPERED` error code.** Defined at §15.1 line 1079 with matching `PERMANENT`/409/`details.elicitationId`/`details.originPod`/`details.tamperingPod` shape. No regression — but inherits the field-vocabulary contradiction from §9.2 (the row mentions `{title, description, schema, inputs}` in its prose).
- **Delegation hand-off rules.** The new paragraph does not contradict §8's delegation hand-off or lease rules. Delegation is unchanged; elicitation chains already traverse the delegation tree hop-by-hop per the pre-existing §9.2 diagram, and the new paragraph narrows behavior (intermediate pods forward by ID only) without altering delegation contracts.
- **`respond_to_elicitation` authorization paragraph.** The triple `(session_id, user_id, elicitation_id)` remains the authorization boundary for response routing; the new paragraph adds a content-integrity boundary at emission time but does not alter response-side authorization.
- **Client capability negotiation.** No change to `adapterCapabilities.supportsElicitation` semantics; the new paragraph applies regardless of whether the client's adapter advertises elicitation support.
- **`deep elicitation suppression` (depth >= 3).** Unchanged. The new paragraph applies orthogonally — intermediate pods that are allowed to suppress or dismiss still may; they merely cannot silently modify the rendered text.

---

## 3. Per-finding status vs. iter7

- **SES-011** (iter8 new regression from `bed7961`): **Fixed**, Medium severity. §9.2 content-integrity paragraph normalized to §8.5's `{message, schema}` tuple; forward-hop wire mechanism reframed to the native MCP `elicitation/create` frame per §15.2.1 per-kind wire projection. Propagated across §15.1, §16.1, §16.5, §16.7 and the four docs mirrors.

All prior SES findings (SES-001 through SES-010 from earlier iterations) are out of scope for iter8 per the regressions-only directive.

---

## 4. Counts

- Critical: 0
- High: 0
- Medium: 1 (SES-011 — new regression from `bed7961`)
- Low: 0 (carry-forwards out of iter8 scope)
- Info: 0

---

## 5. Convergence assessment

**Verdict:** **Not converged** — one Medium regression introduced by the iter7 fix commit.

**Rationale:** The iter7 fix commit (`bed7961`) introduced a content-integrity invariant whose field vocabulary (`{title, description, schema, inputs}`) and wire mechanism (forward-hop `lenny/request_elicitation` re-issuance carrying `elicitation_id`) cannot be reconciled with the authoritative `lenny/request_elicitation` tool schema in §8.5 (which defines only `{schema, message}` and has no `elicitation_id` input). The contradiction is concentrated inside §9 itself and propagates to §15.1, §16.1, §16.5, and §16.7 via derivative mentions. An implementer cannot produce a faithful canonicalization-and-hash comparison without guessing which field set is authoritative. The fix is small (Option A in the recommendation: normalize §9.2's vocabulary to `{message, schema}` and replace "`lenny/request_elicitation` re-issuance" with the MCP-native `elicitation/create` wire projection language already used in §15.2.1) and self-contained in §9.2 plus the four paired surfaces.

**Convergence blockers:** 1 Medium finding (SES-011).

**Recommendation for iter9 fix cycle:** Apply Option A from SES-011's recommendation, resynchronize `docs/runtime-author-guide/platform-tools.md:264` and `docs/reference/metrics.md:251` in the same pass. No cross-cutting implementation changes required; the edit is confined to the five spec sections (§9.2, §15.1, §16.1, §16.5, §16.7) plus two docs files.

---
