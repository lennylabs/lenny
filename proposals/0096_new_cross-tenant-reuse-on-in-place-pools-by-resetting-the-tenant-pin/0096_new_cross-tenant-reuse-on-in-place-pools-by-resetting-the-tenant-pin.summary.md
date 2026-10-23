# Summary: Cross-tenant reuse on in-place pools by resetting the tenant pin after a proven runtime restart

## Summary

**Status and schedule.** Draft skeleton, split out of proposal 0087 on 2026-10-22. It depends on proposal 0087 part 1 being implemented, and it is scheduled in Phase 3 of the proposal track (`gateway-runtime-comms-remediation.md` §10.2). Its hard gate is that security review findings 1, 3, 4, and 5 are closed and that the four supervisor properties pass on a KVM-capable host.

**Problem statement.** `recycle.allowCrossTenantReuse` has no effect, because a recycled pod keeps its pinned tenant for life and nothing resets the pin. With 0087 part 1's supervisor and `restart` lifetime, a pod's runtime process is replaced at each occupancy-zero boundary with an attested end. This proposal resets the tenant pin after that proven restart on `scrubProfile: in-place` microVM pools, so that a later session of another tenant can use the pod.

**What changes.** Written by the review run. The problem statement lists the sites: the §5.2 tenant-pinning, cross-tenant, `scrubProfile`, and step-7 text; §4.6.3 RBAC; §18's package list; the tenant-label webhook; the gateway pin read and acquisition predicates; and the pool admission rules.

**Decisions.** None yet. The owner decisions this proposal starts from are:

- Cross-tenant reuse on `in-place` microVM pools with a fresh runtime process plus the scrub is accepted behind the explicit opt-in, with T4 prohibited and the residuals documented (2026-10-01).
- `allowCrossTenantReuse` stays in the schema for this proposal to give it effect (2026-10-01).
- Kata first, gated on the four supervisor properties on a KVM-capable host (2026-10-02).
- Cross-tenant pools require `onScrubFailure: fail`, and the pin reset is keyed on a successful attestation plus a clean step-6 verification, never on a scrub report alone.

**Watch out for.**

- The new label edge weakens a tier-9 defense-in-depth control and is an owner decision.
- The 0079 statements this proposal amends are in the tree. 0079 is implemented and is not edited.

## Goals

- A deployer can serve sessions of different tenants on one `in-place` microVM pod, one tenant at a time, after an attested runtime restart and a clean scrub verification.

## Non-goals

- Cross-tenant reuse on `sandboxed` or `standard` pools, or on concurrent pools.
- Cross-tenant reuse for `workspaceTier: T4`.
- Any change to 0087 part 1's supervisor, channel, or `restart` lifetime beyond what the pin reset reads.

## Open decisions for human to make

None recorded yet. The review loops write this section.

## Impacts on other proposals

| Proposal | Status | What this change does to it | What it must do |
|:--|:--|:--|:--|
| 0087 | Draft | Reads 0087 part 1's END attestation and `restart` lifetime. | Land first. |
| 0084 | Draft | Relies on its per-generation rebind of the intra-pod MCP listeners (finding 4). | Land first. |
