# Findings from mapping the whole specification

Status: spike output, 2026-11-16. This file records what assigning every section of the current specification, every design, and every reference page to the proposed tree showed about version 0 of `TAXONOMY.md`. The vertical slice records its own findings in `FINDINGS-slice.md`. The tree is in `proposed/MAP.md`, the assignment in `proposed/ASSIGNMENT.md`, and the root map in `proposed/README.md`. The scripts that generated the map and the assignment are in `tools/` (`build.py`, `gen.py`, and `gen_index.py`, run in that order over `pages.tsv`, `specmap.txt`, and `designmap.txt`).

## Results

| Measure | Value |
| :-- | :-- |
| Pages in the proposed tree | 274: 15 overview (including 10 walkthroughs), 103 capability, 35 component, 60 interface, 10 cross-cutting, 13 catalog, 2 conventions, and 36 index pages |
| Current specification headings | 696 at levels 1 to 4 (the other 64 lines that `grep "^#"` returns are comments inside code fences) |
| Headings assigned | 696: 553 by an explicit row, 96 by the §25 subsection rule, 47 by a whole-file rule; none unassigned |
| Designs assigned | 36 of 36, by their numbered sections; 461,000 of their 1,110,000 words sit in sections that land or move, and the rest is history, review, tests, and evidence for the archive |
| Reference pages assigned | 10 of 10 under `docs/reference/` |
| Current text that leaves the specification | about 80,000 of 490,000 words (16%), of which about 32,000 are mechanisms that designs remove |
| Content pages estimated over 6,000 words | 7 after the splits the map already makes; 14 more between 5,000 and 6,000 |

## Changes to the starting lists

**Capability areas.** The map uses thirteen areas instead of nine.

- **Messaging leaves Sessions.** The message delivery design is 73,000 words, §7.2 Interactive Session Model is 9,000 words, and the subject has six capabilities of its own (delivery, inbox and custody, inter-session messages, the event stream, input requests, client tools). Keeping them in Sessions would give that area about twenty pages and an index near the 1,500-word budget.
- **Credentials and identity split into Tenancy and access, and Credentials.** The tenant, environment, and principal objects are used by every area, so they need an area that other areas link to rather than one inside credentials. Credentials alone has five designs (user credentials, service account credentials, user git identity, and the two add-ons).
- **Observability and operations split three ways.** Security and compliance takes the audit log, SIEM export, security oversight, forensics, legal hold, erasure, and residency: §12.8 Compliance Interfaces alone is 15,700 words, and the forensics and security oversight designs add 70,000. Deployment and lifecycle takes installation, local development, platform upgrades, backup and restore, and capacity tiers, which are operator procedures with their own state machines. Observability and operations keeps operational events, health and diagnostics, remediation, runbooks, drift, and alerting.
- **Areas gain capabilities the designs add.** Runtimes and pools gains runtime routes (runtime-routing RT-3 and RT-4 replace §10.7 Experiment Primitives), resource classes (billing-resource-attribution BR-1), and owned pools and reservations (tenant-derived-runtimes DR-5 and DR-10). Workspaces and files gains repository stores and default clones (session-isolation SI-8, tenant-derived-runtimes DR-9 and DR-12, workspace-materialization WM-15). Network and egress gains egress decisions, egress defaults, git access, operator services (connector-removal CR-2 to CR-4), and the outbound connector.

**Components.** §17.1 Kubernetes Resources lists eleven rows, and only some are components. `lenny-gateway-pods`, NetworkPolicies, the Lease, the monitoring resources, and the chart-rendered Secrets are resources that belong to a component's Deployment section. Conversely, the designs add running units that §17.1 does not name: `netagent`, the init containers `lenny-bin-stage` and `lenny-store-fill`, the `keepalive` container, the slot containers running the supervisor, `lenny-egress`, `lenny-egress-authz`, `lenny-git-mirror`, the dedicated CoreDNS instances, `lenny-outbound-gateway`, and the customer-run outbound agent. The `lenny-preflight` Job and the `lenny-backup` Jobs are running units with their own ServiceAccount and NetworkPolicy, so each has a page. KMS and Prometheus join the external dependencies, because §4.3 Token Service and §25.4 Prometheus Requirement place requirements on them. Three headings in §4 that read as components are not running units: §4.2 Session Manager is a logical part of the gateway, §4.4 Event / Checkpoint Store and §4.5 Artifact Store are logical stores over Postgres and the object store, and §4.9 Credential Leasing Service is behavior split between the gateway and the Token Service.

**Interfaces.** The MCP client API leaves (rest-only-client-api RO-8 and RO-11), and the admin, client, and ops APIs are folders by resource group. The map adds the request interceptor protocol (§4.8 `RequestInterceptor` is a contract that an interceptor author builds against), Go extension points (§12.6 and §12.7, for deployers who replace a backend), the runtime SDKs as part of the runtime protocol, a folder of event schemas, stored schemas per store, and deployment configuration with answer files, tier presets, and the bootstrap seed. The management MCP server of `lenny-ops` stays and sits under the ops API.

## Placements the procedure decided badly or ambiguously

1. **Heading-level placement does not work for a third of the text.** 457 of the 669 headings with own text go to one destination, but they hold 25% of the words. 28 headings split five or more ways and hold 35% of the words: §15.1 › Session Replay Semantics (16,300 words), §12.8 Compliance Interfaces (15,700), §16.5 Alerting Rules and SLOs (13,500), §28.5.3 Intra-pod (12,100), §5.2 › Execution Modes (11,900), §16.1 Metrics (11,800), §4.6.1 Warm Pool Controller (9,300), and §7.2 Interactive Session Model (9,000) lead the list. The procedure decides each bold paragraph cleanly, so the migration needs a paragraph-level assignment pass rather than a heading-level one.

2. **Mis-leveled headings hide whole subjects.** "#### Session Replay Semantics" under §15.1 holds the replay endpoint and then, without a heading, the whole admin API endpoint table, the admin API conventions, the error envelope, and the error code catalog. "### Step 1: Check pool status" in §25.7 is a heading inside an embedded example runbook. §16.1.1 and §17.8.1 to §17.8.6 are written one level too high. A reader or agent that navigates by headings cannot find the admin API in the current tree; the procedure only places it after the bold labels are read.

3. **Component pages end up holding rules.** The component-or-capability test asks whether a different implementation would leave the statement true. For the gateway's coordination protocol (§10.1.1 to §10.1.5: the coordination lease, handoff, hold state, stale replicas), the answer is no, so the content goes to the component (GWCOORD). Those sections are normative ("the replica must", "exactly one coordinator"), and the template says a component page states no behavioral rule. The same happens for the adapter's slot registry and bind attempt token (§4.7.1), the supervisor's sweep and file-descriptor broker (§4.7.10, session-isolation SI-5), and the lock storage tiers of `lenny-ops` (§25.4 › Remediation Coordination › Tier Transitions and Split-Brain Prevention). The rules are real and need identifiers that code can cite.

4. **Alerts that detect no capability requirement.** The owners table gives an alert to "the capability whose requirement it detects a breach of". Many alerts in §16.5 detect the health of a unit: `PostgresReplicationLagHigh`, certificate expiry, admission webhook unavailability, controller leader-election churn. No capability requirement covers them, so the map assigns them to the component. The table needs that second row.

5. **Registers that are internal to one component.** §28.3 › Register-entry register lists `REG-COORDLEASE`, `REG-COORDMIRROR`, and `REG-SLOTCOUNT`, whose writer set and reader set are both gateway replicas. Under the interface test ("a party can be built against it without reading the other party") there is no other party, so they are internal parts of the gateway. `REG-CLAIM` (writer: gateway; readers: the WarmPoolController and the claim webhook) and `REG-PODSTATE` (writers: the WarmPoolController and the gateway; reader: the gateway) cross components and are interfaces. The current specification treats all five alike.

6. **Shared API conventions have no owner.** Pagination, ETags, dry-run and confirm, idempotency keys, correlation headers, and rate-limit headers are stated for the admin API inside §15.1 › Session Replay Semantics, for the ops API in §25.2 › API Conventions, and for the client API in §11.5 Idempotency. The owners table names only the error model as cross-cutting. The map adds `cross-cutting/api-conventions.md`.

7. **Per-tier defaults have two owners.** "Limit or default: wherever the setting that carries it is owned" works for one default. §4.1 gives `maxSessionsPerReplica` per tier, §17.8.2 Capacity Tier Reference gives per-tier values for dozens of settings, §25.11 › Tier-Based Retention Defaults gives retention per tier, and §17.8.4 Tier Preset Files ships them as Helm files. A tier default belongs to the setting, and the tier preset file is a generated artifact. The setting definition block needs a per-tier field, and `interfaces/deployment-configuration/tier-presets.md` becomes a generated page.

8. **Go signatures have no rule.** §12.6 Interface Design (5,900 words), five "Go Interface" subsections in §25, §4.6.1 › Internal abstraction layer, and the package list in §4.0 Agent Operability Additions state Go types. Most are implementation, and the procedure has no question that catches them; the map sends them to package documentation unless a deployer can replace the implementation, in which case they are the extension-point interface.

9. **Destinations missing from question 8.** Benchmark and calibration methodologies inside component text (about 30% of §4.1 › Gateway Internal Subsystems, the capacity calibration in §4.1, §6.3 Startup Latency Analysis) go to `TESTING.md`. Sizing guidance and worked examples go to `docs/operator-guide/`. Runbook bodies go to `docs/runbooks/`, where they already exist. Client protocol mappings go to the adapter repositories under rest-only-client-api. None of these four destinations is in the procedure.

10. **An interface hidden in a schema field.** The session callback webhook (`callbackUrl`: CloudEvents delivery, signing, retry, the domain allowlist) is defined in the description of one field in §14 Workspace Plan Schema. The owners table puts an event type on "the interface that carries it", which is a separate events page (EVTHOOK). The procedure reaches the right answer only because the reader notices that a field description defines a whole delivery contract.

11. **Interface fields that carry settings are stated three times.** The slice's pages show it for `minWarm`: the admin API pool page states the error for `minWarm` above `maxWarm`, the `PodSet` page states the constraint `<= maxWarm` and lists the admission refusal, and the warm pools page owns the setting. Each statement links the next, as the taxonomy asks, yet the constraint itself is written three times. A rule that an interface field carrying a setting states its wire name and type and links the setting block for its range would reduce this to one.

12. **Domain object diagrams that mix objects.** §6.2 Pod State Machine draws the pod states and, in the same diagram and text, the session sub-states `input_required`, `suspended`, `resuming`, and `resume_pending`, plus timer behavior for every state. Placement by object splits it five ways (APOD, SESS, SUSP, RESUME, TIMEOUT). The procedure decided correctly; the finding is that the current specification's section titles are not a reliable guide to which object a section governs.

13. **Settings with scopes.** Every recent design has a "Configuration by scope" section (egress-and-git, forensics, security-oversight, tenant-derived-runtimes, tenant-scoping-identity, the credential designs): one setting with a platform default, a tenant override, and a pool or runtime override. The behavioral-setting rule places it on the capability, which is right, but the definition block has no field for its scopes, so each page would invent its own table.

14. **Code collisions.** The map first coded session derive as DERIVE beside the slice's DERIVED for tenant-derived runtimes, and both halves wanted K8S (the slice for `interfaces/kubernetes/index.md`, the map for the external Kubernetes dependency, now KUBE). The slice also uses hyphenated catalog codes (`CAT-ALERTS`), which collide with the `CODE-R1` requirement syntax. Codes need a registry and a format.

15. **Index pages are a type nobody defined.** The slice and the map both wrote `type: index` in front matter, because the TAXONOMY's type list has none for a folder index.

## Answers to the open questions

**1. Shared domain objects.** One owning capability holds each object, and no separate model type is needed. The session is the hardest case: its rules spread over §6.2, §7.1, §7.2, §7.3, §11.3, and §15.1 today. In the map the session object, its state machine, and its external state mapping live on one page (`sessions/session-lifecycle/session-states.md`), and creation, suspension, the end of a session, and timeouts are sibling pages in the same folder that link the states they move between. The tenant fits the same way (`tenancy-and-access/tenants.md`, with settings on a sibling page), as do the runtime (RUNTIME), the pod (APOD), and the delegation task (DLG). The rule that made this work is that when a capability becomes a folder, the object and its state machine stay on one page named for the object. What the tree does need is a generated state-machine catalog (CAT-STATES), because `docs/reference/state-machines.md` exists for readers who want every machine in one place.

**2. End-to-end scenarios.** A non-normative walkthrough folder under the overview. §29 is 16,900 words across nine scenarios, and §25.17 is a tenth. The starting capability would be the wrong home: §29.2 Session start crosses session creation, pod claims, materialization, and the gateway-to-pod channel, and adding its 2,500 words to SCREATE would double that page. A walkthrough must state no rule, and §29 restates rules today (for example §29.10 restates slot-assignment rules). A gate that refuses normative keywords in `overview/walkthroughs/` holds the line.

**3. Metric ownership.** The emitting component. The evidence is that §16.8 lists 65 metrics, 57 of which §25 also defines in the per-service "Metrics" subsections beside the service that emits them, and `docs/reference/metrics.md` already records an emitting component per metric. The capability owns the alert and the SLO and links the metric. 459 distinct metric names appear in the specification, and 71 of them appear in three or more files today; a single owner per metric is what stops that.

**4. The gateway.** One component, as a folder: the index page covers the gateway as a unit (Deployment, replicas, disruption budget, probes, RBAC, NetworkPolicy), and sub-pages cover internal parts that each have their own failure and scaling story (session coordination, drain, checkpoint barrier, scaling and capacity, the pod claimer, the metering proxy, the ops endpoints). Separate component pages would misstate the deployment: §4.1 › Gateway Internal Subsystems says the subsystems are Go interfaces in one binary. The four subsystems §4.1 names (Stream Proxy, Upload Handler, MCP Fabric, LLM Proxy) no longer match the designs: rest-only-client-api removes the MCP surface, workspace-materialization WM-9 moves archive extraction out of the gateway, and llm-upstreams LU-3 replaces the LLM proxy with a metering proxy. The slice's stub of `components/gateway/index.md` lists ten subsystems that differ from the map's sub-pages; the subsystem list is a decision the owner still has to make.

**5. The agent pod.** One folder: an index page for the pod as a unit and one sub-page per container with a distinct process. agent-pod-layout PL-1 gives each container its own owner design (the adapter, the slot containers, `netagent`, the init containers, `keepalive`), and each has its own UID, mounts, settings, and failure behavior. The pod-wide facts (start order, volumes and size limits, security context, Pod Security Standards, project quotas) apply to all containers and stay on the pod pages. The slot containers share one page with the supervisor, because every slot container runs the supervisor as PID 1 and nothing else from Lenny.

**6. Reference runtimes and the web playground.** §26 Reference Runtime Catalog leaves the specification for the reference runtime repositories and the runtime author guide. Nothing Lenny does depends on which reference runtime runs; their contract with Lenny is the runtime protocol, whose conformance suite stays. §26.2 also defines runtime categories, which the owner has ruled out of the specification. §27 Web Playground becomes a component (PLAY), because it is a separately gated process with placement, CSP, asset serving, cleanup, and metrics. Its OIDC cookie-to-token exchange (§27.3.1, 3,200 words) is authentication behavior and goes to a capability (`tenancy-and-access/browser-sessions.md`); its MCP bearer and WebSocket wording leave under rest-only-client-api RO-12.

**7. Page size (data for the slice).** At the time of writing, the slice's pages total 72% of the map's estimates for the same pages (25,400 words against 35,200). Capability pages came out at 0.6 to 1.3 times the estimate, component pages at 0.4 times, because a component sheds the rules its sources restated, and pages whose content is scattered today (the pod claimer, labels and annotations) came out larger than the estimate. Warm pools is the largest at 5,400 words, against an estimate of 8,600. Read the map's estimates over 6,000 words with that ratio in mind.

## Facts stated in several places today

| Fact | Where it is stated | Owner in the map |
| :-- | :-- | :-- |
| The session state machine | §6.2 Pod State Machine (session sub-states), §7.2 › Session state machine, §15.1 › Externally visible vs. internal-only states, `docs/reference/state-machines.md`, session-lifecycle SL-1 and SL-2 | SESS |
| The session age limit | §5.1 example as `maxSessionAge: 7200`, §5.2 as `maxSessionAgeSeconds: 7200`, §7.3 retry policy example, §6.2 timer table, §11.3 Timeouts and Cancellation; 13 files mention it, under two names | TIMEOUT |
| Metric definitions | §16.1 Metrics, §16.8 Section 25 Metrics, the per-service Metrics subsections of §25, the HPA metric roles in §4.1, the first-week list in §17.8.2, `docs/reference/metrics.md` | Each emitting component |
| The `WarmPoolLow` alert | §16.5 warning alerts table, the example runbook in §25.7, and references in §4.6.1, §17.7, and three paragraphs of §17.8.2 | POOL |
| Error codes | §15.1 error code catalog (about 330 rows), the Error Codes subsections of §25, `docs/reference/error-catalog.md`; `CREDENTIAL_POOL_EXHAUSTED` appears in 9 files | Each interface, ERR for shared codes |
| Audit events | §4.9.2 Credential Audit Events, §11.7 Audit Logging, §16.7 Section 25 Audit Events, the Audit Events subsections of §25 | EVTAUD |
| Runtime protocol frames | §15.4.1 and › `MessageEnvelope`, §28.5.3 Intra-pod › Message schemas, §4.7.4, `docs/reference/adapter-contract.md` (9,500 words) | RTPMSG, RTPPART |
| Per-channel degradation | Each contract card's Degradation block in §28.5, and §28.8 Failure and degradation matrix (4,100 words) | Each channel page; CAT-CHANNELS generated |
| Channel endpoints and axes | §28.3 › Channel register and the Link, Endpoint, and Axes blocks of every card in §28.5 | The channel pages |
| Operational defaults | §17.8.1 Operational Defaults — Quick Reference, and each section that owns the setting | The setting's owner; CAT-SETTINGS generated |
| Per-tier capacity values | §4.1 per-replica budget table, §16.5 › Capacity tiers, §17.8.2 Capacity Tier Reference, §25.4 › Recommended Scale by Tier | The setting's owner, with per-tier defaults |
| Admission webhook inventory | §17.2 admission policy manifests and the feature-gated chart inventory (which calls itself the single source of truth), §13.1 and §13.2, §16.5 alerts, §4.6.1 | WEBHOOK and KADMIT |
| Backend requirements | §12.3, §12.4, and §12.5, and again §17.9.3, §17.9.5, and §17.9.7 Backend-Invariant Requirements | PG, REDIS, OBJ |
| `lenny-system` NetworkPolicies | §13.2.4, §25.4 › NetworkPolicy, and the NetworkPolicies row of §17.1 | Each component's Deployment section; NETISO for the baseline |
| The operability summary | §4.0 Agent Operability Additions summarizes §25 | LOPS and OPSEV |
| Runbooks | §17.7 Operational Runbooks, §25.7 Operational Runbooks, and `docs/runbooks/` | `docs/runbooks/`; RUNBOOK for the format |

## Size distribution

| Estimated words | Pages |
| :-- | --: |
| under 500 | 16 |
| 500 to 1,000 | 26 |
| 1,000 to 2,000 | 51 |
| 2,000 to 3,000 | 46 |
| 3,000 to 4,000 | 39 |
| 4,000 to 5,000 | 24 |
| 5,000 to 6,000 | 14 |
| over 6,000 | 7 |

The median content page is about 2,400 words. The pages over 6,000 are warm pools (8,600, slice), default clones (6,600), egress policy (6,500), the WarmPoolController (6,400, slice), checkpoint capture (6,300), the agent pod lifecycle (6,100, slice), and credential delivery (6,100). `ASSIGNMENT.md` gives a split for each. Before the map split them, `lenny-ops` was estimated at 13,800 words, checkpoint capture at 10,600, the gateway at 9,400, and the runtime message frames at 9,100. The smallest pages (the Runtime kind, the purpose page, the capability map) are small because their content is new rather than moved.

## Proposed changes to the taxonomy

1. **Add a walkthrough type** under `overview/walkthroughs/`, non-normative, with a gate that refuses normative keywords. (Open question 2; finding on §29.)
2. **Let a component own internal requirements** about a mechanism that only that implementation has (coordination protocols, registries, brokers), numbered with the component's code, so code cites them like any other requirement. A rule that a different implementation of the same responsibilities would also have to meet stays on the capability. (Finding 3.)
3. **Add an owner row for unit-health alerts:** an alert that detects the health of a running unit rather than a breach of a capability requirement belongs to the component. (Finding 4.)
4. **Add an owner row for registers:** a register whose writer and reader sets are the same component is an internal part of that component; a register shared by two components is an interface. (Finding 5.)
5. **Add an owner row for conventions shared by several interfaces** (`cross-cutting/api-conventions.md`), beside the error model. (Finding 6.)
6. **Extend the setting definition block** with `scopes` (platform, tenant, pool, runtime) and `defaults-by-tier`, and treat tier preset files as generated from it. (Findings 7 and 13.)
7. **Add a rule for Go signatures:** they leave for package documentation, except an extension point a deployer can replace, which is an interface page. (Finding 8.)
8. **Add destinations to question 8:** `TESTING.md` for benchmarks, calibration, and test requirements; `docs/operator-guide/` for sizing guidance and worked examples; `docs/runbooks/` for procedures; the adapter repositories for client protocol mappings; contributor documentation for contributor procedures. (Finding 9.)
9. **Add a rule for interface fields that carry a setting:** the interface states the wire name, the type, and the errors, and links the setting for its meaning and range. (Finding 11.)
10. **Add a rule for capability folders:** when a capability splits into a folder, the domain object and its state machine stay on one page named for the object. (Open question 1.)
11. **Add a Limits section to the capability template.** Every design has a "Limits" or "Residual risks" section, and the template has nowhere to put what a capability does not guarantee.
12. **State that a capability's security section includes its threats**, and that the cross-cutting security model holds the system threat model and lists the capabilities with their own threats. Several designs (user credential delivery, user git identity, service account credentials, connector removal) carry a threat model of their own.
13. **Fix the code format and keep a registry:** two to nine uppercase letters with no hyphen, registered in one file before use, with a gate that refuses a duplicate or a code that differs from an existing one by a suffix. (Finding 14.)
14. **Add `index` to the page types**, with the front matter the slice and the map already use. (Finding 15.)
15. **Make paragraph-level assignment the unit of migration.** A heading-level table cannot place 35% of the current text; the migration needs the bold-label pass this map did for the largest sections, done for every section. (Finding 1.)
16. **Add a state-machine catalog and a channel catalog** to the catalog list, generated from model blocks and channel pages, to replace `docs/reference/state-machines.md` and §28.8.

## Method and limits

- Word counts come from an `awk` pass over `spec/*.md` that counts the words under each heading up to its next heading, outside code fences. Design words come from the same pass over each design's level-2 sections.
- Each section's split percentages are judgments made from its bold paragraph labels, which this map read for every section over about 2,000 words. Sections below that size were placed from their heading and first lines. The percentages are good enough for size estimates and too coarse for a migration script.
- Design sections are counted at half weight. The ratio is an assumption; the slice's measured pages suggest the overall ratio of written page to source is lower still.
- The map does not reconcile the gateway subsystem list with the slice's stub of `components/gateway/index.md`, or the catalog file names the slice has not written yet (index, limits, events, custom resources, API operations). `MAP.md` lists both with the slice as owner.

## Reconciliation after both halves finished

Twelve link targets in the slice were neither written nor listed in `MAP.md`. Each was retargeted to the map's page with the same content (52 links in all): `capabilities/billing/resource-classes.md` to `capabilities/runtimes-and-pools/resource-classes.md`; `capabilities/forensics/quarantine.md` to `capabilities/security-and-compliance/quarantine-and-evidence.md`; `capabilities/network/egress.md` to `capabilities/network-and-egress/egress-policy.md`; `capabilities/security/adapter-authentication.md` to `interfaces/channels/gateway-to-pod.md`; `capabilities/security/network-isolation.md` to `capabilities/network-and-egress/network-isolation.md`; `capabilities/security/workload-certificates.md` to `cross-cutting/mtls-and-pki.md`; `capabilities/sessions/checkpoints.md` to `capabilities/sessions/checkpoints/checkpoint-capture.md`; `capabilities/sessions/session-lifecycle.md` to `capabilities/sessions/session-lifecycle/index.md`; `capabilities/sessions/slot-release-and-scrub.md` to `capabilities/sessions/pod-reuse-and-scrub.md`; `capabilities/sessions/slots.md` to `capabilities/sessions/slot-assignment-and-failure.md`; `interfaces/admin-api/bootstrap.md` to `interfaces/deployment-configuration/bootstrap-seed.md`; and `interfaces/channels/registers.md` to `interfaces/channels/gateway-to-store.md`. Anchors inside those targets are the slice's guesses and are checked when the pages are written. The home of the adapter's nonce-only authentication is the least certain of these.
