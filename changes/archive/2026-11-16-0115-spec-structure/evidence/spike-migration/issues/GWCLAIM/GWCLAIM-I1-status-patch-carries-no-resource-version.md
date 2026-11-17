---
id: GWCLAIM-I1
kind: build-gap
severity: high
requirements: [GWCLAIM-R4, CLAIM-R9, kubernetes#podclaim.updateStatus]
change: null
legacy: null
---

# Claim status writes carry no resourceVersion

GWCLAIM-R4 and CLAIM-R9 require every binding-state write to be a patch of the status subresource that carries the `resourceVersion` the writer read, so the API server refuses a write against a stale version. `kubernetes#podclaim.updateStatus` lists `409 Conflict` as the outcome of a stale write.

`applyStatus` in `pkg/gateway/core/podclaimer/bindingstate.go` writes every binding state by Server-Side Apply under the gateway field manager. The apply object carries no `resourceVersion`, so the API server applies it whatever the claim's current version is. A replica that failed over mid-write therefore overwrites a newer state instead of being refused. The fencing argument of CLAIM-R9 does not hold for these writes.

`writeBoundStatus` and `WriteRecyclingStatus` read the claim before they write and could pass the version they read. `WriteReservedStatus`, `WriteRebindStatus`, and `WriteDispositionStatus` do not read the claim first.

The fix sets `ObjectMeta.ResourceVersion` on each apply object from the read the caller made, adds the read where a writer has none, and maps the `409 Conflict` outcome. The tests in `bindingstate_test.go` need a case where a concurrent write lands between the read and the patch.
