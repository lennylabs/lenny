---
layout: default
title: "lenny-ctl tests"
parent: "Testing"
nav_order: 4
description: How the §15.1 operability tests are organised — testing the lenny-ctl command surface across tier-4 integration and tier-5 Kind e2e.
---

# Testing `lenny-ctl`

`lenny-ctl` is Lenny's operator CLI (spec §15.1). It exposes 14 command categories: bootstrap, preflight, runtime management, pool management, credential pools, quota, circuit breakers, external adapters, token management, tenant management, session investigation, erasure jobs, migrations, policy, agent-operability extensions, server discovery, session operations, runtime scaffolding, local stack, installation wizard.

`lenny-ctl` itself is a Phase 4.5 / 13 deliverable — the binary doesn't exist on disk yet. This page documents how the tests are organised so they're ready when it lands.

## Where the tests live

| Test scope | Location |
|:-----------|:---------|
| Unit tests of pure pkg/ subcommand logic | `pkg/lenny-ctl/<subcommand>/*_test.go` |
| Tier-4 integration through the gateway | `tests/tier4_integration/lenny_ctl_<category>_test.go` |
| Tier-5 e2e against a Kind cluster + chart | `tests/tier5_e2e_kind/lenny_ctl_<category>_test.go` |

The `lenny_ctl_` filename prefix groups them at directory listing.

## Test pattern

Every `lenny-ctl` test follows the same shape:

```go
// spec: 15.1 (lenny-ctl credential pool add)
// diagnosis: The command did not produce the expected admin
//
//	rotation event. Inspect the gateway's
//	/v1/admin/credential-pools handler.
func TestCtlCredentialPoolAdd(t *testing.T) {
    gw := gateway.Start(t)  // tier-4: subprocess gateway
    ctl := lennyctl.Build(t)
    out := ctl.Run(t, gw.BaseURL(),
        "credential", "pool", "add",
        "--name", fixtures.CredentialPoolMockAnthropic,
        "--provider", "anthropic")
    assertions.StringContains(t, out.Stdout, "added")
    // ... assertions against the gateway's audit log ...
}
```

The `tests/testinfra/lennyctl/` helper (not yet shipped) will provide:
- `Build(t)` — compile the `lenny-ctl` binary into the test's tempdir
- `Run(t, baseURL, args...)` — invoke the binary with a tenant + auth set
- `RunExpectError(t, baseURL, args...)` — invoke expecting a non-zero exit

## Idempotency contract

Every mutating command must satisfy the §11.5 idempotency contract:
- `--idempotency-key K`: caller-supplied key
- Re-running the same command with the same key must return the cached result, not a duplicate write
- Different bodies under the same key return §11.5 422 `IDEMPOTENCY_KEY_REUSED`

Each test should include the same-key replay assertion.

## Dry-run contract

Every mutating command supports `--dry-run`:
- The command prints the planned mutation (resource shape, fields changed)
- Exit code 0 even when the mutation would fail to apply
- No side effect on the server side

The matching test asserts the dry-run output equals the eventual real-run output.

## Categories

The full category list is in spec §15.1; the test naming convention is `TestCtl<Category><Action>` (e.g. `TestCtlBootstrapFromValues`, `TestCtlPoolUpgradeStart`, `TestCtlCredentialRotate`).

When the `lenny-ctl` binary ships:
1. Move the scaffolded tests under `tests/tier4_integration/scaffolds_test.go` (e.g. `TestAdminBootstrap`) into per-category files.
2. Add entries to `tests/spec-map.json` under §15.1 referencing each test.
3. Update `tests/groups.yaml` phase-4.5-gate to include the new test files.

Until then the category-level scaffolds sit under `tests/tier4_integration/scaffolds_test.go` with `t.Skip("not implemented: §15.1 ...")`.
