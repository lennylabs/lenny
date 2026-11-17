# Design evidence

This directory holds the test evidence the design documents under `designs/` cite. Each subfolder is named after the design file that primarily relies on its contents, and its `README.md` states what ran, in what environment, and what was left out.

| Design file | Evidence |
| :-- | :-- |
| `0127-egress-and-git` | `egress-and-git-design/`, and `user-git-identity-design/spike-gh/` for the git path under candidate T |
| `0152-user-git-identity` | `user-git-identity-design/` |
| `0151-user-credential-delivery` | `user-git-identity-design/spike-gh/`, which the two designs share |
| `0128-forensics-evidence-preservation` | No run of its own. It cites `egress-and-git-design/` for the evidence service run. |
| `0148-tenant-outbound-connector` | `tenant-outbound-connector-design/spike-oc4/` for decision 4, and `egress-and-git-design/` for one earlier run |
| `0145-session-isolation` | `session-isolation-design/supervisor-prototype/`, the prototype supervisor that `0153-workspace-materialization` and `0131-incremental-checkpoint` also cite |
| `0142-security-oversight` | No test ran for this design. |
| `0149-tenant-scoping-identity` | No test ran for this design. |
| `0147-tenant-derived-runtimes` | `tenant-derived-runtimes-design/spike-warm/` for warm clones |
| `0144-session-derive` | No test ran for this design. |
| `0121-billing-resource-attribution` | No test ran for this design. |
| `0118-agent-pod-layout` | `agent-pod-layout-design/attribution-and-quota-spike/`, which `0127-egress-and-git` and `0145-session-isolation` also cite for slot attribution |
| `0138-rest-only-client-api` | `rest-only-client-api-design/`, the spike report and its proof of concept |
| `0124-connector-removal` | `connector-removal-design/`: the condensed tools-and-egress study, the summaries of the fidelity and remote-child studies, the tool-server spike's results with two summary tables of its logs, and the harness-egress spike's results |
| `0136-placement` | `placement-design/`, the research of 2026-11-15 on clusters that span regions and on sandbox runtimes, which `0145-session-isolation` also cites |
| `0143-service-account-credentials` | `service-account-credentials-design/`, the security review of 2026-11-15 of its revision 1. No test ran for this design. |
| `0137-pod-lifecycle-controller` | `pod-lifecycle-controller-design/`, the review of 2026-11-15 of upstream `kubernetes-sigs/agent-sandbox` against Lenny's pod lifecycle. No test ran for this design. |
| No design yet (the specification's structure) | `spec-structure-design/spike/`, the spike of 2026-11-16 that maps the specification onto a tree organized by capability and writes the warm pools and pod lifecycle slice in full |
| No design yet (the specification's structure) | `spec-structure-design/spike-migration/`, the spike of 2026-11-16 that migrates the pod claimer's specification area and then its code |

These files are records of throwaway test runs. They are not product code, and they carry no specification citation. Go source files carry a `.txt` suffix so that they do not join the repository's Go module. Private keys and downloaded third-party files are excluded, and each folder's `README.md` records the URLs and versions of the downloads.

A result that reaches a recording stub proves forwarding only. No production component and no live provider write has run.
