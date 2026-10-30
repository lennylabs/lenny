# Proposal: Catalog the adapter tracing-frame drop counter

- **Status:** **Implemented green (2026-08-14), independently verified on reached tiers 0 and 11.**
  Applied to spec (2026-08-14). Approved (2026-08-14) by jaf sign-off. Verified (2026-08-14). Converged
  after 5 adversarial review rounds (2 findings fixed). See §7 for the review history.
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

### SPEC-1. Add the catalog row

In `spec/16_observability.md`, in the §16.1 metric catalog table, insert one row immediately after the
`SO_PEERCRED self-test failures` row (`spec/16_observability.md:187`):

```
| set_tracing_context frames dropped (`lenny_adapter_set_tracing_context_dropped_total`, counter — increments when a runtime's [§28.5.3](28_communication-channels.md#2853-intra-pod) `set_tracing_context` frame does not address the Attach stream that delivered it and the adapter drops it; emitted by the adapter process inside the agent pod and therefore outside the [§16.9](#169-prometheus-scrape-targets-and-crds) default scrape target set until a deployer wires an adapter scrape target. See [Section 28.5.3](28_communication-channels.md#2853-intra-pod) set_tracing_context addressing.) | Counter         |
```

## 4. Amendments to other artifacts

`docs/reference/metrics.md` already carries the counter in its adapter block with the same semantics, so
the reference needs no edit. Confirm the row against the staged catalog wording when the edit lands.

## 5. Testing

The change reaches tier 0 and tier 11. It stages one specification table row and one test edit, and it
changes no package under `pkg/` or `cmd/`, so no other tier is reached.

**Tier 0 (static).** The staged row writes three intra-repo fragment links, which the fragment-link gate
reads. `TestFragmentLinkGateCertifiesTheTree` (`tests/tier0_static/fragment_link_test.go:375`) reads the
tracked markdown under `spec/` and `docs/` (`tests/tier0_static/fragment_link_test.go:56`) and resolves
the row's two links to `28_communication-channels.md#2853-intra-pod` against the `#### 28.5.3 Intra-pod`
heading at `spec/28_communication-channels.md:496`, and its same-page link to
`#169-prometheus-scrape-targets-and-crds` against the `### 16.9 Prometheus Scrape Targets and CRDs`
heading at `spec/16_observability.md:731`. A mistyped anchor fails the gate on application.

**Tier 11 (documentation).** The adapter metric catalog sweep
`TestAdapterMetricsReachTheDocumentationCatalogs` (`tests/tier11_docs/adapter_metric_catalog_test.go:83`)
requires every adapter metric documented in `docs/reference/metrics.md` to be named in the §16.1 catalog,
with a pending exception at `tests/tier11_docs/adapter_metric_catalog_test.go:50` that names this
proposal. Landing the row makes that exception stale, and the sweep fails until it is removed. Removing
the exception is part of applying this proposal.

## 6. Files touched on application

- `spec/16_observability.md` (SPEC-1).
- `tests/tier11_docs/adapter_metric_catalog_test.go` (drop the pending exception).

## 7. Resolved in adversarial review

### Pass 1 (2026-08-14, automated)

- **SPEC-1 cited a §28.5.3 fragment that does not exist.** Both links in the staged row targeted
  `28_communication-channels.md#2853-ch-msgsock-runtime-message-socket`. The only 28.5.3 heading is
  `#### 28.5.3 Intra-pod` at `spec/28_communication-channels.md:496`, that file declares no kramdown
  anchor attributes, and `tests/spec-anchor-moves.json` records no redirect, so the fragment resolves
  nowhere and the tier-0 fragment-link gate in `tests/tier0_static/fragment_link_test.go` would fail on
  application. Both links now use `#2853-intra-pod`, the form used at `spec/09_mcp-integration.md:24`
  and `spec/15_external-api-surface.md:1494`.

### Pass 2 (2026-08-14, automated)

- **The testing section named tier 11 alone and omitted tier 0.** The staged row adds three intra-repo
  fragment links, and the gate that resolves them is `TestFragmentLinkGateCertifiesTheTree`
  (`tests/tier0_static/fragment_link_test.go:375`), which reads `spec/` and `docs/`
  (`tests/tier0_static/fragment_link_test.go:56`). `.claude/rules/test-coverage.md:57` requires tier 0 on
  every change, and pass 1 already relied on that gate to justify the anchor correction. Section 5 now
  states the reached tiers, names the tier-0 case with the two heading targets it resolves
  (`spec/28_communication-channels.md:496` and `spec/16_observability.md:731`), and names the tier-11 case
  `TestAdapterMetricsReachTheDocumentationCatalogs`
  (`tests/tier11_docs/adapter_metric_catalog_test.go:83`) with the pending exception it holds at `:50`.
