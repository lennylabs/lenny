# Code best practices

Project-wide rules for the Go code in this repository. They apply to every change under `pkg/`, `cmd/`, `sdks/`, and `migrations/`, and to any agent or workflow that writes or modifies code here. They complement the linter rather than restate it.

## Top-level principle

New code is modular, small, and reuses existing surfaces, and it reads like the code around it. Match the surrounding package's naming, error handling, and structure. Keep each change minimal and focused; do not reformat or restructure code the change does not touch.

## What the linter already enforces

Tier 0 runs `golangci-lint` with `errcheck`, `govet`, `staticcheck`, `gosimple`, `unused`, `ineffassign`, `misspell`, `gofumpt`, and `goimports` (local prefix `github.com/lennylabs/lenny`). Do not hand-fight formatting or import grouping; run `gofumpt` and `goimports`. Never ignore a returned error to satisfy the compiler. These are machine-checked; the rules below cover what the linter does not.

## Functions and files

- Keep functions small and single-purpose. Extract a helper when a function exceeds roughly 50 lines or mixes levels of abstraction (request parsing, business logic, and I/O in one body).
- One responsibility per function; one concern per file. Split a file that has grown to cover several concerns.
- Prefer early returns over deep nesting. Guard clauses at the top, the main path unindented.

## Project structure and reuse

- Search for an existing package to reuse or extend before creating a new one. Cross-reference the §4–§17 component layout in the spec so a concern lands in its canonical package.
- One package per concern, named for the concern or the spec component it implements: components under `pkg/<concern>`, controllers under `pkg/controller/<name>`, CRD API types under `pkg/apis/lenny/<version>`. A new concern is a new directory. Avoid a catch-all dumping package; a shared helper goes with the concern it serves or in the established `pkg/common` package.
- Libraries live under `pkg/`; binaries under `cmd/` stay thin and delegate to `pkg/`. Do not put reusable logic in a `cmd/` main.
- Use an `internal/` subpackage to enforce an encapsulation boundary when helpers under a package subtree must not be imported elsewhere (as `pkg/gateway/mcptools/internal` does). Reach for it deliberately when a package's internals would otherwise leak into unrelated callers; most packages do not need one.
- Reuse over duplication: extract a shared helper rather than copy a block. Two near-identical blocks are a refactor, not a pattern.
- Prefer the standard library and the dependencies already in `go.mod`. A new third-party dependency is a supply-chain and maintenance surface: justify it, and reuse the cloud-provider SDK already imported rather than adding a second for the same provider.

## Errors

- Wrap errors with context and `%w` (`fmt.Errorf("claim sandbox %s: %w", id, err)`) so the chain is inspectable with `errors.Is`/`errors.As`.
- Define a typed error when callers branch on the failure; return the sentinel or typed value rather than a string match.
- Do not `panic` in library code; return an error. `panic` is for genuinely unreachable invariants only.
- Fail closed on security-relevant paths (auth, isolation, admission, credential handling): on doubt, deny.

## Interfaces, dependencies, and testability

- Define small interfaces at the consumer, not the producer. Accept interfaces, return concrete types.
- Inject dependencies (clocks, stores, clients, randomness) so a unit test can substitute them. Avoid global mutable singletons for anything that does I/O or carries state.
- Take `context.Context` as the first parameter on any function that does I/O, blocks, or spawns work, and propagate it; honor cancellation and deadlines. Do not bury `context.Background()` deep in a call chain.

## Concurrency and resource cleanup

- Guard shared state with the right primitive; document the invariant the lock protects.
- Code that runs concurrently must be `-race` clean; exercise it under tier 7a (see `test-coverage.md`).
- Do not leak goroutines: every goroutine has a clear exit tied to a context or a closed channel.
- Close what you open: `defer` the close of rows, response bodies, files, and connections. Put a timeout or deadline on every outbound network call; do not issue an unbounded one.

## Logging and secrets

- Log through `log/slog` (or `logr` in controller-runtime code, matching the surrounding controller). Do not use `fmt.Print*` for logging in library code.
- Never log credentials, lease tokens, API keys, or tenant secret material. Log identifiers and outcomes, not secret values (§13 security model).

## Naming, comments, and spec ties

- Use idiomatic Go names; avoid stutter (`session.Session`, not `session.SessionStruct`). Document every exported identifier.
- Comments generously explain why, not what. Delete commented-out code. Include contextual information that may be helpful to future AI agents reviewing or modifying the code.
- Cite the spec on spec-derived logic with `// spec: §X.Y`. Do not include line numbers since they can shift frequently. A reviewer should be able to trace a behavior to its spec section.

## Configuration and compatibility

- A default not fixed by the spec must be overridable by a flag or config value and documented as operator-tunable. Do not hard-code a non-spec constant with no override.
- No backward-compatibility shims, dual modes, legacy flags, or migration paths for external compatibility: the platform is pre-deployment and has no deployments in the wild. Change interfaces freely and update every caller.

## Where these rules apply

- All Go under `pkg/`, `cmd/`, `sdks/`, and `migrations/`.
- Inline code comments that constitute prose follow `doc-style.md`.

## How to apply when editing

1. Read the target package first; match its idioms, error style, and structure.
2. Before adding a package or helper, search for an existing one to extend.
3. Keep functions small and dependencies injected; add the `// spec:` citation on spec-derived logic.
4. Run `gofumpt`, `goimports`, and tier 0; resolve every linter finding rather than suppressing it.
5. Keep the diff scoped to the change; revert incidental reformatting.

## Escape hatches

- Generated code (`*.pb.go`, `zz_generated.deepcopy.go`, mocks, other codegen output) is exempt and must not be hand-edited. After changing a CRD type's `+kubebuilder` markers or a proto definition, regenerate the derived files and the CRD manifests rather than editing them by hand.
- Vendored or third-party code keeps its upstream style.
- A `nolint` directive is permitted only with an inline reason and is the exception, not a routine tool.

## Maintenance

When a review surfaces a recurring code defect this file does not cover, add a specific, actionable rule. Do not restate what the linter already enforces; keep this file to what review catches and tooling does not.
