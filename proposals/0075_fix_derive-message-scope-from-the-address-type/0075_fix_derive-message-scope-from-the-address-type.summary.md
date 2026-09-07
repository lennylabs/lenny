# Summary: Derive message scope from the address type

This document stages the proposed specification and test changes. It does not modify any spec, code, or
doc file. Apply the changes in the "Proposed changes" section after sign-off.

**This draft has not been through adversarial review.** It records a direction and the measurements behind
it. The staged edits are indicative rather than final, and the open questions in §7 are open. Run the
change-proposal convergence loop on it before sign-off.

**Proposal 0076's OD3 has been answered, and this proposal is the successor it names.** The reviewer
answered Question A yes: `CoordinatorFenceRequest` is session-scoped once 0076's CODE-1 records the
generation on the slot entry its identifier resolves. Question B leaves the `spec/04` §4.1 edit to a
successor rather than staging it in 0076, and this proposal is that successor, because SPEC-1 retires the
table those edits would have touched. The answer removes the rule's only counterexample, so the schema,
code, and documentation deliverables an earlier revision carried are dropped; §11 records that.

## Summary

**What changes.** The specification states one derivation rule in place of 0073's classification table, and
the tier-0 gate that reconciles that table against the proto is replaced by a smaller gate. A tier-3
comment that claims the fence's address is pinned in its file is corrected in the same change, because the
comment's membership rule is the table this proposal retires. No proto, code, or documentation file is
touched.

**What is fixed.** Nothing at runtime. This is a classification change. The defect it corrects is that 0073
spends 32 rows of specification text and a tier-0 gate accommodating a single message whose classification
disagreed with the rule the other 30 obey, and whose classification 0076's OD3 has since changed to agree
with it.

**Watch out for.** Proposal 0073 is converged and is not reopened; every edit here applies to text 0073
introduces. Proposal 0076 rewrites the fence handler's state, and its OD3 answer settles the
classification this proposal derives, so this proposal is sequenced after 0076 rather than independent of
it, and its spec edits are written against 0076's applied text. The value rule in 0073 §4.2, which is what
actually changes adapter behavior, is untouched.
