---
code: GWCLAIM
type: component
summary: The gateway part that claims pods.
---

# Pod claimer

## Registers

<a id="register-slot-set"></a>

```lenny-def
kind: register
name: slot set
identifier: REG-SLOTSET
store: Redis
keys: ["lenny:pod:{pod_id}:active_slots"]
members: ["<sessionId>"]
writers: [GWCLAIM]
readers: [GWCLAIM]
summary: A pod's slot occupancy.
```

## Deployment

<a id="rbac-grant-podclaims-write"></a>

```lenny-def
kind: rbac-grant
name: podclaims write
unit: GW
api-groups: [lenny.dev]
resources: [podclaims, podclaims/status]
verbs: [create, get, patch]
namespace-scope: agent namespace
requirement: POOL-R1
summary: The claimer creates and binds claims.
```

<a id="network-rule-claimer-to-api-server"></a>

```lenny-def
kind: network-rule
name: claimer to API server
unit: GW
direction: egress
peer: kube-apiserver
ports: [443/TCP]
requirement: POOL-R1
```

## Settings read

<!-- generated:settings-read -->
<!-- /generated:settings-read -->

## Metrics

<!-- generated:metrics-emitted -->
<!-- /generated:metrics-emitted -->

## Events

<!-- generated:events-emitted -->
<!-- /generated:events-emitted -->
