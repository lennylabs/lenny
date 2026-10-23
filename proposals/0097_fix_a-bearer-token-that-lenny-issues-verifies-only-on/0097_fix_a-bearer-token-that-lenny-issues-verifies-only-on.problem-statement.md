# Problem: Lenny-issued bearer tokens verify only on the process that signed them

## Statement

Lenny's token signing does not implement the §10.2 KMS-backed signing model, and the specification leaves the signing algorithm, key identity, verifier key distribution, signing topology, rotation state, and dev posture unstated or contradictory. The observed consequence is that a bearer token Lenny issues verifies only on the process that signed it (BUILD-GAPS F-10.2.22). Every gateway replica and every Token Service replica generates its own random HS256 signing key at startup in `jwt.NewKMSSigner` (`pkg/auth/jwt/kmssigner.go`), seals a copy under the KMS key, and never persists the sealed copy. The callers are `cmd/lenny-gateway/token_signing.go` (key ID `boot`) and `cmd/lenny-token-service/kms.go` (key ID `dev-1`). As a result:

- Gateway-minted bearers are refused on peer gateway replicas. These cover platform-admin impersonation, admin-token provisioning, and the playground bearer.
- Token Service bearers verify on no gateway. The default chart reverse-proxies `/v1/oauth/*` to the Token Service (`--token-service-http-url`), and the gateway's bearer verifier is a `RotatingVerifier` over its own per-process signer plus an HMAC trust key that the binary refuses outside dev mode. A token from `POST /v1/oauth/token` therefore verifies on no gateway in a production install.
- Token Service replicas reject each other's tokens, because the Token Service verifies caller, subject, and actor tokens with its own signer.
- Every token a process issued stops verifying when that process restarts.

The narrow fix that F-10.2.22 suggests, and that the `NewKMSSigner` doc comment describes, is to persist `SealedKey` and reload it on every replica through `KMSSignerFromSealed`. That fix would restore cross-replica verification without a new KMS seam. Owner decision 1 below rejects it, because it keeps one shared HS256 key in every process's memory and contradicts the §10.2 rule that the signing key never exists in gateway memory. The proposal therefore addresses conformance to §10.2 remote asymmetric signing, and closes F-10.2.22 as a consequence.

The per-process keys contradict several spec statements:

- §13.3 Credential Flow says a rotated token receives `401 token_revoked` "from any replica that has applied the revocation". That sentence concerns revocation status and assumes the signature verifies on every replica; under this defect a peer replica rejects the token on signature before any revocation check.
- §4.3 Token Service says any replica handles any request.
- §12.8 tenant deletion relies on every replica refusing a deleting tenant's bearers. Because of this defect, proposal 0095's tier-4 tenant deletion test (`tests/tier4_integration/tenant_deletion_phase4_test.go`) probes each Lenny-issued bearer only on its minting replica, and its deviations file records that narrowing.

The code also departs from §10.2 Authentication "Session capability context". That section says production signing is "KMS-backed signing (AWS KMS, GCP Cloud KMS, HashiCorp Vault Transit). The signing key never exists in gateway memory — the gateway sends the JWT claims to the KMS service and receives a signature". It also says verification "uses the locally cached KMS public key, not a live KMS call" ("KMS signing failure mode"). In the code:

- The KMS provider seam (`pkg/kms/kms.go`) has only `WrapDEK`, `UnwrapDEK`, and `CurrentKEKVersion`, with no Sign or GetPublicKey.
- The signer is a local HMAC key held in plaintext in process memory (`kmssigner.go` doc comment).
- `KMSSignerFromSealed` has no production caller.
- The published JWK is `kty: oct` with no key material (`pkg/auth/jwt/jwks.go`), so no other process can verify against it.
- `Signer.Sign` takes no `context.Context`.
- There is no Vault Transit provider. Azure Key Vault exists in code and the chart, but not in the §10.2 list.

Closed findings F-10.2.11 and F-10.2.13 recorded the key-in-memory inversion, and F-10.2.11's resolution says the issue "remains tracked". No open finding tracks it.

The specification is silent or contradictory on what a fix needs:

- It never states the signing algorithm. "Public key" and the §16 metric label `get_public_key` imply asymmetric keys.
- It names no configuration for key identity: no flag, environment variable, or Helm value for a KMS key ARN, a GCP CryptoKeyVersion, an Azure key, or a Vault transit key. It also does not say how `kid` maps to a KMS key version.
- It does not say how verifiers obtain the current and previous public keys by `kid`. The candidates are a cached GetPublicKey or a JWKS served by the Token Service or the gateway. It also leaves open the cache refresh rule and fail-closed behaviour on an unknown `kid`.
- It does not say whether the gateway and the Token Service sign with one shared key or with separate keys that each trusts.
- It does not say who drives key rotation or where rotation state lives. `docs/runbooks/jwt-key-rotation.md` documents `tokenService.signingKeySealed`, `tokenService.signingKid`, and a `signing_keys` table, none of which exists.
- It does not say how dev and test stacks with several replicas share a key without a cloud KMS. §17.4 Compose Mode runs a single gateway replica. The tier-4 stack runs several. The `local` KMS provider generates a random KEK per process unless `--kms-master-key-file` is set.
- §4.3 contradicts §10.2, §16.5, and §27.3.1 on which component mints. §4.3 item 3 says session tokens, delegation child tokens, credential-lease tokens, operability-scope tokens, and self-rotation grants are "minted here", at the Token Service, with the bootstrap admin-credential mint carved out as gateway-direct. §10.2 and §27.3.1 have the gateway mint session-capability and playground JWTs through `JWTSigner`, and §16.5 `KMSSigningUnavailable` points the operator at the gateway ServiceAccount's KMS IAM binding. The reconciliation concerns which process mints which token class and whether gateway replicas hold KMS Sign permission. §4.3's "only component with KMS decrypt permissions for downstream OAuth tokens" and "never refresh tokens or KMS keys" govern downstream OAuth credential custody and stay true under remote signing, so the proposal does not restate them.
- §18 cites a JWK Set publication endpoint and a signing-key rotation procedure "per §13.3", and §13.3 contains neither.
- §25.4 says `lenny-ops` validates JWTs against "the same OIDC issuer and JWKS endpoint". In code, `lenny-ops` requires a shared HMAC key in production (`cmd/lenny-ops/main.go`, chart value `security.oidc.bearerTrustKeySecret`) that no component signs with, so `lenny-ops` cannot verify gateway-issued bearers in production.

**Token lifetime.** §10.2 "JWT claims" says `expiry` is "short-lived, refreshed by gateway on each interaction". The sentence entered the design in commit 4a93795e9, when §10.2 described a gateway-internal HMAC key and re-minting cost microseconds. It was not revisited when f25e3740c moved signing to a remote KMS, and no delivery path for a re-minted token exists in any channel. The rest of the spec assumes fixed-lifetime tokens that the holder refreshes before expiry:

- §27.3.1 refreshes at `expiresInSeconds` minus 60 s, and a WebSocket continues under the token it was upgraded with.
- §25.4 refreshes at `security.oidc.tokenRefreshBeforeExpirySeconds` and warns that "very short TTLs cause refresh storms".
- §10.2 and §15 `KMS_SIGNING_UNAVAILABLE` say existing sessions are unaffected by a KMS outage.

Revocation is by `jti` (§13.3 `issued_tokens.revoked_at`, `401 token_revoked`), tenant state is checked on every request, and idle and age limits are enforced on session state. Per-interaction re-minting therefore protects no property the spec relies on. The code re-mints nowhere: no path refreshes per request or near expiry.

**Capacity.** Under remote signing, per-interaction re-minting is infeasible at Tier 3. The §16.5 Tier-3 figures are 10,000 concurrent sessions, 50,000 gateway requests/s, a session creation rate of 200/s (the table) or about 30/s (the §16.5 Little's-law derivation), and about 15/s of delegation children. Re-minting would mean about 2,000 to 50,000 KMS Sign calls/s. Every mint also commits an `issued_tokens` row and a `token.exchanged` audit row under the per-tenant advisory lock (§13.3), against the §12.2 Tier-3 Postgres sustained write ceiling of about 1,600/s.

Under mint-on-create-and-grant with near-expiry refresh and a 15-minute lifetime, the Tier-3 mint rate is about 57 to 227/s sustained, with bursts of about 680/s during a 3× session storm. The burst can exceed default cloud KMS asymmetric Sign quotas. AWS's is about 300/s for ECC and 500/s for RSA, per account per region; these are unverified figures and must be checked against provider documentation. The §10.2 KMS circuit breaker trips on "> 3 consecutive signing failures within 30s", so a quota-throttling burst would refuse every new session fleet-wide. §13.3 "`exp` granularity" says the revocation re-read happens "inside the same advisory-locked transaction as the signing step". With a 5–30 ms remote sign inside that lock, one tenant's mints would serialize. The code signs before the transaction (`pkg/tokenservice/tokenservice.go` `mintToken` then `persistIssuedToken`). §12.2's Postgres write table has no row for token issuance.

**Owner decisions (2026-10-23), settled and not to be reopened:**

1. Production signing is remote asymmetric KMS signing. The private key never exists in gateway or Token Service memory: the process sends the signing input to the KMS and receives a signature. Every verifier verifies locally against cached public keys selected by `kid`. Rejected: one shared HS256 key sealed by the KMS and held in memory (it gives every verifier forging power and contradicts §10.2), and a per-replica KMS-signed intermediate key (it holds a private key in memory). The intermediate key may be described only as a non-default operator mitigation if the proposal finds it necessary; it is not staged as a default.
2. Lenny-issued tokens have a fixed lifetime. The gateway never re-mints per interaction. The §10.2 "refreshed by gateway on each interaction" text is replaced by a rule where `exp` is the minimum of now plus the capability token lifetime and the applicable session, lease, and delegation caps. Holders refresh by pulling a new token before expiry, through `/v1/oauth/token` or the playground endpoint. A WebSocket authenticated at upgrade continues until it closes, and revocation, tenant state, and session state end it.
3. The default capability token lifetime is 15 minutes, operator-tunable within bounds the proposal states.

**Scope.** The proposal settles the silent and contradictory points above in the specification and then implements them:

- A KMS provider Sign and GetPublicKey seam with adapters for the providers the spec lists, plus `local`. The proposal decides the Vault Transit and Azure positions explicitly.
- A remote signer behind `JWTSigner`, with `Sign` taking a context.
- A key-identity configuration surface: flags and Helm values.
- A verifier key set shared by every gateway replica, every Token Service replica, and `lenny-ops`, with current and previous `kid` within the §10.2 overlap window, a refresh rule, and fail-closed on unknown `kid`.
- The gateway and Token Service signing topology, reconciling §4.3 with §10.2.
- The rotation procedure and its state, aligned with the runbook or with the runbook corrected.
- A dev-mode and test posture in which several replicas, and the Token Service, share a dev key. §10.2's dev bullet already permits a local key under `LENNY_DEV_MODE=true`.
- The fixed-lifetime rule.
- Throttling as backpressure in the signing breaker. Provider throttling returns a retryable 503 with `Retry-After` and does not count toward the trip. There is also a per-replica sign rate limit.
- Signing outside the per-tenant advisory lock, with the in-lock re-read discarding a signed but unpersisted token.
- Capacity guidance in §17.8 and §12.2: a mint-rate formula with Tier-3 figures, a KMS Sign quota sizing requirement, a sign-rate metric and an alert near quota, and the token-issuance rows in the Postgres write budget.
- The §18 citation fix.
- The `--jwks-publish` gate and the `jwksAdvertisesAsymmetric` operator notice that F-10.2.14 added, which that finding records must be revisited when an asymmetric signer lands.
- The existing dev-mode shared-key mechanisms (`--bearer-trust-hmac-key-file` from proposal 0017 and `--kms-master-key-file` for a shared local KEK) are built on or retired by the dev posture rather than joined by a third mechanism.

**Staging.** The change reaches §4.3, §10.2, §12.2, §13.3, §15, §16, §17.4, §17.8, §18, §25.4, and §27.3.1, the KMS seam and its provider adapters, the signer, the verifier set, the gateway, the Token Service, `lenny-ops`, the chart, and a runbook, with tests at every tier. That exceeds one implementation pass. The spec settlement lands in one pass because the contradictions interlock. The implementation may be cut into parts, following the precedent of proposal 0087 part 1. A candidate cut is: part 1 implements the Sign and GetPublicKey seam with the local asymmetric provider, the shared verifier key set, the reconciled topology, `kid` rollover, the fixed-lifetime rule, and the tier-4 every-replica probe; part 2 implements the cloud adapters, key-identity flags and Helm values, tier-2 and tier-5 tests, and T-10.2.8 and T-10.2.9; the capacity and backpressure strand goes in part 2 or a part 3. Production refuses `--kms-provider=local` (F-4.3.11), so a part 1 without a cloud adapter must state the production signing posture that holds until the cloud adapters land.

Tests:
- Tier 1: signer, verifier set, `kid` rollover, unknown `kid`, throttling versus outage in the breaker.
- Tier 2: KMS adapters against emulators where available.
- Tier 4: extend `lennyIssuedProbes` in the tenant deletion test so each Lenny-issued bearer is probed on every gateway replica, including Token Service-issued tokens, and remove the 0095 narrowing.
- Tier 5: the KMS-backed path.
- Fold in TEST-GAPS T-10.2.8 (the KMS signer is never exercised above unit tier) and T-10.2.9 (`kid` rotation is never verified end to end), and T-10.2.2 and T-10.2.3 where they fall on the same code.

**Out of scope, recorded as defects the proposal does not stage:**
- Session creation (`POST /v1/sessions`) mints no `session_capability` token, although §10.2 says the session-creation path does. `pkg/gateway/sessionserver` has no signer.
- Delegation child tokens are claim records that are never signed and never written to `issued_tokens` (`pkg/gateway/mcpfabric/delegation/childtoken/childtoken.go`).

The proposal states where those tokens will be signed once they are, so their later fix uses this signer. It does not implement their mint paths. F-10.2.23 and F-10.2.24 (tenancy-mode behaviour) are also out of scope.

## Evidence

- (verified) §10.2 Authentication "Session capability context", production bullet: "KMS-backed signing (AWS KMS, GCP Cloud KMS, HashiCorp Vault Transit). The signing key never exists in gateway memory" (spec/10_gateway-internals.md:213). The list omits Azure.
- (verified) §10.2 "JWT claims": "`expiry` (short-lived, refreshed by gateway on each interaction)" (spec/10_gateway-internals.md:220). §10.2 "KMS signing failure mode": verification uses "the locally cached KMS public key", and the breaker trips on "> 3 consecutive signing failures within 30s" (spec/10_gateway-internals.md:244). §10.2 "Key rotation" (kid, current and previous versions, 24h overlap) and the `LENNY_DEV_MODE` HMAC rule (spec/10_gateway-internals.md:242).
- (verified, with scope correction) §4.3 Token Service item 3 "minted here" with the gateway-direct bootstrap admin carve-out (spec/04_system-components.md:204); "only component with KMS decrypt permissions for downstream OAuth tokens" (:206); "never refresh tokens or KMS keys" (:215); any replica handles any request (:220). The decrypt and key-custody sentences are scoped to downstream OAuth tokens.
- (verified) §13.3 "`exp` granularity": the re-read happens "inside the same advisory-locked transaction as the signing step" (spec/13_security-model.md:658). §13.3 rotation: `401 token_revoked` "from any replica that has applied the revocation" (spec/13_security-model.md:674).
- (verified) §16.1 `lenny_gateway_kms_signing_errors_total` carries the operation label value `get_public_key` (spec/16_observability.md:270). §16.5 `KMSSigningUnavailable` names the gateway ServiceAccount's KMS IAM binding (spec/16_observability.md:486). §16.5 Tier 3: 10,000 concurrent sessions, 200/s creation, 50,000 RPS, and a Little's-law derivation of about 30/s claims and about 15/s delegation (spec/16_observability.md:595-597, :616).
- (verified) §12.2 Tier 3: about 1,600 IOPS ceiling against about 1,300/s sustained, and no token-issuance row in the write table (spec/12_storage-architecture.md:62-69, :121, :125).
- (verified) §18 Phase 14 cites a "JWT signing-key rotation procedure per §13.3: JWK Set publication endpoint, key-overlap window" (spec/18_build-sequence.md:670). §13.3 contains no JWK Set endpoint or signing-key rotation; its only JWKS reference is the upstream IdP (spec/13_security-model.md:688). No signing algorithm (RS256, ES256, EdDSA, HS256) appears anywhere in spec/.
- (verified) §25.4: `lenny-ops` validates JWTs with "the same OIDC issuer and JWKS endpoint" (spec/25_agent-operability.md:1567); `tokenRefreshBeforeExpirySeconds` and "very short TTLs cause refresh storms" (:1997-1998). §27.3.1 refreshes at `expiresInSeconds` minus 60 s and a WebSocket continues under its upgrade token (spec/27_web-playground.md:141, :149). §15 `KMS_SIGNING_UNAVAILABLE` leaves existing sessions unaffected (spec/15_external-api-surface.md:1135). §17.4 Compose runs a single gateway replica.
- (verified) Code, signer: `NewKMSSigner` draws a fresh random key and holds it in plaintext (pkg/auth/jwt/kmssigner.go:55-88); `KMSSignerFromSealed` has no non-test caller (pkg/auth/jwt/kmssigner.go:90-121); callers cmd/lenny-gateway/token_signing.go:88 (`boot`) and cmd/lenny-token-service/kms.go:30 (`dev-1`); `Signer.Sign(c Claims)` takes no context (pkg/auth/jwt/jwt.go:178-179); the JWK is `kty: oct` with no material (pkg/auth/jwt/jwks.go:77-98, :159).
- (verified) Code, verifiers: gateway bearer verifier is a `RotatingVerifier` over its own signer, with HMAC trust refused without `--dev-mode` (cmd/lenny-gateway/token_signing.go:116-145); Token Service verifies with its own signer (pkg/tokenservice/tokenservice.go:205-221, :442, :481, :490); the chart passes `--token-service-http-url` and the gateway reverse-proxies `/v1/oauth/*` (charts/lenny/templates/gateway-deployment.yaml:526; cmd/lenny-gateway/httpsurface.go:284-290).
- (verified) Code, mint sites: impersonation and admin token through `w.jwtSigner` (cmd/lenny-gateway/adminrouter.go:425, :847); playground (cmd/lenny-gateway/httpsurface.go:451-452).
- (verified) Code, KMS seam: `kms.Provider` has only `WrapDEK`, `UnwrapDEK`, and `CurrentKEKVersion` (pkg/kms/kms.go:85-106); adapters exist for aws, gcp, azure, and local and none for Vault; the chart offers `kms.provider` local, aws, gcp, or azure (charts/lenny/values.yaml:873-885); the local provider seeds a random KEK per process unless a master key file is set (pkg/kms/local.go:64-74).
- (verified) Code, issuance order: `mintToken` (signing) runs before `persistIssuedToken`, which takes the per-tenant advisory lock (pkg/tokenservice/tokenservice.go:544, :549, :672, :719-724).
- (verified) Code, lenny-ops: production requires `--bearer-trust-hmac-key-file`, a shared HMAC key (cmd/lenny-ops/main.go:159-181), mounted from chart value `security.oidc.bearerTrustKeySecret` (charts/lenny/templates/ops-deployment.yaml:229-234, :449-454, :502-510; charts/lenny/values.yaml:3582-3594).
- (verified) Code, lifetimes and replicas: per-dialect caps lenny-gateway 24h, lenny-ops 1h, llm-proxy 1h (cmd/lenny-token-service/server.go:42-46); gateway HPA `minReplicas: 2` (charts/lenny/values.yaml:2803); Token Service `replicas: 2` (charts/lenny/values.yaml:403-414); the only `sessionAffinity` in the templates is on the lenny-ops Service.
- (verified) Docs: docs/runbooks/jwt-key-rotation.md:87-100 documents `tokenService.signingKeySealed`, `tokenService.signingKid`, and a `signing_keys` table; none appears in charts, migrations, pkg, or cmd.
- (verified) Tests: `lennyIssuedProbes` probes each bearer only on its minting replica (tests/tier4_integration/tenant_deletion_phase4_test.go:440-452, :690-692); the 0095 deviations file records the narrowing (proposals/0095_.../0095_...deviations.md:159-171).
- (verified) Records: BUILD-GAPS F-10.2.22 OPEN (BUILD-GAPS.md:14403); F-10.2.11 and F-10.2.13 CLOSED, F-10.2.11 "remains tracked" (:14328-14350); F-10.2.14 CLOSED (:14352-14356); F-4.3.1 CLOSED partial and deferred (:709-715); TEST-GAPS T-10.2.1, T-10.2.2, T-10.2.3, T-10.2.8, T-10.2.9 OPEN (TEST-GAPS.md:3774, :3781, :3788, :3823, :3830).
- (verified) History: commit 4a93795e9 added the per-interaction refresh bullet next to "HMAC-SHA256, signed with a gateway-internal key"; commit f25e3740c added KMS-backed signing without changing the refresh bullet. Both edited the docs/ design files that preceded spec/.

## Who observes it

- Clients and operators of any default Helm install. The gateway HPA runs at least two replicas, the Token Service runs two, and the gateway Service sets no session affinity, so a gateway-minted bearer (playground, impersonation, admin token) reaches a refusing replica on roughly half of load-balanced requests.
- Every caller of the RFC 8693 exchange. A token from `POST /v1/oauth/token` verifies on no gateway, and rotation or exchange that lands on the other Token Service replica fails.
- Every holder of an unexpired token after a deploy, HPA scale-in, or eviction. Lifetimes run up to 24h for the lenny-gateway dialect, and the restarted process no longer accepts them.
- Operators of `lenny-ops` in production, which verifies against a shared HMAC key that no component signs with.
- Proposal 0095's tier-4 tenant deletion test, which can verify the §12.8 every-replica refusal only on the minting replica.

## What breaks if nothing changes

- Token exchange, the playground, and impersonation fail intermittently or entirely in a standard multi-replica install.
- The spec statements that any Token Service replica handles any request (§4.3), that a client can reconnect to any gateway replica (§10.1.1), and that revocation is observed from any replica (§13.3) stay unmet for Lenny-issued tokens.
- Production signing keeps a plaintext HMAC key in gateway and Token Service memory, against §10.2.
- The §12.8 every-replica refusal property stays untested across replicas for Lenny-issued bearers. A peer replica refuses those bearers today on signature, so this is a coverage gap rather than an open security hole; it becomes a hole once verification is shared unless the probe is extended.
- Any later fix for the `session_capability` mint or delegation child-token signing has no conforming signer to use.

## Findings this unblocks

- BUILD-GAPS F-10.2.22 (closed by this proposal).
- The unresolved signing half of BUILD-GAPS F-4.3.1 (the §4.3 trust boundary and the gateway's in-process signer), and the in-process key concern F-10.2.11 says "remains tracked".
- TEST-GAPS T-10.2.8, T-10.2.9, T-10.2.2, and T-10.2.3; candidate cross-references T-10.2.1 (no end-to-end OIDC/JWKS bearer test), T-17.5.2 (cloud KMS round-tripped against AWS only), and T-17.1.4 (Token Service multi-replica and KMS absent from the component suite).
- The BUILD-PLAN Phase 14 "JWT signing-key rotation" posture item.
- The removal of the 0095 tier-4 narrowing.

## Prior art considered

- No landed or in-flight proposal stages this change. Under proposals/, only the 0095 deviations file and 0097 mention F-10.2.22, `KMSSignerFromSealed`, `GetPublicKey`, or a `kty: oct` JWK. The unlanded proposals 0084, 0085, 0086, 0087, and 0096 do not address token signing.
- The spec has no mechanism that answers the open questions: no signing algorithm, JWKS only for the upstream IdP and the §25.4 `lenny-ops` sentence, and `get_public_key` only as a metric label.
- F-10.2.22's suggested resolution (one durable sealed key through `KMSSignerFromSealed`, or verification against Token Service-published keys) is the shared-HS256 option owner decision 1 rejects. The `NewKMSSigner` doc comment describes the same path, and `pkg/auth/jwt/kmssigner_test.go` exercises it cross-replica. It is recorded here as the rejected minimal fix.
- The remediation plan queues F-10.2.22 as a direct fix unless the spec does not state the shared key source, in which case the fix goes through the proposal pipeline first (gateway-runtime-comms-remediation.md:3138-3142). The spec does not state it.
- F-4.3.1 (closed, partial and deferred) deferred removing the gateway's KMS provider and never addressed the in-process signer. The §4.3 versus §10.2 topology reconciliation is its unresolved part.
- F-10.2.14 (closed) turned `--jwks-publish` off by default and added the `jwksAdvertisesAsymmetric` notice, recording that an asymmetric signer requires revisiting the gate.
- Reusable code: `pkg/auth/jwt/jwks.go` defines a JWK type with RSA and EC fields and a `PublicJWK` interface intended for asymmetric backends; only the HMAC signers implement it today.
- Dev-posture prior art: proposal 0017 added `--bearer-trust-hmac-key-file` (`pkg/auth/jwt/keyfile.go`), and `providerflags` offers `--kms-master-key-file` for a shared local KEK.

## Validated premises

**Premise lens (revise).**
- Holds: each process makes its own random HMAC key through `NewKMSSigner` (gateway `boot`, Token Service `dev-1`), and `KMSSignerFromSealed` has no production caller. Load-bearing.
- Holds: the gateway verifies only against its own key plus a dev-only HMAC trust key; no gateway verifies Token Service keys; Token Service replicas reject each other's tokens. Load-bearing.
- Holds: `w.jwtSigner` mints impersonation, admin-token, and playground bearers.
- Holds: the KMS seam lacks Sign and GetPublicKey, `Sign` takes no context, the JWK is `kty: oct`, and no Vault adapter exists.
- Holds: the §10.2 quotes, the §16.1 label, and the git history match.
- Refuted in detail: the §4.3 contradiction as originally stated. "Only component with KMS decrypt permissions" is scoped to downstream OAuth tokens and concerns decryption, and "never ... KMS keys" concerns key material that remote signing never hands to a gateway. Only item 3 "minted here" conflicts with §10.2. The statement now targets the minting topology.
- Refuted in detail: the chart value is `security.oidc.bearerTrustKeySecret`, not `ops.oidc.bearerTrustKeySecret`. The substance (production `lenny-ops` requires an HMAC key no component signs with) holds.
- Holds with nuance: the §13.3 sentence reads "from any replica that has applied the revocation" and concerns revocation status; the contradiction is indirect but valid. The §4.3 any-replica statement is accurate.
- Holds: signing precedes the advisory-locked transaction, contrary to §13.3.
- Holds: the dev, runbook, §18, and §25.4 premises; the BUILD-GAPS, TEST-GAPS, and tier-4 records; and the out-of-scope premises (no signer in `pkg/gateway/sessionserver`, no signing or `issued_tokens` write in `childtoken.go`, no per-request re-mint).

**Evidence lens (revise).**
- Verified: §10.2 quotes, §13.3 quotes (the "any replica" quote shortened in the original), §18 citation gap, §16 and §12.2 capacity figures, §25.4, §27.3.1, §15, §17.4, code claims, issuance order, runbook values, tier-4 probe, records, and history.
- Drifted: the original quoted §4.3 as "the only component with KMS decrypt permissions" without "for downstream OAuth tokens". Corrected above.
- Noted: F-10.2.22's title says "gateway replica", narrower than the "process" framing; the code shows the defect also holds for Token Service replicas.
- The evidence lens reported the chart value as `ops.oidc.bearerTrustKeySecret`; the repository shows `security.oidc.bearerTrustKeySecret` (charts/lenny/values.yaml:3584, charts/lenny/templates/ops-deployment.yaml:231), so the premise lens's correction stands.

**Prior-art lens (stands).**
- No proposal stages this change, and the spec answers none of the open questions. Load-bearing.
- F-10.2.22's suggested fix is the rejected option, so the proposal replaces it; the remediation plan's carve-out justifies a proposal.
- F-4.3.1 and F-10.2.14 bear on the change and were missing from the original; now cited.
- Reusable `PublicJWK` code and the dev-mode shared-key mechanisms exist; the dev posture builds on or retires them.
- Additional open TEST-GAPS (T-10.2.1, T-17.5.2, T-17.1.4) and the BUILD-PLAN rotation item overlap.

**Scope lens (revise).**
- The title names a symptom whose narrow fix the owner rejected; the statement now leads with §10.2 conformance.
- The key, verifier, topology, rotation, and dev strands share one root and stay in one problem; `lenny-ops` belongs as a third verifier.
- The fixed-lifetime rule is small and tied to remote signing cost; it stays.
- The capacity and backpressure strand is a distinct sub-problem with its own security argument (§13.3 lock ordering).
- The whole is too large for one implementation pass; the statement now records a staging recommendation and the production-posture obligation of a part 1 without cloud adapters. Load-bearing for the plan, not for the defect.
- The §18 citation fix is a rider; the out-of-scope list is drawn correctly.

**Impact lens (stands).**
- The defect is real: per-process keys, no reload caller. Load-bearing.
- The default chart runs several replicas with no gateway session affinity, so the defect is reachable without special configuration. Load-bearing.
- The RFC 8693 exchange is broken end to end in the default chart. Load-bearing.
- Restarts drop every token a process issued, with lifetimes up to 24h.
- The 0095 tier-4 test is narrowed; this is a coverage gap rather than a current security hole.
- §4.3, §10.1.1, and §13.3 statements are unmet for Lenny-issued tokens.

**Alternatives lens (revise).**
- No reading exists under which nothing needs to change.
- The persisted-sealed-key path is a smaller fix that decision 1 rejects; it is now recorded as the rejected minimal option.
- The §4.3 decrypt and custody rules stay true under remote signing; only the minting topology needs reconciling, and restating the custody rules would widen the change.
- The fixed-lifetime rule belongs here. The `lenny-ops` trust change and the §18 fix are separable fold-ins that touch the same verifier surface.
- The published `kty: oct` JWKS confirms that no other process can verify gateway tokens today.
