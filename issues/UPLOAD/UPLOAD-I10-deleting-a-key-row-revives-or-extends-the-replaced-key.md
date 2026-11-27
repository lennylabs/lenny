---
id: UPLOAD-I10
status: open
kind: build-gap
severity: low
requirements: []
---

# Deleting a signing-key row by hand makes replicas sign with the replaced key, extends its life, or bypasses the publish lead

## Specification

§7.1 **`uploadToken` format and security properties**, the shared signing keys and their rotation (change 0177).

## Evidence

- `SigningKey` picks the newest key whose activation has passed and ignores `RetireAfter` (`pkg/signingkeys/ring.go:355-358`).
- `Insert` overwrites the previous slot's `retire_after` and `delete_after` unconditionally (`pkg/signingkeys/pgstore/pgstore.go:106-110`, `store.go:91-95`).
- The empty-ring path writes slot 1 active at once, backdated by the publish lead (`ring.go:241-245`), so peers refuse its tokens until they reload, and every replica answers 503 for one to two refresh intervals after all rows are deleted.
- None of these is reached in normal rotation. Each is reached when an operator deletes a row to retire a key early.
- Found by the adversarial review of 0177 open decision O8 on 2026-11-26.

## Gap

Deleting the newest row while the previous key is still within its verification time makes replicas sign with the replaced key again, and extends that key's verification time. Deleting every row causes a burst of 503 and 401 responses. §7.1 states none of these effects.

## Suggested resolution

Provide early retirement as a forced rotation through the normal write path, which keeps the publish lead, rather than as row deletion. Make `Insert` leave an earlier `delete_after` in place, and make `SigningKey` skip a key past `RetireAfter`.
