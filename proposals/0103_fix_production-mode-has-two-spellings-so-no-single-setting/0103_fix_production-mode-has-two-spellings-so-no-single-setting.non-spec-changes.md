# Non-spec changes — Production mode has two spellings, so no single setting turns on every production gate

## Design (implementation-facing)

The rule these changes implement is the SPEC-1 **Production mode.** paragraph in §17.9.1. The code has one statement of it:

- `pkg/deployenv` (CODE-1) holds the closed set and the predicate. It imports only the standard library. Every reader of production mode calls `deployenv.IsProduction`, and every validator calls `deployenv.Validate`.
- `providerflags.Bind` (CODE-2) is the one place the carrier is parsed. It binds `--environment` with `LENNY_ENV` as its default and validates the value in the finalizer the gateway, the Token Service, and lenny-ops call before wiring.
- Each binary reads the finalized `kmsOpts.Environment` through `deployenv.IsProduction`. The gateway (CODE-3) and lenny-ops (CODE-4) never read `LENNY_ENV` or any other environment variable for production mode.
- The chart (CHART-1) has one production value, `environment`, which `lenny.kmsArgs` and the preflight Job already render.

Every staged sketch below locates code by symbol and file. The line numbers in the problem statement are evidence and drift as other proposals land.

## Staged code changes

### CODE-1 · pkg/deployenv: the closed set and the predicate

Targets: `pkg/deployenv/deployenv.go` (new), `pkg/deployenv/deployenv_test.go` (new), and `tests/change-graph.json`, which gains a `"pkg/deployenv"` glob key with `"unit": ["pkg/deployenv/...", "pkg/kms/providerflags/...", "pkg/preflight/...", "pkg/ctlcli/...", "cmd/lenny-gateway/...", "cmd/lenny-ops/..."]` so `lenny-test validate-maps` covers the new directory.

```go
// Package deployenv states the §17.9.1 Environment dimension and the
// production-mode predicate that every Lenny binary and the preflight read.
// It imports only the standard library so that pkg/preflight, and through it
// lenny-ctl and lenny-controller, can use it without linking cloud KMS SDKs.
//
// spec: §17.9.1 (Composition Dimensions) "Production mode"
package deployenv

const (
	Local   = "local"
	Dev     = "dev"
	Staging = "staging"
	Prod    = "prod"
)

// Validate returns nil for "" and for an exact member of the closed set.
func Validate(v string) error // otherwise:
// fmt.Errorf("environment %q is not one of local, dev, staging, or prod (§17.9.1)", v)

// IsProduction reports whether v is production mode.
func IsProduction(v string) bool { return v == Prod }
```

The comparison is exact. Do not add `strings.EqualFold` or `strings.TrimSpace`: the chart conditionals in CHART-1 compare `eq .Values.environment "prod"` exactly, and a folding binary would disagree with the rendered chart on `PROD`.

Do not name the package `pkg/environment`; that path belongs to the §10.6 Environment resource. Do not put the predicate in `pkg/kms/providerflags` and import it from `pkg/preflight`: providerflags links cloud KMS SDK packages that lenny-ctl and lenny-controller do not link today.

Tests: see Testing, CODE-1.

### CODE-2 · providerflags validates the carrier and uses the predicate

Targets: `pkg/kms/providerflags/providerflags.go` (the finalizer closure `Bind` returns, `Resolve`, the `--environment` help text, the `Options.Environment` doc comment), `pkg/kms/providerflags/providerflags_test.go`.

1. In the closure `Bind` returns, before `ParseAliases`, add:
   ```go
   if err := deployenv.Validate(opts.Environment); err != nil {
       return fmt.Errorf("--environment: %w", err)
   }
   ```
   This is the fail-closed refusal SPEC-1 states. It runs in the gateway, the Token Service, and lenny-ops because each calls the finalizer before wiring.
2. In `Resolve`, replace `strings.EqualFold(opts.Environment, "prod")` with `deployenv.IsProduction(opts.Environment)`. The local refusal still covers an empty provider, which `Resolve` maps to local first.
3. `--environment` help: `Deployment environment (local | dev | staging | prod); prod is production mode (§17.9.1) and refuses --kms-provider=local. Override via LENNY_ENV.`
4. The `Options.Environment` doc comment cites §17.9.1 "Production mode" and names `deployenv.IsProduction` as the only reader.

The literal `"LENNY_ENV"` in `Bind` stays. It is the one site TEST-1 permits.

Tests: see Testing, CODE-2.

### CODE-3 · Gateway gates read the parsed carrier through pure decision functions

Targets: `cmd/lenny-gateway/production_gates.go` (new), `cmd/lenny-gateway/production_gates_test.go` (new), `cmd/lenny-gateway/billing_pipeline.go`, `cmd/lenny-gateway/stores.go`, `cmd/lenny-gateway/metricsbackfill.go`, `cmd/lenny-gateway/auditpipeline_seams.go`, `cmd/lenny-gateway/audit_batching_test.go`.

`production_gates.go`, annotated `// spec: §17.9.1 (Composition Dimensions) "Production mode"; §11.7 (Audit Logging); §12.3 (Postgres HA Requirements)`:

```go
func siemGateFatal(production bool, err error) bool {
	return production && err != nil && err.Error() == admin.SIEMStartupFatalMessage
}
func pgauditGateFatal(production bool, err error) bool {
	return production && err != nil && err.Error() == admin.PgauditStartupFatalMessage
}
func pgauditPreflightFatal(production, regulatedPresent bool) bool {
	return production && regulatedPresent
}
func auditIntegrityFatal(production bool, err error) bool {
	return production && err != nil
}
```

In `auditpipeline_seams.go`, change the signature to `func auditBatchingNoSIEM(production, batchingEnabled, siemConfigured bool) bool` and return `production && batchingEnabled && !siemConfigured`.

Each build step computes `production := deployenv.IsProduction(f.kmsOpts.Environment)` and calls the function in place of its `os.Getenv("LENNY_ENV") == "production"` comparison:

- `billing_pipeline.go`: the §11.7 SIEM refusal after `admin.ValidateSIEMForRegulatedTenants`, the pgaudit refusal after `admin.ValidatePgauditForRegulatedTenants`, and the fatal after `pgaudit.Preflight`.
- `stores.go`: the fatal after `integrity.Verify`.
- `metricsbackfill.go`: `gwMetrics.SetEnvProduction(production)` and the `auditBatchingNoSIEM(production, ...)` call.

The warn branches, the "could not list tenants" branches, and every log message stay as they are. The SIEM and pgaudit refusals keep their two-level structure: a non-matching error still takes the "could not list tenants" warning, so the fatal check uses `siemGateFatal` and `pgauditGateFatal` while the message comparison that selects the non-production warning stays. Remove `os` imports that become unused.

The gateway calls `f.kmsFinalize()` in `main` before the build steps run, so `f.kmsOpts.Environment` is validated when each gate reads it. Do not move a gate ahead of the finalizer.

Tests: see Testing, CODE-3.

### CODE-4 · lenny-ops derives production from `--environment`

Targets: `cmd/lenny-ops/flags.go`, `cmd/lenny-ops/keyset.go`, `cmd/lenny-ops/main.go`, `cmd/lenny-ops/httpsurface.go`, `cmd/lenny-ops/services_wiring.go`, `cmd/lenny-ops/flags_test.go`, `cmd/lenny-ops/keyset_test.go`, `cmd/lenny-ops/auth_config_test.go` (comments), `cmd/lenny-ops/devmode_gate_test.go` (comments), `cmd/lenny-ops/revocation_test.go` (comments), `pkg/ops/opsserver/opsserver.go`, `pkg/ops/opsserver/idempotency_test.go`, `tests/tier2_component/opsidem/outage_test.go`, `tests/tier3_contract/ops_endpoints/upgrade_test.go`, `tests/tier3_contract/ops_endpoints/upgrade_idempotency_test.go`, `tests/tier4_integration/ops_service_e2e_test.go`, `tests/tier9_security/ops_production_startup_refusal_test.go`, `tests/tier9_security/ops_dev_mode_gate_test.go` (comments), `tests/testinfra/opsprocess/tokens.go` (comment).

1. `flags.go`: delete the `production` field of `opsFlags` and the `--production` flag with its `LENNY_PRODUCTION` default. Add:
   ```go
   // productionMode reports §17.9.1 production mode from the finalized
   // --environment value. Call it only after kmsFinalize has returned nil.
   func (f *opsFlags) productionMode() bool { return deployenv.IsProduction(f.kmsOpts.Environment) }

   // idempotencyKeyRequired reports the §25.4 Tier 2/3 required-key posture from --platform-tier.
   func (f *opsFlags) idempotencyKeyRequired() bool { t := *f.platformTier; return t == "tier2" || t == "tier3" }
   ```
2. Replace every `*w.f.production` and every other read of the deleted field (in `httpsurface.go`, `services_wiring.go`, and `keyset.go`) with `productionMode()`. The §25.4 required-key posture is the exception, because §25.4 keys it on Tier 2/3 rather than production mode. `opsserver.Options` gains `IdempotencyKeyRequired bool`, which `httpsurface.go` sets to `w.f.idempotencyKeyRequired()`. `opsserver.New` passes that field to `opsidem.Config.Production` in place of `opts.Production`, and its comment and the `Options.Production` doc name only the §25.11 full-backup confirm. `buildBackupService`, `checkDevTrustPosture`, `devRelaxed`, and the `httpsurface` fields keep their `production bool` parameters and their behavior.
3. `keyset.go`: delete `errLocalProviderInProduction`, `resolvesToLocal`, and the `production` parameter of `loadOpsKeySet`, which becomes `loadOpsKeySet(ctx, opts, resolve)`. The local refusal now comes from `providerflags.Resolve` as `providerflags.ErrLocalForbidden`. Rewrite the file comment so it names `--environment=prod` and drops both `--production` and the `LENNY_ENV=prod` spelling.
4. `main.go`: in `errDevTrustInProduction`, replace "when --production is set" with "in production mode (--environment=prod)".
5. `flags.go` help text: in `--dev-mode`, replace "Ignored under --production" with "Ignored in production mode (--environment=prod)". In `--bearer-trust-hmac-key-file`, replace each `--production` reference with "production mode (--environment=prod)". In `--platform-tier`, add that tier2 and tier3 also select the §25.4 required `Idempotency-Key`.
6. `flags_test.go` and `keyset_test.go` drop their uses of the deleted field. The tier-9 refusal tests start lenny-ops with `--environment=prod` in place of `--production`. Comments that name `--production` in `auth_config_test.go`, `devmode_gate_test.go`, `revocation_test.go`, `ops_dev_mode_gate_test.go`, and `opsprocess/tokens.go` name production mode (`--environment=prod`); the `production bool` cases and assertions in the first three are unchanged. Tests that set `opsserver.Options.Production` to obtain the required-key posture (the `idemBackupServer` helper in `pkg/ops/opsserver/idempotency_test.go`, the tier-2 `opsidem/outage_test.go` helper, and the tier-3 `upgrade_test.go` and `upgrade_idempotency_test.go` servers) set `IdempotencyKeyRequired` instead.

CODE-4 lands in the same commit as CHART-1. If the binary drops `--production` while the chart or the embedded manifest still renders it, lenny-ops exits at flag parsing.

Tests: see Testing, CODE-4.

### CODE-5 · Preflight and ctlcli use the shared set

Targets: `pkg/preflight/install_prereqs.go`, `pkg/preflight/cosign_production.go`, `pkg/preflight/install_prereqs_test.go`, `pkg/preflight/cosign_production_test.go`, `cmd/lenny-preflight/main.go`, `pkg/ctlcli/install.go`.

1. `install_prereqs.go`: delete `isProductionEnvironment`. `SIEMEndpointCheck.Decide` calls `deployenv.IsProduction(c.Environment)`. The `SIEMEndpointCheck.Environment` comment cites §17.9.1 "Production mode" and drops the both-spellings note.
2. `cosign_production.go`: delete `productionLikeEnvironments` and the lowercasing of the input. The check warns when `deployenv.IsProduction(env) || env == deployenv.Staging`. The comment cites §5.3 "production or staging" and §17.9.1. Remove the `strings` import if it becomes unused.
3. `cmd/lenny-preflight/main.go`: the `--environment` help reads `(local | dev | staging | prod)`. lenny-preflight does not call `deployenv.Validate`.
4. `pkg/ctlcli/install.go`: delete `knownEnvironments`. In `validateAnswers`, replace `!knownEnvironments[a.Environment]` with `a.Environment == "" || deployenv.Validate(a.Environment) != nil`, and keep the error text `environment %q is not one of local, dev, staging, or prod`. The explicit empty check is required: `deployenv.Validate("")` returns nil, and `validateAnswers` rejects an empty Environment today.

Tests: see Testing, CODE-5.

### CODE-6 · Non-spec restatements of the retired spelling

Targets: `pkg/alerting/rules/rules.go` (the `AuditSIEMNotConfigured` and `AuditRetentionLow` descriptions), `docs/alerting/rules.yaml` and `charts/lenny/files/alerting-rules.yaml` (regenerated), `pkg/gateway/metrics/gatewaymetrics/gatewaymetrics_alertthreshold.go` (the comments on the env-production gauge and its `Help`), `pkg/gateway/metrics/gatewaymetrics/gatewaymetrics.go` and `gatewaymetrics_quota.go` (comments naming `LENNY_ENV=production`), `pkg/observability/metrics/catalog.go` (the `lenny_env_production` entry).

1. `AuditSIEMNotConfigured` description begins "The deployment runs in production mode (environment prod) and audit.siem.endpoint is not set." and keeps the rest of its text.
2. `AuditRetentionLow` description begins "The deployment runs in production mode (environment prod) and audit.retentionDays is below 365 days without a configured SIEM." and keeps the rest of its text.
3. The gauge `Help` and the catalog entry both read: `1 when the deployment runs in production mode (--environment=prod, §17.9.1), 0 otherwise; gates the production-only audit alerts (§16.5).`
4. Code comments that name `LENNY_ENV=production` name production mode (`--environment=prod`, §17.9.1).

The metric name `lenny_env_production` and both alert expressions do not change. Regenerate `docs/alerting/rules.yaml` and `charts/lenny/files/alerting-rules.yaml` with `cmd/gen-alerting-rules`, and refresh the embedded manifest copy with `make generate`. Do not hand-edit generated YAML.

Tests: see Testing, CODE-6.

## Staged schema, chart, and migration changes

### CHART-1 · Chart renders one production signal

Targets: `charts/lenny/templates/ops-deployment.yaml`, `charts/lenny/values.yaml`, `charts/lenny/presets/values-tier2.yaml`, `charts/lenny/presets/values-tier3.yaml`, `pkg/chart/values/values.go`, `charts/lenny/values.schema.json` (regenerated), `charts/lenny/tests/ops-deployment_test.yaml`, `charts/lenny/tests/tier-presets_test.yaml`, `charts/lenny/tests/gateway-deployment_test.yaml`, `pkg/embedded/manifests/manifests.yaml` (regenerated).

1. `ops-deployment.yaml`:
   - The `$devTrust` assignment becomes `{{- $devTrust := and .Values.security.oidc.bearerTrustKeySecret .Values.global.devMode (ne .Values.environment "prod") -}}`, and its comment says the hook renders only when `global.devMode` is true and `environment` is not `prod`.
   - Delete the `--production={{ .Values.ops.production }}` argument.
   - Render `- --platform-tier={{ .Values.global.deploymentTier | default "tier1" }}`, the preflight Job's expression, so lenny-ops reads the §25.4 tier posture (CODE-4 step 2).
   - The readiness probe switch becomes `{{- if eq .Values.environment "prod" }}` for `/healthz`, else `/readyz`.
   - Every comment in the file that names `ops.production` or `--production` names `environment: prod` (production mode) instead.
   The comparisons are exact `eq`/`ne` against `"prod"`, matching `deployenv.IsProduction`.
2. `values.yaml`:
   - Delete `ops.production` and its comment block.
   - The `environment` comment states that `prod` is production mode, that it controls every production gate in every Lenny binary and the lenny-ops full-backup confirmation and readiness behavior, and that `staging` is not production mode.
   - The `security.oidc.bearerTrustKeySecret` comment says lenny-ops refuses the hook in production mode (`environment: prod`) and that the chart renders it on lenny-ops only when `global.devMode` is true and `environment` is not `prod`.
3. `values-tier2.yaml` and `values-tier3.yaml`: delete `production: true` under `ops:`, the two-line "Tier N is a production posture" comment above it, and the header line that lists `ops.production`. The `ops:` block keeps its `replicas` and `selfHealth` keys.
4. `pkg/chart/values/values.go`: the `Environment` description lists `local, dev, staging, or prod` and states that `prod` is production mode. Regenerate `charts/lenny/values.schema.json` with the schema generator. Add no `enum`.
5. Run `make generate` in the same commit as CODE-4, so that `pkg/embedded/manifests/manifests.yaml` drops `--production=false` and the comments that name `ops.production`.

Chart tests: see Testing, CHART-1.

## Staged docs changes

### DOCS-1 · Install wizard preset text

Target: `docs/operator-guide/install-wizard.md`, the paragraph under the preset table that begins "Tier 2 and Tier 3 set `ops.production: true`".

Replace the whole paragraph, through "Tier 1 leaves both off.", with:

```
In the `prod` environment, lenny-ops requires confirmation for full backups,
whichever preset is layered. Tier 3 enables the periodic test-restore CronJob,
which restores the latest full backup on the first day of each month. Tier 1
and Tier 2 leave the CronJob off.
```

## Testing

Every Go test below carries `// spec: 17.9.1 (Composition Dimensions)` plus the section of the gate it pins. Tier-2-and-higher tests carry a `// diagnosis:` comment.

### CODE-1 tests (tier 1)

`pkg/deployenv/deployenv_test.go`, one table:
- Accepted by `Validate`: `local`, `dev`, `staging`, `prod`, and `""`.
- Refused by `Validate`: `production`, `Prod`, `PROD`, ` prod`, `prod `, `stage`, and `qa`. Each error names the value.
- `IsProduction` is true for `prod` only. The `PROD` and ` prod` rows are the ones that discriminate the exact-match rule.

### CODE-2 tests (tier 1)

`pkg/kms/providerflags/providerflags_test.go`, each on a fresh `FlagSet` with an injected `env` function:
- The finalizer rejects `LENNY_ENV=production` with no flag, and rejects `--environment=PROD`. The error wraps the `deployenv.Validate` error and names `--environment`.
- The finalizer accepts an unset value and each member.
- `LENNY_ENV=prod` with no flag yields `Environment == "prod"`. `--environment=staging` with `LENNY_ENV=prod` yields `staging`.
- `Resolve` returns `ErrLocalForbidden` for provider `local` and for an empty provider under `prod`, and admits `local` under `staging`, `dev`, and unset.
- The existing token-service flags test that sets an environment keeps passing unchanged.

### CODE-3 tests (tier 1)

`cmd/lenny-gateway/production_gates_test.go`:
- One table per decision function over production true and false crossed with the condition present and absent. `siemGateFatal` and `pgauditGateFatal` include a row with a non-matching error under production, which must return false; that row discriminates the message match. `pgauditPreflightFatal` includes production true with `regulatedPresent` false. `auditIntegrityFatal` includes a nil error under production.
- A wiring test, following the source-text check in `capability_token_ttl_test.go`, reads `billing_pipeline.go`, `stores.go`, and `metricsbackfill.go` with whitespace collapsed. It asserts that each file contains the CODE-3 `production := deployenv.IsProduction(f.kmsOpts.Environment)` statement and passes `production` as the first argument of each call the CODE-3 call-site list names for that file. This is the assertion that fails when a call site drops the gate or feeds it another value; the decision-function tables cannot detect that.

`cmd/lenny-gateway/audit_batching_test.go`: the rows take the bool signature and cover each of the eight combinations.

### CODE-4 tests (tier 1, tier 4, tier 9)

- `flags_test.go`: no flag named `production` is registered; `--environment=prod` yields `productionMode()` true; `--environment=staging` and unset yield false. `--platform-tier=tier2 --environment=dev` yields `idempotencyKeyRequired()` true, `--platform-tier=tier1 --environment=prod` yields false, and an unset value yields false.
- `flags_test.go`: a wiring test, built as the CODE-3 wiring test is, reads `httpsurface.go` and `services_wiring.go` with whitespace collapsed and asserts that they contain `production: w.f.productionMode()`, `Production: w.f.productionMode()`, and `buildBackupService(w.f.productionMode(),`, which fails when either site is fed another value.
- `pkg/ops/opsserver/idempotency_test.go`: `Production: true, IdempotencyKeyRequired: false` admits a full backup with `confirm:true` and no key, and `Production: false, IdempotencyKeyRequired: true` rejects it with 400 `IDEMPOTENCY_KEY_REQUIRED`. These two rows discriminate the split.
- Tier 4, `TestOpsServiceRemediationLockPersistenceE2E` in `tests/tier4_integration/ops_service_e2e_test.go`, which starts lenny-ops with `--platform-tier=tier2` and no `--environment`: a key-less `POST /v1/admin/platform/upgrade/start` answers 400 `IDEMPOTENCY_KEY_REQUIRED`. This is the one assertion that fails if `httpsurface.go` sets `IdempotencyKeyRequired` from `productionMode()`. Extend the test's `// spec:` and `// diagnosis:` comments to cover it.
- `keyset_test.go`: `errors.Is(err, providerflags.ErrLocalForbidden)` for provider `local` and for an empty provider under `prod`; `staging` admits `local`.
- `auth_config_test.go`, `devmode_gate_test.go`, `revocation_test.go`: comments only, per CODE-4 step 6.
- Tier 9, `tests/tier9_security/ops_production_startup_refusal_test.go`: lenny-ops started with `--environment=prod` and `--kms-provider=local` exits with the `ErrLocalForbidden` text; started with `--environment=prod`, `--dev-mode`, and the trust hook it exits with the new `errDevTrustInProduction` text; started with `--environment=PROD` it exits with the `--environment` validation error.

### CODE-5 tests (tier 1)

- `cosign_production_test.go`: the warn table is `{"prod", "staging"}`. `production`, `stage`, `PROD`, `Prod`, ` prod`, and ` Staging ` move to the silent table. Change the existing rows rather than adding beside them.
- `install_prereqs_test.go`: the assertion that the `production` spelling warns becomes an assertion that `production` and `PROD` with no endpoint are silent. `prod` with no endpoint warns, and `staging` with no endpoint is silent.
- `pkg/ctlcli` install tests: next to the existing `qa` row, a `validateAnswers` call with Environment `""` is rejected, and `production` is rejected.

### CODE-6 tests (tier 0, tier 11)

- The generator drift check for the alerting rules passes after regeneration.
- The tier-11 alert-to-runbook resolution passes for both alerts.

### CHART-1 tests (tier 0 chart unit tests, tier 5)

- `tier-presets_test.yaml`: the tier2 case that asserts `--production=true` asserts `notContains` `--production=true` and `contains` `--platform-tier=tier2`, and its title names the platform tier.
- `gateway-deployment_test.yaml` and `ops-deployment_test.yaml`: with `environment: prod`, the container args contain `--environment=prod`, and the container env carries neither `LENNY_ENV` nor `LENNY_PRODUCTION`. `token-service-deployment_test.yaml` and `preflight-job_test.yaml` already pin `--environment=prod`; add the same env assertions there.
- `ops-deployment_test.yaml`: rewrite the cases that set `ops.production` to set `environment`. `environment: prod` renders the `/healthz` readiness path and no dev trust hook even with `global.devMode: true`; `environment: dev` with `global.devMode: true` renders `/readyz` and the hook. Keep the §17.4 case that `environment: prod` without `global.devMode` renders no `--dev-mode`. The default-render case `renders no --dev-mode on lenny-ops by default` replaces `contains --production=false` with `notContains --production=false` and `contains --platform-tier=tier1`, and its comment drops `ops.production`.
- Tier 5: the Kind install with the regenerated chart brings lenny-ops to Ready, which fails if any render still passes `--production`.

### DOCS-1 tests (tier 11)

The documentation tier passes on `docs/operator-guide/install-wizard.md`.

### TEST-1 · Source guard against a second production carrier (tier 0)

Target: `tests/tier0_static/production_mode_carrier_test.go` (new), a Tier 0 static gate in package `tier0_static`, following `tests/tier0_static/jwt_retired_signer_symbols_test.go`.

- Find the repository root with `schematest.RepoRoot`.
- Walk the non-`_test.go` `.go` files under `pkg/` and `cmd/`, skipping `testdata` directories.
- Parse each file with `go/parser`. Fail on any `*ast.BasicLit` string whose unquoted value is exactly `"LENNY_ENV"` in a file outside `pkg/kms/providerflags/`, or exactly `"LENNY_PRODUCTION"` in any file. Report each offending `file:line`.
- The match is exact, so `LENNY_ENV_VAR_BLOCKLIST` is not flagged.
- The doc comment states that a failure is fixed by reading `deployenv.IsProduction` on the finalized `--environment` value, and never by adding an exemption.
- Annotation: `// spec: 17.9.1 (Composition Dimensions) "Production mode"`.
- A self-check case runs the scanner over an in-memory source containing `os.Getenv("LENNY_ENV")` and asserts one finding, so the guard cannot pass vacuously.

## Edge cases and accepted failure modes

- **A leftover `ops.production` key.** The `ops` values object is untyped, so a stale key is ignored and renders nothing. No chart `fail` guard is added.
- **A hand-set `LENNY_PRODUCTION`.** Nothing reads it after CODE-4, and TEST-1 fails on any new read.
- **A staging install that set `ops.production` or layered a tier2 or tier3 preset.** See summary D8.
- **Tests and harnesses that start binaries without `--environment`.** The finalizer accepts the unset value and every gate reads non-production.

## Files touched on application (non-spec)

- pkg/deployenv/deployenv.go (new), pkg/deployenv/deployenv_test.go (new), tests/change-graph.json
- pkg/kms/providerflags/providerflags.go, providerflags_test.go
- cmd/lenny-gateway/production_gates.go (new), production_gates_test.go (new), billing_pipeline.go, stores.go, metricsbackfill.go, auditpipeline_seams.go, audit_batching_test.go
- cmd/lenny-ops/flags.go, keyset.go, main.go, httpsurface.go, services_wiring.go, flags_test.go, keyset_test.go, auth_config_test.go, devmode_gate_test.go, revocation_test.go
- pkg/ops/opsserver/opsserver.go, idempotency_test.go; tests/tier2_component/opsidem/outage_test.go; tests/tier3_contract/ops_endpoints/upgrade_test.go, upgrade_idempotency_test.go; tests/tier4_integration/ops_service_e2e_test.go
- tests/tier9_security/ops_production_startup_refusal_test.go, ops_dev_mode_gate_test.go
- tests/testinfra/opsprocess/tokens.go
- pkg/preflight/install_prereqs.go, cosign_production.go, install_prereqs_test.go, cosign_production_test.go
- cmd/lenny-preflight/main.go
- pkg/ctlcli/install.go and its install tests
- pkg/alerting/rules/rules.go, docs/alerting/rules.yaml (generated), charts/lenny/files/alerting-rules.yaml (generated)
- pkg/gateway/metrics/gatewaymetrics/gatewaymetrics_alertthreshold.go, gatewaymetrics.go, gatewaymetrics_quota.go
- pkg/observability/metrics/catalog.go
- charts/lenny/templates/ops-deployment.yaml, charts/lenny/values.yaml, charts/lenny/presets/values-tier2.yaml, values-tier3.yaml
- pkg/chart/values/values.go, charts/lenny/values.schema.json (generated)
- charts/lenny/tests/ops-deployment_test.yaml, tier-presets_test.yaml, gateway-deployment_test.yaml, token-service-deployment_test.yaml, preflight-job_test.yaml
- pkg/embedded/manifests/manifests.yaml (generated)
- docs/operator-guide/install-wizard.md
- tests/tier0_static/production_mode_carrier_test.go (new)
