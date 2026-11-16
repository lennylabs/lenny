# Evidence for `pod-lifecycle-controller-design.md`

On 2026-11-15 the owner asked for a review of the upstream project `kubernetes-sigs/agent-sandbox` against Lenny's pod lifecycle: what upstream offers today, how Lenny's resource kinds compare with upstream's kinds of the same names, which upstream features are worth borrowing, and whether Lenny should depend on upstream. The file below is that review. It is a read-only study. Nothing ran against a cluster, and no upstream code was built or imported.

| File | What it is |
| :-- | :-- |
| `agent-sandbox-review.md` | The review of 2026-11-15, read at Lenny commit `8702041e8` on branch `design/v1-cleanup` and upstream commit `454d3353` (the `v1.0.11` API types). Each upstream claim carries a URL, and claims that could not be confirmed against a primary source are marked unverified. Section 2 compares Lenny's `Sandbox`, `SandboxTemplate`, `SandboxWarmPool`, and `SandboxClaim` with upstream's field by field. Section 5 sets out the dependency options, and section 6 lists the gaps it found in Lenny's specification and code. `pod-lifecycle-controller-design.md` rests on sections 2, 5, and 6. |

While writing revision 1 of the design, the author fetched upstream's `api/v1beta1/sandbox_types.go` and `extensions/api/v1beta1/sandboxwarmpool_types.go` at tag `v1.0.11` from `raw.githubusercontent.com` to confirm three type facts: `Sandbox.status.podIPs` is a list of strings, `SandboxWarmPool.spec.sandboxTemplateRef` is an object with a `name` member, and the `Sandbox` condition types are `Ready`, `Suspended`, `PodScheduled`, and `Finished`. The design's "Sources" lists the URLs.

Left out: the two downloaded upstream files, which are third-party source. The review has no logs or code.
