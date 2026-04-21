# Perspective 2 — Security & Threat Modeling (iter5)

**Scope:** Verify iter4 SEC-008 through SEC-013 fixes; surface only NEW findings with concrete attack paths.
**Method:** Spot-check spec sections named in iter4 resolutions; evaluate against the iter4 severity rubric.
**Numbering:** SEC-017+ (iter4 ended at SEC-016; SEC-014–SEC-016 absent from iter4 SEC section).

---

## 1. Iter4 finding verification

### SEC-008 — Upload security controls (zip-bomb / symlink / traversal) [High — Fixed]

**Verified.** `spec/07_session-lifecycle.md` §7.4 now encodes every normative validator the iter4 resolution promised: 256 MiB decompressed cap, 100:1 ratio cap, 10 000 entries, 64 MiB per-entry cap, 32 path depth, 4 096 B path length, zip-slip canonicalization, outright rejection of `hardlink`/`character-device`/`block-device`/`FIFO`/`socket`, symlink blocklist for `/proc`, `/sys`, `/dev`, `/run/lenny`. `UPLOAD_ARCHIVE_LIMIT_EXCEEDED` with all nine `details.reason` sub-codes is in §15.1 (line 1072) and the §13.4 summary cross-references §7.4, §8.7, §15.1, §16.1. §13.5 §13.4 list at lines 657-666 mirrors the normative ceilings. No residual gap.

### SEC-009 — Exported workspace files bypass `contentPolicy.interceptorRef` [High — Deferred]

**Status unchanged (still deferred pending user input).** §13.5 "Residual risk — file export content" (line 681) still explicitly documents the gap and points to §8.7 for deployer-side mitigations. The five open questions from iter4 remain unanswered. This is an acknowledged architectural gap, not a regression. No iter5 action possible without user direction on question (a).

### SEC-010 — Trust-based chained-interceptor exception [High — Fixed iter4]

**Verified.** `spec/08_recursive-delegation.md` §8.3 interceptorRef list (lines 131-136) shows the four surviving conditions with the "chained interceptor (trust-based)" option removed. Condition 4 (`CONTENT_POLICY_INTERCEPTOR_SUBSTITUTION`) unconditionally rejects different non-null references and explicitly states out-of-band chaining claims are not accepted; deployers needing composition must keep `interceptorRef` identical across the boundary. Identity-based monotonicity is restored.

### SEC-011 — `lenny-cred-readers` group scope [Medium — Fixed iter4]

**Verified.** §13.1 "`lenny-cred-readers` membership boundary" (line 27) enumerates the two UIDs (adapter + agent), rejects non-adapter/non-agent containers at admission via `POD_SPEC_CRED_GROUP_OVERBROAD`, and documents the subprocess `setgroups(0, NULL)` advisory. §13.1 "Concurrent-workspace mode credential-read scope" (line 29) explicitly folds cross-slot credential readability into the existing `acknowledgeProcessLevelIsolation` deployer flag and emits the `ConcurrentWorkspaceCredentialSharing=True` warning condition. §5.2 pool-validation rejection message (line 494) now lists "shared credential-file group-read access" alongside the other four co-tenancy properties. Scope is narrower than the recommendation (no per-slot GIDs, no AppArmor/Seccomp mandate) but the resolution note explicitly justifies this as commensurate with the already-accepted co-tenancy posture — fix is adequate against the stated threat model.

### SEC-012 — Admin-time RBAC live-probe caller identity [Medium — Fixed iter4]

**Verified.** §4.9 "Admin-time RBAC live-probe (required)" (line 1209) specifies the Token-Service-owned probe over mTLS, the Token Service's `SelfSubjectAccessReview`+`get` sequence, the `{ALLOWED, DENIED, NOT_FOUND}` return set, explicit forbiddance of (a) gateway impersonation via `TokenRequest`/`Impersonate-*` and (b) gateway-SA `SelfSubjectAccessReview`, mapping of `DENIED`/`NOT_FOUND` to 422 `CREDENTIAL_SECRET_RBAC_MISSING`, and the new 503 `CREDENTIAL_PROBE_UNAVAILABLE` (line 984 in §15.1) for probe-transport failures. The handler MUST NOT fail-open on probe errors. Bootstrap-seeded pools are correctly excluded (atomic RBAC rendering). No residual gap.

### SEC-013 — Interceptor weakening cooldown timestamp immutability [Medium — Fixed iter4]

**Verified.** §8.3 rule 5 (line 168) now states `transition_ts` is server-minted from the gateway's monotonic clock-synchronized wall clock, client-supplied values are rejected with `INTERCEPTOR_COOLDOWN_IMMUTABLE` (line 1003 in §15.1) before any state change persists, the cooldown duration is cluster-scoped (Helm value, not admin-API field), a meta-cooldown preserves each pending cooldown against cluster-config reductions, and rejected `INTERCEPTOR_COOLDOWN_IMMUTABLE` attempts are audited. The hash-chained `interceptor.fail_policy_weakened` event in §11.7 carries writer identity and transition metadata. The `interceptors:policy-admin` role split was deliberately dropped as unnecessary once the cooldown moved to cluster-config domain. Fix aligns with the stated threat model (compromised `interceptors:write` credential cannot collapse the cooldown).

---

## 2. New findings

**None.** After spot-checking every iter4 SEC fix and re-examining the surrounding code paths (concurrent-workspace credential sharing, probe transport semantics, cooldown oscillation, archive extraction location), no new concrete attack paths were identified that would warrant Medium or higher severity under the iter5 calibration rubric. The residual subprocess-`setgroups` advisory in §13.1 (runtime-author responsibility, not platform-enforced) and the file-export scanning gap (SEC-009, still deferred) are the known outstanding items — both pre-existing, both explicitly scoped as acceptable-with-acknowledgment or deferred-pending-input. Neither has a new attack path that wasn't already captured.

Theoretical defense-in-depth polish considered and rejected as Low/Info (per calibration rule — no concrete attack path):

- Platform could validate runtime-author `setgroups(0, NULL)` enforcement via container image scanner → already covered by the §13.1 single-tenant trust-boundary framing.
- `INTERCEPTOR_COOLDOWN_IMMUTABLE` rejection audit event could be categorized separately from the generic admin-API denial audit → cosmetic, not security-material.
- The probe `details` field listing every failing `resourceName` for batch pool creation marginally expands the RBAC-structure signal a compromised admin credential learns. Materially no new information beyond what `kubectl auth can-i` would already reveal under the same credential; not a new attack path.

---

## 3. Convergence assessment

**Iter4 SEC findings status after iter5 verification:**
- Fixed & verified: 5 (SEC-008, SEC-010, SEC-011, SEC-012, SEC-013)
- Deferred pending user input: 1 (SEC-009)
- New iter5 findings: 0 Critical, 0 High, 0 Medium, 0 Low/Info

**Convergence: YES** for the Security & Threat Modeling perspective. All actionable iter4 findings are fixed; the single outstanding deferral (SEC-009) is gated on user-level architectural direction and cannot progress without that input, which is the documented project convention (`feedback_proposal_before_edit.md`).
