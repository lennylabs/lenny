# Non-spec changes: Lenny-issued bearer tokens verify only on the process that signed them

## Design (implementation-facing)

The work follows one dependency chain. The KMS seam gains signing (CODE-1). The `pkg/auth/jwt` package gains a remote ES256 signer and a static key set (CODE-2), and the breaker learns throttling (CODE-3). The published JWKS endpoint and the rotation observer go (CODE-10). The three binaries are then wired to one signer construction and one key set (CODE-4, CODE-5, CODE-6), after which the per-process HMAC signer and `RotatingVerifier` are deleted (CODE-12). The outcome mapping (CODE-7), the lifetime knob (CODE-8), the revocation re-read (CODE-9), and the alert catalog (CODE-11) are independent of that chain beyond their stated dependencies. Each deliverable compiles on its own: a deliverable that changes an exported signature updates every caller in the same commit, and a type is deleted only in the deliverable that removes its last caller. The chart (CHART-1 to CHART-5), docs (DOCS-1, DOCS-2), records (REC-1), and the cross-process tests (TEST-1) follow.

**Ordering constraint.** Every adapter (local, aws, gcp, azure, the testinfra stub, and the tenantkms test fake) implements `Sign` and `PublicKey` in CODE-1, before any binary switches to remote signing in CODE-4 and CODE-5. No intermediate commit leaves a production provider without a signer.

**One key-set construction.** `jwt.LoadKeySet` (CODE-2) is the only code that calls `PublicKey` on the `platform:jwt-*` aliases, and the signer is built from the key set's signing entry. The gateway, the Token Service, and `lenny-ops` each call it once at startup. A second `PublicKey` fetch in a signer constructor is a defect, because the signer's `kid` and the verifier's `kid` must come from one fetch.

## Staged code changes

### CODE-1 · KMS seam: `Sign`, `PublicKey`, `ErrThrottled`, and signing-reference validation in every adapter

Targets: `pkg/kms/kms.go`, `pkg/kms/local.go`, `pkg/kms/aws/aws.go`, `pkg/kms/gcp/gcp.go`, `pkg/kms/azure/azure.go`, `pkg/kms/providerflags/providerflags.go`, `tests/testinfra/stubs/kms/provider.go`, and the fake Provider in `pkg/tenantkms/probemanager_test.go`.

1. Extend `kms.Provider` with:

   ```go
   // Sign returns the ES256 signature of a 32-byte SHA-256 digest under the
   // key the alias names, as the 64-byte JWS r||s encoding (RFC 7518 §3.4).
   Sign(ctx context.Context, alias string, digest []byte) ([]byte, error)
   // PublicKey returns the P-256 public key of the key the alias names.
   PublicKey(ctx context.Context, alias string) (*ecdsa.PublicKey, error)
   ```

   Add the sentinel `kms.ErrThrottled`. Every adapter wraps its provider's throttling error with it: AWS `ThrottlingException` and `LimitExceededException`, GCP `codes.ResourceExhausted`, and Azure HTTP 429. `Sign` rejects a digest whose length is not 32 bytes. `PublicKey` returns an error when the key is not an ECDSA P-256 key. An alias with no mapping returns the existing `kms.ErrUnknownKEK`.
2. Each cloud adapter extends its narrow `keyClient` interface with the SDK's sign and get-public-key calls. AWS and GCP return ASN.1 DER signatures, which the adapter converts to the 64-byte r||s form, left-padding each half to 32 bytes. Azure returns r||s already. Every call carries a deadline from the caller's context.
3. Local provider: derive the ES256 private key deterministically from the seed and a signing reference. The reference is the value `--kms-alias` maps the `platform:jwt-*` alias to, and for an unmapped `platform:jwt-signing` it is the alias name. An unmapped `platform:jwt-verify` returns `kms.ErrUnknownKEK`. The `providerflags` local branch passes the `platform:jwt-*` entries of `AliasToKey` to the local provider; it ignores the map today.

   **IMPLEMENTOR'S CHOICE:** the scalar derivation — it is deterministic for a given (seed, reference) pair, uses an HKDF-SHA256 salt domain-separated from `kekMaterial`'s `alias\x00version` salt, yields distinct keys for distinct references, and builds the key from the derived scalar (for example with rejection sampling below the P-256 order). Do not pass the HKDF stream to `ecdsa.GenerateKey`: its output is not deterministic for a fixed reader by design, so two processes with one seed would derive different keys.
4. `providerflags`: when the provider is not `local`, reject at startup a `platform:jwt-signing` or `platform:jwt-verify` alias whose reference is repointable, per SPEC-1 **Production**. For AWS that means a reference starting with `alias/` or an ARN whose resource part starts with `alias/`; only a key ID or a `key/<id>` ARN passes. For GCP the reference must contain `/cryptoKeyVersions/<n>`. For Azure the reference must carry a non-empty `@version`. Update the `--kms-alias` help example to `platform:jwt-signing=<immutable P-256 key ref>`.
5. Rewrite the `kms.Provider` doc comment to describe both operation families: KEK wrap and unwrap for the envelope helper and the other wrap callers, and asymmetric `Sign` and `PublicKey` for the JWT signer. Remove the "sole caller" wording.
6. The testinfra stub gains `Sign` and `PublicKey` over an in-memory P-256 key per alias, plus fault injection for a hard error and for `kms.ErrThrottled`, which TEST-1 and the CODE-4 component test use.

### CODE-2 · `pkg/auth/jwt`: context on `Sign`, remote ES256 signer, and the static key set

Targets: `pkg/auth/jwt/jwt.go`, `pkg/auth/jwt/kmssigner.go`, new `pkg/auth/jwt/keyset.go`, `pkg/auth/jwt/jwks.go`, `pkg/auth/jwt/breakersigner.go`, and every caller of `Signer.Sign`.

1. `jwt.Signer` becomes `Sign(ctx context.Context, c Claims) (string, error)`. The same commit updates every implementation (`BreakerSigner` and the existing HMAC-backed `KMSSigner`, which CODE-12 deletes) and every mint site, each of which passes its request context.
2. Add the remote ES256 signer to `kmssigner.go` as a new type beside the existing `KMSSigner`. It builds the JOSE header `{"alg":"ES256","typ":"JWT","kid":<thumbprint>}`, hashes the signing input with SHA-256, calls `Provider.Sign` on `platform:jwt-signing`, and base64url-encodes the r||s result. Its constructor takes the signing entry from the key set and fetches nothing. It implements no `Verifier`.
3. New `keyset.go`: `LoadKeySet(ctx, provider, signingAlias, verifyAlias string) (*KeySet, error)` calls `PublicKey` for the signing alias (any error is returned) and for the verify alias (`kms.ErrUnknownKEK` means unmapped and is skipped; any other error is returned). It indexes each key by its RFC 7638 thumbprint and exposes the signing entry. `KeySet.Verify` implements SPEC-2 and the `alg` rule of SPEC-1: it rejects any `alg` other than `ES256`, returns `VerifyError` reasons `missing_kid` and `unknown_kid` with no provider call, and then checks the ECDSA signature and the existing claim checks. `KeySet` is the only verifier of Lenny-issued tokens.
4. `jwks.go`: add the EC JWK form and the RFC 7638 thumbprint function to the existing JWK type.
5. `HMACSigner` keeps `Sign(c Claims)` without a context as a concrete method for `pkg/embedded/devauth` and tests, and keeps `Verify` for the dev-only embedded HMAC trust member. It no longer satisfies `jwt.Signer`, so no production wiring can pass it where a signer is expected.

### CODE-3 · Breaker backpressure: throttling, `Retry-After`, and the outcome hook

Target: `pkg/auth/jwt/breakersigner.go`.

1. A `kms.ErrThrottled` error from the inner signer does not call `recordFailure`. `BreakerSigner.Sign` returns `ErrSigningThrottled` for it.
2. Add the typed error `ErrSigningThrottled{RetryAfter time.Duration}`. `RetryAfter` is 1 second on `kms.ErrThrottled`. `ErrSigningUnavailable` (breaker open) gains the remaining cooldown, so every refusal carries the SPEC-4 `Retry-After` source. Callers branch with `errors.As`. Round up to whole seconds with a floor of 1 at the HTTP layer (CODE-7).
3. Extend `BreakerObserver` with `OnOutcome(outcome string)`, called once per `Sign` with one of `ok`, `throttled`, `circuit_open`, or `error` (SPEC-5 **JWT sign requests** row). `OnSigningFailure` keeps firing only for `error` outcomes and `OnRejected` only for `circuit_open`, so `lenny_gateway_kms_signing_errors_total` stops counting throttled requests. `pkg/auth/jwt` gains no metrics dependency.

### CODE-4 · Gateway wiring: shared signer and key set

Targets: `cmd/lenny-gateway/token_signing.go`, `cmd/lenny-gateway/httpsurface.go`, `cmd/lenny-gateway/wiring_fields.go`, `cmd/lenny-gateway/adminrouter.go`, `pkg/gateway/credentials/impersonation`, `pkg/gateway/externalapi/admintoken`, `pkg/gateway/mcpfabric/playground`.

1. Build the key set once with `jwt.LoadKeySet(ctx, kmsProvider, "platform:jwt-signing", "platform:jwt-verify")` and call `log.Fatalf` on error. Build the remote signer from the key set's signing entry and wrap it in `BreakerSigner`.
2. `bearerVerifier` becomes a `MultiVerifier` over the key set plus the existing dev-only embedded HMAC trust member, which stays refused without `--dev-mode`. The playground handler's `Verifier` becomes the key set in place of the bare signer.
3. Every gateway mint site (`adminrouter.go` impersonation and admin token, `admintoken`, `impersonation`, and the playground token handler) calls `Sign(ctx, …)` with the request context.
4. Correct any comment in these files that says the gateway does not mint.

CODE-10 lands before this deliverable, because it removes the JWKS mount and the rotation audit observer that read the gateway's `RotatingVerifier`.

### CODE-5 · Token Service wiring: shared signer and key set

Targets: `cmd/lenny-token-service/kms.go`, `cmd/lenny-token-service/server.go`, `pkg/tokenservice/tokenservice.go`.

1. Use the CODE-4 construction: one `LoadKeySet`, the signer from its signing entry, and a `BreakerSigner` around it. Load failure is fatal.
2. `Options.Verifier` is the key set and verifies caller, subject, and actor tokens. Delete `NewServer`'s signer-as-verifier fallback, and make `NewServer` return an error when `Options.Verifier` is nil. A remote signer is not a verifier, so the fallback would leave the Token Service rejecting every token.
3. `mintToken` calls `Sign(ctx, …)` with the request context.

### CODE-6 · `lenny-ops` wiring: KMS flags, key set, the dev trust hook, and the revocation set

Targets: `cmd/lenny-ops/main.go` (`buildAuthConfig`), `cmd/lenny-ops/flags.go`, a new `cmd/lenny-ops/revocation.go` (item 6), `pkg/gateway/middleware/auth` (item 6, only if the IMPLEMENTOR'S CHOICE below needs a new hook), `charts/lenny/templates/ops-deployment.yaml` (item 5 only).

1. Register `providerflags.Bind` on the `lenny-ops` flag set.
2. When `--production` is set, `lenny-ops` refuses a resolved `local` provider at startup, independent of `--environment`. `lenny-ops` has no `--dev-mode`, and `--production` is its only production signal, so the providerflags environment check alone would admit `local` under `--production` without `LENNY_ENV=prod`.
3. Every `lenny-ops` process loads the key set once at startup through `jwt.LoadKeySet`, with or without `--production`, and a load failure is fatal, as SPEC-2 states for every process. An unset `--kms-provider` resolves to the local provider (`providerflags.Resolve`), so a development `lenny-ops` derives its key set from `--kms-master-key-file` the same way the gateway and the Token Service do. `--production` adds only the refusal of `local` in item 2.
4. `--bearer-trust-hmac-key-file` is fatal when `--production` is set. Outside production it stays as the dev embedded-OIDC trust hook, alongside the key set.
5. In the same commit, `charts/lenny/templates/ops-deployment.yaml` stops rendering `--bearer-trust-hmac-key-file` and its key mount when `ops.production` is true. Without this, a production render passes a flag the binary now refuses, and `lenny-ops` crashloops.
6. Revocation set (SPEC-11 Edit 2, owner decision 4). `lenny-ops` holds a `revocation.Cache` (`pkg/gateway/credentials/revocation`) for Lenny-issued tokens and builds it the way `cmd/lenny-gateway/revocation.go` builds the gateway's:
   - At startup it rehydrates the cache from `issued_tokens` with the gateway's rehydration query. A failed startup load does not stop the process; it retries on the rehydration interval.
   - While Redis is reachable it applies `token.revoked` events through the gateway's propagator (`pkg/gateway/credentials/revocation/propagator`).
   - It repeats the rehydration on the gateway's interval, so the set stays current from Postgres when Redis is down.
   - `buildAuthConfig` wires `authmw.Options.Revocations` to the cache for tokens the key set verified, and a revoked `jti` returns `401 token_revoked`.
   - It does not wire the gateway's `RevocationFreshness` staleness gate (`Cache.Stale`), because that gate refuses every bearer once Postgres has been unreachable past the staleness bound, which is option A of owner decision 4. Instead, a token the key set verified is refused with `503 token_validation_unavailable` only until the first successful rehydration since process start. After that, a Postgres outage leaves the set frozen and key-set tokens not in it are accepted.
   - Upstream IdP tokens and dev trust-hook tokens bypass the revocation set and the startup gate.

   **IMPLEMENTOR'S CHOICE:** how `lenny-ops` reaches `issued_tokens` (the gateway's store type or a narrow reader) and how the startup gate and the key-set scoping are expressed in `authmw` (an existing hook or a new option). The bounds: the rehydration query is the gateway's, so one query defines what a replica loads; no key-set token is refused for staleness after the first successful load; every key-set token is refused before it; and no non-Lenny-issued token is affected by either check.

### CODE-7 · Outcome mapping: `Retry-After` and the sign metrics

Targets: the gateway mint-site error paths of CODE-4, `pkg/tokenservice/tokenservice.go` (`writeKMSUnavailable`), `pkg/gateway/metrics/gatewaymetrics/gatewaymetrics_signing.go`, and the Token Service metrics emitter.

1. Every mint path that receives `ErrSigningUnavailable` or `ErrSigningThrottled` returns `503 KMS_SIGNING_UNAVAILABLE` with `Retry-After` per SPEC-4. `writeKMSUnavailable` takes the duration instead of writing a fixed `30`. The gateway playground and impersonation paths, which set no header today, set it.
2. Register `lenny_jwt_sign_requests_total{component,outcome}` and feed it from `OnOutcome`. The Token Service's breaker observer also increments `lenny_gateway_kms_signing_errors_total{reason}` with the same `inner` and `rejected` semantics as the gateway, so SPEC-5's errors row holds for both signers.

### CODE-8 · Capability token lifetime knob

Targets: `cmd/lenny-gateway/flags.go`, `pkg/gateway/mcpfabric/playground/playground.go`, `charts/lenny/templates/gateway-deployment.yaml` (the flag name only), `cmd/lenny-token-service/flags.go`, `cmd/lenny-token-service/server.go` (`buildServer`), `pkg/tokenservice/tokenservice.go` (`Options`, `buildExchangeRequest`, `durationFromExpiresIn`), `charts/lenny/templates/token-service-deployment.yaml` (the new flag line).

1. Rename `--playground-bearer-ttl-seconds` to `--capability-token-ttl-seconds` (environment variable `LENNY_CAPABILITY_TOKEN_TTL_SECONDS`, default 900). Startup is fatal outside the SPEC-3 Edit 1 bound. No alias for the old flag is kept. In the same commit, `charts/lenny/templates/gateway-deployment.yaml` renders the new flag name from the existing `playground.bearerTtlSeconds` value, so the chart never passes a flag the binary no longer defines; CHART-1 then renames the value.
2. The playground bearer `exp` rule is SPEC-3 Edit 1. Rename `BearerTTL` and its error text to name `auth.capabilityTokenTtlSeconds`.
3. Token Service default (SPEC-3 Edit 1, owner decision 5). `durationFromExpiresIn` in `pkg/tokenservice/tokenservice.go` returns a hard-coded 1 hour for an absent `expires_in` today. Replace it with `Options.CapabilityTokenTTL`, which `buildServer` sets from a new `lenny-token-service` flag `--capability-token-ttl-seconds` (environment variable `LENNY_CAPABILITY_TOKEN_TTL_SECONDS`, default 900, fatal outside the SPEC-3 Edit 1 bound). `NewServer` returns an error when the value is zero. A request that carries `expires_in` keeps it, and the per-dialect caps clamp it as today.
4. For an exchange that carries an `actor_token` (`a2a_delegation`) with no `expires_in`, `buildExchangeRequest` leaves `RequestedExp` zero instead of setting `now + 1 hour` as it does today. `tokenexchange.Derive` already treats a zero `RequestedExp` as absent, so the child's `exp` is `min(subject_token.exp, actor_token.exp, per-dialect cap)` and the capability default never applies to it.
5. In the same commit, `charts/lenny/templates/token-service-deployment.yaml` renders `--capability-token-ttl-seconds` from the same value the gateway template reads, so both binaries take one operator-set default.

### CODE-9 · In-transaction source-token revocation re-read

Targets: `pkg/gateway/storage/issuedtokenstore/issuedtokenstore.go` (`RecordWithAudit`, `RecordWithRotationAudit`), `pkg/tokenservice/tokenservice.go` (`persistIssuedToken` and its store interface), `pkg/tokenservice/rotation_test.go`, `cmd/lenny-gateway/user_revocation.go` (the `adminIssuedTokenStore` interface and its `RecordWithAudit` call), and `cmd/lenny-gateway/user_revocation_test.go` (`fakeAdminIssuedTokenStore`).

1. Both store methods take the list of source JTIs to recheck: the subject `jti` and, when present, the actor `jti`. After the advisory lock and before any insert, they run SPEC-7 step (1a) as one `SELECT jti, revoked_at FROM issued_tokens WHERE jti = ANY($1)`. A listed `jti` whose row has `revoked_at` set returns a typed error naming which source failed. No issuance statement runs.
2. On that error, `persistIssuedToken` discards the signed token and writes the rejection `token.exchanged` audit row with `policy_result: "rejected:subject_token_revoked"` or `"rejected:actor_token_revoked"` under the lock-and-COMMIT discipline of §13.3, then returns `400 invalid_grant` with that reason. A failed rejection audit write returns `500 token_exchange_failed` with the reason in `detail`.
3. A rotation of a token whose predecessor is already revoked, which is a retried or concurrent rotation, now fails with `subject_token_revoked` instead of committing a second mint. Update the `RecordWithRotationAudit` doc comment. Its `ErrNoRows` branch becomes reachable only for a subject with no row. Update `rotation_test.go`.
4. `cmd/lenny-gateway/user_revocation.go` declares `RecordWithAudit` in `adminIssuedTokenStore` and calls it for the admin rotation. Update the interface, the call, which passes an empty recheck list because the admin rotation has no subject token to re-read, and the test fake in the same commit, so `cmd/lenny-gateway` compiles when the store signature changes.
5. The gateway's `childtoken.Minter` (`pkg/gateway/mcpfabric/delegation/childtoken/childtoken.go`) check is unchanged. Its doc comment states that it is a gateway-side pre-check and that SPEC-7 step (1a) is the normative re-read.

### CODE-10 · Delete the published JWKS endpoint and the rotation audit observer

Targets: `cmd/lenny-gateway/httpsurface.go` (the `/.well-known/jwks.json` mount), `cmd/lenny-gateway/flags.go` and `cmd/lenny-gateway/config_parse.go` (`--jwks-publish`), `cmd/lenny-gateway/auth_seams.go` (`jwksAdvertisesAsymmetric`), `cmd/lenny-gateway/authposture.go` and `pkg/gateway/middleware/deprecation/deprecation.go` (the `/.well-known/jwks.json` references), `cmd/lenny-gateway/auditpipeline.go` (the `rotatingVerifier` observer), `pkg/gateway/audit/jwtaudit/jwtaudit.go` (delete the JWT rotation observer and its test; keep `CAObserver`), `pkg/auth/jwt/jwks.go` (delete `JWKSHandler` and `JWKSource` and their unit tests), and `tests/tier4_integration/jwks_test.go` (deleted).

No spec section defines the endpoint, and no in-platform verifier reads it after CODE-4. The rotation observer has no event to emit, because rotation is a configuration rollout.

### CODE-11 · Alert catalog: `KMSSignQuotaApproaching` and the `KMSSigningUnavailable` text

Target: `pkg/alerting/rules/rules.go`, then `make generate`, which regenerates `charts/lenny/files/alerting-rules.yaml` and `docs/alerting/rules.yaml`. Do not hand-edit either generated file.

1. Add `KMSSignQuotaApproaching` with the SPEC-5 Edit 4 expression, `For` 10 minutes, severity warning, `SpecRef` §16.5, and `RunbookURL` pointing at the DOCS-1 "KMS Sign quota" section.
2. Keep the `KMSSigningUnavailable` expression and change its summary and description to name the gateway or Token Service signer (SPEC-5 Edit 3).

**IMPLEMENTOR'S CHOICE:** how the `kms.signQuotaPerSecond` threshold and the not-`local` condition reach the rendered PrometheusRule (a template placeholder in the generated fragment, or a chart-side rule outside the generated set) — the rendered rule's threshold equals `0.8 × kms.signQuotaPerSecond`, the rule is absent when `kms.provider` is `local`, `rules.go` stays the catalog that names the alert, and a CHART-5 helm unittest asserts both.

### CODE-12 · Delete the per-process HMAC signer and `RotatingVerifier`

Targets: `pkg/auth/jwt/kmssigner.go`, `pkg/auth/jwt/rotating.go` (deleted), `pkg/auth/jwt/jwks.go`, `pkg/auth/jwt/breakersigner.go`, `pkg/auth/jwt/multiverifier.go`, and their tests. This lands after CODE-4, CODE-5, and CODE-6 remove the last callers.

1. Delete the HMAC-backed `KMSSigner` with its key generation, `SealedKey`, `KMSSignerFromSealed`, `TokenServiceKEKAlias`, `Verify`, and `PublicJWK`.
2. Delete `rotating.go` (`RotatingVerifier`, `Rotate`, `RetireExpired`, `IsKeyRetired`, and the `key_retired` reason), and drop `key_retired` from `IsUnknownKey` and from `MultiVerifier`'s fall-through list. `IsUnknownKey` keeps accepting `missing_kid` and `unknown_kid`.
3. Delete the `PublicJWK` interface, `HMACSigner.PublicJWK`, and `publicJWKOf`.
4. Delete `BreakerSigner.Verify`, so no signer can be picked up as a verifier.

## Staged schema, chart, and migration changes

### CHART-1 · Values, schema, and presets

Targets: `charts/lenny/values.yaml`, `charts/lenny/values.schema.json`, `pkg/chart/values/values.go`, `charts/lenny/presets/dev.yaml`.

1. Replace `playground.bearerTtlSeconds` with `auth.capabilityTokenTtlSeconds` (default and schema bounds from SPEC-3 Edit 1) in the values, the schema, `pkg/chart/values/values.go`, and the gateway and Token Service templates' `--capability-token-ttl-seconds` lines. The schema minimum moves from 60 to 120, and `pkg/chart/values/schema_test.go` (`TestRejectsBearerTTLOutOfRange`) moves to the new bounds. No alias for the old key is kept.
2. Add `kms.signQuotaPerSecond` (integer, minimum 1, no default). Rendering fails with a message naming the value when `kms.provider` is not `local` and the value is unset.
3. Correct the `kms` comments that say the Token Service wraps its signing key and that `--kms-provider` renders on two binaries, so they name the three binaries and signing through `platform:jwt-signing`. Replace the `platform:token-service-signing` alias example with `platform:jwt-signing=<immutable P-256 key ref>` and document `platform:jwt-verify`.
4. Document `security.oidc.bearerTrustKeySecret` as dev-only. CODE-6 makes `lenny-ops` refuse it under `--production`.

### CHART-2 · Local KMS master key Secret and KMS flags on all three deployments

Targets: a new template for the local KMS master key Secret, `charts/lenny/templates/gateway-deployment.yaml`, `charts/lenny/templates/token-service-deployment.yaml`, `charts/lenny/templates/ops-deployment.yaml`.

1. When `kms.provider` is `local`, render one Secret holding a random master key, generated on first install and preserved across upgrades with `lookup`, following the `etcd-encryption.yaml` pattern. Mount it in the gateway, Token Service, and `lenny-ops` pods and pass `--kms-master-key-file`. When the provider is not `local`, render neither the Secret nor the flag.
2. `ops-deployment.yaml` renders the `--kms-*` flags (provider, aliases, Azure vault URL, environment) the gateway and Token Service already receive. The `lenny-ops` ServiceAccount receives the provider's `GetPublicKey`-only identity binding through the same mechanism the chart uses for the Token Service ServiceAccount.

### CHART-4 · Gateway and `lenny-ops` KMS egress

Targets: `charts/lenny/templates/system-network-policies.yaml` (`allow-gateway-egress`), `charts/lenny/templates/ops-network-policies.yaml` (`lenny-ops-egress`), and `charts/lenny/values.yaml`.

Render the SPEC-10 KMS peer and its render refusal (§13.2.4 Gateway row) in `allow-gateway-egress` and `lenny-ops-egress`. Correct the `kms.endpointCIDR` comment in `values.yaml` so it states that a cloud provider requires a narrower CIDR. Do not copy the always-rendered `kms.endpointCIDR` peer of `allow-token-service` into either policy.

### CHART-5 · PrometheusRule rendering of `KMSSignQuotaApproaching`

Target: `charts/lenny/templates/prometheusrule.yaml` (or the template CODE-11's choice names). Render the CODE-11 rule with the quota threshold and omit it under `local`.

## Staged docs changes

### DOCS-1 · Rewrite the JWT key rotation runbook

Target: `docs/runbooks/jwt-key-rotation.md`.

Remove every `/.well-known/jwks.json` step, the `signing_keys` psql step, the `tokenService.signingKeySealed` and `signingKid` values, the `platform:token-service-signing` alias, and the `RotatingVerifier` overlap and `key_retired` text. Describe the three rollouts and the step-2 `lenny-ctl admin users rotate-token --user lenny-admin`, in the reader-facing form the doc rules require, with no spec section numbers. Replace the "every replica advertises the same JWK Set" check with a check that every gateway, Token Service, and `lenny-ops` pod rolled out under the same `platform:jwt-signing` and `platform:jwt-verify` values. List the IAM grants: the gateway and Token Service ServiceAccounts get `Sign` and `GetPublicKey`, and the `lenny-ops` ServiceAccount gets `GetPublicKey` only. State the startup behavior during a KMS outage. Add `lenny-ops` to `components`. Add a "KMS Sign quota" section for the `KMSSignQuotaApproaching` alert, and add the alert to `triggers`.

### DOCS-2 · Reference, operator guide, and error catalog pages

Targets and edits:

- `docs/reference/configuration.md`: correct the `kms.provider` row to `aws`, `gcp`, `azure`, and `local`, with `local` refused in production. Document the `platform:jwt-signing` and `platform:jwt-verify` alias entries, which must be immutable key-version references on cloud providers. Add `kms.signQuotaPerSecond`, required on cloud providers. Add `kms.endpointCIDR`, which must be narrower than `0.0.0.0/0` on cloud providers. Replace the `playground.bearerTtlSeconds` row with `auth.capabilityTokenTtlSeconds` (default and bounds from SPEC-3 Edit 1), described as the default lifetime of playground bearers and of `POST /v1/oauth/token` tokens requested without `expires_in`. Remove "dev HMAC JWT" from the `playground.devTenantId` row.
- `docs/operator-guide/web-playground.md`: rename the lifetime value in the YAML example.
- `docs/operator-guide/security.md` "Supported KMS Providers": remove the HashiCorp Vault Transit row, list Azure Key Vault for JWT signing, and state that all three cloud providers sign with ES256.
- `docs/operator-guide/agent-operability.md` failure table, "Postgres down" and "Postgres + Redis both down" rows: state that `lenny-ops` keeps accepting Lenny-issued tokens against the revocation set it loaded before the outage, and that a `lenny-ops` started during the outage refuses Lenny-issued tokens until Postgres recovers (SPEC-11 Edit 4).
- `docs/reference/error-catalog.md`, `KMS_SIGNING_UNAVAILABLE` row: name the gateway or Token Service signer, an open breaker or KMS throttling, and state that the response carries `Retry-After`.

### REC-1 · Records

Targets: `BUILD-GAPS.md`, `TEST-GAPS.md`.

- BUILD-GAPS: close F-10.2.22. Add a resolution note to F-10.2.13 recording that the in-memory HS256 signer is replaced by remote ES256 signing; F-10.2.11 names F-10.2.13 as its tracking item. Add a note to the closed F-10.2.11 and F-10.2.14 recording that this proposal supersedes them and deletes the JWKS endpoint and flag. Add a note to the closed F-4.3.1 stating that the gateway still signs in process but holds no private key on a cloud provider. Do not describe this as closing a "signing half".
- TEST-GAPS: T-10.2.9 closed by replacement (the timed overlap window is removed, and TEST-1 item 2 covers rotation). T-10.2.8 advanced (tier-2 stub-backed coverage and a skip-registered tier 6), and it stays open until tier 6 runs. T-10.2.2: the throttle and breaker half is covered, and the `POST /v1/sessions` half stays open with the out-of-scope session-creation mint. T-10.2.3: split into the claim half (covered) and the JWKS half (retired with the endpoint).

## Testing

Every test carries a `// spec:` annotation, and every test at tier 2 and above carries a `// diagnosis:` comment. The tests below land with the deliverable named in each heading.

### Tests that land with CODE-1

- Tier 1, `pkg/kms/local_test.go` (`// spec: 10.2 (Authentication)`): the same reference on two providers sharing one seed gives the same public key; two different mapped references give different keys; an unmapped `platform:jwt-verify` returns `kms.ErrUnknownKEK`; the signing key differs from every KEK the same seed derives; a signature verifies under `PublicKey` and is 64 bytes.
- Tier 1, `pkg/kms/{aws,gcp,azure}/*_test.go` with fake clients: `Sign` returns r||s for a DER signature whose halves need left-padding (the discriminating case, since a 31-byte `r` breaks naive conversion); provider throttling maps to `kms.ErrThrottled`; a non-P-256 key fails `PublicKey`; a 31-byte digest is rejected; a cancelled context aborts the call.
- Tier 1, `pkg/kms/providerflags` (`// spec: 10.2 (Authentication)`): table over AWS `alias/x`, an alias ARN, a key ARN, and a bare key ID; GCP with and without `/cryptoKeyVersions/3`; Azure `name@` and `name@v1`. Only the immutable forms pass, and the rule does not apply under `local`.
- Tier 6, per cloud provider, skip-registered in `tests/registers/skip-reasons.yaml` without credentials and cross-referenced to T-17.5.2: sign with a real P-256 key and verify through `LoadKeySet`.

### Tests that land with CODE-2 and CODE-3

- Tier 1, `pkg/auth/jwt` (`// spec: 10.2 (Authentication)`): `KeySet.Verify` rejects `alg: HS256` (including an HS256 token whose HMAC key is the public key bytes, the algorithm-confusion case) and `alg: none`, returns `missing_kid` and `unknown_kid`, and makes no provider call for an unknown `kid` (assert the stub's call count). A token signed under `platform:jwt-verify` verifies, and one signed by a key outside the set fails. `LoadKeySet` skips an unmapped verify alias and fails on any other error from either alias.
- Tier 1, breaker (`// spec: 10.2 (Authentication)`): randomized interleavings of `ok`, `kms.ErrThrottled`, and hard errors. The invariant checker asserts that the breaker opens if and only if more than 3 hard failures fall inside 30s, that throttled calls never move the count, that every refusal carries a positive `RetryAfter`, and that `OnOutcome` fires exactly once per call with the right value. Boundary cases cover exactly 3 failures (stays closed) and a fourth at 29.9s and at 30.1s.

### Tests that land with CODE-4, CODE-5, CODE-6, and CODE-7

- Tier 2 component, real gateway and Token Service against the testinfra KMS stub (`// spec: 10.2 (Authentication), 15.1 (REST API)`). With throttle injection, the impersonation mint and `POST /v1/oauth/token` return 503 `KMS_SIGNING_UNAVAILABLE` with `Retry-After` and the breaker stays closed. Four hard failures in 30s open the breaker, and the next mint returns `Retry-After` equal to the remaining cooldown. A token issued before the fault still verifies on both processes throughout.
- Tier 1, Token Service: `NewServer` with a nil `Verifier` returns an error.
- Tier 1, `lenny-ops`: `--production` with `kms.provider=local` is fatal, `--production` with `--bearer-trust-hmac-key-file` is fatal, and an unloadable key set is fatal with and without `--production`.
- Tier 1, `cmd/lenny-ops` revocation set (`// spec: 25.4 (The lenny-ops Service), 13.3 (Credential Flow)`), with a fake `issued_tokens` source, a fake clock, and a fake event bus:
  - Before the first successful load, a key-set token returns `503 token_validation_unavailable` while a dev trust-hook token is admitted (the discriminating case for the key-set scoping).
  - After a load, a revoked key-set token returns `401 token_revoked`, and a non-revoked one is admitted.
  - After a load, with the source failing for longer than the gateway's staleness bound (Postgres down), a non-revoked key-set token is still admitted and a revoked one is still refused (the discriminating case against the gateway's fail-closed gate).
  - With the event bus down and the source up, a revocation written to the source is enforced after the next rehydration interval.
  - A `token.revoked` event applied while the source is failing is enforced.

### Tests that land with CODE-8

- Tier 1, gateway and Token Service flags: 119 and 3601 are fatal, and 120 and 3600 start. The playground mint gives `exp` per the SPEC-3 Edit 1 rule at both boundaries.
- Tier 1, `pkg/tokenservice` (`// spec: 10.2 (Authentication), 13.3 (Credential Flow)`): a rotation or scope-narrowing request with no `expires_in` receives `exp = now + CapabilityTokenTTL` when the subject outlives it and `subject_token.exp` when it does not; a request with `expires_in: 3000` and audience `lenny-ops` receives 3000 s; a request with `expires_in` above the per-dialect cap is clamped to the cap; an `actor_token` exchange with no `expires_in` receives `min(subject_token.exp, actor_token.exp, per-dialect cap)` and never the capability default; `NewServer` with a zero `CapabilityTokenTTL` returns an error.

### Tests that land with CODE-9

- Tier 2, `pkg/gateway/storage/issuedtokenstore` against Postgres (`// spec: 13.3 (Credential Flow), 8.2 (Delegation Mechanism)`): a revoked subject gives `subject_token_revoked` and a revoked actor gives `actor_token_revoked`, with no `issued_tokens` insert and one rejection audit row in each case. A listed subject `jti` with no row passes. Two concurrent rotations of one subject, run as randomized interleavings over repeated runs, commit exactly one new token, and the other fails with `subject_token_revoked` (the discriminating assertion for the double-rotation race). A rejection-audit write failure under fault injection returns `500 token_exchange_failed` with the reason in `detail`.
- Tier 1, `pkg/tokenservice/rotation_test.go`: the retried-rotation case expects `subject_token_revoked`.

### Tests that land with CODE-10, CODE-11, and CODE-12

- Tier 1: `/.well-known/jwks.json` returns 404 and `--jwks-publish` is an unknown flag.
- Tier 0: after CODE-12, no non-test reference to `RotatingVerifier`, `KMSSignerFromSealed`, or `key_retired` remains (`go build ./...` and a grep gate in the step).
- Tier 11: the generated alert files match `rules.go`, and `KMSSignQuotaApproaching` resolves to the DOCS-1 runbook section.

### Tests that land with CHART-1 to CHART-5

- Helm unittests in `charts/lenny/tests/`:
  - The master-key Secret and `--kms-master-key-file` render on all three deployments under `local` and are absent otherwise.
  - Rendering fails when `kms.signQuotaPerSecond` is unset on a cloud provider.
  - Under a cloud provider, `kms.endpointCIDR: 0.0.0.0/0` fails rendering, and a narrower CIDR renders one 443 KMS peer in each of `allow-gateway-egress` and `lenny-ops-egress`. Under `local`, neither peer renders.
  - `KMSSignQuotaApproaching` renders with `0.8 × quota` and is absent under `local`.
  - `--capability-token-ttl-seconds` renders, and the schema rejects 119.

### TEST-1 · Cross-process integration, end-to-end, and security tests

Targets: `tests/testinfra/gateway/gateway.go`, `tests/testinfra/tokenservice/tokenservice.go`, `tests/testinfra/opsprocess/opsprocess.go`, `tests/tier4_integration/tenant_deletion_phase4_test.go`, a new tier-4 test file, `tests/tier5_e2e_kind`, the tier-9 security suite, and `tests/spec-map.json`.

1. Harness: each stack writes one master key file and passes it to every gateway, Token Service, and `lenny-ops` process it starts.
2. Tier 4, new test (`// spec: 10.2 (Authentication), 4.3 (Token Service)`):
   - A gateway-minted bearer verifies on every gateway replica and on `lenny-ops`.
   - A `POST /v1/oauth/token` bearer verifies on every gateway replica.
   - Each Token Service replica accepts the other's subject token.
   - A token survives a restart of its signer.
   - Rotation: under `local` with mapped references, run steps 1 to 3 of SPEC-2 as three restarts with new alias values. Assert that old tokens verify after step 2, that new tokens verify after step 2, that an old token is refused with `unknown_kid` after step 3, that the re-minted admin credential keeps working after step 3, and that the replaced admin credential is refused on `lenny-ops` after step 2.
   - `lenny-ops` during a Postgres outage (`// spec: 25.4 (The lenny-ops Service), 25.15 (Failure Mode Analysis)`): mint two `lenny-ops`-audience tokens through `POST /v1/oauth/token`, revoke one, and wait until `lenny-ops` refuses it. Stop Postgres. The non-revoked token is accepted and the revoked one is refused with `401 token_revoked`. Restart `lenny-ops` with Postgres still down: both tokens are refused with `503 token_validation_unavailable`. Start Postgres: the non-revoked token is accepted again.
3. Tier 4: rewrite `lennyIssuedProbes` so each Lenny-issued bearer, including Token Service-issued tokens, is probed on every gateway replica. Remove the per-replica narrowing comment. The 0095 proposal files are not edited.
4. Tier 5, Kind: the full chart with `kms.provider=local` and two gateway replicas; a bearer minted through one replica's Service endpoint verifies through the other.
5. Tier 9 (`// spec: 10.2 (Authentication)`): algorithm confusion (HS256 over the public key), `alg: none`, a forged `kid` that matches no key (assert no KMS call through the stub's counter), and a token signed by a P-256 key outside the set. All are refused on the gateway, the Token Service, and `lenny-ops`.
6. Tier 9 (`// spec: 25.4 (The lenny-ops Service), 13.3 (Credential Flow)`): revocation enforcement on `lenny-ops`. A Lenny-issued token revoked through rotation, through §11.4 user invalidation, and through tenant deletion is refused on `lenny-ops` with `401 token_revoked` within the propagation budget, both with Redis up and, through the rehydration interval, with Redis stopped.
7. `tests/spec-map.json`: remove the entries for `pkg/auth/jwt/rotating_test.go`, the `JWKSHandler` tests in `pkg/auth/jwt/jwks_test.go`, and `tests/tier4_integration/jwks_test.go`, and add every test this proposal introduces.

## Edge cases and accepted failure modes

- **Local provider without a master key file.** A process started without `--kms-master-key-file` derives from a random seed, and its tokens verify only on itself. This is the developer's single-process case. The chart (CHART-2) and the harness (TEST-1) always pass a file.
- **A concurrent rotation whose loser had already signed.** The loser's signed token is discarded (CODE-9). One wasted KMS `Sign` call is the cost.

## Files touched on application (non-spec)

- pkg/kms/kms.go, pkg/kms/local.go, pkg/kms/aws/aws.go, pkg/kms/gcp/gcp.go, pkg/kms/azure/azure.go, pkg/kms/providerflags/providerflags.go, and their tests
- pkg/tenantkms/probemanager_test.go
- pkg/auth/jwt/jwt.go, kmssigner.go, keyset.go (new), rotating.go (deleted in CODE-12), jwks.go, breakersigner.go, multiverifier.go, and their tests
- pkg/gateway/audit/jwtaudit/jwtaudit.go and its test
- pkg/gateway/credentials/impersonation, pkg/gateway/externalapi/admintoken, pkg/gateway/mcpfabric/playground
- pkg/gateway/mcpfabric/delegation/childtoken/childtoken.go (doc comment)
- pkg/gateway/middleware/deprecation/deprecation.go
- pkg/gateway/metrics/gatewaymetrics/gatewaymetrics_signing.go
- pkg/gateway/storage/issuedtokenstore/issuedtokenstore.go and its tests
- pkg/tokenservice/tokenservice.go, rotation_test.go
- pkg/alerting/rules/rules.go
- pkg/chart/values/values.go, schema_test.go
- cmd/lenny-gateway/token_signing.go, httpsurface.go, auth_seams.go, flags.go, config_parse.go, authposture.go, auditpipeline.go, wiring_fields.go, adminrouter.go, user_revocation.go, user_revocation_test.go
- cmd/lenny-token-service/kms.go, server.go, flags.go
- cmd/lenny-ops/main.go, flags.go, revocation.go (new)
- pkg/gateway/middleware/auth (only if CODE-6 item 6 adds a hook)
- charts/lenny/values.yaml, values.schema.json, presets/dev.yaml, templates/gateway-deployment.yaml, templates/token-service-deployment.yaml, templates/ops-deployment.yaml, templates/ops-network-policies.yaml, templates/system-network-policies.yaml, templates/prometheusrule.yaml, a new local KMS master key Secret template, files/alerting-rules.yaml (generated), tests/
- docs/runbooks/jwt-key-rotation.md, docs/reference/configuration.md, docs/operator-guide/web-playground.md, docs/operator-guide/security.md, docs/operator-guide/agent-operability.md, docs/reference/error-catalog.md, docs/alerting/rules.yaml (generated)
- BUILD-GAPS.md, TEST-GAPS.md
- tests/testinfra/stubs/kms/provider.go, tests/testinfra/gateway/gateway.go, tests/testinfra/tokenservice/tokenservice.go, tests/testinfra/opsprocess/opsprocess.go, tests/tier4_integration/tenant_deletion_phase4_test.go, tests/tier4_integration/jwks_test.go (deleted), a new tier-4 test, tests/tier5_e2e_kind, the tier-9 suite, tests/spec-map.json
