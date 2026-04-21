# Perspective 6 — Developer Experience (Runtime Authors) (iter7)

**Scope.** Re-review of the runtime-author developer surfaces. iter6 was **deferred** for this perspective due to sub-agent rate-limit exhaustion (see `iter6/p6_dev_experience.md`), so iter5 is the active baseline. This iter7 pass:

1. Re-verifies all four iter5 Low-severity carry-forwards (DXP-022 — DXP-025) against the current spec.
2. Surveys the same surfaces iter5 covered plus the §17.4 Embedded-Mode walkthrough in full (lines 263–303) and the §15.7 multi-language SDK claim for new drift introduced while P6 was deferred.
3. Applies the `feedback_severity_calibration_iter5.md` rubric — "would improve DX" alone is Low; only a broken wire contract, missing required command for the happy path, or misleading-enough-to-cause-task-abandonment issue rates Medium or above.

Surfaces reviewed this iter:

- `spec/15_external-api-surface.md` §15.4.3 (integration levels), §15.4.4 (sample echo runtime), §15.4.6 (conformance), §15.7 (Runtime Author SDKs).
- `spec/24_lenny-ctl-command-reference.md` §24.3 (runtime management), §24.9 (token print), §24.18 (scaffolder), §24.19 (Embedded-Mode image management).
- `spec/26_reference-runtime-catalog.md` §26.1 (overview), §26.2 (shared patterns), §26.12 (author onramp).
- `spec/17_deployment-topology.md` §17.4 (Embedded-Mode custom-runtime walkthrough).
- `spec/05_runtime-registry-and-pool-model.md` §5.1 (`integrationLevel`).
- `spec/04_system-components.md` §4.7 (lifecycle channel wire messages).

ID convention: iter5 used `DXP-NNN`; this pass follows the task-specified `DEV-NNN` convention and cross-lists the iter5 IDs for traceability. Numbering continues from the iter5 tail (`DXP-025`) so the first new iter7 finding is `DEV-026`.

---

## Inheritance of prior findings (iter5 DXP-022 — DXP-025)

iter6 deferred P6 — **no iter6 fixes were applied to any DevEx finding**. All four iter5 Lows are therefore unchanged in the spec.

| iter5 finding | iter7 disposition | Evidence |
| --- | --- | --- |
| DXP-022 §26.12 `runtime-templates` repository role still undefined; author onramp ambiguous about where to put code [Low] | **Not fixed.** §26.12 (line 485) still reads "New reference runtimes are proposed via a PR to `github.com/lennylabs/runtime-templates`" with no further definition. The repo name does not appear anywhere else in the spec. §24.18 (line 226) and §15.4.6 (line 2395) still describe the scaffolder as emitting files from an in-binary template with "no API calls" — so the `runtime-templates` role (template source repo vs. proposal meta-repo vs. typo for `runtime-<name>`) remains ambiguous. Carry forward as **DEV-022**. |
| DXP-023 `deadline_signal` vs `deadline_approaching` tri-name split [Low] | **Not fixed.** §4.7 line 694 still advertises capability string `"deadline_signal"`; §4.7 line 703 still defines wire message type `deadline_approaching`; §15.4.3 pseudocode (lines 2251, 2288) still declares `supported = [..., "deadline_signal"]` and then switches on `"deadline_approaching"`; §15.4.6 test row (line 2393) is still labeled "deadline signal handling" with an assertion that reads "On `deadline_signal`…" — the bareword conflicts with the actual wire message name. Three distinct names for one concept persist. Carry forward as **DEV-023**. |
| DXP-024 `§26.1` "`local` profile installations" terminology undefined [Low] | **Not fixed.** §26.1 line 30 still reads "For `local` profile installations, `lenny up` auto-grants access to the `default` tenant…". `local` is still not a §17.4 Operating Mode (Embedded, Source, Compose), not a §17.6 Install Profile (`base`, `prod`, `compliance`), and not a §17.8.1 bundled Helm profile. No match anywhere. Carry forward as **DEV-024**. |
| DXP-025 §17.4 Embedded-Mode walkthrough still omits the non-`default`-tenant access grant step [Low] | **Not fixed.** §17.4 walkthrough (lines 263–303) still has no step 3b. An author deploying from a non-default tenant still hits `RUNTIME_NOT_AUTHORIZED` at first session creation with no in-walkthrough pointer to `POST /v1/admin/runtimes/{name}/tenant-access` or the §24.3 tenant-access subcommands. Carry forward as **DEV-025**. |

**Net iter5 carry-forward.** All four iter5 Lows still open (all Low — `feedback_severity_calibration_iter5.md` rubric anchored).

---

## New findings (iter7)

### DEV-026. §17.4 walkthrough uses `lenny-ctl runtime register --file` — a subcommand not documented in §24.3 or §24.18 [Low]

**Section:** `spec/17_deployment-topology.md` §17.4 (lines 289, 303); `spec/24_lenny-ctl-command-reference.md` §24.3 (lines 49–56), §24.18 (line 232).

The primary-path Embedded-Mode walkthrough instructs authors to run:

```
lenny-ctl runtime register --file runtime.yaml
```

(§17.4 line 289) and again refers to `lenny-ctl runtime register` as "the same admin API used in production clusters" at line 303. But:

- **§24.3 Runtime Management** is the canonical command table for runtime admin surfaces. It documents only three subcommands: `lenny-ctl admin runtimes grant-access`, `list-access`, and `revoke-access`. No `register` variant (singular or plural, with or without `admin`) is defined.
- **§24.18** references a similar-but-different form **`lenny-ctl admin runtimes register`** (plural, with `admin`) as part of describing what `lenny runtime publish` wraps (line 232). That form is the one an author would infer by analogy with §24.3's three defined subcommands.
- Neither form (`lenny-ctl runtime register` from §17.4, or `lenny-ctl admin runtimes register` from §24.18) has a dedicated row in §24 with flags, exit codes, API mapping, or min-role; both appear only in prose.

An author who runs the exact §17.4 command will either hit "unknown command" (if only `admin runtimes register` is implemented) or will succeed with an undocumented path shape that diverges from the §24 reference. Either way the §24 reference is incomplete for the primary-path walkthrough. This is DX friction, not a blocker — a motivated author will try both forms or read the source — so Low.

**Recommendation:** Pick one canonical command path, then fix both sites:

- Add a row to **§24.3** for it: `lenny-ctl admin runtimes register --file <runtime.yaml>` | "Register a new runtime definition via a YAML body (equivalent to `POST /v1/admin/runtimes` with the parsed body)" | `POST /v1/admin/runtimes` | `platform-admin`.
- Update **§17.4 line 289 & line 303** to use the exact form in §24.3 (recommended: `lenny-ctl admin runtimes register --file runtime.yaml` to match the §24.3/§24.18 naming pattern).
- Update **§24.18 line 232** to reference the same form.

No REST-API change is needed (the endpoint already exists in §15.1); this is purely spec-internal consistency.

---

### DEV-027. §17.4 walkthrough `runtime.yaml` uses Kubernetes CRD-style header (`apiVersion`/`kind`/`metadata`) — inconsistent with §5.1 flat Runtime schema and will produce a non-loadable document [Low]

**Section:** `spec/17_deployment-topology.md` §17.4 (lines 279–288); `spec/05_runtime-registry-and-pool-model.md` §5.1 (lines 36–60); `spec/15_external-api-surface.md` §15.1 `POST /v1/admin/runtimes` body; `spec/04_system-components.md` §4.6 (CRD inventory); `spec/15_external-api-surface.md` §15.5 (CRD inventory).

The walkthrough's `runtime.yaml` example is:

```yaml
apiVersion: lenny.dev/v1
kind: Runtime
metadata:
  name: my-agent
spec:
  type: agent
  image: my-agent:dev
  integrationLevel: basic  # see §5.1 integrationLevel field: basic | standard | full
```

But **Runtime is not a Kubernetes CRD** — the CRD inventory in §4.6 and §15.5 lists only `SandboxTemplate`, `SandboxWarmPool`, `Sandbox`, and `SandboxClaim`. Runtime is a Postgres-backed platform object managed via REST (`POST /v1/admin/runtimes` per §15.1), not `kubectl apply`. The §5.1 Runtime schema is a **flat** YAML object:

```yaml
# Spec §5.1 line 59 shape (standalone runtime example):
name: my-agent
type: agent
image: my-agent:dev
integrationLevel: full
# ... (no apiVersion/kind/metadata wrapper)
```

An author who takes the §17.4 YAML and (a) tries `kubectl apply -f runtime.yaml` will get "no matches for kind \"Runtime\" in version \"lenny.dev/v1\"" because no such CRD exists; (b) tries `POST /v1/admin/runtimes` with this body will at best get a schema-validation error (the admin API expects a flat body, not a wrapped one); or (c) succeeds only because `lenny-ctl runtime register --file` happens to strip the wrapper before POSTing. In none of those cases is the shown YAML the same shape as the §5.1 Runtime schema that §17.4 line 287 cross-references.

A first-time author copy-pasting the walkthrough gets a working example only if the CLI quietly strips the CRD envelope; if they then try to script against `POST /v1/admin/runtimes` directly (Ops automation, CI) with the same YAML, the body will reject. This is DX friction at the boundary where authors graduate from the walkthrough to automation — Low.

**Recommendation:** Rewrite the §17.4 `runtime.yaml` to match §5.1 exactly:

```yaml
name: my-agent
type: agent
image: my-agent:dev
integrationLevel: basic  # see §5.1 integrationLevel field: basic | standard | full
```

(Drop `apiVersion`, `kind`, `metadata:`; promote `name:` to top-level; drop `spec:` indent.) This makes the walkthrough YAML directly reusable against both `lenny-ctl admin runtimes register --file` and `curl -X POST /v1/admin/runtimes --data-binary @runtime.yaml` (after YAML→JSON conversion). Cross-reference §5.1 by anchor so authors can see the full schema.

Alternatively: if a CRD envelope is intentional (because `lenny-ctl register --file` is planned to accept both flat and wrapped forms for K8s familiarity), document the envelope-wrapper behavior explicitly in §5.1 as an "alternative accepted form" — but this is broader scope than a DX-polish fix.

---

### DEV-028. §15.7 Python/TypeScript SDK type shapes reduced to one sentence while Go gets ~150 lines — multi-language DX parity gap [Low]

**Section:** `spec/15_external-api-surface.md` §15.7 (lines 2523–2675).

The iter5 fix for DXP-013 defined the Go `Handler` interface, `CreateRequest`, `Message`, and `Reply` structs with per-field doc-comments pointing to the §4.7 adapter manifest, §15.4.1 message envelope, §14 workspace plan, and §4.9 credential bundle — ~150 lines of authoritative Go type definitions at lines 2523–2672. But the Python and TypeScript SDK equivalents are collapsed to a single sentence at line 2675:

> "Python and TypeScript SDKs expose an equivalent `Handler` protocol/interface and an equivalent `run()` entrypoint. `CreateRequest`, `Message`, and `Reply` are surfaced as idiomatic language types (e.g., `@dataclass` in Python, `interface` in TypeScript) with the same field set and the same JSON tag mapping above."

Given that the majority of reference runtimes in §26 are Python (`langgraph`, `mastra`, `openai-assistants`, `crewai`) or TypeScript (none currently, but `chat` is a plausible future TS rewrite), the single-sentence treatment creates two concrete DX problems:

1. **Field-name ambiguity.** "Idiomatic language types" leaves open whether Python uses `snake_case` (idiomatic Python) or `camelCase` (matches the JSON tag). `@dataclass` with explicit `field(metadata=...)` mapping, or Pydantic `Field(alias=...)`, or plain `__init__` with manual JSON conversion — each is a different contract. Go uses `json:"..."` tags which is explicit; Python has three common approaches and the spec names none.
2. **Optional / nullable semantics.** The Go `Reply.Final` field uses `json:"final,omitempty"` with a specific rule at lines 2666–2671 ("Final MUST be true for Basic-level runtimes"). Python's `Optional[bool] = None` / `Optional[bool] = False` / `bool = False` each render differently in JSON — the spec does not specify which the SDK produces. A Python runtime author who picks the wrong one will either emit `{"final": null}` (rejected) or fail to flush a terminal frame.

This is analogous to DXP-013 for non-Go runtimes. iter5 fixed DXP-013 only for Go; Python and TypeScript authors still have to read the Go definitions and guess the language mapping. Low severity because (a) the JSON wire shape is fully defined in §15.4.1 so a diligent author can reconstruct the types, and (b) the Go types are authoritative and do at least anchor the behavior — but parity across the three officially-supported SDKs is a legitimate DX gap.

**Recommendation:** Add two short subsections to §15.7 after the Go struct block (after line 2673, before line 2675):

**Python SDK** — one `@dataclass`-based code block showing `CreateRequest`, `Message`, `Reply` using `field(metadata={"json": "..."})` or `pydantic.BaseModel` with `Field(alias=...)`, explicitly stating the chosen convention. Include the `Final MUST be true for Basic-level runtimes` rule.

**TypeScript SDK** — one TypeScript `interface` block for `CreateRequest`, `Message`, `Reply` with the same JSON field names as Go (no `camelCase` translation — JSON wire field names are authoritative), and the same `Final` rule.

Keep the Go block as the authoritative form (since §4.7 manifest and §15.4.1 envelope are already camelCase JSON), and make the Python/TypeScript blocks the "same JSON shape, language-idiomatic declaration style" presentation. ~30–40 lines total. No wire contract changes.

---

## Convergence assessment

| Severity | Count | IDs |
| --- | --- | --- |
| Critical | 0 | — |
| High     | 0 | — |
| Medium   | 0 | — |
| Low      | 7 | DEV-022, DEV-023, DEV-024, DEV-025 (iter5 carry-forwards); DEV-026, DEV-027, DEV-028 (new this iter) |
| Info     | 0 | — |

**iter6 status for P6:** Deferred (rate-limited) — zero fixes applied.

**Severity calibration.** Every finding in this pass is documentation polish within the iter5 rubric:

- The four carry-forwards (DEV-022/023/024/025) were all iter5 Lows and nothing in the spec changed for them; the calibration rubric forbids upgrading severity on unchanged risks.
- The three new findings are all DX consistency gaps (command naming, YAML header shape, multi-language SDK parity) — none breaks a wire contract, none omits a happy-path requirement, and none can strand a runtime author (the Go path works end-to-end; Python/TypeScript authors can reconstruct types from §15.4.1; the §17.4 walkthrough does complete as written even with the CRD-envelope wrapper if the CLI strips it). All Low per the rubric.

**Convergence from the DevEx perspective:** **Yes.** Zero Critical/High/Medium findings remain (same as iter5). The finding count grew from 4 → 7 Lows because iter6 was skipped and a deeper pass of §17.4 surfaced two walkthrough-level consistency items plus one multi-language SDK-parity item that the iter5 pass did not highlight — but no new finding rises above the DX-polish threshold. A single follow-up fix pass handling DEV-022 through DEV-028 together closes the perspective cleanly; alternatively, the reviewer may accept all seven as documentation-iteration debt and declare DevEx converged. The §15.7 / §24 / §26.1 / §26.12 / §17.4 / §5.1 surfaces all remain sufficient for a new runtime author to build, register, and invoke a working Basic-level runtime against Embedded Mode as the primary path; the degraded Basic-level experience (no lifecycle channel, Linux-only for Standard/Full) is still clearly documented; the echo runtime still suffices as a reference; and the SDK/library minimization carve-out for `--language binary --template minimal` is still correctly wired.
