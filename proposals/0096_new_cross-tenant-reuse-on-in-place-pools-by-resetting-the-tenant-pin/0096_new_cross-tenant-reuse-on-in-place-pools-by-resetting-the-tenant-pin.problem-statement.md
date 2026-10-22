# Problem: Cross-tenant reuse on in-place pools by resetting the tenant pin after a proven runtime restart

## Statement

`recycle.allowCrossTenantReuse` is in the pool schema and has no effect. §5.2 admits it only on a sequential recycling pool (`maxConcurrentSessions: 1`) with `isolationProfile: microvm`, rejects `scrubProfile: standard` with it, and rejects it for `workspaceTier: T4`. Since proposal 0079, §5.2 states that a pod on such a pool is never reused across tenants, because a recycled pod keeps the runtime process that served its pinned tenant, and that "serving a second tenant on such a pool requires restarting the runtime process and resetting the tenant pin, which the platform does not do". The owner kept the field so that the supervised runtime process would make it meaningful again (plan §10.3, 2026-10-01).

Proposal 0087 part 1 adds the runtime supervisor and the `restart` lifetime, which replace the runtime process at each occupancy-zero boundary and attest that no runtime process remains. This proposal is part 2 of that work, split out of 0087 on 2026-10-22. It gives `allowCrossTenantReuse` effect on `scrubProfile: in-place` microVM pools by resetting the tenant pin after a proven runtime restart. It reuses the 0087 supervisor and adds no new process.

The plan records the conditions (plan §10.2 Phase 3, the entry for 0087 part 2; plan §10.3):

- Cross-tenant reuse on `in-place` microVM pools with a fresh runtime process plus the scrub is accepted behind the existing explicit opt-in. T4 stays prohibited, and the remaining guest-kernel and adapter residuals are documented. The review directs its security lens at whether these together suffice across tenants.
- Kata first, after the four supervisor properties (PID-namespace init signal immunity, `PR_SET_DUMPABLE` against same-UID inspection, `kill(-1)` scope, and `si_pid`) pass on a KVM-capable host. The 2026-10-02 spike did not test Kata, because its host had no `/dev/kvm`.
- Hard gate: security review findings 1, 3, 4, and 5 are closed. Finding 3 was closed by the Phase 0 pod identity hardening (proposals 0088 and 0089), findings 1 and 5 (cleanup half) by 0087 part 1, finding 4 by proposal 0084 and the runtime-SDK proposal (0090).
- Cross-tenant pools require `onScrubFailure: fail`. The tenant-pin reset is keyed on a successful END attestation plus a clean step-6 verification, never on a scrub report alone (BUILD-GAPS F-5.2.37, the cross-tenant half).

Resetting the pin needs more than writing `{tenant_id} -> unassigned`:

- `unassigned` is terminal in the specification and the tree. §5.2 **Tenant pinning** item 2 permits only `unset -> {tenant_id}` and `{tenant_id} -> unassigned`, §18's package list restates that, and the `lenny-tenant-label-immutability` webhook (`pkg/admission/label_immutability/label_immutability.go`) rejects `unassigned -> {tenant_id}`. The gateway re-stamps the tenant label on every claim, so a pod reset to `unassigned` could never be claimed by a second tenant. The proposal stages a new permitted label edge, for example `unassigned -> {tenant_id}` written only by the gateway ServiceAccount, or a different reset scheme. Either changes a tier-9 defense-in-depth control.
- §5.2 states that "a recycled pod never takes the `{tenant_id} → unassigned` transition", that a pod on an `allowCrossTenantReuse` pool "is never reused across tenants", and that neither `scrubProfile` reuses a pod across tenants. §4.6.3 states that no component performs the reset. Each is amended for a `restart` pool that meets the gate.
- §5.2 recycle step 7 states that a pod is never returned to cross-tenant reuse without a fresh guest. The proposal reconciles that sentence with an `in-place` guest that keeps running.
- The gateway's tenant-pin read (`pkg/gateway/podlifecycle/podclaim/tenantpin.go`) and the acquisition predicates 0079 landed treat a pod labelled `unassigned` as pinned. The proposal restates them for a reset pod.
- Cross-tenant reuse has never worked in code: nothing in `pkg/` or `cmd/` writes `unassigned`. This proposal builds it for the first time.
- A tenant change is a decision the supervisor's attestation gates, so the supervisor is a boundary for it (plan §10.2, security review finding 2). 0087 part 1 states the supervisor's trust basis; this proposal states what a tenant change additionally requires.

## Evidence

- (to verify at review) `spec/05_runtime-registry-and-pool-model.md` §5.2 **Tenant pinning**, the `allowCrossTenantReuse` paragraph, the `scrubProfile` paragraph, and recycle step 7.
- (to verify at review) `spec/04_system-components.md` §4.6.3, the gateway RBAC sentence on the tenant label.
- (to verify at review) `pkg/admission/label_immutability/label_immutability.go` and `pkg/gateway/podlifecycle/podclaim/tenantpin.go`.
- `gateway-runtime-comms-remediation.md` §10.2 Phase 3, the entry "Proposal 0087 part 2: cross-tenant reuse on `in-place` pools by resetting the tenant pin ...", and §10.3, the cross-tenant decision of 2026-10-01 and the trust-model decision of 2026-10-02.
- BUILD-GAPS F-5.2.37, the cross-tenant half.

## Who observes it

A deployer running a Kata pool with `recycle.enabled: true`, `maxConcurrentSessions: 1`, `allowCrossTenantReuse: true`, and `scrubProfile: in-place`. Today such a pool yields no cross-tenant reuse, so idle microVM pods stay pinned to tenants that may never use them, which increases cold starts. The Kind test cluster provisions no Kata runtime, so tier 5 cannot exercise this configuration.

## What breaks if nothing changes

Nothing is unsafe. A deployer who needs a pod usable by any tenant sets `scrubProfile: vm-restart` and pays the reprovision cost. The cost of not building this is that `allowCrossTenantReuse` stays a field with no effect.

## Prerequisites

- Proposal 0087 part 1 implemented (the supervisor, the `restart` lifetime, the END attestation, and the cleanup half of F-5.2.37).
- Proposal 0084 implemented (finding 4 for the intra-pod MCP listeners).
- The four supervisor properties passing on a KVM-capable host under the Kata RuntimeClass.
