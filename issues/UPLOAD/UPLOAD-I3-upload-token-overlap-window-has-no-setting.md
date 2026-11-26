---
id: UPLOAD-I3
status: open
kind: build-gap
severity: low
requirements: []
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
