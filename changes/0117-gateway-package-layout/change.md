# Gateway package layout

Status: `Decided`, revision 2, 2026-11-17. Programs: [v1](../programs/v1.md), milestone M2.

## Problem

The specification is moving to pages organized by capability and component (0115), and the owner settled how the gateway appears in it: one binary partitioned into seams, a session core, parts that apply to the whole replica, and a seam interface page that names the operations between each seam and the core ([TAXONOMY.md](../archive/2026-11-16-0115-spec-structure/evidence/spike/TAXONOMY.md), "Gateway seams" and "Items from the migration spike", item 9). The Go tree does not follow that model, so a reader cannot go from a component page to its code, and nothing in the tree holds a seam to its boundary.

- `pkg/gateway` holds 251 packages in 30 group directories, 199,258 lines of non-test Go and 287,993 lines of tests. Proposal 0020 chose the groups and mapped each to a numbered section of the specification. The groups import each other along 128 edges, 19 pairs of them in both directions ("Current state").
- `pkg/gateway/sessionserver` is one package of 109 files and 29,882 lines that holds the session core, the upload handler, the stream proxy, part of the platform tools seam, API plumbing, and domain logic for experiments, pools, and quotas. It imports 64 gateway packages from 20 groups.
- The seams and the core import each other directly: under the assignment of "SP-1", 25 import edges cross from a seam to the core, from the core to a seam, or from one seam to another. The specification states that each seam has its own concurrency limits and breaker, and nothing states or checks which code belongs to which seam.
- Domain stores and their logic sit inside the gateway, and 15 packages and binaries of other components import them, among them `lenny-ops`, the Token Service, the controller, and the adapter binary.

The migration spike moved one gateway package, the pod claimer, and measured the cost: 102 files and 593 lines changed each way, most of it from renaming the package and from registers keyed by path ([FINDINGS-code.md](../archive/2026-11-16-0115-spec-structure/evidence/spike-migration/FINDINGS-code.md), "Moving the package"). It recommended settling the gateway layout once, before areas move code, and the v1 program places that in milestone M2.

## Scope

- **In scope.**
  - The package tree of the gateway, mapped to the component pages of `spec/components/gateway/` and the seam interface page.
  - The seam interface: a Go package that holds the operations each seam calls on the core and the core calls on each seam, and the specification page that states them.
  - The split of `pkg/gateway/sessionserver` into the seams, the API front, the session core, and the domain packages it holds today.
  - Moving domain logic and stores out of `pkg/gateway` into concern packages under `pkg/`.
  - A tier-0 gate that holds the import rule, with a register of today's violations that may only shrink.
  - The move driver and its manifest, which record every old and new import path.
  - The registers, maps, and tests keyed by package path that each move rewrites.
- **Out of scope.**
  - Behavior. Every step is a refactor that changes no requirement, defined value, or operation, apart from the seam interface page and the import rule.
  - Packages that a member change of the program removes. They stay where they are until that change deletes them (decision 5).
  - Renaming the seams, their metrics, or their Helm values. `gateway.subsystems.mcpFabric` and `lenny_mcp_fabric_*` keep their names here; the changes that redefine the seams (0126, 0133, 0138, and 0153) own any rename.
  - Re-annotating moved code. Each area change replaces its `§` citations under 0115's annotation rule.

## Terms

| Term | Meaning |
| :-- | :-- |
| Seam | A part of the gateway that can be extracted into its own service when its load outgrows the shared process: the stream proxy, the upload handler, the platform tools seam (named MCP Fabric in §4.1), and the metering proxy (named LLM Proxy in §4.1). Each has its own concurrency limits, breaker, metrics, and extraction thresholds ([TAXONOMY.md](../archive/2026-11-16-0115-spec-structure/evidence/spike/TAXONOMY.md), "Gateway seams"). |
| Session core | The work of the gateway that is not a seam: session coordination, the pod claimer, the checkpoint barrier, and the session operations they serve (create, start, bind, resume, suspend, terminate). |
| Replica-level part | A part that applies to the whole replica and is not a seam: the API front, drain, scaling and capacity, and the ops endpoints. |
| API front | The replica-level part that owns the request pipeline: middleware order, authentication, rate limiting, handler registration, and request scope (`components/gateway/api-front.md`, code `GWAPI`). |
| Seam interface | The boundary between each seam and the core: the operations each side calls on the other (`interfaces/gateway-seams.md`, code `GWSEAM`). In code, the package `pkg/gateway/seam`. |
| Seam model | The isolation every seam has: the breaker, the limiter, the per-seam metrics, and the extraction-threshold reader. Today `pkg/gateway/core/subsystem` and `pkg/gateway/metrics/extractionthreshold`. |
| Part | A seam, the session core, or a replica-level part. |
| Domain package | A package of logic or storage whose rules live on a capability page rather than on a gateway component page. It lives in a concern directory under `pkg/`, such as `pkg/quota` or `pkg/audit`. |
| Concern directory | A directory under `pkg/` named for one concern, which the existing `pkg/audit`, `pkg/quota`, `pkg/credential`, and `pkg/delegation` already are (decision 2). |
| Import site | A Go file, tests included, that imports a given package. A move rewrites one line in each import site. |
| Move manifest | The committed list of old and new import paths that the move driver applies, one line per move. It is the translation table for any text that still names an old path. |
| Layout gate | The tier-0 test that reads the import graph and refuses an import that crosses a part boundary other than through the seam interface. |
| Labels | The steps of this change carry `0117.Sn`. Requirement identifiers written here as `GW-R1` and `GWSEAM-<operation>` are numbered when the pages are written. |

## High-level design

The gateway's code follows its component pages. Each seam is a directory under `pkg/gateway` (`streamproxy`, `uploadhandler`, `platformtools`, and `meteringproxy`), the session core is `pkg/gateway/core`, and each replica-level part has its own directory (`api`, `drain`, `scaling`, `opsendpoints`, and `replica` for what the gateway index owns). A new package, `pkg/gateway/seam`, holds the seam interface: the Go interfaces each seam uses to reach the core, the interfaces the core uses to reach each seam, and the route registration interface the API front implements. The seam model moves under it.

A seam reaches the core and every other seam only through `pkg/gateway/seam`. The core imports no seam. The API front imports neither and receives each part's routes through the registration interface. `cmd/lenny-gateway` is the only package that imports every part, and it wires the concrete types into the interfaces as it does today. Extracting a seam then means putting a transport under interfaces that already exist.

Domain logic and stores leave `pkg/gateway` for concern directories under `pkg/`, where any component can import them (decisions 1 and 2). After the change, no package outside `pkg/gateway` and `cmd/lenny-gateway` imports a gateway package, tests aside.

`sessionserver` splits along the same lines. Its upload, stream, and platform tools files move to their seams, its request plumbing to the API front, and its domain files to their concern packages. What remains is the session core's session operations, one package under `pkg/gateway/core` (decision 4).

Every move is a scripted path rewrite that keeps package names (decision 6). The steps run in order of import sites, fewest first, and the layout gate lands before the first move with today's violations registered, so each step removes rows and none can add one.

## Requirements

- **GW-R1, added on the gateway index.** Each seam reaches the session core and every other seam only through the operations of the seam interface. The session core reaches a seam only through the seam interface. The layout gate holds the Go form of this rule.
- **GWSEAM operations, added.** One operation per method of the seam interface, named by its Go method under 0115's operation format (`GWSEAM-LookupSession`), each stating its caller, its callee, and what it returns. "SP-2 The seam interface" lists them.
- No other requirement changes. §4.1 "Gateway Internal Subsystems" keeps its isolation guarantees, which the gateway index and the seam pages take over when their area migrates.

## Current state

Measured on 2026-11-16 at `b1248f85b`. The per-package numbers are in [evidence/packages.tsv](evidence/packages.tsv).

**The tree.** `pkg/gateway` holds 251 packages, 199,258 lines of non-test Go, and 287,993 lines of tests. `pkg/gateway/ARCHITECTURE.md` records the 30 groups of proposal 0020 Part B, executed by `scripts/refactor` from a 215-line manifest. `scripts/refactor/manifest_layout_test.go` pins that layout (`TestMCPFabricSubsystemIsOneExtractableSubtree`, `TestCommittedManifestDestinationsExist`). 87 packages are stores (40 store interfaces and their Postgres, Redis, and memory implementations), with 40,914 lines.

**Group edges.** The groups import each other along 128 edges, and 19 pairs import each other in both directions, among them `session` and `mcpfabric`, `runtime` and `session`, and `mcpfabric` and `sessionserver`. Go forbids cycles between packages and allows them between directories, so the groups hide the dependencies that a seam boundary has to cut.

**How today's layout avoids package cycles.**

- `sessionserver` declares 34 interfaces that `cmd/lenny-gateway` satisfies with concrete types (`TreeBudgetReturner` with `*treebudget.Reserver`, `CoordinationFencer` with `*coordfence.Fencer`, and others in `delegation_seams.go`, `coordination_seams.go`, `checkpoint_seams.go`, and `session_hooks.go`). `mcptools` declares 16. `cmd/lenny-gateway` declares 103 struct types, most of them adapters between these interfaces.
- The platform tools reach session creation by importing `sessionserver` (`mcptools` calls its `ServiceCall` and `CreateSessionService`), while `sessionserver` imports other packages of the same seam (`mcpfabric/mcp`, `gatewaycontrol/platformtools`, and six `delegationtree` packages). The cycle is avoided because the two directions use different packages of the seam.
- `quota/quotacheckpoint` imports `externalapi/admin` to declare that it satisfies `admin.QuotaReconciler`.

**`sessionserver`.** 109 non-test files (one of them behind the `contract` build tag) and 29,882 lines, 69,236 lines of tests, and a `Server` struct of about 158 fields over 620 lines. It imports 64 gateway packages; `cmd/lenny-gateway`, `mcpfabric/mcptools`, and four `tests/testinfra` packages import it. By file ([evidence/sessionserver-files.tsv](evidence/sessionserver-files.tsv)):

| Part | Files | Lines | Share |
| :-- | --: | --: | --: |
| Session core: create, start, finalize, claim and bind, resume, suspend, terminal, derive, replay, messages, coordination lease, holder forwarding | 64 | 19,400 | 65% |
| HTTP plumbing and read handlers: route table, request identity, authorization gates, error envelopes, playground caps, session reads | 16 | 3,353 | 11% |
| Domain: experiment routing and evaluations, pool and runtime discovery, admission quotas, credential pre-claim, metering reads | 10 | 3,043 | 10% |
| Platform tools: tool approvals, elicitations, delegation tree reads and recovery, budget keys, memory | 12 | 1,785 | 6% |
| Upload handler | 5 | 1,626 | 5% |
| Stream proxy: the event stream and logs | 2 | 675 | 2% |

**Parts under the proposed assignment** ([evidence/measure.py](evidence/measure.py)). "Import sites" sums over packages, so a file that imports several of them counts once for each.

| Part | Packages | Code lines | Test lines | Import sites |
| :-- | --: | --: | --: | --: |
| Session core | 32 | 56,365 | 118,825 | 1,020 |
| API front | 13 | 28,468 | 42,323 | 157 |
| Platform tools | 23 | 23,048 | 39,797 | 428 |
| Replica-level parts | 11 | 9,436 | 6,347 | 69 |
| Metering proxy | 2 | 2,571 | 3,968 | 51 |
| Stream proxy | 1 | 800 | 745 | 69 |
| Web playground | 1 | 3,595 | 3,563 | 11 |
| Seam model | 2 | 877 | 1,062 | 28 |
| Domain packages that leave `pkg/gateway` | 124 | 56,511 | 52,880 | 3,848 |
| Removed by a member change | 42 | 17,587 | 18,483 | 450 |

The upload handler has no package today; its code is the five `sessionserver` files above.

**Edges the layout gate would refuse today.** With the shared API conventions counted as domain packages (they leave `pkg/gateway`, "SP-1"), these package imports cross a part boundary:

- Platform tools to core, 8: `mcptools` imports `sessionserver`, `podsession`, `executor`, `messagerouting`, `sessioninbox`, and `runtime/adapter`; `delegation/exportwire` imports `podsession` and `adapterclient`.
- Core to platform tools, 12: `sessionserver` imports `gatewaycontrol/platformtools`, `mcpfabric/mcp`, `inputwait`, `toolapproval`, and six `delegationtree` packages; `session/recycle` imports `leasecontrol`; `runtime/watchdog` imports `treearchive`.
- Core to stream proxy, 2, and platform tools to stream proxy, 3: `sessionserver`, `evictionfallback`, `mcp`, `mcptools`, and `inputwaitrecovery` publish to `sessionevents` directly.
- Domain to API front, 1: `quota/quotacheckpoint` imports `externalapi/admin`.

**Other components that import the gateway.** 798 Go files outside `pkg/gateway` import a gateway package: 178 in `cmd/lenny-gateway`, 520 under `tests/`, and 100 elsewhere. Non-test imports come from 15 packages and binaries: `pkg/ops`, `pkg/controller`, `pkg/tokenservice`, `pkg/podregistry`, `pkg/podlifecycle`, `pkg/ctlcli`, `pkg/mtls`, `pkg/storerouter`, `cmd/lenny-ops`, `cmd/lenny-token-service`, `cmd/lenny-controller`, `cmd/lenny-backup`, `cmd/lenny-adapter`, `cmd/lenny-pool-scaling-controller`, and `cmd/openapi-to-mcp`. Three of these import packages of the session core: `pkg/podregistry` and `pkg/podlifecycle` call `podclaim.ClaimName`, `CreateClaim`, `DeleteClaim`, and `WriteBoundStatus`, and `cmd/lenny-adapter` builds its runtime with `executor.NewSubprocessExecutor`.

**`cmd/lenny-gateway`.** 96 non-test files and 23,425 lines of `package main`, with 23,264 lines of tests. The largest are `flags.go` (1,431 lines), `adminrouter.go` (1,316), and `httpsurface.go` (1,062).

**Path references outside Go.** Live carriers that name gateway paths: `tests/spec-map.json` (1,350 references), `tests/registers/change-graph-coverage.yaml` (190), `tests/change-graph.json` (86), `tests/claim-map.json` (37), `tests/registers/skip-reasons.yaml` (28), `tests/registers/proposal-label-comments.yaml` (22), `TESTING.md` (3), and §4.0 of `spec/04_system-components.md` (3: `eventbuffer`, `operability/health`, and `operability/recommendations`). On the branch of 0115, `tests/registers/spec-section-citations.yaml` keys 235 gateway package directories. Ten tier-0 test files hard-code gateway paths, `spec_map_slot_address_registration_test.go` with 63 of them. The historical records (`BUILD-GAPS.md` with 3,209 references, `TEST-GAPS.md`, `gateway-runtime-comms.md`, `CODE-REVIEW.md`, `proposals/`, and `changes/`) name old paths and keep them.

**Files the kind renames touch.** 214 Go files under `pkg/gateway` and 16 under `cmd/lenny-gateway` name `Sandbox`, `SandboxClaim`, `SandboxTemplate`, `SandboxWarmPool`, or a `pkg/sandbox` path, 72 of them in `sessionserver`, 32 in `podsession`, 19 in `podclaim`, 17 in `mcptools`, 16 in `delegation`, and 13 in `externalapi/admin`. The tree-wide renames of 0137.S5 edit these files.

## Detailed design

### SP-1 The package tree

Package names stay as they are and only directories change (decision 6), so most paths below end in today's package name. Where a kept name would collide with a package already in the target directory, the move manifest gives it a qualified name.

| Directory | Page | Holds |
| :-- | :-- | :-- |
| `pkg/gateway/seam` | `interfaces/gateway-seams.md` (GWSEAM) | New. The operations of "SP-2" as Go interfaces and the value types they carry. Imports no other gateway package. |
| `pkg/gateway/seam/subsystem`, `pkg/gateway/seam/extractionthreshold` | `components/gateway/index.md` (GW), the seam model | `core/subsystem` and `metrics/extractionthreshold`. |
| `pkg/gateway/streamproxy` | `components/gateway/stream-proxy.md` (GWSTREAM) | `session/sessionevents`, and a new package from `sessionserver`'s `events.go` and `logs.go`. |
| `pkg/gateway/uploadhandler` | `components/gateway/upload-handler.md` (GWUPLOAD) | A new package from `sessionserver`'s five upload files. The archive extraction in `podsession` stays until 0153.S3 removes it. |
| `pkg/gateway/platformtools` | `components/gateway/platform-tools.md` (GWTOOLS) | `mcpfabric/{mcptools, mcp, delegation, delegationbudget, delegationtree, elicitationfloor}`, `gatewaycontrol/{platformtools, inputwaitrecovery}`, `session/{inputwait, toolapproval}`, and a new package from `sessionserver`'s twelve platform tools files. |
| `pkg/gateway/meteringproxy` | `components/gateway/metering-proxy.md` (GWLLM) | `llmproxy/llmproxy` and `session/sessionbudget`. |
| `pkg/gateway/core/coordination` | `components/gateway/session-coordination.md` (GWCOORD) | `coordination/{coordination, coordlease, coordfence}`, `storage/{leasestore, dualstore}`. |
| `pkg/gateway/core/podclaim` | `components/gateway/pod-claimer.md` (GWCLAIM) | `podlifecycle/podclaim`, less the claim-object helpers other components call, which move to `pkg/podregistry`. |
| `pkg/gateway/core/checkpoint` | `components/gateway/checkpoint-barrier.md` (GWBARRIER) | `coordination/barrier`, `checkpoint/{checkpointer, resumechunks}`, `storage/evictionfallback`. |
| `pkg/gateway/core/session` | The capability pages of the sessions and messaging areas | The rest of `sessionserver`, `podlifecycle/{podsession, bindrelease, slotreaper}`, `session/{executor, recycle, orphansession, sessionidle, sessionage, routingcache, messagerouting, sessioncallback, sessioninbox, budgetstate}`, `storage/{slotcounter, derivelock}`, and `runtime/{adapterclient, adapter, slothealth, watchdog}`. The subprocess executor that `cmd/lenny-adapter` builds leaves `executor` for a package outside the gateway. |
| `pkg/gateway/api` | `components/gateway/api-front.md` (GWAPI) | `middleware/{circuitbreaker, correlation, deprecation, ratelimit, recover}`, `serviceidentity`, `externalapi/{admin, admintoken}`, and a new package for the pipeline and the registration helper from `sessionserver`'s plumbing files. |
| `pkg/gateway/drain` | `components/gateway/drain.md` (GWDRAIN) | `podlifecycle/{prestop, drainreadiness}`. |
| `pkg/gateway/scaling` | `components/gateway/scaling-and-capacity.md` (GWSCALE) | `metrics/{gcpause, capacityplanning}`. |
| `pkg/gateway/opsendpoints` | `components/gateway/ops-endpoints.md` (GWOPS) | `operability/health`. |
| `pkg/gateway/replica` | `components/gateway/index.md` (GW), the replica | `metrics/gatewaymetrics`, `coordination/{gatewayleader, pdbwatcher}`, `deployment/devmode`, `pki/tlsprobe`. |
| `pkg/gateway/playground` | `components/web-playground.md` (PLAY) | `mcpfabric/playground`, the move 0138 also names. |

The domain packages leave for concern directories (decisions 1 and 2): `pkg/session` (`sessionstore` with its implementations, `sessionlogstore`, `sessioncheckpointmeta`, and `interactionstore`), `pkg/tenancy` (the `environment` stores and `envaccess`), `pkg/runtime` (`runtimestore`, `poolstore`, `runtimecapoverride`, `capabilityinference`, and the `upgrade` packages), `pkg/storage` (the Postgres and Redis substrate helpers, the event bus, erasure, retention, legal hold, partition maintenance, and the eviction state store), `pkg/credential`, `pkg/audit`, `pkg/billing`, `pkg/quota`, `pkg/policy`, `pkg/checkpoint` (retention and the partial manifest store), `pkg/delegation` (the delegation policy store), `pkg/events` (the event buffer), `pkg/recommendations`, `pkg/mtls` (CA rotation), `pkg/workspaceplan` (the provisioning packages), and `pkg/common` (`semver`). The API conventions that the core and the seams share leave with them: `middleware/auth` and `callercheck` to `pkg/auth`, `middleware/idempotency` to `pkg/idempotency`, `middleware/environment` to `pkg/environment`, and `externalapi/{errorclassify, pagination, openapi}` to `pkg/api`; `cmd/openapi-to-mcp` reads the OpenAPI document. The full assignment, row by row, is in [evidence/packages.tsv](evidence/packages.tsv).

### SP-2 The seam interface

`pkg/gateway/seam` declares the operations below, and `spec/interfaces/gateway-seams.md` states each one. The list comes from the edges under "Current state", which are the calls the seams and the core make on each other today.

- **Operations the core serves to every seam:** look up a session and the replica that holds it; forward a request to the holding replica; and resolve the caller's authorization context for a session and an action (today `rbac_gate.go` and `middleware/auth`).
- **Operations the core serves to the platform tools seam:** create a child session, deliver a message to a session, and read or write files on a session's pod for export (today the `sessionserver`, `executor`, `messagerouting`, `sessioninbox`, and `podsession` imports of `mcptools` and `exportwire`).
- **Operations the core serves to the upload handler:** write staged content into a session's workspace (today `upload_to_session.go`'s use of `podsession`).
- **Operations the platform tools seam serves to the core:** the delegation tree's hooks at a session's terminal state (budget return, result rollup, orphan cleanup, archive, and recovery), the lease-control server on the forwarding listener, and the input-wait and tool-approval registries.
- **Operations the stream proxy serves to the core and the seams:** publish an event and open a subscription. The event bus implementation stays in `streamproxy`, and every publisher reaches it through the interface.
- **The route registration interface:** each part registers its routes with the API front through one interface that `pkg/gateway/api` implements, so the API front imports no part (decision 3).

The validation owed in `steps.md` confirms this list by splitting one seam on a throwaway branch before 0117.S1 lands.

### SP-3 The layout gate

A tier-0 test reads `go list -json ./...` and checks, for every non-test package:

1. A package of a seam imports, from `pkg/gateway`, only `pkg/gateway/seam` and its subpackages and packages of its own seam.
2. A package of the session core imports no seam package and no API front package.
3. A package of the API front imports no seam package and no session core package.
4. `pkg/gateway/seam` imports nothing else under `pkg/gateway`.
5. No package outside `pkg/gateway` and `cmd/lenny-gateway` imports a package under `pkg/gateway`. Test packages under `tests/` are exempt.
6. Every package under `pkg/gateway` sits in a directory of "SP-1", or in the register as a package a named change removes.

Replica-level parts other than the API front may import the core and the seam interface; drain calls the checkpoint barrier. The register `tests/registers/gateway-layout.yaml` lists each violation that exists when the gate lands, with the step that removes it, and the packages awaiting removal with the change that removes each. The gate fails on a violation not in the register and on a register row that no longer occurs, so the register only shrinks. The part of each directory comes from a table in the test, which this change keeps in step with "SP-1". Each test that verifies the gate carries `// spec: GW-R1`.

### SP-4 The moves

The move driver of proposal 0020 (`scripts/refactor`) already does what a move needs: `git mv` per directory, a boundary-anchored rewrite of import literals and runtime path references across `*.go`, a rewrite of `tests/spec-map.json` and `tests/change-graph.json`, `gofmt` and `goimports`, the build, vet, and cycle gates, and a rollback on failure. This change replaces its manifest with 0117's and extends it to every register keyed by package directory that still exists when a step runs: `tests/registers/change-graph-coverage.yaml`, `skip-reasons.yaml`, `proposal-label-comments.yaml`, `spec-section-citations.yaml`, and `tests/claim-map.json`. 0116 retires `spec-map.json`, `change-graph.json`, and `claim-map.json` in milestone M1, so in the program's order the driver no longer has to rewrite them; if a move runs first, the driver rewrites them as it did for proposal 0020. The tier-0 tests that hard-code gateway paths take the new paths in the step that moves the package they name. `TestMCPFabricSubsystemIsOneExtractableSubtree` and `TestCommittedManifestDestinationsExist` give way to the layout gate.

The split of `sessionserver` is not a path rewrite. Each split step moves its files to the new package, turns the `Server` fields the files read into a constructor argument typed by the seam interface, and wires the new package in `cmd/lenny-gateway`. The tests of the moved files move with them. A wiring test that pins one seam crossing is deleted where a tier-2 or tier-4 test covers the chain, as the test infrastructure review recommends for the v1 refactors ([review](../archive/2026-11-17-0154-merge-gate/evidence/test-infrastructure-review.md), "Question 2").

When a package moves, its `doc.go` gains the line naming its page codes under 0115's annotation rule, such as `// spec: GWCLAIM, CLAIM`, once the code registry of 0115 holds those codes.

### SP-5 Order

The steps run in order of import sites, fewest first, so the driver and the gate meet small moves before large ones: the metering proxy (51 sites), the seam model with the replica-level parts and the playground (108), the upload handler (a split with no outside importers), the stream proxy (69), the platform tools (428), the API front with the shared API conventions (497), the first group of domain packages (694), the second (905), the session core (1,020), and the session and tenancy stores (1,909). The session core follows every seam split, because what remains of `sessionserver` after the splits is the core.

Every move step follows 0155.S1, which renames the test tier directories and package clauses. The moves rewrite import lines in about 520 files under `tests/`, and a step that ran first would conflict with the rename script line by line. Every move step also follows 0137.S5. The packages most steps move hold files the kind renames edit (214 under `pkg/gateway`), and the steps whose own packages hold none, the metering proxy, the upload handler, and the stream proxy, rewrite import blocks in `cmd/lenny-gateway` files the renames also edit, such as `adminrouter.go` and `controlserver.go`. Both scripted rewrites touch the same import blocks, so whichever runs second has to be re-run rather than merged, and the program already runs 0137.S5 in milestone M1. 0117.S1 has no edge to either step. 0117.S2 follows 0155.S1 so that the gate is written at the new tier name, and has no edge to 0137.S5.

Member changes of the program whose code steps edit a package this change moves land after the step that moves it, as milestone M2 precedes the areas. Their "Current state" sections name today's paths; the move manifest translates them.

## Worked example

A runtime calls `lenny/delegate_task`. After the change:

1. The adapter forwards the call on `LNK-GWCONTROL`, and the platform tools seam's dispatcher (`pkg/gateway/platformtools/mcp`) hands it to the tool handler in `platformtools/mcptools`.
2. The handler checks the delegation policy and the lease through `pkg/delegation` and `pkg/policy`, which any part may import.
3. It creates the child through `seam.Sessions.CreateChild`, which the session core implements in `pkg/gateway/core/session`. Today the handler imports `sessionserver` and calls `ServiceCall`. The interface and method names here are proposals that 0117.S1 fixes.
4. The core claims a pod through `core/podclaim` and publishes the child's state change through `seam.Events.Publish`, which the stream proxy implements.
5. When the child ends, the core calls the delegation tree's terminal hook through `seam.DelegationTree.OnTerminal`, which the platform tools seam implements. Today `sessionserver` imports `treebudget`, `resultrollup`, `orphancleanup`, and `treearchive`.

Extracting the platform tools seam into its own service would put a client of `seam.Sessions` and a server of `seam.DelegationTree` on each side of a network transport, and change no package boundary.

## Limits, and alternatives rejected

- The layout gate checks imports. It does not check that a seam's goroutines run under its own limiter or that its metrics carry its name; those stay as §4.1 states them and as the seam pages will state them.
- The change does not extract any seam into a service and adds no transport.
- Packages that a member change removes stay in their current directories until that change lands, so the tree carries old directory names for those packages until then.
- **Rejected: split `sessionserver` in its own later change**, as the migration spike suggested. The split is what separates the seams from the core, and the layout gate has nothing to hold until it lands. Splitting the core further by component page is left to the area changes (decision 4).
- **Rejected: rename packages to their component names**, such as `podclaim` to `podclaimer`. The spike measured 425 of 593 changed lines as qualifier changes from the rename, against 63 import-path changes (decision 6).
- **Rejected: keep the gateway as one directory tree and enforce the seams by naming conventions alone.** Nothing would fail when a seam imports the core.

## Specification and non-specification changes

- **Specification.**
  - `spec/interfaces/gateway-seams.md` (GWSEAM): written in full, from placeholder to a page with the operations of "SP-2".
  - `spec/components/gateway/index.md` (GW): GW-R1 and the seam model's statement of the rule, with `status: partial`. The rest of the page stays a placeholder until its area migrates.
  - §4.0 of `spec/04_system-components.md`: the paths of `eventbuffer`, `operability/health`, and `operability/recommendations`, in the steps that move them. §4.1 "Gateway Internal Subsystems" gains a link to the seam interface page.
- **Code.** The packages of "SP-1", the new `seam`, `streamproxy`, `uploadhandler`, `platformtools`, and `api` packages from the `sessionserver` split, `cmd/lenny-gateway`'s import paths and the constructors the split needs, the importers in `pkg/ops`, `pkg/controller`, `pkg/tokenservice`, `pkg/podregistry`, `pkg/podlifecycle`, `pkg/ctlcli`, `pkg/mtls`, `pkg/storerouter`, and the other binaries listed under "Current state", and every test that imports a moved package.
- **Tooling and registers.** `scripts/refactor` with the new manifest and the register rewrites, `tests/registers/gateway-layout.yaml` (new), the registers keyed by package directory, the ten tier-0 tests that hard-code gateway paths, and the retirement of `scripts/refactor/manifest_layout_test.go`'s layout tests.
- **Rules.** `.claude/rules/code-best-practices.md`, "Project structure and reuse": a bullet stating where gateway code goes, the import rule, and the concern directories for domain packages.

## Documentation

No client, operator, or runtime-author behavior changes, so no page under `docs/` changes: no published page names a gateway package path today. For contributors:

- `pkg/gateway/ARCHITECTURE.md` becomes the map from each directory to its component page and part, in place of the groups of proposal 0020.
- `TESTING.md` takes the new paths in its three references to `sessionstore`, `memstore`, and `sessionserver`.
- `tests/unit/README.md`, `tests/conformance/README.md`, and `tests/testdata/mcp/README.md` take the new paths where they name gateway packages, unless 0155 has moved or rewritten them first.

## Tests

| Tier | Test |
| :-- | :-- |
| 0 static | The layout gate of "SP-3" (`// spec: GW-R1`): each of its rules with a fixture module that breaks it and one that passes, the register shrink check, and the gate over the tree. The driver's tests in `scripts/refactor` for the register rewrites, which carry no `// spec:` annotation because no specification section governs the tooling. `go list ./...` reports no cycle after each step. |
| 1 unit | The tests of every moved package and of its importers, unchanged except for import paths, run in each step. The `sessionserver` tests move with their files. |
| 2 to 4 | The component, contract, and integration suites that import a moved package, run in the step that moves it. No test changes behavior, so each suite passes with the same test count before and after, less the wiring tests deleted under "SP-4". |
