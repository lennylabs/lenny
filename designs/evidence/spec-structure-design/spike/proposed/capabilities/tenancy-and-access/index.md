---
code: TENAREA
type: index
area: tenancy-and-access
summary: >-
  Lists the capabilities that define tenants, environments, and principals and decide who may do what.
---

# Tenancy and access

The capabilities in this area define the tenant, its settings and environments, and the principals within it, and decide who may do what. Authentication establishes the caller, token exchange issues narrower tokens, and authorization applies roles and scopes.

## Reading paths

- To change how a caller is identified, read [Authentication](authentication.md), then [Principals](principals.md).
- To change who may perform an operation, read [Authorization](authorization.md), then [Security roles and approvers](security-roles-and-approvers.md).
- To change a tenant-wide setting, read [Tenant settings](tenant-settings.md), then [Tenants](tenants.md).

## Capabilities

| Page | Code | Summary |
| :-- | :-- | :-- |
| [Tenants](tenants.md) | TENANT | Defines the tenant object and its lifecycle from creation to deletion. |
| [Tenant settings](tenant-settings.md) | TSET | Defines the tenant settings model, templates, the tenant key, compliance profiles, and how a setting changes on a live tenant. |
| [Environments](environments.md) | ENV | Defines environments, how runtimes are selected into them, the environment context of a session, and cross-environment rules. |
| [Principals](principals.md) | PRINC | Defines users, service accounts, and subjects, and how each is identified within a tenant. |
| [Authentication](authentication.md) | AUTHN | States how callers authenticate: OIDC, API keys, development mode, the token claims Lenny reads, key rotation, user invalidation, and the playground's session. |
| [Token exchange](token-exchange.md) | TOKX | States the token exchange Lenny runs, scope narrowing, impersonation, write-before-issue, rotation, and revocation of issued tokens. |
| [Authorization](authorization.md) | AUTHZ | Defines the roles, the permission matrix, custom roles, scopes, session ownership, security roles, and delegated approvers. |
| [Security roles and approvers](security-roles-and-approvers.md) | SECROLE | Defines the security roles and the delegated approvers who confirm security-weakening changes. |
| [Browser sessions](browser-sessions.md) | BROWSER | States the cookie session a browser holds for the web playground and the token mint rules behind it. |

## Components that carry out this area

- [Gateway](../../components/gateway/index.md): The gateway as a unit: responsibilities, the subsystem list, replicas, disruption budget, probes, ServiceAccount, RBAC, NetworkPolicy, process settings, and metrics. The slice wrote a stub that lists subsystems.
- [Token service](../../components/token-service.md): The Token Service Deployment: its KMS access, secret reads, signing, upgrade and key rotation, and metrics.
- [Web playground](../../components/web-playground.md): The web playground: placement and gating, cookie session handling, UI surface, asset serving and CSP, cleanup, and metrics.

## Interfaces this area exposes

- [Tenants and environments](../../interfaces/admin-api/tenants-and-environments.md): Tenant, tenant setting, environment, and outbound connector operations.
- [Identity and access](../../interfaces/admin-api/identity-and-access.md): User, service account, role, and token operations.
- [Client API](../../interfaces/client-api/index.md): The client REST API: authentication, versioning, and the resource groups, with links to the shared API conventions and error model.
