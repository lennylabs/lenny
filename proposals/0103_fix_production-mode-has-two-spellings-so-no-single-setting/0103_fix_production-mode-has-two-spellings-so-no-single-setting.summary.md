# Summary: Production mode is Environment `prod` on `--environment`, read by every gate through one predicate

## Summary

**Problem statement.** The specification spells production mode two ways: §17.9.1 gives the Environment value `prod`, and §11.7, §12.3, §16.4, §16.5, and §17.6 say `LENNY_ENV=production`. The chart renders only `--environment`, which turns the KMS local-provider refusal on. The gateway reads `LENNY_ENV` directly and compares it to `production`, so on a chart install with `environment: prod` the regulated-tenant SIEM and pgaudit refusals, the audit-ledger integrity refusal, the production-only audit alerts, and the batching warning all stay off. On a regulated-tenant deployment this is a silent compliance failure. lenny-ops carries a further production signal, `--production` (`ops.production`), that can disagree with `--environment`, and the preflight hides the split by accepting both spellings.

**What changes.**
- §17.9.1 gains a **Production mode.** paragraph that defines production mode, its carrier, and its fail-closed refusal, and every `LENNY_ENV=production` site cites it (SPEC-1, SPEC-2).
- A new import-free leaf package, `pkg/deployenv`, holds the closed set and the predicate, and `providerflags.Bind` validates the carrier in its finalizer (CODE-1, CODE-2).
- The gateway gates read the finalized `--environment` value through pure decision functions (CODE-3).
- lenny-ops loses `--production` and its duplicate local-KMS refusal and derives production mode from `--environment`; the chart loses `ops.production` (CODE-4, CHART-1, DOCS-1).
- The preflight, ctlcli, alert descriptions, gauge help, and metric catalog use the one set and the one spelling, and a Tier 0 guard rejects a second carrier (CODE-5, CODE-6, TEST-1).

**Decisions.**
- D1. Production mode is Environment `prod`, carried by `--environment` with `LENNY_ENV` as the gateway, Token Service, and lenny-ops fallback. Every existing reader of the value (§17.9.1, the chart default, the answer files, ctlcli, and the KMS gate) already uses `prod`, so only prose and gate comparisons change and no value set changes.
- D2. The design reuses existing surfaces: §17.9.1's value set, the `--environment` flag `providerflags.Bind` already registers on the gateway, the Token Service, and lenny-ops, and the chart's single `lenny.kmsArgs` render. It adds no flag, chart value, metric, or wire field, and it is the design with the fewest moving parts that makes one chart value control every gate.
- D3. The set and predicate live in `pkg/deployenv`, which imports only the standard library. Placing them in providerflags would link 7 cloud KMS SDK packages into lenny-ctl and lenny-controller, which import `pkg/preflight` and link none today (verified with `go list -deps`). A local copy in `pkg/preflight` would be a second predicate. The name `pkg/environment` is taken by the §10.6 Environment resource.
- D4. A non-empty value outside the closed set makes the gateway, the Token Service, and lenny-ops refuse to start. Without the refusal, retiring `production` would turn a hand-set `LENNY_ENV=production`, which turns the gateway gates on today, into a silent non-production start. An unset value means non-production, because tests, `lenny up`, embedded mode, and harnesses start binaries without the flag while the chart always renders it.
- D5. Matching is exact (`v == "prod"`). The chart conditionals that replace `ops.production` compare `eq .Values.environment "prod"` exactly, so a case-folding binary would read `PROD` as production while the chart rendered the dev trust hook. Accepting `PROD` would also be a second spelling.
- D6. lenny-ops `--production`, `LENNY_PRODUCTION`, `ops.production`, and the preset keys are deleted, and lenny-ops derives production mode from `--environment`. The spec never names `--production`; §10.2 places lenny-ops in one production deployment with the gateway and the Token Service; §25.2 and §25.11 say "in production". The lenny-ops behaviors keep their `production bool` parameters and do not change.
- D7. lenny-ops' own local-KMS pre-check is deleted. Its comment says it exists only because `--production` could disagree with `--environment`, and `providerflags.Resolve`, which `loadOpsKeySet` already calls, refuses local (including an empty provider) under `prod`.
- D8. `staging` is not production mode. A staging install that set `ops.production` or layered a tier2 or tier3 preset loses the lenny-ops full-backup confirmation and the `/healthz` readiness variant, and a Tier 1 install that set `ops.production` also stops requiring `Idempotency-Key`, which §25.4 keys on tier (CODE-4 step 2). This matches §25.11 "in production" and the §17.6 note that capacity tier and environment are independent axes, and it is an intended behavior change.
- D9. The preflight's second spelling is retired: `isProductionEnvironment` is deleted, and the cosign set narrows to `prod` or `staging`, which keeps §5.3's "production or staging".
- D10. Each gateway gate's decision moves into a pure function, because the gates call `log.Fatalf` inline and a predicate test alone does not pin each gate's own condition (the message match, `regulatedPresent`, or the integrity error).
- D11. A Tier 0 static guard fails on a `"LENNY_ENV"` literal outside providerflags and on any `"LENNY_PRODUCTION"` literal. Without it the next direct read reintroduces the split. It lives in `tests/tier0_static` because a test in providerflags would not run under `lenny-test --changed` when a new read lands in `cmd/lenny-gateway`.
- D12. ctlcli's `knownEnvironments` is replaced by `deployenv.Validate`, because a second copy of the four values violates one implementation per concern.
- D13. The metric name `lenny_env_production` and both alert expressions are kept; the name stays accurate under the new definition.
- D14. The spec gets one definition paragraph, and the undefined "in production" and "in a production deployment" phrases in §10.2, §17.4, §25.2, and §25.11 are bound by its closing sentence rather than edited one by one. The closing sentence covers component behavior only, so operator guidance addressed to production deployments in general keeps applying to staging.

**Watch out for.**
- CODE-4 and CHART-1 land in one commit with `make generate`. The embedded manifest renders `--production=false`; if it survives the flag's deletion, lenny-ops exits at flag parsing, and a tier-5 run fails with pods that never reach Ready.
- `deployenv.Validate("")` returns nil. ctlcli's `validateAnswers` rejects an empty Environment today and must keep the explicit empty check that CODE-5 stages.
- Do not add `EqualFold` or `TrimSpace` anywhere in the production path, in Go or in the chart (D5).
- The gateway gates read `f.kmsOpts.Environment` only after `kmsFinalize` has run in `main`, and lenny-ops calls `productionMode()` only after its `kmsFinalize`. A gate moved ahead of the finalizer reads an unvalidated value.
- The SIEM and pgaudit refusals keep their non-matching-error branch ("could not list tenants"). A rewrite that folds it into the fatal path changes behavior; CODE-3 states the structure.
- The existing preflight tests assert that `production`, `PROD`, and ` Staging ` warn. They have to be inverted; adding new rows next to them leaves contradictory assertions.
- The generated alert YAML and `values.schema.json` are regenerated with their generators. Hand edits fail the drift checks.
- The line numbers in the problem statement are evidence from validation time. Locate every site by symbol.

## Goals

- One setting, `environment: prod` in the chart or `--environment=prod` on a binary, turns on every production gate in every Lenny binary and the preflight.
- The specification states production mode once, in §17.9.1, and every production-dependent site cites it.
- An off-set value fails closed at startup rather than silently running outside production mode.
- No code reads production mode from anything but the finalized `--environment` value, and a static gate holds that.

## Non-goals

- Keying production-only gates on `--dev-mode` under §17.4 item 3. Item 3 governs relaxations in dev mode, the design keeps two signals (KMS on `--environment`, audit on `--dev-mode`), it makes `AuditSIEMNotConfigured` and `AuditRetentionLow` fire on staging and non-dev `dev` clusters, and §17.9.1 lists alert thresholds as Environment-driven.
- Keeping `ops.production` as a staging opt-in derived in the chart (`or .Values.ops.production (eq .Values.environment "prod")`). It keeps two chart values and two binary inputs that a hand-run or embedded invocation can set inconsistently, and gives "production" a second meaning on staging.
- Stating lenny-ops `--production` as independent of Environment. It leaves `environment: prod` with `ops.production: false` configurable, misses §25.11 on the default production install, and keeps capacity tier tied to production posture.
- Keeping `--production` and defaulting it from `--environment`. That is an override that can switch production posture off under `environment: prod`.
- Adopting `production` as the canonical spelling. It would change §17.9.1, §17.9.2, the answer files, ctlcli, the chart default and tests, and the KMS gate, which changes a value set.
- Accepting both `prod` and `production` through a shared predicate. Two spellings is the defect, and the finalizer's refusal covers a stray `production`.
- Having the chart set `LENNY_ENV` as a container env var. It adds a second carrier beside `--environment`.
- A new `--production-mode` flag or `LENNY_PRODUCTION_MODE` variable. `--environment` already reaches every binary.
- Case-insensitive matching (D5).
- Refusing to start when the environment is unset (D4).
- Normalizing an empty value to `dev` inside `Bind`. Accepting empty as non-production is one rule fewer, and no gate distinguishes empty from `dev`.
- An `enum` on `environment` in the chart values schema. It restates the validation the binary finalizer enforces, and an off-set value already fails closed and visibly at pod start.
- Validating `--environment` inside lenny-preflight or dropping the preflight Job's `| default "dev"`. Both renders read the one chart value, and the preflight warnings are advisory.
- Editing each "in production" phrase in §10.2, the §17.4 embedded KMS row, §25.2, §25.11, and the generic "fatal in production" phrases in §11.7 (D14).
- Rewriting the §12.3 sentence "regardless of `LENNY_ENV` or SIEM configuration". It carries no production spelling and stays true.
- Renaming the `lenny_env_production` metric (D13).
- An AST guard on `== "production"` comparisons. The literal guard plus every gate reading `deployenv` covers the reintroduction path without matching unrelated `production` strings.
- Recording the environment in Postgres to prevent replica divergence. The single chart key already prevents it.
- A chart `fail` guard for a leftover `ops.production` key. No deployments exist, and the project forbids migration shims.
- Updating the BUILD-GAPS F-17.9.24 status and the remediation-plan queue entry. That is records work at implementation, outside the staged edits.

## Open decisions for human to make

None. The spec and non-spec review loops left no decision open for the human.

## Defects in the shipped tree that this proposal does not stage

- **§11.7 tenant listing fails open at gateway startup.** When the tenant listing for the SIEM and pgaudit compliance checks errors, `billing_pipeline.go` logs "could not list tenants" and continues, even in production mode. It is outside the spelling and carrier defect, and it needs its own finding.
- **§17.6 etcd Secret encryption and node disk encryption warnings have no implementation.** No environment-keyed implementation of either check was found in `pkg/preflight`. This proposal changes only their spec wording; whether they are implemented is a separate finding.
- **§25.4 lenny-ops readiness probe path depends on production in the chart.** `charts/lenny/templates/ops-deployment.yaml` sets the lenny-ops readinessProbe path to `/healthz` when `ops.production` is set and to `/readyz` otherwise, while §25.4 gives `/healthz` unconditionally. CHART-1 re-keys the switch to `environment: prod` (D8) and leaves the disagreement with §25.4 in place. It needs its own finding.
- **lenny-ops production-keyed dev-mode gates have no spec statement.** lenny-ops refuses the dev trust hook in production (`errDevTrustInProduction` in `cmd/lenny-ops/main.go`) and suppresses its dev relaxations in production (`devRelaxed = devMode && !production`). §17.4 "Dev mode guard rails" item 3 names only the gateway's hook and keys it on `LENNY_DEV_MODE`. CODE-4 re-keys both gates to `--environment` and leaves their behavior unchanged. Stating them in the spec needs its own finding.
- **§17.6 installer question omits `staging`.** The "Target environment" question lists `local | dev | prod`. The omission predates this proposal and does not cause the defect.
- **lenny-ops `--platform-tier` is unvalidated.** `cmd/lenny-ops/flags.go:543` registers it as a free string with default `""`, and `charts/lenny/values.schema.json:110-113` types `global.deploymentTier` as a plain string with no enum. CODE-4's `idempotencyKeyRequired()` reads an empty or off-set value as Tier 1, so the §25.4 required `Idempotency-Key` is not enforced for that install. CHART-1 renders the value with `| default "tier1"` and every preset sets a valid tier, so only an operator-typed off-set value reaches the fallback. Tier validation is outside the production-mode spelling defect, and it needs its own finding.

## Impacts on other proposals

| Proposal | Status | What this change does to it | What it must do |
|:--|:--|:--|:--|
| 0097 (Lenny-issued bearer tokens verify on every process) | Implemented | 0097 recorded the production-mode spelling drift as an unstaged pre-existing defect; 0103 fixes it | Nothing. 0097 is not edited |
| 0100 (terminal pipeline) | Approved | 0100 edits `cmd/lenny-gateway/stores.go` wiring and a different §16.5 row; 0103 edits the `integrity.Verify` fatal in the same file and the `AuditSIEMNotConfigured` and `AuditRetentionLow` rows. The anchors are disjoint | Whichever lands second re-reads `stores.go` and the §16.5 table and applies its edits by quoted anchor with no change to content |
| 0101 (off-holder request forwarding) | Approved (2026-10-26) | 0101 edits `cmd/lenny-gateway/metricsbackfill.go` (the coordination fencer) and adds a `tests/testinfra/gatewaypki` key to `tests/change-graph.json`. 0103 edits the `SetEnvProduction` and `auditBatchingNoSIEM` calls in `metricsbackfill.go` and adds a `"pkg/deployenv"` key to `tests/change-graph.json`. The anchors and keys are disjoint, so no 0101 deliverable loses its subject | Whichever lands second re-reads `metricsbackfill.go` and `tests/change-graph.json` and confirms that both changes are present. 0101's approval stands and needs no human re-review |
| 0102 (platform-admin impersonation revocation) | Approved | 0102 SPEC-7 edits a §11.7 item and the §16.5 `PlatformAuditResidencyViolation` row; 0103 edits other §11.7 bullets and other §16.5 rows. The anchors are disjoint | Whichever lands second applies its edits by quoted anchor with no change to content |

## Deliverable index

- SPEC-1 — spec/17_deployment-topology.md — define production mode in §17.9.1
- SPEC-2 — spec/11_policy-and-controls.md, spec/12_storage-architecture.md, spec/16_observability.md, spec/17_deployment-topology.md — rewrite every `LENNY_ENV=production` site to cite §17.9.1
- CODE-1 — pkg/deployenv/deployenv.go — the closed set and the production predicate
- CODE-2 — pkg/kms/providerflags/providerflags.go — validate `--environment` in the finalizer and use the predicate in `Resolve`
- CODE-3 — cmd/lenny-gateway/production_gates.go — gateway gates read the finalized carrier through pure decision functions
- CODE-4 — cmd/lenny-ops/flags.go — lenny-ops derives production mode from `--environment`; `--production` and the duplicate local-KMS refusal are deleted
- CODE-5 — pkg/preflight/install_prereqs.go — preflight and ctlcli use the shared set; the dual spellings are retired
- CODE-6 — pkg/alerting/rules/rules.go — alert descriptions, gauge help, and metric catalog drop the retired spelling
- CHART-1 — charts/lenny/templates/ops-deployment.yaml — the chart renders one production signal; `ops.production` is deleted
- DOCS-1 — docs/operator-guide/install-wizard.md — preset paragraph keys the backup confirmation on the `prod` environment
- TEST-1 — tests/tier0_static/production_mode_carrier_test.go — Tier 0 guard against a second production carrier
