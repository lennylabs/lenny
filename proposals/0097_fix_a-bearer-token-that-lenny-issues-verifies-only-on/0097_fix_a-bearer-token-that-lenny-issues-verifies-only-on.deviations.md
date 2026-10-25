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
