# Decisions: 0128 Forensics and evidence preservation

## Open decisions

The M3 readiness review of 2026-11-19 raised O2 to O11, its second pass raised O12 and O13 at the request of other changes, and none has an owner answer. The settled O1 moved to "Record of O1" under "Decisions".

**O2. Name of the evidence report frame and SDK call** (raised by the M3 readiness review, 2026-11-19; sources: 0128-F1, tenancy-security-C5). This is a conflict with [`0135-message-delivery`](../0135-message-delivery/change.md), which records it as its O23.

FE-6's **SDK.** bullet has the runtime write an `activity` frame on `CH-MSGSOCK`, or call the SDK's `reportActivity`, which the adapter bounds at 4 KiB per record and sends on `CH-ACTIVITYREPORT` to `lenny-evidence`. MD-15 of 0135 defines a frame of the same name with `op`, `id`, and `kind`, caps it at 1 MiB, relays it to the session's own stream, and adds `ReportActivity`, `report_activity()`, and `reportActivity()` to the SDKs. The adapter cannot route one frame name to both destinations, so whichever of 0128.S7 and 0135.S4 lands second cannot be built as written.

- (a) 0128 renames its frame and SDK call, for example to `activity_report` and `reportToolActivity`, registers the frame under the naming law, and updates FE-6a, the §4.7, §4.7.11, §15.4, §15.7, §28.3, and §28.5 bullets of "Specification sections affected", the `runtime-sdk.md` documentation row, and the `contract` and `conformance` tests.
- (b) 0128's report becomes a reserved `kind` of 0135's `activity` frame, with a stated route to `lenny-evidence` and 0128's bounds. 0135's **Trust.** and **Routing.** rules then change too.
- (c) 0135 renames its frame and SDK calls.

**Recommendation: (a).** 0135's frame is client-visible and broader, and a shared frame would put tenant-visible data and evidence-only data through one parser.

Blocks: 0128.S7 and 0135.S4.

**O3. Operator-access mirror and approval for the actions of a case** (raised by the M3 readiness review, 2026-11-19; sources: 0128-F2, tenancy-security-C3). This is a conflict with [`0142-security-oversight`](../0142-security-oversight/change.md), which records it as its open decision 39. That entry still names the deferral event `gdpr.erasure_deferred_completed`, which this change renamed to `forensics.erasure_deferred_completed`.

FP3 states that a security hold and a quarantine produce no signal in any tenant-readable surface. SO-4a of 0142 excludes only `forensics.*` events from the mirror to the tenant's chain, while its other rows mirror a platform admin's read that names a tenant as `admin_read` with the endpoint, a platform admin's write as `admin_write`, and platform-tenant events that carry `target_tenant_id`. A `platform-admin` may call `GET /v1/admin/quarantines`, `GET /v1/admin/security-holds`, and `GET /v1/admin/mirror/stores` with a `tenantId` filter, and the endpoint names the quarantine or hold. SO-4b gates a platform admin's read of a tenant's session content and mirrors the request at once, and neither change states whether a case-scoped read of escrowed transcripts or activity content is such a read. This review renamed the deferral event to `forensics.erasure_deferred_completed`, which the existing carve-out covers.

- (a) SO-4a excludes every action taken under a case until the case discloses it: each route under a `forensics.*` permission, whatever role calls it, and every event a case action emits. The carve-out names the routes or permissions rather than the presence of a `caseId`, because a list read accepts `tenantId` without `caseId`. SO-4b does not gate a case-scoped read through 0128's read path, because the custody event records it, and 0142 states that as a residual against SOR-6. A `security`-tier test pins that the tenant's chain carries no mirror row for them.
- (b) SO-4a mirrors those actions with a delay until the case closes or discloses them.
- (c) SO-4a and SO-4b stay as written, and FP3 narrows to `forensics.*` events and error codes.

**Recommendation: (a).** It matches decision 6 ("no signal ... disclosure as a recorded case action") and 0142's decision 16, which leaves the disclosure of quarantines and holds to this change.

Blocks: 0128.S1, 0128.S2, 0128.S3, 0128.S4, and 0128.S7, and 0142.S4 and 0142.S5.

**O4. The legal-hold request body and the findings whose paragraphs this change edits** (raised by the M3 readiness review, 2026-11-19; sources: 0128-F4).

"Defects found while verifying" states that no step closes F-15.1.85, F-11.7.27, or F-12.8.65, while "Specification sections affected" corrects the `POST /v1/admin/legal-hold` body in §15.1 (F-15.1.85, which states that the body "needs a decision rather than a rename"), corrects one of the four wrong OCSF class names in §11.7 (F-11.7.27), and removes the `audit_range` and `workspace_snapshot` ledger types from §12.8 (F-12.8.45 asks for the preflight to enumerate every ledger type).

- (a) This change closes all three: 0128.S4 closes F-15.1.85 with the code's body (`tenantId`, `sessionId`, and `artifactId`) and F-12.8.45 in part, by removing the two ledger types, and 0128.S1 closes F-11.7.27 with all four class corrections. The steps gain `closes` entries, and "Defects found while verifying" is restated.
- (b) This change takes none of them: it drops the legal-hold body correction and the class-name correction from "Specification sections affected", and the findings stay open as issues.

**Recommendation: (a).** This change already edits each paragraph, and the code's body names a tenant, which a `platform-admin` caller needs.

Blocks: 0128.S1 and 0128.S4.

**O5. Per-region escrow clients and escrow immutability** (raised by the M3 readiness review, 2026-11-19; sources: 0128-F6).

FE-3 copies held items to the region's escrow bucket through `storage.regions.<region>.legalHoldEscrow`, which "Configuration by scope" calls an existing setting, and FP4 keeps held material in the tenant's region. The tree builds one escrow store from `--legal-hold-escrow-bucket` and none per region (F-12.8.52), and an escrowed object is deletable by any holder of the escrow store's credentials while its hold is active (F-12.8.63). No change of the program closes either finding, so `SECURITY_HOLD_REGION_UNRESOLVABLE` and FP4 cannot hold as written.

- (a) 0128.S4 builds the per-region escrow clients and closes F-12.8.52, and states whether a security-hold copy carries an object-store legal hold.
- (b) A separate change builds region routing for every store (F-12.8.47) and the per-region escrow clients, and 0128.S4 depends on it.

**Recommendation: (a)**, for the escrow client alone, because FE-3 cannot meet FP4 without it.

Blocks: 0128.S4.

**O6. How the two-person rule of decision 12 selects regulated tenants and covers capture reads** (raised by the M3 readiness review, 2026-11-19; sources: 0128-F8).

Decision 12 settles "two-person release and capture reads on by default for regulated tenants, and off elsewhere". The design carries only `forensics.twoPersonRelease`, a deployment-scoped setting with no default, and states no capture-read rule. The tree marks a regulated tenant through `complianceProfile`, which [`0150-tenant-security-settings`](../0150-tenant-security-settings/change.md) keeps unchanged until a follow-up design replaces it with templates (its O9), and 0150's `regulated@1` template is applied by copy and leaves no lasting mark on the tenant.

- (a) A tenant whose `complianceProfile` is other than `none` gets the rule by default, a platform admin can turn it on or off per tenant, and the deployment setting sets the default for the other tenants. A capture read (`GET /v1/admin/cases/{id}/captures/{captureId}` and the export) needs a second `platform-forensics` principal's approval recorded on the case.
- (b) The rule becomes a per-tenant security setting under 0150's settings model, which the `regulated@1` template turns on, with the same capture-read approval as (a).

**Recommendation: (a).** It keys the default on the field that marks a regulated tenant today, and the follow-up design of 0150's O9 moves it with the other uses of `complianceProfile`.

Blocks: 0128.S2, 0128.S3, 0128.S4, and 0128.S6.

**O7. Which gateway replica delivers `CH-PRESERVE`, and which instance runs a hold's copy worker** (raised by the M3 readiness review, 2026-11-19; sources: 0128-F10).

`LNK-POD-GRPC` is one connection per gateway replica per pod, and the coordinator handoff fences RPCs by generation. FE-1 states only that the handler sends `CH-PRESERVE` and that the hold state accepts it beside `CoordinatorFence`. A warm pod has no coordinator, and a concurrent-slot pod can have sessions coordinated by several replicas. FE-3's audit copy worker extends a segment and advances `auditCursor` with no single-writer rule.

- (a) The handler's replica dials the pod and sends `CH-PRESERVE`, and the adapter accepts it from any authenticated gateway replica whatever the coordinator generation, because the RPC only stops the adapter from ending work. Each hold's worker takes a row lock on the hold for each pass.
- (b) The handler forwards `CH-PRESERVE` to the coordinator of each session on the pod, and to its own replica for a warm pod, and the adapter keeps its generation fence. The hold workers run under leader election.

**Recommendation: (a).** It works for a warm pod and a pod with several coordinators without a forward, and a row lock needs no new election.

Blocks: 0128.S2 and 0128.S4.

**O8. A freeze during a `cleanup` run, and a quarantine that arrives during a slot release** (raised by the M3 readiness review, 2026-11-19; sources: 0128-F11, checkpoint-workspace-C3, pod-structure-C6). This change relies on the conflict between [`0145-session-isolation`](../0145-session-isolation/change.md) SIR-23 and [`0153-workspace-materialization`](../0153-workspace-materialization/change.md) WMR-12, which 0145 records as its O20. Option (a) here follows 0145 O20 (a), and option (b) follows 0145 O20 (b), so FE-1's **Behind a setup run.** states the `cleanup` case as the answer to 0145 O20 settles it.

FE-1's **Behind a setup run.** cites WM-3 and covers only a `setup` run. SIR-23 of 0145 kills a `cleanup` run's process group and then serves the `freeze`, because a `cleanup` run has no cancel pipe, while WMR-12 of 0153 answers every request other than `fs`, `ping`, and `end` with `worker_busy` during any run. FE-1 also does not state what `CH-PRESERVE` does to a slot release already in End, Sweep, or `cleanup`.

- (a) FE-1 cites SI-3 for the freeze rule and follows SIR-23: a `freeze` during `cleanup` kills the cleanup worker's process group and is then served. `CH-PRESERVE` stops a release in progress before its next step, the slot is reported as partly released in the per-step status, and the `integration` freeze tests cover both cases.
- (b) FE-1 follows WMR-12 with a cancel pipe added to `cleanup` runs, so a `freeze` during `cleanup` cancels the run as during `setup`, and `CH-PRESERVE` stops a release in progress as in (a).

**Recommendation: (a)**, which follows 0145's ownership of the `CH-SUPERVISE` protocol.

Blocks: 0128.S2.

**O9. The outcome of a paused session on a quarantined pod under `sessionDisposition: fail`** (raised by the M3 readiness review, 2026-11-19; sources: checkpoint-workspace-C6). This is a conflict with [`0146-session-lifecycle`](../0146-session-lifecycle/change.md).

FE-1 fails a quarantined pod's sessions with `orphan_pod_terminated`, the reason a lost pod produces, so that the tenant receives no signal (decision 6). Under 0146, a paused session whose pod terminated stays paused with its pod reference cleared and never reaches that state, so failing a paused session on a quarantined pod produces an outcome that only a quarantine produces.

- (a) Under `fail`, a paused session on the quarantined pod gets 0146's lost-pod outcome: it stays paused with its pod reference cleared.
- (b) A paused session fails with `orphan_pod_terminated`, and decision 6 records the exception as a residual signal.

**Recommendation: (a).** It keeps the outcome identical to a lost pod's, which is what decision 6 asks of the reason.

Blocks: 0128.S2.

**O10. The refusal of session creation under the activity floor** (raised by the M3 readiness review, 2026-11-19; sources: 0128-F13).

A floor that keeps `exec` or `file` on refuses session creation on a pool that is not monitored, and `activityMonitoring` is valid only with `isolationProfile: sandboxed`, so on a deployment with `standard` or `microvm` pools such a floor refuses every session of the tenant there. No error code or status is named, and a per-tenant floor raised during an investigation surfaces through this refusal and through the tenant's read of its own settings, against FP3.

- (a) The refusal returns an existing placement error with the same text as any other unmet pool requirement, the tenant reads `agentActivity.floor` as part of its settings, and a platform admin's write to it is not a case action, so a floor is set as deployment policy and never during an investigation. The runbook states that.
- (b) The refusal returns a new error code, the tenant cannot read `agentActivity.floor`, and a write to it is a case action under FP3.

**Recommendation: (a).** A tenant needs to read the floor to set its own settings above it, so hiding it does not hold, and the runbook keeps the floor out of investigations.

Blocks: 0128.S7 and 0128.S8.

**O11. Who sets the `agentActivity.*` streams, and whether turning one off is a weakening** (raised by the M3 readiness review, 2026-11-19; sources: 0128-F22). This concerns [`0142-security-oversight`](../0142-security-oversight/change.md) SO-2.

"Configuration by scope" gives `agentActivity.tool`, `.exec`, and `.file` to the tenant admin. SO-2's tenant security record holds a customer's security constraints, a `security-admin` writes it, and a relaxation waits out the weakening delay. Neither change states whether a stream turned off is such a weakening.

- (a) The `agentActivity.*` streams join the tenant security record: a `security-admin` writes them, and turning a stream off or shortening its retention is a weakening under SO-2's delay.
- (b) They stay tenant settings that a `tenant-admin` writes as an audited write, as decision 23 states for a write above the floor.

**Recommendation: (a).** A stolen `tenant-admin` token could otherwise turn evidence off at once before an investigation starts, which the activity floor exists to limit.

Blocks: 0128.S7 and 0128.S8.

**O12. The step that 0128.S4's output-file edge names if 0135.S4 is split** (raised by the M3 readiness review, 2026-11-19; requested by change 0135; mirrors 0135 O9).

0128.S4 depends on `0135.S4 (part)`, because its copy of output files waits for [MD-18](../0135-message-delivery/change.md#md-18-files). O9 of [`0135-message-delivery`](../0135-message-delivery/decisions.md) asks whether 0135.S4 splits into four steps, of which the fourth carries the file exports of MD-18 and depends on 0131.

- (a) 0135.S4 is split, as 0135 O9 (a) states, and 0128.S4's edge names the file export step that the split creates, still as a `(part)` edge for the output-file copy alone.
- (b) 0135.S4 stays whole, as 0135 O9 (b) states, and 0128.S4 keeps its `0135.S4 (part)` edge.

**Recommendation: (a)**, following 0135's recommendation. The output-file copy needs only MD-18, so the edge to the file export step waits for nothing else of 0135.S4.

Blocks: the `depends-on` entry of 0128.S4.

**O13. One residency region per deployment against per-region escrow** (raised by the M3 readiness review, 2026-11-19; requested by change 0136; mirrors 0136 O10).

[`0136-placement`](../0136-placement/change.md) D24 and PM-7 state that a deployment serves one residency region, while §12.8 **Data residency** item 2 keeps per-region storage routing from the `storage.regions` map, with per-region legal-hold escrow. FE-3 copies held items to `storage.regions.<region>.legalHoldEscrow`, FP4 keeps held material in the tenant's region, and a hold whose region has no escrow entry fails with `SECURITY_HOLD_REGION_UNRESOLVABLE`. O10 of [`0136-placement`](../0136-placement/decisions.md) asks which residency model holds.

- (a) Per-region storage routing stays, as 0136 O10 (a) states. FE-3, FP4, and `SECURITY_HOLD_REGION_UNRESOLVABLE` stand, and O5 decides how 0128.S4 builds the per-region escrow clients.
- (b) Per-region storage routing is retired in favour of one deployment per region, as 0136 O10 (b) states. FE-3 copies to the deployment's one escrow bucket, `SECURITY_HOLD_REGION_UNRESOLVABLE` and the per-region escrow lookup leave, FP4 holds because the deployment runs in one region, and O5 has nothing left to decide.

**Recommendation: (a)**, following 0136's recommendation for v1, because (b) is a removal that no member change scopes.

Blocks: 0128.S4, and the answer to O5.

## Decisions

Revision 24 recorded every decision as settled. Decision numbers are referenced above. Decisions 14, 20, and 21 are stated in this list, and the others are in the table:

- **Decision 14: network cut on quarantine.** Every agent allow policy excludes the label, including `allow-gateway-ingress`, and the label follows the acknowledged `CH-PRESERVE`. The cost is that a pod whose adapter did not acknowledge can lose `CH-MSGSOCK` at 120 seconds.
- **Decision 20: selecting monitored pods.** The `activityMonitoring` pool field and `runtimeClasses.monitored.name`, because one profile maps to one RuntimeClass. A new isolation profile would touch each profile-keyed rule with no change in isolation. A node-wide setting would charge unmonitored pools and fail every gVisor pod on a collector outage.
- **Decision 21: the `file` stream.** Off per pool until the spike reports the Sentry's cost, so early adopters have `exec` and tool evidence alone.

| # | Decision | Recommendation | Cost of the recommendation | Main alternative |
| :-- | :-- | :-- | :-- | :-- |
| 1 | Producer for agent activity | **Settled**, in the owner's words "go with your recommendations". Two layers: tool activity on every profile, and gVisor runtime monitoring for `observed` process and file evidence. | A new DaemonSet, node configuration, and a dependency on gVisor's trace points, whose compatibility and reconnection behaviour need a spike. | SDK only: no node component, and everything is `self_reported`. A host eBPF sensor sees nothing inside the default profile. |
| 2 | Kata coverage in the first version | **Settled**, in the owner's words "go with your recommendations". Supervisor tracing is the first follow-up. `tool_only`, stated in the API. | The highest-risk profile lacks process evidence, and `microvm` is what §5.3 recommends for that risk. | An in-guest sensor: a custom guest kernel and agent, as Ant Group built, with records that remain `self_reported`. |
| 3 | `isolationProfile: standard` coverage | **Settled**, in the owner's words "go with your recommendations". A public producer contract and a tested Tetragon forwarder, with no sensor in the chart. | A deployer installs a privileged DaemonSet itself. | A chart option, which ships a privileged DaemonSet on agent nodes in Lenny's chart. |
| 4 | Default state | **Settled.** Off at the deployment level, with a separate setting for tool and connector activity, for processes and commands, and for file access, in the owner's words "ok (separate config values for tool and connector activity, processes/commands, and file access)". A SaaS operator turns each on, and tenants choose within it. Under [`0124-connector-removal`](../0124-connector-removal/change.md) (2026-11-15), connector activity no longer exists, and the tool setting covers platform tool calls and the SDK's reports. | No evidence exists for a tenant until someone chooses it, and an incident before that has none. | On by default at `tool` level, which is cheap and needs a customer agreement to state it. |
| 5 | Who reads activity | Settled by the owner with [SO decision 19](../0142-security-oversight/decisions.md#decisions): `tool`, `exec`, and `file` to `tenant-admin`, with `security-auditor` only through `auditorStreamGrants`, `args` only on grant, and the operator only under an open case. The `security-auditor` default is withdrawn. | A subject tenant admin can read its own streams, a customer's security team takes an explicit step and a delay, and the case requirement slows the operator. | Operator-only: simpler, and it removes the customer's evidence, which [`0142-security-oversight`](../0142-security-oversight/change.md) exists to provide. |
| 6 | Does the tenant learn of a quarantine or hold | **Settled**, in the owner's words "no setting. Operators can decide to alert their customers by themselves (they can build the automation themselves, if they want it automated)". No signal, a generic existing failure reason, and disclosure as a recorded case action. An operator that wants to notify customers builds it on the `forensics.*` platform-tenant events, which reach its own SIEM copy and its §25.5 subscriptions. | A tenant sees a failure whose reason does not state the cause, and an operator can misuse silence. SO-4 must carve it out. | Immediate notice or a distinct reason: simple and transparent, and it warns a malicious tenant. |
| 7 | Erasure during a hold | **Settled**, in the owner's words "setting that controls whether a generic sentence or accurate result (generic sentence by default, configurable text)". Proceed, defer only under an approved basis, and carry `forensics.erasure.retentionStatement`, `generic` by default with configurable text on every receipt, or `accurate`. | The generic text appears on receipts where nothing was retained, and `accurate` tells a tenant that a hold exists. §11.8 calls the operator a processor, so the choice is each operator's legal assessment. | A receipt stating a retention, which hints at an investigation. Blocking erasure, which reveals the hold and delays the tenant's obligation. |
| 8 | Retention class default | **Settled**, in the owner's word "ok". 0 days in the chart, and a SaaS operator sets a period in its agreement, 90 days being a starting value. | Without it, offboarding removes evidence. With it, data outlives the tenant, on a basis the operator assesses. | A non-zero chart default, which makes the promise for every deployer. |
| 9 | Who may set a quarantine | **Settled**, in the owner's word "ok". `platform-forensics` and `platform-admin`. Tenants ask through the operator. | A customer's team cannot freeze its own pod quickly. | A tenant request permission limited to an exclusive pod, once the audit and cost rules are proven. |
| 10 | Default session disposition | **Settled**, in the owner's word "ok". `fail`. | A false positive ends a customer's session. | `resume`, which keeps work going and restores possibly tainted state. |
| 11 | Quarantine lifetime | **Settled**, in the owner's word "ok". A platform admin lists every quarantined pod through `GET /v1/admin/quarantines`, including break-glass ones. No automatic release, with an alert at 24 hours. | A forgotten quarantine holds a node from upgrading. Cluster upgrades can stall. | An automatic release at a deadline, which destroys evidence on a timer. |
| 12 | Release control | **Settled**, in the owner's word "ok". Two-person release and capture reads on by default for regulated tenants, and off elsewhere. | A small team needs a second person at release. | One person, with the audit event as the control. |
| 13 | Capture automation | **Settled**, in the owner's word "ok". No privileged Lenny controller. An unbound role and a published image, used through the deployer's privileged access. | Capture is slower and manual, and needs a practised operator. | An API-driven capture controller holding `nodes/checkpoint`, and privileged Job creation, at the cost of a standing high-value target. |
| 15 | Argument detail default | **Settled**, in the owner's word "ok". `redacted`. | Secrets with no recognizable form are stored, encrypted. | `metadata`, which loses the arguments an exfiltration investigation needs. |
| 16 | Who pays | **Settled**, in the owner's word "ok". The operator pays for quarantined pods and held bytes. Tenants are metered for activity bytes. | Operators carry investigation costs. | Charging the tenant, which reveals the hold. |
| 17 | Source independent of the decision service | **Settled**, in the owner's word "ok". Envoy's second logger to `lenny-evidence`, with the deployer's log pipeline and flow logs as an optional outside source. | A second stream of tunnel records, and a compromised `lenny-evidence` remains a common point. | A log shipper in `lenny-egress` with its own bucket credential, which is stronger against `lenny-evidence` and breaks the rule that no producer holds a store credential. |
| 18 | How a hold protects artifacts | **Settled**, in the owner's word "ok". Copy on hold to escrow. | Copy time and storage at set time. Later writes to a session need a named range. | An in-place flag, which is cheaper and changes rotation and quota in ways a tenant can see. |
| 19 | `onCoverageLoss` default | **Settled**, in the owner's word "ok". `continue`, with an alert and a gap record. | A dark pod keeps running. | `retire`, which turns a collector rollout into capacity loss. |
| 22 | Post-quarantine freeze | **Settled**, in the owner's word "ok". Automatic in-pod SIGSTOP on sidecar pods after the acknowledgement, with `freeze: false` to opt out, and a node-side freeze in the runbook. Since revision 31 every agent pod has slot containers, so the in-pod freeze applies to every slot, after any setup run is cancelled. | It changes process state, and a frozen process may affect checkpoint methods, untested. A slot with a setup run outstanding is frozen only after the run ends. | No freeze: the agent keeps running and can destroy evidence, though it is observed live. |
| 23 | Activity floor and key delay | **Settled**, in the owner's word "ok". `forensics.activity.floor` and `keyDestructionDelay`, with chart defaults of `none` and 0, and a SaaS operator raising them in its agreement. | A floor reduces the tenant's control over collection, and a delay postpones erasure of a key, on a period the operator assesses. With the defaults, a tenant can erase evidence before a hold exists. | No floor, leaving this to the tenant. A non-zero chart default, which makes the promise for every deployer. |
| 24 | Hold basis | **Settled**, in the owner's word "ok". A required enumerated `basis` and `basisRef` per hold, with erasure deferred only for `legal_obligation` and `legal_claims` by default. | Each hold needs the operator's own judgement of its basis, which slows the first minutes, so an investigator quarantines first and sets the hold once a basis exists. An incident-response basis alone loses material to erasure and to tenant deletion, and a store hold is treated the same way. | Treat security response as deferring by default, which asserts a legal conclusion in code. |
| 25 | `keepalive` container | **Settled**, in the owner's word "ok". A regular container in every agent pod, at a minimal request. | One more container per pod, including warm pods, a small density cost, a reserved UID, and an image to pin. | Rely on the adapter staying alive, which leaves a pod with an exited adapter losing the volumes. An ephemeral container at quarantine time, which is unverified and is subject to the cred-guard webhook. |
| 26 | Liveness of agent pods | **Settled**, in the owner's word "ok", and amended by the owner on 2026-11-09. Agent pods carry no liveness probe, which matches the tree. The amendment of §4.4.3 that this decision first carried, under which the adapter exits on `checkpointStuck`, is dropped, because [IC-9](../0131-incremental-checkpoint/change.md#ic-9-removal-of-the-embedded-sigstop-checkpoint-path) removes the `SIGSTOP` checkpoint path and the flag. | A deadlocked adapter goes undetected by the kubelet, until the gateway's gRPC keepalive probes on its pod connection and the orphan session reconciler act. | Keep a probe, so a freeze of a slot container leads the kubelet to kill it, or route it through `keepalive`, which adds a proxy to a component meant to do nothing. |
| 27 | Writer of a break-glass quarantine row | **Settled**, in the owner's word "ok". The forensics reconciler writes the row with `origin: break_glass` and a custody event once the session store (Postgres) is reachable, and `GET` reports the labelled pod as `unregistered` until then. The WarmPoolController projects the label and creates no row. | The row's principal is the forensics reconciler, so the Kubernetes audit log remains the record of who stamped the label. | The WarmPoolController writes the row when it projects the label, which gives a second writer of `pod_quarantines` and a controller loop that otherwise reads only informers. |
| 29 | Defaults of the quarantine alert, the held-UID budget, and the reconciler schedule | **Settled** on 2026-11-13, when the owner accepted the proposed values in the answers to the consolidated list of open decisions and asked to "record any owed validations". `forensics.quarantine.maxPods` defaults to 10, `gitMirror.heldUidBudget` to a quarter of the mirror's fetch-UID range, as [`0127-egress-and-git`, decision 66](../0127-egress-and-git/decisions.md#decisions) also records, and the `lenny-evidence-reconcile` schedule to hourly. Each has an owed validation ("Validation owed" in `steps.md`, item 7). | An alert at 10 pods can fire late on a small deployment and early during a large incident, a quarter of the range limits concurrent fetches while stores are held, and an hourly reconciler finds a disagreement up to an hour late. | No chart default, which revision 32 left to the chart. |
| 28 | Evidence keys wrapped by the tenant key when its state becomes `retained` (O1) | **Settled** on 2026-11-13, in the owner's words "FE O1: keep". Data keys wrapped by the tenant key stay so wrapped, `lenny-evidence` unwraps them through the tenant key while it exists, and the tenant security settings count them with the tenant's objects under the key before the state moves from `retained` to `none` ([TSS-4](../0150-tenant-security-settings/change.md#tss-4-tenant-key)). | The key state stays `retained` while any evidence data key of the tenant is wrapped by the tenant key, which can be for the longest stream retention. | Rewrap every data key under the regional evidence key when the state becomes `retained`, at a KMS call pair per key, which takes those records out of reach of the tenant key's deletion. |

### Record of O1

The M3 readiness review of 2026-11-19 moved this entry here from "Open decisions", because the owner settled it as decision 28.

**O1. Evidence keys wrapped by the tenant key when the key state becomes `retained`** (raised by [`0150-tenant-security-settings`](../0150-tenant-security-settings/change.md) as I12).

While a tenant's key state is `active`, `lenny-evidence` wraps each new session and pod-incarnation data key with the tenant key ("Keys" under FE-6). A relaxation of `encryption.tenantKey` moves the state to `retained` ([TSS-4](../0150-tenant-security-settings/change.md#tss-4-tenant-key)), and new data keys are then wrapped by the regional evidence key.

- (a) Keep. Data keys wrapped by the tenant key stay so wrapped, and `lenny-evidence` unwraps them through the tenant key while it exists. The tenant settings design counts them, with the tenant's objects under the key, before the state moves from `retained` to `none`.
- (b) Rewrap. When the state becomes `retained`, a job in `lenny-evidence` rewraps every data key of the tenant under the regional evidence key, with one KMS decrypt and one encrypt per key.

**Recommendation: (a).** It matches TSS-4's rule that objects written under the tenant key keep it, so a tenant's evidence and its other data follow one rule, and Phase 4a's deletion of the tenant key still makes every evidence record wrapped under it unreadable. (b) costs a KMS call pair per data key and takes those records out of reach of the tenant key's deletion. (a) needs the one change to the tenant settings design listed under "Changes requested of other designs".

**Settled (2026-11-13): (a)**, in the owner's words "FE O1: keep" (decision 28).
