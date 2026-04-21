# Perspective 2 — Security & Threat Modeling (SEC) — Iteration 7

**Scope:** iter7 security re-review after iter6 rate-limit deferral; iter5 closed with `Convergence: YES` on all actionable items (5 fixed, 1 deferred pending user direction). Confirm iter5-verified fixes remain intact and surface any NEW concrete attack paths introduced or newly observable.

**Method:** (a) revisit every iter4 SEC resolution that iter5 marked "Fixed" to confirm no regression in iter5/iter6 patches; (b) re-examine the adapter↔agent boundary, delegation monotonicity, upload safety, elicitation chain, tracing-context propagation, and the credential-lease path for new attack paths; (c) spot-check cross-perspective iter6 findings for SEC spillover (none identified — iter6 summary confirms no SEC-category findings in the reviewed P11–P25 cohort).

**Numbering:** SEC-017+ (iter5 declared SEC-014–SEC-016 absent and added 0 new findings; iter6 was deferred and added nothing).

**Severity calibration:** anchored to the iter1–iter5 rubric per `feedback_severity_calibration_iter5.md`. Medium requires a concrete attack path; Low covers defense-in-depth gaps without a direct bypass; Info is spec-clarity only.

---

## 1. Prior-iteration carry-forwards

### SEC-008 — Upload security controls (zip-bomb / symlink / traversal) [High — Fixed iter4; verified iter5]

**Re-verified iter7.** `spec/07_session-lifecycle.md` §7.4 still encodes every normative validator; `spec/13_security-model.md` §13.4 still mirrors the ceilings; `UPLOAD_ARCHIVE_LIMIT_EXCEEDED` with the nine `details.reason` sub-codes is present in `spec/15_external-api-surface.md` §15.1. iter5/iter6 fix commits did not touch §7.4 or §13.4 in ways that would regress this fix. No new gap.

### SEC-009 — Exported workspace files bypass `contentPolicy.interceptorRef` [High — Deferred]

**Status unchanged.** `spec/08_recursive-delegation.md` §8.7 "Security note — exported files are untrusted input" (line 725) still documents the gap and points to the deployer-side mitigation (workspace-plan `inlineFile` interceptor). Exported files remain outside `contentPolicy.interceptorRef` scope — the known architectural gap flagged in iter4 and re-confirmed iter5. Still deferred pending user decision on question (a) from iter4. No iter7 action possible without user direction.

### SEC-010 — Trust-based chained-interceptor exception [High — Fixed iter4; verified iter5]

**Re-verified iter7.** §8.3 interceptorRef condition list still has the four surviving conditions (trust-based chained exception removed). `CONTENT_POLICY_INTERCEPTOR_SUBSTITUTION` unconditionally rejects non-identical non-null references. Identity-based monotonicity intact.

### SEC-011 — `lenny-cred-readers` group scope [Medium — Fixed iter4; verified iter5]

**Re-verified iter7.** `spec/13_security-model.md` §13.1 still enumerates the two authorized UIDs (adapter + agent), the `POD_SPEC_CRED_GROUP_OVERBROAD` admission rejection, the subprocess `setgroups(0, NULL)` advisory, and the `ConcurrentWorkspaceCredentialSharing=True` warning condition for concurrent-workspace pools. Scope is narrower than the iter4 recommendation (no per-slot GIDs) but the resolution explicitly justifies this as commensurate with the co-tenancy posture already accepted via `acknowledgeProcessLevelIsolation`. No regression. **However:** the §13.1 claim about ephemeral debug containers is incomplete — see SEC-017 (new).

### SEC-012 — Admin-time RBAC live-probe caller identity [Medium — Fixed iter4; verified iter5]

**Re-verified iter7.** §4.9 still names the Token-Service-owned probe over mTLS, the `SelfSubjectAccessReview`+`get` sequence, the `{ALLOWED, DENIED, NOT_FOUND}` return set, forbiddance of gateway impersonation and `SelfSubjectAccessReview` via the gateway SA, `CREDENTIAL_SECRET_RBAC_MISSING` (422) and `CREDENTIAL_PROBE_UNAVAILABLE` (503) error-code mapping, and the no-fail-open handler semantics. Bootstrap-seeded pools remain excluded. No residual gap.

### SEC-013 — Interceptor weakening cooldown timestamp immutability [Medium — Fixed iter4; verified iter5]

**Re-verified iter7.** §8.3 rule 5 still states `transition_ts` is server-minted from the gateway's monotonic clock, client-supplied values rejected with `INTERCEPTOR_COOLDOWN_IMMUTABLE`, cooldown duration is cluster-scoped (Helm value), a meta-cooldown preserves pending cooldowns against cluster-config reductions, rejected attempts are audited, and the hash-chained `interceptor.fail_policy_weakened` event carries writer identity. No regression.

---

## 2. New findings (iter7)

### SEC-017. Ephemeral debug containers can acquire the agent UID and `lenny-cred-readers` GID [Medium]

**Section:** `spec/13_security-model.md` §13.1 "`lenny-cred-readers` membership boundary" (the paragraph at line 27 that describes ephemeral debug containers); `spec/17_deployment-topology.md` §17.2 admission-policies inventory (the 12-item list at lines 40–53).

**Description.** §13.1 asserts: _"Ephemeral debug containers attached post-hoc via `kubectl debug` inherit only the pod's `fsGroup` (which is required) but are pinned to a separate `runAsUser` outside the adapter/agent UIDs by the pod's `securityContext` defaults; they do not acquire group-read on the credential file."_ This assertion is not enforced by any admission control the spec actually ships.

Concrete attack path:
1. A Kubernetes ephemeral container is added to a running pod via the `pods/ephemeralcontainers` subresource (`kubectl debug --target=<agent> --image=<attacker> --set-image=...`). An actor with `update` on `pods/ephemeralcontainers` in a Lenny agent namespace (e.g., a cluster operator whose RBAC was not scoped away from agent namespaces, a compromised SRE tooling SA, or a supply-chain attack via an operator that legitimately holds that verb) can create an ephemeral container with its own `securityContext`.
2. `EphemeralContainer.securityContext.runAsUser` is honored by the kubelet **in preference to** pod-level `securityContext.runAsUser` when set. The admission-policies inventory in §17.2 lists 12 gates: `POD_SPEC_HOST_SHARING_FORBIDDEN`, RuntimeClass-aware PSS, label-immutability, direct-mode-isolation, sandboxclaim-guard, data-residency-validator, pool-config-validator, t4-node-isolation, drain-readiness, CRD conversion, plus the two OPA/Gatekeeper PSS webhooks. **None** of these are scoped to `pods/ephemeralcontainers` operations, and none validates that an ephemeral container's `runAsUser`/`supplementalGroups` does not equal the adapter UID or the agent UID, nor that the ephemeral container does not declare the `lenny-cred-readers` GID.
3. Kubernetes PSS "restricted" (the reference model `POD_SPEC_HOST_SHARING_FORBIDDEN` and the RuntimeClass-aware admission relies on) forbids `runAsUser: 0` and certain non-root-run-as-user combinations, but it does **not** forbid reuse of a pod's existing non-root UID, and it does not restrict `supplementalGroups` membership. An attacker can choose `runAsUser: <agentUID>` and `supplementalGroups: [<lenny-cred-readers GID>]` — both values the attacker can discover by reading the live pod spec before creating the ephemeral container.
4. Because the credential tmpfs is mounted with `fsGroup: <lenny-cred-readers GID>` (§13.1 Cross-UID file delivery), and the file is mode `0440` (group-readable), and ephemeral containers mount all of the target pod's volumes by default, the ephemeral container can read `/run/lenny/credentials.json` and extract the active lease token (direct mode) or the proxy URL + lease ID (proxy mode). This bypasses the §13.1 "credential-file read boundary" invariant.

The adapter-agent boundary documented elsewhere in §4.7 and §13.1 (separate UIDs, nonce handshake, HMAC challenge-response, `SO_PEERCRED` self-test) does not defend against this: those controls govern the socket boundary. The filesystem credential delivery path relies entirely on `fsGroup` + `lenny-cred-readers` membership, which ephemeral containers can spoof.

**Recommendation.** Add a dedicated `ValidatingAdmissionWebhook` scoped to `pods/ephemeralcontainers` in Lenny agent namespaces (or an OPA/Gatekeeper/Kyverno ConstraintTemplate with equivalent rule) that rejects any ephemeral container request where:
1. `container.securityContext.runAsUser` equals the adapter UID or agent UID declared on the target pod, OR
2. `container.securityContext.supplementalGroups` (or `runAsGroup`) includes the `lenny-cred-readers` GID, OR
3. Any of the `runAsUser`/`runAsGroup`/`supplementalGroups` fields is absent (so the ephemeral container would inherit pod-level defaults — which include `lenny-cred-readers` via `fsGroup` semantics for mounted volumes).

Failure-mode should be fail-closed with a new error code (e.g., `EPHEMERAL_CONTAINER_CRED_UID_FORBIDDEN`). Add a matching unavailability alert per the §17.2 "High-availability contract applies to every rendered webhook" requirement. Update the §13.1 claim to reference the new webhook rather than asserting a Kubernetes-default behavior that does not exist.

**Severity justification:** Medium — concrete exploitable path with no in-spec mitigation; gated on `pods/ephemeralcontainers` RBAC which Lenny does not itself grant but which is commonly held by operator/SRE identities in multi-tenant clusters. Calibration anchor: SEC-011 (cred-readers overbroad) was Medium; this is structurally the same credential-boundary-bypass class with a different attacker vector.

---

### SEC-018. Elicitation text integrity is not preserved across intermediate chain hops [Medium]

**Section:** `spec/09_mcp-integration.md` §9.2 "Elicitation Chain" (the MCP hop-by-hop model at lines 42–54 and the provenance table at lines 56–68).

**Description.** §9.2 establishes that elicitations flow `External Tool → Gateway connector → Child pod → Parent pod → Gateway edge → Client / Human`, and that _"The gateway mediates every hop but **does not erase the hop structure**."_ Provenance metadata (`initiator_type` ∈ {`connector`, `agent`}, `origin_pod`, `delegation_depth`, `origin_runtime`, `purpose`) is attached by the gateway at origination.

The spec does **not** define any content-integrity mechanism over the elicitation text/title/description that the human user ultimately sees. At each hop, the receiving agent pod is the one that re-emits the elicitation upstream — it is free to alter the `title`, `description`, `schema`, `inputs`, or any other user-visible field before forwarding. The client UI displays the final hop's text alongside the `initiator_type` label, but `initiator_type` is a single enum (`connector` | `agent`) — it does not identify which agent in a multi-hop chain authored the wording that the user sees. There is no signature, hash, or deterministic canonicalization binding the displayed text to `origin_pod`.

Concrete attack path:
1. A child pod running at depth 2 (three-hop chain: `child → parent → gateway → user`) legitimately requests an elicitation with text _"Confirm: delete file `./scratch.txt`?"_.
2. The parent pod (compromised by prompt injection from a malicious tool output, or by a second-party runtime library the user doesn't fully trust) rewrites the elicitation text to _"Confirm: delete the entire workspace?"_ before emitting it upstream. The gateway forwards the rewritten elicitation with `initiator_type: agent` and `origin_pod: <child>`.
3. The user sees an `agent`-initiated elicitation (already a lower-trust indicator than `connector`) with the rewritten text. `origin_pod` is displayed but the rewriting parent's identity is not — the human cannot distinguish _"this child asked me to delete `scratch.txt`"_ from _"this child is claiming to ask me to delete the workspace"_ without comparing against the child's own audit log.

The §7.4 "archive validation" path is irrelevant here (this is text, not files); the `contentPolicy.interceptorRef` path does not apply (interceptors cover `TaskSpec.input`, per SEC-009, and do not cover elicitations); the `deep elicitation suppression` rule (`depth ≥ 3`) reduces but does not eliminate the attack (two-hop chains at depth 0–2 are still exposed, including the common `user → root agent → delegated child` pattern).

The rate-limit-free `respond_to_elicitation` authorization check at §9.2 (lines 94) validates `(session_id, user_id, elicitation_id)` — it ensures responses route correctly but does nothing for the forward content-integrity direction.

**Recommendation.** Add one of the following content-integrity mechanisms at §9.2:
1. **Hop-attested text (preferred).** Each hop in the elicitation chain signs a canonicalized hash of `{elicitation_id, title, description, schema, inputs, origin_pod}` with a per-pod ephemeral key registered at the gateway during pod startup; the gateway includes every hop's signature chain in the delivered elicitation and the client UI renders the chain. A mismatch between the signing pod and the declared `origin_pod` at any hop is flagged as `ELICITATION_CONTENT_TAMPERED` and the elicitation is dropped.
2. **Gateway-origin binding (simpler).** The gateway stores the original elicitation content at origination time (keyed by `elicitation_id`) and requires each intermediate hop to forward only `elicitation_id` + a minimal forwarding envelope — not the text. The final render to the client is driven by the gateway's stored original, so intermediate pods have no textual surface to modify.
3. **Explicit "rewritten by" provenance.** Each hop that wishes to modify elicitation text must emit its own `lenny/request_elicitation` (creating a new `elicitation_id`); forwarding is opaque-only. The client UI renders the full chain of elicitation-ids and their respective `origin_pod` values, so the user sees that pod X asked Y and pod A claims Y means Z.

Options (1) and (2) are backward-compatible with the `initiator_type` and `origin_pod` metadata; option (3) is a larger surface change.

**Severity justification:** Medium — concrete attack path exploiting an unstated trust assumption in the elicitation chain. Calibration anchor: SEC-010 (trust-based chained interceptor) was High because it allowed policy weakening; this is Medium because the end effect is UX deception rather than direct policy/credential bypass.

---

### SEC-019. `SO_PEERCRED` startup self-test does not assert the adapter UID is non-zero [Low]

**Section:** `spec/04_system-components.md` §4.7 "Mandatory `SO_PEERCRED` startup self-test (adapter prerequisite)" (lines 870–877).

**Description.** Step 3 of the self-test (line 873): _"The adapter calls `getsockopt(SO_PEERCRED)` on the accepted connection and asserts that the returned `uid` matches `os.Getuid()` (the adapter's own UID)."_ The check validates that `SO_PEERCRED` is functional, but it does not assert `os.Getuid() != 0`. If the pod is misconfigured such that the adapter container runs as UID 0 (root):
- `os.Getuid() == 0`,
- `SO_PEERCRED` returns `uid: 0` for the loopback connection from the adapter to itself,
- the two values match, and the self-test passes.

The admission-plane is the primary defense against UID-0 containers: PSS "restricted" (warn+audit at namespace level, enforce via the RuntimeClass-aware `lenny-direct-mode-isolation` / OPA-Gatekeeper constraint templates at §17.2) forbids `runAsNonRoot: false` and plain `runAsUser: 0`. But the admission plane is fail-closed **only** if the required webhooks are deployed and healthy — during a preflight-bypass install, a chart-author omission (SEC-017 shows a parallel gap on a different subresource), or a fail-closed outage that is worked around with emergency `--validate=false` operator tooling, a UID-0 adapter could be admitted. In that degraded state the `SO_PEERCRED` self-test should be a defense-in-depth last line of defense against mis-identification, but with no non-zero assertion, it is not.

If an attacker with cluster-admin (or compromised operator SA) can temporarily suspend admission and launch a UID-0 adapter, the self-test passes, the adapter reports READY, the agent connects, and `SO_PEERCRED` reports `uid: 0` on the agent socket as well (because the agent container shares the user namespace) — the adapter's `expected UID match` check succeeds without distinguishing between a legitimately-UIDed agent and a UID-0 agent. The boundary becomes "anyone with UID 0 in this namespace" instead of "the designated agent UID."

**Recommendation.** Extend the self-test step 3 to assert: _"the returned `uid` matches `os.Getuid()` **and** is non-zero **and** is not the agent UID"_ (the latter because an agent UID equal to the adapter UID is also a misconfiguration that collapses the boundary). Emit `lenny_adapter_sopeercred_selftest_failed_total` with a `reason` label (`uid_mismatch`, `uid_zero`, `uid_collision_with_agent`, `syscall_error`) so operators can disambiguate the failure cause. Keep the `adapter.requireSoPeercred: false` escape hatch intact for gVisor divergence (nonce-only mode) but assert the non-zero check even in nonce-only mode — the UID-0 check costs one comparison and has no dependence on `SO_PEERCRED` being functional.

**Severity justification:** Low — defense-in-depth gap only exploitable in admission-degraded states; not a Medium because the primary admission control (PSS restricted + RuntimeClass-aware webhooks) is functional in normal operation. Calibration anchor: iter5 theoretical-only defense-in-depth polishes were classified as Low/Info; this is a concrete (if admission-gated) hole, landing at Low.

---

### SEC-020. `tracingContext` value blocklist does not cover content-exfiltration text [Low]

**Section:** `spec/08_recursive-delegation.md` §8.3 "`tracingContext` validation (gateway-enforced)" (the table at lines 238–245 and the audit/data-lifecycle paragraph at line 249).

**Description.** The validation table defines: max serialized size 4 KB, max key length 128 B, max value length 256 B, max entries 32, a **key-name** case-insensitive blocklist matching `*secret*`, `*token*`, `*password*`, `*key*`, `*credential*`, `*authorization*`, and a **value** URL blocklist (`http://` or `https://` prefixes rejected). Audit events log keys only (values redacted).

The gap is that both blocklists are for exfiltration of **secrets the parent has access to** and **redirection of tracing endpoints**. They do not address a parent runtime using `tracingContext` as a **content-exfiltration channel** for session content itself — e.g., the parent can set:
```
tracingContext = {
  "debug.q_01": "<first 256 bytes of user prompt>",
  "debug.q_02": "<next 256 bytes>",
  ...
  "debug.q_15": "<up to entry 32>",
}
```
with key names that pass the sensitive-name regex (none of `q_01..q_15` match `*secret*|*token*|…`). Per §8.3, `tracingContext` is forwarded to every child runtime via its adapter manifest AND to the deployer-configured tracing backend (Lenny does not see or filter the tracing endpoint URLs — §8.3 explicitly documents that endpoint URLs are deployer config, not parent-controlled, which is a different control axis from this one).

The effective channel bandwidth is bounded: 32 entries × 256 B ≈ 8 KB per session (4 KB serialized cap is the true limit). Over a long-running session with N delegations, the parent can register fresh values on each delegation hop, effectively N × 4 KB to the same tracing backend.

Note: the audit-log-keys-only redaction protects the audit plane but does not constrain forwarding to the tracing backend. Tracing backends are typically deployer-trusted but not session-content-authorized — e.g., a LangSmith or Datadog trace dashboard may be accessible to a broader team than the session's data classification allows.

**Recommendation.** One of:
1. Extend the key-name blocklist to cover content-exfiltration name patterns: `*message*`, `*prompt*`, `*payload*`, `*content*`, `*response*`, `*summary*`, `*text*`, `*q_*`, `*question*`, `*answer*` (case-insensitive). Reject with the existing `TRACING_CONTEXT_SENSITIVE_KEY` error.
2. Add a **value-shape** check: reject tracing values that exceed an entropy or length threshold likely to carry natural-language content (e.g., reject values > 64 B that are not matching a typical trace-identifier shape — hex, UUID, short alphanumeric). Tighten by context: trace-ID and run-ID values are typically < 64 characters and alphanumeric; anything longer or containing spaces/punctuation is likely content.
3. Document explicitly in §8.3 that `tracingContext` values are a deliberate, bounded forwarding channel to a deployer-trusted backend, and require that the tracing backend be treated as "in scope for the session's data classification" for audit/compliance purposes. This is the lightest change and aligns with the existing deferred posture for SEC-009.

Option (3) alone is acceptable if the threat model treats tracing backends as same-scope-as-session. Options (1)+(2) are recommended if the threat model treats tracing backends as a wider audience than the session's data-class.

**Severity justification:** Low — bounded exfiltration (~8 KB per delegation hop), deployer-trusted endpoint, already audit-logged at key granularity. Not a Medium because it requires a compromised parent runtime (already assumed hostile in §13.5 threat model) AND a mis-scoped tracing backend (deployer configuration responsibility). Calibration anchor: iter5's CRD-015 adjacent data-exposure concerns were Low when mitigation was in deployer scope.

---

### SEC-021. `expected_domain` `*.suffix` wildcard matching semantics are under-specified [Info]

**Section:** `spec/09_mcp-integration.md` §9.2 URL-mode elicitation controls (line 72: _"exact match or `*.suffix` wildcard"_) and the `expected_domain` row in the provenance table (line 65).

**Description.** §9.2 requires a `domainAllowlist` array for agent-initiated URL-mode elicitation and validates URL hosts against it via _"exact match or `*.suffix` wildcard"_. The same matching style is referenced for connector `expected_domain` at line 73 (_"Wildcards and subdomain matching follow the connector's registered domain policy"_). Neither location formally defines:
1. Does `*.example.com` match the apex `example.com` (bare hostname with no leading label)?
2. Does `*.example.com` match a multi-label subdomain `a.b.example.com`, or only single-label `a.example.com`?
3. Is matching case-insensitive on the domain portion (per RFC 1035)?
4. Is IDN / Punycode canonicalization applied before matching (otherwise `*.éxample.com` and `*.xn--xample-hva.com` diverge)?
5. Are IP-literal hosts (`https://10.0.0.1/`) compared literally or rejected outright?

Depending on the implementation choice, either legitimate OAuth flows may be unexpectedly rejected (strict interpretation: `*.example.com` does not match apex) or malicious URLs may slip through (loose interpretation: `*.example.com` matches `attacker.evil.example.com.attacker.com`).

**Recommendation.** Add a normative sub-table to §9.2 specifying, for `*.suffix` matching:
- Apex coverage: `*.example.com` does / does not match `example.com` (choose one — common OAuth convention is that the wildcard does **not** match apex; e.g., TLS cert semantics).
- Multi-label coverage: `*.example.com` matches exactly one label (`a.example.com`) but not multi-label (`a.b.example.com`) — align with TLS wildcard semantics (RFC 6125).
- Case folding: ASCII-case-insensitive per RFC 1035.
- IDN canonicalization: convert the URL's host to A-label (Punycode) before matching; reject mixed-script inputs per UTS 46 nontransitional processing.
- IP-literal hosts: rejected outright unless explicitly listed by CIDR in a separate allowlist (not covered by `domainAllowlist`).

Also reference this rule from §15.1 `DOMAIN_NOT_ALLOWLISTED` so error messages can include the reason (`apex-not-matched`, `multi-label-rejected`, `idn-canonicalization-failed`, `ip-literal-forbidden`).

**Severity justification:** Info — spec-clarity finding, not a concrete vulnerability at current spec text (the ambiguity exists, but whether it becomes exploitable depends on implementation choice). Calibration anchor: iter5 "theoretical defense-in-depth polish considered and rejected as Low/Info" class — this is Info because there is no concrete attack path; the implementor's choice determines the security property.

---

## 3. Convergence assessment

**Prior-iteration SEC status after iter7 verification:**
- SEC-008 [High]: Fixed iter4, re-verified iter5 and iter7 — no regression
- SEC-009 [High]: Deferred pending user direction — unchanged
- SEC-010 [High]: Fixed iter4, re-verified — no regression
- SEC-011 [Medium]: Fixed iter4, re-verified — no regression on its original scope, but see SEC-017 which surfaces an adjacent gap (ephemeral-container channel) that SEC-011's admission rule does not cover
- SEC-012 [Medium]: Fixed iter4, re-verified — no regression
- SEC-013 [Medium]: Fixed iter4, re-verified — no regression

**New iter7 findings:**
- 0 Critical
- 2 Medium (SEC-017, SEC-018)
- 2 Low (SEC-019, SEC-020)
- 1 Info (SEC-021)

**Convergence: NO** for the Security & Threat Modeling perspective.

Rationale: SEC-017 is a concrete credential-boundary-bypass attack path with no in-spec mitigation (Medium), and SEC-018 is a concrete UX-deception attack path on the elicitation chain (Medium). Both require spec additions (new admission webhook scope for SEC-017; content-integrity mechanism for SEC-018). Low/Info findings can be addressed incrementally, but the two Medium findings should be fixed or explicitly deferred-with-rationale before declaring SEC convergence.

The iter5 deferral (SEC-009) remains unchanged and still depends on user-level architectural direction, which is the documented project convention per `feedback_proposal_before_edit.md`.
