---
layout: default
title: Reference
nav_order: 7
has_children: true
description: Lookup tables and schemas — error catalog, metrics, state machines, configuration, CloudEvents catalog, WorkspacePlan schema, and glossary.
---

# Reference

This section contains lookup tables, schemas, and diagrams for cross-cutting concerns in the Lenny platform. It is reference material -- not tutorials or walkthroughs. Use it to look up specific error codes, metric names, state machine transitions, configuration fields, and terminology.

---

## What's in this section

| Page | Description |
|:-----|:------------|
| [Error Catalog](error-catalog) | Complete table of every Lenny error code with category, HTTP status, retryability, description, and recommended client action. |
| [Metrics Reference](metrics) | All Prometheus metrics emitted by Lenny components, including type, labels, emitting component, and associated alert rules. |
| [State Machines](state-machines) | Mermaid diagrams and transition tables for session, pod, task, delegation, and pool upgrade lifecycles. |
| [Configuration Reference](configuration) | Complete `values.yaml` field reference organized by component, with types, defaults, and validation rules. |
| [CloudEvents Catalog](cloudevents-catalog) | All CloudEvents types emitted by the platform (`dev.lenny.*`) with envelope, subject, and data-field schemas. |
| [WorkspacePlan Schema](workspace-plan) | Session workspace declarative spec: sources, setup commands, env, timeouts, retries, callbacks, delegation lease. |
| [Glossary](glossary) | Alphabetical definitions of all Lenny-specific terms and concepts. |

---

## How to use this section

- **Looking up an error?** Go to the [Error Catalog](error-catalog). Error codes are grouped by category (TRANSIENT, PERMANENT, POLICY, UPSTREAM) and each entry includes the recommended client response.
- **Investigating a metric or alert?** Go to the [Metrics Reference](metrics). Metrics are organized by component and each entry links to the alert rules that consume it.
- **Understanding a state transition?** Go to the [State Machines](state-machines) page for visual diagrams and complete transition tables.
- **Configuring a deployment?** Go to the [Configuration Reference](configuration) for the full Helm values schema with types, defaults, and validation constraints.
- **Wiring up a webhook receiver?** Go to the [CloudEvents Catalog](cloudevents-catalog) for the envelope shape and the per-type `data` schemas.
- **Composing a WorkspacePlan?** Go to the [WorkspacePlan Schema](workspace-plan) for the session-creation payload reference.
- **Unfamiliar with a term?** Go to the [Glossary](glossary) for concise definitions and cross-references to the relevant documentation pages.
