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
  - "an audit row rewritten by a GDPR erasure has no redaction receipt, or its receipt does not verify"
  - "a redacted audit row was rewritten again after its receipt was signed"
  - "the audit-events list reports chainIntegrity=broken on a row whose payload carries the redaction marker"
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

An audit row that a GDPR erasure rewrote in place has no redaction receipt whose signature verifies. Without a verifying receipt, the audit chain verifier cannot tell an authorized redaction from a tamper that cleared the row's payload, so it reports the row `broken`.

The erasure job writes one redaction receipt per rewritten row. The receipt is signed inside the KMS with the ES256 key that the `platform:audit-signing` alias names, and it records that key's identifier. The gateway's periodic integrity check counts a row as missing its receipt when the row has no receipt, when the receipt names another row or tenant, when the row's current content hash differs from the receipt's `new_hash`, or when the receipt's signature does not verify under a trusted key. The trusted keys are the keys that `platform:audit-signing` and `platform:audit-verify` name and the retired receipt keys that `audit.retiredReceiptKeyIds` lists. A receipt signed under a key outside that set counts as missing in the same way as an absent one.

## Trigger

`AuditRedactionReceiptMissing` fires on `increase(lenny_audit_redaction_receipt_missing_total[15m]) > 0`. The metric carries the `tenant_id` label of the affected tenant.

## Diagnosis

### Step 1: Identify the affected rows

List the tenant's audit events over the affected window and keep the rows the verifier reports `broken`:

<!-- access: api method=GET path=/v1/admin/audit-events -->
```bash
curl -sS -H "Authorization: Bearer $LENNY_ADMIN_TOKEN" \
  "$LENNY_GATEWAY_URL/v1/admin/audit-events?tenantId=<tenant_id>&since=<RFC3339 start>" \
  | jq '.items[] | select(.unmapped.lenny_chain.integrity == "broken") | {uid: .metadata.uid, time, chain: .unmapped.lenny_chain}'
```

A row whose payload carries the redaction marker (`"redacted": true`) and whose integrity is `broken` is a redaction without a verifying receipt. The verifier reports such a row `broken` wherever it sits in the chain, including the tenant's newest row, which no later row links to yet. A redaction-marked row with no receipt at all is reported `broken` by the chain verifier and counted by the periodic integrity check even when the tenant has no other receipt.

### Step 2: Compare the receipt's key with the trusted keys

List the keys the gateway trusts for receipts:

<!-- access: api method=GET path=/v1/admin/audit-receipt-keys -->
```bash
curl -sS -H "Authorization: Bearer $LENNY_ADMIN_TOKEN" \
  "$LENNY_GATEWAY_URL/v1/admin/audit-receipt-keys" | jq '.keys[].keyId'
```

Read the receipt's `signature_kms_key_id` from the `audit_redaction_receipts` table for the row's `tenant_id` and `sequence_number`. The outcome falls into one of these cases:

- **No receipt row exists.** The erasure job rewrote the row without committing a receipt, or the row was rewritten outside an erasure job.
- **The key identifier is not in the list.** The receipt was signed under a key that neither alias names and `audit.retiredReceiptKeyIds` does not list, such as a key two rotations old that was not added to the list, or under a key the platform never held.
- **The key identifier is in the list and the signature verifies.** The row's payload was rewritten after the receipt was signed, so the row's content hash no longer equals the receipt's `new_hash`. A row written back to its pre-redaction content falls in this case, and its payload no longer carries the redaction marker, so Step 1's filter on the marker does not list it; read `audit_redaction_receipts` for the tenant to find it.
- **The key identifier is in the list and the signature does not verify.** The signature does not cover the stored receipt, so a signed column was changed after signing or the signature was forged.

## Remediation

1. **A key rotation dropped the receipt's key.** Add the key identifier the receipt records to `audit.retiredReceiptKeyIds`, as the [Security guide](../operator-guide/security.md#redaction-receipt-signing-key) describes, and roll the gateway. Confirm that the key version still exists in the KMS and that the gateway ServiceAccount holds `GetPublicKey` on it, because the gateway does not start when it cannot read a listed key. Once the key is trusted again, the next integrity cycle stops counting the row.
2. **No receipt exists, the row was rewritten after its receipt, or the signature does not verify under a trusted key.** Treat the row as a potential tamper. Keep the row and the receipt row in place, notify security and compliance, and follow [audit-chain-gap](./audit-chain-gap.md) for the broader chain-integrity investigation. Find the erasure job that should have produced the receipt through `GET /v1/admin/erasure-jobs/{jobId}`; a receipt is written only by the erasure job, so a rewritten row with no matching erasure job did not come from Lenny.

## Verification

`lenny_audit_redaction_receipt_missing_total` stops increasing for the tenant, and the audit-events list reports the row `redacted_gdpr` rather than `broken`.

## Escalation

Page security and compliance for any redaction-marked row that no authorized erasure job produced, and for any receipt whose signature does not verify under a trusted key. Page platform engineering when the erasure job fails to sign receipts repeatedly, which points to the KMS rather than to the audit store.
