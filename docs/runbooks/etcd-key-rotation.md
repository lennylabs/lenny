---
layout: default
title: "etcd-key-rotation"
parent: "Runbooks"
triggers: []
components:
  - controlPlane
symptoms:
  - "scheduled rotation of the etcd Secret encryption key is due"
  - "the aescbc encryption key may be compromised and must be replaced"
  - "an operator changed etcdEncryption.aescbcKeys and Secrets must be rewritten"
tags:
  - etcd
  - encryption
  - secrets
  - key-rotation
  - kubernetes
requires:
  - cluster-access
related:
  - etcd-operations
  - credential-revocation
---

# etcd-key-rotation

Procedure to rotate the etcd encryption-at-rest key for Kubernetes Secrets. The Lenny chart renders an `apiserver.config.k8s.io/v1` EncryptionConfiguration into the `lenny-etcd-encryption` ConfigMap (§13.1 Pod Security); the operator wires that document into every control-plane `kube-apiserver` via `--encryption-provider-config`. The aescbc key inside that document encrypts every Kubernetes Secret at rest, including the credential-pool API keys Lenny stores via `secretRef`.

This runbook applies to self-managed clusters where the operator controls the apiserver flags. On cloud-managed clusters (EKS, GKE, AKS) the `secrets` resource is encrypted by the provider's envelope-encryption integration (AWS KMS, GCP Cloud KMS, Azure Key Vault); rotate the key through the provider's KMS console or API instead, and skip to the Verification section.

This is a planned operator procedure rather than an alert response. Run it on the rotation schedule the deployment's key-management policy sets, or immediately when the current key is suspected compromised.

The rotation is a four-stage edit to the provider list. The provider list is ordered: the apiserver encrypts with the first provider and decrypts by trying each provider in order. A new key is therefore added as the second-position aescbc provider first (so it can decrypt nothing yet but is present), promoted to first position (so new writes use it), used to rewrite every Secret, and only then is the old key removed.

## Trigger

- A scheduled key-rotation interval defined by the deployment's key-management policy has elapsed.
- The current aescbc key is suspected compromised (etcd snapshot exposure, backup leak, or node compromise).
- An operator has staged a new key in `etcdEncryption.aescbcKeys` and the existing Secrets still hold ciphertext under the previous key.

## Diagnosis

### Step 1 — Read the active EncryptionConfiguration

<!-- access: kubectl requires=cluster-access -->
```bash
kubectl -n lenny-system get configmap lenny-etcd-encryption \
  -o jsonpath='{.data.encryption-config\.yaml}'
```

Confirm the document lists an `aescbc` provider before the `identity` provider, and note the `name` of each key under `aescbc.keys`. The key in first position is the one new writes are encrypted with.

### Step 2 — Confirm the apiserver is using the config

<!-- access: kubectl requires=cluster-access -->
```bash
kubectl -n kube-system get pod -l component=kube-apiserver \
  -o yaml | grep -- "--encryption-provider-config"
```

Every control-plane apiserver must reference the same `--encryption-provider-config` path. If any apiserver is missing the flag, the cluster is not uniformly encrypting Secrets; resolve that before rotating.

### Step 3 — Verify the current key actually encrypts Secrets

<!-- access: kubectl requires=cluster-access -->
```bash
kubectl -n kube-system get pod -l component=etcd \
  -o name | head -n1
```

Pick one etcd pod, then read a known Secret's raw value straight from etcd and confirm it begins with the `k8s:enc:aescbc:` prefix:

<!-- access: kubectl requires=cluster-access -->
```bash
kubectl -n kube-system exec <etcd-pod> -- sh -c '
  ETCDCTL_API=3 etcdctl \
    --cacert=/etc/kubernetes/pki/etcd/ca.crt \
    --cert=/etc/kubernetes/pki/etcd/server.crt \
    --key=/etc/kubernetes/pki/etcd/server.key \
    get /registry/secrets/lenny-system/ --prefix --keys-only' | head
```

A value that begins with `k8s:enc:aescbc:v1:` confirms encryption is active. A value that begins with `k8s:enc:identity:` or with plaintext `{` confirms the Secret is not yet encrypted under aescbc and must be rewritten (Remediation Step 4 covers the rewrite).

## Remediation

Stage the rotation as an ordered edit to the provider list. Apply each stage to the chart values, re-render, and propagate the EncryptionConfiguration to every control-plane node before moving to the next stage.

### Step 1 — Add the new key in second position

Generate a fresh 32-byte key:

<!-- access: kubectl requires=cluster-access -->
```bash
head -c 32 /dev/urandom | base64
```

Add it to `etcdEncryption.aescbcKeys` as the second entry, leaving the existing key first. The apiserver still encrypts with the old key; the new key is present only so every apiserver can decrypt data once the keys are reordered.

```yaml
etcdEncryption:
  aescbcKeys:
    - name: key1
      secret: <existing-key>
    - name: key2
      secret: <new-key-from-the-command-above>
```

<!-- access: kubectl requires=cluster-access -->
```bash
helm upgrade lenny lennylabs/lenny -f values.yaml
```

Extract the re-rendered document onto every control-plane node and restart each apiserver so all of them hold both keys:

<!-- access: kubectl requires=cluster-access -->
```bash
kubectl -n lenny-system get configmap lenny-etcd-encryption \
  -o jsonpath='{.data.encryption-config\.yaml}' \
  > /etc/kubernetes/enc/encryption-config.yaml
```

### Step 2 — Wait until every apiserver holds both keys

<!-- access: kubectl requires=cluster-access -->
```bash
kubectl -n kube-system get pod -l component=kube-apiserver \
  -o wide
```

All apiserver pods must be `Running` and `Ready` after the restart. Do not proceed until every control-plane apiserver has reloaded the two-key configuration; a stale apiserver that lacks `key2` cannot decrypt data written by an apiserver that has already promoted it.

### Step 3 — Promote the new key to first position

Reorder `etcdEncryption.aescbcKeys` so the new key is first. New writes now encrypt under `key2`; the old `key1` remains only for decrypting Secrets not yet rewritten.

```yaml
etcdEncryption:
  aescbcKeys:
    - name: key2
      secret: <new-key>
    - name: key1
      secret: <existing-key>
```

<!-- access: kubectl requires=cluster-access -->
```bash
helm upgrade lenny lennylabs/lenny -f values.yaml
```

Propagate the re-rendered document to every control-plane node and restart each apiserver again, as in Step 1.

### Step 4 — Rewrite every Secret under the new key

A Secret keeps its old ciphertext until it is next written. Force a rewrite of every Secret in the cluster so each is re-encrypted under the first-position key:

<!-- access: kubectl requires=cluster-access -->
```bash
kubectl get secrets --all-namespaces -o json | kubectl replace -f -
```

For a large cluster, rewrite namespace by namespace to bound the apiserver write load:

<!-- access: kubectl requires=cluster-access -->
```bash
for ns in $(kubectl get ns -o jsonpath='{.items[*].metadata.name}'); do
  kubectl -n "$ns" get secrets -o json | kubectl -n "$ns" replace -f -
done
```

### Step 5 — Remove the old key

Once every Secret has been rewritten (Verification confirms this), drop the old key from `etcdEncryption.aescbcKeys`, leaving only the new key.

```yaml
etcdEncryption:
  aescbcKeys:
    - name: key2
      secret: <new-key>
```

<!-- access: kubectl requires=cluster-access -->
```bash
helm upgrade lenny lennylabs/lenny -f values.yaml
```

Propagate the re-rendered document to every control-plane node and restart each apiserver a final time. The old key is now gone; any Secret still encrypted under it would become unreadable, which is why the rewrite in Step 4 must complete before this step runs.

## Verification

### Step 1 — Confirm Secrets are encrypted under the new key

<!-- access: kubectl requires=cluster-access -->
```bash
kubectl -n kube-system exec <etcd-pod> -- sh -c '
  ETCDCTL_API=3 etcdctl \
    --cacert=/etc/kubernetes/pki/etcd/ca.crt \
    --cert=/etc/kubernetes/pki/etcd/server.crt \
    --key=/etc/kubernetes/pki/etcd/server.key \
    get /registry/secrets/lenny-system/<a-known-secret>'
```

The raw value must begin with `k8s:enc:aescbc:v1:key2:` and must not contain the plaintext Secret data. If the prefix still names `key1`, the rewrite in Remediation Step 4 did not cover that namespace; rerun it before removing the old key.

### Step 2 — Confirm the apiservers are healthy

<!-- access: kubectl requires=cluster-access -->
```bash
kubectl -n kube-system get pod -l component=kube-apiserver
kubectl create secret generic etcd-rotation-test \
  --from-literal=ok=true -n lenny-system
kubectl delete secret etcd-rotation-test -n lenny-system
```

Every apiserver pod is `Running` and `Ready`, and the create-then-delete of a test Secret succeeds, which confirms the cluster can encrypt and decrypt under the rotated key list.

### Step 3 — Confirm the chart and cluster agree

<!-- access: kubectl requires=cluster-access -->
```bash
kubectl -n lenny-system get configmap lenny-etcd-encryption \
  -o jsonpath='{.data.encryption-config\.yaml}'
```

The rendered EncryptionConfiguration lists only the new key under `aescbc.keys`, and the `identity` provider remains last as the decrypt fallback for any resource type not covered by the `secrets` rule.

## Escalation

Escalate to:

- **Cluster admin / SRE** when a control-plane apiserver fails to restart after the EncryptionConfiguration is propagated, or when `kubectl get secrets` returns a decrypt error mid-rotation. A decrypt error means an apiserver is missing a key that another apiserver has already used to encrypt; restore the prior provider list on every node and restart before retrying.
- **Cloud provider support** for managed Kubernetes (EKS, GKE, AKS) where the apiserver flags and the KMS integration are provider-controlled. Custom `--encryption-provider-config` is not accepted on those clusters; rotation goes through the provider's KMS key-rotation surface.
- **Security on-call** when the rotation is driven by a suspected key compromise. Treat exposed etcd snapshots or backups taken before the rotation as still readable under the old key, and follow `credential-revocation` for any credential-pool API keys whose backing Secret was in scope.
