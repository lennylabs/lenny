# Tenant outbound connector

Status: outline for owner review, 2026-11-02. Nothing here is designed in detail, built, or staged. Proposals carry the label OC-n.

## Problem

Customers commonly keep git servers and package registries on private networks. In `egress-and-git-design.md`, reaching a private address needs a platform registration, and a shared egress address makes a customer's firewall rule admit other tenants too. For a SaaS operator each such customer is a support ticket, and a customer may refuse any inbound firewall rule at all. A customer-run agent that dials out to the platform removes both problems.

## Scope

- The connector agent a customer runs in its own network.
- The in-cluster connector gateway.
- Tenant self-service registration of a connector and its routes.
- Connector identity, liveness, and rotation.

Out of scope: egress policy, routing, and evidence, which `egress-and-git-design.md` owns.

## What this design can rely on

`egress-and-git-design.md`, under "Tenant outbound connector", provides these, so a connector adds no Envoy route and changes no decision contract:

- Typed route targets that egress routes, git routes, the resolver, and the mirror all name the same way.
- The forwarded CONNECT route to an address the decision pins, with the connector's identity injected as the upstream credential.
- A selector peer on the `lenny-egress` policy for an in-cluster service.
- A registration validator written per target kind.
- Mints for a host behind a connector taken through `lenny-egress`, and a CA on a credential binding for a private host with no endpoint registration.
- Per-endpoint CAs and `allowedRefPatterns` acting in `netagent`, `credentialPinned` once EG-12 lands, and evidence records naming the route target.

Validators refuse connectors at level 1.

## Items

| Label | Item | Summary |
| :-- | :-- | :-- |
| OC-1 | Connector gateway and agent | The gateway accepts outbound connections from agents and forwarded CONNECTs from `lenny-egress`, and carries tunnels between them. The agent is a small binary or image a customer runs. |
| OC-2 | Registration and identity | A tenant admin registers a connector, receives a credential, and adds routes. It covers rotation and revocation, per-tenant isolation at the gateway, and which hosts a connector may serve. |
| OC-3 | Operations | Liveness and its surfacing to the tenant, high availability, quotas, and evidence from the gateway. |

## Dependencies

| Proposal | Depends on | Why |
| :-- | :-- | :-- |
| OC-1 | EG-2 in `egress-and-git-design.md` | Typed route targets and the mint path |
| OC-2 | OC-1, EG-8, EG-9 | The gateway, the visibility reads, and the policy objects |
| OC-3 | OC-1 | The gateway |

## Evidence for the need

- A Cursor forum post describes "[self-managed GitLab on an internal host (VPN-only)](https://forum.cursor.com/t/self-hosted-worker-online-but-empty-repo-list-in-agents-ui/156060)".
- On Codex, one user wrote that using self-hosted git "[would be a total Game changer](https://community.openai.com/t/gitlab-with-codex-cloud-agent/1268080)".
- Cursor [documents egress ranges and tunnels](https://cursor.com/docs/cloud-agent/security-network.md).

## Open questions

- Should Lenny build the connector, or adopt an existing tunnel such as a Cloudflare Tunnel or Tailscale and specify only the integration?
- Does the mirror serve repositories behind a connector, given that the mirror holds fetched contents centrally?
- Who is responsible for name resolution errors inside the customer's network, and how does a tenant admin debug them?
