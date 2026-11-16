# Design evidence

This directory holds the test evidence the design documents under `designs/` cite. Each subfolder is named after the design file that primarily relies on its contents, and its `README.md` states what ran, in what environment, and what was left out.

| Design file | Evidence |
| :-- | :-- |
| `egress-and-git-design.md` | `egress-and-git-design/`, and `user-git-identity-design/spike-gh/` for the git path under candidate T |
| `user-git-identity-design.md` | `user-git-identity-design/` |
| `user-credential-delivery-design.md` | `user-git-identity-design/spike-gh/`, which the two designs share |
| `forensics-evidence-preservation-design.md` | No run of its own. It cites `egress-and-git-design/` for the evidence service run. |
| `tenant-outbound-connector-design.md` | `tenant-outbound-connector-design/spike-oc4/` for decision 4, and `egress-and-git-design/` for one earlier run |
| `session-isolation-design.md` | `session-isolation-design/supervisor-prototype/`, the prototype supervisor that `workspace-materialization-design.md` and `incremental-checkpoint-design.md` also cite |
| `security-oversight-design.md` | No test ran for this design. |
| `tenant-scoping-identity-design.md` | No test ran for this design. |
| `tenant-derived-runtimes-design.md` | `tenant-derived-runtimes-design/spike-warm/` for warm clones |
| `session-derive-design.md` | No test ran for this design. |
| `billing-resource-attribution-design.md` | No test ran for this design. |
| `agent-pod-layout-design.md` | `agent-pod-layout-design/attribution-and-quota-spike/`, which `egress-and-git-design.md` and `session-isolation-design.md` also cite for slot attribution |
| `rest-only-client-api-design.md` | `rest-only-client-api-design/`, the spike report and its proof of concept |
| `connector-removal-design.md` | `connector-removal-design/`: the condensed tools-and-egress study, the summaries of the fidelity and remote-child studies, the tool-server spike's results with two summary tables of its logs, and the harness-egress spike's results |
| `placement-design.md` | `placement-design/`, the research of 2026-11-15 on clusters that span regions and on sandbox runtimes, which `session-isolation-design.md` also cites |
| `service-account-credentials-design.md` | `service-account-credentials-design/`, the security review of 2026-11-15 of its revision 1. No test ran for this design. |
| `pod-lifecycle-controller-design.md` | `pod-lifecycle-controller-design/`, the review of 2026-11-15 of upstream `kubernetes-sigs/agent-sandbox` against Lenny's pod lifecycle. No test ran for this design. |

These files are records of throwaway test runs. They are not product code, and they carry no specification citation. Go source files carry a `.txt` suffix so that they do not join the repository's Go module. Private keys and downloaded third-party files are excluded, and each folder's `README.md` records the URLs and versions of the downloads.

A result that reaches a recording stub proves forwarding only. No production component and no live provider write has run.
