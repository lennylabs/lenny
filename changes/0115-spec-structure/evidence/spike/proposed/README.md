---
code: README
type: conventions
summary: >-
  The root map of the specification: what Lenny is, the capability areas, the other top-level folders, and reading paths by task.
---

# Lenny specification

Lenny is a Kubernetes-native platform that runs agent sessions for many tenants. A client creates a session through the REST API; the gateway claims a pre-warmed pod for the session's runtime, builds its workspace, and relays messages and output between the client and the runtime. Sessions can suspend, resume from checkpoints, and delegate work to child sessions, while the platform meters usage, enforces policy and egress rules, and exposes an operability API for human operators and DevOps agents.

This page is the entry point. Read a folder index to find a page, read the page summary to decide whether to read on, and follow links for terms a page does not own. [Conventions](conventions.md) states the page types, the placement rules, and the identifier scheme.

## Capability areas

| Area | Summary |
| :-- | :-- |
| [Runtimes and pools](capabilities/runtimes-and-pools/index.md) | Lists the capabilities that decide which runtimes Lenny runs, on which pods, and how those pods are kept warm, claimed, reused, and retired. |
| [Sessions](capabilities/sessions/index.md) | Lists the capabilities that govern a session from creation to its end, including suspension, resume, checkpoints, derive, and isolation between sessions on one pod. |
| [Messaging](capabilities/messaging/index.md) | Lists the capabilities that carry messages into a session and output out of it: delivery, inboxes, inter-session messages, the event stream, input requests, and client tools. |
| [Workspaces and files](capabilities/workspaces-and-files/index.md) | Lists the capabilities that build a session's workspace and move files in and out of it. |
| [Delegation](capabilities/delegation/index.md) | Lists the capabilities that let a session start child sessions and govern the resulting tree. |
| [Tenancy and access](capabilities/tenancy-and-access/index.md) | Lists the capabilities that define tenants, environments, and principals and decide who may do what. |
| [Credentials](capabilities/credentials/index.md) | Lists the capabilities that hold credentials and deliver them to sessions and pods. |
| [Network and egress](capabilities/network-and-egress/index.md) | Lists the capabilities that decide what network traffic agent pods may send and how it is attributed. |
| [Policy and controls](capabilities/policy-and-controls/index.md) | Lists the capabilities that admit, shape, and refuse requests. |
| [Billing and usage](capabilities/billing-and-usage/index.md) | Lists the capabilities that meter usage, enforce budgets, and emit billing events. |
| [Observability and operations](capabilities/observability-and-operations/index.md) | Lists the capabilities that let operators and DevOps agents observe, diagnose, and remediate the platform. |
| [Security and compliance](capabilities/security-and-compliance/index.md) | Lists the capabilities that record evidence, oversee security, and meet data obligations. |
| [Deployment and lifecycle](capabilities/deployment-and-lifecycle/index.md) | Lists the capabilities that install, size, upgrade, back up, and restore a Lenny deployment. |

## Other folders

| Folder | Contents |
| :-- | :-- |
| [Overview](overview/index.md) | Purpose and context, principles and non-goals, the architecture, the capability and interface maps, and non-normative walkthroughs. |
| [Components](components/index.md) | One page per running unit: Lenny's processes, the agent pod and its containers, and the external dependencies. |
| [Interfaces](interfaces/index.md) | Contracts that one party can be built against: the client, admin, and ops APIs, the runtime protocol, the channels, Lenny's Kubernetes API, the CLI, events, and stored and deployment schemas. |
| [Cross-cutting](cross-cutting/index.md) | Properties every page upholds: the security model, tenant isolation, PKI, availability, compatibility, observability conventions, the error model, API conventions, data classification, and naming. |
| [Catalogs](catalogs/index.md) | Generated lists of settings, limits, errors, metrics, alerts, events, labels, custom resources, API operations, requirements, and terms, each row pointing at its owner. |

## Reading paths

- To change how a session acquires a pod, read [Pod claims](capabilities/runtimes-and-pools/pod-claims.md), then [Agent pod lifecycle](capabilities/runtimes-and-pools/agent-pod-lifecycle.md), then [Pod claimer](components/gateway/pod-claimer.md).
- To change how many pods a pool keeps warm, read [Warm pools](capabilities/runtimes-and-pools/warm-pools.md), then [Pool scaling controller](components/pool-scaling-controller.md).
- To add a field to session creation, read [Sessions](interfaces/client-api/sessions.md), then [Session creation](capabilities/sessions/session-lifecycle/session-creation.md), then [Workspace plan](interfaces/workspace-plan.md).
- To change what a runtime receives on its socket, read [Runtime protocol](interfaces/runtime-protocol/index.md), then [Messages](interfaces/runtime-protocol/messages.md), then [Message delivery](capabilities/messaging/message-delivery.md).
- To change which hosts a session may reach, read [Egress policy](capabilities/network-and-egress/egress-policy.md), then [Egress decisions](capabilities/network-and-egress/egress-decisions.md), then [netagent](components/agent-pod/netagent.md).
- To change how model calls are metered and billed, read [LLM upstreams](capabilities/network-and-egress/llm-upstreams.md), then [Usage records](capabilities/billing-and-usage/usage-records.md), then [Billing events](capabilities/billing-and-usage/billing-events.md).
- To change what survives a pod failure, read [Retry and resume](capabilities/sessions/retry-and-resume.md), then [Checkpoint capture](capabilities/sessions/checkpoints/checkpoint-capture.md), then [Restore](capabilities/sessions/checkpoints/restore.md).
- To change who may call an admin operation, read [Authorization](capabilities/tenancy-and-access/authorization.md), then [Admin API](interfaces/admin-api/index.md).
- To add a metric or an alert, read [Observability conventions](cross-cutting/observability-conventions.md), then [Alerting and slos](capabilities/observability-and-operations/alerting-and-slos.md).
- To change what an operator agent can do, read [Ops API](interfaces/ops-api/index.md), then [Remediation and escalation](capabilities/observability-and-operations/remediation-and-escalation.md), then [Health and diagnostics](capabilities/observability-and-operations/health-and-diagnostics.md).
- To change a Helm value or its default, read [Deployment configuration](interfaces/deployment-configuration/index.md).
- To find any setting, error, metric, or label by name, read [Settings](catalogs/settings.md), then [Errors](catalogs/errors.md), then [Metrics](catalogs/metrics.md), then [Labels](catalogs/labels.md).
