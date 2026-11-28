# Client-chosen upload secret

Status: `Decided`, revision 3, 2026-11-27.

## Problem

The responses of `POST /v1/sessions`, `POST /v1/environments/{name}/sessions`, `POST /v1/sessions/{id}/derive`, and `POST /v1/sessions/{id}/replay` carry the [§7.1](../../spec/07_session-lifecycle.md#71-normal-flow) `uploadToken`, the credential that authorizes uploads and `FinalizeWorkspace` before the session starts. Three changes are stacked on that one value:

- Change [0173](../archive/2026-11-25-0173-idempotency-stored-credentials/change.md) stores a create response with a redaction reference in the token's place and rebuilds the token on an idempotent replay, which needs a deterministic token (0173 decision 2, IK-1 (a)).
- Change [0177](../0177-shared-upload-token-keys/change.md) shares the HMAC key ring through `platform_signing_keys` so that the rebuild and the verification work on any replica, with rotation, a publish lead, a stale bound, a forced rotation route, and its audit event.
- Change [0178](../0178-idempotency-keys-by-principal/change.md) decides which credential may trigger the rebuild (decision 8, the rotation chain).

Since 0177 decision 11, the session row records the token's SHA-256 digest and the verifier refuses a token whose digest differs. The HMAC no longer authenticates the token: it remains so that a replay can rebuild it, it is the only check of the token's expiry, and it stands behind the missing-row path of the verifier ([UPLOAD-I12](../../issues/UPLOAD/UPLOAD-I12-verifier-admits-a-token-when-the-session-row-is-missing.md)). The ring has open defects of its own ([UPLOAD-I3](../../issues/UPLOAD/UPLOAD-I3-upload-token-overlap-window-has-no-setting.md), [UPLOAD-I5](../../issues/UPLOAD/UPLOAD-I5-one-bad-signing-key-row-stops-every-replica.md), [UPLOAD-I13](../../issues/UPLOAD/UPLOAD-I13-a-first-key-written-after-every-row-is-deleted-skips-the-publish-lead.md), [UPLOAD-I14](../../issues/UPLOAD/UPLOAD-I14-replica-whose-key-reload-fails-refuses-valid-tokens.md), [UPLOAD-I16](../../issues/UPLOAD/UPLOAD-I16-a-planted-far-future-forced-row-refuses-every-forced-rotation.md), and [UPLOAD-I17](../../issues/UPLOAD/UPLOAD-I17-a-key-committed-on-an-ambiguous-commit-goes-unaudited.md)), and a replica whose ring is stale answers session creation with `503` (0177 decision 4).

When the client generates the upload secret and sends only its digest, no response carries a credential, a lost-response retry still works because the client holds the secret, and the idempotency record scope of 0178 needs no identity finer than the subject. The finalize audit row and the `422 IDEMPOTENCY_KEY_REUSED` details also stop carrying material derived from the secret ([UPLOAD-I19](../../issues/UPLOAD/UPLOAD-I19-the-finalize-audit-row-carries-the-upload-token-digest.md)).

This change is design C, which the owner chose for this change and change 0178 on 2026-11-27 (decision 1), the upload credential half; revision 11 and later of change 0178 carry the record scope and the replay admission. Decisions 2 to 4 withdraw or narrow the earlier answers this change reverses, decisions 5, 6, and 8 settle the adapters, derived sessions, and edge MCP tools, and decision 7 keeps the change outside the v1 program.

## Scope

The change covers:

- The field `uploadTokenDigest` on the routes and the tools that open an upload phase, its validation, its refusal everywhere else, and the refusal of a digest already in use.
- The marker of the upload phase on the session row, `sessions.upload_phase`, which every insert site writes.
- The removal of `uploadToken` from every response.
- The verifier of the upload secret on upload and finalize: the marker, the digest compare, the expiry from the row, the consumed mark, and the missing row.
- Every client that reads `uploadToken` today: the client SDKs, the CLI, the playground, the install smoke test, the edge MCP client tools until change 0138 removes them, the test fixtures, the OpenAPI document, and the documentation.
- The retirement of 0173's redaction and restore, and of the upload-token half of 0177: the key ring, its table, its rotation, its settings, its admin route, its `lenny-ops` tool, its audit event, and its runbook. Step 0179.S2 of change 0179 reduces the formal model (decision 4).
- §11.5 **Stored credentials.**, which reduces to the rule that a response carrying a credential is not stored.

It leaves out:

- The redaction-receipt half of 0177, which signs in the KMS and does not use the ring (0177 decision 5).
- `sessions.upload_token_consumed_at` and the consumed mark of 0177 decision 3, which stay.
- The idempotency record scope and replay admission, which change 0178 carries.
- The mid-session upload through `POST /v1/sessions/{id}/upload`, which checks the upload token although §7.4 authenticates it with the caller's bearer ([UPLOAD-I4](../../issues/UPLOAD/UPLOAD-I4-mid-session-upload-checks-the-upload-token.md)); US-4 keeps the order of the checks in `admitUpload`.
- The `lenny-ops` idempotency records ([APICONV-I16](../../issues/APICONV/APICONV-I16-ops-idempotency-replays-the-impersonation-bearer-by-subject.md)).

## Terms

| Term | Meaning |
| :-- | :-- |
| Upload secret | A string the client generates for one session, 43 to 128 characters from the base64url alphabet (`A`–`Z`, `a`–`z`, `0`–`9`, `-`, and `_`), which a client produces by encoding at least 32 random bytes. It takes the place of the §7.1 `uploadToken` and travels in the existing `X-Lenny-Upload-Token` header. |
| Upload digest | The lowercase hexadecimal SHA-256 of the upload secret's ASCII bytes, 64 characters. The client sends it as `uploadTokenDigest`, and the session row stores it in the existing column `sessions.upload_token_digest`. |
| Upload phase | The interval in which a session in `created` accepts uploads and `FinalizeWorkspace`, from creation until finalize or `maxCreatedStateTimeoutSeconds` (default 300 s). |
| Upload phase marker | The column `sessions.upload_phase`: `secret` for a session whose creation recorded an upload digest, and `none` for a session that has no upload phase open to a client. |
| Seed | Bytes a client persists so that a retry of a keyed create derives the same upload secret (US-5). |
| Key ring | The upload-token signing keys of 0177 in `platform_signing_keys`, loaded, refreshed, rotated, and swept by `pkg/signingkeys` for the `upload-token` purpose. |
| Restore | 0173's replacement of the token by a redaction reference in a stored response, and its rebuild under the original key on replay (`pkg/uploadtoken/redact.go`). |
| Design A, A2, V1, C, and D | The options of open decision O1 of revision 2, which decision 1 settles as design C: A keeps the rotation chain of 0178 decision 8 with the key ring and the restore, A2 is A without 0178 decision 11, V1 mints a random token again on replay under the chain, C is this change with 0178 from revision 11, and D removes the upload credential. |

## High-level design

A client that creates a session generates a random upload secret, keeps it, and sends the secret's SHA-256 digest in the create request. The gateway stores the digest on the session row, as it stores the digest of the token it mints today, marks the row as having an upload phase, and returns no token. To upload files or finalize the workspace, the client presents the secret in the `X-Lenny-Upload-Token` header. The gateway reads the row's marker, hashes the secret, compares the result with the row in constant time, checks the expiry the row records, and refuses a secret the first finalize consumed. A session created with no upload phase, such as a create-and-start session or a delegated child, is marked so, and its row accepts no secret.

Because the response carries no credential, an idempotent replay returns it as stored, and the gateway needs no deterministic token, no shared signing key, and no rotation. The key ring of 0177, its table, its route, its tool, its audit event, its runbook, and its settings are removed, and so is 0173's restore. A client that restarts between the create and its retry persists the secret with the idempotency key and the body, or derives the secret from a seed it persists, as the client SDKs do.

## Requirements

None by identifier. §7.1 is content of the placeholder pages SESS and UPLOAD, and §11.5 is content of the placeholder page APICONV.

## Current state

On `main` at `1ce7a1941`:

- Mint sites: `pkg/gateway/core/session/sessionserver/create.go` (`mintUploadToken`, used by create, environment create, `derive.go`, and `replay.go` for `POST /v1/sessions/{id}/replay`, which is not an idempotent path), and `create_and_start.go`, which mints a token already marked consumed (0177 decision 11, UPLOAD-I8).
- Rows with no upload token: a `lenny/delegate_task` child is inserted in `created` with an empty `upload_token_digest` (`pkg/gateway/platformtools/mcptools/mcptools_register.go`). The column is `TEXT NOT NULL DEFAULT ''`, and `upload_token_expiry` is a nullable `TIMESTAMPTZ` (`migrations/0001_initial_schema.up.sql`).
- Derived sessions: the derive response returns an `uploadToken` for uploads into the derived session (`derive.go`), and a derived row has no workspace plan, so its finalize binds the plan in the finalize body (`resolveFinalizePlan`, `finalize.go`). §7.1 **Transcript as downloadable artifact:** tells clients that they can add the previous transcript to a derived session.
- The edge MCP client tools (`pkg/gateway/platformtools/mcptools`): `lenny/create_session` returns `uploadToken` (`mcptools_register.go`) with its schema generated from the OpenAPI document (`generated_schemas.go` by `pkg/gateway/mcpfabric/mcpschemagen`); `lenny/upload_files` requires `uploadToken` in a hand-written schema, and `lenny/finalize_workspace` requires it in `GeneratedFinalizeWorkspaceInputSchema` (`client_tools.go`); `lenny/create_and_start_session` forwards its body to `POST /v1/sessions/start`. `seam.CreatedSession.UploadToken` (`pkg/gateway/seam/core.go`) carries the token to the MCP tool.
- The verifier: `verifyUploadTokenValue` (`pkg/gateway/core/session/sessionserver/upload_seam.go`) admits every token when no verifier is wired, runs the HMAC verify, admits the token when the row is missing (UPLOAD-I12), compares the digest in constant time, and refuses a consumed token. It never reads `upload_token_expiry`; the HMAC token carries the expiry. `UPLOAD_TOKEN_MISMATCH` (`403`) answers a token whose embedded session id differs (`pkg/gateway/uploadhandler/upload.go`, `pkg/api/errorclassify/errorclassify.go`), and GWSEAM-VerifyUploadToken (`spec/interfaces/gateway-seams.md`) lists `ErrUploadTokenMismatch` among its returns.
- Exposure of the digest: the finalize handler writes the row's digest into the detail of the audit event `session.finalize_workspace` (`finalize_handler.go`, `lifecycle.go`), whose code comment cites §16.6. No section of the specification lists the event: §16.6 is the operational events catalog, §16.3 names `session.finalize_workspace` only as a tracing span, and the generated events catalog `spec/catalogs/events.md` is empty. The `422 IDEMPOTENCY_KEY_REUSED` details carry `storedHash`, the hash of the stored request body (`pkg/idempotency/idempotencymw/idempotency.go`).
- The key ring: `pkg/signingkeys` is imported only by `pkg/uploadtoken/shared.go`, `pkg/gateway/api/admin/upload_token_keys.go` (with the rotator held in `tenants.go`), and the gateway wiring (`cmd/lenny-gateway/flags.go`, `token_signing.go`, and `wiring_fields.go`). `PurposeUploadToken` is its only purpose. Migrations 0205 (`platform_signing_keys`, and `sessions.upload_token_consumed_at`) and 0207 (`platform_signing_keys.forced`) hold its state. The chart names the alias `platform:upload-token-signing` only in the `kms.aliases` comment of `charts/lenny/values.yaml` and holds no IAM grant; `docs/operator-guide/security.md` tells the operator to grant the gateway's ServiceAccount `Encrypt` and `Decrypt` on the key. The gateway flags are `--signing-key-refresh-interval`, `--signing-key-publish-lead`, and `--upload-token-key-rotation-interval`. The admin route is `POST /v1/admin/upload-token-keys/rotate`, the `lenny-ops` tool `admin.rotate_upload_token_keys` (`pkg/ops/mcp/generated_tools.go`) proxies it, the audit event is `platform.upload_token_key_rotated`, and the runbook is `docs/runbooks/upload-token-key-rotation.md`. `pkg/api/openapi/openapi.json` carries the route and the `uploadToken` fields. The KMS stub `tests/testinfra/stubs/kms` serves the alias.
- The restore: `pkg/uploadtoken/redact.go` and the credential path of `pkg/idempotency/idempotencymw/gate.go`.
- The formal model of FV-2: `tests/formal/uploadtokens/`, built by step 0179.S1 of change [0179](../0179-upload-token-model/change.md).
- Clients: `sdks/client/go/lenny` (`client.go`, `types.go`, `upload.go`, and the MCP client's `CreateSession` in `mcp.go`) and `sdks/client/go/test-helper`; `sdks/client/python/lenny` (`client.py`, `types.py`) with its tests and test helper; `sdks/client/typescript/src` (`client.ts`, `types.ts`, `test-helper.ts`) with its tests; the CLI's `lenny session new` in `pkg/embedded/localcli/session.go`, which creates over REST without an idempotency key; `pkg/gateway/playground/ui/app.js`; and `pkg/ctlcli/install_smoke.go`, which creates a session through the Go SDK's MCP client and uploads nothing.
- Tests that read the token: `tests/security`, `tests/integration`, `tests/chaos/upload_token_keys_postgres_outage_test.go`, `tests/contract/rest_mcp_consistency`, `tests/contract/sdks`, `tests/contract/rest_idempotency`, `tests/contract/rest_sessions`, `tests/component/stores`, `tests/component/migrations`, `tests/e2e_kind`, `tests/load_local`, `tests/load_kind/scenarios/checkpoint_duration`, `tests/testinfra/sessiondriver`, `tests/testinfra/mcpcreate`, `tests/testinfra/lifecycleharness`, and `tests/docs`.
- On the unmerged branch `change/0177.S5-forced-key-horizon`, step 0177.S5 builds 0177 decision 14 (a horizon on the forced-rotation refusal, which settles 0177's O11) and files UPLOAD-I18. Neither the decision nor the issue is on `main`; decision 3 brings the branch's two records commits to the branch of revision 3, withdraws decision 14, and cancels step 0177.S5.

## Detailed design

### US-1 The digest and the upload phase marker

Step 0182.S1 adds the column `sessions.upload_phase TEXT NOT NULL DEFAULT 'none'` with a check constraint on its values, `secret` and `none`. The default is `none`, so an insert site that writes no marker opens no upload phase. Every insert site writes the marker:

| Route or tool | `uploadTokenDigest` | `upload_phase` |
| :-- | :-- | :-- |
| `POST /v1/sessions` | Required. | `secret` |
| `POST /v1/environments/{name}/sessions` | Required. | `secret` |
| `POST /v1/sessions/{id}/derive` | Required (decision 6). | `secret` |
| `POST /v1/sessions/{id}/replay` | Required, until change 0144 removes the route; the source session's digest is never copied. | `secret` |
| `lenny/create_session` (edge MCP client tool) | Generated or derived and kept by the tool, or the tool removed, as decision 8 states, until change 0138 removes the tool. | `secret` |
| `POST /v1/sessions/start` and `lenny/create_and_start_session` (create-and-start) | Refused. The row also carries the consumed mark. | `none` |
| `lenny/delegate_task`, and a child that a pod creates | Refused, so a parent runtime cannot set a child's digest. | `none` |

A row marked `secret` records the digest in `sessions.upload_token_digest` and `upload_token_expiry` set to the creation time plus `maxCreatedStateTimeoutSeconds`. Every other route and tool refuses a request that carries `uploadTokenDigest` with `400 VALIDATION_ERROR` naming the field in `details.fields[].field`, the format the v1 program's decision 8 fixes. That includes the arguments of the platform tools a pod calls.

### US-2 Validation of the digest

The gateway refuses a digest that is not 64 lowercase hexadecimal characters with `400 VALIDATION_ERROR`. It refuses a digest that the row of another session of the same tenant and the same user records while that session is in `created` or `finalizing` with `400 VALIDATION_ERROR`, `details.fields[].field` `uploadTokenDigest`, and `details.reason` `digest_in_use`, so a client that reuses one secret, or sends a constant, cannot make it valid on several sessions before start. The refusal names no session. A partial unique index on `sessions (tenant_id, user_id, upload_token_digest) WHERE upload_token_digest <> '' AND state IN ('created', 'finalizing')` enforces the refusal under concurrent creates, and the handler maps its violation to the same refusal. The predicate leaves out every row with no digest, so rows marked `none` never collide.

The index is per user. Uniqueness across users protects nothing, because every upload and finalize also needs the owner's bearer and the ownership check, and a tenant-wide index would tell one user that a live session of another user records a given digest. The idempotency middleware does not store the `digest_in_use` refusal (0178 IP-8), so a retry after the other session leaves `created` runs. An idempotent retry of the same create replays the stored response before the handler runs and is not refused.

### US-3 Responses

No response carries `uploadToken`, and no response echoes `uploadTokenDigest`. The `422 IDEMPOTENCY_KEY_REUSED` details drop `storedHash`, because the stored request body contains the digest. The audit event `session.finalize_workspace` drops the digest from its detail (UPLOAD-I19). The row keeps `session_id`, which identifies the same session the digest did, so an analyst correlates the finalize row with the uploads of the session, refused or admitted, through `session_id`, which the `session.upload` audit rows carry. No section of the specification lists the event today; §7.1 states that the finalize audit row carries no material derived from the upload secret, and the code comment that cites §16.6 cites §7.1.

### US-4 The verifier

Upload and `FinalizeWorkspace` run the verifier after the route's ownership check. For every wiring, the minimal gateway and the tests included, it:

1. Reads the session row, and refuses with `401 UPLOAD_TOKEN_INVALID` when the row is missing (UPLOAD-I12).
2. Branches on `upload_phase`:
   - `none`: refuses an upload and a finalize with `401 UPLOAD_TOKEN_INVALID`, whatever the header carries.
   - `secret`: runs steps 3 to 6.
3. Refuses a header that is absent, shorter than 43 or longer than 128 characters, or outside the base64url alphabet, with `401 UPLOAD_TOKEN_INVALID`.
4. Compares the SHA-256 of the header with the recorded digest in constant time, always, and refuses a mismatch with `401 UPLOAD_TOKEN_INVALID`.
5. Refuses with `401 UPLOAD_TOKEN_EXPIRED` when `upload_token_expiry` is NULL or the current time is after it.
6. Refuses with `410 UPLOAD_TOKEN_CONSUMED` when `upload_token_consumed_at` is set.

A secret presented for another session fails step 4, so `UPLOAD_TOKEN_MISMATCH` has no case left: it leaves the §15.1 error catalog, `pkg/api/errorclassify`, GWSEAM-VerifyUploadToken and `pkg/gateway/seam`, and the error classifiers of the client SDKs. The consumed mark of 0177 decision 3 stays, set in the `finalizing` → `ready` write.

`admitUpload` keeps running the verifier before the precondition check, so a mid-session upload through `POST /v1/sessions/{id}/upload` is still refused, which UPLOAD-I4 records: with `410 UPLOAD_TOKEN_CONSUMED` for a row marked `secret` after finalize, as today, and with `401 UPLOAD_TOKEN_INVALID` for a row marked `none`, where a create-and-start session answers `410` today.

### US-5 Clients

- **The client SDKs.** The Go, Python, and TypeScript client SDKs generate the secret, send the digest, keep the secret on the result of the create, and present it on upload and finalize.
  - Without a seed, the SDK draws 32 bytes from the platform's cryptographic random source (`crypto/rand` in Go, `secrets.token_bytes` in Python, and `crypto.getRandomValues` in TypeScript) and encodes them as base64url without padding, 43 characters.
  - With a seed and an idempotency key, the secret is the base64url encoding, without padding, of the 32-byte output of HKDF-SHA256 (RFC 5869) with the seed as the input keying material, no salt (which RFC 5869 defines as 32 zero bytes), and as `info` the UTF-8 bytes of `lenny/upload-secret/v1`, one zero byte, and the UTF-8 bytes of the idempotency key. A retry with the same seed and key derives the same secret, and so sends the same body.
  - The SDK refuses a seed shorter than 32 bytes, and a seed on a create without an idempotency key, with an argument error before any request.
  - The parameters: in Go, the request option `lenny.WithUploadSecretSeed(seed []byte)` beside `lenny.WithIdempotencyKey`, and the field `UploadSecret` of the create result; in Python, `RequestOptions(upload_secret_seed=...)` beside `idempotency_key`, and the attribute `upload_secret` of the result; in TypeScript, `RequestOptions.uploadSecretSeed` (a `Uint8Array`) beside `idempotencyKey`, and the field `uploadSecret` of the result.
  - The reference test vector, in the shared fixture `sdks/client/testdata/upload-secret-v1.json` that each SDK's unit test reads: the seed of the 32 bytes `0x00` to `0x1f` in order and the idempotency key `k1` give the secret `WNAYAkZXhQT_-2tH8lPtj8X0HJLJ_L-AUIHHL-WJ1wA` and the digest `892431293710413232fe3aa0c904db2334735bf6a6ee0494c1ea7464cc7f12ff`. These values were computed on 2026-11-27 with an implementation of RFC 5869 over Python's standard `hmac` and `hashlib`; step 0182.S1 confirms them with each SDK's HKDF before it records the fixture.
  - The Go SDK's MCP client, `MCPClient.CreateSession`, follows decision 8.
- **The CLI.** `lenny session new` (`pkg/embedded/localcli`) creates over REST without an idempotency key, so it draws a random secret through the Go SDK.
- **The playground.** `app.js` generates the secret with `crypto.getRandomValues` and hashes it with `crypto.subtle.digest`, which a secure context provides: HTTPS in production, where the playground's cookies are `Secure`, and `localhost` in development.
- **The install smoke test.** `pkg/ctlcli/install_smoke.go` creates through the Go SDK's MCP client, and keeps working as decision 8 states.
- **The edge MCP client tools.** `lenny/create_session`, `lenny/upload_files`, `lenny/finalize_workspace`, and `lenny/create_and_start_session`, their schemas, and the schema generator get the least work that keeps the tests passing, as decision 8 states: the tools generate or derive the secret and keep it as any other client does, or the tools that open or use an upload phase are removed. The builder of step 0182.S1 picks the cheaper path and records it as a default in the step's digest. Change 0138 removes the tools in any case.
- **Tests.** The contract, `e2e_kind`, `load_local`, `load_kind`, security, chaos, and integration fixtures, and the test helpers under `tests/testinfra`, generate a secret per session.

### US-6 Retirement of the restore and the key ring

Removed in step 0182.S2, under decisions 2 and 3:

- 0173's restore: `pkg/uploadtoken/redact.go`, the redaction path of `pkg/idempotency/idempotencymw/gate.go`, and §11.5 **Stored credentials.**'s redaction text, which becomes: a response that carries a credential is not stored, and the handler calls `idempotencymw.DoNotStoreCredential`. Step 0182.S1 leaves that paragraph as it is, because the restore code stays until 0182.S2 and has no input once no response carries a token.
- The key ring: `pkg/signingkeys`, the HMAC `Issuer`, `Verifier`, `KeyRing`, and `Rotator` of `pkg/uploadtoken` with `shared.go`, the wiring in `cmd/lenny-gateway`, the `503` on a stale ring, and the flags `--signing-key-refresh-interval`, `--signing-key-publish-lead`, and `--upload-token-key-rotation-interval`.
- The schema: two forward migrations, numbered when each step is built. Step 0182.S1's migration (0209 at the time of writing) adds `sessions.upload_phase` and the partial unique index of US-2. Step 0182.S2's migration (the next number, 0210 at the time of writing) drops `platform_signing_keys` with the `forced` column of 0207, and keeps `sessions.upload_token_consumed_at`; its down migration recreates the table empty. Migrations 0205 and 0207 are not edited.
- The chart: the `platform:upload-token-signing` alias in the `kms.aliases` comment, and the KMS stub's alias in `tests/testinfra/stubs/kms`.
- The admin surface: `POST /v1/admin/upload-token-keys/rotate`, `pkg/gateway/api/admin/upload_token_keys.go` and the rotator in `tenants.go`, the `lenny-ops` tool `admin.rotate_upload_token_keys`, the audit event `platform.upload_token_key_rotated`, the OpenAPI entries, and `docs/runbooks/upload-token-key-rotation.md` with its entry in `docs/runbooks/index.md`.
- The chaos test `tests/chaos/upload_token_keys_postgres_outage_test.go`.
- The specification: §7.1 **Cryptographic protection:**, **Shared signing keys:**, **Rotation with a publish lead:**, **Forced rotation:**, and **Key refresh failure:**; the §12.3 **Platform signing keys.** table; the §12.9 row for `platform_signing_keys`; the §15 admin route and its §15.1 row; and the audit event.
- The model under `tests/formal/uploadtokens/` is reduced by step 0179.S2 of change 0179 to the properties of decision 4, and FV-2 is narrowed to them.

### US-7 The specification

- §7.1, step 8 of the normal flow and **`uploadToken` format and security properties**: the client-chosen secret, its format, its digest, the routes of US-1 and the upload phase marker, the verifier of US-4, and the client's duty to persist the secret, or a seed, with the idempotency key; the bullets of US-6 removed in step 0182.S2. **Session scope binding:** states that the row binds the secret to its session, **Issued-token binding:** becomes the digest compare, and **Persistence:** states that the session row holds the digest and that no stored response holds the secret or a reference to it. The **Session isolation response (`sessionIsolationLevel`)** paragraph no longer names `uploadToken` beside `session_id`. **Transcript as downloadable artifact:** keeps the transcript upload into a derived session, which the client authorizes with the secret whose digest it sent on derive (decision 6).
- §7.1 states that the `session.finalize_workspace` audit row carries no material derived from the upload secret (US-3).
- §7.4 **Upload authorization:** names the secret.
- §11.5: the `422` details without `storedHash`, and `digest_in_use` among the refusals that are not stored; **Stored credentials.** in step 0182.S2, as US-6 states.
- §12.3: the column `sessions.upload_phase` and the partial unique index of US-2; §12.3 and §12.9 as US-6 states.
- §15 and §15.1: the request schemas of US-1, the responses without `uploadToken`, the `400` refusals of US-1 and US-2, and `UPLOAD_TOKEN_MISMATCH` removed.
- `spec/interfaces/gateway-seams.md`: GWSEAM-VerifyUploadToken returns no `ErrUploadTokenMismatch`, and the operation that creates a session for the MCP tool returns no upload token.

## Ordering

- Step 0182.S1 lands before or with step 0178.S3, which merges only with or after it (0178 "Ordering").
- Step 0182.S1 rewrites the verifier that step 0177.S4 built, and depends on it.
- Step 0182.S2 follows 0182.S1, because the ring stays in use until no route mints an HMAC token. The `503` on a stale ring and UPLOAD-I14 stop mattering when 0182.S1 lands, because nothing mints and the verifier runs no HMAC check; the ring's code and its other issues stay until 0182.S2.
- The adapters of change 0138 generate or derive the secret as any other client does (decision 5), so 0182.S1 lands before 0138's adapters ship; "RO-8" in 0138 states the adapter's duty.

## Worked example

`alice`'s SDK creates a session with `Idempotency-Key: k1` and a seed it keeps in its state store. It derives the secret `s-a` from the seed and `k1`, and sends `uploadTokenDigest` `d-a = sha256(s-a)`. The gateway writes `d-a` and `upload_phase` `secret` to the row of `sess_1` with an expiry 300 seconds ahead, and answers with the session id and no token. The response is lost, and the client process restarts. The SDK derives `s-a` again from the persisted seed and `k1`, retries with the same body, and the gateway replays the stored response. The SDK uploads two files with `X-Lenny-Upload-Token: s-a`; each request reaches a different replica, which reads the row and compares `sha256(s-a)` with `d-a`. Finalize sets `upload_token_consumed_at`, and a later upload with `s-a` answers `410 UPLOAD_TOKEN_CONSUMED`. A playground bearer of `alice` that sends `k1` with a body that carries its own digest `d-p` receives `422 IDEMPOTENCY_KEY_REUSED`, and its details hold no hash. `alice`'s second create sends `d-a` again by mistake while `sess_1` is still in `created`, and is refused with `400 VALIDATION_ERROR`, reason `digest_in_use`; `bob`, another user of the tenant, may send `d-a` and is not refused. A `lenny/delegate_task` child of `alice`'s session is written with `upload_phase` `none`, and a finalize of it with any header answers `401 UPLOAD_TOKEN_INVALID`.

## Limits, and alternatives rejected

- The gateway cannot check a secret's entropy from its digest. A weak or reused secret exposes that client's own pre-start window to holders of the same user's credentials, which is the exposure of any credential of the user after start (§10.2 rule 5). US-2 refuses a digest in use among the user's live sessions, and the documentation states the generation rule.
- The `digest_in_use` refusal tells a credential of the user that another live session of the same user records that digest. It tells nothing about another user's sessions.
- A client that restarts between the create and its retry and has neither persisted the secret nor used a seed retries with a new digest, receives `422 IDEMPOTENCY_KEY_REUSED`, and the first session holds a pod until its `created` deadline. The SDKs derive the secret from a persisted seed, and the client guide states the duty.
- A model-driven MCP client cannot compute a SHA-256. The edge MCP client tools generate or derive the secret themselves, or are removed, until change 0138 removes them (decision 8), and 0138's adapters generate or derive and keep the secret as any other client does (decision 5).
- Lenny prescribes no derivation, key, storage, or replay behaviour for an adapter (decision 5). The adapter's clients never hold the secret, so an upload or a finalize that one of them sends through the adapter is authorized for that client by its bearer and the route's ownership check, and by the secret the adapter presents.
- A `curl` user generates the secret and its digest with standard tools, which the client guide shows.
- Option (b) of 0173's IK-1, minting a random token again on replay and writing its digest to the row, also retires the ring and the restore without a client change, but it keeps a credential in the replayed response and so needs the chain scope of 0178 decision 8 (design V1, which decision 1 did not choose).
- Removing the upload token and authorizing uploads before start by the bearer and ownership (design D of O1) removes the most code and asks nothing of clients, and it drops creator-only uploads before start; decision 1 did not choose it.

## Specification and non-specification changes

- `spec/07_session-lifecycle.md`: §7.1 step 8, **`uploadToken` format and security properties** with **Persistence:**, the **Session isolation response** paragraph, **Transcript as downloadable artifact:**, and the finalize audit row; §7.4 **Upload authorization:**.
- `spec/11_policy-and-controls.md`: §11.5 **Stored credentials.**, the `422` details, and the refusals that are not stored.
- `spec/12_storage-architecture.md`: §12.3 the column `sessions.upload_phase` and the index of US-2, and **Platform signing keys.** removed; §12.9 the `platform_signing_keys` row removed.
- `spec/15_external-api-surface.md`: the create, environment create, derive, and replay request and response schemas; the admin route removed; §15.1 the refusals and `UPLOAD_TOKEN_MISMATCH` removed.
- `spec/16_observability.md`: `platform.upload_token_key_rotated` removed.
- `spec/interfaces/gateway-seams.md`: GWSEAM-VerifyUploadToken and the session-creating operation of the MCP tool.
- `spec/catalogs/`: the regenerated error, event, setting, and operation catalogs.
- `migrations/`: the migration of step 0182.S1 (the marker and the index) and the drop migration of step 0182.S2.
- `pkg/gateway/core/session/sessionserver`: the intake, the marker at every insert site, the refusal, the verifier, and the audit detail.
- `pkg/gateway/platformtools/mcptools`: the marker of a `lenny/delegate_task` child, the refusal of the field, and the edge MCP client tools and their schemas under decision 8.
- `pkg/gateway/mcpfabric/mcpschemagen`: the generated schemas under decision 8.
- `pkg/gateway/seam`: `CreatedSession.UploadToken` and `ErrUploadTokenMismatch` removed.
- `pkg/gateway/uploadhandler`: the header check and the error codes.
- `pkg/session/sessionstore` and its Postgres store: the column `upload_phase`.
- `pkg/idempotency/idempotencymw`: the restore path and `storedHash` removed.
- `pkg/uploadtoken` and `pkg/signingkeys`: removed except what the verifier keeps.
- `pkg/gateway/api/admin`, `pkg/ops/mcp`, `pkg/api/openapi`, and `pkg/api/errorclassify`.
- `cmd/lenny-gateway`: the wiring and the flags.
- `charts/lenny`: the alias comment.
- `sdks/client/go`, `sdks/client/python`, `sdks/client/typescript`, and the fixture `sdks/client/testdata/upload-secret-v1.json`; `pkg/embedded/localcli`, `pkg/gateway/playground/ui`, and `pkg/ctlcli`.
- `tests/`: the fixtures and helpers of US-5, `tests/testinfra/lifecycleharness`, `tests/testinfra/stubs/kms`, `tests/chaos/upload_token_keys_postgres_outage_test.go` removed, and `tests/formal/uploadtokens/`, which step 0179.S2 of change 0179 reduces (decision 4).

## Documentation

- `docs/api/rest.md` (client developer): `uploadTokenDigest` on each route of US-1, its format, the `400` refusals, the absence of `uploadToken` in responses, the header on upload and finalize, the error codes of US-4, `UPLOAD_TOKEN_MISMATCH` removed, and `uploadTokenDigest` required on derive.
- `docs/api/mcp.md` (client developer): the edge MCP client tools as step 0182.S1 leaves them under decision 8, either generating and keeping the secret or removed, until 0138 removes them.
- `docs/api/admin.md` (operator): the rotate route removed.
- `docs/client-guide/session-lifecycle.md`, `docs/client-guide/wire-format.md`, and `docs/client-guide/authentication.md` (client developer): how to generate the secret (at least 32 random bytes, base64url-encoded, 43 to 128 characters), compute the digest, keep the secret secret, persist it or a seed with an idempotency key and the body for a retry after a restart, and never reuse it; the seed derivation of US-5 with its test vector, for a client that does not use an SDK.
- `docs/client-guide/error-handling.md` and `docs/reference/error-catalog.md` (client developer): `UPLOAD_TOKEN_MISMATCH` removed, `401 UPLOAD_TOKEN_INVALID` for a session with no upload phase, `401 UPLOAD_TOKEN_EXPIRED` for an unset expiry, and the `400 VALIDATION_ERROR` reason `digest_in_use`.
- `docs/client-guide/sdk-examples/curl.md`, `go.md`, `python.md`, `typescript.md`, and `mcp-sdk.md` (client developer): the examples generate the secret or pass a seed; the `curl` example uses `openssl rand` and `sha256sum`.
- `docs/client-guide/webhooks.md` (client developer): the create example.
- `docs/tutorials/first-session.md`, `session-derive-replay.md`, `multi-tenant-setup.md`, `using-connectors.md`, and `agent-memory.md` (client developer): the create and upload steps, and the digest on derive.
- `docs/getting-started/concepts.md`, `docs/reference/glossary.md` ("Upload Token"), and `docs/reference/state-machines.md` (the `created` row, "Upload token issued") (every reader): the client-chosen secret in place of an issued token.
- `docs/operator-guide/security.md` (operator): the upload secret and what the gateway stores, the removal of the shared upload-token keys with the `Encrypt` and `Decrypt` grant on `platform:upload-token-signing`, the incident procedure, and the flags; what a weak secret exposes; and the receipt-key text, which stays.
- `docs/operator-guide/configuration.md` and `docs/reference/configuration.md` (operator): the three flags and the `platform:upload-token-signing` alias removed.
- `docs/runbooks/upload-token-key-rotation.md` (operator): removed, with its links and its entry in `docs/runbooks/index.md`.
- `docs/reference/metrics.md` and the audit reference (operator): `platform.upload_token_key_rotated` removed, and the `session.finalize_workspace` detail without the digest.
- Upgrade notes for the release (operator), in the operator guide:
  - Step 0182.S1: a token minted before the upgrade stops working at once, because US-4's format check refuses the `.`-separated HMAC token and the migration marks every existing row `none`; a session in `created` at the upgrade is recreated by its client.
  - Step 0182.S2: the migration drops `platform_signing_keys`. After it, remove the `platform:upload-token-signing` entry from `kms.aliases`, revoke the gateway ServiceAccount's `Encrypt` and `Decrypt` grant on that key, and schedule the key's deletion in the KMS, and remove the three flags from any deployment that sets them, because the gateway exits on an unknown flag.

## Tests

- Unit, `pkg/gateway/core/session/sessionserver` and `pkg/gateway/platformtools/mcptools`: each route of US-1 writes its marker, stores the digest where it takes one, and returns no token; create-and-start, `lenny/delegate_task`, and every other route refuse the field; a malformed digest and a digest in use by the same user are refused, and a digest in use by another user is not; the verifier refuses a missing row, a row marked `none` with any header, a missing header, a malformed header, a mismatch, an expired row, a row with no expiry, and a consumed row, with the codes of US-4, and admits the right secret on a row written by another replica; a derive stores its digest and marks its row `secret`, and the replay route requires a digest and never copies the source's. Cites §7.1 (Normal Flow) and §7.4 (Upload Safety).
- Unit, `pkg/idempotency/idempotencymw`: the `422` details hold no `storedHash`; a replay of a create returns the stored response byte for byte; a `digest_in_use` refusal is not stored. Cites §11.5 (Idempotency).
- Unit, the client SDKs: the secret has the length and alphabet of the Terms, the digest is its SHA-256, a keyed create derives the reference vector's secret from its seed and key, a short seed and a seed without a key are refused, and the Go SDK's MCP client follows decision 8. Cites §7.1.
- Component, `tests/component/migrations` and `tests/component/stores`: the migration of step 0182.S1 adds the column with its default and constraint and the index, and rolls back; the index refuses a second live session of one user with one digest under concurrent creates, and admits two rows of one user with no digest; the drop migration removes `platform_signing_keys`, keeps `upload_token_consumed_at`, and rolls back. Cites §7.1 and §12.3.
- Contract, `tests/contract/rest_sessions`, `tests/contract/rest_mcp_consistency`, and `tests/contract/sdks`: the request and response schemas, the refusals, the error codes, and the MCP tools as step 0182.S1 leaves them under decision 8. Cites §7.1 and §15.1.
- Contract, `tests/contract/rest_idempotency`: the stored record of each route of US-1 holds no `uploadToken` field and no value equal to the secret, and a replay returns no credential. This is step 0182.S1's own check; the static test of 0178 IP-8, which step 0178.S4 builds later, covers every stored route. Cites §11.5.
- Integration: upload on one replica and finalize on another with no shared key. Cites §7.1.
- Security, `tests/security`: a secret of one session is refused on another; a forged header is refused; a finalize of a delegated child or a create-and-start session with any header is refused; the finalize audit detail and the `422` details hold no digest; a parent runtime cannot set a child's digest; a replayed create carries no credential. Cites §7.1 and §11.5.
- The model under `tests/formal/uploadtokens/`, which step 0179.S2 of change 0179 reduces and reruns (decision 4).
