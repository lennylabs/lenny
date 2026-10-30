# Code review: structure, organization, modularization, and reuse

Date: 2026-06-03. Branch reviewed: `impl/v1-initial`.

This document records a structural review of the Lenny codebase. It covers project
structure, package organization, modularization, code reuse, naming, and test
organization across the Go module `github.com/lennylabs/lenny` (approximately
612,000 lines of Go in about 415 buildable packages and 24 binaries). It does not
assess functional correctness, security, or performance, except where an
organizational problem creates a latent correctness hazard, which is called out
explicitly.

## How this review was produced

The review came from a parallel multi-agent survey. Thirty-four agents examined the
tree at once: twenty-five covered individual subsystems and nine each applied one
cross-cutting lens (dependency layering, cross-subsystem duplication, naming,
abstraction, wiring, test organization, and documentation alignment). The agents
produced 215 raw findings, which were deduplicated into 30 themes. The 24
highest-impact or most-uncertain claims were then re-checked against the source by
independent verification agents that confirmed, corrected, or refuted each one. The
corrections from that pass are folded into the recommendations below and summarized
in [Appendix A](#appendix-a-what-verification-changed).

Counts in this document are point-in-time measurements and will drift as the code
changes. Where a number is given, it is the measured value on the review date.

## Overall assessment

Lenny is a large and functioning system, and several of its core conventions are
sound and applied consistently. Verification confirmed the following strengths:

- A uniform persistence pattern: a top-level `Store` interface, an in-memory
  `Memory` implementation, and a `pgstore` subpackage, with transaction and
  null-handling helpers shared through `pkg/gateway/pgtenant`.
- Separation of pure decision logic from controller-runtime adapters in
  `pkg/controller`.
- A structured-logging facility (`pkg/observability/logging`) wired into nine
  binary entrypoints, with a standard-library bridge that already emits JSON.
- A CI-validated specification map (`tests/spec-map.json`) covering 164 of 185 spec
  sections, plus a defined multi-tier test taxonomy.
- A working composition-root pattern in `cmd/lenny-ops/deps.go`.
- Shared primitives that already centralize concerns earlier assumed to be
  duplicated: an error-code classifier (`pkg/gateway/errorclassify`) and a pub/sub
  bus (`pkg/gateway/pubsub`).

The structural debt is concentrated in one pattern: growth by accretion without
periodic consolidation. It shows up as god-packages and god-files in the gateway, as
repeated copies of cross-cutting scaffolding, as duplicated wire and domain
contracts that have measurably diverged in the field, as four
production-orphaned-but-tested packages, and as the absence of any `internal/`
boundaries, which leaves all 415 packages on the public surface. None of this
prevents the system from building, testing, and shipping. The highest-value actions
are also among the cheapest, and they appear first below.

## Priority overview

Severity reflects impact on maintenance, onboarding, and change safety. Effort is a
rough implementation estimate. Confidence reflects how thoroughly the underlying
claim was verified.

| ID | Area | Severity | Effort | Confidence |
|:--|:--|:--|:--|:--|
| [R01](#r01) | Relocate the multi-megabyte governance ledgers | High | Small | High |
| [R02](#r02) | Resolve production-orphaned packages and dead symbols | High | Medium | High |
| [R03](#r03) | Shared error taxonomy: sentinels and a status mapper | High | Medium | High |
| [R06](#r06) | Introduce `internal/` boundaries | High | Medium | High |
| [R07](#r07) | Split the gateway god-files along existing seams | High | Medium | High |
| [R04](#r04) | Factor `pgstore` CRUD and error-mapping boilerplate | High | Large | High |
| [R05](#r05) | Decompose `sessionserver` and `admin` god-packages | High | Large | High |
| [R08](#r08) | Give the gateway a composition root | High | Large | High |
| [R12](#r12) | One owner per duplicated wire and protocol contract | High | Large | High |
| [R21](#r21) | Generalize store-conformance tests; consolidate doubles | High | Large | Medium |
| [R26](#r26) | Consolidate the admission `Decision` result type | Medium | Small | High |
| [R09](#r09) | Split the `gatewaymetrics` god-struct | Medium | Medium | High |
| [R10](#r10) | Shared cmd-support package for env/flag/mTLS wiring | Medium | Medium | High |
| [R11](#r11) | Consolidate audit-append and error-envelope scaffolding | Medium | Medium | High |
| [R17](#r17) | Move gateway `log.Printf` to context-aware `slog` | Medium | Medium | High |
| [R23](#r23) | Disambiguate colliding leaf package names | Medium | Medium | High |
| [R24](#r24) | Rename packages whose name does not match contents | Medium | Medium | High |
| [R14](#r14) | Extract shared periodic-job scaffolding | Medium | Medium | Medium |
| [R15](#r15) | Consolidate controller-runtime scaffolding | Medium | Medium | Medium |
| [R18](#r18) | Move misplaced packages; consolidate split layouts | Medium | Medium | Medium |
| [R19](#r19) | Split `podsession.Binder`; dedupe path-safety helpers | Medium | Medium | Medium |
| [R20](#r20) | Reduce per-provider scaffolds in `llmproxy` and `embedded` | Medium | Medium | Medium |
| [R22](#r22) | Wire the zero-importer `testinfra`; pick one test clock | Medium | Medium | Medium |
| [R25](#r25) | Settle the package-name taxonomy | Medium | Medium | Medium |
| [R27](#r27) | Consolidate cross-cutting domain helpers | Medium | Medium | Medium |
| [R30](#r30) | Strengthen human-readable spec-to-code alignment | Medium | Medium | Medium |
| [R13](#r13) | Adopt a package-creation rule; merge micro-packages | Medium | Large | High |
| [R16](#r16) | Unify the preflight check abstraction and engine | Medium | Large | Medium |
| [R28](#r28) | Strip spec markers from runtime strings | Low | Medium | Medium |
| [R29](#r29) | Settle persistence detail conventions | Low | Medium | Medium |

## Quick wins

These items combine small effort with real benefit and have no hard dependency on
the larger refactors.

1. **Relocate the governance ledgers (part of [R01](#r01)).** Move `BUILD-GAPS.md`,
   `TEST-GAPS.md`, `BUILD-PROGRESS.md`, and `BUILD-PLAN.md` into `docs/development/`,
   move `close-build-gaps.sh` into `scripts/`, and remove the untracked `tmp/`
   scratch tree. This is pure relocation with no code change and immediately reduces
   root clutter. The git-history rewrite is a separate, larger phase.
2. **Sweep dead symbols (part of [R02](#r02)).** Delete the `var _ =` and
   import keepalives in `pkg/audit/chain.go`, `pkg/gateway/mcp/mcp.go`, and
   `pkg/kms/azure/azure.go`, and collapse `checkpointer.triggerForSource` to its
   single return value.
3. **Consolidate the env helper (part of [R10](#r10)).** Extract one
   whitespace-trimming env helper into a new `pkg/cliconfig` and replace the six
   `envOr` copies. This corrects a per-binary behavior difference: the same
   environment variable with padding resolves differently in `lenny-gateway` than in
   `lenny-ops`.
4. **Extract `TenantLister` and `StaticTenants` (part of [R14](#r14)).** These are
   byte-identical across 11 and 5 packages respectively. A single shared declaration
   is a mechanical lift with no behavior change. This is explicitly not the
   propagator merge, which verification showed is already served by `pubsub.Bus`.
5. **Standalone package renames (part of [R23](#r23)).** Rename
   `pkg/gateway/adapter` to `adaptercaps` (9 importers) and the gateway middleware
   `circuitbreaker` to `breakermiddleware` (7 importers). Tool-assisted, no
   functional risk, and removes the forced `gwadapter` and `cbmw` aliases.
6. **Admission `FromDecision` helper ([R26](#r26)).** Add one shared result type and
   `FromDecision` helper to collapse the eight plain webhook adapters and seven
   identical `Decision` structs.
7. **Fix stale doc comments and surface the spec map (part of [R30](#r30)).**
   Correct `sessionevents` (`Package events`), the `sessionserver` "minimal gateway"
   header, and the `observability/audit` `opsevents` path reference, then add a
   one-line pointer to `tests/spec-map.json` from `README.md` and `CONTRIBUTING.md`.
8. **Add a regression guard for spec markers (part of [R28](#r28)).** Add a CI grep
   that rejects `§` markers inside `errors.New`, `fmt.Errorf`, and log string
   literals. The project doc-style rule already forbids this pattern.
9. **Split `gatewaymetrics.Metrics` ([R09](#r09)).** The split into per-domain
   structs touches only the composition root (about 16 non-test call sites), because
   consumers already depend on narrow interfaces. The payoff is high for a
   single-tree change, provided the shared Prometheus registry is threaded through
   rather than duplicated.

## Suggested remediation sequence

The recommendations have dependencies. A workable order is the following.

1. **Foundations and hygiene.** Relocate the ledgers ([R01](#r01)), resolve the
   orphaned packages and dead symbols ([R02](#r02)), and land the shared
   sentinel-to-status error mapper ([R03](#r03)). Begin introducing `internal/`
   boundaries in the gateway ([R06](#r06)). Reconcile the divergent wire and domain
   contracts ([R12](#r12)) before the copies drift further, starting with the
   mechanical JSON-RPC and CloudEvents extractions.
2. **Decomposition, with the new scaffolding in place.** Split the god-files
   ([R07](#r07)) and the `gatewaymetrics` struct ([R09](#r09)) as low-risk warm-ups,
   then extract a gateway composition root ([R08](#r08)) and decompose the
   `sessionserver` and `admin` god-packages ([R05](#r05)) so the extracted
   sub-packages land inside `internal/`.
3. **Shared reuse layers.** Build the `pgstore` base ([R04](#r04)) on top of the new
   error sentinels, consolidate the audit and error-envelope scaffolding
   ([R11](#r11)), add the cmd-support package ([R10](#r10)), and consolidate
   micro-package clusters ([R13](#r13)) and the periodic-job and controller
   scaffolding ([R14](#r14), [R15](#r15)).
4. **Naming, testing, consistency, and docs.** Apply the renames ([R23](#r23)–[R25](#r25), [R26](#r26)),
   the test-infrastructure consolidation ([R21](#r21), [R22](#r22)), the logging and
   convention work ([R17](#r17), [R29](#r29)), the preflight and provider-scaffold
   unification ([R16](#r16), [R20](#r20)), the runtime-string cleanup ([R28](#r28)),
   and the documentation alignment ([R30](#r30)), recording the structural decisions
   as ADRs once they are made.

---

## Repository structure and hygiene

### R01
**Relocate the multi-megabyte governance ledgers and reclaim git history.**

`BUILD-GAPS.md` (4,999,978 bytes, 45,109 lines) and `TEST-GAPS.md` (3,396,630 bytes,
12,103 lines) are machine-generated checklists that dominate the repository's text
weight. Together they exceed the entire `spec/` tree. `BUILD-GAPS.md` has been
rewritten across about 641 commits, each producing a fresh roughly 5 MB blob, and
`du -sh .git` reports 342 MB (323 MB of clone-relevant objects) against 612,000 lines
of source. Both files sit at the repository root among more than a dozen loose `.md`
files with no index, a one-off `close-build-gaps.sh` sits at the root while
`scripts/` exists, and an untracked 29-file `tmp/` tree holds alternate-product specs.

Move `BUILD-GAPS.md`, `TEST-GAPS.md`, `BUILD-PROGRESS.md`, and `BUILD-PLAN.md` into
`docs/development/` (or a `governance/` directory) and add a short root index that
classifies the remaining root `.md` files as durable or build-state. Prefer an
append-friendly machine-readable format (for example JSONL) over a 45,000-line
markdown file that re-snapshots on every edit. If markdown is retained, schedule a
coordinated history rewrite (`git filter-repo`) of the two largest files to reclaim
object-store size. Move `close-build-gaps.sh` into `scripts/` and remove or relocate
the untracked `tmp/` tree.

The file moves and the `scripts/` cleanup are independently safe. The history
rewrite is destructive and requires every clone to re-clone, so treat it as a
separate coordinated step.

**Severity** High · **Effort** Small · **Confidence** High · **Category** structure ·
**Sequencing** First; independent of all code changes.

### R02
**Resolve the four production-orphaned packages and remove dead-symbol keepalives.**

Four complete, tested packages have zero non-test production importers, so they pass
CI and read as live while misleading readers about which implementation is
authoritative: `pkg/adapter/embeddedcheckpoint` (about 488 lines), `pkg/degradation`
(about 115 lines, whose only non-test reference is a comment in `sdks/runtime/go`),
`pkg/gateway/routingcache` (about 194 lines), and `pkg/gateway/outputpartfidelity`
(about 475 lines, the documented fidelity matrix that the live translator
reimplements). Separately, `llmproxy.Handler` hardcodes `DialectAnthropic` at
`handler.go:311`, `:339`, and `:562`, so the registered `OpenAIDirectTranslator` is
reachable by provider string yet trips its own `DialectOpenAI` guard and returns an
error for every real request, and `OpenAIResponsesTranslator` is never constructed in
production. Smaller dead markers exist: `var _ = strings.TrimSpace` in
`audit/chain.go`, `var _ = errInternal` in `mcp.go`, an `azruntime` import keepalive
in `azure.go`, and `checkpointer.triggerForSource`, a switch whose every branch
returns the same value.

For each orphan, either wire it into the path it claims to serve or delete it
(recoverable from history). `outputpartfidelity` is exercised by
`tests/tier10_conformance/scaffolds_test.go` behind the `conformance` build tag, so
deleting it also removes that conformance subject; prefer routing the live
translator through it (resolving the `executor.OutputPart` versus `runtime.OutputPart`
split from [R12](#r12)) so the conformance matrix is the served code. The other three
are covered only by their own unit tests. For the LLM proxy, either derive the
dialect from the lease or provider through the hardcoded sites and the stream path,
or remove `OpenAIResponsesTranslator` and unregister `OpenAIDirectTranslator` at
`main.go:6488`. Remove the four dead-symbol keepalives and collapse
`triggerForSource` to its constant.

**Severity** High · **Effort** Medium · **Confidence** High · **Category** dead-code ·
**Sequencing** After R01. Do the three test-only orphans first; the
`outputpartfidelity` and dialect work couples to the `OutputPart` split in R12.

### R18
**Move misplaced packages and consolidate split source-of-truth layouts.**

Several packages and artifact trees sit where their name or role does not match.
`pkg/admission/ownership` is not an admission webhook (it imports only `fmt` and
`strings`, has zero importers under `pkg/admission`, seven under `pkg/controller`,
and several in the gateway); it is a server-side-apply field-ownership matrix, so its
placement creates an inbound edge into the admission subsystem. `pkg/backup` is an
orphaned top-level parent holding only a `retention` subpackage whose own
documentation places it under the `lenny-ops` backup feature. `pkg/objectstore`
reimplements the four cloud backends that `pkg/blobstore` already provides, with
little test coverage. `storerouter` re-aliases `platform/store` identity and enum
types as a backward-compatibility shim. Deployment artifacts are scattered across six
one-child top-level directories (`compose/`, `deploy/`, `charts/`, `packaging/`,
`build/`, and `dist/`), tooling binaries are interleaved with product binaries in
`cmd/`, and `schemas/*.proto` is split three directories away from `pkg/proto/`.

Move `pkg/admission/ownership` to `pkg/controller/ownership` to match its consumers.
Move `pkg/backup/retention` into `pkg/ops/backup/retention` and delete the empty
parent. Extract the generic per-provider byte-store plumbing into a shared package
that both `blobstore` and `objectstore` consume (or have `objectstore` delegate to
`blobstore`), and add backend tests. Migrate `storerouter` callers to
`platform/store` directly and delete the alias shim. Consolidate deployment artifacts
under one `deploy/` umbrella, split `cmd/` into product binaries and `cmd/tools/`, and
co-locate each `.proto` with its generated package.

Verification noted that the `ownership` placement is a deliberate grouping with the
other pure-decision admission packages and is documented as intended for future
webhook use, so that move is a grouping judgment rather than a defect fix. It is
mechanically safe but touches 12 import sites plus a cross-reference comment. The
`objectstore`/`blobstore` deduplication is the higher-value part; the deployment-
directory and proto co-location are cosmetic.

**Severity** Medium · **Effort** Medium · **Confidence** Medium · **Category** structure ·
**Sequencing** Independent moves; the `ownership` move pairs with R06.

## Modularization and coupling

### R06
**Introduce `internal/` boundaries so implementation packages are compiler-private.**

There are zero `internal/` directories under `pkg/` or `cmd/` (the single `internal/`
on disk is `scripts/internal`, which is shell). The module has 415 buildable
packages, and `pkg/gateway` alone has about 191 sub-packages, every one importable by
any consumer including the SDKs under `sdks/` and any downstream module. No
compiler-enforced line separates a subsystem's intended API from its implementation
detail, which permits cross-subsystem reach-through, makes any refactor within
`pkg/gateway/*` a potential break for external importers, and forgoes the cheapest
coupling control Go provides at this scale.

Introduce `internal/` boundaries subsystem by subsystem. A pragmatic first step is a
`pkg/gateway/internal/` for sub-packages that exist only to serve the gateway
(session-server helpers, middleware, lease control, and similar), plus a top-level
`internal/` for cross-cutting helpers not meant for SDK or third-party import.
Promote only the genuinely stable-API packages out of `internal/`. Do this alongside
[R05](#r05) so extracted sub-packages are private from creation. The move relocates
packages and rewrites import paths, and it will surface any current cross-subtree
imports the new boundary forbids.

**Severity** High · **Effort** Medium · **Confidence** High · **Category** layering-coupling ·
**Sequencing** Precede or run alongside R05. Prove the import-path migration on the
gateway before extending module-wide.

### R05
**Decompose the gateway god-packages `sessionserver` and `admin`.**

Two orchestrators concentrate most of their subsystems and amplify every change.
`pkg/gateway/sessionserver` has 88 imports and a `Server` struct of 106 fields
(`sessionserver.go:115-478`) built field-for-field from a 100-field `Options` struct
(`sessionserver.go:641-1301`; the `New` constructor references `opts.` exactly 100
times), spanning 135 files and about 14,500 non-test lines across create, start,
resume, upload, eval, memory, usage, metering, tree, tool-approval, and elicitation.
`pkg/gateway/admin` has 76 imports and a single `Router` struct with about 69
dependency fields, 53 `With*` builders, and 254 methods across 42 files, and it is
itself imported 84 times. Adding any session capability threads a new field through
`Options` and `Server` and a new method on the same receiver; `admin` forces every
resource handler into one struct.

Decompose both along the concern clusters the file layout already implies
(`sessionserver`: upload, eval/usage/quota, memory, tree, and lifecycle; `admin`:
tenants, pools, credential pools, audit, billing, and experiments). Give each cluster
a small struct holding only the dependencies it uses, mounted on a parent mux via a
`Routes(mux)` method, with per-group dependency structs replacing the mega-`Options`.
The 100 distinct `opts.*` assignments in `New` are the concrete seam any split
follows. Stage one cluster at a time behind the existing route tables, and introduce
the `internal/` boundaries from [R06](#r06) as the extraction proceeds so the
sub-packages are not re-exported.

**Severity** High · **Effort** Large · **Confidence** High · **Category** modularization ·
**Sequencing** Highest structural impact and largest effort. Start after R06 is under
way; R07 and R09 are lower-risk warm-ups in the same tree.

### R13
**Adopt a package-creation rule and consolidate micro-package clusters.**

Package-per-tiny-concern granularity inflates the import graph and is the structural
force behind the orchestrators' fan-out and the composition root's constructor count.
136 gateway sub-packages have exactly one non-test source file; repository-wide, 246
of 385 `pkg` packages (64 percent) have a single non-test file, many of 38 to 150
lines. Clear topical clusters span many sibling single-file packages: the credential
family (14 `cred*` and `credential*` packages at the flat top level), the pod family,
the delegation family (`pkg/delegation/{cycle,lease,recovery,tracing}` under a parent
with no `.go` files), and the `XxxStore`-interface-plus-single-`pgstore`-impl pattern
repeated 27 times where only `sessionstore` has a second backend. `derivelock` sits
among the credential packages by alphabetical adjacency despite being an unrelated
session-derivation lock.

Adopt an explicit rule: a new package is justified only when it defines a stable
interface consumed by multiple callers, isolates a backend technology, or breaks an
import cycle. Single-concern leaf packages that meet none of these become files in
their parent. Concretely: group the credential family under
`pkg/gateway/cred/{assign,cache,fallback,...}`, collapse the delegation subpackages
into files in `pkg/delegation`, and for the 27 single-backend stores collapse the
`pgstore` subpackage into the parent. Place `derivelock` with the session or
coordination concerns.

Verification corrected the repository-wide single-file figure to 246 of 385 (an
earlier 213 matched no reproducible method) and noted the gateway figure drops from
136 to 104 once the nested `pgstore` and `redisstore` store-adapter packages are
excluded. Those store-adapter packages follow the conventional Go backend-isolation
pattern and are a deliberate boundary, so state which set any consolidation targets
and treat them separately from genuinely thin single-purpose packages.

**Severity** Medium · **Effort** Large · **Confidence** High · **Category** modularization ·
**Sequencing** Couples to R06 and R05. Do the `cred/` and `delegation/` regrouping as
those subsystems are touched; the store-adapter collapse pairs with R04.

### R19
**Split `podsession.Binder` and dedupe the path-safety helpers.**

`podsession.Binder` fuses two execution modes (session mode and concurrent-slot mode)
into one 30-import type across two parallel files, with duplicated method pairs
(`Bind`/`BindSlot`, `assignCredentials`/`assignSlotCredentials`, `connect`/`connectSlot`,
`Release`/`ReleaseSlot`, and `drain`/`DrainSandbox`); the `StartSession` block and the
`resolveSandbox`-dial-`NegotiateVersion` sequence are copied verbatim and have drifted
in error wrapping. Three hand-rolled sandbox status-update conflict-retry loops bypass
the package's own `podclaim.ApplyGatewayPhase` helper with inconsistent retry bounds.
In the workspace slice, `resolvePath`, `pathWithin`, and `parseMode` (the
workspace-root containment defense and the setuid/setgid mode guard) are byte-identical
between `pkg/adapter/workspace` and `pkg/adapter/sharedassets`, which the code
comments admit, and `ArchivePolicy` re-declares `upload.RuntimeAllow` field for field.

Extract the shared claim-and-handshake and a single `assignCredentials` into private
helpers used by both modes, and split the public surface into `SessionBinder` and
`SlotBinder` embedding a common connector. Add one
`podclaim.updateSandboxStatusWithRetry` and route the three loops through it with a
standardized retry count. Extract `resolvePath`, `pathWithin`, and `parseMode` into one
shared `pathsafe` helper used by both `workspace` and `sharedassets`; because this is a
security-defense duplication, deduplicating reduces the chance of one copy drifting.
Replace `ArchivePolicy` with `upload.RuntimeAllow` or a type alias.

**Severity** Medium · **Effort** Medium · **Confidence** Medium · **Category** modularization ·
**Sequencing** Self-contained. The `pathsafe` dedup is a small, security-relevant win;
the `Binder` split is the larger piece.

## Large files and wiring

### R07
**Split the gateway god-files along their existing seams.**

Several files concentrate thousands of lines and many concerns into one unit, which
makes them merge hot-spots that resist review and per-concern testing.
`cmd/lenny-gateway/main.go` is more than 8,200 lines with a `func main()` spanning
about 6,000 lines; `gatewaymetrics.go` is 4,505 lines (one `Metrics` struct, 153
Prometheus fields, an approximately 1,835-line `New`, and 157 methods across about 40
metric domains); `mcptools.go` is 3,752 lines with an approximately 2,016-line
`Register` inlining 16 tool handlers; `sessionserver.go` (2,874 lines) and `start.go`
(2,283 lines) interleave HTTP handlers, pod and slot binding, credential resolution,
workspace-plan handling, and metrics; `admin/tenants.go` (1,835 lines) doubles as the
`Router` definition, the 130-route handler table, the package-wide `writeError` and
`writeJSON` helpers, and tenant CRUD. `pkg/alerting/rules/rules.go` (1,731 lines),
`pkg/loadctl/server.go` (1,057 lines), and `cmd/lenny-ctl/main.go` (1,834 lines) are
similar.

Split each file along the seams the code already implies, keeping the package
boundary unless a struct split is warranted (see [R05](#r05)). For `gatewaymetrics`,
move each subsystem's fields, registration, and methods into per-domain files and
replace the monolithic `New` with table-driven per-subsystem registrars. For
`mcptools`, extract each tool handler into a named function or file and reduce
`Register` to a dispatch table, as the package already does in `elicitation.go` and
`messaging.go`. For `tenants.go`, extract `router.go`, `routes.go`, `respond.go`, and
`gates.go`. For `rules.go` and `loadctl/server.go`, separate the data catalog from the
transport, orchestration, report, and simulator code. These are largely mechanical
splits within the existing package, with no public API change.

**Severity** High · **Effort** Medium · **Confidence** High · **Category** file-size ·
**Sequencing** Low-risk; can proceed in parallel. The `gatewaymetrics` and `mcptools`
splits unblock the deeper R05 and R09 work.

### R08
**Give the gateway a composition root and stop wiring everything in `func main()`.**

`cmd/lenny-gateway/main.go` is an 8,200-line file whose single `func main()` spans
about 6,000 lines, declares exactly 232 flags as loose pointers with no config
struct, and contains 52 inline `go` background-loop launches, 13 store-selection
branches, and about 35 inline adapter, observer, and auditor glue types defined after
`main()` that translate gateway-internal types into `pkg` audit and metrics sinks.
Those glue types are reusable composition logic that cannot be unit-tested in `cmd`,
and they force the binary to import 223 `pkg` packages and to know every subsystem's
internal event types. The codebase already demonstrates the better pattern in
`cmd/lenny-ops/deps.go`, a composition root of typed `*Config` and `*Deps` structs and
`build*` functions, although that file has itself grown to 1,782 lines mixing seven
subsystems.

Adopt the `lenny-ops` `deps.go` convention as the standard. Define a `GatewayConfig`
struct with nested sub-configs populated by one `parseConfig()` and validated by one
`Validate()`, and extract cohesive `build*(cfg, deps)` functions
(`buildStores`, `buildSessionServer`, `buildAdminRouter`, `buildLLMProxy`,
`buildLeaseControl`, `buildAuditPipeline`, and the background loops) into dedicated
files so `main()` shrinks to parse-config, call builders, run, and shut down. Move the
inline glue adapters into the packages whose interfaces they satisfy (or a
`cmd/lenny-gateway/adapters` package) with one `emitAudit` helper. Split `lenny-ops`
`deps.go` by subsystem as well. This is ordinary hand-written wiring with no generated
markers, so the functional risk is low.

Verification corrected the in-file constructor count to 179 (162 inside `func main()`),
not the approximately 269 originally cited, which counted `New*` across all files
including tests. The structural concern holds on the corrected figures.

**Severity** High · **Effort** Large · **Confidence** High · **Category** config-wiring ·
**Sequencing** Pairs with R05 (the builders construct the decomposed sub-packages) and
R10 (the env helpers feed `parseConfig`). Stage builder extraction one subsystem at a
time.

### R09
**Split the `gatewaymetrics` god-struct into per-domain metric structs.**

`gatewaymetrics.Metrics` is one struct with 153 Prometheus fields, an approximately
1,835-line `New()`, and 157 methods spanning about 40 unrelated metric domains, passed
wholesale while consumers separately define 19 or more narrow `XxxMetrics` interfaces
(45 counting `Recorder`, `Observer`, and `Sink` suffixes) to slice it. The concrete
`*gatewaymetrics.Metrics` is referenced directly almost exclusively in the
`cmd/lenny-gateway` wiring layer (16 of 36 non-test references; the rest are
documentation comments). Every consumer-package function signature already takes the
narrow local interface.

Split the god-struct into per-domain metric structs that each satisfy the existing
narrow interfaces. Because consumers already depend on those structural interfaces, the
split touches only the composition root. Replace the monolithic `New()` with
table-driven per-subsystem registrars (this is the `gatewaymetrics` half of
[R07](#r07)). Keep one shared `prometheus.Registry`: verification noted the single
private registry is shared across all metrics for scrape isolation, so a split must
thread one registry through rather than create several. Real coupling is only about 16
non-test signature-level uses, so the change is easier than the raw usage-site count
suggests.

**Severity** Medium · **Effort** Medium · **Confidence** High · **Category** abstraction ·
**Sequencing** Strong early win in the gateway tree; lower risk than R05. Do alongside
R07.

## Reuse and duplication

### R03
**Add a shared error taxonomy: canonical sentinels and a sentinel-to-status mapper.**

There is no shared sentinel-to-status layer. 35 packages each declare
`ErrNotFound = errors.New("<pkg>: ... not found")` and 15 declare `ErrAlreadyExists`.
About 44 non-test files independently translate a not-found sentinel into an HTTP or
gRPC status inline (about 144 `errors.Is(...ErrNotFound)` mapping lines), so a missed
mapping silently returns 500 for a genuinely absent resource. This per-package-sentinel
design is the root of the 42 duplicated `pgx.ErrNoRows` mappings in [R04](#r04). The
ops subsystem repeats its own `Error{Code,Message}` plus `CodeOf` trio across six
service packages and a per-handler code-to-status map across six `opsserver` files,
despite `pkg/ops/conventions` owning the envelope. Normative OAuth codes such as
`token_validation_unavailable` are bare string literals across eight files.

Add a sentinel-to-status mapper rather than collapsing the 35 package-local sentinels;
collapsing them creates import cycles, and per-package sentinels are idiomatic. The
defect is the duplicated translation at the transport boundary. Add a small shared
layer exporting canonical `ErrNotFound`, `ErrAlreadyExists`, `ErrConflict`, and
`ErrForbidden`, plus `Status(err)` returning the HTTP status, gRPC code, and string
code. Have stores wrap the shared sentinel with `%w` and have handlers call the one
mapper. Integrate with the existing `pkg/gateway/errorclassify` classifier (already
consumed by REST and MCP) so the new mapper produces the code `errorclassify` already
consumes. Preserve per-site messages and details. In `pkg/ops`, add
`conventions.CodedError` and `conventions.WriteCodedError` and delete the six local
`Error` types and per-handler writers. Add a `pkg/oautherr` for the normative OAuth
codes.

Verification confirmed the 35 and 15 sentinel counts exactly, corrected a "52 mapping
files" figure to about 44 non-test files and 144 mapping lines, and noted that
`errorclassify` already centralizes code-to-category mapping, so the missing layer is
specifically sentinel-to-status.

**Severity** High · **Effort** Medium · **Confidence** High · **Category** reuse-duplication ·
**Sequencing** Foundational for R04. Land the shared mapper first, then migrate stores
and handlers incrementally. The ops consolidation is an independent parallel track.

### R04
**Factor `pgstore` CRUD, error-mapping, and timestamp boilerplate into a shared base.**

The persistence layer follows one good convention (a top-level `Store` interface, an
in-memory `Memory`, and a `pgstore/` subpackage, with `pgtenant.InTx`, `NullTime`, and
`MonotonicNext` already shared) but copy-pastes the surrounding boilerplate. There are
39 packages named `pgstore`; 34 declare `func New(pool *pgxpool.Pool) *Store`; the
`23505`-to-`ErrAlreadyExists` mapping appears in about 16 files; the
`pgx.ErrNoRows`-to-not-found mapping appears in 42 files (85 `errors.Is` sites); plus
hand-written `rows.Next` scan loops, `json.Marshal` column helpers, the
now-or-`IsZero` create-timestamp guard, and identical GDPR erasure stubs. The two
Redis-backed stores and six `redisstore` packages repeat the same `Store{client}`,
`New(client)`, and key-prefix scaffold with `redis.Nil` mapping in 14 files.

Extend `pgtenant` (or add a `pkg/gateway/pgstorekit`) with an embeddable `Base{pool,
now}` carrying the constructor, generic read helpers `QueryRows[T]` and `QueryOne[T]`
wrapping query, close, next, error, and `ErrNoRows` translation, a `MapWriteError`
translating `23505` to the shared `ErrAlreadyExists` from [R03](#r03), an `IsNotFound`,
a `JSONColumn`/`ScanJSON` pair, and shared `DeleteByTenantID` and erasure helpers.
Migrate the 39 packages incrementally so each keeps only its store-specific SQL and
scanners. Add a parallel `redisutil` with a namespace key builder and an `IsMiss`
helper.

Verification confirmed the package and mapping counts and corrected the constructor
claim: only about 26 of the 34 `New` functions are the byte-identical one-liner and
about 28 reduce to the trivial `&Store{pool}` body; six stores (`baselinestore`,
`correctionstore`, `leasestore`, `evalstore`, `memorystore`, and the `NewWithOptions`
delegators) inject per-store fields and must keep bespoke constructors. The
error-classification helpers are safe to apply uniformly because those sites are
textually uniform; scope the constructor refactor carefully.

**Severity** High · **Effort** Large · **Confidence** High · **Category** reuse-duplication ·
**Sequencing** Depends on R03. Stage as: read/write/json helpers in `pgtenant`,
migrate trivial stores, then leave the six bespoke-constructor stores using the helpers
while keeping their own `New`.

### R10
**Create a shared cmd-support package for env, flag, mTLS, scheme, and pool wiring.**

Foundational wiring primitives are copy-pasted across binaries because no shared
cmd-support package exists, and the copies have drifted. `envOr` is defined in six
binaries, and its trim behavior splits three against three: `gateway`,
`pool-scaling-controller`, and `ctl` wrap `os.Getenv` in `strings.TrimSpace` while
`ops`, `webhook`, and `token-service` do not, so the same environment variable with
whitespace resolves differently per binary (the `ctl` copy's own comment documents trim
as intended). `envInt`, `envDuration`, `envBool`, `envFloat`, and `splitCSV` are
similarly duplicated. mTLS gRPC option builders are reimplemented per binary (the
cleanest `TLSServerOption`, `TLSClientOption`, and `WithServerName` are private to
`pkg/adapter/transport.go` while `gateway` and `token-service` hand-roll cert reload
and `tls.Config` inline). `buildScheme()` is defined three times across `controller`,
`webhook`, and `pool-scaling-controller`, and `webhook` omits `apiextensionsv1`.
Postgres pool construction is hand-rolled in six binaries while Redis is correctly
centralized in `pkg/redisconn`.

Create `pkg/cliconfig` exporting `String`, `Int`, `Int64`, `Duration`, `Bool`,
`Float`, `SplitCSV`, and `SplitAndTrim` with one agreed trim and truthy policy
(`TrimSpace` is the deliberate behavior per the `ctl` comment), replacing the local
copies. Promote the `transport.go` builders into `pkg/mtls` as canonical client and
server gRPC mTLS constructors and route every mTLS binary through them. Add a single
`lennyv1.NewScheme()` next to the API types for the three controller binaries
(including the omitted `apiextensionsv1`). Add a `pkg/pgconn` mirroring `pkg/redisconn`
for DSN parsing, pool sizing, TLS, and the schema-version probe. The `envOr`
consolidation is a safe bug fix: these back chart-rendered flag defaults where outer
whitespace is never meaningful, and the webhook comma-separated lists are unaffected
because `TrimSpace` strips only the outer ends.

**Severity** Medium · **Effort** Medium · **Confidence** High · **Category** reuse-duplication ·
**Sequencing** Feeds R08. The env-helper consolidation can land immediately.

### R11
**Consolidate duplicated audit-append, error-envelope, and HTTP-response scaffolding.**

Cross-cutting gateway plumbing is re-declared per package. The audit-append interface
`Append(ctx, tenantID, eventType, payload, at) (audit.Row, error)` is declared fresh in
about 10 non-test files, and the in-memory `ChainSet`-to-`Append` adapter is hand-rolled
at least three times (`policy`, `auditscope`, and `admin`) with byte-identical bodies;
three policy observers repeat the same construct-and-emit body. The error-envelope
writer is independently defined in four middleware packages and has drifted into two
incompatible JSON formats: `auth`, `environment`, and `circuitbreaker` emit
`{error:{code,message}}` with no category or retryable flag, while `idempotency` and
`correlation` run `errorclassify.Classify` and emit the spec-required category and
retryable fields, and `ratelimit` hardcodes them inline, so the same gateway returns
inconsistent error bodies depending on which middleware rejects. The `writeJSON` and
`writeError` pair is reimplemented in about 14 packages (`loadctl` defines it twice).

Declare the audit-append interface once next to `audit.Row` (`audit.Appender`) and
provide one `audit.NewChainSetAppender(chains, clock)` consumed by `policy`,
`auditscope`, `admin`, and `circuitbreaker`, plus a `policy` `emitAudit` helper for the
three observers. Add one exported envelope writer (`errorclassify.WriteError` or a
`pkg/gateway/httperror` package) that always runs `Classify` and emits the canonical
`{error:{code,category,message,retryable,details}}` body, and replace every
middleware-local writer with it. Provide one `httpjson` `WriteJSON`/`WriteError` for the
small HTTP servers. This routes through the same `errorclassify` classifier that
[R03](#r03) builds on.

The inconsistent error body is the correctness-relevant concern and should be addressed
with R03. The audit-appender and `httpjson` consolidations are independent cleanups.

**Severity** Medium · **Effort** Medium · **Confidence** High · **Category** reuse-duplication ·
**Sequencing** Couples to R03.

### R12
**Establish one owner per duplicated wire or protocol contract.**

Multiple wire and domain contracts that should have one owner are re-declared
structurally across packages, and the copies disagree. JSON-RPC 2.0 envelope types are
triplicated across `mcp`, `mcpruntimes`, and `connectorinvoke`. The CloudEvents v1.0.2
envelope is duplicated across `events` and `eventbus`, with `SpecVersion` disagreeing
(`1.0.2` versus `1.0`). The §6.2 pod-phase enum is defined under three naming
conventions (`State` in `sandbox/state`, `PodState` in `podlifecycle`, and
`SandboxPhase` in `sandboxclaim_guard`) with no cross-import, and the transition table
is implemented twice with divergent legal edges (`sandbox/state.ValidTransitions` has
58 edges, `podlifecycle.allowedTransition` has 37; they diverge in both directions, for
example `claimed -> running_setup` is legal in `podlifecycle` but absent from the
canonical table). Delegation budget axes are re-declared as eight or more struct types
across five packages, with `treebudget.TreeCounters` and `delegationbudget.TreeCounters`
byte-identical (the latter's own comment admits it mirrors the former).

Create `pkg/gateway/jsonrpc` owning the JSON-RPC types and error-code constants, and
have `mcpruntimes` and `connectorinvoke` import it. Extract the CloudEvents envelope,
prefix, and spec-version into one package that both `events` and `eventbus` depend on,
reconciling `SpecVersion`. Make `pkg/sandbox/state` the single §6.2 phase type and route
both transition tables through it. Adopting `ValidTransitions` is a behavioral
reconciliation rather than a mechanical dedup: the two tables encode different models,
so `podlifecycle.TransitionPodState` would start rejecting the shortcut edges it
currently accepts. Verification confirmed `TransitionPodState` has no production caller
today, so nothing breaks now, but any future caller must be audited; treat this as a
latent correctness hazard worth closing before a caller appears. For the budgets,
introduce one canonical type and collapse the genuinely identical pairs. Verification
also noted that the eight types do not all share the same field set (`TreeCounters` has three axes,
`LeaseSlice` carries six or seven including non-counter caps, and
`sessionstore.DelegationLease` carries JSON and DB tags needing a serialization shim),
so scope the merge to the exact-duplicate pairs and place the shared type in a neutral
package.

**Severity** High · **Effort** Large · **Confidence** High · **Category** reuse-duplication ·
**Sequencing** The JSON-RPC and CloudEvents extractions are independent and mechanical
and can land early. The §6.2 phase unification and the budget collapse touch behavior
and should be scheduled deliberately.

### R14
**Extract shared periodic-job scaffolding; keep the propagators separate.**

Two families of background plumbing are reimplemented per package. An identical
`TenantLister` interface (`ListTenants(ctx) ([]string, error)`) is declared in 11
distinct gateway packages, all byte-identical, and five of those also duplicate an
identical `StaticTenants []string` with its `ListTenants` method; the
`Run(ctx, onTick)` ticker, select, and shutdown loop recurs across multiple sweepers.
Separately, there are five packages named `propagator`.

The `TenantLister` and `StaticTenants` extraction is a clean low-risk lift: the 11
declarations are byte-identical, so a single shared `gatewaysweep.TenantLister` and
`StaticTenants` is mechanically extractable with no behavior change. The propagators are
a different case. Verification refuted the "extract one shared pub/sub loop" framing: the
Redis pub/sub mechanism is already factored into the single shared `pkg/gateway/pubsub`
`Bus`, and the five propagators are thin domain adapters over it whose substance diverges
intentionally (different payloads and surfaces, two add a Postgres LISTEN/NOTIFY fallback
with a dual-transport `Run`, `podterminate` adds origin-stamping self-skip, and all five
`New` signatures differ). Treat propagator consolidation as optional and lower priority.

**Severity** Medium · **Effort** Medium · **Confidence** Medium · **Category** reuse-duplication ·
**Sequencing** Do the `TenantLister`/`StaticTenants` extraction early. Defer the
propagator merge.

### R15
**Consolidate controller-runtime scaffolding: SSA retry, status apply, ticker runnables.**

`pkg/controller` separates pure decision logic from controller-runtime adapters well,
but the controller-runtime glue has no shared home. `func retryOnConflictSSA` is defined
identically in `warmpool/controller.go:47` and `sandbox/controller.go:46` (same
`maxAttempts=5`, 100 ms to 2 s backoff, and jitter), and the same retry concept is
wrapped a third way in `runtime/controller.go`. The status-condition merge-and-apply
boilerplate is hand-written three times under two retry strategies. The `manager.Options`
block (`MaxConcurrentReconciles` plus `QueueFactory`) is copied across four
`SetupWithManager` methods, and five or more bespoke ticker leader-election `Runnable`
loops reimplement the same immediate-first-tick and select-on-context control flow
(`poolscaling` and `tenantdeletion` are pure copy-paste). The `lenny.dev/pool` label is
duplicated as `LabelPool` and `poolLabelKey` with no compile-time link.

Add a shared `pkg/controller` helper: one `RetryOnConflict`, one
`ApplyStatusConditions(ctx, c, obj, fieldOwner, conds...)` doing read-merge-skip-apply
under a single retry strategy, one `Options(maxConcurrent, qf)` helper, and one
`TickerRunnable` taking a `tick func(ctx) error` plus interval, name, and
leader-election flag, implementing `Start` and `NeedLeaderElection` once. Define
`lenny.dev/pool` once in a shared low-level package imported by both `warmpool` and
`poolscaling`.

Confidence is medium: the `retryOnConflictSSA` duplication and the duplicated label are
concrete and grep-verifiable from the cited lines, but the breadth of the ticker-loop and
status-condition claims was not independently re-verified.

**Severity** Medium · **Effort** Medium · **Confidence** Medium · **Category** reuse-duplication ·
**Sequencing** Self-contained within `pkg/controller`; a good parallel track.

### R16
**Unify the preflight subsystem: one check abstraction, one engine, one prober adapter.**

The preflight subsystem carries the survey's sharpest internal duplication. Two
check-authoring conventions coexist (22 free functions `CheckXxx` returning `Decision`
versus 18 struct `XxxCheck.Decide`), and the `Decide` signature forks three ways
(`Decide()`, `Decide(ctx)`, and `Decide(ctx, reader)`). There are two divergent
orchestration engines: `pkg/preflight/infra` has its own `Run`, `Config`, and probers
with uniform pass/fail constructors, while the parent `pkg/preflight` hand-builds 46
pass-decision literals; the two never share probers, so any helper fix must be done
twice. A single-method `Prober` plus `XxxProbeFunc` adapter is hand-rolled 32 times with
varying method names that block a generic helper. `Run` is a 1,049-line orchestrator
built from a 46-site append wall over a 45-field `Config` bag.

Adopt one `Check` interface with one `Decide` signature and convert all checks to it,
replacing the special-casing in `Run` with a table-driven registry. Promote the `infra`
pass/fail constructors and uniform switch into `pkg/preflight` so there is one
orchestrator, or document the split explicitly and have both consume one prober set.
Introduce one generic func-adapter for the `Prober` single-method interface and delete
the 32 per-file types. Split `Config` and gather-error handling into separate files.

Confidence is medium: the two-engine split and the prober-adapter repetition are
concrete, but the specific counts were not independently re-verified.

**Severity** Medium · **Effort** Large · **Confidence** Medium · **Category** abstraction ·
**Sequencing** Self-contained in `pkg/preflight` and `cmd/lenny-preflight`; schedule
independently.

### R20
**Reduce per-provider and per-backend scaffold duplication in `llmproxy` and `embedded`.**

Several subsystems repeat near-identical per-variant scaffolds. The five `llmproxy`
provider translators repeat an approximately 80 percent identical
`TranslateRequest`/`TranslateResponse` skeleton (dialect check, API-key check, JSON parse,
presence checks, status mapping, and usage extraction); `openai_direct` and `azure`
differ only in URL and header. SSE streaming is reimplemented per package
(`llmproxy.RelayStream` versus `translator.writeOpenAIStream`). In `pkg/embedded`, the
reference-runtime catalog is hand-mirrored between `stack/catalog.go` and a block in
`charts/lenny/values.yaml` with no test binding them, and the copies have already
diverged: the chart entries carry `agentInterface`, `runtimeOptionsSchemaRef`, and
`labels` fields the Go `ReferenceRuntime` struct omits, plus a third partial copy of the
names in `cmd/lenny-ctl/install.go`. CLI flag parsing is hand-rolled in four
inconsistent styles, process-lifecycle teardown is duplicated between `k3s.Supervisor`
and `stack.managedProcess` with a drifted grace window, and the component-name list is
duplicated across logs, restart, status, and usage.

Extract a `validateProxyRequest` preamble and a per-provider descriptor (dialect, header
builder, URL builder, and usage extractor) so the OpenAI-family translators collapse to
one parameterized translator, and provide one shared SSE writer. For the runtime
catalog, a 1:1 generator is unsafe today because the chart intentionally carries fields
the Go seed omits; any unification must either extend the Go struct to the chart's full
field set or scope the shared source to the common subset. A lower-risk first step is a
drift-detection test that parses both sources and compares overlapping fields, folding in
`install.go`'s name list. Adopt the standard-library `flag` across `embedded/localcli`,
extract one managed-child-process type used by both `k3s` and `managedProcess`, and
derive the component lists from one canonical set.

**Severity** Medium · **Effort** Medium · **Confidence** Medium · **Category** reuse-duplication ·
**Sequencing** Two independent tracks. For the catalog, ship the drift-detection test
first; the `llmproxy` descriptor extraction pairs with R02 and R12.

### R27
**Consolidate cross-cutting domain helpers: SPIFFE parsing, KMS alias, version compare, KMS skeleton, state machine.**

Security- and domain-relevant helpers are reimplemented rather than shared. Two SPIFFE
URI parsers (`pkg/spiffeid.Parse` returning `ID` and `pkg/mtls/spiffe.Parse` returning
`Identity`) independently enforce the same base scheme, host, user-info, query, and
fragment rules, and `mtls/spiffe` does not import `spiffeid`. The canonical per-tenant
KEK alias `"tenant:"+id` is rebuilt inline at four production sites (two as
byte-identical private `kekAlias` functions) despite the documented `tenantkms.AliasFor`
helper. Version comparison is reimplemented four times (`backup`, `upgradeservice`,
`releasechannel`, and `preflight`) with divergent edge cases. The three cloud KMS
providers reimplement an identical alias-map, resolve, `SetAlias`, DEK-guard, and
`ErrUnknownKEK` skeleton. State-machine scaffolding (`Transition`, `validSet`, `IsValid`,
`InvalidTransitionError`, `Terminal`, and `All`) is copy-pasted across five `state`
packages with only the constant and edge lists differing.

Have `mtls/spiffe` layer its path-structure validation on top of a shared base-URI parser
rather than duplicating it. Verification corrected the framing: do not collapse
`Identity` into `ID`, because the two enforce different forbidden-component rules and
have already drifted (`spiffeid` rejects ports and `.`/`..` path segments and lowercases
the trust domain; `mtls/spiffe` does none of those and instead requires an exactly
three-segment agent or interceptor path that the §10.3 NET-060 and NET-063 handshake
verifiers depend on). The safe refactor extracts the shared base checks into a common
helper that `mtls/spiffe` builds on; reconciling the port and case differences changes
which peer certificates the gateway accepts and is a security-boundary change needing
review. Export one KEK-alias builder and call it from the four sites. Promote one
`CompareSemver` into a shared `pkg/version`. Extract the KMS skeleton into a base
parameterized by a per-provider encrypt and decrypt closure. Extract a generic
`statemachine` helper so each state package keeps only its constants and edges.

**Severity** Medium · **Effort** Medium · **Confidence** Medium · **Category** reuse-duplication ·
**Sequencing** The state-machine helper overlaps with R12's §6.2 phase work. The KMS
skeleton and version-compare consolidations are independent; the SPIFFE rule
reconciliation is a deliberate security review.

## Naming

### R23
**Disambiguate colliding leaf package names.**

Many distinct directories share the same leaf package identifier, which forces ad-hoc
aliases with no convention and makes navigation and grep ambiguous. Confirmed
collisions: two packages named `adapter` (`pkg/adapter` and `pkg/gateway/adapter`, the
latter a 38-line capabilities holder), two named `circuitbreaker`, 39 named `pgstore`,
plus `state` five times, `mcp` three times, `audit` three times, `ratelimit` three
times, and `runtime` three times. The aliasing is genuinely forced by Go: `main.go`
aliases the gateway adapter as `gwadapter`, and five files alias the middleware
circuit breaker as `cbmw`. 32 base names are declared two or more times module-wide.

Where the duplicate is a recurring parallel pattern (`state` five times, the
propagators), collapse it into one shared package (see [R12](#r12), [R14](#r14)) rather
than renaming copies. Where genuinely distinct, give the leaf a disambiguating role
name: rename `pkg/gateway/adapter` to `adaptercaps`, the gateway middleware
`circuitbreaker` to `breakermiddleware`, the underlying mechanism packages
`ratelimit` to `ratecounter` and `failopen` to `failopencontrol` so the middleware
keeps the feature name, and the three `mcp` packages to `adaptermcp`, `gatewaymcp`, and
`opsmcp`. For `pgstore`, either name each impl package after its domain or enforce one
canonical `<domain>pg` alias rule. The gateway-side renames have the smaller blast
radius (`gateway/adapter` has 9 importers versus 28 for `pkg/adapter`).

**Severity** Medium · **Effort** Medium · **Confidence** High · **Category** naming ·
**Sequencing** Mechanical renames with no functional risk. Sequence the `state`, `mcp`,
and propagator cases after the R12 and R14 consolidations remove the duplicates.

### R24
**Rename packages whose name does not match their contents.**

Several packages are named for one concern but implement another.
`pkg/gateway/leasecontrol` is named for §8.6 lease extension but implements the entire
`adapterv1.GatewayControlServer` (all six RPCs), the SA-token and mTLS interceptors,
the §8.6 elicitation and consent coordinator, an auto-extension rate limiter, and the
in-memory budget source (5,676 lines). `pkg/gateway/platformtools` and `connectortools`
depend back on interfaces defined in this lease-named package. Three unrelated `lease`
packages read as a family but are not, and both `leasestore` and `credleasestore`
export a bare type `Store`. Two packages named `translator` both own the OpenAI
concern. `drift` (§25.10 config drift) and `driftmonitor` (§13.3 NTP clock drift)
collide semantically. `pkg/ops/events` is named `events` but its `Service` is a stream
service.

Rename `leasecontrol` to `gatewaycontrol` (mirroring the client-side
`pkg/adapter/gatewaycontrol`) and move the `PlatformToolService` and
`ConnectorToolService` interface definitions there, or split the §8.6 extension logic
out so the platform and connector contracts no longer live in a lease-named package.
Rename `leasestore` to `coordinationlease` or `sessionlockstore` and prefer
concept-specific exported type names over bare `Store`. Disambiguate the translators by
layer and consolidate their OpenAI types (see [R12](#r12)). Rename `drift` to
`configdrift`, `driftmonitor` to `clockdrift`, and `pkg/ops/events` to `eventstream`.

The `leasecontrol` rename is multi-package: it is the canonical import name for
contracts consumed by `platformtools`, `connectortools`, and `cmd/lenny-gateway`, its
user-visible error strings are prefixed `leasecontrol:` and will drift unless updated,
and a split must preserve the shared sentinel errors and the single `*Service` receiver
that create real cohesion.

**Severity** Medium · **Effort** Medium · **Confidence** High · **Category** naming ·
**Sequencing** The `leasecontrol` rename is highest-value but most coupled; schedule it
alongside R12. The `drift` and `ops/events` renames are independent low-risk wins.

### R25
**Settle the package-name taxonomy: ops suffixes, the metrics convention, admission casing, and `api` versus `apis`.**

Several naming taxonomies are unsettled. `pkg/ops` uses three suffixes for related
concepts (`opsserver` for the HTTP surface, `opsservice` for the background body,
`operations` for the aggregator, and `opsinventory` for adapters), where `server` and
`service` are near-synonyms distinguished only in doc comments. The `*metrics` packages
use three conventions (subsystem-prefixed like `gatewaymetrics`, domain-prefixed like
`cloudmetrics`, and two bare packages both named `metrics` in `observability/` and
`ops/` that collide and force `obsmetrics` and `opsmetrics` aliases). 10 of 12
`pkg/admission` subpackages use snake_case directory and package names
(`cosign_verify`, `t4_node_isolation`, and similar). No other packages in the
repository use snake_case, the casing violates Go convention, and it forces aliases. `pkg/api` and
`pkg/apis` differ by one letter (the REST surface versus the Kubernetes CRD group), and
`pkg/ops/gateway` declares `package gateway`, shadowing the top-level gateway namespace.

Define one suffix per role and document it: `-server` or `-httpapi` for transport,
`-service` for the running body, and a descriptive noun for aggregators; rename
`opsserver` to `opshttp`. Adopt subsystem-qualified `*metrics` names everywhere so no
two collide. Rename the 10 snake_case admission packages to idiomatic single-word
lowercase (`dataresidency`, `nodeisolation`, `cosignverify`, and so on) and drop the
aliases. Keep `pkg/apis` (the controller-runtime convention) but rename `pkg/api` to
`pkg/wire/apitypes`, and rename the `pkg/ops/gateway` package to `gatewayclient`.

**Severity** Medium · **Effort** Medium · **Confidence** Medium · **Category** naming ·
**Sequencing** Mechanical renames. The snake_case admission renames pair with R26;
the ops-suffix and `api`/`apis` renames are independent.

### R26
**Consolidate the admission `Decision` result type and webhook adapter glue.**

The `Decision`-to-allow/deny mapping
(`if decision.Allowed { return Allow() } else { return Deny(int32(decision.Code), decision.Reason) }`)
is duplicated across all 10 webhook adapter files in `pkg/admission/webhook`. The
result struct is declared in 10 distinct guard packages; seven are byte-for-byte
`{Allowed, Reason, Code}` and the other three are supersets (`drain_readiness` adds
`Forced`, `pool_config_validator` adds `Warnings` and `BudgetExceeded`, and
`registry_digest` adds `OffendingImages`). `Allow()` and `Deny()` are already
centralized in `webhook.go`, so the residual duplication is the result-type
declaration plus the small if/return glue.

Introduce a shared result type plus one `FromDecision(Decision) *AdmissionResponse`
helper, collapsing the eight plain adapters and the seven identical structs. The three
guards with extra fields cannot fold into a bare `{Allowed, Reason, Code}` without a
base-plus-extension split, and `FromDecision` must stay pure, so the
`drain_readiness` audit side-effect and the `pool_config` warning propagation remain in
their own adapters. The helper covers the common case but does not eliminate all 10 type
declarations or fully replace those two adapters. The per-guard request input types are
legitimately distinct and out of scope.

**Severity** Medium · **Effort** Small · **Confidence** High · **Category** abstraction ·
**Sequencing** Small, self-contained quick win. Pairs with R25 and R28.

## Test organization

### R21
**Generalize the store-conformance harness and consolidate inline test doubles.**

Two compounding test-duplication problems. First, 21 dual-backend stores each ship a
full mutex-and-map `Memory` fake, and the `Memory` and `pgstore` halves of one concept
are tested in two unrelated trees (`Memory` in-package; Postgres in
`tests/tier2_component/stores`, 41 files), with only `memorystore/memorystoretest` as a
reusable contract harness, so the two implementations can silently diverge on NULL and
zero handling, not-found-versus-empty, and ordering. Second, test doubles are reinvented
inline: 237 distinct fake and stub type names (202 `fake*`, 35 `stub*`, and 0 `mock*`)
across co-located test files, recurring by base name (`fakeClock` nine times,
`fakeMetrics` eight, `fakeSource` seven, and `fakeStore` six).

Generalize the `memorystoretest` pattern into a per-store `<store>test` package
exporting `RunContract(t, factory func() Store)` that encodes the interface semantics
once, invoked from both the in-package `Memory` test and the tier-2 `pgstore` test,
collapsing the two divergent suites. For the fakes, provide a generic `inmem.Map[K,V]`
that `Memory` types embed, and hand-write or generate one canonical no-op metrics fake
and one recording-store fake in a shared `testsupport` package. Verification corrected
two premises: scope the fake consolidation to the about 39 recurring cross-file names
rather than all 237 (most of the 237 are package-local one-offs), and the claim that no
shared test-double package exists was refuted, because shared doubles already live under
`tests/testinfra/` (`stubs/kms`, `mocks/otelcollector`, `clockstep`, and `fakekube`).
Route new shared doubles there.

**Severity** High · **Effort** Large · **Confidence** Medium · **Category** testing ·
**Sequencing** The `RunContract` harness pairs with R04; the fake consolidation pairs
with R22.

### R22
**Wire the zero-importer `testinfra` packages and pick one canonical test clock.**

The test taxonomy and `testinfra` tree are disciplined, but reuse fails at the helper
layer. A cluster of shared `testinfra` packages built for exactly the reinvented
behavior have zero importers: `assertions` (including a row-level-security
`TenantIsolation` contract check, while 127 tenant-isolation tests hand-roll the
two-tenant write-and-read pattern), `fixtures`, `golden`, `goleak`, `matrix`, `ports`,
and `randctl`. Overlapping deterministic-clock facilities (`clockstep` with three
importers, `timectl` with two) coexist with no documented division, which is why nine
packages hand-rolled their own `fakeClock`. A few monolithic test files mirror the god
production files (`mcptools_test.go` 2,840 lines, `create_test.go` 1,337,
`gatewaymetrics_test.go` 1,904).

For each zero-importer `testinfra` package, either wire it in or delete it with its
self-tests. Route the 127 tenant-isolation tests through `assertions.TenantIsolation`
so the contract is expressed once, adopt `tests/testinfra/golden` in the inline golden
tests, and decide on `goleak` and `randctl` explicitly. Pick `clockstep` as the
canonical test clock (it is the richest, with a steppable `Now()`/`Advance()` and
timer-waking), document it in `TESTING.md`, replace the nine `fakeClock` copies, and
consolidate `timectl` into it. Verification corrected the original framing: do not
standardize on `pkg/clockinject`, which is a chaos-offset passthrough wrapper correctly
injected from the gateway composition root into about 41 call sites and is not a
settable per-test clock. Split `mcptools_test.go` and `create_test.go` along the seams
their test names expose (the test half of [R07](#r07)).

**Severity** Medium · **Effort** Medium · **Confidence** Medium · **Category** testing ·
**Sequencing** Pairs with R21. The clock decision precedes replacing the nine
`fakeClock` copies; the test-file splits pair with R07.

## Consistency and conventions

### R17
**Migrate gateway `log.Printf` to context-aware `slog` with structured attributes.**

A purpose-built structured-logging package (`pkg/observability/logging`, an `slog` JSON
handler plus a standard-library bridge and a correlation `Wrap`, wired via
`logging.Setup` in nine binary entrypoints) is bypassed by the dominant call style.
`log.Printf` appears at about 69 real non-test sites across `pkg/`, of which about 48
across 22 files are in `pkg/gateway`. Direct message-emitting `slog` calls in non-test
`pkg` number five. The standard-library bridge does route every `log.Printf` line
through the JSON handler, so output is structured JSON on the wire, but it is flat:
fixed `Info` level with no per-call severity, and no correlation fields, because
`log.Printf` carries no context and the handler's `correlation.From(ctx)` sees only the
background context. A third idiom exists (controller-runtime `logr.FromContext` in eight
files).

Pick `slog` as the single in-process logging interface and migrate the gateway hot-path
`log.Printf` sites to a `*slog.Logger` with structured attributes
(`slog.String("session_id", id)`, `slog.Any("err", err)`) rather than relying on the
bridge for first-party code. Standardize the correlation field set (`session_id`,
`tenant_id`, `request_id`, and `trace_id`) through `logging.Wrap`. Either document the
controller's `logr` usage as a deliberate boundary or bridge `logr` onto the same `slog`
handler. Verification reframed this: the payoff is recovering severity levels and
correlation fields rather than fixing unstructured logging, since the bridge already
emits structured JSON. The handler, correlation plumbing, and tests already exist, so
risk is low, and severity is medium rather than high.

**Severity** Medium · **Effort** Medium · **Confidence** High · **Category** consistency ·
**Sequencing** Independent once the call sites are catalogued; pairs with R05 and R08
where they touch the same files.

### R28
**Strip spec-section markers from runtime strings and externalize inline schema literals.**

Two spec-numbering couplings leak into runtime artifacts. Spec section markers (`§N.N`)
are embedded in about 80 `errors.New` and `fmt.Errorf` strings and about 92 log and
error sites (for example `redisconn` `ErrAuthRequired` carries `...(§12.4)...`), which
the project doc-style rule flags as ageing poorly: when a section is renumbered the
strings become wrong, and external consumers may match on text that silently changes.
Separately, `mcptools` hand-writes 18 `InputSchema` values as raw-JSON Go string
literals (the `delegate_task` literal is 2,080 characters) inside the 2,000-line
`Register` function, unvalidated at compile time, drifting from the arg structs that
decode them, and unusable by the REST surface. Admission reject-code constants also
diverge (three packages use `RejectCode`, others inline the code in message strings).

Strip `§` references from the message portion of `errors.New` and `fmt.Errorf` and from
log strings, keeping the spec citation in an adjacent comment; where a requirement ID is
useful for support, prefer a stable non-positional code. Add a grep guard for `§` inside
those literals to prevent regrowth. Move the `mcptools` `InputSchema` strings into a
schema block or generate them from the arg structs so the schema and decoder share one
definition and can be referenced by REST validation. Standardize one exported
reject-code constant per admission decision package.

**Severity** Low · **Effort** Medium · **Confidence** Medium · **Category** consistency ·
**Sequencing** Low priority. The grep guard is a cheap regrowth guard; the `mcptools`
schema externalization pairs with R07.

### R29
**Settle persistence detail conventions: in-memory naming, clock injection, layout, and constructor style.**

Within the otherwise uniform store convention, sibling packages drift on details that
raise navigation and test cost. In-memory naming has three forms for the same role
(`Memory`/`NewMemory`, `MemoryStore`/`NewMemoryStore`, and `NewInMemory`). Clock
injection is split: some `pgstore` packages inject a `now func() time.Time` or a clock
field, while `experimentstore`, `poolstore`, `runtimestore`, `credentialpoolstore`, and
`billingstore` hardcode `time.Now()` inline with no seam, and the field name itself
splits between `now` and `clock`. Store layout is inconsistent (`sessionstore` splits
`memstore/` and `pgstore/` subpackages while `transcriptstore` and `interactionstore`
keep `NewMemory` in the root plus `pgstore/`, and `sessionlogstore` and `admintoken`
have no `pgstore`). Constructor style is split three ways module-wide (functional
options about 13 times, config struct about 22, and bare `New(pool)` about 49) with no
documented rule. File naming mixes snake_case and joined-word within one package
(`sessionserver` has both `upload_abort.go` and `uploadmetrics.go`).

Standardize on the majority `Memory`/`NewMemory` naming and rename the outliers. Add a
uniform `now func() time.Time` seam, defaulted in `New` and named identically, to every
`pgstore`, retrofitting the five that hardcode `time.Now`. Pick one session-store layout
and apply it to `transcriptstore`, `interactionstore`, and `sessionlogstore`, and
document `admintoken`'s Kubernetes-secret backend. State one constructor convention in a
`CONTRIBUTING` note (bare deps for simple stores, a config struct for multi-field, and
options for many-optional) and standardize the clock parameter name. Pick one
file-naming rule and apply it package by package, starting with the large flat gateway
packages.

**Severity** Low · **Effort** Medium · **Confidence** Medium · **Category** consistency ·
**Sequencing** Lowest priority; do opportunistically as each store is touched for R04 or
R21. The uniform clock seam pairs with R22.

## Documentation alignment

### R30
**Strengthen human-readable spec-to-code alignment: architecture mapping, ADRs, `doc.go`, and stale references.**

Machine-readable traceability is strong (the CI-validated `tests/spec-map.json` maps 164
of 185 spec sections to packages and tests, and 381 of 951 `pkg` files cite a spec
section), but human-readable structural alignment for newcomers is weak. The contributor
architecture overview (`docs/getting-started/architecture.md`, 536 lines) cites neither a
spec section nor a package path, so it floats free of both the spec and the code.
`spec-map.json` is never advertised in `README.md` or `CONTRIBUTING.md`, and there is no
reverse spec-section-to-package index. The ADR program is mandated (the catalog reserves
ADR-0001 through ADR-0015), but only ADR-0007, ADR-0008, and the meta ADR-0000 are
written, and the largest structural decisions the code embodies have no ADR. Only five
of about 416 packages carry a `doc.go`. Several references are stale: `sessionevents`
says `Package events`, the `sessionserver` header still describes a no-auth,
no-Postgres "minimal gateway", and `observability/audit` references a renamed
`opsevents` path.

Add a component-to-package-to-spec table to `docs/getting-started/architecture.md` and
link each component heading to its spec section; surface `tests/spec-map.json` from
`README.md` and `CONTRIBUTING.md` as the authoritative cross-reference and optionally
render a spec-section-to-package view. Backfill the highest-value structural ADRs
(gateway tiering, `storerouter`, and delegation decomposition) or downgrade the
catalog's reserved-number claim, and add a CI check flagging new gateway sub-packages or
new `*store` packages without a referenced ADR. Adopt a `doc.go`-per-package convention
enforced above a file-count threshold, backfilling `sessionserver`, `admin`,
`mcptools`, `llmproxy`, and `delegation` first. Fix the stale references.

**Severity** Medium · **Effort** Medium · **Confidence** Medium · **Category** docs ·
**Sequencing** Independent of code changes; pairs with the structural work it documents.
Fix the stale references and surface the spec map now; backfill the ADRs after the R05,
R06, and R13 decisions so they record what was done.

---

## Appendix A: what verification changed

The adversarial verification pass re-checked 24 of the highest-impact claims against the
source. It confirmed most, corrected several figures, and refuted two. The corrections
are folded into the recommendations above and collected here for transparency.

- **No committed binary.** An initial scan suggested `lenny-preflight` (a 69 MB binary)
  was tracked. Verification refuted this: the file is correctly gitignored, is absent
  from HEAD, and a prior commit (`9997756a`) explicitly removed it. The oversized
  tracked files are limited to `BUILD-GAPS.md`, `TEST-GAPS.md`, and a deliberate tar test fixture
  (`tests/testdata/uploads/archives/bomb-count.tar`). [R01](#r01) addresses the
  documents and carries no committed-binary claim.
- **Gateway constructor count.** The `cmd/lenny-gateway` wiring concern was originally
  stated as about 269 constructors; the honest figure is 179 in the file and 162 inside
  `func main()`. The about-269 figure counted `New*` across all package files including
  tests. [R08](#r08) uses the corrected numbers.
- **`sessionserver` field counts.** Corrected upward to 106 (`Server`) and 100
  (`Options`); the earlier 105 and 99 were each one short. The decomposition in
  [R05](#r05) stands on the corrected counts.
- **Single-file package figure.** The repository-wide count is 246 of 385 packages, not
  the 213 originally cited. The gateway figure is 136, or 104 once the conventional
  nested store-adapter packages are excluded. [R13](#r13) states which set a
  consolidation targets.
- **Logging is already structured.** The gateway `log.Printf` usage routes through a JSON
  handler, so output is structured but flat. The deficiency is missing severity levels
  and correlation fields, so [R17](#r17) is framed accordingly and downgraded to medium.
- **`pgstore` constructors.** About 26 of 34 `New` functions are byte-identical rather
  than all 34; six stores legitimately inject per-store fields. [R04](#r04) scopes the
  constructor refactor around this.
- **Propagators already share a primitive.** The five propagators are thin adapters over
  the shared `pubsub.Bus`, so the clean extraction is only the byte-identical
  `TenantLister` and `StaticTenants`. [R14](#r14) separates the two.
- **Shared test doubles already exist.** `tests/testinfra/` holds shared doubles, and
  `clockstep` is the canonical steppable test clock. [R21](#r21) and [R22](#r22) route
  new doubles there rather than inventing a parallel home, and scope the fake
  consolidation to the recurring names.
- **Latent transition-table hazard.** The two §6.2 pod-phase transition tables diverge in
  both directions, but `podlifecycle.TransitionPodState` has no production caller today,
  so it is a latent hazard rather than an active defect. [R12](#r12) closes it before a
  caller appears.

## Appendix B: review method and coverage

- **Survey breadth.** 34 agents ran in parallel: 25 covered individual subsystems and 9
  applied one cross-cutting lens each. All 34 returned results.
- **Finding volume.** 215 raw findings were deduplicated into 30 themes.
- **Verification.** 24 high-impact claims were re-checked by independent agents. Most
  were confirmed; the corrections appear in Appendix A.
- **Tooling.** `go list` for the dependency graph, plus `git ls-files`, `wc`, and `grep`
  for metrics. `staticcheck` and `golangci-lint` are not installed and were not used.
- **Scope boundary.** This review covers structure, organization, modularization, reuse,
  naming, and test organization. It does not assess functional correctness, security, or
  performance, except for the latent hazards noted in [R02](#r02) and [R12](#r12).
- **Counts are point-in-time.** Measurements were taken on 2026-06-03 and will drift as
  the code changes. For example, `cmd/lenny-gateway/main.go` measured between 8,229 and
  8,259 lines across the run as the file was edited.
