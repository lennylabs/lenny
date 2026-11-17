---
code: NETAREA
type: index
area: network-and-egress
summary: >-
  Lists the capabilities that decide what network traffic agent pods may send and how it is attributed.
---

# Network and egress

Lists the capabilities that decide what network traffic agent pods may send and how it is attributed.

## Pages

<!-- generated:index -->

| Page | Code | Summary |
| :-- | :-- | :-- |
| [DNS](dns.md) | `DNS` | States the DNS resolution agent pods get and the mitigations against DNS exfiltration. |
| [Egress decisions](egress-decisions.md) | `EGDEC` | States how each outbound connection is attributed to a slot, decided against the effective policy, and recorded in the evidence streams. |
| [Egress defaults](egress-defaults.md) | `EGDEF` | States the default egress policy, how a scope replaces or augments it, exfiltration controls, and TLS interception. |
| [Egress policy](egress-policy.md) | `EGRESS` | States the egress policy model: scopes, the effective policy, defaults, replacement and augmentation, exfiltration controls, and TLS interception. |
| [Git access](git-access.md) | `GIT` | States how a pod reaches git hosts: covered hosts, the git mirror, and the git configuration injected into a slot. |
| [LLM upstreams](llm-upstreams.md) | `LLMUP` | Defines LLM upstreams, their assignment to sessions, the metering proxy's routing rules, cache isolation, calls outside the proxy, and forwarded attribution. |
| [Network isolation](network-isolation.md) | `NETISO` | States the default-deny baseline and the allow-lists that apply to agent pods and to the platform namespace. |
| [Operator services](operator-services.md) | `OPSVC` | States how a runtime reaches operator services through egress, the identity assertion attached to those requests, and the egress halt at suspension. |
| [Outbound connector](outbound-connector.md) | `OUTCONN` | Lets a tenant reach hosts on its private network through an agent it runs, with registration, identity, liveness, rotation, and revocation. |

<!-- /generated:index -->
