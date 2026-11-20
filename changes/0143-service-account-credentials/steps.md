# Steps: 0143 Service account credentials

## Validation owed

| Item | Why it matters | Status | Step that waits |
| :-- | :-- | :-- | :-- |
| Entra accepts a client assertion with `aud` equal to the issuer identifier under RFC 10070, or still requires the token endpoint | The default of `assertionAudience` for Entra | Owed. The Entra page shows the token endpoint **[documented]**. | 0143.S2 |
| Entra, Google, and AWS accept an exchange JWT signed with RS256 by a 3,072-bit key under the exchange issuer, with a principal subject | O1 and the vendor profiles | Owed **[documented]** for RS256, **[unverified]** for the key size. | 0143.S3, 0143.S4 |
| How long Entra, Google, and AWS cache an issuer's key set | The default of `prePublishSeconds` | Owed. None of the pages read states it **[documented]**. | 0143.S3 |
| AWS accepts `urn:lenny:exchange:aws` as an IAM OIDC identity provider audience | The `awsWebIdentity` audience | Owed. AWS's page describes the audience as a client identifier of any value **[documented]**. | 0143.S4 |
| AWS `AssumeRoleWithWebIdentity` answers `AccessDenied` for a subject the trust policy does not name | SA-4's mapping for `awsWebIdentity` | Owed **[unverified]**. The key retrieval case of `InvalidIdentityToken` is **[documented]** on the IAM page. | 0143.S3, 0143.S4 |
| Google STS accepts an exchange JWT carrying `principal_type`, and an attribute condition on it holds | The documented Google condition | Owed. | 0143.S3 |
| Which providers read an `audience` parameter on the client credentials grant | Whether `audience` belongs on the type | Owed **[unverified]**. | 0143.S2 |
| Entra refuses an assertion signed with a key whose registered certificate has expired | The certificate expiry alert | Owed **[unverified]**. | 0143.S2 |
| RFC 10070 exists under that number and states the audience rule SA-2 cites, read on the RFC Editor's page before the specification lane cites it | The default `assertionAudience: issuer` and the client assertion's `typ` | Owed. The M3 readiness review could not confirm it from the tree. | 0143.S2 |

## Steps

### 0143.S1 Subject namespaces and principal-owned credentials
- status: Planned
- aliases: [SA-A, SA-6, SA-1]
- lanes: [specification, code, documentation]
- depends-on: [0151.S1, 0151.S3, 0127.S9, 0149.S6]

SA-6 and SA-1: the subject namespaces at the identity boundary, the subject-naming writes, and token issuance, including the refusal of a changed `caller_type` on `/v1/oauth/token` and the impersonation bearer's `caller_type`; the release rule, `principalTypes`, the principal-keyed rows, the service account routes with the self-write refusal, the reasons at create `no_row`, `principal_type`, and `trust_changed`, and the audit fields with `sessionOrigin`. `provider_refused` lands with 0143.S2 and `type_suspended` with 0143.S3, the steps that create those states. The step closes no finding.

Under decision 8 of [0117](../0117-gateway-package-layout/decisions.md), this step edits `spec/interfaces/gateway-seams.md` and `pkg/gateway/seam` for any gateway seam operation whose mechanism it changes, and names those operations in its digest. `GWSEAM-MaterializeChild` and `GWSEAM-CheckChildCredentials` return `ErrChildUserCredentialNotFound`, which `pkg/gateway/core/session/sessionserver` maps from the LLM credential router's `ErrUserCredentialNotFound` today, so the builder confirms whether 0151 has placed the `userCredential` child check behind them before this step lands, and edits them when it has.

SA-6 lands in this step because a service account must not own a credential while its subject can equal a person's. This step can land with `registered` alone.

This step adds the `~svc~` and `~agt~` namespace refusal to the person credential admin write route that 0151.S1 (UC-1) builds, as UC-1 **Named user.** of [`0151-user-credential-delivery`](../0151-user-credential-delivery/change.md) states. It also adds the namespace stripping to step 7 of the active-source check of [`0149-tenant-scoping-identity`](../0149-tenant-scoping-identity/change.md), which 0149.S6 builds: the check strips one `~svc~` or `~agt~` from the subject before it reads `~<source_id>~`, and treats a remainder with no `~` prefix as a bare deployment subject. Its `unit` and `security` tests cover a retired source's Lenny-issued `~svc~` token, which meets that source's `tokens_not_before`, and a deployment `~svc~` subject at a tenant whose `deploymentIssuerAllowed` is false, which meets the bare-subject refusal.

### 0143.S2 Client credentials source
- status: Planned
- aliases: [SA-B, SA-2, SA-4]
- lanes: [specification, code, documentation]
- depends-on: [0143.S1, 0127.S2, 0127.S6, 0148.S2 (part)]

SA-2 with the shared parts of SA-4: the client credentials grant, client assertions with the audience binding, generated keys and the one-key-per-row rule, discovery, the cache per row, the lease, the failure states, the stored error text, and the metrics.

It needs the mint path of 0127.S2 (EG-2), the tenant-registered public service check and the per-endpoint CA of 0127.S6 (EG-6), and, for a private token endpoint or issuer alone, the outbound hosts of 0148.S2 (OC-2), which depends on 0148.S1. This step adds a source.

### 0143.S3 Issuer exchange source
- status: Planned
- aliases: [SA-C, SA-3]
- lanes: [specification, code, documentation]
- depends-on: [0143.S2, 0127.S4, 0142.S2, 0142.S4 (part)]

SA-3: the exchange issuer with its key set, routes, and rotation, the exchange JWT, the `entraFederated`, `gcpSts`, `jwtBearer`, and `tokenExchange` profiles with their fixed or bound endpoints and audiences, the approval through `tokenTrust.allowedRecords`, and the egress design's revision 92 hooks. The operator's approval path, mirrored to the tenant's chain under SO-4a, waits on 0142.S4. This step adds a source.

### 0143.S4 Delivery for the new sources
- status: Planned
- aliases: [SA-D, SA-5]
- lanes: [specification, code, documentation]
- depends-on: [0143.S2, 0143.S3, 0151.S5, 0127.S4, 0142.S4 (part), 0151.S6 (part)]

SA-5's `file` for the new sources, and the `awsWebIdentity` profile with `delivery: metadata`, the `userCredentialMetadataDelivery` grant, the 900-second duration, and the exposure records.

It needs the `metadata` delivery of 0127.S4 (EG-4). The mirror of a `userCredentialMetadataDelivery` grant or withdrawal waits on 0142.S4 (SO-4a), and `leakScan` under `metadata` waits on the leak scanning of 0151.S6 (UC-6), which that change calls optional.
