---
layout: default
title: Installing via krew
parent: "Operator Guide"
nav_order: 14
---

# Installing `lenny-ctl` via krew

`lenny-ctl` is published both as a standalone binary and as a [kubectl plugin](https://kubernetes.io/docs/tasks/extend-kubectl/kubectl-plugins/) distributed through the [krew plugin index](https://krew.sigs.k8s.io/). Both forms use the same flags, subcommands, environment variables, and output formats — `kubectl lenny <subcommand>` is exactly equivalent to `lenny-ctl <subcommand>`.

Use the krew form when you already rely on `kubectl` as your primary cluster interaction tool. Use the standalone binary for CI/CD pipelines, air-gapped environments, and any context where installing plugins through `kubectl` is inconvenient.

---

## Prerequisites

- `kubectl` 1.24 or later.
- [krew](https://krew.sigs.k8s.io/docs/user-guide/setup/install/) installed and configured on your PATH.

Verify:

```
kubectl krew version
```

---

## Installation

```
kubectl krew update
kubectl krew install lenny
```

Verify:

```
kubectl lenny --version
```

---

## Discovery

```
kubectl krew search lenny
kubectl krew info lenny
```

---

## Upgrade

```
kubectl krew upgrade lenny
```

---

## Invocation

Every subcommand from [lenny-ctl](lenny-ctl.md) works identically under `kubectl lenny`:

```
kubectl lenny admin pools list
kubectl lenny admin sessions get sess_abc123
kubectl lenny preflight --config values.yaml
kubectl lenny audit query --since 24h
```

---

## Authentication

`kubectl-lenny` does not derive the Lenny API URL from the active `kubectl` context. The `kubectl` context identifies the Kubernetes cluster; Lenny may live in any namespace or be exposed on any Ingress hostname within that cluster. Configure the Lenny API endpoint explicitly:

```
export LENNY_API_URL=https://lenny.example.com
export LENNY_API_TOKEN=$(kubectl get secret lenny-admin-token -n lenny-system -o jsonpath='{.data.token}' | base64 -d)

kubectl lenny admin tenants list
```

Or pass `--api-url` and `--token` on every invocation. See [lenny-ctl -- global flags](lenny-ctl.md).

For convenience, you can wrap these into a per-context shell function:

```bash
lenny() {
  local ctx=$(kubectl config current-context)
  case "$ctx" in
    prod-cluster)  export LENNY_API_URL=https://lenny.prod.example.com ;;
    staging)       export LENNY_API_URL=https://lenny.staging.example.com ;;
  esac
  kubectl lenny "$@"
}
```

---

## Uninstall

```
kubectl krew uninstall lenny
```

---

## Release sync

The krew index is updated on every tagged Lenny release. The standalone binary and the kubectl plugin share a single release tag and identical checksums — you can compare `lenny-ctl --version` with `kubectl lenny --version` to confirm they match.
