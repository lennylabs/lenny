# Tenant scoping and identity

Status: design for owner review, revision 1, 2026-11-02. Nothing here is built or staged, and no test or spike was run for it. Statements about the tree were checked by reading the code and specification on this date. Proposals carry the label TS-n.

## Problem

A company runs Lenny as a multi-tenant SaaS. One of its customers has several teams and wants each team to have its own egress and git configuration, with SSO and user lifecycle coming from the customer's own directory.

The following gaps stand in the way:

- In `egress-and-git-design.md`, egress policy, VCS credential pools, and credential bindings are tenant-wide. Any client in a tenant can name any `onRequest` binding or allowlisted repository, and any agent in a tenant can fetch any allowlisted repository at run time. §10.6 Environments select runtimes and connectors only.
- Lenny trusts one OIDC issuer per deployment and has no SCIM, so a SaaS operator needs an identity broker for per-customer SSO. The specification does not say so, and it does not state what such a broker must guarantee.

## Scope

This design covers Environment scoping of the objects in `egress-and-git-design.md`, the identity model a SaaS operator builds on, and the user lifecycle path into Lenny. It treats those egress objects as fixed inputs. `security-oversight-design.md` owns security roles.

## Requirements

- R1. A tenant admin can give an Environment its own VCS credential pools, credential bindings, host lists, and routes.
- R2. An object with no Environment scope keeps the meaning `egress-and-git-design.md` gives it.
- R3. Scoping holds at run time, against an agent acting inside a session, as well as at session create.
- R4. A narrowing that Lenny cannot enforce on a given pod is refused rather than ignored.
- R5. The specification states which party provides per-customer SSO, SCIM, and login routing.
- R6. A customer's directory can end a departing user's access to Lenny, including running sessions, within a bound the directory and the operator control.
- R7. No customer-controlled input can produce authority over another tenant.

## Security properties

- TP1. A session in Environment E can use a scoped object only when the object lists E. For credentials, the Token Service and `lenny-egress-authz` each hold this independently, from their own reads of the session row and the object.
- TP2. A session's Environment is set by the gateway at create, checked against the caller's membership, and never changes. Nothing in a pod can set it.
- TP3. A session outside an Environment, and a pod with no per-session policy, sees unscoped objects alone. Lack of an Environment never widens access.
- TP4. A caller receives the same refusal for an object scoped to another Environment and for an object that does not exist, so team object names do not leak across teams.
- TP5. An IdP-verified token cannot confer `platform-admin`.
- TP6. Environment scoping is a policy boundary inside a tenant. It carries none of the pod pinning or row-level isolation that P4 and §12 give the tenant boundary. "Residual risks" states what follows.

## Current state

Each row was checked against the named source.

| Claim | Status | Source |
| :-- | :-- | :-- |
| `auth.oidc.issuerUrl` and `auth.tenantIdClaim` are single Helm values | True. `auth.oidc.clientId` is also single. Each renders to one flag on the gateway and the Token Service. | `charts/lenny/values.yaml`, `gateway-deployment.yaml`, `token-service-deployment.yaml` |
| One IdP verifier exists per process | True. `buildIdPVerifier` and `buildCallerVerifier` build one `OIDCVerifier`, which `TenantClaimVerifier` wraps. | `cmd/lenny-gateway/token_signing.go`, `cmd/lenny-token-service/caller_verifier.go` |
| A per-tenant identity provider exists | Partly. §10.6 **Tenant RBAC Config** lists `identityProvider (OIDC)`. In code, that record holds only `type` (`oidc` or empty) and the RFC 7662 introspection settings. It cannot name an issuer. | `pkg/gateway/externalapi/admin/rbac_config.go` |
| The specification has SCIM | False. No match in `spec/`, `pkg/`, `cmd/`, or `charts/`. | Search |
| Groups reach Lenny through tokens or introspection | True. `groups` is a fixed claim name, and introspection replaces the set for a tenant that turns it on. | `pkg/auth/jwt/jwt.go`, `pkg/gateway/middleware/auth/auth.go` |
| IdP tokens can assert roles | True, including `platform-admin`. See "Defects found while verifying". | `Claims.Roles`, `normalizeUserBearer`, §10.2 **Role assignment:** |
| A session records its Environment | Yes for a create that names one, in `sessions.environment`. Absent for a transparent-path create. No code sets it on a delegated child, a derived session, or a replayed session. | `create.go`, `derive.go`, `replay.go`, `delegation/service.go` |
| A precedent for Environment-scoped credentials exists | Yes. User credentials and connector tokens carry an `environment` column in which the empty string means the no-environment scope (§4.3 and migration 0059). | `credentialstore.go` |

## TS-1: Environment scoping

### A session's Environment

TS-1 relies on TS-3, which makes `sessions.environment` dependable. A session has a value E when its create named an Environment through `/v1/environments/{name}/sessions`, `/mcp/environments/{name}`, a scoped model namespace, or the `environment` body field. All of these already run the explicit admission check (membership, at least `creator`, and the runtime selector). A transparent-path session has no E, and the formulas below write that as E = ∅. See decision 3.

### What is scoped

Scoping applies to objects the platform evaluates per session.

| Object | Scoped | Reason |
| :-- | :-- | :-- |
| VCS credential pool | Yes, through `environments` | Selected per repository at mint time |
| Credential binding | Yes, through `environments` | Resolved per session at I8 |
| Always-allowed, session-requestable, and denied hosts | Yes, through `environmentPolicies` | Read per decision |
| Egress routes and git routes | Yes, through `environmentPolicies` | Read per decision. A route can name a connector once OC-1 lands, with no further change. |
| Tenant warm-up additions | No | They apply before any session exists. |
| Tenant pool overlay | No | It applies at pod creation or per pool and tenant. |
| Egress group and CA trust bundle | No | Each is fixed at pod creation or dialled by tenant. |
| Client config files | No | `netagent` writes them at tenant binding. See decision 4. |
| Per-class gates | No | They remain platform grants per tenant. |
| Tenant-registered upstreams and git endpoints | No | The registration is tenant-wide, and a scoped route selects it. |

### API

The objects gain these fields through the admin API resources that EG-1, EG-4, and EG-9 define:

```yaml
vcsCredentialPool:
  name: web-github
  repositoryPrefixes: ["github.com/acme-web/"]
  environments: [web]            # absent: tenant-wide

credentialBinding:
  name: payments-stripe
  scope: onRequest
  hosts: [api.stripe.com]
  environments: [payments]

tenantEgressPolicy:
  alwaysAllowed: [npm, pypi]
  sessionRequestable: [huggingface.co]
  denied: [pastebin.com]
  environmentPolicies:
    payments:
      alwaysAllowed: [stripe-api]
      sessionRequestable: []
      denied: [huggingface.co]
      egressRoutes: [{ hosts: ["*.stripe.com"], upstream: acme-pci-swg }]
      gitRoutes: []
```

The rules for these fields are:

- `environments` is either absent or a non-empty list. An empty list is refused with `422 VALIDATION_ERROR`, because it has no clear reading.
- Every name must be an existing Environment in the tenant, or the write fails with `422 ENVIRONMENT_NOT_FOUND`. An `environmentPolicies` key follows the same rule.
- A deployment with no Environment registry wired refuses these fields with `422 ENVIRONMENTS_NOT_AVAILABLE`, since §10.6 admission runs open in that posture.
- `DELETE /v1/admin/environments/{name}` fails with `409 RESOURCE_HAS_DEPENDENTS` while any object or policy key names the Environment. A recreated Environment therefore inherits nothing.
- A tenant warm-up addition cannot name a repository whose only covering pool is scoped, because a tenant-bound pod with no session has no E.
- Two pools can hold the same repository prefix only when their `environments` lists are disjoint, and neither may be tenant-wide. Selection takes the longest prefix among the pools visible to E, so exactly one pool answers for any repository and Environment. This lets two teams use separate GitHub Apps on one organization.

The effective-policy read gains an optional `environment` parameter and reports `environment` as a deciding layer. `GET /v1/egress/decisions` and the evidence export gain an `environment` filter. Both wait for EG-8.

### Data model

- VCS pool and binding rows gain a nullable `environments` text array. NULL means tenant-wide.
- The tenant egress policy document gains `environmentPolicies`, keyed by Environment name.
- Neither is a foreign key, following the reasoning in migration 0059. The write check and the delete guard hold the reference.
- The existing `sessions.environment` column holds E. No new session column is needed.
- All tables are tenant-scoped under the existing row-level security policy.

### Effective policy

Define the following, where E = ∅ selects no Environment entries:

- `visible(objects, E)` is the objects with no `environments` list, plus those whose list contains E.
- `always(E)` is tenant always-allowed ∪ `environmentPolicies[E].alwaysAllowed`.
- `requestable(E)` is tenant session-requestable ∪ `environmentPolicies[E].sessionRequestable`.
- `denied(E)` is tenant denied ∪ `environmentPolicies[E].denied`.
- `routes(E)` is `environmentPolicies[E]` routes in order, followed by tenant routes in order.

The formulas from "Effective policy" in `egress-and-git-design.md` become:

| Case | Formula | Change |
| :-- | :-- | :-- |
| Unbound pod | `ceiling ∩ pool warm-up policy` | None |
| Tenant-bound pod without a session | `ceiling ∩ (always(∅) ∪ tenant warm-up additions) − denied(∅)` | Environment entries do not apply. |
| Pod with one session | `ceiling ∩ session allowed − denied(E)` | `session allowed` is the plan's list, or `always(E)` when the plan sets none. Each list entry must lie within `always(E) ∪ requestable(E)`. |
| `restricted` pool with `gitAccess` | `gitAccess hosts ∩ git hosts(E) − denied(E)` | `git hosts(E)` takes hosts from `visible(VCS pools, E)` and from the explicit git routes in `routes(E)`. |
| Pod with concurrent sessions | `ceiling ∩ always(∅) − denied(∅)` | Environment entries do not apply, for the mechanical reason the original formula states. |

Consequent rules:

- **Bound.** The pool ceiling bounds every Environment entry. A tenant deny applies in every Environment, so an Environment cannot re-allow a host the tenant denies.
- **Repositories.** Readable repositories are the session's `gitClone` sources, the allowlists of `visible(VCS pools, E)`, and public `sharedAssets` entries. Writable repositories must lie within `visible(VCS pools, E)` at bind and at each mint.
- **Bindings.** A session attaches `always` bindings from `visible(bindings, E)`, and it can name `onRequest` bindings from the same set.
- **Pods with no per-session policy.** On a concurrent-session pool or a service-mode pool, `denied(E)` cannot be enforced. A create or service-mode request that names an E with a non-empty `environmentPolicies[E].denied` is refused with `422 ENVIRONMENT_POLICY_NOT_ENFORCEABLE`, under R4. Without such denied entries, the request proceeds with unscoped objects.

### Enforcement points

The session create check alone is insufficient. An agent can run `git fetch` at any time against any repository on the tenant's allowlist, and `always` bindings attach with no plan entry, so most of the enforcement happens after create.

| Point | Component | Check |
| :-- | :-- | :-- |
| Session create | Gateway | Named bindings, `gitClone` sources, `writableRepositories`, and `egress.allowedHosts` lie within the sets above for E. |
| Ref resolution | Gateway through I3 | The decision service reads E from the session the request names. No new header reaches a level 2 proxy. |
| CONNECT | `lenny-egress-authz` | The formula for the pod's case, with E from `pkg/binding`. |
| Mint or secret on I8 | Token Service, then I3 | Each component reads E and the object, and both must agree (TP1). Nothing is minted on refusal. |
| Fetch from the mirror | Mirror through I3 | Readable repositories for E on every request. The tenant's store stays shared, and authorization separates teams. |
| Git path and binding hosts | `netagent` | No new logic. It enforces what I7 and I8 return, and it flushes credentials at session end and on each binding change, so a sequentially reused pod does not carry a credential across sessions. |

`netagent` needs no Environment concept. The cache key `egress-and-git-design.md` uses for `always` bindings (the tenant pin) stays safe because the flush at session end precedes the next session.

### Failure behaviour

| Condition | Result |
| :-- | :-- |
| An object outside E is named at create | `422` with the same code and message as an unknown object (TP4) |
| Environment store or policy read fails at create | `500 INTERNAL_ERROR`, as admission does today, and no session is created |
| `pkg/binding` cannot read the session row | Fails closed, with the existing retryable error for a binding that is not yet visible |
| A tenant admin removes E from a pool or binding during a session | The object's version bumps, the gateway pushes a binding change, `netagent` flushes, and the next I8 request is refused. The session continues without it. |
| A tenant admin removes a host from `requestable(E)` during a session | Applies to new sessions, as the tenant list does in `egress-and-git-design.md`. Adding the host to `denied(E)` closes it at the next decision. |
| A member leaves the team during a session | The session keeps E, because membership is a check at create. See "Residual risks". |
| An Environment vanishes under a live session through a store-level race | `visible` finds no entries for E, which narrows the session to unscoped objects. |

### Who can do what

| Action | `platform-admin` | `tenant-admin` | Environment `admin` | Environment `creator` | `user` with no role in E |
| :-- | :-- | :-- | :-- | :-- | :-- |
| Set `environments` or `environmentPolicies` | Yes | Yes, own tenant | No, unless TS-5 lands | No | No |
| Read the scoped objects | Yes | Yes | No admin read | No | No |
| Create a session in E | No, unless a member | No, unless a member | Yes | Yes | No |
| Name a scoped object in a plan | Only in a session in E | Only in a session in E | Yes, in E | Yes, in E | No |

Writes pass through EG-9's single write path, so they carry its audit event and any SO-2 guardrail. A `tenant-admin` remains able to scope any object to any Environment and add itself as a member, which matches §10.2's statement of tenant authority.

### Why not separate tenants

A customer can give each team its own tenant today. That yields tenant pinning, row-level isolation, and separate keys, which Environments do not provide. It also means no shared policy, no shared admin, no combined billing rollup, and no delegation between teams, since Lenny has no grouping above a tenant. Teams needing an isolation boundary should be tenants, and teams needing separate configuration under one administration should be Environments. The documentation states this choice.

### Does TS-1 need a hook

The outline said no, on the basis that TS-1 adds an optional field and a check at create. That holds for the fields, which are additive. It does not hold overall, because enforcement is at run time in components EG-2 ships, evidence records are written from day one, and EG-1 fixes the prefix rule. See "Changes requested of egress-and-git-design.md".

## TS-3: Session Environment context

TS-1 cannot rest on the current propagation. TS-3 lands first and depends on nothing in `egress-and-git-design.md`.

- **Derive and replay.** The new session takes the source's Environment, and the explicit admission check runs against it. A caller who has left the Environment gets `403`.
- **Delegated child.** The child takes the parent's E when the parent's `runtimeSelector` admits the target runtime. Otherwise it takes the `targetEnvironment` of the first outbound declaration in the parent Environment's list that permits the target and that the target Environment's inbound side accepts, which is the §10.6 **Gateway enforcement at delegation time** check. The order is under the source Environment's control and stays deterministic when other Environments are added. A parent with E = ∅ has a child with E = ∅.
- **Effects on existing features.** Billing rollup, retention tier, default delegation policy, and §4.3 credential scope read the same field, so a child and a derived session now follow their Environment in each. §10.6 **Connectors are never cross-environment** already states this outcome for connectors.
- **Deletion guard.** §15.1 says deletion of an Environment is blocked while sessions are active in it. `handleDeleteEnvironment` and the store's `Delete` contain no such check. TS-3 adds it.
- **Naming.** §10.6 **V1 data model accommodations** describes `environmentId` on session creation as a no-op stub. The code reads a body field named `environment` and enforces it, and `?environmentId=` on `GET /v1/runtimes` narrows the list. The specification changes to match the code.
- **Transparent create.** `requireEnvironmentAdmission` admits any member of any Environment on the transparent path, with no role check or selector check. A `viewer` can therefore create a session on a runtime the transparent filter hides from it. No other check was found in `pkg/gateway/sessionserver`. TS-3 requires at least `creator` in an Environment whose selector admits the runtime.

## TS-2: Identity model

### Options

Option A is a SaaS-operated identity broker. Lenny trusts one issuer, the broker. The broker federates to each customer's IdP, terminates SCIM, and issues tokens carrying the tenant, roles, and groups. Option B has Lenny hold a per-tenant OIDC configuration and serve SCIM itself.

| Concern | A: broker | B: per-tenant OIDC and SCIM in Lenny |
| :-- | :-- | :-- |
| Lenny code for SSO | None beyond the changes in this section | A tenant IdP record, a verifier registry that routes on an unverified `iss` and rejects unknown issuers before any fetch, per-tenant JWKS caches on the gateway and the Token Service, and per-tenant claim mapping for the tenant, roles, and groups |
| Issuer URLs from tenants | None | Each is a tenant-supplied URL the gateway fetches, so it needs the SSRF checks of EG-6's registrations, and the gateway NetworkPolicy needs an egress rule for arbitrary IdPs |
| Issuer ownership | Broker concern | Lenny must prove a tenant controls the issuer or domain it registers, or one tenant can claim another's IdP |
| Protocols | Whatever the broker federates, including SAML | OIDC alone. SAML-only customers need a SAML service provider as well. |
| Login routing | One authorization endpoint. The broker does home-realm discovery. | Lenny needs discovery by domain or path across the playground, `lenny-ctl`, and MCP clients. The gateway has no protected resource metadata route today, and each client learns the issuer out of band. |
| SCIM | Broker or vendor endpoint | A SCIM 2.0 server per RFC 7643 and RFC 7644: users, groups, PATCH, filtering, per-tenant bearer credentials, and conformance work against each directory's behaviour |
| Group storage | None. Groups stay in tokens. | New tables, and a change to `envaccess` to read them. Erasure and §12.9 classification follow. |
| Subject stability | The broker issues `sub`, so a customer moving from one IdP to another keeps its users. | `sub` belongs to each IdP. Moving IdP orphans session ownership (§10.2 **Session ownership.**) unless Lenny adds account linking. |
| Several IdPs in one tenant | Broker concern, and it namespaces `sub` and groups | Lenny must namespace both, since `(tenant_id, subject)` is the user key and `oidc-group` is a bare string match |
| Tenant boundary | One claim from one issuer. A broker fault or mapping error crosses tenants. | Issuer pinned to tenant, so one customer's IdP cannot assert another tenant |
| Outage | A broker outage stops logins for all tenants. Running sessions continue, and the verifier keeps cached keys when a refresh fails. | A customer IdP outage affects that tenant alone |
| Operator's work | Run or buy a broker, and write a sync worker | Run Lenny's new surface, and handle customer IdP support inside Lenny |
| Single-company deployment | Unaffected, using its own IdP as the issuer | Carries the new code path unused |

No effort estimate was measured for either option. Option B is a new subsystem, and it does not remove the operator's need for a customer-facing configuration portal, which §10.2 lists as a future goal.

An intermediate option accepts a list of issuers with each pinned to a tenant, with no SCIM and no login routing. It closes the shared-claim risk at the cost of the verifier registry, tenant-supplied URLs, and login routing, so it has most of B's security cost and little of its product value. It also serves an operator that runs one Keycloak realm per customer, since each realm is its own issuer. Decision 8 names it as the fallback.

### What comparable systems do

- Keycloak's organizations link customer IdPs to an organization inside one realm, so one issuer serves all customers. Its token claim is an object keyed by organization alias. Keycloak's SCIM is per realm, and its documentation says organization groups cannot have members managed through SCIM ([project documentation](https://www.keycloak.org/docs/latest/server_admin/index.html)). A vendor extension adds [per-organization SCIM as experimental](https://phasetwo.io/blog/orgs-scim-experimental/) (vendor source). An operator choosing Keycloak in one realm therefore needs that extension or another SCIM path.
- Auth0 organizations put `org_id` in tokens, and its documentation calls validating that identifier the preferred method ([vendor documentation](https://auth0.com/docs/organizations/using-tokens)). Its [device flow does not natively carry organizations](https://support.auth0.com/center/s/article/missing-org-id-in-auth0-device-authentication-flow) (vendor source), which matters to a CLI login.
- WorkOS sells Directory Sync, which [hides SCIM behind an API and delivers changes through webhooks or an events API](https://workos.com/docs/directory-sync) (vendor documentation).
- Products sold directly to enterprises expose SSO and SCIM themselves: [Devin](https://docs.devin.ai/enterprise/security-access/sso/guide.md) and [Cursor](https://cursor.com/docs/account/teams/scim) (vendor documentation). Those pages do not say whether a broker sits behind them. Devin's page states that without SCIM, groups sync only at login, and that the vendor's team configures the connection. Lenny's direct customer is the operator, and the operator holds the position these vendors hold.
- [Coder](https://coder.com/docs/admin/users/idp-sync.md) syncs groups, roles, and organizations from claims per organization, and it overwrites membership at login (vendor documentation).

### Recommendation

Adopt option A. The specification states that one issuer is trusted per deployment, and that per-customer SSO, SCIM, and login routing belong to the operator's broker or IdP. Lenny then provides what any broker needs from it: a stated contract, a hard boundary on platform authority, and a working lifecycle path. These costs remain with the operator, and the documentation states them: a broker to run or buy, a sync worker, and a tenant boundary that rests on the broker's correctness.

Per-tenant OIDC remains additive later, because `composeBearerVerifier` already composes verifier members, and it can be revisited when the triggers in decision 8 occur.

### Broker contract

These go into §10.2 as requirements on the issuer named by `auth.oidc.issuerUrl`.

| Claim | What Lenny does | Requirement on the broker |
| :-- | :-- | :-- |
| `iss`, `aud`, `exp`, `nbf` | Verified against `auth.oidc.issuerUrl` and `auth.oidc.clientId` | One issuer and one audience for all tenants |
| Tenant claim (`auth.tenantIdClaim`) | Read as a string. Refused when absent, malformed, or unregistered. | Set from the broker's organization record. Never derived from a user attribute or a claim a customer can write. A nested claim needs a mapper to a string. |
| `sub` | The user key within a tenant, and session owner | Stable, never reassigned, and unique within a tenant across all of that customer's IdPs |
| `roles` | An array of role names | Tenant-scoped roles only. A customer's own mapping must not reach it unfiltered. |
| `groups` | An array matched exactly against `oidc-group` members | Stable names, namespaced when a tenant has several IdPs. A complete list or none: Entra [limits a JWT to 200 groups](https://docs.azure.cn/en-us/entra/identity/users/directory-service-limits-restrictions) (provider documentation), and past that a broker must resolve the set itself. An absent claim reads as no groups, so the failure is a refused Environment. |

The broker must also keep token lifetimes short enough for the operator's offboarding bound, and it must deliver lifecycle events as described below.

### Role boundary

§10.2 **Role assignment:** allows an OIDC claim to carry any built-in role. In code, an IdP-verified token's `roles` reach `Principal.Roles` unchanged, and `platform-admin` then spans all tenants. With a broker, one mapping error gives a customer the platform.

TS-2 changes this as follows:

- The IdP boundary, beside `normalizeUserBearer`, removes `platform-admin` from an IdP-verified token's roles on the gateway and on the Token Service. The request continues with the remaining roles. Each removal emits an `auth.platform_role_dropped` audit event and a counter with an alert.
- `platform-admin` then comes from a platform-managed role row, a Lenny-issued token, or a §25.4 service identity. `/v1/admin/users` already allows a `platform-admin` to grant that role, and §15.1 must list that surface, which it omits today. The operator's staff log in through the broker under the operator's own tenant, and a row grants their role.
- The specification and the code agree on claim names (decision 10).

### User lifecycle (TS-4)

An operator's directory path runs: customer directory, SCIM to the broker or a vendor, sync worker, Lenny. Today the last hop fails in these ways:

- `POST /v1/admin/users/{userId}/invalidate` answers `404` when the user has no row. Rows come only from the admin API, so a user who only ever logged in through an IdP cannot be invalidated, and that user's sessions keep running.
- `requireActiveUserForAction` admits a user with no row, so an absent row also means no gate on a fresh token.
- `hard_disable` and `full_revoke` set `DeletedAt`, and no endpoint clears it, so a returning employee cannot be reinstated. Entra [reactivates users that move from soft-deleted to active](https://learn.microsoft.com/en-us/entra/identity/app-provisioning/how-provisioning-works) (provider documentation).
- Invalidation needs `manage_users`, which also assigns roles. A worker holding it has more authority than deprovisioning requires.

TS-4 makes these changes:

- **Upsert on invalidate.** Invalidating an unknown subject creates its row in the invalidated state and runs the §11.4 steps, which list sessions by owner and do not depend on the row having existed.
- **Reinstate.** `POST /v1/admin/users/{userId}/reinstate` clears the invalidated state and restores nothing else. It refuses a user under an §12.8 erasure, and it does not revive a row removed through `DELETE`.
- **Ordering.** Both calls accept `observedAt`. A call older than the row's last lifecycle change answers `200` with `applied: false`, so a retried event cannot reverse a later one. Both calls are idempotent.
- **Permission.** A new `manage_user_lifecycle` permission covers these calls alone. `tenant-admin` holds it, and a custom role can hold it alone, such as `identity-sync`. The worker authenticates per tenant, through the client credentials path of §10.2, with that role.

Group changes have no Lenny API. A new token or an introspection call carries them, and §10.6 introspection is available per tenant for an operator that needs current groups at each request. When the worker cannot reach Lenny, the event waits in the worker, and a user stays active until delivery succeeds. A partial failure reported in the invalidate response is retried by the worker, as the idempotent call allows. For a caller without `platform-admin`, §11.4 step 5 reports the impersonation cascade as skipped, which is correct for a customer user, and the operator invalidates its own staff with a `platform-admin` caller.

Offboarding latency is the directory's delivery time plus the sync worker's. Microsoft documents incremental cycles [at intervals defined per application](https://learn.microsoft.com/en-us/entra/identity/app-provisioning/how-provisioning-works) (provider documentation), with no general figure on that page. Until delivery, a user can create sessions while a token is valid. Lenny-issued tokens default to 900 seconds and are capped at 24 hours for `lenny-gateway` (§13.3 **`exp` granularity and parent-expiry handling.**), and a running session does not depend on its creator's token, so only invalidation ends it.

### Who can do what (identity)

| Action | Operator (`platform-admin` or broker admin) | Customer IdP admin | `tenant-admin` | Sync worker |
| :-- | :-- | :-- | :-- | :-- |
| Connect a customer IdP to the broker | Yes | Through the broker's portal, if offered | No | No |
| Create the tenant and the broker organization | Yes | No | No | No |
| Set tenant-scoped roles | Through mapping or row | Through broker mapping, if allowed | Through row | No |
| Grant `platform-admin` | Row, by `platform-admin` | No | No | No |
| Invalidate and reinstate users | Yes | No | Yes | Yes, own tenant |
| Assign groups | Broker | Directory | No | No |

## Configuration by scope

| Setting | Description | Scope | Managed by |
| :-- | :-- | :-- | :-- |
| `auth.oidc.issuerUrl` and `auth.oidc.clientId` | The one issuer and audience the gateway and the Token Service trust for IdP tokens. | Deployment | Platform admin (Helm) |
| `auth.tenantIdClaim` | The claim holding the tenant identifier on an IdP token. | Deployment | Platform admin (Helm) |
| `auth.rolesClaim` and `auth.groupsClaim` (decision 10) | The claims holding roles and groups on an IdP token, defaulting to `roles` and `groups`. | Deployment | Platform admin (Helm) |
| Broker organization, IdP links, and claim mappers | The per-customer SSO configuration and the source of each contract claim. | Broker, per customer | Operator, or customer through a broker portal |
| Directory and SCIM connection | The source of user and group changes for a customer. | Broker or vendor, per customer | Customer IdP admin with the operator |
| `identityProvider` introspection settings | An RFC 7662 endpoint that supplies current groups for each request. | Tenant | Tenant admin (`rbac-config`) |
| `noEnvironmentPolicy` | Whether a user with no Environment can reach runtimes. Such a user's sessions have E = ∅. | Deployment default, tenant | Platform admin, tenant admin |
| Platform-managed role row | A user's role, which overrides the token's roles. | User in a tenant | Tenant admin, and platform admin for `platform-admin` |
| `identity-sync` custom role | A role holding `manage_user_lifecycle` alone, for the sync worker. | Tenant | Tenant admin or operator |
| Environment members | The groups and subjects with a role in an Environment. | Environment | Tenant admin |
| `environments` on a VCS credential pool | The Environments whose sessions can use the pool. Absent means tenant-wide. | Pool in a tenant | Tenant admin |
| `environments` on a credential binding | The Environments whose sessions can use the binding. Absent means tenant-wide. | Binding in a tenant | Tenant admin |
| `environmentPolicies[E].alwaysAllowed` | Hosts each session in E reaches by default, added to the tenant's. | Environment | Tenant admin |
| `environmentPolicies[E].sessionRequestable` | Hosts a session in E can request, added to the tenant's. | Environment | Tenant admin |
| `environmentPolicies[E].denied` | Hosts refused to sessions in E, added to the tenant's. | Environment | Tenant admin |
| `environmentPolicies[E].egressRoutes` and `gitRoutes` | Routes tried for sessions in E before the tenant's routes. | Environment | Tenant admin |
| Environment on a session | The Environment the session runs in, set at create and fixed thereafter. | Session | Client, subject to membership |

Every setting in the "Scopes and who manages them" table of `egress-and-git-design.md` that this table does not name stays as written there.

## Scenarios

**Two teams in one customer.** Tenant `acme` has Environments `web` and `payments`, with members from the groups `acme-web` and `acme-payments`. Pool `web-github` covers `github.com/acme-web/` for `web`, and pool `pay-github` covers `github.com/acme-pay/` for `payments`. The tenant allows `npm`. `payments` adds `stripe-api` and denies `huggingface.co`.

- Alice, in `web`, creates a session through `/v1/environments/web/sessions` that clones `acme-web/site`, and it succeeds.
- A plan from Alice naming `acme-pay/ledger` is rejected with the same answer as a repository outside every pool.
- An agent in Alice's session runs `git fetch https://github.com/acme-pay/ledger`. The scope check refuses it, nothing is minted, and `git` gets 403.
- Bob, in `payments`, reaches `api.stripe.com`, and Alice's session gets 403 for the same host.

**A session on the transparent path.** Alice calls `POST /v1/sessions` with no Environment, on a tenant whose `web` runtime she can see. The session has E = ∅ and reaches `npm`. A plan naming `web-github` fails. The error message tells a caller to use an Environment path for Environment objects, without naming any object.

**Two teams and one GitHub organization.** Pools `web-app` and `pay-app` both cover `github.com/acme/`, with separate GitHub Apps and allowlists, for `web` and `payments` respectively. A third, tenant-wide pool with that prefix is refused at write. GitHub enforces each App's own repository selection, so the teams' tokens differ.

**A team's cloud role.** Binding `payments-aws` has delivery `federation`, and it lists `payments`. The subject `lenny:acme:payments-aws` is stable. Acme's AWS trust policy names that subject, so only a session able to obtain this binding reaches the role. A session in `web` gets no token from I8.

**A shared pod on a recycling pool.** Pool `coding` has `recycle.enabled: true`. Alice's `web` session ends, `netagent` flushes its credentials, and Bob's `payments` session claims the pod. Bob's binding set and policy are correct, and any file state the scrub does not reach survives from Alice's session.

**A concurrent pool.** Pool `chat` serves concurrent sessions. Bob's create in `payments` fails with `ENVIRONMENT_POLICY_NOT_ENFORCEABLE`, because `payments` denies a host that the pool cannot deny per session. Alice's create in `web` proceeds, since `web` has no denied entries, and it receives unscoped objects.

**A delegated child.** Alice's `web` session delegates to a runtime that `web` selects, and the child has E = `web`. It then delegates to `shared-review`, which `web` declares outbound to `platform` and `platform` accepts inbound. The grandchild has E = `platform` and uses that Environment's objects.

**Onboarding a customer.** The operator creates tenant `globex`, a broker organization whose claim maps to `globex`, and a link to Globex's IdP. Alice, a Globex administrator, logs in. The broker issues `roles: [tenant-admin]` under a mapping the operator set. Alice creates Environments with Globex's group names.

**A departure.** Bob leaves Globex. He has logged in but has no user row. Globex's directory deactivates him, the broker receives the SCIM change, and the sync worker calls `invalidate` with `mode: full_revoke`, as `identity-sync` in tenant `globex`. Lenny creates the row, ends his sessions, and revokes his issued tokens. A later retry answers with `applied: false`. If Bob returns, the worker calls `reinstate`.

**A mapping error.** A broker change lets Acme's mapping reach `roles`, and a token arrives with `roles: [platform-admin]`. The gateway removes the role, the caller acts with no platform authority, and the alert fires. A separate error placing `tenant_id: globex` on Acme's token is not caught, which "Residual risks" states.

## Residual risks

- **Environment scoping shares pods.** On a recycling pool, state the scrub does not reach survives into another Environment's session within the tenant, as it does between users today. A team needing separation uses a pool with `recycle.enabled: false`, or a separate tenant (decision 5).
- **Shared tenant resources.** The tenant's mirror store, Token Service token cache, egress group, and P7 quotas are shared across Environments. One team can consume the tenant's quota (decision 6). One team's egress address is the same as another's, so a customer firewall rule admits both.
- **Tenant-wide objects stay tenant-wide.** A team cannot opt out of a tenant-wide binding or host. It can deny a host. The tenant admin must scope a binding to remove it from a team. Scoping a pool also leaves a repository reachable through any wider tenant-wide pool whose allowlist names it, so the write response warns when a scoped pool's repository appears on such an allowlist.
- **Membership at create.** A user removed from a group keeps running sessions, and those sessions keep E's objects, until the session ends or the user is invalidated. Removal from an Environment alone has no session effect.
- **Exfiltration between teams.** A session in `web` with a host open can send data anywhere that host accepts. Scoping limits credentials and destinations. It does not stop a user who belongs to two teams from moving data between sessions.
- **Broker correctness.** The tenant boundary at the gateway rests on one claim from one issuer. A broker fault or a compromised broker can issue a token for any registered tenant. A second claim from the same issuer adds nothing against a compromise, so none is required. This risk is the cost of decision 8.
- **Broker availability.** A broker outage stops new logins and token refresh for all tenants. Verification continues on cached keys, and running sessions continue.
- **Stale group data.** Without introspection, group changes wait for a new token. With introspection, a failure of the tenant's endpoint fails its requests closed with `503`.
- **Lifecycle delivery.** Lenny cannot detect a lost event. The operator's worker owns retry, and a user stays active during its outage.
- **Secret in `rbac-config`.** See "Defects found while verifying".

## Defects found while verifying

These exist independent of this design. They are for filing in `BUILD-GAPS.md` under the proposal named, and this document does not file them.

| Severity (proposed) | Defect | Closed by |
| :-- | :-- | :-- |
| High | An IdP-verified token's `roles` claim can assert `platform-admin` (`normalizeUserBearer` clears `typ`, `session_id`, `delegation_depth`, and `origin` only), and §10.2 **Role assignment:** permits it. | TS-2 |
| High | `invalidate` answers `404` for a user with no row, so a user known only through an IdP cannot be stopped, against §11.4's incident-response purpose. | TS-4 |
| Medium | Delegated, derived, and replayed sessions have no Environment, so their billing rollup, retention tier, and credential scope ignore it. | TS-3 |
| Medium | The transparent create path admits any member of any Environment, with no role or selector check. | TS-3 |
| Medium | Environment deletion has no active-session check, against §15.1. | TS-3 |
| Medium | `GET /v1/admin/tenants/{id}/rbac-config` returns `introspectionClientSecret`, which the tenant row stores in its JSON configuration with no redaction. | TS-4 |
| Medium | No endpoint reverses `hard_disable` or `full_revoke`. | TS-4 |
| Low | §10.2 and `docs/client-guide/authentication.md` name a `lenny_role` claim, and the code reads `roles`. `docs/tutorials/multi-tenant-setup.md` and `docs/operator-guide/security.md` show `auth.oidc.issuer`, `tenantClaim`, `roleClaim`, `jwksUri`, `audience`, and `auth.issuerUrl`, none of which the chart defines. | TS-2 |
| Low | `/v1/admin/users` exists in code and is absent from the §15.1 endpoint table. | TS-2 |

## Changes requested of egress-and-git-design.md

Revision 37 of `egress-and-git-design.md` applied the items below that had to land before its proposals, so they are history here rather than open requests.

This document does not edit that design. Each request names why adding it later would be rework.

| Request | Proposal | Why now |
| :-- | :-- | :-- |
| Add an optional `environment` attribution field to the evidence envelope, beside `sessionId`, which the decision service and the Token Service set from the session row, and a producer's own claim never sets. | EG-10 | It is a stored record format. A team's evidence cannot be backfilled once session rows age out, because evidence retention is per stream and is set apart from session retention. A team security reader needs the filter from the first record. |
| Have `pkg/binding` return the session's Environment with the resolved binding, read from the session row, and have the decision service, the Token Service, and the mirror reach tenant objects through one resolver taking tenant and Environment, which filters nothing until TS-1. | EG-2 | It is the same reason as the `quarantined` state: the enforcement point exists in several components. A request for the gateway's `ls-refs` reads Environment from the session it names, so level 2 proxies need no new header on the I3 contract. |
| State a rule for overlapping VCS pool prefixes, enforce it in the write validator rather than a unique index, and select the longest prefix from a candidate set the caller supplies. | EG-1 | "Repository routing" has no overlap rule today. A unique index on tenant and prefix would need a migration and a behaviour change for TS-1's disjoint-Environment case. |
| State which inputs a delegated child receives. The design says nothing on delegation. A child's §8.2 `TaskSpec` carries no host list, so under "Pod with one session" a child falls to the tenant's always-allowed hosts, which is wider than a parent that narrowed with `egress.allowedHosts`. The request is that a same-Environment child inherits the parent's list and named bindings. A child in another Environment gets that Environment's defaults only, with the bilateral declaration as consent. | EG-9 and EG-4 | It closes a widening path independent of TS-1, on the same reasoning as §8.3 isolation monotonicity, and it stores inherited inputs on the session record. |
| Update the TS-1 row of the cross-design table to read: EG-1, EG-2, EG-3, EG-4, and EG-9, plus EG-8 for the read filters. | Text | EG-3 owns `gitAccess` and `writableRepositories`, and EG-2 owns `pkg/binding`. |

The `environments` fields, `environmentPolicies`, and the effective-policy parameter are additive and need nothing from that design. `netagent`'s credential cache also needs no change, for the reason under "Enforcement points".

## Specification sections affected

- §2 Goals and Non-Goals: per-customer SSO and SCIM as the operator's responsibility.
- §4.3: the Environment that a child and a derived session use for credential scope.
- §4.9: `environments` on VCS credential pools and credential bindings, and the overlap rule.
- §8.2 and §8.3: the Environment a delegated child takes, and inherited egress inputs.
- §10.2: the identity model paragraph, the broker contract, **Role assignment:**, the IdP role boundary, the `manage_user_lifecycle` row of the permission matrix, and claim names.
- §10.6: scoped objects, session Environment rules, the `identityProvider` description, and **V1 data model accommodations**.
- §11.4: invalidation of a subject with no row, reinstatement, and event ordering.
- §11.7: the `auth.platform_role_dropped` and reinstatement audit events.
- §12.9: classification of the upserted user row.
- §14: plan validation against the Environment.
- §15.1: the Environment body field, the new fields and error codes, `/v1/admin/users`, `reinstate`, the Environment delete guard, and the `environment` filter on evidence reads.
- §16: the dropped-role counter and alert, and the refusal counter.
- §17.6: `auth.rolesClaim` and `auth.groupsClaim`.
- §24: `lenny-ctl` commands for reinstatement and for scoped fields.

The sections that `egress-and-git-design.md` lists change first, and the TS-1 edits apply on top of the text those proposals land.

## Proposals and dependencies

| Label | Proposal | Contents |
| :-- | :-- | :-- |
| TS-1 | Environment scoping | The fields, the formulas, enforcement in the gateway, the decision service, the Token Service, and the mirror, the refusal rule, the delete guard for references, and the read filters. |
| TS-2 | Identity model | The specification statement, the broker contract, the IdP role boundary, claim names, and the §15.1 listing of `/v1/admin/users`. |
| TS-3 | Session Environment context | Derive, replay, and delegation propagation, the active-session delete guard, the transparent create check, and the naming correction. |
| TS-4 | User lifecycle path | Upsert on invalidate, reinstate, ordering, the permission, and redaction of the introspection secret. |
| TS-5 | Delegated team administration | Environment `admin` writes to its own Environment's scoped objects, if decision 7 adopts it. |

Dependencies are stated here and in the other design documents. Proposals from different designs can interleave as long as these hold.

| Proposal | Depends on | Why |
| :-- | :-- | :-- |
| TS-1 | EG-1, EG-2, EG-3, EG-4, EG-9, and TS-3 | The objects it scopes, the resolver, the git inputs, and a dependable session Environment |
| TS-1 (read filters step) | EG-8, and the envelope field in EG-10 | The reads it extends |
| TS-2 | None | It can land at any point, and it should land early because it closes a High defect |
| TS-3 | None | It can land at any point |
| TS-4 | None | TS-2's contract refers to it, and either order works |
| TS-5 | TS-1 | The scoped fields |

No `EG-`, `SO-`, `FE-`, or `OC-` proposal depends on a TS proposal. The relationships in the other direction are these:

- The requests above sit inside EG-1, EG-2, EG-4, EG-9, and EG-10 as hooks, so those proposals do not wait.
- SO-1's open question on whether a broker supplies `security-auditor` has this answer: the role is tenant-scoped, so the broker can convey it in `roles` under this contract, and a platform-managed row overrides it. The row precedence means a `tenant-admin` can remove it, which remains SO-1's question.
- SO-2's guardrails act on TS-1's writes through EG-9's single write path, and they can forbid an Environment's use of an opt-in with no TS change.
- OC-2 routes can appear under `environmentPolicies` with no OC change.

## Documentation

Each proposal carries the documentation for its own surface. Pages describe behaviour with no section numbers, and they land after the specification text they describe. The owner pages are these:

| Page | Change |
| :-- | :-- |
| `operator-guide/identity.md` (new) | Owns the identity model, the broker contract, broker selection criteria, the role boundary, and the lifecycle path. It includes one worked integration under decision 12. |
| `operator-guide/multi-tenancy.md` | **Environments** gains the choice between a tenant and an Environment, and it links to the scoping page. **Multi-Tenant Mode** links to the identity page. |
| "Tenant configuration" in the `egress-and-git-design.md` chapter | Gains team scoping: the fields, how they combine with tenant settings, and the refusal rule. |
| "Git access", "Credential bindings", and "Egress policy" in that chapter | Each states the scoped form of its objects in a few sentences and links to "Tenant configuration". |
| `operator-guide/security.md` | **Client Authentication** shrinks to a summary with corrected keys and a link to the identity page. |
| `client-guide/authentication.md` | Corrects the role claim name, and explains which sessions see which objects by access path. |
| `tutorials/multi-tenant-setup.md` | Replaces Helm keys the chart does not define, and adds an Environment-scoped step. |
| `reference/configuration.md`, the API reference, and the error reference | New values, fields, endpoints, and codes. |
| `operator-guide/lenny-ctl.md` | The new commands. |
| `reference/glossary.md` | Entries for identity broker and scoped object. |

Tier 11 checks the examples against the chart and the schemas.

## Tests

Each proposal turns this list into tests with `// spec:` annotations at the tiers `test-coverage.md` maps. No test exists yet.

### Tier 0: static

- The schemas of the new fields validate, and an empty `environments` list fails validation.
- The chart renders `auth.rolesClaim` and `auth.groupsClaim` to both binaries, and the binaries define the flags.

### Tier 1: unit

- `visible`, `always`, `requestable`, `denied`, and `routes` cover E = ∅, a matching E, a non-matching E, and each formula case.
- A tenant deny beats an Environment allow, and the ceiling bounds an Environment entry.
- Pool selection covers the longest prefix, disjoint lists, and refusal of a tenant-wide overlap and of an overlapping list.
- The IdP boundary removes `platform-admin` and keeps other roles, and a Lenny-issued token is unaffected.
- `observedAt` ordering covers older, equal, and newer calls.
- Child Environment selection covers same-Environment, outbound order, no match, and a parent with E = ∅.

### Tier 2: component

- Admin handlers refuse an unknown Environment name, and refuse deletion while a reference or an active session exists.
- `pkg/binding` returns the Environment from the session row, and it fails closed on a read failure.

### Tier 3: contract

- The admin API representation of each scoped object and of `environmentPolicies`.
- The refusal for an out-of-scope object and for an unknown object are byte-identical apart from the request's own name (TP4).
- The `reinstate` and `invalidate` request and response formats, including `applied`.
- The evidence envelope carries `environment` on each producer's stream.

### Tier 4: integration

- A session create through each explicit path sets E, and through the transparent path leaves it empty.
- Derive, replay, and delegation propagate E, and billing rollup and retention follow.
- A `viewer`, and a member whose Environments do not select the runtime, are refused on the transparent create.
- Edit propagation: removing E from a binding refuses the next I8 request, and adding a host to `denied(E)` refuses the next decision within the policy version rule.
- Invalidating a subject with no row creates the row, ends sessions, and gates a fresh token at session create and at reconnect. Reinstate restores session create. A user under erasure cannot be reinstated.
- `GET rbac-config` does not return the introspection secret.

### Tier 5: end to end on Kind

- Two Environments in one tenant run the first scenario in full: clone, push, a refused fetch, a bound host, and a refused host, through `netagent`, `lenny-egress`, the decision service, and the Token Service.
- A sequentially reused pod carries no credential across a change of Environment.
- A concurrent pool refuses a session in an Environment with denied entries, and it admits one with none.

### Tier 6: end to end on cloud

No behaviour here needs cloud resources.

### Tier 7: load

- Concurrent writes to an object's `environments` and to session creates produce a consistent decision for each session.
- Repeated identical `invalidate` calls during a `reinstate` converge on the state of the newest `observedAt`.
- The resolver adds no measurable decision latency beyond the existing cached policy read, measured against a stated threshold in the proposal.

### Tier 8: chaos

- Postgres loss during a decision or a mint fails closed.
- The issuer's JWKS endpoint goes down during a run, cached keys continue to verify, and an unknown `kid` is refused.
- A partial `full_revoke` reports its failures, and a retry completes it.

### Tier 9: security

- A token from the configured issuer with `roles: [platform-admin]` cannot reach any platform-only endpoint on the gateway, and cannot exchange into that authority at `/v1/oauth/token`.
- A session cannot change its Environment through any request field, and a body `environment` on a path naming another is overridden.
- An agent in E cannot obtain a credential scoped elsewhere through I8, a direct call to the Token Service with its own token, the mirror, or the local git endpoint.
- A compromised-decision test: a stubbed decision service that allows a scoped object does not produce a mint, since the Token Service's own read refuses it.
- A token with an absent, malformed, or unregistered tenant claim keeps the §10.2 refusals, and a nested tenant claim is refused.
- A role holding `manage_user_lifecycle` alone can call `invalidate` and `reinstate` in its own tenant, and nothing else.

### Tier 10: conformance

No runtime adapter contract changes.

### Tier 11: documentation

- Helm keys in identity pages and tutorials exist in `values.yaml`.
- Examples of scoped objects validate against the schemas.

## Decisions

Each of these is open for the owner. The recommendation is the design above.

| # | Decision | Recommendation | Cost of the recommendation | Alternative |
| :-- | :-- | :-- | :-- | :-- |
| 1 | How an object names its Environments | A list on pools and bindings, and an `environmentPolicies` section for host lists and routes | It differs from §10.6's label selectors for runtimes and connectors. Each new Environment needs edits on each shared scoped object. | Selectors on the Environment. These cannot express "absent means tenant-wide", and a label edit silently widens access, with visibility readable only by evaluating every selector. |
| 2 | Outline question: does a session in E see tenant-wide objects? | Yes. It sees the union. | A team cannot exclude a tenant-wide binding, and the tenant admin must scope it instead. | E's objects alone. Every shared object would then need every Environment listed, and R2 would no longer hold. |
| 3 | The Environment of a transparent-path session | None, so it sees unscoped objects alone | Clients must use an Environment path to reach team objects. | Inference when the caller belongs to one Environment. Joining a second team would then change an unrelated client's access silently, and billing and retention would shift. |
| 4 | Which objects are scoped | Pools, bindings, host lists, and routes | A team cannot have its own `.npmrc` or `pip.conf` through client config files. It uses a tenant file or a runtime image. | Writing those files at session binding. That needs a change to `netagent`'s write timing, and it can follow if requested. |
| 5 | Reused pods across Environments | Accept the residual, and document pools without recycling | State left by one team's session can reach another's. Concurrent and service pools get unscoped objects. | An Environment pin beside the tenant pin. It needs claim filtering and pool capacity per Environment, and it inherits the complexity EG-0 addresses for tenants. |
| 6 | Outline question: per-team egress quota | None now, with §10.6's billing rollup for cost | One team can exhaust the tenant's P7 quota. Billing rollup covers tokens and does not cover egress bytes. | A per-Environment bucket in the decision service, which is additive later. |
| 7 | Team self-service | Defer TS-5. A `tenant-admin` writes scoped objects. | Central IT handles each team's requests. §10.2's statement that an Environment `admin` gains management within the Environment remains unimplemented: `RoleAdmin` has no use outside `pkg/environment`, and all Environment routes need `manage_environments`. | TS-5 now, with a per-Environment bound set by the tenant admin. That adds a delegated authority over hosts and secrets, which needs a security review of its own. |
| 8 | Identity model | Option A, a broker. Revisit when a customer refuses a shared issuer in a security review, when several operators need one realm per customer, or when evidence shows the tenant claim risk dominating. The fallback is the issuer-per-tenant intermediate. | The tenant boundary rests on the broker's correctness. All tenants share its outage. The operator runs a broker and a worker. The evidence for the need is vendor documentation, with no first-hand complaint found, so priority is uncertain. | Option B, at the costs in the comparison table. |
| 9 | An IdP token asserting `platform-admin` | Remove the role, continue, audit, and alert | A mapping error stays quiet to the caller, who learns only from missing authority. An operator using claim-based staff roles must add rows. | Refuse the whole token, which is louder but locks out a legitimate user of the same token. Or a Helm opt-in that keeps today's behaviour, which leaves TP5 false by configuration. |
| 10 | Claim names | Keep `roles` and `groups` as defaults, remove `lenny_role` from the specification, and add `auth.rolesClaim` and `auth.groupsClaim` | More Helm values and a code path reading a named claim. A broker could map these names alone. | No new values, which is smaller, and which assumes every broker can emit those exact names as arrays. |
| 11 | What a directory deactivation does | Document `full_revoke` as the recommended worker action, and leave the choice to the worker | An operator can select a weaker mode by mistake. | Fix it in Lenny with a separate deprovision endpoint, which removes a choice some operators want. |
| 12 | Reference broker | Name none as required. Publish selection criteria and one tested integration on Keycloak, which runs in test infrastructure. | Keycloak's per-realm SCIM needs a separate path, so the integration covers SSO and a worker fed by a stand-in. | A hosted vendor reference, which cannot run in the suite without an account and which reads as an endorsement. |
| 13 | User rows at login | No. The upsert happens at invalidation. | A customer listing users sees only those with rows. | A row at first session create, which adds a write and personal data to a hot path. |

## Evidence for the need

- A Claude Code issue asks to "[let users declare additional allowlisted domains at the project or session level](https://github.com/anthropics/claude-code/issues/52982)". It concerns user-owned domains in a managed sandbox, so it relates only in part to team scoping. It was closed as not planned.
- GitHub lets organization admins [manage the Copilot agent firewall across repositories and control whether repository admins add entries](https://github.blog/changelog/2026-04-03-organization-firewall-settings-for-copilot-cloud-agent/) (vendor source). This is the closest analogue found for a layer below the organization.
- For SSO and SCIM, the evidence is vendor documentation, as listed under "What comparable systems do". No first-hand complaint was found.
