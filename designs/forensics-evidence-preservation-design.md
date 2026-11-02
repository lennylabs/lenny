# Forensics and evidence preservation

Status: outline for owner review, 2026-11-02. Nothing here is designed in detail, built, or staged. Proposals carry the label FE-n.

## Problem

An operator's forensics team investigates prompt-injection exfiltration, abuse, suspected cross-tenant breaches, and leaked credentials. Lenny records a great deal, but nothing can freeze evidence. Legal holds cover sessions and artifacts only (§12.8, `POST /v1/admin/legal-hold`), a tenant admin can see and release them, and force-terminate and tenant suspension release the pod. Tenant teardown deletes the audit store. Agent activity, such as commands the agent ran, is not recorded at all.

## Scope

- Quarantining a pod and a mirror store.
- Holds and retention that serve an investigation against the tenant under investigation.
- Reconciling evidence sources after a compromise.
- Capture procedures and an abuse workflow.
- Agent activity evidence.

Out of scope: producing egress, tunnel, git, and credential records, which `egress-and-git-design.md` owns. Who may read evidence belongs to `security-oversight-design.md`.

## Items

| Label | Item | Summary |
| :-- | :-- | :-- |
| FE-1 | Quarantine | Sets the `quarantined` binding state, blocks retirement and drain, keeps tenant work from the pod, and records custody. |
| FE-2 | Mirror store hold | Sets the store's held flag, which stops re-chown, repack, and purge, and takes a volume snapshot. |
| FE-3 | Platform-only holds and a security retention class | Holds a tenant cannot see or release, with tenant and time-range scope, and a retention class that survives tenant teardown. The §12.8 ledger reads `audit_range`, which has no setter today, and the §16.4 partition GC checks only the SIEM guard. |
| FE-4 | Reconciler | Compares Envoy records, decisions, and mints, and reports disagreement. A compromised decision service writes its own records and supplies Envoy's tags, so the reconciler needs a source it does not control. |
| FE-5 | Capture runbook and abuse workflow | §17.7 has no forensic runbook. Covers kubelet checkpointing, memory capture, the query from an abuse report to a tenant, and chain of custody. |
| FE-6 | Agent activity streams | Bash commands, files opened, and similar evidence, from feedback received on Lenny. New `agent.activity` streams in the envelope `egress-and-git-design.md` defines (see its "Evidence streams"). |

## Agent activity: decisions to make

- **Producer.** An adapter or runtime producer is `self_reported` and sees only what the agent tells it. A node-level sensor (eBPF, for example) is `observed` and sees every process, but it adds a privileged DaemonSet on agent nodes, which `egress-and-git-design.md` otherwise avoids.
- **Sensitivity.** Commands and paths can contain secrets, so the stream needs its own permission, retention, and redaction, and a per-session key through `payloadKeyRef`, so that `DeleteByUser` can destroy the key while anchored digests still verify. Whether the customer or the operator can read it is a question for `security-oversight-design.md`.
- **Volume.** Process and file events exceed egress decisions by orders of magnitude, so sampling rules and per-tenant cost need a decision.
- **Scope.** A customer's security team and the operator's forensics team may want different detail, so this could be several streams.

## Dependencies

| Proposal | Depends on | Why |
| :-- | :-- | :-- |
| FE-1 | EG-2 in `egress-and-git-design.md` | The `quarantined` binding state |
| FE-2 | EG-5 | The held flag |
| FE-3 | EG-10 | The held marker on manifests |
| FE-4 | EG-2, EG-3, EG-5, EG-10 | The streams it reconciles |
| FE-5 | FE-1, FE-2 | The procedures it documents |
| FE-6 | EG-10 | The envelope, ingest, and key field |

## Evidence for the need

- Wiz's guidance says to [preserve forensic evidence before moving workloads](https://www.wiz.io/api/md/academy/container-security/kubernetes-incident-response), and [KEP-2008](https://www.kubernetes.dev/resources/keps/2008/) covers kubelet checkpointing.
- CircleCI's [incident report](https://circleci.com/blog/jan-4-2023-incident-report/) said "there is no way for us to know if your secrets were used".
- Okta's [root cause](https://sec.okta.com/articles/2023/11/unauthorized-access-oktas-support-case-management-system-root-cause) describes missing scope through "an entirely different log event".
- [RFC 7422](https://www.rfc-editor.org/rfc/rfc7422.html) covers connection logging behind shared addresses.

Reddit was unavailable to the research tools, and no first-hand account of investigating an agent incident was found, so the agent-specific evidence is thin.

## Open questions

- Who may set a quarantine, and does the tenant learn of it?
- How does quarantine interact with tenant erasure requests under an open investigation?
- Does a quarantined pod count against the tenant's quota and bill?
