# Deviations: Only the runtime container runs at the agent UID, and every agent-pod container sets runAsGroup

The implementor owns this file. It stays empty until an implementation records a departure from what the proposal states.

## Accepted: Egress-capture file mode set through a named constant and helper

**Status:** accepted

**What the proposal says.** Non-spec changes, CODE-3 item 4, states that `cmd/lenny-egress-capture/main.go` opens the capture file with mode `0o640` in place of `0o600`, with the explanatory comment next to that call.

**What landed instead.** In `cmd/lenny-egress-capture/main.go`, the mode is the named constant `captureFileMode = 0o640`, and a helper `openCaptureFile(path)` opens the file. The explanatory comment sits on the constant, and `main()` calls the helper.

**Why.** The test plan requires a tier-1 test that the file is created with mode `0640`. The `main()` function exits the process and cannot be tested directly, so step S2 moved the open call into a helper that the test can call. Step S2 reports that the resulting behavior is the same as the staged change.

**What a later reader would otherwise get wrong.** A reader looking for the `0o640` literal and its comment at the open call inside `main()` does not find them there. The mode and its comment are on `captureFileMode`, and the open call is in `openCaptureFile`.

## Accepted: Tier-3 contract test for the lenny-pod-security webhook and extra tier-1 webhook cases

**Status:** accepted

**What the proposal says.** Non-spec changes, Testing, lists tier-1 CODE-2 webhook tests for an injected regular container at the agent UID, an init container at the adapter UID, and a pod-level-only `runAsUser`. Step S3 lists tiers 0, 1, and 3, and the proposal stages no tier-3 test file.

**What landed instead.** Step S3 added every staged webhook case. It also added `tests/tier3_contract/admission_pod_security/container_identity_test.go` under the `contract` build tag. The test posts a raw AdmissionReview to `webhook.Handler(PodSecurity(...))` and checks the deny reply (403 with the reserved-UID message) and the admit reply. Step S3 registered the file under §13.1 in `tests/spec-map.json` so that `lenny-test validate-maps` passes. Step S3 also added tier-1 webhook cases for an embedded-pod init container named `adapter` and for an ephemeral container with no `runAsGroup`.

**Why.** Step S3 reports that the step lists tier 3, that the webhook's AdmissionReview reply is the wire contract this change affects, and that no contract test existed for `lenny-pod-security`.

**What a later reader would otherwise get wrong.** A reader comparing the tree against the proposal's test plan finds a tier-3 test file, a `tests/spec-map.json` entry, and two tier-1 webhook cases that the proposal does not stage, and has no record of which step added them or why.

## Accepted: Internal constants and violation formatter in pkg/podsecurity

**Status:** accepted

**What the proposal says.** Non-spec changes, CODE-1 item 2, gives the helper signature and the two message formats. It names no constants.

**What landed instead.** `pkg/podsecurity/podsecurity.go` adds the unexported constants `adapterContainerName` and `runtimeContainerName`, and an unexported `reservedUIDViolation` formatter that `identityViolations` uses. Step S3 reports that the message text and the violation order are as staged.

**Why.** Step S3 reports that the adapter and runtime container names would otherwise appear as repeated string literals, and that the constants and the formatter are internal and change no surface the proposal defines.

**What a later reader would otherwise get wrong.** A reader looking for the container names as string literals and for the message formats inline in `identityViolations` does not find them there. The names are on `adapterContainerName` and `runtimeContainerName`, and the reserved-UID message is built by `reservedUIDViolation`.

## Accepted: Sockethog fault container moved off the adapter UID in the peer-credential fault test

**Status:** accepted

**What the proposal says.** Non-spec changes, TEST-1 item 5, lists the fixture files to bring into conformance. It does not mention the native-sidecar fault container in `tests/tier9_security/adapter_peercred_test.go`, which runs at the adapter UID.

**What landed instead.** In `faultPodManifest` in `tests/tier9_security/adapter_peercred_test.go`, the `sockethog` init container runs as `1000:1000` in place of `runAsUser: 65532`, and the adapter container gained `runAsGroup: 65532`. A comment states that an abstract socket name carries no file permissions, so the fault does not depend on the fault container sharing the adapter UID.

**Why.** Step S4 reports that the `sockethog` container ran at the reserved adapter UID, which the container identity clause rejects in an agent namespace. The fault namespace has no agent-namespace label today, but the sweep asks for non-reserved identities on every container in the listed files. Step S4 reports that the live tier-9 fault test still passes.

**What a later reader would otherwise get wrong.** A reader comparing the tree against the TEST-1 sweep finds an edit to `adapter_peercred_test.go` that the proposal does not list, and could assume the fault still requires the adapter UID or that the change altered what the test exercises.

## Accepted: Cluster-free tier-9 manifest admission checks and spec-map entry

**Status:** accepted

**What the proposal says.** Non-spec changes, TEST-1, lists the tier-9 cases and the fixture sweep. It adds no cluster-free manifest checks and no `tests/spec-map.json` entries.

**What landed instead.** Step S4 added `tests/tier9_security/admission_manifest_offline_test.go`, which runs every tier-9 bypass, positive-control, and credential manifest through `webhook.PodSecurity` in-process. Step S4 mapped the new file under the §13.1 entry in `tests/spec-map.json`.

**Why.** Step S4 reports that these checks catch a fixture that the webhook rejects for an unintended reason without needing a Kind cluster, and that the `validate-maps` gate requires every test file to appear in `tests/spec-map.json`.

**What a later reader would otherwise get wrong.** A reader comparing the tree against the proposal's test plan finds a tier-9 test file and a `tests/spec-map.json` entry that the proposal does not stage, and has no record of which step added them or why.

## Accepted: runAsGroup added to lenny-system probe pods outside the webhook's scope

**Status:** accepted

**What the proposal says.** Non-spec changes, TEST-1 item 5, sweeps the `lenny-system` probe pods in the `chaos_helpers`, `network_policy`, and `gateway_probe` fixtures as agent-namespace fixtures.

**What landed instead.** Those probe containers gained a container-level `runAsGroup` equal to their `runAsUser` (100). Their comments state that `lenny-system` is outside the pod-security webhook's scope.

**Why.** Step S4 reports that those pods are created in `lenny-system`, which the pod-security webhook does not cover. Step S4 applied the listed sweep to them, and reports that the edit has no effect on admission.

**What a later reader would otherwise get wrong.** A reader following the proposal could conclude that the pod-security webhook admits or rejects these probe pods and that the added `runAsGroup` is required for admission. The webhook does not evaluate pods in `lenny-system`, and the field is present only because the sweep listed those files.

## Accepted: Operator-guide container identity item names the pods/ephemeralcontainers subresource

**Status:** accepted

**What the proposal says.** Non-spec changes, DOCS-1 item 1, describes the operator-guide container identity item as validating every Pod `CREATE` and `UPDATE` in an agent namespace after mutating admission. It does not name the `pods/ephemeralcontainers` subresource.

**What landed instead.** In `docs/operator-guide/namespace-and-isolation.md`, the first sentence of item 9 reads: "Validates every Pod `CREATE` and `UPDATE`, and every `UPDATE` to the `pods/ephemeralcontainers` subresource, in an agent namespace after mutating admission, under every RuntimeClass."

**Why.** Step S5 reports that the landed §13.1 **Container identity.** paragraph and `charts/lenny/templates/admission-policies/pod-security-webhook.yaml` both include the subresource. `kubectl debug` attaches ephemeral containers through that subresource, and such a request is not a Pod `UPDATE`, so the staged wording understated the webhook's scope. Step S5 reports that the change follows the design-review finding.

**What a later reader would otherwise get wrong.** A reader comparing the operator guide against the staged DOCS-1 text finds a clause about the `pods/ephemeralcontainers` subresource that the proposal does not stage. A reader relying on the staged text alone could conclude that the webhook does not evaluate ephemeral containers attached through `kubectl debug`.
