---
code: POD
type: index
summary: >-
  The agent pod as a unit: its containers in start order, volumes and size limits, project quotas, security context, and pod-level metrics.
status: placeholder
---

# Agent pod

The agent pod as a unit: its containers in start order, volumes and size limits, project quotas, security context, and pod-level metrics.

## Pages

| Page | Code | Summary |
| :-- | :-- | :-- |
| [Adapter](adapter.md) | `ADAPTER` | The adapter container: startup sequence, the slot registry, gateway and runtime connections, health checks, process settings, and metrics. |
| [Init and support containers](init-and-support-containers.md) | `PODINIT` | The init containers that stage platform binaries and fill repository stores, and the keepalive container. |
| [netagent](netagent.md) | `NETAGENT` | The netagent sidecar: the local egress endpoint, per-slot attribution, git on covered hosts, and per-slot credential files. |
| [Runtime supervisor](runtime-supervisor.md) | `SUPV` | The slot containers and the runtime supervisor that runs as PID 1 in each: process launch and end, the sweep, the file-descriptor broker, and the materialization helper. |
| [Volumes and quotas](volumes-and-quotas.md) | `PODVOL` | The agent pod's volumes, their size limits and eviction behavior, and the project quotas on slot volumes. |

## Content today

This index owns content of its own as well as the listing above. The sources below hold that content until the page is written.

Numbered sections:

- [§4.7.11 Adapter-Agent Security Boundary](../../04_system-components.md#4711-adapter-agent-security-boundary), in part
- [§6.4 Pod Filesystem Layout](../../06_warm-pod-model.md#64-pod-filesystem-layout), in part
- [§13.1 Pod Security](../../13_security-model.md#131-pod-security), in part
- [§17.1 Kubernetes Resources](../../17_deployment-topology.md#171-kubernetes-resources), in part
- [§17.2 Namespace Layout](../../17_deployment-topology.md#172-namespace-layout), in part

Changes of the v1 program whose content lands on this page:

- [0118-agent-pod-layout](../../../changes/0118-agent-pod-layout/change.md): Requirements, PL-1, Limits
- [0127-egress-and-git](../../../changes/0127-egress-and-git/change.md): Pod models
- [0145-session-isolation](../../../changes/0145-session-isolation/change.md): SI-1
- [0153-workspace-materialization](../../../changes/0153-workspace-materialization/change.md): WM-14
