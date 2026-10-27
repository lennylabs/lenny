# Deviations — 0103_fix_production-mode-has-two-spellings-so-no-single-setting

The implementor owns this file. It stays empty until an implementation records a departure from what the proposal states.

## Proposed: Extra tier-9 cases for the retired spelling and an empty KMS provider

**Status:** proposed

**What the proposal says.** The CODE-4 tests section of the testing plan states that the tier-9 test adds a case where lenny-ops started with `--environment=PROD` exits with the `--environment` validation error.

**What landed instead.** `TestOpsRefusesOffSetEnvironment_spec_17_9_1` in `tests/tier9_security/ops_production_startup_refusal_test.go` checks both `PROD` and the retired `production` spelling. `TestOpsProductionRefusesLocalKMSProvider` adds an explicit `--kms-provider=` (empty) case next to the unset and `local` cases.

**Why.** Step S6 (CODE-4 and CHART-1) reported that the extra cases pin the D4 fail-closed refusal of the retired spelling and the refusal of an empty provider. Every case the proposal asks for is present.

**What a later reader would otherwise get wrong.** A reader comparing the test file to the proposal would find cases the proposal does not list. The `production` and empty `--kms-provider` cases are additions from step S6 and do not replace any case the proposal names.

## Proposed: Extra chart cases for staging, PROD, and the tier presets

**Status:** proposed

**What the proposal says.** The CHART-1 tests section states that `ops-deployment_test.yaml` rewrites the existing `ops.production` cases to set `environment`.

**What landed instead.** `charts/lenny/tests/ops-deployment_test.yaml` also adds two new cases, in which `environment: staging` and `environment: PROD` each render the `/readyz` readiness path. `tier-presets_test.yaml` adds a tier3 case that checks for `--platform-tier=tier3` and the absence of `--production=true`, and it checks that the tier2 preset renders `/readyz`.

**Why.** Step S6 reported that these cases pin D5 (exact match) and D8 (staging and the capacity tier are not production mode) in the chart. They add cases and change none of the cases the proposal names.

**What a later reader would otherwise get wrong.** A reader comparing the chart tests to the proposal would find staging, `PROD`, tier3, and tier2 `/readyz` cases the proposal does not list, and could read them as an unreviewed change to the CHART-1 test plan. They are additions from step S6.
