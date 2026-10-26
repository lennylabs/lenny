# Problem: Production mode has two spellings, so no single setting turns on every production gate

## Statement

Production mode has two spellings and more than one carrier, so no single setting turns on every production gate (BUILD-GAPS F-17.9.24). The specification's §17.9.1 Composition Dimensions table gives the Environment dimension as `local`, `dev`, `staging`, or `prod`. The chart's `lenny.kmsArgs` helper renders `--environment={{ .Values.environment }}` on the gateway, the Token Service, and lenny-ops, and the preflight Job template renders `--environment={{ .Values.environment | default "dev" }}` separately. `providerflags` binds `--environment` with its default taken from `LENNY_ENV`, and refuses the local KMS provider only when the value case-insensitively equals `prod`.

The specification spells production mode `LENNY_ENV=production` at these sites:

- §11.7 Audit Logging: the startup grant verification (refuse to start on unexpected UPDATE or DELETE grants on the audit tables), the regulated-tenant-without-SIEM startup refusal, and the regulated-tenant production-mode refusal.
- §12.3 Postgres HA Requirements: the T2 audit-batching-without-SIEM startup warning.
- §16.4 Logging: the `AuditRetentionLow` startup warning.
- §16.5: the `AuditSIEMNotConfigured` and `AuditRetentionLow` alert conditions.
- §17.6 Packaging and Installation: the etcd Secret encryption, node disk encryption, and SIEM endpoint preflight warnings.

The gateway compares `os.Getenv("LENNY_ENV")` to the literal `production` for these gates:

- the §11.7 regulated-tenant SIEM refusal, the regulated-tenant pgaudit refusal, and the pgaudit preflight refusal (`cmd/lenny-gateway/billing_pipeline.go`);
- the F-12.3.5 audit-ledger integrity fatal (`integrity.Verify` in `cmd/lenny-gateway/stores.go`);
- the `lenny_env_production` gauge that gates the §16.5 `AuditSIEMNotConfigured` and `AuditRetentionLow` alerts (`cmd/lenny-gateway/metricsbackfill.go`);
- the §12.3 `AuditBatchingNoSIEM` warning (`cmd/lenny-gateway/metricsbackfill.go` and `auditBatchingNoSIEM` in `cmd/lenny-gateway/auditpipeline_seams.go`).

No chart template sets `LENNY_ENV`. On a chart install with `environment: prod`, the KMS gate fires and none of the gateway gates above fire. Setting `LENNY_ENV=production` by hand without the flag turns the gateway gates on and leaves the KMS gate off, because `production` does not equal `prod`. Setting both values together turns both families on, so the defect is the absence of a single setting rather than an impossible configuration.

lenny-ops carries a further, independent production signal: `--production` (environment variable `LENNY_PRODUCTION`, chart value `ops.production`, default `false`). It makes a full backup require `confirm:true`, refuses the local KMS provider and the dev trust hook, and suppresses the dev-mode relaxations. lenny-ops also receives `--environment` through `lenny.kmsArgs`, so one install can render `environment: prod` together with `ops.production: false`. The comment in `cmd/lenny-ops/keyset.go` uses a third spelling, `LENNY_ENV=prod`.

Two sites lie outside the code defect. The cloud-managed pooler defense in `stores.go` fires regardless of `LENNY_ENV`, so it is not a gate to fix. The §17.6 preflight code already accepts both spellings: `isProductionEnvironment` treats `prod` and `production` as production, and the cosign check also accepts `staging` and `stage`. For §17.6 only the specification wording is inconsistent, and the dual-spelling acceptance is a workaround the fix absorbs or retires.

The specification contradicts itself on the spelling, so the fix goes through a proposal. The proposal chooses one spelling and one carrier for production mode and rewrites every specification site to it. §17.9.1 already defines `prod` carried by `--environment` with `LENNY_ENV` as its fallback, and the proposal either adopts that or states why it does not. Every env-gated check in every binary reads the chosen carrier, and the gateway already holds the parsed `--environment` value in its KMS options. The chart renders the carrier once. The proposal states how lenny-ops' `--production` relates to the carrier, either by deriving it from the chosen value or by stating the two as independent, without redesigning the lenny-ops-specific behaviors. The non-spec carriers that restate `LENNY_ENV=production` are updated with it: the alert descriptions in `pkg/alerting/rules/rules.go`, `docs/alerting/rules.yaml`, and `charts/lenny/files/alerting-rules.yaml`, the gauge help text, and the metric catalog entry. The proposal adds a tier-1 test per gate and a chart test for the render.

## Evidence

- (verified) `spec/17_deployment-topology.md` §17.9.1 Composition Dimensions: Environment is `local` | `dev` | `staging` | `prod` (line 1408 at validation time).
- (verified) `spec/11_policy-and-controls.md` §11.7 Audit Logging: `LENNY_ENV=production` at the startup grant verification (line 366), the regulated-tenant-without-SIEM fatal (line 465), and the regulated-tenant production-mode refusal (line 483).
- (verified) `spec/12_storage-architecture.md` §12.3 Postgres HA Requirements: "In `LENNY_ENV=production` mode without SIEM" batching warning (line 99). Line 79 mentions `LENNY_ENV` without a production spelling and is not a site.
- (verified) `spec/16_observability.md` §16.4 Logging `AuditRetentionLow` startup warning (line 409); §16.5 `AuditSIEMNotConfigured` (line 478) and `AuditRetentionLow` (line 520).
- (verified) `spec/17_deployment-topology.md` §17.6 preflight checks table: etcd Secret encryption, node disk encryption, and SIEM endpoint warnings keyed on `LENNY_ENV=production` (lines 552, 553, and 558).
- (verified) `charts/lenny/templates/_helpers.tpl` `lenny.kmsArgs` renders `--environment={{ .Values.environment }}` (line 578); it is included by the gateway, Token Service, and lenny-ops Deployments (`ops-deployment.yaml` line 205). `charts/lenny/templates/preflight-job.yaml` line 298 renders `--environment` for the preflight Job. `charts/lenny/values.yaml` sets `environment: "dev"`. Under `charts/`, `LENNY_ENV` appears only in alert description strings in `charts/lenny/files/alerting-rules.yaml`.
- (verified) `pkg/kms/providerflags/providerflags.go`: `--environment` defaults from `LENNY_ENV` (line 211); the local provider is refused only when `strings.EqualFold(opts.Environment, "prod")` (line 86).
- (verified) `cmd/lenny-gateway/billing_pipeline.go` lines 211, 227, and 245; `cmd/lenny-gateway/stores.go` line 266; `cmd/lenny-gateway/metricsbackfill.go` lines 357 and 362; `cmd/lenny-gateway/auditpipeline_seams.go` line 128. Each compares the environment value to `"production"`.
- (verified) `cmd/lenny-gateway/stores.go` lines 143 to 153: the cloud-managed pooler defense comment states "The fatal fires regardless of LENNY_ENV".
- (verified) `pkg/preflight/install_prereqs.go` lines 118 to 127: `isProductionEnvironment` accepts `prod` and `production`, with the comment "§17.6 keys on LENNY_ENV=production; the chart `environment` value uses `prod`, so both spellings match". `pkg/preflight/cosign_production.go` lines 13 to 19 accept `prod`, `production`, `staging`, and `stage`.
- (verified) `cmd/lenny-ops/flags.go` line 269: `--production` from `LENNY_PRODUCTION`, default false. `charts/lenny/templates/ops-deployment.yaml` line 201 renders `--production={{ .Values.ops.production }}`. `cmd/lenny-ops/keyset.go` lines 16 to 20 state the gap and use the spelling `LENNY_ENV=prod`.
- (verified) Non-spec restatements of the `production` spelling: `pkg/alerting/rules/rules.go` lines 888 and 1282; `docs/alerting/rules.yaml` lines 584 and 930; `pkg/observability/metrics/catalog.go` line 431; `pkg/gateway/metrics/gatewaymetrics/gatewaymetrics_alertthreshold.go` lines 53 and 161.
- (lead, audit record) BUILD-GAPS F-17.9.24 is an OPEN High entry. The identifier does not appear in `spec/`, `pkg/`, `cmd/`, or `charts/`.

## Who observes it

Every operator who installs a production deployment with the chart observes it, because the chart's production value is `prod` and no template sets `LENNY_ENV`. The operator sees no error: the gateway gates that §11.7, §12.3, §16.4, and §16.5 require in production run their non-production branch, which logs a warning or does nothing. Compliance reviewers of a regulated-tenant deployment observe it as a missing refusal or a missing alert. An operator who works around it by setting `LENNY_ENV=production` by hand observes the KMS local-provider refusal staying off unless `--environment=prod` is also set. lenny-ops operators observe a separate production posture that follows `ops.production` rather than `environment`.

## What breaks if nothing changes

A regulated-tenant deployment (`soc2`, `fedramp`, or `hipaa`) installed with `environment: prod` starts without a SIEM or pgaudit sink and logs a warning, where §11.7 requires the gateway to refuse to start. It also starts with unexpected UPDATE or DELETE grants on the audit ledgers and logs a warning, where §11.7 requires a refusal. The `lenny_env_production` gauge stays 0, so the production-only `AuditSIEMNotConfigured` and `AuditRetentionLow` alerts never fire, and the §12.3 batching-without-SIEM warning is not logged. These are silent compliance-posture failures reachable by default. lenny-ops on the same install keeps its non-production backup and dev-relaxation behavior unless `ops.production` is also set. The specification keeps two contradictory spellings, so each new production gate picks one and widens the split.

## Findings this unblocks

- BUILD-GAPS F-17.9.24 (production-mode spelling), filed from proposal 0097's list of unstaged pre-existing defects.
- The "Production-mode spelling proposal (not yet written)" entry queued after 0097 in `gateway-runtime-comms-remediation.md`.

## Prior art considered

- No proposal, open or landed, stages this change. Apart from 0103, only the implemented proposal 0097 mentions `LENNY_ENV`, and its summary records the drift as a pre-existing defect it left unstaged ("The drift predates this change and spans every production gate."). The Draft and Approved proposals 0084, 0085, 0086, 0096, 0100, 0101, and 0102 do not touch it.
- BUILD-GAPS F-17.9.24 is OPEN and unclaimed. The TEST-GAPS entries that mention production mode assume `LENNY_ENV=production` and do not fix the spelling.
- Partial prior art exists in code. The preflight package (`isProductionEnvironment`, introduced with F-17.6.1 in b9a85116d) accepts both spellings, and the cosign preflight accepts a wider set. This is a template for one canonical predicate, and it reconciles only the preflight. It does not reach the gateway gates.
- BUILD-GAPS F-17.4.35, fixed in 9fe24b9bd, moved lenny-ops' security relaxations onto `--dev-mode` / `LENNY_DEV_MODE` and kept `--production` for the §25.11 backup gate, the local-KMS refusal, and the trust-hook refusal. That narrows the lenny-ops question and does not unify production mode.
- No specification mechanism resolves the spelling. §17.9.1 lists `prod`, and the other sections listed under Statement still say `LENNY_ENV=production`.

## Validated premises

### Premise lens (verdict: revise)

- Holds: §17.9.1 gives `local | dev | staging | prod`, while §11.7, §12.3, §16.4, §16.5, and §17.6 spell production mode `LENNY_ENV=production`. The original statement omitted the §12.3 and §16.4 sites; its "(and any other site)" clause covered them. The proposal depends on this premise.
- Holds: the chart renders `--environment` through `lenny.kmsArgs`, default `dev`, and no template sets `LENNY_ENV`. The helper also reaches lenny-ops, and the preflight Job receives `--environment` separately, so lenny-ops receives both `--environment` and `--production`.
- Holds: `providerflags` defaults `--environment` from `LENNY_ENV` and refuses local KMS only on `prod`; `LENNY_ENV=production` alone leaves the KMS gate off. The proposal depends on this premise.
- Holds: the gateway SIEM and pgaudit fatals, the audit-integrity fatal, the `lenny_env_production` gauge, and the `AuditBatchingNoSIEM` warning compare to `production` and stay off on `environment: prod`. The gateway already holds the parsed `--environment` value. The proposal depends on this premise.
- Refuted detail: the original statement named "billing and pooler production gates". The pooler defense fires regardless of `LENNY_ENV`; the env-gated check in `stores.go` is the audit-integrity verification, and the `billing_pipeline.go` gates are §11.7 SIEM and pgaudit refusals. Verified against `stores.go` lines 143 to 153 and 255 to 270. The Statement now names them correctly.
- Refuted detail: the original statement implied the §17.6 preflight warnings fail on a chart install. The preflight Job reads `--environment` and accepts both spellings, so those warnings fire on `environment: prod`. For §17.6 only the specification wording is defective.
- Holds: lenny-ops' `--production` is an independent production signal, confirmed by its own `keyset.go` comment.
- Holds: the alert descriptions, gauge help text, and metric catalog entry restate the `production` spelling and are sites to update.

### Evidence lens (verdict: revise)

- Verified: §17.9.1 Environment values; the `lenny.kmsArgs` render on the gateway, the Token Service, and lenny-ops (the original list of two binaries omitted lenny-ops); `values.yaml` default `dev`; `LENNY_ENV` absent from chart templates.
- Verified: the `providerflags` default and `prod` comparison.
- Verified: §11.7 uses `LENNY_ENV=production` for the SIEM refusal and the startup grant verification.
- Verified: §16.5 alert conditions, plus §16.4, §12.3, and the rule descriptions as unnamed sites.
- Verified in spec, differs in code: §17.6 preflight is wording-only, because the preflight accepts both spellings.
- Verified: the gateway comparisons in `metricsbackfill.go`, `stores.go`, `billing_pipeline.go`, and `auditpipeline_seams.go`.
- Partly false: the "billing and pooler" naming in the original statement. The pooler gate is unconditional. This agrees with the premise lens and was confirmed in the tree.
- Verified: lenny-ops `--production` (`LENNY_PRODUCTION`, `ops.production`, default false) and the third spelling `LENNY_ENV=prod` in `keyset.go`.
- Unverifiable as cited: F-17.9.24 exists only in the BUILD-GAPS audit record, which is a lead rather than evidence.

### Prior-art lens (verdict: stands)

- No proposal stages the change; 0097 records the drift as unstaged.
- F-17.9.24 is open and unclaimed, and the remediation plan queues the proposal as not yet written.
- The preflight package already accepts both spellings, so the §17.6 warnings fire on a chart install; the proposal absorbs or retires that workaround.
- The core premise stands for the gateway. This lens listed the pooler gate among the affected gates; the premise, evidence, and scope lenses refute that, and `stores.go` confirms the pooler defense is unconditional.
- lenny-ops receives both `--environment` and `--production`.
- F-17.4.35 (9fe24b9bd) already narrowed lenny-ops' `--production` without unifying production mode.
- No specification mechanism resolves the spelling; the generated alert descriptions and the gauge help text are further sites.

### Scope lens (verdict: revise)

- One problem: every site consumes the deployment's production posture, and choosing the spelling and carrier is a single decision that every other edit waits on. Cutting the work into spec, gateway, and chart pieces would leave each blocked on the same decision.
- lenny-ops takes two independent chart values, so `environment: prod` leaves lenny-ops non-production unless `ops.production` is set. This lens recommends folding `ops.production` into the chosen carrier or deriving it from that value, rather than only stating the relation.
- The original site list was incomplete: §11.7 grant verification, §12.3 batching warning, §16.4 retention wording, the pgaudit gates, and the dual-spelling workarounds in the preflight and cosign checks.
- The "billing and pooler" naming in the original statement was wrong; the pooler check is out of scope because it is already unconditional.
- A cut that defers lenny-ops' `--production` to a follow-up is possible but not recommended, because it would leave a second production signal in the chart. The lens recommends settling it as one derivation rule without redesigning lenny-ops' specific behaviors.

### Impact lens (verdict: stands)

- Every chart-based production install reaches the defect. The proposal depends on this premise.
- The affected gateway gates are wider than the original statement listed: the SIEM refusal, the pgaudit refusal, the pgaudit preflight refusal, the audit-ledger integrity refusal, the `lenny_env_production` gauge, and the `AuditBatchingNoSIEM` warning. The proposal depends on this premise.
- The impact is silent compliance-posture failure on regulated-tenant deployments with no operator-visible error. The proposal depends on this premise.
- The KMS gate uses the other spelling; two values set together turn both families on, so the defect is the lack of a single setting.
- The specification contradicts itself, so the fix needs a specification change.
- Mitigating detail: the §17.6 preflight already works on a chart install; lenny-ops' `--production` is a third signal.

### Alternatives lens (verdict: stands)

- No reading removes the need for a change: the specification contradicts itself and the gateway gates never fire on a chart install. The proposal depends on this premise.
- A smaller intervention exists: keep §17.9.1's `prod` on `--environment` (with `LENNY_ENV` as fallback) as the single signal, rewrite the `LENNY_ENV=production` sites, and have the gateway gates read the parsed `providerflags` `Options.Environment`. The chart already renders that flag on the gateway, and a chart test asserting `--environment=dev` exists. The Statement records this as the default the proposal adopts or argues against.
- The site list should include the non-spec carriers. This lens cited the batching site as §12.4; the tree places it in §12.3 Postgres HA Requirements, and line 79 of that file is not a production-spelling site.
- On lenny-ops, this lens judges that stating the relation (derived or independent) is the right size and that a full merge widens scope. This disagrees with the scope lens, which prefers folding `ops.production` in. The Statement requires the proposal to state the relation and leaves the choice between derivation and independence to the proposal.
