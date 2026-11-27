---
id: UPLOAD-I6
status: planned
kind: spec-defect
severity: low
requirements: []
change: 0177.S4
---

# The upload-token verifier accepts any expiry a token carries, so a leaked key mints tokens that verify until the key is deleted

## Specification

§7.1 (Normal Flow), **`uploadToken` format and security properties.**: the token "expires at `session_creation_time + maxCreatedStateTimeoutSeconds` (default 300 s)", and a replaced key verifies, after the next key becomes active, for the stale bound plus the longer of the overlap window and the token lifetime. §7.1 states no check of the expiry against the configured lifetime, and no procedure to retire a key early.

## Evidence

- `pkg/uploadtoken/uploadtoken.go`, `Verifier.Verify`: `if v.clock().After(parsed.Expiry) { return parsed, ErrExpired }`. The verifier compares the token's own expiry with the clock and nothing else.
- `pkg/signingkeys/policy.go`, `UploadTokenVerificationTime`, and `pkg/signingkeys/pgstore`, `Insert`: a replaced key's row is deleted at its successor's activation plus overlap + max(overlap, token lifetime). Step 0177.S2 lengthened this from 5 to 10 minutes at the defaults, and it grows with `--max-created-state-timeout-seconds`, which has no upper bound.
- `pkg/signingkeys` has no path that deletes a key or forces a rotation on demand.
- Found by the security review of step 0177.S2 on 2026-11-26.

## Gap

A holder of a leaked upload-token secret can mint a token for any session with an expiry as far ahead as it likes, and the token verifies on every replica until the key's row is deleted: up to a rotation interval plus the verification time. An operator has no documented way to retire a suspected key early.

## Suggested resolution

Have §7.1 require the verifier to refuse a token whose expiry lies further ahead than the configured token lifetime plus a clock-skew allowance, so a forged token gains nothing beyond the key's own verification time. Document, or add a command for, retiring a key early: delete its row and write the next key. This needs an owner decision because it adds a requirement to §7.1.
