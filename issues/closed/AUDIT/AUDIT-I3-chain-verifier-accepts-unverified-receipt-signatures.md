---
id: AUDIT-I3
status: closed
kind: build-gap
severity: high
requirements: []
change: 0177.S1
closed: fixed
closed-by: 0177.S1
---

# The audit chain verifier accepts a redaction receipt without verifying its signature

## Specification

§11.7 (Audit Logging), on the `chainIntegrity` value `redacted_gdpr`: "verifiers treat this as a valid discontinuity only when the corresponding receipt is present and signature-verified, and raise `broken` otherwise."

## Evidence

- `pkg/audit/chain.go`: the redacted-row checks accept a receipt when `rcpt.OriginalHash == row.Hash && rcpt.Signature != ""`, at both call sites, and verify no signature.
- Found while writing revision 2 of change 0177 on 2026-11-25, beside AUDIT-I2.

## Gap

Any non-empty value in a receipt's `signature` column makes a rewritten audit row read as an authorized GDPR redaction rather than a broken chain, so a database writer who rewrites a row and inserts a receipt with a made-up signature passes chain verification. AUDIT-I2 hides the gap today, because no receipt signature could be verified anyway.

## Suggested resolution

Verify the receipt signature against the key that `signature_kms_key_id` names, and report `broken` when it does not verify, as change 0177 "SK-6" states. Tests: unit (a receipt with a wrong signature, an unknown key id, and a valid one), and integration through a real erasure.
