---
code: CAT-LABELS
type: catalog
summary: >-
  Every label, annotation, finalizer, and readiness gate Lenny defines, with the objects that carry
  it and its owner entry. Generated from the definition blocks on the labels and annotations page;
  this slice's rows only.
---

<!-- GENERATED from `kind: label`, `kind: annotation`, `kind: finalizer`, and `kind: readiness-gate` definition blocks. Do not edit by hand. In the spike this file was written by hand to show what the generator would produce, and holds the warm pools and pod lifecycle slice's rows only. -->

# Labels and annotations catalog

| Key | Kind | On | Mutability | Owner entry |
| :-- | :-- | :-- | :-- | :-- |
| `lenny.dev/agent-pod-ready` | readiness gate | agent pod | | [entry](../interfaces/kubernetes/labels-and-annotations.md#lenny-dev-agent-pod-ready) |
| `lenny.dev/config-generation` | annotation | `PodSet` | Rewritten on each render | [entry](../interfaces/kubernetes/labels-and-annotations.md#lenny-dev-config-generation) |
| `lenny.dev/drain-request` | annotation | agent pod | | [entry](../interfaces/kubernetes/labels-and-annotations.md#lenny-dev-drain-request) |
| `lenny.dev/host-schedulable` | label | agent pod | Mutable | [entry](../interfaces/kubernetes/labels-and-annotations.md#lenny-dev-host-schedulable) |
| `lenny.dev/pod-set` | label | `PodSet`, `AgentPod`, agent pod | Required, immutable | [entry](../interfaces/kubernetes/labels-and-annotations.md#lenny-dev-pod-set) |
| `lenny.dev/pool` | label | `PodSet`, `AgentPod`, agent pod | Required, immutable | [entry](../interfaces/kubernetes/labels-and-annotations.md#lenny-dev-pool) |
| `lenny.dev/quarantine` | label | agent pod | Transition rule | [entry](../interfaces/kubernetes/labels-and-annotations.md#lenny-dev-quarantine) |
| `lenny.dev/runtime` | label | agent pod | Set at creation | [entry](../interfaces/kubernetes/labels-and-annotations.md#lenny-dev-runtime) |
| `lenny.dev/schema-version` | annotation | Lenny CRDs | Set by the chart | [entry](../interfaces/kubernetes/labels-and-annotations.md#lenny-dev-schema-version) |
| `lenny.dev/served` | label | agent pod | Set once | [entry](../interfaces/kubernetes/labels-and-annotations.md#lenny-dev-served) |
| `lenny.dev/session-cleanup` | finalizer | `AgentPod` | | [entry](../interfaces/kubernetes/labels-and-annotations.md#lenny-dev-session-cleanup) |
| `lenny.dev/state` | label | agent pod | Follows the phase | [entry](../interfaces/kubernetes/labels-and-annotations.md#lenny-dev-state) |
| `lenny.dev/tenant-id` | label | agent pod | Transition rule | [entry](../interfaces/kubernetes/labels-and-annotations.md#lenny-dev-tenant-id) |
| scrub warning (key unresolved) | annotation | agent pod | | [entry](../interfaces/kubernetes/labels-and-annotations.md#scrub-warning) |
