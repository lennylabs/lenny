# Adversarial security review: 0143-service-account-credentials revision 1

Reviewed on 2026-11-15. The review covers `changes/0143-service-account-credentials/change.md` revision 1 and the changes it made to `0151-user-credential-delivery` (rev 39), `0127-egress-and-git` (rev 91), `0124-connector-removal` (rev 6), `0152-user-git-identity` (rev 44), and `0149-tenant-scoping-identity` (rev 41), as shown by `git diff 8702041e8 -- designs/`. The spec sections §10.2 (Session ownership, `typ`, playground mint) and §13.3 (token exchange, impersonation, write-before-issue ordering) and `0144-session-derive` SD-P1 were also read. No file under the repository was edited.

Findings are ranked by severity. Citations name a file and a heading.

---

## F1. High (Critical if O3 is adopted): the exchange JWT can be sent anywhere a tenant admin chooses, so it works as a signing oracle for any enrolled principal

**Design text at fault.**
- `0143-service-account-credentials`, "SA-3 The `issuerExchange` source", **The type.**, **The exchange JWT.** (the `aud` row: "`jwtAudience`, a single value ... the authorization server's issuer identifier or token endpoint for `jwtBearer` and `tokenExchange`"), and **Exchange profiles.** (the `jwtBearer` and `tokenExchange` rows post to a tenant-set `tokenEndpoint`).
- "Threat model", SAP-3 ("never reaches ... a destination other than the exchange endpoint in the token trust record").
- "Threats", the row "Tenant admin controlling configuration" ("Under `issuerExchange` the rewrite needs no secret"), which states only that "a new token reaches a host she added".
- SA-1, **Write routes.**, and SA-3, **The row.** ("verified at write with one exchange unless `verify: false`").

**Problem.** The type author sets `jwtAudience` and `tokenEndpoint` independently. Nothing ties the JWT's audience to the endpoint that receives it. UCP-5 protects stored secrets when a record changes, but an `issuerExchange` row holds no secret. A rewrite therefore costs the attacker nothing, and the Token Service signs a fresh JWT on the attacker's request.

**Attack.** Carol (tenant admin), an `agent` caller holding `tenant-admin` (O4 notes that these callers write credentials), or a stolen provisioning-backend bearer with type-write and `manage_user_credentials` does the following:
1. Create the type `x` with `profile: jwtBearer`, `tokenEndpoint: https://attacker.example/token`, and `jwtAudience: api://AzureADTokenExchange`. Any vendor audience works equally, such as the Google pool provider URL or `sts.amazonaws.com`.
2. `PUT /v1/admin/service-accounts/report-bot/credentials/x` with `verify: true`. Verification signs an exchange JWT with `sub: lenny:acme:service:report-bot` under the tenant's federation key and posts it to the attacker. No session of `report-bot` is needed.
3. Replay the JWT within `assertionLifetimeSeconds` (up to 600 s) at Entra (as the FIC's `client_assertion`), at Google STS, or at AWS `AssumeRoleWithWebIdentity`. Each of these trusts issuer, subject, and audience, and none of them can see `credential_type`, except Google when a condition is configured on it. The attacker receives `report-bot`'s cloud identity outside Lenny, with no `credential.mint` trail tied to a session.

Under O3 the same steps work with `PUT /v1/admin/users/alice/credentials/x` and `sub: lenny:acme:user:alice`, so the tenant admin acts as Alice at every provider that trusts Alice's subject. A tenant that uses SO-2 role separation keeps this power only if `tokenTrust.allowedRecords` is in force, and the design does not make that the default for `issuerExchange`.

**Fix.**
- Fix the endpoint of each vendor profile in platform code: `login.microsoftonline.com/<tid>/oauth2/v2.0/token` for `entraFederated`, `sts.googleapis.com` for `gcpSts`, and the STS regional endpoints for `awsWebIdentity`.
- On `jwtBearer` and `tokenExchange`, require `aud` to equal the record's `tokenEndpoint`, or the issuer that RFC 8414 discovery verified for that endpoint. Refuse every reserved vendor audience (`api://AzureADTokenExchange`, `//iam.googleapis.com/...`, `https://iam.googleapis.com/...`, `sts.amazonaws.com`) on these profiles.
- Make an `issuerExchange` type write a security-admin act under SO-2, or put it under `tokenTrust.allowedRecords` by default.
- Restate the threat row and SAP-3 to name the residual that remains after the fix.

## F2. High (conditional on a subject collision): only the credential row guards against subject collision; session ownership does not

**Design text at fault.** `0143-service-account-credentials`, SA-1, **The release rule.**, second paragraph ("subjects of different principal types can share a string ... nothing forces apart"), and **Named service account.** ("A subject that is a person's is not detectable at write"). The rule is inherited from `0151-user-credential-delivery`, UCP-1.

**Problem.** The design states that a human and a service account can share a subject string. It defends only the credential lookup. Every other control keys on the bare subject:
- Spec §10.2 **Session ownership.** rule 1 sets the owner to "the subject". Rule 5 lets only the owner act on a session, which covers sending input, deriving, attaching, and answering elicitations.
- `0144-session-derive` SD-P1 gives a derived session its source's owner.
- TS-4 invalidation reads the subject, and §12.8 erasure deletes by subject.

**Attack.** Human H and service S share the canonical subject `X`. This can happen when `subject_claim` maps to a value a person can choose, such as `preferred_username`, or when an identity source reissues identifiers. H calls `POST /v1/sessions/{id}/messages` on S's running session, which holds `graph-reports`. The session ownership check passes, so H's input drives an agent that injects S's token. H can also derive from S's session. In the other direction, S's bearer acts on H's sessions. Separately, invalidating or erasing H also stops or deletes S's rows.

**Fix.** Either enforce disjointness at the identity boundary or carry the caller type through every control:
- Disjointness: keep a per-tenant registry of (canonical subject, caller type) written the first time each subject is seen, and refuse a token whose caller type differs for that subject. Alternatively, namespace service subjects (for example `~svc~`).
- Carrying the caller type: key ownership (§10.2 rules 1, 2, and 5), derive, invalidation, and erasure on (caller type, subject).

The design must also state which of these it relies on.

## F3. Medium: the shared federation issuer lets an agent-readable federation JWT pass any trust that does not pin the exact subject

**Design text at fault.**
- SA-3, **The issuer (O1).** (the third bullet reuses the tenant's existing federation registration).
- The paragraph after it ("never satisfy the same trust condition").
- SAP-4.
- The "Threats" row "An agent holding a federation token".
- `0127-egress-and-git`, **Exchange by the Token Service.**

**Problem.** The `principal_type` claim helps only at Google. AWS and Entra cannot condition on custom claims. The egress design's own review row records "AWS cannot condition on a tenant claim". The design separates subjects, but that protects only trusts that name an exact subject. AWS `StringLike` on `lenny:acme:*`, an AWS trust conditioned on `aud` alone, a Google `principalSet` over the pool, and a generic AS that maps any `sub` from the issuer all admit both kinds of JWT. Two further gaps exist:
- The audiences collide. AWS uses `sts.amazonaws.com` for both kinds.
- The egress group source-address condition does not separate the two paths, because the pod's SDK and the Token Service leave through the same group.

**Attack.** A prompt-injected agent under `delivery: federation` reads `/run/netagent/credentials/<name>/token` and presents it at the AWS role created for `report-bot`, whose trust policy uses `StringLike sub lenny:acme:*` and the shared `sts.amazonaws.com` audience. The agent then holds `report-bot`'s role.

**Fix.**
- Prefer the O1 alternative: a dedicated exchange issuer path per tenant, so each key set and registration serves one use.
- At minimum, refuse any `jwtAudience` that equals a federation binding audience in the same tenant, and the reverse.
- Restate SAP-4 so that the claim half holds only at Google.
- Have the provider setup page state that AWS and Entra need an exact `sub` condition.

## F4. Medium: O3 at Entra does not act as the user, and O3 hands the tenant admin control of the mapping from users to provider identities

**Design text at fault.** SA-3, **Human callers (O3).**; "Open decisions" O3; the `entraFederated` row of **Exchange profiles.**

**Problem.**
- `entraFederated` is the client credentials grant. A human row therefore receives an app-only token carrying the application's permissions, which are usually tenant-wide (for example Graph `Mail.Read` over every mailbox). The provider never sees the user's identity.
- The tenant admin writes human rows through the person route without the user acting. A provider trust granted per pool or per attribute lets the admin attach any user to any identity.
- With F1, the admin can act as any O3 user directly.
- At a generic AS that maps `sub` to a directory user, a user subject amounts to full impersonation.

**Fix.**
- Keep humans out of the first version, which is the O3 alternative. If they are admitted, require the user's own act to enroll (`self`) and security-admin approval, and refuse `entraFederated` for human principals.
- Land F1's fix first.

## F5. Medium: O6 `metadata` reuses a gate granted for bearer files, and the design understates its blast radius

**Design text at fault.** SA-5, **`metadata` (O6).** and **The gate class.**; "Open decisions" O6 (cost: "15 minutes at least"); "Configuration by scope" (`awsWebIdentity.durationSeconds` "from 900 to the role's maximum"); `0127-egress-and-git`, **Gates.**

**Problem.**
- A tenant that already holds `userCredentialDelivery` for bearer files gains AWS signing credentials with no new platform-admin decision.
- The duration reaches the role maximum, which AWS sets up to 12 hours.
- The credentials work from anywhere, and they can chain into other roles.
- SA-5 states that exposure records, `lockEgressToHosts`, and `leakScan` apply for `file`, and says nothing of them for `metadata`.
- The design does not state how a loopback fetch is attributed to a slot on a pool with several slots.

**Fix.**
- Add a separate grant, or a grant parameter, for `metadata`.
- Cap the duration at 900 to 3,600 seconds.
- Send a session `Policy` that scopes the credentials down.
- Apply exposure records with the credentials' expiry.
- State per-slot attribution through the slot UID.
- Restate the cost as up to the duration cap, usable from anywhere, with role chaining possible.

## F6. Medium: "a stolen service bearer cannot plant a token" does not hold for service accounts that hold `manage_user_credentials`

**Design text at fault.** SA-1, **Type check.** and **Owner check.**; "Alternatives rejected", the `self` row.

**Problem.** The service account route does not refuse a writer whose subject is the named service account. Two kinds of service account can therefore write their own rows through the admin route:
- the provisioning backend, which needs `manage_user_credentials` to do its job;
- any service account that holds `tenant-admin`.

A stolen bearer of such an account can also plant rows for every other service account in the tenant.

**Fix.**
- Refuse a write when the writer's subject equals the named subject.
- Allow a write grant that is limited to particular service accounts.
- Correct the stated property.

## F7. Low: the client assertion audience can name one server while the request goes to another

**Design text at fault.** SA-2, the `issuer` and `assertionAudience` rows and **Discovery.** Discovery and its RFC 8414 issuer check run only when `tokenEndpoint` is absent. When both `issuer` and `tokenEndpoint` are set by hand, `aud` can name an honest issuer while the request goes to a hostile endpoint. This is the audience-injection pattern that RFC 10070 addresses. It is exploitable only when the same `privateKey` is uploaded to two rows, because UCP-5 blocks reuse of a stored key after a record edit.

**Fix.** Under `assertionAudience: issuer`, require discovery, or a metadata check that `token_endpoint` belongs to the issuer. Refuse an uploaded key whose public key fingerprint is already held by another row.

## F8. Low: CR-2 now gives `agent` callers a service subject

**Design text at fault.** `0124-connector-removal`, the `sub` and `user`, `principal_type` term rows (rev 6). An operator service can no longer tell an `agent` caller that holds `tenant-admin` from an ordinary service account, and the two collapse if their subjects ever share a string.

**Fix.** Use `lenny:<tenant>:agent:<id>` and `principal_type: agent`.

## F9. Low: key rotation and error mapping

**Design text at fault.** `0127-egress-and-git` **Issuer** (rotation keeps only the previous key); SA-4 **Failure.**

**Problem.** A new `kid` is used as soon as it exists. A provider that caches the key set refuses JWTs signed with it. The design maps that refusal through `InvalidIdentityToken` and `AADSTS70021`, so after `refusalGraceSeconds` (600 s) every `issuerExchange` row of the tenant moves to `user_act_required`. AWS's page describes `IDPCommunicationError` as the error for an unreachable identity provider, and `InvalidIdentityToken` as "could not be validated". The design's reading of `InvalidIdentityToken` as a key-set fetch failure is not on that page. `ExpiredToken` is unmapped.

**Fix.** Publish the next key at least one provider cache period before signing with it. Map `ExpiredToken` to `unavailable`.

## F10. Low: audit and posture gaps

- The design copies the provider's `error_description`, which is hostile input under C9, into the audit event and the admin notification. It must be escaped wherever it is rendered and scrubbed of the row's own client identifier, secret, and JWT values. SAP-9 depends on this scrub.
- Key generation, activation, discard, `retry`, and verify-at-write exchanges have no named audit event. Each one signs with the tenant's federation key or uses a stored secret.
- Posture does not report `issuerExchange` types with a tenant-registered endpoint, or with a human principal type.
- `credential.mint` does not distinguish a playground-origin session, minted from a pasted service bearer under `apiKey` mode, from the job's own sessions.
- The design does not state that `clientCredentials` tokens usually carry application permissions that reach the whole provider tenant. It also does not state that `requestRules` presets were written for user tokens, or that the existing `userCredentialDelivery` grant now covers service tokens.

---

## Checked and found sound

- **Release rule.** The Token Service reads the principal and caller type from `pkg/binding`, and no client or pod input names a principal. The principal type is sealed as AAD, so a row moved to the other type fails to decrypt. `agent` and an empty caller type receive nothing.
- **Caller type origin.** The caller type comes only from `service_claim` or `auth.serviceClaim` (TS-2 allowlist). It is copied on `/v1/oauth/token` and never elevated. `a2a_delegation` requires the caller to be the `subject_token`'s identity. `tenant_mismatch` refuses cross-tenant exchange. `service_token` is never minted externally.
- **Impersonation.** Bearers cannot create sessions (§13.3 step 1a rejects impersonation rows on every create) and are refused on every write route. Session input is refused under UCP-3.
- **Delegated children and crossings.** A child copies its root's principal and needs a lease that names the binding (UCP-8). TS-3 crossings stay within one tenant and are lease-gated.
- **Derive.** Only the owner may derive, and the derived session takes the source's owner. F2 is the exception.
- **Playground.** The caller type is preserved. Use there is equivalent to possessing the bearer.
- **Cross-tenant isolation.**
  - Rows, cache keys, and connection reuse are keyed on tenant.
  - Each tenant has its own issuer and key set, protected by a tombstone (SAP-5).
  - The route acts only within the caller's tenant, and a `platform-admin` has no path into a customer's service account.
- **Stored secrets.** UCP-5 (any record change makes stored `clientCredentials` secrets and keys unusable) defeats endpoint retargeting for those secrets. Secrets and keys are write-only, envelope-sealed, and kept in the Token Service.
- **Discovery.** Discovery runs only at write and is fixed in the record. RFC 8414 issuer equality is checked. Calls are HTTPS and pass the C8 and C9 registration checks. They follow no redirects, and they are bounded at 10 s and 64 KiB through `lenny-egress` with the route named. Run-time re-discovery is refused.
- **Tokens and errors.** Refresh tokens are discarded. `token_type` is checked, and DPoP and `N_A` are refused. `invalid_client` maps to `user_act_required` with no link prompt.
- **Cache key.** The cache is keyed on tenant, principal type, principal, type, environment, generation, and digest, sealed as AAD, and never shared with the egress mint cache.
- **Pods.** UCP-2, `retireOnRelease`, the refusal on unmarked releases, and per-slot attribution apply whatever the principal type. Recycled and used pods stay refused.
- **Subject separation.** The binding regex `^[a-z][a-z0-9-]{0,62}$` and the tenant identifier regex `^[a-zA-Z0-9_-]{1,128}$` keep federation subjects (two colons) disjoint from principal subjects. The CR-2 identity key set (ES256, `/identity/`) stays separate from the federation key set.
- **Writes and lifecycle.**
  - `self` is refused for service callers.
  - Security-role, invalidated, and erased subjects are refused on the service route.
  - TS-4 invalidation stops releases.
  - The VCS host policy refuses every source on `userAuthorization: required` API hosts.

## External claims (checked live on 2026-11-15 unless noted)

- **RFC 10070.** Published November 2026 as a Proposed Standard, updating RFCs 7521, 7522, 7523, and 9126. Client-authentication `aud` MUST be the issuer as its sole value, and the token endpoint MUST NOT be used. A grant may name the issuer or the token endpoint. `typ` `client-authentication+jwt` is a SHOULD. The vulnerability was disclosed in January 2025. The RFC states no `jti` replay rule. The design is correct on each point. Entra's page still shows the token endpoint as the audience, so the default `assertionAudience: issuer` remains a validation owed.
- **Entra.**
  - Correct: the RS256 "tested" statement, the limit of 20 FICs, the 600-character fields, the single audience, the absence of wildcards, and `AADSTS70021` during propagation.
  - Note: a FIC on a managed identity accepts only `api://AzureADTokenExchange`, and the result of `entraFederated` is an app-only token (F4).
- **Google.** RS256 and ES256 are supported, and `exp` may be at most 24 h after `iat`. Both are correct. The 127-character `google.subject` limit is not on the page fetched and was not re-verified.
- **AWS `AssumeRoleWithWebIdentity`.**
  - Correct: the call is unsigned; `RoleSessionName` takes 2 to 64 characters of `[\w+=,.@-]`; `DurationSeconds` runs from 900 to the role maximum (at most 43,200); CloudTrail records the subject, with advice against PII; and RS and ES algorithms are accepted.
  - Not correct: the reading of `InvalidIdentityToken` as a key-set fetch failure (F9).
- **RFC 6749 §§2.3.1, 4.4, 5.1, and 5.2, RFC 8693 §2, RFC 8707, and RFC 8414 §3.3.** The statements are consistent with the RFCs as I know them, but I did not fetch them live.
