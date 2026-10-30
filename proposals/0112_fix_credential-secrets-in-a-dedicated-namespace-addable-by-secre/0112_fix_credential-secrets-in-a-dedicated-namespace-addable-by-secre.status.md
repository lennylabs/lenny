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

The spec loop ran eight rounds and did not converge. The run included three full-pool sweeps over the spec pool. The loop fixed 19 findings.

The findings below were not closed by the run and remain open. The run did not settle these questions, so the proposal does not record them as decided.

Spec and summary findings:

- Revocation runbook. `docs/runbooks/credential-revocation.md` Step 4 rotates credentials with `kubectl ... -n lenny-system`, and DOCS-1 does not cover that step.
- Gateway-only CIDR wording. The NET-040 lead-in and the kube-apiserver CIDR preflight row in §17.6 still describe the egress as gateway egress. No staged edit covers them.
- Admin API tenant-viewer. `docs/api/admin.md` omits `tenant-viewer` from the GET credential-pool rows of its CRUD table, and no DOCS-1 bullet covers the omission.
- CLI synopsis. The §24.5 add-credential synopsis, `--pool <name> --provider <p>`, matches no CLI flag. The mismatch predates this proposal.
- Test fakes. CODE-4 and CODE-5 do not name the `DeleteTenant` test fakes that must satisfy the widened `CredentialSecretWriter` interface.
- Installation guide. `docs/operator-guide/installation.md` Step 3 creates the LLM credential Secret in `lenny-system` before the chart is installed, which SPEC-2 (f) makes incorrect. DOCS-1 does not list the file.
- Rotation block. `docs/operator-guide/lenny-ctl.md` "Credential Rotation" shows create, add, and remove steps. DOCS-1 coverage of that block has not been checked.
- Tenant identifier spelling. The §15.1 create row says `?tenant_id=`, and the handlers read `tenantId`. SPEC-7 (a) carries the query spelling forward.
- Writes to a deleting tenant. Nothing refuses platform-admin pool writes against a tenant in the `deleting` state. The gap predates this proposal, and the review has not decided whether the summary's defects list records it.
- Owner decision. The summary's Open decisions section lists the preflight check for the credentials namespace as decision 1, with no recommendation.
- Unverified: a tenant-admin can name any IAM role ARN for the Token Service to assume. The claim is unverified and predates this proposal. The owner or a security pass decides it.

Non-spec findings, recorded for the non-spec loop, which did not run:

- CODE-2 or CODE-6 gives the Token Service Kubernetes client the client-go default rate limits. The finding asks for an explicit, overridable QPS and burst that follow the gateway's cluster QPS and burst flags.
- CODE-3 step 5 returns `412 ETAG_MISMATCH` for an unprobed introduced reference on the locked row in `POST .../credentials`, which takes no `If-Match`. No spec text names a 412 for this case.
- The OpenAPI re-enable route's `x-lenny-required-role` and the minimum role in `me.go` must follow SPEC-7 (d).
- DOCS-1 carries the no-Token-Service VCS read outcome to the operator page for the dev profile, if a fixer adds that outcome to SPEC-3 (c). The staged spec does not state that outcome yet.
- DOCS-1 adds the credential-Secret causes of a stalled Phase 4 to `docs/runbooks/tenant-deletion-overdue.md`, and updates the opening impact sentence of `docs/runbooks/token-service-outage.md` for the VCS read.
- DOCS-1 adds a `TOKEN_SERVICE_NOT_CONFIGURED` row to `docs/reference/error-catalog.md` and names that code in the `docs/api/admin.md` bullet.
