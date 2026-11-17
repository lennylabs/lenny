# Migration spike: specification half

An agent migrated the specification area that the gateway pod claimer (`pkg/gateway/podlifecycle/podclaim`) implements into the new structure at the target state, from the current specification and the designs. It worked on the throwaway branch `spike/spec-code-migration` (commits `8bf053629` and `f6a1f7787`), wrote nothing under `spec/`, and took about 24 minutes and 474,000 agent tokens. The pages are in [spec-next/](spec-next/). `python3 spec-next/tools/specref.py check` reports 439 links and no errors.

## Pages written

| Path | Code | Words | Requirements |
| :-- | :-- | --: | --: |
| `capabilities/runtimes-and-pools/pod-claims.md` | CLAIM | 5,692 | 17 |
| `capabilities/runtimes-and-pools/warm-pools.md` | POOL | 5,031 | 26 |
| `capabilities/runtimes-and-pools/agent-pod-lifecycle.md` | APOD | 4,293 | 18 |
| `capabilities/sessions/slot-assignment-and-failure.md` | SLOTOPS | 1,941 | 7 |
| `capabilities/sessions/session-isolation.md` (partial) | SLOT | 1,100 | 4 |
| `components/gateway/pod-claimer.md` | GWCLAIM | 1,539 | 4 |
| `interfaces/kubernetes/podclaim.md` | KPCLAIM | 936 | operations `kubernetes#podclaim.*` |
| `interfaces/kubernetes/agentpod.md` | KAPOD | 985 | none |
| `interfaces/kubernetes/podset.md` | KPSET | 1,488 | none |
| `interfaces/kubernetes/labels-and-annotations.md` (partial) | KLABEL | 1,092 | 13 key definitions |
| `conventions.md` | CONV | 850 | the code registry |

The pages carry 77 requirements, 78 definition blocks, and 43 `unresolved` comments. 24 placeholder pages with front matter only (`status: placeholder`) let every link resolve. The other outputs are `MIGRATION-LEDGER.md`, `legacy-stubs.md`, and `tools/specref.py` (292 lines, standard library only).

## Ledger

264 paragraphs of §4.6, §4.6.1, §4.6.3, §5.2, §6.2, and §17.2, plus 9 rows for singleton citations:

| Fate | Paragraphs | Words |
| :-- | --: | --: |
| Migrated | 58 | 5,001 |
| Changed by a design | 54 | 12,791 |
| Moved outside the specification | 20 | 1,005 |
| Removed by a design | 24 | 2,980 |
| Duplicate | 4 | 638 |
| Not migrated in this spike (another area owns it) | 104 | 15,134 |

34 paragraphs needed judgment. Examples:

- §5.2 "Deployer acknowledgment (runtime process kept across sessions)", 584 words. SI D4 removes it, but one rule inside it, that a used pod is admitted only on a pool that reuses pods, survives as CLAIM-R5 because the tenant-derived runtimes design depends on it.
- §5.2 "Slot assignment atomicity", 770 words. It became SLOTOPS-R1 to R5, CLAIM-R12, CLAIM-R16, and a register on GWCLAIM. SI-4 changes the capacity comparison to `AgentPod.spec.slotCount` and adds `dead:<n>` members.
- §4.6.1 "ADR-007" fencing verification. The designs keep it in the specification, and TAXONOMY sends tests to `TESTING.md`. The tests moved out and the fencing argument became CLAIM-R9's rationale.

## Contradictions

| Contradiction | Resolution |
| :-- | :-- |
| RV-3 has the WarmPoolController delete a `PodSet`; PLC makes the PoolScalingController its only writer. | PLC (POOL-R7). |
| REC-1 and PLC keep the fencing tests in the specification; TAXONOMY moves them out. | TAXONOMY. |
| SI D4 removes the runtime-process acknowledgment; DR-5 and DR-9 need its used-pod rule. | The rule stays as CLAIM-R5. |
| The tenant-label webhook has two names (§5.2 and §4.6.3 against §17.2). | `lenny-label-immutability`. |
| §16.3 has the controller emit `session.claim_pod`; the claim runs in the gateway. | The claimer's span (GWCLAIM-R3). |
| The host-node precondition sat on the `sdk_connecting` edge, which PL-8 removes. | Moved to the recycle disposition (APOD-R7). |
| SI-10 and PL-9 word the shared-pod list differently. | SI-10's list (SLOT-R1). |
| §5.2 names a `scrub_warning` annotation without a key. | The code's key, `lenny.dev/scrub-warning`. |
| §5.2 retires a one-slot pod after any failed session; SI-3 keeps the slot live after some failures. | Unresolved; owner question 1. |
| "Runtime not live" reads a report about a process that SI D4 and D15 remove. | Unresolved; owner question 2. |
| The `retireOnRelease` triggers differ between designs, and no design names the claim field. | Unresolved; owner question 3. |
| Whether the leaked state survives SI-3, and dead and leaked members are not rebuilt after a Redis restart. | Unresolved; owner question 4. |
| Whether `scrubProfile` values keep their meaning after SI-3. | Unresolved; owner question 5. |

The code's tests also cite §3.1, §3.2, §3.4, and §6.41, none of which exist; they were mapped by their citation text.

## Gaps in TAXONOMY version 2

1. No anchor rule for definition blocks. The spike used `<kind>-<slug>`, such as `setting-gateway-claimholdttlseconds`, and `<resource>-<verb>` for operations.
2. Term anchors could collide across pages. The spike made them unique across the specification, and the tool refuses duplicates.
3. An incremental migration links into pages not yet written. The spike added `status: placeholder` and `status: partial`.
4. A setting several capabilities use. `maxConcurrentSessionsPerPod` lives on SLOT, with `read-by` naming the others.
5. A register written by several parts of one binary. The Redis slot set went to GWCLAIM.
6. Admission refusals with a message and no error code. The spike proposed `PODCLAIM_CONCURRENT` and `PODCLAIM_QUARANTINED` on CLAIM.
7. A component's own RBAC grants within the gateway's. Listed on GWCLAIM and marked unresolved.
8. Reviewer annotations. `src`, `judgment`, and `unresolved` comment conventions were added to `conventions.md`.

## Reference tool

`check` caught three planted errors: a heading anchor on another page, an unknown identifier, and an identifier on the wrong page. Moving `slot-assignment-and-failure.md` with `git mv` produced 31 errors; `fix` rewrote 69 links in 6 files, and `check` returned to zero. The move was reverted. The tool keeps links that still resolve through a detour, loses a `../spec-next/` prefix on the links it rewrites, and relies on a committed registry snapshot to know where pages were before a move.

## Effort

Reconciling the designs with each other and with the specification took most of the effort: three passes over about 600,000 words of designs, 34 judgment rows, and 43 unresolved items. Reusing the slice pages, which already applied the pod lifecycle design, cut four pages to an upgrade. The ledger was cheap once a script split the paragraphs.

The area is 11 pages and about 25,000 written words. Scaled to the map's 278 pages, the agent estimates about 25 areas of this size, each one long agent session plus owner review of 30 to 40 judgment rows, and areas dense with design changes (sessions, credentials, and egress) cost more.
