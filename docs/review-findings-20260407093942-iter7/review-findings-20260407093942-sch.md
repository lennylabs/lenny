# Technical Design Review Findings — 2026-04-07 (Iteration 7, Perspective 18: Content Model, Data Formats & Schema Design)

**Document reviewed:** `docs/technical-design.md`
**Review perspective:** Content Model, Data Formats & Schema Design
**Iteration:** 7
**Category prefix:** SCH (starting at 034)
**Scope:** Critical, High, Medium only. Regressions, new issues, and incomplete fixes only — previously Fixed or Skipped findings not re-reported (SCH-029 Skipped status confirmed unchanged).

Prior SCH findings status (iter1 through iter6):
- SCH-001–018: iter1. SCH-001 Fixed, SCH-002–SCH-003 Fixed, SCH-004–SCH-008 Fixed, SCH-009–SCH-013 carried; SCH-014–SCH-018 Low/Info, not in scope.
- SCH-019–021: iter2, all Fixed.
- SCH-029: iter4, Skipped.
- SCH-030 (over-run semantics): Fixed — §8.3 item 5 documents hard-cap in proxy mode vs soft-cap in direct mode with zero-floor return.
- SCH-031 (capabilityInferenceMode): **Incomplete** — warning log added, but default remains `admin` and `capabilityInferenceMode` field was not added. Re-reported below.
- SCH-032 (billing sequence_number): Fixed — §11.2.1 has sequencing authority, provisional renumbering, and replay endpoint.
- SCH-033 (adapter manifest version/minPlatformVersion): Partially fixed — `minPlatformVersion` semver field added; adapter manifest `version` integer is deliberate (schema version, not runtime version). The integer schema version is appropriate for its purpose; this finding is considered resolved.

---

## Findings Summary

| Severity | Count |
| -------- | ----- |
| Critical | 0     |
| High     | 0     |
| Medium   | 3     |

All 3 findings are schema completeness gaps: one incomplete fix regression and two new gaps in webhook and billing event schemas.

---

## Detailed Findings

---

### SCH-031 `capabilityInferenceMode` Field Still Absent; Default `admin` Unchanged [Medium] — Incomplete Fix

**Section:** 5.1 (Capability Inference from MCP `ToolAnnotations`)

**Prior status:** Reported in iter4 as Medium. The iter4 recommendation was to (a) change the default inference from `admin` to `write`, and (b) add a `capabilityInferenceMode` field on `RuntimeDefinition` (`strict` keeps `admin`; `permissive` uses `write`). The fix applied only added a `WARN`-level log at registration time. The default is still `admin` and the `capabilityInferenceMode` field does not exist.

**Current state:** §5.1 line 1737 documents the warning log. The inference table at line 1732 still reads `No annotations → admin (conservative default)`. No `capabilityInferenceMode` field appears anywhere in the `RuntimeDefinition` schema.

**Residual risk:** Most third-party MCP tools omit annotations. Every unannotated tool is silently inferred as `admin`. When assigned to a pool without `admin` capability, the tool fails at call time with `TOOL_CAPABILITY_DENIED` — the registration-time warning is the only signal. Deployers who bulk-register third-party connectors with mixed annotation completeness cannot opt into a less destructive default without annotating every tool individually. The warning makes the failure observable but not preventable without per-tool configuration.

**Recommendation:** Add `capabilityInferenceMode` to `RuntimeDefinition` and `ConnectorDefinition` with two values:
- `strict` (default, current behavior): unannotated tools infer `admin`
- `permissive`: unannotated tools infer `write`

Add a `toolCapabilityOverrides` map as a bulk fallback so deployers can override per-tool capability without source changes. Document both in §5.1.

---

### SCH-034 Webhook Payload Schema Has Three Undocumented Gaps [Medium]

**Section:** 14 (`callbackUrl` field notes), 15.1 (`GET /v1/sessions/{id}/webhook-events`)

**Problem — three distinct gaps:**

**Gap 1: `data` field is only documented for `session.completed`.** The payload schema example (§14) shows one `data` shape:
```json
"data": {
  "usage": { "inputTokens": 15000, "outputTokens": 8000 },
  "artifacts": ["workspace.tar.gz"]
}
```
The five other event types — `session.failed`, `session.terminated`, `session.awaiting_action`, `delegation.completed` — have no documented `data` schema. Receiver implementors cannot know: for `session.failed`, does `data` carry the error code and message? For `session.terminated`, does it carry the termination reason (e.g., `coordinator_lost`, `store_unavailable`)? For `delegation.completed`, does it carry the child session ID, usage, and output? These are load-bearing fields for CI pipelines and billing integrations.

**Gap 2: `callbackSecret` field is missing from the WorkspacePlan schema and field table.** Section 14's `callbackUrl` note (line 5704) states "The signing secret is provided by the client at session creation (`callbackSecret` field, stored encrypted)." However, `callbackSecret` does not appear in the WorkspacePlan JSON example (§14) and is absent from the field notes table. Implementors reading the WorkspacePlan schema do not know this field exists, its type (string), or any constraints (min entropy, max length, accepted encoding).

**Gap 3: `X-Lenny-Signature` header format is undefined.** Section 14 states the signature is sent in `X-Lenny-Signature` using HMAC-SHA256. It does not specify:
- The header value format (e.g., `v1=<hex>`, `sha256=<hex>`, or `t=<timestamp>,v1=<hex>`)
- Whether a timestamp is included in the signed payload for replay protection
- The signed payload construction (e.g., `timestamp + "." + body`, raw body, JSON-canonical body)

Without this, webhook receivers cannot implement signature verification and are either left insecure (ignoring the signature) or must reverse-engineer the format from the SDK source.

**Recommendation:**
1. Add a per-event `data` schema table to §14 documenting the `data` fields for each event type (`session.completed`, `session.failed`, `session.terminated`, `session.awaiting_action`, `delegation.completed`).
2. Add `callbackSecret` to the WorkspacePlan JSON example and the field notes table with type `string`, constraints (16–64 character opaque string, base64 recommended), and note that it is stored encrypted and never returned in GET responses.
3. Document `X-Lenny-Signature` header format explicitly: recommended format is `t=<unix_timestamp>,v1=<hmac_hex>` where the signed payload is `<timestamp>.<body_bytes>`. Specify the timestamp replay window (e.g., 300 s). Document that the SDK `VerifyWebhookSignature(secret, header, body)` helper implements this.

---

### SCH-035 BillingEvent Flat Schema Has No Null/Absent Field Contract Per Event Type [Medium]

**Section:** 11.2.1 (Event schema, all events)

**Problem:** The billing event schema table (§11.2.1) is a flat, all-events table where event-type-conditional fields are annotated inline with "(for X events only)" but have no explicit null/absent/zero semantics for other event types. Specifically:

- `corrects_sequence` is typed `uint64` with no nullable marker. For non-`billing_correction` events, uint64 cannot represent "absent" — the value `0` is a valid sequence number at tenant initialization. An analytics consumer reading this field cannot distinguish "this event does not correct anything" from "this event corrects sequence 0".
- `credential_pool_id` and `credential_id` are typed `string`. For non-`credential.leased` events, the table does not state whether these are empty string, absent (field not in JSON), or `null`.
- All six event-type-conditional fields (`corrects_sequence`, `correction_reason_code`, `correction_detail`, `parent_isolation`, `target_isolation`, `matched_policy_rule`, `pool_name`, `pool_isolation`, `conflicting_pool_name`, `conflicting_isolation`, `revoked_by`, `revocation_reason`, `leases_terminated`) have the same ambiguity.

This is a concrete billing-specific instantiation of the general SCH-018 (null vs absent semantics) finding, which was filed at Info level in iter1 and has not been addressed. For billing records specifically, this is Medium severity: analytics consumers and third-party billing exporters that query the `EventStore` cannot write portable readers without trial-and-error against actual database rows.

**Recommendation:** Add a clarifying sentence after the event schema table:

> "Event-type-conditional fields are **absent** (field not present in the JSON payload or database row) when the event type does not use them. Consumers MUST treat absent uint64 fields as `0` for non-correction context, and absent string fields as empty string. The `corrects_sequence` field is absent (not `0`) for all event types except `billing_correction`. Analytics consumers MUST filter on `event_type = 'billing_correction'` before reading `corrects_sequence`."

Additionally, add a "Required for event types" column to the schema table to make conditionality explicit per field.
