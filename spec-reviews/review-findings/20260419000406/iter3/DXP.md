# Iter3 DXP Review

**Date:** 2026-04-19. **Focus:** regression-check of iter2 fixes on Tier-0/Embedded-Mode onboarding, SDK minimization, package names, conformance suite, and Basic-level shorthand.

All five iter2 findings (DXP-001 through DXP-005) have been resolved correctly. The "Tier 0" label is gone (replaced consistently with "Embedded Mode"), the runtime-author path now names Embedded Mode as primary, §26.1 softened MUST to SHOULD and gates the guidance on Standard/Full only, the `from_mcp_content()` helper now lives in the Runtime Author SDK module, §15.4.6 "Conformance Test Suite" now exists and explicitly maps "conformance level" to Integration Level, and the §15.4.4 Basic-level echo pseudocode now uses the shorthand form.

Two minor residual issues remain, plus one small new inconsistency introduced by the iter2 wording.

---

### DXP-006 §15.7 scaffolder description contradicts §24.18 `binary`/`minimal` no-SDK promise [Low]
**Files:** `15_external-api-surface.md` §15.7 line 2150; `24_lenny-ctl-command-reference.md` §24.18 line 232

§24.18 line 232 (iter2 fix): "The `binary`/`minimal` combination deliberately emits a Basic-level-compliant skeleton with **no SDK imports** — the generated `main` file contains only the stdin/stdout JSON Lines loop from [§15.4.4]... No `github.com/lennylabs/runtime-sdk-go`, no `lenny-runtime` Python package, no `@lennylabs/runtime-sdk` dependency appears in the generated Dockerfile or manifest."

§15.7 line 2150 (unchanged): "The [§24] CLI includes a `lenny runtime init` subcommand that generates a new runtime skeleton (`<runtime>/`): `Dockerfile`, **`main.<lang>` using the SDK's `Handler` interface**, `runtime.yaml` ([§5.1]), a `Makefile` with `build`, `test`, and `push` targets... Supported templates: Go, Python, TypeScript, and the generic stdin/stdout binary template."

The unqualified "`main.<lang>` using the SDK's `Handler` interface" contradicts the `binary`/`minimal` carve-out in §24.18 line 232 — the generic stdin/stdout binary template does **not** use `Handler`, it has no SDK dependency at all. A runtime author reading §15.7 would conclude all scaffolder output is SDK-based.

Additionally, §15.7 line 2154: "Every reference runtime in [§26]... is built on top of the Runtime Author SDKs above, using **the scaffolder output as the starting point**. Third-party runtimes get the same code path at release time." Since §26.1's table shows every reference runtime is Standard or Full level, this holds for first-party runtimes — but the sentence "Third-party runtimes get the same code path" overreaches for Basic-level third parties that use `--template minimal`.

**Recommendation:** Amend §15.7 line 2150 to: "`main.<lang>` using the SDK's `Handler` interface (for `--language {go|python|typescript}`) or a bare stdin/stdout JSON Lines loop with no SDK imports (for `--language binary --template minimal`; see [§24.18])." Amend line 2154 to: "Third-party Standard- and Full-level runtimes get the same code path at release time; Basic-level third-party runtimes using `--template minimal` ship a no-SDK skeleton per [§24.18]."

---

### DXP-007 §26.1 "`local` profile installations" is undefined terminology [Low]
**Files:** `26_reference-runtime-catalog.md` §26.1 line 30; cross-reference: `17_deployment-topology.md` §17.9.1 line 1238

§26.1 line 30: "For **`local` profile installations**, `lenny up` auto-grants access to the `default` tenant for every reference runtime it installs so the developer can invoke them without additional setup."

§26.3 `claude-code` line 173 (YAML comment): "`warmCount: 2           # local profile override: 0 (cold start OK on laptop)`"

There is no named "local profile" anywhere in the spec. The closest constructs are:
1. §17.9.1 line 1238 — **Environment** axis with values `local | dev | staging | prod` (one dimension of an answer-file tuple, not a unified "profile").
2. §17.9.6 line 1422 — `answers/laptop.yaml` answer file which selects `backends=embedded`.
3. §17.4 — **Embedded Mode** (`lenny up`).

A runtime author reading "`local` profile installations" cannot match it to any of these precisely. The `lenny up` auto-grant behavior is an Embedded Mode behavior, not an Environment-axis behavior.

**Recommendation:** Replace "`local` profile installations" with "Embedded Mode installations (`lenny up`, §17.4)" in §26.1 line 30, and replace the `# local profile override` YAML comment in §26.3 line 173 (and any analogous comments in §26.4–§26.11) with `# Embedded Mode override` to match the canonical mode name established in §17.4.

---

### DXP-008 §17.4 "Plugging in a custom runtime" Embedded Mode path omits tenant-access grant step for non-`default` tenants [Low]
**Files:** `17_deployment-topology.md` §17.4 lines 253–288; cross-reference: `26_reference-runtime-catalog.md` §26.1 line 30

§17.4 lines 266–276 describe the Embedded Mode registration flow for a custom runtime:
```
cat > runtime.yaml <<'EOF'
apiVersion: lenny.dev/v1
kind: Runtime
metadata:
  name: my-agent
spec:
  type: agent
  image: my-agent:dev
  integrationLevel: basic
EOF
lenny-ctl runtime register --file runtime.yaml
```

Line 288: "No rebuild of the Lenny platform is required — `lenny up` runs the released `lenny` binary, and `lenny-ctl runtime register` is the same admin API used in production clusters. Basic-level runtimes can stop here..."

However, §26.1 line 30 states: "Reference runtimes are registered by `lenny-ctl install` (§17.6, §24.20) as platform-global records **with no default tenant access grants**. Operators grant access per tenant via `POST /v1/admin/runtimes/{name}/tenant-access` with body `{\"tenantId\": \"<uuid>\"}`... after install. For `local` profile installations, `lenny up` auto-grants access to the `default` tenant **for every reference runtime it installs**..."

The §26.1 auto-grant applies to reference runtimes that `lenny up` installs — not to custom runtimes a user later registers via `lenny-ctl runtime register`. A Basic-level author following §17.4 lines 253–288 registers a platform-global record and then the session-create curl on line 282 will receive `403 FORBIDDEN` under the platform-default `noEnvironmentPolicy: deny-all` ([§17.6](17_deployment-topology.md#176-packaging-and-installation) line 335), because no tenant-access grant was created.

This is the exact onboarding cliff-edge the prior iter2 DXP-001 fix was meant to eliminate. Basic-level authors stop at step 5 and hit a 403.

**Recommendation:** After line 276 (`lenny-ctl runtime register --file runtime.yaml`), insert a required step:
```
# 3b. Grant the default tenant access to the newly-registered runtime
# (lenny up only auto-grants access for reference runtimes it installs; custom
#  runtimes require an explicit tenant-access grant — see §26.1.)
lenny-ctl runtime tenant-access grant my-agent --tenant default
```
Alternatively, extend the `lenny up` auto-grant behavior in §17.4 and §26.1 to also cover custom runtimes registered in Embedded Mode and document that in both sections.

---

## Cross-cutting

- The iter2 fixes resolved all five prior findings. "Tier 0" is not present anywhere in the spec; the Embedded/Source/Compose mode names are used consistently across §15.4.5, §17.4, §23.2, §24.18, §24.19, §26.1, §27.
- "Capacity Tier 1/2/3" (§17.8.2, §17.9.1) is cleanly distinguished from "Embedded/Source/Compose Mode" — no collision observed. §17.4 line 117 makes the disambiguation explicit.
- The three new findings above are all Low severity: DXP-006 and DXP-007 are terminology-consistency issues that a careful reader can work through; DXP-008 is a concrete onboarding cliff that blocks the Basic-level "can stop here" promise with a 403 on first curl.
- Iter1 found nothing to regression-check; all iter1-verified items (OutputPart schema rules, capability matrix, Basic-level limitations list) remain intact.
