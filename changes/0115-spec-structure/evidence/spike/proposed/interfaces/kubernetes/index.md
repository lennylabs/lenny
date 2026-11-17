---
code: K8S
type: interface
summary: >-
  Lenny extends the Kubernetes API with its own custom resources, labels, and annotations, and
  guards them with admission webhooks. This page states the conventions every Lenny resource
  follows: the API group and version, the shipped CRDs and their schema version, field ownership,
  validation, and how an admission refusal reaches a client.
owns: [conventions]
depends-on: [WEBHOOK, KPSET, KAPOD, KPCLAIM, KLABEL]
---

# Lenny's Kubernetes API

Lenny extends the Kubernetes API with its own custom resources, labels, and annotations, and guards them with admission webhooks. This page states the conventions every Lenny resource follows: the API group and version, the shipped CRDs and their schema version, field ownership, validation, and how an admission refusal reaches a client.

## Terms {#terms}

**Owned here.**

| Term | Definition |
| :-- | :-- |
| Lenny kind | A custom resource kind Lenny defines in the `lenny.dev` API group. |
| Schema-version annotation | The annotation `lenny.dev/schema-version` on each Lenny CRD, which states the schema revision the CRD carries. |
| Field owner | The single writer of a field of a Lenny resource, enforced by server-side apply. |

**Borrowed.**

| Term | Gloss |
| :-- | :-- |
| Admission webhook | A Lenny process the API server calls before it stores a write ([Admission webhooks](../../components/admission-webhooks.md)). |

## Overview {#overview}

A client of the Kubernetes API, such as `kubectl`, an operator's tooling, or a Lenny controller, reads Lenny's resources like any other. Most Lenny resources have one writer, a Lenny component, and a person or tool other than that writer may read them but is refused when it writes them. The resource pages state each resource's fields, its writers, and every refusal a client can meet.

## Conventions {#conventions}

### Group, version, and kinds {#group}

- Every Lenny kind is registered in the API group `lenny.dev` at version `v1alpha1`.
- The kinds are [`PodSet`](podset.md), [`AgentPod`](agentpod.md), [`PodClaim`](podclaim.md), and [`Runtime`](runtime.md). `PodSet`, `AgentPod`, and `PodClaim` are namespaced and live in the agent namespace; `Runtime` is cluster-scoped.
- The chart ships one CRD per kind in `charts/lenny/crds/`, and the embedded stack ships the same CRDs. No other CRD is part of Lenny's API.
- A kind or resource name that Lenny no longer defines is not served under any alias. A manifest naming one is refused by the API server with `no matches for kind`, and a request to its path answers `404 NotFound`.

### Schema version {#schema-version}

- Each Lenny CRD carries the [schema-version annotation](labels-and-annotations.md#lenny-dev-schema-version). Each Lenny controller compares it at start with the value its binary expects, and `lenny-preflight` compares it with the value the chart release expects. A mismatch stops the controller or fails the preflight ([Upgrade and compatibility](../../cross-cutting/upgrade-and-compatibility.md#crd-schema-version)).
- A change to a CRD's schema moves the annotation's value. Adding a new CRD does not.
- `lenny-preflight` reports each CRD the release expects and does not find with `CRD '<name>' is missing — apply updated CRDs before running helm upgrade`.

### Field ownership {#field-ownership}

- Each field of a Lenny resource has one owner, named in the resource page's field tables. A resource's `status` is written through the status subresource.
- Owners write with server-side apply under a named field manager, so the API server refuses one owner's write to a field another owns with `409 Conflict`. RBAC grants resources and subresources and does not separate fields within them; server-side apply does.
- A client that is not a field's owner may read the field. A write by such a client is refused by the API server's conflict check or by the resource's admission rules.

### Validation {#validation}

- Each CRD carries an OpenAPI schema with CEL rules. The API server refuses a write that fails the schema before any webhook or controller sees it, and names the field.
- A field marked immutable in a resource page carries the CEL rule `self == oldSelf`.
- Lenny controllers validate their inputs again at reconcile.

### Admission {#admission}

- Lenny's admission webhooks are validating webhooks with `failurePolicy: Fail`. When a webhook that reviews a write cannot be reached, the API server refuses the write.
- A webhook refusal reaches the client as the API server's admission error: HTTP `403 Forbidden` with the webhook's message, which begins with a rejection label.
- The resource pages list each refusal with its rejection label, the condition that triggers it, and the requirement behind it.

### Rejection labels {#rejection-labels}

A rejection label is an upper-case code at the start of an admission message, stable across releases, that names the rule a write broke. Tooling matches on the label rather than on the message text. <!-- unresolved: §17.2 "Rejection labels." states the convention for the pod security webhook; whether every Lenny webhook, including the claim guard whose message today carries no label, follows it is not stated by any design. The resource pages propose labels and mark each as unresolved. -->

## Compatibility {#compatibility}

`v1alpha1` carries no compatibility promise across releases. Lenny has no deployment in the wild, so no conversion webhook converts between versions of these kinds, and a development cluster from an earlier build is reinstalled ([Upgrade and compatibility](../../cross-cutting/upgrade-and-compatibility.md)).
