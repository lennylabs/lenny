---
id: UPLOAD-I14
status: planned
kind: spec-defect
severity: medium
requirements: []
change: 0182.S1
---

# A replica whose key reload fails refuses valid upload tokens signed with a newer key while it can still read the session row

## Specification

§7.1 (Normal Flow), **`uploadToken` format and security properties.**, **Shared signing keys**: "Every gateway replica signs and verifies with the same signing keys, so a token minted on one replica verifies on any other." **Rotation with a publish lead**: "so every replica has loaded a key before any replica signs with it." **Key refresh failure**: "A replica that cannot reach Postgres keeps signing and verifying with the keys it holds and does not rotate", and once its last successful reload is older than the overlap window "verification continues with the keys it holds."

## Evidence

- `pkg/signingkeys/ring.go`, `reload`: a failed unwrap of any key the ring does not hold yet fails the whole reload and leaves the snapshot and the reload instant unchanged. The unwrap calls the KMS through `EnvelopeSealer.Open`.
- `pkg/gateway/core/session/sessionserver/upload_seam.go`, `verifyUploadTokenValue`: `Verifier.Verify` checks the HMAC against the ring's keys before the session row is read, so a token whose key the ring lacks is refused with `uploadtoken.ErrInvalid`, which `writeUploadTokenError` answers with `401 UPLOAD_TOKEN_INVALID`.
- The TLA+ model of change 0179 (`tests/formal/uploadtokens/`), property `ReachableReplicaVerifies`, configuration `r2_default`. In the counterexample, replica r1's link to the KMS is cut while its link to Postgres works. Replica r2 forces a rotation, the new key becomes active one publish lead later, and r2 mints a token with it. From then on r1 can read the session row and refuses the token, because every reload fails on the unwrap of the new key. The same refusal follows a cut of r1's link to Postgres that spans the new key's write and activation, for up to one refresh interval after the link returns.
- Found by the model of change 0179 on 2026-11-27.

## Gap

The publish lead guarantees only that a replica whose reloads succeed holds a key before any replica signs with it. A replica whose reloads fail keeps serving uploads and finalizes for sessions whose rows it can read, and it answers `401 UPLOAD_TOKEN_INVALID`, a non-retryable refusal, for a valid token signed with a key published after its last successful reload. A cut of the KMS link alone keeps the refusal for the whole outage, and a cut of the Postgres link keeps it for up to one refresh interval after the link returns. §7.1 states that a token verifies on any replica, and its refresh-failure paragraph covers only a replica that cannot reach Postgres, where the row read fails as well.

## Suggested resolution

Open decision O3 of change 0179 states the options: a retryable `503` in place of the `401` when the HMAC check misses and the ring's last successful reload is at least one publish lead old; the same with one rate-limited reload on such a miss before the `503`; or a §7.1 statement of the limit. The model shows that a ring younger than the publish lead holds every key any replica signs with, so the `401` stays correct for a miss on such a ring.

Under design C, open decision O1 (a) of change 0182, step 0182.S1 removes the HMAC check from the verifier, which then reads only the session row, so this issue becomes moot when 0182.S1 lands, before step 0182.S2 removes the ring, and open decision O4 of 0182 asks whether 0179's O3 then closes as moot. The issue stays `open` until the owner answers O1 and O3 of change 0182 and O3 of change 0179; under O1 (d) or (e) of 0182 the options of 0179's O3 apply.

The owner answered on 2026-11-27: design C (decision 1 of change 0182, UC-1 (a)), the retirement of the key ring (decision 3 of 0182, UC-3 (a)), and FV-2 narrowed with open decision O3 of change 0179 moot (decision 4 of 0182, UC-4 (a), recorded as 0179 decision 3). Step 0182.S1 removes the HMAC check from the verifier and closes this issue as obsolete.
