---
id: CLAIM-I4
kind: build-gap
severity: high
requirements: [CLAIM-R4, POOL-R5, POOL-R6, kubernetes#podset.list]
change: null
legacy: null
---

# The claim set has no producer of pod sets, pod-set labels, or pod hashes

CLAIM-R4 limits every acquisition to the claim set: the pod sets of the session's pool whose `spec.podHash` is the pod hash of the session's revision. The claimer now computes the claim set (`claimSet` and `listCandidates` in `pkg/gateway/core/podclaimer/claimset.go`) from a `PodSetLister` and the request's `PodHash`, and lists candidates by `lenny.dev/pool` together with `lenny.dev/pod-set in (...)`. The tests in `claimset_test.go` pin it.

Nothing in the tree feeds it:

- No `PodSet` kind exists, so no Kubernetes implementation of `PodSetLister` can list one (`kubernetes#podset.list`). The gateway wires no lister, and with no lister the claimer lists by the pool label alone, which CLAIM-R4 forbids.
- The WarmPoolController stamps no `lenny.dev/pod-set` label on a `Sandbox` or a pod (POOL-R5), so a wired lister would find no candidates.
- No revision selection exists (RV-2), so no session has a pod hash. `ClaimRequest.PodHash` and `SlotRequest.PodHash` stay empty at every caller (`pkg/gateway/podlifecycle/podsession/bindclaim.go`, `resume.go`, and the session server).
- The retry and resume rule of CLAIM-R4 (claim from the recorded revision's pod sets while one exists, and otherwise move to the revision with the highest weight under RV-6) belongs to the resume driver, which records no revision.

The claimer side is done. The rest lands with the runtime revisions change: the `PodSet` kind and its lister, the pod-set label on pods, revision selection that fills `PodHash`, and the wiring that sets `Claimer.PodSets` and `SlotClaimer.PodSets`. Once a lister is always wired, the nil branch of `listCandidates` is deleted.
