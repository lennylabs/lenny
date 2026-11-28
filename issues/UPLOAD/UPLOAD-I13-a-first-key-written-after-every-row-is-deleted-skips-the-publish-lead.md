---
id: UPLOAD-I13
status: planned
kind: build-gap
severity: low
requirements: []
change: 0182.S2
---

# A first upload-token key written after every row was deleted is active at once, so peers refuse its tokens until they reload

## Specification

§7.1 (Normal Flow), **`uploadToken` format and security properties.**, **Rotation with a publish lead**: every replica has loaded a key before any replica signs with it.

## Evidence

- `pkg/signingkeys/ring.go`, `rotateIfDue`: a ring whose last reload found no key writes slot 1 with `activeFrom` set to `now.Add(-r.lead)`, active at once, as `Open` does for an empty table.
- The replica that wins the slot signs with the new key immediately; a peer refuses those tokens with `401 UPLOAD_TOKEN_INVALID` until its next reload, up to one refresh interval, and every replica answers session creation with `503` until it holds a key.
- This was the third bullet of UPLOAD-I10. Step 0177.S4 fixed the first two (a retired key never signs again, and a rewrite never extends the replaced key) and documented the effect of deleting rows in `docs/operator-guide/security.md`, which tells operators to use the forced rotation instead; the empty-ring path is unchanged.
- Recorded from the general review of steps 0177.S3 and 0177.S4 on 2026-11-26.

## Gap

The publish lead does not hold for the first key written after an operator deletes every row of the `upload-token` purpose.

## Suggested resolution

Write the replacement first key one publish lead ahead when the ring has already been opened (the rows were deleted under a running fleet), keeping the backdated first key only for an `Open` that finds an empty table, and accept the minting gap of one publish lead. Or keep the behavior, since the documented procedure never deletes rows. This needs the owner's choice.

Change 0182 removes the key ring in step 0182.S2, under which this issue closes as obsolete. The issue stays `open` until the owner answers O1 and O3 of change 0182; under O1 (d) or (e) the ring stays and the choice above stands.

The owner answered on 2026-11-27: design C (decision 1 of change 0182, UC-1 (a)) and the retirement of the upload-token half of change 0177 (decision 3 of 0182, UC-3 (a)). Step 0182.S2 removes the key ring and closes this issue as obsolete.
