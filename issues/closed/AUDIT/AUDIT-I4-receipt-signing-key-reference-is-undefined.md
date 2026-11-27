---
id: AUDIT-I4
status: closed
kind: spec-defect
severity: medium
requirements: []
change: 0177.S1
closed: fixed
closed-by: 0177.S1
---

# The redaction receipt's signing key points to a §11.7 key that §11.7 does not define

## Specification

§12.8 item 14, **`RedactionReceipt` schema.**, column `signature`: "Signed by the platform's KMS-held audit signing key (same key as [§11.7](11_policy-and-controls.md#117-audit-logging) genesis-nonce signing in regulated profiles). External verifiers fetch the public key from the admin API and verify signatures before accepting a `redacted_gdpr` discontinuity as authorized."

## Evidence

- §11.7 describes the per-tenant `genesis_nonce` as generated with `crypto/rand` and stored in the tenant configuration record and the SIEM. It describes no signing of the nonce and no audit signing key, in regulated profiles or otherwise.
- No code holds an audit signing key, and no admin route serves a public key for receipts (change 0177 revision 2, "Current state").
- The receipt signer in `pkg/storage/deadletterredaction/signer.go` uses an HMAC key, which has no public key (AUDIT-I2).

## Gap

The specification names a key that does not exist and an admin route that is not defined, so an implementer cannot tell which key signs receipts or how an external verifier obtains it.

## Suggested resolution

Name the receipt signing key and its algorithm in §12.8, and define the admin route for its public keys if external verification stays, under the owner's answer to open decision O3 of change 0177, whose step 0177.S1 corrects the reference under every option.
