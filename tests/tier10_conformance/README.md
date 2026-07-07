# tier10_conformance

Runtime-adapter conformance suite per TESTING.md §12.10. `cmd/lenny-compliance` is the conformance harness: a standalone binary that takes a runtime binary path and a declared integration level, runs the §15.4 test battery for that level, and produces a JSON report.

The tests here build the harness and the bundled reference runtimes (`echo`, `streaming-echo`, `delegation-echo`) and exercise the harness against them. A conformant runtime must pass the Basic, Standard, and Full batteries appropriate to its declared level. A runtime that declares a lower level than it implements must fail at the next level up.

## Current state

The suite is implemented. `scaffolds_test.go` builds `cmd/lenny-compliance` and the bundled reference runtimes (`echo`, `streaming-echo`, `delegation-echo`) and exercises `TestBasicAdapterProtocol`, `TestStandardLevel`, `TestFullLevel`, and `TestBundledRuntimesEveryPR` against them, plus `TestReferenceCatalogNightly`, `TestThirdPartyRegistration`, and `TestFidelityMatrix`. `concurrent_slot_conformance_test.go` and `recycle_scrub_conformance_test.go` cover the §5.2 concurrent-session slot lifecycle and whole-pod scrub conformance scenarios.

- `TestReferenceCatalogNightly` asserts `pkg/compliance.ReferenceCatalog()` is structurally complete against the §26.1 reference-runtime manifest unconditionally. It additionally exercises the registry-driven image-pull contract, logging the recognized registry, only when `LENNY_REFERENCE_IMAGE_REGISTRY` is set; the multi-gigabyte image pull itself is a release-pipeline concern out of scope for the in-process test runner.
- `TestThirdPartyRegistration` exercises `pkg/compliance.RegisterAdapterUnderTest`, the same entry point a downstream runtime project imports from its own test code, against the bundled echo runtime.
- `TestFidelityMatrix` asserts the documented per-`MessagePart` fidelity table against the OpenAI Chat Completions and Open Responses translators in `pkg/gateway/externalapi/outputpartfidelity`.

Every test in the tier gates on the Go toolchain being on `PATH` (needed to build the harness and the runtimes); that is the one skip in the suite, and it is a genuine external-dependency skip rather than a missing deliverable. No test in the tier is currently blocked on an undelivered dependency.

## Build tag and invocation

```bash
# All conformance tests
lenny-test --tier conformance

# A single named subset
lenny-test --tier conformance --subset basic
```

Each file declares `//go:build conformance`. The Go toolchain must be available; a missing toolchain is a genuine external-dependency skip.

## Subdirectory conventions

When the suite grows, group new test files by the level they exercise:

- Basic-level scenarios → `basic_*_test.go`
- Standard-level scenarios → `standard_*_test.go`
- Full-level scenarios → `full_*_test.go`

Subdirectories (`basic/`, `standard/`, `full/`) are reserved for the eventual split if the file count exceeds the readability threshold.
