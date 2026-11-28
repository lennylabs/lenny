---
id: UPLOAD-I3
status: planned
kind: build-gap
severity: low
requirements: []
change: 0182.S2
---

# The upload-token overlap window has no gateway setting

## Specification

§7.1 (Normal Flow), **`uploadToken` format and security properties.**: "the gateway keeps the previous key valid during a short overlap window (default: 5 minutes)". `.claude/rules/code-best-practices.md` ("Configuration and compatibility") requires a default to be overridable by a flag or config value.

## Evidence

- `cmd/lenny-gateway/token_signing.go`, `openUploadTokenKeyRing`: the verification time and the stale bound are both `uploadtoken.DefaultOverlapWindow`, with no flag. The rotation interval has `--upload-token-key-rotation-interval`.
- The gap predates step 0177.S1: the per-process rotator on `main` used the same constant with no flag. Found by the review of 0177.S1 on 2026-11-26.

## Gap

An operator cannot change the overlap window that §7.1 gives as a default.

## Suggested resolution

Add a gateway flag for the overlap window once open decision O5 of change 0177 settles how the overlap window, the stale bound, and the token lifetime relate.

Change 0182 (design C, open decision O1 of 0182) removes the key ring in step 0182.S2, under which this issue closes as obsolete. The issue stays `open` until the owner answers O1 and O3 of change 0182; under O1 (d) or (e) the ring stays and this resolution applies.

The owner answered on 2026-11-27: design C (decision 1 of change 0182, UC-1 (a)) and the retirement of the upload-token half of change 0177 (decision 3 of 0182, UC-3 (a)). Step 0182.S2 removes the key ring and closes this issue as obsolete.
