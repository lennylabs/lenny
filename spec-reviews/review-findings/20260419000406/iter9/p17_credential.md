## CRD iter9 review — regressions from iter8 fix commit `df0e675`

**Scope:** credential-lifecycle surfaces touched by iter8 fix pass (§13.1 cred-guard fourth rejection condition, §17.2 admission-policies inventory item 13, §15.1 `EPHEMERAL_CONTAINER_CRED_UID_FORBIDDEN`, paired docs in `docs/reference/error-catalog.md`, runbook `docs/runbooks/ephemeral-container-cred-guard-unavailable.md`, `docs/operator-guide/namespace-and-isolation.md` item 8).

### CRD-030 `EPHEMERAL_CONTAINER_CRED_UID_FORBIDDEN` sub-reason enumeration and retry guidance do not reflect condition (iv) added to §13.1 [Medium]

**Section:** 15.1 (error row `EPHEMERAL_CONTAINER_CRED_UID_FORBIDDEN`); `docs/reference/error-catalog.md` paired row.

The iter8 CRD-029 fix added a fourth rejection condition to the `lenny-ephemeral-container-cred-guard` ValidatingAdmissionWebhook in `spec/13_security-model.md` §13.1: condition (iv) rejects ephemeral containers whose `volumeMounts` reference the credential tmpfs volume by name, or whose `mountPath` equals `/run/lenny` or begins with `/run/lenny/`. `spec/17_deployment-topology.md` §17.2 item 13 was updated in the same commit to enumerate four conditions, and `docs/operator-guide/namespace-and-isolation.md` item 8 correctly describes four conditions.

However, the paired error-catalog entries on the REST surface were not updated:

- `spec/15_external-api-surface.md` line 1091 enumerates the `details.reason` sub-code as one of seven values covering only conditions (i)–(iii): `runAsUser_equals_adapter_uid`, `runAsUser_equals_agent_uid`, `cred_readers_gid_in_supplementalGroups`, `cred_readers_gid_in_runAsGroup`, `runAsUser_absent`, `runAsGroup_absent`, `supplementalGroups_absent`. There is no sub-code for the condition-(iv) volumeMount rejection path.
- The same row's retry guidance — "Not retryable as-is — the caller must submit an ephemeral container whose `securityContext` explicitly sets `runAsUser`/`runAsGroup`/`supplementalGroups` to values outside the adapter UID, agent UID, and `lenny-cred-readers` GID" — does not instruct the caller to omit the credential-volume mount or the `/run/lenny` mountPath, so a well-intentioned operator following this guidance after a condition-(iv) rejection would repeatedly retry and receive the same rejection.
- `docs/reference/error-catalog.md` line 123 carries the identical seven-sub-code enumeration and incomplete retry guidance.

Because condition (iv) is the operative closure for the fsGroup side-channel (per §13.1's "Relationship among the four conditions" paragraph), it is the most likely rejection path for any sophisticated bypass attempt, yet it has no discriminable sub-code in the operator-facing error payload and no remediation hint in the catalog prose.

**Recommendation:** In both `spec/15_external-api-surface.md` line 1091 and `docs/reference/error-catalog.md` line 123: (a) extend the `details.reason` sub-code enumeration with at least one value covering condition (iv) — suggest `credential_volume_mounted` (matches the volume-name branch) plus `run_lenny_path_mounted` (matches the mountPath-prefix branch), or a single `credential_volume_or_path_mounted` if the operator experience is served equally by a combined code; whichever is chosen, both branches of condition (iv) should be reachable from the sub-code or `details` payload; (b) extend the retry guidance to state that the caller must additionally ensure the ephemeral container's `volumeMounts` do not reference the pod-level credential tmpfs volume and contain no entry whose `mountPath` equals `/run/lenny` or begins with `/run/lenny/`. Cross-reference `spec/13_security-model.md` §13.1 condition (iv) in both surfaces so the four-condition taxonomy is consistent across the code path rejection, the spec narrative, and the operator-facing error catalog.

---

## Perspectives with no CRD-scope regressions

The following iter8 surfaces touching credential-lifecycle concerns were inspected and passed:

- §13.1 "`lenny-cred-readers` membership boundary" — four-condition narrative is internally coherent; condition (iv) correctly explains the fsGroup side-channel closure; cross-references to §15.1 (error code) and §16.5 (`EphemeralContainerCredGuardUnavailable` alert) and §17.2 (admission-policies item 13) are intact.
- §17.2 admission-policies inventory item 13 — now enumerates four conditions consistent with §13.1.
- `docs/runbooks/ephemeral-container-cred-guard-unavailable.md` — correctly references "four rejection conditions the webhook enforces" (line 34) and points at §13.1.
- `docs/operator-guide/namespace-and-isolation.md` item 8 — four conditions accurately described; no drift from §13.1.

No additional CRD-scope regressions detected.
