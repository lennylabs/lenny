# Security oversight for customers and the operator

Status: outline for owner review, 2026-11-02. Nothing here is designed in detail, built, or staged. Proposals carry the label SO-n.

## Problem

Lenny treats the tenant admin as the top customer authority, and it keeps most evidence and posture in the operator's custody. A customer's security team has to approve the service and keep watching it, separately from the admin who configures it. It has no read-only role with audit access, no way to constrain the tenant admin's opt-ins, no view of operator access to its tenant, and no independent destination for its evidence. The operator's own rights over the platform's secrets are also broader than their stated need.

## Scope

- A read-only security role.
- Tenant guardrails over the tenant admin's opt-ins, and a posture view.
- A per-tenant SIEM destination.
- Visibility of operator access to a tenant.
- Delegated approvers for risky actions.
- Two defects in operator tooling that this review filed.

Out of scope: producing egress and git evidence, which `egress-and-git-design.md` owns, and preserving evidence during an investigation, which `forensics-evidence-preservation-design.md` owns.

## Items

| Label | Item | Summary |
| :-- | :-- | :-- |
| SO-1 | `security-auditor` role | Reads audit events, evidence streams, posture, and subscriptions, with no session content. A tenant admin cannot remove it without a platform action or a recorded approval, to be decided. §10.2's permission matrix has no audit row today, and `tenant-viewer` reads all session content. |
| SO-2 | Tenant guardrails and posture API | A tenant record writable only by a separate role that can forbid `file` delivery, `allowStoredTokenPush`, tenant-registered upstreams, report-only mode, and host patterns. Weakening emits an event, requires a justification, and can carry a cooldown, following §9.2's and §8.3's weakening precedents. A posture API reports pool sharing, `isolationProfile`, enforcement level, gates, and chain integrity. |
| SO-3 | Per-tenant SIEM destination | `audit.siem.endpoint` is one platform value today, so a regulated tenant's independent copy sits with the operator. |
| SO-4 | Operator access transparency | `admin.impersonation_*` events sit under the platform tenant (§11.7, CMP-058), so a customer cannot see them. Mirror them to the target tenant, with optional per-tenant approval. |
| SO-5 | Delegated approvers | `tool-use/{id}/approve` is limited to the session owner today (§10.2, **Session ownership.**). A security approver, and an approval-required flag on `writableRepositories`. |
| SO-6 | F-25.6.9 | Make the `certManagerExpiring` window and the health thresholds relative to each Certificate's lifetime, and exclude `isCA` Certificates from the remedy. §25.6 changes too. |
| SO-7 | F-25.4.40 | Narrow `lenny-ops`'s Secret rights, give `versionsource.HelmChart` another read, and state in §25.4 which of its rights place it inside the PKI trust boundary. |

## Dependencies

| Proposal | Depends on | Why |
| :-- | :-- | :-- |
| SO-1 | EG-2, EG-8 in `egress-and-git-design.md` | The named permissions on the new reads |
| SO-2 | EG-2 | One write path for opt-ins |
| SO-3 | EG-2 | The evidence export |
| SO-4, SO-5, SO-6, and SO-7 | None | They can land at any point |

`egress-and-git-design.md` does not wait for SO-6: its Certificates carry `lenny.dev/doctor-optout` until then.

## Evidence for the need

- GitHub lets organization owners [configure all firewall settings at the organization level](https://docs.github.com/en/copilot/how-tos/use-copilot-agents/coding-agent/customize-the-agent-firewall), including disabling repository admins' custom rules.
- Microsoft's [Customer Lockbox](https://learn.microsoft.com/purview/customer-lockbox-requests) is an analogue for approval of operator access.
- A vendor-authored [CISO question list](https://blog.sondera.ai/p/ciso-questions-for-agent-vendors) asks whether agent logs are separate from the user's activity logs.

Reddit was unavailable to the research tools and Hacker News returned little, so the evidence for this outline is thin on first-hand posts.

## Open questions

- Who can remove a `security-auditor`, and what stops a tenant admin from doing so?
- Does the SaaS operator's identity broker supply this role, or does Lenny?
- Which guardrails can a customer set without the operator's involvement?
