---
code: CONV
type: conventions
summary: How the fixture specification is written.
---

# Conventions

## Definition blocks

<!-- generated:definition-kinds -->

A definition block is a fenced code block whose info string is `lenny-def` and whose body is a YAML mapping. The `kind` field names the block's kind, and the kind's schema below lists the other fields. A block carries every required field of its kind and no field its kind lacks.

Each block's anchor is `<kind>-<slug>`, where the slug is the block's name lower-cased with each run of characters other than letters and digits replaced by `-` and with no `-` at either end: the setting `gateway.claimHoldTTLSeconds` is anchored `setting-gateway-claimholdttlseconds`. An operation is anchored by its identifier `<CODE>-<name>` slugged the same way: the operation `createPool` on the page `ADMPOOLS` is anchored `admpools-createpool`. The anchor is an HTML anchor element on its own line directly above the block, with only blank lines between them, whose id is the anchor. No two blocks, owned terms, or requirements of the specification share an anchor.

A field of type `page code` or `page codes` names pages by the codes their front matter registers. `specshift defs check` refuses a code that no page of the tree carries.

### Field types

| Type | Accepted value |
| :-- | :-- |
| string | A non-empty scalar, read as text. |
| scalar | A string, a number, or a boolean. |
| integer | An integer. |
| boolean | `true` or `false`. |
| enum | One of the values the field lists. |
| list | A YAML sequence of scalars, which may be empty (`[]`). |
| scalar or list | A scalar or a sequence of scalars. |
| enum list | A non-empty sequence of values the field lists. |
| page code | One registered page code. |
| page codes | One registered page code, or a non-empty sequence of them. |
| mapping | A non-empty mapping whose values are scalars. |
| transitions | A non-empty sequence of mappings, each with `from` (a state or a sequence of states), `to` (a state), and optionally `on` and `rule`. Each state named is one of the block's `states`. |

### Generated sections

A page carries a generated section between the marker comments `<!-- generated:<section> -->` and `<!-- /generated:<section> -->`, each on its own line. `specshift defs generate` replaces what lies between the markers and keeps the rest of the page. A page that a block names in `read-by`, `emitter`, or `unit` carries the section that block fills, unless the page is a placeholder.

| Section | Content |
| :-- | :-- |
| `settings-read` | The settings whose `read-by` names the page's code. |
| `metrics-emitted` | The metrics whose `emitter` names the page's code. |
| `events-emitted` | The event types whose `emitter` names the page's code. |
| `rbac-grants` | The page's own `grant` blocks and the `rbac-grant` blocks whose `unit` names the page's code. |
| `network-rules` | The `network-rule` blocks whose `unit` names the page's code. |
| `index` | On a folder's `index.md` whose type is not `index`, the table of the folder's pages. An `index.md` of type `index` is generated whole, and a folder index with no such section gets one appended under a Pages heading. |
| `definition-kinds` | This reference. |
| `code-registry` | On the conventions page, the code registry: every page's code, type, path, and summary, read from the pages' front matter. |

### `setting`

- Defines: one setting.
- Owner: The capability whose object the setting configures for a behavioral setting; the component for a process or deployment setting.
- Catalog: `catalogs/settings.md`.
- Name field: `name`, which gives the anchor's slug.

| Field | Type | Required | Meaning |
| :-- | :-- | :-- | :-- |
| `name` | string | yes | The setting's name as an operator writes it, such as `gateway.claimHoldTTLSeconds` or a flag such as `--create-qps`. |
| `type` | string | yes | The value type, such as `integer`, `duration`, `boolean`, `string`, `enum`, or `quantity`. |
| `default` | scalar | yes | The default value, or a phrase such as `none` when the setting has no default. |
| `range` | scalar or list | no | The accepted range, as `[min, max]` or a phrase. A block carries `range` or `values`, never both. |
| `values` | list | no | The accepted values of an enumerated setting. |
| `unit` | string | no | The unit of a numeric value, such as `seconds`. |
| `scopes` | list | yes | Where the setting is set, such as `platform`, `tenant`, or `pool`. |
| `defaults-by-tier` | mapping | no | Per-tier defaults, keyed by capacity tier. The tier presets of the settings catalog are generated from it. |
| `read-by` | page codes | yes | The codes of the components whose processes read the setting. Each component page lists it in its generated `settings-read` section. |
| `summary` | string | yes | One sentence that says what the defined thing is, used in the catalog row. |

### `limit`

- Defines: one limit.
- Owner: Wherever the setting that carries the limit is owned; for a fixed limit, the capability that enforces it.
- Catalog: `catalogs/limits.md`.
- Name field: `name`, which gives the anchor's slug.

| Field | Type | Required | Meaning |
| :-- | :-- | :-- | :-- |
| `name` | string | yes | The limit's name, such as `maxUploadBytes`. |
| `value` | scalar | yes | The limit's value. |
| `unit` | string | no | The unit of the value, such as `bytes`. |
| `scopes` | list | no | What the limit applies to, such as `session` or `tenant`. |
| `defaults-by-tier` | mapping | no | Per-tier values, keyed by capacity tier. |
| `setting` | string | no | The setting that overrides the limit, when one does. |
| `enforced-by` | page codes | no | The codes of the components that enforce the limit. |
| `summary` | string | yes | One sentence that says what the defined thing is, used in the catalog row. |

### `error`

- Defines: one error code.
- Owner: The capability whose rule produces the code; the cross-cutting error model for a code with several causes.
- Catalog: `catalogs/errors.md`.
- Name field: `name`, which gives the anchor's slug.

| Field | Type | Required | Meaning |
| :-- | :-- | :-- | :-- |
| `name` | string | yes | The error code as the wire carries it, such as `WARM_POOL_EXHAUSTED`. |
| `status` | integer | yes | The HTTP status the code is returned with. |
| `category` | enum: `PERMANENT`, `POLICY`, `TRANSIENT`, `UPSTREAM` | no | The error category of the shared error taxonomy. |
| `retryable` | boolean | no | Whether a client may retry the request unchanged. |
| `message` | string | no | The exact message, when the code carries a fixed one. |
| `details` | mapping | no | The fields of the error's details object and their values. |
| `returned-by` | list | no | The identifiers of the operations that return the code. |
| `rule` | scalar or list | no | The identifiers of the requirements whose rule produces the code. |
| `summary` | string | yes | One sentence that says what the defined thing is, used in the catalog row. |

### `admission-refusal`

- Defines: one admission refusal.
- Owner: The capability whose rule the admission webhook enforces. A refusal carries its exact message and no error code.
- Catalog: `catalogs/errors.md`.
- Name field: `name`, which gives the anchor's slug.

| Field | Type | Required | Meaning |
| :-- | :-- | :-- | :-- |
| `name` | string | yes | A name for the refusal, such as `podclaim concurrent claim`. |
| `message` | string | yes | The exact message the webhook returns. |
| `status` | integer | no | The HTTP status a Kubernetes client sees, `403` when absent. |
| `resource` | string | no | The resource whose writes the webhook reviews. |
| `operations` | list | no | The admission operations refused, such as `CREATE`. |
| `webhook` | string | no | The webhook that refuses the write. |
| `rule` | scalar or list | no | The identifiers of the requirements the refusal enforces. |
| `summary` | string | yes | One sentence that says what the defined thing is, used in the catalog row. |

### `condition-reason`

- Defines: one condition reason.
- Owner: The capability whose rule sets the condition.
- Catalog: `catalogs/errors.md`.
- Name field: `name`, which gives the anchor's slug.

| Field | Type | Required | Meaning |
| :-- | :-- | :-- | :-- |
| `name` | string | yes | The reason string, such as `PodSetKeyCollision`. |
| `resource` | string | yes | The custom resource whose status carries the condition. |
| `condition` | string | yes | The condition type the reason is set on. |
| `rule` | scalar or list | no | The identifiers of the requirements that set it. |
| `summary` | string | yes | One sentence that says what the defined thing is, used in the catalog row. |

### `metric`

- Defines: one metric.
- Owner: The capability whose behavior the metric measures; the component for a unit's health as a process; the observability conventions for a metric every controller emits.
- Catalog: `catalogs/metrics.md`.
- Name field: `name`, which gives the anchor's slug.

| Field | Type | Required | Meaning |
| :-- | :-- | :-- | :-- |
| `name` | string | yes | The metric name, such as `lenny_pod_claim_duration_seconds`. |
| `type` | enum: `counter`, `gauge`, `histogram`, `summary` | yes | The metric type. |
| `unit` | string | no | The unit, when the name does not carry it. |
| `labels` | list | yes | The label names, `[]` for none. |
| `emitter` | page codes | yes | The codes of the components that emit the metric. Each component page lists it in its generated `metrics-emitted` section. |
| `summary` | string | yes | One sentence that says what the defined thing is, used in the catalog row. |

### `alert`

- Defines: one alert.
- Owner: The owner of the requirement whose breach the alert detects; the component for an alert on a unit's health.
- Catalog: `catalogs/alerts.md`.
- Name field: `name`, which gives the anchor's slug.

| Field | Type | Required | Meaning |
| :-- | :-- | :-- | :-- |
| `name` | string | yes | The alert name, such as `WarmPoolLow`. |
| `expr` | string | yes | The alert's expression, in PromQL or in words. |
| `severity` | enum: `critical`, `info`, `warning` | yes | The severity label. |
| `for` | string | no | How long the expression holds before the alert fires, such as `5m`. |
| `detects` | scalar or list | no | The identifiers of the requirements whose breach the alert detects. |
| `runbook` | string | no | The runbook that handles the alert. |
| `summary` | string | yes | One sentence that says what the defined thing is, used in the catalog row. |

### `event`

- Defines: one event type.
- Owner: The interface that carries the event.
- Catalog: `catalogs/events.md`.
- Name field: `type`, which gives the anchor's slug.

| Field | Type | Required | Meaning |
| :-- | :-- | :-- | :-- |
| `type` | string | yes | The event type, such as `dev.lenny.session.created`. |
| `emitter` | page codes | yes | The codes of the components that emit the event. Each component page lists it in its generated `events-emitted` section. |
| `payload` | string | yes | What the event's data carries, in one sentence or as a list of fields. |
| `summary` | string | yes | One sentence that says what the defined thing is, used in the catalog row. |

### `label`

- Defines: one label.
- Owner: The Kubernetes labels and annotations page.
- Catalog: `catalogs/labels.md`.
- Name field: `key`, which gives the anchor's slug.

| Field | Type | Required | Meaning |
| :-- | :-- | :-- | :-- |
| `key` | string | yes | The key, such as `lenny.dev/pool`. |
| `carriers` | list | yes | The objects that carry it. |
| `values` | scalar or list | yes | The values it takes, as a list or a phrase. |
| `mutability` | string | yes | When it may change, such as `required; immutable` or `set once, from unset`. |
| `transitions` | string | no | The permitted value changes and who may make each, when mutability is a transition rule. |
| `refusal` | string | no | The error code or admission refusal a forbidden change receives. |
| `writers` | list | yes | The components or parts that write it, as codes or short phrases. |
| `readers` | list | yes | The components or parts that read it, as codes or short phrases. |
| `meaning` | scalar or list | no | The identifiers of the requirements that give it meaning. |
| `summary` | string | yes | One sentence that says what the defined thing is, used in the catalog row. |

### `annotation`

- Defines: one annotation.
- Owner: The Kubernetes labels and annotations page.
- Catalog: `catalogs/labels.md`.
- Name field: `key`, which gives the anchor's slug.

| Field | Type | Required | Meaning |
| :-- | :-- | :-- | :-- |
| `key` | string | yes | The key, such as `lenny.dev/pool`. |
| `carriers` | list | yes | The objects that carry it. |
| `values` | scalar or list | yes | The values it takes, as a list or a phrase. |
| `mutability` | string | no | When it may change, such as `required; immutable` or `set once, from unset`. |
| `transitions` | string | no | The permitted value changes and who may make each, when mutability is a transition rule. |
| `refusal` | string | no | The error code or admission refusal a forbidden change receives. |
| `writers` | list | yes | The components or parts that write it, as codes or short phrases. |
| `readers` | list | yes | The components or parts that read it, as codes or short phrases. |
| `meaning` | scalar or list | no | The identifiers of the requirements that give it meaning. |
| `summary` | string | yes | One sentence that says what the defined thing is, used in the catalog row. |

### `finalizer`

- Defines: one finalizer.
- Owner: The Kubernetes labels and annotations page.
- Catalog: `catalogs/labels.md`.
- Name field: `key`, which gives the anchor's slug.

| Field | Type | Required | Meaning |
| :-- | :-- | :-- | :-- |
| `key` | string | yes | The finalizer, such as `lenny.dev/session-cleanup`. |
| `carriers` | list | yes | The objects that carry it. |
| `writers` | list | yes | The components or parts that write it, as codes or short phrases. |
| `readers` | list | no | The components or parts that read it, as codes or short phrases. |
| `meaning` | scalar or list | no | The identifiers of the requirements that give it meaning. |
| `summary` | string | yes | One sentence that says what the defined thing is, used in the catalog row. |

### `readiness-gate`

- Defines: one readiness gate.
- Owner: The Kubernetes labels and annotations page.
- Catalog: `catalogs/labels.md`.
- Name field: `key`, which gives the anchor's slug.

| Field | Type | Required | Meaning |
| :-- | :-- | :-- | :-- |
| `key` | string | yes | The condition type of the gate, such as `lenny.dev/agent-pod-ready`. |
| `carriers` | list | yes | The objects that carry it. |
| `writers` | list | yes | The components or parts that write it, as codes or short phrases. |
| `readers` | list | no | The components or parts that read it, as codes or short phrases. |
| `meaning` | scalar or list | no | The identifiers of the requirements that give it meaning. |
| `summary` | string | yes | One sentence that says what the defined thing is, used in the catalog row. |

### `custom-resource`

- Defines: one custom resource.
- Owner: The custom resource's Kubernetes interface page.
- Catalog: `catalogs/custom-resources.md`.
- Name field: `name`, which gives the anchor's slug.

| Field | Type | Required | Meaning |
| :-- | :-- | :-- | :-- |
| `name` | string | yes | The resource kind, such as `PodClaim`. |
| `group` | string | yes | The API group, such as `lenny.dev`. |
| `version` | string | yes | The served version, such as `v1alpha1`. |
| `scope` | string | yes | `Namespaced` or `Cluster`, optionally followed by the namespace in parentheses. |
| `plural` | string | no | The plural resource name. |
| `crd` | string | no | The CustomResourceDefinition's name. |
| `short-name` | string | no | The short name, when the resource has one. |
| `writers` | list | no | The components or parts that write it, as codes or short phrases. |
| `readers` | list | no | The components or parts that read it, as codes or short phrases. |
| `summary` | string | yes | One sentence that says what the defined thing is, used in the catalog row. |

### `operation`

- Defines: one operation.
- Owner: The interface page that defines the operation. The block's anchor is the operation identifier `<CODE>-<name>` lower-cased, such as `admpools-createpool`.
- Catalog: `catalogs/api-operations.md`.
- Name field: `name`, which gives the anchor's slug.

| Field | Type | Required | Meaning |
| :-- | :-- | :-- | :-- |
| `name` | string | yes | The operation's own name in its interface: the OpenAPI `operationId`, the gRPC method, the Kubernetes verb, or the CLI command words, such as `createPool`. |
| `method` | string | no | The HTTP method, the RPC kind, or the Kubernetes verb. |
| `path` | string | no | The HTTP path or the resource the operation acts on. |
| `returns` | list | no | The error codes and refusals the operation returns, by name. |
| `requirements` | list | no | The identifiers of the requirements behind the operation's outcomes. |
| `summary` | string | yes | One sentence that says what the defined thing is, used in the catalog row. |

### `state`

- Defines: one state machine.
- Owner: The capability that governs the object's lifecycle.
- Catalog: `catalogs/state-machines.md`.
- Name field: `name`, which gives the anchor's slug.

| Field | Type | Required | Meaning |
| :-- | :-- | :-- | :-- |
| `name` | string | yes | The state machine's name, such as `claim binding state`. |
| `object` | string | yes | The domain object whose states these are. |
| `states` | list | yes | Every state, in lifecycle order. |
| `initial` | string | no | The state an object starts in; one of `states`. |
| `terminal` | list | no | The states an object never leaves; each one of `states`. |
| `transitions` | transitions | yes | Each transition as a mapping: `from` (a state or a list of states), `to` (a state), and optionally `on` (the trigger) and `rule` (a requirement identifier). |
| `summary` | string | yes | One sentence that says what the defined thing is, used in the catalog row. |

### `register`

- Defines: one register.
- Owner: The narrowest page that covers every writer: a part page, a component index, the gateway seams page, or a channel interface page.
- Catalog: `catalogs/channels.md`.
- Name field: `name`, which gives the anchor's slug.

| Field | Type | Required | Meaning |
| :-- | :-- | :-- | :-- |
| `name` | string | yes | The register's name, such as `slot set`. |
| `identifier` | string | no | The register's channel identifier, such as `REG-SLOTSET`, when it mediates two participants. |
| `store` | string | yes | The store that holds it, such as `Redis` or `Postgres`. |
| `keys` | list | yes | The keys, tables, or objects it occupies. |
| `members` | list | no | The member or value forms it holds. |
| `writers` | list | yes | The components or parts that write it, as codes or short phrases. |
| `readers` | list | yes | The components or parts that read it, as codes or short phrases. |
| `summary` | string | yes | One sentence that says what the defined thing is, used in the catalog row. |

### `grant`

- Defines: one RBAC grant.
- Owner: The page of the running unit whose ServiceAccount holds the grant, for a grant the unit needs as a whole.
- Catalog: `catalogs/contributions.md`.
- Name field: `name`, which gives the anchor's slug.

| Field | Type | Required | Meaning |
| :-- | :-- | :-- | :-- |
| `name` | string | yes | A short name for the grant, unique on the page, such as `podclaims write`. |
| `service-account` | string | no | The ServiceAccount, when the unit runs under more than one. |
| `api-groups` | list | yes | The API groups of the rule; the core group is `""`. |
| `resources` | list | yes | The resources and subresources of the rule, such as `podclaims/status`. |
| `verbs` | enum list: `*`, `bind`, `create`, `delete`, `deletecollection`, `escalate`, `get`, `impersonate`, `list`, `patch`, `update`, `use`, `watch` | yes | The verbs of the rule. |
| `namespace-scope` | string | yes | Where the grant applies: `cluster`, or the namespace it is bound in, such as `agent namespace`. |
| `requirement` | string | yes | The identifier of the requirement behind the grant or rule, such as `GWCLAIM-R4`. |
| `summary` | string | no | What the grant is for, when the requirement does not say it. |

### `rbac-grant`

- Defines: one contributed RBAC grant.
- Owner: The part page whose requirement needs the grant. The unit's page lists it in its generated `rbac-grants` section.
- Catalog: `catalogs/contributions.md`.
- Name field: `name`, which gives the anchor's slug.

| Field | Type | Required | Meaning |
| :-- | :-- | :-- | :-- |
| `name` | string | yes | A short name for the grant, unique on the page, such as `podclaims write`. |
| `unit` | page code | yes | The code of the running unit whose ServiceAccount holds the grant, such as `GW`. |
| `service-account` | string | no | The ServiceAccount, when the unit runs under more than one. |
| `api-groups` | list | yes | The API groups of the rule; the core group is `""`. |
| `resources` | list | yes | The resources and subresources of the rule, such as `podclaims/status`. |
| `verbs` | enum list: `*`, `bind`, `create`, `delete`, `deletecollection`, `escalate`, `get`, `impersonate`, `list`, `patch`, `update`, `use`, `watch` | yes | The verbs of the rule. |
| `namespace-scope` | string | yes | Where the grant applies: `cluster`, or the namespace it is bound in, such as `agent namespace`. |
| `requirement` | string | yes | The identifier of the requirement behind the grant or rule, such as `GWCLAIM-R4`. |
| `summary` | string | no | What the grant is for, when the requirement does not say it. |

### `network-rule`

- Defines: one contributed network rule.
- Owner: The part page whose requirement needs the traffic. The unit's page lists it in its generated `network-rules` section.
- Catalog: `catalogs/contributions.md`.
- Name field: `name`, which gives the anchor's slug.

| Field | Type | Required | Meaning |
| :-- | :-- | :-- | :-- |
| `name` | string | yes | A short name for the rule, unique on the page, such as `gateway to adapter`. |
| `unit` | page code | yes | The code of the running unit whose NetworkPolicy carries the rule. |
| `direction` | enum: `egress`, `ingress` | yes | The direction of the traffic, seen from the unit. |
| `peer` | string | yes | The other end, as a component code, a selector, or a CIDR. |
| `ports` | list | yes | The ports, such as `8443/TCP`. |
| `requirement` | string | yes | The identifier of the requirement behind the grant or rule, such as `GWCLAIM-R4`. |
| `summary` | string | no | What the traffic is for, when the requirement does not say it. |

<!-- /generated:definition-kinds -->

An example inside an outer fence is not a block:

````markdown
```lenny-def
kind: setting
name: example
```
````

## Code registry

<!-- generated:code-registry -->

| Code | Type | Path | Summary |
| :-- | :-- | :-- | :-- |
| `CONV` | conventions | [`conventions.md`](conventions.md) | How the fixture specification is written. |
| `CAPS` | index | [`capabilities/index.md`](capabilities/index.md) | Lists the capability areas. |
| `RTPOOL` | index | [`capabilities/pools/index.md`](capabilities/pools/index.md) | Lists the capabilities that keep pods warm. |
| `PSIZE` | capability | [`capabilities/pools/pool-sizing.md`](capabilities/pools/pool-sizing.md) | Sizes a pool. The page is being written, so its blocks stay off the documentation's reference pages. |
| `POOL` | capability | [`capabilities/pools/warm-pools.md`](capabilities/pools/warm-pools.md) | Keeps a pool's pods warm. This page defines the pool and its settings. |
| `CATALERT` | catalog | [`catalogs/alerts.md`](catalogs/alerts.md) | Lists every alert with its severity, the requirements whose breach it detects, and its owner block. |
| `CATOPS` | catalog | [`catalogs/api-operations.md`](catalogs/api-operations.md) | Lists every interface operation with its identifier, method, path, and owner block. |
| `CATCHAN` | catalog | [`catalogs/channels.md`](catalogs/channels.md) | Lists every register with its store, writers, readers, and owner block. |
| `CATCONTR` | catalog | [`catalogs/contributions.md`](catalogs/contributions.md) | Lists every RBAC grant and network rule with the running unit that carries it, the page that states it, and the requirement behind it. |
| `CATCRD` | catalog | [`catalogs/custom-resources.md`](catalogs/custom-resources.md) | Lists every custom resource kind with its group, version, scope, and owner block. |
| `CATERR` | catalog | [`catalogs/errors.md`](catalogs/errors.md) | Lists every error code, admission refusal, and condition reason with its status and owner block. |
| `CATEVT` | catalog | [`catalogs/events.md`](catalogs/events.md) | Lists every event type with its emitting components, its payload, and its owner block. |
| `CATGLOSS` | catalog | [`catalogs/glossary.md`](catalogs/glossary.md) | Lists every owned term with a one-sentence gloss and a link to its owner. |
| `CATIDX` | index | [`catalogs/index.md`](catalogs/index.md) | Lists the catalogs generated from the definition blocks, the terms, and the requirements of the specification's pages. |
| `CATLABEL` | catalog | [`catalogs/labels.md`](catalogs/labels.md) | Lists every label, annotation, finalizer, and readiness gate with its carriers, mutability, and owner block. |
| `CATLIMIT` | catalog | [`catalogs/limits.md`](catalogs/limits.md) | Lists every limit with its value, unit, and owner block. |
| `CATMETRIC` | catalog | [`catalogs/metrics.md`](catalogs/metrics.md) | Lists every metric with its type, labels, emitting components, and owner block. |
| `CATREQ` | catalog | [`catalogs/requirements.md`](catalogs/requirements.md) | Lists every requirement identifier with its title and page. |
| `CATSET` | catalog | [`catalogs/settings.md`](catalogs/settings.md) | Lists every setting with its type, default, range, scopes, the components that read it, its tier presets, and its owner block. |
| `CATSTATE` | catalog | [`catalogs/state-machines.md`](catalogs/state-machines.md) | Lists every state machine with its object, its states, and its owner block. |
| `GW` | component | [`components/gateway/index.md`](components/gateway/index.md) | The gateway as a unit. |
| `GWCLAIM` | component | [`components/gateway/pod-claimer.md`](components/gateway/pod-claimer.md) | The gateway part that claims pods. |
| `COMPS` | index | [`components/index.md`](components/index.md) | Lists the components. |
| `PSC` | component | [`components/pool-scaling-controller.md`](components/pool-scaling-controller.md) | The controller that sizes pools. |
| `WPC` | component | [`components/warm-pool-controller.md`](components/warm-pool-controller.md) | The controller that creates agent pods. |
| `IFS` | index | [`interfaces/index.md`](interfaces/index.md) | Lists the interfaces. |
| `K8S` | interface | [`interfaces/kubernetes/index.md`](interfaces/kubernetes/index.md) | Lenny's Kubernetes API conventions. |
| `KLABEL` | interface | [`interfaces/kubernetes/labels-and-annotations.md`](interfaces/kubernetes/labels-and-annotations.md) | The labels and annotations Lenny defines. |
| `KPCLAIM` | interface | [`interfaces/kubernetes/podclaim.md`](interfaces/kubernetes/podclaim.md) | The PodClaim custom resource. |

<!-- /generated:code-registry -->
