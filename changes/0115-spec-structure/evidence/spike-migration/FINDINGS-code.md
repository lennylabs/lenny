# Migration spike: code half

An agent migrated the gateway pod claimer, `pkg/gateway/podlifecycle/podclaim`, against the pages in [spec-next/](spec-next/), under revision 1 of the draft annotation rule. It worked on the throwaway branch `spike/spec-code-migration` (commits `e428e3e29` to `4e291f48a`) and took about 52 minutes and 463,000 agent tokens. The package ended at `pkg/gateway/core/podclaimer`. The per-annotation log is [annotation-fates.tsv](annotation-fates.tsv), and the issues it filed are under [issues/](issues/).

## Numbers

| | Before (`podlifecycle/podclaim`) | After (`core/podclaimer`) |
| :-- | :-- | :-- |
| Lines of code | 1,936 in 5 files | 1,871 in 7 files |
| Lines of tests | 4,537 in 18 files | 4,549 in 19 files |
| Test functions | 91, 84 annotated | 98, 86 annotated |
| Test names with a section suffix | 73 | 0 |
| `spec:` lines in code and tests | 43 and 85 | 31 and 86 |
| Comment lines carrying a section number, in code and tests | 175 and 92 | 0 and 1 |
| Annotations in function bodies or on fields | 42 | 0 |

The final code cites 33 identifiers, and each resolves. The 12 unannotated tests verify behavior no requirement states, or code with no target page.

## Annotation fates

194 comment groups cited the specification: 89 in code and 105 in tests.

| Fate | Code | Tests | Total |
| :-- | --: | --: | --: |
| Mechanical: the mapping gave it | 16 | 41 | 57 |
| Semantic: placed by reading the code and the pages | 13 | 28 | 41 |
| Dropped by the rule: caller, helper, field, or inline | 51 | 19 | 70 |
| Stale: cited something the code does not do | 3 | 0 | 3 |
| No target | 2 | 13 | 15 |
| Removed with the code | 4 | 4 | 8 |

Examples of the semantic cases:

- `claimIdle` and `acquireFreshClaim` each carried an in-body comment on the same lost-race check. The rule's few-sites signal led to a `claimLost` helper, cited CLAIM-R1 and CLAIM-R11.
- The mapping said to delete `kept_runtime_rule_test.go` with the runtime-process acknowledgment. Its tests verify the used-pod refusal, which CLAIM-R5 keeps, so they moved to `used_pod_rule_test.go`.
- `applyStatus` carried only prose about the patch-only grant. It is where GWCLAIM-R4 is decided, and placing the citation showed that the patch omits the `resourceVersion` the rule requires (GWCLAIM-I1).

## Removals

- **Runtime-process acknowledgment (SI D4).** 13 files, +140 and −442 lines, with one-line edits at two call sites outside the package. The deletion was mechanical. Outside the package, 160 Go files hold 854 references to the runtime-process names.
- **`rewarmStartedAt` (PL-8).** Not removed. 23 Go files outside the package read it, and removing the writer would leave preConnect pools with no path to `reserved`. Issue CLAIM-I2.
- **The uptime stamp.** Not removed. Removing it stops every uptime drain until the WarmPoolController reads the cap from the `PodSet`. Issue APOD-I1.

## Claiming from the revision's pod sets (CLAIM-R4)

The claimer gained a `PodSetLister`, the pod hash on its requests, and 9 tests, 7 of which fail when the filter is off. No caller changed, because nothing upstream has a pod hash, and no `PodSet` kind or pod-set label exists yet, so the gateway wires no lister and the claimer still lists by pool (CLAIM-I4). The pages left three choices open, and the agent chose: a failed `PodSet` list is a retryable failure, a request without a pod hash fails closed, and the claimer lists by pool until a lister is wired.

## Kind-rename blast radius

| Type | Go code: files and references | Go tests: files and references | Other files with identifiers |
| :-- | :-- | :-- | :-- |
| `Sandbox` | 107 and 698 | 163 and 987 | 74 |
| `SandboxClaim` | 78 and 308 | 137 and 675 | 63 |
| `SandboxTemplate` | 68 and 359 | 102 and 515 | 56 |
| `SandboxWarmPool` | 69 and 297 | 98 and 452 | 40 |

The renames touch 463 Go files and 609 files overall, including 17 under `spec/`, 26 under `docs/`, and 30 under `charts/`. Four packages are named after the kinds, 322 files import them, and the code holds 653 lower-case identifiers such as `sandboxName`.

## Moving the package

The move changed 102 files with 593 lines changed each way: the package's 26 files, 56 importers, 8 files that name the package only in comments, and 12 other files. The non-Go files were a tier-0 register keyed by path, a tier-0 test with 8 hard-coded paths, 50 paths in `tests/spec-map.json`, and the issue files. Most changed lines came from renaming the package (425 qualifier changes) rather than from import paths (63). A regular-expression rewrite also turned six `kubernetes#podclaim.*` operation identifiers into `kubernetes#podclaimer.*`, which the reference tool caught.

`pkg/gateway/core` already holds `semver` and `subsystem` (the seam breaker and limiter). The agent suggests reserving `core` for the session core, and moving those two to `pkg/gateway/seams` and `pkg/common/semver`.

## Gateway layout sketch

| Current groups | Target |
| :-- | :-- |
| `core/podclaimer`; `podlifecycle/{podsession,bindrelease,slotreaper}`; `coordination/{coordination,coordlease,coordfence,barrier}`; `checkpoint/*`; most of `session/*`; `storage/{leasestore,slotcounter,derivelock,dualstore}`; `runtime/{adapterclient,slothealth,watchdog}` | Session core |
| `session/sessionevents`, with stream code now inside `sessionserver` | Stream proxy seam |
| Upload code inside `sessionserver` | Upload handler seam |
| `mcpfabric/{delegation*,delegationtree/*,delegationbudget,delegationpolicystore,elicitationfloor,mcptools}`; `gatewaycontrol/*`; `session/{inputwait,toolapproval,interactionstore,memorystore}` | Platform tools seam |
| `llmproxy/{llmproxy,credrouter}` | Metering proxy seam |
| `core/subsystem`, `metrics/extractionthreshold` | The shared seam model |
| `podlifecycle/{drainreadiness,prestop}`; `coordination/{gatewayleader,pdbwatcher}`; `metrics/{gatewaymetrics,gcpause,capacityplanning}`; `operability/*`; `eventbuffer`; `deployment/devmode` | The replica |
| `sessionserver`; `externalapi/*`; `middleware/*`; `callercheck`; `serviceidentity`; `mcpfabric/{mcpruntimes,mcpschemagen,playground}` | API surface: TAXONOMY has no home for it |
| `environment/*`, `credentials/*`, `quota/*`, `billing/*`, `audit/*`, `policy/*`, `experiment/*`, `pki/*`, `provisioning/*`, the `runtime/*` registries, and the rest of `storage/*` | Domain stores and infrastructure: TAXONOMY has no home for them |
| `connectors/*`, `upgrade/*`, `runtime/sdkwarm`, `llmproxy/{proxycache,semanticcache}`, `mcpfabric/mcp` | Removed by designs |
| `runtime/{statelessproxy,statelessrouting,tenantaffinity}` | Unclear |

`sessionserver` (109 files, 29,882 lines) imports 20 of the 30 gateway groups and holds API handlers, upload, streaming, and claim orchestration together. `mcpfabric/delegationtree/leasecontrol` sits in the platform tools seam but hosts the scrub-report server the core imports.

## Issues filed

| Issue | Kind and severity | Summary |
| :-- | :-- | :-- |
| GWCLAIM-I1 | build gap, high | Status writes carry no `resourceVersion`, which breaks GWCLAIM-R4 and the CLAIM-R9 fencing. |
| CLAIM-I1 | spec defect, low | References to the fencing and hold rules point one number low. |
| CLAIM-I2 | build gap, medium | The `rewarmStartedAt` removal crosses three packages. |
| CLAIM-I3 | question | The idle scan deletes a claim in a case CLAIM-R2 does not list. |
| CLAIM-I4 | build gap, high | Nothing produces the claim set: no `PodSet` kind, pod-set label, or revision selection. |
| CLAIM-I5 | build gap, medium | The Postgres fallback selects by pool and ignores the claim set. |
| APOD-I1 | build gap, medium | The uptime cap travels on an annotation. |
| SLOTOPS-I1 | build gap, medium | The slot bound comes from the pool's current setting instead of `AgentPod.spec.slotCount`. |

## Tests

`GOOS=linux CGO_ENABLED=0 go build ./...` passes. On darwin the build fails in `tests/testinfra/supervisedpod` as it did before the spike. `go test -count=1` passes for the moved package and its importers (`podsession`, `session/recycle`, `sessionserver`, `pkg/podlifecycle`, `pkg/podregistry`, and `cmd/lenny-gateway`), and the tier-2 component tests `slotrelease`, `postclaimrelease`, and `translators` pass. Tiers 4, 7a, 8, and 9 were compiled and vetted but not run. Tier 0 shows the same 8 failures as before the spike and no new ones.

## Assessment

For this package, re-citing and changing the code in place cost less than rewriting it from the pages would have:

- 57 citations mapped directly, 70 disappeared under the rule, and 41 needed reading.
- Reading found three defects (GWCLAIM-I1, SLOTOPS-I1, and CLAIM-I3) rather than wrong behavior throughout.
- The tests pin edge cases the pages state in a sentence or not at all: the rebind race, aborted preconditions, rollbacks on a detached context, duplicate and leaked releases, and error classification.
- The blockers sit outside the package, and a rewrite would meet them too.

Most friction in the move came from registers keyed by path or section and from the package rename. The agent recommends:

1. Do the kind renames once, tree-wide, with Go-aware tooling.
2. Retire `spec-map.json` and the path-keyed tier-0 inventories in favor of coverage derived from annotations.
3. Per area, remove what has no outside dependents, re-cite, implement the deltas, and file issues for removals that cross packages.
4. Move packages with Go-aware tooling.
5. Split `sessionserver` along the seams as its own change.
