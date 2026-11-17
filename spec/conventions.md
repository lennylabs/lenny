---
code: CONV
type: conventions
summary: >-
  How the specification is read and written: page types, the placement procedure, owners of enumerable facts, definition blocks, identifiers, references and moves, successor stubs and the frozen reference, the gloss rule, the page template, page status, normative language, and the code registry.
---

# Conventions

This page states how the specification under `spec/` is organized and how its pages are written. [README.md](README.md) is the root map. The pages of the specification tree live in the folders this page names. The numbered files `01_*.md` to `29_*.md` hold the content of every page that is not yet written, and each placeholder page lists the numbered sections that hold its content. An area's migration moves that content into the area's pages and leaves a successor stub at each migrated heading of a numbered file.

The specification has these goals:

- Every fact is stated once, on the page whose type owns that kind of fact. Every other mention is a link, optionally with a one-sentence gloss.
- A reader, human or agent, reaches any fact by descending from the root map through an index page and a page summary, without loading unrelated pages.
- Identifiers do not move when the tree is reorganized.

## Normative language

The keywords MUST, MUST NOT, REQUIRED, SHALL, SHALL NOT, SHOULD, SHOULD NOT, RECOMMENDED, MAY, and OPTIONAL carry the meanings that BCP 14 (RFC 2119 and RFC 8174) gives them when they appear in upper case. A requirement uses them to state its rule. Overview pages, walkthrough pages, index pages, catalogs, and glosses carry no normative keyword. In those, the lower-case words "must", "shall", and "should" also count as normative keywords, because they state a rule in either case.

## Page types

| Type | Folder | Owns | Does not own |
| :-- | :-- | :-- | :-- |
| Overview | `overview/` | The system's purpose and context (actors and external systems), principles, non-goals, and the maps of areas, capabilities, components, and interfaces. | Any rule, value, or behavior. |
| Walkthrough | `overview/walkthroughs/` | A non-normative trace of one scenario across capabilities and components. Each step links the requirement and the operation it exercises. | Any rule. |
| Capability | `capabilities/<area>/<capability>.md`, or a folder when the capability splits | What the system does end to end: the domain objects the capability governs (definition, identity, relationships, states and state machine, and invariants), the behavioral rules as numbered requirements, the flows across components, the behavioral settings, the errors a single rule of the capability produces, the metrics that measure the capability and the alerts that detect a breach of its requirements, failure behavior, security properties and threats, limits (what the capability does not guarantee), and the current rationale. | How a component is deployed or run as a process. The syntax of an interface. History and alternatives considered. |
| Component | `components/<component>.md`, or a folder for a unit with internal parts | A running unit: a process, a controller, a container, a Job, or an external dependency Lenny relies on. It owns the unit's responsibilities (each a link to the requirement it carries out), the interfaces it serves and consumes, its internal parts, the requirements of mechanisms only this implementation has (numbered with the component's code), its deployment properties (replicas, leader election, scaling, probes, resources, ServiceAccount, RBAC, and NetworkPolicy), its process settings, the list of every setting its process reads, its failure modes as a unit and how it recovers, the metrics and alerts about its health as a unit, and the registers only it reads and writes. | A rule that any implementation of the same responsibilities must meet. That rule stays on the capability. |
| Interface | `interfaces/<interface>/...` | A contract one party can be built against without reading the other party: the client, admin, and ops APIs by resource group, the runtime protocol and its SDKs, the adapter manifest, the gateway-to-pod channels and the registers two components share, Lenny's Kubernetes resources (custom resources, labels and annotations, and the admission outcomes a Kubernetes client sees), the request interceptor protocol, the extension points a deployer can replace, the CLI, event schemas, the WorkspacePlan schema, stored schemas, and deployment configuration. It owns syntax, operations with their identifiers, fields with wire names, types, and syntactic constraints, the error codes each operation returns (by link to the code's owner), authentication and authorization requirements, and versioning. | The rules Lenny applies to decide an outcome, the meaning and range of a setting, and the meaning of an error code. |
| Cross-cutting | `cross-cutting/` | A property or convention every capability and component upholds: the security model and system threat model, tenant isolation, mTLS and the PKI, availability and failure principles, upgrade and compatibility policy, observability conventions and the metrics every controller emits, the error model and the error codes several rules share, the API conventions several interfaces share (pagination, ETags, dry run and confirm, idempotency keys, and correlation and rate-limit headers), data classification and retention, and naming laws. | How one capability meets the property. The capability states that, and the cross-cutting page lists the capabilities that carry it. |
| Catalog | `catalogs/` | No fact. Each catalog is generated from the definition blocks on owner pages: settings and defaults (with tier presets), limits, errors, metrics, alerts, events, labels and annotations, custom resources, API operations, requirements, state machines, channels, and the glossary. | Any definition. |
| Index | `index.md` in every folder | The folder's summary, its reading paths, the table of its pages with their summaries, and the components and interfaces that serve it. | Any fact a page in the folder owns. |
| Conventions | `README.md`, `conventions.md` | How to read and write the specification. | Content about Lenny. |

An index page owns content of its own only where this page assigns it, as for the gateway seam model under "Gateway seams" and the registers under "Registers".

## Placement procedure

Ask the questions in order and stop at the first yes. Apply the procedure to each paragraph. A heading is too coarse a unit, because the paragraphs under one heading often belong to several pages.

1. Is it about how to read or write the specification? It belongs to the conventions.
2. Is it a plan, a decision history, a comparison with other products, an open question, or a roadmap item? Is it guidance (sizing, tuning, or worked examples), a procedure (a runbook), a benchmark or calibration method, a test plan, a client protocol mapping, or a contributor procedure? It leaves the specification for the destination that "Destinations outside the specification" names.
3. Is it a value in an enumerable set, such as an error code, a metric, an alert, a setting, a label, an event type, a custom resource kind, or a state? Define it in a definition block on its owner page (see "Owners of enumerable facts"), and let the catalog list it.
4. Can a party be built against it without knowing how the other party decides? It is an interface fact: syntax, fields, operations, the codes returned, authentication, or versioning.
5. Does every capability or component have to uphold it? It belongs to a cross-cutting page.
6. Is it about one running unit as a unit, or about a mechanism only that implementation has? It belongs to the component.
7. Is it about what the system does, decides, or guarantees, or what a domain object is? It belongs to the capability.
8. Does it trace a scenario across capabilities without adding a rule? It belongs to a walkthrough.
9. Is it purpose, context, or a map? It belongs to the overview.

**Interface or capability.** A statement that holds for any implementation behind the interface and is visible to the caller belongs to the interface, for example "`POST /v1/admin/pools` returns `409 RESOURCE_ALREADY_EXISTS` when a pool with the name exists". A statement about how Lenny decides belongs to the capability, for example "a pool name is refused when it is not a valid Kubernetes label value, because …". The interface lists the code and links its owner. The capability states the rule and links the operation.

**Component or capability.** If replacing the component with a different implementation of the same responsibilities would leave the statement true, the statement belongs to the capability. Otherwise it belongs to the component, which numbers it as its own requirement when it is normative.

**Go types.** A Go type inside one binary is a component's internal part. The component page describes it in words where a reader needs it, and package documentation holds its signature. A Go interface a deployer can replace is an extension-point interface page.

## Facts that are both rule and syntax

Some facts have a rule, with a reason, and a syntactic form an interface exposes. Each is stated once, and the other side links it.

| Fact | Stated on | The other side |
| :-- | :-- | :-- |
| A field that carries a setting | The setting's definition block on the capability: meaning, default, range, and scopes. | The interface states the wire name, the type, and the codes returned, and links the setting. |
| A field whose values are a capability's states | The capability's model. | The interface field's value list is generated from the model. |
| A naming rule for an object, such as `claim-<podName>` or `<pool>.<key16>` | The capability requirement. | The interface field's constraint links the requirement. |
| A label's mutability | The entry on the labels and annotations page: key, values, carriers, writers, readers, and mutability. | The capability requirement states the reason and links the entry. A resource page's admission table lists the refusal and links the entry. |

## Owners of enumerable facts

| Fact | Owner |
| :-- | :-- |
| Error code produced by one rule | The capability whose rule produces it. Each interface that returns it lists it by link. |
| Error code with several causes | The cross-cutting error model. |
| Behavioral setting | The capability whose object it configures. Other users link it. |
| Process or deployment setting | The component. |
| Metric that measures a capability's behavior | The capability. The definition block names its emitter, and the emitter's component page lists it. |
| Metric about a unit's health as a process | The component. |
| Metric every controller emits | The cross-cutting observability conventions page. |
| Alert that detects a breach of a requirement | The owner of the requirement. |
| Alert that detects a unit's health | The component. |
| Event type | The interface that carries it, with the emitter named in the definition block. |
| Label or annotation key | The Kubernetes labels and annotations page. |
| Custom resource kind | Its Kubernetes interface page. |
| Domain object and its state machine | The capability that governs its lifecycle. When the capability splits into a folder, the object and its state machine stay on one page named for the object. |
| Register | The page that "Registers" names. |
| Limit or default, including a per-tier default | The owner of the setting that carries it. Tier preset files are generated. |

## Definition blocks

A definition block defines one enumerable fact on its owner page. It is a fenced block with the info string `lenny-def`, holding YAML, and it is preceded by its anchor on a line of its own:

````markdown
<a id="setting-gateway-claimholdttlseconds"></a>

```lenny-def
kind: setting
name: gateway.claimHoldTTLSeconds
type: integer
default: 10
range: [0, 300]
scopes: [platform, pool]
defaults-by-tier: {small: 10, medium: 10, large: 10}
read-by: [GWCLAIM]
summary: How long a recycled pod's claim stays reserved for its tenant's next session.
```
````

Every block carries `kind` and the name field its kind's schema names: `name` for most kinds, `type` for an event, and `key` for a label, an annotation, a finalizer, or a readiness gate. The `summary` is one sentence that says what the fact is; a contribution block may leave it out when its requirement says what the block is for. Metric and event blocks carry `emitter:`, and setting blocks carry `read-by:`, so the component pages' lists of what a unit emits and reads are generated. Each kind's schema fixes the other fields.

**Admission refusals with no code.** A migration states the designed target and adds no behavior, so it invents no error code. A refusal that carries a message and no code is an `admission-refusal` block, keyed by name, with its exact message. Giving the refusal a machine-readable code is a change of its own.

**Contribution blocks.** When several part pages of one running unit each need something from the unit, each part states its need in a contribution block that names the requirement behind it, and the unit's page carries the generated table. Settings (`read-by`) and metrics (`emitter`) already follow this pattern. RBAC grants (`rbac-grant`) and network rules (`network-rule`) are contribution kinds, and each names the running unit in its `unit` field. A grant that the unit needs as a whole, rather than for one part, is a `grant` block on the unit's own page. A gate checks each `grant`, `rbac-grant`, and `network-rule` block against the chart's rendered Role and NetworkPolicy. Listeners and ports, flags and environment variables, and volumes and mounts become contribution kinds when a page needs them.

**Kinds.** The kinds and their schemas follow. `specshift defs generate` writes them from the schemas that `specshift defs check` applies, so the list below and the check agree.

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

## Registers

The narrowest page that covers every writer owns a register:

- the page of one part of a component, when only that part writes it;
- the component's index page, when several of its parts write it;
- `interfaces/gateway-seams.md`, when it crosses a gateway seam;
- an interface page, when it crosses components.

The owner defines the register in a `register` block.

## Identifiers

**Page codes.** Every page has a code of two to nine upper-case letters with no hyphen and no digit, registered in the code registry below. A page's code does not change when its title or path does. The registry refuses a code registered twice, and a code that extends an existing code by one letter (`DERIVE` beside `DERIVED`).

**Requirements.** A requirement is numbered per page and carries a title and an explicit anchor equal to its lower-cased identifier:

```markdown
<a id="pool-r5"></a>**POOL-R5. Pool and pod-set labels are required and immutable.** Text of the rule.
```

Numbers are never reused. A retired requirement keeps its number with a successor pointer.

**Issues.** An issue about a requirement of a page is identified `<CODE>-I<n>` and lives under `issues/<CODE>/` as `<CODE>-I<n>-<slug>.md`. Issue numbers are never reused within a code.

**Operations.** An interface operation has the identifier `<CODE>-<operation name>`, where the operation name is the interface's own: the gRPC method name (`ADAPTERRPC-ReportPodScrub`), the OpenAPI `operationId` (`ADMPOOLS-createPool`), the Kubernetes API verb (`KPCLAIM-create`, `KPCLAIM-updateStatus`), or the CLI command words (`CLIPOOL-poolCreate`). Its anchor is the identifier lower-cased (`kpclaim-create`). Each REST operation has an `operationId` in the specification, and the generated OpenAPI document and SDKs use it.

**Grammar.** Every identifier has the form `<CODE>-<suffix>`. A suffix of `R` and a number is a requirement, a suffix of `I` and a number is an issue, and any other suffix is an operation. The registry refuses a collision.

**Terms.** An owned term carries an explicit anchor equal to its slug, placed in the term's table cell: `<a id="reserved-hold"></a>Reserved hold`. A term anchor is unique across the specification. When two concepts would share a word, each takes a qualified name (`pod-phase` and `claim-phase`).

**Citations from code.** Code, tests, issues, and change records cite identifiers and never paths: a requirement (`POOL-R5`), an operation (`ADMPOOLS-createPool`), or a definition block's anchor. An identifier survives any move of its page.

## References and moves

The tree is reorganized after it lands. These rules make a move a mechanical step.

- **Every link targets an identifier.** A link into the specification ends at a page, by its path, or at an identifier anchor: a requirement (`#pool-r5`), an owned term (`#reserved-hold`), an operation (`#kpclaim-create`), or a definition block (`#setting-maxidleseconds`). No link from another page targets a heading anchor. Template headings such as "Requirements" are never link targets.
- **The path is derived data.** Links are ordinary relative Markdown links, so a reader follows them without a resolver. A reference tool builds the registry of identifiers from front matter and anchors, and it rewrites the path of every link whose target or fragment is a registered identifier. Moving a page is `git mv` followed by one run of the tool.
- **Code carries no paths.** Annotations in code, test names, issue front matter, and change records cite identifiers, so a page move changes no code.
- **Moving one requirement.** A requirement that moves to another page takes that page's next number. The old identifier is retired with a successor pointer, and the registry records the move. The tool rewrites every citation from that record, code included.
- **Merging and renaming pages.** A merged page keeps one code. The other code stays in the registry as an alias and resolves indefinitely.
- **Domain.** The tool rewrites the tracked tree except the archives: `changes/archive/`, `proposals/`, and the frozen gap records. Those keep the paths they were written with, and their identifiers resolve through the tool's lookup command.
- **The published site.** The documentation site has a redirect for each moved page, generated from the registry's history of moves.
- **Gates.** Every link into the specification resolves. Every fragment is a registered identifier. No code is registered twice, and no identifier is retired without a successor or an explicit removal record.

A section-number citation of a numbered file resolves through the successor stub at the section's heading once the section's content has migrated, as "Successor stubs" states.

## Successor stubs

An area change that migrates the content of a numbered section turns the section into a successor stub. The stub keeps the section's heading and anchor, and a successor list that links the pages and identifiers now holding the content replaces the migrated paragraphs. A section citation (`§N.M`) therefore reaches the same heading before and after the migration, and the heading names where the content went.

The heading and the anchor of a numbered section do not change while any section citation points at them. A stub edits the body of a section and leaves its heading as it was.

**Full stub.** A section whose content has all moved carries the marker line `<!-- successor-stub -->` directly under its heading, followed by the successor list and no other text. The gates and the citation tools read the marker to tell a full stub from a section that still holds content.

```markdown
### N.M Section title

<!-- successor-stub -->

The content of this section has moved to these pages and identifiers:

- [POOL-R5](capabilities/runtimes-and-pools/warm-pools.md#pool-r5): Pool and pod-set labels are required and immutable.
- [POOL](capabilities/runtimes-and-pools/warm-pools.md): Warm pools
```

**Partial stub.** A section that keeps part of its content carries no marker. Its successor list opens the section, and the kept text follows the list unchanged until its own area migrates.

```markdown
### N.M Section title

Part of the content of this section has moved to these pages and identifiers:

- [CLAIM-R10](capabilities/runtimes-and-pools/pod-claims.md#claim-r10): Title of the requirement.

The paragraphs that have not migrated.
```

**Successor list.** Each entry links one page or identifier by a path relative to the numbered file and gives its title: the bold title of a requirement, the heading of a page, the kind and name of a definition block, or the text of an owned term. Every entry targets a page or an identifier anchor, as "References and moves" requires of every link into the tree.

**Writing a stub.** Run `specshift ref stub §N.M --to <identifier>[,<identifier>...]` to write a full stub, and add `--keep` to write a partial stub. An entry of `--to` is a page code or alias, a page path, a requirement or operation identifier, or a term or definition-block anchor. The command builds the list from the registry and refuses a name the registry does not hold or holds only as a retired identifier. It refuses a section with subsections unless `--recursive` is given, and with `--recursive` every heading under the section becomes a stub with the same list. Stubbing a section that already carries a list adds the new entries to that list, and stubbing a partial stub without `--keep` removes the kept text and writes the marker.

## Frozen reference

The git tag `spec-frozen` marks the commit before the first area change edits the specification, and that change creates the tag. The numbered files at the tag are the frozen reference: they hold the original text of every numbered section after its content has migrated. No copy of the numbered files stays in the tree.

Run `specshift ref lookup §N.M` to read a section citation. The command prints the section's file and anchor, states whether the section is a full stub, a partial stub, or not yet migrated, lists the successors its stub names, and prints the section's text at `spec-frozen`. Before the tag exists, the command states that the tag does not exist yet. To read a whole numbered file as it stood, run `git show spec-frozen:spec/NN_file-name.md`.

## Gloss rule

A page that uses a term it does not own gives a one-sentence gloss and a link to the owner. A gloss says what the thing is. It never states a value, a default, a rule, or a limit. A gloss holds no number, no normative keyword, and exactly one link. A borrowed term's row in the page's Terms table is its gloss: a row whose term cell carries no anchor, with the gloss in its second cell and the one link anywhere in the row.

## Page template

Every page opens with front matter. The renderer turns the summary into the opening paragraph of a content page, so the body of a content page does not repeat it.

```yaml
---
code: POOL
type: capability   # overview | walkthrough | capability | component | interface | cross-cutting | catalog | index | conventions
area: runtimes-and-pools
summary: >-
  Two to four sentences that state what the page covers, used by the index pages.
owns: [objects, requirements, settings, errors, metrics, alerts]
depends-on: [RUNTIME, REV, PLACE]
status: partial    # absent on a page written in full
---
```

`code`, `type`, and `summary` are required on every page. `area` is required on every page under `capabilities/<area>/`. `status` is described under "Page status".

Every content page then has **Terms** (owned terms with definitions, and borrowed terms with a gloss and a link) and **Overview** (a plain-language explanation without normative values), followed by the sections of its type:

| Type | Sections after the overview |
| :-- | :-- |
| Capability | Model (objects, states, and invariants); Requirements; Flows (each step links a component and an operation); Settings; Errors; Metrics and alerts; Failure behavior; Security properties and threats; Limits; Rationale; Related capabilities. |
| Component | Responsibilities; Interfaces served and consumed; Internal parts; Requirements of its own mechanisms; Deployment; Settings read (generated, with process settings defined here); Failure and recovery; Metrics and alerts (its own definitions, then the generated list of what it emits). |
| Interface | Conventions for this interface, or a link to the cross-cutting API conventions; Operations or resources, each with fields, constraints, codes returned, and the requirements behind each outcome; Compatibility. |
| Cross-cutting | The property; the rules every page upholds; the pages that carry it, with the requirement each states. |
| Walkthrough | The scenario; numbered steps, each linking a requirement and an operation. |

An index page's table of the folder's pages and subfolders, with their summaries, is generated from the pages' front matter by `specshift defs generate`. The page holds its front matter, the folder's summary as a paragraph, and that table. Any section the generator does not write, such as "Content today", is kept as written.

## Page status

The front matter field `status` records how much of a page is written.

- A page written in full has no `status` field.
- `status: partial` marks a page written in part. The front matter or the body names what is missing.
- `status: placeholder` marks a page that is not yet written. A placeholder holds its front matter, its title, and a "Content today" section that links the numbered sections holding its content and the changes whose content lands on it. It registers no identifier.

No link into a placeholder carries a fragment. When a placeholder becomes a full page, the reference tool lists the page-level links into it so that each can move to an identifier.

## Review comments during migration

While an area migrates, its pages may carry review comments that the renderer drops:

- `<!-- src: ... -->` names the numbered section and bold label, or the design, that a paragraph came from.
- `<!-- judgment: ... -->` records a placement or a reading that needed semantic judgment.
- `<!-- unresolved: ... -->` records a fact the sources do not settle.

These comments exist only while the area migrates. Before the area lands, each `unresolved` comment becomes an issue or an open decision on the area's change, and the `src` and `judgment` comments move into the migration record in the change's `evidence/` folder. The gates refuse all three on a page whose `status` is neither `placeholder` nor `partial`.

## Page size

- A content page stays under about 6,000 words.
- An index page stays under about 1,500 words.
- A capability that carries a sizing formula or a sub-procedure longer than about 1,000 words moves it to a sub-page.

The gates report a page over these sizes.

## Destinations outside the specification

| Content | Destination |
| :-- | :-- |
| The phased build sequence, phase gates, and milestones | A program under `changes/programs/`. |
| Absent-behavior claims and open questions | An issue under `issues/<CODE>/`. |
| Decision history and alternatives | The archived change under `changes/archive/`, or the proposal under `proposals/`. |
| Roadmap and post-v1 items | The program's milestones, and `docs/about/status.md`, which is generated from them. |
| Comparison with other products and community strategy | `docs/about/`. |
| Benchmarks, calibration methods, and the tests that verify a requirement | `TESTING.md`. A requirement may name its test tiers. |
| Sizing, tuning, and worked examples | `docs/operator-guide/`. |
| Runbook procedures | `docs/runbooks/`. The runbook format stays in the specification. |
| Client protocol mappings (MCP, OpenAI, and A2A) | The adapter repositories. |
| Reference runtime descriptions | The reference runtime repositories and `docs/runtime-author-guide/`. The runtime protocol and its conformance suite stay in the specification. |
| Contributor procedures | `CONTRIBUTING.md` and the contributor documentation under `docs/about/`. |
| Go signatures of internal types | Package documentation. |

## Gateway seams

The gateway is one binary partitioned into seams. A seam is a part of the gateway that can be extracted into its own service when its load outgrows the shared process. Each seam has its own concurrency limits and queue depth, its own in-process circuit breaker, its own metrics, and extraction thresholds that state when to split it out. The seams are the stream proxy, the upload handler, the platform tools seam, and the metering proxy.

- The gateway folder's index page, `components/gateway/index.md`, owns the seam model: the isolation guarantees every seam has, the rule that a seam reaches another seam's state only through its seam interface, and the gateway's deployment as a unit.
- Each seam is a component page in the gateway folder. It owns the seam's responsibilities, its concurrency and breaker settings, its metrics, its extraction thresholds, and its failure mode while it is not extracted. The calibration method for the thresholds belongs in `TESTING.md`.
- The work that is not a seam is the session core: session coordination, the pod claimer, and the checkpoint barrier. Each is a component page in the gateway folder, and the index lists them as the core.
- Drain, scaling and capacity, the ops endpoints, and the request pipeline (`components/gateway/api-front.md`: middleware order, authentication, rate limiting, handler registration, and request scope) apply to the whole replica, and the index lists them under the replica. The request pipeline is a part page of the replica and is not a seam.
- Each seam's boundary with the core is the interface page `interfaces/gateway-seams.md`. It lists the operations each seam calls on the core, such as session lookup, the coordination lease, and the caller's authorization context, and the operations the core calls on the seam. Extracting a seam puts a transport under an interface that already exists. A gate checks that a seam's Go package imports only its seam interface from the core.

**Domain stores.** The rules of a domain store live on capability pages, its schemas on the stored-schema interface pages, and the store itself under `components/external/`. In code, packages follow components for running units and capability areas for domain logic that several components share.

## Code registry

The table lists every page code with its page type, its path relative to `spec/`, and its summary. `specshift defs generate` writes it from the front matter of the pages, and the folder index pages read the same front matter, so a row's summary is the text the index shows. A code is registered by giving a page front matter that carries it, and `specshift ref check` refuses a code that does not match the grammar under "Identifiers" or that two pages carry.

<!-- generated:code-registry -->

| Code | Type | Path | Summary |
| :-- | :-- | :-- | :-- |
| `README` | conventions | [`README.md`](README.md) | Root map: what Lenny is in one paragraph, the capability areas, and reading paths by task. |
| `CONV` | conventions | [`conventions.md`](conventions.md) | How the specification is read and written: page types, the placement procedure, owners of enumerable facts, definition blocks, identifiers, references and moves, successor stubs and the frozen reference, the gloss rule, the page template, page status, normative language, and the code registry. |
| `BILLEV` | capability | [`capabilities/billing-and-usage/billing-events.md`](capabilities/billing-and-usage/billing-events.md) | States the billing event stream: what emits each event, delivery sinks, immutability, and corrections. |
| `BUDGET` | capability | [`capabilities/billing-and-usage/budgets.md`](capabilities/billing-and-usage/budgets.md) | Defines credit budgets, budget inheritance, and the delegation budget counters. |
| `BILLAREA` | index | [`capabilities/billing-and-usage/index.md`](capabilities/billing-and-usage/index.md) | Lists the capabilities that meter usage, enforce budgets, and emit billing events. |
| `RATE` | capability | [`capabilities/billing-and-usage/rate-cards.md`](capabilities/billing-and-usage/rate-cards.md) | Defines rate cards and how a usage record is priced against them. |
| `ATTR` | capability | [`capabilities/billing-and-usage/resource-attribution.md`](capabilities/billing-and-usage/resource-attribution.md) | Attributes pod time, idle pods, stored bytes, and dedicated nodes to tenants, with reconciliation against the object store. |
| `USAGE` | capability | [`capabilities/billing-and-usage/usage-records.md`](capabilities/billing-and-usage/usage-records.md) | Defines usage records for LLM calls and resources and the usage the client API reports. |
| `LINK` | capability | [`capabilities/credentials/account-links.md`](capabilities/credentials/account-links.md) | States how a user links an account through OAuth, the link prompt, and the link check before a session needs the account. |
| `ADDON` | capability | [`capabilities/credentials/credential-add-ons.md`](capabilities/credentials/credential-add-ons.md) | Defines the credential add-ons for GitHub and GitLab, their kinds, and their deny presets. |
| `CDELIV` | capability | [`capabilities/credentials/credential-delivery.md`](capabilities/credentials/credential-delivery.md) | States how credentials reach a runtime: delivery modes, file delivery, and what a runtime may read. |
| `CPOOL` | capability | [`capabilities/credentials/credential-pools.md`](capabilities/credentials/credential-pools.md) | Defines credential pools for VCS providers, their secret topology, leases, governance boundaries, emergency revocation, and secret lifecycle. |
| `CREQ` | capability | [`capabilities/credentials/credential-request-rules.md`](capabilities/credentials/credential-request-rules.md) | States the request rules and presets that decide which credentials a session may request and receive. |
| `CREDAREA` | index | [`capabilities/credentials/index.md`](capabilities/credentials/index.md) | Lists the capabilities that hold credentials and deliver them to sessions and pods. |
| `PRAUTH` | capability | [`capabilities/credentials/pull-request-authoring.md`](capabilities/credentials/pull-request-authoring.md) | States how a session authors pull and merge requests under a user's identity and the independent approval check. |
| `REPOAUTH` | capability | [`capabilities/credentials/repository-authorization.md`](capabilities/credentials/repository-authorization.md) | States which repositories a session may reach under a user's VCS identity and how that authorization is decided. |
| `SACRED` | capability | [`capabilities/credentials/service-account-credentials.md`](capabilities/credentials/service-account-credentials.md) | Lets a service account own credentials through the client credentials and issuer exchange sources. |
| `UCRED` | capability | [`capabilities/credentials/user-credentials.md`](capabilities/credentials/user-credentials.md) | Defines user credential types, the registered source, the user credential binding, and request rules and presets. |
| `DAVAIL` | capability | [`capabilities/delegation/delegation-availability.md`](capabilities/delegation/delegation-availability.md) | States the delegation levels, how a session's level is resolved, the refusal when delegation is unavailable, and the tool list a runtime receives. |
| `LEASE` | capability | [`capabilities/delegation/delegation-lease.md`](capabilities/delegation/delegation-lease.md) | Defines the delegation lease, its fields and inheritance, tree-wide limits, isolation monotonicity, and lease extension. |
| `DPOL` | capability | [`capabilities/delegation/delegation-policy.md`](capabilities/delegation/delegation-policy.md) | Defines the delegation policy resource, tag evaluation, the session-level policy cap, content policy, and the scanning of exported files. |
| `DLG` | capability | [`capabilities/delegation/delegation.md`](capabilities/delegation/delegation.md) | States the delegation mechanism, approval modes, the task tree, task records and results, and what a parent sees of its children. |
| `DLGAREA` | index | [`capabilities/delegation/index.md`](capabilities/delegation/index.md) | Lists the capabilities that let a session start child sessions and govern the resulting tree. |
| `LEASEXT` | capability | [`capabilities/delegation/lease-extension.md`](capabilities/delegation/lease-extension.md) | States how a delegation lease is extended, the configuration layering, and the extension approval modes. |
| `DTREE` | capability | [`capabilities/delegation/tree-recovery-and-cascade.md`](capabilities/delegation/tree-recovery-and-cascade.md) | States how a delegation tree recovers, cascades on a parent's end, cleans up orphans, and how parents watch children's streams. |
| `BACKUP` | capability | [`capabilities/deployment-and-lifecycle/backup-and-restore.md`](capabilities/deployment-and-lifecycle/backup-and-restore.md) | States backup creation, scheduling, retention, verification, restore with its safety checks, and disaster recovery targets. |
| `TIER` | capability | [`capabilities/deployment-and-lifecycle/capacity-tiers.md`](capabilities/deployment-and-lifecycle/capacity-tiers.md) | Defines the capacity tiers, the scale each tier targets, and the workload assumptions behind them. |
| `DEPAREA` | index | [`capabilities/deployment-and-lifecycle/index.md`](capabilities/deployment-and-lifecycle/index.md) | Lists the capabilities that install, size, upgrade, back up, and restore a Lenny deployment. |
| `INSTALL` | capability | [`capabilities/deployment-and-lifecycle/installation.md`](capabilities/deployment-and-lifecycle/installation.md) | States what installation creates, the bootstrap seed, namespace quotas, image registries and air-gapped installs. |
| `LOCALDEV` | capability | [`capabilities/deployment-and-lifecycle/local-development.md`](capabilities/deployment-and-lifecycle/local-development.md) | States the local development modes, the embedded stack, its backends, and its guard rails. |
| `UPGRADE` | capability | [`capabilities/deployment-and-lifecycle/platform-upgrades.md`](capabilities/deployment-and-lifecycle/platform-upgrades.md) | States the platform upgrade state machine, upgrade checks and release channels, schema migration, CRD upgrades, and configuration diff and apply. |
| `CAPS` | index | [`capabilities/index.md`](capabilities/index.md) | Lists the capability areas with their summaries and the capabilities in each. |
| `CTOOL` | capability | [`capabilities/messaging/client-tools.md`](capabilities/messaging/client-tools.md) | Lets a client supply tools a runtime calls, with admission, response validation, pending calls, and continuation. |
| `STREAM` | capability | [`capabilities/messaging/event-stream.md`](capabilities/messaging/event-stream.md) | States the session event stream: event kinds and their order, activity, output and reasoning streaming, turn and usage events, resume after a gap, and back-pressure. |
| `INBOX` | capability | [`capabilities/messaging/inbox-and-custody.md`](capabilities/messaging/inbox-and-custody.md) | States the session inbox, its states and custody, durability, expiry, retraction, dead-lettering, and delivery receipts. |
| `MSGAREA` | index | [`capabilities/messaging/index.md`](capabilities/messaging/index.md) | Lists the capabilities that carry messages into a session and output out of it: delivery, inboxes, inter-session messages, the event stream, input requests, and client tools. |
| `INPUT` | capability | [`capabilities/messaging/input-requests.md`](capabilities/messaging/input-requests.md) | States how a runtime asks the client for input, the elicitation chain through delegation, harness approvals, and the session's input policy. |
| `IMSG` | capability | [`capabilities/messaging/inter-session-messaging.md`](capabilities/messaging/inter-session-messaging.md) | States how one session messages another: scope, the configuration hierarchy, rate limits, content policy, and threading. |
| `MSG` | capability | [`capabilities/messaging/message-delivery.md`](capabilities/messaging/message-delivery.md) | States the delivery intents, the delivery paths, turn accounting, and the executor that delivers one message at a time to the runtime. |
| `DNS` | capability | [`capabilities/network-and-egress/dns.md`](capabilities/network-and-egress/dns.md) | States the DNS resolution agent pods get and the mitigations against DNS exfiltration. |
| `EGDEC` | capability | [`capabilities/network-and-egress/egress-decisions.md`](capabilities/network-and-egress/egress-decisions.md) | States how each outbound connection is attributed to a slot, decided against the effective policy, and recorded in the evidence streams. |
| `EGDEF` | capability | [`capabilities/network-and-egress/egress-defaults.md`](capabilities/network-and-egress/egress-defaults.md) | States the default egress policy, how a scope replaces or augments it, exfiltration controls, and TLS interception. |
| `EGRESS` | capability | [`capabilities/network-and-egress/egress-policy.md`](capabilities/network-and-egress/egress-policy.md) | States the egress policy model: scopes, the effective policy, defaults, replacement and augmentation, exfiltration controls, and TLS interception. |
| `GIT` | capability | [`capabilities/network-and-egress/git-access.md`](capabilities/network-and-egress/git-access.md) | States how a pod reaches git hosts: covered hosts, the git mirror, and the git configuration injected into a slot. |
| `NETAREA` | index | [`capabilities/network-and-egress/index.md`](capabilities/network-and-egress/index.md) | Lists the capabilities that decide what network traffic agent pods may send and how it is attributed. |
| `LLMUP` | capability | [`capabilities/network-and-egress/llm-upstreams.md`](capabilities/network-and-egress/llm-upstreams.md) | Defines LLM upstreams, their assignment to sessions, the metering proxy's routing rules, cache isolation, calls outside the proxy, and forwarded attribution. |
| `NETISO` | capability | [`capabilities/network-and-egress/network-isolation.md`](capabilities/network-and-egress/network-isolation.md) | States the default-deny baseline and the allow-lists that apply to agent pods and to the platform namespace. |
| `OPSVC` | capability | [`capabilities/network-and-egress/operator-services.md`](capabilities/network-and-egress/operator-services.md) | States how a runtime reaches operator services through egress, the identity assertion attached to those requests, and the egress halt at suspension. |
| `OUTCONN` | capability | [`capabilities/network-and-egress/outbound-connector.md`](capabilities/network-and-egress/outbound-connector.md) | Lets a tenant reach hosts on its private network through an agent it runs, with registration, identity, liveness, rotation, and revocation. |
| `ALERTSLO` | capability | [`capabilities/observability-and-operations/alerting-and-slos.md`](capabilities/observability-and-operations/alerting-and-slos.md) | States the SLO targets, burn-rate alerting, the bundled alert set and its operator customization, and SLO export. |
| `DRIFT` | capability | [`capabilities/observability-and-operations/configuration-drift.md`](capabilities/observability-and-operations/configuration-drift.md) | States how drift between the declared and the running configuration is detected and reconciled. |
| `DIAG` | capability | [`capabilities/observability-and-operations/health-and-diagnostics.md`](capabilities/observability-and-operations/health-and-diagnostics.md) | States how platform health and capacity recommendations are derived, the diagnostic operations, and automatic remediation. |
| `OPSAREA` | index | [`capabilities/observability-and-operations/index.md`](capabilities/observability-and-operations/index.md) | Lists the capabilities that let operators and DevOps agents observe, diagnose, and remediate the platform. |
| `OPSEV` | capability | [`capabilities/observability-and-operations/operational-events.md`](capabilities/observability-and-operations/operational-events.md) | States which state changes emit operational events, buffering in the gateway, and delivery by stream, polling, and webhook. |
| `REMED` | capability | [`capabilities/observability-and-operations/remediation-and-escalation.md`](capabilities/observability-and-operations/remediation-and-escalation.md) | States remediation locks, their consistency, stealing and long holds, the operations inventory, and escalation. |
| `RUNBOOK` | capability | [`capabilities/observability-and-operations/runbooks.md`](capabilities/observability-and-operations/runbooks.md) | States the machine-readable runbook format and how runbooks are discovered from health results and alerts. |
| `ADMIT` | capability | [`capabilities/policy-and-controls/admission-and-fairness.md`](capabilities/policy-and-controls/admission-and-fairness.md) | States the policy engine, the admission checks and their order, and fairness between tenants. |
| `BREAKER` | capability | [`capabilities/policy-and-controls/circuit-breakers.md`](capabilities/policy-and-controls/circuit-breakers.md) | States the operator-managed circuit breakers, their storage and propagation, and their effect on admission. |
| `POLAREA` | index | [`capabilities/policy-and-controls/index.md`](capabilities/policy-and-controls/index.md) | Lists the capabilities that admit, rate-limit, and refuse requests. |
| `QUOTA` | capability | [`capabilities/policy-and-controls/quotas-and-rate-limits.md`](capabilities/policy-and-controls/quotas-and-rate-limits.md) | States the quota hierarchy, storage quotas, rate limits, counter recovery, and the fail-open budgets during a store outage. |
| `INTCPT` | capability | [`capabilities/policy-and-controls/request-interceptors.md`](capabilities/policy-and-controls/request-interceptors.md) | States where request interceptors run, how their verdicts apply, their failure policy, and in-cluster and tenant-scoped interceptors. |
| `APOD` | capability | [`capabilities/runtimes-and-pools/agent-pod-lifecycle.md`](capabilities/runtimes-and-pools/agent-pod-lifecycle.md) | Defines the agent pod and its states and how a pod is created, becomes ready, is recycled, drained, quarantined, and deleted. |
| `RTPOOL` | index | [`capabilities/runtimes-and-pools/index.md`](capabilities/runtimes-and-pools/index.md) | Lists the capabilities that decide which runtimes Lenny runs, on which pods, and how those pods are kept warm, claimed, reused, and retired. |
| `ISO` | capability | [`capabilities/runtimes-and-pools/isolation-profiles.md`](capabilities/runtimes-and-pools/isolation-profiles.md) | Defines the isolation profiles a pool can run under, what each profile isolates, and the acknowledgements a weaker profile requires. |
| `OWNPOOL` | capability | [`capabilities/runtimes-and-pools/owned-pools-and-reservations.md`](capabilities/runtimes-and-pools/owned-pools-and-reservations.md) | States the pools a tenant owns for its derived runtimes, the pod budget, and capacity reservations. |
| `PLACE` | capability | [`capabilities/runtimes-and-pools/placement.md`](capabilities/runtimes-and-pools/placement.md) | Assigns each pool a node class, an offering, a region, and a tenancy, renders them into each pod, and checks that pods land where their placement says. |
| `CLAIM` | capability | [`capabilities/runtimes-and-pools/pod-claims.md`](capabilities/runtimes-and-pools/pod-claims.md) | Acquires an idle pod for a session through a per-pod claim, holds a recycled pod for its tenant, and recovers orphaned claims. |
| `RCLASS` | capability | [`capabilities/runtimes-and-pools/resource-classes.md`](capabilities/runtimes-and-pools/resource-classes.md) | Defines resource classes in configuration, how a pool's class maps to container requests and limits, and the checks at load, write, and reconcile. |
| `RUNTIME` | capability | [`capabilities/runtimes-and-pools/runtime-registry.md`](capabilities/runtimes-and-pools/runtime-registry.md) | Registers runtimes, states the runtime fields and their rules, operator-derived runtimes and the merge of derived fields, and published metadata. |
| `REV` | capability | [`capabilities/runtimes-and-pools/runtime-revisions.md`](capabilities/runtimes-and-pools/runtime-revisions.md) | Records each change to a runtime's versioned fields as a revision, selects a revision per session, and moves traffic between revisions in a rollout. |
| `ROUTE` | capability | [`capabilities/runtimes-and-pools/runtime-routes.md`](capabilities/runtimes-and-pools/runtime-routes.md) | Chooses the runtime for a session from routing labels and runtime routes, which replace experiment variants. |
| `DERIVED` | capability | [`capabilities/runtimes-and-pools/tenant-derived-runtimes.md`](capabilities/runtimes-and-pools/tenant-derived-runtimes.md) | Lets a granted tenant derive its own runtime from a platform base, with an owned pool, a pod budget, and capacity reservations. |
| `POOL` | capability | [`capabilities/runtimes-and-pools/warm-pools.md`](capabilities/runtimes-and-pools/warm-pools.md) | Defines pools and pod sets, keeps warm inventory between its targets, and protects warm pods from voluntary disruption. |
| `ACTSTR` | capability | [`capabilities/security-and-compliance/agent-activity-streams.md`](capabilities/security-and-compliance/agent-activity-streams.md) | States the agent activity streams recorded for forensics and their retention. |
| `AUDIT` | capability | [`capabilities/security-and-compliance/audit-log.md`](capabilities/security-and-compliance/audit-log.md) | States what is audited, the integrity of the audit log, tenant validation at write, and audit queries. |
| `RESID` | capability | [`capabilities/security-and-compliance/data-residency.md`](capabilities/security-and-compliance/data-residency.md) | States data residency, its admission control, key and backup residency, and the multi-region consistency model. |
| `ERASE` | capability | [`capabilities/security-and-compliance/erasure.md`](capabilities/security-and-compliance/erasure.md) | States user-level erasure: the erasure job, its order of deletion, pseudonymization, receipts, processing restriction, and erasure of backups and external sinks. |
| `SECAREA` | index | [`capabilities/security-and-compliance/index.md`](capabilities/security-and-compliance/index.md) | Lists the capabilities that record evidence, oversee security, and meet data obligations. |
| `HOLD` | capability | [`capabilities/security-and-compliance/legal-hold.md`](capabilities/security-and-compliance/legal-hold.md) | States legal and platform-only holds, their effect on retention and checkpoint rotation, and the hold reconciler. |
| `PSEUDO` | capability | [`capabilities/security-and-compliance/pseudonymization-and-receipts.md`](capabilities/security-and-compliance/pseudonymization-and-receipts.md) | States the pseudonymization of retained records at erasure, the erasure salt, billing erasure policy, and erasure receipts. |
| `FORENSIC` | capability | [`capabilities/security-and-compliance/quarantine-and-evidence.md`](capabilities/security-and-compliance/quarantine-and-evidence.md) | States pod quarantine for evidence, the reconciler that ends it, the capture workflow, and security incident response. |
| `SECOV` | capability | [`capabilities/security-and-compliance/security-oversight.md`](capabilities/security-and-compliance/security-oversight.md) | States the tenant security record and posture, operator access transparency, and the rights of the operability service. |
| `SIEM` | capability | [`capabilities/security-and-compliance/siem-export.md`](capabilities/security-and-compliance/siem-export.md) | States how audit records reach a SIEM, the OCSF mapping, per-tenant destinations, dead-lettering, and the erasure boundary. |
| `CKPASS` | capability | [`capabilities/sessions/checkpoints/capture-passes.md`](capabilities/sessions/checkpoints/capture-passes.md) | States how a checkpoint is captured in passes, the change tracker, and the size limits and drain budgets that bound a capture. |
| `CKPT` | capability | [`capabilities/sessions/checkpoints/checkpoint-capture.md`](capabilities/sessions/checkpoints/checkpoint-capture.md) | States when a checkpoint is taken, how capture runs in passes, quiesce and abort, size limits and drain budgets, and the checkpoint capability model. |
| `CKSTORE` | capability | [`capabilities/sessions/checkpoints/checkpoint-storage.md`](capabilities/sessions/checkpoints/checkpoint-storage.md) | States the checkpoint catalog, reference closure, quota, retention, garbage collection, and the behavior when the object store fails. |
| `CKIDX` | index | [`capabilities/sessions/checkpoints/index.md`](capabilities/sessions/checkpoints/index.md) | Lists the checkpoint pages: capture, storage and retention, and restore. |
| `RESTORE` | capability | [`capabilities/sessions/checkpoints/restore.md`](capabilities/sessions/checkpoints/restore.md) | States how a checkpoint is restored into a slot for resume, derive, and download, and how a restore is sealed. |
| `SDERIVE` | capability | [`capabilities/sessions/derive.md`](capabilities/sessions/derive.md) | Starts a session from an earlier one at its start or at its latest checkpoint, with the copy and restore rules and the options a derive takes. |
| `SESAREA` | index | [`capabilities/sessions/index.md`](capabilities/sessions/index.md) | Lists the capabilities that govern a session from creation to its end, including suspension, resume, checkpoints, derive, and isolation between sessions on one pod. |
| `SCRUB` | capability | [`capabilities/sessions/pod-reuse-and-scrub.md`](capabilities/sessions/pod-reuse-and-scrub.md) | States when a pod may serve another session, the scrub that runs between sessions, the reuse prohibitions, and the acknowledgements weak reuse requires. |
| `RESUME` | capability | [`capabilities/sessions/retry-and-resume.md`](capabilities/sessions/retry-and-resume.md) | States the retry policy after a pod failure, session generations, the resume flow, and the states a session takes when retries run out. |
| `SCRUBPROC` | capability | [`capabilities/sessions/scrub-procedure.md`](capabilities/sessions/scrub-procedure.md) | States the scrub procedure step by step, the supervised scrub sequence, what the scrub reaches, the sweep, and the Kata variant. |
| `SLOT` | capability | [`capabilities/sessions/session-isolation.md`](capabilities/sessions/session-isolation.md) | States the isolation between sessions that share a pod: slots, slot identity, slot release, the per-slot resource and credential scope, and slot failure. |
| `SESLC` | index | [`capabilities/sessions/session-lifecycle/index.md`](capabilities/sessions/session-lifecycle/index.md) | Lists the pages of the session lifecycle: states, creation, suspension and dormancy, the end of a session, and timeouts. |
| `SCREATE` | capability | [`capabilities/sessions/session-lifecycle/session-creation.md`](capabilities/sessions/session-lifecycle/session-creation.md) | States the creation flow from request to a bound pod, its atomicity, the checks at the post-chain point, and the isolation level reported to the client. |
| `SEND` | capability | [`capabilities/sessions/session-lifecycle/session-end.md`](capabilities/sessions/session-lifecycle/session-end.md) | States how a session closes, the seal against further delivery, the terminal pipeline, and the window after a terminal state. |
| `SESS` | capability | [`capabilities/sessions/session-lifecycle/session-states.md`](capabilities/sessions/session-lifecycle/session-states.md) | Defines the session object, its identity, its states and transitions, its failure class, and the mapping of internal states to the states clients see. |
| `SUSP` | capability | [`capabilities/sessions/session-lifecycle/suspension-and-dormancy.md`](capabilities/sessions/session-lifecycle/suspension-and-dormancy.md) | States when a session suspends, what happens to its slot and pod, dormancy of idle sessions, and suspension of a delegation tree. |
| `TIMEOUT` | capability | [`capabilities/sessions/session-lifecycle/timeouts.md`](capabilities/sessions/session-lifecycle/timeouts.md) | Defines every session timer and watchdog, which states pause each one, and cancellation. |
| `SETUP` | capability | [`capabilities/sessions/setup-commands.md`](capabilities/sessions/setup-commands.md) | States the setup commands a runtime and a plan declare, their policy, ordering, limits, and failure. |
| `SLOTOPS` | capability | [`capabilities/sessions/slot-assignment-and-failure.md`](capabilities/sessions/slot-assignment-and-failure.md) | States how a session is assigned a slot atomically, slot liveness and release, slot failure and retry, and the rehydration of slots after recovery. |
| `AUTHN` | capability | [`capabilities/tenancy-and-access/authentication.md`](capabilities/tenancy-and-access/authentication.md) | States how callers authenticate: OIDC, API keys, development mode, the token claims Lenny reads, key rotation, user invalidation, and the playground's session. |
| `AUTHZ` | capability | [`capabilities/tenancy-and-access/authorization.md`](capabilities/tenancy-and-access/authorization.md) | Defines the roles, the permission matrix, custom roles, scopes, session ownership, security roles, and delegated approvers. |
| `BROWSER` | capability | [`capabilities/tenancy-and-access/browser-sessions.md`](capabilities/tenancy-and-access/browser-sessions.md) | States the cookie session a browser holds for the web playground and the token mint rules behind it. |
| `ENV` | capability | [`capabilities/tenancy-and-access/environments.md`](capabilities/tenancy-and-access/environments.md) | Defines environments, how runtimes are selected into them, the environment context of a session, and cross-environment rules. |
| `TENAREA` | index | [`capabilities/tenancy-and-access/index.md`](capabilities/tenancy-and-access/index.md) | Lists the capabilities that define tenants, environments, and principals and decide who may do what. |
| `PRINC` | capability | [`capabilities/tenancy-and-access/principals.md`](capabilities/tenancy-and-access/principals.md) | Defines users, service accounts, and subjects, and how each is identified within a tenant. |
| `SECROLE` | capability | [`capabilities/tenancy-and-access/security-roles-and-approvers.md`](capabilities/tenancy-and-access/security-roles-and-approvers.md) | Defines the security roles and the delegated approvers who confirm security-weakening changes. |
| `TSET` | capability | [`capabilities/tenancy-and-access/tenant-settings.md`](capabilities/tenancy-and-access/tenant-settings.md) | Defines the tenant settings model, templates, the tenant key, compliance profiles, and how a setting changes on a live tenant. |
| `TENANT` | capability | [`capabilities/tenancy-and-access/tenants.md`](capabilities/tenancy-and-access/tenants.md) | Defines the tenant object and its lifecycle from creation to deletion. |
| `TOKX` | capability | [`capabilities/tenancy-and-access/token-exchange.md`](capabilities/tenancy-and-access/token-exchange.md) | States the token exchange Lenny runs, scope narrowing, impersonation, write-before-issue, rotation, and revocation of issued tokens. |
| `ARTIFACT` | capability | [`capabilities/workspaces-and-files/artifacts.md`](capabilities/workspaces-and-files/artifacts.md) | States what Lenny keeps as session artifacts, transcripts, blob resolution, deduplication, and artifact retention. |
| `CLONE` | capability | [`capabilities/workspaces-and-files/default-clones.md`](capabilities/workspaces-and-files/default-clones.md) | States the default git clones a tenant-derived runtime declares and how they reach a session's workspace. |
| `FILEX` | capability | [`capabilities/workspaces-and-files/file-export.md`](capabilities/workspaces-and-files/file-export.md) | States the file export model between sessions and to clients, export limits, and scanning of exported files. |
| `FILEMSG` | capability | [`capabilities/workspaces-and-files/files-in-messages.md`](capabilities/workspaces-and-files/files-in-messages.md) | States how files travel with messages and output, including files a child returns to its parent. |
| `WSAREA` | index | [`capabilities/workspaces-and-files/index.md`](capabilities/workspaces-and-files/index.md) | Lists the capabilities that build a session's workspace and move files in and out of it. |
| `MATER` | capability | [`capabilities/workspaces-and-files/materialization.md`](capabilities/workspaces-and-files/materialization.md) | States how the adapter and the helper build a workspace from a plan inside the slot, the ordering and collision rules, atomicity, and the validation of results. |
| `REPO` | capability | [`capabilities/workspaces-and-files/repository-stores.md`](capabilities/workspaces-and-files/repository-stores.md) | States the shared repository stores on a pod, the warm list, repository folders, and the warm-up checkout. |
| `UPLOAD` | capability | [`capabilities/workspaces-and-files/uploads.md`](capabilities/workspaces-and-files/uploads.md) | States upload authorization, the upload token, size caps, archive handling, and mid-session uploads. |
| `WSPLAN` | capability | [`capabilities/workspaces-and-files/workspace-plan.md`](capabilities/workspaces-and-files/workspace-plan.md) | States what a workspace plan means: its sources, when it binds, its defaults, and the rules Lenny applies to each member. |
| `CATALERT` | catalog | [`catalogs/alerts.md`](catalogs/alerts.md) | Lists every alert with its severity, the requirements whose breach it detects, and its owner block. |
| `CATOPS` | catalog | [`catalogs/api-operations.md`](catalogs/api-operations.md) | Lists every interface operation with its identifier, method, path, and owner block. |
| `CATCHAN` | catalog | [`catalogs/channels.md`](catalogs/channels.md) | Lists every register with its store, writers, readers, and owner block. |
| `CATCONTR` | catalog | [`catalogs/contributions.md`](catalogs/contributions.md) | Lists every RBAC grant and network rule with the running unit that carries it, the page that states it, and the requirement behind it. |
| `CATCRD` | catalog | [`catalogs/custom-resources.md`](catalogs/custom-resources.md) | Lists every custom resource kind with its group, version, scope, and owner block. |
| `CATERR` | catalog | [`catalogs/errors.md`](catalogs/errors.md) | Lists every error code, admission refusal, and condition reason with its status and owner block. |
| `CATEVENT` | catalog | [`catalogs/events.md`](catalogs/events.md) | Lists every event type with its emitting components, its payload, and its owner block. |
| `CATGLOSS` | catalog | [`catalogs/glossary.md`](catalogs/glossary.md) | Lists every owned term with a one-sentence gloss and a link to its owner. |
| `CATIDX` | index | [`catalogs/index.md`](catalogs/index.md) | Lists the catalogs generated from the definition blocks, the terms, and the requirements of the specification's pages. |
| `CATLABEL` | catalog | [`catalogs/labels.md`](catalogs/labels.md) | Lists every label, annotation, finalizer, and readiness gate with its carriers, mutability, and owner block. |
| `CATLIMIT` | catalog | [`catalogs/limits.md`](catalogs/limits.md) | Lists every limit with its value, unit, and owner block. |
| `CATMETRIC` | catalog | [`catalogs/metrics.md`](catalogs/metrics.md) | Lists every metric with its type, labels, emitting components, and owner block. |
| `CATREQ` | catalog | [`catalogs/requirements.md`](catalogs/requirements.md) | Lists every requirement identifier with its title and page. |
| `CATSET` | catalog | [`catalogs/settings.md`](catalogs/settings.md) | Lists every setting with its type, default, range, scopes, the components that read it, its tier presets, and its owner block. |
| `CATSTATE` | catalog | [`catalogs/state-machines.md`](catalogs/state-machines.md) | Lists every state machine with its object, its states, and its owner block. |
| `WEBHOOK` | component | [`components/admission-webhooks.md`](components/admission-webhooks.md) | The admission webhook server and admission policies: what is rendered, availability, failure modes, and metrics. |
| `AGDNS` | component | [`components/agent-dns.md`](components/agent-dns.md) | The dedicated CoreDNS instances for agent pods, including the resolver for proxied pods. |
| `ADAPTER` | component | [`components/agent-pod/adapter.md`](components/agent-pod/adapter.md) | The adapter container: startup sequence, the slot registry, gateway and runtime connections, health checks, process settings, and metrics. |
| `POD` | index | [`components/agent-pod/index.md`](components/agent-pod/index.md) | The agent pod as a unit: its containers in start order, volumes and size limits, project quotas, security context, and pod-level metrics. |
| `PODINIT` | component | [`components/agent-pod/init-and-support-containers.md`](components/agent-pod/init-and-support-containers.md) | The init containers that stage platform binaries and fill repository stores, and the keepalive container. |
| `NETAGENT` | component | [`components/agent-pod/netagent.md`](components/agent-pod/netagent.md) | The netagent sidecar: the local egress endpoint, per-slot attribution, git on covered hosts, and per-slot credential files. |
| `SUPV` | component | [`components/agent-pod/runtime-supervisor.md`](components/agent-pod/runtime-supervisor.md) | The slot containers and the runtime supervisor that runs as PID 1 in each: process launch and end, the sweep, the file-descriptor broker, and the materialization helper. |
| `PODVOL` | component | [`components/agent-pod/volumes-and-quotas.md`](components/agent-pod/volumes-and-quotas.md) | The agent pod's volumes, their size limits and eviction behavior, and the project quotas on slot volumes. |
| `EGAUTHZ` | component | [`components/egress-decision-service.md`](components/egress-decision-service.md) | The egress decision service (lenny-egress-authz): pod identification, policy reads, deployment, and metrics. |
| `EGPROXY` | component | [`components/egress-proxy.md`](components/egress-proxy.md) | The central egress proxy (lenny-egress): deployment, transport security, and metrics. |
| `CERTM` | component | [`components/external/cert-manager.md`](components/external/cert-manager.md) | cert-manager: the certificates Lenny requests, CA rotation, and failure modes. |
| `EXTIDX` | index | [`components/external/index.md`](components/external/index.md) | Lists the external dependencies and the requirements Lenny places on each. |
| `KMS` | component | [`components/external/kms.md`](components/external/kms.md) | The KMS: keys Lenny uses, who may call them, rotation, and failure behavior. |
| `KUBE` | component | [`components/external/kubernetes.md`](components/external/kubernetes.md) | The Kubernetes API server, etcd, and the CNI: version floor, API rate limits, etcd tuning, and network policy enforcement. |
| `OBJ` | component | [`components/external/object-store.md`](components/external/object-store.md) | The object store: backends, HA, versioning, replication, lifecycle rules, encryption, and outage handling. |
| `PG` | component | [`components/external/postgres.md`](components/external/postgres.md) | Postgres and its pooler: topology, HA, connection pooling, write classification, failover behavior, encryption, and backups. |
| `PROM` | component | [`components/external/prometheus.md`](components/external/prometheus.md) | Prometheus: when it is required, scrape targets, rule rendering, evaluation cost, and the bring-your-own model. |
| `REDIS` | component | [`components/external/redis.md`](components/external/redis.md) | Redis: topology, logical separation, failure modes per use, scalability ceilings, and TLS. |
| `GWAPI` | component | [`components/gateway/api-front.md`](components/gateway/api-front.md) | The gateway's request pipeline as a part of every replica: middleware order, authentication, rate limiting, handler registration, and request scope. |
| `GWBARRIER` | component | [`components/gateway/checkpoint-barrier.md`](components/gateway/checkpoint-barrier.md) | The checkpoint barrier the gateway fans out to pods during a rolling update. |
| `GWDRAIN` | component | [`components/gateway/drain.md`](components/gateway/drain.md) | The preStop drain of a gateway replica: its stages, deadlines, and terminal pipelines at process exit. |
| `GW` | index | [`components/gateway/index.md`](components/gateway/index.md) | The gateway as a unit: the seam model and the isolation every seam has, the session core, the parts that apply to the whole replica, replicas, disruption budget, probes, ServiceAccount, RBAC, NetworkPolicy, process settings, and metrics. |
| `GWLLM` | component | [`components/gateway/metering-proxy.md`](components/gateway/metering-proxy.md) | The gateway's metering proxy listener for LLM upstream calls: routes, limits, process settings, and metrics. |
| `GWOPS` | component | [`components/gateway/ops-endpoints.md`](components/gateway/ops-endpoints.md) | The gateway-side operability parts: the health service, capacity recommendations, the event buffer, and the in-process alert tracker. |
| `GWTOOLS` | component | [`components/gateway/platform-tools.md`](components/gateway/platform-tools.md) | The platform tools seam (§4.1 MCP Fabric): platform tool calls from pods, delegation orchestration, and the elicitation chain. |
| `GWCLAIM` | component | [`components/gateway/pod-claimer.md`](components/gateway/pod-claimer.md) | The gateway's pod claimer: candidate selection, claim creation, and the Postgres fallback path. |
| `GWSCALE` | component | [`components/gateway/scaling-and-capacity.md`](components/gateway/scaling-and-capacity.md) | Gateway autoscaling: HPA and KEDA metric roles, the per-replica session budget, the minimum-replica formula, and subsystem extraction thresholds. |
| `GWCOORD` | component | [`components/gateway/session-coordination.md`](components/gateway/session-coordination.md) | How gateway replicas coordinate a session: the coordination lease, handoff, hold state, stale replicas, and behavior when both stores fail. |
| `GWSTREAM` | component | [`components/gateway/stream-proxy.md`](components/gateway/stream-proxy.md) | The stream proxy seam: session attachment, event relay, and reconnection for SSE readers. |
| `GWUPLOAD` | component | [`components/gateway/upload-handler.md`](components/gateway/upload-handler.md) | The upload handler seam: upload proxying, validation, and staging. |
| `GITMIR` | component | [`components/git-mirror.md`](components/git-mirror.md) | The git mirror (lenny-git-mirror): storage, fetch scheduling, holds, deployment, and metrics. |
| `COMPS` | index | [`components/index.md`](components/index.md) | Lists the components with their summaries, grouped as Lenny processes, agent pod containers, and external dependencies. |
| `BKJOB` | component | [`components/lenny-ops/backup-jobs.md`](components/lenny-ops/backup-jobs.md) | The lenny-backup Jobs that lenny-ops creates: pod specification, ServiceAccount, NetworkPolicy, and bucket policy. |
| `LOPSAGG` | component | [`components/lenny-ops/gateway-fan-out.md`](components/lenny-ops/gateway-fan-out.md) | How lenny-ops calls every gateway replica, sources metrics with and without Prometheus, and aggregates health and recommendations. |
| `LOPSSVC` | component | [`components/lenny-ops/hosted-services.md`](components/lenny-ops/hosted-services.md) | The internal parts of the services lenny-ops hosts: storage tiers, implementation notes, and metrics for events, diagnostics, locks, escalations, audit queries, drift, backups, and upgrades. |
| `LOPS` | index | [`components/lenny-ops/index.md`](components/lenny-ops/index.md) | The lenny-ops Deployment: purpose, Helm values, Kubernetes resources, RBAC, NetworkPolicy, leader election, startup, self-monitoring, and TLS. |
| `LOPSKRES` | component | [`components/lenny-ops/kubernetes-resources.md`](components/lenny-ops/kubernetes-resources.md) | The Kubernetes resources the chart renders for lenny-ops: Helm values, Services, ServiceAccount and RBAC, NetworkPolicies, and the disruption budget. |
| `OUTAGT` | component | [`components/outbound-agent.md`](components/outbound-agent.md) | The outbound agent a tenant runs on its own network: packaging, keys, and attestations. |
| `OUTGW` | component | [`components/outbound-gateway.md`](components/outbound-gateway.md) | The outbound gateway Deployment that tenant agents dial, its replica groups, and metrics. |
| `PSC` | component | [`components/pool-scaling-controller.md`](components/pool-scaling-controller.md) | The PoolScalingController: reconciliation from Postgres, scaling computation, leader election, and metrics. |
| `PREFLT` | component | [`components/preflight-job.md`](components/preflight-job.md) | The lenny-preflight Job and the checks it runs, each linked to the requirement it verifies. |
| `TOKSVC` | component | [`components/token-service.md`](components/token-service.md) | The Token Service Deployment: its KMS access, secret reads, signing, upgrade and key rotation, and metrics. |
| `WPC` | component | [`components/warm-pool-controller.md`](components/warm-pool-controller.md) | The WarmPoolController: pod creation and status, leader election, failover, API and etcd load, and metrics. |
| `PLAY` | component | [`components/web-playground.md`](components/web-playground.md) | The web playground: placement and gating, cookie session handling, UI surface, asset serving and CSP, cleanup, and metrics. |
| `APICONV` | cross-cutting | [`cross-cutting/api-conventions.md`](cross-cutting/api-conventions.md) | Conventions shared by the client, admin, and ops APIs: idempotency, ETags, pagination, dry-run, correlation headers, and rate-limit headers. |
| `AVAIL` | cross-cutting | [`cross-cutting/availability-and-degradation.md`](cross-cutting/availability-and-degradation.md) | Availability principles, fail-open and fail-closed rules, dependency outage behavior, and total-outage recovery. |
| `DATACLASS` | cross-cutting | [`cross-cutting/data-classification-and-retention.md`](cross-cutting/data-classification-and-retention.md) | Data classes, the controls each class requires, encryption at rest, and retention defaults. |
| `ERR` | cross-cutting | [`cross-cutting/error-model.md`](cross-cutting/error-model.md) | The error envelope, the error codes shared across interfaces, validation errors, the degradation envelope, and the refusal of removed fields. |
| `XC` | index | [`cross-cutting/index.md`](cross-cutting/index.md) | Lists the properties every capability and component upholds, with the pages that carry each. |
| `PKI` | cross-cutting | [`cross-cutting/mtls-and-pki.md`](cross-cutting/mtls-and-pki.md) | The certificate authority, certificate lifecycle, and the peer validation every mTLS link performs. |
| `NAMING` | cross-cutting | [`cross-cutting/naming.md`](cross-cutting/naming.md) | The channel naming law, resource name rules, and the naming table of retired spellings. |
| `OBSCONV` | cross-cutting | [`cross-cutting/observability-conventions.md`](cross-cutting/observability-conventions.md) | Metric and attribute naming, labels, log format, trace propagation, and the single source of alert rules. |
| `SECMODEL` | cross-cutting | [`cross-cutting/security-model.md`](cross-cutting/security-model.md) | Trust boundaries, the threat model, the adapter-to-runtime boundary, and image supply chain controls. |
| `TENISO` | cross-cutting | [`cross-cutting/tenant-isolation.md`](cross-cutting/tenant-isolation.md) | The tenant isolation property: tenant identifiers on every record, row-level security, key and object prefixes, and the query rules that keep tenants apart. |
| `COMPAT` | cross-cutting | [`cross-cutting/upgrade-and-compatibility.md`](cross-cutting/upgrade-and-compatibility.md) | The compatibility policy: API versioning, expand-contract migrations, CRD versioning, wire-contract versions, and version skew. |
| `ADMAN` | interface | [`interfaces/adapter-manifest.md`](interfaces/adapter-manifest.md) | The adapter manifest a runtime reads at startup, with every field. |
| `ADMBILL` | interface | [`interfaces/admin-api/billing.md`](interfaces/admin-api/billing.md) | Rate card, budget, billing correction, and usage operations. |
| `ADMCOMP` | interface | [`interfaces/admin-api/compliance.md`](interfaces/admin-api/compliance.md) | Legal hold, erasure job, residency, and tenant deletion operations. |
| `ADMCRED` | interface | [`interfaces/admin-api/credentials-and-upstreams.md`](interfaces/admin-api/credentials-and-upstreams.md) | Credential pool, LLM upstream, and revocation operations. |
| `ADMIAM` | interface | [`interfaces/admin-api/identity-and-access.md`](interfaces/admin-api/identity-and-access.md) | User, service account, role, and token operations. |
| `ADMIN` | index | [`interfaces/admin-api/index.md`](interfaces/admin-api/index.md) | The admin REST API under /v1/admin: the conventions its operation pages rely on and the list of operation pages. |
| `ADMPOL` | interface | [`interfaces/admin-api/policies.md`](interfaces/admin-api/policies.md) | Delegation policy, interceptor, quota, and circuit breaker operations. |
| `APOOLS` | interface | [`interfaces/admin-api/pools.md`](interfaces/admin-api/pools.md) | Pool operations. |
| `ADMRT` | interface | [`interfaces/admin-api/runtimes.md`](interfaces/admin-api/runtimes.md) | Runtime, revision, route, and resource class operations. |
| `ADMTEN` | interface | [`interfaces/admin-api/tenants-and-environments.md`](interfaces/admin-api/tenants-and-environments.md) | Tenant, tenant setting, environment, and outbound connector operations. |
| `CHCTRL` | interface | [`interfaces/channels/control-plane.md`](interfaces/channels/control-plane.md) | The control-plane channel for drain-readiness admission. |
| `CHGWPOD` | interface | [`interfaces/channels/gateway-to-pod.md`](interfaces/channels/gateway-to-pod.md) | The gateway-to-pod link and its channels, with the adapter RPC operations and request scope rules. |
| `CHSTORE` | interface | [`interfaces/channels/gateway-to-store.md`](interfaces/channels/gateway-to-store.md) | The gateway-to-store channels and the registers that mediate replicas through Redis, Postgres, and the Kubernetes API. |
| `CHAN` | index | [`interfaces/channels/index.md`](interfaces/channels/index.md) | The gateway-to-pod channels and registers: classes, axes, the contract card format, the exclusivity model, and the wire-contract artifacts. |
| `CHREPLICA` | interface | [`interfaces/channels/inter-replica.md`](interfaces/channels/inter-replica.md) | The channel that forwards off-holder session requests between gateway replicas. |
| `CHINTRA` | interface | [`interfaces/channels/intra-pod.md`](interfaces/channels/intra-pod.md) | The intra-pod channels between Lenny's own processes, including the supervision channel; the runtime-facing channels point to the runtime protocol. |
| `CHEGRESS` | interface | [`interfaces/channels/pod-egress.md`](interfaces/channels/pod-egress.md) | The pod-egress channels to the metering proxy and the object store. |
| `CHPODGW` | interface | [`interfaces/channels/pod-to-gateway.md`](interfaces/channels/pod-to-gateway.md) | The pod-to-gateway link and the adapter events channel. |
| `CLIADM` | interface | [`interfaces/cli/admin-commands.md`](interfaces/cli/admin-commands.md) | Runtime, pool, credential, quota, breaker, user, tenant, erasure, policy, and session commands. |
| `CLI` | index | [`interfaces/cli/index.md`](interfaces/cli/index.md) | lenny-ctl: global flags, server discovery and routing, output formats, and the command groups. |
| `CLIINST` | interface | [`interfaces/cli/install-and-local.md`](interfaces/cli/install-and-local.md) | Install, bootstrap, preflight, the installation wizard, the local stack, and image commands. |
| `CLIOPS` | interface | [`interfaces/cli/ops-commands.md`](interfaces/cli/ops-commands.md) | Operability commands: diagnose, events, audit, drift, locks, escalations, backups, upgrades, migrations, and MCP management. |
| `CLISCAF` | interface | [`interfaces/cli/runtime-scaffolding.md`](interfaces/cli/runtime-scaffolding.md) | The runtime scaffolding commands. |
| `CAPICRED` | interface | [`interfaces/client-api/credentials-and-links.md`](interfaces/client-api/credentials-and-links.md) | User credential and account link operations. |
| `CAPIDISC` | interface | [`interfaces/client-api/discovery-and-usage.md`](interfaces/client-api/discovery-and-usage.md) | Runtime discovery, published metadata, and usage reporting. |
| `CAPIEVT` | interface | [`interfaces/client-api/event-stream.md`](interfaces/client-api/event-stream.md) | The session event stream endpoint, the event kind registry, resume parameters, and keepalive. |
| `CAPIFILE` | interface | [`interfaces/client-api/files-and-artifacts.md`](interfaces/client-api/files-and-artifacts.md) | Upload, artifact, blob, and export operations. |
| `CAPI` | index | [`interfaces/client-api/index.md`](interfaces/client-api/index.md) | The client REST API: authentication, versioning, and the resource groups, with links to the shared API conventions and error model. |
| `CAPIMSG` | interface | [`interfaces/client-api/messages.md`](interfaces/client-api/messages.md) | Message operations: send, retract, list, respond to input requests, and client tool results. |
| `CAPISES` | interface | [`interfaces/client-api/sessions.md`](interfaces/client-api/sessions.md) | Session operations: create, start, get, list, interrupt, terminate, delete, derive, and the states and errors each returns. |
| `DCFGANS` | interface | [`interfaces/deployment-configuration/answer-files.md`](interfaces/deployment-configuration/answer-files.md) | Answer files: composition dimensions and the catalog of answer files. |
| `DCFGSEED` | interface | [`interfaces/deployment-configuration/bootstrap-seed.md`](interfaces/deployment-configuration/bootstrap-seed.md) | The bootstrap seed format that installs runtimes, pools, and tenants before the admin API is reachable. |
| `DCFG` | index | [`interfaces/deployment-configuration/index.md`](interfaces/deployment-configuration/index.md) | The deployment configuration: the Helm values structure, the values schema, chart-rendered resources, and feature gates. |
| `DCFGTIER` | interface | [`interfaces/deployment-configuration/tier-presets.md`](interfaces/deployment-configuration/tier-presets.md) | The tier preset files and the values each tier sets. |
| `EVTAUD` | interface | [`interfaces/events/audit-records.md`](interfaces/events/audit-records.md) | The audit record schema and the audit event types. |
| `EVTBILL` | interface | [`interfaces/events/billing-events.md`](interfaces/events/billing-events.md) | The billing event types, their schemas, and the null-field contract. |
| `EVT` | index | [`interfaces/events/index.md`](interfaces/events/index.md) | The CloudEvents envelope and the transports that carry Lenny's events. |
| `EVTOPS` | interface | [`interfaces/events/operational-events.md`](interfaces/events/operational-events.md) | The operational event types and their data schemas. |
| `EVTHOOK` | interface | [`interfaces/events/session-webhooks.md`](interfaces/events/session-webhooks.md) | The session callback webhooks and their signing. |
| `EXT` | interface | [`interfaces/extension-points.md`](interfaces/extension-points.md) | The Go extension points a deployer can replace: storage backends, the pool manager, and the cluster registry. |
| `GWSEAM` | interface | [`interfaces/gateway-seams.md`](interfaces/gateway-seams.md) | The boundary between each gateway seam and the session core. |
| `IFS` | index | [`interfaces/index.md`](interfaces/index.md) | Lists the interfaces, the parties on each side, and the page that owns each. |
| `INTCPTIF` | interface | [`interfaces/interceptor-protocol.md`](interfaces/interceptor-protocol.md) | The protocol between the gateway and a request interceptor: phases, payloads, verdicts, and authentication. |
| `KADMIT` | interface | [`interfaces/kubernetes/admission.md`](interfaces/kubernetes/admission.md) | The writes Lenny's admission webhooks and policies refuse, with the refusal each returns. |
| `KAPOD` | interface | [`interfaces/kubernetes/agentpod.md`](interfaces/kubernetes/agentpod.md) | The AgentPod kind. |
| `KAPI` | index | [`interfaces/kubernetes/index.md`](interfaces/kubernetes/index.md) | Lenny's Kubernetes API conventions: the API group and version, the shipped CRDs and their schema version, field ownership, validation, and how an admission refusal reaches a client. |
| `KLABEL` | interface | [`interfaces/kubernetes/labels-and-annotations.md`](interfaces/kubernetes/labels-and-annotations.md) | Every label and annotation key, with its writer and readers. |
| `KPCLAIM` | interface | [`interfaces/kubernetes/podclaim.md`](interfaces/kubernetes/podclaim.md) | The PodClaim kind. |
| `KPSET` | interface | [`interfaces/kubernetes/podset.md`](interfaces/kubernetes/podset.md) | The PodSet kind. |
| `KRUNTIME` | interface | [`interfaces/kubernetes/runtime.md`](interfaces/kubernetes/runtime.md) | The Runtime kind. |
| `OPSAUD` | interface | [`interfaces/ops-api/audit-and-drift.md`](interfaces/ops-api/audit-and-drift.md) | Audit query and configuration drift operations. |
| `OPSLIFE` | interface | [`interfaces/ops-api/backup-and-upgrades.md`](interfaces/ops-api/backup-and-upgrades.md) | Backup, restore, version, upgrade, and configuration apply operations. |
| `OPSME` | interface | [`interfaces/ops-api/caller-identity.md`](interfaces/ops-api/caller-identity.md) | The caller identity and capability discovery operations. |
| `OPSHOOK` | interface | [`interfaces/ops-api/events-and-webhooks.md`](interfaces/ops-api/events-and-webhooks.md) | Operational event stream, polling, buffer, and webhook subscription operations. |
| `OPSHEALTH` | interface | [`interfaces/ops-api/health-and-diagnostics.md`](interfaces/ops-api/health-and-diagnostics.md) | Health, recommendation, diagnostic, and runbook operations. |
| `OPSAPI` | index | [`interfaces/ops-api/index.md`](interfaces/ops-api/index.md) | The ops API served by the gateway and lenny-ops: terminology, envelopes, pagination, rate limits, and the operation groups. |
| `OPSMCP` | interface | [`interfaces/ops-api/mcp-management.md`](interfaces/ops-api/mcp-management.md) | The management MCP server of lenny-ops: tools, schemas, capability negotiation, and headers. |
| `OPSREM` | interface | [`interfaces/ops-api/remediation-and-operations.md`](interfaces/ops-api/remediation-and-operations.md) | Remediation lock, escalation, and operations inventory operations. |
| `RTPCONF` | interface | [`interfaces/runtime-protocol/conformance.md`](interfaces/runtime-protocol/conformance.md) | The conformance suite by capability, how it is packaged, and how failures are categorized. |
| `RTPFS` | interface | [`interfaces/runtime-protocol/filesystem-and-environment.md`](interfaces/runtime-protocol/filesystem-and-environment.md) | The paths, environment variables, and injected configuration a runtime sees in its slot. |
| `RTPHAND` | interface | [`interfaces/runtime-protocol/handshake-and-capabilities.md`](interfaces/runtime-protocol/handshake-and-capabilities.md) | The handshake frames, the capabilities a runtime declares, and their dependencies. |
| `RTP` | index | [`interfaces/runtime-protocol/index.md`](interfaces/runtime-protocol/index.md) | The runtime protocol between the adapter and a runtime: transports, capabilities in place of integration levels, versioning, and the frame families. |
| `RTPPART` | interface | [`interfaces/runtime-protocol/message-parts.md`](interfaces/runtime-protocol/message-parts.md) | The MessagePart format, the message envelope fields, binary input and output, and the mapping from MCP content blocks. |
| `RTPMSG` | interface | [`interfaces/runtime-protocol/messages.md`](interfaces/runtime-protocol/messages.md) | The message socket frames, the MessagePart format, tool and frame names, exit codes, and leniency rules. |
| `RTPTOOL` | interface | [`interfaces/runtime-protocol/platform-tools.md`](interfaces/runtime-protocol/platform-tools.md) | The platform tools a runtime calls, through frames or the reference MCP bridge, with schemas including the delegation tools and the task result. |
| `RTPOPS` | interface | [`interfaces/runtime-protocol/runtime-ops.md`](interfaces/runtime-protocol/runtime-ops.md) | The runtime-ops channel frames: quiesce, interrupt acknowledgement, and the remaining rotation frames. |
| `RTPSDK` | interface | [`interfaces/runtime-protocol/runtime-sdks.md`](interfaces/runtime-protocol/runtime-sdks.md) | The runtime author SDKs: packages, what each adds to the protocol, and what each leaves to the runtime. |
| `STOR` | index | [`interfaces/stored-schemas/index.md`](interfaces/stored-schemas/index.md) | The stored schemas that must stay compatible across versions, and the compatibility rules for each store. |
| `STOROBJ` | interface | [`interfaces/stored-schemas/object-store-layout.md`](interfaces/stored-schemas/object-store-layout.md) | The object key layout and the checkpoint pack format. |
| `STORPG` | interface | [`interfaces/stored-schemas/postgres.md`](interfaces/stored-schemas/postgres.md) | The Postgres tables other versions read: sessions, tasks, pod state, billing, audit, and operations tables. |
| `STORREDIS` | interface | [`interfaces/stored-schemas/redis-keys.md`](interfaces/stored-schemas/redis-keys.md) | The Redis key patterns and their tenant prefixing. |
| `WSPLANIF` | interface | [`interfaces/workspace-plan.md`](interfaces/workspace-plan.md) | The WorkspacePlan schema with every field, and its versioning. |
| `ARCH` | overview | [`overview/architecture.md`](overview/architecture.md) | Maps the components, the namespaces they run in, the stores they use, and the links between them, with one line per component and a link to its page. |
| `CAPMAP` | overview | [`overview/capability-map.md`](overview/capability-map.md) | Maps each capability area and capability to the components that carry it out and the interfaces that expose it. |
| `OVW` | index | [`overview/index.md`](overview/index.md) | Lists the overview pages: purpose and context, principles and non-goals, the architecture, the capability and interface maps, and the walkthroughs. |
| `IFMAP` | overview | [`overview/interface-map.md`](overview/interface-map.md) | Maps each interface to the parties on each side, its transport, and its owning pages, and states that the client contract is REST alone. |
| `GOALS` | overview | [`overview/principles-and-non-goals.md`](overview/principles-and-non-goals.md) | States the design principles, the goals, the non-goals, and the decisions Lenny leaves to deployers. |
| `CTX` | overview | [`overview/purpose-and-context.md`](overview/purpose-and-context.md) | States what Lenny is for, the actors that use it (clients, tenant admins, platform operators, runtime authors, and DevOps agents), and the external systems it depends on. |
| `WKCKPT` | overview | [`overview/walkthroughs/checkpoint-capture.md`](overview/walkthroughs/checkpoint-capture.md) | Traces a checkpoint from its trigger through quiesce, capture passes, and upload. |
| `WKHANDOFF` | overview | [`overview/walkthroughs/coordinator-handoff.md`](overview/walkthroughs/coordinator-handoff.md) | Traces a coordinator handoff and a crash takeover between gateway replicas. |
| `WKDRAIN` | overview | [`overview/walkthroughs/gateway-drain.md`](overview/walkthroughs/gateway-drain.md) | Traces a gateway replica drain with its checkpoint barrier and session handoff. |
| `WALK` | index | [`overview/walkthroughs/index.md`](overview/walkthroughs/index.md) | Lists the non-normative walkthroughs that trace one scenario across components and channels, and states the trace notation. |
| `WKMSG` | overview | [`overview/walkthroughs/interactive-message.md`](overview/walkthroughs/interactive-message.md) | Traces one client message through delivery, the runtime's turn, and the streamed output. |
| `WKSTOP` | overview | [`overview/walkthroughs/interrupt-terminate-delete.md`](overview/walkthroughs/interrupt-terminate-delete.md) | Traces an interrupt, a terminate, and a delete through the gateway, the adapter, and the pod. |
| `WKOPS` | overview | [`overview/walkthroughs/ops-remediation.md`](overview/walkthroughs/ops-remediation.md) | Traces a DevOps agent observing an operational event, diagnosing it, taking a remediation lock, and verifying recovery. |
| `WKEVICT` | overview | [`overview/walkthroughs/pod-eviction.md`](overview/walkthroughs/pod-eviction.md) | Traces the eviction of an agent pod that holds an active session. |
| `WKRESUME` | overview | [`overview/walkthroughs/restore-and-resume.md`](overview/walkthroughs/restore-and-resume.md) | Traces a resume after pod loss, from the failure through a new claim and restore. |
| `WKSTART` | overview | [`overview/walkthroughs/session-start.md`](overview/walkthroughs/session-start.md) | Traces a session from the create request through pod claim, materialization, and the first message. |
| `WKCOTEN` | overview | [`overview/walkthroughs/shared-pod-co-tenancy.md`](overview/walkthroughs/shared-pod-co-tenancy.md) | Traces two sessions on one pod with several slots. |

<!-- /generated:code-registry -->
