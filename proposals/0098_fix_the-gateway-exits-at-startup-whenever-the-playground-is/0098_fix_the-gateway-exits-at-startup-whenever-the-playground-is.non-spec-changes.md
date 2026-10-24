# Non-spec changes — Gateway exits at startup when the playground is enabled

## Design (implementation-facing)

CODE-1 changes the one label literal that SPEC-1 renames. The values passed to the label, the metric label validator in `pkg/observability/metrics`, and the fatal startup path in `cmd/lenny-gateway/httpsurface.go` stay unchanged (summary D2 and D4). TEST-1 boots the tier-4 replicas with the dev-mode playground and adds playground-bearer probes to the existing deletion assertions. REC-1 closes F-27.8.1 and repairs its records in one commit (summary D11).

## Staged code changes

### CODE-1 · Register the page-views counter with `auth_mode`

Target: `pkg/gateway/mcpfabric/playground/metrics.go`, function `NewMetrics`, the `lenny_playground_page_views_total` registration.

Replace

```go
	}, []string{"authMode"})
```

with

```go
	}, []string{"auth_mode"})
```

The `Help` text, the `pageView` body, and the value it passes (`string(mode)`) stay unchanged. Do not edit `cmd/lenny-gateway/httpsurface.go` or `pkg/observability/metrics`.

**IMPLEMENTOR'S CHOICE:** whether to add a `// spec: §27.8 (Metrics); §16.1.1 (Attribute Naming)` comment above the registration. Any comment added cites by section and heading, with no line number.

Land the tier-1 test under **Tests that land with CODE-1** in the same commit, written first and confirmed failing against the `authMode` literal before the replacement above is applied.

In `tests/spec-map.json`, append `pkg/gateway/mcpfabric/playground/metrics_test.go::TestNewMetricsRegistersThroughTheLabelValidator_spec_27_8` to the `tests` list of section `"27.8"`, so `lenny-test validate-maps` maps the new test.

## Staged schema, chart, and migration changes

None.

## Staged docs changes

### REC-1 · Close F-27.8.1 and bring its records current

All five edits land in one commit. The tier-11 currency test reads the BUILD-GAPS heading and the `secondHalfFilings` entry together, so splitting them leaves that test in a state neither commit intends.

1. `BUILD-GAPS.md`, finding F-27.8.1. Change the heading

   ```
   ### - [ ] F-27.8.1 — The gateway exits at startup whenever the playground is enabled, because the `authMode` label fails the metric label validator [Medium] — OPEN
   ```

   to

   ```
   ### - [x] F-27.8.1 — The gateway exits at startup whenever the playground is enabled, because the `authMode` label fails the metric label validator [Medium] — CLOSED
   ```

   Use the `— CLOSED` suffix the neighbouring closed findings use. Do not write `— RESOLVED`, which no BUILD-GAPS heading uses.

2. `BUILD-GAPS.md`, the same finding's **Deviation record (proposal 0095, 2026-10-23):** paragraph. Replace the quoted title `"Proposed: the tier-4 playground-bearer refusal case is skipped"` with `"the tier-4 playground-bearer refusal case is skipped"`. Then append, after that paragraph, a resolution note of the form

   ```
   **Resolution (proposal 0098, <implementation date>):** Proposal 0098 (`proposals/0098_fix_the-gateway-exits-at-startup-whenever-the-playground-is/`) renames the §27.8 label of `lenny_playground_page_views_total` to `auth_mode` and registers it under that name in `pkg/gateway/mcpfabric/playground/metrics.go`. `pkg/gateway/mcpfabric/playground/metrics_test.go::TestNewMetricsRegistersThroughTheLabelValidator_spec_27_8` registers the §27.8 set through the metric label validator, and `tests/tier4_integration/tenant_deletion_phase4_test.go::TestTenantDeletionPhase4ClearsArchivedTreesAndCheckpoints_spec_12_8` now starts both replicas with the dev-mode playground and refuses a playground bearer minted before Phase 3.
   ```

   The note must contain the literal `**Resolution`, which the currency test's closed-finding branch checks for.

3. `tests/tier11_docs/build_gaps_data_lifecycle_records_currency_test.go`, `secondHalfFilings`, the `F-27.8.1` entry. Change its `deviation` value from `"## Proposed: the tier-4 playground-bearer refusal case is skipped"` to `"## the tier-4 playground-bearer refusal case is skipped"`. Leave the `TrimPrefix` logic and the sibling F-10.2.22 entry unchanged. After this edit, `TrimPrefix(c.deviation, "## Proposed: ")` is a no-op for this entry, and the field is read only while the finding is open.

4. `TEST-GAPS.md`, finding T-12.8.9. Append a new annotation after the existing **Annotation (proposal 0095, 2026-10-23):** paragraph, of the form

   ```
   - **Annotation (proposal 0098, <implementation date>):** Proposal 0098 (`proposals/0098_fix_the-gateway-exits-at-startup-whenever-the-playground-is/`) closes F-27.8.1, so the playground-bearer case of `tests/tier4_integration/tenant_deletion_phase4_test.go::TestTenantDeletionPhase4ClearsArchivedTreesAndCheckpoints_spec_12_8` runs. Each replica starts with the dev-mode playground bound to `acme`, mints a playground bearer before the deletion, and refuses it during the Phase 3.5 pause and after the tombstone. The finding stays OPEN for the tier-5 CRD cleanup and the per-tenant sequence survival.
   ```

   Keep the 0095 annotation verbatim, because the tier-11 T-12.8.9 pin matches its text. Leave the T-12.8.9 heading OPEN.

5. `gateway-runtime-comms-remediation.md`, the item `**F-27.8.1: the gateway exits at startup with the playground enabled.**`. Tick its box (`- [ ]` to `- [x]`), append `(proposal 0098, implemented <implementation date>)` to the bold title's sentence in the way the P5a and P5b items record theirs, and add a nested line in their format:

   ```
     - **Outcome.** Proposal 0098 (`proposals/0098_fix_the-gateway-exits-at-startup-whenever-the-playground-is/`) renames the §27.8 page-views label to `auth_mode` in the spec and in `metrics.go`, adds a tier-1 test that registers the §27.8 set through the metric label validator, runs the tier-4 playground-bearer refusal case, and closes F-27.8.1.
   ```

   Leave the P5b item's Outcome unchanged, because the tier-11 P5b test requires it to keep mentioning `F-27.8.1`.

## Testing

### Tests that land with CODE-1

New file `pkg/gateway/mcpfabric/playground/metrics_test.go`, package `playground`, tier 1.

```go
// spec: §27.8 (Metrics); §16.1.1 (Attribute Naming)
func TestNewMetricsRegistersThroughTheLabelValidator_spec_27_8(t *testing.T) {
	reg := prometheus.NewRegistry()
	m, err := NewMetrics(reg) // must be nil; today fails with `label "authMode" is not snake_case`
	m.pageView(AuthModeDev)
	// Gather reg, find the lenny_playground_page_views_total family, and assert
	// its single metric carries exactly one label pair: name auth_mode, value dev.
}
```

- The discriminating assertion is the label name `auth_mode` on the gathered series. A nil-error assertion alone passes for any label the validator accepts and does not pin the §27.8 cell (summary D9).
- Error path: the test fails against the current `authMode` literal at `NewMetrics`. Confirm that failure before applying CODE-1.

### TEST-1 · Run the tier-4 playground-bearer refusal case

Target: `tests/tier4_integration/tenant_deletion_phase4_test.go`.

1. `deletionGatewayArgs`: append `"--playground-enabled"`, `"--playground-auth-mode=dev"`, and `"--playground-dev-tenant-id=acme"`. All three are required. Without `--playground-dev-tenant-id=acme`, `Config.Validate` refuses dev mode under `--multi-tenant=true` and the replica exits at startup with `LENNY_PLAYGROUND_DEV_TENANT_REQUIRED`, which reads like the original defect.
2. The doc comment on `deletionGatewayArgs`: replace the sentence "The playground stays disabled, because a gateway started with it enabled exits at startup (F-27.8.1)." with a sentence stating that the dev-mode playground is bound to `acme`, because multi-tenant dev mode requires an explicit non-default dev tenant.
3. In `TestTenantDeletionPhase4ClearsArchivedTreesAndCheckpoints_spec_12_8`, delete the `t.Run("playground bearer", ...)` block that calls `t.Skip`, and delete the comment sentence above it beginning "A playground bearer, minted before Phase 3 because the mint runs no registry check, needs a gateway started with the playground enabled". Replace that sentence with one stating that each replica mints a playground bearer under its own Token Service key, so each one is probed only on the replica that minted it.
4. After `probes` is built and before the first `requireBearersAdmitted` call, mint one playground bearer per replica: for each index `i` and base URL in `replicas`, POST the body `{}` with `Content-Type: application/json` to `base + "/v1/playground/token"`, require status 200, decode the `bearerToken` field, and append `bearerProbe{name: fmt.Sprintf("acme playground (replica %d)", i), bearer: tok, replicas: []string{base}}` to `probes`. The mint must run after the bootstrap seed creates `acme` and before the DELETE request.
5. Add no new assertion. The existing `requireBearersAdmitted(t, "before the deletion", ...)`, `requireBearersRefused(t, "during the Phase 3.5 pause", probes)`, and `requireBearersRefused(t, "after the tombstone", probes)` calls cover the new probes.

**IMPLEMENTOR'S CHOICE:** whether step 4 is written inline or as a helper beside `lennyIssuedProbes`. Each probe's `replicas` holds only its minting replica's base URL, and the mint runs between the bootstrap seed and the DELETE.

- Spec ties: the test keeps its existing `// spec:` annotation for §12.8, and the playground mint adds §27.3.1 (OIDC cookie-to-MCP-bearer exchange) to it.
- Spec-named failure: the probes assert the §12.8 refusal of a playground bearer minted before Phase 3, both during the Phase 3.5 pause and after the tombstone.
- Startup path: both replicas now boot the gateway binary with `--playground-enabled`, so a regression in `NewMetrics` fails the tier-4 run at `gateway.StartWith`.
- A failure other than F-27.8.1 that this case surfaces is filed as a new BUILD-GAPS finding and does not widen 0098.

### Records gate that lands with REC-1

- `TestDataLifecycleSecondHalfFilingsAreFiledOnceWithTheirOwners` (tier 11): the F-27.8.1 subtest passes through the closed-finding branch. The F-10.2.22 subtest stays red, outside this proposal.
- `TestDataLifecycleSecondHalfTestGapRecords` (tier 11): the T-12.8.9 subtest still passes, because the 0095 annotation is unchanged and the finding stays OPEN.
- `TestRemediationPlanRecordsTheDataLifecycleSecondHalf` (tier 11): still passes, because the P5b Outcome is unchanged.

## Edge cases and accepted failure modes

- Cross-replica playground bearers are not probed. Each playground bearer verifies only on its minting replica until F-10.2.22 is fixed (summary D6).
- The tier-11 currency test stays red on the F-10.2.22 subtest after REC-1 lands (summary D11).

## Files touched on application (non-spec)

- pkg/gateway/mcpfabric/playground/metrics.go
- pkg/gateway/mcpfabric/playground/metrics_test.go (new)
- tests/spec-map.json
- tests/tier4_integration/tenant_deletion_phase4_test.go
- tests/tier11_docs/build_gaps_data_lifecycle_records_currency_test.go
- BUILD-GAPS.md
- TEST-GAPS.md
- gateway-runtime-comms-remediation.md
