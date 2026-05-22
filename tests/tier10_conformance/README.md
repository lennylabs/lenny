# tier10_conformance

Runtime-adapter conformance suite per TESTING.md §12.10. `cmd/lenny-compliance` is the conformance harness: a standalone binary that takes a runtime binary path and a declared integration level, runs the §15.4 test battery for that level, and produces a JSON report.

The tests here build the harness and the bundled reference runtimes (`echo`, `streaming-echo`, `delegation-echo`) and exercise the harness against them. A conformant runtime must pass the Basic, Standard, and Full batteries appropriate to its declared level. A runtime that declares a lower level than it implements must fail at the next level up.

## Current state

The suite is a placeholder. `scaffolds_test.go` is the only file in the tier; every named §12.10 test calls `t.Skip` with a `blocked:` diagnosis naming the missing deliverable:

| Test | Blocked on |
|:--|:--|
| `TestReferenceCatalogNightly` | §26 reference-runtime OCI images published to a registry |
| `TestThirdPartyRegistration` | `RegisterAdapterUnderTest` entry point in `cmd/lenny-compliance` |
| `TestFidelityMatrix` | Documented per-OutputPart fidelity table plus the OpenAI / Anthropic translators |

Once those deliverables land the scaffolds split into per-subject `*_test.go` files and lose their skips.

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
