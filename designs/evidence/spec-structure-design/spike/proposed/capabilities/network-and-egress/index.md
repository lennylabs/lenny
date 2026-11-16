---
code: NETAREA
type: index
area: network-and-egress
summary: >-
  Lists the capabilities that decide what network traffic agent pods may send and how it is attributed.
---

# Network and egress

The capabilities in this area decide what network traffic an agent pod may send and how that traffic is attributed. Network policy isolates pods by default, the egress policy decides each outbound connection, and dedicated paths serve DNS, git, LLM upstreams, operator services, and a tenant's private network.

## Reading paths

- To change which hosts a session may reach, read [Egress policy](egress-policy.md), then [Egress defaults](egress-defaults.md), then [Egress decisions](egress-decisions.md).
- To change how model calls are routed and metered, read [LLM upstreams](llm-upstreams.md), then [Metering proxy](../../components/gateway/metering-proxy.md).
- To change how a pod reaches a private network, read [Outbound connector](outbound-connector.md).

## Capabilities

| Page | Code | Summary |
| :-- | :-- | :-- |
| [Network isolation](network-isolation.md) | NETISO | States the default-deny baseline and the allow-lists that apply to agent pods and to the platform namespace. |
| [Egress policy](egress-policy.md) | EGRESS | States the egress policy model: scopes, the effective policy, defaults, replacement and augmentation, exfiltration controls, and TLS interception. |
| [Egress decisions](egress-decisions.md) | EGDEC | States how each outbound connection is attributed to a slot, decided against the effective policy, and recorded in the evidence streams. |
| [DNS](dns.md) | DNS | States the DNS resolution agent pods get and the mitigations against DNS exfiltration. |
| [LLM upstreams](llm-upstreams.md) | LLMUP | Defines LLM upstreams, their assignment to sessions, the metering proxy's routing rules, cache isolation, calls outside the proxy, and forwarded attribution. |
| [Git access](git-access.md) | GIT | States how a pod reaches git hosts: covered hosts, the git mirror, and the git configuration injected into a slot. |
| [Operator services](operator-services.md) | OPSVC | States how a runtime reaches operator services through egress, the identity assertion attached to those requests, and the egress halt at suspension. |
| [Outbound connector](outbound-connector.md) | OUTCONN | Lets a tenant reach hosts on its private network through an agent it runs, with registration, identity, liveness, rotation, and revocation. |
| [Egress defaults](egress-defaults.md) | EGDEF | States the default egress policy, how a scope replaces or augments it, exfiltration controls, and TLS interception. |

## Components that carry out this area

- [netagent](../../components/agent-pod/netagent.md): The netagent sidecar: the local egress endpoint, per-slot attribution, git on covered hosts, and per-slot credential files.
- [Egress proxy](../../components/egress-proxy.md): The central egress proxy (lenny-egress): deployment, transport security, and metrics.
- [Egress decision service](../../components/egress-decision-service.md): The egress decision service (lenny-egress-authz): pod identification, policy reads, deployment, and metrics.
- [Git mirror](../../components/git-mirror.md): The git mirror (lenny-git-mirror): storage, fetch scheduling, holds, deployment, and metrics.
- [Agent DNS](../../components/agent-dns.md): The dedicated CoreDNS instances for agent pods, including the resolver for proxied pods.
- [Outbound gateway](../../components/outbound-gateway.md): The outbound gateway Deployment that tenant agents dial, its replica groups, and metrics.
- [Metering proxy](../../components/gateway/metering-proxy.md): The gateway's metering proxy listener for LLM upstream calls: routes, limits, process settings, and metrics.

## Interfaces this area exposes

- [Pod egress](../../interfaces/channels/pod-egress.md): The pod-egress channels to the metering proxy and the object store.
- [Filesystem and environment](../../interfaces/runtime-protocol/filesystem-and-environment.md): The paths, environment variables, and injected configuration a runtime sees in its slot.
