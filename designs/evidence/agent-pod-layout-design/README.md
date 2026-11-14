# Evidence for agent-pod-layout-design.md

`attribution-and-quota-spike/` is the AWS spike of 2026-11-13 (run 2026-11-14 UTC) on one `m8i.2xlarge` node with nested virtualization, Amazon Linux 2023, kernel 6.12.115, containerd 2.4.2, runc 1.5.3, gVisor `release-20261109.0`, Kata Containers 4.3.1 (guest kernel 6.18.56), and k3s v1.37.2, with the kubelet's directory on XFS mounted `noatime,prjquota`.

- `RESULTS.md` is the report. Every claim carries `[measured]` or `[inference]`.
- Test 1 measured per-slot connection attribution (`sock_diag` and `/proc/net/tcp`) under runc with and without user namespaces, gVisor with its own network stack and with host networking, and Kata virtio-fs and block-plain. `egress-and-git-design.md` and `session-isolation-design.md` cite it.
- Test 2 measured project quotas: detection from an unprivileged pod and from a node agent, assignment, `statfs` from inside the pod, mode-000 directories, behaviour at the limit, the kubelet's quota monitoring feature gate, and gVisor and Kata.
- `aq/` is the test harness, a static Go binary. Its Go files carry a `.txt` suffix so that they do not join the repository's Go module.
- `aws/` holds the node setup, run, and cleanup scripts. `results/` holds the raw per-test output and `versions.txt`. `logs/` holds the run logs.

The run cost $0.27, and every resource was deleted and checked by ID, as `RESULTS.md` records. The container image built for the run is not kept.
