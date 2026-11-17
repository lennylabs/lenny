---
code: CAPS
type: index
summary: >-
  Lists the capability areas with their summaries and the capabilities in each.
---

# Capabilities

A capability page states what Lenny does end to end: the domain objects it governs, its numbered requirements, the flows across components, its behavioral settings, its behavior under failure, and its security properties. Capabilities are grouped into areas, and each area has an index.

## Areas

| Area | Code | Summary | Capabilities |
| :-- | :-- | :-- | :-- |
| [Runtimes and pools](runtimes-and-pools/index.md) | RTPOOL | Lists the capabilities that decide which runtimes Lenny runs, on which pods, and how those pods are kept warm, claimed, reused, and retired. | RUNTIME, REV, DERIVED, ROUTE, RCLASS, PLACE, ISO, POOL, CLAIM, APOD, OWNPOOL |
| [Sessions](sessions/index.md) | SESAREA | Lists the capabilities that govern a session from creation to its end, including suspension, resume, checkpoints, derive, and isolation between sessions on one pod. | SESLC, RESUME, CKIDX, SDERIVE, SETUP, SLOT, SCRUB, SLOTOPS, SCRUBPROC |
| [Messaging](messaging/index.md) | MSGAREA | Lists the capabilities that carry messages into a session and output out of it: delivery, inboxes, inter-session messages, the event stream, input requests, and client tools. | MSG, INBOX, IMSG, STREAM, INPUT, CTOOL |
| [Workspaces and files](workspaces-and-files/index.md) | WSAREA | Lists the capabilities that build a session's workspace and move files in and out of it. | WSPLAN, MATER, UPLOAD, FILEMSG, FILEX, ARTIFACT, REPO, CLONE |
| [Delegation](delegation/index.md) | DLGAREA | Lists the capabilities that let a session start child sessions and govern the resulting tree. | DLG, DPOL, LEASE, DAVAIL, DTREE, LEASEXT |
| [Tenancy and access](tenancy-and-access/index.md) | TENAREA | Lists the capabilities that define tenants, environments, and principals and decide who may do what. | TENANT, TSET, ENV, PRINC, AUTHN, TOKX, AUTHZ, SECROLE, BROWSER |
| [Credentials](credentials/index.md) | CREDAREA | Lists the capabilities that hold credentials and deliver them to sessions and pods. | CPOOL, UCRED, LINK, SACRED, CDELIV, REPOAUTH, PRAUTH, ADDON, CREQ |
| [Network and egress](network-and-egress/index.md) | NETAREA | Lists the capabilities that decide what network traffic agent pods may send and how it is attributed. | NETISO, EGRESS, EGDEC, DNS, LLMUP, GIT, OPSVC, OUTCONN, EGDEF |
| [Policy and controls](policy-and-controls/index.md) | POLAREA | Lists the capabilities that admit, shape, and refuse requests. | ADMIT, INTCPT, QUOTA, BREAKER |
| [Billing and usage](billing-and-usage/index.md) | BILLAREA | Lists the capabilities that meter usage, enforce budgets, and emit billing events. | BUDGET, RATE, USAGE, BILLEV, ATTR |
| [Observability and operations](observability-and-operations/index.md) | OPSAREA | Lists the capabilities that let operators and DevOps agents observe, diagnose, and remediate the platform. | OPSEV, DIAG, REMED, RUNBOOK, DRIFT, SLO |
| [Security and compliance](security-and-compliance/index.md) | SECAREA | Lists the capabilities that record evidence, oversee security, and meet data obligations. | AUDIT, SIEM, SECOV, FORENSIC, ACTSTR, HOLD, ERASE, RESID, PSEUDO |
| [Deployment and lifecycle](deployment-and-lifecycle/index.md) | DEPAREA | Lists the capabilities that install, size, upgrade, back up, and restore a Lenny deployment. | INSTALL, LOCALDEV, UPGRADE, BACKUP, TIER |
