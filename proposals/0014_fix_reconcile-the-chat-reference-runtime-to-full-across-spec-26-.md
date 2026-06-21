# Proposal: Reconcile the chat reference runtime to Full across spec 26.1 and the implementation sites

- **Status:** Approved (2026-06-21). Verified (2026-06-20); converged after 2 adversarial review rounds (0 findings fixed); signed off by the user for implementation, not yet implemented. Both §8 open decisions are resolved per the proposal's recommendations: F-26.1.1 is left CLOSED with a superseded annotation rather than reopened (§3.10, §8 decision 1), and the orphan `cmd/lenny-compliance/reference-catalog.yaml` is deleted on application (§3.5 path 1, §8 decision 2). One docs-alignment edit site (`docs/about/status.md:91`, §3.11) was added by the orchestrator after convergence following independent verification of a finding the loop had refuted on materiality.
- **Date:** 2026-06-20.
- **Scope:** Resolves a spec-internal contradiction on the `chat` reference runtime's integration level. The §26.1 catalog table lists `chat` as Standard (`spec/26_reference-runtime-catalog.md:22`), while §26.7 states Full in three places (`spec/26_reference-runtime-catalog.md:309,313,322`) and declares `credentialCapabilities.hotRotation: true` (`spec/26_reference-runtime-catalog.md:336`), a capability that is coherent only at Full. This proposal stages a single spec edit (the §26.1 `chat` Level cell from Standard to Full) and the implementation, chart, and test edits that follow from it. It stages no new field, RPC, schema, flag, conformance taxonomy, or mode. It closes F-26.7.1 and records the reversal of the chart-side resolution that F-26.1.1 applied.

This document stages the proposed spec, code, chart, and test changes. It does not modify any spec, code, chart, or test file. Apply the changes in the "Proposed changes" section after sign-off.

## 1. Problem

The spec contradicts itself on the `chat` reference runtime's integration level, and the implementation follows the defective side. The §26.1 catalog table lists `chat` as Standard (`spec/26_reference-runtime-catalog.md:22`), while §26.7 asserts Full in three places: the prose calls `chat` "the smallest useful Full-level runtime" (`spec/26_reference-runtime-catalog.md:309`), the Conformance-level line reads Full (`spec/26_reference-runtime-catalog.md:313`), and the YAML highlights declare `integrationLevel: full` (`spec/26_reference-runtime-catalog.md:322`). §15.4.6 defines "Conformance level" as equal to the integration level from §15.4.3, with no separate taxonomy (`spec/15_external-api-surface.md:2380`), so the §26.7 Conformance-level line and the §26.1 Level cell assert one fact that disagrees with itself. This is finding F-26.7.1 (`BUILD-GAPS.md:46188`).

### 1.1 Full is the internally coherent side

§26.7 declares `credentialCapabilities.hotRotation: true` for `chat` (`spec/26_reference-runtime-catalog.md:336`). §4 ties `hotRotation: true` to lifecycle-channel delivery with no restart: when a runtime declares `credentialCapabilities.hotRotation: true`, the gateway uses hot rotation over the lifecycle channel and the runtime rebinds credentials in place without a restart (`spec/04_system-components.md:1474`). The §15.4.3 level matrix makes the lifecycle channel Full-only: it is N/A at Basic and Standard and Yes at Full (`spec/15_external-api-surface.md:2119`). The same matrix makes in-place credential rotation Full-only: Basic and Standard perform checkpoint then pod restart then `AssignCredentials`, and only Full does in-place rotation via `RotateCredentials` with no session interruption (`spec/15_external-api-surface.md:2122`). A Standard `chat` would advertise `hotRotation: true`, a capability its absent lifecycle channel cannot deliver. The §26.7 capabilities block is coherent only at Full, so the defect is the single §26.1 Level cell at `spec/26_reference-runtime-catalog.md:22`, which should read Full. The phrase "the minimum useful runtime" in that same cell describes the runtime's size, and the §26.7 prose phrase "the smallest useful Full-level runtime" makes the size reading explicit; neither phrase fixes the level at Standard.

The `chat` scaffolder template already declares Full with this proposal's exact reasoning: `runtime-chat.yaml.tmpl:12` sets `integrationLevel: full`, `:27` sets `hotRotation: true`, and the header comment at `:5-6` states "It declares integrationLevel: full because in-place credential rotation depends on the lifecycle channel." The scaffolded runtime a user generates from `lenny runtime init <name> --template chat` is therefore Full, while the reference `chat` it is modeled on is recorded as Standard. Reconciling §26.1 to Full removes that divergence.

### 1.2 The implementation followed the Standard side at sites the finding does not fully enumerate

The implementation hard-codes the Standard side. F-26.7.1 names three sites (`BUILD-GAPS.md:46191`): `pkg/embedded/stack/catalog.go`, `pkg/embedded/stack/catalog_test.go`, and `pkg/compliance/reference_catalog.yaml`. The full set is larger:

- `pkg/embedded/stack/catalog.go:197` sets `IntegrationLevel: "standard"` for `chat`, with a package comment at `:167-169` ("chat is Standard, every other reference runtime is Full") and a per-entry comment at `:192-195` ("Standard level"). The same entry sets `CredentialCapabilities: &credentialCapabilities{HotRotation: true, ...}` at `:205`, the identical incoherence the spec carries.
- `pkg/compliance/reference_catalog.yaml:64` sets `level: standard`. This file is embedded at `pkg/compliance/catalog.go:19` (`//go:embed reference_catalog.yaml`) and parsed by `compliance.ReferenceCatalog()`.
- `pkg/embedded/stack/catalog_test.go:37-38` special-cases `chat` to `want = "standard"`, with a comment at `:34-35`.
- `pkg/embedded/stack/bootstrap_seed_admin_test.go:93-94` asserts `chat.IntegrationLevel == "standard"` after the bootstrap seed registers the catalog, with a comment at `:87-88`. This is a second Go test the finding does not name.
- `tests/tier10_conformance/scaffolds_test.go:385` asserts `"chat": compliance.LevelStandard` in `TestReferenceCatalogNightly`, which reads the embedded `pkg/compliance/reference_catalog.yaml` through `compliance.ReferenceCatalog()` (`scaffolds_test.go:376`). This is the conformance-tier consumer of the compliance YAML the finding does not name.
- `charts/lenny/values.yaml:2954` sets `integrationLevel: standard`, with a comment at `:2947-2951` that cites F-26.1.1.
- `charts/lenny/tests/reference-runtimes_test.yaml:116-123` asserts `spec.integrationLevel: standard` for `chat`, with a comment at `:111-115` that cites F-26.1.1.

The chart value flows unchanged into the registered `Runtime` CRD through `charts/lenny/templates/reference-runtimes.yaml:55` (`integrationLevel: {{ .integrationLevel }}`). The two chart sites were set to Standard by F-26.1.1 (`BUILD-GAPS.md:45257`), a CLOSED finding that flipped the chart from `full` to `standard` to match the defective §26.1 cell. Reconciling §26.1 to Full reverses that resolution.

### 1.3 An orphan duplicate of the compliance catalog

`cmd/lenny-compliance/reference-catalog.yaml` is a byte-identical 3457-byte copy of `pkg/compliance/reference_catalog.yaml` (verified with `diff`; both are 3457 bytes). It also sets `level: standard` for `chat` at line 64. No `//go:embed` directive points at it (the only catalog embed is `pkg/compliance/catalog.go:19`), no Go file reads it by path, no drift-guard test asserts the two are byte-identical, and no packaging manifest references it. Its own header comment at `cmd/lenny-compliance/reference-catalog.yaml:6-14` claims to be "the catalog of record consulted by" the tier-10 `TestReferenceCatalogNightly`, but that test consults the `pkg/compliance` copy instead (`scaffolds_test.go:376`). Editing the cmd-side file changes no behavior and no test outcome. Carrying two byte-identical catalog files, one dead, already violates the single-canonical-implementation-per-concern principle.

## 2. Decisions

- **Resolve the contradiction toward Full.** The §26.7 capabilities block (`hotRotation: true` at `spec/26_reference-runtime-catalog.md:336`) is coherent only at Full per the spec coupling (`spec/04_system-components.md:1474`; `spec/15_external-api-surface.md:2119,2122`). Three of the four spec assertions (§26.7 lines 309, 313, 322) say Full; the lone §26.1 cell says Standard. The scaffolder template corroborates Full (`runtime-chat.yaml.tmpl:5-6,12,27`). Mutating §26.7 to `hotRotation: false` is the available alternative and is rejected because it contradicts the spec preponderance and the scaffolder template.
- **Treat `spec/26_reference-runtime-catalog.md:22` as the single spec defect.** Change that Level cell only; leave §26.7 and every other §26.1 row unchanged.
- **Flip every code, chart, and test site to Full.** F-26.7.1 names three; the verified set adds `pkg/embedded/stack/bootstrap_seed_admin_test.go:93-94` and `tests/tier10_conformance/scaffolds_test.go:385`. Every site that hard-codes the Standard side must move with the spec cell, or the spec-vs-implementation contradiction persists or the build and tests break after the flip.
- **Update every §26.1-citing comment at the flipped sites** so the prose stops asserting `chat` is Standard.
- **Reverse F-26.1.1's chart-to-Standard resolution.** Once §26.1 reads Full, the chart value and its helm-unittest must read Full. Record the reversal so a future reader does not re-flip the chart to match a cell that no longer says Standard.
- **Flip only the live compliance catalog; scope out the orphan duplicate.** The live consumer is `pkg/compliance/reference_catalog.yaml`, embedded at `pkg/compliance/catalog.go:19` and read by `compliance.ReferenceCatalog()`. `cmd/lenny-compliance/reference-catalog.yaml` is dead: no embed, no path reader, no drift guard. Editing it changes no behavior. The applier deletes the orphan or leaves it explicitly out of scope; a byte-identical drift guard that hardcodes the dead duplicate is not added.
- **Keep the change mechanical.** No new field, RPC, schema, flag, conformance taxonomy, or mode. §15.4.6 already equates conformance level with integration level. The `chat` special-case in `catalog_test.go` collapses after the flip; remove the dead branch.

## 3. Proposed changes

### 3.1 Spec change: `spec/26_reference-runtime-catalog.md` §26.1 catalog table `chat` Level cell (line 22)

Anchor on the `chat` row of the §26.1 catalog table, currently at line 22. The current row reads:

```
| `chat`             | General-purpose | Standard | "Talk to an LLM" with no tools; demonstrates the minimum useful runtime          |
```

The Level cell is the only spec assertion that reads Standard. §26.7 says Full in three places (`spec/26_reference-runtime-catalog.md:309,313,322`) and declares `hotRotation: true` (`spec/26_reference-runtime-catalog.md:336`), which is Full-only per `spec/04_system-components.md:1474` and `spec/15_external-api-surface.md:2119,2122`. Change the Level cell from `Standard` to `Full`. Replacement row:

```
| `chat`             | General-purpose | Full     | "Talk to an LLM" with no tools; demonstrates the minimum useful runtime          |
```

Notes for the applier:

- Change only the Level cell. The phrase "the minimum useful runtime" describes the runtime's size and stays unchanged.
- Leave every other column and every other row of the §26.1 table unchanged.
- Do not edit §26.7; it already reads Full at lines 309, 313, 322, and 336.
- Preserve the column alignment of the markdown table when widening `Standard` to `Full` (the cell narrows by four characters; re-pad with spaces so the pipes line up).

### 3.2 Code change: `pkg/embedded/stack/catalog.go` `chat` entry and comments

Anchor on the `chat` literal in `referenceRuntimes`, currently at line 191-207, and on the package comment at lines 164-169.

The `chat` entry sets `IntegrationLevel: "standard"` at line 197 while setting `CredentialCapabilities: &credentialCapabilities{HotRotation: true, ...}` at line 205, the same incoherence the spec carries. Flipping to Full removes it and aligns with §3.1.

Change the literal at line 197 from:

```go
		IntegrationLevel:       "standard",
```

to:

```go
		IntegrationLevel:       "full",
```

Reword the per-entry comment at lines 192-195 from:

```go
		// spec: §26.1 line 22 / §26.7 — chat is the minimum useful runtime:
		// Standard level, the small resource class only, multi_turn with
		// immediate (no queued) injection.
```

to:

```go
		// spec: §26.1 line 22 / §26.7 — chat is the minimum useful runtime:
		// Full level (hotRotation: true requires the Full-only lifecycle
		// channel per §15.4.3), the small resource class only, multi_turn
		// with immediate (no queued) injection.
```

Reword the package comment at lines 167-169 from:

```go
	// ghcr.io/lennylabs/runtime-<name>:1.0.0. The §26.1 catalog table
	// fixes the integration level of each runtime: chat is Standard, every
	// other reference runtime is Full.
```

to:

```go
	// ghcr.io/lennylabs/runtime-<name>:1.0.0. The §26.1 catalog table
	// fixes the integration level of each runtime: every reference
	// runtime, including chat, is Full.
```

Notes for the applier:

- Leave `CredentialCapabilities: &credentialCapabilities{HotRotation: true, ...}` at line 205 unchanged; it is coherent once the level is Full.
- Leave `AllowedResourceClasses`, `SupportedProviders`, `Capabilities`, `Image`, `Description`, and `Labels` on the `chat` entry unchanged.

### 3.3 Code change: `pkg/compliance/reference_catalog.yaml` `chat` entry (line 64)

Anchor on the `chat` entry's `level` key, currently at line 64. The current entry reads:

```yaml
  - name: chat
    category: general-purpose
    level: standard
    image: lennylabs/runtime-chat:1.0.0
    notes: "Talk-to-an-LLM runtime with no tools — the minimum useful runtime (§26.7)."
```

This file is embedded at `pkg/compliance/catalog.go:19` and read by `compliance.ReferenceCatalog()`, the live consumer that the tier-10 `TestReferenceCatalogNightly` calls (`scaffolds_test.go:376`). Change `level: standard` at line 64 to:

```yaml
    level: full
```

Notes for the applier:

- Change only the `level` value. The `notes` string is level-neutral and stays unchanged.
- The em-dash in the existing `notes` string is preserved verbatim; it is an existing line this edit does not touch.

### 3.4 Code change: `tests/tier10_conformance/scaffolds_test.go` `TestReferenceCatalogNightly` assertion (line 385)

Anchor on the `chat` entry of the `wantNames` map in `TestReferenceCatalogNightly`, currently at line 385. This test reads the embedded `pkg/compliance/reference_catalog.yaml` through `compliance.ReferenceCatalog()` (`scaffolds_test.go:376`) and asserts the expected level for every reference runtime. After §3.3 it must expect Full for `chat` or the test fails. Change:

```go
		"chat":              compliance.LevelStandard,
```

to:

```go
		"chat":              compliance.LevelFull,
```

Notes for the applier:

- Leave the other eight entries in `wantNames` unchanged; they already expect `compliance.LevelFull`.
- F-26.7.1 omits this site. It is a required edit: without it the tier-10 conformance test breaks after §3.3.
- Run `TestReferenceCatalogNightly` (or the tier-10 conformance package) to confirm green after the edit.

### 3.5 Code change: `cmd/lenny-compliance/reference-catalog.yaml` orphan duplicate (out of scope or deleted)

`cmd/lenny-compliance/reference-catalog.yaml` is a byte-identical copy of `pkg/compliance/reference_catalog.yaml` with no consumer: no `//go:embed` points at it, no Go file reads it by path, and no drift-guard test references it (verified in §1.3). Editing its `chat` level changes no behavior and no test outcome.

The applier takes one of two paths:

1. **Delete** `cmd/lenny-compliance/reference-catalog.yaml`, since it duplicates the live `pkg/compliance/reference_catalog.yaml` with no consumer and its header comment misdescribes which catalog the tier-10 test reads. Before deleting, confirm with `grep -rn "reference-catalog" cmd/ tests/ pkg/` and a `go:embed` scan that nothing references it.
2. **Leave it out of scope** if removing a dead file is outside this change's remit. In that case do not edit it; record in §9 that it remains an untouched orphan.

Notes for the applier:

- Do not add a byte-identical drift guard that hardcodes this duplicate.
- Do not flip only this file while leaving the live `pkg/compliance` copy at Standard; that would invert the live/dead relationship without fixing the contradiction.

### 3.6 Code change: `pkg/embedded/stack/catalog_test.go` collapse the chat special-case (lines 32-42)

Anchor on `TestReferenceRuntimesIntegrationLevels`, currently at lines 32-42. The test special-cases `chat` to `want = "standard"` following the §26.1 cell. After the flip every reference runtime is Full, so the branch is dead. The current body reads:

```go
func TestReferenceRuntimesIntegrationLevels(t *testing.T) {
	for _, rt := range ReferenceRuntimes() {
		// §26.1: chat is Standard; every other reference runtime is
		// Full.
		want := "full"
		if rt.Name == "chat" {
			want = "standard"
		}
		if rt.IntegrationLevel != want {
			t.Errorf("%s integrationLevel = %q, want %q", rt.Name, rt.IntegrationLevel, want)
		}
```

Replace the comment and remove the special-case branch so the body reads:

```go
func TestReferenceRuntimesIntegrationLevels(t *testing.T) {
	for _, rt := range ReferenceRuntimes() {
		// §26.1: every reference runtime, including chat, is Full.
		want := "full"
		if rt.IntegrationLevel != want {
			t.Errorf("%s integrationLevel = %q, want %q", rt.Name, rt.IntegrationLevel, want)
		}
```

Notes for the applier:

- Keep the `// spec:` annotation convention; the spec citation moves into the reworded comment.
- Leave the `ghcr.io/lennylabs/runtime-` image-prefix assertion and the `@sha256:` digest-pin assertion (lines 43-50) unchanged.
- Run the test to confirm green.

### 3.7 Code change: `pkg/embedded/stack/bootstrap_seed_admin_test.go` `chat` level assertion (lines 87-94)

Anchor on the `chat` block of the bootstrap-seed admin test, currently at lines 87-94. This is a second Go test, distinct from `catalog_test.go`, that asserts `chat.IntegrationLevel == "standard"` after the bootstrap seed registers the catalog. F-26.7.1 does not name it; it breaks on the flip. The current text reads:

```go
	// spec: §26.1 line 22 — chat is Standard and carries the small
	// resource class only.
	chat, err := runtimes.Get(context.Background(), "chat")
	if err != nil {
		t.Fatalf("get chat: %v", err)
	}
	if string(chat.IntegrationLevel) != "standard" {
		t.Errorf("chat integrationLevel = %q, want standard", chat.IntegrationLevel)
	}
```

Replace it with:

```go
	// spec: §26.1 line 22 / §26.7 — chat is Full (hotRotation: true
	// requires the Full-only lifecycle channel) and carries the small
	// resource class only.
	chat, err := runtimes.Get(context.Background(), "chat")
	if err != nil {
		t.Fatalf("get chat: %v", err)
	}
	if string(chat.IntegrationLevel) != "full" {
		t.Errorf("chat integrationLevel = %q, want full", chat.IntegrationLevel)
	}
```

Notes for the applier:

- Leave the `AllowedResourceClasses` (`[small]`) and the injection-capability assertions that follow (lines 96 onward) unchanged.
- Run the test to confirm green.

### 3.8 Chart change: `charts/lenny/values.yaml` `chat` entry and comment (lines 2947-2954)

Anchor on the `chat` entry in `referenceRuntimes.catalog`, currently at lines 2952-2954, and its comment at lines 2947-2951. The chart value flows unchanged into the registered `Runtime` CRD through `charts/lenny/templates/reference-runtimes.yaml:55`. F-26.1.1 set this to `standard` to match the defective §26.1 cell; §3.1 reverses that.

Change `integrationLevel: standard` at line 2954 to:

```yaml
      integrationLevel: full
```

Replace the comment at lines 2947-2951, which currently reads:

```yaml
    # spec: §26.1 line 22 anchors `chat` at Standard ("the minimum useful
    # runtime"). Every other reference runtime is Full; the chart value
    # MUST match the spec catalog row so the registered Runtime CRD
    # advertises the §15.4.6 conformance battery the chat runtime
    # actually implements. F-26.1.1.
```

with:

```yaml
    # spec: §26.1 line 22 anchors `chat` at Full ("the minimum useful
    # runtime"); hotRotation: true requires the Full-only lifecycle
    # channel (§15.4.3). The chart value MUST match the spec catalog row
    # so the registered Runtime CRD advertises the §15.4.6 conformance
    # battery the chat runtime implements. F-26.7.1 (supersedes F-26.1.1).
```

Notes for the applier:

- Leave `image`, `allowedResourceClasses`, `supportedProviders`, the `capabilities` block, and the injection note (lines 2953, 2955-2963) unchanged.

### 3.9 Chart change: `charts/lenny/tests/reference-runtimes_test.yaml` `chat` assertion and comment (lines 111-123)

Anchor on the `chat` integration-level assertion, currently at lines 116-123, and its comment at lines 111-115. The helm-unittest case asserts `spec.integrationLevel: standard` for `chat` and was added by F-26.1.1 to guard the Standard value. After §3.1 and §3.8 it must assert Full or helm-unittest fails. The current text reads:

```yaml
  # spec: §26.1 line 22 — F-26.1.1. The chat runtime is the §26.1
  # catalog's lone Standard-level entry ("the minimum useful runtime").
  # Every other reference runtime is Full; the chart MUST match the
  # spec table so the registered Runtime CRD advertises the §15.4.6
  # conformance battery the runtime actually implements.
  - it: registers chat at integrationLevel standard per §26.1 line 22
    documentSelector:
      path: metadata.name
      value: chat
    asserts:
      - equal:
          path: spec.integrationLevel
          value: standard
```

Replace it with:

```yaml
  # spec: §26.1 line 22 — F-26.7.1 (supersedes F-26.1.1). Every reference
  # runtime, including chat, is Full; chat's hotRotation: true requires
  # the Full-only lifecycle channel (§15.4.3). The chart MUST match the
  # spec table so the registered Runtime CRD advertises the §15.4.6
  # conformance battery the runtime implements.
  - it: registers chat at integrationLevel full per §26.1 line 22
    documentSelector:
      path: metadata.name
      value: chat
    asserts:
      - equal:
          path: spec.integrationLevel
          value: full
```

Notes for the applier:

- Run `helm unittest` on the `lenny` chart to confirm the case passes after the edit.
- Leave the surrounding cases (the `allowedResourceClasses` assertion at lines 105-109 and the "renders no runtimes when the catalog is disabled" case at lines 125 onward) unchanged.

### 3.10 BUILD-GAPS change: close F-26.7.1 and record the F-26.1.1 reversal

Anchor on the F-26.7.1 entry at `BUILD-GAPS.md:46188` and the F-26.1.1 entry at `BUILD-GAPS.md:45257`.

Mark F-26.7.1 CLOSED. Record that §26.1 line 22 was reconciled to Full (the internally coherent side per `hotRotation: true`), and that the implementation, chart, and test sites and their comments were flipped to Full: `pkg/embedded/stack/catalog.go`, `pkg/compliance/reference_catalog.yaml`, `pkg/embedded/stack/catalog_test.go`, `pkg/embedded/stack/bootstrap_seed_admin_test.go`, `tests/tier10_conformance/scaffolds_test.go`, `charts/lenny/values.yaml`, and `charts/lenny/tests/reference-runtimes_test.yaml`.

Annotate the F-26.1.1 entry that its chart-to-Standard resolution was superseded by the F-26.7.1 reconciliation to Full. The chart now reads `integrationLevel: full` and its helm-unittest asserts `full`.

Notes for the applier:

- This is a build-tracking update. It changes no behavior.
- See §8 for the open decision on whether to reopen-and-re-resolve F-26.1.1 or leave it CLOSED with a superseded annotation.

### 3.11 Docs change: `docs/about/status.md` reference-runtime catalog `chat` row (line 91)

Anchor on the `chat` row of the "Reference runtime catalog" table in the project status page, currently at line 91. The Notes cell describes `chat` as "Standard integration level", which after §3.1 reconciles §26.1 to Full describes superseded behavior. The current row reads:

```
| `chat` | Not started | Generic LLM chat, no tools. Standard integration level. |
```

Change the Notes cell to read Full:

```
| `chat` | Not started | Generic LLM chat, no tools. Full integration level. |
```

Notes for the applier:

- Change only the integration-level word in the Notes cell. Leave the `Status` value (`Not started`) and the rest of the cell unchanged.
- Leave every other row of the catalog table unchanged; none of them states an integration level for a runtime this proposal touches.
- Per the docs-content rules, docs follow the spec; this edit makes the page agree with the reconciled §26.1 cell rather than driving the spec decision.

## 4. Non-goals

- **No edit to §26.7.** It is already coherent on Full at `spec/26_reference-runtime-catalog.md:309,313,322,336`. The defect is solely the §26.1 cell at line 22.
- **No change to `chat` capabilities, resource class, providers, injection modes, isolation profile, or limits.** Only the level is wrong. `hotRotation: true` is what makes Full coherent and stays.
- **No new field, RPC, schema, flag, conformance taxonomy, or deployment mode.** §15.4.6 already equates conformance level with integration level (`spec/15_external-api-surface.md:2380`).
- **No dual-level or compatibility path for `chat`.** v1 ships a single canonical level per reference runtime; `chat` is Full everywhere.
- **No edit to the scaffolder template or the scaffold comments.** `cmd/lenny-ctl/runtimescaffold/templates/runtime-chat.yaml.tmpl:5-6,12,27` already declares Full, and the scaffold comments at `pkg/ctlcli/runtime.go:346` and `cmd/lenny-ctl/runtimescaffold/scaffold.go:112-113,213` already describe the scaffolded `chat` as Full. They corroborate the direction and need no change.
- **No change to the §26.2 or §5.3 isolation-profile references near `chat`.** `isolationProfile: sandboxed` and the `standard`/`sandboxed` profile names are isolation profiles, unrelated to integration level.
- **No edit to `cmd/lenny-compliance/reference-catalog.yaml` beyond an optional deletion.** The original draft proposed flipping it in lockstep with the live `pkg/compliance` copy and adding a byte-identical drift guard. That is rejected: the file has no consumer (no embed, no path reader, no test), so editing it changes no behavior, and a drift guard would hardcode a dead duplicate. The applier either deletes the orphan or leaves it untouched (§3.5). Two original C3 rationale claims are false and dropped. First, the claim that "the compliance catalog drives §15.4.6 conformance-level signaling" is wrong: §15.4.6 reads each runtime's own `runtime.yaml` `integrationLevel` (`spec/15_external-api-surface.md:2382-2384`) rather than a compliance catalog YAML. Second, the claim that "the duplicate must move in lockstep or the two diverge" describes a divergence with no consumer and no enforcing test, so it is harmless.

## 5. Testing

- **Tier 0 (static):** confirm the edited §26.1 table renders and the markdown column alignment holds. Run `gofumpt` and `goimports` on the edited Go files; run `go build` and `go vet` on `pkg/embedded/stack`, `pkg/compliance`, and `tests/tier10_conformance`.
- **Tier 1 (unit):** run `pkg/embedded/stack` unit tests, including `TestReferenceRuntimesIntegrationLevels` (§3.6, the dead `chat` branch removed) and the bootstrap-seed admin test (§3.7, now asserting Full). Both must pass with `chat` at Full.
- **Tier 10 (conformance):** run `TestReferenceCatalogNightly` (`tests/tier10_conformance/scaffolds_test.go`), which reads the embedded `pkg/compliance/reference_catalog.yaml` and after §3.3 and §3.4 expects `compliance.LevelFull` for `chat`.
- **Chart unit tests (helm-unittest):** run the `lenny` chart unit suite, including the reworked `registers chat at integrationLevel full per §26.1 line 22` case (§3.9). The case must pass with the value `full`.
- **Tier 11 (docs):** confirm the §26.1 table and the §26.7 body agree (both Full), and that the catalog comments in `pkg/embedded/stack/catalog.go`, `charts/lenny/values.yaml`, and `charts/lenny/tests/reference-runtimes_test.yaml` no longer assert `chat` is Standard.
- **No new behavioral tier-2-or-higher test is added.** The change flips an enumerated value at each site; the existing assertions are updated to expect Full and are re-run.

## 6. Findings closed on application

- **F-26.7.1** (`BUILD-GAPS.md:46188`, DEFERRED): closed. The deferral cited spec arbitration belonging to the maintainer; §3.1 performs that arbitration toward Full. §3.2 through §3.9 re-align the catalog entry, the compliance YAML, the chart, and every test assertion.
- **F-26.1.1** (`BUILD-GAPS.md:45257`, CLOSED): its chart-to-Standard resolution is reversed by §3.8 and §3.9 and annotated as superseded (§3.10). See §8 for the record-keeping choice.

## 7. Resolved in adversarial review

Review rounds populate this section. The challenge revision already folded into the draft: C3's scope was reduced from "flip both compliance YAMLs and add a byte-identical drift guard" to "flip only the live `pkg/compliance/reference_catalog.yaml`, add the omitted `tests/tier10_conformance/scaffolds_test.go:385` assertion, and delete or scope out the orphan `cmd/lenny-compliance/reference-catalog.yaml`." Two false rationale claims were dropped. The compliance catalog does not drive §15.4.6 signaling, because §15.4.6 reads each runtime's own `runtime.yaml integrationLevel` (`spec/15_external-api-surface.md:2382-2384`). The duplicate need not move in lockstep, because the orphan has no consumer. The `tests/tier10_conformance/scaffolds_test.go:385` site, omitted from the original enumeration, is now a staged change (§3.4).

After convergence, the orchestrator added one edit site the review loop had raised and then refuted on materiality: `docs/about/status.md:91` describes `chat` as "Standard integration level" in the reference-runtime catalog table (independently verified by `grep`). It is a real docs-alignment site: once §26.1 reads Full, that line describes superseded behavior. It is staged as §3.11.

## 8. Open decisions for review

- **F-26.1.1 record-keeping.** F-26.1.1 is already CLOSED with a chart-to-Standard resolution. Two conventions keep the tree consistent: (1) reopen F-26.1.1 and re-resolve it toward Full, or (2) leave it CLOSED and annotate it as superseded by F-26.7.1 (the §3.10 approach). The proposal recommends option (2): F-26.1.1's stated remediation (chart to Standard) was correct against the spec at the time it was applied, and the reversal is a consequence of the later §26.1 reconciliation rather than a defect in F-26.1.1's own resolution. **Resolved at sign-off (2026-06-21): option (2).** F-26.1.1 stays CLOSED and is annotated as superseded by F-26.7.1 per §3.10; it is not reopened.
- **Orphan-file disposition.** §3.5 leaves the applier to delete `cmd/lenny-compliance/reference-catalog.yaml` or scope it out. Deletion removes the single-canonical-implementation violation; scoping it out defers that cleanup. The proposal recommends deletion, since the file has no consumer and its header comment misdescribes which catalog the tier-10 test reads. **Resolved at sign-off (2026-06-21): delete.** The applier takes §3.5 path 1 and deletes the orphan after confirming no consumer with the `grep`/`go:embed` scan; it is not left untouched.

## 9. Files touched on application

- `spec/26_reference-runtime-catalog.md`: §26.1 catalog table `chat` Level cell (line 22) changed from Standard to Full. §26.7 unchanged.
- `pkg/embedded/stack/catalog.go`: `chat` entry `IntegrationLevel` (line 197) changed to `"full"`; the per-entry comment (lines 192-195) and the package comment (lines 167-169) reworded to state every reference runtime, including chat, is Full. `CredentialCapabilities` (line 205) unchanged.
- `pkg/compliance/reference_catalog.yaml`: `chat` `level` (line 64) changed to `full`.
- `tests/tier10_conformance/scaffolds_test.go`: `TestReferenceCatalogNightly` `wantNames` `chat` entry (line 385) changed to `compliance.LevelFull`.
- `pkg/embedded/stack/catalog_test.go`: `TestReferenceRuntimesIntegrationLevels` (lines 32-42) loses the `chat` special-case branch, and the comment (lines 34-35) is reworded so `want = "full"` applies to every runtime.
- `pkg/embedded/stack/bootstrap_seed_admin_test.go`: `chat` integration-level assertion (lines 93-94) changed to expect `full`; the comment (lines 87-88) reworded.
- `charts/lenny/values.yaml`: `chat` entry `integrationLevel` (line 2954) changed to `full`; the comment (lines 2947-2951) reworded to cite F-26.7.1 and the Full reasoning.
- `charts/lenny/tests/reference-runtimes_test.yaml`: `chat` integration-level assertion (lines 116-123) changed to assert `full`; the `it` description and the comment (lines 111-115) reworded.
- `cmd/lenny-compliance/reference-catalog.yaml`: deleted as a dead orphan duplicate (§8 decision 2 resolved to deletion at sign-off; §3.5 path 1).
- `docs/about/status.md`: the reference-runtime catalog `chat` row (line 91) Notes cell changed from "Standard integration level" to "Full integration level".
- `BUILD-GAPS.md`: F-26.7.1 (line 46188) marked CLOSED; F-26.1.1 (line 45257) annotated as superseded by the F-26.7.1 reconciliation to Full.
