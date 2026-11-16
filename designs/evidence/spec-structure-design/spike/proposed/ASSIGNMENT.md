# Assignment of current material to the proposed tree

Status: spike output, 2026-11-16. This file assigns every heading of the current specification, every design, and every page under `docs/reference/` to its destination in the proposed tree. Page codes resolve through `MAP.md`. A destination that leaves the specification names its target in words. Percentages give the share of a section's own words that each destination receives; they are estimates made from the section's bold paragraph labels, and they feed the size estimates at the end of this file.

Short destination names: "removed" is removed by a design (archive only); "archive" is the archive of designs and proposals (decision records); "BUILD-PLAN.md" is `BUILD-PLAN.md` (and `BUILD-GAPS.md` for open claims); "ROADMAP.md" is `ROADMAP.md`; "docs/about" is `docs/about/`; "issues" is the issue tracker; "operator guide" is `docs/operator-guide/`; "runbooks" is `docs/runbooks/`; "runtime author guide" is `docs/runtime-author-guide/`; "client guide" is `docs/client-guide/`; "reference runtimes" is the reference runtime repositories and `docs/runtime-author-guide/`; "adapter repos" is the client protocol adapter repositories (`lenny-adapter-*`); "code docs" is Go package documentation; "TESTING.md" is `TESTING.md`; "contributor docs" is `CONTRIBUTING.md` and contributor docs.

Sections are cited by number and heading. An unnumbered heading is cited by the path from its nearest numbered ancestor. "Own words" counts the words under a heading up to its first subheading, outside code fences.

## Coverage

- The current specification has 696 headings outside code fences: 2 at level 1, 46 at level 2, 357 at level 3, and 291 at level 4. `grep -n "^#" spec/*.md` returns 760 lines; the other 64 are comment lines inside code fences. No heading is deeper than level 4.
- Every heading is assigned: 553 by an explicit row, 96 by the subsection rule below, and 47 by a whole-file rule (§18 and §23 leave the specification whole). None is unassigned.
- 27 headings are containers with no own text (for example "## 4. System Components" and "### 4.6 Pod Lifecycle Controllers"); their content is assigned through their subsections.
- **Subsection rule.** Most subsections of §25 follow one template (Endpoints, Go Interface, Data Sources, Storage, Degradation, Metrics, Error Codes, Audit Events). For a subsection family whose parent row names a capability, a component, and an interface, a child titled Endpoints, Response, Error Codes, Authorization, Filters, or Status Values goes to the interface; Go Interface leaves to package documentation; Data Sources, Caching, Implementation, Metrics, and storage tiers go to the component; Storage splits between the component and the Postgres stored schema; Degradation, Behavior, Rules Engine, and Alerting Rules go to the capability; Audit Events go to the audit record interface.

## Specification headings

### `spec/01_executive-summary.md`

| Current heading | Own words | Destination | Split note |
| :-- | --: | :-- | :-- |
| §1 Executive Summary | 129 | CTX | Purpose paragraph. |
| &nbsp;&nbsp;§1 Executive Summary › Core Design Principles | 209 | PRIN |  |

### `spec/02_goals-and-non-goals.md`

| Current heading | Own words | Destination | Split note |
| :-- | --: | :-- | :-- |
| §2 Goals and Non-Goals | 0 | None (container) |  |
| &nbsp;&nbsp;§2 Goals and Non-Goals › Goals | 219 | PRIN |  |
| &nbsp;&nbsp;§2 Goals and Non-Goals › Non-Goals | 131 | PRIN |  |

### `spec/03_high-level-architecture.md`

| Current heading | Own words | Destination | Split note |
| :-- | --: | :-- | :-- |
| §3 High-Level Architecture | 349 | ARCH 80%, CTX 20% | Component diagram and request path → ARCH; actors and external systems → CTX. |

### `spec/04_system-components.md`

| Current heading | Own words | Destination | Split note |
| :-- | --: | :-- | :-- |
| §4 System Components | 0 | None (container) |  |
| &nbsp;&nbsp;§4.0 Agent Operability Additions | 452 | code docs 50%, LOPS 20%, OPSEV 30% | lenny-ops introduction → LOPS; the Go package list leaves to package documentation; the EventEmitter wiring list → OPSEV, whose event types EVTOPS defines. |
| &nbsp;&nbsp;§4.1 Edge Gateway Replicas | 891 | GW 25%, GWSCALE 30%, DCFGTIER 10%, TESTING.md 25%, ARCH 10% | Role → ARCH; responsibilities, deployment, HPA metric roles, and maxSessionsPerReplica → GW; per-tier values → DCFGTIER; the capacity calibration methodology leaves to TESTING.md; the MCP and type: mcp endpoints leave (rest-only-client-api RO-11, agent-runtimes-only AR-1). |
| &nbsp;&nbsp;&nbsp;&nbsp;§4.1 Edge Gateway Replicas › Gateway Internal Subsystems | 1286 | GW 30%, GWSCALE 30%, TESTING.md 30%, removed 10% | Subsystem partition, per-subsystem limits and breakers, extraction thresholds → GW internal parts and process settings; LLM Proxy subsystem → GWLLM; threshold calibration methodology leaves to TESTING.md; the MCP Fabric wording leaves with the MCP client API. |
| &nbsp;&nbsp;&nbsp;&nbsp;§4.1 Edge Gateway Replicas › Request Message Scope | 337 | CHGWPOD 85%, TESTING.md 15% | Request scope derivation → conventions of CHGWPOD; the tier-0 gate statement → TESTING.md. |
| &nbsp;&nbsp;§4.2 Session Manager | 1164 | SESS 50%, GW 20%, STORPG 20%, TENISO 10% | Session record and generations → SESS model; columns → STORPG; the multi-tenancy statement → TENISO; the "manages" list → GW internal parts. |
| &nbsp;&nbsp;§4.3 Token Service | 499 | TOKSVC 60%, TOKX 20%, removed 20% | Deployment, ServiceAccount, KMS access → TOKSVC; token endpoint role → TOKX; connector and LLM provider credential roles removed (connector-removal CR-1, llm-upstreams-and-cost-budgets LU-4). |
| &nbsp;&nbsp;§4.4 Event / Checkpoint Store | 0 | None (container) |  |
| &nbsp;&nbsp;&nbsp;&nbsp;§4.4.1 Stored Records and Audit Egress | 146 | AUDIT 50%, STORPG 50% | Stored record kinds → STORPG; audit egress → AUDIT. |
| &nbsp;&nbsp;&nbsp;&nbsp;§4.4.2 Checkpoint Atomicity and Partial Manifests | 592 | CKPT 60%, CKSTORE 40% | Atomicity rule → CKPT; manifest format → CKSTORE and STOROBJ (incremental-checkpoint IC-2 supersedes the manifest). |
| &nbsp;&nbsp;&nbsp;&nbsp;§4.4.3 Checkpoint Quiescence, Timeout, and Abort Handling | 1452 | CKPT 85%, RTPOPS 15% | Quiesce, timeout, and abort rules → CKPT; quiesce frames → RTPOPS. |
| &nbsp;&nbsp;&nbsp;&nbsp;§4.4.4 Checkpoint SLOs and Scheduling | 740 | CKPT 70%, SLO 30% | Interval and scheduling rules → CKPT; checkpoint SLO targets → SLO. |
| &nbsp;&nbsp;&nbsp;&nbsp;§4.4.5 Checkpoint Storage Failure and Eviction Fallback | 1621 | CKPT 30%, CKSTORE 40%, STORPG 10%, OBJ 20% | Eviction fallback behavior → CKPT; storage failure rules → CKSTORE; EvictionStateStore records → STORPG; object-store outage handling → OBJ. |
| &nbsp;&nbsp;§4.5 Artifact Store | 328 | ARTIFACT 70%, OBJ 30% | What is stored and its retention → ARTIFACT; backend → OBJ. |
| &nbsp;&nbsp;§4.6 Pod Lifecycle Controllers | 11 | None (container) |  |
| &nbsp;&nbsp;&nbsp;&nbsp;§4.6.1 Warm Pool Controller (Pod Lifecycle) | 9255 | WPC 35%, POOL 15%, CLAIM 15%, APOD 8%, KUBE 10%, K8S 5%, WEBHOOK 2%, removed 10% | Role, responsibilities, leader election, scaling, failover, API rate limiting, cold-start fill, crash during scale-down → WPC; internal abstraction layer → WPC internal parts; resource kinds and API group → K8S and the kind pages; claim mechanism, occupancy projection, reserved hold, orphaned claim detection, fallback claim path and its preconditions, mirror reconciliation → CLAIM; onPoolExhausted, warm pool sizing, idle cost and scale-to-zero, disruption protection → POOL; finalizers and the host-schedulable label → APOD and KLABEL; claim admission webhook → WEBHOOK and KADMIT; etcd write pressure, tuning, and the tuning matrix → KUBE; ADR-007, dependency pinning, go/no-go, and fallback plan leave (pod-lifecycle-controller PLC-1, decision-records REC-1). |
| &nbsp;&nbsp;&nbsp;&nbsp;§4.6.2 PoolScalingController (Pool Configuration) | 1693 | PSC 55%, POOL 40%, KPSET 5% | Reconcile from Postgres, leader election, admission-denial handling → PSC; scaling formula, demand inputs, schedules → POOL; the fields it writes → KPSET. |
| &nbsp;&nbsp;&nbsp;&nbsp;§4.6.3 CRD Field Ownership and Write Boundaries | 2627 | KPSET 20%, KAPOD 20%, KPCLAIM 20%, KADMIT 15%, WPC 8%, PSC 7%, GW 5%, CLAIM 5% | Field-ownership table → the writer column of each kind page; SSA enforcement and conflict retry → WPC and PSC; gateway RBAC grants → GW; the claim status phase enumeration → KPCLAIM; the Postgres-authoritative-state webhook → KADMIT and WEBHOOK. |
| &nbsp;&nbsp;§4.7 Runtime Adapter | 0 | None (container) |  |
| &nbsp;&nbsp;&nbsp;&nbsp;§4.7.1 Role and Gateway RPC Contract | 4947 | CHGWPOD 40%, ADAPTER 15%, SEND 10%, SLOTOPS 15%, CLAIM 10%, SCRUB 10% | RPC table → CHGWPOD operations; registry entries, the registry critical section → ADAPTER internal parts; bind attempt token, stamp-once rule, admission → SLOT and CLAIM; session seal → SEND; Shutdown teardown kinds → SCRUB. |
| &nbsp;&nbsp;&nbsp;&nbsp;§4.7.2 Checkpoint and Interrupt Mutual Exclusion | 308 | CKPT 70%, ADAPTER 30% | Mutual-exclusion rule → CKPT; its lock → ADAPTER. |
| &nbsp;&nbsp;&nbsp;&nbsp;§4.7.3 Adapter Events on CH-ADAPTEREVENTS | 158 | CHPODGW |  |
| &nbsp;&nbsp;&nbsp;&nbsp;§4.7.4 Adapter ↔ Runtime Protocol (Intra-Pod) | 57 | RTP | Pointer section; the runtime protocol pages own the content. |
| &nbsp;&nbsp;&nbsp;&nbsp;§4.7.5 Adapter Manifest | 234 | ADMAN |  |
| &nbsp;&nbsp;&nbsp;&nbsp;§4.7.6 Adapter Manifest Field Reference | 873 | ADMAN |  |
| &nbsp;&nbsp;&nbsp;&nbsp;§4.7.7 Runtime Integration Levels (agent-type only) | 77 | RTP | Integration levels become declared capabilities (runtime-protocol RP-2). |
| &nbsp;&nbsp;&nbsp;&nbsp;§4.7.8 Credential Rotation by Integration Level | 908 | removed 80%, RTPOPS 20% | Removed with LLM credential rotation (llm-upstreams-and-cost-budgets LU-4 names §4.7.8); any VCS rotation frame → RTPOPS. |
| &nbsp;&nbsp;&nbsp;&nbsp;§4.7.9 Startup Sequence for `type: agent` Runtimes | 276 | ADAPTER 60%, RTPHAND 40% | Adapter start order → ADAPTER; the runtime-visible handshake → RTPHAND. |
| &nbsp;&nbsp;&nbsp;&nbsp;§4.7.10 Deployment Model | 4768 | SUPV 40%, SCRUB 25%, APOD 10%, CHINTRA 10%, removed 15% | Sidecar-versus-embedded trade-offs leave (agent-pod-layout PL-2); runtime process lifetime, supervisor launch, end, sweep, and the runtime-exit table → SUPV; restart and keep dispositions → SCRUB; retirement outcomes → APOD; supervision frames → CHINTRA; health check → ADAPTER. |
| &nbsp;&nbsp;&nbsp;&nbsp;§4.7.11 Adapter-Agent Security Boundary | 2501 | SECMODEL 60%, POD 20%, SLOT 20% | Trust boundary → SECMODEL; per-container enforcement → POD; per-slot items → SLOT. |
| &nbsp;&nbsp;§4.8 Gateway Policy Engine | 100 | ADMIT |  |
| &nbsp;&nbsp;&nbsp;&nbsp;§4.8 Gateway Policy Engine › `RequestInterceptor` Extension Point | 3960 | INTCPT 45%, INTCPTIF 40%, ADMIT 10%, PKI 5% | Phases, payloads, verdicts → INTCPTIF; where the chain runs and how verdicts apply, failPolicy → INTCPT (runtime-routing RT-2 supersedes part); the built-in chain order → ADMIT; interceptor mTLS → PKI. |
| &nbsp;&nbsp;§4.9 Credential Leasing Service | 132 | CPOOL |  |
| &nbsp;&nbsp;&nbsp;&nbsp;§4.9 Credential Leasing Service › Credential Provider | 156 | CPOOL 50%, removed 50% | VCS providers → CPOOL; LLM providers removed (LU-4). |
| &nbsp;&nbsp;&nbsp;&nbsp;§4.9 Credential Leasing Service › Credential Pool | 824 | CPOOL 60%, removed 40% |  |
| &nbsp;&nbsp;&nbsp;&nbsp;§4.9 Credential Leasing Service › Credential Storage Topology | 2448 | CPOOL 50%, TOKSVC 30%, STORPG 20% | Secret topology rules → CPOOL; Token Service secret read path → TOKSVC; encrypted tables → STORPG. |
| &nbsp;&nbsp;&nbsp;&nbsp;§4.9 Credential Leasing Service › Pre-Claim Credential Availability Check | 245 | removed 80%, SCREATE 20% | Removed (LU-4); the configuration check at the post-chain point → SCREATE and LLMUP. |
| &nbsp;&nbsp;&nbsp;&nbsp;§4.9 Credential Leasing Service › Credential Lease | 120 | CPOOL 50%, removed 50% |  |
| &nbsp;&nbsp;&nbsp;&nbsp;§4.9 Credential Leasing Service › `materializedConfig` Schema by Provider | 914 | CDELIV 40%, removed 60% | LLM provider schemas removed; VCS forms → CDELIV. |
| &nbsp;&nbsp;&nbsp;&nbsp;§4.9 Credential Leasing Service › Credential Policy | 249 | removed | credentialPolicy removed (LU-4); the tenant's LLM upstream list replaces it → LLMUP. |
| &nbsp;&nbsp;&nbsp;&nbsp;§4.9 Credential Leasing Service › Three Credential Modes | 274 | removed 60%, CDELIV 40% |  |
| &nbsp;&nbsp;&nbsp;&nbsp;§4.9 Credential Leasing Service › Pre-Authorized Credential Flow | 1086 | CPOOL 40%, removed 60% |  |
| &nbsp;&nbsp;&nbsp;&nbsp;§4.9 Credential Leasing Service › Fallback Flow | 477 | removed | Fallback chains removed (LU-4). |
| &nbsp;&nbsp;&nbsp;&nbsp;§4.9 Credential Leasing Service › Proactive Lease Renewal | 1214 | removed | Proactive renewal removed (LU-4). |
| &nbsp;&nbsp;&nbsp;&nbsp;§4.9 Credential Leasing Service › LLM Reverse Proxy | 3175 | LLMUP 45%, GWLLM 35%, removed 20% | Replaced by the metering proxy (LU-3): routing and security rules → LLMUP; listener, process settings, metrics → GWLLM; lease-token injection removed. |
| &nbsp;&nbsp;&nbsp;&nbsp;§4.9 Credential Leasing Service › Semantic Caching | 475 | removed | Removed (llm-response-cache-removal RC-1); provider prompt caching passthrough (RC-5) → LLMUP. |
| &nbsp;&nbsp;&nbsp;&nbsp;§4.9 Credential Leasing Service › `CredentialRouter` Interface | 749 | removed | Removed (LU-4). |
| &nbsp;&nbsp;&nbsp;&nbsp;§4.9 Credential Leasing Service › Credential Governance Boundaries | 301 | CPOOL 60%, SECMODEL 40% |  |
| &nbsp;&nbsp;&nbsp;&nbsp;§4.9 Credential Leasing Service › Emergency Credential Revocation | 1159 | CPOOL 70%, runbooks 30% | Revocation rules → CPOOL; operator procedure → docs/runbooks; LLM rows removed. |
| &nbsp;&nbsp;&nbsp;&nbsp;§4.9 Credential Leasing Service › Security Boundaries | 224 | CPOOL 50%, SECMODEL 50% |  |
| &nbsp;&nbsp;&nbsp;&nbsp;§4.9 Credential Leasing Service › Credential Lifecycle and Secret Management | 769 | CPOOL 60%, TOKSVC 20%, KMS 20% |  |
| &nbsp;&nbsp;&nbsp;&nbsp;§4.9.1 KMS Key Rotation Procedure | 418 | KMS 30%, TOKSVC 30%, runbooks 40% | Rotation behavior → TOKSVC and KMS; the step-by-step procedure → docs/runbooks. |
| &nbsp;&nbsp;&nbsp;&nbsp;§4.9.2 Credential Audit Events | 608 | EVTAUD 60%, CPOOL 40% | Event schemas → EVTAUD; which action emits which event → CPOOL. |

### `spec/05_runtime-registry-and-pool-model.md`

| Current heading | Own words | Destination | Split note |
| :-- | --: | :-- | :-- |
| §5 Runtime Registry and Pool Model | 0 | None (container) |  |
| &nbsp;&nbsp;§5.1 Runtime | 905 | RUNTIME 55%, RTPHAND 10%, POOL 10%, KRUNTIME 5%, removed 20% | Runtime fields, labels, tenant customization → RUNTIME; capabilities.* → RTPHAND (runtime-protocol RP-2); preConnect and sdkWarmBlockingPaths → POOL; type: mcp and integrationLevel removed (agent-runtimes-only AR-1, AR-4). |
| &nbsp;&nbsp;&nbsp;&nbsp;§5.1 Runtime › Standalone Runtime | 524 | RUNTIME 80%, ADMRT 20% | Rules → RUNTIME; example and write operation → ADMRT. |
| &nbsp;&nbsp;&nbsp;&nbsp;§5.1 Runtime › Derived Runtime | 212 | RUNTIME |  |
| &nbsp;&nbsp;&nbsp;&nbsp;§5.1 Runtime › Inheritance Rules | 90 | RUNTIME |  |
| &nbsp;&nbsp;&nbsp;&nbsp;§5.1 Runtime › Normative Merge Algorithm | 869 | RUNTIME |  |
| &nbsp;&nbsp;&nbsp;&nbsp;§5.1 Runtime › Worked Examples | 170 | RUNTIME 50%, operator guide 50% | One example stays under Flows; the rest move to the operator guide. |
| &nbsp;&nbsp;&nbsp;&nbsp;§5.1 Runtime › Derived Runtime Instantiation | 177 | RUNTIME |  |
| &nbsp;&nbsp;&nbsp;&nbsp;§5.1 Runtime › Setup Commands and Policy | 43 | SETUP |  |
| &nbsp;&nbsp;&nbsp;&nbsp;§5.1 Runtime › `agentInterface` Field | 43 | RUNTIME |  |
| &nbsp;&nbsp;&nbsp;&nbsp;§5.1 Runtime › `publishedMetadata` Field | 455 | RUNTIME 70%, CAPIDISC 30% |  |
| &nbsp;&nbsp;&nbsp;&nbsp;§5.1 Runtime › Capability Inference from MCP `ToolAnnotations` | 263 | removed | Removed (agent-runtimes-only AR-1; connector-removal). |
| &nbsp;&nbsp;&nbsp;&nbsp;§5.1 Runtime › First-Party Reference Runtimes | 88 | reference runtimes |  |
| &nbsp;&nbsp;&nbsp;&nbsp;§5.1 Runtime › Minimal Configuration | 95 | operator guide 60%, RUNTIME 40% |  |
| &nbsp;&nbsp;§5.2 Pool Configuration and Execution Modes | 106 | POOL |  |
| &nbsp;&nbsp;&nbsp;&nbsp;§5.2 Pool Configuration and Execution Modes › Execution Modes | 11900 | SCRUB 15%, SCRUBPROC 20%, SLOT 10%, SLOTOPS 15%, APOD 10%, POOL 8%, CLAIM 4%, SUPV 5%, TIMEOUT 2%, removed 11% | Pool dimensions, onPoolExhausted, pinned idle inventory → POOL; tenant pinning → CLAIM; maxClientIdleSeconds → TIMEOUT; T4 reuse prohibition, scrub model, deployer acknowledgements, client visibility of weak reuse → SCRUB; Lenny scrub procedure, supervised scrub, what the scrub reaches, Sweep, Kata variant, onScrubFailure → SCRUBPROC; slot and reclaim hold → SLOT; concurrent-session slot failure, assignment atomicity, rehydration, slot retry → SLOTOPS; recycle lifecycle, keep pod, retirement policy → APOD; runtime supervisor → SUPV; service mode and stateless runtimes removed (agent-runtimes-only AR-2). |
| &nbsp;&nbsp;&nbsp;&nbsp;§5.2 Pool Configuration and Execution Modes › Execution Mode Scaling Implications | 952 | POOL 70%, removed 30% | mode_factor formula → POOL; service-mode rows removed. |
| &nbsp;&nbsp;&nbsp;&nbsp;§5.2 Pool Configuration and Execution Modes › Pool Taxonomy | 114 | POOL 50%, operator guide 50% | Disallowed combinations → POOL; hot and cold pool strategy → operator guide. |
| &nbsp;&nbsp;&nbsp;&nbsp;§5.2 Pool Configuration and Execution Modes › Bootstrap Behavior — Pool Warming at First Deployment | 447 | POOL 60%, CAPISES 10%, PLACE 15%, PSC 15% | PoolWarmingUp condition, bootstrap fill, WarmPoolBootstrapping alert → POOL; the 503 response → CAPISES; topology spread → PLACE; operator visibility → PSC metrics. |
| &nbsp;&nbsp;§5.3 Isolation Profiles | 731 | ISO 60%, SECMODEL 25%, WEBHOOK 15% | Profiles and acknowledgeNonceOnlyAuth → ISO; image supply chain → SECMODEL; RuntimeClass-aware admission → WEBHOOK and KADMIT. |

### `spec/06_warm-pod-model.md`

| Current heading | Own words | Destination | Split note |
| :-- | --: | :-- | :-- |
| §6 Warm Pod Model | 0 | None (container) |  |
| &nbsp;&nbsp;§6.1 What a Pre-Warmed Pod Looks Like | 2896 | POOL 50%, APOD 15%, PSC 15%, ADAPTER 10%, SCRUB 10% | Pod-warm and SDK-warm states, demotion, demotion-rate breaker → POOL; breaker persistence across failover → PSC; adapter SIGTERM in sdk_connecting and its watchdog → ADAPTER and APOD; one-session-per-pod default and recycle opt-in → SCRUB; per-session credential lease removed (LU-4). |
| &nbsp;&nbsp;§6.2 Pod State Machine | 7353 | APOD 30%, SESS 20%, SUSP 15%, RESUME 15%, TIMEOUT 10%, SLOT 5%, CLAIM 5% | Pod states, crash during a session, host-node precondition → APOD; session sub-states drawn in the same machine (input_required, suspended, resuming, resume_pending) → SESS, SUSP, RESUME; timer behavior across states → TIMEOUT; pre-running slot cleanup and leaked slots → SLOT; reserved hold → CLAIM; proxy and direct mode rows removed. |
| &nbsp;&nbsp;§6.3 Startup Latency Analysis | 1096 | SLO 40%, POOL 20%, TESTING.md 40% | Latency targets → SLO; what pre-warming removes from the hot path → POOL rationale; benchmark requirements → TESTING.md. |
| &nbsp;&nbsp;§6.4 Pod Filesystem Layout | 1048 | RTPFS 60%, POD 30%, DATACLASS 10% | Paths a runtime sees → RTPFS (agent-pod-layout PL-3 rewrites them); volumes and responsibility split → POD; data-at-rest protection → DATACLASS. |

### `spec/07_session-lifecycle.md`

| Current heading | Own words | Destination | Split note |
| :-- | --: | :-- | :-- |
| §7 Session Lifecycle | 0 | None (container) |  |
| &nbsp;&nbsp;§7.1 Normal Flow | 4541 | SCREATE 45%, MATER 10%, UPLOAD 10%, ARTIFACT 8%, SDERIVE 8%, SESS 7%, SEND 12% | Creation steps, atomicity, reclaim on failed bind, isolation response → SCREATE; uploadToken → UPLOAD; materialization steps → MATER; artifact retention and transcript → ARTIFACT; derive semantics → SDERIVE; failureClass → SESS; seal-and-export, terminal pipeline, post-terminal window, terminal writes → SEND. |
| &nbsp;&nbsp;§7.2 Interactive Session Model | 8999 | STREAM 20%, SESS 15%, IMSG 20%, INBOX 20%, MSG 10%, RESUME 5%, GWCOORD 5%, CAPIMSG 5% | Client operations → CAPIMSG; streaming events, ordering and resume, reconnect, SSE back-pressure → STREAM; reattach symmetry and ReattachedChild → GWCOORD and DTREE; state machine and terminal writes → SESS; inter-session messaging, scope, hierarchy, rate limits, content policy, sibling patterns → IMSG; inbox, durable inbox, DLQ, receipts, drain on terminal → INBOX; routing paths and precedence → MSG (message-delivery MD-5 supersedes the seven paths). |
| &nbsp;&nbsp;§7.3 Retry and Resume | 2546 | RESUME 85%, removed 5%, DTREE 5%, STREAM 5% | Retry policy, generations, resume flow and driver, awaiting_client_action → RESUME; children → DTREE; last_seq → STREAM; credential leases on the replacement pod removed. |
| &nbsp;&nbsp;§7.4 Upload Safety | 1130 | UPLOAD 80%, CAPIFILE 20% |  |
| &nbsp;&nbsp;§7.5 Setup Commands | 460 | SETUP |  |

### `spec/08_recursive-delegation.md`

| Current heading | Own words | Destination | Split note |
| :-- | --: | :-- | :-- |
| §8 Recursive Delegation | 0 | None (container) |  |
| &nbsp;&nbsp;§8.1 Design Philosophy | 115 | DLG |  |
| &nbsp;&nbsp;§8.2 Delegation Mechanism | 2137 | DLG 60%, LEASE 10%, RTPTOOL 10%, DTREE 10%, removed 10% | Flow, what the parent sees, virtual child interface → DLG; LeaseSlice and tracingContext injection → LEASE; tool surface → RTPTOOL; tree memory and subtree offloading → DTREE; type: mcp refusal removed. |
| &nbsp;&nbsp;§8.3 Delegation Policy and Lease | 0 | None (container) |  |
| &nbsp;&nbsp;&nbsp;&nbsp;§8.3 Delegation Policy and Lease › `DelegationPolicy` as First-Class Resource | 3727 | DPOL 85%, ADMPOL 15% |  |
| &nbsp;&nbsp;&nbsp;&nbsp;§8.3 Delegation Policy and Lease › Delegation Lease | 7004 | LEASE 60%, BUDGET 15%, DPOL 10%, removed 15% | Lease fields, treeVisibility, allowSelfRecursion, isolation monotonicity, tree-wide limits → LEASE; budget reservation model → BUDGET; delegation presets → DPOL; experimentContext (runtime-routing RT-1) and credential propagation (LU-4) removed. |
| &nbsp;&nbsp;§8.4 Approval Modes | 74 | DLG |  |
| &nbsp;&nbsp;§8.5 Delegation Tools | 656 | RTPTOOL 60%, DLG 40% | Tool schemas → RTPTOOL; semantics → DLG. |
| &nbsp;&nbsp;§8.6 Lease Extension | 662 | LEASEXT |  |
| &nbsp;&nbsp;&nbsp;&nbsp;§8.6 Lease Extension › Configuration Layering | 359 | LEASEXT |  |
| &nbsp;&nbsp;&nbsp;&nbsp;§8.6 Lease Extension › Lease Extension Approval Modes | 1384 | LEASEXT |  |
| &nbsp;&nbsp;§8.7 File Export Model | 795 | FILEX |  |
| &nbsp;&nbsp;§8.8 TaskRecord and TaskResult Schema | 0 | None (container) |  |
| &nbsp;&nbsp;&nbsp;&nbsp;§8.8 TaskRecord and TaskResult Schema › TaskRecord | 1364 | DLG 40%, STORPG 30%, RTPTOOL 30% | Record semantics → DLG; columns → STORPG; the MCP column removed (RO-11). |
| &nbsp;&nbsp;&nbsp;&nbsp;§8.8 TaskRecord and TaskResult Schema › TaskResult | 1022 | RTPTOOL 70%, DLG 30% |  |
| &nbsp;&nbsp;§8.9 Task Tree | 48 | DLG |  |
| &nbsp;&nbsp;§8.10 Delegation Tree Recovery | 2417 | DTREE |  |

### `spec/09_mcp-integration.md`

| Current heading | Own words | Destination | Split note |
| :-- | --: | :-- | :-- |
| §9 MCP Integration | 0 | None (container) |  |
| &nbsp;&nbsp;§9.1 Where MCP Is Used | 137 | IFMAP 50%, adapter repos 50% | Which protocol is used where → IFMAP; client-facing MCP → MCP adapter repository. |
| &nbsp;&nbsp;&nbsp;&nbsp;§9.1 Where MCP Is Used › Platform MCP Server Tools | 186 | RTPTOOL |  |
| &nbsp;&nbsp;&nbsp;&nbsp;§9.1 Where MCP Is Used › Runtime Discovery | 83 | CAPIDISC 60%, adapter repos 40% |  |
| &nbsp;&nbsp;§9.2 Elicitation Chain | 2062 | INPUT 70%, RTPTOOL 15%, adapter repos 15% | Chain rules → INPUT; the runtime's elicitation tool → RTPTOOL; MCP elicitation forms → MCP adapter. |
| &nbsp;&nbsp;§9.3 Connector Definition and OAuth/OIDC | 0 | None (container) |  |
| &nbsp;&nbsp;&nbsp;&nbsp;§9.3 Connector Definition and OAuth/OIDC › `ConnectorDefinition` as First-Class Resource | 189 | removed | Connectors removed (connector-removal CR-1); successor pointer → OPSVC. |
| &nbsp;&nbsp;&nbsp;&nbsp;§9.3 Connector Definition and OAuth/OIDC › OAuth/OIDC Flow | 319 | LINK 40%, removed 60% | Shared OAuth link flow → LINK (connector-authorization CA-0); connector parts removed. |
| &nbsp;&nbsp;§9.4 Memory Store | 871 | removed 80%, operator guide 20% | Removed (connector-authorization CA-10); external memory pattern → operator guide. |

### `spec/10_gateway-internals.md`

| Current heading | Own words | Destination | Split note |
| :-- | --: | :-- | :-- |
| §10 Gateway Internals | 0 | None (container) |  |
| &nbsp;&nbsp;§10.1 Horizontal Scaling | 0 | None (container) |  |
| &nbsp;&nbsp;&nbsp;&nbsp;§10.1.1 Stateless Replicas and Per-Session Coordination | 911 | GWCOORD 80%, CHSTORE 20% | Coordination rules → GWCOORD; REG-COORDLEASE → CHSTORE. |
| &nbsp;&nbsp;&nbsp;&nbsp;§10.1.2 Coordinator Handoff Protocol | 1107 | GWCOORD 80%, CHGWPOD 20% | Handoff protocol → GWCOORD; CH-FENCE messages → CHGWPOD. |
| &nbsp;&nbsp;&nbsp;&nbsp;§10.1.3 Dual-Store Unavailability | 390 | GWCOORD 60%, AVAIL 40% |  |
| &nbsp;&nbsp;&nbsp;&nbsp;§10.1.4 Coordinator-Loss Detection and Hold State | 933 | GWCOORD 70%, SESS 30% | Detection → GWCOORD; the hold state as session behavior → SESS. |
| &nbsp;&nbsp;&nbsp;&nbsp;§10.1.5 Stale Replica Behavior | 304 | GWCOORD |  |
| &nbsp;&nbsp;&nbsp;&nbsp;§10.1.6 Autoscaling Metrics and HPA Policy | 1021 | GWSCALE 70%, DCFGTIER 30% |  |
| &nbsp;&nbsp;&nbsp;&nbsp;§10.1.7 preStop Hook Drain | 6561 | GWDRAIN 85%, SEND 15% | Drain stages → GWDRAIN; terminal pipelines at process exit → SEND. |
| &nbsp;&nbsp;&nbsp;&nbsp;§10.1.8 Rolling Updates and the CheckpointBarrier Protocol | 1619 | GWBARRIER 70%, CHGWPOD 20%, GW 10% |  |
| &nbsp;&nbsp;§10.2 Authentication | 3416 | AUTHN 65%, BROWSER 10%, TOKSVC 10%, PLAY 15% | Claims, tenant_id extraction, verifier key set and rotation → AUTHN; signing topology and KMS failure → TOKSVC; playground auth paths and mint invariants → BROWSER, cookie handling in the process → PLAY. |
| &nbsp;&nbsp;&nbsp;&nbsp;§10.2 Authentication › Authorization and RBAC | 1526 | AUTHZ 85%, ADMIAM 15% |  |
| &nbsp;&nbsp;§10.3 mTLS PKI | 3031 | PKI 70%, CERTM 15%, GW 5%, REDIS 3%, PG 2%, TESTING.md 5% | CA, lifecycle, peer validation → PKI; cert-manager failure and CA rotation → CERTM; startup TLS probe and configuration validation → GW; Redis and PgBouncer TLS → REDIS and PG; integration test requirement → TESTING.md. |
| &nbsp;&nbsp;§10.4 Gateway Reliability | 640 | GW 60%, AVAIL 40% |  |
| &nbsp;&nbsp;§10.5 Upgrade and Rollback Strategy | 1482 | COMPAT 35%, UPGRADE 30%, TOKSVC 10%, runbooks 15%, REV 10% | Expand-contract discipline and CRD versioning → COMPAT; migration tooling and CRD upgrade procedure → UPGRADE; script usage and GitOps note → docs; Token Service and KMS rotation → TOKSVC; adapter and agent binaries → REV. |
| &nbsp;&nbsp;&nbsp;&nbsp;§10.5 Upgrade and Rollback Strategy › `RuntimeUpgrade` State Machine | 1088 | REV 70%, removed 30% | Replaced by the runtime-level rollout (runtime-revisions RV-4). |
| &nbsp;&nbsp;§10.6 Environment Resource and RBAC Model | 1126 | ENV 75%, AUTHZ 10%, removed 15% | Environments, tag selection, cross-environment delegation, noEnvironmentPolicy → ENV (tenant-scoping TS-1 supersedes); member roles → AUTHZ; mcpRuntimeFilters and connectorSelector removed. |
| &nbsp;&nbsp;§10.7 Experiment Primitives | 7421 | removed 90%, ROUTE 10% | Removed (runtime-routing RT-1); runtime routes replace variant routing → ROUTE. |

### `spec/11_policy-and-controls.md`

| Current heading | Own words | Destination | Split note |
| :-- | --: | :-- | :-- |
| §11 Policy and Controls | 0 | None (container) |  |
| &nbsp;&nbsp;§11.1 Admission and Fairness | 580 | ADMIT |  |
| &nbsp;&nbsp;§11.2 Budgets and Quotas | 2952 | QUOTA 50%, BUDGET 35%, removed 15% | Quota hierarchy, storage quota, counter crash recovery, overshoot formula → QUOTA; budget inheritance and delegation budget counters → BUDGET; proxy and direct mode rows removed. |
| &nbsp;&nbsp;&nbsp;&nbsp;§11.2.1 Billing Event Stream | 3403 | BILLEV 55%, EVTBILL 35%, GW 5%, AUDIT 5% | Event types, schema, null contract → EVTBILL; sinks, immutability, corrections and their authorization → BILLEV; metrics → GW; correction audit → AUDIT. |
| &nbsp;&nbsp;§11.3 Timeouts and Cancellation | 920 | TIMEOUT |  |
| &nbsp;&nbsp;§11.4 User Invalidation | 621 | AUTHN 70%, PRINC 30% |  |
| &nbsp;&nbsp;§11.5 Idempotency | 197 | APICONV |  |
| &nbsp;&nbsp;§11.6 Circuit Breakers | 2125 | BREAKER 75%, ADMPOL 15%, EVTOPS 5%, EVTAUD 5% |  |
| &nbsp;&nbsp;§11.7 Audit Logging | 5875 | AUDIT 50%, SIEM 35%, TSET 10%, STORPG 5% | Integrity, hash chain, write-time tenant validation, platform-tenant residency, Postgres-only gap, superuser bypass → AUDIT; OCSF format, mapping, pinning, translation failure, dead-letter → SIEM; compliance profile gate and downgrade ratchet → TSET; columns → STORPG. |
| &nbsp;&nbsp;§11.8 Security Incident Response | 401 | FORENSIC 60%, runbooks 40% |  |

### `spec/12_storage-architecture.md`

| Current heading | Own words | Destination | Split note |
| :-- | --: | :-- | :-- |
| §12 Storage Architecture | 0 | None (container) |  |
| &nbsp;&nbsp;§12.1 Design Principle | 96 | ARCH |  |
| &nbsp;&nbsp;§12.2 Storage Roles | 530 | ARCH 40%, STORPG 40%, removed 20% | Role-to-backend map → ARCH; tables → STORPG; MemoryStore row removed (CA-10). |
| &nbsp;&nbsp;§12.3 Postgres HA Requirements | 5528 | PG 60%, TENISO 10%, operator guide 25%, AVAIL 5% | Topology, pooler, RLS defenses, write classification, failover, encryption, backups → PG; R-01 to R-03 query rules → TENISO; IOPS and sizing guidance → operator guide. |
| &nbsp;&nbsp;§12.4 Redis HA and Failure Modes | 5704 | REDIS 55%, QUOTA 20%, STORREDIS 15%, operator guide 10% | Topology, logical separation, scalability ceilings, cluster pre-plan → REDIS; key isolation and patterns → STORREDIS and TENISO; failure behavior per use and fail-open budgets → QUOTA; throughput quantification → operator guide. |
| &nbsp;&nbsp;§12.5 Artifact Store | 5861 | OBJ 45%, CKSTORE 30%, ARTIFACT 10%, QUOTA 5%, DATACLASS 10% | Backend, HA, versioning, replication, TLS, pre-drain check → OBJ; encryption and the T4 per-tenant key → DATACLASS; checkpoint retention, eviction context, manifests, GC, tombstone sweep → CKSTORE (incremental-checkpoint IC-6 supersedes); storage quota → QUOTA. |
| &nbsp;&nbsp;§12.6 Interface Design | 718 | EXT 60%, code docs 40% | Scaling extension interfaces → EXT; shared type definitions and struct cross-reference → package documentation. |
| &nbsp;&nbsp;&nbsp;&nbsp;§12.6 Interface Design › PodRegistry | 892 | EXT 40%, code docs 60% |  |
| &nbsp;&nbsp;&nbsp;&nbsp;§12.6 Interface Design › StoreRouter | 1242 | EXT 40%, PG 30%, code docs 30% |  |
| &nbsp;&nbsp;&nbsp;&nbsp;§12.6 Interface Design › ClusterRegistry | 721 | EXT 50%, code docs 50% |  |
| &nbsp;&nbsp;&nbsp;&nbsp;§12.6 Interface Design › CredentialGenerator | 51 | code docs |  |
| &nbsp;&nbsp;&nbsp;&nbsp;§12.6 Interface Design › EventBus | 2275 | CHSTORE 40%, EXT 20%, code docs 20%, EVT 20% | Pub/sub topics → CHSTORE; CloudEvents envelope → EVT. |
| &nbsp;&nbsp;§12.7 Extensibility | 41 | EXT |  |
| &nbsp;&nbsp;§12.8 Compliance Interfaces | 15691 | ERASE 28%, PSEUDO 12%, RESID 18%, HOLD 15%, TENANT 12%, BACKUP 5%, DATACLASS 5%, ARTIFACT 2%, removed 3% | T4 ephemeral workspace isolation → DATACLASS; legal hold, checkpoint rotation, hold reconciler → HOLD; erasure, user-level invocation, erasure job, lifecycle fence, processing restriction, backends in scope, DeleteByUser order → ERASE; erasure_salt, pseudonymization, billing erasure, receipts → PSEUDO; tenant deletion, hold interaction, SLA → TENANT; backups in erasure scope and post-restore reconciler → ERASE and BACKUP; workspace deduplication → ARTIFACT; residency, admission control, KMS and backup residency, multi-region → RESID; MemoryStore preflight removed. |
| &nbsp;&nbsp;§12.9 Data Classification | 895 | DATACLASS 85%, TSET 15% |  |

### `spec/13_security-model.md`

| Current heading | Own words | Destination | Split note |
| :-- | --: | :-- | :-- |
| §13 Security Model | 0 | None (container) |  |
| &nbsp;&nbsp;§13.1 Pod Security | 2170 | POD 50%, SECMODEL 20%, SLOT 20%, removed 10% | Pod namespace isolation, container identity, supervisor volume → POD; per-slot credential scope → SLOT; fsGroup delivery and lenny-cred-readers removed (agent-pod-layout PL-5, LU-4). |
| &nbsp;&nbsp;§13.2 Network Isolation | 0 | None (container) |  |
| &nbsp;&nbsp;&nbsp;&nbsp;§13.2.1 CNI Requirement and Default-Deny Baseline | 141 | NETISO 60%, KUBE 40% | Baseline → NETISO; CNI requirement → KUBE. |
| &nbsp;&nbsp;&nbsp;&nbsp;§13.2.2 Agent Pod Allow-List Policies | 1040 | NETISO 80%, removed 20% | provider-direct, NET-006, NET-044 removed (LU-4, egress EG-2). |
| &nbsp;&nbsp;&nbsp;&nbsp;§13.2.3 Checkpoint Capability Model | 1139 | CKPT 60%, CHEGRESS 40% | Presigned capability rules → CKPT; CH-OBJSTORE contract → CHEGRESS. |
| &nbsp;&nbsp;&nbsp;&nbsp;§13.2.4 `lenny-system` Namespace NetworkPolicies | 4007 | NETISO 20%, GW 15%, LOPS 15%, TOKSVC 10%, WPC 8%, PSC 7%, WEBHOOK 8%, EGPROXY 5%, AGDNS 5%, PLAY 2%, PG 5% | Default deny for lenny-system → NETISO; each component allow-list → that component's Deployment section; ingress from the ingress controller → GW. |
| &nbsp;&nbsp;&nbsp;&nbsp;§13.2.5 Gateway Egress to Upstream LLM Providers | 1708 | LLMUP 60%, GW 20%, removed 20% |  |
| &nbsp;&nbsp;&nbsp;&nbsp;§13.2.6 Per-Pool Egress Relaxation and `egressProfile` | 1113 | EGRESS 80%, removed 20% |  |
| &nbsp;&nbsp;&nbsp;&nbsp;§13.2.7 DNS Exfiltration Mitigation and the Dedicated CoreDNS Instance | 929 | DNS 60%, AGDNS 40% |  |
| &nbsp;&nbsp;§13.3 Credential Flow | 5001 | TOKX 60%, AUTHN 5%, CKPT 5%, SECMODEL 5%, removed 25% | Token issuance and rotation, parameter mapping, claims, scope narrowing, impersonation, write-before-issue, revocation, rate limits, client authentication → TOKX; checkpoint object-store capabilities → CKPT; connector credentials removed (CR-1); LLM credential modes removed (LU-4); translator trust boundary removed (RO-11). |
| &nbsp;&nbsp;§13.4 Upload Security | 260 | UPLOAD |  |
| &nbsp;&nbsp;§13.5 Delegation Chain Content Security | 459 | DPOL |  |

### `spec/14_workspace-plan-schema.md`

| Current heading | Own words | Destination | Split note |
| :-- | --: | :-- | :-- |
| §14 Workspace Plan Schema | 3831 | WSPLANIF 55%, WSPLAN 25%, EVTHOOK 10%, LEASE 10% | Fields and constraints → WSPLANIF; source semantics, binding, defaults → WSPLAN; the callbackUrl webhook (delivery, signing, domain allowlist) → EVTHOOK; the delegation lease member → LEASE; credentialPolicy removed. |
| &nbsp;&nbsp;§14.1 WorkspacePlan Schema Versioning | 1785 | WSPLANIF 60%, COMPAT 40% |  |

### `spec/15_external-api-surface.md`

| Current heading | Own words | Destination | Split note |
| :-- | --: | :-- | :-- |
| §15 External API Surface | 1493 | IFMAP 20%, adapter repos 60%, removed 20% | The REST and MCP split and ExternalAdapterRegistry leave (rest-only-client-api RO-1, RO-11); IFMAP states that the client contract is REST alone. |
| &nbsp;&nbsp;&nbsp;&nbsp;§15 External API Surface › Shared Adapter Types | 2396 | removed | Adapter framework types removed (RO-11). |
| &nbsp;&nbsp;&nbsp;&nbsp;§15 External API Surface › SessionEvent Kind Registry | 2004 | CAPIEVT 60%, STREAM 20%, removed 20% | Kind registry → CAPIEVT; sub-state coverage → STREAM; dispatch filter, capability consistency, pluggability tiers, built-in adapter inventory, type: mcp endpoints, single-shot model, Open Responses conversations removed. |
| &nbsp;&nbsp;§15.1 REST API | 2828 | CAPI 15%, CAPISES 35%, CAPIFILE 15%, CAPIDISC 10%, CAPICRED 10%, SESS 10%, removed 5% | Endpoint tables → the client-api page for each resource group; preconditions and visible states → CAPISES with rules → SESS; derive-failure audit rows → EVTAUD; evaluation hooks removed (RT-1). |
| &nbsp;&nbsp;&nbsp;&nbsp;§15.1 REST API › Session Replay Semantics | 16287 | SDERIVE 5%, CAPISES 4%, ADMIN 10%, ADMRT 5%, ADMTEN 5%, ADMIAM 4%, ADMCRED 4%, ADMPOL 4%, ADMBILL 2%, ADMCOMP 3%, APOOLS 3%, DCFGSEED 2%, OPSAPI 3%, AUTHZ 5%, APICONV 12%, ERR 20%, USAGE 3%, removed 6% | Mis-leveled heading covering most of the admin API. Replay → SDERIVE and CAPISES (session-derive SD-1; evalRef removed); admin endpoint table → admin-api pages by group; operational and bootstrap planes → ADMIN and DCFGSEED; operability endpoints → OPSAPI; scope taxonomy and OIDC claim extensions → AUTHZ; MCP extension contract → OPSMCP; CI contract → TESTING.md; correlation headers, envelopes, OpenAPI, rate-limit headers, dryRun, ETags, pagination → APICONV; error envelope, error catalog, validation format → ERR and each owner; connector and experiment dryRun and connector live test removed; usage response → USAGE. |
| &nbsp;&nbsp;§15.2 MCP API | 2185 | adapter repos 85%, OPSMCP 15% | Client MCP tools, versions, wire projection → MCP adapter repository; management MCP references → OPSMCP. |
| &nbsp;&nbsp;&nbsp;&nbsp;§15.2.1 REST/MCP Consistency Contract | 1217 | adapter repos 70%, CAPI 30% | The REST half → CAPI compatibility; consistency obligations → adapter conformance (RO-6). |
| &nbsp;&nbsp;§15.3 Internal Control API (Custom Protocol) | 53 | CHGWPOD |  |
| &nbsp;&nbsp;§15.4 Runtime Adapter Specification | 535 | RTP 60%, CHAN 40% | Schema artifact list → CHAN wire-contract register; contracts it restates (SDK-warm demotion, bind attempt token, reclaim hold) → RTP with the rules in POOL and SLOT. |
| &nbsp;&nbsp;&nbsp;&nbsp;§15.4.1 Message Format and Binary I/O Requirements | 364 | RTPPART |  |
| &nbsp;&nbsp;&nbsp;&nbsp;§15.4 Runtime Adapter Specification › Translation Fidelity Matrix | 1576 | adapter repos 80%, RTPPART 20% | Protocol columns leave (RO-11); the MessagePart column → RTPMSG. |
| &nbsp;&nbsp;&nbsp;&nbsp;§15.4 Runtime Adapter Specification › `MessageEnvelope` — Unified Message Format | 1907 | RTPPART 60%, INBOX 20%, IMSG 20% | Envelope fields → RTPMSG; delivery_receipt and message_expired semantics → INBOX; threadId and inReplyTo → IMSG. |
| &nbsp;&nbsp;&nbsp;&nbsp;§15.4.2 RPC Lifecycle State Machine | 212 | CHGWPOD 50%, RTPMSG 50% |  |
| &nbsp;&nbsp;&nbsp;&nbsp;§15.4.3 Runtime Integration Levels | 2057 | RTP 50%, RTPHAND 30%, runtime author guide 20% | Levels become declared capabilities (RP-2); comparison matrix → RTP; MCP integration guidance → runtime author guide. |
| &nbsp;&nbsp;&nbsp;&nbsp;§15.4.4 Sample Echo Runtime | 920 | runtime author guide |  |
| &nbsp;&nbsp;&nbsp;&nbsp;§15.4.5 Runtime Author Roadmap | 460 | runtime author guide |  |
| &nbsp;&nbsp;&nbsp;&nbsp;§15.4.6 Conformance Test Suite | 1372 | RTPCONF 80%, TESTING.md 20% |  |
| &nbsp;&nbsp;§15.5 API Versioning and Stability | 1184 | COMPAT 60%, CAPI 20%, ADMIN 20% |  |
| &nbsp;&nbsp;§15.6 Client SDKs | 264 | client guide 70%, CAPI 30% |  |
| &nbsp;&nbsp;§15.7 Runtime Author SDKs | 2333 | RTPSDK 70%, runtime author guide 30% |  |

### `spec/16_observability.md`

| Current heading | Own words | Destination | Split note |
| :-- | --: | :-- | :-- |
| §16 Observability | 0 | None (container) |  |
| &nbsp;&nbsp;§16.1 Metrics | 11765 | GW 20%, GWSCALE 10%, GWCOORD 5%, GWLLM 5%, GWCLAIM 5%, WPC 15%, PSC 5%, TOKSVC 3%, ADAPTER 7%, LOPSSVC 5%, EGPROXY 2%, EGAUTHZ 3%, NETAGENT 3%, PG 2%, REDIS 2%, OBJ 2%, removed 6% | Each metric row → the Metrics section of its emitting component, with a link to the requirement it measures; the generated metrics catalog replaces the table; experiment, connector, and LLM credential metrics removed. |
| &nbsp;&nbsp;§16.1.1 Attribute Naming | 826 | OBSCONV |  |
| &nbsp;&nbsp;§16.2 Key Latency Breakpoints | 38 | SLO |  |
| &nbsp;&nbsp;§16.3 Distributed Tracing | 583 | OBSCONV |  |
| &nbsp;&nbsp;§16.4 Logging | 747 | OBSCONV |  |
| &nbsp;&nbsp;§16.5 Alerting Rules and SLOs | 13452 | SLO 25%, TIER 10%, POOL 8%, CLAIM 4%, APOD 4%, CKPT 5%, AUDIT 5%, BILLEV 3%, GWCOORD 4%, GW 6%, CPOOL 3%, PG 4%, REDIS 3%, OBJ 2%, CERTM 2%, WEBHOOK 3%, LOPS 3%, TESTING.md 3%, removed 3% | Each alert row → the capability whose requirement it detects a breach of, or the component for unit-health alerts; bundled and operator-driven split, SLO targets, burn-rate alerts → SLO; capacity tiers, Tier 4, workload profile, scale target → TIER; benchmark requirement → TESTING.md. |
| &nbsp;&nbsp;§16.6 Operational Events Catalog | 491 | EVTOPS 70%, OPSEV 30% |  |
| &nbsp;&nbsp;§16.7 Section 25 Audit Events | 4422 | EVTAUD 60%, REMED 10%, BACKUP 10%, UPGRADE 5%, DRIFT 5%, DIAG 5%, AUDIT 5% | Event schemas → EVTAUD; which operation emits each event → the owning capability. |
| &nbsp;&nbsp;§16.8 Section 25 Metrics | 333 | LOPS 80%, GWOPS 20% |  |
| &nbsp;&nbsp;§16.9 Prometheus Scrape Targets and CRDs | 217 | PROM 50%, DCFG 50% |  |
| &nbsp;&nbsp;§16.10 OpenSLO Export | 198 | SLO |  |

### `spec/17_deployment-topology.md`

| Current heading | Own words | Destination | Split note |
| :-- | --: | :-- | :-- |
| §17 Deployment Topology | 0 | None (container) |  |
| &nbsp;&nbsp;§17.1 Kubernetes Resources | 876 | ARCH 40%, GW 10%, TOKSVC 5%, WPC 5%, PSC 5%, LOPS 10%, BKJOB 5%, PROM 10%, POD 5%, PG 5% | The table becomes the component map in ARCH; each row's replicas, PDB, rolling update, and ServiceAccount → its component page. |
| &nbsp;&nbsp;§17.2 Namespace Layout | 4784 | ARCH 10%, KADMIT 25%, WEBHOOK 25%, PLACE 10%, POD 5%, NETISO 5%, DCFG 10%, PREFLT 5%, INSTALL 5% | Namespace list → ARCH; Pod Security Standards → POD; admission manifests, rejection labels, feature-gated inventory, HA contract, downgrade enforcement, failure modes → WEBHOOK and KADMIT; node isolation → PLACE; ResourceQuota and LimitRange → INSTALL; preflight enumeration → PREFLT. |
| &nbsp;&nbsp;§17.3 Disaster Recovery | 445 | BACKUP 60%, operator guide 40% |  |
| &nbsp;&nbsp;§17.4 Local Development Mode (`lenny-dev`) | 76 | LOCALDEV |  |
| &nbsp;&nbsp;&nbsp;&nbsp;§17.4 Local Development Mode (`lenny-dev`) › Embedded Mode: `lenny up` — Single-binary embedded stack | 1685 | LOCALDEV 70%, CLIINST 30% |  |
| &nbsp;&nbsp;&nbsp;&nbsp;§17.4 Local Development Mode (`lenny-dev`) › Source Mode: `make run` — Zero-dependency developer mode | 174 | LOCALDEV 50%, contributor docs 50% |  |
| &nbsp;&nbsp;&nbsp;&nbsp;§17.4 Local Development Mode (`lenny-dev`) › Compose Mode: `docker compose up` — Full local stack | 177 | LOCALDEV |  |
| &nbsp;&nbsp;&nbsp;&nbsp;§17.4 Local Development Mode (`lenny-dev`) › Credential-testing profile | 215 | LOCALDEV |  |
| &nbsp;&nbsp;&nbsp;&nbsp;§17.4 Local Development Mode (`lenny-dev`) › Observability in dev mode | 41 | LOCALDEV |  |
| &nbsp;&nbsp;&nbsp;&nbsp;§17.4 Local Development Mode (`lenny-dev`) › Zero-credential mode | 98 | LOCALDEV |  |
| &nbsp;&nbsp;&nbsp;&nbsp;§17.4 Local Development Mode (`lenny-dev`) › Dev mode guard rails | 217 | LOCALDEV |  |
| &nbsp;&nbsp;&nbsp;&nbsp;§17.4 Local Development Mode (`lenny-dev`) › Smoke test | 52 | TESTING.md |  |
| &nbsp;&nbsp;&nbsp;&nbsp;§17.4 Local Development Mode (`lenny-dev`) › Plugging in a custom runtime | 784 | runtime author guide |  |
| &nbsp;&nbsp;§17.5 Cloud Portability | 30 | PRIN |  |
| &nbsp;&nbsp;§17.6 Packaging and Installation | 2698 | INSTALL 35%, DCFG 15%, DCFGSEED 25%, UPGRADE 20%, COMPAT 5% | Chart contents → DCFG; CRD upgrade procedure, validation hook, stale CRD recovery → UPGRADE; bootstrap seed → DCFGSEED (format) and INSTALL (behavior). |
| &nbsp;&nbsp;&nbsp;&nbsp;§17.6 Packaging and Installation › Checks performed | 4537 | PREFLT 70%, INSTALL 30% | Each check → PREFLT, linking the requirement it verifies. |
| &nbsp;&nbsp;&nbsp;&nbsp;§17.6 Packaging and Installation › Helm `values.schema.json` | 236 | DCFG |  |
| &nbsp;&nbsp;&nbsp;&nbsp;§17.6 Packaging and Installation › Interactive installer (`lenny-ctl install`) | 775 | INSTALL 50%, CLIINST 50% |  |
| &nbsp;&nbsp;§17.7 Operational Runbooks | 5351 | runbooks 85%, RUNBOOK 15% | Runbook bodies → docs/runbooks; machine-consumable format → RUNBOOK. |
| &nbsp;&nbsp;§17.8 Capacity Planning and Defaults | 79 | TIER |  |
| &nbsp;&nbsp;§17.8.1 Operational Defaults — Quick Reference | 871 | CAT-SETTINGS | Hand-kept copy of defaults; each default moves to its setting's owner and the settings catalog lists it. |
| &nbsp;&nbsp;§17.8.2 Capacity Tier Reference | 7140 | DCFGTIER 35%, operator guide 35%, POOL 10%, GWSCALE 10%, PG 5%, REDIS 5% | Per-tier values → DCFGTIER; formulas a controller computes → POOL and GW; KEDA and Prometheus Adapter paths, worked examples, first-week workflow, sizing guidance → operator guide. |
| &nbsp;&nbsp;§17.8.3 Tier Promotion Guide | 68 | operator guide |  |
| &nbsp;&nbsp;&nbsp;&nbsp;§17.8.3 Tier Promotion Guide › Step 1 — Run Phase 13.5 Load Tests | 157 | operator guide |  |
| &nbsp;&nbsp;&nbsp;&nbsp;§17.8.3 Tier Promotion Guide › Step 2 — Go/No-Go Decision | 190 | operator guide |  |
| &nbsp;&nbsp;&nbsp;&nbsp;§17.8.3 Tier Promotion Guide › Step 3 — Apply Tier 3 Helm Values | 114 | operator guide |  |
| &nbsp;&nbsp;&nbsp;&nbsp;§17.8.3 Tier Promotion Guide › Step 4 — Post-Promotion Validation | 97 | operator guide |  |
| &nbsp;&nbsp;§17.8.4 Tier Preset Files | 120 | DCFGTIER |  |
| &nbsp;&nbsp;§17.8.5 Mandatory `lenny-ops` Deployment | 88 | LOPS |  |
| &nbsp;&nbsp;§17.8.6 Image Registry and Air-Gap | 164 | INSTALL 60%, DCFG 40% |  |
| &nbsp;&nbsp;§17.9 Deployment Answer Files | 140 | DCFGANS |  |
| &nbsp;&nbsp;&nbsp;&nbsp;§17.9.1 Composition Dimensions | 404 | DCFGANS |  |
| &nbsp;&nbsp;&nbsp;&nbsp;§17.9.2 Answer File Catalog | 335 | DCFGANS |  |
| &nbsp;&nbsp;&nbsp;&nbsp;§17.9.3 Cloud-Managed Backends | 458 | PG 35%, REDIS 30%, OBJ 35% |  |
| &nbsp;&nbsp;&nbsp;&nbsp;§17.9.4 Cloud Object Storage Lifecycle Requirements | 349 | OBJ 70%, CKSTORE 30% |  |
| &nbsp;&nbsp;&nbsp;&nbsp;§17.9.5 Self-Managed Backends | 195 | PG 35%, REDIS 30%, OBJ 35% |  |
| &nbsp;&nbsp;&nbsp;&nbsp;§17.9.6 Embedded Backends (Embedded Mode) | 107 | LOCALDEV |  |
| &nbsp;&nbsp;&nbsp;&nbsp;§17.9.7 Backend-Invariant Requirements | 408 | PG 35%, REDIS 30%, OBJ 35% |  |

### `spec/18_build-sequence.md`

| Current heading | Own words | Destination | Split note |
| :-- | --: | :-- | :-- |
| §18 Build Sequence | 122 | BUILD-PLAN.md | Whole section leaves. |
| &nbsp;&nbsp;§18.1 Phasing Intent | 397 | BUILD-PLAN.md | Whole section leaves. |
| &nbsp;&nbsp;§18.2 Phase Map | 533 | BUILD-PLAN.md | Whole section leaves. |
| &nbsp;&nbsp;§18.3 Phase 0 — Bootstrap the infrastructure repo | 192 | BUILD-PLAN.md | Whole section leaves. |
| &nbsp;&nbsp;§18.4 Phase 1 — Core types and wire contracts | 114 | BUILD-PLAN.md | Whole section leaves. |
| &nbsp;&nbsp;§18.5 Phase 1.5 — Database migration framework | 169 | BUILD-PLAN.md | Whole section leaves. |
| &nbsp;&nbsp;§18.6 Phase 2 — Adapter protocol, `make run`, ImageResolver, startup benchmark | 378 | BUILD-PLAN.md | Whole section leaves. |
| &nbsp;&nbsp;§18.7 Phase 2.5 — Observability foundation, shared rule packages | 199 | BUILD-PLAN.md | Whole section leaves. |
| &nbsp;&nbsp;§18.8 Phase 2.8 — `streaming-echo` runtime | 80 | BUILD-PLAN.md | Whole section leaves. |
| &nbsp;&nbsp;§18.9 Phase 3 — Pool scaling, delegation policy, runtime upgrade, mTLS | 378 | BUILD-PLAN.md | Whole section leaves. |
| &nbsp;&nbsp;§18.10 Phase 3.5 — Admission policies, `lenny-ops` first deploy | 389 | BUILD-PLAN.md | Whole section leaves. |
| &nbsp;&nbsp;§18.11 Phase 4 — Session manager, REST | 582 | BUILD-PLAN.md | Whole section leaves. |
| &nbsp;&nbsp;§18.12 Phase 4.5 — Admin API foundation, authentication, bootstrap | 239 | BUILD-PLAN.md | Whole section leaves. |
| &nbsp;&nbsp;§18.13 Phase 5 — ExternalAdapterRegistry, MCP/Completions/Open Responses | 175 | BUILD-PLAN.md | Whole section leaves. |
| &nbsp;&nbsp;§18.14 Phase 5.4 — etcd encryption at rest | 63 | BUILD-PLAN.md | Whole section leaves. |
| &nbsp;&nbsp;§18.15 Phase 5.5 — Basic credential leasing, Token Service | 244 | BUILD-PLAN.md | Whole section leaves. |
| &nbsp;&nbsp;§18.16 Phase 5.6 — Targeted security design review (credential) | 52 | BUILD-PLAN.md | Whole section leaves. |
| &nbsp;&nbsp;§18.17 Phase 5.75 — Minimum viable policy enforcement | 113 | BUILD-PLAN.md | Whole section leaves. |
| &nbsp;&nbsp;§18.18 Phase 5.8 — LLM Proxy, `lenny-direct-mode-isolation` admission webhook | 176 | BUILD-PLAN.md | Whole section leaves. |
| &nbsp;&nbsp;§18.19 Phase 6 — Interactive sessions, SDKs | 163 | BUILD-PLAN.md | Whole section leaves. |
| &nbsp;&nbsp;§18.20 Phase 6.5 — Incremental load test (streaming) | 33 | BUILD-PLAN.md | Whole section leaves. |
| &nbsp;&nbsp;§18.21 Phase 7 — Policy engine (quotas, budgets, audit hooks) | 352 | BUILD-PLAN.md | Whole section leaves. |
| &nbsp;&nbsp;§18.22 Phase 8 — Checkpoint/resume, drain-readiness webhook | 267 | BUILD-PLAN.md | Whole section leaves. |
| &nbsp;&nbsp;§18.23 Phase 9 — Delegation, `delegation-echo` | 447 | BUILD-PLAN.md | Whole section leaves. |
| &nbsp;&nbsp;§18.24 Phase 9.5 — Incremental load test (delegation) | 35 | BUILD-PLAN.md | Whole section leaves. |
| &nbsp;&nbsp;§18.25 Phase 10 — MCP fabric (virtual child interfaces, elicitation chain) | 222 | BUILD-PLAN.md | Whole section leaves. |
| &nbsp;&nbsp;§18.26 Phase 11 — Advanced credentials, multi-provider translators, revocation | 106 | BUILD-PLAN.md | Whole section leaves. |
| &nbsp;&nbsp;§18.27 Phase 11.5 — Incremental load test (credential lifecycle) | 32 | BUILD-PLAN.md | Whole section leaves. |
| &nbsp;&nbsp;§18.28 Phase 12a — Token Service hardening (KMS envelope, OAuth) | 152 | BUILD-PLAN.md | Whole section leaves. |
| &nbsp;&nbsp;§18.29 Phase 12b — `type: mcp` runtime support | 53 | BUILD-PLAN.md | Whole section leaves. |
| &nbsp;&nbsp;§18.30 Phase 12c — `sessionPolicy` presets and service mode | 134 | BUILD-PLAN.md | Whole section leaves. |
| &nbsp;&nbsp;§18.31 Phase 13 — Full observability, audit, `lenny-backup`, compliance webhooks | 1605 | BUILD-PLAN.md | Whole section leaves. |
| &nbsp;&nbsp;§18.32 Phase 13.5 — Pre-hardening full-system load baseline | 216 | BUILD-PLAN.md | Whole section leaves. |
| &nbsp;&nbsp;§18.33 Phase 14 — Comprehensive security hardening | 431 | BUILD-PLAN.md | Whole section leaves. |
| &nbsp;&nbsp;§18.34 Phase 14.5 — Post-hardening SLO re-validation | 46 | BUILD-PLAN.md | Whole section leaves. |
| &nbsp;&nbsp;§18.35 Phase 15 — Environment resource, RBAC, cross-environment delegation | 117 | BUILD-PLAN.md | Whole section leaves. |
| &nbsp;&nbsp;§18.36 Phase 16 — Experiments, PoolScalingController integration | 130 | BUILD-PLAN.md | Whole section leaves. |
| &nbsp;&nbsp;§18.37 Phase 16.5 — Experiment load test SLO re-validation | 36 | BUILD-PLAN.md | Whole section leaves. |
| &nbsp;&nbsp;§18.38 Phase 17a — Documentation, governance, community launch | 744 | BUILD-PLAN.md | Whole section leaves. |
| &nbsp;&nbsp;§18.39 Phase 17b — Memory, semantic caching, eval hooks | 130 | BUILD-PLAN.md | Whole section leaves. |
| &nbsp;&nbsp;§18.40 Hard Prerequisite Chain | 456 | BUILD-PLAN.md | Whole section leaves. |

### `spec/19_resolved-decisions.md`

| Current heading | Own words | Destination | Split note |
| :-- | --: | :-- | :-- |
| §19 Resolved Decisions | 688 | archive | Summary table of decisions; the designs and proposals are the decision records (decision-records-design), and each current rationale moves to its owner page's Rationale section. |

### `spec/20_open-questions.md`

| Current heading | Own words | Destination | Split note |
| :-- | --: | :-- | :-- |
| §20 Open Questions | 11 | issues |  |

### `spec/21_planned-post-v1.md`

| Current heading | Own words | Destination | Split note |
| :-- | --: | :-- | :-- |
| §21 Planned / Post-V1 | 1955 | ROADMAP.md |  |

### `spec/22_explicit-non-decisions.md`

| Current heading | Own words | Destination | Split note |
| :-- | --: | :-- | :-- |
| §22 Explicit Non-Decisions | 373 | PRIN | Decisions left to deployers → PRIN. |

### `spec/23_competitive-landscape.md`

| Current heading | Own words | Destination | Split note |
| :-- | --: | :-- | :-- |
| §23 Competitive Landscape | 553 | docs/about | Whole section leaves. |
| &nbsp;&nbsp;&nbsp;&nbsp;§23 Competitive Landscape › Feature Comparison Matrix | 509 | docs/about | Whole section leaves. |
| &nbsp;&nbsp;&nbsp;&nbsp;§2026 Open-Source Entrants Matrix | 728 | docs/about | Whole section leaves. |
| &nbsp;&nbsp;§23.1 Why Lenny? | 645 | docs/about | Whole section leaves. |
| &nbsp;&nbsp;&nbsp;&nbsp;§23.1 Why Lenny? › Additional Platform Capabilities | 774 | docs/about | Whole section leaves. |
| &nbsp;&nbsp;§23.2 Community Adoption Strategy | 664 | docs/about | Whole section leaves. |

### `spec/24_lenny-ctl-command-reference.md`

| Current heading | Own words | Destination | Split note |
| :-- | --: | :-- | :-- |
| §24 `lenny-ctl` Command Reference | 417 | CLI |  |
| &nbsp;&nbsp;§24.0 Packaging and Installation | 252 | CLIINST |  |
| &nbsp;&nbsp;§24.1 Bootstrap | 59 | CLIINST |  |
| &nbsp;&nbsp;§24.2 Preflight | 452 | CLIINST |  |
| &nbsp;&nbsp;§24.3 Runtime Management | 124 | CLIADM |  |
| &nbsp;&nbsp;§24.4 Pool Management | 597 | CLIADM |  |
| &nbsp;&nbsp;§24.5 Credential Management | 381 | CLIADM |  |
| &nbsp;&nbsp;§24.6 Quota Operations | 36 | CLIADM |  |
| &nbsp;&nbsp;§24.7 Circuit Breakers | 108 | CLIADM |  |
| &nbsp;&nbsp;§24.8 External Adapter Management | 91 | removed | External adapters removed (RO-11). |
| &nbsp;&nbsp;§24.9 User and Token Management | 239 | CLIADM |  |
| &nbsp;&nbsp;§24.10 Tenant Management | 241 | CLIADM |  |
| &nbsp;&nbsp;§24.11 Session Investigation | 76 | CLIOPS |  |
| &nbsp;&nbsp;§24.12 Erasure Job Management | 134 | CLIADM |  |
| &nbsp;&nbsp;§24.13 Migration Management | 346 | CLIOPS |  |
| &nbsp;&nbsp;§24.14 Policy Management | 86 | CLIADM |  |
| &nbsp;&nbsp;§24.15 Agent-Operability Commands | 365 | CLIOPS |  |
| &nbsp;&nbsp;§24.16 Server Discovery and Routing | 187 | CLI |  |
| &nbsp;&nbsp;§24.17 Session Operations | 351 | CLIADM |  |
| &nbsp;&nbsp;§24.18 Runtime Scaffolding | 899 | CLISCAF |  |
| &nbsp;&nbsp;§24.19 Local Stack | 218 | CLIINST |  |
| &nbsp;&nbsp;&nbsp;&nbsp;§24.19.1 Image Management | 466 | CLIINST |  |
| &nbsp;&nbsp;§24.20 Installation Wizard | 276 | CLIINST |  |

### `spec/25_agent-operability.md`

| Current heading | Own words | Destination | Split note |
| :-- | --: | :-- | :-- |
| §25 Agent Operability | 60 | OPSAREA |  |
| §25.1 Design Philosophy and Agent Model | 0 | None (container) |  |
| &nbsp;&nbsp;§25.1 Design Philosophy and Agent Model › Component Model | 229 | ARCH 50%, OPSAREA 50% |  |
| &nbsp;&nbsp;§25.1 Design Philosophy and Agent Model › The Operational Loop | 129 | OPSAREA |  |
| &nbsp;&nbsp;§25.1 Design Philosophy and Agent Model › The Bootstrap Problem and External Agent Model | 242 | OPSAREA 50%, PRIN 50% |  |
| &nbsp;&nbsp;§25.1 Design Philosophy and Agent Model › Design Constraints | 234 | PRIN 50%, OPSAREA 50% |  |
| &nbsp;&nbsp;§25.1 Design Philosophy and Agent Model › Authentication for Agent Callers | 58 | AUTHN |  |
| &nbsp;&nbsp;&nbsp;&nbsp;§25.1 Design Philosophy and Agent Model › Authentication for Agent Callers › Scoped Tokens | 724 | AUTHZ 60%, TOKX 40% |  |
| &nbsp;&nbsp;§25.1 Design Philosophy and Agent Model › Agent Identity and Correlation | 105 | APICONV 50%, AUDIT 50% |  |
| §25.2 Architecture Overview | 0 | None (container) |  |
| &nbsp;&nbsp;§25.2 Architecture Overview › The Split | 222 | ARCH 60%, LOPS 40% |  |
| &nbsp;&nbsp;§25.2 Architecture Overview › Why This Boundary | 197 | LOPS | Rationale for the gateway and lenny-ops boundary. |
| &nbsp;&nbsp;§25.2 Architecture Overview › API Conventions | 29 | OPSAPI |  |
| &nbsp;&nbsp;&nbsp;&nbsp;§25.2 Architecture Overview › API Conventions › Terminology | 106 | OPSAPI |  |
| &nbsp;&nbsp;&nbsp;&nbsp;§25.2 Architecture Overview › API Conventions › Canonical Degradation Envelope | 367 | ERR 50%, APICONV 50% |  |
| &nbsp;&nbsp;&nbsp;&nbsp;§25.2 Architecture Overview › API Conventions › Pagination | 249 | APICONV |  |
| &nbsp;&nbsp;&nbsp;&nbsp;§25.2 Architecture Overview › API Conventions › Dry-Run / Confirm Pattern | 175 | APICONV |  |
| &nbsp;&nbsp;&nbsp;&nbsp;§25.2 Architecture Overview › API Conventions › Error Response Envelope | 206 | ERR |  |
| &nbsp;&nbsp;&nbsp;&nbsp;§25.2 Architecture Overview › API Conventions › Filter Parameter Naming | 93 | APICONV |  |
| &nbsp;&nbsp;&nbsp;&nbsp;§25.2 Architecture Overview › API Conventions › Operation Correlation | 60 | APICONV |  |
| &nbsp;&nbsp;&nbsp;&nbsp;§25.2 Architecture Overview › API Conventions › Canonical Progress Envelope | 418 | APICONV 50%, OPSREM 50% |  |
| §25.3 Gateway-Side Ops Endpoints | 40 | GWOPS |  |
| &nbsp;&nbsp;§25.3 Gateway-Side Ops Endpoints › Platform Health API | 39 | DIAG |  |
| &nbsp;&nbsp;&nbsp;&nbsp;§25.3 Gateway-Side Ops Endpoints › Platform Health API › Endpoints | 44 | OPSHEALTH | (subsection rule) Interface fact under the DIAG/GWOPS/OPSHEALTH split of this subsection family. |
| &nbsp;&nbsp;&nbsp;&nbsp;§25.3 Gateway-Side Ops Endpoints › Platform Health API › Go Interface | 21 | code docs | (subsection rule) Go signature; package documentation, or the component internal parts where a reader needs it. |
| &nbsp;&nbsp;&nbsp;&nbsp;§25.3 Gateway-Side Ops Endpoints › Platform Health API › Data Sources | 103 | GWOPS | (subsection rule) Unit fact (implementation, storage, metrics) → the component. |
| &nbsp;&nbsp;&nbsp;&nbsp;§25.3 Gateway-Side Ops Endpoints › Platform Health API › Health Derivation Rules | 188 | DIAG |  |
| &nbsp;&nbsp;&nbsp;&nbsp;§25.3 Gateway-Side Ops Endpoints › Platform Health API › `suggestedAction` / `suggestedActions` Contract | 388 | OPSHEALTH |  |
| &nbsp;&nbsp;&nbsp;&nbsp;§25.3 Gateway-Side Ops Endpoints › Platform Health API › Caching | 29 | GWOPS | (subsection rule) Unit fact (implementation, storage, metrics) → the component. |
| &nbsp;&nbsp;&nbsp;&nbsp;§25.3 Gateway-Side Ops Endpoints › Platform Health API › Degradation | 47 | DIAG | (subsection rule) Behavioral rule or behavior under failure → the capability. |
| &nbsp;&nbsp;&nbsp;&nbsp;§25.3 Gateway-Side Ops Endpoints › Platform Health API › Storage | 6 | GWOPS 50%, STORPG 50% | (subsection rule) Storage mechanism → component; table definitions → STORPG. |
| &nbsp;&nbsp;&nbsp;&nbsp;§25.3 Gateway-Side Ops Endpoints › Platform Health API › Metrics | 33 | GWOPS | (subsection rule) Unit fact (implementation, storage, metrics) → the component. |
| &nbsp;&nbsp;&nbsp;&nbsp;§25.3 Gateway-Side Ops Endpoints › Platform Health API › Error Codes | 24 | OPSHEALTH | (subsection rule) Interface fact under the DIAG/GWOPS/OPSHEALTH split of this subsection family. |
| &nbsp;&nbsp;§25.3 Gateway-Side Ops Endpoints › Capacity Recommendations | 14 | DIAG |  |
| &nbsp;&nbsp;&nbsp;&nbsp;§25.3 Gateway-Side Ops Endpoints › Capacity Recommendations › Endpoint | 19 | OPSHEALTH | (subsection rule) Interface fact under the DIAG/GWOPS/OPSHEALTH split of this subsection family. |
| &nbsp;&nbsp;&nbsp;&nbsp;§25.3 Gateway-Side Ops Endpoints › Capacity Recommendations › Go Interface | 46 | code docs | (subsection rule) Go signature; package documentation, or the component internal parts where a reader needs it. |
| &nbsp;&nbsp;&nbsp;&nbsp;§25.3 Gateway-Side Ops Endpoints › Capacity Recommendations › Rules Engine | 532 | DIAG |  |
| &nbsp;&nbsp;&nbsp;&nbsp;§25.3 Gateway-Side Ops Endpoints › Capacity Recommendations › Degradation | 116 | DIAG | (subsection rule) Behavioral rule or behavior under failure → the capability. |
| &nbsp;&nbsp;&nbsp;&nbsp;§25.3 Gateway-Side Ops Endpoints › Capacity Recommendations › Storage | 7 | GWOPS 50%, STORPG 50% | (subsection rule) Storage mechanism → component; table definitions → STORPG. |
| &nbsp;&nbsp;&nbsp;&nbsp;§25.3 Gateway-Side Ops Endpoints › Capacity Recommendations › Metrics | 21 | GWOPS | (subsection rule) Unit fact (implementation, storage, metrics) → the component. |
| &nbsp;&nbsp;&nbsp;&nbsp;§25.3 Gateway-Side Ops Endpoints › Capacity Recommendations › Error Codes | 38 | OPSHEALTH | (subsection rule) Interface fact under the DIAG/GWOPS/OPSHEALTH split of this subsection family. |
| &nbsp;&nbsp;§25.3 Gateway-Side Ops Endpoints › Version and Config Introspection | 7 | OPSLIFE |  |
| &nbsp;&nbsp;&nbsp;&nbsp;§25.3 Gateway-Side Ops Endpoints › Version and Config Introspection › Endpoints | 89 | OPSLIFE | (subsection rule) Interface fact under the UPGRADE/GWOPS/OPSLIFE split of this subsection family. |
| &nbsp;&nbsp;&nbsp;&nbsp;§25.3 Gateway-Side Ops Endpoints › Version and Config Introspection › Storage | 1 | GWOPS 50%, STORPG 50% | (subsection rule) Storage mechanism → component; table definitions → STORPG. |
| &nbsp;&nbsp;§25.3 Gateway-Side Ops Endpoints › Event Emission | 384 | OPSEV |  |
| &nbsp;&nbsp;&nbsp;&nbsp;§25.3 Gateway-Side Ops Endpoints › Event Emission › Event Types | 314 | EVTOPS |  |
| &nbsp;&nbsp;&nbsp;&nbsp;§25.3 Gateway-Side Ops Endpoints › Event Emission › Metrics | 37 | GWOPS | (subsection rule) Unit fact (implementation, storage, metrics) → the component. |
| &nbsp;&nbsp;§25.3 Gateway-Side Ops Endpoints › Gateway Event Buffer | 38 | OPSEV |  |
| &nbsp;&nbsp;&nbsp;&nbsp;§25.3 Gateway-Side Ops Endpoints › Gateway Event Buffer › Endpoint | 28 | OPSEVT | (subsection rule) Interface fact under the OPSEV/GWOPS/OPSEVT split of this subsection family. |
| &nbsp;&nbsp;&nbsp;&nbsp;§25.3 Gateway-Side Ops Endpoints › Gateway Event Buffer › Go Interface | 80 | code docs | (subsection rule) Go signature; package documentation, or the component internal parts where a reader needs it. |
| &nbsp;&nbsp;&nbsp;&nbsp;§25.3 Gateway-Side Ops Endpoints › Gateway Event Buffer › Behavior | 375 | OPSEV |  |
| &nbsp;&nbsp;&nbsp;&nbsp;§25.3 Gateway-Side Ops Endpoints › Gateway Event Buffer › Degradation | 14 | OPSEV | (subsection rule) Behavioral rule or behavior under failure → the capability. |
| &nbsp;&nbsp;&nbsp;&nbsp;§25.3 Gateway-Side Ops Endpoints › Gateway Event Buffer › Storage | 5 | GWOPS 50%, STORPG 50% | (subsection rule) Storage mechanism → component; table definitions → STORPG. |
| &nbsp;&nbsp;&nbsp;&nbsp;§25.3 Gateway-Side Ops Endpoints › Gateway Event Buffer › Metrics | 45 | GWOPS | (subsection rule) Unit fact (implementation, storage, metrics) → the component. |
| §25.4 The `lenny-ops` Service | 0 | None (container) |  |
| &nbsp;&nbsp;§25.4 The `lenny-ops` Service › Purpose | 66 | LOPS |  |
| &nbsp;&nbsp;§25.4 The `lenny-ops` Service › Deployment | 33 | LOPS |  |
| &nbsp;&nbsp;&nbsp;&nbsp;§25.4 The `lenny-ops` Service › Deployment › Helm Values Hierarchy | 149 | DCFG |  |
| &nbsp;&nbsp;&nbsp;&nbsp;§25.4 The `lenny-ops` Service › Deployment › `lenny-ops` Helm Values | 1284 | LOPSK8S |  |
| &nbsp;&nbsp;§25.4 The `lenny-ops` Service › Kubernetes Resources | 30 | LOPSK8S |  |
| &nbsp;&nbsp;&nbsp;&nbsp;§25.4 The `lenny-ops` Service › Kubernetes Resources › Deployment: `lenny-ops` | 56 | LOPS |  |
| &nbsp;&nbsp;&nbsp;&nbsp;§25.4 The `lenny-ops` Service › Kubernetes Resources › Probes | 137 | LOPS |  |
| &nbsp;&nbsp;&nbsp;&nbsp;§25.4 The `lenny-ops` Service › Kubernetes Resources › Services | 71 | LOPSK8S |  |
| &nbsp;&nbsp;&nbsp;&nbsp;§25.4 The `lenny-ops` Service › Kubernetes Resources › ServiceAccount and RBAC | 218 | LOPSK8S |  |
| &nbsp;&nbsp;&nbsp;&nbsp;§25.4 The `lenny-ops` Service › Kubernetes Resources › NetworkPolicy | 2113 | LOPSK8S |  |
| &nbsp;&nbsp;&nbsp;&nbsp;§25.4 The `lenny-ops` Service › Kubernetes Resources › PodDisruptionBudget | 78 | LOPSK8S |  |
| &nbsp;&nbsp;&nbsp;&nbsp;§25.4 The `lenny-ops` Service › Kubernetes Resources › Leader Election | 115 | LOPS |  |
| &nbsp;&nbsp;&nbsp;&nbsp;§25.4 The `lenny-ops` Service › Kubernetes Resources › Backup Job NetworkPolicy | 508 | BKJOB |  |
| &nbsp;&nbsp;&nbsp;&nbsp;§25.4 The `lenny-ops` Service › Kubernetes Resources › CNI / Service Mesh Compatibility | 100 | LOPS 50%, NETISO 50% |  |
| &nbsp;&nbsp;&nbsp;&nbsp;§25.4 The `lenny-ops` Service › Kubernetes Resources › Air-Gapped Deployments | 95 | LOPS 50%, INSTALL 50% |  |
| &nbsp;&nbsp;§25.4 The `lenny-ops` Service › Dependencies | 215 | LOPS |  |
| &nbsp;&nbsp;§25.4 The `lenny-ops` Service › Prometheus Requirement | 36 | PROM 60%, LOPSAGG 40% |  |
| &nbsp;&nbsp;&nbsp;&nbsp;§25.4 The `lenny-ops` Service › Prometheus Requirement › Why required at Tier 2/3 | 288 | PROM |  |
| &nbsp;&nbsp;&nbsp;&nbsp;§25.4 The `lenny-ops` Service › Prometheus Requirement › Why optional at Tier 1 | 70 | PROM |  |
| &nbsp;&nbsp;&nbsp;&nbsp;§25.4 The `lenny-ops` Service › Prometheus Requirement › BYO model | 94 | PROM |  |
| &nbsp;&nbsp;&nbsp;&nbsp;§25.4 The `lenny-ops` Service › Prometheus Requirement › Preflight validation | 148 | PREFLT |  |
| &nbsp;&nbsp;&nbsp;&nbsp;§25.4 The `lenny-ops` Service › Prometheus Requirement › Operational consequences summary | 207 | LOPSAGG |  |
| &nbsp;&nbsp;§25.4 The `lenny-ops` Service › Storage Routing | 448 | LOPS 50%, PG 50% |  |
| &nbsp;&nbsp;§25.4 The `lenny-ops` Service › Multi-Replica Lenny-Ops Scaling | 42 | LOPS |  |
| &nbsp;&nbsp;&nbsp;&nbsp;§25.4 The `lenny-ops` Service › Multi-Replica Lenny-Ops Scaling › When to Scale | 139 | operator guide |  |
| &nbsp;&nbsp;&nbsp;&nbsp;§25.4 The `lenny-ops` Service › Multi-Replica Lenny-Ops Scaling › Recommended Scale by Tier | 66 | DCFGTIER |  |
| &nbsp;&nbsp;&nbsp;&nbsp;§25.4 The `lenny-ops` Service › Multi-Replica Lenny-Ops Scaling › Trade-Offs | 189 | LOPS |  |
| &nbsp;&nbsp;&nbsp;&nbsp;§25.4 The `lenny-ops` Service › Multi-Replica Lenny-Ops Scaling › Operator Decision Checklist | 96 | operator guide |  |
| &nbsp;&nbsp;§25.4 The `lenny-ops` Service › Startup and Readiness | 86 | LOPS |  |
| &nbsp;&nbsp;§25.4 The `lenny-ops` Service › Authentication | 387 | AUTHN 50%, LOPS 50% |  |
| &nbsp;&nbsp;§25.4 The `lenny-ops` Service › Caller Identity and Capability Discovery | 49 | AUTHZ |  |
| &nbsp;&nbsp;&nbsp;&nbsp;§25.4 The `lenny-ops` Service › Caller Identity and Capability Discovery › Endpoints | 76 | OPSME | (subsection rule) Interface fact under the AUTHZ/LOPSSVC/OPSME split of this subsection family. |
| &nbsp;&nbsp;&nbsp;&nbsp;§25.4 The `lenny-ops` Service › Caller Identity and Capability Discovery › `GET /v1/admin/me` Response | 343 | OPSME |  |
| &nbsp;&nbsp;&nbsp;&nbsp;§25.4 The `lenny-ops` Service › Caller Identity and Capability Discovery › Authorization | 35 | OPSME | (subsection rule) Interface fact under the AUTHZ/LOPSSVC/OPSME split of this subsection family. |
| &nbsp;&nbsp;&nbsp;&nbsp;§25.4 The `lenny-ops` Service › Caller Identity and Capability Discovery › Degradation | 105 | AUTHZ | (subsection rule) Behavioral rule or behavior under failure → the capability. |
| &nbsp;&nbsp;&nbsp;&nbsp;§25.4 The `lenny-ops` Service › Caller Identity and Capability Discovery › Metrics | 23 | LOPSSVC | (subsection rule) Unit fact (implementation, storage, metrics) → the component. |
| &nbsp;&nbsp;&nbsp;&nbsp;§25.4 The `lenny-ops` Service › Caller Identity and Capability Discovery › Error Codes | 30 | OPSME | (subsection rule) Interface fact under the AUTHZ/LOPSSVC/OPSME split of this subsection family. |
| &nbsp;&nbsp;&nbsp;&nbsp;§25.4 The `lenny-ops` Service › Caller Identity and Capability Discovery › Audit Events | 23 | EVTAUD | (subsection rule) Audit event schemas → EVTAUD; the emitting operation links its owner capability. |
| &nbsp;&nbsp;§25.4 The `lenny-ops` Service › Operations Inventory | 61 | OPSREM |  |
| &nbsp;&nbsp;&nbsp;&nbsp;§25.4 The `lenny-ops` Service › Operations Inventory › Endpoints | 84 | OPSREM | (subsection rule) Interface fact under the REMED/LOPSSVC/OPSREM split of this subsection family. |
| &nbsp;&nbsp;&nbsp;&nbsp;§25.4 The `lenny-ops` Service › Operations Inventory › Operation Kinds | 107 | OPSREM |  |
| &nbsp;&nbsp;&nbsp;&nbsp;§25.4 The `lenny-ops` Service › Operations Inventory › Response | 138 | OPSREM | (subsection rule) Interface fact under the REMED/LOPSSVC/OPSREM split of this subsection family. |
| &nbsp;&nbsp;&nbsp;&nbsp;§25.4 The `lenny-ops` Service › Operations Inventory › Canonical `operationId` Format | 81 | OPSREM |  |
| &nbsp;&nbsp;&nbsp;&nbsp;§25.4 The `lenny-ops` Service › Operations Inventory › Filters | 142 | OPSREM | (subsection rule) Interface fact under the REMED/LOPSSVC/OPSREM split of this subsection family. |
| &nbsp;&nbsp;&nbsp;&nbsp;§25.4 The `lenny-ops` Service › Operations Inventory › Status Values | 93 | OPSREM | (subsection rule) Interface fact under the REMED/LOPSSVC/OPSREM split of this subsection family. |
| &nbsp;&nbsp;&nbsp;&nbsp;§25.4 The `lenny-ops` Service › Operations Inventory › Event Subscription | 34 | OPSREM | (subsection rule) Interface fact under the REMED/LOPSSVC/OPSREM split of this subsection family. |
| &nbsp;&nbsp;&nbsp;&nbsp;§25.4 The `lenny-ops` Service › Operations Inventory › Implementation | 35 | LOPSSVC | (subsection rule) Unit fact (implementation, storage, metrics) → the component. |
| &nbsp;&nbsp;&nbsp;&nbsp;§25.4 The `lenny-ops` Service › Operations Inventory › Degradation | 82 | REMED | (subsection rule) Behavioral rule or behavior under failure → the capability. |
| &nbsp;&nbsp;&nbsp;&nbsp;§25.4 The `lenny-ops` Service › Operations Inventory › Authorization | 101 | OPSREM | (subsection rule) Interface fact under the REMED/LOPSSVC/OPSREM split of this subsection family. |
| &nbsp;&nbsp;&nbsp;&nbsp;§25.4 The `lenny-ops` Service › Operations Inventory › Metrics | 47 | LOPSSVC | (subsection rule) Unit fact (implementation, storage, metrics) → the component. |
| &nbsp;&nbsp;&nbsp;&nbsp;§25.4 The `lenny-ops` Service › Operations Inventory › Error Codes | 45 | OPSREM | (subsection rule) Interface fact under the REMED/LOPSSVC/OPSREM split of this subsection family. |
| &nbsp;&nbsp;&nbsp;&nbsp;§25.4 The `lenny-ops` Service › Operations Inventory › Audit Events | 18 | EVTAUD | (subsection rule) Audit event schemas → EVTAUD; the emitting operation links its owner capability. |
| &nbsp;&nbsp;§25.4 The `lenny-ops` Service › Calling the Gateway | 368 | LOPSAGG |  |
| &nbsp;&nbsp;§25.4 The `lenny-ops` Service › Metrics Source and Cross-Replica Aggregation | 95 | LOPSAGG |  |
| &nbsp;&nbsp;&nbsp;&nbsp;§25.4 The `lenny-ops` Service › Metrics Source and Cross-Replica Aggregation › "Prometheus Unreachable" Definition | 124 | LOPSAGG |  |
| &nbsp;&nbsp;&nbsp;&nbsp;§25.4 The `lenny-ops` Service › Metrics Source and Cross-Replica Aggregation › Fallback Caching | 205 | LOPSAGG |  |
| &nbsp;&nbsp;&nbsp;&nbsp;§25.4 The `lenny-ops` Service › Metrics Source and Cross-Replica Aggregation › Health Aggregation | 235 | DIAG 50%, LOPSAGG 50% |  |
| &nbsp;&nbsp;&nbsp;&nbsp;§25.4 The `lenny-ops` Service › Metrics Source and Cross-Replica Aggregation › Recommendations Aggregation | 331 | DIAG 50%, LOPSAGG 50% |  |
| &nbsp;&nbsp;&nbsp;&nbsp;§25.4 The `lenny-ops` Service › Metrics Source and Cross-Replica Aggregation › Pool Bottleneck Analysis (Diagnostics) | 55 | DIAG |  |
| &nbsp;&nbsp;&nbsp;&nbsp;§25.4 The `lenny-ops` Service › Metrics Source and Cross-Replica Aggregation › OIDC Token Lifecycle (GatewayClient) | 190 | LOPSAGG |  |
| &nbsp;&nbsp;§25.4 The `lenny-ops` Service › Rate Limiting | 90 | OPSAPI |  |
| &nbsp;&nbsp;§25.4 The `lenny-ops` Service › Idempotency | 274 | APICONV 50%, LOPSSVC 50% |  |
| &nbsp;&nbsp;&nbsp;&nbsp;§25.4 The `lenny-ops` Service › Idempotency › Behavior during Postgres outage | 418 | LOPSSVC |  |
| &nbsp;&nbsp;&nbsp;&nbsp;§25.4 The `lenny-ops` Service › Idempotency › Retention | 43 | LOPSSVC |  |
| &nbsp;&nbsp;&nbsp;&nbsp;§25.4 The `lenny-ops` Service › Idempotency › Error Codes | 97 | OPSAPI |  |
| &nbsp;&nbsp;§25.4 The `lenny-ops` Service › Remediation Coordination | 53 | REMED |  |
| &nbsp;&nbsp;&nbsp;&nbsp;§25.4 The `lenny-ops` Service › Remediation Coordination › Consistency Model | 165 | REMED |  |
| &nbsp;&nbsp;&nbsp;&nbsp;§25.4 The `lenny-ops` Service › Remediation Coordination › Endpoints | 107 | OPSREM | (subsection rule) Interface fact under the REMED/LOPSSVC/OPSREM split of this subsection family. |
| &nbsp;&nbsp;&nbsp;&nbsp;§25.4 The `lenny-ops` Service › Remediation Coordination › Authorization | 267 | OPSREM | (subsection rule) Interface fact under the REMED/LOPSSVC/OPSREM split of this subsection family. |
| &nbsp;&nbsp;&nbsp;&nbsp;§25.4 The `lenny-ops` Service › Remediation Coordination › Lock Struct | 116 | OPSREM |  |
| &nbsp;&nbsp;&nbsp;&nbsp;§25.4 The `lenny-ops` Service › Remediation Coordination › Storage Tiers | 860 | LOPSSVC | (subsection rule) Unit fact (implementation, storage, metrics) → the component. |
| &nbsp;&nbsp;&nbsp;&nbsp;§25.4 The `lenny-ops` Service › Remediation Coordination › Tier Transitions and Split-Brain Prevention | 835 | REMED 50%, LOPSSVC 50% | The exclusivity guarantee → REMED; the tier mechanism → LOPS. |
| &nbsp;&nbsp;&nbsp;&nbsp;§25.4 The `lenny-ops` Service › Remediation Coordination › Clock Source | 102 | LOPSSVC | (subsection rule) Unit fact (implementation, storage, metrics) → the component. |
| &nbsp;&nbsp;&nbsp;&nbsp;§25.4 The `lenny-ops` Service › Remediation Coordination › Stealing | 111 | REMED |  |
| &nbsp;&nbsp;&nbsp;&nbsp;§25.4 The `lenny-ops` Service › Remediation Coordination › Holding a Lock Across Long Remediations | 259 | REMED |  |
| &nbsp;&nbsp;&nbsp;&nbsp;§25.4 The `lenny-ops` Service › Remediation Coordination › Multi-Replica Behavior | 85 | LOPSSVC | (subsection rule) Unit fact (implementation, storage, metrics) → the component. |
| &nbsp;&nbsp;&nbsp;&nbsp;§25.4 The `lenny-ops` Service › Remediation Coordination › Error Codes | 132 | OPSREM | (subsection rule) Interface fact under the REMED/LOPSSVC/OPSREM split of this subsection family. |
| &nbsp;&nbsp;&nbsp;&nbsp;§25.4 The `lenny-ops` Service › Remediation Coordination › Metrics | 76 | LOPSSVC | (subsection rule) Unit fact (implementation, storage, metrics) → the component. |
| &nbsp;&nbsp;&nbsp;&nbsp;§25.4 The `lenny-ops` Service › Remediation Coordination › Audit Events | 22 | EVTAUD | (subsection rule) Audit event schemas → EVTAUD; the emitting operation links its owner capability. |
| &nbsp;&nbsp;§25.4 The `lenny-ops` Service › Escalation | 55 | REMED |  |
| &nbsp;&nbsp;&nbsp;&nbsp;§25.4 The `lenny-ops` Service › Escalation › Endpoints | 41 | OPSREM | (subsection rule) Interface fact under the REMED/LOPSSVC/OPSREM split of this subsection family. |
| &nbsp;&nbsp;&nbsp;&nbsp;§25.4 The `lenny-ops` Service › Escalation › Behavior | 153 | REMED | (subsection rule) Behavioral rule or behavior under failure → the capability. |
| &nbsp;&nbsp;&nbsp;&nbsp;§25.4 The `lenny-ops` Service › Escalation › Storage Tiers (Create Path) | 201 | LOPSSVC | (subsection rule) Unit fact (implementation, storage, metrics) → the component. |
| &nbsp;&nbsp;&nbsp;&nbsp;§25.4 The `lenny-ops` Service › Escalation › Emission Exactly-Once | 122 | REMED |  |
| &nbsp;&nbsp;&nbsp;&nbsp;§25.4 The `lenny-ops` Service › Escalation › Reconciliation | 159 | LOPSSVC |  |
| &nbsp;&nbsp;&nbsp;&nbsp;§25.4 The `lenny-ops` Service › Escalation › Storage Tiers (Query Path) | 103 | LOPSSVC | (subsection rule) Unit fact (implementation, storage, metrics) → the component. |
| &nbsp;&nbsp;&nbsp;&nbsp;§25.4 The `lenny-ops` Service › Escalation › Event Emission | 102 | EVTOPS |  |
| &nbsp;&nbsp;&nbsp;&nbsp;§25.4 The `lenny-ops` Service › Escalation › Storage | 133 | STORPG |  |
| &nbsp;&nbsp;&nbsp;&nbsp;§25.4 The `lenny-ops` Service › Escalation › Error Codes | 42 | OPSREM | (subsection rule) Interface fact under the REMED/LOPSSVC/OPSREM split of this subsection family. |
| &nbsp;&nbsp;§25.4 The `lenny-ops` Service › Self-Monitoring | 64 | LOPS |  |
| &nbsp;&nbsp;&nbsp;&nbsp;§25.4 The `lenny-ops` Service › Self-Monitoring › Self-Health Checks | 414 | LOPS |  |
| &nbsp;&nbsp;&nbsp;&nbsp;§25.4 The `lenny-ops` Service › Self-Monitoring › Metrics | 57 | LOPS |  |
| &nbsp;&nbsp;§25.4 The `lenny-ops` Service › Structured Logging | 131 | OBSCONV 50%, LOPS 50% |  |
| &nbsp;&nbsp;§25.4 The `lenny-ops` Service › TLS | 455 | PKI 50%, LOPS 50% |  |
| §25.5 Operational Event Stream | 184 | OPSEV |  |
| &nbsp;&nbsp;§25.5 Operational Event Stream › Endpoints | 184 | OPSEVT | (subsection rule) Interface fact under the OPSEV/LOPSSVC/OPSEVT split of this subsection family. |
| &nbsp;&nbsp;§25.5 Operational Event Stream › Go Interface | 207 | code docs | (subsection rule) Go signature; package documentation, or the component internal parts where a reader needs it. |
| &nbsp;&nbsp;§25.5 Operational Event Stream › Storage | 464 | LOPSSVC 50%, STORPG 50% | (subsection rule) Storage mechanism → component; table definitions → STORPG. |
| &nbsp;&nbsp;&nbsp;&nbsp;§25.5 Operational Event Stream › Storage › Delivery Tracking Retention | 150 | LOPSSVC | (subsection rule) Unit fact (implementation, storage, metrics) → the component. |
| &nbsp;&nbsp;§25.5 Operational Event Stream › Cursor Model | 157 | OPSEV 50%, OPSEVT 50% |  |
| &nbsp;&nbsp;§25.5 Operational Event Stream › SSE Delivery | 260 | OPSEV 50%, OPSEVT 50% |  |
| &nbsp;&nbsp;§25.5 Operational Event Stream › Polling Delivery | 176 | OPSEV 50%, OPSEVT 50% |  |
| &nbsp;&nbsp;§25.5 Operational Event Stream › Webhook Delivery | 168 | OPSEV 50%, OPSEVT 50% |  |
| &nbsp;&nbsp;&nbsp;&nbsp;§25.5 Operational Event Stream › Webhook Delivery › Webhook Secret Lifecycle | 218 | OPSEV |  |
| &nbsp;&nbsp;&nbsp;&nbsp;§25.5 Operational Event Stream › Webhook Delivery › SSRF and DNS Rebinding Protections | 207 | OPSEV |  |
| &nbsp;&nbsp;&nbsp;&nbsp;§25.5 Operational Event Stream › Webhook Delivery › Subscription Cache and Invalidation | 213 | LOPSSVC | (subsection rule) Unit fact (implementation, storage, metrics) → the component. |
| &nbsp;&nbsp;&nbsp;&nbsp;§25.5 Operational Event Stream › Webhook Delivery › Tenant Isolation | 132 | OPSEV |  |
| &nbsp;&nbsp;§25.5 Operational Event Stream › Degradation | 436 | OPSEV | (subsection rule) Behavioral rule or behavior under failure → the capability. |
| &nbsp;&nbsp;§25.5 Operational Event Stream › Metrics | 74 | LOPSSVC | (subsection rule) Unit fact (implementation, storage, metrics) → the component. |
| &nbsp;&nbsp;§25.5 Operational Event Stream › Error Codes | 102 | OPSEVT | (subsection rule) Interface fact under the OPSEV/LOPSSVC/OPSEVT split of this subsection family. |
| &nbsp;&nbsp;§25.5 Operational Event Stream › Audit Events | 4 | EVTAUD | (subsection rule) Audit event schemas → EVTAUD; the emitting operation links its owner capability. |
| §25.6 Diagnostic Endpoints | 25 | DIAG |  |
| &nbsp;&nbsp;§25.6 Diagnostic Endpoints › Endpoints | 48 | OPSHEALTH | (subsection rule) Interface fact under the DIAG/LOPSSVC/OPSHEALTH split of this subsection family. |
| &nbsp;&nbsp;§25.6 Diagnostic Endpoints › Go Interface | 31 | code docs | (subsection rule) Go signature; package documentation, or the component internal parts where a reader needs it. |
| &nbsp;&nbsp;§25.6 Diagnostic Endpoints › Response Types | 174 | OPSHEALTH | (subsection rule) Interface fact under the DIAG/LOPSSVC/OPSHEALTH split of this subsection family. |
| &nbsp;&nbsp;§25.6 Diagnostic Endpoints › Data Sources | 410 | LOPSSVC | (subsection rule) Unit fact (implementation, storage, metrics) → the component. |
| &nbsp;&nbsp;§25.6 Diagnostic Endpoints › Degradation | 156 | DIAG | (subsection rule) Behavioral rule or behavior under failure → the capability. |
| &nbsp;&nbsp;§25.6 Diagnostic Endpoints › Metrics | 20 | LOPSSVC | (subsection rule) Unit fact (implementation, storage, metrics) → the component. |
| &nbsp;&nbsp;§25.6 Diagnostic Endpoints › Error Codes | 63 | OPSHEALTH | (subsection rule) Interface fact under the DIAG/LOPSSVC/OPSHEALTH split of this subsection family. |
| &nbsp;&nbsp;§25.6 Diagnostic Endpoints › Audit Events | 4 | EVTAUD | (subsection rule) Audit event schemas → EVTAUD; the emitting operation links its owner capability. |
| &nbsp;&nbsp;§25.6 Diagnostic Endpoints › Auto-remediation (`fix=true` mode) | 394 | DIAG |  |
| §25.7 Operational Runbooks | 0 | None (container) |  |
| &nbsp;&nbsp;§25.7 Operational Runbooks › Rationale | 81 | RUNBOOK |  |
| &nbsp;&nbsp;§25.7 Operational Runbooks › Runbook Authoring Guidelines | 311 | RUNBOOK 50%, contributor docs 50% |  |
| &nbsp;&nbsp;§25.7 Operational Runbooks › Step 1: Check pool status | 412 | runbooks | A heading inside an embedded example runbook, written at section level. |
| &nbsp;&nbsp;§25.7 Operational Runbooks › Runbook Discovery | 19 | RUNBOOK |  |
| &nbsp;&nbsp;&nbsp;&nbsp;§25.7 Operational Runbooks › Runbook Discovery › Path A: Index API with Rich Filtering | 298 | RUNBOOK 50%, OPSHEALTH 50% |  |
| &nbsp;&nbsp;&nbsp;&nbsp;§25.7 Operational Runbooks › Runbook Discovery › Path B: Health API Links to Runbooks | 257 | DIAG 50%, RUNBOOK 50% |  |
| &nbsp;&nbsp;&nbsp;&nbsp;§25.7 Operational Runbooks › Runbook Discovery › Path C: Fetch the Full List | 96 | RUNBOOK 50%, OPSHEALTH 50% |  |
| &nbsp;&nbsp;&nbsp;&nbsp;§25.7 Operational Runbooks › Runbook Discovery › Discovery Flow Summary | 75 | RUNBOOK |  |
| §25.8 Platform Lifecycle Management | 10 | UPGRADE |  |
| &nbsp;&nbsp;§25.8 Platform Lifecycle Management › Endpoints | 136 | OPSLIFE | (subsection rule) Interface fact under the UPGRADE/LOPSSVC/OPSLIFE split of this subsection family. |
| &nbsp;&nbsp;§25.8 Platform Lifecycle Management › Image Registry Configuration | 30 | INSTALL |  |
| &nbsp;&nbsp;&nbsp;&nbsp;§25.8 Platform Lifecycle Management › Image Registry Configuration › Helm Values | 92 | INSTALL 50%, DCFG 50% |  |
| &nbsp;&nbsp;&nbsp;&nbsp;§25.8 Platform Lifecycle Management › Image Registry Configuration › Image Resolution | 143 | INSTALL |  |
| &nbsp;&nbsp;&nbsp;&nbsp;§25.8 Platform Lifecycle Management › Image Registry Configuration › Runtime API | 40 | OPSLIFE |  |
| &nbsp;&nbsp;§25.8 Platform Lifecycle Management › Version Aggregation | 87 | UPGRADE |  |
| &nbsp;&nbsp;§25.8 Platform Lifecycle Management › Upgrade Check | 53 | UPGRADE |  |
| &nbsp;&nbsp;&nbsp;&nbsp;§25.8 Platform Lifecycle Management › Upgrade Check › Release Channel Response | 127 | OPSLIFE |  |
| &nbsp;&nbsp;&nbsp;&nbsp;§25.8 Platform Lifecycle Management › Upgrade Check › Release Channel Service Details | 224 | UPGRADE |  |
| &nbsp;&nbsp;&nbsp;&nbsp;§25.8 Platform Lifecycle Management › Upgrade Check › Air-Gapped Support | 190 | UPGRADE |  |
| &nbsp;&nbsp;§25.8 Platform Lifecycle Management › Cert-Manager Integration | 300 | CERTM |  |
| &nbsp;&nbsp;§25.8 Platform Lifecycle Management › Bootstrapping Lenny-Ops Monitoring | 255 | LOPS 50%, PROM 50% |  |
| &nbsp;&nbsp;§25.8 Platform Lifecycle Management › Upgrade State Machine | 146 | UPGRADE |  |
| &nbsp;&nbsp;&nbsp;&nbsp;§25.8 Platform Lifecycle Management › Upgrade State Machine › Phase Details | 709 | UPGRADE |  |
| &nbsp;&nbsp;&nbsp;&nbsp;§25.8 Platform Lifecycle Management › Upgrade State Machine › Rollback | 205 | UPGRADE |  |
| &nbsp;&nbsp;&nbsp;&nbsp;§25.8 Platform Lifecycle Management › Upgrade State Machine › Pausing and Resuming | 257 | UPGRADE |  |
| &nbsp;&nbsp;§25.8 Platform Lifecycle Management › Config Diff and Config Apply | 139 | UPGRADE 50%, OPSLIFE 50% |  |
| &nbsp;&nbsp;§25.8 Platform Lifecycle Management › Postgres Schema | 118 | STORPG |  |
| &nbsp;&nbsp;§25.8 Platform Lifecycle Management › Degradation | 61 | UPGRADE | (subsection rule) Behavioral rule or behavior under failure → the capability. |
| &nbsp;&nbsp;§25.8 Platform Lifecycle Management › Metrics | 63 | LOPSSVC | (subsection rule) Unit fact (implementation, storage, metrics) → the component. |
| &nbsp;&nbsp;§25.8 Platform Lifecycle Management › Alerting Rules | 43 | UPGRADE | (subsection rule) Behavioral rule or behavior under failure → the capability. |
| &nbsp;&nbsp;§25.8 Platform Lifecycle Management › Error Codes | 153 | OPSLIFE | (subsection rule) Interface fact under the UPGRADE/LOPSSVC/OPSLIFE split of this subsection family. |
| &nbsp;&nbsp;§25.8 Platform Lifecycle Management › Audit Events | 15 | EVTAUD | (subsection rule) Audit event schemas → EVTAUD; the emitting operation links its owner capability. |
| §25.9 Audit Log Query API | 117 | OPSAUD 50%, AUDIT 50% |  |
| &nbsp;&nbsp;§25.9 Audit Log Query API › Endpoints | 430 | OPSAUD | (subsection rule) Interface fact under the AUDIT/LOPSSVC/OPSAUD split of this subsection family. |
| &nbsp;&nbsp;§25.9 Audit Log Query API › Implementation | 623 | LOPSSVC | (subsection rule) Unit fact (implementation, storage, metrics) → the component. |
| &nbsp;&nbsp;§25.9 Audit Log Query API › Diagnostics Audit Rate Limiting | 139 | AUDIT |  |
| &nbsp;&nbsp;§25.9 Audit Log Query API › Query Limits and Scatter-Gather | 137 | OPSAUD 50%, LOPSSVC 50% |  |
| &nbsp;&nbsp;§25.9 Audit Log Query API › Degradation | 62 | AUDIT | (subsection rule) Behavioral rule or behavior under failure → the capability. |
| &nbsp;&nbsp;§25.9 Audit Log Query API › Metrics | 79 | LOPSSVC | (subsection rule) Unit fact (implementation, storage, metrics) → the component. |
| &nbsp;&nbsp;§25.9 Audit Log Query API › Error Codes | 73 | OPSAUD | (subsection rule) Interface fact under the AUDIT/LOPSSVC/OPSAUD split of this subsection family. |
| &nbsp;&nbsp;§25.9 Audit Log Query API › Downstream SIEM Scope Boundary (GDPR Erasure) | 311 | ERASE 50%, SIEM 50% |  |
| &nbsp;&nbsp;§25.9 Audit Log Query API › Audit Events | 25 | EVTAUD | (subsection rule) Audit event schemas → EVTAUD; the emitting operation links its owner capability. |
| §25.10 Configuration Drift Detection | 12 | DRIFT |  |
| &nbsp;&nbsp;§25.10 Configuration Drift Detection › Endpoints | 107 | OPSAUD | (subsection rule) Interface fact under the DRIFT/LOPSSVC/OPSAUD split of this subsection family. |
| &nbsp;&nbsp;§25.10 Configuration Drift Detection › Drift Detection Logic | 69 | DRIFT |  |
| &nbsp;&nbsp;&nbsp;&nbsp;§25.10 Configuration Drift Detection › Drift Detection Logic › Snapshot Validation | 833 | DRIFT |  |
| &nbsp;&nbsp;&nbsp;&nbsp;§25.10 Configuration Drift Detection › Drift Detection Logic › Running-State Caching | 47 | LOPSSVC | (subsection rule) Unit fact (implementation, storage, metrics) → the component. |
| &nbsp;&nbsp;&nbsp;&nbsp;§25.10 Configuration Drift Detection › Drift Detection Logic › Comparison Scope | 85 | DRIFT |  |
| &nbsp;&nbsp;§25.10 Configuration Drift Detection › Reconciliation | 78 | DRIFT |  |
| &nbsp;&nbsp;§25.10 Configuration Drift Detection › Degradation | 136 | DRIFT | (subsection rule) Behavioral rule or behavior under failure → the capability. |
| &nbsp;&nbsp;§25.10 Configuration Drift Detection › Metrics | 32 | LOPSSVC | (subsection rule) Unit fact (implementation, storage, metrics) → the component. |
| &nbsp;&nbsp;§25.10 Configuration Drift Detection › Error Codes | 62 | OPSAUD | (subsection rule) Interface fact under the DRIFT/LOPSSVC/OPSAUD split of this subsection family. |
| &nbsp;&nbsp;§25.10 Configuration Drift Detection › Audit Events | 22 | EVTAUD | (subsection rule) Audit event schemas → EVTAUD; the emitting operation links its owner capability. |
| §25.11 Backup and Restore API | 105 | BACKUP |  |
| &nbsp;&nbsp;§25.11 Backup and Restore API › Endpoints | 428 | OPSLIFE | (subsection rule) Interface fact under the BACKUP/LOPSSVC/OPSLIFE split of this subsection family. |
| &nbsp;&nbsp;§25.11 Backup and Restore API › Go Interface | 93 | code docs | (subsection rule) Go signature; package documentation, or the component internal parts where a reader needs it. |
| &nbsp;&nbsp;§25.11 Backup and Restore API › Response Types | 131 | OPSLIFE | (subsection rule) Interface fact under the BACKUP/LOPSSVC/OPSLIFE split of this subsection family. |
| &nbsp;&nbsp;§25.11 Backup and Restore API › Backup Execution | 23 | BACKUP |  |
| &nbsp;&nbsp;&nbsp;&nbsp;§25.11 Backup and Restore API › Backup Execution › Creation Sequence (Insert-Before-Job) | 191 | BACKUP |  |
| &nbsp;&nbsp;&nbsp;&nbsp;§25.11 Backup and Restore API › Backup Execution › Job Pod Specification | 525 | BKJOB |  |
| &nbsp;&nbsp;&nbsp;&nbsp;§25.11 Backup and Restore API › Backup Execution › Sensitive Content Policy | 185 | BACKUP |  |
| &nbsp;&nbsp;&nbsp;&nbsp;§25.11 Backup and Restore API › Backup Execution › MinIO Bucket Policy | 78 | OBJ 50%, BKJOB 50% |  |
| &nbsp;&nbsp;&nbsp;&nbsp;§25.11 Backup and Restore API › Backup Execution › ArtifactStore Backup (MinIO workspace bucket replication) | 1865 | BACKUP 50%, OBJ 30%, RESID 20% |  |
| &nbsp;&nbsp;&nbsp;&nbsp;§25.11 Backup and Restore API › Backup Execution › Backup Progress | 48 | OPSLIFE |  |
| &nbsp;&nbsp;§25.11 Backup and Restore API › Scheduled Backups | 45 | BACKUP |  |
| &nbsp;&nbsp;§25.11 Backup and Restore API › Retention Enforcement | 40 | BACKUP |  |
| &nbsp;&nbsp;&nbsp;&nbsp;§25.11 Backup and Restore API › Retention Enforcement › Tier-Based Retention Defaults | 180 | BACKUP 50%, DCFGTIER 50% |  |
| &nbsp;&nbsp;§25.11 Backup and Restore API › Backup Verification | 53 | BACKUP |  |
| &nbsp;&nbsp;§25.11 Backup and Restore API › Restore Execution | 644 | BACKUP |  |
| &nbsp;&nbsp;&nbsp;&nbsp;§25.11 Backup and Restore API › Restore Execution › Pre-Restore Backup Lifecycle | 162 | BACKUP |  |
| &nbsp;&nbsp;&nbsp;&nbsp;§25.11 Backup and Restore API › Restore Execution › Restore Failure and Recovery | 503 | BACKUP |  |
| &nbsp;&nbsp;&nbsp;&nbsp;§25.11 Backup and Restore API › Restore Execution › Safety Check | 146 | BACKUP |  |
| &nbsp;&nbsp;§25.11 Backup and Restore API › Restore Workflow (Day-2 Operations) | 157 | operator guide 50%, BACKUP 50% |  |
| &nbsp;&nbsp;&nbsp;&nbsp;§25.11 Backup and Restore API › Restore Workflow (Day-2 Operations) › RTO/RPO Targets by Tier | 183 | BACKUP 50%, SLO 50% |  |
| &nbsp;&nbsp;&nbsp;&nbsp;§25.11 Backup and Restore API › Restore Workflow (Day-2 Operations) › Test Restore | 59 | operator guide |  |
| &nbsp;&nbsp;§25.11 Backup and Restore API › Storage | 176 | LOPSSVC 50%, STORPG 50% | (subsection rule) Storage mechanism → component; table definitions → STORPG. |
| &nbsp;&nbsp;§25.11 Backup and Restore API › Degradation | 73 | BACKUP | (subsection rule) Behavioral rule or behavior under failure → the capability. |
| &nbsp;&nbsp;§25.11 Backup and Restore API › Metrics | 77 | LOPSSVC | (subsection rule) Unit fact (implementation, storage, metrics) → the component. |
| &nbsp;&nbsp;§25.11 Backup and Restore API › Alerting Rules | 94 | BACKUP | (subsection rule) Behavioral rule or behavior under failure → the capability. |
| &nbsp;&nbsp;§25.11 Backup and Restore API › Error Codes | 608 | OPSLIFE | (subsection rule) Interface fact under the BACKUP/LOPSSVC/OPSLIFE split of this subsection family. |
| &nbsp;&nbsp;§25.11 Backup and Restore API › Audit Events | 226 | EVTAUD | (subsection rule) Audit event schemas → EVTAUD; the emitting operation links its owner capability. |
| §25.12 MCP Management Server | 48 | OPSMCP |  |
| &nbsp;&nbsp;§25.12 MCP Management Server › Scope | 117 | OPSMCP |  |
| &nbsp;&nbsp;§25.12 MCP Management Server › Architecture | 126 | LOPS 50%, OPSMCP 50% |  |
| &nbsp;&nbsp;§25.12 MCP Management Server › Tool Inventory | 16 | OPSMCP |  |
| &nbsp;&nbsp;&nbsp;&nbsp;§25.12 MCP Management Server › Tool Inventory › Observation Tools (read-only) | 463 | OPSMCP |  |
| &nbsp;&nbsp;&nbsp;&nbsp;§25.12 MCP Management Server › Tool Inventory › Action Tools (mutating) | 809 | OPSMCP |  |
| &nbsp;&nbsp;§25.12 MCP Management Server › Tool Schema Example | 82 | OPSMCP |  |
| &nbsp;&nbsp;§25.12 MCP Management Server › OpenAPI Schema Discovery | 183 | OPSMCP |  |
| &nbsp;&nbsp;§25.12 MCP Management Server › Security Model | 422 | OPSMCP 50%, AUTHZ 50% |  |
| &nbsp;&nbsp;§25.12 MCP Management Server › Dry-Run Result Mapping | 212 | OPSMCP |  |
| &nbsp;&nbsp;§25.12 MCP Management Server › Capability Negotiation | 307 | OPSMCP |  |
| &nbsp;&nbsp;§25.12 MCP Management Server › Tool Schema Details | 229 | OPSMCP |  |
| &nbsp;&nbsp;§25.12 MCP Management Server › Headers and Correlation | 178 | OPSMCP |  |
| &nbsp;&nbsp;§25.12 MCP Management Server › SSE Event Subscription via MCP | 55 | OPSMCP |  |
| §25.13 Bundled Alerting Rules | 0 | None (container) |  |
| &nbsp;&nbsp;§25.13 Bundled Alerting Rules › Rationale | 141 | SLO |  |
| &nbsp;&nbsp;§25.13 Bundled Alerting Rules › Single Source of Truth | 188 | OBSCONV |  |
| &nbsp;&nbsp;§25.13 Bundled Alerting Rules › Helm Rendering | 168 | DCFG 50%, OBSCONV 50% |  |
| &nbsp;&nbsp;§25.13 Bundled Alerting Rules › Tier-Aware Defaults | 220 | DCFGTIER 50%, SLO 50% |  |
| &nbsp;&nbsp;§25.13 Bundled Alerting Rules › Operator Customization Model | 203 | SLO 50%, DCFG 50% |  |
| &nbsp;&nbsp;§25.13 Bundled Alerting Rules › Gateway In-Process Tracker (Fallback Behavior) | 188 | GWOPS |  |
| &nbsp;&nbsp;§25.13 Bundled Alerting Rules › Alertmanager Routing | 130 | SLO 50%, operator guide 50% |  |
| &nbsp;&nbsp;§25.13 Bundled Alerting Rules › Storage | 35 | OBSCONV |  |
| &nbsp;&nbsp;§25.13 Bundled Alerting Rules › Evaluation Cost | 209 | PROM |  |
| &nbsp;&nbsp;§25.13 Bundled Alerting Rules › Metrics | 75 | LOPS 50%, GW 50% |  |
| &nbsp;&nbsp;§25.13 Bundled Alerting Rules › Audit Events | 29 | EVTAUD |  |
| &nbsp;&nbsp;§25.13 Bundled Alerting Rules › Failure Mode Implications | 119 | SLO |  |
| §25.14 `lenny-ctl` Extensions | 18 | CLIOPS |  |
| &nbsp;&nbsp;§25.14 `lenny-ctl` Extensions › Routing | 69 | CLI |  |
| &nbsp;&nbsp;§25.14 `lenny-ctl` Extensions › Runbook Commands | 53 | CLIOPS |  |
| &nbsp;&nbsp;§25.14 `lenny-ctl` Extensions › Remediation Lock Commands | 50 | CLIOPS |  |
| &nbsp;&nbsp;§25.14 `lenny-ctl` Extensions › Escalation Commands | 49 | CLIOPS |  |
| &nbsp;&nbsp;§25.14 `lenny-ctl` Extensions › Log Commands | 57 | CLIOPS |  |
| &nbsp;&nbsp;§25.14 `lenny-ctl` Extensions › Identity and Operations Commands | 86 | CLIOPS |  |
| &nbsp;&nbsp;§25.14 `lenny-ctl` Extensions › Diagnose Commands | 61 | CLIOPS |  |
| &nbsp;&nbsp;§25.14 `lenny-ctl` Extensions › Event Commands | 83 | CLIOPS |  |
| &nbsp;&nbsp;§25.14 `lenny-ctl` Extensions › Audit Commands | 141 | CLIOPS |  |
| &nbsp;&nbsp;§25.14 `lenny-ctl` Extensions › Drift Commands | 70 | CLIOPS |  |
| &nbsp;&nbsp;§25.14 `lenny-ctl` Extensions › Backup and Restore Commands | 189 | CLIOPS |  |
| &nbsp;&nbsp;§25.14 `lenny-ctl` Extensions › Upgrade Commands | 110 | CLIOPS |  |
| &nbsp;&nbsp;§25.14 `lenny-ctl` Extensions › MCP-Management Commands | 100 | CLIOPS |  |
| §25.15 Failure Mode Analysis | 693 | AVAIL |  |
| &nbsp;&nbsp;§25.15 Failure Mode Analysis › Total-Outage Recovery Runbook | 71 | runbooks |  |
| &nbsp;&nbsp;&nbsp;&nbsp;§25.15 Failure Mode Analysis › Total-Outage Recovery Runbook › Triage (first 5 minutes) | 133 | runbooks |  |
| &nbsp;&nbsp;&nbsp;&nbsp;§25.15 Failure Mode Analysis › Total-Outage Recovery Runbook › Recovery Paths | 512 | runbooks |  |
| &nbsp;&nbsp;&nbsp;&nbsp;§25.15 Failure Mode Analysis › Total-Outage Recovery Runbook › Decision Tree | 64 | runbooks |  |
| &nbsp;&nbsp;&nbsp;&nbsp;§25.15 Failure Mode Analysis › Total-Outage Recovery Runbook › Why human-only | 104 | AVAIL | Rationale for human-only total-outage recovery. |
| §25.16 Deployment Topology Summary | 0 | None (container) |  |
| &nbsp;&nbsp;§25.16 Deployment Topology Summary › Minimal (single-node / dev) | 82 | ARCH 50%, LOCALDEV 50% |  |
| &nbsp;&nbsp;§25.16 Deployment Topology Summary › Production | 155 | ARCH |  |
| &nbsp;&nbsp;§25.16 Deployment Topology Summary › External DevOps Agent | 96 | ARCH 50%, OPSAREA 50% |  |
| §25.17 End-to-End Operational Example | 26 | WKOPS |  |
| &nbsp;&nbsp;§25.17 End-to-End Operational Example › Scenario | 29 | WKOPS |  |
| &nbsp;&nbsp;§25.17 End-to-End Operational Example › Step 1: Observe — Event Arrives | 72 | WKOPS |  |
| &nbsp;&nbsp;§25.17 End-to-End Operational Example › Step 2: Detect — Agent Evaluates | 21 | WKOPS |  |
| &nbsp;&nbsp;§25.17 End-to-End Operational Example › Step 3: Diagnose — Fetch Context | 122 | WKOPS |  |
| &nbsp;&nbsp;§25.17 End-to-End Operational Example › Step 4: Decide — Agent Chooses Action | 36 | WKOPS |  |
| &nbsp;&nbsp;§25.17 End-to-End Operational Example › Step 5: Act — Acquire Lock and Remediate | 52 | WKOPS |  |
| &nbsp;&nbsp;§25.17 End-to-End Operational Example › Step 6: Verify — Confirm Recovery | 79 | WKOPS |  |
| &nbsp;&nbsp;§25.17 End-to-End Operational Example › Failure Path: Escalation | 85 | WKOPS |  |
| &nbsp;&nbsp;§25.17 End-to-End Operational Example › Multi-Cluster Note | 68 | WKOPS |  |

### `spec/26_reference-runtime-catalog.md`

| Current heading | Own words | Destination | Split note |
| :-- | --: | :-- | :-- |
| §26 Reference Runtime Catalog | 427 | reference runtimes |  |
| &nbsp;&nbsp;§26.1 Catalog Overview | 290 | reference runtimes |  |
| &nbsp;&nbsp;§26.2 Shared Patterns for Coding-Agent Runtimes | 1001 | reference runtimes 70%, removed 30% | Runtime categories leave the specification (owner feedback; egress design "Runtime categories in §26.2"). |
| &nbsp;&nbsp;§26.3 `claude-code` | 708 | reference runtimes |  |
| &nbsp;&nbsp;§26.4 `gemini-cli` | 92 | reference runtimes |  |
| &nbsp;&nbsp;§26.5 `codex` | 104 | reference runtimes |  |
| &nbsp;&nbsp;§26.6 `cursor-cli` | 151 | reference runtimes |  |
| &nbsp;&nbsp;§26.7 `chat` | 343 | reference runtimes |  |
| &nbsp;&nbsp;§26.8 `langgraph` | 240 | reference runtimes |  |
| &nbsp;&nbsp;§26.9 `mastra` | 149 | reference runtimes |  |
| &nbsp;&nbsp;§26.10 `openai-assistants` | 243 | reference runtimes |  |
| &nbsp;&nbsp;§26.11 `crewai` | 246 | reference runtimes |  |
| &nbsp;&nbsp;§26.12 Adding a new reference runtime | 66 | contributor docs |  |

### `spec/27_web-playground.md`

| Current heading | Own words | Destination | Split note |
| :-- | --: | :-- | :-- |
| §27 Web Playground | 49 | PLAY |  |
| &nbsp;&nbsp;§27.1 Purpose and non-goals | 104 | PLAY |  |
| &nbsp;&nbsp;§27.2 Placement and gating | 856 | PLAY 80%, DCFG 20% |  |
| &nbsp;&nbsp;§27.3 Authentication | 978 | BROWSER 60%, PLAY 40% |  |
| &nbsp;&nbsp;&nbsp;&nbsp;§27.3.1 OIDC cookie-to-MCP-bearer exchange | 3216 | BROWSER 40%, PLAY 20%, removed 40% | Cookie session and token mint rules → BROWSER; process-side handling → PLAY; the MCP bearer and WebSocket wording leave (RO-12). |
| &nbsp;&nbsp;§27.4 UI surface | 226 | PLAY |  |
| &nbsp;&nbsp;§27.5 Protocol | 202 | PLAY 50%, removed 50% |  |
| &nbsp;&nbsp;§27.6 Session lifecycle and cleanup | 466 | PLAY 70%, SEND 30% |  |
| &nbsp;&nbsp;§27.7 Asset serving and CSP | 129 | PLAY |  |
| &nbsp;&nbsp;§27.8 Metrics | 273 | PLAY |  |
| &nbsp;&nbsp;§27.9 Security considerations | 327 | PLAY 60%, SECMODEL 40% |  |
| &nbsp;&nbsp;§27.10 Roll-forward notes | 65 | BUILD-PLAN.md |  |

### `spec/28_communication-channels.md`

| Current heading | Own words | Destination | Split note |
| :-- | --: | :-- | :-- |
| §28 Communication Channels | 89 | CHAN |  |
| &nbsp;&nbsp;§28.1 Naming law | 697 | NAMING |  |
| &nbsp;&nbsp;§28.2 Taxonomy and axes | 304 | CHAN |  |
| &nbsp;&nbsp;§28.3 Registers | 181 | CHAN |  |
| &nbsp;&nbsp;&nbsp;&nbsp;§28.3 Registers › Link register | 76 | CHAN 50%, CAT-CHANNELS 50% |  |
| &nbsp;&nbsp;&nbsp;&nbsp;§28.3 Registers › Channel register | 439 | CAT-CHANNELS |  |
| &nbsp;&nbsp;&nbsp;&nbsp;§28.3 Registers › Register-entry register | 280 | CAT-CHANNELS |  |
| &nbsp;&nbsp;&nbsp;&nbsp;§28.3 Registers › Naming table | 132 | NAMING |  |
| &nbsp;&nbsp;§28.4 Claim register | 147 | CHAN 50%, BUILD-PLAN.md 50% | ABSENT rows are build gaps → BUILD-GAPS.md; the register format → CHAN. |
| &nbsp;&nbsp;§28.5 Contract cards | 365 | CHAN |  |
| &nbsp;&nbsp;&nbsp;&nbsp;§28.5.1 Gateway-to-pod | 2064 | CHGWPOD |  |
| &nbsp;&nbsp;&nbsp;&nbsp;§28.5.2 Pod-to-gateway | 765 | CHPODGW |  |
| &nbsp;&nbsp;&nbsp;&nbsp;§28.5.3 Intra-pod | 12056 | RTPMSG 33%, RTPPART 17%, RTPOPS 15%, RTPTOOL 12%, CHINTRA 13%, removed 10% | CH-MSGSOCK card and frames, MessagePart → RTPMSG; CH-RUNTIMEOPS → RTPOPS; CH-MCP-PLATFORM and adapter-local tools → RTPTOOL; CH-SUPERVISE → CHINTRA; CH-MCP-CONNECTOR removed (CR-1). |
| &nbsp;&nbsp;&nbsp;&nbsp;§28.5.4 Inter-replica | 358 | CHREPLICA |  |
| &nbsp;&nbsp;&nbsp;&nbsp;§28.5.5 Pod-egress | 1758 | CHEGRESS 60%, removed 40% | CH-OBJSTORE → CHEGRESS; CH-LLMPROXY replaced by the metering proxy routes (LU-3). |
| &nbsp;&nbsp;&nbsp;&nbsp;§28.5.6 Control-plane | 855 | CHCTRL |  |
| &nbsp;&nbsp;&nbsp;&nbsp;§28.5.7 Gateway-to-store | 1017 | CHSTORE |  |
| &nbsp;&nbsp;§28.6 Exclusivity and concurrency model | 1179 | CHAN |  |
| &nbsp;&nbsp;§28.7 Wire-contract artifact register | 862 | CHAN 50%, COMPAT 50% |  |
| &nbsp;&nbsp;§28.8 Failure and degradation matrix | 4088 | CAT-CHANNELS 60%, AVAIL 40% | Per-channel rows restate each card's Degradation block; generate them (CAT-CHANNELS); cross-channel principles → AVAIL. |

### `spec/29_communication-scenarios.md`

| Current heading | Own words | Destination | Split note |
| :-- | --: | :-- | :-- |
| §29 Communication Scenarios | 300 | WALK |  |
| &nbsp;&nbsp;§29.1 Participants and trace notation | 1227 | WALK |  |
| &nbsp;&nbsp;§29.2 Session start | 2476 | WKSTART |  |
| &nbsp;&nbsp;§29.3 Interactive message send | 4005 | WKMSG |  |
| &nbsp;&nbsp;§29.4 Interrupt, terminate, and delete | 1601 | WKSTOP |  |
| &nbsp;&nbsp;§29.5 Checkpoint capture | 2002 | WKCKPT |  |
| &nbsp;&nbsp;§29.6 Restore and resume | 1364 | WKRESUME |  |
| &nbsp;&nbsp;§29.7 Gateway drain | 1001 | WKDRAIN |  |
| &nbsp;&nbsp;§29.8 Coordinator handoff and crash takeover | 956 | WKHANDOFF |  |
| &nbsp;&nbsp;§29.9 Agent pod eviction | 692 | WKEVICT |  |
| &nbsp;&nbsp;§29.10 Co-tenancy on a concurrent-session pod | 1303 | WKCOTEN |  |

### `spec/README.md`

| Current heading | Own words | Destination | Split note |
| :-- | --: | :-- | :-- |
| README › Lenny Technical Design | 3 | README | Title of the current tree; the new root map replaces it. |
| README › Table of Contents | 1512 | README | Hand-kept table of contents; the root map and the index pages replace it. |

## Designs

Each design is treated as implemented. "Lands" means the section becomes current behavior on the named pages; its words count toward the size estimates at half weight. Sections not listed (Problem, Scope, Terms, High-level design, Decisions, Current state, Alternatives rejected, Validation owed, Tests, Specification changes, Non-specification changes, Documentation, Instructions for reviewers, Proposal split, Sources, and Review history) go to the archive of designs. A design's Terms table supplies glossary entries to the pages that own each term.

| Design | Words | Class | Section → destination |
| :-- | --: | :-- | :-- |
| `agent-pod-layout-design.md` | 41,909 | Change: lands as current behavior; PL-2 and PL-8 are removals. | Requirements → POD; PL-1 → POD; PL-2 → removed; PL-3 → RTPFS; PL-4 → RTPFS 50%, FILEX 50%; PL-5 → PODVOL; PL-6 → RTPFS; PL-7 → archive; PL-8 → removed; PL-9 → POOL 50%, SLOT 50%; PL-10 → PODVOL 60%, QUOTA 40%; Limits → POD |
| `agent-runtimes-only-design.md` | 15,005 | Removal: archive only, except AR-3 (one runtime connection per session) and AR-4 (refusal of removed fields), which land. | AR-1 → removed; AR-2 → removed; AR-3 → RTPHAND; AR-4 → ERR 50%, RUNTIME 50%; AR-5 → SCREATE; AR-6 → operator guide |
| `anthropic-messages-api-design.md` | 20,240 | Moves to the Anthropic adapter repository; only AM-4 (conversation input) governs Lenny. | AM-1 → adapter repos; AM-2 → adapter repos; AM-3 → adapter repos; AM-4 → MSG 50%, CAPIMSG 50%; AM-5 → adapter repos; AM-6 → adapter repos; AM-7 → adapter repos; AM-8 → adapter repos; AM-9 → adapter repos; AM-10 → adapter repos |
| `billing-resource-attribution-design.md` | 23,245 | Change: lands as current behavior. | Design → RCLASS 25%, ATTR 45%, EVTBILL 20%, ADMRT 10%; Residual risks → ATTR |
| `client-tools-design.md` | 20,488 | Change: lands as current behavior; the surface mappings of CT-6 for provider protocols move to adapters. | Requirements → CTOOL; CT-1 → RTPMSG 60%, CAPIMSG 40%; CT-2 → RTPHAND; CT-3 → CTOOL; CT-4 → CTOOL; CT-5 → CTOOL; CT-6 → CAPIMSG 50%, adapter repos 50%; CT-7 → CTOOL; CT-8 → CTOOL; CT-9 → DLG; CT-10 → RTPSDK 50%, RTPCONF 50% |
| `connector-authorization-and-external-memory-design.md` | 21,549 | Record: archive only, except CA-0 (shared OAuth link flow) and CA-10 (memory removal, with the external memory pattern for the operator guide). | CA-0 → LINK; CA-1 → archive; CA-2 → archive; CA-3 → archive; CA-4 → LINK 50%, archive 50%; CA-5 → archive; CA-6 → archive; CA-7 → archive; CA-8 → archive; CA-9 → archive; CA-10 → removed; External memory pattern → operator guide |
| `connector-removal-design.md` | 18,254 | Removal with additions: CR-1 is archive only; CR-2 to CR-4 land. | Requirements → OPSVC; Threat model → OPSVC; CR-1 → removed; CR-2 → OPSVC; CR-3 → OPSVC; CR-4 → OPSVC 50%, SUSP 50% |
| `decision-records-design.md` | 4,485 | Removal: archive only. It deletes ADR labels and the docs/adr folder; §19 leaves the specification under the same decision. | REC-1 → removed |
| `delegation-availability-design.md` | 19,106 | Change: lands as current behavior. | Requirements → DAVAIL; DA-1 → DAVAIL; DA-2 → DAVAIL; DA-3 → DAVAIL; DA-4 → DAVAIL 50%, RTPTOOL 50%; DA-5 → DAVAIL; DA-6 → DAVAIL; DA-7 → DAVAIL 50%, INPUT 50% |
| `egress-and-git-design.md` | 112,410 | Change: lands as current behavior across network, credentials, and several components. | Requirements → EGRESS; Security properties → EGRESS; Architecture → EGDEC 30%, NETAGENT 25%, EGPROXY 15%, EGAUTHZ 15%, GITMIR 5%, PKI 5%, AGDNS 5%; Git → GIT 50%, GITMIR 20%, CDELIV 15%, CPOOL 10%, APOOLS 5%; Credential delivery to runtimes → CDELIV; Interfaces → IFMAP; Configuration by scope → EGRESS; Effective policy → EGRESS; Injected configuration → RTPFS 50%, EGRESS 50%; Runtime SDKs → RTPSDK; Pod models → POD; Network policy and placement → NETISO 60%, AGDNS 20%, PLACE 20%; Defaults, replacement, and augmentation → EGDEF; TLS interception → EGDEF; Residual risks and limits → EGRESS 50%, SECMODEL 50%; Reserved for other designs → archive |
| `forensics-evidence-preservation-design.md` | 36,709 | Change: lands as current behavior. | Requirements → FORENSIC; Security properties → FORENSIC; Architecture → FORENSIC; FE-1 → FORENSIC 50%, APOD 30%, PODINIT 20%; FE-2 → HOLD 60%, GITMIR 40%; FE-3 → HOLD 60%, DATACLASS 40%; FE-4 → FORENSIC; FE-5 → FORENSIC 50%, runbooks 50%; FE-6 → ACTSTR; Configuration by scope → FORENSIC 50%, ACTSTR 50%; Scenarios → operator guide; Residual risks → FORENSIC |
| `github-credential-addon-design.md` | 15,739 | Change: lands as current behavior. | GH-1 → ADDON; GH-2 → ADDON 60%, PRAUTH 40%; GH-3 → ADDON 50%, EGRESS 50%; Configuration by scope → ADDON; Worked examples → operator guide; Residual risks → ADDON |
| `gitlab-credential-addon-design.md` | 7,820 | Change: lands as current behavior. | GL-1 → ADDON; GL-2 → ADDON 60%, PRAUTH 40%; GL-3 → ADDON 50%, EGRESS 50%; Configuration by scope → ADDON; Worked examples → operator guide; Residual risks → ADDON |
| `incremental-checkpoint-design.md` | 68,691 | Change: lands as current behavior; IC-9 is a removal; measurements and benchmarks stay in the archive. | Requirements → CKPT; Security properties → CKPT 50%, CKSTORE 50%; IC-1 → CKPASS 80%, SUPV 20%; IC-2 → CKSTORE 50%, STOROBJ 50%; IC-3 → CKPT 50%, CHGWPOD 50%; IC-4 → CKSTORE 80%, QUOTA 20%; IC-5 → RESTORE 70%, SDERIVE 15%, SEND 15%; IC-6 → CKSTORE 60%, HOLD 20%, ERASE 20%; IC-7 → CKPASS 60%, GWDRAIN 40%; IC-8 → CKPT 50%, CKPASS 50%; IC-9 → removed; Stored formats and wire contracts this forces → STOROBJ 50%, CHGWPOD 50% |
| `llm-response-cache-removal-design.md` | 9,546 | Removal: archive only, except RC-5 (provider prompt caching passthrough), which lands in LLMUP. | RC-1 → removed; RC-2 → removed; RC-3 → removed; RC-4 → removed; RC-5 → LLMUP |
| `llm-upstreams-and-cost-budgets-design.md` | 24,756 | Change: lands as current behavior; LU-4 is a removal. | Requirements → LLMUP; LU-1 → LLMUP; LU-2 → LLMUP 60%, SCREATE 20%, DLG 20%; LU-3 → GWLLM 40%, LLMUP 30%, CHEGRESS 30%; LU-4 → removed 80%, ERR 20%; LU-5 → RATE; LU-6 → BUDGET; LU-7 → USAGE 60%, EVTBILL 40%; LU-8 → LLMUP; LU-9 → LLMUP; LU-10 → LLMUP 60%, STREAM 40%; LU-11 → LLMUP; Limits → LLMUP |
| `mcp-protocol-upgrade-design.md` | 11,012 | Mostly moves to the MCP adapter repository; MP-5, MP-7, and MP-8 govern Lenny; MP-6 is withdrawn. | MP-1 → adapter repos; MP-2 → adapter repos; MP-3 → adapter repos; MP-4 → adapter repos; MP-5 → RTPTOOL; MP-6 → removed; MP-7 → OPSMCP 50%, RTPSDK 50%; MP-8 → COMPAT; MP-9 → adapter repos |
| `message-delivery-design.md` | 71,435 | Change: lands as current behavior; MD-8 and part of MD-21 move to adapters. | Requirements → MSG; MD-1 → MSG; MD-2 → RTPHAND 50%, MSG 50%; MD-3 → INBOX; MD-4 → MSG; MD-5 → MSG; MD-6 → MSG 60%, GWCOORD 40%; MD-7 → CAPIMSG; MD-8 → adapter repos; MD-9 → IMSG 60%, DLG 40%; MD-10 → CTOOL; MD-11 → INBOX; MD-12 → RTPSDK; MD-13 → reference runtimes; MD-14 → MSG; MD-15 → STREAM; MD-16 → STREAM 70%, RTPMSG 30%; MD-17 → STREAM; MD-18 → FILEMSG; MD-19 → INPUT; MD-20 → STREAM 50%, USAGE 50%; MD-21 → STREAM 50%, adapter repos 50%; MD-22 → DTREE; MD-23 → RTPMSG 50%, RTPTOOL 50%; MD-24 → RTPMSG; MD-25 → DTREE 50%, CAPIEVT 50%; MD-26 → FILEMSG 50%, FILEX 50%; Worked example → WKMSG; Limits → MSG |
| `placement-design.md` | 23,191 | Change: lands as current behavior. | Requirements → PLACE; Security properties → PLACE; PM-1 → PLACE; PM-2 → PLACE; PM-3 → PLACE 70%, APOOLS 30%; PM-4 → PLACE; PM-5 → PLACE 50%, WPC 50%; PM-6 → PLACE 50%, WEBHOOK 50%; PM-7 → PLACE 50%, RESID 50%; PM-8 → PLACE 40%, GW 20%, LOPS 20%, TOKSVC 20%; PM-9 → ARCH; Removals → removed; Limits → PLACE |
| `pod-lifecycle-controller-design.md` | 25,395 | Rename and reframing: lands in the kind pages and the controller pages; PLC-5 (Go names) leaves to code. | PLC-1 → WPC 50%, PSC 50%; PLC-2 → KPSET 35%, KAPOD 35%, KPCLAIM 30%; PLC-3 → K8S 25%, KPSET 25%, KAPOD 25%, KPCLAIM 25%; PLC-4 → WPC; PLC-5 → code docs; PLC-6 → archive; PLC-7 → NAMING 50%, RUNTIME 25%, POOL 25% |
| `rest-only-client-api-design.md` | 26,010 | Change and removal: RO-1, RO-2, RO-4, RO-6, and RO-7 land; RO-8 to RO-10 move to adapter repositories; RO-11 is archive only. | Requirements → CAPI; External protocol versions → adapter repos; RO-1 → CAPI 50%, IFMAP 50%; RO-2 → CAPISES 30%, CAPIMSG 30%, CAPIEVT 20%, INPUT 20%; RO-3 → IFMAP 50%, adapter repos 50%; RO-4 → AUTHZ; RO-5 → adapter repos 60%, COMPAT 40%; RO-6 → adapter repos 50%, CAPI 50%; RO-7 → CLI 40%, PLAY 30%, client guide 30%; RO-8 → adapter repos; RO-9 → adapter repos; RO-10 → adapter repos; RO-11 → removed; RO-12 → archive |
| `runtime-protocol-design.md` | 25,830 | Change: lands as current behavior. | Requirements → RTP; RP-1 → RTPHAND; RP-2 → RTPHAND; RP-3 → RTPTOOL; RP-4 → RTPHAND 50%, DAVAIL 50%; RP-5 → RTPTOOL 50%, RTPSDK 50%; RP-6 → ADAPTER 50%, RTP 50%; RP-7 → RTP 50%, COMPAT 50%; RP-8 → RTPCONF; Worked example → runtime author guide |
| `runtime-revisions-design.md` | 27,505 | Change: lands as current behavior; it replaces the pool-level RuntimeUpgrade. | Requirements → REV; RV-1 → REV; RV-2 → REV; RV-3 → REV 50%, POOL 50%; RV-4 → REV; RV-5 → REV 50%, RUNTIME 50%; RV-6 → REV; RV-7 → REV 60%, CAPISES 40%; Worked example: an upgrade on AWS → operator guide |
| `runtime-routing-design.md` | 16,393 | Change and removal: RT-1 removes experiments and evaluations (archive only); RT-2 to RT-6 land. | Requirements → ROUTE; RT-1 → removed; RT-2 → INTCPT 50%, INTCPTIF 50%; RT-3 → ROUTE; RT-4 → ROUTE; RT-5 → INTCPT; RT-6 → INTCPT; RT-7 → archive |
| `security-oversight-design.md` | 32,510 | Change: lands as current behavior. | Requirements → SECOV; Security properties → SECOV; SO-1 → SECROLE; SO-2 → SECOV 70%, TSET 30%; SO-3 → SIEM; SO-4 → SECOV; SO-5 → SECROLE 60%, SECOV 40%; SO-6 → PKI 50%, SECOV 50%; SO-7 → LOPS 50%, SECROLE 50%; Configuration by scope → SECOV; Worked scenarios → operator guide; Interactions with `egress-and-git-design.md` → EGRESS 50%, SECOV 50%; Residual risks → SECOV |
| `service-account-credentials-design.md` | 20,254 | Change: lands as current behavior. | Requirements → SACRED; SA-1 → SACRED; SA-2 → SACRED; SA-3 → SACRED 60%, TOKX 40%; SA-4 → SACRED; SA-5 → CDELIV; SA-6 → PRINC; Configuration by scope → SACRED; Threat model → SACRED |
| `session-derive-design.md` | 18,866 | Change: lands as current behavior; replay leaves. | Requirements → SDERIVE; Security properties → SDERIVE; SD-1 → SDERIVE 70%, CAPISES 30%; What an exact start does not cover → SDERIVE; Stored formats and wire contracts this forces → STORPG |
| `session-isolation-design.md` | 36,326 | Change: lands as current behavior. | Requirements → SLOT; SI-1 → SLOT 50%, POD 50%; SI-2 → SLOT 50%, CHINTRA 50%; SI-3 → SUPV 40%, SLOTOPS 30%, SETUP 30%; SI-4 → SLOTOPS 60%, KAPOD 40%; SI-5 → SUPV; SI-6 → CKPT 30%, RESTORE 20%, SLOT 30%, SCRUB 20%; SI-7 → SLOT; SI-8 → REPO; SI-9 → SLOT 50%, RCLASS 50%; SI-10 → SLOT; SI-11 → SLOT 50%, PLACE 50%; SI-12 → archive |
| `session-lifecycle-design.md` | 25,900 | Change: lands as current behavior. | Requirements → SESS; SL-1 → SESS; SL-2 → SESS; SL-3 → SUSP; SL-4 → SUSP 50%, DTREE 50%; SL-5 → SUSP 50%, INBOX 50%; SL-6 → SUSP; SL-7 → SEND; SL-8 → DTREE; SL-9 → DTREE; SL-10 → TIMEOUT 50%, ADMIT 50%; SL-11 → CAPISES 50%, SESS 50%; SL-12 → DTREE |
| `tenant-derived-runtimes-design.md` | 73,857 | Change: lands as current behavior; DR-1 to DR-3 are prerequisites recorded in the archive. | Requirements → DERIVED; Security properties → DERIVED; DR-1 → archive; DR-4 → DERIVED; Who turns it on → DERIVED 60%, TSET 40%; Fields → DERIVED 50%, ADMRT 50%; DR-5 → OWNPOOL 60%, POOL 20%, APOOLS 20%; DR-6 → DERIVED; DR-9 → CLONE; DR-12 → REPO 50%, RTPFS 50%; DR-10 → OWNPOOL 60%, PLACE 20%, ATTR 20%; DR-8 → DERIVED; DR-7 → DERIVED 50%, SECOV 50%; Configuration by scope → DERIVED; Scenarios → operator guide; Residual risks → DERIVED |
| `tenant-outbound-connector-design.md` | 26,722 | Change: lands as current behavior. | Requirements → OUTCONN; Security properties → OUTCONN; Build versus adopt → archive; Design → OUTCONN 50%, OUTGW 25%, OUTAGT 15%, ADMTEN 10%; Configuration by scope → OUTCONN; Scenarios → operator guide; Residual risks → OUTCONN |
| `tenant-scoping-identity-design.md` | 53,056 | Change: lands as current behavior. | Requirements → ENV; Security properties → TENISO; TS-1 → ENV; TS-3 → ENV 60%, SCREATE 20%, TENISO 20%; Identity model (TS-2, TS-4, and TS-6 to TS-9) → PRINC 60%, AUTHN 20%, AUTHZ 20%; Configuration by scope → ENV 50%, PRINC 50%; Scenarios → operator guide; Residual risks → TENISO |
| `tenant-security-settings-design.md` | 18,086 | Change: lands as current behavior; TSS-9 is a removal. | Requirements → TSET; Security properties → TSET; Inventory → TSET; TSS-1 → TSET; TSS-2 → TSET; TSS-3 → ADMTEN; TSS-4 → TSET 50%, KMS 50%; TSS-5 → TSET 50%, DATACLASS 50%; TSS-6 → TSET 50%, PLACE 50%; TSS-7 → TSET; TSS-8 → DATACLASS; TSS-9 → removed |
| `user-credential-delivery-design.md` | 47,406 | Change: lands as current behavior. | Requirements → UCRED; Threat model → UCRED; UC-1 → UCRED; UC-2 → LINK; UC-3 → UCRED; UC-4 → CREQ; UC-5 → CDELIV; UC-7 → ADDON; UC-8 → LINK 60%, INPUT 40%; Configuration by scope → UCRED; Worked examples → operator guide; Residual risks → UCRED |
| `user-git-identity-design.md` | 36,275 | Change: lands as current behavior. | Personas and requirements → REPOAUTH; Threat model → REPOAUTH 50%, PRAUTH 50%; UG-1 → LINK 50%, ADDON 50%; UG-2 → REPOAUTH; UG-3 → PRAUTH; UG-4 → PRAUTH; Configuration by scope → REPOAUTH 50%, PRAUTH 50%; Worked examples → operator guide; Residual risks → PRAUTH |
| `workspace-materialization-design.md` | 33,939 | Change: lands as current behavior. | WM-1 → MATER; WM-2 → MATER 50%, SUPV 50%; WM-3 → MATER 50%, CHINTRA 50%; WM-4 → MATER; WM-5 → MATER; WM-7 → MATER; WM-8 → MATER; WM-9 → MATER 50%, UPLOAD 50%; WM-10 → REPO 50%, MATER 50%; WM-11 → SUPV 50%, CKPT 50%; WM-12 → UPLOAD 50%, FILEX 50%; WM-13 → RESTORE 50%, SDERIVE 50%; WM-14 → MATER 50%, POD 50%; WM-15 → REPO; WM-16 → UPLOAD 50%, REPO 50%; Requirements → MATER; Security properties → MATER |

## Documentation reference pages

The pages under `docs/reference/` restate specification content today. In the proposed tree each one is generated from a catalog, or reduced to a guide that links the owning pages.

| Page | Words | Proposed treatment |
| :-- | --: | :-- |
| `docs/reference/adapter-contract.md` | 9,462 | Reduce to a runtime-author guide page that links `interfaces/runtime-protocol/` (RTP and its sub-pages) and `interfaces/adapter-manifest.md` (ADMAN). Its frame tables are generated from the definition blocks of RTPMSG, RTPOPS, and RTPTOOL. The gateway-to-adapter gRPC section leaves the page, because no runtime author builds against it; CHGWPOD owns it. |
| `docs/reference/cloudevents-catalog.md` | 858 | Generate from the events catalog (`catalogs/events.md`), whose rows come from EVTOPS, EVTBILL, EVTAUD, and EVTHOOK. |
| `docs/reference/configuration.md` | 6,646 | Generate from the settings catalog (`catalogs/settings.md`). Each Helm value is a setting block on the capability (behavioral settings) or the component (process settings) that owns it. |
| `docs/reference/error-catalog.md` | 6,182 | Generate from the errors catalog (`catalogs/errors.md`), whose rows come from each interface page and from ERR for shared codes. |
| `docs/reference/execution-modes.md` | 1,838 | Delete. Service mode is removed (agent-runtimes-only AR-2). The sessionPolicy matrix and presets are setting blocks on SCRUB, SLOT, and APOD, and the decision guide moves to `docs/operator-guide/`. |
| `docs/reference/glossary.md` | 3,915 | Generate from the glossary catalog (`catalogs/glossary.md`), whose rows come from the Terms section of every page. |
| `docs/reference/index.md` | 457 | Replace with a pointer to `catalogs/index.md` and the generated pages. |
| `docs/reference/metrics.md` | 8,223 | Generate from the metrics catalog (`catalogs/metrics.md`), whose rows come from the Metrics section of each component. |
| `docs/reference/state-machines.md` | 4,182 | Generate from the proposed state-machine catalog (CAT-STATES), whose rows come from the model blocks of SESS, APOD, DLG, REV, and UPGRADE. The pool upgrade state machine it draws is replaced by the runtime rollout (runtime-revisions RV-4). |
| `docs/reference/workspace-plan.md` | 1,002 | Generate from the field blocks of WSPLANIF, or link it. |

## Sections that leave the specification

| Current section | Words | Destination | Note |
| :-- | --: | :-- | :-- |
| §18 Build Sequence | 10,502 | `BUILD-PLAN.md` | A plan rather than a contract. Phase exit criteria that state a requirement move to the requirement's page; the rest is the plan. |
| §19 Resolved Decisions | 688 | The archive of designs and proposals | The decision-records design makes designs and proposals the decision records. Each row's current rationale moves to the Rationale section of its owner page. |
| §20 Open Questions | 11 | The issue tracker | The section holds one line. |
| §21 Planned / Post-V1 | 1,955 | `ROADMAP.md` | Its A2A and Agent Protocol items also leave Lenny under rest-only-client-api RO-11. |
| §22 Explicit Non-Decisions | 373 | PRIN (stays) | The items are non-goals with reasons, so they stay in the overview rather than leaving. |
| §23 Competitive Landscape | 3,873 | `docs/about/` | Comparison with other products. |
| §26 Reference Runtime Catalog | 4,060 | Reference runtime repositories and `docs/runtime-author-guide/` | Runtime categories leave the specification. Conformance obligations stay in RTPCONF. |
| §29 Communication Scenarios | 16,927 | `overview/walkthroughs/` (stays, non-normative) | Each scenario becomes one walkthrough page that links requirements and operations and states no rule. |
| §17.7 Operational Runbooks | 5,351 | `docs/runbooks/` | The runbook bodies; the machine-consumable format stays in RUNBOOK. |
| §17.8.3 Tier Promotion Guide | 626 | `docs/operator-guide/` | A procedure. |
| README › Table of Contents | 1,515 | `README.md` root map and index pages | A hand-kept table of contents. |

## Page size estimates

The estimate is the weighted words of the specification sections assigned to the page plus half the words of the design sections assigned to it. A fully written page adds front matter, a summary, a terms table, and definition blocks, and drops restatements; the slice's pages measure that difference. Pages written by the slice are listed for completeness, and their splits are proposals to the slice.

### Pages estimated over 6,000 words

| Page | Code | Spec words | Design words (half) | Estimate | Proposed split |
| :-- | :-- | --: | --: | --: | :-- |
| `capabilities/runtimes-and-pools/warm-pools.md` | POOL | 7,662 | 908 | 8,571 | Slice-owned. Split into pools-and-pod-sets (definitions, pod sets, disruption) and warm-inventory (targets, SDK-warm, demotion breaker, bootstrap). |
| `capabilities/workspaces-and-files/default-clones.md` | CLONE | 0 | 6,647 | 6,647 | Split into clone declaration and refresh (what is cloned, when, on which pods) and clone credentials and failure. |
| `capabilities/network-and-egress/egress-policy.md` | EGRESS | 890 | 5,652 | 6,542 | Split into egress-policy (model and effective policy) and egress-scopes (configuration by scope and tenant pool overlays). |
| `components/warm-pool-controller.md` | WPC | 5,578 | 793 | 6,371 | Slice-owned. Move etcd and API-server tuning to the Kubernetes dependency page (already 10% of §4.6.1 goes there). |
| `capabilities/sessions/checkpoints/checkpoint-capture.md` | CKPT | 4,415 | 1,858 | 6,273 | Split quiesce, timeout, and abort handling into its own page beside capture passes. |
| `capabilities/runtimes-and-pools/agent-pod-lifecycle.md` | APOD | 5,585 | 496 | 6,082 | Slice-owned. Move the runtime-exit outcomes table to the runtime supervisor component. |
| `capabilities/credentials/credential-delivery.md` | CDELIV | 475 | 5,597 | 6,072 | Split delivery modes from the file-delivery mechanics and per-slot credential files. |

The map already splits these subjects, which a first pass estimated over budget: the gateway (GWSCALE), lenny-ops (LOPSK8S, LOPSSVC), checkpoint capture (CKPASS), egress defaults (EGDEF), message parts (RTPPART), slot assignment and failure (SLOTOPS), credential request rules (CREQ), security roles (SECROLE), lease extension (LEASEXT), pod volumes (PODVOL), owned pools (OWNPOOL), browser sessions (BROWSER), pseudonymization and receipts (PSEUDO), and the scrub procedure (SCRUBPROC). Before those splits, lenny-ops was estimated at 13,800 words, checkpoint capture at 10,600, the runtime message frames at 9,100, and the gateway at 9,400.

### Pages estimated between 5,000 and 6,000 words

| Page | Code | Estimate |
| :-- | :-- | --: |
| `components/gateway/drain.md` | GWDRAIN | 5,878 |
| `capabilities/runtimes-and-pools/tenant-derived-runtimes.md` | DERIVED | 5,775 |
| `components/external/postgres.md` | PG | 5,719 |
| `capabilities/tenancy-and-access/authorization.md` | AUTHZ | 5,684 |
| `interfaces/channels/gateway-to-pod.md` | CHGWPOD | 5,666 |
| `capabilities/sessions/session-lifecycle/session-states.md` | SESS | 5,525 |
| `components/agent-pod/runtime-supervisor.md` | SUPV | 5,501 |
| `components/lenny-ops/hosted-services.md` | LOPSSVC | 5,471 |
| `capabilities/credentials/credential-pools.md` | CPOOL | 5,324 |
| `interfaces/runtime-protocol/messages.md` | RTPMSG | 5,221 |
| `capabilities/credentials/pull-request-authoring.md` | PRAUTH | 5,210 |
| `interfaces/runtime-protocol/platform-tools.md` | RTPTOOL | 5,161 |
| `capabilities/credentials/user-credentials.md` | UCRED | 5,145 |
| `capabilities/sessions/checkpoints/checkpoint-storage.md` | CKSTORE | 5,028 |

### Distribution

| Estimated words | Pages |
| :-- | --: |
| 0 to 500 | 16 |
| 500 to 1,000 | 26 |
| 1,000 to 2,000 | 51 |
| 2,000 to 3,000 | 46 |
| 3,000 to 4,000 | 39 |
| 4,000 to 5,000 | 24 |
| 5,000 to 6,000 | 14 |
| over 6,000 | 7 |

223 content pages have an estimate. The median is about 2,400 words. The pages hold about 410,000 words of current specification text and about 204,000 words from designs at half weight; the current specification has 490,000 words, of which the rest leaves under the rows of `MAP.md`.

### All estimates

| Code | Path | Spec words | Design words (half) | Estimate |
| :-- | :-- | --: | --: | --: |
| POOL | `capabilities/runtimes-and-pools/warm-pools.md` | 7,662 | 908 | 8,571 |
| CLONE | `capabilities/workspaces-and-files/default-clones.md` | 0 | 6,647 | 6,647 |
| EGRESS | `capabilities/network-and-egress/egress-policy.md` | 890 | 5,652 | 6,542 |
| WPC | `components/warm-pool-controller.md` | 5,578 | 793 | 6,371 |
| CKPT | `capabilities/sessions/checkpoints/checkpoint-capture.md` | 4,415 | 1,858 | 6,273 |
| APOD | `capabilities/runtimes-and-pools/agent-pod-lifecycle.md` | 5,585 | 496 | 6,082 |
| CDELIV | `capabilities/credentials/credential-delivery.md` | 475 | 5,597 | 6,072 |
| GWDRAIN | `components/gateway/drain.md` | 5,576 | 301 | 5,878 |
| DERIVED | `capabilities/runtimes-and-pools/tenant-derived-runtimes.md` | 0 | 5,775 | 5,775 |
| PG | `components/external/postgres.md` | 5,719 | 0 | 5,719 |
| AUTHZ | `capabilities/tenancy-and-access/authorization.md` | 3,023 | 2,660 | 5,684 |
| CHGWPOD | `interfaces/channels/gateway-to-pod.md` | 5,033 | 632 | 5,666 |
| SESS | `capabilities/sessions/session-lifecycle/session-states.md` | 4,283 | 1,242 | 5,525 |
| SUPV | `components/agent-pod/runtime-supervisor.md` | 2,502 | 2,998 | 5,501 |
| LOPSSVC | `components/lenny-ops/hosted-services.md` | 5,471 | 0 | 5,471 |
| CPOOL | `capabilities/credentials/credential-pools.md` | 4,634 | 689 | 5,324 |
| RTPMSG | `interfaces/runtime-protocol/messages.md` | 4,084 | 1,136 | 5,221 |
| PRAUTH | `capabilities/credentials/pull-request-authoring.md` | 0 | 5,210 | 5,210 |
| RTPTOOL | `interfaces/runtime-protocol/platform-tools.md` | 3,673 | 1,487 | 5,161 |
| UCRED | `capabilities/credentials/user-credentials.md` | 0 | 5,145 | 5,145 |
| CKSTORE | `capabilities/sessions/checkpoints/checkpoint-storage.md` | 2,748 | 2,280 | 5,028 |
| SLOT | `capabilities/sessions/session-isolation.md` | 2,491 | 2,493 | 4,985 |
| BACKUP | `capabilities/deployment-and-lifecycle/backup-and-restore.md` | 4,950 | 0 | 4,950 |
| SLO | `capabilities/observability-and-operations/alerting-and-slos.md` | 4,887 | 0 | 4,887 |
| REV | `capabilities/runtimes-and-pools/runtime-revisions.md` | 909 | 3,946 | 4,856 |
| LLMUP | `capabilities/network-and-egress/llm-upstreams.md` | 2,453 | 2,396 | 4,849 |
| PRINC | `capabilities/tenancy-and-access/principals.md` | 186 | 4,659 | 4,845 |
| DTREE | `capabilities/delegation/tree-recovery-and-cascade.md` | 2,758 | 2,085 | 4,843 |
| ENV | `capabilities/tenancy-and-access/environments.md` | 844 | 3,996 | 4,841 |
| LEASE | `capabilities/delegation/delegation-lease.md` | 4,799 | 0 | 4,799 |
| OBJ | `components/external/object-store.md` | 4,778 | 0 | 4,778 |
| PLACE | `capabilities/runtimes-and-pools/placement.md` | 545 | 4,145 | 4,690 |
| ERASE | `capabilities/security-and-compliance/erasure.md` | 4,548 | 95 | 4,644 |
| REDIS | `components/external/redis.md` | 4,542 | 0 | 4,542 |
| WKMSG | `overview/walkthroughs/interactive-message.md` | 4,005 | 495 | 4,500 |
| AUTHN | `capabilities/tenancy-and-access/authentication.md` | 3,156 | 1,341 | 4,498 |
| GWCOORD | `components/gateway/session-coordination.md` | 4,381 | 54 | 4,436 |
| AUDIT | `capabilities/security-and-compliance/audit-log.md` | 4,386 | 0 | 4,386 |
| MATER | `capabilities/workspaces-and-files/materialization.md` | 454 | 3,916 | 4,370 |
| CKPASS | `capabilities/sessions/checkpoints/capture-passes.md` | 0 | 4,331 | 4,331 |
| DPOL | `capabilities/delegation/delegation-policy.md` | 4,327 | 0 | 4,327 |
| MSG | `capabilities/messaging/message-delivery.md` | 899 | 3,421 | 4,321 |
| SACRED | `capabilities/credentials/service-account-credentials.md` | 0 | 4,231 | 4,231 |
| SECOV | `capabilities/security-and-compliance/security-oversight.md` | 0 | 4,220 | 4,220 |
| TSET | `capabilities/tenancy-and-access/tenant-settings.md` | 721 | 3,390 | 4,112 |
| QUOTA | `capabilities/policy-and-controls/quotas-and-rate-limits.md` | 2,909 | 1,078 | 3,988 |
| TOKX | `capabilities/tenancy-and-access/token-exchange.md` | 3,390 | 585 | 3,975 |
| RTPPART | `interfaces/runtime-protocol/message-parts.md` | 3,872 | 0 | 3,872 |
| SCRUB | `capabilities/sessions/pod-reuse-and-scrub.md` | 3,761 | 73 | 3,834 |
| LOPSK8S | `components/lenny-ops/kubernetes-resources.md` | 3,794 | 0 | 3,794 |
| STREAM | `capabilities/messaging/event-stream.md` | 2,327 | 1,459 | 3,787 |
| ERR | `cross-cutting/error-model.md` | 3,646 | 130 | 3,777 |
| PLAY | `components/web-playground.md` | 3,716 | 57 | 3,773 |
| RESUME | `capabilities/sessions/retry-and-resume.md` | 3,717 | 0 | 3,717 |
| FILEMSG | `capabilities/workspaces-and-files/files-in-messages.md` | 0 | 3,703 | 3,703 |
| PREFLT | `components/preflight-job.md` | 3,563 | 0 | 3,563 |
| EVTAUD | `interfaces/events/audit-records.md` | 3,512 | 0 | 3,512 |
| INBOX | `capabilities/messaging/inbox-and-custody.md` | 2,181 | 1,297 | 3,478 |
| LINK | `capabilities/credentials/account-links.md` | 127 | 3,348 | 3,475 |
| OUTCONN | `capabilities/network-and-egress/outbound-connector.md` | 0 | 3,469 | 3,469 |
| GIT | `capabilities/network-and-egress/git-access.md` | 0 | 3,447 | 3,447 |
| HOLD | `capabilities/security-and-compliance/legal-hold.md` | 2,353 | 1,056 | 3,410 |
| CLAIM | `capabilities/runtimes-and-pools/pod-claims.md` | 3,396 | 0 | 3,396 |
| SDERIVE | `capabilities/sessions/derive.md` | 1,177 | 2,188 | 3,365 |
| RESID | `capabilities/security-and-compliance/data-residency.md` | 3,197 | 160 | 3,357 |
| OPSMCP | `interfaces/ops-api/mcp-management.md` | 3,300 | 54 | 3,355 |
| APICONV | `cross-cutting/api-conventions.md` | 3,310 | 0 | 3,310 |
| INSTALL | `capabilities/deployment-and-lifecycle/installation.md` | 3,297 | 0 | 3,297 |
| DCFGTIER | `interfaces/deployment-configuration/tier-presets.md` | 3,280 | 0 | 3,280 |
| DATACLASS | `cross-cutting/data-classification-and-retention.md` | 2,236 | 1,042 | 3,278 |
| SECMODEL | `cross-cutting/security-model.md` | 2,730 | 547 | 3,277 |
| FORENSIC | `capabilities/security-and-compliance/quarantine-and-evidence.md` | 240 | 3,028 | 3,268 |
| UPGRADE | `capabilities/deployment-and-lifecycle/platform-upgrades.md` | 3,259 | 0 | 3,259 |
| GWSCALE | `components/gateway/scaling-and-capacity.md` | 3,258 | 0 | 3,258 |
| WSPLANIF | `interfaces/workspace-plan.md` | 3,178 | 0 | 3,178 |
| CAPISES | `interfaces/client-api/sessions.md` | 1,685 | 1,450 | 3,136 |
| AVAIL | `cross-cutting/availability-and-degradation.md` | 3,120 | 0 | 3,120 |
| PKI | `cross-cutting/mtls-and-pki.md` | 2,547 | 565 | 3,112 |
| SIEM | `capabilities/security-and-compliance/siem-export.md` | 2,211 | 900 | 3,112 |
| RUNTIME | `capabilities/runtimes-and-pools/runtime-registry.md` | 2,749 | 357 | 3,107 |
| SLOTOPS | `capabilities/sessions/slot-assignment-and-failure.md` | 2,527 | 561 | 3,088 |
| ADDON | `capabilities/credentials/credential-add-ons.md` | 0 | 3,072 | 3,072 |
| ACTSTR | `capabilities/security-and-compliance/agent-activity-streams.md` | 0 | 3,039 | 3,039 |
| BUDGET | `capabilities/billing-and-usage/budgets.md` | 2,083 | 921 | 3,004 |
| SUSP | `capabilities/sessions/session-lifecycle/suspension-and-dormancy.md` | 1,102 | 1,878 | 2,981 |
| DLG | `capabilities/delegation/delegation.md` | 2,633 | 308 | 2,942 |
| REPOAUTH | `capabilities/credentials/repository-authorization.md` | 0 | 2,931 | 2,931 |
| RTPFS | `interfaces/runtime-protocol/filesystem-and-environment.md` | 628 | 2,269 | 2,898 |
| COMPAT | `cross-cutting/upgrade-and-compatibility.md` | 2,509 | 331 | 2,840 |
| TOKSVC | `components/token-service.md` | 2,600 | 115 | 2,715 |
| PODVOL | `components/agent-pod/volumes-and-quotas.md` | 0 | 2,711 | 2,711 |
| EGDEF | `capabilities/network-and-egress/egress-defaults.md` | 0 | 2,696 | 2,696 |
| RTPHAND | `interfaces/runtime-protocol/handshake-and-capabilities.md` | 818 | 1,874 | 2,692 |
| SCREATE | `capabilities/sessions/session-lifecycle/session-creation.md` | 2,092 | 589 | 2,682 |
| OPSEV | `capabilities/observability-and-operations/operational-events.md` | 2,651 | 0 | 2,651 |
| INPUT | `capabilities/messaging/input-requests.md` | 1,443 | 1,207 | 2,651 |
| CHINTRA | `interfaces/channels/intra-pod.md` | 2,044 | 602 | 2,646 |
| PSC | `components/pool-scaling-controller.md` | 2,529 | 116 | 2,645 |
| EGDEC | `capabilities/network-and-egress/egress-decisions.md` | 0 | 2,627 | 2,627 |
| CLIINST | `interfaces/cli/install-and-local.md` | 2,616 | 0 | 2,616 |
| WEBHOOK | `components/admission-webhooks.md` | 2,214 | 383 | 2,598 |
| STORPG | `interfaces/stored-schemas/postgres.md` | 2,452 | 110 | 2,562 |
| RTPSDK | `interfaces/runtime-protocol/runtime-sdks.md` | 1,633 | 928 | 2,561 |
| NETAGENT | `components/agent-pod/netagent.md` | 352 | 2,189 | 2,542 |
| SEND | `capabilities/sessions/session-lifecycle/session-end.md` | 2,163 | 373 | 2,537 |
| OPSVC | `capabilities/network-and-egress/operator-services.md` | 0 | 2,533 | 2,533 |
| OBSCONV | `cross-cutting/observability-conventions.md` | 2,528 | 0 | 2,528 |
| INTCPT | `capabilities/policy-and-controls/request-interceptors.md` | 1,782 | 744 | 2,526 |
| IMSG | `capabilities/messaging/inter-session-messaging.md` | 2,181 | 337 | 2,518 |
| WKSTART | `overview/walkthroughs/session-start.md` | 2,476 | 0 | 2,476 |
| NETISO | `capabilities/network-and-egress/network-isolation.md` | 2,007 | 435 | 2,442 |
| CTOOL | `capabilities/messaging/client-tools.md` | 0 | 2,420 | 2,420 |
| LEASEXT | `capabilities/delegation/lease-extension.md` | 2,405 | 0 | 2,405 |
| TENISO | `cross-cutting/tenant-isolation.md` | 669 | 1,717 | 2,387 |
| SCRUBPROC | `capabilities/sessions/scrub-procedure.md` | 2,380 | 0 | 2,380 |
| CLIADM | `interfaces/cli/admin-commands.md` | 2,297 | 0 | 2,297 |
| CREQ | `capabilities/credentials/credential-request-rules.md` | 0 | 2,291 | 2,291 |
| BILLEV | `capabilities/billing-and-usage/billing-events.md` | 2,275 | 0 | 2,275 |
| LOCALDEV | `capabilities/deployment-and-lifecycle/local-development.md` | 2,238 | 0 | 2,238 |
| ADAPTER | `components/agent-pod/adapter.md` | 2,113 | 115 | 2,228 |
| BROWSER | `capabilities/tenancy-and-access/browser-sessions.md` | 2,214 | 0 | 2,214 |
| DAVAIL | `capabilities/delegation/delegation-availability.md` | 0 | 2,213 | 2,213 |
| RTPOPS | `interfaces/runtime-protocol/runtime-ops.md` | 2,207 | 0 | 2,207 |
| DIAG | `capabilities/observability-and-operations/health-and-diagnostics.md` | 2,198 | 0 | 2,198 |
| ARCH | `overview/architecture.md` | 1,996 | 165 | 2,162 |
| EXT | `interfaces/extension-points.md` | 2,140 | 0 | 2,140 |
| CHSTORE | `interfaces/channels/gateway-to-store.md` | 2,109 | 0 | 2,109 |
| INTCPTIF | `interfaces/interceptor-protocol.md` | 1,584 | 512 | 2,096 |
| ATTR | `capabilities/billing-and-usage/resource-attribution.md` | 0 | 2,019 | 2,019 |
| WKCKPT | `overview/walkthroughs/checkpoint-capture.md` | 2,002 | 0 | 2,002 |
| TIMEOUT | `capabilities/sessions/session-lifecycle/timeouts.md` | 1,893 | 104 | 1,998 |
| UPLOAD | `capabilities/workspaces-and-files/uploads.md` | 1,618 | 379 | 1,997 |
| GITMIR | `components/git-mirror.md` | 0 | 1,988 | 1,988 |
| ADMRT | `interfaces/admin-api/runtimes.md` | 919 | 1,069 | 1,988 |
| REPO | `capabilities/workspaces-and-files/repository-stores.md` | 0 | 1,944 | 1,944 |
| EVTBILL | `interfaces/events/billing-events.md` | 1,191 | 738 | 1,929 |
| GWLLM | `components/gateway/metering-proxy.md` | 1,699 | 222 | 1,922 |
| TENANT | `capabilities/tenancy-and-access/tenants.md` | 1,882 | 0 | 1,882 |
| PSEUDO | `capabilities/security-and-compliance/pseudonymization-and-receipts.md` | 1,882 | 0 | 1,882 |
| REMED | `capabilities/observability-and-operations/remediation-and-escalation.md` | 1,859 | 0 | 1,859 |
| CLIOPS | `interfaces/cli/ops-commands.md` | 1,854 | 0 | 1,854 |
| OPSLIFE | `interfaces/ops-api/backup-and-upgrades.md` | 1,836 | 0 | 1,836 |
| OPSREM | `interfaces/ops-api/remediation-and-operations.md` | 1,800 | 0 | 1,800 |
| CAPIMSG | `interfaces/client-api/messages.md` | 449 | 1,325 | 1,775 |
| EGPROXY | `components/egress-proxy.md` | 435 | 1,313 | 1,749 |
| SECROLE | `capabilities/tenancy-and-access/security-roles-and-approvers.md` | 0 | 1,691 | 1,691 |
| ROUTE | `capabilities/runtimes-and-pools/runtime-routes.md` | 742 | 945 | 1,687 |
| CHEGRESS | `interfaces/channels/pod-egress.md` | 1,510 | 167 | 1,677 |
| EGAUTHZ | `components/egress-decision-service.md` | 352 | 1,313 | 1,666 |
| ADMTEN | `interfaces/admin-api/tenants-and-environments.md` | 814 | 813 | 1,627 |
| CAPIEVT | `interfaces/client-api/event-stream.md` | 1,202 | 424 | 1,626 |
| WKSTOP | `overview/walkthroughs/interrupt-terminate-delete.md` | 1,601 | 0 | 1,601 |
| BREAKER | `capabilities/policy-and-controls/circuit-breakers.md` | 1,593 | 0 | 1,593 |
| KADMIT | `interfaces/kubernetes/admission.md` | 1,590 | 0 | 1,590 |
| ADMPOL | `interfaces/admin-api/policies.md` | 1,529 | 0 | 1,529 |
| ARTIFACT | `capabilities/workspaces-and-files/artifacts.md` | 1,492 | 0 | 1,492 |
| LOPSAGG | `components/lenny-ops/gateway-fan-out.md` | 1,486 | 0 | 1,486 |
| RUNBOOK | `capabilities/observability-and-operations/runbooks.md` | 1,458 | 0 | 1,458 |
| DRIFT | `capabilities/observability-and-operations/configuration-drift.md` | 1,434 | 0 | 1,434 |
| TIER | `capabilities/deployment-and-lifecycle/capacity-tiers.md` | 1,424 | 0 | 1,424 |
| OWNPOOL | `capabilities/runtimes-and-pools/owned-pools-and-reservations.md` | 0 | 1,373 | 1,373 |
| WKRESUME | `overview/walkthroughs/restore-and-resume.md` | 1,364 | 0 | 1,364 |
| KAPOD | `interfaces/kubernetes/agentpod.md` | 525 | 807 | 1,333 |
| WKCOTEN | `overview/walkthroughs/shared-pod-co-tenancy.md` | 1,303 | 0 | 1,303 |
| RTPCONF | `interfaces/runtime-protocol/conformance.md` | 1,097 | 198 | 1,296 |
| KPSET | `interfaces/kubernetes/podset.md` | 610 | 679 | 1,289 |
| APOOLS | `interfaces/admin-api/pools.md` | 488 | 747 | 1,235 |
| PRIN | `overview/principles-and-non-goals.md` | 1,200 | 0 | 1,200 |
| ADMIT | `capabilities/policy-and-controls/admission-and-fairness.md` | 1,076 | 104 | 1,180 |
| AGDNS | `components/agent-dns.md` | 571 | 582 | 1,154 |
| GWBARRIER | `components/gateway/checkpoint-barrier.md` | 1,133 | 0 | 1,133 |
| FILEX | `capabilities/workspaces-and-files/file-export.md` | 795 | 325 | 1,120 |
| KPCLAIM | `interfaces/kubernetes/podclaim.md` | 525 | 590 | 1,116 |
| BKJOB | `components/lenny-ops/backup-jobs.md` | 1,115 | 0 | 1,115 |
| ADMAN | `interfaces/adapter-manifest.md` | 1,107 | 0 | 1,107 |
| NAMING | `cross-cutting/naming.md` | 829 | 219 | 1,048 |
| CERTM | `components/external/cert-manager.md` | 1,023 | 0 | 1,023 |
| OUTGW | `components/outbound-gateway.md` | 0 | 1,017 | 1,017 |
| PROM | `components/external/prometheus.md` | 1,006 | 0 | 1,006 |
| WKDRAIN | `overview/walkthroughs/gateway-drain.md` | 1,001 | 0 | 1,001 |
| DCFGSEED | `interfaces/deployment-configuration/bootstrap-seed.md` | 1,000 | 0 | 1,000 |
| OPSHEALTH | `interfaces/ops-api/health-and-diagnostics.md` | 995 | 0 | 995 |
| KUBE | `components/external/kubernetes.md` | 981 | 0 | 981 |
| RCLASS | `capabilities/runtimes-and-pools/resource-classes.md` | 0 | 963 | 963 |
| WSPLAN | `capabilities/workspaces-and-files/workspace-plan.md` | 957 | 0 | 957 |
| WKHANDOFF | `overview/walkthroughs/coordinator-handoff.md` | 956 | 0 | 956 |
| CHPODGW | `interfaces/channels/pod-to-gateway.md` | 923 | 0 | 923 |
| CLISCAF | `interfaces/cli/runtime-scaffolding.md` | 899 | 0 | 899 |
| ADMIAM | `interfaces/admin-api/identity-and-access.md` | 880 | 0 | 880 |
| DCFGANS | `interfaces/deployment-configuration/answer-files.md` | 879 | 0 | 879 |
| SETUP | `capabilities/sessions/setup-commands.md` | 503 | 369 | 872 |
| EVTOPS | `interfaces/events/operational-events.md` | 865 | 0 | 865 |
| STORREDIS | `interfaces/stored-schemas/redis-keys.md` | 855 | 0 | 855 |
| CHCTRL | `interfaces/channels/control-plane.md` | 855 | 0 | 855 |
| IFMAP | `overview/interface-map.md` | 367 | 481 | 848 |
| OPSAUD | `interfaces/ops-api/audit-and-drift.md` | 799 | 0 | 799 |
| RESTORE | `capabilities/sessions/checkpoints/restore.md` | 0 | 749 | 749 |
| OPSEVT | `interfaces/ops-api/events-and-webhooks.md` | 694 | 0 | 694 |
| WKEVICT | `overview/walkthroughs/pod-eviction.md` | 692 | 0 | 692 |
| USAGE | `capabilities/billing-and-usage/usage-records.md` | 488 | 164 | 653 |
| ADMCRED | `interfaces/admin-api/credentials-and-upstreams.md` | 651 | 0 | 651 |
| CAPIFILE | `interfaces/client-api/files-and-artifacts.md` | 650 | 0 | 650 |
| OUTAGT | `components/outbound-agent.md` | 0 | 610 | 610 |
| WKOPS | `overview/walkthroughs/ops-remediation.md` | 590 | 0 | 590 |
| GWCLAIM | `components/gateway/pod-claimer.md` | 588 | 0 | 588 |
| GWOPS | `components/gateway/ops-endpoints.md` | 572 | 0 | 572 |
| DNS | `capabilities/network-and-egress/dns.md` | 557 | 0 | 557 |
| ADMCOMP | `interfaces/admin-api/compliance.md` | 488 | 0 | 488 |
| OPSME | `interfaces/ops-api/caller-identity.md` | 484 | 0 | 484 |
| CAPIDISC | `interfaces/client-api/discovery-and-usage.md` | 469 | 0 | 469 |
| KMS | `components/external/kms.md` | 279 | 184 | 463 |
| ISO | `capabilities/runtimes-and-pools/isolation-profiles.md` | 438 | 0 | 438 |
| EVTHOOK | `interfaces/events/session-webhooks.md` | 383 | 0 | 383 |
| STOROBJ | `interfaces/stored-schemas/object-store-layout.md` | 0 | 367 | 367 |
| CHREPLICA | `interfaces/channels/inter-replica.md` | 358 | 0 | 358 |
| RATE | `capabilities/billing-and-usage/rate-cards.md` | 0 | 347 | 347 |
| PODINIT | `components/agent-pod/init-and-support-containers.md` | 0 | 331 | 331 |
| ADMBILL | `interfaces/admin-api/billing.md` | 325 | 0 | 325 |
| CAPICRED | `interfaces/client-api/credentials-and-links.md` | 282 | 0 | 282 |
| CTX | `overview/purpose-and-context.md` | 198 | 0 | 198 |
| KRUNTIME | `interfaces/kubernetes/runtime.md` | 45 | 0 | 45 |
| KLABEL | `interfaces/kubernetes/labels-and-annotations.md` | 0 | 0 | 0 |
| CAPMAP | `overview/capability-map.md` | 0 | 0 | 0 |
