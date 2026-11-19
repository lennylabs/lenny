# Steps: 0117 Gateway package layout

## Validation owed

| Item | Waits on it |
| :-- | :-- |
| A throwaway split of the upload handler and of the platform tools' calls into the core, on a spike branch, to confirm the operations of "SP-2" before the specification states them: every edge listed under "Current state" either goes through a seam interface method or is shown to need one the list lacks. | 0117.S1 |
| The cost of one move with the driver after 0116 has retired the path-keyed maps: files and lines changed per import site, measured on the first move step, against the spike's 102 files and 593 lines for the pod claimer. | Nothing; the steps after 0117.S3 use it to check their size. |
| The assignment of each package in [evidence/packages.tsv](evidence/packages.tsv), re-run against the tree when 0117.S2 lands, because member changes delete and add packages before then. | 0117.S2 |

## Steps

The import sites in each title are counted over the packages the step moves, as [evidence/packages.tsv](evidence/packages.tsv) gives them on 2026-11-16. Every step after 0117.S2 removes its rows from the layout gate's register. The code registry and placeholder pages of 0115 must have merged before 0117.S1 starts and before any `doc.go` names a page code; 0115's steps are framed on its branch, so the edge is stated here rather than listed.

### 0117.S1 The seam interface page and the import rule
- status: Landed
- lanes: [specification]

`spec/interfaces/gateway-seams.md` written with the operations of "SP-2", each with its identifier, caller, callee, and result, and GW-R1 on `spec/components/gateway/index.md` with the page marked `status: partial`. §4.1 "Gateway Internal Subsystems" gains a link to the seam interface page. It lands after the validation owed on the operations list.

### 0117.S2 The seam interface package, the layout gate, and the move driver
- status: Landed
- lanes: [specification, code, documentation]
- depends-on: [0117.S1, 0155.S1]

`pkg/gateway/seam` with the interfaces of "SP-2", including the route registration interface, which the later steps implement; the tier-0 layout gate of "SP-3" with its fixtures; `tests/registers/gateway-layout.yaml` with every violation and every package awaiting removal that exists when it lands; `scripts/refactor` with 0117's manifest and the register rewrites of "SP-4", the retirement of the layout tests in `scripts/refactor/manifest_layout_test.go`, `pkg/gateway/ARCHITECTURE.md` rewritten as the map of "SP-1", and the bullet in `code-best-practices.md`. It follows 0155.S1 so the gate is written in the renamed static tier. Under decision 9, GW-R1 states the wiring form of the rule, and a static check of `cmd/lenny-gateway` refuses a value of one part passed where another part expects an interface not declared in `pkg/gateway/seam`, with today's crossings in `tests/registers/gateway-wiring.yaml`, which only shrinks. Under decision 10, GW-R2 confines the credential and admission packages, and the layout gate holds it, with today's other importers in `tests/registers/gateway-layout.yaml`.

### 0117.S3 Metering proxy (51 import sites)
- status: Landed
- lanes: [specification, code]
- depends-on: [0117.S2, 0137.S5, 0155.S1]

`llmproxy/llmproxy` and `session/sessionbudget` to `pkg/gateway/meteringproxy`. The removed `llmproxy` packages of 0132 and 0133 stay where they are. 0133.S2 then edits the proxy at its new path. The seam interface page gains `GWSEAM-ExpireSession` and `GWSEAM-RotateCredential`, served by the session core to the metering proxy (decision 20, settled by the owner on 2026-11-18,), and the metering proxy's budget operations split into one interface per caller. The usage recorder leaves `cmd/lenny-gateway` for a new package, `pkg/gateway/meteringproxy/metering`, with the per-session budget state the proxy serves through the seam interface, and the proxy's flags and route registration move into `llmproxy` (decision 7).

### 0117.S4 Seam model, replica-level parts, and the playground (108 import sites)
- status: Landed
- lanes: [specification, code]
- depends-on: [0117.S2, 0137.S5, 0155.S1]

`core/subsystem` and `metrics/extractionthreshold` to `pkg/gateway/seam`; `core/semver` to `pkg/common`; the drain, scaling, ops endpoint, and replica packages of "SP-1"; and `mcpfabric/playground` to `pkg/gateway/playground`. §4.0 takes the new path of `operability/health`. The tier-0 tests that name these paths take the new ones. The step routes the crossings its moves leave: the seam model gates the Token Service read through `Subsystem.Admit` and receives no Token Service value; the request-metrics middleware moves to the API front as `pkg/gateway/api/requestmetrics`; the audit appender moves from the policy package to `pkg/audit`; the T4 KMS probe runs from the session core's `pkg/gateway/core/leaderjobs`; the metering proxy registers its own metric collectors; and GW-R2 allows the playground the packages its token mint reads (decisions 21 to 25). The flags only the playground, the startup TLS probe, the disruption budget watcher, and the leader election read move into those packages under decision 7.

### 0117.S5 Upload handler split
- status: Landed
- lanes: [specification, code]
- depends-on: [0117.S2, 0137.S5, 0155.S1]

`sessionserver`'s five upload files and their tests to a new package, `pkg/gateway/uploadhandler`, which reaches the core through the session lookup, request forwarding, and workspace write operations of the seam interface, with authorization applied by each route's access check, and registers its routes with the API front. The archive extraction in `podsession` stays until 0153.S3 removes it. The seam interface page gains `GWSEAM-RequestTenant`, served by the API front, and `GWSEAM-VerifyUploadToken`, `GWSEAM-ReserveStorage`, `GWSEAM-AdjustStorage`, and `GWSEAM-AuditUpload`, served by the session core to the upload handler, because the upload token, quota, tenant, and audit packages the handler used stay with the core under GW-R2 (open decisions 31 to 33). `GWSEAM-ForwardRequest` gets an interface of its own, and `GWSEAM-WriteWorkspace` carries each file's mode and names the phase that failed. Until 0117.S8, the session server serves the route registration interface on the mux the client and forward listeners serve, and the upload handler's flags and route registration move out of `cmd/lenny-gateway` (decision 7).

### 0117.S6 Stream proxy split (69 import sites)
- status: Landed
- lanes: [specification, code]
- depends-on: [0117.S2, 0137.S5, 0155.S1]

`session/sessionevents` to `pkg/gateway/streamproxy`, and `sessionserver`'s `events.go` and `logs.go` with their tests to a new package there. Every publisher, in the core and in the platform tools seam, reaches the event bus through the publish operation of the seam interface. The event bus serves the seam's event operations itself, and the platform tools seam's attach stream, the dual-store monitor's broadcast, and the eviction bridge reach it through them too. The event and log streams reach the core through the session lookup, the handoff frame operations, and `GWSEAM-RequestTenant`, which 0117.S5 also adds, and register their routes through the session server's interim route registration, as 0117.S5 arranges it. `GWSEAM-Subscribe` returns the retained events with the live channel, which the attach stream and the event and log streams replay before they tail. The replay buffer depth flag moves into the bus's package (decision 7).

### 0117.S7 Platform tools seam (428 import sites)
- status: Landed
- lanes: [specification, code]
- depends-on: [0117.S6, 0137.S5, 0155.S1]

The `mcpfabric` packages that no change removes, `gatewaycontrol/*`, `session/inputwait`, and `session/toolapproval` to `pkg/gateway/platformtools`, and `sessionserver`'s twelve platform tools files to a new package there. The platform tools' imports of `sessionserver`, `podsession`, `executor`, `messagerouting`, `sessioninbox`, `runtime/adapter`, and `adapterclient` become calls on the seam interface, and the core's imports of the delegation tree, `mcp`, `platformtools`, `inputwait`, and `toolapproval` become calls on the interfaces this seam serves. It follows 0117.S6 because its publishers use the stream proxy's publish operation. `mcpfabric/delegationpolicystore` leaves with the domain packages in 0117.S9. `mcptools/internal/genmcpschemas`, the `go:generate` tool of `mcptools` that 0138 removes, moves with `mcptools`, which runs it by a relative path, as an exception to decision 5: this step deletes its register row, and it belongs to the platform tools seam until 0138 removes it. When the step adds seam operations, it also adds the line of decision 8 to the member change behind each mechanism those operations serve, such as 0141.S1's correction of the delegation `PreRoute`.

As built: the packages keep their names under `pkg/gateway/platformtools`, except the GatewayControl platform tool bridge `gatewaycontrol/platformtools`, which becomes `platformtools/toolbridge` (decision 6). Of `sessionserver`'s twelve platform tools files, five move with their tests into the new package `platformtools` (`memory.go`, `interactions.go`, `interactions_resolution.go`, `tree.go` as `tree_rest.go`, and `toolapproval.go`), and `delegation_seams.go` is deleted: the core reaches the tree budget counters its interfaces named through `seam.TreeBudgets`, and its high-watermark observer moves to `hwm_observer.go`. The package's `Handler` serves the memory route, the delegation tree read, the tool-use approve and deny routes, and the elicitation respond and dismiss routes, and registers them through `seam.Routes`, which the session server serves until 0117.S8; `tests/testinfra/toolwire.Attach` attaches it in the harnesses as the composition root does. The tool approval gate moves with `toolapproval.go` and serves `GWSEAM-AwaitApproval` to the pod executor, and `sessionserver.Options` loses, among others, `Memory`, `InteractionAuditSink`, `ToolApprovalWaits`, and `TreeCycleObserver`. It gains `TreeRecovery`, `TreeRollups`, `TreeBudgets`, and `TreeOrphans`, which the seam serves and the composition root and the lifecycle harness wire; a session server built without them runs no delegation tree recovery on resume and does not enforce the orphan cap, unlike `main`, where the session server built both itself. The other six files (`budget_keys_expired.go`, `budget_rearm.go`, `children_reattached.go`, `delegated_child_materialize.go`, `elicitation_expiry.go`, and `treerecovery.go`) stay in the session core, because they hold the core's side of operations the seam interface page assigns to the session core, such as `GWSEAM-MaterializeChild`, `GWSEAM-StartNode`, `GWSEAM-ReattachNode`, `GWSEAM-FailNode`, `GWSEAM-HandleBudgetKeysExpired`, and `GWSEAM-ChildrenReattachedFrame`. The session core serves the seam through `sessionserver.ToolsSeam` and the confined packages' operations through `pkg/gateway/core/toolops`, and the seam serves the core through `seam.DelegationTree`'s split interfaces with adapters in `platformtools`. The seam registers its own metric collectors, the delegation tree cycle counter among them (open decision 54). Open decisions 51 to 54 record the choices the build made with their recommendations built. The lines of decision 8 went to 0118, 0127, 0133, 0135, 0141, 0145, 0146, 0149, and 0151; 0127 and 0149 are not on decision 8's list, and they carry lines because the operations this step adds serve mechanisms they reshape or remove (`GWSEAM-IssueVCSToken`; `GWSEAM-ResolveEnvironments`, `GWSEAM-RequestCaller`, `GWSEAM-DelegatorRefusal`, and `GWSEAM-AdmitActiveCaller`).

### 0117.S8 API front and the shared API conventions (497 import sites)
- status: Planned
- lanes: [code]
- depends-on: [0117.S5, 0117.S6, 0117.S7, 0137.S5, 0155.S1]

`middleware/auth` and `callercheck` to `pkg/auth`, `middleware/idempotency` to `pkg/idempotency`, `middleware/environment` to `pkg/environment`, and `externalapi/{errorclassify, pagination, openapi}` to `pkg/api`; the rest of `middleware`, `serviceidentity`, and `externalapi/{admin, admintoken}` to `pkg/gateway/api`; and `sessionserver`'s pipeline files to a new package there with the route registration interface of decision 3. `quota/quotacheckpoint`'s adapter for `admin.QuotaReconciler` moves to the admin package's side so that the quota package imports no part. Until this step, the session server serves the route registration interface and mounts the routes the split packages register on its own mux, which the client and forward listeners serve (0117.S5). It follows the three splits so that the plumbing files it moves are the ones left in `sessionserver` after them. When the step adds seam operations, it also adds the line of decision 8 to the member change behind each mechanism those operations serve, such as 0141.S1's correction of the delegation `PreRoute`.

### 0117.S9 Domain packages, first group (694 import sites)
- status: Planned
- lanes: [specification, code]
- depends-on: [0117.S2, 0137.S5, 0155.S1]

The `audit`, `billing`, `quota`, and `policy` packages, the checkpoint retention and partial manifest stores, `mcpfabric/delegationpolicystore`, `eventbuffer`, `operability/recommendations`, the CA rotation packages, and the `provisioning` packages to their concern directories. §4.0 takes the new paths of `eventbuffer` and `operability/recommendations`. `pkg/ops`, `pkg/ctlcli`, `cmd/lenny-ops`, `cmd/lenny-token-service`, and `cmd/lenny-backup` take the new paths.

### 0117.S10 Domain packages, second group (905 import sites)
- status: Planned
- lanes: [code]
- depends-on: [0117.S2, 0137.S5, 0155.S1]

The `credentials` packages that 0133 keeps, the `storage` packages outside the core, the runtime and pool stores, `runtimecapoverride`, `capabilityinference`, and the `upgrade` packages to their concern directories. `pkg/controller`, `pkg/tokenservice`, `pkg/mtls`, `pkg/storerouter`, and `cmd/lenny-pool-scaling-controller` take the new paths.

### 0117.S11 Session core (1,020 import sites)
- status: Planned
- lanes: [code]
- depends-on: [0117.S5, 0117.S6, 0117.S7, 0117.S8, 0137.S5, 0155.S1]

The rest of `sessionserver` and the core packages of "SP-1" to `pkg/gateway/core`, its domain files to their concern packages, the claim-object helpers that `pkg/podregistry` and `pkg/podlifecycle` call out of `podclaim`, and the subprocess executor that `cmd/lenny-adapter` builds out of `executor`. It follows every split, because what remains of `sessionserver` after them is the core.

### 0117.S12 Session and tenancy stores (1,909 import sites)
- status: Planned
- lanes: [code, documentation]
- depends-on: [0117.S2, 0137.S5, 0155.S1]

`session/sessionstore` with its implementations, `sessionlogstore`, `sessioncheckpointmeta`, `sessionusage`, `interactionstore`, and the `environment` stores to `pkg/session`, `pkg/billing`, and `pkg/tenancy`. `pkg/controller`, `pkg/embedded`, `pkg/delegation`, `cmd/lenny-ops`, and `cmd/lenny-controller` take the new paths. `TESTING.md` and the test READMEs take the new paths. With this step the layout gate's register holds only the packages awaiting removal.

### Notes on the steps

- A move step deletes the rows of the violations it removes and adds no row to the layout or the wiring register (decision 15). A row whose paths the step's move rewrites maps back to its old paths; a crossing or an import the step would create goes through the seam interface instead.
- Steps 0117.S3 to 0117.S12 are refactors under the escape hatch of `spec-driven-development.md` for a change that alters no behavior: each passes the same tests before and after, less the wiring tests "SP-4" deletes.
- The domain steps 0117.S9, 0117.S10, and 0117.S12 depend on nothing but the gate and may run in any order relative to the splits; they are numbered by import sites. Steps that share no package may share a branch, as `changes/README.md` allows.
- Member changes whose code steps edit a package this change moves land after the step that moves it, which milestone M2's place before the areas gives. 0153.S3 removes the archive extraction from the core after 0117.S11, and 0133.S2, 0138.S6, and 0124's removal delete packages the register lists.

### 0117.S13 Move what removals left
- status: Planned
- lanes: [code, documentation]
- depends-on: [0117.S12]

Runs before v1 closes, after the member changes that remove packages listed in the layout gate's register have landed (decision 5). Every package in that register that its change did not delete, and every part of one that remains, moves into the layout as decision 2 places it and is named as decision 6 states. The step lands when the register is empty. A leftover it moves under a confined path stays confined under GW-R2: `credfallback`, `credrenewal`, and `leaserevoke`, which 0133 removes, and `connectorauthz`, which 0124 removes.

### Notes on package names

Each move step, 0117.S3 to 0117.S13, settles the names of the packages it moves under decision 6, in the same step: a package whose name does not fit its component page or concern is renamed there, with its importers, and none is left for a final pass.

