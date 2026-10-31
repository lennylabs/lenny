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

The adversarial review ran two loops. Neither loop converged.

- The spec loop ran 12 rounds and did not converge. It included 5 full-pool sweeps over the spec pool.
- The non-spec loop ran 6 rounds and did not converge. It included 2 full-pool sweeps.

The two loops fixed 52 findings in total. The run did not record a split of that count between the loops.

Findings that the loops did not close remain open. The review log, `proposals/0112_fix_credential-secrets-in-a-dedicated-namespace-addable-by-secre/0112_fix_credential-secrets-in-a-dedicated-namespace-addable-by-secre.review-log.md`, records the disposition of each finding, including the findings the end-of-spec-loop reconciliation carried into the non-spec loop and into DOCS-1. Two findings from the final non-spec round remain open:

- The Token Service `--secret-namespace` flag defaults to the pod namespace or `lenny-system`, while the gateway `--credentials-namespace` flag defaults to `lenny-credentials`. The chart sets both to the same namespace, and off-chart runs fail closed. Whether the mismatch is a defect is not decided.
- With no Token Service configured, a soft-deleted pool keeps its Lenny-written references after the pool DELETE removes their Secrets, so Phase 4 retries until a Token Service is configured. Whether this is a defect is not decided.

The summary's Open decisions section reads "None." The operator-run listing of unreferenced Secrets in the credentials namespace was decided by the orchestrator under the owner's standing delegation, as recorded in the review log entry `orchestrator.decisions-2026-10-30f`. SPEC-11 and CODE-8 stage the listing.

In the final non-spec round, a skeptic refuted the docs-completeness findings raised against the error catalog, the tenant-deletion runbook, the `--force-update` tables, the `docs/api/admin.md` rows, and the deploy-to-cluster namespace count, as recorded in the review log.
