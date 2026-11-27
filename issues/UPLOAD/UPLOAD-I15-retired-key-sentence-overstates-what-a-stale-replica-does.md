---
id: UPLOAD-I15
status: planned
kind: spec-defect
severity: low
requirements: []
change: 0177.S4
---

# The §7.1 sentence that a replica never signs with a key after its retirement instant does not hold for a replica that has not loaded the retirement

## Specification

§7.1 (Normal Flow), **`uploadToken` format and security properties.**, **Rotation with a publish lead**, as step 0177.S4 edits it on branch `change/0177.S3-S4-audit-tail-and-key-incidents` at `8785d86e9`: "A key's retirement instant is the activation instant of the key that replaced it, and a replica never signs with a key after its retirement instant, including when the row of the replacing key no longer exists." The same paragraph also states: "A replica whose last successful reload is as old as the stale bound can still sign with the replaced key".

## Evidence

- `pkg/signingkeys/ring.go`, `SigningKey` (0177.S4): a key is skipped once the `RetireAfter` that the replica's snapshot holds has passed. A replica whose last successful reload predates the write of the replacing key holds no `RetireAfter` for the key and keeps signing with it until its snapshot is older than the stale bound.
- The TLA+ model of change 0179 (`tests/formal/uploadtokens/`): `MintNeverAfterHeldRetirement` holds in every configuration, and `StaleMintBounded` holds, so a replica that has not loaded a key's retirement instant signs with the key at most the stale bound minus the publish lead after that instant (3 minutes at the defaults). A scratch check of the model found the case: a replica whose link to the KMS is cut signs with the replaced key at its retirement instant.
- Found by the model of change 0179 on 2026-11-27.

## Gap

The first sentence reads as a rule about the retirement instant in the table, which a replica that has not reloaded since the replacing key was written does not know. The behavior is the one decision 7 of change 0177 designs for, and every token such a replica signs still expires before the key's deletion instant. Only the sentence overstates it.

## Suggested resolution

Reword the sentence in step 0177.S4's specification edit to "a replica never signs with a key after the retirement instant it has loaded for that key", and keep the stale-bound sentence that follows as the statement of what a replica that has not loaded it does.

Step 0177.S4 rewords the sentence on its branch, together with the matching sentence of `docs/operator-guide/security.md`.
