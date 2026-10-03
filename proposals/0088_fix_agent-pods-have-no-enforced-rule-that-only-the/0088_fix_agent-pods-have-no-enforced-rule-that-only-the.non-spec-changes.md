# Non-spec changes: Only the runtime container runs at the agent UID, and every agent-pod container sets runAsGroup

These changes are staged. Applying them is the implementation's work, under the
implementation checklist.

## Design (implementation-facing)

Enforcement is one clause in the existing `pkg/podsecurity` per-container loop (CODE-1). The `lenny-pod-security` webhook already flattens init, regular, and ephemeral containers into that validator, and `cmd/lenny-webhook` already parses the adapter and agent UIDs from the `security.podUIDs` chart values for `EphemeralContainerCredGuard`. CODE-2 passes them through. CODE-3 makes the pod builder's output satisfy the clause. TEST-1 brings the hand-written agent-namespace test pods into conformance. No flag, chart value, error code, or webhook is added.

Landing order matters: CODE-3 precedes CODE-1, because the nonzero clause rejects every pod the current builder produces (no container sets `runAsGroup` today).

## Staged code changes

### CODE-1 · pkg/podsecurity identity clause

Targets: `pkg/podsecurity/podsecurity.go` (`PodSpec`, `ContainerSpec`, `ValidateAgentPod`), `pkg/podsecurity/podsecurity_test.go`, `pkg/podsecurity/runtimeclass_test.go`, `pkg/podsecurity/fuzz_test.go`.

1. Add `AdapterUID, AgentUID int64` to `PodSpec`, beside `CredVolumeName`, each with a doc comment naming the `security.podUIDs` value it carries. Add `RunAsUser *int64` to `ContainerSpec`. The `ValidateAgentPod` signature is unchanged.
2. Add an unexported helper in `podsecurity.go`, called from the `ValidateAgentPod` per-container loop after the existing `runAsGroup` OVERBROAD clause:

   ```go
   // spec: §13.1 (Container identity)
   func identityViolations(c ContainerSpec, spec PodSpec, credGID int64, credentialContainer map[string]bool) []string
   ```

   It reports these violations, in this order, each as its own string:

   - (a) `container %q must set non-zero runAsUser and runAsGroup (§13.1 Container identity)` when `RunAsUser` or `RunAsGroup` is nil or 0.
   - (b) `container %q runAsUser %d is reserved for the %q container (§13.1 Container identity)` when `*RunAsUser == spec.AdapterUID` and the container is not (`Name == "adapter"` and `credentialContainer[Name]`), with `%q` = `adapter`; or when `*RunAsUser == spec.AgentUID` and the container is not (`Name == "runtime"` and `credentialContainer[Name]`), with `%q` = `runtime`.
   - (c) `container "runtime" must not use the lenny-cred-readers GID %d as runAsGroup (§13.1 Container identity)` when `Name == "runtime"`, `credentialContainer[Name]`, and `*RunAsGroup == credGID`.

   The `credentialContainer[Name]` term is the discriminating condition. The credential set holds names of regular containers only (D2). Tier-1 fixtures bypass the apiserver, so they keep container names unique across the three lists. Do not key the reservation on the name alone. The clause applies under every RuntimeClass; `RuntimeClassPolicy` grants no identity relaxation. When `spec.AdapterUID` or `spec.AgentUID` is 0, clause (a) already rejects any container at UID 0, so (b) needs no zero guard.
3. Update the package doc comment to list the identity clause among the checks the validator applies.
4. Give every existing fixture in the three test files explicit, nonzero, non-reserved container identities so the existing cases keep testing what they tested.

### CODE-2 · lenny-pod-security webhook wiring

Targets: `pkg/admission/webhook/pod_security.go` (`PodSecurity`, `translatePodSpec`, `translateContainer`), `cmd/lenny-webhook/main.go` (`newMux`), `pkg/admission/webhook/pod_security_test.go`, `pkg/admission/webhook/pod_security_uid_lockstep_test.go`, `tests/integration/admission_policy_test.go`, `charts/lenny/templates/admission-policies/pod-security-webhook.yaml` (header comment only).

1. Change the signature to `PodSecurity(adapterUID, agentUID, credReadersGID int64, credVolumeName string, rcPolicy podsecurity.RuntimeClassPolicy) Decider`. The parameter order matches `EphemeralContainerCredGuard`. Update its doc comment to name the identity clause.
2. Change `translatePodSpec` to take the two UIDs and set `spec.AdapterUID` and `spec.AgentUID`. Change `translateContainer` to copy `sc.RunAsUser` into `out.RunAsUser`.
3. In `newMux`, the `/pod-security` handler becomes `webhook.PodSecurity(adapterUID, agentUID, credReadersGID, podspec.CredVolumeName, rcPolicy)`. `newMux` already receives the three values as plain `int64` parameters, so no dereference is needed.
4. Update every `webhook.PodSecurity` call in `pod_security_test.go` with the new arguments, give `hardenedContainer` and every hand-built container in that file container identities as CODE-1 item 4 states, and update the call in `tests/integration/admission_policy_test.go` to pass `podspec.AdapterUID, podspec.AgentUID, podspec.CredReadersGID`. The integration file builds only under the `integration` build tag, so `go build ./...` does not catch a missed call; run `go test -tags integration -run TestAdmissionPolicy ./tests/integration/`, which no `lenny-test` tier selects.
5. Change the lockstep helper `decidePodSecurity` to take `(adapterUID, agentUID, gid int64)` and pass all three to `PodSecurity`.
6. Revise the chart template's header comment to state that the webhook also enforces the §13.1 container identity clause on every container after mutation. No template logic changes.

### CODE-3 · pod builder runAsGroup and egress-capture UID

Targets: `pkg/controller/sandbox/podspec/podspec.go` (`containerSecurityContext`, `injectEgressCaptureSidecar`, a new constant beside `EgressCaptureContainerName`), `pkg/controller/sandbox/podspec/podspec_test.go`, `pkg/controller/sandbox/podspec/egress_capture_test.go`, `cmd/lenny-egress-capture/main.go`, `cmd/lenny-egress-capture/main_test.go`.

1. In `containerSecurityContext(uid int64)`, add `RunAsGroup: ptr.To(uid)` with a `// spec: §13.1 (User row; Container identity)` comment. The comment states that the primary GID equals the UID and is deliberately not the `lenny-cred-readers` GID, as SPEC-1 **Container identity.** requires for the `runtime` container. `basePod` stays unchanged and sets no pod-level `runAsUser` or `runAsGroup`.
2. Add an unexported constant in the `const` block with `EgressCaptureContainerName`:

   ```go
   // egressCaptureUID is the UID and primary GID of the test-only
   // egress-capture container, which the builder injects only when the
   // controller's egress-capture image is set. It lies outside the default
   // adapter and agent UIDs, the default lenny-cred-readers GID, and 0.
   egressCaptureUID int64 = 65531
   ```

3. `injectEgressCaptureSidecar` uses `containerSecurityContext(egressCaptureUID)` in place of the agent UID.
4. In `cmd/lenny-egress-capture/main.go`, open the capture file with mode `0o640` in place of `0o600`. The adjacent comment states that the capture emptyDir is fsGroup-managed and setgid, so the file's group is `lenny-cred-readers`, which the `runtime` container holds as a supplementary group and reads through.

## Staged schema, chart, and migration changes

None. The chart change is a comment inside CODE-2.

## Staged docs changes

### DOCS-1 · operator guide container identity item

Target: `docs/operator-guide/namespace-and-isolation.md`, section **Admission Policies**, list **Policy Manifests**.

Add a new numbered item after item 8 (`lenny-ephemeral-container-cred-guard`), in its own item rather than appended to item 8. Title it as the pod-security webhook's container identity check. The item states, in this order:

1. The pod-security webhook validates every Pod CREATE and UPDATE in an agent namespace after mutating admission, under every RuntimeClass, so containers a deployer's mutating webhook injects are checked too.
2. Every init, regular, and ephemeral container sets `securityContext.runAsUser` and `securityContext.runAsGroup` at container level to nonzero values, and a pod-level value does not satisfy the check.
3. The adapter UID and the agent UID are the chart values `security.podUIDs.adapter` (default 65532) and `security.podUIDs.agent` (default 65533). Only the `adapter` container may run at the adapter UID, and only the `runtime` container at the agent UID.
4. The recommended fix for a rejected injector is to configure it to give its container an explicit `runAsUser` and `runAsGroup` outside both reserved UIDs. Distroless images run as `nonroot`, UID 65532, which equals the default adapter UID.
5. The alternative, after the recommended fix, is to override `security.podUIDs.adapter`, which changes the adapter identity on every agent pod.

In item 8, add one sentence noting that ephemeral containers are also subject to the new item's check. Include no specification section numbers. Apply `doc-style.md` and `doc-content.md`.

## Testing

Every test carries `// spec: 13.1 (Pod Security)`; tier-2-and-higher tests also carry a `// diagnosis:` comment.

**Tier 1 (CODE-1), table cases in `pkg/podsecurity/podsecurity_test.go`.** Each rejected case asserts the CODE-1 clause letter by its message substring; each admitted case asserts no error.

- Rejected under (a): `runAsUser` absent; `runAsGroup` absent; `runAsUser` 0; `runAsGroup` 0; a pod that sets only a pod-level `runAsUser`, modelled as a container with nil `RunAsUser`.
- Rejected under (b): an injected regular container at the agent UID; an injected regular container at the adapter UID; an init container at the adapter UID; an init container with `restartPolicy: Always` at the agent UID; an embedded pod (only `runtime` in the credential set) whose init container is named `adapter` and runs at the adapter UID. The embedded-pod init `adapter` case is the one that discriminates a name-only check from the credential-set check.
- Rejected under (c): `runtime` with `runAsGroup` equal to the cred-readers GID.
- Admitted: `adapter` with `runAsGroup` equal to the cred-readers GID; a sidecar pod at the default UIDs with GID equal to UID; an embedded pod with `runtime` only; a non-reserved injected sidecar with explicit identities.
- Boundary: `AdapterUID` and `AgentUID` overridden to non-default values, where a container at the old default UIDs is admitted and one at the new values is rejected.

**Tier 1 (CODE-1), `pkg/podsecurity/runtimeclass_test.go`.** Under the gVisor and the Kata policy, each (a), (b), and (c) case still rejects.

**Tier 1 (CODE-1), `pkg/podsecurity/fuzz_test.go`.** Extend the fuzz input with `RunAsUser`, `AdapterUID`, `AgentUID`, and `CredentialContainerNames`. The property: an admitted pod has no container at `AdapterUID` unless it is named `adapter` and `adapter` is in `CredentialContainerNames`, no container at `AgentUID` unless it is named `runtime` and `runtime` is in `CredentialContainerNames`, and no container with a nil or zero identity.

**Tier 1 (CODE-2), `pkg/admission/webhook/pod_security_test.go`.** One denial for an injected regular container at the agent UID and one for an init container at the adapter UID, each through the full decode and translate path, asserting `Allowed == false` and the CODE-1 (b) message. One case asserts that a container whose only `runAsUser` is pod-level is denied, which proves `translateContainer` reads the container value.

**Tier 1 (CODE-2), `pkg/admission/webhook/pod_security_uid_lockstep_test.go`.** A pod built with overridden adapter and agent UIDs and the default GID, plus an injected regular container at the overridden agent UID, is denied with the CODE-1 (b) message by a webhook given the overridden UIDs, and the same pod with that container at `podspec.AgentUID` is admitted. Built sidecar, embedded, and egress-capture pods are admitted at the default triple and at an overridden triple.

**Tier 1 (CODE-3), `pkg/controller/sandbox/podspec`.** For the sidecar, embedded, and egress-capture builds: every container has `RunAsGroup == RunAsUser` and both are nonzero; and no container other than `runtime` runs at the agent UID and none other than `adapter` at the adapter UID.

**Tier 1 (CODE-3), `cmd/lenny-egress-capture/main_test.go`.** The capture file is created with mode 0640.

### TEST-1 · Kind and security tiers, and the agent-namespace fixture sweep

1. Tier 9, `tests/tier9_security/admission_security_test.go`: add `bypassCases` for an injected regular container at the agent UID, an injected regular container at the adapter UID, an init container at the adapter UID, a container that omits `runAsGroup`, and `runtime` with the cred-readers GID as `runAsGroup`, each keyed by the CODE-1 message substring in `wantReason`. Give `hardenedPodManifest` explicit container identities.
2. Tier 9, `tests/tier9_security/credential_leakage_test.go`: in `TestCredentialLeakageNetworkEgress`, assert from the `runtime` container's `ls -ln` that the capture file has mode 0640 and the `lenny-cred-readers` GID, and fail when the `runtime` container's `cat` of it fails for any reason other than a missing file. The existing not-yet-written return swallows a permission error.
3. Tier 9, `tests/tier9_security/admission_ephemeral_test.go`: CODE-1 (a) also rejects the attach body, and the API server reports one webhook's denial, so the test accepts a denial naming either `ephemeral-container-cred-guard.lenny.dev` with `EPHEMERAL_CONTAINER_CRED_UID_FORBIDDEN` or `pod-security.lenny.dev` with the CODE-1 (a) message. Keep the body without `runAsGroup`: adding it makes both webhooks admit the attach. Revise the file header, `ephemeralAttachBody`, and diagnosis comments to match. The guard's own decision stays pinned by the tier-1 tests in `pkg/admission/ephemeral_container_cred_guard`.
4. Tier 5, `tests/tier5_e2e_kind/admission_test.go`: give the compliant pod manifest explicit container identities, and add a rejected case for a container at a reserved UID.
5. Fixture sweep. Find candidate fixtures with both `grep -rln 'kind: Pod' tests` (YAML manifests) and `grep -rln 'corev1.Pod{' tests` (typed fixtures). Keep every file that creates the pod in a namespace labelled `lenny.dev/agent-namespace=true` on a cluster where the webhook runs, and give each of its containers an explicit container-level, nonzero, non-reserved `runAsUser` and `runAsGroup`. The sweep includes at least these files: `tests/tier5_e2e_kind/admission_featuregated_test.go`, `tests/tier8_chaos/chaos_helpers_test.go`, `tests/tier8_chaos/component_failure_test.go`, `tests/tier9_security/admission_cred_test.go`, `tests/tier9_security/ops_agent_pod_boundary_test.go`, `tests/tier9_security/network_policy_test.go`, `tests/tier9_security/agent_egress_test.go`, `tests/tier9_security/adapter_peercred_test.go`, `tests/tier9_security/gateway_probe_test.go`, and `tests/tier6_e2e_cloud/agent_objectstore_egress_test.go`. The tier-6 probe moves off its pod-level UID 65532, the default adapter UID, to an explicit container-level identity such as 1000:1000; its run needs operator-provisioned cloud resources, so implement the change and run the local tiers. `tests/tier6_e2e_cloud/behavior_test.go` (an expected rejection) and `tests/tier4_integration/recycle_scrub_path_test.go` (envtest, no webhook) need no change.
6. Before tiers 5, 8, and 9, rebuild and reload the webhook and controller images from this tree, delete every agent pod the controller built before the reload (`kubectl -n <agent-namespace> delete pods -l lenny.dev/managed=true` for each agent namespace) so the warm pools refill under the new builder, and then run the environment preflight in `test-coverage.md`. The preflight's Failed-pod sweep does not find these pods, because they stay Running.

## Edge cases and accepted failure modes

- Images built before CODE-3 produce pods that the CODE-1 webhook rejects. A Kind cluster running the new webhook image with an old controller image fails every warm pod; rebuild both images together.
- A collision between `egressCaptureUID` and an operator-chosen `security.podUIDs` value makes egress-capture pods fail admission. The container is test-only, and the failure is closed.
- A pod built before CODE-3 is denied on every later Pod UPDATE, including the controller's state-label patch and the gateway's tenant and drain-request stamps. An idle pod of this kind fails every claim that selects it and is never recycled. The TEST-1 preflight item deletes these pods.

## Files touched on application (non-spec)

- `pkg/podsecurity/podsecurity.go`, `pkg/podsecurity/podsecurity_test.go`, `pkg/podsecurity/runtimeclass_test.go`, `pkg/podsecurity/fuzz_test.go` (CODE-1)
- `pkg/admission/webhook/pod_security.go`, `pkg/admission/webhook/pod_security_test.go`, `pkg/admission/webhook/pod_security_uid_lockstep_test.go`, `cmd/lenny-webhook/main.go`, `tests/integration/admission_policy_test.go`, `charts/lenny/templates/admission-policies/pod-security-webhook.yaml` (CODE-2)
- `pkg/controller/sandbox/podspec/podspec.go`, `pkg/controller/sandbox/podspec/podspec_test.go`, `pkg/controller/sandbox/podspec/egress_capture_test.go`, `cmd/lenny-egress-capture/main.go`, `cmd/lenny-egress-capture/main_test.go` (CODE-3)
- `tests/tier9_security/admission_security_test.go`, `tests/tier9_security/credential_leakage_test.go`, `tests/tier9_security/admission_ephemeral_test.go`, `tests/tier5_e2e_kind/admission_test.go`, and the sweep files listed in TEST-1 (TEST-1)
- `docs/operator-guide/namespace-and-isolation.md` (DOCS-1)
