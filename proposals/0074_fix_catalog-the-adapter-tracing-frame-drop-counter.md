# Proposal: Catalog the adapter tracing-frame drop counter

- **Status:** Draft for review.
- **Date:** 2026-08-14
- **Scope:** Adds the `lenny_adapter_set_tracing_context_dropped_total` counter to the §16.1 metric
  catalog. The counter exists in code and in the deployer-facing metrics reference; the specification
  catalog an operator reads to decide what to scrape and alert on does not name it. Stages one spec edit
  and no code change.

This document stages the proposed specification change. It does not modify any spec, code, or doc file.
Apply the change in the "Proposed changes" section after sign-off.

## 1. Problem

Proposal 0069 gave the adapter an addressing rule for the §28.5.3 `set_tracing_context` frame: a frame
that does not address the Attach stream that delivered it is dropped, counted, and logged as a protocol
error. The count landed as `lenny_adapter_set_tracing_context_dropped_total`, registered in
`pkg/adapter/metrics.go` and documented in `docs/reference/metrics.md` and
`docs/reference/adapter-contract.md`.

The §16.1 metric catalog in `spec/16_observability.md` enumerates the other adapter-emitted signals
(`lenny_adapter_sopeercred_disabled_total` at `spec/16_observability.md:186`,
`lenny_adapter_sopeercred_selftest_failed_total` at `:187`, and `lenny_adapter_coordinator_hold` at
`:185`) and gained no row for this one, because 0069 stated the count in §28.5.3 prose only and listed no
`spec/16` edit among the files it touched.

The counter is the only externally observable signal that a runtime's tracing frames are being dropped.
The runtime receives nothing back for a dropped frame and the frame is discarded, so an operator who does
not know the series exists has no way to see the condition. A catalog that omits it understates the
adapter's metric surface.

## 2. Decisions

1. **The row goes in the Coordination & Reconciliation block, beside its siblings.** The two
   `SO_PEERCRED` adapter counters and the adapter coordinator-hold gauge sit there, and the catalog groups
   by emitting concern rather than by emitting process.

2. **The row repeats the scrape-reachability note its siblings carry.** The adapter emits inside the agent
   pod, which the §16.9 default scrape target set does not reach, so the row states that condition the way
   `:186` and `:187` state it.

3. **No alerting rule is staged.** A dropped frame loses advisory metadata and fails no task, and the
   counter sits outside the default scrape set, so there is no default target for a bundled rule to
   evaluate. Deployers who wire an adapter scrape target alert on it as they do on
   `lenny_adapter_sopeercred_disabled_total`.

## 3. Proposed changes

### SPEC-1: add the catalog row

In `spec/16_observability.md`, in the §16.1 metric catalog table, insert one row immediately after the
`SO_PEERCRED self-test failures` row (`spec/16_observability.md:187`):

```
| set_tracing_context frames dropped (`lenny_adapter_set_tracing_context_dropped_total`, counter — increments when a runtime's [§28.5.3](28_communication-channels.md#2853-ch-msgsock-runtime-message-socket) `set_tracing_context` frame does not address the Attach stream that delivered it and the adapter drops it; emitted by the adapter process inside the agent pod and therefore outside the [§16.9](#169-prometheus-scrape-targets-and-crds) default scrape target set until a deployer wires an adapter scrape target. See [Section 28.5.3](28_communication-channels.md#2853-ch-msgsock-runtime-message-socket) set_tracing_context addressing.) | Counter         |
```

## 4. Amendments to other artifacts

`docs/reference/metrics.md` already carries the counter in its adapter block with the same semantics, so
the reference needs no edit. Confirm the row against the staged catalog wording when the edit lands.

## 5. Testing

The tier-11 adapter metric catalog sweep in
`tests/tier11_docs/adapter_metric_catalog_test.go` requires every adapter metric documented in
`docs/reference/metrics.md` to be named in the §16.1 catalog, with a pending exception that names this
proposal. Landing the row makes that exception stale, and the sweep fails until it is removed. Removing
the exception is part of applying this proposal.

## 6. Files touched on application

- `spec/16_observability.md` (SPEC-1).
- `tests/tier11_docs/adapter_metric_catalog_test.go` (drop the pending exception).
