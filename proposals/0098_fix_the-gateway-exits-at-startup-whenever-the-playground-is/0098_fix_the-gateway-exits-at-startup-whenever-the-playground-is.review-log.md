# Review log — 0098_fix_the-gateway-exits-at-startup-whenever-the-playground-is

## Standing context

## Ledger

### [spec.1.review-applicability.1]
FACT: SPEC-1's quoted row matches exactly one line, spec/27_web-playground.md:240, under `### 27.8 Metrics` (line 236); spec/18_build-sequence.md:772 names the metric without its label, so it needs no edit. No docs/, schemas/, or charts/ surface names the label. EVIDENCE: grep over spec docs schemas charts

### [spec.1.review-citations.1]
FACT: The §27.8 row SPEC-1 replaces exists verbatim at spec/27_web-playground.md:240 under `### 27.8 Metrics` (:236). The underscore-case rule SPEC-1 relies on is the "Lenny-specific underscore-case names elsewhere" sentence at spec/16_observability.md:294, under `### 16.1.1 Attribute Naming` (:292). — EVIDENCE: spec/27_web-playground.md:240, spec/16_observability.md:294
FACT: No other spec/, docs/, schemas/, or charts/ site names the page-views label. spec/18_build-sequence.md:772 lists the metric name with no label, and docs/reference/metrics.md has no page_views row. — EVIDENCE: grep -rn page_views spec schemas charts docs

### [spec.1.review-client-surface.1]
FACT: `lenny_playground_page_views_total` is named only at spec/27_web-playground.md:240 (§27.8 row) and spec/18_build-sequence.md:772 (name only, no label); no docs/, charts/, schemas/ or dashboard surface carries the metric or its label, so SPEC-1 is the only spec site — EVIDENCE: grep -rln playground_page_views over charts/ docs/ schemas/ pkg/ returns only pkg/gateway/mcpfabric/playground/metrics.go
FACT: §16.1.1 "Attribute naming" states "Lenny-specific underscore-case names" at spec/16_observability.md:294; it contains no literal "snake_case" — EVIDENCE: spec/16_observability.md:294

### [spec.1.review-docs-alignment.1]
FACT: docs/ never names `lenny_playground_page_views_total` or its label; docs/reference/metrics.md lists only `lenny_playground_dev_tenant_not_seeded_total` (line 376) among playground metrics, so the SPEC-1 label rename has no docs mirror site to stage. EVIDENCE: grep -rn playground_page_views docs spec schemas charts -> only spec/27_web-playground.md:240 and spec/18_build-sequence.md:772 (name only, no label)
WATCHOUT: the absence of the other playground metrics from docs/reference/metrics.md is pre-existing and outside this minimal proposal; do not file it here. EVIDENCE: docs/reference/metrics.md:376

### [spec.1.review-edit-sites.1]
FACT: `lenny_playground_page_views_total` appears in spec/, docs/, schemas/, charts/ at exactly two sites: spec/27_web-playground.md:240 (the §27.8 row SPEC-1 edits) and spec/18_build-sequence.md:772 (names the metric only, no label, so it stays correct). docs/ has no row for it. EVIDENCE: grep -rn page_views spec docs schemas charts
FACT: §16.1.1 underscore-case rule is at spec/16_observability.md:294; no §16 inventory row lists the playground counter or an authMode label. EVIDENCE: spec/16_observability.md:294

### [spec.1.review-fresh.1]
FACT: `lenny_playground_page_views_total` appears in spec/ only at spec/27_web-playground.md:240 (the SPEC-1 row, verbatim match) and by name only at spec/18_build-sequence.md:772 (no label named); no docs/ or schemas/ site names it or a label `auth_mode`/`authMode` for it. EVIDENCE: grep over spec docs schemas charts
FACT: the underscore-case rule SPEC-1's design cites is in §16.1.1 Attribute Naming (heading spec/16_observability.md:292, sentence :294 "Lenny-specific underscore-case names elsewhere"). EVIDENCE: spec/16_observability.md:294

### [spec.1.review-mechanism.1]
FACT: The page-views counter appears in spec only at spec/27_web-playground.md:240 (the §27.8 row SPEC-1 quotes verbatim) and by name at spec/18_build-sequence.md:772, which names no label; no §16 inventory row or alert carries it. EVIDENCE: grep -rn page_views spec docs schemas charts
FACT: §16.1.1 states labels use "Lenny-specific underscore-case names". EVIDENCE: spec/16_observability.md:294
UNVERIFIED: docs/reference/metrics.md has no row for lenny_playground_page_views_total (grep found none). That is a gap in the docs, not in the spec, and belongs to the non-spec loop if anyone wants it. The caller directive says to keep the proposal minimal.

### [spec.1.review-plan-conformance.1]
FACT: The plan item this proposal claims is the unchecked F-27.8.1 bullet under "Queued direct fixes filed by 0095's implementation (2026-10-23)". It assigns three deliverables: the §27.8 rename to `auth_mode` (SPEC-1), the code change (CODE-1), and unblocking 0095's skipped tier-4 playground-bearer case (TEST-1). It names no §16.1.1 edit. — EVIDENCE: gateway-runtime-comms-remediation.md:3143-3146
FACT: P5b's "Filed" paragraph names F-27.8.1 only as a filing and assigns no deliverable to 0098. — EVIDENCE: gateway-runtime-comms-remediation.md:2906-2909
DECISION: no plan-conformance findings on the spec staging — BECAUSE the plan's only spec deliverable is the rename, and SPEC-1 stages it with the same identifier — ALTERNATIVES: filing the declined §16.1.1 clause, rejected because the plan does not require it and D3 records the decision to decline it

### [spec.1.review-single-source.1]
FACT: The only spec site naming the page-views label is the §27.8 row; §18 build-sequence lists the metric name without labels, and no §16 table lists `authMode`/`auth_mode` as a label — EVIDENCE: spec/27_web-playground.md:240, spec/18_build-sequence.md:772
DECISION: no single-source findings on the spec staging — BECAUSE SPEC-1 is one cell edit and the design paragraph cites §16.1.1 and D3 in one clause each rather than restating them — ALTERNATIVES: none considered material
