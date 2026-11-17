---
code: WALK
type: index
summary: >-
  Lists the non-normative walkthroughs that trace one scenario across components and channels, and states the trace notation.
---

# Walkthroughs

Each walkthrough traces one scenario step by step. Every step names the component that acts, the interface operation or channel message it uses, and the requirement that decides the outcome, as a link. A walkthrough states no rule of its own: where a step and a requirement disagree, the requirement holds and the walkthrough is wrong.

## Pages

| Page | Code | Summary |
| :-- | :-- | :-- |
| [Session start](session-start.md) | WKSTART | Traces a session from the create request through pod claim, materialization, and the first message. |
| [Interactive message](interactive-message.md) | WKMSG | Traces one client message through delivery, the runtime's turn, and the streamed output. |
| [Interrupt terminate delete](interrupt-terminate-delete.md) | WKSTOP | Traces an interrupt, a terminate, and a delete through the gateway, the adapter, and the pod. |
| [Checkpoint capture](checkpoint-capture.md) | WKCKPT | Traces a checkpoint from its trigger through quiesce, capture passes, and upload. |
| [Restore and resume](restore-and-resume.md) | WKRESUME | Traces a resume after pod loss, from the failure through a new claim and restore. |
| [Gateway drain](gateway-drain.md) | WKDRAIN | Traces a gateway replica drain with its checkpoint barrier and session handoff. |
| [Coordinator handoff](coordinator-handoff.md) | WKHANDOFF | Traces a coordinator handoff and a crash takeover between gateway replicas. |
| [Pod eviction](pod-eviction.md) | WKEVICT | Traces the eviction of an agent pod that holds an active session. |
| [Shared pod co tenancy](shared-pod-co-tenancy.md) | WKCOTEN | Traces two sessions on one pod with several slots. |
| [Ops remediation](ops-remediation.md) | WKOPS | Traces a DevOps agent observing an operational event, diagnosing it, taking a remediation lock, and verifying recovery. |
