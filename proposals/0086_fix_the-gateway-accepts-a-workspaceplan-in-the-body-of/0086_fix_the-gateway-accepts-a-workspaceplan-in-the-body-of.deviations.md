# Deviations — 0086_fix_the-gateway-accepts-a-workspaceplan-in-the-body-of

The implementor owns this file. It stays empty until an implementation records a departure from what the proposal states.

## Proposed: Tier 9 uploadRef scope test wires a pod binder and adds a positive control

**Status:** accepted: `TestPlanCannotFetchAnotherSessionsUpload` in `tests/tier9_security/upload_ref_scope_test.go` builds its pod binder with the counting store as `Blobs`, so the binder's materialization reads go through the wrapper, and the trailing own-session finalize fails the test when the wrapper records no Get for A's content. The zero-Get assertion is the one TEST-1 states, and the wiring and the positive control make it fail when the scope check is absent (2026-10-29, orchestrator under the owner's delegation)

**Reported by:** step S15 (TEST-1: plan-binding, MCP schema, alert, setup-per-session, contract and security tests).

**What the proposal says.** TEST-1 Tier 9 states: "Wrap the gateway's blob store in a counting wrapper and assert that it recorded zero Get calls for A's key." The proposal says nothing further about how the wrapper is wired into the gateway.

**What landed instead.** `tests/tier9_security/upload_ref_scope_test.go` wires an envtest-backed pod binder whose `Blobs` field is the counting wrapper. After the zero-Get assertions, the test adds a positive control: session A finalizes with its own `uploadRef`, and the test asserts that the wrapper then records a Get for A's content.

**Why.** Without a pod binder, the gateway never reads a blob, so a zero-Get assertion passes even when the scope check is missing. The binder and the positive control make the zero count meaningful.

**What a later reader would otherwise get wrong.** A reader working from the proposal text alone would expect a test that asserts only a zero Get count against a bare blob-store wrapper. That reader would not expect the envtest pod binder or the trailing positive-control finalize, and could remove either one as surplus, which would let the test pass with the scope check absent.

## Proposed: Recycled-pod binder test returns the pod to idle by deleting the SandboxClaim and reuses the existing adapter recorder

**Status:** accepted: `returnRecycledPodToIdle` in `pkg/gateway/podlifecycle/podsession/binder_test.go` deletes the per-pod SandboxClaim, which is the state a successful scrub leaves, and `assertOneSetupAfterFinalize` reads the session IDs that `bindRequestRecorder` records for every adapter RPC. The test asserts the TEST-1 requirement of one FinalizeWorkspace followed by one RunSetup per session on a recycled pod. The scrub path keeps its own tests in the `TestReleaseRecycling*` cases of the same file (2026-10-29, orchestrator under the owner's delegation)

**Reported by:** step S15 (TEST-1: plan-binding, MCP schema, alert, setup-per-session, contract and security tests).

**What the proposal says.** The TEST-1 Tier 2 `binder_test.go` recycled-pod case says to "return the pod to idle" and to use "an adapter fake that records the session ID of each FinalizeWorkspace and RunSetup call".

**What landed instead.** The test returns the pod to idle by deleting the recycling per-pod SandboxClaim (`returnRecycledPodToIdle` in `binder_test.go`). The adapter fake is the existing `concurrentAdapter`, whose `bindRequestRecorder` already records the session ID of every RPC. The exclusive Bind path and the shared-pod slot case both use the same `assertOneSetupAfterFinalize` helper.

**Why.** The proposal does not say how the pod returns to idle, and no in-package helper performs that transition. Deleting the claim reproduces the state a successful scrub leaves for the next claim. Reusing the existing recorder avoids writing a second fake.

**What a later reader would otherwise get wrong.** A reader working from the proposal text alone would look for a dedicated adapter fake that records FinalizeWorkspace and RunSetup session IDs, and would not find one. That reader would also not know that the idle transition is simulated by claim deletion rather than by running the scrub path, so the test does not exercise the scrub itself.

## Proposed: `ClaimOpts.RequiresDemotion` remains in the pod lifecycle interface

**Status:** accepted: the field is tracked as BUILD-GAPS F-12.6.27, a behavior-free refactor in the direct-fix queue that runs after 0086's implementation (`gateway-runtime-comms-remediation.md` §10.2, the block for proposal 0086). The field still declares `RequiresDemotion bool` in `pkg/podlifecycle/interfaces.go`, no non-test code sets it, and the §6.1 demotion runs at the bind through `podsession.RequiresDemotion` (2026-10-29, orchestrator under the owner's delegation)

**Reported by:** step sweep (stale code the landed spec edits left behind).

**What the proposal says.** The proposal stages no change to `pkg/podlifecycle/interfaces.go`.

**What landed instead.** `pkg/podlifecycle/interfaces.go` is unchanged at line 117 and still declares `RequiresDemotion bool`.

**Why.** The field contradicts the landed specification. §4.6.1 now states: "`ClaimOpts` includes optional `priority`, optional `clusterID` (nullable, for future multi-cluster)." The field is code rather than a comment, so removing it changes behavior the proposal did not stage.

**What a later reader would otherwise get wrong.** A reader of `pkg/podlifecycle/interfaces.go` would take the old `ClaimOpts` contract, which includes `RequiresDemotion`, as current.

## Proposed: `ClaimOpts.RequiresDemotion` remains in the pod registry

**Status:** accepted: the field is tracked as BUILD-GAPS F-12.6.27, which removes it from `pkg/podregistry/podregistry.go` together with `pkg/podlifecycle/interfaces.go` in the direct fix queued after 0086's implementation (2026-10-29, orchestrator under the owner's delegation)

**Reported by:** step sweep (stale code the landed spec edits left behind).

**What the proposal says.** The proposal stages no change to `pkg/podregistry/podregistry.go`.

**What landed instead.** `pkg/podregistry/podregistry.go` is unchanged at line 80 and still declares `RequiresDemotion bool`.

**Why.** The field contradicts the landed specification. The §12.6 struct table now lists the `ClaimOpts` key fields as "`PoolID`, `TenantID`, `SessionID`, `Priority` ..., `ClusterID` (nullable `*ClusterID`, always nil in v1)". The field is code rather than a comment, so removing it changes behavior the proposal did not stage.

**What a later reader would otherwise get wrong.** A reader of `pkg/podregistry/podregistry.go` would take the old `ClaimOpts` contract, which includes `RequiresDemotion`, as current.

## Proposed: CRD pod registry claim test still sets `RequiresDemotion`

**Status:** accepted: the setter is tracked as BUILD-GAPS F-12.6.27, whose suggested resolution removes this tier 2 setter together with the two struct fields in the direct fix queued after 0086's implementation (2026-10-29, orchestrator under the owner's delegation)

**Reported by:** step sweep (stale code the landed spec edits left behind).

**What the proposal says.** The proposal stages no change to `tests/tier2_component/stores/crdpodregistry_claim_test.go`.

**What landed instead.** `tests/tier2_component/stores/crdpodregistry_claim_test.go` is unchanged at line 311 and still sets `RequiresDemotion: true,`.

**Why.** The test contradicts the landed specification, because the `ClaimOpts` key fields in the §12.6 struct table no longer include `RequiresDemotion`. The line is code rather than a comment, so changing it changes behavior the proposal did not stage.

**What a later reader would otherwise get wrong.** A reader of `tests/tier2_component/stores/crdpodregistry_claim_test.go` would take the old `ClaimOpts` contract, which includes `RequiresDemotion`, as current.

## Proposed: Sandbox CRD still describes a `WorkspacePlan` field on the pod spec

**Status:** accepted: the field and its generated description are tracked as BUILD-GAPS F-12.6.28, which removes `Sandbox.spec.workspacePlan` from `pkg/apis/lenny/v1alpha1/sandbox_types.go`, regenerates the CRD, and removes `PodSpec.WorkspacePlan` in the direct fix queued after 0086's implementation, together with F-12.6.27 (2026-10-29, orchestrator under the owner's delegation)

**Reported by:** step sweep (stale code the landed spec edits left behind).

**What the proposal says.** The proposal stages no change to `charts/lenny/crds/lenny.dev_sandboxes.yaml`.

**What landed instead.** `charts/lenny/crds/lenny.dev_sandboxes.yaml` is unchanged at line 310 and still carries this description:

```
WorkspacePlan is the §14 WorkspacePlan a §12.6 CreatePod pod was
created with. spec: §12.6 — the PodSpec carries the
WorkspacePlan. It is empty for a warm pod (the WarmPoolController
creates pods without a session, so the plan is materialized at
session claim);
```

**Why.** The description contradicts the landed specification. The §12.6 struct table now lists the `PodSpec` key fields as "`RuntimeDefinitionRef`, `IsolationProfile`, `ExecutionMode`, resource limits", without a `WorkspacePlan`. The CRD description is generated from `sandbox_types.go`, so the field and its description are part of the generated schema rather than a standalone comment, and changing them changes behavior the proposal did not stage.

**What a later reader would otherwise get wrong.** A reader of `charts/lenny/crds/lenny.dev_sandboxes.yaml` would take the old contract, in which the pod spec carries the `WorkspacePlan`, as current.
