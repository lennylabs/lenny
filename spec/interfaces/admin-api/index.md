---
code: ADMIN
type: index
summary: >-
  The admin REST API under /v1/admin: the conventions its operation pages rely on and the list of operation pages.
status: placeholder
---

# Admin API

The admin REST API under /v1/admin: the conventions its operation pages rely on and the list of operation pages.

## Pages

| Page | Code | Summary |
| :-- | :-- | :-- |
| [Billing](billing.md) | `ADMBILL` | Rate card, budget, billing correction, and usage operations. |
| [Compliance](compliance.md) | `ADMCOMP` | Legal hold, erasure job, residency, and tenant deletion operations. |
| [Credentials and upstreams](credentials-and-upstreams.md) | `ADMCRED` | Credential pool, LLM upstream, and revocation operations. |
| [Identity and access](identity-and-access.md) | `ADMIAM` | User, service account, role, and token operations. |
| [Policies](policies.md) | `ADMPOL` | Delegation policy, interceptor, quota, and circuit breaker operations. |
| [Pools](pools.md) | `APOOLS` | Pool operations. |
| [Runtimes](runtimes.md) | `ADMRT` | Runtime, revision, route, and resource class operations. |
| [Tenants and environments](tenants-and-environments.md) | `ADMTEN` | Tenant, tenant setting, environment, and outbound connector operations. |

## Content today

This index owns content of its own as well as the listing above. The sources below hold that content until the page is written.

Numbered sections:

- [§15.1 REST API › Session Replay Semantics](../../15_external-api-surface.md#session-replay-semantics), in part
- [§15.5 API Versioning and Stability](../../15_external-api-surface.md#155-api-versioning-and-stability), in part
