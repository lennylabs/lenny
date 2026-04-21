# Perspective 6 — Developer Experience (iter5)

**Scope.** Re-review of the runtime-author / operator developer surfaces against iter4 summary findings DXP-009 … DXP-021, with specific attention to:

- `spec/15_external-api-surface.md` §15.4 (adapter spec, integration levels, echo runtime, conformance tests) and §15.7 (Runtime Author SDKs).
- `spec/24_lenny-ctl-command-reference.md` §24.9, §24.18, §24.19 (tokens, scaffolder, Embedded-Mode image management).
- `spec/26_reference-runtime-catalog.md` §26.1, §26.2, §26.12 (catalog overview, shared patterns, author onramp).
- `spec/17_deployment-topology.md` §17.4 (primary-path Embedded-Mode custom-runtime walkthrough).
- `spec/05_runtime-registry-and-pool-model.md` §5.1 (Runtime schema, `integrationLevel` field).

**Calibration.** iter5 severity anchored to the iter4 rubric (no inflation for unchanged risks). "Would improve DX" alone is Low; only a broken wire contract, missing required command, or misleading-enough-to-cause-task-abandonment finding rates Medium or above.

## Inheritance of prior findings (iter4 DXP-009 … DXP-021)

| iter4 finding | iter5 disposition | Evidence |
| --- | --- | --- |
| DXP-009 §15.7 Protocol codec contradicts stdin/stdout + Unix-socket contract [High] | **Fixed.** §15.7 "Protocol codec" bullet rewritten (lines 2490–2496) to scope by integration level: Basic = stdin/stdout JSON Lines, Standard/Full = abstract-Unix-socket dial helpers for `@lenny-platform-mcp`, `@lenny-connector-<id>`, `@lenny-lifecycle`, with an explicit "runtime does not participate in mTLS" sentence. Matches §15.4.1, §4.7. |
| DXP-010 §15.7 lists non-existent MCP helper tools [High] | **Fixed.** §15.7 "Platform MCP tool helpers" (line 2497) now lists the §4.7 authoritative set (`lenny/delegate_task`, `lenny/await_children`, `lenny/cancel_child`, `lenny/discover_agents`, `lenny/output`, `lenny/request_elicitation`, `lenny/memory_write`, `lenny/memory_query`, `lenny/request_input`, `lenny/send_message`, `lenny/get_task_tree`, `lenny/set_tracing_context`). `tool_call`, `interrupt`, and `ready` are explicitly called out as non-MCP-tools (protocol frame / lifecycle signal / not present). |
| DXP-011 `integrationLevel` field used by §17.4/§15.4.6 but undefined in §5.1 [High] | **Fixed.** §5.1 line 59 declares `integrationLevel: full # basic \| standard \| full — optional; defaults to basic`. Inheritance rules at §5.1 lines 169 and 189 mark it as never-overridable on derived runtimes. §15.4.6 line 2359 and §24.18 line 231 wire `lenny runtime validate` to read the field. All §26 reference-runtime YAMLs declare `integrationLevel: full`. |
| DXP-012 `lenny image import` and `lenny token print` undocumented in §24 [High] | **Fixed.** §24.9 line 120 defines `lenny token print` with Embedded-Mode gate, exit codes, and audience. §24.19.1 (lines 268–291) introduces an "Image Management" subsection with `lenny image import <reference>` (with `--file`, `--namespace`), `lenny image list`, `lenny image rm`, prerequisites, exit codes, and Clustered-Mode rejection. |
| DXP-013 `Handler` interface references undefined `CreateRequest` / `Message` / `Reply` [Medium] | **Fixed.** §15.7 lines 2523–2661 now define all three types as Go structs with per-field doc-comments pointing to the §4.7 adapter manifest, §15.4.1 `MessageEnvelope` / `OutputPart`, §14 `WorkspacePlan`, and §4.9 credential bundle, plus a "do not introduce new wire types" disclaimer (line 2523). |
| DXP-014 §15.7 scaffolder paragraph universalizes SDK use despite §24.18 no-SDK carve-out [Medium] | **Fixed.** §15.7 line 2665 now qualifies the scaffolder sentence with the `--language binary --template minimal` exception and cross-references §24.18. |
| DXP-015 §24.18 cross-product has 12 combinations but only 1 specified [Medium] | **Fixed.** §24.18 lines 234–248 now carry a 4×3 matrix specifying each cell's level and SDK presence, with an "Unsupported combinations" block rejecting `binary/chat` and `binary/coding` with exit code `5 SCAFFOLD_UNSUPPORTED_COMBINATION` and prose rationale pointing to §15.4.3. |
| DXP-016 §26.1 "scaffolder copies one of these as a template" misrepresents templates [Medium] | **Fixed.** §26.1 line 6 now reads "emits one of three templates (`chat`, `coding`, `minimal`)" and spells out which reference runtimes share each template's conventions, with an explicit "There is no per-reference-runtime template." |
| DXP-017 §26.12 references `github.com/lennylabs/runtime-templates` repo but role undefined [Low] | **Not fixed.** §26.12 (line 485) still says "New reference runtimes are proposed via a PR to `github.com/lennylabs/runtime-templates`" with no definition of the repo's role vs. per-runtime repos or vs. the scaffolder templates that ship inside the `lenny-ctl` binary per §15.4.6 / §24.18. Carry-forward as **DXP-022** below. |
| DXP-018 `deadline_signal` vs `deadline_approaching` naming split [Low] | **Not fixed.** §4.7 line 694 advertises capability string `"deadline_signal"`; §4.7 line 703 defines message type `deadline_approaching`; §15.4.3 Full-level pseudocode line 2251 declares `supported = [..., "deadline_signal"]` and then switches on `"deadline_approaching"` (line 2288); §15.4.6 test row at line 2393 is labeled **deadline signal handling** but the assertion text says "On `deadline_signal`, the runtime writes a final `response`…" which reads as the message name even though the actual wire message is `deadline_approaching`. Three names for one concept. Carry-forward as **DXP-023** below. |
| DXP-019 §15.7 scaffolder description implies universal SDK use (iter3 carry-over) [Low] | **Fixed.** Subsumed by DXP-014's fix at §15.7 line 2665. |
| DXP-020 §26.1 "`local` profile installations" terminology undefined (iter3 carry-over) [Low] | **Not fixed.** §26.1 line 30 still says "For `local` profile installations, `lenny up` auto-grants access to the `default` tenant…". `local` is neither a §17.4 Operating Mode nor a §17.6 Install Profile; `lenny up` is Embedded Mode. Carry-forward as **DXP-024** below. |
| DXP-021 §17.4 Embedded-Mode walkthrough omits non-`default`-tenant access grant (iter3 carry-over) [Low] | **Not fixed.** §17.4 walkthrough (lines 263–303) still has no step 3b; the §26.1 "Tenant access" note (line 30) only covers the `default` tenant Embedded-Mode auto-grant case. A non-default-tenant author still hits `RUNTIME_NOT_AUTHORIZED` at first session creation with no in-walkthrough pointer. Carry-forward as **DXP-025** below. |

**Net iter4 carry-forward.** DXP-017, DXP-018, DXP-020, DXP-021 remain unfixed (all Low).

---

## New findings (iter5)

### DXP-022. §26.12 `runtime-templates` repository role still undefined; author onramp ambiguous about where to put code [Low]

**Section:** `spec/26_reference-runtime-catalog.md` §26.12 (line 485); `spec/15_external-api-surface.md` §15.4.6 (line 2395 "fixtures ship inside the `lenny` binary"); `spec/24_lenny-ctl-command-reference.md` §24.18 (line 226).

iter4 DXP-017 carried forward. §26.12 tells new-reference-runtime authors to PR to `github.com/lennylabs/runtime-templates`, but:

- §24.18 describes the scaffolder as emitting files from an in-binary template source (all logic is "local — no API calls are made", line 226). §15.4.6 line 2395 explicitly says "fixtures ship inside the `lenny` binary". There is no external repo consulted at `lenny runtime init` time.
- §26.3–§26.11 each list a **per-runtime** repo (`github.com/lennylabs/runtime-claude-code`, `…-gemini-cli`, …) as the implementation home. A new reference runtime has no reason to live anywhere else.
- The term `runtime-templates` appears **only** in §26.12 — nowhere else in the spec. An author reading §26.12 cannot tell whether this repo (a) holds scaffolder template source that the build pipeline bakes into `lenny-ctl`, (b) is a meta-repo of proposals and ADRs, or (c) was meant to be `runtime-<name>` with the hyphen substitution a typo.

An author following §26.12 as written will file a PR to `github.com/lennylabs/runtime-templates` with a full runtime implementation, the maintainers will redirect them to `github.com/lennylabs/runtime-<name>`, and the author will redo the PR. This is DX friction, not a blocker — Low.

**Recommendation:** Rewrite §26.12 to separate two steps. Step 1: "Scaffold a new runtime locally with `lenny-ctl runtime init <name>` (§24.18) — this emits a repo skeleton identical to the first-party reference runtimes." Step 2: "Push the skeleton to `github.com/lennylabs/runtime-<name>` (a new per-runtime repo) and open a PR adding an appendix entry in this section." Then add a single sentence clarifying `runtime-templates`: either "The scaffolder template source is maintained in `github.com/lennylabs/runtime-templates`; changes land there and are baked into `lenny-ctl` at release time. PRs to `runtime-templates` update the skeletons every author starts from." — or delete the reference entirely if the templates live in the `lenny-ctl` monorepo. Align with §24.18's "no API calls" claim so authors can distinguish template-source contribution from new-reference-runtime contribution.

---

### DXP-023. `deadline_signal` vs `deadline_approaching` tri-name split persists; conformance-test label and capability string do not match the wire message type [Low]

**Section:** `spec/04_system-components.md` §4.7 (lines 694, 703); `spec/15_external-api-surface.md` §15.4.3 pseudocode (lines 2251, 2288), §15.4.6 conformance-test row (line 2393).

iter4 DXP-018 carried forward. Three distinct names still reference one concept:

1. **Capability string** (§4.7 line 694): `"deadline_signal"`.
2. **Wire message `type`** (§4.7 line 703; switched on in §15.4.3 line 2288): `"deadline_approaching"`.
3. **Test label + assertion phrasing** (§15.4.6 line 2393): "deadline signal handling" / "On `deadline_signal`, the runtime writes a final `response` (possibly with `error.code: "DEADLINE_EXCEEDED"`) and exits cleanly before the deadline elapses."

The test assertion string uses a bareword that reads as a wire-level message name (`deadline_signal`) but no message with that `type` ever exists on the wire. An author reading the test row in isolation will `case "deadline_signal":` in their switch statement, observe no signals, and produce a runtime that passes the capability-declaration portion of the handshake but fails the actual deadline path — a silent behavioural bug that the conformance suite phrasing invited.

**Recommendation:** Pick one root noun. Preferred: keep capability string `"deadline_signal"` (it is stable on the handshake surface and authors declare support for a feature, not a message) and rename the test label to match the wire name — "deadline_approaching handling — on `deadline_approaching`, the runtime writes a final `response`…". Alternatively rename the capability to `"deadline_approaching"` to match the message. Either approach closes the split; update §4.7 line 694 or §4.7 line 703, §15.4.3 lines 2251 & 2288, §15.4.6 line 2393, and any §26.2 cross-references in lockstep.

---

### DXP-024. `§26.1` "`local` profile installations" terminology still undefined; no §17.4 / §17.6 term matches [Low]

**Section:** `spec/26_reference-runtime-catalog.md` §26.1 (line 30).

iter4 DXP-020 carried forward. §26.1 line 30: "For `local` profile installations, `lenny up` auto-grants access to the `default` tenant for every reference runtime it installs so the developer can invoke them without additional setup." `local` is:

- **Not** a §17.4 Operating Mode — those are Embedded (`lenny up`), Source (`make run`), and Compose (docker compose).
- **Not** a §17.6 Install Profile — the profiles layered by the Helm values system are `base`, `prod`, `compliance`, plus optional overlays (the keyword `local` does not appear).
- **Not** a §17.8.1 bundled Helm profile.

Since `lenny up` is explicitly the Embedded Mode entry point (§24.19 line 260), the paragraph clearly intends "Embedded Mode installations" — but says something different. A reader grepping for `local` profile configuration will find nothing and conclude the sentence is stale or refers to a feature to be enabled via some unseen config switch.

**Recommendation:** Change line 30 to: "For **Embedded Mode** installations ([§17.4](17_deployment-topology.md#174-local-development-mode-lenny-dev)), `lenny up` auto-grants access to the `default` tenant for every reference runtime it installs so the developer can invoke them without additional setup." No other changes required; `local profile` is not used elsewhere so no broader cleanup is needed.

---

### DXP-025. §17.4 Embedded-Mode custom-runtime walkthrough still omits the non-`default`-tenant access grant step [Low]

**Section:** `spec/17_deployment-topology.md` §17.4 (lines 263–303); `spec/26_reference-runtime-catalog.md` §26.1 (line 30); `spec/15_external-api-surface.md` §15.1 `POST /v1/admin/runtimes/{name}/tenant-access`.

iter4 DXP-021 carried forward. The primary-path custom-runtime walkthrough in §17.4 registers `my-agent` against the embedded gateway via `lenny-ctl runtime register --file runtime.yaml` (line 289) and immediately invokes it via `curl` against `/v1/sessions` (line 297). §26.1 line 30 documents that reference runtimes have no default tenant access grants and that `lenny up` auto-grants access to the **`default`** tenant only. The walkthrough silently assumes the author is invoking from the `default` tenant — the Embedded-Mode token printed by `lenny token print` is indeed scoped to the `default` tenant by default, so the walkthrough happens to work — but:

- If the author later wants to test against a second tenant (a realistic iteration after the first success), the session creation returns `RUNTIME_NOT_AUTHORIZED` with no pointer in the walkthrough to the `POST /v1/admin/runtimes/{name}/tenant-access` fix.
- The `integrationLevel: basic` line added to the walkthrough's `runtime.yaml` (iter4 fix for DXP-011 at line 287) makes `my-agent` behave like a first-party reference runtime from the tenant-access perspective. The §26.1 "no default grants" rule should therefore apply to `my-agent` the same way it applies to `claude-code` — but the walkthrough does not call this out.

**Recommendation:** Insert a step 3b in §17.4 after line 289:

```
# 3b. (Optional) If invoking from a non-`default` tenant, grant tenant access explicitly:
lenny-ctl admin runtimes tenant-access add --name my-agent --tenant <tenant-id>
# (equivalent REST: POST /v1/admin/runtimes/my-agent/tenant-access {"tenantId":"<uuid>"})
# Embedded Mode's `lenny up` auto-grants access for the `default` tenant only; any
# other tenant needs an explicit grant before session creation succeeds.
```

Cross-reference §26.1 "Tenant access" and §15.1. Keep the step marked optional so the happy path (default tenant, already granted) stays one-liner-friendly.

---

## Convergence assessment

- **Critical:** 0
- **High:** 0
- **Medium:** 0
- **Low:** 4 (DXP-022, DXP-023, DXP-024, DXP-025 — all carry-forwards of iter4 Lows whose fixes were not included in this iteration's pass)
- **Info:** 0

**Fixed this iteration:** DXP-009 (High), DXP-010 (High), DXP-011 (High), DXP-012 (High), DXP-013 (Medium), DXP-014 (Medium), DXP-015 (Medium), DXP-016 (Medium), DXP-019 (Low, subsumed by DXP-014 fix). Nine of thirteen iter4 findings addressed, including all four iter4 Highs and all four iter4 Mediums.

**Convergence:** **Yes** from the DevEx perspective. Zero Critical/High/Medium findings remain. The four surviving Lows are unchanged-in-severity carry-forwards and are pure polish (repo-role wording, naming consistency, terminology alignment, an optional walkthrough step). They do not block a runtime author from building, registering, and invoking a working runtime against either Embedded Mode or a clustered install, and none of them represents a broken contract — only documentation drift or naming splits that the reviewer's iter4 rubric already classified as non-blocking. The DevEx surface is materially improved over iter4: the §15.7 SDK contract now matches §15.4 / §4.7, the `integrationLevel` field is first-class in §5.1, both previously-undocumented commands (`lenny image import`, `lenny token print`) are specified, the scaffolder cross-product is fully specified with explicit rejections, and the reference-runtime catalog's template story is internally consistent. Reviewer may accept the four remaining Lows as documentation-iteration debt and declare DevEx convergence; alternatively a single follow-up pass handling DXP-022 / DXP-023 / DXP-024 / DXP-025 together would close the perspective cleanly without blocking the overall spec.
