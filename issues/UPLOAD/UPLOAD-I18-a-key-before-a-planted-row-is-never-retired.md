---
id: UPLOAD-I18
status: planned
kind: build-gap
severity: low
requirements: []
change: 0182.S2
---

# The key before a row planted with a plain INSERT is never retired or deleted

## Specification

§7.1 (Normal Flow), **`uploadToken` format and security properties.**, **Rotation with a publish lead**: "A key's retirement instant is the activation instant of the key that replaced it." and "After the next key becomes active, a replaced key verifies for the stale bound plus the longer of the overlap window and the token lifetime [...] and the gateway then deletes its row." §12.3 **Platform signing keys.**: "A key's `retire_after` is the next key's `active_from`, and its `delete_after` is `retire_after` plus the purpose's verification time, after which the gateway deletes the row."

## Evidence

- `pkg/signingkeys/pgstore/pgstore.go` and `pkg/signingkeys/store.go`, `Insert`: the write of a key at slot `n` sets `retire_after` and `delete_after` only on the row at slot `n - 1`.
- A holder of the gateway's database credentials who inserts a row at slot `n + 1` with a plain `INSERT` (UPLOAD-I16) leaves the row at slot `n` with no `retire_after` and no `delete_after`.
- Since step 0177.S5, a forced rotation writes slot `n + 2` after such a row when the row is marked forced and activates more than one publish lead plus 1 s ahead. That write retires the planted row at slot `n + 1` and leaves slot `n` unchanged.
- `Ring.VerificationKeys` keeps a key whose `delete_after` is unset, and `DeleteExpired` deletes only rows whose `delete_after` is set, so the key at slot `n` verifies, and its row stays in `platform_signing_keys`, until an operator removes it.
- `docs/runbooks/upload-token-key-rotation.md`, Verification Step 2, waits for the count of rows at or below the noted slot plus one to reach zero, which it then never does.
- Found while building step 0177.S5 (the `PlantForced` action of `tests/formal/uploadtokens/UploadTokens.tla`), 2026-11-27.

## Gap

The key that was active when a database writer planted a row keeps verifying after the rotations that replace it, and the runbook's verification step does not finish. The issued-token binding still refuses every token that key did not issue, and each token it issued expires, so the key admits no new token. A holder of the database credentials can also clear `retire_after` or `delete_after` of any row directly, so the gap adds no capability to that holder.

## Suggested resolution

Either `Insert` sets `retire_after` and `delete_after` on every earlier row of the purpose whose values are unset or later (`rotation_slot < n` rather than `rotation_slot = n - 1`), so a gap in the retirement chain closes on the next write, or the runbook's escalation names a row below the noted slot whose `delete_after` stays unset as a sign of a write from outside the gateway. The first option changes a rule of §7.1 and §12.3 and needs a change.

Decision 3 of change 0182 (UC-3 (a), 2026-11-27) cancels step 0177.S5 and removes the key ring, `platform_signing_keys`, and the runbook in step 0182.S2, which closes this issue as obsolete.
