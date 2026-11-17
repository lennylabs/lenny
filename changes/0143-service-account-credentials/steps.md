# Steps: 0143 Service account credentials

## Validation owed

| Item | Why it matters | Status |
| :-- | :-- | :-- |
| Entra accepts a client assertion with `aud` equal to the issuer identifier under RFC 10070, or still requires the token endpoint | The default of `assertionAudience` for Entra | Owed. The Entra page shows the token endpoint **[documented]**. |
| Entra, Google, and AWS accept an exchange JWT signed with RS256 by a 3,072-bit key under the exchange issuer, with a principal subject | O1 and the vendor profiles | Owed **[documented]** for RS256, **[unverified]** for the key size. |
| How long Entra, Google, and AWS cache an issuer's key set | The default of `prePublishSeconds` | Owed. None of the pages read states it **[documented]**. |
| AWS accepts `urn:lenny:exchange:aws` as an IAM OIDC identity provider audience | The `awsWebIdentity` audience | Owed. AWS's page describes the audience as a client identifier of any value **[documented]**. |
| AWS `AssumeRoleWithWebIdentity` answers `AccessDenied` for a subject the trust policy does not name | SA-4's mapping for `awsWebIdentity` | Owed **[unverified]**. The key retrieval case of `InvalidIdentityToken` is **[documented]** on the IAM page. |
| Google STS accepts an exchange JWT carrying `principal_type`, and an attribute condition on it holds | The documented Google condition | Owed. |
| Which providers read an `audience` parameter on the client credentials grant | Whether `audience` belongs on the type | Owed **[unverified]**. |
| Entra refuses an assertion signed with a key whose registered certificate has expired | The certificate expiry alert | Owed **[unverified]**. |

## Steps

### 0143.S1 Subject namespaces and principal-owned credentials
- status: Planned
- aliases: [SA-A, SA-6, SA-1]
- lanes: [specification, code, documentation]
- depends-on: [0151.S1, 0151.S3, 0127.S9, 0149.S6]

SA-6 and SA-1: the subject namespaces at the identity boundary, the subject-naming writes, and token issuance; the release rule, `principalTypes`, the principal-keyed rows, the service account routes with the self-write refusal, the reasons at create, and the audit fields. The step closes no finding.

SA-6 lands in this step because a service account must not own a credential while its subject can equal a person's. This step can land with `registered` alone.

### 0143.S2 Client credentials source
- status: Planned
- aliases: [SA-B, SA-2, SA-4]
- lanes: [specification, code, documentation]
- depends-on: [0143.S1, 0127.S2]

SA-2 with the shared parts of SA-4: the client credentials grant, client assertions with the audience binding, generated keys and the one-key-per-row rule, discovery, the cache per row, the lease, the failure states, the stored error text, and the metrics.

It needs the mint path of 0127.S2 (EG-2). This step adds a source.

### 0143.S3 Issuer exchange source
- status: Planned
- aliases: [SA-C, SA-3]
- lanes: [specification, code, documentation]
- depends-on: [0143.S2, 0127.S4, 0142.S2]

SA-3: the exchange issuer with its key set, routes, and rotation, the exchange JWT, the `entraFederated`, `gcpSts`, `jwtBearer`, and `tokenExchange` profiles with their fixed or bound endpoints and audiences, the approval through `tokenTrust.allowedRecords`, and the egress design's revision 92 hooks. This step adds a source.

### 0143.S4 Delivery for the new sources
- status: Planned
- aliases: [SA-D, SA-5]
- lanes: [specification, code, documentation]
- depends-on: [0143.S2, 0143.S3, 0151.S5, 0127.S4]

SA-5's `file` for the new sources, and the `awsWebIdentity` profile with `delivery: metadata`, the `userCredentialMetadataDelivery` grant, the 900-second duration, and the exposure records.

It needs the `metadata` delivery of 0127.S4 (EG-4).
