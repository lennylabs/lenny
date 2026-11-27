---
id: AUDIT-I2
status: closed
kind: build-gap
severity: high
requirements: []
change: 0177.S1
closed: fixed
closed-by: 0177.S1
---

# The redaction receipt signing key is generated per process and discarded, so a receipt signed before a restart or by another replica cannot be verified

## Specification

[§12.8](../../spec/12_storage-architecture.md#128-compliance-interfaces) (Compliance Interfaces), `DeleteByUser` step 14, **`RedactionReceipt` schema.**:

- "`signature` (bytea) — detached signature over the canonical JCS serialization of the tuple `(receipt_id, audit_event_id, tenant_id, sequence_number, original_hash, new_hash, erasure_job_id, legal_basis, redactor_identity, timestamp)`. Signed by the platform's KMS-held audit signing key (same key as [§11.7](../../spec/11_policy-and-controls.md#117-audit-logging) genesis-nonce signing in regulated profiles). External verifiers fetch the public key from the admin API and verify signatures before accepting a `redacted_gdpr` discontinuity as authorized."
- "`signature_kms_key_id` (text) — the KMS key identifier used to produce `signature`; recorded inline so a later key rotation leaves prior receipts independently verifiable."
- "Receipts are retained for `audit.gdprRetentionDays` in lockstep with the `gdpr.*` audit events they reference". [§16.4](../../spec/16_observability.md#164-logging) sets the default of `audit.gdprRetentionDays` to 2555 days.

Step 14 also states: "Chain verifiers that encounter a `redacted_gdpr` row without a signature-verifying `RedactionReceipt` MUST raise `broken` and fire `AuditRedactionReceiptMissing`".

## Evidence

- `pkg/storage/deadletterredaction/signer.go`, `NewKMSReceiptSigner`: the function draws a fresh 32-byte HMAC-SHA256 key from `crypto/rand`, seals it with `envelope.New(provider, kekAlias)` and discards the result (`if _, err := cipher.Seal(ctx, key); err != nil`), and returns an `HMACReceiptSigner` over the plaintext key. The sealed key is stored nowhere, so the key exists only in that process's memory and is lost when the process exits.
- `cmd/lenny-gateway/adminrouter.go`: the gateway builds the signer at start with `deadletterredaction.NewKMSReceiptSigner(context.Background(), w.kmsProvider, "platform:audit-redaction-signing", "boot")`. Every replica and every restart stamps the key id `boot` into `signature_kms_key_id`, so the column cannot tell two keys apart.
- The signature is an HMAC, a symmetric MAC. No public key exists for an external verifier to fetch, and no admin API route serves one.
- `pkg/audit/chain.go`: the chain verifier accepts a receipt whose `OriginalHash` matches and whose `Signature` is non-empty (`rcpt.Signature != ""`), and its comment states that "the minimal implementation does not verify the signature cryptographically; production wires KMS". The defect therefore causes no visible failure today: no code verifies a receipt signature.
- The §11.7 audit signing key that step 14 names has no counterpart in the code. `pkg/tenancy/tenantstore/pgstore` generates the per-tenant `genesis_nonce` and stores it unsigned, and no package signs it. §11.7 itself describes the genesis nonce and its delivery to the SIEM and does not mention signing it, so the parenthetical "same key as §11.7 genesis-nonce signing in regulated profiles" names a key that §11.7 does not define.
- `pkg/kms` (`Provider.Sign` and `Provider.PublicKey`) offers ES256 signing under a KMS-held key, which the §10.2 JWT signer uses through `jwt.LoadSigning`. The receipt signer does not use it.

## Gap

A receipt signed by one replica cannot be verified by another replica, and a receipt signed before a restart cannot be verified by any process after it, because the key that signed it no longer exists anywhere. Once a chain verifier checks signatures as step 14 requires, every receipt not signed by the verifying process fails, so every `redacted_gdpr` row reads `broken` and fires `AuditRedactionReceiptMissing`. Receipts have to verify for `audit.gdprRetentionDays`, and an external verifier has no public key to verify them with.

## Suggested resolution

Sign receipts under a key that every replica shares and that outlives every process, with the key identifier recorded per receipt so a rotated key still verifies older receipts. Decide whether that key is the KMS-held asymmetric key step 14 describes or a shared key ring, and state the decision in §12.8 and §11.7. The owner directed that this be solved together with [UPLOAD-I1](../UPLOAD/UPLOAD-I1-upload-token-keys-and-consumed-state-per-replica.md), reusing its mechanism where possible (Owner's direction in chat, 2026-11-25). Change [0177](../../changes/0177-shared-upload-token-keys/change.md) carries both, with the choice of key open as its decision O3. A chain verifier that checks the signature is part of the fix, because without it the defect has no test that fails.
