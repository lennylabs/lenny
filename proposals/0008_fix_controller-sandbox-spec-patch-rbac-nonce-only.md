# Proposal: Controller `patch` grant on the `Sandbox` main resource for the §4.7 nonce-only carrier write

- **Status:** Applied to spec (2026-06-19). Verified and converged after 2 adversarial review rounds (0 findings fixed).
- **Date:** 2026-06-19.
- **Scope:** Reconciles the §4.6.3 RBAC paragraph and the controller chart RBAC to the implemented §4.7 nonce-only surfacing mechanism. The WarmPoolController owns `Sandbox.spec.*` (§4.6.3 ownership table) and records the nonce-only render decision on `Sandbox.spec.requireSoPeercred` via Server-Side Apply, which issues an HTTP PATCH and therefore requires the `patch` verb on the `Sandbox` main resource. The spec RBAC paragraph and the chart grant `create`/`update`/`delete` but not `patch` on `Sandbox`, so the carrier write is forbidden and the §4.7 `PoolSecurityDegraded` alert never fires for a pool running with `SO_PEERCRED` disabled. The fix adds `patch` to the controller's `sandboxes` rule, reconciles the spec paragraph, and updates the chart RBAC unit test.

This document stages the proposed spec and chart changes. It does not modify any spec, code, or doc file. Apply the changes in the "Proposed changes" section after sign-off.

## 1. Problem

The §4.7 nonce-only-mode surfacing path is the audited security-degradation signal for a pool whose adapter-agent `SO_PEERCRED` boundary is disabled. On a live cluster it fails closed: the controller cannot write the carrier that drives the surfacing, so the mandatory alert never has a series. The under-grant originates in the spec's own §4.6.3 RBAC paragraph and is mirrored by the controller chart.

**The implemented mechanism writes `Sandbox.spec` via Server-Side Apply.** The §4.6.3 ownership table assigns `Sandbox` `spec.*` to the WarmPoolController (`spec/04_system-components.md:596`). The Sandbox reconciler records the §4.7 render decision on the `Sandbox.spec.requireSoPeercred` carrier before it creates the pod, so the decision is crash-safe and recoverable (`pkg/controller/sandbox/controller.go:496-554`). The write is a Server-Side Apply:

- `recordNonceOnlyCarrier` (`pkg/controller/sandbox/controller.go:617`) applies a patch whose body is `{"spec":{"requireSoPeercred":false}}` at `pkg/controller/sandbox/controller.go:636`: `r.Client.Patch(ctx, patch, client.RawPatch(types.ApplyPatchType, body), client.FieldOwner(string(ownership.WarmPoolController)))`.
- `clearNonceOnlyCarrier` (`pkg/controller/sandbox/controller.go:651`) applies the symmetric empty-spec patch at `pkg/controller/sandbox/controller.go:660` to drop a stale carrier on the §4.5 revert path.

Both target the `sandboxes` **main resource** (the patch body sets a `spec` field, so the write goes to the main resource rather than the status subresource). The field manager is `lenny-warm-pool-controller` (`ownership.WarmPoolController`, `pkg/admission/ownership/ownership.go:41`). Server-Side Apply always issues an HTTP `PATCH`, so this write requires the `patch` verb on `sandboxes`. The other controller SSA applies are status-subresource writes: `pkg/controller/sandbox/controller.go:814`, `pkg/controller/warmpool/controller.go:762`, and `pkg/controller/warmpool/controller.go:850` all call `.Status().Patch(..., client.Apply, ...)` against `sandboxes/status`, `sandboxtemplates/status`, and `sandboxwarmpools/status`, which the existing `*/status` `patch` grant already covers.

**The spec RBAC paragraph under-grants.** §4.6.3 (`spec/04_system-components.md:609`) enumerates the WarmPoolController grants: "The WarmPoolController ServiceAccount has `create`/`update`/`delete` on `Sandbox` and `get`/`patch` on `status` subresources of `Sandbox`, `SandboxTemplate`, and `SandboxWarmPool` (SSA status updates require `get` to read the current resource and `patch` to apply the partial object — `update` is insufficient for SSA Apply requests)...". The paragraph grants `patch` on `sandboxes/status` only and omits it from the `Sandbox` main resource, while §4.6.3 makes the WarmPoolController the owner of `Sandbox.spec.*` and the §4.7 carrier applies a `Sandbox.spec` field via SSA. The paragraph's own parenthetical for the status case ("`update` is insufficient for SSA Apply requests") is the exact reason the main-resource grant must include `patch` for the spec-field SSA write, but the main-resource grant was never updated to match. The §4.7 narration describes the surfacing as a status-only write (it "sets `SOPeercredDisabled=True` on the `Sandbox` status", `spec/04_system-components.md:910`), yet the implementation persists the `Sandbox.spec.requireSoPeercred` carrier first and derives the `SOPeercredDisabled` status condition from it, because the agent pod holds no apiserver access and the render decision must survive a crash between the carrier write and pod creation (`pkg/controller/sandbox/controller.go:496-518`).

**The chart mirrors the under-grant.** `charts/lenny/templates/controller-rbac.yaml:48-50` grants the controller ServiceAccount `verbs: ["get", "list", "watch", "create", "update", "delete"]` on `resources: ["sandboxes"]`, which omits `patch`. The `sandboxes/status` rule (`charts/lenny/templates/controller-rbac.yaml:64-66`) includes `patch` but does not cover a `Sandbox.spec` SSA apply. The helm unit test `charts/lenny/tests/controller-rbac_test.yaml:67-68` asserts the exact verb list `["get", "list", "watch", "create", "update", "delete"]` on `sandboxes`, so the chart change requires updating that assertion in the same commit or the test fails.

**Observed failure.** On a live Kind cluster, `kubectl auth can-i patch sandboxes.lenny.dev -n lenny-agents --as=system:serviceaccount:lenny-system:lenny-controller` returns `no` and `... can-i update ...` returns `yes`. For an acknowledged nonce-only pool (a `deploymentModel: sidecar` runtime with `requireSoPeercred: false` and a pool carrying `acknowledgeNonceOnlyAuth: true`), the WarmPoolController creates the member Sandbox but the carrier write is rejected:

```
record nonce-only decision for sandbox chaos-nonce-only-pool-zvcfl: sandboxes.lenny.dev
"chaos-nonce-only-pool-zvcfl" is forbidden: User "system:serviceaccount:lenny-system:lenny-controller"
cannot patch resource "sandboxes" in API group "lenny.dev" in the namespace "lenny-agents"
```

Because the carrier is never written, `poolNonceOnly` (`pkg/controller/warmpool/controller.go:900`) sees neither `Sandbox.spec.requireSoPeercred == false` nor the `SOPeercredDisabled=True` condition (both derive from the carrier), so the WarmPoolController never writes `SecurityDegradedMode=True` on the SandboxTemplate and the `lenny_pool_security_degraded` gauge stays 0. §4.7 (`spec/04_system-components.md:911`) requires that "Nonce-only operation MUST be covered by an alert with a human-review SLA", satisfied by the bundled §16.5 `PoolSecurityDegraded` rule on `lenny_pool_security_degraded == 1`. With the carrier write forbidden, that alert has no live series: a pool runs with the `SO_PEERCRED` boundary disabled and operators receive no signal. This is a fail-open security-surfacing defect.

This proposal does not introduce new behavior. The WarmPoolController already owns `Sandbox.spec.*` per §4.6.3, and the carrier write already exists in code; the spec RBAC paragraph and the chart never granted the verb the implemented SSA write requires.

## 2. Decisions

- **Grant exactly `patch` on the `Sandbox` main resource.** SSA Apply requires exactly `patch` on the resource it applies to (the same requirement the spec already records for `sandboxes/status`). Adding `patch` is the minimal grant that unblocks the carrier write; `create`/`update`/`delete` are retained. The RBAC stays at resource granularity, because Kubernetes RBAC cannot scope a verb to a single field, so the field-level write boundary continues to rest on SSA field managers (§4.6.3 SSA enforcement), exactly as the existing `sandboxes/status` `patch` grant does.
- **No `sandboxes/finalizers` grant is added.** The WarmPoolController sets the session-cleanup finalizer in the object it passes to `r.Client.Create` (`pkg/controller/warmpool/controller.go:537`). The finalizer is a create-time object field carried by the existing `create` verb, and no code path issues an SSA or strategic-merge patch against the `finalizers` subresource, so that subresource grant is not required by this change.
- **The grant is controller-only.** The gateway ServiceAccount holds no `patch` or `watch` on the `Sandbox` main resource and continues not to, per §4.6.3 (`spec/04_system-components.md:611`): `Sandbox.spec` and `Sandbox.status` are written solely by the WarmPoolController. This proposal touches only the controller ServiceAccount's `sandboxes` rule.
- **Reconcile the spec paragraph to the implemented mechanism.** The §4.6.3 RBAC paragraph is corrected to grant `patch` on `Sandbox` and to name the reason (the WarmPoolController's SSA write of the `Sandbox.spec.requireSoPeercred` carrier, §4.7), mirroring the existing status-SSA explanation. The §4.7 narration at `spec/04_system-components.md:910` is left describing the operator-visible outcome (the `SOPeercredDisabled` / `SecurityDegradedMode` conditions); a short clause is added there noting the WarmPoolController records the decision on the WarmPoolController-owned `Sandbox.spec.requireSoPeercred` carrier and derives the condition from it, so the spec text and the carrier-first implementation coincide.

## 3. CRD and RBAC changes

The change is one verb added to one existing ClusterRole rule. No CRD schema changes, no new resources, no new ServiceAccount, and no admission-webhook changes: the `lenny-pool-config-validator` webhook scopes only `sandboxwarmpools` and `sandboxtemplates` (`charts/lenny/templates/admission-policies/pool-config-validator-webhook.yaml:39`), so it does not gate writes to `sandboxes`, and the controller's `sandboxes` rule is the sole gate on the carrier write.

| Subject | Resource | Before | After |
| --- | --- | --- | --- |
| `system:serviceaccount:lenny-system:lenny-controller` | `lenny.dev/sandboxes` (main) | `get`, `list`, `watch`, `create`, `update`, `delete` | `get`, `list`, `watch`, `create`, `update`, `patch`, `delete` |

The `sandboxes/status` rule (`charts/lenny/templates/controller-rbac.yaml:64-66`) and every other rule are unchanged.

## 4. Proposed changes

### 4.1 Spec change: `spec/04_system-components.md` §4.6.3 RBAC paragraph (line 609)

Replace the first sentence of the WarmPoolController grant enumeration. The current text is:

```
The WarmPoolController ServiceAccount has `create`/`update`/`delete` on `Sandbox` and `get`/`patch` on `status` subresources of `Sandbox`, `SandboxTemplate`, and `SandboxWarmPool` (SSA status updates require `get` to read the current resource and `patch` to apply the partial object — `update` is insufficient for SSA Apply requests),
```

Replace it with:

```
The WarmPoolController ServiceAccount has `create`/`update`/`patch`/`delete` on `Sandbox` and `get`/`patch` on `status` subresources of `Sandbox`, `SandboxTemplate`, and `SandboxWarmPool` (the `patch` verb on the `Sandbox` main resource and on the `status` subresources is required because the WarmPoolController applies its owned fields via Server-Side Apply, which issues an HTTP `PATCH`: it records the §4.7 nonce-only render decision on the WarmPoolController-owned `Sandbox.spec.requireSoPeercred` carrier through an SSA apply, and it applies status conditions through SSA — `update` is insufficient for SSA Apply requests),
```

### 4.2 Spec change: `spec/04_system-components.md` §4.7 surfacing narration (line 910)

In the §4.7 escalation bullet, after the sentence "The WarmPoolController, which renders the flag and owns both status subresources per the [§4.6.3](#463-crd-field-ownership-and-write-boundaries) ownership table, sets `SOPeercredDisabled=True` on the `Sandbox` status and `SecurityDegradedMode=True` on the `SandboxTemplate` status.", insert the following sentence:

```
The WarmPoolController records the render decision on the WarmPoolController-owned `Sandbox.spec.requireSoPeercred` carrier (an SSA apply to the `Sandbox` main resource) before it creates the pod, so the decision survives a crash between the carrier write and pod creation, and derives the `SOPeercredDisabled` condition from the carrier; the `Sandbox.spec` carrier write is why the WarmPoolController ServiceAccount holds the `patch` verb on the `Sandbox` main resource ([§4.6.3](#463-crd-field-ownership-and-write-boundaries)).
```

### 4.3 Chart change: `charts/lenny/templates/controller-rbac.yaml` (lines 48-50)

In the controller ClusterRole's `sandboxes` rule, add `patch` to the verb list. The current rule is:

```yaml
  - apiGroups: ["lenny.dev"]
    resources: ["sandboxes"]
    verbs: ["get", "list", "watch", "create", "update", "delete"]
```

Replace the `verbs` line with:

```yaml
  - apiGroups: ["lenny.dev"]
    resources: ["sandboxes"]
    # patch is required for the WarmPoolController's Server-Side Apply write of
    # the §4.7 nonce-only render decision onto the Sandbox.spec.requireSoPeercred
    # carrier (pkg/controller/sandbox/controller.go recordNonceOnlyCarrier /
    # clearNonceOnlyCarrier); SSA issues an HTTP PATCH, for which `update` is
    # insufficient — the same reason the sandboxes/status rule below needs patch.
    verbs: ["get", "list", "watch", "create", "update", "patch", "delete"]
```

### 4.4 Chart test change: `charts/lenny/tests/controller-rbac_test.yaml` (lines 67-68)

Update the helm-unittest assertion that pins the exact `sandboxes` verb list to include `patch`. The current assertion fragment is:

```yaml
            resources: ["sandboxes"]
            verbs: ["get", "list", "watch", "create", "update", "delete"]
```

Replace the `verbs` value with the new list:

```yaml
            resources: ["sandboxes"]
            verbs: ["get", "list", "watch", "create", "update", "patch", "delete"]
```

If the surrounding assertion matches the rule by document path and `contains`/`equal` semantics, keep the same matcher; only the verb array changes. Add a one-line comment above the assertion noting that `patch` backs the §4.7 nonce-only carrier SSA write, matching the chart-template comment in 4.3.

## 5. Testing

- **Chart (helm-unittest):** `charts/lenny/tests/controller-rbac_test.yaml` asserts the controller `sandboxes` rule includes `patch` (the 4.4 edit). This is the unit-level guard that a future RBAC narrowing cannot silently drop the verb again. Run the chart unit tests (`helm unittest charts/lenny`) and confirm green.
- **Tier 9 (security / RBAC):** add or extend a tier-9 assertion that the controller ServiceAccount can `patch` `sandboxes.lenny.dev` in an agent namespace (a `SelfSubjectAccessReview` / `kubectl auth can-i` against the installed RBAC), so the live grant is verified rather than only the rendered chart. A failure means the §4.7 carrier write is forbidden and the `PoolSecurityDegraded` alert cannot fire.
- **Tier 8 (chaos), already committed:** `tests/tier8_chaos/nonce_only_degradation_test.go::TestNonceOnlyModeDegradationAndRecovery` registers a sidecar `requireSoPeercred: false` Runtime CR, creates an acknowledged nonce-only pool through the admin API, and asserts the WarmPoolController surfaces `SecurityDegradedMode=True` and `lenny_pool_security_degraded == 1`, then reverts and asserts recovery. It currently fails at the degradation assertion because the carrier write is forbidden; with the grant in place and the controller redeployed it exercises the degradation and revert-latch path to green.

  **Amendment (recorded on application, S4).** This proposal originally stated that no change to the tier-8 test was part of it. Running the test end to end against the live Kind cluster during S4 surfaced two test-only changes that were necessary to take it to a genuine Go `PASS`, so the test was modified and this clause is updated to record that. (1) The committed test hard-coded a single pool name; a pool name is the PRIMARY KEY of the Postgres `sandbox_warm_pools` table (`migrations/0033`) and a soft-deleted row keeps the name occupied, so the second run on a cluster fails at pool creation with `409 RESOURCE_CONFLICT`. S4 builds a per-run unique pool + Runtime name (`uniqueNonceOnlyName`) so the test is repeatable. (2) The committed recovery leg deleted the pool, which turns into a `SandboxTemplate` delete that the §10.5 deletion-guard webhook blocks under the §13.2 default-deny on a live cluster (the orthogonal, separately-tracked F-13.2.24 chart defect), so the proposal's "full path to green" claim for the recovery leg was over-optimistic. S4 rewrites the recovery leg to replace the pool's nonce-only members in place — scale the warm count to 0 through the §25.17 admin API, then delete the member Sandboxes directly — which exercises the §4.7 condition-clearing transition with the SandboxTemplate still present and asserts the stronger explicit `SecurityDegradedMode=False` outcome. Both edits are test-only and spec-faithful; they change no production behavior and no spec design. The full reconciliation is recorded in the BUILD-GAPS.md **F-4.7.22** "Scope reconciliation with proposal 0008 (S4)" note, which supersedes the original "no change to that test" wording.
- **Tier 11 (docs):** none required, because no metric, alert, or runbook is added or renamed; the `PoolSecurityDegraded` alert and `lenny_pool_security_degraded` gauge already exist with their §16.5 / `docs/runbooks/` entries. This change makes the existing series reachable; it does not alter the observability inventory.

## 6. Non-goals

- **Changing the carrier mechanism to a status-only write.** Writing the nonce-only decision to `Sandbox.status` instead of `Sandbox.spec` would avoid the main-resource `patch` grant, but it loses the crash-safety the carrier-first ordering provides (`pkg/controller/sandbox/controller.go:496-518`): the render decision must persist before the pod is created so a crash between the two does not leave a nonce-only pod with no recorded decision and no `SOPeercredDisabled` condition. The implemented design is correct; the spec and chart are the defect. Rejected.
- **Granting the gateway ServiceAccount `patch` on `sandboxes`.** The carrier write is a WarmPoolController write; the gateway must not gain main-resource write access to `Sandbox` (§4.6.3, `spec/04_system-components.md:611`). Out of scope.
- **Adding a `sandboxes/finalizers` grant.** No code path patches that subresource (see Decisions); the finalizer is set at create time under the existing `create` verb. Rejected as unnecessary.
- **Broadening the grant beyond `patch` (e.g. `*`).** Least privilege; only the verb the SSA write needs is added.

## 7. Files touched on application

- `spec/04_system-components.md`: §4.6.3 RBAC paragraph (line 609) grant correction; §4.7 surfacing narration (line 910) carrier-write clause.
- `charts/lenny/templates/controller-rbac.yaml`: add `patch` to the controller `sandboxes` rule (lines 48-50).
- `charts/lenny/tests/controller-rbac_test.yaml`: update the `sandboxes` verb-list assertion (lines 67-68).
- (Test infrastructure, applied during implementation, not staged as exact text here) a tier-9 RBAC assertion that the controller SA can `patch` `sandboxes`.
- (Test infrastructure, recorded on application — see the §5 "Amendment (recorded on application, S4)" clause) `tests/tier8_chaos/nonce_only_degradation_test.go`: per-run unique pool/Runtime names (`uniqueNonceOnlyName`) for re-run repeatability and an in-place member-replacement recovery leg, both test-only and spec-faithful. The reconciliation is recorded in the BUILD-GAPS.md F-4.7.22 "Scope reconciliation with proposal 0008 (S4)" note.
