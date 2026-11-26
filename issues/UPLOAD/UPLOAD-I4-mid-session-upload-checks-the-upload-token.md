---
id: UPLOAD-I4
status: open
kind: build-gap
severity: low
requirements: []
---

# The upload endpoint checks the upload token for a mid-session upload, which §7.4 authenticates with the caller's bearer

## Specification

§7.4 (Upload Safety), **Upload authorization:**: "Mid-session uploads (after `FinalizeWorkspace`) use the caller's bearer ([§10.2](10_gateway-internals.md#102-authentication)) instead; the `uploadToken` is not reissued."

## Evidence

- `pkg/gateway/uploadhandler/upload.go`, `admitUpload`: the token check (`h.verifyUploadToken`) runs before the §15.1 precondition check, so an upload to a `running` session through `POST /v1/sessions/{id}/upload` must present an upload token, and the token is consumed once the session finalized.
- Before step 0177.S1, the refusal came only from the replica that finalized the session, because the consumed record was per replica. With the consumed mark on the session row, every replica refuses it with `410 UPLOAD_TOKEN_CONSUMED`. Found by the review of 0177.S1 on 2026-11-26.

## Gap

A mid-session upload through the upload endpoint is refused for a token that §7.4 says it does not need.

## Suggested resolution

In `admitUpload`, skip the token check for a session the precondition table admits from `running`, and authenticate it with the caller's bearer through the ownership check that already precedes it, or route such uploads only through the mid-session upload endpoint and document it.
