---
id: UPLOAD-I16
status: open
kind: question
severity: low
requirements: []
---

# A row planted with forced set and a far-future activation instant refuses every forced rotation

## Specification

§7.1 (Normal Flow), **`uploadToken` format and security properties.**, **Forced rotation**, as step 0177.S4 edits it for decision 13 of change 0177: "While the newest key is a key that a forced rotation wrote and that is not active yet, a forced rotation writes nothing and is refused with `409 INVALID_STATE_TRANSITION`".

## Evidence

- `pkg/signingkeys/ring.go`, `RotateNow`: the refusal condition is `newest.Forced && newest.ActiveFrom.After(now)`, with no upper limit on `ActiveFrom`.
- `pkg/signingkeys/ring.go`, `rotateIfDue`: a scheduled rotation writes nothing while the newest key is not active, so the scheduled path is blocked by the same row.
- A key that `RotateNow` writes is active one publish lead after the write, so a forced key whose `active_from` is more than one publish lead ahead was not written by the gateway.
- `docs/operator-guide/security.md` names the database credentials of the gateway among the accesses that expose a key, and tells operators not to delete rows of `platform_signing_keys` by hand.
- Found by the security review of decision 13 of change 0177 on 2026-11-27.

## Gap

A holder of the gateway's database credentials can insert or update the newest row with `forced = true` and an `active_from` far in the future. Before decision 13, a forced rotation wrote the next slot and retired such a row; now every forced rotation answers `409` until that instant, and the scheduled path is blocked as before. A database writer can already stop minting in other ways, such as a row that cannot be unwrapped (UPLOAD-I5), so the new harm is that the admin API no longer recovers from this one row. The runbook's escalation now names a `409` whose `details.activeFrom` is more than one publish lead ahead.

## Suggested resolution

Open decision O11 of change 0177 states the options: (a) refuse only while the pending forced key's `active_from` is at most one publish lead (plus a clock-skew margin) ahead, and otherwise write the next slot and log a warning; (b) keep the condition of decision 13 and rely on the runbook's escalation (row repair by hand). Recommendation: (a).
