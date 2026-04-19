### DEL-001 `maxDelegationPolicy` Inheritance Semantics Ambiguity [High]
**Files:** `08_recursive-delegation.md` §8.3

The spec states: "Child leases inherit the intersection of the parent's effective policy; they cannot specify a `maxDelegationPolicy` that is less restrictive than the parent's effective `maxDelegationPolicy`."

This creates ambiguity: Does the child inherit the parent's `maxDelegationPolicy` value by default, or does the child's own `maxDelegationPolicy` (if set) face a restrictiveness check? When a parent session has `maxDelegationPolicy: "read-only-policy"` and creates a child with no explicit `maxDelegationPolicy` (null), the spec does not clarify whether the child automatically inherits, or the child gets a fresh policy context.

**Recommendation:** Clarify the inheritance model: either (a) explicitly state that children inherit the parent's `maxDelegationPolicy` value as a default when not set, or (b) define the semantics of null `maxDelegationPolicy` in a child when the parent has a non-null cap.

---

### DEL-002 `snapshotPolicyAtLease` Scope Boundary with Interceptor Config Changes [Medium]
**Files:** `08_recursive-delegation.md` §8.3

The spec documents `snapshotPolicyAtLease: true` snapshots matching pool IDs but not interceptor configuration. It does not define: (1) detection mechanism for interceptor config changes; (2) application point (retroactive or only to new calls); (3) rollback implications if failPolicy is weakened and then strengthened.

**Recommendation:** Define cache invalidation strategy for interceptor config changes. Specify whether in-flight delegation calls use the interceptor configuration at the time of the call or at the time the delegation was submitted.

---

### DEL-003 Deep Tree Recovery with `maxDelegationPolicy` Restrictions on Intermediate Nodes [High]
**Files:** `08_recursive-delegation.md` §8.10, §8.3

The spec provides clear guidance on deep-tree recovery timing (depth 5+ requires `maxTreeRecoverySeconds ≥ 900 + 600 + 120 = 1620s`). However, does not address what happens if an intermediate node in a depth-5 tree has a `maxDelegationPolicy` that was narrowed between initial delegation and recovery phase.

**Recommendation:** Explicitly state that delegation policy enforcement during tree recovery uses the policy at the time of original delegation (stored in session record), not the live policy. This aligns with "once a child session is running, its delegation was already approved."

---

### DEL-004 Extension Denial Cool-Off Persistence Across Gateway Failover [Medium]
**Files:** `08_recursive-delegation.md` §8.6

The spec states cool-off flag is "persisted to the `delegation_tree_budget` Postgres table" but does NOT specify: (1) query semantics — does new replica read before processing pending requests? (2) race condition — can in-flight extension request bypass cool-off? (3) clock skew — comparison of cool_off_expiry across replicas.

**Recommendation:** Define the handoff protocol for the `extension-denied` flag: (a) new replica MUST read the flag before resuming lease extension state machine, (b) specify how in-flight requests are handled, (c) add UTC clock requirement for cool-off expiry comparison.

---

### DEL-005 `CREDENTIAL_PROVIDER_MISMATCH` Rejection During Multi-Hop Cross-Environment Tree [Medium]
**Files:** `08_recursive-delegation.md` §8.3, `10_gateway-internals.md` §10.6

The spec defines that cross-environment `inherit`-mode delegation rejects with `CREDENTIAL_PROVIDER_MISMATCH` if parent's credential pool providers do not intersect with child's `supportedProviders`. However, for multi-hop trees (Root Env A → Child Env B → GrandChild Env C) the spec does NOT state whether the compatibility check uses Env A's providers (inherited), Env B's providers, or some other reference.

**Recommendation:** Clarify the credential pool identity rule for cross-environment multi-hop trees.
