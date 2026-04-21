# Iter8 Security Review — Regressions Only

**Scope**: Regressions introduced by fix commit `bed7961` only. Pre-existing issues and long-lived carry-forwards are out of scope per the iter8 regressions-only protocol.

**Fix envelope surveyed**:
- `spec/13_security-model.md` §13.1–§13.2 (ephemeral-container cred-guard webhook narrative; NetworkPolicy webhook count update)
- `spec/09_mcp-integration.md` §9.2 (elicitation content integrity / gateway-origin binding invariant)
- `spec/16_observability.md` §16.1, §16.5, §16.7 (new metric, alerts, audit event)
- `spec/15_external-api-surface.md` §15.1 (two new PERMANENT error codes)
- `spec/17_deployment-topology.md` §17.2 (webhook #13 entry; count bumps)
- `docs/reference/error-catalog.md`, `docs/reference/metrics.md`, `docs/runtime-author-guide/platform-tools.md`, `docs/operator-guide/namespace-and-isolation.md`, `docs/operator-guide/observability.md` (docs sync)

**Consistency checks performed**:
- `EPHEMERAL_CONTAINER_CRED_UID_FORBIDDEN` classification (PERMANENT/422) matches across §15.1 and docs/reference/error-catalog.md.
- `ELICITATION_CONTENT_TAMPERED` classification (PERMANENT/409) matches across §9.2, §15.1, docs/reference/error-catalog.md.
- Webhook rejection-condition text in §13.1 is verbatim-identical to §17.2 webhook #13 row (three conditions: adapter/agent UID runAsUser, cred-readers GID in supplementalGroups/runAsGroup, any of runAsUser/runAsGroup/supplementalGroups absent).
- NetworkPolicy webhook count bumped from eight to nine in §13.2, matching the new webhook registry size.
- Metric labels `origin_pod`, `tampering_pod` consistent across spec §16.1, docs/reference/metrics.md, and the §16.5 alert body.
- Alert severities: `ElicitationContentTamperDetected` is Critical (correctly placed); `EphemeralContainerCredGuardUnavailable` and `AdmissionPlaneFeatureFlagDowngrade` are Warning (correctly placed).
- Audit event `elicitation.content_tamper_detected` payload fields enumerated once in §16.7 and cross-referenced from §9.2.
- Docs sync propagated to all four reference/guide documents with matching terminology.

**Result**: No regressions detected.
