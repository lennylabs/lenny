# Technical Design Review Findings — 2026-04-07 (Iteration 11)

**Document reviewed:** `docs/technical-design.md` (8,674 lines)
**Iteration:** 11 (25 agents, 1 per perspective)
**Total findings:** ~29 (0 Critical, ~3 High, ~22 Medium, ~5 Low)
**Clean perspectives:** CPS, WPL, CRD, SCH, EXP, MSG, POL (7 of 25)

## High Findings

| # | ID | Finding | Section |
|---|-----|---------|---------|
| 1 | K8S-030 | WPC RBAC `sandboxes/status` needs `get`/`patch` (not just `update`) for SSA | 4.6.3 |
| 2 | K8S-031 | CIDR drift detection goroutine needs `nodes` + `networkpolicies` read RBAC — not listed | 4.6.3, 13.2 |
| 3 | K8S-032 | `lenny-drain-readiness` webhook intercepts `nodes/eviction` — non-existent K8s resource; should be `pods/eviction` | 12.5 |

## Medium Findings (~22)

**Factual errors:**
- SEC-051: env blocklist glob "not including `_` boundaries" contradicts every example pattern
- EXM-035: §10.1 attributes Tier 3 rate (200/s) to Tier 2; Tier 2 is 30/s
- API-044: `DELEGATION_CYCLE_DETECTED` error description says "session_id" but mechanism uses runtime identity tuple
- DOC-142: §15.4 says "see Section 16" for Phase 2 build sequence; should be Section 18
- DEL-036: Cycle detection rationale conflates "cross-tree" with "runtime-identity" cycles

**Design flaws / contradictions:**
- SEC-050: SA token audience default `lenny-gateway` is static — defeats cross-deployment replay protection
- NET-033: `lenny.dev/egress-profile` label not protected by immutability webhook
- TNT-032: `lenny-noenvironmentpolicy-audit` named as K8s ValidatingAdmissionWebhook but intercepts Lenny REST API (impossible)
- CMP-039: Per-tenant single erasure salt breaks sequential multi-user billing pseudonymization coherence
- CMP-040: GDPR Recital 26 anonymization claim overstated — statistical re-identification not addressed
- OBS-048: `root_session_id` label on delegation metrics creates unbounded Prometheus cardinality
- SLC-037: `created` state timeout credential revocation atomicity undefined

**Operational gaps:**
- SCL-033: §17.8.2 missing mcp-type safety_factor (2.0 vs 1.5)
- PRT-033: `one_shot` second `request_input` error code unspecified
- DXP-034: macOS abstract socket constraint not in Runtime Author Roadmap
- OPS-039: Redis runbook conflates 60s per-outage with 300s cumulative cap
- STR-036: `DROP SEQUENCE billing_seq_{tenant_id}` not bound to store interface
- STR-037: MinIO lifecycle rule scoped to checkpoints/ only; other versioned prefixes unaddressed
- FLR-035: `lenny_gateway_rejection_rate` used in HPA guidance but never defined in §16.1
- BLD-029: Phase 16 still single-line entry (carry-forward)
- OPS-040: Preflight doesn't validate statusUpdateDeduplicationWindow for cloud-managed

_7 perspectives clean: CPS, WPL, CRD, SCH, EXP, MSG, POL._
