# Removal of the openai-assistants reference runtime

Status: `In progress`, revision 1, 2026-11-25.

## Problem

The reference runtime catalog of [§26](../../spec/26_reference-runtime-catalog.md) lists `openai-assistants`, a runtime that adapts the OpenAI Assistants API to Lenny's session lifecycle. OpenAI shut the Assistants API down on 2026-08-26, as desk validation VAL-017 of the M3 readiness review found, so the runtime has no upstream to adapt. The tree still registers it: the embedded catalog, the chart values, the rendered manifests, the compliance catalog, the test fixtures, and the documentation all name it, and `lenny up` registers a record for it.

## Scope

The change removes `openai-assistants` from every carrier that states the current system or ships with it:

- The specification: §26.10, the §26.1 catalog row, the §14 `runtimeOptions` schema, and the runtime lists of §5.1 (**First-Party Reference Runtimes**) and §15.7 (**Relationship to reference runtime catalog.**).
- The code and the chart: `pkg/embedded/stack/catalog.go`, `pkg/compliance/reference_catalog.yaml`, `pkg/ctlcli/install.go`, the `referenceRuntimes.catalog` entry in `charts/lenny/values.yaml`, the rendered `pkg/embedded/manifests/manifests.yaml`, and the citations of §26.10 in Go comments and the CRD field descriptions generated from them.
- The tests: the chart's reference-runtimes test, the embedded catalog test, the conformance catalog test, the test fixture constant, the runtime list of `cmd/lenny-test`, and the subset groups of `tests/groups.subsets.yaml`.
- The documentation and the root-level records: the client guide, the runtime author guide, the status page, the comparisons page, the `lenny up` walkthrough, the documentation index, `README.md`, and `TESTING.md`.

It leaves out the records that keep the words they were written with: `changes/archive/`, `proposals/`, the audit records `BUILD-GAPS.md` and `TEST-GAPS.md`, and `testdata/` directories. Other changes that are not finished and that name the runtime in their design text, 0118, 0122, 0135, and 0137, are left as written: under "Reading the program" of [v1](../programs/v1.md), an edit a member change states for a removed runtime has no successor and is moot.

## Requirements

None by identifier. §26 is a numbered section, and its area has not migrated.

## Detailed design

### OA-1 The specification

§26.10 is deleted with its heading. No section citation in the domain the citation gates read points at it once this change removes the citations in code, and §26.11 and §26.12 keep their numbers and anchors, so no citation of another section moves. The specification's root map drops the §26.10 entry.

The §26.1 catalog row and the §14 schema are deleted. The runtime lists of §5.1 and §15.7 drop the name, and the `temperature` note of the §14 `claude-code` schema drops it from its list of runtimes whose providers accept [0, 2].

### OA-2 The code and the chart

The catalog entry, the chart value, the compliance catalog entry, and the `lenny-ctl install` reference-runtime name are deleted. The embedded manifests are rendered again with `make generate`'s Helm step. The Go comments that cite §26.10 on `RuntimeSpec` and the runtime controller drop the citation, and the CRD manifests are generated again from the Go types.

## Specification and non-specification changes

- `spec/26_reference-runtime-catalog.md`: §26.1 and §26.10.
- `spec/14_workspace-plan-schema.md`: **Built-in runtime `runtimeOptions` schemas.**
- `spec/05_runtime-registry-and-pool-model.md`: §5.1, **First-Party Reference Runtimes**.
- `spec/15_external-api-surface.md`: §15.7, **Relationship to reference runtime catalog.**
- `spec/README.md`: the §26 entries of the root map.
- `pkg/embedded/stack`, `pkg/compliance`, `pkg/ctlcli`, `pkg/apis/lenny/v1alpha1`, `pkg/controller/runtime`, `pkg/embedded/manifests`, `pkg/embedded/crds`, `charts/lenny`, `cmd/lenny-test`, `tests/conformance`, `tests/testinfra/fixtures`, and `tests/groups.subsets.yaml`.
- `tests/registers/spec-section-citations.yaml`: the section-citation counts of the packages whose annotations change.

## Documentation

- `docs/client-guide/index.md` (client developer): the runtime table drops the row.
- `docs/runtime-author-guide/index.md` (runtime author): the reference runtime table drops the row.
- `docs/about/status.md` (all readers): the runtime table drops the row.
- `docs/about/comparisons.md` (evaluator): the list of framework adapters drops the name.
- `docs/tutorials/lenny-up-walkthrough.md` (operator): the bundled catalog list drops the name.
- `docs/index.md` and `README.md` (all readers): the catalog list drops the name.

## Tests

The change removes a runtime and adds no behavior. The tests that named the runtime drop it: the chart's reference-runtimes test renders one document fewer and drops the case for the runtime, the embedded catalog test and the conformance catalog test drop the name, and the test fixture constant `RuntimeOpenAIAssistants` is deleted.
