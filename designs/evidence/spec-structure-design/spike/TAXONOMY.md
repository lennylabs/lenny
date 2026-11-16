# Spec structure spike: information types and placement rules

Status: version 1, 2026-11-16. Version 0 was written before the spike to give it rules to test. Version 1 applies what the vertical slice ([FINDINGS-slice.md](FINDINGS-slice.md)) and the whole-tree map ([FINDINGS-map.md](FINDINGS-map.md)) showed. "Changes from version 0" at the end lists each change with the finding behind it. The pages under `proposed/` were written against version 0, so they differ from version 1 where the list says so.

The proposed specification tree lives under `proposed/` in this folder. It sits outside `spec/` because the spike is a review artifact, and nothing here changes the current specification.

## Goals

- Every fact is stated once, in the page whose type owns that kind of fact. Every other mention is a link, optionally with a one-sentence gloss.
- A reader, human or agent, reaches any fact by descending from the root map through an index page and a page summary, without loading unrelated pages.
- Identifiers do not move when the tree is reorganized.

## Page types

| Type | Folder | Owns | Does not own |
| :-- | :-- | :-- | :-- |
| Overview | `overview/` | The system's purpose and context (actors and external systems), principles, non-goals, and the maps of areas, capabilities, components, and interfaces. | Any rule, value, or behavior. |
| Walkthrough | `overview/walkthroughs/` | A non-normative trace of one scenario across capabilities and components, each step linking the requirement and the operation it exercises. | Any rule. A gate refuses normative keywords on walkthrough pages. |
| Capability | `capabilities/<area>/<capability>.md`, or a folder when it splits | What the system does end to end: the domain objects the capability governs (definition, identity, relationships, states and state machine, invariants), the behavioral rules as numbered requirements, the flows across components, the behavioral settings, the errors a single rule of the capability produces, the metrics that measure the capability and the alerts that detect a breach of its requirements, failure behavior, security properties and threats, limits (what the capability does not guarantee), and the current rationale. | How a component is deployed or run as a process. The syntax of an interface. History and alternatives considered. |
| Component | `components/<component>.md`, or a folder for a unit with internal parts | A running unit: a process, a controller, a container, a Job, or an external dependency Lenny relies on. Its responsibilities (each a link to the requirement it carries out), the interfaces it serves and consumes, its internal parts, the requirements of mechanisms only this implementation has (numbered with the component's code), its deployment properties (replicas, leader election, scaling, probes, resources, ServiceAccount, RBAC, NetworkPolicy), its process settings, the list of every setting its process reads (by link where the setting is behavioral), its failure modes as a unit and how it recovers, the metrics and alerts about its health as a unit, and the registers only it reads and writes. | A rule that any implementation of the same responsibilities must meet. That rule stays on the capability. |
| Interface | `interfaces/<interface>/...` | A contract one party can be built against without reading the other party: the client, admin, and ops APIs by resource group, the runtime protocol and its SDKs, the adapter manifest, the gateway-to-pod channels and the registers two components share, Lenny's Kubernetes resources (custom resources, labels and annotations, admission outcomes a Kubernetes client sees), the request interceptor protocol, the extension points a deployer can replace, the CLI, event schemas, the WorkspacePlan schema, stored schemas, and deployment configuration. It owns syntax, operations with their identifiers, fields with wire names, types, and syntactic constraints, the error codes each operation returns (by link to the code's owner), authentication and authorization requirements, and versioning. | The rules Lenny applies to decide an outcome, the meaning and range of a setting, and the meaning of an error code. |
| Cross-cutting | `cross-cutting/` | A property or convention every capability and component upholds: the security model and system threat model, tenant isolation, mTLS and the PKI, availability and failure principles, upgrade and compatibility policy, observability conventions and the metrics every controller emits, the error model and the error codes several rules share, the API conventions several interfaces share (pagination, ETags, dry run and confirm, idempotency keys, correlation and rate-limit headers), data classification and retention, and naming laws. | How one capability meets the property. The capability states that, and the cross-cutting page lists the capabilities that carry it. |
| Catalog | `catalogs/` | Nothing. Each catalog is generated from definition blocks on owner pages: settings and defaults (with tier presets), limits, errors, metrics, alerts, events, labels and annotations, custom resources, API operations, requirements, state machines, channels, and the glossary. | Any definition. |
| Index | `index.md` in every folder | The folder's summary, its reading paths, the table of its pages with their summaries, and the components and interfaces that serve it. | Any fact a page in the folder owns. |
| Conventions | `README.md`, `conventions.md` | How to read and write the specification: page types, this placement procedure, normative language, identifiers, the code registry, and the gloss rule. | Content about Lenny. |

## Placement procedure

Ask the questions in order and stop at the first yes. Apply the procedure to each paragraph; a heading is too coarse a unit, because a third of the current text sits under headings whose paragraphs go to five or more pages.

1. Is it about how to read or write the specification? Conventions.
2. Is it a plan, a decision history, a comparison with other products, an open question, or a roadmap item? Is it guidance (sizing, tuning, worked examples), a procedure (a runbook), a benchmark or calibration method, a test plan, a client protocol mapping, or a contributor procedure? It leaves the specification. See "Destinations outside the specification".
3. Is it a value in an enumerable set (an error code, a metric, an alert, a setting, a label, an event type, a custom resource kind, a state)? Define it on its owner page (see "Owners of enumerable facts") and let the catalog list it.
4. Can a party be built against it without knowing how the other party decides? It is an interface fact: syntax, fields, operations, the codes returned, authentication, versioning.
5. Does every capability or component have to uphold it? Cross-cutting.
6. Is it about one running unit as a unit, or about a mechanism only that implementation has? Component.
7. Is it about what the system does, decides, or guarantees, or what a domain object is? Capability.
8. Does it trace a scenario across capabilities without adding a rule? Walkthrough.
9. Is it purpose, context, or a map? Overview.

**Interface or capability.** A statement that holds for any implementation behind the interface and is visible to the caller belongs to the interface ("`POST /v1/admin/pools` returns `409 RESOURCE_ALREADY_EXISTS` when a pool with the name exists"). A statement about how Lenny decides belongs to the capability ("a pool name is refused when it is not a valid Kubernetes label value, because …"). The interface lists the code and links its owner; the capability states the rule and links the operation.

**Component or capability.** If replacing the component with a different implementation of the same responsibilities would leave the statement true, it belongs to the capability. Otherwise it belongs to the component, which numbers it as its own requirement when it is normative.

**A Go type inside one binary** is a component's internal part, described in words where a reader needs it, with its signature left to package documentation. A Go interface a deployer can replace is an extension-point interface page.

## Facts that are both rule and syntax

Some facts have a rule (with a reason) and a syntactic form an interface exposes. Each is stated once, with the other side linking:

| Fact | Stated on | The other side |
| :-- | :-- | :-- |
| A field that carries a setting | The setting block on the capability: meaning, default, range, scopes. | The interface states the wire name, the type, and the codes returned, and links the setting. |
| A field whose values are a capability's states | The capability's model block. | The interface field's value list is generated from the model block. |
| A naming rule for an object (`claim-<podName>`, `<pool>.<key16>`) | The capability requirement. | The interface field's constraint links the requirement. |
| A label's mutability | The labels page entry (key, values, carriers, writers, readers, mutability). | The capability requirement states the reason and links the entry; a resource page's admission table lists the refusal and links the entry. |

## Owners of enumerable facts

| Fact | Owner |
| :-- | :-- |
| Error code produced by one rule | The capability whose rule produces it. Each interface that returns it lists it by link. |
| Error code with several causes | The cross-cutting error model. |
| Behavioral setting | The capability whose object it configures. Other users link it. |
| Process or deployment setting | The component. |
| Metric that measures a capability's behavior | The capability. The definition block names its emitter, and the emitter's component page lists it. |
| Metric about a unit's health as a process | The component. |
| Metric every controller emits | The cross-cutting observability page. |
| Alert that detects a breach of a requirement | The owner of the requirement. |
| Alert that detects a unit's health | The component. |
| Event type | The interface that carries it, with the emitter named in the definition block. |
| Label or annotation key | The Kubernetes labels and annotations page. |
| Custom resource kind | Its Kubernetes interface page. |
| Domain object and its state machine | The capability that governs its lifecycle. When the capability splits into a folder, the object and its state machine stay on one page named for the object. |
| Register shared by two components | The channel interface page that carries it. |
| Register one component both writes and reads | That component. |
| Limit or default, including a per-tier default | Wherever the setting that carries it is owned. Tier preset files are generated. |

Each definition is a structured block on the owner page, so a generator builds the catalogs:

````markdown
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

Metrics carry `emitter:`, events carry `emitter:`, and settings carry `read-by:`, so the component pages' lists are generated as well.

## Destinations outside the specification

| Content | Destination |
| :-- | :-- |
| The phased build sequence, phase gates, and absent-behavior claims | `BUILD-PLAN.md` and `BUILD-GAPS.md` |
| Decision history and alternatives | The archived design or proposal |
| Roadmap and post-v1 items | `ROADMAP.md` |
| Open questions | Issues |
| Comparison with other products and community strategy | `docs/about/` |
| Benchmarks, calibration methods, and the tests that verify a requirement | `TESTING.md`; a requirement may name its test tiers |
| Sizing, tuning, and worked examples | `docs/operator-guide/` |
| Runbook procedures | `docs/runbooks/`; the runbook format stays in the specification |
| Client protocol mappings (MCP, OpenAI, A2A) | The adapter repositories |
| Reference runtime descriptions | The reference runtime repositories and the runtime author guide; the runtime protocol and its conformance suite stay |
| Contributor procedures | Contributor documentation |
| Go signatures of internal types | Package documentation |

## Identifiers

- Every page has a code of two to nine upper-case letters with no hyphen, registered in `conventions.md` before use. A gate refuses a duplicate and a code that differs from an existing one only by a suffix (`DERIVE` beside `DERIVED`).
- Requirements are numbered per page and carry a title: `**POOL-R5. Pool and pod-set labels are required and immutable.**` Numbers are never reused, and a retired requirement keeps its number with a successor pointer.
- Each requirement and each owned term carries an explicit anchor equal to its lower-cased identifier or term slug (`#pool-r5`, `#reserved-hold`).
- Interface operations carry identifiers of the form `<interface folder>#<resource>.<verb>`: `admin-api#pools.create`, `kubernetes#podclaim.create`.
- Code cites requirements or operations: `// spec: POOL-R5`, `// spec: admin-api#pools.create`. An identifier survives any move of its page. A heading anchor that moves goes through the redirect map.

## Gloss rule

A page that uses a term it does not own gives a one-sentence gloss and a link to the owner. A gloss says what the thing is. It never states a value, a default, a rule, or a limit. A gate checks that a gloss holds no number, no normative keyword, and exactly one link.

## Page template

Every page opens with front matter. The renderer turns the summary into the opening paragraph, so the body does not repeat it.

```yaml
---
code: POOL
type: capability   # overview | walkthrough | capability | component | interface | cross-cutting | catalog | index
area: runtimes-and-pools
summary: >-
  Two to four sentences that state what the page covers, used by the index pages.
owns: [objects, requirements, settings, errors, metrics, alerts]
depends-on: [RUNTIME, REV, PLACE]
---
```

Every content page then has **Terms** (owned terms with definitions; borrowed terms with a gloss and a link) and **Overview** (a plain-language explanation without normative values), followed by the type's sections:

| Type | Sections after the overview |
| :-- | :-- |
| Capability | Model (objects, states, invariants); Requirements; Flows (each step links a component and an operation); Settings; Errors; Metrics and alerts; Failure behavior; Security properties and threats; Limits; Rationale; Related capabilities. |
| Component | Responsibilities; Interfaces served and consumed; Internal parts; Requirements of its own mechanisms; Deployment; Settings read (generated, with process settings defined here); Failure and recovery; Metrics and alerts (its own definitions, then the generated list of what it emits). |
| Interface | Conventions for this interface (or a link to the cross-cutting API conventions); Operations or resources, each with fields, constraints, codes returned, and the requirements behind each outcome; Compatibility. |
| Cross-cutting | The property; the rules every page upholds; the pages that carry it, with the requirement each states. |
| Walkthrough | The scenario; numbered steps, each linking a requirement and an operation. |

## Progressive discovery

- `README.md` is the root map: what Lenny is in one paragraph, the areas, and reading paths by task ("to change how pods are claimed, read …").
- Each folder has an `index.md` that stays under about 1,500 words.
- A content page stays under about 6,000 words. A capability that carries a sizing formula or a sub-procedure longer than about 1,000 words moves it to a sub-page.

## Open decisions for the owner

1. **Capability areas.** The map uses thirteen areas: runtimes and pools, sessions, messaging, workspaces and files, delegation, tenancy and access, credentials, network and egress, policy and controls, billing and usage, observability and operations, security and compliance, and deployment and lifecycle ([FINDINGS-map.md](FINDINGS-map.md), "Changes to the starting lists").
2. **The gateway's internal parts.** The gateway is one component folder. Its sub-pages need a list that matches the designs, because the four subsystems of §4.1 no longer do. The map proposes session coordination, drain, the checkpoint barrier, scaling and capacity, the pod claimer, the metering proxy, and the ops endpoints.
3. **Metric ownership.** The two halves disagreed: the slice found that owning metrics by emitter scatters one capability's metrics, and the map found that the current duplication follows the emitter. Version 1 resolves it by meaning, with the emitter recorded in each block and the component's list generated.
4. **Reference runtimes leave the specification.** The map sends §26 to the reference runtime repositories and the runtime author guide.

## Changes from version 0

| Change | Finding |
| :-- | :-- |
| Paragraph-level placement | Map 1, 2 |
| Walkthrough type and its gate | Map open question 2 |
| Index type | Slice 17; map 15 |
| Components own requirements of their own mechanisms | Map 3 |
| Metrics by meaning, emitter recorded; unit-health metrics and alerts on the component; controller metrics cross-cutting | Slice 2, 3; map 4 and open question 3 |
| Single-cause errors on the capability; the example code corrected | Slice 4, 5 |
| "Facts that are both rule and syntax" table | Slice 6, 7; map 11 |
| Representations and registers in the owners table | Slice 8; map 5 |
| Settings used by several capabilities; `scopes`, `defaults-by-tier`, `read-by` | Slice 1, 10; map 7, 13 |
| Go types and extension points | Slice 11; map 8 |
| Destinations outside the specification | Slice 12, 13; map 9; map open question 6 |
| Operation identifiers for every interface | Slice 14 |
| Requirement titles and anchors; term anchors | Slice 15, 19 |
| Summary rendered once | Slice 16 |
| Code format and registry | Slice 18; map 14 |
| Limits, and threats in the security section | Map proposals 11, 12 |
| Object and state machine on one page in a capability folder | Map open question 1 |
| State-machine and channel catalogs | Map proposal 16 |
| Shared API conventions cross-cutting | Map 6 |
| Sub-page rule for formulas and sub-procedures | Slice open question 7 |
