---
layout: default
title: Audit (OCSF wire format)
parent: "Operator Guide"
nav_order: 15
---

# Audit Logging and the OCSF Wire Format

{: .note }
> **Reviewing Lenny for security or compliance? Start at [Security Principles](security-principles) §1.6 and §3.** This page is the wire-format reference: OCSF v1.1.0 field mapping, hash-chain integrity, SIEM delivery configuration, and verifier guidance.

Lenny persists audit records in Postgres with a hash-chained append-only schema (see [Security](security.md#audit-logging)). Every record that leaves the Postgres hot tier — to an external SIEM, a pgaudit sink, a webhook subscriber, or an agent-operability audit query — is serialized as an [OCSF (Open Cybersecurity Schema Framework) v1.1.0](https://schema.ocsf.io/1.1.0/) JSON record. OCSF is the single wire format. There is no alternative format and no per-deployer format switch.

---

## Why OCSF

- **SIEM compatibility.** Splunk, Microsoft Sentinel, Google Chronicle, Panther, and others parse OCSF records natively, so no per-vendor connector is needed.
- **Audit portability.** OCSF is a vendor-neutral CNCF-adjacent standard, so audit data remains portable if you later change SIEMs.
- **Stable field names.** Lenny's internal schema can evolve without changing the external wire format.

---

## Field mapping (Lenny → OCSF)

Lenny's audit row columns and payload fields map onto OCSF class attributes deterministically:

| Lenny field | OCSF attribute |
|---|---|
| `id` | `metadata.uid` |
| `sequence_number` | `metadata.sequence` |
| `tenant_id` | `metadata.tenant_uid` |
| `created_at` | `time` (numeric epoch ms) |
| `event_type` | `class_uid` + `activity_id` |
| `user_id` | `actor.user.uid` |
| `caller_kind` (`human` \| `service` \| `agent`) | `actor.user.type` |
| `operation_id` | `metadata.correlation_uid` |
| `session_id` | `metadata.labels["lenny.session_id"]` |
| `policy_result` | `disposition` + `disposition_id` |
| `denial_reason` | `status_detail` |
| `resource_type`, `resource_id` | `resources[0].type`, `resources[0].uid` |
| `source_ip` | `src_endpoint.ip` |
| `user_agent` | `http_request.user_agent` |
| `prev_hash`, `chainIntegrity` | `unmapped.lenny_chain.{prev_hash,integrity}` |
| `genesis_nonce` (first entry only) | `unmapped.lenny_chain.genesis_nonce` |
| Remaining Lenny payload fields | `unmapped.lenny.*` |

The full class-uid/activity-id mapping is maintained in the repository's `schemas/ocsf-mapping.yaml` file and regenerated in CI from Lenny's event-type catalog.

---

## Hash-chain integrity across the wire format

Lenny computes the `prev_hash` chain over the **canonical Postgres tuple** (not over OCSF bytes). OCSF translation is a pure rendering step — it cannot affect chain integrity. External verifiers can either:

- Recompute the chain from `unmapped.lenny_chain.prev_hash` combined with the reversible OCSF fields, OR
- Fetch the raw canonical pre-OCSF tuple via `GET /v1/admin/audit-events/{id}?format=raw-canonical` (requires the `audit:raw-canonical:read` scope).

Chain integrity status is surfaced on every response envelope as `chainIntegrityReport` (`verified`, `broken`, `gap_suspected`, `rechained_post_outage`, `redacted_gdpr`) plus per-record `unmapped.lenny_chain.integrity`.

---

## Verifying redaction receipts

A GDPR erasure rewrites the payload of a dead-lettered audit row in place, which breaks that row's content hash. The erasure job writes a redaction receipt for each rewritten row, and the receipt's signature authorizes the break. A verifier accepts a `redacted_gdpr` row only when the row's receipt verifies; a row whose receipt is absent or does not verify is `broken`. The [Security guide](security.md#redaction-receipt-signing-key) describes the receipt signing key and its rotation.

A receipt is a row of the `audit_redaction_receipts` table, located by the redacted row's `tenant_id` and `sequence_number`. Its signed fields are `receipt_id`, `audit_event_id`, `tenant_id`, `sequence_number`, `original_hash`, `new_hash`, `erasure_job_id`, `legal_basis`, `redactor_identity`, and `timestamp`. The `signature` column holds the signature, and `signature_kms_key_id` names the KMS key version that produced it.

An external verifier checks a receipt in four steps:

1. Fetch the trusted public keys from `GET /v1/admin/audit-receipt-keys`, which requires the `platform-admin` or `tenant-admin` role. The response lists one entry per trusted key, each with its `keyId`, the algorithm `ES256`, and the public key as a JSON Web Key (`jwk`) whose `kid` equals the `keyId`.
2. Select the key whose `keyId` equals the receipt's `signature_kms_key_id`. A receipt whose key identifier matches no listed key does not verify.
3. Build the signed bytes: a JSON object of the ten signed fields, serialized with the RFC 8785 JSON Canonicalization Scheme. In that object, `original_hash` and `new_hash` are lowercase hexadecimal strings, `sequence_number` is a number, the identifiers are strings, and `timestamp` is an RFC 3339 UTC string with at most six fractional digits and no trailing zeros, such as `2026-11-26T09:00:00.123456Z`.
4. Verify the signature as ES256 over the SHA-256 digest of those bytes. The signature is 64 bytes: the big-endian `r` followed by the big-endian `s`, each 32 bytes.

```json
{
  "keys": [
    {
      "keyId": "arn:aws:kms:us-east-1:111122223333:key/1234abcd-12ab-34cd-56ef-1234567890ab",
      "alg": "ES256",
      "jwk": {
        "kty": "EC",
        "crv": "P-256",
        "x": "<base64url>",
        "y": "<base64url>",
        "kid": "arn:aws:kms:us-east-1:111122223333:key/1234abcd-12ab-34cd-56ef-1234567890ab",
        "alg": "ES256",
        "use": "sig"
      }
    }
  ]
}
```

---

## OCSF version

Current wire version is **OCSF v1.1.0**, advertised on every record via `metadata.version` and on every `/v1/admin/audit-events` response envelope via `ocsfVersion`. Version upgrades are coordinated releases — there is no runtime-selectable dual-version emission.

---

## SIEM delivery

Configure `audit.siem.endpoint` in your Helm values:

```yaml
audit:
  siem:
    endpoint: "https://siem.example.com/ingest"
    failureThresholdPercent: 5
    maxDeliveryLagSeconds: 30
```

The SIEM forwarder POSTs OCSF JSON records to this endpoint. At startup the gateway sends a test OCSF record and refuses to start until it is acknowledged.

Monitor delivery health via:

- `lenny_audit_chain_verification_broken_total` — detected tampering.
- `lenny_audit_chain_rechained_post_outage_total` — chain rewrites after `lenny-ops` deferred-write recovery (expected during outages).
- `AuditSIEMDeliveryLag` alert — fires when lag exceeds `maxDeliveryLagSeconds`.
- `AuditPartitionDropBlocked` alert — fires when the SIEM forwarder falls too far behind partition GC.

For regulated tenants (`complianceProfile: soc2 | fedramp | hipaa`) a configured SIEM endpoint is mandatory — the gateway refuses to start without one.

---

## Sample OCSF record (session terminated)

```json
{
  "metadata": {
    "version": "1.1.0",
    "uid": "01HNXZM8QR9K7SJQ5T2N3PV4WE",
    "sequence": 1847321,
    "tenant_uid": "t_acme",
    "correlation_uid": "op_8f3c2a",
    "labels": { "lenny.session_id": "sess_abc123" }
  },
  "time": 1745572800000,
  "class_uid": 6003,
  "activity_id": 4,
  "actor": {
    "user": {
      "uid": "user_alice",
      "type": "User"
    }
  },
  "disposition": "Success",
  "disposition_id": 1,
  "resources": [
    { "type": "session", "uid": "sess_abc123" }
  ],
  "unmapped": {
    "lenny_chain": {
      "prev_hash": "a3f1...c7e2",
      "integrity": "verified"
    },
    "lenny": {
      "terminated_by": "admin",
      "reason": "operator_initiated"
    }
  }
}
```

---

## Related

- [Security](security.md) — audit integrity controls, hash chaining, append-only grants.
- [Reference: Configuration](../reference/configuration.md) — `audit.*` Helm values.
- [CloudEvents catalog](../reference/cloudevents-catalog.md) — audit records travel as the `data` field of a CloudEvents envelope when delivered over the operational event stream.
