---
id: CLAIM-I2
kind: build-gap
severity: medium
requirements: [CLAIM-R10, APOD-R5]
change: null
legacy: null
---

# The rewarmStartedAt stamp has no requirement and cannot be removed inside the claimer

PL-8 of the pod lifecycle controller design removes the SDK re-warm. The target pages have no `sdk_connecting` phase (APOD-R5) and hold a scrubbed pod in `reserved` directly (CLAIM-R10), so the claim's `status.rewarmStartedAt` has no requirement.

The writer is `WriteRewarmStartedStatus` in `pkg/gateway/core/podclaimer/bindingstate.go`. Its only caller is the recycle disposition in `pkg/gateway/session/recycle/scrubreporter_seams.go`, which stamps it on preConnect pools and then waits for a re-warm poll in `recycleboundary.go`. The readers are the WarmPoolController's projection and orphan GC (`pkg/controller/warmpool/occupancy.go`, `gc.go`), the sandbox controller's re-warm (`pkg/controller/sandbox/sdkwarm.go`, `lifecycle/lifecycle.go`), the CRD type, and the adapter proto. Twenty-three Go files outside the claimer name the field, and the recycle package alone holds 191 references to the re-warm.

Deleting the writer alone would leave a preConnect pool's recycle disposition without a path to `reserved`. The removal belongs to the PL-8 step, which changes the recycle disposition, the WarmPoolController, and the sandbox controller together. The claimer keeps the function, without a `spec:` annotation, until that step deletes it with its test `TestRewarmStartedRequiresRecyclingClaim`.
