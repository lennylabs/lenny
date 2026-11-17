---
id: CLAIM-I1
kind: spec-defect
severity: low
requirements: [CLAIM-R2, CLAIM-R5, CLAIM-R9, CLAIM-R10, kubernetes#podclaim.updateStatus, kubernetes#podclaim.delete]
change: null
legacy: null
---

# Cross-references to the fencing and reserved-hold rules point one number low

Several references to CLAIM requirements name the requirement numbered one below the one that states the rule. The pattern matches a renumbering after CLAIM-R4 (the claim set) was inserted. Every link resolves, so `specref.py check` passes, and only a reader notices.

- The term "Reserved hold" on `pod-claims.md` cites CLAIM-R9; the hold is CLAIM-R10.
- CLAIM-R2's first bullet cites "CLAIM-R9, CLAIM-R5" for the hold's end; the precondition-guarded deletion is CLAIM-R10.
- CLAIM-R5 says the gateway "ends the hold with the precondition-guarded deletion of CLAIM-R9"; the deletion is CLAIM-R10.
- `interfaces/kubernetes/podclaim.md`, `kubernetes#podclaim.updateStatus`: both outcomes cite CLAIM-R8 (the pinned idle pod); the ordering rule is CLAIM-R9.
- `interfaces/kubernetes/podclaim.md`, `kubernetes#podclaim.delete`: the `409 Conflict` outcome cites CLAIM-R9; the aborted expiry is CLAIM-R10.

The code in `pkg/gateway/core/podclaimer` cites CLAIM-R10 for the hold and CLAIM-R9 for the binding-state writes. A gate that compares a link's text with the requirement title at its target would catch this class of error.
