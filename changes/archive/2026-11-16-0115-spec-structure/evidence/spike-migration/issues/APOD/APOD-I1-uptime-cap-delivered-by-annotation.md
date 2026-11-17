---
id: APOD-I1
kind: build-gap
severity: medium
requirements: [APOD-R8]
change: null
legacy: null
---

# The gateway delivers the uptime cap through an annotation the pages do not define

APOD-R8 states that the WarmPoolController derives a recycling pod's age from its creation time and drains it past `sessionPolicy.recycle.maxPodUptimeSeconds`. The `PodSet` spec carries that setting (`interfaces/kubernetes/podset.md`), and `labels-and-annotations.md` defines no `lenny.dev/max-pod-uptime-seconds` annotation.

Today no CRD the WarmPoolController reconciles carries the cap, so the gateway stamps it on every pod it places a slot on (`StampMaxPodUptime` in `pkg/gateway/core/podclaimer/podstamps.go`, called from `reserveSlot`, and from the resume path in `pkg/gateway/podlifecycle/podsession/resume.go`), and the controller's `reconcileUptime` arm reads it (`pkg/controller/warmpool/pod_reconciler.go`).

Removing the stamp inside the claimer would stop every uptime drain, because the controller would have no cap to read. The removal follows the step that gives the controller the `PodSet` (or the current `SandboxWarmPool`) field. That step deletes `StampMaxPodUptime`, its call sites, the annotation constant in `pkg/apis/lenny/v1alpha1/annotations.go`, and the tests in `maxpoduptime_test.go`.
