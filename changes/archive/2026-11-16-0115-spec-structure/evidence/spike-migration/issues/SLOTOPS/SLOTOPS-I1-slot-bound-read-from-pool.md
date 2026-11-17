---
id: SLOTOPS-I1
kind: build-gap
severity: medium
requirements: [SLOTOPS-R1, APOD-R2]
change: null
legacy: null
---

# The slot reservation compares against the pool's current setting

SLOTOPS-R1 requires the reservation to compare the pod's occupancy with the slot count recorded on the pod, `AgentPod.spec.slotCount`, whatever the pool's current `sessionPolicy.maxConcurrentSessionsPerPod` is. A pod is fixed at creation (APOD-R2), so an edit to the pool must not change how many sessions an existing pod takes.

`reserveSlot` in `pkg/gateway/core/podclaimer/slotclaimer.go` passes `SlotRequest.MaxConcurrentSessions` to `slotcounter.Counter.Reserve`. The session server fills that field from the pool's current policy. The `Sandbox` type has no slot count field, so the claimer has nothing to read.

The fix needs the slot count on the pod's custom resource first (the `AgentPod` kind of `interfaces/kubernetes/agentpod.md`, written by the WarmPoolController at creation). The claimer then reads it from the candidate it already lists, and `SlotRequest.MaxConcurrentSessions` leaves the request.
