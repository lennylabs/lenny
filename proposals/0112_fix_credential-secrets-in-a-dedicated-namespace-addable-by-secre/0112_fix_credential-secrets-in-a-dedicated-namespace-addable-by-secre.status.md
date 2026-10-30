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

The spec loop ran 12 rounds and did not converge. The loop included 5 full-pool sweeps over the spec pool and fixed 21 findings. The findings listed below were not closed by the run and remain open. The run did not settle them, so this proposal does not record them as decided.

Open after the run:

- Operator-run listing of stray Secrets. The summary's Open decisions section records this as the one open decision: whether to add a read-only `lenny-ctl` command, run under the operator's own Kubernetes credentials, that lists the Secrets in the credentials namespace that no credential pool references and that Lenny did not write. The loop derived no recommendation. A listing run by a Lenny identity conflicts with SPEC-2 (f) rule 7.
- Writes to a deleting tenant. Nothing refuses platform-admin pool writes against a tenant in the `deleting` state. The gap predates this proposal, and the review did not decide whether the summary's defects list records it.
- Unverified claim: a tenant-admin can name any IAM role ARN for the Token Service to assume. The claim predates this proposal. The owner or a security pass decides it.
- The §17.6 kube-apiserver CIDR preflight row still describes the egress as gateway egress. No staged edit covers it.
- `docs/runbooks/credential-revocation.md` Step 4 rotates credentials with `kubectl ... -n lenny-system`. No DOCS-1 bullet covers that step.
- `docs/api/admin.md` omits `tenant-viewer` from the GET credential-pool rows of its CRUD table. No DOCS-1 bullet covers the omission.
- The §24.5 `add-credential` synopsis reads `--pool <name> --provider <p>`, which matches no CLI flag. The mismatch predates this proposal.
- The §15.1 credential-pool create row names the query parameter `?tenant_id=`, and the handlers read `tenantId`.
- `docs/operator-guide/installation.md` Step 3 creates the LLM credential Secret in `lenny-system` before the chart is installed, which SPEC-2 (f) makes incorrect. No DOCS-1 bullet lists the file.
- `docs/operator-guide/installation.md` and `docs/operator-guide/lenny-ctl.md` describe a `--force-update` upsert as replacing a differing resource. After this proposal, a forced credential-pool update also deletes each Lenny-written Secret that the seed drops (SPEC-2 (f) rule 3). DOCS-1 covers `lenny-ctl.md` only for roles and rotation.

Findings for the non-spec loop, which did not run:

- The Token Service Kubernetes client uses the client-go default rate limits. The finding asks for an explicit, overridable QPS and burst that follow the gateway's cluster QPS and burst flags (CODE-2 or CODE-6).
- CODE-3 step 5 returns `412 ETAG_MISMATCH` for an unprobed introduced reference on the locked row in `POST .../credentials`, which takes no `If-Match`. No spec text names a 412 for this case.
- The OpenAPI re-enable route's `x-lenny-required-role` and the minimum role in `me.go` must follow SPEC-7 (d).
- If a fixer adds the no-Token-Service VCS read outcome to SPEC-3 (c), DOCS-1 carries that outcome to the operator page for the dev profile. The staged spec does not state the outcome yet.
- DOCS-1 adds the credential-Secret causes of a stalled Phase 4 to `docs/runbooks/tenant-deletion-overdue.md`, and updates the opening impact sentence of `docs/runbooks/token-service-outage.md` for the VCS read.
- DOCS-1 adds a `TOKEN_SERVICE_NOT_CONFIGURED` row to `docs/reference/error-catalog.md` and names that code in the `docs/api/admin.md` bullet.
