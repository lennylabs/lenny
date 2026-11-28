---
id: UPLOAD-I12
status: planned
kind: build-gap
severity: low
requirements: []
change: 0182.S1
---

# The upload-token check admits a token when the session row it re-reads is missing

## Specification

§7.1 (Normal Flow), **`uploadToken` format and security properties.**, **Issued-token binding**: the gateway refuses a token whose digest differs from the digest the session row records, and refuses every token for a row that records no digest.

## Evidence

- `pkg/gateway/core/session/sessionserver/upload_seam.go`, `verifyUploadTokenValue`: `case errors.Is(err, sessionstore.ErrNotFound): return nil`. A token whose signature verifies is admitted when the row read finds no row, and the caller's own lookup is relied on to report the missing session.
- `pkg/gateway/uploadhandler/upload.go`, `admitUpload`: `LookupSession` reads the row before the token check, which reads it again. A row hard-deleted between the two reads lets an upload proceed against the first read's row with a token that is not bound to any row, and the blob it writes is orphaned.
- Found by the security review of steps 0177.S3 and 0177.S4 on 2026-11-26.

## Gap

Every other path of the token check fails closed since step 0177.S4. The missing-row path admits a token that the issued-token binding never checked. Exploiting it needs a holder of a key's secret and a concurrent hard delete of the session row.

## Suggested resolution

Return `uploadtoken.ErrInvalid` on `sessionstore.ErrNotFound`. Finalize of a missing session then answers `401 UPLOAD_TOKEN_INVALID` rather than `404`, so the §7.1 and §15.1 text on that outcome and the tests that pin `404` change with it.

Step 0182.S1 of change 0182 makes the verifier fail closed on a missing row (US-4 of 0182). The issue stays `open` until the owner answers O1 of change 0182, the choice of design; under an answer other than (a) or (c), the resolution above applies on its own.

The owner chose design C on 2026-11-27 (decision 1 of change 0182, UC-1 (a)). Step 0182.S1 makes the verifier fail closed on a missing row and closes this issue.
