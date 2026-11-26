# Steps: 0177 Shared signing keys for upload tokens and redaction receipts

## Validation owed

None.

## Steps

### 0177.S1 Shared signing keys, consumed state, and receipt signing
- status: Planned
- lanes: [specification, code, documentation]
- depends-on: []
- closes: [UPLOAD-I1, AUDIT-I2, AUDIT-I3, AUDIT-I4]

No open decision touches the step: decisions 3, 4, and 5 settle the store (Postgres only), the behavior when the ring cannot be refreshed (stop minting, keep verifying), and the receipt key (KMS signing). §7.1 **`uploadToken` format and security properties**, §12.8 step 14 **`RedactionReceipt` schema.**, §12.3 for the key table, §12.9 for the classification of the signing keys, and the §15 admin route for the receipt public keys, as SK-5 and SK-6 in `change.md` state. Then the signing-key store in Postgres, keyed by purpose and serving the upload-token purpose, with rotation with a publish lead, refresh, and the sweep of each purpose's retired keys (SK-2), the shared consumed state (SK-3), the failure behavior (SK-4), the receipt signer that signs inside the KMS through `kms.Provider.Sign` in place of `deadletterredaction.NewKMSReceiptSigner`, the public-key lookup by key identifier in `pkg/kms`, and the chain verifier's check of the receipt signature (SK-6), the wiring in `cmd/lenny-gateway/token_signing.go` and `cmd/lenny-gateway/adminrouter.go`, and the unit, component, contract, load_local, integration, chaos, and security tests. Then `docs/operator-guide/security.md`, `docs/operator-guide/configuration.md`, `docs/operator-guide/audit-ocsf.md`, `docs/runbooks/audit-redaction-receipt-missing.md`, and `docs/api/admin.md`. The step closes [UPLOAD-I1](../../issues/UPLOAD/UPLOAD-I1-upload-token-keys-and-consumed-state-per-replica.md), [AUDIT-I2](../../issues/AUDIT/AUDIT-I2-redaction-receipt-signing-key-per-process.md), [AUDIT-I3](../../issues/AUDIT/AUDIT-I3-chain-verifier-accepts-unverified-receipt-signatures.md), and [AUDIT-I4](../../issues/AUDIT/AUDIT-I4-receipt-signing-key-reference-is-undefined.md) (decision 2). It lands before any milestone whose tests run more than one gateway replica (decision 1).
