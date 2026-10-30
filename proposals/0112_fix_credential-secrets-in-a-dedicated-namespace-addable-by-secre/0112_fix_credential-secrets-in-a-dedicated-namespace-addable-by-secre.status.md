---
proposal: 0112_fix_credential-secrets-in-a-dedicated-namespace-addable-by-secre
title: Credential Secrets in a dedicated namespace, addable by reference or through the admin API
kind: fix
status: Draft
drafted-date: 2026-10-30
drafted-by: change-proposal
reviewed-date: 
reviewed-by: 
approved-date: 
approved-by: 
implemented-date: 
implemented-by: 
---

## Review history

Recorded 2026-10-30.

The adversarial review ran one spec loop. The non-spec loop did not run.

The spec loop ran four rounds and did not converge. Round 3 ran the mechanism lens alone. The other rounds ran the review lenses listed in the review log. The run fixed 22 findings in the spec loop, and it included one full-pool sweep over the spec pool.

The findings listed below were not closed by the run and remain open. The run did not settle these questions, so the proposal does not record them as decided:

- SPEC-2 (f) **Credentials namespace.** rule 4 says the gateway obtains a stored value only through the Token Service keyed read, while the accepted edge case says a delete response can carry the object. The review log records this contradiction as open in `[spec.4.fix-design-G1.1]`.
- The non-spec targets do not yet name the `DeleteTenant` test fakes that must satisfy the widened `CredentialSecretWriter` (`[spec.4.fix-G1.1]`).
- `docs/operator-guide/installation.md` Step 3 creates the LLM credential Secret in `lenny-system` before the chart is installed, which SPEC-2 (f) makes incorrect. The file is not listed in DOCS-1 (`[spec.4.fix-G3.1]`, `[spec.4.fix-design-G3.1]`).
- The NET-040 lead-in and the §17.6 kube-apiserver CIDR preflight row still describe `kubeApiServerCIDR` as gateway-only, and no staged edit covers them (`[spec.4.review-edit-sites.1]`).
- CODE-4 step 5 gives no outcome for a removal path when no writer is wired and a Lenny-written reference is dropped.
- `docs/runbooks/credential-revocation.md` Step 4 rotates credentials with `kubectl ... -n lenny-system`, and DOCS-1 does not cover that step.
- `docs/api/admin.md` lists `platform-admin, tenant-admin` for the GET credential-pools rows and omits `tenant-viewer`.
- The §24.5 add-credential command syntax, `--pool <name> --provider <p>`, does not match the CLI flags.

The review log also holds two follow-up entries labelled `f1`. They resolve the drafting-stage narrowing question for `secretRef` introduction and rewrite the 0110 entry under **Impacts on other proposals**. They are not review rounds.
