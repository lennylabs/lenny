---
id: APICONV-I18
status: open
kind: spec-defect
severity: medium
requirements: []
---

# lenny-ops scopes an idempotency record by the bare subject, with no tenant or verifier source

## Specification

[§25.4](../../spec/25_agent-operability.md#254-the-lenny-ops-service) (The `lenny-ops` Service), **Idempotency**: the primary key is `(key, caller_id)`, where `caller_id` is the OIDC `sub` claim of the requesting service account, and the error table defines `403 IDEMPOTENCY_KEY_OWNED_BY_OTHER_CALLER` for a key that another caller holds. [§11.5](../../spec/11_policy-and-controls.md#115-idempotency) **Mechanism.** scopes a gateway record by the principal's tenant, subject, and bound session, and treats the same key from different subjects independently, within a tenant and across tenants.

## Evidence

- `callerIdentity` in `pkg/ops/opsserver/backup.go` returns `Principal.Subject`, which `New` in `pkg/ops/opsserver/opsserver.go` passes to `opsidem` as `CallerID`. Both stores key a record by `(key, caller_id)` (`pkg/ops/opsidem/memory.go`, `pkg/ops/opsidem/pgstore/pgstore.go`), and `ops_idempotency_keys` has no tenant or source column.
- `lenny-ops` admits a principal holding `platform-admin` or `tenant-admin` (`requireAdminRole` in `pkg/ops/opsserver/auth.go`), and with `--auth-multi-tenant` it resolves each bearer's tenant from its tenant claim (`cmd/lenny-ops/flags.go`). It verifies Lenny-issued tokens and, in dev mode, the dev trust hook (`buildAuthConfig` in `cmd/lenny-ops/main.go`, `cmd/lenny-ops/httpsurface.go`).
- `Claim` in `pkg/ops/opsidem/pgstore/pgstore.go` answers `IDEMPOTENCY_KEY_OWNED_BY_OTHER_CALLER` when a live row holds the key under another `caller_id`, as §25.4 requires.
- Found by the in-depth review of `pkg/ops/opsidem` against the gateway's idempotency design that decision 19 of change 0178 asked for, 2026-11-27 (finding F6), and checked against the code of `main` at `c173b3790` on the same day.

## Gap

Two principals of different tenants or different verifiers whose `sub` values are equal share records, and so do the tokens derived for one subject, such as a delegated or impersonation token that holds an admin role. The owned-by-other `403` lets a caller learn that a key is in use and lets it block a key it knows for another caller ([APICONV-I17](APICONV-I17-lenny-ops-keys-unauthenticated-requests-by-a-client-supplied-caller.md) shows the unauthenticated form).

## Suggested resolution

Change §25.4 to scope a record by tenant, subject, and verifier source, together with the route of [APICONV-I16](APICONV-I16-ops-idempotency-replays-the-impersonation-bearer-by-subject.md), as change 0178 does for the gateway, and remove `IDEMPOTENCY_KEY_OWNED_BY_OTHER_CALLER` so that the same key from another caller is a separate record that reveals nothing. Recreate `ops_idempotency_keys` with the new key in one migration, with no compatibility shim. Add unit tests that the same subject under two tenants and under two sources gets two records.

Scheduling waits on the owner's answer to IP-21 on the M3 readiness decision page: whether step 0178.S6 takes the full list of findings of that review, keeps the scope that decision 19 of change 0178 approved, or splits off an urgent fix. The issue stays `open` with no `change` until that answer.
