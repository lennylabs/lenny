# DXP Review — Iteration 2

**Date:** 2026-04-19. **Focus:** Minimum-tier onboarding, echo runtime, SDK minimization, OutputPart vs MCP, iter1 regressions.
**Prior:** iter1/DXP.md ("No real issues found") was accurate for its scope; iter2 issues come from new Tier 0 and Runtime-Author-SDK material.

---

### DXP-001 Runtime-author onboarding still routes to Tier 1 after Tier 0 became primary [High]
**Files:** `15_external-api-surface.md` §15.4.5 step 6 (line 1797); `17_deployment-topology.md` §17.4 (lines 98–165, 232–261); `23_competitive-landscape.md` lines 123, 127

§17.4 declares Tier 0 (`lenny up`) "the primary path for deployers evaluating or using Lenny" and names runtime authors as Tier 0's intended audience (line 108). It is the only local mode that ships reference runtimes and exercises the real Kubernetes code path. Three onboarding surfaces still point Basic-level authors at Tier 1 only: §15.4.5 step 6 ("Use `make run`"), §23.2 persona table (line 123, "`make run` local dev mode"), and §23.2 TTHW (line 127, "clone the repo, run `make run`").

Worse, **there is no documented path for plugging a custom runtime into Tier 0.** §17.4 "Plugging in a custom runtime" (lines 232–261) covers only Tier 1 and Tier 2. A Basic-level author following the primary recommendation hits a wall.

**Recommendation:** (a) Add a Tier 0 case to "Plugging in a custom runtime" showing registration against the embedded gateway. (b) Update §15.4.5 step 6 and §23.2 persona/TTHW text to list both modes with pick-guidance.

---

### DXP-002 "MUST start from the scaffolder" contradicts Basic-level zero-SDK claims [High]
**Files:** `26_reference-runtime-catalog.md` §26.1 (line 6); `15_external-api-surface.md` §15.4.1 (line 1100), §15.4.3 (lines 1501, 1584)

§26.1: "Teams building their own runtimes MUST start from the scaffolder (`lenny-ctl runtime init`, §24.18)." The scaffolder generates skeletons built on the Go/Python/TypeScript Runtime Author SDKs. This contradicts §15.4.1 line 1100 ("**No SDK required**"), §15.4.3 line 1501 ("Zero Lenny knowledge required"), and §15.4.3 line 1584 ("Basic level prioritizes simplicity and zero Lenny knowledge").

Consequences: (1) a runtime author in a language the SDK does not support (Rust, Java, Ruby) sees an unsatisfiable MUST — §24.18 offers only `{go|python|typescript|binary}`. (2) The "zero Lenny knowledge" promise is negated by a mandatory Lenny CLI + template.

**Recommendation:** Soften §26.1 to: "Teams building Standard- or Full-level runtimes SHOULD start from the scaffolder. Basic-level runtimes MAY implement the stdin/stdout protocol directly (see §15.4.4)." Also confirm §24.18's `binary`/`minimal` template emits a Basic-level-compliant skeleton with no SDK imports, and document that explicitly.

---

### DXP-003 `from_mcp_content()` package path conflicts with Runtime Author SDK package name [Medium]
**Files:** `15_external-api-surface.md` §15.4.1 line 1098; §15.7 lines 1878–1880

§15.4.1 says the Go helper ships in `github.com/lennylabs/lenny-sdk-go/outputpart`. §15.7 declares the Runtime Author SDK as `github.com/lennylabs/runtime-sdk-go`. Different modules; `lenny-sdk-go` is also ambiguous with the Client SDKs in §15.6.

**Recommendation:** Change §15.4.1 line 1098 to `github.com/lennylabs/runtime-sdk-go/outputpart` (or the exact chosen sub-package). Audit SDK package references across §15.4.1, §15.6, §15.7 for consistency.

---

### DXP-004 "Conformance test suite" referenced but not defined in §15.4.3 [Medium]
**Files:** `26_reference-runtime-catalog.md` §26.1 line 8; `15_external-api-surface.md` §15.4.3

§26.1: "Each ships a conformance test suite ([§15.4.3]...)" and "Reference runtimes claim a **conformance level** in their README; CI fails the release if conformance tests for the claimed level regress." §15.4.3 defines Integration Levels and the capability matrix but **does not define conformance tests, their structure, how they run, or what they assert.** "Conformance level" is undefined — unclear whether it equals Basic/Standard/Full or is independent.

**Recommendation:** Either (a) add a §15.4.6 "Conformance Test Suite" listing test categories per level (stdin/stdout protocol, 10 s heartbeat ack, shutdown-within-`deadline_ms`, MCP nonce handshake, lifecycle-channel handling) and the `lenny runtime validate` entry point; or (b) retarget §26.1's cross-reference to §24.18 and define "conformance level" as equal to Integration Level.

---

### DXP-005 §15.4.4 echo pseudocode omits the Basic-level shorthand it advertises [Low]
**Files:** `15_external-api-surface.md` §15.4.1 lines 1088–1094; §15.4.4 lines 1598–1622

§15.4.1 advertises `{"type": "response", "text": "..."}` as the minimal Basic-level shorthand. §15.4.4's Basic-level echo pseudocode writes the canonical nested form (`"output": [{"type": "text", "inline": "..."}]`) instead. The sample is meant to be the lowest-barrier copy-paste starting point; the verbose form makes the shorthand look optional despite being explicitly promoted for Basic level.

**Recommendation:** Use the shorthand as the primary Basic-level example in §15.4.4, with a trailing note that the canonical form is available when structured output is needed.

---

## Cross-cutting

- Tier 0 was added for the deployer/evaluator persona without propagating to runtime-author surfaces (DXP-001, DXP-002).
- SDK-minimization claims in §15.4 are undermined by MUSTs in §15.7 and §26.1 (DXP-002, DXP-003).
- No regressions observed on iter1's checked items (OutputPart schema rules, capability matrix, Basic-level limitations list, `from_mcp_content` helper). New issues come from Tier 0 and Runtime-Author-SDK material layered on top.
