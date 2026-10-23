# Spec changes: Lenny-issued bearer tokens verify only on the process that signed them

## Design (as the spec must state it)

Every Lenny-issued JWT is signed with one ES256 key that the KMS alias `platform:jwt-signing` names. The gateway and the Token Service both sign with it, through remote KMS `Sign` calls on a cloud provider and through a master-key-derived key on the local provider. Every gateway replica, every Token Service replica, and `lenny-ops` load the public keys of `platform:jwt-signing` and the optional `platform:jwt-verify` once at startup and verify locally by `kid`. Rotation state lives in the alias map and changes only through configuration rollouts. SPEC-1 states the algorithm, the providers, the reference immutability rule, the dev posture, the signing topology, and the recording of the gateway's session-creation mint. SPEC-2 states the key set and the rotation procedure.

Lenny-issued tokens have a fixed lifetime set at mint (SPEC-3). Signing runs before the write-before-issue transaction, and a revocation re-read inside that transaction discards a signed token whose subject or actor was revoked (SPEC-7). Provider throttling returns the existing retryable `KMS_SIGNING_UNAVAILABLE` without tripping the breaker (SPEC-4, SPEC-5), and §17.8 sizes the sign rate and the quota (SPEC-8). SPEC-6, SPEC-9, SPEC-10, and SPEC-11 correct the sentences elsewhere in the spec that the new model makes false.

SPEC-1, SPEC-4, SPEC-6, and SPEC-9 land in the same spec pass. Each pair contradicts the other until all of them land: SPEC-1 names a single signer, while the current §10.2 failure-mode paragraph (SPEC-4), §4.3 item 3 (SPEC-6), and the §17.4 KMS row (SPEC-9) each describe the old model.

## Edge cases and accepted failure modes

- **KMS unreachable at process start.** The process cannot load its key set and does not start (SPEC-2). A crashlooping pod is the accepted fail-closed cost of loading keys once.
- **KMS unreachable while running.** Verification is unaffected because the key set is in memory (SPEC-2). New mints fail with `KMS_SIGNING_UNAVAILABLE` once the breaker opens (SPEC-4).
- **Token with no `kid` or an unknown `kid`.** Rejected with `missing_kid` or `unknown_kid`, with no KMS call (SPEC-2). An attacker-chosen `kid` cannot drive KMS traffic.
- **A repointable key reference.** A process refuses to start when a `platform:jwt-*` alias names an AWS alias, a GCP CryptoKey without a version, or an Azure key without a version (SPEC-1). Without this rule a reference repointed outside Helm would make every new token fail on every verifier.
- **Quota throttling burst.** Throttled requests return a retryable 503 with `Retry-After` and do not count toward the breaker trip (SPEC-4). Mints degrade under a burst and do not stop fleet-wide.
- **Rotation step taken early.** Step 2 before step 1 has rolled out everywhere makes new tokens fail on processes that lack the new key. Step 3 before the `lenny-gateway` cap elapses rejects unexpired tokens the old key signed. SPEC-2 states the ordering and the wait.
- **Bootstrap admin credential across rotation.** Its lifetime exceeds the step-3 wait, so SPEC-2 step 2 re-mints it. An operator who skips that step is locked out at step 3.
- **Concurrent rotation of one subject.** The second transaction's step (1a) re-read sees the subject revoked by the first and rejects with `subject_token_revoked` (SPEC-7). A retried rotation of an already-rotated token fails the same way.
- **Signed but rejected token.** A token signed before the transaction whose subject or actor turns out revoked is discarded and never returned (SPEC-7).

## Staged edits

### SPEC-1 · spec/10_gateway-internals.md § 10.2 Authentication: "Session capability context", "Signing topology", the playground signer references, and the `typ` stamping sentence, and § 10.3 mTLS PKI: `playground.devTenantId` validation row; spec/16_observability.md § 16.7 Section 25 Audit Events: `token.exchanged`; spec/17_deployment-topology.md § 17.6 Packaging and Installation: `playground.devTenantId` bullet; spec/27_web-playground.md § 27.2 Placement and gating and § 27.3 Authentication: the dev playground JWT references

**Edit 1.** Replace the paragraph that begins "**Session capability context:** After authentication, the gateway mints a **signed JWT** via a pluggable `JWTSigner` interface. Two backends are supported:" together with its two bullets ("**Production:** KMS-backed signing (AWS KMS, GCP Cloud KMS, HashiCorp Vault Transit). …" and "**Dev mode:** Local HMAC-SHA256 key, enabled only when `LENNY_DEV_MODE=true` …") with:

```markdown
**Session capability context:** After authentication, the gateway mints a **signed JWT** via a pluggable `JWTSigner` interface. Every Lenny-issued JWT is signed with ES256 (ECDSA over P-256 with SHA-256). The JOSE header carries `alg: ES256` and a `kid`, and the `kid` is the [RFC 7638](https://www.rfc-editor.org/rfc/rfc7638) JWK thumbprint of the signing public key. The verifier of Lenny-issued tokens (see "Verifier key set and rotation" below) rejects a token whose `alg` is anything other than `ES256`, including `HS256` and `none`. The signing key is the key that the reserved alias `platform:jwt-signing` maps to in the KMS alias map (`kms.aliases`). The signer runs on the configured KMS provider (`kms.provider`):

- **Production:** AWS KMS (key spec `ECC_NIST_P256`), GCP Cloud KMS (algorithm `EC_SIGN_P256_SHA256`), or Azure Key Vault (algorithm `ES256`). With a cloud provider, the private key never exists in gateway or Token Service memory: the signer sends the SHA-256 digest of the JWT signing input to the KMS `Sign` operation and receives the signature. This eliminates the risk of key extraction from a compromised gateway process. The aliases `platform:jwt-signing` and `platform:jwt-verify` each map to an immutable key-version reference: an AWS key ID or key ARN, a GCP CryptoKeyVersion name ending in `/cryptoKeyVersions/<n>`, or an Azure key identifier with a non-empty `@<version>` suffix. A process refuses to start when either alias maps to a repointable reference, which is an AWS alias name or alias ARN, a GCP CryptoKey without a version, or an Azure key without a version.
- **Development:** With `kms.provider: local`, the local provider derives the ES256 key from its master key and the key reference the alias maps to (the alias name itself when `platform:jwt-signing` is unmapped), so processes that share one master key share one signing key. The gateway, the Token Service, and `lenny-ops` refuse to start with `kms.provider: local` in a production deployment. The local provider is for development only ([Section 17.4](17_deployment-topology.md#174-local-development-mode-lenny-dev)). Development and production run the same signer and verifier code, and no HMAC signer exists for Lenny-issued tokens.

**Signing topology.** The gateway and the Token Service sign with the one `platform:jwt-signing` key. The Token Service mints the tokens issued on `POST /v1/oauth/token` ([§4.3](04_system-components.md#43-token-service) item 3). The gateway mints in process, through its `JWTSigner`, the bootstrap admin credential ([§13.3](13_security-model.md#133-credential-flow) "Gateway-mediated bootstrap admin-credential rotation ordering"), platform-admin impersonation bearers, playground bearers, and `session_capability` tokens on the session-creation path. The gateway records each session-creation `session_capability` token in `issued_tokens`, together with its `token.exchanged` audit row ([§16.7](16_observability.md#167-section-25-audit-events)), in one transaction under statements (1), (2), and (3) of [§13.3](13_security-model.md#133-credential-flow) "Write-before-issue ordering" before it returns the token. A token that either component signs verifies on every gateway replica, every Token Service replica, and `lenny-ops`. The gateway and Token Service ServiceAccounts hold the KMS `Sign` and `GetPublicKey` permissions on the signing key, and the `lenny-ops` ServiceAccount holds `GetPublicKey` only.
```

**Edit 2.** In the paragraph "**Playground auth paths and `origin: "playground"` JWT claim.**", replace "they inherit KMS signing (production) or HMAC signing (dev mode, `LENNY_DEV_MODE=true`), key rotation, claim structure, and revocation semantics" with:

```markdown
they inherit the ES256 signing key, key rotation, claim structure, and revocation semantics
```

**Edit 3.** In the `dev` bullet of the playground per-mode mint points, replace "and issues a dev HMAC-signed session JWT (dev-mode `JWTSigner`) with the `origin: "playground"` claim attached" with:

```markdown
and issues a session JWT signed by the `JWTSigner`, with the `origin: "playground"` claim attached
```

**Edit 4.** In §10.2 "**JWT claims:**", in the paragraph under the `typ` bullet, replace "The gateway stamps `typ` inside the write-before-issue transaction alongside the other claims" with:

```markdown
The minting component stamps `typ` alongside the other claims when it signs the token
```

**Edit 5.** In spec/16_observability.md §16.7, in the `token.exchanged` entry, replace "and additionally emitted by the gateway with `exchange_type: admin_rotation` on its direct in-process bootstrap admin-credential rotation ([§13.3](13_security-model.md#133-credential-flow)), which mints and records the new token in the gateway process rather than through an RFC 8693 exchange. Payload fields: `exchange_type` (`admin_rotation` \| `delegation_mint` \| `lease_issue` \| `scope_narrow`)," with the following text. The entry stays one physical line.

```markdown
and additionally emitted by the gateway, which mints and records the token in process rather than through an RFC 8693 exchange, with `exchange_type: admin_rotation` on its bootstrap admin-credential rotation ([§13.3](13_security-model.md#133-credential-flow)) and `exchange_type: session_mint` on its session-creation `session_capability` mint ([§10.2](10_gateway-internals.md#102-authentication) "Signing topology"). Payload fields: `exchange_type` (`admin_rotation` \| `delegation_mint` \| `lease_issue` \| `scope_narrow` \| `session_mint`),
```

**Edit 6.** Replace "dev HMAC JWT" with "dev-mode playground JWT" at each of these sites, keeping each table row on one physical line: the `playground.devTenantId` row of the §10.3 "Gateway startup configuration validation (platform defaults)" table ("Names the tenant bound to the dev HMAC JWT `tenant_id` claim"); the §17.6 `playground.devTenantId` bullet ("tenant bound to the dev HMAC JWT `tenant_id` claim"); the §27.2 Helm table row for `playground.devTenantId` ("Tenant bound to the dev HMAC JWT `tenant_id` claim"); and the §27.3 `playground.authMode=dev` bullet ("The `tenant_id` claim on the dev HMAC JWT").

**Edit 7.** In §27.3 "**Mode-agnostic `origin: "playground"` JWT claim.**", in the `dev` bullet of the per-mode mint points, replace "issues a dev HMAC-signed session JWT ([§10.2](10_gateway-internals.md#102-authentication) dev-mode signer)" with:

```markdown
issues a session JWT signed by the `JWTSigner` ([§10.2](10_gateway-internals.md#102-authentication))
```

### SPEC-2 · spec/10_gateway-internals.md § 10.2 Authentication: "Key rotation" becomes "Verifier key set and rotation"

Replace the whole paragraph that begins "**Key rotation:** KMS backends support automatic key rotation natively." with:

```markdown
**Verifier key set and rotation.** The KMS alias map holds the rotation state. `platform:jwt-signing` names the key that signs, and the optional `platform:jwt-verify` names a second key that verifies and never signs. At startup, every gateway replica, every Token Service replica, and `lenny-ops` fetch, with the KMS `GetPublicKey` operation, the public key of `platform:jwt-signing` and, when it is mapped, of `platform:jwt-verify`, and hold them for the life of the process as a key set indexed by `kid`. A process that cannot load a mapped entry does not start. The key set is never refreshed, because a version-pinned public key is immutable; a key change reaches a process only through a restart with new alias values. A token whose header carries no `kid` is rejected with reason `missing_kid`, and a token whose `kid` is not in the key set is rejected with reason `unknown_kid`. Neither rejection triggers a KMS call. Verification never calls the KMS, so a KMS outage leaves verification in running processes unaffected.

Rotation is three configuration rollouts of the gateway, the Token Service, and `lenny-ops`. Each step begins only after the previous step's rollout has completed on every gateway, Token Service, and `lenny-ops` pod.

1. Create the new key and map `platform:jwt-verify` to it. Every process then verifies with both keys and signs with the old key.
2. Swap the two mappings, so that `platform:jwt-signing` names the new key and `platform:jwt-verify` names the old key. After the rollout, rotate the bootstrap admin credential with `lenny-ctl admin users rotate-token --user lenny-admin` ([§17.6](17_deployment-topology.md#176-packaging-and-installation)). Its lifetime exceeds the step-3 wait, so an admin credential the old key signed would stop verifying at step 3.
3. Remove `platform:jwt-verify` no earlier than the `lenny-gateway` per-dialect lifetime cap ([§13.3](13_security-model.md#133-credential-flow) "`exp` granularity and parent-expiry handling") after the step-2 rollout completes. That cap bounds every token the old key signed other than the bootstrap admin credential, including the gateway-minted impersonation and playground bearers, whose own caps are shorter.
```

### SPEC-3 · spec/10_gateway-internals.md § 10.2 Authentication: `expiry` claim and playground mint invariant 4; spec/27_web-playground.md § 27.2 Placement and gating and § 27.3.1 OIDC cookie-to-MCP-bearer exchange

**Edit 1.** In §10.2 "**JWT claims:**", replace the bullet "`expiry` (short-lived, refreshed by gateway on each interaction)" with:

```markdown
- `expiry`: fixed at mint and never extended; the gateway never re-mints a token on an interaction. A `session_capability` token minted by `POST /v1/playground/token` receives `exp = min(now + auth.capabilityTokenTtlSeconds, subject token exp, per-dialect cap)`, and its holder obtains a new token before `exp` as [§27.3.1](27_web-playground.md#2731-oidc-cookie-to-mcp-bearer-exchange) states. `auth.capabilityTokenTtlSeconds` defaults to `900` and is bounded `120 ≤ ttl ≤ 3600`. `a2a_delegation` child tokens keep the [§13.3](13_security-model.md#133-credential-flow) child formula. Every other token, including the `session_capability` token minted on the session-creation path, keeps its per-dialect cap, and the bootstrap admin credential has a 10-year lifetime and is retired by rotation ([§17.6](17_deployment-topology.md#176-packaging-and-installation)).
```

**Edit 2.** In §10.2 "**Playground mint invariants**", item 4 "**Origin and duration.**", replace "`exp = min(subject_token.exp, now + playground.bearerTtlSeconds)`" with:

```markdown
`exp` follows the "JWT claims" `expiry` rule
```

**Edit 3.** In §27.2, replace the Helm table row that begins "| `playground.bearerTtlSeconds` | `900` |" with this row, kept on one physical line:

```markdown
| `auth.capabilityTokenTtlSeconds` | `900` | Lifetime of the MCP bearer tokens minted by `POST /v1/playground/token`. Defined in [§10.2](10_gateway-internals.md#102-authentication). See [§27.3.1](#2731-oidc-cookie-to-mcp-bearer-exchange). |
```

**Edit 4.** In §27.3.1, in the sentence that begins "Default `expiresInSeconds` is `900` (15 min); configurable via `playground.bearerTtlSeconds`", replace "configurable via `playground.bearerTtlSeconds` (bounded `60 ≤ ttl ≤ 3600`)" with "configurable via `auth.capabilityTokenTtlSeconds` ([§10.2](10_gateway-internals.md#102-authentication))", and replace "(KMS-backed in production, HMAC in dev)" with:

```markdown
(ES256 with the key of the configured KMS provider)
```

**Edit 5.** In §27.3.1, in the "**Revocation write.**" bullet, replace "bounded by `⌈oidcSessionTtlSeconds / bearerTtlSeconds⌉ + 1`" with:

```markdown
bounded by `⌈oidcSessionTtlSeconds / auth.capabilityTokenTtlSeconds⌉ + 1`
```

**Edit 6.** In §27.3.1, in the **Auth by mode** table row for `dev`, replace "the `JWTSigner` is the dev HMAC backend" with "the `JWTSigner` signs ES256 ([§10.2](10_gateway-internals.md#102-authentication))", and replace "`exp = now + playground.bearerTtlSeconds`" with:

```markdown
`exp = now + auth.capabilityTokenTtlSeconds`
```

The row stays one physical line.

### SPEC-4 · spec/10_gateway-internals.md § 10.2 Authentication: "KMS signing failure mode"; spec/15_external-api-surface.md error table: `KMS_SIGNING_UNAVAILABLE`

**Edit 1.** Replace the whole paragraph that begins "**KMS signing failure mode:** If the KMS service is unavailable when the gateway attempts to mint a new session JWT" with:

```markdown
**KMS signing failure mode:** If a signer cannot obtain a signature, the mint fails with a retryable `KMS_SIGNING_UNAVAILABLE` error (HTTP 503, `retryable: true`) and no token is issued. The rule covers every mint path on the gateway and on the Token Service. Existing sessions and previously issued tokens are **unaffected**: their JWTs are already signed, and verification uses the key set loaded at startup (see "Verifier key set and rotation" above) rather than a live KMS call. Each signer process wraps its KMS signing calls in an automatic, in-memory circuit breaker (distinct from the operator-managed circuit breakers in [Section 11.6](11_policy-and-controls.md#116-circuit-breakers)). When the `JWTSigner` circuit breaker trips to open state (> 3 consecutive signing failures within 30s), every new mint on that process is rejected with `KMS_SIGNING_UNAVAILABLE` until the circuit resets. Provider throttling is backpressure rather than a signing failure: a sign request the KMS throttles returns `KMS_SIGNING_UNAVAILABLE` and does not count toward the trip. Every `KMS_SIGNING_UNAVAILABLE` response carries `Retry-After`, in whole seconds and at least 1: the remaining breaker cooldown when the breaker is open, and 1 second when the KMS throttled the request. The `KMSSigningUnavailable` alert ([Section 16.5](16_observability.md#165-alerting-rules-and-slos)) fires when the signing error rate exceeds its threshold, and the `KMSSignQuotaApproaching` alert fires when the fleet sign rate nears the configured provider quota.
```

**Edit 2.** In the §15 error table, replace the row that begins "| `KMS_SIGNING_UNAVAILABLE`   | `TRANSIENT` | 503         |" with this row, kept on one physical line:

```markdown
| `KMS_SIGNING_UNAVAILABLE`   | `TRANSIENT` | 503         | The JWT signer (gateway or Token Service) could not obtain a signature: its circuit breaker is open or the KMS throttled the request. The response carries `Retry-After`. Existing sessions are unaffected until token expiry. See [Section 10.2](10_gateway-internals.md#102-authentication). |
```

### SPEC-5 · spec/16_observability.md § 16.1 Metrics and § 16.5 Alerting Rules and SLOs: signing metrics and alerts

**Edit 1.** In §16.1, replace the row that begins "| KMS signing errors (`lenny_gateway_kms_signing_errors_total`, counter labeled by `kms_backend`, `operation`" with this row, kept on one physical line:

```markdown
| KMS signing errors (`lenny_gateway_kms_signing_errors_total`, counter labeled by `reason`: `inner` (the KMS returned an error other than throttling) or `rejected` (the circuit breaker refused the call); emitted by the gateway and Token Service signers; throttled sign requests are not counted; a sustained error rate trips the signer circuit breaker and drives the `KMSSigningUnavailable` warning alert ([Section 16.5](#165-alerting-rules-and-slos)); see [Section 10.2](10_gateway-internals.md#102-authentication)) | Counter         |
```

**Edit 2.** In §16.1, insert immediately after that row, on one physical line:

```markdown
| JWT sign requests (`lenny_jwt_sign_requests_total`, counter labeled by `component` (`gateway`, `token-service`) and `outcome`: `ok`, `throttled` (the KMS throttled the request), `circuit_open` (the circuit breaker refused it before any KMS call), or `error` (any other failure); one increment per sign request; feeds the `KMSSignQuotaApproaching` alert ([Section 16.5](#165-alerting-rules-and-slos)); see [Section 10.2](10_gateway-internals.md#102-authentication)) | Counter         |
```

**Edit 3.** In §16.5, replace the row that begins "| `KMSSigningUnavailable`          |" with this row, kept on one physical line:

```markdown
| `KMSSigningUnavailable`          | `rate(lenny_gateway_kms_signing_errors_total[30s]) > 1` for > 60s; a `JWTSigner` circuit breaker on the gateway or the Token Service has tripped open, and new mints on that process fail with `KMS_SIGNING_UNAVAILABLE`. Existing sessions are unaffected until JWT expiry. Operator should verify KMS reachability and the KMS IAM binding of the ServiceAccount for the component the alert's `job` label names. See [Section 10.2](10_gateway-internals.md#102-authentication). | Warning  |
```

**Edit 4.** In §16.5, insert immediately after that row, on one physical line:

```markdown
| `KMSSignQuotaApproaching`        | `sum(rate(lenny_jwt_sign_requests_total{outcome!="circuit_open"}[5m])) > 0.8 * kms.signQuotaPerSecond` for > 10 min; the fleet-wide rate of sign requests that reach the KMS exceeds 80% of the configured provider quota ([Section 17.8.2](17_deployment-topology.md#1782-capacity-tier-reference) "KMS Sign rate"). Rendered only when `kms.provider` is not `local`. Operator should raise the provider's asymmetric `Sign` quota and `kms.signQuotaPerSecond` together. See [Section 10.2](10_gateway-internals.md#102-authentication). | Warning  |
```

### SPEC-6 · spec/04_system-components.md § 4.3 Token Service: Role item 3 "Canonical token endpoint"

In item 3, replace "Session tokens, delegation child tokens, credential-lease tokens, operability-scope tokens, and the general admin and service-principal self-rotation grant" with "Delegation child tokens, credential-lease tokens, operability-scope tokens, and the general admin and service-principal self-rotation grant", keeping the rest of that sentence, including the parenthetical "(either via direct caller request or via internal Token Service calls from other gateway subsystems)". Insert immediately after that sentence, before "The bootstrap admin-credential (`lenny-admin`) initial mint":

```markdown
The gateway mints in process the tokens that [§10.2](10_gateway-internals.md#102-authentication) "Signing topology" lists, and both components sign with the one key that section defines.
```

The bootstrap admin carve-out sentence and the custody sentences under **Deployment** are unchanged.

### SPEC-7 · spec/13_security-model.md § 13.3 Credential Flow: token issuance, write-before-issue, `exp` granularity, and actor-token freshness; spec/08_recursive-delegation.md § 8.2 Delegation Mechanism: actor-token freshness

**Edit 1.** In §13.3 "**Write-before-issue ordering.**", replace the text from "The ordering is a single Postgres transaction with three statements" through "the caller receives `500 token_exchange_failed` with no token." with:

```markdown
The Token Service signs the token before it opens this transaction, outside the advisory lock. The ordering is a single Postgres transaction, executed in this order: (1) acquire the per-tenant audit advisory lock ([§11.7](11_policy-and-controls.md#117-audit-logging) item 3); (1a) SELECT the `issued_tokens` rows for the `subject_token` `jti` and, when an `actor_token` is present, the `actor_token` `jti`, and reject the exchange when either row has `revoked_at` set, with `invalid_grant` reason `subject_token_revoked` for the subject and `actor_token_revoked` for the actor; a token with no `issued_tokens` row passes this step; (2) INSERT the issued-token row into `issued_tokens` (carrying `jti`, hashed token, `tenant_id`, `sub`, `scope`, `audience`, `exp`); (3) INSERT the `token.exchanged` row into `audit_log`. The transaction is then `COMMIT`-ed. Only after the `COMMIT` succeeds does the gateway return `access_token` to the caller. When step (1a) rejects, the transaction skips steps (2) and (3) and writes the rejection audit row under the rejected-exchange discipline below. If any statement fails or step (1a) rejects, the signed token is **discarded and never returned**; on a statement failure the transaction rolls back and the caller receives `500 token_exchange_failed` with no token.
```

The sentences from "This closes the failover window:" to the end of the paragraph are unchanged.

**Edit 2.** In §13.3 "**`exp` granularity and parent-expiry handling.**", replace the text from "if `actor_token.exp <= now` the exchange is rejected with `invalid_grant` reason `actor_token_expired`." to the end of the paragraph with:

```markdown
if `actor_token.exp <= now` the exchange is rejected with `invalid_grant` reason `actor_token_expired`. A child token minted via `actor_token` receives `exp = min(requested_exp, subject_token.exp, actor_token.exp, per-dialect cap)`. The per-dialect lifetime caps are `lenny-gateway` 24 hours, `lenny-ops` 1 hour, and `llm-proxy` 1 hour. Signing precedes the write-before-issue transaction, so the `exp` clamp bounds a child to its subject's and actor's expiry.
```

**Edit 3.** In §13.3 "**Actor-token freshness under concurrent rotation.**", replace "the Token Service reads the `actor_token`'s `jti` against the revocation cache **inside** the same advisory-locked transaction that issues the child" with "the Token Service performs the Write-before-issue ordering step (1a) re-read of `issued_tokens.revoked_at` for the `actor_token`'s `jti` **inside** the advisory-locked transaction that issues the child", and replace "between the client's exchange submission and the Token Service's cache check" with:

```markdown
between the client's exchange submission and that re-read
```

**Edit 4.** In §8.2 "**Actor-token freshness under concurrent parent rotation/revocation.**", replace "The Token Service reads the `actor_token`'s `jti` against the cluster-wide revocation cache ([§13.3](13_security-model.md#133-credential-flow) "Token rotation and revocation") **inside** the same advisory-locked transaction that issues the child token." with:

```markdown
The Token Service performs the [§13.3](13_security-model.md#133-credential-flow) "Write-before-issue ordering" step (1a) re-read of `issued_tokens.revoked_at` for the `actor_token`'s `jti` **inside** the advisory-locked transaction that issues the child token.
```

**Edit 5.** In §13.3 "**Token issuance and rotation (RFC 8693 token exchange):**", replace "The bootstrap `lenny-admin` Secret rotation is the one exception: it is a gateway-direct in-process mint that does not go through `/v1/oauth/token`" with the following, keeping the rest of the sentence from " (the gateway signs the new admin token in process" onward:

```markdown
The gateway in-process mints that [§10.2](10_gateway-internals.md#102-authentication) "Signing topology" lists, including the bootstrap `lenny-admin` Secret rotation, do not go through `/v1/oauth/token`
```

### SPEC-8 · spec/17_deployment-topology.md § 17.8.1 Operational Defaults — Quick Reference and § 17.8.2 Capacity Tier Reference: lifetime, quota, and sign rate

**Edit 1.** In the §17.8.1 table, insert immediately after the row that begins "| `/v1/oauth/token` per-tenant global rate limit (`oauth.rateLimit.tenantPerSecond`) |", each row on one physical line:

```markdown
| Playground MCP bearer lifetime (`auth.capabilityTokenTtlSeconds`) | 900 s (15 min) | [§10.2](10_gateway-internals.md#102-authentication) |
| KMS asymmetric Sign quota (`kms.signQuotaPerSecond`) | No default; required when `kms.provider` is not `local` | [§17.8.2](#1782-capacity-tier-reference) |
```

**Edit 2.** In §17.8.2, insert immediately before the heading `### 17.8.3 Tier Promotion Guide`:

```markdown
**KMS Sign rate.** Every Lenny-issued token costs one KMS `Sign` call ([§10.2](10_gateway-internals.md#102-authentication)). The sustained fleet sign rate is approximately `session creation rate + delegation mint rate + concurrent playground bearer holders ÷ (auth.capabilityTokenTtlSeconds − 60) + admin, impersonation, and rotation mints`, where 60 seconds is the holder refresh lead ([§27.3.1](27_web-playground.md#2731-oidc-cookie-to-mcp-bearer-exchange)). The terms come from the [§16.5](16_observability.md#165-alerting-rules-and-slos) tier parameters and the `capacityPlanning.*` workload values. At Tier 3 with the default lifetime (200 sessions/s, about 15 delegations/s, and at most 10,000 playground bearer holders, the Tier 3 session ceiling), the sustained sign rate is about 230/s, and about 690/s when the rate triples in a session storm. Set `kms.signQuotaPerSecond` to the provider's documented asymmetric `Sign` quota for the account and region, and raise the provider quota to at least the storm figure; a deployment whose quota is below the storm figure returns retryable `503 KMS_SIGNING_UNAVAILABLE` responses during storms. ES256 verification adds tens of microseconds of gateway CPU per authenticated request.
```

### SPEC-9 · spec/17_deployment-topology.md § 17.4 Local Development Mode: "Embedded Mode: `lenny up`" dependency table KMS row, "State and resets", and "Dev mode guard rails"

**Edit 1.** Replace the dependency table row that begins "| KMS            | In-process random KEK (`kms.provider: local`)" with this row, kept on one physical line:

```markdown
| KMS            | Local provider (`kms.provider: local`)                           | Keyed by a master key Secret the chart generates once per install; derives KEKs and the ES256 JWT signing key from that master key ([§10.2](10_gateway-internals.md#102-authentication)). Refused in a production deployment; operators MUST NOT use this for production |
```

**Edit 2.** Under "Dev mode guard rails", replace "Dev mode relaxes security defaults (TLS, JWT signing) for local convenience" with "Dev mode relaxes security defaults (TLS) for local convenience", and replace item 3's "including TLS bypass, JWT signing bypass, and any future relaxations." with:

```markdown
including TLS bypass and any future relaxations. JWT signing is not relaxed in dev mode ([§10.2](10_gateway-internals.md#102-authentication)).
```

**Edit 3.** Under "State and resets", in the first bullet, replace "The application data stores (Postgres, Redis, KMS, artifacts)" with "The application data stores (Postgres, Redis, artifacts)".

### SPEC-10 · spec/13_security-model.md § 13.2.4 `lenny-system` Namespace NetworkPolicies: Gateway and `lenny-ops` rows; spec/25_agent-operability.md § 25.4 NetworkPolicy: `lenny-ops-egress`; spec/17_deployment-topology.md § 17.1 Kubernetes Resources: NetworkPolicies row

**Edit 1.** In the §13.2.4 Gateway row, Egress Allowed column, replace "`kube-system` CoreDNS (UDP/TCP 53); external HTTPS (TCP 443, rendered" with the following text. The row stays one physical line.

```markdown
`kube-system` CoreDNS (UDP/TCP 53); KMS endpoint (TCP 443, CIDR `{{ .Values.kms.endpointCIDR }}`), rendered when `kms.provider` is not `local`; the chart refuses to render with any other `kms.provider` while `kms.endpointCIDR` is `0.0.0.0/0` or `::/0`, so a cloud deployment sets it to the subnet of the provider's private KMS endpoint, because NetworkPolicy allows are additive and an unrestricted peer would cancel the excepts of the external HTTPS rule (NET-057); external HTTPS (TCP 443, rendered
```

**Edit 2.** In the §13.2.4 `lenny-ops` row, Egress Allowed column, replace "see [Section 25.4](25_agent-operability.md#254-the-lenny-ops-service) `lenny-ops-egress`). The chart emits exactly one" with the following text. The row stays one physical line.

```markdown
see [Section 25.4](25_agent-operability.md#254-the-lenny-ops-service) `lenny-ops-egress`); KMS endpoint (TCP 443, CIDR `{{ .Values.kms.endpointCIDR }}`) for `GetPublicKey` on the [Section 10.2](10_gateway-internals.md#102-authentication) signing keys, rendered under the Gateway row KMS condition. The chart emits exactly one
```

**Edit 3.** In §25.4, in the `lenny-ops-egress` YAML block, insert immediately before the comment line "  # Webhook delivery (egress to internet) — host filtering enforced at the application layer (SSRF checks).":

```yaml
  # KMS endpoint for GetPublicKey on the §10.2 JWT signing keys. Rendered when
  # kms.provider is not local; §13.2.4 (Gateway row) states the CIDR rule.
  - to: [{ ipBlock: { cidr: "{{ .Values.kms.endpointCIDR }}" } }]
    ports: [{ protocol: TCP, port: 443 }]
```

**Edit 4.** In the §17.1 NetworkPolicies row, replace "Egress allows Postgres, Redis, MinIO, Prometheus." with:

```markdown
Egress allows Postgres, Redis, MinIO, Prometheus, and the KMS endpoint when `kms.provider` is not `local`.
```

### SPEC-11 · spec/18_build-sequence.md § 18.33 Phase 14 — Comprehensive security hardening; spec/25_agent-operability.md § 25.4 The `lenny-ops` Service: Startup and Readiness, Authentication, and `GET /v1/admin/me` Response "Degradation", and § 25.15 Failure Mode Analysis: "Postgres down" and "Postgres + Redis both down" rows

**Edit 1.** In §18.33, replace the bullet "- JWT signing-key rotation procedure per [§13.3](13_security-model.md#133-credential-flow): JWK Set publication endpoint, key-overlap window, rollover audit event emission (`platform.jwt_signing_key_rotated`), and the operator runbook." with:

```markdown
- JWT signing-key rotation procedure per [§10.2](10_gateway-internals.md#102-authentication) "Verifier key set and rotation": the three-rollout alias rotation, the bootstrap admin credential rotation in step 2, and the operator runbook.
```

**Edit 2.** In §25.4 "Authentication", replace "`lenny-ops` validates JWT tokens using the same OIDC issuer and JWKS endpoint." with:

```markdown
`lenny-ops` validates upstream IdP tokens against the same OIDC issuer as the gateway. It validates Lenny-issued tokens against the verifier key set defined in [Section 10.2](10_gateway-internals.md#102-authentication) "Verifier key set and rotation" and applies to them the revocation check of [Section 13.3](13_security-model.md#133-credential-flow) "Token rotation and revocation" and "Authoritative durability for revocation", including the refusal to validate while Postgres is unreachable.
```

**Edit 3.** In §25.4 "Startup and Readiness", replace "`lenny-ops` starts its HTTP listener immediately." with:

```markdown
`lenny-ops` starts its HTTP listener immediately after it loads the verifier key set of [Section 10.2](10_gateway-internals.md#102-authentication) "Verifier key set and rotation".
```

**Edit 4.** In the §25.15 table, append this sentence to the end of the Impact cell of the "**Postgres down**" row and of the "**Postgres + Redis both down**" row, keeping each row on one physical line:

```markdown
`lenny-ops` refuses Lenny-issued tokens for the duration ([Section 25.4](#254-the-lenny-ops-service) "Authentication").
```

**Edit 5.** In §25.4 "`GET /v1/admin/me` Response", sub-heading "Degradation", replace "If Postgres is unreachable, `/v1/admin/me` still returns" with:

```markdown
If Postgres is unreachable, `/v1/admin/me` still returns for a caller authenticated by an upstream IdP token (a Lenny-issued token is refused, per "Authentication" above)
```

## Spec files touched

- spec/04_system-components.md (SPEC-6)
- spec/08_recursive-delegation.md (SPEC-7)
- spec/10_gateway-internals.md (SPEC-1, SPEC-2, SPEC-3, SPEC-4)
- spec/13_security-model.md (SPEC-7, SPEC-10)
- spec/15_external-api-surface.md (SPEC-4)
- spec/16_observability.md (SPEC-1, SPEC-5)
- spec/17_deployment-topology.md (SPEC-1, SPEC-8, SPEC-9, SPEC-10)
- spec/18_build-sequence.md (SPEC-11)
- spec/25_agent-operability.md (SPEC-10, SPEC-11)
- spec/27_web-playground.md (SPEC-1, SPEC-3)
