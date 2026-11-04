# Design evidence

This directory holds the test evidence the design documents under `designs/` cite. Each subfolder is named after the design file that primarily relies on its contents, and its `README.md` states what ran, in what environment, and what was left out.

| Design file | Evidence |
| :-- | :-- |
| `egress-and-git-design.md` | `egress-and-git-design/`, and `user-git-identity-design/spike-gh/` for the git path under candidate T |
| `user-git-identity-design.md` | `user-git-identity-design/` |
| `user-credential-delivery-design.md` | `user-git-identity-design/spike-gh/`, which the two designs share |
| `forensics-evidence-preservation-design.md` | No run of its own. It cites `egress-and-git-design/` for the evidence service run. |
| `tenant-outbound-connector-design.md` | No run of its own. It cites `egress-and-git-design/` for one run. |
| `security-oversight-design.md` | No test ran for this design. |
| `tenant-scoping-identity-design.md` | No test ran for this design. |
| `tenant-derived-runtimes-design.md` | No test ran for this design. |

These files are records of throwaway test runs. They are not product code, and they carry no specification citation. Go source files carry a `.txt` suffix so that they do not join the repository's Go module. Private keys and downloaded third-party files are excluded, and each folder's `README.md` records the URLs and versions of the downloads.

A result that reaches a recording stub proves forwarding only. No production component and no live provider write has run.
