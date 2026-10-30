---
layout: default
title: "audit-redaction-receipt-missing"
parent: "Runbooks"
triggers:
  - alert: AuditRedactionReceiptMissing
    severity: critical
components:
  - audit
  - compliance
symptoms:
  - "an audit row classified chainIntegrity=redacted_gdpr has no matching signed RedactionReceipt"
  - "verification reports a missing or signature-invalid receipt for a redaction window"
tags:
  - audit
  - gdpr
  - compliance
  - redaction
requires:
  - admin-api
related:
  - audit-chain-gap
  - audit-grant-drift
  - audit-pipeline-degraded
---

# audit-redaction-receipt-missing

A GDPR-redacted audit row exists without a corresponding signed RedactionReceipt, so the §11.7 verifier cannot distinguish a legitimate redaction from a tamper.

## Trigger

`AuditRedactionReceiptMissing` — `increase(lenny_audit_redaction_receipt_missing_total[15m]) > 0`.

## Diagnosis

### Step 1 — Identify the affected window

<!-- access: api method=GET path=/v1/admin/audit-events -->
```bash
curl -sS "$LENNY_OPS_URL/v1/admin/audit-events?chain_state=redacted_gdpr&since=24h" \
  | jq '.events[] | {id, tenant_id, recorded_at, chain_state}'
```

Each row names the audit chain (`tenant_id`) and the time window that the redaction covers.

### Step 2 — Look for the receipt

<!-- access: api method=GET path=/v1/admin/redaction-receipts -->
```bash
curl -sS "$LENNY_OPS_URL/v1/admin/redaction-receipts?tenant_id=<id>&since=<recorded_at>" \
  | jq '.receipts[] | {id, signature_valid, window_start, window_end}'
```

A `signature_valid: false` or empty list confirms the row is unaccompanied.

## Remediation

1. **If the redaction was authorized but the receipt write failed:** locate the original erasure job in `/v1/admin/erasure-jobs` and re-issue the receipt for the same window — the §12.8 ledger preserves enough metadata to re-sign.
2. **If no erasure job exists for the redaction:** treat the row as a potential tamper. Quarantine the chain (do not delete the row), notify security and compliance, and follow [audit-chain-gap.md](./audit-chain-gap.md) for the broader §11.7 chain-integrity investigation.
3. After issuing the receipt, re-run `POST /v1/admin/audit/verify` against the chain and confirm `chainIntegrity=redacted_gdpr` resolves with the receipt attached.

## Verification

`lenny_audit_redaction_receipt_missing_total` is non-incrementing and the chain verifier reports no orphaned `redacted_gdpr` rows.

## Escalation

Page security and compliance for any unaccompanied redaction that was not driven by an authorized erasure job. Page platform engineering when receipt issuance fails repeatedly.

Cross-reference: [§11.7](../../spec/11_policy-and-controls.md#117-audit-logging), [§12.8](../../spec/12_storage-architecture.md#128-compliance-interfaces).
