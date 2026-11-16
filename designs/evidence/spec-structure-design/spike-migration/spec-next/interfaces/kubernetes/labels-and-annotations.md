---
code: KLABEL
type: interface
area: kubernetes
status: partial
summary: >-
  The labels, annotations, finalizers, and readiness gates Lenny puts on its own resources and on
  agent pods. Each entry states the objects that carry the key, its values, its writers and
  readers, whether it can change, and the capability that owns its meaning. This spike holds only
  the keys the pool, pod, and claim lifecycle use.
owns: [labels]
depends-on: [K8S, POOL, CLAIM, APOD, WPC, PSC, GWCLAIM, WEBHOOK]
---

# Labels and annotations

<!-- partial: the other `lenny.dev/` keys (`lenny.dev/managed`, `lenny.dev/egress-profile`, `lenny.dev/egress-group`, `lenny.dev/dns-policy`, `lenny.dev/node-class`, `lenny.dev/component`, `lenny.dev/webhook-name`, and the rest) are added when their capabilities are migrated. -->

## Terms {#terms}

| Term | Definition |
| :-- | :-- |
| <a id="key"></a>Key | The full name of a label, annotation, finalizer, or readiness gate. Every Lenny key has the prefix `lenny.dev/`. |
| <a id="immutable-key"></a>Immutable key | A key whose value the Kubernetes API refuses to change or remove once set. |
| <a id="transition-rule"></a>Transition rule | A rule that lets a key's value change only along stated transitions and only by stated principals. |

## Overview {#overview}

A key here is an interface: a component that writes it and a component or person that selects by it agree on its spelling and its values through this page. What a key means for Lenny's behavior is stated by the capability each entry links. This page states the contract a Kubernetes client can rely on.

## Conventions {#conventions}

- A key's writers are the only principals that set it. A key that is immutable or has a transition rule is guarded by `lenny-label-immutability` ([Admission webhooks](../../components/admission-webhooks.md)), and a refused write answers `403 Forbidden` as [Lenny's Kubernetes API](index.md) states.
- A selector that names `lenny.dev/pod-set` also names `lenny.dev/pool` ([POOL-R6](../../capabilities/runtimes-and-pools/warm-pools.md#pool-r6)).

## Labels {#labels}

<a id="label-lenny-dev-pool"></a>

```lenny-def
kind: label
key: lenny.dev/pool
on: [PodSet, AgentPod, agent pod]
values: the pool name
mutability: required; immutable
writers: [PSC on PodSet create, WPC on AgentPod and pod create]
readers: [GWCLAIM, gateway recycle coordinator, WPC, operators]
meaning: POOL-R5, POOL-R6
summary: The pool a PodSet, an AgentPod, or a pod belongs to.
```

<a id="label-lenny-dev-pod-set"></a>

```lenny-def
kind: label
key: lenny.dev/pod-set
on: [PodSet, AgentPod, agent pod]
values: key16, the first 16 lower-case hexadecimal characters of the pod-set key
mutability: required; immutable
writers: [PSC on PodSet create, WPC on AgentPod and pod create]
readers: [GWCLAIM, WPC]
meaning: POOL-R5, POOL-R6
summary: The pod set a PodSet, an AgentPod, or a pod belongs to, within its pool.
```

<a id="label-lenny-dev-runtime"></a>

```lenny-def
kind: label
key: lenny.dev/runtime
on: [agent pod]
values: the runtime name
mutability: set at creation
writers: [WPC]
readers: [operators]
meaning: APOD-R2
summary: The runtime an agent pod runs.
```

<a id="label-lenny-dev-state"></a>

```lenny-def
kind: label
key: lenny.dev/state
on: [agent pod]
values: idle | active | draining; absent in other phases
mutability: changes with the pod's phase
writers: [WPC]
readers: [warm-pod PodDisruptionBudget selector, NetworkPolicy selectors, monitoring, operators]
meaning: APOD-R6
summary: The pod's phase, reduced to three values for selectors.
```

<a id="label-lenny-dev-tenant-id"></a>

```lenny-def
kind: label
key: lenny.dev/tenant-id
on: [agent pod]
values: a tenant identifier, or unassigned
mutability: transition rule
transitions: "unset → <tenantId> by the gateway's ServiceAccount; unset → <owner> by the WarmPoolController's ServiceAccount at creation on an owned pool or a single-tenant platform pool; <tenantId> → unassigned by the WarmPoolController's ServiceAccount"
refusal: tenant_label_immutable
writers: [GWCLAIM, WPC]
readers: [GWCLAIM, WPC]
meaning: CLAIM-R6
summary: The tenant pin of an agent pod.
```

Any other change is refused with [`tenant_label_immutable`](../../capabilities/runtimes-and-pools/pod-claims.md#error-tenant-label-immutable). No component performs the `<tenantId> → unassigned` transition on a recycled pod ([CLAIM-R6](../../capabilities/runtimes-and-pools/pod-claims.md#claim-r6)). <!-- unresolved: §5.2 and §4.6.3 name the guarding webhook `lenny-tenant-label-immutability` and §17.2 item 5 names `lenny-label-immutability`; this page uses the §17.2 name, which PLC and TDR also use. --> <!-- unresolved: whether the `→ unassigned` transition has any remaining use is not stated. -->

<a id="label-lenny-dev-served"></a>

```lenny-def
kind: label
key: lenny.dev/served
on: [agent pod]
values: "true"
mutability: set once, from unset
writers: [GWCLAIM, at first assignment on an owned pool or a single-tenant platform pool]
readers: [GWCLAIM, WPC]
meaning: CLAIM-R5, CLAIM-R6
summary: Marks a pod of an owned or single-tenant pool that has received a session.
```
<!-- unresolved: TDR stamps the label on owned pools and PM extends the rule to `placement.tenantId` pools; whether a shared pool's pods carry it is not stated. -->

<a id="label-lenny-dev-host-schedulable"></a>

```lenny-def
kind: label
key: lenny.dev/host-schedulable
on: [agent pod]
values: "true" | "false"; absent before the pod is bound to a node
mutability: mutable; not guarded
writers: [WPC, field manager lenny-warm-pool-controller, from its node informer]
readers: [gateway recycle coordinator; absent reads as "false"]
meaning: APOD-R7
summary: Whether the pod's node accepts new pods.
```

The key is left out of the guarded set so the WarmPoolController can change it on every cordon and uncordon.

<a id="label-lenny-dev-quarantine"></a>

```lenny-def
kind: label
key: lenny.dev/quarantine
on: [agent pod]
values: the quarantine identifier
mutability: transition rule
transitions: "set by the gateway or by a member of forensics.breakGlassGroups; removed only by the WarmPoolController"
writers: [gateway, break-glass operators]
readers: [WPC, GWCLAIM, lenny-podclaim-guard, NetworkPolicy selectors, the forensics reconciler]
meaning: APOD-R18, CLAIM-R5
summary: Marks a pod held for forensic investigation.
```

## Annotations {#annotations}

<a id="annotation-lenny-dev-drain-request"></a>

```lenny-def
kind: annotation
key: lenny.dev/drain-request
on: [agent pod]
values: present; the value is not read
writers: [GWCLAIM, gateway recycle coordinator, WPC on placement drift]
readers: [WPC]
meaning: APOD-R12
summary: A request that the WarmPoolController drain the pod.
```
<!-- unresolved: no source states the value; the tree writes a timestamp. -->

<a id="annotation-lenny-dev-config-generation"></a>

```lenny-def
kind: annotation
key: lenny.dev/config-generation
on: [PodSet]
values: the pool record's pool_config_generation that the PodSet was rendered from
writers: [PSC]
readers: [gateway drift check, admin API sync status]
meaning: POOL-R23
summary: The pool configuration generation a PodSet carries.
```

<a id="annotation-lenny-dev-scrub-warning"></a>

```lenny-def
kind: annotation
key: lenny.dev/scrub-warning
on: [agent pod]
values: present after a failed whole-pod scrub under onScrubFailure warn
writers: [GWCLAIM]
readers: [operators, audit]
meaning: APOD-R9
summary: Marks a pod held after a failed whole-pod scrub.
```
<!-- judgment: §5.2 and §6.2 say "a `scrub_warning` annotation" without a key; the key is the tree's. -->

## Finalizers and readiness gates {#finalizers}

<a id="finalizer-lenny-dev-session-cleanup"></a>

```lenny-def
kind: finalizer
key: lenny.dev/session-cleanup
on: [AgentPod]
writers: [WPC, which adds it at creation and removes it under APOD-R16]
meaning: APOD-R16
summary: Keeps an AgentPod until its session is safe.
```

<a id="readiness-gate-lenny-dev-agent-pod-ready"></a>

```lenny-def
kind: readiness-gate
key: lenny.dev/agent-pod-ready
on: [agent pod]
writers: [WPC, once the pod is a warm pod]
meaning: APOD-R3
summary: Marks an agent pod as a warm pod.
```

## Compatibility {#compatibility}

The readiness gate replaces `lenny.dev/sandbox-ready`. The labels `lenny.dev/runtime-process`, `lenny.dev/delivery-mode`, and `lenny.dev/workspace-tier` leave with the fields they recorded. No retired key is read.
