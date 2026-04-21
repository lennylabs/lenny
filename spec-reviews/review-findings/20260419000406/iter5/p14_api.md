# Perspective 14 — API Design (iter5)

**Scope.** Re-review of the external API surface (`spec/15_external-api-surface.md`) and MCP consistency (`spec/09_mcp-integration.md`) against iter4 findings API-010 through API-016 and the `/v1/admin/*` endpoint catalogue touched in iter4 (HTTP status reclassifications, PUT/DELETE credential rows, `delivery_receipt` enum, `TREE_VISIBILITY_WEAKENING`).

**Calibration.** iter5 severities anchored to the iter4 rubric. No severity drift on carry-forwards (per `feedback_severity_calibration_iter5`). Fit-and-finish items remain Low.

## Inheritance of prior findings

| iter4 finding | iter5 disposition | Evidence |
| --- | --- | --- |
| API-010 `CREDENTIAL_SECRET_RBAC_MISSING` / `GIT_CLONE_AUTH_UNSUPPORTED_HOST` / `GIT_CLONE_AUTH_HOST_AMBIGUOUS` 400 → 422 [High] | **Fixed** | §15.4 lines 983 (`PERMANENT`/422), 1052, 1053 (`POLICY`/422). No stale `400 CREDENTIAL_SECRET_RBAC_MISSING` / `400 GIT_CLONE_AUTH_*` references remain in `spec/` (grep-verified). |
| API-011 `CONTENT_POLICY_WEAKENING` / `CONTENT_POLICY_INTERCEPTOR_SUBSTITUTION` / `DELEGATION_POLICY_WEAKENING` 403 → 422 [Medium] | **Fixed** | §15.4 lines 1055–1057 all at `POLICY`/422 with inline "Aligned with the canonical §15.4 pattern …" pointers; `TREE_VISIBILITY_WEAKENING` (line 1058) carries the same pattern. |
| API-012 `DELEGATION_PARENT_REVOKED` 409 PERMANENT → 403 POLICY [Medium] | **Fixed** | §15.4 line 1027 now `POLICY`/403 with rationale naming `CREDENTIAL_REVOKED` / `LEASE_SPIFFE_MISMATCH` as the canonical peers; §8.2 inline reference unaffected (names code, not status). |
| API-013 REST/MCP contract-test matrix not updated [Medium] | **Fixed** | §15.2.1 `RegisterAdapterUnderTest` matrix (line 1384) now enumerates the session-creation rejection family (`VARIANT_ISOLATION_UNAVAILABLE`, `REGION_CONSTRAINT_UNRESOLVABLE`, `GIT_CLONE_AUTH_UNSUPPORTED_HOST`, `GIT_CLONE_AUTH_HOST_AMBIGUOUS`, `ENV_VAR_BLOCKLISTED`, `SDK_DEMOTION_NOT_SUPPORTED`, `POOL_DRAINING`, `CIRCUIT_BREAKER_OPEN`, `ERASURE_IN_PROGRESS`, `TENANT_SUSPENDED`) with an in-spec maintenance rule binding §15.4 additions to matrix updates. |
| API-014 catalog uniqueness invariant not stated [Low] | **Not fixed** (iter4 Skipped, no Resolution block). See API-017 (carry-forward, Low). |
| API-015 `UNREGISTERED_PART_TYPE` uses `WARNING` category outside canonical taxonomy [Low] | **Not fixed** (iter4 Skipped, no Resolution block). See API-018 (carry-forward, Low). |
| API-016 `RESTORE_ERASURE_RECONCILE_FAILED` HTTP 500 for known operator-action failure [Low] | **Not fixed** (iter4 Skipped, no Resolution block). See API-019 (carry-forward, Low). |

No new Critical/High/Medium API-level issues were introduced by iter4's reclassifications or by the credential-pool / endpoint additions. The shared error taxonomy (§15.2.1 item 3), the REST/MCP consistency contract, and the MCP wire-projection table (§15.2 "Per-kind MCP wire projection") continue to hold; the §15.4 catalogue remains single-source-of-truth with one `(code, http_status, category, retryable)` tuple per row for every code checked.

## New findings (iter5)

All three iter5 findings are carry-forward surfaces of iter4 Low items that did not land a fix. Per the severity-calibration rule, they stay at Low; they are enumerated here so iter5 tracking surfaces them rather than silently letting them lapse.

### API-017 Catalog uniqueness invariant still not stated at the §15.4 header [Low]

**Section:** `spec/15_external-api-surface.md` §15.4 error-code catalogue (header at line 967, table begins line 969).

Iter3 API-006 and iter4 API-014 both recommended a single sentence near the `**Error code catalog:**` header stating the invariant that each `code` appears at most once in the table and carries a single `(category, httpStatus, retryable)` tuple. The invariant is implicit across the iter3/iter4 consolidations (API-005, API-010, API-011, API-012 all depend on it) and is referenced only inside §15.2.1 rule 3 and the iter4 fix prose — never at the catalog header where future contributors read. Without an explicit statement, future regressions of the iter1/iter2 duplicate-row class (the original API-001 / API-005 problem) are again undefended. Convergence-wise this is Low because all currently-known duplicate rows are consolidated and the §15.2.1 RegisterAdapterUnderTest matrix would catch a behavioural drift; the gap is a documentation-hardening fit-and-finish carry-forward.

**Recommendation:** Add a single sentence immediately under the `**Error code catalog:**` heading (before line 969): "Each `code` appears at most once in this table and carries a single `(category, httpStatus, retryable)` tuple. Per-endpoint descriptions of the same code live in the endpoint table (§15.1) and in the referenced section, not here." This restates the invariant §15.2.1 item 3 assumes, and anchors future contributors at the point-of-edit.

---

### API-018 `UNREGISTERED_PART_TYPE` row uses `WARNING` category outside the canonical `TRANSIENT|PERMANENT|POLICY|UPSTREAM` taxonomy [Low]

**Section:** `spec/15_external-api-surface.md` §15.4 error-code catalogue line 1036 (`UNREGISTERED_PART_TYPE` row); canonical taxonomy stated at line 965 and §16.3.

The row reads `| UNREGISTERED_PART_TYPE | WARNING | — | …`. One line above (line 965), the header prose states the `category` field is "one of `TRANSIENT`, `PERMANENT`, `POLICY`, `UPSTREAM` as defined in [Section 16.3]". `WARNING` is not in that closed set. The `—` (absent) HTTP status confirms the row is not actually a wire error but a non-rejecting annotation emitted on the `OutputPart` (§15.4.1 line 1479 and line 1498 describe it as an `unregistered_platform_type` warning annotation, not a wire error). Placing an annotation signal inside the error-code catalogue with a non-canonical category violates the taxonomy the same table advertises and confuses third-party adapter authors writing a `RegisterAdapterUnderTest` matcher. The iter4 §15.2.1 contract test (`retryable` / `category` equivalence check, rule 5d) assumes all catalogue rows carry a canonical category; `WARNING` either forces a contract-test exception or silently passes through.

**Recommendation:** Remove the `UNREGISTERED_PART_TYPE` row from the §15.4 error-code table and re-document it exclusively in §15.4.1 as an `OutputPart` warning annotation (it already has a full treatment there under "Namespace convention for third-party types" at line 1498). If a cross-reference at the §15.4 catalogue level is desirable, add a one-line footnote under the table header: "The `unregistered_platform_type` annotation is emitted as an `OutputPart` warning, not a catalogue error — see §15.4.1." This keeps the catalogue's category taxonomy closed and removes the annotation/error confusion.

---

### API-019 `RESTORE_ERASURE_RECONCILE_FAILED` HTTP 500 PERMANENT for a known operator-action failure path [Low]

**Section:** `spec/25_agent-operability.md` §25.11 backup/restore error-code table line 4296.

The row catalogues `RESTORE_ERASURE_RECONCILE_FAILED | PERMANENT | 500` for the post-restore GDPR erasure reconciler failure (the restore lifecycle between `restore_completed` and the gateway restart). The description itself enumerates four known operator-action failure sub-causes: individual replay failure, Postgres unavailability mid-reconcile, enumeration error, and the legal-hold ledger freshness gate (`gdpr.backup_reconcile_blocked`, reason `legal_hold_ledger_stale`). HTTP 500 PERMANENT is reserved in §15.4 (`INTERNAL_ERROR`, line 1008) for unexpected server errors — not for a known operator-action failure path that the handler deliberately returns and that §25.11 expects the operator to resolve via `GET /v1/admin/restore/{id}/status` + `POST /v1/admin/restore/{id}/confirm-legal-hold-ledger`. The ledger-stale sub-reason in particular is a `POLICY` rejection in the iter4 §15.4 taxonomy sense (well-formed restore request, rejected by a server-state policy gate analogous to `ERASURE_BLOCKED_BY_LEGAL_HOLD` at 409). The 500 PERMANENT row conflates a deliberate policy rejection with a bug-class internal error, skewing `INTERNAL_ERROR` alerts and dashboards that treat any 500 as a gateway defect.

**Recommendation:** Either (a) split the row into two codes — a `RESTORE_ERASURE_RECONCILE_FAILED` (`TRANSIENT`/503) covering the transient reconciler sub-causes (Postgres unavailability, enumeration error) and a `RESTORE_ERASURE_BLOCKED_BY_LEGAL_HOLD_LEDGER_STALE` (`POLICY`/409) covering the ledger-freshness gate, mirroring the `ERASURE_BLOCKED_BY_LEGAL_HOLD` (`POLICY`/409) pattern in §15.4 — or (b) keep the single code but recategorise it to `POLICY`/409 and remove the "individual replay failure" sub-cause from its description (folded into a separate transient code). Option (a) is preferable because the two sub-cause classes have different operator remediation (retry vs. ledger confirm) and the iter4 §15.4 taxonomy already models this split elsewhere. Update the §25.11 error-codes table line 4296 and the `gdpr.backup_reconcile_blocked` cross-reference in §12.8 accordingly.

---

## Convergence assessment

- **API-010 / API-011 / API-012 / API-013 (iter4 High/Medium) are cleanly resolved.** No stale HTTP-status inline references remain; each consolidated code carries a single canonical tuple; the REST/MCP contract test matrix is in lockstep with §15.4.
- **No new Critical/High/Medium findings.** The full §15.4 catalogue, the §15.1 endpoint table (including the iter4-added PUT/DELETE credential rows and the `CREDENTIAL_PROBE_UNAVAILABLE` TRANSIENT/503 row), the §15.2 MCP tool surface, the §15.2.1 consistency contract, and the `delivery_receipt` / `message_expired` closed enums (§15.2) are internally consistent and taxonomically clean.
- **Three Low carry-forwards** (API-017, API-018, API-019) persist from iter4. Each is a documentation / classification hardening item with no runtime contract impact and no REST/MCP divergence risk under the iter4 test matrix. They do not block convergence; they are listed so iter5 tracking surfaces them explicitly and they can land as a batched editorial fix in a single follow-up commit to §15.4 and §25.11 if desired.

**Net severity tally (iter5 API perspective):** Critical 0, High 0, Medium 0, Low 3 (all carry-forwards). Convergence criterion met for API Design.
