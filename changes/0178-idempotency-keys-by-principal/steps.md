# Steps: 0178 Idempotency keys scoped by principal

## Validation owed

An in-depth review of `pkg/ops/opsidem` against the gateway's idempotency design, which started on 2026-11-27 in answer to the owner's note on decision 19. Step 0178.S6 waits on it: the review completes the step's scope, and a finding that needs the owner's decision becomes an open decision of this change.

## Steps

### 0178.S1 Records scoped by principal
- status: Landed
- lanes: [specification, code, documentation]
- depends-on: []
- closes: [APICONV-I1]

No open decision touches the step. §11.5 **Mechanism.**, as revision 3 of `change.md` stated it (decisions 1 and 3). Then the subject and the session id in `idempotency.Key` and the store interface, the resolver for the REST middleware and the MCP hook, the migration of `idempotency_keys` (0206), and the unit, component, contract, load_local, and security tests. Then the idempotency sections of `docs/api/rest.md` and `docs/api/mcp.md`. The step closes [APICONV-I1](../../issues/closed/APICONV/APICONV-I1-idempotency-key-scoped-per-tenant-only.md), which 0138.S1 closed before this change took it over.

### 0178.S2 User erasure deletes the user's idempotency records
- status: Landed
- lanes: [specification, code, documentation]
- depends-on: [0178.S1]
- closes: [ERASE-I2]

§12.8 **Storage backends in erasure scope:** gains idempotency records, deleted by `(tenant_id, subject)`, and the **`DeleteByUser` dependency-ordered deletion sequence** and tenant deletion Phase 4 gain their steps, as IP-6 in `change.md` states (decision 2). Then `DeleteByUser` on the idempotency stores, the new store name in `pkg/storage/erasure/order.go`, the user eraser and the tenant eraser in the erasure wiring of `cmd/lenny-gateway`, and the tests: unit tests of the stores and the erasure orders, an integration test with a real erasure, and the erasure completeness check `TestErasureDeletesTheUsersRowsInEveryWiredStore_spec_12_8` extended to the new store. Then the GDPR erasure section of `docs/operator-guide/security.md`. The step depends on 0178.S1, because a record names no user until 0178.S1 adds the subject. It closes [ERASE-I2](../../issues/closed/ERASE/ERASE-I2-user-erasure-leaves-idempotency-records.md).

### 0178.S3 The idempotency scope by subject, source, session, and route
- status: Building
- lanes: [specification, code, documentation]
- depends-on: [0178.S1, 0182.S1]
- closes: [APICONV-I2, APICONV-I3, APICONV-I5, APICONV-I7, AUTHN-I2, PLAY-I5]

The branch's build of decisions 8, 10, and 11 is reworked to the record scope of IP-1, IP-2, IP-3, and IP-4, as IP-9 in `change.md` lists, under decisions 12, 13, 15, and 16, which narrow decision 8 and withdraw decisions 10 and 11. The step merges to `main` only with or after step 0182.S1 ("Ordering" in `change.md`).

First the specification, as IP-5 states: §11.5 **Mechanism.** states the scope by subject, source, session dimension, route, path parameters, effective audience, and key, the attribution columns, and that the effective audience protects no credential of a subject from another, and drops **Retry across a credential refresh.**; §16.1 lists `lenny_idempotency_replay_other_credential_total`; `GWSEAM-RequestCaller` names the source and the session dimension; and the branch's edits of §13.3, §10.2, §17.4, §17.8.1, §25.4, and §27.3.1 for the rotation chain and the client claim are reverted, while §10.2 keeps the clearing of `act`. Then the code, mostly by deletion: the `rch` claim and its mint sites, `jwt.ChainOf`, the client-claim verifier, `authmw.Principal.Chain`, the flag `--oidc-client-id-claim`, the chart value `auth.oidc.clientIdClaim` and its schema, and `principal_chain_required` are removed with their tests; the session dimension is filled from the calling session of an `agent` call and from `Principal.SessionID` otherwise; migration 0208 gains `stored_by_jti` and `stored_by_client` in place; the stores write them, and the middleware and the MCP hook count a replay by another `jti`. The tests: a retry after any refresh replays; a delegation child with a session id and the pod's own call under that session file apart; one key on two routes or two sessions runs twice; the attribution and the metric, at the unit, component, contract, and security tiers, each isolation case with its mutation check. Then `docs/api/rest.md`, `docs/api/mcp.md`, `docs/client-guide/authentication.md`, `docs/operator-guide/security.md`, `docs/reference/configuration.md`, and `docs/reference/metrics.md`, as "Documentation" in `change.md` lists. APICONV-I3, APICONV-I7, and PLAY-I5 close because no stored response carries a credential once 0182.S1 has landed, and APICONV-I2, APICONV-I5, and AUTHN-I2 close as fixed. APICONV-I12 closed as obsolete with decision 16.

### 0178.S4 Replay admission by the route's authorization checks, the stored routes, and the refusals not stored
- status: Building
- lanes: [specification, code, documentation]
- depends-on: [0178.S3]
- closes: [APICONV-I6, APICONV-I8, APICONV-I13, APICONV-I14]

IP-7 and IP-8 in `change.md` (decision 18). §11.5 states the checks a replay runs on each kind of route, the checks it does not run again (state, quota, policy, body, and headers, so a finalize replay does not check the upload secret), the `404` of a replay whose session was deleted, the stored routes with the `/mcp` allow-list of `tools/call`, and the responses that are not stored, `digest_in_use` included; `spec/interfaces/gateway-seams.md` gains GWSEAM-AdmitRouteReplay and extends GWSEAM-AdmitToolReplay. Then the idempotency lookup of every stored REST route, the session-creating routes included, moves inside `pkg/gateway/api/pipeline` after `guard` in the client listener's gate, with the middleware outside the mux keeping only `/mcp`; the session core serves the replay checks of IP-7 through GWSEAM-AdmitRouteReplay, and GWSEAM-AdmitToolReplay gains `lenny/create_and_start_session` and the checks of `lenny/delegate_task`; the MCP hook and the JSON-RPC path of the middleware store only the `tools/call` requests of the allow-listed tools; a static test and a contract test fail on a credential in a stored response; and the gateway stores no `401`, no upload-token refusal, no `digest_in_use` refusal, and none of the refusals of decision 8 that can clear. The tests: a replay on a session route by a caller whom the permission gate or the ownership check refuses receives the refusal and leaves the record; a create replay by a caller without `manage_own_sessions`, outside the stored environment, or a playground caller outside `allowedRuntimes` is refused; a `lenny/delegate_task` replay by a caller that does not own the parent is refused; an invalidated or restricted bearer's replay of finalize, start, or resume is refused; a keyed call to a tool outside the allow-list, and a keyed `tools/list`, store nothing; a `401` provoked with a predicted key is not stored; a retry after a quota refusal runs; the outer middleware and the pipeline compute one scope digest; and an admitted replay returns the stored response, at the unit, contract, and security tiers. Then the replay and storage text of `docs/api/rest.md`, `docs/api/mcp.md`, and `docs/operator-guide/security.md`. The branch's build of decision 8's closed list, the refusals not stored, and GWSEAM-AdmitToolReplay is kept and extended. The step closes [APICONV-I13](../../issues/APICONV/APICONV-I13-idempotency-replay-skips-the-pipelines-permission-and-ownership-checks.md) and [APICONV-I14](../../issues/APICONV/APICONV-I14-mcp-read-tool-results-are-stored-and-replayed-without-the-tools-checks.md).

### 0178.S5 Self-rotation in the client SDK and CLI
- status: Cancelled
- lanes: [code, documentation]
- depends-on: [0178.S3]
- closes: []

Cancelled by decision 14 on 2026-11-27 (owner's answer to IP-15), which withdraws decision 9, because a retry replays after any refresh once the scope names no rotation chain. The step would have built decision 9: the Go SDK's token source (`sdks/client/go/lenny/auth.go`), every other client SDK under `sdks/`, and the CLI refresh a Lenny-issued token by self-rotation, an exchange of the token for itself with its full `audience` and `scope`, with tests that a refresh issues a token of the same lineage and that a refresh after `exp` or at the lineage depth limit fails with the error the client reports, and `docs/client-guide/authentication.md` states the refresh and its limits: before `exp` only, at most `auth.tokenLineageMaxDepth` links (TOKX-I26), and the revocation of the replaced token.

### 0178.S6 The `lenny-ops` idempotency records
- status: Planned
- lanes: [specification, code, documentation]
- depends-on: []
- closes: [APICONV-I16]

Decision 19 and IP-10 in `change.md`. The step starts after the in-depth review of `pkg/ops/opsidem` against the gateway's idempotency design reports ("Validation owed"); the review completes its scope. First the specification: §25.4 **Idempotency** states that a record is matched on the key, the caller, the endpoint, and a SHA-256 of the request body, that a mismatch answers `422 IDEMPOTENCY_KEY_REUSED`, that a response carrying a credential is not stored, and that a replay through the MCP bridge runs the bridge's scope gate first. Then `pkg/ops/opsidem` and its Postgres store compare the endpoint and the body hash, with a migration of `ops_idempotency_keys` for the hash; the impersonation proxy and every tool or route whose response carries a token mark the response, and `opsidem` releases the claim rather than storing it; and the replay path of `/mcp/management` runs the scope gate of `pkg/ops/mcp` before it returns a stored response. The tests of IP-10 at the unit, component, and contract tiers. Then the "Idempotency" section of `docs/operator-guide/agent-operability.md`. The step depends on no other step of this change and no answer of change 0182, and closes [APICONV-I16](../../issues/APICONV/APICONV-I16-ops-idempotency-replays-the-impersonation-bearer-by-subject.md).
