---
id: UPLOAD-I5
status: open
kind: question
severity: low
requirements: []
---

# One unreadable row of platform_signing_keys stops every replica's key reload, and the application role may delete every row

## Specification

§7.1 (Normal Flow), **`uploadToken` format and security properties.**: "A replica loads the keys at startup and does not start when it cannot read or unwrap them", and a replica whose last successful reload is older than the overlap window stops minting. §12.3 (Postgres HA Requirements), **Platform signing keys.**, describes the table.

## Evidence

- `pkg/signingkeys/ring.go`, `reload`: a row with another algorithm or a secret that does not unwrap fails the whole reload, which leaves the snapshot and the reload instant unchanged.
- `migrations/0205_platform_signing_keys.up.sql`: `GRANT SELECT, INSERT, UPDATE, DELETE ON platform_signing_keys TO lenny_app`.
- Found by the security review of step 0177.S1 on 2026-11-26. The same review led the step to write a new first key when a reload finds the table empty.

## Gap

A single malformed or foreign row, written by anyone with the `lenny_app` credentials, makes every replica's reload fail, and five minutes later every replica answers session creation with `503`. The `lenny_app` role can also delete every row, which forces every replica onto a new key and refuses every outstanding token.

## Suggested resolution

Decide whether a reload skips, and alerts on, a row it cannot read when that row is not the newest key, and whether `lenny_app` keeps `DELETE` on the table or the sweep moves to a narrower role. Both trade the fail-closed startup rule of §7.1 against availability, so they are the owner's call.
