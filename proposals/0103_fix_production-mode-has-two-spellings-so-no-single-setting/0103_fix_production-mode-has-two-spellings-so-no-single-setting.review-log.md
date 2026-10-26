# Review log — 0103_fix_production-mode-has-two-spellings-so-no-single-setting

## Standing context

Changelog (compaction pass 1, after spec rounds 1-3, reconcile.1, and non-spec rounds 1-6):
- Lifted the durable residue of every ledger entry from [spec.1.fix-G1.1] through [non-spec.6.review-test-coverage.1]. Repeated FACTs (the ten-site inventory, the three Bind callers, kmsFinalize ordering, the opsidem required-key set, `--platform-tier` registration, change-graph key spelling) were merged into one line each.
- Retired as closed: the three DEFERREDs on the summary.md defects list (closed by [reconcile.1] CORRECTS); the DEFERRED on a docs sentence for the off-set refusal, carried as OPEN by [reconcile.1] and closed by the [non-spec.1.review-docs-alignment.1] DECISION; the UNVERIFIED on unstated lenny-ops production gates (closed by the [reconcile.1] defects bullets); the UNVERIFIED on the "metric catalog consistency check" (closed by [non-spec.1.fix-G2.1]).
- Deleted superseded WATCHOUTs: SPEC-1 fallback sentence false for lenny-preflight ([spec.1.review-applicability.1], fixed by scoping in [spec.1.fix-G1.1]); behavior-kinds parenthetical too narrow ([spec.1.review-security.1], parenthetical deleted). Kept as withdrawn-option traps: keying the required key on productionMode() ([non-spec.1.review-mechanism.1]) and the CODE-3 carrier test ([non-spec.1.review-test-coverage.1]).
- Applied CORRECTS [non-spec.2.fix-G1.1]: the required-key 400 does not need Postgres; the older claim in [non-spec.2.fix-design-G1.1] is dropped.
- Disagreement noted: [non-spec.6.review-test-coverage.1] says the gateway gate sites use `w.f` rather than a local `f`; the newer [non-spec.6.fix-G1.1]/[non-spec.6.fix-design-G1.1] say each build method opens with `f := w.f`. Both hold at once; the fix wording is kept.
- Reached the 200-line target.

### Settled

**Plan scope**: the plan's only entry is the "Production-mode spelling proposal" queue item (F-17.9.24); BUILD-GAPS adds a tier-1 test per gate and a chart render test; all staged; records update is a declared non-goal.
**Plan invariants**: INV-1..7, S-6, and the 2026-10-12 expansive-test instruction do not bind 0103.
**Ten spec sites**: spec/ has exactly ten `LENNY_ENV=production` sites (§11.7 x3, §12.3 x1, §16.4 x1, §16.5 x2, §17.6 checks table x3); SPEC-2 (a)-(j) map one-to-one and every quoted anchor matches verbatim.
**§12.3 bare mention**: "regardless of `LENNY_ENV` or SIEM configuration" (§12.3) is the one other spec mention and is deliberately kept.
**SPEC-1 anchors**: "#### 17.9.1 Composition Dimensions", the Environment row cell starting "Alert thresholds, log verbosity,", and the "Each dimension has an independent default" paragraph exist; slug `#1791-composition-dimensions` follows the tree convention.
**No overlap**: proposals 0100-0102 stage no edit to any SPEC-1/SPEC-2 target.
**No spec carrier yet**: spec/ names no `--environment` carrier, `--production`, `LENNY_PRODUCTION`, `ops.production`, or `lenny_env_production`; SPEC-1 duplicates no spec text.
**SPEC-1 is the one home**: SPEC-1 edit (b) is the only home of predicate, carrier, fallback scope, refusal, unset rule, and staging rule; summary D4 is the refusal's reason home.
**spec-changes Design cut**: Design is a pointer at SPEC-1; Edge cases lost lead-in, Unset, Off-set, and Staging bullets; §5.3 non-edit survives as a bullet citing D9; Preflight Job and Operator guidance bullets kept.
**Fallback scoped**: SPEC-1's `LENNY_ENV` fallback sentence names the gateway, Token Service, and lenny-ops; summary D1 matches.
**Parenthetical dropped**: SPEC-1's closing sentence has no behavior-kinds list, so it binds the §15.1 connector "`https` only in production" rule.
**Three Bind callers**: only cmd/lenny-gateway/flags.go, cmd/lenny-ops/flags.go, and cmd/lenny-token-service/flags.go call providerflags.Bind, the only `LENNY_ENV` fallback; all default Provider to local.
**kmsFinalize first**: all three Bind binaries call kmsFinalize in main before any wiring, so every production read sees the validated value.
**lenny-preflight flag**: lenny-preflight declares its own `--environment` with default "" and no env read; CODE-5 keeps it so (no deployenv.Validate).
**Chart render**: `--environment` renders through `lenny.kmsArgs` on gateway, Token Service, and lenny-ops, and through preflight-job.yaml with `| default "dev"`.
**Precedence and case**: flag over `LENNY_ENV` matches providerflags; Resolve uses EqualFold today, SPEC-1 requires exact match, non-spec staging owns the change; an off-set `PROD` still fails closed via Validate.
**connector https**: connectorstore refuses non-https in every environment, stricter than §15.1 and conformant.
**External adapter gate**: the §15 external-adapter production gate does not depend on environment.
**§10.3 list**: §10.3 startup validation is a required-key list; the off-set refusal does not belong there.
**Production profiles**: "production profiles" (§4, §13, §16) key on `global.devMode`/`otlpTlsEnabled`, outside SPEC-1.
**Unspelled sites bound**: §10.2/§17.4 local-KMS refusal, §11.7 pgaudit fatal, §13.1 preflight hard-fail, §15.1 connector https, §25 backup confirm are bound by SPEC-1's closing sentence with no edit.
**No client contract**: production mode reaches no OpenAPI, SDK, schema, CRD, or MCP field; lenny-ops backup REST field stays `confirm`; lenny-ops has no OpenAPI.
**lenny-ctl preflight**: standalone `lenny-ctl preflight` reads the values.yaml `environment` key, the same chart value.
**Code readers**: non-providerflags readers are gateway `LENNY_ENV` sites (billing_pipeline.go x3, stores.go x1, metricsbackfill.go x2) and lenny-ops `--production`; CODE-2..5 stage all; pkg/loadctl "production" and preflight `tierIsProduction` are unrelated.
**No other binary gates**: lenny-controller, pool-scaling-controller, lenny-backup, lenny-compliance, and webhooks carry no production gate.
**ctlcli tier default**: pkg/ctlcli/install.go comparing Environment to "prod"||"staging" for a default tier is not a production gate.
**lenny_env_production gauge**: alert expressions read it; no spec metric table lists it (pre-existing).
**Alert text copies**: rules.go plus three generated renders (docs/alerting/rules.yaml, charts/lenny/files/alerting-rules.yaml, pkg/embedded/manifests/manifests.yaml); CODE-6 regenerates.
**Docs sites**: docs/ names only `ops.production` at install-wizard.md:176 (DOCS-1); generic "production mode" pages stay true; configuration.md has no `environment` row (pre-existing).
**Docs off-set refusal**: no docs finding for the off-set refusal; the audit-ocsf.md fix carries "(`environment: prod`)" (closes the [reconcile.1] OPEN).
**audit-ocsf.md:91**: states the SIEM refusal unconditionally while §11.7 refuses only in production; recorded in [non-spec.1.review-docs-alignment.1].
**Values schema**: `ops` is an untyped Object, so a stale `ops.production` is ignored; Environment description lives at pkg/chart/values/values.go.
**No extraEnv**: the chart has no extraEnv hook on gateway, Token Service, or ops.
**No test sets off-set env**: no test or harness sets `LENNY_ENV`/`--environment` off-set; token-service flags_test.go:154 only clears it (test file, TEST-1 skips).
**TEST-1 green on landing**: after S5 and S6 the only non-test `"LENNY_ENV"` literal is providerflags.go:211 and no `"LENNY_PRODUCTION"` remains.
**Checklist deps**: S9 (DOCS-1) and S10 (TEST-1) depend on S1; S1..S10 acyclic; deliverable index complete.
**Opsserver.Options.Production split**: one bool fed both §25.11 confirm and §25.4 required key; split into Production (productionMode()) and IdempotencyKeyRequired (`--platform-tier` tier2|tier3) passed to opsidem.Config.Production.
**Platform-tier render**: CHART-1 renders `--platform-tier={{ .Values.global.deploymentTier | default "tier1" }}` on lenny-ops; values default tier1; tier2/tier3 presets set it under `global:`.
**--platform-tier today**: lenny-ops registers it (LENNY_PLATFORM_TIER, default "") for GET /v1/admin/me only; rendering it fills platform.tier, which aligns with §25.4; no docs name it.
**Required-key set**: opsidem requires a key only on POST upgrade/start, restore/execute, and full backups; 400 and the outage 503 both key on Config.Production.
**tier-4 tests unaffected**: the two tier-4 tests starting lenny-ops with tier2 POST only remediation-locks.
**Required-key test setters**: idempotency_test.go idemBackupServer, tier2 opsidem/outage_test.go, tier3 upgrade_test.go:234 and upgrade_idempotency_test.go:83,147 move to IdempotencyKeyRequired; all send confirm:true; backup_test.go stays.
**Tier-4 wiring pin**: one key-less upgrade/start assertion in TestOpsServiceRemediationLockPersistenceE2E (tier2, no `--environment`) expects 400; opsidem wraps the mux before routing.
**Idem store never nil**: buildIdempotencyStore falls back to a memory store, so every lenny-ops process installs the middleware (CORRECTS [non-spec.2.fix-design-G1.1]).
**lenny-ops ordering**: checkDevTrustPosture runs before loadOpsKeySet/Resolve, so prod plus trust hook reports the trust-hook error first.
**lenny-ops test dispositions**: CODE-4 step 6 is the one home; the three auth tests are comment-only (they pass a `production bool`); only flags_test.go and keyset_test.go touch the deleted field, both false.
**Auth test comment sites**: devmode_gate_test.go:18,20,101; auth_config_test.go:102,137; revocation_test.go:488.
**CODE-6 text**: step 3 says gauge Help and catalog entry "both read" the target string; the consistency-check test bullet is deleted.
**Catalog lookup**: the function is `metrics.LookupMetric`; catalog tests check only non-empty Help, sort order, and §16.1 completeness.
**Change-graph key**: CODE-1 Targets add a `"pkg/deployenv"` key listing pkg/deployenv plus providerflags, preflight, ctlcli, cmd/lenny-gateway, cmd/lenny-ops; S3 is "CODE-1."
**Change-graph coverage rule**: a glob key on the path or an ancestor, or an exact-directory baseline prefix; baseline never grows; bare and trailing-slash keys both valid.
**Covered new files**: cmd/lenny-gateway/production_gates.go and tests/tier0_static files are covered by existing keys; tier0_static always runs.
**CODE-3 wiring test**: the carrier test is replaced by a source-text test over billing_pipeline.go, stores.go, metricsbackfill.go asserting `production := deployenv.IsProduction(f.kmsOpts.Environment)` and `production` first in each listed call; precedent capability_token_ttl_test.go.
**Gateway harness limit**: tier-4 gateway harness runs on local KMS, so no binary-level prod gateway test; D10 tier-1 functions are the pin.
**Problem statement fix**: the lenny-ops `--production` enumeration now includes the §25.4 required key.
**No reliability surface**: every gate is a one-shot startup check; no retry, lease, or redelivery path.
**No security finding**: D8 loosening on tier2/3 with non-prod env is less strict, aligns lenny-ops with the gateway (§17.4 keys relaxation on LENNY_DEV_MODE); not filed.
**Docs-OPEN status**: the review log carries no OPEN decision for the human; summary "Open decisions" stays.

### Traps

**Do not re-add lists outside SPEC-1**: no list of behavior kinds or fallback binaries in Design, Edge cases, or D1. SPEC-1 edit (b) is the only home; a closed list leaves the next uncovered kind unbound.

**Withdrawn: lenny-preflight fallback**: rejected adding a `LENNY_ENV` fallback to lenny-preflight (breaks TEST-1's literal guard) and rejected deleting the spec fallback sentence (contradicts "No other ... environment variable"). Any fallback sentence names the three Bind binaries, never "a binary".

**Withdrawn: required key on productionMode()**: round 1 CODE-4 re-keyed the §25.4 required key to `prod`, contradicting "at Tier 2/3". Also rejected: recording it in D8 (ships fail-open), deriving from `opts.Me.Tier` (nil-able display struct), keeping `ops.production` (D6's deleted carrier).

**Withdrawn: CODE-3 carrier test**: it drove only providerflags.Bind, duplicated CODE-2, and passed with a call-site bool replaced by false. Also rejected: a gatewayFlags.productionMode() method, a subprocess log.Fatalf test (needs tenants store and Postgres).

**CODE-3 statement is fixed text**: the wiring test asserts CODE-3's literal `f.kmsOpts.Environment` spelling (each build method opens `f := w.f`). Do not assert `w.f.kmsOpts` or copy the call list into the test bullet; rewording the sketch line means updating the test.

**Withdrawn: Help equality test**: no check compares gatewaymetrics Help with catalog text (texts differ today). Rejected a one-gauge tier-1 equality test and a package-wide check. Do not reintroduce a test bullet claiming such a check; `metrics.Lookup` does not exist.

**helm-unittest notContains**: matches whole elements, so `notContains: --production` passes vacuously. Assert the exact rendered element `--production=false` or `--production=true`.

**Default-render chart case**: ops-deployment_test.yaml "renders no --dev-mode on lenny-ops by default" asserts `contains --production=false` without setting ops.production, so "rewrite the cases that set ops.production" missed it. Filed in non-spec round 1; later rounds found every site in target lists.

**Unvalidated --platform-tier**: "" or an off-set `global.deploymentTier` reads as Tier 1 (keys optional). Accepted as out of scope; filing it was rejected as hypothetical hardening.

**Tier1+prod process case**: do not add a tier-4 case starting lenny-ops with `--platform-tier=tier1 --environment=prod`; opsprocess uses local KMS, which Resolve refuses under prod, so it never starts. flags_test.go covers tier1+prod.

**idemBackupServer one bool**: the helper takes one bool; split rows need both Options fields, so it gains a parameter or rows build Options directly.

**New package needs change-graph key**: any new pkg/ or cmd/ directory needs its own key in the same deliverable or validate-maps fails; a later step making another package import deployenv owes that package in the key.

**Baseline refused**: rejected a baseline entry in change-graph-coverage.yaml (downward-only, an exemption) and a key naming only pkg/deployenv/... (selects no caller tests under `--changed`).

**keyset_test.go comments**: keyset_test.go:25,47 comments name `--production`; step 6's comment list does not name them.

**TEST-1 literal scope**: TEST-1 matches only the exact literal "LENNY_ENV"; Help or description strings containing "LENNY_ENV=production" are removed only by CODE-6.

**Pre-existing §17.6 wizard list**: the §17.6 "Target environment" row and "independent axes" note list only `local | dev | prod`; summary records it as a pre-existing defect. Do not file it.

**§17.6 installer note partial**: already omitted LENNY_DEV_MODE before SPEC-1; not a copy SPEC-1 makes stale.

**§25 tier gloss refuted**: §25 "Tier 1 (dev) ... Tier 2/3 (production)" glosses capacity tiers; the Prometheus requirement keys on tier. Filed once in spec round 3 and refuted; do not re-file.

**problem-statement fallback mentions**: problem-statement.md lines describing current §17.9.1 with `LENNY_ENV` fallback describe the current spec, not SPEC-1; leave them.

**Pre-existing spec duplications**: §16.4 SIEM paragraph and §16.5 AuditRetentionLow row restate retention-low; §11.7 states the SIEM refusal twice. Both predate 0103; not (g) findings.

**Summary watch-out restatements**: summary "Watch out for" bullets restate rules (same-commit, ctlcli empty check, finalizer ordering). Not filed; a lens that disagrees files them together in one finding.

**ctlcli error string**: the kept "environment %q is not one of local, dev, staging, or prod" message is not a second copy of the set (D12).

**Tier2/3 lenny-ops loosening**: tier2/3 preset installs with non-prod env lose lenny-ops local-KMS refusal, trust-hook refusal, devRelaxed suppression, and backup confirm. Recorded as less strict, rejected as a finding in several rounds; do not re-file.

**Readiness switch recorded**: the lenny-ops /readyz-vs-/healthz switch is in the summary defects list. Do not re-file.

**lenny-ctl Idempotency-Key**: lenny-ctl sends no Idempotency-Key on upgrade/start or restore/execute; pre-existing, outside 0103.

**Runbook gaps pre-existing**: CrashLoopBackOff cause lists are not exhaustive; metrics.md lists neither audit alert; AuditRetentionLow has no runbook. Predate 0103; not findings.

**Test-infra comments**: "production=false" comments in tests/testinfra/kind/e2e-values.yaml:363 and two tier5 tests fall below the edit-site bar; a fixer may add them to the CODE-4 comment list.

**Embedded manifest grep**: an escaped "\-\-production" grep against manifests.yaml can return nothing, yet the file renders `--production=false`; CHART-1 step 5 claims are correct.

**Tier3 preset grep**: `global.deploymentTier: tier3` sits indented under `global:`; a column-0 grep misses it.

**cosign second table**: TestCheckCosignProductionEnabledSilent keeps passing unchanged; not a missed edit.

**Preflight Job wording**: the Preflight Job edge-case bullet still says "off-set value", whose defining bullet was deleted; wording only.

**token-service flags test**: the CODE-2 "existing token-service flags test that sets an environment" means clearTokenServiceEnv, which sets `LENNY_ENV` empty; judged below the bar.

**CHART-1 Targets omission**: Targets omit token-service-deployment_test.yaml and preflight-job_test.yaml, but Testing and Files touched name them; rejected as a finding.

### Open

**opsidem.Config.Production misnamed**: named for production, documented as Tier 2/3; rename left for later cleanup ([non-spec.1.fix-G1.1], [non-spec.1.fix-design-G1.1]).
**Nil kmsOpts in lenny-ops tests** (UNVERIFIED): deps_setup_test.go, events_wiring_test.go, and revocation_test.go:650 build opsFlags without kmsOpts; a path calling productionMode() would nil-deref ([non-spec.1.fix-G3.1]).

### Deferred

None open. Every DEFERRED in this window was closed by [reconcile.1] or [non-spec.1.review-docs-alignment.1].

## Ledger

### [non-spec.7.review-test-coverage.1]
FACT: the r7 diff is one hunk (CODE-3 Testing bullet 2, carrier test -> source-text wiring test). Each gate site sits in a method opening with `f := w.f` (billing_pipeline.go:31/39, stores.go:92/93, metricsbackfill.go:44/45), so the asserted `production := deployenv.IsProduction(f.kmsOpts.Environment)` statement is placeable once per file, and `production` is the first argument of every CODE-3-listed call. EVIDENCE: cmd/lenny-gateway/metricsbackfill.go:357,362; billing_pipeline.go:211,227,245; stores.go:266
FACT: the precedent exists: capability_token_ttl_test.go:42-47 reads startupgates.go and matches with whitespace collapsed. EVIDENCE: cmd/lenny-gateway/capability_token_ttl_test.go:42
USEFUL [non-spec.6.fix-G1.1]: the withdrawn carrier test's flag/env precedence rows remain covered by CODE-2 tests (LENNY_ENV=prod, --environment=staging over LENNY_ENV=prod) and CODE-1 IsProduction rows; no coverage was lost.

### [non-spec.8.fix-G1.1]
DECISION: CODE-4 tests gain a tier-1 source-text wiring test in `cmd/lenny-ops/flags_test.go` asserting `production: w.f.productionMode()` (authInputs), `Production: w.f.productionMode()` (opsserver.Options), and `buildBackupService(w.f.productionMode(),` — BECAUSE a `prod` lenny-ops refuses local KMS and the harness has no cloud KMS, so no binary test reaches these sites, and the CODE-3 gateway test is the precedent — ALTERNATIVES: also asserting `IdempotencyKeyRequired: w.f.idempotencyKeyRequired()` (already pinned by the tier-4 key-less upgrade/start assertion; adding it falsifies that bullet's "one assertion" sentence); a new test file (cascades into Targets and Files touched); asserting keyset.go checkDevTrustPosture (pinned by the tier-9 `--environment=prod` trust-hook refusal).
WATCHOUT: the authInputs `production:` site is the dev-mode authentication relaxation gate (`devRelaxed` in buildAuthConfig), so it is fail-open if fed false; do not drop it from the wiring test to match a "backup confirm" framing — EVIDENCE: cmd/lenny-ops/main.go:248-253, cmd/lenny-ops/httpsurface.go:90
FACT: the five `*w.f.production` reads in cmd/lenny-ops are keyset.go:63,66, httpsurface.go:90,177, services_wiring.go:129; gofmt aligns the struct fields, so the wiring test must collapse whitespace — EVIDENCE: cmd/lenny-ops/httpsurface.go:90,177
USEFUL [non-spec.6.fix-G1.1]: the CODE-3 wiring-test precedent (capability_token_ttl_test.go) gave the method for the lenny-ops pin directly.

### [non-spec.8.fix-design-G1.1]
DECISION: close the lenny-ops wiring gap with one bullet in Testing, CODE-4 tests: a source-text wiring test in `cmd/lenny-ops/flags_test.go` (built as the CODE-3 wiring test is) asserting httpsurface.go contains `production: w.f.productionMode()` and `Production: w.f.productionMode()`, and services_wiring.go contains `buildBackupService(w.f.productionMode(),` — BECAUSE no binary-level test can run lenny-ops in `prod` (prod refuses local KMS, harness has no cloud KMS), and those three sites are otherwise unpinned — ALTERNATIVES: a new test file (forces Targets and Files-touched edits for no gain); asserting `IdempotencyKeyRequired: w.f.idempotencyKeyRequired()` too (already pinned by the tier-4 key-less upgrade/start assertion, and including it falsifies that bullet's "the one assertion" claim); asserting keyset.go `checkDevTrustPosture(..., w.f.productionMode(), ...)` (already pinned by the tier-9 trust-hook refusal).
FACT: httpsurface.go:90 `authInputs.production` feeds `buildAuthConfig` -> `devRelaxed(devMode, production)`; fed false in prod with `--dev-mode` and no trust hook, the ops surface would serve unauthenticated (fail-open). It is a security gate, so the wiring test must cover it, not only the §25.11 confirm sites. — EVIDENCE: cmd/lenny-ops/main.go:248-253, cmd/lenny-ops/httpsurface.go:82-93
FACT: the five reads of the deleted field are keyset.go:63 (checkDevTrustPosture, tier-9 pinned), keyset.go:66 (loadOpsKeySet param deleted by CODE-4 step 3), httpsurface.go:90 and :177, services_wiring.go:129. — EVIDENCE: grep `\*w.f.production` cmd/lenny-ops/*.go
WATCHOUT: the wiring test prescribes the literal form `w.f.productionMode()` at each site; an implementor who hoists to a local (`prod := w.f.productionMode()`) fails it. CODE-4 step 2 already says "with `productionMode()`", so the direct call is the intended form.

### [non-spec.8.review-applicability.1]
FACT: The round-7 wiring test is applicable as written. Each gate site sits in a build method that opens with `f := w.f` (billing_pipeline.go:39 buildBillingPipeline; stores.go:93 buildPersistenceStores, gate at :266; metricsbackfill.go:45 buildMetricsBackfill), so the `production := deployenv.IsProduction(f.kmsOpts.Environment)` statement the test asserts compiles in each file. The source-text model in capability_token_ttl_test.go:42-47 (os.ReadFile plus a whitespace-collapse regexp) exists. EVIDENCE: cmd/lenny-gateway/capability_token_ttl_test.go:42-47
FACT: pkg/ops/opsserver/backup_test.go needs no IdempotencyKeyRequired move. TestCreateBackupConfirmGateInProduction (line 71) posts a key-less full backup under Production:true and expects 200 today, so it does not depend on the required-key posture. EVIDENCE: pkg/ops/opsserver/backup_test.go:72-75
FACT: Every docs/, charts/, and pkg/embedded file that carries a retired spelling is in a target list: install-wizard.md, the two generated alert YAMLs, ops-deployment.yaml and its test, values.yaml, the two tier presets, tier-presets_test.yaml, and manifests.yaml. The lanes spec, code, docs, and test all appear in other proposals' checklists.
USEFUL [Standing context: TEST-1 green on landing]: confirmed by grep. After S5 and S6, the only non-test "LENNY_ENV" literal left is providerflags.go:211.

### [non-spec.8.review-citations.1]
FACT: the new CODE-3 wiring test is workable as written: capability_token_ttl_test.go:42-47 reads startupgates.go and matches a whitespace-collapsed string, and each gate site sits inside a build method opening with `f := w.f` (billing_pipeline.go:39 buildBillingPipeline, stores.go:93 buildPersistenceStores, metricsbackfill.go:45 buildMetricsBackfill). EVIDENCE: cmd/lenny-gateway/billing_pipeline.go:211,227,245; stores.go:266; metricsbackfill.go:357,362
FACT: auditBatchingNoSIEM takes a string env today (auditpipeline_seams.go:127), so CODE-3's bool signature change and audit_batching_test.go rewrite are both required. EVIDENCE: cmd/lenny-gateway/audit_batching_test.go:27
USEFUL [Standing context]: the citation inventory (ops sites, CHART-1 test names, catalog/Help text, keyset.go comment for D7) re-verified in round 8 with no drift; no citation findings.

### [non-spec.8.review-client-surface.1]
FACT: backup.BackupRequest.Production is tagged `json:"-"`, so the confirm posture never reaches the lenny-ops REST body; the client-visible contract is only the `confirm` field and the `IDEMPOTENCY_KEY_REQUIRED` code, whose message ("at the current tier") already matches the tier keying — EVIDENCE: pkg/ops/backup/service.go:215, pkg/ops/opsidem/opsidem.go:191-193
FACT: rendering `--platform-tier` on lenny-ops changes GET /v1/admin/me platform.tier from "" to the chart tier; spec §25 example shows "tier": "tier2" (spec/25_agent-operability.md:1642), so this aligns with the spec and needs no docs edit (no docs page names platform.tier) — EVIDENCE: cmd/lenny-ops/flags.go:543-544
USEFUL [Standing: No client contract]: re-verified; no OpenAPI, SDK, schema, CRD, or MCP surface carries production mode.

### [non-spec.8.review-docs-alignment.1]
FACT: docs/ (outside the generated docs/alerting/rules.yaml) names no `LENNY_ENV`, `LENNY_PRODUCTION`, `--production`, `lenny_env_production`, `--platform-tier`, or `bearerTrustKeySecret`; the only `ops.production` site is install-wizard.md:176, which DOCS-1 replaces. EVIDENCE: grep over docs/ in round 8
FACT: AuditRetentionLow has no runbook page or index row in docs/runbooks (pre-existing; the rule carries no runbook annotation), so CODE-6 description edits add no runbook obligation. EVIDENCE: docs/alerting/rules.yaml:925-931, docs/runbooks/index.md:104
FACT: lenny_env_production does not appear in docs/reference; catalog.go Help change regenerates no docs page. EVIDENCE: pkg/observability/metrics/catalog.go:431
DECISION: no docs-alignment finding in round 8 — BECAUSE the only diff since r6 is the CODE-3 wiring test (test-only, no docs surface) and the whole-proposal docs sweep found every changed behavior either absent from docs/ or covered by DOCS-1 — ALTERNATIVES: token-service-outage.md "Common error classes" as a missing cause for the off-set refusal, rejected because the list is illustrative (it omits the local-KMS refusal too) and the settled "Docs off-set refusal" line already closed it

### [non-spec.8.review-edit-sites.1]
FACT: a full-tree sweep for `ops.production`, `--production`, `LENNY_PRODUCTION`, `LENNY_ENV`, `isProductionEnvironment`, `productionLikeEnvironments`, `knownEnvironments`, `auditBatchingNoSIEM`, `SetEnvProduction`, `loadOpsKeySet`, and `Options.Production` setters finds every non-comment site already named in a CODE-n, CHART-1, or DOCS-1 target list. The only unlisted hits are test-infra comments (e2e-values.yaml:363, two tier5 tests), which the Traps section already records as below the bar. EVIDENCE: charts/lenny/templates/ops-deployment.yaml:82,201,476; pkg/embedded/manifests/manifests.yaml:4773; docs/operator-guide/install-wizard.md:176
FACT: docs/ names no `--environment`, `--platform-tier`, `platform.tier`, or `lenny_env_production`. The AuditSIEMNotConfigured runbook (docs/runbooks/audit-pipeline-degraded.md) carries no production-mode condition text, and no test pins the alert description or gauge Help strings. EVIDENCE: pkg/gateway/metrics/gatewaymetrics/gatewaymetrics_alertthreshold_test.go:80-110
WATCHOUT: spec §25 "`lenny-ops` Helm Values" names `platform.tier` as the tier key, but the chart has no `platform.tier`. It carries `global.deploymentTier` (which CHART-1 renders onto lenny-ops `--platform-tier`, the same expression the preflight Job uses) and `capacityPlanning.tier`. This divergence predates 0103 and is not a 0103 edit site. Do not file it against CHART-1. EVIDENCE: spec/25_agent-operability.md:814; charts/lenny/templates/preflight-job.yaml:310; charts/lenny/values.yaml:1985,3796
USEFUL [Code readers]: the settled inventory of gateway LENNY_ENV sites matched the grep exactly (billing_pipeline.go:211,227,245; stores.go:266; metricsbackfill.go:357,362).

### [non-spec.8.review-fresh.1]
FACT: r8 diff is the single CODE-3 Testing hunk (wiring source-text test); the gate sites, `f := w.f` openers, and the capability_token_ttl_test.go:42-47 precedent all hold. EVIDENCE: cmd/lenny-gateway/billing_pipeline.go:39,211,227,245; stores.go:93,266; metricsbackfill.go:45,357,362
FACT: no test, harness, chart template, or cmd/ caller passes `--production` or sets `LENNY_ENV` other than tier9 ops_production_startup_refusal_test.go:92,124 (in CODE-4 targets) and token-service flags_test.go:154 (clears it). EVIDENCE: grep over tests/ cmd/ charts/
FACT: tier-9 trust-hook case uses --kms-provider=aws without aliases; switching `--production` to `--environment=prod` is safe because checkDevTrustPosture runs before Resolve/validateSigningReferences. EVIDENCE: cmd/lenny-ops/keyset.go:62-67; pkg/kms/providerflags/providerflags.go:86-91
WATCHOUT: CODE-5 cosign "move to the silent table" means TestCheckCosignProductionDevPasses (cosign disabled); moving rows into TestCheckCosignProductionEnabledSilent (cosign enabled) would be vacuous. Judged wording, not filed. EVIDENCE: pkg/preflight/cosign_production_test.go:16,52
USEFUL [non-spec.7.review-test-coverage.1]: its gate-site and precedent line numbers were accurate and saved re-deriving them.

### [non-spec.8.review-mechanism.1]
FACT: every gateway gate site sits directly in a build method that opens with `f := w.f` and not inside a func literal (stores.go buildPersistenceStores f:=w.f at :93, gate :266; billing_pipeline.go buildBillingPipeline :39, gates :211/:227/:245; metricsbackfill.go buildMetricsBackfill :45, sites :357/:362), so the CODE-3 statement `production := deployenv.IsProduction(f.kmsOpts.Environment)` compiles at each site and the round-7 source-text wiring test can match it. No existing `production` identifier in those files collides. — EVIDENCE: cmd/lenny-gateway/stores.go:93,266
FACT: the wiring-test precedent exists: capability_token_ttl_test.go reads startupgates.go with os.ReadFile and collapses whitespace with a regexp before strings.Contains. — EVIDENCE: cmd/lenny-gateway/capability_token_ttl_test.go:42-47
FACT: all three Bind callers pass defaults with only Provider set, so an unset --environment is "" (matches SPEC-1 unset rule); Bind's def() does no trimming, so exact-match holds end to end. — EVIDENCE: pkg/kms/providerflags/providerflags.go:196-212; cmd/lenny-gateway/flags.go:1355
USEFUL [Standing context "Platform-tier render"]: values.yaml global.deploymentTier defaults tier1 (values.yaml:1985), so the CHART-1 `| default "tier1"` and the default-render `contains --platform-tier=tier1` assertion agree.

### [non-spec.8.review-plan-conformance.1]
USEFUL [Settled: Plan scope]: re-verified in round 8. The only plan entry is gateway-runtime-comms-remediation.md:3270-3272 (F-17.9.24); BUILD-GAPS.md:40484 adds a tier-1 test per gate and a chart render test. Every deliverable is staged (SPEC-1/SPEC-2, CODE-2..5, CHART-1, CODE-3 and CHART-1 tests); the records update is a declared non-goal (summary.md:68). No findings.
FACT: the 2026-10-12 expansive-test instruction (plan:2734) binds the terminal-transition block and 0092/recycling/R20 only, not the "New proposals, each after proposal 0097" list containing the production-mode entry. EVIDENCE: gateway-runtime-comms-remediation.md:2734-2738, 3266-3272

### [non-spec.8.review-reliability.1]
USEFUL [Standing context "No reliability surface"]: confirmed again in round 8; the only r6->r8 change (CODE-3 wiring test replacing the carrier test) is a source-text test and adds no retry, lease, or redelivery path.
FACT: the prod-only fatal after integrity.Verify treats any error, including a transient Postgres error at startup, as fatal; recovery is the kubelet restart backoff, and the predicate is the pre-existing §12.3 semantics that 0103 only re-keys — EVIDENCE: cmd/lenny-gateway/stores.go:265-269

### [non-spec.8.review-security.1]
DECISION: no security findings in round 8 — BECAUSE the only change since the r6 snapshot swaps the CODE-3 carrier test for a source-text wiring test, which tightens the fail-closed pin; every gate reads the validated, chart-rendered --environment, and the §25.4 required key reads the chart-rendered --platform-tier (both trusted inputs, no pod self-report, no store-backed bound) — ALTERNATIVES: re-filing the tier2/3 non-prod lenny-ops loosening; rejected per Standing context "Tier2/3 lenny-ops loosening" (do not re-file)
USEFUL [Standing context "Tier2/3 lenny-ops loosening"]: saved re-deriving the local-KMS, trust-hook, and devRelaxed loss on tier2/3 installs with the default environment dev
FACT: the gateway gate sites all sit in methods that open with `f := w.f` (billing_pipeline.go:39, stores.go:93 for the integrity.Verify fatal at :266, metricsbackfill.go:45), so the CODE-3 wiring test's `f.kmsOpts.Environment` statement fits each file — EVIDENCE: cmd/lenny-gateway/stores.go:92-93,266

### [non-spec.8.review-single-source.1]
FACT: The only r6->r8 non-spec change is the CODE-3 wiring-test bullet; it cites "the CODE-3 call-site list" rather than re-listing calls, so it adds no copy. Each gate file opens its build method with `f := w.f` (billing_pipeline.go:39, stores.go:93, metricsbackfill.go:45), so the asserted statement is writable in every file — EVIDENCE: non-spec-changes.md:213
DECISION: no single-source findings this round — BECAUSE the remaining overlaps (summary "Watch out for" bullets vs CODE-3/4/5 constraints, CODE-1 one-clause reasons vs D3/D5, Edge-case bullets vs D4/D8) each state a constraint in one clause plus a citation, which the materiality bar refutes — ALTERNATIVES: filing the Watch-out bullets as copies of CODE-3 line 98/100 and CODE-5 step 4; rejected as one-clause summaries, same ground as the refuted Edge-case finding.

### [non-spec.8.review-test-coverage.1]
FACT: lenny-ops reads production at four sites today: services_wiring.go:129 buildBackupService(*w.f.production), httpsurface.go:90 authInputs.production, httpsurface.go:177 opsserver.Options.Production, keyset.go:63 checkDevTrustPosture. Only keyset.go:63 (tier-9 trust-hook test) and Resolve (tier-9 local-KMS test) are pinned to the flag at binary level. — EVIDENCE: cmd/lenny-ops/services_wiring.go:129, cmd/lenny-ops/httpsurface.go:90,177
WATCHOUT: no binary-level test can pin the §25.11 confirm under --environment=prod, because prod refuses local KMS and the tier-4 harness has no cloud KMS. Use the CODE-3 source-text wiring-test precedent (capability_token_ttl_test.go) for lenny-ops too. — EVIDENCE: tests/tier9_security/ops_production_startup_refusal_test.go:120-127
USEFUL [non-spec.6.fix-G1.1]: the gateway build methods do open with `f := w.f` (billing_pipeline.go:39, metricsbackfill.go:45, stores.go:93), so the round-8 wiring test's statement match is satisfiable.

### [non-spec.9.review-test-coverage.1]
FACT: The r9 diff is one added CODE-4 wiring-test bullet. All three strings it asserts match the only lenny-ops production reads outside keyset.go once whitespace is collapsed and the CODE-4 step 2 rewrite has landed. The reads are httpsurface.go:90 (`production:`), httpsurface.go:177 (`Production:`), and services_wiring.go:129 (`buildBackupService(`). The keyset.go reads at 63 and 66 are pinned at runtime by the tier-9 ops_production_startup_refusal_test rows (local KMS, and dev trust hook under --environment=prod). EVIDENCE: cmd/lenny-ops/httpsurface.go:90,177; cmd/lenny-ops/services_wiring.go:129; cmd/lenny-ops/keyset.go:63-66

### [non-spec.10.review-applicability.1]
FACT: the r10 CODE-4 wiring-test strings hold after CODE-4 step 2. Today the code reads httpsurface.go:90 `production:    *w.f.production,`, httpsurface.go:177 `Production:           *w.f.production,`, and services_wiring.go:129 `buildBackupService(*w.f.production, backupDeps{`. The precedent collapses whitespace runs to one space (capability_token_ttl_test.go:47 `space.ReplaceAllString(..., " ")`), so the rewritten lines match `production: w.f.productionMode()`, `Production: w.f.productionMode()`, and `buildBackupService(w.f.productionMode(),`. The match is case-sensitive, so the lowercase string is not a substring of the capitalized one. — EVIDENCE: cmd/lenny-ops/httpsurface.go:90,177; cmd/lenny-ops/services_wiring.go:129
FACT: the checklist S1..S10 still covers every deliverable exactly once, each step has one lane, and every Depends-on points to an earlier step. The deviations file is empty. — EVIDENCE: implementation-checklist.md

### [non-spec.10.review-citations.1]
FACT: the r10 lenny-ops wiring-test strings match the post-CODE-4 text once whitespace is collapsed: httpsurface.go:90 `production:    *w.f.production,` and :177 `Production:           *w.f.production,`, services_wiring.go:129 `buildBackupService(*w.f.production, backupDeps{`. Precedent capability_token_ttl_test.go:45-46 collapses `\s+` to one space. EVIDENCE: cmd/lenny-ops/httpsurface.go:90,177; cmd/lenny-ops/services_wiring.go:129
FACT: keyset.go:63 also reads `*w.f.production` (checkDevTrustPosture); the r10 wiring test does not cover it, and the tier-9 trust-hook refusal row covers it at the binary level instead. EVIDENCE: cmd/lenny-ops/keyset.go:63

### [non-spec.10.review-client-surface.1]
USEFUL [Standing context: No client contract]: re-verified in round 10; docs/, schemas/, sdks/ carry no `--production`, `LENNY_PRODUCTION`, or `LENNY_ENV` outside install-wizard.md:176 (DOCS-1) and the generated alert render docs/alerting/rules.yaml (CODE-6). docs/runbooks/backup-overdue.md:69 "required for a full backup in production" stays true.
FACT: the round-10 lenny-ops wiring test strings match gofmt output once whitespace is collapsed; today's sites are httpsurface.go:90 `production:    *w.f.production`, httpsurface.go:177 `Production:           *w.f.production`, services_wiring.go:129 `buildBackupService(*w.f.production,` — EVIDENCE: cmd/lenny-ops/httpsurface.go:90,177; cmd/lenny-ops/services_wiring.go:129

### [non-spec.10.review-docs-alignment.1]
DECISION: no docs-alignment finding in round 10 — BECAUSE the only diff since the r8 snapshot is a CODE-4 lenny-ops wiring test bullet (test-only, no docs surface), and a re-sweep of docs/ found every changed behavior either absent from docs/ or covered by DOCS-1 — ALTERNATIVES: agent-operability.md:289 ("Agents may send Idempotency-Key") and backup-overdue.md:66-69 (key-less full-backup curl) as stale under the tier-keyed required key, rejected because both were already inaccurate for Tier 2/3 before 0103 (ops.production fed the required key) and 0103 does not change Tier 2/3 preset behavior
FACT: the DOCS-1 replacement claim "Tier 1 and Tier 2 leave the CronJob off" holds: only values-tier3.yaml sets backups.restoreTest, values-tier1.yaml disables it. EVIDENCE: charts/lenny/presets/values-tier3.yaml:170-173, charts/lenny/presets/values-tier1.yaml:104-106
USEFUL [non-spec.8.review-docs-alignment.1]: its docs grep inventory held unchanged in round 10.

### [non-spec.10.review-edit-sites.1]

FACT: every tracked file outside proposals/ and scratchpad/ that names `--production`, `LENNY_PRODUCTION`, or `ops.production` is in a CODE-4, CHART-1, or DOCS-1 target list. The files are the ops-deployment template, values.yaml, both presets, tier-presets_test, ops-deployment_test, manifests.yaml, install-wizard.md, the lenny-ops flags/keyset/main sources and their tests, both tier-9 tests, and opsprocess/tokens.go. The embedded profile charts/lenny/presets/dev.yaml sets neither environment nor ops.production. — EVIDENCE: grep -rln over the tree; Makefile:66
FACT: the non-test `LENNY_ENV` sites are rules.go:888,1282, the three generated alert YAMLs, the gatewaymetrics comments and Help (alertthreshold.go:53,144,161; quota.go:64; gatewaymetrics.go:2007), catalog.go:431, install_prereqs.go:120, keyset.go:20, the gateway gate sites, and providerflags.go:211. CODE-2..CODE-6 cover every one. — EVIDENCE: grep LENNY_ENV over pkg cmd charts docs
FACT: no docs page, runbook, or OpenAPI text names `lenny_env_production`, `LENNY_ENV=production`, or a lenny-ops production flag. The `global.deploymentTier` comment (values.yaml:1977) and its schema description list metric-label uses only. They do not enumerate consumers, so rendering `--platform-tier` from that value does not falsify them. — EVIDENCE: charts/lenny/values.yaml:1977-1985; charts/lenny/values.schema.json:110-112
WATCHOUT: the lenny-ops Go comments that name `--production` (main.go:180,191,202,239,241; keyset.go:18-30,59; flags.go:464-465) go beyond what CODE-4 step 4 and step 5 list by name. TEST-1 does not catch comments. They are code prose outside spec/docs/charts, so they fall below the edit-site bar, but an implementor should sweep the whole file. — EVIDENCE: cmd/lenny-ops/main.go:180-241

### [non-spec.10.review-fresh.1]
FACT: the round-9 CODE-4 lenny-ops wiring-test bullet matches the tree. The only non-keyset reads of the deleted field are httpsurface.go:90 `production:    *w.f.production,`, httpsurface.go:177 `Production:           *w.f.production,`, and services_wiring.go:129 `buildBackupService(*w.f.production, backupDeps{`. After whitespace collapse they become the three asserted strings, and the match is case-sensitive, so `production:` and `Production:` are distinct. keyset.go:63 (checkDevTrustPosture) is pinned by the tier-9 binary test rather than by this wiring test. EVIDENCE: cmd/lenny-ops/httpsurface.go:90,177; cmd/lenny-ops/services_wiring.go:129
FACT: cmd/lenny-ops/deps.go:609 `Production: production` is the buildBackupService parameter (deps.go:504) on a BackupRequest. It is not an opsserver.Options setter, and CODE-4 does not need to touch it. EVIDENCE: cmd/lenny-ops/deps.go:504,609
FACT: the chart's other environment readers, lenny.gatewayLogLevel (_helpers.tpl:441) and the gateway LENNY_LOG_LEVEL env (gateway-deployment.yaml:1425), are verbosity rather than production gates, and the Kind e2e values set no environment. EVIDENCE: charts/lenny/templates/_helpers.tpl:441-447; tests/testinfra/kind/e2e-values.yaml
USEFUL [Standing context "Code readers" / full-tree sweep FACT at review-log.md:193]: re-sweeping both spellings plus `"prod"` and `"production"` literals found nothing outside the staged lists apart from the known test-infra comments.

### [non-spec.10.review-mechanism.1]
FACT: lenny-ops reads the deleted `production` field at four sites: httpsurface.go:90 (authInputs), httpsurface.go:177 (opsserver.Options), services_wiring.go:129 (buildBackupService), and keyset.go:63 (checkDevTrustPosture). The new CODE-4 wiring test covers the first three; the keyset.go site is pinned by the tier-9 trust-hook refusal test. keyset.go:66 (loadOpsKeySet) loses the parameter under CODE-4 step 3. EVIDENCE: cmd/lenny-ops/keyset.go:63-66
FACT: The gateway and lenny-ops both hold kmsOpts as `*providerflags.Options`, and each gateway build method opens with `f := w.f` (billing_pipeline.go:39, stores.go:93, metricsbackfill.go:45), so `f.kmsOpts.Environment` compiles at every CODE-3 site. EVIDENCE: cmd/lenny-gateway/flags.go:372
FACT: lenny.kmsArgs renders `--environment={{ .Values.environment }}` without a default, so an empty chart value renders an empty flag, which the finalizer accepts as non-production. EVIDENCE: charts/lenny/templates/_helpers.tpl:578
USEFUL [Settled: tier-4 tests unaffected, Required-key set]: these saved re-deriving that the tier2 harness starts POST only remediation-locks.

### [non-spec.10.review-plan-conformance.1]
USEFUL [Settled: Plan scope]: re-verified in round 10; the only plan entry is still gateway-runtime-comms-remediation.md:3270-3272 (F-17.9.24), and the only change since the r8 snapshot is one CODE-4 wiring test row (non-spec-changes.md:220), which adds no plan deliverable. No findings.

### [non-spec.10.review-reliability.1]
FACT: the only r8->r10 change is one CODE-4 wiring-test bullet; its asserted strings match the current sites once whitespace is collapsed (httpsurface.go:90 `production:    *w.f.production,`, :177 `Production:           *w.f.production,`, services_wiring.go:129 `buildBackupService(*w.f.production,`). EVIDENCE: cmd/lenny-ops/httpsurface.go:90,177; cmd/lenny-ops/services_wiring.go:129
USEFUL [Standing context "No reliability surface"]: confirmed again; every gate is a one-shot startup check, so this lens returned no findings.

### [non-spec.10.review-security.1]
- FACT: the round-10 lenny-ops wiring test covers httpsurface.go:90,177 and services_wiring.go:129; the keyset.go:63 checkDevTrustPosture production read is pinned instead by the binary-level tier-9 TestOpsProductionRefusesDevTrustHook (cloud provider, no --dev-mode needed, because checkDevTrustPosture refuses on production before checking devMode). EVIDENCE: cmd/lenny-ops/main.go:209-219, tests/tier9_security/ops_production_startup_refusal_test.go:109-134
- FACT: the chart never renders LENNY_ENV today, so the gateway's LENNY_ENV=production gates are unreachable in a chart install; rendering `environment: prod` through --environment tightens them. EVIDENCE: charts/lenny/templates/_helpers.tpl:578
- USEFUL [Settled "No security finding"]: the D8 loosening (tier2/3 with non-prod environment now renders the dev trust hook only when global.devMode is also true) re-checked; still only a relaxation gated on explicit devMode, matching the gateway. Not filed.

### [non-spec.10.review-single-source.1]
FACT: The round-9 addition (CODE-4 tests, flags_test.go wiring test) refers to the CODE-3 wiring test ("built as the CODE-3 wiring test is") and does not restate it. Its three asserted strings match today's sites with `*w.f.production` swapped for `w.f.productionMode()` — EVIDENCE: cmd/lenny-ops/httpsurface.go:90,177; cmd/lenny-ops/services_wiring.go:129
FACT: The SPEC-1 paragraph is still the only stating site of the carrier, fallback, exact match, refusal, unset rule, and staging rule. spec/ and docs/ contain no `--environment` carrier text, and the only `LENNY_ENV` hits are the ten SPEC-2 sites plus the kept §12.3 mention — EVIDENCE: lookup exact `--environment`/`LENNY_ENV` over spec+docs
DECISION: no single-source findings this round — BECAUSE every second site (watch-outs, edge cases, non-goals, the CODE-1 guardrails) cites its home or gives the reason in one clause — ALTERNATIVES: filing the spec-changes "Preflight Job" edge case against non-goal "Validating --environment inside lenny-preflight"; not filed, because each states the advisory-preflight reason in roughly one clause

### [non-spec.10.review-test-coverage.1]
FACT: lenny-ops has four reads of the deleted `production` field: httpsurface.go:90 (authInputs `production:`), httpsurface.go:177 (opsserver `Production:`), services_wiring.go:129 (`buildBackupService`), and keyset.go:63/66 (checkDevTrustPosture, loadOpsKeySet). The new CODE-4 wiring test pins the first three. The tier-9 trust-hook case pins keyset.go:63, and keyset_test plus the tier-9 local-KMS case pin keyset.go:66. — EVIDENCE: cmd/lenny-ops/httpsurface.go:90,177; cmd/lenny-ops/services_wiring.go:129; cmd/lenny-ops/keyset.go:63-66
FACT: the capability_token_ttl_test.go precedent collapses whitespace runs to a single space (`regexp \s+` -> " "), so wiring-test needles that contain spaces, such as `production: w.f.productionMode()`, match gofmt-aligned struct fields. — EVIDENCE: cmd/lenny-gateway/capability_token_ttl_test.go:46-47
DECISION: no test-coverage findings in round 10 — BECAUSE every staged behavior (Validate/finalizer deny, Resolve local refusal, gateway gate decisions and wiring, lenny-ops productionMode/idempotencyKeyRequired split and wiring, preflight/ctlcli set, chart render, carrier guard) has a named test that covers its deny or boundary path — ALTERNATIVES: filing a missing binary-level off-set refusal test for the gateway and the Token Service, rejected because the refusal lives in the shared finalizer that CODE-2 pins and that the lenny-ops tier-9 PROD case exercises end to end
