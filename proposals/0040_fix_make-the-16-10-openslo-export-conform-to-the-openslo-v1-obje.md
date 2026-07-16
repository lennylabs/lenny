# Proposal: Make the §16.10 OpenSLO export conform to the OpenSLO v1 object model: one condition per AlertPolicy, a required notificationTargets reference to a conventional AlertNotificationTarget, and alertPolicyRef object entries

- **Status:** Verified (2026-07-16). Converged after 3 adversarial review rounds (1 findings fixed); awaiting sign-off.
- **Date:** 2026-07-16.
- **Scope:** A code-only reconciliation of the §16.10 OpenSLO export (`pkg/alerting/rules/openslo.go`, `RenderOpenSLO`) to the OpenSLO v1 object model the export already advertises with `apiVersion: openslo/v1`. It splits each SLO's two-condition AlertPolicy into two single-condition AlertPolicy documents, populates each AlertPolicy's required `notificationTargets` with a conventional `targetRef`, and renders each SLO `alertPolicies` entry as an `{alertPolicyRef: <name>}` object rather than a bare string. It touches `pkg/alerting/rules/openslo.go` and `pkg/alerting/rules/openslo_test.go`, regenerates the two code-generated artifacts (`charts/lenny/files/openslo.yaml`, `docs/alerting/openslo.yaml`), adds one documentation line to the `charts/lenny/values.yaml` `monitoring.openslo` comment, and un-skips the tier-0 conformance test (`tests/tier0_static/openslo_export_test.go`). The change adds no spec normative content, no Helm configuration key, no CLI flag, and no new OpenSLO document kind. It runs entirely at chart-generation and test time and carries no runtime or operator-hardware dependency.

This document stages the proposed code and test changes. It does not modify any spec, code, or doc file. Apply the changes in the "Proposed changes" section after sign-off.

## 1. Problem

The §16.10 OpenSLO export advertises OpenSLO v1 documents (`apiVersion: openslo/v1`, `pkg/alerting/rules/openslo.go:15`) but emits documents that violate the OpenSLO v1 object model in three ways, so a conformant tool (Sloth, Nobl9, OpenSLO Reliably) rejects or misinterprets them. The vendored OpenSLO v1 schema (`tests/testdata/openslo/schema/openslo-v1.schema.json`) transcribes the OpenSLO README object model and is cross-checked against the official OpenSLO Go SDK `govy` validators (`SliceLength(1,1)` on conditions, `SliceMinLength(1)` on notificationTargets), which Sloth's parser shares (`tests/testdata/openslo/README.md`). §16.10 (`spec/16_observability.md:742-746`) states only that the chart renders OpenSLO v1 documents into a ConfigMap of SLO, SLI, and AlertPolicy objects; it defines none of the three object-model requirements and no notification-target configuration surface.

### (A) An AlertPolicy carries two conditions against the one-condition cap

Each SLO renders one AlertPolicy whose `spec.conditions` packs two inline AlertCondition entries: the fast-burn critical condition (`openslo.go:189-203`) and the slow-burn warning condition (`openslo.go:204-218`), typed by the `Conditions []openSLOConditionEntry` field (`openslo.go:90`). OpenSLO v1 caps `spec.conditions` at exactly one entry (`openslo-v1.schema.json:186-187`, `minItems` 1 and `maxItems` 1), so a conformant validator rejects a two-condition AlertPolicy.

### (B) The AlertPolicy omits the required notificationTargets

`alertPolicySpec` (`openslo.go:87-91`) has no `notificationTargets` field and `RenderOpenSLO` never populates one. OpenSLO v1 requires `spec.notificationTargets` to be present and non-empty (`openslo-v1.schema.json:177`, `notificationTargets` in `AlertPolicySpec.required`; `:193`, `minItems` 1), so a conformant validator rejects an AlertPolicy that omits it. A repo-wide search for `notificationTarget` finds no producer anywhere in the tree.

### (C) The SLO alertPolicies entries are bare strings against the required object entries

`sloSpec.AlertPolicies` is `[]string` (`openslo.go:72`), set to `[]string{policyName}` (`openslo.go:178`), so the SLO emits bare-string `alertPolicies` entries. OpenSLO v1 requires each entry to be an object that either references an AlertPolicy (`{alertPolicyRef: <name>}`) or inlines one; `SLOAlertPolicyEntry` is `type: object` with an `anyOf` over the `alertPolicyRef` and inline branches (`openslo-v1.schema.json:79-88`). A bare string is neither branch, so a conformant validator rejects it.

### The tier-0 conformance test is written and skipped

A tier-0 conformance test that validates the generated chart fragment against the vendored schema already exists (`tests/tier0_static/openslo_export_test.go`, `TestOpenSLOChartFragmentMatchesSpecification`) and is `t.Skip`-ped (`:62-67`). Its skip message enumerates exactly these three non-conformances and states that populating a real `notificationTargets` was thought to need new deployer-facing configuration the package lacks, so the fix awaited a decision. This proposal makes that test run and pass.

## 2. Decisions

- **Code-only reconciliation; no spec edit.** §16.10 already advertises `apiVersion: openslo/v1`, so making the export satisfy the OpenSLO v1 object model reconciles the code to a contract the spec already states rather than adding a normative requirement. The three object-model rules (one condition per AlertPolicy, a non-empty `notificationTargets`, object `alertPolicies` entries) are properties of the OpenSLO v1 model the vendored schema transcribes rather than new §16.10 behavior. A conventional `targetRef` is a plain cross-reference name that introduces no configuration surface. §16.10 therefore stays unedited and the change carries no spec normative content.
- **Classified fix.** The dominant action reconciles an existing export to the object model it already claims and un-skips a test written to catch the gap. There is no additive subsystem.
- **Preserve the multi-window burn-rate by splitting, rather than dropping a window.** OpenSLO v1 caps an AlertPolicy at one condition, so each SLO's fast-burn critical and slow-burn warning conditions render as two separate AlertPolicy documents (`<name>-burnrate-fast`, `<name>-burnrate-slow`), each carrying exactly one condition. Dropping the slow-burn warning to fit one policy would diverge the export from the §16.5 multi-window alerts it is a view of, so the export keeps both windows across two policies. The document kind set stays SLI, SLO, and AlertPolicy (two AlertPolicy documents per SLO), so the tier-0 test's kind-routing map needs no new entry.
- **Represent the notification target as a fixed conventional targetRef.** Each AlertPolicy renders `notificationTargets: [{targetRef: "lenny-slo-notifications"}]`, where the value is a package constant naming an OpenSLO AlertNotificationTarget the deployer defines in their own tool (where the destination type, channel, and credentials belong). This satisfies the schema's `AlertNotificationTargetEntry` `targetRef` branch (`openslo-v1.schema.json:163-172`), which requires only a non-empty string with no pattern or format constraint, and it needs no top-level AlertNotificationTarget document, so neither a new subschema in the vendored schema nor a new entry in the test's kind-routing map is required.
- **No deployer configuration, no install-time placeholder, no fail-closed gate, no CLI flag.** A `targetRef` is an opaque cross-reference name the deployer's OpenSLO tool resolves; it has no effect on the rendered PromQL or on which series the alerts evaluate. Conformance therefore does not require deployer configuration: `RenderOpenSLO` emits the fixed name directly. This differs from the `__DEPLOYMENT_TIER__` placeholder, which the Helm template substitutes because the tier is spliced into the query text and label selectors (`openslo.go:247`) and so changes which time series the alerts read; the notification-target name changes nothing about the rendered documents' semantics, so reusing the tier-substitution mechanism for it would add surface by analogy. An undefined `targetRef` already surfaces as an error when the deployer's OpenSLO tool ingests the ConfigMap, so a conventional constant does not route alerts to nowhere silently; it moves the "define this target" feedback from Helm-install time to tool-ingestion time. The spec states no per-deployment notification-routing requirement, so no config key, `values.schema.json` edit, `required` fail-closed gate, or `--notification-target` flag is added.
- **Reuse the existing render and generation surfaces.** The change splits an existing document, changes two existing field types, and adds one field and one constant to `pkg/alerting/rules/openslo.go`. It introduces no new top-level OpenSLO document kind, no new schema subschema, no new RPC, and no new generated file. `RenderOpenSLO`'s signature is unchanged, so the `gen-alerting-rules` callers and the `lenny-ctl slo export` call site need no edit.

## 3. The conformant document set

Per §16.5 SLO, the export renders four documents rather than three:

- One `SLI` document, unchanged.
- One `SLO` document whose `spec.alertPolicies` carries two `{alertPolicyRef: <name>}` objects, one referencing each burn-rate AlertPolicy.
- One `AlertPolicy` document named `<name>-burnrate-fast`, carrying exactly one condition (the fast-window 1h/14x critical condition) and `notificationTargets: [{targetRef: "lenny-slo-notifications"}]`.
- One `AlertPolicy` document named `<name>-burnrate-slow`, carrying exactly one condition (the slow-window 6h/3x warning condition) and the same single `notificationTargets` entry.

The two AlertPolicy documents preserve the §16.5 multi-window burn-rate the single AlertPolicy previously packed into two conditions. Each of the four documents validates against its per-kind subschema (`SLIDocument`, `SLODocument`, `AlertPolicyDocument`), so the tier-0 conformance test passes with the kind-routing map unchanged.

## 4. Proposed changes

### CODE-1. Render one condition per AlertPolicy, alertPolicyRef object entries, and a conventional targetRef notificationTargets on each policy

**Target:** `pkg/alerting/rules/openslo.go` (the `sloSpec.AlertPolicies` field `:72`; the `alertPolicySpec` struct `:87-91`; the `RenderOpenSLO` per-SLO loop `:136-223`; the `RenderOpenSLO` doc comment `:113-127`).

**Change (types and constant).** Change `sloSpec.AlertPolicies` from `[]string` to a slice of a new reference type, add a `NotificationTargets` field to `alertPolicySpec`, add the target-ref type, and add the conventional-name constant:

```go
// openSLOAlertPolicyRef is one entry in an SLO's alertPolicies list. The
// OpenSLO v1 object model requires each entry to be an object that either
// references an AlertPolicy by name or inlines one; a bare string is
// neither. spec: §16.10 (OpenSLO v1 object model).
type openSLOAlertPolicyRef struct {
	AlertPolicyRef string `yaml:"alertPolicyRef"`
}

// openSLONotificationTargetRef is one entry in an AlertPolicy's
// notificationTargets list. It references, by name, an OpenSLO
// AlertNotificationTarget the deployer defines in their own tool.
type openSLONotificationTargetRef struct {
	TargetRef string `yaml:"targetRef"`
}

// openSLONotificationTargetName is the conventional name every exported
// AlertPolicy references in its (OpenSLO-required) notificationTargets.
// The deployer defines an OpenSLO AlertNotificationTarget of this name in
// their own tooling, where the destination type, channel, and credentials
// live. The value is an opaque cross-reference: it does not change the
// rendered queries or the series the alerts evaluate.
const openSLONotificationTargetName = "lenny-slo-notifications"
```

`sloSpec.AlertPolicies` (`:72`) becomes `AlertPolicies []openSLOAlertPolicyRef` (keep the `yaml:"alertPolicies"` tag). `alertPolicySpec` (`:87-91`) gains `NotificationTargets []openSLONotificationTargetRef \`yaml:"notificationTargets"\`` after `Conditions`.

**Change (per-SLO loop).** In the `RenderOpenSLO` loop, name the two policies and set the SLO's `AlertPolicies` to the two reference objects:

```go
fastPolicyName := d.Name + "-burnrate-fast"
slowPolicyName := d.Name + "-burnrate-slow"
```

Set `sloSpec.AlertPolicies` (currently `[]string{policyName}` at `:178`) to:

```go
AlertPolicies: []openSLOAlertPolicyRef{
	{AlertPolicyRef: fastPolicyName},
	{AlertPolicyRef: slowPolicyName},
},
```

Replace the single AlertPolicy document (`:181-221`) with two documents, each carrying one condition and one notification target. The fast document:

```go
openSLODoc{
	APIVersion: openSLOAPIVersion,
	Kind:       "AlertPolicy",
	Metadata:   openSLOMeta{Name: fastPolicyName, Labels: labels},
	Spec: alertPolicySpec{
		Description:        "Fast-window error-budget burn-rate policy for " + d.Objective,
		AlertWhenBreaching: true,
		Conditions: []openSLOConditionEntry{{
			Kind:     "AlertCondition",
			Metadata: openSLOMeta{Name: d.Name + "-fast-burn"},
			Spec: openSLOConditionSpec{
				Description: "Fast-window (1h) error-budget burn rate exceeds the page threshold.",
				Severity:    string(SeverityCritical),
				Condition: openSLOBurnRateCond{
					Kind:           "burnrate",
					Op:             "gt",
					Threshold:      burnRateFastMultiplier,
					LookbackWindow: prometheusDuration(burnRateFastWindow),
					AlertAfter:     prometheusDuration(burnRateFastWindow),
				},
			},
		}},
		NotificationTargets: []openSLONotificationTargetRef{{TargetRef: openSLONotificationTargetName}},
	},
},
```

The slow document is symmetric: `Name: slowPolicyName`, condition metadata name `d.Name + "-slow-burn"`, `Severity: string(SeverityWarning)`, `Threshold: burnRateSlowMultiplier`, `LookbackWindow`/`AlertAfter: prometheusDuration(burnRateSlowWindow)`, and the same single `NotificationTargets` entry. The condition metadata names (`<name>-fast-burn`, `<name>-slow-burn`), thresholds, windows, and severities are unchanged from the current inline conditions; only the packaging into two policies changes. The `policyName := d.Name + "-burnrate"` local (`:142`) is removed since it is no longer referenced.

Both split policy metadata names stay within the schema's 63-character, lowercase-hyphen metadata-name limit (`openslo-v1.schema.json:9-13`): the longest §16.5 SLO name is `session-creation-success-rate` (29 characters), so `session-creation-success-rate-burnrate-fast` (43 characters) is valid.

**Change (doc comment).** Update the `RenderOpenSLO` doc comment (`:113-127`) so it states that each SLO renders an SLI document, an SLO document, and two burn-rate AlertPolicy documents (a fast-window critical policy and a slow-window warning policy), each AlertPolicy carrying one condition and a `notificationTargets` entry referencing the conventional AlertNotificationTarget.

**Rationale:** This removes all three structural non-conformances (A at `:90,:188-219`, B at `:87-91`, C at `:72,:178`) against the vendored schema, while preserving the §16.5 multi-window burn-rate and the canonical metric references. The signature is unchanged, so no caller is touched.

### CODE-2. Regenerate the chart fragment and docs export, and note the target convention in the values comment

**Target:** `charts/lenny/files/openslo.yaml` (generated), `docs/alerting/openslo.yaml` (generated), `charts/lenny/values.yaml` (the `monitoring.openslo` comment `:1022`).

**Change.** Run `make generate` to regenerate `charts/lenny/files/openslo.yaml` and `docs/alerting/openslo.yaml` from the amended `RenderOpenSLO`. Do not hand-edit the generated files; the `gen-alerting-rules` callers (`renderOpenSLOFragment`, `renderDocsOpenSLO`) and the `openSLOChartHeader`/`openSLODocsHeader` are unchanged because `RenderOpenSLO`'s signature and the `__DEPLOYMENT_TIER__` placeholder are unchanged. Add one line to the `openslo` comment in `charts/lenny/values.yaml` (`:1022`, currently carrying only `enabled`/`namespace`/`name`) documenting the convention: each exported AlertPolicy references an OpenSLO AlertNotificationTarget named `lenny-slo-notifications`, which the deployer defines in their own OpenSLO tool (where the destination and credentials live). No `notificationTarget` value key is added.

**Rationale:** The generated OpenSLO artifacts must reflect the conformant structure so the tier-0 test validates the generated output, and the deployer needs to know which AlertNotificationTarget name the exported policies reference. The values comment is documentation and adds no configuration surface.

### TEST-1. Un-skip the tier-0 OpenSLO conformance test

**Target:** `tests/tier0_static/openslo_export_test.go` (`TestOpenSLOChartFragmentMatchesSpecification`, the `t.Skip(...)` call `:62-67`).

**Change.** Remove the `t.Skip(...)` call so the test validates each document in the regenerated `charts/lenny/files/openslo.yaml` against its per-kind subschema. The kind-routing map (`:75-81`) stays SLI, SLO, and AlertPolicy because no new top-level document kind is introduced. The regenerated fragment must pass: two AlertPolicy documents per SLO, each with exactly one condition and a non-empty `targetRef` `notificationTargets`, and the SLO `alertPolicies` rendered as `alertPolicyRef` objects.

**Rationale:** The test was written to validate the generated chart fragment against the vendored OpenSLO v1 schema and is skipped pending this decision; the fix must make it run and pass.

### TEST-2. Update the openslo package unit tests to the conformant structure

**Target:** `pkg/alerting/rules/openslo_test.go` (the `parsedDoc` decode struct `:15-42`; `TestRenderOpenSLOEmitsThreeDocsPerSLO` `:64-89`; `TestRenderOpenSLOObjectivesMatchCatalog` `:95-133`; `TestRenderOpenSLOBurnRateMatchesAlerts` `:140-162`) and `tests/spec-map.json` (the `"16.10"` section's `tests` list `:2627-2632`).

**Change.** Update `parsedDoc.Spec` (`:22-41`): change `AlertPolicies []string` (`:25`) to a struct slice decoding `alertPolicyRef`, and add a `NotificationTargets` field decoding `targetRef`. Rename `TestRenderOpenSLOEmitsThreeDocsPerSLO` to `TestRenderOpenSLOEmitsFourDocsPerSLO` to reflect four documents per SLO, assert `len(docs) == len(defs)*4`, and assert two AlertPolicy documents per SLO. Update the `"16.10"` section of `tests/spec-map.json` (`:2631`) so its `pkg/alerting/rules/openslo_test.go::TestRenderOpenSLOEmitsThreeDocsPerSLO` entry becomes `pkg/alerting/rules/openslo_test.go::TestRenderOpenSLOEmitsFourDocsPerSLO`, matching the renamed function. The spec-map validator strips the `::TestName` selector before stat-ing the file (`cmd/lenny-test/cmd_validate.go:507-517`), so it does not catch a stale function name; leaving the old name would point the §16.10 coverage map at a function that no longer exists. Update `TestRenderOpenSLOObjectivesMatchCatalog`'s referential check (`:127-131`) to read `ap.AlertPolicyRef` against the rendered names. Update `TestRenderOpenSLOBurnRateMatchesAlerts` to expect one condition per policy: assert the `<name>-burnrate-fast` policy carries the critical 14x/1h condition and the `<name>-burnrate-slow` policy the warning 3x/6h condition across the two documents. Add coverage asserting every AlertPolicy carries a present, non-empty `notificationTargets` whose single entry's `targetRef` equals `lenny-slo-notifications`. The `RenderOpenSLO` call sites are unchanged because the signature is unchanged. Carry the `// spec:` annotations.

**Rationale:** The existing unit tests assert the non-conformant structure (three documents per SLO, two conditions per AlertPolicy, `alertPolicies` as `[]string`) and would fail against the fix; they must encode the new structure and pin the notificationTargets and alertPolicyRef object structures.

## 5. Non-goals

- **No spec edit and no notification-target configuration surface (dropped SPEC-1).** An earlier draft added a §16.10 normative section defining a deployer-facing notification-target configuration key and stating the three object-model requirements. A conventional `targetRef` is a plain cross-reference name that needs no configuration surface and no normative addition, so the spec stays unedited and the export is reconciled to the object model §16.10 already advertises. This alternative is dropped as higher surface than the conformance problem requires.
- **No Helm configuration key and no fail-closed gate (dropped CODE-3).** An earlier draft added a `monitoring.openslo.notificationTarget` key to `values.yaml`, a `monitoring` edit to `values.schema.json`, and a `required`/`replace` substitution with a fail-closed gate in `openslo-configmap.yaml`. Because the `targetRef` is opaque and per-deployment routing is not a §16.10 requirement, the fixed constant satisfies the schema with strictly less surface. This alternative is retained only if the team establishes per-deployment notification-target routing as a §16.10 feature, stated explicitly rather than derived from conformance.
- **No CLI flag (dropped CODE-4).** With `RenderOpenSLO`'s signature unchanged, `lenny-ctl slo export` needs no `--notification-target` flag and no edit.
- **No top-level AlertNotificationTarget document and no schema subschema.** The change does not add an `AlertNotificationTargetDocument` subschema to `tests/testdata/openslo/schema/openslo-v1.schema.json` or a new entry to the tier-0 test's kind-routing map. The `targetRef` design keeps the document kind set SLI, SLO, and AlertPolicy.
- **No notification destination or credentials.** The change defines no Slack channel, webhook URL, PagerDuty key, or email address and no credentials. Those live in the deployer's OpenSLO tool under the referenced AlertNotificationTarget name.
- **No change to the §16.5 SLO catalog.** The `SLODefinitions` catalog, the burn-rate thresholds and windows, and the canonical metric names are unchanged; the export stays a faithful view of the same source.
- **No change to the §16.9 PrometheusRule or ConfigMap alert export or the in-process evaluator.** The change is scoped to the §16.10 OpenSLO export.
- **No edit to the vendored OpenSLO v1 schema or its README.** The export is made to conform to that authoritative transcription rather than the schema being changed to match the export.

## 6. Testing

The change reaches tier 0 and tier 1 for `pkg/alerting/rules` and the tier-0 static suite per `.claude/rules/test-coverage.md`. It touches a wire-format contract (the generated OpenSLO YAML the deployer's external tooling consumes), which the tier-0 conformance test validates against the vendored schema. Each test pins one behavior the change introduces and asserts the non-happy path the object model names.

- **tier-0 conformance (spec-named-failure path, TEST-1/CODE-1/CODE-2):** In `tests/tier0_static/openslo_export_test.go`, un-skip `TestOpenSLOChartFragmentMatchesSpecification` so it validates every document in the regenerated `charts/lenny/files/openslo.yaml` against its per-kind subschema. The non-happy path is a document that satisfies Lenny's own decoding but violates the OpenSLO v1 object model (a two-condition AlertPolicy, an AlertPolicy missing `notificationTargets`, or a bare-string `alertPolicies` entry) that a conformant tool would reject. `// spec: 16.10 (OpenSLO v1 object-model conformance of the generated chart fragment)`.
- **tier-1 one condition per AlertPolicy (boundary path, CODE-1):** In `pkg/alerting/rules/openslo_test.go`, assert each SLO renders two AlertPolicy documents (`<name>-burnrate-fast`, `<name>-burnrate-slow`), each with exactly one condition, and that the fast policy carries the 14x/1h critical condition and the slow policy the 3x/6h warning condition. The non-happy path is a policy carrying two conditions against the `maxItems: 1` cap, or a dropped burn-rate window. `// spec: 16.10 (one condition per AlertPolicy), 16.5 (multi-window burn rate preserved across two policies)`.
- **tier-1 non-empty notificationTargets on every AlertPolicy (spec-named-failure path, CODE-1):** In `pkg/alerting/rules/openslo_test.go`, assert every AlertPolicy carries a present, non-empty `notificationTargets` whose single entry's `targetRef` equals `lenny-slo-notifications`. The non-happy path is an AlertPolicy with an absent or empty `notificationTargets`, which the object model's `required` and `minItems: 1` reject. `// spec: 16.10 (required non-empty notificationTargets)`.
- **tier-1 alertPolicies object entries (boundary path, CODE-1):** In `pkg/alerting/rules/openslo_test.go`, assert each SLO's `alertPolicies` renders as `{alertPolicyRef: <name>}` objects, each referencing a rendered AlertPolicy document by name, rather than bare strings. The non-happy path is a bare-string entry that is neither the `alertPolicyRef` branch nor an inline AlertPolicy. `// spec: 16.10 (SLO alertPolicies as reference objects)`.

## 7. Findings closed on application

This proposal has no assigned BUILD-GAPS or TEST-GAPS finding ID. It closes the structural non-conformance recorded in the `t.Skip` message of `tests/tier0_static/openslo_export_test.go:62-67` (the two-condition AlertPolicy, the omitted `notificationTargets`, and the bare-string `alertPolicies` entries) by making `TestOpenSLOChartFragmentMatchesSpecification` run and pass against the regenerated chart fragment. The change runs at chart-generation and test time and needs no operator hardware.

## 8. Resolved in adversarial review

Subsequent adversarial review rounds populate this section. The drafting pass applied the following convergence revision before first review:

- **The notification target moved from a deployer configuration surface to a fixed conventional constant, dropping SPEC-1, CODE-3, and CODE-4.** An earlier draft added a §16.10 spec section and a `monitoring.openslo.notificationTarget` Helm key (with a `values.schema.json` edit, an install-time placeholder mirroring `__DEPLOYMENT_TIER__`, a `required` fail-closed gate, and a `lenny-ctl slo export --notification-target` flag) so a deployer could set the destination. The schema's `targetRef` branch (`openslo-v1.schema.json:163-172`) requires only a non-empty opaque string, and a `targetRef` is a cross-reference the deployer resolves in their own tool with no effect on the rendered PromQL or the evaluated series, so conformance needs no deployer configuration. The tier-substitution analogy fails because the tier is spliced into query text and label selectors while the target name changes nothing about the documents' semantics, and the fail-closed argument was circular (the render failed without a target only because the design required one; an undefined `targetRef` already errors at tool-ingestion time). The revision emits a fixed `openSLONotificationTargetName = "lenny-slo-notifications"` directly from `RenderOpenSLO`, documents the convention in the `values.yaml` comment and the code, and drops the spec section, the Helm config key, the `values.schema.json` edit, the placeholder threading, the fail-closed gate, and the CLI flag. The signature-changing parts of CODE-1, CODE-2, and CODE-4 were removed accordingly; per-deployment target routing is recorded as an open decision rather than smuggled in as a conformance fix.

### Pass 1 (2026-07-16, automated)

- **Tracked the tier-1 test rename in the spec-section coverage map.** TEST-2 renamed `TestRenderOpenSLOEmitsThreeDocsPerSLO` but left `tests/spec-map.json` unlisted, so the `"16.10"` section's `tests` entry (`tests/spec-map.json:2631`) would point at a function that no longer exists after the edit. The spec-map validator strips the `::TestName` selector before stat-ing the `.go` file (`cmd/lenny-test/cmd_validate.go:507-517`), so it does not flag a stale function name, and the §16.10 coverage map would ship wrong. TEST-2 now names the concrete new function (`TestRenderOpenSLOEmitsFourDocsPerSLO`) and directs updating the `"16.10"` entry, and §10 adds `tests/spec-map.json` to the files touched.

## 9. Open decisions for review

### Notification-target representation

The proposal renders, on each AlertPolicy, `notificationTargets: [{targetRef: "lenny-slo-notifications"}]` referencing an AlertNotificationTarget the deployer defines externally in their OpenSLO tool (option C), keeping the document kinds SLI, SLO, and AlertPolicy and requiring no schema or test-map change. Two alternatives change the surface:

- **Option A** inlines a full `{kind: AlertNotificationTarget, metadata, spec}` target in every AlertPolicy. It is self-contained and needs no schema or test-map change, but it duplicates the target across every policy and requires the deployer to configure the destination type rather than only a name.
- **Option B** emits a single top-level AlertNotificationTarget document and references it via `targetRef`. It is the idiomatic OpenSLO define-once, reference-many form and is self-contained within the ConfigMap, but it requires adding an `AlertNotificationTargetDocument` subschema to the vendored schema, a new entry in the tier-0 test's kind-routing map, and an update to the §16.10 sentence naming the emitted document kinds.

The choice trades self-containment of the ConfigMap against added schema and test surface. The recommendation is to confirm option C; the reviewer may select A or B instead.

A related decision is whether the notification-target name should become per-deployment configurable. The spec states no per-deployment notification-routing requirement, so the fixed constant is used. If the team decides distinct targets per deployment (for example `acme-prod` versus `acme-staging` sharing one OpenSLO tool) is a §16.10 feature, the dropped `monitoring.openslo.notificationTarget` key (CODE-3) is added back, justified as a feature rather than derived from conformance.

## 10. Files touched on application

- `pkg/alerting/rules/openslo.go`: CODE-1 (change `sloSpec.AlertPolicies` `:72` to `[]openSLOAlertPolicyRef`; add `NotificationTargets` to `alertPolicySpec` `:87-91`; add the `openSLOAlertPolicyRef` and `openSLONotificationTargetRef` types and the `openSLONotificationTargetName` constant; split the single AlertPolicy document `:181-221` into `<name>-burnrate-fast` and `<name>-burnrate-slow`, each with one condition and one `targetRef` notification target; set the SLO `AlertPolicies` to the two reference objects `:178`; remove the now-unused `policyName` local `:142`; update the `RenderOpenSLO` doc comment `:113-127`).
- `pkg/alerting/rules/openslo_test.go`: TEST-2 (decode `alertPolicyRef` and `targetRef` in `parsedDoc`; rename `TestRenderOpenSLOEmitsThreeDocsPerSLO` to `TestRenderOpenSLOEmitsFourDocsPerSLO`; expect four documents and two AlertPolicy documents per SLO; assert one condition per policy with the fast/critical and slow/warning split; assert the `alertPolicyRef` object entries; assert the non-empty `targetRef` notificationTargets on every AlertPolicy).
- `tests/spec-map.json`: TEST-2 (rename the `"16.10"` section's `pkg/alerting/rules/openslo_test.go::TestRenderOpenSLOEmitsThreeDocsPerSLO` test entry `:2631` to `::TestRenderOpenSLOEmitsFourDocsPerSLO`, tracking the function rename so the §16.10 coverage map does not dangle).
- `charts/lenny/files/openslo.yaml` (generated): CODE-2 (regenerated via `make generate`; not hand-edited).
- `docs/alerting/openslo.yaml` (generated): CODE-2 (regenerated via `make generate`; not hand-edited).
- `charts/lenny/values.yaml`: CODE-2 (one documentation line in the `monitoring.openslo` comment `:1022` naming the `lenny-slo-notifications` AlertNotificationTarget the deployer defines in their tool; no config key added).
- `tests/tier0_static/openslo_export_test.go`: TEST-1 (remove the `t.Skip(...)` call `:62-67` so `TestOpenSLOChartFragmentMatchesSpecification` validates the regenerated fragment; kind-routing map `:75-81` unchanged).
