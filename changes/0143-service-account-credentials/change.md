# Service account credentials

Status: `Draft`, revision 2, 2026-11-15. Revision 1 recorded the owner's answers of 2026-11-15 to the question of service accounts, which "Owner decisions" quotes, and designed what they adopt: a service account owns credentials under the `userCredential` machinery of [`0151-user-credential-delivery`](../0151-user-credential-delivery/change.md), a `clientCredentials` source runs the OAuth 2.0 client credentials grant for it, and an `issuerExchange` source exchanges a JWT that Lenny's own issuer signs at a provider that trusts that issuer. Revision 2 applies the adversarial security review of revision 1, kept as [`evidence/service-account-credentials-design/security-review.md`](evidence/security-review.md), and the owner's answer of 2026-11-15 to its summary, "O1-O7: go with your recommendations". Vendor profiles post only to fixed endpoints with fixed audiences, the generic profiles bind the JWT's audience to the issuer whose token endpoint they call, and an `issuerExchange` type needs the tenant security admin's approval. Service, agent, and human callers hold subjects in disjoint namespaces (SA-6). Exchange JWTs move to an exchange issuer of their own, which reverses revision 1's recommendation for O1 because of review finding 3. `issuerExchange` serves service accounts alone, a service account cannot write its own rows, AWS credentials reach a pod under `delivery: metadata` behind a grant of their own with a 900-second duration, and `registered` types admit persons alone unless a tenant opts in. No decision is open. Nothing here is built or staged, and nothing in `spec/` changes until a proposal lands it through the change-proposal pipeline. The design reads against every design under `designs/` as implemented, in particular [`0151-user-credential-delivery`](../0151-user-credential-delivery/change.md) revision 40, [`0127-egress-and-git`](../0127-egress-and-git/change.md) revision 92, [`0124-connector-removal`](../0124-connector-removal/change.md) revision 7, [`0152-user-git-identity`](../0152-user-git-identity/change.md) revision 45, [`0149-tenant-scoping-identity`](../0149-tenant-scoping-identity/change.md) revision 42, [`0142-security-oversight`](../0142-security-oversight/change.md) revision 58, [`0150-tenant-security-settings`](../0150-tenant-security-settings/change.md) revision 9, [`0148-tenant-outbound-connector`](../0148-tenant-outbound-connector/change.md) revision 32, [`0128-forensics-evidence-preservation`](../0128-forensics-evidence-preservation/change.md) revision 35, and [`0141-runtime-routing`](../0141-runtime-routing/change.md) revision 19. The revisions named for the user credential, egress, connector removal, VCS layer, tenant scoping, and security oversight designs apply the changes this design asks of them. Proposals carry the labels SA-1 to SA-6, requirements SAR-n, security properties SAP-n, the owner's decisions D1 to D3, SD1 to SD3, and O1 to O7, and the proposals that land them SA-A to SA-D under "Proposal split and order". "Revision history" at the end records the revisions.

Programs: [v1](../programs/v1.md). The owner's final cross-review of the moved designs is pending, so the status is `Draft`.

Each factual claim carries one of these tags where the source matters:

| Tag | Meaning |
| :-- | :-- |
| **[source]** | Read in the Lenny tree, the specification, or a design under `designs/` on 2026-11-15. |
| **[documented]** | Read on a standards document or a vendor page on 2026-11-15, and not tested. "Sources" lists the pages. Nothing in this document was run against a provider. |
| **[unverified]** | Reasoned from a page without a statement that settles it, or not read at all. "Validation owed" lists each one that matters. |
| **[design]** | What this design requires. Nothing implements it. |

## Problem

"Terms", after "Scope", defines the vocabulary this document uses.

The owner asked on 2026-11-15: "what about service accounts? A user may be a legitimate service account but have no human behind it".

Under [`0151-user-credential-delivery`](../0151-user-credential-delivery/change.md) as implemented before its revision 39, which applies this design, a service account cannot hold a credential of its own **[source]**:

- Its property UCP-1 releases a `userCredential` token only when the session row's `caller_type` is `human`. The caller types are `human`, `service`, and `agent`, copied from the subject token, and a delegated child copies its root's value.
- "Sessions with no human user" under its UC-3 states that a service session receives no user token.
- Its decision 14 sends service sessions to the tenant's stored-secret bindings of [`0127-egress-and-git`](../0127-egress-and-git/change.md).
- Its write rules refuse a service subject: the admin write route refuses a subject that is a service, and `POST /v1/credentials` admits only a caller whose `caller_type` is `human`.

A stored-secret binding belongs to the tenant. A binding with `scope: always` attaches to every session of the tenant, and one with `scope: onRequest` attaches to any session whose plan names it, so nothing ties such a credential to one service account. A tenant whose report service and whose deployment service each need their own Microsoft Graph or Google Cloud identity can give each one a binding, and any session of the tenant can still name either.

Two further gaps follow. A service account that authenticates at a provider with an OAuth client, rather than with a static token, needs a party to run the OAuth 2.0 client credentials grant and hold the client secret. A provider that can trust an external OIDC issuer, as Microsoft Entra, Google Cloud, and AWS can, needs no stored secret at all, and Lenny's existing `delivery: federation` gives that trust only to a binding, with the JWT readable by the agent.

The security review of revision 1 found that Lenny's subjects did not keep a person apart from a service account with the same subject string, that a tenant admin could direct an exchange JWT to any server, and that sharing the federation issuer let an agent-readable federation JWT pass a provider trust meant for a service account. Revision 2 closes each of these.

## Scope

**Added:**

- Principal-owned credentials (SA-1): a service account owns rows of a `userCredential` type, and the Token Service releases a row only to sessions whose row names that service account.
- `source: clientCredentials` (SA-2): the Token Service runs the client credentials grant of RFC 6749 section 4.4 with a client identifier and a client secret or a private key that a service account's row holds.
- `source: issuerExchange` (SA-3): the Token Service signs a JWT for the session's service account under the tenant's exchange issuer and exchanges it at a provider that trusts that issuer, through a vendor profile with a fixed endpoint and audience, or through the JWT bearer grant of RFC 7523 or the token exchange grant of RFC 8693 with the audience bound to the endpoint.
- The provider calls, caching, and failure states both new sources share (SA-4), and their deliveries and gates (SA-5).
- Subject namespaces (SA-6): a service account's and an `agent` caller's Lenny subject carries a reserved prefix, so no person shares a subject with either.

**Kept unchanged:**

- The `registered` source, including long-lived bearer tokens a backend writes, which the owner confirmed stay supported.
- The `oauth2Link` source, which stays human-only because a link needs a person's browser consent.
- UC-3's refusal of pods that can serve someone else, the `retireOnRelease` mark, the impersonation refusal, the delegation lease rule, and one token per principal and type.
- `delivery: federation` of the egress design, which keeps its per-binding subject, its JWT in the pod, and its own issuer and key set.

**Out of scope:**

- `agent` callers, which stay refused (O4).
- Persons under `issuerExchange` (O3).
- Mutual TLS client authentication of RFC 8705 (O5).
- Lenny signing AWS requests in `netagent`. AWS is reached through `delivery: metadata` (O6).
- Revoking an issued access token at a provider. Neither grant issues a token Lenny tracks for revocation, and the token lapses at its expiry.
- Microsoft Entra national clouds and the AWS regions under `amazonaws.com.cn`, whose hosts the vendor profiles do not list. A later revision adds them as values of the profile's fixed host list.

## Terms

| Term | Meaning |
| :-- | :-- |
| Principal | The identity a session runs as, which the session row records: its user identifier and its `caller_type`. A `human` principal is a person, and a `service` principal is a service account. A delegated child carries its root's principal. |
| Service account | A principal whose bearer carries `caller_type: service`: a system principal authenticating through the client credentials path of §10.2, or a token a tenant identity source marks as a service through its `service_claim` ([`0149-tenant-scoping-identity`](../0149-tenant-scoping-identity/change.md)). No person stands behind it. |
| Lenny subject | The `subject` half of Lenny's user key `(tenant_id, subject)`. For a person it is the canonical subject of [`0149-tenant-scoping-identity`, "Several IdPs in one tenant, and namespacing"](../0149-tenant-scoping-identity/change.md#several-idps-in-one-tenant-and-namespacing). For a service account it is `~svc~` followed by the canonical subject, and for an `agent` caller `~agt~` followed by it (SA-6). |
| Subject namespace | The set of Lenny subjects of one caller type: `~svc~` for service accounts, `~agt~` for `agent` callers, and every other subject for persons (SA-6). |
| Principal-owned credential | A row of a `userCredential` type keyed on its principal's type and Lenny subject, which the Token Service releases only to sessions of that principal. The identifiers keep the stem `userCredential` and `user_credential` for both principal types (decision SD3). |
| `principalTypes` | A field of a user credential type that lists which principal types may own its rows: `human`, `service`, or both (SA-1). |
| Provider call | A call the Token Service makes to a token endpoint or exchange endpoint to obtain an access token for a principal under `clientCredentials` or `issuerExchange` (SA-4). |
| Client assertion | A JWT that the Token Service signs with a service account's private key to authenticate it at a token endpoint under `private_key_jwt` (RFC 7523 section 2.2) **[documented]**. |
| Exchange issuer | An OIDC issuer of the Token Service per tenant, beside the federation issuer and the identity issuer, `https://<lenny>/exchange/<tenant-id>`, whose RS256 key set signs exchange JWTs and nothing else (SA-3). |
| Exchange JWT | A JWT that the Token Service signs with the tenant's exchange key, naming the session's service account, and presents at a provider under `issuerExchange` (SA-3). It never leaves the Token Service except to the endpoint its type binds. |
| Exchange profile | The request layout an `issuerExchange` type uses: the vendor profiles `entraFederated`, `gcpSts`, and `awsWebIdentity`, whose endpoints and audiences platform code fixes, and the generic profiles `jwtBearer` and `tokenExchange`, whose audience is bound to the endpoint (SA-3). |
| Principal subject | The `sub` of an exchange JWT: `lenny:<tenant>:service:<principal-id>`, where `<principal-id>` is the service account's canonical subject, the format identity assertions use. |
| `refusalGraceSeconds` | How long consecutive refusals of an exchange count as `unavailable` before the row moves to `user_act_required` (SA-4). |
| `userCredentialMetadataDelivery` | The per-tenant platform grant that admits `delivery: metadata` on a `userCredential` binding of an `awsWebIdentity` type (SA-5). |

### Terms from other designs and the platform

| Term | Meaning |
| :-- | :-- |
| `caller_type` | The kind of caller behind a bearer, `human`, `service`, or `agent`, copied from the subject token on exchange and never elevated ([§13.3 Credential Flow](../../spec/13_security-model.md#133-credential-flow)). [`0151-user-credential-delivery`](../0151-user-credential-delivery/change.md) records it on the session row, and `pkg/binding` returns it. |
| `agent` caller | A caller whose token carries `caller_type: agent`, which [§25.1](../../spec/25_agent-operability.md#251-design-philosophy-and-agent-model) assigns to the service accounts of AI agents that operate Lenny with the `platform-admin` or `tenant-admin` role. |
| Canonical subject | A customer identity source's user key, `~<source_id>~<value of subject_claim>`, or a deployment source's `sub` as it stands ([`0149-tenant-scoping-identity`, "Several IdPs in one tenant, and namespacing"](../0149-tenant-scoping-identity/change.md#several-idps-in-one-tenant-and-namespacing)). |
| User credential type | A tenant object naming a kind of per-principal token, its source, and the hosts where it works ([`0151-user-credential-delivery`, "UC-1"](../0151-user-credential-delivery/change.md#uc-1-credential-types-and-the-registered-source)). |
| `userCredential` binding | A credential binding whose `secret` names a type, so each session receives its own principal's token ([UC-3](../0151-user-credential-delivery/change.md#uc-3-the-usercredential-binding)). |
| Token Service | The Lenny service that stores credentials, mints and releases tokens, and answers the pod credential API ([§4.3 Token Service](../../spec/04_system-components.md#43-token-service)). No other component decrypts a stored credential. |
| Pod credential API | The gRPC API on which `netagent` asks the Token Service for a binding's token, defined as I8 in [`0127-egress-and-git`, "Interfaces"](../0127-egress-and-git/change.md#interfaces). |
| `netagent` | The egress proxy container in each agent pod, which attaches binding secrets to requests under `delivery: inject`, writes them to files under `delivery: file`, and serves provider credentials on loopback under `delivery: metadata` ([`0127-egress-and-git`, "Delivery modes"](../0127-egress-and-git/change.md#delivery-modes)). |
| `lenny-egress` and the mint path | The central egress proxy, and the route the Token Service's provider calls take through it with the tenant asserted, bounded in time, response size, and redirects ([`0127-egress-and-git`, "Credentials"](../0127-egress-and-git/change.md#credentials)). |
| Token trust record | The record of everything that decides where a stored token can go, whose digest is sealed with each stored value. Any change makes the stored value unusable until a new write or a revert ([UC-1, "Token trust record"](../0151-user-credential-delivery/change.md#token-trust-record)). |
| Delivery ceiling | The highest delivery a stored token may take, set on the type and held in the token trust record: `inject` or `file` for a type ([UC-1](../0151-user-credential-delivery/change.md#types)), and `metadata` for an `awsWebIdentity` type (SA-5). |
| `registeredBy` | A type field naming who may write its rows: `self`, the row's owner through `POST /v1/credentials`, and `service`, a caller holding `manage_user_credentials` through an admin route ([UC-1, "Who can write"](../0151-user-credential-delivery/change.md#who-can-write)). |
| `user_act_required` | The one state of a stored row that needs a new act. For a link it is a relink, and for a written row it is a new write ([UC-1](../0151-user-credential-delivery/change.md#token-trust-record)). |
| Link prompt | The URL-mode elicitation the gateway raises when a human's account link stops working ([UC-8](../0151-user-credential-delivery/change.md#uc-8-link-prompts)). It does not apply to rows of this design. |
| Federation issuer | The Token Service's OIDC issuer per tenant, `https://<lenny>/oidc/<tenant-id>`, with its own RS256 key set, under which `delivery: federation` issues a 15-minute JWT with the subject `lenny:<tenant>:<binding>` that the runtime exchanges at a provider ([`0127-egress-and-git`, "Delivery modes"](../0127-egress-and-git/change.md#delivery-modes), **`federation`**). It signs no exchange JWT. |
| Identity issuer | The Token Service's second issuer per tenant, `https://<lenny>/identity/<tenant-id>`, whose ES256 key set signs identity assertions for operator services and nothing else ([`0124-connector-removal`, "CR-2 Identity assertion"](../0124-connector-removal/change.md#cr-2-identity-assertion)). |
| `metadata` delivery | The egress design's delivery in which `netagent` serves short-lived provider credentials on loopback in a provider's metadata endpoint format, with an AWS plugin shipped first ([`0127-egress-and-git`, "Delivery modes"](../0127-egress-and-git/change.md#delivery-modes)). |
| Gates | Per-tenant platform grants for opt-ins that place a credential in the agent's reach: `agentReadableDelivery` for a tenant's credential under `file`, `federation`, or `metadata`, `userCredentialDelivery` for a principal's token under `file`, and `userCredentialMetadataDelivery` for a service account's AWS credentials under `metadata` ([`0127-egress-and-git`, "Delivery modes"](../0127-egress-and-git/change.md#delivery-modes), **Gates.**). |
| Exposure records | Durable rows the Token Service writes for each generation of a value released into the agent's reach ([UC-5, "Exposure records"](../0151-user-credential-delivery/change.md#exposure-records)). |
| `tokenTrust.allowedRecords` | The tenant security record's list of token trust records a `security-admin` approved, each as its digest and content. Adding an entry is a weakening that waits out the tenant's delay ([`0142-security-oversight`, "SO-2"](../0142-security-oversight/change.md#so-2-tenant-security-record-and-posture)). |
| Impersonation bearer | A token a `platform-admin` obtains through `/v1/admin/impersonation` to act as a principal ([§13.3](../../spec/13_security-model.md#133-credential-flow), **Platform-admin impersonation**). |
| Post-chain point | The point after the `PreRoute` chain and the revision choice at which every check that depends on the final runtime and pool runs, before the pod claim ([RT-2](../0141-runtime-routing/change.md#post-chain-point)). |

### Labels from other designs

| Labels | Design | What they name |
| :-- | :-- | :-- |
| UC-1 to UC-8, UCP-1 to UCP-12, UCR-n, and decisions 4, 14, 41, and 43 | [`0151-user-credential-delivery`](../0151-user-credential-delivery/change.md) | Types and `registered`, account links, the binding, request rules, `delivery: file`, leak scanning, add-ons, and link prompts; its security properties and requirements; decision 4 on a tenant admin writing a token under a user's name; decision 14 on sessions with no user, which this design supersedes; decision 41 on pods that can serve someone else; and decision 43, which records this design's change to UCP-1. |
| EG-2, EG-4, EG-9, P2, P3, and P4 | [`0127-egress-and-git`](../0127-egress-and-git/change.md#steps) | The pod network agent with the mint path, credential delivery, and the single write path for tenant opt-ins; and the egress security properties that keep long-lived credentials out of pods (P2), keep egress credentials away from the agent (P3), and keep tenants apart (P4) ([Security properties](../0127-egress-and-git/change.md#security-properties)). |
| CR-2 | [`0124-connector-removal`](../0124-connector-removal/change.md#cr-2-identity-assertion) | The identity assertion, whose `sub` format this design's principal subject shares. |
| SO-2, SO-4a, and SP2 | [`0142-security-oversight`](../0142-security-oversight/change.md#so-2-tenant-security-record-and-posture) | The tenant security record with its guardrails and posture, the mirror of a platform admin's write to the tenant's chain, and the property that keeps a tenant admin from acting on a security role holder. |
| TS-4, TS-6, and TS-8 | [`0149-tenant-scoping-identity`](../0149-tenant-scoping-identity/change.md#user-lifecycle-ts-4) | User lifecycle and invalidation, which reads the subject and so covers a service account; identity sources, the verifier registry, and canonical subjects; and the SCIM server. |
| SD-P1 | [`0144-session-derive`](../0144-session-derive/change.md) | The rule that a derived session takes its source's owner. |
| Service identities | [`0152-user-git-identity`, "Service identities"](../0152-user-git-identity/change.md#service-identities) | The `serviceIdentities` list of a VCS pool, under which a listed service session takes the App binding. |
| C8 and C9 | [`0148-tenant-outbound-connector`](../0148-tenant-outbound-connector/change.md#security-properties) | A registered host cannot shadow a platform-reserved host (C8), and content from a customer network is hostile (C9). |

## High-level design

A tenant runs automated jobs as service accounts: a nightly report, a deployment pipeline, or a triage bot for incoming tickets. Each job starts Lenny sessions under its own service account and needs its own identity at an outside service, such as Microsoft Graph or Google Cloud Storage. Before this design a tenant could give such a job only a tenant-wide secret, which any session of the tenant could also name. After it, the job's credential belongs to the service account, and only that service account's sessions receive it.

The credential can come from a stored token, an OAuth client, or a provider's trust in Lenny. The tenant's backend can write a token for the service account, as it can for a person today. The backend can instead write the service account's OAuth client, its client identifier and either a client secret or a private key, and the Token Service then asks the provider for short-lived access tokens with it whenever a session needs one. Or the tenant can set the provider up to trust Lenny's exchange issuer, and the Token Service then signs a short JWT naming the service account and trades it at the provider for an access token, so Lenny stores no secret at all. In every case the long-lived material stays in the Token Service, and the agent's requests receive a short-lived token through `netagent`, as they receive any injected credential. AWS returns signing credentials that `netagent` cannot attach, so a tenant whose platform admin grants it reaches AWS through `delivery: metadata`, which places 15-minute credentials within the agent's reach.

The rules that protect a person's token protect a service account's token too. A session receives only the token of the principal on its own session row. The session runs on a pod that has served no session and serves no later one. An operator impersonating the principal cannot give the session input. A child session receives the token only when its delegation lease names the binding.

Three rules keep the new sources from becoming a way around those protections. A service account's Lenny subject begins with `~svc~`, which no person's subject can, so session ownership, derive, invalidation, and erasure never join a person with a service account. An exchange JWT goes only to the endpoint its profile fixes or binds, under an issuer that signs nothing else, and a tenant's `security-admin` approves each `issuerExchange` type before it is used. A service account cannot write its own rows.

A person can use `registered` and an account link as before. The issuer exchange serves service accounts alone (O3).

```
 Tenant backend ──(service account row: token, OAuth client, or provider parameters)──> Gateway ──┐
                                                                                                  ▼
       Token Service: type rows keyed on principal type and Lenny subject, sealed to the token trust record
         │  clientCredentials: client assertion or secret ──┐
         │  issuerExchange: exchange JWT (exchange key) ────┼──> lenny-egress ──> fixed or bound endpoint
         │                                                  │                        │
         │  <────────── access token (cached per row) ──────┘<───────────────────────┘
         ▼ pod credential API: access token, hosts, attach
 Pod: runtime ──(request)──> netagent ──(token attached)──> lenny-egress ──> type hosts (graph.microsoft.com)
```

### Worked example

`acme` runs a nightly report job under the service account `report-bot`, which authenticates to Lenny through §10.2's client credentials path with `caller_type: service`. Its Lenny subject is `~svc~report-bot`. The job reads mail statistics from Microsoft Graph.

1. Carol, `acme`'s tenant admin, registers an application for `report-bot` in `acme`'s Entra tenant and grants it the Graph application permission it needs. She defines the type `graph-reports` with `source: clientCredentials`, `principalTypes: [service]`, `clientAuth: private_key_jwt`, the Entra token endpoint, `scope: https://graph.microsoft.com/.default`, `hosts: [graph.microsoft.com]`, and `registeredBy: [service]`. **[design]**
2. `acme`'s provisioning backend calls `POST /v1/admin/service-accounts/report-bot/credentials/graph-reports/keys` with the application's client identifier. The Token Service generates a key pair, keeps the private key, and returns a self-signed certificate. Carol uploads the certificate to the Entra application, and the backend activates the key. Activation makes one token request, which succeeds, so the row commits. **[design]**
3. The job creates a session naming the binding `graph-reports` on the default pool. The gateway reads `caller_type: service` and the Lenny subject `~svc~report-bot` from the principal, writes them on the session row, finds an active row for `report-bot` under the type, and writes `retireOnRelease` on the claim at bind. **[design]**
4. The agent runs `curl https://graph.microsoft.com/v1.0/reports/...` with the placeholder. `netagent` asks the pod credential API. The Token Service reads `~svc~report-bot` and `service` from the session row, finds no cached token, signs a client assertion, posts the client credentials request to the Entra token endpoint through `lenny-egress`, receives a one-hour access token, caches it for the row, and releases it with `cacheUntil` of 300 seconds. `netagent` attaches it. **[design]** The Entra request layout is **[documented]**.
5. Bob, a developer at `acme`, starts a session naming `graph-reports`. His session row names Bob with `caller_type: human`, and the type admits only service accounts, so his session receives nothing, and with `required: true` its creation is refused with `USER_CREDENTIAL_NOT_FOUND`. **[design]**
6. Months later Entra refuses the client assertion with `invalid_client`, because someone removed the certificate. The row moves to `user_act_required` with the reason `client_rejected`, the release is refused, a tenant notification and the audit event `user_credential.provider_refused` name the row, and the job's next session is refused at creation. No link prompt is raised, because no person can answer it. The backend generates and activates a new key. **[design]**

The same job could reach Google Cloud Storage with no stored secret. Carol configures a workload identity pool provider in `acme`'s Google project that trusts the exchange issuer `https://lenny.acme.com/exchange/acme`, grants a role to the principal whose subject is `lenny:acme:service:report-bot`, and defines a type with `source: issuerExchange`, `profile: gcpSts`, and the project number, pool, and provider. Dave, `acme`'s `security-admin`, reviews the type's token trust record and adds it to `tokenTrust.allowedRecords`, which takes effect after the tenant's weakening delay. The Token Service then signs an exchange JWT for `report-bot` whose audience is that pool provider and posts it to `sts.googleapis.com`, the one host the profile allows.

### The parts

- **Principal-owned credentials (SA-1).** UCP-1 releases the token of the principal on the session row for `human` and `service` callers, a type's `principalTypes` lists which principals may own its rows, admin routes write rows for a service account, and no service account writes its own.
- **`clientCredentials` (SA-2).** A service account's row holds an OAuth client, and the Token Service runs the client credentials grant with `client_secret_basic`, `client_secret_post`, or `private_key_jwt`, with `scope`, `audience`, and `resource`, and with token endpoint discovery.
- **`issuerExchange` (SA-3).** The Token Service signs an exchange JWT for the service account under the tenant's exchange issuer and exchanges it through an exchange profile whose endpoint and audience the author cannot point elsewhere, after a `security-admin` approves the type.
- **Provider calls (SA-4).** The route, bounds, caching per row, refresh before expiry, and the mapping of provider errors to `user_act_required` or `unavailable`.
- **Delivery (SA-5).** `inject` by default, `file` behind `userCredentialDelivery` (O2), and `metadata` for the AWS profile behind `userCredentialMetadataDelivery` (O6).
- **Subject namespaces (SA-6).** `~svc~` and `~agt~` prefixes on the Lenny subjects of service and `agent` callers, enforced where a subject enters Lenny, where a service account's subject is first written, and where Lenny issues a token.

## Requirements

Alice and Bob are developers at `acme`, Carol is its tenant admin, and Dave is its `security-admin`. `report-bot` and `deploy-bot` are service accounts of `acme`. `globex` is another tenant.

- SAR-1. A tenant can deliver a credential that belongs to one service account to that service account's sessions and to no other session.
- SAR-2. A tenant's backend can write a token, an OAuth client, or provider parameters for a named service account through an admin route, with no person involved.
- SAR-3. A service account's OAuth client secret or private key never reaches a pod, a gateway log, or any destination other than the token endpoint in the type's token trust record.
- SAR-4. A tenant can reach a provider that trusts Lenny's exchange issuer with no secret stored in Lenny.
- SAR-5. A provider refusal that needs a new act moves the row to `user_act_required` and tells the tenant, and a transient failure does not.
- SAR-6. A service account's token takes every protection a person's token takes: the pod refusals, `retireOnRelease`, the impersonation refusal, the delegation lease rule, request rules, the delivery ceiling, the token trust record, and audit.
- SAR-7. A session of `deploy-bot` never receives a token of `report-bot`, a human session never receives a service account's token, and a service session never receives a human's token.
- SAR-8. An `agent` caller receives no principal-owned token.
- SAR-9. Each provider call is audited in `credential.mint` with the principal type and the source, and carries no secret.
- SAR-10. No configuration a tenant admin writes makes the Token Service send an exchange JWT to an endpoint other than the vendor endpoint its profile fixes, or the token endpoint of the issuer its audience names.
- SAR-11. A person and a service account never hold the same Lenny subject, so session ownership, derive, invalidation, and erasure never treat one as the other.
- SAR-12. A service account cannot create, change, or remove its own credential rows.
- SAR-13. An `issuerExchange` type takes effect only after the tenant's `security-admin`, or a platform admin with a justification, approves its token trust record.

## SA-1 Principal-owned credentials

**The release rule.** UCP-1 becomes: a session receives only the token of the principal on its session row, and only when that row's `caller_type` is `human` or `service` and equals the principal type the token's row records. A delegated child's row copies its root's principal and `caller_type`. An empty value or `agent` receives nothing. No client or pod input names another principal, and the Token Service holds this from its own read through `pkg/binding` **[design]**.

A person and a service account never share a Lenny subject, because SA-6 places every service account's subject in the `~svc~` namespace and refuses that prefix on every human token. The principal-type check stays as a second control: a row records its principal type, and the type is sealed with the value, so a row moved to the other principal type fails to decrypt.

**Types.** A user credential type gains `principalTypes`:

| Source | Allowed values | Default |
| :-- | :-- | :-- |
| `registered` | `[human]`, `[service]`, or `[human, service]` | `[human]`, so every existing type is unchanged and a tenant opts a type into service accounts explicitly (O7) |
| `oauth2Link` | `[human]` | `[human]`, fixed, because a link needs a person's browser consent |
| `clientCredentials` | `[service]` | `[service]`, fixed, because an OAuth client stands for an application |
| `issuerExchange` | `[service]` | `[service]`, fixed (O3) |

`principalTypes` is in the token trust record, so narrowing or widening it makes the type's stored rows unusable until a new write or a revert, as any record change does. A type write naming a value the source does not allow, such as `human` on an `issuerExchange` type, is refused with `422 VALIDATION_ERROR` naming the field. A row write whose principal type the type does not list is refused the same way.

**Rows.** The type-row table of UC-1 keys on tenant, principal type, Lenny subject, `typeId`, and environment. The principal type joins the additional data sealed with each envelope, beside the tenant, role, principal, `typeId`, environment, digest, and the fields written with the value, so a row moved to the other principal type fails to decrypt. `written_by` records the writer's Lenny subject and caller type, as it does today.

**Write routes.** The admin route for a person stays, and a route for a service account sits beside it:

```
PUT    /v1/admin/service-accounts/{subject}/credentials/{type}
GET    /v1/admin/service-accounts/{subject}/credentials
GET    /v1/admin/service-accounts/{subject}/credentials/{type}
DELETE /v1/admin/service-accounts/{subject}/credentials/{type}
POST   /v1/admin/service-accounts/{subject}/credentials/{type}/revoke
POST   /v1/admin/service-accounts/{subject}/credentials/{type}/retry
POST   /v1/admin/service-accounts/{subject}/credentials/{type}/keys
POST   /v1/admin/service-accounts/{subject}/credentials/{type}/keys/{kid}/activate
```

- **The subject.** `{subject}` is the service account's canonical subject. The route forms the Lenny subject by prefixing `~svc~`, so a write on this route can name no person and no `agent` caller. A `{subject}` that already begins with `~` followed by `svc~` or `agt~` is refused with `422 VALIDATION_ERROR`, because it would form a doubled prefix.
- **Authorization.** `manage_user_credentials`, as for the person route. The route acts within the caller's own tenant, and a `platform-admin` holds no path into a customer's service account.
- **Type check.** `registeredBy` must include `service`, and `principalTypes` must include `service`. `self` is never valid for a service account's row: `POST /v1/credentials` keeps admitting only a caller whose `caller_type` is `human`.
- **No self-writes.** Every route above that changes a row (`PUT`, `DELETE`, `revoke`, `retry`, `keys`, and `activate`) refuses a caller whose Lenny subject equals the row's Lenny subject, with `403 USER_CREDENTIAL_SELF_WRITE`, whatever permission the caller holds. A provisioning backend that holds `manage_user_credentials`, and a service account that holds `tenant-admin`, therefore cannot write, rotate, retry, or remove their own rows, so a stolen bearer of either cannot plant a token for its own later sessions. The person route cannot name a service account (SA-6), and `self` is refused for service callers, so no route remains. The refusal emits `user_credential.write_refused` with the reason `self_write`.
- **Owner check.** The row's owner is the named service account. A caller holding the permission acts on rows of its own tenant.
- **Named service account.** The subject is accepted as given, as the person route accepts a user who has no row yet. These subjects are refused: one with an invalidation level under [TS-4](../0149-tenant-scoping-identity/change.md#user-lifecycle-ts-4), one with a completed erasure job, and one holding a security role under [SO-2](../0142-security-oversight/change.md#so-2-tenant-security-record-and-posture), on the reasoning of SP2.
- **The person route.** `PUT /v1/admin/users/{userId}/credentials/{type}` refuses a `{userId}` in the `~svc~` or `~agt~` namespace with `422 VALIDATION_ERROR`, and keeps refusing a subject that an add-on's service list names, such as a VCS pool's `serviceIdentities`, so a row meant for a service account is written under the service account route alone.
- **Own listing.** A service caller's `GET /v1/credentials` lists the metadata of its own rows, with `written_by`, as a person's lists hers.
- **Impersonation.** An impersonation bearer is refused on every route above, through `callercheck`, as on the person routes.
- **Audit.** `credential.registered`, `credential.rotated`, and the others of §4.9.2 carry `principalType` beside `type` and `written_by`. SA-2 and SA-4 name the events of key generation, key activation, verification, and `retry`.

**Everything else is reused** **[design]**:

- **One token per principal and type**, selected by the session's environment and then the no-environment row, as UC-3 states.
- **Rotation and revocation.** A write increments `generation`, the gateway pushes a binding change to the principal's sessions holding a binding of the type, and `netagent` flushes, under [UC-1, "Rotation and removal"](../0151-user-credential-delivery/change.md#rotation-and-removal). `DELETE` and `revoke` stop use.
- **Pods.** UC-3's refusal of pods that can serve someone else, with `422 USER_CREDENTIAL_UNSUPPORTED_POD`, its check points, the `retireOnRelease` mark, and the pod credential API's refusal of an unmarked release apply to a session of any principal type ([UC-3, "Limits under both deliveries"](../0151-user-credential-delivery/change.md#limits-under-both-deliveries)).
- **Impersonation.** A delegation tree in which any session holds a `userCredential` binding refuses an impersonation bearer's session input with `impersonation_user_credential`, whatever the principal type.
- **Delegation.** A child receives the binding only when its lease names it in `userCredentialBindings`, and it carries its root's principal.
- **Request rules, the delivery ceiling, the token trust record, leak scanning, and exposure records** apply unchanged.
- **Lifecycle.** `soft_disable`, `hard_disable`, and `full_revoke` under TS-4 apply to a service account's Lenny subject as to a person's, through `pkg/binding`'s invalidation level, and stop its releases. Erasure under §12.8 deletes its rows. Tenant deletion deletes every row.
- **Quarantine.** `quarantined` stops a release from each instance's own read.

**Sessions without a usable token.** A session whose principal has no usable row of the type, including an `agent` caller and a principal type the type does not list, gets UC-3's outcomes: with `required: true` the session is refused with `USER_CREDENTIAL_NOT_FOUND`, and with `required: false` it starts, with no directory under `delivery: file`, no credentials under `delivery: metadata`, and unauthenticated requests under `delivery: inject`. `details.reason` names the cause: `no_row`, `principal_type`, `trust_changed`, `provider_refused`, or `type_suspended`. An `oauth2Link` type answers a human with `403 LINKS_REQUIRED` as before, and a service account with `USER_CREDENTIAL_NOT_FOUND` and `principal_type`.

**VCS hosts.** The VCS layer's host policy refuses any `userCredential` type with `delivery: inject` on the API host of a VCS pool with `userAuthorization: required`, whatever its source, and refuses a binding with `delivery: file` on a covered host unless every VCS pool covering it sets `allowRuntimeHeldCredentials`. A service session on such a pool takes the App binding through `serviceIdentities`, as [`0152-user-git-identity`, "Service identities"](../0152-user-git-identity/change.md#service-identities) states, and a principal-owned credential does not change that.

**Audit.** `credential.mint` gains `principalType`, and `tokenSource` gains `serviceRegistered` for a service account's `registered` row, `clientCredentials`, and `issuerExchange`, beside UC-3's values. It also gains `sessionOrigin`, which reads `playground` for a session the playground created and is absent otherwise, so a release to a session minted from a pasted service bearer under the playground's `apiKey` mode is distinguishable from the job's own sessions. The record carries no principal identifier, as UC-3's carries no user identifier, and the join lives in rows that erasure deletes.

## SA-2 The `clientCredentials` source

The client credentials grant of RFC 6749 section 4.4 lets a confidential client obtain an access token with its own credentials. The request carries `grant_type=client_credentials` and an optional `scope`, the client must authenticate, and the response should include no refresh token **[documented]**.

**The type.**

```yaml
userCredentialTypes:
  - name: graph-reports
    source: clientCredentials
    principalTypes: [service]          # fixed for this source
    hosts: [graph.microsoft.com]
    delivery: inject                   # the delivery ceiling: inject | file (O2). Default inject.
    attach: "Authorization: Bearer {{secret}}"
    registeredBy: [service]
    clientCredentials:
      issuer: null                     # optional: discovery, and the assertion audience under `issuer`
      tokenEndpoint: https://login.microsoftonline.com/<entra-tenant-id>/oauth2/v2.0/token
      clientAuth: private_key_jwt      # client_secret_basic | client_secret_post | private_key_jwt
      assertionAudience: tokenEndpoint # issuer | tokenEndpoint. Default issuer.
      assertionAlg: PS256              # RS256 | PS256 | ES256. Default RS256.
      scope: "https://graph.microsoft.com/.default"
      audience: null                   # optional, for providers that read it
      resource: []                     # optional, RFC 8707
      assumedLifetimeSeconds: 300      # used when a response has no expires_in
```

| Field | Meaning |
| :-- | :-- |
| `issuer` | The authorization server's issuer identifier. When it is set, the write runs discovery under **Discovery** below. `assertionAudience: issuer` needs it. |
| `tokenEndpoint` | The HTTPS token endpoint. Set directly or resolved by discovery at write, and then fixed in the record. When `issuer` and `tokenEndpoint` are both set, discovery must return this endpoint. |
| `clientAuth` | How the client authenticates. A write naming a method the discovered metadata does not list is refused. |
| `assertionAudience` | Under `private_key_jwt`, the client assertion's `aud`: the issuer identifier, as RFC 10070 requires, or the token endpoint URL, which Microsoft Entra's page shows. |
| `assertionAlg` | The client assertion's signing algorithm. A generated key matches it. |
| `scope`, `audience`, `resource` | Request parameters. `scope` is RFC 6749's. `resource` is RFC 8707's, an absolute URI with no fragment, and may repeat **[documented]**. `audience` is not defined for this grant by RFC 6749, and some providers read it **[unverified]**, so the type offers it as a named field and sends it only when set. |
| `assumedLifetimeSeconds` | The lifetime the Token Service assumes when a response has no `expires_in`, which RFC 6749 section 5.1 makes recommended rather than required **[documented]**. Default 300, from 60 to 3,600. Posture reports a type whose provider omits it. |

Every field above, the hosts, `attach`, `principalTypes`, and the delivery ceiling are in the token trust record, with the route and CA of the token endpoint host and of each type host. Any change makes the type's stored rows unusable until a new write or a revert, so a configuration edit cannot send a stored client secret to a new endpoint **[design]**.

**The row.** A service account's row holds its client, written through the service account route:

```
PUT /v1/admin/service-accounts/report-bot/credentials/graph-reports
{"clientId": "...", "clientSecret": "...", "verify": true}
```

or, under `private_key_jwt`, `{"clientId": "...", "privateKey": "<PKCS #8 PEM>", "certificate": "<PEM, optional>"}`. The client secret and private key are write-only and stored envelope-encrypted under the tenant's Token-Service-only key with the record digest sealed, as UC-1 stores a `registered` value. The client identifier is stored in the same envelope. `maxBytes` bounds the row, 16 KiB by default.

**One key per row.** The Token Service records the SHA-256 fingerprint of each private key's public key, keyed per tenant, and refuses an uploaded or generated key whose fingerprint another row of the tenant holds with `422 VALIDATION_ERROR` and the reason `key_reused`. A key therefore signs client assertions for one row and one token endpoint.

**Generated keys.** `POST .../keys` with `{"clientId": "...", "alg": "PS256"}` makes the Token Service generate a key pair, RSA 3,072-bit for `RS256` and `PS256` or P-256 for `ES256`, keep the private key, and return the public key as a JWK, its `kid`, and a self-signed X.509 certificate valid for 365 days, which a provider that registers certificates, such as Entra, takes. The key stays pending until `POST .../keys/{kid}/activate`, which verifies it with one token request unless `verify: false` and then replaces the active key. One pending key exists at a time, and a pending key not activated within 7 days is discarded. The private key never leaves the Token Service. Posture reports each certificate's expiry, and an alert fires 30 days before it **[design]**. Whether Entra refuses an assertion signed with a key whose registered certificate has expired is **[unverified]**.

The audit events `user_credential.key_generated`, `user_credential.key_activated`, and `user_credential.key_discarded` name the tenant, principal type, principal, type, `kid`, the public key's fingerprint, and the writer, on the tenant's §11.7 chain **[design]**.

**Verification at write.** A write with `verify: true`, the default, makes one token request to the type's recorded token endpoint before it commits. A refusal answers `422 USER_CREDENTIAL_PROVIDER_REFUSED` with the provider's `error` code, and the row is not stored. A transient failure answers `503 USER_CREDENTIAL_PROVIDER_UNAVAILABLE`, and the row is not stored. `verify: false` stores without a request, for a backend that writes before the provider has propagated the client. Each verification emits `user_credential.verified` with its outcome and the provider's `error` code, and writes a `credential.mint` record as any provider call does.

**The request.** A `POST` of `application/x-www-form-urlencoded` to the token endpoint, with `grant_type=client_credentials`, `scope`, `audience`, and each `resource` when set, and the client authentication:

| `clientAuth` | What the request carries | Source |
| :-- | :-- | :-- |
| `client_secret_basic` | `Authorization: Basic` over the client identifier and secret, each form-urlencoded first, as RFC 6749 section 2.3.1 requires. An authorization server that issues passwords must accept this scheme. | **[documented]** |
| `client_secret_post` | `client_id` and `client_secret` in the body. RFC 6749 section 2.3.1 calls this NOT RECOMMENDED and limits it to clients that cannot use Basic, so the type offers it for providers that require it, and posture reports it. | **[documented]** |
| `private_key_jwt` | `client_assertion_type=urn:ietf:params:oauth:client-assertion-type:jwt-bearer` and `client_assertion`, a single JWT, under RFC 7523 section 2.2 and OpenID Connect Core section 9, with `client_id` in the body as Entra's request shows. | **[documented]** |

**The client assertion.** The header carries `alg` from `assertionAlg`, the key's `kid`, `x5t#S256` when the row holds a certificate, and `typ`. The claims are `iss` and `sub` equal to the client identifier, `aud`, `jti` from a random UUID, `iat` and `nbf` at signing, and `exp` 60 seconds later. Under `assertionAudience: issuer`, `aud` is the issuer identifier alone and `typ` is `client-authentication+jwt`, as RFC 10070 requires and recommends: it updates RFC 7523 so that a client authentication JWT names the authorization server's issuer identifier as its sole audience and never the token endpoint URL, after an audience injection attack disclosed in January 2025 **[documented]**. Under `assertionAudience: tokenEndpoint`, `aud` is the token endpoint URL the request goes to and `typ` is `JWT`, as OpenID Connect Core section 9 and Entra's certificate credentials page state, with `PS256` and `x5t#S256` on Entra **[documented]**. The default is `issuer`, and a provider that still checks the older audience takes `tokenEndpoint`, which posture reports.

**The audience names the server called.** Under either value, the assertion's audience and the request's destination name one server. Under `assertionAudience: tokenEndpoint` they are the same URL. Under `assertionAudience: issuer`, discovery is required, and the token endpoint is the one the issuer's own metadata names: a type that sets `issuer` alone takes `token_endpoint` from discovery, and a type that sets `issuer` and `tokenEndpoint` by hand is refused with `422 VALIDATION_ERROR` and the reason `endpoint_not_issuers` unless discovery returns that endpoint. A hand-set issuer can therefore never name an honest server while the request and its assertion go to another **[design]**.

**Discovery.** When the type names `issuer`, the write fetches the authorization server metadata of RFC 8414 at the issuer's well-known URI, which inserts `/.well-known/oauth-authorization-server` between the host and any path, and falls back to `<issuer>/.well-known/openid-configuration`. It refuses a document whose `issuer` differs from the issuer used, as RFC 8414 section 3.3 requires, and takes `token_endpoint`. It checks `clientAuth` against `token_endpoint_auth_methods_supported`, whose default is `client_secret_basic`, `assertionAlg` against `token_endpoint_auth_signing_alg_values_supported` under `private_key_jwt`, and `client_credentials` against `grant_types_supported` when present **[documented]**. Discovery runs only at a type write, through the mint path, and its result is fixed in the record. The Token Service never re-discovers at run time, because a discovery answer that changed would redirect a stored secret. A later write of the type re-runs discovery, and a different endpoint changes the digest.

**The response.** The Token Service requires a JSON body with `access_token` and a `token_type` of `Bearer`, compared case-insensitively, within the size bound. It reads `expires_in`, or `assumedLifetimeSeconds` when it is absent. It discards a `refresh_token` without storing it and counts it in a metric, because the grant needs none: Entra states that this flow never grants one **[documented]**. A `token_type` of `DPoP` or `N_A`, or a missing `access_token`, is a provider fault under SA-4.

**What the token carries.** A client credentials token acts with the application's own permissions, which at Microsoft Graph and similar providers usually reach every user and mailbox of the provider tenant. The documentation states this, and that the request rule presets shipped for user tokens were written for a person's token and may not narrow an application token in the same way.

**Rotation of the client.** A new write of the row, with a new secret or a new key, increments `generation`. The Token Service drops the row's cached access token and requests a new one at the next release, so a secret already removed at the provider fails at once. The gateway pushes a binding change to the service account's sessions, and `netagent` flushes, under UC-1. The provider's access tokens issued under the old secret stay valid there until they expire. The documented order for a tenant is: add the new secret or certificate at the provider, write it to Lenny with `verify: true`, then remove the old one at the provider.

**Hostile endpoints.** A token endpoint, and an issuer used for discovery, are tenant-registered input under C9. Each must be HTTPS and pass the egress design's check for a tenant-registered public service: resolved at registration, refused on the exclusion list, a registered `ipBlock`, or the cluster's addresses, and re-resolved periodically. A private endpoint is an outbound host of [`0148-tenant-outbound-connector`](../0148-tenant-outbound-connector/change.md), reached through its route. C8 holds, so a tenant cannot register a public provider's host behind its own network.

## SA-3 The `issuerExchange` source

A provider that trusts an external OIDC issuer accepts a JWT that issuer signed and returns its own access token. Under `issuerExchange` the Token Service signs an exchange JWT naming the session's service account under the tenant's exchange issuer, presents it at the provider, and releases the resulting access token on the pod credential API, so `netagent` injects it and no pod sees the JWT **[design]**.

**The type.**

```yaml
userCredentialTypes:
  - name: gcs-reports
    source: issuerExchange
    principalTypes: [service]          # fixed for this source (O3)
    hosts: [storage.googleapis.com]
    delivery: inject
    attach: "Authorization: Bearer {{secret}}"
    registeredBy: [service]
    issuerExchange:
      profile: gcpSts                  # entraFederated | gcpSts | awsWebIdentity | jwtBearer | tokenExchange
      assertionLifetimeSeconds: 300    # 60 to 600
      gcpSts:
        projectNumber: "123456789"
        poolId: lenny
        providerId: acme
        scope: [https://www.googleapis.com/auth/cloud-platform]
        impersonate: true              # call generateAccessToken for the row's service account
```

The author names a profile and the identifiers the profile needs. No type field holds an endpoint URL or a JWT audience for a vendor profile, and a generic profile takes the issuer alone. "Endpoints and audiences" below states what the Token Service builds from them.

**Approval (SAR-13).** An `issuerExchange` type takes effect only when its token trust record is on the tenant's `tokenTrust.allowedRecords`, whether or not the tenant security record holds that member for other types. Until then the type is suspended under SO-2's guardrail mechanism: the type write succeeds and returns the record's content and digest, and nothing under the type is written, verified, exchanged, or released. A row write on a suspended type is refused with `422 GUARDRAIL_VIOLATION` and `details.rule: tokenTrust.allowedRecords`, and a session naming a binding of it gets `USER_CREDENTIAL_NOT_FOUND` with `details.reason: type_suspended`. A `security-admin` approves the type by adding the record's digest and content to `tokenTrust.allowedRecords`, which is a weakening under SO-2: it needs a justification and takes effect after the tenant's `weakeningDelaySeconds`, during which another `security-admin` can cancel it. A tenant with no `security-admin` asks its operator, whose `platform-admin` writes the same entry with a justification and is mirrored to the tenant's chain under SO-4a. Any change to the type changes the digest, so a changed type is suspended again until its new record is approved. [`0142-security-oversight`, "User credentials and request rules"](../0142-security-oversight/change.md#user-credentials-and-request-rules) states the rule **[design]**.

**The row.** A service account receives an exchanged token only when a row names it, so an exchange is attempted for no service account the tenant did not enroll. The row holds the principal's provider parameters and no secret: `clientId` for `entraFederated`, `serviceAccount` for `gcpSts` with `impersonate: true`, `roleArn` for `awsWebIdentity`, and nothing for the others. It is written through the service account route, sealed as any row is, and verified at write with one exchange unless `verify: false`. Verification signs one exchange JWT and posts it to the endpoint the type's record holds, which is the profile's fixed endpoint or the bound endpoint below, and to no other URL. Rotation of a row means a new write, which re-verifies.

**The issuer (O1).** The exchange JWT is signed under the exchange issuer, an OIDC issuer per tenant beside the federation issuer and the identity issuer **[design]**:

- **URL.** `https://<lenny>/exchange/<tenant-id>`, keyed by the tenant identifier, whose reuse the §12.8 tenant tombstone prevents, as for the federation issuer.
- **Key set.** Each tenant has its own exchange key set in the Token Service's key store (`kms.go`), RSA 3,072-bit keys signing with RS256, separate from the federation key set and from the identity key set, and destroyed when the tenant is deleted. It signs exchange JWTs and nothing else. RS256 is the algorithm Entra's page states it tests for workload identity federation, and AWS and Google accept it **[documented]**.
- **Discovery and key set routes.** The Token Service serves, unauthenticated, `GET https://<lenny>/exchange/<tenant-id>/.well-known/openid-configuration` with `issuer`, `jwks_uri`, `response_types_supported: ["id_token"]`, `subject_types_supported: ["public"]`, `id_token_signing_alg_values_supported: ["RS256"]`, and `claims_supported` listing the claims below, which are the fields AWS's page lists for an IAM OIDC identity provider **[documented]**, and `GET https://<lenny>/exchange/<tenant-id>/jwks.json` with each key's `kid`, `kty`, `n`, `e`, `alg: RS256`, and `use: sig`. Both carry `Cache-Control: max-age=300`. A `kid` begins with the tenant identifier. The `/exchange/` path on the gateway ingress is reserved for the Token Service, as `/oidc/` and `/identity/` are. Providers fetch these documents, so the path must be publicly routable with a publicly trusted certificate, and a deployment without one cannot offer `issuerExchange`.
- **Registration.** A tenant registers the exchange issuer at each provider as a trust of its own: an IAM OIDC identity provider at AWS, a workload identity pool provider at Google, and a federated identity credential per service account at Entra. A registration of the federation issuer admits no exchange JWT, and the reverse holds, because each provider checks the issuer.
- **Rotation.** The Token Service rotates a tenant's exchange key every `tokenService.exchangeIssuer.keyRotationDays`, 30 by default. A new key is published in the key set at least `tokenService.exchangeIssuer.prePublishSeconds`, 86,400 by default, before the Token Service first signs with it, so a provider that cached the earlier key set has refreshed it by then. None of the pages read for Entra, Google, or AWS states how long the provider caches an issuer's key set **[documented]**, so the default is a margin of one day that "Validation owed" asks to measure. The previous key stays published for 600 seconds, the longest exchange JWT lifetime, plus the same period after its last use. An emergency rotation through the admin API drops the previous key at once and signs with the new key at once, and refusals within `prePublishSeconds` after it count as `unavailable` under SA-4 whatever their duration, so a provider's stale cache moves no row to `user_act_required`. The audit events `token_service.exchange_key_published`, `token_service.exchange_key_activated`, and `token_service.exchange_key_retired` name the tenant, the `kid`, and the reason.

Revision 1 recommended the federation issuer for exchange JWTs. The security review's finding 3 showed that AWS and Entra cannot condition a trust on a custom claim, that AWS uses one audience for both kinds of JWT, and that the egress group's address does not tell the pod's SDK from the Token Service, so a trust that does not pin an exact subject admitted an agent-readable federation JWT as a service account. The owner settled O1 as the separate exchange issuer that revision 1 listed as its alternative.

**The exchange JWT.**

```json
{ "alg": "RS256", "typ": "JWT", "kid": "acme-exchange-2026-11-a" }
{
  "iss": "https://lenny.acme.com/exchange/acme",
  "sub": "lenny:acme:service:report-bot",
  "aud": "https://iam.googleapis.com/projects/123456789/locations/global/workloadIdentityPools/lenny/providers/acme",
  "iat": 1794729600, "nbf": 1794729600, "exp": 1794729900,
  "jti": "1b6e0f3a-8d4c-4b2e-9a7f-5c3d2e1f0a9b",
  "tenant": "acme",
  "principal_type": "service",
  "credential_type": "gcs-reports"
}
```

| Claim | Value |
| :-- | :-- |
| `iss` | The tenant's exchange issuer. |
| `sub` | The principal subject, `lenny:<tenant>:service:<principal-id>`, where `<principal-id>` is the service account's canonical subject. The format is the identity assertion's (CR-2), so an operator can correlate the two, and the issuers keep them apart. |
| `aud` | The single audience "Endpoints and audiences" fixes or binds for the profile. No type field sets it. |
| `iat`, `nbf`, `exp` | Signing time, the same, and signing time plus `assertionLifetimeSeconds`, 300 by default, from 60 to 600. Google refuses an `exp` more than 24 hours after `iat`, and Entra's guidance for assertions is 5 to 10 minutes **[documented]**. |
| `jti` | A random UUID per exchange, recorded in `credential.mint`. |
| `tenant`, `principal_type`, `credential_type` | The tenant, `service`, and the type's name, which a provider can test in a condition, such as a Google attribute condition. |

Every claim comes from the session row the Token Service reads, and from the type. The JWT is minted per exchange and never cached, and it goes only to the endpoint the type's record holds.

**Endpoints and audiences.** Platform code builds each vendor profile's endpoint and audience from fixed parts and from identifiers whose patterns cannot name another host or path. A write that fails a pattern is refused with `422 VALIDATION_ERROR` naming the field **[design]**:

| Profile | Endpoint the Token Service posts to | `aud` of the exchange JWT | Author supplies | Source |
| :-- | :-- | :-- | :-- | :-- |
| `entraFederated` | `https://login.microsoftonline.com/<entraTenantId>/oauth2/v2.0/token` | `api://AzureADTokenExchange` | `entraTenantId`, a GUID | Entra's client credentials page states the endpoint, and its workload identity federation page names `api://AzureADTokenExchange` as the recommended audience and the one audience a user-assigned managed identity accepts **[documented]** |
| `gcpSts` | `https://sts.googleapis.com/v1/token`, and with `impersonate: true` `https://iamcredentials.googleapis.com/v1/projects/-/serviceAccounts/<row serviceAccount>:generateAccessToken` | `https://iam.googleapis.com/projects/<projectNumber>/locations/global/workloadIdentityPools/<poolId>/providers/<providerId>`, the provider's default audience, with the STS `audience` parameter `//iam.googleapis.com/projects/<projectNumber>/locations/global/workloadIdentityPools/<poolId>/providers/<providerId>` | `projectNumber` (digits), `poolId` and `providerId` (`^[a-z0-9-]{1,63}$`), and `scope` | Google's pages for other providers and for the STS `token` method **[documented]** |
| `awsWebIdentity` | `https://sts.<region>.amazonaws.com/` with the action `AssumeRoleWithWebIdentity` | `urn:lenny:exchange:aws`, a value Lenny chooses, which the tenant registers as the IAM OIDC identity provider's audience | `region`, matching `^[a-z]{2}(-[a-z]+)+-[0-9]+$` | AWS's page states that the audience is the client identifier the tenant registers, any value **[documented]**. The value is **[design]**. |
| `jwtBearer`, `tokenExchange` | The `token_endpoint` that RFC 8414 discovery of `issuer` returns at the type write, fixed in the record | `issuer`, the issuer identifier that discovery confirmed | `issuer`, an HTTPS URL | RFC 10070 lets a grant's audience be the issuer identifier, and RFC 8414 section 3.3 requires the metadata's `issuer` to equal it **[documented]** |

For `jwtBearer` and `tokenExchange` the binding is exact: the write fetches the metadata of `issuer` as SA-2's **Discovery** fetches it, refuses with `422 VALIDATION_ERROR` and the reason `issuer_mismatch` a document whose `issuer` is not equal character for character to the type's `issuer`, and records the document's `token_endpoint` as the one endpoint the type posts to. The exchange JWT's `aud` is that `issuer`. A type has no field for either value alone, so an audience naming one server with a request going to another cannot be written. These are refused with `422 VALIDATION_ERROR` and the reason `reserved_audience`: an `issuer` equal to a vendor audience above (`api://AzureADTokenExchange`, any value beginning `https://iam.googleapis.com/` or `//iam.googleapis.com/`, `sts.amazonaws.com`, or `urn:lenny:exchange:aws`); an `issuer` or `token_endpoint` whose host is `login.microsoftonline.com`, `sts.googleapis.com`, `iamcredentials.googleapis.com`, `iam.googleapis.com`, or a host ending in `.amazonaws.com`, which belong to the vendor profiles; and an `issuer` or `token_endpoint` on Lenny's own ingress host. An exchange JWT a hostile issuer receives therefore names that issuer as its audience, and a provider that checks its own audience refuses it.

**Subjects.** A principal identifier that holds a character a provider refuses in a subject, or a subject longer than the profile's limit, is refused at the row write with `422 VALIDATION_ERROR`. Google limits `google.subject` to 127 characters, Entra limits a federated credential's subject to 600, and AWS returns the subject in `SubjectFromWebIdentityToken`, which holds 6 to 255 characters **[documented]**. AWS logs the subject in CloudTrail and recommends against personal data in it **[documented]**. A service account's canonical subject is a client identifier, which is not personal data.

**Exchange profiles.**

| Profile | Request | Result | Source |
| :-- | :-- | :-- | :-- |
| `jwtBearer` | A form `POST` to the bound endpoint with `grant_type=urn:ietf:params:oauth:grant-type:jwt-bearer`, `assertion` holding the single exchange JWT, and an optional `scope`, under RFC 7523 section 2.1. No client authentication. | A bearer access token | **[documented]** |
| `tokenExchange` | A form `POST` to the bound endpoint with `grant_type=urn:ietf:params:oauth:grant-type:token-exchange`, `subject_token` holding the exchange JWT, `subject_token_type=urn:ietf:params:oauth:token-type:jwt`, and optional `resource`, `audience`, `scope`, and `requested_token_type`, under RFC 8693 section 2.1. No actor token. | The response's `access_token` with `issued_token_type` and a `token_type` of `Bearer`. A `token_type` of `N_A` is refused, because the token cannot be attached. | **[documented]** |
| `entraFederated` | The Entra client credentials request with `client_id` from the row, `scope` ending in `/.default`, `grant_type=client_credentials`, `client_assertion_type=urn:ietf:params:oauth:client-assertion-type:jwt-bearer`, and `client_assertion` holding the exchange JWT. The JWT's `iss`, `sub`, and `aud` must match a federated identity credential on the application or user-assigned managed identity. | A bearer access token with the application's permissions, which Entra issues without any user | **[documented]** |
| `gcpSts` | A JSON `POST` with `grantType` `urn:ietf:params:oauth:grant-type:token-exchange`, the `audience` above, `scope`, `requestedTokenType` `urn:ietf:params:oauth:token-type:access_token`, `subjectToken`, and `subjectTokenType` `urn:ietf:params:oauth:token-type:jwt`. With `impersonate: true`, a second `POST` to `generateAccessToken` with `scope` and `lifetime`, under the federated token, for APIs whose federation has limitations, as Google's page states. | A bearer access token. `generateAccessToken` returns `accessToken` and `expireTime`, one hour by default and at most one hour without an organization policy. | **[documented]** |
| `awsWebIdentity` (O6) | The STS `AssumeRoleWithWebIdentity` action with `RoleArn` from the row, `RoleSessionName`, `WebIdentityToken` holding the exchange JWT, `DurationSeconds=900`, and the type's `sessionPolicy` as `Policy` when set. The call needs no AWS credentials. `RoleSessionName` takes 2 to 64 characters of `[\w+=,.@-]`, so the Token Service writes `lenny-`, the first 40 characters of the principal identifier with any other character replaced by `-`, `-`, and the first 8 hexadecimal digits of the subject's SHA-256. | Temporary credentials (access key identifier, secret access key, and session token) that sign requests, which `inject` cannot attach, delivered under `metadata` (SA-5) | **[documented]** |

The profiles share one exchange step: sign the JWT, make the request through the mint path to the recorded endpoint, and read the result. The type's profile block, the endpoints built from it, and the audience are in the token trust record, so changing any of them makes the type's rows unusable until a new write or a revert, and suspends the type until its new record is approved.

**The relationship to `delivery: federation`.** Both rest on a provider trusting an issuer of the tenant, and they differ in the issuer, who holds the JWT, and whom it names:

| | `delivery: federation` | `source: issuerExchange` |
| :-- | :-- | :-- |
| Issuer and key set | The federation issuer, `https://<lenny>/oidc/<tenant-id>` | The exchange issuer, `https://<lenny>/exchange/<tenant-id>` |
| Who holds the JWT | The runtime, in `/run/netagent/credentials/<name>/token`, readable at the agent UID | The Token Service alone |
| Who exchanges it | The provider's SDK in the pod | The Token Service, through the mint path |
| Subject | `lenny:<tenant>:<binding>`, the binding as a workload | `lenny:<tenant>:service:<principal-id>`, the session's service account |
| Audience | The binding's `audience` | Fixed by the vendor profile, or bound to the generic profile's issuer |
| Lifetime | 15 minutes | `assertionLifetimeSeconds`, 300 seconds by default |
| What reaches the pod | The JWT and a provider configuration file | An access token under `inject` or `file`, or 15-minute AWS credentials under `metadata` |
| Gate | `agentReadableDelivery` | None under `inject`, `userCredentialDelivery` under `file`, and `userCredentialMetadataDelivery` under `metadata` |
| Approval | None beyond the gate | The tenant's `security-admin`, through `tokenTrust.allowedRecords` |
| Owner of the identity | The tenant | One service account |

The federation and exchange issuers share the design of the tenant tombstone, the per-tenant key store, the public reachability requirement, and the provider-side conditions on the egress group's address. They share no key, route, or provider registration. Federation's `pool` and `session` claims are absent from an exchange JWT, because one exchange serves every session of the service account while the cached token lives, and `credential.mint` records which sessions received it.

**Trust setup at the provider.** The documentation states these steps, each **[documented]** and none tested:

- **Microsoft Entra.** On the application registration or user-assigned managed identity that stands for the service account, add a federated identity credential with the issuer `https://<lenny>/exchange/<tenant-id>`, the principal subject, and the audience `api://AzureADTokenExchange`. Grant the application its permissions, and write the row with its client identifier. A federated credential matches issuer and subject exactly with no wildcards, an application holds at most 20, and a new credential can take minutes to propagate, during which an exchange fails with `AADSTS70021`. The token Entra returns carries the application's permissions, which usually reach the whole Entra tenant, and the documentation states this.
- **Google Cloud.** Create a workload identity pool and an OIDC provider with the issuer URI `https://<lenny>/exchange/<tenant-id>` and no custom allowed audiences, so the provider accepts its default audience, which is the audience Lenny sends. Map `google.subject=assertion.sub` and `attribute.principal_type=assertion.principal_type`, and optionally add an attribute condition such as `assertion.principal_type == 'service'`. Grant roles to `principal://iam.googleapis.com/projects/<number>/locations/global/workloadIdentityPools/<pool>/subject/<principal subject>`, or grant `roles/iam.workloadIdentityUser` on a service account and set `impersonate: true`. A grant to the whole pool through `principalSet` admits every service account of the tenant whose type names that pool, and the documentation advises a grant per subject. Google fetches the key set from the public discovery document, and an uploaded key set does not follow Lenny's rotation, so the documentation does not offer it.
- **AWS.** Create an IAM OIDC identity provider with the issuer URL `https://<lenny>/exchange/<tenant-id>` and the audience `urn:lenny:exchange:aws`, and a role whose trust policy allows `sts:AssumeRoleWithWebIdentity` with `StringEquals` conditions on the provider's `aud` key equal to `urn:lenny:exchange:aws` and on its `sub` key equal to the principal subject. A `StringLike` condition on `sub` admits every service account of the tenant, and the documentation states this. The role's maximum session duration is irrelevant, because Lenny requests 900 seconds. The key set may hold at most 100 RSA keys and 100 EC keys. The documentation recommends a permission policy condition on `aws:SourceIp` equal to the tenant's egress group address, which bounds where the credentials work.
- **Other authorization servers.** Register the exchange issuer and the subject as the server's documentation states, publish the server's RFC 8414 metadata, and choose `jwtBearer` or `tokenExchange`. The server must accept its own issuer identifier as the JWT's audience.

**Human callers (O3).** `issuerExchange` admits service accounts alone. A type naming `human` in `principalTypes` is refused, so a provider never receives an exchange JWT with a person's subject, and a tenant admin cannot attach a person to a provider identity through Lenny. A person reaches such a provider with `registered` or `oauth2Link`.

## SA-4 Provider calls, caching, and failure

**Route and bounds.** Every provider call, a token request, a discovery fetch, and an exchange, goes through `lenny-egress` on the Token Service's identity with the tenant asserted, whatever the host's route, as UC-2's link provider calls do. The decision service honours the assertion for the endpoints the tenant's types record, and it sees each call. Each call is bounded at 10 seconds and a 64 KiB response, follows no redirect, uses the CA from the host's endpoint or outbound host record, and names its expected route on the CONNECT, which the decision service refuses when the route it would choose differs, as UC-1 states for every call that carries a token or a secret. A Token Service connection is reused only within one tenant, route, and CA **[design]**.

**Cache.** The Token Service caches the access token per row: tenant, principal type, Lenny subject, `typeId`, environment, `generation`, and the record digest, sealed with that key as additional data under UC-1's rule, and never in the egress mint cache. Several bindings of one type share the principal's token, because every request parameter sits on the type and a binding cannot change one. `netagent` caches per session and binding as for every binding. The response on the pod credential API carries `cacheUntil` of the smaller of 300 seconds and the token's expiry less 60 seconds.

**Refresh.** A release that finds less than a third of the token's lifetime or less than 60 seconds left, whichever is longer, requests a new token first. One provider call runs at a time per row, under a row lease of 30 seconds taken by a conditional update, as UC-2's refresh lease is. Another replica waits up to 5 seconds by re-reading the row, then serves the cached token if it is still valid, and otherwise answers `unavailable`. An idle row makes no call. Neither grant issues a refresh token, so each refresh is a new grant. AWS credentials under `metadata` last 900 seconds, so their refresh runs at 300 seconds before expiry under this rule.

**Failure.** A provider answer maps to one of these outcomes **[design]**:

| Answer | Outcome |
| :-- | :-- |
| A bearer access token, or AWS credentials, within the bounds | Cached and released. A row in `user_act_required` that succeeds through `retry` returns to `active`. |
| `invalid_client` (RFC 6749 section 5.2, which may arrive with 401) under `clientCredentials` | `user_act_required` with the reason `client_rejected`. |
| `unauthorized_client`, `unsupported_grant_type`, `invalid_scope`, `invalid_request`, or `invalid_target` (RFC 8707) under `clientCredentials` | `user_act_required` with the reason `parameters_refused`, because the client or the type's parameters need a change. |
| Under `issuerExchange`: `invalid_grant`, `invalid_client`, `invalid_request`, `invalid_target`, Entra's `AADSTS70021`, or AWS's `InvalidIdentityToken`, `IDPRejectedClaim`, or `AccessDenied` | `unavailable` while consecutive refusals span less than `refusalGraceSeconds`, 600 by default, and for any span within `tokenService.exchangeIssuer.prePublishSeconds` after an emergency key rotation. After that, `user_act_required` with the reason `exchange_refused`. The grace covers Entra's propagation delay for a new federated credential **[documented]**, and AWS's `InvalidIdentityToken`, which the STS page describes as a token AWS could not validate and the IAM page also returns, with the message "Couldn't retrieve verification key from your identity provider", for a key set it cannot retrieve **[documented]**. |
| AWS's `ExpiredToken`, which the STS page describes as a web identity token that is expired or not valid **[documented]** | `unavailable`, retried with a newly signed JWT, with an alert, because the Token Service signs each JWT at the call and an expiry points at clock skew between Lenny and AWS. |
| 429, 5xx, `temporarily_unavailable`, `server_error`, AWS's `IDPCommunicationError`, a timeout, or a CONNECT refusal | `unavailable`, retried with backoff from 1 second doubling to 60 seconds, with no state change. A cached token is served until it expires. |
| A 200 with no `access_token`, a `token_type` other than `Bearer`, a body past the bound, or a malformed body | `unavailable`, with an alert, because the fault is the provider's or the type's and a retry rarely cures it. |

A row in `user_act_required` releases nothing, and the release is refused with the reason `user_act_required`, which flushes `netagent` through a binding change as a refused release check does. The row returns to `active` on a new write, on a revert of the record, or on `POST .../retry`, which makes one request to the recorded endpoint and clears the state on success. A tenant that fixed the provider side calls `retry`, because an `issuerExchange` row has no secret to rewrite. Each `retry` emits `user_credential.retried` with the caller, the row, and the outcome.

**Provider error text.** The provider's `error` code is stored and shown only when it matches `^[\x20-\x21\x23-\x5B\x5D-\x7E]{1,64}$`, the character set RFC 6749 section 5.2 allows, and is replaced by `invalid_error_code` otherwise. Its `error_description` is hostile input under C9. The Token Service keeps at most its first 256 characters, removes control characters, replaces the row's client identifier, any client secret, any client assertion, and any exchange JWT the request carried with `[redacted]`, and escapes every character outside printable ASCII as `\uXXXX`. Every renderer, including the admin notification and the posture read, treats the stored value as text and escapes it for its own format, such as HTML. The audit event and the notification carry only this stored form **[design]**.

**No link prompt.** UC-8 raises a prompt for a person's account link. A service account has no person to answer, and a row of these sources holds no link, so no prompt is raised. The tenant learns of a refusal from the audit event `user_credential.provider_refused` on its §11.7 chain, naming the tenant, principal type, principal, type, reason, the provider's `error` code, and the stored `error_description`; from a notification to the tenant's admins; and from the alert below. A session of the principal sees its requests go out without a token, and the host answers 401.

**Metrics and alerts.** `lenny_user_credential_provider_calls_total` by `source`, `profile`, and `outcome` (`ok`, `refused`, `unavailable`, `invalid_response`); `lenny_user_credential_provider_call_duration_seconds` by `source` and `profile`; `lenny_user_credential_rows_act_required` by `source`; and `lenny_user_credential_refresh_tokens_discarded_total`. The alert `UserCredentialProviderRefused` fires when a row enters `user_act_required`, `UserCredentialProviderUnavailable` when a row has answered `unavailable` for 5 minutes, and `UserCredentialExchangeTokenExpired` on any `ExpiredToken`.

**Stopping a credential.** UC-3's "Stopping a credential" applies, with these differences. `DELETE` and `revoke` on a row stop releases and flush `netagent` by binding change, and the access token already issued stays valid at the provider until it expires, because Lenny revokes no access token there. Deleting a client secret or certificate at the provider stops new tokens at the next refresh. Removing the trust at the provider stops new exchanges at the next refresh. AWS credentials served under `metadata` stay valid for the rest of their 900 seconds.

## SA-5 Delivery

**`inject`**, the default, attaches the access token through the type's `attach` template on the type's hosts, with `requestRules` available, as for any `userCredential` binding. The pod holds the placeholder alone.

**`file` (O2).** `delivery: file` is admitted for both new sources under the existing gate: the `userCredentialDelivery` grant, a delivery ceiling of `file` on the type, and the binding's opt-in, as UC-5 states. The file holds the access token alone, never the client secret, private key, or exchange JWT. Exposure records apply, and each row names the token's expiry, after which the exposure needs no action, because the token lapses by itself. `lockEgressToHosts`, `leakScan`, and `agentHeldCredential` apply.

**`metadata` (O6).** An `awsWebIdentity` type returns signing credentials, which `inject` cannot attach. A `userCredential` binding of such a type takes `delivery: metadata`, and `netagent`'s AWS plugin serves the credentials on loopback in the AWS container credentials format that the egress design's `metadata` delivery defines **[design]**:

- **Its own grant.** `metadata` on a `userCredential` binding needs `userCredentialMetadataDelivery`, a platform-admin grant per tenant in a risk class of its own, separate from `userCredentialDelivery`. A tenant that holds `userCredentialDelivery` for bearer files gains nothing under `metadata` until a platform admin grants this one. The type's delivery ceiling takes the value `metadata`, which only an `awsWebIdentity` type may hold, and the binding opts in with `delivery: metadata`. A grant or withdrawal is mirrored to the tenant's chain under SO-4a, and a `security-admin` can forbid the delivery through `credentialDelivery.forbid` with `metadata` or with `userCredential.metadata`.
- **Duration.** The Token Service requests `DurationSeconds=900`, the minimum AWS's page states for `AssumeRoleWithWebIdentity` **[documented]**, and no type field changes it. The role's maximum session duration, up to 12 hours **[documented]**, does not apply.
- **Session policy.** An optional type field, `awsWebIdentity.sessionPolicy`, holds an inline IAM policy of at most 2,048 characters that the Token Service sends as `Policy`, so the credentials hold the intersection of the role's policy and it **[documented]**. It is in the token trust record.
- **Exposure records.** Each set of credentials released to a session writes an exposure record naming the session, binding, type, the access key identifier, and the credentials' expiry, under UC-5's rules, and posture lists open records. After the expiry the exposure needs no action, because the credentials lapse. `user_credential.exposure_ended` reaches the tenant's backend at session end, as for `file`.
- **`lockEgressToHosts`.** The option applies as under `file`: it limits the session's effective allow set, and every descendant's, to the type's hosts and the base destinations, recorded on the session row as `egressLockHosts`, and it is refused on a pool with `egressProfile: internet`. The documentation recommends it for every `metadata` binding.
- **`leakScan`.** The option applies as under `file`, with the secret access key and the session token as the values the coordinating replica scans for, under UC-6's limits.
- **`agentHeldCredential`.** The session is marked, as under `file`.
- **Per-slot attribution.** On a pod with several slots, `netagent` serves a loopback fetch only to the slot that owns the connection, which it attributes by the socket's owner UID ([`0127-egress-and-git`, "Slot attribution and fetch grants"](../0127-egress-and-git/change.md#slot-attribution-and-fetch-grants)), and refuses an unattributed fetch. UC-3 already refuses a `userCredential` binding on a pool without per-slot attribution.

**Blast radius of `metadata`.** An agent can read the credentials from the loopback endpoint and use them from anywhere until they expire, at most 900 seconds after issue, unless the role's policy conditions on the egress group's address. Within that window they hold the role's permissions, narrowed by `sessionPolicy` when set, and they can call `sts:AssumeRole` into any role that trusts this role, so role chaining extends the reach to whatever those roles allow. Lenny cannot revoke them. The documentation states each of these, recommends the narrowest role, a session policy, an `aws:SourceIp` condition, and no outbound role trust, and lists the grant's risk class beside `agentReadableDelivery`.

**The gate classes.** `userCredentialDelivery` covers a principal's token under `file`, whatever the principal type. `userCredentialMetadataDelivery` covers a service account's AWS credentials under `metadata`. A service account's token is per principal and released under a `userCredential` binding, with exposure records and the per-principal checks, so it belongs with those classes. `agentReadableDelivery` stays the class of a tenant's credential.

## SA-6 Subject namespaces

A Lenny subject names one caller type. Before this design a person and a service account could hold the same subject string, as when `subject_claim` maps a value a person can choose or an identity source reissues an identifier, and session ownership (§10.2 rules 1, 2, and 5), derive (SD-P1), invalidation (TS-4), and erasure (§12.8) all key on the bare subject. The security review's finding 2 showed that a person could then send input to a service account's session that holds its credential. SA-6 closes this at the identity boundary, so every one of those controls stays keyed on the subject and needs no change **[design]**.

**The namespaces.** A caller's Lenny subject is its canonical subject under [TS-6](../0149-tenant-scoping-identity/change.md#several-idps-in-one-tenant-and-namespacing), with a prefix chosen by its `caller_type`:

| `caller_type` | Lenny subject | Example |
| :-- | :-- | :-- |
| `human` | The canonical subject | `~k3f9~a1b2c3` from a customer source, or `alice@acme.com` from a deployment source |
| `service` | `~svc~` followed by the canonical subject | `~svc~report-bot`, or `~svc~~k3f9~d4e5f6` from a customer source |
| `agent` | `~agt~` followed by the canonical subject | `~agt~prod-watchdog` |

A source identifier is never `svc` or `agt`, so a customer source's human subject, which begins with `~<source_id>~`, cannot begin with a reserved prefix. `userstore`'s limit of 256 printable characters applies to the result, and a longer result is refused at the boundary.

**Where it is enforced.**

1. **Identity mapping.** The verifier registry's one resolver, which returns a token's tenant and canonical subject under TS-6, and the deployment issuer's path both read `caller_type` from `service_claim` or `auth.serviceClaim` and then form the Lenny subject. A token that resolves to `caller_type: human` and whose canonical subject begins with `~svc~` or `~agt~` is refused with `401 SUBJECT_RESERVED`, whatever source issued it. The gateway, `lenny-ops`, and the Token Service share the resolver, so all three hold the rule.
2. **Service account creation.** Lenny keeps no service account object, and a service account comes into being through the writes that first name its subject. Each write that names a subject takes the Lenny subject and checks its prefix: role assignment through `POST` and `PUT /v1/admin/users` accepts any namespace, and records it; the TS-4 lifecycle upsert accepts any namespace; the SCIM server of TS-8 provisions persons and refuses a reserved prefix with `400`; a VCS pool's `serviceIdentities` entries and the tenant security record's `lifecycleSubjects` subject entries must begin with `~svc~` and are refused with `422 VALIDATION_ERROR` otherwise; the service account credential route of SA-1 forms the subject itself; and the person credential route refuses a reserved prefix. A write cannot therefore record a person's subject where a service account's belongs, or the reverse.
3. **Token issuance.** Every Lenny-issued token carries a `user_id` and a `caller_type` that agree: `/v1/oauth/token` copies both from the subject token and never changes either, the playground mint preserves both under its invariant 5, and an impersonation bearer takes the target's `caller_type` from the target subject's prefix. The minting component refuses to sign a token whose `caller_type` and subject prefix disagree, with `500 INTERNAL_ERROR` and an alert, because that is a code defect. Every verifier refuses such a token with `401 TOKEN_INVALID`.

**What follows.** Session ownership, derive, invalidation, erasure, role rows, credential rows, and `credential.mint`'s joins key on the Lenny subject, so a person never owns a service account's session and invalidating or erasing one never touches the other. An `agent` caller's subject is disjoint from both. Identity assertions (CR-2) and exchange JWTs name the canonical subject under a type segment, `user`, `service`, or `agent`, so the prefix never reaches a provider.

**Existing subjects.** No Lenny deployment runs outside development, so no stored subject is migrated. From the release that lands SA-6, a person whose identity source presents a canonical subject beginning with `~svc~` or `~agt~` is refused at authentication with `401 SUBJECT_RESERVED`, and the `auth_failure` event names the reason. On a customer source the canonical subject begins with the source identifier, so this arises only on a deployment source whose `sub` begins with `~`, which TS-6 already refuses once customer sources are enabled. The operator then has the deployment issuer emit another `sub` for that person.

## Configuration by scope

| Setting | Scope | Who sets it | Default |
| :-- | :-- | :-- | :-- |
| `principalTypes` | Type | `tenant-admin` | `[human]` on `registered`, `[human]` fixed on `oauth2Link`, `[service]` fixed on `clientCredentials`, and `[service]` fixed on `issuerExchange` |
| `clientCredentials.issuer`, `tokenEndpoint`, `clientAuth`, `assertionAudience`, `assertionAlg`, `scope`, `audience`, `resource` | Type | `tenant-admin`, in the token trust record | None, none, none, `issuer`, `RS256`, none, none, none. A write must set `tokenEndpoint` or `issuer`, and `clientAuth`. `assertionAudience: issuer` needs `issuer`. |
| `assumedLifetimeSeconds` | Type | `tenant-admin` | 300, from 60 to 3,600 |
| `issuerExchange.profile` and the profile block (`entraTenantId`; `projectNumber`, `poolId`, `providerId`, `scope`, and `impersonate`; `region` and `sessionPolicy`; or `issuer`) | Type | `tenant-admin`, in the token trust record, approved by `security-admin` through `tokenTrust.allowedRecords` | None. A write must set `profile` and its block. `gcpSts.impersonate` defaults to `false`. |
| `assertionLifetimeSeconds` | Type | `tenant-admin` | 300, from 60 to 600 |
| `awsWebIdentity` duration | Platform code | Fixed | 900 seconds |
| A row's `clientId`, `clientSecret`, `privateKey`, `certificate`, `serviceAccount`, or `roleArn` | Row | A caller holding `manage_user_credentials` under `registeredBy: [service]`, other than the service account itself | None |
| `verify` on a row write or key activation | Request | The writer | `true` |
| `userCredentialMetadataDelivery` | Tenant | Platform admin (admin API) | Not granted |
| Client assertion lifetime | Deployment | Fixed | 60 seconds |
| Generated key size, certificate validity, and pending key age | Deployment | Platform (Helm): `tokenService.clientKeys.rsaBits`, `certificateValidityDays`, and `pendingKeyMaxAgeDays` | 3,072, 365, and 7 |
| Exchange key rotation and pre-publication | Deployment | Platform (Helm): `tokenService.exchangeIssuer.keyRotationDays` and `prePublishSeconds` | 30 days and 86,400 seconds |
| `refusalGraceSeconds` | Deployment | Platform (Helm): `tokenService.providerCalls.refusalGraceSeconds` | 600 |
| Provider call bounds | Deployment | Platform (Helm): `tokenService.providerCalls.timeoutSeconds` and `maxResponseBytes` | 10 seconds and 64 KiB |
| Row lease and wait | Deployment | Platform (Helm) | 30 seconds and 5 seconds, as UC-2's refresh lease |
| `cacheUntil` ceiling | Deployment | Fixed, as UC-1 | 300 seconds |
| Certificate expiry alert | Deployment | Platform (Helm): `tokenService.clientKeys.expiryAlertDays` | 30 days |
| Subject prefixes | Platform code | Fixed | `~svc~` and `~agt~` |

Every non-spec default is operator-tunable and documented as such, except the values marked fixed.

### Refusals

- Bob's human session names a binding of `graph-reports`, a type with `principalTypes: [service]`: no token, and with `required: true`, `USER_CREDENTIAL_NOT_FOUND` with `details.reason: principal_type`.
- A session of `deploy-bot` names the binding: no row for `deploy-bot`, so `USER_CREDENTIAL_NOT_FOUND` with `no_row`. It never receives `report-bot`'s token.
- An `agent` caller names the binding: `USER_CREDENTIAL_NOT_FOUND` with `principal_type`.
- `report-bot`'s bearer calls `POST /v1/credentials` for its own row: refused, because `self` admits only a `human` caller.
- `acme`'s provisioning backend, whose Lenny subject is `~svc~provisioner`, calls `PUT /v1/admin/service-accounts/provisioner/credentials/graph-reports`: refused with `403 USER_CREDENTIAL_SELF_WRITE`.
- An operator calls `PUT /v1/admin/service-accounts/report-bot/credentials/graph-reports` under an impersonation bearer: refused.
- An operator sends input under an impersonation bearer to `report-bot`'s session, which holds the binding: refused with `impersonation_user_credential`.
- A session of `report-bot` on a pool with `recycle.enabled: true` names the binding: refused with `422 USER_CREDENTIAL_UNSUPPORTED_POD`.
- Carol edits `tokenEndpoint`: every row of the type is unusable, and no stored secret reaches either endpoint, until the backend writes again or Carol reverts.
- Carol writes an `issuerExchange` type with `profile: jwtBearer` and `issuer: https://iam.googleapis.com/projects/1/locations/global/workloadIdentityPools/p/providers/q`: refused with `reserved_audience`.
- Carol writes a `jwtBearer` type whose `issuer` is `https://as.acme.example` and whose metadata names `https://evil.example` as its issuer: refused with `issuer_mismatch`.
- Carol writes an `issuerExchange` type with `principalTypes: [service, human]`: refused with `422 VALIDATION_ERROR`.
- The backend writes a row on `gcs-reports` before Dave's approval takes effect: refused with `422 GUARDRAIL_VIOLATION`.
- A row write for a principal subject of 130 characters on a `gcpSts` type: refused, because Google limits the subject to 127.
- A person's token whose `sub` is `~svc~report-bot` reaches the deployment issuer's path: refused with `401 SUBJECT_RESERVED`.
- Carol writes a `metadata` binding on an `awsWebIdentity` type for a tenant without `userCredentialMetadataDelivery`: refused with the egress design's gate error.

## Threat model

The assets are each principal's long-lived material (a client secret, a private key, or a stored token), the access tokens and AWS credentials the provider issues, the exchange signing key, the provider-side trust that names a principal subject, and the separation of subjects between persons and service accounts.

### Security properties

- SAP-1. A session receives only the token of the principal on its session row, only when the row's `caller_type` is `human` or `service`, and only from a row whose sealed principal type equals that `caller_type`. The Token Service holds this from its own read through `pkg/binding` (UCP-1 as revised).
- SAP-2. A client secret and a private key never leave the Token Service. A client secret goes only to the token endpoint in the type's token trust record, under the route and CA recorded there, and a private key, which signs for one row alone, signs only client assertions whose `aud` names the server the request goes to.
- SAP-3. An exchange JWT never reaches a pod, a log, or a destination other than the endpoint the type's record holds: the vendor endpoint the profile fixes, or the token endpoint that the issuer named as the JWT's audience published. It lives at most `assertionLifetimeSeconds` and carries a unique `jti`. The residual is that a tenant admin can create a generic type naming an issuer she runs, which then receives JWTs whose audience is her own issuer, and only after a `security-admin` approves the type.
- SAP-4. Exchange JWTs and federation tokens are signed by different keys under different issuers, so a provider trust registered for one issuer refuses every token of the other, at every provider and whatever the trust's subject or audience condition.
- SAP-5. A tenant's exchange JWTs are signed with that tenant's exchange key under that tenant's issuer, so a provider trust that names one tenant's issuer refuses another tenant's JWTs (P4).
- SAP-6. A refresh token in a response of either grant is discarded and never stored.
- SAP-7. Every provider call goes through `lenny-egress` with the tenant asserted and its route named, so the decision service sees it and a route change stops it rather than redirecting it.
- SAP-8. UCP-2, UCP-3, UCP-4, UCP-6, UCP-7, UCP-8, UCP-9, and UCP-10 hold for a service account's token as for a person's.
- SAP-9. Every release and every provider call writes a `credential.mint` record with the principal type and the source, and no record carries a secret, a JWT, an access token, or unescaped provider text.
- SAP-10. No person and no service account share a Lenny subject, and no `agent` caller shares one with either, because the identity boundary, the subject-naming writes, and token issuance hold the namespaces of SA-6.
- SAP-11. No caller writes, rotates, retries, or removes a credential row whose Lenny subject is its own, on any route.
- SAP-12. An `issuerExchange` type signs nothing until a `security-admin`, or a platform admin with a justification mirrored to the tenant, has approved its current token trust record, and every change to the type needs a new approval.
- SAP-13. AWS credentials reach a pod only under `userCredentialMetadataDelivery`, last 900 seconds, and leave an exposure record.

### Threats

| Attacker | Can | Cannot | Why |
| :-- | :-- | :-- | :-- |
| A session of another service account, or a human session, in the tenant | Name the binding. | Receive `report-bot`'s token. | SAP-1: the row is keyed and sealed on its principal type and Lenny subject, and the session row decides. |
| A person whose identity source presents a subject equal to a service account's canonical subject | Sign in as that person. | Own, send input to, derive from, or attach to the service account's sessions, or be invalidated or erased with it. | SAP-10: the service account's Lenny subject carries `~svc~`, which no human token can carry. |
| Prompt-injected agent in `report-bot`'s session under `inject` | Use the access token through the type's hosts for what it permits, within `requestRules`. | Read the token, the client secret, the private key, or the exchange JWT. | UCP-6, SAP-2, and SAP-3. |
| Prompt-injected agent under `file` | Read the access token and use it anywhere until it expires, at most the provider's lifetime, one hour at Entra and Google by default. | Read the client secret, private key, or exchange JWT, or obtain a new token after the session. | The file holds the access token alone (SA-5). Exposure rows record it. |
| Prompt-injected agent under `metadata` | Read the AWS credentials and use them, from anywhere unless the role conditions on the egress address, for at most 900 seconds, including assuming roles that trust the role. | Obtain credentials after the session, or a longer duration. | SAP-13. The blast radius is stated under SA-5. |
| Tenant admin or backend holding `manage_user_credentials` | Write a row for any other service account of the tenant, including a client of an account the writer controls, as UC-1 accepts for persons (decision 4 of that design). Write `issuerExchange` provider parameters naming another role or application that trusts the same subject. | Read back a stored secret. Write across tenants. Write its own rows. Make a provider trust a subject the provider's own admin did not configure. | Writes are one-way and audited, SAP-11 refuses self-writes, and the provider-side trust names the subject outside Lenny. A stolen backend bearer still reaches every other service account's rows, which the residual risks state. |
| Tenant admin controlling configuration | Change a `clientCredentials` type's hosts or endpoints and then write rows again, so a new token reaches a host she added. Create an `issuerExchange` type naming a vendor profile with identifiers of her choosing, or a generic issuer she runs. | Send a token or secret stored before the edit to a new destination. Send an exchange JWT to any server other than a vendor's fixed endpoint or the token endpoint of the issuer named as its audience. Use any `issuerExchange` type before a `security-admin` approves it. | UCP-5, SAP-3, and SAP-12. For `clientCredentials`, the enforcing control for a tenant that separates roles stays `tokenTrust.allowedRecords` under SO-2. |
| An `agent` caller holding `tenant-admin` | Write service account rows, as a tenant admin can. | Hold a principal-owned token, or pass for a service account in an identity assertion. | O4, SAP-10, and CR-2's `principal_type: agent`. |
| Holder of `report-bot`'s Lenny bearer | Start sessions as `report-bot` and use its provider tokens through them. | Read its client secret, write its rows, or impersonate another principal. | The bearer is the service account's identity at Lenny, `self` is refused for a service account's row, and SAP-11 refuses the admin routes. |
| Compromised gateway replica | Forge a session row naming any principal and obtain that principal's tokens through a pod it drives, across tenants, as UC-1 states for persons. | Decrypt a row itself, or sign an exchange JWT. | The Token Service holds the keys. A replica's forgery reaches what the forged session would receive. |
| Compromised Token Service, or an operator holding its keys | Sign an exchange JWT for any service account of any tenant, and use any stored client secret. | Act at a provider beyond the trust that provider places in the tenant's exchange issuer and in each principal's client. | Inherent in trusting Lenny's issuer and storing secrets in Lenny. The documentation states it for each provider's trust setup. |
| Hostile token endpoint or issuer registered by a tenant | Answer token requests as it likes for its own tenant's rows, and receive exchange JWTs whose audience is its own issuer. | Reach a private address, shadow a reserved host, receive another tenant's secret, receive a JWT with a vendor audience, or redirect a call. | The registration check, C8, C9, the reserved audiences of SA-3, the mint path's bounds with no redirect, and UCP-7. |
| A party that captures a client assertion | Replay it at the same authorization server until its `exp`, 60 seconds, unless the server refuses a reused `jti`. | Use it at another server. | The audience names the server called, as RFC 10070 requires under `assertionAudience: issuer`, which also closes the audience injection attack it describes. |
| An agent holding a federation token for its binding | Exchange it where the tenant's trust names the federation issuer and the binding's subject. | Pass any trust registered for the exchange issuer. | SAP-4. |
| An operator impersonating `report-bot` | Read what impersonation allows. | Give the session input or write its rows. | UCP-3 and the route refusals. |

### Residual risks

- A backend or tenant admin holding `manage_user_credentials` can write rows for every service account of the tenant other than its own, as decision 4 of the user credential design accepts for persons. A stolen bearer of the provisioning backend therefore reaches every other service account's rows.
- A `clientCredentials` token usually carries application permissions that reach the whole provider tenant, and request rule presets were written for user tokens.
- Under `metadata`, AWS credentials work from anywhere for 900 seconds unless the role conditions on the source address, and they can chain into other roles.
- None of the providers states its key set cache period, so the one-day pre-publication is a margin rather than a documented bound.
- Approval of an `issuerExchange` type protects a tenant only while its `security-admin` and `tenant-admin` are different people, which posture reports.

## Current state

| Area | What exists before the revisions this design applies |
| :-- | :-- |
| Session row | `caller_type` on the session row, set from the principal at create and copied to a child, under UC-3 **[source]**. In the tree, `Principal` carries `CallerType` and the session store does not **[source]**. |
| Subjects | A customer source's canonical subject is `~<source_id>~<value>`, and a deployment source's `sub` keeps its form. Nothing keeps a service account's subject apart from a person's **[source]**. |
| Release | UCP-1 releases a `userCredential` token only to a `human` session **[source]**. |
| Writes | The admin write route refuses a service subject, and `POST /v1/credentials` admits only a `human` caller **[source]**. |
| Sessions without a user | UC-3 refuses or starts them without a token according to `required`, and decision 14 sends them to tenant stored-secret bindings **[source]**. |
| Tenant bindings | A stored secret binding with `scope: always` attaches to every session of the tenant, and one with `scope: onRequest` to any session naming it **[source]**. |
| Federation | The Token Service is an OIDC issuer per tenant under `/oidc/`, with a subject per binding and the JWT in the pod, behind `agentReadableDelivery` **[source]**. |
| Provider calls | The Token Service calls link provider endpoints and VCS mint endpoints through the mint path **[source]**. It runs no client credentials grant and no exchange of its own JWT. |
| Identity assertions | `sub` is `lenny:<tenant>:user:<user-id>` or `lenny:<tenant>:service:<principal-id>` under the identity issuer **[source]**. |

## Owner decisions

| ID | Decision |
| :-- | :-- |
| D1 | Service accounts own per-principal credentials under the `userCredential` machinery. The owner asked on 2026-11-15, "what about service accounts? A user may be a legitimate service account but have no human behind it", and adopted option (b): a tenant admin or backend (`registeredBy: [service]`) writes a token for a service account under a `source: registered` type, the Token Service releases it only to sessions whose row names that service account, and UCP-1 becomes the token of the principal on the session row for `human` and `service` callers. One token per principal, the rotation, revocation, and request rules, UC-3's refusals on reused pods and on pods without slot attribution, and the impersonation refusal are reused. `oauth2Link` stays human-only, `agent` callers stay refused (O4), and long-lived bearer `registered` tokens stay supported, as the owner confirmed (SA-1). |
| D2 | `source: clientCredentials`. The owner answered on 2026-11-15: "b + client credentials ... go ahead". The Token Service runs the client credentials grant of RFC 6749 section 4.4 with a service account's client identifier and client secret or private key, which no pod reads, caches the access token, requests a new one before expiry, and `netagent` injects it (SA-2, SA-4). |
| D3 | Provider trust of Lenny's issuer. The owner answered on 2026-11-15: "I think we should also support 'A provider can be set up to trust Lenny's own issuer directly'". The Token Service signs a JWT for the session's principal and exchanges it at the provider through the JWT bearer grant of RFC 7523 or the token exchange of RFC 8693, with cloud workload identity federation as the common case, and `netagent` injects the result, so Lenny stores no secret (SA-3). |
| Review | The owner answered the summary of the security review of revision 1 and its updated recommendations on 2026-11-15: "O1-O7: go with your recommendations". Revision 2 applies every finding of [`evidence/service-account-credentials-design/security-review.md`](evidence/security-review.md) and settles O1 to O7 below as recommended. |
| O1 | Exchange JWTs are signed under a separate exchange issuer, `https://<lenny>/exchange/<tenant-id>`, with its own RS256 key set, separate from the federation issuer and from the identity issuer of CR-2 (SA-3). This reverses revision 1's recommendation of the federation issuer, because review finding 3 showed that AWS and Entra cannot condition on a custom claim and AWS uses one audience for both kinds of JWT, so a trust that does not pin an exact subject admitted an agent-readable federation token as a service account. The cost is another public route, another key set to rotate, and another registration at each provider. |
| O2 | `delivery: file` of an access token from a `clientCredentials` or `issuerExchange` row is admitted under the `userCredentialDelivery` grant, a type ceiling of `file`, and the binding's opt-in, with the file holding the access token alone (SA-5). The agent can read an access token that lives up to the provider's lifetime, often one hour. |
| O3 | `issuerExchange` serves service accounts alone. A type naming `human` in `principalTypes` is refused (SA-3). Review finding 4 showed that `entraFederated` returns an application token with tenant-wide permissions rather than acting as the person, and that a tenant admin writing a person's row would attach that person to any provider identity. A person reaches such a provider with `registered` or `oauth2Link`. |
| O4 | `agent` callers stay refused for every source. §25.1's `agent` callers are operability service accounts holding `platform-admin` or `tenant-admin`, the roles that write credentials, so a credential owned by one would be written and used by the same automation. A deployer whose agent needs a provider credential runs it under a `service` principal. An `agent` caller's identity assertion carries `principal_type: agent` and the subject `lenny:<tenant>:agent:<id>` ([`0124-connector-removal`, "CR-2 Identity assertion"](../0124-connector-removal/change.md#cr-2-identity-assertion)), and its Lenny subject carries `~agt~` (SA-6). |
| O5 | Mutual TLS client authentication of RFC 8705 is left out of the first version. `private_key_jwt` gives the same property, no shared secret, through the mint path as it stands. A provider that offers only `tls_client_auth` or `self_signed_tls_client_auth` cannot be used. |
| O6 | The `awsWebIdentity` profile is admitted, and its credentials reach a pod under `delivery: metadata`, behind the new grant `userCredentialMetadataDelivery`, with `DurationSeconds` fixed at 900, exposure records, `lockEgressToHosts`, `leakScan`, and per-slot attribution (SA-5). The cost is AWS signing credentials within the agent's reach for 900 seconds, usable from anywhere unless the role conditions on the source address, with role chaining possible. |
| O7 | The default of `principalTypes` on a `registered` type is `[human]`. A tenant opts a type into service accounts by writing `[service]` or `[human, service]`, so every existing type is unchanged. This reverses revision 1's default of `[human, service]`. |
| SD1 | The source name `issuerExchange`, chosen by this design under the owner's instruction to choose a clear name. |
| SD2 | A dedicated admin route for a service account's rows, beside the person route, so a write states which principal type it means. |
| SD3 | The identifiers `userCredential`, `user_credential`, `USER_CREDENTIAL_*`, and `manage_user_credentials` keep their names for both principal types, because renaming them touches every design, schema, and error code for no change in behaviour. The documentation states that "user" in these names covers a service account. The grant `userCredentialMetadataDelivery` follows the same stem. |

## Open decisions

None.

## Documentation changes

Each page below states every setting, default, limit, failure mode, and contract this design adds. The changelog names every new field, route, error code, reason, event, metric, and alert.

- **The user credential types page** of the tenant admin guide states `principalTypes` with its allowed values and default per source, including the `[human]` default on `registered` and how to opt a type into service accounts; that "user" in the identifiers covers a service account (SD3); the `clientCredentials` and `issuerExchange` sources with every field of "Configuration by scope", its default and range; that each field is in the token trust record and that any change makes stored rows unusable until a new write or a revert; that an `issuerExchange` type needs a `security-admin`'s approval and waits out the weakening delay; and the delivery ceilings of the new sources, with `file` under O2 and `metadata` under O6.
- **A service account credentials page** for backend integrators states the Lenny subject of a service account and its `~svc~` prefix, the routes of SA-1 with their permission, `registeredBy: [service]`, the refused subjects, the refusal of self-writes with `USER_CREDENTIAL_SELF_WRITE`, the refusal of `self` for a service account, `verify` and its answers, the generated key flow with its key sizes, certificate validity, pending key age, activation, and the one-key-per-row rule, the rotation order for a client secret or certificate, `retry`, and `GET /v1/credentials` for a service caller.
- **A provider setup page** states the trust setup of SA-3 for Entra, Google Cloud, AWS, and a generic authorization server, each step, the exchange issuer and its discovery and key set URLs, that the federation issuer's registration does not serve exchange JWTs, the fixed endpoint and audience of each vendor profile, the audience binding of the generic profiles and the reserved audiences, the subject format and its length limits per provider, the claims and how to condition on `principal_type`, the RS256 algorithm and the key rotation with its pre-publication, the advice to grant per subject and to use `StringEquals` at AWS, the Entra propagation delay and its 20-credential limit, that Entra and other client credentials tokens carry application permissions, Google's 24-hour and 127-character limits and the `impersonate` option, AWS's key and audience limits and the personal-data advice for subjects, and that every vendor statement is from the vendor's page.
- **The AWS page** states `delivery: metadata` for an `awsWebIdentity` type, the `userCredentialMetadataDelivery` grant, the fixed 900-second duration, `sessionPolicy`, the exposure records, `lockEgressToHosts` and `leakScan`, and the blast radius under SA-5.
- **The client credentials reference** states the request for each `clientAuth`, the client assertion's header and claims under each `assertionAudience`, RFC 10070's audience rule and when to choose `tokenEndpoint`, the rule that a hand-set token endpoint must match the issuer's metadata, discovery with the metadata fields it reads and the issuer check, the response checks, `assumedLifetimeSeconds`, and that a refresh token is discarded.
- **The failure modes page** states each row of SA-4's failure table with its outcome, `refusalGraceSeconds`, the reasons `client_rejected`, `parameters_refused`, and `exchange_refused`; `USER_CREDENTIAL_NOT_FOUND` with `details.reason` values `no_row`, `principal_type`, `trust_changed`, `provider_refused`, and `type_suspended`, `422 USER_CREDENTIAL_PROVIDER_REFUSED` and `503 USER_CREDENTIAL_PROVIDER_UNAVAILABLE` at write, `403 USER_CREDENTIAL_SELF_WRITE`, `401 SUBJECT_RESERVED`, the `VALIDATION_ERROR` reasons `key_reused`, `endpoint_not_issuers`, `issuer_mismatch`, and `reserved_audience`, how provider error text is stored and escaped, that no link prompt is raised, and how the tenant learns of a refusal.
- **The identity page** of the tenant admin guide states the subject namespaces of SA-6, where each prefix is enforced, which writes take a namespaced subject, and what happens to a person whose subject begins with a reserved prefix.
- **The operator guide** states the Helm values of "Configuration by scope", the provider call bounds and route, the row lease, the metrics and alerts of SA-4, that the exchange issuer's discovery must be public for these profiles, the exchange key rotation and emergency rotation, the `userCredentialMetadataDelivery` grant and its risk class, and the trust a provider places in Lenny's operator and Token Service.
- **The security model page** states the threat model above, including what a compromised Token Service or replica reaches, the residual of a provider trust granted per pool, and the residual risks.
- **`docs/reference/error-catalog.md`** adds `USER_CREDENTIAL_PROVIDER_REFUSED`, `USER_CREDENTIAL_PROVIDER_UNAVAILABLE`, `USER_CREDENTIAL_SELF_WRITE`, and `SUBJECT_RESERVED`, with the new `details.reason` values of `USER_CREDENTIAL_NOT_FOUND` and `VALIDATION_ERROR`.
- **`docs/reference/metrics.md`** adds the metrics and alerts of SA-4, and the audit event catalogue adds `user_credential.provider_refused`, `user_credential.key_generated`, `user_credential.key_activated`, `user_credential.key_discarded`, `user_credential.verified`, `user_credential.retried`, `user_credential.write_refused`, `token_service.exchange_key_published`, `token_service.exchange_key_activated`, `token_service.exchange_key_retired`, the `principalType` field, and `credential.mint`'s `sessionOrigin`.

## Specification changes

Each lands through `change-proposal`. The edits locate their targets by heading.

| Section | Change |
| :-- | :-- |
| [§4.2 Session Manager](../../spec/04_system-components.md#42-session-manager) | `caller_type` on the session row, as UC-3 stages, read as the principal type for a principal-owned credential. |
| [§4.3 Token Service](../../spec/04_system-components.md#43-token-service) | The user credential category keyed on principal type and Lenny subject; the client credentials grant with its client authentication methods, the audience binding, discovery, and response checks; the exchange issuer per tenant with its RS256 key set, discovery and key set routes, rotation with pre-publication, and emergency rotation; the exchange JWT, its claims, and its profiles with their fixed endpoints and audiences and the generic profiles' binding; the cache per row, the refresh rule, the row lease, the failure states, and the storage of provider error text; and the statement that a client secret, private key, and exchange JWT never leave the Token Service. |
| [§4.9 Pre-Authorized Credential Flow](../../spec/04_system-components.md#pre-authorized-credential-flow) | The service account routes, `registeredBy: [service]` for them, the self-write refusal, the refusal of `self` for a service caller, `verify`, key generation and activation, the one-key-per-row rule, `retry`, `principalTypes` with its defaults, and the approval of an `issuerExchange` type. |
| [§10.2 Authentication](../../spec/10_gateway-internals.md#102-authentication) | The subject namespaces of SA-6 at the identity boundary and on every Lenny-issued token, `SUBJECT_RESERVED`, the agreement of `user_id` and `caller_type`, `manage_user_credentials` covering the service account routes, and the permission matrix rows for those routes. |
| [§11.4 User Invalidation](../../spec/11_policy-and-controls.md#114-user-invalidation) | Invalidation of a service account's Lenny subject stops its principal-owned releases. |
| [§12.8 Compliance Interfaces](../../spec/12_storage-architecture.md#128-compliance-interfaces) and [§12.9 Data Classification](../../spec/12_storage-architecture.md#129-data-classification) | The principal type column, the client rows, pending keys, and exchange keys in the Secret class, and their deletion with the subject and with the tenant. |
| [§13.3 Credential Flow](../../spec/13_security-model.md#133-credential-flow) | The release rule of SAP-1, the exchange issuer that signs exchange JWTs that no pod receives, the impersonation bearer's `caller_type` taken from the target's namespace, and the `metadata` delivery of AWS credentials under its own grant. |
| [§15.1 REST API](../../spec/15_external-api-surface.md#151-rest-api) | The routes of SA-1, the `/exchange/` discovery and key set routes, `USER_CREDENTIAL_PROVIDER_REFUSED`, `USER_CREDENTIAL_PROVIDER_UNAVAILABLE`, `USER_CREDENTIAL_SELF_WRITE`, `SUBJECT_RESERVED`, and the `details.reason` values of `USER_CREDENTIAL_NOT_FOUND` and `VALIDATION_ERROR`. |
| [§16.1 Metrics](../../spec/16_observability.md#161-metrics), [§16.5 Alerting Rules and SLOs](../../spec/16_observability.md#165-alerting-rules-and-slos), and [§16.7 Section 25 Audit Events](../../spec/16_observability.md#167-section-25-audit-events) | The metrics, alerts, and audit events of SA-2, SA-3, and SA-4, `principalType` on the credential events, and `sessionOrigin` on `credential.mint`. |

## Non-specification changes

| Area | Change |
| :-- | :-- |
| Token Service | The principal-keyed type rows, the self-write refusal, the client credentials grant, client assertions with the audience binding, key generation, pending keys, and the key fingerprint index, discovery, the exchange issuer and its key set, routes, and rotation, the exchange profiles with fixed endpoints and audiences, the generic profiles' binding and reserved audiences, the cache per row with its lease, the failure states, the stored error text, and the metrics. |
| `pkg/binding` | Return the principal type with the principal, as it returns `caller_type` today under UC-3. |
| `pkg/auth/jwt` and the verifier registry | The subject namespaces at the identity boundary, `SUBJECT_RESERVED`, and the refusal of a token whose `caller_type` and subject prefix disagree. |
| The gateway's credential and admin routes | The service account routes and their checks, the `details.reason` values at create, and the prefix checks on `serviceIdentities`, `lifecycleSubjects`, SCIM, and the person route. |
| `schemas/lenny-tokenservice.proto` | The row write with principal type, key generation, activation, and retry calls, the provider-refused and type-suspended states on the release response, and the AWS credentials on the release response under `metadata`. |
| The decision service | Honour the tenant assertion for the token, discovery, and exchange endpoints the tenant's types record. |
| `netagent` | None under `inject` and `file`. Under `metadata`, the AWS plugin serves a `userCredential` binding's credentials to the owning slot. |
| `migrations` | The principal type column, the client row fields, the pending key table, the key fingerprint index, the exchange key table, and the per-row token cache with its lease. |
| `charts/lenny` | The Helm values of "Configuration by scope", the `/exchange/` ingress path, and the alerts. |
| `BUILD-GAPS.md` | No finding is filed by this design. |

## Changes to other designs

Revision 1 applied its changes in the same pass to [`0151-user-credential-delivery`](../0151-user-credential-delivery/change.md) revision 39, [`0127-egress-and-git`](../0127-egress-and-git/change.md) revision 91, [`0124-connector-removal`](../0124-connector-removal/change.md) revision 6, [`0152-user-git-identity`](../0152-user-git-identity/change.md) revision 44, and [`0149-tenant-scoping-identity`](../0149-tenant-scoping-identity/change.md) revision 41. Revision 2 applies these in the same pass:

- [`0151-user-credential-delivery`](../0151-user-credential-delivery/change.md) revision 40: `principalTypes` defaults to `[human]` on `registered` and is fixed at `[service]` on `issuerExchange`; the service account route forms the Lenny subject and refuses self-writes; the person route refuses a reserved prefix; `type_suspended` joins the `details.reason` values; and the label row, configuration, refusals, decision 43, and revision notes follow.
- [`0127-egress-and-git`](../0127-egress-and-git/change.md) revision 92: the federation issuer signs federation tokens alone, keeps RS256 because Entra tests only RS256 issuers and federation serves the Azure SDKs, and publishes a new key before signing with it; **Exchange by the Token Service.** names the exchange issuer; the **Gates** paragraph adds `userCredentialMetadataDelivery`; and "Reserved for other designs" carries the `/exchange/` path and the grant.
- [`0124-connector-removal`](../0124-connector-removal/change.md) revision 7: the federation token term returns to a key set that signs nothing else; CR-2's `sub` and `principal_type` give an `agent` caller `lenny:<tenant>:agent:<id>` and `principal_type: agent`; and the subject names the canonical subject without its namespace prefix.
- [`0152-user-git-identity`](../0152-user-git-identity/change.md) revision 45: a `serviceIdentities` entry names a Lenny subject in the `~svc~` namespace, and a pool write naming any other subject is refused.
- [`0149-tenant-scoping-identity`](../0149-tenant-scoping-identity/change.md) revision 42: the subject namespaces of SA-6 under "Several IdPs in one tenant, and namespacing", the reserved source identifiers, the SCIM refusal, `SUBJECT_RESERVED`, and the tests.
- [`0142-security-oversight`](../0142-security-oversight/change.md) revision 58: an `issuerExchange` type needs its record on `tokenTrust.allowedRecords` whatever the member's state, `credentialDelivery.forbid` gains `userCredential.metadata`, `lifecycleSubjects` and `userAuthorization.serviceIdentities` hold subjects in the `~svc~` namespace, and posture reports `issuerExchange` types with their approval state and the `userCredentialMetadataDelivery` grant.

[`0129-github-credential-addon`](../0129-github-credential-addon/change.md) describes `caller_type` as recording whether the caller is `human`, which stays true.

## Tests

Every test carries a `// spec:` annotation once the specification sections land, and tests at tier 2 and above carry `// diagnosis:`.

| Tier | Coverage |
| :-- | :-- |
| 0 | A lint refuses a client secret, private key, or exchange JWT in any log, event, or `credential.mint` field, refuses any read of the client row, pending key, or exchange key tables outside the Token Service, and refuses an exchange profile whose endpoint or audience is read from a type field. |
| 1 | The release rule: a `human` session against a `service` row reads nothing, and the reverse, and an `agent` and an empty `caller_type` read nothing. `principalTypes` defaults and allowed values per source, `human` refused on `issuerExchange`, and its change moving the digest. A row moved between principal types fails to decrypt. The subject namespaces: a human token with each reserved prefix refused, a service and an `agent` token prefixed, a source identifier of `svc` or `agt` never generated, the 256-character limit, and a token whose `caller_type` and prefix disagree refused at mint and at verify. The self-write refusal on every changing route. The client assertion's claims and `typ` under each `assertionAudience`, with a 60-second `exp`, and a hand-set endpoint that discovery does not return refused. A reused key fingerprint refused. Client authentication encoding for `client_secret_basic`, including a secret holding `:` and `%`. Discovery's well-known URI with and without an issuer path, and refusal of a mismatched `issuer`. Each vendor profile's endpoint and audience built from its identifiers, and each pattern refusing a value that would name another host or path. The generic profiles' binding, `issuer_mismatch`, and each reserved audience and host. Each row of SA-4's failure table, including the grace window, `ExpiredToken`, and the window after an emergency rotation. The stored error text: truncation, control characters, redaction, and escaping. The exchange JWT's claims, the subject length limits per profile, and `RoleSessionName` derivation within 64 characters and its pattern. |
| 2 | The Token Service against a KMS-backed key store: client key generation, a pending key discarded after its age, activation, and the exchange key signing with RS256 that verifies against the published key set across a scheduled rotation with pre-publication and across an emergency rotation, with the federation key set never holding an exchange key. |
| 3 | The service account routes' request and response schemas; the `/exchange/` discovery document and key set against AWS's listed fields; the release response's provider-refused and type-suspended states; the AWS credentials on the release response; and the Token Service proto calls. |
| 4 | Against an OAuth stand-in: each `clientAuth`, `scope`, `audience`, and repeated `resource`; discovery; a response with a refresh token discarded; a response without `expires_in`; `invalid_client` moving the row to `user_act_required` and `retry` restoring it; a 503 leaving the row `active` and serving the cached token; a concurrent refresh across replicas producing one call; and a client secret rotated with `verify: true`. Against stand-ins for every profile: each request body, the impersonation step, and a refusal within and past `refusalGraceSeconds`, with every request reaching only the recorded endpoint, including under `verify`. An `issuerExchange` type suspended until its record is on `tokenTrust.allowedRecords`, a row write refused meanwhile, and a changed type suspended again. A human session, a session of another service account, and an `agent` session against a service account's binding under each value of `required`. A person and a service account whose canonical subjects are equal: the person cannot send input to, derive from, or invalidate the service account's session. Impersonated input to a service account's session refused. A record change: no stored secret reaches the old or the new endpoint. The provisioning backend's self-write refused. |
| 5 | On Kind, a session of a service account receives an injected token through `netagent`, its pod retires at release, and a recycling pool refuses it. Under O2, a `file` delivery of an access token with its exposure row. Under O6, `metadata` serving stand-in AWS credentials to the owning slot alone, with an exposure row, `lockEgressToHosts`, and a tenant without the grant refused. |
| 6 | On cloud, a service account reaches Microsoft Graph through `clientCredentials` with a generated key and through `entraFederated`, Google Cloud Storage through `gcpSts`, and an AWS API through `awsWebIdentity` and `metadata` with a 900-second duration, each through the exchange issuer's public discovery, and a federation token refused at each provider registration of the exchange issuer. |
| 7 | Provider calls at a refresh burst across many rows, with the lease bounding calls to one per row. |
| 8 | The Token Service restarted during a refresh, `lenny-egress` unavailable during a refresh with the cached token served until expiry, and the provider unreachable for longer than `refusalGraceSeconds` leaving the row `active`. |
| 9 | A cross-tenant probe on every route and row; a hostile token endpoint with redirects, oversized bodies, and an SSRF target at registration; a hostile issuer whose metadata names a vendor host; a captured client assertion replayed at another server; a federation token presented to a stand-in that trusts the exchange issuer; an `error_description` carrying markup and control characters rendered in the notification; and no secret, JWT, or token in any slot file, environment, or response under `inject`. |
| 11 | Every error code, reason, metric, alert, and audit event of this design has its documentation page. |

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

## Alternatives rejected

| Alternative | Reason |
| :-- | :-- |
| Keep decision 14 of `0151-user-credential-delivery`: tenant stored-secret bindings for service sessions | Any session of the tenant can name a binding, so nothing ties the credential to one service account. The owner adopted option (b) (D1). |
| A tenant binding per service account with a new `principals` filter on the binding | It duplicates the per-principal rows, rotation, and release check that the `userCredential` machinery already has, and adds a second release rule. |
| Let a service account write its own row through `POST /v1/credentials` (`self`), or through an admin route when it holds `manage_user_credentials` | A stolen service bearer could then plant a token that later sessions of that service account use. An admin writer that is a different principal keeps the write separate from the identity that uses it (SAP-11). |
| Carry the caller type through every ownership, derive, invalidation, and erasure check instead of namespacing subjects | Every one of those controls, and every later one, would need the change, and a missed one would reopen the collision. The namespace holds at the identity boundary for all of them (SA-6). |
| A per-tenant registry of the first caller type seen for each subject | It refuses a collision only after one side has signed in, and a reissued identifier can still reach the other side's records before that. |
| Sign exchange JWTs with the federation key set (revision 1's O1) | AWS and Entra cannot condition on `principal_type`, and AWS used one audience for both kinds, so an agent-readable federation token passed a trust that did not pin the exact subject (review finding 3). |
| Sign exchange JWTs with the identity key set | It mixes operator-service assertions with provider credentials, its routes may be internal, and it signs with ES256, which Entra has not tested. |
| Let the author set `tokenEndpoint` and `jwtAudience` independently (revision 1) | A tenant admin could have the Token Service sign a JWT with a vendor audience for any service account and post it to her own server (review finding 1). |
| `issuerExchange` for persons (revision 1's O3 recommendation) | `entraFederated` returns an application token rather than acting as the person, and a tenant admin writing a person's row would attach that person to any provider identity (review finding 4). |
| `metadata` under the `userCredentialDelivery` grant with a duration up to the role's maximum (revision 1's O6 recommendation) | A tenant holding the grant for bearer files would gain AWS signing credentials lasting up to 12 hours with no new platform decision (review finding 5). |
| Exchange per session, with the session in the exchange JWT | It multiplies provider calls by the session count against providers that throttle token requests, and the provider's token carries no session either. `credential.mint` records which sessions received each token. |
| Give the runtime a per-principal JWT in a file | It puts a JWT that a provider trusts for the principal within the agent's reach, which `delivery: federation` already offers per binding behind `agentReadableDelivery`. The owner asked for the Token Service to exchange and `netagent` to inject (D3). |
| Re-discover the token endpoint at run time | A discovery answer that changed would move a stored secret to a new endpoint without a record change. |
| Map provider refusals to a link prompt | No person stands behind a service account to answer it, and the tenant's admins learn of it through the audit event, the notification, and the alert. |

## Steps

| Proposal | Contents | Depends on |
| :-- | :-- | :-- |
| SA-A | SA-6 and SA-1: the subject namespaces at the identity boundary, the subject-naming writes, and token issuance; the release rule, `principalTypes`, the principal-keyed rows, the service account routes with the self-write refusal, the reasons at create, and the audit fields. Closes no finding. | UC-1, UC-3, EG-9, and TS-6 |
| SA-B | SA-2 with the shared parts of SA-4: the client credentials grant, client assertions with the audience binding, generated keys and the one-key-per-row rule, discovery, the cache per row, the lease, the failure states, the stored error text, and the metrics. | SA-A and EG-2's mint path |
| SA-C | SA-3: the exchange issuer with its key set, routes, and rotation, the exchange JWT, the `entraFederated`, `gcpSts`, `jwtBearer`, and `tokenExchange` profiles with their fixed or bound endpoints and audiences, the approval through `tokenTrust.allowedRecords`, and the egress design's revision 92 hooks. | SA-B, EG-4, and SO-2 |
| SA-D | SA-5's `file` for the new sources, and the `awsWebIdentity` profile with `delivery: metadata`, the `userCredentialMetadataDelivery` grant, the 900-second duration, and the exposure records. | SA-B, SA-C, UC-5, and EG-4's `metadata` delivery |

SA-A can land with `registered` alone, and SA-B and SA-C each add a source. SA-6 lands in SA-A because a service account must not own a credential while its subject can equal a person's.

## Sources

Standards, read on 2026-11-15:

- RFC 6749, The OAuth 2.0 Authorization Framework, sections 2.3.1, 4.4, 5.1, and 5.2: <https://www.rfc-editor.org/rfc/rfc6749.html>
- RFC 7523, JWT Profile for OAuth 2.0 Client Authentication and Authorization Grants, sections 2.1, 2.2, 3, and 3.1: <https://www.rfc-editor.org/rfc/rfc7523.html>
- RFC 10070, Updates to OAuth 2.0 JWT Client Authentication and Assertion-Based Authorization Grants, November 2026, which updates RFC 7521, 7522, 7523, and 9126: <https://www.rfc-editor.org/rfc/rfc10070.html> and <https://www.rfc-editor.org/info/rfc10070>
- RFC 8693, OAuth 2.0 Token Exchange, sections 2.1, 2.2.1, 2.2.2, and 3: <https://www.rfc-editor.org/rfc/rfc8693.html>
- RFC 8707, Resource Indicators for OAuth 2.0, section 2: <https://www.rfc-editor.org/rfc/rfc8707.html>
- RFC 8414, OAuth 2.0 Authorization Server Metadata, sections 2, 3, and 3.3: <https://www.rfc-editor.org/rfc/rfc8414.html>
- RFC 8705, OAuth 2.0 Mutual-TLS Client Authentication and Certificate-Bound Access Tokens, sections 2 and 3: <https://www.rfc-editor.org/rfc/rfc8705.html>
- OpenID Connect Core 1.0, section 9: <https://openid.net/specs/openid-connect-core-1_0.html>

Vendor pages, read on 2026-11-15:

- Microsoft Entra, client credentials flow, with the token endpoint: <https://learn.microsoft.com/en-us/entra/identity-platform/v2-oauth2-client-creds-grant-flow>
- Microsoft Entra, certificate credentials and the assertion format: <https://learn.microsoft.com/en-us/entra/identity-platform/certificate-credentials>
- Microsoft Entra, workload identity federation considerations, with the 20-credential limit, the 600-character fields, the single audience and its recommended value `api://AzureADTokenExchange`, the RS256 statement, and propagation. It states no key set cache period: <https://learn.microsoft.com/en-us/entra/workload-id/workload-identity-federation-considerations>
- Google Cloud, workload identity federation with other providers, with the issuer, key set, algorithm, default audience, and lifetime requirements: <https://docs.cloud.google.com/iam/docs/workload-identity-federation-with-other-providers>
- Google Cloud, workload identity federation overview, whose attribute table states that `google.subject` "can't exceed 127 characters", re-read on 2026-11-15 after the security review could not confirm it on the other page: <https://docs.cloud.google.com/iam/docs/workload-identity-federation>
- Google Cloud, Security Token Service `token` method: <https://docs.cloud.google.com/iam/docs/reference/sts/rest/v1/TopLevel/token>
- Google Cloud, IAM Credentials `generateAccessToken`: <https://docs.cloud.google.com/iam/docs/reference/credentials/rest/v1/projects.serviceAccounts/generateAccessToken>
- AWS STS, `AssumeRoleWithWebIdentity`, with `DurationSeconds` from 900 to 43,200 and a role maximum of 1 to 12 hours, the `Policy` parameter of at most 2,048 characters, the 6-to-255-character `SubjectFromWebIdentityToken`, and the errors `ExpiredToken` ("expired or is not valid"), `IDPCommunicationError`, `IDPRejectedClaim`, and `InvalidIdentityToken` ("could not be validated by AWS"): <https://docs.aws.amazon.com/STS/latest/APIReference/API_AssumeRoleWithWebIdentity.html>
- AWS IAM, creating an OIDC identity provider, with the discovery fields, algorithms, the audience as a client identifier, the key and audience limits, and `InvalidIdentityToken` with "Couldn't retrieve verification key from your identity provider" for key retrieval failures. It states no key set cache period: <https://docs.aws.amazon.com/IAM/latest/UserGuide/id_roles_providers_create_oidc.html>
