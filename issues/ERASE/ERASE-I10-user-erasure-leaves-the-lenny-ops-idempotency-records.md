---
id: ERASE-I10
status: open
kind: spec-defect
severity: low
requirements: []
---

# User erasure deletes no lenny-ops idempotency records

## Specification

[§12.8](../../spec/12_storage-architecture.md#128-compliance-interfaces) (Compliance Interfaces): the erasure scope table's row "Idempotency records" and step 17 of the **`DeleteByUser` dependency-ordered deletion sequence** delete the user's `idempotency_keys` rows found by `(tenant_id, subject)`. Neither names `ops_idempotency_keys`, which [§25.4](../../spec/25_agent-operability.md#254-the-lenny-ops-service) (The `lenny-ops` Service, **Idempotency**) defines.

## Evidence

- `ops_idempotency_keys` stores `caller_id`, the caller's `sub`, and the stored response body (`pkg/ops/opsidem/pgstore/pgstore.go`), for 24 hours or 7 days.
- A stored response can hold personal data: a keyed `POST /mcp/management` that calls a gateway-owned admin tool, such as `admin.create_user` (`pkg/ops/mcp/generated_tools.go`), stores the gateway's response through `opsidem`.
- No eraser in `cmd/lenny-gateway` or `pkg/ops` deletes rows of `ops_idempotency_keys`.
- Found by the in-depth review of `pkg/ops/opsidem` against the gateway's idempotency design that decision 19 of change 0178 asked for, 2026-11-27 (finding F11), and checked against the code of `main` at `c173b3790` on the same day.

## Gap

An erased user's subject, and data about the user in a stored response, stay in `ops_idempotency_keys` until the record expires, and in each backup taken meanwhile.

## Suggested resolution

Add `ops_idempotency_keys` to the §12.8 erasure scope, deleting the rows whose `caller_id` is the erased user's subject, and state what happens to a record of another caller whose stored response names the user, as [APICONV-I15](../APICONV/APICONV-I15-stored-idempotency-responses-outlive-what-they-describe-and-have-no-size-limit.md) asks for the gateway. The gateway's user eraser cannot reach the platform-scoped table through a tenant transaction, so the step names the store that deletes the rows.

Scheduling waits on the owner's answer to IP-21 on the M3 readiness decision page: whether step 0178.S6 takes the full list of findings of that review, keeps the scope that decision 19 of change 0178 approved, or splits off an urgent fix. The issue stays `open` with no `change` until that answer.
