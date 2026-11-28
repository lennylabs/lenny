---
id: OPSREM-I1
status: open
kind: build-gap
severity: low
requirements: []
---

# The operations inventory has no source for the idempotency_in_progress and webhook_delivery_pending kinds

## Specification

[§25.4](../../spec/25_agent-operability.md#254-the-lenny-ops-service) (The `lenny-ops` Service), **Operations Inventory › Operation Kinds**: `idempotency_in_progress` comes from `ops_idempotency_keys` where `status='in_progress'`, and `webhook_delivery_pending` from `ops_event_deliveries` where `status='pending'`.

## Evidence

- `operations.New` in `cmd/lenny-ops/services_wiring.go` registers the lock, escalation, upgrade, restore, and backup sources and the drift reconciliation source. Its comment says that "the idempotency and webhook-delivery kinds plug in as their subsystems expose enumeration".
- `parseOpKinds` in `pkg/ops/opsserver/operations.go` accepts both kinds in a filter, and `KindPrefix` in `pkg/ops/operations/inventory.go` gives them the prefixes `idemp` and `delivery`, but no `Source` returns either kind, and `opsidem.Store` has no method that lists in-progress records.
- Found by the in-depth review of `pkg/ops/opsidem` against the gateway's idempotency design that decision 19 of change 0178 asked for, 2026-11-27 (finding F13), and checked against the code of `main` at `c173b3790` on the same day.

## Gap

`GET /v1/admin/operations` never lists an in-progress idempotency claim or a pending webhook delivery, and a filter on either kind returns an empty page with no degradation warning.

## Suggested resolution

Add a source for each kind, with a listing method on the idempotency store and on the delivery store, or remove the kinds from §25.4 and from the filter. Add a contract test that an in-progress claim and a pending delivery appear in the inventory.

The owner answered IP-21 on the M3 readiness decision page on 2026-11-27 with a redesign of the `lenny-ops` idempotency records as a change of its own (decision 20 of change 0178), which is change [0183](../../changes/0183-lenny-ops-idempotency/change.md). Step 0183.S5 of that change closes the `idempotency_in_progress` part of this issue. The `webhook_delivery_pending` part has no idempotency record and no step, so the issue stays `open` with no `change` until a step takes that part.
