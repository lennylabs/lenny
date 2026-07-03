---
layout: default
title: "audit-chain-gap"
parent: "Runbooks"
triggers:
  - alert: AuditChainGap
    severity: warning
components:
  - platform
  - audit
symptoms:
  - "an audit hash chain reports a missing sequence number or a hash mismatch, breaking §11.7 chain integrity"
  - "GET /v1/admin/audit-events returns a chainIntegrityReport with broken or gap_suspected rows"
tags:
  - audit
  - compliance
  - security
related:
  - audit-pipeline-degraded
  - audit-redaction-receipt-missing
---

# audit-chain-gap

An audit hash chain has a missing sequence number or a hash mismatch, breaking §11.7 chain integrity. The per-tenant audit chain is the platform's tamper-evidence mechanism: each row's hash includes the prior row's hash and the authoritative `sequence_number`, so any retroactive modification, deletion, or reinsertion breaks the chain for every subsequent row. A `chainIntegrity = broken` finding is one of the two critical-severity audit conditions on the platform. This runbook distinguishes a genuine break from the three authorized discontinuities (`rechained_post_outage`, `gap_suspected`, `redacted_gdpr`) and recovers each case.

## Trigger

The `AuditChainGap` alert fires when the §16.5 condition documented in the alert rule holds for its `for:` window. The condition tracks `lenny_audit_chain_verification_broken_total` (a broken segment surfaced by a verification query) and the periodic background chain sampler. See [Metrics Reference §Alert rules](../reference/metrics.html#alert-rules) for the exact PromQL.

## Diagnosis

The chain verifier reports per-row state via the `chainIntegrity` field with the §11.7 enumeration: `verified`, `broken`, `unchecked`, `rechained_post_outage`, `gap_suspected`, and `redacted_gdpr`. Resolve which verdict is present before remediating, because only `broken` and unreceipted `redacted_gdpr` are genuine tamper signals. Identify the affected tenant and the row range from the firing alert's labels and the gateway logs for the same window.

### Step 1 — Query the audit trail and read the chain-integrity verdict

The paginated response carries a `chainIntegrityReport` envelope tallying the per-row verdicts, so a single query classifies the discontinuity.

<!-- access: api method=GET path=/v1/admin/audit-events -->
```
GET /v1/admin/audit-events?tenantId=<tenant>&from=<rfc3339>&to=<rfc3339>&limit=500
```

Read the `chainIntegrityReport.summary` counts and the per-row `chainIntegrity` values to determine the verdict:

- **`rechained_post_outage`** — the range was rewritten by the §25.9 reconciliation pass after a Postgres outage. This is an expected, authorized discontinuity. Confirm an outage covered the range by cross-referencing `ops_postgres_outage_log`.
- **`gap_suspected`** — a `sequence_number` gap was detected. This is an advisory, non-alarming signal with two benign sources, distinguished by the `ops_postgres_outage_log` window and the `prev_hash` linkage across the gap. The first source is a period during which Postgres was unavailable: the gap bounds match an `ops_postgres_outage_log` window, and the deferred events are reconciled and re-stamped `rechained_post_outage`. The second source is a benign `nextval` rollback: an audit-write transaction consumed a per-tenant sequence value and then rolled back without committing a row while Postgres was available, which leaves no `ops_postgres_outage_log` window and an intact `prev_hash` chain across the gap. The audit query API reports the rollback case with `reason: "nextval_rollback"` rather than an outage window. It requires no reconciliation and no operator action, and it is never re-stamped `rechained_post_outage`. Only a gap accompanied by a non-linking `prev_hash` is tampering; treat that as `broken`.
- **`redacted_gdpr`** — a row was rewritten in place by the §12.8 `DeleteByUser` PII redaction step under GDPR Article 17. This is authorized only when the corresponding signed `RedactionReceipt` is present and its signature verifies; the verifier raises `broken` otherwise.
- **`broken`** — a mismatch not attributable to a known outage or a receipted redaction. Treat this as a potential tamper event and escalate.

### Step 2 — Confirm the outage boundary for a gap or rechain verdict

<!-- access: api method=GET path=/v1/admin/audit-events/summary -->
```
GET /v1/admin/audit-events/summary?tenantId=<tenant>&from=<rfc3339>&to=<rfc3339>
```

The summary groups counts by chain verdict so the operator can confirm the suspected range aligns with an `ops_postgres_outage_log` window. A gap whose bounds match an outage boundary is the expected `lenny-ops` deferred-write case rather than tampering.

### Step 3 — Recompute the chain independently for a broken verdict

For a `broken` verdict, recompute the chain against the canonical Postgres tuple. The raw-canonical endpoint returns the exact field set Postgres hashed over, so an auditor can recompute `SHA-256(prev_hash || canonical_tuple)` without trusting the OCSF rendering. The endpoint requires the `audit:raw-canonical:read` scope.

<!-- access: api method=GET path=/v1/admin/audit-events/{seq} -->
```
GET /v1/admin/audit-events/<sequence_number>?format=raw-canonical
```

Compare the recomputed hash against the stored `prev_hash` of the next row. If the platform has an external SIEM configured (`audit.siem.endpoint`), compare the Postgres rows against the independent SIEM copy: a database superuser can alter Postgres but cannot alter the SIEM stream, so a divergence localizes the tampered rows.

## Remediation

The remediation depends on the verdict identified in diagnosis.

1. **`rechained_post_outage`.** No action is required. The reconciliation pass reinserted the deferred `lenny-ops` events with their original timestamps and recomputed the chain for the affected range; the verdict records that the segment was legitimately rewritten. Confirm `audit_log_deferred_writes` has no stale, unreconciled rows for the range. See the audit-pipeline-degraded runbook if reconciliation has not completed.

2. **`gap_suspected` matching an outage boundary.** Allow the §25.9 reconciliation pass to complete; it inserts the buffered events and re-stamps the range as `rechained_post_outage`. The reconciliation is idempotent and tolerates partial completion, so a re-run is safe. If `audit_log_deferred_writes` holds rows that have not been reconciled, escalate to audit-pipeline-degraded and do not delete deferred rows manually.

3. **`gap_suspected` with no outage window (benign `nextval` rollback).** No action is required. A gap that no `ops_postgres_outage_log` window covers and whose `prev_hash` chain links across it is a benign `nextval` rollback: a transaction consumed a per-tenant sequence value and rolled back without committing a row, leaving an interior gap that every committed row still links across by `prev_hash`. The audit query API reports it with `reason: "nextval_rollback"`. Do not run the reconciliation pass and do not escalate. This case has no deferred rows to reconcile, so it is never re-stamped `rechained_post_outage`. It is distinct from an outage gap, which carries an `ops_postgres_outage_log` window and deferred events awaiting reconciliation.

4. **`redacted_gdpr`.** Verify the signed `RedactionReceipt` covering the discontinuity. When the receipt is present and its signature verifies, the discontinuity is authorized and no action is required. When the receipt is absent or its signature fails, treat the row as `broken` and escalate; a missing receipt for a redacted row is the audit-redaction-receipt-missing condition.

5. **`broken` (genuine tamper signal).** Do not attempt to rewrite the chain. Preserve the current state for forensic review, capture the divergence between Postgres and the SIEM copy, and escalate immediately. Chain repair on a confirmed tamper event is a compliance action that requires the on-call security engineer and the data-protection owner. An operator must not re-seal a broken chain, because re-sealing destroys the tamper evidence.

## Verification

- A re-query of `GET /v1/admin/audit-events` for the affected range returns a `chainIntegrityReport` with no `broken` rows.
- An outage-induced `gap_suspected` range reports `rechained_post_outage` after reconciliation completes.
- A benign `nextval` rollback gap remains `gap_suspected` with `reason: "nextval_rollback"`, no `ops_postgres_outage_log` window, and an intact `prev_hash` chain across the gap. It is not re-stamped `rechained_post_outage` and requires no further action.
- `lenny_audit_chain_verification_broken_total` stops advancing.
- The affected component's health-API row reports `healthy` again.

## Escalation

Page the on-call security engineer and the data-protection owner immediately for any `broken` verdict or any `redacted_gdpr` row whose `RedactionReceipt` is absent or fails signature verification. For an outage-attributable `gap_suspected` or `rechained_post_outage` verdict that does not clear after the reconciliation pass completes, escalate to the on-call platform engineer via the audit-pipeline-degraded path.
