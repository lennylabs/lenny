---
code: KPCLAIM
type: interface
summary: >-
  The PodClaim custom resource and its operations.
---

# PodClaim

## Resource {#resource}

```lenny-def
kind: custom-resource
name: PodClaim
group: lenny.dev
```

## Operations {#operations}

<a id="kpclaim-create"></a>

### `KPCLAIM-create` {#create}

Creates a claim for [CLAIM-R1](../../capabilities/pools/pod-claims.md#claim-r1).

<a id="kpclaim-updatestatus"></a>

### `KPCLAIM-updateStatus` {#update-status}

Writes the binding state after [`KPCLAIM-create`](#kpclaim-create).
