---
code: WALK
type: index
summary: >-
  Lists the non-normative walkthroughs that trace one scenario across components and channels, and states the trace notation.
status: placeholder
---

# Walkthroughs

Lists the non-normative walkthroughs that trace one scenario across components and channels, and states the trace notation.

## Pages

| Page | Code | Summary |
| :-- | :-- | :-- |
| [Checkpoint capture](checkpoint-capture.md) | `WKCKPT` | Traces a checkpoint from its trigger through quiesce, capture passes, and upload. |
| [Coordinator handoff](coordinator-handoff.md) | `WKHANDOFF` | Traces a coordinator handoff and a crash takeover between gateway replicas. |
| [Gateway drain](gateway-drain.md) | `WKDRAIN` | Traces a gateway replica drain with its checkpoint barrier and session handoff. |
| [Interactive message](interactive-message.md) | `WKMSG` | Traces one client message through delivery, the runtime's turn, and the streamed output. |
| [Interrupt, terminate, and delete](interrupt-terminate-delete.md) | `WKSTOP` | Traces an interrupt, a terminate, and a delete through the gateway, the adapter, and the pod. |
| [Ops remediation](ops-remediation.md) | `WKOPS` | Traces a DevOps agent observing an operational event, diagnosing it, taking a remediation lock, and verifying recovery. |
| [Pod eviction](pod-eviction.md) | `WKEVICT` | Traces the eviction of an agent pod that holds an active session. |
| [Restore and resume](restore-and-resume.md) | `WKRESUME` | Traces a resume after pod loss, from the failure through a new claim and restore. |
| [Session start](session-start.md) | `WKSTART` | Traces a session from the create request through pod claim, materialization, and the first message. |
| [Shared pod co-tenancy](shared-pod-co-tenancy.md) | `WKCOTEN` | Traces two sessions on one pod with several slots. |

## Content today

This index owns content of its own as well as the listing above. The sources below hold that content until the page is written.

Numbered sections:

- [§29 Communication Scenarios](../../29_communication-scenarios.md)
- [§29.1 Participants and trace notation](../../29_communication-scenarios.md#291-participants-and-trace-notation)
