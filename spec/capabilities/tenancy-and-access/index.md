---
code: TENAREA
type: index
area: tenancy-and-access
summary: >-
  Lists the capabilities that define tenants, environments, and principals and decide who may do what.
---

# Tenancy and access

Lists the capabilities that define tenants, environments, and principals and decide who may do what.

## Pages

| Page | Code | Summary |
| :-- | :-- | :-- |
| [Authentication](authentication.md) | `AUTHN` | States how callers authenticate: OIDC, API keys, development mode, the token claims Lenny reads, key rotation, user invalidation, and the playground's session. |
| [Authorization](authorization.md) | `AUTHZ` | Defines the roles, the permission matrix, custom roles, scopes, session ownership, security roles, and delegated approvers. |
| [Browser sessions](browser-sessions.md) | `BROWSER` | States the cookie session a browser holds for the web playground and the token mint rules behind it. |
| [Environments](environments.md) | `ENV` | Defines environments, how runtimes are selected into them, the environment context of a session, and cross-environment rules. |
| [Principals](principals.md) | `PRINC` | Defines users, service accounts, and subjects, and how each is identified within a tenant. |
| [Security roles and approvers](security-roles-and-approvers.md) | `SECROLE` | Defines the security roles and the delegated approvers who confirm security-weakening changes. |
| [Tenant settings](tenant-settings.md) | `TSET` | Defines the tenant settings model, templates, the tenant key, compliance profiles, and how a setting changes on a live tenant. |
| [Tenants](tenants.md) | `TENANT` | Defines the tenant object and its lifecycle from creation to deletion. |
| [Token exchange](token-exchange.md) | `TOKX` | States the token exchange Lenny runs, scope narrowing, impersonation, write-before-issue, rotation, and revocation of issued tokens. |
