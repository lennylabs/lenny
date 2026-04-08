# Review Findings — Perspective 19: Build Sequence & Implementation Risk (Iteration 14)

**Spec file:** `technical-design.md`
**Section reviewed:** §18 (Build Sequence), with cross-references to §4.1, §4.9, §17.5, §17.6, §17.9
**Prior findings skipped:** BLD-001 through BLD-034

## Findings

### BLD-035 Phase 5.4 Deliverables Infeasible on Managed Kubernetes [Medium]
**Section:** 18 (Phase 5.4)

Phase 5.4 lists three deliverables for etcd encryption at rest. Two are infeasible on managed Kubernetes (the primary deployment target per §17.9):

1. **Deliverable (1):** "EncryptionConfiguration manifest included in Helm chart." `EncryptionConfiguration` is not a Kubernetes API resource — it is a static file referenced by the kube-apiserver `--encryption-provider-config` flag on the control plane node. On managed Kubernetes (EKS, GKE, AKS), etcd encryption is configured through cloud-provider APIs (e.g., `aws eks create-cluster --encryption-config`, `gcloud container clusters update --database-encryption-key`), not through any manifest that can be deployed via Helm. On self-managed clusters, it is a static file on the control plane, also not deployable via `kubectl apply` or Helm. Including it in a Helm chart is architecturally incorrect.

2. **Deliverable (2):** "CI gate verifying that a test Secret written to the cluster is stored encrypted in etcd (confirmed via `etcdctl get` on the raw key)." On managed Kubernetes, operators have no direct etcd access. `etcdctl` cannot reach the managed etcd endpoint. The spec itself acknowledges this at §17.6 (preflight Job, line 8045): etcd encryption "cannot be verified programmatically by the preflight Job (it lacks etcd access)." The same limitation applies to CI.

The contradiction is internal: §4.9 (line 1036) and §17.6 (line 8045) both correctly state that etcd encryption verification is the operator's responsibility and cannot be done programmatically, yet Phase 5.4 lists programmatic CI verification and a Helm-deployed EncryptionConfiguration manifest as hard deliverables gating Phase 5.5.

**Recommendation:** Replace Phase 5.4 deliverables (1) and (2) with: (1) documentation and Helm-values-driven preflight validation — the Helm chart includes `etcdEncryption.verified: false` (default) which the preflight Job checks; the operator must set it to `true` after manually confirming encryption is active using the provider-specific verification commands already documented in §4.9. (2) For self-managed clusters only, include a reference `EncryptionConfiguration` file in `docs/examples/` (not in the Helm chart) with instructions for applying it to the kube-apiserver. (3) Retain the CI gate but scope it to self-managed test clusters where `etcdctl` access is available; document it as not applicable to managed-K8s CI environments.

---

**Total findings this iteration:** 1 (0 Critical, 0 High, 1 Medium)
