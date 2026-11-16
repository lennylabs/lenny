# Proposed specification tree: map

Status: spike output, 2026-11-16. This file lists every page of the proposed tree under `proposed/`, with its type, its code, a one-line summary, and an estimate of its size. Paths are relative to `proposed/`. The "Written by" column records which half of the spike owns the page: "slice" pages are written in full by the vertical slice, "map" pages are named and summarized here only, and "proposed to slice" pages are catalogs this map adds to the slice's catalog list.

Rows marked "seams v2" in the "Written by" column were edited by hand after generation, to apply "Gateway seams" in [TAXONOMY.md](../TAXONOMY.md) version 2; their estimates are judgment, and `ASSIGNMENT.md` still splits §4.1 "Gateway Internal Subsystems" the way version 1 did.

The size estimate is the words of the current specification sections assigned to the page, weighted by the share given in `ASSIGNMENT.md`, plus half the words of the design sections assigned to it. Designs carry evidence tags, current-state notes, and rationale that a page does not keep, so half is the working ratio. Index, catalog, and conventions pages have no estimate. `ASSIGNMENT.md` lists the pages estimated over 6,000 words and the split each one takes.

## Page counts

| Type | Pages |
| :-- | --: |
| overview | 15 |
| capability | 103 |
| component | 38 |
| interface | 61 |
| cross-cutting | 10 |
| catalog | 13 |
| conventions | 2 |
| index | 36 |
| Total | 278 |

Index pages are counted once under "index" and not under the type of their folder. The overview count includes the walkthrough pages.

## Overview

The walkthroughs are non-normative. Each traces one scenario and links the requirements and operations it passes through; none states a rule.

| Path | Type | Code | Summary | Est. words | Written by |
| :-- | :-- | :-- | :-- | --: | :-- |
| `overview/index.md` | index | OVW | Lists the overview pages: purpose and context, principles and non-goals, the architecture, the capability and interface maps, and the walkthroughs. |  | map |
| `overview/architecture.md` | overview | ARCH | Maps the components, the namespaces they run in, the stores they use, and the links between them, with one line per component and a link to its page. | 2,200 | map |
| `overview/capability-map.md` | overview | CAPMAP | Maps each capability area and capability to the components that carry it out and the interfaces that expose it. | new | map |
| `overview/interface-map.md` | overview | IFMAP | Maps each interface to the parties on each side, its transport, and its owning pages, and states that the client contract is REST alone. | 800 | map |
| `overview/principles-and-non-goals.md` | overview | PRIN | States the design principles, the goals, the non-goals, and the decisions Lenny leaves to deployers. | 1,200 | map |
| `overview/purpose-and-context.md` | overview | CTX | States what Lenny is for, the actors that use it (clients, tenant admins, platform operators, runtime authors, and DevOps agents), and the external systems it depends on. | 200 | map |
| `overview/walkthroughs/index.md` | index | WALK | Lists the non-normative walkthroughs that trace one scenario across components and channels, and states the trace notation. |  | map |
| `overview/walkthroughs/checkpoint-capture.md` | overview | WKCKPT | Traces a checkpoint from its trigger through quiesce, capture passes, and upload. | 2,000 | map |
| `overview/walkthroughs/coordinator-handoff.md` | overview | WKHANDOFF | Traces a coordinator handoff and a crash takeover between gateway replicas. | 1,000 | map |
| `overview/walkthroughs/gateway-drain.md` | overview | WKDRAIN | Traces a gateway replica drain with its checkpoint barrier and session handoff. | 1,000 | map |
| `overview/walkthroughs/interactive-message.md` | overview | WKMSG | Traces one client message through delivery, the runtime's turn, and the streamed output. | 4,500 | map |
| `overview/walkthroughs/interrupt-terminate-delete.md` | overview | WKSTOP | Traces an interrupt, a terminate, and a delete through the gateway, the adapter, and the pod. | 1,600 | map |
| `overview/walkthroughs/ops-remediation.md` | overview | WKOPS | Traces a DevOps agent observing an operational event, diagnosing it, taking a remediation lock, and verifying recovery. | 600 | map |
| `overview/walkthroughs/pod-eviction.md` | overview | WKEVICT | Traces the eviction of an agent pod that holds an active session. | 700 | map |
| `overview/walkthroughs/restore-and-resume.md` | overview | WKRESUME | Traces a resume after pod loss, from the failure through a new claim and restore. | 1,400 | map |
| `overview/walkthroughs/session-start.md` | overview | WKSTART | Traces a session from the create request through pod claim, materialization, and the first message. | 2,500 | map |
| `overview/walkthroughs/shared-pod-co-tenancy.md` | overview | WKCOTEN | Traces two sessions on one pod with several slots. | 1,300 | map |

## Capabilities

Each capability area is a folder under `capabilities/`. A capability larger than one page becomes a folder with its own index (`sessions/session-lifecycle/`, `sessions/checkpoints/`).

| Path | Type | Code | Summary | Est. words | Written by |
| :-- | :-- | :-- | :-- | --: | :-- |
| `capabilities/index.md` | index | CAPS | Lists the capability areas with their summaries and the capabilities in each. |  | map |

### Runtimes and pools

| Path | Type | Code | Summary | Est. words | Written by |
| :-- | :-- | :-- | :-- | --: | :-- |
| `capabilities/runtimes-and-pools/index.md` | index | RTPOOL | Lists the capabilities that decide which runtimes Lenny runs, on which pods, and how those pods are kept warm, claimed, reused, and retired. |  | slice |
| `capabilities/runtimes-and-pools/agent-pod-lifecycle.md` | capability | APOD | Defines the agent pod and its states and how a pod is created, becomes ready, is recycled, drained, quarantined, and deleted. | 6,100 | slice |
| `capabilities/runtimes-and-pools/isolation-profiles.md` | capability | ISO | Defines the isolation profiles a pool can run under, what each profile isolates, and the acknowledgements a weaker profile requires. | 400 | map |
| `capabilities/runtimes-and-pools/owned-pools-and-reservations.md` | capability | OWNPOOL | States the pools a tenant owns for its derived runtimes, the pod budget, and capacity reservations. | 1,400 | map |
| `capabilities/runtimes-and-pools/placement.md` | capability | PLACE | Assigns each pool a node class, an offering, a region, and a tenancy, renders them into each pod, and checks that pods land where their placement says. | 4,700 | map |
| `capabilities/runtimes-and-pools/pod-claims.md` | capability | CLAIM | Acquires an idle pod for a session through a per-pod claim, holds a recycled pod for its tenant, and recovers orphaned claims. | 3,400 | slice |
| `capabilities/runtimes-and-pools/resource-classes.md` | capability | RCLASS | Defines resource classes in configuration, how a pool's class maps to container requests and limits, and the checks at load, write, and reconcile. | 1,000 | map |
| `capabilities/runtimes-and-pools/runtime-registry.md` | capability | RUNTIME | Registers runtimes, states the runtime fields and their rules, operator-derived runtimes and the merge of derived fields, and published metadata. | 3,100 | map |
| `capabilities/runtimes-and-pools/runtime-revisions.md` | capability | REV | Records each change to a runtime's versioned fields as a revision, selects a revision per session, and moves traffic between revisions in a rollout. | 4,900 | map |
| `capabilities/runtimes-and-pools/runtime-routes.md` | capability | ROUTE | Chooses the runtime for a session from routing labels and runtime routes, which replace experiment variants. | 1,700 | map |
| `capabilities/runtimes-and-pools/tenant-derived-runtimes.md` | capability | DERIVED | Lets a granted tenant derive its own runtime from a platform base, with an owned pool, a pod budget, and capacity reservations. | 5,800 | map |
| `capabilities/runtimes-and-pools/warm-pools.md` | capability | POOL | Defines pools and pod sets, keeps warm inventory between its targets, and protects warm pods from voluntary disruption. | 8,600 | slice |

### Sessions

| Path | Type | Code | Summary | Est. words | Written by |
| :-- | :-- | :-- | :-- | --: | :-- |
| `capabilities/sessions/index.md` | index | SESAREA | Lists the capabilities that govern a session from creation to its end, including suspension, resume, checkpoints, derive, and isolation between sessions on one pod. |  | map |
| `capabilities/sessions/checkpoints/index.md` | index | CKIDX | Lists the checkpoint pages: capture, storage and retention, and restore. |  | map |
| `capabilities/sessions/checkpoints/capture-passes.md` | capability | CKPASS | States how a checkpoint is captured in passes, the change tracker, and the size limits and drain budgets that bound a capture. | 4,300 | map |
| `capabilities/sessions/checkpoints/checkpoint-capture.md` | capability | CKPT | States when a checkpoint is taken, how capture runs in passes, quiesce and abort, size limits and drain budgets, and the checkpoint capability model. | 6,300 | map |
| `capabilities/sessions/checkpoints/checkpoint-storage.md` | capability | CKSTORE | States the checkpoint catalog, reference closure, quota, retention, garbage collection, and the behavior when the object store fails. | 5,000 | map |
| `capabilities/sessions/checkpoints/restore.md` | capability | RESTORE | States how a checkpoint is restored into a slot for resume, derive, and download, and how a restore is sealed. | 700 | map |
| `capabilities/sessions/derive.md` | capability | SDERIVE | Starts a session from an earlier one at its start or at its latest checkpoint, with the copy and restore rules and the options a derive takes. | 3,400 | map |
| `capabilities/sessions/pod-reuse-and-scrub.md` | capability | SCRUB | States when a pod may serve another session, the scrub that runs between sessions, the reuse prohibitions, and the acknowledgements weak reuse requires. | 3,800 | map |
| `capabilities/sessions/retry-and-resume.md` | capability | RESUME | States the retry policy after a pod failure, session generations, the resume flow, and the states a session takes when retries run out. | 3,700 | map |
| `capabilities/sessions/scrub-procedure.md` | capability | SCRUBPROC | States the scrub procedure step by step, the supervised scrub sequence, what the scrub reaches, the sweep, and the Kata variant. | 2,400 | map |
| `capabilities/sessions/session-isolation.md` | capability | SLOT | States the isolation between sessions that share a pod: slots, slot identity, slot release, the per-slot resource and credential scope, and slot failure. | 5,000 | map |
| `capabilities/sessions/session-lifecycle/index.md` | index | SESLC | Lists the pages of the session lifecycle: states, creation, suspension and dormancy, the end of a session, and timeouts. |  | map |
| `capabilities/sessions/session-lifecycle/session-creation.md` | capability | SCREATE | States the creation flow from request to a bound pod, its atomicity, the checks at the post-chain point, and the isolation level reported to the client. | 2,700 | map |
| `capabilities/sessions/session-lifecycle/session-end.md` | capability | SEND | States how a session closes, the seal against further delivery, the terminal pipeline, and the window after a terminal state. | 2,500 | map |
| `capabilities/sessions/session-lifecycle/session-states.md` | capability | SESS | Defines the session object, its identity, its states and transitions, its failure class, and the mapping of internal states to the states clients see. | 5,500 | map |
| `capabilities/sessions/session-lifecycle/suspension-and-dormancy.md` | capability | SUSP | States when a session suspends, what happens to its slot and pod, dormancy of idle sessions, and suspension of a delegation tree. | 3,000 | map |
| `capabilities/sessions/session-lifecycle/timeouts.md` | capability | TIMEOUT | Defines every session timer and watchdog, which states pause each one, and cancellation. | 2,000 | map |
| `capabilities/sessions/setup-commands.md` | capability | SETUP | States the setup commands a runtime and a plan declare, their policy, ordering, limits, and failure. | 900 | map |
| `capabilities/sessions/slot-assignment-and-failure.md` | capability | SLOTOPS | States how a session is assigned a slot atomically, slot liveness and release, slot failure and retry, and the rehydration of slots after recovery. | 3,100 | map |

### Messaging

| Path | Type | Code | Summary | Est. words | Written by |
| :-- | :-- | :-- | :-- | --: | :-- |
| `capabilities/messaging/index.md` | index | MSGAREA | Lists the capabilities that carry messages into a session and output out of it: delivery, inboxes, inter-session messages, the event stream, input requests, and client tools. |  | map |
| `capabilities/messaging/client-tools.md` | capability | CTOOL | Lets a client supply tools a runtime calls, with admission, response validation, pending calls, and continuation. | 2,400 | map |
| `capabilities/messaging/event-stream.md` | capability | STREAM | States the session event stream: event kinds and their order, activity, output and reasoning streaming, turn and usage events, resume after a gap, and back-pressure. | 3,800 | map |
| `capabilities/messaging/inbox-and-custody.md` | capability | INBOX | States the session inbox, its states and custody, durability, expiry, retraction, dead-lettering, and delivery receipts. | 3,500 | map |
| `capabilities/messaging/input-requests.md` | capability | INPUT | States how a runtime asks the client for input, the elicitation chain through delegation, harness approvals, and the session's input policy. | 2,700 | map |
| `capabilities/messaging/inter-session-messaging.md` | capability | IMSG | States how one session messages another: scope, the configuration hierarchy, rate limits, content policy, and threading. | 2,500 | map |
| `capabilities/messaging/message-delivery.md` | capability | MSG | States the delivery intents, the delivery paths, turn accounting, and the executor that delivers one message at a time to the runtime. | 4,300 | map |

### Workspaces and files

| Path | Type | Code | Summary | Est. words | Written by |
| :-- | :-- | :-- | :-- | --: | :-- |
| `capabilities/workspaces-and-files/index.md` | index | WSAREA | Lists the capabilities that build a session's workspace and move files in and out of it. |  | map |
| `capabilities/workspaces-and-files/artifacts.md` | capability | ARTIFACT | States what Lenny keeps as session artifacts, transcripts, blob resolution, deduplication, and artifact retention. | 1,500 | map |
| `capabilities/workspaces-and-files/default-clones.md` | capability | CLONE | States the default git clones a tenant-derived runtime declares and how they reach a session's workspace. | 6,600 | map |
| `capabilities/workspaces-and-files/file-export.md` | capability | FILEX | States the file export model between sessions and to clients, export limits, and scanning of exported files. | 1,100 | map |
| `capabilities/workspaces-and-files/files-in-messages.md` | capability | FILEMSG | States how files travel with messages and output, including files a child returns to its parent. | 3,700 | map |
| `capabilities/workspaces-and-files/materialization.md` | capability | MATER | States how the adapter and the helper build a workspace from a plan inside the slot, the ordering and collision rules, atomicity, and the validation of results. | 4,400 | map |
| `capabilities/workspaces-and-files/repository-stores.md` | capability | REPO | States the shared repository stores on a pod, the warm list, repository folders, and the warm-up checkout. | 1,900 | map |
| `capabilities/workspaces-and-files/uploads.md` | capability | UPLOAD | States upload authorization, the upload token, size caps, archive handling, and mid-session uploads. | 2,000 | map |
| `capabilities/workspaces-and-files/workspace-plan.md` | capability | WSPLAN | States what a workspace plan means: its sources, when it binds, its defaults, and the rules Lenny applies to each member. | 1,000 | map |

### Delegation

| Path | Type | Code | Summary | Est. words | Written by |
| :-- | :-- | :-- | :-- | --: | :-- |
| `capabilities/delegation/index.md` | index | DLGAREA | Lists the capabilities that let a session start child sessions and govern the resulting tree. |  | map |
| `capabilities/delegation/delegation-availability.md` | capability | DAVAIL | States the delegation levels, how a session's level is resolved, the refusal when delegation is unavailable, and the tool list a runtime receives. | 2,200 | map |
| `capabilities/delegation/delegation-lease.md` | capability | LEASE | Defines the delegation lease, its fields and inheritance, tree-wide limits, isolation monotonicity, and lease extension. | 4,800 | map |
| `capabilities/delegation/delegation-policy.md` | capability | DPOL | Defines the delegation policy resource, tag evaluation, the session-level policy cap, content policy, and the scanning of exported files. | 4,300 | map |
| `capabilities/delegation/delegation.md` | capability | DLG | States the delegation mechanism, approval modes, the task tree, task records and results, and what a parent sees of its children. | 2,900 | map |
| `capabilities/delegation/lease-extension.md` | capability | LEASEXT | States how a delegation lease is extended, the configuration layering, and the extension approval modes. | 2,400 | map |
| `capabilities/delegation/tree-recovery-and-cascade.md` | capability | DTREE | States how a delegation tree recovers, cascades on a parent's end, cleans up orphans, and how parents watch children's streams. | 4,800 | map |

### Tenancy and access

| Path | Type | Code | Summary | Est. words | Written by |
| :-- | :-- | :-- | :-- | --: | :-- |
| `capabilities/tenancy-and-access/index.md` | index | TENAREA | Lists the capabilities that define tenants, environments, and principals and decide who may do what. |  | map |
| `capabilities/tenancy-and-access/authentication.md` | capability | AUTHN | States how callers authenticate: OIDC, API keys, development mode, the token claims Lenny reads, key rotation, user invalidation, and the playground's session. | 4,500 | map |
| `capabilities/tenancy-and-access/authorization.md` | capability | AUTHZ | Defines the roles, the permission matrix, custom roles, scopes, session ownership, security roles, and delegated approvers. | 5,700 | map |
| `capabilities/tenancy-and-access/browser-sessions.md` | capability | BROWSER | States the cookie session a browser holds for the web playground and the token mint rules behind it. | 2,200 | map |
| `capabilities/tenancy-and-access/environments.md` | capability | ENV | Defines environments, how runtimes are selected into them, the environment context of a session, and cross-environment rules. | 4,800 | map |
| `capabilities/tenancy-and-access/principals.md` | capability | PRINC | Defines users, service accounts, and subjects, and how each is identified within a tenant. | 4,800 | map |
| `capabilities/tenancy-and-access/security-roles-and-approvers.md` | capability | SECROLE | Defines the security roles and the delegated approvers who confirm security-weakening changes. | 1,700 | map |
| `capabilities/tenancy-and-access/tenant-settings.md` | capability | TSET | Defines the tenant settings model, templates, the tenant key, compliance profiles, and how a setting changes on a live tenant. | 4,100 | map |
| `capabilities/tenancy-and-access/tenants.md` | capability | TENANT | Defines the tenant object and its lifecycle from creation to deletion. | 1,900 | map |
| `capabilities/tenancy-and-access/token-exchange.md` | capability | TOKX | States the token exchange Lenny runs, scope narrowing, impersonation, write-before-issue, rotation, and revocation of issued tokens. | 4,000 | map |

### Credentials

| Path | Type | Code | Summary | Est. words | Written by |
| :-- | :-- | :-- | :-- | --: | :-- |
| `capabilities/credentials/index.md` | index | CREDAREA | Lists the capabilities that hold credentials and deliver them to sessions and pods. |  | map |
| `capabilities/credentials/account-links.md` | capability | LINK | States how a user links an account through OAuth, the link prompt, and the link check before a session needs the account. | 3,500 | map |
| `capabilities/credentials/credential-add-ons.md` | capability | ADDON | Defines the credential add-ons for GitHub and GitLab, their kinds, and their deny presets. | 3,100 | map |
| `capabilities/credentials/credential-delivery.md` | capability | CDELIV | States how credentials reach a runtime: delivery modes, file delivery, and what a runtime may read. | 6,100 | map |
| `capabilities/credentials/credential-pools.md` | capability | CPOOL | Defines credential pools for VCS providers, their secret topology, leases, governance boundaries, emergency revocation, and secret lifecycle. | 5,300 | map |
| `capabilities/credentials/credential-request-rules.md` | capability | CREQ | States the request rules and presets that decide which credentials a session may request and receive. | 2,300 | map |
| `capabilities/credentials/pull-request-authoring.md` | capability | PRAUTH | States how a session authors pull and merge requests under a user's identity and the independent approval check. | 5,200 | map |
| `capabilities/credentials/repository-authorization.md` | capability | REPOAUTH | States which repositories a session may reach under a user's VCS identity and how that authorization is decided. | 2,900 | map |
| `capabilities/credentials/service-account-credentials.md` | capability | SACRED | Lets a service account own credentials through the client credentials and issuer exchange sources. | 4,200 | map |
| `capabilities/credentials/user-credentials.md` | capability | UCRED | Defines user credential types, the registered source, the user credential binding, and request rules and presets. | 5,100 | map |

### Network and egress

| Path | Type | Code | Summary | Est. words | Written by |
| :-- | :-- | :-- | :-- | --: | :-- |
| `capabilities/network-and-egress/index.md` | index | NETAREA | Lists the capabilities that decide what network traffic agent pods may send and how it is attributed. |  | map |
| `capabilities/network-and-egress/dns.md` | capability | DNS | States the DNS resolution agent pods get and the mitigations against DNS exfiltration. | 600 | map |
| `capabilities/network-and-egress/egress-decisions.md` | capability | EGDEC | States how each outbound connection is attributed to a slot, decided against the effective policy, and recorded in the evidence streams. | 2,600 | map |
| `capabilities/network-and-egress/egress-defaults.md` | capability | EGDEF | States the default egress policy, how a scope replaces or augments it, exfiltration controls, and TLS interception. | 2,700 | map |
| `capabilities/network-and-egress/egress-policy.md` | capability | EGRESS | States the egress policy model: scopes, the effective policy, defaults, replacement and augmentation, exfiltration controls, and TLS interception. | 6,500 | map |
| `capabilities/network-and-egress/git-access.md` | capability | GIT | States how a pod reaches git hosts: covered hosts, the git mirror, and the git configuration injected into a slot. | 3,400 | map |
| `capabilities/network-and-egress/llm-upstreams.md` | capability | LLMUP | Defines LLM upstreams, their assignment to sessions, the metering proxy's routing rules, cache isolation, calls outside the proxy, and forwarded attribution. | 4,800 | map |
| `capabilities/network-and-egress/network-isolation.md` | capability | NETISO | States the default-deny baseline and the allow-lists that apply to agent pods and to the platform namespace. | 2,400 | map |
| `capabilities/network-and-egress/operator-services.md` | capability | OPSVC | States how a runtime reaches operator services through egress, the identity assertion attached to those requests, and the egress halt at suspension. | 2,500 | map |
| `capabilities/network-and-egress/outbound-connector.md` | capability | OUTCONN | Lets a tenant reach hosts on its private network through an agent it runs, with registration, identity, liveness, rotation, and revocation. | 3,500 | map |

### Policy and controls

| Path | Type | Code | Summary | Est. words | Written by |
| :-- | :-- | :-- | :-- | --: | :-- |
| `capabilities/policy-and-controls/index.md` | index | POLAREA | Lists the capabilities that admit, shape, and refuse requests. |  | map |
| `capabilities/policy-and-controls/admission-and-fairness.md` | capability | ADMIT | States the policy engine, the admission checks and their order, and fairness between tenants. | 1,200 | map |
| `capabilities/policy-and-controls/circuit-breakers.md` | capability | BREAKER | States the operator-managed circuit breakers, their storage and propagation, and their effect on admission. | 1,600 | map |
| `capabilities/policy-and-controls/quotas-and-rate-limits.md` | capability | QUOTA | States the quota hierarchy, storage quotas, rate limits, counter recovery, and the fail-open budgets during a store outage. | 4,000 | map |
| `capabilities/policy-and-controls/request-interceptors.md` | capability | INTCPT | States where request interceptors run, how their verdicts apply, their failure policy, and in-cluster and tenant-scoped interceptors. | 2,500 | map |

### Billing and usage

| Path | Type | Code | Summary | Est. words | Written by |
| :-- | :-- | :-- | :-- | --: | :-- |
| `capabilities/billing-and-usage/index.md` | index | BILLAREA | Lists the capabilities that meter usage, enforce budgets, and emit billing events. |  | map |
| `capabilities/billing-and-usage/billing-events.md` | capability | BILLEV | States the billing event stream: what emits each event, delivery sinks, immutability, and corrections. | 2,300 | map |
| `capabilities/billing-and-usage/budgets.md` | capability | BUDGET | Defines credit budgets, budget inheritance, and the delegation budget counters. | 3,000 | map |
| `capabilities/billing-and-usage/rate-cards.md` | capability | RATE | Defines rate cards and how a usage record is priced against them. | 300 | map |
| `capabilities/billing-and-usage/resource-attribution.md` | capability | ATTR | Attributes pod time, idle pods, stored bytes, and dedicated nodes to tenants, with reconciliation against the object store. | 2,000 | map |
| `capabilities/billing-and-usage/usage-records.md` | capability | USAGE | Defines usage records for LLM calls and resources and the usage the client API reports. | 700 | map |

### Observability and operations

| Path | Type | Code | Summary | Est. words | Written by |
| :-- | :-- | :-- | :-- | --: | :-- |
| `capabilities/observability-and-operations/index.md` | index | OPSAREA | Lists the capabilities that let operators and DevOps agents observe, diagnose, and remediate the platform. |  | map |
| `capabilities/observability-and-operations/alerting-and-slos.md` | capability | SLO | States the SLO targets, burn-rate alerting, the bundled alert set and its operator customization, and SLO export. | 4,900 | map |
| `capabilities/observability-and-operations/configuration-drift.md` | capability | DRIFT | States how drift between the declared and the running configuration is detected and reconciled. | 1,400 | map |
| `capabilities/observability-and-operations/health-and-diagnostics.md` | capability | DIAG | States how platform health and capacity recommendations are derived, the diagnostic operations, and automatic remediation. | 2,200 | map |
| `capabilities/observability-and-operations/operational-events.md` | capability | OPSEV | States which state changes emit operational events, buffering in the gateway, and delivery by stream, polling, and webhook. | 2,700 | map |
| `capabilities/observability-and-operations/remediation-and-escalation.md` | capability | REMED | States remediation locks, their consistency, stealing and long holds, the operations inventory, and escalation. | 1,900 | map |
| `capabilities/observability-and-operations/runbooks.md` | capability | RUNBOOK | States the machine-readable runbook format and how runbooks are discovered from health results and alerts. | 1,500 | map |

### Security and compliance

| Path | Type | Code | Summary | Est. words | Written by |
| :-- | :-- | :-- | :-- | --: | :-- |
| `capabilities/security-and-compliance/index.md` | index | SECAREA | Lists the capabilities that record evidence, oversee security, and meet data obligations. |  | map |
| `capabilities/security-and-compliance/agent-activity-streams.md` | capability | ACTSTR | States the agent activity streams recorded for forensics and their retention. | 3,000 | map |
| `capabilities/security-and-compliance/audit-log.md` | capability | AUDIT | States what is audited, the integrity of the audit log, tenant validation at write, and audit queries. | 4,400 | map |
| `capabilities/security-and-compliance/data-residency.md` | capability | RESID | States data residency, its admission control, key and backup residency, and the multi-region consistency model. | 3,400 | map |
| `capabilities/security-and-compliance/erasure.md` | capability | ERASE | States user-level erasure: the erasure job, its order of deletion, pseudonymization, receipts, processing restriction, and erasure of backups and external sinks. | 4,600 | map |
| `capabilities/security-and-compliance/legal-hold.md` | capability | HOLD | States legal and platform-only holds, their effect on retention and checkpoint rotation, and the hold reconciler. | 3,400 | map |
| `capabilities/security-and-compliance/pseudonymization-and-receipts.md` | capability | PSEUDO | States the pseudonymization of retained records at erasure, the erasure salt, billing erasure policy, and erasure receipts. | 1,900 | map |
| `capabilities/security-and-compliance/quarantine-and-evidence.md` | capability | FORENSIC | States pod quarantine for evidence, the reconciler that ends it, the capture workflow, and security incident response. | 3,300 | map |
| `capabilities/security-and-compliance/security-oversight.md` | capability | SECOV | States the tenant security record and posture, operator access transparency, and the rights of the operability service. | 4,200 | map |
| `capabilities/security-and-compliance/siem-export.md` | capability | SIEM | States how audit records reach a SIEM, the OCSF mapping, per-tenant destinations, dead-lettering, and the erasure boundary. | 3,100 | map |

### Deployment and lifecycle

| Path | Type | Code | Summary | Est. words | Written by |
| :-- | :-- | :-- | :-- | --: | :-- |
| `capabilities/deployment-and-lifecycle/index.md` | index | DEPAREA | Lists the capabilities that install, size, upgrade, back up, and restore a Lenny deployment. |  | map |
| `capabilities/deployment-and-lifecycle/backup-and-restore.md` | capability | BACKUP | States backup creation, scheduling, retention, verification, restore with its safety checks, and disaster recovery targets. | 5,000 | map |
| `capabilities/deployment-and-lifecycle/capacity-tiers.md` | capability | TIER | Defines the capacity tiers, the scale each tier targets, and the workload assumptions behind them. | 1,400 | map |
| `capabilities/deployment-and-lifecycle/installation.md` | capability | INSTALL | States what installation creates, the bootstrap seed, namespace quotas, image registries and air-gapped installs. | 3,300 | map |
| `capabilities/deployment-and-lifecycle/local-development.md` | capability | LOCALDEV | States the local development modes, the embedded stack, its backends, and its guard rails. | 2,200 | map |
| `capabilities/deployment-and-lifecycle/platform-upgrades.md` | capability | UPGRADE | States the platform upgrade state machine, upgrade checks and release channels, schema migration, CRD upgrades, and configuration diff and apply. | 3,300 | map |

## Components

Lenny's own processes come first, then the agent pod and its containers, then the external dependencies under `components/external/`. The gateway, lenny-ops, and the agent pod are folders with sub-pages; `FINDINGS-map.md` gives the reasons.

| Path | Type | Code | Summary | Est. words | Written by |
| :-- | :-- | :-- | :-- | --: | :-- |
| `components/index.md` | index | COMPS | Lists the components with their summaries, grouped as Lenny processes, agent pod containers, and external dependencies. |  | map |
| `components/admission-webhooks.md` | component | WEBHOOK | The admission webhook server and admission policies: what is rendered, availability, failure modes, and metrics. | 2,600 | slice |
| `components/agent-dns.md` | component | AGDNS | The dedicated CoreDNS instances for agent pods, including the resolver for proxied pods. | 1,200 | map |
| `components/agent-pod/index.md` | index | POD | The agent pod as a unit: its containers in start order, volumes and size limits, project quotas, security context, and pod-level metrics. |  | map |
| `components/agent-pod/adapter.md` | component | ADAPTER | The adapter container: startup sequence, the slot registry, gateway and runtime connections, health checks, process settings, and metrics. | 2,200 | map |
| `components/agent-pod/init-and-support-containers.md` | component | PODINIT | The init containers that stage platform binaries and fill repository stores, and the keepalive container. | 300 | map |
| `components/agent-pod/netagent.md` | component | NETAGENT | The netagent sidecar: the local egress endpoint, per-slot attribution, git on covered hosts, and per-slot credential files. | 2,500 | map |
| `components/agent-pod/runtime-supervisor.md` | component | SUPV | The slot containers and the runtime supervisor that runs as PID 1 in each: process launch and end, the sweep, the file-descriptor broker, and the materialization helper. | 5,500 | map |
| `components/agent-pod/volumes-and-quotas.md` | component | PODVOL | The agent pod's volumes, their size limits and eviction behavior, and the project quotas on slot volumes. | 2,700 | map |
| `components/egress-decision-service.md` | component | EGAUTHZ | The egress decision service (lenny-egress-authz): pod identification, policy reads, deployment, and metrics. | 1,700 | map |
| `components/egress-proxy.md` | component | EGPROXY | The central egress proxy (lenny-egress): deployment, transport security, and metrics. | 1,700 | map |
| `components/external/index.md` | index | EXTIDX | Lists the external dependencies and the requirements Lenny places on each. |  | map |
| `components/external/cert-manager.md` | component | CERTM | cert-manager: the certificates Lenny requests, CA rotation, and failure modes. | 1,000 | map |
| `components/external/kms.md` | component | KMS | The KMS: keys Lenny uses, who may call them, rotation, and failure behavior. | 500 | map |
| `components/external/kubernetes.md` | component | KUBE | The Kubernetes API server, etcd, and the CNI: version floor, API rate limits, etcd tuning, and network policy enforcement. | 1,000 | map |
| `components/external/object-store.md` | component | OBJ | The object store: backends, HA, versioning, replication, lifecycle rules, encryption, and outage handling. | 4,800 | map |
| `components/external/postgres.md` | component | PG | Postgres and its pooler: topology, HA, connection pooling, write classification, failover behavior, encryption, and backups. | 5,700 | map |
| `components/external/prometheus.md` | component | PROM | Prometheus: when it is required, scrape targets, rule rendering, evaluation cost, and the bring-your-own model. | 1,000 | map |
| `components/external/redis.md` | component | REDIS | Redis: topology, logical separation, failure modes per use, scalability ceilings, and TLS. | 4,500 | map |
| `components/gateway/index.md` | index | GW | The gateway as a unit: the seam model (isolation guarantees every seam has and the rule that a seam reaches the core only through its seam interface), the seam, core, and replica page lists, replicas, disruption budget, probes, ServiceAccount, RBAC, NetworkPolicy, process settings, and metrics. The slice wrote a stub with an earlier list. |  | slice; seams v2 |
| `components/gateway/checkpoint-barrier.md` | component | GWBARRIER | The checkpoint barrier the gateway fans out to pods during a rolling update. | 1,100 | map |
| `components/gateway/drain.md` | component | GWDRAIN | The preStop drain of a gateway replica: its stages, deadlines, and terminal pipelines at process exit. | 5,900 | map |
| `components/gateway/metering-proxy.md` | component | GWLLM | The metering proxy seam (§4.1 LLM Proxy): its listener for LLM upstream calls, routes, limits, concurrency and breaker settings, metrics, and extraction thresholds. | 1,900 | map; seams v2 |
| `components/gateway/ops-endpoints.md` | component | GWOPS | The gateway-side operability parts: the health service, capacity recommendations, the event buffer, and the in-process alert tracker. | 600 | map |
| `components/gateway/pod-claimer.md` | component | GWCLAIM | The gateway's pod claimer: candidate selection, claim creation, and the Postgres fallback path. | 600 | slice |
| `components/gateway/platform-tools.md` | component | GWTOOLS | The platform tools seam (§4.1 MCP Fabric): platform tool calls from pods, delegation orchestration, and the elicitation chain, with its concurrency and breaker settings, metrics, and extraction thresholds. | 700 | seams v2 |
| `components/gateway/scaling-and-capacity.md` | component | GWSCALE | Gateway autoscaling: HPA and KEDA metric roles, the per-replica session budget, the minimum-replica formula, and the process-wide GC pressure signal. Each seam's extraction thresholds are on the seam's page. | 2,900 | map; seams v2 |
| `components/gateway/session-coordination.md` | component | GWCOORD | How gateway replicas coordinate a session: the coordination lease, handoff, hold state, stale replicas, and behavior when both stores fail. | 4,400 | map |
| `components/gateway/stream-proxy.md` | component | GWSTREAM | The stream proxy seam: session attachment, event relay, and reconnection for SSE readers, with its concurrency and breaker settings, metrics, and extraction thresholds. | 600 | seams v2 |
| `components/gateway/upload-handler.md` | component | GWUPLOAD | The upload handler seam: upload proxying, validation, and staging, with its concurrency and breaker settings, metrics, and extraction thresholds. | 500 | seams v2 |
| `components/git-mirror.md` | component | GITMIR | The git mirror (lenny-git-mirror): storage, fetch scheduling, holds, deployment, and metrics. | 2,000 | map |
| `components/lenny-ops/index.md` | index | LOPS | The lenny-ops Deployment: purpose, Helm values, Kubernetes resources, RBAC, NetworkPolicy, leader election, startup, self-monitoring, and TLS. |  | map |
| `components/lenny-ops/backup-jobs.md` | component | BKJOB | The lenny-backup Jobs that lenny-ops creates: pod specification, ServiceAccount, NetworkPolicy, and bucket policy. | 1,100 | map |
| `components/lenny-ops/gateway-fan-out.md` | component | LOPSAGG | How lenny-ops calls every gateway replica, sources metrics with and without Prometheus, and aggregates health and recommendations. | 1,500 | map |
| `components/lenny-ops/hosted-services.md` | component | LOPSSVC | The internal parts of the services lenny-ops hosts: storage tiers, implementation notes, and metrics for events, diagnostics, locks, escalations, audit queries, drift, backups, and upgrades. | 5,500 | map |
| `components/lenny-ops/kubernetes-resources.md` | component | LOPSK8S | The Kubernetes resources the chart renders for lenny-ops: Helm values, Services, ServiceAccount and RBAC, NetworkPolicies, and the disruption budget. | 3,800 | map |
| `components/outbound-agent.md` | component | OUTAGT | The outbound agent a tenant runs on its own network: packaging, keys, and attestations. | 600 | map |
| `components/outbound-gateway.md` | component | OUTGW | The outbound gateway Deployment that tenant agents dial, its replica groups, and metrics. | 1,000 | map |
| `components/pool-scaling-controller.md` | component | PSC | The PoolScalingController: reconciliation from Postgres, scaling computation, leader election, and metrics. | 2,600 | slice |
| `components/preflight-job.md` | component | PREFLT | The lenny-preflight Job and the checks it runs, each linked to the requirement it verifies. | 3,600 | map |
| `components/token-service.md` | component | TOKSVC | The Token Service Deployment: its KMS access, secret reads, signing, upgrade and key rotation, and metrics. | 2,700 | map |
| `components/warm-pool-controller.md` | component | WPC | The WarmPoolController: pod creation and status, leader election, failover, API and etcd load, and metrics. | 6,400 | slice |
| `components/web-playground.md` | component | PLAY | The web playground: placement and gating, cookie session handling, UI surface, asset serving and CSP, cleanup, and metrics. | 3,800 | map |

## Interfaces

Each interface is either one page or a folder whose index states the conventions of that interface.

| Path | Type | Code | Summary | Est. words | Written by |
| :-- | :-- | :-- | :-- | --: | :-- |
| `interfaces/index.md` | index | IFS | Lists the interfaces, the parties on each side, and the page that owns each. |  | map |
| `interfaces/adapter-manifest.md` | interface | ADMAN | The adapter manifest a runtime reads at startup, with every field. | 1,100 | map |
| `interfaces/admin-api/index.md` | index | ADMIN | The admin REST API under /v1/admin: the conventions its operation pages rely on and the list of operation pages. |  | slice |
| `interfaces/admin-api/billing.md` | interface | ADMBILL | Rate card, budget, billing correction, and usage operations. | 300 | map |
| `interfaces/admin-api/compliance.md` | interface | ADMCOMP | Legal hold, erasure job, residency, and tenant deletion operations. | 500 | map |
| `interfaces/admin-api/credentials-and-upstreams.md` | interface | ADMCRED | Credential pool, LLM upstream, and revocation operations. | 700 | map |
| `interfaces/admin-api/identity-and-access.md` | interface | ADMIAM | User, service account, role, and token operations. | 900 | map |
| `interfaces/admin-api/policies.md` | interface | ADMPOL | Delegation policy, interceptor, quota, and circuit breaker operations. | 1,500 | map |
| `interfaces/admin-api/pools.md` | interface | APOOLS | Pool operations. | 1,200 | slice |
| `interfaces/admin-api/runtimes.md` | interface | ADMRT | Runtime, revision, route, and resource class operations. | 2,000 | map |
| `interfaces/admin-api/tenants-and-environments.md` | interface | ADMTEN | Tenant, tenant setting, environment, and outbound connector operations. | 1,600 | map |
| `interfaces/channels/index.md` | index | CHAN | The gateway-to-pod channels and registers: classes, axes, the contract card format, the exclusivity model, and the wire-contract artifacts. |  | map |
| `interfaces/channels/control-plane.md` | interface | CHCTRL | The control-plane channel for drain-readiness admission. | 900 | map |
| `interfaces/channels/gateway-to-pod.md` | interface | CHGWPOD | The gateway-to-pod link and its channels, with the adapter RPC operations and request scope rules. | 5,700 | map |
| `interfaces/channels/gateway-to-store.md` | interface | CHSTORE | The gateway-to-store channels and the registers that mediate replicas through Redis, Postgres, and the Kubernetes API. | 2,100 | map |
| `interfaces/channels/inter-replica.md` | interface | CHREPLICA | The channel that forwards off-holder session requests between gateway replicas. | 400 | map |
| `interfaces/channels/intra-pod.md` | interface | CHINTRA | The intra-pod channels between Lenny's own processes, including the supervision channel; the runtime-facing channels point to the runtime protocol. | 2,600 | map |
| `interfaces/channels/pod-egress.md` | interface | CHEGRESS | The pod-egress channels to the metering proxy and the object store. | 1,700 | map |
| `interfaces/channels/pod-to-gateway.md` | interface | CHPODGW | The pod-to-gateway link and the adapter events channel. | 900 | map |
| `interfaces/cli/index.md` | index | CLI | lenny-ctl: global flags, server discovery and routing, output formats, and the command groups. |  | map |
| `interfaces/cli/admin-commands.md` | interface | CLIADM | Runtime, pool, credential, quota, breaker, user, tenant, erasure, policy, and session commands. | 2,300 | map |
| `interfaces/cli/install-and-local.md` | interface | CLIINST | Install, bootstrap, preflight, the installation wizard, the local stack, and image commands. | 2,600 | map |
| `interfaces/cli/ops-commands.md` | interface | CLIOPS | Operability commands: diagnose, events, audit, drift, locks, escalations, backups, upgrades, migrations, and MCP management. | 1,900 | map |
| `interfaces/cli/runtime-scaffolding.md` | interface | CLISCAF | The runtime scaffolding commands. | 900 | map |
| `interfaces/client-api/index.md` | index | CAPI | The client REST API: authentication, versioning, and the resource groups, with links to the shared API conventions and error model. |  | map |
| `interfaces/client-api/credentials-and-links.md` | interface | CAPICRED | User credential and account link operations. | 300 | map |
| `interfaces/client-api/discovery-and-usage.md` | interface | CAPIDISC | Runtime discovery, published metadata, and usage reporting. | 500 | map |
| `interfaces/client-api/event-stream.md` | interface | CAPIEVT | The session event stream endpoint, the event kind registry, resume parameters, and keepalive. | 1,600 | map |
| `interfaces/client-api/files-and-artifacts.md` | interface | CAPIFILE | Upload, artifact, blob, and export operations. | 700 | map |
| `interfaces/client-api/messages.md` | interface | CAPIMSG | Message operations: send, retract, list, respond to input requests, and client tool results. | 1,800 | map |
| `interfaces/client-api/sessions.md` | interface | CAPISES | Session operations: create, start, get, list, interrupt, terminate, delete, derive, and the states and errors each returns. | 3,100 | map |
| `interfaces/deployment-configuration/index.md` | index | DCFG | The deployment configuration: the Helm values structure, the values schema, chart-rendered resources, and feature gates. |  | map |
| `interfaces/deployment-configuration/answer-files.md` | interface | DCFGANS | Answer files: composition dimensions and the catalog of answer files. | 900 | map |
| `interfaces/deployment-configuration/bootstrap-seed.md` | interface | DCFGSEED | The bootstrap seed format that installs runtimes, pools, and tenants before the admin API is reachable. | 1,000 | map |
| `interfaces/deployment-configuration/tier-presets.md` | interface | DCFGTIER | The tier preset files and the values each tier sets. | 3,300 | map |
| `interfaces/events/index.md` | index | EVT | The CloudEvents envelope and the transports that carry Lenny's events. |  | map |
| `interfaces/events/audit-records.md` | interface | EVTAUD | The audit record schema and the audit event types. | 3,500 | map |
| `interfaces/events/billing-events.md` | interface | EVTBILL | The billing event types, their schemas, and the null-field contract. | 1,900 | map |
| `interfaces/events/operational-events.md` | interface | EVTOPS | The operational event types and their data schemas. | 900 | map |
| `interfaces/events/session-webhooks.md` | interface | EVTHOOK | The session callback webhooks and their signing. | 400 | map |
| `interfaces/extension-points.md` | interface | EXT | The Go extension points a deployer can replace: storage backends, the pool manager, and the cluster registry. | 2,100 | map |
| `interfaces/gateway-seams.md` | interface | GWSEAM | The boundary between each gateway seam and the session core: the operations each side calls, so that extracting a seam puts a transport under an existing interface. New content; the current specification defines the seams' isolation and not their boundary. | 1,000 | seams v2 |
| `interfaces/interceptor-protocol.md` | interface | INTCPTIF | The protocol between the gateway and a request interceptor: phases, payloads, verdicts, and authentication. | 2,100 | map |
| `interfaces/kubernetes/index.md` | index | K8S | Lenny's Kubernetes API conventions: the API group and version, the shipped CRDs and their schema version, field ownership, validation, and how an admission refusal reaches a client. |  | slice |
| `interfaces/kubernetes/admission.md` | interface | KADMIT | The writes Lenny's admission webhooks and policies refuse, with the refusal each returns. | 1,600 | map |
| `interfaces/kubernetes/agentpod.md` | interface | KAPOD | The AgentPod kind. | 1,300 | slice |
| `interfaces/kubernetes/labels-and-annotations.md` | interface | KLABEL | Every label and annotation key, with its writer and readers. | new | slice |
| `interfaces/kubernetes/podclaim.md` | interface | KPCLAIM | The PodClaim kind. | 1,100 | slice |
| `interfaces/kubernetes/podset.md` | interface | KPSET | The PodSet kind. | 1,300 | slice |
| `interfaces/kubernetes/runtime.md` | interface | KRUNTIME | The Runtime kind. | new | map |
| `interfaces/ops-api/index.md` | index | OPSAPI | The ops API served by the gateway and lenny-ops: terminology, envelopes, pagination, rate limits, and the operation groups. |  | map |
| `interfaces/ops-api/audit-and-drift.md` | interface | OPSAUD | Audit query and configuration drift operations. | 800 | map |
| `interfaces/ops-api/backup-and-upgrades.md` | interface | OPSLIFE | Backup, restore, version, upgrade, and configuration apply operations. | 1,800 | map |
| `interfaces/ops-api/caller-identity.md` | interface | OPSME | The caller identity and capability discovery operations. | 500 | map |
| `interfaces/ops-api/events-and-webhooks.md` | interface | OPSEVT | Operational event stream, polling, buffer, and webhook subscription operations. | 700 | map |
| `interfaces/ops-api/health-and-diagnostics.md` | interface | OPSHEALTH | Health, recommendation, diagnostic, and runbook operations. | 1,000 | map |
| `interfaces/ops-api/mcp-management.md` | interface | OPSMCP | The management MCP server of lenny-ops: tools, schemas, capability negotiation, and headers. | 3,400 | map |
| `interfaces/ops-api/remediation-and-operations.md` | interface | OPSREM | Remediation lock, escalation, and operations inventory operations. | 1,800 | map |
| `interfaces/runtime-protocol/index.md` | index | RTP | The runtime protocol between the adapter and a runtime: transports, capabilities in place of integration levels, versioning, and the frame families. |  | map |
| `interfaces/runtime-protocol/conformance.md` | interface | RTPCONF | The conformance suite by capability, how it is packaged, and how failures are categorized. | 1,300 | map |
| `interfaces/runtime-protocol/filesystem-and-environment.md` | interface | RTPFS | The paths, environment variables, and injected configuration a runtime sees in its slot. | 2,900 | map |
| `interfaces/runtime-protocol/handshake-and-capabilities.md` | interface | RTPHAND | The handshake frames, the capabilities a runtime declares, and their dependencies. | 2,700 | map |
| `interfaces/runtime-protocol/message-parts.md` | interface | RTPPART | The MessagePart format, the message envelope fields, binary input and output, and the mapping from MCP content blocks. | 3,900 | map |
| `interfaces/runtime-protocol/messages.md` | interface | RTPMSG | The message socket frames, the MessagePart format, tool and frame names, exit codes, and leniency rules. | 5,200 | map |
| `interfaces/runtime-protocol/platform-tools.md` | interface | RTPTOOL | The platform tools a runtime calls, through frames or the reference MCP bridge, with schemas including the delegation tools and the task result. | 5,200 | map |
| `interfaces/runtime-protocol/runtime-ops.md` | interface | RTPOPS | The runtime-ops channel frames: quiesce, interrupt acknowledgement, and the remaining rotation frames. | 2,200 | map |
| `interfaces/runtime-protocol/runtime-sdks.md` | interface | RTPSDK | The runtime author SDKs: packages, what each adds to the protocol, and what each leaves to the runtime. | 2,600 | map |
| `interfaces/stored-schemas/index.md` | index | STOR | The stored schemas that must stay compatible across versions, and the compatibility rules for each store. |  | map |
| `interfaces/stored-schemas/object-store-layout.md` | interface | STOROBJ | The object key layout and the checkpoint pack format. | 400 | map |
| `interfaces/stored-schemas/postgres.md` | interface | STORPG | The Postgres tables other versions read: sessions, tasks, pod state, billing, audit, and operations tables. | 2,600 | map |
| `interfaces/stored-schemas/redis-keys.md` | interface | STORREDIS | The Redis key patterns and their tenant prefixing. | 900 | map |
| `interfaces/workspace-plan.md` | interface | WSPLANIF | The WorkspacePlan schema with every field, and its versioning. | 3,200 | map |

## Cross-cutting

| Path | Type | Code | Summary | Est. words | Written by |
| :-- | :-- | :-- | :-- | --: | :-- |
| `cross-cutting/index.md` | index | XC | Lists the properties every capability and component upholds, with the pages that carry each. |  | map |
| `cross-cutting/api-conventions.md` | cross-cutting | APICONV | Conventions shared by the client, admin, and ops APIs: idempotency, ETags, pagination, dry-run, correlation headers, and rate-limit headers. | 3,300 | map |
| `cross-cutting/availability-and-degradation.md` | cross-cutting | AVAIL | Availability principles, fail-open and fail-closed rules, dependency outage behavior, and total-outage recovery. | 3,100 | map |
| `cross-cutting/data-classification-and-retention.md` | cross-cutting | DATACLASS | Data classes, the controls each class requires, encryption at rest, and retention defaults. | 3,300 | map |
| `cross-cutting/error-model.md` | cross-cutting | ERR | The error envelope, the error codes shared across interfaces, validation errors, the degradation envelope, and the refusal of removed fields. | 3,800 | map |
| `cross-cutting/mtls-and-pki.md` | cross-cutting | PKI | The certificate authority, certificate lifecycle, and the peer validation every mTLS link performs. | 3,100 | map |
| `cross-cutting/naming.md` | cross-cutting | NAMING | The channel naming law, resource name rules, and the naming table of retired spellings. | 1,000 | map |
| `cross-cutting/observability-conventions.md` | cross-cutting | OBSCONV | Metric and attribute naming, labels, log format, trace propagation, and the single source of alert rules. | 2,500 | map |
| `cross-cutting/security-model.md` | cross-cutting | SECMODEL | Trust boundaries, the threat model, the adapter-to-runtime boundary, and image supply chain controls. | 3,300 | map |
| `cross-cutting/tenant-isolation.md` | cross-cutting | TENISO | The tenant isolation property: tenant identifiers on every record, row-level security, key and object prefixes, and the query rules that keep tenants apart. | 2,400 | map |
| `cross-cutting/upgrade-and-compatibility.md` | cross-cutting | COMPAT | The compatibility policy: API versioning, expand-contract migrations, CRD versioning, wire-contract versions, and version skew. | 2,800 | map |

## Catalogs

The slice owns `catalogs/`. The paths below follow the catalog list of `TAXONOMY.md`; the slice's files decide the final names. The last two rows are catalogs this map proposes.

| Path | Type | Code | Summary | Est. words | Written by |
| :-- | :-- | :-- | :-- | --: | :-- |
| `catalogs/index.md` | index | CAT-INDEX | Lists the generated catalogs. |  | slice |
| `catalogs/alerts.md` | catalog | CAT-ALERTS | Every alert with the requirement it detects a breach of. |  | slice |
| `catalogs/api-operations.md` | catalog | CAT-OPS | Every API operation across the client, admin, and ops APIs. |  | slice |
| `catalogs/channels.md` | catalog | CAT-CHANNELS | Proposed: every link, channel, and register with its endpoints and degradation, generated from the channel pages. |  | proposed to slice |
| `catalogs/custom-resources.md` | catalog | CAT-CRDS | Every custom resource kind. |  | slice |
| `catalogs/errors.md` | catalog | CAT-ERRORS | Every error code with its interface and owner. |  | slice |
| `catalogs/events.md` | catalog | CAT-EVENTS | Every event type with its carrying interface. |  | slice |
| `catalogs/glossary.md` | catalog | CAT-GLOSSARY | Every owned term with its owner page. |  | slice |
| `catalogs/labels.md` | catalog | CAT-LABELS | Every label and annotation key. |  | slice |
| `catalogs/limits.md` | catalog | CAT-LIMITS | Every limit with its value and owner. |  | slice |
| `catalogs/metrics.md` | catalog | CAT-METRICS | Every metric with its emitting component. |  | slice |
| `catalogs/requirements.md` | catalog | CAT-REQS | Every requirement identifier with its page. |  | slice |
| `catalogs/settings.md` | catalog | CAT-SETTINGS | Every setting with its default, range, and owner. |  | slice |
| `catalogs/state-machines.md` | catalog | CAT-STATES | Proposed: every state machine, generated from the model blocks of the owning capabilities. |  | proposed to slice |

## Conventions

| Path | Type | Code | Summary | Est. words | Written by |
| :-- | :-- | :-- | :-- | --: | :-- |
| `README.md` | conventions | README | Root map: what Lenny is in one paragraph, the capability areas, and reading paths by task. |  | map |
| `conventions.md` | conventions | CONV | Page types, the placement procedure, normative language, the identifier scheme, the gloss rule, and the definition-block format. |  | map |

## What leaves the specification

Each row names a destination outside the specification, what goes there, the current sections it comes from, and the words of current specification text it takes. Design words are not counted here.

| Destination | What goes there | Current sections | Spec words |
| :-- | :-- | :-- | --: |
| `BUILD-PLAN.md` (and `BUILD-GAPS.md` for open claims) | The phased build sequence, phase exit criteria, the hard prerequisite chain, roll-forward notes, and the rows of the channel claim register that record absent behavior. | §18 Build Sequence; §27.10 Roll-forward notes; part of §28.4 Claim register | 10,640 |
| the archive of designs and proposals (decision records) | The resolved-decisions table, which the designs and proposals already record as decision records, and the design sections that record history, alternatives, measurements, and validation. | §19 Resolved Decisions; the history, alternatives, benchmarks, and review sections of every design | 690 |
| `ROADMAP.md` | Planned post-v1 work. | §21 Planned / Post-V1 | 1,960 |
| the issue tracker | Open questions. | §20 Open Questions | 10 |
| `docs/about/` | The competitive landscape, comparison matrices, adoption strategy, and the "why Lenny" text. | §23 Competitive Landscape | 3,870 |
| `docs/operator-guide/` | Sizing guidance, worked examples, the tier promotion guide, operator decision checklists, restore workflow, and the external memory pattern. | §17.8.2 (part), §17.8.3, §12.3 and §12.4 (sizing parts), §25.4 Multi-Replica Lenny-Ops Scaling (part), §25.11 Restore Workflow (part), §9.4 (part), §5.1 Worked Examples (part) | 6,070 |
| `docs/runbooks/` | Runbook bodies and step-by-step procedures. The runbooks already exist under `docs/runbooks/`; the specification keeps only the runbook format and discovery. | §17.7 Operational Runbooks; §25.15 Total-Outage Recovery Runbook; §4.9.1 KMS Key Rotation Procedure (part); the example runbook inside §25.7 | 6,640 |
| `docs/runtime-author-guide/` | The sample echo runtime, the runtime author roadmap, integration guidance by level, and plugging in a custom runtime. | §15.4.4, §15.4.5, §15.4.3 (part), §17.4 Plugging in a custom runtime | 3,280 |
| `docs/client-guide/` | The client SDK list. | §15.6 Client SDKs | 180 |
| the reference runtime repositories and `docs/runtime-author-guide/` | Each reference runtime's description, its image, and its configuration. Runtime categories leave the specification. | §26 Reference Runtime Catalog; §5.1 First-Party Reference Runtimes | 3,780 |
| the client protocol adapter repositories (`lenny-adapter-*`) | Every client protocol other than REST: the MCP client API, OpenAI Completions and Responses, Anthropic Messages, A2A, the translation tables, and protocol version negotiation. | §15 introduction, §15.2 MCP API, §15.2.1, the protocol columns of the Translation Fidelity Matrix, §9.1 (part), §9.2 (MCP forms) | 5,280 |
| removed by a design (archive only) | Mechanisms a design removes: experiments and evaluations, connectors, the memory store, the semantic cache, LLM credential leasing and direct mode, the adapter framework, `type: mcp` runtimes, service mode, the embedded adapter model, ADR labels, and the agent-sandbox dependency text. | §10.7; §9.3; §9.4; §4.9 (most); §15 Shared Adapter Types; parts of §5.1, §5.2, §4.7.10, §13.3, and many others | 32,370 |
| Go package documentation | Go signatures and package lists that are implementation rather than contract. | §4.0 package list; the Go Interface subsections of §25; §12.6 (part) | 2,770 |
| `TESTING.md` | Benchmark and calibration methodologies, smoke tests, CI contracts, and test-requirement statements. | §4.1 calibration methodology; §4.1 Gateway Internal Subsystems calibration; §6.3 (part); §15.4.6 (part); §17.4 Smoke test | 1,980 |
| `CONTRIBUTING.md` and contributor docs | Contributor procedures: source mode, runbook authoring guidelines, and adding a reference runtime. | §17.4 Source Mode (part); §25.7 Runbook Authoring Guidelines (part); §26.12 | 310 |

Of 490,317 words of current specification text with an assignment, about 79,800 (16%) leave the specification under the rows above, and about 32,400 of those are removals by designs.

