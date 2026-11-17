---
id: NAMING-I1
status: open
kind: build-gap
severity: medium
requirements: []
---

# Adapter metric names keep the retired channel spelling

## Specification

§28.1 (Naming law), **N4.**: "The metric half of N4 is deferred." The deferral is recorded as an issue that names the step that discharges it, per §28.4 (Implementation status).

## Evidence

- Claim register row (`tests/claim-map.json`, retired by 0116.S5): claim "Adapter metric names carrying the retired channel spelling", status `ABSENT`, anchor `#naming-table`, deferral `R12`.
- Surface recorded on the row: "pkg/adapter/metrics.go:71, pkg/adapter/metrics.go:79".
- Note recorded on the row: "the two metric names keep the retired spelling until R12 adds the adapter metrics endpoint and the catalog entries".
- `pkg/adapter/metrics.go` still defines `lenny_adapter_control_events_total` and `lenny_adapter_control_events_dropped_total`, which count `CH-ADAPTEREVENTS` events (triage, 2026-11-17).

## Gap

The adapter's two event metrics carry a name that is not the `CH-ADAPTEREVENTS` identifier, and the adapter exposes no metrics endpoint whose catalog entries would carry the canonical name.

## Suggested resolution

The row deferred the work to step R12, "The adapter-to-gateway control direction, with hold state", of `gateway-runtime-comms-remediation.md`.

No member change of the v1 program renames these metrics. `CH-ADAPTEREVENTS` survives into the target state, since 0139 and 0145 add events to it.
