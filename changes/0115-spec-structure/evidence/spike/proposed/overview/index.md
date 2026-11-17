---
code: OVW
type: index
summary: >-
  Lists the overview pages: purpose and context, principles and non-goals, the architecture, the capability and interface maps, and the walkthroughs.
---

# Overview

The overview pages state Lenny's purpose and context and map its areas, capabilities, components, and interfaces. They state no rule, value, or behavior; each map entry links the page that does. The walkthroughs trace scenarios across components and are non-normative.

## Pages

| Page | Code | Summary |
| :-- | :-- | :-- |
| [Purpose and context](purpose-and-context.md) | CTX | States what Lenny is for, the actors that use it (clients, tenant admins, platform operators, runtime authors, and DevOps agents), and the external systems it depends on. |
| [Principles and non goals](principles-and-non-goals.md) | PRIN | States the design principles, the goals, the non-goals, and the decisions Lenny leaves to deployers. |
| [Architecture](architecture.md) | ARCH | Maps the components, the namespaces they run in, the stores they use, and the links between them, with one line per component and a link to its page. |
| [Capability map](capability-map.md) | CAPMAP | Maps each capability area and capability to the components that carry it out and the interfaces that expose it. |
| [Interface map](interface-map.md) | IFMAP | Maps each interface to the parties on each side, its transport, and its owning pages, and states that the client contract is REST alone. |

## Walkthroughs

| Page | Code | Summary |
| :-- | :-- | :-- |
| [Walkthroughs](walkthroughs/index.md) | WALK | Lists the non-normative walkthroughs that trace one scenario across components and channels, and states the trace notation. |
| [Session start](walkthroughs/session-start.md) | WKSTART | Traces a session from the create request through pod claim, materialization, and the first message. |
| [Interactive message](walkthroughs/interactive-message.md) | WKMSG | Traces one client message through delivery, the runtime's turn, and the streamed output. |
| [Interrupt terminate delete](walkthroughs/interrupt-terminate-delete.md) | WKSTOP | Traces an interrupt, a terminate, and a delete through the gateway, the adapter, and the pod. |
| [Checkpoint capture](walkthroughs/checkpoint-capture.md) | WKCKPT | Traces a checkpoint from its trigger through quiesce, capture passes, and upload. |
| [Restore and resume](walkthroughs/restore-and-resume.md) | WKRESUME | Traces a resume after pod loss, from the failure through a new claim and restore. |
| [Gateway drain](walkthroughs/gateway-drain.md) | WKDRAIN | Traces a gateway replica drain with its checkpoint barrier and session handoff. |
| [Coordinator handoff](walkthroughs/coordinator-handoff.md) | WKHANDOFF | Traces a coordinator handoff and a crash takeover between gateway replicas. |
| [Pod eviction](walkthroughs/pod-eviction.md) | WKEVICT | Traces the eviction of an agent pod that holds an active session. |
| [Shared pod co tenancy](walkthroughs/shared-pod-co-tenancy.md) | WKCOTEN | Traces two sessions on one pod with several slots. |
| [Ops remediation](walkthroughs/ops-remediation.md) | WKOPS | Traces a DevOps agent observing an operational event, diagnosing it, taking a remediation lock, and verifying recovery. |
