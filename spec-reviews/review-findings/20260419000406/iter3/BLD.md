# Iter3 BLD Review

Prior findings verified:

- **BLD-001, BLD-002, BLD-003 (iter1)**: Confirmed fixed in iter2; no regression.
- **BLD-004 (iter2)**: Phase 0 row still mixes resolved artifacts (MIT LICENSE committed) with the remaining ADR-007 gate under the "Pre-implementation gating decisions" heading. Iter2 commit `2a46fb6` did not touch `18_build-sequence.md`, so BLD-004 remains open as filed.

Iter2 regression re-check: commit `2a46fb6` modified no content in `spec/18_build-sequence.md`. Several iter2 content changes elsewhere introduce new sequencing obligations that Phase 18 does not yet reflect.

---

### BLD-005 Phase 3.5 admission-policy deployment list is now incomplete against §17.2's 12-item enumeration [HIGH]

**Files:** `spec/18_build-sequence.md:14` (Phase 3.5), `spec/17_deployment-topology.md:40-57` (§17.2)

Iter2's K8S-037 fix expanded §17.2 into a canonical 12-item enumeration of admission webhooks / policies shipped by the Helm chart (`lenny-label-immutability`, `lenny-direct-mode-isolation`, `lenny-sandboxclaim-guard`, `lenny-data-residency-validator`, `lenny-pool-config-validator`, `lenny-t4-node-isolation`, `lenny-drain-readiness`, `lenny-crd-conversion`, plus the PSS policies). §17.2 also adds a **fail-closed `lenny-preflight` enumeration check** that verifies all expected webhooks are present in the cluster; any missing webhook fails the install/upgrade.

Phase 3.5 still names only `lenny-sandboxclaim-guard` by name and handwaves the rest as "admission policy deployment (RuntimeClass-aware PSS enforcement, `shareProcessNamespace` validation, [...])". Practical consequences:

1. A Phase 3.5 Helm install that renders only `sandboxclaim-guard` + PSS + `POD_SPEC_HOST_SHARING_FORBIDDEN` will fail the `lenny-preflight` fail-closed enumeration check because the other webhooks listed in §17.2 are absent.
2. Phase 3 brings up PoolScalingController (which writes `SandboxTemplate.spec` / `SandboxWarmPool.spec` via SSA). Without `lenny-pool-config-validator` deployed, Rule Set 1 (§4.6 semantic budget rules — tiered-cap vs `terminationGracePeriodSeconds` + BarrierAck floor) cannot reject invalid pool configurations authored by PoolScalingController itself, which is explicitly the point the iter2 K8S-036 fix was making ("These rules are NOT subject to the `userInfo`-based bypass [...] the PoolScalingController is rejected if it attempts to write a configuration that violates the preStop budget").
3. Phase 3 also introduces `lenny.dev/managed`/`lenny.dev/tenant-id`/`lenny.dev/state` label mutations; `lenny-label-immutability` is the webhook that enforces NET-003 transitions. Without it at Phase 3, a controller bug could regress label state undetected.
4. CRDs exist from Phase 1; `lenny-crd-conversion` is needed before the first CRD version bump occurs (Phase 3's `RuntimeUpgrade` state machine and Phase 10.5's upgrade strategy both exercise CRD schema evolution surfaces).

**Recommendation:** Rewrite Phase 3.5's admission-policy deliverable to enumerate the webhooks deployed at that phase and explicitly call out webhooks deferred to later phases. Concretely:

- Phase 3.5 deploys (entry-wise): the PSS policies (#1, #2), `POD_SPEC_HOST_SHARING_FORBIDDEN` (#3), label-namespace targeting (#4), `lenny-label-immutability` (#5), `lenny-sandboxclaim-guard` (#7), `lenny-pool-config-validator` (#9), and `lenny-crd-conversion` (#12) — the set needed once PoolScalingController (Phase 3) and pod-label mutations are live.
- Deferred to Phase 5.8: `lenny-direct-mode-isolation` (#6) — requires `deliveryMode: proxy` path to exist.
- Deferred to Phase 13 (compliance profile enforcement): `lenny-data-residency-validator` (#8) and `lenny-t4-node-isolation` (#10).
- Deferred to Phase 8 (checkpoint/resume, MinIO live): `lenny-drain-readiness` (#11).

Then explicitly note that the `lenny-preflight` fail-closed enumeration (§17.2) must be feature-gated on the phase under test — either by tagging each webhook in the chart with a `phase` marker or by running `lenny-preflight` in a "phase-aware" mode that matches its expected-webhook set to the deployed phase. Without that gate, §17.2's fail-closed preflight actively prevents the build from reaching Phase 5.8 / 8 / 13 because webhooks not yet in the chart will be reported as missing.

---

### BLD-006 Phase 4.5 bootstrap seed missing `global.noEnvironmentPolicy` after iter2 TNT fix [HIGH]

**Files:** `spec/18_build-sequence.md:23` (Phase 4.5), `spec/18_build-sequence.md:28` (access-gap note after Phase 5), `spec/10_gateway-internals.md:544-549` (§10.6 platform-vs-tenant split), `spec/11_policy-and-controls.md:13` (§11 access-path default policy)

Iter2 TNT-002/003/004 split the `noEnvironmentPolicy` handling into two asymmetric branches:

- **Platform-level omission** (Helm: `global.noEnvironmentPolicy` unset) is now a **FATAL startup error** — the gateway refuses to become Ready and emits `LENNY_CONFIG_MISSING{config_key=noEnvironmentPolicy, scope=platform}`.
- **Tenant-level omission** silently defaults to `deny-all`.

Phase 4.5's "Gateway loads config from Postgres" step is where the gateway first transitions into fully-configured-and-Ready mode. The build-sequence text at Phase 4.5 describes bootstrap seed / Helm Job / authentication but does not mention that `global.noEnvironmentPolicy` is a **hard startup-configuration key** that must appear in the Helm values before the gateway can become Ready. The existing "access gap" note after Phase 5 line 28 only discusses the tenant-level `noEnvironmentPolicy: allow-all` on the default tenant — it does not tell the implementer that the platform-scope Helm value is a separate, fatal-if-missing key.

An AI implementer following Phase 4.5 literally would wire up the admin API + Postgres config load, attempt `helm install`, and the gateway would fail Readiness with `LENNY_CONFIG_MISSING`, potentially without understanding why.

**Recommendation:** Extend Phase 4.5's deliverable list with an explicit startup-configuration gate:

> **Startup-configuration keys (fatal-if-missing).** Phase 4.5 must ship Helm values covering the startup-configuration validation table in [§10.3](10_gateway-internals.md#103-mtls-pki). In particular, `global.noEnvironmentPolicy` MUST be set to `deny-all` (recommended default) or `allow-all` — the gateway emits `LENNY_CONFIG_MISSING{config_key=noEnvironmentPolicy, scope=platform}` and fails Readiness if the key is unset ([§10.6](10_gateway-internals.md#106-environment-resource-and-rbac-model)). This is orthogonal to the tenant-level `noEnvironmentPolicy` seeded for the default tenant (see the Phase 5 access-gap note below).

Also amend the existing Phase 5 access-gap note (line 28) to distinguish "Helm `global.noEnvironmentPolicy` platform key" from "bootstrap-seeded tenant-level `noEnvironmentPolicy`".

---

### BLD-007 Phase 5.8 LLM Proxy deliverables reference `lenny-direct-mode-isolation` as existing, but Phase 3.5 hasn't deployed it [MEDIUM]

**Files:** `spec/18_build-sequence.md:44` (Phase 5.8 item #9), `spec/18_build-sequence.md:14` (Phase 3.5)

Phase 5.8 deliverable (9) reads: "**Admission control enforcement** — `lenny-direct-mode-isolation` webhook blocks `deliveryMode: direct` + `isolationProfile: standard` when `tenancy.mode: multi`". This is worded as referencing an existing webhook, but Phase 3.5's admission policy deployment list does not include `lenny-direct-mode-isolation` (see BLD-005). Reading the build sequence strictly, the webhook appears for the first time in Phase 5.8, which is fine — but Phase 5.8 should say explicitly that it **deploys** the webhook at that point, not merely enforces via it.

**Recommendation:** Reword Phase 5.8 item (9) as "**Admission control enforcement** — deploy the `lenny-direct-mode-isolation` `ValidatingAdmissionWebhook` ([§17.2](17_deployment-topology.md#172-namespace-layout) entry #6) that blocks `deliveryMode: direct` + `isolationProfile: standard` when `tenancy.mode: multi`; this is the phase at which this webhook becomes part of the rendered Helm chart." Then in BLD-005's recommended Phase 3.5 rewrite, explicitly note that `lenny-direct-mode-isolation` is deferred to Phase 5.8. The combination closes the gap.

---

### BLD-008 Phase 13 compliance work does not enumerate `lenny-data-residency-validator` / `lenny-t4-node-isolation` webhook deployments [MEDIUM]

**Files:** `spec/18_build-sequence.md:63` (Phase 13), `spec/17_deployment-topology.md:49-51` (§17.2 webhook entries #8, #10)

Phase 13 says "compliance profile enforcement (SOC2/HIPAA/FedRAMP/NIS2/DORA retention presets) and durable audit." Iter2's K8S-037 adds two compliance-oriented webhooks to §17.2: `lenny-data-residency-validator` (#8, enforces `dataResidencyRegion` on tenant-scoped CRDs — [§12.8](12_storage-architecture.md#128-compliance-interfaces)) and `lenny-t4-node-isolation` (#10, enforces T4 Restricted dedicated-node placement — [§6.4](06_warm-pod-model.md#64-resource-limits-and-isolation)). Phase 13 does not name either. Without explicit sequencing, an implementer may treat these as "deploy with the rest of the chart" from Phase 3.5 (and trip the BLD-005 preflight failure) or as "deploy whenever" (and reach compliance-claim maturity without them).

**Recommendation:** Add to Phase 13 deliverables:

> Deploy the compliance-tier admission webhooks from [§17.2](17_deployment-topology.md#172-namespace-layout): `lenny-data-residency-validator` (entry #8) and `lenny-t4-node-isolation` (entry #10). Both are fail-closed (`failurePolicy: Fail`) with `replicas: 2` + `podDisruptionBudget.minAvailable: 1`. These webhooks MUST be rendered by the chart from Phase 13 onward; the `lenny-preflight` enumeration expects them from this phase forward.

---

### BLD-009 Phase 8 checkpoint/resume work does not enumerate `lenny-drain-readiness` deployment [LOW]

**Files:** `spec/18_build-sequence.md:48` (Phase 8), `spec/17_deployment-topology.md:52` (§17.2 webhook entry #11)

`lenny-drain-readiness` is a pre-drain MinIO health-check webhook (§17.2 entry #11) that blocks pod eviction when MinIO cannot accept checkpoint uploads (NET-037 in §13.2). Phase 8 is when checkpoint/resume + artifact seal-and-export land — i.e., when MinIO becomes load-bearing for session survival. Phase 8's current one-line description ("Checkpoint/resume + artifact seal-and-export") does not mention deploying the drain-readiness webhook.

**Recommendation:** Expand Phase 8 to:

> Phase 8: Checkpoint/resume + artifact seal-and-export. Deploy `lenny-drain-readiness` `ValidatingAdmissionWebhook` ([§17.2](17_deployment-topology.md#172-namespace-layout) entry #11) — pre-drain MinIO health check blocking pod eviction when MinIO is unavailable, preventing data loss during drain.

---

### BLD-010 Phase 1 wire-contract artifacts do not cover iter2 Shared Adapter Types / SessionEvent Kind Registry [LOW]

**Files:** `spec/18_build-sequence.md:8` (Phase 1), `spec/15_external-api-surface.md:160-329` (Shared Adapter Types + SessionEvent Kind Registry)

Iter2 PRT-005/006/007 added a normative **Shared Adapter Types** section in §15 ([§15.2](15_external-api-surface.md) lines 160+) and a **SessionEvent Kind Registry** (line 329+). These are normative Go types (`SessionMetadata`, `SessionEvent`, closed-enum `SessionEventKind`) that every external adapter (Phase 5's `ExternalAdapterRegistry` consumers and Phase 12b's `type: mcp` runtimes) must accept.

Phase 1 lists wire-contract artifacts for the adapter binary (`schemas/lenny-adapter.proto`, etc.) but does not include a corresponding artifact for the gateway-side external-protocol-adapter shared types. This is a lower-priority inclusion because the Go types are internal-API-surface rather than a client-facing wire contract, but Phase 1 is currently the only place where wire contracts are committed up-front, and callers registering via Phase 5 admin API need the type definitions from day one.

**Recommendation:** Either (a) extend Phase 1's "wire-contract artifacts" item to include a committed `pkg/adapter/shared.go` (or equivalent Go package) containing the Shared Adapter Types and SessionEventKind registry, with the CI gate that §15 additions mirror code changes; or (b) leave Phase 1 unchanged and instead add to Phase 5's `ExternalAdapterRegistry` deliverable a line naming the Shared Adapter Types commit as a Phase 5 prerequisite. Option (a) matches the Phase 1 pattern better and keeps all normative type commitments in one phase.

---

No other real regressions found. Iter2 renames (Embedded/Source/Compose Mode) did not reach §18 — its tier references are capacity tiers, which are unchanged. Iter2 CRD-001/002/003/004/005 fixes, SEC-001/002, SES-004/005, FLR-002/003/004, and OBS-007/008/009 are below Phase 18's granularity and require no sequencing changes. Parallelism annotations for 12a/12b/12c remain accurate against iter2 §4 changes (12a now explicitly notes KMS envelope encryption for OAuth tokens, consistent with iter2's CRD-clarifications).
