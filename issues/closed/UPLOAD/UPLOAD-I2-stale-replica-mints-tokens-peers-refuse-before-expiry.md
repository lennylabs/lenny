---
id: UPLOAD-I2
status: closed
kind: spec-defect
severity: low
requirements: []
change: 0177.S2
closed: fixed
closed-by: 0177.S2
---

# A replica that cannot refresh its signing keys can mint upload tokens that its peers refuse before the tokens expire

## Specification

§7.1 (Normal Flow), **`uploadToken` format and security properties.**: a replaced key "verifies for the overlap window after the next key becomes active", and a replica whose last successful reload "is older than the overlap window" stops minting. The token's TTL is `maxCreatedStateTimeoutSeconds` (default 300 s), and the overlap window defaults to 5 minutes.

## Evidence

- `cmd/lenny-gateway/token_signing.go`, `openUploadTokenKeyRing`: `UploadTokenPolicy(..., uploadtoken.DefaultOverlapWindow)` and `StaleAfter: uploadtoken.DefaultOverlapWindow`.
- `pkg/signingkeys/ring.go`, `SigningKey`: `if now.Sub(r.lastReload) > r.stale`.
- Found by the review of step 0177.S1 on 2026-11-26.

## Gap

A replica whose last reload was at t0 keeps signing with the old key until t0 + 5 minutes when a peer publishes the next key just after t0. The peers delete the old key at t0 + 7 minutes (publish lead plus overlap), and the tokens the stale replica minted are valid until t0 + 10 minutes, so for about three minutes the peers answer `401 UPLOAD_TOKEN_INVALID` to tokens that have not expired. The same holds on healthy replicas whenever `maxCreatedStateTimeoutSeconds` exceeds the overlap window.

## Suggested resolution

Open decision O5 of change 0177 lists the options; the recommendation lengthens a replaced key's verification time to the overlap window plus the stale bound and refuses a token TTL above the overlap window.
