# Evidence for `placement-design.md`

The owner asked on 2026-11-15 how region placement works, whether a Kubernetes cluster can span regions, and which sandbox runtimes have gained traction for AI agents. The file below is the web research that answered those questions. It is a read-only study. Nothing ran against a cluster or a provider.

| File | What it is |
| :-- | :-- |
| `residency-and-sandboxes-research.md` | Research of 2026-11-15 with a source URL on each claim. Claims that could not be confirmed against a primary source are marked unverified, and the author's reasoning is marked as inference. Part 1 covers clusters that span regions: managed offerings, etcd latency guidance, partition behaviour, cross-region networking and cost, and which data lives in the control plane. `placement-design.md` PM-7 **Residency across regions.** rests on it. Part 2 covers sandbox runtimes for AI agents: Kata 4.0 and its VMMs, AWS Nitro Sandboxes, Edera, Firecracker, Hyperlight, kubernetes-sigs/agent-sandbox, and the isolation that sandbox providers use. `session-isolation-design.md` cites it for the Kata VMM note and the owed spike of other VM backends. |

Left out: nothing. The report has no logs or code.
