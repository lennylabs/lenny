# Deviations: Lenny-issued bearer tokens verify only on the process that signed them

The implementor owns this file. It stays empty until an implementation records a departure from what the proposal states.

## Proposed: Tier-6 KMS signing tests skip through a categorized reason instead of a skip-register entry

**Status:** proposed

**Reported by:** step S12 (CODE-1 KMS seam: Sign, PublicKey, ErrThrottled, reference validation).

**What the proposal says:** Testing, "Tests that land with CODE-1", states that the tier-6 per-provider tests are "skip-registered in tests/registers/skip-reasons.yaml".

**What landed instead:** `requireSigningKey` in `tests/tier6_e2e_cloud/jwt_signing_kms_test.go` skips with a categorized reason, "blocked: T-17.5.2: <ENV> names no provisioned P-256 signing key version ...". No entry was added to `tests/registers/skip-reasons.yaml`.

**Why:** The step reports that `tests/registers/skip-reasons.yaml` is a baseline that only shrinks. `tests/tier0_static/skip_reason_classifier_test.go` accepts no new entries, and it accepts a skip whose reason opens with a TESTING.md §17.9 category. A "blocked:" reason that names T-17.5.2 records the skip and the cross-reference in the form the tier-0 gate accepts.

**What a later reader would otherwise get wrong:** A reader following the proposal text would look for the tier-6 KMS signing tests in `tests/registers/skip-reasons.yaml` and conclude they are unregistered or missing. The skip record is the categorized reason string in the test file itself.

## Proposed: Tier-6 KMS signing check verifies with ecdsa.Verify instead of LoadKeySet

**Status:** proposed

**Reported by:** step S12 (CODE-1 KMS seam: Sign, PublicKey, ErrThrottled, reference validation).

**What the proposal says:** Testing, "Tests that land with CODE-1", states that tier 6 must "sign with a real P-256 key and verify through LoadKeySet".

**What landed instead:** `assertSignVerifies` in `tests/tier6_e2e_cloud/jwt_signing_kms_test.go` resolves the provider through `providerflags.Resolve`, signs under `platform:jwt-signing`, and checks the r||s signature against the provider's own `PublicKey` with `ecdsa.Verify` (`kmstest.VerifyRS`).

**Why:** The step reports that `LoadKeySet` belongs to CODE-2, a later step that does not exist yet at S12. The tier-6 check can switch to `LoadKeySet` when CODE-2 lands.

**What a later reader would otherwise get wrong:** A reader following the proposal text would assume the tier-6 test exercises the `LoadKeySet` verification path end to end. As landed at S12, it verifies only against the provider's `PublicKey`, and `LoadKeySet` coverage at tier 6 depends on a later change to this test.

## Proposed: KeySet.Verify rejects a non-ES256 alg with the reason unsupported_alg, checked before key selection

**Status:** proposed

**Reported by:** step S13 (CODE-2 context-taking Sign, remote ES256 signer, KeySet, HMACSigner test-site ports).

**What the proposal says:** Non-spec changes, CODE-2 item 3, states that `KeySet.Verify` "rejects any alg other than ES256". The proposal names no `VerifyError` reason for that rejection.

**What landed instead:** `selectKey` in `pkg/auth/jwt/keyset.go` rejects a token whose alg is not ES256 with a new `VerifyError` reason, `unsupported_alg`. The alg check runs before the kid checks, so an HS256 or `none` token never reaches key selection.

**Why:** The step reports that callers and tests need a reason string to tell the alg rule apart from `signature_mismatch`, and the spec names none. Running the alg check first makes an algorithm-confusion token fail on the alg rule even when it carries a valid kid.

**What a later reader would otherwise get wrong:** A reader following the proposal text would not know that `unsupported_alg` exists as a `VerifyError` reason. That reader could expect a non-ES256 token to fail with `signature_mismatch` or with a kid-related reason. The landed code reports `unsupported_alg` for every non-ES256 token, including one that carries a valid kid.

## Proposed: Comments naming deleted JWKS code are rewritten in the CODE-10 step

**Status:** proposed

**Reported by:** step S15 (CODE-10 delete JWKS endpoint and rotation observer).

**What the proposal says:** The CODE-10 targets list no doc-comment edits outside the named files. CODE-12 item 5 later rewords the `jwtaudit` package doc comment, which still describes the rotation observer.

**What landed instead:** The step rewrote the `jwtaudit` package doc comment in `pkg/gateway/audit/jwtaudit/jwtaudit.go` so it describes only the CA observer. The step also removed the comments that named deleted code or the deleted route: in `cmd/lenny-gateway/token_signing.go`, the `RotatingVerifier` block comment named `JWKSHandler` and JWKS publication; in `cmd/lenny-gateway/adminrouter.go`, the `buildHTTPSurface` doc listed JWKS; in `cmd/lenny-gateway/config_parse.go`, the `envFlagDefault` example named `--jwks-publish`; and `tests/tier8_chaos/scaffolds_test.go` and `tests/tier3_contract/rest_auth/jwt_forgery_test.go` carried comments citing the published JWKS document.

**Why:** The step reports that without these edits the comments would name a deleted type, flag, or route. Each change is confined to comments.

**What a later reader would otherwise get wrong:** A reader following the proposal text would expect the `jwtaudit` package doc comment to change in CODE-12 and would find it already rewritten. That reader would also not expect comment edits in `token_signing.go`, `adminrouter.go`, `config_parse.go`, or the two test files as part of CODE-10.

## Proposed: auditscope system-write test swaps its sample event type

**Status:** proposed

**Reported by:** step S15 (CODE-10 delete JWKS endpoint and rotation observer).

**What the proposal says:** The step's work names the files to edit. `pkg/gateway/audit/auditscope/auditscope_test.go` is absent from the target list.

**What landed instead:** In `pkg/gateway/audit/auditscope/auditscope_test.go`, the system-write test now appends `platform.ca_rotated` where it previously appended `platform.jwt_signing_key_rotated`.

**Why:** The step reports that D18 drops `platform.jwt_signing_key_rotated`. The test used that event type only as a sample string, so the swap keeps the dropped identifier out of the tree. The test's behavior is unchanged.

**What a later reader would otherwise get wrong:** A reader following the proposal text would not expect `auditscope_test.go` to change in CODE-10. A search for `platform.jwt_signing_key_rotated` that returns nothing in that test does not indicate a lost assertion.

## Proposed: jwtaudit Option retargets to CAObserver and unread JWK symbols are deleted with the JWKS endpoint

**Status:** proposed

**Reported by:** step S15 (CODE-10 delete JWKS endpoint and rotation observer).

**What the proposal says:** CODE-10 deletes the JWT rotation observer and keeps `CAObserver`. It does not say what happens to the shared `Option` type, which was declared as `func(*Observer)`.

**What landed instead:** In `pkg/gateway/audit/jwtaudit/jwtaudit.go`, `Option` is now `func(*CAObserver)`, the `With*` functions take `*CAObserver`, and `NewCAObserver` no longer copies options through a proxy `Observer`. Beyond the step's listed targets, the step also deleted `JWKSet`, the `PublicJWK` interface, `KMSSigner.PublicJWK`, `RotatingVerifier.CurrentJWK` and `RotatingVerifier.PreviousJWKs`, `publicJWKOf`, and `sortJWKsByKid` from `pkg/auth/jwt/jwks.go`. `HMACSigner.PublicJWK` remains until the later HMAC deletion step.

**Why:** The step reports that removing the observer would otherwise leave a dead `Option` type and a proxy struct. The deleted `jwks.go` symbols had no remaining reader once `JWKSHandler` was deleted, and the dead-code rule requires removing them in the same change.

**What a later reader would otherwise get wrong:** A reader following the proposal text would expect `Option` to keep its `func(*Observer)` signature and the listed `jwks.go` symbols to survive CODE-10, and might schedule their removal in a later step. The reader would also not know that `HMACSigner.PublicJWK` is the one `PublicJWK` method still present and is left for the HMAC deletion step.

## Proposed: gateway mint sites already pass the request context before CODE-4

**Status:** proposed

**Reported by:** step S16 (CODE-4 gateway wiring: shared signer and key set).

**What the proposal says:** CODE-4 item 3 in the non-spec changes says that every gateway mint site changes to call `Sign(ctx, ...)` with the request context.

**What landed instead:** CODE-4 changed no mint-site code. `pkg/gateway/credentials/impersonation/service.go` (`Issue`), `pkg/gateway/externalapi/admintoken/admintoken.go` (`sign`), and `pkg/gateway/mcpfabric/playground/token.go` (`r.Context()`) already pass the request context, and the callers in `pkg/gateway/externalapi/admin` pass `req.Context()`. The step corrected only the doc comments at those sites.

**Why:** The step reports that an earlier step (CODE-2) changed `jwt.Signer` to take a context and updated every mint site, so item 3 was already complete when CODE-4 began.

**What a later reader would otherwise get wrong:** A reader following the proposal text would expect the CODE-4 diff to carry the mint-site context changes. That reader would find only doc-comment edits at those sites in CODE-4 and might conclude that item 3 was skipped, when the code change landed with CODE-2.

## Proposed: CODE-4 edits gateway flag help text and Embedded Mode verifier tests outside its targets

**Status:** proposed

**Reported by:** step S16 (CODE-4 gateway wiring: shared signer and key set).

**What the proposal says:** The CODE-4 targets do not list `cmd/lenny-gateway/flags.go`, `cmd/lenny-gateway/auth_seams_test.go`, or `cmd/lenny-gateway/embedded_audience_test.go`.

**What landed instead:** The `--bearer-trust-hmac-key-file` help text in `cmd/lenny-gateway/flags.go` now says "on top of the §10.2 JWT key set" where it previously said "Token Service signer". The tier-1 Embedded Mode verifier tests in `auth_seams_test.go` and `embedded_audience_test.go` now build their primary member from the production key-set construction where they previously used an HMAC signer.

**Why:** The step reports that the help text described the removed signer, and that with an HMAC primary the tests would continue to check a verifier composition that production no longer uses.

**What a later reader would otherwise get wrong:** A reader following the proposal text would not expect these three files to change in CODE-4. That reader might schedule the help-text correction or the test migration for a later step, or treat the changes as unrelated to the proposal.

## Proposed: CODE-4 adds tier-4 and tier-9 key-set tests the proposal does not describe

**Status:** proposed

**Reported by:** step S16 (CODE-4 gateway wiring: shared signer and key set).

**What the proposal says:** The step header lists tiers 0, 1, 2, 4, and 9. Under "Tests that land with CODE-4...", the non-spec changes list only tier-2 tests for the gateway: the in-process 503 mapping, which belongs to a later step, and the binary-exit test.

**What landed instead:** The step added a tier-4 peer-replica test (`tests/tier4_integration/gateway_keyset_peer_replica_test.go`) and a tier-9 bearer-path test (`tests/tier9_security/gateway_keyset_bearer_test.go`). The proposal does not describe either test for this deliverable.

**Why:** The step reports that its tier list requires tiers 4 and 9, and that these tests pin the corrected cross-replica outcome and key-set-only verification at those tiers. The step also reports that they partly overlap the later cross-process TEST-1 work.

**What a later reader would otherwise get wrong:** A reader following the proposal text would not expect tier-4 or tier-9 tests to exist after CODE-4. A reader implementing TEST-1 might write duplicate coverage without knowing that these two files already pin part of it.

## Proposed: Shared signing construction moves to pkg/auth/jwt as LoadSigning

**Status:** proposed

**Reported by:** step S17 (CODE-5 Token Service wiring: shared signer and key set).

**What the proposal says:** The CODE-5 targets in the non-spec changes are `cmd/lenny-token-service/kms.go`, `cmd/lenny-token-service/server.go`, and `pkg/tokenservice/tokenservice.go`, and the Token Service uses "the CODE-4 construction", which is the gateway's key-set and signer build.

**What landed instead:** The gateway's local `buildJWTSigning` helper moved to `pkg/auth/jwt/loadsigning.go` as `jwt.LoadSigning`. `cmd/lenny-gateway/token_signing.go` and `cmd/lenny-token-service/kms.go` both call it, and `cmd/lenny-gateway/jwt_signing_test.go` now calls `jwt.LoadSigning`. The step also added the Origin constant `jwt.OriginPlayground` in `pkg/auth/jwt/jwt.go`.

**Why:** The step reports that two main packages cannot share a helper that lives under `cmd/`. The code-best-practices rule asks for one shared helper in place of two copies of the same block, and the proposal's "One key-set construction" design calls for a single construction. The step reports no behavior change.

**What a later reader would otherwise get wrong:** A reader following the proposal text would look for the key-set and signer construction in `cmd/lenny-gateway` and expect the Token Service to carry its own copy or to reach into the gateway package. That reader would also not expect `pkg/auth/jwt/loadsigning.go`, the `jwt.OriginPlayground` constant, or the edits to `cmd/lenny-gateway/token_signing.go` and `cmd/lenny-gateway/jwt_signing_test.go` as part of CODE-5.

## Proposed: Token Service throttle and breaker tier-2 tests deferred to the outcome-mapping step

**Status:** proposed

**Reported by:** step S17 (CODE-5 Token Service wiring: shared signer and key set).

**What the proposal says:** The non-spec Testing section, under "Tests that land with CODE-4...CODE-7", lists as its first tier-2 bullet an in-process Token Service test with throttle injection, hard-failure, and breaker-open cases that return 503 `KMS_SIGNING_UNAVAILABLE` with `Retry-After`.

**What landed instead:** The step did not add this test. It added only the tier-2 fatal-startup test that its work text requires.

**Why:** The step reports that the throttle and `Retry-After` outcome mapping on the Token Service belongs to the outcome-mapping deliverable (checklist S20), which has not landed. Until that mapping lands, a hard failure still returns 500 `server_error`, so the bullet's assertions cannot pass before S20.

**What a later reader would otherwise get wrong:** A reader following the proposal text would expect the in-process Token Service 503 and `Retry-After` test to exist once CODE-5 lands, and could conclude that it was dropped. As landed at S17, that test is absent, a hard KMS failure on the Token Service returns 500 `server_error`, and the test depends on S20.

## Proposed: CODE-13 permissive /healthz tests renamed to match their 503 assertions

**Status:** proposed

**Reported by:** step S33 (CODE-13 either-dependency permissive /healthz and production readiness).

**What the proposal says:** The section "Tests that land with CODE-13" states that `TestHealthzReportsOK` and `TestHealthzPermissiveIgnoresDependencyOutage` keep their names and change to expect 503.

**What landed instead:** Both tests expect 503, and they are renamed `TestHealthzPermissive503WithNoProbes` and `TestHealthzPermissive503WhenBothDepsDown` in `pkg/ops/opsserver/opsserver_test.go` (at the top of the file and above the table test).

**Why:** The step reports that the old names describe the opposite of what the tests now assert: one name says the endpoint reports OK, and the other says it ignores a dependency outage. `tests/spec-map.json` maps `opsserver_test.go` at file level, so no map entry changed.

**What a later reader would otherwise get wrong:** A reader following the proposal text would search for `TestHealthzReportsOK` and `TestHealthzPermissiveIgnoresDependencyOutage` and conclude that the tests were removed. As landed at S33, the same tests exist under the names `TestHealthzPermissive503WithNoProbes` and `TestHealthzPermissive503WhenBothDepsDown`.

## Proposed: strictHealthDeps keeps its name while both /healthz modes read it

**Status:** proposed

**Reported by:** step S33 (CODE-13 either-dependency permissive /healthz and production readiness).

**What the proposal says:** CODE-13 item 1 directs a rewrite of the `strictHealthDeps` doc comment. It does not direct a rename of the variable.

**What landed instead:** The variable keeps the name `strictHealthDeps` in `pkg/ops/opsserver/opsserver.go`. Both the strict and the permissive `/healthz` modes now read it, and its doc comment states that.

**Why:** The step reports that keeping the name preserves the identifier the proposal names, and that a rename would be a change the proposal does not request.

**What a later reader would otherwise get wrong:** A reader going by the variable name would assume that `strictHealthDeps` is consulted only in strict mode. As landed at S33, the permissive mode also reads it, and the doc comment is the authoritative description of its use.

## D1 · step S22 · 2026-10-25 · accepted
**Status:** accepted
**Proposal says:** Non-spec changes, CODE-9 item 5 (`0097_fix_a-bearer-token-that-lenny-issues-verifies-only-on.non-spec-changes.md:136`), keeps the `childtoken.Minter` actor-token revocation check unchanged and requires its doc comment to state that "SPEC-7 step (1a) is the normative re-read". The spec states the same expectation: §8.2 (`spec/08_recursive-delegation.md:61`) and §13.3 (`spec/13_security-model.md:683`) say the Token Service runs the step (1a) actor-token re-read inside the transaction that issues the child token.
**Implemented instead:** The doc comment at `pkg/gateway/mcpfabric/delegation/childtoken/childtoken.go:53-57` describes the Minter's revocation-cache read as a gateway-side pre-check and states that the child mint runs in process, does not call the Token Service, and therefore does not run the §13.3 step (1a) re-read of `issued_tokens.revoked_at`. This matches the code: `MintChildToken` (`childtoken.go:124-148`) takes the audit lock, checks the revocation cache through `m.revocations.IsRevoked`, validates through `tokenexchange`, and returns the child token without calling `issuedtokenstore` or the Token Service. Its only production caller is `pkg/gateway/mcpfabric/delegation/service.go:1431`, and `cmd/lenny-gateway/mcpsurface.go:158` wires it in process.
**Why:** The two requirements of CODE-9 item 5 contradict each other on the in-process path. Before commit 21b697ea9, the comment followed the proposal's wording and added that a parent rotated after the pre-check "is caught there". The round-2 review flagged that sentence as false, because the Minter never reaches the Token Service. Commit 21b697ea9 rewrote the comment to describe the code accurately, and the reviews in rounds 3, 4, and 5 flagged the accurate comment for departing from the proposal's wording. Both available wordings have drawn a finding. The only change that makes the proposal's wording true is routing the delegation child mint through the Token Service's in-transaction `issued_tokens` re-read. Item 5 forbids that change by keeping the check unchanged, the proposal stages no such mechanism, and the change lies outside the scope of S22.
**Consequence if the proposal is not corrected:** A reader of CODE-9 item 5, §8.2, or §13.3 would conclude that a delegation child token is protected by the step (1a) in-transaction re-read, so that a parent token rotated or revoked after the gateway pre-check is rejected at issuance. On the in-process path the only check is the gateway's revocation-cache pre-check. An implementor who restores the proposal's comment wording would reintroduce the false claim the round-2 review flagged.
**Suggested next step:** file a follow-up proposal
**Evidence:** Commit 21b697ea9 ("childtoken: describe the Minter's revocation-cache read as a gateway-side pre-check") rewrote the `RevocationChecker`, `Minter`, and `MintChildToken` doc comments. Rounds 4 and 5 produced no new commit. Three judges each ruled with high confidence that the landed code and comment are correct, that the proposal is wrong, and that no change permitted under S22 closes the finding. The finding's own recommended fix is "No code change. Keep the landed comment", and it refers the gap between the in-process mint and §8.2 and §13.3 to the proposal or spec owner, who decides whether to route the delegation child mint through step (1a) or to describe the in-process path as it works.

## D2 · step S29 · 2026-10-25 · accepted
**Status:** accepted
**Proposal says:** Non-spec changes, TEST-1 item 6 (`0097_fix_a-bearer-token-that-lenny-issues-verifies-only-on.non-spec-changes.md:328`), requires that a token "revoked ... through tenant deletion gets the gateway's revoked-token response on lenny-ops within 35 s".
**Implemented instead:** The tenant-deletion subtest in `tests/tier9_security/ops_revocation_enforcement_test.go:285-299` revokes the token through rotation, deletes the tenant, waits for the tombstone, restarts lenny-ops, and asserts the revoked-token refusal (`opsprocess.CodeRevoked`). The subtest checks that a deleted tenant's revoked `issued_tokens` rows stay in the revocation set that a fresh lenny-ops process loads. `cmd/lenny-ops/revocation.go:135` loads revocations for tombstoned tenants, which matches the proposal's design note (`non-spec-changes.md:101`) that lenny-ops reads rows through a tenant lister that includes deleted tenants.
**Why:** The spec defines no revocation of an issued token by tenant deletion. In the §12.8 tenant deletion lifecycle, Phase 3 ("Revoke credentials") commits TenantState `deleting` and deregisters the tenant, so the gateway answers 403 `TENANT_NOT_FOUND`. The same phase returns credential leases, deletes TokenStore rows through `TokenStore.DeleteByTenant`, and flushes the Redis access-token cache. It never sets `issued_tokens.revoked_at`. Under §13.3 "Authoritative durability for revocation", Phase 4 deletes only the `issued_tokens` rows older than the trailing hour. The §25.4 revocation set (rules 1 to 3) holds only the jti values of rows whose `revoked_at` is set, and §25.4 has no rule that refuses a bearer because its tenant is deregistered or tombstoned. Two changes could produce the outcome item 6 describes: tenant deletion could write `revoked_at`, or lenny-ops could refuse a deregistered tenant's token. Each is behavior with no spec basis, so each requires a spec change through the proposal pipeline first. Rewording item 6 is a proposal edit. Neither change is open to the fixer under S29. Rounds 1, 2, 4, and 5 each closed the finding reported before them, and no round has a legal change available for this finding.
**Consequence if the proposal is not corrected:** A reader of TEST-1 item 6 would conclude that tenant deletion places a tenant's issued tokens in the lenny-ops revocation set, and that lenny-ops refuses a deleted tenant's token with the revoked-token response. Under §12.8, §13.3, and §25.4, tenant deletion leaves `revoked_at` unset, and lenny-ops refuses the token only when it was revoked by another path. An implementor who follows item 6 literally would write a test that no spec-conforming implementation passes, or would add a deleted-tenant refusal to lenny-ops without a spec basis.
**Suggested next step:** correct the proposal
**Evidence:** Round 1 (385cf832f) created `ops_revocation_enforcement_test.go` with the rotate-then-delete subtest. Round 2 (823a67a6c) changed only `tests/change-graph.json`. Round 4 (aadc2b815) closed the admin-credential and gateway-minted playground findings. Round 5 (c43151993) kept the restarted lenny-ops process alive for the Redis-stopped pass. The finding first appeared after round 5. Three judges each ruled with high confidence that the landed subtest checks the closest property the spec defines, that item 6 describes an outcome no spec-conforming implementation produces, and that no change permitted under S29 closes the finding. The finding recommends rewording item 6 as "a revocation of a token whose tenant is later deleted stays enforced". If lenny-ops is to refuse a deleted tenant's tokens, that requires a §25.4 spec change first.

## D3 · step S29 · 2026-10-25 · accepted
**Status:** accepted
**Proposal says:** Non-spec changes, TEST-1 item 4 (`0097_fix_a-bearer-token-that-lenny-issues-verifies-only-on.non-spec-changes.md:326`), requires that "GET /v1/admin/me on the chart-deployed lenny-ops returns 200 with the token's subject" for a `POST /v1/oauth/token` token. The Postgres-outage bullet of TEST-1 item 2 (`non-spec-changes.md:322`) requires that this token "is accepted", which the round-1 test read as a 200.
**Implemented instead:** The tests accept either a 200 or the 403 FORBIDDEN that the lenny-ops role gate returns after the bearer middleware has admitted the token. The helpers `Outcome.Authenticated` and `WaitAuthenticated` in `tests/testinfra/opsprocess/tokens.go`, pinned by `TestAuthenticatedSeparatesTheRoleGateFromAuthenticationRefusals`, encode that predicate. The call sites are `tests/tier4_integration/jwt_keyset_cross_process_test.go:503`, `:514`, and `:529`, and `requireOpsAuthenticates` in `tests/tier5_e2e_kind/jwt_keyset_cross_replica_test.go`. The tests separately pin a 200 with `identity.sub` for the gateway-minted impersonation bearer and the admin credential.
**Why:** The Token Service stamps no roles claim on the tokens it exchanges. `withOpsAuth` in `pkg/ops/opsserver/auth.go:40-61` applies `requireAdminRole` (`auth.go:105-122`) to every path except the probes, `/metrics`, and the cache-invalidate RPC (`isUnauthenticatedPath`, `auth.go:95-101`), so `/v1/admin/me` is gated. `requireAdminRole` answers 403 FORBIDDEN to a principal without platform-admin or tenant-admin. The gate implements the §25.4 "Authentication" sentence "Requires platform-admin or tenant-admin role on all endpoints" (`spec/25_agent-operability.md:1573`). The proposal's expected outcome relies on the §25.4 "Caller Identity" sentence "Any authenticated caller may call /v1/admin/me" (`spec/25_agent-operability.md:1684`), and the two sentences contradict each other. Two changes could produce the 200. Exempting `/v1/admin/me` from the gate in product code would settle the §25.4 contradiction outside the proposal pipeline, is a security-relevant authorization change, and lies outside the test-only targets of S29. Stamping roles on exchanged tokens has no spec basis. Rewording items 2 and 4 is a proposal edit, which the fixer may not make. The role-gate 403 is the strongest evidence available that the key set and the revocation set admitted the token.
**Consequence if the proposal is not corrected:** A reader of TEST-1 items 2 and 4 would expect lenny-ops to answer a `POST /v1/oauth/token` token on `/v1/admin/me` with 200 and the token's subject. Under the landed role gate and the §25.4 "Authentication" sentence, that token receives the role gate's 403 after authentication and the revocation check have admitted it. An implementor who restores a strict 200 assertion would write a test the landed code cannot pass, and an implementor who exempts `/v1/admin/me` to satisfy it would resolve a spec contradiction in code without a spec decision.
**Suggested next step:** correct the proposal
**Evidence:** Round 1 (385cf832f) created the step's tests, with the tier-4 Postgres-outage case asserting a 200 for the kept token. Round 8 (b86c30348) added `Outcome.Authenticated` and `WaitAuthenticated` and switched the tier-4 outage case and the tier-5 exchanged-token check to accept the role-gate 403; its commit message states that a token with no roles claim is refused at the lenny-ops role gate. Rounds 2, 4, 5, 6, and 9 (823a67a6c, aadc2b815, c43151993, 2434a9967) and 1e8921ee8 did not touch this subject. Round 10 produced no commit and stated that the finding needs no code change. Three judges each ruled with medium confidence that the landed assertions are the strongest outcome the code and spec allow, that the proposal wording is what needs to change, and that no change permitted under S29 closes the finding. The judges left to a human the choice between correcting TEST-1 items 2 and 4 and filing a separate change that exempts `/v1/admin/me` from the role gate. The judges also ruled that a separate finding, that the tier-4 Postgres-outage checks at `jwt_keyset_cross_process_test.go:506-520` run once immediately after `pgProxy.Cut()` and before any read has failed, has a legal test-only fix (hold the outage past `opsReadBound`, 35 s, repeat the kept and revoked assertions, and re-check `/healthz`) and stays with the loop. This deviation does not cover that finding.

## Proposed: role-gate 403 counted as lenny-ops admission for exchanged tokens

**Status:** proposed

**Reported by:** step S29 (TEST-1 harness change, cross-process tests, and tier-8 chaos cases).

**What the proposal says:** TEST-1 item 4 (Tier 5) says that `GET /v1/admin/me` on the chart-deployed lenny-ops returns 200 with the token's subject for a `POST /v1/oauth/token` token. The Postgres-outage bullet of TEST-1 item 2 says that the non-revoked token is accepted.

**What landed instead:** The tests count either a 200 or the role gate's 403 FORBIDDEN, which follows the bearer middleware, as admission for the roles-less exchanged token. The predicate is `opsprocess.Outcome.Authenticated` in `tests/testinfra/opsprocess/tokens.go`. `requireOpsAuthenticates` in `tests/tier5_e2e_kind/jwt_keyset_cross_replica_test.go` uses it, and `checkOutageOutcomes` and `WaitAuthenticated` in `tests/tier4_integration/jwt_keyset_cross_process_test.go` use it.

**Why:** The step reports that `pkg/ops/opsserver/auth.go` applies the platform-admin or tenant-admin gate to every endpoint, as the §25.4 "Authentication" sentence states. Exchanged tokens carry no roles claim, so lenny-ops cannot return 200 for one. A 200 would require either a product change that exempts `/v1/admin/me` from the gate, which settles the conflict inside §25.4 outside the proposal pipeline, or a correction to the proposal. The step reports that a human decides which.

**What a later reader would otherwise get wrong:** A reader of TEST-1 items 2 and 4 would expect lenny-ops to answer a `POST /v1/oauth/token` token on `/v1/admin/me` with 200 and the token's subject, and would read a 403 in the landed tests as a refusal. As landed, that 403 comes from the role gate after the key set and the revocation check have admitted the token. Entry D3 above records the same departure.

## Proposed: Runbook front matter names the lenny-ops component `ops`

**Status:** proposed

**Reported by:** step S30 (DOCS-1 runbook rewrite).

**What the proposal says:** DOCS-1 says "Add `lenny-ops` to `components`."

**What landed instead:** The `components:` list in the front matter of `docs/runbooks/jwt-key-rotation.md` adds the component as `ops`.

**Why:** The step reports that the runbook front matter and the component maps in the runbook index already name lenny-ops `ops` everywhere, for example in `ops-clock-skew.md` and in the index's alert map. A `lenny-ops` key would be a second spelling of the same component, and neither the index nor the health-API component lookup uses that spelling.

**What a later reader would otherwise get wrong:** A reader of DOCS-1 would search the runbook front matter for `lenny-ops` and conclude that the component was never added. As landed, the component appears as `ops`.

## Proposed: DOCS-1 also edits docs/runbooks/index.md

**Status:** proposed

**Reported by:** step S30 (DOCS-1 runbook rewrite).

**What the proposal says:** DOCS-1 names `docs/runbooks/jwt-key-rotation.md` as its only target.

**What landed instead:** `docs/runbooks/index.md` also changed. The warning-alert map gained a `KMSSignQuotaApproaching` row that points at `jwt-key-rotation.html#kms-sign-quota`, and the "Credentials and tokens" catalog group gained a `jwt-key-rotation` entry.

**Why:** The step reports that the index requires every runbook trigger to appear in its alert-to-runbook map, and that `jwt-key-rotation` was missing from the index catalog. Without these rows, the new trigger cannot be found from the index.

**What a later reader would otherwise get wrong:** A reader who takes DOCS-1's target list as the full extent of the docs change would treat the `docs/runbooks/index.md` edits as out of scope or unattributed. As landed, they belong to DOCS-1.

## Proposed: T-10.2.8 record describes a categorized-reason tier-6 skip rather than a skip-register entry

**Status:** proposed

**Reported by:** step S32 (REC-1 BUILD-GAPS and TEST-GAPS records).

**What the proposal says:** REC-1 in the non-spec changes advances T-10.2.8 with "tier-2 stub-backed coverage and a skip-registered tier 6". The tests section of CODE-1 says the tier-6 tests are skip-registered in `tests/registers/skip-reasons.yaml`.

**What landed instead:** The annotation under the T-10.2.8 heading in `TEST-GAPS.md` describes the tree as it stands. `tests/tier6_e2e_cloud/jwt_signing_kms_test.go` skips with the categorized reason `blocked: T-17.5.2: <variable> names no provisioned P-256 signing key version; create one and export its immutable reference`, and `tests/registers/skip-reasons.yaml` holds no entry for it. The annotation also records that the file now verifies through `jwt.LoadKeySet`, which supersedes the earlier S12 deviation entry stating that the file used `ecdsa.Verify`.

**Why:** The step reports that its task was to write the T-10.2.8 record from the tree. The skip register only shrinks, so the S12 step skipped through a categorized reason, and that departure already has its own entry in this file. A tier-11 test now checks the record against the test file and the register.

**What a later reader would otherwise get wrong:** A reader of REC-1 would expect the T-10.2.8 record to cite a skip-register entry and would search `tests/registers/skip-reasons.yaml` for one that does not exist. A reader of the S12 entry alone would believe the tier-6 file still verifies with `ecdsa.Verify`.

## Proposed: REC-1 recounts the TEST-GAPS summary counts and adds a tier-11 records test

**Status:** proposed

**Reported by:** step S32 (REC-1 BUILD-GAPS and TEST-GAPS records).

**What the proposal says:** REC-1 names `BUILD-GAPS.md` and `TEST-GAPS.md` as its only targets. It does not mention the `TEST-GAPS.md` summary counts or a tier-11 test.

**What landed instead:** The "Summary counts" line in `TEST-GAPS.md` was recounted from the headings and now reads 423 resolved and 1377 open. It previously read 418 resolved and 1382 open, which already disagreed with the headings before this change. The step also added `tests/tier11_docs/build_gaps_jwt_signing_records_currency_test.go` and mapped its tests under §10.2 in `tests/spec-map.json`.

**Why:** The step reports that its header lists tiers 0 and 11, and that the new records test follows the existing tier-11 records-currency pattern. Closing two findings without recounting would have left the summary count wrong.

**What a later reader would otherwise get wrong:** A reader who takes REC-1's target list as the full extent of the change would treat the summary-count edit, the new tier-11 test file, and the `tests/spec-map.json` entries as out of scope or unattributed. A reader comparing the old and new counts would attribute the whole difference of five to this proposal, although part of it corrects a drift that predates the change.
