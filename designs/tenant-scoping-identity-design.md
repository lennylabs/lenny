# Tenant scoping and identity

Status: design for owner review, revision 18, 2026-11-03. Revision 18 applies the stop check on revision 17, under "Review of revision 17". Revision 17 applied the stop check on revision 16, and revision 16 applied a second review of the identity design and a first review of cross-Environment sharing and delegation limits, and the owner's decision 3. No review has read revision 18's text. Revision 5 added scoping of user credential types, and revisions 6 to 12 correct it after review. Revision 13 applies the owner's decision 8, which adopts per-tenant OIDC and a SCIM server beside the broker default, and revision 15 applies decisions 14 and 26. It reads `egress-and-git-design.md` revision 55, `user-credential-delivery-design.md` revision 14, `user-git-identity-design.md` revision 17, and `security-oversight-design.md` revision 17. Nothing here is built or staged, and no test or spike was run for it. Statements about the tree were checked by reading the code and specification. Proposals carry the label TS-n. The reviews of revisions up to 12 ended with a stop check that traced the last round's changes and found no Major or higher issue, and that check covers none of the identity design. No production component, identity provider, or directory has been exercised.

## Problem

A company runs Lenny as a multi-tenant SaaS. One of its customers has several teams and wants each team to have its own egress and git configuration, with SSO and user lifecycle coming from the customer's own directory.

The following gaps stand in the way:

- In `egress-and-git-design.md`, egress policy, VCS credential pools, and credential bindings are tenant-wide. Any client in a tenant can name any `onRequest` binding or allowlisted repository, and any agent in a tenant can fetch any allowlisted repository at run time. §10.6 Environments select runtimes and connectors only.
- Lenny trusts one OIDC issuer per deployment and has no SCIM. A SaaS operator therefore needs an identity broker for per-customer SSO, which the specification neither says nor bounds with a contract, and a customer cannot bring its own issuer.

## Scope

This design covers Environment scoping of the objects in `egress-and-git-design.md`, the identity model (a broker by default, or a customer's issuer per tenant), a SCIM server, and the user lifecycle path into Lenny. Migrating a tenant between issuers is out of scope. It treats those egress objects as fixed inputs. `security-oversight-design.md` owns security roles.

## Requirements

- R1. A tenant admin can give an Environment its own VCS credential pools, credential bindings, host lists, and routes.
- R2. An object with no Environment scope keeps the meaning `egress-and-git-design.md` gives it.
- R3. Scoping holds at run time, against an agent acting inside a session, as well as at session create.
- R4. A narrowing that Lenny cannot enforce on a given pod is refused rather than ignored.
- R5. The specification states the identity model: the deployment issuer by default, a customer issuer per tenant, and what stays with an operator.
- R6. A customer's directory can end a departing user's access to Lenny, including running sessions, within a bound the directory and the operator control, through SCIM or a sync worker.
- R7. No customer-controlled input, including a customer's issuer, can produce authority over another tenant or any platform-wide authority.
- R8. One deployment holds tenants on the deployment issuer and tenants on customer issuers at once, and a single-company deployment is unaffected.

## Security properties

- TP1. A session in Environment E can use a scoped object only when the object lists E. For credentials, the Token Service and `lenny-egress-authz` each hold this independently, from their own reads of the session row and the object. The mirror's own fetches hold it through the pool and the cause each request names.
- TP2. The gateway sets a session's Environment at create and it never changes. A root session's Environment is checked against the caller's membership. A delegated child takes its parent's, or under a declared sharing path the target's. Nothing in a pod can set it. A runtime cannot create a root session, because the intra-pod tool surface omits those tools.
- TP3. A pod with no per-session policy applies unscoped objects alone, for every session it serves. Omitting an Environment never widens access: a caller cannot leave an Environment's denies or routes behind by using the transparent path.
- TP4. A caller receives the same refusal for an object scoped to another Environment and for an object that does not exist, so team object names do not leak across teams.
- TP5. A role claim from an IdP cannot confer `platform-admin`. A `platform-admin` row can exist only in one configured operator tenant, so a subject in any other tenant cannot hold the role. Whichever issuer authenticates the operator tenant can assert a staff subject, so that issuer holds platform authority: with the deployment issuer in that position, a broker compromise reaches every tenant.
- TP6. Environment scoping is a policy boundary inside a tenant. It carries none of the pod pinning or row-level isolation that P4 and §12 give the tenant boundary. "Residual risks" states what follows.
- TP7. A delegated child runs in its parent's Environment unless a sharing path applies, so a chain of delegations reaches nothing its root's Environment does not offer. Under a sharing path, the child runs under the target Environment's policy, the caller's denies and routes do not follow it, and it and its descendants cannot cross again. A runtime can name no other session as a delegation parent.
- TP8. A customer source's tenant comes from the record its verified issuer selects, and never from a token claim. The deployment issuer cannot speak for a tenant configured for customer sources.
- TP9. An unverified `iss` selects a verifier and nothing else, and an unknown one causes no fetch from that issuer.
- TP11. Every browser login goes through the same resolver as a bearer, and an authorization code goes only to the source whose callback received it.
- TP12. A subject whose source is not active is refused on every verifier path, including with a Lenny-issued token.
- TP10. A customer source's subjects and groups are namespaced, so two issuers in a deployment or a tenant cannot collide.

## Current state

Each row was checked against the named source.

| Claim | Status | Source |
| :-- | :-- | :-- |
| `auth.oidc.issuerUrl` and `auth.tenantIdClaim` are single Helm values | True. `auth.oidc.clientId` is also single. Each renders to one flag on the gateway and the Token Service. | `charts/lenny/values.yaml`, `gateway-deployment.yaml`, `token-service-deployment.yaml` |
| One IdP verifier exists per process | True. `buildIdPVerifier` and `buildCallerVerifier` build one `OIDCVerifier`, which `TenantClaimVerifier` wraps. | `cmd/lenny-gateway/token_signing.go`, `cmd/lenny-token-service/caller_verifier.go` |
| A per-tenant identity provider exists | Partly. §10.6 **Tenant RBAC Config** lists `identityProvider (OIDC)`. In code, that record holds only `type` (`oidc` or empty) and the RFC 7662 introspection settings. It cannot name an issuer. | `pkg/gateway/externalapi/admin/rbac_config.go` |
| The specification has SCIM | False. No match in `spec/`, `pkg/`, `cmd/`, or `charts/`. | Search |
| The gateway can reach an external issuer | Yes under `allow-gateway-egress-llm-upstream`, which excludes private ranges and which a CIDR list can narrow. | `gateway-llm-upstream-egress.yaml`, §13.2.4 |
| The Token Service can reach an external issuer | Only through the KMS row. `allow-token-service` renders `kms.endpointCIDR` on TCP 443 unconditionally. Its default `0.0.0.0/0` opens port 443 with no excepts under the local provider, and a cloud KMS narrows it, which removes the path. | `system-network-policies.yaml`, `_helpers.tpl`, `values.yaml` |
| The playground login uses the verifier chain | False. `buildPlaygroundOIDC` passes the raw `OIDCVerifier`, and `extractSubject` reads the tenant and user from its claims. The code and verifier are posted before the ID token is checked, on one callback path. | `cmd/lenny-gateway/playground_oidc.go`, `pkg/gateway/mcpfabric/playground/oidc.go` |
| `lenny-ctl` logs in | False. It takes `--token` or `LENNY_API_TOKEN`. | §24, `pkg/ctlcli` |
| Groups reach Lenny through tokens or introspection | True. `groups` is a fixed claim name, and introspection replaces the set for a tenant that turns it on. | `pkg/auth/jwt/jwt.go`, `pkg/gateway/middleware/auth/auth.go` |
| IdP tokens can assert roles | True, including `platform-admin`. See "Defects found while verifying". | `Claims.Roles`, `normalizeUserBearer`, §10.2 **Role assignment:** |
| A session records its Environment | Yes for a create that names one, in `sessions.environment`. Absent for a transparent-path create. No code sets it on a delegated child, a derived session, or a replayed session. | `create.go`, `derive.go`, `replay.go`, `delegation/service.go` |
| A precedent for Environment-scoped credentials exists | Yes. User credentials and connector tokens carry an `environment` column in which the empty string means the no-environment scope (§4.3 and migration 0059). | `credentialstore.go` |

## TS-1: Environment scoping

### A session's Environment

TS-1 relies on TS-3, which makes `sessions.environment` dependable. A session has a value E when its create named an Environment through `/v1/environments/{name}/sessions`, `/mcp/environments/{name}`, a scoped model namespace, or the `environment` body field. All of these already run the explicit admission check (membership, at least `creator`, and the runtime selector). A transparent-path session has no E, and the formulas below write that as E = ∅.

A new session with E = ∅ (a root create, a derive, a replay, or a delegated child) is refused with `422 ENVIRONMENT_REQUIRED` when any Environment in the tenant whose selector admits the runtime has `denied` or route entries. The check reads the tenant's Environments and takes no input from the caller's groups, so a token with no `groups` claim, under either `noEnvironmentPolicy` value, cannot shed a team's policy. A resume is exempt, so a deny added later does not strand a running session. A caller who belongs to no such Environment has no path to that runtime, including an administrator who is not a member, and a `tenant-admin` resolves that by adding the member or by giving the team its own runtime. See decision 3.

Experiment routing and a late `PreRoute` interceptor can rewrite a session's runtime and pool after admission (`routeExperiment` and `preRouteAfterExperiment` in `create.go`, and `ApplyExperimentRouting` in `buildChildSession`). The runtime-dependent checks in this section therefore run again on the final row: the selector for E, `ENVIRONMENT_REQUIRED`, and the rule for pods with no per-session policy. A rewrite that fails one refuses the create or the delegation, as `VARIANT_ISOLATION_UNAVAILABLE` does. A pool fixed only at claim is checked at claim, before bind.

### What is scoped

Scoping applies to objects the platform evaluates per session.

| Object | Scoped | Reason |
| :-- | :-- | :-- |
| VCS credential pool | Yes, through `environments` | Selected per repository at mint time |
| Credential binding | Yes, through `environments` | Resolved per session at I8 |
| User credential type and link provider | Yes, through `environments` | Resolved per session at I8, under `user-credential-delivery-design.md`. The binding that names one is scoped as above, and a session needs both visible. |
| A user's token (a `registered` row or a link) | The `registered` row has an `environment` column, and §4.3's empty value means no Environment. A link has none, and it is keyed on the link provider's name. | The Token Service takes the session's E, then the no-Environment row. A link records a fact about a person on a provider, so a member of two teams links once. |
| `requestRules` and presets | No | They are part of a binding, which is scoped. A team that needs other rules has its own binding on that host, and the one-binding-per-host rule applies per session. |
| Always-allowed, session-requestable, and denied hosts | Yes, through `environmentPolicies` | Read per decision |
| Egress routes and git routes | Yes, through `environmentPolicies` | Read per decision. A route can name a connector once OC-1 lands, with no further change. A token trust record covers every such route on its host, across every Environment, as `user-credential-delivery-design.md`, "Route value", states, so one team's scoped route, a move between Environments, or a reorder on a token's host makes that token unusable for all teams, and an asserted route uses `routes(E)` for the session's E. The covered host set reads every git route and VCS pool of the tenant, unfiltered by Environment, so a session in E2 on a host only E1 names gets a refused push rather than a tunnel, as `egress-and-git-design.md`, "Git on covered hosts", states. |
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
- A tenant warm-up addition or a `gitRepository` warm fill cannot name a repository whose only covering pool is scoped, because a pod with no session has no E.
- Two pools can hold the same repository prefix only when their `environments` lists are disjoint, and neither may be tenant-wide. Selection takes the longest prefix among the pools visible to E, so exactly one pool answers for any repository and Environment. This lets two teams use separate GitHub Apps on one organization.
- A credential binding's `hosts` must lie within the allow set of each Environment the binding lists, that is `always(E) ∪ requestable(E)` for each E, or within the tenant's set for a tenant-wide binding. TS-1 extends the "each host must lie within the tenant's effective allow set" validator of EG-4 in this way, or the `payments-stripe` example above would fail.
- A mirror prefetch request names a pool whenever more than one pool covers the repository, and only a tenant admin can name a scoped pool.
- A shadow policy's candidate carries its own `environmentPolicies`, and each decision evaluates the candidate with the session's E. `environments` lists sit on objects and stay outside the candidate.
- A write that adds `denied` or route entries to an Environment answers with a `Warning` header and a count when live sessions in it run on pods with no per-session policy, because those sessions cannot adopt the entries.
- A write that adds an Environment's first `denied` or route entry answers with a `Warning` header listing the runtimes its selector admits, because transparent creates on those runtimes will now answer `ENVIRONMENT_REQUIRED`. A tenant admin learns this before those clients break. A selector or runtime label change that extends such an Environment to new runtimes answers with the same `Warning`.

The effective-policy read gains an optional `environment` parameter and reports `environment` as a deciding layer. `GET /v1/egress/decisions` and the evidence export gain an `environment` filter. Both wait for EG-8.

### Data model

- VCS pool and binding rows gain a nullable `environments` text array. NULL means tenant-wide.
- The tenant egress policy document gains `environmentPolicies`, keyed by Environment name.
- Neither is a foreign key, following the reasoning in migration 0059. The write check and the delete guard hold the reference.
- The existing `sessions.environment` column holds E.
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
| Pod with concurrent sessions | `ceiling ∩ always(∅) − denied(∅)` | Environment entries do not apply, for the mechanical reason the original formula states. See **Pods with no per-session policy.** |
| Delegated child in the parent's Environment | The one-session formula, or the `restricted` row, with the parent's E, and the parent's `egress.allowedHosts` and named bindings as its inputs | The rule in `egress-and-git-design.md`. |
| Delegated child under a sharing path to T | The one-session formula, or the `restricted` row, with E = T, and `session allowed` = `always(T)` | No input comes from the parent, and no term comes from the caller's Environment. `denied(T)` includes the tenant's list. |

Consequent rules:

- **Bound.** The pool ceiling bounds every Environment entry. A tenant deny applies in every Environment, so an Environment cannot re-allow a host the tenant denies.
- **Repositories.** Readable repositories are the session's `gitClone` sources, the allowlists of `visible(VCS pools, E)`, and public `sharedAssets` entries. Writable repositories must lie within `visible(VCS pools, E)` at bind and at each mint.
- **Bindings.** A session attaches `always` bindings from `visible(bindings, E)`, and it can name `onRequest` bindings from the same set.
- **User credentials.** A `userCredential` binding needs its type visible too, as `visible(types, E)`. It takes `onRequest`, or `poolAttach` under `user-git-identity-design.md`, "The attach rule", whose trigger is a declared writable repository under such a pool in `visible(VCS pools, E)`. The pin from a `required` pool is outside `visible`, because it reads every VCS pool of the tenant. The pod refusal in `user-credential-delivery-design.md` covers E = ∅ on a concurrent or service-mode pod, because those pods refuse such a binding outright.
- **Pods with no per-session policy.** A concurrent-session pool or a service-mode pool evaluates with E = ∅ for every session on the pod, including at the Token Service and in the readable and writable sets. "Git on covered hosts" in `egress-and-git-design.md` makes those sets the union over the pod's sessions, because `netagent` cannot attribute a request to a slot, so a scoped repository on such a pod would be open to every slot. The rules follow from this:
  - A plan that names an object visible to E alone (for example a repository whose only covering pool is scoped) is refused with `422 ENVIRONMENT_POLICY_NOT_ENFORCEABLE`.
  - A session whose E has non-empty `denied`, `egressRoutes`, or `gitRoutes` is refused with the same code (R4). The check runs at create, derive, replay, resume, delegated child placement, and each service-mode request that names an Environment, on the final runtime and pool.
  - Otherwise the session proceeds with unscoped objects. Entries of E that add access do not apply, which narrows the session, and the effective-policy read reports that.

### Enforcement points

The session create check alone is insufficient. An agent can run `git fetch` at any time against any repository on the tenant's allowlist, and `always` bindings attach with no plan entry, so most of the enforcement happens after create.

| Point | Component | Check |
| :-- | :-- | :-- |
| Session create, derive, replay, resume, and delegation | Gateway | Named bindings, `gitClone` sources, `writableRepositories`, and `egress.allowedHosts` lie within the sets above for E, and the refusal rules for transparent creates and for pods with no per-session policy apply. |
| Ref resolution | Gateway through I3 | The decision service reads E from the session the request names. No new header reaches a level 2 proxy. |
| CONNECT | `lenny-egress-authz` | The formula for the pod's case, with E from `pkg/binding`. |
| Mint or secret on I8 | Token Service, then I3 | Each component reads E and the object, and both must agree (TP1). Nothing is minted on refusal. |
| Serving from the mirror | Mirror through I3 | The resolver selects the pool for the repository and E, and readable repositories for E are checked on every request. A store's identity in `egress-and-git-design.md` includes the credential, so same-prefix pools in different Environments use different stores. |
| Filling the mirror | Mirror to the Token Service on I8 | The request names the pool and its cause: the calling pod's UID, the session for a gateway `ls-refs`, or the prefetch record. The Token Service reads that pod's binding, that session, or that record itself, and refuses a pool not visible to the resulting E. Warm fills reach unscoped pools alone. |
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
| A delegation target with neither the parent's selector nor a live two-sided sharing path | `TARGET_NOT_IN_SCOPE` |
| A child reached by crossing, or its descendant, tries to cross | `TARGET_NOT_IN_SCOPE`, with the audit reason `second_crossing` |
| A resume meets a deny added to E after the session began on a pod with no per-session policy | `ENVIRONMENT_POLICY_NOT_ENFORCEABLE`, and the session stays suspended until its owner ends it or the tenant admin changes the policy. |
| An Environment vanishes under a live session through a store-level race | `visible` finds no entries for E, which narrows the session to unscoped objects. |

### Who can do what

| Action | `platform-admin` | `tenant-admin` | Environment `admin` | Environment `creator` | `user` with no role in E |
| :-- | :-- | :-- | :-- | :-- | :-- |
| Set `environments` or `environmentPolicies` | Yes | Yes, own tenant | No, unless TS-5 lands | No | No |
| Read the scoped objects | Yes | Yes | No admin read | No | No |
| Create a session in E | No, unless a member | No, unless a member | Yes | Yes | No |
| Name a scoped object in a plan | Only in a session in E | Only in a session in E | Yes, in E | Yes, in E | No |
| Set `dangerouslyAllowCrossEnvironmentSharing` | Yes | Yes, own tenant, subject to SO-2 | No | No | No |
| Add or remove a `dangerouslyShare` entry | Yes | Yes, own tenant, subject to SO-2, as does a custom role holding `manage_environments` | No | No | No |

Writes pass through EG-9's single write path, so they carry its audit event and any SO-2 guardrail. A `tenant-admin` remains able to scope any object to any Environment and add itself as a member, which matches §10.2's statement of tenant authority.

### Why not separate tenants

A customer can give each team its own tenant today. That yields tenant pinning, row-level isolation, and separate keys, which Environments do not provide. It also means no shared policy, no shared admin, no combined billing rollup, and no delegation between teams, since Lenny has no grouping above a tenant. Teams needing an isolation boundary should be tenants, and teams needing separate configuration under one administration should be Environments. The documentation states this choice.

### Does TS-1 need a hook

The outline said no, on the basis that TS-1 adds an optional field and a check at create. That holds for the fields, which are additive. It does not hold overall, because enforcement is at run time in components EG-2 ships, evidence records are written from day one, and EG-1 fixes the prefix rule. Revision 38 of `egress-and-git-design.md` carries those hooks. The mirror's I8 request needs one more, which "Changes requested of egress-and-git-design.md" states.

## TS-3: Session Environment context and cross-Environment sharing

TS-1 cannot rest on the current propagation. TS-3 lands first and depends on nothing in `egress-and-git-design.md`, apart from the evidence field step the dependency table names.

- **Derive and replay.** The new session takes the source's Environment, and the explicit admission check runs against it. A caller who has left the Environment gets `403`.
- **Delegated child's Environment.** The child takes the parent's E, set on the row `buildChildSession` writes, when `runtimeSelector` of the parent's E admits the final runtime. Otherwise it needs a sharing path under "Cross-Environment sharing", and with none it answers `TARGET_NOT_IN_SCOPE`.
- **The gate reads the session.** `WithSessionPrincipal` gives an in-pod caller a principal with no groups, and `runtimeAuthorizedForCaller` and `crossEnvReachable` resolve Environments from that principal. A session whose owner belongs through a group therefore has no Environments in the gate. Under `deny-all` its delegations answer `TARGET_NOT_IN_SCOPE`, including within its own Environment, and under `allow-all` every runtime is admitted, regardless of selectors. Tests set `Groups` on the principal directly, which hides this. TS-3 gates a session with E on the session's E and the live declarations, with no reference to the principal or to the owner's membership. A session with E = ∅ keeps the existing check, adds `ENVIRONMENT_REQUIRED`, and cannot cross.
- **The parent is the calling session.** `lenny/delegate_task` reads `parentSessionId` and `lenny/await_children` reads `sessionId` from their arguments, and `lookupCallerSession` admits any session of the same owner. A runtime can therefore name another session as parent and escape its limit, its Environment, and its crossing mark, or read another session's child results. When the principal carries `SessionID`, as `WithSessionPrincipal` sets it, TS-3 uses that session on both tools and answers `403 PARENT_SESSION_MISMATCH` to a different value, as `callerSessionID` does for `lenny/get_task_tree`. A client at the gateway edge still names its parent, and the rules read that session's row. F-10.2.29 is separate: a compromised adapter can present another session's ID, and this rule does not stop that. TS-3 does not depend on F-10.2.29, and its tier 9 tests cover a runtime's tool calls and no forged adapter call.
- **The intra-pod tool surface.** `platformtools.Bridge` forwards any registered tool, so a runtime can call `lenny/create_session` or `lenny/create_and_start_session` and start a new root with no limit, another Environment, and no crossing mark. TS-3 allow-lists the Bridge's list and call to §9.1's **Platform MCP Server Tools** table, which omits those tools, and any other name answers `ErrPlatformToolNotFound`. The list also holds `lenny/vcs_token` until EG-3 removes that tool, because `git-credential-lenny` calls it over the platform MCP socket, so TS-3 keeps depending on nothing. The gateway edge is unchanged. A later proposal for F-10.2.32 must not reopen this by exempting in-pod principals.
- **Fail closed.** The session gate refuses on every read error, where `runtimeAuthorizedForCaller` returns true on a `Runtimes.Get` error today. `lenny/discover_agents` answers from the session's E and its live sharing paths, in place of the principal's groups, and it omits sharing paths from a session under `lockEgressToHosts`.
- **Effects on existing features.** Billing rollup, retention tier, default delegation policy, §4.3 credential scope, and connectors read the same field, so a child and a derived session follow the session's Environment in each.
- **Deletion guard.** §15.1 says deletion of an Environment is blocked while sessions are active in it. `handleDeleteEnvironment` and the store's `Delete` contain no such check. TS-3 adds it.
- **Naming.** §10.6 **V1 data model accommodations** describes `environmentId` on session creation as a no-op stub. The code reads a body field named `environment` and enforces it, and `?environmentId=` on `GET /v1/runtimes` narrows the list. The specification changes to match the code.
- **Transparent create.** `requireEnvironmentAdmission` admits any member of any Environment on the transparent path, with no role check or selector check. A `viewer` can therefore create a session on a runtime the transparent filter hides from it. No other check was found in `pkg/gateway/sessionserver`. TS-3 requires at least `creator` in an Environment whose selector admits the runtime. TS-1 adds the `ENVIRONMENT_REQUIRED` refusal to the same check.
- **Groups on Lenny-issued tokens.** `mintToken` writes no `groups` claim, so a token from `/v1/oauth/token` cannot satisfy an `oidc-group` member, and §13.3's claim structure does not list one. The playground mint already copies the subject's groups. TS-3 copies them on exchange in the same way. No deny or route depends on this, because `ENVIRONMENT_REQUIRED` reads no groups.

### Client delegation limits (decision 26)

The owner settled this on 2026-11-03, in the words "yes, add it with maxDepth: 0". `delegationLease.maxDepth: 0` at session creation means this session cannot delegate. It needs no gate, because it only narrows. TS-3 carries it, because it reworks the same gate, and it closes F-8.2.34 apart from `delegationPolicyRef`.

**Today.** The field has no effect. `DelegationLeaseRequest` is validated, stored, and echoed, a root row's `DelegationLease` stays nil, and `mcptools_register.go` leaves `Request.MaxDepth` unset, so `checkDelegationDepth` resolves the Helm fallback on every hop. `sessionstore.DelegationLease` has no `MaxDepth` field, which breaks §8.2's "never re-resolved against live Helm values mid-tree". Zero has four meanings: "no further delegation" on `DelegationLeaseRequest`, unset in `ResolveMaxDepth`, "inherit" on `Request.MaxDepth`, and no limit in `ValidateChildSlice`.

**One reading of `maxDepth`.** Absent means this layer sets nothing, and 0 means none.

- `sessionstore.DelegationLease` gains `MaxDepth` as a pointer, written on every root and child, so no later hop reads Helm. `IsZero` gains a clause for it, with a test, because a stored `{maxDepth: 0}` would otherwise read back as NULL and fail open.
- `MaxDepthInputs.ExplicitClient` becomes a pointer. `Request.MaxDepth` goes, because `delegate_task` has no such input and a child takes its parent's stored value.
- `CheckDepth` answers refusal for 0, in place of `ErrUnresolvedMaxDepth`.
- A nil stored value refuses delegation, since nothing is deployed and every creator writes a value.
- Depth comes from `parent.DelegationDepth`, fixed at admission. `buildLineage`'s `walkCap` reads Helm today, so a lowered value would make depth stick and the stored limit unreachable. The lineage walk gets its own bound.

**`maxChildrenTotal`.** A client's `maxChildrenTotal: 0` becomes a stored `maxDepth: 0` at create, so the two fields agree. The integer, the slice arithmetic, the Lua, and `Dimensions` stay as they are, and 0 in the stored integer keeps meaning no limit. A positive value goes on the root's lease as a per-parent cap, because the counter key is `children_total:<parent>` and a child with no slice stores none, so it does not bound a tree. The documentation says so. `maxDepth` already cannot be extended under §8.6, and the code requests extension of `Tokens` alone, so no extension rule is needed.

**Resolution.** §8.2's "first value present" read as the client's value winning. It becomes a minimum, so a client can only narrow:

- `base` is the minimum of the values present among the preset entry, the Runtime's default lease, and the `DelegationPolicy` ceiling. When none is present, `base` is `gateway.delegation.defaultMaxDepth`.
- The effective value is the smaller of the client's value and `base`, and it is `base` when the client sets none.
- A child stores its parent's value, and the stored value is compared with depth from the root, so 0 refuses every child and no child can widen it.
- Only a client can supply 0. The preset, Runtime, policy, and Helm layers stay at 1 or more, and `gateway.delegation.defaultMaxDepth` must be at least 1. It is not a chart value today and no template passes it to the gateway (F-8.3.12), so TS-3 defines it in `values.yaml` and the schema with a minimum of 1 and renders it to `--delegation-default-max-depth`.

Only the Helm layer is wired today, because `adm.policyCeiling` is 0 and the code comment calls presets and Runtime defaults future work. TS-3 wires the client layer and applies the minimum over whichever layers exist. A preset above a Runtime default used to win under the written order and now loses, though neither layer exists in code.

**Which fields the wiring reaches.** `maxDepth` and `maxChildrenTotal`. `delegationPolicyRef` stays as it is today: it is accepted, stored, and echoed, and it has no effect, because `detectCycle` reads the target's value and `stampLeasePolicy` overwrites the child's. §27.4's playground sets it, and TS-3 refuses and removes nothing there. That part of F-8.2.34 stays open. The other §8.2 lease fields, such as `maxTokenBudget`, and §8.3's preset forms stay absent from the request.

**Every root creator.** `POST /v1/sessions`, `POST /v1/sessions/start` and `lenny/create_and_start_session`, whose request gains the field, `BindSingleShot`, derive, and replay each write a resolved value. A derive or replay starts a new root at depth 0, so it stores the source's value minus the source's depth, with a floor of 0, and it has no field to narrow it. A resume keeps its row. Tree recovery brings a node back on its stored lease record, as §8.10 requires, so it cannot re-issue a wider one.

**Enforcement.** The gateway refuses `lenny/delegate_task` for that session with `403 DELEGATION_DISABLED`, category `POLICY`, not retryable. No such code exists in `spec/`, `pkg/`, `tests/`, or `docs/`. The check runs at the top of the handler and again in `validateDelegation`, and it reads the calling session's stored lease, so a runtime's direct call gets the same answer.

**Hiding the tool.** Hiding is a convenience, and the refusal is the control.

| Surface | Can it be hidden? |
| :-- | :-- |
| Intra-pod `@lenny-platform-mcp` `tools/list` with one session | Yes. `ListPlatformTools` already takes the session identifier, and `Bridge.ListPlatformTools` ignores it today, so it drops `lenny/delegate_task` there. `lenny/discover_agents` answers with no targets. |
| Intra-pod list on a process with no session or several | No. `platformToolProvider.List` refuses it, as it does today. |
| A runtime that cached the list | No. A recycled or SDK-warm process can hold an earlier list, and a direct call gets the refusal. |
| Gateway-edge `/mcp` `tools/list` | No. `Catalog()` is one shared list, and a connection names no parent session until a call does. |

### Cross-Environment sharing (decision 14)

The owner settled decision 14 on 2026-11-03: keep cross-Environment delegation, under option B with guardrails, in the words "go with B with these guardrails". TS-3 carries it, because it needs TS-3's session-based gate and child propagation, which also close F-10.6.15 and F-10.6.16.

**Model.** A crossing child runs under the target Environment's policy, unchanged: the target's allow list, denies, routes, bindings, pools, and repositories. A shared runtime has the same egress and credentials for every caller. Nothing in those sets is intersected with the caller's. A child still takes what binds any child from its parent: isolation monotonicity, `treeVisibility`, `contentPolicy`, and its stored `maxDepth`. What moves across is the `TaskSpec`'s `input` and exported files, `lenny/send_message` between parent and child, and between siblings under that scope, and `runtimeRef` in `lenny/get_task_tree`. Its results return to the caller.

**Opt-ins.** Sharing is off for each tenant by default, and it needs both of these:

```yaml
tenant:
  dangerouslyAllowCrossEnvironmentSharing: false   # default

environment:                     # web
  dangerouslyShare:
    outbound:
      - targetEnvironment: platform
        runtimes: { matchLabels: { shared: "true" } }
environment:                     # platform
  dangerouslyShare:
    inbound:
      - sourceEnvironment: web
        runtimes: { matchLabels: { shared: "true" } }
```

`dangerouslyShare` replaces `crossEnvironmentDelegation`, in the same two-sided form. `runtimes` must be a non-empty selector on both sides, because `Selector.Matches` with no clauses admits every runtime. `ENVIRONMENT_NOT_FOUND` and `RESOURCE_HAS_DEPENDENTS` cover these names, so a recreated Environment inherits no path. With the tenant setting off or forbidden, a write that adds an entry answers `422 CROSS_ENVIRONMENT_SHARING_DISABLED`, and a removal always succeeds. Existing declarations stay stored and have no effect, and they apply again if the setting returns, which posture lists. A `tenant-admin` writes both sides, so the two-sided form records each Environment's intent and does not separate two people. No name in the tree conflicts with these.

**The check.** On each `delegate_task` whose final runtime the parent's selector does not admit, the gate requires all of these, reading them live as §10.6 **Cross-environment check evaluation semantics** states:

1. The session's stored lease permits delegation. A session under `maxDepth: 0` can never cross, whatever the Environments declare, and it gets `DELEGATION_DISABLED` first.
2. The session is not under a lock, its own or one inherited from an ancestor, because that lock limits every descendant to its hosts and a crossing child runs under T's list. A child records the lock as `egressLockHosts`, with the inherited inputs the delegated child rule stores on its session row, and each descendant, derived session, and replayed session copies it, so a session holding no binding is still under it. UC-5 owns the field. Otherwise this answers `TARGET_NOT_IN_SCOPE`.
3. The tenant setting is on and not suspended.
4. The parent's E is not ∅, and its `crossed_from_environment` is null.
5. An outbound entry in E's list names a target T and its `runtimes` admit the runtime. The first such entry in list order that also passes the next two checks selects T, so the source Environment controls the order.
6. T's `runtimeSelector` admits the runtime.
7. An inbound entry in T's list names E, and its `runtimes` admit the runtime.

For a session with a crossing mark, every delegation, including one inside T, also requires that the tenant setting is on and not suspended, that the marked Environment's outbound list still names T, and that T's inbound list still names the marked Environment. Otherwise it answers `TARGET_NOT_IN_SCOPE`, so a forbid or a removed declaration stops a crossed subtree from delegating further. Sessions already running continue.

`CrossEnvironmentReachable` returns a boolean today, and the child needs T, so it must return T. Its wildcard match on `*` goes. The check runs again on the final row after experiment routing, and a rewrite that leaves the path, changes T, or changes whether the hop crosses refuses the delegation, because §8.3's `inherit` check ran on the earlier answer. An in-flight child is not revoked when a declaration or the setting changes.

**Guardrails.**

1. **Tenant denies.** The tenant's `denied` list applies in every Environment, because `denied(T)` includes it. `environmentPolicies[E].denied` of the caller does not apply to the child.
2. **User authorization.** The child keeps its parent's user (§8.2 **Child identity.**), so a mint on a VCS pool in T with `userAuthorization: required` checks Alice's own access to that repository, and TS-4's invalidation levels stop it. This covers repository reads and writes on `required` pools. It does not cover a pool with `userAuthorization: off`, where the App's token reaches T's allowlist for any caller, a stored API key under `inject`, a tenant-wide or scoped binding with no user, `federation` or `metadata` delivery, a host on T's allow list, connectors, or LLM credentials. A `userCredential` binding crosses only when the delegation lease names it and T can see the binding and its type, and `caller_type` is copied as `user-credential-delivery-design.md` states.
3. **No second crossing.** A child stores its parent's `maxDepth` across a crossing, so the target's policy does not widen it. The child's row records `crossed_from_environment`, a new nullable column holding the caller's E. A session with a value cannot cross, which closes transitive chains. A same-Environment descendant stays in T, copies the value, inherits its parent's inputs under the ordinary rule, and can delegate to runtimes T's selector admits. It cannot cross either. A derived or replayed session copies the value and meets the explicit admission check for T.
4. **Named Environments only.** `sourceEnvironment: "*"` and any wildcard source or target are refused at write with `422 VALIDATION_ERROR`. The sharing side names who it trusts. This differs from §10.6's example, which shows `sourceEnvironment: "*"`. `runtimes` keeps its label selector.
5. **Security oversight.** A `security-admin` can forbid the tenant setting through SO-2's `crossEnvironmentSharing: forbid`. A forbid refuses additions and suspends existing paths, so the setting reads as off at the gate. Turning the setting on, and adding a `dangerouslyShare` entry, each take SO-2's weakening delay at a tenant with a security record, and posture reports the setting and each declared path. A label edit on a runtime or a change to T's selector can add a runtime to a path at once, with no delay, which "Residual risks" states.
6. **Records.** The delegation audit event, which carries `cross_environment` today, gains `caller_environment` and `target_environment`. §11.7 events, `credential.mint`, and evidence for the child carry `environment`, which is T, and `crossedFromEnvironment`, which is the caller's. Billing and retention follow T, through the same field they read today.

**Accepted risk.** The owner accepted these two statements:

- On a declared path, an injected caller can hand data to a target runtime, which can send it anywhere the target allows, skipping the caller's denies and proxy.
- A non-member's agent can drive the target's credentials and receive results, on any pool with no user check.

The first statement includes anything the caller's agent can read, such as a `file` token with no `lockEgressToHosts`. A session under that lock cannot cross.

**Interactions.**

| Surface | Behaviour under this model |
| :-- | :-- |
| `restricted` pool | The `restricted` row applies with E = T: `gitAccess hosts ∩ git hosts(T) − denied(T)`. Nothing comes from the caller. |
| Mirror route | T's git routes apply. The resolver selects the pool from `visible(VCS pools, T)`, and that pool's credential selects the store. A fill's cause resolves to the child's session, so E = T. The child can read T's stores on pools with `userAuthorization: off`. |
| Connectors | §10.6 **Connectors are never cross-environment** holds, and "their own environment" means T, because the child is in T. It uses T's `connectorSelector`. §4.3 keys connector tokens on user, connector, tenant, and Environment, so Alice's token in `web` does not apply, and a connector needing a user token has none until she authorizes one in T. |
| `credentialPropagation: inherit` | §8.3's check stays. A crossing hop with `inherit` draws from the parent's origin LLM pool when the providers intersect, so the caller's LLM credential serves the target runtime. `independent` remains the default, and `deny` is available. |
| Concurrent and service-mode pods | The rule under "Pods with no per-session policy" applies with E = T, at placement. The caller's Environment is not read. |
| `ENVIRONMENT_REQUIRED` | It does not apply, because the child has E = T. |
| Default delegation policy | T's. It applies only to discovery, because §10.6 step 4 has no code today. |

## Identity model (TS-2, TS-4, and TS-6 to TS-9)

### Owner's decision

The owner decided decision 8 on 2026-11-03, reversing revision 12's recommendation, in these words: "I think we need to support per-tenant OIDC and serve SCIM. In that model, a deployer can use a broker by default but change to a customer's SSO when needed". The evidence for the need remains vendor documentation with no first-hand complaint found, so the decision rests on the owner's judgment of the product rather than on that evidence.

The model is:

- **A tenant has one or more identity sources.** The default is the deployment source, the issuer at `auth.oidc.issuerUrl`, which can be the operator's broker. It needs no record until the tenant uses SCIM, and the tenant comes from the tenant claim, under "Broker contract".
- **A tenant can instead be configured with a customer source**, the customer's own OIDC issuer, pinned to that tenant, under "Activation rule". The source is a configuration choice. Lenny has no mechanism for moving a tenant from one to the other, and the owner removed migration from this design.
- **Lenny serves SCIM 2.0**, for a tenant on either kind of source.
- **Both kinds coexist.** One deployment holds tenants on the deployment source and tenants on customer sources at once.
- **A single-company deployment is unaffected.** With `auth.multiTenant: false` or no customer source, the registry holds only the deployment issuer, subjects keep their form, and a write of a source is refused.

This table records what each side costs. Column B is now designed below, and column A stays the default.

| Concern | A: broker | B: per-tenant OIDC and SCIM in Lenny |
| :-- | :-- | :-- |
| Lenny code for SSO | The protected resource metadata route and the `WWW-Authenticate` challenge, and the boundary changes in this section | The same route, plus the source record, the verifier registry, per-issuer JWKS caches on the gateway and the Token Service, and per-source claim mapping |
| URLs from tenants | No issuer URLs. `identityProvider.introspectionEndpoint` is already a tenant-supplied URL the gateway calls, and TS-4 adds its missing address check. | Each issuer and `jwks_uri` is also a URL the gateway and the Token Service fetch |
| Issuer ownership | Broker concern | Lenny must establish that a tenant controls what it registers |
| Protocols | Whatever the broker federates, including SAML | OIDC alone. A SAML-only customer stays on the deployment source behind a broker. |
| Login routing | One authorization endpoint named in one metadata document. The broker does home-realm discovery. | Per-tenant URLs and metadata |
| SCIM | Lenny's server, or a broker or vendor endpoint with a sync worker | Lenny's server |
| Group storage | None required. Groups can stay in tokens. | New tables when a source takes groups from SCIM |
| Subject stability | The broker issues `sub`, so a customer changing IdPs behind it keeps its users. | `sub` belongs to each issuer, and Lenny provides no migration (see "Residual risks") |
| Several IdPs in one tenant | Broker concern | Lenny namespaces `sub` and groups |
| Tenant boundary | One claim from one issuer. A broker fault or mapping error crosses tenants. | The issuer selects the tenant, so a customer's IdP cannot assert another tenant |
| Outage | A broker outage stops logins for every tenant on it. | A customer IdP outage affects that tenant alone |
| Operator's work | Run or buy a broker | Run Lenny's new surface, and handle customer IdP support |

No effort estimate was measured. Column B is a new subsystem, and it does not remove the need for a customer-facing portal.

### What comparable systems do

- Keycloak's organizations link customer IdPs to an organization inside one realm, so one issuer serves all customers. Its token claim is an object keyed by organization alias. Keycloak's SCIM is per realm, and its documentation says organization groups cannot have members managed through SCIM ([project documentation](https://www.keycloak.org/docs/latest/server_admin/index.html)). A vendor extension adds [per-organization SCIM as experimental](https://phasetwo.io/blog/orgs-scim-experimental/) (vendor source).
- Auth0 organizations put `org_id` in tokens, and its documentation calls validating that identifier the preferred method ([vendor documentation](https://auth0.com/docs/organizations/using-tokens)). Its [device flow does not natively carry organizations](https://support.auth0.com/center/s/article/missing-org-id-in-auth0-device-authentication-flow) (vendor source), which matters to a CLI login.
- WorkOS sells Directory Sync, which [hides SCIM behind an API and delivers changes through webhooks or an events API](https://workos.com/docs/directory-sync) (vendor documentation).
- Products sold directly to enterprises expose SSO and SCIM themselves: [Devin](https://docs.devin.ai/enterprise/security-access/sso/guide.md) and [Cursor](https://cursor.com/docs/account/teams/scim) (vendor documentation). Those pages do not say whether a broker sits behind them. Devin's page states that without SCIM, groups sync only at login, and that the vendor's team configures the connection.
- [Coder](https://coder.com/docs/admin/users/idp-sync.md) syncs groups, roles, and organizations from claims per organization, and it overwrites membership at login (vendor documentation).
- Microsoft states that [`sub` is pairwise per application, `oid` is immutable across applications, and `email` must never be used for authorization](https://learn.microsoft.com/en-us/entra/identity-platform/id-token-claims-reference) (provider documentation). Three design choices below follow from those statements: a per-source subject claim, per-issuer `iss` values such as Entra's tenant-specific URI, and no automatic match on email.

### Identity source record (TS-6)

`tenant_identity_sources` is a tenant-scoped table under the existing row-level security policy.

| Column | Meaning |
| :-- | :-- |
| `tenant_id`, `name` | The primary key. `name` is short and tenant-chosen, it cannot contain `~`, and `deployment` is reserved. |
| `source_id` | A random short identifier, fixed at creation, used in canonical subjects, groups, callbacks, and SCIM credentials. |
| `issuer` | The exact issuer string. A partial unique index over rows in any state but `draft` holds the pin, so a draft claims nothing and a retired row still holds its issuer. Only a `draft` or `retired` row can be deleted, which for a `retired` row releases the index, and `source_id` is never reused. |
| `authorization_endpoint`, `token_endpoint` | Captured from discovery at proof, checked under "SSRF and NetworkPolicy", and used for every login. A change is a change on the source. |
| `audiences` | The values accepted in `aud`, never empty. When a token has several audiences and an `azp`, `azp` must be in this list or equal `client_id`. |
| `client_id`, `clientSecret` | The login client. PKCE with no secret is the default, as `buildPlaygroundOIDC` does today. `clientSecret` is optional and write-only, as below. |
| `subject_claim` | The claim holding the user identifier, `sub` by default. An Entra source can name `oid`. |
| `roles_claim`, `role_map` | The claim read for roles, and a map from its values to tenant-scoped role names. An unmapped value grants nothing. |
| `groups_claim`, `group_source` | The claim read for groups, and `token` or `scim`. |
| `service_claim` | An optional claim with the values that mark a token as `caller_type: service` or `agent`, as §25.1's agent callers need. Every other token from this source has `caller_type: human`. |
| `match_claim`, `scim_deactivate_mode` | The claim a directory's `externalId` must equal, fixed to `subject_claim` on a customer source, and what a SCIM deactivation does, under "SCIM server". |
| `email_domains` | Verified domains used for login routing. None is required. |
| `state` | `draft`, `pending_effective`, `active`, `suspended`, or `retired`. `pending_effective` applies only to first activation, and such a row refuses writes. A `retired` or `suspended` row refuses writes too, and a reactivation that changes a field states the changes as a `pending_version` and follows the activation rule. A `retired` row returns to `active` with the same `source_id` under **Reactivating**, so a customer can recover its own issuer, and a `suspended` row returns as "Failure behaviour (identity)" states. |
| `pending_version` | A proposed change to an active source or settings row, or to a reactivating row, under the activation rule. The row stays on its live values until the version takes effect. A pending `clientSecret` waits in its own sealed row keyed on tenant, `source_id`, and version, and the digest covers a keyed hash of its value. A pending `deploymentIssuerAllowed` waits on the tenant record beside the flag, in the same form. |
| `tokens_not_before` | Set at retirement and carried in the snapshot. Registry step 7 refuses any token, IdP or Lenny-issued, whose `iat` is earlier. |
| `proof`, `approved_digest` | The results of the checks under "Issuer pin and proof", with who ran them and when. The preview holds the canonical subject, and no name. Any draft edit clears `proof`. Approval records a digest of the first activation's row or of the pending version, which must still match at effect. |
| `activated_by`, `ticket_id`, `effective_at` | The activation record. |

**Deployment settings.** A tenant on the deployment source that uses SCIM has one row with `source_id: deployment`, the reserved name, and no `issuer`. It holds `match_claim`, `scim_deactivate_mode`, `group_source`, and its credentials, it has the same states, and it is not a registry entry. A change to it follows the activation rule, and the proofs do not apply, because it names no issuer.

**Client secret.** The same mechanism applies as for a link provider's client secret in `user-credential-delivery-design.md`. The Token Service stores the submitted value envelope-encrypted under the tenant's Token-Service-only key, in a row keyed on tenant and `source_id`, sealed with a digest of `tenant_id`, `source_id`, `issuer`, `token_endpoint`, and `client_id`. No route returns it, and no field names a Secret, so a tenant cannot point at another tenant's. The Token Service alone reads it, and a source with a secret has its code exchange done by the Token Service over mTLS, which sends the value only when the digest matches and returns the token response to the gateway. A change to any sealed field leaves the secret unusable until a write re-supplies it.

A tenant holds at most a small number of sources, a Helm value that defaults to 5, so the registry and its caches stay bounded. A write naming an issuer on `identity.deniedIssuers`, a Helm list that ships with known shared issuers, is refused, and so is the deployment issuer. Writes go through a new admin resource, `/v1/admin/tenants/{id}/identity-sources`, with `If-Match`, and through EG-9's single write path once it lands. §10.6's `identityProvider` keeps its introspection settings and gains no issuer.

**Activation rule.** A source can be drafted by a `tenant-admin`, a `security-admin`, or a `platform-admin`. Activation is the power to decide who every user in a tenant is, so it follows a stricter rule (decision 17):

- A `platform-admin` activates a source, with a ticket, once the proofs pass. A `tenant-admin` cannot, because a stolen tenant admin credential could otherwise plant a lasting backdoor.
- At a tenant with a `security-admin` row in any state, activation and a change to `deploymentIssuerAllowed` take SO-2's delay. Any reachable `security-admin` must also approve and can cancel. Invalidating the security team therefore does not remove the delay. This extends SO decision 23's rule for `identityProvider.*`, since an operator able to repoint an issuer silently could regain what the pin removes.
- A deployment can set `identity.allowTenantActivation: true`, default `false`, under which a tenant's `security-admin` activates alone, with the proofs passing and the same delay, and no operator ticket. A tenant with no `security-admin` always goes through the operator.
- Every change on an active source or a deployment settings row, including `roles_claim`, `groups_claim`, `group_source`, `client_id`, `clientSecret`, endpoints, an added `email_domains` or `audiences` entry, `match_claim`, and `scim_deactivate_mode`, creates a `pending_version` under the same rule. Further widening writes are refused while one exists. A stated narrowing applies at once: removing an audience other than the last, a `role_map` entry, or an email domain. A narrowing, a suspension, or a retirement cancels the pending version, and its proposer can submit it again.
- Before activation, a preflight reads the proof's preview. At a tenant with a `security-admin` row in any state, it refuses unless a `security-admin` row exists on a canonical subject of the new source, so oversight survives activation, and it lists what stays on old subjects: principals, `lifecycleSubjects`, role rows, and owners of running sessions. On a reactivation it also lists role rows, custom roles, and SCIM credentials written by that source's subjects. The preflight refuses or lists, and it migrates nothing.
- **Retiring.** A `security-admin` retires a source at once. A `platform-admin`'s retirement at a tenant with an active `security-admin` takes SO-2's delay and can be cancelled, because immediate retirement would lock out the principals who could cancel what follows. At a tenant with none, it applies at once with a ticket. An attacker asserting a principal's subject through a compromised issuer could cancel a delayed retirement, so the incident path is invalidation first and retirement second: an invalidated principal does not count as active here, and this path is reversible. Retirement sets `tokens_not_before`, and TS-8 adds the step that revokes the source's SCIM credentials. It ends playground records from the source. Its subjects are refused under "Verifier registry".
- **`invalidateSubjects`.** This optional mode needs a second `security-admin` or SO-2's delay, because it cannot be undone, and that confirmation comes before the retirement it accompanies, since a sole source's retirement refuses the second principal too. It runs §11.4's `full_revoke` for each of the source's subjects, as a job that reports partial failure, placing an `admin` hold on each. Reactivation clears none of those holds. An admin clears them per subject, or in bulk by naming the job.
- **Stranded rows.** A security principal row on a subject whose source is not active counts as active for approval and for SO's **Platform admin limit**, so a retirement cannot remove the approver by locking it out.
- **Reactivating.** Returning the same `source_id` needs a new sign-in proof and a ticket recording the customer's request, and it drops cached keys. The row returns on its stored values, and any changed field waits as a `pending_version` under the activation rule. When a reachable principal exists, such as on a second source, it also needs that principal's approval and the delay. A proof does not show that a compromised key was rotated, because such a key can choose its own `iat`, so the customer rotates it first. A different issuer follows the activation rule.
- **A customer that has permanently lost its issuer.** Its principals cannot authenticate, and they count as active, so no customer approval exists. The operator removes those rows under SO-2's delay, which nobody can cancel, and assigns a new `security-admin` against the customer's recorded request. The customer's protection in this case is the operator's process and the mirrored audit record.
- `issuer` and `subject_claim` cannot change on a row, because canonical subjects include `source_id` and a changed issuer's `sub` values could collide with the old ones and inherit their records. A different issuer is a new row with a new `source_id`.

**Pointing an existing tenant at a different issuer.** The write is allowed, under the activation rule and its preflight above, with no further check. Refusing it would need a definition of a tenant that already has users, and an exception for tenants that want this. "Residual risks" states the consequence.

**Which issuer a tenant accepts.** The tenant record holds `deploymentIssuerAllowed`, true by default. Activating a customer source sets it false, and retiring that source leaves it false, so retiring a compromised source does not reopen the tenant to the broker. Setting it back to true follows the activation rule. A tenant therefore accepts either the deployment issuer or its customer sources, and never both.

### Verifier registry

`pkg/auth/jwt` gains a registry member that `composeBearerVerifier` and `buildCallerVerifier` join behind the Lenny key set, so the gateway and the Token Service run one implementation:

1. It refuses a token over 16 KiB, decodes the payload with no verification, and reads `iss`, capped at 512 bytes.
2. It looks `iss` up in an in-memory snapshot of active sources, by exact match. The deployment issuer is the entry that keeps today's path.
3. An unknown `iss` is refused with `401 TOKEN_INVALID`, with no fetch of that issuer and no store read. The reason `unknown_issuer` appears in logs and counters and never in a response, because a caller could otherwise test which IdPs are customers. §25.4's `serveServiceIdentity` still submits a `TokenReview` for a token claiming its audience, which is an existing path and is not an issuer fetch.
4. The entry's own verifier checks the signature, `iss`, `aud`, `exp`, and `nbf`. The unverified `iss` selected a verifier and nothing else, so a token claiming a victim's issuer but signed elsewhere fails against the victim's keys.
5. For a customer source, the tenant is the record's tenant, and any tenant claim in the token is ignored. For the deployment issuer, the tenant comes from `auth.tenantIdClaim`, and the request is refused with `401 TENANT_ISSUER_MISMATCH` when that tenant's `deploymentIssuerAllowed` is false. This second check is what keeps a broker out of a pinned tenant.
6. The claim allowlist, `normalizeUserBearer`, and the role boundary apply, and the canonical subject is computed.
7. A subject whose source is not active is refused with `401 TOKEN_INVALID`, by its `~<source_id>~` prefix against the snapshot, and so is a token whose `iat` precedes the source's `tokens_not_before`, or that has no `iat` when that value is set. A bare subject is refused at a tenant whose `deploymentIssuerAllowed` is false. This check sits in the auth middleware, in `lenny-ops`, and on `/v1/oauth/token` for the caller, subject, and actor tokens, so it covers Lenny-issued tokens too. The bootstrap `lenny-admin` credential and §25.4 service identities are exempt, as the recovery path.

The snapshot is rebuilt from Postgres on a version change announced through Redis and on a poll, and it survives a store outage with its last value. It reads a dedicated view exposing `tenant_id`, `source_id`, `issuer`, `state`, `tokens_not_before`, `deploymentIssuerAllowed`, and the mapping fields to a dedicated role, because a pre-authentication read across tenants cannot use §12's `__all__` path. `lenny-ops` keeps its single issuer, and a customer source's user reaches it only through a Lenny-issued token. TS-2 closes F-13.3.27: `/v1/oauth/token` refuses a playground-origin `subject_token`, because `mintToken` drops `origin` and `lenny-ops` would otherwise accept the result. It reverses `TestPlaygroundOriginSubjectTokenAccepted_spec_13_3` and the `authenticateCaller` comment that pin today's behaviour. Tenant administration uses the gateway's admin API. Reasons are counted without the `iss` value, because it is attacker-chosen.

### Keys and caches

Each source has its own `OIDCVerifier` on each gateway and Token Service replica, built lazily on the first token for that issuer. `OIDCVerifier.key` holds its mutex across a fetch, so one slow IdP can pile up waiting requests. TS-6 therefore refreshes in the background with no lock held. `MinRefetchInterval` already limits fetches for an unknown `kid`, per issuer. A refresh re-reads discovery, because `jwksURI` is otherwise cached until restart.

These verifiers accept keys that a tenant's issuer chooses, and verification runs before the rate limit in `httpsurface.go`. TS-2 therefore bounds RSA at 4096 bits, a Helm value, and requires `e` of 65537, because `publicKey` enforces only a minimum today. The cost of an oversized key has not been measured.

A refresh failure keeps the previous keys, as `refreshLocked` does today. A replica that restarts during an IdP outage has none, so that source's tokens fail on that replica until the IdP returns. For a customer source this design adds a maximum staleness, a Helm value that defaults to 24 hours, past which that source's tokens are refused with `503` and its alert fires, because an IdP that rotated out a compromised key while unreachable would otherwise stay trusted indefinitely. The deployment issuer keeps today's behaviour. Decision 22 covers the value.

### Claim allowlist and mapping

TS-2 changes the IdP boundary for every issuer. Today an IdP-verified token keeps `caller_type`, `act`, `authorized_tools`, and `scope`, and `normalizeUserBearer` clears only `typ`, `session_id`, `delegation_depth`, and `origin`. The boundary instead builds claims from an allowlist, each read by exact key, because Go's struct decoding also matches other cases: `iss`, `aud`, `azp`, `exp`, `nbf`, `iat`, `jti`, `sub` or `subject_claim`, `scope`, the roles claim, and the groups claim, plus the tenant claim on the deployment issuer. `match_claim`, `service_claim`, `nonce`, `email`, and UG's provider identity claim are read by exact key and returned only to the purpose that needs them. Everything else is dropped. An absent or empty subject claim refuses the token.

`scope` is filtered to values with the `tools:` prefix, as §25.1's deployer policies at the IdP need. A token with no such value has an absent scope, and a malformed `tools:` value stays `TOKEN_INVALID`, as does a value with any case of `tool` as its prefix that fails to parse, such as `tool:pool:read` or `TOOLS:pool:*`, because reading it as absent would widen the token. Dropped values are counted. No mapping is needed, because scope only narrows, so it is safe from any issuer, and `openid profile` no longer refuses the token. `caller_type` comes only from `service_claim`, or `auth.serviceClaim` on the deployment issuer, which can set `service` or `agent`, and every other token reads `human`. The `security-*` values in the roles claim pass the boundary to SO's resolver, which removes them under `managed` and reads them under `oidc`, so `role_map` is not involved.

The tenant has no mapping on a customer source, because the record supplies it. `role_map` is explicit because customer IdPs emit a `roles` claim for their own purposes, as Entra's token reference shows, and today's code would read it as Lenny roles. The mapping can name tenant-scoped built-in roles and custom roles, and a write naming `platform-admin` or a `security-*` role is refused. `groups_claim` must yield an array of strings. Past Entra's overage limit [the claim is absent](https://learn.microsoft.com/en-us/entra/identity-platform/id-token-claims-reference) (provider documentation), which reads as no groups and refuses the Environment, so a tenant with users in many groups uses `group_source: scim`.

### Issuer pin and proof

An issuer pins to one tenant through the partial unique index, checked at activation, and the deployment issuer can never be registered as a customer source (decision 18). A conflict returns a generic refusal, so the answer does not say whether another tenant holds the issuer. `email_domains` are unique in the same way. The harm being prevented is specific. A tenant pointing at an issuer it runs gains nothing outside itself, because the tenant comes from the record. A tenant claiming another customer's issuer can squat it, so the customer that owns it cannot register, and with a claimed email domain it can route that customer's users to its own tenant's login.

These proofs run in order, and each is recorded on the row:

1. **Discovery.** `issuer` in the document must be identical to the URL used, as [OpenID Connect Discovery requires](https://openid.net/specs/openid-connect-discovery-1_0.html), and `jwks_uri` must be `https`. This proves the URL serves a consistent issuer, and no more.
2. **Sign-in.** A named customer administrator completes an authorization code login with PKCE and a nonce against the issuer, through the client the record names. The login starts from the drafting admin's session, or from a one-time token that names the expected source, is short-lived, and is single use. It uses the callback and resolver under "Browser login" and creates no session or bearer. The result previews the mapped subject, roles, and groups. This proves a client is registered at that issuer and the mapping works. It is weak on ownership alone, since some IdPs let an ordinary user register an application.
3. **Domain.** For each entry in `email_domains`, a DNS TXT record at `_lenny-challenge.<domain>` holds a value bound to the tenant and source. This proves control of a domain and is what permits routing by it. It does not bind a domain to a hosted issuer, because no record at `login.microsoftonline.com` is the customer's to write. The record is re-read daily, and a lapsed domain stops routing and affects no token.
4. **Attestation.** The operator's ticket records the customer's request for this issuer. Under `identity.allowTenantActivation`, the `security-admin`'s approval stands in its place.

No mechanical proof binds a customer to a hosted issuer, so the pin rests on proofs 2 and 3 together with proof 4. A disputed issuer is resolved by a `platform-admin` retiring the wrong row and deleting it, with an audit event, which releases the index. At a tenant with a `security-admin` row in any state, the delete takes SO-2's delay. If the tenant's own `security-admin` cancels the retirement, the operator invalidates that principal first, which applies at once, and then retires. "Residual risks" states this.

### SSRF and NetworkPolicy

Every customer issuer, `jwks_uri`, `authorization_endpoint`, and `token_endpoint` passes one checker, shared with TS-4's `introspectionEndpoint` check. The deployment issuer is exempt, because an operator can run a broker in the cluster:

- The URL must be `https`, on port 443 unless the record's port is on a Helm allowlist, with no userinfo or fragment, and with no query except on `authorization_endpoint`, where some IdPs publish one.
- Each resolved address is refused when it falls in the §13.2.5 excluded ranges, in `127.0.0.0/8`, `::1/128`, `0.0.0.0/8`, or `100.64.0.0/10`, which that list omits and which NetworkPolicy does not cover for loopback, or in the cluster's own node and load-balancer addresses. An IPv4-mapped IPv6 address is normalized first.
- The dialer repeats that check on the address it connects to, so a name that re-resolves between check and dial is refused.
- Redirects are refused on every call the checker governs, including the token request, whose client follows them today. The transport ignores proxy environment variables. Responses are capped in size, and the key count is bounded.

The consequences in the chart are these:

- **Gateway.** `allow-gateway-egress-llm-upstream` is the gateway's only external HTTPS path, and it already excludes private ranges (§13.2.4). When `gateway.llmUpstreamEgress.enabled` is false or `egressCIDRs.llmProviders` replaces the broad rule with a CIDR list, a customer's issuer is unreachable. The chart refuses `identity.customerSources.enabled` with the rule disabled, and it warns on a CIDR list, which gains an `identity.issuerCIDRs` value for strict deployers. NetworkPolicy cannot match names, so a strict deployer maintains addresses per customer.
- **Token Service.** `allow-token-service` renders `kms.endpointCIDR` on TCP 443 unconditionally, which is open with no excepts under the local provider's default and closed to issuers once a cloud KMS narrows it. "Defects found while verifying" records this, and the Token Service should use `lenny.kmsEgressRule`, as the gateway does. Without that change, the excepts below are void under the local provider, because allows are additive, and only the dialer's check would hold. `egress-and-git-design.md` adds a direct rule for provider APIs with the gateway's exclusions, under "Credentials", and this design uses the same rule, which also widens the egress of the component that holds every provider credential. TS-6 renders the rule when EG-1 has not.
- **Private-address IdPs.** An issuer on a private address is refused in the first version, so that customer stays behind a broker or exposes its IdP's metadata publicly (decision 21).

### Browser login (TS-6)

A browser login is part of the same boundary as the registry. The playground, the sign-in proof, and the link start page in `user-git-identity-design.md` all use this path, and this design lands it in TS-6 because customers register these URIs at their IdPs and a later change forces them to register again.

1. **One resolver.** The registry exposes one function that takes a source and an ID token and returns the tenant and canonical subject, applying the allowlist, the role boundary, `deploymentIssuerAllowed`, and the active-source check. `extractSubject`'s read of raw claims goes away, so a bare playground login with `tenant_id: acme` on a broker token gets `TENANT_ISSUER_MISMATCH`. The proof purpose is exempt from the active-source check, since it runs on a draft, and it creates no session.
2. **One redirect URI per source.** Each source has `/auth/callback/{source_id}`, and the deployment source has `/auth/callback/deployment`. `state` names the purpose, so one URI serves all three uses.
3. **Refusal before any token request.** The state cookie is scoped to that callback's path, with a short lifetime as today. The callback refuses when the cookie is absent, when `state` does not match, or when the state's source differs from the path's source, and none of these sends the code or the verifier anywhere. A hostile source that bounces a browser to another IdP therefore delivers the code to the other source's callback, where no state cookie exists.
4. **RFC 9207.** When the response carries `iss`, it must equal the source's issuer. It is not required, because IdPs differ.
5. **Exchange, then resolve.** The code and verifier go only to the source's recorded `token_endpoint`, directly or through the Token Service when the source has a secret. The ID token and nonce then go through the resolver.
6. **Cookies.** The state cookie is scoped per source, as above. The playground session cookie is scoped per tenant in TS-7, by path under `/t/{tenantId}/playground/`, with the mint moved under that prefix, because a browser does not send a cookie scoped to `/playground/` to `/v1/playground/token`. A silent login at a hostile source then cannot replace a session on another tenant.

How each provider behaves on these flows is untested, including silent single sign-on and RFC 9207 support.

### Login routing (TS-7)

Only the deployment source can use the bare URLs, because nothing else identifies a tenant before a token exists.

| Client | How the tenant is known | Behaviour |
| :-- | :-- | :-- |
| MCP client | A per-tenant resource URL, `/t/{tenantId}/mcp` and `/t/{tenantId}/mcp/environments/{name}` | A 401 names the metadata at `/.well-known/oauth-protected-resource/t/{tenantId}/mcp`, which lists that tenant's active issuers in `authorization_servers`. Bare `/mcp` names the deployment issuer alone. |
| REST client | The same prefix, or none, since the bearer identifies the tenant | A token from a customer source also works on the bare path. Under `/t/{tenantId}`, a token that resolves to another tenant gets `403 TENANT_PATH_MISMATCH`. |
| Playground | `/t/{tenantId}/playground`, or an email domain typed on the shared page when the source has verified domains | Login follows "Browser login". A tenant with several sources gets a chooser. |
| `lenny-ctl` | `--token` or `LENNY_API_TOKEN`, as today. It has no login command. | Unchanged. A customer's administrator supplies a bearer from its own tooling. |
| Link start page (`user-git-identity-design.md`) | The flow's tenant | Logs in through that tenant's source and "Browser login", in place of `auth.oidc.issuerUrl` alone. |

RFC 9728 metadata names the deployment issuer for the deployment resource, and per-tenant documents name only that tenant's issuers (decision 20). One shared document listing all issuers would disclose every customer, and [the MCP specification leaves selection to the client](https://modelcontextprotocol.io/specification/2025-06-18/basic/authorization). The per-tenant document is public, so it discloses a tenant identifier and its IdP host to anyone who guesses the identifier, as the egress design's `/oidc/<tenant-id>` path already does for its identifier. The specification also says a client without dynamic registration needs a client ID by hand, so a customer IdP lacking that feature needs one configured in each MCP client. How MCP clients behave against these documents is unproven.

### Several IdPs in one tenant, and namespacing

A customer source's user key is the canonical subject `~<source_id>~<value of subject_claim>`. `subjectPattern` in `userstore` already admits `~` and limits a subject to 256 printable characters, and a longer result is refused at the boundary. A deployment source's `sub` keeps its form, so a single-company deployment and every tenant that never moves are unchanged. Once customer sources are enabled, a deployment token whose `sub` begins with `~` is refused, and a deployment group beginning with `~` is dropped, so no presented value can collide. `(tenant_id, subject)` stays the user key throughout.

Groups follow the same form, `~<source_id>~<group>`, because `oidc-group` is a bare string match and two IdPs could both assert `security-engineers`. TS-6 fixes this form, since Environment members store it. Under `group_source: scim`, `<group>` is `displayName`, which SCIM holds unique per source. A rename changes the string, so its members stop matching, which fails closed, and posture lists the Environment members with no group. An Environment member names that string, and the admin API accepts it verbatim. A deployment source's groups keep their form. Two subjects from two sources in one tenant are two users, and Lenny has no account linking between them.

The `~<source_id>~` prefix in `user_id` names which issuer vouched, so §11.7 events and evidence need no new field for attribution. A deployment subject carries no prefix, which names the deployment issuer.

### Role boundary

§10.2 **Role assignment:** allows an OIDC claim to carry any built-in role. In code, an IdP-verified token's `roles` reach `Principal.Roles` unchanged, and `platform-admin` then spans all tenants. TS-2 closes this for every issuer, and TS-6 extends it:

- The IdP boundary, beside `normalizeUserBearer`, removes `platform-admin` from an IdP-verified token's roles on the gateway and on the Token Service. The request continues with the remaining roles. Each removal emits an `auth.platform_role_dropped` audit event and a counter with an alert.
- A customer source goes further, because only values `role_map` names become roles, and that map cannot name a platform role. A customer's IdP therefore has no way to express one.
- `platform-admin` comes from a platform-managed role row, a Lenny-issued token, or a §25.4 service identity. `/v1/admin/users` already allows a `platform-admin` to grant that role, and §15.1 must list that surface, which it omits today.
- **Operators' staff.** `auth.platformAdminTenant` names one operator tenant, and `platform-admin` rows exist only there, on every write path: `POST` and `PUT /v1/admin/users`, and the bootstrap seed, with `tenant_user_roles.go` already refusing the role. The role resolver ignores such a row elsewhere, and chart validation requires `gateway.adminToken.tenant` to match. Staff log in through whichever issuer authenticates that tenant. The broker contract requires that tenant's IdP link to be operator-only.
- **What this does and does not stop.** A customer's IdP can never reach platform authority, on either kind of source. The issuer of the operator tenant can, by asserting a staff `sub`. With the default, a broker compromise reaches platform authority and therefore every tenant, including pinned ones through impersonation. The operator tenant can hold a customer source naming the operator's own IdP, which takes the broker out of that path (decision 24).
- The specification and the code agree on claim names (decision 10).

**How the tenant-claim risk changes.** For a tenant on the deployment source, the risk is as it was: a broker fault or compromise can issue a token for that tenant. For a tenant on a customer source, the tenant comes from the pin and the check in registry step 5 refuses the deployment issuer, so the broker cannot reach it, and the customer's IdP can reach only its own tenant. A broker and a pinned issuer therefore coexist without either speaking for the other's tenants at the token boundary. This does not remove the operator from the pinned tenant's trust. A `platform-admin` retains impersonation and can repoint a source, both audited, and the second takes the delay at a tenant with a `security-admin` row in any state.

**An issuer's power over its principals.** An issuer controls every principal it authenticates. It can assert the subject of a `tenant-admin` or a security principal, and it chooses the `iat` value SO's SP1 reads, and the row then grants the role, whatever `securityRoleSource` says. `managed` stops a role claim, and it does not separate a principal from its issuer. On a tenant on the deployment source, an operator's broker can therefore assert a `security-approver`'s `sub` and approve the operator's own request. Posture reports each principal's issuer. Only an independent second factor on approvals would change this, and decision 25 leaves it out.

### Broker contract

These go into §10.2 as requirements on the issuer named by `auth.oidc.issuerUrl`, and they are the contract for the default source.

| Claim | What Lenny does | Requirement on the broker |
| :-- | :-- | :-- |
| `iss`, `aud`, `exp`, `nbf` | Verified against `auth.oidc.issuerUrl` and `auth.oidc.clientId` | One issuer and one audience for all tenants on this source |
| Tenant claim (`auth.tenantIdClaim`) | Read as a string. Refused when absent, malformed, unregistered, or naming a tenant that does not allow this issuer. | Set from the broker's organization record. Never derived from a user attribute or a claim a customer can write. A nested claim needs a mapper to a string. |
| `sub` | The user key within a tenant, and session owner | Stable, never reassigned, unique within a tenant across all of that customer's IdPs, and not beginning with `~` |
| `roles` | An array of role names | Tenant-scoped roles only. A customer's own mapping must not reach it unfiltered. |
| `groups` | An array matched exactly against `oidc-group` members | Stable names, namespaced when a tenant has several IdPs. A complete list or none: Entra [limits a JWT to 200 groups](https://docs.azure.cn/en-us/entra/identity/users/directory-service-limits-restrictions) (provider documentation), and past that a broker must resolve the set itself. An absent claim reads as no groups, so the failure is a refused Environment. |

The broker must also keep token lifetimes short enough for the operator's offboarding bound, and keep the operator tenant's IdP link operator-only. It delivers lifecycle events through a sync worker, or the customer's directory calls Lenny's SCIM directly.

### Discovery

§10.2 calls the gateway an MCP-standard protected resource server. [The MCP authorization specification](https://modelcontextprotocol.io/specification/2025-06-18/basic/authorization) requires such a server to serve RFC 9728 metadata and to name its location in `WWW-Authenticate` on a 401. No route, handler, or specification text in the tree does this, so a client learns the issuer out of band. TS-2 adds `/.well-known/oauth-protected-resource`, naming `auth.oidc.issuerUrl`, and the challenge header. TS-7 adds the per-tenant documents.

### SCIM server (TS-8)

The server implements RFC 7643 and RFC 7644 at `/scim/v2/{tenantId}/{sourceName}`, on the gateway's ingress behind its own middleware rather than the OIDC chain. A tenant on the deployment source uses the source name `deployment`.

| Surface | First version |
| :-- | :-- |
| Discovery | `ServiceProviderConfig`, `ResourceTypes`, and `Schemas`, stating what is absent |
| Users | `GET` by `id`, list with pagination, `POST`, `PUT`, `PATCH`, and `DELETE`. Core user schema. Extension fields are dropped and not stored. `externalId` cannot change after provisioning. |
| Groups | The same methods, with `PATCH` add and remove on `members`, and `excludedAttributes=members`. `displayName` is unique per source. |
| Filters | `eq` on `userName`, `externalId`, `id`, and `displayName`. Any other filter answers `400 invalidFilter`. |
| `PATCH` | Both the `path: active` form and the pathless `value` form. Vendor blogs report that [identity providers differ on this](https://ssojet.com/blog/okta-scim-provisioning-saas-vendors) (vendor source), and Okta's own documentation was not retrieved. |
| Absent | Bulk, sort, ETags, `/Me`, and password changes |

These follow what [Microsoft's tutorial](https://learn.microsoft.com/en-us/entra/identity/app-provisioning/use-scim-to-provision-users-and-groups) lists for its service (provider documentation): queries by `userName` and `externalId`, groups by `displayName`, `excludedAttributes=members`, and a soft-deleted user still returned with `active=false`.

**Credentials.** A credential is a random 256-bit bearer, bound to one tenant and one source, stored only as a keyed hash, shown once, and rotated with an overlap. A `tenant-admin` holding `manage_user_lifecycle` or a `security-admin` mints one, because the directory is the customer's. It grants the two calls of TS-4 and writes to directory tables, and nothing else in the admin API. Its audit subject is `scim:<source_id>`. Disallowing the deployment issuer disables a `deployment` credential.

**Quotas.** Per source, Helm values cap credentials, users, groups, and memberships per user. Requests are rate limited per tenant, and failed authentication per source address, since an invalid bearer has no credential to count against. The body limit is 1 MiB, invalidations are coalesced, and the group cache is bounded per tenant.

**Matching a directory user to a token.** SCIM carries no OIDC subject, so a source names `match_claim`, a claim the source declares immutable and verified, whose value must equal the directory's `externalId`. A declaration is a change on the source, under the activation rule. `userName` and `preferred_username` never bind, because a user may be able to edit them and Microsoft warns that they change.

- On a customer source, `match_claim` is `subject_claim`, so the canonical subject is computed at provisioning. `externalId` is unique per source. An Entra source maps `objectId` to `externalId` and sets `subject_claim: oid`, since Microsoft's default user mapping uses `mailNickname` and `sub` is pairwise.
- On the deployment source, a broker issues its own `sub`, so the directory row stays unbound until a token's `match_claim` equals `externalId`, and it then binds once. Every IdP token from a deployment source with a settings row is checked against the directory, with the read cached, whether or not a user row exists. A match on an inactive row refuses the login and places a hold on that subject under the `lifecycleSubjects` rule, so a security principal is protected as it is on a direct write. A deployment `externalId` beginning with `~` is refused.
- `active: false` on an unbound row has no subject to hold. The SCIM response reports it, and an alert fires. "Residual risks" states what this leaves running.
- Posture reports provisioned rows that never matched a login, and logins with no directory row, because a wrong mapping otherwise deactivates a subject nobody uses while the intended user stays active.

**Deprovisioning into TS-4.**

| SCIM operation | Effect |
| :-- | :-- |
| `active: false` | A hold of this source's class at `scim_deactivate_mode`, `full_revoke` by default (decision 11), through the same code as `POST .../invalidate`, at server time |
| `DELETE /Users/{id}` | The same hold, and the directory row goes. The user row stays held. It is not an erasure, which keeps §12.8's job path. |
| `active: true` | Clears this source's hold alone. Any other class's hold stays, and the response reports `active: false`. |
| Any write on a subject with a completed erasure job | `409` |
| A lifecycle write on a security principal | Refused per user with `403` unless `scim:<source_id>` is in SO-2's `lifecycleSubjects`, and posture reports it. A group write is not a lifecycle write. |
| Provisioning a user | A `users` row with no role and `RoleAssigned` false, written by its own path, because `handleCreateUser` sets it and would blank the user's token roles. No session, role, or Environment follows from it. Decision 13's rule on login rows is unchanged. |

A store failure answers `503` with `Retry-After`. Microsoft's service [retries and eventually quarantines a failing job](https://learn.microsoft.com/en-us/entra/identity/app-provisioning/how-provisioning-works) (provider documentation), so a user stays active during an outage, which "Residual risks" states.

**Conformance.** The test corpus is a set of recorded and hand-written request sequences per directory, because no live directory is available to the suite. Passing it shows agreement with the corpus and no more, and each directory's own validator is a manual step per release. No directory has been tried.

### Directory groups (TS-9)

`directory_users` holds `tenant_id`, `source_id`, the SCIM `id`, `external_id`, `user_name`, `active`, and the bound subject, if any. `directory_groups` holds the SCIM `id`, `external_id`, and `display_name`, and `directory_group_members` joins them. All are tenant-scoped under row-level security.

`group_source` on the source selects where a user's groups come from, and the two never merge (decision 19):

- **`token`**, the default, keeps today's behaviour.
- **`scim`** makes the auth middleware replace `Principal.Groups` from `directory_group_members`, at the same point introspection replaces them today, for IdP tokens and Lenny-issued tokens alike.

A tenant with a customer source refuses `introspectionEnabled` on every source kind, because a tenant-set endpoint would receive every bearer presented for the tenant. Activation refuses while it is on. A deployment-source tenant is left to SO decision 23.

`envaccess` keeps its `Caller` input, so `Membership` and `AuthorizedRuntimes` are unchanged, and the new reader sits ahead of it. The read is cached for 30 seconds, a SCIM write invalidates it through Redis, and a store failure answers `503`, as `GROUP_INTROSPECTION_UNAVAILABLE` does. `GET /v1/admin/environments/{name}/access-report`, which §15.1 describes as a resolved member list with group expansion, can read these tables for a `scim` source and can do no such expansion for a `token` source.

**Erasure and classification.** §12.9 adds directory users, groups, and memberships at T3 beside user identifiers, source records at T3, and SCIM credential hashes and the stored client secret at T4. `DeleteByUser` removes the directory row, the memberships, and any proof preview naming that subject, and `DeleteByTenant` removes all of them. An unbound directory row has no subject to find, so an erasure job addresses it by SCIM `id`. §12.8's tombstone refuses re-provisioning an erased subject, with the `409` above. Group display names can carry personal or organizational data, so they are T3 as well.

### When SCIM and token claims disagree

| Disagreement | Which wins | Reason |
| :-- | :-- | :-- |
| A valid token, and SCIM has deactivated the user | SCIM | TS-4's refusal at authentication reads the row on every bearer, so a token's remaining lifetime grants nothing at `full_revoke`. |
| A stale `groups` claim, with `group_source: scim` | SCIM | The claim is ignored. |
| A `groups` claim, with `group_source: token`, and SCIM holds different groups | The token | The source said so. Posture reports a tenant whose SCIM groups are stored and unused, because this is the likely misconfiguration. |
| SCIM says active, and the IdP refuses login | The IdP | No token exists, so nothing reaches Lenny. |
| SCIM says `active: true`, and an administrator holds the user | The administrator | SCIM clears only its own hold. |
| A role in a token and a platform-managed row | The row | §10.2's precedence. SCIM carries no roles in the first version. |

For the TS-1 rules:

- Explicit Environment admission and the TS-3 transparent `creator` check use the groups the table above resolves.
- `ENVIRONMENT_REQUIRED` reads the tenant's Environments and no groups, so neither source can change its answer.
- A running session keeps E, and `visible`, the delegation gate, and `ENVIRONMENT_POLICY_NOT_ENFORCEABLE` read E alone, so a SCIM group removal does not reach a running session. Only a lifecycle event does, under TS-4's levels.
- A Lenny-issued token carries groups copied at exchange under TS-3, and under `scim` they are replaced at each request, so they cannot outlive a removal. Under `token`, they last until that token's expiry.

### User lifecycle (TS-4)

A directory reaches Lenny in one of two ways: directly through the SCIM server, or through a broker or vendor endpoint and a sync worker that calls the routes below. Both end in the same invalidation code. Today that code fails in these ways:

- `POST /v1/admin/users/{userId}/invalidate` answers `404` when the user has no row. Rows come only from the admin API, so a user who only ever logged in through an IdP cannot be invalidated, and that user's sessions keep running.
- `requireActiveUserForAction` admits a user with no row, so an absent row also means no gate on a fresh token.
- `hard_disable` and `full_revoke` set `DeletedAt`, and no endpoint clears it, so a returning employee cannot be reinstated. Entra [reactivates users that move from soft-deleted to active](https://learn.microsoft.com/en-us/entra/identity/app-provisioning/how-provisioning-works) (provider documentation).
- Invalidation needs `manage_users`, which also assigns roles. A worker holding it has more authority than deprovisioning requires.
- Invalidation does not stop an administrator. `serveBearer` never reads `IsActive`, `PlatformRoles.ResolveRoles` ignores `Disabled` and `DeletedAt`, and the only callers of `user.IsActive` guard session creation, interaction resolution, and elicitation. An invalidated `tenant-admin` holding a live IdP token keeps the admin API.

TS-4 makes these changes:

- **Upsert on invalidate.** Invalidating an unknown subject creates its row in the invalidated state and runs the §11.4 steps, which list sessions by owner and do not depend on the row having existed.
- **Refusal at authentication.** The row lookup the middleware already performs for `PlatformRoles` also reads the invalidation state, so this adds no read. A `full_revoke` row refuses every bearer with `401 USER_INVALIDATED`, on the gateway, on `lenny-ops`, and for a caller credential at `/v1/oauth/token`. On the gateway this adds no read. The Token Service has no user store today, so it gains a pooler read of the user row. A `soft_disable` or `hard_disable` row keeps the §11.4 session behaviour and reduces the principal's roles to `user`, so no disabled user holds admin authority.
- **Holds per caller class.** The user row gains a `holds` column, one entry per class: `admin`, `sync`, and `scim:<source_id>`, each with its mode and stored time. The effective mode is the highest remaining, and a user is active only when none remains. A caller holding `manage_users` or `security-admin` is `admin`, and any other caller with `manage_user_lifecycle` is `sync`. `invalidate` writes its caller's class, and `reinstate` clears only that class, so a directory or a stolen worker credential cannot erase an incident hold. An `admin` caller can name other classes to clear. This is a stored format, and it lands in TS-4 because TS-8 reads it.
- **Deleted rows and store errors.** A `DELETE`d row is refused as a `full_revoke` row is, because `SoftDelete` sets `DeletedAt` and leaves `RoleAssigned` and `Roles` in place, so a deleted administrator keeps its role today. A store error fails closed on the gateway and the Token Service. `lenny-ops` deliberately authorizes on the token's roles when the lookup fails (`claimOnLookupError`, §25.4), and this design leaves that as written, so a Postgres outage suspends the refusal on `lenny-ops` alone. A §25.4 service identity is unaffected, which keeps a recovery path.
- **Impersonation bearers.** `soft_disable` and `hard_disable` run the §13.3 **Platform-admin impersonation** rule 6 cascade, which runs on `full_revoke` alone today, so a disabled user's bearers do not outlive its authority. A caller without `platform-admin` gets the existing skipped report.
- **Reinstate.** `POST /v1/admin/users/{userId}/reinstate` clears a hold and restores nothing else. It refuses a user under an §12.8 erasure, it does not revive a row removed through `DELETE`, and it refuses a caller reinstating itself with `403`. It is the only way back: `PUT /v1/admin/users/{userId}` refuses `disabled: false` on an invalidated row, where it would otherwise skip the self-reinstatement and ordering rules, and the bootstrap seed's force update refuses to reverse a disable. Upserts for unknown subjects are capped per tenant.
- **Ordering.** Both calls accept `observedAt`, and the hold stores min(`observedAt`, server time). A future stamp, whether malicious or from a fast worker clock, therefore cannot make a later call stale, and an emergency call at server time always applies. An absent value takes the server's time. A call whose value is older than its class's stored time answers `200` with `applied: false`, so a retried event cannot reverse a later one, and on a tie an invalidation wins. Both calls are idempotent.
- **Permission.** A new `manage_user_lifecycle` permission covers these calls alone. `tenant-admin` holds it, and a custom role can hold it alone, such as `identity-sync`. A worker authenticates per tenant, through the client credentials path of §10.2, with that role, and `service_claim` or `auth.serviceClaim` marks it as a service. A SCIM credential holds the same two calls for its own class.
- **`identityProvider` hardening.** `GET rbac-config` stops returning `introspectionClientSecret`. The write validator and the client refuse an `introspectionEndpoint` that resolves to the §13.2.5 excluded ranges, through the shared checker. The handler checks only for HTTPS and a host today, and the client uses a default dialer, while the gateway posts bearers to that URL.

**User credentials and links.** A session's `caller_type` now sits on its row, and a delegated child copies its parent's value, so a service or agent caller receives no user token from a read of that row. Each of `soft_disable`, `hard_disable`, and `full_revoke` stops a user's `userCredential` releases, through `pkg/binding`'s invalidation level, and it revokes `oauth2Link` tokens at the provider, under `user-git-identity-design.md`. A `registered` token stays valid at its provider, because Lenny cannot revoke it, and the tenant is told. Reinstate restores neither. A subject with an invalidation level or an Article 18 restriction cannot be written to through `PUT /v1/admin/users/{userId}/credentials/{type}`. `identityBinding: claim` adds a provider identity claim to the broker contract or to a customer source's mapping. An issuer that cannot supply it uses `directory`, whose write sits under `manage_user_lifecycle`, so the sync worker can hold it. A SCIM credential does not hold that write in the first version.

When a worker or a directory cannot reach Lenny, the event waits with it, and a user stays active until delivery succeeds. A partial failure reported in the invalidate response is retried, as the idempotent call allows. For a caller without `platform-admin`, §11.4 step 5 reports the impersonation cascade as skipped, which is correct for a customer user, and the operator invalidates its own staff with a `platform-admin` caller.

Offboarding latency is the directory's delivery time, plus the sync worker's on that path. Microsoft documents incremental cycles [at intervals defined per application](https://learn.microsoft.com/en-us/entra/identity/app-provisioning/how-provisioning-works) (provider documentation), with no general figure on that page. Until delivery, a user can create sessions while a token is valid. Lenny-issued tokens default to 900 seconds and are capped at 24 hours for `lenny-gateway` (§13.3 **`exp` granularity and parent-expiry handling.**), and a running session does not depend on its creator's token, so only invalidation ends it.

### What stays with an operator's portal

Lenny supplies admin APIs and no customer-facing identity UI, which §10.2 lists as a future goal. These remain the operator's: the page where a customer enters issuer details, instructions for registering an application at each IdP and for publishing the DNS record, sign-up and contract records behind the ticket, SAML bridging through a broker, support for a customer's IdP misconfiguration, and any directory connection other than SCIM.

### Failure behaviour (identity)

| Condition | Result |
| :-- | :-- |
| Unknown `iss` | `401 TOKEN_INVALID`, with the reason logged, and no fetch of that issuer |
| Customer IdP down, keys cached on a replica | That tenant's existing tokens verify on that replica, and fail on a replica that restarted. New logins fail at the IdP. Other tenants are unaffected. |
| Customer IdP down past maximum staleness | That source's tokens get `503`, and its alert fires. Lenny-issued tokens keep working until they expire. |
| The only source of a tenant is retired | Its users are refused, including with Lenny-issued tokens. It can return under **Reactivating**, or the deployment issuer can be allowed again under the activation rule. |
| A later re-resolution of an issuer lands in an excluded range | The source becomes `suspended`, its subjects are refused, and its alert fires. A `platform-admin` returns it to `active` once the address is fixed, with a ticket and no new approval, because the proof and approval still stand. |
| Registry snapshot cannot refresh | The last snapshot serves, and an alert fires past a staleness bound. A source retired during the outage stays trusted until refresh on that replica. |
| Row read fails | Fails closed on the gateway and the Token Service, as role resolution does |

### Who can do what (identity)

| Action | `platform-admin` | `security-admin` | `tenant-admin` | Customer IdP admin | SCIM credential or sync worker |
| :-- | :-- | :-- | :-- | :-- | :-- |
| Draft a source | Yes | Yes | Yes | No | No |
| Activate or change a source, or change `deploymentIssuerAllowed` | Yes, with a ticket, under the delay at a tenant with a `security-admin` row in any state | Approves or cancels. Activates alone only under `identity.allowTenantActivation`. | No | Completes the sign-in proof | No |
| Retire a source | With a ticket, and under the delay at a tenant with an active `security-admin` | At once | No | No | No |
| Use `invalidateSubjects` | Under the delay | With a second `security-admin`, or under the delay | No | No | No |
| Reactivate the same `source_id` | With a ticket recording the customer's request and a new proof | Approves, with the delay, when reachable | No | Completes the proof | No |
| Delete a `retired` row to release its issuer | Yes, under the delay at a tenant with a `security-admin` row in any state | No | No | No | No |
| Delete a `draft` row | Yes | Yes | Yes | No | No |
| Connect a customer IdP to the broker | Yes, as broker admin | No | No | Through the broker's portal, if offered | No |
| Create the tenant | Yes | No | No | No | No |
| Set tenant-scoped roles | Through mapping or row | No | Through row | Through its IdP's claim values, as `role_map` maps them | No |
| Grant `platform-admin` | Row, in the operator tenant alone | No | No | No | No |
| Mint a SCIM credential | Yes | Yes | With `manage_user_lifecycle` | No | No |
| Place and clear holds | Yes | Yes | Yes, and no self-reinstatement | No | Yes, own tenant, own class |
| Assign groups | Through broker | No | No | Directory | SCIM writes them |

## Configuration by scope

| Setting | Description | Scope | Managed by |
| :-- | :-- | :-- | :-- |
| `auth.oidc.issuerUrl` and `auth.oidc.clientId` | The deployment issuer and audience, which can be the operator's broker, and the default source of every tenant. | Deployment | Platform admin (Helm) |
| `identity.customerSources.enabled` | Whether a tenant can hold a customer source. | Deployment | Platform admin (Helm) |
| `identity.allowTenantActivation` | Whether a tenant's `security-admin` can activate a source with no operator ticket. | Deployment | Platform admin (Helm) |
| `identity.issuerCIDRs`, the source limit per tenant, maximum key staleness, the RSA key limit, and SCIM quotas | Issuer addresses for a strict egress list, the number of sources a tenant holds, how long cached keys stay trusted, the largest key accepted, and per-source SCIM counts. | Deployment | Platform admin (Helm) |
| `gateway.delegation.defaultMaxDepth` | The last-resort `maxDepth`, 10 by default and at least 1, rendered to the gateway. | Deployment | Platform admin (Helm) |
| `auth.tenantIdClaim` | The claim holding the tenant identifier on a deployment issuer's token. A customer source has none. | Deployment | Platform admin (Helm) |
| `auth.rolesClaim` and `auth.groupsClaim` (decision 10) | The claims holding roles and groups on a deployment issuer's token, defaulting to `roles` and `groups`. | Deployment | Platform admin (Helm) |
| Identity source (`issuer`, `audiences`, `client_id`, write-only `clientSecret`, captured endpoints, and `email_domains`) | The customer's issuer pinned to this tenant, and how logins reach it. | Tenant | Drafted by a tenant admin, security admin, or platform admin. Activated under "Activation rule". |
| Source mapping (`subject_claim`, `roles_claim`, `role_map`, `groups_claim`, `group_source`, and `service_claim`) | How that issuer's claims become a subject, tenant-scoped roles, and groups. | Source | As above |
| SCIM credential | The directory's bearer for one tenant and source. | Source | Tenant admin with `manage_user_lifecycle`, or security admin, with counts capped per source |
| `scim_deactivate_mode` and `match_claim` | What a deactivation does, and the immutable, verified claim that joins a directory user to a token. | Source, or the tenant's deployment settings row | As the source mapping |
| `auth.platformAdminTenant` | The one tenant whose rows can hold `platform-admin`. | Deployment | Platform admin (Helm) |
| `identity.deniedIssuers` | Shared issuers that cannot be a customer source. | Deployment | Platform admin (Helm), with a shipped list |
| `auth.serviceClaim` | The claim and value marking a deployment issuer's token as a service caller. | Deployment | Platform admin (Helm) |
| `deploymentIssuerAllowed` | Whether the tenant accepts the deployment issuer. Activating a customer source sets it false. | Tenant | Under the activation rule |
| Broker organization, IdP links, and claim mappers | The per-customer SSO configuration and the source of each contract claim. | Broker, per customer | Operator, or customer through a broker portal |
| Directory connection | The source of user and group changes for a customer, through Lenny's SCIM server, or through a broker or vendor and a sync worker. | Customer's directory | Customer IdP admin with the operator |
| `identityProvider` introspection settings | An RFC 7662 endpoint that supplies current groups for each request. | Tenant, and refused at a tenant with a customer source | Tenant admin (`rbac-config`), subject to SO decision 23 |
| `noEnvironmentPolicy` | Whether a user with no Environment can reach runtimes. Such a user's sessions have E = ∅. | Deployment default, tenant | Platform admin, tenant admin |
| Platform-managed role row | A user's role, which overrides the token's roles. | User in a tenant | Tenant admin, and platform admin for `platform-admin` |
| `identity-sync` custom role | A role holding `manage_user_lifecycle` alone, for a sync worker. | Tenant | Tenant admin or operator |
| Environment members | The groups and subjects with a role in an Environment. | Environment | Tenant admin |
| `environments` on a VCS credential pool | The Environments whose sessions can use the pool. Absent means tenant-wide. | Pool in a tenant | Tenant admin |
| `environments` on a credential binding | The Environments whose sessions can use the binding. Absent means tenant-wide. | Binding in a tenant | Tenant admin |
| `environments` on a user credential type or link provider | The Environments whose sessions can use it. Absent means tenant-wide. | Type or provider in a tenant | Tenant admin |
| `environmentPolicies[E].alwaysAllowed` | Hosts each session in E reaches by default, added to the tenant's. | Environment | Tenant admin |
| `environmentPolicies[E].sessionRequestable` | Hosts a session in E can request, added to the tenant's. | Environment | Tenant admin |
| `environmentPolicies[E].denied` | Hosts refused to sessions in E, added to the tenant's. | Environment | Tenant admin |
| `environmentPolicies[E].egressRoutes` and `gitRoutes` | Routes tried for sessions in E before the tenant's routes. | Environment | Tenant admin |
| `dangerouslyAllowCrossEnvironmentSharing` | Whether any Environment in the tenant can share runtimes across Environments. Off by default. | Tenant | Tenant admin, and a security admin can forbid it. Turning it on takes SO-2's delay at a tenant with a security record. |
| `dangerouslyShare.outbound` and `.inbound` | The named Environments and non-empty runtime selectors a sharing path allows. Both sides must declare it, and wildcard names are refused. | Environment | Tenant admin or a custom role with `manage_environments`, with an added entry taking SO-2's delay |
| `delegationLease.maxDepth` and `.maxChildrenTotal` | The session's depth limit, where 0 means it cannot delegate, and a per-parent child cap, where 0 is stored as `maxDepth: 0`. A client can only narrow. | Session | Client |
| Environment on a session | The Environment the session runs in, set at create and fixed thereafter, or the target under a sharing path. | Session | Client, subject to membership or to a declared path |

Every setting in the "Scopes and who manages them" table of `egress-and-git-design.md` that this table does not name stays as written there.

## Scenarios

**Two teams in one customer.** Tenant `acme` has Environments `web` and `payments`, with members from the groups `acme-web` and `acme-payments`. Pool `web-github` covers `github.com/acme-web/` for `web`, and pool `pay-github` covers `github.com/acme-pay/` for `payments`. The tenant allows `npm`. `payments` adds `stripe-api` and denies `huggingface.co`.

- Alice, in `web`, creates a session through `/v1/environments/web/sessions` that clones `acme-web/site`, and it succeeds.
- A plan from Alice naming `acme-pay/ledger` is rejected with the same answer as a repository outside every pool.
- An agent in Alice's session runs `git fetch https://github.com/acme-pay/ledger`. The scope check refuses it, nothing is minted, and `git` gets 403.
- Bob, in `payments`, reaches `api.stripe.com`, and Alice's session gets 403 for the same host.

**A session on the transparent path.** Alice calls `POST /v1/sessions` with no Environment, on a tenant whose `web` runtime she can see, and `web` has no denied or route entries. The session has E = ∅ and reaches `npm`. A plan naming `web-github` fails. The error message tells a caller to use an Environment path for Environment objects, without naming any object. Bob makes the same call for a runtime `payments` selects, and it fails with `ENVIRONMENT_REQUIRED`, whether or not his token carries groups, so he cannot shed the deny on `huggingface.co` by omitting the Environment.

**Two teams and one GitHub organization.** Pools `web-app` and `pay-app` both cover `github.com/acme/`, with separate GitHub Apps and allowlists, for `web` and `payments` respectively. A third, tenant-wide pool with that prefix is refused at write. GitHub enforces each App's own repository selection, so the teams' tokens differ.

**A team's cloud role.** Binding `payments-aws` has delivery `federation`, and it lists `payments`. The subject `lenny:acme:payments-aws` is stable. Acme's AWS trust policy names that subject, so only a session able to obtain this binding reaches the role. A session in `web` gets no token from I8.

**A shared pod on a recycling pool.** Pool `coding` has `recycle.enabled: true`. Alice's `web` session ends, `netagent` flushes its credentials, and Bob's `payments` session claims the pod. Bob's binding set and policy are correct, and any file state the scrub does not reach survives from Alice's session.

**A concurrent pool.** Pool `chat` serves concurrent sessions. Bob's create in `payments` fails with `ENVIRONMENT_POLICY_NOT_ENFORCEABLE`, because the pool cannot apply `payments`' deny or its route to `acme-pci-swg` per session. His retry through `POST /v1/sessions` fails with `ENVIRONMENT_REQUIRED`. Alice's create in `web` proceeds with unscoped objects, and a plan from her naming `acme-web/site`, whose only pool is scoped, is refused, so no slot on the pod can reach that repository through the union.

**A delegated child.** Alice's `web` session delegates to a runtime that `web` selects, and the child has E = `web` with her session's inputs, as does each further hop. A target `web` neither selects nor shares answers `TARGET_NOT_IN_SCOPE`, whether Alice's group gives her membership or not. A `payments` child placed on `chat` is refused like a create.

**A shared review runtime.** Tenant `acme` turns on `dangerouslyAllowCrossEnvironmentSharing`. `web` declares `shared-review` outbound to `platform`, and `platform` declares it inbound from `web`. Alice, who is not a member of `platform`, has a `web` session delegate to it. The child has E = `platform`, `crossed_from_environment: web`, and `platform`'s hosts, routes, and bindings, and it bills to `platform`. It reads a `platform` repository on a pool with `userAuthorization: off`, and a `required` pool refuses it unless Alice has access. Its results return to her session, and it can send what `web` handed it to any host `platform` allows, including one `web` denies. When it delegates to `other-tool`, which `platform` shares with `tools`, the gate answers `TARGET_NOT_IN_SCOPE`. `credential.mint` and the audit event name both `web` and `platform`.

**An experiment variant.** A running experiment sends 10 percent of a runtime's sessions to a variant on pool `chat`. Bob's create in `payments` passes admission against the base runtime, and the check on the final row refuses it, since `chat` cannot apply `payments`' deny. A variant whose runtime `web`'s selector does not admit is refused in the same way for Alice.

**Onboarding a customer on the broker.** The operator creates tenant `globex`, a broker organization whose claim maps to `globex`, and a link to Globex's IdP. Alice, a Globex administrator, logs in. The broker issues `roles: [tenant-admin]` under a mapping the operator set. Alice creates Environments with Globex's group names.

**A tenant on its own issuer, beside a tenant on the broker.** The operator creates tenant `acme` with a customer source for Acme's Entra tenant, with `subject_claim: oid`, `role_map` sending Acme's `lenny-admins` value to `tenant-admin`, and `group_source: scim`. The sign-in proof previews Alice's subject as `~k3f9~<oid>`, and the operator activates the source against a ticket. Alice's MCP client uses `/t/acme/mcp`, whose metadata names Acme's issuer alone. `globex`, in the same deployment, stays on the broker and is unchanged.

- A broker token with `tenant_id: acme` gets `401 TENANT_ISSUER_MISMATCH`.
- A token from Acme's issuer carrying `tenant_id: globex` resolves to `acme`, because the claim is ignored.
- A token with an unknown `iss` is refused with no fetch.

**A customer's IdP asserts a platform role.** A token from Acme's issuer carries `roles: [platform-admin]`. `role_map` has no such entry, so it grants nothing, and the boundary records the drop. A `platform-admin` trying to grant Alice the role by row is refused, because `acme` is not the operator tenant.

**A stale claim against SCIM.** Bob's Entra group changes, and Acme's directory removes him from `payments-engineers` through SCIM. His token still carries that group. Under `group_source: scim`, his next `/v1/environments/payments/sessions` call gets `403`, because the claim is ignored. His running `payments` session continues. `ENVIRONMENT_REQUIRED` answers as it did before, since it reads no groups. If Acme's directory then deactivates him, his next request gets `401 USER_INVALIDATED`, whatever his token says, and his sessions end under `full_revoke`.

**A squatted issuer.** A trial tenant drafts a source naming Globex's Entra URI. The draft holds no index, so Globex can still register. The trial tenant fails the sign-in proof, since it holds no client there. Were it to pass, it would still need the operator's attestation, or under `identity.allowTenantActivation` its own `security-admin`'s approval, so that option carries the larger squatting risk. A `platform-admin` then retires the wrong row and deletes it to release the index. If the squatter's `security-admin` cancels the retirement, the operator invalidates that principal first.

**A mix-up attempt.** Tenant `evil` runs a source on its own IdP, and its admin sends Bob, an operator staff member, a proof link. Bob's browser reaches evil's authorization endpoint, which redirects to the broker with the deployment `client_id` and redirect URI. The broker returns a code to `/auth/callback/deployment`. The state cookie is scoped to evil's callback path, so it is absent there, and Lenny refuses with no token request. The code never reaches evil. How a given broker handles that redirect is untested.

**A compromised source is retired.** Acme's IdP is compromised, and the attacker asserts `tenant-admin`, mints a SCIM credential, and opens a playground session. A `security-admin` requests `invalidateSubjects: true`, a second one confirms it, and the first retires the source. Tokens from that issuer stop, Lenny-issued tokens of its subjects are refused, the credential and the playground record are revoked, and a job ends those subjects' sessions and revokes their links. Broker tokens stay refused, because `deploymentIssuerAllowed` stays false. After Acme rotates its key, the source returns to `active` with the same `source_id` on a new proof and a ticket, and tokens issued before retirement stay refused through `tokens_not_before`. The preflight lists role rows and credentials the attacker's subjects wrote, and an admin clears the job's holds so Acme's users return.

**An incident hold and the directory.** An admin invalidates Carol during an incident, placing an `admin` hold. Acme's directory then sends `active: false`, then `active: true`. The `scim` hold comes and goes, the `admin` hold stays, and SCIM reports `active: false`. A sync worker's `reinstate` behaves the same way.

**Activation with a security team.** Globex, on the broker, has a `security-admin` on a bare subject. A request to activate its own IdP stops at the preflight, which lists that principal as stranded. The `security-admin` assigns a new principal on the preview's canonical subject, which takes SO-2's delay, and approves. The preflight then passes, and the old row stays behind, unreachable, as "Residual risks" states.

**A departure.** Bob leaves Globex. He has logged in but has no user row. Globex's directory deactivates him, the broker receives the SCIM change, and the sync worker calls `invalidate` with `mode: full_revoke`, as `identity-sync` in tenant `globex`. Lenny creates the row, ends his sessions, and revokes his issued tokens. A later retry is idempotent. If Bob returns, the worker calls `reinstate`. A tenant whose directory calls Lenny's SCIM directly gets the same result with no worker.

**An invalidated administrator.** Carol, a `tenant-admin` at Globex, is invalidated with `full_revoke` while holding a valid IdP token. Her next admin call gets `401 USER_INVALIDATED`. She cannot call `reinstate`, and a stale `observedAt` from any caller cannot reverse a newer invalidation. Another `tenant-admin` can reinstate her.

**A mapping error.** A broker change lets Acme's mapping reach `roles`, and a token arrives with `roles: [platform-admin]`. The gateway removes the role, the caller acts with no platform authority, and the alert fires. A separate error placing `tenant_id: globex` on Acme's token is not caught, which "Residual risks" states.

## Review of revision 9

Round 5 of the reviews raised the route assertion under `boundRoutes(E)`. It holds, and the row states it. None was rejected.

## Review of revision 8

Round 4 of the reviews raised M1, the route value's scope and order, M2, the covered set under Environments, m1, the stale heading, and m21, a stale request. Each holds, and the rows point to the other designs' rules. None was rejected.

## Review of revision 7

Round 3 of the reviews raised H1, routes per Environment against one digest, and m3, the "existing" column. Both hold. The route row points to the record's route value and states its cost, and the column text is corrected. None was rejected.

## Review of revision 6

Round 2 of the reviews raised N5, the attach trigger, and N13, the pin's reach against `visible`, on this document. Both hold, and the text above points to the attach rule and states the pin's source. None was rejected.

## Review of revision 5

Two independent reviews of the user credential designs raised these on this document, each verified against the text.

| Finding | Outcome | Resolution |
| :-- | :-- | :-- |
| "It takes `onRequest` alone" against the pool's implicit attach | Holds | `poolAttach`, defined once in `user-git-identity-design.md`. |
| The link's key | Holds | Keyed on the provider's name. |

None was rejected.

## Residual risks

- **Environment scoping shares pods.** On a recycling pool, state the scrub does not reach survives into another Environment's session within the tenant, as it does between users today. A team needing separation uses a pool with `recycle.enabled: false`, or a separate tenant (decision 5).
- **Shared tenant resources.** The mirror's store for a pool used by several Environments, the Token Service token cache, egress group, and P7 quotas are shared across Environments. One team can consume the tenant's quota (decision 6). One team's egress address is the same as another's, so a customer firewall rule admits both.
- **Tenant-wide objects stay tenant-wide.** A team cannot opt out of a tenant-wide binding or host. It can deny a host. The tenant admin must scope a binding to remove it from a team. Scoping a pool also leaves a repository reachable through any wider tenant-wide pool whose allowlist names it, so the write response warns when a scoped pool's repository appears on such an allowlist.
- **A person's token across teams.** A link has no Environment, so a member of two teams has one token. Under `oauth2Link` with no narrowing template, a session in either team holds that token's full reach, whichever team's repositories it was meant for.
- **Policy added after a session began.** A deny or a route added to an Environment reaches its one-session pods at the next decision. A transparent session created before the entry existed keeps E = ∅, and a session already running on a concurrent pod keeps running until its next resume, as the write's `Warning` reports.
- **Cross-Environment sharing.** On a declared path, an injected caller can hand data to a target runtime, which can send it anywhere the target allows, skipping the caller's denies and proxy. That data includes anything the agent can read, such as a `file` token with no `lockEgressToHosts`. A non-member's agent can drive the target's credentials and receive results, on any pool with no user check. The owner accepted both under decision 14. A `tenant-admin` writes both sides of a path, so its two sides do not separate duties.
- **Label edits on a sharing path.** A label edit through `PUT /v1/admin/runtimes/{name}`, or a change to T's selector, adds a runtime to a declared path at once, with no delay. Posture lists paths and reports after the fact.
- **Forged adapter calls.** `lenny/delegate_task`'s parent rule trusts the session the adapter presents, so a compromised adapter can still name another session until F-10.2.29 closes.
- **Per-parent child cap.** A positive `maxChildrenTotal` caps each parent's children and does not bound a tree.
- **Shared runtimes run with the caller's objects.** This applies to a runtime several selectors admit, with no sharing path. A runtime that several Environments select runs under each session's own E, so that team's scoped credentials and repositories reach the shared runtime's code. A tenant admin decides this when writing the selector, and no inbound consent exists on the side that owns the runtime.
- **Members of several teams.** A caller in both `web` and `payments` can create in `web` and avoid `payments`' deny. A deny binds a team's sessions and does not bind a person. Under `allow-all`, a token with no groups can likewise run E = ∅ on a runtime that no Environment with denied or route entries selects.
- **Invalidation during a Postgres outage.** `lenny-ops` falls back to token roles, as above.
- **Mirror assertions.** The Token Service verifies the cause a mirror fill names, and a compromised mirror can still name any live cause in a tenant whose route names it. This is the same limit as its tenant assertion.
- **Membership at create.** A user removed from a group keeps running sessions, and those sessions keep E's objects, until the session ends or the user is invalidated. Removal from an Environment alone has no session effect.
- **Exfiltration between teams.** A session in `web` with a host open can send data anywhere that host accepts. Scoping limits credentials and destinations. It does not stop a user who belongs to two teams from moving data between sessions.
- **Changing a tenant's issuer.** Each issuer has its own subjects. If an operator points an existing tenant at a different issuer, Lenny treats the new subjects as new users, and data keyed on the old ones (sessions, user rows, links, and registered tokens) does not carry over. Lenny provides no migration. That includes security principal rows and `lifecycleSubjects` on old subjects, which the activation preflight lists, and a new subject cannot re-link a provider account an old one holds, because `user_links` is unique on `provider_user_id`.
- **Broker correctness.** For a tenant on the deployment source, the tenant boundary rests on one claim from one issuer. A broker fault or a compromised broker can issue a token for any such tenant, and a second claim from the same issuer adds nothing. A broker that authenticates the operator tenant can also assert a staff subject, which reaches platform authority and, through impersonation, every tenant, including pinned ones (TP5).
- **An issuer's principals.** An issuer controls every principal it authenticates, including a `security-approver`, whatever `securityRoleSource` says. On a tenant on the broker, an operator can therefore act as that approver through its broker. Posture reports the dependency, and decision 25 leaves a second factor out.
- **A customer's IdP.** A compromised or misconfigured customer IdP can assert any subject, mapped role, or token group in its own tenant, including `tenant-admin` through `role_map`. Under `managed`, `security-*` claims stay ignored, but it can assert a principal's subject, as above. It cannot reach another tenant or a platform role.
- **Hosted issuers.** No mechanical proof binds a customer to an issuer on a shared host, so a pin rests on the sign-in proof, the domain proof, and the operator's attestation. The first activation holds the index until a `platform-admin` resolves a dispute, and `identity.allowTenantActivation` removes the operator's check from that path. The denied list is known shared issuers and cannot be complete.
- **One issuer per tenant across the deployment.** A customer with two Lenny tenants, or an issuer shared by many customers such as a consumer provider, cannot use customer sources and stays behind a broker (decision 18).
- **Operator reach into a pinned tenant.** A `platform-admin` keeps impersonation and can activate a source. At a tenant with no `security-admin` row in any state, nothing delays that, and SO-4 is what makes it visible. Where the delay applies, a stolen `platform-admin` can first invalidate the principals who could cancel and write its own tickets, so the delay, the SIEM event, and any outage are what remain to warn the customer.
- **Unchanged reactivation.** With no reachable principal, a stolen `platform-admin` with a login at a compromised issuer can undo a `security-admin`'s emergency retirement at once, by returning the row unchanged. This has no delay, because retirement has to stay reversible during an incident. `tokens_not_before` still refuses older tokens.
- **Cached keys.** An IdP rotating out a compromised key while unreachable stays trusted up to the maximum staleness.
- **A stolen `tenant-admin`.** It can mint a SCIM credential, which posture lists, and it can write role rows and custom roles. On a deployment-source tenant with no security principal, it can also set `introspectionEndpoint` and capture bearers, which SO decision 23 governs.
- **A stolen SCIM credential.** It can deactivate any unprotected user in its tenant, within its quotas, and under `group_source: scim` it can change group membership, and so Environment membership. It cannot authenticate as anyone, assign a role, or clear another class's hold.
- **Egress for issuers.** The gateway's external rule and the Token Service's are wide, and each customer issuer depends on them. A strict deployer maintains addresses per customer.
- **Published metadata.** A per-tenant metadata document discloses a tenant identifier and its IdP host, and the typed-email page discloses which domains are customers.
- **Declared claims.** `match_claim` is immutable and verified because the source says so. Lenny cannot check that, and a wrong declaration brings back takeover through a directory row.
- **Ongoing proof.** Discovery and domains are re-read, and the sign-in proof and attestation run once.
- **Broker availability.** A broker outage stops new logins and token refresh for all tenants on it. Verification continues on cached keys, and running sessions continue. A customer IdP's outage does the same for its tenant alone.
- **Running sessions on unbound rows.** A user on the deployment source whose directory row never bound has no subject to hold, so `active: false` leaves that user's running sessions up until the next IdP token matches. A Lenny-issued bearer carries no `match_claim` either, so it also waits for that. The SCIM response and an alert report the case, and R6 holds only in part here.
- **Lost issuer.** A customer that has permanently lost its issuer cannot approve or cancel its own recovery, and the operator acts under a delay nobody can cancel.
- **Stale snapshots.** A replica with a stale snapshot can mint a Lenny token after retirement with an `iat` that passes `tokens_not_before`, and it survives a later reactivation until it expires.
- **Rotated keys.** `tokens_not_before` cannot stop a compromised key that was not rotated, because such a key chooses its own `iat`.
- **Stale group data.** Under `group_source: token` with no introspection, group changes wait for a new token. With introspection, a failure of the tenant's endpoint fails its requests closed with `503`. Under `scim`, changes wait for the directory's delivery and the 30-second cache, and a store failure answers `503`.
- **Lifecycle delivery.** Lenny cannot detect a lost event. The directory or the operator's worker owns retry, and a user stays active during an outage of either side.
- **Unproven.** Nothing in the identity design has run. Open questions include how MCP clients behave against per-tenant metadata and audiences, whether Entra's, Okta's, and other directories' SCIM traffic matches the corpus, the 24-hour staleness value, provider behaviour on the browser login path including silent single sign-on, the CPU cost of oversized keys, the dial-time address check under production resolvers, group read latency on the auth path, and memory for per-issuer verifiers across many tenants and replicas.
- **Secret in `rbac-config`.** See "Defects found while verifying".

## Defects found while verifying

These exist independent of this design. Each is filed in `BUILD-GAPS.md` under the identifier the row names, and the row names the proposal that closes it.

| Severity (proposed) | Defect | Closed by |
| :-- | :-- | :-- |
| High | F-10.2.38: an IdP-verified token's `roles` claim can assert `platform-admin` (`normalizeUserBearer` clears `typ`, `session_id`, `delegation_depth`, and `origin` only), and §10.2 **Role assignment:** permits it. | TS-2 |
| High | F-11.4.19: `invalidate` answers `404` for a user with no row, so a user known only through an IdP cannot be stopped, against §11.4's incident-response purpose. | TS-4 |
| High | F-11.4.20: `serveBearer` never reads a user's invalidation state, so an invalidated administrator with a live IdP token keeps the admin API. | TS-4 |
| High | F-10.6.16: in-pod `lenny/delegate_task` resolves Environments from a principal with no groups, so group members cannot delegate under `deny-all`, and `allow-all` admits every runtime regardless of selectors. | TS-3 |
| Medium | F-10.6.17: experiment routing and the late `PreRoute` segment rewrite a session's runtime after §10.6 admission, so the explicit selector check does not cover the runtime that runs. | TS-3 |
| Medium | F-11.4.21: `PUT disabled:false` reverses a disable with no check. F-11.4.20 covers a `DELETE`d user that keeps its assigned role. `soft_disable` and `hard_disable` leave impersonation bearers live, which §13.3 rule 6 states as today's behaviour, so TS-4 changes that part and it is not filed. | TS-4 |
| Medium | F-10.6.18: tokens from `/v1/oauth/token` carry no groups, so they cannot satisfy an `oidc-group` Environment member. | TS-3 |
| Medium | F-10.6.15: delegated, derived, and replayed sessions have no Environment, so their billing rollup, retention tier, and credential scope ignore it. | TS-3 |
| Medium | F-10.6.19: the transparent create path admits any member of any Environment, with no role or selector check. | TS-3 |
| Medium | F-10.6.20: Environment deletion has no active-session check, against §15.1. | TS-3 |
| Medium | F-10.6.21: `GET /v1/admin/tenants/{id}/rbac-config` returns `introspectionClientSecret`, which the tenant row stores in its JSON configuration with no redaction. | TS-4 |
| Medium | F-10.6.22: `introspectionEndpoint` has no address check, and the gateway posts bearers to it. | TS-4 |
| Medium | F-10.2.39: the gateway serves no RFC 9728 metadata and no `WWW-Authenticate` challenge, against §10.2's statement that it is an MCP-standard protected resource server. | TS-2 |
| Medium | F-11.4.21: no endpoint reverses `hard_disable` or `full_revoke`. | TS-4 |
| Low | F-10.2.40: §10.2 and `docs/client-guide/authentication.md` name a `lenny_role` claim, and the code reads `roles`. `docs/tutorials/multi-tenant-setup.md` and `docs/operator-guide/security.md` show `auth.oidc.issuer`, `tenantClaim`, `roleClaim`, `jwksUri`, `audience`, and `auth.issuerUrl`, none of which the chart defines. | TS-2 |
| Low | F-15.1.74: `/v1/admin/users` exists in code and is absent from the §15.1 endpoint table. | TS-2 |

Reading the tree for revisions 13 and 14 found these more, and the last row came from a separate read. They exist today with a single issuer, and each is filed in `BUILD-GAPS.md` under the identifier the row names.

| Severity (proposed) | Defect | Evidence | Closed by |
| :-- | :-- | :-- | :-- |
| Medium | F-10.2.41: an IdP-verified token keeps Lenny's internal claims, and a non-Lenny `scope` refuses the token. `caller_type`, `act`, `authorized_tools`, and `scope` survive on a signed token, and `serveBearer` copies `CallerType`, `Scope`, and `Act` to the principal. A token with `scope: "openid profile"` gets `401 TOKEN_INVALID`. A claim spelled `TENANT_ID` can fill `tenant_id` and `ROLES` can fill `Roles`, because Go's struct decoding also matches other cases, which an in-process check confirmed. A custom `auth.tenantIdClaim` is read by exact key, so the tenant case arises under the default name. | `normalizeUserBearer` in `pkg/auth/jwt/oidc.go`, `Claims` and `decodeClaimsJSON` in `pkg/auth/jwt/jwt.go`, `serveBearer` in `pkg/gateway/middleware/auth/auth.go`, `Parse` and `ParseScope` in `pkg/common/scopes/scopes.go` | TS-2 |
| Medium, filed Low with one operator-configured issuer | F-10.2.42: the JWKS parser has no upper bound on RSA size and no limit on `e`, verification runs before the rate limit, and the gateway sets no `MaxHeaderBytes`. The CPU cost is an unmeasured estimate. | `oidcMinRSABits` and `publicKey` in `pkg/auth/jwt/oidc.go`, no `MaxHeaderBytes` in `cmd/` or `pkg/`, and the order in `cmd/lenny-gateway/httpsurface.go` | TS-2 |
| Minor | F-13.2.25 already holds the first half, and F-13.2.30 holds the local default's open rule. In production mode or with any cloud KMS, an external issuer's caller tokens fail at `/v1/oauth/token` under an enforcing CNI, while Lenny-issued callers work. `allow-token-service` has no external HTTPS rule apart from `kms.endpointCIDR`, which the chart requires to be narrow in that mode. Under the local provider's default, that row opens TCP 443 to everything, including private ranges and IMDS, with no excepts. The Token Service should use `lenny.kmsEgressRule`, as `allow-gateway-egress` and `lenny-ops-egress` do. | `allow-token-service` in `charts/lenny/templates/system-network-policies.yaml`, `lenny.kmsEgressRule` in `_helpers.tpl`, `kms.endpointCIDR` in `values.yaml`, `buildCallerVerifier` in `cmd/lenny-token-service/caller_verifier.go`, and §13.2.4's Token Service row | TS-6 |
| Medium, filed Low with one operator-configured issuer | F-27.3.17: the playground's token request follows redirects. The rest of this row becomes a defect only under per-tenant sources. It posts the code and verifier before checking the ID token, on one callback path, and tenant and user come from raw claims. With one issuer the harm is limited, and the code is what TS-6 replaces. | `Exchange` and `extractSubject` in `pkg/gateway/mcpfabric/playground/oidc.go`, `buildPlaygroundOIDC` in `cmd/lenny-gateway/playground_oidc.go` | TS-6 |
| High | F-8.2.34: a client's `delegationLease` at session creation has no effect. It is validated, stored, and echoed, and delegation reads the Helm fallback, and zero reads as unset, so `maxChildrenTotal: 0` would mean no limit if connected as it stands. | `DelegationLeaseRequest` references, `checkDelegationDepth` in `pkg/gateway/mcpfabric/delegation/service.go`, `pkg/delegation/lease/lease.go`, and §8.2 **Effective `maxDepth` resolution** | TS-3 |

Revisions 15 and 16 found more in today's tree, and each is filed in `BUILD-GAPS.md`:

- F-8.2.35 (High, TS-3): a runtime can name another session of the same owner as delegation parent.
- F-9.1.13 (High, TS-3): the intra-pod tool surface forwards every registered tool, so a runtime can create a root session.
- F-27.3.18 (High, TS-7): the playground session cookie's path excludes the mint at `/v1/playground/token`.
- F-13.3.27 (Medium, TS-2): a playground-origin `subject_token` loses `origin` on exchange, and `lenny-ops` accepts the result.
- F-10.6.23 (Low, TS-3): `runtimeAuthorizedForCaller` returns true on a `Runtimes.Get` error.
- F-10.6.24 (Low, TS-3): an empty `runtimes` selector on a cross-environment declaration admits every runtime.
- Folded into F-8.2.34: `CreateAndStartRequest` drops `delegationLease`, `BindSingleShot` rows hold a nil lease, `CheckDepth` refuses 0 with `ErrUnresolvedMaxDepth`, and `buildLineage` ties its walk to Helm.
- Folded into F-11.4.21: the bootstrap seed's force update reverses a disable.

`lenny/await_children` also admits any session of the owner through `in.SessionID`, which is folded into F-8.2.35. F-8.3.12 (Medium, TS-3): `gateway.delegation.defaultMaxDepth` is not a chart value, so nothing renders it to the gateway or validates it as at least 1.

## Changes requested of egress-and-git-design.md

Revision 55 of `egress-and-git-design.md` carries the earlier requests: the `environment` and `crossedFromEnvironment` envelope fields, the `pkg/binding` resolver hook, the prefix overlap rule, the delegated child rule, and the TS-1 dependency row. No hook remains from the retired bound.

| Request | Proposal | Why now |
| :-- | :-- | :-- |
| Largely applied: the mirror's I8 request names the pool and its cause, and the mirror prefetch resource accepts a pool name. Still open: the Token Service reads the cause itself. | EG-5 and EG-2 | I8 is a wire contract and the mirror is a shipped enforcement point. Without this the Token Service has no independent Environment check on a mirror fill, and same-prefix pools have no answer for a prefetch. Naming the pool is correct with no Environment involved, because a store's identity already includes the credential. |

The `environments` fields, `environmentPolicies`, the effective-policy parameter, and the binding host validator are additive and need nothing else from that design. `netagent` needs no change, because it receives resulting sets. Its credential cache also needs none, for the reason under "Enforcement points". On a pod with several sessions, the resolver receives E = ∅ from TS-1's own rule, which leaves the union in "Git on covered hosts" as written.

## Changes in other design documents

Revisions 13 to 18 made these edits where a sentence had become false, and changed revisions as stated.

| Document | Edit | No edit needed |
| :-- | :-- | :-- |
| `security-oversight-design.md`, revision 17 | "Who supplies" text, IdP removal under SCIM, the TS-4 and TS-2 dependency rows, decision 2's wording, the issuer's power over principals, a stranded principal, a lost issuer, a principal's groups, the `crossEnvironmentSharing` guardrail with its weakening and posture entries, adding a `dangerouslyShare` entry as a weakening, the roles claim in place of `lenny_role`, decision 19 settled, activation under a `security-admin` row in any state, and TS-3's dependency row | Decision 23 stays as written, and TS-6 applies its rule to the new record. `securityRoleSource` holds for every issuer. Security principals key on the canonical subject. |
| `user-git-identity-design.md`, revision 17 | The start page's login through the shared resolver, the `claim` binding, the §10.2 note, decision 37's cost, UG-1's unconditional dependency on TS-6, the cross-Environment rule, and step 5's refusal | `allowedAccounts` reads the provider and does not change under a tenant's own issuer. `identityBinding: directory` keys on Lenny's subject, and `verifiedEmail` needs the source to emit an authoritative `email`. |
| `user-credential-delivery-design.md`, revision 14 | Its pointers, the sentence on a delegation bound, and the delegation paragraph | The user key is `(tenant_id, subject)`, and a customer source's subject is `~<source_id>~<value>`, with no sentence false. |
| `forensics-evidence-preservation-design.md`, revision 17 | Its pointers, and the withdrawal of the `security-auditor` default under SO decision 19, in the read table, the SO-1 row, and decision 5 | A session's owner is still found on its row, and no sentence on records is false. |
| `tenant-outbound-connector-design.md`, revision 17 | Its pointers | None. |
| `egress-and-git-design.md`, revision 55 | The delegated child rule and the resolver hook, which named the retired intersection and bound, the `crossedFromEnvironment` envelope field and hook, UG-1's row, and the note that TS-3's list carries `lenny/vcs_token` until EG-3 | Its Token Service rule for provider APIs is what TS-6 reuses, and it does not need to name issuers. |

## Specification sections affected

- §2 Goals and Non-Goals: the identity model, and what stays with an operator.
- §4.3: the Environment that a child and a derived session use for credential scope.
- §4.9: `environments` on VCS credential pools and credential bindings, and the overlap rule.
- §7.3: the refusal at resume on a pod with no per-session policy.
- §8.2: "Every effective delegation lease MUST carry a positive integer `maxDepth`" allows 0 as an explicit client or parent value, the **Effective `maxDepth` resolution** order becomes a minimum, `maxChildrenTotal: 0` is stored as `maxDepth: 0` and a positive value is a per-parent cap, the parent is the calling session, depth comes from the parent's row, the Environment a delegated child takes, refusal at placement, and no second crossing.
- §8.3: `inherit`'s check runs under `dangerouslyShare`.
- §8.10: the stored `maxDepth` on the lease record.
- §9.1: the **Platform MCP Server Tools** table becomes the intra-pod allow-list, with `lenny/vcs_token` until EG-3, and `lenny/delegate_task` is hidden per session where the surface allows.
- §14: `delegationLease` semantics, including 0 and the create-and-start request, and that `delegationPolicyRef` has no effect.
- §25.1: an IdP token's `scope` is filtered to `tools:` values at the boundary, and `caller_type: "agent"` comes from the source's mapping.
- §10.7: the checks that run on the final row after experiment routing.
- §10.2: the identity model paragraph, the source record, the verifier registry, `tenant_id` extraction by source, canonical subjects, the broker contract, the discovery routes, the claim allowlist, RSA bounds, the operator tenant, **Role assignment:**, the IdP role boundary, refusal of invalidated principals, the `manage_user_lifecycle` row of the permission matrix, and claim names.
- §10.3: the configuration validation for `identity.*` values, and `auth.oidc.issuerUrl` as the default source.
- §13.2.4 and §13.2.5: the Token Service's use of `lenny.kmsEgressRule` and its external rule, and the shared address checker with its added ranges.
- §13.3: the claim allowlist's effect on exchanged tokens.
- §27.3.1: per-source callbacks, the shared resolver, the refusal before a token request, per-tenant cookies, and the mint under the prefix.
- §13.3: refusal of a playground-origin `subject_token`.
- §25.4: `lenny-ops` keeps one issuer.
- §10.6: scoped objects, session Environment rules, the session-based delegation gate, `dangerouslyShare` replacing `crossEnvironmentDelegation`, refusal of wildcard names against the example showing `sourceEnvironment: "*"`, the tenant setting, that a crossing child is in the target and uses its connectors, and what its policy is, the `identityProvider` description, namespaced group members, `group_source`, and **V1 data model accommodations**.
- §11.4: invalidation of a subject with no row, the effect of each mode on authentication, holds per caller class, reinstatement, event ordering, and SCIM as a caller.
- §13.3: groups on exchanged tokens, and the rule 6 cascade on `soft_disable` and `hard_disable`.
- §11.7: both Environments on events for a crossing child, and the `auth.platform_role_dropped`, reinstatement, identity source, and SCIM credential audit events.
- §12 and §12.8: the source, directory, and SCIM credential tables, with their erasure.
- §12.9: classification of the upserted user row, the source record, directory rows, and SCIM credentials.
- §14: plan validation against the Environment.
- §15.1: the Environment body field, the new fields and error codes, `/v1/admin/users`, `reinstate`, `identity-sources`, the `/t/{tenantId}` prefix, the SCIM surface, the Environment delete guard, and the `environment` filter on evidence reads.
- §16: the dropped-role counter and alert, the refusal counter, per-source key staleness, registry staleness, and SCIM request and failure metrics.
- §17.6: `auth.rolesClaim`, `auth.groupsClaim`, `identity.*`, and `gateway.delegation.defaultMaxDepth`.
- §15.1: `DELEGATION_DISABLED`, `PARENT_SESSION_MISMATCH`, the `dangerouslyShare` field, `CROSS_ENVIRONMENT_SHARING_DISABLED`, and the access report's paths.
- §18.35: the Phase 15 exit criteria, for the opt-ins and the session-based gate.
- §12: `sessions.crossed_from_environment`.
- §24: `lenny-ctl` commands for reinstatement and for scoped fields.

The sections that `egress-and-git-design.md` lists change first, and the TS-1 edits apply on top of the text those proposals land.

## Proposals and dependencies

| Label | Proposal | Contents |
| :-- | :-- | :-- |
| TS-1 | Environment scoping | The fields, the formulas, enforcement in the gateway, the decision service, the Token Service, and the mirror, the refusal rules including checks after routing, the binding host validator, the delete guard for references, shadow policy evaluation, and the read filters. |
| TS-2 | Identity model and platform boundary | The specification statement, the broker contract, the deployment protected resource metadata route, the claim allowlist, RSA and token size bounds, the IdP role boundary, the operator tenant, the playground-origin `subject_token` refusal, claim names, and the §15.1 listing of `/v1/admin/users`. Closes F-10.2.38, F-10.2.39, F-10.2.40, F-13.3.27, and F-15.1.74. |
| TS-3 | Session Environment context | Derive, replay, and delegation propagation, the session-based delegation gate, cross-Environment sharing with its opt-ins, the check, no second crossing, both Environments on records, the client delegation limits with one reading of `maxDepth` and a stored value on every root creator, depth from the parent's row, the calling-session parent rule, the Bridge allow-list, and `DELEGATION_DISABLED`, groups on exchanged tokens, the active-session delete guard, the transparent create check, and the naming correction. Closes F-8.2.34 apart from `delegationPolicyRef`, F-8.2.35, F-8.3.12, F-9.1.13, F-10.6.15, F-10.6.16, F-10.6.17, F-10.6.18, F-10.6.19, F-10.6.20, F-10.6.23, and F-10.6.24. |
| TS-4 | User lifecycle path | Upsert on invalidate, holds per caller class, refusal at authentication including deleted rows, the impersonation cascade on disables, reinstate as the only reversal, ordering, the permission, and `identityProvider` hardening. Closes F-11.4.19, F-11.4.20, and F-11.4.21, and the read half of F-10.6.21 and the address check of F-10.6.22. |
| TS-5 | Delegated team administration | Environment `admin` writes to its own Environment's scoped objects, if decision 7 adopts it. |
| TS-6 | Identity sources and the verifier registry | The record and its state machine, including `suspended` and reactivation, the activation rule and preflight, retirement with `tokens_not_before`, reactivation, `pending_version` and the approval digest, the deployment settings row, the client secret, the registry and active-source check, per-issuer caches, claim mapping, canonical subjects and the group form, per-source callbacks and the shared resolver, the pin and proofs, the address checker's use on issuers, and the Token Service's egress rules and chart checks. |
| TS-7 | Login routing | The `/t/{tenantId}` prefix, per-tenant metadata and challenges, the playground's per-tenant pages, chooser, cookie scope, and mint under the prefix, and email domain routing. |
| TS-8 | SCIM server | The surface, credentials and quotas, the retirement step that revokes credentials, subject matching, deprovisioning into TS-4, user rows, and the conformance corpus. |
| TS-9 | Directory groups | Group tables, `group_source`, the group reader ahead of `envaccess`, the access report's expansion, and erasure and classification. |

These land early, because a stored format, a registered URI, or a shipped enforcement point would otherwise need rework: the claim allowlist in TS-2, holds in TS-4, and per-source callbacks, the deployment settings row, the group form, `suspended`, and reactivation in TS-6.

Dependencies are stated here and in the other design documents. Proposals from different designs can interleave as long as these hold.

| Proposal | Depends on | Why |
| :-- | :-- | :-- |
| TS-1 | EG-1, EG-2, EG-3, EG-4, EG-9, and TS-3 | The objects it scopes, the resolver, the git inputs, and a dependable session Environment |
| TS-1 (mirror steps) | EG-5 | Pool selection and the fill request |
| TS-1 (route step) | EG-6 | The upstreams routes name |
| TS-1 (read filters step) | EG-8, and the envelope field in EG-10 | The reads it extends |
| TS-2 | None | It can land at any point, and it should land early because it closes a High defect |
| TS-6 | TS-2 and TS-4 | The role boundary, and the shared address checker |
| TS-6 (activation under a delay) | SO-1 and SO-2, whichever lands second adding the case | `security-admin` approval and the weakening delay |
| TS-7 | TS-2 and TS-6 | The deployment route and the sources it names |
| TS-8 | TS-4 and TS-6 | Invalidation levels, the permission, and source identifiers |
| TS-9 | TS-3 and TS-8 | Groups on exchanged tokens, and directory data |
| TS-3 | None | It can land at any point |
| TS-3 (evidence field step) | The envelope field in EG-10 | Both Environments on `credential.mint` and evidence |
| TS-8 (retirement step) | TS-6 | Retirement names the credentials |
| TS-3 (guardrail case) | SO-2, whichever lands second adding the case | `crossEnvironmentSharing: forbid` and the delay |
| TS-3 (lock case) | UC-5, whichever lands second adding the case | Check 2 reads the lock UC-5 adds |
| TS-4 | None | TS-2's contract refers to it, and either order works |
| TS-5 | TS-1 | The scoped fields |

No `EG-`, `SO-`, `FE-`, or `OC-` proposal depends on a TS proposal. The relationships in the other direction are these:

- The hooks sit inside EG-1, EG-2, EG-5, EG-9, and EG-10, so those proposals do not wait.
- `security-oversight-design.md` narrows SO-1's question: under `securityRoleSource: managed`, a `security-*` claim is ignored from any issuer. A customer source's `role_map` cannot name one, so `oidc` needs SO's own rule to read such a claim, which TS-6 leaves to whichever lands second.
- SO-2's guardrails act on TS-1's writes through EG-9's single write path, and they can forbid an Environment's use of an opt-in with no TS change.
- OC-2 routes can appear under `environmentPolicies` with no OC change.
- UC-1 and UC-2 define types and link providers. TS-1 adds their `environments` field when both have landed, and neither waits. UG-2 and UC-3 read TS-4's invalidation level through `pkg/binding`, which returns it from EG-2. UG-1's start page uses TS-6's callback and resolver, so UG-1 depends on TS-6's login path on every source, and a broker's URI is registered once.

## Documentation

Each proposal carries the documentation for its own surface. Pages describe behaviour with no section numbers, and they land after the specification text they describe. The owner pages are these:

| Page | Change |
| :-- | :-- |
| `operator-guide/identity.md` (new) | Owns the identity model, choosing a broker or a customer issuer, the broker contract, broker selection criteria, activating a source and its proofs, claim mapping, the role boundary, and the lifecycle path. It states that a tenant's issuer is a choice to make at onboarding, and that Lenny provides no migration. It includes one worked integration under decision 12. |
| `operator-guide/scim.md` (new) | Owns the SCIM surface, credentials, subject matching per directory, deactivation, directory groups, what Lenny does when SCIM and tokens disagree, and the conformance status per directory. |
| `operator-guide/multi-tenancy.md` | **Environments** gains the choice between a tenant and an Environment, a section on cross-Environment sharing that states the two accepted risks and what user authorization does not cover, and a link to the scoping page. **Multi-Tenant Mode** links to the identity page. |
| "Tenant configuration" in the `egress-and-git-design.md` chapter | Gains team scoping: the fields, how they combine with tenant settings, and the refusal rule. |
| "Git access", "Credential bindings", and "Egress policy" in that chapter | Each states the scoped form of its objects in a few sentences and links to "Tenant configuration". |
| `operator-guide/security.md` | **Client Authentication** shrinks to a summary with corrected keys and a link to the identity page. The network section gains the issuer egress rules. |
| `operator-guide/web-playground.md` | Per-source login, and a correction to its statement that exchanging a playground bearer succeeds, since TS-2 refuses it. |
| `client-guide/delegation-and-tasks.md` | How a client disables delegation with `maxDepth: 0`, what `maxChildrenTotal` does, and that a client can only narrow. |
| `client-guide/authentication.md` | Corrects the role claim name, explains which sessions see which objects by access path, and states the per-tenant MCP and playground URLs. |
| `tutorials/multi-tenant-setup.md` | Replaces Helm keys the chart does not define, and adds an Environment-scoped step. |
| `reference/configuration.md`, the API reference, the concepts page, and the error reference | New values, fields, endpoints, and codes, and the rename of `crossEnvironmentDelegation`. |
| `operator-guide/lenny-ctl.md` | The new commands. |
| `reference/glossary.md` | Entries for identity broker, identity source, canonical subject, and scoped object. |

Tier 11 checks the examples against the chart and the schemas.

## Tests

Each proposal turns this list into tests with `// spec:` annotations at the tiers `test-coverage.md` maps. No test exists yet.

### Tier 0: static

- The schemas of the new fields validate, and an empty `environments` list fails validation.
- The chart renders `auth.rolesClaim`, `auth.groupsClaim`, and the `identity.*` values to both binaries, and the binaries define the flags. It renders `gateway.delegation.defaultMaxDepth` to `--delegation-default-max-depth` and refuses a value below 1.
- The chart refuses `identity.customerSources.enabled` with the gateway's external rule disabled, and renders the Token Service's rule.
- SCIM `Schemas` and `ResourceTypes` documents validate.

### Tier 1: unit

- `visible`, `always`, `requestable`, `denied`, and `routes` cover E = ∅, a matching E, a non-matching E, and each formula case.
- A tenant deny beats an Environment allow, and the ceiling bounds an Environment entry.
- Pool selection covers the longest prefix, disjoint lists, and refusal of a tenant-wide overlap and of an overlapping list.
- The IdP boundary removes `platform-admin` and keeps other roles, and a Lenny-issued token is unaffected.
- The registry covers an exact match, a near match, an oversized `iss`, an unknown `iss` with a counted zero fetches, and a token whose `iss` names one source and whose signature is another's.
- Tenant resolution covers a customer source ignoring a tenant claim, and `TENANT_ISSUER_MISMATCH`.
- Canonical subjects cover the prefix, the length limit, a deployment `sub` beginning with `~`, and `issuer` and `subject_claim` immutable on a row.
- `role_map` covers unmapped values, and refuses `platform-admin` and `security-*` at write. A `platform-admin` row is refused outside the operator tenant on each write path, and the resolver ignores one found elsewhere.
- The claim allowlist drops `caller_type`, `act`, `authorized_tools`, and `scope`, reads each kept claim by exact key, and a token with `scope: "openid profile"` verifies. `service_claim` sets `caller_type`.
- JWKS parsing refuses an RSA key over the limit and any `e` other than 65537, and a token over 16 KiB is refused before parsing.
- Holds cover each class, the highest remaining mode, and a class clearing only its own.
- Every change on an active source is classified, with only the stated narrowings applying at once, and an empty `audiences` is refused. `azp` is checked.
- The preflight refuses and lists, and it writes nothing else.
- Source and group names refuse `~`, and `deployment` is reserved.
- The address checker covers each excluded range, IPv4-mapped forms, redirects, size caps, and a name that re-resolves at dial.
- SCIM parsing covers each `PATCH` form, each filter, a refused filter, and boolean spellings in the corpus. The disagreement table is covered row by row.
- `observedAt` ordering covers older, equal, and newer calls, with an invalidation winning a tie.
- Lease resolution covers explicit 0 against absent on the request, in `MaxDepthInputs`, and through JSON storage, including `IsZero` with only `MaxDepth` set. A nil stored value refuses delegation. The minimum covers each layer present or absent, with the Helm fallback used only when none is, and a client value above `base` does not raise it. A client's `maxChildrenTotal: 0` is stored as `maxDepth: 0`, the integer keeps 0 as no limit, and a positive value caps one parent's children. Depth reads `parent.DelegationDepth`, unchanged when the Helm value moves.
- A delegated child takes the parent's E when the selector admits the runtime. The check covers each of its conditions alone: a session's own lock, a child holding no binding under a lock inherited from its parent, and a derive from such a child, the setting off or suspended, E = ∅, a set `crossed_from_environment`, an absent outbound or inbound entry, T's selector, and list order selecting T. A session with a crossing mark is refused inside T once the setting or either name goes.
- The crossing formula covers `always(T)`, `denied(T)` with the tenant's entries, no input from the parent, and the `restricted` row. A write with a `*` or wildcard source or target, or an empty selector, is refused. Adding an entry is refused with the setting off or forbidden, and removing one succeeds. Deleting an Environment a path names is refused.
- The `ENVIRONMENT_REQUIRED` and `ENVIRONMENT_POLICY_NOT_ENFORCEABLE` rules cover denied entries, route entries, entries that only add access, and objects visible to E alone. `ENVIRONMENT_REQUIRED` gives the same answer for a token with groups and one without, under both `noEnvironmentPolicy` values, and it does not refuse a resume.
- The checks on the final row cover a variant runtime, a variant pool, and a late `PreRoute` rewrite on a create, and a variant on a delegated child, including a rewrite that changes whether the hop crosses.
- The Bridge's list and call admit the §9.1 table's names and `lenny/vcs_token`, and refuse every other.
- `scope` keeps `tools:` values, drops and counts others, reads absent when none remain, and refuses a malformed `tools:` value and near-misses such as `tool:pool:read` and `TOOLS:pool:*`. `service_claim` sets `service` and `agent`. `caller_type` reads `human` by default. An absent subject claim refuses the token.
- A shadow candidate with `environmentPolicies` records its outcome under the session's E and changes no outcome.
- The binding host validator covers a tenant-wide binding and a binding listing several Environments.
- A future `observedAt` is stored as server time, so a later call at server time applies, and repeated future stamps do not extend it.

### Tier 2: component

- Admin handlers refuse an unknown Environment name, and refuse deletion while a reference or an active session exists.
- `pkg/binding` returns the Environment from the session row, and it fails closed on a read failure.
- Source state transitions in Postgres, with the unique index refusing a second tenant on one issuer and the deployment issuer refused.
- Directory tables under row-level security, with one tenant's credential unable to read or write another's rows.

### Tier 3: contract

- The admin API representation of each scoped object and of `environmentPolicies`.
- The refusal for an out-of-scope object and for an unknown object are byte-identical apart from the request's own name (TP4).
- The `reinstate` and `invalidate` request and response formats, including `applied`.
- `DELEGATION_DISABLED` on REST and MCP envelopes, and `delegationLease` echoing an explicit 0.
- The evidence envelope carries `environment` on each producer's stream.
- The `identity-sources` resource, the `/t/{tenantId}` paths, and RFC 9728 documents with `WWW-Authenticate`.
- SCIM requests and responses against RFC 7643 and RFC 7644 as far as the surface goes, including the error schema, and against the recorded directory corpus.

### Tier 4: integration

- A session create through each explicit path sets E, and through the transparent path leaves it empty.
- Derive, replay, and delegation propagate E, and billing rollup and retention follow. A crossing child bills and retains under T, and its records name both Environments. Same-Environment descendants copy `crossed_from_environment` and cannot cross.
- A session under `maxDepth: 0` gets `DELEGATION_DISABLED` from a runtime's direct `lenny/delegate_task` call through the intra-pod server and at the gateway edge, the intra-pod list omits the tool, and `lenny/discover_agents` is empty. `POST /v1/sessions/start` applies the field, and every root creator writes a value. A different `parentSessionId` or `lenny/await_children` session from an in-pod caller gets `PARENT_SESSION_MISMATCH`. A session with an absent value delegates up to the resolved limit, and that limit stays stored when the Helm value later changes.
- A child cannot widen its parent's value through `delegate_task`. A derive and a replay store the source's value minus its depth, and a resume keeps its row. Tree recovery after a pod failure brings a node back with the same stored value. A non-empty `delegationPolicyRef` is accepted, echoed, and changes nothing, and §27.4's playground payload still creates a session.
- A session under `maxDepth: 0` cannot cross with both sides declared.
- A declaration or setting change applies to the next `delegate_task` and revokes no in-flight child. An experiment rewrite off the path refuses the delegation. A `userCredential` binding crosses only when the lease names it and T can see it. A `required` pool in T checks the parent's user, and a pool with `userAuthorization: off` does not. A crossing child uses T's connectors and has no token of the caller's Environment. §8.3's `inherit` check runs on a crossing.
- SO-2's forbid refuses the write and suspends the paths, turning the setting on waits out the delay, and posture lists the paths.
- A `viewer`, and a member whose Environments do not select the runtime, are refused on the transparent create.
- The existing `cross_environment_delegation_test` moves to the opt-ins, with an in-pod caller whose principal has no groups.
- Edit propagation: removing E from a binding refuses the next I8 request, and adding a host to `denied(E)` refuses the next decision within the policy version rule.
- Invalidating a subject with no row creates the row and ends sessions. After `full_revoke`, a fresh IdP token is refused on every path, including the admin API and `/v1/oauth/token`. After `soft_disable` or `hard_disable`, the same token keeps session reads and loses admin authority. Reinstate restores access. A user under erasure cannot be reinstated, and a caller cannot reinstate itself.
- An in-pod delegation with no groups on the principal follows the session's E under both `noEnvironmentPolicy` values. A chain of delegations keeps E's denies and routes at every hop, and the decision service, the Token Service, and the mirror agree two hops down.
- `PUT disabled:false` is refused on an invalidated row. A `DELETE`d administrator's token is refused. `soft_disable` and `hard_disable` by a `platform-admin` revoke the user's impersonation bearers. A token from `/v1/oauth/token` carries the subject's groups and enters an Environment by `oidc-group`.
- A mirror fill names a pool and cause. The Token Service refuses a pool not visible to the cause's E, and a prefetch of a scoped pool by a non-admin is refused.
- `introspectionEndpoint` resolving to an excluded range is refused at write and at call.
- The metadata route names the issuer, and a 401 carries the challenge header. A per-tenant document names only that tenant's active issuers.
- A fake OIDC issuer per tenant: login, verification, mapping, and rotation with a new `kid`, on the gateway and on the Token Service at `/v1/oauth/token`.
- A proof login runs on a draft through the resolver. A `platform-admin`'s retirement at a tenant with a `security-admin` waits out the delay and can be cancelled, and a `security-admin`'s applies at once. `invalidateSubjects` needs a second `security-admin` or the delay, and its holds survive reactivation until cleared. A stranded row counts as active for approval. Reactivation needs a new proof and a ticket, and drops cached keys. `tokens_not_before` refuses older IdP and Lenny-issued tokens, and tokens with no `iat`, on the gateway, on `/v1/oauth/token` for each token, and on `lenny-ops`. A retired issuer stays held until its row is deleted.
- A `retired` or `suspended` row refuses writes, and a reactivation that changes a field waits as a `pending_version` under the activation rule, while an unchanged return does not. A suspension cancels a pending version.
- A first activation's row refuses writes, a draft edit clears `proof`, and a digest that no longer matches stops the change at effect. A change on an active source or settings row leaves the row `active` on live values during the delay, further widening is refused, and a narrowing or retirement cancels the pending version. Activation and a `deploymentIssuerAllowed` change wait out the delay after every `security-admin` is invalidated. Only a `draft` or `retired` row can be deleted, and a `retired` row's deletion takes the delay where one applies. `introspectionEnabled` is refused at a tenant with a customer source. A playground-origin `subject_token` is refused.
- A deployment token matching an inactive directory row is refused whether or not a user row exists, a login hold on a security principal follows `lifecycleSubjects`, and `active: false` on an unbound row reports in the SCIM response and fires the alert.
- A callback refuses with no token request when its state cookie is absent, when `state` does not match, when the state's source differs from the path's, or when RFC 9207 `iss` differs. A bare playground login and the link start page go through the resolver and answer `TENANT_ISSUER_MISMATCH` for a broker token naming a pinned tenant. `/t/{tenantId}` refuses a token of another tenant.
- A proof login needs the drafting admin's session or a live one-time token, creates no session, and a token cannot be used twice. Retirement refuses Lenny-issued tokens of its subjects, revokes SCIM credentials and playground records, and `invalidateSubjects` reports partial failure. Reactivation keeps `source_id`.
- A secret is returned by no route, is sent only by the Token Service, and is unusable after a sealed field changes.
- A SCIM deactivate reaches each `scim_deactivate_mode`, `active: true` clears only SCIM's own hold, re-provisioning an erased subject is refused, a security principal needs `lifecycleSubjects`, `externalId` cannot change, quotas apply, and a provisioned row leaves token roles in effect.
- Under `group_source: scim`, a stale claim does not admit, a removal takes effect within the cache bound, a store failure answers `503`, and `introspectionEnabled` is refused.
- `match_claim` binds once on the deployment source, a matching login on an inactive row is refused, a deployment `externalId` beginning with `~` is refused, and a `deployment` credential stops when the tenant disallows the deployment issuer.
- Erasure removes directory rows and memberships, and tenant deletion removes sources, credentials, and directory rows.
- `GET rbac-config` does not return the introspection secret.

### Tier 5: end to end on Kind

- Two Environments in one tenant run the first scenario in full: clone, push, a refused fetch, a bound host, and a refused host, through `netagent`, `lenny-egress`, the decision service, and the Token Service.
- A sequentially reused pod carries no credential across a change of Environment.
- A runtime on Kind under `maxDepth: 0` calls `lenny/delegate_task` directly and is refused, with no child pod claimed, and a pod failure in a tree recovers no wider lease.
- A crossing child on Kind reaches T's hosts, is refused a tenant-denied host, and reaches a host the caller's Environment denies, which pins the accepted behaviour.
- A concurrent pool refuses a session in an Environment with denied or route entries and a plan naming a scoped repository, and it admits an Environment with none. A slot on an admitted pod cannot reach a scoped repository through the union.
- A create, derive, replay, resume, and delegated child each meet the same refusal on that pool.
- Tenants on the deployment issuer and on a fake customer issuer run at once in one deployment, each refusing the other's tokens, under the chart's NetworkPolicy with an enforcing CNI, on the gateway and on the Token Service.
- A single-company install with no source behaves as before, and subjects keep their form.

### Tier 6: end to end on cloud

No behaviour needs cloud resources for automation. A manual per-release check runs each named directory's own SCIM validator and a live login against a test tenant, where the operator has one.

### Tier 7: load

- Concurrent writes to an object's `environments` and to session creates produce a consistent decision for each session.
- Repeated identical `invalidate` calls during a `reinstate` converge on the state of the newest stored time.
- The resolver adds no measurable decision latency beyond the existing cached policy read, measured against a stated threshold in the proposal.
- A flood of tokens with unknown `iss` values causes no fetch of those issuers and stays within a stated CPU bound, and refresh holds no lock during a fetch. Tokens for one slow issuer do not delay another's on a shared replica.
- Many sources across replicas, for verifier memory and the group read on the auth path, against stated thresholds. A SCIM burst stays within its rate limit, with one tenant's burst not affecting another.

### Tier 8: chaos

- Postgres loss during a decision or a mint fails closed. During the loss, the gateway refuses every bearer on the role lookup, and `lenny-ops` authorizes on token roles as §25.4 states.
- The issuer's JWKS endpoint goes down during a run, cached keys continue to verify, an unknown `kid` is refused, and past maximum staleness that source alone is refused.
- A Postgres outage leaves the registry on its last snapshot, a SCIM write answers `503`, and a `scim` source's group read answers `503`.
- A partial `full_revoke` reports its failures, and a retry completes it.

### Tier 9: security

- A token from the configured issuer with `roles: [platform-admin]` cannot reach any platform-only endpoint on the gateway, and cannot exchange into that authority at `/v1/oauth/token`.
- A member of an Environment with denied entries cannot reach a denied host through the transparent path, derive, replay, or delegation.
- With no sharing path, a descendant at any depth cannot obtain an object scoped outside its root's Environment, or reach a host that Environment denies. With one, a child and its descendants cannot obtain the caller's scoped objects or cross a second time, no pod input can set `crossed_from_environment`, and the tenant's denies hold.
- A hostile source cannot obtain another source's code through a redirect at its authorization endpoint, tested with a fake broker, and a silent login there cannot replace another tenant's session cookie.
- A hostile discovery document naming an in-cluster, loopback, or redirecting endpoint is refused for each endpoint.
- A tenant cannot name another tenant's secret, because no field names one.
- A broker token cannot reach a pinned tenant through the bare playground login.
- An invalidated `tenant-admin` with a valid IdP token cannot reach the admin API, reinstate itself, or poison ordering with a future `observedAt`, and a token with no groups cannot shed a team's deny under `allow-all`.
- A compromised runtime cannot delegate under `maxDepth: 0` or shed its limit, Environment, or mark by any tool call: naming another session as parent, creating a root session, or calling any tool outside the §9.1 table. A forged adapter call is out of scope until F-10.2.29 closes, and a test records that gap.
- A session cannot change its Environment through any request field, and a body `environment` on a path naming another is overridden.
- An agent in E cannot obtain a credential scoped elsewhere through I8, a direct call to the Token Service with its own token, the mirror, or `netagent`'s git path.
- A compromised-decision test: a stubbed decision service that allows a scoped object does not produce a mint, since the Token Service's own read refuses it.
- A token with an absent, malformed, or unregistered tenant claim keeps the §10.2 refusals, and a nested tenant claim is refused.
- A customer issuer cannot reach another tenant through any claim, a platform role through any claim or row outside the operator tenant, or a `security-*` role under `managed`. A broker token cannot reach a tenant whose `deploymentIssuerAllowed` is false, including after its customer source retires.
- A tenant admin cannot activate or widen a source, and at a tenant with a `security-admin`, a platform admin's activation waits out the delay and can be cancelled.
- A new issuer's `sub` equal to an old one's reaches no old row, because the `source_id` differs.
- Issuer URLs that resolve to excluded ranges, redirect, or rebind are refused, with the gateway and the Token Service both tested.
- A SCIM credential reaches only its own tenant and source, and nothing else in the admin API. Its value appears in no log.
- A role holding `manage_user_lifecycle` alone can call `invalidate` and `reinstate` in its own tenant, and nothing else.

### Tier 10: conformance

No runtime adapter contract changes.

### Tier 11: documentation

- Helm keys in identity pages and tutorials exist in `values.yaml`.
- Examples of scoped objects, sources, and SCIM requests validate against the schemas.

## Review of revision 13

One adversarial review reported on revision 13. Each finding was checked against the text and the tree by reading, and nothing was run. Provider and runtime statements in it stay untested.

| Finding | Outcome | Resolution |
| :-- | :-- | :-- |
| C1, shared callback and code theft | Holds. `Exchange` posts before validating, on one callback path. | "Browser login": per-source redirect URIs, refusal before any token request, RFC 9207, scoped cookies, and bound proof logins. The confidential broker client is a contract request. |
| H1, playground bypasses the registry | Holds. `extractSubject` reads raw claims. | One resolver for every browser login. |
| H2, `client_secret_ref` | Holds. | A write-only value the Token Service alone reads, sealed. The report's by-reference path for `platform-admin` is not adopted, so no field names a Secret. |
| M1, internal claims | Holds, today too. | The allowlist in TS-2, with `caller_type` through `service_claim`. `scope` stays dropped, with no mapping added. |
| M2, retirement | Holds. | Refusal by active source on every path, revocation, and optional `invalidateSubjects`. |
| M3, incident hold | Holds. | Holds per class in TS-4. |
| M4, `platform-admin` rows | Holds. `tenant_user_roles.go` already refuses the role, so that path needs no change. | One operator tenant. TP5 is corrected, and the residual is plain. Decision 24. |
| M5, unlisted fields | Holds. | Every change takes the rule except stated narrowings. |
| M6, pin index | Holds. | Partial index, reactivation, generic conflict, and a denied list. The shared-issuer entries are from knowledge. |
| M7, `subject_match` | Holds. | `match_claim`, declared immutable and verified, replaces `subject_match`. Administrator confirmation of the first bind is not adopted, as the smaller rule covers it. |
| M8, deployment settings | Holds. | A sentinel row, a reserved name, and the refusals. |
| M9, issuer over security roles | Holds. | Stated here and in SO, with posture. A second factor is decision 25. |
| M10, stranded principals | Holds. | The preflight refuses or lists and migrates nothing. SO's coverage alert counts a stranded row. |
| M11, login endpoints | Holds. The omitted ranges are confirmed in `egressCIDRs.excludePrivate`. | The checker covers each endpoint, with added ranges, and endpoints are captured at proof. |
| M12, CPU | Holds for the missing bound and for `MaxHeaderBytes`. The cost estimate is unmeasured. | Bounds in TS-2, background refresh, and a per-source limit. |
| M13, SCIM quotas | Holds. | Quotas, and extensions are dropped, which is smaller than capping them. |
| Minor, RLS reader | Holds. | A dedicated view. |
| Minor, group namespacing | Holds. | `source_id`, `~` refused, and unique `displayName`. |
| Minor, suspended | Holds. | A state, cleared by a `platform-admin`. |
| Minor, eviction and restart | Holds. | Idle eviction is removed, the restart case is stated, and refresh re-reads discovery. |
| Minor, disclosure | Holds. | The reason stays out of responses, the residual names the email page, and TP9 is corrected. |
| Minor, `lenny-ops` | Holds for the design's sentence, which is corrected. The report's point that the specification and code differ on IdP tokens was not checked further, because this design does not rely on it. | No path in the first version. |
| Minor, audience | Holds. | `azp` and non-empty `audiences`. |
| Minor, case-insensitive decoding | Holds by knowledge of the runtime. | Exact key, and a defect row. |
| Minor, erasure | Holds. | Addressed by SCIM `id`, and the preview holds no name. |
| Minor, SO conflict | Holds. | SO's sentence is corrected, a group write is not a lifecycle write, and `oidc` reads the roles claim directly. |
| Minor, provisioned rows | Holds. | Its own write path. |
| Minor, ongoing proof | Holds in part. | Domains are re-read and `email_domains` are unique. The sign-in proof stays one-time, as a residual. |
| The NetworkPolicy claim | Holds, and it corrects revision 13's claim. | Current state, SSRF section, and the defect row. |
| Proposal order table | Holds. | Each item lands where the table says. |

No finding was rejected outright. The parts not adopted are named above.

## Review of revision 14

A second adversarial review read the identity design at revision 14. Each finding was checked against the text and the tree by reading, and nothing was run.

| Finding | Outcome | Resolution |
| :-- | :-- | :-- |
| N1, dropping `scope` widens narrowed tokens | Holds. `Principal.Scopes` treats absent as no limit. | Kept and filtered to `tools:` values, and §25.1 is listed. |
| N2, immediate retirement | Holds. | The delay on a `platform-admin`'s retirement, a second approver or the delay on `invalidateSubjects`, stranded rows active for approval, a ticket on reactivation, and the lost-issuer case stated in both designs. |
| N3, reactivation revives | Holds. | `tokens_not_before`, a new proof, dropped keys, the wider preflight list, and how holds clear. |
| N4, the rule covers less than its purpose | Holds. | Introspection refused with a customer source, the settings row under the rule, no writes in `pending_effective`, proof cleared, and a digest. |
| N5, unbound rows | Holds. | The check on every deployment IdP token, `match_claim` at the boundary, `lifecycleSubjects` on a login hold, the report, and the residual. |
| Minor, proof against resolver | Holds. | The proof purpose is exempt. |
| Minor, allowlist extras | Holds. | Named, by exact key, per purpose. |
| Minor, absent subject claim | Holds. | Refused. |
| Minor, preflight role | Holds. | A `security-admin` row. |
| Minor, `lenny-ops` and step 7 | Holds. `tokenservice.go` checks only the caller's origin. | Step 7 names it and each token at `/v1/oauth/token`, and a playground-origin `subject_token` is refused. |
| Minor, snapshot view | Holds. | Fields added. |
| Minor, per-source bound | Holds. | Removed, and the background refresh stays. |
| Minor, confidential broker client | Holds. | The contract sentence is dropped, which is smaller than adding a value. |
| Minor, per-tenant cookie | Holds by reading the paths, and browser behaviour is from knowledge. | Scoping and the mint move in TS-7. |
| Minor, checker on `authorization_endpoint` | Holds by knowledge of IdPs. | A query is allowed there. |
| Minor, checker on the deployment issuer | Holds. | Exempt. |
| Minor, hold classes | Holds. | Classified by `manage_users` and `security-admin`. |
| Minor, bootstrap seed | Holds. `bootstrap.go` sets `u.Disabled = p.Disabled`. | Refused. |
| Minor, customer `match_claim` | Holds. | Fixed to `subject_claim`, with `externalId` unique. |
| Minor, uncapped writes | Holds. | Upserts capped per tenant. |
| Minor, retired issuer window | Holds. | A retired row holds its issuer until deleted. |
| Minor, stale wording | Holds. | Corrected. |
| Minor, credentials from a stolen admin | Holds. | Residual, and posture lists them. |
| Minor, default `caller_type` | Holds. | `human`. |
| Minor, SO wording | Holds. | SO names the roles claim. |
| Minor, UG step 5 | Holds. | Added to UG. |
| Order, UG-1 and TS-8 | Holds. | UG-1's dependency is unconditional, and TS-8 adds its step. |

No finding was rejected.

## Review of revision 15

A first review read cross-Environment sharing and the delegation limits at revision 15. Each finding was checked by reading, and nothing was run.

| Finding | Outcome | Resolution |
| :-- | :-- | :-- |
| H1, a runtime names any session as parent | Holds today. The handler reads `in.ParentSessionID`, and `lookupCallerSession` checks the owner alone. | The calling session is the parent, with `PARENT_SESSION_MISMATCH`. Tier 9 covers a runtime's calls, and a forged adapter call stays with F-10.2.29, as a residual with no dependency. |
| H2, a runtime creates a root | Holds today. `Bridge.CallPlatformTool` dispatches any name. | The Bridge's list and call are allow-listed to §9.1's table. |
| H3, `lockEgressToHosts` | Holds. | A session under the lock cannot cross, and the risk text includes readable tokens. |
| M1, a crossed subtree keeps delegating | Holds. | Every delegation under a mark needs the flag and both names. |
| M2, the delay stops at the flag | Holds. | An added entry is a weakening, label edits are a residual, and the table names custom roles. |
| M3, depth reads Helm | Holds. `walkCap` uses `defaultMaxDepth`. | Depth from `parent.DelegationDepth`, with its own walk bound. |
| M4, `maxChildrenTotal: 0` plumbing | Holds. | Removed: stored as `maxDepth: 0`. Only `MaxDepth` gets a pointer, with `IsZero` and a test, and the extension refusal is dropped. |
| M5, `/v1/sessions/start` drops the field | Holds. | The field is added, every root creator writes a value, and nil refuses delegation. |
| M6, `delegationPolicyRef` has no effect | Holds. | Refused at create in revision 16, and revision 17 reverses that. That part of F-8.2.34 stays open. |
| Minor, check order | Holds. | Top of the handler, and again in `validateDelegation`. |
| Minor, gate read errors | Holds. | Fail closed. |
| Minor, removal while off | Holds. | Additions refused and removals allowed. Paths returning with the flag stay as a posture item, and the flag takes the delay. |
| Minor, empty selector | Holds. | Refused. |
| Minor, name references | Holds. | Both errors cover these names. |
| Minor, experiment rewrite | Holds. | A rewrite that changes crossing status or T refuses. |
| Minor, `PreRoute` on delegation | Holds. | Dropped from the delegation sentence and test. |
| Minor, what moves across | Holds. | Added. |
| Minor, "behaves the same" | Holds. | The exceptions are stated. |
| Minor, derive depth | Holds. | Stored minus source depth, and the narrowing clause is dropped. |
| Minor, parent-supplied 0 | Holds. | Removed, along with `Request.MaxDepth`. |
| Minor, wired layers | Holds. | Only Helm is wired today. |
| Minor, discovery | Holds. | From the session's E and paths. |
| Minor, default policy row and envelope field | Holds. | The row is corrected, and egress revision 53 carries the field. |
| Minor, `BUILD-GAPS.md` and `CheckDepth` | Holds. | `CheckDepth` changes, and the coordinator holds the list of stale entries. |

No finding was rejected.

## Review of revision 16

A stop check read revision 16. Each finding was checked by reading, and nothing was run.

| Finding | Outcome | Resolution |
| :-- | :-- | :-- |
| 1, invalidating the security team removes the delay | Holds. | Activation and a `deploymentIssuerAllowed` change take the delay and an objection at a tenant with a `security-admin` row in any state, in both designs. Retirement after invalidation stays immediate. |
| 2, the allow-list breaks in-pod git before EG-3 | Holds. `lenny/vcs_token` is outside §9.1's table. | The list holds it until EG-3, in both designs. The report's alternative of a dependency is not taken. |
| 3, refusing `delegationPolicyRef` breaks §27.4 | Holds. The refusal in revision 16 was wrong. | Reversed. The report's removal is not taken either: TS-3 leaves the field accepted with no effect, says so, and that part of F-8.2.34 stays open. |
| 4, the lock check misses a descendant | Holds. | Check 2 covers an inherited lock, recorded with the child's stored inputs, with a tier 1 case. |
| 5, `state` cannot hold a pending change | Holds. | `pending_version`, with `pending_effective` for first activation alone. |
| 6, deleting a source row | Holds. | Only a `retired` row, under the delay where one applies, with the dispute path stated and the scenario corrected. |
| Minor, reactivation with a reachable principal | Holds. | Approval and the delay when one exists. |
| Minor, compromised issuer cancels retirement | Holds. | Invalidation first, then retirement. |
| Minor, `invalidateSubjects` order | Holds. | Confirmation first. |
| Minor, absent `iat` | Holds. | Refused when `tokens_not_before` is set. |
| Minor, stale snapshot mint | Holds. | Residual. |
| Minor, scope near-misses | Holds. | Refused, and dropped values are counted. |
| Minor, §25.1 `caller_type: "agent"` | Holds. | `service_claim` sets it. |
| Minor, `await_children` | Holds today. | The same rule. |
| Minor, Helm depth of 0 | Holds. | Chart validation. |
| Minor, discovery under the lock | Holds. | Omitted. |
| Minor, lock check dependency | Holds. | A row. |
| Minor, SO dependency table | Holds. | A row in SO. |
| Minor, "active" in SO | Holds. | Extended to the platform admin limit. |
| Minor, settings row proofs | Holds. | They do not apply. |
| Minor, introspection residual | Holds. | Added. |
| Minor, selector widening | Holds. | The same `Warning`. |

No finding was rejected.

## Review of revision 17

A stop check read revision 17. Each finding was checked by reading, and nothing was run.

| Finding | Outcome | Resolution |
| :-- | :-- | :-- |
| Major, a `retired` or `suspended` row has no write rule | Holds. | Such a row refuses writes, a reactivation with changes takes a `pending_version`, and tier 4 covers it. |
| Minor, unchanged reactivation | Holds. | A residual, and the suggested delay is not added, because retirement must stay reversible during an incident. |
| Minor, objection route | Holds. | The objection is removed from activation, because a stolen `platform-admin` can defeat it. SO's objection on assignment stays. |
| Minor, objection under attack | Holds. | A residual. |
| Minor, exchange refusal in the documentation and code | Holds. | The page's row and TS-2 name each. |
| Minor, lock across derive and replay, and the lock's field | Holds. | Both copy `egressLockHosts`, which EG and UC now name. |
| Minor, `suspended` return | Holds. | The `state` row agrees with the failure table. |
| Minor, suspension and a pending version | Holds. | Suspension cancels it. |
| Minor, pending secret and pending `deploymentIssuerAllowed` | Holds. | Where each waits is stated. |
| Minor, draft deletion | Holds. | Allowed. |
| Minor, wording of the retirement row and SO's row | Holds. | Matched. |
| Filing, `lenny/await_children` | Holds. | The parent rule names both tools. |
| Filing, F-8.3.12 | Holds. | TS-3 defines and renders the value before validating it. |

No finding was rejected.

## Decisions

Settled in revision 2:

- **Decision 2, tenant-wide objects.** A session in E sees the union of tenant-wide objects and E's objects. A team cannot exclude a tenant-wide binding, and the tenant admin scopes it instead. Limiting E to its own objects would require every shared object to list every Environment, and R2 would no longer hold.
- **Decision 9, an IdP token asserting `platform-admin`.** The role is removed, the request continues, and an audit event and an alert follow. A mapping error stays quiet to the caller, and an operator using claim-based staff roles must add rows.
- **Decision 13, user rows at login.** No row is created at login, and the upsert happens at invalidation or SCIM provisioning. A customer listing users sees only those with rows.

Decided by the owner on 2026-11-03:

- **Decision 3, a transparent session when a team policy applies.** Refused by runtime with `ENVIRONMENT_REQUIRED`, regardless of the caller's groups or `noEnvironmentPolicy`, with a resume exempt, and a `Warning` on the write that adds an Environment's first `denied` or route entry, listing the runtimes it affects. A transparent client breaks when a tenant admin first adds a deny to any team sharing the runtime. A caller outside every such Environment has no path to that runtime until a tenant admin adds the member or separates the runtime, and a member of several teams can choose the Environment with fewer denies.
- **Decision 26, disabling delegation.** A client can disable delegation for a session with `delegationLease.maxDepth: 0`, in the owner's words "yes, add it with maxDepth: 0". A client can only narrow, resolution becomes a minimum, `maxChildrenTotal: 0` is stored as `maxDepth: 0`, children never widen, and the gateway enforces it with `DELEGATION_DISABLED`. The cost is a changed §8.2 order, under which a preset above a Runtime default no longer wins, and a nil stored value refusing delegation.
- **Decision 14, cross-Environment delegation.** Keep it as opt-in sharing with guardrails, in the owner's words "go with B with these guardrails". A crossing child runs under the target's policy unchanged, with `dangerouslyAllowCrossEnvironmentSharing` and a two-sided `dangerouslyShare`, the tenant's denies, user authorization on `required` pools, no second crossing, named Environments only, a `security-admin` forbid with the weakening delay and posture, and both Environments on records. The accepted risk is that on a declared path, an injected caller can hand data to a target runtime, which can send it anywhere the target allows, skipping the caller's denies and proxy, and that a non-member's agent can drive the target's credentials and receive results, on any pool with no user check. The cost also includes a new session column. The alternative, a stored bound intersecting the caller's and target's sets, is retired.
- **Decision 8, identity model.** Per-tenant OIDC and a SCIM server in Lenny, with a deployer able to use a broker by default and to configure a customer's SSO when needed. This reverses revision 12's recommendation of a broker alone. The evidence for the need is vendor documentation, with no first-hand complaint found, so this is the owner's call and the priority inside it is uncertain. The cost is a new subsystem in Lenny, customer IdP support for the operator, wider token verifier egress, and a tenant boundary with two forms. The owner also removed migration between issuers from this design.

The rest are open for the owner, and the recommendation is the design above.

| # | Decision | Recommendation | Cost of the recommendation | Alternative |
| :-- | :-- | :-- | :-- | :-- |
| 1 | How an object names its Environments | A list on pools and bindings, and an `environmentPolicies` section for host lists and routes | It differs from §10.6's label selectors for runtimes and connectors. Each new Environment needs edits on each shared scoped object. | Selectors on the Environment. These cannot express "absent means tenant-wide", and a label edit silently widens access, with visibility readable only by evaluating every selector. |
| 4 | Which objects are scoped | Pools, bindings, host lists, and routes | A team cannot have its own `.npmrc` or `pip.conf` through client config files. It uses a tenant file or a runtime image. | Writing those files at session binding. That needs a change to `netagent`'s write timing, and it can follow if requested. |
| 5 (reopened) | Sequentially reused pods across Environments | Accept the residual, and document pools without recycling. Decision 15 covers concurrent pods. | State the scrub does not reach passes from one team's session to another's in the tenant. | An Environment pin on recycling pools, in which an acquisition refuses a pod last used by another Environment. It needs a pin record, claim filtering, and capacity per Environment, and it inherits the complexity EG-0 addresses for tenants. Reopened because a pin decided for decision 15 would largely serve this case as well. |
| 6 | Outline question: per-team egress quota | None now, with §10.6's billing rollup for cost | One team can exhaust the tenant's P7 quota. Billing rollup covers tokens and does not cover egress bytes. | A per-Environment bucket in the decision service, which is additive later. |
| 7 | Team self-service | Defer TS-5. A `tenant-admin` writes scoped objects. | Central IT handles each team's requests. §10.2's statement that an Environment `admin` gains management within the Environment remains unimplemented: `RoleAdmin` has no use outside `pkg/environment`, and all Environment routes need `manage_environments`. | TS-5 now, with a per-Environment bound set by the tenant admin. That adds a delegated authority over hosts and secrets, which needs a security review of its own. |
| 10 | Claim names | Keep `roles` and `groups` as defaults for the deployment issuer, remove `lenny_role` from the specification, and add `auth.rolesClaim` and `auth.groupsClaim`. A customer source has its own `roles_claim`, `groups_claim`, and `role_map`. | More Helm values, and a code path reading a named claim. A broker could map these names alone. | No new Helm values, which is smaller for the deployment issuer and assumes every broker can emit those exact names as arrays. Customer sources need their mapping either way. |
| 11 | What a directory deactivation does | `scim_deactivate_mode` per source, defaulting to `full_revoke`, and the same value documented as the recommended sync worker action | Sessions end on deactivation, including a mistaken one, and reinstating restores neither sessions nor links. An operator can select a weaker mode by mistake. | `soft_disable` by default, which leaves running sessions up and weakens R6. Or a fixed mode, which removes a choice some operators want. |
| 12 | Reference issuers | Name no broker as required. Publish selection criteria, and test one integration on Keycloak in test infrastructure, as a broker in one realm and as a customer issuer in another. SCIM conformance runs on a recorded corpus, since Keycloak's SCIM provisions into Keycloak and sends nothing to Lenny. | No directory runs in the suite, so conformance is a statement about the corpus, and a manual check per release carries the remainder. | A hosted vendor reference, which cannot run in the suite without an account and which reads as an endorsement. |
| 15 | Mixed Environments on concurrent and service pods | Evaluate with E = ∅, and refuse scoped objects, denies, and routes on those pods. | A team with its own repositories, denies, or routes cannot use a concurrent or service-mode pool at all. | Pin each pod to one Environment beside the tenant pin, so all its slots share E and E's full policy applies. It restores those pools for teams, at the cost of warm capacity per Environment, a pin record with arbitration like EG-0's, and a label fixed at bind. It is additive later. |
| 16 | Scoping of user credentials | `environments` on types and link providers, with a `registered` row's `environment` column honoured and links having none | A type's token and its Environment are separate lists to keep straight, and one token serves a person in several teams. | A link per Environment, which matches §4.3's connector key and makes a member of two teams link twice for one account. |
| 17 | Who activates an identity source | A `platform-admin` with a ticket and the proofs. At a tenant with a `security-admin` row in any state, also SO-2's delay, with a reachable `security-admin` approving. A deployment can let a tenant's `security-admin` activate alone. | A customer waits on the operator, and an operator ticket per customer IdP. The tenant-only path is off by default. | `tenant-admin` self-service with the proofs, which lets a stolen credential plant an issuer that lasts. Or platform-admin only with no customer check, which leaves the operator able to repoint an issuer unseen. |
| 18 | How an issuer pins | One exact issuer to one tenant among rows that can verify, never the deployment issuer, and never a name on `identity.deniedIssuers`. | A customer with two Lenny tenants, a provider whose issuer is shared by many customers, and a consumer provider all stay behind a broker. | Pinning `(iss, aud)`, or `iss` with a claim such as an organization, which admits those cases. The tenant boundary then rests on the IdP's `aud` or claim values and on one key set across tenants, and it restores a form of the tenant-claim risk. |
| 19 | Where groups come from | One of `token` or `scim` per source, never merged, with `scim` excluding introspection. | A misconfigured source uses the wrong one, which posture reports. Under `scim`, a directory that lags delays both grants and removals. | A union, under which a stale claim outlives a SCIM removal. An intersection, under which a lagging directory refuses new members and a claim missing past overage refuses everyone. |
| 20 | MCP discovery for a customer source | Per-tenant resource URLs and metadata documents. | Clients need a tenant URL, documents disclose a tenant identifier and IdP host, and client behaviour is unproven. | One document listing all issuers, which discloses every customer and leaves the choice to a client with no basis for it. An opaque slug in place of the tenant identifier, which is additive later. |
| 21 | An issuer on a private address | Refused in the first version. | That customer stays behind a broker or publishes its metadata. | A platform-registered private destination reached through `lenny-egress`, which makes authentication depend on that path and needs a registration per customer. It is additive later. |
| 22 | Maximum staleness of a customer source's keys | 24 hours, a Helm value, past which that source's tokens are refused. | An IdP metadata outage past that value stops a tenant whose IdP might still be signing. The figure is a judgment and is untested. | No limit, as the deployment issuer has today, under which a key rotated out during an outage stays trusted indefinitely. |
| 23 | SAML | Not in Lenny. A SAML-only customer stays behind a broker on the deployment source. | The operator needs a broker for those customers, and a customer on SAML cannot pin. | A SAML service provider in Lenny, which is a further subsystem. |
| 24 | Where operators' staff authenticate | The deployment issuer by default, with the operator tenant allowed to hold a customer source naming the operator's own IdP. | By default, a broker compromise reaches platform authority. The separate IdP is one more thing for an operator to run. | Require a separate issuer, which closes the path by default and adds a step to every SaaS install. Or refuse any customer source on that tenant, which makes the broker path permanent. |
| 25 | A second factor for security approvals | None in the first version. Posture reports each principal's issuer. | An issuer can act as any principal it authenticates, including a `security-approver`, and an operator's broker can approve the operator's own request. | An independent factor on approvals, which is the only thing that changes this result. It adds an enrolment and recovery flow. |

## Evidence for the need

- A Claude Code issue asks to "[let users declare additional allowlisted domains at the project or session level](https://github.com/anthropics/claude-code/issues/52982)". It concerns user-owned domains in a managed sandbox, so it relates only in part to team scoping. It was closed as not planned.
- GitHub lets organization admins [manage the Copilot agent firewall across repositories and control whether repository admins add entries](https://github.blog/changelog/2026-04-03-organization-firewall-settings-for-copilot-cloud-agent/) (vendor source). This is the closest analogue found for a layer below the organization.
- For SSO and SCIM, the evidence is vendor documentation, as listed under "What comparable systems do". No first-hand complaint was found. Decision 8 is the owner's.
