# Tenant scoping and identity

Status: outline for owner review, 2026-11-02. Nothing here is designed in detail, built, or staged. Proposals carry the label TS-n.

## Problem

Egress policy, VCS credential pools, and credential bindings are tenant-wide, so any client in a tenant can name any `onRequest` binding or allowlisted repository within those bounds. A customer with several teams cannot give each team its own. §10.6 Environments map OIDC groups to runtimes and connectors only. Separately, `auth.oidc.issuerUrl` and `auth.tenantIdClaim` are single Helm values and the spec has no SCIM, so a SaaS operator needs its own identity broker for per-customer SSO, which the spec does not say.

## Scope

- Scoping egress and git configuration below the tenant.
- Stating the identity model a SaaS operator builds on.

Out of scope: the egress and git objects themselves, which `egress-and-git-design.md` owns, and security roles, which `security-oversight-design.md` owns.

## Items

| Label | Item | Summary |
| :-- | :-- | :-- |
| TS-1 | Environment scoping | An optional Environment list on bindings, VCS pools, and session-requestable hosts, checked at session create. Absent means tenant-wide, so existing objects keep their meaning. |
| TS-2 | Identity model | State in the spec that per-tenant SSO and SCIM are the SaaS operator's identity broker's job, or decide that Lenny provides them. |

TS-1 is additive to the objects in `egress-and-git-design.md`, a new optional field and a new check at session create, so that design carries no hook for it.

## Dependencies

| Proposal | Depends on | Why |
| :-- | :-- | :-- |
| TS-1 | EG-1, EG-4 in `egress-and-git-design.md` | The objects it scopes |
| TS-2 | None | It can land at any point |

## Evidence for the need

- A Claude Code issue asks for control "[at the project or session level](https://github.com/anthropics/claude-code/issues/52982)".
- GitHub layers [organization and repository firewall rules](https://github.blog/changelog/2026-04-03-organization-firewall-settings-for-copilot-cloud-agent/).
- For SSO, the evidence is vendor documentation only, such as [Devin's SSO guide](https://docs.devin.ai/es/enterprise/security-access/sso/guide.md). No first-hand complaint was found, so its priority is uncertain.

## Open questions

- Should a session in an Environment see only that Environment's bindings, or also tenant-wide ones?
- Does per-team spending need its own egress quota, or is §10.6's billing rollup enough?
