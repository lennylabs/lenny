---
code: KPCLAIM
type: interface
summary: The PodClaim custom resource.
---

# `PodClaim`

<a id="custom-resource-podclaim"></a>

```lenny-def
kind: custom-resource
name: PodClaim
group: lenny.dev
version: v1alpha1
scope: Namespaced (agent namespace)
plural: podclaims
crd: podclaims.lenny.dev
short-name: pclaim
writers: [GWCLAIM]
readers: [WPC]
summary: The gateway's claim on one agent pod.
```

## Operations

<a id="kpclaim-create"></a>

```lenny-def
kind: operation
name: create
method: POST
path: podclaims
returns: [pool name invalid]
requirements: [POOL-R1]
summary: Creates a claim.
```

## Events

<a id="event-dev-lenny-claim-bound"></a>

```lenny-def
kind: event
type: dev.lenny.claim.bound
emitter: GWCLAIM
payload: The claim's name and the pod it binds.
summary: A claim was bound.
```
