### CMP-042 Pluggable MemoryStore Erasure Callback Gap [MEDIUM]
**Files:** `12_storage-architecture.md` (§12.1, 12.8), `09_mcp-integration.md` (§9.4)

The `MemoryStore` interface permits deployers to substitute pluggable vector database backends (Mem0, Zep, or custom implementations). Section 12.8 specifies that DeleteByUser includes a MemoryStore deletion step (step 8): "delete all memories written by or scoped to the user." However, the interface definition does not define a mandatory erasure callback contract or validation mechanism. A deployer integrating a custom MemoryStore backend without proper `DeleteByUser` implementation will silently proceed with erasure, leaving undeleted memories in the custom backend while the audit receipt records successful completion. This creates a compliance risk for GDPR Article 17 and HIPAA erasure obligations.

**Recommendation:** Define a mandatory `DeleteByUser` interface method in the `MemoryStore` contract with a documented signature. Add a preflight check to the erasure job that verifies the configured MemoryStore backend exposes the required method. Document in the deployment guide that custom MemoryStore implementations are responsible for implementing DeleteByUser and that failures are non-recoverable (must halt erasure with a critical audit event). Alternatively, if pluggable MemoryStore is not required for v1, move it to a future phase and use only Postgres-backed memory store in v1 to ensure erasure completeness at launch.

---

### CMP-043 GDPR Article 20 Export Scope Ambiguity [MEDIUM]
**Files:** `12_storage-architecture.md` (§12.8), `15_external-api-surface.md` (§15.1 GDPR section)

Section 12.8 documents data portability (Article 20): "Session metadata, audit events, and billing records are available via the admin REST API in JSON format. Deployers are responsible for assembling and delivering portable exports to data subjects." This delegates responsibility to deployers but does not clarify the scope of "audit events." Specifically:

1. Cross-tenant audit reads (platform-admin operations scoped as `current_tenant = '__all__'`) emit audit events recorded under the platform tenant (§11.1), not the target tenant. It is unspecified whether these events should be included in a user's portable export if they document admin impersonation of that user.

2. The spec requires that deployers "account for all Lenny stores listed in the erasure scope table above" when implementing DSAR procedures (§12.8), but does not require deployers to document which audit event categories are included in their export scope. This creates compliance exposure: a deployer who exports only direct user actions and excludes admin reads could face GDPR claims that the export is incomplete.

**Recommendation:** Clarify in §12.8 that user portable exports MUST include: (a) all audit events where `user_id` (actor) matches the requesting user, AND (b) all audit events from platform-admin impersonation operations that accessed or modified data scoped to that user (`unmapped.lenny.target_tenant_id = requesting_user's_tenant_id`). Add a template DSAR audit query to the deployment guide showing the required WHERE clause. Require deployers to document their DSAR export scope in their RoPAs and validate that the scope satisfies Article 20.

---

No real issues found in: GDPR erasure deletion sequence (steps 1–19 are dependency-ordered and complete), audit log integrity (startup verification + periodic checks + hash chaining + pgaudit all present), billing event immutability (INSERT-only grants + trigger bypass correctly modeled), data residency per-region consistency (explicit deployer responsibility noted for cross-region billing aggregation and quotas), salt verification procedure (verification_failed halts erasure until investigated).
