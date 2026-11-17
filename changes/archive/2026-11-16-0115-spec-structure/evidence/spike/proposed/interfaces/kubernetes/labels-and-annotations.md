---
code: KLABEL
type: interface
summary: >-
  The labels, annotations, finalizers, and readiness gates Lenny puts on its own resources and on
  agent pods. This page defines each key: the objects that carry it, its values, its writers and
  readers, whether it can change, and the admission rule that guards it. It holds only the keys the
  pool and pod lifecycle use.
owns: [labels]
depends-on: [K8S, POOL, CLAIM, APOD, WPC, PSC, GWCLAIM, WEBHOOK]
---

# Labels and annotations

The labels, annotations, finalizers, and readiness gates Lenny puts on its own resources and on agent pods. This page defines each key: the objects that carry it, its values, its writers and readers, whether it can change, and the admission rule that guards it. It holds only the keys the pool and pod lifecycle use.

<!-- slice scope: the other `lenny.dev/` keys (`lenny.dev/managed`, `lenny.dev/egress-profile`, `lenny.dev/dns-policy`, `lenny.dev/node-class`, `lenny.dev/component`, `lenny.dev/webhook-name`, and the rest) are added when their capabilities are migrated. -->

## Terms {#terms}

| Term | Definition |
| :-- | :-- |
| Key | The full name of a label, annotation, finalizer, or readiness gate. Every Lenny key has the prefix `lenny.dev/`. |
| Immutable | A key whose value the Kubernetes API refuses to change or remove once set. |
| Transition rule | A key whose value may change only along stated transitions and only by stated principals. |

## Overview {#overview}

A key here is an interface: a component that writes it and a component or person that selects by it agree on its spelling and its values through this page. What a key means for Lenny's behavior is stated by the capability linked in each entry; this page states the contract a Kubernetes client can rely on.

## Conventions {#conventions}

- A key's writers are the only principals that set it. A key marked immutable or with a transition rule is guarded by [`lenny-label-immutability`](../../components/admission-webhooks.md#label-immutability), and a refused write answers `403 Forbidden` as [Lenny's Kubernetes API](index.md#admission) states.
- A selector that names [`lenny.dev/pod-set`](#lenny-dev-pod-set) also names [`lenny.dev/pool`](#lenny-dev-pool).

## Labels {#labels}

### `lenny.dev/pool` {#lenny-dev-pool}

```lenny-def
kind: label
key: lenny.dev/pool
on: [PodSet, AgentPod, agent pod]
values: the pool name
mutability: required; immutable
writers: PoolScalingController on PodSet create; WarmPoolController on AgentPod and pod create
readers: gateway pod claimer; gateway recycle coordinator; WarmPoolController; pool diagnosis; operators
meaning: Warm pools, "Pool label" (POOL-R5, POOL-R6)
```

### `lenny.dev/pod-set` {#lenny-dev-pod-set}

```lenny-def
kind: label
key: lenny.dev/pod-set
on: [PodSet, AgentPod, agent pod]
values: key16, the first 16 lower-case hexadecimal characters of the pod-set key
mutability: required; immutable
writers: PoolScalingController on PodSet create; WarmPoolController on AgentPod and pod create
readers: gateway pod claimer; WarmPoolController (disruption budget, pinned idle listing)
meaning: Warm pools, "Pod-set label" (POOL-R5, POOL-R6)
```

### `lenny.dev/runtime` {#lenny-dev-runtime}

```lenny-def
kind: label
key: lenny.dev/runtime
on: [agent pod]
values: the runtime name
mutability: set at creation
writers: WarmPoolController
readers: operators; the embedded stack's log and pod commands
meaning: Runtime registry, "Runtime name"; a runtime name is a valid label value (RUNTIME requirement on runtime names)
```

### `lenny.dev/state` {#lenny-dev-state}

```lenny-def
kind: label
key: lenny.dev/state
on: [agent pod]
values: idle | active | draining; absent in other phases
mutability: changes with the pod's phase
writers: WarmPoolController
readers: warm-pod PodDisruptionBudget selector; monitoring; NetworkPolicy selectors; operators
meaning: Agent pod lifecycle (APOD-R6)
```

### `lenny.dev/tenant-id` {#lenny-dev-tenant-id}

```lenny-def
kind: label
key: lenny.dev/tenant-id
on: [agent pod]
values: a tenant identifier; or unassigned
mutability: transition rule
transitions: unset → <tenantId> by the gateway's ServiceAccount, or by the WarmPoolController at creation on an owned pool; <tenantId> → unassigned by the WarmPoolController's ServiceAccount
writers: gateway pod claimer; WarmPoolController
readers: gateway pod claimer (tenant pin); WarmPoolController (pinned idle inventory)
meaning: Pod claims, "Tenant pin" (CLAIM-R6, CLAIM-R7)
```

Any other change is refused with `403 Forbidden` and the error `tenant_label_immutable`. No component performs the `<tenantId> → unassigned` transition on a recycled pod ([CLAIM-R6](../../capabilities/runtimes-and-pools/pod-claims.md#claim-r6)). <!-- unresolved: whether the reset transition has any remaining use once no component performs it on a recycled pod is not stated. -->

```lenny-def
kind: error
interface: kubernetes
name: tenant_label_immutable
status: 403
summary: A write changed lenny.dev/tenant-id outside its transition rule.
```

### `lenny.dev/served` {#lenny-dev-served}

```lenny-def
kind: label
key: lenny.dev/served
on: [agent pod]
values: "true"
mutability: set once
writers: gateway pod claimer, at first assignment on an owned pool
readers: gateway pod claimer (owned-pool admission); WarmPoolController (owned-pool inventory)
meaning: Tenant-derived runtimes, "Served label" (CLAIM-R5)
```
<!-- unresolved: the egress design adds the label; its value and its admission rule are not restated in the slice's sources. -->

### `lenny.dev/host-schedulable` {#lenny-dev-host-schedulable}

```lenny-def
kind: label
key: lenny.dev/host-schedulable
on: [agent pod]
values: "true" | "false"; absent before the pod is bound to a node
mutability: mutable; not guarded
writers: WarmPoolController (field manager lenny-warm-pool-controller), from its node informer
readers: gateway recycle coordinator; absent is read as "false"
meaning: Agent pod lifecycle (APOD-R7)
```

The key is left out of the immutable set so the WarmPoolController can change it on every cordon and uncordon. The gateway reads it through its `get` access on pods and needs no node access.

### `lenny.dev/quarantine` {#lenny-dev-quarantine}

```lenny-def
kind: label
key: lenny.dev/quarantine
on: [agent pod]
values: the quarantine identifier
mutability: transition rule
transitions: set by the gateway or by a member of forensics.breakGlassGroups; removed only by the WarmPoolController
writers: gateway; break-glass operators
readers: WarmPoolController; gateway pod claimer; lenny-podclaim-guard; network policy selectors; the forensics reconciler
meaning: Quarantine (APOD-R18; CLAIM-R5)
```

## Annotations {#annotations}

### `lenny.dev/drain-request` {#lenny-dev-drain-request}

```lenny-def
kind: annotation
key: lenny.dev/drain-request
on: [agent pod]
values: <!-- unresolved: no source states the value. -->
writers: gateway; WarmPoolController (placement drift)
readers: WarmPoolController
meaning: Agent pod lifecycle (APOD-R12)
```

### `lenny.dev/config-generation` {#lenny-dev-config-generation}

```lenny-def
kind: annotation
key: lenny.dev/config-generation
on: [PodSet]
values: the pool record's pool_config_generation that the PodSet was rendered from
writers: PoolScalingController
readers: gateway drift check; admin API sync status
meaning: Warm pools (POOL-R23)
```

### `lenny.dev/schema-version` {#lenny-dev-schema-version}

```lenny-def
kind: annotation
key: lenny.dev/schema-version
on: [every Lenny CustomResourceDefinition]
values: an integer as a string
writers: the chart
readers: every Lenny controller at start; lenny-preflight
meaning: Lenny's Kubernetes API, "Schema version"
```

### Scrub warning {#scrub-warning}

```lenny-def
kind: annotation
key: <!-- unresolved: §5.2 and §6.2 call it "the scrub_warning annotation" without a key. -->
on: [agent pod]
values: present after a failed whole-pod scrub under onScrubFailure warn
writers: gateway recycle coordinator
readers: operators; audit
meaning: Agent pod lifecycle (APOD-R9)
```

## Finalizers and readiness gates {#finalizers}

### `lenny.dev/session-cleanup` {#lenny-dev-session-cleanup}

```lenny-def
kind: finalizer
key: lenny.dev/session-cleanup
on: [AgentPod]
writers: WarmPoolController (adds at creation, removes under APOD-R16)
meaning: Agent pod lifecycle (APOD-R16)
```

### `lenny.dev/agent-pod-ready` {#lenny-dev-agent-pod-ready}

```lenny-def
kind: readiness-gate
key: lenny.dev/agent-pod-ready
on: [agent pod]
writers: WarmPoolController, once the pod is a warm pod
meaning: Agent pod lifecycle (APOD-R3)
```

## Compatibility {#compatibility}

The readiness gate replaces `lenny.dev/sandbox-ready`, and `lenny.dev/runtime-process` and `lenny.dev/delivery-mode` leave with the fields they recorded. No retired key is read.
