---
code: KLABEL
type: interface
summary: The labels and annotations Lenny defines.
---

# Labels and annotations

<a id="label-lenny-dev-pool"></a>

```lenny-def
kind: label
key: lenny.dev/pool
carriers: [PodSet, agent pod]
values: the pool name
mutability: required; immutable
writers: [PSC]
readers: [GWCLAIM, WPC]
meaning: POOL-R1
summary: The pool a pod belongs to.
```

<a id="annotation-lenny-dev-drain-request"></a>

```lenny-def
kind: annotation
key: lenny.dev/drain-request
carriers: [agent pod]
values: [requested]
writers: [GWCLAIM]
readers: [WPC]
summary: Asks the controller to drain a pod.
```

<a id="finalizer-lenny-dev-session-cleanup"></a>

```lenny-def
kind: finalizer
key: lenny.dev/session-cleanup
carriers: [AgentPod]
writers: [WPC]
summary: Holds an AgentPod until its session is safe.
```

<a id="readiness-gate-lenny-dev-agent-pod-ready"></a>

```lenny-def
kind: readiness-gate
key: lenny.dev/agent-pod-ready
carriers: [agent pod]
writers: [WPC]
summary: Marks an agent pod ready for a claim.
```
