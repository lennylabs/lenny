# Steps: 0177 Shared signing keys for upload tokens and redaction receipts

## Validation owed

None.

## Steps

### 0177.S1 Shared signing keys, consumed state, and receipt signing
- status: Planned
- lanes: [specification, code, documentation]
- depends-on: []
- closes: [UPLOAD-I1, AUDIT-I2, AUDIT-I3, AUDIT-I4]

Waits on open decisions O1, O2, and O3. §7.1 **`uploadToken` format and security properties**, §12.8 step 14 **`RedactionReceipt` schema.**, the §12 section for the chosen store, and, under O3 (a) or (c), the §15 admin route for the receipt public keys, as SK-5 and SK-6 in `change.md` state. Then the signing-key store keyed by purpose, with rotation with a publish lead, refresh, and the sweep of each purpose's retired keys (SK-2), the shared consumed state (SK-3), the failure behavior (SK-4), the receipt signer in place of `deadletterredaction.NewKMSReceiptSigner` and the chain verifier's check of the receipt signature (SK-6), the wiring in `cmd/lenny-gateway/token_signing.go` and `cmd/lenny-gateway/adminrouter.go`, and the unit, component, contract, load_local, integration, chaos, and security tests. Then `docs/operator-guide/security.md`, `docs/operator-guide/configuration.md`, `docs/operator-guide/audit-ocsf.md`, `docs/runbooks/audit-redaction-receipt-missing.md`, and, under O3 (a) or (c), `docs/api/admin.md`. The step closes [UPLOAD-I1](../../issues/UPLOAD/UPLOAD-I1-upload-token-keys-and-consumed-state-per-replica.md), [AUDIT-I2](../../issues/AUDIT/AUDIT-I2-redaction-receipt-signing-key-per-process.md), [AUDIT-I3](../../issues/AUDIT/AUDIT-I3-chain-verifier-accepts-unverified-receipt-signatures.md), and [AUDIT-I4](../../issues/AUDIT/AUDIT-I4-receipt-signing-key-reference-is-undefined.md) (decision 2). It lands before any milestone whose tests run more than one gateway replica (decision 1).
